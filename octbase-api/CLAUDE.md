# octbase-api — backend contract conventions

These rules were split out of the repository-root `CLAUDE.md` so they load when
work touches `octbase-api/` rather than in every session. The root file keeps
the cross-cutting rules (defaults, seed data, changelog discipline).

- **Auth:** the API is **JWT-only** — every `/api/v1` domain route requires a
  `Bearer` token via `auth.JWTMiddleware` + `shared.LoadUserGlobalRole` (there is
  **no** `X-User-Id` fallback). Public exceptions: `auth/login`, `auth/refresh`,
  `auth/logout`, `auth/mfa/verify`, `auth/forgot-password`,
  `auth/reset-password`, invitation inspect/accept, the OAuth callback
  (`GET /api/v1/oauth/{provider}/callback`), and the HMAC webhook receivers;
  SSE uses
  `auth.OptionalJWTMiddleware` (also accepts `?token=`). Demo user ID
  `00000000-0000-0000-0000-000000000001` is reused across seed, frontend, tests.
- **Error shape:** always `shared.WriteError` → `{"code": "...", "message": "..."}`.
  Business rules use **stable codes** (`TASK_IMMUTABLE`, `TASK_TITLE_REQUIRED`,
  `SLUG_CONFLICT`, `RELEASE_HAS_OPEN_TASKS`, …) that tests assert exactly.
- **Structs are the contract:** handlers return domain structs directly (little
  DTO mapping), so changing a field/JSON tag changes the API immediately.
- **Optimistic locking:** versioned entities (task, project, release, sprint,
  page) update via version-guarded SQL (`WHERE id=… AND version=…`); zero rows →
  `shared.ErrVersionConflict` → **409 `VERSION_CONFLICT`** (map it with
  `shared.WriteUpdateError`). PATCH bodies accept an optional client `version`;
  responses carry the incremented version. New mutable aggregates must follow
  this pattern (see `docs/architecture.md` §3).
- **Activity logging is explicit:** call `activity.Write(...)` after a
  user-visible state change that should appear in the Activity view; it is not a
  DB trigger.
- **Tests are integration-style:** handler tests spin up the real chi router with
  real migrations against Postgres via `internal/testutil` — follow that pattern
  rather than mocking. CI enforces a coverage floor (see the `coverage` skill).
- **Migrations:** add a matching `.up`/`.down` pair. The expected version is now
  **derived from the migration files** (`shared.LatestMigrationVersion`), so there is no
  `expectedMigrationVersion` constant to bump; the test harness reads the migrations
  directory dynamically too.
