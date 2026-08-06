---
name: testing
description: Run Octbase's test suites - Go backend tests (octbase-api) and Playwright/pytest frontend tests (octbase-frontend/tests). Use whenever asked to run, write, or debug tests for this project.
---

# Octbase testing

This repo has two independent test suites: Go tests for `octbase-api` and
Python/Playwright tests for `octbase-frontend`.

## Go backend tests (`octbase-api`)

Tests need a real Postgres reachable via `TEST_DATABASE_URL`. Each test
creates and drops its own schema for isolation, and is **skipped** if the
var isn't set.

A Postgres instance is normally already running via podman-compose —
this checkout's dev stack publishes `localhost:5433`; the demo stack
publishes `localhost:5432` (user/pass/db all `octbase` on both). Check with:

```bash
podman ps   # look for postgres containers publishing 5432/5433
```

Run all tests:

```bash
cd octbase-api
TEST_DATABASE_URL="postgres://octbase:octbase@localhost:5433/octbase?sslmode=disable" go test ./...
```

Run one package / one test:

```bash
TEST_DATABASE_URL="postgres://octbase:octbase@localhost:5433/octbase?sslmode=disable" \
  go test ./internal/workmanagement/... -run TestTaskTemplates_CRUD -v
```

Seeded test users live in `internal/testutil` (`SuperAdminUserID`, `DemoUserID`,
`SecondUserID`, `GuestUserID`, `DisabledUserID` — see `octbase-api/README.md`
for the full table).

Useful adjacent checks: `go build ./...`, `go vet ./...`, `gofmt -l .`,
`golangci-lint run ./...`.

## Frontend unit tests (`octbase-frontend/js/*.test.js`, `octbase-mobile/js/*.test.js`, Vitest)

A second, much faster layer that runs without a browser or a stack:

```bash
npm run test:unit                    # whole layer (~2s, 187 tests)
npm run test:unit -- state.test.js   # one file
npx vitest                           # watch mode
```

Both globs are in `vitest.config.mjs`. The mobile one was added on 2026-08-01 —
before that the runner collected the desktop directory only, so a test file
placed under `octbase-mobile/js/` ran **zero times and still reported green**.
If you add a third source root, add its glob there too.

**They complement the Playwright suite and never replace it** — pure logic only.
Two shapes coexist, and `octbase-frontend/js/README.md` ("Testing") explains
which to reach for: a **real import** of the module under test (add
`// @vitest-environment jsdom` on line 1 if it needs browser globals), or the
`loadModule()` vm harness in `js/testutil.js` when the test needs to substitute
a collaborator. Prefer the real import for anything new. The harness **throws**
on an import shape it cannot rewrite and is shared by eight files, so one new
import form can red-line all of them at once.

## Frontend tests (`octbase-frontend/tests`)

Python + pytest + Playwright. A venv is already set up at
`octbase-frontend/tests/.venv`. **Do not `source .venv/bin/activate`** (it
resets the shell cwd) — call the binaries directly, from the tests dir:

```bash
cd octbase-frontend/tests   # then use .venv/bin/python / .venv/bin/pytest
```

If the venv's binaries fail with `bad interpreter`, recreate it (see the
`frontend-testing` skill).

### Browser choice

**Always set `OCTBASE_BROWSER=chrome`.** Playwright's bundled `chromium`
and `firefox` cannot install on Ubuntu 26.04 ("Playwright does not support
chromium/firefox on ubuntu26.04-x64"). The only working engine is system
Chrome (`/usr/bin/google-chrome`) via the `chrome` channel.

### API target

Tests hit a live API (default `http://127.0.0.1:8000`) and skip entirely if
it's unreachable. The whole `app` fixture flow logs in as the seeded demo
user (`demo@octbase.dev` / `demopass1234`), so the API's demo seed data must be
intact and that login must work.

- The main compose stack on port 8000 may be a long-lived shared/demo
  deployment where the demo password no longer matches the seed — check
  first:

  ```bash
  curl -s -X POST http://127.0.0.1:8000/api/v1/auth/login \
    -H "Content-Type: application/json" -d '{"email":"demo@octbase.dev","password":"demopass1234"}'
  ```

  If that returns `INVALID_CREDENTIALS`, point tests at a fresh/dev stack
  instead, e.g. an isolated podman-compose stack (often already running on
  port 8001 in this environment) via:

  ```bash
  OCTBASE_API_BASE=http://127.0.0.1:8001 OCTBASE_BROWSER=chrome .venv/bin/python -m pytest -q
  ```

  Or bring up a disposable stack from repo root — use ports and a Postgres
  data dir that don't collide with the running stacks (see the `dev-stack`
  skill; without `PGDATA_DIR` the local `.env` points at the live dev
  stack's data dir):

  ```bash
  COMPOSE_PROJECT_NAME=octbase_test POSTGRES_PORT=5434 API_PORT=8002 \
    FRONTEND_PORT=8084 PGDATA_DIR=./pgdata_test \
    podman-compose -p octbase_test up -d
  ```

### Running tests

```bash
OCTBASE_BROWSER=chrome .venv/bin/python -m pytest -q                 # whole suite
OCTBASE_BROWSER=chrome .venv/bin/python -m pytest test_board.py -x -q   # one file
OCTBASE_BROWSER=chrome .venv/bin/python -m pytest test_board.py::TestBoard::test_x -v  # one test
```

**CI runs the suite against the BUILT app, served over HTTP** (37b stage 6):
`npm run build`, then `vite preview` with `/api` proxied to the API, then
`OCTBASE_UI_URL=http://localhost:4173/index.html?e2e=1`. Reproduce that locally
with the recipe in `octbase-frontend/tests/KNOWN_FAILURES.md`
("Against the built `dist/`"). Two things there are load-bearing and cost a
previous session real time: the preview must proxy `/api` so the page is
**same-origin** with its API (the session lives in an HttpOnly refresh cookie a
cross-origin page never gets back), and `OCTBASE_UI_URL` must **carry a query
string** (`test_taskview.py` appends `&taskView=off`).

**`OCTBASE_UI_URL` has no default and the desktop suite needs it.** The old
`file://` fallback to the *source* `octbase-frontend/index.html` stopped being
loadable at 37b stage 2 (module entry, bare specifiers) and was removed in
1.1.2. Unset, the desktop tests now **skip** with a message naming the variable
and giving the recipe — and **fail** when `CI` is set, so a job that forgets it
cannot come back green having driven nothing. `test_mobile.py` (own mobile URL,
still a real `file://` target in `dist-standalone/`) and the `no_stack` helper
tests run without it.

**The browser's web security follows the URL scheme.** `--disable-web-security`
/ `--allow-file-access-from-files` exist only because a `file://` page has a
`null` origin, so since stage 6 they are passed **only** when the UI URL is a
`file://` one. A served run therefore tests with the same-origin policy ON,
which is the only way the suite can see a CORS or CSP regression. The mobile
tests that genuinely open the standalone bundle from disk take the relaxed
browser from the separate `file_browser` fixture.

### Shared fixtures (`conftest.py`)

- `api` — `ApiClient`, session-scoped, skips suite if API unreachable
- `browser` — session-scoped Playwright browser (`OCTBASE_BROWSER` controls
  engine); web security relaxed only when the UI URL is `file://`
- `file_browser` — session-scoped browser that may open `file://` artifacts
  (the mobile standalone bundle); returns `browser` itself when that one already
  carries the flags, so a served run pays for one extra Chrome and a file:// run
  for none
- `page` — fresh browser context per test, instrumented with an in-flight API
  request counter (see `settle()`)
- `app` — page navigated to the app and logged in as the demo user
- `demo_board` — `app` navigated to the seeded Demo Project's board
- `task_panel` — `demo_board` with the seeded demo task's panel open
- `_reap_created_entities` — **autouse**; see "Don't leave data behind" below
- Helpers: `navigate_to()`, `fill_modal()`, `submit_modal()`, `toast_text()`,
  `unique()`, `settle()`, `poll_until()`, `await_next_second()`

### Wait on conditions, not on the clock

The API answers in single-digit milliseconds, so a fixed sleep after an action
is almost entirely dead time. Do **not** add `page.wait_for_timeout(SHORT)` /
`(TIMEOUT)` back — use:

- **`settle(page)`** — returns once no API request is in flight and a frame has
  painted (tens of ms, never raises). This is the replacement for
  "click, then wait a beat for the re-render". `navigate_to()` and
  `submit_modal()` already call it.
- **`poll_until(fn, message=...)`** — polls until `fn()` returns truthy and
  returns it; raises `AssertionError` on timeout. Use when reading state back
  through `api.get(...)` after a write.
- **`await_next_second()`** — only for assertions comparing `createdAt` vs
  `updatedAt`: those are second-precision, so two writes in the same second are
  indistinguishable.

Three traps worth knowing:

- **`settle()` ignores the SSE stream** (`/events`) on purpose. It is long-lived
  and never "finishes", so counting it would peg the in-flight counter at 1 and
  silently turn every `settle()` back into a full 2s sleep.
- **`settle()` can outrun a debounce.** The command palette debounces 250ms and
  the page preview 300ms; `settle()` returns before those timers fire, because
  no request exists yet. Wait on the visible outcome instead.
- **Never `poll_until` an absence.** Polling for "gone"/"still empty" returns on
  the first tick and passes vacuously. Poll a positive precondition first, then
  assert the absence.

### Don't leave data behind

`internal/seed` data is public surface the tests pin to by fixed id, and the
tasks endpoint caps a page at **200 rows**. The seeded demo task is the oldest,
so once the demo project passes 200 tasks it drops out of the board's fetch
window, its card never renders, and every test that clicks it burns Playwright's
**30s default timeout** — a whole-file wipeout that reads as 40+ opaque setup
errors, not as "the data is dirty". This is exactly what had happened to the dev
stack (243 tasks / 104 pages), and it accounted for the bulk of the suite's
runtime.

The autouse `_reap_created_entities` fixture now snapshots task/page/project ids
around each test and deletes what the test added — including entities created by
driving the UI, which per-test `try/finally` cleanup misses. Keep it that way: a
test may create whatever it likes, but the demo project's row counts must be the
same before and after. Verify with:

```bash
podman exec octbase_dev_postgres_1 psql -U octbase -d octbase -t -A -c \
  "SELECT count(*) FROM tasks WHERE project_id='00000000-0000-0000-0000-000000000101'"
```

If a stack is already polluted, reset it: `scripts/reset_db.sh --yes` (wipes and
reseeds that stack — never point it at data you care about).

### Accessibility tests

`test_accessibility.py` uses `axe-playwright-python` (already in
`requirements.txt`).

### Known failure modes (not your change's fault)

`octbase-frontend/tests/KNOWN_FAILURES.md` is the **measured baseline** — the
pass/skip/fail split at a named commit, each known failure classified real-vs-
flake with the run count behind that call, and the isolated-stack recipe that
makes a run reproducible. Compare against it before calling anything a
regression; the modes below are the recurring causes it accounts for.

- **MFA e2e tests (`test_settings.py`) fail with 500s** if the target API has
  no `OCTBASE_MFA_ENC_KEY` set — enrollment can't encrypt the TOTP secret.
  Set a 32-byte key in the stack's `.env` (`openssl rand -base64 32`) or the
  local API's env before blaming the diff.
- **`test_accessibility.py` silently skips its whole file** unless
  `OCTBASE_ACCESS_API_BASE` / `OCTBASE_ACCESS_UI_URL` point at a **served** UI
  (they default to the deployed `dev.octbase.io`, unreachable locally, and
  `file://` will not do). A green suite does **not** mean these ran — 13 of the
  22 default skips are this file. Once pointed at a served stack, one of them
  fails for real (`TestLiveRegions::test_bulk_bar_is_labelled_region`, empty
  seeded backlog) and one is flaky — see `KNOWN_FAILURES.md`; rerun in isolation
  before treating a failure as real.
- **`app` fixture timing out on login** can be the auth rate limit (120/min
  per IP on `/api/v1/auth/*`) after many consecutive runs — wait ~60s.
- **Many setup errors in one file, each taking ~30s**, is the polluted-stack
  signature, not a code regression: the seeded demo task has fallen out of the
  200-row task window and its card cannot render. Check the row count (see
  "Don't leave data behind") before blaming the diff. Historically this was
  mistaken for a stable baseline of "known failures" in `test_task_panel.py`,
  `test_board.py::TestBoardCards` and `test_projects.py` — those all pass
  against a freshly seeded stack.

## End-to-end agile scenario (API-level)

`scripts/run_agile_scenario.sh` is a self-contained, repo-level scenario test:
it resets + reseeds the DB (`scripts/reset_db.sh --yes`), runs
`scripts/simulate_agile_project.py` against the API, then resets again so the
environment is left clean. Wrapped in a 20-minute wall-clock timeout; non-zero
exit on failure. API base comes from `.env` (`API_PORT`), overridable with
`OCTBASE_API_BASE`. ⚠️ It **wipes and reseeds the target stack's data** — never
point it at a stack whose state you care about.
