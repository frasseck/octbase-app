package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// ErrInvalidCredentials is returned on wrong email or password. Deliberately
// vague to avoid oracle attacks.
var ErrInvalidCredentials = errors.New("invalid credentials")

// ErrUserInactive is returned when the user account is deactivated.
var ErrUserInactive = errors.New("account is deactivated")

const bcryptCost = 12

// dummyBcryptHash is a valid bcrypt hash (cost 12) of a fixed throwaway value.
// When login fails because the email is unknown or the account has no usable
// password hash, we still run a bcrypt comparison against this hash so the
// not-found path takes roughly the same time as the wrong-password path. Without
// it, response timing would reveal whether an email is registered (enumeration).
const dummyBcryptHash = "$2a$12$JsQFhIBiL3LOQyUFtsIn7OnpMExxpFWnivJu4BcBP58VgucnnZtFC"

// equalizeLoginTiming runs the same bcrypt work as the real password
// verification so every failing Login branch (unknown email, disabled or
// deleted account, no stored hash) takes roughly as long as a wrong-password
// attempt. This is the single home of the anti-enumeration compare: a change
// to the dummy hash, the cost, or the decision to run it cannot miss a branch
// and silently reopen the timing oracle.
func equalizeLoginTiming(password string) {
	_ = bcrypt.CompareHashAndPassword([]byte(dummyBcryptHash), []byte(password))
}

// EmailProvider implements Provider using bcrypt passwords stored in Postgres.
type EmailProvider struct {
	db     *sql.DB
	secret string
}

// NewEmailProvider creates a new EmailProvider.
func NewEmailProvider(db *sql.DB, jwtSecret string) *EmailProvider {
	return &EmailProvider{db: db, secret: jwtSecret}
}

// Login looks up the user by email, verifies the password, and checks the
// account is active. It does NOT bump last_login_at: a password match is only
// the first factor for MFA-enabled accounts, so the handler records the login
// timestamp once authentication has fully completed (see Handler.touchLastLogin).
func (p *EmailProvider) Login(_ context.Context, email, password string) (*User, error) {
	var u User
	var hash sql.NullString
	var status string
	err := p.db.QueryRow(
		`SELECT id, email, display_name, global_role, is_active, status, password_hash
		   FROM users WHERE email = $1`, email,
	).Scan(&u.ID, &u.Email, &u.Name, &u.GlobalRole, &u.IsActive, &status, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		// An attacker must not be able to distinguish a registered email from an
		// unknown one by response time.
		equalizeLoginTiming(password)
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, fmt.Errorf("query user: %w", err)
	}
	// Prefer the new status column; fall back to legacy is_active for safety.
	if status == "disabled" || status == "deleted" || !u.IsActive {
		// A disabled/deleted account must not be distinguishable from an active
		// one by response time (it otherwise returned ~100x faster).
		equalizeLoginTiming(password)
		return nil, ErrUserInactive
	}
	if !hash.Valid || hash.String == "" {
		equalizeLoginTiming(password)
		return nil, ErrInvalidCredentials
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash.String), []byte(password)); err != nil {
		return nil, ErrInvalidCredentials
	}
	return &u, nil
}

// ValidateToken parses the JWT and returns the user ID.
func (p *EmailProvider) ValidateToken(token string) (string, error) {
	return ParseAccessToken(token, p.secret)
}

// HashPassword hashes a plaintext password with bcrypt cost 12.
func HashPassword(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(h), nil
}

// VerifyPassword checks a plaintext password against a bcrypt hash.
func VerifyPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
