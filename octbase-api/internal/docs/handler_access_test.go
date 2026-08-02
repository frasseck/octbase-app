package docs_test

import (
	"net/http"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
)

// A non-member must not be able to read a project's wiki pages through ANY of
// the docs read routes. These guard the fix for the broken-access-control bug
// where ListPages/GetPage/ListRevisions/ListReferences/SearchPages skipped the
// project membership check that every other bounded context enforces, letting
// any authenticated user read (and full-text search) pages of a project they
// are not a member of. SecondUserID is a seeded USER who is never added as a
// member below, so it stands in for an arbitrary logged-in stranger.
func TestDocsReadRoutes_NonMember_Forbidden(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Confidential")
	page := mustCreatePageID(t, srv, pid, "Secret Runbook", "= Secret\n\ncredential inside")

	cases := []struct {
		name   string
		method string
		path   string
	}{
		{"list pages", http.MethodGet, "/api/v1/projects/" + pid + "/pages"},
		{"get page", http.MethodGet, "/api/v1/pages/" + page},
		{"list revisions", http.MethodGet, "/api/v1/pages/" + page + "/revisions"},
		{"list references", http.MethodGet, "/api/v1/pages/" + page + "/references"},
		{"search pages", http.MethodGet, "/api/v1/projects/" + pid + "/search/pages?q=credential"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := testutil.Do(t, srv, tc.method, tc.path, nil, testutil.SecondUserID)
			testutil.AssertStatus(t, resp, http.StatusForbidden)
			_ = resp.Body.Close()
		})
	}
}

// A member reading through the same routes still succeeds — the guard must not
// have locked out legitimate access. DemoUserID owns projects created by
// MustCreateProject, so it is a member here.
func TestDocsReadRoutes_Member_OK(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Mine")
	page := mustCreatePageID(t, srv, pid, "Doc", "body")

	for _, path := range []string{
		"/api/v1/projects/" + pid + "/pages",
		"/api/v1/pages/" + page,
		"/api/v1/pages/" + page + "/revisions",
		"/api/v1/pages/" + page + "/references",
		"/api/v1/projects/" + pid + "/search/pages?q=body",
	} {
		resp := testutil.Do(t, srv, http.MethodGet, path, nil, testutil.DemoUserID)
		testutil.AssertStatus(t, resp, http.StatusOK)
		_ = resp.Body.Close()
	}
}

// A page-scoped read route for a page that does not exist returns 404 (the
// membership resolution can't find a project) rather than leaking via 403.
func TestDocsPageScopedRead_Missing_NotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/pages/"+missingID+"/revisions", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)
	_ = resp.Body.Close()
}
