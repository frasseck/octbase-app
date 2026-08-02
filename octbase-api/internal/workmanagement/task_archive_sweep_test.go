package workmanagement_test

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/octbase/octbase-api/internal/testutil"
)

// backdateDoneAt rewrites a task's done_at directly so the sweep, which keys off
// "DONE for longer than 30 days", can be exercised without waiting a month.
func backdateDoneAt(t *testing.T, db *sql.DB, taskID string, daysAgo int) {
	t.Helper()
	ts := time.Now().UTC().AddDate(0, 0, -daysAgo).Format(time.RFC3339)
	if _, err := db.Exec(`UPDATE tasks SET done_at = $1 WHERE id = $2`, ts, taskID); err != nil {
		t.Fatalf("backdate done_at: %v", err)
	}
}

func markDone(t *testing.T, srv *httptest.Server, tid string) {
	t.Helper()
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/status",
		map[string]string{"status": "DONE"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
}

func getTask(t *testing.T, srv *httptest.Server, tid string) map[string]interface{} {
	t.Helper()
	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+tid, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var task map[string]interface{}
	testutil.DecodeJSON(t, resp, &task)
	return task
}

func listTasks(t *testing.T, srv *httptest.Server, pid, query string) []map[string]interface{} {
	t.Helper()
	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/tasks"+query, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var tasks []map[string]interface{}
	testutil.DecodeJSON(t, resp, &tasks)
	return tasks
}

func containsTask(tasks []map[string]interface{}, tid string) bool {
	for _, task := range tasks {
		if task["id"] == tid {
			return true
		}
	}
	return false
}

// TestDoneAtSetOnTransition verifies done_at is stamped when a task enters DONE
// and cleared when it is reopened, since the sweep relies on it being accurate.
func TestDoneAtSetOnTransition(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	if doneAt := getTask(t, srv, tid)["doneAt"]; doneAt != nil {
		t.Errorf("new task doneAt = %v, want nil", doneAt)
	}

	markDone(t, srv, tid)
	if doneAt := getTask(t, srv, tid)["doneAt"]; doneAt == nil {
		t.Error("doneAt is nil after marking DONE, want a timestamp")
	}

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/reopen", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	if doneAt := getTask(t, srv, tid)["doneAt"]; doneAt != nil {
		t.Errorf("doneAt = %v after reopen, want nil", doneAt)
	}
}

// TestSweepArchivesStaleDoneTasks checks a task that has been DONE for over a
// month is auto-archived (hidden from the default listing, visible under the
// ARCHIVED filter) the next time tasks are listed.
func TestSweepArchivesStaleDoneTasks(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "Stale")

	markDone(t, srv, tid)
	backdateDoneAt(t, db, tid, 40)

	// Listing triggers the lazy sweep, after which the task is no longer DONE
	// (so it drops off the board) but is findable under the ARCHIVED filter.
	if done := listTasks(t, srv, pid, "?status=DONE"); containsTask(done, tid) {
		t.Error("stale task still listed as DONE after sweep")
	}
	if archived := listTasks(t, srv, pid, "?status=ARCHIVED"); !containsTask(archived, tid) {
		t.Error("swept task not found under ARCHIVED filter")
	}
	if status := getTask(t, srv, tid)["status"]; status != "ARCHIVED" {
		t.Errorf("status = %v, want ARCHIVED", status)
	}
}

// TestSweepKeepsRecentDoneTasks checks a task done within the retention window
// stays DONE and visible — the boundary the sweep must not cross.
func TestSweepKeepsRecentDoneTasks(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "Recent")

	markDone(t, srv, tid)
	backdateDoneAt(t, db, tid, 20)

	tasks := listTasks(t, srv, pid, "")
	if !containsTask(tasks, tid) {
		t.Error("recently-done task was archived too early")
	}
	if status := getTask(t, srv, tid)["status"]; status != "DONE" {
		t.Errorf("status = %v, want DONE", status)
	}
}
