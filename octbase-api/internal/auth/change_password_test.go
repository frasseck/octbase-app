package auth_test

import (
	"net/http"
	"testing"

	"github.com/octbase/octbase-api/internal/shared"
	"github.com/octbase/octbase-api/internal/testutil"
)

// Happy path: a signed-in user changes their own password. Other sessions are
// revoked, this one survives, the old password stops working and the new one
// works.
func TestChangePassword_Roundtrip(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	userID := shared.NewUUID()
	insertUserWithPassword(t, db, userID, "change-me@test.dev", "Change User", "OldPassword12345", true)
	srv := testutil.NewTestServer(t, db)

	// Two live sessions so we can prove the OTHER one is revoked.
	for i := 0; i < 2; i++ {
		resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/login",
			map[string]string{"email": "change-me@test.dev", "password": "OldPassword12345"}, "")
		testutil.AssertStatus(t, resp, http.StatusOK)
		_ = resp.Body.Close()
	}
	var nTokens int
	if err := db.QueryRow(`SELECT count(*) FROM refresh_tokens WHERE user_id = $1`, userID).Scan(&nTokens); err != nil || nTokens != 2 {
		t.Fatalf("expected 2 refresh tokens after two logins, got %d (err %v)", nTokens, err)
	}

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/change-password",
		map[string]string{"currentPassword": "OldPassword12345", "newPassword": "BrandNewPass9876"}, userID)
	testutil.AssertStatus(t, resp, http.StatusNoContent)
	_ = resp.Body.Close()

	// Every prior session revoked; exactly one fresh session re-established for
	// the caller.
	if err := db.QueryRow(`SELECT count(*) FROM refresh_tokens WHERE user_id = $1`, userID).Scan(&nTokens); err != nil || nTokens != 1 {
		t.Errorf("refresh tokens after change = %d, want 1 (err %v)", nTokens, err)
	}

	// Old password dead, new password works.
	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": "change-me@test.dev", "password": "OldPassword12345"}, "")
	testutil.AssertStatus(t, resp, http.StatusUnauthorized)
	_ = resp.Body.Close()
	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": "change-me@test.dev", "password": "BrandNewPass9876"}, "")
	testutil.AssertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()
}

func TestChangePassword_WrongCurrent(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	userID := shared.NewUUID()
	insertUserWithPassword(t, db, userID, "wrong-current@test.dev", "WC User", "OldPassword12345", true)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/change-password",
		map[string]string{"currentPassword": "not-the-password", "newPassword": "BrandNewPass9876"}, userID)
	testutil.AssertStatus(t, resp, http.StatusUnauthorized)
	var body map[string]any
	testutil.DecodeJSON(t, resp, &body)
	if body["code"] != "CURRENT_PASSWORD_INVALID" {
		t.Fatalf("code = %v, want CURRENT_PASSWORD_INVALID", body["code"])
	}
	// Password unchanged: original still logs in.
	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": "wrong-current@test.dev", "password": "OldPassword12345"}, "")
	testutil.AssertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()
}

func TestChangePassword_WeakNew(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	userID := shared.NewUUID()
	insertUserWithPassword(t, db, userID, "weak-new@test.dev", "Weak User", "OldPassword12345", true)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/change-password",
		map[string]string{"currentPassword": "OldPassword12345", "newPassword": "short"}, userID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
	_ = resp.Body.Close()
}

func TestChangePassword_SameAsCurrent(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	userID := shared.NewUUID()
	insertUserWithPassword(t, db, userID, "same-pw@test.dev", "Same User", "OldPassword12345", true)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/change-password",
		map[string]string{"currentPassword": "OldPassword12345", "newPassword": "OldPassword12345"}, userID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
	_ = resp.Body.Close()
}

func TestChangePassword_MissingFields(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	userID := shared.NewUUID()
	insertUserWithPassword(t, db, userID, "missing-fields@test.dev", "MF User", "OldPassword12345", true)
	srv := testutil.NewTestServer(t, db)

	// Empty currentPassword and empty newPassword each fail validation.
	for _, body := range []map[string]string{
		{"newPassword": "BrandNewPass9876"},     // currentPassword absent
		{"currentPassword": "OldPassword12345"}, // newPassword absent
	} {
		resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/change-password", body, userID)
		testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
		_ = resp.Body.Close()
	}
}

// A valid token whose user no longer exists (e.g. deleted between issue and use)
// is rejected as unauthenticated, not routed to the handler. shared.LoadUserGlobalRole
// runs ahead of every authenticated route and treats a missing user row the same
// way it treats a disabled or deleted one — 401, however valid the JWT still is.
// The handler's own sql.ErrNoRows → USER_NOT_FOUND branch is therefore unreachable
// through the router; it is deliberate defence-in-depth so the handler stays safe
// if called without that middleware, as its comment there says. Assert the status
// the caller actually observes.
func TestChangePassword_UserGone(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/change-password",
		map[string]string{"currentPassword": "whatever12345", "newPassword": "BrandNewPass9876"},
		shared.NewUUID()) // authenticated as a user id that was never inserted
	testutil.AssertStatus(t, resp, http.StatusUnauthorized)
	var body map[string]any
	testutil.DecodeJSON(t, resp, &body)
	if body["code"] != "UNAUTHORIZED" {
		t.Fatalf("code = %v, want UNAUTHORIZED", body["code"])
	}
}

func TestChangePassword_Unauthenticated(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	// No user id → no bearer token → the JWT middleware rejects it.
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/change-password",
		map[string]string{"currentPassword": "x", "newPassword": "BrandNewPass9876"}, "")
	testutil.AssertStatus(t, resp, http.StatusUnauthorized)
	_ = resp.Body.Close()
}
