package identityaccess

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/octbase/octbase-api/internal/auditlog"
	"github.com/octbase/octbase-api/internal/rbac"
	"github.com/octbase/octbase-api/internal/shared"
)

type Handler struct {
	db          *sql.DB
	users       *UserRepo
	memberships *MembershipRepo
	audit       *auditlog.Repo
}

func NewHandler(db *sql.DB, users *UserRepo, memberships *MembershipRepo, audit *auditlog.Repo) *Handler {
	return &Handler{db: db, users: users, memberships: memberships, audit: audit}
}

func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Get("/api/v1/users/me", h.GetMe)
	r.Get("/api/v1/projects/{projectId}/members", h.ListMembers)
	r.Get("/api/v1/projects/{projectId}/assignable-users", h.ListAssignableUsers)
	r.Get("/api/v1/projects/{projectId}/memberships", h.ListMemberships)
	r.Post("/api/v1/projects/{projectId}/memberships", h.AddMember)
	r.Patch("/api/v1/projects/{projectId}/memberships/{userId}", h.UpdateRole)
	r.Delete("/api/v1/projects/{projectId}/memberships/{userId}", h.RemoveMember)
	r.Get("/api/v1/projects/{projectId}/permissions", h.GetPermissions)
}

// memberGuard checks that the acting user is a member of the project and
// returns their effective project role. Super Admin bypasses the membership
// requirement and is treated as PROJECT_ADMIN.
func (h *Handler) memberGuard(w http.ResponseWriter, r *http.Request, projectID string) (string, bool) {
	userID := shared.GetUserID(r)
	if userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
		return "", false
	}
	// Super Admin has full access without a membership row.
	if shared.GetGlobalRole(r) == rbac.GlobalSuperAdmin {
		return rbac.ProjectAdmin, true
	}
	role, err := shared.RequireProjectMember(h.db, projectID, userID)
	if errors.Is(err, shared.ErrNotMember) {
		shared.WriteError(w, http.StatusForbidden, "FORBIDDEN", "not a member of this project")
		return "", false
	}
	if err != nil {
		shared.WriteServerError(w, r, err)
		return "", false
	}
	return role, true
}

func (h *Handler) GetMe(w http.ResponseWriter, r *http.Request) {
	userID := shared.GetUserID(r)
	if userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
		return
	}
	u, err := h.users.FindByID(userID)
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

// ListMembers returns memberships joined with user details for the project.
func (h *Handler) ListMembers(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	// Any authenticated user who can view the project may list its members.
	userID := shared.GetUserID(r)
	if userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
		return
	}
	if shared.GetGlobalRole(r) != rbac.GlobalSuperAdmin {
		if _, err := shared.RequireProjectMember(h.db, projectID, userID); errors.Is(err, shared.ErrNotMember) {
			shared.WriteError(w, http.StatusForbidden, "FORBIDDEN", "not a member of this project")
			return
		} else if err != nil {
			shared.WriteServerError(w, r, err)
			return
		}
	}
	ms, err := h.memberships.ListByProjectWithUsers(projectID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, ms)
}

// ListAssignableUsers returns the candidates for a task's assignee or reviewer:
// the project's members plus the active global ADMIN/SUPER_ADMIN accounts. It
// exists because the pickers used to read the membership list, which silently
// excluded global admins — they reach projects without a membership row.
// Read guard is the same as ListMembers: any member of the project, or a Super
// Admin.
func (h *Handler) ListAssignableUsers(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	if _, ok := h.memberGuard(w, r, projectID); !ok {
		return
	}
	us, err := h.memberships.ListAssignableUsers(projectID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, us)
}

func (h *Handler) ListMemberships(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	// Same guard as ListMembers: any authenticated project member (or
	// Super Admin) may list memberships.
	userID := shared.GetUserID(r)
	if userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
		return
	}
	if shared.GetGlobalRole(r) != rbac.GlobalSuperAdmin {
		if _, err := shared.RequireProjectMember(h.db, projectID, userID); errors.Is(err, shared.ErrNotMember) {
			shared.WriteError(w, http.StatusForbidden, "FORBIDDEN", "not a member of this project")
			return
		} else if err != nil {
			shared.WriteServerError(w, r, err)
			return
		}
	}
	ms, err := h.memberships.ListByProject(projectID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, ms)
}

type addMemberRequest struct {
	UserID string `json:"userId"`
	Role   string `json:"role"`
}

func (h *Handler) AddMember(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	actorID := shared.GetUserID(r)
	role, ok := h.memberGuard(w, r, projectID)
	if !ok {
		return
	}
	var req addMemberRequest
	if !shared.DecodeJSONOrBadRequest(w, r, &req) {
		return
	}
	if !shared.IsValidID(req.UserID) {
		shared.WriteValidationError(w, "VALIDATION_ERROR", "userId is required", "userId")
		return
	}
	if req.Role == "" {
		req.Role = RoleProjectMember
	}
	if !rbac.IsValidProjectRole(req.Role) {
		shared.WriteError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "invalid project role")
		return
	}
	if !rbac.CanAssignRole(shared.GetGlobalRole(r), role, req.Role) {
		shared.WriteError(w, http.StatusForbidden, "FORBIDDEN", "only project admins can manage members")
		return
	}
	// The user being added must already exist. Rejecting unknown ids here turns a
	// would-be foreign-key 500 into a clean 404, and keeps "add existing user"
	// distinct from the invite flow that provisions brand-new accounts.
	u, err := h.users.FindByID(req.UserID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if u == nil {
		shared.WriteError(w, http.StatusNotFound, "USER_NOT_FOUND", "user not found")
		return
	}
	now := shared.Now()
	m := &Membership{
		ID:               shared.NewUUID(),
		ProjectID:        projectID,
		UserID:           req.UserID,
		Role:             req.Role,
		AssignedByUserID: actorID,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := h.memberships.Create(m); err != nil {
		// The (project_id, user_id) unique constraint makes re-adding a member a
		// conflict rather than a server error.
		if shared.IsUniqueViolation(err) {
			shared.WriteError(w, http.StatusConflict, "ALREADY_MEMBER", "user is already a member of this project")
			return
		}
		shared.WriteServerError(w, r, err)
		return
	}
	h.audit.Write(actorID, auditlog.ActionMemberAdded, "membership", m.ID,
		fmt.Sprintf(`{"projectId":%q,"userId":%q,"role":%q}`, projectID, req.UserID, req.Role),
		"", "")
	shared.WriteJSON(w, http.StatusCreated, m)
}

type updateRoleRequest struct {
	Role string `json:"role"`
}

func (h *Handler) UpdateRole(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	actorID := shared.GetUserID(r)
	role, ok := h.memberGuard(w, r, projectID)
	if !ok {
		return
	}
	userID := chi.URLParam(r, "userId")
	var req updateRoleRequest
	if !shared.DecodePatch(w, r, map[string]bool{"role": true}, nil, &req) {
		return
	}
	if !rbac.IsValidProjectRole(req.Role) {
		shared.WriteError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "invalid project role")
		return
	}
	currentRole, err := h.memberships.FindRole(projectID, userID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if currentRole == "" {
		shared.WriteError(w, http.StatusNotFound, "MEMBER_NOT_FOUND", "membership not found")
		return
	}
	if !rbac.CanChangeRole(shared.GetGlobalRole(r), role, currentRole, req.Role) {
		shared.WriteError(w, http.StatusForbidden, "FORBIDDEN", "only project owners can grant or revoke ownership")
		return
	}
	ownerCount, err := h.memberships.CountByRole(projectID, rbac.ProjectOwner)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if rbac.WouldRemoveLastOwner(currentRole, req.Role, ownerCount) {
		shared.WriteError(w, http.StatusUnprocessableEntity, "LAST_OWNER", "project must have at least one owner")
		return
	}
	if err := h.memberships.UpdateRole(projectID, userID, req.Role, shared.Now()); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	h.audit.Write(actorID, auditlog.ActionMemberRoleChanged, "membership", userID,
		fmt.Sprintf(`{"projectId":%q,"newRole":%q}`, projectID, req.Role),
		"", "")
	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (h *Handler) RemoveMember(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	actorID := shared.GetUserID(r)
	role, ok := h.memberGuard(w, r, projectID)
	if !ok {
		return
	}
	userID := chi.URLParam(r, "userId")
	currentRole, err := h.memberships.FindRole(projectID, userID)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if currentRole == "" {
		shared.WriteError(w, http.StatusNotFound, "MEMBER_NOT_FOUND", "membership not found")
		return
	}
	if !rbac.CanChangeRole(shared.GetGlobalRole(r), role, currentRole, "") {
		shared.WriteError(w, http.StatusForbidden, "FORBIDDEN", "only project owners can remove an owner")
		return
	}
	ownerCount, err := h.memberships.CountByRole(projectID, rbac.ProjectOwner)
	if err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	if rbac.WouldRemoveLastOwner(currentRole, "", ownerCount) {
		shared.WriteError(w, http.StatusUnprocessableEntity, "LAST_OWNER", "project must have at least one owner")
		return
	}
	if err := h.memberships.Delete(projectID, userID); err != nil {
		shared.WriteServerError(w, r, err)
		return
	}
	h.audit.Write(actorID, auditlog.ActionMemberRemoved, "membership", userID,
		fmt.Sprintf(`{"projectId":%q}`, projectID),
		"", "")
	w.WriteHeader(http.StatusNoContent)
}

// GetPermissions returns the caller's effective permission set for a project,
// so the frontend can drive UI visibility without re-deriving role logic.
func (h *Handler) GetPermissions(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	role, ok := h.memberGuard(w, r, projectID)
	if !ok {
		return
	}
	globalRole := shared.GetGlobalRole(r)
	perms := make(map[string]bool, len(rbac.AllPermissions()))
	for _, p := range rbac.AllPermissions() {
		perms[p] = rbac.HasPermission(globalRole, role, p)
	}
	shared.WriteJSON(w, http.StatusOK, map[string]any{
		"projectId":   projectID,
		"role":        role,
		"permissions": perms,
	})
}
