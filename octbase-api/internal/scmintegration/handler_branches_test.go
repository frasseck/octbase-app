package scmintegration_test

import (
	"net/http"
	"testing"

	"github.com/octbase/octbase-api/internal/scmintegration"
	"github.com/octbase/octbase-api/internal/testutil"
)

func TestCreateRepoConnection_Member_Forbidden(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_MEMBER")

	// A non-owner member may read but not create a repository connection.
	resp := testutil.Do(t, srv, http.MethodPost,
		"/api/v1/projects/"+pid+"/repository-connections",
		map[string]string{"displayName": "R", "repositoryUrl": "https://example.com/r"},
		testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
	_ = resp.Body.Close()
}

func TestCreateRepoConnection_BadJSON(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	resp := testutil.Do(t, srv, http.MethodPost,
		"/api/v1/projects/"+pid+"/repository-connections",
		"not-an-object", testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusBadRequest)
	_ = resp.Body.Close()
}

func TestUpdateRepoConnection_NotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPatch,
		"/api/v1/repository-connections/00000000-0000-0000-0000-0000000000ff",
		map[string]string{"displayName": "X"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)
	_ = resp.Body.Close()
}

func TestUpdateRepoConnection_BadJSON(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	cr := testutil.Do(t, srv, http.MethodPost,
		"/api/v1/projects/"+pid+"/repository-connections",
		map[string]string{"displayName": "R", "repositoryUrl": "https://example.com/r"},
		testutil.DemoUserID)
	var rc map[string]interface{}
	testutil.DecodeJSON(t, cr, &rc)

	resp := testutil.Do(t, srv, http.MethodPatch,
		"/api/v1/repository-connections/"+rc["id"].(string),
		"not-an-object", testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusBadRequest)
	_ = resp.Body.Close()
}

func TestDeleteRepoConnection_NotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodDelete,
		"/api/v1/repository-connections/00000000-0000-0000-0000-0000000000ff",
		nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)
	_ = resp.Body.Close()
}

func TestDeleteRepoConnection_Member_Forbidden(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_MEMBER")
	cr := testutil.Do(t, srv, http.MethodPost,
		"/api/v1/projects/"+pid+"/repository-connections",
		map[string]string{"displayName": "R", "repositoryUrl": "https://example.com/r"},
		testutil.DemoUserID)
	var rc map[string]interface{}
	testutil.DecodeJSON(t, cr, &rc)

	resp := testutil.Do(t, srv, http.MethodDelete,
		"/api/v1/repository-connections/"+rc["id"].(string), nil, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
	_ = resp.Body.Close()
}

func TestCreateBranch_TaskNotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPost,
		"/api/v1/tasks/00000000-0000-0000-0000-0000000000ff/branches",
		map[string]string{"branchName": "feature/x"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)
	_ = resp.Body.Close()
}

func TestCreateBranch_BadJSON(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	resp := testutil.Do(t, srv, http.MethodPost,
		"/api/v1/tasks/"+tid+"/branches", "not-an-object", testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusBadRequest)
	_ = resp.Body.Close()
}

func TestListBranches_TaskNotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodGet,
		"/api/v1/tasks/00000000-0000-0000-0000-0000000000ff/branches", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)
	_ = resp.Body.Close()
}

func TestDeleteBranch_NotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	resp := testutil.Do(t, srv, http.MethodDelete,
		"/api/v1/tasks/"+tid+"/branches/00000000-0000-0000-0000-0000000000ff",
		nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)
	_ = resp.Body.Close()
}

// TestUpdatePRStatus_Repo exercises the BranchReferenceRepo.UpdatePRStatus method,
// which is otherwise only reached via the webhook receivers (which use a stub repo).
func TestUpdatePRStatus_Repo(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	cr := testutil.Do(t, srv, http.MethodPost,
		"/api/v1/projects/"+pid+"/repository-connections",
		map[string]string{"displayName": "R", "repositoryUrl": "https://example.com/r"},
		testutil.DemoUserID)
	var rc map[string]interface{}
	testutil.DecodeJSON(t, cr, &rc)
	br := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/branches",
		map[string]string{"repositoryId": rc["id"].(string), "branchName": "feature/pr"},
		testutil.DemoUserID)
	testutil.AssertStatus(t, br, http.StatusCreated)
	_ = br.Body.Close()

	repo := scmintegration.NewBranchReferenceRepo(db)
	if err := repo.UpdatePRStatus("feature/pr", "OPEN", "https://example.com/pr/1", 1); err != nil {
		t.Fatalf("UpdatePRStatus: %v", err)
	}

	branches, err := repo.ListByTask(tid)
	if err != nil {
		t.Fatalf("ListByTask: %v", err)
	}
	if len(branches) != 1 {
		t.Fatalf("expected 1 branch, got %d", len(branches))
	}
	if branches[0].PRStatus == nil || *branches[0].PRStatus != "OPEN" {
		t.Errorf("PR status not updated: %v", branches[0].PRStatus)
	}
	if branches[0].PRNumber == nil || *branches[0].PRNumber != 1 {
		t.Errorf("PR number not updated: %v", branches[0].PRNumber)
	}
}
