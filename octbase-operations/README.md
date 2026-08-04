# Octbase Operations — Health Observation

Tooling and procedure for **observing the health of every container and the app
as a whole**, and a runbook for **how to react** when a check goes red.

This folder is the operator's entry point. For deployment, env vars, backups,
TLS and metrics see the broader [`../docs/operations.md`](../docs/operations.md);
this folder is specifically the *"is it healthy, and what do I do if not"* layer.

---

## What's here

| File | Purpose |
|---|---|
| `check-health.sh` | One command that probes the whole stack and exits non-zero when something is wrong. Safe to run on a cron / from a monitor. |
| `stamp-baseline.sh` | Repairs instances that will not start after a migration history rewrite (the `001_baseline` squash). Runs as **root** across the client registry; dumps first, decides 38-vs-39 per instance, stamps, restarts, waits for health. `DRY_RUN=1` inspects without changing anything. |
| `repair-039-poststamp.sql` | The follow-up for an instance stamped *without* applying `039` first. Adds only the DDL — the version row is already correct, so do **not** re-stamp. Guarded and transactional: a no-op if the instance was genuinely a 39. |
| `README.md` | This document — the concept and the reaction runbook. |

---

## The stack being observed

Octbase runs as a single `podman-compose` project. The services and how this
tool reaches each one:

| Service | Container (`<project>_…_1`) | Internal port | App-layer probe |
|---|---|---|---|
| **postgres** | `_postgres_1` | 5432 | `pg_isready` (via `podman exec`) + podman healthcheck |
| **octbase-api** | `_octbase-api_1` | 8000 | `GET /health` → JSON `{status, db, migrationVersion}` |
| **octbase-frontend** | `_octbase-frontend_1` | 8080 | `GET /` (Caddy) + `GET /health` (proxy → API) + `GET /m/` (proxy → mobile) |
| **octbase-mobile** | `_octbase-mobile_1` | 8080 | container state (not host-published; also exercised via the frontend's `/m/`) |

`<project>` defaults to `octbase`. The long-lived dev stack in this environment is
`octbase_dev`.

---

## Concept: two layers, worst-of-two

A container being **Up** is not the same as the service being **healthy**. The API
container stays "Up" while its database connection is down or its migrations are
behind — `/health` then returns `503`. So every service is graded on **two layers**
and reported as the *worse* of the two:

1. **Container layer** — `podman inspect`: is it `running`, what is its
   healthcheck `Health.Status`, how many times has it restarted (flapping ≥ 5
   restarts ⇒ `WARN`).
2. **Application layer** — does the service actually answer correctly? This is
   what distinguishes a hung-but-running container from a working one.

States and exit codes:

| State | Meaning | Exit code |
|---|---|---|
| `OK` | container running **and** app answering | `0` |
| `DEGRADED` (`WARN`) | running but unhealthy: 503, flapping, migrations behind, deep probe failing | `1` |
| `DOWN` | container missing or not running | `2` |

The **overall** result is the worst single service. The script exits with that
state's code so cron/monitoring can branch on it.

---

## How to apply it

### Manually / ad-hoc

```bash
./octbase-operations/check-health.sh                  # default project "octbase"
./octbase-operations/check-health.sh --project octbase_dev
./octbase-operations/check-health.sh --quiet          # just the summary line
./octbase-operations/check-health.sh --no-deep        # skip exec probes (restricted hosts)
```

Output is one line per service plus an `==> overall:` summary; colourised on a TTY.

### From a monitor / alerting pipeline (JSON)

```bash
./octbase-operations/check-health.sh --json
# {"project":"octbase","overall":"DEGRADED","ts":"…","services":{"api":{"state":"DEGRADED","detail":"…/health 503…"}, …}}
```

Pipe to your alert tool, or branch on the exit code:

```bash
if ! ./octbase-operations/check-health.sh --json >/var/log/octbase-health.json; then
  mail -s "Octbase health: $(jq -r .overall /var/log/octbase-health.json)" oncall@beyags.com \
    < /var/log/octbase-health.json
fi
```

### On a schedule (cron)

```cron
# Every 5 minutes; only mail on a non-zero exit (degraded or down).
*/5 * * * * /home/.../octbase/octbase-operations/check-health.sh --quiet --json \
              > /var/log/octbase-health.json 2>&1 \
            || mail -s "Octbase UNHEALTHY" oncall@beyags.com < /var/log/octbase-health.json
```

### In CI / post-deploy gate

After `podman-compose up -d --build`, give the stack a moment and gate on health
before declaring the deploy good:

```bash
podman-compose up -d --build
for i in $(seq 1 12); do
  ./octbase-operations/check-health.sh --quiet && break
  sleep 5
done
./octbase-operations/check-health.sh || { echo "deploy unhealthy — rolling back"; exit 1; }
```

---

## How to react — runbook

Read the `detail` column; it names the failing layer. Then:

### `api` — DEGRADED, `/health 503`
The API is up but reports `degraded`. The body says why:

- **`"db":{"status":"error"}`** — the API can't reach Postgres.
  1. Check Postgres first: it'll usually also be red here. `podman logs <project>_postgres_1`.
  2. If Postgres is healthy, it's a connectivity/credential issue — verify
     `OCTBASE_DATABASE_URL` and that both containers share the compose network.
- **`migrationVersion` ≠ expected** — migrations are behind (or a migration failed
  mid-deploy). Check `podman logs <project>_octbase-api_1` for the migrate error.
  See the migration runbook in [`../docs/operations.md`](../docs/operations.md#running-migrations-manually);
  roll the bad migration back with `migrate … down 1` if a deploy half-applied.

### `api` — DEGRADED, `restarts=N`
A crash loop. `podman logs --tail 100 <project>_octbase-api_1` for the panic /
fatal. Common causes: missing/short `OCTBASE_JWT_SECRET` (≥32 bytes required with
demo mode off — the API refuses to start), or an unreachable DB at boot.

### `api` — DOWN / crash-looping straight after an upgrade

If the API stopped starting on the deploy that upgraded it, suspect the database
before the code. An instance whose `schema_migrations` version has no migration
file in the new build cannot be migrated: `main.go` treats that as fatal, so the
container never binds a port. Current builds say so plainly —

```
database migration version is ahead of the migrations on disk: database records
version 38 but the highest migration on disk is 2. …
```

— older ones report golang-migrate's own wording,
`no migration found for version 38: read down for version 38`, which reads like a
corrupt file rather than a database from before a history rewrite.

**This does not look the way the rest of this runbook trains you to expect.**
On 2026-08-04 it took down two client stacks, and neither presented as an `api`
problem from outside:

- The front door still answered `/` with **200**. Only `/api/*` returned 502,
  because Caddy served the static SPA fine and had nothing to proxy to. The
  login page rendered normally and simply could not log in.
- On one stack the API's host port was **still listening** while the container
  behind it was dead — rootless podman's port-forwarder outlives the container
  it forwards to. *A listening port is not evidence the API is alive.* The other
  stack had stopped entirely and did not listen at all. Same root cause, two
  different signatures.

Repair with [`stamp-baseline.sh`](stamp-baseline.sh) (root; `DRY_RUN=1` first).
The one thing not to shortcut is **which version the instance stopped at** — a
38 never ran `039`, so stamping it straight to the baseline records columns,
foreign keys and indexes as applied that do not exist. The database then reports
healthy while the activity feed 500s. Nothing self-repairs on purpose: stamping
is an assertion golang-migrate cannot verify and never re-checks.

Verify afterwards against the schema, not against `/health` — `/health` only
echoes the version row you just wrote:

```bash
psql … -tAc "SELECT (SELECT count(*) FROM pg_constraint
                       WHERE conname LIKE 'fk_activity_entries_%'),
                    (SELECT count(*) FROM information_schema.columns
                       WHERE table_name='activity_entries'
                         AND column_name IN ('release_id','sprint_id','target_deleted'))"
# expect 4 | 3 — if short, apply repair-039-poststamp.sql (DDL only, do not re-stamp)
```

Full procedure and the reasoning behind it:
[`../docs/operations.md`](../docs/operations.md) → "Stamping an instance created
before the 001_baseline squash".

### `postgres` — DOWN / `pg_isready FAILED` / `healthcheck=unhealthy`
The database is the root dependency — fix this before anything else.
1. `podman logs <project>_postgres_1`. Disk-full and corrupt-volume show here.
2. If it won't start, confirm the data volume (`pgdata*`) is intact and mountable.
3. **Do not** delete the volume to "fix" startup — that destroys all data. Restore
   from backup instead (see [`../docs/operations.md`](../docs/operations.md#database-backups)).

### `frontend` — `/ 200` but `/health` not 200
Caddy is serving static files but its **reverse proxy to the API is broken**
(`/health` is proxied to `octbase-api:8000`). Almost always the API itself is the
problem — check the `api` line. If the API is OK, suspect the compose network or
the `Caddyfile` proxy target.

### `frontend` — `/ 502` / `503` / `DOWN`
Caddy container is down or misconfigured. `podman logs <project>_octbase-frontend_1`.
A `Caddyfile` syntax error stops it from starting.

### `mobile` — DOWN
Phones get a broken app even though desktop is fine. The mobile container is not
host-published; the frontend's `/m/` proxies to it. Restart it and confirm the
frontend's `/m/` proxy still resolves:
`podman-compose up -d octbase-mobile`.

### Anything — DOWN (`container missing`)
The expected container name doesn't exist for that project. Either you passed the
wrong `--project`, or the service was never brought up:
`podman-compose -p <project> up -d <service>`.

### General recovery
```bash
podman ps -a --filter "name=<project>_"          # see real states + restart counts
podman logs --tail 100 <project>_<service>_1     # why it's unhappy
podman-compose -p <project> up -d <service>      # recreate one service
```

After any fix, re-run the checker and confirm `==> overall: OK` before standing down.

---

## Extending the checks

Add a service by writing a `check_<name>` function in `check-health.sh` that calls
`check_container_layer "<container>"`, layers on an app probe (`http_probe` for HTTP,
`exec_probe` for in-container commands), and ends with
`record <name> <state> <detail>`. Then call it in the "Run all checks" block.
Keep the worst-of-two-layers rule so an Up-but-broken service never reads green.
