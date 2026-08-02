// Package bootstrap creates the installation's first administrator from the
// environment, so that a freshly provisioned instance can be logged into at all.
//
// Every other route to a SUPER_ADMIN is closed by design: there is no public
// signup and no first-run setup flow, the user API refuses to assign
// SUPER_ADMIN (internal/usermgmt), and an accepted invitation always lands as
// USER (internal/auth). Before this package existed, a non-demo install came up
// with an empty users table and no way in — the operator had to boot once in
// demo mode (which also forces the dev JWT secret) or hand-write an INSERT
// against the database.
//
// The password never reaches this process in plaintext: the provisioner hashes
// it and passes only the bcrypt hash, so no cleartext user credential has to
// live in a deployment's environment or .env file.
package bootstrap

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/octbase/octbase-api/internal/rbac"
	"github.com/octbase/octbase-api/internal/shared"
)

// displayName is what the bootstrapped account is called in the UI until an
// operator renames it.
const displayName = "Administrator"

// Config is the deployment-level bootstrap input, read at the composition root.
// Both fields empty means "not configured" — the normal state of every
// installation that already has its admin.
type Config struct {
	// Email is the login of the account to create.
	Email string
	// PasswordHash is a bcrypt hash of its initial password, as produced by
	// `python3 -c 'import bcrypt; ...'`, htpasswd -B, or auth.HashPassword.
	PasswordHash string
}

// ConfigFromEnv reads the bootstrap configuration from the environment.
func ConfigFromEnv() Config {
	return Config{
		Email:        strings.TrimSpace(strings.ToLower(os.Getenv("OCTBASE_BOOTSTRAP_ADMIN_EMAIL"))),
		PasswordHash: strings.TrimSpace(os.Getenv("OCTBASE_BOOTSTRAP_ADMIN_PASSWORD_HASH")),
	}
}

// configured reports whether either field is set. A half-configured pair is
// still "configured" so that Run can reject it loudly rather than ignore it.
func (c Config) configured() bool {
	return c.Email != "" || c.PasswordHash != ""
}

// Run creates the first administrator if the installation has no users yet.
//
// It is a no-op — not an error — when the installation already has users or
// when nothing is configured, so it is safe to call on every startup: the
// bootstrap variables can stay in a deployment's .env forever without ever
// firing twice.
//
// The emptiness check deliberately comes BEFORE validation. An installation
// that already has users must never fail to start over a stale or malformed
// bootstrap variable in its .env — that would turn a leftover env key into an
// outage on the next restart. On a genuinely empty installation the opposite is
// true: a bad hash means nobody could ever log in, so it is a hard error and the
// caller is expected to refuse to start.
func Run(db *sql.DB, cfg Config) error {
	if !cfg.configured() {
		return nil
	}

	n, err := shared.CountUsers(db)
	if err != nil {
		return fmt.Errorf("bootstrap: count users: %w", err)
	}
	if n > 0 {
		slog.Debug("admin bootstrap skipped; installation already has users", "users", n)
		return nil
	}

	if err := cfg.validate(); err != nil {
		return err
	}

	id := shared.NewUUID()
	now := shared.Now()
	_, err = db.Exec(
		`INSERT INTO users (id,email,display_name,password_hash,global_role,status,is_active,is_admin,created_at,updated_at)
		 VALUES ($1,$2,$3,$4,$5,'active',true,true,$6,$6)`,
		id, cfg.Email, displayName, cfg.PasswordHash, rbac.GlobalSuperAdmin, now,
	)
	if err != nil {
		// Two API containers starting against one database can both see an empty
		// users table and both insert. The email is UNIQUE, so exactly one wins
		// and the loser must not take the deployment down with it.
		if shared.IsUniqueViolation(err) {
			slog.Info("admin bootstrap raced another starting instance; the other won", "email", cfg.Email)
			return nil
		}
		return fmt.Errorf("bootstrap: create admin: %w", err)
	}

	slog.Info("created the installation's first administrator", "email", cfg.Email, "role", rbac.GlobalSuperAdmin)
	return nil
}

// validate rejects input that would produce an account nobody can log into.
func (c Config) validate() error {
	if c.Email == "" {
		return errors.New("bootstrap: OCTBASE_BOOTSTRAP_ADMIN_EMAIL is required when OCTBASE_BOOTSTRAP_ADMIN_PASSWORD_HASH is set")
	}
	if c.PasswordHash == "" {
		return errors.New("bootstrap: OCTBASE_BOOTSTRAP_ADMIN_PASSWORD_HASH is required when OCTBASE_BOOTSTRAP_ADMIN_EMAIL is set")
	}
	if !strings.Contains(c.Email, "@") {
		return fmt.Errorf("bootstrap: OCTBASE_BOOTSTRAP_ADMIN_EMAIL is not an email address: %q", c.Email)
	}
	// A plaintext password here would be stored verbatim as the hash, and no
	// login would ever match it. Catching the shape is what separates "you
	// passed the wrong thing" from a silently unusable account. The cost and
	// salt are left to bcrypt itself to reject at verification time.
	if !strings.HasPrefix(c.PasswordHash, "$2") {
		return errors.New("bootstrap: OCTBASE_BOOTSTRAP_ADMIN_PASSWORD_HASH must be a bcrypt hash ($2a$/$2b$/$2y$...), not a plaintext password")
	}
	return nil
}
