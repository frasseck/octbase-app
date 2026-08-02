// Package mfa implements TOTP-based multi-factor authentication: enrollment,
// verification, disable, and one-time recovery codes. It lives under
// internal/security (not internal/auth or internal/identityaccess) so this
// auth/crypto surface has its own bounded context, separate from the
// low-risk, no-crypto internal/dashboard preferences module — see
// docs/architecture.md and prompts/33_octbase-user-settings-mfa.md.
package mfa

import "time"

// RecoveryCodeCount is how many one-time recovery codes are issued whenever
// MFA is confirmed or codes are regenerated.
const RecoveryCodeCount = 10

// Credential is a user's TOTP secret. EncryptedSecret is AES-256-GCM
// ciphertext (shared.EncryptMFASecret) — the plaintext secret is only ever
// held in memory during enrollment/verification, never persisted.
type Credential struct {
	UserID          string
	EncryptedSecret string
	ConfirmedAt     *time.Time
	// LastTOTPStep is the most recently accepted TOTP time-step (unixtime/period);
	// used to reject replay of a code within its acceptance window.
	LastTOTPStep int64
}

// Pending reports whether enrollment has been started but not yet confirmed
// with a valid code.
func (c *Credential) Pending() bool {
	return c != nil && c.ConfirmedAt == nil
}

// RecoveryCode is one hashed, one-time-use backup code.
type RecoveryCode struct {
	ID       string
	UserID   string
	CodeHash string
	UsedAt   *time.Time
}
