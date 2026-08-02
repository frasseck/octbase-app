# Octbase API — Recreation Prompt

**Purpose:** Recreate the Octbase API PostgreSQL POC baseline.  
**Stack:** Go 1.25 · go-chi/chi v5 · PostgreSQL (lib/pq) · single binary  
**Run:** `OCTBASE_DATABASE_URL="postgres://..." OCTBASE_DEMO_MODE=true ./octbase-api` or via `podman-compose.yml`  
**Port:** the API listens on 8000 by default in both local runs and the container setup

> **Iterative baseline:** This prompt describes the state after the SQLite→PostgreSQL migration (`5_octbase-db.md`) but **before** the MVP hardening (`8_octbase-create-mvp.md`). Key differences from the current MVP: routes have no `/api/v1/` prefix; authentication uses `X-User-Id` headers; the `internal/auth`, `internal/admin`, `internal/notifications`, `internal/webhooks`, and `internal/sse` packages, JWT tokens, `golang-migrate`, and the `/api/v1/` route prefix are all introduced in `8_octbase-create-mvp.md`.

---

## 1. Product Overview

Octbase is a developer-centric task and documentation management API for software teams. It connects project work, task management, AsciiDoc documentation, and source-code branch references in one focused tool.

The prototype is a **modular monolith** written in Go. It is intentionally lightweight: no authentication framework, no message broker, no external services. It uses PostgreSQL as its only dependency beyond the Go standard library.

---

## 2. Repository Layout

```
octbase-api/
├── cmd/octbase-api/main.go          entry point, router wiring
├── internal/
│   ├── shared/                       shared helpers (DB, HTTP, UUID, CORS)
│   ├── seed/                         deterministic demo seed data
│   ├── workmanagement/               core domain (projects, tasks, boards, releases)
│   ├── identityaccess/               users and project memberships
│   ├── docs/                         documentation pages (AsciiDoc)
│   ├── scmintegration/               repository connections and branch references
│   └── activity/                     activity feed (audit log)
├── migrations/
│   └── 001_initial.sql               single SQL migration; applied on startup
├── api/
│   └── openapi.yaml                  core OpenAPI contract snapshot
├── Containerfile
├── podman-compose.yml
└── go.mod
```

Each domain package has exactly four files:

| File | Purpose |
|---|---|
| `domain.go` | Structs, constants, pure helper functions |
| `repo.go` | SQL queries wrapped in repository structs |
| `handler.go` | HTTP handlers, route registration |
| `service.go` | Cross-aggregate business rules (only in workmanagement) |

---

## 3. Technology Decisions (versus the original design document)

The design document specified Java/Spring Boot + PostgreSQL. The prototype was built in Go instead. The database was initially SQLite for simplicity, then migrated to PostgreSQL to match the original design spec.

| Design doc | Actual implementation |
|---|---|
| Java 21 / Spring Boot | Go 1.25 |
| PostgreSQL + Flyway | PostgreSQL (`lib/pq`) + single SQL migration file applied on startup |
| Spring Security | `X-User-Id` request header, no tokens |
| AsciidoctorJ | Hand-rolled AsciiDoc subset renderer (headings, lists, bold) |
| `/actuator/health` | `/health` |
| Hexagonal module structure | Flat package structure (domain/repo/handler/service per package) |

---

## 4. Authentication

Every request optionally carries an `X-User-Id` header containing a UUID.

- **GET** requests: header is optional; no check is done.
- **Non-GET** requests (POST/PATCH/DELETE): a valid UUID in `X-User-Id` is required. Missing or malformed UUID returns `401 UNAUTHORIZED`.

The middleware also sets permissive CORS headers on every response:

```
Access-Control-Allow-Origin: *
Access-Control-Allow-Methods: GET, POST, PUT, PATCH, DELETE, OPTIONS
Access-Control-Allow-Headers: Content-Type, X-User-Id, Authorization
```

OPTIONS preflight requests return `204 No Content`.

---

## 5. Error Response Format

All errors return JSON:

```json
{"code": "ERROR_CODE", "message": "Human-readable description"}
```

Standard error codes: `BAD_REQUEST`, `UNAUTHORIZED`, `NOT_FOUND`, `VALIDATION_ERROR`, `DB_ERROR`. Domain-specific codes are described per endpoint below.

---

## 6. Pagination

Endpoints that return lists accept `?page=0&size=20` query parameters. Default size is 20. Page is zero-indexed.

---

## 7. Domain Constants

### Task Statuses
```
PLANNED     initial status; on backlog or board
IN_PROGRESS task is being worked on
IN_REVIEW   in code/peer review
DONE        complete and immutable
ARCHIVED    archived and immutable
```

`DONE` and `ARCHIVED` tasks cannot be modified (enforced by `IsImmutable()`). To unblock them, call `/reopen`.

### Task Types
```
TASK  BUG  STORY  EPIC  CHORE
```

### Priorities
```
LOW  MEDIUM  HIGH  CRITICAL
```
Default priority: `MEDIUM`

### Relation Types
```
RELATES_TO  BLOCKS  BLOCKED_BY  DUPLICATES
```

### Visibility
```
PUBLIC  PRIVATE
```
Default: `PRIVATE`

### Project Statuses
```
ACTIVE  ARCHIVED
```

### Release Statuses
```
PLANNED  CLOSED
```

### Page Statuses
```
DRAFT  PUBLISHED  ARCHIVED
```

### Roles
```
OWNER  DEVELOPER  VIEWER
```
Default: `DEVELOPER`

### Branch Types
```
feature  bugfix  hotfix  release
```

### SCM Providers
```
FAKE_GITLAB  GITHUB  BITBUCKET
```
Default: `FAKE_GITLAB`

---

## 8. Board/Backlog Logic

This is the most important design rule. **Task status and board placement are independent fields.**

A `Task` has:
- `status` — lifecycle state (`PLANNED`, `IN_PROGRESS`, etc.)
- `boardColumnId` — nullable FK to `board_columns.id`; `null` means not on any board

**Backlog** = tasks where `boardColumnId IS NULL` AND `status NOT IN ('DONE', 'ARCHIVED')`

Moving a task on the board (`POST /api/boards/{boardId}/move-task`) sets `boardColumnId` and `boardRank`. It does **not** automatically change `status`. Status is changed separately via `POST /api/tasks/{taskId}/status`.

Moving a task to the backlog (`POST /api/boards/{boardId}/remove-task`) sets `boardColumnId = null`. Status is unchanged.

---

## 9. Complete API Reference

All endpoints are under the same router with the `AuthMiddleware` applied globally.

### System

```
GET  /health
     → {"status":"ok","database":"ok"}

GET  /api/version
     → {"version":"0.1.0","name":"Octbase API"}

GET  /api/meta/enums
     → {"taskStatuses":[...],"taskPriorities":[...],"taskTypes":[...],"roles":[...],...}

GET  /openapi.yaml
     → serves api/openapi.yaml file
```

---

### Identity and Access

```
GET  /api/users/me
     Header: X-User-Id: <uuid>
     → User{id, email, displayName, createdAt, updatedAt}

GET  /api/projects/{projectId}/memberships
     → []Membership{id, projectId, userId, role, createdAt, updatedAt}

POST /api/projects/{projectId}/memberships
     Body: {userId: string, role?: string}
     Default role: DEVELOPER
     → 201 Membership

PATCH /api/projects/{projectId}/memberships/{userId}
     Body: {role: string}
     → {"status":"updated"}

DELETE /api/projects/{projectId}/memberships/{userId}
     → 204 No Content
```

---

### Projects

```
POST  /api/projects
      Body: {name: string, description?: string, visibility?: string}
      Validation: name must not be blank
      Default visibility: PRIVATE
      → 201 Project{id, name, slug, description, visibility, status, createdAt, updatedAt, version}

GET   /api/projects
      Params: page, size
      → []Project

GET   /api/projects/{projectId}
      → Project | 404 PROJECT_NOT_FOUND

PATCH /api/projects/{projectId}
      Body: {name?: string, description?: string, visibility?: string}
      → Project

POST  /api/projects/{projectId}/archive
      → Project (status: ARCHIVED)

DELETE /api/projects/{projectId}
      Cascades: all memberships, tasks (and sub-resources), boards, releases,
                pages, repository connections, activity entries.
      → 204 No Content | 404 PROJECT_NOT_FOUND
```

The `slug` field is auto-generated from `name` by lowercasing and replacing non-alphanumeric characters with hyphens (collapsing consecutive hyphens).

---

### Tasks

```
POST  /api/projects/{projectId}/tasks
      Body: {title: string, description?: string, taskType?: string,
             priority?: string, assigneeId?: string, releaseId?: string}
      Validation: title must not be blank
      Defaults: taskType=TASK, priority=MEDIUM, status=PLANNED, boardRank=1000
      Sets: reporterId = X-User-Id
      Writes activity: TASK_CREATED
      → 201 Task

GET   /api/projects/{projectId}/tasks
      Params: status, priority, assigneeId, page, size
      → []Task

GET   /api/tasks/{taskId}
      → Task{id, projectId, title, description, taskType, status, priority,
             assigneeId, reporterId, reviewerId, releaseId,
             boardColumnId, boardRank, createdAt, updatedAt, version}
      | 404 TASK_NOT_FOUND

PATCH /api/tasks/{taskId}
      Body: {title?: string, description?: string, taskType?: string, releaseId?: string}
      Blocked if status is DONE or ARCHIVED → 422 TASK_IMMUTABLE
      Writes activity: TASK_UPDATED
      → Task

POST  /api/tasks/{taskId}/assign
      Body: {assigneeId?: string, reviewerId?: string}
      → Task

POST  /api/tasks/{taskId}/status
      Body: {status: string}
      Blocked if status is DONE or ARCHIVED → 422 TASK_IMMUTABLE
      Writes activity: TASK_STATUS_CHANGED
      → Task

POST  /api/tasks/{taskId}/priority
      Body: {priority: string}
      → Task

POST  /api/tasks/{taskId}/copy
      Creates a new task with title "Copy of {original}", status=PLANNED,
      boardColumnId=null, boardRank=1000. Copies: description, taskType, priority.
      Sets reporterId = X-User-Id.
      → 201 Task

POST  /api/tasks/{taskId}/archive
      Sets status = ARCHIVED
      → Task

POST  /api/tasks/{taskId}/reopen
      Sets status = PLANNED
      → Task

DELETE /api/tasks/{taskId}
      Cascades: branch_references, page_task_references, task_relations,
                task_attachments, task_links, task_comments.
      → 204 No Content | 404 TASK_NOT_FOUND
```

---

### Task Comments

```
POST  /api/tasks/{taskId}/comments
      Body: {text: string}
      Sets: authorId = X-User-Id
      Writes activity: TASK_COMMENT_ADDED
      → 201 TaskComment{id, taskId, authorId, text, createdAt, updatedAt}

GET   /api/tasks/{taskId}/comments
      → []TaskComment

PATCH /api/tasks/{taskId}/comments/{commentId}
      Body: {text: string}
      Verifies comment belongs to taskId → 404 COMMENT_NOT_FOUND if not
      → TaskComment

DELETE /api/tasks/{taskId}/comments/{commentId}
      Verifies comment belongs to taskId → 404 COMMENT_NOT_FOUND if not
      → 204 No Content
```

---

### Task Links

```
POST   /api/tasks/{taskId}/links
       Body: {url: string, title?: string}
       → 201 TaskLink{id, taskId, url, title, createdAt}

GET    /api/tasks/{taskId}/links
       → []TaskLink

DELETE /api/tasks/{taskId}/links/{linkId}
       → 204 No Content
```

---

### Task Attachments

Attachments are metadata-only (no file upload).

```
POST   /api/tasks/{taskId}/attachments
       Body: {filename: string, contentType?: string, sizeBytes?: int, externalUrl?: string}
       → 201 TaskAttachment{id, taskId, filename, contentType, sizeBytes, externalUrl, createdAt}

GET    /api/tasks/{taskId}/attachments
       → []TaskAttachment

DELETE /api/tasks/{taskId}/attachments/{attachmentId}
       → 204 No Content
```

---

### Task Relations

```
POST   /api/tasks/{taskId}/relations
       Body: {targetTaskId: string, relationType?: string}
       Default relationType: RELATES_TO
       Domain rules enforced by Service.AddRelation():
         - A task cannot relate to itself → 422 TASK_SELF_RELATION
         - The same relation cannot be added twice → 422 TASK_RELATION_DUPLICATE
         - A BLOCKS relation that would create a cycle → 422 TASK_RELATION_CYCLE
       → 201 TaskRelation{id, sourceTaskId, targetTaskId, relationType, createdAt}

GET    /api/tasks/{taskId}/relations
       → []TaskRelation

DELETE /api/tasks/{taskId}/relations/{relationId}
       → 204 No Content
```

---

### Boards

```
POST  /api/projects/{projectId}/boards
      Body: {name: string, isDefault?: bool}
      → 201 Board{id, projectId, name, isDefault, createdAt, updatedAt}

GET   /api/projects/{projectId}/boards
      → []Board

GET   /api/projects/{projectId}/boards/default
      Includes columns array in response
      → Board{...columns: []BoardColumn}
      | 404 BOARD_NOT_FOUND

GET   /api/boards/{boardId}
      Includes columns array in response
      → Board | 404 BOARD_NOT_FOUND

PATCH /api/boards/{boardId}
      Body: {name?: string}
      → Board

DELETE /api/boards/{boardId}
      Sets board_column_id=NULL on all tasks in the board's columns,
      deletes columns, then deletes the board.
      → 204 No Content | 404 BOARD_NOT_FOUND
```

---

### Board Columns

```
POST   /api/boards/{boardId}/columns
       Body: {name: string, status?: string, position?: int}
       Default status: PLANNED
       → 201 BoardColumn{id, boardId, name, status, position, createdAt, updatedAt}

PATCH  /api/boards/{boardId}/columns/{columnId}
       Body: {name?: string, status?: string, position?: int}
       → BoardColumn

DELETE /api/boards/{boardId}/columns/{columnId}
       → 204 No Content
```

---

### Board Task Operations

```
POST /api/boards/{boardId}/move-task
     Body: {taskId: string, boardColumnId: string, boardRank: int}
     Sets task.boardColumnId and task.boardRank. Does NOT change task.status.
     Writes activity: TASK_MOVED
     → Task

POST /api/boards/{boardId}/remove-task
     Body: {taskId: string}
     Sets task.boardColumnId = null. Status unchanged. Task goes to backlog.
     → Task
```

---

### Backlog

```
GET /api/projects/{projectId}/backlog
    → []Task  (where boardColumnId IS NULL AND status NOT IN ('DONE','ARCHIVED'))
```

---

### Releases

```
POST  /api/projects/{projectId}/releases
      Body: {name: string, goal?: string, dueDate?: string}
      Default status: PLANNED
      → 201 Release{id, projectId, name, goal, dueDate, status, createdAt, updatedAt, version}

GET   /api/projects/{projectId}/releases
      → []Release

GET   /api/releases/{releaseId}
      → Release | 404 MILESTONE_NOT_FOUND

PATCH /api/releases/{releaseId}
      Body: {name?: string, goal?: string, dueDate?: string}
      → Release

POST  /api/releases/{releaseId}/close
      Domain rule: release can only be closed if no open tasks are assigned to it.
      "Open" = status not in DONE or ARCHIVED.
      Error: 422 MILESTONE_HAS_OPEN_TASKS
      Writes activity: MILESTONE_CLOSED
      → Release (status: CLOSED)

POST  /api/releases/{releaseId}/reopen
      Sets status = PLANNED
      → Release

DELETE /api/releases/{releaseId}
      Nullifies task.release_id for all assigned tasks, then deletes the release.
      → 204 No Content | 404 MILESTONE_NOT_FOUND
```

---

### Documentation Pages

Content is AsciiDoc stored as plain text. The API renders it to HTML on write using a built-in subset renderer (headings `=`/`==`/`===`, bullet lists `* `, bold `**text**`).

```
POST  /api/projects/{projectId}/pages
      Body: {title: string, content?: string, slug?: string, parentPageId?: string}
      Slug auto-generated from title if omitted.
      Slug must be unique within the project → 409 SLUG_CONFLICT
      Sets status: DRAFT, renders content to renderedHtml
      → 201 Page{id, projectId, parentPageId, title, slug, content, renderedHtml,
                  status, createdAt, updatedAt, version}

GET   /api/projects/{projectId}/pages
      → []Page (flat list; tree structure is built by the client from parentPageId)

GET   /api/projects/{projectId}/search/pages
      Params: q, page, size
      → []Page

GET   /api/pages/{pageId}
      → Page | 404 PAGE_NOT_FOUND

PATCH /api/pages/{pageId}
      Body: {title?: string, content?: string, slug?: string}
      Blocked if status is ARCHIVED → 422 PAGE_ARCHIVED
      Re-renders renderedHtml when content changes
      → Page

POST  /api/pages/{pageId}/render-preview
      Body: {content: string}
      Does NOT modify the page. Returns rendered HTML only.
      → {"html": "<div class=\"asciidoc-content\">...</div>"}

POST  /api/pages/{pageId}/publish
      Body: {message?: string}
      Sets status = PUBLISHED, re-renders content.
      Creates an immutable PageRevision.
      Writes activity: PAGE_PUBLISHED
      → Page

POST  /api/pages/{pageId}/archive
      Sets status = ARCHIVED
      → Page

DELETE /api/pages/{pageId}
      Cascades: page_revisions, page_task_references.
      → 204 No Content | 404 PAGE_NOT_FOUND

GET   /api/pages/{pageId}/revisions
      → []PageRevision{id, pageId, content, message, authorId, createdAt}

GET   /api/pages/{pageId}/references
      → []PageReference{id, pageId, taskId, createdAt}

POST  /api/pages/{pageId}/references/rebuild
      Scans page content for patterns matching task UUIDs and rebuilds
      the page_task_references table for this page.
      → []PageReference
```

---

### Task Categories

```
POST   /api/projects/{projectId}/task-categories
       Body: {name: string, description?: string, color?: string}
       Default color: gray
       → 201 TaskCategory{id, projectId, name, description, color, createdAt, updatedAt}

GET    /api/projects/{projectId}/task-categories
       → []TaskCategory

PATCH  /api/task-categories/{categoryId}
       Body: {name?: string, description?: string, color?: string}
       → TaskCategory

DELETE /api/task-categories/{categoryId}
       → 204 No Content
```

---

### Task Templates

```
POST   /api/projects/{projectId}/task-templates
       Body: {name: string, titleTemplate?: string, descriptionTemplate?: string,
              taskType?: string, priority?: string}
       Defaults: taskType=TASK, priority=MEDIUM
       → 201 TaskTemplate{id, projectId, name, titleTemplate, descriptionTemplate,
                          taskType, priority, createdAt, updatedAt}

GET    /api/projects/{projectId}/task-templates
       → []TaskTemplate

GET    /api/task-templates/{templateId}
       → TaskTemplate | 404 TEMPLATE_NOT_FOUND

PATCH  /api/task-templates/{templateId}
       Body: {name?, titleTemplate?, descriptionTemplate?, taskType?, priority?}
       → TaskTemplate

DELETE /api/task-templates/{templateId}
       → 204 No Content

POST   /api/task-templates/{templateId}/instantiate
       Body: {title?: string, description?: string}
       Creates a new task from the template. Title defaults to template.titleTemplate
       (or template.name if titleTemplate is empty). Sets reporterId = X-User-Id.
       Writes activity: TASK_CREATED
       → 201 Task
```

---

### SCM Integration

```
POST  /api/projects/{projectId}/repository-connections
      Body: {displayName: string, repositoryUrl: string,
             provider?: string, defaultBranch?: string}
      Defaults: provider=FAKE_GITLAB, defaultBranch=main
      → 201 RepositoryConnection{id, projectId, provider, displayName,
                                  repositoryUrl, defaultBranch, createdAt, updatedAt}

GET   /api/projects/{projectId}/repository-connections
      → []RepositoryConnection

PATCH /api/repository-connections/{repositoryId}
      Body: {displayName?, repositoryUrl?, defaultBranch?}
      → RepositoryConnection

DELETE /api/repository-connections/{repositoryId}
      Cascades: branch_references for this repository.
      → 204 No Content | 404 REPO_NOT_FOUND

POST  /api/tasks/{taskId}/branches
      Body: {repositoryId: string, branchName: string, branchType?: string}
      Default branchType: feature
      Writes activity: BRANCH_CREATED
      → 201 BranchReference{id, taskId, repositoryId, branchName, branchType, createdAt}

GET   /api/tasks/{taskId}/branches
      → []BranchReference

DELETE /api/tasks/{taskId}/branches/{branchId}
      → 204 No Content | 404 BRANCH_NOT_FOUND
```

---

### Activity

```
GET  /api/projects/{projectId}/activity
     Params: page, size
     → []ActivityEntry{id, projectId, taskId, actorUserId, type, message,
                        payloadJson, createdAt}

GET  /api/tasks/{taskId}/activity
     → []ActivityEntry
```

Activity is written by handlers using the `ActivityWriter` interface (`Write(projectID, taskID, actorID, actType, message)`). `taskID` may be an empty string for project-level events.

Activity types generated:
```
TASK_CREATED        TASK_UPDATED        TASK_STATUS_CHANGED
TASK_COMMENT_ADDED  TASK_MOVED          MILESTONE_CLOSED
PAGE_PUBLISHED      BRANCH_CREATED
```

---

## 10. Database Schema

PostgreSQL, applied as a single migration at startup (`migrations/001_initial.sql`). All SQL uses `$N` numbered placeholders. Case-insensitive text search uses `ILIKE`.

```sql
users               (id, email, display_name, created_at, updated_at)
projects            (id, name, slug, description, visibility, status, created_at, updated_at, version)
memberships         (id, project_id, user_id, role, created_at, updated_at)
                    UNIQUE(project_id, user_id)
task_categories     (id, project_id, name, description, color, created_at, updated_at)
task_templates      (id, project_id, name, title_template, description_template, task_type, priority, created_at, updated_at)
releases          (id, project_id, name, goal, due_date, status, created_at, updated_at, version)
boards              (id, project_id, name, is_default INTEGER, created_at, updated_at)
board_columns       (id, board_id, name, status, position INTEGER, created_at, updated_at)
tasks               (id, project_id, title, description, task_type, status, priority,
                     assignee_id, reporter_id, reviewer_id, release_id,
                     board_column_id, board_rank INTEGER, created_at, updated_at, version)
task_comments       (id, task_id, author_id, text, created_at, updated_at)
task_links          (id, task_id, url, title, created_at)
task_attachments    (id, task_id, filename, content_type, size_bytes INTEGER, external_url, created_at)
task_relations      (id, source_task_id, target_task_id, relation_type, created_at)
pages               (id, project_id, parent_page_id, title, slug, content, rendered_html,
                     status, created_at, updated_at, version)
page_revisions      (id, page_id, content, message, author_id, created_at)
page_task_references (id, page_id, task_id, created_at) UNIQUE(page_id, task_id)
repository_connections (id, project_id, provider, display_name, repository_url, default_branch, created_at, updated_at)
branch_references   (id, task_id, repository_id, branch_name, branch_type, created_at)
activity_entries    (id, project_id, task_id, actor_user_id, type, message, payload_json, created_at)
```

All IDs are `TEXT` UUIDs. Timestamps are `TEXT` in RFC3339 format (`"2024-01-01T00:00:00Z"`). Booleans (`is_default`) are stored as `INTEGER` (0/1) for compatibility.

---

## 11. Seed Data

Activated via `OCTBASE_DEMO_MODE=true`. Runs on every startup as a **full upsert** — every row is inserted or reset to its canonical state using `ON CONFLICT (id) DO UPDATE SET ...`. This ensures the demo environment is predictable across restarts and test runs. All IDs are deterministic:

| Resource | ID |
|---|---|
| Demo User | `00000000-0000-0000-0000-000000000001` |
| Demo Project | `00000000-0000-0000-0000-000000000101` |
| Task 1 (Implement user authentication) | `00000000-0000-0000-0000-000000000201` |
| Task 2 (Write API documentation) | `00000000-0000-0000-0000-000000000202` |
| Board (Main Board) | `00000000-0000-0000-0000-000000000301` |
| Column Planned | `00000000-0000-0000-0000-000000000311` |
| Column In Progress | `00000000-0000-0000-0000-000000000312` |
| Column Review | `00000000-0000-0000-0000-000000000313` |
| Column Done | `00000000-0000-0000-0000-000000000314` |
| Release (v1.0 Launch) | `00000000-0000-0000-0000-000000000401` |
| Page (Getting Started) | `00000000-0000-0000-0000-000000000501` |
| Repository Connection | `00000000-0000-0000-0000-000000000601` |

Seeded state:
- Demo user: `demo@octbase.dev` / `Demo User` / role: `OWNER`
- Project: `Demo Project` (PUBLIC, ACTIVE)
- Task 1: status `IN_PROGRESS`, priority `HIGH`, assigned to demo user, linked to release, in `ColumnInProgressID`, has a comment, a link (https://jwt.io), an attachment (`auth-diagram.png`), a relation (BLOCKS Task 2), a branch reference (`feature/user-auth`)
- Task 2: status `PLANNED`, priority `MEDIUM`, in `ColumnPlannedID`
- Release: `v1.0 Launch`, goal `Ship first version`, dueDate `2024-06-01`, status `PLANNED`
- Page: `Getting Started`, status `PUBLISHED`, with one revision
- Repository: `FAKE_GITLAB` provider, URL `https://gitlab.example.com/demo/octbase`
- One seeded activity entry: `TASK_CREATED` for Task 1

---

## 12. Shared Utilities (`internal/shared/`)

```go
// DB
shared.OpenDB(dsn string) (*sql.DB, error)      // opens PostgreSQL connection via lib/pq
shared.RunMigrations(db, sql string) error
shared.ReadMigrationFile(path string) (string, error)
shared.NewUUID() string                         // generates UUID v4 using crypto/rand
shared.Now() string                             // RFC3339 UTC timestamp
shared.IsValidUUID(s string) bool

// HTTP
shared.WriteJSON(w, status, v)
shared.WriteError(w, status, code, message)
shared.DecodeJSON(r, v) error
shared.GetUserID(r) string                      // reads UserIDKey from context
shared.ParsePagination(r) PaginationParams{Page, Size}
shared.AuthMiddleware(next) http.Handler
shared.CORSHeaders(w)
```

---

## 13. Domain Business Rules

These rules are enforced in code (not just documented):

| Rule | Location | Error code |
|---|---|---|
| Task title must not be blank | `CreateTask` handler | `TASK_TITLE_REQUIRED` |
| DONE/ARCHIVED tasks cannot be modified | `UpdateTask`, `ChangeStatus` | `TASK_IMMUTABLE` |
| Project name must not be blank | `CreateProject` handler | `VALIDATION_ERROR` |
| Page slug must be unique within project | `CreatePage`, `UpdatePage` | `SLUG_CONFLICT` |
| Archived page cannot be modified | `UpdatePage` handler | `PAGE_ARCHIVED` |
| Release cannot close with open tasks | `Service.CloseRelease()` | `MILESTONE_HAS_OPEN_TASKS` |
| Task cannot relate to itself | `Service.AddRelation()` | `TASK_SELF_RELATION` |

`IsImmutable(status string) bool` returns true for `DONE` and `ARCHIVED`.

---

## 14. Container and Runtime

### Containerfiles

There are two Containerfiles. There is **no root-level `Containerfile`** — the frontend is served independently by nginx.

**`octbase-api/Containerfile`** — API-only image. Multi-stage build. Uses `docker.io/library/golang:1.25-alpine` as builder and `gcr.io/distroless/static-debian12` as the minimal runtime image:

```dockerfile
FROM docker.io/library/golang:1.25-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /octbase-api ./cmd/octbase-api

FROM gcr.io/distroless/static-debian12
COPY --from=builder /octbase-api /octbase-api
COPY --from=builder /src/migrations /migrations
COPY --from=builder /src/api /api
COPY --from=builder /src/web /web
EXPOSE 8000
ENTRYPOINT ["/octbase-api"]
```

**`octbase-frontend/Containerfile`** — nginx serving the SPA. Nginx listens on port 8080 (not 80) because rootless Podman cannot bind privileged ports. The `docker.io/library/nginx:1-alpine` image is used.

```dockerfile
FROM docker.io/library/nginx:1-alpine
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY app.js app.css index.html favicon.ico logo.png /usr/share/nginx/html/
EXPOSE 8080
```

Images use public Docker Hub and Google Container Registry — no private registry login required.

### `podman-compose.yml` (repo root — full stack)

```yaml
services:
  postgres:
    image: docker.io/library/postgres:16
    environment:
      POSTGRES_DB: octbase
      POSTGRES_USER: octbase
      POSTGRES_PASSWORD: octbase
    volumes:
      - octbase_data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U octbase"]
      interval: 5s
      timeout: 5s
      retries: 5

  octbase-api:
    image: localhost/octbase-api:latest
    build:
      context: ./octbase-api
      dockerfile: Containerfile
    ports:
      - "8000:8000"
    environment:
      OCTBASE_DATABASE_URL: "postgres://octbase:octbase@postgres:5432/octbase?sslmode=disable"
      OCTBASE_DEMO_MODE: "true"
    depends_on:
      postgres:
        condition: service_healthy

  octbase-frontend:
    image: localhost/octbase-frontend:latest
    build:
      context: ./octbase-frontend
      dockerfile: Containerfile
    ports:
      - "8080:8080"
    depends_on:
      - octbase-api

volumes:
  octbase_data:
```

`podman-compose` v1 does not honour `condition: service_healthy`, so the API includes a startup ping-retry loop (up to 10 attempts, 1–10 s linear backoff) before running migrations.

The SPA is served by nginx at **http://localhost:8080/**. The API is at **http://localhost:8000/**. The API does **not** serve the SPA in the POC — only its own docs (`/docs`), OpenAPI spec (`/openapi.yaml`), and REST endpoints. The MVP (`8_octbase-create-mvp.md`) adds the full SPA to the API image, eliminating the nginx container.

### Environment variables

| Variable | Default | Purpose |
|---|---|---|
| `PORT` | `8000` | HTTP listen port |
| `OCTBASE_DATABASE_URL` | `postgres://octbase:octbase@localhost:5432/octbase?sslmode=disable` | PostgreSQL connection string |
| `OCTBASE_DEMO_MODE` | (unset) | Set to `"true"` to run seed on startup |

---

## 15. Implementation Gotchas

1. **Board columns carry the canonical status mapping.** `board_columns.status` tells the board which task status that column represents. Dragging a card does NOT auto-update `tasks.status` — the frontend must call `POST /api/tasks/{id}/status` separately if it wants the status to change.

2. **Backlog query uses two conditions.** Both must hold: `board_column_id IS NULL` AND `status NOT IN ('DONE','ARCHIVED')`. A DONE task with `boardColumnId = null` is not on the backlog.

3. **Page content field is named `content`, not `body`.** The rendered output is `renderedHtml`.

4. **`GET /api/projects/{projectId}/boards/default` vs `/api/boards/{boardId}`.** Both return the full board including `columns` array. All other board list endpoints omit columns.

5. **The default board for a project may not exist.** `GET /api/projects/{projectId}/boards/default` returns `404 BOARD_NOT_FOUND` if no board has `is_default = 1`. The client must create the board with `POST /api/projects/{projectId}/boards` and then add columns individually with `POST /api/boards/{boardId}/columns`.

6. **Pagination is zero-indexed pages.** `?page=0&size=20` returns the first 20 items.

7. **Timestamps are stored and returned as plain RFC3339 strings.** The `TEXT` column type is used for timestamps rather than PostgreSQL's native `TIMESTAMP` — this keeps the code simpler and preserves the RFC3339 lexicographic sort order. The `shared.Now()` helper returns `time.Now().UTC().Format(time.RFC3339)`.

8. **The membership ID in seed data reuses the user UUID.** This is intentional (idempotent seed using `ON CONFLICT (id) DO UPDATE`). All real memberships created via the API get proper `shared.NewUUID()` IDs.

9. **`PATCH` endpoints are partial updates.** Fields absent from the body are left unchanged. Implemented via pointer fields (`*string`, `*int`) in request structs; `nil` means "not provided".

10. **The AsciiDoc renderer is minimal by design.** It handles `=`/`==`/`===` headings, `* ` bullet lists, `**bold**` inline, and plain paragraphs. It does not handle tables, code blocks, admonitions, or links.

11. **All transaction blocks use `defer tx.Rollback()`**. The idiomatic pattern is:
    ```go
    tx, err := r.db.Begin()
    if err != nil { return err }
    defer tx.Rollback() // safe after Commit — returns ErrTxDone
    // ... exec statements ...
    return tx.Commit()
    ```
    Never call `tx.Rollback()` manually on error paths; the deferred call covers all paths including panics.

12. **Cascading deletes are handled in the repository layer via transactions**, not via `ON DELETE CASCADE` FK constraints. Each `Delete` method executes ordered `DELETE`/`UPDATE` statements within a single transaction. Child rows are deleted before parent rows to satisfy FK constraints. See `ProjectRepo.Delete`, `TaskRepo.Delete`, `BoardRepo.Delete`, `ReleaseRepo.Delete`, `PageRepo.Delete`, `RepositoryConnectionRepo.Delete`.

13. **Comment update/delete handlers verify ownership.** `UpdateComment` and `DeleteComment` check that `c.TaskID == taskID` (from the URL) before acting, returning 404 if the comment doesn't belong to the specified task.
