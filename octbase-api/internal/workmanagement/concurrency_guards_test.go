package workmanagement_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/octbase/octbase-api/internal/shared"
	"github.com/octbase/octbase-api/internal/testutil"
	"github.com/octbase/octbase-api/internal/workmanagement"
)

// Concurrency guards beyond the classic PATCH endpoints
// (version_conflict_test.go): boards, board columns and comments carry a
// version since migration 030; the task quick actions (move/status/priority/
// assign) accept an optional version; and cross-row invariants (one active
// sprint, unique page slug, no closing a release over open tasks) are enforced
// by the database, not just by handler pre-checks.

func doJSON(t *testing.T, srv *httptest.Server, method, path string, body any) map[string]interface{} {
	t.Helper()
	resp := testutil.Do(t, srv, method, path, body, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var out map[string]interface{}
	testutil.DecodeJSON(t, resp, &out)
	return out
}

func assertConflict(t *testing.T, srv *httptest.Server, method, path string, body any) {
	t.Helper()
	resp := testutil.Do(t, srv, method, path, body, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusConflict)
	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "VERSION_CONFLICT" {
		t.Errorf("code = %v, want VERSION_CONFLICT", e["code"])
	}
}

func TestUpdateBoard_VersionConflict(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)

	got := doJSON(t, srv, http.MethodPatch, "/api/v1/boards/"+bid,
		map[string]interface{}{"name": "Edit A", "version": 1})
	if v, _ := got["version"].(float64); v != 2 {
		t.Fatalf("version after update = %v, want 2", got["version"])
	}
	assertConflict(t, srv, http.MethodPatch, "/api/v1/boards/"+bid,
		map[string]interface{}{"name": "Stale", "version": 1})

	got = doJSON(t, srv, http.MethodGet, "/api/v1/boards/"+bid, nil)
	if got["name"] != "Edit A" {
		t.Errorf("name = %v, want 'Edit A' (stale write must not win)", got["name"])
	}
}

func TestUpdateColumn_VersionConflict_AndPartialPatch(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)
	cid := testutil.MustAddColumn(t, srv, bid, "Lane A", "LANE_A", 0)

	// A partial PATCH must leave omitted fields untouched (the handler loads
	// the existing column instead of writing a zeroed struct).
	got := doJSON(t, srv, http.MethodPatch, "/api/v1/boards/"+bid+"/columns/"+cid,
		map[string]interface{}{"position": 3, "version": 1})
	if got["name"] != "Lane A" || got["status"] != "LANE_A" {
		t.Fatalf("partial patch clobbered fields: name=%v status=%v", got["name"], got["status"])
	}
	if v, _ := got["version"].(float64); v != 2 {
		t.Fatalf("version after update = %v, want 2", got["version"])
	}
	assertConflict(t, srv, http.MethodPatch, "/api/v1/boards/"+bid+"/columns/"+cid,
		map[string]interface{}{"name": "Stale", "version": 1})
}

func TestUpdateComment_VersionConflict(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	create := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/comments",
		map[string]string{"text": "first"}, testutil.DemoUserID)
	testutil.AssertStatus(t, create, http.StatusCreated)
	var c map[string]interface{}
	testutil.DecodeJSON(t, create, &c)
	cid, _ := c["id"].(string)
	if v, _ := c["version"].(float64); v != 1 {
		t.Fatalf("new comment version = %v, want 1", c["version"])
	}

	got := doJSON(t, srv, http.MethodPatch, "/api/v1/tasks/"+tid+"/comments/"+cid,
		map[string]interface{}{"text": "Edit A", "version": 1})
	if v, _ := got["version"].(float64); v != 2 {
		t.Fatalf("version after update = %v, want 2", got["version"])
	}
	assertConflict(t, srv, http.MethodPatch, "/api/v1/tasks/"+tid+"/comments/"+cid,
		map[string]interface{}{"text": "Stale", "version": 1})
}

func TestTaskQuickActions_VersionConflict(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	bid := testutil.MustCreateBoard(t, srv, pid)
	cid := testutil.MustAddColumn(t, srv, bid, "Doing", "DOING", 0)
	tid := testutil.MustCreateTask(t, srv, pid, "T")

	// Stale versions are rejected on every quick action…
	assertConflict(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/status",
		map[string]interface{}{"status": "IN_PROGRESS", "version": 99})
	assertConflict(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/priority",
		map[string]interface{}{"priority": "HIGH", "version": 99})
	assertConflict(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/assign",
		map[string]interface{}{"assigneeId": testutil.DemoUserID, "version": 99})
	assertConflict(t, srv, http.MethodPost, "/api/v1/boards/"+bid+"/move-task",
		map[string]interface{}{"taskId": tid, "boardColumnId": cid, "boardRank": 1000, "version": 99})

	// …and the current version succeeds and is incremented in the response.
	got := doJSON(t, srv, http.MethodPost, "/api/v1/boards/"+bid+"/move-task",
		map[string]interface{}{"taskId": tid, "boardColumnId": cid, "boardRank": 1000, "version": 1})
	if v, _ := got["version"].(float64); v != 2 {
		t.Errorf("version after move = %v, want 2", got["version"])
	}
}

// TestStartSprint_SingleActiveEnforcedByDB proves the one-active-sprint rule
// holds even when the handler's advisory FindActive pre-check is bypassed —
// i.e. the exact interleaving of two concurrent starts.
func TestStartSprint_SingleActiveEnforcedByDB(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	var sprintIDs []string
	for _, name := range []string{"S1", "S2"} {
		resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/sprints",
			map[string]string{"name": name}, testutil.DemoUserID)
		testutil.AssertStatus(t, resp, http.StatusCreated)
		var sp map[string]interface{}
		testutil.DecodeJSON(t, resp, &sp)
		sprintIDs = append(sprintIDs, sp["id"].(string))
	}

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/sprints/"+sprintIDs[0]+"/start", map[string]string{}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	// The HTTP path reports the friendly domain error…
	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/sprints/"+sprintIDs[1]+"/start", map[string]string{}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "SPRINT_ALREADY_ACTIVE" {
		t.Errorf("code = %v, want SPRINT_ALREADY_ACTIVE", e["code"])
	}

	// …and a write that slips past the pre-check (the concurrent interleaving)
	// is stopped by idx_sprints_one_active.
	if _, err := db.Exec(`UPDATE sprints SET status='ACTIVE' WHERE id=$1`, sprintIDs[1]); !shared.IsUniqueViolation(err) {
		t.Errorf("direct second ACTIVE update: err = %v, want unique violation", err)
	}
}

// TestCompleteSprint_StaleVersionRollsBack proves completion is atomic: when
// the guarded sprint update loses (a concurrent editor bumped the version
// between read and write), the task unlinking and board teardown roll back.
func TestCompleteSprint_StaleVersionRollsBack(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/sprints",
		map[string]string{"name": "S1"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	var sp map[string]interface{}
	testutil.DecodeJSON(t, resp, &sp)
	sid := sp["id"].(string)

	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/sprints/"+sid+"/start", map[string]string{}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	tid := testutil.MustCreateTask(t, srv, pid, "In sprint")
	if _, err := db.Exec(`UPDATE tasks SET sprint_id=$1 WHERE id=$2`, sid, tid); err != nil {
		t.Fatal(err)
	}

	svc := workmanagement.NewService(db,
		workmanagement.NewTaskRepo(db), workmanagement.NewTaskCommentRepo(db),
		workmanagement.NewTaskLinkRepo(db), workmanagement.NewTaskAttachmentRepo(db),
		workmanagement.NewTaskRelationRepo(db), workmanagement.NewReleaseRepo(db),
		workmanagement.NewBoardRepo(db), workmanagement.NewBoardColumnRepo(db),
		workmanagement.NewSprintRepo(db), workmanagement.NewTaskTemplateRepo(db))

	stale := &workmanagement.Sprint{ID: sid, ProjectID: pid, Name: "S1", Status: workmanagement.SprintStatusActive, Version: 99}
	if _, err := svc.CompleteSprint(stale, workmanagement.EstimationUnitNone); err != shared.ErrVersionConflict {
		t.Fatalf("CompleteSprint with stale version: err = %v, want ErrVersionConflict", err)
	}

	// Rollback: the task is still linked and the sprint board still exists.
	var sprintLink *string
	if err := db.QueryRow(`SELECT sprint_id FROM tasks WHERE id=$1`, tid).Scan(&sprintLink); err != nil {
		t.Fatal(err)
	}
	if sprintLink == nil || *sprintLink != sid {
		t.Errorf("task sprint link = %v, want %s (unlink must roll back)", sprintLink, sid)
	}
	var boards int
	if err := db.QueryRow(`SELECT COUNT(*) FROM boards WHERE sprint_id=$1`, sid).Scan(&boards); err != nil {
		t.Fatal(err)
	}
	if boards != 1 {
		t.Errorf("sprint boards = %d, want 1 (teardown must roll back)", boards)
	}
}

// TestCreatePage_DuplicateSlug covers both the handler pre-check (409) and the
// DB unique index that closes the concurrent-create window.
func TestCreatePage_DuplicateSlug(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/pages",
		map[string]string{"title": "Spec", "slug": "spec"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)

	resp = testutil.Do(t, srv, http.MethodPost, "/api/v1/projects/"+pid+"/pages",
		map[string]string{"title": "Spec 2", "slug": "spec"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusConflict)
	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "SLUG_CONFLICT" {
		t.Errorf("code = %v, want SLUG_CONFLICT", e["code"])
	}

	// A write bypassing the pre-check (the concurrent interleaving) is stopped
	// by idx_pages_project_slug.
	_, err := db.Exec(`INSERT INTO pages (id,project_id,title,slug,created_at,updated_at) VALUES ('11111111-1111-1111-1111-111111111111',$1,'X','spec','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, pid)
	if !shared.IsUniqueViolation(err) {
		t.Errorf("direct duplicate slug insert: err = %v, want unique violation", err)
	}
}

func TestCreateProject_DuplicateName_Conflict(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	testutil.MustCreateProject(t, srv, "Same Name")

	// Previously a raw 500 from the UNIQUE constraint; now a clean conflict.
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/projects",
		map[string]string{"name": "Same Name"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusConflict)
	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "SLUG_CONFLICT" {
		t.Errorf("code = %v, want SLUG_CONFLICT", e["code"])
	}
}

// TestCloseRelease_OpenTaskAtWriteTime proves the close is atomic: even when
// the open-task count pre-check is bypassed (a task joins the release between
// check and write), the conditional UPDATE refuses to close over it.
func TestCloseRelease_OpenTaskAtWriteTime(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	rid := testutil.MustCreateRelease(t, srv, pid, "R1")
	tid := testutil.MustCreateTask(t, srv, pid, "Open task")
	if _, err := db.Exec(`UPDATE tasks SET release_id=$1 WHERE id=$2`, rid, tid); err != nil {
		t.Fatal(err)
	}

	// HTTP path: friendly domain error from the pre-check.
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/releases/"+rid+"/close", map[string]string{}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "RELEASE_HAS_OPEN_TASKS" {
		t.Errorf("code = %v, want RELEASE_HAS_OPEN_TASKS", e["code"])
	}

	// Repo path with the pre-check bypassed: the guarded conditional UPDATE
	// must refuse (zero rows → ErrVersionConflict for the service to
	// disambiguate) and leave the release open.
	repo := workmanagement.NewReleaseRepo(db)
	m := &workmanagement.Release{ID: rid, Status: workmanagement.StatusClosed, UpdatedAt: "2026-01-01T00:00:00Z", Version: 1}
	if err := repo.CloseGuarded(m); err != shared.ErrVersionConflict {
		t.Fatalf("CloseGuarded over open task: err = %v, want ErrVersionConflict", err)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM releases WHERE id=$1`, rid).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status == workmanagement.StatusClosed {
		t.Errorf("release closed over an open task")
	}
}
