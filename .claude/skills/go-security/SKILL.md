---
name: go-security
description: Security-review the octbase-api Go backend against OWASP Go-SCP (Go Secure Coding Practices). Covers the established security invariants (auth/session, crypto, SQL, file upload, XSS sanitizer, SSRF/CSRF, headers, rate limiting) and a grep-driven sweep to catch regressions. Use when reviewing a backend diff/PR for security, adding an auth/crypto/file/SCM/webhook code path, or asked to do a security check.
---

# Go security review (octbase-api)

Reference guide lives at `docs/go-webapp-scp.pdf` (OWASP Go-SCP). Review the Go
backend against its chapters. The codebase is already security-mature — the goal
of a review is **catching regressions against the invariants below**, not
re-deriving them. Cite findings by `file:line` and the Go-SCP chapter.

## Established invariants — verify these still hold

| Area | Invariant | Where |
|---|---|---|
| Crypto | AES-256-GCM + `crypto/rand`; key from `OCTBASE_SCM_ENC_KEY` (32B). **Never `math/rand` for secrets/tokens.** | `internal/shared/crypto.go` |
| JWT | HMAC method asserted in keyfunc (blocks `alg=none`); issuer checked. Secret validated ≥32B in main and **passed into** `auth.NewHandler` — no silent dev-key fallback. | `internal/auth/jwt.go`, `cmd/.../main.go` |
| Sessions | Refresh-token **rotation + reuse detection** (revokes all sessions on replay); cookies `HttpOnly`+`SameSite=Strict`+`Secure` (`OCTBASE_SECURE_COOKIES`); tokens stored as SHA-256. | `internal/auth/handler.go` |
| Passwords | bcrypt cost 12; **dummy-hash compare** on unknown user to kill timing enumeration; generic "invalid credentials". Policy is `shared.ValidatePassword` (≥12 chars + common-password blocklist) — every path that sets a password must call it. | `internal/auth/email_provider.go`, `internal/shared/password.go` |
| Password reset | Forgot-password always answers **202 with one fixed body** (unknown/disabled/deleted accounts take the same path; even a DB error still 202s) and mails **asynchronously** so timing can't enumerate accounts. Tokens: `crypto/rand` 32B, stored **SHA-256 only**, 60-min TTL, **single-use enforced inside the tx** (guarded `UPDATE … WHERE used_at IS NULL`), one outstanding token per account. Reset revokes **all** refresh tokens, re-checks account status at redemption, does **not** clear MFA. Link is built from `OCTBASE_APP_URL` (never the Host header) with the token in the **fragment**; one stable `RESET_TOKEN_INVALID` for unknown/expired/used. Extra per-IP budget (5/15 min) on top of the auth-group limiter; expired rows purged by the retention job. | `internal/auth/password_reset.go`, `internal/retention/retention.go` |
| MFA (TOTP) | TOTP secrets encrypted at rest (AES-256-GCM) with a **dedicated** `OCTBASE_MFA_ENC_KEY` (32B, deliberately separate from the SCM key); a decrypt failure is a hard error, **never** a fall-through to the recovery-code path. Recovery codes stored **SHA-256 only**, consumed one-time via guarded `UPDATE … WHERE used_at IS NULL`. Login with MFA enrolled returns a **stateless, scoped `challengeToken`** (its issuer differs from access tokens — it must never pass `JWTMiddleware`, and an access token must never pass `/auth/mfa/verify`: `MFA_CHALLENGE_INVALID`); verification has a per-account attempt cap on top of the IP limiter. Disable/regenerate require fresh proof (password or valid code) so a stolen access token can't strip MFA. | `internal/security/mfa/`, `internal/auth/handler.go` (`VerifyMFA`), `internal/auth/jwt.go` |
| Disabled accounts | Rejected on **every** authed request via DB lookup (access tokens not relied on for revocation). | `internal/shared/auth.go` (`LoadUserGlobalRole`) |
| SQL | Always parameterized (`$1`…). Dynamic `ORDER BY` only from a **whitelist** map + binary ASC/DESC. No string-built predicates. | `internal/workmanagement/task_repo.go` |
| File upload | Opaque server-generated hex storage keys (no user filename on disk) + root-escape check; content-type sniffed & **allowlisted**; **SVG rejected**; downloads `Content-Disposition: attachment` + per-response `nosniff`, only png/jpeg/gif/webp inline. | `internal/workmanagement/storage.go`, `attachment_handler.go` |
| XSS | Default-deny allowlist HTML sanitizer; server is source of truth. No `text/template` for HTML. | `internal/docs/sanitize.go`, `internal/workmanagement/sanitize.go` |
| SSRF | Two outbound HTTP surfaces: (1) OAuth token exchange to an **operator-env** `tokenURL`; (2) real SCM provider calls to the **user-supplied** `apiBaseUrl`. The user-supplied surface is guarded — `NewProvider` rejects a non-`http(s)` scheme or a literal internal-IP host (`400 SCM_URL_NOT_ALLOWED`), and all real SCM traffic uses a dialer that resolves the target and refuses loopback/RFC1918/RFC6598/link-local (incl. `169.254.169.254`)/multicast, dialing the validated IP directly (DNS-rebind + redirect safe). Any **new** request-controlled outbound host must reuse this guard. | `internal/scmintegration/ssrf.go`, `oauth.go` |
| CSRF (OAuth) | `state` is one-time server UUID, expiring, consumed once, provider-segment checked; redirect target is fixed env (no open redirect). | `internal/scmintegration/handler.go` |
| Webhooks | HMAC-SHA256 verified with constant-time `hmac.Equal`; body size-limited. | `internal/webhooks/handler.go` |
| Headers/CORS | `SecurityHeaders` sets nosniff, X-Frame-Options:DENY, Referrer-Policy, Permissions-Policy, strict CSP (`default-src 'none'`; `/docs` overrides). CORS reflects only the configured origin — **never `*` with credentials**. | `internal/shared/httpx.go` |
| Trusted proxy | `shared.RealIP` honors `X-Forwarded-For`/`X-Real-IP` **only from `OCTBASE_TRUSTED_PROXIES`**; default ignores them. Audit IPs use `shared.ClientIP`, never raw XFF. | `internal/shared/realip.go` |
| Rate limiting | Per-IP fixed window on public auth routes; depends on RealIP being correct. | `internal/shared/ratelimit.go`, `cmd/.../main.go` |
| Frontend edge (Caddy) | All three Caddy configs ship `script-src 'self'` (**no `'unsafe-inline'`** — inline `<script>` blocks stay externalized to `js/*.js`) and `connect-src 'self'` (**no `ws:`/`wss:`** — the SPAs use only same-origin fetch + SSE). `/docs` is excluded from the edge CSP (the API sets its own Swagger policy). | `octbase-frontend/caddy/Caddyfile{,.tls}`, `octbase-mobile/caddy/Caddyfile` |
| URL overrides | `?apiBase=` (both SPAs) and `?desktop=` (mobile) are honored **only in dev contexts** (`file://` or loopback host, the `DEV_CONTEXT` gate); `?desktop=` additionally allows only http(s)/file targets because it lands in `href`s. Never add a URL param that redirects traffic or links on deployed origins. | `octbase-frontend/js/config.js`, `octbase-mobile/js/core.js` |

## Regression sweep

Run from `octbase-api/`. Each line should return nothing (or only known-safe hits):

```bash
grep -rn '"math/rand"' --include='*.go' internal cmd | grep -v _test.go      # insecure RNG for tokens
grep -rn 'InsecureSkipVerify' --include='*.go' .                              # TLS verification disabled
grep -rn 'fmt.Sprintf' --include='*.go' internal cmd | grep -iE 'select|insert|update|delete|where' | grep -v _test.go  # SQL string-building (must be whitelist-only)
grep -rn 'X-Forwarded-For' --include='*.go' internal cmd | grep -v _test.go  # must go through shared.ClientIP, not raw reads
grep -rn 'os/exec\|text/template' --include='*.go' internal cmd | grep -v _test.go  # command-exec / HTML-unsafe templating
grep -rn 'image/svg' internal/workmanagement/attachment_handler.go           # SVG must stay rejected
gofmt -l .   ;   go vet ./...                                                 # hygiene
```

Frontend edge (run from the repo root):

```bash
grep -rn "unsafe-inline\|ws:" octbase-frontend/caddy octbase-mobile/caddy | grep -vE ':[0-9]+:\s*#' | grep -v style-src   # script-src/connect-src must stay tight (style-src 'unsafe-inline' is the only allowed hit)
grep -rn '^<script>' octbase-frontend/*.html octbase-mobile/*.html                              # no inline scripts — externalize instead
grep -n "URL_PARAMS.get('apiBase')" octbase-frontend/js/config.js octbase-mobile/js/core.js | grep -v DEV_CONTEXT && echo "apiBase override lost its DEV_CONTEXT gate"
```

Also confirm: new authed routes sit under the `JWTMiddleware`+`LoadUserGlobalRole`
group; new project-scoped handlers call `shared.ProjectMemberGuard`; new outbound
HTTP whose host derives from request input goes through the SCM egress guard
(`scmintegration.checkOutboundURL` + the guarded dialer in `ssrf.go`) — never a
bare `http.Get`/`http.Client` on a user-controlled URL.

**Access control (BOLA/IDOR) — the highest-risk sweep.** Membership is required
to *read* project content, not just write it: every project-scoped handler,
**including GETs**, must call `memberGuard`/`ProjectMemberGuard` before returning
data. Page/entity-scoped routes whose URL carries only the child ID (e.g.
`/pages/{id}/revisions`) must resolve the parent to its `ProjectID` and guard on
that (see `docs` `pageMemberGuard`). To catch a missing guard, list read routes
and grep their handlers — a project-scoped handler with no `memberGuard` call is
a finding:

```bash
grep -rnA6 'func (h \*Handler) \(List\|Get\|Search\)' --include='*.go' internal | grep -L 'memberGuard\|ProjectMemberGuard\|GetGlobalRole'  # eyeball read handlers that never guard
```

Regression precedent (fixed 2026-07-11): project-scoped **read** routes across
two contexts — the `docs` page routes (ListPages/GetPage/ListRevisions/
ListReferences/SearchPages) *and* the `workmanagement` releases/sprints/
categories/templates lists — leaked PRIVATE-project data to any authenticated
non-member until guards were added; the write routes had been guarded all
along. When adding a route, guard the read the same as the write.

Regression precedent (fixed 2026-07-11): a real-provider SCM repository
connection dialed its user-supplied `apiBaseUrl` through a plain `http.Client`
with no egress controls, so any authenticated project owner could reach internal
hosts or the cloud-metadata endpoint (SSRF). Fixed by the scheme/IP preflight +
guarded dialer in `internal/scmintegration/ssrf.go`. Any outbound host taken from
request input must reuse that guard.

## Deployment items (not code — flag, don't "fix")

- `OCTBASE_MFA_ENC_KEY` must be set (decodes to exactly 32 bytes; `openssl rand -base64 32`) before anyone can enroll in MFA — without it the MFA endpoints 500. Compose passes it through from `.env` (default empty). Rotating it orphans existing enrollments (secrets no longer decrypt → hard error by design).
- HSTS is sent by the Caddy front doors since v1.0.3 (`Strict-Transport-Security` in all three Caddyfiles) — verify it survives any Caddy config change and is not stripped by an outer proxy.
- `OCTBASE_APP_URL` must be set to the real frontend origin in production — password-reset and invitation emails embed it (fallback is `http://localhost:8080`, i.e. dead links; it is deliberately never derived from the Host header).
- Production `OCTBASE_DATABASE_URL` must use `sslmode=require`/`verify-full` (dev default is `disable`).
- Set `OCTBASE_TRUSTED_PROXIES` to the edge proxy when deployed behind one, else rate-limit/audit IPs degrade to the proxy IP.

## Related
- Frontend/browser-side invariants → `js-security` skill
- Test suite → `testing` skill · Coverage floor → `coverage` skill · Running a stack → `dev-stack` skill
- Backend conventions → `go-backend-reviewer` agent
