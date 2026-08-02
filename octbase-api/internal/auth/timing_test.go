package auth_test

import (
	"context"
	"testing"
	"time"

	internalauth "github.com/octbase/octbase-api/internal/auth"
	"github.com/octbase/octbase-api/internal/shared"
	"github.com/octbase/octbase-api/internal/testutil"
)

// TestLogin_TimingEqualized asserts that every failing login branch — unknown
// email, disabled account, deleted account, missing password hash — performs
// the same bcrypt work as a wrong-password attempt (auth.equalizeLoginTiming),
// so none of them is distinguishable by response time (enumeration oracle).
// Bcrypt at cost 12 takes hundreds of milliseconds; a branch that skips the
// compare returns in well under a millisecond, so requiring half the
// wrong-password reference duration separates the two cleanly without being
// flaky.
func TestLogin_TimingEqualized(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	provider := internalauth.NewEmailProvider(db, "test-secret")
	ctx := context.Background()

	insertUserWithPassword(t, db, shared.NewUUID(), "timing-active@test.dev", "Active", "Str0ngPass123", true)
	insertUserWithPassword(t, db, shared.NewUUID(), "timing-disabled@test.dev", "Disabled", "Str0ngPass123", false)
	deletedID := shared.NewUUID()
	insertUserWithPassword(t, db, deletedID, "timing-deleted@test.dev", "Deleted", "Str0ngPass123", true)
	if _, err := db.Exec(`UPDATE users SET status='deleted' WHERE id=$1`, deletedID); err != nil {
		t.Fatalf("mark deleted: %v", err)
	}
	now := shared.Now()
	if _, err := db.Exec(
		`INSERT INTO users (id, email, display_name, password_hash, is_active, created_at, updated_at)
		 VALUES ($1,$2,$3,NULL,true,$4,$5)`,
		shared.NewUUID(), "timing-nohash@test.dev", "No Hash", now, now,
	); err != nil {
		t.Fatalf("insert user without hash: %v", err)
	}

	timeLogin := func(email string) time.Duration {
		start := time.Now()
		_, err := provider.Login(ctx, email, "definitely-wrong")
		if err == nil {
			t.Fatalf("Login(%q) succeeded, want failure", email)
		}
		return time.Since(start)
	}

	// Warm-up plus reference: a wrong password against a real hash is the
	// timing every other failure branch must match.
	timeLogin("timing-active@test.dev")
	ref := timeLogin("timing-active@test.dev")

	for _, email := range []string{
		"timing-unknown@test.dev",
		"timing-disabled@test.dev",
		"timing-deleted@test.dev",
		"timing-nohash@test.dev",
	} {
		if d := timeLogin(email); d < ref/2 {
			t.Errorf("Login(%q) returned in %v, want >= %v (half the wrong-password reference %v) — a failure branch is skipping the bcrypt timing equalisation", email, d, ref/2, ref)
		}
	}
}
