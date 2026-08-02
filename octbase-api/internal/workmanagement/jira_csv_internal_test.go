package workmanagement

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// TestImportJiraCSV_DisabledEdition verifies that when the Jira CSV import is
// switched off by the deployment edition (OCTBASE_EDITION=TEAM), the import route
// answers 403 with the stable FEATURE_DISABLED code instead of importing. The
// gate replies before touching any handler dependency, so a zero Handler is
// enough — no DB needed.
func TestImportJiraCSV_DisabledEdition(t *testing.T) {
	h := &Handler{}
	r := chi.NewRouter()
	h.RegisterCSVRoutes(r, false)

	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/projects/00000000-0000-0000-0000-000000000010/import/jira-csv",
		strings.NewReader("Summary\nSome task\n"))
	req.Header.Set("Content-Type", "text/csv")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d (body: %s)", w.Code, w.Body.String())
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != "FEATURE_DISABLED" {
		t.Errorf("code = %q, want %q", body.Code, "FEATURE_DISABLED")
	}
}
