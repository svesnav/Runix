package servers

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/runix/runix/internal/platform/httpx"
)

type Repository interface {
	Create(ctx context.Context, s Server) (Server, error)
	GetByID(ctx context.Context, id string) (Server, error)
	GetByTokenHash(ctx context.Context, hash []byte) (Server, error)
	List(ctx context.Context, page httpx.Page) ([]Server, int64, error)
	ListAll(ctx context.Context) ([]Server, error)
	Update(ctx context.Context, s Server) (Server, error)
	UpdateTokenHash(ctx context.Context, id string, hash []byte) error
	ApplyAgentFacts(ctx context.Context, s Server) error
	SetConnectionStatus(ctx context.Context, id string, status ConnectionStatus) error
	// MarkAllOffline flips every online server to offline; run at boot so a
	// control-plane restart never leaves stale presence.
	MarkAllOffline(ctx context.Context) error
	RecordHeartbeat(ctx context.Context, id string, m MetricsPoint) error
	Delete(ctx context.Context, id string) error
	CountByStatus(ctx context.Context) (map[ConnectionStatus]int, error)

	CreateGroup(ctx context.Context, g Group) (Group, error)
	ListGroups(ctx context.Context) ([]Group, error)
	DeleteGroup(ctx context.Context, id string) error
	AddGroupMember(ctx context.Context, groupID, serverID string) error
	RemoveGroupMember(ctx context.Context, groupID, serverID string) error
	GroupIDsOfServer(ctx context.Context, serverID string) ([]string, error)
	ServerIDsOfGroup(ctx context.Context, groupID string) ([]string, error)

	Metrics(ctx context.Context, serverID string, from, to time.Time, limit int) ([]MetricsPoint, error)
	PruneMetrics(ctx context.Context, before time.Time) (int64, error)
}

type postgresRepository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) Repository {
	return &postgresRepository{pool: pool}
}

const serverColumns = `id::text, name, description, address, hostname, os, os_version, kernel_version,
	architecture, agent_version, location, tags, labels, agent_token_hash,
	cpu_cores, memory_bytes, swap_bytes, disk_bytes,
	docker_available, systemd_available, runtime_types, connection_status,
	last_heartbeat_at, last_seen_at, created_at, updated_at`

func scanServer(row pgx.Row) (Server, error) {
	var s Server
	err := row.Scan(&s.ID, &s.Name, &s.Description, &s.Address, &s.Hostname, &s.OS, &s.OSVersion,
		&s.KernelVersion, &s.Architecture, &s.AgentVersion, &s.Location, &s.Tags, &s.Labels,
		&s.AgentTokenHash, &s.CPUCores, &s.MemoryBytes, &s.SwapBytes, &s.DiskBytes,
		&s.DockerAvailable, &s.SystemdAvailable, &s.RuntimeTypes, &s.ConnectionStatus,
		&s.LastHeartbeatAt, &s.LastSeenAt, &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Server{}, ErrNotFound
	}
	if err != nil {
		return Server{}, fmt.Errorf("servers: scan: %w", err)
	}
	if s.Tags == nil {
		s.Tags = []string{}
	}
	if s.Labels == nil {
		s.Labels = map[string]string{}
	}
	if s.RuntimeTypes == nil {
		s.RuntimeTypes = []string{}
	}
	return s, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func (r *postgresRepository) Create(ctx context.Context, s Server) (Server, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO servers (name, description, address, location, tags, labels, agent_token_hash)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING `+serverColumns,
		s.Name, s.Description, s.Address, s.Location, s.Tags, s.Labels, s.AgentTokenHash)
	created, err := scanServer(row)
	if isUniqueViolation(err) {
		return Server{}, ErrConflict
	}
	return created, err
}

func (r *postgresRepository) GetByID(ctx context.Context, id string) (Server, error) {
	return scanServer(r.pool.QueryRow(ctx,
		`SELECT `+serverColumns+` FROM servers WHERE id = $1::uuid`, id))
}

func (r *postgresRepository) GetByTokenHash(ctx context.Context, hash []byte) (Server, error) {
	return scanServer(r.pool.QueryRow(ctx,
		`SELECT `+serverColumns+` FROM servers WHERE agent_token_hash = $1`, hash))
}

func (r *postgresRepository) List(ctx context.Context, page httpx.Page) ([]Server, int64, error) {
	var total int64
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM servers`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("servers: count: %w", err)
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+serverColumns+` FROM servers ORDER BY name LIMIT $1 OFFSET $2`,
		page.Limit(), page.Offset())
	if err != nil {
		return nil, 0, fmt.Errorf("servers: list: %w", err)
	}
	defer rows.Close()
	var out []Server
	for rows.Next() {
		s, err := scanServer(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, s)
	}
	return out, total, rows.Err()
}

func (r *postgresRepository) ListAll(ctx context.Context) ([]Server, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+serverColumns+` FROM servers ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("servers: list all: %w", err)
	}
	defer rows.Close()
	var out []Server
	for rows.Next() {
		s, err := scanServer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *postgresRepository) Update(ctx context.Context, s Server) (Server, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE servers SET name = $2, description = $3, address = $4, location = $5, tags = $6,
			labels = $7, updated_at = now()
		WHERE id = $1::uuid
		RETURNING `+serverColumns,
		s.ID, s.Name, s.Description, s.Address, s.Location, s.Tags, s.Labels)
	updated, err := scanServer(row)
	if isUniqueViolation(err) {
		return Server{}, ErrConflict
	}
	return updated, err
}

func (r *postgresRepository) UpdateTokenHash(ctx context.Context, id string, hash []byte) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE servers SET agent_token_hash = $2, updated_at = now() WHERE id = $1::uuid`, id, hash)
	if err != nil {
		return fmt.Errorf("servers: update token: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ApplyAgentFacts stores what the agent reported at hello time.
func (r *postgresRepository) ApplyAgentFacts(ctx context.Context, s Server) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE servers SET hostname = $2, os = $3, os_version = $4, kernel_version = $5,
			architecture = $6, agent_version = $7, cpu_cores = $8, memory_bytes = $9,
			swap_bytes = $10, disk_bytes = $11, docker_available = $12,
			systemd_available = $13, runtime_types = $14, updated_at = now()
		WHERE id = $1::uuid`,
		s.ID, s.Hostname, s.OS, s.OSVersion, s.KernelVersion, s.Architecture,
		s.AgentVersion, s.CPUCores, s.MemoryBytes, s.SwapBytes, s.DiskBytes,
		s.DockerAvailable, s.SystemdAvailable, s.RuntimeTypes)
	if err != nil {
		return fmt.Errorf("servers: apply agent facts: %w", err)
	}
	return nil
}

func (r *postgresRepository) SetConnectionStatus(ctx context.Context, id string, status ConnectionStatus) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE servers SET connection_status = $2, last_seen_at = now(), updated_at = now()
		WHERE id = $1::uuid`, id, status)
	if err != nil {
		return fmt.Errorf("servers: set connection status: %w", err)
	}
	return nil
}

func (r *postgresRepository) MarkAllOffline(ctx context.Context) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE servers SET connection_status = 'offline' WHERE connection_status = 'online'`)
	if err != nil {
		return fmt.Errorf("servers: mark all offline: %w", err)
	}
	return nil
}

func (r *postgresRepository) RecordHeartbeat(ctx context.Context, id string, m MetricsPoint) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("servers: begin heartbeat: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		UPDATE servers SET last_heartbeat_at = $2, last_seen_at = $2, updated_at = now()
		WHERE id = $1::uuid`, id, m.CollectedAt); err != nil {
		return fmt.Errorf("servers: heartbeat update: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO server_metrics (server_id, collected_at, cpu_percent, load1, load5, load15,
			memory_used, memory_total, swap_used, swap_total, disk_used, disk_total,
			net_rx_bytes, net_tx_bytes, temperature, uptime_secs)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		ON CONFLICT (server_id, collected_at) DO NOTHING`,
		id, m.CollectedAt, m.CPUPercent, m.Load1, m.Load5, m.Load15,
		m.MemoryUsed, m.MemoryTotal, m.SwapUsed, m.SwapTotal, m.DiskUsed, m.DiskTotal,
		m.NetRxBytes, m.NetTxBytes, m.Temperature, m.UptimeSecs); err != nil {
		return fmt.Errorf("servers: heartbeat metrics: %w", err)
	}
	return tx.Commit(ctx)
}

func (r *postgresRepository) Delete(ctx context.Context, id string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM servers WHERE id = $1::uuid`, id)
	if err != nil {
		return fmt.Errorf("servers: delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *postgresRepository) CountByStatus(ctx context.Context) (map[ConnectionStatus]int, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT connection_status, count(*) FROM servers GROUP BY connection_status`)
	if err != nil {
		return nil, fmt.Errorf("servers: count by status: %w", err)
	}
	defer rows.Close()
	out := map[ConnectionStatus]int{}
	for rows.Next() {
		var status ConnectionStatus
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return nil, err
		}
		out[status] = n
	}
	return out, rows.Err()
}

func (r *postgresRepository) CreateGroup(ctx context.Context, g Group) (Group, error) {
	err := r.pool.QueryRow(ctx, `
		INSERT INTO server_groups (name, description) VALUES ($1, $2)
		RETURNING id::text, created_at`, g.Name, g.Description).Scan(&g.ID, &g.CreatedAt)
	if isUniqueViolation(err) {
		return Group{}, ErrConflict
	}
	if err != nil {
		return Group{}, fmt.Errorf("servers: create group: %w", err)
	}
	return g, nil
}

func (r *postgresRepository) ListGroups(ctx context.Context) ([]Group, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id::text, name, description, created_at FROM server_groups ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("servers: list groups: %w", err)
	}
	defer rows.Close()
	var out []Group
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.ID, &g.Name, &g.Description, &g.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (r *postgresRepository) DeleteGroup(ctx context.Context, id string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM server_groups WHERE id = $1::uuid`, id)
	if err != nil {
		return fmt.Errorf("servers: delete group: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *postgresRepository) AddGroupMember(ctx context.Context, groupID, serverID string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO server_group_members (group_id, server_id)
		VALUES ($1::uuid, $2::uuid) ON CONFLICT DO NOTHING`, groupID, serverID)
	if err != nil {
		return fmt.Errorf("servers: add group member: %w", err)
	}
	return nil
}

func (r *postgresRepository) RemoveGroupMember(ctx context.Context, groupID, serverID string) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM server_group_members WHERE group_id = $1::uuid AND server_id = $2::uuid`,
		groupID, serverID)
	if err != nil {
		return fmt.Errorf("servers: remove group member: %w", err)
	}
	return nil
}

func (r *postgresRepository) GroupIDsOfServer(ctx context.Context, serverID string) ([]string, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT group_id::text FROM server_group_members WHERE server_id = $1::uuid`, serverID)
	if err != nil {
		return nil, fmt.Errorf("servers: groups of server: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (r *postgresRepository) ServerIDsOfGroup(ctx context.Context, groupID string) ([]string, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT server_id::text FROM server_group_members WHERE group_id = $1::uuid`, groupID)
	if err != nil {
		return nil, fmt.Errorf("servers: servers of group: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (r *postgresRepository) Metrics(ctx context.Context, serverID string, from, to time.Time, limit int) ([]MetricsPoint, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT server_id::text, collected_at, cpu_percent, load1, load5, load15,
			memory_used, memory_total, swap_used, swap_total, disk_used, disk_total,
			net_rx_bytes, net_tx_bytes, temperature, uptime_secs
		FROM server_metrics
		WHERE server_id = $1::uuid AND collected_at >= $2 AND collected_at <= $3
		ORDER BY collected_at DESC LIMIT $4`, serverID, from, to, limit)
	if err != nil {
		return nil, fmt.Errorf("servers: metrics: %w", err)
	}
	defer rows.Close()
	var out []MetricsPoint
	for rows.Next() {
		var m MetricsPoint
		if err := rows.Scan(&m.ServerID, &m.CollectedAt, &m.CPUPercent, &m.Load1, &m.Load5,
			&m.Load15, &m.MemoryUsed, &m.MemoryTotal, &m.SwapUsed, &m.SwapTotal,
			&m.DiskUsed, &m.DiskTotal, &m.NetRxBytes, &m.NetTxBytes,
			&m.Temperature, &m.UptimeSecs); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *postgresRepository) PruneMetrics(ctx context.Context, before time.Time) (int64, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM server_metrics WHERE collected_at < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("servers: prune metrics: %w", err)
	}
	return tag.RowsAffected(), nil
}
