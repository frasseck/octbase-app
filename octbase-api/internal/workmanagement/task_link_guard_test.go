package workmanagement_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
)

// These tests pin the link-validation guards added by the 2026-08-02 review:
// relation targets must exist in the source task's project (previously any
// UUID was accepted — a cross-project relation leaked task IDs both ways via
// the symmetric inverse row, and a nonexistent target rode the FK into a 500
// existence oracle), and releaseId must name a release of the task's project
// on the create, PATCH and bulk doors (previously bare-TEXT release_id
// persisted anything, silently mis-counting RELEASE_HAS_OPEN_TASKS).

func errCode(t *testing.T, resp *http.Response) string {
	t.Helper()
	var out struct {
		Code string `json:"code"`
	}
	testutil.DecodeJSON(t, resp, &out)
	_ = resp.Body.Close()
	return out.Code
}

// TestAddRelation_UnknownTargetIs422Not500: a nonexistent targetTaskId used to
// hit the FK and answer 500 INTERNAL_ERROR — a cross-project task-ID existence
// oracle (500 vs 201). Now it is a stable 422.
func TestAddRelation_UnknownTargetIs422Not500(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "RelGuard")
	src := testutil.MustCreateTask(t, srv, pid, "Source")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+src+"/relations",
		map[string]any{"targetTaskId": "99999999-0000-0000-0000-000000000099", "relationType": "BLOCKS"},
		testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
	if code := errCode(t, resp); code != "TASK_NOT_FOUND" {
		t.Errorf("code = %q, want TASK_NOT_FOUND", code)
	}
}

// TestAddRelation_CrossProjectTargetRefused: the response must be identical to
// the unknown-target case (no oracle), and no inverse row may appear on the
// other project's task.
func TestAddRelation_CrossProjectTargetRefused(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "RelGuardA")
	otherPid := testutil.MustCreateProject(t, srv, "RelGuardB")
	src := testutil.MustCreateTask(t, srv, pid, "Source")
	victim := testutil.MustCreateTask(t, srv, otherPid, "Victim")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+src+"/relations",
		map[string]any{"targetTaskId": victim, "relationType": "BLOCKS"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
	if code := errCode(t, resp); code != "TASK_NOT_FOUND" {
		t.Errorf("code = %q, want TASK_NOT_FOUND (indistinguishable from unknown)", code)
	}

	var n int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM task_relations WHERE source_task_id=$1 OR target_task_id=$1`, victim,
	).Scan(&n); err != nil {
		t.Fatalf("count relations: %v", err)
	}
	if n != 0 {
		t.Errorf("victim task carries %d relation rows, want 0 (no cross-project leak)", n)
	}

	// Same project still works.
	dst := testutil.MustCreateTask(t, srv, pid, "Target")
	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+src+"/relations",
		map[string]any{"targetTaskId": dst, "relationType": "BLOCKS"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	_ = resp.Body.Close()
}

// TestReleaseAssignment_Guarded: unknown and cross-project releaseId answer
// 422 RELEASE_NOT_FOUND on PATCH and create; a project's own release links.
func TestReleaseAssignment_Guarded(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Release guard")
	otherPid := testutil.MustCreateProject(t, srv, "Other project")
	tid := testutil.MustCreateTask(t, srv, pid, "T")
	ownRel := testutil.MustCreateRelease(t, srv, pid, "R1")
	foreignRel := testutil.MustCreateRelease(t, srv, otherPid, "R2")

	for name, rid := range map[string]string{
		"unknown":       "99999999-0000-0000-0000-000000000099",
		"cross-project": foreignRel,
	} {
		resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/tasks/"+tid,
			map[string]any{"releaseId": rid}, testutil.DemoUserID)
		testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
		if code := errCode(t, resp); code != "RELEASE_NOT_FOUND" {
			t.Errorf("%s PATCH: code = %q, want RELEASE_NOT_FOUND", name, code)
		}
		resp = testutil.Do(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/tasks", pid),
			map[string]any{"title": "T " + name, "releaseId": rid}, testutil.DemoUserID)
		testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
		_ = resp.Body.Close()
	}

	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/tasks/"+tid,
		map[string]any{"releaseId": ownRel}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()
	if got := getTask(t, srv, tid)["releaseId"]; got != ownRel {
		t.Errorf("releaseId = %v, want %s", got, ownRel)
	}
}

// TestBulkSetRelease_Guarded: the bulk door validates like PATCH, applies the
// project's own release, and stores the empty value as NULL (clear).
func TestBulkSetRelease_Guarded(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "BulkRelease")
	tid := testutil.MustCreateTask(t, srv, pid, "T")
	rel := testutil.MustCreateRelease(t, srv, pid, "R")

	resp := testutil.Do(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/tasks/bulk", pid),
		map[string]any{"taskIds": []string{tid}, "action": "set_release", "value": "99999999-0000-0000-0000-000000000099"},
		testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
	if code := errCode(t, resp); code != "RELEASE_NOT_FOUND" {
		t.Errorf("code = %q, want RELEASE_NOT_FOUND", code)
	}

	if updated := doBulk(t, srv, pid, []string{tid}, "set_release", rel); updated != 1 {
		t.Fatalf("updated = %d, want 1", updated)
	}
	if got := getTask(t, srv, tid)["releaseId"]; got != rel {
		t.Errorf("releaseId = %v, want %s", got, rel)
	}

	if updated := doBulk(t, srv, pid, []string{tid}, "set_release", ""); updated != 1 {
		t.Fatalf("clear: updated = %d, want 1", updated)
	}
	var stored *string
	if err := db.QueryRow(`SELECT release_id FROM tasks WHERE id=$1`, tid).Scan(&stored); err != nil {
		t.Fatalf("read release_id: %v", err)
	}
	if stored != nil {
		t.Errorf("release_id = %q, want SQL NULL (not empty string)", *stored)
	}
}

// TestBulkSetAssignee_Guarded: the bulk door runs requireAssignable — the
// exact check whose absence once let typo'd UUIDs persist — and stores the
// empty value as NULL.
func TestBulkSetAssignee_Guarded(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "BulkAssignee")
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	resp := testutil.Do(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/tasks/bulk", pid),
		map[string]any{"taskIds": []string{tid}, "action": "set_assignee", "value": "99999999-0000-0000-0000-000000000099"},
		testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
	if code := errCode(t, resp); code != "ASSIGNEE_INVALID" {
		t.Errorf("code = %q, want ASSIGNEE_INVALID", code)
	}

	if updated := doBulk(t, srv, pid, []string{tid}, "set_assignee", testutil.DemoUserID); updated != 1 {
		t.Fatalf("updated = %d, want 1", updated)
	}
	if got := getTask(t, srv, tid)["assigneeId"]; got != testutil.DemoUserID {
		t.Errorf("assigneeId = %v, want %s", got, testutil.DemoUserID)
	}

	if updated := doBulk(t, srv, pid, []string{tid}, "set_assignee", ""); updated != 1 {
		t.Fatalf("clear: updated = %d, want 1", updated)
	}
	var stored *string
	if err := db.QueryRow(`SELECT assignee_id FROM tasks WHERE id=$1`, tid).Scan(&stored); err != nil {
		t.Fatalf("read assignee_id: %v", err)
	}
	if stored != nil {
		t.Errorf("assignee_id = %q, want SQL NULL (not empty string)", *stored)
	}
}
