package workmanagement_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
)

// findSprintBoard returns the id of the board provisioned for a sprint together
// with the id of its first lane (columns are copied from the project's default
// board on sprint creation).
func findSprintBoard(t *testing.T, srv *httptest.Server, projectID, sprintID string) (boardID, columnID string) {
	t.Helper()
	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+projectID+"/boards", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var boards []map[string]interface{}
	testutil.DecodeJSON(t, resp, &boards)
	for _, b := range boards {
		if b["isSprintBoard"] == true && b["sprintId"] == sprintID {
			boardID = b["id"].(string)
		}
	}
	if boardID == "" {
		t.Fatalf("no sprint board found for sprint %s", sprintID)
	}
	full := testutil.Do(t, srv, http.MethodGet, "/api/v1/boards/"+boardID, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, full, http.StatusOK)
	var board struct {
		Columns []map[string]interface{} `json:"columns"`
	}
	testutil.DecodeJSON(t, full, &board)
	if len(board.Columns) == 0 {
		t.Fatalf("sprint board %s has no columns", boardID)
	}
	return boardID, board.Columns[0]["id"].(string)
}

func mustMoveOntoBoard(t *testing.T, srv *httptest.Server, boardID, columnID, taskID string) *http.Response {
	t.Helper()
	return testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+boardID+"/move-task",
		map[string]interface{}{"taskId": taskID, "boardColumnId": columnID, "boardRank": 1000}, testutil.DemoUserID)
}

// TestCompleteSprint_SnapshotsBoardScope verifies that completing a sprint
// records how many tasks were on the board and how many were DONE, so the
// historical sprint still reports e.g. 2/5 even though the 3 unfinished tasks
// are returned to the backlog (their sprintId is cleared) on completion.
func TestCompleteSprint_SnapshotsBoardScope(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	// Default board with two lanes — the sprint board copies these on creation.
	bid := testutil.MustCreateBoard(t, srv, pid)
	testutil.MustAddColumn(t, srv, bid, "Todo", "PLANNED", 0)
	testutil.MustAddColumn(t, srv, bid, "Done", "DONE", 1)

	sid := mustCreateSprint(t, srv, pid, "Sprint 1")
	sbID, todoCol := findSprintBoard(t, srv, pid, sid)

	// Plan 5 tasks onto the board (sprint still PLANNED).
	var taskIDs []string
	for i := 0; i < 5; i++ {
		tid := testutil.MustCreateTask(t, srv, pid, "Task")
		resp := mustMoveOntoBoard(t, srv, sbID, todoCol, tid)
		testutil.AssertStatus(t, resp, http.StatusOK)
		_ = resp.Body.Close()
		taskIDs = append(taskIDs, tid)
	}

	// Mark 2 of them DONE.
	for _, tid := range taskIDs[:2] {
		resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/status",
			map[string]interface{}{"status": "DONE"}, testutil.DemoUserID)
		testutil.AssertStatus(t, resp, http.StatusOK)
		_ = resp.Body.Close()
	}

	startResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/sprints/"+sid+"/start", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, startResp, http.StatusOK)
	_ = startResp.Body.Close()

	compResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/sprints/"+sid+"/complete", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, compResp, http.StatusOK)
	var completed map[string]interface{}
	testutil.DecodeJSON(t, compResp, &completed)

	if got := completed["committedCount"]; got != float64(5) {
		t.Errorf("committedCount = %v, want 5", got)
	}
	if got := completed["completedCount"]; got != float64(2) {
		t.Errorf("completedCount = %v, want 2", got)
	}
	if got := completed["status"]; got != "COMPLETED" {
		t.Errorf("status = %v, want COMPLETED", got)
	}
}

// TestCompleteSprint_ScopeFollowsSprintIDNotBoard verifies that the sprint scope
// is defined by tasks.sprint_id, not by what is on the board.
//
// This inverts the rule the suite previously pinned, deliberately. Board
// membership was chosen so the count could never disagree with what the user
// sees, but the two ways of committing a task were never equivalent: the task
// panel writes sprint_id without placing a card, so every task committed that
// way was invisible to the count. The cost was not cosmetic — the same query
// feeds the permanent completion snapshot, and Sprint 2 on the dogfooding
// instance closed as 40/41 when 84 tasks carried its sprint_id and 82 were DONE.
//
// So a task whose link is cleared while its card stays on the board leaves the
// scope (here: 2 remain, 1 of them DONE), because the link is what records
// commitment.
func TestCompleteSprint_ScopeFollowsSprintIDNotBoard(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)
	testutil.MustAddColumn(t, srv, bid, "Todo", "PLANNED", 0)
	testutil.MustAddColumn(t, srv, bid, "Done", "DONE", 1)

	sid := mustCreateSprint(t, srv, pid, "Sprint 1")
	sbID, todoCol := findSprintBoard(t, srv, pid, sid)

	// Plan 3 tasks onto the board (sprint still PLANNED).
	var taskIDs []string
	for i := 0; i < 3; i++ {
		tid := testutil.MustCreateTask(t, srv, pid, "Task")
		resp := mustMoveOntoBoard(t, srv, sbID, todoCol, tid)
		testutil.AssertStatus(t, resp, http.StatusOK)
		_ = resp.Body.Close()
		taskIDs = append(taskIDs, tid)
	}

	// Mark 1 DONE.
	doneResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+taskIDs[0]+"/status",
		map[string]interface{}{"status": "DONE"}, testutil.DemoUserID)
	testutil.AssertStatus(t, doneResp, http.StatusOK)
	_ = doneResp.Body.Close()

	// Clear one task's sprint link via the task detail while its card stays on
	// the board. The card is not the commitment, so this leaves the scope.
	clrResp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/tasks/"+taskIDs[1],
		map[string]interface{}{"sprintId": nil}, testutil.DemoUserID)
	testutil.AssertStatus(t, clrResp, http.StatusOK)
	_ = clrResp.Body.Close()

	startResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/sprints/"+sid+"/start", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, startResp, http.StatusOK)
	_ = startResp.Body.Close()

	compResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/sprints/"+sid+"/complete", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, compResp, http.StatusOK)
	var completed map[string]interface{}
	testutil.DecodeJSON(t, compResp, &completed)

	if got := completed["committedCount"]; got != float64(2) {
		t.Errorf("committedCount = %v, want 2 (the tasks still carrying sprintId)", got)
	}
	if got := completed["completedCount"]; got != float64(1) {
		t.Errorf("completedCount = %v, want 1", got)
	}
}

// TestMoveTask_ActiveSprintScopeLocked verifies that once a sprint is ACTIVE,
// new tasks cannot be added to its board (scope is committed), but tasks already
// in the sprint can still be moved between lanes.
func TestMoveTask_ActiveSprintScopeLocked(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)
	testutil.MustAddColumn(t, srv, bid, "Todo", "PLANNED", 0)
	testutil.MustAddColumn(t, srv, bid, "Doing", "IN_PROGRESS", 1)

	sid := mustCreateSprint(t, srv, pid, "Sprint 1")
	sbID, todoCol := findSprintBoard(t, srv, pid, sid)
	// Fetch the second lane id for the within-board move check.
	full := testutil.Do(t, srv, http.MethodGet, "/api/v1/boards/"+sbID, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, full, http.StatusOK)
	var board struct {
		Columns []map[string]interface{} `json:"columns"`
	}
	testutil.DecodeJSON(t, full, &board)
	doingCol := board.Columns[1]["id"].(string)

	// Plan one task while PLANNED.
	planned := testutil.MustCreateTask(t, srv, pid, "Planned task")
	r1 := mustMoveOntoBoard(t, srv, sbID, todoCol, planned)
	testutil.AssertStatus(t, r1, http.StatusOK)
	_ = r1.Body.Close()

	// Start the sprint — scope is now locked.
	startResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/sprints/"+sid+"/start", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, startResp, http.StatusOK)
	_ = startResp.Body.Close()

	// A brand new backlog task cannot be added to the running sprint's board.
	late := testutil.MustCreateTask(t, srv, pid, "Late task")
	r2 := mustMoveOntoBoard(t, srv, sbID, todoCol, late)
	testutil.AssertStatus(t, r2, http.StatusUnprocessableEntity)
	_ = r2.Body.Close()

	// The already-planned task may still be moved between lanes.
	r3 := mustMoveOntoBoard(t, srv, sbID, doingCol, planned)
	testutil.AssertStatus(t, r3, http.StatusOK)
	_ = r3.Body.Close()
}
