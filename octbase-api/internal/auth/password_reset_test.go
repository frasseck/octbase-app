package auth_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"net/http"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/octbase/octbase-api/internal/shared"
	"github.com/octbase/octbase-api/internal/testutil"
)

// captureMailer records outbound mail so tests can pull the reset link out of
// the email body (the 202 response deliberately reveals nothing).
type captureMailer struct {
	mu   sync.Mutex
	sent []capturedMail
}

type capturedMail struct{ To, Subject, Body string }

func (m *captureMailer) Send(_ context.Context, to, subject, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, capturedMail{To: to, Subject: subject, Body: body})
	return nil
}

// waitForMail polls for the n-th captured mail; the reset email is dispatched
// asynchronously so response latency can't leak whether an account exists.
func (m *captureMailer) waitForMail(t *testing.T, n int) capturedMail {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		if len(m.sent) >= n {
			mail := m.sent[n-1]
			m.mu.Unlock()
			return mail
		}
		m.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("mail %d not sent within deadline", n)
	return capturedMail{}
}

var resetURLRe = regexp.MustCompile(`#/reset-password/([0-9a-f]+)`)

func resetTokenFromMail(t *testing.T, mail capturedMail) string {
	t.Helper()
	m := resetURLRe.FindStringSubmatch(mail.Body)
	if m == nil {
		t.Fatalf("no reset link in mail body: %q", mail.Body)
	}
	return m[1]
}

// sha256Hex mirrors the server-side token hashing (the raw token is never
// stored) so tests can seed password_reset_tokens rows directly.
func sha256Hex(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

func insertResetToken(t *testing.T, db *sql.DB, rawToken, userID string, expiresAt time.Time, used bool) {
	t.Helper()
	var usedAt any
	if used {
		usedAt = time.Now()
	}
	if _, err := db.Exec(
		`INSERT INTO password_reset_tokens (token_hash, user_id, expires_at, used_at) VALUES ($1,$2,$3,$4)`,
		sha256Hex(rawToken), userID, expiresAt, usedAt,
	); err != nil {
		t.Fatalf("insert reset token: %v", err)
	}
}

func TestForgotPassword_RoundTrip(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	userID := shared.NewUUID()
	insertUserWithPassword(t, db, userID, "reset-roundtrip@test.dev", "Reset User", "OldPassword12345", true)
	mail := &captureMailer{}
	srv := testutil.NewTestServer(t, db, testutil.WithMailer(mail))

	// Log in so the account holds a live refresh token the reset must revoke.
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": "reset-roundtrip@test.dev", "password": "OldPassword12345"}, "")
	testutil.AssertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()
	var nTokens int
	if err := db.QueryRow(`SELECT count(*) FROM refresh_tokens WHERE user_id = $1`, userID).Scan(&nTokens); err != nil || nTokens == 0 {
		t.Fatalf("expected a refresh token after login, got %d (err %v)", nTokens, err)
	}

	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/forgot-password",
		map[string]string{"email": "reset-roundtrip@test.dev"}, "")
	testutil.AssertStatus(t, resp, http.StatusAccepted)
	_ = resp.Body.Close()

	sent := mail.waitForMail(t, 1)
	if sent.To != "reset-roundtrip@test.dev" {
		t.Errorf("mail to = %q, want the requesting address", sent.To)
	}
	token := resetTokenFromMail(t, sent)

	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/reset-password",
		map[string]string{"token": token, "newPassword": "BrandNewPass9876"}, "")
	testutil.AssertStatus(t, resp, http.StatusNoContent)
	_ = resp.Body.Close()

	// All sessions revoked.
	if err := db.QueryRow(`SELECT count(*) FROM refresh_tokens WHERE user_id = $1`, userID).Scan(&nTokens); err != nil || nTokens != 0 {
		t.Errorf("refresh tokens after reset = %d, want 0 (err %v)", nTokens, err)
	}

	// Old password dead, new password works.
	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": "reset-roundtrip@test.dev", "password": "OldPassword12345"}, "")
	testutil.AssertStatus(t, resp, http.StatusUnauthorized)
	_ = resp.Body.Close()
	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": "reset-roundtrip@test.dev", "password": "BrandNewPass9876"}, "")
	testutil.AssertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()

	// The state change is audited.
	var nAudit int
	if err := db.QueryRow(
		`SELECT count(*) FROM audit_logs WHERE action = 'USER_PASSWORD_RESET' AND target_id = $1`, userID,
	).Scan(&nAudit); err != nil || nAudit != 1 {
		t.Errorf("audit rows = %d, want 1 (err %v)", nAudit, err)
	}
}

func TestForgotPassword_UnknownEmail_Returns202(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	mail := &captureMailer{}
	srv := testutil.NewTestServer(t, db, testutil.WithMailer(mail))

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/forgot-password",
		map[string]string{"email": "nobody-here@test.dev"}, "")
	testutil.AssertStatus(t, resp, http.StatusAccepted)
	var body map[string]string
	testutil.DecodeJSON(t, resp, &body)
	if body["message"] == "" {
		t.Error("expected the generic confirmation message")
	}
	// No token minted, no mail sent.
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM password_reset_tokens`).Scan(&n); err != nil || n != 0 {
		t.Errorf("reset tokens = %d, want 0 (err %v)", n, err)
	}
	time.Sleep(50 * time.Millisecond)
	mail.mu.Lock()
	defer mail.mu.Unlock()
	if len(mail.sent) != 0 {
		t.Errorf("mails sent = %d, want 0", len(mail.sent))
	}
}

func TestForgotPassword_NewRequestInvalidatesPriorToken(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	userID := shared.NewUUID()
	insertUserWithPassword(t, db, userID, "reset-two@test.dev", "Two Tokens", "OldPassword12345", true)
	mail := &captureMailer{}
	srv := testutil.NewTestServer(t, db, testutil.WithMailer(mail))

	for i := 0; i < 2; i++ {
		resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/forgot-password",
			map[string]string{"email": "reset-two@test.dev"}, "")
		testutil.AssertStatus(t, resp, http.StatusAccepted)
		_ = resp.Body.Close()
		mail.waitForMail(t, i+1) // serialize: each token must be minted before the next request voids it
	}
	first := resetTokenFromMail(t, mail.waitForMail(t, 1))
	second := resetTokenFromMail(t, mail.waitForMail(t, 2))

	// The first link died when the second was requested.
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/reset-password",
		map[string]string{"token": first, "newPassword": "BrandNewPass9876"}, "")
	testutil.AssertStatus(t, resp, http.StatusBadRequest)
	var errBody map[string]string
	testutil.DecodeJSON(t, resp, &errBody)
	if errBody["code"] != "RESET_TOKEN_INVALID" {
		t.Errorf("code = %q, want RESET_TOKEN_INVALID", errBody["code"])
	}

	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/reset-password",
		map[string]string{"token": second, "newPassword": "BrandNewPass9876"}, "")
	testutil.AssertStatus(t, resp, http.StatusNoContent)
	_ = resp.Body.Close()
}

func TestResetPassword_ExpiredToken(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	userID := shared.NewUUID()
	insertUserWithPassword(t, db, userID, "reset-expired@test.dev", "Expired", "OldPassword12345", true)
	insertResetToken(t, db, "expiredtoken00", userID, time.Now().Add(-time.Minute), false)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/reset-password",
		map[string]string{"token": "expiredtoken00", "newPassword": "BrandNewPass9876"}, "")
	testutil.AssertStatus(t, resp, http.StatusBadRequest)
	var errBody map[string]string
	testutil.DecodeJSON(t, resp, &errBody)
	if errBody["code"] != "RESET_TOKEN_INVALID" {
		t.Errorf("code = %q, want RESET_TOKEN_INVALID", errBody["code"])
	}
}

func TestResetPassword_ReusedToken(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	userID := shared.NewUUID()
	insertUserWithPassword(t, db, userID, "reset-reuse@test.dev", "Reuse", "OldPassword12345", true)
	insertResetToken(t, db, "reusabletoken0", userID, time.Now().Add(time.Hour), false)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/reset-password",
		map[string]string{"token": "reusabletoken0", "newPassword": "BrandNewPass9876"}, "")
	testutil.AssertStatus(t, resp, http.StatusNoContent)
	_ = resp.Body.Close()

	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/reset-password",
		map[string]string{"token": "reusabletoken0", "newPassword": "AnotherNewPass99"}, "")
	testutil.AssertStatus(t, resp, http.StatusBadRequest)
	var errBody map[string]string
	testutil.DecodeJSON(t, resp, &errBody)
	if errBody["code"] != "RESET_TOKEN_INVALID" {
		t.Errorf("code = %q, want RESET_TOKEN_INVALID", errBody["code"])
	}
}

func TestResetPassword_PolicyRejected_TokenSurvives(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	userID := shared.NewUUID()
	insertUserWithPassword(t, db, userID, "reset-weak@test.dev", "Weak", "OldPassword12345", true)
	insertResetToken(t, db, "weakpwtoken000", userID, time.Now().Add(time.Hour), false)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/reset-password",
		map[string]string{"token": "weakpwtoken000", "newPassword": "short"}, "")
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
	var errBody map[string]any
	testutil.DecodeJSON(t, resp, &errBody)
	if errBody["code"] != "VALIDATION_ERROR" {
		t.Errorf("code = %v, want VALIDATION_ERROR", errBody["code"])
	}

	// A policy rejection must not burn the token — the user retries with a
	// stronger password on the same link.
	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/reset-password",
		map[string]string{"token": "weakpwtoken000", "newPassword": "BrandNewPass9876"}, "")
	testutil.AssertStatus(t, resp, http.StatusNoContent)
	_ = resp.Body.Close()
}

// Declared last in the file on purpose: the per-IP attempt budget is process-
// global, so this test spends whatever the earlier forgot-password tests left
// and must not run before them.
func TestForgotPassword_RateLimit(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)

	limited := false
	for i := 0; i < 10; i++ {
		resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/forgot-password",
			map[string]string{"email": "ratelimit@test.dev"}, "")
		if resp.StatusCode == http.StatusTooManyRequests {
			limited = true
			_ = resp.Body.Close()
			break
		}
		testutil.AssertStatus(t, resp, http.StatusAccepted)
		_ = resp.Body.Close()
	}
	if !limited {
		t.Error("expected the per-IP rate limit to trip within 10 requests")
	}
}
