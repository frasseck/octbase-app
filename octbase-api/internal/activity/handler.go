package activity

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/octbase/octbase-api/internal/shared"
)

// Handler holds activity HTTP handlers.
type Handler struct {
	db   *sql.DB
	repo *Repo
}

func NewHandler(db *sql.DB, repo *Repo) *Handler { return &Handler{db: db, repo: repo} }

func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Get("/api/v1/projects/{projectId}/activity", h.ListProjectActivity)
	r.Get("/api/v1/tasks/{taskId}/activity", h.ListTaskActivity)
}

// memberGuard checks project membership. SUPER_ADMIN bypasses membership checks.
func (h *Handler) memberGuard(w http.ResponseWriter, r *http.Request, projectID string) bool {
	_, ok := shared.ProjectMemberGuard(h.db, w, r, projectID)
	return ok
}

// PageSize is the default number of activity entries per page. An activity row
// is one line, so a screenful is far more than the 20 a task list defaults to;
// 50 is a page a reader actually scrolls rather than one they immediately have
// to click past. ?size= overrides it up to shared.MaxPageSize.
const PageSize = 50

func (h *Handler) ListProjectActivity(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	if !h.memberGuard(w, r, projectID) {
		return
	}
	pg := shared.ParsePaginationSize(r, PageSize)
	entries, err := h.repo.ListByProject(projectID, pg.Page, pg.Size)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, entries)
}

func (h *Handler) ListTaskActivity(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskId")
	// Look up the task's project to enforce membership.
	var projectID string
	err := h.db.QueryRow(`SELECT project_id FROM tasks WHERE id = $1`, taskID).Scan(&projectID)
	if errors.Is(err, sql.ErrNoRows) {
		shared.WriteError(w, http.StatusNotFound, "TASK_NOT_FOUND", "task not found")
		return
	}
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if !h.memberGuard(w, r, projectID) {
		return
	}
	pg := shared.ParsePaginationSize(r, PageSize)
	entries, err := h.repo.ListByTask(taskID, pg.Page, pg.Size)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, entries)
}
