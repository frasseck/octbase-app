package workmanagement

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/octbase/octbase-api/internal/shared"
)

func (h *Handler) CreateSprint(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	_, ok := h.writerGuard(w, r, projectID)
	if !ok {
		return
	}
	var req struct {
		Name      string  `json:"name"`
		Goal      string  `json:"goal"`
		StartDate *string `json:"startDate"`
		EndDate   *string `json:"endDate"`
		ReleaseID *string `json:"releaseId"`
	}
	if err := shared.DecodeJSON(r, &req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		shared.WriteValidationError(w, "VALIDATION_ERROR", "sprint name must not be blank", "name")
		return
	}
	now := shared.Now()
	s := &Sprint{
		ID: shared.NewUUID(), ProjectID: projectID, Name: req.Name,
		Goal: req.Goal, StartDate: req.StartDate, EndDate: req.EndDate,
		Status: SprintStatusPlanned, ReleaseID: req.ReleaseID,
		CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	if !h.checkSprintDates(w, r, s) {
		return
	}
	if err := h.sprints.Create(s); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	// Provision the sprint board immediately so the sprint can be planned (tasks
	// dragged from the backlog onto the board) while it is still PLANNED. The
	// board lives from creation until the sprint is completed or deleted.
	if err := h.svc.provisionSprintBoard(s); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusCreated, s)
}

func (h *Handler) ListSprints(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	if _, ok := h.memberGuard(w, r, projectID); !ok {
		return
	}
	ss, err := h.sprints.ListByProject(projectID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	fillLiveCountsBatch(h.sprints, ss)
	shared.WriteJSON(w, http.StatusOK, ss)
}

// fillLiveCountsBatch is the list-sized fillLiveCounts: one grouped aggregate for
// the whole page instead of a 3-table aggregate per sprint. The overlay rules are
// identical — completed sprints keep their snapshot, every other sprint is
// overwritten with its board's current membership (0/0 when its board holds
// nothing, which is what the map's missing key means), and a query error leaves
// every sprint's stored counts untouched.
func fillLiveCountsBatch(repo *SprintRepo, ss []Sprint) {
	live := make([]string, 0, len(ss))
	for i := range ss {
		if ss[i].Status != SprintStatusCompleted {
			live = append(live, ss[i].ID)
		}
	}
	if len(live) == 0 {
		return
	}
	counts, err := repo.CountTasksBySprints(live)
	if err != nil {
		return
	}
	for i := range ss {
		if ss[i].Status == SprintStatusCompleted {
			continue
		}
		c := counts[ss[i].ID]
		ss[i].CommittedCount = c.Total
		ss[i].CompletedCount = c.Done
	}
}

// fillLiveCounts overlays a non-completed sprint's committed/completed counts
// with its board's current membership, so the sprint card always reflects what
// is on the board right now. A completed sprint keeps the counts snapshotted at
// completion: its board has been removed, so the live count would read 0.
func (h *Handler) fillLiveCounts(s *Sprint) {
	if s.Status == SprintStatusCompleted {
		return
	}
	total, done, err := h.sprints.CountTasks(s.ID)
	if err != nil {
		return
	}
	s.CommittedCount = total
	s.CompletedCount = done
}

func (h *Handler) GetSprint(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "sprintId")
	s, err := h.sprints.FindByID(id)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if s == nil {
		shared.WriteError(w, http.StatusNotFound, "SPRINT_NOT_FOUND", "sprint not found")
		return
	}
	if _, ok := h.memberGuard(w, r, s.ProjectID); !ok {
		return
	}
	h.fillLiveCounts(s)
	shared.WriteJSON(w, http.StatusOK, s)
}

func (h *Handler) UpdateSprint(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "sprintId")
	s, err := h.sprints.FindByID(id)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if s == nil {
		shared.WriteError(w, http.StatusNotFound, "SPRINT_NOT_FOUND", "sprint not found")
		return
	}
	if s.Status == SprintStatusCompleted {
		shared.WriteError(w, http.StatusUnprocessableEntity, "SPRINT_COMPLETED", "cannot edit a completed sprint")
		return
	}
	_, ok := h.writerGuard(w, r, s.ProjectID)
	if !ok {
		return
	}
	var req struct {
		Name      *string `json:"name"`
		Goal      *string `json:"goal"`
		StartDate *string `json:"startDate"`
		EndDate   *string `json:"endDate"`
		ReleaseID *string `json:"releaseId"`
		// Version, when sent, is the version the client's edit is based on; the
		// guarded update rejects the write with 409 if the sprint has moved on.
		Version *int `json:"version"`
	}
	// `status` is a dedicated-route rejection: sprints transition only via
	// their start/complete routes, and PATCHing it used to answer 200 while
	// changing nothing.
	if !shared.DecodePatch(w, r,
		map[string]bool{
			"name": true, "goal": true, "startDate": true, "endDate": true,
			"releaseId": true, "version": true,
		},
		map[string]string{
			"status": "status cannot be changed here; use POST /api/v1/sprints/{sprintId}/start or …/complete",
		}, &req) {
		return
	}
	if req.Name != nil {
		if strings.TrimSpace(*req.Name) == "" {
			shared.WriteValidationError(w, "VALIDATION_ERROR", "sprint name must not be blank", "name")
			return
		}
		s.Name = *req.Name
	}
	if req.Goal != nil {
		s.Goal = *req.Goal
	}
	if req.StartDate != nil {
		s.StartDate = req.StartDate
	}
	if req.EndDate != nil {
		s.EndDate = req.EndDate
	}
	if req.ReleaseID != nil {
		s.ReleaseID = req.ReleaseID
	}
	if !h.checkSprintDates(w, r, s) {
		return
	}
	if req.Version != nil {
		s.Version = *req.Version
	}
	s.UpdatedAt = shared.Now()
	if err := h.sprints.Update(s); err != nil {
		h.writeUpdateError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, s)
}

func (h *Handler) StartSprint(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "sprintId")
	s, err := h.sprints.FindByID(id)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if s == nil {
		shared.WriteError(w, http.StatusNotFound, "SPRINT_NOT_FOUND", "sprint not found")
		return
	}
	_, ok := h.writerGuard(w, r, s.ProjectID)
	if !ok {
		return
	}
	if err := h.svc.StartSprint(s); err != nil {
		if !h.writeDomainError(w, err) {
			h.writeUpdateError(w, r, err)
		}
		return
	}
	actorID := shared.GetUserID(r)
	_ = h.writeSprintActivity(s.ProjectID, s.ID, actorID, "SPRINT_STARTED", map[string]any{"name": s.Name})
	shared.WriteJSON(w, http.StatusOK, s)
}

func (h *Handler) CompleteSprint(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "sprintId")
	s, err := h.sprints.FindByID(id)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if s == nil {
		shared.WriteError(w, http.StatusNotFound, "SPRINT_NOT_FOUND", "sprint not found")
		return
	}
	_, ok := h.writerGuard(w, r, s.ProjectID)
	if !ok {
		return
	}
	if s.Status != SprintStatusActive {
		shared.WriteError(w, http.StatusUnprocessableEntity, "SPRINT_NOT_ACTIVE", "only an ACTIVE sprint can be completed")
		return
	}
	// The effort snapshot is taken in the project's unit as it stands right
	// now; a project with estimation off snapshots nothing.
	unit := EstimationUnitNone
	project, err := h.projects.FindByID(s.ProjectID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if project != nil {
		unit = project.EstimationUnit
	}
	reset, err := h.svc.CompleteSprint(s, unit)
	if err != nil {
		if !h.writeDomainError(w, err) {
			h.writeUpdateError(w, r, err)
		}
		return
	}
	actorID := shared.GetUserID(r)
	_ = h.writeSprintActivity(s.ProjectID, s.ID, actorID, "SPRINT_COMPLETED", map[string]any{"name": s.Name})
	// Tearing the sprint board down detaches its cards, and a detached task is
	// reset to PLANNED (OCT-304) — so the unfinished work carried out of the
	// sprint returns to the backlog as work not started. One replayable entry
	// each, like every other status change.
	h.writeBulkActivity(s.ProjectID, reset, actorID, "TASK_STATUS_CHANGED", map[string]any{"status": StatusPlanned})
	shared.WriteJSON(w, http.StatusOK, s)
}

func (h *Handler) DeleteSprint(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "sprintId")
	s, err := h.sprints.FindByID(id)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if s == nil {
		shared.WriteError(w, http.StatusNotFound, "SPRINT_NOT_FOUND", "sprint not found")
		return
	}
	_, ok := h.writerGuard(w, r, s.ProjectID)
	if !ok {
		return
	}
	// A sprint board only lives while its sprint exists; remove it first so the
	// board is never orphaned (boards.sprint_id is ON DELETE SET NULL).
	reset, err := h.svc.RemoveSprintBoard(id)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	h.writeBulkActivity(s.ProjectID, reset, shared.GetUserID(r), "TASK_STATUS_CHANGED", map[string]any{"status": StatusPlanned})
	if err := h.sprints.Delete(id); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// checkSprintDates enforces the date rules for a sprint write: when both bounds
// are set, the end must not precede the start, and the range must not overlap
// another non-completed sprint in the same project. Sprints without both bounds
// are left unconstrained (lenient). It writes the error response and returns
// false when a rule is violated.
func (h *Handler) checkSprintDates(w http.ResponseWriter, r *http.Request, s *Sprint) bool {
	if s.StartDate == nil || s.EndDate == nil {
		return true
	}
	if *s.StartDate > *s.EndDate {
		shared.WriteValidationError(w, "VALIDATION_ERROR", "sprint end date must not precede start date", "endDate")
		return false
	}
	other, err := h.sprints.FindOverlapping(s.ProjectID, *s.StartDate, *s.EndDate, s.ID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return false
	}
	if other != nil {
		shared.WriteError(w, http.StatusUnprocessableEntity, "SPRINT_OVERLAP", "sprint dates overlap an existing sprint")
		return false
	}
	return true
}
