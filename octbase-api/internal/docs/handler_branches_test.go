package docs_test

import (
	"net/http"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
)

const missingID = "00000000-0000-0000-0000-0000000000ff"

func TestCreatePage_Viewer_Forbidden(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_VIEWER")
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/pages",
		map[string]string{"title": "T", "content": "c"}, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
	_ = resp.Body.Close()
}

func TestCreatePage_BadJSON(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/pages",
		"not-an-object", testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusBadRequest)
	_ = resp.Body.Close()
}

func TestUpdatePage_NotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/pages/"+missingID,
		map[string]string{"title": "X"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)
	_ = resp.Body.Close()
}

func TestUpdatePage_Viewer_Forbidden(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	page := mustCreatePageID(t, srv, pid, "Doc", "body")
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_VIEWER")
	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/pages/"+page,
		map[string]string{"title": "X"}, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
	_ = resp.Body.Close()
}

func TestUpdatePage_BadJSON(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	page := mustCreatePageID(t, srv, pid, "Doc", "body")
	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/pages/"+page,
		"not-an-object", testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusBadRequest)
	_ = resp.Body.Close()
}

func TestUpdatePage_SlugConflict(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	mustCreatePageID(t, srv, pid, "First Page", "a")
	page2 := mustCreatePageID(t, srv, pid, "Second Page", "b")
	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/pages/"+page2,
		map[string]string{"slug": "first-page"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusConflict)
	_ = resp.Body.Close()
}

func TestUpdatePage_SlugChange_OK(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	page := mustCreatePageID(t, srv, pid, "Doc", "body")
	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/pages/"+page,
		map[string]string{"slug": "renamed-slug"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var p map[string]interface{}
	testutil.DecodeJSON(t, resp, &p)
	if p["slug"] != "renamed-slug" {
		t.Errorf("slug = %v, want renamed-slug", p["slug"])
	}
}

func TestRenderPreview_BadJSON(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	page := mustCreatePageID(t, srv, pid, "Doc", "body")
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/pages/"+page+"/render-preview",
		"not-an-object", testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusBadRequest)
	_ = resp.Body.Close()
}

func TestPublishPage_NotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/pages/"+missingID+"/publish",
		map[string]string{"message": "m"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)
	_ = resp.Body.Close()
}

func TestPublishPage_Viewer_Forbidden(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	page := mustCreatePageID(t, srv, pid, "Doc", "body")
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_VIEWER")
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/pages/"+page+"/publish",
		map[string]string{"message": "m"}, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
	_ = resp.Body.Close()
}

func TestArchivePage_NotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/pages/"+missingID+"/archive",
		nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)
	_ = resp.Body.Close()
}

func TestArchivePage_Viewer_Forbidden(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	page := mustCreatePageID(t, srv, pid, "Doc", "body")
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_VIEWER")
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/pages/"+page+"/archive",
		nil, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
	_ = resp.Body.Close()
}

func TestRebuildReferences_NotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/pages/"+missingID+"/references/rebuild",
		nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)
	_ = resp.Body.Close()
}

func TestRebuildReferences_Viewer_Forbidden(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	page := mustCreatePageID(t, srv, pid, "Doc", "body")
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_VIEWER")
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/pages/"+page+"/references/rebuild",
		nil, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
	_ = resp.Body.Close()
}

func TestDeletePage_Viewer_Forbidden(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	page := mustCreatePageID(t, srv, pid, "Doc", "body")
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_VIEWER")
	resp := testutil.Do(t, srv, http.MethodDelete, "/api/v1/pages/"+page, nil, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
	_ = resp.Body.Close()
}
