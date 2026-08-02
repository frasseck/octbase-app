# Octbase — deep security assessment (professional-scan replacement)

You are a **senior offensive + application-security consultant** engaged to run a
full security assessment of Octbase. This engagement stands in for a paid
third-party security scan / penetration test: the deliverable is a
**professional-grade findings report backed by evidence**, not a hardening
sprint. Octbase is a **fully functioning, production-deployed enterprise
application** (one stack per client), not an MVP — treat it as live software
whose owners need to know their real, exploitable risk before the next audit or
customer security review.

You are Opus 4.8 executing this later, autonomously. Work from the actual code;
do not assume, guess, or accept a control as present because it "should" be —
open the file and confirm. Every claim in your report must cite `file:line` or a
reproduced observation.

---

## 0. Rules of engagement

- **Assess, don't rewrite.** This is a scan/pentest replacement. Your primary
  output is findings + evidence + a report. Do **not** modify product code,
  migrations, or config as part of the assessment. If the user explicitly asks
  for remediation afterward, that is a separate, gated phase (see §8).
- **Non-destructive.** You may read code, run the test suite, run static/SCA
  tooling, stand up a **local disposable** stack, and exercise it with curl /
  the driver. Never touch a real client deployment, never run destructive DB
  operations, never exfiltrate real data. Use the `dev-stack` skill for a
  throwaway seeded stack and the `run-octbase` / `frontend-testing` skills to
  drive it.
- **Evidence over assertion.** A finding without a concrete
  failure/exploitation path is a *hypothesis* — either prove it (PoC request,
  reproduced response, failing negative test you write in a scratch dir) or
  downgrade it to "informational / needs manual confirmation." Do not pad the
  report with theoretical issues dressed as confirmed ones.
- **Adversarial mindset — go beyond the checklist.** The repo already documents
  its own security invariants (the `go-security` and `js-security` skills). Use
  them as a **regression baseline you verify**, then push past them: a
  professional scan finds what the invariant list *doesn't* anticipate. Ask "how
  would I break this," not "is the documented control still here."
- **Never log or paste secrets** (tokens, passwords, session IDs, `Authorization`
  headers, cookies, encryption keys, PII) into the report or scratch files.
  Redact. If you must reference a leaked secret you found, cite its location,
  not its value.
- Scratch work (PoC scripts, captured traffic, tool output) goes in the
  scratchpad dir, never in the repo tree.

---

## 1. Ground truth — the system under test

Octbase is a split monorepo: a **Go modular monolith** API plus **no-build
static JS** frontends behind **Caddy**, on **PostgreSQL**, deployed via
**Podman Compose**, one stack per client. Read `CLAUDE.md`,
`docs/architecture.md`, `docs/technical_documentation.md`,
`docs/hosting-concept.md`, and `.env.example` before touching anything, then
confirm each of the following against the code rather than trusting this summary:

| Component | Path | Security-relevant surface |
|---|---|---|
| API (Go) | `octbase-api/` | JWT-only auth, chi router, bounded contexts under `internal/`, golang-migrate, OpenAPI |
| Desktop SPA | `octbase-frontend/` | plain-DOM `innerHTML` rendering + **Caddy front door** (reverse-proxies `/api`, serves `/m/`) |
| Mobile SPA | `octbase-mobile/` | served under `/m/`, its own Caddy |
| Shared JS | `octbase-shared/` | byte-identical sanitizer/i18n synced to both SPAs (drift = sanitizer holes) |
| Deploy | `podman-compose.yml`, `podman-compose.dev.yml`, `octbase-frontend/caddy/` | containers, edge CSP/headers, install-password gate |
| Config | `.env.example` | all env: `OCTBASE_JWT_SECRET`, `OCTBASE_SECURE_COOKIES`, `OCTBASE_CORS_ORIGIN`, `OCTBASE_SCM_ENC_KEY`, `OCTBASE_MFA_ENC_KEY`, `OCTBASE_TRUSTED_PROXIES`, `OCTBASE_APP_URL`, `OCTBASE_DEMO_MODE`, `OCTBASE_SITE_AUTH`, `OCTBASE_DATABASE_URL` |

**Baseline the executor should verify, not re-derive:** the established
invariants are catalogued in the `go-security` and `js-security` skills (auth /
JWT / sessions / passwords / password-reset / MFA / disabled-accounts / SQL /
file-upload / XSS sanitizer / SSRF / OAuth-CSRF / webhooks / headers-CORS /
trusted-proxy / rate-limit; and browser-side: innerHTML escaping discipline,
DOMPurify policy, token-in-memory storage, tokens-in-fragment, URL-override dev
gating, edge CSP `script-src 'self'`). **Invoke both skills** and run their
regression sweeps as step one — a broken invariant is an immediate finding. The
last full audit is `docs/security-audit-2026-07-02.md`; read it so you don't
re-report known-fixed issues and so you close its explicit gaps (it flagged that
the **TOTP MFA subsystem was never put through the invariant checklist** — that
gap is in-scope for you now).

---

## 2. Phase 1 — Recon, inventory & threat model

Produce a concise but complete map. A pro scan starts by knowing the whole
attack surface:

1. **Entry points & routes:** enumerate every `/api/v1` route from the chi
   router and cross-check against the OpenAPI spec (there's an `apicontract`
   test that asserts route↔spec parity — use it). Mark each route
   **authenticated / public / webhook / SSE**. The public set is small and
   fixed (`auth/login`, `auth/refresh`, `auth/mfa/verify`, `auth/forgot-password`,
   `auth/reset-password`, invitation inspect/accept, HMAC webhook receivers,
   SSE optional-JWT) — any route outside that set that reaches data without
   `JWTMiddleware` + `LoadUserGlobalRole` is a critical finding.
2. **Trust boundaries:** browser ↔ Caddy ↔ API ↔ Postgres; API ↔ external SCM
   providers (outbound); API ↔ SMTP. Draw where untrusted input crosses each.
3. **Roles & tenancy model:** global roles vs project memberships/roles; what
   "one stack per client" means for isolation (there is **no** cross-client
   multitenancy inside one DB — confirm that assumption holds and note it).
4. **Data classification:** what PII and secrets exist (user name/email, bcrypt
   hashes, encrypted SCM tokens, encrypted TOTP secrets, session/reset/recovery
   token hashes) and where each lives.
5. **Threat model:** enumerate threats per STRIDE and per **OWASP Top 10 (2021)**
   + **OWASP API Security Top 10 (2023)**, ranked by likelihood×impact for *this*
   app. Call out up front that **Broken Object-Level / Function-Level
   Authorization (BOLA/BFLA/IDOR) is the highest-risk class here** — it is a
   project-scoped, membership-gated app rendering other users' content, and the
   most recent real bugs were exactly missing read-side membership guards.

---

## 3. Phase 2 — Static analysis & code review (SAST)

Cover the OWASP verification standard (**ASVS L2** as the bar for an enterprise
app) across the backend. Do not just run the invariant greps — read the
security-critical code paths and reason about them:

1. **AuthN:** JWT construction/verification (alg pinning, `alg=none`, issuer/
   audience/expiry/nbf), the **challenge-token issuer isolation** (an MFA
   challenge token must never pass `JWTMiddleware`; an access token must never
   pass `/auth/mfa/verify`), refresh rotation + reuse detection, logout/
   revocation, disabled-account rejection on every request.
2. **AuthZ (deepest dive):** for **every** project-scoped handler — including
   `List`/`Get`/`Search` reads — confirm `ProjectMemberGuard`/`memberGuard`
   runs before any data is returned; for child routes carrying only a child ID
   (e.g. `/pages/{id}/revisions`) confirm the parent is resolved to its
   `ProjectID` and guarded there. Confirm function-level gating on admin/
   usermgmt/retention routes. Build the route→guard matrix and flag every gap.
   Then **prove** at least the highest-risk gaps dynamically in Phase 3.
3. **Injection:** SQL parameterization everywhere; dynamic `ORDER BY`/filter
   only from whitelists; `LIKE`/`ILIKE` metacharacter escaping; no `os/exec`,
   no `text/template` for HTML, no `fmt.Sprintf` building predicates or paths.
4. **Crypto & secrets:** AES-256-GCM + `crypto/rand`, dedicated keys
   (`OCTBASE_SCM_ENC_KEY` vs `OCTBASE_MFA_ENC_KEY`), key-length enforcement,
   no `math/rand` for anything security-bearing, bcrypt cost, SHA-256 at-rest
   for tokens/recovery-codes, constant-time comparisons for webhooks/HMAC.
5. **Password & account lifecycle:** password policy on *every* set-password
   path, timing-safe enumeration defenses on login and forgot-password
   (single fixed 202, async mail), single-use/TTL/one-outstanding reset tokens,
   MFA disable/regenerate requiring fresh proof, reset revoking all refresh
   tokens.
6. **File upload/download:** content-type sniff + allowlist, SVG rejection,
   opaque storage keys, path-traversal/root-escape checks, `Content-Disposition:
   attachment` + `nosniff`, size limits.
7. **SSRF / outbound:** every request-controlled outbound host must go through
   the SCM egress guard (scheme + internal-IP preflight + DNS-rebind-safe
   dialer that blocks loopback/RFC1918/RFC6598/link-local incl. `169.254.169.254`).
   Grep for any bare `http.Get`/`http.Client` on a user-influenced URL.
8. **CSRF / OAuth state / open redirect:** JWT-in-header model's CSRF exposure,
   OAuth `state` one-time/expiring/consumed, redirect target fixed to env.
9. **Error handling & info leak:** generic client errors, no stack traces / SQL
   errors / internal paths in responses, panic recovery, structured logs with
   redaction and request IDs, **no secrets/PII in logs** (verify by actually
   capturing stdout from a running instance in Phase 3).
10. **Rate limiting & DoS:** per-IP limits on all public/expensive routes,
    body-size limits, server/DB/outbound timeouts; dependence on `RealIP` being
    trustworthy only behind `OCTBASE_TRUSTED_PROXIES`.
11. **Business-logic & state:** optimistic-locking bypass, mass-assignment via
    struct-as-DTO (can a client set a field it shouldn't — role, owner,
    version, IDs?), enum/default tampering, workflow ordering abuse.

Frontend SAST (invoke `js-security` and run its sweep, then read new render
paths): innerHTML escaping discipline, DOMPurify policy (client mirrors server —
server is source of truth), `rtSafeHref`/`rtSafeImageSrc`, token-in-memory-only,
tokens only in `#` fragment, no `eval`/`new Function`/`document.write`, no inline
`<script>`, URL overrides dev-gated, `localStorage` holds prefs not secrets.

---

## 4. Phase 3 — Dynamic testing (DAST / manual pentest)

Stand up a **local disposable seeded stack** (`dev-stack` skill; use the dev
overlay for Mailpit so you can inspect reset/invite mail). Then actively attack
it — this is where a scan earns its keep. Confirm the app under test, its port,
and demo logins from the skill, and drive it with curl and the `run-octbase`
`driver.py`.

Attacks to actually execute and capture request/response evidence for:

1. **Unauthenticated reach:** hit protected routes with no / expired / malformed
   / `alg=none` / wrong-issuer tokens → expect 401, never data.
2. **BOLA/IDOR (priority):** create two users; make user A a member of a PRIVATE
   project and user B a non-member. As B, attempt to read every project-scoped
   resource of A's project by ID — tasks, boards, backlog, releases, sprints,
   categories, templates, pages, revisions, references, attachments, activity,
   comments. Any 200 returning A's data is a **Critical** finding with a PoC.
3. **BFLA:** as a plain member, call admin/usermgmt/retention/role-change
   endpoints → expect 403.
4. **Cross-tenant/privilege escalation:** attempt to set your own global role,
   another user's role, project ownership, or `version`/owner fields via PATCH
   (mass-assignment).
5. **Auth flows:** login enumeration timing (unknown vs known user), lockout /
   rate-limit thresholds (hammer login → expect 429 and confirm reset window),
   forgot-password single-202 behavior for unknown/disabled/deleted accounts,
   reset-token reuse (redeem twice → second fails), MFA challenge-token misuse
   across endpoints, refresh-token replay (reuse a rotated refresh → all
   sessions revoked).
6. **Injection payloads:** SQLi-style strings in search/filter/sort params
   (expect parameterized safety + `LIKE` metachar handling, no 500s); path
   traversal in any file/attachment path param.
7. **XSS:** store rich-text/task/page/comment content containing
   `javascript:` URLs, `<img onerror>`, `<svg>`, `data:` images, malformed HTML;
   fetch it back and confirm the **server** sanitizer neutralized it (client is
   secondary). Verify an `<img>` with a disallowed src is stripped, links get
   `rel=noopener noreferrer`.
8. **File upload:** upload an SVG (must reject), a polyglot / renamed
   executable, an oversized file, a content-type/extension mismatch; confirm
   download forces `attachment` + `nosniff`.
9. **SSRF:** create an SCM connection whose `apiBaseUrl` targets `127.0.0.1`,
   an RFC1918 host, `169.254.169.254`, a DNS name resolving to internal, and an
   http→internal redirect → expect `SCM_URL_NOT_ALLOWED` / blocked dial.
10. **Webhooks:** post to both receivers with a bad/missing HMAC signature and an
    oversized body → expect rejection; confirm constant-time compare.
11. **Headers/CORS/CSP:** inspect response headers on API and on the Caddy front
    door for both SPAs — nosniff, frame-ancestors/X-Frame-Options, Referrer-
    Policy, Permissions-Policy, HSTS (TLS config), and the strict CSP
    (`script-src 'self'`, `connect-src 'self'`, no `unsafe-inline`); attempt a
    disallowed cross-origin credentialed request → must not be reflected; test
    `Origin: null`.
12. **Cookie flags:** confirm the refresh cookie is `HttpOnly` + `Secure`
    (when `OCTBASE_SECURE_COOKIES=true`) + `SameSite=Strict`, and the
    JS-visible presence marker holds no secret.
13. **Log leak:** capture the running API's stdout across login + a task update
    and grep for `Authorization`, `password`, `token`, `Cookie`, email
    addresses — anything sensitive that appears is a finding.
14. **Install-password gate:** the two-var `OCTBASE_SITE_AUTH` front-door toggle
    — confirm it fails closed correctly and doesn't crash-loop (there's prior
    history here; see git log).

---

## 5. Phase 4 — Dependency, supply-chain & secrets (SCA + secret scanning)

A professional scan always includes these — run the tooling, don't eyeball:

1. **Go dependencies:**
   ```bash
   cd octbase-api
   go vet ./...
   go run golang.org/x/vuln/cmd/govulncheck@latest ./...
   golangci-lint run ./...   # includes gosec-class linters if configured
   ```
   Report every reachable CVE with its advisory ID, affected version, fixed
   version, and reachability (govulncheck reports call-path reachability — say
   whether the vulnerable symbol is actually reached).
2. **Frontend deps:** the SPAs are **no-build** (no `package.json` runtime
   deps) — the only third-party JS is **vendored** (DOMPurify/`purify.js`,
   `qrcode.js`). Verify each vendored file against its known-good upstream
   release for tampering/backdooring and note the pinned version + whether it
   carries known CVEs. Flag if any vendored lib is outdated.
3. **Container images:** review base images in the Dockerfiles / compose
   (Postgres, Caddy — note Caddy is a shell-less minimal image), image pinning,
   non-root execution, no secrets baked into layers. Run a container/image
   vuln scan if tooling is available (e.g. `trivy`/`grype`); otherwise document
   the image tags and known-advisory status manually.
4. **Secret scanning:** scan the whole tree (and, if cheap, git history) for
   committed secrets — API keys, private keys, real JWT secrets, DB passwords,
   `.env` files that should be ignored. Confirm `.env` is gitignored and only
   `.env.example` with placeholders is committed. `git log -p` grep for
   accidentally-committed-then-removed secrets is in scope (a secret in history
   is still leaked).

---

## 6. Phase 5 — Infrastructure, deployment & config hardening

Inspect `podman-compose.yml`, `podman-compose.dev.yml`, all Caddyfiles
(`octbase-frontend/caddy/`, `octbase-mobile/caddy/`), `.env.example`,
`.github/workflows/ci.yml`, and `docs/operations.md` / `docs/hosting-concept.md`.
Assess and report (recommend, don't change):

1. **Fail-closed production config:** does the app refuse to start in
   non-demo mode with a dev `OCTBASE_JWT_SECRET` / `<32B` secret,
   `OCTBASE_SECURE_COOKIES=false`, or wildcard/empty `OCTBASE_CORS_ORIGIN`?
   Verify by attempting to boot with each insecure value.
2. **TLS/HSTS:** HTTPS-only in prod, HSTS only where TLS is terminated
   correctly, secure cookies behind proxy, `sslmode=require`/`verify-full` on
   the production DB URL.
3. **Secrets management:** `OCTBASE_MFA_ENC_KEY` / `OCTBASE_SCM_ENC_KEY` /
   `OCTBASE_JWT_SECRET` sourced from env not image, key rotation implications
   documented, no default keys usable in prod.
4. **Container posture:** non-root, minimal images, least-privilege DB user,
   no dev services (Mailpit) in the deployable stack, health checks that don't
   leak sensitive info, `/metrics` access-restricted.
5. **Reverse-proxy correctness:** `OCTBASE_TRUSTED_PROXIES` set so RealIP (and
   thus rate-limit + audit IPs) is trustworthy; which layer owns which security
   header so none is silently dropped if a layer changes.
6. **CI security gates:** does CI run tests + lint + coverage floor? Note the
   absence of any automated `govulncheck` / secret-scanning / SAST step in CI as
   a process finding, and recommend adding them.

---

## 7. Deliverable — the professional report

Write the report to `docs/security-assessment-<YYYY-MM-DD>.md` (use the date
supplied to you; do not invent one) and mirror the top-level summary into your
final chat message. Structure it exactly like a paid engagement report:

1. **Executive summary** — one paragraph a non-engineer stakeholder can read:
   overall risk posture, count of findings by severity, the single most
   important thing to fix, and whether the app is fit for an enterprise customer
   security review today.
2. **Scope & methodology** — what you assessed (commit hash / branch), the
   standards applied (OWASP Top 10 2021, API Top 10 2023, ASVS L2, Go-SCP), the
   phases run, and explicit **limitations** (e.g. no real-deployment testing,
   no fuzzing campaign, time-boxed) — honesty about what a scan did *not* cover
   is part of a professional report.
3. **Findings** — one entry per finding, ordered by severity, each with:
   - **ID / Title / Severity** (Critical / High / Medium / Low / Informational,
     with a CVSS 3.1 vector where meaningful).
   - **Category** (OWASP/CWE reference).
   - **Location** (`file:line` and/or endpoint).
   - **Description & impact** — what an attacker gains.
   - **Evidence / PoC** — the exact request/response, failing check, or code
     excerpt that proves it (secrets redacted). Mark unproven items
     "Informational — needs manual confirmation."
   - **Remediation** — specific, minimal, and Octbase-idiomatic (name the file,
     the helper to reuse, the pattern to follow).
   - **Effort / risk of fix.**
4. **Positive assurances** — invariants and controls you verified intact
   (so the reader knows what was checked and found good, not just what failed).
   Tie back to the invariant tables you confirmed.
5. **Dependency & supply-chain appendix** — govulncheck/lint output summary,
   vendored-lib versions, container image advisory status, secret-scan result.
6. **Deployment / ops checklist** — the required-for-production env vars,
   secrets, proxy/TLS settings, and monitoring, framed as a go-live gate.
7. **Prioritized remediation roadmap** — Critical/High "fix before next release,"
   Medium "next cycle," Low/Info "backlog," with rough sequencing.
8. **Verification commands** — the exact commands you ran (tests, vet, lint,
   govulncheck, sweeps, curl PoCs) so a reader can reproduce.

Severity guidance: rate by real exploitability and blast radius in the
one-stack-per-client model. A cross-member data leak in a PRIVATE project is
**Critical/High** even if it needs an authenticated account, because the whole
product promise is project-scoped confidentiality.

---

## 8. Optional remediation phase (only if the user asks)

If — and only if — the user explicitly requests fixes after reading the report,
switch modes and follow the repo's normal change discipline: small reviewable
commits, a reproducing/negative **test for every security fix**, run the
`go-security` / `js-security` sweeps and `go test ./...` + coverage floor
(`coverage` skill) + frontend guards (`frontend-guards` skill), update
`CHANGELOG.md` under `## Unreleased` (Security), and keep docs/`CLAUDE.md` in
sync per the mandatory-change-checks rule. Never lower the coverage floor or
weaken a CI guard to make a build pass. Until then, **do not change product
code** — your job is the assessment.

---

Begin with §1 (read ground truth) and §2 (inventory + threat model), invoking the
`go-security` and `js-security` skills and running their regression sweeps as the
baseline pass, then proceed phase by phase. Do not declare the assessment
complete until every phase has run and every finding carries evidence.
