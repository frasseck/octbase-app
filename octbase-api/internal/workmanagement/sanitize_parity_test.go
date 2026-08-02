package workmanagement

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/octbase/octbase-api/internal/shared"
)

// The URL guards exist a second time in JS (octbase-shared/richtext.js —
// rtSafeHref / rtSafeImageSrc), because DOMPurify runs in the browser and has
// to reach the same verdict the server already reached. That JS copy carried a
// comment claiming parity and nothing enforced it, so a change here could
// silently leave the browser accepting a URL the server rejects (or refusing to
// render one it stored).
//
// The Go side of the href guard now lives in internal/shared (SafeHref), shared
// with internal/docs; safeImageSrc stays here because the task and page image
// policies genuinely differ. The case table below covers this module's pair, so
// it pins the href guard for pages too.
//
// testdata/url-guard-cases.json at the repository root is the contract. This
// test and octbase-frontend/js/richtext.test.js both read it, so a one-sided
// change fails on both sides instead of drifting.

type urlGuardCase struct {
	Input string `json:"input"`
	Want  bool   `json:"want"`
	Why   string `json:"why"`
}

type urlGuardCases struct {
	SafeHref     []urlGuardCase `json:"safeHref"`
	SafeImageSrc []urlGuardCase `json:"safeImageSrc"`
}

// loadURLGuardCases reads the shared table. Its absence is a failure, not a
// skip: a parity test that quietly passes when it cannot find its cases is
// worse than no test, because the comment in richtext.js would still claim
// something is checked.
func loadURLGuardCases(t *testing.T) urlGuardCases {
	t.Helper()
	// internal/workmanagement → octbase-api → repository root.
	path := filepath.Join("..", "..", "..", "testdata", "url-guard-cases.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read shared case table (%s): %v", path, err)
	}
	var cases urlGuardCases
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatalf("parse shared case table: %v", err)
	}
	if len(cases.SafeHref) == 0 || len(cases.SafeImageSrc) == 0 {
		t.Fatal("shared case table is empty for one of the guards")
	}
	return cases
}

func TestSafeHref_SharedCaseTable(t *testing.T) {
	for _, c := range loadURLGuardCases(t).SafeHref {
		if got := shared.SafeHref(c.Input); got != c.Want {
			t.Errorf("safeHref(%q) = %v, want %v — %s", c.Input, got, c.Want, c.Why)
		}
	}
}

func TestSafeImageSrc_SharedCaseTable(t *testing.T) {
	for _, c := range loadURLGuardCases(t).SafeImageSrc {
		if got := safeImageSrc(c.Input); got != c.Want {
			t.Errorf("safeImageSrc(%q) = %v, want %v — %s", c.Input, got, c.Want, c.Why)
		}
	}
}
