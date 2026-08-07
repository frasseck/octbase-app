package scmintegration_test

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/octbase/octbase-api/internal/scmintegration"
	"github.com/octbase/octbase-api/internal/shared"
	"github.com/octbase/octbase-api/internal/testutil"
)

// connectGitHubNoToken creates a GITHUB connection without a token (so no
// validation network call happens) — used as the target of an OAuth flow.
func connectGitHubNoToken(t *testing.T, srv *httptest.Server, pid string) map[string]interface{} {
	t.Helper()
	cr := testutil.Do(t, srv, http.MethodPost,
		"/api/v1/projects/"+pid+"/repository-connections",
		map[string]string{
			"provider": "GITHUB", "displayName": "Repo",
			"repositoryUrl": "https://github.com/org/repo",
		}, testutil.DemoUserID)
	var rc map[string]interface{}
	testutil.DecodeJSON(t, cr, &rc)
	return rc
}

func TestCreatePullRequest_GitHub(t *testing.T) {
	setEncKey(t)
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/org/repo":
			_, _ = w.Write([]byte(`{"default_branch":"trunk"}`))
		case "/repos/org/repo/git/ref/heads/trunk":
			_, _ = w.Write([]byte(`{"object":{"sha":"abc"}}`))
		case "/repos/org/repo/git/refs":
			w.WriteHeader(http.StatusCreated)
		case "/repos/org/repo/pulls":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"number":42,"html_url":"https://github.com/org/repo/pull/42","state":"open"}`))
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	}))
	defer gh.Close()

	rc := connectGitHub(t, srv, pid, gh.URL) // validates + stores token
	// Create a branch first.
	br := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/branches",
		map[string]string{"repositoryId": rc["id"].(string), "branchName": "feature/x"},
		testutil.DemoUserID)
	var branch map[string]interface{}
	testutil.DecodeJSON(t, br, &branch)

	resp := testutil.Do(t, srv, http.MethodPost,
		"/api/v1/tasks/"+tid+"/branches/"+branch["id"].(string)+"/pull-request",
		map[string]string{"title": "My PR", "body": "desc"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var pr map[string]interface{}
	testutil.DecodeJSON(t, resp, &pr)
	if pr["number"].(float64) != 42 {
		t.Errorf("pr number = %v, want 42", pr["number"])
	}

	// The branch reference should now carry the PR metadata.
	list := testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+tid+"/branches", nil, testutil.DemoUserID)
	var branches []map[string]interface{}
	testutil.DecodeJSON(t, list, &branches)
	if len(branches) != 1 || branches[0]["prStatus"] != "open" {
		t.Errorf("branch PR status not recorded: %+v", branches)
	}
}

func TestOAuthAuthorize_ReturnsURL(t *testing.T) {
	setEncKey(t)
	t.Setenv("OCTBASE_OAUTH_GITHUB_CLIENT_ID", "cid")
	t.Setenv("OCTBASE_OAUTH_GITHUB_CLIENT_SECRET", "sec")
	t.Setenv("OCTBASE_OAUTH_GITHUB_AUTH_URL", "https://provider.example/authorize")
	t.Setenv("OCTBASE_OAUTH_REDIRECT_BASE", "https://api.example")

	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	rc := connectGitHubNoToken(t, srv, pid)

	resp := testutil.Do(t, srv, http.MethodGet,
		"/api/v1/repository-connections/"+rc["id"].(string)+"/oauth/authorize", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var out map[string]string
	testutil.DecodeJSON(t, resp, &out)
	loc, _ := url.Parse(out["authorizeUrl"])
	if loc.Host != "provider.example" || loc.Query().Get("state") == "" || loc.Query().Get("client_id") != "cid" {
		t.Errorf("bad authorize url: %s", out["authorizeUrl"])
	}
}

// seedOAuthCallback stubs the GitHub token endpoint and seeds an unexpired
// state row, exactly as the authorize endpoint would, leaving the callback
// ready to be called. Returns the state and the connection id.
func seedOAuthCallback(t *testing.T, db *sql.DB, srv *httptest.Server, pid string) (state, rcID string) {
	t.Helper()
	rc := connectGitHubNoToken(t, srv, pid)
	rcID = rc["id"].(string)

	// Stub the GitHub token endpoint.
	tokenStub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"gho_new","refresh_token":"ghr_new","expires_in":3600}`))
	}))
	t.Cleanup(tokenStub.Close)
	t.Setenv("OCTBASE_OAUTH_GITHUB_CLIENT_ID", "cid")
	t.Setenv("OCTBASE_OAUTH_GITHUB_CLIENT_SECRET", "sec")
	t.Setenv("OCTBASE_OAUTH_GITHUB_TOKEN_URL", tokenStub.URL)

	// Seed a state row directly, as the authorize endpoint would.
	states := scmintegration.NewOAuthStateRepo(db)
	state = shared.NewUUID()
	if err := states.Create(&scmintegration.OAuthState{
		State: state, Provider: "GITHUB", RepositoryID: rcID, UserID: testutil.DemoUserID,
		ExpiresAt: shared.Now()[:19] + "Z", // not relied upon; replaced below
	}, shared.Now()); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	// Use a comfortably future expiry.
	if _, err := db.Exec(`UPDATE oauth_states SET expires_at=$1 WHERE state=$2`, "2999-01-01T00:00:00Z", state); err != nil {
		t.Fatalf("set expiry: %v", err)
	}
	return state, rcID
}

func TestOAuthCallback_StoresTokens(t *testing.T) {
	setEncKey(t)
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	// The callback has two success branches: it answers 200 JSON, or — when
	// OCTBASE_APP_URL is set — a 302 to that URL. This test asserts the JSON
	// one, so pin the variable instead of inheriting whatever the environment
	// happens to hold. Without this the branch was chosen by ambient state, and
	// testutil.Do follows redirects with http.DefaultClient: exporting a
	// stack's .env to pick up TEST_DATABASE_URL was enough to send this test
	// out to the real host named by OCTBASE_APP_URL, where it failed on that
	// host's TLS certificate and pointed nowhere near the actual cause.
	// TestOAuthCallback_RedirectsToAppURL covers the other branch.
	t.Setenv("OCTBASE_APP_URL", "")

	state, rcID := seedOAuthCallback(t, db, srv, pid)

	resp := testutil.Do(t, srv, http.MethodGet,
		"/api/v1/oauth/github/callback?code=abc&state="+state, nil, "")
	testutil.AssertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()

	// Connection should now be OAUTH with a stored (encrypted) token.
	repo := scmintegration.NewRepositoryConnectionRepo(db)
	got, err := repo.FindByID(rcID)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if got.AuthKind != "OAUTH" || got.AccessToken == "" || got.RefreshToken == "" {
		t.Errorf("connection not updated: authKind=%q token?%v refresh?%v", got.AuthKind, got.AccessToken != "", got.RefreshToken != "")
	}
	dec, _ := shared.DecryptSecret(got.AccessToken)
	if dec != "gho_new" {
		t.Errorf("stored access token = %q, want gho_new", dec)
	}
}

// TestOAuthCallback_RedirectsToAppURL covers the callback's other success
// branch: with OCTBASE_APP_URL set it sends the browser back to the app
// instead of answering JSON. That branch had no coverage at all, which is why
// nothing noticed that reaching it from a test meant leaving the machine.
//
// The request deliberately does NOT go through testutil.Do. What matters is
// the Location header, and testutil.Do follows redirects with
// http.DefaultClient — following this one would issue a real request to
// whatever OCTBASE_APP_URL names. The value below is an unroutable
// .invalid host (RFC 2606), so a regression that starts following it fails
// loudly here rather than reaching a real server.
func TestOAuthCallback_RedirectsToAppURL(t *testing.T) {
	setEncKey(t)
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	const appURL = "https://app.example.invalid"
	t.Setenv("OCTBASE_APP_URL", appURL)

	state, _ := seedOAuthCallback(t, db, srv, pid)

	req, err := http.NewRequest(http.MethodGet,
		srv.URL+"/api/v1/oauth/github/callback?code=abc&state="+state, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusFound {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusFound)
	}
	if loc := resp.Header.Get("Location"); loc != appURL {
		t.Errorf("Location = %q, want %q", loc, appURL)
	}
}

func TestOAuthCallback_InvalidState(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	resp := testutil.Do(t, srv, http.MethodGet,
		"/api/v1/oauth/github/callback?code=abc&state=does-not-exist", nil, "")
	testutil.AssertStatus(t, resp, http.StatusBadRequest)
	_ = resp.Body.Close()
}

func TestOAuthRefresh_RotatesToken(t *testing.T) {
	setEncKey(t)
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	rc := connectGitHubNoToken(t, srv, pid)
	rcID := rc["id"].(string)

	// Promote the connection to OAUTH with an (encrypted) refresh token.
	repo := scmintegration.NewRepositoryConnectionRepo(db)
	conn, _ := repo.FindByID(rcID)
	conn.AuthKind = "OAUTH"
	conn.RefreshToken, _ = shared.EncryptSecret("ghr_old")
	conn.AccessToken, _ = shared.EncryptSecret("gho_old")
	conn.UpdatedAt = shared.Now()
	if err := repo.Update(conn); err != nil {
		t.Fatalf("update: %v", err)
	}

	tokenStub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err == nil && r.Form.Get("grant_type") != "refresh_token" {
			t.Errorf("grant_type = %q, want refresh_token", r.Form.Get("grant_type"))
		}
		_, _ = w.Write([]byte(`{"access_token":"gho_rotated","refresh_token":"ghr_rotated","expires_in":7200}`))
	}))
	defer tokenStub.Close()
	t.Setenv("OCTBASE_OAUTH_GITHUB_CLIENT_ID", "cid")
	t.Setenv("OCTBASE_OAUTH_GITHUB_CLIENT_SECRET", "sec")
	t.Setenv("OCTBASE_OAUTH_GITHUB_TOKEN_URL", tokenStub.URL)

	resp := testutil.Do(t, srv, http.MethodPost,
		"/api/v1/repository-connections/"+rcID+"/oauth/refresh", map[string]string{}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()

	got, _ := repo.FindByID(rcID)
	dec, _ := shared.DecryptSecret(got.AccessToken)
	if dec != "gho_rotated" {
		t.Errorf("rotated access token = %q, want gho_rotated", dec)
	}
	if got.TokenExpiresAt == "" {
		t.Errorf("expected token_expires_at to be set after rotation")
	}
}

func TestOAuthRefresh_NotOAuth(t *testing.T) {
	setEncKey(t)
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	rc := connectGitHubNoToken(t, srv, pid)
	resp := testutil.Do(t, srv, http.MethodPost,
		"/api/v1/repository-connections/"+rc["id"].(string)+"/oauth/refresh", map[string]string{}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusBadRequest)
	_ = resp.Body.Close()
}
