package webhooks_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/octbase/octbase-api/internal/shared"
	"github.com/octbase/octbase-api/internal/testutil"
)

// These tests pin the invariants the merge-webhook auto-close used to bypass
// by writing status through the repo directly (2026-08-02 review): the BLOCKER
// completion guard, task immutability, board realignment and the activity
// entry the sprint burndown replays.

// TestAutoClose_SkipsBlockedTask: a task with an open BLOCKER child is not
// completed by a merged PR — the same refusal the interactive status door
// answers with TASK_HAS_BLOCKER; the webhook skips instead.
func TestAutoClose_SkipsBlockedTask(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	t.Setenv("OCTBASE_WEBHOOK_SECRET_BITBUCKET", testBitbucketSecret)
	srv, taskID, branchName := autoCloseServer(t, db, "BITBUCKET")

	now := shared.Now()
	var projectID string
	if err := db.QueryRow(`SELECT project_id FROM tasks WHERE id=$1`, taskID).Scan(&projectID); err != nil {
		t.Fatalf("project id: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO tasks (id, project_id, parent_id, title, task_type, status, priority, board_rank, seq_number, created_at, updated_at, version)
		 VALUES ($1,$2,$3,'Blocker child','SUBTASK','PLANNED','BLOCKER',1000,2,$4,$5,1)`,
		shared.NewUUID(), projectID, taskID, now, now,
	); err != nil {
		t.Fatalf("blocker child: %v", err)
	}

	resp := doWebhook(t, srv, "/api/v1/webhooks/bitbucket", testBitbucketSecret,
		bitbucketPR(branchName), map[string]string{"X-Event-Key": "pullrequest:fulfilled"})
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var status string
	if err := db.QueryRow(`SELECT status FROM tasks WHERE id = $1`, taskID).Scan(&status); err != nil {
		t.Fatalf("query status: %v", err)
	}
	if status != "PLANNED" {
		t.Errorf("task status = %q, want PLANNED (blocked by open BLOCKER child)", status)
	}
}

// TestAutoClose_ArchivedTaskStaysArchived: a merge webhook cannot revive a
// finished task — ARCHIVED is immutable on this door like on every other.
func TestAutoClose_ArchivedTaskStaysArchived(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	t.Setenv("OCTBASE_WEBHOOK_SECRET_BITBUCKET", testBitbucketSecret)
	srv, taskID, branchName := autoCloseServer(t, db, "BITBUCKET")

	if _, err := db.Exec(`UPDATE tasks SET status='ARCHIVED' WHERE id=$1`, taskID); err != nil {
		t.Fatalf("archive task: %v", err)
	}

	resp := doWebhook(t, srv, "/api/v1/webhooks/bitbucket", testBitbucketSecret,
		bitbucketPR(branchName), map[string]string{"X-Event-Key": "pullrequest:fulfilled"})
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var status string
	if err := db.QueryRow(`SELECT status FROM tasks WHERE id = $1`, taskID).Scan(&status); err != nil {
		t.Fatalf("query status: %v", err)
	}
	if status != "ARCHIVED" {
		t.Errorf("task status = %q, want ARCHIVED (immutable)", status)
	}
}

// TestAutoClose_AlignsBoardAndWritesActivity: a boarded task auto-closed by a
// merged PR moves into its board's Done lane (OCT-90/OCT-303) and gains the
// TASK_STATUS_CHANGED activity entry the Activity view and burndown replay,
// with the empty actor of a system action.
func TestAutoClose_AlignsBoardAndWritesActivity(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	t.Setenv("OCTBASE_WEBHOOK_SECRET_BITBUCKET", testBitbucketSecret)
	srv, taskID, branchName := autoCloseServer(t, db, "BITBUCKET")

	now := shared.Now()
	var projectID string
	if err := db.QueryRow(`SELECT project_id FROM tasks WHERE id=$1`, taskID).Scan(&projectID); err != nil {
		t.Fatalf("project id: %v", err)
	}
	boardID := shared.NewUUID()
	plannedCol := shared.NewUUID()
	doneCol := shared.NewUUID()
	if _, err := db.Exec(
		`INSERT INTO boards (id, project_id, name, is_default, created_at, updated_at) VALUES ($1,$2,'Board',1,$3,$4)`,
		boardID, projectID, now, now,
	); err != nil {
		t.Fatalf("board: %v", err)
	}
	for i, col := range []struct{ id, name, status string }{
		{plannedCol, "Planned", "PLANNED"},
		{doneCol, "Done", "DONE"},
	} {
		if _, err := db.Exec(
			`INSERT INTO board_columns (id, board_id, name, status, position, created_at, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			col.id, boardID, col.name, col.status, i, now, now,
		); err != nil {
			t.Fatalf("column %s: %v", col.name, err)
		}
	}
	if _, err := db.Exec(`UPDATE tasks SET board_column_id=$1 WHERE id=$2`, plannedCol, taskID); err != nil {
		t.Fatalf("place card: %v", err)
	}

	resp := doWebhook(t, srv, "/api/v1/webhooks/bitbucket", testBitbucketSecret,
		bitbucketPR(branchName), map[string]string{"X-Event-Key": "pullrequest:fulfilled"})
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var status string
	var columnID *string
	if err := db.QueryRow(`SELECT status, board_column_id FROM tasks WHERE id = $1`, taskID).Scan(&status, &columnID); err != nil {
		t.Fatalf("query task: %v", err)
	}
	if status != "DONE" {
		t.Errorf("task status = %q, want DONE", status)
	}
	if columnID == nil || *columnID != doneCol {
		t.Errorf("board_column_id = %v, want the Done lane %s", columnID, doneCol)
	}

	var n int
	var actor, payload string
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM activity_entries WHERE task_id=$1 AND type='TASK_STATUS_CHANGED'`, taskID,
	).Scan(&n); err != nil {
		t.Fatalf("count activity: %v", err)
	}
	if n != 1 {
		t.Fatalf("TASK_STATUS_CHANGED activity entries = %d, want 1", n)
	}
	if err := db.QueryRow(
		`SELECT actor_user_id, payload_json FROM activity_entries WHERE task_id=$1 AND type='TASK_STATUS_CHANGED'`, taskID,
	).Scan(&actor, &payload); err != nil {
		t.Fatalf("read activity: %v", err)
	}
	if actor != "" {
		t.Errorf("activity actor = %q, want empty (system action)", actor)
	}
	var params struct {
		Status string `json:"status"`
		From   string `json:"from"`
	}
	if err := json.Unmarshal([]byte(payload), &params); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if params.Status != "DONE" || params.From != "PLANNED" {
		t.Errorf("activity params = %+v, want status DONE from PLANNED", params)
	}
}
