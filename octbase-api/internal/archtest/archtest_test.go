// Package archtest turns the dependency rules of docs/architecture.md §1 into
// an executable contract: everything may use core, core must not import
// bounded contexts, and contexts must not import each other — except through
// the reviewed allowlist below. Test files are exempt: like main.go, they are
// composition roots and may wire concrete types from any context.
package archtest

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const internalPrefix = "github.com/octbase/octbase-api/internal/"

// Every package under internal/ must appear in exactly one of these sets; the
// test fails on unclassified packages so adding a context forces an explicit
// layering decision here.
var (
	// core: domain-agnostic infrastructure any package may import.
	core = set("shared", "rbac", "auth", "auditlog", "mailer", "sse")

	// modules: bounded contexts. They may import core, never each other.
	modules = set(
		"activity", "admin", "dashboard", "docs", "identityaccess",
		"notifications", "retention", "scmintegration", "security/mfa",
		"usermgmt", "webhooks", "workmanagement",
	)

	// unrestricted: composition/test plumbing that may import anything, and
	// that non-test code must not import.
	unrestricted = set("seed", "bootstrap", "testutil", "apicontract", "archtest")
)

// allowedCross lists the reviewed exceptions to "contexts do not import each
// other". Additions require the same architectural sign-off as an edit to
// docs/architecture.md.
var allowedCross = map[string]string{
	"workmanagement -> docs": "project import consumes docs' published types and task-reference extraction",
	"auth -> security/mfa":   "login issues the MFA challenge token; entangled by design (architecture.md §4)",
}

func set(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

// contextOf resolves a package path under internal/ to its classified context
// by longest prefix, so subpackages (e.g. security/mfa's helpers) inherit
// their context's classification. Unclassified paths are returned unchanged.
func contextOf(p string) string {
	for q := p; q != ""; {
		if core[q] || modules[q] || unrestricted[q] {
			return q
		}
		i := strings.LastIndex(q, "/")
		if i < 0 {
			break
		}
		q = q[:i]
	}
	return p
}

func TestDependencyRules(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}

	imports := map[string][]string{} // internal package -> internal packages it imports
	fset := token.NewFileSet()
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// Build constraints are not evaluated: a //go:build-excluded or
		// OS-suffixed file is still checked. None exist under internal/ today.
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		pkg := filepath.ToSlash(rel)
		if _, ok := imports[pkg]; !ok {
			imports[pkg] = nil
		}
		for _, imp := range f.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return err
			}
			if strings.HasPrefix(p, internalPrefix) {
				imports[pkg] = append(imports[pkg], strings.TrimPrefix(p, internalPrefix))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	for pkg, imps := range imports {
		from := contextOf(pkg)
		if !core[from] && !modules[from] && !unrestricted[from] {
			t.Errorf("internal/%s is not classified: add it to core, modules, or unrestricted in archtest", pkg)
			continue
		}
		if unrestricted[from] {
			continue
		}
		for _, imp := range imps {
			to := contextOf(imp)
			if to == from || core[to] {
				continue
			}
			edge := from + " -> " + to
			if _, ok := allowedCross[edge]; ok {
				continue
			}
			switch {
			case unrestricted[to]:
				t.Errorf("%s: non-test code must not import internal/%s", edge, imp)
			case core[from]:
				t.Errorf("%s: core packages must not import bounded contexts", edge)
			default:
				t.Errorf("%s: contexts must not import each other; depend on a consumer-defined interface wired in main, or allowlist the edge in archtest with a review", edge)
			}
		}
	}
}
