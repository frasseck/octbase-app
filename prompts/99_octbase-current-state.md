# Octbase — Current-State Reference

*Last synced with the code: 2026-07-27 (release_v22).*

This is the **authoritative, consolidated description of Octbase as it exists today**.
The other prompts in this directory are historical (they document the product as it
was at the point each was written); when a historical prompt and this document
disagree, **this document and the source code win**. Keep this file in sync with the
code, `octbase-api/README.md`, `docs/operations.md`, the user guide
(`octbase-frontend/user-guide.html`), and `octbase-api/api/openapi.yaml`.

> Ground truth, in order: the running code → `openapi.yaml` → this file → README /
> operations / user guide. If you change behaviour, update all of them in the same change.

---

## 1. What Octbase is

A focused project-management tool that replaces Jira, Confluence, and Bitbucket
integration for a single, invitation-only team. It deliberately ships fewer features
than Atlassian; every feature that ships makes the core workflow faster.

- **Backend** — Go modular monolith (`octbase-api/`), chi router, PostgreSQL, JWT auth,
  Server-Sent Events, HMAC webhooks, Prometheus metrics.
- **Frontend** — vanilla-JS single-page app (`octbase-frontend/`), **no build step, no
  framework**, hash-based routing, i18n (English / German), served by Caddy. A phone-first
  SPA (`octbase-mobile/`) is served under `/m/` by the same Caddy front door (which also
  detects mobile devices by User-Agent).
- **Landing page** — the public marketing site lives in a **separate repository**
  (`ocete.ch`) and is **not part of this repo** (and is not in the compose stack).

---

## 2. Repository layout

| Path | Purpose |
|---|---|
| `README.md` | Root-level project overview, features, quick start |
| `CLAUDE.md` | Live agent instructions (repo-wide conventions, skills) |
| `octbase-api/` | Go backend: `cmd/`, `internal/`, `api/openapi.yaml`, `migrations/` (sequential from 001; head derived from files), `web/` (docs + user guide), `README.md`, tests |
| `octbase-frontend/` | SPA: `index.html`, ordered `js/*.js` (no `app.js` — split into load-ordered classic scripts), `js/i18n.js`, `css/`, `locales/`, `img/`, `user-guide.html`, `caddy/`, `tests/` |
| `octbase-mobile/` | Phone-first static SPA, served under `/m/` by the frontend's Caddy |
| `octbase-shared/` | Canonical source for cross-site modules (`i18n.js`, `meta.js`, `qrcode.js`, `purify.js`, `richtext.js`), synced byte-identically into both SPAs by `scripts/sync-shared.sh`; drift fails CI |
| `octbase-operations/` | Ops runbook + `check-health.sh` |
| `podman-compose.yml` | Full deployable stack: postgres + api + frontend (Caddy) + mobile — **no Mailpit** |
| `podman-compose.dev.yml` | Dev-only overlay adding Mailpit mail capture (layer with `-f`; never deploy) |
| `.env.example` | Every supported environment variable with defaults |
| `docs/operations.md` | Production runbook |
| `prompts/` | Historical creation/process prompts + this reference + the quality master (`100_*`) |

> The root-level **`README.md`** (product overview, features, quick start) and
> **`CLAUDE.md`** (live agent instructions) now live at the repo root, not under
> `prompts/`. `octbase-api/README.md` remains the backend-specific reference
> (API surface, domain rules, bounded contexts).

---

## 3. Backend module map (`octbase-api/internal/`)

Each domain package owns its `domain.go` (types), `repo.go` (SQL), `handler.go` (HTTP),
and tests. `cmd/octbase-api/main.go` wires them together.

| Package | Responsibility |
|---|---|
| `auth` | JWT issue/validate, bcrypt, refresh-token rotation, invitation flow, `JWTMiddleware`, pluggable `Provider` |
| `identityaccess` | Users and project memberships |
| `rbac` | Pure permission functions (`Can*`, `HasPermission`, `WouldRemoveLastOwner`) — no DB |
| `usermgmt` | SUPER_ADMIN user CRUD at `/api/v1/users` (rate-limited 60/min) |
| `auditlog` | Append-only `audit_logs` write + list (SUPER_ADMIN) |
| `admin` | Legacy admin panel: invitations, imports, legacy user endpoints |
| `workmanagement` | Projects, tasks (incl. pin, threaded comments, auto-archive), links, attachments, relations, boards/columns, releases, sprints, categories, templates, search, Jira CSV import/export |
| `docs` | Wiki pages, revisions, render-preview, TOC, task cross-references |
| `scmintegration` | Repository connections (PAT + OAuth, encrypted tokens), branch references, PR status |
| `webhooks` | Bitbucket / GitHub HMAC-SHA256 receivers |
| `notifications` | In-app + email notifications, per-user/per-kind preferences, Jira/Confluence import |
| `sse` | Server-Sent Events hub + presence |
| `activity` | Project / task activity feed |
| `dashboard` | Per-user preferences (language, theme) behind `/api/v1/me/preferences` |
| `security` | TOTP MFA: enrollment, verify, recovery codes (secrets AES-256-GCM encrypted with `OCTBASE_MFA_ENC_KEY`) |
| `retention` | GDPR data purge for deactivated accounts |
| `bootstrap` | First-run admin provisioning from `OCTBASE_BOOTSTRAP_*` env |
| `mailer` | SMTP with stdout dev-mode fallback (Mailpit only via the dev compose overlay) |
| `seed` | Demo data (gated by `OCTBASE_DEMO_MODE`) |
| `shared` | DB helpers, HTTP utils, CORS, rate limiting, RBAC guards, i18n errors, secret encryption |
| `apicontract` | Test-only: asserts chi routes and `openapi.yaml` paths stay in parity |
| `archtest` | Test-only: enforces the core/module dependency direction (no module→module imports outside a reviewed allowlist) |
| `testutil` | Schema-isolated test infrastructure (JWT tokens, per-test schema) |

---

## 4. Domain entities & enums

**Entities** — `Project` (with `abbreviation`, `estimationUnit`,
`themeEnabled`/`initiativeEnabled`), `Task` (type, status, priority, assignee,
reporter, reviewer, release, sprint, due date, board column + rank, seq number,
external ref, `pinned`, `doneAt`, `parentId` for the type hierarchy,
`storyPoints`/`estimateHours`), `TaskComment` (optional `parentId` for replies),
`TaskLink`, `TaskAttachment`, `TaskRelation`, `Board`, `BoardColumn`,
`BoardExternalColumn`, `Sprint`, `Release`, `TaskCategory`, `TaskTemplate`, `Page`,
`PageRevision`, `PageReference`, `RepositoryConnection`, `BranchReference`.

**Canonical enums** (`GET /api/v1/meta/enums`, defined in `cmd/octbase-api/main.go`):

| Enum | Values |
|---|---|
| Task statuses | `PLANNED` · `IN_PROGRESS` · `IN_REVIEW` · `DONE` · `ARCHIVED` (plus custom board-lane names) |
| Task priorities | `LOW` · `MEDIUM` · `HIGH` · `CRITICAL` · `BLOCKER` (plus per-project custom priorities via `project_priorities`) |
| Task types | `TASK` · `STORY` · `EPIC` · `SUBTASK` · `INITIATIVE` · `THEME` — a strict hierarchy (THEME→INITIATIVE→EPIC→STORY→TASK→SUBTASK); THEME/INITIATIVE are opt-in per project. **No `BUG`/`CHORE` types** — those appear only in historical prompts |
| Estimation units | `NONE` · `POINTS` · `HOURS` (per-project `estimationUnit`, default `NONE` = no estimate UI; task `storyPoints`/`estimateHours` where `null` means *unestimated*, distinct from `0`) |
| Relation types | `RELATES_TO` · `BLOCKS` · `BLOCKED_BY` · `DUPLICATES` |
| Visibilities | `PUBLIC` · `PRIVATE` |
| Release statuses | `PLANNED` · `CLOSED` |
| Page statuses | `DRAFT` · `PUBLISHED` · `ARCHIVED` |
| Branch types | `feature` · `bugfix` · `hotfix` · `release` |
| SCM providers | `FAKE_GITLAB` · `GITHUB` · `BITBUCKET` (each usable against a self-hosted instance via `apiBaseUrl`) |
| Global roles | `SUPER_ADMIN` · `ADMIN` · `USER` · `GUEST` |
| Project roles | `PROJECT_OWNER` · `PROJECT_ADMIN` · `PROJECT_MEMBER` · `PROJECT_VIEWER` |

---

## 5. Roles & authorization

Two layers, both enforced server-side on every request:

- **Global role** (one per user): `SUPER_ADMIN`, `ADMIN`, `USER`, `GUEST`. There is **no
  `DEVELOPER`/`MAINTAINER`/`REPORTER`/bare-`OWNER` role** — those appear only in
  historical prompts.
- **Project role** (per membership): `PROJECT_OWNER`, `PROJECT_ADMIN`,
  `PROJECT_MEMBER`, `PROJECT_VIEWER`, enforced via the permission matrix at
  `GET /api/v1/projects/{id}/permissions`.

Invariants: every project always has ≥ 1 OWNER; only an OWNER can grant/revoke OWNER or
remove the last owner (`422 LAST_OWNER`). SUPER_ADMIN sees all projects regardless of
membership. Disabling an account invalidates all of its refresh tokens immediately.

---

## 6. Features (current)

- **Tasks** — board (Kanban) / backlog / list views with shared, URL-encoded filters;
  inline creation (`N`); per-project sequence numbers prefixed by the project
  **abbreviation** (e.g. `DP-42`); **threaded comments** (replies via `parentId`) with
  `@mention`; links, attachments, relations; categories and templates (API only, no UI
  yet); **pin / unpin**; copy / archive / reopen; bulk actions; find-by-ID
  (`OCT-202`-style queries in backlog, board and task search).
- **Task hierarchy** — `parentId` with the strict type chain
  (THEME→INITIATIVE→EPIC→STORY→TASK→SUBTASK); THEME/INITIATIVE opt-in per
  project; a mindmap view visualises the hierarchy (open tasks by default,
  with a done-toggle).
- **Effort estimation** — per-project `estimationUnit` (`NONE`/`POINTS`/`HOURS`,
  owner-gated, default `NONE` = feature invisible); story-point chips or
  hour input on tasks (create + edit, desktop + mobile); `null` =
  unestimated ≠ `0`; estimates ride along in export/import/copy.
- **Personal settings & MFA** — per-user language/theme preferences and
  TOTP MFA (enrollment + recovery codes, optional `OCTBASE_REQUIRE_MFA`
  enforcement); user avatars.
- **Auto-archive** — a DONE task is lazily swept to `ARCHIVED` (hidden from the board but
  still reachable via the Archive view and reopenable) once it has been DONE for
  `DoneTaskRetentionDays` (**30 days**, a single named constant). The sweep keys off
  `done_at`, which is set on entering DONE and cleared on any transition out; it runs
  lazily on list, not via a background ticker.
- **Configurable boards** — multiple boards per project, a default board, ordered
  columns (lanes) you can add / rename / reorder / delete; a lane name doubles as a task
  status; external columns map a lane to an SCM state.
- **Planning** — releases (long-horizon, `PLANNED`→`CLOSED`, cannot close with open
  tasks) and sprints (time-boxed, one ACTIVE per project, completing returns unfinished
  tasks to the backlog).
- **Wiki pages** — AsciiDoc with live preview, `DRAFT`→`PUBLISHED`→`ARCHIVED`, revisions,
  auto TOC, task cross-references, per-project page search.
- **SCM** — repository connections, branch references with name suggestions, PR status,
  auto-close on merge, HMAC webhooks. Connections authenticate by **personal access
  token or OAuth** (`authKind` = `PAT`/`OAUTH`); access/refresh tokens are
  **AES-256-GCM encrypted at rest**; `apiBaseUrl` targets self-hosted GitLab /
  Bitbucket Server / GitHub Enterprise.
- **Import / export** — Jira CSV (per-project and platform-wide, `dryRun` supported),
  Confluence HTML ZIP → draft pages, Jira CSV export per project.
- **Notifications** — in-app bell + optional email, per-kind preferences (assigned,
  reviewer set, mentioned, status changed, release due).
- **Real-time** — SSE per project (~1 s), reconnect with backoff (1→2→4→max 30 s),
  presence endpoint.
- **Navigation/UX** — `Ctrl/Cmd+K` command palette, "My Work" dashboard, bookmarkable
  filter-preserving URLs, full keyboard shortcuts, EN/DE i18n.
- **Admin** — user management, audit log, invitations (all SUPER_ADMIN for user mgmt /
  audit; ADMIN+ for invitations/imports).
- **Ops** — `/metrics`, deep `/api/v1/health` (DB pool + migration version, 503 when
  degraded), graceful 30 s shutdown, structured JSON logs.

---

## 7. API surface

Full reference: `octbase-api/api/openapi.yaml` (browsable at `/docs`) and the API
section of `octbase-api/README.md`. Highlights: all routes under `/api/v1/`; JWT bearer
for everything except the public auth/invitation routes, the OAuth callback, and the
HMAC webhooks; auth routes rate-limited **120/min per IP**, `/api/v1/users` **60/min**.
Notable recent endpoints: `POST /tasks/{taskId}/pin`, `POST /tasks/{taskId}/archive`,
and the OAuth flow (`/repository-connections/{id}/oauth/authorize`,
`/repository-connections/{id}/oauth/refresh`, `/oauth/{provider}/callback`).

---

## 8. Infrastructure & operations

- **Reverse proxy: Caddy** (not nginx — no nginx config exists). `octbase-frontend/caddy/Caddyfile`
  serves the SPA on `:8080` and reverse-proxies `/api/*`, `/docs`,
  `/openapi.yaml` to the API, with `flush_interval -1` on the `/events` SSE route; it
  also serves `/m/` (mobile) and proxies `/mailpit/` to the Mailpit container
  (which exists only when the dev overlay is layered; `Caddyfile.tls` never
  proxies it). **`/metrics` is not proxied by any Caddy config** — scrape
  `octbase-api:8000` directly (a Caddy-layer source-IP restriction is inert
  under rootless podman; see `docs/operations.md`).
  `Caddyfile.tls` terminates TLS on `:8443` (certs at `/etc/caddy/tls/`), sets HSTS +
  security headers; note it carries **no `/m/` route** (desktop SPA only).
- **Migrations** — `golang-migrate`, sequential from `001`, run automatically at startup; the
  expected version is **derived from the migration files** (`shared.LatestMigrationVersion`).
  The health endpoint reports the version and returns 503 on mismatch.
- **Env vars** — see `.env.example` and `docs/operations.md`. Production-required:
  `OCTBASE_DATABASE_URL`, `OCTBASE_JWT_SECRET`, `OCTBASE_CORS_ORIGIN`,
  `OCTBASE_SECURE_COOKIES=true`. SCM token encryption uses `OCTBASE_SCM_ENC_KEY`
  (AES-256 key, required for any token-backed connection); OAuth needs
  `OCTBASE_OAUTH_REDIRECT_BASE` plus per-provider
  `OCTBASE_OAUTH_<GITHUB|GITLAB|BITBUCKET>_CLIENT_ID/_SECRET`.
- **Containers** — every service has its own `Containerfile`; `podman-compose.yml` runs
  **postgres + api + frontend (Caddy) + mobile** with `restart: always` and
  per-service resource limits. **Mailpit is dev-only**: layer
  `podman-compose.dev.yml` to add it (reached at `/mailpit/` through the frontend
  Caddy, basic-auth'd, bound to 127.0.0.1); never deploy the overlay.

---

## 9. Testing

- **Go** — `TEST_DATABASE_URL=... go test ./...`; each test gets an isolated schema;
  skips cleanly when the env var is unset. Lint with `golangci-lint run ./...`. The
  `apicontract` package fails the build if chi routes and `openapi.yaml` drift apart.
- **Frontend** — Playwright + pytest under `octbase-frontend/tests/` (`OCTBASE_BROWSER`
  = firefox/chromium/chrome). Requires a live API with `OCTBASE_DEMO_MODE=true`. See the
  `frontend-testing` and `testing` skills and the README testing section.
- Demo users (demo mode): `super@octbase.dev` / `super1234` (SUPER_ADMIN) and
  `demo@octbase.dev` / `demo1234` (ADMIN). Demo project abbreviation: `DP`.

---

## 10. Known current-state caveats (verify before relying on them)

- **Release error codes now use the `RELEASE_*` prefix.** The entity was renamed
  `Milestone`→`Release`, and the error codes and activity action were aligned to match:
  `RELEASE_NOT_FOUND`, `RELEASE_HAS_OPEN_TASKS`, and the `RELEASE_CLOSED` activity event.
  Existing `MILESTONE_CLOSED` activity rows are rewritten by migration
  `016_release_activity_rename`. Historical prompts that still reference the old
  `MILESTONE_*` codes are out of date on that point.
- **Task categories and templates** exist in the backend, seed, and API, but have **no
  frontend UI** yet — document them as API-only until a UI ships. (Note: the board
  `template` field, e.g. `scrum`, is a *board* template and unrelated to task templates.)
- **Auto-archive retention** is the in-code constant `DoneTaskRetentionDays = 30`
  (`internal/workmanagement/domain.go`), not yet a per-project setting; the sweep is
  lazy (triggered on list), so a stale DONE task can briefly remain until the next list.
- `octbase-frontend/user-guide.html` is the single canonical user guide, served
  by Caddy. The API's `/docs` page links to `/user-guide.html`, which resolves to
  this same file via the shared-origin reverse proxy.
