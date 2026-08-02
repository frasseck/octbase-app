# UX & Bug TODO

> **Archived:** This was a snapshot from a 2026-05-30 test run. The sampled items have since been fixed in the codebase. Kept for historical context only.

Results of a full end-to-end browser test run on 2026-05-30 against the running
`podman-compose` stack (`demo@octbase.dev` / `demo1234`).

---

## 🔴 Bugs — broken behaviour

### 1. Demo seed writes password hash in wrong bcrypt prefix
`seed.go` currently inserts the demo user without a password (comment says "use invite
flow"), but the running DB ends up with a `$2y$` hash (PHP bcrypt) which Go's
`golang.org/x/crypto/bcrypt` treats as an unknown variant and rejects. Anyone starting
fresh can never log in.

**Fix:** Either remove the password_hash column from the seed upsert entirely (keep the
invite-flow comment) and document the one-time setup step, or generate the hash in Go
(`auth.HashPassword`) and hard-code the `$2a$12$...` result in seed.go.

---

### 2. Search page: Enter key does nothing
`renderSearchPage` puts a `<button onclick="runSearchPage()">` next to the input but
adds no `onkeydown` handler. Pressing Enter is the natural action; the button alone is
a dead-end for keyboard users.

**Fix in `app.js`:**
```js
// in renderSearchPage(), change the input to:
<input ... id="sp-input" onkeydown="if(event.key==='Enter')runSearchPage()" ...>
```

---

### 3. ~~Task panel remembers previous task's tab~~ [FIXED]
`S.taskPanelTab` is never reset when a different task is opened. Open task A, switch to
Activity, close the panel, open task B — you land on Activity, not Details.

**Fix:** Reset to `'details'` at the top of `openTaskPanel()`:
```js
async function openTaskPanel(taskId) {
  if (S.taskPanelId !== taskId) S.taskPanelTab = 'details';
  ...
}
```

---

### 4. Release "Close" button has no visible style
The code uses class `btn btn-success btn-sm` but `app.css` defines no `.btn-success`
rule. The button renders identically to `btn-secondary` (white/border), making Close
and Edit look the same.

**Fix:** Add to `app.css`:
```css
.btn-success { background: #00875a; color: #fff; }
.btn-success:hover { background: #006644; }
```

---

### 5. Dashboard "Recent Pages" rows are not clickable
`renderDashboardPage` renders `.dash-page-row` divs with no `onclick`. Clicking them
does nothing. Users expect to navigate to the page.

**Fix:** Add `onclick` that navigates to the project + page:
```js
const pageRow = (p) => `
  <div class="dash-page-row" style="cursor:pointer"
       onclick="selectProject('${p.projectId}').then(()=>{S.selectedPage='${p.id}';setView('pages')})">
    📄 ${esc(p.title)} <span class="text-muted text-sm">${esc(p.projectName)}</span>
  </div>`;
```

---

### 6. Dashboard "Upcoming Releases" rows are not clickable
Same pattern — `.dash-release-row` has no `onclick`. Users expect a click to open
the project's releases view.

**Fix:** Add `onclick="selectProject('${m.projectId}').then(()=>setView('releases'))"`.

---

### 7. ~~Inline task creation (N key) types "n" into the new input~~ [FIXED]
When `showInlineTaskCreate` is triggered by the `keydown` handler on `'n'/'N'`, the
same keypress event fires on the newly-focused `<input>`, inserting the character "n"
as the first character of the task title.

**Fix:** In the `showInlineTaskCreate` input's `focus` / creation block, clear any
pre-filled value or stop the keydown propagation:
```js
// after inserting and focusing the input:
input.value = '';
```
Or in the global keydown handler, call `e.preventDefault()` for `'n'` before calling
`showInlineTaskCreate()`.

---

### 8. Archived tasks show in Backlog and Board without visual distinction
Tasks with `status=ARCHIVED` are returned by the task list API and appear inline with
active tasks in both the backlog rows and board columns. The `ARCHIVED` badge is shown
but there's no separation or default filtering. The backlog screenshot shows "Fix auth
bug — ARCHIVED" mixed between active tasks.

**Fix (option A — filter by default):** Pass `excludeArchived=true` in the default
task list call, and show archived tasks only when a filter explicitly requests them.

**Fix (option B — visual group):** Separate archived tasks into a collapsed "Archived"
section at the bottom of backlog, hidden by default.

---

### 9. Search topbar title shows "Projects" instead of "Search"
When `S.project = null` and `S.view !== 'dashboard'`, `renderTopbar` falls through to
the title `'Projects'`. The search page (`/search`) therefore shows "Projects" in the
topbar breadcrumb.

**Fix:** In `renderTopbar`, add a check for the search view or derive the title from
the route:
```js
const title = S.view === 'dashboard' ? 'My Work'
            : S.view === 'search'    ? 'Search'
            : 'Projects';
```
And set `S.view = 'search'` inside `renderSearchPage`.

---

### 10. S.repos not loaded when opening task Branches tab from outside Repos view
The branch-link form renders `<option value="${r.id}">${r.displayName}</option>` from
`S.repos`, but `S.repos` is only populated when `renderRepos()` runs. Opening a task
from the board without having visited Repositories first means the repo selector is
always empty, making the Branches tab non-functional.

**Fix:** Load repos at project load time in `loadProject()`:
```js
S.repos = await api.repos.list(pid).catch(() => []);
```

---

---

### 10b. ~~No way to move a backlog task onto the board~~ [FIXED]
`api.boards.move()` is only called when dragging between board columns or immediately
after creating a task while the board view is open. A task already in the backlog
(no `boardColumnId`) has no UI path to the board — there is no "Move to board" button
and the task panel Details tab has no Board Column picker.

**Fix implemented:** Bulk action bar now shows an "Add to board…" column selector in
backlog view and a "→ Backlog" button in board view, allowing multiple selected tasks
to be moved between board and backlog in one action.

---

## 🟡 Usability — confusing but not broken

### 11. Task description saves on blur with no feedback
The description `<textarea>` uses `onchange` (fires on blur). There is no explicit
Save button, no "unsaved changes" indicator, and no toast until the user clicks away.
A user who types and then immediately clicks a different panel tab loses their changes
(panel re-renders before blur fires on some browsers).

**Fix:** Change to an explicit Save button or switch to `oninput` + debounced auto-save
with a subtle "Saving…" / "Saved" indicator.

---

### 12. Sidebar shows too many test-artifact projects
The sidebar lists every project ever created, including integration-test leftovers
("UI Test Project 9675483", "Board Creation Project 9479963", etc.). With 8+ entries
the sidebar becomes unusable before the product even ships.

**Fix (data):** The seed should run a cleanup step that deletes non-seed projects on
demo startup, or the test suite should use isolated schemas so test data never lands in
the shared DB.

**Fix (UI):** Cap the sidebar project list to the 5 most-recently-visited projects with
a "See all →" link.

---

### 13. Activity feed entries have no task name or link
Every entry reads "task moved on board" or "task status changed to IN_REVIEW" with no
task title, no sequence number, and no click target. The feed is visually dense but
informationally useless.

**Fix:** The API already returns `message` strings like "Task created: Implement user
authentication". If those messages aren't rich enough, include `taskId` in the activity
item and make each row `onclick="openTaskPanel(e.taskId)"`.

---

### 14. "Create Task" modal has no Assignee field
Users frequently want to assign immediately on creation. The modal asks for Title, Type,
Priority, Release, Description — but not Assignee. Every newly-created task starts
Unassigned, requiring a second interaction to open the panel and set it.

**Fix:** Add an Assignee `<select>` to the `showCreateTask` modal, populated from
`S.members`, defaulting to "Unassigned".

---

### 15. Projects grid renders as a plain list, not cards
`renderProjects` emits `.project-card` divs, but the CSS rule for `.project-card`
(border-radius, padding, icon background etc.) only applies when inside `.project-card`
— the grid wrapper `<div class="projects-grid">` has no CSS definition. The projects
list looks like bare unstyled rows.

**Fix:** Add `.projects-grid` styles:
```css
.projects-grid { display: flex; flex-direction: column; gap: 8px; max-width: 700px; }
```

---

### 16. Topbar "Projects" title vs "My Work" confusion
The `My Work` item in the sidebar is active on the dashboard, but the breadcrumb in the
topbar says "My Work". Clicking "All Projects" in the topbar area when inside a project
is the `‹ All Projects` back link in the sidebar — but there is no standalone topbar
breadcrumb for the projects list. Users landing on `/projects` see the topbar title
"Projects" but the sidebar label "PROJECTS" instead of a unified heading.

**Fix:** Standardize: sidebar section header + topbar title should match. Use
`S.view` to drive a single title string.

---

### 17. Board column count badge doesn't reflect applied filters
The badge shows task count per column from the pre-filter fetch. When a priority filter
is applied, cards disappear but the count badge still shows the unfiltered total.

**Fix:** Compute badge counts from the filtered `tasksByCol` array, not from the API
total.

---

### 18. Task type filter missing from topbar
Users can filter by priority and status but not by task type (Bug, Story, Epic, etc.),
even though type is a prominent piece of information displayed on every card.

**Fix:** Add a Type filter `<select>` to the `filterBar` in `renderTopbar`.

---

## 🟢 Polish — makes it fun to use

### 19. Add keyboard shortcut hints to sidebar nav items
The CSS already defines `.kbd-hint` for sidebar items, but no shortcuts are rendered.
Adding `<span class="kbd-hint">B</span>` next to Board, `L` next to Backlog etc. would
teach keyboard navigation to new users instantly.

---

### 20. Empty board columns should have a "+ Add task" affordance at the bottom
The `col-add-btn` CSS class exists but no element in the board column template uses it.
An empty column with a faint "+ Add" button at the bottom is far more inviting than a
blank grey area.

**Fix:** Add to each column in `renderBoard`:
```js
<button class="col-add-btn" onclick="showCreateTask()">+ Add task</button>
```

---

### 21. Command palette page results don't navigate to the page
In `paletteSelect`, clicking a page result does:
```js
if (S.project) router.go(`/projects/${S.project.id}/pages`);
```
This navigates to the pages view root, not the specific page. The selected page ID is
available in `_paletteResults[idx]`.

**Fix:**
```js
} else if (r.type === 'page') {
  S.selectedPage = r.id;
  if (S.project && S.project.id === r.projectId) {
    setView('pages');
  } else {
    await selectProject(r.projectId);
    S.selectedPage = r.id;
    setView('pages');
  }
}
```

---

### 22. "Assign to me" (A shortcut) has no visual feedback in the panel details
After pressing A, a toast says "Assigned to you" and the panel reloads. But the panel
reload causes a full re-fetch with a spinner flash. Since assignment only changes one
field, an optimistic update would feel instant.

---

### 23. Page editor title is read-only in edit mode
The `page-editor-title` element in edit mode is a static `<div>` — you can't rename a
page while editing it. The title is only editable in a separate workflow (delete +
recreate). Add a writable `<input>` for the title in edit mode.

---

### 24. Release progress is not shown
Release cards show Name, Due Date, Goal, and Status but no task count or
done/total progress bar (e.g. "3/7 tasks done"). This is the primary reason people use
releases.

**Fix:** Include task counts in the releases API response or fetch them per release
and render a `<progress>` bar on each card.

---

### 25. Board header area is empty (`<div class="board-header"></div>`)
The board has an unexplained empty div at the top. Remove it or use it for the filter
bar (currently in the topbar).

---

### 26. Login page has no loading/submitting state on the button
After clicking "Sign in", the button remains active and clickable during the network
request. A double-submit is possible.

**Fix:** Disable the button and show a spinner label during `doLogin`.

---

### 27. Notification bell icon is the 🔔 emoji — yellow on white
The emoji bell has OS-dependent rendering and looks like a warning icon on some
platforms (especially Linux). Use an SVG icon or a Unicode symbol that doesn't carry
warning connotations.

---

### 28. Repos view uses unstyled `.repo-row` instead of `.repo-item` from CSS
`renderRepos` renders `<div class="repo-row">` but the CSS defines `.repo-item` with
proper padding, border-radius and border. The existing CSS rule is dead and the view
renders without card borders.

**Fix:** Change `class="repo-row"` → `class="repo-item"` in `renderRepos`.

---

*End of UX audit — 2026-05-30*
