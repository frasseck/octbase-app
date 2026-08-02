package workmanagement_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
)

// ---- helpers ----

// setEstimationUnit switches the project's estimation unit as the given actor
// and returns status + error code, so both the happy and the rejected paths
// can use it.
func setEstimationUnit(t *testing.T, srv *httptest.Server, pid, unit, actorID string) (int, string) {
	t.Helper()
	status, code, _ := patchProject(t, srv, pid, map[string]interface{}{"estimationUnit": unit}, actorID)
	return status, code
}

// mustSetEstimationUnit switches the unit as the project owner and fails
// unless it took.
func mustSetEstimationUnit(t *testing.T, srv *httptest.Server, pid, unit string) {
	t.Helper()
	status, code := setEstimationUnit(t, srv, pid, unit, testutil.DemoUserID)
	if status != http.StatusOK {
		t.Fatalf("set estimationUnit=%s: status %d code %s, want 200", unit, status, code)
	}
}

// patchTask and getTask come from this package's existing test helpers.
// Every assertion about a write here goes through getTask rather than the
// write's own response body: Octbase has shipped a "200 with the value
// unchanged" bug before (clearing an assignee), and only a read-back tells the
// two apart.

// estimatedTask creates a leaf TASK in a project already switched to unit.
func estimatedTask(t *testing.T, srv *httptest.Server, pid, title string) (string, int) {
	t.Helper()
	task := mustPostTask(t, srv, pid, title, "TASK", "")
	id, _ := task["id"].(string)
	version, _ := task["version"].(float64)
	return id, int(version)
}

// ---- the project setting ----

func TestEstimation_DefaultsToNoneAndRejectsEstimates(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Estimation default")

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid, nil, testutil.DemoUserID)
	var p map[string]interface{}
	testutil.DecodeJSON(t, resp, &p)
	if p["estimationUnit"] != "NONE" {
		t.Fatalf("fresh project estimationUnit = %v, want NONE", p["estimationUnit"])
	}

	// With estimation off, neither unit is writable.
	id, version := estimatedTask(t, srv, pid, "Unestimated")
	for field, value := range map[string]interface{}{"storyPoints": 5, "estimateHours": 2.5} {
		status, code, _ := patchTask(t, srv, id, map[string]interface{}{field: value, "version": version})
		if status != http.StatusUnprocessableEntity || code != "ESTIMATION_UNIT_INACTIVE" {
			t.Errorf("%s while estimation off: status %d code %s, want 422 ESTIMATION_UNIT_INACTIVE",
				field, status, code)
		}
	}

	// The task still reads back unestimated — and both fields are present in
	// the response as explicit nulls, not omitted.
	after := getTask(t, srv, id)
	for _, field := range []string{"storyPoints", "estimateHours"} {
		if v, ok := after[field]; !ok || v != nil {
			t.Errorf("after rejected write, %s = %v (present=%v), want explicit null", field, v, ok)
		}
	}
}

func TestEstimation_UnitIsOwnerGatedAndValidated(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Estimation guards")
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_MEMBER")

	// An ordinary member may not change the setting.
	if status, _ := setEstimationUnit(t, srv, pid, "POINTS", testutil.SecondUserID); status != http.StatusForbidden {
		t.Fatalf("member setting estimationUnit: status %d, want 403", status)
	}

	// An unknown unit is a loud 422, not a silently stored string.
	status, code := setEstimationUnit(t, srv, pid, "BANANAS", testutil.DemoUserID)
	if status != http.StatusUnprocessableEntity || code != "ESTIMATION_UNIT_INVALID" {
		t.Fatalf("invalid unit: status %d code %s, want 422 ESTIMATION_UNIT_INVALID", status, code)
	}

	// The owner can, and it reads back.
	mustSetEstimationUnit(t, srv, pid, "POINTS")
	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid, nil, testutil.DemoUserID)
	var p map[string]interface{}
	testutil.DecodeJSON(t, resp, &p)
	if p["estimationUnit"] != "POINTS" {
		t.Fatalf("after owner switch, estimationUnit = %v, want POINTS", p["estimationUnit"])
	}
}

func TestEstimation_UnitChangeIsLoggedOnTheProject(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Estimation activity")
	mustSetEstimationUnit(t, srv, pid, "HOURS")

	resp := testutil.Do(t, srv, http.MethodGet,
		fmt.Sprintf("/api/v1/projects/%s/activity", pid), nil, testutil.DemoUserID)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("project activity: status %d", resp.StatusCode)
	}
	var entries []map[string]interface{}
	testutil.DecodeJSON(t, resp, &entries)
	for _, e := range entries {
		if e["type"] == "PROJECT_ESTIMATION_UNIT_CHANGED" {
			return
		}
	}
	t.Fatalf("PROJECT_ESTIMATION_UNIT_CHANGED missing from project activity: %v", entries)
}

// ---- the estimate on the task ----

func TestEstimation_StoryPointsRoundTripClearAndValidate(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Points")
	mustSetEstimationUnit(t, srv, pid, "POINTS")
	id, version := estimatedTask(t, srv, pid, "Estimate me")

	// Round trip: PATCH → GET.
	status, code, out := patchTask(t, srv, id, map[string]interface{}{"storyPoints": 8, "version": version})
	if status != http.StatusOK {
		t.Fatalf("set storyPoints: status %d code %s", status, code)
	}
	version = int(out["version"].(float64))
	if got := getTask(t, srv, id)["storyPoints"]; got != float64(8) {
		t.Fatalf("storyPoints read back = %v, want 8", got)
	}

	// 0 is a deliberate estimate, not "unestimated".
	_, _, out = patchTask(t, srv, id, map[string]interface{}{"storyPoints": 0, "version": version})
	version = int(out["version"].(float64))
	if got := getTask(t, srv, id)["storyPoints"]; got != float64(0) {
		t.Fatalf("storyPoints 0 read back = %v, want 0", got)
	}

	// Clearing must actually clear — proven by read-back, not by the 200.
	status, code, out = patchTask(t, srv, id, map[string]interface{}{"storyPoints": nil, "version": version})
	if status != http.StatusOK {
		t.Fatalf("clear storyPoints: status %d code %s", status, code)
	}
	version = int(out["version"].(float64))
	if got, ok := getTask(t, srv, id)["storyPoints"]; !ok || got != nil {
		t.Fatalf("cleared storyPoints read back = %v (present=%v), want null", got, ok)
	}

	// Out-of-range values are rejected with the stable code.
	for _, bad := range []int{-1, 101} {
		status, code, _ := patchTask(t, srv, id, map[string]interface{}{"storyPoints": bad, "version": version})
		if status != http.StatusUnprocessableEntity || code != "STORY_POINTS_INVALID" {
			t.Errorf("storyPoints %d: status %d code %s, want 422 STORY_POINTS_INVALID", bad, status, code)
		}
	}

	// The inactive unit is refused even though the project does estimate.
	status, code, _ = patchTask(t, srv, id, map[string]interface{}{"estimateHours": 3, "version": version})
	if status != http.StatusUnprocessableEntity || code != "ESTIMATION_UNIT_INACTIVE" {
		t.Errorf("estimateHours under POINTS: status %d code %s, want 422 ESTIMATION_UNIT_INACTIVE", status, code)
	}

	// A stale version still gets the ordinary optimistic-locking answer.
	status, code, _ = patchTask(t, srv, id, map[string]interface{}{"storyPoints": 3, "version": version - 1})
	if status != http.StatusConflict || code != "VERSION_CONFLICT" {
		t.Errorf("stale version: status %d code %s, want 409 VERSION_CONFLICT", status, code)
	}
}

func TestEstimation_HoursRoundTripAndValidate(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Hours")
	mustSetEstimationUnit(t, srv, pid, "HOURS")
	id, version := estimatedTask(t, srv, pid, "Quote me")

	status, code, out := patchTask(t, srv, id, map[string]interface{}{"estimateHours": 7.25, "version": version})
	if status != http.StatusOK {
		t.Fatalf("set estimateHours: status %d code %s", status, code)
	}
	version = int(out["version"].(float64))
	if got := getTask(t, srv, id)["estimateHours"]; got != 7.25 {
		t.Fatalf("estimateHours read back = %v, want 7.25", got)
	}

	// Clearing clears.
	_, _, out = patchTask(t, srv, id, map[string]interface{}{"estimateHours": nil, "version": version})
	version = int(out["version"].(float64))
	if got, ok := getTask(t, srv, id)["estimateHours"]; !ok || got != nil {
		t.Fatalf("cleared estimateHours read back = %v (present=%v), want null", got, ok)
	}

	// Range and precision: three decimals would be rounded away by the
	// NUMERIC(7,2) column, so it is refused rather than silently altered.
	for _, bad := range []float64{-0.5, 1000.01, 1.005} {
		status, code, _ := patchTask(t, srv, id, map[string]interface{}{"estimateHours": bad, "version": version})
		if status != http.StatusUnprocessableEntity || code != "ESTIMATE_HOURS_INVALID" {
			t.Errorf("estimateHours %v: status %d code %s, want 422 ESTIMATE_HOURS_INVALID", bad, status, code)
		}
	}

	// The other unit is inactive here.
	status, code, _ = patchTask(t, srv, id, map[string]interface{}{"storyPoints": 5, "version": version})
	if status != http.StatusUnprocessableEntity || code != "ESTIMATION_UNIT_INACTIVE" {
		t.Errorf("storyPoints under HOURS: status %d code %s, want 422 ESTIMATION_UNIT_INACTIVE", status, code)
	}
}

func TestEstimation_SwitchingUnitsKeepsBothValues(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Non-destructive")
	mustSetEstimationUnit(t, srv, pid, "POINTS")
	id, version := estimatedTask(t, srv, pid, "Both units")

	_, _, out := patchTask(t, srv, id, map[string]interface{}{"storyPoints": 13, "version": version})
	version = int(out["version"].(float64))

	// POINTS → HOURS: the points value goes dormant, not away, and hours can
	// now be written alongside it.
	mustSetEstimationUnit(t, srv, pid, "HOURS")
	if got := getTask(t, srv, id)["storyPoints"]; got != float64(13) {
		t.Fatalf("after switch to HOURS, storyPoints = %v, want 13 (dormant, not destroyed)", got)
	}
	_, _, out = patchTask(t, srv, id, map[string]interface{}{"estimateHours": 20.5, "version": version})
	version = int(out["version"].(float64))

	// → NONE → back to POINTS: both survive the whole trip.
	mustSetEstimationUnit(t, srv, pid, "NONE")
	mustSetEstimationUnit(t, srv, pid, "POINTS")
	final := getTask(t, srv, id)
	if final["storyPoints"] != float64(13) {
		t.Errorf("after POINTS→HOURS→NONE→POINTS, storyPoints = %v, want 13", final["storyPoints"])
	}
	if final["estimateHours"] != 20.5 {
		t.Errorf("after POINTS→HOURS→NONE→POINTS, estimateHours = %v, want 20.5", final["estimateHours"])
	}
	_ = version
}

func TestEstimation_ClearingIsAllowedWhileTheUnitIsDormant(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Dormant clear")
	mustSetEstimationUnit(t, srv, pid, "POINTS")
	id, version := estimatedTask(t, srv, pid, "Wrong number")

	_, _, out := patchTask(t, srv, id, map[string]interface{}{"storyPoints": 40, "version": version})
	version = int(out["version"].(float64))

	// Switching away must not strand a wrong estimate forever: setting a value
	// in a dormant unit is refused, but removing one stays possible.
	mustSetEstimationUnit(t, srv, pid, "HOURS")
	status, code, _ := patchTask(t, srv, id, map[string]interface{}{"storyPoints": 41, "version": version})
	if status != http.StatusUnprocessableEntity || code != "ESTIMATION_UNIT_INACTIVE" {
		t.Fatalf("setting a dormant estimate: status %d code %s, want 422 ESTIMATION_UNIT_INACTIVE", status, code)
	}
	status, code, _ = patchTask(t, srv, id, map[string]interface{}{"storyPoints": nil, "version": version})
	if status != http.StatusOK {
		t.Fatalf("clearing a dormant estimate: status %d code %s, want 200", status, code)
	}
	if got, ok := getTask(t, srv, id)["storyPoints"]; !ok || got != nil {
		t.Fatalf("dormant storyPoints after clear = %v (present=%v), want null", got, ok)
	}
}

func TestEstimation_ContainerTypesCannotCarryAnEstimate(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Containers")
	mustSetEstimationUnit(t, srv, pid, "POINTS")

	// Directly estimating an EPIC is refused.
	epic := mustPostTask(t, srv, pid, "Container", "EPIC", "")
	epicID, _ := epic["id"].(string)
	epicVersion := int(epic["version"].(float64))
	status, code, _ := patchTask(t, srv, epicID, map[string]interface{}{"storyPoints": 5, "version": epicVersion})
	if status != http.StatusUnprocessableEntity || code != "ESTIMATION_NOT_ALLOWED_FOR_TYPE" {
		t.Errorf("estimate on EPIC: status %d code %s, want 422 ESTIMATION_NOT_ALLOWED_FOR_TYPE", status, code)
	}

	// And so is the back door: retyping an already-estimated TASK into a
	// container, which never names an estimate in the request at all.
	id, version := estimatedTask(t, srv, pid, "Estimated leaf")
	_, _, out := patchTask(t, srv, id, map[string]interface{}{"storyPoints": 3, "version": version})
	version = int(out["version"].(float64))
	status, code, _ = patchTask(t, srv, id, map[string]interface{}{"taskType": "EPIC", "version": version})
	if status != http.StatusUnprocessableEntity || code != "ESTIMATION_NOT_ALLOWED_FOR_TYPE" {
		t.Errorf("retype estimated TASK to EPIC: status %d code %s, want 422 ESTIMATION_NOT_ALLOWED_FOR_TYPE", status, code)
	}

	// Clearing the estimate first makes the same retype succeed.
	_, _, out = patchTask(t, srv, id, map[string]interface{}{"storyPoints": nil, "version": version})
	version = int(out["version"].(float64))
	if status, code, _ := patchTask(t, srv, id, map[string]interface{}{"taskType": "EPIC", "version": version}); status != http.StatusOK {
		t.Errorf("retype after clearing: status %d code %s, want 200", status, code)
	}
}

func TestEstimation_ChangeIsLoggedOnTheTask(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Task activity")
	mustSetEstimationUnit(t, srv, pid, "POINTS")
	id, version := estimatedTask(t, srv, pid, "Logged")

	if status, code, _ := patchTask(t, srv, id, map[string]interface{}{"storyPoints": 5, "version": version}); status != http.StatusOK {
		t.Fatalf("set storyPoints: status %d code %s", status, code)
	}
	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+id+"/activity", nil, testutil.DemoUserID)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("task activity: status %d", resp.StatusCode)
	}
	var entries []map[string]interface{}
	testutil.DecodeJSON(t, resp, &entries)
	for _, e := range entries {
		if e["type"] == "TASK_UPDATED" {
			return
		}
	}
	t.Fatalf("estimate change left no TASK_UPDATED activity: %v", entries)
}

func TestEstimation_CopyCarriesTheEstimate(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Copy")
	mustSetEstimationUnit(t, srv, pid, "POINTS")
	id, version := estimatedTask(t, srv, pid, "Original")
	if status, code, _ := patchTask(t, srv, id, map[string]interface{}{"storyPoints": 21, "version": version}); status != http.StatusOK {
		t.Fatalf("set storyPoints: status %d code %s", status, code)
	}

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+id+"/copy", nil, testutil.DemoUserID)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("copy task: status %d", resp.StatusCode)
	}
	var cp map[string]interface{}
	testutil.DecodeJSON(t, resp, &cp)
	copyID, _ := cp["id"].(string)
	if got := getTask(t, srv, copyID)["storyPoints"]; got != float64(21) {
		t.Fatalf("copied task storyPoints = %v, want 21", got)
	}
}

// ---- estimates supplied at create (OCT-199) ----

// postTaskBody creates a task from a raw body so a test can send fields the
// postTask helper does not model.
func postTaskBody(t *testing.T, srv *httptest.Server, pid string, body map[string]interface{}) (int, string, map[string]interface{}) {
	t.Helper()
	resp := testutil.Do(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/tasks", pid), body, testutil.DemoUserID)
	var out map[string]interface{}
	testutil.DecodeJSON(t, resp, &out)
	if resp.StatusCode == http.StatusCreated {
		return resp.StatusCode, "", out
	}
	code, _ := out["code"].(string)
	return resp.StatusCode, code, out
}

func TestEstimation_CreateAcceptsAndPersistsTheEstimate(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Create with estimate")
	mustSetEstimationUnit(t, srv, pid, "POINTS")

	status, code, created := postTaskBody(t, srv, pid,
		map[string]interface{}{"title": "Estimated at birth", "taskType": "TASK", "storyPoints": 8})
	if status != http.StatusCreated {
		t.Fatalf("create with storyPoints: status %d code %s, want 201", status, code)
	}
	// The response must not claim a value the row does not hold — read it back.
	id, _ := created["id"].(string)
	if got := getTask(t, srv, id)["storyPoints"]; got != float64(8) {
		t.Fatalf("storyPoints read back after create = %v, want 8 (silently dropped?)", got)
	}

	// 0 at create is a deliberate estimate, not "unestimated".
	_, _, zero := postTaskBody(t, srv, pid,
		map[string]interface{}{"title": "Zero effort", "taskType": "TASK", "storyPoints": 0})
	zid, _ := zero["id"].(string)
	if got := getTask(t, srv, zid)["storyPoints"]; got != float64(0) {
		t.Fatalf("storyPoints 0 at create read back = %v, want 0", got)
	}

	// Omitting it still creates an unestimated task.
	_, _, none := postTaskBody(t, srv, pid, map[string]interface{}{"title": "No estimate", "taskType": "TASK"})
	nid, _ := none["id"].(string)
	if got, ok := getTask(t, srv, nid)["storyPoints"]; !ok || got != nil {
		t.Fatalf("omitted storyPoints = %v (present=%v), want null", got, ok)
	}
}

func TestEstimation_CreateAppliesTheSameRulesAsPatch(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	// Estimation off: refused, not silently dropped.
	off := testutil.MustCreateProject(t, srv, "Create rules off")
	status, code, _ := postTaskBody(t, srv, off,
		map[string]interface{}{"title": "Nope", "taskType": "TASK", "storyPoints": 5})
	if status != http.StatusUnprocessableEntity || code != "ESTIMATION_UNIT_INACTIVE" {
		t.Errorf("create with estimate while off: status %d code %s, want 422 ESTIMATION_UNIT_INACTIVE", status, code)
	}

	pid := testutil.MustCreateProject(t, srv, "Create rules points")
	mustSetEstimationUnit(t, srv, pid, "POINTS")

	cases := []struct {
		name string
		body map[string]interface{}
		want string
	}{
		{"out of range high", map[string]interface{}{"title": "x", "taskType": "TASK", "storyPoints": 101}, "STORY_POINTS_INVALID"},
		{"out of range low", map[string]interface{}{"title": "x", "taskType": "TASK", "storyPoints": -1}, "STORY_POINTS_INVALID"},
		{"inactive unit", map[string]interface{}{"title": "x", "taskType": "TASK", "estimateHours": 4}, "ESTIMATION_UNIT_INACTIVE"},
		{"container type", map[string]interface{}{"title": "x", "taskType": "EPIC", "storyPoints": 5}, "ESTIMATION_NOT_ALLOWED_FOR_TYPE"},
	}
	for _, tc := range cases {
		status, code, _ := postTaskBody(t, srv, pid, tc.body)
		if status != http.StatusUnprocessableEntity || code != tc.want {
			t.Errorf("create %s: status %d code %s, want 422 %s", tc.name, status, code, tc.want)
		}
	}
}

// A rejected create must not burn a sequence number: the estimate is validated
// before NextSeqNumber precisely so a refused request leaves no gap in the
// project's task keys (DP-1, DP-2, … stay contiguous).
func TestEstimation_RejectedCreateDoesNotBurnASeqNumber(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Seq gap")
	mustSetEstimationUnit(t, srv, pid, "POINTS")

	_, _, first := postTaskBody(t, srv, pid, map[string]interface{}{"title": "First", "taskType": "TASK"})
	firstSeq, _ := first["seqNumber"].(float64)

	// Three creates that must all be refused.
	for i := 0; i < 3; i++ {
		if status, _, _ := postTaskBody(t, srv, pid,
			map[string]interface{}{"title": "Rejected", "taskType": "TASK", "storyPoints": 999}); status != http.StatusUnprocessableEntity {
			t.Fatalf("expected the create to be refused, got %d", status)
		}
	}

	_, _, next := postTaskBody(t, srv, pid, map[string]interface{}{"title": "Second", "taskType": "TASK"})
	nextSeq, _ := next["seqNumber"].(float64)
	if nextSeq != firstSeq+1 {
		t.Fatalf("seqNumber jumped %v → %v across 3 rejected creates; rejected creates burned keys",
			firstSeq, nextSeq)
	}
}
