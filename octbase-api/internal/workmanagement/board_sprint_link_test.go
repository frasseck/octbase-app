package workmanagement_test

import (
	"net/http"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
)

// TestMoveTask_OntoSprintBoard_SetsSprintID verifies that moving a task onto a
// sprint board enrolls it in that board's sprint (so the sprint's task count
// reflects board membership), and that removing it again clears the link.
func TestMoveTask_OntoSprintBoard_SetsSprintID(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)
	col := testutil.MustAddColumn(t, srv, bid, "Todo", "PLANNED", 0)

	// Create a sprint and mark the board as its sprint board.
	spResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/sprints",
		map[string]interface{}{"name": "Sprint 1"}, testutil.DemoUserID)
	testutil.AssertStatus(t, spResp, http.StatusCreated)
	var sp map[string]interface{}
	testutil.DecodeJSON(t, spResp, &sp)
	sid := sp["id"].(string)

	linkResp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/boards/"+bid,
		map[string]interface{}{"isSprintBoard": true, "sprintId": sid}, testutil.DemoUserID)
	testutil.AssertStatus(t, linkResp, http.StatusOK)
	_ = linkResp.Body.Close()

	// A backlog task starts with no sprint link.
	taskID := testutil.MustCreateTask(t, srv, pid, "Backlog item")

	// Moving it onto the sprint board must set sprintId.
	mvResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+bid+"/move-task",
		map[string]interface{}{"taskId": taskID, "boardColumnId": col, "boardRank": 1000}, testutil.DemoUserID)
	testutil.AssertStatus(t, mvResp, http.StatusOK)
	var moved map[string]interface{}
	testutil.DecodeJSON(t, mvResp, &moved)
	if moved["sprintId"] != sid {
		t.Fatalf("after move onto sprint board: sprintId = %v, want %v", moved["sprintId"], sid)
	}

	// Removing it from the sprint board must clear the link.
	rmResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+bid+"/remove-task",
		map[string]interface{}{"taskId": taskID}, testutil.DemoUserID)
	testutil.AssertStatus(t, rmResp, http.StatusOK)
	var removed map[string]interface{}
	testutil.DecodeJSON(t, rmResp, &removed)
	if removed["sprintId"] != nil {
		t.Fatalf("after remove from sprint board: sprintId = %v, want nil", removed["sprintId"])
	}
}

// TestMoveTask_OntoRegularBoard_KeepsSprintID verifies a normal (non-sprint)
// board move does not clobber a sprint link set elsewhere (e.g. the task panel).
func TestMoveTask_OntoRegularBoard_KeepsSprintID(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)
	col := testutil.MustAddColumn(t, srv, bid, "Todo", "PLANNED", 0)

	spResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/sprints",
		map[string]interface{}{"name": "Sprint 1"}, testutil.DemoUserID)
	testutil.AssertStatus(t, spResp, http.StatusCreated)
	var sp map[string]interface{}
	testutil.DecodeJSON(t, spResp, &sp)
	sid := sp["id"].(string)

	taskID := testutil.MustCreateTask(t, srv, pid, "Backlog item")
	// Assign the sprint directly (as the task panel does).
	patchResp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/tasks/"+taskID,
		map[string]interface{}{"sprintId": sid}, testutil.DemoUserID)
	testutil.AssertStatus(t, patchResp, http.StatusOK)
	_ = patchResp.Body.Close()

	// Moving onto the regular board must leave the sprint link intact.
	mvResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+bid+"/move-task",
		map[string]interface{}{"taskId": taskID, "boardColumnId": col, "boardRank": 1000}, testutil.DemoUserID)
	testutil.AssertStatus(t, mvResp, http.StatusOK)
	var moved map[string]interface{}
	testutil.DecodeJSON(t, mvResp, &moved)
	if moved["sprintId"] != sid {
		t.Fatalf("after move onto regular board: sprintId = %v, want %v (unchanged)", moved["sprintId"], sid)
	}
}
