package usermgmt_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
)

// ── Installation user limit (OCTBASE_MAX_USERS) ──────────────────────────────
// The testutil fixture seeds exactly 5 users (super admin, demo admin, second,
// guest, disabled), so a limit of 5 is already full and a limit of 6 has one
// free seat.

func createUserPayload(email string) map[string]interface{} {
	return map[string]interface{}{
		"email":       email,
		"displayName": "Seat Taker",
		"password":    "securepass123",
		"globalRole":  "USER",
	}
}

func TestCreateUser_UserLimitReached(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db, testutil.WithUserLimit(5))

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/users",
		createUserPayload("sixth@test.dev"), testutil.SuperAdminUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)

	var body map[string]interface{}
	testutil.DecodeJSON(t, resp, &body)
	if body["code"] != "USER_LIMIT_REACHED" {
		t.Errorf("code = %v, want USER_LIMIT_REACHED", body["code"])
	}
	if want := "errors.userLimitReached"; body["messageKey"] != want {
		t.Errorf("messageKey = %v, want %q", body["messageKey"], want)
	}
}

func TestCreateUser_LastFreeSeat_ThenLimit(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db, testutil.WithUserLimit(6))

	// Seat 6 of 6 is free.
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/users",
		createUserPayload("sixth@test.dev"), testutil.SuperAdminUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var created map[string]interface{}
	testutil.DecodeJSON(t, resp, &created)

	// Seat 7 is not.
	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/users",
		createUserPayload("seventh@test.dev"), testutil.SuperAdminUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
	_ = resp.Body.Close()

	// GDPR-deleting an account turns it into a tombstone, which does not
	// occupy a seat — the same email's replacement fits again.
	resp = testutil.Do(t, srv, http.MethodDelete,
		fmt.Sprintf("/api/v1/users/%s", created["id"]), nil, testutil.SuperAdminUserID)
	testutil.AssertStatus(t, resp, http.StatusNoContent)
	_ = resp.Body.Close()

	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/users",
		createUserPayload("seventh@test.dev"), testutil.SuperAdminUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	_ = resp.Body.Close()
}

func TestCreateUser_NoLimitByDefault(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db) // no WithUserLimit → unlimited

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/users",
		createUserPayload("sixth@test.dev"), testutil.SuperAdminUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	_ = resp.Body.Close()
}
