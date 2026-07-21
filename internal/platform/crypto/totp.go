package crypto

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" // #nosec G505 -- RFC 6238 mandates HMAC-SHA1 for TOTP interop
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// TOTP per RFC 6238 (30s period, 6 digits, HMAC-SHA1), compatible with
// every mainstream authenticator app.
const (
	totpPeriod = 30 * time.Second
	totpDigits = 6
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

func GenerateTOTPSecret() (string, error) {
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("crypto: read totp secret: %w", err)
	}
	return b32.EncodeToString(raw), nil
}

// TOTPCode computes the 6-digit code for a secret at time t. Exposed so
// tests can act as the authenticator side.
func TOTPCode(secret string, t time.Time) (string, error) {
	return totpCode(secret, t)
}

func totpCode(secret string, t time.Time) (string, error) {
	key, err := b32.DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return "", fmt.Errorf("crypto: invalid totp secret: %w", err)
	}
	counter := uint64(t.Unix()) / uint64(totpPeriod.Seconds()) // #nosec G115 -- unix time is positive
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)

	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)

	offset := sum[len(sum)-1] & 0x0f
	code := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", code%1_000_000), nil
}

// VerifyTOTP accepts the current period plus one period of clock skew in
// each direction, comparing in constant time.
func VerifyTOTP(secret, code string, now time.Time) bool {
	code = strings.TrimSpace(code)
	if len(code) != totpDigits {
		return false
	}
	ok := false
	for _, skew := range []time.Duration{0, -totpPeriod, totpPeriod} {
		want, err := totpCode(secret, now.Add(skew))
		if err != nil {
			return false
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			ok = true
		}
	}
	return ok
}

// TOTPProvisioningURI renders the otpauth:// URI encoded into enrollment QR
// codes by the frontend.
func TOTPProvisioningURI(issuer, account, secret string) string {
	return fmt.Sprintf("otpauth://totp/%s:%s?secret=%s&issuer=%s&digits=%d&period=%d",
		url.PathEscape(issuer), url.PathEscape(account), secret, url.QueryEscape(issuer),
		totpDigits, int(totpPeriod.Seconds()))
}
