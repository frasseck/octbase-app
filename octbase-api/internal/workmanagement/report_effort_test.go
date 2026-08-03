package workmanagement_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/octbase/octbase-api/internal/testutil"
)

// Effort-based burndown and velocity. The count series these
// extend is asserted in report_test.go and must stay untouched — see
// TestSprintBurndown_UnitTasksIsUnchanged.

// burndownRaw fetches a burndown with an explicit query string and returns the
// status, the decoded body and the raw bytes (the byte-identity check needs
// the bytes, not a re-encoding of the decoded map).
func burndownRaw(t *testing.T, srv *httptest.Server, sprintID, query string) (int, map[string]interface{}, string) {
	t.Helper()
	path := "/api/v1/sprints/" + sprintID + "/burndown" + query
	resp := testutil.Do(t, srv, http.MethodGet, path, nil, testutil.DemoUserID)
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read burndown body: %v", err)
	}
	var out map[string]interface{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return resp.StatusCode, out, string(raw)
}

// estimatedSprint builds a project that estimates in unit, with a started
// 3-day sprint holding tasks whose estimates are the given values (nil = the
// task is deliberately left unestimated). It returns the project id, the
// sprint id and the task ids in the order the estimates were given.
func estimatedSprint(t *testing.T, srv *httptest.Server, unit string, estimates []*float64) (pid, sid string, taskIDs []string) {
	t.Helper()
	pid = testutil.MustCreateProject(t, srv, "Effort burndown "+unit)
	mustSetEstimationUnit(t, srv, pid, unit)
	bid := testutil.MustCreateBoard(t, srv, pid)
	testutil.MustAddColumn(t, srv, bid, "Todo", "PLANNED", 0)
	testutil.MustAddColumn(t, srv, bid, "Done", "DONE", 1)

	sid = mustCreateSprint(t, srv, pid, "Effort Sprint")
	sbID, todoCol := findSprintBoard(t, srv, pid, sid)

	field := "storyPoints"
	if unit == "HOURS" {
		field = "estimateHours"
	}
	for _, est := range estimates {
		task := mustPostTask(t, srv, pid, "Effort task", "TASK", "")
		tid := task["id"].(string)
		if est != nil {
			version := task["version"].(float64)
			value := *est
			if field == "storyPoints" {
				status, code, _ := patchTask(t, srv, tid,
					map[string]interface{}{field: int(value), "version": int(version)})
				if status != http.StatusOK {
					t.Fatalf("set %s: status %d code %s", field, status, code)
				}
			} else {
				status, code, _ := patchTask(t, srv, tid,
					map[string]interface{}{field: value, "version": int(version)})
				if status != http.StatusOK {
					t.Fatalf("set %s: status %d code %s", field, status, code)
				}
			}
		}
		resp := mustMoveOntoBoard(t, srv, sbID, todoCol, tid)
		testutil.AssertStatus(t, resp, http.StatusOK)
		_ = resp.Body.Close()
		taskIDs = append(taskIDs, tid)
	}

	today := time.Now().UTC().Truncate(24 * time.Hour)
	setSprintDates(t, srv, sid,
		today.AddDate(0, 0, -2).Format("2006-01-02"), today.Format("2006-01-02"))
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/sprints/"+sid+"/start", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()
	return pid, sid, taskIDs
}

func f64(v float64) *float64 { return &v }

// TestSprintBurndown_UnitPoints proves the accumulator, not the shape: the
// series must drop by a task's *points* on the day it finishes, not by one.
func TestSprintBurndown_UnitPoints(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	// 8 + 3 + 1 = 12 committed points across three tasks.
	_, sid, taskIDs := estimatedSprint(t, srv, "POINTS", []*float64{f64(8), f64(3), f64(1)})

	today := time.Now().UTC().Truncate(24 * time.Hour)
	// Finish the 8-pointer yesterday and the 1-pointer today. A task-counting
	// burndown would read 3 → 2 → 1; effort must read 12 → 4 → 3.
	for _, tid := range []string{taskIDs[0], taskIDs[2]} {
		resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/status",
			map[string]string{"status": "DONE"}, testutil.DemoUserID)
		testutil.AssertStatus(t, resp, http.StatusOK)
		_ = resp.Body.Close()
	}
	backdateTaskDone(t, db, taskIDs[0], today.AddDate(0, 0, -1).Add(12*time.Hour))

	status, body, _ := burndownRaw(t, srv, sid, "?unit=points")
	if status != http.StatusOK {
		t.Fatalf("burndown ?unit=points: status %d", status)
	}
	if body["committed"].(float64) != 12 {
		t.Errorf("committed = %v, want 12 (8+3+1 points)", body["committed"])
	}
	if body["unit"] != "points" {
		t.Errorf("unit = %v, want points (echoed back so the axis cannot be mislabelled)", body["unit"])
	}
	if body["unestimated"].(float64) != 0 {
		t.Errorf("unestimated = %v, want 0", body["unestimated"])
	}

	rem := remainingByDate(t, body)
	day := func(offset int) string { return today.AddDate(0, 0, offset).Format("2006-01-02") }
	want := map[string]float64{day(-2): 12, day(-1): 4, day(0): 3}
	for date, w := range want {
		got, ok := rem[date]
		if !ok || got == nil {
			t.Errorf("remaining[%s] = %v, want %v", date, got, w)
			continue
		}
		if *got != w {
			t.Errorf("remaining[%s] = %v, want %v", date, *got, w)
		}
	}
}

// TestSprintBurndown_UnitHours covers the second unit and fractional effort:
// hours are NUMERIC, so 2.5 must survive the round trip intact.
func TestSprintBurndown_UnitHours(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	_, sid, taskIDs := estimatedSprint(t, srv, "HOURS", []*float64{f64(2.5), f64(1.25)})

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+taskIDs[0]+"/status",
		map[string]string{"status": "DONE"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()

	status, body, _ := burndownRaw(t, srv, sid, "?unit=hours")
	if status != http.StatusOK {
		t.Fatalf("burndown ?unit=hours: status %d", status)
	}
	if body["committed"].(float64) != 3.75 {
		t.Errorf("committed = %v, want 3.75", body["committed"])
	}
	today := time.Now().UTC().Truncate(24 * time.Hour).Format("2006-01-02")
	rem := remainingByDate(t, body)
	if got := rem[today]; got == nil || *got != 1.25 {
		t.Errorf("remaining[%s] = %v, want 1.25", today, got)
	}
}

// TestSprintBurndown_UnestimatedReported is the whole point of the
// `unestimated` field: an unestimated task weighs 0, which is
// indistinguishable from "already done" unless the report says so.
func TestSprintBurndown_UnestimatedReported(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	_, sid, _ := estimatedSprint(t, srv, "POINTS", []*float64{f64(5), nil, nil})

	status, body, _ := burndownRaw(t, srv, sid, "?unit=points")
	if status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	if body["committed"].(float64) != 5 {
		t.Errorf("committed = %v, want 5 (the two unestimated tasks weigh 0)", body["committed"])
	}
	if body["unestimated"].(float64) != 2 {
		t.Errorf("unestimated = %v, want 2", body["unestimated"])
	}
}

// TestSprintBurndown_UnitMismatch422 pins the refusal to silently fall back:
// asking for the wrong effort unit is an error, not a task count.
func TestSprintBurndown_UnitMismatch422(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	_, sid, _ := estimatedSprint(t, srv, "POINTS", []*float64{f64(3)})

	status, body, _ := burndownRaw(t, srv, sid, "?unit=hours")
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("burndown ?unit=hours in a POINTS project: status %d, want 422", status)
	}
	if body["code"] != "ESTIMATION_UNIT_INACTIVE" {
		t.Errorf("code = %v, want ESTIMATION_UNIT_INACTIVE", body["code"])
	}
}

// TestSprintBurndown_EffortUnitInNoneProject422 covers the same refusal for a
// project that does not estimate at all.
func TestSprintBurndown_EffortUnitInNoneProject422(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "No estimation")
	bid := testutil.MustCreateBoard(t, srv, pid)
	testutil.MustAddColumn(t, srv, bid, "Todo", "PLANNED", 0)
	sid := mustCreateSprint(t, srv, pid, "Plain Sprint")
	today := time.Now().UTC().Truncate(24 * time.Hour)
	setSprintDates(t, srv, sid, today.Format("2006-01-02"), today.Format("2006-01-02"))
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/sprints/"+sid+"/start", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()

	status, body, _ := burndownRaw(t, srv, sid, "?unit=points")
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422", status)
	}
	if body["code"] != "ESTIMATION_UNIT_INACTIVE" {
		t.Errorf("code = %v, want ESTIMATION_UNIT_INACTIVE", body["code"])
	}

	status, body, _ = burndownRaw(t, srv, sid, "?unit=nonsense")
	if status != http.StatusBadRequest && status != http.StatusUnprocessableEntity {
		t.Fatalf("unknown unit: status %d, want a 4xx", status)
	}
	if body["code"] != "VALIDATION_ERROR" {
		t.Errorf("unknown unit code = %v, want VALIDATION_ERROR", body["code"])
	}
}

// TestSprintBurndown_UnitTasksIsUnchanged is the back-compat guard: the
// effort feature must not have changed a single byte of what existing clients
// see, so ?unit=tasks and the bare call are compared to each other and to the
// absence of the new fields.
func TestSprintBurndown_UnitTasksIsUnchanged(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	_, sid, taskIDs := estimatedSprint(t, srv, "POINTS", []*float64{f64(8), f64(3), nil})

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+taskIDs[0]+"/status",
		map[string]string{"status": "DONE"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()

	_, bare, bareRaw := burndownRaw(t, srv, sid, "")
	_, explicit, explicitRaw := burndownRaw(t, srv, sid, "?unit=tasks")
	if bareRaw != explicitRaw {
		t.Errorf("?unit=tasks differs from the no-param call:\n bare = %s\n tasks = %s", bareRaw, explicitRaw)
	}
	if _, ok := bare["unit"]; ok {
		t.Errorf("no-param response carries a unit field (%v); it must stay byte-identical to the pre-effort shape", bare["unit"])
	}
	if _, ok := bare["unestimated"]; ok {
		t.Errorf("no-param response carries an unestimated field; it must stay byte-identical to the pre-effort shape")
	}
	// Counting tasks: 3 committed, 1 finished — the 8 points are irrelevant here.
	if bare["committed"].(float64) != 3 {
		t.Errorf("committed = %v, want 3 tasks", bare["committed"])
	}
	today := time.Now().UTC().Truncate(24 * time.Hour).Format("2006-01-02")
	if got := remainingByDate(t, explicit)[today]; got == nil || *got != 2 {
		t.Errorf("remaining[%s] = %v, want 2 tasks", today, got)
	}
}

// TestVelocity_EffortSnapshot walks the whole velocity contract: completion
// snapshots effort next to the counts, the report returns both, and a later
// project-level unit switch does not rewrite history.
func TestVelocity_EffortSnapshot(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	pid, sid, taskIDs := estimatedSprint(t, srv, "POINTS", []*float64{f64(8), f64(3), f64(1)})

	// Finish the 8-pointer, then complete the sprint: 12 committed points, 8
	// completed, 3 tasks committed, 1 completed.
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+taskIDs[0]+"/status",
		map[string]string{"status": "DONE"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()
	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/sprints/"+sid+"/complete", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()

	entries := getVelocity(t, srv, pid)
	if len(entries) != 1 {
		t.Fatalf("velocity entries = %d, want 1", len(entries))
	}
	e := entries[0]
	if e["committed"].(float64) != 3 || e["completed"].(float64) != 1 {
		t.Errorf("counts = %v/%v, want 3/1 (the count series must be untouched)", e["committed"], e["completed"])
	}
	if e["committedEstimate"].(float64) != 12 || e["completedEstimate"].(float64) != 8 {
		t.Errorf("effort = %v/%v, want 12/8", e["committedEstimate"], e["completedEstimate"])
	}
	if e["estimateUnit"] != "POINTS" {
		t.Errorf("estimateUnit = %v, want POINTS", e["estimateUnit"])
	}

	// Switching the project to hours must not relabel or recompute the sprint
	// that was measured in points.
	mustSetEstimationUnit(t, srv, pid, "HOURS")
	after := getVelocity(t, srv, pid)[0]
	if after["estimateUnit"] != "POINTS" || after["committedEstimate"].(float64) != 12 {
		t.Errorf("after unit switch: %v %v, want POINTS 12 — a historical sprint keeps the unit it was completed in",
			after["estimateUnit"], after["committedEstimate"])
	}
}

// TestVelocity_NoEstimationSnapshotsNull keeps the non-estimating project's
// chart working: the effort fields are null, not 0, so a client can tell
// "not measured" from "measured as nothing".
func TestVelocity_NoEstimationSnapshotsNull(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Velocity no estimation")
	bid := testutil.MustCreateBoard(t, srv, pid)
	testutil.MustAddColumn(t, srv, bid, "Todo", "PLANNED", 0)
	testutil.MustAddColumn(t, srv, bid, "Done", "DONE", 1)
	sid := mustCreateSprint(t, srv, pid, "Plain Sprint")
	sbID, todoCol := findSprintBoard(t, srv, pid, sid)
	tid := testutil.MustCreateTask(t, srv, pid, "Plain task")
	resp := mustMoveOntoBoard(t, srv, sbID, todoCol, tid)
	testutil.AssertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()

	today := time.Now().UTC().Truncate(24 * time.Hour)
	setSprintDates(t, srv, sid, today.Format("2006-01-02"), today.Format("2006-01-02"))
	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/sprints/"+sid+"/start", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()
	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/sprints/"+sid+"/complete", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()

	e := getVelocity(t, srv, pid)[0]
	for _, field := range []string{"committedEstimate", "completedEstimate", "estimateUnit"} {
		v, present := e[field]
		if !present {
			t.Errorf("%s missing from the velocity entry", field)
			continue
		}
		if v != nil {
			t.Errorf("%s = %v, want null for a sprint completed without estimation", field, v)
		}
	}
}

// getVelocity fetches the project velocity report.
func getVelocity(t *testing.T, srv *httptest.Server, projectID string) []map[string]interface{} {
	t.Helper()
	resp := testutil.Do(t, srv, http.MethodGet,
		"/api/v1/projects/"+projectID+"/reports/velocity", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var out []map[string]interface{}
	testutil.DecodeJSON(t, resp, &out)
	return out
}
