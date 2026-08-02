package activity_test

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/octbase/octbase-api/internal/shared"
	"github.com/octbase/octbase-api/internal/testutil"
)

// The activity log is history: it outlives the task, release or sprint it
// describes, and deleting that thing unlinks the entry (nulling the reference
// and setting targetDeleted) rather than removing it. Deleting the *project* is
// the single exception — there is no feed left to read the entries in.
// See migration 039 and OCT 8bf73df4.

func listProjectActivity(t *testing.T, srv *httptest.Server, pid string) []map[string]any {
	t.Helper()
	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/activity", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var entries []map[string]any
	testutil.DecodeJSON(t, resp, &entries)
	return entries
}

func findEntry(entries []map[string]any, actType string) map[string]any {
	for _, e := range entries {
		if e["type"] == actType {
			return e
		}
	}
	return nil
}

func mustCreateSprint(t *testing.T, srv *httptest.Server, projectID, name string) string {
	t.Helper()
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+projectID+"/sprints",
		map[string]string{"name": name}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var s map[string]any
	testutil.DecodeJSON(t, resp, &s)
	return s["id"].(string)
}

func TestDeleteTask_KeepsActivityUnlinkedAndMarked(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "Doomed Task")

	before := listProjectActivity(t, srv, pid)
	if findEntry(before, "TASK_CREATED") == nil {
		t.Fatal("expected a TASK_CREATED entry before the delete")
	}

	resp := testutil.Do(t, srv, http.MethodDelete, "/api/v1/tasks/"+tid, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNoContent)

	after := listProjectActivity(t, srv, pid)
	e := findEntry(after, "TASK_CREATED")
	if e == nil {
		t.Fatal("TASK_CREATED entry disappeared with its task; the log must survive it")
	}
	if e["taskId"] != nil {
		t.Errorf("taskId = %v, want nil — the link must be dropped so the row opens nothing", e["taskId"])
	}
	if e["targetDeleted"] != true {
		t.Errorf("targetDeleted = %v, want true — this is what the UI greys out by", e["targetDeleted"])
	}
}

func TestDeleteRelease_KeepsActivityUnlinkedAndMarked(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	rid := testutil.MustCreateRelease(t, srv, pid, "1.0.0")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/releases/"+rid+"/close", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	e := findEntry(listProjectActivity(t, srv, pid), "RELEASE_CLOSED")
	if e == nil {
		t.Fatal("expected a RELEASE_CLOSED entry")
	}
	if e["releaseId"] != rid {
		t.Fatalf("releaseId = %v, want %q — without it the delete cannot find the entry", e["releaseId"], rid)
	}

	resp = testutil.Do(t, srv, http.MethodDelete, "/api/v1/releases/"+rid, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNoContent)

	e = findEntry(listProjectActivity(t, srv, pid), "RELEASE_CLOSED")
	if e == nil {
		t.Fatal("RELEASE_CLOSED entry disappeared with its release")
	}
	if e["releaseId"] != nil {
		t.Errorf("releaseId = %v, want nil", e["releaseId"])
	}
	if e["targetDeleted"] != true {
		t.Errorf("targetDeleted = %v, want true", e["targetDeleted"])
	}
}

func TestDeleteSprint_KeepsActivityUnlinkedAndMarked(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	sid := mustCreateSprint(t, srv, pid, "Sprint 1")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/sprints/"+sid+"/start", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	e := findEntry(listProjectActivity(t, srv, pid), "SPRINT_STARTED")
	if e == nil {
		t.Fatal("expected a SPRINT_STARTED entry")
	}
	if e["sprintId"] != sid {
		t.Fatalf("sprintId = %v, want %q", e["sprintId"], sid)
	}

	resp = testutil.Do(t, srv, http.MethodDelete, "/api/v1/sprints/"+sid, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNoContent)

	e = findEntry(listProjectActivity(t, srv, pid), "SPRINT_STARTED")
	if e == nil {
		t.Fatal("SPRINT_STARTED entry disappeared with its sprint")
	}
	if e["sprintId"] != nil {
		t.Errorf("sprintId = %v, want nil", e["sprintId"])
	}
	if e["targetDeleted"] != true {
		t.Errorf("targetDeleted = %v, want true", e["targetDeleted"])
	}
}

// A project-level entry never had a reference, so deleting anything else must
// not grey it out — targetDeleted distinguishes "lost its target" from "never
// had one".
func TestProjectLevelActivity_IsNotMarkedDeleted(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/pages",
		map[string]string{"title": "Page", "content": "hello"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)

	resp = testutil.Do(t, srv, http.MethodDelete, "/api/v1/tasks/"+tid, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNoContent)

	e := findEntry(listProjectActivity(t, srv, pid), "PAGE_CREATED")
	if e == nil {
		t.Fatal("expected a PAGE_CREATED entry")
	}
	if e["targetDeleted"] != false {
		t.Errorf("targetDeleted = %v, want false for an entry that never had a target", e["targetDeleted"])
	}
}

func TestDeleteProject_RemovesActivity(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	testutil.MustCreateTask(t, srv, pid, "T")

	var before int
	if err := db.QueryRow(`SELECT COUNT(*) FROM activity_entries WHERE project_id=$1`, pid).Scan(&before); err != nil {
		t.Fatalf("count activity: %v", err)
	}
	if before == 0 {
		t.Fatal("expected activity before the project delete")
	}

	resp := testutil.Do(t, srv, http.MethodDelete, "/api/v1/projects/"+pid, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNoContent)

	var after int
	if err := db.QueryRow(`SELECT COUNT(*) FROM activity_entries WHERE project_id=$1`, pid).Scan(&after); err != nil {
		t.Fatalf("count activity: %v", err)
	}
	if after != 0 {
		t.Errorf("activity_entries left after project delete = %d, want 0", after)
	}
}

// insertActivity writes n project-level entries straight to the table, which is
// far cheaper than driving n HTTP writes just to fill a page.
func insertActivity(t *testing.T, db *sql.DB, projectID, taskID string, n int) {
	t.Helper()
	var tid any
	if taskID != "" {
		tid = taskID
	}
	for i := 0; i < n; i++ {
		// Distinct timestamps: LIMIT/OFFSET over a tied ORDER BY may repeat or
		// skip rows between pages, which would make the page-2 count a coin flip.
		_, err := db.Exec(
			`INSERT INTO activity_entries (id,project_id,task_id,actor_user_id,type,message,payload_json,created_at)
			 VALUES ($1,$2,$3,$4,'TASK_UPDATED','','{}',$5)`,
			shared.NewUUID(), projectID, tid, testutil.DemoUserID,
			fmt.Sprintf("2026-01-01T00:%02d:%02dZ", i/60, i%60))
		if err != nil {
			t.Fatalf("insert activity: %v", err)
		}
	}
}

func TestListProjectActivity_DefaultsTo50PerPage(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	insertActivity(t, db, pid, "", 60)

	entries := listProjectActivity(t, srv, pid)
	if len(entries) != 50 {
		t.Errorf("default page = %d entries, want 50", len(entries))
	}

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/activity?page=1", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var page2 []map[string]any
	testutil.DecodeJSON(t, resp, &page2)
	if len(page2) != 10 {
		t.Errorf("page 1 = %d entries, want the remaining 10", len(page2))
	}
}

func TestListTaskActivity_Paginates(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")
	insertActivity(t, db, pid, tid, 60)

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+tid+"/activity", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var entries []map[string]any
	testutil.DecodeJSON(t, resp, &entries)
	if len(entries) != 50 {
		t.Errorf("default page = %d entries, want 50", len(entries))
	}

	resp = testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+tid+"/activity?size=5", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	testutil.DecodeJSON(t, resp, &entries)
	if len(entries) != 5 {
		t.Errorf("size=5 returned %d entries", len(entries))
	}
}
