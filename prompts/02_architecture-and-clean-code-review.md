# 02 — Architecture & clean-code review (backend)

You are a **professional code reviewer** — the kind a team hires for a paid
external review before a launch. Your subject is `octbase-api/`: its bounded
contexts, its domain rules, its handlers, and the Go itself. The deliverable is a
written review, not a refactor.

Read `prompts/README.md` first for ground truth and house rules. The frontend
equivalent of this prompt is [04](04_frontend-quality-review.md); run that one
for anything under `octbase-frontend/`, `octbase-mobile/`, or `octbase-shared/`.

---

## The single most important rule

**`docs/architecture.md` is normative and outranks every general principle you
know.** Octbase is a modular monolith practising *strategic* DDD and is
**deliberately not hexagonal** (§2). The following are decisions, not defects,
and filing them as findings is itself a review error:

- Domain structs are the API contract; there is little DTO mapping.
- Handlers orchestrate directly; there is no use-case/interactor layer.
- Repositories are concrete types, not interfaces behind ports.
- The SPAs are plain DOM with no framework and no client state library.

What the decision record *does* keep, and what you therefore grade:

1. The composition root exists only in `cmd/octbase-api/main.go`.
2. Bounded contexts under `internal/` have explicit boundaries. A cross-context
   dependency is legitimate only as a **consumer-defined interface** or a use of
   another context's exported domain type — never a reach into its tables,
   its repo, or its unexported internals.
3. Invariants live in one place per aggregate (domain helper or service), not
   scattered across handlers.
4. Stable error codes are an executable contract (tests assert them exactly).
5. Optimistic locking is the concurrency model for versioned aggregates (§3).
6. The §"When to revisit" triggers — if you believe the code has hit one, that
   is a finding worth raising, but it is a *decision request*, not a defect.

`internal/archtest` encodes rule 2 mechanically. Run it, and treat a gap between
what it enforces and what §1–2 say as a finding about the test.

---

## Part A — Inspect before judging

Produce a short structural assessment first. It is the map the rest of the review
is written against, and writing it usually surfaces half the findings:

1. **Context map** — every package under `internal/`, one line on what it owns,
   and every edge between them. Derive the edges from imports (`go list`), not
   from intent. Mark each edge as interface / exported-domain-type / violation.
2. **Aggregates and their invariants** — Project, Task, Board, Release, Sprint,
   Page, and any newer one. For each: where the invariant is enforced, whether
   more than one place enforces it, and whether it is version-guarded.
3. **Error-code inventory** — every stable code the API can emit, where it is
   written, and whether a test asserts it. Codes are public surface; an
   unreferenced one is dead contract and a duplicated one is a naming defect.
4. **High-risk files** — the largest handlers, the widest structs, anything with
   mixed responsibilities or an unusual amount of recent churn.

---

## Part B — Review each package

For every package in `internal/`, review for:

**Correctness**
- Errors checked, wrapped with context (`fmt.Errorf("...: %w", err)`), never
  silently swallowed; sentinel errors compared with `errors.Is`.
- Every mutation of a versioned aggregate goes through the version-guarded path
  and maps a zero-row update to `409 VERSION_CONFLICT` via
  `shared.WriteUpdateError`.
- Transactions wrap multi-row writes; no partial state survives a failure.
- `activity.Write(...)` is called for user-visible state changes that belong in
  the Activity view — it is an explicit call, not a trigger, so absence is easy
  to miss and impossible to see from the schema.
- No goroutine leaks, no unbounded work per request, context propagated to every
  query and outbound call.

**Contract**
- Handler responses match `openapi.yaml`; a changed field or JSON tag changes the
  API immediately, because structs are the contract.
- Error bodies always go through `shared.WriteError`, with the stable code.
- Defaults applied by handlers match the documented defaults in `CLAUDE.md`.
- Mass assignment: can a client set a field it should not — role, owner,
  identifiers, `version` — by including it in a PATCH body?

**Clean Go** (baseline: the `go-best-practices` skill)
- Small, single-purpose functions; names that reveal intent; no vague `data` /
  `item` / `manager` / `utils` where a domain word exists.
- Magic strings and numbers replaced by domain constants.
- Boilerplate that repeats more than a handful of times is a helper, not a
  pattern to copy — permission-guard and error-mapping preambles are the usual
  offenders.
- Dependency budget: stdlib-first HTTP, no framework lock-in, and any new
  dependency justified against the skill's evaluation bar.
- No dead code, no unused exports, no commented-out blocks, no TODO without an
  owner.

**Tests as design evidence**
- Tests are integration-style against real Postgres via `internal/testutil` —
  new tests follow that pattern rather than mocking.
- Setup goes through the `testutil` helper constructors instead of duplicating
  fixtures.
- A test that asserts only a status code is a coverage number, not a test:
  the contract is status **plus** error code (failures) or key fields
  (successes).

---

## Part C — Verify, then report

Run these and record the result of each (commands and environment come from the
`testing`, `coverage`, and `db-migrations` skills — do not improvise them):

```bash
cd octbase-api
go build ./... && go vet ./... && gofmt -l .
golangci-lint run ./...          # pinned version, see .github/workflows/ci.yml
go test ./... -coverprofile=cover.out     # needs TEST_DATABASE_URL
go tool cover -func=cover.out | tail -1   # compare against the CI floor
go test ./internal/archtest ./internal/apicontract
```

Also confirm, with greps rather than trust:

- Every migration has a matching `.up`/`.down` pair, and the health check's
  expected version derives from the files (`shared.LatestMigrationVersion`) —
  there is no constant to bump.
- Every behaviour change in the diff under review has a `CHANGELOG.md`
  `## Unreleased` entry under one of the four allowed headings (`Added`,
  `Changed`, `Fixed`, `Security`), and no entry describes something the code
  does not do.

---

## Deliverable

A written review, ordered by severity, that a maintainer could act on without
opening a single file first:

```
# Octbase Backend Review — <date> @ <git SHA>

## Verdict and headline risks (≤ 10 lines)

## Context map
Table of packages, their edges, and any edge that is not interface-or-domain-type.

## Findings (blocking first)
<file:line> — <what is wrong> — <which rule/source of truth it breaks> —
<concrete fix, naming the helper or pattern to follow> — <how to prove it>

## Aggregate invariant table
| Aggregate | Invariant | Enforced in | Version-guarded | Test |

## Checks run
| Check | Result |

## Applied fixes
Only the small, safe, high-confidence ones — each with its proving test.

## Decision requests
Anything that needs a human call, including any `docs/architecture.md`
"when to revisit" trigger you believe has been hit.
```

Only fix what is small, safe, and unambiguous; everything larger goes in the
report. Be critical and specific — this codebase is meant to be maintained for
years, and a review that finds nothing is usually a review that read nothing.
