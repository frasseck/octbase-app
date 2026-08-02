Act as a senior full-stack engineer working on Octbase, an existing Go (chi + PostgreSQL) task-management app with a build-free, vanilla-JS frontend (`octbase-frontend/js/*.js`, classic `<script>`s loaded in dependency order via `index.html`). This prompt introduces a **configurable task-list module** and uses it to ship a new **Task view** — a *classic management layer* over the same tasks the kanban board manages.

The **overriding requirement is a shared, configurable foundation**, not a one-off view. Task management is the core of Octbase, so the list of tasks — and the task API endpoints behind it — must become a **single reusable module that both the Backlog and the new Task view are built on**. "Backlog" and "Task view" should be two *configurations* of one engine, not two parallel code paths. Treat "one configurable base, used twice" as the primary acceptance criterion; the specific Task view is the proof that the base is reusable.

Two clarifications that set the priorities:

- **The module + its endpoints are bedrock, not a disposable feature.** The Backlog depends on this module too, so the module is *not* meant to be deleted — it is meant to be *configured*. A per-deployment toggle can hide the **Task view entry**, but the shared engine and the task endpoints (`list` with filters/paging, `status`, `bulk`, `search`) are the documented contract everything else stands on. Solidify that contract.
- **Drag-and-drop is optional, not crucial.** It is a per-configuration toggle, off by default for the Task view in its first iteration. The headline affordances of the management layer are **fulltext search, status grouping, and bulk edits** — the operations a board can't do well — not kanban gestures. Build the module so D&D can be switched on later without rework, but do not let it drive the design or the tests.

A standalone Tasks list view existed in an **earlier, pre-split (monolithic `app.js`) version** and was removed as redundant with the backlog (see the docstring in `octbase-frontend/tests/test_tasks.py`: *"The standalone Tasks list view was removed (it was redundant with the Backlog)"*). The reason it was redundant is exactly what this prompt fixes: there was no shared abstraction, so a second list meant duplicated code. This time the list is **one module**, the Task view is a **management layer** (cross-cutting status overview + bulk ops), and the Backlog is re-expressed on the same engine.

**Read the "Current state" section first and reuse existing patterns** rather than inventing parallel ones: `applyTaskFilters`/`filterTasksBySearch` (`state.js`), `statusBadge`/`STATUSES`/`STATUS_META` (`meta.js`, shared), the backlog list/row rendering (`views-board.js`), the bulk-action bar (`views-shell.js`), the hash router (`api.js`), and the i18n setup (`locales/en.json` + `de.json`).

## Current state (read before designing)

- **The backlog list is the latent "module" to extract.** `renderBacklog` (`views-board.js:653`) fetches `api.tasks.list(pid,{size:200})` into `_backlogTasks`, filters with `applyTaskFilters(..., {backlogOnly:true})`, and renders a header + `.backlog-list` body. The body is built by `backlogListInner(tasks)` (`views-board.js:694`); a search keystroke calls `refreshBacklogList` (`views-board.js:700`), which re-renders **only** the list body via `backlogListInner` so input focus is preserved. Rows come from `backlogRow` (`views-board.js:724`): checkbox, type badge, seq, title (opens the task panel), priority dot, **status badge**, assignee, due. This fetch → filter → group → list-body → focus-preserving-refresh shape is the engine to generalize.
- **Filtering is centralized — reuse it.** `applyTaskFilters(tasks, {boardOnly, backlogOnly, ignoreSearch})` and `filterTasksBySearch(tasks, {fulltext})` (`state.js:110-137`) are the single source of truth for type/priority/status/search filtering. `taskFilterParams()` serializes filters to the URL. Do not re-implement filtering.
- **The task endpoints are the base contract.** `api.tasks.list(pid, p={})` (`api.js:204`) already takes query params (`qs(p)`), `api.tasks.status(id, s)` (`api.js:208`), `api.tasks.bulk(pid, d)` (`api.js:216`), and `api.tasks.search(pid, q)` (`api.js:215`) exist. The module is built on these. **Decide and document** whether the management view's data needs server-side filtering/paging (status/`q`/`size`) rather than the backlog's fetch-200-then-filter-client-side approach, and flag any endpoint gap as an explicit decision rather than silently scaling the client fetch.
- **View registration is two hard-coded lists.** The project sidebar builds a `views` array (`views-shell.js:39`) — `board`, `backlog`, `sprints`, `releases`, `pages`, `repos`, `activity`, `archive` — and `renderContent` dispatches on `S.view` via a `switch` (`views-shell.js:332`, no `default`). `setView(v)` (`views-shell.js:301`) clears selection, updates the URL, and re-renders.
- **The router has no view allowlist.** `handleRoute` (`api.js:377`) matches `^/projects/([^/]+)(?:/(.+))?$` and sets `S.view = sub` directly (default `board`, `api.js:412`), then restores `priority|status|type|q` filters. So **any** `/projects/:id/<anything>` becomes a view name and an unknown view renders nothing. A disabled Task view's stale URL must degrade gracefully — design for it.
- **Bulk actions, and the just-removed status control.** `updateBulkBar` (`views-shell.js:679`) renders the fixed bottom bar from `S.selectedTasks`; actions flow through `applyBulkAction(action, value)` (`views-shell.js:716`) → `api.tasks.bulk(pid, {taskIds, action, value})`. The bar is **hidden on the board** and only shows on a selection. **A dedicated bulk "Set status" select and its `bulkSetStatus` helper were just removed** (commit `71b177d`); the bar now offers assignee / priority / "Add to board" (backlog) / archive / delete. **The backend `set_status` bulk action still exists** — the management view re-introduces the status control on top of it. Checkbox helpers `taskCheckbox`, `selectAllCheckbox`, `toggleSelectAll`, `syncSelectAllCheckbox` are intact.
- **Status model & single-task status change.** `STATUS_META`/`STATUSES` (canonical order: `PLANNED`, `IN_PROGRESS`, `IN_REVIEW`, `DONE`, `ARCHIVED`) live in shared `meta.js` (synced from `octbase-shared/`, drift-guarded by `scripts/check-shared-sync.sh`). `statusBadge(status)` renders the chip.
- **Naming.** `views-task.js` already exists and is the **task detail panel** (`openTaskPanel`/`renderTaskPanel`). To avoid collision, the new module is **`views-tasklist.js`** (the shared list engine + Task-view config). Do not call it `views-taskview.js`.
- **Build-free asset pipeline.** Every JS/CSS file is referenced from `index.html` with `?v=<hash>` stamped by `scripts/stamp-assets.py` (CI runs `--check`). New module = new `<script>` line + restamp. `data-act`/`data-change`/`data-input` handlers are dispatched centrally and registered in `bootstrap.js` (`registerActions`).
- **i18n.** Only `en.json` + `de.json` remain. New user-facing strings need both; `js/i18n.test.js` enforces en/de key parity. `nav.tasks` and `task.status.*` labels already exist (the latter reusable for subsection headers).
- **Orphan CSS.** `.task-table*` rules from the old removed view linger (`css/app.css:762+`). Reuse them for the management list, or clean them up — don't leave them dangling.
- **Config pattern.** `config.js` holds env/demo constants and `RELATION_TYPES`; `API_BASE` shows the established URL-param-with-fallback override pattern. There is no feature/config registry today — you will add the first one.

## Goal

Ship four things together, in this order of importance:

1. A **shared, configurable task-list module** (`views-tasklist.js`) that owns fetch → filter → group → render-body → focus-preserving-search-refresh, parameterized by a config object.
2. **Re-express the Backlog on that module** as a behavior-preserving refactor (config #1). The backlog's current behavior and its tests must be unchanged — this is the proof the base is genuinely reusable, not just additive code.
3. The **Task view** (config #2): a cross-cutting, status-grouped management layer over the project's tasks, with fulltext search and bulk status change as headline affordances.
4. A small **view-config / enable mechanism** that can hide the Task-view entry per deployment and a **router allowlist** so a disabled/unknown view degrades gracefully.

Keep board, backlog, search, and the bulk bar behaving exactly as before. Additive at the seams; a refactor (not a rewrite) at the core.

---

### 0. The shared task-list module & its config contract (foundational — do this first)

This is the point of the exercise. Get the engine and its config shape right before writing either view.

- **One module, one engine.** `octbase-frontend/js/views-tasklist.js`, loaded from `index.html` after `views-board.js` and before `bootstrap.js`, stamped. It exposes a generic renderer — e.g. `renderTaskList(mount, config)` plus a `refreshTaskList(config)` body-only re-render — that both views call. No view-specific branching lives *inside* the engine; differences live in the **config**.
- **Define the config contract explicitly** (document it in the module header and `js/README.md`). A config describes one list. At minimum:
  - `scope` / `filter` — how to select tasks (e.g. `{backlogOnly:true}` for backlog; a cross-cutting *exclude-ARCHIVED* scope for the Task view). Routes through `applyTaskFilters`; **decide and document** each view's exact scope and how they differ.
  - `grouping` — a function from task → group key + ordered group list + group header renderer. Backlog groups by release; Task view groups by status (canonical `STATUSES` order, excluding `ARCHIVED`). **Empty groups still render their header.**
  - `row` — the row renderer (default: the existing `backlogRow`). Columns/affordances per view.
  - `toolbar` — which filters show (both: type + priority + search; **never a status filter on the Task view** — status is the grouping).
  - `bulk` — which bulk actions apply (Task view adds "Set status"; "Add to board" stays backlog-only).
  - `dnd` — **off by default**; a toggle for later (see §4).
  - `i18n` / `emptyState` — title, empty-state text, a11y labels.
- **Data layer through the endpoints.** The engine fetches via `api.tasks.list(pid, params)`. Prefer server-side filter/paging params where the endpoint supports them over fetching everything and filtering in JS; document the chosen approach and any endpoint gap (this module is the base — its data path should scale).
- **Generalize `refreshBacklogList`.** The focus-preserving, body-only search refresh becomes a single engine function the search keystroke routes to per active view (the `setSearchFilter`/`onResort` seam, `views-shell.js:288`), branching on the view without coupling to either config's internals.

### 1. Re-express the Backlog on the module (behavior-preserving)

- Refactor `renderBacklog` to delegate to `renderTaskList(mount, backlogConfig)`, where `backlogConfig` reproduces today's behavior: `{backlogOnly:true}` scope, group-by-release, `backlogRow`, type+priority+search toolbar, the current bulk actions incl. "Add to board".
- **No behavior change and no test changes for the backlog.** If a backlog test must change, you have altered behavior — stop and reconcile. This step is the regression gate that proves the engine is faithful.

### 2. The Task view (config #2 — the classic management layer)

- **Scope.** A cross-cutting overview: include board *and* backlog tasks (unlike the backlog's not-on-board filter); exclude `ARCHIVED` (that's the Archive view). Document the exact scope and how it differs from backlog.
- **Group by status**, one subsection per status in canonical order (excluding `ARCHIVED`), each with a `statusBadge` + count header; empty statuses still render their header.
- **Management feel, linear (not a board).** Reuse the `backlogRow` layout and/or the orphaned `.task-table` styling: one row per task — checkbox, type badge, seq, title (opens the panel via `openTaskPanel`), priority, assignee, due. The primary interactions are **search, multi-select, and bulk edit**, not lanes.
- **Fulltext search is the headline.** Reuse `#task-search` and `applyTaskFilters(..., {ignoreSearch:false})` → `filterTasksBySearch(..., {fulltext:true})`. On input, re-render only the grouped body via the engine's refresh, preserving focus. Keep type/priority filters; status is implicit (the grouping).
- **No within-group reordering.** Order inside a group is a stable sort (e.g. priority then seq). Document this.

### 3. Bulk status change (the management primary)

- Re-introduce the bulk **"Set status"** select in `updateBulkBar`, shown only when the **Task view is active**. Wire it to the existing `applyBulkAction('set_status', value)` path — the backend `set_status` bulk action still exists; **do not add an endpoint**. After a bulk change, affected rows jump to their new status subsection (re-render).
- Selection reuses `taskCheckbox`/`selectAllCheckbox`/`toggleSelectAll`/`syncSelectAllCheckbox`. Keep assignee/priority/archive/delete here; **"Add to board" stays backlog-only**.
- Restrict status options sensibly (exclude `ARCHIVED`; archive has its own action). Reuse `STATUS_META` labels.

### 4. Drag-and-drop (optional — behind `config.dnd`, off by default)

Not crucial; do not let it shape the engine or the headline tests. Build the seam, ship it off.

- Gate all D&D wiring behind `config.dnd`. With it off (the Task view's initial state) the list is a clean, non-draggable management list and **no drag tests are required to pass**.
- When designing the seam, reuse the board's drag plumbing (`S.dragging`, the document-level dragstart/dragover/drop wiring) rather than a parallel system; factor any shared bit into a **generic** helper free of board-specific assumptions. Dropping a row on another status would call `api.tasks.status(taskId, targetStatus)` then optimistically move the row (reconcile/re-render on failure via `apiErrorMessage`). Document, but do not implement as a hard requirement, the board-reconciliation rule (status-only vs. lane-follows-backend) and the `PROJECT_VIEWER` read-only handling (no `draggable`, no drop wiring).

### 5. View enablement & graceful routing

- **One config source of truth.** Add a small registry to `config.js` (e.g. `const VIEWS = { taskList: { enabled: <default> } }` or a `FEATURES` object), with the `API_BASE`-style override (URL param and/or `localStorage`) so tests can flip it without code edits. Recommend **enabled by default**, but the app must be correct either way. Everything that shows/hides the Task-view entry reads this one flag. **Note in the docs that this toggles the *view*, not the module** — the engine and the backlog config remain regardless.
- **Seams, each one line and flag-guarded:**
  - *Sidebar:* conditionally splice the entry — `...(VIEWS.taskList.enabled ? [{id:'tasks', icon:<glyph>, label:t('nav.tasks')}] : [])`.
  - *Dispatch:* `renderContent` routes `case 'tasks'` to the Task-view config only when enabled.
  - *Bulk bar:* the "Set status" control appears only when the Task view is the active, enabled view.
- **Harden the router.** Introduce a small **known-views allowlist** in `handleRoute` so a disabled or unknown view falls back deterministically (redirect to `backlog`/`board`) instead of rendering blank — this also fixes the existing no-`default` switch. A stale `/projects/:id/tasks` URL with the view disabled must never render blank or throw.
- Add a **"Configuring the task-list module & views"** note to `js/README.md`: the config shape, the flag, the seam lines, and the restamp step.

### 6. i18n, icons, styling, assets

- Add new strings to **both** `en.json` and `de.json`: the Task-view title, empty-state text, and a11y labels for the bulk status select (and the status drop regions if/when D&D ships). Reuse `nav.tasks` and `task.status.*` where they fit. Keep en/de key parity (the node i18n test enforces it).
- Pick a sidebar **icon** from the existing `icons.js` set; don't invent a glyph unless necessary.
- Reuse `.task-table` / `.backlog-*` styles. If you touch CSS, resolve the orphaned `.task-table*` rules (reuse or remove) so nothing dangles.
- Add the `<script src="js/views-tasklist.js?v=…">` line, **register new `data-act`/`data-change` handlers in `bootstrap.js`**, and run `scripts/stamp-assets.py octbase-frontend/index.html` (verify `--check`). If you touch a shared module, re-run `scripts/sync-shared.sh` and confirm the drift guard is clean.

### 7. Tests (octbase-frontend/tests, Playwright + pytest; node i18n test if strings added)

The headline proof is **"one engine, two faithful configs"** — not removability.

- **Backlog non-regression (the engine-fidelity gate):** the existing backlog tests pass **unchanged** after the refactor onto the shared module. Board, search, and the bulk bar behave exactly as before.
- **Task view (enabled):**
  - The "Tasks" sidebar entry and view render; tasks group into per-status subsections in canonical order with correct counts; empty statuses render their header.
  - Fulltext search narrows rows across all groups and preserves input focus.
  - Bulk-select multiple rows + "Set status" → all move to the new subsection (assert via API).
  - (Only if `dnd` is enabled in a test config) a drag updates status and moves the row; a `PROJECT_VIEWER` cannot drag. With `dnd` off — the default — these are not required.
- **Task view (disabled):** no "Tasks" sidebar entry, no Task-view DOM; a stale `/projects/:id/tasks` URL degrades gracefully (falls back to backlog/board — never blank, never a console error); backlog/board/search/bulk unaffected.
- Locale parity for new keys; `stamp-assets.py --check` green; `check-shared-sync.sh` green if a shared module was touched.

---

**Deliverables:** the `views-tasklist.js` shared engine + its documented config contract; the Backlog refactored onto it (behavior-preserving); the Task-view config (management layer) with bulk status change; the optional `dnd`-gated drag seam (shipped off); the `VIEWS`/`FEATURES` flag in `config.js` with the router allowlist + graceful fallback; the minimal flag-guarded seams in `views-shell.js`/`api.js`/`updateBulkBar`; new en/de strings; the `index.html` script line + restamp; `bootstrap.js` handler registrations; tests (backlog non-regression, Task view enabled & disabled); and the "Configuring the task-list module & views" note in `js/README.md`. **Constraints:** one shared module used by two configs; the backlog refactor changes no backlog behavior or tests; the task endpoints are the documented base contract (no new endpoint for bulk status); drag-and-drop is optional and off by default; no regression to board/backlog/search/bulk.
