---
name: db-migrations
description: Add or run PostgreSQL migrations for octbase-api. Covers the golang-migrate file layout, the up/down convention, the automatically-derived expected migration version used by the health check, and the deterministic seed data that schema changes ripple into. Use whenever changing the database schema or running migrations manually.
---

# Octbase database migrations

The API uses **PostgreSQL** with **golang-migrate** (file source). Migrations
live in `octbase-api/migrations/` as paired files:

```
NNN_name.up.sql     # forward
NNN_name.down.sql   # rollback
```

Numbering is sequential and zero-padded (`001_…` through the current head).
Check `ls octbase-api/migrations | tail` for the current head rather than
trusting a hardcoded count here.

**The history was squashed on 2026-08-03.** `001_baseline` is the whole schema
as of that date; migrations `001`–`039` were removed, and their per-change
rationale lives in git history (`git log -- octbase-api/migrations/`). Add new
migrations after the baseline as normal — nothing about the workflow below
changes. Instances created before the squash need a one-off
`migrate force 1` stamp; see `docs/operations.md`.

## Migrations run automatically

`cmd/octbase-api/main.go` calls `shared.RunMigrations(db, "migrations")` on
startup, so a freshly started API (or compose stack) is always migrated to head.
You normally do **not** run migrations by hand.

## Adding a migration — checklist

1. Create the next `NNN_name.up.sql` **and** `NNN_name.down.sql` pair. The down
   file must actually reverse the up (golang-migrate dirties the DB if a step
   fails, so make them correct).
2. **Nothing to bump.** The expected version is derived automatically from the
   migration files on disk — `main.go` computes it at startup via
   `shared.LatestMigrationVersion(...)` and `/api/v1/health` compares the live DB
   version against it, reporting degraded if they differ. (There is no longer an
   `expectedMigrationVersion` constant to update.)
3. If the schema change affects seeded entities, update
   `internal/seed/seed.go` — **seed data is part of the public dev surface**
   (frontend code and Playwright tests assert fixed IDs, titles, the four-column
   board, the demo page). Expect to update tests and UI assumptions too.
4. Run the Go tests (`testing` skill) — handler tests apply the real migrations
   against a Postgres test schema, so a broken migration fails the suite.

## Running migrations manually (rarely needed)

With the `migrate` CLI (see `docs/operations.md`):

```bash
cd octbase-api
migrate -path ./migrations -database "$OCTBASE_DATABASE_URL" up      # all pending
migrate -path ./migrations -database "$OCTBASE_DATABASE_URL" down 1  # roll back one
```

If a migration fails mid-way the schema is marked *dirty*; fix the SQL, then
`migrate ... force <version>` back to a clean version before retrying.

## Related

- Starting a DB/API → `dev-stack` skill
- Running tests → `testing` skill
