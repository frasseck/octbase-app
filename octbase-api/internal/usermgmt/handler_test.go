package usermgmt_test

import (
	"database/sql"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/octbase/octbase-api/internal/testutil"
)

// ── Account management ────────────────────────────────────────────────────────

func TestSuperAdmin_CanListUsers(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/users", nil, testutil.SuperAdminUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()
}

func TestAdmin_CannotListUsers(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/users", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
	_ = resp.Body.Close()
}

func TestUser_CannotListUsers(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/users", nil, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
	_ = resp.Body.Close()
}

func TestSuperAdmin_CanCreateAdmin(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/users", map[string]interface{}{
		"email":       "newadmin@test.dev",
		"displayName": "New Admin",
		"password":    "securepass123",
		"globalRole":  "ADMIN",
	}, testutil.SuperAdminUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)

	var body map[string]interface{}
	testutil.DecodeJSON(t, resp, &body)
	if body["globalRole"] != "ADMIN" {
		t.Errorf("expected globalRole=ADMIN, got %v", body["globalRole"])
	}
}

func TestSuperAdmin_CreateUser_MissingDisplayName_ReturnsMessageKey(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/users", map[string]interface{}{
		"email":      "noname@test.dev",
		"password":   "securepass123",
		"globalRole": "USER",
	}, testutil.SuperAdminUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)

	var body map[string]interface{}
	testutil.DecodeJSON(t, resp, &body)
	// validation.*, not errors.validation.* — the locale files carry these keys
	// at top level, and the errors.-prefixed spelling resolved to nothing (OCT-27).
	if want := "validation.displayNameRequired"; body["messageKey"] != want {
		t.Errorf("messageKey = %v, want %q", body["messageKey"], want)
	}
}

func TestAdmin_CannotCreateAdmin(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/users", map[string]interface{}{
		"email":       "attempted@test.dev",
		"displayName": "Attempt",
		"password":    "securepass123",
		"globalRole":  "ADMIN",
	}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
	_ = resp.Body.Close()
}

func TestSuperAdmin_CannotCreateSuperAdmin(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/users", map[string]interface{}{
		"email":       "evil@test.dev",
		"displayName": "Evil",
		"password":    "securepass123",
		"globalRole":  "SUPER_ADMIN",
	}, testutil.SuperAdminUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
	_ = resp.Body.Close()
}

func TestSuperAdmin_CanDisableAdmin(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPatch,
		"/api/v1/users/"+testutil.DemoUserID+"/disable",
		nil, testutil.SuperAdminUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()
}

func TestSuperAdmin_CannotDisableSuperAdmin(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPatch,
		"/api/v1/users/"+testutil.SuperAdminUserID+"/disable",
		nil, testutil.SuperAdminUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
	_ = resp.Body.Close()
}

func TestSuperAdmin_CanChangeUserRole(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPatch,
		"/api/v1/users/"+testutil.SecondUserID,
		map[string]interface{}{"globalRole": "ADMIN"},
		testutil.SuperAdminUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	var body map[string]interface{}
	testutil.DecodeJSON(t, resp, &body)
	if body["globalRole"] != "ADMIN" {
		t.Errorf("expected ADMIN, got %v", body["globalRole"])
	}
}

func TestSuperAdmin_CanChangeUserEmail(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPatch,
		"/api/v1/users/"+testutil.SecondUserID,
		map[string]interface{}{"email": "  Renamed@Test.dev "},
		testutil.SuperAdminUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	var body map[string]interface{}
	testutil.DecodeJSON(t, resp, &body)
	// Stored trimmed + lowercased, like CreateUser — login matches exactly.
	if body["email"] != "renamed@test.dev" {
		t.Errorf("expected renamed@test.dev, got %v", body["email"])
	}

	var action string
	err := db.QueryRow(
		`SELECT action FROM audit_logs WHERE target_id = $1 AND action = 'USER_EMAIL_CHANGED'`,
		testutil.SecondUserID,
	).Scan(&action)
	if err != nil {
		t.Errorf("expected a USER_EMAIL_CHANGED audit entry: %v", err)
	}
}

// The email is the login identifier, so the change must be effective at the
// login endpoint: the new address signs in, the old one no longer does.
func TestChangedEmail_WorksAtLogin(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/users", map[string]interface{}{
		"email":       "before@test.dev",
		"displayName": "Login Target",
		"password":    "loginpass123",
	}, testutil.SuperAdminUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var created map[string]interface{}
	testutil.DecodeJSON(t, resp, &created)
	userID, _ := created["id"].(string)

	resp = testutil.Do(t, srv, http.MethodPatch, "/api/v1/users/"+userID,
		map[string]interface{}{"email": "after@test.dev"}, testutil.SuperAdminUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()

	login := func(email string) *http.Response {
		return testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/login",
			map[string]interface{}{"email": email, "password": "loginpass123"}, "")
	}
	resp = login("after@test.dev")
	testutil.AssertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()
	resp = login("before@test.dev")
	testutil.AssertStatus(t, resp, http.StatusUnauthorized)
	_ = resp.Body.Close()
}

func TestSuperAdmin_ChangeUserEmail_Conflict(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPatch,
		"/api/v1/users/"+testutil.SecondUserID,
		map[string]interface{}{"email": "demo@octbase.dev"},
		testutil.SuperAdminUserID)
	testutil.AssertStatus(t, resp, http.StatusConflict)

	var body map[string]interface{}
	testutil.DecodeJSON(t, resp, &body)
	if body["code"] != "EMAIL_CONFLICT" {
		t.Errorf("expected EMAIL_CONFLICT, got %v", body["code"])
	}
}

func TestSuperAdmin_ChangeUserEmail_EmptyRejected(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPatch,
		"/api/v1/users/"+testutil.SecondUserID,
		map[string]interface{}{"email": "   "},
		testutil.SuperAdminUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
	_ = resp.Body.Close()
}

func TestAdmin_CannotChangeRoles(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPatch,
		"/api/v1/users/"+testutil.SecondUserID,
		map[string]interface{}{"globalRole": "ADMIN"},
		testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
	_ = resp.Body.Close()
}

// ── Manipulated role field rejection ─────────────────────────────────────────

func TestManipulatedRoleRejected(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	// A regular User tries to promote themselves by patching a role field.
	resp := testutil.Do(t, srv, http.MethodPatch,
		"/api/v1/users/"+testutil.SecondUserID,
		map[string]interface{}{"globalRole": "SUPER_ADMIN"},
		testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
	_ = resp.Body.Close()
}

// ── Audit log access ──────────────────────────────────────────────────────────

func TestSuperAdmin_CanViewAuditLogs(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/audit-logs", nil, testutil.SuperAdminUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()
}

func TestAdmin_CannotViewAuditLogs(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/audit-logs", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
	_ = resp.Body.Close()
}

// ── Project creation ──────────────────────────────────────────────────────────

func TestAdmin_CanCreateProject(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects",
		map[string]string{"name": "My Project"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)

	var p map[string]interface{}
	testutil.DecodeJSON(t, resp, &p)

	// Creator must be PROJECT_OWNER.
	mResp := testutil.Do(t, srv, http.MethodGet,
		"/api/v1/projects/"+p["id"].(string)+"/memberships", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, mResp, http.StatusOK)
	var memberships []map[string]interface{}
	testutil.DecodeJSON(t, mResp, &memberships)
	if len(memberships) == 0 {
		t.Fatal("expected creator to have a membership")
	}
	if memberships[0]["role"] != "PROJECT_OWNER" {
		t.Errorf("creator role = %v, want PROJECT_OWNER", memberships[0]["role"])
	}
}

func TestUser_CannotCreateProject(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects",
		map[string]string{"name": "Unauthorized"}, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
	_ = resp.Body.Close()
}

func TestGuest_CannotCreateProject(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects",
		map[string]string{"name": "Unauthorized"}, testutil.GuestUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
	_ = resp.Body.Close()
}

// ── Cross-project isolation ───────────────────────────────────────────────────

func TestUserA_CannotSeeUserBProject(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	// DemoUser creates a PRIVATE project.
	createResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects",
		map[string]string{"name": "Private Project", "visibility": "PRIVATE"}, testutil.DemoUserID)
	testutil.AssertStatus(t, createResp, http.StatusCreated)
	var p map[string]interface{}
	testutil.DecodeJSON(t, createResp, &p)
	pid := p["id"].(string)

	// SecondUser has no membership — must not learn the project exists (404).
	resp := testutil.Do(t, srv, http.MethodGet,
		"/api/v1/projects/"+pid, nil, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)
	_ = resp.Body.Close()
}

func TestUserA_CannotEditTaskFromProjectB(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	pid := testutil.MustCreateProject(t, srv, "Project B")
	tid := testutil.MustCreateTask(t, srv, pid, "Task in B")

	resp := testutil.Do(t, srv, http.MethodPatch,
		"/api/v1/tasks/"+tid,
		map[string]string{"title": "Hacked title"},
		testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
	_ = resp.Body.Close()
}

// ── Guest read-only access ────────────────────────────────────────────────────

func TestGuest_CannotCreateTask(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	pid := testutil.MustCreateProject(t, srv, "Guest Project")
	testutil.MustAddMember(t, srv, pid, testutil.GuestUserID, "PROJECT_VIEWER")

	resp := testutil.Do(t, srv, http.MethodPost,
		"/api/v1/projects/"+pid+"/tasks",
		map[string]string{"title": "Unauthorized task"},
		testutil.GuestUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
	_ = resp.Body.Close()
}

func TestGuest_CanReadAssignedProject(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	pid := testutil.MustCreateProject(t, srv, "Guest Readable Project")
	testutil.MustAddMember(t, srv, pid, testutil.GuestUserID, "PROJECT_VIEWER")

	resp := testutil.Do(t, srv, http.MethodGet,
		"/api/v1/projects/"+pid, nil, testutil.GuestUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()
}

// ── Disabled user ─────────────────────────────────────────────────────────────

func TestDisabledUser_CannotAccessAPI(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	// JWT is valid but user is disabled; LoadUserGlobalRole should reject with 401.
	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/auth/me", nil, testutil.DisabledUserID)
	testutil.AssertStatus(t, resp, http.StatusUnauthorized)
	_ = resp.Body.Close()
}

// ── Admin cannot manage Super Admin ──────────────────────────────────────────

func TestAdmin_CannotModifySuperAdmin(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	// Admin (DemoUserID) tries to update Super Admin — must be 403.
	resp := testutil.Do(t, srv, http.MethodPatch,
		"/api/v1/users/"+testutil.SuperAdminUserID,
		map[string]interface{}{"displayName": "Hacked"},
		testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
	_ = resp.Body.Close()
}

// ── User can only access assigned projects ────────────────────────────────────

func TestUser_CanOnlySeeAssignedProject(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	pid := testutil.MustCreateProject(t, srv, "Assigned Project")
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_MEMBER")

	// SecondUser can access the assigned project.
	resp := testutil.Do(t, srv, http.MethodGet,
		"/api/v1/projects/"+pid, nil, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()

	// SecondUser can create a task.
	resp2 := testutil.Do(t, srv, http.MethodPost,
		"/api/v1/projects/"+pid+"/tasks",
		map[string]string{"title": "User task"},
		testutil.SecondUserID)
	testutil.AssertStatus(t, resp2, http.StatusCreated)
	_ = resp2.Body.Close()
}

// ── Super Admin actions are audited ──────────────────────────────────────────

func TestSuperAdminActions_AreAudited(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	// Perform a Super Admin action (create user).
	_ = testutil.Do(t, srv, http.MethodPost, "/api/v1/users", map[string]interface{}{
		"email":       "audited@test.dev",
		"displayName": "Audited User",
		"password":    "securepass123",
		"globalRole":  "USER",
	}, testutil.SuperAdminUserID).Body.Close()

	// Check audit log contains the entry.
	auditResp := testutil.Do(t, srv, http.MethodGet, "/api/v1/audit-logs", nil, testutil.SuperAdminUserID)
	testutil.AssertStatus(t, auditResp, http.StatusOK)
	var result map[string]interface{}
	testutil.DecodeJSON(t, auditResp, &result)
	total, _ := result["total"].(float64)
	if total < 1 {
		t.Error("expected at least one audit log entry after Super Admin action")
	}
}

// ── Delete user ───────────────────────────────────────────────────────────────

func TestSuperAdmin_CanDeleteUser(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodDelete,
		"/api/v1/users/"+testutil.SecondUserID,
		nil, testutil.SuperAdminUserID)
	testutil.AssertStatus(t, resp, http.StatusNoContent)
	_ = resp.Body.Close()

	getResp := testutil.Do(t, srv, http.MethodGet,
		"/api/v1/users/"+testutil.SecondUserID,
		nil, testutil.SuperAdminUserID)
	testutil.AssertStatus(t, getResp, http.StatusNotFound)
	_ = getResp.Body.Close()
}

func TestSuperAdmin_CannotDeleteSuperAdmin(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodDelete,
		"/api/v1/users/"+testutil.SuperAdminUserID,
		nil, testutil.SuperAdminUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
	_ = resp.Body.Close()
}

func TestAdmin_CannotDeleteUser(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodDelete,
		"/api/v1/users/"+testutil.SecondUserID,
		nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
	_ = resp.Body.Close()
}

func TestSuperAdmin_DeleteUser_NotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodDelete,
		"/api/v1/users/00000000-0000-0000-0000-0000000000ff",
		nil, testutil.SuperAdminUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)
	_ = resp.Body.Close()
}

func TestSuperAdmin_DeleteUser_InvalidID(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodDelete,
		"/api/v1/users/"+strings.Repeat("x", 40),
		nil, testutil.SuperAdminUserID)
	testutil.AssertStatus(t, resp, http.StatusBadRequest)
	_ = resp.Body.Close()
}

// ── GDPR erasure ──────────────────────────────────────────────────────────────

// TestDeleteUser_ErasesUserWithRelationships deletes a user who actually owns
// data: a project membership, a created project, an authored comment, a sent
// invitation and a pending invitation addressed to them. A hard DELETE would
// violate the memberships/projects/invitations foreign keys, so deletion must
// anonymize in place while authored content survives under "Deleted user".
func TestDeleteUser_ErasesUserWithRelationships(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	pid := testutil.MustCreateProject(t, srv, "Erasure Project")
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_MEMBER")

	// Make the FK situation real: the user "created" the project, sent an
	// invitation, and has a pending invitation addressed to their email.
	if _, err := db.Exec(`UPDATE projects SET created_by_user_id=$1 WHERE id=$2`,
		testutil.SecondUserID, pid); err != nil {
		t.Fatalf("set project creator: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO invitations (id,email,project_id,role,token_hash,invited_by,expires_at)
		 VALUES ('10000000-0000-0000-0000-000000000001','invitee@test.dev',$1,'PROJECT_MEMBER','tok-sent-by-target',$2,$3)`,
		pid, testutil.SecondUserID, time.Now().Add(24*time.Hour)); err != nil {
		t.Fatalf("insert sent invitation: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO invitations (id,email,project_id,role,token_hash,invited_by,expires_at)
		 VALUES ('10000000-0000-0000-0000-000000000002','second@octbase.dev',$1,'PROJECT_MEMBER','tok-sent-to-target',$2,$3)`,
		pid, testutil.DemoUserID, time.Now().Add(24*time.Hour)); err != nil {
		t.Fatalf("insert received invitation: %v", err)
	}

	taskID := testutil.MustCreateTask(t, srv, pid, "Erasure task")
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+taskID+"/comments",
		map[string]string{"text": "written by the erased user"}, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	_ = resp.Body.Close()

	// Erase.
	resp = testutil.Do(t, srv, http.MethodDelete,
		"/api/v1/users/"+testutil.SecondUserID, nil, testutil.SuperAdminUserID)
	testutil.AssertStatus(t, resp, http.StatusNoContent)
	_ = resp.Body.Close()

	// Management API: 404 on GET, absent from the list.
	resp = testutil.Do(t, srv, http.MethodGet,
		"/api/v1/users/"+testutil.SecondUserID, nil, testutil.SuperAdminUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)
	_ = resp.Body.Close()

	listResp := testutil.Do(t, srv, http.MethodGet, "/api/v1/users", nil, testutil.SuperAdminUserID)
	testutil.AssertStatus(t, listResp, http.StatusOK)
	var users []map[string]interface{}
	testutil.DecodeJSON(t, listResp, &users)
	for _, u := range users {
		if u["id"] == testutil.SecondUserID {
			t.Error("deleted user still present in user list")
		}
	}

	// The row is a tombstone: no personal data left, account unusable.
	var email, displayName, status string
	var hash sql.NullString
	if err := db.QueryRow(
		`SELECT email, display_name, status, password_hash FROM users WHERE id=$1`,
		testutil.SecondUserID).Scan(&email, &displayName, &status, &hash); err != nil {
		t.Fatalf("tombstone row must remain: %v", err)
	}
	if strings.Contains(email, "second@octbase.dev") || displayName != "Deleted user" ||
		status != "deleted" || hash.Valid {
		t.Errorf("row not anonymized: email=%q name=%q status=%q hashSet=%v",
			email, displayName, status, hash.Valid)
	}

	// Operational data owned by the user is gone…
	for what, q := range map[string]string{
		"memberships":         `SELECT COUNT(*) FROM memberships WHERE user_id=$1`,
		"refresh tokens":      `SELECT COUNT(*) FROM refresh_tokens WHERE user_id=$1`,
		"invitations to user": `SELECT COUNT(*) FROM invitations WHERE email='second@octbase.dev' AND invited_by<>$1`,
	} {
		var n int
		if err := db.QueryRow(q, testutil.SecondUserID).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", what, err)
		}
		if n != 0 {
			t.Errorf("%s not cleaned up: %d rows left", what, n)
		}
	}
	// …but the invitation they sent survives (its FK now points at the tombstone).
	var sent int
	if err := db.QueryRow(`SELECT COUNT(*) FROM invitations WHERE invited_by=$1`,
		testutil.SecondUserID).Scan(&sent); err != nil {
		t.Fatalf("count sent invitations: %v", err)
	}
	if sent != 1 {
		t.Errorf("invitation sent by erased user should survive, got %d", sent)
	}

	// Authored content survives and renders under the anonymized name.
	cResp := testutil.Do(t, srv, http.MethodGet,
		"/api/v1/tasks/"+taskID+"/comments", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, cResp, http.StatusOK)
	var comments []map[string]interface{}
	testutil.DecodeJSON(t, cResp, &comments)
	if len(comments) != 1 {
		t.Fatalf("expected 1 surviving comment, got %d", len(comments))
	}
	if comments[0]["authorName"] != "Deleted user" {
		t.Errorf("comment author = %q, want %q", comments[0]["authorName"], "Deleted user")
	}

	// The freed email no longer logs in and can be re-registered.
	loginResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": "second@octbase.dev", "password": "irrelevant1"}, "")
	testutil.AssertStatus(t, loginResp, http.StatusUnauthorized)
	_ = loginResp.Body.Close()

	createResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/users", map[string]interface{}{
		"email": "second@octbase.dev", "displayName": "Reborn", "password": "Str0ngNewPass1",
	}, testutil.SuperAdminUserID)
	testutil.AssertStatus(t, createResp, http.StatusCreated)
	_ = createResp.Body.Close()

	// The deletion audit record must not retain the erased email.
	var meta string
	if err := db.QueryRow(
		`SELECT metadata_json FROM audit_logs WHERE action='USER_DELETED' AND target_id=$1`,
		testutil.SecondUserID).Scan(&meta); err != nil {
		t.Fatalf("audit row for deletion: %v", err)
	}
	if strings.Contains(meta, "second@octbase.dev") {
		t.Errorf("audit metadata leaks erased email: %s", meta)
	}
}
