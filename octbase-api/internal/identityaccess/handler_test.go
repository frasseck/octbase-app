package identityaccess_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
)

func TestGetMe_OK(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/users/me", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	var user map[string]interface{}
	testutil.DecodeJSON(t, resp, &user)
	if user["id"] != testutil.DemoUserID {
		t.Errorf("id = %v, want %v", user["id"], testutil.DemoUserID)
	}
	if user["email"] != "demo@octbase.dev" {
		t.Errorf("email = %v", user["email"])
	}
	if user["displayName"] != "Demo User" {
		t.Errorf("displayName = %v", user["displayName"])
	}
}

func TestGetMe_MissingToken(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	// No token → 401.
	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/users/me", nil, "")
	testutil.AssertStatus(t, resp, http.StatusUnauthorized)
}

// TestGetMe_UnknownUser: LoadUserGlobalRole rejects any token whose user id
// does not exist in the users table, so an unknown UUID gets 401 before the
// handler is reached.
func TestGetMe_UnknownUser(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/users/me", nil, "aaaaaaaa-0000-0000-0000-000000000099")
	testutil.AssertStatus(t, resp, http.StatusUnauthorized)
}

// TestGetMe_OK verifies the /me endpoint returns the authenticated user's profile.
// User creation via open registration is replaced by the invitation flow.
// Seeds from testutil are used as the user population.

// TestListMemberships_HasCreatorAsAdmin: creating a project auto-adds the
// creator as PROJECT_OWNER, so the memberships list is never empty.
func TestListMemberships_HasCreatorAsAdmin(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Membership Project")

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/memberships", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var ms []interface{}
	testutil.DecodeJSON(t, resp, &ms)
	if len(ms) != 1 {
		t.Errorf("expected 1 membership (creator as PROJECT_ADMIN), got %d", len(ms))
	}
}

// TestListMembers returns enriched member data.
func TestListMembers(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/members", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var ms []map[string]interface{}
	testutil.DecodeJSON(t, resp, &ms)
	if len(ms) != 1 {
		t.Fatalf("expected 1 member, got %d", len(ms))
	}
	if ms[0]["role"] != "PROJECT_OWNER" {
		t.Errorf("role = %v, want PROJECT_OWNER", ms[0]["role"])
	}
	if ms[0]["email"] == nil || ms[0]["email"] == "" {
		t.Error("email should be present in member data")
	}
}

// TestAddMembership: DemoUser (PROJECT_ADMIN) adds SecondUser as PROJECT_MEMBER.
func TestAddMembership(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/memberships",
		map[string]string{"userId": testutil.SecondUserID, "role": "PROJECT_MEMBER"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var m map[string]interface{}
	testutil.DecodeJSON(t, resp, &m)
	if m["userId"] != testutil.SecondUserID {
		t.Errorf("userId = %v", m["userId"])
	}
	if m["role"] != "PROJECT_MEMBER" {
		t.Errorf("role = %v, want PROJECT_MEMBER", m["role"])
	}
}

func TestAddMembership_DefaultRole(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/memberships",
		map[string]string{"userId": testutil.SecondUserID}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var m map[string]interface{}
	testutil.DecodeJSON(t, resp, &m)
	if m["role"] != "PROJECT_MEMBER" {
		t.Errorf("default role = %v, want PROJECT_MEMBER", m["role"])
	}
}

func TestAddMembership_ForbiddenForNonOwner(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	// Add SecondUser as PROJECT_MEMBER.
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_MEMBER")

	// SecondUser (PROJECT_MEMBER) tries to add another member – should be 403.
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/memberships",
		map[string]string{"userId": testutil.SecondUserID, "role": "PROJECT_VIEWER"}, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
}

func TestUpdateMembershipRole(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	// First add SecondUser as PROJECT_MEMBER.
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_MEMBER")

	// DemoUser (PROJECT_ADMIN) updates SecondUser's role to PROJECT_VIEWER.
	resp := testutil.Do(t, srv, http.MethodPatch,
		"/api/v1/projects/"+pid+"/memberships/"+testutil.SecondUserID,
		map[string]string{"role": "PROJECT_VIEWER"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
}

func TestRemoveMembership(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	// Add SecondUser, then remove them.
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_MEMBER")

	resp := testutil.Do(t, srv, http.MethodDelete,
		"/api/v1/projects/"+pid+"/memberships/"+testutil.SecondUserID, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNoContent)

	// Only the PROJECT_OWNER (DemoUser) remains.
	listResp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/memberships", nil, testutil.DemoUserID)
	var ms []interface{}
	testutil.DecodeJSON(t, listResp, &ms)
	if len(ms) != 1 {
		t.Errorf("expected 1 membership after removal, got %d", len(ms))
	}
}

// ── GET /permissions ─────────────────────────────────────────────────────────

func TestGetPermissions_Owner(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/permissions", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var out map[string]interface{}
	testutil.DecodeJSON(t, resp, &out)

	if out["role"] != "PROJECT_OWNER" {
		t.Errorf("role = %v, want PROJECT_OWNER", out["role"])
	}
	perms, ok := out["permissions"].(map[string]interface{})
	if !ok {
		t.Fatalf("permissions field missing or wrong type: %v", out["permissions"])
	}
	for _, p := range []string{"project.delete", "project.transfer_ownership", "project.change_roles", "task.delete"} {
		if perms[p] != true {
			t.Errorf("permissions[%q] = %v, want true for PROJECT_OWNER", p, perms[p])
		}
	}
}

func TestGetPermissions_Viewer(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_VIEWER")

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/permissions", nil, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var out map[string]interface{}
	testutil.DecodeJSON(t, resp, &out)

	if out["role"] != "PROJECT_VIEWER" {
		t.Errorf("role = %v, want PROJECT_VIEWER", out["role"])
	}
	perms := out["permissions"].(map[string]interface{})
	if perms["project.view"] != true || perms["task.view"] != true {
		t.Errorf("PROJECT_VIEWER must hold view permissions: %v", perms)
	}
	for _, p := range []string{"task.update", "task.delete", "project.update", "project.delete"} {
		if perms[p] != false {
			t.Errorf("permissions[%q] = %v, want false for PROJECT_VIEWER", p, perms[p])
		}
	}
}

func TestGetPermissions_NotAMember(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/permissions", nil, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
}

// ── Privilege escalation on role changes ────────────────────────────────────

// TestUpdateMembershipRole_AdminCannotGrantOwner: a PROJECT_ADMIN cannot
// promote another member to PROJECT_OWNER.
func TestUpdateMembershipRole_AdminCannotGrantOwner(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	// Promote SecondUser to PROJECT_ADMIN, add GuestUser as PROJECT_MEMBER.
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_ADMIN")
	testutil.MustAddMember(t, srv, pid, testutil.GuestUserID, "PROJECT_MEMBER")

	// SecondUser (PROJECT_ADMIN) tries to promote GuestUser to PROJECT_OWNER.
	resp := testutil.Do(t, srv, http.MethodPatch,
		"/api/v1/projects/"+pid+"/memberships/"+testutil.GuestUserID,
		map[string]string{"role": "PROJECT_OWNER"}, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
}

// TestUpdateMembershipRole_AdminCannotDemoteOwner: a PROJECT_ADMIN cannot
// demote the project's PROJECT_OWNER.
func TestUpdateMembershipRole_AdminCannotDemoteOwner(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_ADMIN")

	// SecondUser (PROJECT_ADMIN) tries to demote DemoUser (PROJECT_OWNER).
	resp := testutil.Do(t, srv, http.MethodPatch,
		"/api/v1/projects/"+pid+"/memberships/"+testutil.DemoUserID,
		map[string]string{"role": "PROJECT_ADMIN"}, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
}

// TestUpdateMembershipRole_OwnerCanTransferOwnership: an existing
// PROJECT_OWNER may promote another member to PROJECT_OWNER and demote
// themselves, as long as an owner remains at every step.
func TestUpdateMembershipRole_OwnerCanTransferOwnership(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_MEMBER")

	// DemoUser (PROJECT_OWNER) promotes SecondUser to PROJECT_OWNER.
	resp := testutil.Do(t, srv, http.MethodPatch,
		"/api/v1/projects/"+pid+"/memberships/"+testutil.SecondUserID,
		map[string]string{"role": "PROJECT_OWNER"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	// Now DemoUser can step down to PROJECT_ADMIN since SecondUser is also an owner.
	resp = testutil.Do(t, srv, http.MethodPatch,
		"/api/v1/projects/"+pid+"/memberships/"+testutil.DemoUserID,
		map[string]string{"role": "PROJECT_ADMIN"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
}

// TestUpdateMembershipRole_SuperAdminBypassesEscalation: SUPER_ADMIN may
// grant/revoke PROJECT_OWNER regardless of their own project membership.
func TestUpdateMembershipRole_SuperAdminBypassesEscalation(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_MEMBER")

	resp := testutil.Do(t, srv, http.MethodPatch,
		"/api/v1/projects/"+pid+"/memberships/"+testutil.SecondUserID,
		map[string]string{"role": "PROJECT_OWNER"}, testutil.SuperAdminUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
}

// ── Last-owner protection ─────────────────────────────────────────────────────

// TestUpdateMembershipRole_LastOwnerDemotionRejected: demoting the sole
// PROJECT_OWNER is rejected with 422 LAST_OWNER.
func TestUpdateMembershipRole_LastOwnerDemotionRejected(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	resp := testutil.Do(t, srv, http.MethodPatch,
		"/api/v1/projects/"+pid+"/memberships/"+testutil.DemoUserID,
		map[string]string{"role": "PROJECT_ADMIN"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
	var out map[string]interface{}
	testutil.DecodeJSON(t, resp, &out)
	if out["code"] != "LAST_OWNER" {
		t.Errorf("code = %v, want LAST_OWNER", out["code"])
	}
}

// TestUpdateMembershipRole_LastOwnerDemotionSucceedsWithSecondOwner verifies
// the demotion succeeds once a second PROJECT_OWNER exists.
func TestUpdateMembershipRole_LastOwnerDemotionSucceedsWithSecondOwner(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_OWNER")

	resp := testutil.Do(t, srv, http.MethodPatch,
		"/api/v1/projects/"+pid+"/memberships/"+testutil.DemoUserID,
		map[string]string{"role": "PROJECT_ADMIN"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
}

// TestRemoveMembership_LastOwnerRejected: removing the sole PROJECT_OWNER is
// rejected with 422 LAST_OWNER.
func TestRemoveMembership_LastOwnerRejected(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	resp := testutil.Do(t, srv, http.MethodDelete,
		"/api/v1/projects/"+pid+"/memberships/"+testutil.DemoUserID, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
	var out map[string]interface{}
	testutil.DecodeJSON(t, resp, &out)
	if out["code"] != "LAST_OWNER" {
		t.Errorf("code = %v, want LAST_OWNER", out["code"])
	}
}

// TestRemoveMembership_LastOwnerSucceedsWithSecondOwner verifies removal
// succeeds once a second PROJECT_OWNER exists.
func TestRemoveMembership_LastOwnerSucceedsWithSecondOwner(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_OWNER")

	resp := testutil.Do(t, srv, http.MethodDelete,
		"/api/v1/projects/"+pid+"/memberships/"+testutil.DemoUserID, nil, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusNoContent)
}

// ── Member-add escalation ────────────────────────────────────────────────────

// TestAddMembership_NonOwnerCannotGrantOwner: a PROJECT_ADMIN inviting a new
// member as PROJECT_OWNER is rejected.
func TestAddMembership_NonOwnerCannotGrantOwner(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_ADMIN")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/memberships",
		map[string]string{"userId": testutil.GuestUserID, "role": "PROJECT_OWNER"}, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
}

// ── Assignable users (assignee/reviewer pickers) ─────────────────────────────

// assignableByID indexes a GET /assignable-users response by user id.
func assignableByID(t *testing.T, srv *httptest.Server, pid, actorID string) map[string]map[string]interface{} {
	t.Helper()
	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/assignable-users", nil, actorID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var us []map[string]interface{}
	testutil.DecodeJSON(t, resp, &us)
	byID := map[string]map[string]interface{}{}
	for _, u := range us {
		id, _ := u["userId"].(string)
		if _, dup := byID[id]; dup {
			t.Errorf("user %s listed twice", id)
		}
		byID[id] = u
	}
	return byID
}

// TestListAssignableUsers_IncludesGlobalAdmins is the reason this endpoint
// exists: the assignee/reviewer pickers used to read the membership list, which
// silently excluded global admins because they reach a project without holding
// a membership row. A Super Admin who never joined the project must still be
// selectable; the ordinary USER and GUEST accounts must not be.
func TestListAssignableUsers_IncludesGlobalAdmins(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	byID := assignableByID(t, srv, pid, testutil.DemoUserID)

	// The creator is a member and keeps their project role.
	creator, ok := byID[testutil.DemoUserID]
	if !ok {
		t.Fatalf("creator missing from assignable users: %v", byID)
	}
	if creator["member"] != true {
		t.Errorf("creator member = %v, want true", creator["member"])
	}
	if creator["role"] != "PROJECT_OWNER" {
		t.Errorf("creator role = %v, want PROJECT_OWNER", creator["role"])
	}

	// The Super Admin is not a member but must be assignable, with an empty
	// project role marking them as such.
	sa, ok := byID[testutil.SuperAdminUserID]
	if !ok {
		t.Fatalf("super admin missing from assignable users: %v", byID)
	}
	if sa["member"] != false {
		t.Errorf("super admin member = %v, want false", sa["member"])
	}
	if sa["role"] != "" {
		t.Errorf("super admin role = %q, want empty", sa["role"])
	}
	if sa["globalRole"] != "SUPER_ADMIN" {
		t.Errorf("super admin globalRole = %v", sa["globalRole"])
	}
	// A non-member admin's email is withheld: the pickers show display names,
	// and this endpoint must not hand every project member a directory of admin
	// addresses. Members' emails are unchanged — GET /members already has them.
	if sa["email"] != "" {
		t.Errorf("super admin email = %v, want it withheld for a non-member", sa["email"])
	}
	if creator["email"] != "demo@octbase.dev" {
		t.Errorf("member email = %v, want it kept", creator["email"])
	}

	// Non-admin non-members stay out, and so does the disabled ADMIN account.
	for _, id := range []string{testutil.SecondUserID, testutil.GuestUserID, testutil.DisabledUserID} {
		if u, found := byID[id]; found {
			t.Errorf("non-member %s must not be assignable: %v", id, u)
		}
	}
}

// TestListAssignableUsers_MemberWinsOverGlobalAdmin: an admin who *is* a member
// is listed once, with their project role rather than as a bare global admin.
func TestListAssignableUsers_MemberWinsOverGlobalAdmin(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	testutil.MustAddMember(t, srv, pid, testutil.SuperAdminUserID, "PROJECT_VIEWER")
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_MEMBER")

	byID := assignableByID(t, srv, pid, testutil.DemoUserID)

	sa := byID[testutil.SuperAdminUserID]
	if sa == nil || sa["member"] != true || sa["role"] != "PROJECT_VIEWER" {
		t.Errorf("super admin as member = %v, want member with PROJECT_VIEWER", sa)
	}
	// An ordinary member is included once they join.
	if second := byID[testutil.SecondUserID]; second == nil || second["member"] != true {
		t.Errorf("added member missing or not marked as member: %v", second)
	}
}

// TestListAssignableUsers_NonMemberForbidden: the candidate list carries names
// and email addresses of a project's members, so it is guarded exactly like
// GET /members — a USER outside the project gets 403.
func TestListAssignableUsers_NonMemberForbidden(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	resp := testutil.Do(t, srv, http.MethodGet,
		"/api/v1/projects/"+pid+"/assignable-users", nil, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
}

// TestAssignTask_GlobalAdminReviewer is the end-to-end shape of the reported
// bug: a Super Admin who is not a project member can be set as a task's
// reviewer, and the task reads back with them in place.
func TestAssignTask_GlobalAdminReviewer(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "Needs review")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/assign",
		map[string]any{"reviewerId": testutil.SuperAdminUserID}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	resp = testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+tid, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var task map[string]interface{}
	testutil.DecodeJSON(t, resp, &task)
	if task["reviewerId"] != testutil.SuperAdminUserID {
		t.Errorf("reviewerId = %v, want %v", task["reviewerId"], testutil.SuperAdminUserID)
	}
	// The creator is recorded as the reporter, which is what the UI shows.
	if task["reporterId"] != testutil.DemoUserID {
		t.Errorf("reporterId = %v, want %v", task["reporterId"], testutil.DemoUserID)
	}
}
