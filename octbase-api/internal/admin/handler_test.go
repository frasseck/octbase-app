package admin_test

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/octbase/octbase-api/internal/admin"
	"github.com/octbase/octbase-api/internal/auditlog"
	"github.com/octbase/octbase-api/internal/auth"
	"github.com/octbase/octbase-api/internal/shared"
	"github.com/octbase/octbase-api/internal/testutil"
)

// Coverage note: allowLegacyTarget's fail-closed branch (a real DB error on
// the target-role lookup → 500, never a bypass) is not reachably testable
// here without fault injection — closing the *sql.DB makes the earlier
// LoadUserGlobalRole middleware fail with 401 before the guard runs, and the
// test harness has no per-query error hook. The branch is documented rather
// than silently uncovered; if a DB-fault harness is ever added, assert that an
// ADMIN acting on a SUPER_ADMIN during a lookup error gets 500, not success.

// newAdminTestServer wires the auth + RBAC middleware chain and admin routes.
// Mirrors the production wiring: JWTMiddleware → LoadUserGlobalRole → RequireAdmin.
func newAdminTestServer(t *testing.T, db *sql.DB) *httptest.Server {
	t.Helper()
	provider := auth.NewEmailProvider(db, testutil.TestJWTSecret)
	adminHandler := admin.NewHandler(db, auditlog.NewRepo(db))

	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Group(func(r chi.Router) {
		r.Use(auth.JWTMiddleware(provider))
		r.Use(shared.LoadUserGlobalRole(db))
		r.Use(shared.RequireJSON)
		r.Group(func(r chi.Router) {
			r.Use(admin.RequireAdmin())
			adminHandler.RegisterRoutes(r)
		})
	})

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func TestRequireAdmin_Rejects_NonAdmin(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := newAdminTestServer(t, db)

	// SecondUserID has global_role=USER — not an admin.
	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/admin/users", nil, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)

	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "FORBIDDEN" {
		t.Errorf("code = %v, want FORBIDDEN", e["code"])
	}
}

func TestListUsers_AdminOnly(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	// DemoUserID has global_role=ADMIN — no extra setup needed.
	srv := newAdminTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/admin/users", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	var users []interface{}
	testutil.DecodeJSON(t, resp, &users)
	if len(users) < 1 {
		t.Errorf("expected at least 1 user in list, got %d", len(users))
	}
	first, _ := users[0].(map[string]interface{})
	for _, field := range []string{"id", "email", "displayName", "globalRole", "status"} {
		if _, ok := first[field]; !ok {
			t.Errorf("expected field %q in user object", field)
		}
	}
}

func TestUpdateUser_Deactivate(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := newAdminTestServer(t, db)

	// Deactivate the second user.
	resp := testutil.Do(t, srv, http.MethodPatch,
		"/api/v1/admin/users/"+testutil.SecondUserID,
		map[string]interface{}{"isActive": false},
		testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	var result map[string]interface{}
	testutil.DecodeJSON(t, resp, &result)
	if result["isActive"] != false {
		t.Errorf("isActive = %v, want false", result["isActive"])
	}

	// Verify in the DB.
	var active bool
	if err := db.QueryRow(`SELECT is_active FROM users WHERE id = $1`, testutil.SecondUserID).Scan(&active); err != nil {
		t.Fatalf("query user: %v", err)
	}
	if active {
		t.Error("expected user to be deactivated in the database")
	}

	var action string
	if err := db.QueryRow(
		`SELECT action FROM audit_logs WHERE target_id = $1 ORDER BY created_at DESC LIMIT 1`,
		testutil.SecondUserID,
	).Scan(&action); err != nil {
		t.Fatalf("query audit log: %v", err)
	}
	if action != auditlog.ActionUserDisabled {
		t.Errorf("audit action = %q, want %q", action, auditlog.ActionUserDisabled)
	}
}

func TestUpdateUser_UnknownOrDeleted_NotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := newAdminTestServer(t, db)

	// Unknown user id.
	resp := testutil.Do(t, srv, http.MethodPatch,
		"/api/v1/admin/users/00000000-0000-0000-0000-0000000000ff",
		map[string]interface{}{"isActive": true},
		testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)
	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "USER_NOT_FOUND" {
		t.Errorf("code = %v, want USER_NOT_FOUND", e["code"])
	}

	// A GDPR-erased tombstone must not be resurrectable via the legacy endpoint.
	if _, err := db.Exec(`UPDATE users SET status='deleted', is_active=false WHERE id=$1`,
		testutil.SecondUserID); err != nil {
		t.Fatalf("mark user deleted: %v", err)
	}
	resp = testutil.Do(t, srv, http.MethodPatch,
		"/api/v1/admin/users/"+testutil.SecondUserID,
		map[string]interface{}{"isActive": true},
		testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)
	_ = resp.Body.Close()

	var status string
	if err := db.QueryRow(`SELECT status FROM users WHERE id=$1`, testutil.SecondUserID).Scan(&status); err != nil {
		t.Fatalf("query user: %v", err)
	}
	if status != "deleted" {
		t.Errorf("status = %q, want tombstone to stay deleted", status)
	}
}

func TestUpdateUser_MissingIsActive(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := newAdminTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPatch,
		"/api/v1/admin/users/"+testutil.SecondUserID,
		map[string]interface{}{},
		testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)

	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "VALIDATION_ERROR" {
		t.Errorf("code = %v, want VALIDATION_ERROR", e["code"])
	}
}

func TestResetPassword_OK(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := newAdminTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPost,
		"/api/v1/admin/users/"+testutil.SecondUserID+"/reset-password",
		map[string]string{},
		testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	var result map[string]interface{}
	testutil.DecodeJSON(t, resp, &result)
	if result["message"] == nil {
		t.Error("expected message in reset-password response")
	}

	var action string
	if err := db.QueryRow(
		`SELECT action FROM audit_logs WHERE target_id = $1 ORDER BY created_at DESC LIMIT 1`,
		testutil.SecondUserID,
	).Scan(&action); err != nil {
		t.Fatalf("query audit log: %v", err)
	}
	if action != auditlog.ActionUserPasswordReset {
		t.Errorf("audit action = %q, want %q", action, auditlog.ActionUserPasswordReset)
	}
}

func TestResetPassword_UserNotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := newAdminTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPost,
		"/api/v1/admin/users/00000000-0000-0000-0000-000000000000/reset-password",
		map[string]string{},
		testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)

	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "USER_NOT_FOUND" {
		t.Errorf("code = %v, want USER_NOT_FOUND", e["code"])
	}
}
