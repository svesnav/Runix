package users

import (
	"context"
	"errors"
	"testing"

	"github.com/runix/runix/internal/platform/crypto"
	"github.com/runix/runix/internal/platform/httpx"
)

type fakeRepo struct {
	users   map[string]User
	admins  int
	created []User
}

func (f *fakeRepo) Create(_ context.Context, u User) (User, error) {
	u.ID = "id-" + u.Username
	f.created = append(f.created, u)
	return u, nil
}
func (f *fakeRepo) GetByID(_ context.Context, id string) (User, error) {
	u, ok := f.users[id]
	if !ok {
		return User{}, ErrNotFound
	}
	return u, nil
}
func (f *fakeRepo) GetByIdentifier(context.Context, string) (User, error) {
	return User{}, ErrNotFound
}
func (f *fakeRepo) List(context.Context, httpx.Page) ([]User, int64, error) {
	return nil, 0, nil
}
func (f *fakeRepo) Update(_ context.Context, u User) (User, error) { return u, nil }
func (f *fakeRepo) UpdatePassword(_ context.Context, id, hash string, mustChange bool) error {
	u := f.users[id]
	u.PasswordHash = hash
	u.MustChangePassword = mustChange
	f.users[id] = u
	return nil
}
func (f *fakeRepo) SetTOTP(context.Context, string, bool, string) error { return nil }
func (f *fakeRepo) Delete(context.Context, string) error                { return nil }
func (f *fakeRepo) CountActiveWithRole(context.Context, string) (int, error) {
	return f.admins, nil
}
func (f *fakeRepo) Any(context.Context) (bool, error) { return len(f.users) > 0, nil }

type alwaysAdmin struct{}

func (alwaysAdmin) HasRole(context.Context, string, string) (bool, error) { return true, nil }

func TestCreateValidation(t *testing.T) {
	svc := NewService(&fakeRepo{}, nil)
	ctx := context.Background()

	cases := []CreateInput{
		{Username: "ab", Email: "a@b.co", Password: "long-enough-password"},
		{Username: "valid.user", Email: "not-an-email", Password: "long-enough-password"},
		{Username: "valid.user", Email: "a@b.co", Password: "short"},
	}
	for i, in := range cases {
		if _, err := svc.Create(ctx, in); !errors.Is(err, ErrInvalid) {
			t.Errorf("case %d: err = %v, want ErrInvalid", i, err)
		}
	}
	u, err := svc.Create(ctx, CreateInput{
		Username: "valid.user", Email: "a@b.co", Password: "long-enough-password",
	})
	if err != nil {
		t.Fatalf("valid create failed: %v", err)
	}
	if u.PasswordHash == "" || u.PasswordHash == "long-enough-password" {
		t.Error("password was not hashed")
	}
}

func TestChangePasswordRequiresCurrent(t *testing.T) {
	hash, _ := crypto.HashPassword("old-password-123")
	repo := &fakeRepo{users: map[string]User{"u1": {ID: "u1", PasswordHash: hash}}}
	svc := NewService(repo, nil)
	ctx := context.Background()

	if err := svc.ChangePassword(ctx, "u1", "wrong", "new-password-456"); !errors.Is(err, ErrWrongPassword) {
		t.Errorf("wrong current password: err = %v", err)
	}
	if err := svc.ChangePassword(ctx, "u1", "old-password-123", "new-password-456"); err != nil {
		t.Fatalf("valid change failed: %v", err)
	}
	ok, _, _ := crypto.VerifyPassword("new-password-456", repo.users["u1"].PasswordHash)
	if !ok {
		t.Error("new password not stored")
	}
}

func TestLastAdminProtected(t *testing.T) {
	repo := &fakeRepo{users: map[string]User{"u1": {ID: "u1", IsActive: true}}, admins: 1}
	svc := NewService(repo, alwaysAdmin{})
	ctx := context.Background()

	if err := svc.Delete(ctx, "u1"); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("deleting last admin: err = %v, want ErrLastAdmin", err)
	}
	if _, err := svc.Update(ctx, "u1", UpdateInput{Email: "a@b.co", IsActive: false}); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("deactivating last admin: err = %v, want ErrLastAdmin", err)
	}
	repo.admins = 2
	if err := svc.Delete(ctx, "u1"); err != nil {
		t.Errorf("delete with two admins failed: %v", err)
	}
}
