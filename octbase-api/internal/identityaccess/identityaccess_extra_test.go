package identityaccess_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/octbase/octbase-api/internal/auditlog"
	"github.com/octbase/octbase-api/internal/identityaccess"
	"github.com/octbase/octbase-api/internal/shared"
	"github.com/octbase/octbase-api/internal/testutil"
)

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

func newIAHandler(db *sql.DB) *identityaccess.Handler {
	return identityaccess.NewHandler(db, identityaccess.NewUserRepo(db),
		identityaccess.NewMembershipRepo(db), auditlog.NewRepo(db))
}

func withUser(req *http.Request, userID string) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), shared.UserIDKey, userID))
}

// TestGetMe_DirectBranches covers GetMe's 401 (no user id) and 500 (DB error)
// guards, which the wired middleware otherwise makes unreachable.
func TestGetMe_DirectBranches(t *testing.T) {
	// 401: no user id in context.
	h := newIAHandler(nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/me", nil)
	rec := httptest.NewRecorder()
	h.GetMe(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no user: status = %d, want 401", rec.Code)
	}

	// 500: user id present but the lookup fails.
	hc := newIAHandler(closedDB(t))
	req = withUser(httptest.NewRequest(http.MethodGet, "/api/v1/users/me", nil), shared.NewUUID())
	rec = httptest.NewRecorder()
	hc.GetMe(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("db error: status = %d, want 500", rec.Code)
	}
}

// TestMemberGuard_DBError covers the server-error branch in memberGuard (and the
// equivalent inline guard in ListMembers) when the membership lookup fails.
func TestMemberGuard_DBError(t *testing.T) {
	hc := newIAHandler(closedDB(t))

	// GetPermissions -> memberGuard -> RequireProjectMember fails -> 500.
	req := withUser(httptest.NewRequest(http.MethodGet, "/api/v1/projects/p/permissions", nil), shared.NewUUID())
	rec := httptest.NewRecorder()
	hc.GetPermissions(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("permissions: status = %d, want 500", rec.Code)
	}

	// ListMembers has its own inline membership guard -> 500 on DB error.
	req = withUser(httptest.NewRequest(http.MethodGet, "/api/v1/projects/p/members", nil), shared.NewUUID())
	rec = httptest.NewRecorder()
	hc.ListMembers(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("members: status = %d, want 500", rec.Code)
	}

	// ListMemberships has the same inline membership guard as ListMembers ->
	// 500 on DB error once authenticated.
	req = withUser(httptest.NewRequest(http.MethodGet, "/api/v1/projects/p/memberships", nil), shared.NewUUID())
	rec = httptest.NewRecorder()
	hc.ListMemberships(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("memberships: status = %d, want 500", rec.Code)
	}
}

// TestListMemberships_NoUser covers ListMemberships' 401 (no user id) guard,
// mirroring ListMembers.
func TestListMemberships_NoUser(t *testing.T) {
	h := newIAHandler(nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects/p/memberships", nil)
	rec := httptest.NewRecorder()
	h.ListMemberships(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

// TestGetMe_MissingUser401Direct also covers memberGuard's empty-user 401 guard.
func TestMemberGuard_NoUser(t *testing.T) {
	h := newIAHandler(nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects/p/permissions", nil)
	rec := httptest.NewRecorder()
	h.GetPermissions(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

// GetPermissions as Super Admin exercises memberGuard's Super-Admin bypass.
func TestGetPermissions_SuperAdminBypass(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "SA Perms Project")
	// Super Admin is not a member but still gets permissions (treated as admin).
	resp := testutil.Do(t, srv, http.MethodGet,
		fmt.Sprintf("/api/v1/projects/%s/permissions", pid), nil, testutil.SuperAdminUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var out map[string]interface{}
	testutil.DecodeJSON(t, resp, &out)
	if out["role"] != "PROJECT_ADMIN" {
		t.Errorf("role = %v, want PROJECT_ADMIN for Super Admin bypass", out["role"])
	}
}

// A non-member must not be able to list a project's members.
func TestListMembers_NonMemberForbidden(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Members Project")
	resp := testutil.Do(t, srv, http.MethodGet,
		fmt.Sprintf("/api/v1/projects/%s/members", pid), nil, testutil.SecondUserID)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

func TestAddMember_BadJSON(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "AddBadJSON Project")
	resp := patchOrPost(t, srv, http.MethodPost,
		fmt.Sprintf("/api/v1/projects/%s/memberships", pid), "{bad", testutil.DemoUserID)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestAddMember_InvalidRole(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "AddInvalidRole Project")
	resp := testutil.Do(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/memberships", pid),
		map[string]string{"userId": testutil.SecondUserID, "role": "BOGUS"}, testutil.DemoUserID)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", resp.StatusCode)
	}
}

// TestAddMember_ExistingUser covers the "add existing user" path: a Super Admin
// adds a seeded account directly, a repeat add is a clean conflict, and an
// unknown id is a 404 rather than a foreign-key 500.
func TestAddMember_ExistingUser(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "AddExisting Project")

	// First add succeeds.
	ok := testutil.Do(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/memberships", pid),
		map[string]string{"userId": testutil.SecondUserID, "role": "PROJECT_MEMBER"}, testutil.SuperAdminUserID)
	if ok.StatusCode != http.StatusCreated {
		t.Errorf("add existing user: status = %d, want 201", ok.StatusCode)
	}
	_ = ok.Body.Close()

	// Re-adding the same user violates the unique constraint -> 409.
	dup := testutil.Do(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/memberships", pid),
		map[string]string{"userId": testutil.SecondUserID, "role": "PROJECT_MEMBER"}, testutil.SuperAdminUserID)
	if dup.StatusCode != http.StatusConflict {
		t.Errorf("duplicate member: status = %d, want 409", dup.StatusCode)
	}
	_ = dup.Body.Close()

	// Unknown (but well-formed) user id -> 404.
	nf := testutil.Do(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/memberships", pid),
		map[string]string{"userId": "00000000-0000-0000-0000-0000000000aa", "role": "PROJECT_MEMBER"}, testutil.SuperAdminUserID)
	if nf.StatusCode != http.StatusNotFound {
		t.Errorf("unknown user: status = %d, want 404", nf.StatusCode)
	}
	_ = nf.Body.Close()

	// Missing userId -> 422 validation error.
	bad := testutil.Do(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/memberships", pid),
		map[string]string{"role": "PROJECT_MEMBER"}, testutil.SuperAdminUserID)
	if bad.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("missing userId: status = %d, want 422", bad.StatusCode)
	}
	_ = bad.Body.Close()
}

func TestUpdateRole_Errors(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "UpdateRole Project")
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_MEMBER")

	// Invalid role -> 422.
	inv := testutil.Do(t, srv, http.MethodPatch,
		fmt.Sprintf("/api/v1/projects/%s/memberships/%s", pid, testutil.SecondUserID),
		map[string]string{"role": "BOGUS"}, testutil.DemoUserID)
	if inv.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("invalid role: status = %d, want 422", inv.StatusCode)
	}
	_ = inv.Body.Close()

	// Bad JSON -> 400.
	bad := patchOrPost(t, srv, http.MethodPatch,
		fmt.Sprintf("/api/v1/projects/%s/memberships/%s", pid, testutil.SecondUserID), "{bad", testutil.DemoUserID)
	if bad.StatusCode != http.StatusBadRequest {
		t.Errorf("bad json: status = %d, want 400", bad.StatusCode)
	}
	_ = bad.Body.Close()

	// Unknown member (valid role) -> 404.
	nf := testutil.Do(t, srv, http.MethodPatch,
		fmt.Sprintf("/api/v1/projects/%s/memberships/%s", pid, testutil.GuestUserID),
		map[string]string{"role": "PROJECT_MEMBER"}, testutil.DemoUserID)
	if nf.StatusCode != http.StatusNotFound {
		t.Errorf("unknown member: status = %d, want 404", nf.StatusCode)
	}
	_ = nf.Body.Close()
}

func TestRemoveMember_NotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "RemoveNF Project")
	resp := testutil.Do(t, srv, http.MethodDelete,
		fmt.Sprintf("/api/v1/projects/%s/memberships/%s", pid, testutil.GuestUserID), nil, testutil.DemoUserID)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// patchOrPost issues a request with a raw (possibly malformed) JSON body and a
// bearer token for userID.
func patchOrPost(t *testing.T, srv *httptest.Server, method, path, body, userID string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testutil.TokenForUser(userID))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	return resp
}
