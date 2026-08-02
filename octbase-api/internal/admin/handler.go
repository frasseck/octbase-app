// Package admin provides legacy admin-only endpoints.
// New user management is in internal/usermgmt. This package is kept for
// backward compat (e.g. old API clients using /api/v1/admin/users).
package admin

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/octbase/octbase-api/internal/auditlog"
	"github.com/octbase/octbase-api/internal/rbac"
	"github.com/octbase/octbase-api/internal/shared"
)

// Handler handles legacy admin HTTP routes.
type Handler struct {
	db    *sql.DB
	audit *auditlog.Repo
}

// NewHandler creates a new admin Handler.
func NewHandler(db *sql.DB, audit *auditlog.Repo) *Handler { return &Handler{db: db, audit: audit} }

// RegisterRoutes registers admin endpoints under the authenticated+admin group.
func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Get("/api/v1/admin/users", h.ListUsers)
	r.Patch("/api/v1/admin/users/{userId}", h.UpdateUser)
	r.Post("/api/v1/admin/users/{userId}/reset-password", h.ResetPassword)
}

// RequireAdmin middleware rejects callers who are not SUPER_ADMIN or ADMIN.
// Must run after shared.LoadUserGlobalRole, which sets the role in context.
func RequireAdmin() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !rbac.IsAdminRole(shared.GetGlobalRole(r)) {
				shared.WriteError(w, http.StatusForbidden, "FORBIDDEN", "admin access required")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

type adminUser struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"displayName"`
	GlobalRole  string `json:"globalRole"`
	Status      string `json:"status"`
	CreatedAt   string `json:"createdAt"`
}

func (h *Handler) ListUsers(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(
		`SELECT id, email, display_name, global_role, status, created_at
		   FROM users WHERE status <> 'deleted' ORDER BY created_at DESC`,
	)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	defer func() { _ = rows.Close() }()

	var users []adminUser
	for rows.Next() {
		var u adminUser
		if err := rows.Scan(&u.ID, &u.Email, &u.DisplayName, &u.GlobalRole, &u.Status, &u.CreatedAt); err != nil {
			shared.WriteServerError(w, r, err)
			return
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if users == nil {
		users = []adminUser{}
	}
	shared.WriteJSON(w, http.StatusOK, users)
}

// allowLegacyTarget blocks a plain ADMIN from acting on a SUPER_ADMIN account
// through these legacy endpoints. The "who may act on whom" policy itself
// lives in rbac.CanAdministerAccount (the same authority family as the
// rbac.CanDisableUser/CanUpdateUserRole predicates the modern usermgmt path
// uses), so the legacy enable/disable + password-reset endpoints cannot drift
// from it. Returns true if the request may proceed.
func (h *Handler) allowLegacyTarget(w http.ResponseWriter, r *http.Request, targetID string) bool {
	var targetRole string
	err := h.db.QueryRow(`SELECT global_role FROM users WHERE id = $1`, targetID).Scan(&targetRole)
	if errors.Is(err, sql.ErrNoRows) {
		// Unknown target: let the handler's own lookup return the right 404.
		return true
	}
	if err != nil {
		// Fail closed on a real lookup error — a transient DB failure must never
		// let the SUPER_ADMIN protection be bypassed (an ADMIN could otherwise
		// disable or reset the top role during the error window).
		shared.WriteServerError(w, r, err)
		return false
	}
	if !rbac.CanAdministerAccount(shared.GetGlobalRole(r), targetRole) {
		shared.WriteError(w, http.StatusForbidden, "FORBIDDEN", "only a Super Admin can modify a Super Admin account")
		return false
	}
	return true
}

func (h *Handler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	targetID := chi.URLParam(r, "userId")
	if !h.allowLegacyTarget(w, r, targetID) {
		return
	}
	var req struct {
		IsActive *bool `json:"isActive"`
	}
	if !shared.DecodePatch(w, r, map[string]bool{"isActive": true}, nil, &req) {
		return
	}
	if req.IsActive == nil {
		shared.WriteError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "isActive is required")
		return
	}
	status := "active"
	if !*req.IsActive {
		status = "disabled"
	}
	// The status guard keeps GDPR-erased accounts (status 'deleted') from being
	// resurrected through this legacy endpoint.
	res, err := h.db.Exec(
		`UPDATE users SET is_active = $1, status = $2, updated_at = $3
		  WHERE id = $4 AND status <> 'deleted'`,
		*req.IsActive, status, shared.Now(), targetID,
	)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		shared.WriteError(w, http.StatusNotFound, "USER_NOT_FOUND", "user not found")
		return
	}
	// Terminate all active sessions immediately when an account is disabled.
	if !*req.IsActive {
		_, _ = h.db.Exec(`DELETE FROM refresh_tokens WHERE user_id = $1`, targetID)
	}
	action := auditlog.ActionUserEnabled
	if !*req.IsActive {
		action = auditlog.ActionUserDisabled
	}
	actorID := shared.GetUserID(r)
	h.audit.Write(actorID, action, "user", targetID, "{}", shared.ClientIP(r), r.UserAgent())
	shared.WriteJSON(w, http.StatusOK, map[string]any{
		"isActive": *req.IsActive,
		"status":   status,
	})
}

func (h *Handler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	targetID := chi.URLParam(r, "userId")

	// One lookup serves both the target-role guard and the 404 check (the
	// legacy-target rule would otherwise re-read the same PK row). Fail closed
	// on a real lookup error, as in allowLegacyTarget.
	var email, targetRole string
	if err := h.db.QueryRow(`SELECT email, global_role FROM users WHERE id = $1 AND status <> 'deleted'`, targetID).Scan(&email, &targetRole); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			shared.WriteError(w, http.StatusNotFound, "USER_NOT_FOUND", "user not found")
			return
		}
		shared.WriteServerError(w, r, err)
		return
	}
	if !rbac.CanAdministerAccount(shared.GetGlobalRole(r), targetRole) {
		shared.WriteError(w, http.StatusForbidden, "FORBIDDEN", "only a Super Admin can modify a Super Admin account")
		return
	}

	// Invalidate all active sessions.
	_, _ = h.db.Exec(`DELETE FROM refresh_tokens WHERE user_id = $1`, targetID)

	actorID := shared.GetUserID(r)
	h.audit.Write(actorID, auditlog.ActionUserPasswordReset, "user", targetID, "{}", shared.ClientIP(r), r.UserAgent())

	shared.WriteJSON(w, http.StatusOK, map[string]string{
		"message": "user sessions invalidated; user must set a new password via invitation flow",
	})
}
