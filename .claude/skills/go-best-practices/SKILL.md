---
name: go-best-practices
description: General Go engineering practices for octbase-api beyond security — stdlib-first HTTP, small interfaces, dependency budget and evaluation, middleware as function composition, avoiding framework lock-in. Use when adding or upgrading a Go dependency, designing a new package or API surface, writing HTTP handlers/middleware, or reviewing a diff for idiomatic Go.
---

# Go best practices (octbase-api)

Complements `go-security` (security invariants) and `docs/architecture.md`
(normative architecture). This skill covers general engineering judgment:
what to depend on, how big an abstraction to build, and how to keep the
HTTP layer plain.

## Stdlib-first HTTP — the standing decision

`net/http` is the default; the **only** routing dependency is
`github.com/go-chi/chi/v5`, chosen precisely because it is
`http.Handler`-native. Every handler in this codebase is a plain
`http.HandlerFunc(w http.ResponseWriter, r *http.Request)` and every
middleware is `func(http.Handler) http.Handler` — chi adds routing and
nothing else, so removing it would be a mechanical change, not a rewrite.

**Do not introduce a framework that owns its own context or handler type**
(gin, echo, fiber, iris, …). The test is simple: if a handler's signature
is not `(http.ResponseWriter, *http.Request)`, the library creates lock-in
— converting *to* it is one line, converting *away* means rewriting every
handler. Rationale (from "Gin is a very bad software library",
eblog.fly.dev/ginbad.html): such frameworks conflate routing, server
config, and rendering into one giant type; `gin.Context` alone has 133
methods and pulls a ~1M-LOC transitive tree to do what `net/http` already
does.

Existing stdlib idioms to reuse instead of importing helpers:

- Responses: `json.NewEncoder(w).Encode(...)` / `shared.WriteError` — one
  way to write JSON, not eleven.
- Middleware: wrap-and-delegate, e.g. `auth.JWTMiddleware`,
  `shared.SecurityHeaders` in `internal/shared/httpx.go`:

  ```go
  func Middleware(next http.Handler) http.Handler {
      return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
          // before
          next.ServeHTTP(w, r)
      })
  }
  ```

- Request-scoped values: `r.Context()` + typed context keys (see
  `shared.LoadUserGlobalRole`), never a framework context bag.

## Dependency budget

`octbase-api/go.mod` has ~7 direct dependencies (chi, jwt, migrate, pgx,
otp, prometheus client, x/crypto), each doing one job the stdlib doesn't.
That count is a feature. Before adding a dependency:

1. **Try the stdlib first.** The size of a solution should be proportional
   to the size of the problem — most "need a library" moments are a
   30-line helper.
2. **Read the code and docs yourself** — the actual repo, not the README
   pitch. Check how it's tested and maintained.
3. **Cost the transitive tree**, not just the direct import
   (`go mod graph`, `go build -o /tmp/bin && du -h` before/after).
   Question unconditional imports of things you won't use.
4. **Prefer fewer features.** All things being equal, choose the library
   with the smaller API surface — "the bigger the interface, the weaker
   the abstraction."
5. **Check for lock-in.** Does it interoperate through standard
   interfaces (`http.Handler`, `io.Reader`, `database/sql`/pgx), or does
   it want to own your types?
6. **Record the decision** — a short rationale (what was considered, why
   this, date) in the PR description or `docs/architecture.md` if it's
   load-bearing, so it can be re-evaluated instead of becoming folklore.

## API-surface design (our own packages)

The same skepticism applies inward — a bounded context's exported surface
is an API:

- Expose deep functionality through small interfaces; if an interface
  needs more than a handful of methods, it's probably two concerns
  (compare `gin.Engine` merging router + server + templating — the
  anti-pattern).
- One way to do each thing. Don't add a second exported helper that
  differs from an existing one only in defaults; change or wrap at the
  call site.
- Handlers return domain structs directly (structs-are-the-contract, see
  CLAUDE.md) — don't introduce DTO/mapping layers, and don't add
  indirection whose only job is to call the next layer.

## If a framework dependency ever sneaks in

Containment, not big-bang rewrite: prohibit new usage, keep new handlers
on plain `net/http`, migrate incrementally at natural seams, and enforce
the boundary in review (`go-backend-reviewer` agent).
