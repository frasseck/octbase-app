# Octbase — Security Assessment (2026-07-14)

**Engagement type:** Professional-grade application & offensive security assessment
(scan / pentest replacement). Assess-only; **no product code was modified.**
**Target:** Octbase split monorepo — Go modular-monolith API, no-build static SPAs
behind Caddy, PostgreSQL, Podman-Compose (one stack per client).
**Commit / branch:** `308baec` on `release_v17`. App version `1.0.5` (running dev stack).
**Standards applied:** OWASP Top 10 (2021), OWASP API Security Top 10 (2023),
OWASP ASVS L2 (target bar), OWASP Go-SCP.
**Method:** invariant regression sweeps (`go-security`, `js-security`) → SAST code
review of every security-critical path → live DAST/pentest against a local disposable
seeded stack (API `127.0.0.1:8001`) → SCA / secret / supply-chain / infra review.

---

## 1. Executive summary

Octbase remains a **security-mature codebase** whose documented invariants —
JWT auth, session rotation, password/reset hygiene, SQL parameterization, XSS
sanitization, the SSRF egress guard, rate limiting, security headers/CORS — were
verified **intact and, where testable, confirmed live**. However, this assessment
found a **systemic class of Broken Object-Level Authorization (BOLA/IDOR) bugs**
that the previous audits did not: several write/delete handlers correctly guard the
**parent named in the URL** but then act on a **different child object taken from the
request body or a second path segment without verifying it belongs to that parent**.
**Four of these are High and were proven live** — any authenticated user who is a
writer in *any one* project can move, disclose, unlink, and delete tasks, task links,
and task relations belonging to **any other project in the installation, including
PRIVATE projects they are not a member of.** Because the entire product promise is
project-scoped confidentiality and integrity, this is the single most important thing
to fix.

Separately, a **High supply-chain finding**: the API container pins
`GOTOOLCHAIN=go1.26.4`, overriding the `go.mod` bump to `go1.26.5` that the team's
own commit `93222d7` made to fix crypto/tls advisory **GO-2026-5856** — so production
images are still built with the toolchain the team believes it patched.

**Findings by severity:** 5 High · 9 Medium · 13 Low · numerous informational /
positive assurances.

**Fit for an enterprise customer security review today?** **Not yet.** The four High
BOLA bugs (H1–H4) and the toolchain regression (H5) must be fixed and re-tested
first. None are internet-unauthenticated (all require a low-privilege authenticated
account), but in a multi-user install they defeat the project-isolation guarantee that
is the product's core security claim. Once H1–H5 and the Medium authorization gaps
(M1–M3) are remediated with regression tests, the application's posture is strong.

---

## 2. Scope & methodology

**In scope & assessed:** `octbase-api/` (all bounded contexts), `octbase-frontend/`,
`octbase-mobile/`, `octbase-shared/`, the Caddy front doors, `podman-compose*.yml`,
Containerfiles, `.env.example`, `.github/workflows/ci.yml`, migrations, seed data.

**Phases run:** (0) invariant regression sweeps; (1) recon / route inventory /
threat model; (2) SAST across AuthN, AuthZ, injection, crypto, uploads, SSRF,
webhooks, CSRF/OAuth, error handling, rate limiting, business logic, and the frontend
render/token paths; (3) live DAST — BOLA/BFLA, mass-assignment, injection, stored
XSS, uploads, SSRF, webhooks, headers/CORS, auth-flow timing, rate limits,
refresh-rotation race; (4) SCA / secrets / vendored libs / images; (5) infra & config
hardening.

**Live testing setup:** two test users were created via the real invitation flow on a
**local disposable seeded stack**; a victim ADMIN owned a PRIVATE project, a
non-member attacker operated from a separate project. **All test users and projects
created during the engagement were deleted afterward; the disabled test account was
re-enabled; the stack was returned to its original state** (health `ok`, seed intact).

**Limitations (honest disclosure):** no testing against any real client deployment;
no fuzzing campaign; MFA-persistence (M4) was confirmed by code reading, not driven
end-to-end (would require a live TOTP generator); container/image CVE scanning was
manual (no `trivy`/`grype`/`gitleaks` on the host); timing analysis is bounded by the
120/min login rate limit; the assessment is time-boxed and does not replace a
recurring program.

**Threat model highlights.** Trust boundaries: browser ↔ Caddy ↔ API ↔ Postgres;
API ↔ external SCM (outbound); API ↔ SMTP. Top risk class for this app is
**BOLA/BFLA** — it is a project-scoped, membership-gated app rendering other users'
content, and both the most recent real bug (2026-07-11 read-guard regression) and the
findings below are exactly that class. There is **no cross-client multitenancy inside
one database** (one stack per client) — confirmed; "tenant" isolation *within* a stack
is the **project**.

---

## 3. Findings

Severities reflect real exploitability and blast radius in the one-stack-per-client
model. A cross-**project** data/integrity breach in a PRIVATE project is High even
when it needs an authenticated account, because project-scoped confidentiality is the
product promise.

### Access-control root cause (H1–H4, M1–M3, M9, L8)

Guards resolve and check the **parent named in the URL** but then operate on a child
identified elsewhere (`taskId`/`linkId`/`relationId`/`externalColumnId` in the body or
a second path segment) **without confirming the child resolves back to that guarded
parent/project.** The correct pattern already exists in-repo — `DeleteAttachment`
(`a.TaskID==taskID`), `UpdateColumn` (`c.BoardID==boardID`), `UpdateComment`
(`c.TaskID==taskID`) all do this cross-check; the handlers below omit it.

---

### H1 — Cross-project task takeover via `MoveTask` (High, CONFIRMED live)
- **Category:** OWASP API1:2023 BOLA / CWE-639. **CVSS 3.1:** AV:N/AC:L/PR:L/UI:N/S:C/C:H/I:H/A:L (~8.5).
- **Location:** `internal/workmanagement/board_handler.go:492-567`; unscoped write via `TaskRepo.update` (`WHERE id=… AND version=…`, no project scope).
- **Description & impact:** the handler guards `board.ProjectID` + `RequireWriter`, then loads the task from **`req.TaskID` (request body)** and updates its column/rank (and, on a sprint board, its `sprint_id`) with **no `task.ProjectID == board.ProjectID` check.** An attacker who is a writer in any one project can move, re-sprint, and — because the handler returns the full task object — **read** any task in any project.
- **Evidence (live PoC):** attacker B (member of P2 only, `403` on a direct read of A's task) → `POST /api/v1/boards/{B's board}/move-task {"taskId":"<A's private task>","boardColumnId":"<B's column>","boardRank":0}` → **`200`** with A's full task body (`projectId` = A's PRIVATE project, title `A-victim-task-1`) returned to B.
- **Remediation:** after guarding the board, load the task and reject if `t.ProjectID != b.ProjectID`; likewise validate `boardColumnId` belongs to the board. **Effort:** small; **risk:** low.

### H2 — Cross-project task mutation via `RemoveTaskFromBoard` (High, CONFIRMED live)
- **Category:** API1:2023 BOLA / CWE-639. **CVSS ~7.6** (C:L/I:H).
- **Location:** `internal/workmanagement/board_handler.go:569-618`.
- **Description & impact:** identical defect — guards `board.ProjectID`, loads task from `req.TaskID`, clears `board_column_id`/`sprint_id`, no same-project check.
- **Evidence (live PoC):** B → `POST /api/v1/boards/{B's board}/remove-task {"taskId":"<A's private task>"}` → **`200`**, A's task returned and unlinked from its board/sprint.
- **Remediation:** same cross-check as H1.

### H3 — Cross-project deletion via `DeleteLink` (High, CONFIRMED live)
- **Category:** API1:2023 BOLA / CWE-639. **CVSS ~6.5** (I:H, scoped delete).
- **Location:** `internal/workmanagement/task_handler.go:1059-1075` → `TaskLinkRepo.Delete` = `DELETE FROM task_links WHERE id=$1` (`task_repo.go:667`).
- **Description & impact:** `taskGuard(taskId)` gates the *named* task's project, then the link is deleted by `{linkId}` with no check that the link's `task_id == taskId`. Any writer can delete any task link installation-wide.
- **Evidence (live PoC):** B → `DELETE /api/v1/tasks/{B's own task}/links/{A's link id}` → **`204`**; the link then verifiably absent from A's task (`GET …/links` no longer lists it).
- **Remediation:** load the link and reject unless `link.TaskID == taskId` (mirror `DeleteAttachment`).

### H4 — Cross-project deletion via `DeleteRelation` (High, CONFIRMED live)
- **Category:** API1:2023 BOLA / CWE-639. **CVSS ~6.5.**
- **Location:** `internal/workmanagement/task_handler.go:1244-1260` → `service.go:165-180` (`DELETE FROM task_relations WHERE id=$1`, deletes relation + inverse).
- **Evidence (live PoC):** B → `DELETE /api/v1/tasks/{B's own task}/relations/{A's relation id}` → **`204`**; relation verifiably absent from A's task afterward.
- **Remediation:** confirm the relation's source task belongs to `taskId` before deleting.

### H5 — Production images built with a Go toolchain the team already flagged vulnerable (High, CONFIRMED)
- **Category:** OWASP A06:2021 Vulnerable & Outdated Components / supply chain.
- **Location:** `octbase-api/Containerfile:5` `ENV GOTOOLCHAIN=go1.26.4` vs `octbase-api/go.mod:5` `toolchain go1.26.5` (bumped by commit `93222d7`, "bump Go toolchain to 1.26.5 (crypto/tls **GO-2026-5856**)").
- **Description & impact:** an exact `GOTOOLCHAIN=go1.26.4` overrides the `go.mod` toolchain directive (the `go 1.25.0` line is satisfied by 1.26.4), so container builds still use **1.26.4** — the version the team's own commit identifies as carrying the crypto/tls flaw. TLS is used for outbound SCM calls and inbound serving.
- **Evidence:** the `go.mod`/Containerfile mismatch and the commit message above; `govulncheck` on the source tree (which uses the higher toolchain) is clean, masking the container's older build.
- **Remediation:** set `ENV GOTOOLCHAIN=go1.26.5` (or `auto`, making `go.mod` the single source of truth) and rebuild/redeploy; add a CI/pre-push check that the Containerfile pin ≥ `go.mod` toolchain. **Effort:** trivial.

---

### M1 — Legacy admin endpoints let ADMIN act on SUPER_ADMIN / any user (Medium, CONFIRMED live)
- **Category:** API5:2023 Broken Function-Level Authorization / CWE-269.
- **Location:** `internal/admin/handler.go:83` (`UpdateUser`, enable/disable) & `:131` (`ResetPassword`, invalidates all sessions) — gated only by `RequireAdmin()` (ADMIN **or** SUPER_ADMIN) with **no target-role check**. The modern `usermgmt` path deliberately blocks this.
- **Evidence (live PoC):** ADMIN → `PATCH /api/v1/admin/users/{SUPER_ADMIN id}` `{"isActive":true}` → **`200`** (proves authorization passes on a SUPER_ADMIN target; `isActive:false` would disable the top-role account and delete its sessions). Contrast: `PATCH /api/v1/users/{superId}/disable` by the same ADMIN → **`403` "account management requires Super Admin"**.
- **Remediation:** add the same target-role guard the `usermgmt` path uses (refuse SUPER_ADMIN targets; require SUPER_ADMIN actor), or retire the legacy endpoints.

### M2 — PROJECT_VIEWER can mutate/delete categories & templates (Medium, CONFIRMED live)
- **Category:** API5:2023 BFLA / CWE-285.
- **Location:** `internal/workmanagement/project_handler.go` — `UpdateCategory:364`, `DeleteCategory:415`, `DeleteTemplate:615` call `memberGuard` but omit `RequireWriter` (their `Create*` counterparts require it).
- **Evidence (live PoC):** B added to A's project as `PROJECT_VIEWER` → `PATCH /api/v1/task-categories/{id}` → **`200`**; `DELETE /api/v1/task-templates/{id}` → **`204`**.
- **Remediation:** add `RequireWriter` to these three handlers.

### M3 — PROJECT_VIEWER can perform SCM writes (Medium, CONFIRMED at authz layer)
- **Category:** API5:2023 BFLA.
- **Location:** `internal/scmintegration/handler.go` — `CreateBranch:328`, `DeleteBranch:440`, `CreatePullRequest:463` resolve the project but gate on membership only, never `RequireWriter`.
- **Evidence (live):** a `PROJECT_VIEWER` calling `CreateBranch` passed the authorization gate (reached the handler body, failing only downstream with `REPO_NOT_FOUND` because no repo connection existed — **not** a `403`). The missing writer gate is confirmed.
- **Remediation:** add `RequireWriter`.

### M4 — MFA enroll/confirm require no re-auth; persistence survives password reset (Medium, code-CONFIRMED)
- **Category:** A07:2021 Identification & Auth Failures / account-takeover persistence.
- **Location:** `internal/security/mfa/handler.go:58` (`Enroll`), `:102` (`Confirm`) — no `reauthenticate` (unlike `Disable`/`Regenerate`); `internal/auth/password_reset.go` updates the hash and clears refresh tokens but never touches `mfa_enabled`/`mfa_credentials`.
- **Impact:** an attacker holding a transient/stolen access token for a victim *without* MFA can enroll MFA bound to the attacker's authenticator; the victim's later **password reset does not undo it**, locking the victim out with no self-service recovery (recovery codes were shown only to the attacker).
- **Remediation:** require password/re-auth before `Confirm` activates MFA, and/or clear MFA on password reset, and/or expose an admin "reset MFA." (Not exercised end-to-end — see limitations.)

### M5 — Default DB password + host-published Postgres/API ports in the deployable base compose (Medium)
- **Location:** `podman-compose.yml` — `POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:-octbase}`, `ports: ${POSTGRES_PORT:-5432}:5432` (binds all interfaces), and the API `${API_PORT:-8000}:8000`.
- **Impact:** a deployment keeping defaults exposes Postgres (`octbase:octbase`) and the API directly on the host's public interface, the latter bypassing the optional `OCTBASE_SITE_AUTH` front-door gate.
- **Remediation:** bind to `127.0.0.1` (or drop the host port mappings entirely — containers share the compose network) and require a non-default `POSTGRES_PASSWORD`.

### M6 — `OCTBASE_SECURE_COOKIES=false` accepted in non-demo mode (Medium)
- **Location:** `internal/auth/handler.go:882` — read at request time, no startup validation; `podman-compose.yml` defaults it `"false"`.
- **Impact:** a production deploy that forgets the flag issues the refresh cookie without `Secure`; the token can leak over any plaintext request to the host.
- **Remediation:** in non-demo mode refuse to start (or hard-warn) unless `OCTBASE_SECURE_COOKIES=true`, with an explicit opt-out for genuine HTTP-only intranet installs.

### M7 — Container base images use floating tags, no digest pinning (Medium, supply chain)
- **Location:** all Containerfiles (`hi/go:latest`, `ubi9/ubi-micro:latest`, `hi/nodejs:latest`, `hi/caddy`, `hi/postgresql:18`) + build-time `npx esbuild@0.24.2`.
- **Impact:** non-reproducible builds; a regressed/compromised upstream tag flows in silently.
- **Remediation:** pin `FROM` by `@sha256:` digest and refresh deliberately.

### M8 — No automated vuln / secret / SAST scanning in CI (Medium, process)
- **Location:** `.github/workflows/ci.yml` — runs lint + tests + coverage floor + frontend guards + e2e, but no `govulncheck`, secret scanner, SAST, or image scan. The tooling exists only in opt-in local git hooks (`scripts/security-heavy.sh` runs `govulncheck` at pre-push), trivially bypassed with `--no-verify`.
- **Impact:** exactly the class that would have caught H5; secrets can land via any clone lacking the hooks.
- **Remediation:** add CI jobs for `govulncheck ./...`, gitleaks/trufflehog, an image scan (`trivy`) on the built images, and optionally CodeQL/gosec; default `permissions: contents: read`.

### M9 — Cross-project deletion via `DeleteExternalColumn` (Medium, code-CONFIRMED)
- **Location:** `internal/workmanagement/board_handler.go:746-771` → `DELETE FROM board_external_columns WHERE id=$1` — `extID` not verified to belong to `boardId`. Same class as H1–H4, lower-value object (display reference).
- **Remediation:** verify `extColumn.BoardID == boardId`.

---

### Low

- **L1 — Disabled-account login timing oracle (CONFIRMED live).** `internal/auth/email_provider.go:60` returns before bcrypt for `disabled`/inactive accounts. Measured: disabled **2.6 ms** median vs **256 ms** (active, wrong password) vs **257 ms** (unknown user). A known email is thus classifiable as disabled by response time. Rate-limited to 120/min. *Fix:* run a dummy bcrypt on the disabled path; also add `status=='deleted'` to the guard.
- **L2 — TOTP code replay within the ±1 step window (code-CONFIRMED).** `internal/security/mfa/verify.go:35` uses `totp.Validate` (period 30, skew 1 ≈ 90 s acceptance) with no consumed-code record (unlike recovery codes). A phished/observed code is replayable ~90 s. *Fix:* record & reject the last-accepted step, or reduce skew.
- **L3 — Refresh rotation not atomic (hardening; race did NOT reproduce).** `internal/auth/repo.go:45-62` — `Claim` reads `rotated_at` unlocked and `Rotate` does `UPDATE … WHERE id=$1` with no `rotated_at IS NULL`/`RowsAffected` guard. **Live test: 8 concurrent refreshes with one stolen cookie → 1 success + 7 `REFRESH_TOKEN_INVALID`; single-use held and the reuse path fail-safe-revoked.** The feared parallel-session outcome did not occur (Postgres serializes the row writes). *Fix (defense-in-depth):* guarded `UPDATE … WHERE id=$1 AND rotated_at IS NULL` + `RowsAffected==0 ⇒ reuse`.
- **L4 — Enroll/Confirm route group omits `LoadUserGlobalRole` (code-CONFIRMED).** `cmd/octbase-api/main.go:413` — a mid-session-disabled user can still hit `/users/me/mfa/enroll`/`confirm` until the access token expires (≤15 min). Small blast radius. *Fix:* lightweight disabled-account check in the two handlers.
- **L5 — CSV formula injection in Jira export (code-CONFIRMED).** `internal/workmanagement/jira_csv.go:451-490` writes `t.Title` / attachment filename into cells with no `= + - @` prefix neutralization. A task titled `=HYPERLINK(…)` executes when a human opens the export in a spreadsheet. Jira re-import is unaffected (Low). *Fix:* prefix risky cells with `'`.
- **L6 — SCM error message leaks internal resolution detail (SSRF oracle, CONFIRMED live).** `internal/scmintegration/ssrf.go:92` → provider error → client. Live: an `apiBaseUrl` DNS name resolving to `::1` returned `502 … "scm egress refused: ::1 is a loopback address"`. The dial is still blocked (the guard works); only resolution metadata leaks to a project owner. *Fix:* return a fixed opaque message.
- **L7 — Upload content-type confusion: SVG stored as `image/png` (CONFIRMED live, not exploitable).** `resolveUploadType` (`attachment_handler.go`) trusts the declared `image/png` when the byte-sniff is generic (`text/plain`), and Go's `DetectContentType` sniffs `<svg…>` as `text/plain`. So a script-bearing SVG uploads as `image/png` (`201`). **Not XSS-exploitable** — it is served back as `image/png` with `X-Content-Type-Options: nosniff`, so the browser will not execute it. *Fix:* reject when a declared image type is contradicted by non-image textual content; explicitly detect XML/SVG signatures.
- **L8 — Cross-project foreign-key links not validated same-project (code-CONFIRMED).** `AddRelation`, and `releaseId`/`sprintId`/`boardColumnId` on task/sprint/board writes, are not checked to belong to the same project. Integrity only (reading the linked entity still needs membership). `CreateBoard` already validates its sprint link — inconsistent.
- **L9 — SCM connection create stores arbitrary `repositoryUrl` schemes.** `file://`, `gopher://`, and internal-IP `repositoryUrl` values are stored on create (`201`). `repositoryUrl` is display metadata (outbound uses `apiBaseUrl`, which *is* guarded), so this is cosmetic input-validation hygiene. *Fix:* validate scheme on `repositoryUrl` too.
- **L10 — Single all-powerful DB role.** API connects as the DB-owning bootstrap (superuser) role; no migrate/runtime privilege split. *Fix (optional, external DBs):* least-privilege runtime role.
- **L11 — API container runs as root.** `octbase-api/Containerfile` final stage has no `USER` (ubi-micro `Config.User` empty). Mitigated by shell-less image + rootless podman. *Fix:* add a non-root numeric `USER`.
- **L12 — `sslmode=disable` default & silent `OCTBASE_APP_URL` fallback in non-demo mode.** `main.go` DSN fallback and `password_reset.go:218` `appBaseURL()` fall back to `sslmode=disable` / `http://localhost:8080` with no non-demo guard (cleartext DB creds if pointed at an external DB; broken reset/invite links). *Fix:* warn/refuse in non-demo mode. Also: no compose healthcheck on API/frontend/mobile (only Postgres).
- **L13 — Frontend custom priority names rendered unescaped (defense-in-depth).** `meta.js:28` `priorityMeta(p).label` reaches `<option>`/`title` sinks in several views without `esc()`; safe **only** because the backend regex `[A-Z][A-Z0-9_]{0,19}` (`domain.go:512`) forbids HTML metacharacters, which `check-innerhtml.mjs` cannot see. *Fix:* wrap in `esc()` so XSS safety doesn't depend on a regex in another module.

---

## 4. Positive assurances (verified intact — many confirmed live)

**Access control (reads) — solid and consistent.** Every project-scoped **read**
(List/Get/Search) guards membership before returning data; the 2026-07-11 read-guard
regression is fully fixed (docs pages/revisions/references, releases, sprints,
categories, templates, priorities all re-verified). `shared.ProjectMemberGuard` is the
single implementation (SuperAdmin-only bypass). **Live:** a non-member attacker hit
**30+ project-scoped read routes and all write/BFLA/mass-assignment routes — every one
returned `403`/`404`** (43 controls held, 0 leaks). Search is membership-scoped in SQL;
SSE re-checks membership + disabled status under OptionalJWT; bulk task ops scope every
call by `projectID`.

**AuthN / JWT / sessions.** HMAC method asserted in the keyfunc (**`alg=none` and
forged/garbage tokens rejected live → `401`**); three distinct signed issuers isolate
access vs MFA-challenge vs MFA-enrollment tokens (a challenge token can't pass
`JWTMiddleware`; an access token can't pass `/auth/mfa/verify`). Refresh tokens stored
SHA-256, rotation with reuse-detection that revokes the whole family (**confirmed live
— fail-safe under concurrency**). Cookies **`HttpOnly; Secure; SameSite=Strict`**,
refresh path-scoped, presence-marker holds no secret (confirmed live).

**Passwords / reset.** bcrypt cost 12; **dummy-hash timing defense confirmed live**
(unknown 257 ms ≈ active-wrong-pw 256 ms — indistinguishable). `ValidatePassword`
enforced on every set-password path. Forgot-password: **single fixed `202` for
known/unknown confirmed live**, async mail, tokens SHA-256/60-min/single-use-in-tx,
one outstanding, reset revokes all sessions, link from `OCTBASE_APP_URL` in the `#`
fragment. (Residual synchronous-DB timing delta measured at **0.18 ms** — not a usable
oracle.)

**MFA (structure).** TOTP secrets AES-256-GCM at rest under the dedicated
`OCTBASE_MFA_ENC_KEY`; decrypt failure is a hard error (no recovery-path fall-through);
recovery codes SHA-256, one-time via guarded `UPDATE`; per-account verify cap;
`Disable`/`Regenerate` require fresh proof. (Gaps: M4, L2, L4.)

**Injection / crypto.** All queries parameterized; every `fmt.Sprintf` near SQL builds
only a `$n` index; dynamic `ORDER BY` whitelist-only; `LIKE` metachars escaped
(`EscapeLike`) at all five search sites. **Live: SQLi/`LIKE`/traversal payloads in
search returned `200 []` with no `500`.** AES-256-GCM + `crypto/rand`, dedicated
SCM/MFA keys length-enforced, no `math/rand`, `hmac.Equal` for webhooks.

**SSRF egress guard — confirmed live.** `apiBaseUrl` to literal loopback / `10.0.0.0/8`
/ `169.254.169.254` → **`400 SCM_URL_NOT_ALLOWED`** at preflight; a DNS name resolving
to `::1` → blocked at dial time by the guarded dialer (DNS-rebind + redirect safe).
Cloud-metadata endpoint reachable **only** as a blocked request.

**Stored XSS — server sanitizer is source of truth (confirmed live).** A task
description containing `<img onerror>`/`<script>`/`javascript:` came back **neutralized**
(`<img>`/`<a>` stripped of dangerous attrs, script removed). Page AsciiDoc renders to a
**HTML-escaped** `renderedHtml` (the field the SPA displays) — the raw `content` source
is never injected. Frontend: `check-innerhtml.mjs`, shared-drift, and asset-stamp
guards clean; DOMPurify policy + `rtSafeHref`/`rtSafeImageSrc` intact; access token
memory-only, never in `localStorage`; tokens only in the SSE `?token=` and the reset/
invite `#` fragment; no `eval`/`new Function`/`document.write`/inline `<script>`.

**File upload/download — confirmed live.** SVG rejected (`415`); a 30 MiB upload
rejected; opaque random storage keys; download forces `X-Content-Type-Options: nosniff`
(+ `attachment` for non-images). (Type-confusion caveat: L7.)

**Webhooks / headers / CORS — confirmed live.** Missing/bad HMAC → `403` (fail-closed);
oversized body severed by `MaxBytesReader`. API sends nosniff, `X-Frame-Options: DENY`,
strict CSP (`default-src 'none'`), Referrer-Policy, Permissions-Policy; **CORS never
reflects `evil` or `Origin: null`, preflight from a foreign origin → `403`.** Caddy
front door: `script-src 'self'` (no inline), `connect-src 'self'`, HSTS, nosniff,
`X-Frame-Options: DENY`.

**Rate limiting — confirmed live.** Login: exactly **120 × `401` then `429` with
`Retry-After`** (first `429` at attempt #121) per IP/min. usermgmt 60/min;
forgot-password + MFA-verify have extra per-key budgets.

**Fail-closed config (verified good).** JWT secret `<32 B` → `os.Exit(1)` in non-demo
(the `.env.example` placeholder itself fails this); CORS can never be wildcard-with-
credentials; empty trusted-proxy default ignores spoofable `X-Forwarded-For`.

---

## 5. Dependency & supply-chain appendix

- **Go:** `go vet ./...` clean; **`govulncheck ./...` → 0 reachable vulnerabilities.**
  One unreachable module advisory: **GO-2026-5932** (`golang.org/x/crypto@v0.53.0`
  `openpgp`, unmaintained) — code doesn't call it (Informational). **Caveat H5:** the
  *container* builds with `go1.26.4`, so its binary is not covered by this
  source-tree scan.
- **Vendored JS:** **DOMPurify 3.4.11** (`octbase-shared/purify.js`) — current, past all
  known mXSS CVEs (<3.1.3 / <3.2.4); **qrcode-generator 1.4.4** — latest, no known CVEs.
  No tampering (no network sinks); byte-identical across both SPAs (drift guard clean).
  *Recommend:* record upstream SHA-256 of the two vendored files for machine-checkable
  integrity.
- **Container images:** all floating tags (M7); API runtime `ubi9/ubi-micro` runs as
  root (L11); Caddy/Postgres run non-root; multi-stage builds carry no toolchain/shell;
  no secrets baked into layers; Mailpit absent from the deployable base stack (dev
  overlay only). No scanner on host — run `trivy image` where available.
- **Secret scan:** **clean** — no committed secrets in the tree or across 365 commits
  of history; `.env` gitignored (only `.env.example` placeholders committed); demo/seed
  credentials are bcrypt hashes gated behind `OCTBASE_DEMO_MODE=true`; the dev JWT
  fallback is demo-mode-only with a hard `os.Exit(1)` in production.

---

## 6. Deployment / go-live gate (config checklist)

Required before a production/customer deployment:
1. **Rebuild images with `GOTOOLCHAIN=go1.26.5`** (H5) and confirm the running binary's
   toolchain.
2. `OCTBASE_JWT_SECRET` ≥ 32 B (enforced); `OCTBASE_SECURE_COOKIES=true` (M6);
   `OCTBASE_MFA_ENC_KEY` / `OCTBASE_SCM_ENC_KEY` set to 32 B from env, not image.
3. `OCTBASE_CORS_ORIGIN` = real frontend origin; `OCTBASE_APP_URL` = real origin
   (L12); `OCTBASE_TRUSTED_PROXIES` = the edge proxy (else rate-limit/audit IPs degrade).
4. `OCTBASE_DATABASE_URL` `sslmode=require`/`verify-full`; non-default
   `POSTGRES_PASSWORD`; **do not host-publish Postgres/API ports** (M5).
5. TLS terminated at Caddy with HSTS; verify the strict CSP survives any Caddy change.
6. Confirm Mailpit is absent (`podman ps | grep -i mailpit` empty).

---

## 7. Prioritized remediation roadmap

**Fix before next release (Critical/High):**
1. **H1–H4** — add the parent/child same-project cross-check to `MoveTask`,
   `RemoveTaskFromBoard`, `DeleteLink`, `DeleteRelation` (and, same pattern, M9
   `DeleteExternalColumn`). One shared fix pattern; a **reproducing negative test per
   handler** (a non-member/other-project actor must get `403`/`404`).
2. **H5** — bump the container `GOTOOLCHAIN` and rebuild; add the CI toolchain check.

**Next cycle (Medium):**
3. **M1** (legacy admin target-role guard), **M2/M3** (add `RequireWriter`), **M4**
   (MFA re-auth / clear on reset), **M6** (secure-cookie startup guard), **M5**
   (loopback-bind ports / non-default DB password), **M7** (digest-pin images), **M8**
   (add `govulncheck`/secret/image scanning to CI).

**Backlog (Low/Info):** L1–L13 as defense-in-depth and hygiene; wire the local
security hooks into CI so they can't be `--no-verify`'d away.

---

## 8. Verification commands (reproducible)

```bash
# Regression sweeps (all clean)
#   go-security: math/rand, InsecureSkipVerify, SQL fmt.Sprintf, XFF, os/exec,
#                text/template, SVG-reject, gofmt/go vet
#   js-security: eval/new Function/document.write, storage.setItem, _blank/noopener,
#                token-in-URL, shared innerHTML, inline <script>, Caddy CSP, apiBase gate
cd octbase-api && go vet ./... && gofmt -l .
go run golang.org/x/vuln/cmd/govulncheck@latest ./...     # 0 reachable; GO-2026-5932 unreachable
node scripts/check-innerhtml.mjs
bash scripts/check-shared-sync.sh
# Dynamic PoCs (scratchpad harnesses, non-destructive; test data deleted after):
#   BOLA/BFLA sweep (43 controls held) · H1-H4 cross-project PoC (200/200/204/204)
#   M1 legacy-admin (200 on SUPER target) · M2/M3 viewer-write (200/204)
#   SSRF guard (400 SCM_URL_NOT_ALLOWED; ::1 dial-blocked) · rate limit (120→429)
#   login timing (disabled 2.6ms vs bcrypt 256ms) · stored XSS (neutralized)
#   alg=none/garbage (401) · CORS (evil/null not reflected) · upload (SVG 415, 30MB reject)
#   refresh-rotation race (single-use held under 8-way concurrency)
```

---

*Assessment performed against a local disposable seeded stack only; no real client
deployment was touched. All test users/projects created during the engagement were
deleted and the stack returned to its original state (health `ok`, seed intact,
migration version 30). No secrets are reproduced in this report.*

---

## 9. Remediation applied (2026-07-14)

A remediation pass followed this assessment (see `CHANGELOG.md` `## Unreleased`
`### Security`). Status:

- **Fixed with regression tests:** H1–H4 (cross-project move/remove/delete —
  same-project child checks), M9 (external-column scoped delete), M2/M3 (writer
  role on category/template/SCM writes), M1 (legacy admin SUPER_ADMIN target
  guard), M4 (enroll re-auth + disabled-account check), L2 (TOTP replay,
  migration `031`), L1 (disabled-account login timing), L3 (atomic refresh
  rotation), L4 (enroll/confirm disabled check), L5 (CSV formula injection), L6
  (SSRF error-message oracle), L7 (upload content-type confusion), L8 (move-task
  column scoping), L9 (SCM `repositoryUrl` scheme).
  New tests: `internal/workmanagement/security_fixes_test.go`,
  `internal/security/mfa/security_fixes_test.go`,
  `internal/workmanagement/jira_csv_sanitize_test.go`. Full suite green;
  coverage 74.5% (floor 73.0%).
- **Fixed in config/infra:** H5 (container toolchain → `go1.26.5`), M6
  (`OCTBASE_SECURE_COOKIES`/`OCTBASE_APP_URL` fail-closed startup), M5 (loopback
  port binding + required `POSTGRES_PASSWORD`), M8 (CI `govulncheck` +
  toolchain-pin check + gitleaks, least-privilege `permissions`), L12
  (`sslmode=disable` startup warning).
- **Deferred:** none remaining. The four items held back on 2026-07-14 (M7, L10,
  L11, L13) were all completed on 2026-07-16 — see §9.1.

### 9.1 Deferred-item follow-up (2026-07-16)

The deferred items are being closed out one at a time (see `CHANGELOG.md`
`## Unreleased` `### Security`).

- **M7 — Fixed.** All five base images (`hi/go`, `ubi9/ubi-micro`, `hi/nodejs`,
  `hi/caddy`, `hi/postgresql:18`) are digest-pinned in the three Containerfiles
  and `podman-compose.yml`; each digest was resolved from the registry (not
  guessed) and all three images were rebuilt green from the pins. The H5
  toolchain-pin check still passes. Refresh procedure documented in
  `docs/operations.md`. Residual, accepted and documented: `esbuild@0.24.2` is
  version-pinned but not integrity-pinned (npm versions are immutable), and the
  dev-only Mailpit image stays on a floating tag as it never ships to a client.
- **L10 — Fixed (opt-in).** Migrations can now run as a dedicated owner role
  (`OCTBASE_MIGRATE_DATABASE_URL`) while traffic is served by a restricted
  DML-only role (`OCTBASE_DATABASE_URL`); `scripts/db-least-privilege.sql`
  provisions it. Verified on a disposable Postgres: with the split configured the
  app logs in, reads and writes normally and `/health` is green at migration
  version 31, while the runtime role is refused DDL (`CREATE TABLE` →
  `ERROR: permission denied for schema public`, `rolsuper = f`); with only the
  legacy single URL set, startup and behaviour are unchanged. The single-role
  default is retained deliberately for the bundled per-stack Postgres — this
  finding's real target is external/managed (and especially shared) databases.
- **L11 — Fixed.** The API image runs as non-root UID 10001 (group 0). The
  blocker named in this report — a shell-less `ubi-micro` with a root-owned
  `/data` — is solved by building the data dir in the builder stage and copying
  it in with `--chown=10001:0`, so no `RUN`/`useradd` is needed in the runtime
  stage. Verified on a freshly built disposable stack: `podman inspect` and
  `podman top` both report UID 10001, `/health` green, the CA bundle /
  `migrations` / `api` / `web` all still readable, and an attachment upload +
  download round-trips byte-identically.
  **Also fixed the latent gap this finding pointed at:** attachments had no
  persistent volume, so uploads lived in the container's writable layer and were
  lost on every recreate. `podman-compose.yml` now mounts
  `${ATTACHMENTS_DIR:-./attachments}` at `/data/attachments` with `:U`; a file
  uploaded before a forced recreate still downloads identically after it.
- **L13 — Fixed.** Every `priorityMeta(p).label` sink now passes through `esc()`
  (create-task modal, backlog/task filter, bulk-edit select, task-panel select,
  task-settings badges, and `priorityDot`'s `title=` + its `t()`-built
  screen-reader label). The option **values** were carrying the raw name too and
  are escaped as well. Rebased onto the in-flight `js/` refactor rather than
  fighting it; the mobile SPA needed no change (its sinks already use `esc()` or
  the auto-escaping `` html`` `` tag). Regression-tested by
  `test_sanitizer.py::TestCustomPriorityEscaping`, which stubs `/task-priorities`
  with `<img src=x onerror=…>`: with the escaping the name renders as inert
  literal text (0 `<img>` elements created, `onerror` never fires); reverting the
  escaping makes that test fail with the payload **executing**, which confirms
  the guard is real rather than vacuous. All five frontend guards pass and assets
  were re-stamped. Still defense in depth: `ValidPriorityName` remains the
  primary control.
