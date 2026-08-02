package workmanagement_test

import (
	"net/http"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
)

// TestArchivedProject_WriterMutationsRejected pins the archived-project
// contract: archived projects are uniformly read-only. Every writer-level
// content mutation answers 409 PROJECT_ARCHIVED — the same status the
// requirePermission-gated paths (board create, task delete) always returned —
// while reads keep working and owner-level project administration (archive,
// delete) stays available.
func TestArchivedProject_WriterMutationsRejected(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Frozen")
	tid := testutil.MustCreateTask(t, srv, pid, "Task")

	// Fixtures that must exist before the freeze: a category, a template, a
	// repository connection and a branch (for the pull-request attempt).
	catResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/task-categories",
		map[string]string{"name": "Cat"}, testutil.DemoUserID)
	testutil.AssertStatus(t, catResp, http.StatusCreated)
	var cat map[string]interface{}
	testutil.DecodeJSON(t, catResp, &cat)
	catID := cat["id"].(string)

	tmplResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/task-templates",
		map[string]string{"name": "Tmpl", "taskType": "TASK", "priority": "HIGH"}, testutil.DemoUserID)
	testutil.AssertStatus(t, tmplResp, http.StatusCreated)
	var tmpl map[string]interface{}
	testutil.DecodeJSON(t, tmplResp, &tmpl)
	tmplID := tmpl["id"].(string)

	rcResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/repository-connections",
		map[string]string{"displayName": "Repo", "repositoryUrl": "https://example.com/repo"}, testutil.DemoUserID)
	testutil.AssertStatus(t, rcResp, http.StatusCreated)
	var rc map[string]interface{}
	testutil.DecodeJSON(t, rcResp, &rc)
	rcID := rc["id"].(string)

	brResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/branches",
		map[string]string{"repositoryId": rcID, "branchName": "feature/frozen"}, testutil.DemoUserID)
	testutil.AssertStatus(t, brResp, http.StatusCreated)
	var br map[string]interface{}
	testutil.DecodeJSON(t, brResp, &br)
	brID := br["id"].(string)

	archResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/archive", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, archResp, http.StatusOK)

	cases := []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{"CreateBranch", http.MethodPost, "/api/v1/tasks/" + tid + "/branches",
			map[string]string{"repositoryId": rcID, "branchName": "feature/late"}},
		{"CreatePullRequest", http.MethodPost, "/api/v1/tasks/" + tid + "/branches/" + brID + "/pull-request",
			map[string]string{}},
		{"DeleteBranch", http.MethodDelete, "/api/v1/tasks/" + tid + "/branches/" + brID, nil},
		{"UpdateCategory", http.MethodPatch, "/api/v1/task-categories/" + catID,
			map[string]string{"name": "Renamed"}},
		{"DeleteCategory", http.MethodDelete, "/api/v1/task-categories/" + catID, nil},
		{"UpdateTemplate", http.MethodPatch, "/api/v1/task-templates/" + tmplID,
			map[string]string{"name": "Renamed"}},
		{"DeleteTemplate", http.MethodDelete, "/api/v1/task-templates/" + tmplID, nil},
		{"CreateTask", http.MethodPost, "/api/v1/projects/" + pid + "/tasks",
			map[string]string{"title": "Late task"}},
		{"UpdateTask", http.MethodPatch, "/api/v1/tasks/" + tid,
			map[string]string{"title": "Renamed"}},
		{"AddComment", http.MethodPost, "/api/v1/tasks/" + tid + "/comments",
			map[string]string{"content": "hi"}},
		{"CreateSprint", http.MethodPost, "/api/v1/projects/" + pid + "/sprints",
			map[string]string{"name": "S1"}},
		{"CreateRelease", http.MethodPost, "/api/v1/projects/" + pid + "/releases",
			map[string]string{"name": "R1"}},
		{"CreatePage", http.MethodPost, "/api/v1/projects/" + pid + "/pages",
			map[string]string{"title": "P1", "content": "= P1"}},
		{"UpdateProject", http.MethodPatch, "/api/v1/projects/" + pid,
			map[string]string{"name": "Renamed"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := testutil.Do(t, srv, tc.method, tc.path, tc.body, testutil.DemoUserID)
			testutil.AssertStatus(t, resp, http.StatusConflict)
			var out map[string]interface{}
			testutil.DecodeJSON(t, resp, &out)
			if out["code"] != "PROJECT_ARCHIVED" {
				t.Errorf("code = %v, want PROJECT_ARCHIVED", out["code"])
			}
		})
	}

	// Reads are unaffected.
	for _, path := range []string{
		"/api/v1/tasks/" + tid,
		"/api/v1/projects/" + pid + "/task-categories",
		"/api/v1/projects/" + pid + "/pages",
		"/api/v1/tasks/" + tid + "/branches",
	} {
		r := testutil.Do(t, srv, http.MethodGet, path, nil, testutil.DemoUserID)
		testutil.AssertStatus(t, r, http.StatusOK)
		_ = r.Body.Close()
	}

	// Owner-level project administration stays available: deleting the
	// archived project still works.
	delResp := testutil.Do(t, srv, http.MethodDelete, "/api/v1/projects/"+pid, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, delResp, http.StatusNoContent)
}

// TestUnarchiveProject_RestoresWrites is the other half of the contract above:
// archiving must not be a one-way door. Every write route answers 409
// PROJECT_ARCHIVED once a project is archived — including PATCH /projects/{id}
// — so without an unarchive route the only way back would have been the
// database.
func TestUnarchiveProject_RestoresWrites(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	pid := testutil.MustCreateProject(t, srv, "Unarchive Me")
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/archive", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()

	// Precondition: the project really is frozen.
	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/tasks",
		map[string]any{"title": "nope"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusConflict)
	_ = resp.Body.Close()

	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/unarchive", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var project map[string]any
	testutil.DecodeJSON(t, resp, &project)
	if project["status"] != "ACTIVE" {
		t.Fatalf("status after unarchive = %v, want ACTIVE", project["status"])
	}

	// And writing works again.
	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/tasks",
		map[string]any{"title": "back in business"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	_ = resp.Body.Close()

	// Idempotent: unarchiving an active project is a no-op, not an error.
	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/unarchive", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()
}

// TestUnarchiveProject_OwnerOnly mirrors the archive route's guard: a plain
// member must not be able to bring a project back.
func TestUnarchiveProject_OwnerOnly(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	pid := testutil.MustCreateProject(t, srv, "Owner Only Unarchive")
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_MEMBER")
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/archive", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()

	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/unarchive", nil, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
	_ = resp.Body.Close()

	// A non-member gets nothing either — not even the project's existence.
	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/unarchive", nil, testutil.GuestUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
	_ = resp.Body.Close()
}
