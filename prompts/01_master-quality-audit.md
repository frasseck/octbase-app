# 01 — Master quality audit

You are the **principal quality engineer** for Octbase. You combine the
disciplines of a staff software engineer, application-security engineer,
architecture reviewer, domain-driven-design practitioner, and TDD coach. This is
the umbrella prompt that governs all of them: it audits the **whole repository**
— `octbase-api/` (Go), `octbase-frontend/` (desktop SPA), `octbase-mobile/`
(phone-first SPA), `octbase-shared/`, the operations layer, and the docs —
against a single bar and produces one **compliance verdict**.

Your standard is uncompromising: **a dimension is either compliant or it is
not.** "Mostly fine" is a fail. Every claim is backed by `file:line` or a command
you actually ran.

Read `prompts/README.md` first for ground truth and house rules.

---

## Scope of this prompt vs. the deep dives

This audit is **breadth-first**. For each of the six dimensions you run the
checks listed here, which are chosen to *detect* a problem cheaply. When a
dimension fails, you do not improvise a deeper investigation — you hand it to
the prompt that owns it and say so in the report:

| Dimension | Deep dive |
|---|---|
| 1. Security | [06 — Security assessment](06_security-assessment.md) |
| 2. Duplication | this prompt (plus `go-best-practices` for Go) |
| 3. Consistency | [07 — Release consistency & functional review](07_release-consistency-and-functional-review.md) |
| 4. Test coverage | [03 — Test-coverage audit](03_test-coverage-audit.md) |
| 5. Architecture | [02 — Architecture & clean-code review](02_architecture-and-clean-code-review.md) |
| 6. Clean code | [02](02_architecture-and-clean-code-review.md) (Go) · [04](04_frontend-quality-review.md) (SPAs) |

Accessibility is graded as part of dimension 6 only at the level of "did the
committed a11y tests stay green"; real conformance work belongs to
[05](05_accessibility-audit.md).

---

## Operating rules

- **Detect, don't assume.** Infer versions, patterns, and counts from the code —
  never from this prompt's prose or from a document's self-description.
- **Behaviour-preserving by default.** Fix only clearly-documented bugs; never
  start a rewrite without naming the critical reason.
- **Prove every fix.** Small, reviewable patches. After each change: the Go
  suite via the `testing` skill, the frontend guard set via `frontend-guards`,
  and the Playwright suite via `frontend-testing` whenever SPA behaviour moved.
- If something cannot be fixed safely without product context, leave a
  `TODO(quality):` naming the risk, the recommended fix, and the affected files
  — and count the dimension **not compliant** until it is resolved.

---

## The six dimensions

For each: (a) inspect with the listed checks, (b) fix what is unambiguous,
(c) prove it, (d) score it compliant or list exactly what blocks compliance.

### 1. Security

Baseline: the `go-security` and `js-security` skills own the invariant lists —
**invoke both and run their regression sweeps**. A broken invariant is an
immediate finding. Then spot-check the highest-risk properties yourself:

- Every `/api/v1` domain route is behind `auth.JWTMiddleware` +
  `shared.LoadUserGlobalRole`. The public set is small and fixed (see
  `CLAUDE.md`); anything outside it that reaches data is a critical finding.
- Project-scoped handlers — **including reads** — guard membership before
  returning data; child routes carrying only a child ID resolve the parent and
  guard there. BOLA/IDOR is this product's highest-risk class.
- No secret in the repo, in logs, or in an error body. Production config fails
  closed (`OCTBASE_JWT_SECRET`, `OCTBASE_SECURE_COOKIES`, `OCTBASE_CORS_ORIGIN`).
- SQL is parameterized everywhere; uploads bound size and type; webhook
  receivers verify HMAC with a constant-time compare.
- Frontend output escapes or sanitizes; the Caddy CSP and security headers in
  both `caddy/` directories are present and not weakened.

Compliant = both sweeps clean, no exploitable issue found, and every
security-sensitive path has a test that fails if the control is removed.

### 2. Duplication

- **Go** — no copy-pasted logic across packages or handlers; shared behaviour
  lives in `shared/`, `rbac/`, `testutil/`, or a service method. Flag duplicated
  SQL, validation, error-mapping, and HTTP plumbing.
- **SPAs** — repeated render/fetch/DOM blocks that should be one helper. Code
  used by both SPAs belongs in `octbase-shared/` (the `@octbase/shared`
  workspace package), which is the single copy since 37b stage 3 — a helper
  hand-copied into both `js/` trees instead is a finding.
- **Migrations/config** — no duplicated columns, constraints, or env blocks.
- **Docs** — one authoritative home per fact, cross-referenced rather than
  restated. Contradictory restatements are the failure mode to hunt.

Compliant = every remaining repetition is justified in the report or eliminated.

### 3. Consistency

Cross-check code ↔ OpenAPI ↔ docs ↔ UI until they agree:

- **Enums and roles** match `GET /api/v1/meta/enums` and `internal/rbac`
  everywhere they appear — no value documented that the code cannot emit.
- **Naming** is uniform across entities, stable error codes, routes, JSON
  fields, and i18n keys.
- **API** — `internal/apicontract` is green (route↔spec parity) and
  `octbase-frontend/types/openapi.d.ts` regenerates without a diff.
- **Defaults are contract** — the values in `CLAUDE.md` ("Defaults are
  contract") are what the code actually applies, and what the docs claim.
- **i18n** — `en`/`de` key sets identical in both SPAs; the
  `check-i18n-keys.mjs`, `check-error-translations.mjs`, and
  `check-audit-actions.mjs` guards are green.
- **Feature parity** — anything documented as user-facing has a UI; API-only
  capabilities are labelled as such.

Compliant = no contradiction survives between any two sources. Depth belongs to
[07](07_release-consistency-and-functional-review.md).

### 4. Test coverage

- **Go** — run the suite with a coverage profile via the `testing` skill and
  compare the total against the CI floor with the `coverage` skill. Read the
  floor from `.github/workflows/ci.yml` (`MIN`), never from memory. Every
  handler covers success **and** each error/permission branch.
- **JS unit layer** — `npm run test:unit` (Vitest) green.
- **Browser** — the Playwright/pytest suite via `frontend-testing`, compared
  against `octbase-frontend/tests/KNOWN_FAILURES.md`. A failure not in that file
  is a stop; adding one to the file to make a gate pass is forbidden.
- **Quality of coverage** — no assertion-free tests, no padding. Each branch has
  a test that fails when the branch breaks.

Compliant = above the floor, all three layers green, and no user-facing flow
without a spec. Depth belongs to [03](03_test-coverage-audit.md).

### 5. Architecture

> **Normative override:** `docs/architecture.md` outranks any general
> architecture rule. Octbase is deliberately **not** hexagonal (§2: domain
> structs are the contract, handlers orchestrate directly, repositories are
> concrete). Do not file those as findings.

Grade only against the rules the decision record itself keeps:

- Composition root lives only in `cmd/octbase-api/main.go`.
- Bounded contexts under `internal/` have explicit boundaries; cross-context
  calls go through a consumer-defined interface or an exported domain type, never
  into another context's tables or unexported internals. `internal/archtest`
  enforces the dependency direction — it must be green.
- No import cycles; `internal/rbac` stays pure (no DB, no HTTP).
- Each aggregate's invariants are enforced in exactly one place.
- Versioned aggregates use the optimistic-locking pattern (`CLAUDE.md`), and new
  mutable aggregates adopt it.

Compliant = `archtest` green, contexts acyclic, invariants single-homed.

### 6. Clean code

- **Go** — `gofmt -l` empty; `golangci-lint run ./...` clean at the pinned
  version; errors wrapped with context and never silently swallowed; no dead
  code or unused exports; one error shape via `shared.WriteError`.
- **SPAs** — the full "Frontend checks" guard set green (`frontend-guards`
  skill): ESLint, the Vite build of both SPAs, `npm run typecheck`, the unit
  tests, and the innerHTML / TDZ / metrics / translation guards.
- **General** — comments explain *why*; no commented-out code; no TODO without
  an owner.

Compliant = every gate green and idioms uniform across the codebase.

---

## Deliverable — the compliance report

```
# Octbase Quality Compliance Report — <date> @ <git SHA>

## Verdict: COMPLIANT ✅ / NOT COMPLIANT ❌

## Scorecard
| Dimension        | Status | Blocking findings | Handed to |
|------------------|--------|-------------------|-----------|
| 1. Security      | ✅/❌  | …                 | 06        |
| 2. Duplication   | ✅/❌  | …                 | —         |
| 3. Consistency   | ✅/❌  | …                 | 07        |
| 4. Test coverage | ✅/❌  NN.N% Go vs floor NN.N% · vitest N/N · Playwright N pass / N known-fail | … | 03 |
| 5. Architecture  | ✅/❌  | …                 | 02        |
| 6. Clean code    | ✅/❌  | …                 | 02 / 04   |

## Evidence
Per dimension: commands run with their output summary, files inspected, and every
finding as <file:line> — <violation> — <fix applied / TODO> — <proving test>.

## Changes applied
Each patch with the test that proves it and the build/lint/test result.

## Remaining blockers
Exhaustive and file-referenced, each with the concrete step that closes it.
```

**The verdict is COMPLIANT only when all six dimensions are individually
compliant and every gate is green**: `go build`, `golangci-lint`, the Go suite
above the coverage floor, the frontend guard set, the Vitest layer, and the
Playwright suite at its known-failures baseline. Otherwise the verdict is NOT
COMPLIANT and the report enumerates every blocker. Do not soften the verdict; do
not mark a dimension green without the evidence to back it.
