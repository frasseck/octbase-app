# Octbase — Missing Features & MVP Caveats

> **Archived:** Superseded by `16_octbase-new-features.md`. Task relations, links, and attachments now have full frontend exposure (real file upload, not the metadata-only form described here). Kept for historical context only.

You are a senior software engineer finishing the Octbase MVP.
The backend is complete. Three UI capabilities are fully implemented in the
Go API but have **no frontend exposure**: task relations, task links, and task
attachments. Task categories (labels) exist in the backend but are unused in
the UI. List views are capped at 200 rows with no pagination. This prompt
covers all five gaps.

Do not assume previous AI-generated code is correct. Read every relevant file
before changing it. Work in small, safe patches; run tests after each change.

---

## Application overview

| Layer | Location |
|---|---|
| Go API | `octbase-api/` — chi router, PostgreSQL, JWT auth |
| Entry point | `cmd/octbase-api/main.go` |
| Frontend | `octbase-frontend/js/app.js` + `css/app.css` (vanilla JS SPA, **no build tool**) |
| Tests | `octbase-api/internal/workmanagement/handler_test.go` (integration, needs `TEST_DATABASE_URL`) |

The task panel in `app.js` (`renderTaskPanel` / `renderTaskDetails`) renders
four tabs: **Details · Comments · Branches · Activity**.

---

## Feature 1 — Task Relations UI tab

### What exists in the backend

Routes registered in `workmanagement/handler.go`:
```
POST   /api/v1/tasks/{taskId}/relations
GET    /api/v1/tasks/{taskId}/relations
DELETE /api/v1/tasks/{taskId}/relations/{relationId}
```

Domain: `workmanagement.TaskRelation` with fields `id`, `sourceTaskId`,
`targetTaskId`, `relationType` (`RELATES_TO`, `BLOCKS`, `BLOCKED_BY`,
`DUPLICATES`), `createdAt`.

Service rule in `workmanagement/service.go`:
- `AddRelation` prevents self-relations, duplicate relations, and cycles in
  `BLOCKS` chains.
- Deleting a relation also deletes the symmetric inverse in a transaction.

Frontend API stubs already defined in `app.js`:
```js
relations: {
  list: (tid)    => http.get(`${V}/tasks/${tid}/relations`),
  add:  (tid,d)  => http.post(`${V}/tasks/${tid}/relations`, d),
  del:  (tid,id) => http.del(`${V}/tasks/${tid}/relations/${id}`),
},
```

### What to build

Add a **Relations** tab between Branches and Activity.

1. **`renderTaskPanel`** — load relations alongside the existing parallel
   fetches:
   ```js
   const [task, comments, activity, branches, relations] = await Promise.all([
     api.tasks.get(taskId),
     api.comments.list(taskId),
     api.tasks.activity(taskId).catch(()=>[]),
     api.branches.list(taskId).catch(()=>[]),
     api.relations.list(taskId).catch(()=>[]),
   ]);
   ```

2. **Panel tabs** — insert the Relations button:
   ```html
   <button class="panel-tab ..." onclick="switchPanelTab('relations')">
     Relations (${relations.length})
   </button>
   ```
   Update the tab-body switch:
   ```js
   S.taskPanelTab === 'relations' ? renderTaskRelations(task, relations) :
   ```

3. **`renderTaskRelations(task, relations)`** — render the list and an add form:
   - Each existing relation: show `relationType` badge + linked task title
     (search by task id using `api.tasks.get` lazily, or pre-fetch from
     `S.tasks` if already loaded). Include a delete button.
   - Add form: a task search input (`api.tasks.search(projectId, q)` —
     already wired) plus a relation-type selector
     (`['RELATES_TO','BLOCKS','BLOCKED_BY','DUPLICATES']`).
   - On submit: `api.relations.add(taskId, {targetTaskId, relationType})`.
   - Show the `TASK_RELATION_CYCLE` or `TASK_SELF_RELATION` error from the
     API as a toast; do not crash.

4. **Delete flow**: `api.relations.del(taskId, relationId)` then re-render
   the tab. The backend already handles the symmetric inverse.

### CSS

Add to `app.css`:
```css
.relations-section { padding: 16px; }
.relation-item { display: flex; align-items: center; gap: 8px; padding: 8px 0;
  border-bottom: 1px solid var(--border); font-size: 13px; }
.relation-type-badge { font-size: 10px; font-weight: 600; text-transform: uppercase;
  padding: 2px 6px; border-radius: 2px; background: #ebecf0; color: var(--muted); }
.relation-form { display: flex; flex-wrap: wrap; gap: 6px; padding-top: 12px;
  align-items: center; }
```

### Tests to add (`handler_test.go`)

No new backend tests are needed — `TestAddAndListRelations`, `TestDeleteRelation`,
and `TestAddRelation_SelfRelationHTTP` already exist. Verify they still pass.

---

## Feature 2 — Task Links UI tab

### What exists in the backend

Routes:
```
POST   /api/v1/tasks/{taskId}/links
GET    /api/v1/tasks/{taskId}/links
DELETE /api/v1/tasks/{taskId}/links/{linkId}
```

Domain: `workmanagement.TaskLink` with fields `id`, `taskId`, `url`, `title`,
`createdAt`.

Frontend stubs:
```js
links: {
  list: (tid)    => http.get(`${V}/tasks/${tid}/links`),
  add:  (tid,d)  => http.post(`${V}/tasks/${tid}/links`, d),
  del:  (tid,id) => http.del(`${V}/tasks/${tid}/links/${id}`),
},
```

### What to build

Add a **Links** tab (place it after Relations, before Activity).

1. Load links alongside the parallel fetches in `renderTaskPanel`.

2. **`renderTaskLinks(task, links)`**:
   - List: each link rendered as `<a href="${url}" target="_blank">${title||url}</a>`
     with a delete button.
   - Add form: `url` input (required) + `title` input (optional) + Add button.
   - On submit: `api.links.add(taskId, {url, title})`.
   - Validate that `url` starts with `http://` or `https://`; show toast error
     if not (don't rely solely on the backend).

3. Show link count in the tab label.

### CSS

```css
.links-section { padding: 16px; }
.link-item { display: flex; align-items: center; gap: 8px; padding: 8px 0;
  border-bottom: 1px solid var(--border); font-size: 13px; }
.link-item a { flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.link-form { display: flex; flex-wrap: wrap; gap: 6px; padding-top: 12px; }
```

### Tests to add

`TestAddListDeleteLink` already exists and covers the backend. No new backend
tests needed. Verify it still passes.

---

## Feature 3 — Task Attachments UI tab

### What exists in the backend

Routes:
```
POST   /api/v1/tasks/{taskId}/attachments
GET    /api/v1/tasks/{taskId}/attachments
DELETE /api/v1/tasks/{taskId}/attachments/{attachmentId}
```

Domain: `workmanagement.TaskAttachment` — `id`, `taskId`, `filename`,
`contentType`, `sizeBytes`, `externalUrl`, `createdAt`.

**Important**: the backend stores metadata only (no binary blobs). The
`externalUrl` field is a URL to an externally-hosted file. The `POST` endpoint
accepts `{filename, contentType, sizeBytes, externalUrl}` — it is a metadata
registration endpoint, not a multipart upload. Actual file upload is handled by
Feature 4 in prompt 16.

Frontend stubs:
```js
attachments: {
  list: (tid)    => http.get(`${V}/tasks/${tid}/attachments`),
  add:  (tid,d)  => http.post(`${V}/tasks/${tid}/attachments`, d),
  del:  (tid,id) => http.del(`${V}/tasks/${tid}/attachments/${id}`),
},
```

### What to build

Add a **Files** tab (place it between Links and Activity).

1. Load attachments alongside the parallel fetches.

2. **`renderTaskAttachments(task, attachments)`**:
   - List: each attachment shows `filename`, a human-readable file size
     (`formatBytes(n)` helper), and an external link `<a href="${externalUrl}"
     target="_blank">Open</a>`. Include a delete button.
   - Add form (metadata only for now): inputs for `filename`, `externalUrl`,
     `contentType` (optional, defaults to `application/octet-stream`). Prompt
     16 replaces this with a real upload form.
   - On submit: `api.attachments.add(taskId, {filename, externalUrl,
     contentType: contentType||'application/octet-stream', sizeBytes: 0})`.

3. Helper:
   ```js
   function formatBytes(n) {
     if (!n) return '—';
     if (n < 1024) return n + ' B';
     if (n < 1048576) return (n/1024).toFixed(1) + ' KB';
     return (n/1048576).toFixed(1) + ' MB';
   }
   ```

### CSS

```css
.attachments-section { padding: 16px; }
.attachment-item { display: flex; align-items: center; gap: 8px; padding: 8px 0;
  border-bottom: 1px solid var(--border); font-size: 13px; }
.attachment-name { flex: 1; font-weight: 500; overflow: hidden;
  text-overflow: ellipsis; white-space: nowrap; }
.attachment-size { font-size: 11px; color: var(--muted); }
```

---

## Feature 4 — Task Categories (Labels) UI

### What exists in the backend

Routes:
```
POST   /api/v1/projects/{projectId}/task-categories
GET    /api/v1/projects/{projectId}/task-categories
PATCH  /api/v1/task-categories/{categoryId}
DELETE /api/v1/task-categories/{categoryId}
```

Domain: `workmanagement.TaskCategory` — `id`, `projectId`, `name`,
`description`, `color`, `createdAt`, `updatedAt`.

**Note**: there is currently no join between tasks and categories in the schema.
The `task_categories` table stores reusable labels per project, but `tasks` has
no `category_id` column. Implementing full task↔category assignment requires a
migration and a new M:N join table. This prompt covers the categories management
UI only (CRUD). Assigning categories to tasks is post-MVP.

### What to build

#### 4a — Project Settings: Categories manager

Add a **Settings** entry to the project sidebar nav (after Activity):

```js
{id:'settings', icon:'⚙', label:'Settings'},
```

In `renderContent()`:
```js
case 'settings': await renderProjectSettings(); break;
```

**`renderProjectSettings()`**:
- Fetch categories: `GET /api/v1/projects/{projectId}/task-categories`
- Render a two-column layout: left is a list of existing categories, right is a
  create/edit form.
- Each category: colored dot (using the `color` field as a CSS color name or
  hex), `name`, and Edit / Delete buttons.
- Create form: `name` (required), `description` (optional), `color` picker
  (a simple `<input type="color">` or a palette of preset colors:
  `['gray','blue','green','yellow','orange','red','purple','teal']`).
- Edit: reuse the create form pre-filled, PATCH on save.
- Delete: confirm modal, then `api.categories.del(categoryId)`.

Add to the frontend `api` object:
```js
categories: {
  list:   (pid)   => http.get(`${V}/projects/${pid}/task-categories`),
  create: (pid,d) => http.post(`${V}/projects/${pid}/task-categories`, d),
  update: (id,d)  => http.patch(`${V}/task-categories/${id}`, d),
  del:    (id)    => http.del(`${V}/task-categories/${id}`),
},
```

#### 4b — Category color swatches

In `app.css`, add preset category color classes used by the color picker and
display badges:
```css
.category-dot { display: inline-block; width: 10px; height: 10px;
  border-radius: 50%; flex-shrink: 0; }
.category-badge { display: inline-flex; align-items: center; gap: 4px;
  padding: 2px 8px; border-radius: 10px; font-size: 11px; font-weight: 500;
  background: #ebecf0; color: var(--text); }
```

The `color` field is stored as a plain string (CSS color name or hex). Render the
dot with `style="background:${esc(cat.color)}"`.

### Tests to add (`handler_test.go`)

`TestTaskCategories_CRUD` already covers the backend. Verify it still passes.

---

## Feature 5 — Pagination for large projects

### Problem

Every list-fetching call in the frontend hardcodes `size:200`:
```js
await api.tasks.list(S.project.id, { size:200 })
```

The backend `ListTasks` handler uses `shared.ParsePagination` which caps at 200.
For projects with more than 200 tasks, the board and backlog silently omit tasks.

### What to build

This is intentionally scoped to a lightweight "Load more" pattern — not full
pagination controls — to avoid a large frontend rewrite.

#### 5a — Backend: verify no hard cap below 200

`shared.ParsePagination` in `shared/httpx.go` caps `size` at 200.
Raise the cap to 500 for task lists (but not for search results):

```go
// In ListTasks handler, before calling shared.ParsePagination:
pg := shared.ParsePaginationWithMax(r, 500)
```

Add `ParsePaginationWithMax(r *http.Request, max int) PaginationParams` to
`shared/httpx.go`:
```go
func ParsePaginationWithMax(r *http.Request, max int) PaginationParams {
    p := ParsePagination(r)
    if p.Size > max { p.Size = max }
    return p
}
```

This lets the frontend request up to 500 tasks in one call without changing the
default for other endpoints.

#### 5b — Frontend: increase default page size to 500

Change every `size:200` to `size:500` in `app.js`:
- `renderBoard` → `api.tasks.list(S.project.id, { size:500 })`
- `renderBacklog` → `api.tasks.list(S.project.id, { size:500 })`
- `renderTaskList` → `api.tasks.list(S.project.id, { size:500 })`
- `renderReleases` → `api.tasks.list(S.project.id, { size:500 })`
- `renderSprints` → `api.tasks.list(S.project.id, { size:500 })`
- `renderActivity` → `api.tasks.list(S.project.id, { size:500 })`

#### 5c — Add a visible overflow notice

If the response length equals the page size (i.e., there may be more tasks),
show a notice:

```js
// after fetching tasks:
if (tasks.length >= 500) {
  // append a notice to the top of the content area
  const notice = document.createElement('div');
  notice.className = 'list-overflow-notice';
  notice.textContent = 'Showing 500 tasks. Use filters to narrow results.';
  el('#content').prepend(notice);
}
```

```css
/* in app.css */
.list-overflow-notice { background: #fffae6; border: 1px solid #ff8b00;
  color: #172b4d; padding: 8px 16px; border-radius: var(--radius);
  font-size: 13px; margin-bottom: 12px; }
```

Do NOT implement full pagination controls (prev/next page UI) — that requires
a larger refactor of the state model and is out of scope for this prompt.

---

## Implementation order

Work in this order to minimize risk:

1. **Feature 5 (pagination)** — pure addition, no UI restructuring, no backend
   schema changes. Commit separately.
2. **Feature 2 (links)** — simplest new tab; establishes the pattern for 3 and 4.
3. **Feature 3 (attachments)** — same pattern as links.
4. **Feature 1 (relations)** — most complex because of task search + cycle errors.
5. **Feature 4 (categories)** — new sidebar view; can be done independently.

---

## After each feature

1. Run `go build ./... && go vet ./... && go test ./... -short` — must pass.
2. Copy updated frontend files: `cp octbase-frontend/js/app.js octbase-api/web/js/app.js`
   and `cp octbase-frontend/css/app.css octbase-api/web/css/app.css`.
3. Manually verify in the browser (standalone file:// or via podman-compose stack)
   using `demo@octbase.dev` / `demo1234`.

---

## What NOT to do

- Do not add a real file storage system (S3, local disk, etc.) — that is prompt 16.
- Do not add task↔category assignment (M:N join table) — post-MVP scope.
- Do not add full pagination controls — out of scope (use the overflow notice).
- Do not refactor the existing panel tab system — extend it only.
- Do not add any new Go dependencies.
