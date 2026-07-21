package crypto

import (
	"strings"
	"testing"
	"time"
)

func TestPasswordRoundTrip(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("unexpected hash format: %s", hash)
	}
	ok, rehash, err := VerifyPassword("correct horse battery staple", hash)
	if err != nil || !ok {
		t.Fatalf("verify correct password: ok=%v err=%v", ok, err)
	}
	if rehash {
		t.Error("fresh hash should not need rehash")
	}
	ok, _, err = VerifyPassword("wrong", hash)
	if err != nil || ok {
		t.Fatalf("verify wrong password: ok=%v err=%v", ok, err)
	}
}

func TestPasswordRehashDetection(t *testing.T) {
	// Hash generated with weaker parameters than current policy.
	old := "$argon2id$v=19$m=8,t=1,p=1$c2FsdHNhbHRzYWx0c2FsdA$K/BBZ1FW6XZmkOTAM8sGDl2v0dtvKMUzVKZa8/y1WdQ"
	_, _, err := VerifyPassword("x", old)
	if err != nil {
		t.Fatalf("old-params hash should parse: %v", err)
	}
}

func TestPasswordMalformed(t *testing.T) {
	for _, h := range []string{"", "plaintext", "$argon2i$v=19$m=8,t=1,p=1$x$y"} {
		if _, _, err := VerifyPassword("x", h); err == nil {
			t.Errorf("hash %q accepted", h)
		}
	}
}

func TestSealerRoundTrip(t *testing.T) {
	s, err := NewSealer("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("NewSealer: %v", err)
	}
	sealed, err := s.Seal("JBSWY3DPEHPK3PXP")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	got, err := s.Open(sealed)
	if err != nil || got != "JBSWY3DPEHPK3PXP" {
		t.Fatalf("Open = %q, %v", got, err)
	}
	if _, err := s.Open(sealed[:len(sealed)-2] + "xx"); err == nil {
		t.Error("tampered ciphertext accepted")
	}
	if _, err := NewSealer("short"); err == nil {
		t.Error("short key accepted")
	}
}

func TestTOTPRFCVectors(t *testing.T) {
	// RFC 6238 Appendix B vectors (SHA-1), truncated to 6 digits.
	secret := b32.EncodeToString([]byte("12345678901234567890"))
	vectors := map[int64]string{
		59:          "287082",
		1111111109:  "081804",
		1234567890:  "005924",
		20000000000: "353130",
	}
	for unix, want := range vectors {
		got, err := totpCode(secret, time.Unix(unix, 0).UTC())
		if err != nil {
			t.Fatalf("totpCode(%d): %v", unix, err)
		}
		if got != want {
			t.Errorf("totpCode(%d) = %s, want %s", unix, got, want)
		}
	}
}

func TestVerifyTOTPSkew(t *testing.T) {
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("GenerateTOTPSecret: %v", err)
	}
	now := time.Now()
	prev, _ := totpCode(secret, now.Add(-30*time.Second))
	next, _ := totpCode(secret, now.Add(30*time.Second))
	cur, _ := totpCode(secret, now)
	for _, code := range []string{cur, prev, next} {
		if !VerifyTOTP(secret, code, now) {
			t.Errorf("code %s within skew rejected", code)
		}
	}
	far, _ := totpCode(secret, now.Add(120*time.Second))
	if VerifyTOTP(secret, far, now) && far != cur && far != prev && far != next {
		t.Error("code outside skew accepted")
	}
	if VerifyTOTP(secret, "12345", now) {
		t.Error("wrong-length code accepted")
	}
}

func TestRandomTokenAndHash(t *testing.T) {
	a, err := RandomToken(32)
	if err != nil {
		t.Fatalf("RandomToken: %v", err)
	}
	b, _ := RandomToken(32)
	if a == b {
		t.Error("two random tokens are equal")
	}
	if len(HashToken(a)) != 32 {
		t.Error("HashToken should return 32 bytes")
	}
}
