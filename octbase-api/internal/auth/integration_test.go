package auth_test

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"

	internalauth "github.com/octbase/octbase-api/internal/auth"
	"github.com/octbase/octbase-api/internal/shared"
	"github.com/octbase/octbase-api/internal/testutil"
)

// insertUserWithPassword inserts a user with a hashed password.
func insertUserWithPassword(t *testing.T, db *sql.DB, id, email, name, password string, active bool) {
	t.Helper()
	hash, err := internalauth.HashPassword(password)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	now := shared.Now()
	_, err = db.Exec(
		`INSERT INTO users (id, email, display_name, password_hash, is_active, created_at, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		id, email, name, hash, active, now, now,
	)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
}

// TestLogin_ValidCredentials verifies login returns an access token.
func TestLogin_ValidCredentials(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	id := shared.NewUUID()
	insertUserWithPassword(t, db, id, "login@test.dev", "Login User", "Str0ngPass123", true)

	srv := testutil.NewTestServer(t, db)
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": "login@test.dev", "password": "Str0ngPass123"}, "")
	testutil.AssertStatus(t, resp, http.StatusOK)
	var result map[string]interface{}
	testutil.DecodeJSON(t, resp, &result)
	if result["accessToken"] == nil {
		t.Error("expected accessToken in login response")
	}
}

// TestLogin_WrongPassword verifies wrong password returns 401.
func TestLogin_WrongPassword(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	insertUserWithPassword(t, db, shared.NewUUID(), "wrongpw@test.dev", "WrongPW", "correct", true)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": "wrongpw@test.dev", "password": "wrong"}, "")
	testutil.AssertStatus(t, resp, http.StatusUnauthorized)
	_ = resp.Body.Close()
}

// TestDeactivatedUser_CannotLogin verifies inactive users get 401.
func TestDeactivatedUser_CannotLogin(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	insertUserWithPassword(t, db, shared.NewUUID(), "inactive@test.dev", "Inactive", "Str0ngPass123", false)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": "inactive@test.dev", "password": "Str0ngPass123"}, "")
	testutil.AssertStatus(t, resp, http.StatusUnauthorized)
	_ = resp.Body.Close()
}

// TestJWT_ProtectedEndpoint verifies valid JWT → 200 on protected route.
func TestJWT_ProtectedEndpoint(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/users/me", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()
}

// TestViewer_CannotWrite verifies PROJECT_VIEWER role is blocked on write endpoints.
func TestViewer_CannotWrite(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "RBAC Project")
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_VIEWER")

	resp := testutil.Do(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/tasks", pid),
		map[string]string{"title": "Should fail"}, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
	_ = resp.Body.Close()
}

// TestInvitation_AcceptFlow tests invite → accept → token returned.
func TestInvitation_AcceptFlow(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	// Make DemoUser admin so they can invite.
	_, _ = db.Exec(`UPDATE users SET is_admin = true WHERE id = $1`, testutil.DemoUserID)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/admin/invitations",
		map[string]interface{}{"email": "invitee@test.dev", "role": "DEVELOPER"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var inv map[string]interface{}
	testutil.DecodeJSON(t, resp, &inv)

	acceptURL, _ := inv["acceptURL"].(string)
	if acceptURL == "" {
		t.Fatal("expected acceptURL")
	}
	rawToken := extractToken(acceptURL)

	resp = testutil.Do(t, srv, http.MethodPost,
		fmt.Sprintf("/api/v1/invitations/%s/accept", rawToken),
		map[string]string{"name": "Invitee", "password": "Str0ngPass123"}, "")
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var result map[string]interface{}
	testutil.DecodeJSON(t, resp, &result)
	if result["accessToken"] == nil {
		t.Error("expected accessToken after invitation accept")
	}
}

// TestBulkTasks_UpdatesMultiple verifies bulk action updates all specified tasks.
func TestBulkTasks_UpdatesMultiple(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Bulk Project")
	tid1 := testutil.MustCreateTask(t, srv, pid, "Task 1")
	tid2 := testutil.MustCreateTask(t, srv, pid, "Task 2")
	tid3 := testutil.MustCreateTask(t, srv, pid, "Task 3")

	resp := testutil.Do(t, srv, http.MethodPost,
		fmt.Sprintf("/api/v1/projects/%s/tasks/bulk", pid),
		map[string]interface{}{
			"taskIds": []string{tid1, tid2, tid3},
			"action":  "set_priority",
			"value":   "HIGH",
		}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var result map[string]interface{}
	testutil.DecodeJSON(t, resp, &result)
	if int(result["updated"].(float64)) != 3 {
		t.Errorf("expected 3 updated, got %v", result["updated"])
	}
}

// TestUnifiedSearch verifies the /search endpoint returns results.
func TestUnifiedSearch_ReturnsResults(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "SearchProject")
	testutil.MustCreateTask(t, srv, pid, "FindableTaskTitle")

	resp := testutil.Do(t, srv, http.MethodGet,
		fmt.Sprintf("/api/v1/search?q=Findable&projectId=%s", pid),
		nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var result map[string]interface{}
	testutil.DecodeJSON(t, resp, &result)
	tasks, _ := result["tasks"].([]interface{})
	if len(tasks) == 0 {
		t.Error("expected at least one task result")
	}
	pages, _ := result["pages"].([]interface{})
	if len(pages) > 0 {
		page, _ := pages[0].(map[string]interface{})
		if page["projectId"] == nil || page["projectId"] == "" {
			t.Error("expected page search result to include projectId")
		}
	}
	task, _ := tasks[0].(map[string]interface{})
	if task["projectId"] == nil || task["projectId"] == "" {
		t.Error("expected task search result to include projectId")
	}
}

// TestDashboard_ReturnsSections verifies all four dashboard sections are present.
func TestDashboard_ReturnsSections(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/users/me/dashboard", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var result map[string]interface{}
	testutil.DecodeJSON(t, resp, &result)
	for _, key := range []string{"assignedTasks", "reviewingTasks", "recentPages", "upcomingReleases"} {
		if result[key] == nil {
			t.Errorf("dashboard missing %q section", key)
		}
	}
	recentPages, _ := result["recentPages"].([]interface{})
	if len(recentPages) > 0 {
		page, _ := recentPages[0].(map[string]interface{})
		if page["projectId"] == nil || page["projectId"] == "" {
			t.Error("expected dashboard recent page to include projectId")
		}
	}
}

// TestTaskSeqNumber verifies tasks get incrementing project-scoped seq numbers.
func TestTaskSeqNumber(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "SeqProject")

	var t1, t2 map[string]interface{}
	resp1 := testutil.Do(t, srv, http.MethodPost,
		fmt.Sprintf("/api/v1/projects/%s/tasks", pid),
		map[string]string{"title": "First"}, testutil.DemoUserID)
	testutil.DecodeJSON(t, resp1, &t1)

	resp2 := testutil.Do(t, srv, http.MethodPost,
		fmt.Sprintf("/api/v1/projects/%s/tasks", pid),
		map[string]string{"title": "Second"}, testutil.DemoUserID)
	testutil.DecodeJSON(t, resp2, &t2)

	seq1 := t1["seqNumber"]
	seq2 := t2["seqNumber"]
	if seq1 == nil || seq2 == nil {
		t.Error("seqNumber should not be nil")
		return
	}
	if seq1.(float64) >= seq2.(float64) {
		t.Errorf("expected seq2 > seq1, got seq1=%v seq2=%v", seq1, seq2)
	}
}

// extractToken pulls the invitation raw token from the accept URL.
// URL form: http://host/#/invitations/{token}/accept
func extractToken(url string) string {
	const needle = "/invitations/"
	idx := len(url) - 1
	for url[idx] != '/' {
		idx--
	}
	// idx is the last '/' before "accept"
	end := idx
	start := 0
	for i := 0; i < len(url); i++ {
		if url[i:min(i+len(needle), len(url))] == needle {
			start = i + len(needle)
			break
		}
	}
	return url[start:end]
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestRefreshToken_OK verifies that a valid refresh cookie produces a new access token.
func TestRefreshToken_OK(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	id := shared.NewUUID()
	insertUserWithPassword(t, db, id, "refresh@test.dev", "Refresh User", "pass1234", true)
	srv := testutil.NewTestServer(t, db)

	// 1. Login to obtain the refresh_token cookie.
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	loginURL := srv.URL + "/api/v1/auth/login"
	body := `{"email":"refresh@test.dev","password":"pass1234"}`
	loginResp, err := client.Post(loginURL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("login request: %v", err)
	}
	if loginResp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want 200", loginResp.StatusCode)
	}
	_ = loginResp.Body.Close()

	// 2. POST to /refresh — cookie jar forwards the refresh_token cookie.
	refreshResp, err := client.Post(srv.URL+"/api/v1/auth/refresh", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("refresh request: %v", err)
	}
	defer func() { _ = refreshResp.Body.Close() }()
	if refreshResp.StatusCode != http.StatusOK {
		t.Fatalf("refresh status = %d, want 200", refreshResp.StatusCode)
	}
	var result map[string]interface{}
	testutil.DecodeJSON(t, refreshResp, &result)
	if result["accessToken"] == nil {
		t.Error("expected accessToken in refresh response")
	}
}

// TestLogout_ClearsRefreshToken verifies that logout prevents subsequent refreshes.
func TestLogout_ClearsRefreshToken(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	id := shared.NewUUID()
	insertUserWithPassword(t, db, id, "logout@test.dev", "Logout User", "pass1234", true)
	srv := testutil.NewTestServer(t, db)

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	// Login.
	body := `{"email":"logout@test.dev","password":"pass1234"}`
	loginResp, err := client.Post(srv.URL+"/api/v1/auth/login", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	_ = loginResp.Body.Close()

	// Logout.
	logoutResp, err := client.Post(srv.URL+"/api/v1/auth/logout", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	_ = logoutResp.Body.Close()

	// Refresh after logout must be rejected.
	refreshResp, err := client.Post(srv.URL+"/api/v1/auth/refresh", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	defer func() { _ = refreshResp.Body.Close() }()
	if refreshResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("refresh after logout = %d, want 401", refreshResp.StatusCode)
	}
}

// TestAuthMe_OK verifies /api/v1/auth/me returns the authenticated user.
func TestAuthMe_OK(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/auth/me", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	var user map[string]interface{}
	testutil.DecodeJSON(t, resp, &user)
	if user["id"] != testutil.DemoUserID {
		t.Errorf("id = %v, want %s", user["id"], testutil.DemoUserID)
	}
	if user["email"] == nil {
		t.Error("expected email in /auth/me response")
	}
}

// TestGetInvitation_OK verifies that GET /api/v1/invitations/{token} returns invitation data.
func TestGetInvitation_OK(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	// Make DemoUser admin so they can create invitations.
	_, _ = db.Exec(`UPDATE users SET is_admin = true WHERE id = $1`, testutil.DemoUserID)
	srv := testutil.NewTestServer(t, db)

	// Create an invitation.
	cr := testutil.Do(t, srv, http.MethodPost, "/api/v1/admin/invitations",
		map[string]interface{}{"email": "getinv@test.dev", "role": "DEVELOPER"}, testutil.DemoUserID)
	testutil.AssertStatus(t, cr, http.StatusCreated)
	var inv map[string]interface{}
	testutil.DecodeJSON(t, cr, &inv)

	acceptURL, _ := inv["acceptURL"].(string)
	if acceptURL == "" {
		t.Fatal("expected acceptURL")
	}
	rawToken := extractToken(acceptURL)

	// GET the invitation by raw token.
	resp := testutil.Do(t, srv, http.MethodGet,
		fmt.Sprintf("/api/v1/invitations/%s", rawToken), nil, "")
	testutil.AssertStatus(t, resp, http.StatusOK)

	var result map[string]interface{}
	testutil.DecodeJSON(t, resp, &result)
	if result["email"] != "getinv@test.dev" {
		t.Errorf("email = %v, want getinv@test.dev", result["email"])
	}
	if result["role"] != "DEVELOPER" {
		t.Errorf("role = %v, want DEVELOPER", result["role"])
	}
}
