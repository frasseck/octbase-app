You are a senior SRE. Your job is to build a **monitoring solution that observes
every single container of every single Octbase instance** on the fleet, and pages
a human when something is wrong. Each client runs an isolated `podman-compose`
stack (postgres + octbase-api + octbase-frontend + octbase-mobile — **no
Mailpit**; it is dev-overlay-only since 2026-07-02, so ~4 containers per
instance, not 5) under
its own Linux user — see `80_octbase-deployment.md` and
[`docs/hosting-concept.md`](../docs/hosting-concept.md) for how instances are laid
out. "Every container of every instance" means: for N clients you are watching
~5×N containers plus the shared machine edge (main Caddy) and the host itself.

Ground truth before you start:
- `octbase-operations/check-health.sh` already grades **one** stack with a
  worst-of-two-layers rule (container layer via `podman inspect` + application layer
  via `/health`, `/`, `/m/`) and emits `--json` and exit codes. Read
  `octbase-operations/README.md` — its reaction runbook is the model for alert
  responses. **Extend this; do not replace its logic.**
- The API already exposes Prometheus metrics at `/metrics` (request count/latency by
  route, SSE connection gauge) and a deep `/api/v1/health` (DB pool + migration
  version, 503 when degraded). Postgres has a compose healthcheck.
- Instances are discoverable by convention: compose project `octbase-<client_id>`,
  and `deploy/ansible/clients/*.yml` is the source of truth for which clients exist.

## What to build (put it under `octbase-operations/monitoring/`)

Layer the solution — cheap breadth first, rich depth second — and label **every**
signal with the tenant so you can answer "which client is down" instantly.

1. **Fleet health sweep (breadth, always-on).** Generalize the single-stack check
   to the whole fleet:
   - Add `octbase-operations/check-fleet.sh` that enumerates every
     `octbase-<client_id>` compose project on the host (from `podman ps` and/or the
     ansible `clients/` inventory), runs the per-stack `check-health.sh --json`
     for each, and aggregates into one fleet JSON:
     `{ "ts": …, "overall": "DEGRADED", "clients": { "acme": {overall, services:{…per container…}}, … } }`.
   - It must list **every container** it expected per client (5 services) and flag a
     **missing** container as `DOWN`, not silently skip it — a client with only 4/5
     containers up is a failure.
   - Exit non-zero on any client degraded/down so it is cron- and CI-safe, exactly
     like the single-stack script.

2. **Metrics & container-level observability (depth).** Stand up a small monitoring
   stack (its own `podman-compose.monitoring.yml`, one per host or one central):
   - **Prometheus** scraping, per instance, `octbase-api:/metrics` and probing
     `/api/v1/health`. Give every target a `client="<id>"` and `instance_dns` label
     (generate the scrape config from the same `clients/*.yml` inventory so new
     clients appear automatically — a small templating step or file_sd).
   - **cAdvisor** (or `podman stats`/Prometheus podman exporter) so you get
     **per-container** CPU, memory, restart count, and OOM-kills for every container
     of every stack. This is the literal "monitor every single container"
     requirement — a container that is `Up` but pinned at its memory limit and
     OOM-looping must be visible. Tie the container→client mapping via the compose
     project label (`io.podman.compose.project`) so panels group by tenant.
   - **node_exporter** for host CPU/mem/disk (density means one full disk kills every
     tenant — watch `pgdata` volume growth and free space hard).
   - **blackbox_exporter** hitting each `https://<dns_name>/health` from outside, so
     you catch edge/TLS/cert-expiry problems the in-host checks can't see.

3. **Alerting.** Wire **Alertmanager** with tenant-aware routing and sensible rules
   grounded in the existing runbook:
   - API `/health` returning 503 for >2m (DB down or migrations behind →
     runbook "api — DEGRADED, /health 503").
   - Container restart rate high / OOM-killed (→ "api — DEGRADED, restarts=N").
   - Postgres healthcheck failing / `pg_isready` red (→ "postgres — DOWN", the root
     dependency — page hardest on this).
   - Any expected container missing for a client.
   - Host disk >85%, cert expiry <14 days, blackbox probe failing.
   - Route by severity: page (PostFinance-paying client down = P1) vs. ticket
     (single flapping mobile container). Include the `client` label in every alert
     so on-call knows the blast radius immediately.

4. **Dashboards.** A **Grafana** dashboard with a fleet overview (one row/tile per
   client, red/amber/green from the worst container) and a per-client drill-down
   (the 5 containers' CPU/mem/restarts + API latency/SSE gauge + DB pool). A new
   client provisioned by the ansible play should appear without editing dashboards
   by hand (template variable driven by the `client` label).

5. **Schedule + escalation.** Cron the fleet sweep every 1–5 min emailing on
   non-zero exit (mirror the cron example in `octbase-operations/README.md`, but
   fleet-wide and to `oncall@beyags.com`). Prometheus/Alertmanager is the primary
   real-time path; the sweep is the belt-and-suspenders that also catches a
   completely dead host where the metrics stack itself is down.

## Constraints / do-not-break
- **Rootless podman:** the exporters must read each client user's containers.
  Decide and document the access model (a per-user exporter sidecar in each stack vs.
  a privileged host-level collector reading all users' podman sockets) and its
  security trade-off — do not run the whole monitoring stack as root just because
  it is easy.
- **Isolation:** monitoring one client must never be able to read another client's
  application data — you are collecting container/metrics telemetry, not tenant DB
  contents. `/metrics` exposes no PII; keep it that way and keep it network-restricted
  (the per-instance `Caddyfile.tls` already limits `/metrics` to private ranges — do
  the same at the edge).
- Reuse `check-health.sh`'s state model (`OK`/`DEGRADED`/`DOWN`, worst-of-two) so the
  two layers agree and the runbook language stays valid.

## Deliverable summary
- `octbase-operations/check-fleet.sh` + tests/example JSON output.
- `octbase-operations/monitoring/` compose stack (Prometheus, cAdvisor, node_exporter,
  blackbox_exporter, Alertmanager, Grafana) with generated, inventory-driven scrape
  config and provisioned dashboards + alert rules.
- Extend `octbase-operations/README.md` with a "Fleet monitoring" section: what each
  layer watches, how a new client is auto-discovered, and an alert→runbook mapping
  table (each alert points at the existing reaction runbook entry).

## Verification
```bash
# Fleet sweep sees every container of every stack and fails on any gap:
./octbase-operations/check-fleet.sh --json | jq '.clients | to_entries[] | {client:.key, overall:.value.overall}'
# Prove it catches a killed container (not just a stopped stack):
podman kill octbase-acme_octbase-api_1
./octbase-operations/check-fleet.sh; echo "exit=$?"      # expect non-zero, acme DEGRADED/DOWN
# Metrics + alerts:
curl -s localhost:9090/api/v1/targets | jq '.data.activeTargets[] | {job:.labels.job, client:.labels.client, health:.health}'
promtool check rules octbase-operations/monitoring/alerts/*.yml
# Simulate DB loss on one client and confirm exactly one client's API alerts (blast radius = 1 tenant):
podman stop octbase-acme_postgres_1   # expect acme /health 503 alert only
```
Add a second client and confirm it shows up in the sweep, Prometheus targets, and
the Grafana fleet overview **without hand-editing config**.
