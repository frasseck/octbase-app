package auth_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/octbase/octbase-api/internal/shared"
	"github.com/octbase/octbase-api/internal/testutil"
)

// TestLogin_UnknownEmail documents the enumeration-resistant behaviour: an
// unknown email must return the same 401 INVALID_CREDENTIALS as a wrong password
// (and the provider runs a dummy bcrypt comparison so the timing matches too —
// see auth.dummyBcryptHash).
func TestLogin_UnknownEmail(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": "does-not-exist@test.dev", "password": "whatever"}, "")
	testutil.AssertStatus(t, resp, http.StatusUnauthorized)
	var body map[string]any
	testutil.DecodeJSON(t, resp, &body)
	if body["code"] != "INVALID_CREDENTIALS" {
		t.Errorf("code = %v, want INVALID_CREDENTIALS (must match the wrong-password response)", body["code"])
	}
}

// loginRefreshCookie logs in and returns the refresh_token cookie.
func loginRefreshCookie(t *testing.T, srvURL, email, password string) *http.Cookie {
	t.Helper()
	resp, err := http.Post(srvURL+"/api/v1/auth/login", "application/json",
		strings.NewReader(`{"email":"`+email+`","password":"`+password+`"}`))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	for _, c := range resp.Cookies() {
		if c.Name == "refresh_token" {
			return c
		}
	}
	t.Fatal("login did not set a refresh_token cookie")
	return nil
}

func postRefresh(t *testing.T, srvURL string, cookie *http.Cookie) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, srvURL+"/api/v1/auth/refresh", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	return resp
}

// TestRefreshToken_ReuseRevokesSessions verifies refresh-token rotation with
// reuse detection: replaying an already-rotated cookie is rejected AND revokes
// every session for the user (so the legitimately-rotated token stops working
// too).
func TestRefreshToken_ReuseRevokesSessions(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	insertUserWithPassword(t, db, shared.NewUUID(), "reuse@test.dev", "Reuse User", "Str0ngPass123", true)
	srv := testutil.NewTestServer(t, db)

	// 1. Log in → original refresh cookie.
	original := loginRefreshCookie(t, srv.URL, "reuse@test.dev", "Str0ngPass123")

	// 2. Legitimate refresh rotates it → fresh cookie, 200.
	resp := postRefresh(t, srv.URL, original)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first refresh status = %d, want 200", resp.StatusCode)
	}
	var rotated *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "refresh_token" {
			rotated = c
		}
	}
	_ = resp.Body.Close()
	if rotated == nil {
		t.Fatal("refresh did not set a new refresh_token cookie")
	}

	// 3. Replay the ORIGINAL (now-rotated) cookie → 401 reuse detection.
	reuse := postRefresh(t, srv.URL, original)
	if reuse.StatusCode != http.StatusUnauthorized {
		t.Errorf("replayed token status = %d, want 401", reuse.StatusCode)
	}
	_ = reuse.Body.Close()

	// 4. The whole family is revoked: even the legitimately-rotated cookie now fails.
	after := postRefresh(t, srv.URL, rotated)
	if after.StatusCode != http.StatusUnauthorized {
		t.Errorf("post-reuse refresh with rotated token status = %d, want 401 (all sessions revoked)", after.StatusCode)
	}
	_ = after.Body.Close()

	// 5. No refresh tokens remain for the user.
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM refresh_tokens rt JOIN users u ON u.id = rt.user_id WHERE u.email = $1`, "reuse@test.dev").Scan(&n); err != nil {
		t.Fatalf("count tokens: %v", err)
	}
	if n != 0 {
		t.Errorf("refresh_tokens rows = %d, want 0 after reuse revocation", n)
	}
}
