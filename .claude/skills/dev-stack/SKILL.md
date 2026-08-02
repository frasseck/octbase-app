---
name: dev-stack
description: Bring up or locate a seeded Octbase API/stack for development, manual testing, curl smoke tests, or Playwright. Covers the long-lived compose stacks already running in this environment, running the Go API locally against Postgres, ports, demo logins, and disposable stacks. Use whenever you need a running API before testing or investigating anything.
---

# Running the Octbase stack

Almost everything (frontend tests, screenshots, curl checks, manual investigation)
needs a **seeded API reachable over HTTP**. Prefer reusing an already-running
stack over starting a new one.

## What's usually already running

One long-lived `podman-compose` stack normally exists in this environment,
driven by **this checkout and its `.env`**. Check with `podman ps`:

| Stack (`-p` name) | Checkout | Postgres | API | Frontend | Mailpit |
|---|---|---|---|---|---|
| `octbase_dev` | `/home/claude/dev.ocete.ch` (this repo; plain `podman-compose up`) | `localhost:8102` | `localhost:8101` | `localhost:8100` | — |

These come from this checkout's `.env` (`POSTGRES_PORT` / `API_PORT` /
`FRONTEND_PORT`), which is the authority — read it rather than this table if the
two disagree. They moved from 5433/8001/8081 on 2026-07-26 and this table did
not follow until 2026-08-01.

> The old `octbase` demo stack (`/home/claude/demo.ocete.ch`, ports
> 5432/8000/8080) is **gone since 2026-07-13** — the public demo migrated to
> its own `oct-demo` account (managed by `octbase-service`, not reachable
> from this account). Test the demo via **https://demo.ocete.ch**; ports
> 8000/5432 are usually free for local/disposable use now.

> ⚠️ **Never run compose with a different `-p` from this checkout without
> overriding the `.env`.** `-p <other>` does not override
> `COMPOSE_PROJECT_NAME=octbase_dev`, the dev ports, or
> `PGDATA_DIR=./pgdata_dev` — a second Postgres on the **same data
> directory** as the live dev stack risks corruption; disposable stacks must
> set their own ports and `PGDATA_DIR`. Verify before relying on the dev
> stack:
>
> ```bash
> curl -s -X POST http://127.0.0.1:8101/api/v1/auth/login \
>   -H "Content-Type: application/json" \
>   -d '{"email":"demo@octbase.dev","password":"demopass1234"}'
> ```
>
> A stale port here is worse than no port: this line used to read `:8000`,
> which the dev stack has not answered since 2026-07-26. Nothing listens there
> now, and the day something does it will belong to another session — so the
> check meant to prove the stack is yours would have been the thing that
> handed you someone else's.

## A fixed port is not yours — check before you bind, prove it after

**This environment runs concurrent sessions.** Every port in the table above is
a fixed number that another session may already hold, and the failure is
silent in both directions: your process logs `bind: address already in use`
**to its log file** and exits, while every check you would naturally run keeps
answering "fine" — the port responds, `/api/v1/health` returns 200, and the
seeded demo login succeeds because the fixtures are byte-identical on every
stack. From there on you are driving someone else's API.

Not hypothetical: on 2026-07-31 one session ran two full test files, and a
password change, against another session's stack before the mismatch surfaced
(OCT `23fce744`; the `vite preview` half is `e4d0f2d7`).

**Before starting anything on a fixed port:**

```bash
ss -lptn 'sport = :8000'        # one line (the header) = free
```

Read it carefully — `ss` prints its header even when nothing listens, so
"output" is not the signal, a `LISTEN` **row** is. When a row appears, its
`Process` column names the pid only for **your own** processes; it is empty for
a process owned by another account, which in this environment is exactly the
case you care about. Empty Process column = someone else's, and you cannot
`kill` your way out of it.

**After starting, prove the stack that answers is yours.** Pick a discriminator
that actually differs between stacks:

```bash
ss -lptn 'sport = :8000'        # the pid must be the process YOU started
curl -s http://127.0.0.1:8000/api/v1/health   # migrationVersion, not version
```

`migrationVersion` works as the discriminator — a build from your working tree
is usually ahead of a stack someone started days ago (37 vs 36 in the incident
above). The **app version does not**: every dev stack reports `beta`. Neither
does a successful demo login.

If either check disagrees, stop and pick another port rather than "just using"
the stack that answered — the next write lands in another session's data.

## Demo logins (when `OCTBASE_DEMO_MODE=true`)

| Email | Password | Role |
|---|---|---|
| `super@octbase.dev` | `superpass1234` | SUPER_ADMIN |
| `demo@octbase.dev`  | `demopass1234`  | ADMIN |

Seed data is deterministic — fixed IDs, the Demo Project, its four-column board,
a published page. Tests and the UI depend on it. The reused demo user ID is
`00000000-0000-0000-0000-000000000001`.

The passwords above are what **today's** `seed.go` writes, and seeding only runs
on an empty database. A long-lived stack seeded before the 1.1.2 commit
`66aebda` ("the demo accounts stop holding passwords the app would refuse")
still holds the shorter originals, so this login answers `INVALID_CREDENTIALS`
on a stack that is otherwise healthy — stale seed data, not the wrong stack and
not a broken build. Reseed, or point at a fresh database, rather than hunting
for the old credentials.

## Health / introspection endpoints

| URL | What |
|---|---|
| `/api/v1/health` | DB pool status + current migration version |
| `/docs` | OpenAPI UI |
| `/metrics` | Prometheus metrics |

The API logs `migrationVersion`; it should equal the latest migration version,
which is **derived from the migration files** at startup via
`shared.LatestMigrationVersion` (no hardcoded constant to bump). Health degrades
if the applied version doesn't match.

## Run the API locally (without compose)

The API **auto-runs migrations on startup** and seeds when `OCTBASE_DEMO_MODE=true`,
so you only need a reachable Postgres. The migrations path is **relative**
(`migrations`), so you must run from the `octbase-api/` directory.

```bash
ss -lptn 'sport = :8000'   # FIRST: the port must be free, or you test their stack
cd /home/claude/dev.ocete.ch/octbase-api
OCTBASE_DEMO_MODE=true \
OCTBASE_DATABASE_URL="postgres://octbase:octbase@localhost:5432/octbase?sslmode=disable" \
PORT=8000 \
go run ./cmd/octbase-api
```

`OCTBASE_DATABASE_URL` defaults to the line above if unset. Use a **separate
database** if you don't want to touch a running stack's data.

If you need the MFA endpoints (or `test_settings.py`), also set
`OCTBASE_MFA_ENC_KEY` to a 32-byte key (`openssl rand -base64 32`) — without
it, MFA enrollment 500s. Compose stacks read it from their `.env` (default
empty).

## Bring up a fresh disposable stack

From the repo root, give it its own project name, ports **and Postgres data
directory** so it can't collide (without `PGDATA_DIR` the local `.env` would
point it at `./pgdata_dev` — the live dev stack's data dir):

```bash
# FIRST: every port you are about to claim must be free (header line only).
ss -lptn 'sport = :5434 or sport = :8002 or sport = :8084'

COMPOSE_PROJECT_NAME=octbase_test POSTGRES_PORT=5434 API_PORT=8002 \
FRONTEND_PORT=8084 PGDATA_DIR=./pgdata_test \
podman-compose -p octbase_test up -d
```

Tear down with `podman-compose -p octbase_test down -v` and remove the
`pgdata_test` directory.

The base compose file has **no Mailpit** (it is the deployable unit; mail logs
to stdout). To exercise real mail sending with captured email, layer the
dev-only overlay: `podman-compose -f podman-compose.yml -f podman-compose.dev.yml up -d`
— the Mailpit UI/API then run at `http://127.0.0.1:8025/mailpit/` (basic auth
`octbase:octbase`). Never deploy the overlay.

## Related

- Running tests against a stack → `testing` skill
- Browser/Playwright/screenshots → `frontend-testing` skill
- Schema changes → `db-migrations` skill
