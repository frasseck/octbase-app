# Octbase API

A Go REST API for the Octbase project management domain, backed by PostgreSQL.
The API serves its own docs UI and OpenAPI spec; the SPA frontend is served separately by Caddy.

## Quick Start

```bash
# Run full stack (API + postgres + frontend)
podman-compose -f ../podman-compose.yml up --build

# Dev variant with captured email (adds a Mailpit container — see "Email" below):
podman-compose -f ../podman-compose.yml -f ../podman-compose.dev.yml up --build

# App:      http://localhost:8080/
# API:      http://localhost:8000/
# Docs UI:  http://localhost:8000/docs
# OpenAPI:  http://localhost:8000/openapi.yaml
# Mailpit:  http://localhost:8080/mailpit/   (dev overlay only)
```

## Development

Start a local PostgreSQL instance:

```bash
podman run -d --name octbase-pg \
  -e POSTGRES_DB=octbase \
  -e POSTGRES_USER=octbase \
  -e POSTGRES_PASSWORD=octbase \
  -p 5432:5432 \
  registry.access.redhat.com/hi/postgresql:18
```

Run the API:

```bash
go mod tidy
OCTBASE_DATABASE_URL="postgres://octbase:octbase@localhost:5432/octbase?sslmode=disable" \
OCTBASE_DEMO_MODE=true \
go run ./cmd/octbase-api
```

Build the binary:

```bash
go build -o octbase-api ./cmd/octbase-api
```

## Environment Variables

| Variable | Default | Description |
| --- | --- | --- |
| `PORT` | `8000` | HTTP listen port |
| `OCTBASE_DATABASE_URL` | `postgres://octbase:octbase@localhost:5432/octbase?sslmode=disable` | PostgreSQL connection string |
| `OCTBASE_JWT_SECRET` | dev placeholder | 32+ random bytes — rotate to log out all users |
| `OCTBASE_DEMO_MODE` | `false` | Seeds demo users on startup |
| `OCTBASE_SECURE_COOKIES` | `false` | Set `true` in production so the refresh cookie carries the `Secure` flag |
| `OCTBASE_LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error` |
| `OCTBASE_APP_URL` | `http://localhost:8080` | Base URL used to build links in emails (invitation accept, task deep-links) |
| `OCTBASE_SMTP_HOST` | _(empty)_ | SMTP server host. **Leave blank to log emails to stdout** instead of sending |
| `OCTBASE_SMTP_PORT` | `587` | SMTP server port |
| `OCTBASE_SMTP_FROM` | `noreply@beyags.com` | `From` address on outgoing mail |
| `OCTBASE_SMTP_USER` | _(empty)_ | SMTP username (omit for unauthenticated relays like Mailpit) |
| `OCTBASE_SMTP_PASS` | _(empty)_ | SMTP password |

This table covers the most common variables — the full list (SCM encryption,
OAuth, trusted proxies, pool sizing, feature toggles, app version) lives in
[`../.env.example`](../.env.example) and [`../docs/operations.md`](../docs/operations.md).

## Email

The API sends transactional email for **member invitations** and **task-change
notifications** (see the `mailer` and `notifications` packages). When
`OCTBASE_SMTP_HOST` is empty the mailer falls back to dev-mode: every message is
logged to stdout instead of being delivered.

For local development, layer `podman-compose.dev.yml` on top of the base
compose file to add a [Mailpit](https://mailpit.axllent.org/) container as a
fake SMTP server, so the real send path is exercised without mail leaving the
host (the overlay points `OCTBASE_SMTP_HOST` at `mailpit`, port `1025`):

```bash
podman-compose -f ../podman-compose.yml -f ../podman-compose.dev.yml up -d
```

Mailpit is **development-only and must never be deployed**: its mailbox holds
every message the API sends, including password-reset and invitation links.
The base `podman-compose.yml` contains no Mailpit service and the production
front door (`Caddyfile.tls`) does not proxy `/mailpit`.

Mailpit runs with `MP_WEBROOT=mailpit`, its web UI port bound to `127.0.0.1`
only and protected by basic auth (`octbase:octbase` by default; override with
`MAILPIT_UI_AUTH=user:pass`). Reach it locally at
**http://localhost:8025/mailpit/** or through the dev frontend Caddy at
**http://localhost:8080/mailpit/**. The REST API lives under the same
`/mailpit/` web root:

```bash
# List captured messages
curl -su octbase:octbase http://127.0.0.1:8025/mailpit/api/v1/messages

# Read one message body
curl -su octbase:octbase http://127.0.0.1:8025/mailpit/api/v1/message/<ID>

# Clear the mailbox
curl -su octbase:octbase -X DELETE http://127.0.0.1:8025/mailpit/api/v1/messages
```

Task-change emails are on by default and go to the task's reporter and assignee;
the actor who made the edit is never emailed, and reporter/assignee are
de-duplicated. Point the `OCTBASE_SMTP_*` variables at a real relay for
production.

## Architecture

- **Go standard library** + chi router
- **pgx** PostgreSQL driver (`database/sql` via `pgx/v5/stdlib`)
- **JWT authentication** — every `/api/v1` route (except `auth/login`, `auth/refresh`, `auth/logout`, `auth/mfa/verify`, `auth/forgot-password`, `auth/reset-password`, `health`, `version`, `meta/enums`, `config`, invitation inspect/accept, the OAuth callback `GET /api/v1/oauth/{provider}/callback`, and the HMAC webhook receivers) requires a `Bearer` token
- **Modular monolith** organised by bounded context:
  - `auth` — JWT issue/validate, bcrypt, refresh tokens, invitation flow, change-password, `JWTMiddleware`, `LoadUserGlobalRole`
  - `bootstrap` — creates the first `SUPER_ADMIN` from `OCTBASE_BOOTSTRAP_ADMIN_*` while the users table is empty
  - `identityaccess` — users, project memberships
  - `rbac` — pure permission functions (`Can*()` helpers), no DB
  - `usermgmt` — SUPER_ADMIN user CRUD at `/api/v1/users`
  - `auditlog` — `audit_logs` table, `Repo.Write()`, `GET /api/v1/audit-logs` (SUPER_ADMIN only)
  - `admin` — legacy admin panel: invitations, `RequireAdmin` middleware
  - `workmanagement` — projects, tasks, boards, releases, categories, templates
  - `docs` — wiki pages, revisions, render-preview, page search
  - `scmintegration` — repository connections (PAT or OAuth, tokens encrypted at rest), branch references, PR status, PR creation
  - `notifications` — in-app notifications
  - `sse` — Server-Sent Events hub, presence
  - `webhooks` — Bitbucket/GitHub HMAC receivers
  - `mailer` — SMTP with stdout dev-mode fallback
  - `activity` — project activity feed
  - `dashboard` — per-user dashboard preferences
  - `security/mfa` — TOTP MFA enrollment and recovery codes
  - `retention` — GDPR data purge
  - `shared` — DB/transaction helpers, HTTP/JSON utils, CORS, RBAC guards
  - `seed` — deterministic demo data (`OCTBASE_DEMO_MODE`)
  - test-only: `testutil` (shared test infrastructure), `apicontract` (route↔OpenAPI parity), `archtest` (core/module dependency direction)
- Each domain package contains its HTTP handlers, repositories, domain structs, and tests
- Migrations run at startup via `golang-migrate` from `migrations/` (sequentially numbered from `001`, each a `.up.sql`/`.down.sql` pair; the expected head version is derived from the files — nothing to bump)
- **slog** structured JSON logging

## Authorization model

**Global roles** (stored on the user): `SUPER_ADMIN` · `ADMIN` · `USER` · `GUEST`

**Project roles** (stored on the membership): `PROJECT_OWNER` · `PROJECT_ADMIN` · `PROJECT_MEMBER` · `PROJECT_VIEWER`

Authorization flow:
1. `JWTMiddleware` validates the token and sets `userID` in context
2. `LoadUserGlobalRole` loads `global_role` and blocks disabled accounts immediately
3. Per-handler: `shared.GetGlobalRole(r)` → `rbac.Can*()` helpers, which delegate to `rbac.HasPermission(globalRole, projectRole, permission)` against a permission matrix (`internal/rbac/rbac.go`)
4. `memberGuard`: SUPER_ADMIN bypasses membership checks and is treated as PROJECT_ADMIN (but `HasPermission` still grants SUPER_ADMIN every permission)

### Project ownership

- Every project always has at least one `PROJECT_OWNER` (the creator on `CreateProject`; backfilled by migration `010` for pre-existing projects)
- `GET /api/v1/projects/{id}/permissions` returns `{projectId, role, permissions: {<permission>: bool}}` for the current user — see `rbac.AllPermissions()` for the full key list
- `GET /api/v1/projects/{id}/assignable-users` returns the candidates for a task's assignee/reviewer: the project's members **plus** every active global `ADMIN`/`SUPER_ADMIN`, who reach a project without a membership row and would otherwise be invisible to the pickers. An admin who is also a member appears once, as a member. A non-member admin's `role` is empty, `member` is `false`, and their `email` is withheld — this list must not become an admin-address directory readable from any project. Same read guard as `GET .../members`
- Granting or revoking `PROJECT_OWNER` on a membership (`PATCH .../memberships/{userId}`) requires the actor to already be an `OWNER` (or `SUPER_ADMIN`) — `rbac.CanAssignRole` / `rbac.CanChangeRole`
- Changing or removing the last `PROJECT_OWNER`'s membership is rejected with `422 {code:"LAST_OWNER"}` — `rbac.WouldRemoveLastOwner`

## Demo Data

When `OCTBASE_DEMO_MODE=true`, seed data is upserted on startup (idempotent):

| Email | Password | Global role | ID suffix |
|---|---|---|---|
| `super@octbase.dev` | `superpass1234` | SUPER_ADMIN | `…0010` |
| `demo@octbase.dev` | `demopass1234` | ADMIN | `…0001` |

Also seeded: a demo project with tasks, board, release, wiki page, repository connection, and activity entries.

Authenticate and call the API:

```bash
TOKEN=$(curl -s -X POST http://localhost:8000/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"super@octbase.dev","password":"superpass1234"}' \
  | python3 -c "import json,sys; print(json.load(sys.stdin)['accessToken'])")

curl http://localhost:8000/api/v1/projects -H "Authorization: Bearer $TOKEN"
curl http://localhost:8000/api/v1/users    -H "Authorization: Bearer $TOKEN"
curl http://localhost:8000/api/v1/audit-logs -H "Authorization: Bearer $TOKEN"
```

## Testing

Octbase has three complementary test suites: Go integration tests for this API,
a Playwright/pytest browser suite for the frontend, and a full multi-user API
scenario that drives the whole product end to end.

### 1. Go backend tests (`octbase-api`)

Integration-style tests that run the real HTTP handlers against a real
PostgreSQL. They require `TEST_DATABASE_URL`; each test applies the real
migrations into its own schema and drops it afterwards for isolation, and the
whole suite **skips** if the variable is not set. There are well over a hundred
`*_test.go` files across the bounded contexts (117 as of 2026-08-02).

```bash
cd octbase-api
TEST_DATABASE_URL="postgres://octbase:octbase@localhost:5432/octbase?sslmode=disable" \
go test ./...

# One package / one test
TEST_DATABASE_URL=... go test ./internal/workmanagement/... -run TestTaskTemplates_CRUD -v
```

CI enforces a hard statement-coverage floor (currently **73.0%**, see
`.github/workflows/ci.yml`) that must never be lowered to green a build.
Adjacent checks: `go build ./...`, `go vet ./...`, `gofmt -l .`,
`golangci-lint run ./...`.

Seeded test users available in `internal/testutil`:

| Constant | Email suffix | Global role |
|---|---|---|
| `SuperAdminUserID` | `…0010` | SUPER_ADMIN |
| `DemoUserID` | `…0001` | ADMIN |
| `SecondUserID` | `…0002` | USER |
| `GuestUserID` | `…0003` | GUEST |
| `DisabledUserID` | `…0004` | disabled ADMIN |

### 2. Frontend end-to-end tests (`octbase-frontend/tests`)

Python + pytest + Playwright — ~25 `test_*.py` files covering the board,
backlog, sprints, milestones, task panel, members, RBAC, permissions, search,
navigation, i18n, activity, repos, rich-text attachments and accessibility.
They drive a real browser against a **live, seeded, demo-mode API** and skip
cleanly if it is unreachable. A virtualenv lives at
`octbase-frontend/tests/.venv`.

```bash
cd octbase-frontend/tests
# Always use system Chrome on this OS; bundled chromium/firefox will not install.
OCTBASE_BROWSER=chrome .venv/bin/python -m pytest -q                 # whole suite
OCTBASE_BROWSER=chrome .venv/bin/python -m pytest test_board.py -x -q   # one file

# Target a non-default API (e.g. the dev stack on :8001)
OCTBASE_API_BASE=http://127.0.0.1:8001 OCTBASE_BROWSER=chrome .venv/bin/python -m pytest -q
```

### 3. Full agile scenario — end-to-end API (`scripts/`)

A self-contained, multi-user scenario that exercises the whole product the way
a five-person team would if they used Octbase to build Octbase: it onboards
users with distinct global and project roles, staffs a project, configures
categories/release/sprints, fills an epic/story/task backlog, relates,
links and discusses the work, runs two sprints through the board (enroll → In
Progress → In Review → Done, carry-over, scope-lock), manages the release,
writes wiki pages, and verifies search, activity feeds, dashboards,
notifications, the audit log and RBAC — ~60 assertions over the REST API in a
few seconds (no browser).

```bash
# Reset + reseed the DB, run the scenario, reset again — all within a 20-minute
# budget. Targets the stack described by ./.env (the dev stack on :8001).
scripts/run_agile_scenario.sh

# Or run just the scenario against an already-running stack (it self-suffixes
# user/project names, so it is idempotent even without a reset):
python3 scripts/simulate_agile_project.py --base http://127.0.0.1:8001
```

The runner reseeds via `scripts/reset_db.sh` and exits non-zero if any
assertion fails or the run exceeds its time budget.

## Key Endpoints

All routes are prefixed `/api/v1/`. The authoritative list is the OpenAPI spec at `http://localhost:8000/docs`.

### System & auth
- `GET  /api/v1/health` — DB pool stats + migration version (503 when degraded)
- `POST /api/v1/auth/login` — `{ email, password }` → `accessToken` + refresh cookie
- `POST /api/v1/auth/refresh` · `POST /api/v1/auth/logout` · `GET /api/v1/auth/me`
- `POST /api/v1/auth/forgot-password` — public, `{ email }` → 202 always (no enumeration); emails a 60-minute single-use reset link
- `POST /api/v1/auth/reset-password` — public, `{ token, newPassword }`; revokes all refresh tokens on success
- `POST /api/v1/auth/change-password` — authenticated, `{ currentPassword, newPassword }`; revokes every other session, re-issues the caller's

### User management (SUPER_ADMIN)
- `GET|POST /api/v1/users`
- `GET|PATCH /api/v1/users/{userId}`
- `PATCH /api/v1/users/{userId}/disable`
- `DELETE /api/v1/users/{userId}`
- `GET /api/v1/audit-logs`

### Projects & tasks
- `GET|POST /api/v1/projects` · `GET|PATCH /api/v1/projects/{projectId}`
- `GET|POST /api/v1/projects/{projectId}/tasks`
- `GET|PATCH|DELETE /api/v1/tasks/{taskId}`
- `POST /api/v1/projects/{projectId}/tasks/bulk`

### Boards, releases & sprints
- `GET|POST /api/v1/projects/{projectId}/boards`
- `GET|POST /api/v1/projects/{projectId}/releases`
- `GET|POST /api/v1/projects/{projectId}/sprints`
- `POST /api/v1/sprints/{sprintId}/start` · `/complete`
- `GET /api/v1/sprints/{sprintId}/burndown` — task-count burndown (ACTIVE/COMPLETED sprints)
- `GET /api/v1/projects/{projectId}/reports/velocity` — last N completed sprints' committed/completed counts

### Documentation & SCM
- `GET|POST /api/v1/projects/{projectId}/pages` · `GET|PATCH /api/v1/pages/{pageId}`
- `GET|POST /api/v1/projects/{projectId}/repository-connections`
- `GET /api/v1/repository-connections/{repositoryId}/oauth/authorize` · `POST .../oauth/refresh` · `GET /api/v1/oauth/{provider}/callback`
- `POST /api/v1/tasks/{taskId}/branches/{branchId}/pull-request`
- `GET /api/v1/projects/{projectId}/events` — SSE stream

### Webhooks (HMAC-authenticated, not JWT)
- `POST /api/v1/webhooks/bitbucket` · `POST /api/v1/webhooks/github`

## Domain Rules

1. Project name must not be blank → `422 VALIDATION_ERROR`
2. Task title must not be blank → `422 TASK_TITLE_REQUIRED`
3. DONE/ARCHIVED tasks cannot be modified, except the placement fields `parentId`, `sprintId`, `releaseId` → `422 TASK_IMMUTABLE`
4. Task cannot relate to itself → `422 TASK_SELF_RELATION`
5. Duplicate relations rejected → `422 TASK_RELATION_DUPLICATE`
6. BLOCKS relations must not create cycles → `422 TASK_RELATION_CYCLE`
7. Release cannot close with open tasks → `422 RELEASE_HAS_OPEN_TASKS`
7b. A task's assignee/reviewer must be a project member or a global admin (the set `GET /projects/{id}/assignable-users` returns) → `422 ASSIGNEE_INVALID` / `422 REVIEWER_INVALID`. `null` and `""` both mean "nobody" and clear the field; omitting the field leaves it untouched
8. Page slug must be unique within project → `409 SLUG_CONFLICT`
9. Publishing a page creates a `PageRevision`
10. A DONE task is auto-archived (status → ARCHIVED) 30 days after completion. `tasks.done_at` is stamped on entry to DONE and cleared on any exit; a lazy sweep (`ArchiveStaleDone`, run when tasks are listed — no background job) flips stale DONE tasks to ARCHIVED and writes a `TASK_AUTO_ARCHIVED` activity event. Retention is `workmanagement.DoneTaskRetentionDays` (30).
11. A task cannot move to DONE while a direct child still carries `BLOCKER` priority → `422 TASK_HAS_BLOCKER` (enforced on both `POST /tasks/{id}/status` and the bulk `set_status` action).
