package webhooks_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	"github.com/octbase/octbase-api/internal/workmanagement"
)

const (
	testBitbucketSecret = "test-bb-secret"
	testGitHubSecret    = "test-gh-secret"
)

// signBody returns the HMAC-SHA256 signature in "sha256=<hex>" format.
func signBody(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// doWebhook sends a POST request to path with a signed body.
func doWebhook(t *testing.T, srv *httptest.Server, path, secret string, payload interface{}, extraHeaders map[string]string) *http.Response {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hub-Signature-256", signBody(secret, body))
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	return resp
}

// buildWebhookServer sets up a real chi router with webhook routes and the required DB repos.
func buildWebhookServer(t *testing.T) *httptest.Server {
	t.Helper()
	sqlDB := testutil.NewTestDB(t)
	if sqlDB == nil {
		return nil
	}
	branchRepo := scmintegration.NewBranchReferenceRepo(sqlDB)
	taskRepo := workmanagement.NewTaskRepo(sqlDB)
	hub := sse.NewHub()
	go hub.Run()

	handler := webhooks.NewHandler(sqlDB, branchRepo, taskRepo, hub)

	r := chi.NewRouter()
	r.Post("/api/v1/webhooks/bitbucket", handler.HandleBitbucket)
	r.Post("/api/v1/webhooks/github", handler.HandleGitHub)

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func TestHandleBitbucket_NotConfigured(t *testing.T) {
	srv := buildWebhookServer(t)
	if srv == nil {
		return
	}
	// Unset secret so handler reports "not configured".
	t.Setenv("OCTBASE_WEBHOOK_SECRET_BITBUCKET", "")

	body, _ := json.Marshal(map[string]string{"test": "data"})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/webhooks/bitbucket", bytes.NewReader(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 when secret not set, got %d", resp.StatusCode)
	}
}

func TestHandleBitbucket_InvalidSignature(t *testing.T) {
	srv := buildWebhookServer(t)
	if srv == nil {
		return
	}
	t.Setenv("OCTBASE_WEBHOOK_SECRET_BITBUCKET", testBitbucketSecret)

	body, _ := json.Marshal(map[string]string{"test": "data"})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/webhooks/bitbucket", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", "sha256=invalidsignature")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 for invalid signature, got %d", resp.StatusCode)
	}
}

func TestHandleBitbucket_InvalidJSON(t *testing.T) {
	srv := buildWebhookServer(t)
	if srv == nil {
		return
	}
	t.Setenv("OCTBASE_WEBHOOK_SECRET_BITBUCKET", testBitbucketSecret)

	body := []byte(`not valid json {{{`)
	sig := signBody(testBitbucketSecret, body)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/webhooks/bitbucket", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sig)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid JSON, got %d", resp.StatusCode)
	}
}

func TestHandleBitbucket_PushEvent(t *testing.T) {
	srv := buildWebhookServer(t)
	if srv == nil {
		return
	}
	t.Setenv("OCTBASE_WEBHOOK_SECRET_BITBUCKET", testBitbucketSecret)

	payload := map[string]interface{}{
		"push": map[string]interface{}{
			"changes": []interface{}{},
		},
	}
	resp := doWebhook(t, srv, "/api/v1/webhooks/bitbucket", testBitbucketSecret, payload,
		map[string]string{"X-Event-Key": "repo:push"})
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for valid push event, got %d", resp.StatusCode)
	}
}

func TestHandleGitHub_NotConfigured(t *testing.T) {
	srv := buildWebhookServer(t)
	if srv == nil {
		return
	}
	t.Setenv("OCTBASE_WEBHOOK_SECRET_GITHUB", "")

	body, _ := json.Marshal(map[string]string{"test": "data"})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/webhooks/github", bytes.NewReader(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 when secret not set, got %d", resp.StatusCode)
	}
}

func TestHandleGitHub_PushEvent(t *testing.T) {
	srv := buildWebhookServer(t)
	if srv == nil {
		return
	}
	t.Setenv("OCTBASE_WEBHOOK_SECRET_GITHUB", testGitHubSecret)

	payload := map[string]interface{}{
		"ref":     "refs/heads/main",
		"commits": []interface{}{},
	}
	resp := doWebhook(t, srv, "/api/v1/webhooks/github", testGitHubSecret, payload,
		map[string]string{"X-GitHub-Event": "push"})
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for valid GitHub push event, got %d", resp.StatusCode)
	}
}

func TestHandleGitHub_AutoCloseTask(t *testing.T) {
	sqlDB := testutil.NewTestDB(t)
	if sqlDB == nil {
		return
	}
	t.Setenv("OCTBASE_WEBHOOK_SECRET_GITHUB", testGitHubSecret)

	branchRepo := scmintegration.NewBranchReferenceRepo(sqlDB)
	taskRepo := workmanagement.NewTaskRepo(sqlDB)
	hub := sse.NewHub()
	go hub.Run()

	handler := webhooks.NewHandler(sqlDB, branchRepo, taskRepo, hub)
	r := chi.NewRouter()
	r.Post("/api/v1/webhooks/github", handler.HandleGitHub)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	// Create a project + task + repo + branch with auto_close_on_merge.
	now := shared.Now()
	projectID := shared.NewUUID()
	if _, err := sqlDB.Exec(
		`INSERT INTO projects (id, name, slug, visibility, created_by_user_id, created_at, updated_at) VALUES ($1,$2,$3,'PUBLIC',$4,$5,$6)`,
		projectID, "AutoClose Project", "autoclose-project", testutil.DemoUserID, now, now,
	); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := sqlDB.Exec(
		`INSERT INTO memberships (id, project_id, user_id, role, created_at, updated_at) VALUES ($1,$2,$3,'PROJECT_ADMIN',$4,$5)`,
		shared.NewUUID(), projectID, testutil.DemoUserID, now, now,
	); err != nil {
		t.Fatalf("create membership: %v", err)
	}
	taskID := shared.NewUUID()
	if _, err := sqlDB.Exec(
		`INSERT INTO tasks (id, project_id, title, task_type, status, priority, board_rank, seq_number, created_at, updated_at, version) VALUES ($1,$2,'AC Task','TASK','PLANNED','MEDIUM',1000,1,$3,$4,1)`,
		taskID, projectID, now, now,
	); err != nil {
		t.Fatalf("create task: %v", err)
	}
	repoID := shared.NewUUID()
	if _, err := sqlDB.Exec(
		`INSERT INTO repository_connections (id, project_id, display_name, repository_url, provider, default_branch, auto_close_on_merge, created_at, updated_at) VALUES ($1,$2,'Repo','https://github.com/test/repo','GITHUB','main',true,$3,$4)`,
		repoID, projectID, now, now,
	); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	branchName := fmt.Sprintf("feature/task-%s", taskID[:8])
	if _, err := sqlDB.Exec(
		`INSERT INTO branch_references (id, task_id, repository_id, branch_name, branch_type, created_at) VALUES ($1,$2,$3,$4,'feature',$5)`,
		shared.NewUUID(), taskID, repoID, branchName, now,
	); err != nil {
		t.Fatalf("create branch: %v", err)
	}

	// Send a GitHub PR merged event for this branch.
	payload := map[string]interface{}{
		"action": "closed",
		"pull_request": map[string]interface{}{
			"merged":   true,
			"number":   42,
			"html_url": "https://github.com/test/repo/pull/42",
			"head":     map[string]interface{}{"ref": branchName},
		},
	}
	resp := doWebhook(t, srv, "/api/v1/webhooks/github", testGitHubSecret, payload,
		map[string]string{"X-GitHub-Event": "pull_request"})
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for auto-close webhook, got %d", resp.StatusCode)
	}

	// Verify the task status was set to DONE.
	var status string
	if err := sqlDB.QueryRow(`SELECT status FROM tasks WHERE id = $1`, taskID).Scan(&status); err != nil {
		t.Fatalf("query task status: %v", err)
	}
	if status != "DONE" {
		t.Errorf("expected task status DONE after auto-close, got %s", status)
	}
}
