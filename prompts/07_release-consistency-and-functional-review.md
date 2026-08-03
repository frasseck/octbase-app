# 07 — Release consistency & functional review

You are the **senior test and quality engineer** for Octbase, and this is the
release gate. Where [01](01_master-quality-audit.md) audits the code against
itself, this review audits the **shipped product against its own promises**:

- **Documentation truthfulness** — the user guide and the style guide describe
  the product that actually ships, and the shipped UI conforms to the guide.
- **Functional verification** — "every function of the tool works" is proven by
  executing it, not by reading code.
- **Mobile parity** — `octbase-mobile/` is a first-class target, including its
  deliberate *non*-parity decisions.
- **Single source of truth** — no fact has two homes that can drift apart.

Run it before a release, after any feature that touches docs or UI, or on
request. Read `prompts/README.md` first for ground truth and house rules.

For the release mechanics themselves — changelog entry, version stamping,
merge, deploy — use the `release` and `release-readiness` skills. This prompt
decides whether the product *deserves* to be released; those skills perform the
release.

---

## Part 1 — The automated battery

Run all of it and record each result. Commands and environment come from the
skills (`testing`, `frontend-guards`, `frontend-testing`, `coverage`,
`dev-stack`, `db-migrations`) — invoke them rather than improvising, and if a
command here disagrees with a skill, the skill wins and this file is the bug.

**Backend**

```bash
cd octbase-api
go build ./... && go vet ./... && gofmt -l . && golangci-lint run ./...
go test ./... -count=1 -coverprofile=cover.out     # TEST_DATABASE_URL required
go tool cover -func=cover.out | tail -1            # vs the CI floor (ci.yml MIN)
```

The suite includes `internal/apicontract` (route↔OpenAPI parity) and
`internal/archtest` (context dependency direction). Both must be green — they
are the two checks that make a whole class of drift unexpressible.

**Frontend** — the full "Frontend checks" set via `frontend-guards`: ESLint, the
Vite build of both SPAs, `npm run typecheck`, `npm run test:unit`, the generated
API types diffing clean, `npm audit --omit=dev`, and the innerHTML, TDZ,
metrics, error-translation, audit-action, and i18n-key guards.

**Migrations** — every `NNN_*.up.sql` has its `.down` pair, and the running
stack's `/health` reports the version derived from the files. There is no
constant to bump; if the derivation and reality disagree, that is the finding.

**Stack** — a seeded dev stack answers `/health` and a demo login, per
`dev-stack`. Use `stack-health` if it does not.

Then verify by grep rather than by trust:

- **Changelog discipline** — every behaviour change in the diff under review has
  an entry under `## Unreleased`, under one of the four allowed headings
  (`Added` / `Changed` / `Fixed` / `Security`) and no others; and no entry
  describes something the code does not do. A change to `octbase-shared/`
  reaches both SPAs at once and needs an entry as much as either.
- **Cross-context imports** in `octbase-api/internal/` — each must be a
  consumer-defined interface or an exported domain type. Anything reaching into
  another context's tables or unexported internals is a finding, whether or not
  `archtest` currently catches it.

---

## Part 2 — Functional verification

1. **The Playwright/pytest suite** against a seeded stack, via
   `frontend-testing`, driving the **built** apps. Diff failures against
   `octbase-frontend/tests/KNOWN_FAILURES.md`: entries there are the baseline and
   get re-verified and re-reported with their age; **new** failures are findings.
   Never add an entry to make the gate pass.
2. **The end-to-end API scenario** — `scripts/run_agile_scenario.sh` against a
   **disposable** stack, never the shared demo stack.
3. **The feature ↔ test map.** Every chapter of
   `octbase-frontend/user-guide.html` maps to at least one passing spec file or a
   scripted probe you actually ran. Report uncovered chapters **by name**. A
   shipped feature with no spec is a blocking gap, not a footnote.
4. **The mobile walk.** Load `/m/` on the dev stack at a phone viewport with
   system Chrome and walk: login (including the MFA challenge with an
   MFA-enabled account), board, task open and edit, the property sheets, create
   task, search, notifications, settings, language switch, theme switch. No
   console errors, no raw i18n keys, no dead control.

---

## Part 3 — Documentation and style guide

**User guide** (`octbase-frontend/user-guide.html`)

- Every documented capability exists and behaves as described — spot-check
  against the live stack; the feature↔test map from Part 2 is the instrument.
- Every user-visible change since the last release is documented: walk
  `CHANGELOG.md ## Unreleased` line by line.
- No stale label, route, role, shortcut, or default. Demo credentials and
  defaults match the seed, which is public surface.
- No capability is claimed that was removed. A promise the product no longer
  keeps is worse than an undocumented feature, and this is the failure mode with
  the worst commercial consequences.

**Style guide** — two artifacts with different jobs:

- `docs/octbase-ui-styleguide.pdf` — the design-system source.
- `octbase-frontend/styleguide.html` — the **living** guide; it must document
  every component pattern the app actually ships.

Check both directions:

1. **Guide → app.** Semantic tokens only: no raw hex in the CSS outside the
   `:root` / `[data-theme]` token blocks. Icon buttons for recurring actions
   carry both an accessible name and a tooltip. Hit targets meet the minimum.
   Every view works at the narrow breakpoint. Colour is never the only signal.
2. **App → guide.** Any component class a feature introduced has a section in
   `styleguide.html`, following the existing page structure, checked in every
   shipped theme. A shipped component missing from the living guide is a finding.
3. **Both SPAs use the same tokens and components for the same job.** Flag
   desktop/mobile drift in shared patterns while respecting the deliberate scope
   differences.

---

## Part 4 — Single source of truth

- **Shared code** is canonical in `octbase-shared/` and imported by name. You
  are not checking bytes — there is one copy, so drift is not expressible. You
  are checking that no *new* shared-worthy helper bypassed the package by being
  hand-copied into both `js/` trees.
- **Docs and prompts** — one authoritative home per fact, cross-referenced
  rather than restated. Re-check the known tension class: a document restating
  an architecture, security, or process rule that `docs/architecture.md`,
  `CLAUDE.md`, or a skill now owns. Fix by cross-referencing, never by restating.
- **Locale catalogs** — keys no code path can reach, static or dynamic-prefix,
  are dead weight. List them per SPA; the mobile catalog tends to inherit
  desktop-only keys.
- **Go** — duplicated SQL, validation, or error mapping across handlers belongs
  in `shared/` or a service method; cite the `go-best-practices` skill.

---

## Operating rules specific to this review

- **Report first.** Apply fixes only for unambiguous mechanical defects — the
  typo class, and drift a guard script proves — each as a small patch with its
  check re-run. Anything needing product judgement goes to the findings list.
- **Don't re-audit what a green check already proves.** The battery exists so
  that reading time goes to docs, UI conformance, and functional gaps.
- **Respect deliberate decisions.** Non-hexagonal architecture, plain DOM, the
  mobile app's reduced scope, desktop-only management surfaces, and `beta` as the
  build-default version string are decisions. Grade against the decision record,
  not against generic best practice.

---

## Deliverable

```
# Octbase Release Consistency & Functional Review — <date> @ <git SHA>

## Verdict: PASS ✅ / FAIL ❌   (FAIL if any blocking finding is open)

## Command battery
| Check | Result |
|---|---|
| go build / vet / gofmt / golangci-lint | |
| go test (incl. apicontract + archtest) + coverage vs floor | |
| Frontend checks (ESLint, build ×2 SPAs, typecheck, unit, API types, guards) | |
| migrations ↔ /health | |
| Playwright suite (new vs known failures) | |
| agile API scenario | |
| mobile walk | |

## Findings (blocking first)
<file:line or view> — <violation> — <source of truth it contradicts> — <fix> — <proof>

## User-guide chapters without functional coverage
## Style-guide conformance gaps (both directions)
## Duplication and dead-weight inventory
## Changelog gaps
```

If blocking findings are open, the release does not go. Hand the findings to the
prompt that owns the dimension — [02](02_architecture-and-clean-code-review.md),
[03](03_test-coverage-audit.md), [04](04_frontend-quality-review.md),
[05](05_accessibility-audit.md), [06](06_security-assessment.md) — rather than
opening a parallel backlog document here. Backlog documents in this directory
are how the last set of prompts rotted.
