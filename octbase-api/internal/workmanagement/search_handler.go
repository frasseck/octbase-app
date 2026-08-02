package workmanagement

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/octbase/octbase-api/internal/auditlog"
	"github.com/octbase/octbase-api/internal/rbac"
	"github.com/octbase/octbase-api/internal/shared"
)

func (h *Handler) SearchTasks(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	if _, ok := h.memberGuard(w, r, projectID); !ok {
		return
	}
	q := r.URL.Query().Get("q")
	if len(q) > 500 {
		shared.WriteError(w, http.StatusBadRequest, "QUERY_TOO_LONG", "search query must be ≤ 500 characters")
		return
	}
	if len(q) < minSearchQueryLen {
		// Same threshold and same no-error shape as the unified and page searches.
		// Two reasons here: pg_trgm extracts no trigram from a shorter pattern, and
		// an *empty* q made the ILIKE `%%`, i.e. "every task in the project" — a
		// full scan dressed up as a search. Neither has a caller worth keeping.
		shared.WriteJSON(w, http.StatusOK, []Task{})
		return
	}
	pg := shared.ParsePagination(r)
	ts, err := h.tasks.SearchByTitle(projectID, q, pg.Page, pg.Size)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, ts)
}

// minSearchQueryLen is the shortest query the trigram-indexed searches accept.
// pg_trgm cannot extract a trigram from a pattern shorter than three characters,
// so a 1- or 2-character `%q%` bypasses the GIN indexes from migration 022 and
// degrades into a sequential scan of tasks/pages/projects. Below the threshold the
// search answers 200 with no results rather than scanning.
const minSearchQueryLen = 3

// UnifiedSearch searches across tasks, pages, and projects visible to the user.
// Query must be at least minSearchQueryLen characters. Returns up to 5 results
// per category.
func (h *Handler) UnifiedSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if len(q) < minSearchQueryLen {
		shared.WriteJSON(w, http.StatusOK, map[string]any{
			"tasks": []any{}, "pages": []any{}, "projects": []any{},
		})
		return
	}
	projectID := r.URL.Query().Get("projectId")
	userID := shared.GetUserID(r)

	tasks, err := h.tasks.UnifiedSearchTasks(userID, projectID, q, 5)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	pages, err := h.pages.SearchPages(userID, projectID, q, 5)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	projects, err := h.projects.SearchVisible(userID, q, 5)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, map[string]any{
		"tasks":    tasks,
		"pages":    pages,
		"projects": projects,
	})
}

// GetDashboard returns the personal dashboard for the authenticated user.
func (h *Handler) GetDashboard(w http.ResponseWriter, r *http.Request) {
	userID := shared.GetUserID(r)
	if userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
		return
	}

	assigned, err := h.tasks.GetAssignedTasks(userID, 20)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	reviewing, err := h.tasks.GetReviewingTasks(userID, 10)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	recentPages, err := h.pages.GetRecentByAuthor(userID, 5)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	upcomingReleases, err := h.releases.GetUpcoming(userID, 14, 5)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}

	// Projects and boards the user can access, for the My Work screen. Both
	// respect read access: super admins see all projects, everyone else only
	// the projects they are a member of.
	var projects []Project
	if shared.GetGlobalRole(r) == rbac.GlobalSuperAdmin {
		projects, err = h.projects.ListAll(0, 50)
	} else {
		projects, err = h.projects.List(userID, 0, 50)
	}
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	// One query for all of them, not one per project: with the 50-project page
	// size above, the loop this replaced cost up to 50 round trips per dashboard
	// load. ListByProjects preserves the same flattened order (grouped by project
	// in this list's order, by created_at within a project).
	projectIDs := make([]string, 0, len(projects))
	for _, p := range projects {
		projectIDs = append(projectIDs, p.ID)
	}
	boards, err := h.boards.ListByProjects(projectIDs)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]any{
		"assignedTasks":    assigned,
		"reviewingTasks":   reviewing,
		"recentPages":      recentPages,
		"upcomingReleases": upcomingReleases,
		"projects":         projects,
		"boards":           boards,
	})
}

// maxBulkTaskIDs caps how many tasks one bulk action may address. Every branch is
// now a constant number of queries, but the request still binds the whole ID array
// into one statement, so an unbounded list is an unbounded amount of work in a
// single query. 500 is far above what the board's selection UI can produce.
const maxBulkTaskIDs = 500

// batchActivityWriter is the batched counterpart of ActivityWriter, satisfied by
// *activity.Repo. Declared here (like txActivityWriter) so a writer that only
// implements Write still works — writeBulkActivity falls back to a per-task loop.
type batchActivityWriter interface {
	WriteBatch(projectID string, taskIDs []string, actorID, actType string, params map[string]any) error
}

// writeBulkActivity records one activity entry per task ID. Activity logging stays
// explicit and per-task (a bulk status change must remain replayable for the
// sprint burndown); the batched writer only collapses N inserts into one
// statement. It deliberately does not go through writeActivity: a bulk op
// broadcasts a single project-scoped board refresh instead of one event per task,
// so board viewers re-render once.
func (h *Handler) writeBulkActivity(projectID string, taskIDs []string, actorID, actType string, params map[string]any) {
	if len(taskIDs) == 0 {
		return
	}
	if bw, ok := h.activity.(batchActivityWriter); ok {
		_ = bw.WriteBatch(projectID, taskIDs, actorID, actType, params)
		return
	}
	for _, id := range taskIDs {
		_ = h.activity.Write(projectID, id, actorID, actType, params)
	}
}

// BulkUpdateTasks applies one action to multiple tasks in a single transaction.
// realignBulkStatusPlacement brings the cards of a bulk status change back in
// step with the status that was just written, using the same rules as the
// single-task endpoint: a boarded card moves to its board's lane for the new
// status, and an unplaced task in an in-flight status is put on the board so it
// stops being listed as backlog. See alignBoardColumnToStatus and
// placeUnboardedTaskForStatus for the rules and their exceptions.
//
// One task at a time on purpose. The rank of an appended card is read from the
// lane it joins, so writing each card before computing the next is what keeps
// several cards landing in the same lane from all claiming the same rank.
func (h *Handler) realignBulkStatusPlacement(projectID string, ids []string, now string) error {
	tasks, err := h.tasks.ListByIDs(projectID, ids)
	if err != nil {
		return err
	}
	for i := range tasks {
		t := &tasks[i]
		before := t.BoardColumnID
		if err := h.alignBoardColumnToStatus(t); err != nil {
			return err
		}
		if before == t.BoardColumnID || (before != nil && t.BoardColumnID != nil && *before == *t.BoardColumnID) {
			continue
		}
		if err := h.tasks.SetBoardPlacement(t.ID, t.BoardColumnID, t.BoardRank, now); err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler) BulkUpdateTasks(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	_, ok := h.writerGuard(w, r, projectID)
	if !ok {
		return
	}
	var req struct {
		TaskIDs []string `json:"taskIds"`
		Action  string   `json:"action"`
		Value   string   `json:"value"`
	}
	if err := shared.DecodeJSON(r, &req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON")
		return
	}
	if len(req.TaskIDs) == 0 {
		shared.WriteError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "taskIds must not be empty")
		return
	}
	if len(req.TaskIDs) > maxBulkTaskIDs {
		shared.WriteError(w, http.StatusUnprocessableEntity, "BULK_TOO_MANY_TASKS",
			fmt.Sprintf("taskIds must not contain more than %d tasks", maxBulkTaskIDs))
		return
	}
	if req.Action == "set_status" {
		req.Value = strings.TrimSpace(req.Value)
		allowed, err := h.statusAllowed(projectID, req.Value)
		if err != nil {
			shared.WriteServerError(w, r, err)
			return
		}
		if !allowed {
			shared.WriteError(w, http.StatusUnprocessableEntity, "INVALID_STATUS", "unknown status value")
			return
		}
		// Same completion rule as the single-task status change, over the whole
		// selection at once: the guard rejects the request as soon as any task is
		// blocked, so this stays two queries rather than N (completionGuard).
		if req.Value == StatusDone && !h.completionGuard(w, r, req.TaskIDs) {
			return
		}
	}
	if req.Action == "set_priority" {
		allowed, err := h.priorityAllowed(projectID, req.Value)
		if err != nil {
			shared.WriteServerError(w, r, err)
			return
		}
		if !allowed {
			shared.WriteError(w, http.StatusUnprocessableEntity, "INVALID_PRIORITY", "unknown priority value")
			return
		}
	}

	now := shared.Now()
	actorID := shared.GetUserID(r)
	var (
		ids     []string
		updated int
		err     error
	)
	switch req.Action {
	case "set_status":
		ids, err = h.tasks.BulkSetStatus(projectID, req.TaskIDs, req.Value, now)
		updated = len(ids)
		// The bulk SET writes status and nothing else, so the cards it moved are
		// left saying something different from their tasks: a boarded card sits
		// in the lane for its OLD status, and an unboarded one stays in the
		// backlog wearing an in-flight label. Realign them exactly as the
		// single-task endpoint does (OCT-303).
		if err == nil {
			err = h.realignBulkStatusPlacement(projectID, ids, now)
		}
		// One entry per affected task, like a single status change, so bulk
		// transitions stay replayable for the sprint burndown. No "from": the
		// bulk update is a blind per-row SET and never read the old status.
		h.writeBulkActivity(projectID, ids, actorID, "TASK_STATUS_CHANGED", map[string]any{"status": req.Value})
	case "set_priority":
		ids, err = h.tasks.BulkSetPriority(projectID, req.TaskIDs, req.Value, now)
		updated = len(ids)
	case "set_assignee":
		ids, err = h.tasks.BulkSetAssignee(projectID, req.TaskIDs, req.Value, now)
		updated = len(ids)
	case "set_release":
		ids, err = h.tasks.BulkSetRelease(projectID, req.TaskIDs, req.Value, now)
		updated = len(ids)
	case "archive":
		ids, err = h.tasks.BulkArchive(projectID, req.TaskIDs, now)
		updated = len(ids)
		h.writeBulkActivity(projectID, ids, actorID, "TASK_ARCHIVED", map[string]any{"status": StatusArchived})
	case "delete":
		// Collect uploaded-file storage keys before the cascade removes rows —
		// one query for the whole selection, not one per task.
		var fileKeys []string
		if h.storage != nil {
			if ks, kErr := h.attachments.StorageKeysForTasks(req.TaskIDs); kErr == nil {
				fileKeys = ks
			}
		}
		var deleted []Task
		deleted, err = h.tasks.BulkDelete(projectID, req.TaskIDs)
		updated = len(deleted)
		if err == nil {
			for _, k := range fileKeys {
				_ = h.storage.Remove(k)
			}
			for _, t := range deleted {
				h.audit.Write(actorID, auditlog.ActionTaskDeleted, "task", t.ID,
					fmt.Sprintf(`{"projectId":%q,"title":%q}`, t.ProjectID, t.Title), "", "")
			}
		}
	default:
		shared.WriteError(w, http.StatusBadRequest, "INVALID_ACTION", "unknown bulk action")
		return
	}
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}

	// A bulk action (status/priority/assignee/release/archive/delete) changes
	// many cards at once; broadcast a single project-scoped board refresh so
	// co-workers viewing the board update once, covering the actions that write
	// no per-task activity (priority/assignee/release/delete).
	if updated > 0 {
		h.publishBoardEvent(projectID, "", actorID, "BULK_"+req.Action)
	}

	shared.WriteJSON(w, http.StatusOK, map[string]int{"updated": updated})
}
