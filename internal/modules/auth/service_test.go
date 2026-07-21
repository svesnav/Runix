package auth

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/runix/runix/internal/modules/users"
	"github.com/runix/runix/internal/platform/crypto"
	"github.com/runix/runix/internal/platform/token"
)

type memRepo struct {
	sessions map[string]*Session
	recovery map[string][][]byte
	pats     map[string]*PAT
}

func newMemRepo() *memRepo {
	return &memRepo{
		sessions: map[string]*Session{},
		recovery: map[string][][]byte{},
		pats:     map[string]*PAT{},
	}
}

func (m *memRepo) CreateSession(_ context.Context, s Session) (Session, error) {
	s.ID = uuid.NewString()
	s.CreatedAt = time.Now()
	s.LastUsedAt = time.Now()
	m.sessions[s.ID] = &s
	return s, nil
}

func (m *memRepo) SessionByRefreshHash(_ context.Context, hash []byte) (Session, error) {
	for _, s := range m.sessions {
		if string(s.RefreshHash) == string(hash) {
			return *s, nil
		}
	}
	return Session{}, ErrSessionInvalid
}

func (m *memRepo) SessionByID(_ context.Context, id string) (Session, error) {
	if s, ok := m.sessions[id]; ok {
		return *s, nil
	}
	return Session{}, ErrSessionInvalid
}

func (m *memRepo) SessionsOfUser(_ context.Context, userID string) ([]Session, error) {
	var out []Session
	for _, s := range m.sessions {
		if s.UserID == userID && s.RevokedAt == nil {
			out = append(out, *s)
		}
	}
	return out, nil
}

func (m *memRepo) TouchSession(context.Context, string) error { return nil }

func (m *memRepo) RevokeSession(_ context.Context, id, replacedBy string) error {
	if s, ok := m.sessions[id]; ok && s.RevokedAt == nil {
		now := time.Now()
		s.RevokedAt = &now
		s.ReplacedBy = replacedBy
	}
	return nil
}

func (m *memRepo) RevokeAllSessions(_ context.Context, userID string) error {
	now := time.Now()
	for _, s := range m.sessions {
		if s.UserID == userID && s.RevokedAt == nil {
			s.RevokedAt = &now
		}
	}
	return nil
}

func (m *memRepo) DeleteExpiredSessions(context.Context, time.Time) (int64, error) { return 0, nil }

func (m *memRepo) ReplaceRecoveryCodes(_ context.Context, userID string, hashes [][]byte) error {
	m.recovery[userID] = hashes
	return nil
}

func (m *memRepo) ConsumeRecoveryCode(_ context.Context, userID string, hash []byte) (bool, error) {
	codes := m.recovery[userID]
	for i, h := range codes {
		if string(h) == string(hash) {
			m.recovery[userID] = append(codes[:i], codes[i+1:]...)
			return true, nil
		}
	}
	return false, nil
}

func (m *memRepo) CreatePAT(_ context.Context, t PAT) (PAT, error) {
	t.ID = uuid.NewString()
	t.CreatedAt = time.Now()
	m.pats[t.ID] = &t
	return t, nil
}

func (m *memRepo) PATByHash(_ context.Context, hash []byte) (PAT, error) {
	for _, t := range m.pats {
		if string(t.TokenHash) == string(hash) {
			return *t, nil
		}
	}
	return PAT{}, ErrNotFound
}

func (m *memRepo) PATsOfUser(_ context.Context, userID string) ([]PAT, error) {
	var out []PAT
	for _, t := range m.pats {
		if t.UserID == userID {
			out = append(out, *t)
		}
	}
	return out, nil
}

func (m *memRepo) TouchPAT(context.Context, string) error { return nil }

func (m *memRepo) RevokePAT(_ context.Context, userID, id string) error {
	if t, ok := m.pats[id]; ok && t.UserID == userID {
		now := time.Now()
		t.RevokedAt = &now
		return nil
	}
	return ErrNotFound
}

type memUsers struct {
	byID map[string]*users.User
}

func (m *memUsers) GetByID(_ context.Context, id string) (users.User, error) {
	if u, ok := m.byID[id]; ok {
		return *u, nil
	}
	return users.User{}, users.ErrNotFound
}

func (m *memUsers) GetByIdentifier(_ context.Context, ident string) (users.User, error) {
	for _, u := range m.byID {
		if u.Username == ident || u.Email == ident {
			return *u, nil
		}
	}
	return users.User{}, users.ErrNotFound
}

func (m *memUsers) UpdatePassword(_ context.Context, id, hash string, mustChange bool) error {
	m.byID[id].PasswordHash = hash
	m.byID[id].MustChangePassword = mustChange
	return nil
}

func (m *memUsers) SetTOTP(_ context.Context, id string, enabled bool, secretEnc string) error {
	m.byID[id].TOTPEnabled = enabled
	m.byID[id].TOTPSecretEnc = secretEnc
	return nil
}

const testPassword = "test-password-12345"

func newTestService(t *testing.T) (*Service, *memRepo, *memUsers, string) {
	t.Helper()
	hash, err := crypto.HashPassword(testPassword)
	if err != nil {
		t.Fatal(err)
	}
	userID := uuid.NewString()
	store := &memUsers{byID: map[string]*users.User{
		userID: {ID: userID, Username: "alice", Email: "alice@example.com",
			PasswordHash: hash, IsActive: true},
	}}
	repo := newMemRepo()
	tokens, err := token.NewManager("0123456789abcdef0123456789abcdef", 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	sealer, err := crypto.NewSealer("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc, err := NewService(repo, store, nil, tokens, sealer,
		Config{RefreshTTL: time.Hour, RememberTTL: 24 * time.Hour}, log)
	if err != nil {
		t.Fatal(err)
	}
	return svc, repo, store, userID
}

var testClient = ClientInfo{IP: "127.0.0.1", UserAgent: "test"}

func TestLoginSuccess(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	res, err := svc.Login(context.Background(), "alice", testPassword, false, testClient)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if res.MFARequired || res.Tokens.AccessToken == "" || res.Tokens.RefreshToken == "" {
		t.Fatalf("unexpected result: %+v", res)
	}
	p, err := svc.AuthenticateAccess(context.Background(), res.Tokens.AccessToken)
	if err != nil {
		t.Fatalf("AuthenticateAccess: %v", err)
	}
	if p.Username != "alice" {
		t.Errorf("principal = %+v", p)
	}
}

func TestLoginFailures(t *testing.T) {
	svc, _, store, userID := newTestService(t)
	ctx := context.Background()

	if _, err := svc.Login(ctx, "alice", "wrong-password", false, testClient); !errors.Is(err, ErrBadCredentials) {
		t.Errorf("wrong password: %v", err)
	}
	if _, err := svc.Login(ctx, "nobody", testPassword, false, testClient); !errors.Is(err, ErrBadCredentials) {
		t.Errorf("unknown user: %v", err)
	}
	store.byID[userID].IsActive = false
	if _, err := svc.Login(ctx, "alice", testPassword, false, testClient); !errors.Is(err, ErrUserDisabled) {
		t.Errorf("disabled user: %v", err)
	}
}

func TestLoginRateLimit(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	ctx := context.Background()
	var last error
	for range 15 {
		_, last = svc.Login(ctx, "alice", "wrong", false, testClient)
	}
	if !errors.Is(last, ErrRateLimited) {
		t.Errorf("after 15 failures: %v, want ErrRateLimited", last)
	}
}

func TestRefreshRotationAndReuseDetection(t *testing.T) {
	svc, _, _, userID := newTestService(t)
	ctx := context.Background()

	res, err := svc.Login(ctx, "alice", testPassword, false, testClient)
	if err != nil {
		t.Fatal(err)
	}
	first := res.Tokens.RefreshToken

	second, err := svc.Refresh(ctx, first, testClient)
	if err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	if second.RefreshToken == first {
		t.Fatal("refresh token was not rotated")
	}

	// Replaying the rotated token must nuke every session.
	if _, err := svc.Refresh(ctx, first, testClient); !errors.Is(err, ErrTokenReuse) {
		t.Fatalf("reuse: %v, want ErrTokenReuse", err)
	}
	if _, err := svc.Refresh(ctx, second.RefreshToken, testClient); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("post-reuse refresh: %v, want ErrSessionInvalid", err)
	}
	sessions, _ := svc.Sessions(ctx, userID)
	if len(sessions) != 0 {
		t.Errorf("%d sessions alive after reuse detection, want 0", len(sessions))
	}
}

func TestMFAFlow(t *testing.T) {
	svc, _, store, userID := newTestService(t)
	ctx := context.Background()

	setup, err := svc.SetupMFA(ctx, userID)
	if err != nil {
		t.Fatalf("SetupMFA: %v", err)
	}
	if setup.Secret == "" || setup.URI == "" {
		t.Fatalf("empty setup: %+v", setup)
	}
	if store.byID[userID].TOTPSecretEnc == setup.Secret {
		t.Fatal("totp secret stored unencrypted")
	}

	code, err := currentTOTP(setup.Secret)
	if err != nil {
		t.Fatal(err)
	}
	recovery, err := svc.EnableMFA(ctx, userID, code)
	if err != nil {
		t.Fatalf("EnableMFA: %v", err)
	}
	if len(recovery) != recoveryCodeCount {
		t.Fatalf("%d recovery codes, want %d", len(recovery), recoveryCodeCount)
	}

	res, err := svc.Login(ctx, "alice", testPassword, false, testClient)
	if err != nil {
		t.Fatal(err)
	}
	if !res.MFARequired || res.MFAToken == "" {
		t.Fatalf("mfa not required after enable: %+v", res)
	}

	if _, err := svc.VerifyMFA(ctx, res.MFAToken, "000000", false, testClient); !errors.Is(err, ErrMFACode) {
		t.Errorf("bad code: %v", err)
	}
	code, _ = currentTOTP(setup.Secret)
	done, err := svc.VerifyMFA(ctx, res.MFAToken, code, false, testClient)
	if err != nil {
		t.Fatalf("VerifyMFA: %v", err)
	}
	if done.Tokens.AccessToken == "" {
		t.Fatal("no tokens after mfa")
	}

	// Recovery code works exactly once.
	res2, _ := svc.Login(ctx, "alice", testPassword, false, testClient)
	if _, err := svc.VerifyMFA(ctx, res2.MFAToken, recovery[0], false, testClient); err != nil {
		t.Fatalf("recovery code rejected: %v", err)
	}
	res3, _ := svc.Login(ctx, "alice", testPassword, false, testClient)
	if _, err := svc.VerifyMFA(ctx, res3.MFAToken, recovery[0], false, testClient); !errors.Is(err, ErrMFACode) {
		t.Errorf("reused recovery code accepted: %v", err)
	}
}

func TestPATLifecycle(t *testing.T) {
	svc, _, _, userID := newTestService(t)
	ctx := context.Background()

	created, err := svc.CreatePAT(ctx, userID, "ci-token", nil)
	if err != nil {
		t.Fatalf("CreatePAT: %v", err)
	}
	p, err := svc.AuthenticateAccess(ctx, created.Plain)
	if err != nil {
		t.Fatalf("PAT auth: %v", err)
	}
	if p.Method != "pat" || p.UserID != userID {
		t.Errorf("principal = %+v", p)
	}
	if err := svc.RevokePAT(ctx, userID, created.PAT.ID); err != nil {
		t.Fatalf("RevokePAT: %v", err)
	}
	if _, err := svc.AuthenticateAccess(ctx, created.Plain); !errors.Is(err, ErrSessionInvalid) {
		t.Errorf("revoked PAT accepted: %v", err)
	}
}

// currentTOTP mirrors the authenticator app side for tests.
func currentTOTP(secret string) (string, error) {
	return crypto.TOTPCode(secret, time.Now())
}
