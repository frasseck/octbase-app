You are a senior backend/infrastructure engineer responsible for Octbase's data durability and graceful-failure behavior ahead of v0.1. Read `prompts/_release-v01-audit.md` (from `step_00`) first for known issues in this area.

Principle: a project management tool that loses or corrupts a team's tasks, or falls over hard when Postgres hiccups, is worse than no tool at all. This step is about making failure boring.

## Practical steps

1. **Migrations: up/down completeness**
   ```bash
   ls octbase-api/migrations/ | sort
   ```
   - Every `NNN_name.up.sql` must have a matching `NNN_name.down.sql`. For any missing `down` migration, write one that reverses the `up` migration exactly (drop added columns/tables/indexes, restore defaults).
   - Add/confirm a test or CI step that runs `migrate up` then `migrate down` to version 0 then `migrate up` again against a throwaway schema, to catch irreversible migrations early:
     ```bash
     migrate -path ./migrations -database "$TEST_DATABASE_URL" up
     migrate -path ./migrations -database "$TEST_DATABASE_URL" down -all
     migrate -path ./migrations -database "$TEST_DATABASE_URL" up
     ```

2. **Backup script**
   - Create `octbase-api/scripts/backup.sh` (or `scripts/` at repo root, matching existing conventions):
     ```bash
     #!/usr/bin/env bash
     set -euo pipefail
     : "${OCTBASE_DATABASE_URL:?OCTBASE_DATABASE_URL is required}"
     : "${BACKUP_DIR:=/backups}"
     mkdir -p "$BACKUP_DIR"
     ts=$(date +%Y%m%d-%H%M%S)
     pg_dump "$OCTBASE_DATABASE_URL" | gzip > "$BACKUP_DIR/octbase-$ts.sql.gz"
     find "$BACKUP_DIR" -name 'octbase-*.sql.gz' -mtime +30 -delete
     ```
   - Create `octbase-api/scripts/restore.sh`:
     ```bash
     #!/usr/bin/env bash
     set -euo pipefail
     : "${OCTBASE_DATABASE_URL:?OCTBASE_DATABASE_URL is required}"
     file="${1:?Usage: restore.sh <backup-file.sql.gz>}"
     gunzip -c "$file" | psql "$OCTBASE_DATABASE_URL"
     ```
   - Update `docs/operations.md` to reference these scripts instead of inline cron snippets, and document how to wire `scripts/backup.sh` into cron/systemd-timer for the actual deployment target.
   - **Verify**: run `backup.sh` against the dev compose database, then `restore.sh` into a throwaway database, and confirm row counts match for a couple of key tables (`tasks`, `users`). Document the exact commands used in the deliverable section.

3. **Graceful degradation under DB outage**
   - With the API running, stop the Postgres container (`podman stop` / `docker stop` the db service) and:
     - Hit `/api/v1/health` — confirm it returns `503` with a JSON body that does NOT include the DSN or raw driver error, just a status field.
     - Hit an authenticated endpoint (e.g. `/api/v1/users/me/dashboard`) — confirm it returns a clean `5xx` with a generic error body, not a panic/stack trace, and the process does not crash (`go run` stays alive).
   - Restart Postgres and confirm `/api/v1/health` returns to `200` without restarting the API.
   - If any of the above fails, fix it in `internal/shared` (DB error handling) and `cmd/octbase-api/main.go` (panic recovery middleware). Add an integration test if the test harness supports stopping/starting a DB connection (e.g. by closing the pool mid-test); otherwise document this as a manual verification step in `docs/operations.md`.

4. **SMTP outage doesn't block requests**
   - Confirm (read `internal/mailer` and its callers in `internal/notifications`) that sending an email is either async (goroutine/queue) or has a short timeout, so a user action that triggers a notification (e.g. assigning a task) doesn't hang waiting on SMTP.
   - If email sending is synchronous and blocking with no timeout, add a timeout (e.g. 5s) via `context.WithTimeout` on the SMTP call, and log+continue on failure rather than failing the parent request.
   - Add a test that simulates a slow/unreachable SMTP host and asserts the triggering API call still completes promptly.

5. **SSE hub cleanup**
   - Read `internal/sse`. Confirm that on client disconnect (request context cancelled), the connection is removed from the hub's subscriber map/slice and any associated goroutine exits.
   - Write a test that opens N SSE connections, disconnects them, and asserts the hub's internal subscriber count returns to 0 (you may need to expose a small test-only accessor or use the existing presence endpoint).

6. **Graceful shutdown verification**
   - Start the API, open an SSE connection, send `SIGTERM` (or run under `podman-compose down` with the 30s timeout configured), and confirm:
     - In-flight HTTP requests complete.
     - The SSE connection receives a clean close (not an abrupt socket reset) within the drain window.
   - If shutdown doesn't close SSE streams before the drain timeout, add explicit SSE hub shutdown handling in `main.go`'s shutdown sequence.

7. **Concurrency spot-checks**
   - Write (or extend existing) Go tests that fire concurrent requests (`go test -race`, with goroutines + `sync.WaitGroup`) at:
     - Task creation in the same project (assert sequence numbers `TB-N` are unique, no duplicates/gaps under race).
     - Sprint completion concurrently with a task status update in that sprint (assert no task ends up in an inconsistent state — e.g. both "moved to backlog" and "still in completed sprint").
     - Bulk action endpoint called twice concurrently on overlapping task IDs (assert final state is consistent, no partial/duplicate updates).
   - Run with `go test -race ./...` and fix any data races or logic races found (likely needs a `SELECT ... FOR UPDATE` or a serializable transaction around sequence-number assignment if not already present).

## Deliverable

Append to `prompts/_release-v01-audit.md`:
- Migration up/down/up verification result.
- Backup/restore verification result (commands + row-count check).
- DB-outage, SMTP-outage, SSE-cleanup, and shutdown test results (pass/fail + fixes made).
- Any race conditions found and how they were fixed, with the `-race` test output.

Verification:
```bash
cd octbase-api && go test -race ./...
```
