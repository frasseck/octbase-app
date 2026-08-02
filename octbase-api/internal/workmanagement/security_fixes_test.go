package workmanagement_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
)

// These guard the cross-project Broken-Object-Level-Authorization fixes from the
// 2026-07-14 security assessment: several write/delete handlers guarded the
// project named in the URL but then acted on a child object (task/link/relation/
// column) taken from the request body or a second path segment without checking
// it belonged to that project. An authenticated writer in ONE project could
// mutate, disclose, or delete objects in ANY other project (incl. PRIVATE ones).
//
// Shared shape: DemoUserID (ADMIN) owns a "victim" project P1 and an "attacker"
// project P2; SecondUserID is added to P2 as a writer but is NOT a member of P1.
// Acting as SecondUserID, a cross-project reference into P1 must now be rejected.

func mustCreateLink(t *testing.T, srv *httptest.Server, taskID, actor string) string {
	t.Helper()
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+taskID+"/links",
		map[string]string{"url": "https://example.com", "title": "secret"}, actor)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var m map[string]any
	testutil.DecodeJSON(t, resp, &m)
	return m["id"].(string)
}

func TestMoveTask_CrossProjectTask_NotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	p1 := testutil.MustCreateProject(t, srv, "Victim P1")
	victimTask := testutil.MustCreateTask(t, srv, p1, "victim task")

	p2 := testutil.MustCreateProject(t, srv, "Attacker P2")
	testutil.MustAddMember(t, srv, p2, testutil.SecondUserID, "PROJECT_MEMBER")
	board2 := testutil.MustCreateBoard(t, srv, p2)
	col2 := testutil.MustAddColumn(t, srv, board2, "Todo", "PLANNED", 0)

	// Attacker (writer in P2, not a member of P1) tries to move P1's task onto
	// their own board. Must be rejected without disclosing or mutating the task.
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+board2+"/move-task",
		map[string]any{"taskId": victimTask, "boardColumnId": col2, "boardRank": 0}, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)
	_ = resp.Body.Close()
}

func TestRemoveTaskFromBoard_CrossProjectTask_NotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	p1 := testutil.MustCreateProject(t, srv, "Victim P1")
	victimTask := testutil.MustCreateTask(t, srv, p1, "victim task")

	p2 := testutil.MustCreateProject(t, srv, "Attacker P2")
	testutil.MustAddMember(t, srv, p2, testutil.SecondUserID, "PROJECT_MEMBER")
	board2 := testutil.MustCreateBoard(t, srv, p2)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+board2+"/remove-task",
		map[string]any{"taskId": victimTask}, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)
	_ = resp.Body.Close()
}

func TestDeleteLink_CrossProjectLink_NotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	p1 := testutil.MustCreateProject(t, srv, "Victim P1")
	victimTask := testutil.MustCreateTask(t, srv, p1, "victim task")
	victimLink := mustCreateLink(t, srv, victimTask, testutil.DemoUserID)

	p2 := testutil.MustCreateProject(t, srv, "Attacker P2")
	testutil.MustAddMember(t, srv, p2, testutil.SecondUserID, "PROJECT_MEMBER")
	attackerTask := testutil.MustCreateTask(t, srv, p2, "attacker task")

	// Attacker deletes the victim's link by routing it through their own task.
	resp := testutil.Do(t, srv, http.MethodDelete,
		"/api/v1/tasks/"+attackerTask+"/links/"+victimLink, nil, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusNotFound)
	_ = resp.Body.Close()

	// The victim's link must still be there.
	got := testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+victimTask+"/links", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, got, http.StatusOK)
	var links []map[string]any
	testutil.DecodeJSON(t, got, &links)
	if len(links) != 1 {
		t.Fatalf("victim link was deleted cross-project: got %d links, want 1", len(links))
	}

	// Positive control: the owner can delete their own link.
	ownLink := mustCreateLink(t, srv, victimTask, testutil.DemoUserID)
	ok := testutil.Do(t, srv, http.MethodDelete, "/api/v1/tasks/"+victimTask+"/links/"+ownLink, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, ok, http.StatusNoContent)
	_ = ok.Body.Close()
}

func TestDeleteRelation_CrossProjectRelation_NotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	p1 := testutil.MustCreateProject(t, srv, "Victim P1")
	a := testutil.MustCreateTask(t, srv, p1, "A")
	b := testutil.MustCreateTask(t, srv, p1, "B")
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+a+"/relations",
		map[string]string{"targetTaskId": b, "relationType": "RELATES_TO"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var rel map[string]any
	testutil.DecodeJSON(t, resp, &rel)
	relID := rel["id"].(string)

	p2 := testutil.MustCreateProject(t, srv, "Attacker P2")
	testutil.MustAddMember(t, srv, p2, testutil.SecondUserID, "PROJECT_MEMBER")
	attackerTask := testutil.MustCreateTask(t, srv, p2, "attacker task")

	del := testutil.Do(t, srv, http.MethodDelete,
		"/api/v1/tasks/"+attackerTask+"/relations/"+relID, nil, testutil.SecondUserID)
	testutil.AssertStatus(t, del, http.StatusNotFound)
	_ = del.Body.Close()

	// Relation must survive.
	got := testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+a+"/relations", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, got, http.StatusOK)
	var rels []map[string]any
	testutil.DecodeJSON(t, got, &rels)
	if len(rels) == 0 {
		t.Fatal("victim relation was deleted cross-project")
	}
}

func TestDeleteRelation_MissingRelation_NotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	p1 := testutil.MustCreateProject(t, srv, "P1")
	task := testutil.MustCreateTask(t, srv, p1, "task")

	// Deleting a relation id that never existed answers 404, not a misleading
	// 204 — consistent with the sibling DeleteLink / DeleteExternalColumn.
	del := testutil.Do(t, srv, http.MethodDelete,
		"/api/v1/tasks/"+task+"/relations/00000000-0000-0000-0000-0000000000ff", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, del, http.StatusNotFound)
	_ = del.Body.Close()
}

func TestViewer_CannotWriteCategoriesOrTemplates(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	p1 := testutil.MustCreateProject(t, srv, "P1")
	testutil.MustAddMember(t, srv, p1, testutil.SecondUserID, "PROJECT_VIEWER")

	// Owner creates a category + template.
	cResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+p1+"/task-categories",
		map[string]string{"name": "Cat"}, testutil.DemoUserID)
	testutil.AssertStatus(t, cResp, http.StatusCreated)
	var cat map[string]any
	testutil.DecodeJSON(t, cResp, &cat)
	catID := cat["id"].(string)

	tResp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+p1+"/task-templates",
		map[string]string{"name": "Tmpl", "taskType": "TASK", "priority": "MEDIUM"}, testutil.DemoUserID)
	testutil.AssertStatus(t, tResp, http.StatusCreated)
	var tmpl map[string]any
	testutil.DecodeJSON(t, tResp, &tmpl)
	tmplID := tmpl["id"].(string)

	// A PROJECT_VIEWER must not mutate/delete them.
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPatch, "/api/v1/task-categories/" + catID, map[string]string{"name": "hacked"}},
		{http.MethodDelete, "/api/v1/task-categories/" + catID, nil},
		{http.MethodDelete, "/api/v1/task-templates/" + tmplID, nil},
	} {
		r := testutil.Do(t, srv, c.method, c.path, c.body, testutil.SecondUserID)
		testutil.AssertStatus(t, r, http.StatusForbidden)
		_ = r.Body.Close()
	}
}

func TestViewer_CannotCreateBranch(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	p1 := testutil.MustCreateProject(t, srv, "P1")
	task := testutil.MustCreateTask(t, srv, p1, "task")
	testutil.MustAddMember(t, srv, p1, testutil.SecondUserID, "PROJECT_VIEWER")

	// RequireWriter must reject the viewer before any repo lookup (403, not 404).
	r := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+task+"/branches",
		map[string]string{"repositoryId": "00000000-0000-0000-0000-0000000000ff", "branchName": "x", "branchType": "feature"},
		testutil.SecondUserID)
	testutil.AssertStatus(t, r, http.StatusForbidden)
	_ = r.Body.Close()
}

func TestLegacyAdmin_CannotActOnSuperAdmin(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	// A plain ADMIN (DemoUserID) must not toggle a SUPER_ADMIN via the legacy
	// /admin/users endpoint (it had no target-role guard).
	r := testutil.Do(t, srv, http.MethodPatch, "/api/v1/admin/users/"+testutil.SuperAdminUserID,
		map[string]any{"isActive": true}, testutil.DemoUserID)
	testutil.AssertStatus(t, r, http.StatusForbidden)
	_ = r.Body.Close()

	// Positive control: ADMIN may still act on a non-super account.
	ok := testutil.Do(t, srv, http.MethodPatch, "/api/v1/admin/users/"+testutil.SecondUserID,
		map[string]any{"isActive": true}, testutil.DemoUserID)
	testutil.AssertStatus(t, ok, http.StatusOK)
	_ = ok.Body.Close()
}
