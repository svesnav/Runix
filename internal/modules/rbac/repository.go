package rbac

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound = errors.New("rbac: not found")
	ErrConflict = errors.New("rbac: already exists")
)

// Repository is owned by the service; postgresRepository implements it.
type Repository interface {
	ListRoles(ctx context.Context) ([]Role, error)
	GetRoleByKey(ctx context.Context, key string) (Role, error)
	CreateRole(ctx context.Context, r Role) (Role, error)
	UpdateRole(ctx context.Context, r Role) (Role, error)
	DeleteRole(ctx context.Context, id string) error
	SetRolePermissions(ctx context.Context, roleID string, perms []string) error

	RoleKeysOfUser(ctx context.Context, userID string) ([]string, error)
	SetUserRoles(ctx context.Context, userID string, roleIDs []string) error
	UserPermissions(ctx context.Context, userID string) ([]string, error)

	ListGroups(ctx context.Context) ([]Group, error)
	CreateGroup(ctx context.Context, g Group) (Group, error)
	DeleteGroup(ctx context.Context, id string) error
	AddGroupMember(ctx context.Context, groupID, userID string) error
	RemoveGroupMember(ctx context.Context, groupID, userID string) error
	GroupMemberIDs(ctx context.Context, groupID string) ([]string, error)

	CreateGrant(ctx context.Context, g Grant) (Grant, error)
	DeleteGrant(ctx context.Context, id string) error
	ListGrants(ctx context.Context) ([]Grant, error)
	// GrantsForUser returns direct user grants plus grants of every group
	// the user belongs to.
	GrantsForUser(ctx context.Context, userID string) ([]Grant, error)
}

type postgresRepository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) Repository {
	return &postgresRepository{pool: pool}
}

func (r *postgresRepository) scanRoles(ctx context.Context, rows pgx.Rows) ([]Role, error) {
	defer rows.Close()
	var out []Role
	index := map[string]int{}
	for rows.Next() {
		var role Role
		if err := rows.Scan(&role.ID, &role.Key, &role.Name, &role.Description,
			&role.IsSystem, &role.CreatedAt, &role.UpdatedAt); err != nil {
			return nil, fmt.Errorf("rbac: scan role: %w", err)
		}
		role.Permissions = []string{}
		index[role.ID] = len(out)
		out = append(out, role)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}
	ids := make([]string, 0, len(out))
	for _, role := range out {
		ids = append(ids, role.ID)
	}
	permRows, err := r.pool.Query(ctx,
		`SELECT role_id::text, permission FROM role_permissions WHERE role_id = ANY($1::uuid[]) ORDER BY permission`, ids)
	if err != nil {
		return nil, fmt.Errorf("rbac: load role permissions: %w", err)
	}
	defer permRows.Close()
	for permRows.Next() {
		var roleID, perm string
		if err := permRows.Scan(&roleID, &perm); err != nil {
			return nil, fmt.Errorf("rbac: scan role permission: %w", err)
		}
		if i, ok := index[roleID]; ok {
			out[i].Permissions = append(out[i].Permissions, perm)
		}
	}
	return out, permRows.Err()
}

const roleColumns = `id::text, key, name, description, is_system, created_at, updated_at`

func (r *postgresRepository) ListRoles(ctx context.Context) ([]Role, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+roleColumns+` FROM roles ORDER BY key`)
	if err != nil {
		return nil, fmt.Errorf("rbac: list roles: %w", err)
	}
	return r.scanRoles(ctx, rows)
}

func (r *postgresRepository) GetRoleByKey(ctx context.Context, key string) (Role, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+roleColumns+` FROM roles WHERE key = $1`, key)
	if err != nil {
		return Role{}, fmt.Errorf("rbac: get role: %w", err)
	}
	roles, err := r.scanRoles(ctx, rows)
	if err != nil {
		return Role{}, err
	}
	if len(roles) == 0 {
		return Role{}, fmt.Errorf("%w: role %q", ErrNotFound, key)
	}
	return roles[0], nil
}

func (r *postgresRepository) CreateRole(ctx context.Context, role Role) (Role, error) {
	err := r.pool.QueryRow(ctx, `
		INSERT INTO roles (key, name, description, is_system)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (key) DO NOTHING
		RETURNING id::text, created_at, updated_at`,
		role.Key, role.Name, role.Description, role.IsSystem,
	).Scan(&role.ID, &role.CreatedAt, &role.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Role{}, fmt.Errorf("%w: role %q", ErrConflict, role.Key)
	}
	if err != nil {
		return Role{}, fmt.Errorf("rbac: create role: %w", err)
	}
	if err := r.SetRolePermissions(ctx, role.ID, role.Permissions); err != nil {
		return Role{}, err
	}
	return role, nil
}

func (r *postgresRepository) UpdateRole(ctx context.Context, role Role) (Role, error) {
	tag, err := r.pool.Exec(ctx,
		`UPDATE roles SET name = $2, description = $3, updated_at = now() WHERE id = $1::uuid`,
		role.ID, role.Name, role.Description)
	if err != nil {
		return Role{}, fmt.Errorf("rbac: update role: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return Role{}, fmt.Errorf("%w: role %s", ErrNotFound, role.ID)
	}
	if role.Permissions != nil {
		if err := r.SetRolePermissions(ctx, role.ID, role.Permissions); err != nil {
			return Role{}, err
		}
	}
	return role, nil
}

func (r *postgresRepository) DeleteRole(ctx context.Context, id string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM roles WHERE id = $1::uuid AND NOT is_system`, id)
	if err != nil {
		return fmt.Errorf("rbac: delete role: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: role %s (or role is system)", ErrNotFound, id)
	}
	return nil
}

func (r *postgresRepository) SetRolePermissions(ctx context.Context, roleID string, perms []string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("rbac: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM role_permissions WHERE role_id = $1::uuid`, roleID); err != nil {
		return fmt.Errorf("rbac: clear role permissions: %w", err)
	}
	for _, p := range perms {
		if _, err := tx.Exec(ctx,
			`INSERT INTO role_permissions (role_id, permission) VALUES ($1::uuid, $2)`, roleID, p); err != nil {
			return fmt.Errorf("rbac: add role permission: %w", err)
		}
	}
	return tx.Commit(ctx)
}

func (r *postgresRepository) RoleKeysOfUser(ctx context.Context, userID string) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT r.key FROM user_roles ur JOIN roles r ON r.id = ur.role_id
		WHERE ur.user_id = $1::uuid ORDER BY r.key`, userID)
	if err != nil {
		return nil, fmt.Errorf("rbac: roles of user: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (r *postgresRepository) SetUserRoles(ctx context.Context, userID string, roleIDs []string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("rbac: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM user_roles WHERE user_id = $1::uuid`, userID); err != nil {
		return fmt.Errorf("rbac: clear user roles: %w", err)
	}
	for _, id := range roleIDs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO user_roles (user_id, role_id) VALUES ($1::uuid, $2::uuid)`, userID, id); err != nil {
			return fmt.Errorf("rbac: assign role: %w", err)
		}
	}
	return tx.Commit(ctx)
}

func (r *postgresRepository) UserPermissions(ctx context.Context, userID string) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT DISTINCT rp.permission
		FROM user_roles ur JOIN role_permissions rp ON rp.role_id = ur.role_id
		WHERE ur.user_id = $1::uuid`, userID)
	if err != nil {
		return nil, fmt.Errorf("rbac: user permissions: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *postgresRepository) ListGroups(ctx context.Context) ([]Group, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id::text, name, description, created_at FROM user_groups ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("rbac: list groups: %w", err)
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

func (r *postgresRepository) CreateGroup(ctx context.Context, g Group) (Group, error) {
	err := r.pool.QueryRow(ctx, `
		INSERT INTO user_groups (name, description) VALUES ($1, $2)
		ON CONFLICT (name) DO NOTHING
		RETURNING id::text, created_at`,
		g.Name, g.Description).Scan(&g.ID, &g.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Group{}, fmt.Errorf("%w: group %q", ErrConflict, g.Name)
	}
	if err != nil {
		return Group{}, fmt.Errorf("rbac: create group: %w", err)
	}
	return g, nil
}

func (r *postgresRepository) DeleteGroup(ctx context.Context, id string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM user_groups WHERE id = $1::uuid`, id)
	if err != nil {
		return fmt.Errorf("rbac: delete group: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: group %s", ErrNotFound, id)
	}
	return nil
}

func (r *postgresRepository) AddGroupMember(ctx context.Context, groupID, userID string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO user_group_members (group_id, user_id)
		VALUES ($1::uuid, $2::uuid) ON CONFLICT DO NOTHING`, groupID, userID)
	if err != nil {
		return fmt.Errorf("rbac: add group member: %w", err)
	}
	return nil
}

func (r *postgresRepository) RemoveGroupMember(ctx context.Context, groupID, userID string) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM user_group_members WHERE group_id = $1::uuid AND user_id = $2::uuid`, groupID, userID)
	if err != nil {
		return fmt.Errorf("rbac: remove group member: %w", err)
	}
	return nil
}

func (r *postgresRepository) GroupMemberIDs(ctx context.Context, groupID string) ([]string, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT user_id::text FROM user_group_members WHERE group_id = $1::uuid`, groupID)
	if err != nil {
		return nil, fmt.Errorf("rbac: group members: %w", err)
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

const grantColumns = `id::text, subject_type, subject_id::text, permission, scope_type, scope_id, created_at, coalesce(created_by::text, '')`

func scanGrants(rows pgx.Rows) ([]Grant, error) {
	defer rows.Close()
	var out []Grant
	for rows.Next() {
		var g Grant
		if err := rows.Scan(&g.ID, &g.SubjectType, &g.SubjectID, &g.Permission,
			&g.ScopeType, &g.ScopeID, &g.CreatedAt, &g.CreatedBy); err != nil {
			return nil, fmt.Errorf("rbac: scan grant: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (r *postgresRepository) CreateGrant(ctx context.Context, g Grant) (Grant, error) {
	var createdBy *string
	if g.CreatedBy != "" {
		createdBy = &g.CreatedBy
	}
	err := r.pool.QueryRow(ctx, `
		INSERT INTO grants (subject_type, subject_id, permission, scope_type, scope_id, created_by)
		VALUES ($1, $2::uuid, $3, $4, $5, $6::uuid)
		ON CONFLICT DO NOTHING
		RETURNING id::text, created_at`,
		g.SubjectType, g.SubjectID, g.Permission, g.ScopeType, g.ScopeID, createdBy,
	).Scan(&g.ID, &g.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Grant{}, fmt.Errorf("%w: identical grant", ErrConflict)
	}
	if err != nil {
		return Grant{}, fmt.Errorf("rbac: create grant: %w", err)
	}
	return g, nil
}

func (r *postgresRepository) DeleteGrant(ctx context.Context, id string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM grants WHERE id = $1::uuid`, id)
	if err != nil {
		return fmt.Errorf("rbac: delete grant: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: grant %s", ErrNotFound, id)
	}
	return nil
}

func (r *postgresRepository) ListGrants(ctx context.Context) ([]Grant, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+grantColumns+` FROM grants ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("rbac: list grants: %w", err)
	}
	return scanGrants(rows)
}

// GrantsForUser returns everything that applies to a user: their own
// grants, those of every group they belong to, and those of every role
// they hold.
func (r *postgresRepository) GrantsForUser(ctx context.Context, userID string) ([]Grant, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+grantColumns+` FROM grants
		WHERE (subject_type = 'user' AND subject_id = $1::uuid)
		   OR (subject_type = 'group' AND subject_id IN
		       (SELECT group_id FROM user_group_members WHERE user_id = $1::uuid))
		   OR (subject_type = 'role' AND subject_id IN
		       (SELECT role_id FROM user_roles WHERE user_id = $1::uuid))`, userID)
	if err != nil {
		return nil, fmt.Errorf("rbac: grants for user: %w", err)
	}
	return scanGrants(rows)
}
