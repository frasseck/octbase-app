# Octbase Frontend Quality Check

A review prompt for the Octbase vanilla-JS frontend. Use it for code review, bug hunting, or onboarding.

---

## File Structure

```
octbase-frontend/
  index.html          # HTML shell — no inline CSS or JS
  css/app.css         # All styles
  js/app.js           # All JavaScript (strict mode); js/i18n.js for translations
  locales/            # en.json · de.json · fr.json i18n bundles
  img/                # Logos and favicons
  user-guide.html     # End-user guide (shipped) — the single canonical user guide for the whole project
  caddy/Caddyfile     # Caddy config (listens on 8080, reverse-proxies /api/, /docs, /metrics, /openapi.yaml; flush_interval -1 on the SSE route)
  caddy/Caddyfile.tls # TLS variant (redirect :8080→HTTPS, terminate TLS on :8443, restrict /metrics to private nets)
  .containerignore    # Excludes tests/ and dev-only assets from the container build
  tests/              # Playwright + pytest E2E suite
```

The frontend is served by its own **Caddy** container (port 8080) built from `octbase-frontend/Containerfile`. It is **not** bundled into the API image. The API image (`octbase-api/Containerfile`) serves only the API and its own docs page.

---

## HTML Checklist

Validate with: `curl -s -H "Content-Type: text/html; charset=utf-8" --data-binary @index.html 'https://validator.w3.org/nu/?out=json'`

Expected: zero errors, zero warnings.

| Check | Expected |
|-------|----------|
| DOCTYPE present | `<!DOCTYPE html>` |
| `lang` on `<html>` | `lang="en"` |
| `charset` meta first in `<head>` | `<meta charset="UTF-8">` |
| `viewport` meta present | `width=device-width, initial-scale=1.0` |
| `<title>` present | `Octbase` |
| CSS linked | `<link rel="stylesheet" href="app.css">` |
| JS linked with `defer` | `<script src="app.js" defer></script>` |
| No inline `<style>` | none |
| No inline `<script>` | none |
| All tags properly closed | yes |
| Logo has `alt` text | `alt="Octbase"` |

Key shell elements (all populated by JS at runtime):

| ID | Role |
|----|------|
| `#app` | Root flex container |
| `#sidebar` (`<aside>`) | Left nav; contains `#sidebar-nav` and `#sidebar-user` |
| `#sidebar-nav` | Project/view navigation items (rendered by `renderSidebar()`) |
| `#user-avatar` | 2-letter initials, updated by `init()` |
| `#user-name` | Display name, updated by `init()` |
| `#main` | Content area; gets class `panel-open` when task panel is open |
| `#topbar` | Top action bar (rendered by `renderTopbar()`) |
| `#content` | Main view area (rendered by `renderContent()`) |
| `#task-panel` | Fixed right-side panel; gets class `open` |
| `#task-panel-content` | Panel body (rendered by `renderTaskPanel()`) |
| `#modal-backdrop` | Full-screen modal overlay; starts with class `hidden` |
| `#modal` | Modal content (rendered by `showModal()`) |
| `#toast-container` | Toast notification stack |

---

## CSS Checklist

Validate embedded in a full HTML document via the W3C nu validator (the standalone CSS validator rejects vendor-prefixed rules as errors; nu treats them correctly as warnings only).

```sh
python3 -c "
import sys
css = open('app.css').read()
html = '<!DOCTYPE html><html lang=\"en\"><head><meta charset=\"UTF-8\"><title>t</title><style>' + css + '</style></head><body></body></html>'
open('/tmp/css_check.html','w').write(html)
"
curl -s -H "Content-Type: text/html; charset=utf-8" \
  --data-binary @/tmp/css_check.html \
  'https://validator.w3.org/nu/?out=json' | \
  python3 -c "import json,sys; msgs=json.load(sys.stdin)['messages']; print('OK' if not msgs else msgs)"
```

Expected: `OK`

### Design tokens (`app.css` `:root`)

| Variable | Value | Used for |
|----------|-------|----------|
| `--sidebar-w` | `220px` | Sidebar width |
| `--panel-w` | `440px` | Task panel width |
| `--bg` | `#f4f5f7` | Page background |
| `--surface` | `#fff` | Card/panel backgrounds |
| `--border` | `#dfe1e6` | Dividers |
| `--text` | `#172b4d` | Body text |
| `--muted` | `#6b778c` | Secondary text |
| `--primary` | `#0052cc` | Brand blue |
| `--primary-h` | `#0041a3` | Hover state |
| `--primary-light` | `#deebff` | Tinted backgrounds |
| `--sidebar-bg` | `#0747a6` | Sidebar navy |
| `--radius` | `3px` | Default border radius |
| `--radius-lg` | `6px` | Card/modal radius |
| `--shadow` | `0 1px 3px …` | Card shadow |
| `--shadow-md` | `0 4px 12px …` | Panel/modal shadow |

### Critical CSS rules to verify

| Selector | Key property | Expected |
|----------|-------------|----------|
| `body` | `overflow` | `hidden` (prevents double scrollbars) |
| `#main.panel-open` | `margin-right` | `var(--panel-w)` |
| `#task-panel` | `transform` | `translateX(100%)` (hidden by default) |
| `#task-panel.open` | `transform` | `translateX(0)` |
| `#modal-backdrop.hidden` | `display` | `none !important` |
| `.board-column` | `width` | `270px` fixed |
| `.board-column.drag-over` | `outline` | `2px dashed var(--primary)` |
| `.card.dragging` | `opacity` | `0.5` |
| `.logo-img` | `max-width` | `118px` |
| `.sidebar-logo` | `justify-content` | `flex-start` |
| `.pages-layout` | `height` | `calc(100vh - 52px)` |
| `#content.content-pages` | `padding` | `0` |
| `.content-board` | `padding` | `16px 20px` |

### Status badge classes

| Class | Background | Text |
|-------|-----------|------|
| `.badge-planned` | `#dfe1e6` | `#5e6c84` |
| `.badge-in-progress` | `#deebff` | `#0052cc` |
| `.badge-in-review` | `#eae6ff` | `#403294` |
| `.badge-done` | `#e3fcef` | `#006644` |
| `.badge-archived` | `#ebecf0` | `#6b778c` |

### Status dot classes (board column header)

The `statusDot(s)` helper returns `<span class="status-dot status-dot-{variant}">`. No inline `background` style is used.

| Class | Background |
|-------|-----------|
| `.status-dot-planned` | `#7a869a` |
| `.status-dot-in-progress` | `#0052cc` |
| `.status-dot-in-review` | `#8b00d4` |
| `.status-dot-done` | `#00875a` |
| `.status-dot-archived` | `#6b778c` |

### Priority dot classes

| Class | Color |
|-------|-------|
| `.prio-low` | `#36b37e` |
| `.prio-medium` | `#ff8b00` |
| `.prio-high` | `#de350b` |
| `.prio-critical` | `#6554c0` |

### Type badge classes

| Class | Background | Text | Symbol |
|-------|-----------|------|--------|
| `.type-task` | `#deebff` | `#0052cc` | T |
| `.type-bug` | `#ffebe6` | `#de350b` | B |
| `.type-story` | `#e3fcef` | `#006644` | S |
| `.type-epic` | `#eae6ff` | `#403294` | E |
| `.type-chore` | `#ebecf0` | `#6b778c` | C |

### Utility classes

These are available throughout `app.js` template strings. Prefer them over any inline style.

| Class(es) | Property |
|-----------|---------|
| `.flex` | `display:flex` |
| `.flex-1` / `.flex-2` | `flex:1` / `flex:2` |
| `.flex-wrap` | `flex-wrap:wrap` |
| `.flex-between` | `display:flex; justify-content:space-between; align-items:center` |
| `.items-center` | `align-items:center` |
| `.gap-1` / `.gap-2` / `.gap-3` | `gap:4px` / `8px` / `12px` |
| `.ml-auto` | `margin-left:auto` |
| `.mb-3` / `.mb-4` / `.mb-5` | `margin-bottom:12px` / `16px` / `20px` |
| `.mt-1` / `.mt-2` | `margin-top:4px` / `8px` |
| `.text-muted` | `color:var(--muted)` |
| `.text-sm` | `font-size:12px` |
| `.font-semibold` | `font-weight:600` |
| `.icon-danger` | `color:#de350b` (used on trash icon buttons) |
| `.hidden` | `display:none !important` |

### Component classes (new since v0.1)

| Class | Purpose |
|-------|---------|
| `.view-wrap` | `max-width:700px` wrapper for list views |
| `.view-header` | Flex row: view title + primary action button |
| `.view-title` | `font-size:16px; font-weight:600` section heading |
| `.panel-action-bar` | Task panel action row (status, priority, buttons) |
| `.panel-priority-label` | Priority text in action bar |
| `.panel-actions` | Right-aligned button group in action bar (`ml-auto`) |
| `.desc-save-btn` | Save button below description textarea (`margin-top:6px`) |
| `.comment-header` | Flex row: author + action icons, `align-items:baseline` |
| `.comment-actions` | Edit + delete icon group in comment header |
| `.comment-edit-actions` | Save/Cancel buttons below edit textarea |
| `.card-id` | Short task UUID in board card top row (`ml-auto`, muted) |
| `.card-tag-sm` | Release tag in board card footer (`font-size:10px`) |
| `.tab-section` | `margin-bottom:12px` wrapper for tab list content |
| `.tab-form` | Flex add-form row inside task panel tabs |
| `.priority-cell` | Flex row: priority dot + priority label text |
| `.repo-item` | Repo connection row in Repositories view |
| `.repo-info` | Flex-1 info column inside `.repo-item` |
| `.repo-name` | Display name in repo item |
| `.box` | Surface card with border and padding |
| `.box-title` | Bold title inside `.box` |
| `.grid-gap` | `display:grid; gap:8px` (form layout) |
| `.page-tree-label` | Uppercase tree section header |
| `.page-tree-new-btn` | Full-width "New Page" button in tree |
| `.page-tree-title` | Truncating title span in page tree items |
| `.page-actions-end` | `ml-auto` flex group in page action bar |
| `.page-edit-actions` | Save/Preview/Cancel row in page edit mode |
| `.page-display-title` | `h1` styling for page view title |
| `.page-preview-box` | Grey box wrapping rendered preview output |
| `.revision-item` / `.revision-title` | Revision list items in modal |
| `.activity-wrap` | `max-width:600px` activity view container |
| `.activity-view-title` | Activity section heading |
| `.activity-box` | Surface card wrapping activity feed |

---

## CSS Standards

**No `style="..."` attributes may appear in template literal strings in `app.js`.** All visual properties must be expressed as CSS classes in `app.css`.

**Allowed exceptions:**
- Programmatic DOM manipulation via `element.classList.add/remove()` (e.g. switching between `.content-board` and `.content-pages` on `#content`)
- Dynamic colours or values that are genuinely runtime-only and cannot be expressed as a class (currently none)

**Enforcing the rule:**
```sh
grep 'style="' octbase-frontend/app.js
```
Expected output: empty.

---

## JavaScript Checklist

`app.js` runs in `'use strict'` mode. It is a single JavaScript entry file in a no-build vanilla HTML/CSS/JS frontend with no bundler or framework.

### Architecture

| Section | Description |
|---------|-------------|
| CONFIG | `API_BASE`, `DEMO_USER_ID`, `STATUS_META`, `PRIORITY_META`, `TYPE_META` constants |
| HTTP CLIENT | `http.get/post/patch/del` — fetch wrapper, throws on non-2xx |
| API | `api.*` — domain-named wrappers over HTTP client |
| STATE | `const S` — single global state object |
| UTILS | `el()`, `h()`, `esc()`, `fmtDate()`, `fmtDateTime()`, `memberName()`, `releaseName()`, DOM helpers |
| TOAST | `toast(msg, type)` — 3.5 s auto-dismiss notifications |
| MODAL | `showModal()` / `hideModal()` — single-slot modal; `confirmDelete()` — danger-styled variant |
| SIDEBAR | `renderSidebar()` — projects list or per-project nav |
| ROUTER | `setView()`, `renderTopbar()`, `renderContent()` |
| VIEWS | `renderProjects`, `renderBoard`, `renderBacklog`, `renderTaskList`, `renderReleases`, `renderPages`, `renderRepos`, `renderActivity` |
| TASK PANEL | `openTask`, `closeTaskPanel`, `renderTaskPanel`, `renderPanelTab`, and per-tab renderers |
| INIT | `init()` — loads user, loads projects, kicks off render |

### API client (`api.*`)

Every entity exposes all applicable CRUD operations. Check that the following are present and call the correct HTTP method + path:

| Namespace | Methods |
|-----------|---------|
| `api.projects` | `list`, `get`, `create`, `update`, `archive`, `del` |
| `api.tasks` | `list`, `get`, `create`, `update`, `status`, `priority`, `assign`, `copy`, `archive`, `reopen`, `del`, `search`, `activity` |
| `api.comments` | `list`, `add`, `update`, `del` |
| `api.links` | `list`, `add`, `del` |
| `api.relations` | `list`, `add`, `del` |
| `api.attachments` | `list`, `add`, `del` |
| `api.branches` | `list`, `create`, `del` |
| `api.boards` | `getDefault`, `create`, `addColumn`, `move`, `remove`, `del` |
| `api.backlog` | `get` |
| `api.releases` | `list`, `get`, `create`, `update`, `close`, `reopen`, `del` |
| `api.pages` | `list`, `get`, `create`, `update`, `publish`, `archive`, `del`, `revisions`, `preview`, `search` |
| `api.activity` | `project`, `task` |
| `api.members` | `list`, `add`, `remove` |
| `api.repos` | `list`, `create`, `update`, `del` |
| `api.user` | `me` |

### State shape (`S`)

```js
{
  projects:    [],          // all loaded projects
  project:     null,        // currently selected project
  board:       null,        // current default board (with columns[])
  tasks:       [],          // tasks loaded for board view
  releases:  [],          // project releases
  pages:       [],          // project pages
  members:     [],          // project memberships
  usersMap:    {},          // userId → { displayName, email }
  repos:       [],          // repository connections
  view:        'projects',  // active view name
  taskPanelId: null,        // open task ID
  taskPanelTab:'details',   // active panel tab
  dragging:    null,
  selectedPage:null,
  pageEditMode:false,
  filters:     { status:'', priority:'', type:'', q:'' },
}
```

### XSS safety

Every user-controlled string rendered into HTML **must** pass through `esc()`:

```js
function esc(s) {
  return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;')
                  .replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}
```

Check that: task titles, project names, descriptions, comments, page titles, release names, branch names, filenames, and link titles all use `esc()` before insertion into template literals.

One exception: `p.renderedHtml` from the API is inserted directly as:
```js
<div class="page-rendered">${p.renderedHtml || '…'}</div>
```
This is intentional — the server renders trusted AsciiDoc. The API must sanitise the HTML it produces.

### Key behaviours to verify

| Behaviour | Where |
|-----------|-------|
| `S` is accessible as `window.S` | immediately after the top-level `const S` declaration |
| Board drag-and-drop updates task rank | `initDnD()` → `api.boards.move()` |
| Panel opens with class `open` on `#task-panel` | `openTask()` |
| Panel close removes `open`, clears content | `closeTaskPanel()` |
| Immutable tasks (DONE/ARCHIVED) hide edit controls but still show Copy/Reopen where applicable | `renderTaskPanel()` + `renderPanelDetails()` |
| Title auto-saves after 800 ms debounce; board refreshes on success | `renderTaskPanel()` |
| Filter debounce on search input is 400 ms | `debounceFilter()` |
| New task in a column is moved to that column after creation | `showCreateTask()` |
| Copy button creates task with `status: PLANNED`, no board column | `copyTask()` |
| Backlog lists only tasks with `boardColumnId == null` | server-side, surfaced by `renderBacklog()` |
| Page preview renders AsciiDoc without saving | `previewPage()` |
| Error from API is shown as error toast | all `catch(e)` blocks |
| Board re-renders after any task save (title, desc, status, priority, type, release, copy, archive, reopen, delete) | all task mutation functions |
| Content padding toggled via `classList` not `style=` | `renderBoard` adds `.content-board`; `renderPages` adds `.content-pages`; `renderContent` removes both |

### Delete operations

All destructive deletes must:
1. Use a 🗑 (wastebasket emoji) icon button with class `btn-icon` — text labels are not used for delete actions.
2. Call `confirmDelete(title, body, onConfirm)` which displays a danger-styled modal before proceeding.
3. The panel close button (`✕`) is the only button that uses `✕`.

| Entity | Trigger | After delete |
|--------|---------|--------------|
| Project | 🗑 on project card | Re-render projects list |
| Task | 🗑 in panel action bar | Close panel; re-render current view |
| Comment | 🗑 per comment row | Re-render task panel |
| Branch | 🗑 per branch row | Re-render task panel |
| Link | 🗑 per link row | Re-render task panel |
| Relation | 🗑 per relation row | Re-render task panel |
| Attachment | 🗑 per attachment row | Re-render task panel |
| Release | 🗑 on release card | Re-render releases |
| Page | 🗑 in page action bar | Clear selected page; re-render pages |
| Repository connection | 🗑 per repo row | Re-render repos |

---

## Views Checklist

Verify each view is reachable from the sidebar and renders without JS errors.

| View | Sidebar label | Key content |
|------|--------------|-------------|
| Projects | (home, no project selected) | Cards with name, description, status badge, 🗑 delete button |
| Board | Board | Kanban columns, task cards with type/id/title/priority/release/assignee; drag-and-drop; empty state with "Create Default Board" |
| Backlog | Backlog | Tasks grouped by release; "→ Board" move button |
| Tasks | Tasks | Table with filter bar (search, status, priority, type); click row opens panel |
| Releases | Releases | Cards with name, goal, due date, status; Close/Reopen/Edit/🗑 |
| Pages | Pages | Tree sidebar + page view; Edit/Publish/Archive/🗑/Revisions actions |
| Repositories | Repositories | List of repo connections with 🗑; Add Repository form |
| Activity | Activity | Reverse-chronological project activity feed |

---

## E2E Test Suite

Tests live in `octbase-frontend/tests/` and run against a live API. Set `OCTBASE_API_BASE` to the API URL (default `http://127.0.0.1:8000`).

```sh
cd octbase-frontend/tests
OCTBASE_API_BASE=http://127.0.0.1:8000 pytest -v
OCTBASE_API_BASE=http://127.0.0.1:8000 pytest test_board.py -v
OCTBASE_API_BASE=http://127.0.0.1:8000 pytest test_task_panel.py -v
```

Requires: running API backed by PostgreSQL with `OCTBASE_DEMO_MODE=true`, frontend test dependencies installed in a virtualenv, and Playwright browsers installed (`python -m playwright install firefox`). The easiest setup is `podman-compose up --build -d` from the repo root. Access the app at **http://localhost:8080/**.

### Test files

| File | Coverage |
|------|----------|
| `conftest.py` | Fixtures: `app`, `demo_board`, `task_panel`, `api`; constants: `DEMO_PROJECT_ID`, `DEMO_TASK_ID` |
| `test_board.py` | Board structure, cards, create task, drag-and-drop, empty state |
| `test_task_panel.py` | Panel open/close, all 7 tabs, details editing, status/priority/type changes, copy, archive/reopen, immutable DONE state |
| `test_pages.py` | Page creation, edit, publish, archive, revision list |
| `test_releases.py` | Release CRUD, close/reopen |
| `test_activity.py` | Activity feed entries |

### Seed data the tests depend on

| Constant | Value |
|----------|-------|
| `DEMO_PROJECT_ID` | `00000000-0000-0000-0000-000000000101` |
| `DEMO_TASK_ID` | `00000000-0000-0000-0000-000000000201` |
| `DEMO_TASK_TITLE` | `Implement user authentication` |
| Demo task status | `IN_PROGRESS`, priority `HIGH`, type `TASK` |
| Demo task assignee | `Demo User` (`00000000-0000-0000-0000-000000000001`) |
| Demo task release | `v1.0 Launch` |
| Demo task has | 1 comment (`Working on this now`), 1 link (`JWT`), 1 attachment (`auth-diagram`), 1 relation, 1 branch (`feature/…`) |

---

## Common Issues

| Symptom | Likely cause |
|---------|-------------|
| Board shows "No board yet" for demo project | Seed did not run; `OCTBASE_DEMO_MODE=true` not set |
| Task panel does not open | `#task-panel` missing from DOM or JS error before `openTask()` |
| Panel tabs missing | `renderTaskPanel()` not finding `#task-panel-content` |
| Logo not showing | `logo.png` missing from the frontend container image; rebuild with `podman-compose build` |
| CSS not loading | `app.css` missing from container image |
| JS not loading | `app.js` missing from container image |
| `window.S` undefined in tests | `app.js` not loaded or executed before test selector runs |
| Drag-and-drop test skipped | No card in Review column; restore seed or create a task there first |
| `"body"` vs `"content"` field error | Pages API uses `content`, not `body` |
| `api` fixture `ImportError` | `api` is a pytest fixture, not a plain importable symbol — do not import it |
| Frontend at port 8000 instead of 8080 | App is now served by Caddy on 8080; the API on 8000 no longer serves the SPA |
| Board not refreshing after edit | A save function is missing `if(S.view==='board') renderBoard()` after its API call |
| `style="` found in `app.js` | Inline style introduced; move to a class in `app.css` |

---

## Deployment Notes

The core app stack runs three containers (the full `podman-compose.yml` also adds the
`octbase-web` landing page and its `mailer` service):

| Container | Image | Port | Purpose |
|-----------|-------|------|---------|
| `postgres` | `registry.access.redhat.com/hi/postgresql:latest` | — | Database |
| `octbase-api` | `localhost/octbase-api:latest` | 8000 | Go REST API |
| `octbase-frontend` | `localhost/octbase-frontend:latest` | 8080 | Caddy serving SPA + reverse proxy |

Start:
```sh
podman-compose up --build
```

The frontend nginx config (`octbase-frontend/nginx.conf`) proxies `/api/`, `/docs`, `/health`, and `/openapi.yaml` to `octbase-api:8000`. All other paths serve the SPA's `index.html`.

Each component can be built independently:
```sh
podman build -f octbase-api/Containerfile -t localhost/octbase-api:latest ./octbase-api
podman build -f octbase-frontend/Containerfile -t localhost/octbase-frontend:latest ./octbase-frontend
```
