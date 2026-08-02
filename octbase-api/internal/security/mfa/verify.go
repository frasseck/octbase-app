package mfa

import (
	"crypto/subtle"
	"fmt"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/octbase/octbase-api/internal/shared"
)

// totpPeriodSeconds and totpSkewSteps mirror the library defaults used by
// totp.Validate (period 30s, ±1 step). Kept explicit so the replay window here
// stays in lockstep with acceptance.
const (
	totpPeriodSeconds = 30
	totpSkewSteps     = 1
)

// matchTOTPStep returns the time-step (unixtime/period) at which code is a valid
// TOTP for secret within the accepted ±skew window, or 0 if it matches none. A
// real step is always a large positive number, so 0 unambiguously means "no
// match" and never collides with a stored last_totp_step.
func matchTOTPStep(code, secret string, now time.Time) int64 {
	for skew := int64(-totpSkewSteps); skew <= totpSkewSteps; skew++ {
		t := now.Add(time.Duration(skew*totpPeriodSeconds) * time.Second)
		gen, err := totp.GenerateCode(secret, t)
		if err == nil && subtle.ConstantTimeCompare([]byte(gen), []byte(code)) == 1 {
			return t.Unix() / totpPeriodSeconds
		}
	}
	return 0
}

// ValidateCode checks code against a user's confirmed TOTP secret, falling
// back to consuming a recovery code if it doesn't match. It is exported so
// internal/auth's login-challenge verification (POST /auth/mfa/verify) can
// check a second factor without duplicating the TOTP/recovery-code logic —
// auth imports mfa for this one call; mfa never imports auth (see
// docs/architecture.md's note on the two packages' dependency direction).
func ValidateCode(repo *Repo, userID, code string) (bool, error) {
	if code == "" {
		return false, nil
	}
	cred, err := repo.GetCredential(userID)
	if err != nil {
		return false, err
	}
	if cred != nil && !cred.Pending() {
		secret, err := shared.DecryptMFASecret(cred.EncryptedSecret)
		if err != nil {
			// A decrypt failure is a server-side problem (rotated or missing
			// OCTBASE_MFA_ENC_KEY, corrupted ciphertext), not a wrong code.
			// Surface it so it becomes a logged 500 instead of silently
			// falling through to the recovery-code path and locking every
			// MFA-enabled user out with a misleading MFA_CODE_INVALID.
			return false, fmt.Errorf("decrypt totp secret: %w", err)
		}
		// Accept a valid TOTP only once per time-step: MarkTOTPStep advances the
		// stored step and returns false if this code (or an earlier in-window
		// step) was already consumed, defeating replay within the ~90s window.
		if step := matchTOTPStep(code, secret, time.Now()); step > 0 {
			advanced, err := repo.MarkTOTPStep(userID, step)
			if err != nil {
				return false, err
			}
			// A currently-valid but already-used code is a replay, not a recovery
			// code — return without falling through to the recovery-code path.
			return advanced, nil
		}
	}
	return repo.ConsumeRecoveryCode(userID, hashRecoveryCode(code))
}
