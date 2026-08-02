# Octbase — Quality Fixes Plan 02 (deep review follow-up)

> Review run: **2026-06-25** (deep code/design/presentation review of the whole repo).
> This document is the actionable backlog from that review. Unlike `fixes-01`
> (which was test-coverage driven), this round targets **production-blocking bugs,
> security hardening, and the largest maintainability/presentation gaps**.
>
> Items marked **[DONE]** were implemented in this session and verified with
> `go build ./...`, `go vet ./...`, and the no-DB Go test packages. Items marked
> **[DEFERRED]** are larger frontend changes that require the Playwright/`frontend-testing`
> harness to verify safely and are intentionally left as a planned backlog.

---

## Verdict snapshot

The codebase is genuinely well-engineered: a clean modular-monolith Go API
(parameterized SQL, RBAC, refresh-token rotation, HMAC webhooks with constant-time
compare, defense-in-depth HTML sanitization on client **and** server, slog,
Prometheus, graceful shutdown) and an unusually disciplined no-build SPA
(auto-escaping `html\`\`` template, full event delegation, mirrored client sanitizer).
`go build` and `go vet` are clean and the test suite is large.

The fixes below are hardening and polish, not rescue — but two of them are true
production blockers.

| Severity | Item | Status |
|---|---|---|
| 🔴 Critical | Health check stuck at `expectedMigrationVersion = 15` (16 migrations exist) → permanent 503 | **[DONE]** |
| 🔴 Critical | App boots with a default JWT secret on a mere warning | **[DONE]** |
| 🟠 High | Login timing side-channel enables email enumeration | **[DONE]** |
| 🟠 High | `podman-compose.yml` ships a hardcoded JWT secret | **[DONE]** |
| 🟢 Low | `cover*.out` artifacts clutter the working tree | **[N/A]** already in `.gitignore` |
| 🟠 High | `js/app.js` is one 5,866-line file | **[DONE]** split into 14 classic-script files |
| 🟠 High | `i18n.js` + meta/icon maps duplicated across frontends | **[DEFERRED]** |
| 🟡 Med | No dark mode despite full M3 token plumbing | **[DEFERRED]** |
| 🟡 Med | Manual asset cache-busting (`?v=…`) | **[DEFERRED]** |

> Correction vs. the verbal review: mobile is **not** "missing `fr.json`" — French is
> *intentionally* disabled in `octbase-mobile/js/i18n.js` (`AVAILABLE_LOCALES = ['en','de']`).
> No change made there.
>
> Verified-good (no change needed): webhook receivers reject empty-secret with 403 and
> use `hmac.Equal` (constant-time); SQL is parameterized with whitelisted sort columns.

---

## 🔴 1. Health check / migration version — **[DONE]**

**Bug.** `cmd/octbase-api/main.go` hardcoded `expectedMigrationVersion = 15`, but the
repo has 16 migrations (`016_release_activity_rename`). After migrations run the DB is at
version 16, so `healthHandler` evaluated `version != expectedMigrationVersion` as true and
returned `503 degraded` on **every** fully-migrated deployment. Orchestrators and load
balancers would treat the service as unhealthy. This is the second time the file moved
without the constant being bumped, so the fix removes the hand-maintained constant entirely.

**Fix.**
- Added `shared.LatestMigrationVersion(migrationsPath)` — scans `*.up.sql` filenames for
  the highest `NNN_` numeric prefix. The expected version is now **derived from the
  migration files**, so it can never drift again.
- `main.go` computes `expectedMigrationVersion` once at startup and passes it into
  `healthHandler`; the hardcoded const is gone.
- Added a unit test for `LatestMigrationVersion` (no DB required).

## 🔴 2. JWT secret fail-fast — **[DONE]**

**Risk.** `main.go`/`auth.jwtSecret()` fell back to `"dev-secret-change-in-production"`
with only a `slog.Warn`. A known signing key means anyone can forge admin JWTs, and a
warning in a JSON log stream is easy to miss.

**Fix.** In `main.go`, the server now **refuses to start** when `OCTBASE_JWT_SECRET` is
empty or shorter than 32 bytes, *unless* `OCTBASE_DEMO_MODE=true` (where the dev default is
acceptable and still warned). Fail loud, not soft.

## 🟠 3. Login timing side-channel — **[DONE]**

**Risk.** `auth/EmailProvider.Login` returned immediately on `sql.ErrNoRows` (and on a
missing hash) *without* running bcrypt. Since the present-account path runs a cost-12
bcrypt compare (~tens of ms), response-time measurement reveals whether an email is
registered (user enumeration).

**Fix.** On not-found / invalid-hash, `Login` now performs a bcrypt comparison against a
fixed dummy hash before returning `ErrInvalidCredentials`, equalizing the timing of the
valid- and invalid-email paths.

## 🟠 4. Hardcoded compose secret — **[DONE]**

`podman-compose.yml` set `OCTBASE_JWT_SECRET: "dev-compose-secret-change-in-production"`
inline. Changed to source `${OCTBASE_JWT_SECRET}` from the environment/`.env` and enabled
demo mode for the local stack so it still boots without a real secret (consistent with #2).

## 🟢 5. Working-tree artifacts — **[N/A]**

Re-checked: `octbase-api/.gitignore` already lists `*.out` / `cover*.out`, and the
`cover.out`/`cover_wm.out` files in the tree are untracked. No change required.

---

## Deferred backlog (frontend — needs `frontend-testing` harness)

These are real and recommended, but each touches the SPA broadly and must be verified
with Playwright before shipping. Left as a planned next session.

- **D1 — Modularize `octbase-frontend/js/app.js` (5,866 lines). [DONE]**
  > **Constraint discovered:** native ES modules are *not* viable here. Chrome blocks
  > `import` over `file://` (origin `null`), and both the Playwright suite
  > (`tests/conftest.py` loads `index.html` via `file://`) and the standalone-demo
  > feature (`USE_STANDALONE_DEMO_AUTH = protocol === 'file:'`) depend on `file://`.
  > Verified empirically with headless Chrome.
  >
  > **Done instead:** split into **14 ordered classic `<script>` files** that share one
  > global scope (`config, icons, api, state, framework, realtime,
  > views-shell/board/task/agile/content/crud, admin, bootstrap`). Cut at top-level
  > boundaries preserving exact order, so the concatenation is **byte-identical** to the
  > old `app.js` (proven by diff in the split script). Largest file now 889 lines.
  > `bootstrap.js` loads last (wires delegation + `init()`). Each non-first file gets its
  > own `'use strict';` so strict mode is preserved per script.
  >
  > **Verified:** every file `node --check` clean; headless `file://` smoke test boots with
  > 0 console errors; full Playwright suite **227 passed / 19 skipped / 0 failed**
  > (baseline before the split was 224 passed + 3 attachment-env failures that now pass
  > with writable storage).
  >
  > **Follow-ups (both DONE):**
  > - **HTML-injection guard** — `octbase-frontend/scripts/check-innerhtml.mjs` fails on
  >   `innerHTML +=`, string concatenation into `innerHTML`, `document.write`, and
  >   unescaped interpolation of known user-content fields (`.title`, `.name`, `.text`,
  >   …). It is precise (clean on the current code, catches the real antipatterns —
  >   verified against a deliberately-bad fixture) rather than a blanket "no innerHTML"
  >   rule, which would only false-positive on the SPA's pre-escaped fragment vars. Wired
  >   into CI as a new `frontend` job (also runs `node --check` on every file); `build`
  >   now depends on it.
  > - **Explicit namespace** — `js/namespace.js` adds `window.App`, a read-only facade
  >   over the core singletons/state (`App.api`, `App.router`, `App.state`, …). A *full*
  >   per-symbol namespacing is intentionally not done: the Playwright suite drives the
  >   app via global `window.router` and the delegation registry resolves handlers by bare
  >   function name, so both depend on the globals. The boundaries, load-order contract,
  >   and HTML-safety convention are documented in `js/README.md`.
- **D2 — Extract a shared frontend package. [DONE]**
  > Measured what is actually shareable: **`i18n.js` is byte-identical** and the
  > **`STATUS_META`/`PRIORITY_META`/`TYPE_META`** maps (+ derived `STATUSES`/`PRIORITIES`/
  > `TASK_TYPES`) are identical. **Locales and the icon set are NOT** — mobile ships a
  > reduced string/glyph set (locales differ by ~35 lines; 38 vs 45 icons), so those stay
  > per-app by design.
  >
  > Created `octbase-shared/` as the canonical source for `i18n.js` + `meta.js`. Because
  > both SPAs are no-build static sites served from **separate containers** (and ES modules
  > don't load over `file://`), each app keeps a physical copy under `js/`, written by
  > `scripts/sync-shared.sh` and **guarded against drift in CI** by
  > `scripts/check-shared-sync.sh` (new step in the `frontend` job). Removed the duplicated
  > definitions from `octbase-frontend/js/config.js` and `octbase-mobile/js/core.js`; both
  > index.html files now load the shared `meta.js`; the mobile Containerfile ships it.
  >
  > **Verified:** drift guard clean; `node --check` on all shared/app files; headless
  > `file://` smoke of **both** apps (boot, `STATUSES===5`, 0 console errors); Playwright
  > suite green on a freshly-seeded DB (a stray `test_sidebar_shows_all_projects` failure
  > on the shared dev DB was traced to accumulated test projects exceeding the sidebar's
  > recency cap — not a regression; 52/52 incl. `test_projects.py` pass on a clean DB).
- **D3 — Dark mode. [DONE]** (desktop `octbase-frontend`)
  > Added a full M3 dark palette in `css/app.css`, applied via `[data-theme="dark"]`
  > (explicit) and `@media (prefers-color-scheme: dark)` (system default). Introduced
  > theme-aware structural tokens (`--app-bg`, `--state-hover{,-weak}`, `--state-selected`)
  > and replaced the hardcoded light-only values (`#f8f8f8` content bg, the
  > `rgba(25,28,26,.0x)` hover/selected overlays) that would otherwise break in dark; the
  > `#fff` uses are all text-on-color and stay. A topbar toggle (`cycleTheme`) cycles
  > system → light → dark, persisted in `localStorage`; a tiny inline script in
  > `index.html` applies it before first paint (no FOUC). New `theme` icon + i18n keys in
  > en/de/fr (parity 613).
  >
  > **Verified:** headless screenshots of the board in both themes (light bg
  > `246,250,246` / dark `25,28,26`, 0 console errors); light mode visually unchanged.
  > Scope note: mobile (`octbase-mobile/css/mobile.css`) dark mode is a separate follow-up.
- **D4 — Automated cache-busting. [DONE]**
  > `scripts/stamp-assets.py` rewrites every locally-referenced asset's `?v=` to the first
  > 12 hex of its SHA-256 (idempotent; `--check` mode is a CI drift guard, wired into the
  > `frontend` job). Ran it on both apps' `index.html` (replacing the hand-bumped date
  > tokens). Added Caddy cache headers to both Caddyfiles — `immutable, max-age=1y` for
  > fingerprinted `*.js/*.css/*.woff2` and `no-cache` for HTML — so the browser always
  > revalidates the HTML and an asset URL changes iff its bytes change.
  > **Verified:** stamping idempotent + guard; `caddy validate` passes; a live local Caddy
  > returns `immutable` on assets and `no-cache` on `index.html`/`/`. Cross-check: the
  > shared `i18n.js`/`meta.js` get identical hashes in both apps.
  > Note: the container minifies JS, so the committed `?v=` (hash of source) won't equal
  > the served minified bytes — cache-busting is still correct (URL changes on every source
  > change, enforced by the guard). Locales (XHR, unversioned) keep default caching.
- **D5 — `user-guide.html`. [DONE — scoped]**
  > A markdown→HTML generator for the stable 779-line guide is disproportionate in a
  > no-build project and risks content-fidelity loss on a blind transcription, so it is
  > **deferred** (explicit follow-up). Instead delivered the concrete, safe improvement the
  > guide actually needed: **dark-mode support** (it's token-based, so a
  > `@media (prefers-color-scheme: dark)` `:root` override mirroring the app palette flips
  > it; it opens standalone in a new tab so it follows the OS preference). **Verified:**
  > headless screenshots in light and dark (bg/text flip, 0 page errors, callouts/badges
  > readable).

### Backend follow-ups — **[DONE]**
- **B1 [DONE]** — Split `internal/workmanagement/repo.go` (1,527 lines) into per-aggregate
  files (`project_repo.go`, `task_repo.go`, `board_repo.go`, `planning_repo.go`; `repo.go`
  keeps the shared `execer`/`rowScanner` interfaces). Mechanical, order-preserving (the
  split script asserted the body partition is exact); build/vet/tests green.
- **B2 [DONE]** — Replaced the per-column `ListByColumn` loop in
  `board_handler.go:populateExternalColumnTasks` with a single batched
  `TaskRepo.ListByColumns(ids)` (`WHERE board_column_id IN (...)`), grouping results by
  column — removes the N+1.
- **B3 [DONE]** — Refresh-token reuse detection. Migration `017` adds `refresh_tokens.rotated_at`;
  rotation now *marks* the old token instead of deleting it (`RefreshTokenRepo.Rotate`), and
  `Claim` reports whether a presented token was already rotated. Replaying a rotated token
  revokes the whole session family (`DeleteByUser`) and writes a `REFRESH_TOKEN_REUSE`
  audit entry. `Store` opportunistically purges expired rows so the table stays bounded.
- **Tests [DONE]** — `internal/auth/hardening_test.go`: `TestLogin_UnknownEmail`
  (enumeration-resistant 401 parity) and `TestRefreshToken_ReuseRevokesSessions`
  (rotate → replay → 401 + family revoked). Webhook unsigned/invalid-signature paths were
  already covered (`TestHandle{Bitbucket,GitHub}_NotConfigured`, `_InvalidSignature`).
- **Drift fix** — `internal/testutil/testutil.go` `readMigrations` now reads the migrations
  directory dynamically (sorted) instead of a hardcoded list that had silently fallen behind
  at `015`; this is what would have hidden migration `017` from the test schema. Same class
  of bug as the old hardcoded `expectedMigrationVersion`.

---

## Definition of done (this session)

`go build ./...`, `go vet ./...`, `gofmt -l` clean, and the no-DB Go test packages green,
with items 1–5 implemented and a regression test for the migration-version derivation.
DB-backed health/login tests and the deferred frontend work are tracked above for the
next pass.
