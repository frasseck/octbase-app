Act as a senior full-stack engineer working on Octbase, an existing Go (chi + PostgreSQL) task management SaaS with a React-free vanilla JS frontend (`octbase-frontend/js/app.js`).

## Current state (read before designing)

- **Global roles** (`users.global_role`): `SUPER_ADMIN`, `ADMIN`, `USER`, `GUEST`. `SUPER_ADMIN` bypasses all project checks.
- **Project roles** (`memberships.role`, one row per `(project_id, user_id)`): `PROJECT_ADMIN`, `PROJECT_MEMBER`, `PROJECT_VIEWER`. `memberships` also has `assigned_by_user_id`.
- **Ownership today**: `projects.created_by_user_id` records the creator, but it is *not* a permission concept — a `PROJECT_ADMIN` member has the same rights as the creator. There is no "last owner" protection and no way to transfer ownership.
- **Authorization**: all decisions live as pure functions in `internal/rbac/rbac.go` (`CanCreateProject`, `CanEditProject`, `CanDeleteProject`, `CanManageProjectMembers`, `CanCreateTask`, `CanEditTask`, `CanDeleteTask`, `CanViewTask`, etc.), each taking `(globalRole, projectRole)`. Handlers call a `memberGuard(projectID)` helper (see `internal/workmanagement/handler.go:179-205`) to load the caller's effective project role before calling these.
- **Frontend**: `AppPerms` (`octbase-frontend/js/app.js:396-422`) re-derives the caller's project role from `S.user.projectMemberships` (an array returned at login) and exposes coarse checks like `canManageProjectMembers()`, `canEditTask()`, `isReadOnlyProject()`. The comment there already states the backend is authoritative.
- **Migrations**: idempotent, `up`/`down` pairs, numbered sequentially (`009_rbac.up/down.sql` is the latest landed). Follow this convention for new migrations — check the `migrations/` directory for the next free number before naming yours, since other in-flight prompts may have already claimed `010`/`011`.
- **Audit log**: `audit_logs` table + `auditlog.Repo.Write()` is already used for sensitive actions (role changes, member removal, etc.) — extend this, don't build a parallel mechanism.
- **Invitations**: `invitations` table already exists (project-scoped, with a `role` column) and is used for the invite flow.

## Goal

Move from the current **3 coarse project roles with hardcoded per-action checks** to a **fine-grained, permission-key-based authorization system**, while keeping the existing role names/levels as the default role set (don't introduce a parallel "Owner/Admin/Member/Viewer" naming scheme that conflicts with `PROJECT_ADMIN`/`PROJECT_MEMBER`/`PROJECT_VIEWER`). Specifically, add a fourth role above `PROJECT_ADMIN` — `PROJECT_OWNER` — and define an explicit permission matrix so future custom roles are possible without a backend redeploy.

Produce a complete technical proposal (design doc, no implementation yet) covering the items below. Append it to `prompts/_release-v01-audit.md` under a new "Project-based permissions" section if that file exists in this branch; otherwise write it to `prompts/24_project-based-permissions_design.md`.

### 1. Data model
- Decide: do permissions live purely in code (extend `rbac.go` with a permission-key table keyed by role) for now, or add a `role_permissions` table (`role TEXT, permission TEXT, project_id TEXT NULL`) so a project can later override its defaults? Recommend the simpler option but design the schema so the richer one is additive.
- New `memberships.role` value `PROJECT_OWNER`. Decide whether `PROJECT_ADMIN`/`MEMBER`/`VIEWER` keep their current meaning or shift down a level — document the final mapping explicitly.
- Constraint: every project must have at least one `PROJECT_OWNER` at all times (document how this is enforced — DB trigger vs. application-level transaction).
- Provide example SQL migration(s) (idempotent, `up`/`down`, numbered per the convention above), including any new indexes/constraints and the backfill described in section 9.

### 2 & 3. Roles and permissions
- Default project roles: `PROJECT_OWNER`, `PROJECT_ADMIN`, `PROJECT_MEMBER`, `PROJECT_VIEWER`.
- Define permission keys covering at least: `project.view`, `project.update`, `project.delete`, `project.archive`, `project.invite_users`, `project.remove_users`, `project.change_roles`, `project.transfer_ownership`, `task.create`, `task.view`, `task.update`, `task.delete`, `task.assign`, `task.comment`. Map each existing `Can*` function in `rbac.go` to one or more of these keys so the refactor is traceable.
- Note where `SUPER_ADMIN` / `ADMIN` global roles interact with project permissions (today `SUPER_ADMIN` bypasses everything; `ADMIN` only affects `CanCreateProject`) and whether that stays unchanged.

### 4. Permission matrix
A table: rows = permission keys, columns = `PROJECT_OWNER` / `PROJECT_ADMIN` / `PROJECT_MEMBER` / `PROJECT_VIEWER`, cells = allowed/denied. Cross-check against current behavior in `rbac.go` so nothing regresses for existing `PROJECT_ADMIN`/`PROJECT_MEMBER`/`PROJECT_VIEWER` members.

### 5. Authorization logic
- `HasPermission(globalRole, projectRole, permission string) bool` (or similar) as the single source of truth, replacing the per-action `Can*` functions (or having them delegate to it).
- Privilege escalation: a `PROJECT_ADMIN` must not be able to grant `PROJECT_OWNER` or remove/demote an `PROJECT_OWNER`; only an existing `PROJECT_OWNER` (or `SUPER_ADMIN`) can call `project.change_roles` targeting `PROJECT_OWNER`, and `project.transfer_ownership` is its own permission.
- Last-owner protection: reject role changes/removals that would leave a project with zero `PROJECT_OWNER`s; specify the exact error response.
- Archived/deleted projects (`projects.status`): define which permissions remain valid on an archived project (e.g. `project.view`/`task.view` yes, all writes no) vs. a soft-deleted one.

### 6. API design
Extend the existing `/api/v1/projects/{projectId}/...` surface (don't invent a new prefix):
- `GET /api/v1/projects/{projectId}/members` — list members + roles.
- `POST /api/v1/projects/{projectId}/members` (or extend `invitations`) — invite a user with a role.
- `PATCH /api/v1/projects/{projectId}/members/{userId}` — change role (subject to escalation/last-owner rules).
- `DELETE /api/v1/projects/{projectId}/members/{userId}` — remove member.
- `GET /api/v1/projects/{projectId}/permissions` — returns the caller's effective permission set for this project (this is what the frontend should consume instead of re-deriving roles client-side).
- Show how existing task CRUD handlers (`internal/workmanagement/handler.go`) add a permission check via the new `HasPermission` helper. Follow the existing success/error response shapes (`{ id, ..., createdAt, updatedAt, version }` / `{ code, message, messageKey, details }`).

### 7. Code examples
Real Go code (not pseudocode) for:
- Permission key constants and the role→permission matrix in `internal/rbac/rbac.go`.
- `HasPermission(globalRole, projectRole, permission string) bool`.
- A `requirePermission(permission string)` middleware/guard that composes with the existing `memberGuard(projectID)` pattern.
- One protected route rewritten to use it (e.g. `DELETE /tasks/{taskId}` using `task.delete`).

### 8. Frontend considerations
- `AppPerms` should fetch `/api/v1/projects/{projectId}/permissions` (cache per project in `S`) instead of re-deriving role logic from `projectMemberships` + hardcoded role-name checks.
- Show/hide pattern for buttons (member management, task actions) using the fetched permission set.
- Reiterate why this doesn't replace backend checks (already noted in the existing `AppPerms` comment — keep that framing).

### 9. Migration plan
- Backfill `PROJECT_OWNER`: for each project, promote the member matching `projects.created_by_user_id` (if they have a membership row) from `PROJECT_ADMIN` to `PROJECT_OWNER`. For projects with no `created_by_user_id` or where the creator has no membership row (pre-009 data), define a deterministic fallback (e.g. earliest-assigned `PROJECT_ADMIN` by `memberships.created_at`/`id`, or flag for `SUPER_ADMIN` manual review) — document the chosen fallback and write it as part of the migration's `UPDATE`.
- Confirm no existing `PROJECT_ADMIN`/`PROJECT_MEMBER`/`PROJECT_VIEWER` loses access they currently have (the permission matrix in section 4 must be a superset of current `Can*` results for those roles).
- `invitations.role` and any role validation (`IsValidProjectRole`) need to accept `PROJECT_OWNER`.

### 10. Testing
Test cases (as `rbac_test.go` table-driven tests plus handler-level integration tests) covering:
- Each permission key × each role, matching the matrix in section 4.
- Privilege escalation attempts (e.g. `PROJECT_ADMIN` trying to grant/revoke `PROJECT_OWNER`, or change their own role).
- Last-owner removal/demotion rejected; succeeds once a second owner exists.
- `SUPER_ADMIN`/`ADMIN` global-role interactions unchanged.
- Archived project: writes rejected, reads allowed (per section 5's decision).
- Frontend `AppPerms`: permissions endpoint drives button visibility (Playwright test alongside existing `octbase-frontend/tests`).

## Constraints
- Keep the design additive — no breaking changes to existing `PROJECT_ADMIN`/`PROJECT_MEMBER`/`PROJECT_VIEWER` semantics for current users.
- Reuse existing patterns: `memberGuard`, `auditlog.Repo.Write()`, idempotent numbered migrations, existing response/error shapes, existing i18n setup for any new frontend strings.
- This is a design-only prompt — do not implement code changes; the output is the technical proposal described above.
