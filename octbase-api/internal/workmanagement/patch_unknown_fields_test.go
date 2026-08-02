package workmanagement_test

import (
	"net/http"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
)

// Every PATCH handler must reject fields it does not update instead of
// silently accepting them with a 200 (the class UpdateTask/UpdateRelease/
// UpdateSprint closed piecemeal, now centralized in shared.DecodePatch).
// The reproduced defect that motivated this sweep: PATCH /projects/{id}
// {"status":"ARCHIVED"} answered 200, bumped the version, and changed nothing.

func TestUpdateProject_RejectsUnsupportedFields(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	// status has a dedicated route (POST /projects/{id}/archive); the PATCH is
	// rejected with a hint and the project is untouched — including its version.
	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/projects/"+pid,
		map[string]string{"status": "ARCHIVED"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusBadRequest)
	var body map[string]any
	testutil.DecodeJSON(t, resp, &body)
	if body["code"] != "UNSUPPORTED_FIELD" {
		t.Fatalf("expected UNSUPPORTED_FIELD, got %v", body["code"])
	}

	got := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, got, http.StatusOK)
	var p map[string]any
	testutil.DecodeJSON(t, got, &p)
	if p["status"] != "ACTIVE" {
		t.Fatalf("status must be unchanged after a rejected PATCH; got %v", p["status"])
	}
	if p["version"] != float64(1) {
		t.Fatalf("version must not bump on a rejected PATCH; got %v", p["version"])
	}

	// A wholly unknown field is rejected, not ignored.
	r := testutil.Do(t, srv, http.MethodPatch, "/api/v1/projects/"+pid,
		map[string]string{"nonsense": "x"}, testutil.DemoUserID)
	testutil.AssertStatus(t, r, http.StatusBadRequest)
	_ = r.Body.Close()

	// Supported fields still update normally.
	ok := testutil.Do(t, srv, http.MethodPatch, "/api/v1/projects/"+pid,
		map[string]string{"description": "updated"}, testutil.DemoUserID)
	testutil.AssertStatus(t, ok, http.StatusOK)
	var updated map[string]any
	testutil.DecodeJSON(t, ok, &updated)
	if updated["description"] != "updated" {
		t.Fatalf("description = %v", updated["description"])
	}
}

// The remaining PATCH endpoints in this context share the same contract; one
// sweep asserts each rejects an unknown key loudly.
func TestPatchEndpoints_RejectUnknownFields(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")
	bid := testutil.MustCreateBoard(t, srv, pid)
	cid := testutil.MustAddColumn(t, srv, bid, "Lane", "PLANNED", 0)

	catResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/task-categories",
		map[string]string{"name": "Cat"}, testutil.DemoUserID)
	testutil.AssertStatus(t, catResp, http.StatusCreated)
	var cat map[string]any
	testutil.DecodeJSON(t, catResp, &cat)

	tplResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/task-templates",
		map[string]string{"name": "Tpl", "titleTemplate": "T: {x}"}, testutil.DemoUserID)
	testutil.AssertStatus(t, tplResp, http.StatusCreated)
	var tpl map[string]any
	testutil.DecodeJSON(t, tplResp, &tpl)

	comResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/comments",
		map[string]string{"text": "hello"}, testutil.DemoUserID)
	testutil.AssertStatus(t, comResp, http.StatusCreated)
	var com map[string]any
	testutil.DecodeJSON(t, comResp, &com)

	endpoints := []struct {
		name string
		path string
	}{
		{"category", "/api/v1/task-categories/" + cat["id"].(string)},
		{"template", "/api/v1/task-templates/" + tpl["id"].(string)},
		{"board", "/api/v1/boards/" + bid},
		{"column", "/api/v1/boards/" + bid + "/columns/" + cid},
		{"comment", "/api/v1/tasks/" + tid + "/comments/" + com["id"].(string)},
	}
	for _, ep := range endpoints {
		t.Run(ep.name, func(t *testing.T) {
			resp := testutil.Do(t, srv, http.MethodPatch, ep.path,
				map[string]string{"nonsense": "x"}, testutil.DemoUserID)
			testutil.AssertStatus(t, resp, http.StatusBadRequest)
			var body map[string]any
			testutil.DecodeJSON(t, resp, &body)
			if body["code"] != "UNSUPPORTED_FIELD" {
				t.Fatalf("%s: expected UNSUPPORTED_FIELD, got %v", ep.name, body["code"])
			}
		})
	}
}
