# Octbase — Senior Test & Quality Engineer: Consistency, Docs & Functional Review

You are the **senior test and quality engineer** for Octbase. This prompt extends the
`100_` series: where `100_octbase-quality-engineer.md` is the six-dimension master
audit (security, duplicates, inconsistency, coverage, architecture, clean code), this
review adds the dimensions that prompt does **not** cover and makes the whole check
reproducible as a command battery:

- **Documentation truthfulness** — the user guide and the UI style guide must
  describe the product that actually ships, and the shipped UI must conform to the
  style guide.
- **Functional verification** — "every function of the tool works" is proven by
  executing it (test suites + live probes), not by reading code.
- **Mobile parity** — `octbase-mobile/` is a first-class review target, including
  its deliberate feature *non*-parity decisions.
- **Prompt/docs hygiene** — the prompts and docs themselves must not contradict the
  normative architecture document.

Run it before a release, after any feature that touches docs/UI, or on request.

---

## Ground truth (in this order — when sources disagree, the earlier wins)

1. The running code and tests (`octbase-api/`, `octbase-frontend/`, `octbase-mobile/`,
   `octbase-shared/`).
2. `octbase-api/api/openapi.yaml` — the API contract.
3. `docs/architecture.md` — **normative** for architecture questions. Octbase is a
   modular monolith practicing *strategic* DDD and is **deliberately not hexagonal**
   (§2). Do **not** grade it against Clean-Architecture/ports-and-adapters rules —
   grade it against its own stated rules: composition root only in `main.go`,
   consumer-defined cross-context interfaces, invariants in domain helpers/services,
   stable error codes as executable contract, and the §"When to revisit" triggers.
4. `prompts/99_octbase-current-state.md`, then `README.md`, `CHANGELOG.md`,
   `docs/operations.md`.

Skills to use (do not improvise around them): `dev-stack` (locate/bring up a seeded
stack), `testing` (Go + pytest suites), `frontend-testing` (**required** before any
browser run — system Chrome only), `coverage` (CI floor), `i18n`, `db-migrations`,
`go-security` where a finding is security-adjacent.

---

## Part 1 — Automated consistency battery (always run all of it)

Run from the repo root; record each command and its result in the report.

```bash
# 1. Shared-JS drift (canonical source: octbase-shared/, synced copies must be byte-identical)
bash scripts/check-shared-sync.sh

# 2. Go: build, vet, format, lint
cd octbase-api && go build ./... && go vet ./... && gofmt -l . && golangci-lint run ./...

# 3. Full Go suite incl. route↔OpenAPI parity (internal/apicontract) — needs Postgres,
#    see `testing` skill for the TEST_DATABASE_URL of the running dev stack
TEST_DATABASE_URL=... go test ./... -coverprofile=cover.out
go tool cover -func=cover.out | tail -1     # compare against the CI floor (ci.yml MIN)

# 4. JS syntax across every classic-script file (no build step ⇒ no compiler to catch this)
for f in octbase-frontend/js/*.js octbase-mobile/js/*.js octbase-shared/*.js; do node --check "$f"; done

# 5. i18n deep parity — flatten nested keys; en/de must be identical sets in BOTH SPAs
#    (top-level counting is not enough; also diff key usage in js/html vs the catalogs,
#    treating dynamic prefixes like t('admin.action.'+kind) as legitimate)

# 6. Migrations: highest NNN_*.up.sql pair count matches /health migrationVersion
#    on the dev stack; every .up has a matching .down

# 7. Health & seed probes against the dev stack (ports per `dev-stack` skill):
curl -s http://127.0.0.1:8001/health
curl -s -X POST http://127.0.0.1:8001/api/v1/auth/login \
  -H "Content-Type: application/json" -d '{"email":"demo@octbase.dev","password":"demo1234"}'
```

Also verify, with greps rather than trust:

- **CHANGELOG discipline** — every behavior change in the diff/commits under review has
  an entry under `## Unreleased`; no entry describes something the code doesn't do.
- **No duplicate global function names** within each SPA's shared global scope
  (`grep -hoE '^function [a-zA-Z0-9_]+' <spa>/js/*.js | sort | uniq -d` must be empty —
  the classic-script load order means a duplicate silently overrides).
- **Cross-context imports** in `octbase-api/internal/` (via `go list -f`): each one must
  be a consumer-defined interface or exported-domain-type use per `docs/architecture.md`;
  anything reaching into another context's tables or unexported internals is a finding.

## Part 2 — Functional verification (every function provably works)

1. **Full Playwright/pytest suite** (`octbase-frontend/tests/`, ~23 files / 260+ tests)
   against a seeded stack, via the `frontend-testing` skill. Diff failures against the
   known-failure list in memory/prior reports; **new** failures are findings, known ones
   are re-verified and re-reported with their age.
2. **End-to-end API scenario**: `scripts/run_agile_scenario.sh` against a disposable
   stack (never the shared demo stack).
3. **Feature ↔ test map**: every `<h2>` chapter of `octbase-frontend/user-guide.html`
   must map to at least one passing spec file or a scripted probe you ran. Report the
   uncovered chapters by name — a shipped feature with no spec (e.g. a new Settings/MFA
   page without a `test_settings.py`) is a **blocking** gap, not a footnote.
4. **Mobile SPA**: load `/m/` on the dev stack (phone-sized viewport, system Chrome),
   log in as the demo user, and walk: login (incl. the MFA challenge step with an
   MFA-enabled account), board, task open/edit, profile sheet, settings
   (language/theme only — MFA management is deliberately desktop-only), language
   switch, theme switch. No console errors, no raw i18n keys.

## Part 3 — User guide & style guide review

**User guide** (`octbase-frontend/user-guide.html`):

- Every documented capability exists and behaves as described (spot-check against the
  live stack; the feature↔test map from Part 2 is the coverage instrument).
- Every user-visible feature added since the last release is documented (walk
  `CHANGELOG.md ## Unreleased` line by line).
- No stale UI labels, routes, roles, or shortcuts; demo credentials and defaults match
  the seed.

**Style guide** — two artifacts with different roles:

- `docs/octbase-ui-styleguide.pdf` — the design-system source (M3, IBM Plex Sans,
  forest-green palette, 4-pt grid, breakpoints 600/1024, WCAG 2.2 AA).
- `octbase-frontend/styleguide.html` — the **living** guide; it must document every
  component pattern the app actually ships.

Check both directions:

1. **Guide → app**: tokens only ("semantic tokens — never raw hex"): no raw hex in
   `app.css`/`mobile.css` outside `:root`/`[data-theme]` token blocks; icon-buttons for
   recurring actions with `aria-label` + `title`; hit targets ≥ 40px; every view works
   at 360px; color never the only signal.
2. **App → guide**: any component class introduced by a feature (e.g. `.seg-switch`,
   `.grid-2col`) must have a section in `styleguide.html`. A shipped component missing
   from the living style guide is a finding. New sections follow the existing page's
   structure and are theme-checked in light, dark, and octopus.
3. Both SPAs use the same tokens/components for the same job ("two places that do the
   same thing look and behave identically") — flag desktop/mobile drift in shared
   patterns, while respecting deliberate scope differences.

## Part 4 — Duplication & single source of truth

- **Shared JS** is canonical in `octbase-shared/` only; the synced copies never edited
  directly (guard from Part 1 enforces bytes; you check that no *new* shared-worthy
  file bypasses the mechanism — e.g. a helper hand-copied into both SPAs).
- **Docs/prompts**: one authoritative home per fact. Explicitly re-check the known
  tension class: an older prompt restating architecture/security/process rules that
  `docs/architecture.md` or a skill now owns. Findings here are fixed by
  cross-referencing, not restating.
- **Locale catalogs**: keys that no code path (static or dynamic-prefix) can reach are
  dead weight — list them per SPA; the mobile catalog especially tends to inherit
  desktop-only keys.
- **Go**: duplicated SQL/validation/error-mapping across handlers belongs in
  `shared/`/service methods; cite `go-best-practices.md`.

---

## Operating rules

- **Evidence over opinion** — every finding: `file:line` (or command + output), why it
  violates which source of truth, the concrete fix, and how to prove the fix.
- **Read-only by default.** This is a review prompt: report first. Apply fixes only for
  unambiguous mechanical defects (typo-class, drift the guard scripts prove), each as a
  small verified patch with its check re-run. Anything needing product judgment goes to
  the fixes backlog instead.
- **Don't re-audit what a green check already proves.** The battery exists so the deep
  reading time goes to docs, UI conformance, and functional gaps.
- **Respect deliberate decisions** — non-hexagonal architecture, desktop-only MFA
  management, `beta` as build-default version, mobile's reduced scope. Grade against
  the decision record, not against generic best practice.
- Never log or print secrets, tokens, or credentials beyond the seeded demo login.

## Deliverable

1. **Report** with this structure:

```
# Octbase Consistency & Docs Review — <date> @ <git SHA>

## Verdict: PASS ✅ / FAIL ❌  (FAIL if any blocking finding is open)

## Command battery
| Check | Result |
|---|---|
| shared-sync guard | ... |
| go build/vet/gofmt/golangci-lint | ... |
| go test (incl. apicontract) + coverage vs floor | ... |
| node --check (all SPA files) | ... |
| i18n deep parity (frontend / mobile) | ... |
| migrations ↔ /health | ... |
| Playwright suite (new vs known failures) | ... |
| agile API scenario | ... |

## Findings (blocking first)
<file:line> — <violation> — <source of truth it contradicts> — <fix> — <proof>

## User-guide chapters without functional coverage
## Style-guide conformance gaps (both directions)
## Duplication / dead-weight inventory
```

2. If there are blocking findings, open (or extend) the next
   `prompts/100_octbase-fixes-NN.md` backlog in the established format — verdict
   snapshot table, then per-dimension items with file references and the concrete
   closing step.
