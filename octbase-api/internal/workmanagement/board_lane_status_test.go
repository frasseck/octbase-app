package workmanagement_test

import (
	"net/http"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
)

// TestMoveTask_AdoptsLaneStatus verifies the board→status direction is
// server-side: parking a card in a lane sets the task's status to the lane's,
// so every client (web drag, mobile move sheet, raw API) stays consistent.
func TestMoveTask_AdoptsLaneStatus(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)
	testutil.MustAddColumn(t, srv, bid, "Planned", "PLANNED", 0)
	inProgress := testutil.MustAddColumn(t, srv, bid, "In Progress", "IN_PROGRESS", 1)

	taskID := testutil.MustCreateTask(t, srv, pid, "Card")
	mv := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+bid+"/move-task",
		map[string]interface{}{"taskId": taskID, "boardColumnId": inProgress, "boardRank": 1000}, testutil.DemoUserID)
	testutil.AssertStatus(t, mv, http.StatusOK)
	var moved map[string]interface{}
	testutil.DecodeJSON(t, mv, &moved)
	if moved["status"] != "IN_PROGRESS" {
		t.Fatalf("status after lane move = %v, want IN_PROGRESS", moved["status"])
	}

	// The change is persisted, not just echoed.
	get := testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+taskID, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, get, http.StatusOK)
	var after map[string]interface{}
	testutil.DecodeJSON(t, get, &after)
	if after["status"] != "IN_PROGRESS" {
		t.Fatalf("persisted status = %v, want IN_PROGRESS", after["status"])
	}
}

// TestMoveTask_ImmutableTaskKeepsStatus verifies a DONE task cannot be dragged
// into a lane with a different status (it must be reopened first), while
// reordering within its own lane stays allowed.
func TestMoveTask_ImmutableTaskKeepsStatus(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)
	planned := testutil.MustAddColumn(t, srv, bid, "Planned", "PLANNED", 0)
	done := testutil.MustAddColumn(t, srv, bid, "Done", "DONE", 1)

	taskID := testutil.MustCreateTask(t, srv, pid, "Card")
	// Moving into the Done lane completes the task (adopts DONE).
	mv := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+bid+"/move-task",
		map[string]interface{}{"taskId": taskID, "boardColumnId": done, "boardRank": 1000}, testutil.DemoUserID)
	testutil.AssertStatus(t, mv, http.StatusOK)
	var moved map[string]interface{}
	testutil.DecodeJSON(t, mv, &moved)
	if moved["status"] != "DONE" {
		t.Fatalf("status after move to Done lane = %v, want DONE", moved["status"])
	}

	// Dragging the finished card into another lane is refused…
	back := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+bid+"/move-task",
		map[string]interface{}{"taskId": taskID, "boardColumnId": planned, "boardRank": 1000}, testutil.DemoUserID)
	var e map[string]interface{}
	testutil.DecodeJSON(t, back, &e)
	if back.StatusCode != http.StatusUnprocessableEntity || e["code"] != "TASK_IMMUTABLE" {
		t.Fatalf("move DONE task across lanes: status %d code %v, want 422 TASK_IMMUTABLE", back.StatusCode, e["code"])
	}

	// …but reordering within the Done lane is not a status change and works.
	reorder := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+bid+"/move-task",
		map[string]interface{}{"taskId": taskID, "boardColumnId": done, "boardRank": 2000}, testutil.DemoUserID)
	testutil.AssertStatus(t, reorder, http.StatusOK)
}

// TestMoveTask_BlockerGuardOnDoneLane verifies the BLOCKER-child rule holds on
// the board too: a drag into the Done lane is the same claim as "status: DONE".
func TestMoveTask_BlockerGuardOnDoneLane(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)
	testutil.MustAddColumn(t, srv, bid, "Planned", "PLANNED", 0)
	done := testutil.MustAddColumn(t, srv, bid, "Done", "DONE", 1)

	story := mustPostTask(t, srv, pid, "Story", "STORY", "")
	storyID := story["id"].(string)
	child := mustPostTask(t, srv, pid, "Child", "TASK", storyID)
	childID := child["id"].(string)
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+childID+"/priority",
		map[string]interface{}{"priority": "BLOCKER"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	mv := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+bid+"/move-task",
		map[string]interface{}{"taskId": storyID, "boardColumnId": done, "boardRank": 1000}, testutil.DemoUserID)
	var e map[string]interface{}
	testutil.DecodeJSON(t, mv, &e)
	if mv.StatusCode != http.StatusUnprocessableEntity || e["code"] != "TASK_HAS_BLOCKER" {
		t.Fatalf("move into Done lane with blocker child: status %d code %v, want 422 TASK_HAS_BLOCKER", mv.StatusCode, e["code"])
	}
}

// TestDeleteColumn_DetachesTasks verifies deleting a populated lane returns its
// tasks to the backlog instead of leaving them with a dangling column id
// (invisible on the board and excluded from the backlog).
func TestDeleteColumn_DetachesTasks(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)
	testutil.MustAddColumn(t, srv, bid, "Planned", "PLANNED", 0)
	qa := testutil.MustAddColumn(t, srv, bid, "QA", "QA", 1)

	taskID := testutil.MustCreateTask(t, srv, pid, "Card")
	mv := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+bid+"/move-task",
		map[string]interface{}{"taskId": taskID, "boardColumnId": qa, "boardRank": 1000}, testutil.DemoUserID)
	testutil.AssertStatus(t, mv, http.StatusOK)

	del := testutil.Do(t, srv, http.MethodDelete, "/api/v1/boards/"+bid+"/columns/"+qa, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, del, http.StatusNoContent)

	get := testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+taskID, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, get, http.StatusOK)
	var after map[string]interface{}
	testutil.DecodeJSON(t, get, &after)
	if after["boardColumnId"] != nil {
		t.Fatalf("boardColumnId = %v, want nil after its lane was deleted", after["boardColumnId"])
	}
	// It returns to PLANNED (OCT-304): a task with no card is in the backlog, and
	// the backlog holds work that has not started. This case is the strongest
	// argument for that rule — the status it used to keep was "QA", a CUSTOM
	// status defined by the very lane just deleted, so keeping it left the task
	// wearing a stage that no longer existed on any board.
	if after["status"] != "PLANNED" {
		t.Fatalf("status = %v, want PLANNED after its lane was deleted", after["status"])
	}
	bl := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/backlog", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, bl, http.StatusOK)
	var backlog []map[string]interface{}
	testutil.DecodeJSON(t, bl, &backlog)
	found := false
	for _, bt := range backlog {
		if bt["id"] == taskID {
			found = true
		}
	}
	if !found {
		t.Fatalf("task %s missing from the backlog after its lane was deleted", taskID)
	}
}

// TestDeleteColumn_ScopedToBoard verifies a column can only be deleted through
// its own board: a writer's guard on board A must not delete board B's lanes.
func TestDeleteColumn_ScopedToBoard(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bidA := testutil.MustCreateBoard(t, srv, pid)
	testutil.MustAddColumn(t, srv, bidA, "Planned", "PLANNED", 0)
	testutil.MustAddColumn(t, srv, bidA, "Extra", "IN_PROGRESS", 1)
	bidB := testutil.MustCreateBoard(t, srv, pid)
	testutil.MustAddColumn(t, srv, bidB, "Planned", "PLANNED", 0)
	victim := testutil.MustAddColumn(t, srv, bidB, "Done", "DONE", 1)

	del := testutil.Do(t, srv, http.MethodDelete, "/api/v1/boards/"+bidA+"/columns/"+victim, nil, testutil.DemoUserID)
	var e map[string]interface{}
	testutil.DecodeJSON(t, del, &e)
	if del.StatusCode != http.StatusNotFound || e["code"] != "COLUMN_NOT_FOUND" {
		t.Fatalf("cross-board delete: status %d code %v, want 404 COLUMN_NOT_FOUND", del.StatusCode, e["code"])
	}

	// The victim column is untouched.
	get := testutil.Do(t, srv, http.MethodGet, "/api/v1/boards/"+bidB, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, get, http.StatusOK)
	var board map[string]interface{}
	testutil.DecodeJSON(t, get, &board)
	cols := board["columns"].([]interface{})
	found := false
	for _, c := range cols {
		if c.(map[string]interface{})["id"] == victim {
			found = true
		}
	}
	if !found {
		t.Fatalf("board B's column was deleted through board A")
	}
}

// TestUpdateColumn_StatusChangeRetagsTasks verifies re-statusing a lane carries
// its cards along: the lane's non-terminal tasks adopt the new status, while
// DONE/ARCHIVED tasks that happen to sit in the lane are left untouched.
func TestUpdateColumn_StatusChangeRetagsTasks(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)
	qa := testutil.MustAddColumn(t, srv, bid, "QA", "QA", 0)

	// Card 1 sits in the lane with its status.
	t1 := testutil.MustCreateTask(t, srv, pid, "In QA")
	mv := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+bid+"/move-task",
		map[string]interface{}{"taskId": t1, "boardColumnId": qa, "boardRank": 1000}, testutil.DemoUserID)
	testutil.AssertStatus(t, mv, http.StatusOK)

	// Card 2 is DONE but parked in the lane (no DONE lane exists on this board,
	// so completing it leaves the card where it is).
	t2 := testutil.MustCreateTask(t, srv, pid, "Finished in QA")
	mv2 := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+bid+"/move-task",
		map[string]interface{}{"taskId": t2, "boardColumnId": qa, "boardRank": 2000}, testutil.DemoUserID)
	testutil.AssertStatus(t, mv2, http.StatusOK)
	st := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+t2+"/status",
		map[string]interface{}{"status": "DONE"}, testutil.DemoUserID)
	testutil.AssertStatus(t, st, http.StatusOK)

	// Re-status the lane.
	up := testutil.Do(t, srv, http.MethodPatch, "/api/v1/boards/"+bid+"/columns/"+qa,
		map[string]interface{}{"status": "VERIFY"}, testutil.DemoUserID)
	testutil.AssertStatus(t, up, http.StatusOK)

	get1 := testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+t1, nil, testutil.DemoUserID)
	var after1 map[string]interface{}
	testutil.DecodeJSON(t, get1, &after1)
	if after1["status"] != "VERIFY" {
		t.Fatalf("lane task status = %v, want VERIFY after lane re-status", after1["status"])
	}
	get2 := testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+t2, nil, testutil.DemoUserID)
	var after2 map[string]interface{}
	testutil.DecodeJSON(t, get2, &after2)
	if after2["status"] != "DONE" {
		t.Fatalf("finished task status = %v, want DONE untouched by lane re-status", after2["status"])
	}
}

// TestUpdateColumn_DuplicateStatusRejected verifies PATCH gets the same
// uniqueness rule as POST — previously the DB unique index surfaced as a 500.
func TestUpdateColumn_DuplicateStatusRejected(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)
	testutil.MustAddColumn(t, srv, bid, "Planned", "PLANNED", 0)
	second := testutil.MustAddColumn(t, srv, bid, "In Progress", "IN_PROGRESS", 1)

	up := testutil.Do(t, srv, http.MethodPatch, "/api/v1/boards/"+bid+"/columns/"+second,
		map[string]interface{}{"status": "PLANNED"}, testutil.DemoUserID)
	var e map[string]interface{}
	testutil.DecodeJSON(t, up, &e)
	if up.StatusCode != http.StatusConflict || e["code"] != "COLUMN_STATUS_DUPLICATE" {
		t.Fatalf("duplicate lane status via PATCH: status %d code %v, want 409 COLUMN_STATUS_DUPLICATE", up.StatusCode, e["code"])
	}
}

// TestUpdateColumn_InvalidStatusShapeRejected verifies PATCH enforces the same
// status shape rule as POST (non-empty, bounded length).
func TestUpdateColumn_InvalidStatusShapeRejected(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)
	col := testutil.MustAddColumn(t, srv, bid, "Planned", "PLANNED", 0)

	up := testutil.Do(t, srv, http.MethodPatch, "/api/v1/boards/"+bid+"/columns/"+col,
		map[string]interface{}{"status": "   "}, testutil.DemoUserID)
	var e map[string]interface{}
	testutil.DecodeJSON(t, up, &e)
	if up.StatusCode != http.StatusConflict || e["code"] != "COLUMN_STATUS_INVALID" {
		t.Fatalf("blank lane status via PATCH: status %d code %v, want 409 COLUMN_STATUS_INVALID", up.StatusCode, e["code"])
	}
}
