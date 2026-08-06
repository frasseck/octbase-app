# Octbase Hosting & Scaling Concept

> Status: Reference architecture / planning document
> Audience: Operations, infrastructure, and platform engineering
> Companion: see [`business-plan.md`](business-plan.md) for the cost-per-user / pricing model, [`operations.md`](operations.md) for the per-variable runbook, and `podman-compose.yml` for the reference stack.

This document defines how Octbase is hosted today and how it scales from a single
box to a multi-node, multi-tenant platform. It is grounded in **measured resource
footprints** of the running stack, not estimates.

---

## 1. Scope & goals

- Provide a repeatable sizing model for "how many users / instances fit on box X".
- Define the supported scaling topologies and when to move between them.
- Call out the architectural constraints (state, connections, storage) that
  govern safe scaling.
- Give concrete, copy-ready configuration recommendations.

**Out of scope:** application feature design, CI/CD pipeline, backup tooling
selection (a backup *policy* is stated; tool choice is left to ops).

---

## 2. System architecture

A full Octbase application deployment is four services (see `podman-compose.yml`):

| Service | Role | Runtime | Listens | State |
|---|---|---|---|---|
| `postgres` | System of record | PostgreSQL | 5432 | **Stateful** — durable DB volume |
| `octbase-api` | Application/API tier | Go (static binary) | 8000 | **Stateless** except local attachments dir |
| `octbase-frontend` | Desktop SPA + reverse proxy | Caddy (static) | 8080 | Stateless |
| `octbase-mobile` | Phone-first SPA | Caddy (static) | 8080 | Stateless |

The public **marketing/landing site** (`octbase-web` + its `mailer`, both stateless
Caddy/Go) is a **separate website deployed from its own repository (`octbase-web`)**.
It is included below in the platform-wide topology and sizing because it still
runs as a small stateless edge, but it is not part of the application stack and
scales independently.

### 2.1 State inventory (the part that governs scaling)

Scaling is easy where there is no state and hard where there is. Octbase has
exactly three pieces of state:

1. **PostgreSQL** — the single source of truth. All horizontal scaling decisions
   ultimately route back to the database.
2. **Attachments directory** — `OCTBASE_ATTACHMENTS_DIR` (default `/data/attachments`)
   is a **local filesystem path** on the API container. This is the one piece of
   hidden state that breaks naïve horizontal scaling of the API (see §6.2).
3. **Authentication** — JWT access tokens are **stateless** (no server-side
   session store); refresh tokens are rotated and validated against the DB. This
   means the API tier can scale horizontally without sticky sessions, *provided*
   attachments are externalised.

### 2.2 Request path

```
            ┌─────────────────────────── TLS / reverse proxy ───────────────────────────┐
  Client ──▶│  (Caddy / nginx / Traefik)  → static SPA assets                            │
            │                              → /api/*  reverse-proxied to octbase-api:8000  │
            └────────────────────────────────────────────────────────────────────────────┘
                                                  │
                                                  ▼
                                          octbase-api (Go)
                                                  │  pool: max 25 conns/instance
                                                  ▼
                                            PostgreSQL
```

The frontend container already reverse-proxies `/api` to the API (see
`octbase-frontend/caddy/Caddyfile`) and serves the mobile SPA under `/m/` on the
same origin, so the browser shares one origin/cookie scope.

---

## 3. Measured resource baseline

Idle memory per container, measured with `podman stats` on this host:

| Container | Idle RAM | CPU (idle) |
|---|---|---|
| postgres | ~41 MB | ~0.3% |
| octbase-api | ~6 MB | <0.1% |
| octbase-frontend | ~17 MB | ~0.1% |
| octbase-mobile | ~16 MB | <0.1% |
| octbase-web | ~18 MB | <0.1% |
| mailer | ~1.4 MB | <0.1% |
| **Full stack** | **~100 MB idle** | **near-zero** |

**Under load** the only component that grows materially is PostgreSQL (shared
buffers + up to 25 backend processes at ~5–10 MB each). Realistic working budget:

| Component | Idle | Under 25-user load |
|---|---|---|
| Full stack with own DB | ~100 MB | ~0.6–1.0 GB |
| App-only stack (shared DB) | ~58 MB | ~150 MB |

The three Caddy services are static file servers and are effectively free; they
are also the most CDN/cache-friendly tier.

---

## 4. The governing constraint: database connections

The API pool **defaults to 25 max open connections** per instance
(`cmd/octbase-api/main.go`; tunable via `OCTBASE_DB_MAX_OPEN_CONNS` /
`OCTBASE_DB_MAX_IDLE_CONNS`, defaults 25/5).
PostgreSQL's default `max_connections` is **100**. Therefore, at the default pool:

> **4 API instances against one Postgres = 100 connections = the default cap.**

This is the first wall you hit when consolidating onto a shared database, and it
is unrelated to CPU/RAM headroom. Mitigations, in order of preference:

1. **Right-size the pool.** 25 connections is generous for 25 users. Lowering
   the pool to ~10 triples the number of instances a default Postgres supports.
   This is now an env var — set `OCTBASE_DB_MAX_OPEN_CONNS` (and
   `OCTBASE_DB_MAX_IDLE_CONNS`) per deployment; defaults remain 25/5.
2. **Introduce pgBouncer** (transaction pooling) in front of Postgres. Many API
   instances share a small pool of real backends. This is the clean, standard
   answer above ~4 instances and is **strongly recommended** for any shared-DB
   topology.
3. **Raise `max_connections`** — works, but each backend costs ~5–10 MB RAM and
   adds scheduler contention; treat as a complement to pooling, not a substitute.

**Recommendation:** make the pool size an environment variable and put pgBouncer
in front of any shared database. See §9.

---

## 5. Scaling topologies

Three supported models, in increasing order of scale and operational maturity.

### Model A — Stack-per-tenant (vertical)

Each tenant/instance gets the full four-container stack, including its own
Postgres. This is the current `podman-compose.yml` model.

- **Pros:** strongest isolation (blast radius = one tenant), trivial per-tenant
  backup/restore, no shared-connection math.
- **Cons:** N× Postgres overhead (RAM + duplicated buffer cache), N databases to
  patch/back up, no resource sharing.
- **Use when:** small number of high-value tenants, strict data isolation
  requirements, or per-tenant lifecycle (independent upgrades).

### Model B — Shared database, multiple app stacks

One PostgreSQL (with pgBouncer) serves many lightweight app-only stacks.

- **Pros:** ~40% less per-instance RAM, one DB to operate, shared buffer cache.
- **Cons:** shared failure domain at the DB; requires connection pooling; tenant
  isolation becomes a schema/row-level concern, not a process boundary.
- **Use when:** many small tenants, cost efficiency matters, and the DB is
  managed/HA.

### Model C — Horizontally scaled stateless tier (target platform)

Stateless API replicas behind a load balancer; managed/HA Postgres; attachments
on object storage; static assets on a CDN.

- **Pros:** elastic, rolling deploys, node failure tolerance, best $/user at scale.
- **Cons:** highest operational complexity; requires the two refactors in §6.2
  and §9.
- **Use when:** growth beyond a single node, or an SLA that demands HA.

---

## 6. Capacity planning — reference node: 8 vCPU / 32 GB

Worked example for a single host. Reserve **~4 GB RAM + 1 vCPU** for the host OS
and the podman/container runtime.

### 6.1 With per-instance database (Model A)

| Constraint | Math | Limit |
|---|---|---|
| RAM | 28 GB usable ÷ ~1 GB per loaded stack | ~25–28 |
| CPU | ~1 vCPU headroom per active stack, 7 usable | **~8 comfortable, ~12 light load** |

**CPU is the ceiling. Plan for 8 instances (200 users @ 25), push to 12 if usage
is light and bursty.**

### 6.2 With one shared database

The total user load is unchanged — consolidating the DB saves RAM and gives a
shared cache, it does **not** reduce the work. Numbers depend on where the DB lives:

| DB placement | App-tier limit on this node | Notes |
|---|---|---|
| Shared DB **on this box** | ~8–12 instances | Reserve ~2 vCPU + ~6 GB for Postgres+pgBouncer |
| Shared DB **on its own box** | ~8–14 instances | This node runs only light app stacks; the DB host becomes the limit |

In both cases you **must** resolve the connection math from §4 first, or you cap
at 4 instances regardless of CPU/RAM.

> **Attachments caveat:** with more than one API replica sharing a database, the
> local `OCTBASE_ATTACHMENTS_DIR` must become **shared storage** (NFS/EFS or, better,
> an object store such as S3/MinIO). Otherwise a file uploaded via replica A is
> invisible to replica B. This refactor is the prerequisite for Model C.

> **In-process state caveat (also Model C prerequisites):** two further pieces of
> API state live in process memory and are correct only with **one API replica per
> deployment** (which Models A and B guarantee):
>
> 1. **The SSE hub** (`internal/sse`) — realtime events and presence are fanned out
>    from an in-memory hub. With N replicas, a client streaming from replica A never
>    sees events published on replica B. Scale-out needs a shared bus (PostgreSQL
>    `LISTEN/NOTIFY` is the natural first step; Redis pub/sub beyond that).
> 2. **The rate limiter** (`shared.RateLimit`) — per-IP counters are per-process, so
>    N replicas multiply the effective auth/user-management rate limits by N.
>    Independently of replica count, the counters are only per-*client* if the
>    deployment propagates client IPs: rootless podman's `rootlessport` handler
>    NATs published-port traffic, so an unconfigured stack keys every client into
>    one bucket. Recovering them needs an edge proxy on the host plus the
>    `FRONTEND_BIND_ADDR` / `OCTBASE_FRONTEND_TRUSTED_PROXIES` /
>    `OCTBASE_TRUSTED_PROXIES` trio — see `docs/technical_documentation.md` §4
>    ("Client IP propagation"). Any load balancer fronting a scaled-out fleet must
>    keep appending to `X-Forwarded-For`, and each stack's trust list must name
>    only the private hop directly in front of its API.
>
> (The lazy DONE-task sweep throttle is also per-process, but replicas only make the
> sweep run more often — no correctness impact.)

---

## 7. Per-service resource limits

**Done** — `podman-compose.yml` now carries these limits, so one busy stack
cannot starve its neighbours when several stacks share a host. The budget
(Model A) for reference:

```yaml
services:
  postgres:
    deploy:
      resources:
        limits:   { cpus: "2.0", memory: 1024M }
        reservations: { cpus: "0.25", memory: 256M }
  octbase-api:
    deploy:
      resources:
        limits:   { cpus: "1.0", memory: 256M }
        reservations: { cpus: "0.10", memory: 64M }
  octbase-frontend:   { deploy: { resources: { limits: { cpus: "0.5", memory: 64M } } } }
  octbase-mobile:     { deploy: { resources: { limits: { cpus: "0.5", memory: 64M } } } }
```

(The marketing site and its mailer live in the separate `octbase-web` repo and
carry their own limits there.)

Also tune Postgres per stack (it ships with defaults sized for a generic host):
`shared_buffers ≈ 256M`, `max_connections` set to your pool size + headroom,
`work_mem` modest. For many small tenants, smaller `shared_buffers` per DB is
better than the default.

---

## 8. Network, TLS & routing

- **Terminate TLS** at a single reverse proxy (Caddy/Traefik/nginx) in front of
  the stacks. Set `OCTBASE_SECURE_COOKIES=true` so the refresh cookie carries the
  `Secure` flag (it defaults to `false` for local HTTP dev).
- Set `OCTBASE_CORS_ORIGIN` and `OCTBASE_APP_URL` to the real public origin per
  deployment.
- **Security headers are part of the shipped Caddy configs** and must survive
  any proxy/CDN placed in front: HSTS (TLS config only), nosniff, `DENY`
  framing, and a strict CSP — `script-src 'self'` (no inline scripts; the SPAs
  load only external files) and `connect-src 'self'` (same-origin fetch + SSE,
  no WebSockets). `/docs` is deliberately excluded from the edge CSP because
  the API serves it with its own route-specific Swagger policy — an
  operator-supplied edge must not blanket-apply the app CSP to `/docs`. Do not
  re-add `'unsafe-inline'` to `script-src` or widen `connect-src`; both were
  removed as hardening (see `docs/security-audit-2026-07-02.md`).
- **Optional installation password.** Set `OCTBASE_SITE_AUTH=on` plus a bcrypt
  `OCTBASE_SITE_PASSWORD_HASH` (from `caddy hash-password`, frontend container) to
  put the whole browser-facing app behind a front-door HTTP Basic Auth prompt —
  useful for a pre-launch or staging stack on a public URL. It
  excludes `/api/*` and `/health` (so JWT auth, webhooks, SSE and health probes
  keep working) and is a UI-hiding convenience, not a replacement for the API's
  own authentication. See `docs/operations.md` → "Installation password".
- URL overrides used by tests/previews (`?apiBase=…`, mobile `?desktop=…`) are
  inert on deployed origins by design — they only work from `file://` or
  loopback hosts. Deployment configuration goes through env vars
  (`OCTBASE_CORS_ORIGIN`, `OCTBASE_APP_URL`, …), never URL parameters.
- **Static tier → CDN.** The frontend/mobile/web assets are immutable, hashed
  (cache-busting already in place) and ideal for a CDN or edge cache. This offloads
  the majority of request volume from the application nodes entirely.
- Route `/api/*` to the API tier; everything else to static assets. In Model C
  this fronts a pool of API replicas (round-robin; no sticky sessions needed once
  attachments are externalised — JWTs are stateless).

---

## 9. Database scaling

The database is the scaling pivot. Roadmap as load grows:

1. **Connection pooling (pgBouncer)** — transaction mode, in front of Postgres.
   Decouples app-instance count from backend count. *Adopt at the first shared-DB
   deployment.*
2. **Vertical scaling** — Postgres benefits directly from more RAM (buffer cache)
   and faster disk. Cheapest early win.
3. **Read replicas** — offload read-heavy endpoints (boards, listings) to replicas
   once a single primary saturates on reads.
4. **Managed Postgres / HA** — for Model C, use a managed service or a
   Patroni/repmgr cluster for automated failover. Removes the single point of
   failure that Models A/B share.
5. **Backups & PITR** — nightly base backup + WAL archiving (point-in-time
   recovery). Test restores quarterly. This is mandatory before any production
   shared-DB topology.

---

## 10. High availability

| Tier | Single-node (A/B) | HA target (C) |
|---|---|---|
| Static SPA | Single Caddy | CDN + multiple origins |
| API | Single instance per stack | ≥2 replicas behind LB, rolling deploy |
| Database | Single Postgres (SPOF) | Primary + replica with automated failover |
| Attachments | Local disk | Object storage (S3/MinIO), replicated |

Octbase's stateless API + JWT auth means the **API tier reaches HA cheaply** once
attachments are externalised — the only blocker between Model B and a
fault-tolerant API tier is §6.2's storage refactor.

---

## 11. Observability & operations

- **Logs:** `OCTBASE_LOG_LEVEL` (`debug|info|warn|error`); ship container stdout to
  a central aggregator (Loki/ELK).
- **Metrics:** collect host + per-container CPU/RAM (cadvisor/node_exporter) and
  Postgres metrics (postgres_exporter: connection count vs. cap, slow queries,
  cache hit ratio). Connection count vs. `max_connections` is the single most
  important capacity signal for shared-DB topologies.
- **Health:** the API exposes a health endpoint; wire it to the load balancer and
  uptime monitoring. (Note: the in-repo frontend tests skip when `/health` is not
  2xx — the same endpoint underpins production liveness checks.)
- **Alerting thresholds (suggested):** DB connections > 80% of cap; node RAM > 85%;
  sustained node CPU > 80%; attachment volume > 80% full.

---

## 12. Security considerations at scale

- **Secrets:** `OCTBASE_JWT_SECRET` must be ≥32 random bytes and **unique per
  deployment**; rotating it logs everyone out. Manage via a secrets store, not
  `.env` files on disk, in production.
- **Demo mode off:** ensure `OCTBASE_DEMO_MODE=false` in production — it seeds demo
  data and enables a dev JWT fallback.
- **Webhook secrets:** set `OCTBASE_WEBHOOK_SECRET_GITHUB` / `_BITBUCKET` (HMAC
  verification) wherever webhooks are exposed.
- **Tenant isolation (Model B/C):** with a shared database, isolation moves from a
  process boundary to the data layer — audit that queries are tenant-scoped and
  consider row-level security as a defence in depth.
- **Least-privilege database role:** by default the API serves traffic and runs
  migrations as the schema owner (a superuser in the stock image), so an app
  compromise owns the database server. For any **external/managed database** —
  and especially a **shared** one under Model B/C, where the blast radius is every
  tenant — serve traffic as a restricted DML-only role
  (`OCTBASE_DATABASE_URL`) and run migrations as the owner
  (`OCTBASE_MIGRATE_DATABASE_URL`). Provision with
  `scripts/db-least-privilege.sql`; see `docs/operations.md`
  ("Least-privilege runtime database role"). The bundled per-stack Postgres of
  Model A may keep a single role.
- **Upload limits:** `OCTBASE_MAX_UPLOAD_MB` caps single-file size; size shared
  storage and set quotas accordingly.

---

## 13. Cost model & pricing

The cost-per-user model, the dedicated-vs-shared **provider comparison**, and the
commercial case have moved to [`business-plan.md`](business-plan.md). It prices the
§6 density numbers into a €/user/month figure (CCX33 reference instance) and totals
the §15 reference platform. Re-price against your own provider before committing.

---

## 14. Recommended roadmap

| Stage | Topology | Trigger | Key actions |
|---|---|---|---|
| 0 | Model A, single node | Today | Resource limits (§7) — done; tune Postgres, set prod env/secrets |
| 1 | Model A, packed node | Up to ~8–12 stacks / node | Enforce limits, monitoring, backups |
| 2 | Model B, shared DB | Many small tenants | Externalise pool size, **add pgBouncer**, shared/HA Postgres |
| 3 | Model C, horizontal | Beyond one node / SLA | **Externalise attachments to object storage**, ≥2 API replicas + LB, CDN, managed HA DB |

### Prerequisites that unblock everything above Model A

1. ~~**Make `SetMaxOpenConns` configurable**~~ — **done.** The pool is now set via
   `OCTBASE_DB_MAX_OPEN_CONNS` / `OCTBASE_DB_MAX_IDLE_CONNS` (defaults 25/5), so the
   4-instance ceiling on a default Postgres can be tuned per deployment.
2. **Externalise the attachments directory** to shared object storage — the last
   piece of hidden local state blocking a stateless, horizontally scaled API tier.
   Still outstanding.

---

## 15. Reference deployment proposal — the 2,500-user platform

Sections 5–14 give the building blocks; this section assembles them into one
concrete, named end-state for a target of **100 client tenants × 25 users =
2,500 users**. It is a worked instance of **Model C** with the shared singletons
factored out exactly once. Treat the numbers as a sizing template — re-measure
(§3) and re-price (business-plan.md) against your own hardware before committing.

### 15.1 Design principles

1. **Shared core, deployed once.** Three services are *generic* — they are not
   per-tenant and there is no reason to run more than one logical copy of each
   for the whole platform:
   - **`postgres`** — one logical database, on its own machine(s), as an HA pair
     (primary + fallback), fronted by pgBouncer (§4, §9).
   - **`mailer`** — one stateless relay for the whole platform.
   - **`octbase-web`** — one marketing/landing site for the whole platform.
2. **Stateless app fleet, scaled horizontally.** The `octbase-api` + static
   SPA tier is the only thing that grows with users. It scales out as N identical
   replicas behind a load balancer — *once attachments are externalised* (§6.2,
   the gating prerequisite from §14).
3. **State lives in exactly two places:** the Postgres pair and the object store.
   Everything else is cattle: any node can be reimaged without data loss.
4. **N+1 everywhere that matters.** Every tier that serves the platform has at
   least one redundant unit so a single node failure (or a rolling deploy) never
   drops the SLA.

### 15.2 Topology

```
                                  Internet
                                     │
                          ┌──────────┴───────────┐
                          │   CDN (static SPA,    │   immutable, hashed assets
                          │   marketing assets)   │   — offloads most requests
                          └──────────┬───────────┘
                                     │  /api/*  + cache misses
                              ┌──────┴──────┐
                              │ Load balancer│  TLS terminate, round-robin,
                              │  (LB, no     │  /health checks, no sticky
                              │   sticky)    │  sessions (JWT stateless)
                              └──┬────────┬──┘
              ┌──────────────────┘        └───────────────────┐
              ▼                                                ▼
   ┌─────────────────────┐                      ┌──────────────────────────────┐
   │  WEBSITE EDGE (×2)   │                      │      APP FLEET (×8)           │
   │  small shared-vCPU   │                      │  CCX43 dedicated, identical   │
   │  · octbase-web       │                      │  · octbase-api  ×~15/node     │
   │  · mailer ──► SMTP    │                      │  · frontend/mobile static     │
   │  (stateless, generic)│                      │    (or served from CDN)       │
   └─────────────────────┘                      │  stateless · cattle           │
                                                 └───────────┬──────────────────┘
                                                 pgBouncer    │ object store
                                              (transaction)   │ (attachments)
                                                 ▼            ▼
                                   ┌───────────────────┐   ┌──────────────────┐
                                   │  DB PRIMARY        │   │  OBJECT STORAGE  │
                                   │  CCX33  + pgBouncer │   │  S3 / MinIO       │
                                   └─────────┬─────────┘   │  (replicated)     │
                                  sync/stream │             └──────────────────┘
                                   ┌─────────▼─────────┐
                                   │  DB FALLBACK       │   automated failover
                                   │  CCX33  (standby)  │   (Patroni / repmgr)
                                   └───────────────────┘
```

### 15.3 Bill of materials

Sizing uses the §6 density rule — **~1 API instance (25 users) per dedicated
vCPU, app-only/shared-DB**, with one core per node reserved for the OS/runtime.
On a **CCX43 (16 vCPU)** that is ~15 servable instances/node = **375 users/node**.

| Tier | Role | Unit | Count | Capacity | Redundancy |
|---|---|---|---|---|---|
| **App fleet** | `octbase-api` + static SPA, stateless | CCX43 (16 vCPU / 64 GB) | **8** | ~15 inst/node → 7 nodes serve 2,625 users; 8th is N+1 | survives 1 node down (7×375 = 2,625 ≥ 2,500) |
| **DB primary** | Postgres + pgBouncer | CCX33 (8 vCPU / 32 GB) | **1** | one logical DB for all tenants | — |
| **DB fallback** | Streaming standby, auto-failover | CCX33 (8 vCPU / 32 GB) | **1** | hot standby | promotes on primary loss |
| **Website edge** | `octbase-web` + `mailer`, stateless | small shared-vCPU (e.g. CX22) | **2** | generic, low traffic, CDN-fronted | 2× behind LB |
| **Object storage** | Externalised attachments | S3 / managed object store | usage | shared by whole fleet | provider-replicated |
| **Load balancer** | TLS, routing, health checks | managed LB | 1–2 | — | managed HA |
| **CDN** | Static SPA + marketing assets | edge cache | — | absorbs most request volume | provider HA |

**Why 8 app nodes and not 16× CCX33:** CCX43 and CCX33 cost the same per vCPU
(~€17.3/vCPU, business-plan.md §1.3), so fleet compute cost is ~identical either way — but 8 larger
nodes mean **half as many machines to patch, monitor and roll**. Prefer the
larger unit until a single node's blast radius becomes a concern. (16× CCX33 is
the drop-in alternative if you want a smaller failure domain per node.)

### 15.4 Solving the connection math at fleet scale (§4 applied)

8 nodes × ~15 instances = **~120 API instances** against **one** database. Naïvely
that is 120 × 25 = 3,000 connections — far past Postgres's limits. The fix is the
§4 stack, and it is mandatory here:

- **pgBouncer in transaction mode** on the DB host. The 120 instances fan into a
  small pool of real backends (`default_pool_size` ~50–100 is ample for this load).
- **Right-size the app pool:** set `OCTBASE_DB_MAX_OPEN_CONNS=10` (and
  `OCTBASE_DB_MAX_IDLE_CONNS=2`) per instance, so even direct pressure on pgBouncer
  is bounded.
- **Postgres `max_connections` ~200–250** on the DB host (32 GB absorbs the
  ~5–10 MB/backend cost comfortably). pgBouncer, not the app fleet, is what talks
  to those backends.

> Connection count vs. `max_connections` (and pgBouncer pool saturation) is the
> single most important capacity signal here — alert at 80% (§11).

### 15.5 Tenant model on the shared core

With one shared database, tenant isolation moves from a process boundary to the
data layer (§5 Model B/C, §12). Audit that every query is tenant-scoped and
consider Postgres **row-level security** as defence in depth. The app fleet is
fully multi-tenant: any replica can serve any of the 100 client tenants, which is
what lets the fleet scale on aggregate users rather than on tenant count.

### 15.6 Indicative cost

The priced bill of materials for this topology (~€3,030/mo all-in →
~€1.0–1.2/user/month at 2,500 users) lives in
[`business-plan.md`](business-plan.md) §3, alongside the single-node cost model.

### 15.7 What must ship before this topology is real

1. **Externalise attachments to object storage** — the outstanding §14
   prerequisite. Until done, the app fleet is *not* safely horizontal (a file
   uploaded via one replica is invisible to the others, §6.2). **This is the
   gating item.**
2. **Stand up pgBouncer + the HA DB pair** (Patroni/repmgr) with WAL archiving and
   PITR, and **test a failover and a restore** before cutover (§9).
3. **Per-service resource limits** (§7) on every app node so co-located instances
   can't starve each other.
4. **Wire `/health` to the LB** and ship metrics/logs to a central aggregator
   (§11) — the fleet is only as operable as its observability.

---

## 16. Hosting options — consolidated comparison & recommendation

Sections 5 and 10 introduced Models A/B/C as topologies. This section consolidates
*every* option considered for the commercial service into one decision reference,
adds the **shared-server / database-per-tenant** model (the practical sweet spot
that sits between Models A and B), and states the recommendation that the
[`business-plan.md`](business-plan.md) and [`growth-to-20-plan.md`](growth-to-20-plan.md)
build on.

### 16.1 The four options

**O1 — Dedicated stack per tenant** (instance-per-tenant; the current
`podman-compose` model = Model A). Each client gets the full four-container stack
*including its own PostgreSQL*.

- Isolation: maximal — separate process, separate DB, separate everything.
- App changes: none.
- Cost: highest — N× Postgres RAM/ops; ~1 GB per loaded stack.
- Backup/restore/upgrade/migration: trivially independent per tenant.
- Breaks down at: tens of tenants, where N separate Postgres instances become an
  operational burden (N backups, N patches, N monitors).

**O2 — Dedicated app stack + database-per-tenant on a shared Postgres server**
*(recommended)*. Each client gets its own app stack and its **own database with
its own DB role**, but all those databases live on **one shared Postgres server**
(or a few sharded servers).

- Isolation: strong — separate databases + separate roles mean a query bug or a
  leaked credential cannot cross tenants (the connection literally cannot see
  another tenant's database).
- App changes: **none** — no `tenant_id`, no query scoping, **no isolation audit**.
- Cost: far lower than O1 — one shared buffer cache and one set of background
  processes instead of N Postgres instances.
- Per-tenant logical backup/restore: yes (`pg_dump` one database).
- Hard requirement: the connection math (§4) — **pgBouncer becomes mandatory**.
- Caveats: shared server ⇒ shared blast radius (needs DB-tier HA); no per-tenant
  resource isolation inside Postgres (needs `statement_timeout` + per-role
  connection caps); PITR is **cluster-wide**, so run cluster WAL/PITR for disaster
  recovery *plus* per-tenant `pg_dump` for "undo my data" requests.
- Scales to ~100 tenants comfortably; **shard across 2–3 Postgres servers** beyond
  that — free, because each tenant is a self-contained database, so you just
  repoint its DSN at another server with no data migration of the rest.

**O3 — Shared everything / true multi-tenant** (Model B/C; one DB, shared schema).
One app fleet and one database serve all tenants; isolation is a `tenant_id`
column + Postgres row-level security.

- Cost: lowest per tenant at large scale.
- Isolation: logical only — a missed `WHERE` clause is a cross-tenant data leak.
- App changes: **large** — `tenant_id` on every table, scope every query, RLS,
  and a security-critical data-isolation audit (the slow, human, unavoidable part).
- Wins at: hundreds-to-thousands of small tenants, where O1/O2 per-database
  overhead finally dominates the cost.

**O4 — Horizontal API tier** (orthogonal — an HA option layered on O1/O2/O3).
Run ≥2 stateless `octbase-api` replicas behind a load balancer for HA or for a
large tenant.

- The API is already replication-ready (stateless JWTs, DB-backed refresh tokens)
  **except** for the local attachments directory. Externalising attachments to
  object storage (§6.2, §14) is the one prerequisite. Not needed for raw
  throughput at 25 users/tenant; needed for per-tenant fault tolerance.

### 16.2 At-a-glance comparison

| Dimension | O1 dedicated stack | O2 db-per-tenant (shared server) | O3 true multi-tenant |
|---|---|---|---|
| Data isolation | Maximal (separate DB process) | Strong (separate DB + role) | Logical (`WHERE` + RLS) |
| App code change | None | **None** | Large (+ audit) |
| Per-tenant cost | Highest | Low | Lowest |
| Ops surface | N stacks + N Postgres | N stacks + 1–3 Postgres | 1 fleet + 1 DB |
| Restore one tenant | Trivial | Easy (`pg_dump`) | Hard |
| Noisy-neighbour risk | None | Some (shared server) | Highest |
| Blast radius (DB) | One tenant | All on that server → needs HA | All tenants |
| Connection pooling | Not needed | **pgBouncer mandatory** | pgBouncer mandatory |
| Sweet spot | A few high-value tenants | **~10–100+ tenants** | Hundreds+ small tenants |

### 16.3 The economic consequence: minimum seats

In any *dedicated* model (O1/O2) the cost is **per stack/tenant**, not per user, so
a tiny client amortises a fixed per-tenant overhead over very few seats. Two ways
to cover that fixed cost: a **minimum billed seat count**, or — cleaner — a **flat
base fee per client** that funds the per-tenant overhead directly, after which a
seat minimum is unnecessary and clients of any size are profitable. The commercial
model in [`business-plan.md`](business-plan.md) takes the base-fee route (€19 base
+ ≤€2.95/seat, no seat floor). O3 dissolves the problem entirely (a one-user tenant
costs almost nothing) but only by taking on the §16.1 audit.

### 16.4 Recommendation

For Octbase's commercial service:

1. **Adopt O2 (database-per-tenant on a shared, HA Postgres server) as the
   default.** It delivers genuine per-client data isolation — the product
   differentiator — at a cost structure close to shared hosting, with **zero
   application changes** and no isolation audit.
2. **Reserve O1** for clients who contractually require a fully dedicated database
   *instance* (a premium isolation tier).
3. **Layer O4** (horizontal API + externalised attachments) once a per-tenant HA
   SLA is on the table.
4. **Defer O3** until the tenant count reaches the hundreds and per-database
   overhead — not architecture — becomes the binding cost.

---

*Resource figures in §3 are measured on an 8 vCPU / 32 GB host; re-measure on your
target hardware before committing to density numbers.*
