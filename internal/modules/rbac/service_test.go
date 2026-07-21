package rbac

import (
	"context"
	"io"
	"log/slog"
	"testing"
)

type fakeRepo struct {
	Repository
	perms  map[string][]string // userID -> role permissions
	grants map[string][]Grant  // userID -> effective grants
	calls  int
}

func (f *fakeRepo) UserPermissions(_ context.Context, userID string) ([]string, error) {
	f.calls++
	return f.perms[userID], nil
}

func (f *fakeRepo) GrantsForUser(_ context.Context, userID string) ([]Grant, error) {
	return f.grants[userID], nil
}

type fakeGroups struct{ groups map[string][]string }

func (f fakeGroups) GroupIDsOfServer(_ context.Context, serverID string) ([]string, error) {
	return f.groups[serverID], nil
}

func newTestService(repo *fakeRepo, groups ServerGroupResolver) *Service {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewService(repo, groups, log)
}

func TestCheckGlobalRolePermission(t *testing.T) {
	repo := &fakeRepo{perms: map[string][]string{"u1": {PermServerView}}}
	svc := newTestService(repo, nil)

	ok, err := svc.Check(context.Background(), "u1", PermServerView, GlobalScope)
	if err != nil || !ok {
		t.Fatalf("Check = %v, %v; want allowed", ok, err)
	}
	ok, _ = svc.Check(context.Background(), "u1", PermUsersManage, GlobalScope)
	if ok {
		t.Fatal("permission not held was allowed")
	}
	// Global role permission also satisfies scoped checks.
	ok, _ = svc.Check(context.Background(), "u1", PermServerView, ServerScope("srv-1"))
	if !ok {
		t.Fatal("global permission should cover server scope")
	}
}

func TestCheckServerScopedGrant(t *testing.T) {
	repo := &fakeRepo{grants: map[string][]Grant{
		"u1": {{SubjectType: SubjectUser, SubjectID: "u1", Permission: PermServerPower,
			ScopeType: ScopeServer, ScopeID: "srv-1"}},
	}}
	svc := newTestService(repo, nil)

	ok, _ := svc.Check(context.Background(), "u1", PermServerPower, ServerScope("srv-1"))
	if !ok {
		t.Fatal("matching server grant denied")
	}
	ok, _ = svc.Check(context.Background(), "u1", PermServerPower, ServerScope("srv-2"))
	if ok {
		t.Fatal("grant for another server allowed")
	}
	ok, _ = svc.Check(context.Background(), "u1", PermServerPower, GlobalScope)
	if ok {
		t.Fatal("server-scoped grant satisfied a global check")
	}
}

func TestCheckServerGroupGrant(t *testing.T) {
	repo := &fakeRepo{grants: map[string][]Grant{
		"u1": {{SubjectType: SubjectGroup, SubjectID: "g1", Permission: PermServerView,
			ScopeType: ScopeServerGroup, ScopeID: "sg-1"}},
	}}
	groups := fakeGroups{groups: map[string][]string{"srv-1": {"sg-1"}, "srv-2": {"sg-2"}}}
	svc := newTestService(repo, groups)

	ok, _ := svc.Check(context.Background(), "u1", PermServerView, ServerScope("srv-1"))
	if !ok {
		t.Fatal("server-group grant denied for member server")
	}
	ok, _ = svc.Check(context.Background(), "u1", PermServerView, ServerScope("srv-2"))
	if ok {
		t.Fatal("server-group grant allowed for non-member server")
	}
}

// A runtime-scoped grant must authorize that runtime and nothing else. This
// path was previously stored but never evaluated, so the grant silently did
// nothing.
func TestCheckRuntimeScopedGrant(t *testing.T) {
	scopeID := RuntimeScopeID("srv-1", "docker", "web")
	repo := &fakeRepo{grants: map[string][]Grant{
		"u1": {{SubjectType: SubjectUser, SubjectID: "u1", Permission: PermRuntimeManage,
			ScopeType: ScopeRuntime, ScopeID: scopeID}},
	}}
	svc := newTestService(repo, nil)
	ctx := context.Background()

	ok, _ := svc.Check(ctx, "u1", PermRuntimeManage, RuntimeScope("srv-1", "docker", "web"))
	if !ok {
		t.Error("grant for this exact runtime was denied")
	}
	// A different runtime on the same server.
	ok, _ = svc.Check(ctx, "u1", PermRuntimeManage, RuntimeScope("srv-1", "docker", "db"))
	if ok {
		t.Error("grant leaked to another runtime")
	}
	// The same runtime name on a different server must not match, which is
	// why the scope id carries the server.
	ok, _ = svc.Check(ctx, "u1", PermRuntimeManage, RuntimeScope("srv-2", "docker", "web"))
	if ok {
		t.Error("grant leaked to another server's identically named runtime")
	}
	// It must not widen into server-level authority.
	ok, _ = svc.Check(ctx, "u1", PermRuntimeManage, ServerScope("srv-1"))
	if ok {
		t.Error("runtime grant satisfied a server-scoped check")
	}
}

func TestCheckCachesAndInvalidates(t *testing.T) {
	repo := &fakeRepo{perms: map[string][]string{"u1": {PermServerView}}}
	svc := newTestService(repo, nil)

	ctx := context.Background()
	for range 3 {
		if _, err := svc.Check(ctx, "u1", PermServerView, GlobalScope); err != nil {
			t.Fatal(err)
		}
	}
	if repo.calls != 1 {
		t.Errorf("repo hit %d times, want 1 (cached)", repo.calls)
	}
	svc.Invalidate()
	if _, err := svc.Check(ctx, "u1", PermServerView, GlobalScope); err != nil {
		t.Fatal(err)
	}
	if repo.calls != 2 {
		t.Errorf("repo hit %d times after invalidate, want 2", repo.calls)
	}
}

func TestCreateGrantValidation(t *testing.T) {
	svc := newTestService(&fakeRepo{}, nil)
	ctx := context.Background()

	if _, err := svc.CreateGrant(ctx, Grant{SubjectType: SubjectUser, SubjectID: "u",
		Permission: "not.a.permission", ScopeType: ScopeGlobal}); err == nil {
		t.Error("unknown permission accepted")
	}
	if _, err := svc.CreateGrant(ctx, Grant{SubjectType: SubjectUser, SubjectID: "u",
		Permission: PermServerView, ScopeType: ScopeServer}); err == nil {
		t.Error("server scope without id accepted")
	}
	if _, err := svc.CreateGrant(ctx, Grant{SubjectType: "robot", SubjectID: "u",
		Permission: PermServerView, ScopeType: ScopeGlobal}); err == nil {
		t.Error("bad subject type accepted")
	}
}

// Every permission must have a human-readable descriptor, and descriptors
// must not drift from the catalog.
func TestPermissionCatalogCoversEveryPermission(t *testing.T) {
	described := map[string]Descriptor{}
	for _, d := range PermissionCatalog() {
		if d.Label == "" || d.Description == "" || d.Group == "" {
			t.Errorf("permission %q has an incomplete descriptor: %+v", d.Key, d)
		}
		if _, dup := described[d.Key]; dup {
			t.Errorf("permission %q described twice", d.Key)
		}
		described[d.Key] = d
	}
	for _, key := range AllPermissions() {
		if _, ok := described[key]; !ok {
			t.Errorf("permission %q has no descriptor", key)
		}
	}
	for key := range described {
		if !ValidPermission(key) {
			t.Errorf("descriptor %q is not a known permission", key)
		}
	}
}

func TestBuiltinRolesUseValidPermissions(t *testing.T) {
	for role, perms := range builtinRoles() {
		for _, p := range perms {
			if !ValidPermission(p) {
				t.Errorf("role %s references unknown permission %q", role, p)
			}
		}
	}
}
