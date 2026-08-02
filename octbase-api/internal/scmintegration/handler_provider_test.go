package scmintegration_test

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
)

// setEncKey configures a deterministic 32-byte AES key for token encryption.
func setEncKey(t *testing.T) {
	t.Helper()
	t.Setenv("OCTBASE_SCM_ENC_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
}

// githubStub serves the minimal GitHub REST surface the provider touches.
func githubStub(t *testing.T, created *bool) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/org/repo":
			_, _ = w.Write([]byte(`{"default_branch":"trunk"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/repos/org/repo/git/ref/heads/trunk":
			_, _ = w.Write([]byte(`{"object":{"sha":"deadbeef"}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/repos/org/repo/git/refs":
			if created != nil {
				*created = true
			}
			w.WriteHeader(http.StatusCreated)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
}

func connectGitHub(t *testing.T, srv *httptest.Server, pid, apiBase string) map[string]interface{} {
	t.Helper()
	cr := testutil.Do(t, srv, http.MethodPost,
		"/api/v1/projects/"+pid+"/repository-connections",
		map[string]string{
			"provider":      "GITHUB",
			"displayName":   "Repo",
			"repositoryUrl": "https://github.com/org/repo",
			"apiBaseUrl":    apiBase,
			"accessToken":   "ghp_token",
		}, testutil.DemoUserID)
	var rc map[string]interface{}
	testutil.DecodeJSON(t, cr, &rc)
	return rc
}

func TestCreateRepoConnection_GitHubValidates(t *testing.T) {
	setEncKey(t)
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	gh := githubStub(t, nil)
	defer gh.Close()

	rc := connectGitHub(t, srv, pid, gh.URL)
	// Default branch backfilled from the live repo, token never serialized.
	if rc["defaultBranch"] != "trunk" {
		t.Errorf("defaultBranch = %v, want trunk (from provider)", rc["defaultBranch"])
	}
	if _, leaked := rc["accessToken"]; leaked {
		t.Errorf("accessToken must not be serialized in the response")
	}
}

func TestCreateRepoConnection_GitHubRepoNotFound(t *testing.T) {
	setEncKey(t)
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer gh.Close()

	resp := testutil.Do(t, srv, http.MethodPost,
		"/api/v1/projects/"+pid+"/repository-connections",
		map[string]string{
			"provider": "GITHUB", "displayName": "Repo",
			"repositoryUrl": "https://github.com/org/repo",
			"apiBaseUrl":    gh.URL, "accessToken": "bad",
		}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)
	_ = resp.Body.Close()
}

func TestCreateBranch_GitHubCreatesRemote(t *testing.T) {
	setEncKey(t)
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	var created bool
	gh := githubStub(t, &created)
	defer gh.Close()

	rc := connectGitHub(t, srv, pid, gh.URL)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/branches",
		map[string]string{"repositoryId": rc["id"].(string), "branchName": "feature/x"},
		testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	_ = resp.Body.Close()
	if !created {
		t.Errorf("expected the remote branch to be created via the provider")
	}
}

func TestCreateBranch_GitHubBranchExists_Links(t *testing.T) {
	setEncKey(t)
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	// The branch already exists on the remote: creation is rejected, but the
	// existence lookup confirms it, so the API links it instead of failing.
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/org/repo":
			_, _ = w.Write([]byte(`{"default_branch":"trunk"}`))
		case r.URL.Path == "/repos/org/repo/git/ref/heads/trunk":
			_, _ = w.Write([]byte(`{"object":{"sha":"deadbeef"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/repos/org/repo/git/ref/heads/feature/x":
			_, _ = w.Write([]byte(`{"object":{"sha":"cafe"}}`)) // branch exists
		case r.Method == http.MethodPost && r.URL.Path == "/repos/org/repo/git/refs":
			w.WriteHeader(http.StatusUnprocessableEntity) // already exists
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer gh.Close()

	rc := connectGitHub(t, srv, pid, gh.URL)
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/branches",
		map[string]string{"repositoryId": rc["id"].(string), "branchName": "feature/x"},
		testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	_ = resp.Body.Close()

	// The existing branch should be linked (one persisted reference).
	list := testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+tid+"/branches", nil, testutil.DemoUserID)
	var branches []interface{}
	testutil.DecodeJSON(t, list, &branches)
	if len(branches) != 1 {
		t.Errorf("expected the existing branch to be linked (1 reference), got %d", len(branches))
	}
}

func TestCreateBranch_GitHubInvalidName_Fails(t *testing.T) {
	setEncKey(t)
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	// Creation is rejected and the branch does not exist (404): the name was
	// invalid, so the request fails and nothing is persisted.
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/org/repo":
			_, _ = w.Write([]byte(`{"default_branch":"trunk"}`))
		case r.URL.Path == "/repos/org/repo/git/ref/heads/trunk":
			_, _ = w.Write([]byte(`{"object":{"sha":"deadbeef"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/repos/org/repo/git/ref/heads/bad..name":
			w.WriteHeader(http.StatusNotFound) // branch does not exist
		case r.Method == http.MethodPost && r.URL.Path == "/repos/org/repo/git/refs":
			w.WriteHeader(http.StatusUnprocessableEntity) // rejected as invalid
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer gh.Close()

	rc := connectGitHub(t, srv, pid, gh.URL)
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/branches",
		map[string]string{"repositoryId": rc["id"].(string), "branchName": "bad..name"},
		testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusConflict)
	_ = resp.Body.Close()

	// No branch reference should have been persisted for an invalid name.
	list := testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+tid+"/branches", nil, testutil.DemoUserID)
	var branches []interface{}
	testutil.DecodeJSON(t, list, &branches)
	if len(branches) != 0 {
		t.Errorf("expected 0 persisted branches for an invalid name, got %d", len(branches))
	}
}

func TestCreateRepoConnection_TokenMissingEncKey(t *testing.T) {
	t.Setenv("OCTBASE_SCM_ENC_KEY", "") // encryption not configured
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"default_branch":"main"}`))
	}))
	defer gh.Close()

	resp := testutil.Do(t, srv, http.MethodPost,
		"/api/v1/projects/"+pid+"/repository-connections",
		map[string]string{
			"provider": "GITHUB", "displayName": "Repo",
			"repositoryUrl": "https://github.com/org/repo",
			"apiBaseUrl":    gh.URL, "accessToken": "ghp_token",
		}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusBadRequest)
	_ = resp.Body.Close()
}
