package workmanagement_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
)

const missingID = "00000000-0000-0000-0000-0000000000ff"

// ── Release error branches ──────────────────────────────────────────────────

func TestUpdateRelease_NotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/releases/"+missingID,
		map[string]string{"name": "X"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)
	_ = resp.Body.Close()
}

func TestUpdateRelease_BadJSON(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	rid := testutil.MustCreateRelease(t, srv, pid, "R")
	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/releases/"+rid,
		"not-an-object", testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusBadRequest)
	_ = resp.Body.Close()
}

func TestUpdateRelease_Viewer_Forbidden(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	rid := testutil.MustCreateRelease(t, srv, pid, "R")
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_VIEWER")
	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/releases/"+rid,
		map[string]string{"name": "X"}, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
	_ = resp.Body.Close()
}

func TestReopenRelease_NotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/releases/"+missingID+"/reopen",
		nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)
	_ = resp.Body.Close()
}

func TestReopenRelease_Viewer_Forbidden(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	rid := testutil.MustCreateRelease(t, srv, pid, "R")
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_VIEWER")
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/releases/"+rid+"/reopen",
		nil, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
	_ = resp.Body.Close()
}

func TestDeleteRelease_NotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	resp := testutil.Do(t, srv, http.MethodDelete, "/api/v1/releases/"+missingID,
		nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)
	_ = resp.Body.Close()
}

func TestDeleteRelease_Viewer_Forbidden(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	rid := testutil.MustCreateRelease(t, srv, pid, "R")
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_VIEWER")
	resp := testutil.Do(t, srv, http.MethodDelete, "/api/v1/releases/"+rid,
		nil, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
	_ = resp.Body.Close()
}

func TestCreateRelease_Viewer_Forbidden(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_VIEWER")
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/releases",
		map[string]string{"name": "R"}, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
	_ = resp.Body.Close()
}

func TestCreateRelease_BadJSON(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/releases",
		"not-an-object", testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusBadRequest)
	_ = resp.Body.Close()
}

// ── Private-project visibility on GetProject ────────────────────────────────

func TestGetProject_Private_NonMember_Forbidden(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	cr := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects",
		map[string]string{"name": "Secret", "visibility": "PRIVATE"}, testutil.DemoUserID)
	testutil.AssertStatus(t, cr, http.StatusCreated)
	var p map[string]interface{}
	testutil.DecodeJSON(t, cr, &p)
	pid := p["id"].(string)

	// A non-member must not learn the project exists: read access requires
	// membership, so the response is 404 (non-disclosure) rather than 403.
	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid, nil, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)
	_ = resp.Body.Close()
}

func TestGetProject_Private_Member_OK(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	cr := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects",
		map[string]string{"name": "Secret", "visibility": "PRIVATE"}, testutil.DemoUserID)
	testutil.AssertStatus(t, cr, http.StatusCreated)
	var p map[string]interface{}
	testutil.DecodeJSON(t, cr, &p)
	pid := p["id"].(string)
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_VIEWER")

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid, nil, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()
}

func TestGetProject_Private_SuperAdmin_OK(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	cr := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects",
		map[string]string{"name": "Secret", "visibility": "PRIVATE"}, testutil.DemoUserID)
	testutil.AssertStatus(t, cr, http.StatusCreated)
	var p map[string]interface{}
	testutil.DecodeJSON(t, cr, &p)
	pid := p["id"].(string)

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid, nil, testutil.SuperAdminUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()
}

// ── UpdateProject reachable branches ────────────────────────────────────────

func TestUpdateProject_BlankName(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/projects/"+pid,
		map[string]string{"name": "   "}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
	_ = resp.Body.Close()
}

func TestUpdateProject_FullFields(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/projects/"+pid,
		map[string]string{"name": "New", "abbreviation": "np", "description": "d", "visibility": "PRIVATE"},
		testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var p map[string]interface{}
	testutil.DecodeJSON(t, resp, &p)
	if p["abbreviation"] != "NP" {
		t.Errorf("abbreviation = %v, want NP (uppercased)", p["abbreviation"])
	}
}

// ── jira CSV export exercises formatJiraDueDate via a task with a due date ───

func TestExportJiraCSV_WithDueDate(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "Has due date")
	// Set a due date so the exporter formats it through formatJiraDueDate.
	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/tasks/"+tid,
		map[string]string{"dueDate": "2026-09-15"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()

	exp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/export/jira-csv", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, exp, http.StatusOK)
	defer func() { _ = exp.Body.Close() }()
	buf := make([]byte, 4096)
	n, _ := exp.Body.Read(buf)
	if !strings.Contains(string(buf[:n]), "15/Sep/26") {
		t.Errorf("expected Jira-formatted due date 15/Sep/26 in export, got: %s", string(buf[:n]))
	}
}
