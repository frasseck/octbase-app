package workmanagement_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
)

// ── UnifiedSearch ───────────────────────────────────────────────────────────

func TestUnifiedSearch_ShortQuery_Empty(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/search?q=a", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var out map[string][]interface{}
	testutil.DecodeJSON(t, resp, &out)
	if len(out["tasks"]) != 0 || len(out["pages"]) != 0 || len(out["projects"]) != 0 {
		t.Errorf("expected empty results for short query, got %v", out)
	}
}

func TestUnifiedSearch_FindsTask(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Searchable Project")
	testutil.MustCreateTask(t, srv, pid, "Findable Task Title")

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/search?q=Findable", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var out map[string][]interface{}
	testutil.DecodeJSON(t, resp, &out)
	if len(out["tasks"]) == 0 {
		t.Errorf("expected at least one task result, got %v", out["tasks"])
	}
}

func TestUnifiedSearch_FindsPage(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Docs Project")
	cr := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/pages",
		map[string]string{"title": "Encyclopedia Article", "content": "body"}, testutil.DemoUserID)
	testutil.AssertStatus(t, cr, http.StatusCreated)
	_ = cr.Body.Close()

	// Global search and project-scoped search both exercise SearchPages and the
	// scanPageSearchResults row loop.
	for _, path := range []string{
		"/api/v1/search?q=Encyclopedia",
		"/api/v1/search?q=Encyclopedia&projectId=" + pid,
	} {
		resp := testutil.Do(t, srv, http.MethodGet, path, nil, testutil.DemoUserID)
		testutil.AssertStatus(t, resp, http.StatusOK)
		var out map[string][]interface{}
		testutil.DecodeJSON(t, resp, &out)
		if len(out["pages"]) == 0 {
			t.Errorf("%s: expected at least one page result, got %v", path, out["pages"])
		}
	}
}

func TestSearchTasks_QueryTooLong(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	long := strings.Repeat("x", 501)
	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/search/tasks?q="+long, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusBadRequest)
	_ = resp.Body.Close()
}

// A query below the trigram threshold must answer empty rather than scan. The
// empty-q case is the one that mattered in practice: it made the ILIKE `%%`, so
// "search for nothing" returned every task in the project.
func TestSearchTasks_BelowMinLengthReturnsEmpty(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	testutil.MustCreateTask(t, srv, pid, "Zebra widget")

	for _, q := range []string{"", "Z", "Ze"} {
		resp := testutil.Do(t, srv, http.MethodGet,
			"/api/v1/projects/"+pid+"/search/tasks?q="+q, nil, testutil.DemoUserID)
		testutil.AssertStatus(t, resp, http.StatusOK)
		var out []map[string]interface{}
		testutil.DecodeJSON(t, resp, &out)
		if len(out) != 0 {
			t.Errorf("q=%q: expected no results below the minimum length, got %d", q, len(out))
		}
	}

	// Three characters is the threshold, so this one still searches.
	resp := testutil.Do(t, srv, http.MethodGet,
		"/api/v1/projects/"+pid+"/search/tasks?q=Zeb", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var out []map[string]interface{}
	testutil.DecodeJSON(t, resp, &out)
	if len(out) != 1 {
		t.Errorf("q=Zeb: expected the seeded task to match, got %d results", len(out))
	}
}

func TestUnifiedSearch_ProjectScoped(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Scoped")
	testutil.MustCreateTask(t, srv, pid, "Zebra widget")

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/search?q=Zebra&projectId="+pid, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()
}

// LIKE/ILIKE metacharacters in the query must be treated as literal text: a
// trailing backslash must not blow up the pattern (Postgres rejects a pattern
// ending in the escape character → 500 before shared.EscapeLike), and % / _
// must not act as wildcards.
func TestUnifiedSearch_LikeMetacharacters(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Meta")
	testutil.MustCreateTask(t, srv, pid, "Rollout 100% complete")
	testutil.MustCreateTask(t, srv, pid, "Findable Task Title")

	get := func(q string) map[string][]interface{} {
		t.Helper()
		resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/search?q="+url.QueryEscape(q), nil, testutil.DemoUserID)
		testutil.AssertStatus(t, resp, http.StatusOK)
		var out map[string][]interface{}
		testutil.DecodeJSON(t, resp, &out)
		return out
	}

	// Literal "%" is searchable.
	if out := get("100%"); len(out["tasks"]) != 1 {
		t.Errorf("q=100%%: expected exactly the percent-titled task, got %v", out["tasks"])
	}
	// "_" must not act as a single-character wildcard.
	if out := get("Find_ble"); len(out["tasks"]) != 0 {
		t.Errorf("q=Find_ble: expected no wildcard match, got %v", out["tasks"])
	}
	// A trailing backslash must not error the query.
	if out := get(`backsl\`); len(out["tasks"]) != 0 {
		t.Errorf(`q=backsl\: expected no match, got %v`, out["tasks"])
	}
}

// ── GetDashboard ────────────────────────────────────────────────────────────

func TestGetDashboard_OK(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "Assigned to me")
	// Assign the task to the demo user so it shows on the dashboard. Assignment
	// has a dedicated endpoint; PATCH /tasks/{id} no longer accepts assigneeId.
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/assign",
		map[string]string{"assigneeId": testutil.DemoUserID}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()

	d := testutil.Do(t, srv, http.MethodGet, "/api/v1/users/me/dashboard", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, d, http.StatusOK)
	var out map[string]interface{}
	testutil.DecodeJSON(t, d, &out)
	for _, k := range []string{"assignedTasks", "reviewingTasks", "recentPages", "upcomingReleases"} {
		if _, ok := out[k]; !ok {
			t.Errorf("dashboard missing key %q", k)
		}
	}
}

// TestGetDashboard_SuperAdmin exercises the Super Admin branch, which lists ALL
// projects (ProjectRepo.ListAll) rather than only the caller's memberships.
func TestGetDashboard_SuperAdmin(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	// A project the super admin is NOT a member of must still appear.
	testutil.MustCreateProject(t, srv, "Owned by demo")

	d := testutil.Do(t, srv, http.MethodGet, "/api/v1/users/me/dashboard", nil, testutil.SuperAdminUserID)
	testutil.AssertStatus(t, d, http.StatusOK)
	var out map[string]interface{}
	testutil.DecodeJSON(t, d, &out)
	projects, ok := out["projects"].([]interface{})
	if !ok || len(projects) == 0 {
		t.Errorf("super admin dashboard should list all projects, got %v", out["projects"])
	}
}

// ── Task template HTTP endpoints ────────────────────────────────────────────

func TestListTemplates_HTTP(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	cr := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/task-templates",
		map[string]string{"name": "Bug template", "titleTemplate": "Bug: {x}", "taskType": "TASK"}, testutil.DemoUserID)
	testutil.AssertStatus(t, cr, http.StatusCreated)
	_ = cr.Body.Close()

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/task-templates", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var list []interface{}
	testutil.DecodeJSON(t, resp, &list)
	if len(list) != 1 {
		t.Errorf("expected 1 template, got %d", len(list))
	}
}

func TestGetTemplate_HTTP(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	cr := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/task-templates",
		map[string]string{"name": "T", "titleTemplate": "x"}, testutil.DemoUserID)
	var tmpl map[string]interface{}
	testutil.DecodeJSON(t, cr, &tmpl)
	tid := tmpl["id"].(string)

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/task-templates/"+tid, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()
}

func TestGetTemplate_NotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/task-templates/"+missingID, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)
	_ = resp.Body.Close()
}

func TestUpdateTemplate_HTTP(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	cr := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/task-templates",
		map[string]string{"name": "Old", "titleTemplate": "x"}, testutil.DemoUserID)
	var tmpl map[string]interface{}
	testutil.DecodeJSON(t, cr, &tmpl)
	tid := tmpl["id"].(string)

	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/task-templates/"+tid,
		map[string]string{"name": "New", "priority": "HIGH", "taskType": "STORY", "descriptionTemplate": "d"},
		testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var updated map[string]interface{}
	testutil.DecodeJSON(t, resp, &updated)
	if updated["name"] != "New" || updated["priority"] != "HIGH" {
		t.Errorf("template not updated: %v", updated)
	}
}

func TestUpdateTemplate_NotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/task-templates/"+missingID,
		map[string]string{"name": "X"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)
	_ = resp.Body.Close()
}

// ── Bulk update actions (each branch) ───────────────────────────────────────

func TestBulkUpdate_AllActions(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	rid := testutil.MustCreateRelease(t, srv, pid, "R")
	t1 := testutil.MustCreateTask(t, srv, pid, "T1")
	t2 := testutil.MustCreateTask(t, srv, pid, "T2")
	ids := []string{t1, t2}

	cases := []struct {
		action string
		value  string
	}{
		{"set_status", "IN_PROGRESS"},
		{"set_priority", "HIGH"},
		{"set_assignee", testutil.DemoUserID},
		{"set_release", rid},
		{"archive", ""},
	}
	for _, c := range cases {
		body := map[string]interface{}{"taskIds": ids, "action": c.action}
		if c.value != "" {
			body["value"] = c.value
		}
		resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/tasks/bulk", body, testutil.DemoUserID)
		testutil.AssertStatus(t, resp, http.StatusOK)
		var out map[string]int
		testutil.DecodeJSON(t, resp, &out)
		if out["updated"] != 2 {
			t.Errorf("action %s: updated = %d, want 2", c.action, out["updated"])
		}
	}
}

func TestBulkUpdate_EmptyTaskIDs(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/tasks/bulk",
		map[string]interface{}{"taskIds": []string{}, "action": "archive"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
	_ = resp.Body.Close()
}

func TestBulkUpdate_InvalidPriority(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/tasks/bulk",
		map[string]interface{}{"taskIds": []string{tid}, "action": "set_priority", "value": "NOPE"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
	_ = resp.Body.Close()
}
