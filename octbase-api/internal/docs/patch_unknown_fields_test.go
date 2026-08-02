package docs_test

import (
	"net/http"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
)

// PATCH /pages/{id} must reject fields it does not update instead of silently
// accepting them with a 200. The reproduced defect: {"status":"ARCHIVED"}
// answered 200, bumped the version, and left the page DRAFT — status moves
// only through POST /pages/{id}/publish and /archive.
func TestUpdatePage_RejectsUnsupportedFields(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/pages",
		map[string]string{"title": "T", "content": "c"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var page map[string]any
	testutil.DecodeJSON(t, resp, &page)
	pageID := page["id"].(string)

	r := testutil.Do(t, srv, http.MethodPatch, "/api/v1/pages/"+pageID,
		map[string]string{"status": "ARCHIVED"}, testutil.DemoUserID)
	testutil.AssertStatus(t, r, http.StatusBadRequest)
	var body map[string]any
	testutil.DecodeJSON(t, r, &body)
	if body["code"] != "UNSUPPORTED_FIELD" {
		t.Fatalf("expected UNSUPPORTED_FIELD, got %v", body["code"])
	}

	got := testutil.Do(t, srv, http.MethodGet, "/api/v1/pages/"+pageID, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, got, http.StatusOK)
	var p map[string]any
	testutil.DecodeJSON(t, got, &p)
	if p["status"] != "DRAFT" {
		t.Fatalf("status must be unchanged after a rejected PATCH; got %v", p["status"])
	}
	if p["version"] != float64(1) {
		t.Fatalf("version must not bump on a rejected PATCH; got %v", p["version"])
	}

	// A wholly unknown field is rejected; supported fields still work.
	r = testutil.Do(t, srv, http.MethodPatch, "/api/v1/pages/"+pageID,
		map[string]string{"nonsense": "x"}, testutil.DemoUserID)
	testutil.AssertStatus(t, r, http.StatusBadRequest)
	_ = r.Body.Close()

	ok := testutil.Do(t, srv, http.MethodPatch, "/api/v1/pages/"+pageID,
		map[string]string{"title": "T2"}, testutil.DemoUserID)
	testutil.AssertStatus(t, ok, http.StatusOK)
	var updated map[string]any
	testutil.DecodeJSON(t, ok, &updated)
	if updated["title"] != "T2" {
		t.Fatalf("title = %v", updated["title"])
	}
}
