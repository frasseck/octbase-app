package seed_test

import (
	"testing"

	"github.com/octbase/octbase-api/internal/seed"
	"github.com/octbase/octbase-api/internal/testutil"
	"github.com/octbase/octbase-api/internal/workmanagement"
)

// TestSeedSeedsTaskCounter guards against the duplicate-task-ID regression: the
// seed inserts demo tasks with fixed seq_numbers (1, 2), so the per-project task
// counter must be seeded to that maximum. Otherwise NextSeqNumber lazily starts
// at 1 and the first UI-created task collides with a seeded DP-1/DP-2.
func TestSeedSeedsTaskCounter(t *testing.T) {
	db := testutil.NewTestDB(t)

	if err := seed.Run(db); err != nil {
		t.Fatalf("seed.Run: %v", err)
	}

	// No two seeded tasks in the demo project may share a seq_number.
	var dupes int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM (
		   SELECT seq_number FROM tasks WHERE project_id = $1
		   GROUP BY seq_number HAVING COUNT(*) > 1
		 ) d`, seed.ProjectID).Scan(&dupes); err != nil {
		t.Fatalf("query duplicate seq_numbers: %v", err)
	}
	if dupes != 0 {
		t.Fatalf("seeded tasks have %d duplicated seq_number(s); want 0", dupes)
	}

	// The highest seeded seq_number is 2, so the next allocated number must be 3.
	var maxSeed int
	if err := db.QueryRow(
		`SELECT COALESCE(MAX(seq_number), 0) FROM tasks WHERE project_id = $1`,
		seed.ProjectID).Scan(&maxSeed); err != nil {
		t.Fatalf("query max seq_number: %v", err)
	}

	next, err := workmanagement.NextSeqNumber(db, seed.ProjectID)
	if err != nil {
		t.Fatalf("NextSeqNumber: %v", err)
	}
	if next != maxSeed+1 {
		t.Fatalf("first allocated seq = %d; want %d (maxSeed %d + 1) — counter not seeded, would duplicate an existing DP-%d",
			next, maxSeed+1, maxSeed, next)
	}
}
