package workmanagement

import (
	"database/sql"
	"fmt"
)

type BoardRepo struct{ db *sql.DB }

func NewBoardRepo(db *sql.DB) *BoardRepo { return &BoardRepo{db: db} }

const boardColumns = `id,project_id,name,is_default,min_columns,max_columns,is_sprint_board,sprint_id,created_at,updated_at,version`

// CreateTx inserts a board inside an existing transaction.
func (r *BoardRepo) CreateTx(tx *sql.Tx, b *Board) error { return r.create(tx, b) }

func (r *BoardRepo) create(ex execer, b *Board) error {
	if b.Version == 0 {
		b.Version = 1
	}
	_, err := ex.Exec(`INSERT INTO boards (id,project_id,name,is_default,min_columns,max_columns,is_sprint_board,sprint_id,created_at,updated_at,version) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		b.ID, b.ProjectID, b.Name, boolToInt(b.IsDefault), b.MinColumns, b.MaxColumns, boolToInt(b.IsSprintBoard), b.SprintID, b.CreatedAt, b.UpdatedAt, b.Version)
	return err
}

func (r *BoardRepo) FindByID(id string) (*Board, error) {
	row := r.db.QueryRow(`SELECT `+boardColumns+` FROM boards WHERE id=$1`, id)
	return scanBoard(row)
}

func (r *BoardRepo) FindDefault(projectID string) (*Board, error) {
	row := r.db.QueryRow(`SELECT `+boardColumns+` FROM boards WHERE project_id=$1 AND is_default=1 LIMIT 1`, projectID)
	return scanBoard(row)
}

// FindBySprint returns the sprint board linked to the given sprint, or nil if
// the sprint owns no board. A sprint owns at most one board (see Service
// provisioning), so LIMIT 1 is exact.
func (r *BoardRepo) FindBySprint(sprintID string) (*Board, error) {
	row := r.db.QueryRow(`SELECT `+boardColumns+` FROM boards WHERE sprint_id=$1 AND is_sprint_board=1 LIMIT 1`, sprintID)
	return scanBoard(row)
}

func (r *BoardRepo) ListByProject(projectID string) ([]Board, error) {
	rows, err := r.db.Query(`SELECT `+boardColumns+` FROM boards WHERE project_id=$1 ORDER BY created_at`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var bs []Board
	for rows.Next() {
		var b Board
		var isDefault, isSprintBoard int
		if err := rows.Scan(&b.ID, &b.ProjectID, &b.Name, &isDefault, &b.MinColumns, &b.MaxColumns, &isSprintBoard, &b.SprintID, &b.CreatedAt, &b.UpdatedAt, &b.Version); err != nil {
			return nil, err
		}
		b.IsDefault = isDefault == 1
		b.IsSprintBoard = isSprintBoard == 1
		bs = append(bs, b)
	}
	if bs == nil {
		bs = []Board{}
	}
	return bs, rows.Err()
}

// ListByProjects returns the boards of every given project in a single query,
// flattened in the same order a loop of ListByProject calls produced: grouped by
// project in the order projectIDs was passed, and by created_at within a project.
// array_position keeps that grouping in SQL, so the caller does not have to
// reassemble it. It backs the dashboard, which would otherwise issue one query
// per accessible project (N+1).
func (r *BoardRepo) ListByProjects(projectIDs []string) ([]Board, error) {
	if len(projectIDs) == 0 {
		return []Board{}, nil
	}
	rows, err := r.db.Query(`SELECT `+boardColumns+` FROM boards
		WHERE project_id = ANY($1)
		ORDER BY array_position($1::text[], project_id), created_at`, projectIDs)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	bs := []Board{}
	for rows.Next() {
		var b Board
		var isDefault, isSprintBoard int
		if err := rows.Scan(&b.ID, &b.ProjectID, &b.Name, &isDefault, &b.MinColumns, &b.MaxColumns, &isSprintBoard, &b.SprintID, &b.CreatedAt, &b.UpdatedAt, &b.Version); err != nil {
			return nil, err
		}
		b.IsDefault = isDefault == 1
		b.IsSprintBoard = isSprintBoard == 1
		bs = append(bs, b)
	}
	return bs, rows.Err()
}

func (r *BoardRepo) Update(b *Board) error {
	res, err := r.db.Exec(`UPDATE boards SET name=$1,min_columns=$2,max_columns=$3,is_sprint_board=$4,sprint_id=$5,updated_at=$6,version=version+1 WHERE id=$7 AND version=$8`,
		b.Name, b.MinColumns, b.MaxColumns, boolToInt(b.IsSprintBoard), b.SprintID, b.UpdatedAt, b.ID, b.Version)
	return versionGuardedResult(res, err, &b.Version)
}

// Delete removes a board. It returns the ids of the tasks whose status was
// reset by the detach (see DeleteTx) so the caller can log per-task activity.
func (r *BoardRepo) Delete(id, now string) ([]string, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	reset, err := r.DeleteTx(tx, id, now)
	if err != nil {
		return nil, err
	}
	return reset, tx.Commit()
}

// DeleteTx removes a board (detaching its tasks and dropping its columns)
// inside an existing transaction, and returns the ids of the tasks whose status
// the detach reset.
//
// Taking a task off a board resets its status (OCT-304): the backlog is defined
// as having no card, so a detached task that kept IN_PROGRESS would be listed as
// work not started while claiming to be under way — the contradiction OCT-303
// closed from the other direction. DONE and ARCHIVED are left alone, the same
// carve-out UpdateRetagTasks makes: they are immutable, they are outside the
// backlog query anyway, and resetting them would un-complete finished work.
//
// This is the path CompleteSprint tears a sprint board down through, so it is
// also what returns unfinished sprint work to the backlog as PLANNED.
func (r *BoardRepo) DeleteTx(tx *sql.Tx, id, now string) ([]string, error) {
	// Reset first — this statement selects on the status it is about to
	// overwrite, so the blanket detach below has to follow it, not precede it.
	rows, err := tx.Query(`UPDATE tasks SET board_column_id=NULL, status=$1, updated_at=$2, version=version+1
		WHERE board_column_id IN (SELECT id FROM board_columns WHERE board_id=$3)
		  AND status NOT IN ('DONE','ARCHIVED') RETURNING id`, StatusPlanned, now, id)
	if err != nil {
		return nil, fmt.Errorf("reset detached board tasks: %w", err)
	}
	// Drained and closed inside the helper — the result set must be closed
	// before the tx can commit.
	reset, err := scanIDRows(rows)
	if err != nil {
		return nil, err
	}
	// Whatever the reset skipped (DONE/ARCHIVED) still has to leave the board.
	if _, err := tx.Exec(`UPDATE tasks SET board_column_id=NULL WHERE board_column_id IN (SELECT id FROM board_columns WHERE board_id=$1)`, id); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM board_columns WHERE board_id=$1`, id); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM boards WHERE id=$1`, id); err != nil {
		return nil, err
	}
	return reset, nil
}

func scanBoard(row *sql.Row) (*Board, error) {
	var b Board
	var isDefault, isSprintBoard int
	err := row.Scan(&b.ID, &b.ProjectID, &b.Name, &isDefault, &b.MinColumns, &b.MaxColumns, &isSprintBoard, &b.SprintID, &b.CreatedAt, &b.UpdatedAt, &b.Version)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan board: %w", err)
	}
	b.IsDefault = isDefault == 1
	b.IsSprintBoard = isSprintBoard == 1
	return &b, nil
}

// BoardColumnRepo handles board column persistence.
type BoardColumnRepo struct{ db *sql.DB }

func NewBoardColumnRepo(db *sql.DB) *BoardColumnRepo { return &BoardColumnRepo{db: db} }

func (r *BoardColumnRepo) Create(c *BoardColumn) error { return r.create(r.db, c) }

// CreateTx inserts a board column inside an existing transaction.
func (r *BoardColumnRepo) CreateTx(tx *sql.Tx, c *BoardColumn) error { return r.create(tx, c) }

func (r *BoardColumnRepo) create(ex execer, c *BoardColumn) error {
	if c.Version == 0 {
		c.Version = 1
	}
	_, err := ex.Exec(`INSERT INTO board_columns (id,board_id,name,status,position,created_at,updated_at,version) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		c.ID, c.BoardID, c.Name, c.Status, c.Position, c.CreatedAt, c.UpdatedAt, c.Version)
	return err
}

// StatusExistsForBoard returns true if the board already has a column with the given status.
func (r *BoardColumnRepo) StatusExistsForBoard(boardID, status string) (bool, error) {
	var count int
	err := r.db.QueryRow(`SELECT COUNT(*) FROM board_columns WHERE board_id=$1 AND status=$2`, boardID, status).Scan(&count)
	return count > 0, err
}

// StatusExistsForProject returns true if any board in the project has a column
// (lane) with the given status. This is how custom lane names become valid task
// statuses: a status is permitted iff a lane defines it.
func (r *BoardColumnRepo) StatusExistsForProject(projectID, status string) (bool, error) {
	var count int
	err := r.db.QueryRow(`SELECT COUNT(*) FROM board_columns bc JOIN boards b ON b.id = bc.board_id WHERE b.project_id=$1 AND bc.status=$2`, projectID, status).Scan(&count)
	return count > 0, err
}

// FindByBoardAndStatus returns the (at most one) column on a board whose lane
// status matches, or nil. A board is constrained to a single column per status
// (COLUMN_STATUS_DUPLICATE), so this is the lane a task must sit in for its
// status — used to keep the board column aligned with a status change.
func (r *BoardColumnRepo) FindByBoardAndStatus(boardID, status string) (*BoardColumn, error) {
	row := r.db.QueryRow(`SELECT id,board_id,name,status,position,created_at,updated_at,version FROM board_columns WHERE board_id=$1 AND status=$2`, boardID, status)
	var c BoardColumn
	err := row.Scan(&c.ID, &c.BoardID, &c.Name, &c.Status, &c.Position, &c.CreatedAt, &c.UpdatedAt, &c.Version)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan board column: %w", err)
	}
	return &c, nil
}

// MaxBoardRankInColumn returns the highest board_rank among tasks currently in a
// column, or 0 when the column is empty. A card realigned into a lane by a
// status change is appended below it (max + DefaultBoardRank).
func (r *BoardColumnRepo) MaxBoardRankInColumn(columnID string) (int, error) {
	var maxRank int
	err := r.db.QueryRow(`SELECT COALESCE(MAX(board_rank),0) FROM tasks WHERE board_column_id=$1`, columnID).Scan(&maxRank)
	if err != nil {
		return 0, fmt.Errorf("max board rank: %w", err)
	}
	return maxRank, nil
}

// FindByID returns a single board column by primary key, or nil if not found.
func (r *BoardColumnRepo) FindByID(id string) (*BoardColumn, error) {
	row := r.db.QueryRow(`SELECT id,board_id,name,status,position,created_at,updated_at,version FROM board_columns WHERE id=$1`, id)
	var c BoardColumn
	err := row.Scan(&c.ID, &c.BoardID, &c.Name, &c.Status, &c.Position, &c.CreatedAt, &c.UpdatedAt, &c.Version)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan board column: %w", err)
	}
	return &c, nil
}

// FindByIDInBoard returns the column only when it belongs to boardID, or nil —
// the parent-scoped ownership guard for column sub-resource routes.
func (r *BoardColumnRepo) FindByIDInBoard(id, boardID string) (*BoardColumn, error) {
	row := r.db.QueryRow(`SELECT id,board_id,name,status,position,created_at,updated_at,version FROM board_columns WHERE id=$1 AND board_id=$2`, id, boardID)
	var c BoardColumn
	err := row.Scan(&c.ID, &c.BoardID, &c.Name, &c.Status, &c.Position, &c.CreatedAt, &c.UpdatedAt, &c.Version)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan board column: %w", err)
	}
	return &c, nil
}

func (r *BoardColumnRepo) ListByBoard(boardID string) ([]BoardColumn, error) {
	rows, err := r.db.Query(`SELECT id,board_id,name,status,position,created_at,updated_at,version FROM board_columns WHERE board_id=$1 ORDER BY position`, boardID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var cs []BoardColumn
	for rows.Next() {
		var c BoardColumn
		if err := rows.Scan(&c.ID, &c.BoardID, &c.Name, &c.Status, &c.Position, &c.CreatedAt, &c.UpdatedAt, &c.Version); err != nil {
			return nil, err
		}
		cs = append(cs, c)
	}
	if cs == nil {
		cs = []BoardColumn{}
	}
	return cs, rows.Err()
}

func (r *BoardColumnRepo) Update(c *BoardColumn) error {
	res, err := r.db.Exec(`UPDATE board_columns SET name=$1,status=$2,position=$3,updated_at=$4,version=version+1 WHERE id=$5 AND version=$6`,
		c.Name, c.Status, c.Position, c.UpdatedAt, c.ID, c.Version)
	return versionGuardedResult(res, err, &c.Version)
}

func (r *BoardColumnRepo) Delete(id string) error {
	_, err := r.db.Exec(`DELETE FROM board_columns WHERE id=$1`, id)
	return err
}

// DeleteDetachingTasks removes a column — scoped to its board, so a writer on
// one board can never delete another board's lane — after detaching the tasks
// parked in it, mirroring what BoardRepo.DeleteTx does for a whole board. There
// is no FK on tasks.board_column_id: without the detach the lane's tasks keep a
// dangling column id, rendering on no board while also being excluded from the
// backlog (which selects board_column_id IS NULL). Reports whether the column
// existed on the board, and returns the ids of the tasks whose status the
// detach reset so the caller can log per-task activity.
//
// The detached tasks are reset to PLANNED (OCT-304) for the reason given on
// BoardRepo.DeleteTx: a task with no card is in the backlog, and the backlog
// holds work that has not started. DONE and ARCHIVED keep their status.
func (r *BoardColumnRepo) DeleteDetachingTasks(boardID, id, now string) (bool, []string, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return false, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.Exec(`DELETE FROM board_columns WHERE id=$1 AND board_id=$2`, id, boardID)
	if err != nil {
		return false, nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, nil, nil
	}
	// Reset before the blanket detach: this statement selects on the status it
	// overwrites, so clearing the lane first would leave it nothing to match.
	rows, err := tx.Query(`UPDATE tasks SET board_column_id=NULL, status=$1, updated_at=$2, version=version+1
		WHERE board_column_id=$3 AND status NOT IN ('DONE','ARCHIVED') RETURNING id`, StatusPlanned, now, id)
	if err != nil {
		return false, nil, fmt.Errorf("reset detached lane tasks: %w", err)
	}
	// Drained and closed inside the helper — the result set must be closed
	// before the tx can commit.
	reset, err := scanIDRows(rows)
	if err != nil {
		return false, nil, err
	}
	if _, err := tx.Exec(`UPDATE tasks SET board_column_id=NULL WHERE board_column_id=$1`, id); err != nil {
		return false, nil, err
	}
	return true, reset, tx.Commit()
}

// UpdateRetagTasks persists a column edit and, when the lane's status changed,
// retags the lane's tasks from their old statuses to the new one in the same
// transaction. A lane's cards carry its status (the board buckets by column,
// every other view reads task.status), so changing one without the other makes
// the same card report two different stages. DONE and ARCHIVED tasks are left
// untouched (immutable), and done_at stays in sync exactly like task updates
// do. Returns the ids of the retagged tasks so the caller can log replayable
// per-task status-change activity, like bulk set_status does.
func (r *BoardColumnRepo) UpdateRetagTasks(c *BoardColumn, oldStatus, now string) ([]string, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.Exec(`UPDATE board_columns SET name=$1,status=$2,position=$3,updated_at=$4,version=version+1 WHERE id=$5 AND version=$6`,
		c.Name, c.Status, c.Position, c.UpdatedAt, c.ID, c.Version)
	if err := versionGuardedResult(res, err, &c.Version); err != nil {
		return nil, err
	}
	var retagged []string
	if c.Status != oldStatus {
		rows, err := tx.Query(`UPDATE tasks SET status=$1, updated_at=$2, version=version+1,
			done_at=CASE WHEN $1='DONE' THEN COALESCE(done_at,$2) ELSE NULL END
			WHERE board_column_id=$3 AND status NOT IN ('DONE','ARCHIVED') RETURNING id`, c.Status, now, c.ID)
		if err != nil {
			return nil, fmt.Errorf("retag lane tasks: %w", err)
		}
		// Drained and closed inside the helper — the result set must be closed
		// before the tx can commit.
		if retagged, err = scanIDRows(rows); err != nil {
			return nil, err
		}
	}
	return retagged, tx.Commit()
}

// scanIDRows drains a single-id-column result set and closes it before
// returning, so a caller inside a transaction can commit afterwards.
func scanIDRows(rows *sql.Rows) ([]string, error) {
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// CountByBoard returns the number of columns (lanes) on a board.
func (r *BoardColumnRepo) CountByBoard(boardID string) (int, error) {
	var n int
	err := r.db.QueryRow(`SELECT COUNT(*) FROM board_columns WHERE board_id=$1`, boardID).Scan(&n)
	return n, err
}

// BoardExternalColumnRepo handles persistence of cross-board read-only columns.
type BoardExternalColumnRepo struct{ db *sql.DB }

func NewBoardExternalColumnRepo(db *sql.DB) *BoardExternalColumnRepo {
	return &BoardExternalColumnRepo{db: db}
}

func (r *BoardExternalColumnRepo) Create(c *BoardExternalColumn) error {
	_, err := r.db.Exec(`INSERT INTO board_external_columns (id,board_id,source_column_id,position,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6)`,
		c.ID, c.BoardID, c.SourceColumnID, c.Position, c.CreatedAt, c.UpdatedAt)
	return err
}

// ExistsForBoard reports whether the consuming board already references the
// given source column.
func (r *BoardExternalColumnRepo) ExistsForBoard(boardID, sourceColumnID string) (bool, error) {
	var n int
	err := r.db.QueryRow(`SELECT COUNT(*) FROM board_external_columns WHERE board_id=$1 AND source_column_id=$2`, boardID, sourceColumnID).Scan(&n)
	return n > 0, err
}

// ListByBoard returns the read-only external columns referenced by a board,
// resolving the source board name and column name/status for display.
func (r *BoardExternalColumnRepo) ListByBoard(boardID string) ([]BoardExternalColumn, error) {
	rows, err := r.db.Query(`
		SELECT ec.id, ec.board_id, ec.source_column_id, ec.position, ec.created_at, ec.updated_at,
		       sc.board_id, sb.name, sb.project_id, p.name, sc.name, sc.status
		FROM board_external_columns ec
		JOIN board_columns sc ON sc.id = ec.source_column_id
		JOIN boards sb ON sb.id = sc.board_id
		JOIN projects p ON p.id = sb.project_id
		WHERE ec.board_id=$1
		ORDER BY ec.position, ec.created_at`, boardID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var cs []BoardExternalColumn
	for rows.Next() {
		var c BoardExternalColumn
		if err := rows.Scan(&c.ID, &c.BoardID, &c.SourceColumnID, &c.Position, &c.CreatedAt, &c.UpdatedAt,
			&c.SourceBoardID, &c.SourceBoardName, &c.SourceProjectID, &c.SourceProjectName, &c.SourceColumnName, &c.SourceColumnStatus); err != nil {
			return nil, err
		}
		cs = append(cs, c)
	}
	if cs == nil {
		cs = []BoardExternalColumn{}
	}
	return cs, rows.Err()
}

// Delete removes an external-column reference only when it belongs to boardID and
// reports whether a row was removed, so a writer on one board cannot delete
// another project's board external columns.
func (r *BoardExternalColumnRepo) Delete(boardID, id string) (bool, error) {
	res, err := r.db.Exec(`DELETE FROM board_external_columns WHERE id=$1 AND board_id=$2`, id, boardID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ReleaseRepo handles release persistence.
