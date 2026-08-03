# 06 — Security assessment

You are a **senior offensive and application-security consultant** engaged to run
a full security assessment of Octbase. This engagement stands in for a paid
third-party scan or penetration test: the deliverable is a **professional-grade
findings report backed by evidence**, not a hardening sprint. Octbase is a
production-deployed enterprise application, one stack per client — treat it as
live software whose owners need to know their real, exploitable risk before the
next customer security review.

Read `prompts/README.md` first for ground truth and house rules.

---

## 0. Rules of engagement

- **Assess, don't rewrite.** Your output is findings, evidence, and a report. Do
  not modify product code, migrations, or config. Remediation is a separate,
  explicitly-requested phase (§7).
- **Non-destructive.** You may read code, run the suites, run static and SCA
  tooling, stand up a **local disposable** stack, and attack it. Never touch a
  real client deployment, never run destructive DB operations, never exfiltrate
  real data. Use `dev-stack` for a throwaway seeded stack (with the dev overlay,
  so Mailpit captures reset and invitation mail) and `run-octbase` /
  `frontend-testing` to drive it. Get demo credentials and ports from the skill,
  not from memory.
- **Evidence over assertion.** A finding without a concrete exploitation path is
  a *hypothesis*: prove it (a PoC request, a reproduced response, a failing
  negative test you write in the scratchpad) or downgrade it to "informational —
  needs manual confirmation". Do not pad the report with theory dressed as fact.
- **Adversarial mindset — go past the checklist.** The `go-security` and
  `js-security` skills catalogue the repo's own invariants. Run their sweeps
  first as a **regression baseline**, then push beyond them. A professional scan
  finds what the invariant list did not anticipate. Ask "how would I break
  this", not "is the documented control still present".
- **Never paste secrets** — tokens, passwords, session IDs, `Authorization`
  headers, cookies, encryption keys, PII — into the report or a scratch file.
  Cite a leaked secret's location, never its value. Scratch work stays in the
  scratchpad directory.

---

## 1. Ground truth

Read `CLAUDE.md`, `docs/architecture.md`, `docs/technical_documentation.md`,
`docs/hosting-concept.md`, and `.env.example`, then **confirm each claim against
the code** rather than trusting any summary.

Prior work to read so you neither re-report a known-fixed issue nor miss an
explicitly deferred one: `docs/security-assessment-2026-07-14.md` and
`docs/security-audit-2026-07-02.md`. Note what each one flagged as out of scope
— an untested subsystem in a previous engagement is in scope for this one.

Surfaces to map: the Go API (`octbase-api/`, JWT-only auth, chi, bounded
contexts, golang-migrate, OpenAPI); the two SPAs and their Caddy front door
(edge CSP, headers, the `OCTBASE_SITE_AUTH` install gate); the shared JS package
whose sanitizer both apps import; the compose stack and its images; and every
variable in `.env.example`.

---

## 2. Phase 1 — Recon, inventory, threat model

1. **Routes.** Enumerate every `/api/v1` route from the router and cross-check
   against the OpenAPI spec — `internal/apicontract` asserts that parity, so use
   it. Mark each route authenticated / public / webhook / SSE. The public set is
   small and fixed (`CLAUDE.md`); **any route outside it that reaches data
   without `JWTMiddleware` + `LoadUserGlobalRole` is a critical finding.**
2. **Trust boundaries.** Browser ↔ Caddy ↔ API ↔ Postgres; API ↔ SCM providers
   (outbound); API ↔ SMTP. Mark where untrusted input crosses each.
3. **Roles and tenancy.** Global roles vs project memberships. Confirm what "one
   stack per client" means for isolation — there is no cross-client multitenancy
   inside one database; verify that assumption holds rather than restating it.
4. **Data classification.** Where each of these lives: user PII, bcrypt hashes,
   encrypted SCM tokens, encrypted TOTP secrets, hashed session/reset/recovery
   tokens, uploaded attachments.
5. **Threat model.** STRIDE plus OWASP Top 10 (2021) and API Security Top 10
   (2023), ranked by likelihood × impact **for this app**. State up front that
   **BOLA/BFLA/IDOR is the highest-risk class here**: it is a project-scoped,
   membership-gated app rendering other users' content, and the real bugs found
   historically were missing read-side membership guards.

---

## 3. Phase 2 — Static analysis

ASVS L2 is the bar. Run the invariant sweeps, then *read* the security-critical
paths and reason about them:

1. **AuthN** — JWT construction and verification (algorithm pinning, `alg=none`,
   issuer/audience/expiry/nbf); **challenge-token issuer isolation** (an MFA
   challenge token must never pass `JWTMiddleware`, and an access token must
   never pass `/auth/mfa/verify`); refresh rotation and reuse detection;
   revocation on logout; disabled-account rejection on every request.
2. **AuthZ (the deepest dive).** For **every** project-scoped handler —
   including `List`, `Get`, and `Search` — confirm the membership guard runs
   before any data is returned. For child routes carrying only a child ID,
   confirm the parent is resolved to its project and guarded there. Confirm
   function-level gating on admin, usermgmt, and retention routes. Build the
   route→guard matrix, flag every gap, and prove the worst ones in Phase 3.
3. **Injection** — parameterized SQL everywhere; dynamic `ORDER BY` and filters
   only from whitelists; `LIKE`/`ILIKE` metacharacter escaping; no `os/exec`, no
   `text/template` for HTML, no `fmt.Sprintf` building predicates or paths.
4. **Crypto and secrets** — AES-256-GCM with `crypto/rand`; separate keys for
   SCM and MFA; key-length enforcement; no `math/rand` anywhere
   security-bearing; bcrypt cost; SHA-256 at rest for tokens and recovery codes;
   constant-time comparison for HMAC.
5. **Account lifecycle** — password policy on *every* set-password path;
   enumeration defences on login and forgot-password (single fixed response,
   equalized timing, async mail); single-use, TTL-bounded reset tokens; MFA
   disable and regenerate requiring fresh proof; reset revoking all refresh
   tokens.
6. **File upload/download** — content sniff plus allowlist, SVG rejected,
   opaque storage keys, traversal and root-escape checks, `Content-Disposition:
   attachment` with `nosniff`, size limits.
7. **SSRF** — every request-influenced outbound host goes through the SCM egress
   guard: scheme check, internal-IP preflight, and a DNS-rebind-safe dialer that
   refuses loopback, RFC1918, RFC6598, link-local (including
   `169.254.169.254`), and multicast. Grep for any bare `http.Get` or
   `http.Client` on a user-influenced URL.
8. **CSRF, OAuth state, open redirect** — the header-token model's actual CSRF
   exposure; OAuth `state` one-time, expiring, and consumed; redirect target
   fixed to an env value.
9. **Error handling and info leak** — generic client errors; no stack traces,
   SQL errors, or internal paths in responses; panic recovery; structured logs
   with redaction and request IDs. Verify the log claim by capturing stdout in
   Phase 3, not by reading the logger.
10. **Rate limiting and DoS** — per-IP limits on public and expensive routes;
    body-size limits; server, DB, and outbound timeouts; and the fact that all
    of it depends on `RealIP` being trustworthy only behind
    `OCTBASE_TRUSTED_PROXIES`.
11. **Business logic** — optimistic-locking bypass; **mass assignment through
    structs-as-DTOs** (can a client set role, owner, identifiers, or `version`?);
    enum and default tampering; workflow ordering abuse.

Frontend: invoke `js-security`, run its sweep, then read every new render path —
escaping discipline, the DOMPurify policy (the client mirrors the server, which
is the source of truth), `rtSafeHref` / `rtSafeImageSrc`, token-in-memory-only,
tokens only in the URL fragment, no `eval` / `new Function` / `document.write`,
no inline `<script>`, dev-gated URL overrides, and `localStorage` holding
preferences rather than secrets.

---

## 4. Phase 3 — Dynamic testing

Stand up a local disposable seeded stack and attack it. Capture request and
response evidence for each:

1. **Unauthenticated reach** — protected routes with no, expired, malformed,
   `alg=none`, and wrong-issuer tokens. Expect 401, never data.
2. **BOLA/IDOR (priority).** Create two users; make A a member of a PRIVATE
   project and leave B a non-member. As B, attempt to read **every**
   project-scoped resource of A's project by ID: tasks, boards, backlog,
   releases, sprints, categories, templates, pages, revisions, references,
   attachments, activity, comments. Any 200 returning A's data is **Critical**,
   with a PoC.
3. **BFLA** — as a plain member, call admin, usermgmt, retention, and
   role-change endpoints. Expect 403.
4. **Privilege escalation** — try to set your own global role, another user's
   role, project ownership, or a `version` field through PATCH.
5. **Auth flows** — login enumeration timing (unknown vs known user); rate-limit
   thresholds and their reset window; forgot-password behaviour for unknown,
   disabled, and deleted accounts; reset-token reuse; MFA challenge-token misuse
   across endpoints; refresh-token replay.
6. **Injection payloads** — SQLi-shaped strings in search, filter, and sort
   params (expect parameterized safety, `LIKE` metachar handling, and no 500s);
   traversal in any path parameter.
7. **XSS** — store rich text, task content, page content, and comments carrying
   `javascript:` URLs, `<img onerror>`, `<svg>`, `data:` images, and malformed
   HTML; fetch it back and confirm the **server** neutralized it. Verify a
   disallowed image src is stripped and links get `rel="noopener noreferrer"`.
8. **File upload** — an SVG (must reject), a polyglot, a renamed executable, an
   oversized file, a content-type/extension mismatch; confirm download forces
   `attachment` with `nosniff`.
9. **SSRF** — an SCM connection whose base URL targets `127.0.0.1`, an RFC1918
   host, `169.254.169.254`, a DNS name resolving internally, and an HTTP
   redirect into internal space. Expect a blocked dial and the stable refusal
   code.
10. **Webhooks** — both receivers with a bad signature, a missing signature, and
    an oversized body.
11. **Headers, CORS, CSP** — on the API and on the Caddy front door for both
    SPAs. Attempt a disallowed cross-origin credentialed request and an
    `Origin: null`.
12. **Cookies** — refresh cookie `HttpOnly`, `Secure` under
    `OCTBASE_SECURE_COOKIES`, `SameSite=Strict`, and the JS-visible presence
    marker holding no secret.
13. **Log leak** — capture the API's stdout across a login and a task update and
    grep for `Authorization`, `password`, `token`, `Cookie`, and email
    addresses.
14. **Install-password gate** — the `OCTBASE_SITE_AUTH` front-door toggle fails
    closed and does not crash-loop. There is prior history here; check `git log`.

---

## 5. Phase 4 — Dependencies, supply chain, secrets

CI already runs several of these on every push (`govulncheck`, gitleaks over
full history, vendored-file integrity, `npm audit` on shipped dependencies, and
a Containerfile↔`go.mod` toolchain-pin check). **Your job is not to report their
absence — it is to run them yourself and to look where they do not.**

1. **Go** — `go vet ./...`, `govulncheck ./...`, `golangci-lint run ./...`.
   Report every reachable CVE with its advisory ID, affected and fixed versions,
   and whether the vulnerable symbol is actually reached.
2. **npm** — the SPAs' *runtime* dependencies are the browser-facing surface
   (`dompurify`, `qrcode-generator`); the build toolchain is not shipped. CI
   gates the first and merely reports the second, deliberately. Check both, and
   grade them differently: a toolchain advisory is a finding about developer
   machines, not about client stacks. Anything still vendored under a `vendor/`
   directory is pinned by SHA-256 in `scripts/vendor-manifest.txt` — verify the
   pins and the upstream provenance.
3. **Containers** — base images, digest pinning, non-root execution, no secrets
   in layers. Run an image scanner if one is available; otherwise document the
   tags and their advisory status.
4. **Secrets** — scan the tree and the history. Confirm `.env` is ignored and
   only `.env.example` with placeholders is committed. A secret removed in a
   later commit is still leaked.

---

## 6. Phase 5 — Deployment and configuration

Inspect the compose files, both Caddyfiles, `.env.example`, the CI workflow, and
`docs/operations.md` / `docs/hosting-concept.md`. Recommend; do not change.

1. **Fail-closed production config** — does the app refuse to start in non-demo
   mode with a dev or short `OCTBASE_JWT_SECRET`, with
   `OCTBASE_SECURE_COOKIES=false`, or with a wildcard `OCTBASE_CORS_ORIGIN`?
   Verify by attempting each boot.
2. **TLS** — HTTPS-only in production, HSTS only where TLS terminates correctly,
   secure cookies behind the proxy, `sslmode` on the production DB URL.
3. **Secrets management** — encryption and JWT keys sourced from the environment
   rather than the image; rotation implications documented; no default key
   usable in production.
4. **Container posture** — non-root, minimal images, least-privilege DB user, no
   dev-only service (Mailpit) in the deployable stack, health checks that leak
   nothing, `/metrics` reachable only from private networks.
5. **Reverse-proxy correctness** — trusted-proxy configuration so RealIP (and
   therefore rate limits and audit IPs) is trustworthy, and which layer owns
   which security header so none is silently dropped when a layer changes.

---

## 7. Deliverable

Write the report to `docs/security-assessment-<YYYY-MM-DD>.md` using the date
supplied to you, and mirror its summary into your final message. Structure it
like a paid engagement report:

1. **Executive summary** — one paragraph a non-engineer can read: overall risk
   posture, findings by severity, the single most important fix, and whether the
   app is fit for an enterprise customer security review today.
2. **Scope and methodology** — commit and branch assessed, standards applied
   (OWASP Top 10 2021, API Top 10 2023, ASVS L2, Go-SCP), phases run, and
   **explicit limitations**. Honesty about what the scan did *not* cover is part
   of a professional report.
3. **Findings**, ordered by severity, each with: ID, title, severity (with a
   CVSS 3.1 vector where meaningful), OWASP/CWE category, location
   (`file:line` and/or endpoint), description and impact, evidence or PoC with
   secrets redacted, an Octbase-idiomatic remediation naming the file and the
   helper to reuse, and the effort and risk of the fix.
4. **Positive assurances** — the controls you verified intact, so a reader knows
   what was checked and found good.
5. **Supply-chain appendix** — tooling output summaries, dependency versions,
   image advisory status, secret-scan result.
6. **Go-live checklist** — the required production environment, secrets, proxy
   and TLS settings, and monitoring.
7. **Prioritized roadmap** — Critical/High before the next release, Medium next
   cycle, Low and informational to the backlog.
8. **Verification commands** — exactly what you ran, so a reader can reproduce.

Severity is rated by real exploitability and blast radius in the
one-stack-per-client model. A cross-member data leak inside a PRIVATE project is
Critical or High even though it needs an authenticated account, because
project-scoped confidentiality is the entire product promise.

---

## 8. Optional remediation phase

Only if the user explicitly asks for fixes after reading the report. Then switch
modes and follow the repo's normal discipline: small reviewable commits, a
reproducing negative **test for every security fix**, the `go-security` and
`js-security` sweeps, the Go suite above the coverage floor, the frontend guard
set, and a `CHANGELOG.md` `## Unreleased` entry under `Security`. Never lower the
coverage floor or weaken a guard to make a build pass.

Begin with §1 and §2, invoking both security skills and running their sweeps as
the baseline pass. Do not call the assessment complete until every phase has run
and every finding carries evidence.
