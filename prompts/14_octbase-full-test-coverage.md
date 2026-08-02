You are a senior staff software engineer and TDD coach working on a Jira-like task management application called Octbase.

Your mission is to achieve full test coverage of the entire application — both the Go API backend and the Playwright-driven frontend — by filling every identified gap without touching any production code.

Do not assume previous AI-generated tests are correct or complete. Inspect every test file yourself and verify coverage against the actual handlers, routes, domain rules, and UI views.

---

## Application overview

### Go API (`octbase-api/`)

- **Runtime**: Go, chi router, PostgreSQL, JWT authentication
- **Entry point**: `cmd/octbase-api/main.go`
- **Packages under `internal/`**:
  - `workmanagement` — projects, tasks (CRUD + sub-resources: comments, links, attachments, relations), boards, columns, backlog, releases, sprints, task categories, task templates, search, dashboard
  - `docs` — pages (create, edit, publish, archive, delete), revisions, task references, preview
  - `identityaccess` — users (`/me`), project memberships (CRUD, roles)
  - `scmintegration` — repository connections, branch references
  - `activity` — append-only project and task activity feed
  - `auth` — email/password login, JWT access + refresh tokens, invitations, logout, `/auth/me`
  - `notifications` — in-app notifications list, mark-read, preferences
  - `admin` — admin-only user management (list, update, reset-password)
  - `webhooks` — Bitbucket and GitHub push/PR event handlers (auto-close tasks by branch name)
  - `sse` — Server-Sent Events hub
  - `shared` — UUID, pagination, CORS, HTTP helpers
  - `seed` — deterministic demo data (fixed UUIDs, used by frontend tests)
  - `testutil` — shared test infrastructure

- **Authentication**: JWT Bearer tokens. Write endpoints reject requests without a valid token. Anonymous GET is permitted for some routes. The `X-User-Id` header is **not** used — authentication is purely JWT.

- **Error shape**: `{"code": "...", "message": "..."}` via `shared.WriteError`. Business rule codes include `TASK_TITLE_REQUIRED`, `TASK_IMMUTABLE`, `SLUG_CONFLICT`, `MILESTONE_HAS_OPEN_TASKS`, `VALIDATION_ERROR`, `CYCLE_DETECTED`, `DUPLICATE_RELATION`, `SELF_RELATION`, `SPRINT_ALREADY_ACTIVE`, `SPRINT_NOT_ACTIVE`, and others.

- **Defaults applied in handlers**: new projects → `PRIVATE` visibility; tasks → `TASK` type, `PLANNED` status, `MEDIUM` priority; memberships → `PROJECT_MEMBER` role; repo connections → `FAKE_GITLAB` provider, `main` branch; branch references → `feature` type.

### Frontend (`octbase-frontend/`)

- **Stack**: plain HTML + CSS + vanilla JS (`js/app.js`), no build step, no framework
- **Views**: `dashboard`, `projects`, `board`, `backlog`, `tasklist`, `releases`, `sprints`, `pages`, `repos`, `activity`, plus a task panel (slide-in, 7 tabs: Details, Comments, Links, Relations, Attachments, Branches, Activity), search (`renderSearchPage`), login, accept-invitation
- **Test stack**: Python + pytest + Playwright (Firefox, headless), located in `octbase-frontend/tests/`
- **Key constants** (from `conftest.py`):
  - `DEMO_USER_ID = "00000000-0000-0000-0000-000000000001"` / email `demo@octbase.dev` / password `demo1234`
  - `DEMO_PROJECT_ID = "00000000-0000-0000-0000-000000000101"`
  - `DEMO_TASK_ID = "00000000-0000-0000-0000-000000000201"` (title: "Implement user authentication")
  - `DEMO_TASK2_ID = "00000000-0000-0000-0000-000000000202"`
  - `DEMO_MILESTONE_ID = "00000000-0000-0000-0000-000000000401"`
  - `DEMO_PAGE_TITLE = "Getting Started"`

---

## Test infrastructure

### Go API tests

- **Database**: Real PostgreSQL, isolated per-test via dedicated schemas. All handler tests skip if `TEST_DATABASE_URL` is not set.
- **Pattern**: `testutil.NewTestDB(t)` creates an isolated schema with full migrations applied; `testutil.NewTestServer(t, db)` wires the full chi router with JWT middleware; `testutil.Do(t, srv, method, path, body, userID)` issues requests with a generated JWT.
- **Helper IDs**: `testutil.DemoUserID` (`...0001`), `testutil.SecondUserID` (`...0002`).
- **Helper constructors**: `MustCreateProject`, `MustCreateTask`, `MustCreateBoard`, `MustAddColumn`, `MustCreateRelease`, `MustAddMember` — use these in new tests instead of duplicating setup.
- **Service-level tests** (`workmanagement/service_test.go`) use an internal `newTestDB` (same PostgreSQL approach) because `testutil` cannot be imported from within `workmanagement` without a cycle.
- **Domain unit tests** (`workmanagement/domain_test.go`) have no DB dependency.
- **Run**: `cd octbase-api && go test ./...` (requires `TEST_DATABASE_URL`).

### Frontend tests

- **Requires a seeded API**: `cd octbase-api && PORT=8093 OCTBASE_DEMO_MODE=true go run ./cmd/octbase-api`
- **Run**: `cd octbase-frontend/tests && OCTBASE_API_BASE=http://127.0.0.1:8093 pytest`
- **Single test**: `pytest test_board.py::TestBoardStructure::test_board_has_four_columns`
- **Fixtures** (from `conftest.py`):
  - `api` — session-scoped `ApiClient` (signs in as demo user, sends Bearer token)
  - `app` — fresh page loaded at the projects list, waits for "Demo Project"
  - `demo_board` — `app` with Demo Project board open (`.board-column` visible)
  - `task_panel` — `demo_board` with the demo task panel open (`#task-panel.open`)
  - `page` — raw Playwright page (fresh context per test)
- **Helpers**: `navigate_to(page, label)`, `fill_modal(page, fields)`, `submit_modal(page)`, `toast_text(page)`, `unique(prefix)`

---

## Current coverage

### Go API — what is covered

**`workmanagement/handler_test.go`** (~80 tests):
- Projects: create (OK, blank name, no user ID), list (public/private visibility), get (found, not found), update, archive, delete (cascade, forbidden for non-member)
- Tasks: create (OK, blank title), list (filter by status, priority, assignee), get (OK, not found), update (OK, immutable when done/archived), assign, change status (OK, valid, invalid, immutable), change priority (OK, invalid), copy (basic, preserves type/priority, has seq number), archive, reopen, bulk update (valid, invalid), delete — **missing**
- Comments: add and list, update (requires membership), delete — **missing**
- Links: add, list, delete
- Attachments: add, list, delete
- Relations (via HTTP): add (self-relation rejected), list, delete
- Boards: create, list, get by ID, get default (with columns, not found), update, delete, add column — **add column has no dedicated test**, update column, delete column, move task, remove task from board
- Backlog: excludes board tasks, excludes done/archived, empty when all on board
- Releases: create, list, get (found, not found), update, close (success, with open tasks), reopen, delete — **missing**
- Sprints: create (OK, blank name), list, start (OK, conflict with active), complete (OK, not active), delete — **GetSprint and UpdateSprint missing**
- Categories: CRUD, forbidden for non-member, not found, update preserves unchanged fields
- Templates: CRUD, get, forbidden for non-member, not found, update — **InstantiateTemplate missing**
- Search: search tasks
- Visibility: public project visible to non-member, private project hidden

**`workmanagement/domain_test.go`** (12 tests):
- `IsImmutable`, `ValidStatus`, `ValidPriority`, `SlugFromName`, `ValidateTaskInput`, `ValidateCommentInput`, `DomainError.Error()`

**`workmanagement/service_test.go`** (13 tests):
- `AddRelation`: self-relation, duplicate, cycle detection, success, symmetry
- `DeleteRelation`: removes inverse
- `CloseRelease`: with open tasks, success, done tasks allowed, in-review blocked
- `AddBoardColumn`: unique status constraint
- `StartSprint`: success, blocked by active sprint
- `CompleteSprint`: moves open tasks to backlog

**`docs/handler_test.go`** (15 tests):
- Create page (OK, auto-slug, duplicate slug), list, get (found, not found), update (OK, blocked when archived), render preview, publish (creates revision), archive, search pages (OK, query too long), rebuild references, list revisions (empty only) — **DeletePage, ListReferences missing**

**`identityaccess/handler_test.go`** (10 tests):
- GetMe (OK, missing token, unknown user), list memberships (creator as owner), list members, add membership (OK, default role, forbidden for non-owner), update role, remove membership

**`scmintegration/handler_test.go`** (7 tests):
- Create repo connection (OK, default provider), list, update — **DeleteRepoConnection missing**
- Create branch (OK, default type), list — **DeleteBranch missing**

**`activity/handler_test.go`** (7 tests):
- List project activity (empty, records task creation, records status change, actor user ID, pagination)
- List task activity (OK, contains comment)

**`shared/shared_test.go`** (13 tests):
- `IsValidUUID`, `NewUUID`, `Now`, `ParsePagination` (defaults, custom, negative, zero, invalid), `CORSMiddleware`

**`auth/auth_test.go`** (4 tests):
- `HashPassword` round-trip, JWT issue+validate, wrong secret, expired token

**`auth/integration_test.go`** (10 tests):
- Login (valid, wrong password), deactivated user cannot login, JWT protected endpoint, viewer cannot write, invitation accept flow, bulk task update, unified search, dashboard, task seq number

### Go API — what is NOT covered

These packages have **no test files at all**:
- `notifications` — `GET /api/v1/users/me/notifications`, `POST .../read-all`, `PATCH .../notifications/{id}`, `GET .../notification-preferences`, `PATCH .../notification-preferences`
- `admin` — `GET /api/v1/admin/users`, `PATCH /api/v1/admin/users/{userId}`, `POST .../reset-password`; also the `RequireAdmin` middleware
- `webhooks` — Bitbucket push/PR handler, GitHub push/PR handler (HMAC auth, auto-close logic)

Individual handler gaps in tested packages:
- `workmanagement`: `DeleteTask`, `DeleteComment`, `DeleteRelease`, `GetSprint`, `UpdateSprint`, `InstantiateTemplate`, add-column (dedicated test), `ListTasks` filter by type
- `docs`: `DeletePage`, `ListReferences`, `ListRevisions` (non-empty case)
- `scmintegration`: `DeleteBranch`, `DeleteRepoConnection`
- `auth`: `POST /api/v1/auth/refresh`, `POST /api/v1/auth/logout`, `GET /api/v1/auth/me`, `GET /api/v1/invitations/{token}`

Also not tested: `GET /api/v1/meta/enums`, `GET /api/v1/version`

### Frontend — what is covered

8 test files, ~146 test methods:
- `test_projects.py` — project list, creation (dialog, validation, cancel/backdrop), navigation
- `test_board.py` — board structure (4 columns, names, add-task buttons, count badge), board cards (visible, type badge, title, priority dot, release tag, assignee avatar, click opens panel, correct column), task creation from board and topbar, no-board empty state (create default board), drag-and-drop
- `test_backlog.py` — backlog nav, breadcrumb, create button, items (type badge, priority dot, status badge, click opens panel, grouped by release), move-to-board (button, dialog, places on board)
- `test_tasks.py` — task list render, column headers, seeded tasks, row badges, row click opens panel, create button; filter bar (search, status, priority, type inputs), status/priority/search filters, no-results empty state, clear filters
- `test_task_panel.py` — panel open/close, 7 tabs present/active/switching, Details tab (description, status/priority/type dropdowns, assignee, release, dates, save, API call), priority/type changes, copy/archive/reopen, immutable done task, Comments (existing, add), Links (existing, add/delete), Relations (existing, type shown), Attachments (existing, filename, add metadata), Branches (existing name, create form), Activity tab entries
- `test_releases.py` — view loads, seeded release, status badge, goal, due date, create button; create (dialog, appears in list, starts planned); edit (modal opens, prepopulated, saves new goal); close/reopen (status changes, error on open tasks)
- `test_pages.py` — nav, breadcrumb, new-page button, seeded page in tree, page title; clicking shows content; edit mode (textarea, save button, preview button); create page (dialog, appears in tree, via API, save content)
- `test_activity.py` — nav, breadcrumb, items with content and timestamps, loads after task action, shows project events; pagination (no crash on many events), refresh on navigation

### Frontend — what is NOT covered

No test files exist for these views:
- **Sprints view** (`renderSprints`) — create sprint, list sprints, start sprint, complete sprint, sprint in sidebar
- **Repos view** (`renderRepos`) — create repository connection, list connections, create branch from task panel (already in `test_task_panel.py`), connection visibility
- **Dashboard view** (`renderDashboardPage`) — renders sections (assigned tasks, recent activity, project stats)
- **Search page** (`renderSearchPage`) — typing triggers search, results shown, clicking result navigates to task, empty-query state
- **Login/auth flow** — login form (correct credentials, wrong password, required fields), accept invitation page
- **Notifications UI** — bell icon in topbar, notification list, mark-as-read, preferences (if surfaced in UI)

---

## Your mission

Work through the gaps methodically. For each gap:
1. Inspect the relevant handler or view code.
2. Write tests that verify the stated contract.
3. Run the relevant tests to confirm they pass.
4. Document any production bug discovered (do not fix it unless it is trivially a typo or obvious oversight, and document the decision).

### Go API — required new tests

**Priority 1 — missing packages (create new test files)**

`internal/notifications/handler_test.go`:
- `TestListNotifications_Empty` — authenticated user gets empty list
- `TestListNotifications_HasEntries` — after inserting a notification directly, it appears in the list
- `TestMarkAllRead` — all notifications transition to `isRead: true`
- `TestMarkRead_SingleNotification` — single notification marked read
- `TestMarkRead_NotFound` — 404 for unknown ID
- `TestGetPreferences_ReturnsDefaults` — preferences list returned
- `TestUpdatePreference_OK` — inApp/email flags updated

`internal/admin/handler_test.go`:
- `TestRequireAdmin_Rejects_NonAdmin` — non-admin user gets 403
- `TestListUsers_AdminOnly` — admin can list users
- `TestUpdateUser_Deactivate` — admin can set `isActive: false`
- `TestResetPassword_OK` — admin can reset a user's password

`internal/webhooks/handler_test.go`:
- `TestHandleBitbucket_PushEvent` — valid JSON payload returns 200
- `TestHandleBitbucket_InvalidJSON` — malformed body returns 400
- `TestHandleGitHub_PushEvent` — valid JSON payload returns 200
- `TestHandleGitHub_AutoCloseTask` — PR merged with matching branch name sets task status to DONE (or at least processes without error)

**Priority 2 — missing handlers in tested packages**

Add to `workmanagement/handler_test.go`:
- `TestDeleteTask_OK` — task deleted, subsequent GET returns 404
- `TestDeleteComment_OK` — comment added then deleted, not in list
- `TestDeleteRelease_OK` — release deleted, subsequent GET returns 404
- `TestGetSprint_OK` — sprint created and retrieved by ID
- `TestUpdateSprint_OK` — sprint name and goal updated
- `TestInstantiateTemplate_OK` — template instantiated creates a task with the template's title/type/priority
- `TestAddBoardColumn_OK` — column added to board appears in GET board response
- `TestListTasks_FilterByType` — tasks filtered by `taskType=BUG` return only bug tasks

Add to `docs/handler_test.go`:
- `TestDeletePage_OK` — page deleted, subsequent GET returns 404
- `TestListReferences_OK` — rebuild references then list them, at least one reference returned
- `TestListRevisions_NonEmpty` — publish creates a revision, revision appears in list

Add to `scmintegration/handler_test.go`:
- `TestDeleteBranch_OK` — branch created then deleted, not in list
- `TestDeleteRepoConnection_OK` — repo connection deleted, not in list

Add to `auth/integration_test.go` (or a new `auth/handler_test.go`):
- `TestRefreshToken_OK` — login returns refresh cookie, call `/auth/refresh` with cookie returns new access token
- `TestLogout_ClearsRefreshToken` — after logout, refresh token no longer works
- `TestAuthMe_OK` — `/api/v1/auth/me` returns the current user
- `TestGetInvitation_OK` — create invitation (admin), get invitation by token returns invitation details

**Priority 3 — utility/infrastructure**

Add to `shared/shared_test.go` (or a new file):
- `TestEnumsHandler` — `GET /api/v1/meta/enums` returns expected keys (taskStatuses, taskPriorities, taskTypes, etc.)
- `TestVersionHandler` — `GET /api/v1/version` returns `{"version": ..., "name": "Octbase API"}`

### Frontend — required new test files

**`test_sprints.py`**:

```python
class TestSprintsView:
    def test_sprints_nav_item_exists
    def test_navigating_to_sprints_loads_view
    def test_sprints_breadcrumb_shows_sprints
    def test_new_sprint_button_visible

class TestSprintCreation:
    def test_create_sprint_dialog_opens
    def test_create_sprint_appears_in_list
    def test_create_sprint_requires_name

class TestSprintLifecycle:
    def test_start_sprint_changes_status
    def test_complete_sprint_changes_status
    def test_start_sprint_when_one_already_active_shows_error
```

**`test_repos.py`**:

```python
class TestReposView:
    def test_repos_nav_item_exists
    def test_navigating_to_repos_loads_view
    def test_new_repo_connection_button_visible

class TestRepoConnectionCreation:
    def test_create_repo_dialog_opens
    def test_create_repo_connection_appears_in_list
    def test_create_repo_requires_name
```

**`test_dashboard.py`**:

```python
class TestDashboardView:
    def test_dashboard_loads_on_app_open
    def test_dashboard_shows_assigned_tasks_section
    def test_dashboard_shows_recent_activity_section
    def test_dashboard_has_new_project_shortcut
    def test_clicking_assigned_task_opens_panel
```

**`test_search.py`**:

```python
class TestSearchView:
    def test_search_input_in_topbar
    def test_typing_query_shows_results
    def test_search_result_shows_task_title
    def test_clicking_result_opens_task_panel
    def test_empty_query_shows_placeholder_state
    def test_no_match_shows_empty_state
```

---

## Implementation rules

- **Follow existing patterns exactly**. Go tests use `testutil.NewTestDB` + `testutil.NewTestServer` + `testutil.Do`. Frontend tests use the `api`, `app`, `demo_board`, `task_panel` fixtures from `conftest.py`.
- **Do not modify production code**, `testutil`, or `conftest.py` unless you discover a real bug caused by something you are testing. Document any such change.
- **Extend `testutil` helpers** (e.g., `MustCreateSprint`, `MustCreatePage`) only if multiple new tests need them. Keep helpers minimal.
- **Test names describe behavior**, not implementation. Prefer `TestDeleteTask_OK`, `TestInstantiateTemplate_CreatesTask`, not `TestDeleteTask_HTTP`.
- **Assert the full contract**: status code, error code for failures, key response fields for successes. Don't just assert `200 OK`.
- **Frontend tests must not rely on timing sleeps**. Use Playwright `wait_for_selector` with `TIMEOUT` or `SHORT` from `conftest.py`.
- **Do not add new Python or Go dependencies** unless absolutely unavoidable. All required libraries are already installed.
- **Mirror file changes**: if a frontend test file is added, verify it works against the standalone `octbase-frontend/index.html?apiBase=...` URL (not the API-served UI).
- **Avoid brittle selectors**. Prefer `text=...`, `:has-text(...)`, `#id`, `.class` over positional or generated selectors.

---

## Quality gate

The task is not complete until:

1. `cd octbase-api && go test ./...` passes (with `TEST_DATABASE_URL` set).
2. `cd octbase-frontend/tests && OCTBASE_API_BASE=http://127.0.0.1:8093 pytest` passes (with the seeded API running).
3. Every handler and route identified in the gaps list above has at least one happy-path test and at least one error-path test.
4. Every new frontend view (sprints, repos, dashboard, search) has at least a smoke test confirming the view loads.
5. No new test introduces a sleep-based wait instead of a Playwright selector wait.
6. No existing test was broken or deleted.

---

## Execution order

Work in this order to maximise parallelism and catch infrastructure issues early:

1. Inspect the production code for each gap (handlers, routes, domain rules, UI elements).
2. Write all Go unit and domain tests first (no database needed).
3. Write Go handler integration tests, running the suite after each package is complete.
4. Write the new frontend test files, running the suite after each file.
5. Run the full Go test suite.
6. Run the full frontend test suite.
7. Produce a final coverage report listing: tests added, any production bugs found, any remaining gaps.

---

## Commands reference

```bash
# Go API tests (requires PostgreSQL)
cd octbase-api
go test ./...
go test ./internal/notifications -run TestListNotifications_Empty -v

# Start seeded API for frontend tests
cd octbase-api
PORT=8093 OCTBASE_DEMO_MODE=true go run ./cmd/octbase-api

# Frontend tests (in a separate terminal)
cd octbase-frontend/tests
OCTBASE_API_BASE=http://127.0.0.1:8093 pytest
OCTBASE_API_BASE=http://127.0.0.1:8093 pytest test_sprints.py -v

# Build check
cd octbase-api
go build -o /tmp/octbase-api-check ./cmd/octbase-api
```
