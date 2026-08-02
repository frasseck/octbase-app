# Octbase Quality Review Prompt

**Purpose:** Review the Octbase prototype for correctness, concept alignment, test coverage, and implementation quality. Use this prompt after any significant change to verify the system is still consistent with its design intent.

---

## 1. System Overview

Octbase is a developer-centric task and documentation management prototype. It is a **modular monolith** built in Go with PostgreSQL, exposing a REST API consumed by a no-build vanilla HTML/CSS/JS frontend.

**Architecture priorities (in order):**
1. Maintainable — clear boundaries, small domain objects, tested behaviour
2. Functionally correct — domain rules enforced where they belong
3. Secure — safe defaults (JWT auth on writes, role checks, CORS)
4. Deployable — Podman Compose, postgres + API in two containers (API serves the SPA directly)

**Stack:**
- API: Go 1.25 · go-chi/chi v5 · lib/pq (PostgreSQL) · golang-migrate · golang-jwt · single binary
- Frontend: vanilla HTML/CSS/JS, no build step, works both from `file://` and when served by the API
- Runtime: Podman Compose (`podman-compose.yml`); images from `docker.io/library` and `gcr.io/distroless`

---

## 2. Repository Layout

```
podman-compose.yml                  full stack: postgres + API (API serves the SPA)

octbase-api/
  cmd/octbase-api/main.go          router wiring, JWT middleware, /api/v1/ prefix, SPA file server
  internal/
    shared/                         IsValidUUID, ParsePagination, AuthMiddleware, CORS, WriteError
    seed/                           deterministic demo seed (full upsert on every startup)
    workmanagement/                 projects, tasks, boards, releases, categories, templates
    identityaccess/                 users, memberships
    docs/                           pages, revisions, references, AsciiDoc renderer
    scmintegration/                 repository connections, branch references
    activity/                       activity feed (audit log)
    auth/                           JWT login, refresh, logout, token blacklist
    admin/                          admin user management
    notifications/                  in-app notifications
    webhooks/                       outbound webhook delivery
    sse/                            Server-Sent Events for live updates
  migrations/
    001_initial.up.sql              core schema (projects, tasks, boards, pages, memberships…)
    002_constraints.up.sql          FK constraints and indexes
    003_auth.up.sql                 auth_tokens, refresh_tokens tables
    004_notifications.up.sql        notifications and webhooks tables
  web/                              copy of SPA assets served by the API (app.js, app.css, index.html…)
  api/openapi.yaml                  core OpenAPI contract snapshot
  Containerfile                     multi-stage build; final image gcr.io/distroless/static-debian12
  podman-compose.yml                API + postgres (no separate nginx container)

octbase-frontend/
  index.html                        HTML shell
  app.css                           all styles (no inline style="" in app.js)
  app.js                            frontend logic (strict mode, no build step)
  favicon.ico                       browser tab icon
  .containerignore                  excludes tests/ and logo1.png
  tests/                            Playwright + pytest end-to-end tests

draft/
  Octbase_Architecture_With_Go_API_Prototype.md
                                    corrected architecture baseline (design source of truth)

prompts/
  1_octbase-api.md                 API POC recreation prompt (PostgreSQL, X-User-Id)
  2_octbase-frontend.md            frontend POC recreation prompt (matches prompt 1)
  3_octbase-quality-review.md      this file (current MVP state)
  4_octbase-frontend-quality-check.md  frontend code review checklist
  5_octbase-db.md                  SQLite→PostgreSQL migration (historical; already applied)
  6_octbase-crud-functions.md      CRUD function additions
  7_octbase-finalize-poc.md        POC hardening (membership guards, RBAC, typed values)
  8_octbase-create-mvp.md          POC→MVP transformation (JWT, /api/v1/, notifications)
  9_TODO_POC.md                     POC completion checklist
  10_TODO_MVP.md                    MVP feature scope and non-goals
  11_TODO_UX.md                     UX bug list and improvements
  12_octbase-frontend-mvp.md       frontend MVP additions
  13_TODO_Frontend.md               frontend task list
```

---

## 3. Domain Constants — Canonical Values

These are the values defined in the running implementation. Verify they match source code constants and are used consistently in tests, the frontend, and the OpenAPI spec.

### Task Status
```
PLANNED     initial; in backlog or on board
IN_PROGRESS being worked on
IN_REVIEW   in code/peer review        ← design doc says REVIEW; impl uses IN_REVIEW
DONE        complete and immutable
ARCHIVED    archived and immutable
```
**Prototype delta:** the design spec uses `REVIEW`; the implementation uses `IN_REVIEW`. This is intentional for clarity. Update the spec if this is accepted permanently.

### Task Types
```
TASK  BUG  STORY  EPIC  CHORE
```
**Prototype delta:** design spec lists `STORY | BUG | TASK | TECH_DEBT | DOCS | SPIKE`. The implementation uses a simpler set. `TECH_DEBT`, `DOCS`, `SPIKE` are deferred to v0.5.

### Priorities
```
LOW  MEDIUM  HIGH  CRITICAL
```

### Relation Types
```
RELATES_TO  BLOCKS  BLOCKED_BY  DUPLICATES
```
**Prototype delta:** design spec includes `IS_BLOCKED_BY` and `IS_DUPLICATED_BY`; implementation uses `BLOCKED_BY` and omits `IS_DUPLICATED_BY`. Acceptable simplification for the prototype.

### Roles
```
OWNER  DEVELOPER  VIEWER
```
**Prototype delta:** design spec lists `ADMIN | MAINTAINER | DEVELOPER | VIEWER`. The implementation uses `OWNER` instead of `ADMIN`/`MAINTAINER`. JWT Bearer auth enforces that write operations require a valid token; role-based permission checks guard OWNER-only operations (project delete, member management, repository connections).

### Release Statuses
```
PLANNED  CLOSED
```
**Prototype delta:** design spec includes `ACTIVE` and `ARCHIVED` release states. These are deferred.

### Page Statuses
```
DRAFT  PUBLISHED  ARCHIVED
```

### SCM Providers
```
FAKE_GITLAB  GITHUB  BITBUCKET
```
Default: `FAKE_GITLAB`. No real provider calls are made.

### Branch Types
```
feature  bugfix  hotfix  release
```

---

## 4. Core Business Rules to Verify

Check that each rule is enforced in code **and** has a corresponding test.

| Rule | Enforced in | Error code | Test |
|---|---|---|---|
| Task title must not be blank | `CreateTask` handler | `TASK_TITLE_REQUIRED` | `TestCreateTask_BlankTitle` |
| DONE task cannot be modified | `UpdateTask`, `ChangeStatus` | `TASK_IMMUTABLE` | `TestUpdateTask_ImmutableWhenDone`, `TestChangeStatus_ImmutableWhenDone` |
| ARCHIVED task cannot be modified | `UpdateTask`, `ChangeStatus` | `TASK_IMMUTABLE` | `TestUpdateTask_ImmutableWhenArchived`, `TestChangeStatus_ImmutableWhenArchived` |
| Project name must not be blank | `CreateProject` handler | `VALIDATION_ERROR` | `TestCreateProject_BlankName` |
| Page slug must be unique within project | `CreatePage`, `UpdatePage` | `SLUG_CONFLICT` | `TestCreatePage_DuplicateSlug` |
| Archived page cannot be modified | `UpdatePage` handler | `PAGE_ARCHIVED` | `TestUpdatePage_BlockedWhenArchived` |
| Release cannot close with open tasks | `Service.CloseRelease()` | `MILESTONE_HAS_OPEN_TASKS` | `TestCloseRelease_WithOpenTasks`, `TestCloseRelease_HTTPWithOpenTasks` |
| Task cannot relate to itself | `Service.AddRelation()` | `TASK_SELF_RELATION` | `TestAddRelation_SelfRelation`, `TestAddRelation_SelfRelationHTTP` |
| Blocking relation must not create a cycle | `Service.AddRelation()` → `HasCycle()` | `TASK_RELATION_CYCLE` | `TestAddRelation_BlocksCycle` |
| Duplicate relation must be rejected | `Service.AddRelation()` | `TASK_RELATION_DUPLICATE` | `TestAddRelation_Duplicate` |
| `render-preview` must not modify the page | `RenderPreview` handler | — | `TestRenderPreview_DoesNotModifyPage` |
| Publish creates an immutable revision | `PublishPage` handler | — | `TestPublishPage_CreatesRevision` |
| `IsImmutable(DONE)` = true | `domain.go` | — | `TestIsImmutable` |
| `IsImmutable(ARCHIVED)` = true | `domain.go` | — | `TestIsImmutable` |

### Backlog definition
Backlog = tasks where `board_column_id IS NULL` AND `status NOT IN ('DONE', 'ARCHIVED')`.

Both conditions are required. A DONE task with no board column is **not** in the backlog.

Relevant tests: `TestBacklog_ExcludesBoardTasks`, `TestBacklog_ExcludesDoneAndArchived`, `TestBacklog_ReturnsEmptyWhenAllOnBoard`.

### Board/Status independence
Moving a task on the board (`POST /api/boards/{id}/move-task`) sets `boardColumnId` and `boardRank`. It does **not** change `task.status`. Status changes require a separate `POST /api/tasks/{id}/status` call.

---

## 5. API Completeness Checklist

Verify each endpoint exists, returns the documented status code, and has at least one handler test.

### System
- [ ] `GET /health` → 200 `{"status":"ok","database":"ok"}`
- [ ] `GET /api/version` → 200
- [ ] `GET /api/meta/enums` → 200
- [ ] `GET /openapi.yaml` → 200

### Identity and Access
- [ ] `GET /api/users/me` → 200 | 401 | 404
- [ ] `GET /api/projects/{id}/memberships` → 200
- [ ] `POST /api/projects/{id}/memberships` → 201
- [ ] `PATCH /api/projects/{id}/memberships/{userId}` → 200
- [ ] `DELETE /api/projects/{id}/memberships/{userId}` → 204

### Projects
- [ ] `POST /api/projects` → 201 | 422
- [ ] `GET /api/projects` → 200
- [ ] `GET /api/projects/{id}` → 200 | 404
- [ ] `PATCH /api/projects/{id}` → 200
- [ ] `POST /api/projects/{id}/archive` → 200
- [ ] `DELETE /api/projects/{id}` → 204

### Tasks
- [ ] `POST /api/projects/{id}/tasks` → 201 | 422
- [ ] `GET /api/projects/{id}/tasks` → 200 (supports `?status=&priority=&assigneeId=&page=&size=`)
- [ ] `GET /api/tasks/{id}` → 200 | 404
- [ ] `PATCH /api/tasks/{id}` → 200 | 422 (immutable)
- [ ] `POST /api/tasks/{id}/assign` → 200
- [ ] `POST /api/tasks/{id}/status` → 200 | 422 (immutable)
- [ ] `POST /api/tasks/{id}/priority` → 200
- [ ] `POST /api/tasks/{id}/copy` → 201
- [ ] `POST /api/tasks/{id}/archive` → 200
- [ ] `POST /api/tasks/{id}/reopen` → 200
- [ ] `DELETE /api/tasks/{id}` → 204

### Task Sub-resources
- [ ] `POST /api/tasks/{id}/comments` → 201
- [ ] `GET /api/tasks/{id}/comments` → 200
- [ ] `PATCH /api/tasks/{id}/comments/{commentId}` → 200 | 404
- [ ] `DELETE /api/tasks/{id}/comments/{commentId}` → 204 | 404
- [ ] `POST /api/tasks/{id}/links` → 201
- [ ] `GET /api/tasks/{id}/links` → 200
- [ ] `DELETE /api/tasks/{id}/links/{linkId}` → 204
- [ ] `POST /api/tasks/{id}/attachments` → 201
- [ ] `GET /api/tasks/{id}/attachments` → 200
- [ ] `DELETE /api/tasks/{id}/attachments/{id}` → 204
- [ ] `POST /api/tasks/{id}/relations` → 201 | 422
- [ ] `GET /api/tasks/{id}/relations` → 200
- [ ] `DELETE /api/tasks/{id}/relations/{id}` → 204
- [ ] `POST /api/tasks/{id}/branches` → 201
- [ ] `GET /api/tasks/{id}/branches` → 200
- [ ] `DELETE /api/tasks/{id}/branches/{branchId}` → 204

### Boards
- [ ] `POST /api/projects/{id}/boards` → 201
- [ ] `GET /api/projects/{id}/boards` → 200
- [ ] `GET /api/projects/{id}/boards/default` → 200 | 404 `BOARD_NOT_FOUND`
- [ ] `GET /api/boards/{id}` → 200
- [ ] `PATCH /api/boards/{id}` → 200
- [ ] `DELETE /api/boards/{id}` → 204
- [ ] `POST /api/boards/{id}/columns` → 201
- [ ] `PATCH /api/boards/{id}/columns/{id}` → 200
- [ ] `DELETE /api/boards/{id}/columns/{id}` → 204
- [ ] `POST /api/boards/{id}/move-task` → 200
- [ ] `POST /api/boards/{id}/remove-task` → 200

### Backlog
- [ ] `GET /api/projects/{id}/backlog` → 200

### Releases
- [ ] `POST /api/projects/{id}/releases` → 201
- [ ] `GET /api/projects/{id}/releases` → 200
- [ ] `GET /api/releases/{id}` → 200 | 404
- [ ] `PATCH /api/releases/{id}` → 200
- [ ] `POST /api/releases/{id}/close` → 200 | 422 `MILESTONE_HAS_OPEN_TASKS`
- [ ] `POST /api/releases/{id}/reopen` → 200
- [ ] `DELETE /api/releases/{id}` → 204

### Pages
- [ ] `POST /api/projects/{id}/pages` → 201 | 409 `SLUG_CONFLICT`
- [ ] `GET /api/projects/{id}/pages` → 200
- [ ] `GET /api/pages/{id}` → 200 | 404
- [ ] `PATCH /api/pages/{id}` → 200 | 422 `PAGE_ARCHIVED`
- [ ] `POST /api/pages/{id}/render-preview` → 200 `{"html":"..."}`
- [ ] `POST /api/pages/{id}/publish` → 200
- [ ] `POST /api/pages/{id}/archive` → 200
- [ ] `DELETE /api/pages/{id}` → 204
- [ ] `GET /api/pages/{id}/revisions` → 200
- [ ] `GET /api/pages/{id}/references` → 200
- [ ] `POST /api/pages/{id}/references/rebuild` → 200

### Search
- [ ] `GET /api/projects/{id}/search/tasks?q=` → 200
- [ ] `GET /api/projects/{id}/search/pages?q=` → 200

### SCM Integration
- [ ] `POST /api/projects/{id}/repository-connections` → 201
- [ ] `GET /api/projects/{id}/repository-connections` → 200
- [ ] `PATCH /api/repository-connections/{id}` → 200
- [ ] `DELETE /api/repository-connections/{id}` → 204

### Task Categories and Templates
- [ ] `POST /api/projects/{id}/task-categories` → 201
- [ ] `GET /api/projects/{id}/task-categories` → 200
- [ ] `PATCH /api/task-categories/{id}` → 200
- [ ] `DELETE /api/task-categories/{id}` → 204
- [ ] `POST /api/projects/{id}/task-templates` → 201
- [ ] `GET /api/projects/{id}/task-templates` → 200
- [ ] `GET /api/task-templates/{id}` → 200 | 404
- [ ] `PATCH /api/task-templates/{id}` → 200
- [ ] `DELETE /api/task-templates/{id}` → 204
- [ ] `POST /api/task-templates/{id}/instantiate` → 201

### Activity
- [ ] `GET /api/projects/{id}/activity` → 200
- [ ] `GET /api/tasks/{id}/activity` → 200

---

## 6. Test Suite Inventory

### Go API tests (`go test ./...`)

Tests require PostgreSQL. Set `TEST_DATABASE_URL` to a database the test runner can create schemas in. Each test creates and drops its own schema for isolation. Tests skip (not fail) when `TEST_DATABASE_URL` is not set.

```bash
cd octbase-api
TEST_DATABASE_URL="postgres://octbase:octbase@localhost:5432/octbase?sslmode=disable" \
go test ./...
```

| Package | Test file | Coverage |
|---|---|---|
| `shared` | `shared_test.go` | `IsValidUUID`, `NewUUID`, `Now`, `ParsePagination` |
| `workmanagement` | `domain_test.go` | `IsImmutable`, `SlugFromName`, all domain constants, `DomainError` |
| `workmanagement` | `service_test.go` | `AddRelation` (self, duplicate, cycle, success), `CloseRelease` (open tasks, success, DONE-only) |
| `workmanagement` | `handler_test.go` | Projects CRUD, tasks CRUD + filters, comments, links, attachments, relations, boards (columns, move, remove), backlog, releases, categories, templates |
| `docs` | `domain_test.go` | `RenderAsciiDoc` (headings, paragraph, list, bold, empty, HTML escaping, wrapper), `ExtractTaskReferences` |
| `docs` | `handler_test.go` | Pages CRUD, slug, publish, archive, render-preview, revisions, references, search |
| `identityaccess` | `handler_test.go` | `GetMe`, memberships CRUD |
| `scmintegration` | `handler_test.go` | Repository connections CRUD, branches CRUD |
| `activity` | `handler_test.go` | Project activity, task activity, pagination, actor ID |

All packages must report `ok`. No package may have test failures.

### Frontend tests (`pytest`)

> **Superseded:** Playwright's bundled Firefox/Chromium do not install on this
> OS — use the `frontend-testing` skill (system Chrome only) for setup and
> gotchas instead of the steps below.

Located in `octbase-frontend/tests/`. Require a running API at `http://127.0.0.1:8000` with demo seed data, Playwright, and pytest. The easiest way to get a running API is `podman-compose up --build` from the repo root.

Install dependencies — see the `frontend-testing` skill for the current, working setup (system Chrome via `OCTBASE_BROWSER=chrome`).

Run (from the tests directory):
```bash
cd octbase-frontend/tests
OCTBASE_API_BASE=http://127.0.0.1:8000 pytest -v
```

| Test file | Views covered |
|---|---|
| `test_projects.py` | Projects list, creation, navigation |
| `test_board.py` | Board columns, cards, creation, drag-and-drop, no-board state |
| `test_backlog.py` | Backlog listing, items, move-to-board |
| `test_tasks.py` | Task list, filter bar (status/priority/type/search) |
| `test_task_panel.py` | Panel open/close, all 7 tabs, details, copy/archive, comments, links, relations, attachments, branches, activity |
| `test_releases.py` | Release list, creation, edit, close/reopen, error on open tasks |
| `test_pages.py` | Page tree, view, edit, AsciiDoc preview, create, archive |
| `test_activity.py` | Activity feed, timestamps, refresh |

---

## 7. Known Gaps and Deferred Items

These gaps are **intentional prototype simplifications**. Do not treat them as bugs unless the review determines they block acceptance criteria.

### Permission enforcement not implemented
The `X-User-Id` header is required for write operations, but no role-based permission check is performed. Any authenticated user (valid UUID) can perform any write. A `PermissionChecker` application service is planned for a later version.

### Task status vocabulary
Design spec uses `REVIEW`; implementation uses `IN_REVIEW`. Both the frontend and OpenAPI spec must match the implementation value (`IN_REVIEW`).

### Task types
Design spec includes `TECH_DEBT | DOCS | SPIKE`. Implementation has `EPIC | CHORE` instead. Align or accept the delta.

### Release lifecycle
Design spec: `PLANNED → ACTIVE → CLOSED → ARCHIVED`. Implementation: `PLANNED | CLOSED` only. `ACTIVE` and `ARCHIVED` states are deferred.

### AsciiDoc renderer
The implementation uses a hand-rolled subset renderer: `=`/`==`/`===` headings, `* ` bullet lists, `**bold**` inline, and plain paragraphs. Tables, code blocks, admonitions, links, and includes are not supported. This is documented in the `octbase-api.md` recreation prompt and is intentional.

### Activity events not wired to all operations
Activity is recorded for: `TASK_CREATED`, `TASK_UPDATED`, `TASK_STATUS_CHANGED`, `TASK_COMMENT_ADDED`, `TASK_MOVED`, `MILESTONE_CLOSED`, `PAGE_PUBLISHED`, `BRANCH_CREATED`. Operations not generating activity: priority change, assign, copy, attachment add, link add, relation add.

### No file upload
Attachments are metadata-only (`filename`, `contentType`, `sizeBytes`, `externalUrl`). No binary upload is supported.

### Branch creation is fake
`POST /api/tasks/{id}/branches` stores the branch reference in the database but does not call any real Git provider. The `FakeGitProviderAdapter` pattern is satisfied.

---

## 8. Frontend Implementation Checklist

Verify each view exists and is reachable from the sidebar.

- [ ] Projects list (default view; delete button per card)
- [ ] Board (Kanban, columns, cards, drag-and-drop cross-column and same-column reorder, no-board empty state)
- [ ] Backlog (grouped by release, "→ Board" button)
- [ ] Tasks (table with filter bar: search, status, priority, type)
- [ ] Task panel (right drawer, 7 tabs: Details · Comments · Links · Relations · Attachments · Branches · Activity; delete buttons throughout)
- [ ] Releases (cards, create, edit, close/reopen, delete)
- [ ] Pages (tree + view/edit, AsciiDoc preview, publish, archive, delete, revisions)
- [ ] Repositories (list with delete, add form)
- [ ] Activity (reverse-chronological feed)

### Design system checks
- [ ] Sidebar: 220 px, dark blue (`#0747a6`), project nav and user avatar at bottom
- [ ] Status badges use class names: `badge-planned`, `badge-in-progress`, `badge-in-review`, `badge-done`, `badge-archived`
- [ ] Board column header uses `.status-dot.status-dot-{variant}` — no inline color style
- [ ] Priority dots: green (LOW), orange (MEDIUM), red (HIGH), purple (CRITICAL)
- [ ] Type badges: T/blue (TASK), B/red (BUG), S/green (STORY), E/purple (EPIC), C/gray (CHORE)
- [ ] Toast notifications: fixed bottom-right, auto-dismiss after 3.5 s
- [ ] Modal: max-width 480 px, backdrop dismisses on click
- [ ] Delete actions: 🗑 icon via `confirmDelete()` — never plain text "Delete" buttons or bare `✕`
- [ ] Task immutability: DONE/ARCHIVED tasks hide Save/edit controls, still show Copy
- [ ] No `style="..."` attributes in `app.js` template strings (`grep 'style="' app.js` → empty)

### Authentication
All write requests must include a valid JWT Bearer token: `Authorization: Bearer <token>`. Tokens are obtained via `POST /api/v1/auth/login`. The HTTP client auto-logs in as the demo user (`demo@octbase.dev` / `demo1234`) and attaches the token to every request.

---

## 9. Seed Data Verification

Activate with `OCTBASE_DEMO_MODE=true`. The seed runs on every startup as a **full upsert**: existing rows are reset to their canonical state via `ON CONFLICT (id) DO UPDATE SET ...`. This ensures the demo environment is predictable after test runs or manual changes.

| Resource | Expected state |
|---|---|
| Demo user | `demo@octbase.dev` / `Demo User` |
| Demo project | `Demo Project` — PUBLIC, ACTIVE |
| Task 1 | `Implement user authentication` — IN_PROGRESS, HIGH, assigned to demo user, release v1.0 Launch, board column "In Progress", has a comment/link/attachment/relation/branch |
| Task 2 | `Write API documentation` — PLANNED, MEDIUM, board column "Planned" |
| Board | `Main Board` — default, 4 columns (Planned/In Progress/Review/Done) |
| Release | `v1.0 Launch` — PLANNED, goal "Ship first version", dueDate 2024-06-01 |
| Page | `Getting Started` — PUBLISHED, 1 revision |
| Repository | `FAKE_GITLAB` provider, `https://gitlab.example.com/demo/octbase` |
| Activity | 1 seeded entry: `TASK_CREATED` for Task 1 |

Verify seed IDs are deterministic (all begin with `00000000-0000-...`).

---

## 10. Acceptance Criteria

The prototype is acceptable when all of the following are true:

1. `TEST_DATABASE_URL=... go test ./...` passes with zero failures across all packages.
2. `podman-compose up --build` (from repo root) starts both services (postgres + API) without errors.
3. Frontend SPA loads at **http://localhost:8000/** (served by the API container). SPA also accessible via `file://` from `octbase-frontend/index.html?apiBase=http://127.0.0.1:8000`.
4. `GET http://localhost:8000/health` returns `{"status":"ok","database":"ok"}`.
5. Demo project, tasks, board, release, and page are visible after seed.
6. Tasks can be created, edited, archived, reopened, copied, commented on, related, and searched.
7. A task cannot be edited when DONE or ARCHIVED (UI hides controls; API returns 422).
8. Release cannot be closed while open tasks are assigned (API returns 422 `MILESTONE_HAS_OPEN_TASKS`).
9. Page AsciiDoc content renders to HTML on save and on preview (without modifying the page).
10. Publishing a page creates an immutable revision visible via `GET /api/pages/{id}/revisions`.
11. Backlog query returns only non-DONE/ARCHIVED tasks with no board column assigned.
12. Moving a task on the board does not automatically change its status.
13. A fake branch reference can be created from a task via `POST /api/tasks/{id}/branches`.
14. Activity feed records task creation, status changes, comments, board moves, page publishes, and branch creation.
15. All domain constants in Go, the OpenAPI spec, and the frontend JavaScript match the canonical values in Section 3.

---

## 11. How to Use This Prompt

### Before a code review
Work through Sections 4–8 systematically. Mark each item pass/fail.

### Before committing a new feature
1. Check that the feature is in scope according to `draft/Octbase_Architecture_With_Go_API_Prototype.md`.
2. Add domain/service/handler tests following existing patterns.
3. Run `go test ./...` — must pass.
4. Verify the OpenAPI spec is updated if a new endpoint is added.
5. Verify the frontend is updated if the feature has a UI component.
6. Update the seed if the feature has demo data implications.

### When onboarding a new developer
Walk through Section 1 (overview), Section 3 (constants), Section 4 (business rules), and Section 9 (seed data) in that order.

### When planning the next version
Refer to Section 7 (known gaps) to identify which deferred items are ready to be promoted to in-scope.
