package webhooks_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/octbase/octbase-api/internal/scmintegration"
	"github.com/octbase/octbase-api/internal/shared"
	"github.com/octbase/octbase-api/internal/sse"
	"github.com/octbase/octbase-api/internal/testutil"
	"github.com/octbase/octbase-api/internal/webhooks"
)

// bitbucketPR builds a minimal Bitbucket pullrequest payload for branchName.
func bitbucketPR(branchName string) map[string]interface{} {
	return map[string]interface{}{
		"pullrequest": map[string]interface{}{
			"id":     float64(7),
			"source": map[string]interface{}{"branch": map[string]interface{}{"name": branchName}},
			"links":  map[string]interface{}{"html": map[string]interface{}{"href": "https://bitbucket.org/x/pr/7"}},
		},
	}
}

func TestHandleBitbucket_PRCreated(t *testing.T) {
	srv := buildWebhookServer(t)
	if srv == nil {
		return
	}
	t.Setenv("OCTBASE_WEBHOOK_SECRET_BITBUCKET", testBitbucketSecret)
	resp := doWebhook(t, srv, "/api/v1/webhooks/bitbucket", testBitbucketSecret,
		bitbucketPR("feature/new"), map[string]string{"X-Event-Key": "pullrequest:created"})
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestHandleBitbucket_PRRejected(t *testing.T) {
	srv := buildWebhookServer(t)
	if srv == nil {
		return
	}
	t.Setenv("OCTBASE_WEBHOOK_SECRET_BITBUCKET", testBitbucketSecret)
	resp := doWebhook(t, srv, "/api/v1/webhooks/bitbucket", testBitbucketSecret,
		bitbucketPR("feature/x"), map[string]string{"X-Event-Key": "pullrequest:rejected"})
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

// TestHandleBitbucket_MissingFields covers the nil-guard early returns in
// updateBranchPR and maybeAutoCloseTask (no pullrequest, no source, no branch).
func TestHandleBitbucket_MissingFields(t *testing.T) {
	srv := buildWebhookServer(t)
	if srv == nil {
		return
	}
	t.Setenv("OCTBASE_WEBHOOK_SECRET_BITBUCKET", testBitbucketSecret)

	cases := []struct {
		name    string
		payload map[string]interface{}
	}{
		{"no pullrequest", map[string]interface{}{}},
		{"no source", map[string]interface{}{"pullrequest": map[string]interface{}{"id": float64(1)}}},
		{"empty branch", map[string]interface{}{"pullrequest": map[string]interface{}{
			"id":     float64(1),
			"source": map[string]interface{}{"branch": map[string]interface{}{}},
		}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := doWebhook(t, srv, "/api/v1/webhooks/bitbucket", testBitbucketSecret,
				c.payload, map[string]string{"X-Event-Key": "pullrequest:fulfilled"})
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Errorf("status = %d, want 200", resp.StatusCode)
			}
		})
	}
}

// TestHandleBitbucket_FulfilledAutoCloses sets up a task whose branch belongs to
// an auto-close repo, then fulfils the PR and asserts the task moves to DONE.
func TestHandleBitbucket_FulfilledAutoCloses(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	t.Setenv("OCTBASE_WEBHOOK_SECRET_BITBUCKET", testBitbucketSecret)
	srv, taskID, branchName := autoCloseServer(t, db, "BITBUCKET")

	resp := doWebhook(t, srv, "/api/v1/webhooks/bitbucket", testBitbucketSecret,
		bitbucketPR(branchName), map[string]string{"X-Event-Key": "pullrequest:fulfilled"})
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var status string
	if err := db.QueryRow(`SELECT status FROM tasks WHERE id = $1`, taskID).Scan(&status); err != nil {
		t.Fatalf("query status: %v", err)
	}
	if status != "DONE" {
		t.Errorf("task status = %q, want DONE", status)
	}
}

// TestAutoClose_NoMatchingBranch covers autoCloseTaskByBranch's no-match return.
func TestAutoClose_NoMatchingBranch(t *testing.T) {
	srv := buildWebhookServer(t)
	if srv == nil {
		return
	}
	t.Setenv("OCTBASE_WEBHOOK_SECRET_BITBUCKET", testBitbucketSecret)
	resp := doWebhook(t, srv, "/api/v1/webhooks/bitbucket", testBitbucketSecret,
		bitbucketPR("no-such-branch"), map[string]string{"X-Event-Key": "pullrequest:fulfilled"})
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestHandleGitHub_Opened(t *testing.T) {
	srv := buildWebhookServer(t)
	if srv == nil {
		return
	}
	t.Setenv("OCTBASE_WEBHOOK_SECRET_GITHUB", testGitHubSecret)
	payload := map[string]interface{}{
		"action": "opened",
		"pull_request": map[string]interface{}{
			"number":   float64(5),
			"html_url": "https://github.com/x/repo/pull/5",
			"head":     map[string]interface{}{"ref": "feature/open"},
		},
	}
	resp := doWebhook(t, srv, "/api/v1/webhooks/github", testGitHubSecret, payload,
		map[string]string{"X-GitHub-Event": "pull_request"})
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestHandleGitHub_ClosedNotMerged(t *testing.T) {
	srv := buildWebhookServer(t)
	if srv == nil {
		return
	}
	t.Setenv("OCTBASE_WEBHOOK_SECRET_GITHUB", testGitHubSecret)
	payload := map[string]interface{}{
		"action": "closed",
		"pull_request": map[string]interface{}{
			"merged":   false,
			"number":   float64(6),
			"html_url": "https://github.com/x/repo/pull/6",
			"head":     map[string]interface{}{"ref": "feature/declined"},
		},
	}
	resp := doWebhook(t, srv, "/api/v1/webhooks/github", testGitHubSecret, payload,
		map[string]string{"X-GitHub-Event": "pull_request"})
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

// TestHandleGitHub_NoPullRequest covers the pr==nil early return.
func TestHandleGitHub_NoPullRequest(t *testing.T) {
	srv := buildWebhookServer(t)
	if srv == nil {
		return
	}
	t.Setenv("OCTBASE_WEBHOOK_SECRET_GITHUB", testGitHubSecret)
	resp := doWebhook(t, srv, "/api/v1/webhooks/github", testGitHubSecret,
		map[string]interface{}{"action": "closed"}, map[string]string{"X-GitHub-Event": "pull_request"})
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestHandleGitHub_InvalidSignature(t *testing.T) {
	srv := buildWebhookServer(t)
	if srv == nil {
		return
	}
	t.Setenv("OCTBASE_WEBHOOK_SECRET_GITHUB", testGitHubSecret)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/webhooks/github", nil)
	req.Header.Set("X-Hub-Signature-256", "sha256=deadbeef")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

func TestHandleGitHub_InvalidJSON(t *testing.T) {
	srv := buildWebhookServer(t)
	if srv == nil {
		return
	}
	t.Setenv("OCTBASE_WEBHOOK_SECRET_GITHUB", testGitHubSecret)
	body := []byte(`not valid json {{{`)
	sig := signBody(testGitHubSecret, body)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sig)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// TestHandleGitHub_NonPREvent covers processGitHubEvent's early return for any
// event that is not "pull_request".
func TestHandleGitHub_NonPREvent(t *testing.T) {
	srv := buildWebhookServer(t)
	if srv == nil {
		return
	}
	t.Setenv("OCTBASE_WEBHOOK_SECRET_GITHUB", testGitHubSecret)
	resp := doWebhook(t, srv, "/api/v1/webhooks/github", testGitHubSecret,
		map[string]interface{}{"zen": "ping"}, map[string]string{"X-GitHub-Event": "ping"})
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

// autoCloseServer wires a webhook server and seeds a project/task/auto-close
// repo/branch, returning the server, the task id, and the branch name.
func autoCloseServer(t *testing.T, sqlDB *sql.DB, provider string) (*httptest.Server, string, string) {
	t.Helper()
	now := shared.Now()
	projectID := shared.NewUUID()
	if _, err := sqlDB.Exec(
		`INSERT INTO projects (id, name, slug, visibility, created_by_user_id, created_at, updated_at) VALUES ($1,$2,$3,'PUBLIC',$4,$5,$6)`,
		projectID, "AC "+provider, "ac-"+provider, testutil.DemoUserID, now, now,
	); err != nil {
		t.Fatalf("project: %v", err)
	}
	taskID := shared.NewUUID()
	if _, err := sqlDB.Exec(
		`INSERT INTO tasks (id, project_id, title, task_type, status, priority, board_rank, seq_number, created_at, updated_at, version) VALUES ($1,$2,'AC Task','TASK','PLANNED','MEDIUM',1000,1,$3,$4,1)`,
		taskID, projectID, now, now,
	); err != nil {
		t.Fatalf("task: %v", err)
	}
	repoID := shared.NewUUID()
	if _, err := sqlDB.Exec(
		`INSERT INTO repository_connections (id, project_id, display_name, repository_url, provider, default_branch, auto_close_on_merge, created_at, updated_at) VALUES ($1,$2,'Repo','https://example/repo',$3,'main',true,$4,$5)`,
		repoID, projectID, provider, now, now,
	); err != nil {
		t.Fatalf("repo: %v", err)
	}
	branchName := fmt.Sprintf("feature/task-%s", taskID[:8])
	if _, err := sqlDB.Exec(
		`INSERT INTO branch_references (id, task_id, repository_id, branch_name, branch_type, created_at) VALUES ($1,$2,$3,$4,'feature',$5)`,
		shared.NewUUID(), taskID, repoID, branchName, now,
	); err != nil {
		t.Fatalf("branch: %v", err)
	}

	branchRepo := scmintegration.NewBranchReferenceRepo(sqlDB)
	hub := sse.NewHub()
	go hub.Run()
	handler := webhooks.NewHandler(sqlDB, branchRepo, newWMHandler(sqlDB), hub)

	r := chi.NewRouter()
	r.Post("/api/v1/webhooks/bitbucket", handler.HandleBitbucket)
	r.Post("/api/v1/webhooks/github", handler.HandleGitHub)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv, taskID, branchName
}
