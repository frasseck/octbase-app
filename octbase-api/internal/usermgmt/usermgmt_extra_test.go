package usermgmt_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
)

// ── GetUser ───────────────────────────────────────────────────────────────────

func TestGetUser_Success(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/users/"+testutil.SecondUserID, nil, testutil.SuperAdminUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var u map[string]interface{}
	testutil.DecodeJSON(t, resp, &u)
	if u["id"] != testutil.SecondUserID {
		t.Errorf("id = %v, want %s", u["id"], testutil.SecondUserID)
	}
}

func TestGetUser_InvalidID(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/users/has%20space", nil, testutil.SuperAdminUserID)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestGetUser_NotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/users/00000000-0000-0000-0000-0000000000ff",
		nil, testutil.SuperAdminUserID)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// ── CreateUser validation/conflict ───────────────────────────────────────────

func TestCreateUser_BadJSON(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/users", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testutil.TokenForUser(testutil.SuperAdminUserID))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestCreateUser_Validation(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	cases := []struct {
		name string
		body map[string]interface{}
		want int
	}{
		{"missing email", map[string]interface{}{"displayName": "X", "password": "Str0ngPass123"}, http.StatusUnprocessableEntity},
		{"short password", map[string]interface{}{"email": "a@test.dev", "displayName": "X", "password": "short"}, http.StatusUnprocessableEntity},
		{"invalid role", map[string]interface{}{"email": "b@test.dev", "displayName": "X", "password": "Str0ngPass123", "globalRole": "WIZARD"}, http.StatusUnprocessableEntity},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/users", c.body, testutil.SuperAdminUserID)
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != c.want {
				t.Errorf("status = %d, want %d", resp.StatusCode, c.want)
			}
		})
	}
}

func TestCreateUser_EmailConflict(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	body := map[string]interface{}{"email": "conflict@test.dev", "displayName": "C", "password": "Str0ngPass123"}
	first := testutil.Do(t, srv, http.MethodPost, "/api/v1/users", body, testutil.SuperAdminUserID)
	testutil.AssertStatus(t, first, http.StatusCreated)
	_ = first.Body.Close()

	second := testutil.Do(t, srv, http.MethodPost, "/api/v1/users", body, testutil.SuperAdminUserID)
	defer func() { _ = second.Body.Close() }()
	if second.StatusCode != http.StatusConflict {
		t.Errorf("status = %d, want 409", second.StatusCode)
	}
}

// TestCreateUser_RecordsForwardedFor covers the X-Forwarded-For path in clientIP
// and the successful create-and-return-user flow.
func TestCreateUser_RecordsForwardedFor(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/users",
		strings.NewReader(`{"email":"xff-user@test.dev","displayName":"XFF","password":"Str0ngPass123"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testutil.TokenForUser(testutil.SuperAdminUserID))
	req.Header.Set("X-Forwarded-For", "198.51.100.7, 10.0.0.1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("status = %d, want 201", resp.StatusCode)
	}
}

// TestCreateUser_PasswordTooLong covers the HashPassword error branch: a
// password that passes the >=8 check but exceeds bcrypt's 72-byte limit.
func TestCreateUser_PasswordTooLong(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/users",
		map[string]interface{}{"email": "longpw@test.dev", "displayName": "L", "password": strings.Repeat("a", 73)},
		testutil.SuperAdminUserID)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}

// ── UpdateUser ────────────────────────────────────────────────────────────────

func TestUpdateUser_RoleErrors(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)

	// Assigning SUPER_ADMIN via the API is forbidden.
	forbid := testutil.Do(t, srv, http.MethodPatch, "/api/v1/users/"+testutil.SecondUserID,
		map[string]interface{}{"globalRole": "SUPER_ADMIN"}, testutil.SuperAdminUserID)
	defer func() { _ = forbid.Body.Close() }()
	if forbid.StatusCode != http.StatusForbidden {
		t.Errorf("assign SUPER_ADMIN: status = %d, want 403", forbid.StatusCode)
	}

	// An otherwise-allowed role change to an unknown role is a 422.
	invalid := testutil.Do(t, srv, http.MethodPatch, "/api/v1/users/"+testutil.SecondUserID,
		map[string]interface{}{"globalRole": "WIZARD"}, testutil.SuperAdminUserID)
	defer func() { _ = invalid.Body.Close() }()
	if invalid.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("invalid role: status = %d, want 422", invalid.StatusCode)
	}
}

func TestUpdateUser_Success(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/users/"+testutil.SecondUserID,
		map[string]interface{}{"displayName": "Renamed", "status": "disabled"}, testutil.SuperAdminUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var u map[string]interface{}
	testutil.DecodeJSON(t, resp, &u)
	if u["displayName"] != "Renamed" {
		t.Errorf("displayName = %v, want Renamed", u["displayName"])
	}
	if u["status"] != "disabled" {
		t.Errorf("status = %v, want disabled", u["status"])
	}
}

// TestUpdateUser_SuperAdminSelfEdit covers OCT-12: a Super Admin may edit
// their own profile. The another-Super-Admin guard used to match the actor
// too, which left the role unable to change its own display name at all —
// there is no self-service profile route, so this is the only write path onto
// a user record. Role and status stay locked even on the self path, since
// this must not become a way around the demote/disable rules.
func TestUpdateUser_SuperAdminSelfEdit(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	self := "/api/v1/users/" + testutil.SuperAdminUserID

	// The point of the fix: renaming yourself works.
	resp := testutil.Do(t, srv, http.MethodPatch, self,
		map[string]interface{}{"displayName": "Nicole Muster"}, testutil.SuperAdminUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var u map[string]interface{}
	testutil.DecodeJSON(t, resp, &u)
	if u["displayName"] != "Nicole Muster" {
		t.Errorf("displayName = %v, want Nicole Muster", u["displayName"])
	}

	// Self-demotion and self-disabling stay refused: the first would strip the
	// installation of its only unrestricted role, the second would lock the
	// signed-in account out of itself.
	for _, c := range []struct {
		name string
		body map[string]interface{}
	}{
		{"self demote", map[string]interface{}{"globalRole": "USER"}},
		{"self disable", map[string]interface{}{"status": "disabled"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := testutil.Do(t, srv, http.MethodPatch, self, c.body, testutil.SuperAdminUserID)
			defer func() { _ = r.Body.Close() }()
			if r.StatusCode != http.StatusForbidden {
				t.Errorf("status = %d, want 403", r.StatusCode)
			}
		})
	}

	// Restating the current status is not a change, so it is not refused —
	// the guard is about moving a Super Admin's status, not about the field
	// appearing in the body alongside an edit.
	keep := testutil.Do(t, srv, http.MethodPatch, self,
		map[string]interface{}{"displayName": "Nicole M.", "status": "active"}, testutil.SuperAdminUserID)
	testutil.AssertStatus(t, keep, http.StatusOK)

	// A *different* Super Admin is still off limits — the guard narrowed to
	// exclude the actor, it did not go away.
	other := "00000000-0000-0000-0000-0000000000a1"
	if _, err := db.Exec(
		`INSERT INTO users (id,email,display_name,global_role,status,is_active,created_at,updated_at)
		 VALUES ($1,'other-super@test.dev','Other Super','SUPER_ADMIN','active',true,now(),now())`, other,
	); err != nil {
		t.Fatalf("seed second super admin: %v", err)
	}
	forbid := testutil.Do(t, srv, http.MethodPatch, "/api/v1/users/"+other,
		map[string]interface{}{"displayName": "Hijacked"}, testutil.SuperAdminUserID)
	defer func() { _ = forbid.Body.Close() }()
	if forbid.StatusCode != http.StatusForbidden {
		t.Errorf("edit another Super Admin: status = %d, want 403", forbid.StatusCode)
	}
}

func TestUpdateUser_Errors(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	missing := "00000000-0000-0000-0000-0000000000ee"
	cases := []struct {
		name   string
		target string
		body   map[string]interface{}
		want   int
	}{
		{"invalid id", "bad id", map[string]interface{}{"displayName": "X"}, http.StatusBadRequest},
		{"not found", missing, map[string]interface{}{"displayName": "X"}, http.StatusNotFound},
		{"invalid status", testutil.SecondUserID, map[string]interface{}{"status": "frozen"}, http.StatusUnprocessableEntity},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/users/"+strings.ReplaceAll(c.target, " ", "%20"),
				c.body, testutil.SuperAdminUserID)
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != c.want {
				t.Errorf("status = %d, want %d", resp.StatusCode, c.want)
			}
		})
	}
}

func TestUpdateUser_BadJSON(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/api/v1/users/"+testutil.SecondUserID, strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testutil.TokenForUser(testutil.SuperAdminUserID))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// ── DisableUser error branches ───────────────────────────────────────────────

func TestDisableUser_Errors(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	missing := "00000000-0000-0000-0000-0000000000dd"
	cases := []struct {
		name   string
		target string
		want   int
	}{
		{"invalid id", "bad%20id", http.StatusBadRequest},
		{"not found", missing, http.StatusNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := testutil.Do(t, srv, http.MethodPatch,
				fmt.Sprintf("/api/v1/users/%s/disable", c.target), nil, testutil.SuperAdminUserID)
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != c.want {
				t.Errorf("status = %d, want %d", resp.StatusCode, c.want)
			}
		})
	}
}
