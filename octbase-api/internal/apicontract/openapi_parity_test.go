package apicontract_test

import (
	"bufio"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/octbase/octbase-api/internal/testutil"
)

// paramRe matches an OpenAPI/chi path parameter segment like {projectId}.
var paramRe = regexp.MustCompile(`\{[^}]+\}`)

// normalize collapses every path parameter to {} so chi route patterns and
// OpenAPI path keys compare equal regardless of the parameter's name.
func normalize(p string) string {
	return paramRe.ReplaceAllString(strings.TrimSuffix(p, "/"), "{}")
}

// outsideTestRouter are documented routes the test router does not assemble:
// the meta routes registered directly in cmd/octbase-api/main.go, the webhook
// receivers (also cmd-registered), and the SSE routes (internal/sse needs the
// live hub). The reverse (documented → served) check exempts them. Keep this
// list in sync; an entry that stops being served should be deleted here so
// the reverse check starts covering it.
var outsideTestRouter = map[string]bool{
	"/api/v1/health":               true,
	"/api/v1/version":              true,
	"/api/v1/config":               true,
	"/api/v1/meta/enums":           true,
	"/api/v1/webhooks/github":      true,
	"/api/v1/webhooks/bitbucket":   true,
	"/api/v1/projects/{}/events":   true,
	"/api/v1/projects/{}/presence": true,
}

// openapiOperations returns the normalized "METHOD path" set declared in
// api/openapi.yaml. It reads the two-space-indented path keys and their
// four-space-indented HTTP-method keys directly rather than fully parsing the
// document, which keeps the test free of a YAML dependency.
func openapiOperations(t *testing.T) map[string]bool {
	t.Helper()
	f, err := os.Open("../../api/openapi.yaml")
	if err != nil {
		t.Fatalf("open openapi.yaml: %v", err)
	}
	defer func() { _ = f.Close() }()

	pathRe := regexp.MustCompile(`^  (/api/v1\S*):\s*$`)
	methodRe := regexp.MustCompile(`^    (get|post|put|patch|delete):`)
	ops := map[string]bool{}
	var current string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if m := pathRe.FindStringSubmatch(line); m != nil {
			current = normalize(m[1])
			continue
		}
		if m := methodRe.FindStringSubmatch(line); m != nil && current != "" {
			ops[strings.ToUpper(m[1])+" "+current] = true
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan openapi.yaml: %v", err)
	}
	if len(ops) == 0 {
		t.Fatal("no /api/v1 operations found in openapi.yaml")
	}
	return ops
}

// servedOperations walks the fully-assembled test router and returns its
// normalized "METHOD path" set for /api/v1.
func servedOperations(t *testing.T) map[string]bool {
	t.Helper()
	db := testutil.NewTestDB(t)
	r := testutil.NewTestRouter(t, db)
	served := map[string]bool{}
	walk := func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if strings.HasPrefix(route, "/api/v1") {
			served[method+" "+normalize(route)] = true
		}
		return nil
	}
	if err := chi.Walk(r, walk); err != nil {
		t.Fatalf("walk routes: %v", err)
	}
	return served
}

func sorted(set map[string]bool) []string {
	var list []string
	for k := range set {
		list = append(list, k)
	}
	sort.Strings(list)
	return list
}

// TestEveryRouteIsDocumented fails if any /api/v1 operation the code serves —
// method-level, not just the path — is absent from openapi.yaml. This catches
// API surface that ships without a contract entry, including a new method
// added to an already-documented path.
func TestEveryRouteIsDocumented(t *testing.T) {
	documented := openapiOperations(t)
	served := servedOperations(t)

	missing := map[string]bool{}
	for op := range served {
		if !documented[op] {
			missing[op] = true
		}
	}
	if len(missing) > 0 {
		t.Errorf("%d operation(s) served but missing from openapi.yaml:\n%s",
			len(missing), strings.Join(sorted(missing), "\n"))
	}
}

// TestEveryDocumentedRouteIsServed is the reverse direction: a documented
// operation that no code serves is contract rot — the spec promises an
// endpoint that 404s. Routes registered in cmd (health/version/meta/config)
// are exempt because the test router does not assemble them.
func TestEveryDocumentedRouteIsServed(t *testing.T) {
	documented := openapiOperations(t)
	served := servedOperations(t)

	stale := map[string]bool{}
	for op := range documented {
		_, path, _ := strings.Cut(op, " ")
		if outsideTestRouter[path] {
			continue
		}
		if !served[op] {
			stale[op] = true
		}
	}
	if len(stale) > 0 {
		t.Errorf("%d operation(s) documented in openapi.yaml but not served (contract rot):\n%s",
			len(stale), strings.Join(sorted(stale), "\n"))
	}
}
