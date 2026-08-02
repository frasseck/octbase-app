package main

import "testing"

// TestMigrationDSN pins the least-privilege role split's backward compatibility
// (security L10). Deployments may run migrations as the schema owner while
// serving traffic as a restricted DML-only role, but the split is opt-in: a
// deployment that sets only OCTBASE_DATABASE_URL must keep running exactly as it
// did before. Getting the fallback wrong would either break every existing
// single-role stack (empty migrate DSN) or silently migrate against the wrong
// database.
func TestMigrationDSN(t *testing.T) {
	const runtime = "postgres://octbase_app:pw@db:5432/octbase"
	const owner = "postgres://octbase:pw@db:5432/octbase"

	tests := []struct {
		name    string
		migrate string
		want    string
	}{
		{"unset falls back to the runtime URL", "", runtime},
		{"blank falls back to the runtime URL", "   ", runtime},
		{"set is used for migrations", owner, owner},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := migrationDSN(runtime, tc.migrate); got != tc.want {
				t.Errorf("migrationDSN(%q, %q) = %q, want %q", runtime, tc.migrate, got, tc.want)
			}
		})
	}
}

// TestMigrationDSNSplitIsDetectable guards the signal runMigrations branches on:
// when the two DSNs differ, migrations must open their own owner connection
// rather than reuse the restricted runtime pool (which by design cannot run DDL).
func TestMigrationDSNSplitIsDetectable(t *testing.T) {
	const runtime = "postgres://octbase_app:pw@db:5432/octbase"
	const owner = "postgres://octbase:pw@db:5432/octbase"

	if migrationDSN(runtime, "") != runtime {
		t.Fatal("single-URL deployments must migrate on the runtime pool")
	}
	if migrationDSN(runtime, owner) == runtime {
		t.Fatal("a configured migrate URL must not resolve to the runtime DSN")
	}
}
