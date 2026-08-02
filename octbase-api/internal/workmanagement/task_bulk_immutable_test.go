package workmanagement_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
)

// These tests pin the immutability rule on the bulk door (2026-08-02 review):
// DONE and ARCHIVED tasks are skipped by bulk set_status — the same rule the
// single-task status route (TASK_IMMUTABLE) and MoveTask enforce, expressed in
// the bulk contract's silent-skip semantics. Without the skip, a bulk
// selection could mass-revert finished work and clear done_at, corrupting
// velocity history and auto-archive eligibility.

// doBulk posts a bulk action and returns the reported "updated" count.
func doBulk(t *testing.T, srv *httptest.Server, pid string, taskIDs []string, action, value string) int {
	t.Helper()
	resp := testutil.Do(t, srv, http.MethodPost,
		fmt.Sprintf("/api/v1/projects/%s/tasks/bulk", pid),
		map[string]any{"taskIds": taskIDs, "action": action, "value": value},
		testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		Updated int `json:"updated"`
	}
	testutil.DecodeJSON(t, resp, &out)
	_ = resp.Body.Close()
	return out.Updated
}

// Tasks are read back with getTask (task_archive_sweep_test.go) rather than
// trusting the write's status code — "trust the write" is how the original
// defect stayed invisible.

// mustSetStatus drives a task through the single-task status door.
func mustSetStatus(t *testing.T, srv *httptest.Server, id, status string) {
	t.Helper()
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+id+"/status",
		map[string]any{"status": status}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()
}

// TestBulkSetStatus_SkipsDoneAndArchived: a mixed selection updates only its
// open tasks; the DONE task keeps its status and its done_at stamp, and the
// ARCHIVED task stays ARCHIVED.
func TestBulkSetStatus_SkipsDoneAndArchived(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "BulkImmutable")
	open := testutil.MustCreateTask(t, srv, pid, "Open")
	done := testutil.MustCreateTask(t, srv, pid, "Done")
	archived := testutil.MustCreateTask(t, srv, pid, "Archived")

	mustSetStatus(t, srv, done, "DONE")
	doneAt := getTask(t, srv, done)["doneAt"]
	if doneAt == nil {
		t.Fatal("precondition: DONE task must have doneAt stamped")
	}
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+archived+"/archive", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()

	updated := doBulk(t, srv, pid, []string{open, done, archived}, "set_status", "IN_PROGRESS")
	if updated != 1 {
		t.Errorf("updated = %d, want 1 (only the open task)", updated)
	}
	if got := getTask(t, srv, open)["status"]; got != "IN_PROGRESS" {
		t.Errorf("open task status = %v, want IN_PROGRESS", got)
	}
	after := getTask(t, srv, done)
	if after["status"] != "DONE" {
		t.Errorf("done task status = %v, want DONE (immutable on the bulk door)", after["status"])
	}
	if after["doneAt"] != doneAt {
		t.Errorf("done task doneAt = %v, want unchanged %v", after["doneAt"], doneAt)
	}
	if got := getTask(t, srv, archived)["status"]; got != "ARCHIVED" {
		t.Errorf("archived task status = %v, want ARCHIVED", got)
	}
}

// TestBulkSetStatus_PlannedCannotMassReopen: set_status PLANNED — the exact
// mass-un-complete the review demonstrated — leaves finished work finished.
func TestBulkSetStatus_PlannedCannotMassReopen(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "BulkReopen")
	d1 := testutil.MustCreateTask(t, srv, pid, "D1")
	d2 := testutil.MustCreateTask(t, srv, pid, "D2")
	mustSetStatus(t, srv, d1, "DONE")
	mustSetStatus(t, srv, d2, "DONE")

	if updated := doBulk(t, srv, pid, []string{d1, d2}, "set_status", "PLANNED"); updated != 0 {
		t.Errorf("updated = %d, want 0", updated)
	}
	for _, id := range []string{d1, d2} {
		if got := getTask(t, srv, id)["status"]; got != "DONE" {
			t.Errorf("task %s status = %v, want DONE", id, got)
		}
	}
}

// TestBulkArchive_DoneStillArchivable: DONE → ARCHIVED via bulk archive is the
// auto-archive transition and must keep working; an already-ARCHIVED task is
// skipped so it is not re-stamped or re-logged.
func TestBulkArchive_DoneStillArchivable(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "BulkArchive")
	done := testutil.MustCreateTask(t, srv, pid, "Done")
	mustSetStatus(t, srv, done, "DONE")

	if updated := doBulk(t, srv, pid, []string{done}, "archive", ""); updated != 1 {
		t.Errorf("first archive: updated = %d, want 1", updated)
	}
	if got := getTask(t, srv, done)["status"]; got != "ARCHIVED" {
		t.Errorf("status = %v, want ARCHIVED", got)
	}
	if updated := doBulk(t, srv, pid, []string{done}, "archive", ""); updated != 0 {
		t.Errorf("second archive: updated = %d, want 0 (already ARCHIVED)", updated)
	}
}

// TestBulkSetStatus_ActivityOnlyForUpdated: the per-task activity trail records
// exactly the tasks the bulk action changed — a skipped immutable task must not
// gain a TASK_STATUS_CHANGED entry for a change that never happened.
func TestBulkSetStatus_ActivityOnlyForUpdated(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "BulkActivity")
	open := testutil.MustCreateTask(t, srv, pid, "Open")
	done := testutil.MustCreateTask(t, srv, pid, "Done")
	mustSetStatus(t, srv, done, "DONE")

	if updated := doBulk(t, srv, pid, []string{open, done}, "set_status", "IN_PROGRESS"); updated != 1 {
		t.Fatalf("updated = %d, want 1", updated)
	}

	count := func(taskID string) int {
		var n int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM activity_entries WHERE task_id=$1 AND type='TASK_STATUS_CHANGED' AND payload_json::jsonb->>'status'='IN_PROGRESS'`,
			taskID,
		).Scan(&n); err != nil {
			t.Fatalf("count activity: %v", err)
		}
		return n
	}
	if n := count(open); n != 1 {
		t.Errorf("open task IN_PROGRESS activity entries = %d, want 1", n)
	}
	if n := count(done); n != 0 {
		t.Errorf("done task IN_PROGRESS activity entries = %d, want 0 (it was skipped)", n)
	}
}
