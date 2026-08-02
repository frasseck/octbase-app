package shared

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/octbase/octbase-api/internal/rbac"
)

// ErrNotMember is returned when the acting user is not a member of the project.
var ErrNotMember = errors.New("not a project member")

const globalRoleKey contextKey = "globalRole"

// LoadUserGlobalRole is middleware that loads the user's global_role and
// validates the account is not disabled on every authenticated request.
// A disabled account gets 401 even if the JWT is still valid.
func LoadUserGlobalRole(db *sql.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID := GetUserID(r)
			if userID != "" {
				var role, status string
				err := db.QueryRow(
					`SELECT global_role, status FROM users WHERE id = $1`, userID,
				).Scan(&role, &status)
				if err != nil || status == "disabled" || status == "deleted" {
					WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid or expired token")
					return
				}
				ctx := context.WithValue(r.Context(), globalRoleKey, role)
				r = r.WithContext(ctx)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// GetGlobalRole returns the global_role stored by LoadUserGlobalRole, or "".
func GetGlobalRole(r *http.Request) string {
	role, _ := r.Context().Value(globalRoleKey).(string)
	return role
}

// IsValidID returns true if id looks like a non-empty UUID (basic sanity check).
func IsValidID(id string) bool {
	if id == "" || len(id) > 36 {
		return false
	}
	return !strings.ContainsAny(id, " \t\n'\"\\;")
}

// RequireProjectMember checks that userID is a member of projectID.
// Returns the member's project role on success, ErrNotMember if absent,
// or a wrapped DB error.
func RequireProjectMember(db *sql.DB, projectID, userID string) (string, error) {
	var role string
	err := db.QueryRow(
		`SELECT role FROM memberships WHERE project_id = $1 AND user_id = $2`,
		projectID, userID,
	).Scan(&role)
	if err == sql.ErrNoRows {
		return "", ErrNotMember
	}
	if err != nil {
		return "", fmt.Errorf("query membership: %w", err)
	}
	return role, nil
}

// IsAssignableUser reports whether userID may be given work on projectID: a
// member of the project, or an active global ADMIN/SUPER_ADMIN, who reaches
// every project without holding a membership row.
//
// This is the server-side counterpart of GET /projects/{id}/assignable-users:
// the API must accept exactly the set the picker offers. It lives here rather
// than in identityaccess because workmanagement is the caller and bounded
// contexts may not import each other (docs/architecture.md §1) — the same
// reason RequireProjectMember reads the memberships table from core.
func IsAssignableUser(db *sql.DB, projectID, userID string) (bool, error) {
	var assignable bool
	err := db.QueryRow(`
		SELECT EXISTS (
			SELECT 1
			FROM users u
			LEFT JOIN memberships m ON m.user_id = u.id AND m.project_id = $1
			WHERE u.id = $2
			  AND (m.id IS NOT NULL
			       OR (u.global_role IN ($3, $4) AND u.status = 'active'))
		)`, projectID, userID, rbac.GlobalSuperAdmin, rbac.GlobalAdmin).Scan(&assignable)
	if err != nil {
		return false, fmt.Errorf("query assignable user: %w", err)
	}
	return assignable, nil
}

// ProjectMemberGuard enforces that the caller may act within projectID and
// returns the caller's effective project role. It writes the standard error
// response and returns ok=false when access is denied:
//   - 401 UNAUTHORIZED if there is no authenticated user,
//   - 403 FORBIDDEN if the user is not a member of the project,
//   - 500 on a database error.
//
// Super Admins are granted ProjectAdmin without a membership row. This is the
// single implementation shared by every bounded context's handler guard.
func ProjectMemberGuard(db *sql.DB, w http.ResponseWriter, r *http.Request, projectID string) (string, bool) {
	userID := GetUserID(r)
	if userID == "" {
		WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
		return "", false
	}
	if GetGlobalRole(r) == rbac.GlobalSuperAdmin {
		return rbac.ProjectAdmin, true
	}
	role, err := RequireProjectMember(db, projectID, userID)
	if errors.Is(err, ErrNotMember) {
		WriteError(w, http.StatusForbidden, "FORBIDDEN", "not a member of this project")
		return "", false
	}
	if err != nil {
		WriteServerError(w, r, err)
		return "", false
	}
	return role, true
}

// RequireProjectWritable writes 409 PROJECT_ARCHIVED and returns false when
// projectID refers to an archived project: archived projects are read-only,
// so every content mutation must pass this check. A missing project passes
// (existence/404 handling stays with the caller); a DB error writes a 500.
// This is the single authority for the archived-project freeze — permission
// guards compose it rather than re-reading project status themselves.
func RequireProjectWritable(db *sql.DB, w http.ResponseWriter, r *http.Request, projectID string) bool {
	var status string
	err := db.QueryRow(`SELECT status FROM projects WHERE id = $1`, projectID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return true
	}
	if err != nil {
		WriteServerError(w, r, err)
		return false
	}
	if status == "ARCHIVED" {
		WriteError(w, http.StatusConflict, "PROJECT_ARCHIVED", "project is archived")
		return false
	}
	return true
}

// ProjectWriterGuard is ProjectMemberGuard plus the writer-role check and the
// archived-project freeze. It is the guard for RequireWriter-level mutations:
//   - 401/403/500 as ProjectMemberGuard,
//   - 403 FORBIDDEN for PROJECT_VIEWER,
//   - 409 PROJECT_ARCHIVED when the project is archived.
func ProjectWriterGuard(db *sql.DB, w http.ResponseWriter, r *http.Request, projectID string) (string, bool) {
	role, ok := ProjectMemberGuard(db, w, r, projectID)
	if !ok {
		return "", false
	}
	if !RequireWriterOr403(w, role) {
		return "", false
	}
	if !RequireProjectWritable(db, w, r, projectID) {
		return "", false
	}
	return role, true
}

// RequireOwner returns an error unless the project role is PROJECT_OWNER or
// PROJECT_ADMIN.
func RequireOwner(role string) error {
	if role != rbac.ProjectOwner && role != rbac.ProjectAdmin {
		return fmt.Errorf("only project admins can perform this action")
	}
	return nil
}

// RequireWriter returns an error if the project role is PROJECT_VIEWER.
func RequireWriter(role string) error {
	if role == rbac.ProjectViewer {
		return fmt.Errorf("viewers cannot perform write operations")
	}
	return nil
}

// RequireWriterOr403 writes the standard 403 FORBIDDEN response and returns
// false when the project role may not perform write operations (see
// RequireWriter). This is the single authority for the writer-role check in
// mutation handlers:
//
//	if !shared.RequireWriterOr403(w, role) {
//		return
//	}
func RequireWriterOr403(w http.ResponseWriter, role string) bool {
	err := RequireWriter(role)
	if err == nil {
		return true
	}
	WriteError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
	return false
}
