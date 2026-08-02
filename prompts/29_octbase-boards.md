Act as a senior full-stack engineer working on Octbase, an existing Go (chi + PostgreSQL) task-management SaaS with a React-free vanilla-JS frontend (`octbase-frontend/js/app.js`). This prompt optimizes **boards**: read-permission gating, owner-created boards with localized default columns, cross-board linked columns, a toggleable backlog column, and surfacing boards/projects on the My Work screen.

Several pieces below already exist in the codebase — **read the "Current state" section first and extend what is there rather than rebuilding it.** Reuse existing patterns: `memberGuard`/`requirePermission`, `rbac.HasPermission`, `auditlog.Repo.Write()`, idempotent numbered `up`/`down` migrations, existing `{ code, message, messageKey, details }` error shape, and the existing i18n setup (`octbase-frontend/locales/en.json` + `de.json`).

## Current state (read before designing)

- **Project roles** (`memberships.role`): `PROJECT_OWNER` > `PROJECT_ADMIN` > `PROJECT_MEMBER` > `PROJECT_VIEWER`. `SUPER_ADMIN` global role bypasses all project checks.
- **Authorization**: pure functions + permission keys in `internal/rbac/rbac.go`. `HasPermission(globalRole, projectRole, permission)` is the source of truth; keys include `project.view`, `task.view`, `task.create`, etc. There is currently **no** `board.*` permission key.
- **Guards** (`internal/workmanagement/handler.go:208`): `memberGuard(projectID)` only checks *membership* (any role) and returns 403 for non-members. `requirePermission(projectID, permission)` layers a `HasPermission` check on top and returns 409 `PROJECT_ARCHIVED` for writes on archived projects.
- **Project listing**: `ListProjects` (`project_handler.go:82`) already filters to the caller's projects via `h.projects.List(userID,...)` (super admin → `ListAll`). **But** `GetProject` only blocks non-members when `Visibility == VisibilityPrivate`, so a *public* project's metadata is readable by non-members while its boards/backlog are not (they use `memberGuard`). This inconsistency must be resolved (section 1).
- **Boards**: `CreateBoard` (`board_handler.go:12`) requires `RequireWriter(role)` — i.e. `PROJECT_MEMBER`+, **not** owner-only — and seeds **no** columns. Boards have `min_columns`/`max_columns` (default 1/10), `is_sprint_board`, `sprint_id` (migration `012_board_config`). Lane CRUD: `AddColumn`/`UpdateColumn`/`DeleteColumn`. The only place default columns exist today is the demo seed (`internal/seed/seed.go:90` — "In Progress"/"Done", hardcoded English).
- **Columns**: `board_columns(name, status, position, ...)`. `status` is one of `PLANNED`, `IN_PROGRESS`, `IN_REVIEW`, `DONE`, `ARCHIVED` (`domain.go:201`). Unique index on `(board_id, status)` (`002_constraints.up.sql:5`) — a board cannot have two columns with the same status.
- **Cross-board columns (already built)**: `board_external_columns(board_id, source_column_id, position)` + handlers `ListExternalColumns`/`AddExternalColumn`/`DeleteExternalColumn` (`board_handler.go`). Enforces same-project (`EXTERNAL_COLUMN_CROSS_PROJECT`), no same-board (`EXTERNAL_COLUMN_SAME_BOARD`), no duplicates. Frontend API is wired (`app.js:383-385`) and i18n keys exist (`board.addExternalColumn`, `board.externalColumnHint`, …). These are read-only by nature. **What is missing: deliberate right-side placement, a clear "linked column" label, and visible source board/column identification.**
- **Backlog**: `GetBacklog` (`board_handler.go:597`) returns project tasks not on the board. The frontend renders the backlog as a **separate view** (`setView('backlog')`, `renderBacklog`), not as a column on the board. i18n keys `nav.backlog`, `task.createBacklogItem` exist.
- **My Work / Dashboard**: `GetDashboard` (`search_handler.go:68`) returns `assignedTasks`, `reviewingTasks`, `recentPages`, `upcomingReleases`. It does **not** return the user's accessible projects or boards. Frontend My Work = the `dashboard` view (`app.js`, `nav.myWork`).

## Goal

Deliver a single, coherent boards experience that is correctly permission-gated and adds the four capabilities the product owner asked for. Produce a **technical proposal + implementation** broken into the sections below. Keep changes additive and backward-compatible; do not regress existing board/external-column/backlog behavior.

---

### 1. Read-permission gating (foundational — do this first)

**Requirement:** A user must only ever see a project — and therefore its boards, columns, and tasks — if they have at least read access to that project. No read access ⇒ the project, its boards, and its tasks are entirely invisible (not listed, not fetchable, no leaked existence).

- Define **read access** explicitly as `rbac.HasPermission(globalRole, projectRole, rbac.PermProjectView)` for a member, plus `SUPER_ADMIN`. Decide and document how `Visibility` (`public`/`private`) interacts: recommend that *visibility never grants access on its own* — non-members get nothing even for "public" projects (membership is required for read). If "public" must remain org-readable, define it as an explicit, auditable rule, not an accidental gap. Resolve the `GetProject` public-visibility inconsistency noted in Current state.
- Audit every board/column/backlog/external-column read endpoint (`ListBoards`, `GetBoard`, `GetDefaultBoard`, `GetBacklog`, `ListExternalColumns`, plus task reads) and ensure they enforce read access consistently. Where they use bare `memberGuard`, decide whether to upgrade to `requirePermission(projectID, PermProjectView)` so a future read-less role is handled correctly (today every member role can view; design so that's a policy in `rbac`, not an accident of `memberGuard`).
- For not-permitted access return the project's existing not-found/forbidden shape **without disclosing existence** where appropriate (prefer 404 over 403 for cross-project resource probing, e.g. fetching a board by id whose project the caller can't read).
- Tests: a `PROJECT_VIEWER` can read boards/tasks; a non-member gets nothing from list endpoints and 404/403 from by-id endpoints; super admin sees all; archived-project reads still allowed, writes still blocked.

### 2. Owner-created boards with localized default column sets (Scrum & Kanban)

**Requirement:** A project owner can create a new board pre-populated with a sensible default set of columns, choosing a **Scrum** or **Kanban** template, with column names in the user's language.

- **Permission:** introduce a `board.create` permission key in `rbac.go` and gate `CreateBoard` on it. Map it so at least `PROJECT_OWNER` can create boards; **recommend** `PROJECT_OWNER` + `PROJECT_ADMIN` (document the final matrix and cross-check it doesn't widen access beyond today's `RequireWriter`, which allowed `PROJECT_MEMBER`+ — this is an intentional *tightening*; call it out and update tests).
- **Templates:** extend `CreateBoard` (or add `POST /api/v1/projects/{projectId}/boards` body field `template: "kanban" | "scrum" | "none"`) to seed default columns atomically in the same transaction as the board. Suggested defaults (map each to an existing `status`, respecting the unique `(board_id, status)` index):
  - **Kanban:** To Do (`PLANNED`) · In Progress (`IN_PROGRESS`) · Done (`DONE`).
  - **Scrum:** To Do (`PLANNED`) · In Progress (`IN_PROGRESS`) · In Review (`IN_REVIEW`) · Done (`DONE`); set `is_sprint_board` per existing sprint linkage.
- **Localization:** column `name` is a stored, user-renamable string, so localize **at creation time** from the creator's locale rather than storing an i18n key. Provide the template label set in `en.json` and `de.json` (e.g. `board.template.kanban.todo`, `…inProgress`, `…inReview`, `…done`) and resolve them server-side from the request locale (or have the frontend send resolved names). Document the chosen approach; ensure existing rename still works afterward.
- Validate seeded column count against the board's `min_columns`/`max_columns` (`ValidateLaneLimits`).
- Tests: owner creates a Kanban board → 3 correctly-named/ordered/status-mapped columns; Scrum → 4; German locale → German names; `PROJECT_MEMBER` is now rejected with 403; column count respects lane limits.

### 3. Linked columns from other boards (right-side, labeled)

**Requirement:** A single column from another board can be linked into my board so I can see that column's tasks; it appears on the **right** of my board and is clearly shown as a **linked (read-only) column**, naming its source board and column.

- Reuse the existing `board_external_columns` table and `Add/List/DeleteExternalColumn` handlers — do **not** add a parallel mechanism. They already enforce same-project, no-same-board, no-duplicate.
- **Placement:** linked columns render to the right of all the board's own columns. Decide whether `position` orders them among themselves on the right (recommend yes) while always rendering after own columns; document it.
- **Read-only + identification:** the linked column is non-interactive (no add/move/edit/remove of its tasks from this board — the source is authoritative). Render a distinct visual treatment and a label using/extending the existing i18n (`board.externalColumnHint` etc.), showing **source board name + source column name** so it's unambiguous. The `GET …/external-columns` response (or `GetBoard`) must include enough denormalized info (source board name, source column name, source column's tasks) for the frontend to render without N+1 calls — extend the repo/DTO if needed.
- Owner/writer-gated add/remove (already `RequireWriter`); align with the section-2 permission decision if linked-column management should also be owner/admin only.
- Tests (Go + Playwright): adding a column from another board in the same project succeeds and renders on the right labeled as linked; its tasks cannot be dragged/edited from the consuming board; removing it doesn't touch the source; cross-project still rejected.

### 4. Toggleable backlog column (left side, show/hide)

**Requirement:** I can show or hide the backlog as a separate column on the **left** of my board whenever I want.

- Surface the existing `GetBacklog` data as an optional **leftmost** column on the board view (distinct from, and without removing, the standalone backlog view). A show/hide toggle controls its visibility.
- **Persistence:** recommend a per-user, per-board UI preference. Simplest: client-side (`localStorage`, keyed by board id) — no migration, low risk. If the product wants it to follow the user across devices, store it on a per-user board-preference row instead; pick one and document the trade-off (recommend client-side for v1).
- Interaction: dragging a task from the backlog column into a board lane uses the existing `MoveTask`/board-add flow; removing from the board returns it to the backlog (existing `RemoveTaskFromBoard` / `nav.movedToBacklog`). Keep the backlog column read-aware: a `PROJECT_VIEWER` sees it but cannot drag.
- i18n: reuse `nav.backlog`; add a show/hide toggle label in `en.json`/`de.json`.
- Tests: toggle shows/hides the left backlog column; state persists across reload; drag from backlog → lane moves the task off the backlog; viewer cannot drag.

### 5. My Work shows accessible boards and projects

**Requirement:** The My Work screen lists the boards I have access to as well as the projects I can access.

- Extend `GetDashboard` (`search_handler.go:68`) to also return:
  - `projects`: the caller's accessible projects (same read-access rule as section 1; reuse `h.projects.List(userID,…)` logic, don't duplicate filtering).
  - `boards`: boards across those accessible projects (cap/paginate sensibly, e.g. recent or default boards per project), each with project id/name for grouping. Ensure a non-member's boards never appear.
- Frontend: render two new sections on the My Work (`dashboard`) view linking into each project's board, reusing existing card/list components and the `nav.myWork` framing. Add any new i18n keys to both locale files.
- Tests: dashboard returns only accessible projects/boards; a project the user was removed from disappears; super admin sees broadly; Playwright check that My Work renders the projects and boards sections and they navigate correctly.

---

### 6. Data model & migrations

- Add a new idempotent `up`/`down` migration with the **next free number** (current latest is `014_task_pinned`; check `migrations/` for the next free integer at implementation time since other prompts may claim it). Only needed if you add server-side persistence (e.g. `board.create` is code-only and needs no migration; a per-user backlog-toggle preference would).
- Preserve the unique `(board_id, status)` constraint when seeding template columns — two columns can't share a status.
- No breaking changes to `boards`, `board_columns`, or `board_external_columns` schemas.

### 7. API design

Extend the existing surface; don't invent a new prefix.
- `POST /api/v1/projects/{projectId}/boards` — add `template` and locale handling (section 2).
- Read endpoints — enforce `PermProjectView` consistently (section 1).
- `GET /api/v1/boards/{boardId}/external-columns` and/or `GET /api/v1/boards/{boardId}` — include denormalized source board/column names + tasks for linked columns (section 3).
- `GET /api/v1/users/me/dashboard` — add `projects` and `boards` (section 5).
- Follow existing success (`{ id, …, createdAt, updatedAt }`) and error (`{ code, message, messageKey, details }`) shapes.

### 8. Frontend considerations (`octbase-frontend/js/app.js`)

- Board layout order: **[optional backlog column] · own columns · [linked columns]** (left → right).
- Gate the "Create board" UI and template picker on the section-2 permission; gate linked-column add/remove and backlog drag on write access (consume server-provided permissions rather than re-deriving roles where possible — see existing `AppPerms`).
- All new strings in both `en.json` and `de.json`; keep both files key-aligned (both are currently 693 lines).
- Run the `frontend-testing` skill before any visual verification/screenshots (per CLAUDE.md).

### 9. Testing

- Go: table-driven `rbac` tests for the new `board.create` key × each role; handler/integration tests for board template seeding, read-permission gating (non-member invisibility), linked-column read-only enforcement, and dashboard projects/boards filtering. Extend existing `board_handler`/`board_config`/`search_dashboard` tests rather than duplicating.
- Frontend (Playwright, `octbase-frontend/tests`): owner board creation with template + locale, linked column rendered read-only on the right, backlog column show/hide + persistence, My Work projects/boards sections. Note the known pre-existing failure `test_sidebar_shows_all_projects` (don't attribute it to this change).

## Constraints

- Additive and backward-compatible; reuse `board_external_columns`, `GetBacklog`, `memberGuard`/`requirePermission`, `HasPermission`, `auditlog`, idempotent numbered migrations, and existing response/error shapes.
- The backend is authoritative for every permission decision; frontend checks are UX only.
- The single intentional tightening (board creation moving from `PROJECT_MEMBER`+ to owner/admin) must be explicitly documented and reflected in updated tests; nothing else should reduce access for existing roles.
