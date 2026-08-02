package workmanagement_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
)

// TestChangeStatus_RealignsBoardColumn verifies OCT-90: changing a boarded
// task's status moves its card into the lane whose status matches, so a status
// change (e.g. from the task panel) keeps the board in sync. This is the
// status→board direction; the board→status direction is server-side too —
// MoveTask adopts the target lane's status (see board_lane_status_test.go).
func TestChangeStatus_RealignsBoardColumn(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)
	planned := testutil.MustAddColumn(t, srv, bid, "Planned", "PLANNED", 0)
	inProgress := testutil.MustAddColumn(t, srv, bid, "In Progress", "IN_PROGRESS", 1)

	// A fresh task defaults to PLANNED; put it on the Planned lane.
	taskID := testutil.MustCreateTask(t, srv, pid, "Card")
	mv := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+bid+"/move-task",
		map[string]interface{}{"taskId": taskID, "boardColumnId": planned, "boardRank": 1000}, testutil.DemoUserID)
	testutil.AssertStatus(t, mv, http.StatusOK)
	var moved map[string]interface{}
	testutil.DecodeJSON(t, mv, &moved)
	if moved["boardColumnId"] != planned {
		t.Fatalf("setup: boardColumnId = %v, want %v", moved["boardColumnId"], planned)
	}
	version := int(moved["version"].(float64))

	// Changing status to IN_PROGRESS must move the card to the In Progress lane.
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+taskID+"/status",
		map[string]interface{}{"status": "IN_PROGRESS", "version": version}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var after map[string]interface{}
	testutil.DecodeJSON(t, resp, &after)
	if after["status"] != "IN_PROGRESS" {
		t.Fatalf("status = %v, want IN_PROGRESS", after["status"])
	}
	if after["boardColumnId"] != inProgress {
		t.Fatalf("boardColumnId = %v, want In Progress lane %v", after["boardColumnId"], inProgress)
	}
}

// TestChangeStatus_UnboardedTaskEntersBoard verifies OCT-303: a task that has
// never been on a board and moves into an in-flight status is PLACED on the
// board rather than left where it was. The backlog is defined as "no card"
// (TaskRepo.Backlog selects board_column_id IS NULL), so leaving it unplaced
// puts a task labelled "In Review" in the list of work not started, and leaves
// the board — the one view of what is moving — silent about it.
func TestChangeStatus_UnboardedTaskEntersBoard(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)
	testutil.MustAddColumn(t, srv, bid, "Planned", "PLANNED", 0)
	inReview := testutil.MustAddColumn(t, srv, bid, "In Review", "IN_REVIEW", 1)
	taskID := testutil.MustCreateTask(t, srv, pid, "Never boarded")

	// Precondition: it is in the backlog, which is where a PLANNED task belongs.
	if !backlogContains(t, srv, pid, taskID) {
		t.Fatalf("setup: task should start in the backlog")
	}

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+taskID+"/status",
		map[string]interface{}{"status": "IN_REVIEW"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var after map[string]interface{}
	testutil.DecodeJSON(t, resp, &after)
	if after["status"] != "IN_REVIEW" {
		t.Fatalf("status = %v, want IN_REVIEW", after["status"])
	}
	if after["boardColumnId"] != inReview {
		t.Fatalf("boardColumnId = %v, want the In Review lane %v", after["boardColumnId"], inReview)
	}
	if backlogContains(t, srv, pid, taskID) {
		t.Fatalf("task is IN_REVIEW and on the board, but still listed in the backlog")
	}
}

// TestChangeStatus_UnboardedStaysInBacklogWhenPlanned verifies the other half of
// the same rule: PLANNED is the backlog's own status, so a planned task is not
// dragged onto the board. Without this the backlog would empty itself.
func TestChangeStatus_UnboardedStaysInBacklogWhenPlanned(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)
	testutil.MustAddColumn(t, srv, bid, "Planned", "PLANNED", 0)
	testutil.MustAddColumn(t, srv, bid, "In Progress", "IN_PROGRESS", 1)
	taskID := testutil.MustCreateTask(t, srv, pid, "Planned work")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+taskID+"/status",
		map[string]interface{}{"status": "PLANNED"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var after map[string]interface{}
	testutil.DecodeJSON(t, resp, &after)
	if after["boardColumnId"] != nil {
		t.Fatalf("boardColumnId = %v, want nil — a PLANNED task stays in the backlog", after["boardColumnId"])
	}
	if !backlogContains(t, srv, pid, taskID) {
		t.Fatalf("PLANNED task left the backlog")
	}
}

// TestChangeStatus_UnboardedNoLaneForStatusStaysOff verifies the conservative
// half: when the board the task would join has no lane for the new status,
// nothing is invented. This mirrors TestChangeStatus_NoMatchingLaneKeepsColumn
// for the unboarded case.
func TestChangeStatus_UnboardedNoLaneForStatusStaysOff(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	// The default board carries only a Planned lane. A second, non-default board
	// carries the IN_PROGRESS lane, which is what makes the status valid
	// project-wide without giving the default board anywhere to put the card.
	bid := testutil.MustCreateBoard(t, srv, pid)
	testutil.MustAddColumn(t, srv, bid, "Planned", "PLANNED", 0)
	other := testutil.MustCreateNonDefaultBoard(t, srv, pid, "Side board")
	testutil.MustAddColumn(t, srv, other, "In Progress", "IN_PROGRESS", 0)
	taskID := testutil.MustCreateTask(t, srv, pid, "Off-board card")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+taskID+"/status",
		map[string]interface{}{"status": "IN_PROGRESS"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var after map[string]interface{}
	testutil.DecodeJSON(t, resp, &after)
	if after["status"] != "IN_PROGRESS" {
		t.Fatalf("status = %v, want IN_PROGRESS", after["status"])
	}
	if after["boardColumnId"] != nil {
		t.Fatalf("boardColumnId = %v, want nil — no lane for the status on that board", after["boardColumnId"])
	}
}

// TestChangeStatus_UnboardedSprintTaskEntersSprintBoard verifies which board an
// unplaced card joins. A card lives in exactly one lane, so this is a choice: a
// task committed to a sprint belongs on that sprint's board — the same board
// MoveTask would have put it on — not on the project's main board.
func TestChangeStatus_UnboardedSprintTaskEntersSprintBoard(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	mainBoard := testutil.MustCreateBoard(t, srv, pid)
	testutil.MustAddColumn(t, srv, mainBoard, "Planned", "PLANNED", 0)
	mainInProgress := testutil.MustAddColumn(t, srv, mainBoard, "In Progress", "IN_PROGRESS", 1)

	sresp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/sprints",
		map[string]interface{}{"name": "S1"}, testutil.DemoUserID)
	testutil.AssertStatus(t, sresp, http.StatusCreated)
	var sprint map[string]interface{}
	testutil.DecodeJSON(t, sresp, &sprint)
	sprintID := sprint["id"].(string)

	// Commit the task while the sprint is still PLANNED — a running sprint's
	// scope is locked (SPRINT_SCOPE_LOCKED) — and never place it on a card.
	cresp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/tasks",
		map[string]interface{}{"title": "Sprint work", "sprintId": sprintID}, testutil.DemoUserID)
	testutil.AssertStatus(t, cresp, http.StatusCreated)
	var task map[string]interface{}
	testutil.DecodeJSON(t, cresp, &task)
	taskID := task["id"].(string)

	// The sprint board was provisioned at sprint creation, seeded from the main
	// board's lanes, so it has an In Progress lane of its own. Neither creating
	// the sprint nor starting it places already-committed cards, which is what
	// leaves this task unboarded. Starting the sprint here exercises the ACTIVE
	// case; the PLANNED case is pinned by the next test.
	start := testutil.Do(t, srv, http.MethodPost, "/api/v1/sprints/"+sprintID+"/start", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, start, http.StatusOK)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+taskID+"/status",
		map[string]interface{}{"status": "IN_PROGRESS"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var after map[string]interface{}
	testutil.DecodeJSON(t, resp, &after)
	placed, _ := after["boardColumnId"].(string)
	if placed == "" {
		t.Fatalf("boardColumnId = %v, want a lane on the sprint board", after["boardColumnId"])
	}
	if placed == mainInProgress {
		t.Fatalf("card landed on the main board's In Progress lane; want the sprint board's")
	}
	// Confirm the lane it joined really belongs to the sprint's own board.
	bresp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/boards", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, bresp, http.StatusOK)
	var boards []map[string]interface{}
	testutil.DecodeJSON(t, bresp, &boards)
	var sprintBoardID string
	for _, b := range boards {
		if sid, ok := b["sprintId"].(string); ok && sid == sprintID {
			sprintBoardID = b["id"].(string)
		}
	}
	if sprintBoardID == "" {
		t.Fatalf("setup: no sprint board was provisioned for %s", sprintID)
	}
	cols := testutil.Do(t, srv, http.MethodGet, "/api/v1/boards/"+sprintBoardID, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, cols, http.StatusOK)
	var board map[string]interface{}
	testutil.DecodeJSON(t, cols, &board)
	found := false
	for _, c := range board["columns"].([]interface{}) {
		if c.(map[string]interface{})["id"] == placed {
			found = true
		}
	}
	if !found {
		t.Fatalf("boardColumnId %s is not a lane of the sprint board %s", placed, sprintBoardID)
	}
}

// TestChangeStatus_UnboardedPlannedSprintTaskEntersSprintBoard pins that the
// sprint choice does not depend on the sprint running: the sprint board exists
// from the sprint's creation (so its scope can be planned) until completion
// deletes it, and a task committed while the sprint is still PLANNED is placed
// on that board — the same board a planning drag (MoveTask) would use — not on
// the project's main board.
func TestChangeStatus_UnboardedPlannedSprintTaskEntersSprintBoard(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	mainBoard := testutil.MustCreateBoard(t, srv, pid)
	testutil.MustAddColumn(t, srv, mainBoard, "Planned", "PLANNED", 0)
	mainInProgress := testutil.MustAddColumn(t, srv, mainBoard, "In Progress", "IN_PROGRESS", 1)

	sresp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/sprints",
		map[string]interface{}{"name": "S1"}, testutil.DemoUserID)
	testutil.AssertStatus(t, sresp, http.StatusCreated)
	var sprint map[string]interface{}
	testutil.DecodeJSON(t, sresp, &sprint)
	sprintID := sprint["id"].(string)

	cresp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/tasks",
		map[string]interface{}{"title": "Planned sprint work", "sprintId": sprintID}, testutil.DemoUserID)
	testutil.AssertStatus(t, cresp, http.StatusCreated)
	var task map[string]interface{}
	testutil.DecodeJSON(t, cresp, &task)
	taskID := task["id"].(string)

	// The sprint is never started. The only IN_PROGRESS lanes in this project
	// are the main board's and the sprint board's copy of it, so "placed, and
	// not on the main board's lane" pins the sprint board without re-walking it.
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+taskID+"/status",
		map[string]interface{}{"status": "IN_PROGRESS"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var after map[string]interface{}
	testutil.DecodeJSON(t, resp, &after)
	placed, _ := after["boardColumnId"].(string)
	if placed == "" {
		t.Fatalf("boardColumnId = %v, want a lane on the PLANNED sprint's board", after["boardColumnId"])
	}
	if placed == mainInProgress {
		t.Fatalf("card landed on the main board's In Progress lane; want the sprint board's")
	}
}

// TestBulkSetStatus_RealignsCards verifies the bulk bar obeys the same rule as
// the single-task endpoint. It used to obey neither half: BulkSetStatus is a
// blind per-row SET of one column, so a boarded card kept the lane of its OLD
// status and an unboarded task stayed in the backlog wearing the new one.
func TestBulkSetStatus_RealignsCards(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)
	planned := testutil.MustAddColumn(t, srv, bid, "Planned", "PLANNED", 0)
	inProgress := testutil.MustAddColumn(t, srv, bid, "In Progress", "IN_PROGRESS", 1)

	unboarded := testutil.MustCreateTask(t, srv, pid, "never boarded")
	boarded := testutil.MustCreateTask(t, srv, pid, "on the Planned lane")
	mv := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+bid+"/move-task",
		map[string]interface{}{"taskId": boarded, "boardColumnId": planned, "boardRank": 1000}, testutil.DemoUserID)
	testutil.AssertStatus(t, mv, http.StatusOK)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/tasks/bulk",
		map[string]interface{}{"action": "set_status", "value": "IN_PROGRESS",
			"taskIds": []string{unboarded, boarded}}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	ranks := map[string]float64{}
	for _, id := range []string{unboarded, boarded} {
		g := testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+id, nil, testutil.DemoUserID)
		testutil.AssertStatus(t, g, http.StatusOK)
		var task map[string]interface{}
		testutil.DecodeJSON(t, g, &task)
		if task["status"] != "IN_PROGRESS" {
			t.Fatalf("%v: status = %v, want IN_PROGRESS", task["title"], task["status"])
		}
		if task["boardColumnId"] != inProgress {
			t.Fatalf("%v: boardColumnId = %v, want the In Progress lane %v", task["title"], task["boardColumnId"], inProgress)
		}
		ranks[id], _ = task["boardRank"].(float64)
	}
	// Two cards appended to the same lane in one bulk action must not claim the
	// same rank, or their order is decided by the created_at tiebreak instead.
	if ranks[unboarded] == ranks[boarded] {
		t.Fatalf("both cards got boardRank %v; ranks must be distinct within a lane", ranks[unboarded])
	}
	if backlogContains(t, srv, pid, unboarded) {
		t.Fatalf("bulk-started task is still listed in the backlog")
	}
}

// backlogContains reports whether the project's backlog lists the task.
func backlogContains(t *testing.T, srv *httptest.Server, projectID, taskID string) bool {
	t.Helper()
	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+projectID+"/backlog", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var items []map[string]interface{}
	testutil.DecodeJSON(t, resp, &items)
	for _, it := range items {
		if it["id"] == taskID {
			return true
		}
	}
	return false
}

// TestChangeStatus_NoMatchingLaneKeepsColumn verifies that when the task's board
// has no lane for the new status, the card stays in its current lane rather than
// being dropped off the board.
func TestChangeStatus_NoMatchingLaneKeepsColumn(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)
	planned := testutil.MustAddColumn(t, srv, bid, "Planned", "PLANNED", 0)
	// A second board carries the IN_PROGRESS lane so the status is valid
	// project-wide, but the task's own board (bid) has no such lane.
	other := testutil.MustCreateBoard(t, srv, pid)
	testutil.MustAddColumn(t, srv, other, "In Progress", "IN_PROGRESS", 0)

	taskID := testutil.MustCreateTask(t, srv, pid, "Card")
	mv := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+bid+"/move-task",
		map[string]interface{}{"taskId": taskID, "boardColumnId": planned, "boardRank": 1000}, testutil.DemoUserID)
	testutil.AssertStatus(t, mv, http.StatusOK)
	var moved map[string]interface{}
	testutil.DecodeJSON(t, mv, &moved)
	version := int(moved["version"].(float64))

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+taskID+"/status",
		map[string]interface{}{"status": "IN_PROGRESS", "version": version}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var after map[string]interface{}
	testutil.DecodeJSON(t, resp, &after)
	if after["boardColumnId"] != planned {
		t.Fatalf("boardColumnId = %v, want unchanged Planned lane %v", after["boardColumnId"], planned)
	}
}
