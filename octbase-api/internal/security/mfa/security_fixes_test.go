package mfa_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/octbase/octbase-api/internal/security/mfa"
	"github.com/octbase/octbase-api/internal/shared"
	"github.com/octbase/octbase-api/internal/testutil"
)

// M4 (2026-07-14 assessment): voluntary MFA enrollment via an access token must
// re-authenticate with the account password, so a stolen access token alone
// cannot bind MFA to an attacker's authenticator.
func TestEnroll_RequiresPasswordReauth(t *testing.T) {
	setMFAEncKey(t)
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)

	// No password → rejected.
	r := testutil.Do(t, srv, http.MethodPost, "/api/v1/users/me/mfa/enroll",
		map[string]string{}, testutil.DemoUserID)
	testutil.AssertStatus(t, r, http.StatusUnauthorized)
	_ = r.Body.Close()

	// Wrong password → rejected.
	r = testutil.Do(t, srv, http.MethodPost, "/api/v1/users/me/mfa/enroll",
		map[string]string{"password": "not-the-password"}, testutil.DemoUserID)
	testutil.AssertStatus(t, r, http.StatusUnauthorized)
	_ = r.Body.Close()

	// Correct password → allowed.
	r = testutil.Do(t, srv, http.MethodPost, "/api/v1/users/me/mfa/enroll",
		map[string]string{"password": testutil.TestUserPassword}, testutil.DemoUserID)
	testutil.AssertStatus(t, r, http.StatusOK)
	_ = r.Body.Close()
}

// L2 (2026-07-14 assessment): a TOTP code accepted once must not be replayable
// within its acceptance window.
func TestValidateCode_RejectsReplay(t *testing.T) {
	setMFAEncKey(t)
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	repo := mfa.NewRepo(db)

	key, err := totp.Generate(totp.GenerateOpts{Issuer: "Octbase", AccountName: "demo@octbase.dev"})
	if err != nil {
		t.Fatalf("generate secret: %v", err)
	}
	enc, err := shared.EncryptMFASecret(key.Secret())
	if err != nil {
		t.Fatalf("encrypt secret: %v", err)
	}
	if err := repo.UpsertPendingCredential(testutil.DemoUserID, enc); err != nil {
		t.Fatalf("upsert credential: %v", err)
	}
	if _, err := db.Exec(`UPDATE mfa_credentials SET confirmed_at = now() WHERE user_id = $1`, testutil.DemoUserID); err != nil {
		t.Fatalf("confirm credential: %v", err)
	}

	code, err := totp.GenerateCode(key.Secret(), time.Now())
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}

	ok, err := mfa.ValidateCode(repo, testutil.DemoUserID, code)
	if err != nil || !ok {
		t.Fatalf("first use of code should succeed: ok=%v err=%v", ok, err)
	}
	ok, err = mfa.ValidateCode(repo, testutil.DemoUserID, code)
	if err != nil {
		t.Fatalf("replay check errored: %v", err)
	}
	if ok {
		t.Fatal("replay of an already-used TOTP code was accepted")
	}
}
