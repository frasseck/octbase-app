package workmanagement_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
)

// postTask creates a task with the given type and optional parent, returning
// the response status code, the error code (for non-2xx), and the created
// task (for 201).
func postTask(t *testing.T, srv *httptest.Server, pid, title, taskType, parentID string) (int, string, map[string]interface{}) {
	t.Helper()
	body := map[string]interface{}{"title": title, "taskType": taskType}
	if parentID != "" {
		body["parentId"] = parentID
	}
	resp := testutil.Do(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/tasks", pid), body, testutil.DemoUserID)
	var out map[string]interface{}
	testutil.DecodeJSON(t, resp, &out)
	if resp.StatusCode == http.StatusCreated {
		return resp.StatusCode, "", out
	}
	code, _ := out["code"].(string)
	return resp.StatusCode, code, out
}

// mustPostTask creates a task and fails the test unless it succeeds.
func mustPostTask(t *testing.T, srv *httptest.Server, pid, title, taskType, parentID string) map[string]interface{} {
	t.Helper()
	status, code, task := postTask(t, srv, pid, title, taskType, parentID)
	if status != http.StatusCreated {
		t.Fatalf("create %s %q: status %d, code %s", taskType, title, status, code)
	}
	return task
}

// patchTask PATCHes a task and returns status + error code + body.
func patchTask(t *testing.T, srv *httptest.Server, taskID string, body map[string]interface{}) (int, string, map[string]interface{}) {
	t.Helper()
	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/tasks/"+taskID, body, testutil.DemoUserID)
	var out map[string]interface{}
	testutil.DecodeJSON(t, resp, &out)
	if resp.StatusCode == http.StatusOK {
		return resp.StatusCode, "", out
	}
	code, _ := out["code"].(string)
	return resp.StatusCode, code, out
}

func TestCreateTask_HierarchyHappyPath(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "H")

	epic := mustPostTask(t, srv, pid, "Epic", "EPIC", "")
	story := mustPostTask(t, srv, pid, "Story", "STORY", epic["id"].(string))
	task := mustPostTask(t, srv, pid, "Task", "TASK", story["id"].(string))
	sub := mustPostTask(t, srv, pid, "Subtask", "SUBTASK", task["id"].(string))

	if story["parentId"] != epic["id"] {
		t.Errorf("story parentId = %v, want %v", story["parentId"], epic["id"])
	}
	if sub["parentId"] != task["id"] {
		t.Errorf("subtask parentId = %v, want %v", sub["parentId"], task["id"])
	}
	// Stories and tasks may also live without a parent.
	mustPostTask(t, srv, pid, "Loose story", "STORY", "")
	mustPostTask(t, srv, pid, "Loose task", "TASK", "")
}

func TestCreateTask_HierarchyViolations(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "H")
	otherPid := testutil.MustCreateProject(t, srv, "Other")

	epic := mustPostTask(t, srv, pid, "Epic", "EPIC", "")
	story := mustPostTask(t, srv, pid, "Story", "STORY", epic["id"].(string))
	task := mustPostTask(t, srv, pid, "Task", "TASK", story["id"].(string))
	foreignStory := mustPostTask(t, srv, otherPid, "Foreign story", "STORY", "")

	cases := []struct {
		name, taskType, parentID, wantCode string
	}{
		{"epic with parent", "EPIC", epic["id"].(string), "TASK_PARENT_NOT_ALLOWED"},
		{"subtask without parent", "SUBTASK", "", "TASK_PARENT_REQUIRED"},
		{"subtask under story", "SUBTASK", story["id"].(string), "TASK_PARENT_TYPE_INVALID"},
		// A parent one or more levels UP is allowed (see
		// TestCreateTask_SkipLevelParents); one level DOWN never is.
		{"story under task", "STORY", task["id"].(string), "TASK_PARENT_TYPE_INVALID"},
		{"parent in other project", "TASK", foreignStory["id"].(string), "TASK_PARENT_INVALID"},
		{"unknown parent", "TASK", "00000000-0000-0000-0000-00000000dead", "TASK_PARENT_INVALID"},
	}
	for _, c := range cases {
		status, code, _ := postTask(t, srv, pid, c.name, c.taskType, c.parentID)
		if status != http.StatusUnprocessableEntity || code != c.wantCode {
			t.Errorf("%s: status %d code %s, want 422 %s", c.name, status, code, c.wantCode)
		}
	}
}

// TestCreateTask_SkipLevelParents pins the relaxation behind OCT-12: a parent
// may be any level above the child, not only the one directly above, so a task
// can sit straight under an epic without a story invented to hold it. SUBTASK
// is the exception and keeps its exactly-a-TASK parent.
func TestCreateTask_SkipLevelParents(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Skip")

	epic := mustPostTask(t, srv, pid, "Epic", "EPIC", "")
	story := mustPostTask(t, srv, pid, "Story", "STORY", epic["id"].(string))
	task := mustPostTask(t, srv, pid, "Task under story", "TASK", story["id"].(string))

	// The point of the change: a TASK directly under an EPIC.
	underEpic := mustPostTask(t, srv, pid, "Task under epic", "TASK", epic["id"].(string))
	if underEpic["parentId"] != epic["id"] {
		t.Errorf("task under epic: parentId = %v, want %v", underEpic["parentId"], epic["id"])
	}
	// Re-parenting an existing task onto an epic works the same way.
	status, code, updated := patchTask(t, srv, task["id"].(string), map[string]interface{}{"parentId": epic["id"]})
	if status != http.StatusOK || updated["parentId"] != epic["id"] {
		t.Errorf("re-parent task onto epic: status %d code %s parentId %v, want 200 and the epic", status, code, updated["parentId"])
	}

	// A SUBTASK still may not skip: its parent must be a TASK, not the epic
	// or story above it.
	for _, c := range []struct{ name, parentID string }{
		{"subtask under epic", epic["id"].(string)},
		{"subtask under story", story["id"].(string)},
	} {
		status, code, _ := postTask(t, srv, pid, c.name, "SUBTASK", c.parentID)
		if status != http.StatusUnprocessableEntity || code != "TASK_PARENT_TYPE_INVALID" {
			t.Errorf("%s: status %d code %s, want 422 TASK_PARENT_TYPE_INVALID", c.name, status, code)
		}
	}
}

// TestUpdateTask_RetypeKeepsSkipLevelChildren covers the retype guard against
// the relaxed rule: children only have to stay *allowed* under the new type,
// which is a weaker condition than being exactly one level below it.
func TestUpdateTask_RetypeKeepsSkipLevelChildren(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Retype")

	// A story holding a task; retyping the story up to an epic leaves the task
	// two levels below it, which the skip-level rule allows.
	story := mustPostTask(t, srv, pid, "Story", "STORY", "")
	child := mustPostTask(t, srv, pid, "Task", "TASK", story["id"].(string))
	status, code, updated := patchTask(t, srv, story["id"].(string), map[string]interface{}{"taskType": "EPIC"})
	if status != http.StatusOK || updated["taskType"] != "EPIC" {
		t.Fatalf("retype story to epic over a task child: status %d code %s type %v, want 200 EPIC", status, code, updated["taskType"])
	}
	// The child kept its parent rather than being stranded.
	if got := getTask(t, srv, child["id"].(string)); got["parentId"] != story["id"] {
		t.Errorf("child parentId = %v, want %v", got["parentId"], story["id"])
	}

	// Retyping *down* past a child is still refused: a subtask under the task
	// would have nothing to be a subtask of.
	task := mustPostTask(t, srv, pid, "Task2", "TASK", "")
	mustPostTask(t, srv, pid, "Sub", "SUBTASK", task["id"].(string))
	status, code, _ = patchTask(t, srv, task["id"].(string), map[string]interface{}{"taskType": "STORY"})
	if status != http.StatusUnprocessableEntity || code != "TASK_HAS_CHILDREN" {
		t.Errorf("retype task with subtask child: status %d code %s, want 422 TASK_HAS_CHILDREN", status, code)
	}
}

func TestUpdateTask_HierarchyRules(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "H")

	story := mustPostTask(t, srv, pid, "Story", "STORY", "")
	task := mustPostTask(t, srv, pid, "Task", "TASK", "")
	taskID := task["id"].(string)

	// Attach the task to the story, then detach it again with an explicit null.
	status, code, updated := patchTask(t, srv, taskID, map[string]interface{}{"parentId": story["id"]})
	if status != http.StatusOK {
		t.Fatalf("set parent: status %d code %s", status, code)
	}
	if updated["parentId"] != story["id"] {
		t.Errorf("parentId = %v, want %v", updated["parentId"], story["id"])
	}
	status, code, updated = patchTask(t, srv, taskID, map[string]interface{}{"parentId": nil})
	if status != http.StatusOK || updated["parentId"] != nil {
		t.Fatalf("clear parent: status %d code %s parentId %v", status, code, updated["parentId"])
	}

	// A subtask can never lose its parent.
	sub := mustPostTask(t, srv, pid, "Sub", "SUBTASK", taskID)
	status, code, _ = patchTask(t, srv, sub["id"].(string), map[string]interface{}{"parentId": nil})
	if status != http.StatusUnprocessableEntity || code != "TASK_PARENT_REQUIRED" {
		t.Errorf("clear subtask parent: status %d code %s, want 422 TASK_PARENT_REQUIRED", status, code)
	}

	// A type change that would strand existing children is rejected …
	status, code, _ = patchTask(t, srv, taskID, map[string]interface{}{"taskType": "STORY"})
	if status != http.StatusUnprocessableEntity || code != "TASK_HAS_CHILDREN" {
		t.Errorf("retype parent with children: status %d code %s, want 422 TASK_HAS_CHILDREN", status, code)
	}
	// … and a type change that keeps the (type, parent) pair inconsistent too.
	status, code, _ = patchTask(t, srv, sub["id"].(string), map[string]interface{}{"taskType": "STORY"})
	if status != http.StatusUnprocessableEntity || code != "TASK_PARENT_TYPE_INVALID" {
		t.Errorf("retype subtask under task: status %d code %s, want 422 TASK_PARENT_TYPE_INVALID", status, code)
	}
	// Changing type while clearing the parent in the same PATCH is fine.
	status, code, _ = patchTask(t, srv, sub["id"].(string), map[string]interface{}{"taskType": "TASK", "parentId": nil})
	if status != http.StatusOK {
		t.Errorf("retype subtask to task with parent cleared: status %d code %s, want 200", status, code)
	}
}

func TestDeleteTask_WithChildrenBlocked(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "H")

	task := mustPostTask(t, srv, pid, "Parent", "TASK", "")
	sub := mustPostTask(t, srv, pid, "Sub", "SUBTASK", task["id"].(string))

	resp := testutil.Do(t, srv, http.MethodDelete, "/api/v1/tasks/"+task["id"].(string), nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "TASK_HAS_CHILDREN" {
		t.Errorf("code = %v, want TASK_HAS_CHILDREN", e["code"])
	}

	// Children first, then the parent — both succeed.
	resp = testutil.Do(t, srv, http.MethodDelete, "/api/v1/tasks/"+sub["id"].(string), nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNoContent)
	resp = testutil.Do(t, srv, http.MethodDelete, "/api/v1/tasks/"+task["id"].(string), nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNoContent)
}

func TestListTasks_FilterByParent(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "H")

	task := mustPostTask(t, srv, pid, "Parent", "TASK", "")
	sub1 := mustPostTask(t, srv, pid, "Sub 1", "SUBTASK", task["id"].(string))
	sub2 := mustPostTask(t, srv, pid, "Sub 2", "SUBTASK", task["id"].(string))
	mustPostTask(t, srv, pid, "Unrelated", "TASK", "")

	resp := testutil.Do(t, srv, http.MethodGet,
		fmt.Sprintf("/api/v1/projects/%s/tasks?parentId=%s", pid, task["id"]), nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var tasks []map[string]interface{}
	testutil.DecodeJSON(t, resp, &tasks)
	if len(tasks) != 2 {
		t.Fatalf("expected 2 children, got %d", len(tasks))
	}
	got := map[interface{}]bool{tasks[0]["id"]: true, tasks[1]["id"]: true}
	if !got[sub1["id"]] || !got[sub2["id"]] {
		t.Errorf("children = %v, want %v and %v", got, sub1["id"], sub2["id"])
	}
}

func TestCopyTask_KeepsParent(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "H")

	task := mustPostTask(t, srv, pid, "Parent", "TASK", "")
	sub := mustPostTask(t, srv, pid, "Sub", "SUBTASK", task["id"].(string))

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+sub["id"].(string)+"/copy", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var copied map[string]interface{}
	testutil.DecodeJSON(t, resp, &copied)
	if copied["parentId"] != task["id"] {
		t.Errorf("copied parentId = %v, want %v", copied["parentId"], task["id"])
	}
}

func TestCreateTemplate_RejectsSubtask(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "H")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/task-templates",
		map[string]string{"name": "Sub template", "taskType": "SUBTASK"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "INVALID_TASK_TYPE" {
		t.Errorf("code = %v, want INVALID_TASK_TYPE", e["code"])
	}
}

// TestExportImport_ParentRoundTrip verifies the epic → story → task → subtask
// chain survives a project export/import with remapped IDs.
func TestExportImport_ParentRoundTrip(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	srcPID := testutil.MustCreateProject(t, srv, "Hierarchy Source")

	epic := mustPostTask(t, srv, srcPID, "Epic", "EPIC", "")
	story := mustPostTask(t, srv, srcPID, "Story", "STORY", epic["id"].(string))
	task := mustPostTask(t, srv, srcPID, "Task", "TASK", story["id"].(string))
	mustPostTask(t, srv, srcPID, "Subtask", "SUBTASK", task["id"].(string))

	archive := doExportProject(t, srv, srcPID)
	dstPID := testutil.MustCreateProject(t, srv, "Hierarchy Target")
	result := doImportProject(t, srv, dstPID, testutil.DemoUserID, archive, "", http.StatusOK)
	if got, _ := result["tasks"].(float64); got != 4 {
		t.Fatalf("tasks = %v, want 4 (result: %v)", result["tasks"], result)
	}

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+dstPID+"/tasks?size=50", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var tasks []map[string]interface{}
	testutil.DecodeJSON(t, resp, &tasks)
	byTitle := map[string]map[string]interface{}{}
	for _, tk := range tasks {
		byTitle[tk["title"].(string)] = tk
	}
	chain := [][2]string{{"Story", "Epic"}, {"Task", "Story"}, {"Subtask", "Task"}}
	for _, link := range chain {
		child, parent := byTitle[link[0]], byTitle[link[1]]
		if child == nil || parent == nil {
			t.Fatalf("imported tasks missing: %v", byTitle)
		}
		if child["parentId"] != parent["id"] {
			t.Errorf("%s parentId = %v, want %s's id %v", link[0], child["parentId"], link[1], parent["id"])
		}
		if child["id"] == parent["parentId"] {
			t.Errorf("unexpected cycle between %s and %s", link[0], link[1])
		}
	}
	if byTitle["Epic"]["parentId"] != nil {
		t.Errorf("epic parentId = %v, want null", byTitle["Epic"]["parentId"])
	}
}
