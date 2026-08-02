package workmanagement_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
)

// The rule under test (OCT-304, decided 2026-08-02): taking a task off the board
// resets its status. It is the other half of OCT-303 — that one stopped an
// in-flight task from being left in the backlog by a status change, this one
// stops it arriving there by the opposite move. Together they make one
// invariant true: a task with no card is PLANNED, DONE or ARCHIVED, never in
// flight. TestBacklogHoldsNoInFlightTask at the bottom asserts exactly that.

// TestRemoveTaskFromBoard_ResetsStatus covers the panel's "Move to backlog".
func TestRemoveTaskFromBoard_ResetsStatus(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)
	testutil.MustAddColumn(t, srv, bid, "Planned", "PLANNED", 0)
	inReview := testutil.MustAddColumn(t, srv, bid, "In Review", "IN_REVIEW", 1)

	taskID := testutil.MustCreateTask(t, srv, pid, "Under review")
	mv := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+bid+"/move-task",
		map[string]interface{}{"taskId": taskID, "boardColumnId": inReview, "boardRank": 1000}, testutil.DemoUserID)
	testutil.AssertStatus(t, mv, http.StatusOK)
	var moved map[string]interface{}
	testutil.DecodeJSON(t, mv, &moved)
	if moved["status"] != "IN_REVIEW" {
		t.Fatalf("setup: status = %v, want IN_REVIEW (the lane's status)", moved["status"])
	}

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+bid+"/remove-task",
		map[string]interface{}{"taskId": taskID}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var after map[string]interface{}
	testutil.DecodeJSON(t, resp, &after)
	if after["boardColumnId"] != nil {
		t.Fatalf("boardColumnId = %v, want nil", after["boardColumnId"])
	}
	if after["status"] != "PLANNED" {
		t.Fatalf("status = %v, want PLANNED — a task in the backlog is not in flight", after["status"])
	}
	if !backlogContains(t, srv, pid, taskID) {
		t.Fatalf("task is not in the backlog after being taken off the board")
	}
}

// TestRemoveTaskFromBoard_KeepsDoneStatus pins the carve-out: DONE and ARCHIVED
// are immutable and are not in the backlog anyway, so resetting them would
// un-complete finished work rather than repair a contradiction.
func TestRemoveTaskFromBoard_KeepsDoneStatus(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)
	testutil.MustAddColumn(t, srv, bid, "Planned", "PLANNED", 0)
	done := testutil.MustAddColumn(t, srv, bid, "Done", "DONE", 1)

	taskID := testutil.MustCreateTask(t, srv, pid, "Finished")
	mv := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+bid+"/move-task",
		map[string]interface{}{"taskId": taskID, "boardColumnId": done, "boardRank": 1000}, testutil.DemoUserID)
	testutil.AssertStatus(t, mv, http.StatusOK)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+bid+"/remove-task",
		map[string]interface{}{"taskId": taskID}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var after map[string]interface{}
	testutil.DecodeJSON(t, resp, &after)
	if after["status"] != "DONE" {
		t.Fatalf("status = %v, want DONE — finished work is not un-completed by leaving the board", after["status"])
	}
}

// TestDeleteBoardColumn_ResetsDetachedTaskStatus covers deleting a lane, which
// detaches whatever was parked in it. The user guide used to promise these tasks
// "keep their status"; that promise is what put in-flight tasks in the backlog.
func TestDeleteBoardColumn_ResetsDetachedTaskStatus(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)
	testutil.MustAddColumn(t, srv, bid, "Planned", "PLANNED", 0)
	inProgress := testutil.MustAddColumn(t, srv, bid, "In Progress", "IN_PROGRESS", 1)
	doneCol := testutil.MustAddColumn(t, srv, bid, "Done", "DONE", 2)

	live := testutil.MustCreateTask(t, srv, pid, "Live work")
	testutil.AssertStatus(t, testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+bid+"/move-task",
		map[string]interface{}{"taskId": live, "boardColumnId": inProgress, "boardRank": 1000}, testutil.DemoUserID), http.StatusOK)
	finished := testutil.MustCreateTask(t, srv, pid, "Finished work")
	testutil.AssertStatus(t, testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+bid+"/move-task",
		map[string]interface{}{"taskId": finished, "boardColumnId": doneCol, "boardRank": 1000}, testutil.DemoUserID), http.StatusOK)

	del := testutil.Do(t, srv, http.MethodDelete, "/api/v1/boards/"+bid+"/columns/"+inProgress, nil, testutil.DemoUserID)
	if del.StatusCode != http.StatusOK && del.StatusCode != http.StatusNoContent {
		t.Fatalf("delete column: status %d", del.StatusCode)
	}

	if got := taskStatus(t, srv, live); got != "PLANNED" {
		t.Fatalf("detached task status = %s, want PLANNED", got)
	}
	if !backlogContains(t, srv, pid, live) {
		t.Fatalf("detached task is not in the backlog")
	}
	// The Done lane was not touched, so its card is untouched too.
	if got := taskStatus(t, srv, finished); got != "DONE" {
		t.Fatalf("task on an untouched lane changed status to %s", got)
	}
}

// TestCompleteSprint_CarriedTaskReturnsAsPlanned covers the path nobody thinks
// of as taking a task off the board: completing a sprint deletes the sprint
// board, so every unfinished task carried out of the sprint is detached by it.
// This is the most common way tasks reach the backlog, which is why it gets no
// exemption — exempting it would refill the backlog with in-flight work after
// every sprint.
func TestCompleteSprint_CarriedTaskReturnsAsPlanned(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	mainBoard := testutil.MustCreateBoard(t, srv, pid)
	testutil.MustAddColumn(t, srv, mainBoard, "Planned", "PLANNED", 0)
	testutil.MustAddColumn(t, srv, mainBoard, "In Progress", "IN_PROGRESS", 1)

	sresp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/sprints",
		map[string]interface{}{"name": "S1"}, testutil.DemoUserID)
	testutil.AssertStatus(t, sresp, http.StatusCreated)
	var sprint map[string]interface{}
	testutil.DecodeJSON(t, sresp, &sprint)
	sprintID := sprint["id"].(string)

	cresp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/tasks",
		map[string]interface{}{"title": "Unfinished", "sprintId": sprintID}, testutil.DemoUserID)
	testutil.AssertStatus(t, cresp, http.StatusCreated)
	var task map[string]interface{}
	testutil.DecodeJSON(t, cresp, &task)
	taskID := task["id"].(string)

	testutil.AssertStatus(t, testutil.Do(t, srv, http.MethodPost, "/api/v1/sprints/"+sprintID+"/start", nil, testutil.DemoUserID), http.StatusOK)
	// Start the work, so the sprint ends with something genuinely in flight.
	testutil.AssertStatus(t, testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+taskID+"/status",
		map[string]interface{}{"status": "IN_PROGRESS"}, testutil.DemoUserID), http.StatusOK)
	if got := taskStatus(t, srv, taskID); got != "IN_PROGRESS" {
		t.Fatalf("setup: status = %s, want IN_PROGRESS", got)
	}

	testutil.AssertStatus(t, testutil.Do(t, srv, http.MethodPost, "/api/v1/sprints/"+sprintID+"/complete", nil, testutil.DemoUserID), http.StatusOK)

	if got := taskStatus(t, srv, taskID); got != "PLANNED" {
		t.Fatalf("carried-over task status = %s, want PLANNED", got)
	}
	if !backlogContains(t, srv, pid, taskID) {
		t.Fatalf("carried-over task is not in the backlog")
	}
}

// TestBacklogHoldsNoInFlightTask states the whole rule as one assertion, over a
// project driven through every door that reaches the backlog. It is the test
// that would catch a fourth door being added later.
func TestBacklogHoldsNoInFlightTask(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)
	testutil.MustAddColumn(t, srv, bid, "Planned", "PLANNED", 0)
	inProgress := testutil.MustAddColumn(t, srv, bid, "In Progress", "IN_PROGRESS", 1)
	inReview := testutil.MustAddColumn(t, srv, bid, "In Review", "IN_REVIEW", 2)

	// Door 1: move to backlog from a lane.
	a := testutil.MustCreateTask(t, srv, pid, "A")
	testutil.AssertStatus(t, testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+bid+"/move-task",
		map[string]interface{}{"taskId": a, "boardColumnId": inReview, "boardRank": 1000}, testutil.DemoUserID), http.StatusOK)
	testutil.AssertStatus(t, testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+bid+"/remove-task",
		map[string]interface{}{"taskId": a}, testutil.DemoUserID), http.StatusOK)

	// Door 2: the lane holding it is deleted.
	b := testutil.MustCreateTask(t, srv, pid, "B")
	testutil.AssertStatus(t, testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+bid+"/move-task",
		map[string]interface{}{"taskId": b, "boardColumnId": inProgress, "boardRank": 1000}, testutil.DemoUserID), http.StatusOK)
	del := testutil.Do(t, srv, http.MethodDelete, "/api/v1/boards/"+bid+"/columns/"+inProgress, nil, testutil.DemoUserID)
	if del.StatusCode != http.StatusOK && del.StatusCode != http.StatusNoContent {
		t.Fatalf("delete column: status %d", del.StatusCode)
	}

	// Door 3: never placed at all (OCT-303's half — it is placed, not stranded).
	c := testutil.MustCreateTask(t, srv, pid, "C")
	testutil.AssertStatus(t, testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+c+"/status",
		map[string]interface{}{"status": "IN_REVIEW"}, testutil.DemoUserID), http.StatusOK)

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/backlog", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var items []map[string]interface{}
	testutil.DecodeJSON(t, resp, &items)
	if len(items) == 0 {
		t.Fatalf("backlog is empty; the assertion below would pass vacuously")
	}
	for _, it := range items {
		if it["status"] != "PLANNED" {
			t.Fatalf("backlog holds %q with status %v; a task with no card must be PLANNED", it["title"], it["status"])
		}
	}
}

// taskStatus reads a task's current status.
func taskStatus(t *testing.T, srv *httptest.Server, taskID string) string {
	t.Helper()
	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+taskID, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var task map[string]interface{}
	testutil.DecodeJSON(t, resp, &task)
	s, _ := task["status"].(string)
	return s
}
