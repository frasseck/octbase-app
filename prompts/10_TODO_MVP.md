# POC → MVP Roadmap

> **Archived:** This roadmap is superseded — auth/JWT/RBAC, SSE, notifications, webhooks, golang-migrate, and the `/api/v1/` prefix described here are all implemented. Kept for historical context only.

**Audience:** Engineering team and product owner.

**Philosophy:** We win not by doing more than Atlassian but by doing less — and doing it so well that switching costs disappear. Every item below must earn its place against that standard. If a feature does not make the core experience meaningfully better for the single client going live first, it does not ship in the MVP.

The client currently uses Jira (tasks/boards), Confluence (documentation), and Bitbucket (source control). They do not use the long tail: custom fields, automation rules, roadmaps, Jira Service Management, Confluence macros, Bitbucket Pipelines advanced config. We replace what they actually use, and we make it faster and clearer.

---

## Non-Goals (explicitly out of MVP)

Call these out now so they do not creep in.

- No custom fields or field schemas
- No automation / rules engine
- No time tracking or story points
- No roadmap / Gantt view
- No multiple boards per project
- No sprints / iterations (releases are sufficient)
- No Jira-style issue hierarchy beyond Task → Subtask (if needed at all)
- No plugin / marketplace / extensions
- No multi-tenant SaaS infrastructure
- No mobile app (responsive web is enough)
- No Bitbucket replacement (we integrate with their existing Bitbucket; we do not host code)
- No Confluence-style nested space hierarchy (flat project-scoped pages are enough)

---

## 1. Identity & Access — the foundation of everything

**Why it blocks everything else:** Until there is a real authentication layer, no feature can be safely shipped to a real user.

- [ ] **Choose and implement an auth strategy**
  - Recommended: email + password with bcrypt + JWT (access token 15 min, refresh token 30 days, stored in httpOnly cookie).
  - Alternative if the client has an identity provider: SAML 2.0 SSO or OIDC. Confirm with client before building.
  - Either way: abstract the identity check behind an `AuthProvider` interface so the impl can be swapped.
- [ ] **User registration and invitation flow**
  - Admin invites a user by email → one-time token sent → user sets password on first login.
  - No self-registration (single client, controlled user list).
- [ ] **Session management**
  - JWT validation middleware replaces the X-User-Id header.
  - Refresh token rotation on each use.
  - Logout endpoint that invalidates the refresh token.
- [ ] **Enforce RBAC on all endpoints** (this is TODO_POC item 1 promoted to MVP-required)
  - Project OWNER: full control including delete, membership management, repo connections.
  - DEVELOPER: create/edit/delete tasks, pages, comments, branches. Cannot manage members or delete the project.
  - VIEWER: read-only on all resources. No writes at all.
  - Enforce in a single middleware layer, not scattered across handlers.
- [ ] **Admin panel (minimal)**
  - List all users, deactivate/reactivate, reset password link.
  - Not a full CRUD UI — just what ops needs to manage the single client's team.
- [ ] **Audit log includes real user identity** — replace the seeded UUIDs with the authenticated user's ID throughout.

---

## 2. User Experience — where the product wins or loses

The POC UI works. The MVP UI must be fast, keyboard-driven, and feel finished. Every interaction should feel like it has fewer steps than Jira.

### Navigation & Information Architecture

- [ ] **Personal dashboard ("My Work")** — default landing page showing: tasks assigned to me, tasks I'm reviewing, recent pages I edited, activity on my tasks. This alone replaces 80% of Jira's home screen clutter.
- [ ] **Project switcher** — keyboard-accessible (⌘K / Ctrl+K) command palette that searches projects, tasks, and pages in one box. Replaces the left sidebar project list for fast navigation.
- [ ] **Breadcrumb-aware URLs** — every view must have a bookmarkable, shareable URL that restores exact state (project, view, task panel open, filters active).
- [ ] **Filter persistence** — active filters survive page reload via URL query params (not localStorage). A filtered board URL can be shared.

### Task Management UX

- [ ] **Inline task creation everywhere** — pressing `N` on the board or backlog opens an inline creation row without a modal. Title + Enter is enough; details can be filled later.
- [ ] **Task detail as a right panel or full page** — POC has a right panel; confirm with the client whether they want a full-page task detail view (better for complex tasks) or keep the panel. The URL should reflect the open task either way.
- [ ] **Bulk actions** — select multiple tasks with checkbox or shift-click; apply status, assignee, release, or priority in one action. Essential for backlog grooming.
- [ ] **@mention users in comments and descriptions** — typing `@name` in a task comment or description triggers a dropdown and creates a notification. Not a nice-to-have: comments without mentions are useless for async teams.
- [ ] **Subtasks** — a task can have child tasks (one level only, no recursion). Required for Epics/Stories/Tasks if the client uses that hierarchy. Confirm before building — if they don't use it, skip it.
- [ ] **Assignee quick filter** — "My tasks" toggle on any view (board, backlog, list) that filters to the current user's tasks without losing other active filters.
- [ ] **Keyboard shortcuts** — define and ship a complete shortcut map. At minimum: `N` new task, `B` board, `L` list, `?` shortcut help, `Esc` close panel, `E` edit focused task, `A` assign to me.

### Documentation UX

- [ ] **Live preview while editing** — the page editor should render AsciiDoc in a split pane or debounced preview panel as the user types, not only on explicit "preview" click.
- [ ] **Task mentions in pages** — typing `TB-123` in a page body auto-links to the task and appears in the task's "referenced in" list. The rebuild-references endpoint already exists; wire it to happen on publish.
- [ ] **Page table of contents** — auto-generate TOC from AsciiDoc headings and render it as a sticky sidebar on the page view. This is table stakes for documentation tools.
- [ ] **Search across tasks and pages together** — one search box in the command palette returns both task and page results, ranked by relevance. Do not force the user to switch between task search and page search.

---

## 3. Real-time Collaboration

Single-client teams expect to see changes without refreshing.

- [ ] **WebSocket or SSE for live updates** — when any user moves a task, changes a status, or adds a comment, other users viewing the same project see the update within 2 seconds.
  - Recommendation: Server-Sent Events (SSE) per project channel. Simpler than WebSocket for unidirectional pushes; sufficient for this use case.
- [ ] **Optimistic UI updates** — the frontend should apply changes locally before the server confirms, then reconcile. Eliminates the 200ms "wait for response" flicker on every interaction.
- [ ] **Presence indicators** — show avatar bubbles on task panels to indicate who else is viewing the same task. Low implementation cost, high perceived quality.

---

## 4. Notifications

Without notifications, the tool is not usable for a team.

- [ ] **In-app notification center** — bell icon with unread count; list of notifications: assigned to task, mentioned in comment, task status changed (on tasks you own or watch), page published.
- [ ] **Email notifications** — one email per event (no digest for MVP): task assigned, @mentioned, release approaching (3 days before due date). Plain text + minimal HTML. Transactional email via SMTP (client may have their own mail server; parameterize this).
- [ ] **Notification preferences** — per-user toggle for each notification type (in-app, email, or both). Opt-out must be easy.
- [ ] **Watch / unwatch tasks** — any user can watch a task to receive notifications on changes, not only the assignee.

---

## 5. SCM Integration (Real)

The client uses Bitbucket. The POC has a placeholder. The MVP has real webhooks.

- [ ] **Bitbucket webhook receiver** — `POST /api/webhooks/bitbucket` receives push and PR events; updates branch status on the linked task automatically.
- [ ] **PR status on task** — when a Bitbucket PR linked to a branch on a task changes state (open, merged, declined), the task panel shows the current PR status with a link. No polling; webhook-driven.
- [ ] **Auto-close task on branch merge** — configurable per project: when the linked PR is merged to the main branch, automatically move the task to DONE.
- [ ] **Branch name suggestions** — when creating a branch reference, suggest a slug based on task type and title (e.g., `feature/TB-42-improve-search`). One click copies it to clipboard.
- [ ] **GitHub support** — if the client has any GitHub repos, add a second webhook receiver for GitHub PR events. Use the same internal branch-reference model; only the inbound event shape differs.

---

## 6. Data Migration — the switch must be painless

Without a migration path, the client keeps Jira running in parallel, which means Octbase never becomes the system of record.

- [ ] **Jira CSV import** — Jira's built-in export produces a CSV. Write an import handler (`POST /api/admin/import/jira-csv`) that maps Jira statuses and priorities to Octbase equivalents, creates tasks, and preserves the original Jira ID in a `external_ref` field on the task. Do not try to import comments or attachments in the first pass.
- [ ] **Confluence HTML export import** — Confluence exports spaces as HTML or PDF. Write a basic import that creates one Octbase page per Confluence page using the content as AsciiDoc (converted with pandoc or a Go library). Hierarchy collapses to flat pages within a project.
- [ ] **Dry-run mode** — the import endpoints must support `?dryRun=true` that returns a preview of what would be created without writing to the DB.
- [ ] **External reference field on tasks** — `external_ref TEXT` column added via migration. Populated during import; useful for cross-linking during the transition period and for support investigations.

---

## 7. Performance & Reliability

The POC works; the MVP must not go down or slow down under real load.

- [ ] **Database connection pool** — configure `db.SetMaxOpenConns`, `db.SetMaxIdleConns`, `db.SetConnMaxLifetime` based on PostgreSQL's `max_connections`. Document the chosen values.
- [ ] **Query performance baseline** — run `EXPLAIN ANALYZE` on the 10 most-used queries (task list with filters, board column fetch, search, activity feed). Fix any seq scan on large tables.
- [ ] **Graceful shutdown** — the server must drain in-flight requests on SIGTERM before exiting. Required for zero-downtime deploys.
- [ ] **Health check depth** — `/health` currently checks DB reachability. Add: connection pool saturation, migration version match.
- [ ] **Database migration tooling** — replace the manual `001_initial.sql` with a migration runner (e.g., `golang-migrate`). Migrations run on startup in a single-node deployment; no manual SQL apply.
- [ ] **Structured logging with levels** — `LOG_LEVEL` env var controls verbosity. Production runs at `warn`; staging at `info`; development at `debug`. No sensitive data at any level.
- [ ] **Application metrics** — expose `/metrics` in Prometheus format: request count and latency by route, DB query latency, WebSocket/SSE connection count, error rate. Wire to Grafana if the client has it; otherwise defer dashboards but emit the metrics.

---

## 8. Deployment & Operations

- [ ] **Environment configuration** — all secrets and environment-specific values via env vars: `OCTBASE_DATABASE_URL`, `OCTBASE_JWT_SECRET`, `OCTBASE_CORS_ORIGIN`, `SMTP_*`, `WEBHOOK_SECRET_BITBUCKET`. No hardcoded defaults except dev fallbacks that are clearly labeled.
- [ ] **Container image for production** — multi-stage Dockerfile: builder stage (Go compile + static frontend build), runner stage (distroless or Alpine, no shell). Image < 50 MB.
- [ ] **CI pipeline** — on every PR: `go test ./...`, `golangci-lint`, frontend `pytest`, build Docker image, push to registry. On merge to main: deploy to staging automatically.
- [ ] **Staging environment** — mirrors production configuration. Client can preview changes before they go live. Use the same Docker Compose or a minimal k8s manifest.
- [ ] **Database backups** — daily automated PostgreSQL dump to an offsite location (S3 or equivalent). Retention: 30 days. Test restore quarterly.
- [ ] **TLS termination** — nginx or a load balancer terminates HTTPS. No HTTP in production. Redirect 301 HTTP → HTTPS.
- [ ] **Deployment runbook** — a single document covering: how to deploy, how to roll back, how to run migrations, how to restore a backup. This is the on-call reference.

---

## 9. Architecture Gaps to Close Before MVP

These are POC shortcuts that become real problems under production conditions.

- [ ] **Complete all TODO_POC items** — every item in TODO_POC.md must be done before the MVP ships. The POC list is the foundation; the MVP list is the building.
- [ ] **Repository interface + test doubles** — replace direct DB calls in tests with in-memory implementations of the repository interfaces. This cuts test suite runtime from ~seconds to ~milliseconds and removes the need for a real DB in unit tests.
- [ ] **Domain event bus** — the naive `activity.Log()` pattern cannot support notifications or WebSocket broadcasts without coupling every service to every consumer. Introduce an in-process event bus (a simple `chan DomainEvent` with typed subscribers) before the MVP adds more consumers.
- [ ] **API versioning** — prefix all routes with `/api/v1/`. When a breaking change is needed, `/api/v2/` can coexist. Without versioning, any breaking change breaks the frontend or any client script the team has written.
- [ ] **Frontend build pipeline** — the no-build SPA is elegant for a POC. For MVP, add esbuild (single binary, zero config) to: bundle modules, minify, add cache-busting hashes to filenames. The user experience of a ~20 kB compressed bundle vs. 1 500 uncached lines of JS matters on slow connections.

---

## 10. What Success Looks Like

The MVP is ready to replace Jira/Confluence for the client when:

1. Every team member logs in without needing a developer to set their UUID.
2. All existing Jira issues and Confluence pages are imported and browseable.
3. Bitbucket PR status appears on the linked task without manual updates.
4. A developer can create a task, link a branch, move it to Done, and see it in the activity feed — in under 60 seconds, without consulting documentation.
5. A manager can see what every team member is working on from the "My Work" dashboard in one click.
6. The system handles a full working day of the team's load without a restart or a slow query.
7. The on-call runbook has been tested by someone who was not involved in writing it.
