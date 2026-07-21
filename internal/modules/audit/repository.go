package audit

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/runix/runix/internal/platform/httpx"
)

// Repository persists audit entries. The service owns this interface;
// postgresRepository is the production implementation.
type Repository interface {
	Insert(ctx context.Context, e Entry) error
	List(ctx context.Context, f Filter, page httpx.Page) ([]Entry, int64, error)
}

type postgresRepository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) Repository {
	return &postgresRepository{pool: pool}
}

func (r *postgresRepository) Insert(ctx context.Context, e Entry) error {
	var actorID *string
	if e.ActorID != "" {
		actorID = &e.ActorID
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO audit_logs
			(actor_id, actor_name, ip, user_agent, request_id, action,
			 target_type, target_id, old_value, new_value, result, error)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		actorID, e.ActorName, e.IP, e.UserAgent, e.RequestID, e.Action,
		e.TargetType, e.TargetID, e.OldValue, e.NewValue, e.Result, e.Error,
	)
	if err != nil {
		return fmt.Errorf("audit: insert: %w", err)
	}
	return nil
}

func (r *postgresRepository) List(ctx context.Context, f Filter, page httpx.Page) ([]Entry, int64, error) {
	where := []string{"true"}
	args := []any{}
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if f.ActorID != "" {
		where = append(where, "actor_id = "+arg(f.ActorID)+"::uuid")
	}
	if f.Action != "" {
		where = append(where, "action = "+arg(f.Action))
	}
	if f.TargetType != "" {
		where = append(where, "target_type = "+arg(f.TargetType))
	}
	if f.TargetID != "" {
		where = append(where, "target_id = "+arg(f.TargetID))
	}
	if !f.From.IsZero() {
		where = append(where, "ts >= "+arg(f.From))
	}
	if !f.To.IsZero() {
		where = append(where, "ts <= "+arg(f.To))
	}
	cond := strings.Join(where, " AND ")

	var total int64
	if err := r.pool.QueryRow(ctx, "SELECT count(*) FROM audit_logs WHERE "+cond, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("audit: count: %w", err)
	}

	query := fmt.Sprintf(`
		SELECT id, ts, coalesce(actor_id::text, ''), actor_name, ip, user_agent,
		       request_id, action, target_type, target_id, old_value, new_value, result, error
		FROM audit_logs WHERE %s
		ORDER BY ts DESC, id DESC
		LIMIT %s OFFSET %s`, cond, arg(page.Limit()), arg(page.Offset()))

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("audit: list: %w", err)
	}
	defer rows.Close()

	var out []Entry
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.Time, &e.ActorID, &e.ActorName, &e.IP, &e.UserAgent,
			&e.RequestID, &e.Action, &e.TargetType, &e.TargetID,
			&e.OldValue, &e.NewValue, &e.Result, &e.Error); err != nil {
			return nil, 0, fmt.Errorf("audit: scan: %w", err)
		}
		out = append(out, e)
	}
	return out, total, rows.Err()
}
