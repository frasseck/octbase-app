package auth_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/octbase/octbase-api/internal/shared"
	"github.com/octbase/octbase-api/internal/testutil"
)

func mfaEncKeyEnv(t *testing.T) {
	t.Helper()
	t.Setenv("OCTBASE_MFA_ENC_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
}

// With enforcement off (the default), a user without MFA logs straight in.
func TestRequireMFA_Off_IssuesTokenWithoutEnrollment(t *testing.T) {
	mfaEncKeyEnv(t)
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	insertUserWithPassword(t, db, shared.NewUUID(), "off@test.dev", "Off User", "Str0ngPass123", true)

	srv := testutil.NewTestServer(t, db) // no WithRequireMFA
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": "off@test.dev", "password": "Str0ngPass123"}, "")
	testutil.AssertStatus(t, resp, http.StatusOK)

	var result map[string]interface{}
	testutil.DecodeJSON(t, resp, &result)
	if result["accessToken"] == nil || result["accessToken"] == "" {
		t.Fatal("expected an accessToken when MFA enforcement is off")
	}
	if result["mfaEnrollmentRequired"] == true {
		t.Fatal("did not expect enrollment to be required when enforcement is off")
	}
}

// With mode "admins", an admin without MFA gets an enrollment challenge, no session.
func TestRequireMFA_Admins_AdminWithoutMFA_ForcedToEnroll(t *testing.T) {
	mfaEncKeyEnv(t)
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	id := shared.NewUUID()
	insertUserWithPassword(t, db, id, "admin@test.dev", "Admin", "Str0ngPass123", true)
	if _, err := db.Exec(`UPDATE users SET global_role='ADMIN' WHERE id=$1`, id); err != nil {
		t.Fatalf("set role: %v", err)
	}

	srv := testutil.NewTestServer(t, db, testutil.WithRequireMFA("admins"))
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": "admin@test.dev", "password": "Str0ngPass123"}, "")
	testutil.AssertStatus(t, resp, http.StatusOK)

	var result map[string]interface{}
	testutil.DecodeJSON(t, resp, &result)
	if result["mfaEnrollmentRequired"] != true {
		t.Fatalf("expected mfaEnrollmentRequired=true, got %v", result["mfaEnrollmentRequired"])
	}
	if result["accessToken"] != nil {
		t.Fatal("expected no accessToken while enrollment is required")
	}
	if result["enrollmentToken"] == nil || result["enrollmentToken"] == "" {
		t.Fatal("expected a non-empty enrollmentToken")
	}
}

// With mode "admins", a non-admin is unaffected and logs straight in.
func TestRequireMFA_Admins_RegularUserUnaffected(t *testing.T) {
	mfaEncKeyEnv(t)
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	insertUserWithPassword(t, db, shared.NewUUID(), "user@test.dev", "User", "Str0ngPass123", true)

	srv := testutil.NewTestServer(t, db, testutil.WithRequireMFA("admins"))
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": "user@test.dev", "password": "Str0ngPass123"}, "")
	testutil.AssertStatus(t, resp, http.StatusOK)

	var result map[string]interface{}
	testutil.DecodeJSON(t, resp, &result)
	if result["accessToken"] == nil || result["accessToken"] == "" {
		t.Fatal("a non-admin should log in normally under mode 'admins'")
	}
}

// The end-to-end forced-enrollment flow: the enrollment token unlocks enroll +
// confirm, and after confirming, a fresh login returns the normal MFA challenge.
func TestRequireMFA_All_EnrollmentTokenCompletesSetup(t *testing.T) {
	mfaEncKeyEnv(t)
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	insertUserWithPassword(t, db, shared.NewUUID(), "enrol@test.dev", "Enrol", "Str0ngPass123", true)
	srv := testutil.NewTestServer(t, db, testutil.WithRequireMFA("all"))

	// 1. Login → enrollment required.
	loginResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": "enrol@test.dev", "password": "Str0ngPass123"}, "")
	testutil.AssertStatus(t, loginResp, http.StatusOK)
	var login map[string]interface{}
	testutil.DecodeJSON(t, loginResp, &login)
	enrollToken, _ := login["enrollmentToken"].(string)
	if enrollToken == "" {
		t.Fatal("expected an enrollmentToken")
	}

	// 2. The enrollment token unlocks /mfa/enroll (Bearer-authenticated).
	enrollResp := doBearer(t, srv, http.MethodPost, "/api/v1/users/me/mfa/enroll", nil, enrollToken)
	testutil.AssertStatus(t, enrollResp, http.StatusOK)
	var enroll map[string]interface{}
	testutil.DecodeJSON(t, enrollResp, &enroll)
	secret, _ := enroll["secret"].(string)
	if secret == "" {
		t.Fatal("expected a TOTP secret from enroll")
	}

	// 3. Confirm with a valid code activates MFA.
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}
	confirmResp := doBearer(t, srv, http.MethodPost, "/api/v1/users/me/mfa/confirm",
		map[string]string{"code": code}, enrollToken)
	testutil.AssertStatus(t, confirmResp, http.StatusOK)

	// 4. A fresh login now returns the normal MFA challenge, not enrollment.
	login2 := testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": "enrol@test.dev", "password": "Str0ngPass123"}, "")
	testutil.AssertStatus(t, login2, http.StatusOK)
	var l2 map[string]interface{}
	testutil.DecodeJSON(t, login2, &l2)
	if l2["mfaRequired"] != true {
		t.Fatalf("expected mfaRequired=true after enrolling, got %v", l2["mfaRequired"])
	}
	if l2["mfaEnrollmentRequired"] == true {
		t.Fatal("enrollment should no longer be required once MFA is active")
	}
}

// Security property: an enrollment token must not authenticate anywhere except
// the enroll/confirm endpoints — it is not a session.
func TestEnrollmentToken_CannotAccessDomainRoutes(t *testing.T) {
	mfaEncKeyEnv(t)
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	insertUserWithPassword(t, db, shared.NewUUID(), "scope@test.dev", "Scope", "Str0ngPass123", true)
	srv := testutil.NewTestServer(t, db, testutil.WithRequireMFA("all"))

	loginResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": "scope@test.dev", "password": "Str0ngPass123"}, "")
	var login map[string]interface{}
	testutil.DecodeJSON(t, loginResp, &login)
	enrollToken := login["enrollmentToken"].(string)

	// /auth/me is a normal authenticated route — the enrollment token must fail.
	resp := doBearer(t, srv, http.MethodGet, "/api/v1/auth/me", nil, enrollToken)
	testutil.AssertStatus(t, resp, http.StatusUnauthorized)
}

// doBearer issues an HTTP request with an explicit Bearer token (testutil.Do
// mints its own token from a user ID, which is not what these tests need).
func doBearer(t *testing.T, srv *httptest.Server, method, path string, body any, token string) *http.Response {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, srv.URL+path, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	return resp
}
