package auth_test

import (
	"database/sql"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/octbase/octbase-api/internal/shared"
	"github.com/octbase/octbase-api/internal/testutil"
)

// enableMFAForUser enrolls and confirms MFA for userID directly against the
// DB (bypassing the HTTP enrollment flow, which internal/security/mfa's own
// tests already cover) and returns the raw TOTP secret.
func enableMFAForUser(t *testing.T, db *sql.DB, userID string) string {
	t.Helper()
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "Octbase", AccountName: userID})
	if err != nil {
		t.Fatalf("generate totp key: %v", err)
	}
	encrypted, err := shared.EncryptMFASecret(key.Secret())
	if err != nil {
		t.Fatalf("encrypt secret: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO mfa_credentials (user_id, encrypted_secret, confirmed_at) VALUES ($1,$2,now())`,
		userID, encrypted,
	); err != nil {
		t.Fatalf("insert mfa credential: %v", err)
	}
	if _, err := db.Exec(`UPDATE users SET mfa_enabled = true WHERE id = $1`, userID); err != nil {
		t.Fatalf("enable mfa: %v", err)
	}
	return key.Secret()
}

func TestLogin_MFAEnabled_ReturnsChallengeNotTokens(t *testing.T) {
	t.Setenv("OCTBASE_MFA_ENC_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	userID := shared.NewUUID()
	insertUserWithPassword(t, db, userID, "mfa-login@test.dev", "MFA User", "Str0ngPass123", true)
	enableMFAForUser(t, db, userID)

	srv := testutil.NewTestServer(t, db)
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": "mfa-login@test.dev", "password": "Str0ngPass123"}, "")
	testutil.AssertStatus(t, resp, http.StatusOK)

	var result map[string]interface{}
	testutil.DecodeJSON(t, resp, &result)
	if result["accessToken"] != nil {
		t.Fatal("expected no accessToken on first-factor login for an MFA-enabled account")
	}
	if result["mfaRequired"] != true {
		t.Errorf("expected mfaRequired=true, got %v", result["mfaRequired"])
	}
	if result["challengeToken"] == nil || result["challengeToken"] == "" {
		t.Fatal("expected a non-empty challengeToken")
	}
}

func TestVerifyMFA_ValidCode_IssuesTokens(t *testing.T) {
	t.Setenv("OCTBASE_MFA_ENC_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	userID := shared.NewUUID()
	insertUserWithPassword(t, db, userID, "mfa-verify@test.dev", "MFA User", "Str0ngPass123", true)
	secret := enableMFAForUser(t, db, userID)

	srv := testutil.NewTestServer(t, db)
	loginResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": "mfa-verify@test.dev", "password": "Str0ngPass123"}, "")
	testutil.AssertStatus(t, loginResp, http.StatusOK)
	var loginResult map[string]interface{}
	testutil.DecodeJSON(t, loginResp, &loginResult)
	challengeToken := loginResult["challengeToken"].(string)

	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}

	verifyResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/mfa/verify",
		map[string]string{"challengeToken": challengeToken, "code": code}, "")
	testutil.AssertStatus(t, verifyResp, http.StatusOK)
	var verifyResult map[string]interface{}
	testutil.DecodeJSON(t, verifyResp, &verifyResult)
	if verifyResult["accessToken"] == nil || verifyResult["accessToken"] == "" {
		t.Fatal("expected an accessToken from a valid MFA verification")
	}
}

func TestVerifyMFA_WrongCode_Rejected(t *testing.T) {
	t.Setenv("OCTBASE_MFA_ENC_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	userID := shared.NewUUID()
	insertUserWithPassword(t, db, userID, "mfa-wrong@test.dev", "MFA User", "Str0ngPass123", true)
	enableMFAForUser(t, db, userID)

	srv := testutil.NewTestServer(t, db)
	loginResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": "mfa-wrong@test.dev", "password": "Str0ngPass123"}, "")
	testutil.AssertStatus(t, loginResp, http.StatusOK)
	var loginResult map[string]interface{}
	testutil.DecodeJSON(t, loginResp, &loginResult)
	challengeToken := loginResult["challengeToken"].(string)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/mfa/verify",
		map[string]string{"challengeToken": challengeToken, "code": "000000"}, "")
	testutil.AssertStatus(t, resp, http.StatusUnauthorized)
}

// TestChallengeToken_CannotAuthenticateAsAccessToken is the key security
// property of the stateless design: a challenge token must not work as a
// bearer token anywhere else, even though it's signed with the same secret.
func TestChallengeToken_CannotAuthenticateAsAccessToken(t *testing.T) {
	t.Setenv("OCTBASE_MFA_ENC_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	userID := shared.NewUUID()
	insertUserWithPassword(t, db, userID, "mfa-scope@test.dev", "MFA User", "Str0ngPass123", true)
	enableMFAForUser(t, db, userID)

	srv := testutil.NewTestServer(t, db)
	loginResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": "mfa-scope@test.dev", "password": "Str0ngPass123"}, "")
	testutil.AssertStatus(t, loginResp, http.StatusOK)
	var loginResult map[string]interface{}
	testutil.DecodeJSON(t, loginResp, &loginResult)
	challengeToken := loginResult["challengeToken"].(string)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+challengeToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 using a challenge token as a bearer token, got %d", resp.StatusCode)
	}
}

// TestAccessToken_CannotBeUsedAsMFAChallenge is the mirror-image property: a
// normal access token must not satisfy the MFA verify endpoint either.
func TestAccessToken_CannotBeUsedAsMFAChallenge(t *testing.T) {
	t.Setenv("OCTBASE_MFA_ENC_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)

	accessToken := testutil.TokenForUser(testutil.DemoUserID)
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/mfa/verify",
		map[string]string{"challengeToken": accessToken, "code": "000000"}, "")
	testutil.AssertStatus(t, resp, http.StatusUnauthorized)

	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "MFA_CHALLENGE_INVALID" {
		t.Errorf("expected MFA_CHALLENGE_INVALID, got %v", e["code"])
	}
}

// loginForChallenge performs the first factor and returns the challenge token.
func loginForChallenge(t *testing.T, srv *httptest.Server, email, password string) string {
	t.Helper()
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": email, "password": password}, "")
	testutil.AssertStatus(t, resp, http.StatusOK)
	var result map[string]interface{}
	testutil.DecodeJSON(t, resp, &result)
	token, _ := result["challengeToken"].(string)
	if token == "" {
		t.Fatal("expected a challengeToken")
	}
	return token
}

// TestVerifyMFA_LegacyInactiveAccount_Rejected: an account deactivated via the
// legacy is_active flag (status left 'active') must not be able to complete a
// login at /auth/mfa/verify — the same gate email_provider.Login enforces.
func TestVerifyMFA_LegacyInactiveAccount_Rejected(t *testing.T) {
	t.Setenv("OCTBASE_MFA_ENC_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	userID := shared.NewUUID()
	insertUserWithPassword(t, db, userID, "mfa-inactive@test.dev", "MFA User", "Str0ngPass123", true)
	secret := enableMFAForUser(t, db, userID)

	srv := testutil.NewTestServer(t, db)
	challengeToken := loginForChallenge(t, srv, "mfa-inactive@test.dev", "Str0ngPass123")

	// Deactivate between the two factors, via the legacy column only.
	if _, err := db.Exec(`UPDATE users SET is_active = false WHERE id = $1`, userID); err != nil {
		t.Fatalf("deactivate user: %v", err)
	}

	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/mfa/verify",
		map[string]string{"challengeToken": challengeToken, "code": code}, "")
	testutil.AssertStatus(t, resp, http.StatusUnauthorized)
}

// TestVerifyMFA_PerAccountAttemptCap: code guessing against one account is
// cut off with 429 RATE_LIMITED after mfaVerifyMaxAttempts, independent of
// the per-IP limiter (which the test router doesn't even mount).
func TestVerifyMFA_PerAccountAttemptCap(t *testing.T) {
	t.Setenv("OCTBASE_MFA_ENC_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	userID := shared.NewUUID()
	insertUserWithPassword(t, db, userID, "mfa-cap@test.dev", "MFA User", "Str0ngPass123", true)
	enableMFAForUser(t, db, userID)

	srv := testutil.NewTestServer(t, db)
	challengeToken := loginForChallenge(t, srv, "mfa-cap@test.dev", "Str0ngPass123")

	for i := 0; i < 10; i++ {
		resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/mfa/verify",
			map[string]string{"challengeToken": challengeToken, "code": "000000"}, "")
		testutil.AssertStatus(t, resp, http.StatusUnauthorized)
	}
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/mfa/verify",
		map[string]string{"challengeToken": challengeToken, "code": "000000"}, "")
	testutil.AssertStatus(t, resp, http.StatusTooManyRequests)

	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "RATE_LIMITED" {
		t.Errorf("expected RATE_LIMITED, got %v", e["code"])
	}
}

// TestVerifyMFA_DecryptFailure_IsServerError: a broken/rotated MFA encryption
// key must surface as a 500, not masquerade as a wrong code (which would
// silently lock out every MFA user with MFA_CODE_INVALID).
func TestVerifyMFA_DecryptFailure_IsServerError(t *testing.T) {
	t.Setenv("OCTBASE_MFA_ENC_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	userID := shared.NewUUID()
	insertUserWithPassword(t, db, userID, "mfa-keyrot@test.dev", "MFA User", "Str0ngPass123", true)
	secret := enableMFAForUser(t, db, userID)

	srv := testutil.NewTestServer(t, db)
	challengeToken := loginForChallenge(t, srv, "mfa-keyrot@test.dev", "Str0ngPass123")

	// Simulate a key rotation after enrollment: the stored ciphertext can no
	// longer be decrypted.
	rotated := make([]byte, 32)
	rotated[0] = 1
	t.Setenv("OCTBASE_MFA_ENC_KEY", base64.StdEncoding.EncodeToString(rotated))

	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/auth/mfa/verify",
		map[string]string{"challengeToken": challengeToken, "code": code}, "")
	testutil.AssertStatus(t, resp, http.StatusInternalServerError)
}
