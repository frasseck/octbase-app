# Octbase CRUD Completeness — Prompt 6

**Purpose:** Add full create/read/update/delete coverage for every entity in the Octbase API and expose the missing operations in the frontend.

**Baseline:** Prompt 1 (API) + Prompt 2 (frontend) describe the existing system. This prompt documents only the **additions**.

---

## 1. Gap Analysis

Before this change, the following operations were missing:

| Entity | Missing |
|---|---|
| Projects | `DELETE` |
| Tasks | `DELETE` |
| Task comments | `PATCH`, `DELETE` |
| Releases | `DELETE` |
| Boards | `DELETE` |
| Pages | `DELETE` (hard delete; archive existed) |
| Repository connections | `DELETE` |
| Branch references | `DELETE` |

Frontend exposure gaps (entities with no management UI):
- Comment edit and delete
- Branch delete
- Release delete
- Task hard-delete (archive existed)
- Project delete
- Page delete
- Repository connection create / delete

---

## 2. New API Endpoints

### Projects
```
DELETE /api/projects/{projectId}
       Cascades: memberships, task categories, task templates, releases,
                 boards → columns, tasks → comments/links/attachments/relations/branches,
                 pages → revisions/references, repository connections → branches,
                 activity entries.
       → 204 No Content | 404 PROJECT_NOT_FOUND
```

### Tasks
```
DELETE /api/tasks/{taskId}
       Cascades: branch_references, page_task_references, task_relations,
                 task_attachments, task_links, task_comments.
       → 204 No Content | 404 TASK_NOT_FOUND
```

### Task Comments
```
PATCH  /api/tasks/{taskId}/comments/{commentId}
       Body: { text: string }
       → TaskComment | 404 COMMENT_NOT_FOUND

DELETE /api/tasks/{taskId}/comments/{commentId}
       → 204 No Content | 404 COMMENT_NOT_FOUND
```

### Releases
```
DELETE /api/releases/{releaseId}
       Nullifies release_id on all tasks assigned to this release before deleting.
       → 204 No Content | 404 MILESTONE_NOT_FOUND
```

### Boards
```
DELETE /api/boards/{boardId}
       Sets board_column_id = NULL on all tasks in the board's columns,
       then deletes columns, then the board.
       → 204 No Content | 404 BOARD_NOT_FOUND
```

### Pages
```
DELETE /api/pages/{pageId}
       Cascades: page_revisions, page_task_references.
       → 204 No Content | 404 PAGE_NOT_FOUND
```

### Repository Connections
```
DELETE /api/repository-connections/{repositoryId}
       Cascades: branch_references for this repository.
       → 204 No Content | 404 REPO_NOT_FOUND
```

### Branch References
```
DELETE /api/tasks/{taskId}/branches/{branchId}
       → 204 No Content | 404 BRANCH_NOT_FOUND
```

---

## 3. Cascade Strategy

All cascades are implemented **in the repository layer** using a database transaction. No `ON DELETE CASCADE` FK constraints are added to the schema. The transaction executes ordered `DELETE`/`UPDATE` statements to respect FK constraints:

- Child rows are deleted before parent rows.
- `NULL`able FK columns (e.g. `task.release_id`, `task.board_column_id`) are cleared with `UPDATE` before the parent is deleted.

---

## 4. Frontend Additions

### API client (app.js)

New entries in the `api` object:
```js
comments: { ..., update:(tid,id,text)=>http.patch(`/api/tasks/${tid}/comments/${id}`,{text}),
                  del:   (tid,id)   =>http.del(`/api/tasks/${tid}/comments/${id}`) }
releases: { ..., del: (id)=>http.del(`/api/releases/${id}`) }
boards: { ..., del: (id)=>http.del(`/api/boards/${id}`) }
projects: { ..., del: (id)=>http.del(`/api/projects/${id}`) }
tasks: { ..., del: (id)=>http.del(`/api/tasks/${id}`) }
pages: { ..., del: (id)=>http.del(`/api/pages/${id}`) }
repos: { ..., del: (id)=>http.del(`/api/repository-connections/${id}`) }
branches: { ..., del:(tid,id)=>http.del(`/api/tasks/${tid}/branches/${id}`) }
```

### Comments tab (task panel)

Each comment row gains two icon buttons:
- **Edit (✎)**: reveals an inline `<textarea>` pre-filled with the comment text and a Save button. On save, calls `PATCH` and re-renders.
- **Delete (✕)**: calls `DELETE` and re-renders. Only visible on comments authored by the current demo user.

### Branches tab (task panel)

Each branch row gains a **delete (✕)** button that calls `DELETE /api/tasks/{taskId}/branches/{branchId}` and re-renders.

### Task panel action bar

A **Delete** button is added next to the Archive button. It opens a confirmation modal ("Permanently delete this task and all its data?"). On confirm, calls `DELETE /api/tasks/{id}`, closes the panel, and refreshes the task list / board.

### Releases view

Each release card gains a **Delete** button. It opens a confirmation modal. On confirm, calls `DELETE /api/releases/{id}` and re-renders the release list.

### Pages view

The page action bar gains a **Delete** button (alongside Publish / Archive). It opens a confirmation modal. On confirm, calls `DELETE /api/pages/{id}`, clears `S.selectedPage`, and re-renders the page tree.

### Projects view

Each project card gains a **Delete** button. It opens a confirmation modal warning that all project data will be permanently removed. On confirm, calls `DELETE /api/projects/{id}`, navigates back to the project list, and re-renders.

### Repositories view (new sidebar nav item)

A new **Repositories** nav item is added to the project sidebar (between Pages and Activity). It renders a list of repository connections with:
- Provider badge, display name, repository URL, default branch
- **Delete (✕)** button per row
- **Add repository** form below the list: display name input, URL input, provider select (`FAKE_GITLAB` / `GITHUB` / `BITBUCKET`), default branch input, Add button

Deleting a repository confirms with the user ("This will also remove all branch references for this repository") then calls `DELETE`.

---

## 5. Confirmation Modal Pattern

Destructive deletes use the existing `showModal` helper with a danger-styled submit button:

```js
showModal('Delete X', 'Are you sure? This cannot be undone.',
  async () => { await api.x.del(id); /* refresh */ }, 'Delete');
```

The submit button in the modal footer gets class `btn-danger` for delete confirmations. Add a helper:

```js
function confirmDelete(title, body, onConfirm) {
  showModal(title, body, onConfirm, 'Delete');
  el('#modal-submit').className = 'btn btn-danger';
}
```

---

## 6. No Schema Changes

The migration file (`migrations/001_initial.sql`) is unchanged. Cascade logic lives entirely in Go repository methods. No new migration file is needed.
