package workmanagement_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
)

// TestViewer_DeniedOnWrites asserts that a PROJECT_VIEWER is rejected with 403
// on every state-changing endpoint. The viewer (SecondUser) is added to a
// project owned by DemoUser; parent resources are created by the owner so each
// handler reaches its permission check rather than a 404.
func TestViewer_DeniedOnWrites(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	owner := testutil.DemoUserID
	viewer := testutil.SecondUserID

	pid := testutil.MustCreateProject(t, srv, "Perms Project")
	testutil.MustAddMember(t, srv, pid, viewer, "PROJECT_VIEWER")
	taskID := testutil.MustCreateTask(t, srv, pid, "Perms Task")

	// Owner-created parent resources so viewer requests hit the permission check.
	boardID := createAndID(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/boards", pid),
		map[string]string{"name": "Board"}, owner)
	releaseID := createAndID(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/releases", pid),
		map[string]string{"name": "R1"}, owner)
	sprintID := createAndID(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/sprints", pid),
		map[string]string{"name": "S1"}, owner)
	commentID := createAndID(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/tasks/%s/comments", taskID),
		map[string]string{"text": "hi"}, owner)

	type req struct {
		name   string
		method string
		path   string
		body   interface{}
	}
	reqs := []req{
		// Project
		{"update project", http.MethodPatch, "/api/v1/projects/" + pid, map[string]string{"name": "x"}},
		{"archive project", http.MethodPost, "/api/v1/projects/" + pid + "/archive", nil},
		{"delete project", http.MethodDelete, "/api/v1/projects/" + pid, nil},
		// Tasks
		{"create task", http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/tasks", pid), map[string]string{"title": "x"}},
		{"update task", http.MethodPatch, "/api/v1/tasks/" + taskID, map[string]string{"title": "x"}},
		{"assign task", http.MethodPost, "/api/v1/tasks/" + taskID + "/assign", map[string]string{"assigneeId": viewer}},
		{"change status", http.MethodPost, "/api/v1/tasks/" + taskID + "/status", map[string]string{"status": "DONE"}},
		{"change priority", http.MethodPost, "/api/v1/tasks/" + taskID + "/priority", map[string]string{"priority": "HIGH"}},
		{"copy task", http.MethodPost, "/api/v1/tasks/" + taskID + "/copy", nil},
		{"archive task", http.MethodPost, "/api/v1/tasks/" + taskID + "/archive", nil},
		{"reopen task", http.MethodPost, "/api/v1/tasks/" + taskID + "/reopen", nil},
		{"delete task", http.MethodDelete, "/api/v1/tasks/" + taskID, nil},
		{"bulk update", http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/tasks/bulk", pid),
			map[string]interface{}{"taskIds": []string{taskID}, "action": "set_priority", "value": "HIGH"}},
		// Comments
		{"add comment", http.MethodPost, "/api/v1/tasks/" + taskID + "/comments", map[string]string{"text": "x"}},
		{"update comment", http.MethodPatch, fmt.Sprintf("/api/v1/tasks/%s/comments/%s", taskID, commentID), map[string]string{"text": "x"}},
		{"delete comment", http.MethodDelete, fmt.Sprintf("/api/v1/tasks/%s/comments/%s", taskID, commentID), nil},
		// Task sub-resources
		{"add link", http.MethodPost, "/api/v1/tasks/" + taskID + "/links", map[string]string{"url": "https://x", "title": "x"}},
		{"add attachment", http.MethodPost, "/api/v1/tasks/" + taskID + "/attachments", map[string]string{"filename": "f", "url": "https://x"}},
		{"add relation", http.MethodPost, "/api/v1/tasks/" + taskID + "/relations", map[string]string{"targetTaskId": taskID, "relationType": "RELATES_TO"}},
		// Boards
		{"create board", http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/boards", pid), map[string]string{"name": "x"}},
		{"update board", http.MethodPatch, "/api/v1/boards/" + boardID, map[string]string{"name": "x"}},
		{"delete board", http.MethodDelete, "/api/v1/boards/" + boardID, nil},
		{"add column", http.MethodPost, "/api/v1/boards/" + boardID + "/columns", map[string]string{"name": "x"}},
		// Releases
		{"create release", http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/releases", pid), map[string]string{"name": "x"}},
		{"update release", http.MethodPatch, "/api/v1/releases/" + releaseID, map[string]string{"name": "x"}},
		{"close release", http.MethodPost, "/api/v1/releases/" + releaseID + "/close", nil},
		{"reopen release", http.MethodPost, "/api/v1/releases/" + releaseID + "/reopen", nil},
		{"delete release", http.MethodDelete, "/api/v1/releases/" + releaseID, nil},
		// Sprints
		{"create sprint", http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/sprints", pid), map[string]string{"name": "x"}},
		{"update sprint", http.MethodPatch, "/api/v1/sprints/" + sprintID, map[string]string{"name": "x"}},
		{"start sprint", http.MethodPost, "/api/v1/sprints/" + sprintID + "/start", nil},
		{"complete sprint", http.MethodPost, "/api/v1/sprints/" + sprintID + "/complete", nil},
		{"delete sprint", http.MethodDelete, "/api/v1/sprints/" + sprintID, nil},
		// Import
		{"import jira csv", http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/import/jira-csv", pid), map[string]string{"csv": "x"}},
	}

	for _, c := range reqs {
		t.Run(c.name, func(t *testing.T) {
			resp := testutil.Do(t, srv, c.method, c.path, c.body, viewer)
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("%s %s as viewer: status = %d, want 403", c.method, c.path, resp.StatusCode)
			}
		})
	}
}

// createAndID performs an owner-authenticated create and returns the new id.
func createAndID(t *testing.T, srv *httptest.Server, method, path string, body interface{}, userID string) string {
	t.Helper()
	resp := testutil.Do(t, srv, method, path, body, userID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var out map[string]interface{}
	testutil.DecodeJSON(t, resp, &out)
	id, _ := out["id"].(string)
	if id == "" {
		t.Fatalf("create %s returned no id; body: %#v", path, out)
	}
	return id
}
