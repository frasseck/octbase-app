package workmanagement_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
)

// patchProject PATCHes a project as the given actor and returns status +
// error code + body.
func patchProject(t *testing.T, srv *httptest.Server, pid string, body map[string]interface{}, actorID string) (int, string, map[string]interface{}) {
	t.Helper()
	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/projects/"+pid, body, actorID)
	var out map[string]interface{}
	testutil.DecodeJSON(t, resp, &out)
	if resp.StatusCode == http.StatusOK {
		return resp.StatusCode, "", out
	}
	code, _ := out["code"].(string)
	return resp.StatusCode, code, out
}

// enableLevels switches the THEME/INITIATIVE hierarchy levels on or off and
// fails the test unless the PATCH succeeds.
func enableLevels(t *testing.T, srv *httptest.Server, pid string, theme, initiative bool) {
	t.Helper()
	status, code, _ := patchProject(t, srv, pid,
		map[string]interface{}{"themeEnabled": theme, "initiativeEnabled": initiative}, testutil.DemoUserID)
	if status != http.StatusOK {
		t.Fatalf("toggle levels: status %d, code %s", status, code)
	}
}

func TestProjectSettings_DefaultOffAndDisabledTypeRejected(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Settings")

	// Fresh projects expose the flags switched off.
	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid, nil, testutil.DemoUserID)
	var p map[string]interface{}
	testutil.DecodeJSON(t, resp, &p)
	if p["themeEnabled"] != false || p["initiativeEnabled"] != false {
		t.Fatalf("fresh project flags = %v/%v, want false/false", p["themeEnabled"], p["initiativeEnabled"])
	}

	// THEME/INITIATIVE tasks are rejected while the levels are off.
	for _, tt := range []string{"THEME", "INITIATIVE"} {
		status, code, _ := postTask(t, srv, pid, tt+" early", tt, "")
		if status != http.StatusUnprocessableEntity || code != "TASK_TYPE_DISABLED" {
			t.Errorf("create %s while disabled: status %d code %s, want 422 TASK_TYPE_DISABLED", tt, status, code)
		}
	}
}

func TestProjectSettings_ExtendedHierarchy(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Hierarchy6")
	enableLevels(t, srv, pid, true, true)

	theme := mustPostTask(t, srv, pid, "Theme", "THEME", "")
	initiative := mustPostTask(t, srv, pid, "Initiative", "INITIATIVE", theme["id"].(string))
	epic := mustPostTask(t, srv, pid, "Epic", "EPIC", initiative["id"].(string))
	mustPostTask(t, srv, pid, "Story", "STORY", epic["id"].(string))

	// With both levels on, a THEME is the top: no parent allowed.
	status, code, _ := postTask(t, srv, pid, "Nested theme", "THEME", theme["id"].(string))
	if status != http.StatusUnprocessableEntity || code != "TASK_PARENT_NOT_ALLOWED" {
		t.Errorf("theme with parent: status %d code %s, want 422 TASK_PARENT_NOT_ALLOWED", status, code)
	}
	// An EPIC's parent must now be an INITIATIVE, not a THEME.
	status, code, _ = postTask(t, srv, pid, "Epic under theme", "EPIC", theme["id"].(string))
	if status != http.StatusUnprocessableEntity || code != "TASK_PARENT_TYPE_INVALID" {
		t.Errorf("epic under theme: status %d code %s, want 422 TASK_PARENT_TYPE_INVALID", status, code)
	}
}

func TestProjectSettings_ThemeOnlyChainSkipsInitiative(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "ThemeOnly")
	enableLevels(t, srv, pid, true, false)

	theme := mustPostTask(t, srv, pid, "Theme", "THEME", "")
	// With INITIATIVE off the level below THEME is EPIC.
	mustPostTask(t, srv, pid, "Epic", "EPIC", theme["id"].(string))
	status, code, _ := postTask(t, srv, pid, "Initiative", "INITIATIVE", theme["id"].(string))
	if status != http.StatusUnprocessableEntity || code != "TASK_TYPE_DISABLED" {
		t.Errorf("initiative while disabled: status %d code %s, want 422 TASK_TYPE_DISABLED", status, code)
	}
}

func TestProjectSettings_ToggleGuards(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Guards")
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_MEMBER")

	// Members below admin cannot touch the settings.
	status, _, _ := patchProject(t, srv, pid, map[string]interface{}{"themeEnabled": true}, testutil.SecondUserID)
	if status != http.StatusForbidden {
		t.Fatalf("member toggling settings: status %d, want 403", status)
	}

	// Disabling a level with tasks of that type still on it is blocked.
	enableLevels(t, srv, pid, true, false)
	mustPostTask(t, srv, pid, "Theme in use", "THEME", "")
	status, code, _ := patchProject(t, srv, pid, map[string]interface{}{"themeEnabled": false}, testutil.DemoUserID)
	if status != http.StatusUnprocessableEntity || code != "TASK_TYPE_IN_USE" {
		t.Fatalf("disable in-use level: status %d code %s, want 422 TASK_TYPE_IN_USE", status, code)
	}
}

// postPriority adds a custom priority as the given actor.
func postPriority(t *testing.T, srv *httptest.Server, pid, name, actorID string) (int, string, map[string]interface{}) {
	t.Helper()
	resp := testutil.Do(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/task-priorities", pid),
		map[string]interface{}{"name": name}, actorID)
	var out map[string]interface{}
	testutil.DecodeJSON(t, resp, &out)
	if resp.StatusCode == http.StatusCreated {
		return resp.StatusCode, "", out
	}
	code, _ := out["code"].(string)
	return resp.StatusCode, code, out
}

func TestProjectPriorities_CRUDAndValidation(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Prio")
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_MEMBER")

	// Only project admins may add priorities.
	if status, _, _ := postPriority(t, srv, pid, "URGENT", testutil.SecondUserID); status != http.StatusForbidden {
		t.Fatalf("member creating priority: status %d, want 403", status)
	}

	// Names are normalized and validated; built-ins are reserved.
	status, _, created := postPriority(t, srv, pid, "  urgent ", testutil.DemoUserID)
	if status != http.StatusCreated || created["name"] != "URGENT" {
		t.Fatalf("create priority: status %d name %v, want 201 URGENT", status, created["name"])
	}
	if status, code, _ := postPriority(t, srv, pid, "URGENT", testutil.DemoUserID); status != http.StatusConflict || code != "PRIORITY_EXISTS" {
		t.Errorf("duplicate priority: status %d code %s, want 409 PRIORITY_EXISTS", status, code)
	}
	for _, builtin := range []string{"HIGH", "BLOCKER"} {
		if status, code, _ := postPriority(t, srv, pid, builtin, testutil.DemoUserID); status != http.StatusUnprocessableEntity || code != "PRIORITY_RESERVED" {
			t.Errorf("built-in name %s: status %d code %s, want 422 PRIORITY_RESERVED", builtin, status, code)
		}
	}
	if status, code, _ := postPriority(t, srv, pid, "not–valid!", testutil.DemoUserID); status != http.StatusUnprocessableEntity || code != "INVALID_PRIORITY_NAME" {
		t.Errorf("invalid name: status %d code %s, want 422 INVALID_PRIORITY_NAME", status, code)
	}

	// Listing returns the custom set only.
	resp := testutil.Do(t, srv, http.MethodGet, fmt.Sprintf("/api/v1/projects/%s/task-priorities", pid), nil, testutil.SecondUserID)
	var list []map[string]interface{}
	testutil.DecodeJSON(t, resp, &list)
	if len(list) != 1 || list[0]["name"] != "URGENT" {
		t.Fatalf("list priorities = %v, want [URGENT]", list)
	}
	prioID := list[0]["id"].(string)

	// Tasks accept the custom priority; unknown values stay rejected.
	resp = testutil.Do(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/tasks", pid),
		map[string]interface{}{"title": "Urgent rollout", "priority": "URGENT"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var task map[string]interface{}
	testutil.DecodeJSON(t, resp, &task)
	resp = testutil.Do(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/tasks", pid),
		map[string]interface{}{"title": "Nope", "priority": "NOT_A_PRIORITY"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)

	// Deleting is blocked while a task carries the priority …
	resp = testutil.Do(t, srv, http.MethodDelete, "/api/v1/task-priorities/"+prioID, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)

	// … and works once it is reassigned.
	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+task["id"].(string)+"/priority",
		map[string]interface{}{"priority": "HIGH"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	resp = testutil.Do(t, srv, http.MethodDelete, "/api/v1/task-priorities/"+prioID, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNoContent)
}

// TestProjectPriorities_DeleteProjectCascade guards against the FK regression
// where deleting a project that still has a custom priority failed on the
// project_priorities → projects foreign key (the priority must be swept by the
// project cascade delete).
func TestProjectPriorities_DeleteProjectCascade(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Cascade")

	if status, _, _ := postPriority(t, srv, pid, "URGENT", testutil.DemoUserID); status != http.StatusCreated {
		t.Fatalf("create priority: status %d", status)
	}
	resp := testutil.Do(t, srv, http.MethodDelete, "/api/v1/projects/"+pid, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNoContent)
}
