package workmanagement

import (
	"database/sql"

	"github.com/octbase/octbase-api/internal/shared"
)

// This file holds cross-cutting persistence helpers shared by the per-aggregate
// repositories (project_repo.go, task_repo.go, board_repo.go, planning_repo.go).

// versionGuardedResult finishes a version-guarded UPDATE; see
// shared.VersionGuardedResult for the semantics.
func versionGuardedResult(res sql.Result, err error, version *int) error {
	return shared.VersionGuardedResult(res, err, version)
}

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows, allowing a single
// scan function to serve both single-row lookups and iteration loops.
type rowScanner interface {
	Scan(dest ...any) error
}
