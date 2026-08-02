package shared

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
)

// ErrEncKeyMissing is returned when a secret must be encrypted but its
// configured environment variable is not set.
var ErrEncKeyMissing = errors.New("encryption key env var is not configured")

// encKey loads a 32-byte AES-256 key from the given environment variable. The
// value may be supplied as base64 or hex; either way it must decode to
// exactly 32 bytes. Returns ErrEncKeyMissing when the variable is unset.
func encKey(envVar string) ([]byte, error) {
	raw := os.Getenv(envVar)
	if raw == "" {
		return nil, fmt.Errorf("%s: %w", envVar, ErrEncKeyMissing)
	}
	if k, err := base64.StdEncoding.DecodeString(raw); err == nil && len(k) == 32 {
		return k, nil
	}
	if k, err := hex.DecodeString(raw); err == nil && len(k) == 32 {
		return k, nil
	}
	return nil, fmt.Errorf("%s must decode (base64 or hex) to 32 bytes", envVar)
}

// encryptWithKey encrypts plaintext with AES-256-GCM using the key from
// envVar and returns base64(nonce||ciphertext). An empty plaintext is
// returned unchanged and requires no key, so callers without a secret to
// store do not need encryption configured.
func encryptWithKey(envVar, plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	key, err := encKey(envVar)
	if err != nil {
		return "", err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ct := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ct), nil
}

// decryptWithKey reverses encryptWithKey. An empty input returns an empty string.
func decryptWithKey(envVar, encoded string) (string, error) {
	if encoded == "" {
		return "", nil
	}
	key, err := encKey(envVar)
	if err != nil {
		return "", err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decode secret: %w", err)
	}
	ns := gcm.NonceSize()
	if len(raw) < ns {
		return "", errors.New("ciphertext too short")
	}
	nonce, ct := raw[:ns], raw[ns:]
	pt, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt secret: %w", err)
	}
	return string(pt), nil
}

// EncryptSecret encrypts plaintext with AES-256-GCM using the key from
// OCTBASE_SCM_ENC_KEY and returns base64(nonce||ciphertext). Used for SCM
// (GitHub/Bitbucket) access/refresh tokens.
func EncryptSecret(plaintext string) (string, error) {
	return encryptWithKey("OCTBASE_SCM_ENC_KEY", plaintext)
}

// DecryptSecret reverses EncryptSecret. An empty input returns an empty string.
func DecryptSecret(encoded string) (string, error) {
	return decryptWithKey("OCTBASE_SCM_ENC_KEY", encoded)
}

// EncryptMFASecret encrypts a TOTP secret with AES-256-GCM using the key from
// OCTBASE_MFA_ENC_KEY. A distinct key from OCTBASE_SCM_ENC_KEY keeps the blast
// radius of a leaked/rotated key scoped to one subsystem.
func EncryptMFASecret(plaintext string) (string, error) {
	return encryptWithKey("OCTBASE_MFA_ENC_KEY", plaintext)
}

// DecryptMFASecret reverses EncryptMFASecret. An empty input returns an empty string.
func DecryptMFASecret(encoded string) (string, error) {
	return decryptWithKey("OCTBASE_MFA_ENC_KEY", encoded)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
