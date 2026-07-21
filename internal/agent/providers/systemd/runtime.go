package systemd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strconv"
	"time"

	"github.com/runix/runix/internal/agent/providers/execx"
	rt "github.com/runix/runix/internal/domain/runtime"
)

type unitRuntime struct {
	unit string
}

func (u *unitRuntime) ID() rt.ID     { return rt.ID(u.unit) }
func (u *unitRuntime) Type() rt.Type { return rt.TypeSystemd }

func (u *unitRuntime) Status(ctx context.Context) (rt.Status, error) {
	props, err := showUnit(ctx, u.unit,
		"ActiveState", "SubState", "ExecMainStatus", "ExecMainStartTimestamp",
		"ExecMainExitTimestamp", "NRestarts", "UnitFileState", "Result")
	if err != nil {
		return rt.Status{}, err
	}
	status := rt.Status{
		State:   mapState(props["ActiveState"], props["SubState"]),
		Health:  rt.HealthUnknown,
		Message: props["ActiveState"] + " (" + props["SubState"] + "), " + props["UnitFileState"],
	}
	if n, err := strconv.Atoi(props["NRestarts"]); err == nil {
		status.RestartCount = n
	}
	if code, err := strconv.Atoi(props["ExecMainStatus"]); err == nil && status.State == rt.StateStopped {
		status.ExitCode = &code
	}
	status.StartedAt = parseSystemdTimestamp(props["ExecMainStartTimestamp"])
	status.FinishedAt = parseSystemdTimestamp(props["ExecMainExitTimestamp"])
	return status, nil
}

func (u *unitRuntime) systemctl(ctx context.Context, verb string, extra ...string) error {
	args := append([]string{verb}, extra...)
	args = append(args, u.unit)
	if _, err := execx.Run(ctx, "systemctl", args...); err != nil {
		return fmt.Errorf("systemd: %s %s: %w", verb, u.unit, err)
	}
	return nil
}

func (u *unitRuntime) Start(ctx context.Context) error {
	return u.systemctl(ctx, "start")
}

func (u *unitRuntime) Stop(ctx context.Context, _ rt.StopOptions) error {
	return u.systemctl(ctx, "stop")
}

func (u *unitRuntime) Restart(ctx context.Context, _ rt.StopOptions) error {
	return u.systemctl(ctx, "restart")
}

func (u *unitRuntime) Reload(ctx context.Context) error {
	return u.systemctl(ctx, "reload")
}

func (u *unitRuntime) Kill(ctx context.Context, signal string) error {
	if signal == "" {
		signal = "SIGKILL"
	}
	return u.systemctl(ctx, "kill", "--signal="+signal)
}

// InvokeAction covers unit-file level operations beyond the shared
// lifecycle verbs.
func (u *unitRuntime) InvokeAction(ctx context.Context, action string) error {
	allowed := []string{"enable", "disable", "mask", "unmask", "daemon-reload"}
	if !slices.Contains(allowed, action) {
		return fmt.Errorf("%w: systemd action %q", rt.ErrNotSupported, action)
	}
	if action == "daemon-reload" {
		_, err := execx.Run(ctx, "systemctl", "daemon-reload")
		return err
	}
	return u.systemctl(ctx, action)
}

func (u *unitRuntime) CheckHealth(ctx context.Context) (rt.Health, error) {
	status, err := u.Status(ctx)
	if err != nil {
		return rt.HealthUnknown, err
	}
	switch status.State {
	case rt.StateRunning:
		return rt.HealthHealthy, nil
	case rt.StateFailed:
		return rt.HealthUnhealthy, nil
	}
	return rt.HealthUnknown, nil
}

func (u *unitRuntime) Inspect(ctx context.Context) (json.RawMessage, error) {
	props, err := showUnit(ctx, u.unit)
	if err != nil {
		return nil, err
	}
	return json.Marshal(props)
}

// Metrics reads the unit's cgroup accounting via systemctl show.
func (u *unitRuntime) Metrics(ctx context.Context) (rt.Metrics, error) {
	props, err := showUnit(ctx, u.unit, "MemoryCurrent", "TasksCurrent")
	if err != nil {
		return rt.Metrics{}, err
	}
	m := rt.Metrics{CollectedAt: time.Now()}
	if v, err := strconv.ParseUint(props["MemoryCurrent"], 10, 64); err == nil {
		m.Memory.UsageBytes = v
	}
	if v, err := strconv.ParseUint(props["TasksCurrent"], 10, 64); err == nil {
		m.PIDs = v
	}
	return m, nil
}

func (u *unitRuntime) Logs(ctx context.Context, opts rt.LogOptions) (rt.LogStream, error) {
	args := []string{"-u", u.unit, "--no-pager", "-o", "json"}
	if opts.Tail > 0 {
		args = append(args, "-n", strconv.Itoa(opts.Tail))
	} else {
		args = append(args, "-n", "200")
	}
	if opts.Follow {
		args = append(args, "-f")
	}
	streamCtx, cancel := context.WithCancel(ctx)
	pipe, wait, err := execx.Stream(streamCtx, "journalctl", args...)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("systemd: journalctl: %w", err)
	}
	return &journalStream{
		pipe:    pipe,
		scanner: bufio.NewScanner(pipe),
		wait:    wait,
		cancel:  cancel,
	}, nil
}

type journalStream struct {
	pipe    io.ReadCloser
	scanner *bufio.Scanner
	wait    func() error
	cancel  context.CancelFunc
}

type journalRecord struct {
	Message   any    `json:"MESSAGE"`
	Timestamp string `json:"__REALTIME_TIMESTAMP"`
	Priority  string `json:"PRIORITY"`
}

func (j *journalStream) Next(ctx context.Context) (rt.LogEntry, error) {
	if err := ctx.Err(); err != nil {
		return rt.LogEntry{}, err
	}
	if !j.scanner.Scan() {
		if err := j.scanner.Err(); err != nil && ctx.Err() == nil {
			return rt.LogEntry{}, err
		}
		return rt.LogEntry{}, io.EOF
	}
	var rec journalRecord
	if err := json.Unmarshal(j.scanner.Bytes(), &rec); err != nil {
		return rt.LogEntry{Source: rt.LogSourceSystem, Line: append([]byte{}, j.scanner.Bytes()...)}, nil
	}
	entry := rt.LogEntry{Source: rt.LogSourceStdout, Line: []byte(journalMessage(rec.Message))}
	if usec, err := strconv.ParseInt(rec.Timestamp, 10, 64); err == nil {
		entry.Timestamp = time.UnixMicro(usec)
	}
	if rec.Priority != "" {
		if p, err := strconv.Atoi(rec.Priority); err == nil && p <= 3 {
			entry.Source = rt.LogSourceStderr
		}
	}
	return entry, nil
}

// journalMessage handles journald's two encodings: plain string, or byte
// array when the message is not valid UTF-8.
func journalMessage(v any) string {
	switch m := v.(type) {
	case string:
		return m
	case []any:
		b := make([]byte, 0, len(m))
		for _, x := range m {
			if f, ok := x.(float64); ok {
				b = append(b, byte(int(f)))
			}
		}
		return string(b)
	}
	return ""
}

func (j *journalStream) Close() error {
	j.cancel()
	_ = j.pipe.Close()
	_ = j.wait()
	return nil
}
