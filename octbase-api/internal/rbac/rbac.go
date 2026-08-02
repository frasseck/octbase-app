// Package rbac defines global and project-level roles and centralises all
// authorisation decisions. Every permission check is a pure function so it can
// be unit-tested without a database.
package rbac

// ── Global roles ──────────────────────────────────────────────────────────────

const (
	GlobalSuperAdmin = "SUPER_ADMIN"
	GlobalAdmin      = "ADMIN"
	GlobalUser       = "USER"
	GlobalGuest      = "GUEST"
)

// ── Project-level roles ───────────────────────────────────────────────────────

const (
	ProjectOwner  = "PROJECT_OWNER"
	ProjectAdmin  = "PROJECT_ADMIN"
	ProjectMember = "PROJECT_MEMBER"
	ProjectViewer = "PROJECT_VIEWER"
)

// ValidGlobalRoles returns all valid global role values.
func ValidGlobalRoles() []string {
	return []string{GlobalSuperAdmin, GlobalAdmin, GlobalUser, GlobalGuest}
}

// ValidProjectRoles returns all valid project role values.
func ValidProjectRoles() []string {
	return []string{ProjectOwner, ProjectAdmin, ProjectMember, ProjectViewer}
}

// IsValidGlobalRole reports whether s is a defined global role.
func IsValidGlobalRole(s string) bool {
	for _, r := range ValidGlobalRoles() {
		if r == s {
			return true
		}
	}
	return false
}

// IsValidProjectRole reports whether s is a defined project role.
func IsValidProjectRole(s string) bool {
	for _, r := range ValidProjectRoles() {
		if r == s {
			return true
		}
	}
	return false
}

// ── Account-management permissions ───────────────────────────────────────────

// CanManageAccounts returns true if the actor may list and manage user accounts.
func CanManageAccounts(globalRole string) bool {
	return globalRole == GlobalSuperAdmin
}

// CanCreateAdmin returns true if the actor may create Admin-level accounts.
func CanCreateAdmin(globalRole string) bool {
	return globalRole == GlobalSuperAdmin
}

// CanUpdateUserRole returns true if the actor may change targetRole.
// Only Super Admin may change roles, and no one may assign the SUPER_ADMIN
// role through the API (it can only be set in the database directly).
func CanUpdateUserRole(actorRole, targetCurrentRole, newRole string) bool {
	if actorRole != GlobalSuperAdmin {
		return false
	}
	// Protect existing Super Admins from being demoted via this path.
	if targetCurrentRole == GlobalSuperAdmin {
		return false
	}
	// Nobody may promote another user to Super Admin via the API.
	if newRole == GlobalSuperAdmin {
		return false
	}
	return true
}

// CanDisableUser returns true if the actor may disable/enable the target.
func CanDisableUser(actorRole, targetRole string) bool {
	if actorRole != GlobalSuperAdmin {
		return false
	}
	return targetRole != GlobalSuperAdmin
}

// CanDeleteUser returns true if the actor may delete the target account.
func CanDeleteUser(actorRole, targetRole string) bool {
	if actorRole != GlobalSuperAdmin {
		return false
	}
	return targetRole != GlobalSuperAdmin
}

// IsAdminRole returns true for the global roles that may reach the admin
// endpoints at all (the admin.RequireAdmin route gate).
func IsAdminRole(globalRole string) bool {
	return globalRole == GlobalSuperAdmin || globalRole == GlobalAdmin
}

// CanAdministerAccount returns true if the actor may act on the target account
// through the legacy admin endpoints (enable/disable, session-invalidating
// password reset): a SUPER_ADMIN may act on anyone; an ADMIN may act on anyone
// except a SUPER_ADMIN. This is the single authority for "who may act on whom"
// on those endpoints — the modern usermgmt endpoints use the stricter
// CanUpdateUserRole/CanDisableUser/CanDeleteUser predicates above, which also
// refuse SUPER_ADMIN targets, so the two paths cannot drift apart on that rule.
func CanAdministerAccount(actorRole, targetRole string) bool {
	if actorRole == GlobalSuperAdmin {
		return true
	}
	return actorRole == GlobalAdmin && targetRole != GlobalSuperAdmin
}

// ── Permission keys ───────────────────────────────────────────────────────────
//
// Permission keys are the single source of truth for project-level
// authorization. Each Can* function below delegates to HasPermission so the
// mapping between legacy checks and permission keys stays explicit.

const (
	PermProjectView              = "project.view"
	PermProjectUpdate            = "project.update"
	PermProjectDelete            = "project.delete"
	PermProjectArchive           = "project.archive"
	PermProjectInviteUsers       = "project.invite_users"
	PermProjectRemoveUsers       = "project.remove_users"
	PermProjectChangeRoles       = "project.change_roles"
	PermProjectTransferOwnership = "project.transfer_ownership"

	PermTaskCreate  = "task.create"
	PermTaskView    = "task.view"
	PermTaskUpdate  = "task.update"
	PermTaskDelete  = "task.delete"
	PermTaskAssign  = "task.assign"
	PermTaskComment = "task.comment"

	// PermBoardCreate gates creation of new boards (with default column
	// templates). Restricted to project owners and admins — this is an
	// intentional tightening from the previous "any writer" rule.
	PermBoardCreate = "board.create"
)

// AllPermissions returns every defined permission key.
func AllPermissions() []string {
	return []string{
		PermProjectView, PermProjectUpdate, PermProjectDelete, PermProjectArchive,
		PermProjectInviteUsers, PermProjectRemoveUsers, PermProjectChangeRoles, PermProjectTransferOwnership,
		PermTaskCreate, PermTaskView, PermTaskUpdate, PermTaskDelete, PermTaskAssign, PermTaskComment,
		PermBoardCreate,
	}
}

// permissionMatrix maps each permission key to the set of project roles that
// hold it. PROJECT_OWNER inherits everything PROJECT_ADMIN had, plus
// ownership-only permissions. PROJECT_ADMIN/MEMBER/VIEWER keep their current
// (pre-PROJECT_OWNER) behaviour.
var permissionMatrix = map[string]map[string]bool{
	PermProjectView:    {ProjectOwner: true, ProjectAdmin: true, ProjectMember: true, ProjectViewer: true},
	PermProjectUpdate:  {ProjectOwner: true, ProjectAdmin: true, ProjectMember: true},
	PermProjectDelete:  {ProjectOwner: true, ProjectAdmin: true},
	PermProjectArchive: {ProjectOwner: true, ProjectAdmin: true},

	PermProjectInviteUsers: {ProjectOwner: true, ProjectAdmin: true},
	PermProjectRemoveUsers: {ProjectOwner: true, ProjectAdmin: true},
	PermProjectChangeRoles: {ProjectOwner: true, ProjectAdmin: true},

	PermProjectTransferOwnership: {ProjectOwner: true},

	PermTaskCreate:  {ProjectOwner: true, ProjectAdmin: true, ProjectMember: true},
	PermTaskView:    {ProjectOwner: true, ProjectAdmin: true, ProjectMember: true, ProjectViewer: true},
	PermTaskUpdate:  {ProjectOwner: true, ProjectAdmin: true, ProjectMember: true},
	PermTaskDelete:  {ProjectOwner: true, ProjectAdmin: true, ProjectMember: true},
	PermTaskAssign:  {ProjectOwner: true, ProjectAdmin: true, ProjectMember: true},
	PermTaskComment: {ProjectOwner: true, ProjectAdmin: true, ProjectMember: true},

	PermBoardCreate: {ProjectOwner: true, ProjectAdmin: true},
}

// HasPermission is the single source of truth for project-level
// authorization decisions. SUPER_ADMIN always passes; otherwise the
// permission is granted if projectRole appears in permissionMatrix[permission].
func HasPermission(globalRole, projectRole, permission string) bool {
	if globalRole == GlobalSuperAdmin {
		return true
	}
	roles, ok := permissionMatrix[permission]
	if !ok {
		return false
	}
	return roles[projectRole]
}

// CanAssignRole reports whether an actor may assign role to a new or existing
// member. Granting PROJECT_OWNER requires the actor to already be a
// PROJECT_OWNER (or SUPER_ADMIN).
func CanAssignRole(globalRole, actorProjectRole, role string) bool {
	if globalRole == GlobalSuperAdmin {
		return true
	}
	if !HasPermission(globalRole, actorProjectRole, PermProjectInviteUsers) {
		return false
	}
	if role == ProjectOwner {
		return actorProjectRole == ProjectOwner
	}
	return true
}

// CanChangeRole reports whether an actor may change a member's role from
// currentRole to newRole (newRole == "" represents removing the member).
// Only an existing PROJECT_OWNER (or SUPER_ADMIN) may grant or revoke
// PROJECT_OWNER; this also protects an owner from being demoted/removed by a
// PROJECT_ADMIN.
func CanChangeRole(globalRole, actorProjectRole, currentRole, newRole string) bool {
	if globalRole == GlobalSuperAdmin {
		return true
	}
	if !HasPermission(globalRole, actorProjectRole, PermProjectChangeRoles) {
		return false
	}
	if (currentRole == ProjectOwner || newRole == ProjectOwner) && actorProjectRole != ProjectOwner {
		return false
	}
	return true
}

// WouldRemoveLastOwner reports whether changing a member from currentRole to
// newRole (or removing them, when newRole == "") would leave the project
// without a PROJECT_OWNER, given ownerCount existing owners (including the
// member being changed).
func WouldRemoveLastOwner(currentRole, newRole string, ownerCount int) bool {
	if currentRole != ProjectOwner {
		return false
	}
	if newRole == ProjectOwner {
		return false
	}
	return ownerCount <= 1
}

// ── Project permissions ───────────────────────────────────────────────────────

// CanCreateProject returns true if the actor may create a new project.
func CanCreateProject(globalRole string) bool {
	return globalRole == GlobalSuperAdmin || globalRole == GlobalAdmin
}

// CanViewProject returns true if the actor may view the project.
// Super Admin always passes; otherwise a non-empty projectRole is required.
func CanViewProject(globalRole, projectRole string) bool {
	return HasPermission(globalRole, projectRole, PermProjectView)
}

// CanEditProject returns true if the actor may edit project metadata.
func CanEditProject(globalRole, projectRole string) bool {
	return HasPermission(globalRole, projectRole, PermProjectUpdate)
}

// CanDeleteProject returns true if the actor may delete the project.
func CanDeleteProject(globalRole, projectRole string) bool {
	return HasPermission(globalRole, projectRole, PermProjectDelete)
}

// CanManageProjectMembers returns true if the actor may add/change/remove members.
func CanManageProjectMembers(globalRole, projectRole string) bool {
	return HasPermission(globalRole, projectRole, PermProjectInviteUsers)
}

// ── Task permissions ──────────────────────────────────────────────────────────

// CanCreateTask returns true if the actor may create tasks in the project.
func CanCreateTask(globalRole, projectRole string) bool {
	return HasPermission(globalRole, projectRole, PermTaskCreate)
}

// CanEditTask returns true if the actor may edit the task.
func CanEditTask(globalRole, projectRole string) bool {
	return HasPermission(globalRole, projectRole, PermTaskUpdate)
}

// CanDeleteTask returns true if the actor may delete the task.
func CanDeleteTask(globalRole, projectRole string) bool {
	return HasPermission(globalRole, projectRole, PermTaskDelete)
}

// CanViewTask returns true if the actor may view the task.
func CanViewTask(globalRole, projectRole string) bool {
	return HasPermission(globalRole, projectRole, PermTaskView)
}

// ── Audit-log access ─────────────────────────────────────────────────────────

// CanViewAuditLogs returns true if the actor may read the audit log.
func CanViewAuditLogs(globalRole string) bool {
	return globalRole == GlobalSuperAdmin
}
