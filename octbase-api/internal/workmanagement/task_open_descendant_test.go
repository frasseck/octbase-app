package workmanagement_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
)

// The completion guard looked one level down, so a BLOCKER task nested any
// deeper than a direct child slipped straight past the guard built to catch it.
// These tests pin the subtree walk that fixes it — and, just as importantly, pin
// the rule that deliberately did NOT change: an open child that is not a BLOCKER
// does not hold its parent open.

// mustCreate creates one task from a full body (testutil's helper only takes a
// title, and these tests need taskType and parentId).
func mustCreate(t *testing.T, srv *httptest.Server, pid string, body map[string]interface{}) string {
	t.Helper()
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/tasks", body, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var out map[string]interface{}
	testutil.DecodeJSON(t, resp, &out)
	return out["id"].(string)
}

// hierarchy builds epic → story → task and returns their ids. The shape matters:
// a TASK's parent must be a STORY, so this is the shortest tree that has a real
// grandchild in it — which is the whole point of these tests.
func hierarchy(t *testing.T, srv *httptest.Server, pid string) (string, string, string) {
	t.Helper()
	epic := mustCreate(t, srv, pid, map[string]interface{}{"title": "Epic", "taskType": "EPIC"})
	story := mustCreate(t, srv, pid, map[string]interface{}{"title": "Story", "taskType": "STORY", "parentId": epic})
	task := mustCreate(t, srv, pid, map[string]interface{}{"title": "Task", "taskType": "TASK", "parentId": story})
	return epic, story, task
}

func setStatus(t *testing.T, srv *httptest.Server, id, status string) *http.Response {
	t.Helper()
	return testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+id+"/status",
		map[string]interface{}{"status": status}, testutil.DemoUserID)
}

func setPriority(t *testing.T, srv *httptest.Server, id, priority string) {
	t.Helper()
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+id+"/priority",
		map[string]interface{}{"priority": priority}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
}

func assertRefusedWithBlocker(t *testing.T, resp *http.Response, what string) {
	t.Helper()
	var out map[string]interface{}
	testutil.DecodeJSON(t, resp, &out)
	if resp.StatusCode != http.StatusUnprocessableEntity || out["code"] != "TASK_HAS_BLOCKER" {
		t.Fatalf("%s: status %d code %v, want 422 TASK_HAS_BLOCKER", what, resp.StatusCode, out["code"])
	}
}

// TestCompleteTask_BlockerGrandchildBlocksTheEpic is the defect. The epic's own
// child carries no BLOCKER, so a one-level check saw nothing while a BLOCKER
// task sat one level further down — nesting alone defeated the guard.
func TestCompleteTask_BlockerGrandchildBlocksTheEpic(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	epic, _, task := hierarchy(t, srv, pid)

	setPriority(t, srv, task, "BLOCKER")
	assertRefusedWithBlocker(t, setStatus(t, srv, epic, "DONE"), "completing an epic over a BLOCKER grandchild")
}

// TestMoveTaskToDoneLane_BlockerGrandchildBlocks — the board is the second door
// to the same transition, and it carried its own copy of the weaker check.
func TestMoveTaskToDoneLane_BlockerGrandchildBlocks(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)
	testutil.MustAddColumn(t, srv, bid, "Planned", "PLANNED", 0)
	done := testutil.MustAddColumn(t, srv, bid, "Done", "DONE", 1)
	epic, _, task := hierarchy(t, srv, pid)

	setPriority(t, srv, task, "BLOCKER")
	mv := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+bid+"/move-task",
		map[string]interface{}{"taskId": epic, "boardColumnId": done, "boardRank": 1000}, testutil.DemoUserID)
	assertRefusedWithBlocker(t, mv, "dragging an epic into the Done lane over a BLOCKER grandchild")
}

// TestBulkComplete_BlockerGrandchildBlocks — the third door.
func TestBulkComplete_BlockerGrandchildBlocks(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	epic, _, task := hierarchy(t, srv, pid)

	setPriority(t, srv, task, "BLOCKER")
	resp := testutil.Do(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/tasks/bulk", pid),
		map[string]interface{}{"action": "set_status", "value": "DONE", "taskIds": []string{epic}},
		testutil.DemoUserID)
	assertRefusedWithBlocker(t, resp, "bulk-completing an epic over a BLOCKER grandchild")
}

// TestCompleteTask_OpenNonBlockerDescendantDoesNotBlock pins the rule that must
// NOT drift, and says why in the place someone would come to change it.
//
// It is tempting to read "an epic went DONE with a live task under it" as a bug
// and widen this guard to every open descendant. That would reverse a deliberate
// design: BLOCKER priority *is* this product's mechanism for "finish me before
// closing the parent" (TestChangeStatus_BlockedByBlockerChild asserts completion
// succeeds once the blocker is re-prioritized), and a wider guard is what locked
// a parent out of DONE permanently on beyags in 2026-07-18 — the incident
// TestChangeStatus_DoneBlockerChildDoesNotBlock records.
//
// Warning a user before they close a container over live children is a UI
// question. It is not this guard's job, and making it so breaks the escape hatch.
func TestCompleteTask_OpenNonBlockerDescendantDoesNotBlock(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	epic, _, task := hierarchy(t, srv, pid)

	// The grandchild is open and merely HIGH — not a blocker.
	setPriority(t, srv, task, "HIGH")
	testutil.AssertStatus(t, setStatus(t, srv, epic, "DONE"), http.StatusOK)
}

// TestCompleteTask_DoneBlockerDescendantDoesNotBlock extends the open-only scope
// down the subtree: a finished blocker blocks nothing, at any depth. Without this
// the deeper walk would re-introduce the 2026-07-18 lockout one level down —
// priority is immutable on a DONE task, so the guard would demand a change
// immutability forbids.
func TestCompleteTask_DoneBlockerDescendantDoesNotBlock(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	epic, story, task := hierarchy(t, srv, pid)

	setPriority(t, srv, task, "BLOCKER")
	testutil.AssertStatus(t, setStatus(t, srv, task, "DONE"), http.StatusOK)
	testutil.AssertStatus(t, setStatus(t, srv, story, "DONE"), http.StatusOK)
	testutil.AssertStatus(t, setStatus(t, srv, epic, "DONE"), http.StatusOK)
}

// TestCompleteTask_ArchivedBlockerDescendantDoesNotBlock — ARCHIVED counts as
// closed too, which is the escape hatch for work a team abandons (the product
// has no "won't do" status).
func TestCompleteTask_ArchivedBlockerDescendantDoesNotBlock(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	epic, _, task := hierarchy(t, srv, pid)

	setPriority(t, srv, task, "BLOCKER")
	ar := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+task+"/archive", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, ar, http.StatusOK)
	testutil.AssertStatus(t, setStatus(t, srv, epic, "DONE"), http.StatusOK)
}

// TestCompleteTask_BlockerInAnotherBranchDoesNotBlock — the walk must stay under
// the task being completed. A BLOCKER elsewhere in the project is somebody
// else's problem.
func TestCompleteTask_BlockerInAnotherBranchDoesNotBlock(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	epic, _, _ := hierarchy(t, srv, pid)

	otherEpic := mustCreate(t, srv, pid, map[string]interface{}{"title": "Other", "taskType": "EPIC"})
	otherStory := mustCreate(t, srv, pid, map[string]interface{}{"title": "OtherStory", "taskType": "STORY", "parentId": otherEpic})
	otherTask := mustCreate(t, srv, pid, map[string]interface{}{"title": "OtherTask", "taskType": "TASK", "parentId": otherStory})
	setPriority(t, srv, otherTask, "BLOCKER")

	testutil.AssertStatus(t, setStatus(t, srv, epic, "DONE"), http.StatusOK)
}
