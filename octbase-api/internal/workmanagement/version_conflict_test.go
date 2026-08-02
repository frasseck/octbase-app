package workmanagement_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
)

// The PATCH endpoints support optimistic locking: every successful update
// increments the entity's version, and a request carrying a stale "version"
// is rejected with 409 VERSION_CONFLICT instead of silently overwriting a
// concurrent editor's changes.

// patchStale asserts that a PATCH with the given stale-version body is
// rejected with 409 VERSION_CONFLICT.
func patchStale(t *testing.T, srv *httptest.Server, path string, body map[string]interface{}) {
	t.Helper()
	resp := testutil.Do(t, srv, http.MethodPatch, path, body, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusConflict)
	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "VERSION_CONFLICT" {
		t.Errorf("code = %v, want VERSION_CONFLICT", e["code"])
	}
}

func TestUpdateTask_VersionConflict(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "Original")

	// First edit on version 1 succeeds and bumps the version in the response.
	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/tasks/"+tid,
		map[string]interface{}{"title": "Edit A", "version": 1}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var task map[string]interface{}
	testutil.DecodeJSON(t, resp, &task)
	if v, _ := task["version"].(float64); v != 2 {
		t.Fatalf("version after update = %v, want 2", task["version"])
	}

	// A second edit still based on version 1 (a concurrent editor's stale
	// snapshot) is rejected and must not overwrite Edit A.
	patchStale(t, srv, "/api/v1/tasks/"+tid, map[string]interface{}{"title": "Edit B", "version": 1})

	get := testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+tid, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, get, http.StatusOK)
	testutil.DecodeJSON(t, get, &task)
	if task["title"] != "Edit A" {
		t.Errorf("title = %v, want 'Edit A' (stale write must not win)", task["title"])
	}
}

func TestUpdateTask_WithoutVersion_StillSucceeds(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "Original")

	// Clients that do not send a version keep last-write-wins semantics across
	// requests (each request is guarded only against a concurrent write between
	// its own read and write).
	for _, title := range []string{"Edit A", "Edit B"} {
		resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/tasks/"+tid,
			map[string]string{"title": title}, testutil.DemoUserID)
		testutil.AssertStatus(t, resp, http.StatusOK)
	}
	get := testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+tid, nil, testutil.DemoUserID)
	var task map[string]interface{}
	testutil.DecodeJSON(t, get, &task)
	if task["title"] != "Edit B" {
		t.Errorf("title = %v, want 'Edit B'", task["title"])
	}
	if v, _ := task["version"].(float64); v != 3 {
		t.Errorf("version = %v, want 3 after two updates", task["version"])
	}
}

func TestUpdateTask_NonIntegerVersion_BadRequest(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "Original")

	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/tasks/"+tid,
		map[string]interface{}{"title": "X", "version": "not-a-number"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusBadRequest)
}

func TestUpdateProject_VersionConflict(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/projects/"+pid,
		map[string]interface{}{"name": "Renamed", "version": 1}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var p map[string]interface{}
	testutil.DecodeJSON(t, resp, &p)
	if v, _ := p["version"].(float64); v != 2 {
		t.Fatalf("version after update = %v, want 2", p["version"])
	}

	patchStale(t, srv, "/api/v1/projects/"+pid, map[string]interface{}{"name": "Stale", "version": 1})
}

func TestUpdateRelease_VersionConflict(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	rid := testutil.MustCreateRelease(t, srv, pid, "R1")

	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/releases/"+rid,
		map[string]interface{}{"goal": "Ship it", "version": 1}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	patchStale(t, srv, "/api/v1/releases/"+rid, map[string]interface{}{"goal": "Stale", "version": 1})
}

func TestUpdateSprint_VersionConflict(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	create := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/sprints",
		map[string]string{"name": "Sprint 1"}, testutil.DemoUserID)
	testutil.AssertStatus(t, create, http.StatusCreated)
	var sp map[string]interface{}
	testutil.DecodeJSON(t, create, &sp)
	sid, _ := sp["id"].(string)

	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/sprints/"+sid,
		map[string]interface{}{"goal": "Focus", "version": 1}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	patchStale(t, srv, "/api/v1/sprints/"+sid, map[string]interface{}{"goal": "Stale", "version": 1})
}
