package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository interface {
	CreateSession(ctx context.Context, s Session) (Session, error)
	SessionByRefreshHash(ctx context.Context, hash []byte) (Session, error)
	SessionByID(ctx context.Context, id string) (Session, error)
	SessionsOfUser(ctx context.Context, userID string) ([]Session, error)
	TouchSession(ctx context.Context, id string) error
	RevokeSession(ctx context.Context, id, replacedBy string) error
	RevokeAllSessions(ctx context.Context, userID string) error
	DeleteExpiredSessions(ctx context.Context, before time.Time) (int64, error)

	ReplaceRecoveryCodes(ctx context.Context, userID string, hashes [][]byte) error
	// ConsumeRecoveryCode burns one unused matching code, reporting whether
	// one matched.
	ConsumeRecoveryCode(ctx context.Context, userID string, hash []byte) (bool, error)

	CreatePAT(ctx context.Context, t PAT) (PAT, error)
	PATByHash(ctx context.Context, hash []byte) (PAT, error)
	PATsOfUser(ctx context.Context, userID string) ([]PAT, error)
	TouchPAT(ctx context.Context, id string) error
	RevokePAT(ctx context.Context, userID, id string) error
}

type postgresRepository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) Repository {
	return &postgresRepository{pool: pool}
}

const sessionColumns = `id::text, user_id::text, refresh_hash, user_agent, ip, remember,
	created_at, last_used_at, expires_at, revoked_at, coalesce(replaced_by::text, '')`

func scanSession(row pgx.Row) (Session, error) {
	var s Session
	err := row.Scan(&s.ID, &s.UserID, &s.RefreshHash, &s.UserAgent, &s.IP, &s.Remember,
		&s.CreatedAt, &s.LastUsedAt, &s.ExpiresAt, &s.RevokedAt, &s.ReplacedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrSessionInvalid
	}
	if err != nil {
		return Session{}, fmt.Errorf("auth: scan session: %w", err)
	}
	return s, nil
}

func (r *postgresRepository) CreateSession(ctx context.Context, s Session) (Session, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO sessions (user_id, refresh_hash, user_agent, ip, remember, expires_at)
		VALUES ($1::uuid, $2, $3, $4, $5, $6)
		RETURNING `+sessionColumns,
		s.UserID, s.RefreshHash, s.UserAgent, s.IP, s.Remember, s.ExpiresAt)
	return scanSession(row)
}

func (r *postgresRepository) SessionByRefreshHash(ctx context.Context, hash []byte) (Session, error) {
	return scanSession(r.pool.QueryRow(ctx,
		`SELECT `+sessionColumns+` FROM sessions WHERE refresh_hash = $1`, hash))
}

func (r *postgresRepository) SessionByID(ctx context.Context, id string) (Session, error) {
	return scanSession(r.pool.QueryRow(ctx,
		`SELECT `+sessionColumns+` FROM sessions WHERE id = $1::uuid`, id))
}

func (r *postgresRepository) SessionsOfUser(ctx context.Context, userID string) ([]Session, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+sessionColumns+` FROM sessions
		WHERE user_id = $1::uuid AND revoked_at IS NULL AND expires_at > now()
		ORDER BY last_used_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("auth: sessions of user: %w", err)
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *postgresRepository) TouchSession(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE sessions SET last_used_at = now() WHERE id = $1::uuid`, id)
	if err != nil {
		return fmt.Errorf("auth: touch session: %w", err)
	}
	return nil
}

func (r *postgresRepository) RevokeSession(ctx context.Context, id, replacedBy string) error {
	var rb *string
	if replacedBy != "" {
		rb = &replacedBy
	}
	_, err := r.pool.Exec(ctx, `
		UPDATE sessions SET revoked_at = now(), replaced_by = $2::uuid
		WHERE id = $1::uuid AND revoked_at IS NULL`, id, rb)
	if err != nil {
		return fmt.Errorf("auth: revoke session: %w", err)
	}
	return nil
}

func (r *postgresRepository) RevokeAllSessions(ctx context.Context, userID string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE sessions SET revoked_at = now()
		WHERE user_id = $1::uuid AND revoked_at IS NULL`, userID)
	if err != nil {
		return fmt.Errorf("auth: revoke all sessions: %w", err)
	}
	return nil
}

func (r *postgresRepository) DeleteExpiredSessions(ctx context.Context, before time.Time) (int64, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("auth: delete expired sessions: %w", err)
	}
	return tag.RowsAffected(), nil
}

func (r *postgresRepository) ReplaceRecoveryCodes(ctx context.Context, userID string, hashes [][]byte) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("auth: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM recovery_codes WHERE user_id = $1::uuid`, userID); err != nil {
		return fmt.Errorf("auth: clear recovery codes: %w", err)
	}
	for _, h := range hashes {
		if _, err := tx.Exec(ctx,
			`INSERT INTO recovery_codes (user_id, code_hash) VALUES ($1::uuid, $2)`, userID, h); err != nil {
			return fmt.Errorf("auth: insert recovery code: %w", err)
		}
	}
	return tx.Commit(ctx)
}

func (r *postgresRepository) ConsumeRecoveryCode(ctx context.Context, userID string, hash []byte) (bool, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE recovery_codes SET used_at = now()
		WHERE id IN (
			SELECT id FROM recovery_codes
			WHERE user_id = $1::uuid AND code_hash = $2 AND used_at IS NULL
			LIMIT 1
		)`, userID, hash)
	if err != nil {
		return false, fmt.Errorf("auth: consume recovery code: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

const patColumns = `id::text, user_id::text, name, token_hash, created_at, last_used_at, expires_at, revoked_at`

func scanPAT(row pgx.Row) (PAT, error) {
	var t PAT
	err := row.Scan(&t.ID, &t.UserID, &t.Name, &t.TokenHash,
		&t.CreatedAt, &t.LastUsedAt, &t.ExpiresAt, &t.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return PAT{}, ErrNotFound
	}
	if err != nil {
		return PAT{}, fmt.Errorf("auth: scan pat: %w", err)
	}
	return t, nil
}

func (r *postgresRepository) CreatePAT(ctx context.Context, t PAT) (PAT, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO personal_access_tokens (user_id, name, token_hash, expires_at)
		VALUES ($1::uuid, $2, $3, $4)
		RETURNING `+patColumns,
		t.UserID, t.Name, t.TokenHash, t.ExpiresAt)
	created, err := scanPAT(row)
	if err != nil && isUniqueViolation(err) {
		return PAT{}, fmt.Errorf("%w: token name %q", ErrConflict, t.Name)
	}
	return created, err
}

func (r *postgresRepository) PATByHash(ctx context.Context, hash []byte) (PAT, error) {
	return scanPAT(r.pool.QueryRow(ctx,
		`SELECT `+patColumns+` FROM personal_access_tokens WHERE token_hash = $1`, hash))
}

func (r *postgresRepository) PATsOfUser(ctx context.Context, userID string) ([]PAT, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+patColumns+` FROM personal_access_tokens
		WHERE user_id = $1::uuid AND revoked_at IS NULL
		ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("auth: pats of user: %w", err)
	}
	defer rows.Close()
	var out []PAT
	for rows.Next() {
		t, err := scanPAT(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *postgresRepository) TouchPAT(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE personal_access_tokens SET last_used_at = now() WHERE id = $1::uuid`, id)
	if err != nil {
		return fmt.Errorf("auth: touch pat: %w", err)
	}
	return nil
}

func (r *postgresRepository) RevokePAT(ctx context.Context, userID, id string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE personal_access_tokens SET revoked_at = now()
		WHERE id = $1::uuid AND user_id = $2::uuid AND revoked_at IS NULL`, id, userID)
	if err != nil {
		return fmt.Errorf("auth: revoke pat: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
