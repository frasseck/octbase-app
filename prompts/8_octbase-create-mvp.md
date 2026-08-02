# Octbase — POC to MVP

**Purpose:** Transform the finalized POC into a production-ready MVP for a single client replacing Jira, Confluence, and Bitbucket with a tool that does fewer things but does them better.

**Read first:** `prompts/10_TODO_MVP.md` and `prompts/9_TODO_POC.md`. `prompts/9_TODO_POC.md` must be fully completed (prompt 7) before this prompt is applied. This prompt builds on that foundation.

**Philosophy:** Every feature that ships must make the core experience meaningfully better for a real team. Nothing ships because it is easy to add, because Jira has it, or because someone might want it later. The non-goals in `prompts/10_TODO_MVP.md` §0 are hard constraints — do not implement them.

---

## 0. Architectural Prerequisites

### 0.1 Confirm prompt 7 is done

Verify:
- `go test ./...` passes
- `golangci-lint run ./...` is clean
- All domain rules from prompt 7 are enforced (membership guards, RBAC, typed value objects, domain events)
- The `cmd/octbase-seed` binary is **not** separate from the main binary — seed logic lives in `internal/seed/seed.go` and runs on startup when `OCTBASE_DEMO_MODE=true`

### 0.2 API versioning

Before adding any new endpoint, prefix all routes with `/api/v1/`. Existing routes stay functionally identical; only the path prefix changes. Update `api/openapi.yaml` and the frontend `api.js` `BASE_PATH` constant accordingly.

### 0.3 Migration tooling

Replace the single `001_initial.sql` approach with `golang-migrate`. Add `github.com/golang-migrate/migrate/v4` to `go.mod`. Migration files live in `octbase-api/migrations/` with the naming convention `NNN_description.up.sql` / `NNN_description.down.sql`. The application runs pending migrations at startup. The seed binary runs after migrations.

---

## 1. Identity & Access

This is the foundation. Do not start section 2 until section 1 is complete and tested.

### 1.1 Auth strategy: email + password with JWT

Implement the following:

**New tables** — `003_auth.up.sql`:
```sql
ALTER TABLE users ADD COLUMN password_hash TEXT;
ALTER TABLE users ADD COLUMN is_active     BOOLEAN NOT NULL DEFAULT true;

CREATE TABLE refresh_tokens (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_refresh_tokens_user ON refresh_tokens(user_id);
```

**New env vars** (add to `.env.example`):
```
OCTBASE_JWT_SECRET=<32+ random bytes, base64-encoded>
OCTBASE_JWT_ACCESS_TTL=15m
OCTBASE_JWT_REFRESH_TTL=720h
```

**New endpoints** in a new `internal/auth/` package:
```
POST /api/v1/auth/login
     Body: { email, password }
     Response: { accessToken, expiresIn } + Set-Cookie: refresh_token=<token>; HttpOnly; SameSite=Strict; Path=/api/v1/auth/refresh
     Error: 401 INVALID_CREDENTIALS (same message for wrong email or wrong password — no oracle)

POST /api/v1/auth/refresh
     Reads refresh token from cookie
     Response: { accessToken, expiresIn } + new Set-Cookie (token rotation)
     Error: 401 REFRESH_TOKEN_INVALID

POST /api/v1/auth/logout
     Invalidates the refresh token stored in cookie
     Response: 204 + Set-Cookie that clears the cookie

GET  /api/v1/auth/me
     Requires valid access token
     Response: { id, name, email, role (if project context is given) }
```

**JWT structure:**
```json
{ "sub": "<user_id>", "iat": <unix>, "exp": <unix> }
```

Use `github.com/golang-jwt/jwt/v5`. The `X-User-Id` header used in the POC is retired. Replace the `AuthMiddleware` with a `JWTMiddleware` that validates the Bearer token in the `Authorization` header and sets the user ID in the request context.

**Password hashing:** `golang.org/x/crypto/bcrypt` with cost 12.

**Abstract the auth provider** behind an interface so the implementation can be swapped for SAML/OIDC later:
```go
// internal/auth/provider.go
type Provider interface {
    Login(ctx context.Context, email, password string) (*User, error)
    ValidateToken(token string) (userID string, err error)
}
```

### 1.2 User invitation flow

Replace `POST /api/v1/users` (open registration) with an invitation system:

**New table** — `003_auth.up.sql`:
```sql
CREATE TABLE invitations (
    id         TEXT PRIMARY KEY,
    email      TEXT NOT NULL,
    project_id TEXT REFERENCES projects(id) ON DELETE CASCADE,
    role       TEXT NOT NULL DEFAULT 'DEVELOPER',
    token_hash TEXT NOT NULL UNIQUE,
    invited_by TEXT NOT NULL REFERENCES users(id),
    expires_at TIMESTAMPTZ NOT NULL,
    accepted_at TIMESTAMPTZ
);
```

**Endpoints:**
```
POST /api/v1/admin/invitations
     OWNER or admin only
     Body: { email, projectId (optional), role }
     Sends invitation email (§5); stores hashed token

GET  /api/v1/invitations/{token}
     Public — validates token, returns { email, projectName, inviterName }

POST /api/v1/invitations/{token}/accept
     Public — Body: { name, password }
     Creates user, marks invitation accepted, adds project membership if projectId set
     Response: same as login (access token + refresh cookie)
```

### 1.3 Admin panel endpoints

Add `internal/admin/` package. Requires a new `is_admin` boolean column on `users`. Admin endpoints are guarded by `RequireAdmin` middleware.

```
GET    /api/v1/admin/users              list all users with status
PATCH  /api/v1/admin/users/{userId}     { isActive: bool } — deactivate/reactivate
POST   /api/v1/admin/users/{userId}/reset-password
                                        generates a one-time token, sends email
```

### 1.4 Frontend: replace header auth with real login

Remove the "Switch user" dropdown from the sidebar. Add:
- A `/login` route in the frontend router that renders a login form.
- On load, if no valid access token exists in memory (not localStorage — store in-memory only, refresh via cookie), redirect to `/login`.
- On login success, store the access token in a module-scoped variable; attach it as `Authorization: Bearer <token>` via `api.js`.
- On 401 from any request, attempt one token refresh; if refresh fails, redirect to `/login`.
- Display the logged-in user's name and initials in the sidebar (from `GET /api/v1/auth/me`).

### 1.5 Tests

- Unit: `bcrypt` hash + verify round-trip
- Unit: JWT issue + validate + expired token rejection
- Integration: login → get access token → call protected endpoint → success
- Integration: login → logout → use old refresh token → 401
- Integration: invite user → accept invitation → login → membership in project
- Integration: deactivated user cannot log in
- Integration: VIEWER cannot reach any write endpoint (end-to-end, not just service test)

---

## 2. User Experience

### 2.1 Personal dashboard ("My Work")

New route: `/` (replaces the project list as the landing page).

**API endpoint:**
```
GET /api/v1/users/me/dashboard
Response: {
    assignedTasks:  Task[],   // status != DONE && status != ARCHIVED, limit 20
    reviewingTasks: Task[],   // reviewer_id = me, status = IN_REVIEW, limit 10
    recentPages:    Page[],   // pages I last edited, limit 5
    upcomingReleases: Release[] // due_date within 14 days, status = PLANNED, limit 5
}
```

The frontend renders this as four columns or sections. Each task row is clickable and opens the task panel. No extra clicks — the first thing a user sees after login tells them what to do today.

### 2.2 Command palette (⌘K / Ctrl+K)

A modal overlay that opens with `Ctrl+K` or `Cmd+K` and closes with `Escape`.

**API endpoint:**
```
GET /api/v1/search?q={query}&projectId={optional}
Response: {
    tasks: [{ id, title, status, projectName }],
    pages: [{ id, title, slug, projectName }],
    projects: [{ id, name, slug }]
}
Limit: 5 results per category. Minimum query length: 2 chars.
```

The frontend renders results in three sections with keyboard navigation (`↑↓` to move, `Enter` to navigate). Selecting a task opens its panel; selecting a page or project navigates to it.

### 2.3 Bookmarkable, filter-preserving URLs

Refactor the frontend router so that all view state is encoded in the URL:

| State | URL |
|---|---|
| Board view, project ABC | `/#/projects/ABC/board` |
| Backlog, filtered by HIGH priority | `/#/projects/ABC/backlog?priority=HIGH` |
| Task panel open | `/#/projects/ABC/board?task=TASK_ID` |
| Page editor | `/#/projects/ABC/pages/PAGE_SLUG/edit` |

On load, parse URL params and restore filters and panel state before first render. Changing a filter updates the URL via `history.replaceState` without a full page reload.

### 2.4 Inline task creation with keyboard shortcut

On the board and backlog views, pressing `N` opens an inline creation row at the top of the first column (board) or the list (backlog). The row contains only a title input. Pressing `Enter` creates the task and resets the row for the next task. Pressing `Escape` cancels. No modal.

### 2.5 Bulk task actions

Add a checkbox to each task card (visible on hover or when any checkbox is checked). When one or more tasks are selected, a floating action bar appears at the bottom of the view with:
- Assign to (user dropdown)
- Set release (release dropdown)
- Set status (status dropdown)
- Set priority (priority dropdown)
- Archive selected

**API endpoint:**
```
POST /api/v1/projects/{projectId}/tasks/bulk
Body: {
    taskIds: string[],
    action: "set_status" | "set_priority" | "set_assignee" | "set_release" | "archive",
    value: string  // the new status, priority, userId, or releaseId
}
Response: { updated: number }
```

Each individual update must be wrapped in a transaction. If any single update fails (e.g., task is immutable), skip it and continue; report the count of successfully updated tasks.

### 2.6 @mention in comments and descriptions

In the task panel comment box, typing `@` triggers a dropdown populated from `GET /api/v1/projects/{projectId}/members`. Selecting a user inserts `@name` as plain text (not a special markup). The API stores the raw text; mention detection happens in a separate step.

**API change:** when a comment is created or a task description is updated, scan the text for `@name` patterns using member display names. For each match, create a notification (§5).

### 2.7 Complete keyboard shortcut map

Implement and document these shortcuts:

| Key | Action |
|-----|--------|
| `N` | New task (inline if on board/backlog, modal otherwise) |
| `B` | Switch to board view |
| `L` | Switch to backlog view |
| `M` | Switch to releases view |
| `P` | Switch to pages view |
| `Ctrl+K` / `Cmd+K` | Open command palette |
| `Esc` | Close panel / modal / palette |
| `E` | Edit focused task title (when task panel is open) |
| `A` | Assign focused task to me |
| `?` | Show keyboard shortcut help overlay |

Register shortcuts globally in `app.js` with a `keydown` listener that ignores events when focus is inside an `input`, `textarea`, or `[contenteditable]`.

### 2.8 Page editor: live split-pane preview

Replace the existing "Preview" button with a split-pane layout: left pane is the raw AsciiDoc textarea; right pane shows the rendered HTML updated 300ms after the user stops typing (debounced `POST /api/v1/pages/{id}/render-preview`). A toggle button collapses the preview pane for distraction-free writing.

### 2.9 Page table of contents

When rendering a page for reading (not editing), extract all `== Heading` and `=== Sub-heading` elements from the rendered HTML and render them as a sticky TOC sidebar on the right side of the page body. Clicking a TOC entry smoothly scrolls to the heading. Hide the TOC if the page has fewer than 3 headings.

### 2.10 Unified search result page

In addition to the command palette (§2.2), add a `/search` route that renders full results with pagination and filters (by project, by type: task / page).

---

## 3. Real-time Collaboration (Server-Sent Events)

### 3.1 SSE endpoint

```
GET /api/v1/projects/{projectId}/events
    Authorization: Bearer <token>
    Response: text/event-stream
```

The handler registers a channel on a per-project in-process hub, holds the connection open, and writes events as they arrive. On client disconnect, the channel is removed from the hub.

**Event shape:**
```
data: {"type":"task.moved","taskId":"...","columnId":"...","actorId":"..."}
```

**Event types to broadcast:**
- `task.created`, `task.updated`, `task.moved`, `task.status_changed`, `task.archived`
- `comment.added`, `comment.deleted`
- `page.published`
- `release.closed`

The `EventBus` from prompt 7 §6.2 is the source. Add an SSE subscriber that fans events out to all connected clients for the relevant project.

### 3.2 Frontend: consume SSE

In `app.js`, when a project view is active, open an `EventSource` to the SSE endpoint. On each event:
- If the event affects a visible entity (task on board, task in panel, etc.), update the DOM in-place without a full re-render.
- Show a subtle "Live" indicator (green dot) in the topbar while connected.
- On `EventSource` error, attempt reconnect with exponential backoff (1s, 2s, 4s, max 30s).

### 3.3 Presence indicators

Add a presence endpoint:
```
GET /api/v1/projects/{projectId}/presence
Response: { viewers: [{ userId, name, viewingTaskId }] }
```

The server tracks active SSE connections per project in a map. The presence endpoint reads from this map. Poll it every 10 seconds (SSE for presence would be overkill). When a task panel is open, show avatars of other users currently viewing that task below the task title.

---

## 4. Notifications

### 4.1 Database schema — `004_notifications.up.sql`

```sql
CREATE TABLE notifications (
    id          TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind        TEXT NOT NULL,  -- "task_assigned", "mentioned", "status_changed", "release_due"
    project_id  TEXT REFERENCES projects(id) ON DELETE CASCADE,
    task_id     TEXT REFERENCES tasks(id) ON DELETE CASCADE,
    page_id     TEXT REFERENCES pages(id) ON DELETE CASCADE,
    message     TEXT NOT NULL,
    is_read     BOOLEAN NOT NULL DEFAULT false,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_notifications_user ON notifications(user_id, created_at DESC);

CREATE TABLE notification_preferences (
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind    TEXT NOT NULL,
    in_app  BOOLEAN NOT NULL DEFAULT true,
    email   BOOLEAN NOT NULL DEFAULT true,
    PRIMARY KEY (user_id, kind)
);
```

### 4.2 Notification endpoints

```
GET    /api/v1/users/me/notifications         ?unreadOnly=true&page=1&size=20
POST   /api/v1/users/me/notifications/read-all
PATCH  /api/v1/users/me/notifications/{id}   { isRead: true }
GET    /api/v1/users/me/notification-preferences
PATCH  /api/v1/users/me/notification-preferences  { kind, inApp, email }
```

### 4.3 Notification triggers

Wire these into the event bus subscriber:

| Event | Recipient | Kind |
|-------|-----------|------|
| Task assigned to user | Assignee | `task_assigned` |
| Task reviewer set | Reviewer | `task_assigned` |
| `@name` in comment | Mentioned user | `mentioned` |
| Task status changed | Task owner (reporter) | `status_changed` |
| Release due in 3 days | All project members | `release_due` |

### 4.4 Email delivery

Add `internal/mailer/` package with interface:
```go
type Mailer interface {
    Send(ctx context.Context, to, subject, body string) error
}
```

Implement with SMTP (`net/smtp`). Env vars: `OCTBASE_SMTP_HOST`, `OCTBASE_SMTP_PORT`, `OCTBASE_SMTP_FROM`, `OCTBASE_SMTP_USER`, `OCTBASE_SMTP_PASS`. If any are unset, the mailer logs the email to stdout (dev mode) instead of sending.

Email template: plain text + minimal HTML (`<h2>`, `<p>`, one action link). No external CSS. The action link is `OCTBASE_APP_URL/projects/{id}/board?task={id}`.

### 4.5 Frontend: notification bell

Add a bell icon to the topbar showing the unread count (polled every 60 seconds or updated via SSE when a `notification.created` event arrives). Clicking it opens a dropdown with the 10 most recent notifications. Each notification links to the relevant task or page. A "Mark all read" button at the top. A "Preferences" link at the bottom goes to the notification settings page.

---

## 5. SCM Integration (Real Webhooks)

### 5.1 Bitbucket webhook receiver

```
POST /api/v1/webhooks/bitbucket
     No auth header required — validated via HMAC-SHA256 signature
     Header: X-Hub-Signature-256: sha256=<hmac>
     Body: Bitbucket Push or Pull Request event payload
```

Env var: `OCTBASE_WEBHOOK_SECRET_BITBUCKET`. If unset, reject all webhook requests with 403.

HMAC validation:
```go
func validateBitbucketSignature(secret, body string, r *http.Request) bool {
    sig := r.Header.Get("X-Hub-Signature-256")
    mac := hmac.New(sha256.New, []byte(secret))
    mac.Write([]byte(body))
    expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
    return hmac.Equal([]byte(sig), []byte(expected))
}
```

**Event handling:**
- `pullrequest:created` — find branch references matching `source.branch.name`; set PR status to `open` on the branch record; publish `branch.pr_opened` domain event.
- `pullrequest:fulfilled` (merged) — set PR status to `merged`; if project setting `auto_close_on_merge=true`, move the linked task to DONE.
- `pullrequest:rejected` — set PR status to `declined`.

**Schema change** — `005_scm.up.sql`:
```sql
ALTER TABLE branch_references ADD COLUMN pr_status    TEXT;    -- open | merged | declined
ALTER TABLE branch_references ADD COLUMN pr_url       TEXT;
ALTER TABLE branch_references ADD COLUMN pr_number    INTEGER;
ALTER TABLE repository_connections ADD COLUMN auto_close_on_merge BOOLEAN NOT NULL DEFAULT false;
```

### 5.2 GitHub webhook receiver

```
POST /api/v1/webhooks/github
     Header: X-Hub-Signature-256 (same HMAC scheme)
     Env var: OCTBASE_WEBHOOK_SECRET_GITHUB
```

Handle `pull_request` events (`opened`, `closed` with `merged: true`, `closed` with `merged: false`). Map to the same internal branch-reference state changes as Bitbucket.

### 5.3 Branch name suggestion

In the task panel, when the user clicks "Link branch", pre-populate the branch name input with:
```
{type}/{project_slug}-{task_seq}-{slugified_title}
```
Example: `feature/tb-42-improve-search`

Add a `sequence_number` (auto-increment per project) to tasks:
```sql
-- 005_scm.up.sql
ALTER TABLE tasks ADD COLUMN seq_number INTEGER;
CREATE SEQUENCE task_seq_{project_id};  -- not feasible dynamically; use:
-- Add a project-scoped sequence using a counter table instead:
CREATE TABLE project_task_counters (
    project_id TEXT PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
    last_seq   INTEGER NOT NULL DEFAULT 0
);
```
On task create, increment and fetch the counter in the same transaction, store on the task. Expose as `seqNumber` in the task JSON. Display as `{PROJECT_SLUG}-{seqNumber}` in the UI (e.g., `TB-42`).

### 5.4 Frontend: PR status on task panel

In the task panel "Branches" section, show for each linked branch:
- Branch name (existing)
- PR status badge: `open` (blue), `merged` (green), `declined` (red), or nothing if no PR
- PR link if `pr_url` is set

---

## 6. Data Migration from Jira / Confluence

### 6.1 Jira CSV import

```
POST /api/v1/admin/import/jira
     Content-Type: multipart/form-data
     Field: file (CSV)
     Field: projectId (string)
     Field: dryRun (boolean, default false)
     Response: { created: number, skipped: number, errors: [{ row, reason }] }
```

**Column mapping** (Jira standard export columns):
| Jira column | Octbase field |
|-------------|----------------|
| Summary | title |
| Description | description |
| Issue Type | taskType (BUG→BUG, Story→STORY, Epic→EPIC, Task→TASK, else CHORE) |
| Priority | priority (Highest/High→HIGH, Medium→MEDIUM, Low/Lowest→LOW) |
| Status | status (Done→DONE, In Progress→IN_PROGRESS, else PLANNED) |
| Issue key | external_ref |
| Assignee | assignee_id (matched by email if user exists; skipped if not) |

**Schema addition:**
```sql
-- 006_migration.up.sql
ALTER TABLE tasks ADD COLUMN external_ref TEXT;
CREATE INDEX idx_tasks_external_ref ON tasks(external_ref);
```

### 6.2 Confluence HTML import

```
POST /api/v1/admin/import/confluence
     Content-Type: multipart/form-data
     Field: file (ZIP — Confluence HTML export)
     Field: projectId
     Field: dryRun
     Response: { created: number, skipped: number, errors: [{ file, reason }] }
```

For each HTML file in the ZIP:
1. Extract the `<title>` as the page title.
2. Convert the HTML body to AsciiDoc using a best-effort conversion (strip Confluence-specific macros; keep headings, paragraphs, lists, and code blocks).
3. Create a page via the docs service with `status = DRAFT`.
4. Store the original Confluence page ID (from the filename or HTML meta) in a `external_ref` column:

```sql
-- 006_migration.up.sql
ALTER TABLE pages ADD COLUMN external_ref TEXT;
```

### 6.3 Import UI

Add an "Import" section to the admin panel (accessible via `/#/admin/import`). Two tabs: "From Jira" and "From Confluence". Each tab has:
- A project selector dropdown
- A file upload input
- A "Dry run" checkbox (default on)
- A "Run import" button
- A results section showing created/skipped/error counts

---

## 7. Performance & Reliability

### 7.1 Connection pool configuration

In `main.go`, after `sql.Open`:
```go
db.SetMaxOpenConns(25)
db.SetMaxIdleConns(5)
db.SetConnMaxLifetime(5 * time.Minute)
db.SetConnMaxIdleTime(1 * time.Minute)
```
Document the values and the PostgreSQL `max_connections` assumption (100 by default).

### 7.2 Graceful shutdown

```go
srv := &http.Server{Addr: addr, Handler: router}

go func() {
    if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
        slog.Error("server error", "err", err)
        os.Exit(1)
    }
}()

quit := make(chan os.Signal, 1)
signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
<-quit

ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()
if err := srv.Shutdown(ctx); err != nil {
    slog.Error("graceful shutdown failed", "err", err)
}
```

### 7.3 Prometheus metrics

Add `github.com/prometheus/client_golang`. Expose `/metrics`. Instrument:
- HTTP request count and duration by route and status (use chi middleware)
- DB query duration (wrap repo methods with a timing helper)
- SSE connection count (gauge, updated on connect/disconnect)
- Notification email success/failure count

### 7.4 Deep health check

Extend `GET /api/v1/health`:
```json
{
    "status": "ok",
    "db": { "status": "ok", "poolOpen": 3, "poolIdle": 2, "migrationVersion": 6 },
    "version": "0.2.0"
}
```
Return `503` if DB ping fails or migration version does not match the expected version.

### 7.5 Log levels

```go
level := slog.LevelInfo
if os.Getenv("OCTBASE_LOG_LEVEL") == "debug" {
    level = slog.LevelDebug
}
// production sets OCTBASE_LOG_LEVEL=warn
```
Audit all `slog.Debug` calls to ensure they do not log request bodies, passwords, or tokens.

---

## 8. Deployment & Operations

### 8.1 Production Containerfile

Multi-stage build in `octbase-api/Containerfile`:
```dockerfile
FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /octbase-api ./cmd/octbase-api

FROM gcr.io/distroless/static-debian12
COPY --from=builder /octbase-api /octbase-api
EXPOSE 8000
ENTRYPOINT ["/octbase-api"]
```

### 8.2 CI pipeline — `.github/workflows/ci.yml` or equivalent

Stages:
1. `lint` — `golangci-lint run ./...`
2. `test` — `go test ./...` with PostgreSQL service container
3. `e2e` — frontend `pytest` with the full stack
4. `build` — `docker build` (or `podman build`), push image on merge to `main`

No step may be skipped. A failed lint blocks the merge.

### 8.3 TLS

Update `octbase-frontend/nginx.conf` to redirect HTTP to HTTPS. TLS certificates are terminated at nginx using a cert path from env var `NGINX_TLS_CERT` and `NGINX_TLS_KEY`. For local development, generate a self-signed cert. Document the Let's Encrypt renewal process for production.

### 8.4 Environment documentation

Write `docs/operations.md` (not a memory file — this is a project document meant for the human ops team) covering:
- All env vars with type, default, and whether they are required
- How to run migrations manually
- How to restore from a backup
- How to add a new user (invite flow)
- How to deactivate a user
- How to rotate the JWT secret (requires all users to re-login)
- How to roll back a deployment

---

## 9. Architecture Decisions to Make Before Building

Before writing any code for this prompt, answer each of these questions and record the decision inline in this file or in a `docs/decisions/` folder:

1. **SAML/OIDC vs email+password:** Does the client have an existing identity provider (Azure AD, Okta, Google Workspace)? If yes, implement OIDC; skip the password hash and invitation token flow. If no, implement email+password as described in §1.1.

2. **SSE vs WebSocket:** The MVP uses SSE (unidirectional, simpler). If the client needs collaborative editing of pages (multiple users editing the same document simultaneously), WebSocket is required. For the MVP, assume no collaborative editing — SSE is sufficient.

3. **Task sequence number display format:** Confirm the `{PROJECT_SLUG}-{seqNumber}` format with the client before building. Changing it later breaks all external references (emails, Slack messages, documentation).

4. **Auto-close on PR merge:** Default `false` per repository connection. Confirm the client wants this behaviour before enabling it by default.

5. **Confluence import scope:** Decide whether to attempt to convert Confluence macros (code blocks, info panels, tables of contents) or strip them. A best-effort conversion is better than a blank page, but set expectations with the client.

---

## Verification Checklist

- [ ] A new user can be invited, accepts the invitation, and logs in via email + password
- [ ] A deactivated user's login attempt returns 401
- [ ] A VIEWER cannot POST to any task, comment, page, or release endpoint
- [ ] The personal dashboard shows assigned tasks, reviewing tasks, recent pages, and upcoming releases for the logged-in user
- [ ] `Ctrl+K` opens the command palette; typing 2+ chars returns matching tasks and pages; `Enter` navigates
- [ ] Filtering the board by HIGH priority updates the URL; reloading the page restores the filter
- [ ] Pressing `N` on the board opens an inline creation row; `Enter` creates the task; `Escape` cancels
- [ ] Selecting 3 tasks and bulk-assigning them updates all 3 in one action; the board reflects the change
- [ ] A task moved by another user appears in the correct column within 2 seconds (SSE)
- [ ] A task assignment triggers an in-app notification for the assignee
- [ ] A task assignment triggers an email to the assignee (check SMTP logs or stdout in dev mode)
- [ ] A Bitbucket webhook for a merged PR updates the branch PR status on the linked task to `merged`
- [ ] If `auto_close_on_merge=true`, the task moves to DONE on PR merge
- [ ] A Jira CSV import (dry run) returns the correct created/skipped counts without writing to the DB
- [ ] A Jira CSV import (live) creates tasks with `external_ref` set to the Jira issue key
- [ ] `/api/v1/health` returns DB migration version and pool stats
- [ ] `GET /metrics` returns Prometheus-formatted metrics including HTTP request duration
- [ ] The CI pipeline fails if any test fails or `golangci-lint` reports issues
- [ ] All routes are under `/api/v1/`
- [ ] TLS is enforced; HTTP redirects to HTTPS with 301
