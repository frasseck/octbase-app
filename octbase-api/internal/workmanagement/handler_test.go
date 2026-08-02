package workmanagement_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
)

// ---- Projects ----

func TestCreateProject_OK(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects",
		map[string]string{"name": "Test Project", "visibility": "PUBLIC"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)

	var p map[string]interface{}
	testutil.DecodeJSON(t, resp, &p)
	if p["name"] != "Test Project" {
		t.Errorf("name = %v, want 'Test Project'", p["name"])
	}
	if p["slug"] == "" {
		t.Error("slug should not be empty")
	}
	if p["status"] != "ACTIVE" {
		t.Errorf("status = %v, want ACTIVE", p["status"])
	}
}

func TestCreateProject_BlankName(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects",
		map[string]string{"name": "   "}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)

	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "VALIDATION_ERROR" {
		t.Errorf("code = %v, want VALIDATION_ERROR", e["code"])
	}
	details, ok := e["details"].(map[string]interface{})
	if !ok || details["field"] != "name" {
		t.Errorf("details.field = %v, want %q", e["details"], "name")
	}
}

func TestCreateProject_RejectsUnsafeAbbreviation(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	// An abbreviation carrying HTML must be rejected, not stored — it is rendered
	// into task keys across the UI (defense-in-depth against stored XSS).
	for _, bad := range []string{`<IMG SRC=x ONERROR=alert(1)>`, "AB CD", "TOOLONGABBREV"} {
		resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects",
			map[string]string{"name": "P", "abbreviation": bad}, testutil.DemoUserID)
		testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
		var e map[string]interface{}
		testutil.DecodeJSON(t, resp, &e)
		if e["code"] != "INVALID_ABBREVIATION" {
			t.Errorf("abbr %q: code = %v, want INVALID_ABBREVIATION", bad, e["code"])
		}
	}
}

func TestCreateProject_AbbreviationUppercased(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects",
		map[string]string{"name": "P", "abbreviation": "ab1"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var p map[string]interface{}
	testutil.DecodeJSON(t, resp, &p)
	if p["abbreviation"] != "AB1" {
		t.Errorf("abbreviation = %v, want AB1", p["abbreviation"])
	}
}

func TestUpdateProject_RejectsUnsafeAbbreviation(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/projects/"+pid,
		map[string]string{"abbreviation": "<b>x</b>"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "INVALID_ABBREVIATION" {
		t.Errorf("code = %v, want INVALID_ABBREVIATION", e["code"])
	}
}

func TestCreateProject_RequiresUserID(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects",
		map[string]string{"name": "No User"}, "")
	testutil.AssertStatus(t, resp, http.StatusUnauthorized)
}

func TestListProjects(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	testutil.MustCreateProject(t, srv, "Project A")
	testutil.MustCreateProject(t, srv, "Project B")

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	var ps []interface{}
	testutil.DecodeJSON(t, resp, &ps)
	if len(ps) < 2 {
		t.Errorf("expected >= 2 projects, got %d", len(ps))
	}
}

func TestGetProject_NotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/00000000-0000-0000-0000-000000000000", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)
}

func TestGetProject_Found(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Found Project")

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var p map[string]interface{}
	testutil.DecodeJSON(t, resp, &p)
	if p["id"] != pid {
		t.Errorf("id = %v, want %v", p["id"], pid)
	}
}

func TestUpdateProject(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Original")

	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/projects/"+pid,
		map[string]string{"name": "Updated", "description": "new desc"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var p map[string]interface{}
	testutil.DecodeJSON(t, resp, &p)
	if p["name"] != "Updated" {
		t.Errorf("name = %v, want Updated", p["name"])
	}
}

func TestArchiveProject(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "To Archive")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/archive", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var p map[string]interface{}
	testutil.DecodeJSON(t, resp, &p)
	if p["status"] != "ARCHIVED" {
		t.Errorf("status = %v, want ARCHIVED", p["status"])
	}
}

func TestDeleteProject(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "To Delete")
	testutil.MustCreateTask(t, srv, pid, "Orphan Task")

	resp := testutil.Do(t, srv, http.MethodDelete, "/api/v1/projects/"+pid, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNoContent)

	resp = testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)
}

func TestDeleteProject_ForbiddenForNonMember(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Protected")

	resp := testutil.Do(t, srv, http.MethodDelete, "/api/v1/projects/"+pid, nil, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
}

// ---- Tasks ----

func TestCreateTask_OK(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Task Project")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/tasks",
		map[string]string{"title": "My Task", "description": "desc"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)

	var task map[string]interface{}
	testutil.DecodeJSON(t, resp, &task)
	if task["title"] != "My Task" {
		t.Errorf("title = %v", task["title"])
	}
	if task["status"] != "PLANNED" {
		t.Errorf("status = %v, want PLANNED", task["status"])
	}
	if task["taskType"] != "TASK" {
		t.Errorf("taskType = %v, want TASK", task["taskType"])
	}
	if task["priority"] != "MEDIUM" {
		t.Errorf("priority = %v, want MEDIUM", task["priority"])
	}
	if task["reporterId"] != testutil.DemoUserID {
		t.Errorf("reporterId = %v, want %v", task["reporterId"], testutil.DemoUserID)
	}
}

func TestCreateTask_BlankTitle(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/tasks",
		map[string]string{"title": "  "}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "TASK_TITLE_REQUIRED" {
		t.Errorf("code = %v, want TASK_TITLE_REQUIRED", e["code"])
	}
	// The response must identify the offending field so the frontend can
	// associate the error with the "title" input (WCAG 3.3.1).
	details, ok := e["details"].(map[string]interface{})
	if !ok {
		t.Fatalf("details = %v, want map with field", e["details"])
	}
	if details["field"] != "title" {
		t.Errorf("details.field = %v, want %q", details["field"], "title")
	}
}

func TestListTasks_FilterByStatus(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Filter Project")
	testutil.MustCreateTask(t, srv, pid, "Planned Task")

	resp := testutil.Do(t, srv, http.MethodGet,
		fmt.Sprintf("/api/v1/projects/%s/tasks?status=PLANNED", pid), nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var tasks []map[string]interface{}
	testutil.DecodeJSON(t, resp, &tasks)
	for _, task := range tasks {
		if task["status"] != "PLANNED" {
			t.Errorf("unexpected status %v in filtered results", task["status"])
		}
	}
}

func TestListTasks_FilterByPriority(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Prio Project")

	testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/tasks",
		map[string]string{"title": "High Task", "priority": "HIGH"}, testutil.DemoUserID)

	resp := testutil.Do(t, srv, http.MethodGet,
		fmt.Sprintf("/api/v1/projects/%s/tasks?priority=HIGH", pid), nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var tasks []map[string]interface{}
	testutil.DecodeJSON(t, resp, &tasks)
	if len(tasks) == 0 {
		t.Error("expected at least one HIGH task")
	}
	for _, task := range tasks {
		if task["priority"] != "HIGH" {
			t.Errorf("unexpected priority %v", task["priority"])
		}
	}
}

func TestGetTask_NotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/00000000-0000-0000-0000-000000000000", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)
}

func TestUpdateTask_OK(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "Original")

	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/tasks/"+tid,
		map[string]string{"title": "Updated", "description": "new"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var task map[string]interface{}
	testutil.DecodeJSON(t, resp, &task)
	if task["title"] != "Updated" {
		t.Errorf("title = %v, want Updated", task["title"])
	}
}

func TestUpdateTask_ImmutableWhenDone(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "Task")

	// Set to DONE
	testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/status",
		map[string]string{"status": "DONE"}, testutil.DemoUserID)

	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/tasks/"+tid,
		map[string]string{"title": "Should Fail"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "TASK_IMMUTABLE" {
		t.Errorf("code = %v, want TASK_IMMUTABLE", e["code"])
	}
}

// A finished task's content is frozen, but its place in the hierarchy is not:
// re-parenting DONE work is how a project gets reorganized into epics and
// stories after the fact.
func TestUpdateTask_ReparentAllowedWhenDone(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "Task")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/tasks",
		map[string]string{"title": "Story", "taskType": "STORY"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var story map[string]interface{}
	testutil.DecodeJSON(t, resp, &story)
	sid := story["id"].(string)

	testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/status",
		map[string]string{"status": "DONE"}, testutil.DemoUserID)

	resp = testutil.Do(t, srv, http.MethodPatch, "/api/v1/tasks/"+tid,
		map[string]string{"parentId": sid}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	// Read back rather than trusting the response: the parent must have stuck
	// and the task must still be DONE.
	resp = testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+tid, nil, testutil.DemoUserID)
	var task map[string]interface{}
	testutil.DecodeJSON(t, resp, &task)
	if task["parentId"] != sid {
		t.Errorf("parentId = %v, want %v", task["parentId"], sid)
	}
	if task["status"] != "DONE" {
		t.Errorf("status = %v, want DONE", task["status"])
	}
}

// The carve-out is parentId only — it must not become a way to smuggle a
// content edit past immutability.
func TestUpdateTask_ReparentWithContentEditRejectedWhenDone(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "Task")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/tasks",
		map[string]string{"title": "Story", "taskType": "STORY"}, testutil.DemoUserID)
	var story map[string]interface{}
	testutil.DecodeJSON(t, resp, &story)
	sid := story["id"].(string)

	testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/status",
		map[string]string{"status": "DONE"}, testutil.DemoUserID)

	resp = testutil.Do(t, srv, http.MethodPatch, "/api/v1/tasks/"+tid,
		map[string]string{"parentId": sid, "title": "Should Fail"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "TASK_IMMUTABLE" {
		t.Errorf("code = %v, want TASK_IMMUTABLE", e["code"])
	}

	// And nothing was applied.
	resp = testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+tid, nil, testutil.DemoUserID)
	var task map[string]interface{}
	testutil.DecodeJSON(t, resp, &task)
	if task["title"] != "Task" || task["parentId"] != nil {
		t.Errorf("task was partially modified: title=%v parentId=%v", task["title"], task["parentId"])
	}
}

func TestChangeStatus(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/status",
		map[string]string{"status": "IN_PROGRESS"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var task map[string]interface{}
	testutil.DecodeJSON(t, resp, &task)
	if task["status"] != "IN_PROGRESS" {
		t.Errorf("status = %v, want IN_PROGRESS", task["status"])
	}
}

func TestChangeStatus_ImmutableWhenDone(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/status",
		map[string]string{"status": "DONE"}, testutil.DemoUserID)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/status",
		map[string]string{"status": "PLANNED"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
}

func TestChangePriority(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/priority",
		map[string]string{"priority": "CRITICAL"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var task map[string]interface{}
	testutil.DecodeJSON(t, resp, &task)
	if task["priority"] != "CRITICAL" {
		t.Errorf("priority = %v, want CRITICAL", task["priority"])
	}
}

func TestSetTaskPin(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/pin",
		map[string]bool{"pinned": true}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var task map[string]interface{}
	testutil.DecodeJSON(t, resp, &task)
	if task["pinned"] != true {
		t.Errorf("pinned = %v, want true", task["pinned"])
	}

	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/pin",
		map[string]bool{"pinned": false}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	testutil.DecodeJSON(t, resp, &task)
	if task["pinned"] != false {
		t.Errorf("pinned = %v, want false", task["pinned"])
	}
}

// Pinning is a board-organization action, so unlike content edits it must work
// even on a DONE (otherwise immutable) task — its lane can still be reorganized.
func TestSetTaskPin_AllowedWhenDone(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/status",
		map[string]string{"status": "DONE"}, testutil.DemoUserID)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/pin",
		map[string]bool{"pinned": true}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var task map[string]interface{}
	testutil.DecodeJSON(t, resp, &task)
	if task["pinned"] != true {
		t.Errorf("pinned = %v, want true on a DONE task", task["pinned"])
	}
}

func TestAssignTask(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/assign",
		map[string]string{"assigneeId": testutil.DemoUserID}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var task map[string]interface{}
	testutil.DecodeJSON(t, resp, &task)
	if task["assigneeId"] != testutil.DemoUserID {
		t.Errorf("assigneeId = %v, want %v", task["assigneeId"], testutil.DemoUserID)
	}
}

// ── Assignee/reviewer validation ─────────────────────────────────────────────

// TestAssignTask_RejectsNonMember: the API accepts exactly the set the picker
// offers. A USER who is not a member of the project is not a candidate, and
// sending their id is rejected rather than stored — previously any string was
// written verbatim and a notification row was created for it.
func TestAssignTask_RejectsNonMember(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	for _, tc := range []struct{ field, code string }{
		{"assigneeId", "ASSIGNEE_INVALID"},
		{"reviewerId", "REVIEWER_INVALID"},
	} {
		resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/assign",
			map[string]string{tc.field: testutil.SecondUserID}, testutil.DemoUserID)
		testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
		var body map[string]interface{}
		testutil.DecodeJSON(t, resp, &body)
		if body["code"] != tc.code {
			t.Errorf("%s: code = %v, want %s", tc.field, body["code"], tc.code)
		}
	}

	// The task is untouched by the rejected writes.
	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+tid, nil, testutil.DemoUserID)
	var task map[string]interface{}
	testutil.DecodeJSON(t, resp, &task)
	if task["assigneeId"] != nil || task["reviewerId"] != nil {
		t.Errorf("task was modified by a rejected assign: %v / %v", task["assigneeId"], task["reviewerId"])
	}
}

// TestAssignTask_RejectsUnknownID: a typo'd or fabricated id is refused too, so
// it can never reach the notification writer.
func TestAssignTask_RejectsUnknownID(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/assign",
		map[string]string{"assigneeId": "aaaaaaaa-0000-0000-0000-000000000099"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
}

// TestAssignTask_AcceptsGlobalAdminAndMember: both halves of the candidate set
// are accepted — a member, and a Super Admin holding no membership row.
func TestAssignTask_AcceptsGlobalAdminAndMember(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_MEMBER")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/assign",
		map[string]any{"assigneeId": testutil.SecondUserID, "reviewerId": testutil.SuperAdminUserID},
		testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var task map[string]interface{}
	testutil.DecodeJSON(t, resp, &task)
	if task["assigneeId"] != testutil.SecondUserID {
		t.Errorf("assigneeId = %v", task["assigneeId"])
	}
	if task["reviewerId"] != testutil.SuperAdminUserID {
		t.Errorf("reviewerId = %v", task["reviewerId"])
	}
}

// TestAssignTask_ClearsWithNullAndEmpty is a regression test for a silent
// no-op: with a *string field, `{"assigneeId": null}` decoded to nil, which the
// handler could not tell from "not sent", so it answered 200 and kept the old
// assignee. The UI's "Unassigned"/"None" option sends exactly that, so clearing
// either field never took effect. Both null and "" must now clear it, and must
// clear it to JSON null rather than an empty string.
func TestAssignTask_ClearsWithNullAndEmpty(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	for _, clear := range []any{nil, ""} {
		tid := testutil.MustCreateTask(t, srv, pid, "T")

		resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/assign",
			map[string]any{"assigneeId": testutil.DemoUserID, "reviewerId": testutil.DemoUserID}, testutil.DemoUserID)
		testutil.AssertStatus(t, resp, http.StatusOK)

		resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/assign",
			map[string]any{"assigneeId": clear, "reviewerId": clear}, testutil.DemoUserID)
		testutil.AssertStatus(t, resp, http.StatusOK)

		resp = testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+tid, nil, testutil.DemoUserID)
		var task map[string]interface{}
		testutil.DecodeJSON(t, resp, &task)
		if task["assigneeId"] != nil {
			t.Errorf("clear with %#v: assigneeId = %#v, want null", clear, task["assigneeId"])
		}
		if task["reviewerId"] != nil {
			t.Errorf("clear with %#v: reviewerId = %#v, want null", clear, task["reviewerId"])
		}
	}
}

// TestAssignTask_OmittedFieldIsUntouched is the other half of the same
// distinction: a body that does not mention reviewerId must leave the reviewer
// alone, rather than clearing it along with the assignee.
func TestAssignTask_OmittedFieldIsUntouched(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/assign",
		map[string]any{"reviewerId": testutil.DemoUserID}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	// Assign someone without mentioning the reviewer at all.
	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/assign",
		map[string]any{"assigneeId": testutil.DemoUserID}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var task map[string]interface{}
	testutil.DecodeJSON(t, resp, &task)
	if task["reviewerId"] != testutil.DemoUserID {
		t.Errorf("reviewerId = %#v, want it untouched by a body that omits it", task["reviewerId"])
	}
}

// TestCreateTask_EmptyAssigneeIsNull: "" on create means unassigned and is
// stored as SQL NULL, not as an empty string.
func TestCreateTask_EmptyAssigneeIsNull(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/tasks",
		map[string]any{"title": "T", "assigneeId": ""}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var task map[string]interface{}
	testutil.DecodeJSON(t, resp, &task)
	if task["assigneeId"] != nil {
		t.Errorf("assigneeId = %#v, want null", task["assigneeId"])
	}
}

// TestCreateTask_RejectsNonMemberAssignee: the create path takes assigneeId too
// and is held to the same set. It is validated before a sequence number is
// taken, so a rejected create leaves no gap in the project's task keys.
func TestCreateTask_RejectsNonMemberAssignee(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/tasks",
		map[string]any{"title": "T", "assigneeId": testutil.SecondUserID}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)

	// The next successful create still gets seq 1 — no key was burned.
	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/tasks",
		map[string]any{"title": "T"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var task map[string]interface{}
	testutil.DecodeJSON(t, resp, &task)
	if seq, ok := task["seqNumber"].(float64); !ok || seq != 1 {
		t.Errorf("seqNumber = %v, want 1 (a rejected create must not consume one)", task["seqNumber"])
	}
}

// TestUpdateTask_ReviewerPointsAtAssignEndpoint: PATCH refuses reviewerId with
// the same pointer it already gave for assigneeId, instead of the generic
// "unsupported field" that left callers guessing.
func TestUpdateTask_ReviewerPointsAtAssignEndpoint(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/tasks/"+tid,
		map[string]any{"reviewerId": testutil.DemoUserID}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusBadRequest)
	var body map[string]interface{}
	testutil.DecodeJSON(t, resp, &body)
	if msg, _ := body["message"].(string); !strings.Contains(msg, "/assign") {
		t.Errorf("message = %q, want it to point at the assign endpoint", msg)
	}
}

func TestCopyTask(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "Original")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/copy", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var copied map[string]interface{}
	testutil.DecodeJSON(t, resp, &copied)
	if copied["title"] != "Copy of Original" {
		t.Errorf("title = %v, want 'Copy of Original'", copied["title"])
	}
	if copied["status"] != "PLANNED" {
		t.Errorf("status = %v, want PLANNED", copied["status"])
	}
	if copied["id"] == tid {
		t.Error("copy should have a different id")
	}
}

func TestArchiveTask(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/archive", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var task map[string]interface{}
	testutil.DecodeJSON(t, resp, &task)
	if task["status"] != "ARCHIVED" {
		t.Errorf("status = %v, want ARCHIVED", task["status"])
	}
}

func TestReopenTask(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/archive", nil, testutil.DemoUserID)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/reopen", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var task map[string]interface{}
	testutil.DecodeJSON(t, resp, &task)
	if task["status"] != "PLANNED" {
		t.Errorf("status = %v, want PLANNED", task["status"])
	}
}

func TestSearchTasks(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	testutil.MustCreateTask(t, srv, pid, "Authentication service")
	testutil.MustCreateTask(t, srv, pid, "Database migration")

	resp := testutil.Do(t, srv, http.MethodGet,
		fmt.Sprintf("/api/v1/projects/%s/search/tasks?q=auth", pid), nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var tasks []map[string]interface{}
	testutil.DecodeJSON(t, resp, &tasks)
	if len(tasks) == 0 {
		t.Error("expected at least one result for 'auth'")
	}
	for _, task := range tasks {
		title, _ := task["title"].(string)
		desc, _ := task["description"].(string)
		if title == "" && desc == "" {
			t.Error("result has no title or description")
		}
	}
}

// ---- Task comments ----

func TestAddAndListComments(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/comments",
		map[string]string{"text": "hello comment"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var c map[string]interface{}
	testutil.DecodeJSON(t, resp, &c)
	if c["text"] != "hello comment" {
		t.Errorf("text = %v", c["text"])
	}
	if c["authorId"] != testutil.DemoUserID {
		t.Errorf("authorId = %v", c["authorId"])
	}
	// The author's display name is resolved server-side so the client never has
	// to fall back to showing the raw author ID.
	if c["authorName"] != "Demo User" {
		t.Errorf("authorName = %v, want %q", c["authorName"], "Demo User")
	}
	if c["parentId"] != nil {
		t.Errorf("top-level comment parentId = %v, want nil", c["parentId"])
	}

	resp = testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+tid+"/comments", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var cs []interface{}
	testutil.DecodeJSON(t, resp, &cs)
	if len(cs) != 1 {
		t.Errorf("expected 1 comment, got %d", len(cs))
	}
}

// TestAddComment_Reply threads a reply under a parent comment and verifies the
// parentId round-trips through both the create response and the list.
func TestAddComment_Reply(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	parentResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/comments",
		map[string]string{"text": "parent"}, testutil.DemoUserID)
	testutil.AssertStatus(t, parentResp, http.StatusCreated)
	var parent map[string]interface{}
	testutil.DecodeJSON(t, parentResp, &parent)
	parentID := parent["id"].(string)

	replyResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/comments",
		map[string]interface{}{"text": "child", "parentId": parentID}, testutil.DemoUserID)
	testutil.AssertStatus(t, replyResp, http.StatusCreated)
	var reply map[string]interface{}
	testutil.DecodeJSON(t, replyResp, &reply)
	if reply["parentId"] != parentID {
		t.Errorf("reply parentId = %v, want %s", reply["parentId"], parentID)
	}

	listResp := testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+tid+"/comments", nil, testutil.DemoUserID)
	var cs []map[string]interface{}
	testutil.DecodeJSON(t, listResp, &cs)
	if len(cs) != 2 {
		t.Fatalf("expected 2 comments, got %d", len(cs))
	}
}

// TestAddComment_RejectsForeignParent rejects a reply whose parent belongs to a
// different task.
func TestAddComment_RejectsForeignParent(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	t1 := testutil.MustCreateTask(t, srv, pid, "T1")
	t2 := testutil.MustCreateTask(t, srv, pid, "T2")

	// A comment on t1...
	r := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+t1+"/comments",
		map[string]string{"text": "on t1"}, testutil.DemoUserID)
	testutil.AssertStatus(t, r, http.StatusCreated)
	var parent map[string]interface{}
	testutil.DecodeJSON(t, r, &parent)

	// ...cannot be the parent of a comment on t2.
	bad := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+t2+"/comments",
		map[string]interface{}{"text": "on t2", "parentId": parent["id"]}, testutil.DemoUserID)
	testutil.AssertStatus(t, bad, http.StatusBadRequest)
}

// ---- Task links ----

func TestAddListDeleteLink(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/links",
		map[string]string{"url": "https://example.com", "title": "Example"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var link map[string]interface{}
	testutil.DecodeJSON(t, resp, &link)
	linkID := link["id"].(string)

	resp = testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+tid+"/links", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var links []interface{}
	testutil.DecodeJSON(t, resp, &links)
	if len(links) != 1 {
		t.Errorf("expected 1 link, got %d", len(links))
	}

	resp = testutil.Do(t, srv, http.MethodDelete, "/api/v1/tasks/"+tid+"/links/"+linkID, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNoContent)

	resp = testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+tid+"/links", nil, testutil.DemoUserID)
	var linksAfter []interface{}
	testutil.DecodeJSON(t, resp, &linksAfter)
	if len(linksAfter) != 0 {
		t.Errorf("expected 0 links after delete, got %d", len(linksAfter))
	}
}

func TestAddLink_UnsafeURLRejected(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/links",
		map[string]string{"url": "javascript:alert(1)", "title": "evil"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusBadRequest)
	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "URL_UNSAFE" {
		t.Errorf("code = %v, want URL_UNSAFE", e["code"])
	}
}

// ---- Task attachments ----

func TestAddListDeleteAttachment(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/attachments",
		map[string]interface{}{"filename": "test.pdf", "contentType": "application/pdf", "sizeBytes": 1024},
		testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var att map[string]interface{}
	testutil.DecodeJSON(t, resp, &att)
	attID := att["id"].(string)
	if att["filename"] != "test.pdf" {
		t.Errorf("filename = %v", att["filename"])
	}

	resp = testutil.Do(t, srv, http.MethodDelete, "/api/v1/tasks/"+tid+"/attachments/"+attID, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNoContent)
}

func TestAddAttachment_UnsafeExternalURLRejected(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/attachments",
		map[string]interface{}{"filename": "evil", "externalUrl": "javascript:alert(1)"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusBadRequest)
	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "URL_UNSAFE" {
		t.Errorf("code = %v, want URL_UNSAFE", e["code"])
	}
}

// ---- Task relations ----

func TestAddRelation_SelfRelationHTTP(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/relations",
		map[string]string{"targetTaskId": tid, "relationType": "BLOCKS"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "TASK_SELF_RELATION" {
		t.Errorf("code = %v, want TASK_SELF_RELATION", e["code"])
	}
}

func TestAddAndListRelations(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid1 := testutil.MustCreateTask(t, srv, pid, "T1")
	tid2 := testutil.MustCreateTask(t, srv, pid, "T2")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid1+"/relations",
		map[string]string{"targetTaskId": tid2, "relationType": "BLOCKS"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)

	// Relation symmetry: BLOCKS A→B also inserts BLOCKED_BY B→A.
	// ListRelations for A returns all rows where A is source or target,
	// so both the BLOCKS(A→B) and the BLOCKED_BY(B→A) appear.
	resp = testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+tid1+"/relations", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var rels []map[string]interface{}
	testutil.DecodeJSON(t, resp, &rels)
	if len(rels) != 2 {
		t.Errorf("expected 2 relations (BLOCKS forward + BLOCKED_BY inverse), got %d", len(rels))
	}

	// Verify T2 sees the BLOCKED_BY relation.
	resp = testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+tid2+"/relations", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var relsB []map[string]interface{}
	testutil.DecodeJSON(t, resp, &relsB)
	var foundBlockedBy bool
	for _, r := range relsB {
		if r["relationType"] == "BLOCKED_BY" {
			foundBlockedBy = true
		}
	}
	if !foundBlockedBy {
		t.Error("expected BLOCKED_BY relation on T2")
	}
}

func TestListProjectRelations(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid1 := testutil.MustCreateTask(t, srv, pid, "T1")
	tid2 := testutil.MustCreateTask(t, srv, pid, "T2")
	tid3 := testutil.MustCreateTask(t, srv, pid, "T3")

	// No relations yet: empty JSON array, not null.
	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/relations", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var rels []map[string]interface{}
	testutil.DecodeJSON(t, resp, &rels)
	if rels == nil || len(rels) != 0 {
		t.Errorf("expected empty array, got %v", rels)
	}

	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid1+"/relations",
		map[string]string{"targetTaskId": tid2, "relationType": "RELATES_TO"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid2+"/relations",
		map[string]string{"targetTaskId": tid3, "relationType": "BLOCKS"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)

	// Every relation stores a forward row plus its symmetric inverse
	// (RELATES_TO is self-inverse), so two adds yield four rows.
	resp = testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/relations", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	testutil.DecodeJSON(t, resp, &rels)
	if len(rels) != 4 {
		t.Errorf("expected 4 relations (2 adds × forward+inverse), got %d", len(rels))
	}

	// Relations in another project must not leak in.
	pid2 := testutil.MustCreateProject(t, srv, "P2")
	resp = testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid2+"/relations", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	testutil.DecodeJSON(t, resp, &rels)
	if len(rels) != 0 {
		t.Errorf("expected no relations in fresh project, got %d", len(rels))
	}
}

func TestDeleteRelation(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid1 := testutil.MustCreateTask(t, srv, pid, "T1")
	tid2 := testutil.MustCreateTask(t, srv, pid, "T2")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid1+"/relations",
		map[string]string{"targetTaskId": tid2, "relationType": "RELATES_TO"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var rel map[string]interface{}
	testutil.DecodeJSON(t, resp, &rel)

	resp = testutil.Do(t, srv, http.MethodDelete, "/api/v1/tasks/"+tid1+"/relations/"+rel["id"].(string), nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNoContent)
}

// ---- Boards ----

func TestCreateBoard(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/boards",
		map[string]interface{}{"name": "Main Board", "isDefault": true}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var b map[string]interface{}
	testutil.DecodeJSON(t, resp, &b)
	if b["name"] != "Main Board" {
		t.Errorf("name = %v", b["name"])
	}
	if b["isDefault"] != true {
		t.Errorf("isDefault = %v, want true", b["isDefault"])
	}
}

func TestGetDefaultBoard_NotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/boards/default", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)
	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "BOARD_NOT_FOUND" {
		t.Errorf("code = %v, want BOARD_NOT_FOUND", e["code"])
	}
}

func TestGetDefaultBoard_WithColumns(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)
	testutil.MustAddColumn(t, srv, bid, "Planned", "PLANNED", 0)
	testutil.MustAddColumn(t, srv, bid, "Done", "DONE", 1)

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/boards/default", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var b map[string]interface{}
	testutil.DecodeJSON(t, resp, &b)
	cols, ok := b["columns"].([]interface{})
	if !ok || len(cols) != 2 {
		t.Errorf("expected 2 columns, got %v", b["columns"])
	}
}

func TestMoveTask(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)
	colID := testutil.MustAddColumn(t, srv, bid, "Planned", "PLANNED", 0)
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+bid+"/move-task",
		map[string]interface{}{"taskId": tid, "boardColumnId": colID, "boardRank": 1000},
		testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var task map[string]interface{}
	testutil.DecodeJSON(t, resp, &task)
	if task["boardColumnId"] != colID {
		t.Errorf("boardColumnId = %v, want %v", task["boardColumnId"], colID)
	}
}

func TestRemoveTaskFromBoard(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)
	colID := testutil.MustAddColumn(t, srv, bid, "Planned", "PLANNED", 0)
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+bid+"/move-task",
		map[string]interface{}{"taskId": tid, "boardColumnId": colID, "boardRank": 1000},
		testutil.DemoUserID)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+bid+"/remove-task",
		map[string]string{"taskId": tid}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var task map[string]interface{}
	testutil.DecodeJSON(t, resp, &task)
	if task["boardColumnId"] != nil {
		t.Errorf("boardColumnId should be nil after removal, got %v", task["boardColumnId"])
	}
}

// ---- Backlog ----

func TestBacklog_ExcludesBoardTasks(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)
	colID := testutil.MustAddColumn(t, srv, bid, "Planned", "PLANNED", 0)

	backlogTaskID := testutil.MustCreateTask(t, srv, pid, "Backlog Task")
	boardTaskID := testutil.MustCreateTask(t, srv, pid, "Board Task")

	testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+bid+"/move-task",
		map[string]interface{}{"taskId": boardTaskID, "boardColumnId": colID, "boardRank": 1000},
		testutil.DemoUserID)

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/backlog", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var tasks []map[string]interface{}
	testutil.DecodeJSON(t, resp, &tasks)

	ids := map[string]bool{}
	for _, task := range tasks {
		ids[task["id"].(string)] = true
	}
	if !ids[backlogTaskID] {
		t.Error("backlog task should be in backlog")
	}
	if ids[boardTaskID] {
		t.Error("board task should not be in backlog")
	}
}

func TestBacklog_ExcludesDoneAndArchived(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	doneID := testutil.MustCreateTask(t, srv, pid, "Done Task")
	archivedID := testutil.MustCreateTask(t, srv, pid, "Archived Task")
	plannedID := testutil.MustCreateTask(t, srv, pid, "Planned Task")

	testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+doneID+"/status",
		map[string]string{"status": "DONE"}, testutil.DemoUserID)
	testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+archivedID+"/archive", nil, testutil.DemoUserID)

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/backlog", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var tasks []map[string]interface{}
	testutil.DecodeJSON(t, resp, &tasks)

	ids := map[string]bool{}
	for _, task := range tasks {
		ids[task["id"].(string)] = true
	}
	if ids[doneID] {
		t.Error("DONE task should not be in backlog")
	}
	if ids[archivedID] {
		t.Error("ARCHIVED task should not be in backlog")
	}
	if !ids[plannedID] {
		t.Error("PLANNED task should be in backlog")
	}
}

// ---- Releases ----

func TestCreateRelease(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/releases",
		map[string]interface{}{"name": "v1.0", "goal": "Ship it", "dueDate": "2025-12-31"},
		testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var m map[string]interface{}
	testutil.DecodeJSON(t, resp, &m)
	if m["name"] != "v1.0" {
		t.Errorf("name = %v", m["name"])
	}
	if m["status"] != "PLANNED" {
		t.Errorf("status = %v, want PLANNED", m["status"])
	}
}

func TestCloseRelease_HTTPWithOpenTasks(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	mid := testutil.MustCreateRelease(t, srv, pid, "v1.0")

	// Assign an open task to the release
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/tasks",
		map[string]interface{}{"title": "Open Task", "releaseId": mid}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)

	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/releases/"+mid+"/close", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "RELEASE_HAS_OPEN_TASKS" {
		t.Errorf("code = %v, want RELEASE_HAS_OPEN_TASKS", e["code"])
	}
}

func TestCloseRelease_HTTPSuccess(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	mid := testutil.MustCreateRelease(t, srv, pid, "v1.0")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/releases/"+mid+"/close", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var m map[string]interface{}
	testutil.DecodeJSON(t, resp, &m)
	if m["status"] != "CLOSED" {
		t.Errorf("status = %v, want CLOSED", m["status"])
	}
}

func TestReopenRelease(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	mid := testutil.MustCreateRelease(t, srv, pid, "v1.0")
	testutil.Do(t, srv, http.MethodPost, "/api/v1/releases/"+mid+"/close", nil, testutil.DemoUserID)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/releases/"+mid+"/reopen", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var m map[string]interface{}
	testutil.DecodeJSON(t, resp, &m)
	if m["status"] != "PLANNED" {
		t.Errorf("status = %v, want PLANNED", m["status"])
	}
}

func TestUpdateRelease(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	mid := testutil.MustCreateRelease(t, srv, pid, "v1.0")

	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/releases/"+mid,
		map[string]string{"name": "v2.0", "goal": "Ship version 2"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var m map[string]interface{}
	testutil.DecodeJSON(t, resp, &m)
	if m["name"] != "v2.0" {
		t.Errorf("name = %v, want v2.0", m["name"])
	}
}

// ---- Task categories and templates ----

func TestTaskCategories_CRUD(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/task-categories",
		map[string]string{"name": "Feature", "color": "blue"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var c map[string]interface{}
	testutil.DecodeJSON(t, resp, &c)
	cid := c["id"].(string)
	if c["name"] != "Feature" {
		t.Errorf("name = %v", c["name"])
	}

	resp = testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/task-categories", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	resp = testutil.Do(t, srv, http.MethodDelete, "/api/v1/task-categories/"+cid, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNoContent)
}

func TestTaskTemplates_CRUD(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/task-templates",
		map[string]string{"name": "Bug Report", "taskType": "TASK", "priority": "HIGH"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var tmpl map[string]interface{}
	testutil.DecodeJSON(t, resp, &tmpl)
	tmplID := tmpl["id"].(string)

	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/task-templates/"+tmplID+"/instantiate",
		map[string]string{"title": "Specific Bug"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var task map[string]interface{}
	testutil.DecodeJSON(t, resp, &task)
	if task["title"] != "Specific Bug" {
		t.Errorf("title = %v", task["title"])
	}
	if task["taskType"] != "TASK" {
		t.Errorf("taskType = %v, want TASK", task["taskType"])
	}

	resp = testutil.Do(t, srv, http.MethodDelete, "/api/v1/task-templates/"+tmplID, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNoContent)
}

// ---- Task: additional coverage ----

func TestGetTask_OK(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "Fetchable Task")

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+tid, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var task map[string]interface{}
	testutil.DecodeJSON(t, resp, &task)
	if task["id"] != tid {
		t.Errorf("id = %v, want %v", task["id"], tid)
	}
	if task["title"] != "Fetchable Task" {
		t.Errorf("title = %v", task["title"])
	}
}

func TestUpdateTask_ImmutableWhenArchived(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "Task")

	testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/archive", nil, testutil.DemoUserID)

	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/tasks/"+tid,
		map[string]string{"title": "Should Fail"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "TASK_IMMUTABLE" {
		t.Errorf("code = %v, want TASK_IMMUTABLE", e["code"])
	}
}

func TestChangeStatus_ImmutableWhenArchived(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/archive", nil, testutil.DemoUserID)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/status",
		map[string]string{"status": "PLANNED"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "TASK_IMMUTABLE" {
		t.Errorf("code = %v, want TASK_IMMUTABLE", e["code"])
	}
}

func TestListAttachments(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/attachments",
		map[string]interface{}{"filename": "one.pdf", "sizeBytes": 100}, testutil.DemoUserID)
	testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/attachments",
		map[string]interface{}{"filename": "two.pdf", "sizeBytes": 200}, testutil.DemoUserID)

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+tid+"/attachments", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var atts []interface{}
	testutil.DecodeJSON(t, resp, &atts)
	if len(atts) != 2 {
		t.Errorf("expected 2 attachments, got %d", len(atts))
	}
}

func TestCopyTask_PreservesTypeAndPriority(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/tasks",
		map[string]string{"title": "Story Task", "taskType": "STORY", "priority": "HIGH"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var original map[string]interface{}
	testutil.DecodeJSON(t, resp, &original)
	tid := original["id"].(string)

	copyResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/copy", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, copyResp, http.StatusCreated)
	var copied map[string]interface{}
	testutil.DecodeJSON(t, copyResp, &copied)

	if copied["taskType"] != "STORY" {
		t.Errorf("taskType = %v, want STORY", copied["taskType"])
	}
	if copied["priority"] != "HIGH" {
		t.Errorf("priority = %v, want HIGH", copied["priority"])
	}
	if copied["boardColumnId"] != nil {
		t.Errorf("boardColumnId should be nil for a copy, got %v", copied["boardColumnId"])
	}
}

// ---- Boards: additional coverage ----

func TestListBoards(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	testutil.MustCreateBoard(t, srv, pid)

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/boards", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var boards []interface{}
	testutil.DecodeJSON(t, resp, &boards)
	if len(boards) != 1 {
		t.Errorf("expected 1 board, got %d", len(boards))
	}
}

func TestGetBoard_ByID(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)
	testutil.MustAddColumn(t, srv, bid, "Planned", "PLANNED", 0)

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/boards/"+bid, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var b map[string]interface{}
	testutil.DecodeJSON(t, resp, &b)
	if b["id"] != bid {
		t.Errorf("id = %v, want %v", b["id"], bid)
	}
	cols, ok := b["columns"].([]interface{})
	if !ok || len(cols) != 1 {
		t.Errorf("expected 1 column in response, got %v", b["columns"])
	}
}

func TestUpdateBoard(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)

	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/boards/"+bid,
		map[string]string{"name": "Renamed Board"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var b map[string]interface{}
	testutil.DecodeJSON(t, resp, &b)
	if b["name"] != "Renamed Board" {
		t.Errorf("name = %v, want Renamed Board", b["name"])
	}
}

func TestUpdateBoardColumn(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)
	cid := testutil.MustAddColumn(t, srv, bid, "Old Name", "PLANNED", 0)

	resp := testutil.Do(t, srv, http.MethodPatch, fmt.Sprintf("/api/v1/boards/%s/columns/%s", bid, cid),
		map[string]string{"name": "New Name"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var col map[string]interface{}
	testutil.DecodeJSON(t, resp, &col)
	if col["name"] != "New Name" {
		t.Errorf("name = %v, want New Name", col["name"])
	}
}

func TestDeleteBoardColumn(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)
	// Two columns so deleting one stays at/above the default minimum of 1 lane.
	testutil.MustAddColumn(t, srv, bid, "Keep", "PLANNED", 0)
	cid := testutil.MustAddColumn(t, srv, bid, "To Delete", "DONE", 1)

	resp := testutil.Do(t, srv, http.MethodDelete, fmt.Sprintf("/api/v1/boards/%s/columns/%s", bid, cid), nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNoContent)

	// Verify the column is gone by fetching the board.
	boardResp := testutil.Do(t, srv, http.MethodGet, "/api/v1/boards/"+bid, nil, testutil.DemoUserID)
	var b map[string]interface{}
	testutil.DecodeJSON(t, boardResp, &b)
	cols, _ := b["columns"].([]interface{})
	if len(cols) != 1 {
		t.Errorf("expected 1 column after delete, got %d", len(cols))
	}
}

// ---- Releases: additional coverage ----

func TestGetRelease(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	mid := testutil.MustCreateRelease(t, srv, pid, "Get Me")

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/releases/"+mid, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var m map[string]interface{}
	testutil.DecodeJSON(t, resp, &m)
	if m["id"] != mid {
		t.Errorf("id = %v, want %v", m["id"], mid)
	}
	if m["name"] != "Get Me" {
		t.Errorf("name = %v, want Get Me", m["name"])
	}
}

func TestListReleases(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	testutil.MustCreateRelease(t, srv, pid, "MS-A")
	testutil.MustCreateRelease(t, srv, pid, "MS-B")

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/releases", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var ms []interface{}
	testutil.DecodeJSON(t, resp, &ms)
	if len(ms) != 2 {
		t.Errorf("expected 2 releases, got %d", len(ms))
	}
}

func TestGetRelease_NotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/releases/00000000-0000-0000-0000-000000000000", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)
	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "RELEASE_NOT_FOUND" {
		t.Errorf("code = %v, want RELEASE_NOT_FOUND", e["code"])
	}
}

// ---- Task filters: additional coverage ----

func TestListTasks_FilterByAssignee(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "Assigned Task")

	testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/assign",
		map[string]string{"assigneeId": testutil.DemoUserID}, testutil.DemoUserID)

	resp := testutil.Do(t, srv, http.MethodGet,
		fmt.Sprintf("/api/v1/projects/%s/tasks?assigneeId=%s", pid, testutil.DemoUserID), nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var tasks []map[string]interface{}
	testutil.DecodeJSON(t, resp, &tasks)
	if len(tasks) == 0 {
		t.Error("expected at least one task for the assigned user")
	}
	for _, task := range tasks {
		if task["assigneeId"] != testutil.DemoUserID {
			t.Errorf("unexpected assigneeId %v in filtered results", task["assigneeId"])
		}
	}
}

func TestBacklog_ReturnsEmptyWhenAllOnBoard(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)
	colID := testutil.MustAddColumn(t, srv, bid, "Planned", "PLANNED", 0)
	tid := testutil.MustCreateTask(t, srv, pid, "Only Task")

	testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+bid+"/move-task",
		map[string]interface{}{"taskId": tid, "boardColumnId": colID, "boardRank": 1000},
		testutil.DemoUserID)

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/backlog", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var tasks []interface{}
	testutil.DecodeJSON(t, resp, &tasks)
	if len(tasks) != 0 {
		t.Errorf("expected empty backlog when all tasks are on the board, got %d", len(tasks))
	}
}

// ---- ChangeStatus validation ----

// A status that no board lane defines is rejected: statuses must be a built-in
// or a custom lane name that exists in the project.
func TestChangeStatus_InvalidStatus(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/status",
		map[string]string{"status": "MADE_UP"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "INVALID_STATUS" {
		t.Errorf("code = %v, want INVALID_STATUS", e["code"])
	}
}

// A custom status becomes valid once a board lane in the project defines it.
func TestChangeStatus_CustomLaneStatusAccepted(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	bid := testutil.MustCreateBoard(t, srv, pid)
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+bid+"/columns",
		map[string]interface{}{"name": "Waiting on client", "status": "Waiting on client", "position": 5},
		testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	_ = resp.Body.Close()

	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/status",
		map[string]string{"status": "Waiting on client"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var task map[string]interface{}
	testutil.DecodeJSON(t, resp, &task)
	if task["status"] != "Waiting on client" {
		t.Errorf("status = %v, want %q", task["status"], "Waiting on client")
	}
}

func TestChangeStatus_ValidStatus(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/status",
		map[string]string{"status": "IN_PROGRESS"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var task map[string]interface{}
	testutil.DecodeJSON(t, resp, &task)
	if task["status"] != "IN_PROGRESS" {
		t.Errorf("status = %v, want IN_PROGRESS", task["status"])
	}
}

func TestChangePriority_InvalidPriority(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/priority",
		map[string]string{"priority": "ULTRA"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "INVALID_PRIORITY" {
		t.Errorf("code = %v, want INVALID_PRIORITY", e["code"])
	}
}

// ---- CopyTask seq number ----

func TestCopyTask_HasSeqNumber(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "Original")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/copy", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var task map[string]interface{}
	testutil.DecodeJSON(t, resp, &task)
	if task["seqNumber"] == nil {
		t.Error("copied task should have a seqNumber, got nil")
	}
}

// ---- ListProjects visibility ----

func TestListProjects_PrivateProjectHiddenFromNonMember(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	// DemoUser creates a PRIVATE project; SecondUser is not a member.
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects",
		map[string]string{"name": "Secret Project", "visibility": "PRIVATE"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)

	resp = testutil.Do(t, srv, http.MethodGet, "/api/v1/projects", nil, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var projects []map[string]interface{}
	testutil.DecodeJSON(t, resp, &projects)
	for _, p := range projects {
		if p["name"] == "Secret Project" {
			t.Error("PRIVATE project should not be visible to non-member")
		}
	}
}

func TestListProjects_PublicProjectHiddenFromNonMember(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects",
		map[string]string{"name": "Open Project", "visibility": "PUBLIC"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)

	// Read access requires membership: visibility no longer grants access on its
	// own, so a non-member must not see the project even when it is PUBLIC.
	resp = testutil.Do(t, srv, http.MethodGet, "/api/v1/projects", nil, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var projects []map[string]interface{}
	testutil.DecodeJSON(t, resp, &projects)
	for _, p := range projects {
		if p["name"] == "Open Project" {
			t.Error("PUBLIC project must not be visible to non-member")
		}
	}
}

// ---- UpdateComment auth ----

func TestUpdateComment_RequiresMembership(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	// DemoUser adds a comment.
	addResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/comments",
		map[string]string{"text": "hello"}, testutil.DemoUserID)
	testutil.AssertStatus(t, addResp, http.StatusCreated)
	var comment map[string]interface{}
	testutil.DecodeJSON(t, addResp, &comment)
	cid := comment["id"].(string)

	// SecondUser (not a member) attempts to update — must be 403.
	resp := testutil.Do(t, srv, http.MethodPatch,
		fmt.Sprintf("/api/v1/tasks/%s/comments/%s", tid, cid),
		map[string]string{"text": "tampered"}, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
	_ = resp.Body.Close()
}

// ---- CreateTask validation via domain ----

func TestCreateTask_BlankTitleRejected(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	resp := testutil.Do(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/tasks", pid),
		map[string]string{"title": "   "}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "TASK_TITLE_REQUIRED" {
		t.Errorf("code = %v, want TASK_TITLE_REQUIRED", e["code"])
	}
}

// ---- BulkUpdateTasks validation ----

func TestBulkUpdate_InvalidStatusRejected(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	resp := testutil.Do(t, srv, http.MethodPost,
		fmt.Sprintf("/api/v1/projects/%s/tasks/bulk", pid),
		map[string]interface{}{"taskIds": []string{tid}, "action": "set_status", "value": "FAKE_UNDEFINED"},
		testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
	_ = resp.Body.Close()
}

func TestBulkUpdate_UnknownActionRejected(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	resp := testutil.Do(t, srv, http.MethodPost,
		fmt.Sprintf("/api/v1/projects/%s/tasks/bulk", pid),
		map[string]interface{}{"taskIds": []string{tid}, "action": "nuke_em", "value": ""},
		testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusBadRequest)
	_ = resp.Body.Close()
}

// TestBulkUpdate_Delete: the "delete" bulk action removes every task in
// taskIds that belongs to the project, including their sub-resources, and
// silently skips ids that don't belong to the project.
func TestBulkUpdate_Delete(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	otherPid := testutil.MustCreateProject(t, srv, "Other")
	t1 := testutil.MustCreateTask(t, srv, pid, "T1")
	t2 := testutil.MustCreateTask(t, srv, pid, "T2")
	foreign := testutil.MustCreateTask(t, srv, otherPid, "Foreign")

	// Give t1 a comment, link, and relation to t2 to exercise the cascade.
	resp := testutil.Do(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/tasks/%s/comments", t1),
		map[string]string{"text": "hi"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	_ = resp.Body.Close()
	resp = testutil.Do(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/tasks/%s/links", t1),
		map[string]string{"url": "https://example.com", "title": "ex"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	_ = resp.Body.Close()
	resp = testutil.Do(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/tasks/%s/relations", t1),
		map[string]string{"targetTaskId": t2, "relationType": "RELATES_TO"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	_ = resp.Body.Close()

	resp = testutil.Do(t, srv, http.MethodPost,
		fmt.Sprintf("/api/v1/projects/%s/tasks/bulk", pid),
		map[string]interface{}{"taskIds": []string{t1, t2, foreign}, "action": "delete", "value": ""},
		testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var out map[string]int
	testutil.DecodeJSON(t, resp, &out)
	if out["updated"] != 2 {
		t.Errorf("updated = %d, want 2 (foreign task should be skipped)", out["updated"])
	}

	resp = testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+t1, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)
	_ = resp.Body.Close()
	resp = testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+t2, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)
	_ = resp.Body.Close()

	// The foreign task (different project) must survive.
	resp = testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+foreign, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()
}

// TestBulkUpdate_DeleteViewerForbidden: a PROJECT_VIEWER cannot use the
// "delete" bulk action.
func TestBulkUpdate_DeleteViewerForbidden(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_VIEWER")

	resp := testutil.Do(t, srv, http.MethodPost,
		fmt.Sprintf("/api/v1/projects/%s/tasks/bulk", pid),
		map[string]interface{}{"taskIds": []string{tid}, "action": "delete", "value": ""},
		testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
}

// ---- Task category auth + data-integrity tests ----

// mustCreateCategory creates a task category and returns its ID.
func mustCreateCategory(t *testing.T, srv *httptest.Server, projectID, name, description, color string) string {
	t.Helper()
	resp := testutil.Do(t, srv, http.MethodPost,
		fmt.Sprintf("/api/v1/projects/%s/task-categories", projectID),
		map[string]string{"name": name, "description": description, "color": color},
		testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var c map[string]interface{}
	testutil.DecodeJSON(t, resp, &c)
	return c["id"].(string)
}

func TestUpdateCategory_PreservesUnchangedFields(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	cid := mustCreateCategory(t, srv, pid, "Original", "keep this", "#abc")

	// PATCH with only the name — description and color must be preserved.
	resp := testutil.Do(t, srv, http.MethodPatch,
		"/api/v1/task-categories/"+cid,
		map[string]string{"name": "Updated"},
		testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	var c map[string]interface{}
	testutil.DecodeJSON(t, resp, &c)
	if c["name"] != "Updated" {
		t.Errorf("name = %v, want Updated", c["name"])
	}
	if c["description"] != "keep this" {
		t.Errorf("description = %v, want 'keep this'", c["description"])
	}
	if c["color"] != "#abc" {
		t.Errorf("color = %v, want #abc", c["color"])
	}
}

func TestUpdateCategory_StaleVersionConflict(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	cid := mustCreateCategory(t, srv, pid, "Original", "", "")

	// First edit succeeds and bumps the row's version.
	resp := testutil.Do(t, srv, http.MethodPatch,
		"/api/v1/task-categories/"+cid,
		map[string]string{"name": "First Edit"},
		testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()

	// A second edit against the now-stale row must be rejected, not silently
	// overwrite the first editor's change.
	resp = testutil.Do(t, srv, http.MethodPatch,
		"/api/v1/task-categories/"+cid,
		map[string]interface{}{"name": "Stale Edit", "version": 1},
		testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusConflict)

	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "VERSION_CONFLICT" {
		t.Errorf("code = %v, want VERSION_CONFLICT", e["code"])
	}
}

func TestUpdateCategory_NonMemberForbidden(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	cid := mustCreateCategory(t, srv, pid, "Feature", "", "")

	// SecondUserID is not a member of the project.
	resp := testutil.Do(t, srv, http.MethodPatch,
		"/api/v1/task-categories/"+cid,
		map[string]string{"name": "Hacked"},
		testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
	_ = resp.Body.Close()
}

func TestUpdateCategory_NotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPatch,
		"/api/v1/task-categories/00000000-0000-0000-0000-000000000000",
		map[string]string{"name": "Ghost"},
		testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)

	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "CATEGORY_NOT_FOUND" {
		t.Errorf("code = %v, want CATEGORY_NOT_FOUND", e["code"])
	}
}

func TestDeleteCategory_NonMemberForbidden(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	cid := mustCreateCategory(t, srv, pid, "Feature", "", "")

	resp := testutil.Do(t, srv, http.MethodDelete,
		"/api/v1/task-categories/"+cid,
		nil, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
	_ = resp.Body.Close()
}

func TestDeleteCategory_NotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodDelete,
		"/api/v1/task-categories/00000000-0000-0000-0000-000000000000",
		nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)

	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "CATEGORY_NOT_FOUND" {
		t.Errorf("code = %v, want CATEGORY_NOT_FOUND", e["code"])
	}
}

// ---- Task template auth tests ----

func TestDeleteTemplate_NonMemberForbidden(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	resp := testutil.Do(t, srv, http.MethodPost,
		fmt.Sprintf("/api/v1/projects/%s/task-templates", pid),
		map[string]string{"name": "Bug Template", "taskType": "TASK", "priority": "HIGH"},
		testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var tmpl map[string]interface{}
	testutil.DecodeJSON(t, resp, &tmpl)
	tmplID := tmpl["id"].(string)

	resp = testutil.Do(t, srv, http.MethodDelete,
		"/api/v1/task-templates/"+tmplID,
		nil, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
	_ = resp.Body.Close()
}

func TestDeleteTemplate_NotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodDelete,
		"/api/v1/task-templates/00000000-0000-0000-0000-000000000000",
		nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)

	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "TEMPLATE_NOT_FOUND" {
		t.Errorf("code = %v, want TEMPLATE_NOT_FOUND", e["code"])
	}
}

// ---- Sprints ----

func mustCreateSprint(t *testing.T, srv *httptest.Server, projectID, name string) string {
	t.Helper()
	resp := testutil.Do(t, srv, http.MethodPost,
		"/api/v1/projects/"+projectID+"/sprints",
		map[string]string{"name": name}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var s map[string]interface{}
	testutil.DecodeJSON(t, resp, &s)
	return s["id"].(string)
}

func TestCreateSprint_OK(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Sprint Project")

	resp := testutil.Do(t, srv, http.MethodPost,
		"/api/v1/projects/"+pid+"/sprints",
		map[string]string{"name": "Sprint 1", "goal": "Ship auth"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)

	var s map[string]interface{}
	testutil.DecodeJSON(t, resp, &s)
	if s["name"] != "Sprint 1" {
		t.Errorf("name = %v, want Sprint 1", s["name"])
	}
	if s["status"] != "PLANNED" {
		t.Errorf("status = %v, want PLANNED", s["status"])
	}
}

func TestCreateSprint_BlankName(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	resp := testutil.Do(t, srv, http.MethodPost,
		"/api/v1/projects/"+pid+"/sprints",
		map[string]string{"name": "  "}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
}

func TestCreateSprint_RejectsOverlap(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Overlap Project")

	first := testutil.Do(t, srv, http.MethodPost,
		"/api/v1/projects/"+pid+"/sprints",
		map[string]string{"name": "Sprint 1", "startDate": "2024-01-01", "endDate": "2024-01-14"}, testutil.DemoUserID)
	testutil.AssertStatus(t, first, http.StatusCreated)

	// Overlapping range is rejected.
	overlap := testutil.Do(t, srv, http.MethodPost,
		"/api/v1/projects/"+pid+"/sprints",
		map[string]string{"name": "Sprint 2", "startDate": "2024-01-10", "endDate": "2024-01-20"}, testutil.DemoUserID)
	testutil.AssertStatus(t, overlap, http.StatusUnprocessableEntity)
	var body map[string]interface{}
	testutil.DecodeJSON(t, overlap, &body)
	if body["code"] != "SPRINT_OVERLAP" {
		t.Errorf("code = %v, want SPRINT_OVERLAP", body["code"])
	}

	// A non-overlapping range (starting after the first ends) is allowed.
	ok := testutil.Do(t, srv, http.MethodPost,
		"/api/v1/projects/"+pid+"/sprints",
		map[string]string{"name": "Sprint 3", "startDate": "2024-01-15", "endDate": "2024-01-28"}, testutil.DemoUserID)
	testutil.AssertStatus(t, ok, http.StatusCreated)
}

func TestCreateSprint_RejectsEndBeforeStart(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Date Project")

	resp := testutil.Do(t, srv, http.MethodPost,
		"/api/v1/projects/"+pid+"/sprints",
		map[string]string{"name": "Bad Dates", "startDate": "2024-02-10", "endDate": "2024-02-01"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
}

func TestListSprints(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	mustCreateSprint(t, srv, pid, "Sprint A")
	mustCreateSprint(t, srv, pid, "Sprint B")

	resp := testutil.Do(t, srv, http.MethodGet,
		"/api/v1/projects/"+pid+"/sprints", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	var ss []interface{}
	testutil.DecodeJSON(t, resp, &ss)
	if len(ss) < 2 {
		t.Errorf("expected >= 2 sprints, got %d", len(ss))
	}
}

func TestStartSprint_Handler(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	sid := mustCreateSprint(t, srv, pid, "Sprint 1")

	resp := testutil.Do(t, srv, http.MethodPost,
		"/api/v1/sprints/"+sid+"/start", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	var s map[string]interface{}
	testutil.DecodeJSON(t, resp, &s)
	if s["status"] != "ACTIVE" {
		t.Errorf("status = %v, want ACTIVE", s["status"])
	}
}

func TestStartSprint_ConflictWithActiveSprint(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	sid1 := mustCreateSprint(t, srv, pid, "Sprint 1")
	sid2 := mustCreateSprint(t, srv, pid, "Sprint 2")

	testutil.Do(t, srv, http.MethodPost, "/api/v1/sprints/"+sid1+"/start", nil, testutil.DemoUserID)

	resp := testutil.Do(t, srv, http.MethodPost,
		"/api/v1/sprints/"+sid2+"/start", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)

	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "SPRINT_ALREADY_ACTIVE" {
		t.Errorf("code = %v, want SPRINT_ALREADY_ACTIVE", e["code"])
	}
}

func TestCompleteSprint_Handler(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	sid := mustCreateSprint(t, srv, pid, "Sprint 1")

	testutil.Do(t, srv, http.MethodPost, "/api/v1/sprints/"+sid+"/start", nil, testutil.DemoUserID)

	resp := testutil.Do(t, srv, http.MethodPost,
		"/api/v1/sprints/"+sid+"/complete", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	var s map[string]interface{}
	testutil.DecodeJSON(t, resp, &s)
	if s["status"] != "COMPLETED" {
		t.Errorf("status = %v, want COMPLETED", s["status"])
	}
}

func TestCompleteSprint_NotActive(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	sid := mustCreateSprint(t, srv, pid, "Sprint 1")

	resp := testutil.Do(t, srv, http.MethodPost,
		"/api/v1/sprints/"+sid+"/complete", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
}

func TestDeleteSprint_OK(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	sid := mustCreateSprint(t, srv, pid, "Sprint 1")

	resp := testutil.Do(t, srv, http.MethodDelete,
		"/api/v1/sprints/"+sid, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNoContent)

	resp = testutil.Do(t, srv, http.MethodGet,
		"/api/v1/sprints/"+sid, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)
}

// ---- Missing handler tests ----

func TestDeleteTask_OK(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "To be deleted")

	resp := testutil.Do(t, srv, http.MethodDelete, "/api/v1/tasks/"+tid, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNoContent)
	_ = resp.Body.Close()

	// Subsequent GET should return 404.
	resp = testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+tid, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)
	_ = resp.Body.Close()
}

// TestDeleteTask_ViewerForbidden: a PROJECT_VIEWER lacks task.delete and is
// rejected with 403.
func TestDeleteTask_ViewerForbidden(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "Protected")
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_VIEWER")

	resp := testutil.Do(t, srv, http.MethodDelete, "/api/v1/tasks/"+tid, nil, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
}

// TestDeleteTask_MemberAndAdminAndOwnerAllowed: PROJECT_MEMBER, PROJECT_ADMIN
// and PROJECT_OWNER all hold task.delete.
func TestDeleteTask_MemberAndAdminAndOwnerAllowed(t *testing.T) {
	roles := []string{"PROJECT_MEMBER", "PROJECT_ADMIN", "PROJECT_OWNER"}
	for _, role := range roles {
		t.Run(role, func(t *testing.T) {
			db := testutil.NewTestDB(t)
			srv := testutil.NewTestServer(t, db)
			pid := testutil.MustCreateProject(t, srv, "P")
			tid := testutil.MustCreateTask(t, srv, pid, "Task")
			testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, role)

			resp := testutil.Do(t, srv, http.MethodDelete, "/api/v1/tasks/"+tid, nil, testutil.SecondUserID)
			testutil.AssertStatus(t, resp, http.StatusNoContent)
		})
	}
}

// TestDeleteTask_ArchivedProjectRejected: deleting a task in an archived
// project is rejected with 409 PROJECT_ARCHIVED, but reading it still works.
func TestDeleteTask_ArchivedProjectRejected(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "Task")

	archiveResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/archive", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, archiveResp, http.StatusOK)

	resp := testutil.Do(t, srv, http.MethodDelete, "/api/v1/tasks/"+tid, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusConflict)
	var out map[string]interface{}
	testutil.DecodeJSON(t, resp, &out)
	if out["code"] != "PROJECT_ARCHIVED" {
		t.Errorf("code = %v, want PROJECT_ARCHIVED", out["code"])
	}

	// GET is unaffected (task.view is allowed even when archived).
	getResp := testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+tid, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, getResp, http.StatusOK)
}

func TestDeleteComment_OK(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	// Add a comment.
	cr := testutil.Do(t, srv, http.MethodPost,
		fmt.Sprintf("/api/v1/tasks/%s/comments", tid),
		map[string]string{"text": "Hello world"}, testutil.DemoUserID)
	testutil.AssertStatus(t, cr, http.StatusCreated)
	var comment map[string]interface{}
	testutil.DecodeJSON(t, cr, &comment)
	cid := comment["id"].(string)

	// Delete it.
	resp := testutil.Do(t, srv, http.MethodDelete,
		fmt.Sprintf("/api/v1/tasks/%s/comments/%s", tid, cid), nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNoContent)
	_ = resp.Body.Close()

	// It should no longer appear in the list.
	lr := testutil.Do(t, srv, http.MethodGet,
		fmt.Sprintf("/api/v1/tasks/%s/comments", tid), nil, testutil.DemoUserID)
	testutil.AssertStatus(t, lr, http.StatusOK)
	var comments []interface{}
	testutil.DecodeJSON(t, lr, &comments)
	for _, c := range comments {
		cm := c.(map[string]interface{})
		if cm["id"] == cid {
			t.Error("deleted comment still appears in list")
		}
	}
}

func TestDeleteRelease_OK(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	mid := testutil.MustCreateRelease(t, srv, pid, "Sprint Goal")

	resp := testutil.Do(t, srv, http.MethodDelete, "/api/v1/releases/"+mid, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNoContent)
	_ = resp.Body.Close()

	// Subsequent GET should return 404.
	resp = testutil.Do(t, srv, http.MethodGet, "/api/v1/releases/"+mid, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)
	_ = resp.Body.Close()
}

func TestGetSprint_OK(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	sid := mustCreateSprint(t, srv, pid, "Sprint Alpha")

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/sprints/"+sid, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	var s map[string]interface{}
	testutil.DecodeJSON(t, resp, &s)
	if s["id"] != sid {
		t.Errorf("id = %v, want %s", s["id"], sid)
	}
	if s["name"] != "Sprint Alpha" {
		t.Errorf("name = %v, want Sprint Alpha", s["name"])
	}
}

func TestGetSprint_NotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodGet,
		"/api/v1/sprints/00000000-0000-0000-0000-000000000000", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)

	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "SPRINT_NOT_FOUND" {
		t.Errorf("code = %v, want SPRINT_NOT_FOUND", e["code"])
	}
}

func TestUpdateSprint_OK(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	sid := mustCreateSprint(t, srv, pid, "Old Name")

	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/sprints/"+sid,
		map[string]interface{}{"name": "New Name", "goal": "New goal"},
		testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	var s map[string]interface{}
	testutil.DecodeJSON(t, resp, &s)
	if s["name"] != "New Name" {
		t.Errorf("name = %v, want New Name", s["name"])
	}
	if s["goal"] != "New goal" {
		t.Errorf("goal = %v, want New goal", s["goal"])
	}
}

func TestUpdateSprint_BlankName(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	sid := mustCreateSprint(t, srv, pid, "Sprint 1")

	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/sprints/"+sid,
		map[string]interface{}{"name": "   "},
		testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)

	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "VALIDATION_ERROR" {
		t.Errorf("code = %v, want VALIDATION_ERROR", e["code"])
	}
}

func TestInstantiateTemplate_OK(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	// Create a template.
	cr := testutil.Do(t, srv, http.MethodPost,
		fmt.Sprintf("/api/v1/projects/%s/task-templates", pid),
		map[string]interface{}{
			"name":          "Bug Report",
			"taskType":      "TASK",
			"priority":      "HIGH",
			"titleTemplate": "Bug: {title}",
		}, testutil.DemoUserID)
	testutil.AssertStatus(t, cr, http.StatusCreated)
	var tmpl map[string]interface{}
	testutil.DecodeJSON(t, cr, &tmpl)
	tmplID := tmpl["id"].(string)

	// Instantiate it.
	resp := testutil.Do(t, srv, http.MethodPost,
		fmt.Sprintf("/api/v1/task-templates/%s/instantiate", tmplID),
		map[string]string{"title": "Cannot log in"},
		testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)

	var task map[string]interface{}
	testutil.DecodeJSON(t, resp, &task)
	if task["taskType"] != "TASK" {
		t.Errorf("taskType = %v, want TASK", task["taskType"])
	}
	if task["priority"] != "HIGH" {
		t.Errorf("priority = %v, want HIGH", task["priority"])
	}
	if task["title"] != "Cannot log in" {
		t.Errorf("title = %v, want 'Cannot log in'", task["title"])
	}
	if task["projectId"] != pid {
		t.Errorf("projectId = %v, want %s", task["projectId"], pid)
	}
}

func TestInstantiateTemplate_UsesTitleTemplate(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	cr := testutil.Do(t, srv, http.MethodPost,
		fmt.Sprintf("/api/v1/projects/%s/task-templates", pid),
		map[string]interface{}{
			"name":          "Story Template",
			"taskType":      "STORY",
			"priority":      "MEDIUM",
			"titleTemplate": "Story Title from Template",
		}, testutil.DemoUserID)
	testutil.AssertStatus(t, cr, http.StatusCreated)
	var tmpl map[string]interface{}
	testutil.DecodeJSON(t, cr, &tmpl)

	// Instantiate without overriding title — should use titleTemplate.
	resp := testutil.Do(t, srv, http.MethodPost,
		fmt.Sprintf("/api/v1/task-templates/%s/instantiate", tmpl["id"].(string)),
		map[string]string{},
		testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)

	var task map[string]interface{}
	testutil.DecodeJSON(t, resp, &task)
	if task["title"] != "Story Title from Template" {
		t.Errorf("title = %v, want 'Story Title from Template'", task["title"])
	}
}

func TestAddBoardColumn_OK(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)

	resp := testutil.Do(t, srv, http.MethodPost,
		fmt.Sprintf("/api/v1/boards/%s/columns", bid),
		map[string]interface{}{"name": "QA", "status": "IN_REVIEW", "position": 3},
		testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)

	var col map[string]interface{}
	testutil.DecodeJSON(t, resp, &col)
	if col["name"] != "QA" {
		t.Errorf("name = %v, want QA", col["name"])
	}
	if col["status"] != "IN_REVIEW" {
		t.Errorf("status = %v, want IN_REVIEW", col["status"])
	}

	// Column should appear in the board GET response.
	br := testutil.Do(t, srv, http.MethodGet, "/api/v1/boards/"+bid, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, br, http.StatusOK)
	var board map[string]interface{}
	testutil.DecodeJSON(t, br, &board)
	cols, _ := board["columns"].([]interface{})
	found := false
	for _, c := range cols {
		cm := c.(map[string]interface{})
		if cm["name"] == "QA" {
			found = true
			break
		}
	}
	if !found {
		t.Error("added column not found in GET board response")
	}
}

func TestListTasks_FilterByType(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	// Create tasks of different types.
	resp1 := testutil.Do(t, srv, http.MethodPost,
		fmt.Sprintf("/api/v1/projects/%s/tasks", pid),
		map[string]interface{}{"title": "An epic", "taskType": "EPIC"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp1, http.StatusCreated)
	var t1 map[string]interface{}
	testutil.DecodeJSON(t, resp1, &t1)

	resp2 := testutil.Do(t, srv, http.MethodPost,
		fmt.Sprintf("/api/v1/projects/%s/tasks", pid),
		map[string]interface{}{"title": "A story", "taskType": "STORY"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp2, http.StatusCreated)

	// Filter by EPIC type — only the epic should appear.
	lr := testutil.Do(t, srv, http.MethodGet,
		fmt.Sprintf("/api/v1/projects/%s/tasks?type=EPIC", pid), nil, testutil.DemoUserID)
	testutil.AssertStatus(t, lr, http.StatusOK)
	var tasks []map[string]interface{}
	testutil.DecodeJSON(t, lr, &tasks)
	if len(tasks) != 1 {
		t.Errorf("expected 1 EPIC task, got %d", len(tasks))
	}
	if len(tasks) == 1 && tasks[0]["taskType"] != "EPIC" {
		t.Errorf("taskType = %v, want EPIC", tasks[0]["taskType"])
	}

	// Verify EPIC task ID matches what was created.
	if len(tasks) == 1 && tasks[0]["id"] != t1["id"] {
		t.Errorf("id = %v, want %v", tasks[0]["id"], t1["id"])
	}
}

func TestUpdateTask_ClearRelease(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	rid := testutil.MustCreateRelease(t, srv, pid, "v1.0")
	tid := testutil.MustCreateTask(t, srv, pid, "Task With Release")

	// Assign a release.
	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/tasks/"+tid,
		map[string]interface{}{"releaseId": rid}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var task map[string]interface{}
	testutil.DecodeJSON(t, resp, &task)
	if task["releaseId"] != rid {
		t.Fatalf("releaseId = %v, want %v", task["releaseId"], rid)
	}

	// Clear the release by sending null.
	resp2 := testutil.Do(t, srv, http.MethodPatch, "/api/v1/tasks/"+tid,
		map[string]interface{}{"releaseId": nil}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp2, http.StatusOK)
	var task2 map[string]interface{}
	testutil.DecodeJSON(t, resp2, &task2)
	if task2["releaseId"] != nil {
		t.Errorf("releaseId = %v after clearing, want nil", task2["releaseId"])
	}
}

func TestUpdateTask_ClearSprint(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "Task")

	// Create a sprint and assign it.
	sr := testutil.Do(t, srv, http.MethodPost,
		fmt.Sprintf("/api/v1/projects/%s/sprints", pid),
		map[string]string{"name": "Sprint 1"}, testutil.DemoUserID)
	testutil.AssertStatus(t, sr, http.StatusCreated)
	var sprint map[string]interface{}
	testutil.DecodeJSON(t, sr, &sprint)
	sid := sprint["id"].(string)

	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/tasks/"+tid,
		map[string]interface{}{"sprintId": sid}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	// Clear sprint.
	resp2 := testutil.Do(t, srv, http.MethodPatch, "/api/v1/tasks/"+tid,
		map[string]interface{}{"sprintId": nil}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp2, http.StatusOK)
	var task2 map[string]interface{}
	testutil.DecodeJSON(t, resp2, &task2)
	if task2["sprintId"] != nil {
		t.Errorf("sprintId = %v after clearing, want nil", task2["sprintId"])
	}
}

func TestCreateTask_WithDueDate(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	resp := testutil.Do(t, srv, http.MethodPost,
		fmt.Sprintf("/api/v1/projects/%s/tasks", pid),
		map[string]interface{}{"title": "Dated task", "dueDate": "2026-12-31"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var task map[string]interface{}
	testutil.DecodeJSON(t, resp, &task)
	if task["dueDate"] != "2026-12-31" {
		t.Errorf("dueDate = %v, want 2026-12-31", task["dueDate"])
	}
}

func TestUpdateTask_SetAndClearDueDate(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "Task")

	// Set due date.
	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/tasks/"+tid,
		map[string]interface{}{"dueDate": "2026-06-30"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var task map[string]interface{}
	testutil.DecodeJSON(t, resp, &task)
	if task["dueDate"] != "2026-06-30" {
		t.Errorf("dueDate = %v, want 2026-06-30", task["dueDate"])
	}

	// Clear due date.
	resp2 := testutil.Do(t, srv, http.MethodPatch, "/api/v1/tasks/"+tid,
		map[string]interface{}{"dueDate": nil}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp2, http.StatusOK)
	var task2 map[string]interface{}
	testutil.DecodeJSON(t, resp2, &task2)
	if task2["dueDate"] != nil {
		t.Errorf("dueDate = %v after clearing, want nil", task2["dueDate"])
	}
}

// ---- Auth gap tests ----

func TestListTasks_NonMemberForbidden(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	// Create a PRIVATE project as DemoUser.
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects",
		map[string]string{"name": "Private", "visibility": "PRIVATE"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var p map[string]interface{}
	testutil.DecodeJSON(t, resp, &p)
	pid := p["id"].(string)

	// SecondUser is not a member; listing tasks must return 403.
	resp2 := testutil.Do(t, srv, http.MethodGet,
		fmt.Sprintf("/api/v1/projects/%s/tasks", pid), nil, testutil.SecondUserID)
	testutil.AssertStatus(t, resp2, http.StatusForbidden)
	_ = resp2.Body.Close()
}

func TestGetBacklog_NonMemberForbidden(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects",
		map[string]string{"name": "Private", "visibility": "PRIVATE"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var p map[string]interface{}
	testutil.DecodeJSON(t, resp, &p)
	pid := p["id"].(string)

	resp2 := testutil.Do(t, srv, http.MethodGet,
		fmt.Sprintf("/api/v1/projects/%s/backlog", pid), nil, testutil.SecondUserID)
	testutil.AssertStatus(t, resp2, http.StatusForbidden)
	_ = resp2.Body.Close()
}

func TestListBoards_NonMemberForbidden(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects",
		map[string]string{"name": "Private", "visibility": "PRIVATE"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var p map[string]interface{}
	testutil.DecodeJSON(t, resp, &p)
	pid := p["id"].(string)

	resp2 := testutil.Do(t, srv, http.MethodGet,
		fmt.Sprintf("/api/v1/projects/%s/boards", pid), nil, testutil.SecondUserID)
	testutil.AssertStatus(t, resp2, http.StatusForbidden)
	_ = resp2.Body.Close()
}

// ---- Validation tests ----

func TestUpdateComment_EmptyText_Rejected(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	// Add a valid comment.
	resp := testutil.Do(t, srv, http.MethodPost,
		fmt.Sprintf("/api/v1/tasks/%s/comments", tid),
		map[string]string{"text": "original text"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var c map[string]interface{}
	testutil.DecodeJSON(t, resp, &c)
	cid := c["id"].(string)

	// Attempt to update with empty text.
	resp2 := testutil.Do(t, srv, http.MethodPatch,
		fmt.Sprintf("/api/v1/tasks/%s/comments/%s", tid, cid),
		map[string]string{"text": ""}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp2, http.StatusUnprocessableEntity)
	var e map[string]interface{}
	testutil.DecodeJSON(t, resp2, &e)
	if e["code"] != "COMMENT_INVALID" {
		t.Errorf("code = %v, want COMMENT_INVALID", e["code"])
	}
}

func TestCreateTask_InvalidTaskType_Rejected(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	resp := testutil.Do(t, srv, http.MethodPost,
		fmt.Sprintf("/api/v1/projects/%s/tasks", pid),
		map[string]string{"title": "Bad type", "taskType": "NONSENSE"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "INVALID_TASK_TYPE" {
		t.Errorf("code = %v, want INVALID_TASK_TYPE", e["code"])
	}
}

func TestCreateTask_InvalidPriority_Rejected(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	resp := testutil.Do(t, srv, http.MethodPost,
		fmt.Sprintf("/api/v1/projects/%s/tasks", pid),
		map[string]string{"title": "Bad priority", "priority": "EXTREME"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "INVALID_PRIORITY" {
		t.Errorf("code = %v, want INVALID_PRIORITY", e["code"])
	}
}

func TestUpdateTask_InvalidTaskType_Rejected(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/tasks/"+tid,
		map[string]string{"taskType": "NONSENSE"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "INVALID_TASK_TYPE" {
		t.Errorf("code = %v, want INVALID_TASK_TYPE", e["code"])
	}
}

// ── Authorization: Get by ID endpoints must enforce membership ───────────────

func TestGetBoard_NonMember_Forbidden(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	// Get the default board ID via the owner.
	lr := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/boards", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, lr, http.StatusOK)
	var boards []map[string]interface{}
	testutil.DecodeJSON(t, lr, &boards)
	if len(boards) == 0 {
		t.Skip("no boards found")
	}
	boardID := boards[0]["id"].(string)

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/boards/"+boardID, nil, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
}

func TestGetRelease_NonMember_Forbidden(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	cr := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/releases",
		map[string]interface{}{"name": "v1.0"}, testutil.DemoUserID)
	testutil.AssertStatus(t, cr, http.StatusCreated)
	var rel map[string]interface{}
	testutil.DecodeJSON(t, cr, &rel)

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/releases/"+rel["id"].(string), nil, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
}

func TestGetSprint_NonMember_Forbidden(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	cr := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/sprints",
		map[string]interface{}{"name": "Sprint 1"}, testutil.DemoUserID)
	testutil.AssertStatus(t, cr, http.StatusCreated)
	var sprint map[string]interface{}
	testutil.DecodeJSON(t, cr, &sprint)

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/sprints/"+sprint["id"].(string), nil, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
}

// A finished task's place in the backlog is not part of the historical record:
// attributing completed work to the sprint and release it belongs to is how a
// project gets reorganized after the fact. Re-parenting alone is not enough —
// an epic/story structure that finished work cannot be scheduled into only
// solves half the problem.
func TestUpdateTask_SprintAndReleaseAssignableWhenDone(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "Task")
	sprintID := mustCreateSprint(t, srv, pid, "Sprint 1")
	releaseID := testutil.MustCreateRelease(t, srv, pid, "1.0.0")

	testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/status",
		map[string]string{"status": "DONE"}, testutil.DemoUserID)

	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/tasks/"+tid,
		map[string]string{"sprintId": sprintID, "releaseId": releaseID}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	// Read back rather than trusting the response.
	resp = testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+tid, nil, testutil.DemoUserID)
	var task map[string]interface{}
	testutil.DecodeJSON(t, resp, &task)
	if task["sprintId"] != sprintID {
		t.Errorf("sprintId = %v, want %v", task["sprintId"], sprintID)
	}
	if task["releaseId"] != releaseID {
		t.Errorf("releaseId = %v, want %v", task["releaseId"], releaseID)
	}
	if task["status"] != "DONE" {
		t.Errorf("status = %v, want DONE", task["status"])
	}
}

// The widened carve-out is still placement-only: a content edit must not be
// able to ride along with a sprint assignment.
func TestUpdateTask_SprintWithContentEditRejectedWhenDone(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "Task")
	sprintID := mustCreateSprint(t, srv, pid, "Sprint 1")

	testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/status",
		map[string]string{"status": "DONE"}, testutil.DemoUserID)

	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/tasks/"+tid,
		map[string]string{"sprintId": sprintID, "description": "rewritten"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "TASK_IMMUTABLE" {
		t.Errorf("code = %v, want TASK_IMMUTABLE", e["code"])
	}

	// And nothing was applied — not even the legal half.
	resp = testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+tid, nil, testutil.DemoUserID)
	var task map[string]interface{}
	testutil.DecodeJSON(t, resp, &task)
	if task["sprintId"] != nil {
		t.Errorf("sprintId was applied despite the rejection: %v", task["sprintId"])
	}
}

// A status filter the project does not define must fail loudly. Answering it
// with an empty list is indistinguishable from "no such tasks exist", which
// turns a typo or an unsupported comma list into a confident wrong answer.
func TestListTasks_UnknownStatusFilterRejected(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	testutil.MustCreateTask(t, srv, pid, "Task")

	for _, bad := range []string{"DONE,ARCHIVED", "NOPE", "done"} {
		resp := testutil.Do(t, srv, http.MethodGet,
			"/api/v1/projects/"+pid+"/tasks?status="+url.QueryEscape(bad), nil, testutil.DemoUserID)
		testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
		var e map[string]interface{}
		testutil.DecodeJSON(t, resp, &e)
		if e["code"] != "INVALID_STATUS" {
			t.Errorf("status=%q: code = %v, want INVALID_STATUS", bad, e["code"])
		}
	}
}

// Custom lane statuses are legitimate filter values and must keep working —
// the rejection above validates against the project's statuses, not the
// built-in enum.
func TestListTasks_BuiltinAndCustomStatusFiltersAccepted(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	testutil.MustCreateTask(t, srv, pid, "Task")
	testutil.MustCreateBoard(t, srv, pid)

	resp := testutil.Do(t, srv, http.MethodGet,
		"/api/v1/projects/"+pid+"/tasks?status=PLANNED", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var ts []map[string]interface{}
	testutil.DecodeJSON(t, resp, &ts)
	if len(ts) != 1 {
		t.Errorf("PLANNED filter returned %d tasks, want 1", len(ts))
	}
}

// releaseId and sprintId are documented filters that the handler never read, so
// they silently returned the whole unfiltered project — a filter that matches
// everything is as wrong as one that matches nothing, and quieter about it.
func TestListTasks_ReleaseAndSprintFilters(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	inSprint := testutil.MustCreateTask(t, srv, pid, "In sprint")
	testutil.MustCreateTask(t, srv, pid, "Not in sprint")
	sprintID := mustCreateSprint(t, srv, pid, "Sprint 1")
	releaseID := testutil.MustCreateRelease(t, srv, pid, "1.0.0")

	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/tasks/"+inSprint,
		map[string]string{"sprintId": sprintID, "releaseId": releaseID}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	for _, f := range []struct{ name, query string }{
		{"sprintId", "sprintId=" + sprintID},
		{"releaseId", "releaseId=" + releaseID},
	} {
		resp := testutil.Do(t, srv, http.MethodGet,
			"/api/v1/projects/"+pid+"/tasks?"+f.query, nil, testutil.DemoUserID)
		testutil.AssertStatus(t, resp, http.StatusOK)
		var ts []map[string]interface{}
		testutil.DecodeJSON(t, resp, &ts)
		if len(ts) != 1 {
			t.Errorf("%s filter returned %d tasks, want 1 (filter ignored?)", f.name, len(ts))
			continue
		}
		if ts[0]["id"] != inSprint {
			t.Errorf("%s filter returned the wrong task: %v", f.name, ts[0]["id"])
		}
	}
}

// The OpenAPI spec calls it taskType; the handler only ever read `type`, so the
// documented spelling matched everything. Both must work.
func TestListTasks_TaskTypeFilterAcceptsBothSpellings(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	testutil.MustCreateTask(t, srv, pid, "A plain task")
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/tasks",
		map[string]string{"title": "An epic", "taskType": "EPIC"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)

	for _, param := range []string{"type", "taskType"} {
		resp := testutil.Do(t, srv, http.MethodGet,
			"/api/v1/projects/"+pid+"/tasks?"+param+"=EPIC", nil, testutil.DemoUserID)
		testutil.AssertStatus(t, resp, http.StatusOK)
		var ts []map[string]interface{}
		testutil.DecodeJSON(t, resp, &ts)
		if len(ts) != 1 {
			t.Errorf("?%s=EPIC returned %d tasks, want 1 (filter ignored?)", param, len(ts))
			continue
		}
		if ts[0]["taskType"] != "EPIC" {
			t.Errorf("?%s=EPIC returned taskType %v", param, ts[0]["taskType"])
		}
	}
}
