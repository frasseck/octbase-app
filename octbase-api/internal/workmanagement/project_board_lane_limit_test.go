package workmanagement_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
)

// setBoardLaneLimit changes the project's board lane limit as the given actor
// and returns status + error code, so both the happy and the rejected paths can
// use it. patchProject lives in project_settings_test.go.
func setBoardLaneLimit(t *testing.T, srv *httptest.Server, pid string, limit int, actorID string) (int, string) {
	t.Helper()
	status, code, _ := patchProject(t, srv, pid, map[string]interface{}{"boardLaneLimit": limit}, actorID)
	return status, code
}

// projectBoardLaneLimit reads the setting back off the project. Every assertion
// here goes through a GET rather than the PATCH response, because a "200 with
// the value unchanged" is a bug this API has shipped before.
func projectBoardLaneLimit(t *testing.T, srv *httptest.Server, pid string) float64 {
	t.Helper()
	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid, nil, testutil.DemoUserID)
	var p map[string]interface{}
	testutil.DecodeJSON(t, resp, &p)
	v, ok := p["boardLaneLimit"].(float64)
	if !ok {
		t.Fatalf("boardLaneLimit missing or not a number in project response: %v", p["boardLaneLimit"])
	}
	return v
}

func TestBoardLaneLimit_DefaultsToTwenty(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Lane limit default")

	if got := projectBoardLaneLimit(t, srv, pid); got != 20 {
		t.Fatalf("fresh project boardLaneLimit = %v, want 20", got)
	}
}

func TestBoardLaneLimit_IsOwnerGatedAndValidated(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Lane limit guards")
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_MEMBER")

	// An ordinary member may not change the setting — it is project-wide, so it
	// sits behind the same owner gate as the other project settings.
	if status, _ := setBoardLaneLimit(t, srv, pid, 50, testutil.SecondUserID); status != http.StatusForbidden {
		t.Fatalf("member setting boardLaneLimit: status %d, want 403", status)
	}

	// Out of range is a loud 422, both ends. A negative is rejected rather than
	// clamped to 0: clamping would turn a client bug into "the cap silently
	// stopped applying".
	for _, bad := range []int{-1, 501} {
		status, code := setBoardLaneLimit(t, srv, pid, bad, testutil.DemoUserID)
		if status != http.StatusUnprocessableEntity || code != "BOARD_LANE_LIMIT_INVALID" {
			t.Errorf("boardLaneLimit=%d: status %d code %s, want 422 BOARD_LANE_LIMIT_INVALID", bad, status, code)
		}
	}

	// The rejected writes left the stored value alone.
	if got := projectBoardLaneLimit(t, srv, pid); got != 20 {
		t.Fatalf("after rejected writes, boardLaneLimit = %v, want the default 20 still", got)
	}

	// The owner can set it, and it reads back.
	if status, code := setBoardLaneLimit(t, srv, pid, 50, testutil.DemoUserID); status != http.StatusOK {
		t.Fatalf("owner setting boardLaneLimit: status %d code %s, want 200", status, code)
	}
	if got := projectBoardLaneLimit(t, srv, pid); got != 50 {
		t.Fatalf("after owner change, boardLaneLimit = %v, want 50", got)
	}
}

// 0 means "draw every card". It is inside the accepted range on purpose: it is
// the pre-38 behaviour and therefore the opt-out, so it must survive a write
// instead of being mistaken for an unset field.
func TestBoardLaneLimit_ZeroIsAcceptedAsUnlimited(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Lane limit unlimited")

	if status, code := setBoardLaneLimit(t, srv, pid, 0, testutil.DemoUserID); status != http.StatusOK {
		t.Fatalf("boardLaneLimit=0: status %d code %s, want 200", status, code)
	}
	if got := projectBoardLaneLimit(t, srv, pid); got != 0 {
		t.Fatalf("boardLaneLimit after setting 0 = %v, want 0 (unlimited)", got)
	}
}

// The setting must not be collateral damage of an unrelated project edit: a
// PATCH that does not mention boardLaneLimit leaves it where it was. The repo
// writes every column on update, so this guards the field's round trip through
// Update(), not just the handler's branch.
func TestBoardLaneLimit_SurvivesUnrelatedProjectPatch(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Lane limit persistence")

	if status, code := setBoardLaneLimit(t, srv, pid, 5, testutil.DemoUserID); status != http.StatusOK {
		t.Fatalf("set boardLaneLimit=5: status %d code %s", status, code)
	}
	status, code, _ := patchProject(t, srv, pid,
		map[string]interface{}{"description": "renamed for the test"}, testutil.DemoUserID)
	if status != http.StatusOK {
		t.Fatalf("unrelated patch: status %d code %s, want 200", status, code)
	}
	if got := projectBoardLaneLimit(t, srv, pid); got != 5 {
		t.Fatalf("boardLaneLimit after unrelated patch = %v, want 5", got)
	}
}
