package workmanagement

import (
	"database/sql"
	"fmt"

	"github.com/octbase/octbase-api/internal/shared"
)

type ReleaseRepo struct{ db *sql.DB }

func NewReleaseRepo(db *sql.DB) *ReleaseRepo { return &ReleaseRepo{db: db} }

func (r *ReleaseRepo) Create(m *Release) error { return r.create(r.db, m) }

// CreateTx inserts inside a caller-owned transaction, so a project import can
// restore releases in the same transaction as the tasks that reference them.
func (r *ReleaseRepo) CreateTx(tx *sql.Tx, m *Release) error { return r.create(tx, m) }

func (r *ReleaseRepo) create(ex execer, m *Release) error {
	_, err := ex.Exec(`INSERT INTO releases (id,project_id,name,goal,due_date,status,created_at,updated_at,version) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		m.ID, m.ProjectID, m.Name, m.Goal, m.DueDate, m.Status, m.CreatedAt, m.UpdatedAt, m.Version)
	return err
}

func (r *ReleaseRepo) FindByID(id string) (*Release, error) {
	row := r.db.QueryRow(`SELECT id,project_id,name,goal,due_date,status,created_at,updated_at,version FROM releases WHERE id=$1`, id)
	return scanRelease(row)
}

func (r *ReleaseRepo) ListByProject(projectID string) ([]Release, error) {
	rows, err := r.db.Query(`SELECT id,project_id,name,goal,due_date,status,created_at,updated_at,version FROM releases WHERE project_id=$1 ORDER BY created_at`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ms []Release
	for rows.Next() {
		m, err := scanRelease(rows)
		if err != nil {
			return nil, err
		}
		ms = append(ms, *m)
	}
	if ms == nil {
		ms = []Release{}
	}
	return ms, rows.Err()
}

func (r *ReleaseRepo) Update(m *Release) error {
	res, err := r.db.Exec(`UPDATE releases SET name=$1,goal=$2,due_date=$3,status=$4,updated_at=$5,version=version+1 WHERE id=$6 AND version=$7`,
		m.Name, m.Goal, m.DueDate, m.Status, m.UpdatedAt, m.ID, m.Version)
	return versionGuardedResult(res, err, &m.Version)
}

// CloseGuarded closes the release with the usual version guard, but only if no
// non-terminal task references it *at write time* — the NOT EXISTS closes the
// race between the caller's open-task check and the close (a task added to the
// release in that window would otherwise be closed over). Zero affected rows
// surfaces as ErrVersionConflict; the caller disambiguates "open task appeared"
// from a genuine version conflict by recounting.
func (r *ReleaseRepo) CloseGuarded(m *Release) error {
	res, err := r.db.Exec(
		`UPDATE releases SET status=$1,updated_at=$2,version=version+1
		 WHERE id=$3 AND version=$4
		   AND NOT EXISTS (SELECT 1 FROM tasks WHERE release_id=$3 AND status NOT IN ('DONE','ARCHIVED'))`,
		m.Status, m.UpdatedAt, m.ID, m.Version)
	return versionGuardedResult(res, err, &m.Version)
}

func (r *ReleaseRepo) Delete(id string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`UPDATE tasks SET release_id=NULL WHERE release_id=$1`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM releases WHERE id=$1`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func scanRelease(s rowScanner) (*Release, error) {
	var m Release
	err := s.Scan(&m.ID, &m.ProjectID, &m.Name, &m.Goal, &m.DueDate, &m.Status, &m.CreatedAt, &m.UpdatedAt, &m.Version)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan release: %w", err)
	}
	return &m, nil
}

// TaskCategoryRepo handles category persistence.
type TaskCategoryRepo struct{ db *sql.DB }

func NewTaskCategoryRepo(db *sql.DB) *TaskCategoryRepo { return &TaskCategoryRepo{db: db} }

func (r *TaskCategoryRepo) Create(c *TaskCategory) error { return r.create(r.db, c) }

// CreateTx inserts inside a caller-owned transaction (project import).
func (r *TaskCategoryRepo) CreateTx(tx *sql.Tx, c *TaskCategory) error { return r.create(tx, c) }

func (r *TaskCategoryRepo) create(ex execer, c *TaskCategory) error {
	_, err := ex.Exec(`INSERT INTO task_categories (id,project_id,name,description,color,created_at,updated_at,version) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		c.ID, c.ProjectID, c.Name, c.Description, c.Color, c.CreatedAt, c.UpdatedAt, c.Version)
	return err
}

func (r *TaskCategoryRepo) ListByProject(projectID string) ([]TaskCategory, error) {
	rows, err := r.db.Query(`SELECT id,project_id,name,description,color,created_at,updated_at,version FROM task_categories WHERE project_id=$1 ORDER BY name`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var cs []TaskCategory
	for rows.Next() {
		var c TaskCategory
		if err := rows.Scan(&c.ID, &c.ProjectID, &c.Name, &c.Description, &c.Color, &c.CreatedAt, &c.UpdatedAt, &c.Version); err != nil {
			return nil, err
		}
		cs = append(cs, c)
	}
	if cs == nil {
		cs = []TaskCategory{}
	}
	return cs, rows.Err()
}

func (r *TaskCategoryRepo) FindByID(id string) (*TaskCategory, error) {
	row := r.db.QueryRow(`SELECT id,project_id,name,description,color,created_at,updated_at,version FROM task_categories WHERE id=$1`, id)
	var c TaskCategory
	err := row.Scan(&c.ID, &c.ProjectID, &c.Name, &c.Description, &c.Color, &c.CreatedAt, &c.UpdatedAt, &c.Version)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// Update persists changes to a category, guarded by the optimistic-locking
// convention (docs/architecture.md §3): zero rows affected means a
// concurrent edit won the race, surfaced as shared.ErrVersionConflict.
func (r *TaskCategoryRepo) Update(c *TaskCategory) error {
	res, err := r.db.Exec(`UPDATE task_categories SET name=$1,description=$2,color=$3,updated_at=$4,version=version+1 WHERE id=$5 AND version=$6`,
		c.Name, c.Description, c.Color, c.UpdatedAt, c.ID, c.Version)
	return versionGuardedResult(res, err, &c.Version)
}

func (r *TaskCategoryRepo) Delete(id string) error {
	_, err := r.db.Exec(`DELETE FROM task_categories WHERE id=$1`, id)
	return err
}

// TaskTemplateRepo handles template persistence.
type TaskTemplateRepo struct{ db *sql.DB }

func NewTaskTemplateRepo(db *sql.DB) *TaskTemplateRepo { return &TaskTemplateRepo{db: db} }

func (r *TaskTemplateRepo) Create(t *TaskTemplate) error { return r.create(r.db, t) }

// CreateTx inserts inside a caller-owned transaction (project import).
func (r *TaskTemplateRepo) CreateTx(tx *sql.Tx, t *TaskTemplate) error { return r.create(tx, t) }

func (r *TaskTemplateRepo) create(ex execer, t *TaskTemplate) error {
	_, err := ex.Exec(`INSERT INTO task_templates (id,project_id,name,title_template,description_template,task_type,priority,created_at,updated_at,version) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		t.ID, t.ProjectID, t.Name, t.TitleTemplate, t.DescriptionTemplate, t.TaskType, t.Priority, t.CreatedAt, t.UpdatedAt, t.Version)
	return err
}

func (r *TaskTemplateRepo) FindByID(id string) (*TaskTemplate, error) {
	row := r.db.QueryRow(`SELECT id,project_id,name,title_template,description_template,task_type,priority,created_at,updated_at,version FROM task_templates WHERE id=$1`, id)
	return scanTemplate(row)
}

func (r *TaskTemplateRepo) ListByProject(projectID string) ([]TaskTemplate, error) {
	rows, err := r.db.Query(`SELECT id,project_id,name,title_template,description_template,task_type,priority,created_at,updated_at,version FROM task_templates WHERE project_id=$1 ORDER BY name`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ts []TaskTemplate
	for rows.Next() {
		var t TaskTemplate
		if err := rows.Scan(&t.ID, &t.ProjectID, &t.Name, &t.TitleTemplate, &t.DescriptionTemplate, &t.TaskType, &t.Priority, &t.CreatedAt, &t.UpdatedAt, &t.Version); err != nil {
			return nil, err
		}
		ts = append(ts, t)
	}
	if ts == nil {
		ts = []TaskTemplate{}
	}
	return ts, rows.Err()
}

// Update persists changes to a template, guarded by the optimistic-locking
// convention (docs/architecture.md §3): zero rows affected means a
// concurrent edit won the race, surfaced as shared.ErrVersionConflict.
func (r *TaskTemplateRepo) Update(t *TaskTemplate) error {
	res, err := r.db.Exec(`UPDATE task_templates SET name=$1,title_template=$2,description_template=$3,task_type=$4,priority=$5,updated_at=$6,version=version+1 WHERE id=$7 AND version=$8`,
		t.Name, t.TitleTemplate, t.DescriptionTemplate, t.TaskType, t.Priority, t.UpdatedAt, t.ID, t.Version)
	return versionGuardedResult(res, err, &t.Version)
}

func (r *TaskTemplateRepo) Delete(id string) error {
	_, err := r.db.Exec(`DELETE FROM task_templates WHERE id=$1`, id)
	return err
}

func scanTemplate(row *sql.Row) (*TaskTemplate, error) {
	var t TaskTemplate
	err := row.Scan(&t.ID, &t.ProjectID, &t.Name, &t.TitleTemplate, &t.DescriptionTemplate, &t.TaskType, &t.Priority, &t.CreatedAt, &t.UpdatedAt, &t.Version)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan template: %w", err)
	}
	return &t, nil
}

// SprintRepo handles sprint persistence.
type SprintRepo struct{ db *sql.DB }

func NewSprintRepo(db *sql.DB) *SprintRepo { return &SprintRepo{db: db} }

func (r *SprintRepo) Create(s *Sprint) error { return r.createRow(r.db, s) }

// CreateTx inserts inside a caller-owned transaction, so a project import can
// restore sprints before the tasks whose sprint_id references them.
func (r *SprintRepo) CreateTx(tx *sql.Tx, s *Sprint) error { return r.createRow(tx, s) }

func (r *SprintRepo) createRow(ex execer, s *Sprint) error {
	_, err := ex.Exec(
		`INSERT INTO sprints (id,project_id,name,goal,start_date,end_date,status,release_id,committed_count,completed_count,committed_estimate,completed_estimate,estimate_unit,created_at,updated_at,version) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
		s.ID, s.ProjectID, s.Name, s.Goal, s.StartDate, s.EndDate, s.Status, s.ReleaseID, s.CommittedCount, s.CompletedCount,
		s.CommittedEstimate, s.CompletedEstimate, s.EstimateUnit, s.CreatedAt, s.UpdatedAt, s.Version)
	return err
}

func (r *SprintRepo) FindByID(id string) (*Sprint, error) {
	row := r.db.QueryRow(
		`SELECT id,project_id,name,goal,start_date,end_date,status,release_id,committed_count,completed_count,committed_estimate,completed_estimate,estimate_unit,created_at,updated_at,version FROM sprints WHERE id=$1`, id)
	return scanSprint(row)
}

func (r *SprintRepo) ListByProject(projectID string) ([]Sprint, error) {
	rows, err := r.db.Query(
		`SELECT id,project_id,name,goal,start_date,end_date,status,release_id,committed_count,completed_count,committed_estimate,completed_estimate,estimate_unit,created_at,updated_at,version FROM sprints WHERE project_id=$1 ORDER BY created_at DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ss []Sprint
	for rows.Next() {
		s, err := scanSprint(rows)
		if err != nil {
			return nil, err
		}
		ss = append(ss, *s)
	}
	if ss == nil {
		ss = []Sprint{}
	}
	return ss, rows.Err()
}

// FindActive returns the currently ACTIVE sprint for the project, or nil if none.
func (r *SprintRepo) FindActive(projectID string) (*Sprint, error) {
	row := r.db.QueryRow(
		`SELECT id,project_id,name,goal,start_date,end_date,status,release_id,committed_count,completed_count,committed_estimate,completed_estimate,estimate_unit,created_at,updated_at,version FROM sprints WHERE project_id=$1 AND status='ACTIVE' LIMIT 1`, projectID)
	s, err := scanSprint(row)
	return s, err
}

// FindOverlapping returns a non-completed sprint in the project whose date
// range intersects [start, end], excluding the sprint with id excludeID (pass
// "" when creating). Only sprints with both bounds set participate; completed
// sprints (past iterations) never block new ones. Dates are ISO YYYY-MM-DD
// strings, so lexicographic comparison matches chronological order.
func (r *SprintRepo) FindOverlapping(projectID, start, end, excludeID string) (*Sprint, error) {
	row := r.db.QueryRow(
		`SELECT id,project_id,name,goal,start_date,end_date,status,release_id,committed_count,completed_count,committed_estimate,completed_estimate,estimate_unit,created_at,updated_at,version FROM sprints
		 WHERE project_id=$1 AND id<>$2 AND status<>'COMPLETED'
		   AND start_date IS NOT NULL AND end_date IS NOT NULL
		   AND start_date <= $3 AND end_date >= $4
		 LIMIT 1`, projectID, excludeID, end, start)
	return scanSprint(row)
}

func (r *SprintRepo) Update(s *Sprint) error { return r.update(r.db, s) }

// UpdateTx runs the guarded sprint update inside an existing transaction.
func (r *SprintRepo) UpdateTx(tx *sql.Tx, s *Sprint) error { return r.update(tx, s) }

func (r *SprintRepo) update(ex execer, s *Sprint) error {
	res, err := ex.Exec(
		`UPDATE sprints SET name=$1,goal=$2,start_date=$3,end_date=$4,status=$5,release_id=$6,committed_count=$7,completed_count=$8,
		        committed_estimate=$9,completed_estimate=$10,estimate_unit=$11,updated_at=$12,version=version+1
		  WHERE id=$13 AND version=$14`,
		s.Name, s.Goal, s.StartDate, s.EndDate, s.Status, s.ReleaseID, s.CommittedCount, s.CompletedCount,
		s.CommittedEstimate, s.CompletedEstimate, s.EstimateUnit, s.UpdatedAt, s.ID, s.Version)
	return versionGuardedResult(res, err, &s.Version)
}

// Delete removes a sprint after clearing sprint_id from its tasks.
func (r *SprintRepo) Delete(id string) error {
	return shared.WithTx(r.db, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE tasks SET sprint_id=NULL WHERE sprint_id=$1`, id); err != nil {
			return fmt.Errorf("clear task sprint: %w", err)
		}
		if _, err := tx.Exec(`DELETE FROM sprints WHERE id=$1`, id); err != nil {
			return fmt.Errorf("delete sprint: %w", err)
		}
		return nil
	})
}

// ClearIncompleteTasksTx unlinks non-terminal tasks from the sprint inside an
// existing transaction (used when completing a sprint).
func (r *SprintRepo) ClearIncompleteTasksTx(tx *sql.Tx, sprintID string) error {
	_, err := tx.Exec(
		`UPDATE tasks SET sprint_id=NULL WHERE sprint_id=$1 AND status NOT IN ('DONE','ARCHIVED')`, sprintID)
	return err
}

// CountTasks returns the sprint's scope: how many non-archived tasks are
// committed to the sprint (total) and how many of those are DONE.
//
// Scope is tasks.sprint_id, NOT board membership. It used to be the latter, on
// the reasoning that the count should never disagree with what the user sees on
// the board — but a task can be committed to a sprint without ever being carded
// (the task panel sets sprint_id directly), and those tasks were then invisible
// to the count. That is not a display nicety: the same query feeds the snapshot
// taken when a sprint is completed, so Sprint 2 on the dogfooding instance
// closed as 40/41 when 84 tasks carried its sprint_id and 82 were DONE, and the
// snapshot is permanent. Counting the link that actually records commitment is
// the only definition that cannot silently under-report.
func (r *SprintRepo) CountTasks(sprintID string) (total, done int, err error) {
	row := r.db.QueryRow(
		`SELECT COUNT(*), COUNT(*) FILTER (WHERE t.status='DONE')
		   FROM tasks t
		  WHERE t.sprint_id=$1 AND t.status<>'ARCHIVED'`, sprintID)
	err = row.Scan(&total, &done)
	return total, done, err
}

// SumEstimates is the effort twin of CountTasks over the identical scope
// (tasks.sprint_id): the summed estimate of the sprint's non-archived tasks
// (committed) and of the DONE ones among them (completed), in the given
// estimation unit. Unestimated tasks (NULL estimate) contribute nothing —
// SUM ignores NULLs — which is the same "counts as 0" rule the burndown
// applies. Returns (0, 0) for EstimationUnitNone: there is nothing to sum.
func (r *SprintRepo) SumEstimates(sprintID, unit string) (committed, completed float64, err error) {
	query, ok := sumEstimatesQueries[unit]
	if !ok {
		return 0, 0, nil
	}
	row := r.db.QueryRow(query, sprintID)
	err = row.Scan(&committed, &completed)
	return committed, completed, err
}

// The two estimate columns cannot be interpolated into one shared query: a
// column name is not a bind parameter, so building the SQL by concatenation
// would put a Go value into the statement text (and gosec G202 rightly objects,
// even where the value comes from a closed set). Every unit-dependent read in
// this package therefore keeps a whitelist of *complete* query literals keyed
// by the unit — the same shape the dynamic ORDER BY whitelist uses. A unit
// absent from the map estimates nothing, which is how NONE is handled.
var sumEstimatesQueries = map[string]string{
	EstimationUnitPoints: `SELECT COALESCE(SUM(t.story_points),0), COALESCE(SUM(t.story_points) FILTER (WHERE t.status='DONE'),0)
		   FROM tasks t
		  WHERE t.sprint_id=$1 AND t.status<>'ARCHIVED'`,
	EstimationUnitHours: `SELECT COALESCE(SUM(t.estimate_hours),0), COALESCE(SUM(t.estimate_hours) FILTER (WHERE t.status='DONE'),0)
		   FROM tasks t
		  WHERE t.sprint_id=$1 AND t.status<>'ARCHIVED'`,
}

// SprintTaskCount is one sprint's scope, as returned by CountTasksBySprints.
type SprintTaskCount struct {
	Total int
	Done  int
}

// CountTasksBySprints is the batched form of CountTasks: the same sprint-link
// counts for many sprints, grouped by sprint in one aggregate query instead of one
// aggregate per sprint. Sprints holding no countable task are absent from the map
// (the aggregate has no group for them), so callers must treat a missing key as
// 0/0 — exactly what CountTasks returns for them.
func (r *SprintRepo) CountTasksBySprints(sprintIDs []string) (map[string]SprintTaskCount, error) {
	out := make(map[string]SprintTaskCount, len(sprintIDs))
	if len(sprintIDs) == 0 {
		return out, nil
	}
	rows, err := r.db.Query(
		`SELECT t.sprint_id, COUNT(*), COUNT(*) FILTER (WHERE t.status='DONE')
		   FROM tasks t
		  WHERE t.sprint_id = ANY($1) AND t.status<>'ARCHIVED'
		  GROUP BY t.sprint_id`, sprintIDs)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var sprintID string
		var c SprintTaskCount
		if err := rows.Scan(&sprintID, &c.Total, &c.Done); err != nil {
			return nil, err
		}
		out[sprintID] = c
	}
	return out, rows.Err()
}

func scanSprint(s rowScanner) (*Sprint, error) {
	var sp Sprint
	err := s.Scan(&sp.ID, &sp.ProjectID, &sp.Name, &sp.Goal, &sp.StartDate, &sp.EndDate, &sp.Status, &sp.ReleaseID,
		&sp.CommittedCount, &sp.CompletedCount, &sp.CommittedEstimate, &sp.CompletedEstimate, &sp.EstimateUnit,
		&sp.CreatedAt, &sp.UpdatedAt, &sp.Version)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan sprint: %w", err)
	}
	return &sp, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// UnifiedSearchTasks searches tasks across all projects visible to the user.
func (r *TaskRepo) UnifiedSearchTasks(userID, projectID, q string, limit int) ([]map[string]any, error) {
	like := "%" + shared.EscapeLike(q) + "%"
	var rows *sql.Rows
	var err error
	if projectID != "" {
		rows, err = r.db.Query(`
			SELECT t.id, t.project_id, t.title, t.status, p.name
			  FROM tasks t
			  JOIN projects p ON p.id = t.project_id
			  JOIN memberships m ON m.project_id = t.project_id AND m.user_id = $1
			 WHERE t.project_id = $2 AND t.title ILIKE $3 AND t.status != 'ARCHIVED'
			 LIMIT $4`, userID, projectID, like, limit)
	} else {
		rows, err = r.db.Query(`
			SELECT t.id, t.project_id, t.title, t.status, p.name
			  FROM tasks t
			  JOIN projects p ON p.id = t.project_id
			  JOIN memberships m ON m.project_id = t.project_id AND m.user_id = $1
			 WHERE t.title ILIKE $2 AND t.status != 'ARCHIVED'
			 LIMIT $3`, userID, like, limit)
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var results []map[string]any
	for rows.Next() {
		var id, projectID, title, status, projectName string
		if err := rows.Scan(&id, &projectID, &title, &status, &projectName); err != nil {
			return nil, err
		}
		results = append(results, map[string]any{
			"id": id, "projectId": projectID, "title": title, "status": status, "projectName": projectName,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if results == nil {
		results = []map[string]any{}
	}
	return results, nil
}

// GetAssignedTasks returns open tasks assigned to the user (max limit).
func (r *TaskRepo) GetAssignedTasks(userID string, limit int) ([]Task, error) {
	rows, err := r.db.Query(`
		SELECT `+taskColumns+`
		  FROM tasks t
		 WHERE t.assignee_id = $1 AND t.status NOT IN ('DONE','ARCHIVED')
		 ORDER BY t.updated_at DESC
		 LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanTaskRows(rows)
}

// GetReviewingTasks returns tasks where reviewer_id = userID and status = IN_REVIEW.
func (r *TaskRepo) GetReviewingTasks(userID string, limit int) ([]Task, error) {
	rows, err := r.db.Query(`
		SELECT `+taskColumns+`
		  FROM tasks t
		 WHERE t.reviewer_id = $1 AND t.status = 'IN_REVIEW'
		 ORDER BY t.updated_at DESC
		 LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanTaskRows(rows)
}

func scanTaskRows(rows *sql.Rows) ([]Task, error) {
	var tasks []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, *t)
	}
	if tasks == nil {
		tasks = []Task{}
	}
	return tasks, nil
}

// SearchVisible returns projects matching q that the user is a member of.
func (r *ProjectRepo) SearchVisible(userID, q string, limit int) ([]map[string]any, error) {
	like := "%" + shared.EscapeLike(q) + "%"
	rows, err := r.db.Query(`
		SELECT p.id, p.name, p.slug
		  FROM projects p
		  JOIN memberships m ON m.project_id = p.id AND m.user_id = $1
		 WHERE p.name ILIKE $2 AND p.status = 'ACTIVE'
		 LIMIT $3`, userID, like, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var results []map[string]any
	for rows.Next() {
		var id, name, slug string
		if err := rows.Scan(&id, &name, &slug); err != nil {
			return nil, err
		}
		results = append(results, map[string]any{"id": id, "name": name, "slug": slug})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if results == nil {
		results = []map[string]any{}
	}
	return results, nil
}

// GetUpcoming returns releases due within daysAhead for projects the user is a
// member of.
//
// due_date is TEXT holding an RFC3339-ordered value (the codebase-wide timestamp
// convention — see migrations/021_task_done_at.up.sql), so its lexicographic order
// is its chronological order and a date comparison can be expressed as a plain
// string range. That matters: the previous `due_date::date <= …` applied a function
// to the column, which makes the predicate unindexable, so no index could ever
// serve it.
//
// The bounds are exactly equivalent to the old casted comparison for any value
// whose first 10 characters are the YYYY-MM-DD date part:
//   - date(due_date) >= CURRENT_DATE  ⟺  due_date >= 'YYYY-MM-DD' of today
//     (today's earliest possible value is the bare date string itself)
//   - date(due_date) <= CURRENT_DATE + N  ⟺  due_date < 'YYYY-MM-DD' of day N+1
//     (half-open upper bound, so a same-day value with a time part still counts)
//
// A NULL due_date stays excluded by the IS NOT NULL guard, as before.
func (r *ReleaseRepo) GetUpcoming(userID string, daysAhead, limit int) ([]Release, error) {
	rows, err := r.db.Query(`
		SELECT m.id, m.project_id, m.name, m.goal, m.due_date, m.status, m.created_at, m.updated_at, m.version
		  FROM releases m
		  JOIN memberships mem ON mem.project_id = m.project_id AND mem.user_id = $1
		 WHERE m.status = 'PLANNED'
		   AND m.due_date IS NOT NULL
		   AND m.due_date >= to_char(CURRENT_DATE, 'YYYY-MM-DD')
		   AND m.due_date < to_char(CURRENT_DATE + ($2::int + 1), 'YYYY-MM-DD')
		 ORDER BY m.due_date ASC
		 LIMIT $3`, userID, daysAhead, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ms []Release
	for rows.Next() {
		m, err := scanRelease(rows)
		if err != nil {
			return nil, err
		}
		ms = append(ms, *m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if ms == nil {
		ms = []Release{}
	}
	return ms, nil
}

// ScopeTaskIDs returns the IDs of the sprint's non-archived tasks — the same
// scope definition as CountTasks (tasks.sprint_id). Used by the burndown report
// for ACTIVE sprints. It was BoardTaskIDs, scoped by board membership, until
// that definition was found to under-report a sprint's real commitment.
func (r *SprintRepo) ScopeTaskIDs(sprintID string) ([]string, error) {
	rows, err := r.db.Query(
		`SELECT t.id
		   FROM tasks t
		  WHERE t.sprint_id=$1 AND t.status<>'ARCHIVED'`, sprintID)
	if err != nil {
		return nil, err
	}
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

// LinkedTaskIDs returns the IDs of tasks whose sprint_id still points at the
// sprint. After completion this is the set of finished tasks (CompleteSprint
// unlinks the unfinished ones), which is exactly what the burndown report
// needs to reconstruct when work got done in a COMPLETED sprint.
func (r *SprintRepo) LinkedTaskIDs(sprintID string) ([]string, error) {
	rows, err := r.db.Query(`SELECT id FROM tasks WHERE sprint_id=$1`, sprintID)
	if err != nil {
		return nil, err
	}
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

// CompletedByProject returns the project's most recently ended COMPLETED
// sprints (up to limit), newest first, straight from the counts snapshotted
// at completion (migration 015).
func (r *SprintRepo) CompletedByProject(projectID string, limit int) ([]Sprint, error) {
	rows, err := r.db.Query(
		`SELECT id,project_id,name,goal,start_date,end_date,status,release_id,committed_count,completed_count,committed_estimate,completed_estimate,estimate_unit,created_at,updated_at,version
		   FROM sprints
		  WHERE project_id=$1 AND status='COMPLETED'
		  ORDER BY end_date DESC NULLS LAST, updated_at DESC
		  LIMIT $2`, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ss []Sprint
	for rows.Next() {
		s, err := scanSprint(rows)
		if err != nil {
			return nil, err
		}
		ss = append(ss, *s)
	}
	if ss == nil {
		ss = []Sprint{}
	}
	return ss, rows.Err()
}

// NextSeqNumber atomically increments and returns the project task counter.
func NextSeqNumber(db execerQuerier, projectID string) (int, error) {
	var seq int
	err := db.QueryRow(`
		INSERT INTO project_task_counters (project_id, last_seq) VALUES ($1, 1)
		ON CONFLICT (project_id) DO UPDATE SET last_seq = project_task_counters.last_seq + 1
		RETURNING last_seq`, projectID).Scan(&seq)
	if err != nil {
		return 0, fmt.Errorf("next seq number: %w", err)
	}
	return seq, nil
}

type execerQuerier interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
}
