package auth

import (
	"database/sql"
	"fmt"
	"time"
)

// RefreshTokenRepo handles persistence of refresh tokens.
type RefreshTokenRepo struct{ db *sql.DB }

// NewRefreshTokenRepo creates a new RefreshTokenRepo.
func NewRefreshTokenRepo(db *sql.DB) *RefreshTokenRepo { return &RefreshTokenRepo{db: db} }

// Store saves a refresh token.
//
// It deliberately does no opportunistic purge of expired rows. This ran on every
// login, refresh, MFA verification and password change — roughly four times an
// hour per active client given the 15-minute access TTL — as an unindexed
// `DELETE ... WHERE expires_at <= now()`, i.e. a sequential scan plus row locks
// on a hot table. It was also redundant: internal/retention purges expired
// refresh tokens at startup and daily thereafter (retention.Purge), which is
// where table-bounding belongs.
func (r *RefreshTokenRepo) Store(id, userID, tokenHash string, expiresAt time.Time) error {
	_, err := r.db.Exec(
		`INSERT INTO refresh_tokens (id, user_id, token_hash, expires_at) VALUES ($1,$2,$3,$4)`,
		id, userID, tokenHash, expiresAt,
	)
	if err != nil {
		return fmt.Errorf("store refresh token: %w", err)
	}
	return nil
}

// FindByHash looks up a valid (non-expired) refresh token by hash.
func (r *RefreshTokenRepo) FindByHash(hash string) (id, userID string, err error) {
	err = r.db.QueryRow(
		`SELECT id, user_id FROM refresh_tokens WHERE token_hash = $1 AND expires_at > now()`,
		hash,
	).Scan(&id, &userID)
	if err != nil {
		return "", "", err
	}
	return id, userID, nil
}

// Claim looks up a non-expired refresh token by hash and reports whether it has
// already been rotated. A rotated token presented again means the cookie was
// captured and replayed after the legitimate client rotated it — the caller
// should treat this as theft and revoke the whole session family.
func (r *RefreshTokenRepo) Claim(hash string) (id, userID string, rotated bool, err error) {
	var rotatedAt sql.NullTime
	err = r.db.QueryRow(
		`SELECT id, user_id, rotated_at FROM refresh_tokens WHERE token_hash = $1 AND expires_at > now()`,
		hash,
	).Scan(&id, &userID, &rotatedAt)
	if err != nil {
		return "", "", false, err
	}
	return id, userID, rotatedAt.Valid, nil
}

// Rotate atomically marks a refresh token as rotated without removing it (so a
// later replay is detectable as reuse — see Claim) and reports whether it was
// the caller that performed the rotation. The `rotated_at IS NULL` guard closes
// the read-then-write race in the Claim→Rotate sequence: if two requests present
// the same still-unrotated token concurrently, exactly one sees rowsAffected 1;
// the loser gets false and the caller treats it as reuse.
func (r *RefreshTokenRepo) Rotate(id string) (bool, error) {
	res, err := r.db.Exec(`UPDATE refresh_tokens SET rotated_at = now() WHERE id = $1 AND rotated_at IS NULL`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// Delete removes a refresh token by ID.
func (r *RefreshTokenRepo) Delete(id string) error {
	_, err := r.db.Exec(`DELETE FROM refresh_tokens WHERE id = $1`, id)
	return err
}

// DeleteByUser removes all refresh tokens for a user.
func (r *RefreshTokenRepo) DeleteByUser(userID string) error {
	_, err := r.db.Exec(`DELETE FROM refresh_tokens WHERE user_id = $1`, userID)
	return err
}

// InvitationRepo handles invitation persistence.
type InvitationRepo struct{ db *sql.DB }

// NewInvitationRepo creates a new InvitationRepo.
func NewInvitationRepo(db *sql.DB) *InvitationRepo { return &InvitationRepo{db: db} }

// Invitation represents a pending user invitation.
type Invitation struct {
	ID         string
	Email      string
	ProjectID  *string
	Role       string
	TokenHash  string
	InvitedBy  string
	ExpiresAt  time.Time
	AcceptedAt *time.Time
}

// Store saves an invitation.
func (r *InvitationRepo) Store(inv *Invitation) error {
	_, err := r.db.Exec(
		`INSERT INTO invitations (id, email, project_id, role, token_hash, invited_by, expires_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		inv.ID, inv.Email, inv.ProjectID, inv.Role, inv.TokenHash, inv.InvitedBy, inv.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("store invitation: %w", err)
	}
	return nil
}

// FindByTokenHash retrieves an invitation by its token hash.
func (r *InvitationRepo) FindByTokenHash(hash string) (*Invitation, error) {
	inv := &Invitation{}
	err := r.db.QueryRow(
		`SELECT id, email, project_id, role, token_hash, invited_by, expires_at, accepted_at
		   FROM invitations WHERE token_hash = $1`, hash,
	).Scan(&inv.ID, &inv.Email, &inv.ProjectID, &inv.Role, &inv.TokenHash,
		&inv.InvitedBy, &inv.ExpiresAt, &inv.AcceptedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find invitation: %w", err)
	}
	return inv, nil
}

// Accept marks the invitation as accepted.
func (r *InvitationRepo) Accept(id string) error {
	_, err := r.db.Exec(
		`UPDATE invitations SET accepted_at = now() WHERE id = $1`, id,
	)
	return err
}
