# Octbase Operations Runbook

## Environment Variables

| Variable | Type | Default | Required | Description |
|---|---|---|---|---|
| `OCTBASE_DATABASE_URL` | string | `postgres://...localhost...` | **Yes** (prod) | PostgreSQL DSN. Use `sslmode=require` or `verify-full` for any database not on the private container network (compose: set `POSTGRES_SSLMODE`) |
| `OCTBASE_MIGRATE_DATABASE_URL` | string | *(empty)* | No | DSN migrations run as. Set it to the schema **owner** and point `OCTBASE_DATABASE_URL` at a restricted DML-only role to separate migrate-time DDL from runtime traffic. Empty (the default) = one role does both, unchanged legacy behaviour. See "Least-privilege runtime database role" below |
| `OCTBASE_DB_MAX_OPEN_CONNS` | int | `25` | No | Max open DB connections per API instance. Lower when many instances share one Postgres — see hosting-concept.md §4 |
| `OCTBASE_DB_MAX_IDLE_CONNS` | int | `5` | No | Max idle DB connections per API instance (clamped to max-open) |
| `OCTBASE_DB_STATEMENT_TIMEOUT` | duration | `30s` | No | Per-statement Postgres timeout on the runtime pool (a runaway query otherwise exhausts the pool); `0` disables; not applied to the migration connection |
| `OCTBASE_JWT_SECRET` | string | `dev-secret-…` (demo mode only) | **Yes** (prod) | 32+ random bytes, base64. The dev default exists **only** when `OCTBASE_DEMO_MODE=true`; outside demo mode the API refuses to start without a 32+-byte secret. Rotate causes all users to re-login |
| `OCTBASE_JWT_ACCESS_TTL` | duration | `15m` | No | Access token lifetime |
| `OCTBASE_JWT_REFRESH_TTL` | duration | `1h` | No | Refresh-token / sliding session lifetime (rotated on each use) |
| `OCTBASE_CORS_ORIGIN` | string | `http://localhost:8080` | **Yes** (prod) | Allowed CORS origin |
| `OCTBASE_TRUSTED_PROXIES` | string | *(empty)* | **Yes** (behind a proxy) | Comma-separated IPs/CIDRs whose `X-Forwarded-For` is honored for per-IP rate limiting and audit-log client IPs. Empty = forwarding headers ignored (safe default, but all clients then share one rate-limit bucket). Set to the API's immediate peer — for the bundled stack, the compose network **subnet** (`podman network inspect <project>_default`), never a container `/32` (reassigned on recreate) and never a public IP. See "Recovering client IPs" below |
| `OCTBASE_FRONTEND_TRUSTED_PROXIES` | string | *(empty)* | No | **Read by the `octbase-frontend` container, not the API.** Addresses whose `X-Forwarded-For` the front-door Caddy preserves instead of overwriting. Empty = trusts nobody (safe in every topology). Set to `private_ranges` **only** with `FRONTEND_BIND_ADDR=127.0.0.1` — see "Recovering client IPs" below |
| `FRONTEND_BIND_ADDR` | string | `0.0.0.0` | No | Interface the front door publishes on (compose-level, not read by the API). `127.0.0.1` when an edge proxy on the same host fronts the stack. Deliberately separate from `BIND_ADDR`, which governs Postgres/API |
| `OCTBASE_SITE_AUTH` | string | *(empty)* | No | **Read by the `octbase-frontend` container, not the API.** Installation-password on/off switch: set to `on` (with a bcrypt `OCTBASE_SITE_PASSWORD_HASH`) to make the Caddy front door prompt for HTTP Basic Auth before serving the browser-facing app (desktop, mobile `/m/`, static pages, docs). Empty = open (historical behavior). Excludes `/api/*` and `/health` so JWT auth, HMAC webhooks, SSE and health probes are unaffected. See "Installation password" below |
| `OCTBASE_SITE_PASSWORD_HASH` | string | *(empty)* | No | bcrypt hash (from `caddy hash-password`) checked when `OCTBASE_SITE_AUTH=on` (frontend container). The shell-less Caddy image can't hash a plaintext at runtime, so supply the hash |
| `OCTBASE_SITE_USER` | string | `octbase` | No | Username shown in the front-door Basic Auth prompt when the gate is on (frontend container) |
| `OCTBASE_SMTP_HOST` | string | *(empty)* | No | SMTP host; empty = log to stdout |
| `OCTBASE_SMTP_PORT` | string | `587` | No | SMTP port |
| `OCTBASE_SMTP_FROM` | string | `noreply@beyags.com` | No | Sender address |
| `OCTBASE_SMTP_USER` | string | *(empty)* | No | SMTP username |
| `OCTBASE_SMTP_PASS` | string | *(empty)* | No | SMTP password |
| `OCTBASE_WEBHOOK_SECRET_BITBUCKET` | string | *(empty)* | No | HMAC secret for Bitbucket webhooks |
| `OCTBASE_WEBHOOK_SECRET_GITHUB` | string | *(empty)* | No | HMAC secret for GitHub webhooks |
| `OCTBASE_APP_URL` | string | `http://localhost:8080` | **Yes** (prod) | The real frontend origin. Embedded in invitation and password-reset email links and notification deep-links, and the OAuth callback redirects back to it. Outside demo mode the API refuses to start without it (the localhost fallback would email dead links) |
| `OCTBASE_APP_VERSION` | string | `beta` (build default) | No | App version string surfaced at `/health`, `/api/v1/version`, `/api/v1/config`, and the desktop app's version tag. Unstamped builds show `beta`; stamp the real release version per deployment |
| `OCTBASE_SCM_ENC_KEY` | string | *(empty)* | For SCM | 32-byte AES-256 key (base64/hex) encrypting stored SCM access tokens — required before any repository connection can be saved |
| `OCTBASE_MFA_ENC_KEY` | string | *(empty)* | For MFA | 32-byte AES-256 key (base64/hex) encrypting users' TOTP secrets — required before any user can enroll in MFA. Deliberately separate from `OCTBASE_SCM_ENC_KEY` |
| `OCTBASE_MFA_CHALLENGE_TTL` | duration | `5m` | No | Lifetime of the short-lived MFA login-challenge token issued between password check and second-factor verification |
| `OCTBASE_OAUTH_REDIRECT_BASE` | string | *(empty)* | For OAuth | Base URL for the SCM OAuth callback (`<base>/api/v1/oauth/<provider>/callback`); needed with the per-provider `OCTBASE_OAUTH_<GITHUB\|GITLAB\|BITBUCKET>_CLIENT_ID`/`_CLIENT_SECRET` pairs |
| `OCTBASE_OAUTH_<PROVIDER>_AUTH_URL` / `_TOKEN_URL` / `_SCOPE` | string | provider cloud defaults | No | Per-provider OAuth endpoint/scope overrides for self-hosted GitLab / GitHub Enterprise |
| `OCTBASE_FEATURE_TASKVIEW` | bool | `true` | No | Feature toggle exposed to the SPA via `GET /api/v1/config`; `false` hides the Task view |
| `OCTBASE_DEMO_MODE` | bool | `false` | No | Seeds demo data on startup |
| `OCTBASE_BOOTSTRAP_ADMIN_EMAIL` | string | — | No | Login of the installation's first `SUPER_ADMIN`, created at startup while the users table is still empty (`internal/bootstrap`). Ignored once the installation has users, so it is safe to leave set |
| `OCTBASE_BOOTSTRAP_ADMIN_PASSWORD_HASH` | string | — | No | That admin's initial password, as a **bcrypt hash** — generate with `htpasswd -bnBC 12 "" '<pw>' \| tr -d ':\n'`. A cleartext value is rejected at startup rather than stored. Must be set together with `OCTBASE_BOOTSTRAP_ADMIN_EMAIL` |
| `OCTBASE_SECURE_COOKIES` | bool | `false` | **Yes** (prod) | Set `true` behind TLS so the refresh cookie carries the `Secure` flag |
| `OCTBASE_LOG_LEVEL` | string | `info` | No | `debug`/`info`/`warn`/`error` |
| `OCTBASE_AUDIT_RETENTION_DAYS` | int | `365` | No | Days audit-log rows (IP + user agent) are kept before the daily purge; `0` disables (see "GDPR & Data Subject Requests") |
| `OCTBASE_ACTIVITY_RETENTION_DAYS` | int | `365` | No | Days activity-feed rows are kept before the daily purge; `0` disables |
| `ATTACHMENTS_DIR` | string | `./attachments` | No | **Compose-level, not read by the API.** Host directory backing the attachments volume. Mounted `:U` so podman chowns it to the API's non-root UID. Each stack needs its own, like `PGDATA_DIR` |
| `OCTBASE_ATTACHMENTS_DIR` | string | `/data/attachments` | No | Directory **inside the API container** where uploaded task-attachment files are stored; must match the volume's mount target. If it cannot be created, file uploads are disabled (external-link attachments still work) |
| `OCTBASE_MAX_UPLOAD_MB` | int | `10` | No | Maximum size of a single uploaded attachment, in MiB; `0` disables the limit |
| `OCTBASE_MAX_USER_STORAGE_MB` | int | `512` | No | Total stored attachment size allowed per user, in MiB; `0` disables the quota. Over-quota uploads answer `413 STORAGE_QUOTA_EXCEEDED` |
| `OCTBASE_MAX_USERS` | int | `5` | No | Installation-wide account limit, including the admin (every non-deleted account counts); `0` disables. Enforced on user creation and invitation create/accept with `403 USER_LIMIT_REACHED` |
| `OCTBASE_REQUIRE_MFA` | string | `off` | No | MFA enforcement scope: `off` / `admins` (ADMIN + SUPER_ADMIN) / `all`. An in-scope login without MFA returns a scoped enrollment challenge instead of a session |
| `OCTBASE_EDITION` | string | `ENTERPRISE` | No | Deployment edition `TEAM`/`BUSINESS`/`ENTERPRISE` (case-insensitive; invalid falls back to `ENTERPRISE`). Gates optional product surface; exposed via `/api/v1/config` |
| `OCTBASE_OPTION_JIRA_IMPORT` | bool | `false` | No | Bookable add-on: enables Jira CSV import on the `BUSINESS` edition (ignored with a warning on `TEAM`; `ENTERPRISE` always includes it) |
| `PORT` | string | `8000` | No | HTTP listen port |

The public marketing/landing site and its contact-form mailer are a **separate
website** (`ocete.ch` repo) with their own `WEB_*` variables — they are no longer
part of this stack.

---

## Recovering client IPs (per-client rate limiting & audit source IPs)

Per-IP rate limiting (`/api/v1/auth/*` 120/min, `/api/v1/users` 60/min) and
audit-log source addresses are only per-*client* if the client's IP survives the
whole path to the API. Out of the box it does **not**, and every client on the
installation shares one bucket — ordinary login traffic can then 429 real users.

**Why.** Rootless podman NATs published-port traffic: a container sees its *own*
address as the peer for every caller, so no caller address survives the
port-forward boundary. The front-door Caddy therefore has nothing truthful to
forward, and overwrites `X-Forwarded-For` with that address.

**The supported fix** is an edge proxy on the host, with the front door closed to
everything else. Three settings, and **the order matters** — each one only widens
trust after the previous one has closed a door:

1. **Close the front door.** `FRONTEND_BIND_ADDR=127.0.0.1`, and point the edge
   proxy at `127.0.0.1:<FRONTEND_PORT>` — *not* at the host's public IP. Verify
   with `ss -ltnp | grep <port>`: it must show `127.0.0.1:<port>`, not `*:<port>`.
2. **Let the chain through.** `OCTBASE_FRONTEND_TRUSTED_PROXIES=private_ranges`,
   so the front-door Caddy preserves the edge's `X-Forwarded-For` and appends its
   peer instead of overwriting.
3. **Trust exactly one hop at the API.** `OCTBASE_TRUSTED_PROXIES=<compose subnet>`
   (e.g. `10.89.4.0/24`, from `podman network inspect <project>_default`).

The API then receives `X-Forwarded-For: <real client>, <frontend container>` and
takes the right-most entry that is not trusted — the real client.

> ⚠️ **Never do step 2 without step 1.** Because of the NAT above, the front-door
> Caddy cannot tell your edge proxy from a stranger connecting directly. Trusting
> forwarded headers on a *publicly reachable* port lets anyone send their own
> `X-Forwarded-For`, choose their rate-limit bucket, and forge audit-log source
> IPs. That is strictly worse than the shared bucket you started with.
> **Unreachability of the port — not the header — is the trust boundary.**

**Choosing what to trust.** Trust only private addresses you control, and use the
network's **subnet**, not a container `/32`: container IPs are reassigned on
recreate, and a stale entry silently reverts you to one shared bucket. Never list
a public address, even one your edge legitimately connects from — the API skips
entries it trusts, so a trusted public IP promotes whatever an attacker places to
its left into "the client" (`TestRealIP_TrustingAPublicEdgeIPIsUnsafe`).

**Verify it worked** — the API log must show the real client with a `:0` port, the
signature of the rewrite:

```bash
curl -s -o /dev/null https://<your-host>/api/v1/health
podman logs --tail 5 <project>_octbase-api_1 | tail -1
# want: ... from <real client ip>:0 - 200
# a container address (e.g. 10.89.4.5) means the chain is still being discarded
```

Then confirm a forged header cannot win — this is the check worth actually
running, because a broken trust boundary looks identical to a working one until
someone abuses it:

```bash
curl -s -o /dev/null -H 'X-Forwarded-For: 9.9.9.9' https://<your-host>/api/v1/health
podman logs --tail 5 <project>_octbase-api_1 | tail -1   # must NOT say 9.9.9.9
```

**Other topologies.** A stack that is its own public entry (the standalone
`Caddyfile.tls`) cannot recover client IPs under this NAT at all: leave both trust
variables empty and accept the shared bucket, or put an edge proxy in front. A
port handler that preserves source IPs (`slirp4netns`/`pasta`) would remove the
limitation but changes networking for every deployment and is not what the
bundled stack uses — it runs netavark with `rootlessport`.

---

## Least-privilege runtime database role

By default the API serves traffic **and** runs migrations as the role in
`OCTBASE_DATABASE_URL`. That role owns the schema and, in the stock Postgres
image, is a superuser — so SQL injection or an application compromise reaches the
entire database server, and there is no separation between migrate-time DDL and
runtime DML.

Deployments against an **external or managed database** should split the two. The
bundled single-container Postgres may keep one role; the split is opt-in and the
single-URL default is unchanged.

| Env var | Role | Used for |
|---|---|---|
| `OCTBASE_DATABASE_URL` | `octbase_app` (restricted) | All request traffic — `SELECT/INSERT/UPDATE/DELETE` only |
| `OCTBASE_MIGRATE_DATABASE_URL` | `octbase` (owner) | Migrations at startup only; the connection is closed as soon as they finish |

Leaving `OCTBASE_MIGRATE_DATABASE_URL` unset keeps the legacy behaviour (one role
does both).

**Provision the restricted role** (idempotent; run as the owner):

```bash
psql "$OWNER_DATABASE_URL" \
  -v app_password="'$(openssl rand -base64 24)'" \
  -f scripts/db-least-privilege.sql
```

The script grants DML on today's tables *and* sets `ALTER DEFAULT PRIVILEGES` so
tables created by **future migrations** are covered automatically. Those default
privileges are scoped to the creating role, so migrations must keep running as
the owner role the script was given (`-v owner_role=…`, default `octbase`) — if
you later migrate as a different role, re-run the script for that role.

**Verify the runtime role cannot run DDL** (this is the point of the exercise):

```bash
# Expect: ERROR: permission denied for schema public
psql "$OCTBASE_DATABASE_URL" -c "CREATE TABLE evil_ddl (id int);"

# Expect: can_create = f, rolsuper = f
psql "$OCTBASE_DATABASE_URL" -c \
  "SELECT current_user, has_schema_privilege(current_user,'public','CREATE') AS can_create,
          (SELECT rolsuper FROM pg_roles WHERE rolname = current_user);"
```

Then start the API with both URLs set and confirm `/health` reports
`db.status: ok` at the expected `migrationVersion` — that proves migrations
applied as the owner while the pool serves as the restricted role. The startup
log line `running migrations as the dedicated owner role` confirms the split is
active.

> **Ordering:** the app role needs its grants before it serves traffic, but the
> script is safe to run before or after the first migration — run it, then start
> the API.

---

## Running Migrations Manually

```bash
# Apply all pending migrations
DATABASE_URL="postgres://..." migrate -path ./migrations -database "$DATABASE_URL" up

# Roll back one migration
migrate -path ./migrations -database "$DATABASE_URL" down 1

# Check current version
migrate -path ./migrations -database "$DATABASE_URL" version
```

The application also runs migrations automatically at startup via golang-migrate.

### Stamping an instance created before the 001_baseline squash

Migrations `001`–`039` were replaced on 2026-08-03 by a single `001_baseline`
holding the same schema. Fresh databases are unaffected: they run the baseline
and land at version 1.

An instance that already ran the old history sits at version **38 or 39** with
no file on disk for that version. This is a **hard outage, not a degraded
health report**: `runMigrations` cannot build a plan from a recorded version it
has no file for, it returns
`no migration found for version 38: read down for version 38`, and
`main.go` treats that as fatal (`os.Exit(1)`). The container crash-loops, no
port is ever bound, and a deploy fails at its health gate with
`Connection refused` — `/api/v1/health` is never reached at all.

**Which version the instance stopped at decides the procedure**, because the
baseline squashed `001`–`039` but these instances did not all get that far:

| Recorded version | Schema state | What to do |
|---|---|---|
| **39** | matches the baseline | stamp only |
| **38** | missing everything migration `039` did | apply `039`, *then* stamp |

Version 38 is **not** a database whose schema already matches the baseline.
`039_activity_referential_integrity` added `activity_entries.release_id`,
`.sprint_id` and `.target_deleted`, backfilled them, and added four foreign
keys and two indexes. Stamping a 38 straight to 1 records those as applied when
they are not — the columns stay missing, and the activity write path fails at
runtime on a schema golang-migrate now believes is current. Check before you
stamp:

```bash
# 1. Read the recorded version, and confirm it is not dirty.
psql "$DATABASE_URL" -c 'select * from schema_migrations;'

# 2. Only if it reads 38 — apply the one migration it never got. The file is no
#    longer on disk; take it from the commit before the squash.
git show 56827b4^:octbase-api/migrations/039_activity_referential_integrity.up.sql \
  | psql "$DATABASE_URL" -v ON_ERROR_STOP=1

# 3. Stamp it as the baseline. No DDL runs; this rewrites one row.
migrate -path ./migrations -database "$DATABASE_URL" force 1
#    Without the migrate CLI, the equivalent is:
#    psql "$DATABASE_URL" -c 'UPDATE schema_migrations SET version=1, dirty=false;'

# 4. Restart the API. It applies 002_notification_params on the way up and
#    should report migrationVersion 2.
curl -fsS localhost:<api-port>/api/v1/health
```

Confirm step 2 is needed rather than assuming: an instance already carrying
`activity_entries.target_deleted` is a 39 whose row simply reads 38, and
re-running `039` on it fails on the duplicate `ADD CONSTRAINT`.

Do this **only** on a database whose schema matches the baseline once step 2
has run. Forcing the version on a partially-migrated database tells
golang-migrate a lie it cannot detect later.

---

## The First Administrator

A fresh non-demo installation has an empty users table and nothing to log in
with — every flow below starts from an account that already exists. Set
`OCTBASE_BOOTSTRAP_ADMIN_EMAIL` and `OCTBASE_BOOTSTRAP_ADMIN_PASSWORD_HASH`
(see the env table above) before the first start and the API creates that first
`SUPER_ADMIN` itself, once, while the table is still empty. Everything after
that — including the invitation flow below — is done as that user.

---

## Adding a New User (Invitation Flow)

1. Log in as a SUPER_ADMIN or ADMIN user.
2. POST `/api/v1/admin/invitations` with
   `{ "email": "user@example.com", "projectId": "<uuid>", "role": "PROJECT_MEMBER" }`.
   The `role` is a **project** role — one of `PROJECT_OWNER`/`PROJECT_ADMIN`/
   `PROJECT_MEMBER`/`PROJECT_VIEWER` — applied as a membership on `projectId`
   when the invite is accepted; it defaults to `PROJECT_MEMBER` and is only
   meaningful together with `projectId` (without one, no membership is
   created). Accepted accounts are always created with **global role `USER`**;
   raising a global role (`ADMIN`, `SUPER_ADMIN`) is a separate, deliberate
   step via the admin users API after the account exists.
3. The response includes an `acceptURL`. Send this to the user.
4. The user opens the URL, enters their name and password, and the account is created.

Invitations expire after 7 days. Re-invite if expired.

---

## Deactivating a User

Preferred (SUPER_ADMIN, `internal/usermgmt`):

```bash
PATCH /api/v1/users/{userId}/disable
```

Legacy admin endpoint (still supported):

```bash
PATCH /api/v1/admin/users/{userId}
{ "isActive": false }
```

Either way, disabling an account immediately invalidates all of its refresh
tokens (active sessions are terminated). Deactivated users cannot log in but
their data is retained.

---

## GDPR & Data Subject Requests

Octbase is deployed one stack per client; the client organization is the data
controller, the hosting party the processor. Sign a data processing agreement
(DPA) per client, and fill in the `[BRACKETED]` operator placeholders in
`octbase-frontend/privacy.html` **before** the deployment goes live (both SPAs
link to that page from the login screen).

### Erasure (Art. 17)

```bash
DELETE /api/v1/users/{userId}    # SUPER_ADMIN
```

This **anonymizes in place** (there is no hard delete): email, display name,
password hash and last-login are overwritten, memberships/sessions/
notifications and pending invitations addressed to the user are removed, and
the account answers 404 everywhere. Content the user authored (tasks,
comments, pages) is retained for the organization and attributed to
"Deleted user". The freed email can be re-registered.

### Access / export (Art. 15 / 20)

There is no self-service export endpoint. For a request, export via SQL —
personal data lives in: `users` (account), `audit_logs.actor_user_id` +
`ip_address`/`user_agent`, `activity_entries.actor_user_id`, `invitations.email`,
and authored rows in `tasks` (assignee/reporter/reviewer), `task_comments`,
`page_revisions`. Example skeleton:

```sql
SELECT * FROM users WHERE id = :uid;
SELECT created_at, action, ip_address, user_agent FROM audit_logs WHERE actor_user_id = :uid;
SELECT created_at, type, message FROM activity_entries WHERE actor_user_id = :uid;
```

### Retention (Art. 5(1)(e))

The API purges automatically at startup and then daily
(`internal/retention`): audit logs and activity entries past
`OCTBASE_AUDIT_RETENTION_DAYS` / `OCTBASE_ACTIVITY_RETENTION_DAYS` (default
365; `0` disables — document any deviation in the client's records of
processing), expired refresh tokens, unaccepted invitations 30 days past
expiry, and expired OAuth state records. Two further retention surfaces are
**not** managed by the app:
container stdout logs (the API request log contains client IPs — cap them,
e.g. `podman logs` driver `max-size`/journald vacuum) and DB backups (aged
personal data lives on in dumps until the dump itself expires; keep backup
retention ≤ the shortest promised erasure horizon or document the exception).

---

## Rotating the JWT Secret

**Impact:** all active sessions are immediately invalidated; every user must log in again.

1. Generate a new secret: `openssl rand -base64 32`
2. Update `OCTBASE_JWT_SECRET` in the environment / secret store.
3. Restart the API. Existing refresh tokens are now invalid (they are HMAC-signed with the old secret, and the lookup will fail on the next refresh attempt).

---

## Deployment

### Start on boot

All services in `podman-compose.yml` set `restart: always`, so Podman restarts them
on crash or daemon restart. For containers to also come back after a **host reboot**,
Podman's restart policy must be re-applied at boot:

- **Rootful Podman**: enable the bundled service once: `systemctl enable --now podman-restart.service`.
- **Rootless Podman**: enable linger for the user running the stack so its systemd
  instance starts at boot without a login session: `loginctl enable-linger <user>`.
  Podman's user-level `podman-restart.service` (`systemctl --user enable --now podman-restart.service`)
  then restarts containers with a restart policy on boot.

Verify after a reboot with `podman ps` — all `octbase-*` and `postgres`
containers should be `Up`.

### Build and push image

Each service has its own `Containerfile` (`octbase-api/`, `octbase-frontend/`,
`octbase-mobile/`). To build and push the API image:

```bash
podman build -f octbase-api/Containerfile -t registry.example.com/octbase-api:v0.2.0 octbase-api/
podman push registry.example.com/octbase-api:v0.2.0
```

### Refreshing container base-image pins

Every `FROM` in the three Containerfiles, and the `postgres` image in
`podman-compose.yml`, is pinned **by digest** rather than by a floating tag:

| Pin | Tag it stood for at pin time |
|---|---|
| `octbase-api/Containerfile` builder | `registry.access.redhat.com/hi/go:latest` |
| `octbase-api/Containerfile` runtime | `registry.access.redhat.com/ubi9/ubi-micro:latest` |
| `octbase-frontend`/`octbase-mobile` jsbuild | `registry.access.redhat.com/hi/nodejs:latest` |
| `octbase-frontend`/`octbase-mobile` runtime | `registry.access.redhat.com/hi/caddy:latest` |
| `podman-compose.yml` postgres | `registry.access.redhat.com/hi/postgresql:18` |

A digest makes the build reproducible and stops a regressed or compromised
upstream rebuild of a tag from flowing into an image silently. The trade-off is
that **security fixes in the base image no longer arrive on their own** — pins
must be refreshed deliberately, on a schedule (quarterly is a reasonable
default) and whenever a relevant base-image CVE is announced.

Bumping the pins is a **deliberate, reviewed change**, never an automated one:

1. Pull the current tag and read back the digest the registry served — do not
   hand-write a digest:
   ```bash
   podman pull registry.access.redhat.com/hi/go:latest
   podman image inspect registry.access.redhat.com/hi/go:latest \
     --format '{{index .RepoDigests 0}}'
   # skopeo, where available, resolves without pulling:
   skopeo inspect docker://registry.access.redhat.com/hi/go:latest | jq -r .Digest
   ```
2. Replace the `@sha256:…` in the `FROM` (or the compose `image:`) and update the
   tag comment above it. The tag has to live in a comment on its **own line** —
   an inline comment on a `FROM` line is a Containerfile parse error.
3. Rebuild all three images and run the suites:
   ```bash
   podman build -f octbase-api/Containerfile -t octbase-api:pin-check octbase-api/
   # The two frontend images build from the REPOSITORY ROOT (the trailing `.`):
   # since 37b stage 3 they are built from the @octbase/shared workspace
   # package, which sits outside either app directory.
   podman build -f octbase-frontend/Containerfile -t octbase-frontend:pin-check .
   podman build -f octbase-mobile/Containerfile -t octbase-mobile:pin-check .
   ```
4. Bring a disposable stack up and gate on health
   (`octbase-operations/check-health.sh`) before deploying anywhere real.
5. Note the bump in `CHANGELOG.md`.

Two related pins are deliberately **not** digest-pinned:

- The npm toolchain the two frontend images install. Both now run `npm ci`
  against the committed `package-lock.json`, which pins every dependency by
  integrity hash — so what used to be an unpinnable `npx --yes esbuild@0.24.2`
  invocation (version-pinned only, because a lockfile was the very thing the
  no-build stance avoided) is now integrity-pinned by the lockfile itself. The
  base image the install runs in is digest-pinned like the rest.
- `docker.io/axllent/mailpit:latest` in `podman-compose.dev.yml` is dev-only and
  never deployed to a client stack (see the Mailpit section below).

### Upgrading a frontend runtime dependency

Since **37b stage 4** the two libraries that reach a browser are ordinary npm
dependencies, pinned to exact versions: `dompurify` on `@octbase/shared` (backs
the rich-text sanitizer) and `qrcode-generator` on both SPAs (the MFA enrollment
QR). Bumping one is a normal reviewed change:

1. Read the upstream release notes and edit the exact version in the owning
   `package.json` — **no `^`, no `~`**. If `qrcode-generator` moves, both SPAs
   move together; npm installs one deduped copy and disagreeing pins are a bug.
2. `npm install` at the repository root, and commit the resulting
   `package-lock.json` in the same commit.
3. `npm audit --omit=dev` must come back clean — CI's "Frontend checks" job runs
   exactly that, deliberately scoped to runtime dependencies so a build-toolchain
   advisory does not gate a frontend change.
4. `npm run build` and run the e2e suite (the rich-text sanitization tests and
   the MFA QR test are the ones that exercise these two), then note the bump in
   `CHANGELOG.md`. The Containerfile Trivy scan (the "Build image" job) flags any
   newly-introduced HIGH/CRITICAL CVE in the shipped image.

A **security** bump follows the same path but is worth landing on its own commit,
so the diff that fixes the advisory is not mixed with unrelated work.

### Refreshing a vendored dependency

What is left vendored is **build-time only** — `scripts/vendor/acorn.mjs`, which
carries `scripts/check-tdz.mjs`. Nothing in `scripts/vendor/` reaches a browser.
It is pinned by SHA-256 in `scripts/vendor-manifest.txt`, and
`scripts/check-vendor-integrity.sh` fails CI (the "Security scan" job) and the
pre-commit sweep if a vendored file drifts from its pin or a new one is left
unpinned. Upgrading it is a **deliberate, reviewed change**:

1. Fetch the new upstream artifact and diff it against the current vendored copy
   so the change is exactly what you expect (the only local delta is a prepended
   Octbase provenance header, marked in the file and described in the manifest's
   `local delta` note):
   ```bash
   curl -sSL https://unpkg.com/acorn@<new-version>/dist/acorn.mjs | diff - scripts/vendor/acorn.mjs
   ```
2. Replace the file, keeping the provenance header.
3. Update its block in `scripts/vendor-manifest.txt`: the version, upstream URL,
   upstream SHA-256, and the `<sha256>  <path>` pin.
4. Re-run `bash scripts/check-vendor-integrity.sh` — it must pass — and note the
   bump in `CHANGELOG.md`.

**Do not vendor a new runtime library.** Browser-shipped code goes through npm,
where the lockfile pins its integrity and `npm audit` watches it for advisories
— a guarantee a hand-maintained SHA-256 line cannot give. Vendoring was a
consequence of the no-npm stance that `docs/architecture.md` §5.2 retired; if
some future dependency genuinely cannot come from npm, argue it there first.

`scripts/vendor/acorn-walk.mjs` was removed on 2026-07-30: nothing imported it
and its bytes could not be traced to an upstream release. Vendor a third-party
file only when something in the tree actually imports it, and only from a dist
you can re-derive — an unused vendored file is CVE surface and review burden
with no offsetting benefit.

### Deploy (Podman Compose)

> **Client instances run released code only.** Deploy from a tagged release
> (`vX.Y.Z` on `main`) — never from an unmerged `release_vN` branch or a
> working tree. A branch deploy puts unreviewed migrations on live databases
> and makes the `/health` version stamp lie; it also cannot be rolled back to
> the previous release once a newer migration has run (down-migrate first).
> This happened once (schema 034 reached beyags/demo ahead of its release,
> found 2026-07-27) and is the reason this rule is written down.

```bash
# Rebuild and (re)start a single service without touching its dependencies
podman-compose up -d --build octbase-api
```

### Rolling back a deployment

Point the image tag back to the previous version (edit `podman-compose.yml` or the
`.env` that supplies it) and bring the service back up:

```bash
podman-compose up -d --no-deps octbase-api
```

If the new migration broke something, roll back the migration first:
```bash
migrate -path ./migrations -database "$DATABASE_URL" down 1
```

> `podman-compose` mirrors the `docker compose` CLI, so the same commands work
> with `docker compose` if you run the stack under Docker instead of Podman.

---

## Database Backups

Daily automated pg_dump via cron:

```cron
0 3 * * * pg_dump "$DATABASE_URL" | gzip > /backups/octbase-$(date +%Y%m%d).sql.gz
```

Retention: keep last 30 days. Test restore quarterly:

```bash
# Restore from backup
gunzip -c /backups/octbase-20260101.sql.gz | psql "$DATABASE_URL_RESTORE"
```

### Attachment files

Uploaded task attachments are **user data stored outside the database**, on the
local filesystem volume at `OCTBASE_ATTACHMENTS_DIR` (default `/data/attachments`).
The database row only holds metadata and an opaque `storage_key`; the bytes live
on disk. A `pg_dump` alone does **not** capture them — back up the volume too:

```cron
# Daily attachment-volume backup alongside the DB dump
0 3 * * * tar czf /backups/octbase-attachments-$(date +%Y%m%d).tar.gz -C /data attachments
```

Restore both together (DB then files) so `storage_key` references resolve:

```bash
gunzip -c /backups/octbase-20260101.sql.gz | psql "$DATABASE_URL_RESTORE"
tar xzf /backups/octbase-attachments-20260101.tar.gz -C /data
```

---

## Attachment File Storage

Task attachments are stored on a **local filesystem volume**, appropriate for the
single-instance podman-compose deployment. `podman-compose.yml` **ships this
volume** — set `ATTACHMENTS_DIR` in `.env` to choose the host directory
(default `./attachments`):

```yaml
    volumes:
      - ${ATTACHMENTS_DIR:-./attachments}:/data/attachments:U
```

Two things about that mount:

- **`:U`** makes podman chown the host directory to the container's user. The API
  container runs as **non-root UID 10001**, so without `:U` the mount arrives
  root-owned and every upload fails (the API logs `attachment storage
  unavailable; file uploads disabled` and only external-link attachments work).
- Like `PGDATA_DIR`, each stack needs its **own** `ATTACHMENTS_DIR` — two stacks
  pointed at one directory would mix their files.

> Before this volume existed, uploads were written to the API container's
> writable layer and were **silently lost on every recreate** (`up --build`,
> image bump, host reboot). If you are upgrading a stack that ran without a
> volume, any previously uploaded files are already gone — the database rows will
> reference storage keys with no file behind them, and their downloads return
> `404`.

Layout and handling:

- Each uploaded file is addressed by a **random, server-generated `storage_key`**
  (256-bit hex), never by the user-supplied filename — this neutralizes
  path-traversal attempts. Keys are sharded into two-character subdirectories.
- Uploads are validated against a **content-type allowlist** (images, PDF, common
  office docs, text, zip — no executables/scripts) using **both** the declared
  `Content-Type` and a byte-sniff (`http.DetectContentType`); mismatches are
  rejected. Size is capped by `OCTBASE_MAX_UPLOAD_MB` (enforced early with
  `http.MaxBytesReader`).
- Files are served only through the authenticated endpoint
  `GET /api/v1/tasks/{taskId}/attachments/{attachmentId}/content`, which enforces
  the same task-visibility guard as task reads. The storage directory is **never**
  exposed via a static file server.
- Deleting a task/project (or bulk-deleting tasks) removes the underlying files;
  copying a task duplicates the bytes under a new key so each task owns an
  independent file lifecycle.

Object storage (S3/MinIO) is intentionally **out of scope**; it would be the
natural option only for a future multi-instance deployment.

---

## TLS Certificates

**Caddy** (the `octbase-frontend` container) terminates TLS and reverse-proxies the
API. `octbase-frontend/caddy/Caddyfile.tls` redirects `:8080` → HTTPS and listens
on `:8443` — but note two things the defaults do **not** give you:

- The frontend image ships **only** `caddy/Caddyfile` (plus the `auth-*.caddy`
  snippets) — `Caddyfile.tls` is *not* baked in (see
  `octbase-frontend/Containerfile`). Bind-mount it in, or rebuild the image with
  it copied to `/etc/caddy/Caddyfile`.
- `podman-compose.yml` publishes only port 8080 (`${FRONTEND_PORT:-8080}:8080`);
  8443 is not published by default.

The practical route is a compose override, e.g. `podman-compose.tls.yml`:

```yaml
services:
  octbase-frontend:
    ports:
      - "${FRONTEND_BIND_ADDR:-0.0.0.0}:8443:8443"
    volumes:
      - ./octbase-frontend/caddy/Caddyfile.tls:/etc/caddy/Caddyfile:ro,Z
      - ./tls/tls.crt:/etc/caddy/tls/tls.crt:ro,Z
      - ./tls/tls.key:/etc/caddy/tls/tls.key:ro,Z
```

layered with `podman-compose -f podman-compose.yml -f podman-compose.tls.yml up -d`
(or the equivalent `podman run -v …` mounts). The cert files must land at:
- `/etc/caddy/tls/tls.crt`
- `/etc/caddy/tls/tls.key`

`Caddyfile.tls` also sets HSTS and the other security headers. `/metrics` is
**not proxied at all** — by any Caddy config; a source-IP restriction at the
Caddy layer is inert under rootless podman (see the Prometheus section below),
so the route is simply not exposed.
The shipped CSP is deliberately tight — `script-src 'self'` (no inline
scripts) and `connect-src 'self'` (no WebSockets) — and `/docs` keeps the
API's own Swagger CSP. When customizing the Caddy config, do not re-add
`'unsafe-inline'` to `script-src` or widen `connect-src`; see
`docs/hosting-concept.md` §8 and `docs/security-audit-2026-07-02.md`.

### Let's Encrypt (Certbot) renewal

If you provision certificates externally with Certbot, renew on a cron and reload
Caddy afterwards:

```bash
certbot renew --quiet
# copy/renew the cert+key into the path Caddy mounts, then reload the config
# Caddy is actually running (with the mount above, Caddyfile.tls is mounted AT
# /etc/caddy/Caddyfile). The container name is compose-generated —
# <project>_octbase-frontend_1, e.g. octbase_octbase-frontend_1; check `podman ps`:
podman exec octbase_octbase-frontend_1 caddy reload --config /etc/caddy/Caddyfile
```

> Caddy can also obtain and renew Let's Encrypt certificates automatically when
> given a public domain instead of a fixed `:8443` site address — see the Caddy
> docs for automatic HTTPS.

For local development, generate a self-signed cert:

```bash
openssl req -x509 -nodes -newkey rsa:2048 \
  -keyout tls.key -out tls.crt \
  -days 365 -subj "/CN=localhost"
```

---

## Installation password (front-door Basic Auth)

For a stack that is reachable on a public URL before it should be generally
usable (a pre-launch client install, a staging host), the Caddy front door can
demand a single shared password for the whole browser-facing app.

**Enable it** with two `.env` entries, then restart the frontend container. The
front-door image is shell-less, so it cannot hash a plaintext password itself —
generate the bcrypt hash once:

```bash
# any local Caddy, or the shipped image:
caddy hash-password --plaintext 'a-strong-shared-secret'
podman run --rm registry.access.redhat.com/hi/caddy \
  caddy hash-password --plaintext 'a-strong-shared-secret'
```

```bash
# .env
OCTBASE_SITE_AUTH=on                    # the on/off switch (empty = off)
OCTBASE_SITE_PASSWORD_HASH=$2a$14$…     # the bcrypt hash from above
OCTBASE_SITE_USER=octbase               # optional, prompt username
```

```bash
podman-compose up -d octbase-frontend
```

- The toggle is **pure Caddy configuration** — the Caddyfile imports
  `auth-{$OCTBASE_SITE_AUTH:}.caddy`, resolving to `auth-on.caddy` (a `basic_auth`
  block) when the switch is `on` and `auth-.caddy` (a no-op) otherwise. No
  entrypoint, no shell, no generated files; only the bcrypt hash ever touches
  disk. Changing it means editing `.env` and restarting the frontend; no image
  rebuild.
- **Two variables, on purpose.** podman-compose can't derive the switch from the
  hash (it supports `${VAR:-default}` but not `${VAR:+on}`), and Caddy's
  `basic_auth` refuses to start with an empty password — so the switch is
  explicit rather than inferred, keeping an unset password from crash-looping the
  front door. Setting the hash but leaving `OCTBASE_SITE_AUTH` off leaves the
  door **open**; setting the switch `on` with no hash makes the front door
  **refuse to start** (visible as `frontend` DEGRADED in `check-health.sh`). Set
  both.
- **Scope:** the desktop app, the mobile app under `/m/`, the static pages and
  the Swagger docs. The gate **deliberately excludes `/api/*` and `/health`**:
  the SPA authenticates the API with an `Authorization: Bearer <JWT>` header —
  the same header Basic Auth uses — and the HMAC webhook receivers, SSE and the
  health probes carry no Basic credentials. The API therefore keeps its JWT
  login as the real security boundary; this gate only hides the UI from casual
  visitors. **It is not a substitute for authentication** — anyone with a valid
  account can still reach the API directly.
- **Health checks:** `octbase-operations/check-health.sh` treats a `401` from the
  front door as *up and password-gated* (not degraded), and `/health` itself is
  never gated, so the API reverse-proxy probe still validates the backend.

---

## Mailpit (captured dev mail — never deployed)

**Mailpit is not part of the deployable stack.** `podman-compose.yml` contains
no Mailpit service; production either leaves `OCTBASE_SMTP_HOST` empty (mail is
logged to stdout) or points it at a real SMTP relay. Mailpit's mailbox holds
every captured message — including password-reset and invitation links — which
is why it must never ship in a client deployment.

For local development, layer the dev override to add it and route the API's
SMTP through it:

```bash
podman-compose -f podman-compose.yml -f podman-compose.dev.yml up -d
```

- Local UI: `http://localhost:8025/mailpit/` (bound to `127.0.0.1` only), or
  `/mailpit/` through the frontend Caddy. Note: the shipped `caddy/Caddyfile` —
  which **is** the config the image deploys (see `octbase-frontend/Containerfile`),
  not a dev-only variant — proxies `/mailpit` unconditionally to the `mailpit`
  service; the TLS variant `Caddyfile.tls` has no such route.
- Basic auth is on by default (`octbase:octbase`); override with
  `MAILPIT_UI_AUTH=user:strong-pass`.
- Verify a deployment is Mailpit-free:
  `podman ps --format '{{.Names}}' | grep -i mailpit` must return nothing, and
  `https://<app-domain>/mailpit/` must **not** serve the Mailpit UI. Expect a
  **502** on a stack running the shipped `Caddyfile` (the proxy target
  `mailpit:8025` does not exist without the dev overlay — the route is always
  there, its backend is not); under `Caddyfile.tls` the path falls through to
  the SPA shell instead. A **200 showing the Mailpit UI is the failure signal**:
  it means the dev overlay (and captured mail) is exposed on a deployed stack.

---

## Prometheus Metrics

Metrics are exposed at `GET /metrics` **on the API service only**. The API puts
**no auth on the route**, so the only thing keeping metrics private is that no
Caddy config proxies them: scrape `octbase-api:8000/metrics` directly (from the
compose network or an SSH tunnel), never through the front door.

**All three Caddyfiles must keep `/metrics` out of their `@backend` path list** —
`octbase-frontend/caddy/Caddyfile`, `octbase-frontend/caddy/Caddyfile.tls`, and
`octbase-mobile/caddy/Caddyfile`. Adding it to any one of them publishes metrics.

> The mobile config is the non-obvious one, and it regressed exactly this way
> (fixed 2026-07-16). `octbase-mobile` is not published to the host, so listing
> `/metrics` there looked harmless — but the front door serves the mobile SPA via
> `handle_path /m/*`, which **strips the prefix**, so a public request for
> `/m/metrics` reached the mobile Caddy as `/metrics` and was proxied to the API.
> `https://<host>/m/metrics` returned the full metrics payload to anyone. Stacks
> were only protected if they had the *optional* installation password
> (`OCTBASE_SITE_AUTH=on`) enabled, which is off by default and is not a metrics
> control. Any route the front door refuses must also be refused by the mobile
> config, or `/m/<route>` reinstates it.

Do not try to restrict `/metrics` by source IP at the Caddy layer. Rootless
podman NATs published-port traffic, so a `not remote_ip 10.0.0.0/8 …` style deny
sees a private container address for **every** caller — including an anonymous
one off the internet — and never fires. `Caddyfile.tls` carried such a rule and
it was inert; it has been removed in favour of simply not proxying the route.
See "Recovering client IPs" above for why peer identity is unavailable here.

Key metrics:
- `octbase_http_requests_total` — request count by method/path/status
- `octbase_http_request_duration_seconds` — request latency
- `octbase_sse_connections` — active SSE connections

Configure Prometheus to scrape `http://octbase-api:8000/metrics`.
