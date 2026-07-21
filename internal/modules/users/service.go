package users

import (
	"context"
	"fmt"
	"net/mail"
	"regexp"
	"unicode/utf8"

	"github.com/runix/runix/internal/platform/crypto"
	"github.com/runix/runix/internal/platform/httpx"
)

const minPasswordLen = 10

var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{2,63}$`)

// AdminGuard prevents mutations that would leave the platform without an
// active administrator. The rbac module supplies role facts at wiring time.
type AdminGuard interface {
	HasRole(ctx context.Context, userID, roleKey string) (bool, error)
}

type Service struct {
	repo  Repository
	guard AdminGuard
}

func NewService(repo Repository, guard AdminGuard) *Service {
	return &Service{repo: repo, guard: guard}
}

type CreateInput struct {
	Username           string
	Email              string
	DisplayName        string
	Password           string
	MustChangePassword bool
}

func (s *Service) Create(ctx context.Context, in CreateInput) (User, error) {
	if !usernamePattern.MatchString(in.Username) {
		return User{}, fmt.Errorf("%w: username must be 3-64 chars (letters, digits, . _ -)", ErrInvalid)
	}
	if _, err := mail.ParseAddress(in.Email); err != nil {
		return User{}, fmt.Errorf("%w: invalid email", ErrInvalid)
	}
	if err := validatePassword(in.Password); err != nil {
		return User{}, err
	}
	hash, err := crypto.HashPassword(in.Password)
	if err != nil {
		return User{}, fmt.Errorf("users: hash password: %w", err)
	}
	return s.repo.Create(ctx, User{
		Username: in.Username, Email: in.Email, DisplayName: in.DisplayName,
		PasswordHash: hash, IsActive: true, MustChangePassword: in.MustChangePassword,
	})
}

func (s *Service) Get(ctx context.Context, id string) (User, error) {
	return s.repo.GetByID(ctx, id)
}

func (s *Service) List(ctx context.Context, page httpx.Page) ([]User, int64, error) {
	return s.repo.List(ctx, page)
}

type UpdateInput struct {
	Email       string
	DisplayName string
	IsActive    bool
}

func (s *Service) Update(ctx context.Context, id string, in UpdateInput) (User, error) {
	if _, err := mail.ParseAddress(in.Email); err != nil {
		return User{}, fmt.Errorf("%w: invalid email", ErrInvalid)
	}
	current, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return User{}, err
	}
	if current.IsActive && !in.IsActive {
		if err := s.ensureNotLastAdmin(ctx, id); err != nil {
			return User{}, err
		}
	}
	return s.repo.Update(ctx, User{
		ID: id, Email: in.Email, DisplayName: in.DisplayName, IsActive: in.IsActive,
	})
}

func (s *Service) Delete(ctx context.Context, id string) error {
	if err := s.ensureNotLastAdmin(ctx, id); err != nil {
		return err
	}
	return s.repo.Delete(ctx, id)
}

// ChangePassword is the self-service path and requires the current password.
func (s *Service) ChangePassword(ctx context.Context, userID, current, next string) error {
	u, err := s.repo.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	ok, _, err := crypto.VerifyPassword(current, u.PasswordHash)
	if err != nil {
		return fmt.Errorf("users: verify password: %w", err)
	}
	if !ok {
		return ErrWrongPassword
	}
	return s.setPassword(ctx, userID, next, false)
}

// AdminSetPassword resets a user's password and forces a change on next
// login.
func (s *Service) AdminSetPassword(ctx context.Context, userID, next string) error {
	return s.setPassword(ctx, userID, next, true)
}

func (s *Service) setPassword(ctx context.Context, userID, next string, mustChange bool) error {
	if err := validatePassword(next); err != nil {
		return err
	}
	hash, err := crypto.HashPassword(next)
	if err != nil {
		return fmt.Errorf("users: hash password: %w", err)
	}
	return s.repo.UpdatePassword(ctx, userID, hash, mustChange)
}

func (s *Service) ensureNotLastAdmin(ctx context.Context, userID string) error {
	if s.guard == nil {
		return nil
	}
	isAdmin, err := s.guard.HasRole(ctx, userID, "admin")
	if err != nil {
		return fmt.Errorf("users: check admin role: %w", err)
	}
	if !isAdmin {
		return nil
	}
	n, err := s.repo.CountActiveWithRole(ctx, "admin")
	if err != nil {
		return err
	}
	if n <= 1 {
		return ErrLastAdmin
	}
	return nil
}

func validatePassword(pw string) error {
	if utf8.RuneCountInString(pw) < minPasswordLen {
		return fmt.Errorf("%w: password must be at least %d characters", ErrInvalid, minPasswordLen)
	}
	if len(pw) > 512 {
		return fmt.Errorf("%w: password too long", ErrInvalid)
	}
	return nil
}
