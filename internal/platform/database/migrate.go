package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log/slog"
	"regexp"
	"sort"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Forward-only embedded migrator. Each *.up.sql runs once, inside a
// transaction, recorded with a checksum so an edited historical migration
// fails loudly instead of silently diverging schemas. Down files exist in
// the repository for operator-driven rollback but are never auto-applied.

type migration struct {
	Version  int
	Name     string
	SQL      string
	Checksum string
}

var migrationFile = regexp.MustCompile(`^(\d{4})_([a-z0-9_]+)\.up\.sql$`)

func loadMigrations(fsys fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("database: read migrations: %w", err)
	}
	var out []migration
	seen := map[int]string{}
	for _, e := range entries {
		m := migrationFile.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		version, err := strconv.Atoi(m[1])
		if err != nil || version == 0 {
			return nil, fmt.Errorf("database: bad migration version in %q", e.Name())
		}
		if prev, dup := seen[version]; dup {
			return nil, fmt.Errorf("database: duplicate migration version %d (%s, %s)", version, prev, e.Name())
		}
		seen[version] = e.Name()
		sqlBytes, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			return nil, fmt.Errorf("database: read %s: %w", e.Name(), err)
		}
		sum := sha256.Sum256(sqlBytes)
		out = append(out, migration{
			Version:  version,
			Name:     m[2],
			SQL:      string(sqlBytes),
			Checksum: hex.EncodeToString(sum[:]),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	for i, m := range out {
		if m.Version != i+1 {
			return nil, fmt.Errorf("database: migration versions must be contiguous from 0001, missing %04d", i+1)
		}
	}
	return out, nil
}

func Migrate(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS, log *slog.Logger) error {
	migrations, err := loadMigrations(fsys)
	if err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    integer PRIMARY KEY,
			name       text NOT NULL,
			checksum   text NOT NULL,
			applied_at timestamptz NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("database: ensure schema_migrations: %w", err)
	}

	applied := map[int]string{}
	rows, err := pool.Query(ctx, `SELECT version, checksum FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("database: read schema_migrations: %w", err)
	}
	for rows.Next() {
		var v int
		var sum string
		if err := rows.Scan(&v, &sum); err != nil {
			rows.Close()
			return fmt.Errorf("database: scan schema_migrations: %w", err)
		}
		applied[v] = sum
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("database: iterate schema_migrations: %w", err)
	}

	for _, m := range migrations {
		if sum, done := applied[m.Version]; done {
			if sum != m.Checksum {
				return fmt.Errorf("database: migration %04d_%s changed after being applied (checksum mismatch)", m.Version, m.Name)
			}
			continue
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("database: begin migration %04d: %w", m.Version, err)
		}
		if _, err := tx.Exec(ctx, m.SQL); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("database: apply %04d_%s: %w", m.Version, m.Name, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO schema_migrations (version, name, checksum) VALUES ($1, $2, $3)`,
			m.Version, m.Name, m.Checksum); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("database: record %04d_%s: %w", m.Version, m.Name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("database: commit %04d_%s: %w", m.Version, m.Name, err)
		}
		log.Info("applied migration", "version", m.Version, "name", m.Name)
	}
	return nil
}
