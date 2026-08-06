# Octbase Technical Documentation

> Status: Reference — the technology stack and runtime topology for operating Octbase.
> Audience: Engineers and operators deploying or maintaining the platform.
> Companions: [`architecture.md`](architecture.md) (normative architecture decisions),
> [`operations.md`](operations.md) (per-variable runbook, backups, TLS),
> [`hosting-concept.md`](hosting-concept.md) (capacity & scaling), and
> [`business-plan.md`](business-plan.md) (cost model). The authoritative env-var
> reference is [`operations.md`](operations.md) and [`.env.example`](../.env.example).

This document describes the **whole stack** required to run Octbase: the services,
the languages and libraries they are built from, how they are containerised and
networked, the data they persist, and the **DNS / TLS** wiring needed to expose
them on a public domain.

---

## 1. Architecture at a glance

Octbase is a split monorepo: a single Go API (a modular monolith) plus several
**static frontends built from ES modules by Vite**, all packaged as containers and orchestrated with
podman-compose. One Caddy container (`octbase-frontend`) is the **front door**: it
terminates TLS, serves the desktop SPA, reverse-proxies `/api` to the Go API, and
serves the mobile SPA under `/m/`. The public marketing site is a **separate
website** with its own repository (`octbase.io`) and is not part of this stack.

```
                                 Internet / DNS
                                       │
                              app.example.com
                                  (A/AAAA)
                                       ▼
   ┌───────────────────────────────────────────┐
   │  octbase-frontend  (Caddy, FRONT DOOR)     │
   │  :8080 / :8443 TLS                          │
   │  • desktop SPA   (/)                         │
   │  • mobile SPA    (/m/  → octbase-mobile)    │
   │  • /api,/docs,/openapi.yaml                  │
   │       → reverse_proxy octbase-api:8000      │
   └───────────────────┬─────────────────────────┘
                       │ /api/*
                       ▼
              ┌──────────────────┐        ┌──────────────────────────────┐
              │  octbase-api      │───────▶│  PostgreSQL                   │
              │  Go, :8000        │  pool  │  :5432, durable volume        │
              │  • REST + SSE     │  (≤25) │  (single source of truth)     │
              │  • migrations     │        └──────────────────────────────┘
              │  • outbound SCM /  │
              │    SMTP / webhooks │        local volume: OCTBASE_ATTACHMENTS_DIR
              └──────────────────┘          (uploaded task attachments)
```

For the measured resource footprint, density, and multi-node topologies, see
[`hosting-concept.md`](hosting-concept.md).

---

## 2. Service inventory

A full deployment is **four containers** (`podman-compose.yml`):

| Service | Role | Runtime / base image | Internal port | Published (default) | State |
|---|---|---|---|---|---|
| `postgres` | System of record | `registry.access.redhat.com/hi/postgresql` | 5432 | `${POSTGRES_PORT:-5432}` | **Stateful** — durable DB volume |
| `octbase-api` | Application / REST API | Go static binary on `ubi9/ubi-micro` (non-root UID 10001) | 8000 | `${API_PORT:-8000}` | Stateless **except** the attachments volume |
| `octbase-frontend` | Desktop SPA **+ front-door reverse proxy** | Caddy (`hi/caddy`) | 8080 (8443 only when `Caddyfile.tls` is mounted in — neither shipped in the image nor published by the compose file; see operations.md §TLS) | `${FRONTEND_PORT:-8080}` | Stateless |
| `octbase-mobile` | Phone-first SPA (served under `/m/`) | Caddy (static) | 8080 | *(not published — via front door)* | Stateless |

Every base image above is referenced **by digest** (`image@sha256:…`), not by the
tag shown here, so builds are reproducible and an upstream tag rebuild cannot
change a deployed stack silently. The tag each digest stood for is recorded in a
comment above the `FROM`; refreshing a pin is a deliberate step documented in
`docs/operations.md` ("Refreshing container base-image pins").

The public marketing/landing site (`octbase-web` + its contact-form `mailer`) is
**not** part of this stack — it is a separate website in its own repository
(`octbase.io`).

`octbase-mobile` is intentionally **not** published to the host — it is only
reachable through the frontend Caddy front door on the internal compose network,
which keeps a single origin (and cookie scope) for the browser.

Each service sets `restart: always` and carries CPU/RAM `deploy.resources` limits
(see `podman-compose.yml`; rationale in [`hosting-concept.md`](hosting-concept.md) §7).

---

## 3. Technology stack

### 3.1 Backend — `octbase-api`

- **Language:** Go (`go 1.25`), compiled `CGO_ENABLED=0` to a single static binary.
- **HTTP router:** `go-chi/chi/v5`.
- **Database driver:** `jackc/pgx/v5` via `pgx/v5/stdlib` (standard `database/sql`).
- **Auth:** `golang-jwt/jwt/v5` — JWT access tokens + DB-backed rotating refresh tokens; bcrypt via `golang.org/x/crypto`.
- **Migrations:** `golang-migrate/migrate/v4`, run automatically at startup; the expected version is derived from the migration files on disk (no constant to bump).
- **Metrics:** `prometheus/client_golang` at `/metrics`.
- **Crypto:** `golang.org/x/crypto` — bcrypt for passwords, AES-256-GCM (`internal/shared/crypto`) to encrypt SCM access tokens at rest.
- **Logging:** Go `slog`, structured JSON, level via `OCTBASE_LOG_LEVEL`.
- **Structure:** modular monolith by bounded context under `internal/` (`auth`, `identityaccess`, `rbac`, `usermgmt`, `auditlog`, `admin`, `workmanagement`, `docs`, `scmintegration`, `notifications`, `sse`, `webhooks`, `mailer`, `activity`, `dashboard`, `security`, `retention`, `bootstrap`, `shared`, `seed`, plus the test-only `apicontract` route↔OpenAPI parity check, `archtest` core/module dependency-direction check, and `testutil` shared test infrastructure). See [`../octbase-api/README.md`](../octbase-api/README.md).
- **Container:** multi-stage build (`octbase-api/Containerfile`) — UBI Go builder → `ubi9/ubi-micro`. The CA trust bundle is copied in so the static binary can verify TLS for outbound calls to GitHub/GitLab/Bitbucket and SMTP. Ships the binary plus `migrations/`, `api/` (OpenAPI), and `web/` (docs UI). Runs as **non-root UID 10001** (group 0): `ubi-micro` is shell-less and has no `useradd`, so `/data/attachments` is built in the builder stage and copied in with `--chown=10001:0`, which is what makes the data dir writable without a `RUN`.

### 3.2 Desktop frontend — `octbase-frontend`

- **Plain DOM, no framework:** plain HTML/CSS/JS — a small fetch wrapper, a `window.S` state object, and per-view render functions. The desktop SPA is a graph of **ES modules bundled by Vite** (37b stage 2): `index.html` loads one `<script type="module" src="js/main.js">`, each file declares its own `import`/`export`, and there is **no load-order contract** — see "File scope & exports" and "Adding a module" in [`../octbase-frontend/js/README.md`](../octbase-frontend/js/README.md). Event handling is delegated from document-level listeners to five dispatch registries, which every module fills for itself at load time via `delegation.js`'s registration API ("Delegation registration" in the same README) — the shell holds no per-view handler list.
  > **Migration status (decided 2026-07-30, `docs/architecture.md` §5.2 — that section carries the per-SPA table):** the "no bundler, no npm dependency graph" half of this stance is retired. **Both SPAs are converted** (`octbase-mobile`'s entry is `js/app.js`, which imports `js/core.js`), and since **stage 3** the `octbase-shared` modules are the `@octbase/shared` workspace package both SPAs import — the byte-identical copies and their sync/drift scripts are gone, and both frontend images now build from the repository-root context and ship the built `dist/`. **Stage 4** moved the two libraries that reach a browser (DOMPurify, the QR generator) from vendored files to the pinned `dompurify` and `qrcode-generator@1.4.4` npm dependencies, guarded in CI by `npm audit --omit=dev`; with them went the last classic `<script>` tags besides `theme-init.js`. Each SPA also builds a second self-contained IIFE bundle for the `file://` standalone demo. Plain DOM and no-framework are unaffected and stay normative.
  > **Dispatch keys on `fn.name`, so minifier identifier mangling must stay off** — under Vite 8 (which bundles with rolldown, not esbuild) that means `rollupOptions.output: { keepNames: true }`; the older `esbuild: { keepNames: true }` is a silent no-op there (that exact no-op shipped a green build with dead buttons once — see the comment in `octbase-frontend/vite.config.js`). Without it every delegated handler silently unregisters (dead buttons, no error).
- **Served by Caddy** from `/usr/share/caddy`. The image is multi-stage since 37b stage 3: a Node stage runs `npm ci` plus the Vite build from the **repository-root** context (the only one that can see `octbase-shared/`) and the Caddy stage ships the resulting `dist/`. Minification keeps **no identifier mangling** (`rollupOptions.output: { keepNames: true }` — on the rolldown output options, since Vite 8 no longer uses esbuild), because the `data-act` dispatch keys handlers by `fn.name` at runtime.
- **Compression:** Caddy `encode zstd gzip`, scoped to the paths served from disk (`/api/*`, `/docs`, `/health`, `/openapi.yaml`, `/mailpit` are excluded so the encoder never buffers the `text/event-stream` SSE responses). Minification alone leaves the desktop SPA at ~372 KB; compressed it is ~91 KB. This is set in each SPA's own Caddyfile rather than left to an edge proxy, so a client stack with nothing in front of it still gets it.
- **Asset fingerprinting:** every asset ships under a content-hashed **filename**, so its URL changes if and only if its bytes do. Vite does this for the module graph; the classic scripts outside it (`theme-init.js`, `docs-init.js`, `user-guide-nav.js`) are hashed into `assets/` by `scripts/vite-hash-classic-assets.mjs`, which rewrites the HTML references. Cache headers follow that guarantee: `/assets/*` is `immutable` for a year, files served under a stable name (`vendor/swagger-ui/*`, the verbatim `fonts/` copy) get `max-age=3600`, and HTML is `no-cache`. The former `?v=<content-hash>` query stamped by `scripts/stamp-assets.py`, its git hooks and its merge driver retired at 37b stage 5.
- Also ships the static **user guide** (`/user-guide.html`), **style guide** (`/styleguide.html`), and the self-hosted **Swagger UI** API explorer (`/docs.html`, assets under `/vendor` so the strict CSP allows them).

### 3.3 Mobile frontend — `octbase-mobile`

- A separate phone-first static SPA (Caddy), served **under `/m/` by the front
  door**, so it shares the same origin, cookies, JWT, and `/api` proxy as the
  desktop app. The front door routes by `User-Agent` (phones → `/m/`; desktops,
  laptops, and tablets incl. iPad stay on `/`).

### 3.4 Database — PostgreSQL

- Single logical database, the **only** stateful, source-of-truth service. Schema
  is owned entirely by `golang-migrate` migrations (`octbase-api/migrations/`,
  sequentially numbered from `001` — the expected head is derived from the files
  at startup). Connection pool defaults to 25 open / 5 idle per API instance,
  tunable via `OCTBASE_DB_MAX_OPEN_CONNS` / `OCTBASE_DB_MAX_IDLE_CONNS`.

### 3.5 Orchestration & runtime

- **Podman / podman-compose** (the compose file mirrors `docker compose`, so the
  same commands work under Docker). Images are built from per-service
  `Containerfile`s on Red Hat UBI base images. State lives in two host paths: the
  Postgres data dir (`PGDATA_DIR`, default `./pgdata`) and the attachments volume
  (`OCTBASE_ATTACHMENTS_DIR`).

---

## 4. Networking & request routing

The `octbase-frontend` Caddy config (`octbase-frontend/caddy/Caddyfile`) is
the single ingress for the application:

| Path pattern | Handling |
|---|---|
| `/api/v1/projects/*/events` | Reverse-proxy to `octbase-api:8000` with `flush_interval -1` (SSE — buffering **must** be disabled or live updates stall) |
| `/api/*`, `/docs`, `/docs/*`, `/health`, `/openapi.yaml` | Reverse-proxy to `octbase-api:8000` (`/metrics` is deliberately not proxied — scrape the API service directly) |
| `/m/*` | Reverse-proxy to `octbase-mobile:8080` (prefix stripped) |
| `/` and other paths | Device-route (phone ↔ desktop), then serve the desktop SPA with SPA fallback (`try_files … /index.html`) |

> **`Caddyfile.tls` (direct-HTTPS variant) is a subset of this table**: it
> terminates TLS, sets HSTS, and proxies the API/SSE routes, but it has **no
> `/m/*` mobile route and no phone↔desktop device redirect** — a stack fronted
> by `Caddyfile.tls` alone serves the desktop SPA for every path, `/m/`
> included. The deployed topology (host reverse proxy → the default
> `Caddyfile`) is unaffected; extend `Caddyfile.tls` with the `handle_path
> /m/*` block if you need the mobile SPA on a direct-TLS stack.

**Security headers** are set at the edge on every site: `X-Content-Type-Options`,
`X-Frame-Options: DENY`, `Referrer-Policy`, `Permissions-Policy`, and a strict
**Content-Security-Policy** (`default-src 'self'`, `connect-src 'self'` — SSE
runs on the app's own origin and no WebSockets are used), plus
`Strict-Transport-Security` (HSTS).

**`/metrics` is not proxied by any Caddy config** — not the front door, not
`Caddyfile.tls`, and **not `octbase-mobile/caddy/Caddyfile`**. The API applies no
auth to the route, so proxying it anywhere publishes it; Prometheus scrapes
`octbase-api:8000` directly instead. The mobile config matters despite that
container never being published: the front door serves it via `handle_path /m/*`,
which strips the prefix, so any route the mobile config proxies is reachable at
`/m/<route>`. Listing `/metrics` there exposed the full payload at
`https://<host>/m/metrics` until it was removed (2026-07-16). Restricting the
route by source IP is not an option here — under rootless podman's NAT every
caller presents a private container address, so a `remote_ip` deny never fires
(see "Client IP propagation" above).

**Optional installation password.** Setting `OCTBASE_SITE_AUTH=on` with a bcrypt
`OCTBASE_SITE_PASSWORD_HASH` (from `caddy hash-password`) on the
`octbase-frontend` container makes the front door demand HTTP Basic Auth before
serving the browser-facing app. The front-door image is shell-less, so the
toggle lives entirely in the Caddyfile: the route imports
`auth-{$OCTBASE_SITE_AUTH:}.caddy`, resolving to `auth-on.caddy` — a
`basic_auth @sitegate {…}` block using `{$OCTBASE_SITE_PASSWORD_HASH}` — when the
switch is `on`, or `auth-.caddy` (a no-op) otherwise. Two variables rather than
one because podman-compose can't derive the switch from the hash
(`${VAR:+on}` is unsupported) and `basic_auth` refuses an empty password at parse
time. `@sitegate` matches everything **except** `/api/*` and `/health`, so the
JWT-authenticated API (whose `Authorization: Bearer` header would collide with
Basic Auth), the HMAC webhook receivers, SSE and health probes are never gated.
Both unset (the default) leaves the front door open. See `docs/operations.md` →
"Installation password".

**CORS:** the API allows exactly one origin, `OCTBASE_CORS_ORIGIN` (must equal the
public app URL). Because the front door serves the SPA and proxies `/api` on the
**same origin**, cross-origin requests are normally unnecessary in production.

### Client IP propagation

Per-IP rate limiting and audit-log source addresses depend on the client's IP
reaching the API. **Two properties of the runtime make this non-obvious, and both
are load-bearing:**

1. **Rootless podman NATs published-port traffic.** The stack runs netavark with
   the `rootlessport` port handler, which rewrites the source address of every
   connection crossing a published port. A container therefore sees *its own*
   address as the peer for **all** callers. Peer identity is destroyed: the front
   door cannot distinguish an edge proxy on the host from a stranger dialling the
   port directly.
2. **Caddy replaces `X-Forwarded-For` from untrusted peers.** With no
   `trusted_proxies`, the front door overwrites any inbound chain with the peer
   address. That is unspoofable but, combined with (1), means the API only ever
   sees the front-door container — so **every client shares one rate-limit
   bucket** unless the stack is configured as below.

The supported topology for real per-client limits — an edge proxy on the host,
with the front door bound to loopback:

```
client ──TLS──► edge proxy (host)  ──127.0.0.1:FRONTEND_PORT──►
                  frontend Caddy (compose net) ──► octbase-api:8000
```

| Setting | Value | Role |
|---|---|---|
| `FRONTEND_BIND_ADDR` | `127.0.0.1` | Closes the front door to everything but this host — **the actual trust boundary** |
| `OCTBASE_FRONTEND_TRUSTED_PROXIES` | `private_ranges` | Front door preserves the edge's XFF and appends its peer instead of overwriting |
| `OCTBASE_TRUSTED_PROXIES` | compose subnet (e.g. `10.89.4.0/24`) | API honors the chain from its immediate peer only |

The API receives `X-Forwarded-For: <real client>, <frontend container>` and
`shared.RealIP` takes the right-most entry that is **not** trusted — the real
client — rewriting `RemoteAddr` to `<client>:0` (the `:0` port is the rewrite's
signature in the API log).

> ⚠️ **`FRONTEND_BIND_ADDR=127.0.0.1` is a precondition, not a nicety.** Because
> of (1), trusting forwarded headers while the port is publicly reachable lets
> anyone set their own `X-Forwarded-For`, pick their rate-limit bucket, and forge
> audit-log IPs — strictly worse than the shared bucket. Trust only the private
> compose **subnet** (container IPs are reassigned on recreate) and never a public
> address: the API skips entries it trusts, so a trusted public IP promotes an
> attacker-supplied entry to its left into "the client".

The edge proxy itself must **not** trust forwarded headers, so that it replaces
whatever a client sends with the real peer. See `docs/operations.md` →
"Recovering client IPs" for the ordered rollout and its verification commands.

A stack that is its own public entry (the standalone `Caddyfile.tls`) cannot
recover client IPs under this NAT: leave both trust variables empty and accept the
shared bucket, or front it with an edge proxy. A source-IP-preserving port handler
(`slirp4netns`/`pasta`) would lift the limitation but changes networking for every
deployment and is not what the bundled stack uses.

### Public endpoints that must be reachable from the internet

| Endpoint | Why it must be public |
|---|---|
| App URL (`/`, `/api/*`) | The application itself; `OCTBASE_APP_URL` / `OCTBASE_CORS_ORIGIN` point here |
| `/api/v1/webhooks/github`, `/api/v1/webhooks/bitbucket` | SCM providers POST push/PR events (HMAC-verified) |
| `/api/v1/oauth/<provider>/callback` | OAuth redirect target — must match `OCTBASE_OAUTH_REDIRECT_BASE` and the provider app's registered callback exactly |

---

## 5. Ports

| Port (host default) | Service | Notes |
|---|---|---|
| `8080` (`FRONTEND_PORT`) | `octbase-frontend` | App + front door (HTTP). TLS variant listens on `8443`, redirecting `8080`. |
| `8000` (`API_PORT`) | `octbase-api` | Direct API access; usually only the front door needs it |
| `5432` (`POSTGRES_PORT`) | `postgres` | Restrict/firewall in production — does not need public exposure |
| `8080` | `octbase-mobile` | Internal only (reached via front door `/m/`) |

In production, publish only `80`/`443` (via the reverse proxy) to the internet;
keep Postgres and the internal services on a private network.

---

## 6. DNS

Octbase ships no DNS configuration of its own — it expects a reverse proxy on a
public host. The records below are the deployment wiring. The reference public
deployment uses `demo.octbase.io`; substitute your own domain.

### 6.1 Records for the application

| Record | Type | Points to | Purpose |
|---|---|---|---|
| `app.example.com` (or apex) | `A` / `AAAA` | Public IPv4 / IPv6 of the host running `octbase-frontend` | The application front door — SPA, `/api`, SSE, webhooks, OAuth callback |

The public marketing/landing site is a separate website (`octbase.io` repo) with
its own hostname and DNS. Pick **one canonical app hostname** here and use it
consistently for `OCTBASE_APP_URL`,
`OCTBASE_CORS_ORIGIN`, and `OCTBASE_OAUTH_REDIRECT_BASE` — they must all agree.

### 6.2 What the hostname must match in config

- `OCTBASE_APP_URL` and `OCTBASE_CORS_ORIGIN` → `https://app.example.com`
- `OCTBASE_OAUTH_REDIRECT_BASE` → `https://app.example.com`, so the registered
  OAuth callback is `https://app.example.com/api/v1/oauth/<provider>/callback`
- The provider webhook URLs you register point at
  `https://app.example.com/api/v1/webhooks/<github|bitbucket>`

### 6.3 TLS prerequisites

For automatic HTTPS (Let's Encrypt via Caddy), the public DNS `A`/`AAAA` record
must resolve to the host **before** issuance, and inbound `80`/`443` must be open
so the ACME challenge can complete. With externally provisioned certs (Certbot),
DNS only needs to be correct for the challenge method you use (HTTP-01 needs port
80; DNS-01 needs API access to your DNS provider). See [`operations.md`](operations.md) → *TLS Certificates*.

### 6.4 Mail (deliverability) DNS

Octbase only sends mail (notifications via `OCTBASE_SMTP_*`); it does **not**
receive mail, so **no `MX` record is required for Octbase itself**. To keep
outgoing mail from being marked as spam, configure the standard records on the
**sending domain** (the domain in `OCTBASE_SMTP_FROM`, e.g. `beyags.com`):

| Record | Type | Purpose |
|---|---|---|
| SPF | `TXT` | Authorise your SMTP relay's IPs to send for the domain |
| DKIM | `TXT` | Sign outgoing mail (key/selector from your SMTP provider) |
| DMARC | `TXT` (`_dmarc`) | Policy + reporting for SPF/DKIM alignment |

If you relay through a managed SMTP provider (the recommended path), use the
SPF/DKIM values they supply.

### 6.5 Internal name resolution

Inside the compose network, services reach each other by **service name** (e.g.
`octbase-api:8000`, `postgres:5432`) via Podman's built-in DNS — no
host DNS or `/etc/hosts` entries are involved. Only the front door(s) are exposed
to public DNS.

---

## 7. Data, persistence & backups

- **PostgreSQL volume** — the database’s durable state (`PGDATA_DIR`, default
  `./pgdata`). Each compose stack must use its own data directory.
- **Attachments volume** — uploaded task-attachment **bytes** live on the local
  filesystem at `OCTBASE_ATTACHMENTS_DIR` (default `/data/attachments`), addressed
  by a random 256-bit `storage_key` (sharded into subdirs); the DB stores only
  metadata. Object storage (S3/MinIO) is intentionally out of scope for the
  single-instance deployment.
- **Profile pictures** — unlike task attachments, a user's avatar is stored
  **inline in the `users` row** (`avatar` bytea, ≤2 MiB), not on the attachments
  volume. It is therefore captured by `pg_dump` and needs no separate backup, and
  it works even on a stack with no attachments volume configured.
- **Backups** — `pg_dump` does **not** capture attachments; back up both the DB
  and the attachments volume, and restore them together. Full procedure (cron
  examples, retention, restore) in [`operations.md`](operations.md) → *Database Backups*.

---

## 8. Deployment & lifecycle

```bash
cp .env.example .env          # set OCTBASE_JWT_SECRET, OCTBASE_SCM_ENC_KEY, etc.
podman-compose up -d --build  # build images and start the full stack
```

- **Migrations** run automatically at API startup (or manually with the `migrate`
  CLI — see [`operations.md`](operations.md)).
- **Start on boot** — all services use `restart: always`; enable
  `podman-restart.service` (and `loginctl enable-linger` for rootless) so they
  return after a host reboot. See [`operations.md`](operations.md) → *Deployment*.
- **Single-service redeploy / rollback** — `podman-compose up -d --no-deps <svc>`
  with the desired image tag.
- **CI** (`.github/workflows/ci.yml`) runs six jobs: Go lint; Go tests against
  Postgres (enforcing a coverage floor); the frontend checks (six guards + JS
  unit tests); the Playwright e2e suite against a seeded stack; a security scan
  (vendor integrity, govulncheck, gitleaks); and the image builds (with Trivy
  scans), gated on all of the above.

### Production checklist (env)

| Must set in production | |
|---|---|
| `OCTBASE_JWT_SECRET` | ≥32 random bytes; the API refuses a weak/placeholder secret with demo mode off |
| `OCTBASE_DEMO_MODE=false` | Disables seed data and the dev JWT fallback |
| `OCTBASE_SECURE_COOKIES=true` | Refresh cookie gets the `Secure` flag (behind TLS) |
| `OCTBASE_CORS_ORIGIN` / `OCTBASE_APP_URL` | The public app URL |
| `OCTBASE_SCM_ENC_KEY` | 32-byte key — required before any SCM repository connection can be saved |
| `OCTBASE_WEBHOOK_SECRET_*` | If SCM webhooks are exposed |

Full variable reference: [`operations.md`](operations.md) and [`.env.example`](../.env.example).

---

## 9. Observability

- **Health:** `GET /api/v1/health` — DB pool stats + migration version; returns
  `503` when the DB is unreachable or the live migration version ≠ the expected
  (files-derived) version. Wire this to your load balancer / uptime check.
- **Metrics:** Prometheus at `/metrics` (`octbase_http_requests_total`,
  `octbase_http_request_duration_seconds`, `octbase_sse_connections`); not
  proxied by any Caddy config (see §4) — scrape `http://octbase-api:8000/metrics`
  directly on the container network.
- **Logs:** structured JSON via `slog` to stdout — collect with your container log
  driver / aggregator.
- **Graceful shutdown:** 30-second connection drain on `SIGTERM`.

---

## 10. Security summary

- **JWT-only API** — every `/api/v1` domain route requires a `Bearer` token
  (`auth.JWTMiddleware` + `shared.LoadUserGlobalRole`); no `X-User-Id` fallback.
  Public exceptions: `auth/login`, `auth/refresh`, `auth/logout`,
  `auth/mfa/verify`, `auth/forgot-password`, `auth/reset-password`, invitation
  inspect/accept, the OAuth callback
  (`GET /api/v1/oauth/{provider}/callback`), and the HMAC webhook receivers.
  SSE accepts an optional `?token=`.
- **RBAC** — global roles (`SUPER_ADMIN`/`ADMIN`/`USER`/`GUEST`) and per-project
  roles enforced server-side via a permission matrix.
- **Encryption at rest** — SCM tokens encrypted with `OCTBASE_SCM_ENC_KEY`
  (AES-256-GCM); passwords bcrypt-hashed.
- **SSRF egress guard** — a real SCM connection's user-supplied API base URL is
  validated (http/https only, no literal internal-IP host → `SCM_URL_NOT_ALLOWED`)
  and all outbound provider traffic goes through a dialer that refuses to connect
  to loopback, private (RFC1918/RFC6598), link-local (incl. the cloud-metadata
  endpoint), and multicast addresses, closing DNS-rebinding across redirects.
- **Webhook authenticity** — Bitbucket/GitHub receivers validate HMAC-SHA256.
- **Edge hardening** — strict CSP, HSTS, `X-Frame-Options: DENY`, sandboxed
  uploads (random storage keys, content-type allowlist + byte sniff, size cap),
  and rate limiting on the public auth routes plus both `invitations/{token}`
  routes (one shared 120/min/IP budget) and `/api/v1/users` (60/min).
- For scale-time tenancy and isolation considerations, see
  [`hosting-concept.md`](hosting-concept.md) §12.

---

## 11. Where to look next

| Need | Document |
|---|---|
| Architectural style, concurrency model, scaling stance (normative) | [`architecture.md`](architecture.md) |
| Per-variable env reference, backups, TLS, user admin | [`operations.md`](operations.md) |
| Capacity, density, multi-node scaling topologies | [`hosting-concept.md`](hosting-concept.md) |
| Cost-per-user / pricing model | [`business-plan.md`](business-plan.md) |
| API surface, domain rules, bounded contexts | [`../octbase-api/README.md`](../octbase-api/README.md) |
| Product overview, features, quick start | [`../README.md`](../README.md) |
| End-user features | [`../octbase-frontend/user-guide.html`](../octbase-frontend/user-guide.html) |
