package auth_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/octbase/octbase-api/internal/shared"
	"github.com/octbase/octbase-api/internal/testutil"
)

// ── Installation user limit (OCTBASE_MAX_USERS) on invitations ───────────────
// The testutil fixture seeds exactly 5 users, so a limit of 5 is already full
// and a limit of 6 has one free seat.

func TestCreateInvitation_UserLimitReached(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	_, _ = db.Exec(`UPDATE users SET is_admin = true WHERE id = $1`, testutil.DemoUserID)
	srv := testutil.NewTestServer(t, db, testutil.WithUserLimit(5))

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/admin/invitations",
		map[string]interface{}{"email": "invitee@test.dev"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)

	var body map[string]interface{}
	testutil.DecodeJSON(t, resp, &body)
	if body["code"] != "USER_LIMIT_REACHED" {
		t.Errorf("code = %v, want USER_LIMIT_REACHED", body["code"])
	}
}

func TestAcceptInvitation_UserLimitReached(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	_, _ = db.Exec(`UPDATE users SET is_admin = true WHERE id = $1`, testutil.DemoUserID)
	srv := testutil.NewTestServer(t, db, testutil.WithUserLimit(6))

	// Seat 6 is still free, so the invitation can be created…
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/admin/invitations",
		map[string]interface{}{"email": "invitee@test.dev"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var inv map[string]interface{}
	testutil.DecodeJSON(t, resp, &inv)
	rawToken := extractToken(inv["acceptURL"].(string))

	// …but the last seat fills up before the invitee gets around to accepting.
	if _, err := db.Exec(
		`INSERT INTO users (id,email,display_name,global_role,status,is_active,created_at,updated_at)
		 VALUES ('00000000-0000-0000-0000-00000000006a','squatter@test.dev','Seat Squatter','USER','active',true,$1,$2)`,
		shared.Now(), shared.Now(),
	); err != nil {
		t.Fatalf("insert seat squatter: %v", err)
	}

	resp = testutil.Do(t, srv, http.MethodPost,
		fmt.Sprintf("/api/v1/invitations/%s/accept", rawToken),
		map[string]string{"name": "Invitee", "password": "Str0ngPass123"}, "")
	testutil.AssertStatus(t, resp, http.StatusForbidden)
	var body map[string]interface{}
	testutil.DecodeJSON(t, resp, &body)
	if body["code"] != "USER_LIMIT_REACHED" {
		t.Errorf("code = %v, want USER_LIMIT_REACHED", body["code"])
	}
}
