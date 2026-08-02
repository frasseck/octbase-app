package workmanagement_test

import (
	"net/http"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
)

func TestGetBoard_NotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/boards/"+missingID, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)
	_ = resp.Body.Close()
}

func TestUpdateBoard_NotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/boards/"+missingID,
		map[string]string{"name": "X"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)
	_ = resp.Body.Close()
}

func TestUpdateBoard_Viewer_Forbidden(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_VIEWER")
	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/boards/"+bid,
		map[string]string{"name": "X"}, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
	_ = resp.Body.Close()
}

func TestUpdateBoard_BadJSON(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)
	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/boards/"+bid, "not-an-object", testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusBadRequest)
	_ = resp.Body.Close()
}

func TestDeleteBoard_NotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	resp := testutil.Do(t, srv, http.MethodDelete, "/api/v1/boards/"+missingID, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)
	_ = resp.Body.Close()
}

func TestDeleteBoard_Viewer_Forbidden(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_VIEWER")
	resp := testutil.Do(t, srv, http.MethodDelete, "/api/v1/boards/"+bid, nil, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
	_ = resp.Body.Close()
}

func TestDeleteBoard_OK(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)
	resp := testutil.Do(t, srv, http.MethodDelete, "/api/v1/boards/"+bid, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNoContent)
	_ = resp.Body.Close()
}

func TestAddColumn_NotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+missingID+"/columns",
		map[string]interface{}{"name": "Todo", "status": "PLANNED", "position": 0}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)
	_ = resp.Body.Close()
}

func TestAddColumn_Viewer_Forbidden(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_VIEWER")
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+bid+"/columns",
		map[string]interface{}{"name": "Todo", "status": "PLANNED", "position": 0}, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
	_ = resp.Body.Close()
}

func TestMoveTask_NotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+missingID+"/move-task",
		map[string]interface{}{"taskId": missingID}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)
	_ = resp.Body.Close()
}

func TestMoveTask_TaskNotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)
	col := testutil.MustAddColumn(t, srv, bid, "Todo", "PLANNED", 0)
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+bid+"/move-task",
		map[string]interface{}{"taskId": missingID, "boardColumnId": col, "boardRank": 0}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)
	_ = resp.Body.Close()
}

func TestMoveTask_Viewer_Forbidden(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_VIEWER")
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+bid+"/move-task",
		map[string]interface{}{"taskId": missingID}, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
	_ = resp.Body.Close()
}

func TestRemoveTaskFromBoard_NotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+missingID+"/remove-task",
		map[string]interface{}{"taskId": missingID}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)
	_ = resp.Body.Close()
}

func TestRemoveTaskFromBoard_TaskNotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+bid+"/remove-task",
		map[string]interface{}{"taskId": missingID}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)
	_ = resp.Body.Close()
}

func TestListExternalColumns_NotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/boards/"+missingID+"/external-columns", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)
	_ = resp.Body.Close()
}

func TestListExternalColumns_NonMember_Forbidden(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)
	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/boards/"+bid+"/external-columns", nil, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
	_ = resp.Body.Close()
}

func TestAddExternalColumn_MissingSource(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+bid+"/external-columns",
		map[string]interface{}{"position": 0}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
	_ = resp.Body.Close()
}

func TestAddExternalColumn_SourceNotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+bid+"/external-columns",
		map[string]interface{}{"sourceColumnId": missingID, "position": 0}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)
	_ = resp.Body.Close()
}

func TestDeleteExternalColumn_NotFoundBoard(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	resp := testutil.Do(t, srv, http.MethodDelete, "/api/v1/boards/"+missingID+"/external-columns/"+missingID, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)
	_ = resp.Body.Close()
}
