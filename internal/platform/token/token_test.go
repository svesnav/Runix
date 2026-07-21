package token

import (
	"errors"
	"testing"
	"time"
)

const secret = "0123456789abcdef0123456789abcdef"

func TestAccessRoundTrip(t *testing.T) {
	m, err := NewManager(secret, 15*time.Minute)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	tok, err := m.Access("user-1", "sess-1")
	if err != nil {
		t.Fatalf("Access: %v", err)
	}
	claims, err := m.Parse(tok, ScopeAccess)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if claims.UserID != "user-1" || claims.SessionID != "sess-1" {
		t.Errorf("claims = %+v", claims)
	}
}

func TestScopeEnforced(t *testing.T) {
	m, _ := NewManager(secret, 15*time.Minute)
	mfa, _ := m.MFA("user-1")
	if _, err := m.Parse(mfa, ScopeAccess); !errors.Is(err, ErrScope) {
		t.Errorf("mfa token accepted as access token: %v", err)
	}
	if _, err := m.Parse(mfa, ScopeMFA); err != nil {
		t.Errorf("mfa token rejected for mfa scope: %v", err)
	}
}

func TestExpiryEnforced(t *testing.T) {
	m, _ := NewManager(secret, -time.Minute)
	// NewManager rejects non-positive TTLs, so build one manually.
	if m != nil {
		t.Fatal("negative ttl accepted")
	}
	m2, _ := NewManager(secret, time.Millisecond)
	tok, _ := m2.Access("u", "s")
	time.Sleep(5 * time.Millisecond)
	if _, err := m2.Parse(tok, ScopeAccess); !errors.Is(err, ErrInvalid) {
		t.Errorf("expired token accepted: %v", err)
	}
}

func TestTamperRejected(t *testing.T) {
	m, _ := NewManager(secret, 15*time.Minute)
	other, _ := NewManager("another-secret-another-secret-32", 15*time.Minute)
	tok, _ := other.Access("u", "s")
	if _, err := m.Parse(tok, ScopeAccess); !errors.Is(err, ErrInvalid) {
		t.Errorf("foreign-signed token accepted: %v", err)
	}
	if _, err := NewManager("short", time.Minute); err == nil {
		t.Error("short secret accepted")
	}
}
