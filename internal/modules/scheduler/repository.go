package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository interface {
	Create(ctx context.Context, t Task, createdBy string) (Task, error)
	Update(ctx context.Context, t Task) (Task, error)
	Get(ctx context.Context, id string) (Task, error)
	List(ctx context.Context, serverID string) ([]Task, error)
	Delete(ctx context.Context, id string) error
	// ClaimDue atomically hands out tasks whose next run has passed,
	// advancing next_run_at so a second control-plane instance cannot run
	// the same task.
	ClaimDue(ctx context.Context, now time.Time, advance func(Task) time.Time) ([]Task, error)
	RecordRun(ctx context.Context, taskID string, run Run, nextRun *time.Time) error
	Runs(ctx context.Context, taskID string, limit int) ([]Run, error)
	SetNextRun(ctx context.Context, id string, next *time.Time) error
}

type postgresRepository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) Repository {
	return &postgresRepository{pool: pool}
}

const columns = `id::text, name, description, server_id::text, kind, payload, cron, enabled,
	next_run_at, last_run_at, last_status, last_error, created_at, updated_at`

func scanTask(row pgx.Row) (Task, error) {
	var t Task
	var payload []byte
	err := row.Scan(&t.ID, &t.Name, &t.Description, &t.ServerID, &t.Kind, &payload, &t.Cron,
		&t.Enabled, &t.NextRunAt, &t.LastRunAt, &t.LastStatus, &t.LastError, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	if err != nil {
		return Task{}, fmt.Errorf("scheduler: scan: %w", err)
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &t.Payload); err != nil {
			return Task{}, fmt.Errorf("scheduler: decode payload: %w", err)
		}
	}
	return t, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func (r *postgresRepository) Create(ctx context.Context, t Task, createdBy string) (Task, error) {
	payload, err := t.Payload.marshal()
	if err != nil {
		return Task{}, err
	}
	var creator *string
	if createdBy != "" {
		creator = &createdBy
	}
	row := r.pool.QueryRow(ctx, `
		INSERT INTO scheduled_tasks (name, description, server_id, kind, payload, cron, enabled, next_run_at, created_by)
		VALUES ($1, $2, $3::uuid, $4, $5, $6, $7, $8, $9::uuid)
		RETURNING `+columns,
		t.Name, t.Description, t.ServerID, t.Kind, payload, t.Cron, t.Enabled, t.NextRunAt, creator)
	created, err := scanTask(row)
	if isUniqueViolation(err) {
		return Task{}, ErrConflict
	}
	return created, err
}

func (r *postgresRepository) Update(ctx context.Context, t Task) (Task, error) {
	payload, err := t.Payload.marshal()
	if err != nil {
		return Task{}, err
	}
	row := r.pool.QueryRow(ctx, `
		UPDATE scheduled_tasks
		SET name = $2, description = $3, kind = $4, payload = $5, cron = $6,
		    enabled = $7, next_run_at = $8, updated_at = now()
		WHERE id = $1::uuid
		RETURNING `+columns,
		t.ID, t.Name, t.Description, t.Kind, payload, t.Cron, t.Enabled, t.NextRunAt)
	updated, err := scanTask(row)
	if isUniqueViolation(err) {
		return Task{}, ErrConflict
	}
	return updated, err
}

func (r *postgresRepository) Get(ctx context.Context, id string) (Task, error) {
	return scanTask(r.pool.QueryRow(ctx, `SELECT `+columns+` FROM scheduled_tasks WHERE id = $1::uuid`, id))
}

func (r *postgresRepository) List(ctx context.Context, serverID string) ([]Task, error) {
	query := `SELECT ` + columns + ` FROM scheduled_tasks`
	args := []any{}
	if serverID != "" {
		query += ` WHERE server_id = $1::uuid`
		args = append(args, serverID)
	}
	query += ` ORDER BY name`

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("scheduler: list: %w", err)
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *postgresRepository) Delete(ctx context.Context, id string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM scheduled_tasks WHERE id = $1::uuid`, id)
	if err != nil {
		return fmt.Errorf("scheduler: delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ClaimDue selects due tasks FOR UPDATE SKIP LOCKED and immediately moves
// their next_run_at forward, so exactly one instance picks up each task.
func (r *postgresRepository) ClaimDue(ctx context.Context, now time.Time, advance func(Task) time.Time) ([]Task, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("scheduler: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `
		SELECT `+columns+` FROM scheduled_tasks
		WHERE enabled AND next_run_at IS NOT NULL AND next_run_at <= $1
		ORDER BY next_run_at
		LIMIT 50
		FOR UPDATE SKIP LOCKED`, now)
	if err != nil {
		return nil, fmt.Errorf("scheduler: claim: %w", err)
	}
	var due []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		due = append(due, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, t := range due {
		next := advance(t)
		var nextPtr *time.Time
		if !next.IsZero() {
			nextPtr = &next
		}
		if _, err := tx.Exec(ctx,
			`UPDATE scheduled_tasks SET next_run_at = $2 WHERE id = $1::uuid`, t.ID, nextPtr); err != nil {
			return nil, fmt.Errorf("scheduler: advance: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("scheduler: commit claim: %w", err)
	}
	return due, nil
}

func (r *postgresRepository) RecordRun(ctx context.Context, taskID string, run Run, nextRun *time.Time) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("scheduler: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		INSERT INTO scheduled_task_runs (task_id, started_at, duration_ms, status, detail)
		VALUES ($1::uuid, $2, $3, $4, $5)`,
		taskID, run.StartedAt, run.DurationMs, run.Status, run.Detail); err != nil {
		return fmt.Errorf("scheduler: record run: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE scheduled_tasks
		SET last_run_at = $2, last_status = $3, last_error = $4,
		    next_run_at = COALESCE($5, next_run_at), updated_at = now()
		WHERE id = $1::uuid`,
		taskID, run.StartedAt, run.Status, run.Detail, nextRun); err != nil {
		return fmt.Errorf("scheduler: update task after run: %w", err)
	}
	// Keep only recent history per task.
	if _, err := tx.Exec(ctx, `
		DELETE FROM scheduled_task_runs
		WHERE task_id = $1::uuid AND id NOT IN (
			SELECT id FROM scheduled_task_runs WHERE task_id = $1::uuid
			ORDER BY started_at DESC LIMIT 50
		)`, taskID); err != nil {
		return fmt.Errorf("scheduler: prune runs: %w", err)
	}
	return tx.Commit(ctx)
}

func (r *postgresRepository) Runs(ctx context.Context, taskID string, limit int) ([]Run, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, task_id::text, started_at, duration_ms, status, detail
		FROM scheduled_task_runs WHERE task_id = $1::uuid
		ORDER BY started_at DESC LIMIT $2`, taskID, limit)
	if err != nil {
		return nil, fmt.Errorf("scheduler: runs: %w", err)
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		var run Run
		if err := rows.Scan(&run.ID, &run.TaskID, &run.StartedAt, &run.DurationMs,
			&run.Status, &run.Detail); err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

func (r *postgresRepository) SetNextRun(ctx context.Context, id string, next *time.Time) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE scheduled_tasks SET next_run_at = $2, updated_at = now() WHERE id = $1::uuid`, id, next)
	if err != nil {
		return fmt.Errorf("scheduler: set next run: %w", err)
	}
	return nil
}
