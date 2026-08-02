package bootstrap_test

import (
	"database/sql"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/octbase/octbase-api/internal/bootstrap"
	"github.com/octbase/octbase-api/internal/rbac"
	"github.com/octbase/octbase-api/internal/shared"
	"github.com/octbase/octbase-api/internal/testutil"
)

const testPassword = "bootstrap-test-password-1"

// emptyDB is testutil.NewTestDB minus its fixture accounts. Everything this
// package does turns on the installation being empty — which the standard test
// DB, with its five seeded users, never is.
func emptyDB(t *testing.T) *sql.DB {
	t.Helper()
	db := testutil.NewTestDB(t)
	if _, err := db.Exec(`TRUNCATE users CASCADE`); err != nil {
		t.Fatalf("truncate users: %v", err)
	}
	return db
}

func hashFor(t *testing.T, pw string) string {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	return string(h)
}

// The whole point of the package: an empty installation gets exactly one
// SUPER_ADMIN, and the password handed over as a hash actually logs in.
func TestRunCreatesLoginableSuperAdmin(t *testing.T) {
	db := emptyDB(t)
	hash := hashFor(t, testPassword)

	if err := bootstrap.Run(db, bootstrap.Config{Email: "admin@acme.example", PasswordHash: hash}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var role, status, stored string
	var isActive bool
	if err := db.QueryRow(
		`SELECT global_role, status, is_active, password_hash FROM users WHERE email = $1`,
		"admin@acme.example").Scan(&role, &status, &isActive, &stored); err != nil {
		t.Fatalf("query bootstrapped admin: %v", err)
	}
	if role != rbac.GlobalSuperAdmin {
		t.Errorf("global_role = %q; want %q", role, rbac.GlobalSuperAdmin)
	}
	if status != "active" || !isActive {
		t.Errorf("status = %q, is_active = %v; want active/true", status, isActive)
	}
	// A stored hash that does not verify is the failure mode that would strand
	// the operator outside a healthy-looking instance.
	if err := bcrypt.CompareHashAndPassword([]byte(stored), []byte(testPassword)); err != nil {
		t.Errorf("stored hash does not verify against the bootstrap password: %v", err)
	}
}

// Safe to call on every startup: the bootstrap variables may stay in a .env
// forever, so a second run must not add a second admin or error.
func TestRunIsNoOpWhenUsersExist(t *testing.T) {
	db := emptyDB(t)
	cfg := bootstrap.Config{Email: "admin@acme.example", PasswordHash: hashFor(t, testPassword)}

	if err := bootstrap.Run(db, cfg); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	// A different admin, as if the operator had rotated the ledger entry.
	second := bootstrap.Config{Email: "other@acme.example", PasswordHash: hashFor(t, testPassword)}
	if err := bootstrap.Run(db, second); err != nil {
		t.Fatalf("second Run: %v", err)
	}

	n, err := shared.CountUsers(db)
	if err != nil {
		t.Fatalf("CountUsers: %v", err)
	}
	if n != 1 {
		t.Errorf("user count = %d after two Runs; want 1", n)
	}
	var exists bool
	if err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM users WHERE email = $1)`, "other@acme.example").Scan(&exists); err != nil {
		t.Fatalf("query: %v", err)
	}
	if exists {
		t.Error("second Run created an account on a non-empty installation")
	}
}

// An installation that already has users must start even with a broken
// bootstrap variable left in its .env — the check order is what guarantees a
// stale env key can never become an outage on restart.
func TestRunIgnoresInvalidConfigWhenUsersExist(t *testing.T) {
	db := testutil.NewTestDB(t)

	if err := bootstrap.Run(db, bootstrap.Config{Email: "admin@acme.example", PasswordHash: hashFor(t, testPassword)}); err != nil {
		t.Fatalf("seed Run: %v", err)
	}
	// Plaintext where a hash belongs, and no email: fatal on an empty install.
	if err := bootstrap.Run(db, bootstrap.Config{PasswordHash: "hunter2"}); err != nil {
		t.Errorf("Run on a populated installation must ignore bad config, got: %v", err)
	}
}

func TestRunIsNoOpWhenUnconfigured(t *testing.T) {
	db := emptyDB(t)

	if err := bootstrap.Run(db, bootstrap.Config{}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	n, err := shared.CountUsers(db)
	if err != nil {
		t.Fatalf("CountUsers: %v", err)
	}
	if n != 0 {
		t.Errorf("user count = %d; want 0 — an unconfigured bootstrap must create nothing", n)
	}
}

// On an empty installation, bad input must fail loudly rather than produce an
// account nobody can log into.
func TestRunRejectsBadConfigOnEmptyInstallation(t *testing.T) {
	cases := []struct {
		name string
		cfg  bootstrap.Config
		want string
	}{
		{"plaintext instead of hash", bootstrap.Config{Email: "a@b.example", PasswordHash: "hunter2"}, "bcrypt hash"},
		{"hash without email", bootstrap.Config{PasswordHash: "$2b$12$abcdefghijklmnopqrstuv"}, "EMAIL is required"},
		{"email without hash", bootstrap.Config{Email: "a@b.example"}, "PASSWORD_HASH is required"},
		{"not an email", bootstrap.Config{Email: "admin", PasswordHash: "$2b$12$abcdefghijklmnopqrstuv"}, "not an email address"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := emptyDB(t)
			err := bootstrap.Run(db, tc.cfg)
			if err == nil {
				t.Fatalf("Run(%+v) = nil; want an error", tc.cfg)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q; want it to mention %q", err, tc.want)
			}
		})
	}
}
