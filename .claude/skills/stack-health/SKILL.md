---
name: stack-health
description: Diagnose an unhealthy Octbase stack — 502s, degraded /health, crash loops, containers down — using octbase-operations/check-health.sh and its reaction runbook. Use whenever a deployed/dev stack misbehaves, after a deploy to gate on health, or when asked whether the stack is healthy.
---

# Diagnosing stack health

`dev-stack` covers bringing stacks **up**; this skill covers **"is it healthy,
and what do I do if not"**. The full concept + runbook lives in
`octbase-operations/README.md`; broader ops (backups, TLS, env) in
`docs/operations.md`.

## The probe: `check-health.sh`

```bash
./octbase-operations/check-health.sh --project octbase_dev   # this checkout's stack
./octbase-operations/check-health.sh                          # default project "octbase" (demo)
./octbase-operations/check-health.sh --json                   # machine-readable
./octbase-operations/check-health.sh --quiet                  # summary line only
./octbase-operations/check-health.sh --no-deep                # skip podman-exec probes
```

Every service is graded on **two layers** and reported as the worse of the two:
container layer (running? healthcheck? restart count — ≥5 restarts ⇒ flapping
WARN) and application layer (API `/health` JSON, Caddy HTTP, `pg_isready`). An
"Up" container whose app answers 503 is **DEGRADED**, not OK.

Exit codes: `0` all OK · `1` at least one DEGRADED · `2` at least one DOWN ·
`3` usage/env error. Overall = worst single service.

## Reaction runbook (condensed)

Read the `detail` column — it names the failing layer. Full version:
`octbase-operations/README.md`.

| Symptom | Cause / action |
|---|---|
| `api` DEGRADED, `/health 503`, body `"db":{"status":"error"}` | API can't reach Postgres. Check the postgres line first; if postgres is green, verify `OCTBASE_DATABASE_URL` + shared compose network. |
| `api` DEGRADED, `migrationVersion` ≠ expected | Migrations behind or half-applied. `podman logs <project>_octbase-api_1` for the migrate error; `db-migrations` skill for the dirty-state recovery (`migrate … force`). |
| `api` DEGRADED, `restarts=N` | Crash loop. `podman logs --tail 100 …`. Common: missing/short `OCTBASE_JWT_SECRET` (≥32 bytes required with demo mode off — API refuses to start), unreachable DB at boot. |
| `postgres` DOWN / `pg_isready FAILED` | Root dependency — fix first. `podman logs <project>_postgres_1` (disk-full, corrupt volume). **Never delete the pgdata volume to "fix" startup** — restore from backup (`docs/operations.md`). |
| `frontend` `/` 200 but `/health` not 200 | Caddy serves statics but its reverse proxy to the API is broken — almost always the API itself; else compose network / Caddyfile proxy target. |
| `frontend` 502/503/DOWN | Caddy container down; a Caddyfile syntax error prevents start. `podman logs <project>_octbase-frontend_1`. |
| `mobile` DOWN | Not host-published; reached via the frontend's `/m/` proxy. `podman-compose up -d octbase-mobile`, then re-check `/m/`. |
| anything DOWN, `container missing` | Wrong `--project`, or service never brought up: `podman-compose -p <project> up -d <service>`. |

General recovery:

```bash
podman ps -a --filter "name=<project>_"          # real states + restart counts
podman logs --tail 100 <project>_<service>_1     # why it's unhappy
podman-compose -p <project> up -d <service>      # recreate one service
```

⚠️ When recreating services, respect the checkout↔project mapping: manage
`octbase_dev` from this checkout, and never use a different `-p` from here
without overriding the `.env` (`.env`/pgdata collision, see `dev-stack`).
The public demo is platform-managed under the `oct-demo` account since
2026-07-13 — diagnose it via https://demo.octbase.io and `octbase-service`,
not from this host's podman.

After any fix, re-run the checker and confirm `==> overall: OK`.

## Post-deploy gate

```bash
podman-compose up -d --build
for i in $(seq 1 12); do
  ./octbase-operations/check-health.sh --quiet && break
  sleep 5
done
./octbase-operations/check-health.sh || echo "deploy unhealthy"
```

## Related

- Bringing up / locating stacks, ports, logins → `dev-stack`
- Migration dirty-state recovery → `db-migrations`
- Releasing/deploying → `release`
