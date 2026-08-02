# Security audit — 2026-07-02

Scope: full pass over `octbase-api` against OWASP Go-SCP (per the `go-security`
skill's invariant checklist and regression sweep), the uncommitted working-tree
diff on `release_v11`, the `octbase-frontend` Caddy front door, and a targeted
frontend sink scan. Auditor: automated review session.

## Result

One low-severity code finding, **fixed in this audit**. All established
security invariants verified intact. Three deployment-time items to keep on
the ops checklist (no code change). No high/critical findings.

## Finding 1 (LOW, fixed): LIKE/ILIKE metacharacters in search input

- **Where:** all five search-pattern build sites —
  `internal/workmanagement/task_repo.go` (`SearchByTitle`),
  `internal/workmanagement/planning_repo.go` (`UnifiedSearchTasks`,
  `ProjectRepo.SearchVisible`), `internal/docs/repo.go`
  (`PageRepo.SearchByTitle`, `SearchPages`).
- **Issue:** user search text was concatenated into an `ILIKE` pattern
  (`"%" + q + "%"`). The queries are fully parameterized, so this was **not**
  SQL injection. But `%`, `_` and `\` kept their pattern meaning:
  - a query ending in `\` produced an invalid pattern — PostgreSQL rejects
    "LIKE pattern must not end with escape character" — surfacing as a
    **500 INTERNAL_ERROR** for ordinary user input (robustness /
    error-handling, Go-SCP input-validation + database-security chapters);
  - `%` / `_` acted as wildcards the user never asked for (results stay
    membership-scoped, so no authorization impact — semantic only).
- **Fix:** new `shared.EscapeLike` escapes `\`, `%`, `_`; applied at all five
  sites. Tests: `internal/shared/escape_like_test.go` (unit) and
  `TestUnifiedSearch_LikeMetacharacters` in
  `internal/workmanagement/search_dashboard_test.go` (integration: literal
  `%` matches, `_` no longer wildcards, trailing `\` returns 200).

## Invariants verified (no regressions)

- **Crypto:** AES-256-GCM with `crypto/rand` nonces; 32-byte key required from
  `OCTBASE_SCM_ENC_KEY`; no `math/rand` anywhere in `internal`/`cmd`.
- **JWT:** HMAC signing method asserted in the keyfunc (blocks `alg=none`),
  issuer pinned; `main.go` refuses to start outside demo mode when
  `OCTBASE_JWT_SECRET` is <32 bytes (`os.Exit(1)`), dev fallback is
  demo-mode-only.
- **Sessions:** refresh rotation with replay/reuse detection revoking all
  sessions; tokens stored as SHA-256; cookies `HttpOnly` + `SameSite=Strict` +
  `Secure` via `OCTBASE_SECURE_COOKIES`.
- **Passwords:** bcrypt cost 12, dummy-hash compare on unknown/passwordless
  users (timing-enumeration defense), generic invalid-credentials error.
- **Disabled accounts:** rejected on every authed request by
  `shared.LoadUserGlobalRole` (DB-backed, not token-trusting).
- **SQL:** all queries parameterized; the only `fmt.Sprintf` near SQL builds a
  `$n` placeholder index, not a value; dynamic `ORDER BY` remains
  whitelist-driven. New migration `022_search_trgm_indexes` only adds pg_trgm
  GIN indexes (schema-qualified, advisory-locked) — no new query surface.
- **Uploads:** random 256-bit hex storage keys, hex-format + root-escape
  checks, content-type sniffed against an allowlist, SVG still rejected,
  attachment downloads forced `attachment` + per-response `nosniff`.
- **XSS:** both default-deny allowlist sanitizers (docs, task descriptions)
  intact; server remains source of truth (`CleanTaskDescription` runs on the
  PATCH path before validation); no `text/template`, no `os/exec`.
- **SSRF/OAuth:** outbound token exchange targets operator-env
  `OCTBASE_OAUTH_*_TOKEN_URL` defaults only, never request input; OAuth
  `state` is a one-time, expiring, consumed server UUID.
  > **Correction (2026-07-11):** this bullet overlooked a second outbound
  > surface — real SCM provider calls dial the *user-supplied* `apiBaseUrl`,
  > which at the time had no egress controls (SSRF reachable by any project
  > owner). Fixed the same day: scheme/internal-IP preflight
  > (`SCM_URL_NOT_ALLOWED`) plus a guarded dialer that blocks internal
  > addresses and DNS rebinding (`internal/scmintegration/ssrf.go`).
- **Webhooks:** HMAC-SHA256 with constant-time `hmac.Equal`;
  `http.MaxBytesReader` body limits on both receivers.
- **Headers/CORS:** API `SecurityHeaders` unchanged (nosniff, DENY framing,
  strict `default-src 'none'` CSP); CORS reflects only the single configured
  origin, never `*` with credentials; "null" origin rejected.
- **Trusted proxy / rate limiting:** `X-Forwarded-For` honored only from
  `OCTBASE_TRUSTED_PROXIES`; public auth routes rate-limited 120/min,
  usermgmt 60/min; all authed routes sit under
  `JWTMiddleware` + `LoadUserGlobalRole` in the router.
- **Working-tree diff (`release_v11`):** security-positive — optimistic
  locking extended to pages (409 `VERSION_CONFLICT` instead of silent
  overwrite), task PATCH now rejects mistyped fields with 400 instead of
  silently dropping them, frontend sends versions with edits.
- **Frontend front door (Caddy):** strict headers on both configs; the TLS
  config adds HSTS (`max-age=63072000; includeSubDomains`) and IP-restricts
  `/metrics` to RFC-1918 ranges.

## Deployment checklist items (flagged, not code)

1. **Mailpit removed from the deployable stack (remediated 2026-07-02).**
   Mailpit — whose mailbox holds all outbound mail, including password-reset
   and invitation links — previously shipped unconditionally in
   `podman-compose.yml` (the per-client deployment unit) behind a default
   `octbase:octbase` basic auth, and the dev Caddyfile proxied it at
   `/mailpit`. It now lives only in the dev overlay `podman-compose.dev.yml`
   (`podman-compose -f podman-compose.yml -f podman-compose.dev.yml up -d`);
   the base stack defaults `OCTBASE_SMTP_HOST` to empty (stdout logging).
   The production `Caddyfile.tls` never proxied `/mailpit`. Ops check for
   existing deployments: `podman ps | grep -i mailpit` must be empty and
   `https://<app-domain>/mailpit/` must 404.
2. **Database TLS:** production `OCTBASE_DATABASE_URL` must use
   `sslmode=require`/`verify-full` (dev default is `disable`).
3. **`OCTBASE_TRUSTED_PROXIES`** must name the edge proxy in any deployment
   behind one, or rate-limit/audit IPs degrade to the proxy IP.

## Hardening opportunity (remediated 2026-07-03)

- ~~The frontend CSP carries `script-src 'unsafe-inline'`~~ — remediated: the
  few fixed inline `<script>` blocks (theme boot, user-guide nav, Swagger
  init, styleguide icon grid) were externalized to `js/*.js` files, and all
  three Caddy configs (frontend dev + TLS, mobile) now ship
  `script-src 'self'` with no inline allowance. The app never used inline
  event handlers (it dispatches via `data-act` delegation), so nothing else
  depended on it. `/docs` (proxied Swagger UI) is excluded from the edge CSP
  and keeps the API's own route-specific policy.

## Follow-up review 2026-07-03 (frontend)

1. **Mobile `?desktop=` override → DOM XSS / open redirect (fixed).** The raw
   URL parameter landed in the `href` of every "Open on desktop" link;
   `esc()` does not neutralize URL schemes, so `?desktop=javascript:…`
   executed on tap and any external URL made a phishing handoff. Now honored
   only in dev contexts (`file://` or loopback host) and only for
   http(s)/file targets (`octbase-mobile/js/core.js`).
2. **`?apiBase=` honored on deployed origins (fixed).** A crafted link could
   point all API traffic — including submitted login credentials — at a
   foreign server. The edge CSP (`connect-src 'self'`) already blocked the
   fetch; the override is now additionally gated to `file://`/loopback
   contexts in both SPAs, so two independent layers must fail before it is
   exploitable. Playwright (`file://`) and localhost preview workflows are
   unaffected.
3. **`connect-src` dropped `ws:`/`wss:` (fixed).** The apps use only
   same-origin fetch + SSE (`EventSource`), so the any-host WebSocket
   allowance was an unnecessary exfiltration channel; `connect-src` is now
   `'self'` in all three Caddy configs.
4. Backend re-verified against the full invariant table (crypto, JWT,
   sessions, passwords, SQL, uploads, sanitizers, SSRF/CSRF, webhooks,
   headers/CORS, trusted proxy, rate limiting) — no regressions; grep sweep,
   `go vet`, `gofmt` clean.

## Scope note: MFA not covered

This audit predates the TOTP MFA feature (`internal/security/mfa`,
`OCTBASE_MFA_ENC_KEY`, the stateless login-challenge JWT issuer — see
`CHANGELOG.md` `## Unreleased`), which landed after 2026-07-03 and has not
been through this invariant checklist. Follow-up review should cover:
recovery-code hashing/storage, the challenge-token issuer/audience isolation
from real access/refresh tokens, the MFA encryption key handling, and the
re-auth requirement on disable/regenerate. Treat the "Invariants verified"
section above as **not** attesting to the MFA subsystem.
