package usermgmt

import (
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/octbase/octbase-api/internal/auditlog"
	"github.com/octbase/octbase-api/internal/auth"
	"github.com/octbase/octbase-api/internal/mailer"
	"github.com/octbase/octbase-api/internal/rbac"
	"github.com/octbase/octbase-api/internal/shared"
)

// Handler serves user-management endpoints (Super Admin only).
type Handler struct {
	db     *sql.DB
	repo   *Repo
	audit  *auditlog.Repo
	mailer mailer.Mailer
	// maxUsers caps how many user accounts the installation may hold
	// (including the admin); 0 or negative means unlimited. See WithUserLimit.
	maxUsers int
}

// NewHandler creates a new Handler. A nil mailer falls back to mailer.New()
// (stdout dev-mode when SMTP is unconfigured), mirroring auth.NewHandler.
func NewHandler(db *sql.DB, repo *Repo, audit *auditlog.Repo, mlr mailer.Mailer) *Handler {
	if mlr == nil {
		mlr = mailer.New()
	}
	return &Handler{db: db, repo: repo, audit: audit, mailer: mlr}
}

// WithUserLimit sets the installation-wide account limit (OCTBASE_MAX_USERS)
// enforced by CreateUser. Values <= 0 disable the limit. Returns the handler
// for chaining.
func (h *Handler) WithUserLimit(n int) *Handler {
	h.maxUsers = n
	return h
}

// RegisterRoutes registers all user-management routes. Must be called inside a
// group that already has JWTMiddleware and LoadUserGlobalRole applied.
func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Get("/api/v1/users", h.ListUsers)
	r.Post("/api/v1/users", h.CreateUser)
	r.Get("/api/v1/users/{userId}", h.GetUser)
	r.Patch("/api/v1/users/{userId}", h.UpdateUser)
	r.Patch("/api/v1/users/{userId}/disable", h.DisableUser)
	r.Delete("/api/v1/users/{userId}", h.DeleteUser)
}

// requireSuperAdmin writes 403 and returns false if the caller is not SUPER_ADMIN.
func (h *Handler) requireSuperAdmin(w http.ResponseWriter, r *http.Request) bool {
	if !rbac.CanManageAccounts(shared.GetGlobalRole(r)) {
		shared.WriteError(w, http.StatusForbidden, "FORBIDDEN", "account management requires Super Admin")
		return false
	}
	return true
}

func (h *Handler) ListUsers(w http.ResponseWriter, r *http.Request) {
	if !h.requireSuperAdmin(w, r) {
		return
	}
	users, err := h.repo.List()
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, users)
}

func (h *Handler) GetUser(w http.ResponseWriter, r *http.Request) {
	if !h.requireSuperAdmin(w, r) {
		return
	}
	targetID := chi.URLParam(r, "userId")
	if !shared.IsValidID(targetID) {
		shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid user id")
		return
	}
	u, err := h.repo.FindByID(targetID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if u == nil {
		shared.WriteError(w, http.StatusNotFound, "USER_NOT_FOUND", "user not found")
		return
	}
	shared.WriteJSON(w, http.StatusOK, u)
}

func (h *Handler) CreateUser(w http.ResponseWriter, r *http.Request) {
	if !h.requireSuperAdmin(w, r) {
		return
	}
	actorID := shared.GetUserID(r)

	var req struct {
		Email       string `json:"email"`
		DisplayName string `json:"displayName"`
		Password    string `json:"password"`
		GlobalRole  string `json:"globalRole"`
	}
	if err := shared.DecodeJSON(r, &req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON")
		return
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	req.DisplayName = strings.TrimSpace(req.DisplayName)

	if req.Email == "" {
		shared.WriteValidationError(w, "VALIDATION_ERROR", "email is required", "email")
		return
	}
	if req.DisplayName == "" {
		shared.WriteValidationError(w, "VALIDATION_ERROR", "displayName is required", "displayName")
		return
	}
	if msg := shared.ValidatePassword(req.Password); msg != "" {
		shared.WriteValidationError(w, "VALIDATION_ERROR", msg, "password")
		return
	}
	if req.GlobalRole == "" {
		req.GlobalRole = rbac.GlobalUser
	}
	// Only Super Admin may create Admin accounts; SUPER_ADMIN cannot be created via API.
	if req.GlobalRole == rbac.GlobalSuperAdmin {
		shared.WriteError(w, http.StatusForbidden, "FORBIDDEN", "SUPER_ADMIN cannot be assigned via the API")
		return
	}
	if req.GlobalRole == rbac.GlobalAdmin && !rbac.CanCreateAdmin(shared.GetGlobalRole(r)) {
		shared.WriteError(w, http.StatusForbidden, "FORBIDDEN", "only Super Admin can create Admin accounts")
		return
	}
	if !rbac.IsValidGlobalRole(req.GlobalRole) {
		shared.WriteError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "invalid globalRole")
		return
	}

	// Installation-wide seat limit. Advisory check-then-insert (a concurrent
	// create could race past it), which is fine for an admin-only endpoint —
	// the limit is a licensing bound, not a security invariant.
	if h.maxUsers > 0 {
		n, err := shared.CountUsers(h.db)
		if err != nil {
			shared.WriteServerError(w, r, err)
			return
		}
		if n >= h.maxUsers {
			shared.WriteError(w, http.StatusForbidden, "USER_LIMIT_REACHED",
				fmt.Sprintf("this installation is limited to %d user accounts", h.maxUsers))
			return
		}
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	now := shared.Now()
	userID := shared.NewUUID()
	if err := h.repo.Create(userID, req.Email, req.DisplayName, hash, req.GlobalRole, "active", now); err != nil {
		if shared.IsUniqueViolation(err) {
			shared.WriteError(w, http.StatusConflict, "EMAIL_CONFLICT", "a user with this email already exists")
			return
		}
		shared.WriteServerError(w, r, err)
		return
	}

	h.audit.Write(actorID, auditlog.ActionUserCreated, "user", userID,
		fmt.Sprintf(`{"globalRole":%q}`, req.GlobalRole),
		clientIP(r), r.UserAgent())

	u, _ := h.repo.FindByID(userID)
	shared.WriteJSON(w, http.StatusCreated, u)
}

func (h *Handler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	if !h.requireSuperAdmin(w, r) {
		return
	}
	actorID := shared.GetUserID(r)
	targetID := chi.URLParam(r, "userId")
	if !shared.IsValidID(targetID) {
		shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid user id")
		return
	}

	target, err := h.repo.FindByID(targetID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if target == nil {
		shared.WriteError(w, http.StatusNotFound, "USER_NOT_FOUND", "user not found")
		return
	}
	if target.GlobalRole == rbac.GlobalSuperAdmin {
		shared.WriteError(w, http.StatusForbidden, "FORBIDDEN", "cannot modify another Super Admin")
		return
	}

	var req struct {
		Email       *string `json:"email"`
		DisplayName *string `json:"displayName"`
		GlobalRole  *string `json:"globalRole"`
		Status      *string `json:"status"`
	}
	if !shared.DecodePatch(w, r, map[string]bool{
		"email": true, "displayName": true, "globalRole": true, "status": true,
	}, nil, &req) {
		return
	}

	email := target.Email
	displayName := target.DisplayName
	globalRole := target.GlobalRole
	status := target.Status

	if req.Email != nil {
		// Lowercase like CreateUser: login looks the address up exactly as
		// stored, so a mixed-case value would lock the user out.
		email = strings.TrimSpace(strings.ToLower(*req.Email))
		if email == "" {
			shared.WriteValidationError(w, "VALIDATION_ERROR", "email is required", "email")
			return
		}
	}
	emailChanged := email != target.Email

	if req.DisplayName != nil {
		displayName = strings.TrimSpace(*req.DisplayName)
	}
	if req.GlobalRole != nil {
		newRole := *req.GlobalRole
		if !rbac.CanUpdateUserRole(shared.GetGlobalRole(r), target.GlobalRole, newRole) {
			shared.WriteError(w, http.StatusForbidden, "FORBIDDEN", "cannot assign that role")
			return
		}
		if !rbac.IsValidGlobalRole(newRole) {
			shared.WriteError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "invalid globalRole")
			return
		}
		globalRole = newRole
	}
	if req.Status != nil {
		s := *req.Status
		if s != "active" && s != "disabled" && s != "invited" {
			shared.WriteError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "invalid status")
			return
		}
		status = s
	}

	now := shared.Now()
	if err := h.repo.UpdateFields(targetID, email, displayName, globalRole, status, now); err != nil {
		if shared.IsUniqueViolation(err) {
			shared.WriteError(w, http.StatusConflict, "EMAIL_CONFLICT", "a user with this email already exists")
			return
		}
		shared.WriteServerError(w, r, err)
		return
	}

	action := auditlog.ActionUserUpdated
	if req.GlobalRole != nil && globalRole != target.GlobalRole {
		action = auditlog.ActionUserRoleChanged
	}
	h.audit.Write(actorID, action, "user", targetID,
		fmt.Sprintf(`{"globalRole":%q,"status":%q}`, globalRole, status),
		clientIP(r), r.UserAgent())

	if emailChanged {
		// Separate audit entry; metadata carries no address (audit rows outlive
		// a later GDPR erasure of the account — see DisableUser).
		h.audit.Write(actorID, auditlog.ActionUserEmailChanged, "user", targetID,
			"{}", clientIP(r), r.UserAgent())

		// Tell the old address its sign-in email no longer works, so a hijacked
		// change doesn't go unnoticed. Best-effort, same as invitation mail: a
		// delivery failure must not fail the update.
		subject, body := emailChangedNotice(displayName)
		if err := h.mailer.Send(r.Context(), target.Email, subject, body); err != nil {
			slog.Error("email-change notice send failed", "userId", targetID, "error", err)
		}
	}

	updated, _ := h.repo.FindByID(targetID)
	shared.WriteJSON(w, http.StatusOK, updated)
}

// emailChangedNotice builds the subject and plain-text body sent to a user's
// previous address after an administrator changed their sign-in email. It
// deliberately omits the new address: the old inbox no longer belongs to the
// account and should not learn where it moved.
func emailChangedNotice(displayName string) (subject, body string) {
	return "Your Octbase sign-in email was changed",
		fmt.Sprintf(
			"Hello %s,\n\n"+
				"An administrator changed the sign-in email address of your Octbase account. "+
				"This address can no longer be used to sign in.\n\n"+
				"If you did not expect this change, contact your administrator immediately.",
			displayName)
}

func (h *Handler) DisableUser(w http.ResponseWriter, r *http.Request) {
	if !h.requireSuperAdmin(w, r) {
		return
	}
	actorID := shared.GetUserID(r)
	targetID := chi.URLParam(r, "userId")
	if !shared.IsValidID(targetID) {
		shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid user id")
		return
	}

	target, err := h.repo.FindByID(targetID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if target == nil {
		shared.WriteError(w, http.StatusNotFound, "USER_NOT_FOUND", "user not found")
		return
	}
	if !rbac.CanDisableUser(shared.GetGlobalRole(r), target.GlobalRole) {
		shared.WriteError(w, http.StatusForbidden, "FORBIDDEN", "cannot disable this account")
		return
	}

	now := shared.Now()
	if err := h.repo.SetStatus(targetID, "disabled", now); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}

	// Invalidate all refresh tokens so the session ends immediately.
	_, _ = h.db.Exec(`DELETE FROM refresh_tokens WHERE user_id = $1`, targetID)

	// Metadata carries no email: the target user ID identifies the account,
	// and audit rows outlive a later GDPR erasure of that account.
	h.audit.Write(actorID, auditlog.ActionUserDisabled, "user", targetID,
		"{}", clientIP(r), r.UserAgent())

	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "disabled"})
}

func (h *Handler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	if !h.requireSuperAdmin(w, r) {
		return
	}
	actorID := shared.GetUserID(r)
	targetID := chi.URLParam(r, "userId")
	if !shared.IsValidID(targetID) {
		shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid user id")
		return
	}

	target, err := h.repo.FindByID(targetID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if target == nil {
		shared.WriteError(w, http.StatusNotFound, "USER_NOT_FOUND", "user not found")
		return
	}
	if !rbac.CanDeleteUser(shared.GetGlobalRole(r), target.GlobalRole) {
		shared.WriteError(w, http.StatusForbidden, "FORBIDDEN", "cannot delete this account")
		return
	}

	// GDPR erasure: anonymize in place rather than hard-delete (see
	// Repo.Anonymize). The audit metadata deliberately omits the email — the
	// target user ID is recorded, and keeping the address here would defeat
	// the erasure.
	if err := h.repo.Anonymize(targetID, shared.Now()); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}

	h.audit.Write(actorID, auditlog.ActionUserDeleted, "user", targetID,
		"{}", clientIP(r), r.UserAgent())

	w.WriteHeader(http.StatusNoContent)
}

// clientIP extracts the real client IP for audit logging. It relies on
// shared.RealIP having already resolved r.RemoteAddr from trusted-proxy headers,
// so it never reads the spoofable X-Forwarded-For directly.
func clientIP(r *http.Request) string {
	return shared.ClientIP(r)
}
