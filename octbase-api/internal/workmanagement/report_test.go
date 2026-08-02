package workmanagement_test

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/octbase/octbase-api/internal/testutil"
)

// setSprintDates PATCHes start/end dates onto a sprint (date-only, UTC).
func setSprintDates(t *testing.T, srv *httptest.Server, sprintID, start, end string) {
	t.Helper()
	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/sprints/"+sprintID,
		map[string]string{"startDate": start, "endDate": end}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()
}

// backdateTaskDone rewrites a task's DONE transition (activity entry +
// done_at) to a past instant, simulating work completed on an earlier day.
func backdateTaskDone(t *testing.T, db *sql.DB, taskID string, at time.Time) {
	t.Helper()
	ts := at.UTC().Format(time.RFC3339)
	if _, err := db.Exec(
		`UPDATE activity_entries SET created_at=$1 WHERE task_id=$2 AND type='TASK_STATUS_CHANGED'`, ts, taskID); err != nil {
		t.Fatalf("backdate activity: %v", err)
	}
	if _, err := db.Exec(`UPDATE tasks SET done_at=$1 WHERE id=$2`, ts, taskID); err != nil {
		t.Fatalf("backdate done_at: %v", err)
	}
}

func getBurndown(t *testing.T, srv *httptest.Server, sprintID string) map[string]interface{} {
	t.Helper()
	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/sprints/"+sprintID+"/burndown", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var out map[string]interface{}
	testutil.DecodeJSON(t, resp, &out)
	return out
}

// remainingByDate flattens the points list into date → remaining (nil kept).
func remainingByDate(t *testing.T, burndown map[string]interface{}) map[string]*float64 {
	t.Helper()
	points, _ := burndown["points"].([]interface{})
	out := make(map[string]*float64, len(points))
	for _, p := range points {
		pm := p.(map[string]interface{})
		date := pm["date"].(string)
		if pm["remaining"] == nil {
			out[date] = nil
			continue
		}
		v := pm["remaining"].(float64)
		out[date] = &v
	}
	return out
}

// TestSprintBurndown_ActiveSprint seeds a 5-day sprint (started 2 days ago)
// with 3 tasks, of which one was finished yesterday and one today, and
// asserts the reconstructed daily remaining counts, the null future days,
// and the ideal line's endpoint.
func TestSprintBurndown_ActiveSprint(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Burndown P")
	bid := testutil.MustCreateBoard(t, srv, pid)
	testutil.MustAddColumn(t, srv, bid, "Todo", "PLANNED", 0)
	testutil.MustAddColumn(t, srv, bid, "Done", "DONE", 1)

	sid := mustCreateSprint(t, srv, pid, "Reported Sprint")
	sbID, todoCol := findSprintBoard(t, srv, pid, sid)

	var taskIDs []string
	for i := 0; i < 3; i++ {
		tid := testutil.MustCreateTask(t, srv, pid, "BD Task")
		resp := mustMoveOntoBoard(t, srv, sbID, todoCol, tid)
		testutil.AssertStatus(t, resp, http.StatusOK)
		_ = resp.Body.Close()
		taskIDs = append(taskIDs, tid)
	}

	today := time.Now().UTC().Truncate(24 * time.Hour)
	start := today.AddDate(0, 0, -2)
	end := today.AddDate(0, 0, 2)
	setSprintDates(t, srv, sid, start.Format("2006-01-02"), end.Format("2006-01-02"))

	startResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/sprints/"+sid+"/start", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, startResp, http.StatusOK)
	_ = startResp.Body.Close()

	// Finish two tasks: one now (today), one backdated to yesterday noon.
	for _, tid := range taskIDs[:2] {
		resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/status",
			map[string]string{"status": "DONE"}, testutil.DemoUserID)
		testutil.AssertStatus(t, resp, http.StatusOK)
		_ = resp.Body.Close()
	}
	backdateTaskDone(t, db, taskIDs[1], today.AddDate(0, 0, -1).Add(12*time.Hour))

	burndown := getBurndown(t, srv, sid)
	if burndown["committed"].(float64) != 3 {
		t.Errorf("committed = %v, want 3", burndown["committed"])
	}
	points, _ := burndown["points"].([]interface{})
	if len(points) != 5 {
		t.Fatalf("points = %d, want 5", len(points))
	}

	rem := remainingByDate(t, burndown)
	day := func(offset int) string { return today.AddDate(0, 0, offset).Format("2006-01-02") }
	assertRemaining := func(date string, want float64) {
		t.Helper()
		got, ok := rem[date]
		if !ok || got == nil {
			t.Errorf("remaining[%s] = %v, want %v", date, got, want)
			return
		}
		if *got != want {
			t.Errorf("remaining[%s] = %v, want %v", date, *got, want)
		}
	}
	assertRemaining(day(-2), 3) // start day: nothing done yet
	assertRemaining(day(-1), 2) // one finished yesterday
	assertRemaining(day(0), 1)  // second finished today
	if v, ok := rem[day(1)]; !ok || v != nil {
		t.Errorf("remaining[%s] = %v, want null (future day of an active sprint)", day(1), v)
	}
	if v, ok := rem[day(2)]; !ok || v != nil {
		t.Errorf("remaining[%s] = %v, want null (future day of an active sprint)", day(2), v)
	}

	last := points[len(points)-1].(map[string]interface{})
	if last["ideal"].(float64) != 0 {
		t.Errorf("ideal endpoint = %v, want 0", last["ideal"])
	}
}

// TestSprintBurndown_CompletedSprint verifies the report survives completion:
// committed comes from the snapshot even though unfinished tasks were
// unlinked, and the finished task's transition is still replayable.
func TestSprintBurndown_CompletedSprint(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Burndown Completed P")
	bid := testutil.MustCreateBoard(t, srv, pid)
	testutil.MustAddColumn(t, srv, bid, "Todo", "PLANNED", 0)
	testutil.MustAddColumn(t, srv, bid, "Done", "DONE", 1)

	sid := mustCreateSprint(t, srv, pid, "Done Sprint")
	sbID, todoCol := findSprintBoard(t, srv, pid, sid)
	var taskIDs []string
	for i := 0; i < 2; i++ {
		tid := testutil.MustCreateTask(t, srv, pid, "BD Task")
		resp := mustMoveOntoBoard(t, srv, sbID, todoCol, tid)
		testutil.AssertStatus(t, resp, http.StatusOK)
		_ = resp.Body.Close()
		taskIDs = append(taskIDs, tid)
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	setSprintDates(t, srv, sid, today.AddDate(0, 0, -1).Format("2006-01-02"), today.Format("2006-01-02"))
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/sprints/"+sid+"/start", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()

	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+taskIDs[0]+"/status",
		map[string]string{"status": "DONE"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()

	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/sprints/"+sid+"/complete", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()

	burndown := getBurndown(t, srv, sid)
	if burndown["committed"].(float64) != 2 {
		t.Errorf("committed = %v, want 2 (snapshot)", burndown["committed"])
	}
	rem := remainingByDate(t, burndown)
	lastDay := today.Format("2006-01-02")
	if got := rem[lastDay]; got == nil || *got != 1 {
		t.Errorf("remaining[%s] = %v, want 1", lastDay, got)
	}
}

func TestSprintBurndown_PlannedSprint422(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Planned P")
	sid := mustCreateSprint(t, srv, pid, "Unstarted")

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/sprints/"+sid+"/burndown", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
	var errBody map[string]interface{}
	testutil.DecodeJSON(t, resp, &errBody)
	if errBody["code"] != "SPRINT_NOT_STARTED" {
		t.Errorf("code = %v, want SPRINT_NOT_STARTED", errBody["code"])
	}
}

// TestProjectVelocity seeds three completed sprints with known snapshot
// counts and asserts ordering (oldest first) and the N cap.
func TestProjectVelocity(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Velocity P")

	// Insert completed sprints directly — velocity is a pure projection of the
	// snapshotted counts, so the workflow that produced them is irrelevant here.
	now := time.Now().UTC()
	for i := 1; i <= 3; i++ {
		end := now.AddDate(0, 0, -7*(3-i)).Format("2006-01-02")
		if _, err := db.Exec(
			`INSERT INTO sprints (id, project_id, name, goal, start_date, end_date, status, committed_count, completed_count, created_at, updated_at, version)
			 VALUES ($1,$2,$3,'',$4,$5,'COMPLETED',$6,$7,$8,$8,1)`,
			fmt.Sprintf("00000000-0000-0000-0000-00000000000%d", i), pid, fmt.Sprintf("Sprint %d", i),
			now.AddDate(0, 0, -7*(3-i)-14).Format("2006-01-02"), end,
			5+i, i, now.Format(time.RFC3339),
		); err != nil {
			t.Fatalf("insert sprint: %v", err)
		}
	}

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/reports/velocity", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var entries []map[string]interface{}
	testutil.DecodeJSON(t, resp, &entries)
	if len(entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(entries))
	}
	// Oldest first.
	for i, wantName := range []string{"Sprint 1", "Sprint 2", "Sprint 3"} {
		if entries[i]["name"] != wantName {
			t.Errorf("entries[%d].name = %v, want %s", i, entries[i]["name"], wantName)
		}
		if entries[i]["committed"].(float64) != float64(5+i+1) {
			t.Errorf("entries[%d].committed = %v, want %d", i, entries[i]["committed"], 5+i+1)
		}
		if entries[i]["completed"].(float64) != float64(i+1) {
			t.Errorf("entries[%d].completed = %v, want %d", i, entries[i]["completed"], i+1)
		}
	}

	// n=2 keeps only the two most recent, still oldest first.
	resp = testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/reports/velocity?n=2", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	testutil.DecodeJSON(t, resp, &entries)
	if len(entries) != 2 || entries[0]["name"] != "Sprint 2" || entries[1]["name"] != "Sprint 3" {
		t.Errorf("n=2 entries = %v, want [Sprint 2, Sprint 3]", entries)
	}
}
