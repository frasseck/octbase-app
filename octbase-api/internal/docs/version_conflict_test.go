package docs_test

import (
	"net/http"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
)

// Page PATCH supports optimistic locking like the workmanagement entities:
// every successful update increments the page's version, and an edit carrying
// a stale "version" is rejected with 409 VERSION_CONFLICT instead of silently
// overwriting a concurrent editor's changes.
func TestUpdatePage_VersionConflict(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	pageID := mustCreatePageID(t, srv, pid, "Doc", "= Doc\n\noriginal")

	// First edit on version 1 succeeds and bumps the version in the response.
	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/pages/"+pageID,
		map[string]interface{}{"content": "= Doc\n\nedit A", "version": 1}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var page map[string]interface{}
	testutil.DecodeJSON(t, resp, &page)
	if v, _ := page["version"].(float64); v != 2 {
		t.Fatalf("version after update = %v, want 2", page["version"])
	}

	// A second edit still based on version 1 is rejected and must not win.
	resp = testutil.Do(t, srv, http.MethodPatch, "/api/v1/pages/"+pageID,
		map[string]interface{}{"content": "= Doc\n\nstale edit B", "version": 1}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusConflict)
	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "VERSION_CONFLICT" {
		t.Errorf("code = %v, want VERSION_CONFLICT", e["code"])
	}

	get := testutil.Do(t, srv, http.MethodGet, "/api/v1/pages/"+pageID, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, get, http.StatusOK)
	testutil.DecodeJSON(t, get, &page)
	if got, _ := page["content"].(string); got != "= Doc\n\nedit A" {
		t.Errorf("content = %q, want edit A (stale write must not win)", got)
	}
}

func TestUpdatePage_WithoutVersion_StillSucceeds(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	pageID := mustCreatePageID(t, srv, pid, "Doc", "= Doc\n\noriginal")

	for _, content := range []string{"= Doc\n\nA", "= Doc\n\nB"} {
		resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/pages/"+pageID,
			map[string]string{"content": content}, testutil.DemoUserID)
		testutil.AssertStatus(t, resp, http.StatusOK)
	}
	get := testutil.Do(t, srv, http.MethodGet, "/api/v1/pages/"+pageID, nil, testutil.DemoUserID)
	var page map[string]interface{}
	testutil.DecodeJSON(t, get, &page)
	if v, _ := page["version"].(float64); v != 3 {
		t.Errorf("version = %v, want 3 after two updates", page["version"])
	}
}
