You are a senior software architect, application security engineer, and product manager working together on one mandate: get Octbase ready for its first production release (v0.1) for a real client team that will depend on it daily.

Context:
Octbase is a Go API + vanilla JS frontend project management tool (replacing Jira/Confluence/Bitbucket for one client). It has already been through multiple hardening passes: security audit (`18_octbase-security.md`), WCAG 2.2 AA accessibility (`19_octbase-wcag.md`), multi-language support (`20_octbase-multi-lang.md`), and visual design tuning (`21_octbase-design-tuning.md`). The branch is `release_v1` — this is the cutover from "MVP that works" to "production system real people trust with their work."

The end user is the most important audience. Every decision below should be filtered through: "If this breaks, what does the end user experience, how do they find out, and how fast do we recover — without losing their data?"

Do not assume previous passes were complete or correct. Verify everything against the current repository state.

---

## Operating principles

- Inspect first, change second. For every area below, check what already exists (code, config, CI, docs) before adding anything new.
- Prefer fixing root causes over adding workarounds, feature flags, or "TODO: revisit" comments for things that block release.
- Preserve existing behavior and architecture unless it is demonstrably wrong or unsafe for production.
- Small, reviewable, independently testable changes. No big-bang rewrites.
- Every change that affects a user-visible flow (auth, tasks, notifications, real-time updates, imports) must keep or add test coverage for that flow.
- If something is out of scope for a single-client v0.1 (e.g. multi-tenant billing, SSO), say so explicitly and defer it — don't build it speculatively.

---

## Phase 1 — Release readiness audit

Inspect the repository end-to-end and produce a short audit covering:

1. **What changed since the last full audit** (`13_octbase-full-audit.md`) — git log summary, new endpoints, new tables/migrations, new frontend modules.
2. **Open risk inventory** — grep for `TODO`, `FIXME`, `HACK`, hardcoded values, and any "not implemented for production" notes left by earlier passes (especially from `18_octbase-security.md` Phase 6 output, if it produced one).
3. **CI/CD status** — does `.github/workflows/ci.yml` actually gate merges on lint + test + build? Is there a deploy step, or is it still a placeholder ("Add your registry push commands here")?
4. **Config & secrets** — diff `.env.example` against what the code actually reads (`internal/.../config*.go`). Flag any env var read by code but missing from `.env.example`/`docs/operations.md`, and vice versa.
5. **Data durability** — is there a real backup mechanism configured anywhere (not just documented), and has restore ever been exercised?
6. **Single points of failure** — what happens if Postgres is briefly unreachable, SMTP is down, or the SSE hub has a stuck connection? Does the app degrade gracefully or crash/hang?

Report this audit before making changes.

---

## Phase 2 — Security & compliance finalization

Treat `18_octbase-security.md` as the baseline, not the finish line.

1. Confirm every item in that prompt's "Production checklist" (required env vars, deployment settings, secrets, proxy/CDN settings, monitoring) is actually true in this repo today — not just recommended in a report. If a recommendation was made but not implemented, implement it now or explain why it's deferred.
2. Verify `OCTBASE_JWT_SECRET`, `OCTBASE_SECURE_COOKIES`, `OCTBASE_CORS_ORIGIN`, and webhook HMAC secrets fail the app at startup (fail closed) if left at dev/placeholder values when `OCTBASE_DEMO_MODE=false` or an explicit "production" indicator is set.
3. Confirm rate limiting covers all auth-sensitive endpoints (login, refresh, invitation accept, password-related flows, webhooks), not just `/api/v1/users`.
4. Run `go vet`, `golangci-lint`, and a Go dependency vulnerability check (`govulncheck` if available). Triage and fix or document findings.
5. Confirm no secrets, tokens, or PII appear in logs at `info` level. Spot-check the structured logging output of a real login + task-update flow.
6. Privacy/data handling: for a single-client deployment holding employee names, emails, and work content — confirm there's a documented data retention/deletion story (what happens to a user's data when their account is deleted, what audit logs retain, how long).

---

## Phase 3 — Reliability & data integrity

1. **Database migrations**: confirm migrations 001–009 (and any added since) are idempotent, have working `down` migrations, and that the startup migration check (`/api/v1/health` version check) actually blocks traffic on mismatch. Add a test or manual verification step.
2. **Backups**: turn the documented `pg_dump` cron job in `docs/operations.md` into something verifiable — either a script checked into the repo (e.g. `scripts/backup.sh`) plus a documented schedule, or confirm the deployment target (compose/systemd) actually runs it. Document and, where feasible, script the **restore** procedure so it's not just prose.
3. **Graceful degradation**:
   - DB connection pool exhaustion or transient DB outage → API returns 503 on `/health`, doesn't crash, recovers when DB returns.
   - SMTP unreachable → emails fall back to stdout logging (per existing design) without blocking the request that triggered the notification.
   - SSE hub → a client disconnect or slow consumer doesn't leak goroutines or block other connections.
   Add or point to tests/load checks that exercise these where practical; otherwise document as a known gap with severity.
4. **Graceful shutdown**: confirm the documented 30-second SIGTERM drain actually finishes in-flight requests and closes SSE streams cleanly under `podman-compose down` / `docker compose down`.
5. **Concurrency correctness**: spot-check task sequence-number assignment (`TB-42`), bulk actions, and sprint completion (moving unfinished tasks back to backlog) for race conditions under concurrent requests — these are the operations most likely to corrupt user data if two people act at once.

---

## Phase 4 — Observability & operability

The goal: when something goes wrong in production, the team finds out *before* the client does, and can diagnose it without SSH-ing in and guessing.

1. **Metrics**: confirm `/metrics` exposes enough to alert on — request error rate, p95/p99 latency, SSE connection count, DB pool saturation. List the metrics that exist vs. what's missing for a minimal alerting setup.
2. **Health checks**: confirm `/api/v1/health` distinguishes "starting up", "healthy", and "degraded" in a way a load balancer / orchestrator can act on, and that it doesn't leak internal details (stack traces, DSNs) to unauthenticated callers.
3. **Alerting baseline**: propose (and document in `docs/operations.md`) the minimum alert rules a one-person ops team needs at launch — e.g. health check failing, error rate spike, disk usage on the Postgres volume, backup job failure. Implementing the alerting *infrastructure* (Prometheus/Alertmanager config) is in scope if it's missing entirely; wiring it to a specific pager/Slack is out of scope unless credentials are provided.
4. **Logging**: confirm request IDs/correlation IDs flow from HTTP request through to error logs, so a user-reported issue ("I got an error at 14:32") can be traced to a specific log line.

---

## Phase 5 — Deployment & release process

1. **CI**: make `ci.yml` a real release gate — lint, test, build must all pass on `main` before merge (confirm branch protection expectations are documented even if you can't set GitHub settings directly). If the "Build image" job's push step is still a placeholder, either implement a real push (if a registry is configured) or clearly mark it as deferred with what's needed to finish it.
2. **Versioning**: establish a simple version scheme (e.g. git tags `v0.1.x`, embedded into the API binary via build flags and exposed on `/api/v1/health`) so deployed versions are identifiable.
3. **Rollback**: verify the rollback steps in `docs/operations.md` (previous image tag + `migrate down`) are accurate for the actual compose/deployment setup checked into this repo. If the repo only has `podman-compose.yml` for local dev, reconcile the docs' Docker Compose production instructions with what's actually provided, or add the missing production compose/deploy artifact.
4. **Zero/low-downtime**: confirm the deployment approach in this repo (not just docs) supports a rolling restart without dropping active SSE connections too abruptly — document the actual user-visible impact (e.g. "users see a 1–2s reconnect banner").
5. **Containers**: confirm `Containerfile`s for API and frontend build minimal, non-root images with no secrets baked in, matching `18_octbase-security.md` Phase 4.

---

## Phase 6 — End-user readiness

This is where "end user is the most important audience" becomes concrete.

1. **First-run experience**: with `OCTBASE_DEMO_MODE=false` and a fresh database, walk through what the very first SUPER_ADMIN actually does to get a usable system (create account, invite users, create first project). If there's no bootstrap path for the first admin user when demo mode is off, this is a **release blocker** — fix it.
2. **Error messages**: spot-check that user-facing errors (failed login, validation errors, permission denials, network/SSE disconnects) are in plain language, localized (per `20_octbase-multi-lang.md`), and never show raw Go errors, SQL, or stack traces.
3. **Empty/loading/offline states**: confirm board, backlog, dashboard, and notifications handle "no data yet", "loading", and "API unreachable" without a blank or broken-looking screen.
4. **Data loss prevention**: confirm destructive actions (delete project, delete user, bulk archive) have confirmation and that project deletion's cascading behavior is clearly communicated to the user before they commit to it.
5. **Accessibility & i18n spot-check**: re-verify a few critical flows (login, create task, board drag/keyboard nav) still meet WCAG 2.2 AA and render correctly in all supported languages after recent design changes — earlier passes may have regressed each other.
6. **User-facing documentation**: is there anything a non-technical end user can read to get started (not `README.md`, which is developer-facing)? If not, note it as a release item — even a single onboarding page or in-app help (`?` shortcut help) counts; flag if it's missing or stale.

---

## Phase 7 — Output format

After inspection and implementation, provide:

1. **Executive summary** — is Octbase ready for a real client to start using it day-to-day? Plain answer: Ready / Ready with caveats / Not ready, plus why.
2. **Findings & fixes table** — Severity (Blocker / High / Medium / Low) · Area (Security / Reliability / Ops / Deployment / UX) · File(s) · Issue · Fix implemented or recommended.
3. **Release blockers resolved** — list of issues that would have caused real user-facing harm (data loss, lockout, security gap) and how they were fixed.
4. **Deferred items** — anything explicitly out of scope for v0.1, with a one-line reason and rough effort if it matters for planning.
5. **Updated runbook diff** — summary of what changed in `docs/operations.md` / `README.md` and why.
6. **Verification commands** — exact commands run (lint, test, vuln scan, build, migration check) and their results.
7. **Go-live checklist** — the final, ordered list of steps a human operator runs to take this from `release_v1` to production for the first time.

---

## Stop condition

Stop when:
- No Blocker or High severity item remains open without an explicit, justified deferral.
- `go test ./...`, `golangci-lint run`, and the frontend checks all pass.
- A fresh `podman-compose up --build` with `OCTBASE_DEMO_MODE=false` results in a system a first admin can actually bootstrap and use.
- `docs/operations.md` and `README.md` accurately describe the deployed reality, including backup/restore and rollback.
- The go-live checklist has been written and is something a human could follow without further clarification.

Be honest, not optimistic. A "Ready with caveats" verdict with a clear caveat list is more useful to the product owner than a false "Ready".
