package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/runix/runix/internal/modules/users"
	"github.com/runix/runix/internal/platform/authn"
	"github.com/runix/runix/internal/platform/crypto"
	"github.com/runix/runix/internal/platform/ratelimit"
	"github.com/runix/runix/internal/platform/token"
)

// UserStore is the slice of the users module this module needs;
// users.Repository satisfies it and the app wires it in.
type UserStore interface {
	GetByID(ctx context.Context, id string) (users.User, error)
	GetByIdentifier(ctx context.Context, identifier string) (users.User, error)
	UpdatePassword(ctx context.Context, id, hash string, mustChange bool) error
	SetTOTP(ctx context.Context, id string, enabled bool, secretEnc string) error
}

// RoleChecker lets the auth layer flag admin principals; rbac provides it.
type RoleChecker interface {
	HasRole(ctx context.Context, userID, roleKey string) (bool, error)
}

type Config struct {
	RefreshTTL  time.Duration
	RememberTTL time.Duration
	Issuer      string
}

type Service struct {
	repo    Repository
	userss  UserStore
	roles   RoleChecker
	tokens  *token.Manager
	sealer  *crypto.Sealer
	limiter *ratelimit.Limiter
	cfg     Config
	log     *slog.Logger

	// dummyHash equalizes timing between unknown users and wrong passwords.
	dummyHash string
}

func NewService(repo Repository, userStore UserStore, roles RoleChecker,
	tokens *token.Manager, sealer *crypto.Sealer, cfg Config, log *slog.Logger) (*Service, error) {
	dummy, err := crypto.HashPassword("runix-timing-equalizer")
	if err != nil {
		return nil, fmt.Errorf("auth: init dummy hash: %w", err)
	}
	if cfg.Issuer == "" {
		cfg.Issuer = "Runix"
	}
	return &Service{
		repo:      repo,
		userss:    userStore,
		roles:     roles,
		tokens:    tokens,
		sealer:    sealer,
		limiter:   ratelimit.New(10, time.Minute),
		cfg:       cfg,
		log:       log,
		dummyHash: dummy,
	}, nil
}

type TokenPair struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ExpiresIn    int    `json:"expiresIn"`
}

type LoginResult struct {
	MFARequired bool
	MFAToken    string
	Tokens      TokenPair
	User        users.User
}

type ClientInfo struct {
	IP        string
	UserAgent string
}

func (s *Service) Login(ctx context.Context, identifier, password string, remember bool, client ClientInfo) (LoginResult, error) {
	if !s.limiter.Allow("ip:"+client.IP) || !s.limiter.Allow("id:"+strings.ToLower(identifier)) {
		return LoginResult{}, ErrRateLimited
	}

	u, lookupErr := s.userss.GetByIdentifier(ctx, identifier)
	hash := s.dummyHash
	if lookupErr == nil {
		hash = u.PasswordHash
	}
	ok, needsRehash, err := crypto.VerifyPassword(password, hash)
	if err != nil {
		return LoginResult{}, fmt.Errorf("auth: verify: %w", err)
	}
	if lookupErr != nil || !ok {
		return LoginResult{}, ErrBadCredentials
	}
	if !u.IsActive {
		return LoginResult{}, ErrUserDisabled
	}
	if needsRehash {
		if newHash, err := crypto.HashPassword(password); err == nil {
			if err := s.userss.UpdatePassword(ctx, u.ID, newHash, u.MustChangePassword); err != nil {
				s.log.Warn("password rehash failed", "user", u.ID, "err", err)
			}
		}
	}

	if u.TOTPEnabled {
		mfaToken, err := s.tokens.MFA(u.ID)
		if err != nil {
			return LoginResult{}, fmt.Errorf("auth: issue mfa token: %w", err)
		}
		return LoginResult{MFARequired: true, MFAToken: mfaToken, User: u}, nil
	}

	pair, err := s.startSession(ctx, u.ID, remember, client)
	if err != nil {
		return LoginResult{}, err
	}
	return LoginResult{Tokens: pair, User: u}, nil
}

// VerifyMFA completes a login that returned MFARequired, accepting a TOTP
// code or an unused recovery code.
func (s *Service) VerifyMFA(ctx context.Context, mfaToken, code string, remember bool, client ClientInfo) (LoginResult, error) {
	claims, err := s.tokens.Parse(mfaToken, token.ScopeMFA)
	if err != nil {
		return LoginResult{}, ErrBadCredentials
	}
	if !s.limiter.Allow("mfa:" + claims.UserID) {
		return LoginResult{}, ErrRateLimited
	}
	u, err := s.userss.GetByID(ctx, claims.UserID)
	if err != nil || !u.IsActive || !u.TOTPEnabled {
		return LoginResult{}, ErrBadCredentials
	}

	if !s.checkTOTP(u, code) {
		used, err := s.repo.ConsumeRecoveryCode(ctx, u.ID, crypto.HashToken(normalizeRecoveryCode(code)))
		if err != nil {
			return LoginResult{}, err
		}
		if !used {
			return LoginResult{}, ErrMFACode
		}
	}

	pair, err := s.startSession(ctx, u.ID, remember, client)
	if err != nil {
		return LoginResult{}, err
	}
	return LoginResult{Tokens: pair, User: u}, nil
}

func (s *Service) checkTOTP(u users.User, code string) bool {
	if u.TOTPSecretEnc == "" {
		return false
	}
	secret, err := s.sealer.Open(u.TOTPSecretEnc)
	if err != nil {
		s.log.Error("totp secret unsealing failed", "user", u.ID, "err", err)
		return false
	}
	return crypto.VerifyTOTP(secret, code, time.Now())
}

func (s *Service) startSession(ctx context.Context, userID string, remember bool, client ClientInfo) (TokenPair, error) {
	opaque, err := crypto.RandomToken(32)
	if err != nil {
		return TokenPair{}, fmt.Errorf("auth: refresh token: %w", err)
	}
	refresh := refreshTokenPrefix + opaque
	ttl := s.cfg.RefreshTTL
	if remember {
		ttl = s.cfg.RememberTTL
	}
	sess, err := s.repo.CreateSession(ctx, Session{
		UserID:      userID,
		RefreshHash: crypto.HashToken(refresh),
		UserAgent:   client.UserAgent,
		IP:          client.IP,
		Remember:    remember,
		ExpiresAt:   time.Now().Add(ttl),
	})
	if err != nil {
		return TokenPair{}, fmt.Errorf("auth: create session: %w", err)
	}
	access, err := s.tokens.Access(userID, sess.ID)
	if err != nil {
		return TokenPair{}, fmt.Errorf("auth: issue access token: %w", err)
	}
	return TokenPair{
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresIn:    int(s.tokens.AccessTTL().Seconds()),
	}, nil
}

// Refresh rotates a refresh token. Presenting an already-rotated token is
// treated as theft: every session of the user is revoked.
func (s *Service) Refresh(ctx context.Context, refreshToken string, client ClientInfo) (TokenPair, error) {
	sess, err := s.repo.SessionByRefreshHash(ctx, crypto.HashToken(refreshToken))
	if err != nil {
		return TokenPair{}, ErrSessionInvalid
	}
	now := time.Now()
	if sess.RevokedAt != nil {
		if sess.ReplacedBy != "" {
			s.log.Warn("refresh token reuse detected, revoking all sessions", "user", sess.UserID)
			if err := s.repo.RevokeAllSessions(ctx, sess.UserID); err != nil {
				s.log.Error("revoke all sessions failed", "user", sess.UserID, "err", err)
			}
			return TokenPair{}, ErrTokenReuse
		}
		return TokenPair{}, ErrSessionInvalid
	}
	if !sess.usable(now) {
		return TokenPair{}, ErrSessionInvalid
	}
	u, err := s.userss.GetByID(ctx, sess.UserID)
	if err != nil || !u.IsActive {
		return TokenPair{}, ErrSessionInvalid
	}

	pair, err := s.startSession(ctx, sess.UserID, sess.Remember, client)
	if err != nil {
		return TokenPair{}, err
	}
	newSess, err := s.repo.SessionByRefreshHash(ctx, crypto.HashToken(pair.RefreshToken))
	if err != nil {
		return TokenPair{}, fmt.Errorf("auth: locate rotated session: %w", err)
	}
	if err := s.repo.RevokeSession(ctx, sess.ID, newSess.ID); err != nil {
		return TokenPair{}, fmt.Errorf("auth: revoke rotated session: %w", err)
	}
	return pair, nil
}

func (s *Service) Logout(ctx context.Context, refreshToken string) error {
	sess, err := s.repo.SessionByRefreshHash(ctx, crypto.HashToken(refreshToken))
	if err != nil {
		return nil // logging out an unknown token is a no-op, not an error
	}
	return s.repo.RevokeSession(ctx, sess.ID, "")
}

// AuthenticateAccess resolves a bearer credential (JWT access token or PAT)
// into a Principal.
func (s *Service) AuthenticateAccess(ctx context.Context, bearer string) (authn.Principal, error) {
	if strings.HasPrefix(bearer, patPrefix) {
		return s.authenticatePAT(ctx, bearer)
	}
	claims, err := s.tokens.Parse(bearer, token.ScopeAccess)
	if err != nil {
		return authn.Principal{}, ErrSessionInvalid
	}
	sess, err := s.repo.SessionByID(ctx, claims.SessionID)
	if err != nil || !sess.usable(time.Now()) || sess.UserID != claims.UserID {
		return authn.Principal{}, ErrSessionInvalid
	}
	u, err := s.userss.GetByID(ctx, claims.UserID)
	if err != nil || !u.IsActive {
		return authn.Principal{}, ErrSessionInvalid
	}
	return s.principal(ctx, u, claims.SessionID, authn.MethodJWT), nil
}

func (s *Service) authenticatePAT(ctx context.Context, tokenStr string) (authn.Principal, error) {
	pat, err := s.repo.PATByHash(ctx, crypto.HashToken(tokenStr))
	if err != nil {
		return authn.Principal{}, ErrSessionInvalid
	}
	now := time.Now()
	if pat.RevokedAt != nil || (pat.ExpiresAt != nil && now.After(*pat.ExpiresAt)) {
		return authn.Principal{}, ErrSessionInvalid
	}
	u, err := s.userss.GetByID(ctx, pat.UserID)
	if err != nil || !u.IsActive {
		return authn.Principal{}, ErrSessionInvalid
	}
	if err := s.repo.TouchPAT(ctx, pat.ID); err != nil {
		s.log.Warn("touch pat failed", "pat", pat.ID, "err", err)
	}
	return s.principal(ctx, u, "", authn.MethodPAT), nil
}

func (s *Service) principal(ctx context.Context, u users.User, sessionID string, method authn.Method) authn.Principal {
	p := authn.Principal{
		UserID: u.ID, Username: u.Username, SessionID: sessionID, Method: method,
	}
	if s.roles != nil {
		if isAdmin, err := s.roles.HasRole(ctx, u.ID, "admin"); err == nil {
			p.IsAdmin = isAdmin
		}
	}
	return p
}

// MFA lifecycle -------------------------------------------------------------

type MFASetup struct {
	Secret string `json:"secret"`
	URI    string `json:"uri"`
}

// SetupMFA stores a fresh (disabled) TOTP secret and returns it for QR
// display. Enabling requires proving possession via EnableMFA.
func (s *Service) SetupMFA(ctx context.Context, userID string) (MFASetup, error) {
	u, err := s.userss.GetByID(ctx, userID)
	if err != nil {
		return MFASetup{}, err
	}
	if u.TOTPEnabled {
		return MFASetup{}, fmt.Errorf("%w: totp already enabled", ErrMFAState)
	}
	secret, err := crypto.GenerateTOTPSecret()
	if err != nil {
		return MFASetup{}, fmt.Errorf("auth: generate totp secret: %w", err)
	}
	sealed, err := s.sealer.Seal(secret)
	if err != nil {
		return MFASetup{}, fmt.Errorf("auth: seal totp secret: %w", err)
	}
	if err := s.userss.SetTOTP(ctx, userID, false, sealed); err != nil {
		return MFASetup{}, err
	}
	return MFASetup{
		Secret: secret,
		URI:    crypto.TOTPProvisioningURI(s.cfg.Issuer, u.Username, secret),
	}, nil
}

// EnableMFA turns TOTP on after the user proves the authenticator works,
// and returns single-use recovery codes (shown exactly once).
func (s *Service) EnableMFA(ctx context.Context, userID, code string) ([]string, error) {
	u, err := s.userss.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if u.TOTPEnabled || u.TOTPSecretEnc == "" {
		return nil, fmt.Errorf("%w: setup required first", ErrMFAState)
	}
	if !s.checkTOTP(u, code) {
		return nil, ErrMFACode
	}
	if err := s.userss.SetTOTP(ctx, userID, true, u.TOTPSecretEnc); err != nil {
		return nil, err
	}
	return s.regenerateRecoveryCodes(ctx, userID)
}

func (s *Service) DisableMFA(ctx context.Context, userID, password, code string) error {
	u, err := s.userss.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if !u.TOTPEnabled {
		return fmt.Errorf("%w: totp not enabled", ErrMFAState)
	}
	ok, _, err := crypto.VerifyPassword(password, u.PasswordHash)
	if err != nil || !ok {
		return ErrBadCredentials
	}
	if !s.checkTOTP(u, code) {
		return ErrMFACode
	}
	if err := s.userss.SetTOTP(ctx, userID, false, ""); err != nil {
		return err
	}
	return s.repo.ReplaceRecoveryCodes(ctx, userID, nil)
}

func (s *Service) RegenerateRecoveryCodes(ctx context.Context, userID, code string) ([]string, error) {
	u, err := s.userss.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if !u.TOTPEnabled {
		return nil, fmt.Errorf("%w: totp not enabled", ErrMFAState)
	}
	if !s.checkTOTP(u, code) {
		return nil, ErrMFACode
	}
	return s.regenerateRecoveryCodes(ctx, userID)
}

func (s *Service) regenerateRecoveryCodes(ctx context.Context, userID string) ([]string, error) {
	codes := make([]string, 0, recoveryCodeCount)
	hashes := make([][]byte, 0, recoveryCodeCount)
	for range recoveryCodeCount {
		raw, err := crypto.RandomToken(6)
		if err != nil {
			return nil, fmt.Errorf("auth: generate recovery code: %w", err)
		}
		code := strings.ToLower(raw[:4] + "-" + raw[4:8])
		codes = append(codes, code)
		hashes = append(hashes, crypto.HashToken(normalizeRecoveryCode(code)))
	}
	if err := s.repo.ReplaceRecoveryCodes(ctx, userID, hashes); err != nil {
		return nil, err
	}
	return codes, nil
}

func normalizeRecoveryCode(code string) string {
	return strings.ToLower(strings.TrimSpace(code))
}

// Sessions & PATs -----------------------------------------------------------

func (s *Service) Sessions(ctx context.Context, userID string) ([]Session, error) {
	return s.repo.SessionsOfUser(ctx, userID)
}

func (s *Service) RevokeSessionByID(ctx context.Context, userID, sessionID string) error {
	sess, err := s.repo.SessionByID(ctx, sessionID)
	if err != nil {
		return err
	}
	if sess.UserID != userID {
		return ErrNotFound
	}
	return s.repo.RevokeSession(ctx, sessionID, "")
}

type CreatedPAT struct {
	PAT   PAT    `json:"token"`
	Plain string `json:"plainToken"`
}

func (s *Service) CreatePAT(ctx context.Context, userID, name string, expiresAt *time.Time) (CreatedPAT, error) {
	if strings.TrimSpace(name) == "" || len(name) > 128 {
		return CreatedPAT{}, fmt.Errorf("%w: token name must be 1-128 characters", ErrConflict)
	}
	opaque, err := crypto.RandomToken(32)
	if err != nil {
		return CreatedPAT{}, fmt.Errorf("auth: generate pat: %w", err)
	}
	plain := patPrefix + opaque
	pat, err := s.repo.CreatePAT(ctx, PAT{
		UserID: userID, Name: name, TokenHash: crypto.HashToken(plain), ExpiresAt: expiresAt,
	})
	if err != nil {
		return CreatedPAT{}, err
	}
	return CreatedPAT{PAT: pat, Plain: plain}, nil
}

func (s *Service) PATs(ctx context.Context, userID string) ([]PAT, error) {
	return s.repo.PATsOfUser(ctx, userID)
}

func (s *Service) RevokePAT(ctx context.Context, userID, id string) error {
	return s.repo.RevokePAT(ctx, userID, id)
}

// CleanupExpiredSessions is run periodically by the app worker.
func (s *Service) CleanupExpiredSessions(ctx context.Context) {
	n, err := s.repo.DeleteExpiredSessions(ctx, time.Now().Add(-24*time.Hour))
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			s.log.Error("session cleanup failed", "err", err)
		}
		return
	}
	if n > 0 {
		s.log.Info("cleaned up expired sessions", "count", n)
	}
}
