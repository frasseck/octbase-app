package workmanagement_test

import (
	"net/http"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
)

// TestReopenTask_MovesCardOutOfDoneLane pins the status→board direction for
// reopen. It used to be the one transition that skipped
// alignBoardColumnToStatus: the task went back to PLANNED while its card stayed
// in the Done lane, so the board and the task disagreed about the same row —
// exactly what MoveTask and ChangeStatus already prevent from the other two
// directions.
func TestReopenTask_MovesCardOutOfDoneLane(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)
	planned := testutil.MustAddColumn(t, srv, bid, "Planned", "PLANNED", 0)
	done := testutil.MustAddColumn(t, srv, bid, "Done", "DONE", 1)

	taskID := testutil.MustCreateTask(t, srv, pid, "Card")
	mv := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+bid+"/move-task",
		map[string]interface{}{"taskId": taskID, "boardColumnId": done, "boardRank": 1000}, testutil.DemoUserID)
	testutil.AssertStatus(t, mv, http.StatusOK)

	re := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+taskID+"/reopen", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, re, http.StatusOK)
	var out map[string]interface{}
	testutil.DecodeJSON(t, re, &out)
	if out["status"] != "PLANNED" {
		t.Fatalf("status after reopen = %v, want PLANNED", out["status"])
	}
	if out["boardColumnId"] != planned {
		t.Fatalf("card after reopen sits in column %v, want the Planned lane %v", out["boardColumnId"], planned)
	}

	// Persisted, not just echoed — the response is built from the same struct the
	// update wrote, so a read-back is what proves the column reached the row.
	get := testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+taskID, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, get, http.StatusOK)
	var after map[string]interface{}
	testutil.DecodeJSON(t, get, &after)
	if after["status"] != "PLANNED" || after["boardColumnId"] != planned {
		t.Fatalf("persisted status/column = %v/%v, want PLANNED/%v",
			after["status"], after["boardColumnId"], planned)
	}
}

// TestReopenTask_KeepsCardWhenBoardHasNoPlannedLane covers the carve-out
// alignBoardColumnToStatus already makes for every other transition: a custom
// board that does not model the target status keeps the card where it is rather
// than dropping it off the board. Reopen must inherit that, not invent a
// stricter rule of its own.
func TestReopenTask_KeepsCardWhenBoardHasNoPlannedLane(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)
	// No PLANNED lane on this board at all.
	inProgress := testutil.MustAddColumn(t, srv, bid, "Doing", "IN_PROGRESS", 0)
	done := testutil.MustAddColumn(t, srv, bid, "Done", "DONE", 1)
	_ = inProgress

	taskID := testutil.MustCreateTask(t, srv, pid, "Card")
	mv := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+bid+"/move-task",
		map[string]interface{}{"taskId": taskID, "boardColumnId": done, "boardRank": 1000}, testutil.DemoUserID)
	testutil.AssertStatus(t, mv, http.StatusOK)

	re := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+taskID+"/reopen", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, re, http.StatusOK)
	var out map[string]interface{}
	testutil.DecodeJSON(t, re, &out)
	if out["status"] != "PLANNED" {
		t.Fatalf("status after reopen = %v, want PLANNED", out["status"])
	}
	if out["boardColumnId"] != done {
		t.Fatalf("card left the board on reopen (column %v); it should stay in %v when no lane models PLANNED",
			out["boardColumnId"], done)
	}
}

// TestReopenTask_UnboardedTaskIsUntouched — a task that is not on any board
// reopens without acquiring a column.
func TestReopenTask_UnboardedTaskIsUntouched(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	taskID := testutil.MustCreateTask(t, srv, pid, "Card")
	st := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+taskID+"/status",
		map[string]interface{}{"status": "DONE"}, testutil.DemoUserID)
	testutil.AssertStatus(t, st, http.StatusOK)

	re := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+taskID+"/reopen", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, re, http.StatusOK)
	var out map[string]interface{}
	testutil.DecodeJSON(t, re, &out)
	if out["status"] != "PLANNED" {
		t.Fatalf("status after reopen = %v, want PLANNED", out["status"])
	}
	if out["boardColumnId"] != nil {
		t.Fatalf("reopen put an unboarded task on a board (column %v)", out["boardColumnId"])
	}
}
