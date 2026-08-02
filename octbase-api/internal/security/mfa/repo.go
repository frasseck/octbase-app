package mfa

import (
	"database/sql"
	"errors"
)

// Repo handles MFA credential and recovery-code persistence.
type Repo struct{ db *sql.DB }

// NewRepo creates a new mfa Repo.
func NewRepo(db *sql.DB) *Repo { return &Repo{db: db} }

// IsEnabled reports whether a user has MFA active (users.mfa_enabled).
func (r *Repo) IsEnabled(userID string) (bool, error) {
	var enabled bool
	err := r.db.QueryRow(`SELECT mfa_enabled FROM users WHERE id = $1`, userID).Scan(&enabled)
	return enabled, err
}

// GetCredential returns a user's credential row, or nil if none exists.
func (r *Repo) GetCredential(userID string) (*Credential, error) {
	c := &Credential{UserID: userID}
	err := r.db.QueryRow(
		`SELECT encrypted_secret, confirmed_at, last_totp_step FROM mfa_credentials WHERE user_id = $1`, userID,
	).Scan(&c.EncryptedSecret, &c.ConfirmedAt, &c.LastTOTPStep)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return c, nil
}

// MarkTOTPStep records the time-step of a just-accepted TOTP code and reports
// whether it advanced the stored step. The guarded WHERE (last_totp_step < step)
// makes it both a replay check and race-safe: a replay of the same (or an
// earlier in-window) step, including two concurrent verifications of one code,
// affects zero rows and returns false. Only the first acceptance advances.
func (r *Repo) MarkTOTPStep(userID string, step int64) (bool, error) {
	res, err := r.db.Exec(
		`UPDATE mfa_credentials SET last_totp_step = $2 WHERE user_id = $1 AND last_totp_step < $2`,
		userID, step,
	)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// UpsertPendingCredential (re)starts enrollment: stores an unconfirmed secret,
// overwriting any earlier pending (unconfirmed) attempt.
func (r *Repo) UpsertPendingCredential(userID, encryptedSecret string) error {
	_, err := r.db.Exec(`
		INSERT INTO mfa_credentials (user_id, encrypted_secret, confirmed_at) VALUES ($1,$2,NULL)
		ON CONFLICT (user_id) DO UPDATE SET encrypted_secret = EXCLUDED.encrypted_secret, confirmed_at = NULL`,
		userID, encryptedSecret,
	)
	return err
}

// ConfirmCredential marks a pending credential confirmed and flips
// users.mfa_enabled on, atomically.
func (r *Repo) ConfirmCredential(tx *sql.Tx, userID string) error {
	if _, err := tx.Exec(`UPDATE mfa_credentials SET confirmed_at = now() WHERE user_id = $1`, userID); err != nil {
		return err
	}
	_, err := tx.Exec(`UPDATE users SET mfa_enabled = true WHERE id = $1`, userID)
	return err
}

// Disable removes a user's credential and recovery codes and flips
// users.mfa_enabled off, atomically.
func (r *Repo) Disable(tx *sql.Tx, userID string) error {
	if _, err := tx.Exec(`DELETE FROM mfa_recovery_codes WHERE user_id = $1`, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM mfa_credentials WHERE user_id = $1`, userID); err != nil {
		return err
	}
	_, err := tx.Exec(`UPDATE users SET mfa_enabled = false WHERE id = $1`, userID)
	return err
}

// ReplaceRecoveryCodes deletes any existing codes and stores a fresh set of
// hashes, atomically.
func (r *Repo) ReplaceRecoveryCodes(tx *sql.Tx, userID string, ids, hashes []string) error {
	if _, err := tx.Exec(`DELETE FROM mfa_recovery_codes WHERE user_id = $1`, userID); err != nil {
		return err
	}
	for i := range ids {
		if _, err := tx.Exec(
			`INSERT INTO mfa_recovery_codes (id, user_id, code_hash) VALUES ($1,$2,$3)`,
			ids[i], userID, hashes[i],
		); err != nil {
			return err
		}
	}
	return nil
}

// ConsumeRecoveryCode marks the unused code matching hash as used and
// reports whether a match was found. Returns false, nil if no unused code
// matches (invalid or already-used code). The used_at IS NULL guard must live
// in the UPDATE's own WHERE clause (not a subquery): under READ COMMITTED a
// subquery is not re-evaluated when the row was concurrently updated, which
// would let two racing requests consume the same one-time code.
func (r *Repo) ConsumeRecoveryCode(userID, hash string) (bool, error) {
	res, err := r.db.Exec(
		`UPDATE mfa_recovery_codes SET used_at = now()
		  WHERE user_id = $1 AND code_hash = $2 AND used_at IS NULL`,
		userID, hash,
	)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
