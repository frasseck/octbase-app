package activity

import (
	"database/sql"
	"encoding/json"

	"github.com/octbase/octbase-api/internal/shared"
)

// Repo handles activity entry persistence.
type Repo struct{ db *sql.DB }

func NewRepo(db *sql.DB) *Repo { return &Repo{db: db} }

// Write inserts an activity entry. taskID can be "" for project-level events.
// params is interpolated by the frontend into the notifications.activity.<type>
// translation string; it may be nil for events with no variable parts.
func (r *Repo) Write(projectID, taskID, actorID, actType string, params map[string]any) error {
	return r.write(r.db, projectID, ref{task: taskID}, actorID, actType, params)
}

// WriteTx inserts an activity entry inside an existing transaction.
func (r *Repo) WriteTx(tx *sql.Tx, projectID, taskID, actorID, actType string, params map[string]any) error {
	return r.write(tx, projectID, ref{task: taskID}, actorID, actType, params)
}

// WriteRelease inserts a release-scoped entry (RELEASE_CLOSED, RELEASE_REOPENED).
// Recording the id, not just the name in params, is what lets DeleteRelease
// unlink the entry instead of leaving it pointing at a release that is gone.
func (r *Repo) WriteRelease(projectID, releaseID, actorID, actType string, params map[string]any) error {
	return r.write(r.db, projectID, ref{release: releaseID}, actorID, actType, params)
}

// WriteSprint is WriteRelease's counterpart for SPRINT_STARTED/SPRINT_COMPLETED.
func (r *Repo) WriteSprint(projectID, sprintID, actorID, actType string, params map[string]any) error {
	return r.write(r.db, projectID, ref{sprint: sprintID}, actorID, actType, params)
}

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// ref is the entry's reference to what it describes. At most one field is set;
// "" means "no reference of this kind", which the insert stores as NULL.
type ref struct{ task, release, sprint string }

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (r *Repo) write(db execer, projectID string, rf ref, actorID, actType string, params map[string]any) error {
	if params == nil {
		params = map[string]any{}
	}
	payload, err := json.Marshal(params)
	if err != nil {
		return err
	}
	entry := &ActivityEntry{
		ID:          shared.NewUUID(),
		ProjectID:   projectID,
		TaskID:      nullable(rf.task),
		ReleaseID:   nullable(rf.release),
		SprintID:    nullable(rf.sprint),
		ActorUserID: actorID,
		Type:        actType,
		PayloadJSON: string(payload),
		CreatedAt:   shared.Now(),
	}
	_, err = db.Exec(`INSERT INTO activity_entries (id,project_id,task_id,release_id,sprint_id,actor_user_id,type,message,payload_json,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		entry.ID, entry.ProjectID, entry.TaskID, entry.ReleaseID, entry.SprintID, entry.ActorUserID, entry.Type, "", entry.PayloadJSON, entry.CreatedAt)
	return err
}

// WriteBatch inserts one activity entry per task ID — same shape as calling
// Write once per task, but as a single multi-row INSERT. Activity logging stays
// explicit (the caller still decides when to log) and the log stays per-task, so
// a bulk board action remains replayable for the sprint burndown; only the round
// trips collapse. All entries share one created_at, exactly as a per-task loop in
// the same request would.
func (r *Repo) WriteBatch(projectID string, taskIDs []string, actorID, actType string, params map[string]any) error {
	if len(taskIDs) == 0 {
		return nil
	}
	if params == nil {
		params = map[string]any{}
	}
	payload, err := json.Marshal(params)
	if err != nil {
		return err
	}
	ids := make([]string, len(taskIDs))
	for i := range taskIDs {
		ids[i] = shared.NewUUID()
	}
	// unnest pairs each generated entry ID with its task ID, so the whole batch
	// is one statement regardless of how many tasks were affected.
	_, err = r.db.Exec(`INSERT INTO activity_entries (id,project_id,task_id,actor_user_id,type,message,payload_json,created_at)
		SELECT u.id, $1, NULLIF(u.task_id,''), $2, $3, '', $4, $5 FROM unnest($6::text[], $7::text[]) AS u(id, task_id)`,
		projectID, actorID, actType, string(payload), shared.Now(), ids, taskIDs)
	return err
}

// entryColumns is the read projection, kept in one place because both list
// queries and scanEntries have to agree on the order.
const entryColumns = `id,project_id,task_id,release_id,sprint_id,target_deleted,actor_user_id,type,message,payload_json,created_at`

func (r *Repo) ListByProject(projectID string, page, size int) ([]ActivityEntry, error) {
	rows, err := r.db.Query(`SELECT `+entryColumns+` FROM activity_entries WHERE project_id=$1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`,
		projectID, size, page*size)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanEntries(rows)
}

func (r *Repo) ListByTask(taskID string, page, size int) ([]ActivityEntry, error) {
	rows, err := r.db.Query(`SELECT `+entryColumns+` FROM activity_entries WHERE task_id=$1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`,
		taskID, size, page*size)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanEntries(rows)
}

func scanEntries(rows *sql.Rows) ([]ActivityEntry, error) {
	var entries []ActivityEntry
	for rows.Next() {
		var e ActivityEntry
		var message string
		if err := rows.Scan(&e.ID, &e.ProjectID, &e.TaskID, &e.ReleaseID, &e.SprintID, &e.TargetDeleted, &e.ActorUserID, &e.Type, &message, &e.PayloadJSON, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.Params = map[string]any{}
		if e.PayloadJSON != "" {
			_ = json.Unmarshal([]byte(e.PayloadJSON), &e.Params)
		}
		entries = append(entries, e)
	}
	if entries == nil {
		entries = []ActivityEntry{}
	}
	return entries, rows.Err()
}
