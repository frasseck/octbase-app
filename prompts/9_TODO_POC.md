# POC Completion Checklist

Clean code, functional correctness, information security, clean architecture, DDD, and TDD matter even in a POC.
This list brings the POC to a state where it demonstrates the full domain faithfully, is safe to run, and can be handed to any engineer who can extend it with confidence.

> **Note:** This checklist has been audited against the current codebase (post-MVP, `8_octbase-create-mvp.md` and later). Items confirmed implemented are checked off with a pointer to the implementation; items that no longer apply because the underlying mechanism changed (e.g. `X-User-Id` → JWT auth) have been removed. Remaining unchecked items are still open.

---

## 1. Domain Integrity & Business Rules

- [x] **Enforce project membership on every write** — `shared.RequireProjectMember` is called via `memberGuard` across handlers.
- [x] **OWNER-only operations** — `shared.RequireOwner` rejects non-admin roles for project delete/archive, membership changes, and repo connection deletes.
- [x] **VIEWER read-only enforcement** — `shared.RequireWriter` returns 403 for `PROJECT_VIEWER` on all write handlers.
- [x] **Task immutability completeness** — `IsImmutable` (covers DONE and ARCHIVED) is checked in `task_handler.go` for field updates, not just status transitions.
- [x] **Release closure guard completeness** — `CloseRelease` uses `CountOpenForRelease`, which counts all tasks not in DONE or ARCHIVED (covers IN_REVIEW).
- [x] **Board column status uniqueness** — enforced via `idx_board_columns_board_status` unique index (`002_constraints.up.sql`).
- [x] **Task relation symmetry** — inserting a BLOCKS relation automatically creates the inverse BLOCKED_BY relation (and removes both on delete); covered by `TestTaskRelations` in `handler_test.go`.
- [x] **Page slug uniqueness validation** — `SlugExistsForProject` check returns `409 SLUG_CONFLICT` instead of a raw constraint violation.
- [x] **Prevent self-relation** — rejected in the service layer; covered by a dedicated test in `service_test.go`.
- [ ] **Board rank ordering** — `move-task` still sets `BoardRank` to whatever int the client sends with no server-side re-indexing of siblings. Define and enforce a gap-based rank scheme (e.g., LexoRank or float gaps) so ordering is stable after repeated moves.

---

## 2. Data Layer

- [ ] **Wrap all multi-step mutations in a transaction** — `WithTx` is now used in several places (e.g. `move-task`), but audit `task instantiate`, `page publish`, `rebuild references`, and `task copy` to confirm each is fully wrapped.
- [x] **Add missing database indexes** — all listed indexes (`tasks(project_id/assignee_id/release_id/status/board_column_id)`, `board_columns(board_id)` + unique `(board_id, status)`, `page_task_references`, `activity_entries`, `branch_references`, `memberships(user_id)`) exist in `002_constraints.up.sql`.
- [ ] **Use TIMESTAMPTZ columns** — timestamps are still stored as TEXT in `001_initial.up.sql`. Replace with `TIMESTAMPTZ DEFAULT now()` in a new migration.
- [ ] **Parameterized queries audit** — one remaining `fmt.Sprintf` in `workmanagement/repo.go` builds a column list with an integer placeholder index (`$%d`), not user input, so it's not an injection risk — but do a final sweep to confirm no other occurrences crept in.
- [ ] **Consistent soft-delete strategy** — no `deleted_at` columns found yet. Decide once: soft delete with `deleted_at TIMESTAMPTZ` for tasks/pages, hard delete elsewhere; document and enforce.
- [ ] **Pagination on all list endpoints** — `activity`, `auditlog`, `notifications`, and `docs` handlers support pagination; verify releases, task categories, task templates, board columns, and repository connections still need it.

---

## 3. Security

- [x] **Restrict CORS origins** — `OCTBASE_CORS_ORIGIN` env var is read and used as `Access-Control-Allow-Origin` (`internal/shared/httpx.go`).
- [x] **Content-Type enforcement** — `shared.RequireJSON` middleware rejects non-JSON write requests with 415 (applied in `cmd/octbase-api/main.go`).
- [ ] **Input length limits** — task title is capped at 255 chars (`workmanagement/domain.go`), but page slug, comment body, and search query (`q`) limits still need verification/enforcement.
- [ ] **No secrets in repository** — ongoing: re-audit env handling whenever new config is added; ensure no DSN, password, or API key is committed.
- [ ] **Sanitize AsciiDoc output** — `RenderAsciiDoc` exists with tests, but confirm the renderer escapes raw HTML blocks by default or is configured to do so (no `bluemonday`/explicit sanitizer found).
- [ ] **Rate-limit search endpoints** — `internal/shared/ratelimit.go` exists but is not yet wired into `search_handler.go`. Apply the limiter to `/search/tasks` and `/search/pages`.
- [ ] **Structured logging must not leak PII** — audit all `slog` call sites; replace any logging of request bodies, user emails, or UUIDs in production-visible paths.

---

## 4. API Completeness

- [x] **User onboarding without DB access** — superseded by the invite flow (`POST /api/v1/invitations/{token}/accept`, `internal/auth`); a separate self-registration endpoint is not needed.
- [x] **List project members endpoint** — `GET /api/v1/projects/{projectId}/members`.
- [x] **Standardize error envelope** — `shared.WriteError(w, status, code, message)` produces `{"code": "...", "message": "..."}` consistently.
- [x] **`GET /api/v1/tasks/{taskId}` membership check** — `GetTask` goes through `taskGuard`, which enforces project membership.
- [ ] **Task list sorting** — `GET /api/v1/projects/{projectId}/tasks` still has no `?sort=`/`?order=` params. Add `created_at`, `updated_at`, `priority`, `board_rank` as sort keys.
- [ ] **OpenAPI spec drift** — `octbase-api/api/openapi.yaml` exists; audit it against the current route table for missing endpoints (sorting, pagination params on all list routes).

---

## 5. Test Coverage

- [ ] **Service-layer unit tests for all guards** — membership enforcement, OWNER-only rules, task immutability, release closure guard extension, board column uniqueness, and self-relation rejection now have dedicated tests; confirm coverage is complete for any newer guards.
- [ ] **Transaction rollback tests** — add tests that inject a DB error mid-transaction and assert no partial state is persisted.
- [ ] **Handler input validation tests** — for each write endpoint, add tests for: missing required fields, oversized inputs, wrong content-type, missing/invalid auth, invalid UUID format.
- [ ] **Pagination tests** — verify `page` and `size` params on all list endpoints, including edge cases (page 0, size > max, last page).
- [ ] **Frontend tests for user creation and membership** — add Playwright tests: create a user via the invite flow, add them to a project, verify role restrictions in the UI.
- [ ] **Frontend test for task relation symmetry** — add a BLOCKS relation in the UI, verify the task panel on the blocked task shows BLOCKED_BY.
- [ ] **Test isolation** — each Go test must create its own schema and drop it in `t.Cleanup`. Verify no test leaks state.
- [ ] **CI smoke test script** — add a runnable CI step that boots the stack and hits every endpoint once. (The earlier `draft/smoke_tests.sh` reference no longer applies — that file doesn't exist.)

---

## 6. Clean Architecture / DDD

- [ ] **No repository logic in handlers** — re-audit for any remaining `db.QueryRow` calls directly in handlers.
- [ ] **Domain events as explicit types** — activity entries are still written by calling `activity.Log(...)` directly from service methods. Consider a `DomainEvent` interface if cross-context coupling becomes a problem.
- [ ] **Value objects for status/priority/type** — status/priority/type are still `string` constants with `Valid()`-style helper functions (e.g. `ValidStatus`); consider typed wrappers if string-comparison bugs recur.
- [ ] **Repository interfaces** — define `TaskRepository`, `ProjectRepository`, etc. as interfaces in the domain package if test doubles without a live DB become necessary.
- [ ] **No cross-context direct DB calls** — verify `scmintegration` handlers go through `workmanagement` rather than querying `tasks` directly.
- [x] **Consistent file layout per bounded context** — current packages (`auth`, `admin`, `notifications`, `webhooks`, `sse`, `identityaccess`, `workmanagement`, `docs`, etc.) already follow `domain.go` / `repo.go` / `service.go` / `handler.go`.

---

## 7. Code Quality

- [ ] **Eliminate all `TODO` and `FIXME` comments** — resolve or promote to this checklist.
- [ ] **Remove seed dependency from production binary** — `internal/seed` is still gated by `OCTBASE_DEMO_MODE` inside `cmd/octbase-api`. Consider moving to a separate `cmd/octbase-seed` binary.
- [ ] **Consistent error wrapping** — use `fmt.Errorf("context: %w", err)` everywhere; never discard errors with `_`.
- [ ] **No magic strings** — status values, role names, provider names used in multiple files must be defined as constants in the domain package and imported, not repeated as literals.
- [ ] **Linting** — no `.golangci.yml` found yet. Add `golangci-lint` with at minimum: `errcheck`, `staticcheck`, `govet`, `unused`, `gosec`. Fix all reported issues.
- [ ] **Frontend: extract API client** — `app.js` (now ~3750 lines) still contains all `fetch()` calls inline with no shared `api.js` module.
- [ ] **Frontend: extract view modules** — split `app.js` into one file per view (`board.js`, `backlog.js`, `releases.js`, `pages.js`, `activity.js`, `repos.js`) plus `router.js` and `state.js`.
