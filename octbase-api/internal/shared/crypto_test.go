package shared

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	t.Setenv("OCTBASE_SCM_ENC_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))

	plain := "ghp_supersecrettoken"
	ct, err := EncryptSecret(plain)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if ct == plain || ct == "" {
		t.Fatalf("ciphertext not produced: %q", ct)
	}

	// Encrypting again yields a different ciphertext (random nonce).
	ct2, err := EncryptSecret(plain)
	if err != nil {
		t.Fatalf("encrypt2: %v", err)
	}
	if ct == ct2 {
		t.Errorf("expected distinct ciphertexts for repeated encryption")
	}

	got, err := DecryptSecret(ct)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if got != plain {
		t.Errorf("round-trip = %q, want %q", got, plain)
	}
}

func TestEncrypt_EmptyIsNoop(t *testing.T) {
	// Empty plaintext needs no key configured.
	ct, err := EncryptSecret("")
	if err != nil {
		t.Fatalf("encrypt empty: %v", err)
	}
	if ct != "" {
		t.Errorf("empty plaintext = %q, want empty", ct)
	}
	pt, err := DecryptSecret("")
	if err != nil || pt != "" {
		t.Errorf("decrypt empty = %q, %v", pt, err)
	}
}

func TestEncrypt_MissingKey(t *testing.T) {
	t.Setenv("OCTBASE_SCM_ENC_KEY", "")
	_, err := EncryptSecret("token")
	if !errors.Is(err, ErrEncKeyMissing) {
		t.Errorf("err = %v, want ErrEncKeyMissing", err)
	}
}

func TestEncrypt_BadKeyLength(t *testing.T) {
	t.Setenv("OCTBASE_SCM_ENC_KEY", base64.StdEncoding.EncodeToString(make([]byte, 16)))
	_, err := EncryptSecret("token")
	if err == nil || !strings.Contains(err.Error(), "32 bytes") {
		t.Errorf("err = %v, want 32-byte length error", err)
	}
}

func TestDecrypt_Tampered(t *testing.T) {
	t.Setenv("OCTBASE_SCM_ENC_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	ct, err := EncryptSecret("token")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if _, err := DecryptSecret(ct + "garbage"); err == nil {
		t.Errorf("expected error decrypting tampered ciphertext")
	}
}
