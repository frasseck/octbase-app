package seed

import (
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/octbase/octbase-api/internal/shared"
)

// The seeded demo accounts held passwords the application would refuse to set:
// "demo1234" and "super1234" are 8 characters against a 12-character policy.
// Nothing caught it because ValidatePassword is enforced on change-password and
// reset-password but never retroactively at login, so the short passwords kept
// working and the contradiction stayed invisible.
//
// It was not cosmetic. It made the change-password success path untestable in
// the browser suite: a test that changed the demo password could not change it
// back — the restore is refused 422 by the policy — so the demo login was dead
// for every remaining test in the run, which presents as 300-plus opaque setup
// errors rather than one clear failure. It stranded a full suite run on
// 2026-07-31 and the only recovery was writing a bcrypt hash straight into
// Postgres.
//
// These two tests are what stop it coming back. The first pins the invariant
// itself; the second pins the pairing, because the constants are documentation
// (READMEs, four skills, both SPAs' demo-login helpers, the Playwright conftest)
// while the hashes are what actually gets seeded, and a change to one without
// the other would leave every one of those documents lying.
func TestSeededPasswordsSatisfyThePolicy(t *testing.T) {
	for _, tc := range []struct{ name, pw string }{
		{"super admin", SuperAdminPassword},
		{"demo user", DemoUserPassword},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if reason := shared.ValidatePassword(tc.pw); reason != "" {
				t.Errorf("seeded password %q is rejected by the app's own policy: %s",
					tc.pw, reason)
			}
		})
	}
}

func TestSeededPasswordHashesMatchTheirConstants(t *testing.T) {
	for _, tc := range []struct{ name, pw, hash string }{
		{"super admin", SuperAdminPassword, superAdminPasswordHash},
		{"demo user", DemoUserPassword, demoUserPasswordHash},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := bcrypt.CompareHashAndPassword([]byte(tc.hash), []byte(tc.pw)); err != nil {
				t.Errorf("seeded hash does not match the documented password %q: %v", tc.pw, err)
			}
		})
	}
}
