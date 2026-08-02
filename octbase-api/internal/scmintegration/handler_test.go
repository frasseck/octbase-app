package scmintegration_test

import (
	"net/http"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
)

func TestCreateRepoConnection_OK(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	resp := testutil.Do(t, srv, http.MethodPost,
		"/api/v1/projects/"+pid+"/repository-connections",
		map[string]string{
			"displayName":   "My Repo",
			"repositoryUrl": "https://gitlab.example.com/org/repo",
			"defaultBranch": "main",
		}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)

	var rc map[string]interface{}
	testutil.DecodeJSON(t, resp, &rc)
	if rc["displayName"] != "My Repo" {
		t.Errorf("displayName = %v", rc["displayName"])
	}
	if rc["provider"] != "FAKE_GITLAB" {
		t.Errorf("provider = %v, want FAKE_GITLAB", rc["provider"])
	}
	if rc["defaultBranch"] != "main" {
		t.Errorf("defaultBranch = %v, want main", rc["defaultBranch"])
	}
}

func TestCreateRepoConnection_DefaultProvider(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	resp := testutil.Do(t, srv, http.MethodPost,
		"/api/v1/projects/"+pid+"/repository-connections",
		map[string]string{"displayName": "Repo", "repositoryUrl": "https://example.com/repo"},
		testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var rc map[string]interface{}
	testutil.DecodeJSON(t, resp, &rc)
	if rc["provider"] != "FAKE_GITLAB" {
		t.Errorf("provider = %v, want FAKE_GITLAB", rc["provider"])
	}
	if rc["defaultBranch"] != "main" {
		t.Errorf("defaultBranch = %v, want main", rc["defaultBranch"])
	}
}

func TestListRepoConnections(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	testutil.Do(t, srv, http.MethodPost,
		"/api/v1/projects/"+pid+"/repository-connections",
		map[string]string{"displayName": "Repo1", "repositoryUrl": "https://example.com/1"},
		testutil.DemoUserID)

	resp := testutil.Do(t, srv, http.MethodGet,
		"/api/v1/projects/"+pid+"/repository-connections", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var rcs []interface{}
	testutil.DecodeJSON(t, resp, &rcs)
	if len(rcs) != 1 {
		t.Errorf("expected 1 repo connection, got %d", len(rcs))
	}
}

func TestListRepoConnections_OAuthAvailable(t *testing.T) {
	t.Setenv("OCTBASE_OAUTH_GITHUB_CLIENT_ID", "id")
	t.Setenv("OCTBASE_OAUTH_GITHUB_CLIENT_SECRET", "secret")
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	// GitHub connection (no token → validation skipped) is OAuth-available.
	testutil.Do(t, srv, http.MethodPost,
		"/api/v1/projects/"+pid+"/repository-connections",
		map[string]string{"provider": "GITHUB", "displayName": "gh", "repositoryUrl": "https://github.com/o/r"},
		testutil.DemoUserID)
	// Fake provider has no OAuth support.
	testutil.Do(t, srv, http.MethodPost,
		"/api/v1/projects/"+pid+"/repository-connections",
		map[string]string{"displayName": "fake", "repositoryUrl": "https://example.com/x"},
		testutil.DemoUserID)

	resp := testutil.Do(t, srv, http.MethodGet,
		"/api/v1/projects/"+pid+"/repository-connections", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var rcs []map[string]interface{}
	testutil.DecodeJSON(t, resp, &rcs)
	got := map[string]bool{}
	for _, rc := range rcs {
		avail, _ := rc["oauthAvailable"].(bool)
		got[rc["provider"].(string)] = avail
	}
	if !got["GITHUB"] {
		t.Errorf("GITHUB oauthAvailable = false, want true (creds configured)")
	}
	if got["FAKE_GITLAB"] {
		t.Errorf("FAKE_GITLAB oauthAvailable = true, want false")
	}
}

func TestUpdateRepoConnection(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	cr := testutil.Do(t, srv, http.MethodPost,
		"/api/v1/projects/"+pid+"/repository-connections",
		map[string]string{"displayName": "Old Name", "repositoryUrl": "https://example.com/repo"},
		testutil.DemoUserID)
	var rc map[string]interface{}
	testutil.DecodeJSON(t, cr, &rc)
	rcID := rc["id"].(string)

	resp := testutil.Do(t, srv, http.MethodPatch,
		"/api/v1/repository-connections/"+rcID,
		map[string]string{"displayName": "New Name", "defaultBranch": "develop"},
		testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var updated map[string]interface{}
	testutil.DecodeJSON(t, resp, &updated)
	if updated["displayName"] != "New Name" {
		t.Errorf("displayName = %v, want New Name", updated["displayName"])
	}
	if updated["defaultBranch"] != "develop" {
		t.Errorf("defaultBranch = %v, want develop", updated["defaultBranch"])
	}
}

func TestCreateBranch_OK(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "My Task")

	// First create a repo connection
	cr := testutil.Do(t, srv, http.MethodPost,
		"/api/v1/projects/"+pid+"/repository-connections",
		map[string]string{"displayName": "Repo", "repositoryUrl": "https://example.com/repo"},
		testutil.DemoUserID)
	var rc map[string]interface{}
	testutil.DecodeJSON(t, cr, &rc)
	rcID := rc["id"].(string)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/branches",
		map[string]string{
			"repositoryId": rcID,
			"branchName":   "feature/my-task",
			"branchType":   "feature",
		}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)

	var branch map[string]interface{}
	testutil.DecodeJSON(t, resp, &branch)
	if branch["branchName"] != "feature/my-task" {
		t.Errorf("branchName = %v", branch["branchName"])
	}
	if branch["branchType"] != "feature" {
		t.Errorf("branchType = %v, want feature", branch["branchType"])
	}
	if branch["taskId"] != tid {
		t.Errorf("taskId = %v, want %v", branch["taskId"], tid)
	}
}

func TestCreateBranch_DefaultType(t *testing.T) {
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

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/branches",
		map[string]string{"repositoryId": rc["id"].(string), "branchName": "my-branch"},
		testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var branch map[string]interface{}
	testutil.DecodeJSON(t, resp, &branch)
	if branch["branchType"] != "feature" {
		t.Errorf("default branchType = %v, want feature", branch["branchType"])
	}
}

func TestListBranches(t *testing.T) {
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

	testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/branches",
		map[string]string{"repositoryId": rc["id"].(string), "branchName": "feature/branch-1"},
		testutil.DemoUserID)
	testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/branches",
		map[string]string{"repositoryId": rc["id"].(string), "branchName": "feature/branch-2"},
		testutil.DemoUserID)

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+tid+"/branches", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var branches []interface{}
	testutil.DecodeJSON(t, resp, &branches)
	if len(branches) != 2 {
		t.Errorf("expected 2 branches, got %d", len(branches))
	}
}

func TestDeleteBranch_OK(t *testing.T) {
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
		map[string]string{"repositoryId": rc["id"].(string), "branchName": "feature/to-delete"},
		testutil.DemoUserID)
	testutil.AssertStatus(t, br, http.StatusCreated)
	var branch map[string]interface{}
	testutil.DecodeJSON(t, br, &branch)
	branchID := branch["id"].(string)

	resp := testutil.Do(t, srv, http.MethodDelete,
		"/api/v1/tasks/"+tid+"/branches/"+branchID, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNoContent)
	_ = resp.Body.Close()

	// Branch should no longer appear in the list.
	lr := testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+tid+"/branches", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, lr, http.StatusOK)
	var branches []interface{}
	testutil.DecodeJSON(t, lr, &branches)
	for _, b := range branches {
		bm := b.(map[string]interface{})
		if bm["id"] == branchID {
			t.Error("deleted branch still appears in list")
		}
	}
}

// ── Authorization boundary tests ────────────────────────────────────────────

func TestListRepoConnections_NonMember_Forbidden(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	resp := testutil.Do(t, srv, http.MethodGet,
		"/api/v1/projects/"+pid+"/repository-connections", nil, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
}

func TestUpdateRepoConnection_NonMember_Forbidden(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	cr := testutil.Do(t, srv, http.MethodPost,
		"/api/v1/projects/"+pid+"/repository-connections",
		map[string]string{"displayName": "Repo", "repositoryUrl": "https://example.com/r"},
		testutil.DemoUserID)
	testutil.AssertStatus(t, cr, http.StatusCreated)
	var rc map[string]interface{}
	testutil.DecodeJSON(t, cr, &rc)

	resp := testutil.Do(t, srv, http.MethodPatch,
		"/api/v1/repository-connections/"+rc["id"].(string),
		map[string]string{"displayName": "Hacked"},
		testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
}

func TestCreateBranch_NonMember_Forbidden(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/branches",
		map[string]string{"branchName": "feature/hack"},
		testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
}

func TestListBranches_NonMember_Forbidden(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+tid+"/branches", nil, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
}

func TestDeleteBranch_NonMember_Forbidden(t *testing.T) {
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
		map[string]string{"repositoryId": rc["id"].(string), "branchName": "feature/b"},
		testutil.DemoUserID)
	var branch map[string]interface{}
	testutil.DecodeJSON(t, br, &branch)

	resp := testutil.Do(t, srv, http.MethodDelete,
		"/api/v1/tasks/"+tid+"/branches/"+branch["id"].(string), nil, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
}

func TestDeleteRepoConnection_OK(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	cr := testutil.Do(t, srv, http.MethodPost,
		"/api/v1/projects/"+pid+"/repository-connections",
		map[string]string{"displayName": "MyRepo", "repositoryUrl": "https://example.com/myrepo"},
		testutil.DemoUserID)
	testutil.AssertStatus(t, cr, http.StatusCreated)
	var rc map[string]interface{}
	testutil.DecodeJSON(t, cr, &rc)
	rcID := rc["id"].(string)

	resp := testutil.Do(t, srv, http.MethodDelete,
		"/api/v1/repository-connections/"+rcID, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNoContent)
	_ = resp.Body.Close()

	// Should no longer appear in list.
	lr := testutil.Do(t, srv, http.MethodGet,
		"/api/v1/projects/"+pid+"/repository-connections", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, lr, http.StatusOK)
	var list []interface{}
	testutil.DecodeJSON(t, lr, &list)
	for _, item := range list {
		im := item.(map[string]interface{})
		if im["id"] == rcID {
			t.Error("deleted repo connection still appears in list")
		}
	}
}
