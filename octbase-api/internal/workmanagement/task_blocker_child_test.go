package workmanagement_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
)

// TestChangeStatus_BlockedByBlockerChild covers the rule that a task cannot be
// completed while one of its children still carries the BLOCKER priority, and
// that completing succeeds once the blocker is re-prioritized.
func TestChangeStatus_BlockedByBlockerChild(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Blockers")

	story := mustPostTask(t, srv, pid, "Story", "STORY", "")
	storyID := story["id"].(string)
	child := mustPostTask(t, srv, pid, "Child task", "TASK", storyID)
	childID := child["id"].(string)

	// Raise the child to BLOCKER priority.
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+childID+"/priority",
		map[string]interface{}{"priority": "BLOCKER"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	// Completing the parent is refused with a stable code.
	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+storyID+"/status",
		map[string]interface{}{"status": "DONE"}, testutil.DemoUserID)
	var out map[string]interface{}
	testutil.DecodeJSON(t, resp, &out)
	if resp.StatusCode != http.StatusUnprocessableEntity || out["code"] != "TASK_HAS_BLOCKER" {
		t.Fatalf("complete with blocker child: status %d code %v, want 422 TASK_HAS_BLOCKER", resp.StatusCode, out["code"])
	}

	// A non-terminal transition (e.g. IN_PROGRESS) is unaffected by the rule.
	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+storyID+"/status",
		map[string]interface{}{"status": "IN_PROGRESS"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	// Resolving the blocker (re-prioritizing the child) unblocks completion.
	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+childID+"/priority",
		map[string]interface{}{"priority": "HIGH"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+storyID+"/status",
		map[string]interface{}{"status": "DONE"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
}

// TestBulkSetStatus_BlockedByBlockerChild covers the same completion rule on the
// bulk status-change path: the whole request is refused when any target task has
// a BLOCKER child, and it goes through once the blocker is cleared.
func TestBulkSetStatus_BlockedByBlockerChild(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "BulkBlockers")

	parent := mustPostTask(t, srv, pid, "Parent", "STORY", "")
	parentID := parent["id"].(string)
	other := mustPostTask(t, srv, pid, "Unrelated", "TASK", "")
	otherID := other["id"].(string)
	child := mustPostTask(t, srv, pid, "Blocker child", "TASK", parentID)
	childID := child["id"].(string)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+childID+"/priority",
		map[string]interface{}{"priority": "BLOCKER"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	bulkDone := func(ids ...string) (int, map[string]interface{}) {
		r := testutil.Do(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/tasks/bulk", pid),
			map[string]interface{}{"taskIds": ids, "action": "set_status", "value": "DONE"}, testutil.DemoUserID)
		var o map[string]interface{}
		testutil.DecodeJSON(t, r, &o)
		return r.StatusCode, o
	}

	// A bulk complete that includes the blocked parent is refused wholesale —
	// the unrelated task must not be moved either.
	status, out := bulkDone(parentID, otherID)
	if status != http.StatusUnprocessableEntity || out["code"] != "TASK_HAS_BLOCKER" {
		t.Fatalf("bulk complete with blocker child: status %d code %v, want 422 TASK_HAS_BLOCKER", status, out["code"])
	}
	listResp := testutil.Do(t, srv, http.MethodGet, fmt.Sprintf("/api/v1/projects/%s/tasks", pid), nil, testutil.DemoUserID)
	var tasks []map[string]interface{}
	testutil.DecodeJSON(t, listResp, &tasks)
	for _, tk := range tasks {
		if tk["id"] == otherID && tk["status"] == "DONE" {
			t.Fatalf("unrelated task was completed despite the rejected bulk request")
		}
	}

	// Clearing the blocker lets the same bulk request through.
	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+childID+"/priority",
		map[string]interface{}{"priority": "MEDIUM"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	if status, out := bulkDone(parentID, otherID); status != http.StatusOK {
		t.Fatalf("bulk complete after clearing blocker: status %d body %v, want 200", status, out)
	}
}

// TestChangeStatus_DoneBlockerChildDoesNotBlock guards the guard: a child that
// still carries BLOCKER priority but is itself DONE must not hold its parent
// open. A finished blocker blocks nothing — and because priority is immutable
// on a DONE task, counting finished children permanently locked the parent out
// of DONE: the guard demanded a priority change that immutability forbade
// (found live on beyags, 2026-07-18).
func TestChangeStatus_DoneBlockerChildDoesNotBlock(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "DoneBlockers")

	story := mustPostTask(t, srv, pid, "Story", "STORY", "")
	storyID := story["id"].(string)
	child := mustPostTask(t, srv, pid, "Resolved blocker", "TASK", storyID)
	childID := child["id"].(string)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+childID+"/priority",
		map[string]interface{}{"priority": "BLOCKER"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	// Finish the child while it still carries BLOCKER priority.
	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+childID+"/status",
		map[string]interface{}{"status": "DONE"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	// The parent must complete: the only BLOCKER child is DONE.
	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+storyID+"/status",
		map[string]interface{}{"status": "DONE"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	// A second, still-open BLOCKER child keeps blocking a reopened parent —
	// the guard is scoped to open children, not removed.
	reopened := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+storyID+"/reopen", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, reopened, http.StatusOK)
	second := mustPostTask(t, srv, pid, "Live blocker", "TASK", storyID)
	secondID := second["id"].(string)
	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+secondID+"/priority",
		map[string]interface{}{"priority": "BLOCKER"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+storyID+"/status",
		map[string]interface{}{"status": "DONE"}, testutil.DemoUserID)
	var out map[string]interface{}
	testutil.DecodeJSON(t, resp, &out)
	if resp.StatusCode != http.StatusUnprocessableEntity || out["code"] != "TASK_HAS_BLOCKER" {
		t.Fatalf("open blocker child must still block: status %d code %v, want 422 TASK_HAS_BLOCKER", resp.StatusCode, out["code"])
	}
}
