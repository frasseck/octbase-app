package workmanagement_test

import (
	"net/http"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
)

// GET /projects/{id}/tasks pages its results (default size 20), and the body
// is a bare array, so without a total a client receiving exactly `size` items
// cannot tell a full page from a truncated one. X-Total-Count carries the
// number of tasks matching the filters across all pages.
func TestListTasks_XTotalCount(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")
	for _, title := range []string{"A", "B", "C"} {
		testutil.MustCreateTask(t, srv, pid, title)
	}

	// A page smaller than the project still reports the full total.
	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/tasks?size=2", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	if got := resp.Header.Get("X-Total-Count"); got != "3" {
		t.Fatalf("X-Total-Count = %q, want 3", got)
	}
	var page []map[string]any
	testutil.DecodeJSON(t, resp, &page)
	if len(page) != 2 {
		t.Fatalf("page size = %d, want 2", len(page))
	}

	// The total respects the same filters as the listing.
	resp = testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/tasks?status=PLANNED&size=1", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	if got := resp.Header.Get("X-Total-Count"); got != "3" {
		t.Fatalf("filtered X-Total-Count = %q, want 3", got)
	}
	resp = testutil.Do(t, srv, http.MethodGet, "/api/v1/projects/"+pid+"/tasks?status=DONE", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	if got := resp.Header.Get("X-Total-Count"); got != "0" {
		t.Fatalf("empty-match X-Total-Count = %q, want 0", got)
	}
}
