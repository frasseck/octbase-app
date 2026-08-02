You are a senior SRE setting up the minimum observability Octbase needs so a small (possibly one-person) ops team finds out about problems before the client does. Read `prompts/_release-v01-audit.md` first.

This is the "is it on fire?" step. Keep it minimal and real — every dashboard/alert added must be backed by a metric that actually exists and a config file that's actually checked into the repo.

## Practical steps

1. **Inventory existing metrics**
   ```bash
   grep -rn 'prometheus\|promauto\|MustRegister' octbase-api/internal | sort -u
   ```
   List every metric currently exported (name, type, labels). Compare against the README's claim of "request count/latency by route, SSE connection gauge".

2. **Close metric gaps**
   For a minimal alerting setup, ensure these exist (add any missing ones using the existing `promauto`/middleware pattern, do not introduce a second metrics library):
   - `octbase_http_requests_total{method,path,status}` — confirm `status` includes 4xx/5xx breakdown.
   - `octbase_http_request_duration_seconds` histogram — confirm it has reasonable buckets for a web app (e.g. 5ms–5s).
   - `octbase_sse_connections` gauge — confirm it's decremented on disconnect (cross-check with `step_02`'s SSE cleanup test).
   - `octbase_db_pool_*` (open/idle/in-use connections) — if not exposed, add via `database/sql`'s `DB.Stats()` in a periodic exporter goroutine.
   - `octbase_migration_version` gauge — exposes current schema version (also surfaced on `/health`), useful for catching a deploy that didn't migrate.

3. **Health endpoint detail**
   - Confirm `/api/v1/health` response shape distinguishes:
     - `200` healthy
     - `503` degraded (DB unreachable, migration mismatch)
   - Confirm the response body is safe for unauthenticated callers (no DSN, no internal hostnames, no stack traces) — just `{"status": "...", "checks": {...}, "version": "..."}`.
   - If `version` isn't in the response yet, wire it up (depends on `step_04`'s build-time version injection — coordinate or stub with `"dev"` if that step hasn't run yet).

4. **Prometheus + Alertmanager config (minimal)**
   - Add `deploy/prometheus/prometheus.yml` (or alongside existing compose files) with a scrape config for `octbase-api:8000/metrics`.
   - Add `deploy/prometheus/alerts.yml` with these rules at minimum:
     - `OctbaseHealthCheckFailing` — `/health` returning non-200 for >2 minutes.
     - `OctbaseHighErrorRate` — 5xx rate > 5% of requests over 5 minutes.
     - `OctbaseHighLatency` — p95 request duration > 1s over 5 minutes.
     - `OctbaseDBPoolSaturated` — in-use connections near the configured max (25 per README) for >5 minutes.
     - `OctbaseBackupJobFailed` — based on a `node_exporter` textfile collector or a simple "last successful backup timestamp" metric written by `scripts/backup.sh` (from `step_02`) — have `backup.sh` write `date +%s > /backups/.last-success` and add an alert if that file's mtime is >26h old.
   - If Prometheus isn't part of the current deployment at all, add a `docker-compose.monitoring.yml` (optional add-on compose file) so it's opt-in rather than forced onto every environment, and document it in `docs/operations.md`.

5. **Request correlation IDs**
   - Confirm the HTTP middleware generates/propagates a request ID (check headers like `X-Request-Id`) and that it's included in every structured log line for that request, including error logs from handlers.
   - If missing, add middleware that generates a UUID per request (or honors an inbound `X-Request-Id`), stores it in `context.Context`, and includes it in the logger used by handlers. Add a test asserting two log lines from the same request share the same request ID.

6. **Alerting documentation**
   - In `docs/operations.md`, add an "Alerting Baseline" section: list each alert, what it means in plain language, and the first diagnostic step (e.g. "check `/api/v1/health`, then `docker logs octbase-api`, then check disk space on the Postgres volume").

## Deliverable

Append to `prompts/_release-v01-audit.md`:
- Metrics added/confirmed (table: name, type, labels, purpose).
- New files added under `deploy/prometheus/` (or wherever placed).
- Health endpoint response sample (200 and simulated 503).
- Request-ID propagation test result.

Verification:
```bash
cd octbase-api && go test ./...
curl -s http://localhost:8000/metrics | grep octbase_
curl -s http://localhost:8000/api/v1/health | jq
```
