package workmanagement_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
)

// Creating a task with a field this route does not model must fail loudly.
// Structs are the contract, so a client sees pinned/reviewerId/status/
// boardColumnId on every task it reads and sends them back on create; each was
// silently dropped while the 201 said the task had been created. UpdateTask has
// rejected unknown keys for a long time — this is the same rule on the other
// half of the same resource.
func TestCreateTask_UnsupportedField_BadRequest(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	cases := []struct {
		name string
		body map[string]interface{}
		// wantMentions is a substring the message must carry, so a field with a
		// dedicated route points at it instead of reading like a typo.
		wantMentions string
	}{
		{"status", map[string]interface{}{"title": "T", "status": "DONE"}, "/status"},
		{"reviewerId", map[string]interface{}{"title": "T", "reviewerId": testutil.DemoUserID}, "/assign"},
		{"pinned", map[string]interface{}{"title": "T", "pinned": true}, "/pin"},
		{"boardColumnId", map[string]interface{}{"title": "T", "boardColumnId": "x"}, "move-task"},
		{"boardRank", map[string]interface{}{"title": "T", "boardRank": 5}, "move-task"},
		{"version", map[string]interface{}{"title": "T", "version": 7}, "server"},
		{"reporterId", map[string]interface{}{"title": "T", "reporterId": testutil.DemoUserID}, "caller"},
		{"typo", map[string]interface{}{"title": "T", "titel": "oops"}, "titel"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/tasks",
				tc.body, testutil.DemoUserID)
			testutil.AssertStatus(t, resp, http.StatusBadRequest)
			var e map[string]interface{}
			testutil.DecodeJSON(t, resp, &e)
			if e["code"] != "UNSUPPORTED_FIELD" {
				t.Fatalf("code = %v, want UNSUPPORTED_FIELD", e["code"])
			}
			msg, _ := e["message"].(string)
			if !strings.Contains(msg, tc.wantMentions) {
				t.Errorf("message %q should mention %q", msg, tc.wantMentions)
			}
		})
	}

	// Nothing was created by any of the rejected requests.
	list := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/tasks", nil, testutil.DemoUserID)
	var tasks []map[string]interface{}
	testutil.DecodeJSON(t, list, &tasks)
	if len(tasks) != 0 {
		t.Errorf("project has %d tasks; a rejected create must not persist", len(tasks))
	}
}

// The fields create *does* model must all still be accepted together, so the
// allowlist cannot drift away from the request struct without a test failing.
func TestCreateTask_AllModelledFieldsAccepted(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	// storyPoints needs the project to have estimation switched on.
	patch := testutil.Do(t, srv, http.MethodPatch, "/api/v1/projects/"+pid,
		map[string]interface{}{"estimationUnit": "POINTS"}, testutil.DemoUserID)
	testutil.AssertStatus(t, patch, http.StatusOK)

	parent := testutil.MustCreateTask(t, srv, pid, "parent")
	// A TASK's parent must be a STORY.
	up := testutil.Do(t, srv, http.MethodPatch, "/api/v1/tasks/"+parent,
		map[string]interface{}{"taskType": "STORY"}, testutil.DemoUserID)
	testutil.AssertStatus(t, up, http.StatusOK)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/tasks",
		map[string]interface{}{
			"title": "full", "description": "<p>d</p>", "taskType": "TASK",
			"priority": "HIGH", "parentId": parent, "assigneeId": testutil.DemoUserID,
			"dueDate": "2026-12-31", "storyPoints": 3,
		}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var task map[string]interface{}
	testutil.DecodeJSON(t, resp, &task)
	if task["title"] != "full" || task["priority"] != "HIGH" {
		t.Errorf("create dropped a modelled field: %v", task)
	}
	if sp, _ := task["storyPoints"].(float64); sp != 3 {
		t.Errorf("storyPoints = %v, want 3", task["storyPoints"])
	}
}
