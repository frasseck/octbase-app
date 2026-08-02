// Package webhooks receives and validates push/PR events from Bitbucket and
// GitHub via HMAC-SHA256 signatures, then updates branch reference states.
package webhooks

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"

	"github.com/octbase/octbase-api/internal/shared"
	"github.com/octbase/octbase-api/internal/sse"
)

// BranchPRUpdater records a pull request's state on the branch reference it
// belongs to. Satisfied by *scmintegration.BranchReferenceRepo.
type BranchPRUpdater interface {
	UpdatePRStatus(branchName, prStatus, prURL string, prNumber int) error
}

// TaskStatusSetter transitions tasks to a new status, stamping done_at like
// every other status-changing path. Satisfied by *workmanagement.TaskRepo.
type TaskStatusSetter interface {
	BulkSetStatus(projectID string, taskIDs []string, status, now string) ([]string, error)
}

// Handler handles incoming SCM webhooks.
type Handler struct {
	db       *sql.DB
	branches BranchPRUpdater
	tasks    TaskStatusSetter
	hub      *sse.Hub
}

// NewHandler creates a new webhook Handler.
func NewHandler(
	db *sql.DB,
	branches BranchPRUpdater,
	tasks TaskStatusSetter,
	hub *sse.Hub,
) *Handler {
	return &Handler{db: db, branches: branches, tasks: tasks, hub: hub}
}

const webhookBodyLimit = 10 << 20 // 10 MiB

// HandleBitbucket processes Bitbucket push and pull-request events.
func (h *Handler) HandleBitbucket(w http.ResponseWriter, r *http.Request) {
	secret := os.Getenv("OCTBASE_WEBHOOK_SECRET_BITBUCKET")
	if secret == "" {
		shared.WriteError(w, http.StatusForbidden, "FORBIDDEN", "webhook not configured")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, webhookBodyLimit)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "bad request")
		return
	}

	if !validateHMAC(secret, body, r.Header.Get("X-Hub-Signature-256")) {
		shared.WriteError(w, http.StatusForbidden, "FORBIDDEN", "invalid signature")
		return
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON")
		return
	}

	event := r.Header.Get("X-Event-Key")
	h.processBitbucketEvent(event, payload)
	w.WriteHeader(http.StatusOK)
}

// HandleGitHub processes GitHub pull_request events.
func (h *Handler) HandleGitHub(w http.ResponseWriter, r *http.Request) {
	secret := os.Getenv("OCTBASE_WEBHOOK_SECRET_GITHUB")
	if secret == "" {
		shared.WriteError(w, http.StatusForbidden, "FORBIDDEN", "webhook not configured")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, webhookBodyLimit)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "bad request")
		return
	}

	if !validateHMAC(secret, body, r.Header.Get("X-Hub-Signature-256")) {
		shared.WriteError(w, http.StatusForbidden, "FORBIDDEN", "invalid signature")
		return
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON")
		return
	}

	event := r.Header.Get("X-GitHub-Event")
	h.processGitHubEvent(event, payload)
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) processBitbucketEvent(event string, p map[string]any) {
	switch event {
	case "pullrequest:created":
		h.updateBranchPR(p, "open")
	case "pullrequest:fulfilled":
		h.updateBranchPR(p, "merged")
		h.maybeAutoCloseTask(p)
	case "pullrequest:rejected":
		h.updateBranchPR(p, "declined")
	default:
		slog.Debug("unhandled Bitbucket event", "event", event)
	}
}

func (h *Handler) processGitHubEvent(event string, p map[string]any) {
	if event != "pull_request" {
		return
	}
	action, _ := p["action"].(string)
	pr, _ := p["pull_request"].(map[string]any)
	if pr == nil {
		return
	}
	merged, _ := pr["merged"].(bool)

	switch action {
	case "opened":
		h.updateGitHubBranchPR(pr, "open")
	case "closed":
		if merged {
			h.updateGitHubBranchPR(pr, "merged")
			h.maybeAutoCloseGitHubTask(pr)
		} else {
			h.updateGitHubBranchPR(pr, "declined")
		}
	}
}

func (h *Handler) updateBranchPR(p map[string]any, status string) {
	pr, _ := p["pullrequest"].(map[string]any)
	if pr == nil {
		return
	}
	src, _ := pr["source"].(map[string]any)
	if src == nil {
		return
	}
	branch, _ := src["branch"].(map[string]any)
	branchName, _ := branch["name"].(string)
	// Use safe nested lookups: a malformed payload missing links/html must not
	// panic the handler goroutine on attacker-controlled webhook input.
	prURL := nestedString(pr, "links", "html", "href")

	var prNum int
	if id, ok := pr["id"].(float64); ok {
		prNum = int(id)
	}

	if err := h.branches.UpdatePRStatus(branchName, status, prURL, prNum); err != nil {
		slog.Error("failed to update PR status", "branch", branchName, "error", err)
	}
}

func (h *Handler) updateGitHubBranchPR(pr map[string]any, status string) {
	head, _ := pr["head"].(map[string]any)
	branchName, _ := head["ref"].(string)
	htmlURL, _ := pr["html_url"].(string)
	var prNum int
	if n, ok := pr["number"].(float64); ok {
		prNum = int(n)
	}
	if err := h.branches.UpdatePRStatus(branchName, status, htmlURL, prNum); err != nil {
		slog.Error("failed to update GitHub PR status", "branch", branchName, "error", err)
	}
}

func (h *Handler) maybeAutoCloseTask(p map[string]any) {
	pr, _ := p["pullrequest"].(map[string]any)
	if pr == nil {
		return
	}
	src, _ := pr["source"].(map[string]any)
	if src == nil {
		return
	}
	branch, _ := src["branch"].(map[string]any)
	branchName, _ := branch["name"].(string)
	h.autoCloseTaskByBranch(branchName)
}

func (h *Handler) maybeAutoCloseGitHubTask(pr map[string]any) {
	head, _ := pr["head"].(map[string]any)
	branchName, _ := head["ref"].(string)
	h.autoCloseTaskByBranch(branchName)
}

func (h *Handler) autoCloseTaskByBranch(branchName string) {
	if branchName == "" {
		return
	}
	// Find the task linked to this branch and check auto_close_on_merge.
	var taskID, projectID string
	err := h.db.QueryRow(`
		SELECT t.id, t.project_id
		  FROM branch_references br
		  JOIN tasks t ON t.id = br.task_id
		  JOIN repository_connections rc ON rc.id = br.repository_id
		 WHERE br.branch_name = $1 AND rc.auto_close_on_merge = true
		 LIMIT 1`, branchName,
	).Scan(&taskID, &projectID)
	if err != nil {
		return
	}

	// Route through TaskRepo.BulkSetStatus rather than a raw UPDATE so done_at
	// gets stamped exactly like every other status-changing path — otherwise a
	// task auto-closed via merge webhook would never qualify for auto-archive.
	if _, err := h.tasks.BulkSetStatus(projectID, []string{taskID}, "DONE", shared.Now()); err != nil {
		slog.Error("auto-close task failed", "taskId", taskID, "error", err)
		return
	}

	h.hub.Publish(projectID, map[string]any{
		"type":   "task.status_changed",
		"taskId": taskID,
		"status": "DONE",
	})
}

// nestedString walks a chain of nested map[string]any keys and returns
// the final value as a string, or "" if any level is missing or the wrong type.
// It never panics on malformed/partial payloads.
func nestedString(m map[string]any, keys ...string) string {
	cur := m
	for i, k := range keys {
		if i == len(keys)-1 {
			s, _ := cur[k].(string)
			return s
		}
		next, ok := cur[k].(map[string]any)
		if !ok {
			return ""
		}
		cur = next
	}
	return ""
}

// validateHMAC verifies the HMAC-SHA256 signature on the request body.
func validateHMAC(secret string, body []byte, sig string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(sig), []byte(expected))
}
