package users

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/runix/runix/internal/platform/httpx"
)

type Repository interface {
	Create(ctx context.Context, u User) (User, error)
	GetByID(ctx context.Context, id string) (User, error)
	// GetByIdentifier resolves a username or email (case-insensitive).
	GetByIdentifier(ctx context.Context, identifier string) (User, error)
	List(ctx context.Context, page httpx.Page) ([]User, int64, error)
	Update(ctx context.Context, u User) (User, error)
	UpdatePassword(ctx context.Context, id, hash string, mustChange bool) error
	SetTOTP(ctx context.Context, id string, enabled bool, secretEnc string) error
	Delete(ctx context.Context, id string) error
	CountActiveWithRole(ctx context.Context, roleKey string) (int, error)
	Any(ctx context.Context) (bool, error)
}

type postgresRepository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) Repository {
	return &postgresRepository{pool: pool}
}

const userColumns = `id::text, username::text, email::text, display_name, password_hash,
	is_active, is_system, must_change_password, totp_enabled, coalesce(totp_secret_enc, ''),
	created_at, updated_at`

func scanUser(row pgx.Row) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Username, &u.Email, &u.DisplayName, &u.PasswordHash,
		&u.IsActive, &u.IsSystem, &u.MustChangePassword, &u.TOTPEnabled, &u.TOTPSecretEnc,
		&u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("users: scan: %w", err)
	}
	return u, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func (r *postgresRepository) Create(ctx context.Context, u User) (User, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO users (username, email, display_name, password_hash, is_active, is_system, must_change_password)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING `+userColumns,
		u.Username, u.Email, u.DisplayName, u.PasswordHash, u.IsActive, u.IsSystem, u.MustChangePassword)
	created, err := scanUser(row)
	if isUniqueViolation(err) {
		return User{}, ErrConflict
	}
	return created, err
}

func (r *postgresRepository) GetByID(ctx context.Context, id string) (User, error) {
	return scanUser(r.pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1::uuid`, id))
}

func (r *postgresRepository) GetByIdentifier(ctx context.Context, identifier string) (User, error) {
	return scanUser(r.pool.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users WHERE username = $1 OR email = $1`, identifier))
}

func (r *postgresRepository) List(ctx context.Context, page httpx.Page) ([]User, int64, error) {
	var total int64
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("users: count: %w", err)
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+userColumns+` FROM users ORDER BY username LIMIT $1 OFFSET $2`,
		page.Limit(), page.Offset())
	if err != nil {
		return nil, 0, fmt.Errorf("users: list: %w", err)
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, u)
	}
	return out, total, rows.Err()
}

func (r *postgresRepository) Update(ctx context.Context, u User) (User, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE users SET email = $2, display_name = $3, is_active = $4, updated_at = now()
		WHERE id = $1::uuid
		RETURNING `+userColumns,
		u.ID, u.Email, u.DisplayName, u.IsActive)
	updated, err := scanUser(row)
	if isUniqueViolation(err) {
		return User{}, ErrConflict
	}
	return updated, err
}

func (r *postgresRepository) UpdatePassword(ctx context.Context, id, hash string, mustChange bool) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE users SET password_hash = $2, must_change_password = $3, updated_at = now()
		WHERE id = $1::uuid`, id, hash, mustChange)
	if err != nil {
		return fmt.Errorf("users: update password: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *postgresRepository) SetTOTP(ctx context.Context, id string, enabled bool, secretEnc string) error {
	var secret *string
	if secretEnc != "" {
		secret = &secretEnc
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE users SET totp_enabled = $2, totp_secret_enc = $3, updated_at = now()
		WHERE id = $1::uuid`, id, enabled, secret)
	if err != nil {
		return fmt.Errorf("users: set totp: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *postgresRepository) Delete(ctx context.Context, id string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM users WHERE id = $1::uuid AND NOT is_system`, id)
	if err != nil {
		return fmt.Errorf("users: delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *postgresRepository) CountActiveWithRole(ctx context.Context, roleKey string) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `
		SELECT count(*) FROM users u
		JOIN user_roles ur ON ur.user_id = u.id
		JOIN roles r ON r.id = ur.role_id
		WHERE r.key = $1 AND u.is_active`, roleKey).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("users: count with role: %w", err)
	}
	return n, nil
}

func (r *postgresRepository) Any(ctx context.Context) (bool, error) {
	var exists bool
	if err := r.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users)`).Scan(&exists); err != nil {
		return false, fmt.Errorf("users: any: %w", err)
	}
	return exists, nil
}
