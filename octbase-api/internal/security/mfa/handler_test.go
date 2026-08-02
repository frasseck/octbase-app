package mfa_test

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/octbase/octbase-api/internal/auth"
	"github.com/octbase/octbase-api/internal/testutil"
)

func setMFAEncKey(t *testing.T) {
	t.Helper()
	t.Setenv("OCTBASE_MFA_ENC_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
}

// mustEnrollSecret enrolls userID and returns the raw (base32) TOTP secret.
func mustEnrollSecret(t *testing.T, srv *httptest.Server, userID string) string {
	t.Helper()
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/users/me/mfa/enroll", map[string]string{"password": testutil.TestUserPassword}, userID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var body map[string]interface{}
	testutil.DecodeJSON(t, resp, &body)
	secret, _ := body["secret"].(string)
	if secret == "" {
		t.Fatal("expected a non-empty secret from enroll")
	}
	return secret
}

// mustEnrollAndConfirm enrolls and confirms userID, returning the confirmed secret.
func mustEnrollAndConfirm(t *testing.T, srv *httptest.Server, userID string) string {
	t.Helper()
	secret := mustEnrollSecret(t, srv, userID)
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/users/me/mfa/confirm",
		map[string]string{"code": code}, userID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	return secret
}

func TestEnroll_ReturnsOtpauthURLAndSecret(t *testing.T) {
	setMFAEncKey(t)
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/users/me/mfa/enroll", map[string]string{"password": testutil.TestUserPassword}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	var body map[string]interface{}
	testutil.DecodeJSON(t, resp, &body)
	if body["secret"] == nil || body["secret"] == "" {
		t.Fatal("expected a non-empty secret")
	}
	if body["otpauthUrl"] == nil {
		t.Fatal("expected an otpauthUrl")
	}
}

func TestConfirm_ValidCode_EnablesMFAAndReturnsRecoveryCodes(t *testing.T) {
	setMFAEncKey(t)
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)

	secret := mustEnrollSecret(t, srv, testutil.DemoUserID)
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/users/me/mfa/confirm",
		map[string]string{"code": code}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	var body map[string]interface{}
	testutil.DecodeJSON(t, resp, &body)
	codes, ok := body["recoveryCodes"].([]interface{})
	if !ok || len(codes) != 10 {
		t.Fatalf("expected 10 recovery codes, got %v", body["recoveryCodes"])
	}

	// Enrolling again is now rejected since MFA is active.
	resp2 := testutil.Do(t, srv, http.MethodPost, "/api/v1/users/me/mfa/enroll", map[string]string{"password": testutil.TestUserPassword}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp2, http.StatusConflict)
}

func TestConfirm_InvalidCode_Rejected(t *testing.T) {
	setMFAEncKey(t)
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)

	mustEnrollSecret(t, srv, testutil.DemoUserID)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/users/me/mfa/confirm",
		map[string]string{"code": "000000"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)

	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "MFA_CODE_INVALID" {
		t.Errorf("expected MFA_CODE_INVALID, got %v", e["code"])
	}
}

func TestConfirm_NoPendingEnrollment_Rejected(t *testing.T) {
	setMFAEncKey(t)
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/users/me/mfa/confirm",
		map[string]string{"code": "123456"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)

	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "MFA_ENROLLMENT_NOT_FOUND" {
		t.Errorf("expected MFA_ENROLLMENT_NOT_FOUND, got %v", e["code"])
	}
}

func TestDisable_NotEnabled_Rejected(t *testing.T) {
	setMFAEncKey(t)
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/users/me/mfa/disable",
		map[string]string{"code": "123456"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)

	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "MFA_NOT_ENABLED" {
		t.Errorf("expected MFA_NOT_ENABLED, got %v", e["code"])
	}
}

func TestDisable_WrongReauth_Rejected(t *testing.T) {
	setMFAEncKey(t)
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	mustEnrollAndConfirm(t, srv, testutil.DemoUserID)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/users/me/mfa/disable",
		map[string]string{"code": "000000"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
}

func TestDisable_WithValidTOTPCode_Succeeds(t *testing.T) {
	setMFAEncKey(t)
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	secret := mustEnrollAndConfirm(t, srv, testutil.DemoUserID)

	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/users/me/mfa/disable",
		map[string]string{"code": code}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNoContent)

	// Now that MFA is off, enroll should be allowed again (not 409).
	resp2 := testutil.Do(t, srv, http.MethodPost, "/api/v1/users/me/mfa/enroll", map[string]string{"password": testutil.TestUserPassword}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp2, http.StatusOK)
}

func TestDisable_WithPassword_Succeeds(t *testing.T) {
	setMFAEncKey(t)
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)

	// Enroll first (uses the seeded password for re-auth), then switch to a custom
	// password to exercise the disable-with-password path against a known value.
	mustEnrollAndConfirm(t, srv, testutil.DemoUserID)

	hash, err := auth.HashPassword("correct-horse-battery")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if _, err := db.Exec(`UPDATE users SET password_hash = $1 WHERE id = $2`, hash, testutil.DemoUserID); err != nil {
		t.Fatalf("set password: %v", err)
	}

	// Wrong password is rejected.
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/users/me/mfa/disable",
		map[string]string{"password": "wrong"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)

	// Correct password succeeds.
	resp2 := testutil.Do(t, srv, http.MethodPost, "/api/v1/users/me/mfa/disable",
		map[string]string{"password": "correct-horse-battery"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp2, http.StatusNoContent)
}

func TestRegenerateRecoveryCodes_NotEnabled_Rejected(t *testing.T) {
	setMFAEncKey(t)
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/users/me/mfa/recovery-codes/regenerate",
		map[string]string{"code": "123456"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)

	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "MFA_NOT_ENABLED" {
		t.Errorf("expected MFA_NOT_ENABLED, got %v", e["code"])
	}
}

func TestRegenerateRecoveryCodes_WrongReauth_Rejected(t *testing.T) {
	setMFAEncKey(t)
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	mustEnrollAndConfirm(t, srv, testutil.DemoUserID)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/users/me/mfa/recovery-codes/regenerate",
		map[string]string{"code": "000000"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
}

// TestDisable_RecoveryCodeInputIsNormalized: recovery codes are typed by hand
// in exactly the scenario where the authenticator is gone, so lowercase or
// hyphen-less entry of a valid code must still match.
func TestDisable_RecoveryCodeInputIsNormalized(t *testing.T) {
	setMFAEncKey(t)
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)

	secret := mustEnrollSecret(t, srv, testutil.DemoUserID)
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/users/me/mfa/confirm",
		map[string]string{"code": code}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var confirmBody map[string]interface{}
	testutil.DecodeJSON(t, resp, &confirmBody)
	recoveryCode := confirmBody["recoveryCodes"].([]interface{})[0].(string)

	// Lowercase, hyphen stripped, surrounded by spaces — still one valid use.
	mangled := " " + strings.ToLower(strings.ReplaceAll(recoveryCode, "-", "")) + " "
	resp2 := testutil.Do(t, srv, http.MethodPost, "/api/v1/users/me/mfa/disable",
		map[string]string{"code": mangled}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp2, http.StatusNoContent)
}

func TestRegenerateRecoveryCodes_OldCodesStopWorking(t *testing.T) {
	setMFAEncKey(t)
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	secret := mustEnrollSecret(t, srv, testutil.DemoUserID)
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/users/me/mfa/confirm",
		map[string]string{"code": code}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var confirmBody map[string]interface{}
	testutil.DecodeJSON(t, resp, &confirmBody)
	oldCodes := confirmBody["recoveryCodes"].([]interface{})
	oldCode := oldCodes[0].(string)

	// Regenerate using a fresh TOTP code (re-auth requirement).
	code2, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}
	resp2 := testutil.Do(t, srv, http.MethodPost, "/api/v1/users/me/mfa/recovery-codes/regenerate",
		map[string]string{"code": code2}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp2, http.StatusOK)
	var regenBody map[string]interface{}
	testutil.DecodeJSON(t, resp2, &regenBody)
	newCodes := regenBody["recoveryCodes"].([]interface{})
	if len(newCodes) != 10 {
		t.Fatalf("expected 10 new recovery codes, got %d", len(newCodes))
	}

	// The old recovery code must no longer disable MFA.
	resp3 := testutil.Do(t, srv, http.MethodPost, "/api/v1/users/me/mfa/disable",
		map[string]string{"code": oldCode}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp3, http.StatusUnprocessableEntity)

	// A new recovery code does work.
	newCode := newCodes[0].(string)
	resp4 := testutil.Do(t, srv, http.MethodPost, "/api/v1/users/me/mfa/disable",
		map[string]string{"code": newCode}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp4, http.StatusNoContent)
}
