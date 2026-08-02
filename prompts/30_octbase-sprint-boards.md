Act as a senior full-stack engineer working on Octbase, an existing Go (chi + PostgreSQL) task-management SaaS with a React-free vanilla-JS frontend (`octbase-frontend/js/app.js`). This prompt adds **sprint boards**: a board that is born with a sprint, lives only while that sprint runs, mirrors the project's main board lanes, surfaces in the menu as **SprintBoard**, is unique per project (one active at a time), and is backed by a hard rule that sprints may not overlap in time.

Several pieces below already exist in the codebase — **read the "Current state" section first and extend what is there rather than rebuilding it.** Reuse existing patterns: `memberGuard`/`requirePermission`, `rbac.HasPermission`, the `Service` domain layer + `DomainError` (`writeDomainError`), `shared.WithTx` + `*Tx` repo methods for atomic multi-row writes, `activity.Write(...)` for the activity feed, idempotent numbered `up`/`down` migrations, the existing `{ code, message, messageKey, details }` error shape, and the existing i18n setup (`octbase-frontend/locales/en.json` + `de.json`, currently key-aligned).

## Current state (read before designing)

- **Sprint domain** (`workmanagement/domain.go:157`): `Sprint{ID, ProjectID, Name, Goal, StartDate *string, EndDate *string, Status, ReleaseID *string, CreatedAt, UpdatedAt, Version}`. `StartDate`/`EndDate` are **optional** ISO date strings stored as `TEXT` (`migrations/007_sprints.up.sql`). Statuses (`domain.go:221`): `PLANNED` → `ACTIVE` → `COMPLETED`.
- **Sprint lifecycle handlers** (`workmanagement/sprint_handler.go`, routed in `handler.go:191-197`): `CreateSprint` (creates as `PLANNED`), `ListSprints`, `GetSprint`, `UpdateSprint` (blocks edits once `COMPLETED`), `StartSprint`, `CompleteSprint`, `DeleteSprint`. All gate writes with `memberGuard` + `shared.RequireWriter(role)` — note this is **not** the newer `rbac` permission path used by boards (see below).
- **One active sprint already enforced — but only at start time.** `Service.StartSprint` (`service.go:240`) calls `sprints.FindActive(projectID)` and returns `DomainError{Code:"SPRINT_ALREADY_ACTIVE"}` if another `ACTIVE` sprint exists. `Service.CompleteSprint` (`service.go:254`) calls `sprints.ClearIncompleteTasks(sprintID)` (moves unfinished tasks back to the backlog) then sets `COMPLETED`. `SprintRepo.FindActive` (`repo.go:1272`) is the single-active lookup.
- **No overlap prevention exists.** `CreateSprint`/`UpdateSprint` validate only that `name` is non-blank; nothing checks `startDate`/`endDate` against other sprints. `grep -rni overlap` over the Go code returns nothing.
- **Boards already know about sprints (partial, manual).** Migration `012_board_config` added `boards.is_sprint_board` and `boards.sprint_id (REFERENCES sprints(id) ON DELETE SET NULL)`. `Board` (`domain.go:97`) carries `IsSprintBoard bool`, `SprintID *string`, and a read-populated `Sprint *Sprint`. Today a board is flagged a sprint board and linked **manually** through board settings (`app.js` ~3246, `bs-sprint` checkbox + `bs-sprint-id` select; `boardToolbar` ~2881 renders a sprint-timing banner). There is **no auto-creation of a board when a sprint is created**, and no lifecycle coupling.
- **Board creation & column seeding (reuse this).** `CreateBoard` (`board_handler.go:14`) is gated on `rbac.PermBoardCreate`, accepts `template: "kanban"|"scrum"|"none"` + `locale`, and seeds default columns **atomically** in `shared.WithTx` via `boards.CreateTx` + `columns.CreateTx` (`board_handler.go:71`). Column name templates live in `board_templates.go` (`templateColumnsFor`, `IsValidBoardTemplate`). `resolveBoardSprint` (`board_handler.go:193`) validates a board↔sprint link is same-project.
- **Main board = the default board.** `boards.FindDefault(projectID)` / `GetDefaultBoard` (`board_handler.go:102`) returns the project's `IsDefault` board. Its own lanes are `board_columns(name, status, position)`; unique index on `(board_id, status)` (`002_constraints.up.sql`) means a board can't have two columns with the same status.
- **Own vs linked columns.** A board's own lanes are `board_columns`. **Linked/external** lanes are `board_external_columns` (read-only views of another board's column; `domain.go:125`, `Board.ExternalColumns`). The product requirement here is "the same lanes (not the linked ones)" — i.e. copy the main board's **own** `board_columns`, **excluding** any `board_external_columns`.
- **Frontend nav** (`app.js` ~1994): the per-project view list is `board, backlog, sprints, releases, pages, repos, activity`, each with an icon + label from `nav.*`. There is **no** `sprintBoard` view yet (the string "SprintBoard" currently appears only in code comments). i18n already has `nav.board`, `nav.sprints`, `sprint.*`, and the activity strings `SPRINT_STARTED`/`SPRINT_COMPLETED`.
- **Migrations:** latest is `014_task_pinned`; the next free number is **015** (re-check `migrations/` at implementation time in case another prompt claims it).

## Goal

When a sprint exists and is running, the project has exactly one **sprint board** tied to it: created with the sprint, seeded from the main board's own lanes, shown in the menu as **SprintBoard**, and torn down (or archived) when the sprint completes. Back this with a no-overlapping-sprints rule so "one running sprint board" is well-defined. Produce a **technical proposal + implementation** broken into the sections below. Keep changes additive and backward-compatible; do not regress the existing manual `isSprintBoard` linkage, board templates, external columns, backlog, or sprint lifecycle.

---

### 1. A sprint owns its board (auto-create, lifecycle-coupled)

**Requirement:** When a sprint is created it must have its own board assigned. That board exists as long as the sprint is running; it goes away when the sprint is no longer running.

- **Decide the trigger and document it.** A sprint is created `PLANNED` and only becomes "running" at `StartSprint` (`ACTIVE`). Recommend tying the board's *existence* to the **running** state: create/activate the sprint board in `Service.StartSprint` and tear it down in `Service.CompleteSprint`, so "exists as long as the sprint is running" is literal. If the product instead wants the board visible from creation, create it in `CreateSprint` but keep it hidden/inactive until start — pick one, state the trade-off, and make the menu (section 4) reflect only the *running* board.
- **Atomic creation.** Reuse the `CreateBoard` seeding pattern: in one `shared.WithTx`, insert the board (`boards.CreateTx`) with `IsSprintBoard = true` and `SprintID = sprint.ID`, then insert its seeded columns (`columns.CreateTx`). A failure mid-seed must leave no partial board.
- **Teardown.** On `CompleteSprint` (and on `DeleteSprint` of a running sprint), remove or archive the sprint board. Because `boards.sprint_id` is `ON DELETE SET NULL`, deleting the sprint alone would orphan the board as a normal board — that's wrong here; explicitly delete/archive the sprint board in the same transaction. Recommend hard-delete for a clean menu; if task history must survive, archive instead and document where completed-sprint boards live. Move-back-to-backlog of unfinished tasks is already handled by `ClearIncompleteTasks` — don't duplicate it.
- **Idempotency & safety:** starting a sprint that already has a board must not create a second one; completing/deleting twice must not error. Guard on `boards.sprint_id`.
- Emit activity via the existing `activity.Write(projectID, "", actorID, ...)` calls already present in `StartSprint`/`CompleteSprint` (extend payloads if useful; reuse `SPRINT_STARTED`/`SPRINT_COMPLETED` rather than inventing parallel events unless a board-specific event is clearly warranted).

### 2. Seed the sprint board from the main board's own lanes

**Requirement:** The sprint board contains the same lanes as the main board — **the board's own columns, not the linked (external) ones**.

- Source = `boards.FindDefault(projectID)` (the `IsDefault` main board). Copy its `board_columns` (`name`, `status`, `position`) into new `board_columns` rows for the sprint board, preserving order. **Do not** copy `board_external_columns` — linked columns are explicitly excluded.
- This is a **point-in-time copy**, not a live mirror: later edits to the main board do not retro-change a running sprint board (call this out; a live mirror is a much larger feature and not requested).
- Respect the unique `(board_id, status)` index — the main board already satisfies it, so a faithful copy will too; assert it. Respect the new board's `min_columns`/`max_columns` (`ValidateLaneLimits`); if the main board has more lanes than the sprint board's max, document the resolution (recommend inheriting the main board's limits so the copy always fits).
- **Edge case:** project has no default board, or the default board has zero columns. Recommend falling back to the existing `templateColumnsFor(..., "scrum"/"kanban", locale)` seed (Scrum fits sprints) and documenting the fallback. Never create a board with fewer than `min_columns` lanes.

### 3. One sprint board active at a time

**Requirement:** Only one sprint board can be active at a time.

- This should fall out of the existing **one-active-sprint** invariant (`StartSprint` → `FindActive` → `SPRINT_ALREADY_ACTIVE`) *if* the board's lifecycle is bound to the running sprint (section 1). Make that coupling explicit: at most one board per project may have a non-archived row with `is_sprint_board = 1` and a `sprint_id` whose sprint is `ACTIVE`.
- Add a defensive guard so the rule holds even if a board is created out-of-band: before creating/activating a sprint board, verify no other active sprint board exists for the project; if one does, return a `DomainError` in the existing shape (e.g. reuse `SPRINT_ALREADY_ACTIVE`, or add `SPRINT_BOARD_ALREADY_ACTIVE` — pick one, keep messages localizable via `messageKey`).
- Consider a partial unique index enforcing it at the DB level (e.g. unique on `project_id` where `is_sprint_board` and the linked sprint is active) — recommend it if expressible cleanly; otherwise enforce in the service layer and document why.

### 4. Show the running sprint board in the menu as "SprintBoard"

**Requirement:** It can be displayed in the menu as **SprintBoard**.

- Add a `sprintBoard` entry to the per-project nav view list (`app.js` ~1994), with an icon (reuse the `sprint` icon or pair it with `board`) and a `nav.sprintBoard` label = "SprintBoard" (EN) / a suitable DE string. Add the key to **both** `en.json` and `de.json`, keeping them key-aligned, and add it to the view-title map (`app.js` ~667).
- **Only show the entry when a running sprint board exists** for the active project (gate on the active sprint / the board's presence). When there's no active sprint, the entry is hidden — don't render a dead link.
- Selecting it loads the sprint board through the existing board view/render path (it's a normal board with `isSprintBoard = true`), so the existing `boardToolbar` sprint banner, drag/move (`MoveTask`), and permission gating apply unchanged. Don't fork the board renderer.
- Keyboard shortcut: optional — if added, follow the existing pattern (the view list carries `key:`) and register it in the shortcuts help (`shortcuts.*` i18n) for both locales.

### 5. No overlapping sprints

**Requirement:** It is not possible to create overlapping sprints.

- Define overlap on the date range `[startDate, endDate]` within a **project**: two sprints overlap when `a.start <= b.end AND b.start <= a.end` (standard inclusive interval test). Enforce on **both** `CreateSprint` and `UpdateSprint` (excluding the sprint being edited from its own check).
- **Handle open-ended/missing dates deliberately** — today both `startDate` and `endDate` are nullable. Decide and document: recommend requiring both dates for any sprint that will be started, and treating a sprint missing a bound as either non-overlapping (lenient) or rejected (strict). State the rule; don't leave it implicit.
- Enforce in the domain/service layer (a new `Service` method or repo query like `FindOverlapping(projectID, start, end, excludeID)`), returning a `DomainError` (e.g. `SPRINT_OVERLAP`) in the existing error shape with a `messageKey` for i18n. Validate server-side regardless of the frontend.
- **Tighten the write path consistently:** sprint writes currently use `memberGuard` + `RequireWriter`, while boards moved to `rbac.PermBoardCreate`. Auto-creating a board from a sprint action blurs that line — decide whether sprint create/start should require the same board-create permission, or whether the auto-created board bypasses the board-create permission because it's a side effect of an authorized sprint action (recommend the latter: a writer who can start a sprint implicitly gets its board). Document the final matrix; don't silently widen who can create boards.

---

### 6. Data model & migrations

- Reuse `boards.is_sprint_board` / `boards.sprint_id` and `board_columns` — **no new board tables needed** for the copy. A migration is only required if you add the section-3 partial unique index or any new column (e.g. a soft-archive flag for completed-sprint boards). If you add one, use the next free number (**015**; verify at implementation time), make it idempotent (`IF NOT EXISTS` / guarded), and provide a matching `down`.
- Preserve the unique `(board_id, status)` constraint when copying lanes.
- No breaking changes to `sprints`, `boards`, `board_columns`, or `board_external_columns`.

### 7. API design

Extend the existing surface; don't invent a new prefix or duplicate sprint endpoints.
- `POST /api/v1/sprints/{sprintId}/start` — also provisions the sprint board (section 1); response unchanged or extended with the board id.
- `POST /api/v1/sprints/{sprintId}/complete` and `DELETE /api/v1/sprints/{sprintId}` — also tear down / archive the sprint board.
- `POST /api/v1/projects/{projectId}/sprints` and `PATCH /api/v1/sprints/{sprintId}` — enforce no-overlap (section 5).
- Reads: the existing `GET /api/v1/boards/{boardId}` already populates `Board.Sprint`; ensure the frontend can locate the running sprint board for a project (reuse `ListBoards`/`ListSprints` + `FindActive`, or add a small lookup — don't add a heavy new endpoint if an existing list suffices).
- Follow existing success (`{ id, …, createdAt, updatedAt }`) and error (`{ code, message, messageKey, details }`) shapes; surface new domain errors through `writeDomainError`.

### 8. Frontend considerations (`octbase-frontend/js/app.js`)

- Add the `sprintBoard` nav entry (section 4), shown only when a running sprint board exists; reuse the existing board view/render path and `boardToolbar` banner.
- Surface the new validation errors (overlap, already-active) as inline form errors / toasts using their `messageKey`, consistent with existing sprint form handling (`sprint.*` keys).
- All new strings in **both** `en.json` and `de.json`, key-aligned. Reuse `nav.*`, `sprint.*`, and activity keys where they exist.
- Run the `frontend-testing` skill before any visual verification/screenshots (per CLAUDE.md).

### 9. Testing

- **Go:** service/handler tests for: starting a sprint provisions a board seeded from the main board's own lanes (and excludes external columns); completing/deleting a running sprint removes/archives its board and is idempotent; only one active sprint board per project (second start rejected); overlap rejected on create and update with the boundary cases (touching, contained, open-ended dates); the lane-copy fallback when the project has no default board. Extend existing `service_test.go`, `board_config_test.go`, `perms_test.go`, and sprint/handler tests rather than duplicating fixtures.
- **Frontend (Playwright, `octbase-frontend/tests`):** SprintBoard menu entry appears only while a sprint is running, navigates to the board, and disappears on completion; creating an overlapping sprint shows the validation error. Note the known pre-existing failure `test_sidebar_shows_all_projects` (don't attribute it to this change).

## Constraints

- Additive and backward-compatible; reuse the `Service`/`DomainError` layer, `shared.WithTx` + `*Tx` repos, `boards`/`board_columns`/`board_external_columns`, `FindDefault`, `FindActive`/`ClearIncompleteTasks`, `templateColumnsFor`, `activity.Write`, idempotent numbered migrations, and existing response/error shapes.
- The backend is authoritative for every rule (one-active-board, no-overlap, lifecycle); frontend checks are UX only.
- The sprint board is a point-in-time copy of the main board's own lanes, not a live mirror, and never copies linked/external columns — state this in the proposal.
- Do not regress the existing manual `isSprintBoard` linkage, board templates, external columns, backlog, or the current sprint lifecycle; any intentional tightening (e.g. requiring both sprint dates, permission changes) must be called out explicitly and reflected in tests.
