package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
)

// Sealer encrypts small secrets at rest (TOTP seeds, future credentials)
// with AES-256-GCM. The key is derived from the configured encryption
// secret via SHA-256 so operators can supply any sufficiently long string.
type Sealer struct {
	aead cipher.AEAD
}

var ErrSealerKey = errors.New("crypto: encryption key must be at least 16 characters")

func NewSealer(secret string) (*Sealer, error) {
	if len(secret) < 16 {
		return nil, ErrSealerKey
	}
	key := sha256.Sum256([]byte(secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("crypto: init cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: init gcm: %w", err)
	}
	return &Sealer{aead: aead}, nil
}

func (s *Sealer) Seal(plaintext string) (string, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("crypto: read nonce: %w", err)
	}
	sealed := s.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.RawStdEncoding.EncodeToString(sealed), nil
}

func (s *Sealer) Open(sealed string) (string, error) {
	raw, err := base64.RawStdEncoding.DecodeString(sealed)
	if err != nil {
		return "", fmt.Errorf("crypto: decode sealed value: %w", err)
	}
	if len(raw) < s.aead.NonceSize() {
		return "", errors.New("crypto: sealed value too short")
	}
	nonce, ct := raw[:s.aead.NonceSize()], raw[s.aead.NonceSize():]
	pt, err := s.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", errors.New("crypto: decryption failed")
	}
	return string(pt), nil
}
