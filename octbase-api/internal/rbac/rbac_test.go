package rbac_test

import (
	"testing"

	"github.com/octbase/octbase-api/internal/rbac"
)

func TestCanManageAccounts(t *testing.T) {
	if !rbac.CanManageAccounts(rbac.GlobalSuperAdmin) {
		t.Error("Super Admin must be able to manage accounts")
	}
	for _, role := range []string{rbac.GlobalAdmin, rbac.GlobalUser, rbac.GlobalGuest} {
		if rbac.CanManageAccounts(role) {
			t.Errorf("%s must not be able to manage accounts", role)
		}
	}
}

func TestCanCreateAdmin(t *testing.T) {
	if !rbac.CanCreateAdmin(rbac.GlobalSuperAdmin) {
		t.Error("Super Admin must be able to create Admin")
	}
	if rbac.CanCreateAdmin(rbac.GlobalAdmin) {
		t.Error("Admin must not be able to create another Admin")
	}
}

func TestCanUpdateUserRole(t *testing.T) {
	// Super Admin can change a regular user's role.
	if !rbac.CanUpdateUserRole(rbac.GlobalSuperAdmin, rbac.GlobalUser, rbac.GlobalAdmin) {
		t.Error("Super Admin should be able to promote User to Admin")
	}
	// Super Admin cannot promote anyone to Super Admin via API.
	if rbac.CanUpdateUserRole(rbac.GlobalSuperAdmin, rbac.GlobalUser, rbac.GlobalSuperAdmin) {
		t.Error("No one may assign SUPER_ADMIN via the API")
	}
	// Super Admin cannot demote another Super Admin.
	if rbac.CanUpdateUserRole(rbac.GlobalSuperAdmin, rbac.GlobalSuperAdmin, rbac.GlobalUser) {
		t.Error("Super Admin cannot demote another Super Admin")
	}
	// Admin cannot change any role.
	if rbac.CanUpdateUserRole(rbac.GlobalAdmin, rbac.GlobalUser, rbac.GlobalAdmin) {
		t.Error("Admin must not be able to change roles")
	}
}

func TestCanDisableUser(t *testing.T) {
	if !rbac.CanDisableUser(rbac.GlobalSuperAdmin, rbac.GlobalAdmin) {
		t.Error("Super Admin must be able to disable Admin")
	}
	if rbac.CanDisableUser(rbac.GlobalSuperAdmin, rbac.GlobalSuperAdmin) {
		t.Error("Super Admin must not be able to disable another Super Admin")
	}
	if rbac.CanDisableUser(rbac.GlobalAdmin, rbac.GlobalUser) {
		t.Error("Admin must not be able to disable users")
	}
}

func TestCanDeleteUser(t *testing.T) {
	if !rbac.CanDeleteUser(rbac.GlobalSuperAdmin, rbac.GlobalAdmin) {
		t.Error("Super Admin must be able to delete Admin")
	}
	if !rbac.CanDeleteUser(rbac.GlobalSuperAdmin, rbac.GlobalUser) {
		t.Error("Super Admin must be able to delete User")
	}
	if rbac.CanDeleteUser(rbac.GlobalSuperAdmin, rbac.GlobalSuperAdmin) {
		t.Error("Super Admin must not be able to delete another Super Admin")
	}
	if rbac.CanDeleteUser(rbac.GlobalAdmin, rbac.GlobalUser) {
		t.Error("Admin must not be able to delete users")
	}
}

func TestCanViewProject(t *testing.T) {
	for _, role := range []string{rbac.ProjectOwner, rbac.ProjectAdmin, rbac.ProjectMember, rbac.ProjectViewer} {
		if !rbac.CanViewProject(rbac.GlobalUser, role) {
			t.Errorf("%s must be able to view the project", role)
		}
	}
	if rbac.CanViewProject(rbac.GlobalUser, "") {
		t.Error("a non-member User must not be able to view the project")
	}
	if !rbac.CanViewProject(rbac.GlobalSuperAdmin, "") {
		t.Error("Super Admin must be able to view any project without membership")
	}
}

func TestCanEditProject(t *testing.T) {
	for _, role := range []string{rbac.ProjectOwner, rbac.ProjectAdmin, rbac.ProjectMember} {
		if !rbac.CanEditProject(rbac.GlobalUser, role) {
			t.Errorf("%s must be able to edit project metadata", role)
		}
	}
	if rbac.CanEditProject(rbac.GlobalUser, rbac.ProjectViewer) {
		t.Error("PROJECT_VIEWER must not be able to edit project metadata")
	}
	if !rbac.CanEditProject(rbac.GlobalSuperAdmin, "") {
		t.Error("Super Admin must be able to edit any project")
	}
}

func TestCanDeleteProject(t *testing.T) {
	for _, role := range []string{rbac.ProjectOwner, rbac.ProjectAdmin} {
		if !rbac.CanDeleteProject(rbac.GlobalUser, role) {
			t.Errorf("%s must be able to delete the project", role)
		}
	}
	for _, role := range []string{rbac.ProjectMember, rbac.ProjectViewer} {
		if rbac.CanDeleteProject(rbac.GlobalUser, role) {
			t.Errorf("%s must not be able to delete the project", role)
		}
	}
	if !rbac.CanDeleteProject(rbac.GlobalSuperAdmin, "") {
		t.Error("Super Admin must be able to delete any project")
	}
}

func TestCanCreateProject(t *testing.T) {
	if !rbac.CanCreateProject(rbac.GlobalSuperAdmin) {
		t.Error("Super Admin must be able to create projects")
	}
	if !rbac.CanCreateProject(rbac.GlobalAdmin) {
		t.Error("Admin must be able to create projects")
	}
	if rbac.CanCreateProject(rbac.GlobalUser) {
		t.Error("User must not be able to create projects")
	}
	if rbac.CanCreateProject(rbac.GlobalGuest) {
		t.Error("Guest must not be able to create projects")
	}
}

func TestCanManageProjectMembers(t *testing.T) {
	// Super Admin always passes regardless of project role.
	if !rbac.CanManageProjectMembers(rbac.GlobalSuperAdmin, "") {
		t.Error("Super Admin must manage members of any project")
	}
	if !rbac.CanManageProjectMembers(rbac.GlobalAdmin, rbac.ProjectAdmin) {
		t.Error("Admin PROJECT_ADMIN must manage members")
	}
	if rbac.CanManageProjectMembers(rbac.GlobalAdmin, rbac.ProjectMember) {
		t.Error("Admin PROJECT_MEMBER must not manage members")
	}
	if rbac.CanManageProjectMembers(rbac.GlobalUser, rbac.ProjectMember) {
		t.Error("User must not manage members")
	}
}

func TestGuestReadOnly(t *testing.T) {
	if rbac.CanCreateTask(rbac.GlobalGuest, rbac.ProjectViewer) {
		t.Error("Guest must not create tasks")
	}
	if rbac.CanEditTask(rbac.GlobalGuest, rbac.ProjectViewer) {
		t.Error("Guest must not edit tasks")
	}
	if rbac.CanDeleteTask(rbac.GlobalGuest, rbac.ProjectViewer) {
		t.Error("Guest must not delete tasks")
	}
	if !rbac.CanViewTask(rbac.GlobalGuest, rbac.ProjectViewer) {
		t.Error("Guest must be able to view tasks")
	}
}

func TestCanViewAuditLogs(t *testing.T) {
	if !rbac.CanViewAuditLogs(rbac.GlobalSuperAdmin) {
		t.Error("Super Admin must view audit logs")
	}
	for _, role := range []string{rbac.GlobalAdmin, rbac.GlobalUser, rbac.GlobalGuest} {
		if rbac.CanViewAuditLogs(role) {
			t.Errorf("%s must not view audit logs", role)
		}
	}
}

func TestIsValidRole(t *testing.T) {
	for _, r := range rbac.ValidGlobalRoles() {
		if !rbac.IsValidGlobalRole(r) {
			t.Errorf("expected %q to be valid global role", r)
		}
	}
	if rbac.IsValidGlobalRole("HACKER") {
		t.Error("HACKER must not be a valid role")
	}
	for _, r := range rbac.ValidProjectRoles() {
		if !rbac.IsValidProjectRole(r) {
			t.Errorf("expected %q to be valid project role", r)
		}
	}
	if !rbac.IsValidProjectRole(rbac.ProjectOwner) {
		t.Error("PROJECT_OWNER must be a valid project role")
	}
	if rbac.IsValidProjectRole("DEVELOPER") {
		t.Error("DEVELOPER must not be a valid project role")
	}
}

// ── HasPermission matrix ────────────────────────────────────────────────────

// TestHasPermission checks the permission matrix for every permission key
// against every project role (plus non-member ""), ensuring PROJECT_ADMIN,
// PROJECT_MEMBER and PROJECT_VIEWER keep their pre-PROJECT_OWNER behaviour and
// PROJECT_OWNER is a superset of PROJECT_ADMIN plus ownership-only permissions.
func TestHasPermission(t *testing.T) {
	// expected[permission] = allowed roles (PROJECT_VIEWER/"" omitted implies denied)
	type want struct {
		owner, admin, member, viewer, none bool
	}
	cases := map[string]want{
		rbac.PermProjectView:              {true, true, true, true, false},
		rbac.PermProjectUpdate:            {true, true, true, false, false},
		rbac.PermProjectDelete:            {true, true, false, false, false},
		rbac.PermProjectArchive:           {true, true, false, false, false},
		rbac.PermProjectInviteUsers:       {true, true, false, false, false},
		rbac.PermProjectRemoveUsers:       {true, true, false, false, false},
		rbac.PermProjectChangeRoles:       {true, true, false, false, false},
		rbac.PermProjectTransferOwnership: {true, false, false, false, false},
		rbac.PermTaskCreate:               {true, true, true, false, false},
		rbac.PermTaskView:                 {true, true, true, true, false},
		rbac.PermTaskUpdate:               {true, true, true, false, false},
		rbac.PermTaskDelete:               {true, true, true, false, false},
		rbac.PermTaskAssign:               {true, true, true, false, false},
		rbac.PermTaskComment:              {true, true, true, false, false},
		rbac.PermBoardCreate:              {true, true, false, false, false},
	}

	for _, perm := range rbac.AllPermissions() {
		w, ok := cases[perm]
		if !ok {
			t.Fatalf("permission %q has no expectation in test matrix", perm)
		}
		roles := map[string]bool{
			rbac.ProjectOwner:  w.owner,
			rbac.ProjectAdmin:  w.admin,
			rbac.ProjectMember: w.member,
			rbac.ProjectViewer: w.viewer,
			"":                 w.none,
		}
		for role, want := range roles {
			if got := rbac.HasPermission(rbac.GlobalUser, role, perm); got != want {
				t.Errorf("HasPermission(USER, %q, %q) = %v, want %v", role, perm, got, want)
			}
		}
	}

	// Verify the matrix is a strict superset for PROJECT_OWNER relative to PROJECT_ADMIN.
	for _, perm := range rbac.AllPermissions() {
		if rbac.HasPermission(rbac.GlobalUser, rbac.ProjectAdmin, perm) && !rbac.HasPermission(rbac.GlobalUser, rbac.ProjectOwner, perm) {
			t.Errorf("PROJECT_OWNER must retain permission %q held by PROJECT_ADMIN", perm)
		}
	}
}

func TestHasPermission_SuperAdminBypassesEverything(t *testing.T) {
	for _, perm := range rbac.AllPermissions() {
		if !rbac.HasPermission(rbac.GlobalSuperAdmin, "", perm) {
			t.Errorf("SUPER_ADMIN must hold permission %q even without a membership", perm)
		}
	}
	// Even an unknown permission key is granted to SUPER_ADMIN.
	if !rbac.HasPermission(rbac.GlobalSuperAdmin, "", "some.unknown.permission") {
		t.Error("SUPER_ADMIN must bypass unknown permission keys too")
	}
	if rbac.HasPermission(rbac.GlobalUser, rbac.ProjectOwner, "some.unknown.permission") {
		t.Error("unknown permission keys must be denied for non-Super-Admins")
	}
}

// ── Privilege escalation: CanAssignRole / CanChangeRole ─────────────────────

func TestCanAssignRole(t *testing.T) {
	// PROJECT_ADMIN may add ordinary members.
	if !rbac.CanAssignRole(rbac.GlobalUser, rbac.ProjectAdmin, rbac.ProjectMember) {
		t.Error("PROJECT_ADMIN must be able to add a PROJECT_MEMBER")
	}
	// PROJECT_ADMIN may not grant PROJECT_OWNER.
	if rbac.CanAssignRole(rbac.GlobalUser, rbac.ProjectAdmin, rbac.ProjectOwner) {
		t.Error("PROJECT_ADMIN must not be able to grant PROJECT_OWNER")
	}
	// PROJECT_OWNER may grant PROJECT_OWNER.
	if !rbac.CanAssignRole(rbac.GlobalUser, rbac.ProjectOwner, rbac.ProjectOwner) {
		t.Error("PROJECT_OWNER must be able to grant PROJECT_OWNER")
	}
	// PROJECT_MEMBER may not add members at all.
	if rbac.CanAssignRole(rbac.GlobalUser, rbac.ProjectMember, rbac.ProjectMember) {
		t.Error("PROJECT_MEMBER must not be able to add members")
	}
	// SUPER_ADMIN can do anything regardless of project role.
	if !rbac.CanAssignRole(rbac.GlobalSuperAdmin, "", rbac.ProjectOwner) {
		t.Error("SUPER_ADMIN must be able to grant PROJECT_OWNER")
	}
}

func TestCanChangeRole(t *testing.T) {
	// PROJECT_ADMIN changing a PROJECT_MEMBER to PROJECT_VIEWER: fine.
	if !rbac.CanChangeRole(rbac.GlobalUser, rbac.ProjectAdmin, rbac.ProjectMember, rbac.ProjectViewer) {
		t.Error("PROJECT_ADMIN must be able to change a PROJECT_MEMBER's role")
	}
	// PROJECT_ADMIN must not be able to promote anyone to PROJECT_OWNER.
	if rbac.CanChangeRole(rbac.GlobalUser, rbac.ProjectAdmin, rbac.ProjectMember, rbac.ProjectOwner) {
		t.Error("PROJECT_ADMIN must not be able to grant PROJECT_OWNER")
	}
	// PROJECT_ADMIN must not be able to demote a PROJECT_OWNER.
	if rbac.CanChangeRole(rbac.GlobalUser, rbac.ProjectAdmin, rbac.ProjectOwner, rbac.ProjectAdmin) {
		t.Error("PROJECT_ADMIN must not be able to demote PROJECT_OWNER")
	}
	// PROJECT_ADMIN must not be able to remove a PROJECT_OWNER (newRole == "").
	if rbac.CanChangeRole(rbac.GlobalUser, rbac.ProjectAdmin, rbac.ProjectOwner, "") {
		t.Error("PROJECT_ADMIN must not be able to remove PROJECT_OWNER")
	}
	// PROJECT_OWNER may demote/transfer another PROJECT_OWNER.
	if !rbac.CanChangeRole(rbac.GlobalUser, rbac.ProjectOwner, rbac.ProjectOwner, rbac.ProjectAdmin) {
		t.Error("PROJECT_OWNER must be able to demote another PROJECT_OWNER")
	}
	// PROJECT_MEMBER cannot change anyone's role.
	if rbac.CanChangeRole(rbac.GlobalUser, rbac.ProjectMember, rbac.ProjectViewer, rbac.ProjectMember) {
		t.Error("PROJECT_MEMBER must not be able to change roles")
	}
	// SUPER_ADMIN bypasses all escalation rules.
	if !rbac.CanChangeRole(rbac.GlobalSuperAdmin, "", rbac.ProjectOwner, rbac.ProjectViewer) {
		t.Error("SUPER_ADMIN must be able to change any role")
	}
}

// ── Last-owner protection ─────────────────────────────────────────────────────

func TestWouldRemoveLastOwner(t *testing.T) {
	cases := []struct {
		name                 string
		currentRole, newRole string
		ownerCount           int
		want                 bool
	}{
		{"demote sole owner", rbac.ProjectOwner, rbac.ProjectAdmin, 1, true},
		{"remove sole owner", rbac.ProjectOwner, "", 1, true},
		{"demote owner with a second owner", rbac.ProjectOwner, rbac.ProjectAdmin, 2, false},
		{"remove owner with a second owner", rbac.ProjectOwner, "", 2, false},
		{"owner-to-owner transfer never violates", rbac.ProjectOwner, rbac.ProjectOwner, 1, false},
		{"changing a non-owner never violates", rbac.ProjectAdmin, rbac.ProjectViewer, 1, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := rbac.WouldRemoveLastOwner(c.currentRole, c.newRole, c.ownerCount); got != c.want {
				t.Errorf("WouldRemoveLastOwner(%q, %q, %d) = %v, want %v", c.currentRole, c.newRole, c.ownerCount, got, c.want)
			}
		})
	}
}
