package workmanagement_test

import (
	"net/http"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
)

// The board side has always validated a sprint link; the task side wrote
// sprint_id with no lookup at all, so the same commitment was accepted or
// refused depending on the route taken. These pin the task side to the board's
// rules.

// An unknown sprint id must be a stable 422, not the 500 the foreign key used
// to produce. The task must be left untouched either way.
func TestTaskSprintGuard_UnknownSprint(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	const unknown = "11111111-2222-3333-4444-555555555555"

	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/tasks/"+tid,
		map[string]interface{}{"sprintId": unknown}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "SPRINT_NOT_FOUND" {
		t.Errorf("code = %v, want SPRINT_NOT_FOUND", e["code"])
	}

	get := testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+tid, nil, testutil.DemoUserID)
	var task map[string]interface{}
	testutil.DecodeJSON(t, get, &task)
	if task["sprintId"] != nil {
		t.Errorf("sprintId = %v, want nil (the rejected write must not persist)", task["sprintId"])
	}

	// The same rule at create time.
	cResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/tasks",
		map[string]interface{}{"title": "T2", "sprintId": unknown}, testutil.DemoUserID)
	testutil.AssertStatus(t, cResp, http.StatusUnprocessableEntity)
	var ce map[string]interface{}
	testutil.DecodeJSON(t, cResp, &ce)
	if ce["code"] != "SPRINT_NOT_FOUND" {
		t.Errorf("create: code = %v, want SPRINT_NOT_FOUND", ce["code"])
	}
}

// A sprint belonging to another project is not a sprint this task may join, and
// it reports SPRINT_NOT_FOUND rather than a distinct code so the response cannot
// be used to probe for sprints in projects the caller cannot see.
func TestTaskSprintGuard_ForeignProjectSprint(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pidA := testutil.MustCreateProject(t, srv, "A")
	pidB := testutil.MustCreateProject(t, srv, "B")
	sidB := mustCreateSprint(t, srv, pidB, "B's sprint")
	tid := testutil.MustCreateTask(t, srv, pidA, "T")

	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/tasks/"+tid,
		map[string]interface{}{"sprintId": sidB}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "SPRINT_NOT_FOUND" {
		t.Errorf("code = %v, want SPRINT_NOT_FOUND", e["code"])
	}
}

// The ACTIVE-sprint scope lock existed only in MoveTask, so it held only if you
// went through the board. It must hold on create and PATCH too — otherwise the
// guarantee the README states ("starting the sprint locks the scope") is one
// PATCH away from being false.
func TestTaskSprintGuard_ActiveSprintScopeLocked(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	sid := mustCreateSprint(t, srv, pid, "S")

	// A task committed while the sprint is still PLANNED is fine.
	early := testutil.MustCreateTask(t, srv, pid, "planned in")
	okResp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/tasks/"+early,
		map[string]interface{}{"sprintId": sid}, testutil.DemoUserID)
	testutil.AssertStatus(t, okResp, http.StatusOK)
	_ = okResp.Body.Close()

	startResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/sprints/"+sid+"/start", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, startResp, http.StatusOK)
	_ = startResp.Body.Close()

	// PATCH may no longer add to it.
	late := testutil.MustCreateTask(t, srv, pid, "late")
	lateResp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/tasks/"+late,
		map[string]interface{}{"sprintId": sid}, testutil.DemoUserID)
	testutil.AssertStatus(t, lateResp, http.StatusUnprocessableEntity)
	var e map[string]interface{}
	testutil.DecodeJSON(t, lateResp, &e)
	if e["code"] != "SPRINT_SCOPE_LOCKED" {
		t.Errorf("patch: code = %v, want SPRINT_SCOPE_LOCKED", e["code"])
	}

	// Nor may create.
	cResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/tasks",
		map[string]interface{}{"title": "born late", "sprintId": sid}, testutil.DemoUserID)
	testutil.AssertStatus(t, cResp, http.StatusUnprocessableEntity)
	var ce map[string]interface{}
	testutil.DecodeJSON(t, cResp, &ce)
	if ce["code"] != "SPRINT_SCOPE_LOCKED" {
		t.Errorf("create: code = %v, want SPRINT_SCOPE_LOCKED", ce["code"])
	}

	// A running sprint's scope is closed to additions, not a cage: a task
	// already in it may still be edited, and may still leave. RemoveTaskFromBoard
	// has no lock either, so the task side must not invent one.
	editResp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/tasks/"+early,
		map[string]interface{}{"sprintId": sid, "title": "renamed in flight"}, testutil.DemoUserID)
	testutil.AssertStatus(t, editResp, http.StatusOK)
	_ = editResp.Body.Close()

	outResp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/tasks/"+early,
		map[string]interface{}{"sprintId": nil}, testutil.DemoUserID)
	testutil.AssertStatus(t, outResp, http.StatusOK)
	_ = outResp.Body.Close()
}

// The counts a sprint reports must include tasks committed through the task
// side, which never place a card. This is the regression that corrupted a real
// velocity record: the snapshot is written once and cannot be recomputed.
func TestSprintCounts_IncludeTasksCommittedWithoutACard(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	sid := mustCreateSprint(t, srv, pid, "S")
	sbID, todoCol := findSprintBoard(t, srv, pid, sid)

	// One task committed the board way...
	carded := testutil.MustCreateTask(t, srv, pid, "carded")
	mResp := mustMoveOntoBoard(t, srv, sbID, todoCol, carded)
	testutil.AssertStatus(t, mResp, http.StatusOK)
	_ = mResp.Body.Close()

	// ...and two the task-panel way, which places no card.
	var uncarded []string
	for _, name := range []string{"uncarded A", "uncarded B"} {
		tid := testutil.MustCreateTask(t, srv, pid, name)
		resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/tasks/"+tid,
			map[string]interface{}{"sprintId": sid}, testutil.DemoUserID)
		testutil.AssertStatus(t, resp, http.StatusOK)
		_ = resp.Body.Close()
		uncarded = append(uncarded, tid)
	}

	doneResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+uncarded[0]+"/status",
		map[string]interface{}{"status": "DONE"}, testutil.DemoUserID)
	testutil.AssertStatus(t, doneResp, http.StatusOK)
	_ = doneResp.Body.Close()

	// The live count, before the sprint is even started.
	get := testutil.Do(t, srv, http.MethodGet, "/api/v1/sprints/"+sid, nil, testutil.DemoUserID)
	var live map[string]interface{}
	testutil.DecodeJSON(t, get, &live)
	if got := live["committedCount"]; got != float64(3) {
		t.Errorf("live committedCount = %v, want 3 (1 carded + 2 uncarded)", got)
	}

	startResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/sprints/"+sid+"/start", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, startResp, http.StatusOK)
	_ = startResp.Body.Close()

	compResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/sprints/"+sid+"/complete", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, compResp, http.StatusOK)
	var completed map[string]interface{}
	testutil.DecodeJSON(t, compResp, &completed)

	if got := completed["committedCount"]; got != float64(3) {
		t.Errorf("snapshot committedCount = %v, want 3 — this is the number that cannot be recomputed later", got)
	}
	if got := completed["completedCount"]; got != float64(1) {
		t.Errorf("snapshot completedCount = %v, want 1", got)
	}
}
