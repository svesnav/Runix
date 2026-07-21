package auth

import (
	"errors"
	"time"
)

var (
	ErrBadCredentials = errors.New("auth: invalid credentials")
	ErrUserDisabled   = errors.New("auth: account disabled")
	ErrSessionInvalid = errors.New("auth: session invalid or expired")
	ErrTokenReuse     = errors.New("auth: refresh token reuse detected")
	ErrMFARequired    = errors.New("auth: mfa required")
	ErrMFACode        = errors.New("auth: invalid mfa code")
	ErrMFAState       = errors.New("auth: mfa not in expected state")
	ErrRateLimited    = errors.New("auth: too many attempts, slow down")
	ErrNotFound       = errors.New("auth: not found")
	ErrConflict       = errors.New("auth: already exists")
)

type Session struct {
	ID          string     `json:"id"`
	UserID      string     `json:"userId"`
	RefreshHash []byte     `json:"-"`
	UserAgent   string     `json:"userAgent"`
	IP          string     `json:"ip"`
	Remember    bool       `json:"remember"`
	CreatedAt   time.Time  `json:"createdAt"`
	LastUsedAt  time.Time  `json:"lastUsedAt"`
	ExpiresAt   time.Time  `json:"expiresAt"`
	RevokedAt   *time.Time `json:"revokedAt,omitempty"`
	ReplacedBy  string     `json:"-"`
}

func (s Session) usable(now time.Time) bool {
	return s.RevokedAt == nil && now.Before(s.ExpiresAt)
}

type PAT struct {
	ID         string     `json:"id"`
	UserID     string     `json:"userId"`
	Name       string     `json:"name"`
	TokenHash  []byte     `json:"-"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
	ExpiresAt  *time.Time `json:"expiresAt,omitempty"`
	RevokedAt  *time.Time `json:"revokedAt,omitempty"`
}

const (
	refreshTokenPrefix = "rnx_rt_"
	patPrefix          = "rnx_pat_"
	recoveryCodeCount  = 10
)
