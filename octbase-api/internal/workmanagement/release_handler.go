package workmanagement

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/octbase/octbase-api/internal/shared"
)

// releaseNotFound writes the standard 404 used by every release handler.
func releaseNotFound(w http.ResponseWriter) {
	shared.WriteError(w, http.StatusNotFound, "RELEASE_NOT_FOUND", "release not found")
}

func (h *Handler) CreateRelease(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	_, ok := h.writerGuard(w, r, projectID)
	if !ok {
		return
	}
	var req struct {
		Name    string  `json:"name"`
		Goal    string  `json:"goal"`
		DueDate *string `json:"dueDate"`
	}
	if err := shared.DecodeJSON(r, &req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON")
		return
	}
	now := shared.Now()
	m := &Release{
		ID: shared.NewUUID(), ProjectID: projectID, Name: req.Name,
		Goal: req.Goal, DueDate: req.DueDate, Status: StatusPlanned,
		CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	if err := h.releases.Create(m); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusCreated, m)
}

func (h *Handler) ListReleases(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	if _, ok := h.memberGuard(w, r, projectID); !ok {
		return
	}
	ms, err := h.releases.ListByProject(projectID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, ms)
}

func (h *Handler) GetRelease(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "releaseId")
	m, err := h.releases.FindByID(id)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if m == nil {
		releaseNotFound(w)
		return
	}
	if _, ok := h.memberGuard(w, r, m.ProjectID); !ok {
		return
	}
	shared.WriteJSON(w, http.StatusOK, m)
}

func (h *Handler) UpdateRelease(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "releaseId")
	m, err := h.releases.FindByID(id)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if m == nil {
		releaseNotFound(w)
		return
	}
	_, ok := h.writerGuard(w, r, m.ProjectID)
	if !ok {
		return
	}
	var req struct {
		Name    *string `json:"name"`
		Goal    *string `json:"goal"`
		DueDate *string `json:"dueDate"`
		// Version, when sent, is the version the client's edit is based on; the
		// guarded update rejects the write with 409 if the release has moved on.
		Version *int `json:"version"`
	}
	// `status` is deliberately a dedicated-route rejection: a release closes and
	// reopens only through its transition routes, and PATCHing it used to answer
	// 200 while the release stayed PLANNED, without even validating the value.
	if !shared.DecodePatch(w, r,
		map[string]bool{"name": true, "goal": true, "dueDate": true, "version": true},
		map[string]string{
			"status": "status cannot be changed here; use POST /api/v1/releases/{releaseId}/close or …/reopen",
		}, &req) {
		return
	}
	if req.Name != nil {
		m.Name = *req.Name
	}
	if req.Goal != nil {
		m.Goal = *req.Goal
	}
	if req.DueDate != nil {
		m.DueDate = req.DueDate
	}
	if req.Version != nil {
		m.Version = *req.Version
	}
	m.UpdatedAt = shared.Now()
	if err := h.releases.Update(m); err != nil {
		h.writeUpdateError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, m)
}

func (h *Handler) CloseRelease(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "releaseId")
	m, err := h.releases.FindByID(id)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if m == nil {
		releaseNotFound(w)
		return
	}
	_, ok := h.writerGuard(w, r, m.ProjectID)
	if !ok {
		return
	}
	if err := h.svc.CloseRelease(m); err != nil {
		if !h.writeDomainError(w, err) {
			h.writeUpdateError(w, r, err)
		}
		return
	}
	actorID := shared.GetUserID(r)
	_ = h.writeActivity(m.ProjectID, "", actorID, "RELEASE_CLOSED", map[string]any{"name": m.Name})
	shared.WriteJSON(w, http.StatusOK, m)
}

func (h *Handler) ReopenRelease(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "releaseId")
	m, err := h.releases.FindByID(id)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if m == nil {
		releaseNotFound(w)
		return
	}
	_, ok := h.writerGuard(w, r, m.ProjectID)
	if !ok {
		return
	}
	m.Status = StatusPlanned
	m.UpdatedAt = shared.Now()
	if err := h.releases.Update(m); err != nil {
		h.writeUpdateError(w, r, err)
		return
	}
	actorID := shared.GetUserID(r)
	_ = h.writeActivity(m.ProjectID, "", actorID, "RELEASE_REOPENED", map[string]any{"name": m.Name})
	shared.WriteJSON(w, http.StatusOK, m)
}

func (h *Handler) DeleteRelease(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "releaseId")
	m, err := h.releases.FindByID(id)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if m == nil {
		releaseNotFound(w)
		return
	}
	_, ok := h.writerGuard(w, r, m.ProjectID)
	if !ok {
		return
	}
	if err := h.releases.Delete(id); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
