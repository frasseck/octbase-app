package workmanagement_test

import (
	"net/http"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
)

// PATCH /tasks/{id} must reject fields it does not update instead of silently
// accepting them with a 200. The regression this guards: a batch of
// PATCH {"status":"DONE"} calls returned 200 while every task stayed PLANNED,
// because status has a dedicated transition endpoint and the unknown key was
// dropped. Status/priority/assignee are rejected with a hint to their endpoint;
// any other unknown field is rejected too; the supported fields still work.
func TestUpdateTask_RejectsUnsupportedFields(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	// A field with its own transition endpoint is rejected with 400 + a hint,
	// and — critically — the status does not change.
	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/tasks/"+tid,
		map[string]string{"status": "DONE"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusBadRequest)
	var body map[string]any
	testutil.DecodeJSON(t, resp, &body)
	if body["code"] != "UNSUPPORTED_FIELD" {
		t.Fatalf("expected UNSUPPORTED_FIELD, got %v", body["code"])
	}

	got := testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+tid, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, got, http.StatusOK)
	var task map[string]any
	testutil.DecodeJSON(t, got, &task)
	if task["status"] != "PLANNED" {
		t.Fatalf("status must be unchanged after a rejected PATCH; got %v", task["status"])
	}

	// priority and assigneeId likewise have dedicated endpoints.
	for _, key := range []string{"priority", "assigneeId"} {
		r := testutil.Do(t, srv, http.MethodPatch, "/api/v1/tasks/"+tid,
			map[string]string{key: "x"}, testutil.DemoUserID)
		testutil.AssertStatus(t, r, http.StatusBadRequest)
		_ = r.Body.Close()
	}

	// A wholly unknown field is rejected, not ignored.
	r := testutil.Do(t, srv, http.MethodPatch, "/api/v1/tasks/"+tid,
		map[string]string{"nonsense": "x"}, testutil.DemoUserID)
	testutil.AssertStatus(t, r, http.StatusBadRequest)
	_ = r.Body.Close()

	// A supported field still updates normally.
	ok := testutil.Do(t, srv, http.MethodPatch, "/api/v1/tasks/"+tid,
		map[string]string{"title": "Renamed"}, testutil.DemoUserID)
	testutil.AssertStatus(t, ok, http.StatusOK)
	_ = ok.Body.Close()
}
