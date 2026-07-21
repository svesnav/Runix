package rbac

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"
)

// ServerGroupResolver answers which server groups a server belongs to; the
// servers module provides it during wiring. Consumer-owned interface keeps
// the modules decoupled.
type ServerGroupResolver interface {
	GroupIDsOfServer(ctx context.Context, serverID string) ([]string, error)
}

type Service struct {
	repo   Repository
	groups ServerGroupResolver
	log    *slog.Logger

	cacheTTL time.Duration
	mu       sync.Mutex
	cache    map[string]cachedPerms
}

type cachedPerms struct {
	global  map[string]struct{}
	grants  []Grant
	expires time.Time
}

func NewService(repo Repository, groups ServerGroupResolver, log *slog.Logger) *Service {
	return &Service{
		repo:     repo,
		groups:   groups,
		log:      log,
		cacheTTL: 15 * time.Second,
		cache:    make(map[string]cachedPerms),
	}
}

func (s *Service) effective(ctx context.Context, userID string) (cachedPerms, error) {
	s.mu.Lock()
	if c, ok := s.cache[userID]; ok && time.Now().Before(c.expires) {
		s.mu.Unlock()
		return c, nil
	}
	s.mu.Unlock()

	perms, err := s.repo.UserPermissions(ctx, userID)
	if err != nil {
		return cachedPerms{}, err
	}
	grants, err := s.repo.GrantsForUser(ctx, userID)
	if err != nil {
		return cachedPerms{}, err
	}
	c := cachedPerms{
		global:  make(map[string]struct{}, len(perms)),
		grants:  grants,
		expires: time.Now().Add(s.cacheTTL),
	}
	for _, p := range perms {
		c.global[p] = struct{}{}
	}
	s.mu.Lock()
	s.cache[userID] = c
	s.mu.Unlock()
	return c, nil
}

// Invalidate drops cached permissions; called after any RBAC mutation.
func (s *Service) Invalidate() {
	s.mu.Lock()
	s.cache = make(map[string]cachedPerms)
	s.mu.Unlock()
}

// Check reports whether userID holds perm for the given scope. Role
// permissions are global; grants can match exactly or through a broader
// scope (a server-group grant covers every server in the group, any grant
// or role at server level covers the runtimes on it).
func (s *Service) Check(ctx context.Context, userID, perm string, scope Scope) (bool, error) {
	c, err := s.effective(ctx, userID)
	if err != nil {
		return false, fmt.Errorf("rbac: resolve permissions: %w", err)
	}
	if _, ok := c.global[perm]; ok {
		return true, nil
	}
	var serverGroups []string
	loadedGroups := false
	for _, g := range c.grants {
		if g.Permission != perm {
			continue
		}
		switch g.ScopeType {
		case ScopeGlobal:
			return true, nil
		case ScopeServer:
			if scope.Type == ScopeServer && g.ScopeID == scope.ID {
				return true, nil
			}
		case ScopeRuntime:
			if scope.Type == ScopeRuntime && g.ScopeID == scope.ID {
				return true, nil
			}
		case ScopeServerGroup:
			if scope.Type != ScopeServer {
				continue
			}
			if !loadedGroups {
				if s.groups == nil {
					continue
				}
				serverGroups, err = s.groups.GroupIDsOfServer(ctx, scope.ID)
				if err != nil {
					return false, fmt.Errorf("rbac: resolve server groups: %w", err)
				}
				loadedGroups = true
			}
			if slices.Contains(serverGroups, g.ScopeID) {
				return true, nil
			}
		}
	}
	return false, nil
}

// GlobalPermissions returns the user's role-derived permission set.
func (s *Service) GlobalPermissions(ctx context.Context, userID string) ([]string, error) {
	c, err := s.effective(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(c.global))
	for p := range c.global {
		out = append(out, p)
	}
	slices.Sort(out)
	return out, nil
}

func (s *Service) HasRole(ctx context.Context, userID, roleKey string) (bool, error) {
	keys, err := s.repo.RoleKeysOfUser(ctx, userID)
	if err != nil {
		return false, err
	}
	return slices.Contains(keys, roleKey), nil
}

// Seed creates or reconciles the built-in roles. System role permission
// sets are code-defined, so upgrades adding permissions propagate here.
func (s *Service) Seed(ctx context.Context) error {
	existing, err := s.repo.ListRoles(ctx)
	if err != nil {
		return fmt.Errorf("rbac: seed: %w", err)
	}
	byKey := map[string]Role{}
	for _, r := range existing {
		byKey[r.Key] = r
	}
	for key, perms := range builtinRoles() {
		if have, ok := byKey[key]; ok {
			if !slices.Equal(have.Permissions, sorted(perms)) {
				if err := s.repo.SetRolePermissions(ctx, have.ID, perms); err != nil {
					return fmt.Errorf("rbac: reconcile role %s: %w", key, err)
				}
				s.log.Info("reconciled system role permissions", "role", key)
			}
			continue
		}
		if _, err := s.repo.CreateRole(ctx, Role{
			Key: key, Name: key, IsSystem: true, Permissions: perms,
			Description: "Built-in role",
		}); err != nil {
			return fmt.Errorf("rbac: create system role %s: %w", key, err)
		}
		s.log.Info("created system role", "role", key)
	}
	s.Invalidate()
	return nil
}

func sorted(in []string) []string {
	out := make([]string, len(in))
	copy(out, in)
	slices.Sort(out)
	return out
}

// Role/group/grant management passthroughs with domain validation.

func (s *Service) ListRoles(ctx context.Context) ([]Role, error) {
	return s.repo.ListRoles(ctx)
}

func (s *Service) CreateRole(ctx context.Context, key, name, description string, perms []string) (Role, error) {
	if key == "" || name == "" {
		return Role{}, fmt.Errorf("%w: key and name are required", ErrInvalid)
	}
	for _, p := range perms {
		if !ValidPermission(p) {
			return Role{}, fmt.Errorf("%w: unknown permission %q", ErrInvalid, p)
		}
	}
	role, err := s.repo.CreateRole(ctx, Role{Key: key, Name: name, Description: description, Permissions: perms})
	if err == nil {
		s.Invalidate()
	}
	return role, err
}

func (s *Service) UpdateRole(ctx context.Context, role Role) (Role, error) {
	for _, p := range role.Permissions {
		if !ValidPermission(p) {
			return Role{}, fmt.Errorf("%w: unknown permission %q", ErrInvalid, p)
		}
	}
	updated, err := s.repo.UpdateRole(ctx, role)
	if err == nil {
		s.Invalidate()
	}
	return updated, err
}

func (s *Service) DeleteRole(ctx context.Context, id string) error {
	err := s.repo.DeleteRole(ctx, id)
	if err == nil {
		s.Invalidate()
	}
	return err
}

func (s *Service) SetUserRoles(ctx context.Context, userID string, roleIDs []string) error {
	err := s.repo.SetUserRoles(ctx, userID, roleIDs)
	if err == nil {
		s.Invalidate()
	}
	return err
}

func (s *Service) RoleKeysOfUser(ctx context.Context, userID string) ([]string, error) {
	return s.repo.RoleKeysOfUser(ctx, userID)
}

func (s *Service) ListGroups(ctx context.Context) ([]Group, error) {
	return s.repo.ListGroups(ctx)
}

func (s *Service) CreateGroup(ctx context.Context, name, description string) (Group, error) {
	if name == "" {
		return Group{}, fmt.Errorf("%w: name is required", ErrInvalid)
	}
	return s.repo.CreateGroup(ctx, Group{Name: name, Description: description})
}

func (s *Service) DeleteGroup(ctx context.Context, id string) error {
	err := s.repo.DeleteGroup(ctx, id)
	if err == nil {
		s.Invalidate()
	}
	return err
}

func (s *Service) AddGroupMember(ctx context.Context, groupID, userID string) error {
	err := s.repo.AddGroupMember(ctx, groupID, userID)
	if err == nil {
		s.Invalidate()
	}
	return err
}

func (s *Service) RemoveGroupMember(ctx context.Context, groupID, userID string) error {
	err := s.repo.RemoveGroupMember(ctx, groupID, userID)
	if err == nil {
		s.Invalidate()
	}
	return err
}

func (s *Service) GroupMemberIDs(ctx context.Context, groupID string) ([]string, error) {
	return s.repo.GroupMemberIDs(ctx, groupID)
}

func (s *Service) ListGrants(ctx context.Context) ([]Grant, error) {
	return s.repo.ListGrants(ctx)
}

func (s *Service) CreateGrant(ctx context.Context, g Grant) (Grant, error) {
	if !ValidPermission(g.Permission) {
		return Grant{}, fmt.Errorf("%w: unknown permission %q", ErrInvalid, g.Permission)
	}
	switch g.SubjectType {
	case SubjectUser, SubjectGroup, SubjectRole:
	default:
		return Grant{}, fmt.Errorf("%w: subject type %q (want user, group or role)",
			ErrInvalid, g.SubjectType)
	}
	switch g.ScopeType {
	case ScopeGlobal:
		g.ScopeID = ""
	case ScopeServer, ScopeServerGroup, ScopeRuntime:
		if g.ScopeID == "" {
			return Grant{}, fmt.Errorf("%w: scope id required for %s scope", ErrInvalid, g.ScopeType)
		}
	default:
		return Grant{}, fmt.Errorf("%w: scope type %q", ErrInvalid, g.ScopeType)
	}
	created, err := s.repo.CreateGrant(ctx, g)
	if err == nil {
		s.Invalidate()
	}
	return created, err
}

func (s *Service) DeleteGrant(ctx context.Context, id string) error {
	err := s.repo.DeleteGrant(ctx, id)
	if err == nil {
		s.Invalidate()
	}
	return err
}
