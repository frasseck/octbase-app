# Octbase Frontend — Recreate Prompt

> **Iterative baseline:** This prompt describes the POC frontend matching `1_octbase-api.md` (X-User-Id auth, no `/api/v1/` prefix, nginx serves the SPA). The login page, JWT Bearer auth flow, dashboard, search views, and the API being moved into the same container are introduced in `8_octbase-create-mvp.md`.

## Goal

Build a fully functional task management frontend for the Octbase API. The frontend is a **no-build vanilla HTML/CSS/JS app** split across `index.html`, `app.css`, and `app.js`, plus static assets. No npm, no framework, and no external dependencies. It must work both when opened directly via `file://` and when served by the Go API.

---

## API

- Base URL: use `http://127.0.0.1:8000` when loaded via `file://`; use `window.location.origin` when served by the API
- Auth: every request must include the header `X-User-Id: 00000000-0000-0000-0000-000000000001` (the demo user)
- All request/response bodies are JSON
- The HTTP client must wrap fetch with `GET`, `POST`, `PATCH`, `DELETE` helpers that attach the auth header and throw on non-2xx

---

## Domain constants

```
Task statuses:  PLANNED | IN_PROGRESS | IN_REVIEW | DONE | ARCHIVED
Task types:     TASK | BUG | STORY | EPIC | CHORE
Priorities:     LOW | MEDIUM | HIGH | CRITICAL
Relation types: RELATES_TO | BLOCKS | BLOCKED_BY | DUPLICATES
```

Immutable tasks (DONE or ARCHIVED) must not expose edit controls.

---

## API endpoints used

### Projects
```
GET    /api/projects
POST   /api/projects                      { name, description, visibility }
GET    /api/projects/{id}
PATCH  /api/projects/{id}
POST   /api/projects/{id}/archive
DELETE /api/projects/{id}
```

### Tasks
```
GET    /api/projects/{id}/tasks           ?status=&priority=&size=200
POST   /api/projects/{id}/tasks          { title, description, taskType, priority, releaseId }
GET    /api/tasks/{id}
PATCH  /api/tasks/{id}                   { title, description, taskType, releaseId }
POST   /api/tasks/{id}/status            { status }
POST   /api/tasks/{id}/priority          { priority }
POST   /api/tasks/{id}/assign            { assigneeId, reviewerId }
POST   /api/tasks/{id}/copy
POST   /api/tasks/{id}/archive
POST   /api/tasks/{id}/reopen
DELETE /api/tasks/{id}
GET    /api/projects/{id}/search/tasks   ?q=
```

### Task sub-resources
```
GET/POST       /api/tasks/{id}/comments              POST: { text }
PATCH/DELETE   /api/tasks/{id}/comments/{commentId}  PATCH: { text }
GET/POST       /api/tasks/{id}/links                 POST: { url, title }
DELETE         /api/tasks/{id}/links/{linkId}
GET/POST       /api/tasks/{id}/relations             POST: { targetTaskId, relationType }
DELETE         /api/tasks/{id}/relations/{id}
GET/POST       /api/tasks/{id}/attachments           POST: { filename, contentType, sizeBytes, externalUrl }
DELETE         /api/tasks/{id}/attachments/{id}
GET/POST       /api/tasks/{id}/branches              POST: { repositoryId, branchName, branchType }
DELETE         /api/tasks/{id}/branches/{branchId}
GET            /api/tasks/{id}/activity
```

### Boards
```
GET    /api/projects/{id}/boards/default
POST   /api/projects/{id}/boards         { name, isDefault: true }
DELETE /api/boards/{id}
POST   /api/boards/{id}/columns          { name, status, position }
POST   /api/boards/{id}/move-task        { taskId, boardColumnId, boardRank }
POST   /api/boards/{id}/remove-task      { taskId }
```

### Backlog
```
GET    /api/projects/{id}/backlog
```

### Releases
```
GET    /api/projects/{id}/releases
POST   /api/projects/{id}/releases     { name, goal, dueDate }
PATCH  /api/releases/{id}              { name, goal, dueDate }
POST   /api/releases/{id}/close
POST   /api/releases/{id}/reopen
DELETE /api/releases/{id}
```

### Pages
```
GET    /api/projects/{id}/pages
POST   /api/projects/{id}/pages          { title, slug, content }
GET    /api/pages/{id}
PATCH  /api/pages/{id}                   { title, content, slug }
POST   /api/pages/{id}/render-preview    { content } → { html }
POST   /api/pages/{id}/publish           { message }
POST   /api/pages/{id}/archive
DELETE /api/pages/{id}
GET    /api/pages/{id}/revisions
GET    /api/projects/{id}/search/pages   ?q=
```

### Activity
```
GET    /api/projects/{id}/activity
GET    /api/tasks/{id}/activity
```

### Identity & Repositories
```
GET          /api/users/me
GET          /api/projects/{id}/memberships
GET/POST     /api/projects/{id}/repository-connections   POST: { displayName, repositoryUrl, provider, defaultBranch }
PATCH/DELETE /api/repository-connections/{id}
```

---

## Architecture

Single-page app, state-driven, no virtual DOM. Global state object `S` holds:
- `projects`, `project` (current), `board`, `tasks`, `releases`, `pages`, `members`, `repos`
- `usersMap` — `{ userId → { displayName, email } }` built from `/api/users/me`
- `view` — current view name
- `taskPanelId`, `taskPanelTab` — open task panel state
- `selectedPage`, `pageEditMode`
- `filters` — `{ status, priority, type, q }`

Routing is a plain `setView(name)` function that clears content and calls the matching render function. No URL hash needed.

---

## Layout

```
┌────────────────┬──────────────────────────────┬──────────────┐
│  Sidebar       │  Topbar                       │  Task Panel  │
│  (220px fixed) ├──────────────────────────────│  (440px,     │
│                │  Content area                 │  slides in   │
│  Logo          │                               │  from right) │
│  Nav items     │                               │              │
│  ─────────     │                               │              │
│  User avatar   │                               │              │
└────────────────┴──────────────────────────────┴──────────────┘
```

- `#main` gains `margin-right: 440px` when the task panel is open (CSS transition)
- The task panel slides in via `transform: translateX(100%)` → `translateX(0)`

---

## Sidebar

**When no project is selected:**
- Section label "Projects" + one item per project
- "New Project" item at the bottom

**When a project is selected:**
- Back link "‹ All Projects"
- Project name as section label
- Nav items: Board · Backlog · Tasks · Releases · Pages · Repositories · Activity
- Active item has a left white border and slightly brighter background

User avatar and display name at the very bottom.

---

## Views

### Projects
- Card per project: colored letter icon, name, description, status badge, 🗑 delete button
- "New Project" dialog: name (required), description, visibility (PUBLIC/PRIVATE)
- Delete opens `confirmDelete()` modal; on confirm calls `DELETE /api/projects/{id}` and re-renders list

### Board (kanban) — the centerpiece
- Horizontally scrollable columns, one per board column, sorted by `position`
- Column header: colored status dot, name in uppercase, task count badge
- Tasks grouped by `boardColumnId` (null = backlog, not shown here), sorted by `boardRank`
- Each card:
  - Top row: task type badge (colored letter), short task ID (first 6 chars of UUID)
  - Title (middle)
  - Footer: priority dot, release tag if set, assignee avatar (initials)
- `+ Add task` button at the bottom of each column (passes the columnId so the new task is immediately placed there via `move-task` API)
- **Drag and drop**: HTML5 `draggable="true"` on cards. Supports both cross-column moves and same-column reordering. On `drop`, call `POST /api/boards/{id}/move-task` with the computed `boardRank`. Visual feedback: `drag-over` class (light blue background) on the hovered column; `drop-before` / `drop-after` class (blue `box-shadow` line above/below) on the card the cursor is hovering over, indicating the insertion point. On success, re-render board.
- **No board case**: If `/boards/default` returns 404/BOARD_NOT_FOUND, show an empty state with a "Create Default Board" button. That button calls `POST /api/projects/{id}/boards` then `POST /api/boards/{id}/columns` four times for Planned/In Progress/Review/Done, then re-renders.

### Backlog
- Tasks where `boardColumnId` is null (from `/api/projects/{id}/backlog`)
- Grouped by release; ungrouped tasks in a "No Release" section
- Each row: type badge, priority dot, title, status badge, hover-reveal "→ Board" button
- "→ Board" opens a modal to choose which column, then calls `move-task`

### Tasks (list)
- Filter bar: search input (debounced 400 ms), status select, priority select, type select, clear button
- Table: Type | Title | Status | Priority | Assignee | Updated
- Click row → opens task panel

### Task panel (right drawer)
- **Header**: type badge, editable title textarea (auto-saves on input with 800 ms debounce), close button
- **Action bar**: status badge, priority dot+label, Copy button, Archive/Reopen button, "→ Backlog" button (if task is on board)
- **Tabs**: Details · Comments · Links · Relations · Attachments · Branches · Activity

**Details tab:**
- Description textarea with explicit Save button (edit disabled for immutable tasks)
- Detail rows: Status (dropdown), Priority (dropdown), Type (dropdown), Assignee (text), Reporter (text), Release (dropdown), Created, Updated

**Comments tab:**
- Chronological list of comments (author initials avatar, name, text, timestamp)
- Add comment textarea + Comment button

**Links tab:**
- List of links with URL, title, delete button
- Add form: URL input + label input + Add button

**Relations tab:**
- List of relations showing type and target task ID (truncated)
- Add form: relation type select + target task ID input + Add button

**Attachments tab:**
- List: filename, size in KB, delete button
- Add form: filename input + external URL input + Add button (metadata only, no file upload)

**Branches tab:**
- List: branch name, branch type tag
- Add form (only if repos exist): repo select + branch name input + type select (feature/bugfix/hotfix/release) + Create button
- If no repos: message "No repository connected"

**Activity tab:**
- Reverse-chronological list from `/api/tasks/{id}/activity`

### Releases
- Card per release: flag icon, name, status badge (PLANNED/CLOSED), goal text, due date, Close/Reopen + Edit buttons
- Edit opens a modal (name, goal, due date)
- Close calls `/api/releases/{id}/close`; shows API error if open tasks still assigned

### Pages
- Two-column layout: tree sidebar (220px) + page view (flex 1)
- Tree: one item per page, diamond icon filled (◆) for PUBLISHED, outline (◇) for DRAFT/ARCHIVED
- Page view has two modes:
  - **View**: rendered HTML from `renderedHtml` field, Edit/Publish/Archive/🗑/Revisions action buttons
  - **Edit**: title input + AsciiDoc textarea + Save button + "Preview Render" button (calls `render-preview`, shows result inline below editor)
- Revisions shown in a modal list (message + timestamp)
- `#content` gets class `content-pages` (sets `padding:0`) when pages view is active, cleared on navigation

### Repositories
- List of repository connections: provider badge, display name, URL, default branch, 🗑 delete button
- Add Repository form: display name, URL, provider select (FAKE_GITLAB/GITHUB/BITBUCKET), default branch
- Delete calls `confirmDelete()` with warning that branch references will also be removed

### Activity
- Reverse-chronological feed of project activity
- Icon map for event types: TASK_CREATED/UPDATED/STATUS_CHANGED/MOVED/COMMENT_ADDED, MILESTONE_CLOSED, PAGE_PUBLISHED, BRANCH_CREATED

---

## Design system

**Colors (CSS variables):**
```
--sidebar-bg:    #0747a6   (dark blue)
--primary:       #0052cc
--primary-light: #deebff
--bg:            #f4f5f7   (page background)
--surface:       #fff
--border:        #dfe1e6
--text:          #172b4d
--muted:         #6b778c
```

**Status badge CSS classes:** `badge-planned` · `badge-in-progress` · `badge-in-review` · `badge-done` · `badge-archived`

**Status dot classes (board column header):** `status-dot status-dot-{variant}` where variant is `planned`, `in-progress`, `in-review`, `done`, `archived`. Never use inline `background` color — always use these classes.

**Priority dots:** colored circles 10px — green (LOW), orange (MEDIUM), red (HIGH), purple (CRITICAL)

**Task type badges:** 20×20px colored squares with a letter — T blue (TASK), B red (BUG), S green (STORY), E purple (EPIC), C gray (CHORE)

**Buttons:** `.btn-primary` (blue fill), `.btn-secondary` (white + border), `.btn-ghost` (transparent), `.btn-danger` (red fill); all have `.btn-sm` variant

**Delete actions:** always use 🗑 (wastebasket emoji) as a `.btn-icon` — never use text labels for delete. Always call `confirmDelete(title, body, onConfirm)` which shows a danger-styled modal before proceeding. The `✕` character is reserved for the task panel close button only.

**Toast notifications:** fixed bottom-right, slide-up animation, `toast-success` (green) / `toast-error` (red) / `toast-info` (blue), auto-remove after 3.5 s

**Modal:** centered overlay, max-width 480px, Cancel + primary action buttons in footer. Click backdrop to dismiss. `confirmDelete()` sets the submit button to `.btn-danger`.

**Board columns:** background `#ebecf0`, border-radius 6px, `drag-over` state = light blue background (`var(--primary-light)`)

**Cards:** white, 3px border-radius, subtle shadow, hover shadow increase. Drop position indicator: `drop-before` / `drop-after` class adds a colored `box-shadow` line above/below the card.

**No inline styles:** `style="..."` attributes are forbidden in template strings. All visual properties must be expressed as CSS classes in `app.css`. Programmatic view switching uses `classList.add/remove` (e.g. `content-board`, `content-pages` on `#content`). Enforce with: `grep 'style="' app.js` — expected output: empty.

---

## Key implementation notes

1. **`memberName(uid)`** — looks up `S.usersMap[uid]`. On project load, call `/api/users/me` and store the result in `usersMap` keyed by `id`. The API has no list-all-users endpoint; only the current user is resolvable.

2. **`releaseName(mid)`** — looks up `S.releases` array by id.

3. **Drag and drop rank** — `initDnD()` tracks `dropTarget` (the card ID being hovered) and `dropBefore` (whether the cursor is in the top or bottom half of that card). On drop, rank is computed as the midpoint between the two adjacent cards' `boardRank` values from `S.tasks`. If dropping below all cards, rank = last card's rank + 1000. Collision-safe: if the midpoint equals an adjacent rank, it is nudged by ±1. The same-column guard (`if toColId === dragFromCol return`) is absent — same-column reordering is fully supported.

4. **Task immutability** — `status === 'DONE' || status === 'ARCHIVED'`. Show all fields read-only, hide Save/edit buttons, but still allow Copy.

5. **Debounced title save** — 800 ms after last keystroke, `PATCH /api/tasks/{id}` with `{ title }`.

6. **Page slug** — auto-generated by the API from the title if not provided. Don't require it in the create form.

7. **Board not found** — the API returns `{ code: "BOARD_NOT_FOUND" }`. Detect this in the catch block by checking `e.message.includes('BOARD_NOT_FOUND')` and render the create-board empty state instead of throwing.

8. **`esc(s)`** — always HTML-escape user content before inserting into innerHTML. Simple replace chain for `& < > "`.

9. **No build tooling** — keep HTML/CSS/JS split into `index.html`, `app.css`, and `app.js`; use normal `<link>` and `<script src>` tags.

10. **Board refresh on every task save** — every function that mutates a task (title debounce, `saveDesc`, `changeStatus`, `changePriority`, `changeType`, `changeRelease`, `copyTask`, `doArchive`, `doReopen`) calls `if(S.view==='board') renderBoard()` after the API call succeeds.

11. **Stale render guard** — async render functions called from action callbacks (`renderPages`, `renderReleases`, `renderRepos`) check `if(S.view !== 'X') return` at entry and after their API call. This prevents a callback that resolves after the user has navigated away from corrupting the current view or its padding class.

12. **Content view padding** — `renderContent()` removes both `content-board` and `content-pages` from `#content` and resets `c.style.padding=''` on every navigation. `renderBoard()` adds `content-board`; `renderPages()` adds `content-pages`. Never set `c.style.padding` directly in view render functions.

---

## File location

```
octbase-frontend/
  index.html
  app.css
  app.js
  nginx.conf          # listens on 8080, proxies /api/ /docs /health /openapi.yaml to API
  favicon.ico
  logo.png
  .containerignore    # excludes tests/ and logo1.png
  tests/
  Containerfile
```

`octbase-frontend/` is the source of truth. It is built into its own nginx container (`octbase-frontend/Containerfile`) and served at **http://localhost:8080/**. The API image does **not** include the SPA files — it only serves its own docs and REST endpoints.

Start the full stack with:
```sh
podman-compose up --build
```

The frontend can also be opened directly via `file://` by passing `?apiBase=http://127.0.0.1:8000` as a query parameter (defaults to `http://127.0.0.1:8000` if omitted).
