package auth_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/octbase/octbase-api/internal/auditlog"
	internalauth "github.com/octbase/octbase-api/internal/auth"
	"github.com/octbase/octbase-api/internal/shared"
	"github.com/octbase/octbase-api/internal/testutil"
)

// closedDB returns an opened-then-closed *sql.DB so every query/exec errors.
func closedDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_ = db.Close()
	return db
}

func sha256hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// ── HashPassword / JWT edge cases ─────────────────────────────────────────────

func TestHashPassword_TooLong(t *testing.T) {
	// bcrypt rejects passwords longer than 72 bytes.
	long := strings.Repeat("a", 73)
	if _, err := internalauth.HashPassword(long); err == nil {
		t.Error("HashPassword(>72 bytes) = nil error, want error")
	}
}

func TestParseAccessToken_NonHMACRejected(t *testing.T) {
	tok := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.RegisteredClaims{
		Issuer:    "octbase-api",
		Subject:   "user-x",
		IssuedAt:  jwt.NewNumericDate(time.Now()),
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	})
	s, err := tok.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("sign none token: %v", err)
	}
	if _, err := internalauth.ParseAccessToken(s, "secret"); err == nil {
		t.Error("ParseAccessToken accepted a non-HMAC token, want rejection")
	}
}

// ── Middleware ────────────────────────────────────────────────────────────────

func TestJWTMiddleware(t *testing.T) {
	const secret = "mw-secret"
	provider := internalauth.NewEmailProvider(nil, secret)
	var gotUser string
	h := internalauth.JWTMiddleware(provider)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser = shared.GetUserID(r)
		w.WriteHeader(http.StatusOK)
	}))

	// No token -> 401.
	rec := newRec(t, h, "", "")
	if rec != http.StatusUnauthorized {
		t.Errorf("no token: status = %d, want 401", rec)
	}
	// Bad token -> 401.
	if rec := newRec(t, h, "Authorization", "Bearer not-a-jwt"); rec != http.StatusUnauthorized {
		t.Errorf("bad token: status = %d, want 401", rec)
	}
	// Valid token -> 200 and user id is set.
	tok, _ := internalauth.IssueAccessToken("user-42", secret, time.Hour)
	if rec := newRec(t, h, "Authorization", "Bearer "+tok); rec != http.StatusOK {
		t.Errorf("valid token: status = %d, want 200", rec)
	}
	if gotUser != "user-42" {
		t.Errorf("user id = %q, want user-42", gotUser)
	}
}

func TestOptionalJWTMiddleware(t *testing.T) {
	const secret = "opt-secret"
	provider := internalauth.NewEmailProvider(nil, secret)
	var gotUser string
	h := internalauth.OptionalJWTMiddleware(provider)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser = shared.GetUserID(r)
		w.WriteHeader(http.StatusOK)
	}))

	// No token: still passes, no user.
	gotUser = "sentinel"
	if rec := newRec(t, h, "", ""); rec != http.StatusOK {
		t.Errorf("no token: status = %d, want 200", rec)
	}
	if gotUser != "" {
		t.Errorf("no-token user = %q, want empty", gotUser)
	}
	// Invalid token: passes, no user.
	gotUser = "sentinel"
	if rec := newRec(t, h, "Authorization", "Bearer garbage"); rec != http.StatusOK {
		t.Errorf("bad token: status = %d, want 200", rec)
	}
	if gotUser != "" {
		t.Errorf("bad-token user = %q, want empty", gotUser)
	}
	// Valid token: user is set.
	tok, _ := internalauth.IssueAccessToken("user-7", secret, time.Hour)
	if rec := newRec(t, h, "Authorization", "Bearer "+tok); rec != http.StatusOK {
		t.Errorf("valid token: status = %d, want 200", rec)
	}
	if gotUser != "user-7" {
		t.Errorf("valid-token user = %q, want user-7", gotUser)
	}
}

func newRec(t *testing.T, h http.Handler, headerKey, headerVal string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	if headerKey != "" {
		req.Header.Set(headerKey, headerVal)
	}
	rec := &statusRecorder{header: http.Header{}, status: http.StatusOK}
	h.ServeHTTP(rec, req)
	return rec.status
}

type statusRecorder struct {
	header http.Header
	status int
}

func (s *statusRecorder) Header() http.Header         { return s.header }
func (s *statusRecorder) Write(b []byte) (int, error) { return len(b), nil }
func (s *statusRecorder) WriteHeader(code int)        { s.status = code }

// ── Repo error / uncovered branches ──────────────────────────────────────────

func TestRefreshTokenRepo_Branches(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	repo := internalauth.NewRefreshTokenRepo(db)

	// DeleteByUser on a real DB (success path).
	if err := repo.DeleteByUser(testutil.DemoUserID); err != nil {
		t.Errorf("DeleteByUser: %v", err)
	}

	// FindByHash with an unknown hash returns sql.ErrNoRows.
	if _, _, err := repo.FindByHash("does-not-exist"); err == nil {
		t.Error("FindByHash(unknown) = nil error, want ErrNoRows")
	}

	// Error branches: a closed pool makes Store and FindByHash fail.
	cdb := closedDB(t)
	crepo := internalauth.NewRefreshTokenRepo(cdb)
	if err := crepo.Store(shared.NewUUID(), testutil.DemoUserID, "h", time.Now().Add(time.Hour)); err == nil {
		t.Error("Store on closed db = nil error, want error")
	}
	if _, _, err := crepo.FindByHash("h"); err == nil {
		t.Error("FindByHash on closed db = nil error, want error")
	}
}

func TestInvitationRepo_Branches(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	repo := internalauth.NewInvitationRepo(db)

	// Store + Accept success paths.
	inv := &internalauth.Invitation{
		ID:        shared.NewUUID(),
		Email:     "repo-inv@test.dev",
		Role:      "PROJECT_MEMBER",
		TokenHash: sha256hex("repo-inv-token"),
		InvitedBy: testutil.DemoUserID,
		ExpiresAt: time.Now().Add(time.Hour),
	}
	if err := repo.Store(inv); err != nil {
		t.Fatalf("Store: %v", err)
	}
	if err := repo.Accept(inv.ID); err != nil {
		t.Errorf("Accept: %v", err)
	}

	// FindByTokenHash unknown -> (nil, nil).
	got, err := repo.FindByTokenHash("unknown-hash")
	if err != nil || got != nil {
		t.Errorf("FindByTokenHash(unknown) = (%v, %v), want (nil, nil)", got, err)
	}

	// Error branches via closed pool.
	cdb := closedDB(t)
	crepo := internalauth.NewInvitationRepo(cdb)
	if err := crepo.Store(inv); err == nil {
		t.Error("Store on closed db = nil error, want error")
	}
	if _, err := crepo.FindByTokenHash("h"); err == nil {
		t.Error("FindByTokenHash on closed db = nil error, want error")
	}
}

// ── EmailProvider.Login branches ─────────────────────────────────────────────

func TestEmailProvider_LoginNullPassword(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	now := shared.Now()
	id := shared.NewUUID()
	// User with no password_hash (NULL) -> invalid credentials, never a panic.
	if _, err := db.Exec(
		`INSERT INTO users (id, email, display_name, global_role, status, is_active, created_at, updated_at)
		 VALUES ($1,'nullpw@test.dev','Null PW','USER','active',true,$2,$3)`,
		id, now, now,
	); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	provider := internalauth.NewEmailProvider(db, testutil.TestJWTSecret)
	if _, err := provider.Login(context.Background(), "nullpw@test.dev", "whatever"); err != internalauth.ErrInvalidCredentials {
		t.Errorf("Login(null password) = %v, want ErrInvalidCredentials", err)
	}
}

func TestEmailProvider_LoginQueryError(t *testing.T) {
	provider := internalauth.NewEmailProvider(closedDB(t), testutil.TestJWTSecret)
	_, err := provider.Login(context.Background(), "x@test.dev", "pw")
	if err == nil || err == internalauth.ErrInvalidCredentials {
		t.Errorf("Login on closed db = %v, want a wrapped query error", err)
	}
}

// ── Login / Refresh / Logout / Me handler branches ───────────────────────────

func TestLogin_BadJSON(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	resp, err := http.Post(srv.URL+"/api/v1/auth/login", "application/json", strings.NewReader("{not json"))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestLogin_ForwardedForRecorded(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	insertUserWithPassword(t, db, shared.NewUUID(), "xff@test.dev", "XFF User", "Str0ngPass123", true)
	srv := testutil.NewTestServer(t, db)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/auth/login",
		strings.NewReader(`{"email":"xff@test.dev","password":"Str0ngPass123"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestRefresh_NoCookie(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	resp, err := http.Post(srv.URL+"/api/v1/auth/refresh", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no-cookie refresh = %d, want 401", resp.StatusCode)
	}
}

func TestRefresh_InvalidCookie(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/auth/refresh", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "refresh_token", Value: "bogus-token-value"})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("invalid-cookie refresh = %d, want 401", resp.StatusCode)
	}
}

func TestRefresh_DisabledAccountRejected(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	id := shared.NewUUID()
	insertUserWithPassword(t, db, id, "disable-refresh@test.dev", "DR User", "Str0ngPass123", true)
	srv := testutil.NewTestServer(t, db)

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginResp, err := client.Post(srv.URL+"/api/v1/auth/login", "application/json",
		strings.NewReader(`{"email":"disable-refresh@test.dev","password":"Str0ngPass123"}`))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	_ = loginResp.Body.Close()

	// Disable the account, then attempt a refresh.
	if _, err := db.Exec(`UPDATE users SET status='disabled', is_active=false WHERE id=$1`, id); err != nil {
		t.Fatalf("disable: %v", err)
	}
	refreshResp, err := client.Post(srv.URL+"/api/v1/auth/refresh", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	_ = refreshResp.Body.Close()
	if refreshResp.StatusCode != http.StatusUnauthorized {
		t.Errorf("refresh for disabled account = %d, want 401", refreshResp.StatusCode)
	}
}

func TestLogout_NoCookie(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	resp, err := http.Post(srv.URL+"/api/v1/auth/logout", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("no-cookie logout = %d, want 204", resp.StatusCode)
	}
}

// TestMe_DefensiveBranches drives the Me handler directly. Through the wired
// middleware chain LoadUserGlobalRole already rejects an empty/unknown user, so
// Me's own 401 (no user id) and 404 (user row missing) guards are only
// reachable by calling the handler in isolation.
func TestMe_DefensiveBranches(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	h := newAuthHandler(db)

	// No user id in context -> 401.
	req := httptestNewGet()
	rec := &statusRecorder{header: http.Header{}, status: http.StatusOK}
	h.Me(rec, req)
	if rec.status != http.StatusUnauthorized {
		t.Errorf("no user id: status = %d, want 401", rec.status)
	}

	// User id present but the row does not exist -> 404.
	req = httptestNewGet()
	req = req.WithContext(context.WithValue(req.Context(), shared.UserIDKey, shared.NewUUID()))
	rec = &statusRecorder{header: http.Header{}, status: http.StatusOK}
	h.Me(rec, req)
	if rec.status != http.StatusNotFound {
		t.Errorf("unknown user: status = %d, want 404", rec.status)
	}
}

func httptestNewGet() *http.Request {
	req, _ := http.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	return req
}

// newAuthHandler builds an auth.Handler wired to db, mirroring production wiring.
func newAuthHandler(db *sql.DB) *internalauth.Handler {
	provider := internalauth.NewEmailProvider(db, testutil.TestJWTSecret)
	tokens := internalauth.NewRefreshTokenRepo(db)
	invitations := internalauth.NewInvitationRepo(db)
	audit := auditlog.NewRepo(db)
	return internalauth.NewHandler(db, provider, tokens, invitations, audit, nil, testutil.TestJWTSecret)
}

// TestLogout_WithCookie exercises the branch that finds and deletes the stored
// refresh token. The login cookie is scoped to /api/v1/auth/refresh, so it must
// be attached to the logout request explicitly.
func TestLogout_WithCookie(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	insertUserWithPassword(t, db, shared.NewUUID(), "logout-cookie@test.dev", "LC User", "Str0ngPass123", true)
	srv := testutil.NewTestServer(t, db)

	loginResp, err := http.Post(srv.URL+"/api/v1/auth/login", "application/json",
		strings.NewReader(`{"email":"logout-cookie@test.dev","password":"Str0ngPass123"}`))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	_ = loginResp.Body.Close()
	var refreshCookie *http.Cookie
	for _, c := range loginResp.Cookies() {
		if c.Name == "refresh_token" {
			refreshCookie = c
		}
	}
	if refreshCookie == nil {
		t.Fatal("login did not set a refresh_token cookie")
	}

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/auth/logout", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(refreshCookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("logout status = %d, want 204", resp.StatusCode)
	}

	// The stored token must be gone.
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM refresh_tokens`).Scan(&n); err != nil {
		t.Fatalf("count tokens: %v", err)
	}
	if n != 0 {
		t.Errorf("refresh_tokens rows = %d, want 0 after logout", n)
	}
}

// TestRefreshPresenceCookie verifies the non-HttpOnly companion cookie that
// lets the SPA gate its bootstrap refresh probe: login sets it with the same
// expiry as the refresh cookie, and logout / a rejected refresh expire it so a
// dead session stops re-probing.
func TestRefreshPresenceCookie(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	insertUserWithPassword(t, db, shared.NewUUID(), "presence@test.dev", "Presence User", "Str0ngPass123", true)
	srv := testutil.NewTestServer(t, db)

	loginResp, err := http.Post(srv.URL+"/api/v1/auth/login", "application/json",
		strings.NewReader(`{"email":"presence@test.dev","password":"Str0ngPass123"}`))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	_ = loginResp.Body.Close()

	var refreshCookie, presentCookie *http.Cookie
	for _, c := range loginResp.Cookies() {
		switch c.Name {
		case "refresh_token":
			refreshCookie = c
		case "refresh_present":
			presentCookie = c
		}
	}
	if refreshCookie == nil {
		t.Fatal("login did not set a refresh_token cookie")
	}
	if presentCookie == nil {
		t.Fatal("login did not set a refresh_present cookie")
	}
	if presentCookie.Value != "1" {
		t.Errorf("refresh_present value = %q, want \"1\"", presentCookie.Value)
	}
	if presentCookie.HttpOnly {
		t.Error("refresh_present must not be HttpOnly (the SPA must read it)")
	}
	if presentCookie.Path != "/" {
		t.Errorf("refresh_present Path = %q, want \"/\"", presentCookie.Path)
	}
	// The marker must rise and fall with the refresh cookie, so its expiry must
	// match (allowing a small skew for the two SetCookie calls).
	if skew := presentCookie.Expires.Sub(refreshCookie.Expires); skew > time.Second || skew < -time.Second {
		t.Errorf("refresh_present expiry skew from refresh_token = %v, want ~0", skew)
	}

	// A rejected refresh (bogus cookie) must expire the marker.
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/auth/refresh", strings.NewReader("{}"))
	req.AddCookie(&http.Cookie{Name: "refresh_present", Value: "1"})
	req.AddCookie(&http.Cookie{Name: "refresh_token", Value: "bogus-token-value"})
	refreshResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	_ = refreshResp.Body.Close()
	if refreshResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("refresh status = %d, want 401", refreshResp.StatusCode)
	}
	assertPresenceCleared(t, refreshResp, "rejected refresh")

	// Logout must expire the marker too.
	logoutReq, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/auth/logout", strings.NewReader("{}"))
	logoutReq.AddCookie(refreshCookie)
	logoutResp, err := http.DefaultClient.Do(logoutReq)
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	_ = logoutResp.Body.Close()
	assertPresenceCleared(t, logoutResp, "logout")
}

// assertPresenceCleared fails unless resp expires the refresh_present cookie
// (empty value with a non-positive Max-Age).
func assertPresenceCleared(t *testing.T, resp *http.Response, when string) {
	t.Helper()
	for _, c := range resp.Cookies() {
		if c.Name == "refresh_present" {
			if c.Value == "" && c.MaxAge < 0 {
				return
			}
			t.Errorf("%s: refresh_present not expired (value=%q maxAge=%d)", when, c.Value, c.MaxAge)
			return
		}
	}
	t.Errorf("%s: no refresh_present cookie in response (expected an expiring one)", when)
}

func TestMe_WithMemberships(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	// Creating a project makes DemoUser a PROJECT_OWNER -> /me memberships loop runs.
	testutil.MustCreateProject(t, srv, "Me Memberships Project")

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/auth/me", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var user meBody
	testutil.DecodeJSON(t, resp, &user)
	if len(user.ProjectMemberships) == 0 {
		t.Error("expected at least one project membership in /me response")
	}
}

type meBody struct {
	ProjectMemberships []struct {
		ProjectID string `json:"projectId"`
		Role      string `json:"role"`
	} `json:"projectMemberships"`
}

// ── CreateInvitation branches ────────────────────────────────────────────────

func TestCreateInvitation_MissingEmail(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/admin/invitations",
		map[string]interface{}{"email": ""}, testutil.DemoUserID)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", resp.StatusCode)
	}
}

func TestCreateInvitation_InvalidProjectRole(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Inv Role Project")
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/admin/invitations",
		map[string]interface{}{"email": "x@test.dev", "projectId": pid, "role": "BOGUS_ROLE"}, testutil.DemoUserID)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", resp.StatusCode)
	}
}

func TestCreateInvitation_NonAdminForbidden(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	// SecondUserID is a plain USER with no project context.
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/admin/invitations",
		map[string]interface{}{"email": "x@test.dev"}, testutil.SecondUserID)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

func TestCreateInvitation_OwnerTransferForbidden(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Owner Transfer Project")
	// SecondUser is a PROJECT_ADMIN (can invite) but not a PROJECT_OWNER.
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_ADMIN")
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/admin/invitations",
		map[string]interface{}{"email": "newowner@test.dev", "projectId": pid, "role": "PROJECT_OWNER"},
		testutil.SecondUserID)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

func TestCreateInvitation_WithProjectSucceeds(t *testing.T) {
	// A configured OCTBASE_APP_URL is woven into the returned acceptURL.
	t.Setenv("OCTBASE_APP_URL", "https://octbase.example")
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Inv Success Project")
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/admin/invitations",
		map[string]interface{}{"email": "member@test.dev", "projectId": pid, "role": "PROJECT_MEMBER"},
		testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var out map[string]interface{}
	testutil.DecodeJSON(t, resp, &out)
	if url, _ := out["acceptURL"].(string); !strings.HasPrefix(url, "https://octbase.example/") {
		t.Errorf("acceptURL = %q, want it to use OCTBASE_APP_URL", url)
	}
}

// ── GetInvitation / AcceptInvitation branches ────────────────────────────────

func TestGetInvitation_NotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/invitations/no-such-token", nil, "")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestGetInvitation_WithProject(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "GetInv Project")
	cr := testutil.Do(t, srv, http.MethodPost, "/api/v1/admin/invitations",
		map[string]interface{}{"email": "getinvproj@test.dev", "projectId": pid, "role": "PROJECT_MEMBER"},
		testutil.DemoUserID)
	testutil.AssertStatus(t, cr, http.StatusCreated)
	var inv map[string]interface{}
	testutil.DecodeJSON(t, cr, &inv)
	rawToken := extractToken(inv["acceptURL"].(string))

	resp := testutil.Do(t, srv, http.MethodGet, fmt.Sprintf("/api/v1/invitations/%s", rawToken), nil, "")
	testutil.AssertStatus(t, resp, http.StatusOK)
	var result map[string]interface{}
	testutil.DecodeJSON(t, resp, &result)
	if result["projectName"] != "GetInv Project" {
		t.Errorf("projectName = %v, want GetInv Project", result["projectName"])
	}
	if result["inviterName"] == nil {
		t.Error("expected inviterName in response")
	}
}

func TestAcceptInvitation_NotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/invitations/no-such-token/accept",
		map[string]string{"name": "X", "password": "Str0ngPass123"}, "")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusGone {
		t.Errorf("status = %d, want 410", resp.StatusCode)
	}
}

func TestAcceptInvitation_BadJSON(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	cr := testutil.Do(t, srv, http.MethodPost, "/api/v1/admin/invitations",
		map[string]interface{}{"email": "badjson@test.dev"}, testutil.DemoUserID)
	testutil.AssertStatus(t, cr, http.StatusCreated)
	var inv map[string]interface{}
	testutil.DecodeJSON(t, cr, &inv)
	rawToken := extractToken(inv["acceptURL"].(string))

	req, _ := http.NewRequest(http.MethodPost,
		srv.URL+fmt.Sprintf("/api/v1/invitations/%s/accept", rawToken), strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestAcceptInvitation_Validation(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	cr := testutil.Do(t, srv, http.MethodPost, "/api/v1/admin/invitations",
		map[string]interface{}{"email": "validate@test.dev"}, testutil.DemoUserID)
	testutil.AssertStatus(t, cr, http.StatusCreated)
	var inv map[string]interface{}
	testutil.DecodeJSON(t, cr, &inv)
	rawToken := extractToken(inv["acceptURL"].(string))
	path := fmt.Sprintf("/api/v1/invitations/%s/accept", rawToken)

	cases := []struct {
		name string
		body map[string]string
	}{
		{"missing name", map[string]string{"password": "Str0ngPass123"}},
		{"missing password", map[string]string{"name": "X"}},
		{"short password", map[string]string{"name": "X", "password": "short"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := testutil.Do(t, srv, http.MethodPost, path, c.body, "")
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusUnprocessableEntity {
				t.Errorf("status = %d, want 422", resp.StatusCode)
			}
		})
	}
}

func TestAcceptInvitation_WithProjectCreatesMembership(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Accept Proj")
	cr := testutil.Do(t, srv, http.MethodPost, "/api/v1/admin/invitations",
		map[string]interface{}{"email": "acceptproj@test.dev", "projectId": pid, "role": "PROJECT_MEMBER"},
		testutil.DemoUserID)
	testutil.AssertStatus(t, cr, http.StatusCreated)
	var inv map[string]interface{}
	testutil.DecodeJSON(t, cr, &inv)
	rawToken := extractToken(inv["acceptURL"].(string))

	resp := testutil.Do(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/invitations/%s/accept", rawToken),
		map[string]string{"name": "Accepted", "password": "Str0ngPass123"}, "")
	testutil.AssertStatus(t, resp, http.StatusCreated)
	_ = resp.Body.Close()

	var count int
	if err := db.QueryRow(
		`SELECT count(*) FROM memberships m JOIN users u ON u.id = m.user_id
		   WHERE m.project_id = $1 AND u.email = 'acceptproj@test.dev'`, pid,
	).Scan(&count); err != nil {
		t.Fatalf("count membership: %v", err)
	}
	if count != 1 {
		t.Errorf("membership rows = %d, want 1", count)
	}
}

func TestAcceptInvitation_DuplicateEmail(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	// Pre-existing user with the invited email.
	insertUserWithPassword(t, db, shared.NewUUID(), "dupe@test.dev", "Existing", "Str0ngPass123", true)
	srv := testutil.NewTestServer(t, db)
	cr := testutil.Do(t, srv, http.MethodPost, "/api/v1/admin/invitations",
		map[string]interface{}{"email": "dupe@test.dev"}, testutil.DemoUserID)
	testutil.AssertStatus(t, cr, http.StatusCreated)
	var inv map[string]interface{}
	testutil.DecodeJSON(t, cr, &inv)
	rawToken := extractToken(inv["acceptURL"].(string))

	resp := testutil.Do(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/invitations/%s/accept", rawToken),
		map[string]string{"name": "Dupe", "password": "Str0ngPass123"}, "")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("status = %d, want 409", resp.StatusCode)
	}
}

// TestHandlers_DBErrors drives handlers with a closed connection pool to cover
// the "internal error" (500) branches that fire when the database is
// unavailable mid-request.
func TestHandlers_DBErrors(t *testing.T) {
	h := newAuthHandler(closedDB(t))

	// Login: provider query fails (not a credential error) -> 500.
	t.Run("login", func(t *testing.T) {
		req := postJSON("/api/v1/auth/login", `{"email":"x@test.dev","password":"pw"}`)
		rec := &statusRecorder{header: http.Header{}, status: http.StatusOK}
		h.Login(rec, req)
		if rec.status != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", rec.status)
		}
	})

	// Me: the user lookup query fails -> 500.
	t.Run("me", func(t *testing.T) {
		req := httptestNewGet()
		req = req.WithContext(context.WithValue(req.Context(), shared.UserIDKey, shared.NewUUID()))
		rec := &statusRecorder{header: http.Header{}, status: http.StatusOK}
		h.Me(rec, req)
		if rec.status != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", rec.status)
		}
	})

	// GetInvitation: FindByTokenHash fails -> 500.
	t.Run("get invitation", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, "/api/v1/invitations/tok", nil)
		rec := &statusRecorder{header: http.Header{}, status: http.StatusOK}
		h.GetInvitation(rec, req)
		if rec.status != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", rec.status)
		}
	})

	// AcceptInvitation: FindByTokenHash fails -> 500.
	t.Run("accept invitation", func(t *testing.T) {
		req := postJSON("/api/v1/invitations/tok/accept", `{"name":"X","password":"Str0ngPass123"}`)
		rec := &statusRecorder{header: http.Header{}, status: http.StatusOK}
		h.AcceptInvitation(rec, req)
		if rec.status != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", rec.status)
		}
	})
}

func postJSON(path, body string) *http.Request {
	req, _ := http.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// TestAcceptInvitation_PasswordTooLongFails covers the HashPassword error branch:
// a password that passes the >=8 check but exceeds bcrypt's 72-byte limit.
func TestAcceptInvitation_PasswordTooLongFails(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	cr := testutil.Do(t, srv, http.MethodPost, "/api/v1/admin/invitations",
		map[string]interface{}{"email": "longpw@test.dev"}, testutil.DemoUserID)
	testutil.AssertStatus(t, cr, http.StatusCreated)
	var inv map[string]interface{}
	testutil.DecodeJSON(t, cr, &inv)
	rawToken := extractToken(inv["acceptURL"].(string))

	resp := testutil.Do(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/invitations/%s/accept", rawToken),
		map[string]string{"name": "Long", "password": strings.Repeat("a", 73)}, "")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}

// ── Env-driven config helpers ────────────────────────────────────────────────

func TestHandlerConfig_FromEnv(t *testing.T) {
	t.Setenv("OCTBASE_JWT_SECRET", "env-secret")
	t.Setenv("OCTBASE_JWT_ACCESS_TTL", "5m")
	t.Setenv("OCTBASE_JWT_REFRESH_TTL", "48h")
	t.Setenv("OCTBASE_SECURE_COOKIES", "true")

	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	insertUserWithPassword(t, db, shared.NewUUID(), "envcfg@test.dev", "Env Cfg", "Str0ngPass123", true)
	srv := testutil.NewTestServer(t, db)

	resp, err := http.Post(srv.URL+"/api/v1/auth/login", "application/json",
		strings.NewReader(`{"email":"envcfg@test.dev","password":"Str0ngPass123"}`))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want 200", resp.StatusCode)
	}
	// The refresh cookie must carry the Secure attribute under OCTBASE_SECURE_COOKIES.
	var sawSecure bool
	for _, c := range resp.Cookies() {
		if c.Name == "refresh_token" && c.Secure {
			sawSecure = true
		}
	}
	if !sawSecure {
		t.Error("expected a Secure refresh_token cookie when OCTBASE_SECURE_COOKIES=true")
	}
}

func TestHandlerConfig_InvalidTTLFallsBack(t *testing.T) {
	t.Setenv("OCTBASE_JWT_ACCESS_TTL", "not-a-duration")
	t.Setenv("OCTBASE_JWT_REFRESH_TTL", "also-bad")

	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	insertUserWithPassword(t, db, shared.NewUUID(), "badttl@test.dev", "Bad TTL", "Str0ngPass123", true)
	srv := testutil.NewTestServer(t, db)

	resp, err := http.Post(srv.URL+"/api/v1/auth/login", "application/json",
		strings.NewReader(`{"email":"badttl@test.dev","password":"Str0ngPass123"}`))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want 200 (TTL should fall back to defaults)", resp.StatusCode)
	}
}
