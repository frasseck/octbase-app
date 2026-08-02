package workmanagement

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/octbase/octbase-api/internal/shared"
)

const taskColumns = `t.id,t.project_id,t.title,t.description,t.task_type,t.status,t.priority,t.parent_id,t.assignee_id,t.reporter_id,t.reviewer_id,t.release_id,t.sprint_id,t.due_date,t.board_column_id,t.board_rank,t.seq_number,t.external_ref,t.created_at,t.updated_at,t.version,t.pinned,t.done_at,t.story_points,t.estimate_hours::float8`

// TaskRepo handles task persistence.
type TaskRepo struct{ db *sql.DB }

func NewTaskRepo(db *sql.DB) *TaskRepo { return &TaskRepo{db: db} }

func (r *TaskRepo) Create(t *Task) error {
	return r.create(r.db, t)
}

// CreateTx inserts a task inside an existing transaction.
func (r *TaskRepo) CreateTx(tx *sql.Tx, t *Task) error {
	return r.create(tx, t)
}

func (r *TaskRepo) create(db execer, t *Task) error {
	// done_at is derived from status, not a caller-supplied field: it is set to
	// updated_at when a task is created already DONE, NULL otherwise. The same
	// CASE in update() keeps it in sync across every write path.
	_, err := db.Exec(`INSERT INTO tasks (id,project_id,title,description,task_type,status,priority,parent_id,assignee_id,reporter_id,reviewer_id,release_id,sprint_id,due_date,board_column_id,board_rank,seq_number,external_ref,created_at,updated_at,version,pinned,done_at,story_points,estimate_hours) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,CASE WHEN $6='DONE' THEN $20 ELSE NULL END,$23,$24)`,
		t.ID, t.ProjectID, t.Title, t.Description, t.TaskType, t.Status, t.Priority, t.ParentID,
		t.AssigneeID, t.ReporterID, t.ReviewerID, t.ReleaseID, t.SprintID, t.DueDate, t.BoardColumnID, t.BoardRank,
		t.SeqNumber, t.ExternalRef, t.CreatedAt, t.UpdatedAt, t.Version, t.Pinned, t.StoryPoints, t.EstimateHours)
	return err
}

func (r *TaskRepo) FindByID(id string) (*Task, error) {
	row := r.db.QueryRow(`SELECT `+taskColumns+` FROM tasks t WHERE t.id=$1`, id)
	return scanTask(row)
}

// FindByIDInProject returns the task only when it belongs to projectID, or nil.
// Parent-scoped lookups are the standard ownership guard for sub-resources:
// the query itself enforces "child belongs to guarded parent", so a child in
// another project reads as not-found (map with shared.RequireFound).
func (r *TaskRepo) FindByIDInProject(id, projectID string) (*Task, error) {
	row := r.db.QueryRow(`SELECT `+taskColumns+` FROM tasks t WHERE t.id=$1 AND t.project_id=$2`, id, projectID)
	return scanTask(row)
}

// validSortColumns maps allowed sortBy values to safe SQL column references.
var validSortColumns = map[string]string{
	"created_at": "t.created_at",
	"updated_at": "t.updated_at",
	"priority":   "t.priority",
	"title":      "t.title",
}

// listFilterColumns maps the supported list filters to their SQL columns, in
// the order the WHERE clause is built. Shared by List and CountList so the
// count can never drift from the listing it describes.
var listFilterColumns = [][2]string{
	{"status", "t.status"},
	{"priority", "t.priority"},
	{"assigneeId", "t.assignee_id"},
	{"taskType", "t.task_type"},
	{"parentId", "t.parent_id"},
	{"releaseId", "t.release_id"},
	{"sprintId", "t.sprint_id"},
}

// buildListWhere renders the WHERE clause for the given project and filters,
// returning the SQL fragment, its bind args and the next free placeholder index.
func buildListWhere(projectID string, filters map[string]string) (string, []any, int) {
	args := []any{projectID}
	n := 1
	q := fmt.Sprintf(` WHERE t.project_id=$%d`, n)
	for _, fc := range listFilterColumns {
		if v, ok := filters[fc[0]]; ok && v != "" {
			n++
			q += fmt.Sprintf(` AND %s=$%d`, fc[1], n)
			args = append(args, v)
		}
	}
	return q, args, n
}

// CountList returns how many tasks match the same project + filters a List
// call would, ignoring pagination — the total the X-Total-Count header carries.
func (r *TaskRepo) CountList(projectID string, filters map[string]string) (int, error) {
	where, args, _ := buildListWhere(projectID, filters)
	var total int
	err := r.db.QueryRow(`SELECT count(*) FROM tasks t`+where, args...).Scan(&total)
	return total, err
}

func (r *TaskRepo) List(projectID string, filters map[string]string, page, size int) ([]Task, error) {
	where, args, n := buildListWhere(projectID, filters)
	q := `SELECT ` + taskColumns + ` FROM tasks t` + where // #nosec G202 -- taskColumns is a package constant; where holds only $n placeholders

	// Sorting: only allow known columns.
	sortCol := "t.created_at"
	if col, ok := validSortColumns[filters["sortBy"]]; ok {
		sortCol = col
	}
	sortDir := "DESC"
	if filters["order"] == "asc" {
		sortDir = "ASC"
	}
	q += fmt.Sprintf(` ORDER BY %s %s`, sortCol, sortDir)

	n++
	q += fmt.Sprintf(` LIMIT $%d`, n)
	args = append(args, size)
	n++
	q += fmt.Sprintf(` OFFSET $%d`, n)
	args = append(args, page*size)
	rows, err := r.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ts []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		ts = append(ts, *t)
	}
	if ts == nil {
		ts = []Task{}
	}
	return ts, rows.Err()
}

// Children returns the direct child tasks of taskID (any status), ordered by
// creation. Used to enforce the hierarchy rules on type change and delete, and
// small enough in practice (one board's subtasks) to load eagerly.
func (r *TaskRepo) Children(taskID string) ([]Task, error) {
	rows, err := r.db.Query(`SELECT `+taskColumns+` FROM tasks t WHERE t.parent_id=$1 ORDER BY t.created_at`, taskID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ts []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		ts = append(ts, *t)
	}
	if ts == nil {
		ts = []Task{}
	}
	return ts, rows.Err()
}

// The direct-child forms of the two guards below (OpenChildPriorityExists and
// AnyOpenChildPriorityExists) were removed when the guards learned to walk the
// whole subtree. They are not kept as convenience wrappers on purpose: a
// one-level check next to a subtree check is the exact shape of the bug that
// was just fixed, and the next caller would pick whichever name read better.

// openDescendantsCTE walks the whole subtree under the task ids in $1, not just
// their direct children. The guards below are about "is there unfinished work
// anywhere under this task", and a one-level check answers a different question:
// an open BLOCKER grandchild slipped straight past the blocker guard, so nesting
// one level deeper was enough to defeat it.
//
// The depth cap is a safety belt, not a business rule. Nothing validates that
// parent_id is acyclic (relations have a cycle guard, the parent hierarchy does
// not), and an unbounded WITH RECURSIVE over a cycle does not error — it spins
// until the connection dies. 50 is far past any real hierarchy: the product
// models six levels (theme → initiative → epic → story → task → subtask).
const openDescendantsCTE = `
	WITH RECURSIVE descendants AS (
		SELECT id, title, status, priority, 1 AS depth
		  FROM tasks WHERE parent_id = ANY($1)
		UNION ALL
		SELECT t.id, t.title, t.status, t.priority, d.depth + 1
		  FROM tasks t JOIN descendants d ON t.parent_id = d.id
		 WHERE d.depth < 50
	)`

// AnyOpenDescendantPriorityExists is the subtree-wide form of
// AnyOpenChildPriorityExists: it reports whether any *open* descendant, at any
// depth, of any of taskIDs carries the given priority. Open-only for the same
// load-bearing reason as the direct-child version — a finished blocker blocks
// nothing, and priority is immutable on a DONE task, so counting finished
// descendants would lock a parent out of DONE permanently.
func (r *TaskRepo) AnyOpenDescendantPriorityExists(taskIDs []string, priority string) (bool, error) {
	if len(taskIDs) == 0 {
		return false, nil
	}
	var exists bool
	err := r.db.QueryRow(openDescendantsCTE+
		` SELECT EXISTS(SELECT 1 FROM descendants WHERE priority=$2 AND status NOT IN ($3,$4))`,
		taskIDs, priority, StatusDone, StatusArchived).Scan(&exists)
	return exists, err
}

func (r *TaskRepo) Update(t *Task) error {
	return r.update(r.db, t)
}

// UpdateTx updates a task inside an existing transaction.
func (r *TaskRepo) UpdateTx(tx *sql.Tx, t *Task) error {
	return r.update(tx, t)
}

func (r *TaskRepo) update(db execer, t *Task) error {
	// done_at follows status: stamped (once) when entering DONE, cleared on any
	// transition out (including reopen). COALESCE preserves the original
	// completion time across later edits that keep the task DONE.
	// The version guard makes the read-modify-write optimistic: the UPDATE only
	// applies if the row still has the version the caller's snapshot was based
	// on, so a concurrent editor's write is never silently overwritten.
	res, err := db.Exec(`UPDATE tasks SET title=$1,description=$2,task_type=$3,status=$4,priority=$5,parent_id=$6,assignee_id=$7,reporter_id=$8,reviewer_id=$9,release_id=$10,sprint_id=$11,due_date=$12,board_column_id=$13,board_rank=$14,pinned=$15,updated_at=$16,story_points=$17,estimate_hours=$18,done_at=CASE WHEN $4='DONE' THEN COALESCE(done_at,$16) ELSE NULL END,version=version+1 WHERE id=$19 AND version=$20`,
		t.Title, t.Description, t.TaskType, t.Status, t.Priority, t.ParentID,
		t.AssigneeID, t.ReporterID, t.ReviewerID, t.ReleaseID, t.SprintID, t.DueDate, t.BoardColumnID, t.BoardRank,
		t.Pinned, t.UpdatedAt, t.StoryPoints, t.EstimateHours, t.ID, t.Version)
	return versionGuardedResult(res, err, &t.Version)
}

func (r *TaskRepo) SearchByTitle(projectID, q string, page, size int) ([]Task, error) {
	likeQ := "%" + shared.EscapeLike(q) + "%"
	rows, err := r.db.Query(`SELECT `+taskColumns+` FROM tasks t WHERE t.project_id=$1 AND (t.title ILIKE $2 OR t.description ILIKE $3) ORDER BY t.created_at DESC LIMIT $4 OFFSET $5`,
		projectID, likeQ, likeQ, size, page*size)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ts []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		ts = append(ts, *t)
	}
	if ts == nil {
		ts = []Task{}
	}
	return ts, rows.Err()
}

func (r *TaskRepo) Backlog(projectID string) ([]Task, error) {
	rows, err := r.db.Query(`SELECT `+taskColumns+` FROM tasks t WHERE t.project_id=$1 AND t.status NOT IN ('DONE','ARCHIVED') AND t.board_column_id IS NULL ORDER BY t.board_rank, t.created_at`,
		projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ts []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		ts = append(ts, *t)
	}
	if ts == nil {
		ts = []Task{}
	}
	return ts, rows.Err()
}

// ListByColumn returns the tasks placed in a single board column, ordered by
// board rank. It backs the read-only rendering of cross-board external columns.
func (r *TaskRepo) ListByColumn(columnID string) ([]Task, error) {
	rows, err := r.db.Query(`SELECT `+taskColumns+` FROM tasks t WHERE t.board_column_id=$1 ORDER BY t.board_rank, t.created_at`, columnID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ts []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		ts = append(ts, *t)
	}
	if ts == nil {
		ts = []Task{}
	}
	return ts, rows.Err()
}

// ListByColumns returns the tasks in any of the given board columns, grouped by
// board_column_id, in a single query. It backs board rendering of multiple
// cross-board external columns without issuing one query per column (N+1).
func (r *TaskRepo) ListByColumns(columnIDs []string) (map[string][]Task, error) {
	out := make(map[string][]Task, len(columnIDs))
	if len(columnIDs) == 0 {
		return out, nil
	}
	args := make([]any, len(columnIDs))
	ph := make([]string, len(columnIDs))
	for i, id := range columnIDs {
		args[i] = id
		ph[i] = fmt.Sprintf("$%d", i+1)
	}
	rows, err := r.db.Query( // #nosec G202 -- concatenates the taskColumns constant and generated $n placeholders only
		`SELECT `+taskColumns+` FROM tasks t WHERE t.board_column_id IN (`+strings.Join(ph, ",")+ // #nosec G202 -- constant columns + generated placeholders
			`) ORDER BY t.board_rank, t.created_at`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		if t.BoardColumnID != nil {
			out[*t.BoardColumnID] = append(out[*t.BoardColumnID], *t)
		}
	}
	return out, rows.Err()
}

// ListByIDs returns the tasks with the given ids that belong to projectID, in
// no particular order. Ids that do not exist or belong elsewhere are simply
// absent, which is the same silent scoping the bulk update itself applies.
func (r *TaskRepo) ListByIDs(projectID string, ids []string) ([]Task, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := r.db.Query(`SELECT `+taskColumns+` FROM tasks t WHERE t.id = ANY($1) AND t.project_id=$2`, ids, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ts []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		ts = append(ts, *t)
	}
	return ts, rows.Err()
}

// SetBoardPlacement writes a task's lane and rank and nothing else. It does not
// bump version, deliberately: it follows a bulk update, which is itself a blind
// per-row SET, and a version bump there would make every card in the selection
// conflict with any editor holding it open.
func (r *TaskRepo) SetBoardPlacement(id string, columnID *string, rank int, now string) error {
	_, err := r.db.Exec(`UPDATE tasks SET board_column_id=$1, board_rank=$2, updated_at=$3 WHERE id=$4`,
		columnID, rank, now, id)
	return err
}

// ListAll returns every task that belongs to projectID, ordered by seq_number
// (nulls last) then created_at. It is intended for bulk operations such as
// CSV export where pagination is not appropriate.
func (r *TaskRepo) ListAll(projectID string) ([]Task, error) {
	rows, err := r.db.Query(
		`SELECT `+taskColumns+` FROM tasks t WHERE t.project_id=$1 ORDER BY t.seq_number NULLS LAST, t.created_at`,
		projectID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ts []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		ts = append(ts, *t)
	}
	if ts == nil {
		ts = []Task{}
	}
	return ts, rows.Err()
}

// cascadeDeleteChildStmts lists, in order, the batched statements that detach a
// task's child rows (branch references, page references, relations,
// attachments, links, comments, activity) before the task row itself is
// removed. Each statement targets any of a slice of task IDs via = ANY($1), so
// it runs once per table regardless of how many tasks are being deleted. Delete
// (a 1-element slice) and BulkDelete (an N-element slice) both go through
// cascadeDeleteTaskChildren, so a future child table only needs to be added
// here once.
//
// Activity is the one that is unlinked rather than deleted: the log is the
// project's history and survives the task it describes, so the entry keeps its
// message and loses its reference. target_deleted is what tells the reader (and
// the UI, which greys the row and drops the link) that the row is not merely
// project-level. Missing this statement is not silent — activity_entries.task_id
// carries an FK, so the task delete that follows would fail.
var cascadeDeleteChildStmts = []string{
	`DELETE FROM branch_references WHERE task_id = ANY($1)`,
	`DELETE FROM page_task_references WHERE task_id = ANY($1)`,
	`DELETE FROM task_relations WHERE source_task_id = ANY($1) OR target_task_id = ANY($1)`,
	`DELETE FROM task_attachments WHERE task_id = ANY($1)`,
	`DELETE FROM task_links WHERE task_id = ANY($1)`,
	`DELETE FROM task_comments WHERE task_id = ANY($1)`,
	`UPDATE activity_entries SET task_id=NULL, target_deleted=TRUE WHERE task_id = ANY($1)`,
}

// cascadeDeleteTaskChildren detaches every child-table row referencing any of
// taskIDs, batched as one statement per table. It does not delete the task
// rows themselves; callers do that afterward, inside the same transaction.
func cascadeDeleteTaskChildren(tx *sql.Tx, taskIDs []string) error {
	if len(taskIDs) == 0 {
		return nil
	}
	for _, stmt := range cascadeDeleteChildStmts {
		if _, err := tx.Exec(stmt, taskIDs); err != nil {
			return fmt.Errorf("task cascade delete: %w", err)
		}
	}
	return nil
}

func (r *TaskRepo) Delete(id string) error {
	return shared.WithTx(r.db, func(tx *sql.Tx) error {
		if err := cascadeDeleteTaskChildren(tx, []string{id}); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM tasks WHERE id=$1`, id); err != nil {
			return fmt.Errorf("task cascade delete: %w", err)
		}
		return nil
	})
}

// BulkDelete removes all taskIDs that belong to projectID, cascading their
// comments, links, attachments, relations, branch references, and page
// references in the same way Delete does. IDs that don't belong to
// projectID are silently skipped. Returns the deleted tasks (for audit
// logging).
//
// The lookup, cascade, and task-row delete are each a single batched
// statement over the whole ID set (via = ANY($1)), rather than one round
// trip per task per table, so this scales as O(1) queries per table instead
// of O(N).
func (r *TaskRepo) BulkDelete(projectID string, taskIDs []string) ([]Task, error) {
	if len(taskIDs) == 0 {
		return nil, nil
	}
	var deleted []Task
	err := shared.WithTx(r.db, func(tx *sql.Tx) error {
		var ids []string
		if err := func() error {
			rows, err := tx.Query(`SELECT `+taskColumns+` FROM tasks t WHERE t.id = ANY($1) AND t.project_id=$2`, taskIDs, projectID)
			if err != nil {
				return err
			}
			defer func() { _ = rows.Close() }()
			for rows.Next() {
				t, err := scanTask(rows)
				if err != nil {
					return err
				}
				deleted = append(deleted, *t)
				ids = append(ids, t.ID)
			}
			return rows.Err()
		}(); err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		// The hierarchy guard applies to bulk deletes too: a task whose children
		// are not all part of this same deletion is silently kept (consistent
		// with how foreign IDs are skipped) — deleting it would orphan the
		// children and violate the tasks.parent_id FK.
		blocked, err := parentsWithOutsideChildren(tx, ids)
		if err != nil {
			return err
		}
		if len(blocked) > 0 {
			kept := deleted[:0]
			ids = ids[:0]
			for _, t := range deleted {
				if blocked[t.ID] {
					continue
				}
				kept = append(kept, t)
				ids = append(ids, t.ID)
			}
			deleted = kept
			if len(ids) == 0 {
				return nil
			}
		}
		if err := cascadeDeleteTaskChildren(tx, ids); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM tasks WHERE id = ANY($1)`, ids); err != nil {
			return fmt.Errorf("task cascade delete: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return deleted, nil
}

// parentsWithOutsideChildren returns the subset of ids that still have child
// tasks outside the ids set itself. Parent+child in the same DELETE statement
// is fine (the FK sees both rows gone); a child left behind is not.
func parentsWithOutsideChildren(tx *sql.Tx, ids []string) (map[string]bool, error) {
	rows, err := tx.Query(`SELECT DISTINCT parent_id FROM tasks WHERE parent_id = ANY($1) AND NOT (id = ANY($1))`, ids)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	blocked := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		blocked[id] = true
	}
	return blocked, rows.Err()
}

// BulkSetStatus updates the status of all taskIDs that belong to projectID.
// Returns the IDs of the rows actually updated, so callers can log a
// replayable per-task status-change activity entry.
//
// DONE and ARCHIVED rows are skipped, not updated: a finished task is immutable
// on every other status door (the status route refuses with TASK_IMMUTABLE,
// MoveTask refuses the lane move), and reopening is a deliberate per-task
// ceremony (POST /tasks/{id}/reopen), not something a bulk selection does as a
// side effect. The skip extends the bulk contract's existing silent-skip
// semantics (unknown and cross-project IDs) rather than failing the batch, so
// a mixed selection still updates its open tasks.
func (r *TaskRepo) BulkSetStatus(projectID string, taskIDs []string, status, now string) ([]string, error) {
	return r.bulkUpdate(projectID, taskIDs, "status", status, now, []string{StatusDone, StatusArchived})
}

// BulkSetPriority updates the priority of all taskIDs that belong to projectID.
// No immutable-status skip: priority stays editable on finished tasks, exactly
// like the single-task priority endpoint.
func (r *TaskRepo) BulkSetPriority(projectID string, taskIDs []string, priority, now string) ([]string, error) {
	return r.bulkUpdate(projectID, taskIDs, "priority", priority, now, nil)
}

// BulkSetAssignee updates the assignee of all taskIDs that belong to projectID.
// No immutable-status skip: assignee stays editable on finished tasks.
func (r *TaskRepo) BulkSetAssignee(projectID string, taskIDs []string, assigneeID, now string) ([]string, error) {
	return r.bulkUpdate(projectID, taskIDs, "assignee_id", assigneeID, now, nil)
}

// BulkSetRelease updates the release of all taskIDs that belong to projectID.
// No immutable-status skip: release placement stays editable on finished tasks
// (re-attributing a DONE task to a release is how history gets corrected).
func (r *TaskRepo) BulkSetRelease(projectID string, taskIDs []string, releaseID, now string) ([]string, error) {
	return r.bulkUpdate(projectID, taskIDs, "release_id", releaseID, now, nil)
}

// BulkArchive sets status = ARCHIVED for all taskIDs that belong to projectID.
// Already-ARCHIVED rows are skipped so re-archiving neither bumps updated_at
// nor logs a second TASK_ARCHIVED activity entry; DONE rows are deliberately
// archivable — DONE → ARCHIVED is the same transition the auto-archive sweep
// performs.
func (r *TaskRepo) BulkArchive(projectID string, taskIDs []string, now string) ([]string, error) {
	return r.bulkUpdate(projectID, taskIDs, "status", StatusArchived, now, []string{StatusArchived})
}

// ArchiveStaleDone flips every DONE task in projectID whose done_at is older
// than cutoff to ARCHIVED, hiding it from the board. It returns the affected
// tasks (post-update) so the caller can log activity for each.
// Because the swept tasks become ARCHIVED, a later sweep never touches them
// again, so the per-task activity log fires exactly once.
func (r *TaskRepo) ArchiveStaleDone(projectID, cutoff, now string) ([]Task, error) {
	// Aliasing the target table as t lets this RETURNING clause reuse the same
	// taskColumns projection as every SELECT in this file, instead of hand
	// -repeating the column list a second time (they'd otherwise drift apart).
	rows, err := r.db.Query(
		`UPDATE tasks AS t SET status=$1, updated_at=$2, done_at=NULL, version=version+1
		 WHERE t.project_id=$3 AND t.status='DONE' AND t.done_at IS NOT NULL AND t.done_at < $4
		 RETURNING `+taskColumns,
		StatusArchived, now, projectID, cutoff,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var archived []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		archived = append(archived, *t)
	}
	return archived, rows.Err()
}

// allowedBulkColumns is the set of columns that bulkUpdate may target.
var allowedBulkColumns = map[string]bool{
	"status":      true,
	"priority":    true,
	"assignee_id": true,
	"release_id":  true,
}

// bulkUpdate applies a single column = value update to every task of the project
// in one statement, silently skipping tasks not found in the project and tasks
// whose current status is in skipStatuses (nil means no status is skipped). Only
// columns in allowedBulkColumns are accepted; others return an error to prevent
// injection from future callers. Returns the IDs of the rows actually updated.
//
// One statement (id = ANY($3) … RETURNING id), not one round trip per task: a
// 500-task bulk action is a single query, and the update really is atomic — the
// per-task loop this replaced could fail halfway and leave part of the selection
// changed.
func (r *TaskRepo) bulkUpdate(projectID string, taskIDs []string, col, value, now string, skipStatuses []string) ([]string, error) {
	if !allowedBulkColumns[col] {
		return nil, fmt.Errorf("bulkUpdate: column %q is not allowed", col)
	}
	if len(taskIDs) == 0 {
		return nil, nil
	}
	// A bulk status change must keep done_at in sync exactly like update() does,
	// otherwise tasks bulk-moved to DONE would never auto-archive.
	doneAtClause := ""
	if col == "status" {
		doneAtClause = `, done_at=CASE WHEN $1='DONE' THEN COALESCE(done_at,$2) ELSE NULL END`
	}
	// assignee_id and release_id are nullable links: an empty value means
	// "clear" and must be stored as SQL NULL, not "", so the "is anyone
	// assigned?" / release-membership reads downstream stay honest — the same
	// normalization emptyToNil applies on the single-task paths.
	var boundValue any = value
	if value == "" && (col == "assignee_id" || col == "release_id") {
		boundValue = nil
	}
	skipClause := ""
	args := []any{boundValue, now, taskIDs, projectID}
	if len(skipStatuses) > 0 {
		skipClause = ` AND NOT (status = ANY($5))`
		args = append(args, skipStatuses)
	}
	rows, err := r.db.Query( // #nosec G202 -- col comes from a fixed switch in the caller, never from input; values are parameterized
		`UPDATE tasks SET `+col+`=$1, updated_at=$2`+doneAtClause+ // #nosec G202 -- col is a fixed switch value in the caller
			` WHERE id = ANY($3) AND project_id=$4`+skipClause+` RETURNING id`,
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("bulk update tasks: %w", err)
	}
	defer func() { _ = rows.Close() }()
	found := make(map[string]bool, len(taskIDs))
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("bulk update tasks: %w", err)
		}
		found[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("bulk update tasks: %w", err)
	}
	// Return the updated IDs in the caller's request order (RETURNING order is
	// unspecified), so the per-task activity entries a bulk status change writes
	// stay in the same order as before.
	var updated []string
	for _, id := range taskIDs {
		if found[id] {
			updated = append(updated, id)
			delete(found, id) // a duplicate ID in the request must not double-count
		}
	}
	return updated, nil
}

func (r *TaskRepo) CountOpenForRelease(releaseID string) (int, error) {
	var count int
	err := r.db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE release_id=$1 AND status NOT IN ('DONE','ARCHIVED')`, releaseID).Scan(&count)
	return count, err
}

func scanTask(s rowScanner) (*Task, error) {
	var t Task
	err := s.Scan(&t.ID, &t.ProjectID, &t.Title, &t.Description, &t.TaskType, &t.Status, &t.Priority, &t.ParentID,
		&t.AssigneeID, &t.ReporterID, &t.ReviewerID, &t.ReleaseID, &t.SprintID, &t.DueDate, &t.BoardColumnID, &t.BoardRank,
		&t.SeqNumber, &t.ExternalRef, &t.CreatedAt, &t.UpdatedAt, &t.Version, &t.Pinned, &t.DoneAt, &t.StoryPoints, &t.EstimateHours)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan task: %w", err)
	}
	return &t, nil
}

// TaskCommentRepo handles comment persistence.
type TaskCommentRepo struct{ db *sql.DB }

func NewTaskCommentRepo(db *sql.DB) *TaskCommentRepo { return &TaskCommentRepo{db: db} }

func (r *TaskCommentRepo) Create(c *TaskComment) error {
	return r.create(r.db, c)
}

// CreateTx inserts a task comment inside an existing transaction.
func (r *TaskCommentRepo) CreateTx(tx *sql.Tx, c *TaskComment) error {
	return r.create(tx, c)
}

func (r *TaskCommentRepo) create(db execer, c *TaskComment) error {
	if c.Version == 0 {
		c.Version = 1
	}
	_, err := db.Exec(`INSERT INTO task_comments (id,task_id,author_id,parent_id,text,created_at,updated_at,version) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		c.ID, c.TaskID, c.AuthorID, c.ParentID, c.Text, c.CreatedAt, c.UpdatedAt, c.Version)
	return err
}

// commentSelect is the shared projection for reads: it LEFT JOINs users so the
// author's display name travels with the comment, falling back to an empty
// string when the author row is missing (e.g. a removed account).
const commentSelect = `SELECT c.id,c.task_id,c.author_id,COALESCE(u.display_name,''),c.parent_id,c.text,c.created_at,c.updated_at,c.version
	FROM task_comments c LEFT JOIN users u ON u.id=c.author_id`

func scanComment(s interface{ Scan(...any) error }, c *TaskComment) error {
	return s.Scan(&c.ID, &c.TaskID, &c.AuthorID, &c.AuthorName, &c.ParentID, &c.Text, &c.CreatedAt, &c.UpdatedAt, &c.Version)
}

func (r *TaskCommentRepo) ListByTask(taskID string) ([]TaskComment, error) {
	rows, err := r.db.Query(commentSelect+` WHERE c.task_id=$1 ORDER BY c.created_at`, taskID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var cs []TaskComment
	for rows.Next() {
		var c TaskComment
		if err := scanComment(rows, &c); err != nil {
			return nil, err
		}
		cs = append(cs, c)
	}
	if cs == nil {
		cs = []TaskComment{}
	}
	return cs, rows.Err()
}

// ListByTasks returns the comments of every given task grouped by task ID, in a
// single query. Within a group the order is identical to ListByTask (created_at
// ascending); tasks without comments are simply absent from the map. It backs the
// project and Jira CSV exports, which would otherwise run one query per task.
func (r *TaskCommentRepo) ListByTasks(taskIDs []string) (map[string][]TaskComment, error) {
	out := make(map[string][]TaskComment, len(taskIDs))
	if len(taskIDs) == 0 {
		return out, nil
	}
	rows, err := r.db.Query(commentSelect+` WHERE c.task_id = ANY($1) ORDER BY c.task_id, c.created_at`, taskIDs)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var c TaskComment
		if err := scanComment(rows, &c); err != nil {
			return nil, err
		}
		out[c.TaskID] = append(out[c.TaskID], c)
	}
	return out, rows.Err()
}

func (r *TaskCommentRepo) FindByID(id string) (*TaskComment, error) {
	var c TaskComment
	err := scanComment(r.db.QueryRow(commentSelect+` WHERE c.id=$1`, id), &c)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// FindByIDInTask returns the comment only when it belongs to taskID, or nil —
// the parent-scoped ownership guard for comment sub-resource routes.
func (r *TaskCommentRepo) FindByIDInTask(id, taskID string) (*TaskComment, error) {
	var c TaskComment
	err := scanComment(r.db.QueryRow(commentSelect+` WHERE c.id=$1 AND c.task_id=$2`, id, taskID), &c)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *TaskCommentRepo) Update(c *TaskComment) error {
	res, err := r.db.Exec(`UPDATE task_comments SET text=$1,updated_at=$2,version=version+1 WHERE id=$3 AND version=$4`,
		c.Text, c.UpdatedAt, c.ID, c.Version)
	return versionGuardedResult(res, err, &c.Version)
}

func (r *TaskCommentRepo) Delete(id string) error {
	_, err := r.db.Exec(`DELETE FROM task_comments WHERE id=$1`, id)
	return err
}

// TaskLinkRepo handles link persistence.
type TaskLinkRepo struct{ db *sql.DB }

func NewTaskLinkRepo(db *sql.DB) *TaskLinkRepo { return &TaskLinkRepo{db: db} }

func (r *TaskLinkRepo) Create(l *TaskLink) error {
	return r.create(r.db, l)
}

// CreateTx inserts a task link inside an existing transaction.
func (r *TaskLinkRepo) CreateTx(tx *sql.Tx, l *TaskLink) error {
	return r.create(tx, l)
}

func (r *TaskLinkRepo) create(db execer, l *TaskLink) error {
	_, err := db.Exec(`INSERT INTO task_links (id,task_id,url,title,created_at) VALUES ($1,$2,$3,$4,$5)`,
		l.ID, l.TaskID, l.URL, l.Title, l.CreatedAt)
	return err
}

func (r *TaskLinkRepo) ListByTask(taskID string) ([]TaskLink, error) {
	rows, err := r.db.Query(`SELECT id,task_id,url,title,created_at FROM task_links WHERE task_id=$1 ORDER BY created_at`, taskID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ls []TaskLink
	for rows.Next() {
		var l TaskLink
		if err := rows.Scan(&l.ID, &l.TaskID, &l.URL, &l.Title, &l.CreatedAt); err != nil {
			return nil, err
		}
		ls = append(ls, l)
	}
	if ls == nil {
		ls = []TaskLink{}
	}
	return ls, rows.Err()
}

// ListByTasks returns the links of every given task grouped by task ID, in a
// single query, with the same per-task order as ListByTask (created_at
// ascending). Tasks without links are absent from the map.
func (r *TaskLinkRepo) ListByTasks(taskIDs []string) (map[string][]TaskLink, error) {
	out := make(map[string][]TaskLink, len(taskIDs))
	if len(taskIDs) == 0 {
		return out, nil
	}
	rows, err := r.db.Query(`SELECT id,task_id,url,title,created_at FROM task_links
		WHERE task_id = ANY($1) ORDER BY task_id, created_at`, taskIDs)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var l TaskLink
		if err := rows.Scan(&l.ID, &l.TaskID, &l.URL, &l.Title, &l.CreatedAt); err != nil {
			return nil, err
		}
		out[l.TaskID] = append(out[l.TaskID], l)
	}
	return out, rows.Err()
}

// Delete removes a link only when it belongs to taskID and reports whether a row
// was removed. Scoping the delete to the task (not just the link ID) stops a
// writer on one task from deleting links attached to another project's task.
func (r *TaskLinkRepo) Delete(taskID, id string) (bool, error) {
	res, err := r.db.Exec(`DELETE FROM task_links WHERE id=$1 AND task_id=$2`, id, taskID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// TaskAttachmentRepo handles attachment persistence.
type TaskAttachmentRepo struct{ db *sql.DB }

func NewTaskAttachmentRepo(db *sql.DB) *TaskAttachmentRepo { return &TaskAttachmentRepo{db: db} }

func (r *TaskAttachmentRepo) Create(a *TaskAttachment) error {
	return r.create(r.db, a)
}

// CreateTx inserts a task attachment inside an existing transaction.
func (r *TaskAttachmentRepo) CreateTx(tx *sql.Tx, a *TaskAttachment) error {
	return r.create(tx, a)
}

func (r *TaskAttachmentRepo) create(db execer, a *TaskAttachment) error {
	_, err := db.Exec(`INSERT INTO task_attachments (id,task_id,filename,content_type,size_bytes,external_url,storage_key,uploaded_by,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		a.ID, a.TaskID, a.Filename, a.ContentType, a.SizeBytes, a.ExternalURL, nullIfEmpty(a.StorageKey), nullIfEmpty(a.UploadedBy), a.CreatedAt)
	return err
}

// UploadedBytesByUser returns the total stored size of the uploaded (not
// external-link) attachments a user has on record, the usage figure the
// per-user storage quota is enforced against.
func (r *TaskAttachmentRepo) UploadedBytesByUser(userID string) (int64, error) {
	var n int64
	err := r.db.QueryRow(`SELECT COALESCE(SUM(size_bytes),0) FROM task_attachments WHERE uploaded_by=$1 AND storage_key IS NOT NULL AND storage_key<>''`, userID).Scan(&n)
	return n, err
}

func (r *TaskAttachmentRepo) ListByTask(taskID string) ([]TaskAttachment, error) {
	rows, err := r.db.Query(`SELECT id,task_id,filename,content_type,size_bytes,external_url,COALESCE(storage_key,''),created_at FROM task_attachments WHERE task_id=$1 ORDER BY created_at`, taskID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var as []TaskAttachment
	for rows.Next() {
		var a TaskAttachment
		if err := rows.Scan(&a.ID, &a.TaskID, &a.Filename, &a.ContentType, &a.SizeBytes, &a.ExternalURL, &a.StorageKey, &a.CreatedAt); err != nil {
			return nil, err
		}
		as = append(as, a)
	}
	if as == nil {
		as = []TaskAttachment{}
	}
	return as, rows.Err()
}

// ListByTasks returns the attachments of every given task grouped by task ID, in
// a single query, with the same per-task order as ListByTask (created_at
// ascending). Tasks without attachments are absent from the map.
func (r *TaskAttachmentRepo) ListByTasks(taskIDs []string) (map[string][]TaskAttachment, error) {
	out := make(map[string][]TaskAttachment, len(taskIDs))
	if len(taskIDs) == 0 {
		return out, nil
	}
	rows, err := r.db.Query(`SELECT id,task_id,filename,content_type,size_bytes,external_url,COALESCE(storage_key,''),created_at
		FROM task_attachments WHERE task_id = ANY($1) ORDER BY task_id, created_at`, taskIDs)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var a TaskAttachment
		if err := rows.Scan(&a.ID, &a.TaskID, &a.Filename, &a.ContentType, &a.SizeBytes, &a.ExternalURL, &a.StorageKey, &a.CreatedAt); err != nil {
			return nil, err
		}
		out[a.TaskID] = append(out[a.TaskID], a)
	}
	return out, rows.Err()
}

// FindByID returns a single attachment by primary key, or nil if not found.
func (r *TaskAttachmentRepo) FindByID(id string) (*TaskAttachment, error) {
	var a TaskAttachment
	err := r.db.QueryRow(`SELECT id,task_id,filename,content_type,size_bytes,external_url,COALESCE(storage_key,''),created_at FROM task_attachments WHERE id=$1`, id).
		Scan(&a.ID, &a.TaskID, &a.Filename, &a.ContentType, &a.SizeBytes, &a.ExternalURL, &a.StorageKey, &a.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan attachment: %w", err)
	}
	return &a, nil
}

// FindByIDInTask returns the attachment only when it belongs to taskID, or
// nil — the parent-scoped ownership guard for attachment sub-resource routes.
func (r *TaskAttachmentRepo) FindByIDInTask(id, taskID string) (*TaskAttachment, error) {
	var a TaskAttachment
	err := r.db.QueryRow(`SELECT id,task_id,filename,content_type,size_bytes,external_url,COALESCE(storage_key,''),created_at FROM task_attachments WHERE id=$1 AND task_id=$2`, id, taskID).
		Scan(&a.ID, &a.TaskID, &a.Filename, &a.ContentType, &a.SizeBytes, &a.ExternalURL, &a.StorageKey, &a.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan attachment: %w", err)
	}
	return &a, nil
}

func (r *TaskAttachmentRepo) Delete(id string) error {
	_, err := r.db.Exec(`DELETE FROM task_attachments WHERE id=$1`, id)
	return err
}

// StorageKeysForTask returns the non-empty storage keys of every uploaded
// attachment on a task, so the caller can delete the underlying files when the
// task (and its DB rows) are removed.
func (r *TaskAttachmentRepo) StorageKeysForTask(taskID string) ([]string, error) {
	return queryStorageKeys(r.db, `SELECT storage_key FROM task_attachments WHERE task_id=$1 AND storage_key IS NOT NULL AND storage_key<>''`, taskID)
}

// StorageKeysForTasks is the batched form of StorageKeysForTask: the keys of
// every uploaded attachment on any of taskIDs, in one query. Used by the bulk
// delete, which needs the whole selection's keys before the cascade removes the
// attachment rows.
func (r *TaskAttachmentRepo) StorageKeysForTasks(taskIDs []string) ([]string, error) {
	if len(taskIDs) == 0 {
		return nil, nil
	}
	return queryStorageKeys(r.db, `SELECT storage_key FROM task_attachments WHERE task_id = ANY($1) AND storage_key IS NOT NULL AND storage_key<>''`, taskIDs)
}

// StorageKeysForProject returns the storage keys of every uploaded attachment on
// any task in the project.
func (r *TaskAttachmentRepo) StorageKeysForProject(projectID string) ([]string, error) {
	return queryStorageKeys(r.db, `SELECT storage_key FROM task_attachments WHERE storage_key IS NOT NULL AND storage_key<>'' AND task_id IN (SELECT id FROM tasks WHERE project_id=$1)`, projectID)
}

// queryStorageKeys runs a one-argument storage-key query; arg is a single task or
// project ID for the scalar variants, or a []string for the batched one.
func queryStorageKeys(db *sql.DB, query string, arg any) ([]string, error) {
	rows, err := db.Query(query, arg)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		if k != "" {
			keys = append(keys, k)
		}
	}
	return keys, rows.Err()
}

// nullIfEmpty maps an empty string to a SQL NULL so optional columns stay NULL
// rather than storing empty strings.
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// TaskRelationRepo handles relation persistence.
type TaskRelationRepo struct{ db *sql.DB }

func NewTaskRelationRepo(db *sql.DB) *TaskRelationRepo { return &TaskRelationRepo{db: db} }

func (r *TaskRelationRepo) Create(rel *TaskRelation) error {
	_, err := r.db.Exec(`INSERT INTO task_relations (id,source_task_id,target_task_id,relation_type,created_at) VALUES ($1,$2,$3,$4,$5)`,
		rel.ID, rel.SourceTaskID, rel.TargetTaskID, rel.RelationType, rel.CreatedAt)
	return err
}

// CreateTx inserts a relation inside an existing transaction.
func (r *TaskRelationRepo) CreateTx(tx *sql.Tx, rel *TaskRelation) error {
	_, err := tx.Exec(`INSERT INTO task_relations (id,source_task_id,target_task_id,relation_type,created_at) VALUES ($1,$2,$3,$4,$5)`,
		rel.ID, rel.SourceTaskID, rel.TargetTaskID, rel.RelationType, rel.CreatedAt)
	return err
}

// FindByID returns a single relation by its primary key, or nil if not found.
func (r *TaskRelationRepo) FindByID(id string) (*TaskRelation, error) {
	var rel TaskRelation
	err := r.db.QueryRow(`SELECT id,source_task_id,target_task_id,relation_type,created_at FROM task_relations WHERE id=$1`, id).
		Scan(&rel.ID, &rel.SourceTaskID, &rel.TargetTaskID, &rel.RelationType, &rel.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan task relation: %w", err)
	}
	return &rel, nil
}

func (r *TaskRelationRepo) ListByTask(taskID string) ([]TaskRelation, error) {
	rows, err := r.db.Query(`SELECT id,source_task_id,target_task_id,relation_type,created_at FROM task_relations WHERE source_task_id=$1 OR target_task_id=$2 ORDER BY created_at`, taskID, taskID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var rels []TaskRelation
	for rows.Next() {
		var rel TaskRelation
		if err := rows.Scan(&rel.ID, &rel.SourceTaskID, &rel.TargetTaskID, &rel.RelationType, &rel.CreatedAt); err != nil {
			return nil, err
		}
		rels = append(rels, rel)
	}
	if rels == nil {
		rels = []TaskRelation{}
	}
	return rels, rows.Err()
}

// ListByProject returns every relation row whose source task belongs to the
// project, in a stable order. Used by the project export; inverse rows are
// included as stored so an import reproduces the table verbatim.
func (r *TaskRelationRepo) ListByProject(projectID string) ([]TaskRelation, error) {
	rows, err := r.db.Query(`SELECT tr.id,tr.source_task_id,tr.target_task_id,tr.relation_type,tr.created_at
		FROM task_relations tr JOIN tasks t ON t.id = tr.source_task_id
		WHERE t.project_id=$1 ORDER BY tr.created_at, tr.id`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var rels []TaskRelation
	for rows.Next() {
		var rel TaskRelation
		if err := rows.Scan(&rel.ID, &rel.SourceTaskID, &rel.TargetTaskID, &rel.RelationType, &rel.CreatedAt); err != nil {
			return nil, err
		}
		rels = append(rels, rel)
	}
	return rels, rows.Err()
}

func (r *TaskRelationRepo) Exists(sourceID, targetID, relType string) (bool, error) {
	var count int
	err := r.db.QueryRow(`SELECT COUNT(*) FROM task_relations WHERE source_task_id=$1 AND target_task_id=$2 AND relation_type=$3`, sourceID, targetID, relType).Scan(&count)
	return count > 0, err
}

func (r *TaskRelationRepo) Delete(id string) error {
	_, err := r.db.Exec(`DELETE FROM task_relations WHERE id=$1`, id)
	return err
}

// DeleteInverseTx removes the inverse of a relation inside an existing transaction.
func (r *TaskRelationRepo) DeleteInverseTx(tx *sql.Tx, sourceID, targetID, relType string) error {
	_, err := tx.Exec(
		`DELETE FROM task_relations WHERE source_task_id=$1 AND target_task_id=$2 AND relation_type=$3`,
		sourceID, targetID, relType,
	)
	return err
}

// HasCycle checks if adding a BLOCKS edge from source to target would create a cycle.
func (r *TaskRelationRepo) HasCycle(sourceID, targetID string) (bool, error) {
	// BFS: can we reach sourceID starting from targetID following BLOCKS edges?
	visited := map[string]bool{}
	queue := []string{targetID}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == sourceID {
			return true, nil
		}
		if visited[cur] {
			continue
		}
		visited[cur] = true
		if err := func() error {
			rows, err := r.db.Query(`SELECT target_task_id FROM task_relations WHERE source_task_id=$1 AND relation_type='BLOCKS'`, cur)
			if err != nil {
				return err
			}
			defer func() { _ = rows.Close() }()
			for rows.Next() {
				var next string
				if err := rows.Scan(&next); err != nil {
					return err
				}
				queue = append(queue, next)
			}
			return rows.Err()
		}(); err != nil {
			return false, err
		}
	}
	return false, nil
}

// BoardRepo handles board persistence.
