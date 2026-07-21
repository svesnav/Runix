package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/runix/runix/internal/modules/audit"
	"github.com/runix/runix/internal/platform/cron"
	"github.com/runix/runix/internal/protocol"
)

// Executor runs a task's work against an agent. The hub satisfies it; the
// interface keeps the scheduler testable without a live connection.
type Executor interface {
	Call(ctx context.Context, serverID, method string, params any) ([]byte, error)
}

const (
	tickInterval = 20 * time.Second
	runTimeout   = 5 * time.Minute
)

type Service struct {
	repo    Repository
	exec    Executor
	auditor *audit.Service
	log     *slog.Logger
}

func NewService(repo Repository, exec Executor, auditor *audit.Service, log *slog.Logger) *Service {
	return &Service{repo: repo, exec: exec, auditor: auditor, log: log}
}

type Input struct {
	Name        string
	Description string
	ServerID    string
	Kind        string
	Payload     Payload
	Cron        string
	Enabled     bool
}

func (s *Service) validate(in Input) (*cron.Schedule, error) {
	if strings.TrimSpace(in.Name) == "" || len(in.Name) > 128 {
		return nil, fmt.Errorf("%w: name must be 1-128 characters", ErrInvalid)
	}
	if in.ServerID == "" {
		return nil, fmt.Errorf("%w: a server is required", ErrInvalid)
	}
	schedule, err := cron.Parse(in.Cron)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	switch in.Kind {
	case KindRuntimeAction:
		if in.Payload.RuntimeType == "" || in.Payload.RuntimeID == "" || in.Payload.Action == "" {
			return nil, fmt.Errorf("%w: runtime type, id and action are required", ErrInvalid)
		}
	case KindRuntimeExec:
		if in.Payload.RuntimeType == "" || in.Payload.RuntimeID == "" || len(in.Payload.Cmd) == 0 {
			return nil, fmt.Errorf("%w: runtime type, id and command are required", ErrInvalid)
		}
	default:
		return nil, fmt.Errorf("%w: unknown task kind %q", ErrInvalid, in.Kind)
	}
	return schedule, nil
}

func (s *Service) Create(ctx context.Context, in Input, createdBy string) (Task, error) {
	schedule, err := s.validate(in)
	if err != nil {
		return Task{}, err
	}
	task := Task{
		Name: in.Name, Description: in.Description, ServerID: in.ServerID,
		Kind: in.Kind, Payload: in.Payload, Cron: in.Cron, Enabled: in.Enabled,
	}
	if in.Enabled {
		next := schedule.Next(time.Now())
		task.NextRunAt = &next
	}
	return s.repo.Create(ctx, task, createdBy)
}

func (s *Service) Update(ctx context.Context, id string, in Input) (Task, error) {
	schedule, err := s.validate(in)
	if err != nil {
		return Task{}, err
	}
	existing, err := s.repo.Get(ctx, id)
	if err != nil {
		return Task{}, err
	}
	existing.Name = in.Name
	existing.Description = in.Description
	existing.Kind = in.Kind
	existing.Payload = in.Payload
	existing.Cron = in.Cron
	existing.Enabled = in.Enabled
	if in.Enabled {
		next := schedule.Next(time.Now())
		existing.NextRunAt = &next
	} else {
		existing.NextRunAt = nil
	}
	return s.repo.Update(ctx, existing)
}

func (s *Service) Get(ctx context.Context, id string) (Task, error) { return s.repo.Get(ctx, id) }

func (s *Service) List(ctx context.Context, serverID string) ([]Task, error) {
	return s.repo.List(ctx, serverID)
}

func (s *Service) Delete(ctx context.Context, id string) error { return s.repo.Delete(ctx, id) }

func (s *Service) Runs(ctx context.Context, taskID string, limit int) ([]Run, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	return s.repo.Runs(ctx, taskID, limit)
}

// RunNow executes a task immediately without disturbing its schedule.
func (s *Service) RunNow(ctx context.Context, id string) (Run, error) {
	task, err := s.repo.Get(ctx, id)
	if err != nil {
		return Run{}, err
	}
	return s.execute(ctx, task, nil), nil
}

// Run drives the scheduling loop until ctx is canceled.
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	s.log.Info("scheduler started", "tick", tickInterval)
	for {
		select {
		case <-ticker.C:
			s.tick(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (s *Service) tick(ctx context.Context) {
	now := time.Now()
	due, err := s.repo.ClaimDue(ctx, now, func(t Task) time.Time {
		schedule, err := cron.Parse(t.Cron)
		if err != nil {
			// A task whose expression became invalid must not spin.
			s.log.Error("task has an invalid cron expression, pausing it",
				"task", t.ID, "cron", t.Cron, "err", err)
			return time.Time{}
		}
		return schedule.Next(now)
	})
	if err != nil {
		if ctx.Err() == nil {
			s.log.Error("claiming due tasks failed", "err", err)
		}
		return
	}
	for _, task := range due {
		go func(task Task) {
			runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), runTimeout)
			defer cancel()
			s.execute(runCtx, task, nil)
		}(task)
	}
}

// execute performs the task's work and records the outcome.
func (s *Service) execute(ctx context.Context, task Task, nextRun *time.Time) Run {
	started := time.Now()
	detail, err := s.dispatch(ctx, task)

	run := Run{
		TaskID: task.ID, StartedAt: started,
		DurationMs: int(time.Since(started).Milliseconds()),
		Status:     StatusSuccess, Detail: detail,
	}
	if err != nil {
		run.Status = StatusFailure
		run.Detail = err.Error()
	}

	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if recErr := s.repo.RecordRun(recordCtx, task.ID, run, nextRun); recErr != nil {
		s.log.Error("recording task run failed", "task", task.ID, "err", recErr)
	}
	s.auditor.Write(recordCtx, audit.Record{
		Action: "scheduler.run", TargetType: "scheduled_task", TargetID: task.Name,
		New: map[string]any{"server": task.ServerID, "kind": task.Kind, "status": run.Status},
		Err: err,
	})
	if err != nil {
		s.log.Warn("scheduled task failed", "task", task.Name, "err", err)
	} else {
		s.log.Info("scheduled task ran", "task", task.Name, "duration_ms", run.DurationMs)
	}
	return run
}

func (s *Service) dispatch(ctx context.Context, task Task) (string, error) {
	switch task.Kind {
	case KindRuntimeAction:
		_, err := s.exec.Call(ctx, task.ServerID, protocol.MethodRuntimeAction,
			protocol.RuntimeActionParams{
				Type:   task.Payload.RuntimeType,
				ID:     task.Payload.RuntimeID,
				Action: task.Payload.Action,
			})
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s %s/%s", task.Payload.Action, task.Payload.RuntimeType, task.Payload.RuntimeID), nil

	case KindRuntimeExec:
		raw, err := s.exec.Call(ctx, task.ServerID, protocol.MethodRuntimeExec,
			protocol.RuntimeExecParams{
				Type: task.Payload.RuntimeType,
				ID:   task.Payload.RuntimeID,
				Cmd:  task.Payload.Cmd,
			})
		if err != nil {
			return "", err
		}
		var result protocol.RuntimeExecResult
		if err := json.Unmarshal(raw, &result); err != nil {
			return "", fmt.Errorf("decode exec result: %w", err)
		}
		if result.ExitCode != 0 {
			return "", fmt.Errorf("command exited with code %d: %s",
				result.ExitCode, trim(string(result.Stderr), 200))
		}
		return trim(string(result.Stdout), 500), nil

	default:
		return "", fmt.Errorf("%w: unknown kind %q", ErrInvalid, task.Kind)
	}
}

func trim(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
