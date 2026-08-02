# Octbase — Finalize POC

**Purpose:** Bring the Octbase proof-of-concept to a complete, trustworthy state. The POC stays a POC — no real authentication UI, no notifications, no file uploads — but every domain rule must be enforced, every endpoint must be safe, every test must be meaningful, and the code must be structured so that any senior engineer can extend it with confidence.

**Read first:** `prompts/9_TODO_POC.md`. This prompt translates every item there into concrete engineering instructions. Work through the sections in order — later sections depend on earlier ones.

**Do not:** add features beyond what is described here. Fix what is broken; do not build what is missing from the MVP scope.

---

## 0. Baseline and Working Assumptions

- The existing system is described in prompts `1_octbase-api.md` through `6_octbase-crud-functions.md`.
- All Go code lives in `octbase-api/`, all frontend code in `octbase-frontend/`.
- The four-file layout per bounded context (`domain.go`, `repo.go`, `service.go`, `handler.go`) must be preserved.
- Tests use a live PostgreSQL instance via `testutil.NewTestDB()`, which creates an isolated schema per test run. Never add mocks.
- `go-best-practices.md` is authoritative on error handling, naming, and structure.

---

## 1. Domain Integrity & Business Rules

### 1.1 Membership guard on every write

Every endpoint that mutates a project resource (task, board, page, release, category, template, repository connection, branch) must validate that the acting user (`X-User-Id`) is a member of the containing project before executing the mutation.

Add a shared helper in `internal/shared/auth.go`:

```go
// RequireProjectMember checks that userID is a member of projectID.
// Returns the member's role on success, a DomainError on failure.
// Call from handlers after resolving the project and user IDs.
func RequireProjectMember(db *sql.DB, projectID, userID string) (string, error)
```

This function must execute:
```sql
SELECT role FROM memberships WHERE project_id = $1 AND user_id = $2
```
Return `shared.ErrNotMember` (new sentinel) if no row; return the role string on success.

Call `RequireProjectMember` at the start of every write handler that receives a `projectId` path param. Place the call after UUID validation and before any business logic.

### 1.2 OWNER-only operations

Define a helper:
```go
func RequireOwner(role string) error // returns DomainError FORBIDDEN if role != "OWNER"
```

Apply `RequireOwner` to:
- `DELETE /api/projects/{projectId}`
- `POST /api/projects/{projectId}/archive`
- `POST /api/projects/{projectId}/memberships`
- `PATCH /api/projects/{projectId}/memberships/{userId}`
- `DELETE /api/projects/{projectId}/memberships/{userId}`
- `POST /api/projects/{projectId}/repository-connections`
- `DELETE /api/repository-connections/{repositoryId}`

### 1.3 VIEWER read-only enforcement

Add a helper:
```go
func RequireWriter(role string) error // returns DomainError FORBIDDEN if role == "VIEWER"
```

Apply `RequireWriter` to every remaining write handler (tasks, comments, boards, releases, pages, branches).

### 1.4 User existence check on X-User-Id

In `AuthMiddleware` (or a new `IdentityMiddleware`), after validating UUID format, query:
```sql
SELECT id FROM users WHERE id = $1
```
Return `401 UNAUTHORIZED` with code `USER_NOT_FOUND` if no row. This replaces the current "any UUID" behaviour.

### 1.5 Task immutability — extend to DONE

In `workmanagement/domain.go`, `IsImmutable` already returns true for ARCHIVED. Extend it:
```go
func IsImmutable(status string) bool {
    return status == StatusDone || status == StatusArchived
}
```
Verify every mutating task handler calls `IsImmutable` before proceeding.

### 1.6 Release closure guard — cover all non-terminal statuses

In `workmanagement/service.go`, `CloseRelease` currently only blocks on `IN_PROGRESS` tasks. Change the guard to:
```go
// Block closure if any task assigned to this release is not DONE or ARCHIVED.
```
Query:
```sql
SELECT COUNT(*) FROM tasks
WHERE release_id = $1 AND status NOT IN ('DONE', 'ARCHIVED')
```

### 1.7 Board column status uniqueness

Add a unique constraint in a new migration `002_constraints.sql`:
```sql
ALTER TABLE board_columns ADD CONSTRAINT board_columns_board_status_unique
    UNIQUE (board_id, status);
```

In `workmanagement/service.go`, before inserting a board column, check for an existing column with the same status in the same board and return a `DomainError` with code `COLUMN_STATUS_DUPLICATE` if found. Do not rely on the DB constraint alone — return a meaningful domain error, not a raw pq error.

### 1.8 Task relation symmetry

In `workmanagement/service.go`, `AddRelation`:
- When inserting a `BLOCKS` relation (A → B), also insert a `BLOCKED_BY` relation (B → A) in the same transaction.
- When deleting a `BLOCKS` relation, also delete the corresponding `BLOCKED_BY` relation.
- Apply the same symmetry for `DUPLICATES` → `DUPLICATES` (it is symmetric by definition; insert both directions).
- `RELATES_TO` is already symmetric; handle it the same way.

### 1.9 Prevent self-relation

In `AddRelation`, before any DB call:
```go
if rel.SourceID == rel.TargetID {
    return &DomainError{Code: "SELF_RELATION", Message: "a task cannot relate to itself"}
}
```

### 1.10 Page slug conflict returns 409

In `docs/service.go` or `docs/repo.go`, catch the pq unique constraint violation on `pages(project_id, slug)` and return a `DomainError{Code: "SLUG_CONFLICT", Message: "a page with this slug already exists in the project"}` instead of propagating the raw driver error.

---

## 2. Data Layer

### 2.1 Transactions for multi-step mutations

Wrap these operations in a database transaction (pass `*sql.Tx` to the relevant repo methods, or use a `WithTx(db, fn)` helper):

- `workmanagement`: `MoveTask` (updates `board_column_id` + `board_rank` + logs activity)
- `workmanagement`: `InstantiateTemplate` (creates task + logs activity)
- `workmanagement`: `CopyTask` (creates task + copies sub-resources + logs activity)
- `docs`: `PublishPage` (creates revision + rebuilds references + logs activity)
- `docs`: `RebuildReferences` (deletes old refs + inserts new refs)
- `workmanagement`: `AddRelation` (inserts both directions of a relation, see §1.8)

Add a helper in `internal/shared/db.go`:
```go
func WithTx(db *sql.DB, fn func(*sql.Tx) error) error {
    tx, err := db.Begin()
    if err != nil {
        return fmt.Errorf("begin tx: %w", err)
    }
    if err := fn(tx); err != nil {
        _ = tx.Rollback()
        return err
    }
    return tx.Commit()
}
```

### 2.2 Database indexes — migration 002

Add to `002_constraints.sql`:
```sql
CREATE INDEX IF NOT EXISTS idx_tasks_project_id     ON tasks(project_id);
CREATE INDEX IF NOT EXISTS idx_tasks_assignee_id    ON tasks(assignee_id);
CREATE INDEX IF NOT EXISTS idx_tasks_release_id   ON tasks(release_id);
CREATE INDEX IF NOT EXISTS idx_tasks_status         ON tasks(status);
CREATE INDEX IF NOT EXISTS idx_tasks_board_column   ON tasks(board_column_id);
CREATE INDEX IF NOT EXISTS idx_board_columns_board  ON board_columns(board_id);
CREATE INDEX IF NOT EXISTS idx_activity_project     ON activity_entries(project_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_activity_task        ON activity_entries(task_id);
CREATE INDEX IF NOT EXISTS idx_page_refs_page       ON page_task_references(page_id);
CREATE INDEX IF NOT EXISTS idx_page_refs_task       ON page_task_references(task_id);
CREATE INDEX IF NOT EXISTS idx_branch_refs_task     ON branch_references(task_id);
CREATE INDEX IF NOT EXISTS idx_memberships_user     ON memberships(user_id);
```

### 2.3 TIMESTAMPTZ migration

Add to `002_constraints.sql` — alter all timestamp columns from `TEXT` to `TIMESTAMPTZ`:
```sql
ALTER TABLE projects       ALTER COLUMN created_at TYPE TIMESTAMPTZ USING created_at::TIMESTAMPTZ,
                           ALTER COLUMN updated_at TYPE TIMESTAMPTZ USING updated_at::TIMESTAMPTZ;
-- repeat for: tasks, task_comments, releases, boards, pages, page_revisions,
--             repository_connections, branch_references, activity_entries, users
```

In all repo files, replace `time.Now().UTC().Format(time.RFC3339)` with `time.Now().UTC()` and update the scan targets from `string` to `time.Time`. Update domain structs accordingly (`CreatedAt time.Time`). In JSON output, `time.Time` marshals to RFC3339 automatically.

### 2.4 Parameterized query audit

Grep for `fmt.Sprintf` in all `repo.go` files. Each occurrence must be reviewed. Any `fmt.Sprintf` that includes a variable in a SQL string must be replaced with a `$N` placeholder. Column names used in dynamic ORDER BY are acceptable only if they are selected from a fixed allow-list, not passed from user input.

### 2.5 Pagination on all list endpoints

Add `?page=1&size=50` support (using the existing `shared.ParsePagination`) to:
- `GET /api/projects/{projectId}/releases`
- `GET /api/projects/{projectId}/pages`
- `GET /api/projects/{projectId}/task-categories`
- `GET /api/projects/{projectId}/task-templates`
- `GET /api/boards/{boardId}/columns` (probably not needed given typical column counts, but add for consistency)
- `GET /api/projects/{projectId}/repository-connections`

---

## 3. Security

### 3.1 Restrict CORS

Replace the wildcard in the CORS middleware with an env-var-driven origin list:
```go
// In shared/cors.go or main.go
allowedOrigin := os.Getenv("OCTBASE_CORS_ORIGIN")
if allowedOrigin == "" {
    allowedOrigin = "http://localhost:8080"
}
```
The CORS middleware must reject preflight requests from unlisted origins with `403`.

Update `podman-compose.yml` to pass `OCTBASE_CORS_ORIGIN=http://localhost:8080` (the nginx frontend container's origin).

### 3.2 Input length limits

Add a `ValidateTaskInput` function in `workmanagement/domain.go` that returns a `DomainError` for:
- `Title` empty or > 255 characters → `TITLE_INVALID`
- `Description` > 50 000 characters → `DESCRIPTION_TOO_LONG`
- `Comment.Text` empty or > 10 000 characters → `COMMENT_INVALID`

Add `ValidatePageInput` in `docs/domain.go`:
- `Slug` not matching `^[a-z0-9][a-z0-9-]{0,98}[a-z0-9]$` or empty → `SLUG_INVALID`
- `Title` empty or > 255 characters → `TITLE_INVALID`
- `Content` > 500 000 characters → `CONTENT_TOO_LONG`

Add a search query length guard in all search handlers:
```go
q := r.URL.Query().Get("q")
if len(q) > 500 {
    shared.WriteError(w, http.StatusBadRequest, "QUERY_TOO_LONG", "search query must be ≤ 500 characters")
    return
}
```

### 3.3 Content-Type enforcement on write endpoints

Add to `shared/middleware.go`:
```go
func RequireJSON(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.Method == http.MethodPost || r.Method == http.MethodPatch || r.Method == http.MethodPut {
            ct := r.Header.Get("Content-Type")
            if !strings.HasPrefix(ct, "application/json") {
                shared.WriteError(w, http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE",
                    "Content-Type must be application/json")
                return
            }
        }
        next.ServeHTTP(w, r)
    })
}
```
Register `RequireJSON` as a global middleware in `main.go`.

### 3.4 Secrets out of the repository

Add a `.env.example` at the repository root listing all required env vars with placeholder values:
```
OCTBASE_DATABASE_URL=postgres://octbase:octbase@localhost:5432/octbase?sslmode=disable
OCTBASE_CORS_ORIGIN=http://localhost:8080
OCTBASE_DEMO_MODE=false
```
Add `.env` to `.gitignore`. Audit the repository for any hardcoded credentials and remove them.

### 3.5 AsciiDoc renderer output safety

In `docs/asciidoc.go` (or wherever the renderer lives), ensure that any raw HTML passthrough blocks (`++++`) are stripped or escaped, not rendered. If the renderer does not already do this, add a post-processing step that strips `<script`, `<iframe`, and `javascript:` occurrences from rendered HTML before returning it to the client.

---

## 4. API Completeness

### 4.1 User registration endpoint

Add to `identityaccess/handler.go`:
```
POST /api/users
Body: { name: string, email: string }
Response: User (201 Created)
Errors: 409 EMAIL_CONFLICT if email already exists
```

The endpoint does not require `X-User-Id` (it is used to create users). Apply only `RequireJSON`.

### 4.2 Project members list with user details

Add to `identityaccess/handler.go`:
```
GET /api/projects/{projectId}/members
Response: [{ id, name, email, role, joinedAt }]
```
This joins `memberships` with `users`. The frontend uses this to populate assignee dropdowns.

### 4.3 Task list — add sorting

In `workmanagement/repo.go`, extend `ListTasks` to accept `sortBy` and `order` params. Valid `sortBy` values: `created_at` (default), `updated_at`, `priority`, `title`. Valid `order` values: `asc`, `desc`. Use a fixed allow-list map to build the ORDER BY clause — never interpolate the raw query parameter.

```
GET /api/projects/{projectId}/tasks?sortBy=priority&order=desc
```

### 4.4 Standardize error envelope

Every non-2xx response body across all handlers must be:
```json
{ "code": "SCREAMING_SNAKE_CASE", "message": "human readable sentence" }
```

Grep for `http.Error(`, `json.NewEncoder(w).Encode(map[string]string{"error":`, and any other ad-hoc error serialization. Replace all with `shared.WriteError(w, status, code, message)` or `shared.WriteServerError(w, r, err)`.

### 4.5 Membership guard on task read

`GET /api/tasks/{taskId}` currently returns data to any caller. Add a project membership check: resolve the task's `project_id`, then call `RequireProjectMember`. If `X-User-Id` is absent, return `401`.

### 4.6 OpenAPI spec sync

After completing the above changes, update `api/openapi.yaml` to reflect:
- `POST /api/users` (new)
- `GET /api/projects/{projectId}/members` (new, replaces memberships endpoint in the spec if already there)
- `sortBy` and `order` query params on task list
- `page` and `size` query params on all newly paginated list endpoints
- Updated error schemas (all using `{ code, message }`)

---

## 5. Testing

### 5.1 Service-layer tests for all new guards

In `workmanagement/service_test.go`, add test cases for:
- `RequireProjectMember` returns error when user is not a member
- `RequireOwner` returns `FORBIDDEN` for DEVELOPER and VIEWER roles
- `RequireWriter` returns `FORBIDDEN` for VIEWER role
- `IsImmutable` returns true for both `DONE` and `ARCHIVED`
- `CloseRelease` fails when any task is `IN_REVIEW` or `IN_PROGRESS`
- Board column status uniqueness — second insert of same status returns `COLUMN_STATUS_DUPLICATE`
- `AddRelation` inserts both sides of a `BLOCKS` relation
- `AddRelation` inserts both sides of a `DUPLICATES` relation
- `AddRelation` returns `SELF_RELATION` when source == target
- Page slug conflict returns `SLUG_CONFLICT`

### 5.2 Transaction rollback tests

In each relevant service test, simulate a DB error mid-transaction by using a test helper that wraps a transaction and forces a rollback after the first insert. Assert that no partial state is written. (Use `testutil.NewTestDB()` + a `sql.Tx` that is rolled back in `t.Cleanup`.)

### 5.3 Handler input validation tests

For each write endpoint, add a test table covering:
- Missing required field → `400` or `422`
- Field exceeding max length → `422` with the appropriate domain error code
- Wrong `Content-Type` → `415`
- Missing `X-User-Id` → `401`
- Invalid UUID in `X-User-Id` → `401`
- Valid UUID for a non-existent user → `401` (after §1.4)

### 5.4 Pagination tests

For each newly paginated list endpoint, add tests:
- `?page=1&size=5` returns at most 5 items
- `?page=2&size=5` returns the correct offset
- `?size=0` or `?size=1000` is clamped to sane defaults (min 1, max 200)
- Response includes `total` count alongside the items

### 5.5 Relation symmetry tests

In `workmanagement/service_test.go`:
- Create tasks A and B; add `BLOCKS` relation A→B; assert `GetRelations(B)` contains `BLOCKED_BY` pointing to A.
- Delete the `BLOCKS` relation; assert the `BLOCKED_BY` relation is also gone.

### 5.6 Frontend Playwright tests — user creation and membership

In `octbase-frontend/tests/test_projects.py`:
- Create a new user via `POST /api/users`.
- Add the user to a project as DEVELOPER.
- Verify the user appears in the assignee dropdown when creating a task.
- Log in as the user (set `X-User-Id` in the UI) and verify they cannot access the "Manage Members" action.

### 5.7 Frontend Playwright test — relation symmetry

In `octbase-frontend/tests/test_task_panel.py`:
- Create tasks A and B.
- Open A's panel; add a `BLOCKS` relation to B.
- Open B's panel; verify `BLOCKED_BY` relation to A is shown.

### 5.8 CI smoke script

Keep the curl smoke coverage in a single executable file at `draft/Octbase_Go_API_Curl_Tests.sh`. It must:
1. Run against a disposable API instance
2. Wait for the API health check to return 200
3. Run one request against every route group (projects, tasks, boards, releases, pages, members, activity, search)
4. Assert each returns the expected HTTP status
5. Exit non-zero on any failure

---

## 6. Clean Architecture / DDD

### 6.1 No raw SQL in handlers

Grep all `handler.go` files for `db.Query`, `db.QueryRow`, `db.Exec`. Every occurrence must be moved to the corresponding `repo.go`. Handlers interact only with repo methods and service methods.

### 6.2 Domain event pattern for activity logging

Define in `internal/shared/events.go`:
```go
type DomainEvent struct {
    Kind      string    // "task.created", "task.status_changed", etc.
    ProjectID string
    TaskID    string    // empty if not task-scoped
    ActorID   string
    Payload   map[string]string
}

type EventBus struct {
    ch chan DomainEvent
}

func NewEventBus() *EventBus
func (b *EventBus) Publish(e DomainEvent)
func (b *EventBus) Subscribe(handler func(DomainEvent))
```

Wire it in `main.go`. The activity package subscribes and writes `activity_entries`. The `workmanagement` and `docs` services publish events instead of calling `activity.Log` directly. Remove the direct import of the `activity` package from `workmanagement` and `docs`.

### 6.3 Typed value objects

In `workmanagement/domain.go`, replace bare string constants with typed aliases:
```go
type TaskStatus  string
type TaskPriority string
type TaskType    string
type MemberRole  string

const (
    StatusPlanned    TaskStatus = "PLANNED"
    StatusInProgress TaskStatus = "IN_PROGRESS"
    // ...
)

func (s TaskStatus) IsValid() bool {
    switch s {
    case StatusPlanned, StatusInProgress, StatusInReview, StatusDone, StatusArchived:
        return true
    }
    return false
}
```

Update all struct fields and function signatures accordingly. Replace all string comparisons (`status == "DONE"`) with typed comparisons (`status == StatusDone`).

### 6.4 Cross-context access via service port

In `scmintegration/handler.go`, any lookup of a task by ID must call a method on a `workmanagement.TaskReader` interface (defined in `workmanagement/domain.go`), not query the `tasks` table directly. Inject the interface into the SCM handler via constructor.

```go
// In workmanagement/domain.go
type TaskReader interface {
    GetTask(id string) (*Task, error)
}
```

### 6.5 Seed binary

> **Note:** The separate `cmd/octbase-seed` approach was not adopted. Seed logic remained in `internal/seed/seed.go` and is invoked from the main binary when `OCTBASE_DEMO_MODE=true`. The `8_octbase-create-mvp.md` prompt supersedes this section.

The seed runs as part of the main binary on startup when `OCTBASE_DEMO_MODE=true` is set. There is no separate seed container or seed binary.

---

## 7. Code Quality

### 7.1 Linting

Add `octbase-api/.golangci.yml`:
```yaml
linters:
  enable:
    - errcheck
    - staticcheck
    - govet
    - unused
    - gosec
    - gofmt
    - misspell
```

Run `golangci-lint run ./...` and fix all reported issues before considering this step done.

### 7.2 Frontend: extract `api.js`

Create `octbase-frontend/api.js`:
```js
const BASE_URL = window.location.protocol === 'file:'
    ? 'http://127.0.0.1:8000'
    : window.location.origin;

let currentUserId = localStorage.getItem('octbase_user_id') || '';

export function setUserId(id) { currentUserId = id; localStorage.setItem('octbase_user_id', id); }
export function getUserId() { return currentUserId; }

export async function request(method, path, body) {
    const headers = { 'Content-Type': 'application/json' };
    if (currentUserId) headers['X-User-Id'] = currentUserId;
    const res = await fetch(`${BASE_URL}${path}`, {
        method,
        headers,
        body: body !== undefined ? JSON.stringify(body) : undefined,
    });
    if (!res.ok) {
        const err = await res.json().catch(() => ({ code: 'UNKNOWN', message: res.statusText }));
        throw Object.assign(new Error(err.message), { code: err.code, status: res.status });
    }
    if (res.status === 204) return null;
    return res.json();
}

export const api = {
    get:    (path)        => request('GET',    path),
    post:   (path, body)  => request('POST',   path, body),
    patch:  (path, body)  => request('PATCH',  path, body),
    del:    (path)        => request('DELETE', path),
};
```

Replace every `fetch(` call in `app.js` with `api.get / api.post / api.patch / api.del`. Remove the inline BASE_URL and header logic.

### 7.3 Frontend: split into view modules

Introduce ES module imports in `index.html` (`<script type="module" src="app.js" defer>`). Split `app.js` into:
- `app.js` — router, init, global state
- `views/board.js`
- `views/backlog.js`
- `views/releases.js`
- `views/pages.js`
- `views/activity.js`
- `views/repos.js`
- `components/task-panel.js`
- `components/modal.js`

Each view module exports a single `render(projectId)` function. The router in `app.js` imports and calls the correct one.

### 7.4 Frontend: user switcher

Add a "Switch user" dropdown in the sidebar under the user avatar. On change:
1. Call `GET /api/users/me` with the selected user's ID to verify they exist.
2. Store via `api.setUserId(id)`.
3. Re-render the current view.

Populate the dropdown from `GET /api/projects/{id}/members` (the new endpoint from §4.2) when a project is selected.

### 7.5 Eliminate magic strings

Grep all Go files for `"OWNER"`, `"DEVELOPER"`, `"VIEWER"`, `"PLANNED"`, `"IN_PROGRESS"`, `"DONE"`, `"ARCHIVED"`, `"GITHUB"`, `"BITBUCKET"`, etc. used as literals in multiple files. Every such string must be a named constant in the domain package and imported by other packages. No raw string literals for domain values in handler or repo files.

---

## Verification Checklist

Before marking this work complete, verify:

- [ ] `go test ./...` passes with zero failures
- [ ] `golangci-lint run ./...` reports zero issues
- [ ] `pytest` for `octbase-frontend/tests/` passes with zero failures
- [ ] `draft/Octbase_Go_API_Curl_Tests.sh` exits 0
- [ ] A request with no `X-User-Id` to any write endpoint returns 401
- [ ] A request with a valid UUID for a non-existent user to any write endpoint returns 401
- [ ] A VIEWER role member cannot POST to any mutating endpoint (returns 403)
- [ ] A DEVELOPER role member cannot delete a project or manage memberships (returns 403)
- [ ] `AddRelation BLOCKS A→B` results in `BLOCKED_BY B→A` in the database
- [ ] Moving a task to a board column and then querying the board reflects the change (transaction integrity)
- [ ] The AsciiDoc preview endpoint strips `<script>` tags from output
- [ ] `POST /api/users` creates a user; a duplicate email returns 409
- [ ] All list endpoints with large datasets return paginated results respecting `page` and `size`
