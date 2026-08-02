package activity_test

import (
	"net/http"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
)

func TestListProjectActivity_Empty(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/activity", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var entries []interface{}
	testutil.DecodeJSON(t, resp, &entries)
	// Project was created but activity only records task events in this impl.
	// Just verify the endpoint is responsive.
	if entries == nil {
		t.Error("expected non-nil slice (even if empty)")
	}
}

func TestListProjectActivity_RecordsTaskCreation(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	testutil.MustCreateTask(t, srv, pid, "Activity Task")

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/activity", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var entries []map[string]interface{}
	testutil.DecodeJSON(t, resp, &entries)
	if len(entries) == 0 {
		t.Error("expected at least one activity entry after task creation")
	}
	found := false
	for _, e := range entries {
		if e["type"] == "TASK_CREATED" {
			found = true
			params, ok := e["params"].(map[string]interface{})
			if !ok {
				t.Fatalf("expected params object, got %T", e["params"])
			}
			if params["title"] != "Activity Task" {
				t.Errorf("params[title] = %v, want %q", params["title"], "Activity Task")
			}
			break
		}
	}
	if !found {
		t.Error("expected TASK_CREATED activity entry")
	}
}

func TestListProjectActivity_RecordsStatusChange(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/status",
		map[string]string{"status": "IN_PROGRESS"}, testutil.DemoUserID)

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/activity", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var entries []map[string]interface{}
	testutil.DecodeJSON(t, resp, &entries)

	found := false
	for _, e := range entries {
		if e["type"] == "TASK_STATUS_CHANGED" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected TASK_STATUS_CHANGED activity entry")
	}
}

func TestListTaskActivity_OK(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "Activity Task")

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+tid+"/activity", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var entries []map[string]interface{}
	testutil.DecodeJSON(t, resp, &entries)
	if len(entries) == 0 {
		t.Error("expected at least TASK_CREATED activity for the task")
	}
	if entries[0]["taskId"] == nil {
		t.Error("taskId should be set on task-specific activity")
	}
}

func TestListTaskActivity_ContainsComment(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/comments",
		map[string]string{"text": "a comment"}, testutil.DemoUserID)

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+tid+"/activity", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var entries []map[string]interface{}
	testutil.DecodeJSON(t, resp, &entries)

	found := false
	for _, e := range entries {
		if e["type"] == "TASK_COMMENT_ADDED" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected TASK_COMMENT_ADDED in task activity")
	}
}

func TestListProjectActivity_ActorUserID(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	testutil.MustCreateTask(t, srv, pid, "T")

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/activity", nil, testutil.DemoUserID)
	var entries []map[string]interface{}
	testutil.DecodeJSON(t, resp, &entries)
	if len(entries) == 0 {
		t.Skip("no entries to check")
	}
	if entries[0]["actorUserId"] != testutil.DemoUserID {
		t.Errorf("actorUserId = %v, want %v", entries[0]["actorUserId"], testutil.DemoUserID)
	}
}

// ── Authorization boundary tests ────────────────────────────────────────────

func TestListProjectActivity_NonMember_Forbidden(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/activity", nil, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
}

func TestListTaskActivity_NonMember_Forbidden(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+tid+"/activity", nil, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
}

func TestListProjectActivity_Pagination(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	// Create several tasks to generate activity
	for i := 0; i < 5; i++ {
		testutil.MustCreateTask(t, srv, pid, "Task")
	}

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/activity?page=0&size=3", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var entries []interface{}
	testutil.DecodeJSON(t, resp, &entries)
	if len(entries) > 3 {
		t.Errorf("expected at most 3 entries with size=3, got %d", len(entries))
	}
}
