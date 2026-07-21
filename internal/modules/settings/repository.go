package settings

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository interface {
	List(ctx context.Context) ([]Setting, error)
	Get(ctx context.Context, key string) (Setting, error)
	Upsert(ctx context.Context, s Setting) (Setting, error)
}

type postgresRepository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) Repository {
	return &postgresRepository{pool: pool}
}

const columns = `key, value, updated_at, coalesce(updated_by::text, '')`

func scan(row pgx.Row) (Setting, error) {
	var s Setting
	err := row.Scan(&s.Key, &s.Value, &s.UpdatedAt, &s.UpdatedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return Setting{}, ErrNotFound
	}
	if err != nil {
		return Setting{}, fmt.Errorf("settings: scan: %w", err)
	}
	return s, nil
}

func (r *postgresRepository) List(ctx context.Context) ([]Setting, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+columns+` FROM settings ORDER BY key`)
	if err != nil {
		return nil, fmt.Errorf("settings: list: %w", err)
	}
	defer rows.Close()
	var out []Setting
	for rows.Next() {
		s, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *postgresRepository) Get(ctx context.Context, key string) (Setting, error) {
	return scan(r.pool.QueryRow(ctx, `SELECT `+columns+` FROM settings WHERE key = $1`, key))
}

func (r *postgresRepository) Upsert(ctx context.Context, s Setting) (Setting, error) {
	var updatedBy *string
	if s.UpdatedBy != "" {
		updatedBy = &s.UpdatedBy
	}
	return scan(r.pool.QueryRow(ctx, `
		INSERT INTO settings (key, value, updated_by) VALUES ($1, $2, $3::uuid)
		ON CONFLICT (key) DO UPDATE SET value = $2, updated_at = now(), updated_by = $3::uuid
		RETURNING `+columns,
		s.Key, s.Value, updatedBy))
}
