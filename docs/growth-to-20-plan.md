# Octbase Growth Plan — MVP to a 20-Instance Commercial Service

> Status: Planning document / roadmap
> Audience: Engineering and operations
> Companion: [`hosting-concept.md`](hosting-concept.md) (topologies, density, cost model)
> and [`operations.md`](operations.md) (per-variable runbook).

`hosting-concept.md` defines *where* Octbase can run and what it costs.
This document defines *what has to be built and operated* to take today's MVP
single-stack to a commercially run fleet of **20 instances of the core app**, in
what order, and roughly how long each step takes.

---

## 1. Baseline — what the MVP already gives us

Grounded in the current code, not aspiration:

- **The app tier is already lean and stateless.** `octbase-api` is a ~6 MB-idle
  Go binary. Access tokens are stateless JWTs; refresh tokens are validated
  against the DB. No server-side session store, so no sticky sessions.
- **State lives in exactly two places:** PostgreSQL, and the **local attachments
  directory** (`OCTBASE_ATTACHMENTS_DIR`, see `cmd/octbase-api/main.go`). The
  second is the one piece of hidden local state that blocks a horizontal API tier.
- **The schema is single-tenant.** There is no `tenant`/`org` column in any
  migration. Today, "20 instances" therefore means **20 separate single-tenant
  deployments** (Model A), one per customer — not 20 replicas of one shared
  service.
- **CI builds but does not ship.** `.github/workflows/ci.yml` runs lint, test,
  frontend checks and image build. There is **no registry push and no deploy
  step** — every rollout is a manual `podman-compose up`.
- **Migrations run automatically at every API startup** (golang-migrate, with an
  advisory lock). Safe for one stack; needs a deliberate fleet strategy.

**Conclusion:** reaching 20 instances is mostly an *operations and provisioning*
problem plus *one* app refactor (attachments). It is **not** an app-performance
problem — the runtime is already efficient.

---

## 2. The strategic fork: tenancy model (decide first)

Because the schema is single-tenant, "20 instances" resolves two ways. Everything
downstream depends on this choice.

| | **Path A — 20 isolated stacks** | **Path B — one multi-tenant service** |
|---|---|---|
| App code change | ~none (already works this way) | Large: `tenant_id` on every table, query scoping, RLS |
| Isolation | Strongest — blast radius = one customer | Logical only, at the data layer |
| Infra @ 20 | ~2 nodes (Model A, 8–12 stacks/node) | Cheaper DB, but carries a schema-migration + audit project |
| Best fit | High-value B2B tenants, strict isolation | Many small / self-serve tenants, cost-sensitive |

**Recommendation: take Path A to reach 20.** Twenty isolated stacks is ~2 boxes,
gives the data isolation enterprise buyers expect, and needs no schema rewrite.
Only invest in Path B (Section 6) if the business model is many small self-serve
tenants where per-stack overhead dominates. The roadmap below is Path-A-first.

> **Committed topology:** within Path A we adopt **option O2 — dedicated app
> stack + database-per-tenant on a shared, HA Postgres server** (see
> [`hosting-concept.md §16`](hosting-concept.md)). It keeps the zero-app-change /
> no-isolation-audit property of dedicated stacks while collapsing N Postgres
> instances into one operable (sharded, HA) server. The practical effect on the
> phases below: **Phase 3 (pgBouncer + DB-tier HA) is promoted from optional to
> the centerpiece**, and the per-tenant Postgres of pure Model A is replaced by a
> per-tenant *database* on the shared server. The commercial rationale and unit
> economics live in [`business-plan.md`](business-plan.md).

---

## 3. Roadmap

### Phase 0 — Harden one production stack
The current stack is a *dev* stack; make one stack genuinely production-grade
before multiplying it.

- Demo mode **off** (`OCTBASE_DEMO_MODE=false` — it seeds demo data and enables a
  dev JWT fallback), unique 32-byte `OCTBASE_JWT_SECRET`,
  `OCTBASE_SECURE_COOKIES=true`, real `CORS_ORIGIN`/`APP_URL`, `SCM_ENC_KEY`,
  webhook secrets.
- Move secrets **out of `.env` files** into a secret store. With 20 instances each
  needing a *unique* JWT secret and DB credentials, flat `.env` files become the
  primary operational risk.
- TLS via the existing `Caddyfile.tls`; wire `/health` to the fronting proxy.

### Phase 1 — Externalise attachments to object storage *(gating refactor)*
The #1 outstanding prerequisite from `hosting-concept.md §14`. Add an **S3/MinIO
backend behind the existing `workmanagement` attachment-storage interface**,
selected by env, keeping the filesystem backend for single-stack/dev.

Why it gates everything: without it you cannot run a second replica, cannot do a
zero-downtime rolling deploy (in-flight uploads disappear), and backups mean
tar-ing 20 separate volumes.

### Phase 2 — Fleet provisioning & deploy automation *(the real "commercial" leap)*
Going from 1 → 20 is an operations problem. "Onboard customer #N" must be one
command, not a checklist.

- **CI/CD:** extend `ci.yml` to push tagged images to a registry, then add a
  deploy job that rolls the fleet.
- **Provisioning template:** per-instance config (subdomain, DB/schema, secrets,
  TLS cert) generated from a single source of truth.
- **Orchestration:** past ~a dozen instances, hand-managed compose files break.
  Move to a small orchestrator (Nomad, or k8s if that's the direction).
- **Migration strategy:** keep auto-migrate for Path A, but make rollout
  migration-aware (migrate, then swap) so 20 startups hold no surprises.

### Phase 3 — Database & backup maturity
- Add **pgBouncer** (transaction mode) the moment any DB is shared — the
  documented fix for the 4-instances-per-Postgres connection wall (pool default
  25 vs Postgres default 100).
- Right-size the pool per deployment (`OCTBASE_DB_MAX_OPEN_CONNS`, already an env
  var).
- Replace the `pg_dump` cron with **WAL archiving + PITR**, and **test restores**.
  Twenty customers makes data loss a contractual problem.

### Phase 4 — Fleet observability
Per-instance `/metrics` and `/health` already exist. The gap is aggregation: ship
all 20 instances' logs/metrics to one place (Prometheus + Loki/ELK) with the
alerts the hosting doc recommends (DB conns >80% of cap, RAM >85%, attachment
volume >80%).

### Phase 5 *(fork, only if business demands)*
- **HA (Model C):** ≥2 API replicas behind an LB, managed/HA Postgres, CDN for
  static assets — cheap *once Phase 1 ships*, because the API is already stateless.
- **Path B multi-tenancy:** the `tenant_id` + RLS project (Section 6).

### Sequencing

```
Phase 0 (harden one) ─► Phase 1 (attachments → object store)  ◄── gating, do early
        │                        │
        └──────► Phase 2 (CI/CD + provisioning)  ◄── the actual 1→20 enabler
                         │
                         ├─► Phase 3 (pgBouncer + PITR)
                         └─► Phase 4 (fleet observability)
                                     │
                                     └─► Phase 5 (HA / multi-tenancy, if needed)
```

---

## 4. Effort estimate

Assumptions: **one experienced backend/infra engineer**, working against this
codebase (small, well-factored, good test coverage). Ranges are calendar time
including testing and docs, not just coding. Phases 1–4 can overlap somewhat;
the **serial critical path** is roughly the sum of Phases 0–2.

| Phase | Scope | Effort | Notes |
|---|---|---|---|
| **0** | Harden one prod stack, secret store, TLS | **0.5–1 week** | Mostly config; secret-store integration is the variable |
| **1** | S3/MinIO attachment backend behind existing interface | **1–2 weeks** | Real code: backend, env wiring, migration of existing files, tests. The gating item |
| **2** | Registry push + deploy automation + provisioning template + orchestrator | **2–4 weeks** | Widest range — depends on staying on compose vs adopting Nomad/k8s |
| **3** | pgBouncer + pool tuning + WAL/PITR + tested restore | **1–2 weeks** | pgBouncer is fast; PITR + a *proven* restore drill is the bulk |
| **4** | Central logs/metrics + dashboards + alerts | **1–1.5 weeks** | Off-the-shelf stack; mostly wiring and dashboards |
| | **Path-A total (to a real, operable 20-fleet)** | **≈ 6–10 weeks** | One engineer; less with two working in parallel on 2–4 |

Add-ons, only if chosen:

| Optional | Effort | Notes |
|---|---|---|
| **Phase 5 — HA (Model C)** | **+2–4 weeks** | LB, HA Postgres (Patroni/repmgr), CDN; cheap *after* Phase 1 |
| **Path B — multi-tenancy** | **+6–12 weeks** | `tenant_id` across the schema, scope every query, RLS, full re-test and data-isolation audit. A project in its own right — do not bundle into the 20-fleet timeline |

**Bottom line:** a credible, operable **20 isolated-stack** service is roughly
**6–10 engineer-weeks** of focused work, with the **attachments refactor (Phase 1)
and provisioning automation (Phase 2)** as the two items on the critical path.
Everything else is either configuration or off-the-shelf tooling. Multi-tenancy
(Path B) is a separate, larger investment to make only if the business model
requires it.

---

*Re-measure resource footprints (`hosting-concept.md §3`) and re-price
(`§13`) against your own target hardware before committing to density or cost
numbers.*
