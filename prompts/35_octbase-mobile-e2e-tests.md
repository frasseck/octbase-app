You are a senior test engineer working on Octbase, a Jira-like project
management tool. The desktop frontend (`octbase-frontend/`) has a mature
Playwright/pytest e2e suite (24 files, run by CI's e2e job). The mobile SPA
(`octbase-mobile/`, served under `/m/`) has **zero test coverage** — the only
CI it gets is `node --check` syntax validation.

Your mission is to close that gap with a mobile e2e smoke suite that covers
every mobile view and the core task flows, reusing the existing test
infrastructure so the suite runs in CI with no workflow changes.

---

## Where the tests live

Add **one new file**: `octbase-frontend/tests/test_mobile.py`.

Rationale: CI's e2e job already runs `python -m pytest -q` in
`octbase-frontend/tests/`, and the session-scoped `browser` and `api`
fixtures in `conftest.py` are app-agnostic. Placing the mobile suite there
gets CI coverage for free. Do **not** modify `conftest.py` or
`.github/workflows/ci.yml`; keep all mobile-specific fixtures inside
`test_mobile.py`.

---

## How the mobile app behaves under test (read before writing selectors)

- **Hash-routed SPA**, `octbase-mobile/index.html`. Routes: `/dashboard`,
  `/projects`, `/projects/{pid}/board`, `/projects/{pid}/backlog`,
  `/projects/{pid}/new`, `/task/{id}`, `/search`, `/notifications`,
  `/settings`, `/login`.
- **`?apiBase=<url>` is honored only in dev contexts** (`file://` or a
  loopback host) — same pattern as the desktop suite's `FILE_URL`.
- **On `file://` the app auto-authenticates** as the seeded demo user
  (`USE_STANDALONE_DEMO_AUTH` in `js/core.js`): `isAuthenticated()` is always
  true and the login form never renders. So:
  - All view/flow tests load `octbase-mobile/index.html` via `file://` with
    `?apiBase=…#/route`.
  - The **login flow** must be tested from a **loopback HTTP origin**: start a
    session-scoped `http.server` (Python stdlib, background thread, port 0)
    serving the `octbase-mobile/` directory, and load
    `http://127.0.0.1:<port>/index.html?apiBase=…`. On a localhost origin the
    real login form renders and `?apiBase` is still honored.
- **Use a phone context**: viewport ≈ 390×844 plus an iPhone user agent
  (`IS_PHONE` must match so desktop-handoff links are hidden, as on a real
  phone). Build the context inside the mobile fixtures — do not reuse the
  desktop `page` fixture.
- Key DOM anchors (from `js/app.js`):
  - Shell: `.app`, `#content`, `.bottom-nav` with 4 `.nav-item`s (My Work,
    Projects, Search, Inbox), `.appbar-title`, `.fab` (create task).
  - Dashboard: `.page-section` sections with `.section-head`.
  - Projects: `.row-card` rows; seeded "Demo Project" must be present.
  - Board: segmented column switcher `.seg-scroll .seg` (one per column,
    active column has `.active`; seeded board: Planned / In Progress /
    Review / Done), cards `.card.task-card`.
  - Task detail: `.detail-title`, `.detail-chips`, `.prop` buttons (status /
    priority / assignee sheets), `#comment-list`, `#comment-input`.
  - Bottom sheet: `#sheet-wrap .sheet` (role=dialog), `.sheet-opt` options,
    Escape closes.
  - Create task: `#ct-title`, `#ct-title-err` (`.hidden` toggled), `#ct-type`,
    `#ct-priority`, `#ct-submit`; success navigates to `/task/{id}`.
  - Search: `#search-input` (debounced 300 ms), results in
    `#search-results .task-card`.
  - Notifications: `.card-list` of `.row-card`s or a `.state` empty state.
  - Settings: `.seg-switch` radiogroups (theme, language), MFA section.
  - Login (loopback only): `#login-form`, `#login-email`, `#login-password`,
    `#login-submit`, `#login-error` (`.hidden` removed on failure).
- Seed constants are already in `conftest.py` (`DEMO_PROJECT_ID`,
  `DEMO_TASK_ID`, `DEMO_TASK_TITLE`, credentials) — import them.

---

## Required tests

**`TestMobileShell`** — app boots from `file://` (auto demo auth):
- dashboard loads with at least one `.page-section` and the bottom nav
- bottom nav has the four items; tapping Projects switches view

**`TestMobileProjects`**:
- projects list shows the seeded Demo Project
- tapping a project row opens its board

**`TestMobileBoard`**:
- board shows the four seeded column segments
- switching to another column updates the active segment
- tapping a task card opens the task detail view

**`TestMobileTaskDetail`** (route directly to `#/task/{DEMO_TASK_ID}`):
- detail shows title, chips, and the status/priority/assignee property rows
- status property opens a bottom sheet with one option per status; Escape
  closes it without changing anything
- adding a comment appends it to `#comment-list` (use `unique()` text)

**`TestMobileCreateTask`**:
- submitting with an empty title stays on the form (the `#ct-title` input is
  `required` and the form has no `novalidate`, so native validation blocks the
  submit; `#ct-title-err` is the JS backstop — assert no navigation happened
  rather than which layer fired)
- creating a task with a `unique()` title navigates to its detail view; verify
  via the API client that the task exists (then delete it to keep the stack
  tidy, if the API allows)

**`TestMobileBacklogSearchInboxSettings`** (one smoke test each):
- backlog view lists task cards
- searching for the seeded task title shows it in the results; tapping opens it
- notifications view renders (list or empty state — assert no error state)
- settings view renders its theme/language switches

**`TestMobileLogin`** (loopback HTTP origin):
- login form renders instead of the app
- wrong password shows `#login-error` and stays on the form
- correct demo credentials land on the dashboard with the bottom nav

---

## Implementation rules

- Follow `conftest.py` conventions exactly: selector waits with `TIMEOUT` /
  `SHORT`, **no sleep-based waits** (the 300 ms search debounce is covered by
  waiting for the result selector, not by sleeping).
- Reuse the session `browser` and `api` fixtures; import constants instead of
  redefining them.
- No new Python dependencies. The loopback server is stdlib
  (`http.server` + `threading`), session-scoped, and must shut down cleanly.
- Mutating tests may only touch the seeded Demo Project (the desktop suite
  already creates tasks/comments there); never delete seeded entities.
- Avoid brittle selectors: prefer ids, stable classes, and `:has-text(...)`.
- Do not modify production code. If a test exposes a real mobile bug,
  document it in the final report instead of patching around it (a skipped
  test with a reason is acceptable for a confirmed bug).

## Quality gate

1. `OCTBASE_BROWSER=chrome OCTBASE_API_BASE=<seeded API> .venv/bin/python -m
   pytest test_mobile.py -q` passes from `octbase-frontend/tests/`
   (see the `testing` and `frontend-testing` skills for the environment
   gotchas — system Chrome only).
2. The rest of the suite still passes (no fixture interference).
3. Every mobile view listed above has at least one test; login, task-status
   sheet, comment, and create-task flows are covered end-to-end.
