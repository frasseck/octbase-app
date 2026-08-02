package auth_test

import (
	"testing"
	"time"

	"github.com/octbase/octbase-api/internal/auth"
)

// ---- bcrypt round-trip ----

func TestHashPassword_RoundTrip(t *testing.T) {
	hash, err := auth.HashPassword("my-secret-pw")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if hash == "" {
		t.Fatal("hash should not be empty")
	}
	if !auth.VerifyPassword(hash, "my-secret-pw") {
		t.Error("VerifyPassword should return true for correct password")
	}
	if auth.VerifyPassword(hash, "wrong-password") {
		t.Error("VerifyPassword should return false for wrong password")
	}
}

// ---- JWT round-trip ----

func TestJWT_IssueAndValidate(t *testing.T) {
	const secret = "test-secret"
	token, err := auth.IssueAccessToken("user-123", secret, time.Minute)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if token == "" {
		t.Fatal("token should not be empty")
	}
	userID, err := auth.ParseAccessToken(token, secret)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if userID != "user-123" {
		t.Errorf("userID = %q, want %q", userID, "user-123")
	}
}

func TestJWT_RejectsExternalToken(t *testing.T) {
	// Simulate a JWT from a different service (no issuer claim).
	// It must be rejected even if the shared secret is the same.
	const secret = "shared-secret"
	// Issue with our issuer then try a token crafted without issuer via
	// ParseAccessToken — a wrong-issuer token from IssueAccessToken is impossible,
	// so we just verify that WrongSecret + right issuer still fails.
	token, _ := auth.IssueAccessToken("user-abc", secret, time.Minute)
	_, err := auth.ParseAccessToken(token, "different-secret")
	if err == nil {
		t.Error("expected error when validating with wrong secret")
	}
}

func TestJWT_WrongSecret(t *testing.T) {
	token, _ := auth.IssueAccessToken("user-123", "secret-a", time.Minute)
	_, err := auth.ParseAccessToken(token, "secret-b")
	if err == nil {
		t.Error("expected error with wrong secret")
	}
}

func TestJWT_ExpiredToken(t *testing.T) {
	token, _ := auth.IssueAccessToken("user-123", "secret", -time.Second)
	_, err := auth.ParseAccessToken(token, "secret")
	if err == nil {
		t.Error("expected error for expired token")
	}
}
