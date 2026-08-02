package workmanagement_test

import (
	"net/http"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
)

// Project-scoped read routes must enforce project membership just like the
// task read routes do. These guard the fix for a broken-access-control bug
// where ListReleases/ListSprints/ListCategories/ListTemplates and GetTemplate
// returned project data to any authenticated user, member or not (a non-member
// could enumerate releases, sprints, categories and task templates — including
// those of PRIVATE projects). SecondUserID is a seeded USER never added as a
// member below, standing in for an arbitrary logged-in stranger.
func TestProjectReadRoutes_NonMember_Forbidden(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Confidential")

	// A template to exercise the entity-scoped GetTemplate route.
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/task-templates",
		map[string]string{"name": "Secret", "taskType": "TASK", "priority": "HIGH"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var tmpl map[string]interface{}
	testutil.DecodeJSON(t, resp, &tmpl)
	tmplID := tmpl["id"].(string)

	for _, path := range []string{
		"/api/v1/projects/" + pid + "/releases",
		"/api/v1/projects/" + pid + "/sprints",
		"/api/v1/projects/" + pid + "/task-categories",
		"/api/v1/projects/" + pid + "/task-templates",
		"/api/v1/task-templates/" + tmplID,
	} {
		r := testutil.Do(t, srv, http.MethodGet, path, nil, testutil.SecondUserID)
		testutil.AssertStatus(t, r, http.StatusForbidden)
		_ = r.Body.Close()
	}
}

// A member still reads all of them — the guard must not lock out legitimate
// access. DemoUserID owns projects from MustCreateProject, so it is a member.
func TestProjectReadRoutes_Member_OK(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Mine")

	for _, path := range []string{
		"/api/v1/projects/" + pid + "/releases",
		"/api/v1/projects/" + pid + "/sprints",
		"/api/v1/projects/" + pid + "/task-categories",
		"/api/v1/projects/" + pid + "/task-templates",
	} {
		r := testutil.Do(t, srv, http.MethodGet, path, nil, testutil.DemoUserID)
		testutil.AssertStatus(t, r, http.StatusOK)
		_ = r.Body.Close()
	}
}
