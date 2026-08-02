package workmanagement_test

import (
	"net/http"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
)

func columnNames(b map[string]interface{}) []string {
	cols, _ := b["columns"].([]interface{})
	names := make([]string, 0, len(cols))
	for _, c := range cols {
		m, _ := c.(map[string]interface{})
		names = append(names, m["name"].(string))
	}
	return names
}

func TestCreateBoard_KanbanTemplateSeedsColumns(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/boards",
		map[string]interface{}{"name": "B", "template": "kanban"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var b map[string]interface{}
	testutil.DecodeJSON(t, resp, &b)

	got := columnNames(b)
	want := []string{"To Do", "In Progress", "Done"}
	if len(got) != len(want) {
		t.Fatalf("kanban columns = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("column[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestCreateBoard_ScrumTemplateGermanLocale(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/boards",
		map[string]interface{}{"name": "B", "template": "scrum", "locale": "de"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var b map[string]interface{}
	testutil.DecodeJSON(t, resp, &b)

	got := columnNames(b)
	want := []string{"Zu erledigen", "In Arbeit", "In Prüfung", "Erledigt"}
	if len(got) != len(want) {
		t.Fatalf("scrum/de columns = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("column[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestCreateBoard_RejectsUnknownTemplate(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/boards",
		map[string]interface{}{"name": "B", "template": "bogus"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
}

func TestCreateBoard_MemberCannotCreate(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	// SecondUser is a writer-level member but not an owner/admin: board creation
	// is now owner/admin-only.
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_MEMBER")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/boards",
		map[string]interface{}{"name": "B", "template": "kanban"}, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
}

func TestDashboard_IncludesAccessibleProjectsAndBoards(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/users/me/dashboard", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var dash map[string]interface{}
	testutil.DecodeJSON(t, resp, &dash)

	projects, _ := dash["projects"].([]interface{})
	var foundProject bool
	for _, p := range projects {
		if m, _ := p.(map[string]interface{}); m["id"] == pid {
			foundProject = true
		}
	}
	if !foundProject {
		t.Error("dashboard projects should include the accessible project")
	}

	boards, _ := dash["boards"].([]interface{})
	var foundBoard bool
	for _, b := range boards {
		if m, _ := b.(map[string]interface{}); m["id"] == bid {
			foundBoard = true
		}
	}
	if !foundBoard {
		t.Error("dashboard boards should include the project's board")
	}
}
