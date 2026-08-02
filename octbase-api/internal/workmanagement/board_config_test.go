package workmanagement_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
)

// ---- Configurable lane limits ----

func TestCreateBoard_DefaultLaneLimits(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/boards",
		map[string]interface{}{"name": "B"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var b map[string]interface{}
	testutil.DecodeJSON(t, resp, &b)
	if b["minColumns"].(float64) != 1 || b["maxColumns"].(float64) != 10 {
		t.Errorf("default limits = %v/%v, want 1/10", b["minColumns"], b["maxColumns"])
	}
}

func TestCreateBoard_RejectsOutOfRangeLimits(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	cases := []struct{ min, max int }{
		{0, 5},  // min below 1
		{1, 11}, // max above 10
		{5, 3},  // min greater than max
	}
	for _, c := range cases {
		resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/boards",
			map[string]interface{}{"name": "B", "minColumns": c.min, "maxColumns": c.max}, testutil.DemoUserID)
		testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
		var e map[string]interface{}
		testutil.DecodeJSON(t, resp, &e)
		if e["code"] != "BOARD_LIMITS_INVALID" {
			t.Errorf("min=%d max=%d: code = %v, want BOARD_LIMITS_INVALID", c.min, c.max, e["code"])
		}
	}
}

func TestUpdateBoard_RejectsOutOfRangeLimits(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)

	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/boards/"+bid,
		map[string]interface{}{"maxColumns": 99}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "BOARD_LIMITS_INVALID" {
		t.Errorf("code = %v, want BOARD_LIMITS_INVALID", e["code"])
	}
}

func TestAddColumn_RejectsAboveMaxLanes(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	// Board capped at 2 lanes.
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/boards",
		map[string]interface{}{"name": "B", "minColumns": 1, "maxColumns": 2}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var b map[string]interface{}
	testutil.DecodeJSON(t, resp, &b)
	bid := b["id"].(string)

	testutil.MustAddColumn(t, srv, bid, "A", "PLANNED", 0)
	testutil.MustAddColumn(t, srv, bid, "B", "IN_PROGRESS", 1)

	// Third column exceeds the max of 2.
	over := testutil.Do(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/boards/%s/columns", bid),
		map[string]interface{}{"name": "C", "status": "DONE", "position": 2}, testutil.DemoUserID)
	testutil.AssertStatus(t, over, http.StatusConflict)
	var e map[string]interface{}
	testutil.DecodeJSON(t, over, &e)
	if e["code"] != "BOARD_MAX_LANES" {
		t.Errorf("code = %v, want BOARD_MAX_LANES", e["code"])
	}
}

func TestDeleteColumn_RejectsBelowMinLanes(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	// Board requiring at least 2 lanes.
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/boards",
		map[string]interface{}{"name": "B", "minColumns": 2, "maxColumns": 5}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var b map[string]interface{}
	testutil.DecodeJSON(t, resp, &b)
	bid := b["id"].(string)

	testutil.MustAddColumn(t, srv, bid, "A", "PLANNED", 0)
	cid := testutil.MustAddColumn(t, srv, bid, "B", "IN_PROGRESS", 1)

	// Removing one would drop to 1 lane, below the minimum of 2.
	del := testutil.Do(t, srv, http.MethodDelete, fmt.Sprintf("/api/v1/boards/%s/columns/%s", bid, cid), nil, testutil.DemoUserID)
	testutil.AssertStatus(t, del, http.StatusConflict)
	var e map[string]interface{}
	testutil.DecodeJSON(t, del, &e)
	if e["code"] != "BOARD_MIN_LANES" {
		t.Errorf("code = %v, want BOARD_MIN_LANES", e["code"])
	}
}

func TestRenameColumn(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)
	cid := testutil.MustAddColumn(t, srv, bid, "Old", "PLANNED", 0)

	resp := testutil.Do(t, srv, http.MethodPatch, fmt.Sprintf("/api/v1/boards/%s/columns/%s", bid, cid),
		map[string]string{"name": "Renamed"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var c map[string]interface{}
	testutil.DecodeJSON(t, resp, &c)
	if c["name"] != "Renamed" {
		t.Errorf("name = %v, want Renamed", c["name"])
	}
}

// ---- Sprint-enabled boards ----

func TestBoard_LinkSprint(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)

	// Create a timed sprint to link.
	spResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/sprints",
		map[string]interface{}{"name": "Sprint 1", "startDate": "2026-01-01", "endDate": "2026-01-14"}, testutil.DemoUserID)
	testutil.AssertStatus(t, spResp, http.StatusCreated)
	var sp map[string]interface{}
	testutil.DecodeJSON(t, spResp, &sp)
	sid := sp["id"].(string)

	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/boards/"+bid,
		map[string]interface{}{"isSprintBoard": true, "sprintId": sid}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var b map[string]interface{}
	testutil.DecodeJSON(t, resp, &b)
	if b["isSprintBoard"] != true {
		t.Errorf("isSprintBoard = %v, want true", b["isSprintBoard"])
	}
	if b["sprintId"] != sid {
		t.Errorf("sprintId = %v, want %v", b["sprintId"], sid)
	}
	// The linked sprint timing is embedded for rendering.
	sprint, ok := b["sprint"].(map[string]interface{})
	if !ok || sprint["startDate"] != "2026-01-01" {
		t.Errorf("embedded sprint timing missing/wrong: %v", b["sprint"])
	}
}

func TestBoard_LinkSprint_CrossProjectRejected(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pidA := testutil.MustCreateProject(t, srv, "A")
	pidB := testutil.MustCreateProject(t, srv, "B")
	bid := testutil.MustCreateBoard(t, srv, pidA)

	// Sprint belongs to project B.
	spResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pidB+"/sprints",
		map[string]interface{}{"name": "S"}, testutil.DemoUserID)
	testutil.AssertStatus(t, spResp, http.StatusCreated)
	var sp map[string]interface{}
	testutil.DecodeJSON(t, spResp, &sp)
	sid := sp["id"].(string)

	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/boards/"+bid,
		map[string]interface{}{"isSprintBoard": true, "sprintId": sid}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "BOARD_SPRINT_PROJECT_MISMATCH" {
		t.Errorf("code = %v, want BOARD_SPRINT_PROJECT_MISMATCH", e["code"])
	}
}

// ---- Cross-board read-only external columns ----

func TestExternalColumn_AddFromAnotherBoard(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	srcBid := testutil.MustCreateBoard(t, srv, pid)
	srcCol := testutil.MustAddColumn(t, srv, srcBid, "Source Lane", "PLANNED", 0)

	// A second board in the same project consumes the column.
	dstResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/boards",
		map[string]interface{}{"name": "Dest"}, testutil.DemoUserID)
	testutil.AssertStatus(t, dstResp, http.StatusCreated)
	var dst map[string]interface{}
	testutil.DecodeJSON(t, dstResp, &dst)
	dstBid := dst["id"].(string)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+dstBid+"/external-columns",
		map[string]interface{}{"sourceColumnId": srcCol}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var ec map[string]interface{}
	testutil.DecodeJSON(t, resp, &ec)
	if ec["sourceColumnId"] != srcCol {
		t.Errorf("sourceColumnId = %v, want %v", ec["sourceColumnId"], srcCol)
	}
	if ec["sourceBoardId"] != srcBid {
		t.Errorf("sourceBoardId = %v, want %v", ec["sourceBoardId"], srcBid)
	}
	if ec["sourceColumnName"] != "Source Lane" {
		t.Errorf("sourceColumnName = %v, want 'Source Lane'", ec["sourceColumnName"])
	}

	// The board GET now carries the external column.
	br := testutil.Do(t, srv, http.MethodGet, "/api/v1/boards/"+dstBid, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, br, http.StatusOK)
	var board map[string]interface{}
	testutil.DecodeJSON(t, br, &board)
	ext, ok := board["externalColumns"].([]interface{})
	if !ok || len(ext) != 1 {
		t.Fatalf("expected 1 external column on board, got %v", board["externalColumns"])
	}
}

func TestExternalColumn_DuplicateRejected(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	srcBid := testutil.MustCreateBoard(t, srv, pid)
	srcCol := testutil.MustAddColumn(t, srv, srcBid, "Lane", "PLANNED", 0)
	dstResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/boards",
		map[string]interface{}{"name": "Dest"}, testutil.DemoUserID)
	var dst map[string]interface{}
	testutil.DecodeJSON(t, dstResp, &dst)
	dstBid := dst["id"].(string)

	first := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+dstBid+"/external-columns",
		map[string]interface{}{"sourceColumnId": srcCol}, testutil.DemoUserID)
	testutil.AssertStatus(t, first, http.StatusCreated)

	dup := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+dstBid+"/external-columns",
		map[string]interface{}{"sourceColumnId": srcCol}, testutil.DemoUserID)
	testutil.AssertStatus(t, dup, http.StatusConflict)
	var e map[string]interface{}
	testutil.DecodeJSON(t, dup, &e)
	if e["code"] != "EXTERNAL_COLUMN_DUPLICATE" {
		t.Errorf("code = %v, want EXTERNAL_COLUMN_DUPLICATE", e["code"])
	}
}

func TestExternalColumn_SameBoardRejected(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)
	col := testutil.MustAddColumn(t, srv, bid, "Lane", "PLANNED", 0)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+bid+"/external-columns",
		map[string]interface{}{"sourceColumnId": col}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "EXTERNAL_COLUMN_SAME_BOARD" {
		t.Errorf("code = %v, want EXTERNAL_COLUMN_SAME_BOARD", e["code"])
	}
}

// TestExternalColumn_CrossProjectAllowedWithReadAccess verifies a column from a
// board in another project can be linked when the caller can read that project,
// and that the linked column carries the source project label and its tasks.
func TestExternalColumn_CrossProjectAllowedWithReadAccess(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pidA := testutil.MustCreateProject(t, srv, "A") // source
	pidB := testutil.MustCreateProject(t, srv, "B") // dest
	srcBid := testutil.MustCreateBoard(t, srv, pidA)
	srcCol := testutil.MustAddColumn(t, srv, srcBid, "Lane", "PLANNED", 0)

	// Put a task in the source column so the linked column has content.
	tid := testutil.MustCreateTask(t, srv, pidA, "Linked Task")
	mv := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+srcBid+"/move-task",
		map[string]interface{}{"taskId": tid, "boardColumnId": srcCol}, testutil.DemoUserID)
	testutil.AssertStatus(t, mv, http.StatusOK)

	dstBid := testutil.MustCreateBoard(t, srv, pidB)

	// DemoUser owns both projects, so they can read the source project.
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+dstBid+"/external-columns",
		map[string]interface{}{"sourceColumnId": srcCol}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var ec map[string]interface{}
	testutil.DecodeJSON(t, resp, &ec)
	if ec["sourceProjectId"] != pidA {
		t.Errorf("sourceProjectId = %v, want %v", ec["sourceProjectId"], pidA)
	}
	if ec["accessible"] != true {
		t.Errorf("accessible = %v, want true", ec["accessible"])
	}
	tasks, _ := ec["tasks"].([]interface{})
	if len(tasks) != 1 {
		t.Fatalf("linked column tasks = %v, want 1", ec["tasks"])
	}
}

// TestExternalColumn_CrossProjectNoAccessRejected verifies that linking a column
// from a project the caller cannot read is rejected.
func TestExternalColumn_CrossProjectNoAccessRejected(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pidA := testutil.MustCreateProject(t, srv, "A") // source — SecondUser is NOT a member
	pidB := testutil.MustCreateProject(t, srv, "B") // dest
	srcBid := testutil.MustCreateBoard(t, srv, pidA)
	srcCol := testutil.MustAddColumn(t, srv, srcBid, "Lane", "PLANNED", 0)
	dstBid := testutil.MustCreateBoard(t, srv, pidB)

	// SecondUser is a writer on the dest project only.
	testutil.MustAddMember(t, srv, pidB, testutil.SecondUserID, "PROJECT_MEMBER")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+dstBid+"/external-columns",
		map[string]interface{}{"sourceColumnId": srcCol}, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "EXTERNAL_COLUMN_NO_ACCESS" {
		t.Errorf("code = %v, want EXTERNAL_COLUMN_NO_ACCESS", e["code"])
	}
}

// TestExternalColumn_TasksHiddenWithoutSourceAccess verifies that a board viewer
// who cannot read the source project sees the linked column but none of its
// tasks (no cross-project data leak).
func TestExternalColumn_TasksHiddenWithoutSourceAccess(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pidA := testutil.MustCreateProject(t, srv, "A") // source
	pidB := testutil.MustCreateProject(t, srv, "B") // dest
	srcBid := testutil.MustCreateBoard(t, srv, pidA)
	srcCol := testutil.MustAddColumn(t, srv, srcBid, "Lane", "PLANNED", 0)
	tid := testutil.MustCreateTask(t, srv, pidA, "Secret Task")
	mv := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+srcBid+"/move-task",
		map[string]interface{}{"taskId": tid, "boardColumnId": srcCol}, testutil.DemoUserID)
	testutil.AssertStatus(t, mv, http.StatusOK)

	dstBid := testutil.MustCreateBoard(t, srv, pidB)
	link := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+dstBid+"/external-columns",
		map[string]interface{}{"sourceColumnId": srcCol}, testutil.DemoUserID)
	testutil.AssertStatus(t, link, http.StatusCreated)

	// SecondUser can read the dest project but not the source project.
	testutil.MustAddMember(t, srv, pidB, testutil.SecondUserID, "PROJECT_VIEWER")

	br := testutil.Do(t, srv, http.MethodGet, "/api/v1/boards/"+dstBid, nil, testutil.SecondUserID)
	testutil.AssertStatus(t, br, http.StatusOK)
	var board map[string]interface{}
	testutil.DecodeJSON(t, br, &board)
	ext, _ := board["externalColumns"].([]interface{})
	if len(ext) != 1 {
		t.Fatalf("expected 1 external column, got %v", board["externalColumns"])
	}
	col := ext[0].(map[string]interface{})
	if col["accessible"] != false {
		t.Errorf("accessible = %v, want false for viewer without source access", col["accessible"])
	}
	if tasks, _ := col["tasks"].([]interface{}); len(tasks) != 0 {
		t.Errorf("linked column tasks = %v, want none (hidden)", col["tasks"])
	}
}

// TestExternalColumn_ReadOnly verifies that a cross-board external column is a
// metadata-only reference: the consuming board exposes no surface to move, edit,
// or remove the source column's tasks, and removing the reference leaves the
// source column and its tasks untouched on the source board.
func TestExternalColumn_ReadOnly(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	// Source board with a column holding a task.
	srcBid := testutil.MustCreateBoard(t, srv, pid)
	srcCol := testutil.MustAddColumn(t, srv, srcBid, "Source Lane", "PLANNED", 0)
	taskID := testutil.MustCreateTask(t, srv, pid, "Shared Task")
	mv := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+srcBid+"/move-task",
		map[string]interface{}{"taskId": taskID, "boardColumnId": srcCol, "boardRank": 1000}, testutil.DemoUserID)
	testutil.AssertStatus(t, mv, http.StatusOK)

	// Consuming board mirrors the source column read-only.
	dstResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/boards",
		map[string]interface{}{"name": "Dest"}, testutil.DemoUserID)
	var dst map[string]interface{}
	testutil.DecodeJSON(t, dstResp, &dst)
	dstBid := dst["id"].(string)

	addResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+dstBid+"/external-columns",
		map[string]interface{}{"sourceColumnId": srcCol}, testutil.DemoUserID)
	testutil.AssertStatus(t, addResp, http.StatusCreated)
	var ec map[string]interface{}
	testutil.DecodeJSON(t, addResp, &ec)
	ecID := ec["id"].(string)

	// Removing the read-only reference from the consuming board must NOT delete
	// the source column nor detach its tasks: the source data is untouched.
	del := testutil.Do(t, srv, http.MethodDelete, "/api/v1/boards/"+dstBid+"/external-columns/"+ecID, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, del, http.StatusNoContent)

	srcBoardResp := testutil.Do(t, srv, http.MethodGet, "/api/v1/boards/"+srcBid, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, srcBoardResp, http.StatusOK)
	var srcBoard map[string]interface{}
	testutil.DecodeJSON(t, srcBoardResp, &srcBoard)
	cols, _ := srcBoard["columns"].([]interface{})
	if len(cols) != 1 {
		t.Fatalf("source column must survive external-column removal, got %v", srcBoard["columns"])
	}
	taskResp := testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+taskID, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, taskResp, http.StatusOK)
	var task map[string]interface{}
	testutil.DecodeJSON(t, taskResp, &task)
	if task["boardColumnId"] != srcCol {
		t.Errorf("source task must remain in source column, boardColumnId = %v, want %v", task["boardColumnId"], srcCol)
	}
}

func TestExternalColumn_Delete(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	srcBid := testutil.MustCreateBoard(t, srv, pid)
	srcCol := testutil.MustAddColumn(t, srv, srcBid, "Lane", "PLANNED", 0)
	dstResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/boards",
		map[string]interface{}{"name": "Dest"}, testutil.DemoUserID)
	var dst map[string]interface{}
	testutil.DecodeJSON(t, dstResp, &dst)
	dstBid := dst["id"].(string)

	addResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+dstBid+"/external-columns",
		map[string]interface{}{"sourceColumnId": srcCol}, testutil.DemoUserID)
	var ec map[string]interface{}
	testutil.DecodeJSON(t, addResp, &ec)
	ecID := ec["id"].(string)

	del := testutil.Do(t, srv, http.MethodDelete, "/api/v1/boards/"+dstBid+"/external-columns/"+ecID, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, del, http.StatusNoContent)

	list := testutil.Do(t, srv, http.MethodGet, "/api/v1/boards/"+dstBid+"/external-columns", nil, testutil.DemoUserID)
	var cols []map[string]interface{}
	testutil.DecodeJSON(t, list, &cols)
	if len(cols) != 0 {
		t.Errorf("expected 0 external columns after delete, got %d", len(cols))
	}
}
