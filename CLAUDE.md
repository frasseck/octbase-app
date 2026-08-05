# Octbase — repository instructions

Project management tool (a Jira replacement with its own wiki pages and
GitHub/GitLab/Bitbucket integration), deployed as
one stack per client. A split monorepo: a Go API plus two static
frontends built from ES modules by Vite (the no-build stance was retired on
2026-07-30 — see §5.2 of the architecture doc). The architectural style (modular monolith, strategic DDD,
deliberately **not** hexagonal) and the conditions to revisit it are normative
in `docs/architecture.md` — hold changes against it.

## Goal-driven execution

Define success criteria before starting non-trivial work; loop until verified
rather than declaring done from a gut feel.

Transform tasks into verifiable goals:
- "Add validation" → "Write tests for invalid inputs, then make them pass"
- "Fix the bug" → "Write a test that reproduces it, then make it pass"
- "Refactor X" → "Ensure tests pass before and after"

For multi-step tasks, state a brief plan:
```
1. [Step] → verify: [check]
2. [Step] → verify: [check]
3. [Step] → verify: [check]
```

## Skills — use these

| Skill | When |
|---|---|
| `dev-stack` | Bring up / locate a seeded API or stack; ports, demo logins, disposable stacks |
| `run-octbase` | Drive the running app end-to-end: curl smoke tests, `driver.py` UI screenshots (desktop + mobile), gotchas |
| `testing` | Run Go backend tests and the Playwright/pytest frontend suite |
| `frontend-testing` | **Required** before running, screenshotting, or visually verifying any frontend — covers browser/venv setup and gotchas |
| `db-migrations` | Add or run PostgreSQL migrations; auto-derived health-check version; seed impact |
| `coverage` | Check the Go coverage floor before pushing |
| `i18n` | Add/change translation strings across the static frontends |
| `frontend-guards` | Before pushing frontend/shared-JS changes — the "Frontend checks" CI checks (ESLint, the Vite build, unit tests, module TDZ hazards, innerHTML, metrics-not-proxied, error/audit-action/i18n-key translations) |
| `go-security` | Security-review a backend diff or new auth/crypto/file/webhook code path against the established invariants |
| `go-best-practices` | Adding/upgrading a Go dependency, designing a package or API surface, writing handlers/middleware — stdlib-first HTTP, dependency budget, no framework lock-in |
| `js-security` | Security-review a frontend/shared-JS diff or new render/URL/token-handling code against the browser-side invariants (XSS escaping, DOMPurify policy, token storage) |
| `stack-health` | Diagnose an unhealthy/degraded stack; post-deploy health gate; reaction runbook |
| `release` | Release a `release_vN` branch: changelog entry, version stamping, merge to main, demo deploy |

A `go-backend-reviewer` agent reviews `octbase-api/` diffs for API-contract and
convention regressions.

> Always invoke `frontend-testing` before running, screenshotting, or visually
> verifying `octbase-frontend` or `octbase-mobile`.

## Repository layout

| Path | Purpose |
|---|---|
| `octbase-frontend/` | Plain-DOM HTML/CSS/JS app (ES modules, built by Vite) + Caddy front door; serves `/m/` mobile and reverse-proxies `/api` |
| `octbase-mobile/` | Phone-first static SPA, served under `/m/` by the frontend |
| `octbase-shared/` | The `@octbase/shared` workspace package — JS imported by both SPAs (`i18n.js`, `meta.js`, `richtext.js`). One copy since 37b stage 3, so the old byte-identical sync + drift guard are gone; the two vendored libraries that used to live here became the pinned `dompurify` / `qrcode-generator` npm deps at stage 4 |
| `octbase-operations/` | Health-observation layer: `check-health.sh` (whole-stack probe) + reaction runbook, plus the fleet repair tooling (`stamp-baseline.sh`, `repair-039-poststamp.sql`) for instances that will not start after a migration history rewrite |
| `testdata/` | Case tables shared by the Go and JS test suites — today `url-guard-cases.json`, the contract between `sanitize.go`'s URL guards and their `octbase-shared/richtext.js` mirror |
| `scripts/` | Repo-level tooling: the TDZ/innerHTML/vendor-integrity guards, classic-asset filename hashing (`vite-hash-classic-assets.mjs` — the `?v=` stamping retired at 37b stage 5), DB reset, the end-to-end agile API scenario |
| `podman-compose.dev.yml` | Dev-only overlay adding Mailpit mail capture; layer with `-f` for local stacks |
| `docs/architecture.md` | **Normative** architecture decisions (style, concurrency model, scaling stance) |
| `docs/operations.md` | Production runbook |
| `docs/hosting-concept.md` | Deployment topology, sizing, multi-client scaling models |
| `docs/technical_documentation.md` | Whole-stack technology reference (services, containers, networking, DNS/TLS) |

The public marketing/landing site is a **separate website** in its own repository
(`ocete.ch`) — it is not part of this repo.

## Build & test commands

```bash
# Go API
cd octbase-api
go test ./...                         # needs TEST_DATABASE_URL (Postgres); see `testing` skill

# Run the API locally (auto-migrates; seeds when demo mode on). Run from octbase-api/.
OCTBASE_DEMO_MODE=true \
OCTBASE_DATABASE_URL="postgres://octbase:octbase@localhost:5432/octbase?sslmode=disable" \
go run ./cmd/octbase-api
```

Frontend end-to-end tests and screenshots have their own setup — **see the
`testing` and `frontend-testing` skills** (system Chrome only; Playwright's
bundled chromium/firefox do not install on this OS).

## High-level architecture

- `octbase-api/cmd/octbase-api/main.go` is the composition root. It serves only
  the API docs (`web/docs.html`, `openapi.yaml`, the swagger-ui assets) —
  **not** the app UI.
- The backend is a **modular monolith** by bounded context under `internal/` —
  one package per context, plus the test-only `apicontract` (route↔OpenAPI
  parity) and `archtest` (core/module dependency-direction) checks.
- The **app frontend is served by its own Caddy container** (`octbase-frontend`),
  which is the front door: it reverse-proxies `/api` to the API and serves the
  mobile SPA under `/m/`. It is **not** mirrored into `octbase-api/web/`.
- The `octbase-frontend` app is plain DOM (a small fetch wrapper, a `window.S`
  state object, per-view render functions; no framework) built from **ES modules
  by Vite** since 37b stage 2 — one `<script type="module">` entry, per-file
  `import`/`export`, **no load-order contract any more**; see
  `octbase-frontend/js/README.md`. `octbase-mobile` is converted the same way
  (`js/app.js` imports `js/core.js`). Each SPA also builds a second
  **self-contained IIFE bundle** for the `file://` standalone demo
  (`npm run build:standalone` → `dist-standalone/`), because a browser refuses
  `import` from a `file://` origin. The Playwright suite drives the desktop SPA
  against the built `dist/` over HTTP via `OCTBASE_UI_URL`, and the mobile tests
  load the mobile `dist-standalone/` from `file://`.

## Key conventions

- **Backend contract conventions** — auth, error shape, structs-as-DTOs,
  optimistic locking, activity logging, integration-style tests, migrations —
  live in `octbase-api/CLAUDE.md`, loaded when you work under that directory.
- **Defaults are contract:** projects → `PRIVATE`, `estimationUnit: NONE`
  (effort estimation is opt-in per project; while it is `NONE` no estimate field
  appears anywhere in that project's UI), `boardLaneLimit: 20` (how many cards a
  board lane draws at once, the rest loading on scroll — display only, so the
  lane's count badge still reports the full size; `0` means draw every card);
  tasks → `TASK`/`PLANNED`/`MEDIUM` with
  `storyPoints`/`estimateHours` `null` (**`null` means *unestimated* and is
  distinct from a deliberate `0`**); memberships → `PROJECT_MEMBER`
  (`RoleDeveloper` is a back-compat alias); repo connections → provider
  `FAKE_GITLAB`, branch `main`; task branches → `feature`.
- **Seed data is public surface:** `internal/seed/seed.go` fixed IDs, titles, the
  four canonical board columns (`Planned`/`In Progress`/`Review`/`Done`), the
  demo page and repo/branch are depended on by UI and tests. Changing seed means
  updating tests and UI too.
- **Frontend stays plain DOM:** reuse existing helpers (`http`, `api`, `esc`,
  `toast`, modal helpers, the shared `S` state) — no framework, no JSX, no
  client state library. This part is not up for revision.
  **The "no bundler, no npm dependency graph" half was retired on 2026-07-30**
  (decision record: `docs/architecture.md` §5.2) and both SPAs are migrating to
  ES modules built by Vite, in stages. Until a stage lands, the code it covers
  is still classic scripts — read §5 for what is true *today* rather than
  assuming either end state, and add an npm dependency only as part of a stage,
  never incidentally.
- **`CHANGELOG.md` tracks core changes:** any change to the behavior of
  `octbase-api/`, `octbase-frontend/`, `octbase-mobile/` or `octbase-shared/`
  (new/changed endpoints, error codes, defaults, migrations, user-visible UI or
  copy) gets an entry under `## Unreleased` in the same PR/commit that makes the
  change. All four are named deliberately: the rule used to say "api or
  frontend", and three commits titled *text change* rewrote user-visible mobile
  strings while staying technically compliant with it. A change to
  `octbase-shared/` reaches both SPAs at once, which makes it more visible than
  either, not less. **The headings are `Added` / `Changed` / `Fixed` /
  `Security`, and only those** — one-off headings (`Tests`, `Release`,
  `Changed / internal`) had drifted in and were folded back in 2026-07-31; a
  test or a version bump is a `Changed`.
  Release mechanics — renaming `Unreleased`, version numbering, and
  `OCTBASE_APP_VERSION` stamping (never `defaultAppVersion`) — live in the
  `release` skill.
