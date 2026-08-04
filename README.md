# Octbase

Project management tool built to replace Jira for task, sprint, and board
work, deployed as one stack per client. Includes its own wiki pages and
integrates with GitHub, GitLab, and Bitbucket (it does not host git).
Fewer features than Jira — every one that ships makes the core workflow
meaningfully faster.

- **Go API** (`octbase-api/`) — JWT auth, PostgreSQL, SSE, webhooks, Prometheus metrics
- **Plain-DOM JS frontends** (`octbase-frontend/`, `octbase-mobile/`) — no framework, no JSX, no client state library; **ES modules built by Vite** from an npm workspace
- **Architecture decisions** — `docs/architecture.md` (normative)
- **Operations runbook** — `docs/operations.md`

## Repository layout

| Path | Purpose |
|---|---|
| `octbase-api/` | Go backend, OpenAPI spec, migrations, tests |
| `octbase-frontend/` | Desktop plain-DOM SPA (ES modules, built by Vite) + Caddy front door — reverse-proxies `/api` and serves the mobile SPA under `/m/` |
| `octbase-mobile/` | Phone-first plain-DOM SPA (ES modules, built by Vite), served under `/m/` by the frontend |
| `octbase-shared/` | `@octbase/shared` — the npm workspace package both SPAs import: i18n loader, task meta, rich-text sanitizer. One copy, no sync |
| `octbase-operations/` | Health observation: `check-health.sh` stack probe + reaction runbook |
| `package.json`, `package-lock.json` | npm workspace root (`octbase-shared`, `octbase-frontend`, `octbase-mobile`) — the frontend build and its pinned toolchain. Node ≥ 22 |
| `testdata/` | Case tables read by **both** test suites — `url-guard-cases.json` pins the Go/JS parity of the URL guards |
| `scripts/` | Repo tooling: the frontend CI guards (`check-innerhtml.mjs`, `check-tdz.mjs`, `check-metrics-not-proxied.sh`), the Vite classic-asset hasher, DB reset, end-to-end agile API scenario, git hooks + security sweep (`git-hooks/`, `security-sweep.sh`, `security-heavy.sh`) |
| `podman-compose.yml` | Full stack: PostgreSQL + API + frontend + mobile (deployable — no dev tooling) |
| `podman-compose.dev.yml` | Dev-only overlay: adds Mailpit mail capture (never deploy) |
| `.env.example` | All supported environment variables with defaults |
| `docs/architecture.md` | Normative architecture decisions (style, concurrency model, scaling stance) |
| `docs/operations.md` | Production runbook |
| `docs/hosting-concept.md` | Deployment topology, sizing, multi-client scaling models |
| `docs/technical_documentation.md` | Whole-stack technology reference (services, containers, networking, DNS/TLS) |
| `docs/` (rest) | Business plan, growth/solo-founder scenarios, security audit, Go/style references |
| `.github/workflows/ci.yml` | CI: lint, test (coverage floor), frontend checks, e2e, security scan → build image (Trivy) |

The public marketing/landing page is a **separate website** in its own
repository (`ocete.ch`); it is not part of this repo.

---

## Quick start

### Option 1 — Podman Compose (recommended)

```bash
cp .env.example .env
```

**`.env.example` is a production template, and copying it is not enough to
bring a stack up.** It ships `OCTBASE_DEMO_MODE=false`, a placeholder
`OCTBASE_JWT_SECRET` and `OCTBASE_SECURE_COOKIES=false`; outside demo mode the
API refuses to start without a ≥ 32-byte secret, `OCTBASE_SECURE_COOKIES=true`
and `OCTBASE_APP_URL` (`cmd/octbase-api/main.go`). Left as copied, the API
container exits at startup and crash-loops. Edit `.env` for one of the two
cases before going further:

*A local/demo stack* — seeds the demo accounts below and permits the dev JWT
fallback. Never deploy it:

```ini
OCTBASE_DEMO_MODE=true
```

*A real deployment* — no accounts are seeded, so provision the first
administrator too (`OCTBASE_BOOTSTRAP_ADMIN_EMAIL` + `_PASSWORD_HASH`):

```ini
POSTGRES_PASSWORD=…      # openssl rand -base64 24
OCTBASE_JWT_SECRET=…     # openssl rand -base64 32
OCTBASE_SECURE_COOKIES=true
OCTBASE_APP_URL=https://your.host
```

Then:

```bash
podman-compose up --build
```

`.env` controls the host ports (`POSTGRES_PORT`, `API_PORT`, `FRONTEND_PORT`) and the
Postgres credentials/database name (`POSTGRES_DB`, `POSTGRES_USER`, `POSTGRES_PASSWORD`)
used by `podman-compose.yml`.

| URL | What |
|---|---|
| http://localhost:8080/ | App |
| http://localhost:8000/api/v1/health | API health (DB pool + migration version) |
| http://localhost:8000/docs | OpenAPI UI |
| http://localhost:8000/metrics | Prometheus metrics |

**First login:** with `OCTBASE_DEMO_MODE=true` the API seeds two users on
startup. A stack that left demo mode off has neither; its first account is
whatever `OCTBASE_BOOTSTRAP_ADMIN_EMAIL` provisioned.

| Email | Password | Role |
|---|---|---|
| `super@octbase.dev` | `superpass1234` | SUPER_ADMIN — full platform access, admin panel, audit logs |
| `demo@octbase.dev` | `demopass1234` | ADMIN — manages projects and members |

Log in at http://localhost:8080/. The **admin panel** (`/#/admin`) and **audit log** (`/#/admin/audit-logs`) are only visible to SUPER_ADMIN users.

To add more users, use the invitation flow — the inviter generates a link and
the invitee sets their own password. A SUPER_ADMIN or ADMIN may invite anyone;
a user who is PROJECT_OWNER or PROJECT_ADMIN of a project may invite to *that*
project (`project.invite_users`, `internal/rbac`), which is why the route is
not behind the admin guard despite its `/admin/` path:

```bash
TOKEN=$(curl -s -X POST http://localhost:8000/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"super@octbase.dev","password":"superpass1234"}' \
  | python3 -c "import json,sys; print(json.load(sys.stdin)['accessToken'])")

curl -X POST http://localhost:8000/api/v1/admin/invitations \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"email":"teammate@example.com","projectId":"<project-uuid>","role":"PROJECT_MEMBER"}'
# `role` is a per-project role, applied to `projectId` on accept. Omit both to
# invite without any project membership — SUPER_ADMIN/ADMIN only, since a
# project admin's permission to invite is scoped to their project. Accepted
# accounts always get global role USER.
# Returns { "acceptURL": "http://localhost:8080/#/invitations/<token>/accept" }
# Open acceptURL in a browser; the invitee enters a name and password.
```

### Option 2 — Go directly

```bash
# Start PostgreSQL
podman run -d --name octbase-pg \
  -e POSTGRES_DB=octbase -e POSTGRES_USER=octbase -e POSTGRES_PASSWORD=octbase \
  -p 5432:5432 registry.access.redhat.com/hi/postgresql:18
# The tag is fine for a throwaway local database. `podman-compose.yml`
# deliberately pins the same image by digest instead — see it for the pin that
# actually ships.

# Run the API — migrations run automatically at startup
cd octbase-api
OCTBASE_DATABASE_URL="postgres://octbase:octbase@localhost:5432/octbase?sslmode=disable" \
OCTBASE_JWT_SECRET="dev-only-change-in-production" \
OCTBASE_DEMO_MODE=true \
go run ./cmd/octbase-api
```

Build and serve the frontend. A static file server pointed at the source tree
does not work: `index.html` loads one ES module entry whose imports resolve
through the npm workspace (`@octbase/shared`), so the app has to be built first.
Node ≥ 22 required.

```bash
npm ci                                        # once, from the repository root
npm run build --workspace @octbase/frontend   # → octbase-frontend/dist/
npm run preview --workspace @octbase/frontend # serves dist/ on :4173
```

Then open `http://localhost:4173/`. Sign in with `super@octbase.dev` / `superpass1234` (requires `OCTBASE_DEMO_MODE=true`).

`vite preview` reverse-proxies `/api` to `http://127.0.0.1:8000` so the previewed
app is **same-origin with its API**, exactly as the deployed Caddy front door
makes it — that is not a convenience. The session lives in an HttpOnly refresh
cookie, and a cross-origin frontend never gets it back, so every page reload
lands on the login screen. Point it at a stack on another port with
`OCTBASE_API_ORIGIN`. Substitute `@octbase/mobile` (port 4174) for the phone SPA.

`npm run dev --workspace @octbase/frontend` starts the Vite dev server with
hot reload, but it has **no** `/api` proxy — use it for markup and styling work,
not for anything that exercises a real session.

---

## Environment variables

See `.env.example` for the full list with types and defaults.

| Variable | Prod-required | Default | Description |
|---|---|---|---|
| `OCTBASE_DATABASE_URL` | yes | `postgres://...localhost...` | PostgreSQL DSN |
| `OCTBASE_JWT_SECRET` | **yes** | dev placeholder | 32+ random bytes. Rotating this logs out all users. |
| `OCTBASE_JWT_ACCESS_TTL` | no | `15m` | Access token lifetime |
| `OCTBASE_JWT_REFRESH_TTL` | no | `1h` | Refresh-token / sliding session lifetime (rotated on each use) — server-side backstop for the 60-minute idle timeout the frontend also enforces |
| `OCTBASE_CORS_ORIGIN` | yes | `http://localhost:8080` | Allowed CORS origin |
| `OCTBASE_SMTP_HOST` | no | *(empty)* | SMTP host — unset logs emails to stdout |
| `OCTBASE_SMTP_PORT` | no | `587` | SMTP port |
| `OCTBASE_SMTP_FROM` | no | `noreply@beyags.com` | Sender address |
| `OCTBASE_SMTP_USER` / `_PASS` | no | — | SMTP credentials |
| `OCTBASE_WEBHOOK_SECRET_BITBUCKET` | no | — | HMAC secret for Bitbucket webhooks |
| `OCTBASE_WEBHOOK_SECRET_GITHUB` | no | — | HMAC secret for GitHub webhooks |
| `OCTBASE_SCM_ENC_KEY` | for SCM | — | 32-byte key (base64/hex) encrypting stored SCM access tokens — required before any repository connection can be saved |
| `OCTBASE_MFA_ENC_KEY` | for MFA | — | 32-byte key (base64/hex) encrypting users' TOTP secrets at rest — required before any user can enroll in MFA; deliberately separate from `OCTBASE_SCM_ENC_KEY` |
| `OCTBASE_REQUIRE_MFA` | no | `off` | MFA enforcement scope: `off` / `admins` (ADMIN + SUPER_ADMIN) / `all` — an in-scope login without MFA gets a scoped enrollment challenge instead of a session |
| `OCTBASE_AUDIT_RETENTION_DAYS` / `_ACTIVITY_RETENTION_DAYS` | no | `365` | GDPR storage-limitation purge windows (days); `0` disables the purge |
| `OCTBASE_FEATURE_TASKVIEW` | no | `true` | Toggles the Task view SPA feature, exposed to the frontend via `/api/v1/config` |
| `OCTBASE_EDITION` | no | `ENTERPRISE` | Deployment edition — `TEAM` / `BUSINESS` / `ENTERPRISE` (case-insensitive; missing/invalid falls back to `ENTERPRISE`). Gates optional product surface per client; exposed via `/api/v1/config` |
| `OCTBASE_OPTION_JIRA_IMPORT` | no | `false` | Additional bookable option: activates Jira CSV import in the `BUSINESS` edition (ignored — with a warning — on `TEAM`; `ENTERPRISE` always includes it) |
| `OCTBASE_DB_MAX_OPEN_CONNS` / `_MAX_IDLE_CONNS` | no | `25` / `5` | DB connection pool sizing per API instance — see `docs/hosting-concept.md` §4 |
| `OCTBASE_OAUTH_<PROVIDER>_CLIENT_ID` / `_CLIENT_SECRET` | no | — | OAuth app credentials per provider (`GITHUB`/`GITLAB`/`BITBUCKET`); enables "Connect with OAuth". Needs `OCTBASE_OAUTH_REDIRECT_BASE` |
| `OCTBASE_APP_URL` | no | `http://localhost:8080` | Base URL used in invitation emails |
| `OCTBASE_ATTACHMENTS_DIR` | no | `/data/attachments` | Filesystem directory for uploaded task attachments — uploads are disabled if it can't be created |
| `OCTBASE_MAX_UPLOAD_MB` | no | `10` | Max size (MiB) of a single uploaded attachment; `0` disables the limit |
| `OCTBASE_MAX_USER_STORAGE_MB` | no | `512` | Total stored attachment size (MiB) allowed per user; `0` disables the quota |
| `OCTBASE_MAX_USERS` | no | `5` | Installation-wide account limit incl. the admin (`403 USER_LIMIT_REACHED`); `0` disables the limit |
| `OCTBASE_APP_VERSION` | no | `beta` (build default) | Version string surfaced at `/health`, `/api/v1/version`, `/api/v1/config` and the app's version tag; stamp per deployment |
| `OCTBASE_TRUSTED_PROXIES` | yes (behind a proxy) | *(empty)* | Comma-separated proxy IPs/CIDRs whose `X-Forwarded-For` is honored for rate limiting and audit IPs; empty ignores forwarding headers |
| `OCTBASE_FRONTEND_TRUSTED_PROXIES` | with the above, bundled stack | *(empty)* | Frontend-Caddy companion to `OCTBASE_TRUSTED_PROXIES`: proxy addresses whose `X-Forwarded-For` the front door preserves instead of overwriting. Behind the bundled front door **both** must be set (use `private_ranges`), or the API ignores the forwarded chain and rate-limits the whole installation as one bucket. Set **only** when `FRONTEND_BIND_ADDR=127.0.0.1` and an on-host edge proxy is the sole way in — see `.env.example` |
| `OCTBASE_SITE_AUTH` | no | *(empty)* | Installation-password on/off switch (frontend container). Set to `on` **together with** `OCTBASE_SITE_PASSWORD_HASH` to lock the whole browser-facing app (desktop, mobile `/m/`, docs) behind a front-door HTTP Basic Auth prompt. Empty = front door open. Excludes `/api/*` and `/health`, so the API keeps its JWT auth and webhooks/SSE/health keep working |
| `OCTBASE_SITE_PASSWORD_HASH` | no | *(empty)* | **bcrypt hash** of the installation password (from `caddy hash-password`), used when `OCTBASE_SITE_AUTH=on`. Frontend container; the shell-less Caddy image can't hash a plaintext at runtime |
| `OCTBASE_SITE_USER` | no | `octbase` | Username shown in the Basic Auth prompt when the gate is on |
| `OCTBASE_DEMO_MODE` | no | `false` | Seeds demo users on startup |
| `OCTBASE_BOOTSTRAP_ADMIN_EMAIL` | no | — | Login of the first `SUPER_ADMIN`, created on startup while the users table is empty; ignored once the installation has users |
| `OCTBASE_BOOTSTRAP_ADMIN_PASSWORD_HASH` | no | — | Its initial password as a **bcrypt hash** (never cleartext; a cleartext value is rejected at startup). Required with, and only with, `OCTBASE_BOOTSTRAP_ADMIN_EMAIL` |
| `OCTBASE_SECURE_COOKIES` | yes (prod) | `false` | Set `true` in production so the refresh cookie carries the `Secure` flag. Enforced: outside demo mode the API refuses to start unless this is `true` (and `OCTBASE_APP_URL` is set), so a forgotten flag fails closed |
| `OCTBASE_DB_STATEMENT_TIMEOUT` | no | `30s` | Server-side `statement_timeout` on the request pool (`0` disables) — cancels a stuck query instead of letting it pin its pooled connection until the API deadlocks. Not applied to the migration connection |
| `OCTBASE_MIGRATE_DATABASE_URL` | no | — | Owner-role connection used only for migrations when the runtime role is a restricted DML-only role (`scripts/db-least-privilege.sql`); unset keeps the legacy single-URL mode |
| `OCTBASE_LOG_LEVEL` | no | `info` | `debug` / `info` / `warn` / `error` |
| `PORT` | no | `8000` | HTTP listen port |

---

## Landing page

The public, static marketing/landing site for Octbase is a **separate website**
maintained in its own repository (`ocete.ch`) — it has no API/database dependency
and is not part of this repo or its `podman-compose.yml`. See that repo for
its build, environment variables, and contact-form mailer.

All services in this `podman-compose.yml` set `restart: always`. To have Podman
restart them after a host reboot, see "Start on boot" in `docs/operations.md`
(enable `podman-restart.service`, plus `loginctl enable-linger` for rootless
setups).

---

## Features

### Identity & Access

- Email + password with JWT — 15-min access tokens, HttpOnly refresh cookies (60-minute sliding session) with rotation on every use
- Invitation-only registration — admins send a link, users set their own password
- **Global roles:** SUPER_ADMIN · ADMIN · USER · GUEST — enforced platform-wide
- **Project roles:** PROJECT_OWNER · PROJECT_ADMIN · PROJECT_MEMBER · PROJECT_VIEWER — enforced per project via a permission matrix (`GET /api/v1/projects/{id}/permissions`); every project always has at least one OWNER, and only an OWNER can grant/revoke the OWNER role or remove the last owner
- Disabled accounts are blocked at the token-validation layer (no endpoint can be reached)
- `Provider` interface — swap the email/password backend for SAML/OIDC without touching callers

### Personal settings & MFA

- **Settings page** (topbar user icon, desktop and mobile) — set preferred **language** and **theme** (`system`/`light`/`dark`/`octopus`), persisted server-side via `GET`/`PATCH /api/v1/users/me/preferences` (`internal/dashboard`) and reconciled with the local cache on login
- **Vocabulary** — the same Settings page switches the interface between **Agile** (default) and **Classic project management**: sprint → phase, backlog → task pool, epic → work package, story → requirement, story points → effort points, release → milestone. A per-user preference (`terminology` on `GET`/`PATCH /users/me/preferences`, default `AGILE`), so two people on one project can each read it their own way. **Labels only** — nothing is converted and no API field is renamed (`sprintId`, `storyPoints` stay), so integrations and exports are unaffected
- **Profile pictures** — upload, replace or remove your own avatar (`POST`/`DELETE /users/me/avatar`, served from `GET /users/{id}/avatar`); an initials avatar is the fallback while it loads or when a user has none
- **TOTP-based MFA** (`internal/security/mfa`) — enroll via scannable QR code or manual setup key (`POST /api/v1/users/me/mfa/enroll`), confirm and receive one-time recovery codes (`.../confirm`), disable or regenerate codes (`.../disable`, `.../recovery-codes/regenerate`) — always requiring re-proof of identity (password or a valid TOTP/recovery code)
- **Stateless login challenge** — when MFA is enabled, `POST /api/v1/auth/login` returns a short-lived `challengeToken` instead of real tokens; `POST /api/v1/auth/mfa/verify` exchanges it plus a TOTP/recovery code for the actual session
- MFA enrollment/management is desktop **and** mobile; the login MFA challenge is part of the shared login flow on both

### Admin panel (`/#/admin`)

- **User management** — list, create, edit global role and status, disable, delete users (SUPER_ADMIN only)
- **Audit log** (`/#/admin/audit-logs`) — immutable record of all privileged actions (SUPER_ADMIN only)
- **Invitations** — generate accept links for new users
- SUPER_ADMIN sees all projects in the system, regardless of membership
- Disabling a user immediately invalidates all their refresh tokens

### Projects & planning concepts

- **Releases** — long-horizon targets that summarise feature sets for a product release; ship a release once all tasks are DONE/ARCHIVED
- **Sprints** — time-boxed iterations (1–2 weeks) used to execute toward a release step by step; only one ACTIVE sprint per project. Each sprint gets its own **sprint board** on creation: while `PLANNED` you drag tasks from the backlog onto it to commit the scope; starting the sprint (`/start`) **locks the scope** so no new tasks can be added to a running sprint (tasks already in scope still move between lanes); completing it (`/complete`) snapshots the scope as *done/committed* (e.g. `2/5`) and moves unfinished tasks back to the backlog. **Sprint scope is a task's `sprintId`**, set either by dragging it onto the sprint board or directly from the task detail — both count, and a task committed from the task detail simply has no card yet. The scope lock applies to both routes: once the sprint is ACTIVE, creating or PATCHing a task into it is refused with `SPRINT_SCOPE_LOCKED` just as a board move is (tasks already in scope still move between lanes, and may still leave). An unknown or cross-project sprint id is refused with `SPRINT_NOT_FOUND`
- Create, edit (name / description / visibility), and delete projects
- Delete cascades all dependent entities in a transaction — tasks, boards, releases, pages, members, activity, and SCM references
- Archive projects to hide them from active lists without deleting data
- PROJECT_OWNER or PROJECT_ADMIN required to delete or archive; PROJECT_MEMBER (or higher) required to edit; only an OWNER can transfer ownership or change member roles to/from OWNER

### Task management

- Board (Kanban), Backlog, Task list, and Mindmap views with shared filter state
- **Mindmap** — renders a project's open tasks as a left-to-right epic → story → task → subtask tree, nested by each task's `parentId`; parentless stories and tasks are collected under synthetic branches
- Project-scoped sequence numbers (`ED-42` format) — the prefix is the project's configurable **abbreviation** (auto-derived from the name, editable to ≤ 10 chars), and the number is assigned atomically on creation
- Task fields: type (`EPIC` · `STORY` · `TASK` · `SUBTASK`, plus opt-in `INITIATIVE` · `THEME`), status, priority (`LOW` · `MEDIUM` · `HIGH` · `CRITICAL` · `BLOCKER`, plus per-project custom values), parent, assignee, reporter, reviewer, release, sprint, due date, and — where the project estimates — `storyPoints` or `estimateHours`
- **Task hierarchy** — a nullable `parentId` links a task to a parent exactly one type level up in the project's active chain (up to `THEME → INITIATIVE → EPIC → STORY → TASK → SUBTASK`): mandatory for a `SUBTASK`, optional everywhere else, never allowed on the chain's top type. Enforced on create and PATCH with stable codes (`TASK_PARENT_REQUIRED`, `TASK_PARENT_NOT_ALLOWED`, `TASK_PARENT_INVALID`, `TASK_PARENT_TYPE_INVALID`); a type change that would strand existing children, or deleting a task that still has children, answers `422 TASK_HAS_CHILDREN`; moving a task to `DONE` while a direct child still carries `BLOCKER` priority answers `422 TASK_HAS_BLOCKER` (on both `POST /tasks/{id}/status` and the bulk `set_status` action). `GET /projects/{id}/tasks` accepts `parentId`, `releaseId`, `sprintId`, `taskType`/`type`, `status`, `assigneeId` and `priority` filters (an unknown `status` answers `422 INVALID_STATUS`) and reports the unpaginated match count in an `X-Total-Count` header; copies keep their parent, and the project export/import round-trips the hierarchy
- **Project task settings** (gear menu → *Task types & priorities*, project admins only) — per-project `themeEnabled`/`initiativeEnabled` flags switch the `THEME` and `INITIATIVE` hierarchy levels on (`PATCH /projects/{id}`; the parent of a type is the level directly above it in the *enabled* chain, so theme → epic while initiatives are off). Creating a task or template of a disabled type answers `422 TASK_TYPE_DISABLED`; switching a level off while tasks/templates of that type exist answers `422 TASK_TYPE_IN_USE`. Admins can also add **custom priorities** on top of the built-in set (`POST/GET /projects/{id}/task-priorities`, `DELETE /task-priorities/{id}`; `INVALID_PRIORITY_NAME` / `PRIORITY_RESERVED` / `409 PRIORITY_EXISTS` / `422 PRIORITY_IN_USE`) — accepted wherever built-in priorities are, and exported/imported with the project
- **Effort estimation** — a project's `estimationUnit` setting (`NONE` | `POINTS` | `HOURS`) is **`NONE` by default**, and while it is, nothing about estimates appears anywhere in that project's UI. With a unit on, leaf tasks carry `storyPoints` (integer 0–100) or `estimateHours` (decimal 0–1000, ≤ 2 places) through the ordinary version-guarded `PATCH /tasks/{id}`; `null` means *unestimated* and is deliberately distinct from a `0`. Switching the unit is owner/admin-only and non-destructive — the estimate stored in the other unit stays and reappears if you switch back. Codes: `ESTIMATION_UNIT_INVALID`, `ESTIMATION_UNIT_INACTIVE`, `STORY_POINTS_INVALID`, `ESTIMATE_HOURS_INVALID`, `ESTIMATION_NOT_ALLOWED_FOR_TYPE`
- **Board lane paging** — a project's `boardLaneLimit` setting caps how many cards a board lane draws at once (**20** by default; `0` draws every card), the rest loading as the reader scrolls or via a **Load N more cards** button at the lane foot. It is a rendering cap only: no task leaves its lane and the lane's count badge still reports the full size, so a capped lane is never mistaken for missing cards. Owner/admin-only, applies to the backlog and mirrored external columns too, and travels with a project export. Code: `BOARD_LANE_LIMIT_INVALID`
- Default statuses (`PLANNED` · `IN_PROGRESS` · `IN_REVIEW` · `DONE` · `ARCHIVED`); custom board lanes also act as task statuses (see **Configurable boards**)
- Inline task creation — press `N` on the board, type a title, press Enter; no modal
- Copy a task (`POST /tasks/{id}/copy`), archive and reopen, AsciiDoc/rich-text description
- **Auto-archive** — a DONE task moves to `ARCHIVED` 30 days after completion (`done_at`), via a lazy sweep on task listing (no background job); archived tasks live in the **Archive** view and stay reopenable, and the sweep logs a `TASK_AUTO_ARCHIVED` activity event
- **Comments** with `@mention` — scans member names and creates in-app notifications; edit/delete own comments; **threaded replies** (a comment can reply to another; deleting a parent removes its reply subtree)
- **Task relations** — `RELATES_TO` · `BLOCKS` · `BLOCKED_BY` · `DUPLICATES` between tasks
- **Links** (external URLs) and **attachments** (file uploads) per task
- **Task categories** — per-project labels for grouping/filtering tasks
- **Task templates** — reusable task definitions that can be instantiated into new tasks; the type is validated and may not be `SUBTASK` (instantiation has no parent to offer)
- Bulk actions (`POST /projects/{id}/tasks/bulk`) — select tasks with checkboxes, then set status / priority / assignee / release, or archive / delete them, in one request

### Reports & statistics

- **Project statistics page** (`/projects/:id/statistics`, chart icon in the topbar left of the settings gear; `GET /projects/{id}/reports/statistics`) — open / in progress / finished-in-30-days / overdue / unassigned counts, the status, type and open-work-by-priority distributions, weekly throughput over the last 8 weeks, average **and** median cycle time from creation to done, open work per assignee, the active sprint, and the release plan. It is deliberately not a sidebar entry: the sidebar lists the places work is done, this is a view onto the project as a whole
- **Sprint burndown** (`GET /sprints/{id}/burndown`) and **velocity** over the last N completed sprints (`GET /projects/{id}/reports/velocity`, default 6, cap 20)
- Where a project estimates effort, the statistics page and the burndown measure the **estimate instead of the ticket count**, and additionally report remaining/done effort and how many tasks are unestimated

### Configurable boards

- Each project can have multiple boards; one is the default (`GET /projects/{id}/boards/default`)
- Boards own ordered **columns** — create, rename, reorder, and delete lanes (`/boards/{id}/columns`); a custom lane name becomes a usable task status
- Drag tasks between columns (`POST /boards/{id}/move-task`) or off the board (`remove-task`); board rank persists ordering within a lane
- **Pin tasks** (`POST /tasks/{id}/pin`) to float a card to the top of its lane — allowed even on DONE/ARCHIVED tasks
- **External columns** map a board lane to an SCM state (e.g. PRs), so merged work surfaces automatically
- **Sprint boards** — a board provisioned for a sprint (lanes copied from the project's default board, or a Scrum template as fallback). It lives from sprint creation until completion; dragging a task onto it enrolls the task in the sprint, and moving onto a running sprint's board is rejected (`SPRINT_SCOPE_LOCKED`). Open it from the **Plan board** / **Open board** button on the sprint card; the board's banner links back to the sprints list

### Real-time

- Server-Sent Events per project — every workmanagement mutation broadcasts a project-scoped `board.changed` event within ~1 second to all viewers
- **Change banner instead of auto-repaint** — a view showing live project data (board, backlog, tasks, the open task panel) raises a "This content has changed" banner with a Reload button; nothing redraws under the reader, and your own changes never raise it
- Reconnects with exponential backoff (1 s → 2 s → 4 s → max 30 s)
- Presence endpoint: which users are currently connected to a project's event stream

### Documentation (wiki pages)

- AsciiDoc editor with split-pane live preview (`POST /pages/{id}/render-preview`, debounced 300 ms) and a built-in syntax cheatsheet
- Supported AsciiDoc: section titles (`=`…`======`), bold `*…*`, italic `_…_`, monospace `` `…` ``, links (`https://…[label]`, `link:…[]`) and bare URLs, unordered/ordered + nested lists, listing/`[source,lang]` code blocks, block quotes (`____`), admonitions (`NOTE/TIP/WARNING/IMPORTANT/CAUTION`), tables (`|===`), and relative block images (`image::/path[alt]`). Passthrough/raw-HTML and external image sources are deliberately unsupported; rendered HTML is allowlist-sanitized server-side, so pages are XSS-safe regardless of authored or imported content.
- Page lifecycle: `DRAFT` → `PUBLISHED` → `ARCHIVED` (`/pages/{id}/publish`, `/pages/{id}/archive`)
- Auto-generated sticky TOC sidebar on read view (shown when ≥ 3 headings)
- Full revision history per page (`GET /pages/{id}/revisions`)
- **Task cross-references** — `TASK-<uuid>` mentions in a page render as links back to the task; references are extracted on save and can be rebuilt (`/pages/{id}/references`, `/references/rebuild`)
- Per-project full-text page search (`GET /projects/{id}/search/pages`)

### SCM integration

- **Repository connections** per project (`/projects/{id}/repository-connections`) for GitHub, Bitbucket, and a `FAKE_GITLAB` test provider
- **Authentication** by personal access token **or OAuth** (authorize → provider callback → refresh); access/refresh tokens are **encrypted at rest** (`shared/crypto`) and never serialised in API responses
- Bitbucket and GitHub webhook receivers, HMAC-SHA256 validated
- PR status (`open` / `merged` / `declined`) on the task panel with a direct link
- **Create a pull/merge request** from a task's branch through the connected provider (`POST /tasks/{id}/branches/{branchId}/pull-request`)
- Auto-close task on PR merge — opt-in per repository connection (`auto_close_on_merge`)
- **Branch references** per task (`/tasks/{id}/branches`) with name suggestions (`feature` · `bugfix` · `hotfix` · `release`), e.g. `feature/ed-42-improve-search`

### Data import & export

- **Jira CSV import** — per project (`POST /projects/{id}/import/jira-csv`); maps status, priority, type (Jira Bug/Sub-task become `TASK`), assignee by email; stores the Jira issue key in `external_ref`; imports repeating `Comment` and `Attachment` columns (attachments become URL-backed links to the original Jira URLs — bytes are never fetched). The response is an import report: counts plus per-row warnings (`{row, issueKey, message}`, capped at 200) for unknown statuses/priorities, unmapped users, and malformed cells. Edition-gated: included in `ENTERPRISE`, a bookable add-on in `BUSINESS` (`OCTBASE_OPTION_JIRA_IMPORT=true`), never available on `TEAM` — when inactive the project-menu entry is hidden and the endpoint answers `403 FEATURE_DISABLED`
- **Jira CSV export** — `GET /projects/{id}/export/jira-csv` round-trips a project's tasks, comments, and URL-backed attachments (`SUBTASK` exports as Jira `Sub-task`)
- **Whole-project ZIP export/import** — `GET /projects/{id}/export` bundles a project (tasks, pages, attachments, plus the releases, sprints, boards, categories and templates the tasks are planned in, and each task's placement in them) into a ZIP; import it into an existing project (`POST /projects/{id}/import`) or as a new project (`POST /projects/import`). Memberships, SCM connections and the activity log are deliberately excluded. `formatVersion` stays 1 — every optional section is optional in both directions
- Imports support `dryRun=true` — preview counts without writing to the database

> There are no platform-wide admin importers (`/admin/import/jira`,
> `/admin/import/confluence` do not exist, for security reasons). For Jira,
> use the per-project import above; there is no Confluence import.

### Notifications

- In-app bell with unread count (polled every 60 s or pushed via SSE)
- Email delivery via SMTP; logs to stdout when `OCTBASE_SMTP_HOST` is unset
- Per-user per-kind preferences (in-app / email toggle)
- Triggers: task assigned, reviewer set, @mentioned, status changed (notifies reporter), release due within 3 days

### Navigation & UX

- `Ctrl+K` / `Cmd+K` command palette — search tasks, pages, and projects in one box with `↑↓ Enter` keyboard navigation
- Personal dashboard ("My Work") — assigned tasks, in-review tasks, recent pages, upcoming releases; this is the landing page after login
- Bookmarkable, filter-preserving URLs — board filters and open task panel state are encoded in the hash (`#/projects/ID/board?priority=HIGH&task=TASK_ID`)
- Full keyboard shortcut set — `N` new task, `B` board, `L` backlog, `S` sprints, `R` releases, `P` pages, `E` edit title, `A` assign to me, `?` shortcut help

### Performance & ops

- Prometheus metrics at `/metrics` — request count/latency by route, SSE connection gauge
- Deep health check at `/api/v1/health` — DB pool stats + migration version; returns 503 when degraded
- Graceful shutdown — 30-second drain on SIGTERM
- Connection pool: 25 max open / 5 idle / 5-minute lifetime (sized for Postgres default of 100 connections)
- Structured JSON logging with `OCTBASE_LOG_LEVEL`
- Rate limiting per IP, applied to two route groups rather than by path prefix:
  the **public** auth routes (`login`, `mfa/verify`, `refresh`, `logout`,
  `forgot-password`, `reset-password`) **and both `invitations/{token}` routes**
  share one 120 req/min budget; `/api/v1/users` — 60 req/min. The authenticated
  `auth/me` and `auth/change-password` are in neither bucket

---

## Testing

### Git hooks — automated security checks

Security and integrity checks run automatically on `git commit` and `git push`,
in two tiers so commits stay fast. Hooks live in `scripts/git-hooks/` and are
activated per clone by `scripts/setup-git.sh` (sets `core.hooksPath`); run it
once after cloning.

| Hook | Script | Runs | Speed |
|---|---|---|---|
| `pre-commit` | `scripts/security-sweep.sh` | Deterministic, no-stack security sweep: frontend integrity guards (innerHTML, module TDZ, JS syntax, no local re-implementation of a shared module, and vendored-dependency integrity) + backend/frontend regression greps (no `math/rand`/`InsecureSkipVerify`/`os-exec`/unescaped `X-Forwarded-For`/SVG-upload/`eval`/inline `<script>`/token-in-URL/secrets-in-storage) + `gofmt`. Ambiguous patterns are printed as non-blocking warnings. (Vite content-hashes asset filenames, so no restamping happens here.) | ~1–2s |
| `pre-push` | `scripts/security-heavy.sh` | `go vet` (always), then `golangci-lint`, `govulncheck`, and `go test` — each warn-skips if the tool or `TEST_DATABASE_URL` is missing (CI is the hard gate). | ~30–90s |

Both scripts are runnable by hand. Override a hook in a pinch with
`git commit --no-verify` / `git push --no-verify` (discouraged). These mirror the
CI guards for fast local feedback; they are **not** the full security review —
the deep, LLM-driven assessment lives in `prompts/06_security-assessment.md` and is
run manually (e.g. pre-release), never as a hook.

### Go tests

```bash
cd octbase-api
TEST_DATABASE_URL="postgres://octbase:octbase@localhost:5432/octbase?sslmode=disable" \
go test ./...
```

Each test creates and drops its own schema — fully isolated, safe to run against a shared dev database. Tests skip cleanly when `TEST_DATABASE_URL` is unset.

Run a single package or test:

```bash
TEST_DATABASE_URL="..." go test ./internal/auth -run TestLogin_ValidCredentials -v
TEST_DATABASE_URL="..." go test ./internal/workmanagement -run TestBulkTasks -v
```

Key coverage: bcrypt round-trip · JWT issue/validate/expiry · login OK · wrong password → 401 · deactivated user → 401 · invitation flow · PROJECT_VIEWER write → 403 · RBAC global-role checks and permission matrix (`HasPermission`, `CanAssignRole`, `CanChangeRole`, `WouldRemoveLastOwner`) · last-owner protection on role changes/removal → 422 `LAST_OWNER` · bulk actions · unified search · dashboard · task sequence numbers.

### Lint

```bash
cd octbase-api && golangci-lint run ./...
```

The CI pipeline (`.github/workflows/ci.yml`) blocks the merge if lint reports any issue.

### Frontend checks

Both SPAs are ES modules built by Vite, so the build itself is the first check:
an import of a name nothing exports fails it. The rest are guards for what a
valid module graph still allows. This mirrors the "Frontend checks" CI job —
`.github/workflows/ci.yml` is the authority if the two ever disagree.

```bash
npm ci                              # once, from the repository root

npx eslint .                        # parses everything, plus no-undef / no-unused-vars
npm run types:generate              # regenerate the API types from openapi.yaml…
git diff --exit-code -- octbase-frontend/types/openapi.d.ts   # …and fail if stale
npm run typecheck                   # tsc over the // @ts-check allowlist
npm run build                       # both SPAs, site + standalone bundles
npm run test:unit                   # JS unit layer (Vitest)
npm audit --omit=dev --audit-level=low        # advisories in the deps that reach a browser

# HTML-injection guard — its self-test runs first and gates, because a rule
# weakened by a refactor still lets the guard itself exit 0
node --test scripts/test-check-innerhtml.mjs
node scripts/check-innerhtml.mjs
# Top-level read of a not-yet-evaluated binding across an import cycle
node scripts/check-tdz.mjs
# /metrics must not be reachable through either Caddy front door
bash scripts/check-metrics-not-proxied.sh
# Translation guards: every Go error code, every audit action, and every t()
# key must exist in both SPAs' locale files — a miss ships a wrong-language
# label that looks right in every screenshot
node scripts/check-error-translations.mjs
node scripts/check-audit-actions.mjs
node scripts/check-i18n-keys.mjs
```

### Frontend end-to-end tests (Playwright + pytest)

The `octbase-frontend/tests/` suite drives the UI with Playwright and verifies state through direct API calls. The backend is JWT-only, so both the browser UI and the test's `ApiClient` sign in as the seeded demo user (`demo@octbase.dev` / `demopass1234`) to obtain a token.

Prerequisites: a live API with `OCTBASE_DEMO_MODE=true` (e.g. via
`podman-compose up -d`), started with `OCTBASE_MFA_ENC_KEY` and
`OCTBASE_ATTACHMENTS_DIR` set — without them the MFA and attachment tests fail
rather than skip, which is why CI sets both on the API it drives — plus a Python
virtualenv with the test deps:

```bash
cd octbase-frontend/tests
python3 -m venv .venv && source .venv/bin/activate
pip install -r requirements.txt
```

#### Browser engine

All browsers run **headless** — pick the engine with `OCTBASE_BROWSER`:

| `OCTBASE_BROWSER` | Engine | Install | When to use |
|---|---|---|---|
| `firefox` (default) | Playwright's bundled Firefox | `python -m playwright install firefox` | Desktop / dev machines |
| `chromium` | Playwright's bundled Chromium | `python -m playwright install chromium` | Headless servers where the bundled Firefox build fails to launch (missing shared libs) |
| `chrome` | System Google Chrome (`channel="chrome"`) | Install Chrome via the OS package manager | Servers without Firefox but with `google-chrome` installed |

```bash
OCTBASE_BROWSER=chromium pytest                  # whole suite
OCTBASE_BROWSER=chromium pytest test_board.py -v # a single file
```

#### Loading the UI: serve the built app over HTTP

**Set `OCTBASE_UI_URL` at the built app.** The desktop suite drives `octbase-frontend/dist/` over HTTP, which is both closer to what a user gets and the only way the suite can see a CORS or CSP regression at all:

```bash
npm ci && npm run build --workspace @octbase/frontend
npm run preview --workspace @octbase/frontend &     # :4173, proxies /api to the API
OCTBASE_UI_URL="http://localhost:4173/index.html?e2e=1" pytest
```

The compose stack's frontend works just as well as the preview server.

> ⚠️ **There is no default, on purpose.** The app is a `<script type="module">` entry importing bare specifiers (`@octbase/shared/…`), and a browser can neither resolve those nor `import` at all from a `file://` origin — so pointing a browser at the source tree (over `file://` **or** via `python3 -m http.server`) produces a blank page. A run with no `OCTBASE_UI_URL` **skips the desktop tests** with a message naming the variable and giving the recipe, and **fails** rather than skips when `CI` is set, so a job that forgets the variable cannot report green on a run that drove nothing.

`file://` is still a real code path — just not this one. The **standalone demo** (`dist-standalone/`) is a self-contained IIFE bundle built for exactly that origin, and the mobile suite loads it from disk on purpose.

`test_accessibility.py` is the exception: its login-page tests need the SPA's real login form, which only renders over `http(s)` (not `file://`), so it hardcodes its own fixtures rather than using `OCTBASE_UI_URL`.

#### Env vars

| Variable | Default | Purpose |
|---|---|---|
| `OCTBASE_API_BASE` | `http://127.0.0.1:8000` | API the UI and `ApiClient` talk to |
| `OCTBASE_UI_URL` | *none — required for the desktop tests* | Where the SPA is loaded from. Point it at the built `dist/` over HTTP, e.g. `http://localhost:4173/index.html?e2e=1`. Unset: desktop tests skip with the recipe (and error under `CI`); `test_mobile.py` and the `no_stack` tests still run |
| `OCTBASE_BROWSER` | `firefox` | Playwright engine: `firefox`, `chromium`, or `chrome` |

The whole suite skips cleanly if the API at `OCTBASE_API_BASE` is unreachable, so it is also safe to run before the compose stack is up.

RBAC tests (`test_rbac.py`) also require `OCTBASE_SUPERADMIN_EMAIL` and `OCTBASE_SUPERADMIN_PASSWORD` env vars to run.

#### Known limitation: login rate limiting

Every test that uses the `app`/`demo_board`/`task_panel` fixtures opens a fresh browser context and signs in through the UI login form (the JWT is kept in memory only, not in `localStorage`, so it can't be reused across contexts). The API gives the public auth routes one shared budget of **120 requests/minute per IP** (`internal/shared/ratelimit.go`), and every login spends from it. Running a large number of e2e tests back to back can exceed that limit, which makes the UI show "Invalid email or password" (masking a `429 RATE_LIMITED`) and causes the `app` fixture's `wait_for_selector("text=Demo Project")` to time out — a flaky failure unrelated to browser/headless setup.

Mitigations:
- Run files individually or with `-k` to keep a single run's login count under the per-minute limit (e.g. `pytest test_board.py -k TestBoardCards`).
- If a run hits this, wait ~60s for the rate-limit window to reset before retrying.

### Common pitfalls

| Symptom | Cause |
|---|---|
| All Go tests show as skipped | `TEST_DATABASE_URL` not set |
| Login returns 401 for `demo@octbase.dev` | API not started with `OCTBASE_DEMO_MODE=true`, so the demo user was never seeded |
| `401 UNAUTHORIZED` on every request | No `Authorization: Bearer` header — the API is JWT-only (no `X-User-Id` fallback); log in first |
| Frontend e2e tests all skip | API at `OCTBASE_API_BASE` unreachable, or browser not installed (`playwright install firefox`/`chromium`) |
| `playwright._impl._errors.Error: BrowserType.launch: ...` / Firefox crashes on launch | Playwright's bundled Firefox doesn't run on this OS and isn't installed. Use `OCTBASE_BROWSER=chromium` or `chrome` instead |
| i18n/translation tests fail only with `OCTBASE_BROWSER=chromium`/`chrome` (`t('key')` returns the raw key) | Chromium blocks `fetch()` of local JSON under `file://`. Set `OCTBASE_UI_URL` to an `http://` URL |
| `app` fixture times out on `text=Demo Project`, login form shows "Invalid email or password" | Login rate limit (120/min) hit by repeated test runs — see "Known limitation: login rate limiting" above |
| SSE events don't reach the browser | Caddy must disable response buffering on the events route — the bundled `octbase-frontend/caddy/Caddyfile` sets `flush_interval -1` on the `/api/v1/projects/*/events` reverse proxy |
| Webhook returns 403 | `OCTBASE_WEBHOOK_SECRET_BITBUCKET` / `_GITHUB` not configured |
| Container API can't reach postgres | Ensure both containers are on the same Podman network and postgres is healthy before the API starts |

---

## API reference

All routes are under `/api/v1/`. Browse the full spec at `http://localhost:8000/docs`.

```
# Auth — the eight public routes below share one 120 req/min per-IP budget
POST   /api/v1/auth/login                 # { email, password } → accessToken + refresh cookie, or a challengeToken if MFA is enabled
POST   /api/v1/auth/mfa/verify            # exchange challengeToken + TOTP/recovery code → accessToken + refresh cookie
POST   /api/v1/auth/refresh               # rotate refresh token → new accessToken
POST   /api/v1/auth/logout                # clear refresh token
POST   /api/v1/auth/forgot-password       # { email } → 202 always; emails a 60-min single-use reset link
POST   /api/v1/auth/reset-password        # { token, newPassword }; revokes all sessions
GET    /api/v1/invitations/{token}        # inspect a pending invitation
POST   /api/v1/invitations/{token}/accept # set name + password, create account

# Auth — authenticated, NOT in the rate-limit bucket above
POST   /api/v1/auth/change-password       # { currentPassword, newPassword }; revokes other sessions, keeps this one
GET    /api/v1/auth/me

# Current user
GET    /api/v1/users/me
GET    /api/v1/users/me/dashboard
GET    /api/v1/users/me/notifications
PATCH  /api/v1/users/me/notifications/{id}        # mark read/unread
POST   /api/v1/users/me/notifications/read-all
GET    /api/v1/users/me/notification-preferences
PATCH  /api/v1/users/me/notification-preferences
GET    /api/v1/users/me/preferences               # language, theme, vocabulary
PATCH  /api/v1/users/me/preferences
POST   /api/v1/users/me/avatar                    # upload/replace own profile picture
DELETE /api/v1/users/me/avatar                    # remove it (back to the initials avatar)
GET    /api/v1/users/{userId}/avatar              # serve a user's picture
POST   /api/v1/users/me/mfa/enroll                # start TOTP enrollment → QR/setup key
POST   /api/v1/users/me/mfa/confirm               # confirm TOTP code → enable MFA, return recovery codes
POST   /api/v1/users/me/mfa/disable               # requires re-auth (password or TOTP/recovery code)
POST   /api/v1/users/me/mfa/recovery-codes/regenerate # requires re-auth
GET    /api/v1/search?q=                          # unified search (tasks, pages, projects)

# User management (SUPER_ADMIN, rate-limited 60/min)
GET    /api/v1/users
POST   /api/v1/users
GET    /api/v1/users/{userId}
PATCH  /api/v1/users/{userId}
PATCH  /api/v1/users/{userId}/disable
DELETE /api/v1/users/{userId}

# Audit log (SUPER_ADMIN)
GET    /api/v1/audit-logs

# Projects
GET    /api/v1/projects
POST   /api/v1/projects
GET    /api/v1/projects/{id}
PATCH  /api/v1/projects/{id}              # edit name / abbreviation / description / visibility
DELETE /api/v1/projects/{id}              # cascade-deletes all dependent data
POST   /api/v1/projects/{id}/archive
POST   /api/v1/projects/{id}/unarchive   # owner only; the way out of the archived freeze
GET    /api/v1/projects/{id}/permissions  # current user's project role + permission map
GET    /api/v1/projects/{id}/members
GET    /api/v1/projects/{id}/assignable-users     # who may be assigned/reviewer on this project
GET    /api/v1/projects/{id}/memberships
POST   /api/v1/projects/{id}/memberships          # add member (role validated via CanAssignRole)
PATCH  /api/v1/projects/{id}/memberships/{userId} # change role (escalation + last-owner checks)
DELETE /api/v1/projects/{id}/memberships/{userId} # remove member (last-owner check)
GET    /api/v1/projects/{id}/activity             # newest first; ?page/?size, 50 per page by default
GET    /api/v1/projects/{id}/relations            # every task relation in the project in one call
GET    /api/v1/projects/{id}/events               # SSE stream
GET    /api/v1/projects/{id}/presence
GET    /api/v1/projects/{id}/search/tasks
GET    /api/v1/projects/{id}/search/pages

# Tasks
GET    /api/v1/projects/{id}/tasks
POST   /api/v1/projects/{id}/tasks
POST   /api/v1/projects/{id}/tasks/bulk
GET    /api/v1/projects/{id}/backlog
GET    /api/v1/tasks/{taskId}
PATCH  /api/v1/tasks/{taskId}
DELETE /api/v1/tasks/{taskId}
POST   /api/v1/tasks/{taskId}/assign | /status | /priority | /pin | /copy | /archive | /reopen
GET    /api/v1/tasks/{taskId}/activity            # same paging; entries survive their task, unlinked
# comments · links · attachments · relations · branches
POST   /api/v1/tasks/{taskId}/comments        GET … PATCH/DELETE …/{commentId}
POST   /api/v1/tasks/{taskId}/links           GET … DELETE …/{linkId}
POST   /api/v1/tasks/{taskId}/attachments     GET … DELETE …/{attachmentId}   # metadata / external-link rows
POST   /api/v1/tasks/{taskId}/attachments/upload                 # multipart file upload
GET    /api/v1/tasks/{taskId}/attachments/{attachmentId}/content # download uploaded file bytes
POST   /api/v1/tasks/{taskId}/relations       GET … DELETE …/{relationId}
POST   /api/v1/tasks/{taskId}/branches        GET … DELETE …/{branchId}

# Task categories, priorities & templates
POST   /api/v1/projects/{id}/task-categories   GET … PATCH/DELETE /api/v1/task-categories/{categoryId}
POST   /api/v1/projects/{id}/task-priorities   GET … DELETE /api/v1/task-priorities/{priorityId}   # custom priorities (admins)
POST   /api/v1/projects/{id}/task-templates    GET … GET/PATCH/DELETE /api/v1/task-templates/{templateId}
POST   /api/v1/task-templates/{templateId}/instantiate

# Boards & columns (configurable)
POST   /api/v1/projects/{id}/boards            GET …
GET    /api/v1/projects/{id}/boards/default
GET    /api/v1/boards/{boardId}   PATCH …   DELETE …
POST   /api/v1/boards/{boardId}/columns        PATCH/DELETE …/columns/{columnId}
POST   /api/v1/boards/{boardId}/move-task | /remove-task
GET    /api/v1/boards/{boardId}/external-columns   POST …   DELETE …/{externalColumnId}

# Releases
POST   /api/v1/projects/{id}/releases   GET …
GET    /api/v1/releases/{releaseId}   PATCH …   DELETE …
POST   /api/v1/releases/{releaseId}/close | /reopen

# Sprints
POST   /api/v1/projects/{id}/sprints   GET …
GET    /api/v1/sprints/{sprintId}   PATCH …   DELETE …
POST   /api/v1/sprints/{sprintId}/start | /complete

# Reports
GET    /api/v1/sprints/{sprintId}/burndown          # task-count burndown (ACTIVE/COMPLETED)
GET    /api/v1/projects/{id}/reports/velocity       # last N completed sprints (default 6, cap 20)
GET    /api/v1/projects/{id}/reports/statistics     # the project statistics page

# Wiki pages
POST   /api/v1/projects/{id}/pages   GET …
GET    /api/v1/pages/{pageId}   PATCH …   DELETE …
POST   /api/v1/pages/{pageId}/publish | /archive
POST   /api/v1/pages/{pageId}/render-preview
GET    /api/v1/pages/{pageId}/revisions
GET    /api/v1/pages/{pageId}/references   POST …/references/rebuild

# SCM
GET    /api/v1/projects/{id}/repository-connections   POST …
PATCH  /api/v1/repository-connections/{repositoryId}   DELETE …
GET    /api/v1/repository-connections/{repositoryId}/oauth/authorize   # start OAuth
POST   /api/v1/repository-connections/{repositoryId}/oauth/refresh     # refresh token
GET    /api/v1/oauth/{provider}/callback                               # OAuth callback
POST   /api/v1/tasks/{taskId}/branches/{branchId}/pull-request         # open a PR/MR
POST   /api/v1/webhooks/bitbucket
POST   /api/v1/webhooks/github

# Import / export
POST   /api/v1/projects/{id}/import/jira-csv
GET    /api/v1/projects/{id}/export/jira-csv
GET    /api/v1/projects/{id}/export       # whole-project ZIP
POST   /api/v1/projects/{id}/import       # ZIP into an existing project
POST   /api/v1/projects/import            # ZIP as a new project

# Admin
POST   /api/v1/admin/invitations          # NOT admin-only despite the path: SUPER_ADMIN, ADMIN,
                                          # or a PROJECT_OWNER/PROJECT_ADMIN of the target project
GET    /api/v1/admin/users                # ADMIN or SUPER_ADMIN — legacy, prefer /api/v1/users
PATCH  /api/v1/admin/users/{userId}       # ADMIN or SUPER_ADMIN
POST   /api/v1/admin/users/{userId}/reset-password   # ADMIN or SUPER_ADMIN

# Platform
GET    /api/v1/health      # DB pool + migration version, 503 when degraded
GET    /api/v1/version
GET    /api/v1/config      # app version + feature toggles (e.g. taskView) for the SPA
GET    /api/v1/meta/enums  # canonical statuses, priorities, types, roles, relation types …
GET    /metrics
GET    /openapi.yaml
```

---

## Architecture

```
octbase-api/
├── cmd/octbase-api/main.go     — server entry point, wiring
├── internal/
│   ├── auth/           — JWT, bcrypt, invitation flow, JWTMiddleware
│   ├── bootstrap/      — first SUPER_ADMIN from env while the users table is empty
│   ├── seed/           — deterministic demo data (fixed IDs, demo project)
│   ├── identityaccess/ — users, project memberships
│   ├── rbac/           — pure permission functions (Can*() helpers), no DB
│   ├── usermgmt/       — SUPER_ADMIN user CRUD at /api/v1/users
│   ├── auditlog/       — audit_logs table, write + list handler
│   ├── admin/          — legacy admin panel: invitations, import
│   ├── workmanagement/ — projects, tasks, boards, releases, seq numbers
│   ├── docs/           — pages, revisions, render-preview, TOC
│   ├── scmintegration/ — repository connections, branch references, PR status
│   ├── notifications/  — in-app notifications
│   ├── sse/            — Server-Sent Events hub, presence
│   ├── webhooks/       — Bitbucket/GitHub HMAC receivers
│   ├── mailer/         — SMTP with stdout dev-mode fallback
│   ├── activity/       — project activity feed
│   ├── dashboard/      — user language/theme preferences
│   ├── security/       — TOTP MFA enroll/confirm/disable, recovery codes
│   ├── retention/      — GDPR audit/activity data purge
│   ├── shared/         — DB helpers, HTTP utils, CORS, RBAC guards
│   ├── apicontract/    — test-only: chi routes ↔ openapi.yaml parity check
│   ├── archtest/       — test-only: core/module dependency-direction check
│   └── testutil/       — shared test infrastructure (JWT tokens, schema isolation)
└── migrations/         — golang-migrate up/down SQL, sequentially numbered
```

Migrations run automatically at startup via `golang-migrate`. The health endpoint reports the current migration version; the server returns 503 if the version does not match the expected value.
