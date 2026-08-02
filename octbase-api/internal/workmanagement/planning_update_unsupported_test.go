package workmanagement_test

import (
	"net/http"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
)

// PATCH /releases/{id} must reject fields it does not update instead of
// silently accepting them with a 200. The regression this guards: a
// PATCH {"status":"CLOSED"} returned 200 while the release stayed PLANNED —
// and even a bogus {"status":"NOT_A_STATUS"} passed. Status moves only through
// POST /releases/{id}/close and /reopen, so the key is rejected with a hint;
// any other unknown field is rejected too; the supported fields still work.
func TestUpdateRelease_RejectsUnsupportedFields(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	mid := testutil.MustCreateRelease(t, srv, pid, "v1.0")

	// status has dedicated transition routes; the PATCH is rejected with a hint
	// and — critically — the status does not change. A bogus value is equally
	// rejected: the key is unsupported regardless of what it carries.
	for _, val := range []string{"CLOSED", "NOT_A_STATUS"} {
		resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/releases/"+mid,
			map[string]string{"status": val}, testutil.DemoUserID)
		testutil.AssertStatus(t, resp, http.StatusBadRequest)
		var body map[string]any
		testutil.DecodeJSON(t, resp, &body)
		if body["code"] != "UNSUPPORTED_FIELD" {
			t.Fatalf("expected UNSUPPORTED_FIELD for status=%s, got %v", val, body["code"])
		}
	}

	got := testutil.Do(t, srv, http.MethodGet, "/api/v1/releases/"+mid, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, got, http.StatusOK)
	var rel map[string]any
	testutil.DecodeJSON(t, got, &rel)
	if rel["status"] != "PLANNED" {
		t.Fatalf("status must be unchanged after a rejected PATCH; got %v", rel["status"])
	}

	// A wholly unknown field is rejected, not ignored.
	r := testutil.Do(t, srv, http.MethodPatch, "/api/v1/releases/"+mid,
		map[string]string{"nonsense": "x"}, testutil.DemoUserID)
	testutil.AssertStatus(t, r, http.StatusBadRequest)
	_ = r.Body.Close()

	// Supported fields still update normally.
	ok := testutil.Do(t, srv, http.MethodPatch, "/api/v1/releases/"+mid,
		map[string]string{"name": "v2.0", "goal": "Ship version 2"}, testutil.DemoUserID)
	testutil.AssertStatus(t, ok, http.StatusOK)
	var updated map[string]any
	testutil.DecodeJSON(t, ok, &updated)
	if updated["name"] != "v2.0" {
		t.Fatalf("name = %v, want v2.0", updated["name"])
	}
}

// PATCH /sprints/{id} has the same contract: status transitions live on
// POST /sprints/{id}/start and /complete, so a status key is rejected loudly
// instead of vanishing into a 200.
func TestUpdateSprint_RejectsUnsupportedFields(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/sprints",
		map[string]string{"name": "Sprint 1"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var created map[string]any
	testutil.DecodeJSON(t, resp, &created)
	sid := created["id"].(string)

	r := testutil.Do(t, srv, http.MethodPatch, "/api/v1/sprints/"+sid,
		map[string]string{"status": "ACTIVE"}, testutil.DemoUserID)
	testutil.AssertStatus(t, r, http.StatusBadRequest)
	var body map[string]any
	testutil.DecodeJSON(t, r, &body)
	if body["code"] != "UNSUPPORTED_FIELD" {
		t.Fatalf("expected UNSUPPORTED_FIELD, got %v", body["code"])
	}

	got := testutil.Do(t, srv, http.MethodGet, "/api/v1/sprints/"+sid, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, got, http.StatusOK)
	var sp map[string]any
	testutil.DecodeJSON(t, got, &sp)
	if sp["status"] != "PLANNED" {
		t.Fatalf("status must be unchanged after a rejected PATCH; got %v", sp["status"])
	}

	// A wholly unknown field is rejected; supported fields still work.
	r = testutil.Do(t, srv, http.MethodPatch, "/api/v1/sprints/"+sid,
		map[string]string{"nonsense": "x"}, testutil.DemoUserID)
	testutil.AssertStatus(t, r, http.StatusBadRequest)
	_ = r.Body.Close()

	ok := testutil.Do(t, srv, http.MethodPatch, "/api/v1/sprints/"+sid,
		map[string]string{"name": "Sprint 1 — Hardening", "goal": "Clear the backlog"}, testutil.DemoUserID)
	testutil.AssertStatus(t, ok, http.StatusOK)
	var updated map[string]any
	testutil.DecodeJSON(t, ok, &updated)
	if updated["name"] != "Sprint 1 — Hardening" {
		t.Fatalf("name = %v", updated["name"])
	}
}
