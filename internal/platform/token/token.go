package token

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const issuer = "runix"

// Scope restricts what a JWT is good for: full API access or only
// completing a pending MFA challenge.
type Scope string

const (
	ScopeAccess Scope = "access"
	ScopeMFA    Scope = "mfa"
)

type Claims struct {
	UserID    string
	SessionID string
	Scope     Scope
}

var (
	ErrInvalid = errors.New("token: invalid token")
	ErrScope   = errors.New("token: wrong scope")
)

// Manager signs and verifies the platform's JWTs (HS256).
type Manager struct {
	secret    []byte
	accessTTL time.Duration
	mfaTTL    time.Duration
}

func NewManager(secret string, accessTTL time.Duration) (*Manager, error) {
	if len(secret) < 32 {
		return nil, errors.New("token: jwt secret must be at least 32 characters")
	}
	if accessTTL <= 0 {
		return nil, errors.New("token: access ttl must be positive")
	}
	return &Manager{secret: []byte(secret), accessTTL: accessTTL, mfaTTL: 5 * time.Minute}, nil
}

func (m *Manager) AccessTTL() time.Duration { return m.accessTTL }

type jwtClaims struct {
	jwt.RegisteredClaims
	SessionID string `json:"sid,omitempty"`
	Scope     Scope  `json:"scope"`
}

func (m *Manager) sign(userID, sessionID string, scope Scope, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := jwtClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuer,
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
		SessionID: sessionID,
		Scope:     scope,
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
	if err != nil {
		return "", fmt.Errorf("token: sign: %w", err)
	}
	return signed, nil
}

// Access issues a short-lived API token bound to a session.
func (m *Manager) Access(userID, sessionID string) (string, error) {
	return m.sign(userID, sessionID, ScopeAccess, m.accessTTL)
}

// MFA issues a token whose only power is completing the MFA challenge.
func (m *Manager) MFA(userID string) (string, error) {
	return m.sign(userID, "", ScopeMFA, m.mfaTTL)
}

// Parse verifies signature, expiry and issuer, and requires wantScope.
func (m *Manager) Parse(tokenStr string, wantScope Scope) (Claims, error) {
	var claims jwtClaims
	_, err := jwt.ParseWithClaims(tokenStr, &claims,
		func(t *jwt.Token) (any, error) { return m.secret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(issuer),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return Claims{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if claims.Scope != wantScope {
		return Claims{}, ErrScope
	}
	return Claims{UserID: claims.Subject, SessionID: claims.SessionID, Scope: claims.Scope}, nil
}
