package workmanagement_test

import (
	"net/http"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
)

// A PATCH carrying a field of the wrong JSON type must be rejected with 400,
// not silently ignored: answering 200 while dropping the field would tell the
// client its edit was saved when it wasn't.
func TestUpdateTask_WrongFieldType_BadRequest(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "Original")

	cases := []map[string]interface{}{
		{"title": 123},
		{"description": true},
		{"taskType": []string{"STORY"}},
		{"dueDate": 20260101},
		{"releaseId": 42},
	}
	for _, body := range cases {
		resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/tasks/"+tid, body, testutil.DemoUserID)
		testutil.AssertStatus(t, resp, http.StatusBadRequest)
		var e map[string]interface{}
		testutil.DecodeJSON(t, resp, &e)
		if e["code"] != "BAD_REQUEST" {
			t.Errorf("body %v: code = %v, want BAD_REQUEST", body, e["code"])
		}
	}

	// The task is unchanged after all rejected writes.
	get := testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+tid, nil, testutil.DemoUserID)
	var task map[string]interface{}
	testutil.DecodeJSON(t, get, &task)
	if task["title"] != "Original" {
		t.Errorf("title = %v, want Original", task["title"])
	}
	if v, _ := task["version"].(float64); v != 1 {
		t.Errorf("version = %v, want 1 (no write must have happened)", task["version"])
	}
}
