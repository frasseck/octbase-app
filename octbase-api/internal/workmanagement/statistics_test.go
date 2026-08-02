package workmanagement_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/octbase/octbase-api/internal/testutil"
)

// Project statistics report — GET /projects/{id}/reports/statistics.

func getStatistics(t *testing.T, srv *httptest.Server, projectID, actorID string) (int, map[string]interface{}) {
	t.Helper()
	resp := testutil.Do(t, srv, http.MethodGet,
		"/api/v1/projects/"+projectID+"/reports/statistics", nil, actorID)
	var out map[string]interface{}
	testutil.DecodeJSON(t, resp, &out)
	return resp.StatusCode, out
}

// mustSetTaskStatus moves a task and fails the test if the move is rejected.
func mustSetTaskStatus(t *testing.T, srv *httptest.Server, taskID, status string) {
	t.Helper()
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+taskID+"/status",
		map[string]string{"status": status}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()
}

// bucket reads one entry out of a distribution list ([{key,count}, …]).
func bucket(t *testing.T, list interface{}, key string) int {
	t.Helper()
	entries, _ := list.([]interface{})
	for _, e := range entries {
		m := e.(map[string]interface{})
		if m["key"] == key {
			return int(m["count"].(float64))
		}
	}
	t.Fatalf("bucket %q not found in %v", key, list)
	return 0
}

// TestStatistics_TotalsAndDistributions covers the headline block: the
// counters, the three distributions, and the rule that a distribution keeps
// its empty buckets so a chart does not change shape between reloads.
func TestStatistics_TotalsAndDistributions(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Stats project")

	planned := testutil.MustCreateTask(t, srv, pid, "Planned task")
	inProgress := testutil.MustCreateTask(t, srv, pid, "Running task")
	done := testutil.MustCreateTask(t, srv, pid, "Finished task")
	mustSetTaskStatus(t, srv, inProgress, "IN_PROGRESS")
	mustSetTaskStatus(t, srv, done, "DONE")

	status, stats := getStatistics(t, srv, pid, testutil.DemoUserID)
	if status != http.StatusOK {
		t.Fatalf("statistics: status %d", status)
	}
	tasks := stats["tasks"].(map[string]interface{})
	for field, want := range map[string]float64{
		"total": 3, "open": 2, "inProgress": 1, "done": 1, "archived": 0,
		"unassigned": 2, "overdue": 0, "dueSoon": 0,
		"createdLast30": 3, "completedLast30": 1,
	} {
		if got := tasks[field].(float64); got != want {
			t.Errorf("tasks.%s = %v, want %v", field, got, want)
		}
	}
	if bucket(t, tasks["byStatus"], "PLANNED") != 1 {
		t.Errorf("byStatus PLANNED = %d, want 1", bucket(t, tasks["byStatus"], "PLANNED"))
	}
	if bucket(t, tasks["byStatus"], "IN_REVIEW") != 0 {
		t.Errorf("byStatus IN_REVIEW should be present as 0, not dropped")
	}
	if bucket(t, tasks["byType"], "TASK") != 3 {
		t.Errorf("byType TASK = %d, want 3", bucket(t, tasks["byType"], "TASK"))
	}
	// Priority covers open work only: the DONE task must not be counted.
	if bucket(t, tasks["byPriority"], "MEDIUM") != 2 {
		t.Errorf("byPriority MEDIUM = %d, want 2 (open tasks only)", bucket(t, tasks["byPriority"], "MEDIUM"))
	}
	if stats["effort"] != nil {
		t.Errorf("effort = %v, want null for a project that does not estimate", stats["effort"])
	}
	_ = planned
}

// TestStatistics_OverdueAndDueSoon pins the two date tiles, including the
// rule that a finished task is never "overdue" however late it was.
func TestStatistics_OverdueAndDueSoon(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Stats dates")
	today := time.Now().UTC().Truncate(24 * time.Hour)

	setDue := func(taskID, date string) {
		t.Helper()
		resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+taskID, nil, testutil.DemoUserID)
		var task map[string]interface{}
		testutil.DecodeJSON(t, resp, &task)
		status, code, _ := patchTask(t, srv, taskID,
			map[string]interface{}{"dueDate": date, "version": int(task["version"].(float64))})
		if status != http.StatusOK {
			t.Fatalf("set dueDate: status %d code %s", status, code)
		}
	}

	late := testutil.MustCreateTask(t, srv, pid, "Late")
	setDue(late, today.AddDate(0, 0, -3).Format("2006-01-02"))
	soon := testutil.MustCreateTask(t, srv, pid, "Soon")
	setDue(soon, today.AddDate(0, 0, 2).Format("2006-01-02"))
	far := testutil.MustCreateTask(t, srv, pid, "Far")
	setDue(far, today.AddDate(0, 0, 30).Format("2006-01-02"))
	lateButDone := testutil.MustCreateTask(t, srv, pid, "Late but finished")
	setDue(lateButDone, today.AddDate(0, 0, -5).Format("2006-01-02"))
	mustSetTaskStatus(t, srv, lateButDone, "DONE")

	_, stats := getStatistics(t, srv, pid, testutil.DemoUserID)
	tasks := stats["tasks"].(map[string]interface{})
	if got := tasks["overdue"].(float64); got != 1 {
		t.Errorf("overdue = %v, want 1 (a finished task is history, not a call to action)", got)
	}
	if got := tasks["dueSoon"].(float64); got != 1 {
		t.Errorf("dueSoon = %v, want 1 (only the one inside the 7-day window)", got)
	}
}

// TestStatistics_EffortBlock covers the estimation block: totals in the
// project's unit, and the unestimated count that says how big the blind spot
// behind those totals is.
func TestStatistics_EffortBlock(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Stats effort")
	mustSetEstimationUnit(t, srv, pid, "POINTS")

	setPoints := func(title string, points int) string {
		t.Helper()
		task := mustPostTask(t, srv, pid, title, "TASK", "")
		id := task["id"].(string)
		status, code, _ := patchTask(t, srv, id,
			map[string]interface{}{"storyPoints": points, "version": int(task["version"].(float64))})
		if status != http.StatusOK {
			t.Fatalf("set storyPoints: status %d code %s", status, code)
		}
		return id
	}
	setPoints("Five", 5)
	eight := setPoints("Eight", 8)
	mustPostTask(t, srv, pid, "Unestimated", "TASK", "")
	mustSetTaskStatus(t, srv, eight, "DONE")

	_, stats := getStatistics(t, srv, pid, testutil.DemoUserID)
	effort := stats["effort"].(map[string]interface{})
	if effort["unit"] != "POINTS" {
		t.Errorf("effort.unit = %v, want POINTS", effort["unit"])
	}
	for field, want := range map[string]float64{"total": 13, "done": 8, "remaining": 5} {
		if got := effort[field].(float64); got != want {
			t.Errorf("effort.%s = %v, want %v", field, got, want)
		}
	}
	if got := effort["unestimated"].(float64); got != 1 {
		t.Errorf("effort.unestimated = %v, want 1", got)
	}
	if stats["estimationUnit"] != "POINTS" {
		t.Errorf("estimationUnit = %v, want POINTS", stats["estimationUnit"])
	}
}

// TestStatistics_ThroughputCycleTimeAndWorkload covers the three derived
// series. The throughput window always has its full complement of weeks (a
// quiet week is a zero bar, not a missing one) and cycle time is null-not-zero
// for a project that has finished nothing.
func TestStatistics_ThroughputCycleTimeAndWorkload(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Stats delivery")

	_, empty := getStatistics(t, srv, pid, testutil.DemoUserID)
	weeks, _ := empty["throughput"].([]interface{})
	if len(weeks) != 8 {
		t.Errorf("throughput weeks = %d, want 8 even with no deliveries", len(weeks))
	}
	cycle := empty["cycleTime"].(map[string]interface{})
	if cycle["sampleSize"].(float64) != 0 || cycle["averageDays"] != nil || cycle["medianDays"] != nil {
		t.Errorf("cycleTime = %v, want an empty sample with null averages", cycle)
	}
	if wl, _ := empty["workload"].([]interface{}); len(wl) != 0 {
		t.Errorf("workload = %v, want empty", wl)
	}
	if empty["sprint"] != nil {
		t.Errorf("sprint = %v, want null with no active sprint", empty["sprint"])
	}

	// Two tasks finished: one today, one backdated four days so the cycle-time
	// sample has a spread the median can sit in.
	first := testutil.MustCreateTask(t, srv, pid, "Delivered A")
	second := testutil.MustCreateTask(t, srv, pid, "Delivered B")
	mustSetTaskStatus(t, srv, first, "DONE")
	mustSetTaskStatus(t, srv, second, "DONE")
	if _, err := db.Exec(`UPDATE tasks SET created_at=$1 WHERE id=$2`,
		time.Now().UTC().AddDate(0, 0, -4).Format(time.RFC3339), second); err != nil {
		t.Fatalf("backdate created_at: %v", err)
	}

	// One open task assigned to the demo user, for the workload breakdown.
	open := testutil.MustCreateTask(t, srv, pid, "Assigned open")
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+open+"/assign",
		map[string]string{"assigneeId": testutil.DemoUserID}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()

	_, stats := getStatistics(t, srv, pid, testutil.DemoUserID)
	weeks, _ = stats["throughput"].([]interface{})
	last := weeks[len(weeks)-1].(map[string]interface{})
	if last["completed"].(float64) != 2 {
		t.Errorf("current week completed = %v, want 2", last["completed"])
	}
	if last["effort"] != nil {
		t.Errorf("throughput effort = %v, want null for a non-estimating project", last["effort"])
	}
	cycle = stats["cycleTime"].(map[string]interface{})
	if cycle["sampleSize"].(float64) != 2 {
		t.Errorf("cycleTime.sampleSize = %v, want 2", cycle["sampleSize"])
	}
	if cycle["averageDays"] == nil || cycle["medianDays"] == nil {
		t.Fatalf("cycleTime averages = %v, want numbers once tasks have finished", cycle)
	}
	if avg := cycle["averageDays"].(float64); avg < 1.5 || avg > 2.5 {
		t.Errorf("cycleTime.averageDays = %v, want ~2 (one same-day task, one four days old)", avg)
	}

	workload, _ := stats["workload"].([]interface{})
	if len(workload) != 1 {
		t.Fatalf("workload = %v, want exactly the one assignee with open work", workload)
	}
	entry := workload[0].(map[string]interface{})
	if entry["userId"] != testutil.DemoUserID || entry["open"].(float64) != 1 {
		t.Errorf("workload entry = %v, want the demo user with 1 open task", entry)
	}
	if entry["effort"] != nil {
		t.Errorf("workload effort = %v, want null for a non-estimating project", entry["effort"])
	}
}

// TestStatistics_SprintAndReleases covers the two planning blocks.
func TestStatistics_SprintAndReleases(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	pid, sid, taskIDs := estimatedSprint(t, srv, "POINTS", []*float64{f64(5), f64(2)})
	mustSetTaskStatus(t, srv, taskIDs[0], "DONE")

	// One open release, due in the past, plus one already closed.
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/releases",
		map[string]string{"name": "R1", "dueDate": time.Now().UTC().AddDate(0, 0, -2).Format("2006-01-02")},
		testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var rel map[string]interface{}
	testutil.DecodeJSON(t, resp, &rel)

	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/releases",
		map[string]string{"name": "R2"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var rel2 map[string]interface{}
	testutil.DecodeJSON(t, resp, &rel2)
	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/releases/"+rel2["id"].(string)+"/close",
		map[string]interface{}{"version": int(rel2["version"].(float64))}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()

	_, stats := getStatistics(t, srv, pid, testutil.DemoUserID)
	sprint := stats["sprint"].(map[string]interface{})
	if sprint["sprintId"] != sid {
		t.Errorf("sprint.sprintId = %v, want the active sprint %s", sprint["sprintId"], sid)
	}
	if sprint["committed"].(float64) != 2 || sprint["completed"].(float64) != 1 {
		t.Errorf("sprint counts = %v/%v, want 2/1", sprint["committed"], sprint["completed"])
	}
	if sprint["committedEstimate"].(float64) != 7 || sprint["completedEstimate"].(float64) != 5 {
		t.Errorf("sprint effort = %v/%v, want 7/5", sprint["committedEstimate"], sprint["completedEstimate"])
	}
	// The end date counts: a sprint ending today still has today to run.
	if sprint["daysRemaining"].(float64) != 1 {
		t.Errorf("daysRemaining = %v, want 1 (the sprint ends today, which still counts)", sprint["daysRemaining"])
	}

	releases := stats["releases"].(map[string]interface{})
	if releases["open"].(float64) != 1 || releases["closed"].(float64) != 1 {
		t.Errorf("releases open/closed = %v/%v, want 1/1", releases["open"], releases["closed"])
	}
	if releases["overdueOpen"].(float64) != 1 {
		t.Errorf("releases.overdueOpen = %v, want 1", releases["overdueOpen"])
	}
	if releases["nextDueName"] != "R1" {
		t.Errorf("releases.nextDueName = %v, want R1", releases["nextDueName"])
	}
}

// TestStatistics_RequiresMembership keeps the report behind the same guard as
// the data it aggregates: a non-member of a PRIVATE project must not be able
// to read its shape out of the numbers.
func TestStatistics_RequiresMembership(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Stats private")
	testutil.MustCreateTask(t, srv, pid, "Secret task")

	status, body := getStatistics(t, srv, pid, testutil.SecondUserID)
	if status != http.StatusForbidden && status != http.StatusNotFound {
		t.Fatalf("non-member statistics: status %d, want 403/404 — got body %v", status, body)
	}
}
