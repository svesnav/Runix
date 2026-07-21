package users

import (
	"errors"
	"time"
)

var (
	ErrNotFound      = errors.New("users: not found")
	ErrConflict      = errors.New("users: username or email already taken")
	ErrInvalid       = errors.New("users: invalid input")
	ErrWrongPassword = errors.New("users: wrong password")
	ErrLastAdmin     = errors.New("users: cannot remove the last active admin")
)

// User is the account entity. PasswordHash and TOTP fields never leave the
// module; DTO mapping strips them.
type User struct {
	ID                 string
	Username           string
	Email              string
	DisplayName        string
	PasswordHash       string
	IsActive           bool
	IsSystem           bool
	MustChangePassword bool
	TOTPEnabled        bool
	TOTPSecretEnc      string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// Public is the safe projection used in every API response.
type Public struct {
	ID                 string    `json:"id"`
	Username           string    `json:"username"`
	Email              string    `json:"email"`
	DisplayName        string    `json:"displayName"`
	IsActive           bool      `json:"isActive"`
	MustChangePassword bool      `json:"mustChangePassword"`
	TOTPEnabled        bool      `json:"totpEnabled"`
	CreatedAt          time.Time `json:"createdAt"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

func (u User) Public() Public {
	return Public{
		ID: u.ID, Username: u.Username, Email: u.Email, DisplayName: u.DisplayName,
		IsActive: u.IsActive, MustChangePassword: u.MustChangePassword,
		TOTPEnabled: u.TOTPEnabled, CreatedAt: u.CreatedAt, UpdatedAt: u.UpdatedAt,
	}
}
