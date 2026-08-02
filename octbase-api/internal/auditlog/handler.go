package auditlog

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/octbase/octbase-api/internal/rbac"
	"github.com/octbase/octbase-api/internal/shared"
)

// Handler serves the audit-log API.
type Handler struct{ repo *Repo }

// NewHandler creates a new Handler.
func NewHandler(repo *Repo) *Handler { return &Handler{repo: repo} }

// RegisterRoutes registers audit-log routes. Must be called inside a group
// that already has JWTMiddleware and LoadUserGlobalRole applied.
func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Get("/api/v1/audit-logs", h.List)
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	if !rbac.CanViewAuditLogs(shared.GetGlobalRole(r)) {
		shared.WriteError(w, http.StatusForbidden, "FORBIDDEN", "audit log access requires Super Admin")
		return
	}
	pg := shared.ParsePagination(r)
	action := r.URL.Query().Get("action")
	result, err := h.repo.List(pg.Page, pg.Size, action)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, result)
}
