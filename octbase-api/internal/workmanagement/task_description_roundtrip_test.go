package workmanagement_test

import (
	"net/http"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
)

// Saving a description the user did not change must not change it. The edit
// form reads a description back and PATCHes it verbatim, so a write path that
// re-escapes its own output corrupts the text a little more on every save:
// "&" became "&amp;", then "&amp;amp;", and arrows like "->" degraded to
// "-&amp;gt;". Real content on the dogfooding instance had already rotted this
// way before it was diagnosed.
//
// This is the API-level half of the guarantee; sanitize_test.go pins the same
// property directly on the sanitizer.
func TestUpdateTask_DescriptionRoundTripIsStable(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "P")

	cases := []struct {
		name string
		desc string
	}{
		{"ampersand", `<p>Tom &amp; Jerry</p>`},
		{"escaped angle brackets", `<p>a &gt; b &lt; c</p>`},
		{"raw punctuation", `a > b < c & d`},
		{"arrow", `<p>stage 1 -> stage 2</p>`},
		{"link with query string", `<a href="https://example.com/?a=1&amp;b=2">link</a>`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tid := testutil.MustCreateTask(t, srv, pid, "T")

			// Establish the stored form, then save it back unchanged twice —
			// what the edit form does when a user opens a task and hits save.
			patch := func(desc string, version float64) map[string]interface{} {
				resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/tasks/"+tid,
					map[string]interface{}{"description": desc, "version": version},
					testutil.DemoUserID)
				testutil.AssertStatus(t, resp, http.StatusOK)
				var task map[string]interface{}
				testutil.DecodeJSON(t, resp, &task)
				return task
			}

			first := patch(tc.desc, 1)
			stored, _ := first["description"].(string)
			version, _ := first["version"].(float64)

			second := patch(stored, version)
			if got, _ := second["description"].(string); got != stored {
				t.Fatalf("description changed on an unmodified save:\n  before: %q\n  after:  %q", stored, got)
			}
			version, _ = second["version"].(float64)

			third := patch(stored, version)
			if got, _ := third["description"].(string); got != stored {
				t.Fatalf("description changed on the third save:\n  before: %q\n  after:  %q", stored, got)
			}

			// And the persisted row agrees with what the writes returned.
			get := testutil.Do(t, srv, http.MethodGet, "/api/v1/tasks/"+tid, nil, testutil.DemoUserID)
			var fetched map[string]interface{}
			testutil.DecodeJSON(t, get, &fetched)
			if got, _ := fetched["description"].(string); got != stored {
				t.Errorf("stored description = %q, want %q", got, stored)
			}
		})
	}
}
