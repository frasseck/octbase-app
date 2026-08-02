package mfa_test

import (
	"bytes"
	"net/http"
	"testing"
	"time"

	internalauth "github.com/octbase/octbase-api/internal/auth"
	"github.com/octbase/octbase-api/internal/testutil"
)

// TestEnroll_ReauthRejectionCode pins the error shape of the enroll re-auth
// requirement: an access token without (or with a wrong) password answers
// 401 with the stable code REAUTH_REQUIRED — the code the frontends key their
// password prompt on.
func TestEnroll_ReauthRejectionCode(t *testing.T) {
	setMFAEncKey(t)
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)

	for name, body := range map[string]map[string]string{
		"no password":    {},
		"wrong password": {"password": "not-the-password"},
	} {
		resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/users/me/mfa/enroll", body, testutil.DemoUserID)
		testutil.AssertStatus(t, resp, http.StatusUnauthorized)
		var out map[string]any
		testutil.DecodeJSON(t, resp, &out)
		if out["code"] != "REAUTH_REQUIRED" {
			t.Errorf("%s: code = %v, want REAUTH_REQUIRED", name, out["code"])
		}
	}
}

// TestEnroll_ForcedEnrollmentTokenExempt: the forced-enrollment-at-login flow
// authenticates with a scoped enrollment token minted right after a successful
// password login, so it must NOT be asked for the password again — the token
// itself is the fresh proof.
func TestEnroll_ForcedEnrollmentTokenExempt(t *testing.T) {
	setMFAEncKey(t)
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)

	token, err := internalauth.IssueMFAEnrollmentToken(testutil.DemoUserID, testutil.TestJWTSecret, time.Minute)
	if err != nil {
		t.Fatalf("issue enrollment token: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/users/me/mfa/enroll", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	testutil.AssertStatus(t, resp, http.StatusOK)
	var out map[string]any
	testutil.DecodeJSON(t, resp, &out)
	if out["secret"] == nil || out["secret"] == "" {
		t.Error("expected a pending TOTP secret from token-authenticated enrollment")
	}
}
