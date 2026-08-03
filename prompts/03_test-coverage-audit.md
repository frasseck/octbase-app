# 03 — Test-coverage audit

You are a **senior test engineer and TDD coach**. Your mission is to find out
what Octbase's test suites do *not* prove, close the gaps that matter, and leave
behind an honest map of what remains.

Read `prompts/README.md` first for ground truth and house rules.

**Derive the gap list yourself.** Do not trust any inventory — including one you
wrote last time. Enumerate the routes, branches, and views from the code, then
subtract what the suites actually exercise. A hand-maintained list of "what is
covered" is wrong within a week; the method below stays correct.

---

## The three layers

Octbase tests at three levels, each with a different job. A gap is only real
once you know which layer should have caught it.

| Layer | What it owns | How to run |
|---|---|---|
| Go integration tests | Every handler, domain rule, and repository against real Postgres | `testing` skill (needs `TEST_DATABASE_URL`) |
| JS unit tests (Vitest) | Pure logic in both SPAs and `octbase-shared/` — filtering, state helpers, i18n, the rich-text policy | `npm run test:unit` |
| Playwright/pytest e2e | Everything DOM-coupled, in both SPAs, against a seeded stack | `frontend-testing` skill (**required** — system Chrome only) |

The e2e suite drives the **built** apps, not the source tree: the desktop tests
load `dist/` over HTTP via `OCTBASE_UI_URL`, and `test_mobile.py` loads the
mobile `dist-standalone/` from `file://` plus a served `dist/` for the login
cases. Run `npm run build` before the suite, or you are testing a stale artifact.

---

## Part 1 — Measure

```bash
cd octbase-api
go test ./... -count=1 -coverprofile=cover.out    # TEST_DATABASE_URL required
go tool cover -func=cover.out | tail -1
```

Compare the total against the CI floor — read `MIN` out of
`.github/workflows/ci.yml`, and use the `coverage` skill for the per-package
breakdown. **The floor is a regression ratchet, never a target and never
negotiable downward.** If a change drops coverage, the answer is a test, not a
lower floor.

Then run the other two layers and record:

- Vitest: files, tests, failures.
- Playwright: pass count, and failures **diffed against
  `octbase-frontend/tests/KNOWN_FAILURES.md`**. A failure in that file is the
  baseline; a failure that is not is a stop — fix it or report it. Adding an
  entry to make a gate pass defeats the artifact.

Coverage percentage is the least interesting number here. Note it, then move on
to the parts it cannot see.

---

## Part 2 — Derive the real gap list

**Backend.** Enumerate every route from the chi router and cross-check against
`internal/apicontract`. For each route ask three questions:

1. Is there a happy-path test asserting **status plus response shape**?
2. Is there a test per error branch it can take — 400 / 401 / 403 / 404 / 409 /
   422 — each asserting the **stable error code**, not just the status?
3. For a project-scoped route, is there a test proving a **non-member is
   refused**? This is the branch most often missing and the one with the worst
   failure mode (see [06](06_security-assessment.md) on BOLA).

Then the same for domain and service code: every exported domain function and
service method, every state transition, every validation path, both sides of
every optimistic-locking guard (success and `VERSION_CONFLICT`).

**Frontend.** Enumerate the views from the view registry and the routes from the
router in each SPA, then map each to the spec file that exercises it. Cover
`octbase-mobile/` the same way — its views are exercised from
`octbase-frontend/tests/test_mobile.py`, which is deliberate (CI's e2e job
already runs that directory, so a mobile suite there needs no workflow change).

For every view, the bar is not "the view loads". It is: the primary flow works,
**and** the empty, error, and permission-denied states render. Happy-path-only
coverage of a view is a gap, not coverage.

**Cross-cutting.** Anything asserted by a case table in `testdata/` must be
exercised from both sides — `testdata/url-guard-cases.json` is the contract
between the Go URL guards in `sanitize.go` and their mirror in
`octbase-shared/richtext.js`, and a case that only one side reads is a hole in
the mirror.

---

## Part 3 — Close the gaps

Work highest-risk-first: security branches, then data-loss branches
(optimistic locking, cascades, migrations), then contract branches, then UI
flows.

Rules while writing tests:

- **Follow existing patterns exactly.** Go tests use `testutil.NewTestDB` +
  `NewTestServer` + `Do`, and the `MustCreate*` helpers rather than duplicated
  setup. Frontend tests use the fixtures and seed constants in
  `octbase-frontend/tests/conftest.py` — import them; never hardcode a demo
  credential or ID into a new test.
- **Do not modify production code** to make a test pass. If a test exposes a
  real bug, report it; fix it only when it is trivially a typo, and document the
  decision. A skipped test with a written reason is acceptable for a confirmed
  bug; a deleted assertion is not.
- **Assert behaviour, not implementation.** Test names describe what the system
  does. Avoid brittle selectors: prefer ids, stable classes, and
  `:has-text(...)` over positional ones.
- **No sleep-based waits** in Playwright. Wait for the selector; debounces are
  covered by waiting for the result, not by sleeping.
- **Mutating e2e tests only touch the seeded Demo Project**, and clean up after
  themselves through the reap fixture. Never delete a seeded entity — the seed
  is public surface that the UI and other tests depend on.
- **Extend `testutil` or `conftest.py` only when several new tests need it**, and
  keep the helper minimal.
- No new Go or Python dependency unless genuinely unavoidable.

---

## Quality gate

The audit is complete when:

1. The Go suite passes and total coverage is at or above the CI floor.
2. `npm run test:unit` passes.
3. The Playwright suite passes with **exactly** the known-failures baseline —
   no new failures, no new baseline entries.
4. Every route identified in Part 2 has at least one happy-path and one
   error-path test asserting the stable error code.
5. Every view in both SPAs has at least a smoke test, and every primary flow has
   its empty/error state covered.
6. No test was weakened, skipped without a reason, or deleted to get there.

---

## Deliverable

```
# Octbase Test-Coverage Audit — <date> @ <git SHA>

## Verdict: gaps closed / gaps remain

## Measurements
| Layer | Result | Gate |
|---|---|---|
| Go coverage | NN.N% | floor NN.N% (ci.yml MIN) |
| Vitest | N files / N tests | all green? |
| Playwright | N passed, N failed | vs KNOWN_FAILURES.md |

## Gap list as derived (not as previously documented)
Route / function / view → which layer should cover it → covered? → risk if it breaks

## Tests added
File, what it proves, and the branch it would catch.

## Production bugs found
Reported, not silently fixed — each with a repro.

## Remaining gaps
Each classified: blocking / acceptable debt, with the reason.
```
