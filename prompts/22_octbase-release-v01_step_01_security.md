You are a senior application security engineer closing out security work for Octbase's v0.1 release. This step turns the recommendations from `18_octbase-security.md` into verified, enforced production behavior. Read `prompts/_release-v01-audit.md` first (from `step_00`) for any security-tagged findings.

Scope: backend Go (`octbase-api/`) and frontend (`octbase-frontend/`). No new features — only verification, enforcement, and fixes.

## Practical steps

1. **Fail-closed startup validation**
   - Find where config is loaded (likely `internal/shared` or `cmd/octbase-api/main.go`).
   - Add a startup check: if `OCTBASE_DEMO_MODE=false` (i.e. "production-like") and any of the following are at their insecure default, **refuse to start** with a clear, non-secret-leaking error:
     - `OCTBASE_JWT_SECRET` equals the dev placeholder (`dev-secret-...` / `dev-only-change-in-production` / `change-this-in-production`) or is shorter than 32 bytes.
     - `OCTBASE_SECURE_COOKIES=false`.
     - `OCTBASE_CORS_ORIGIN` is empty or `*`.
   - Add a Go test in `internal/shared` (or wherever config validation lives) that asserts the server fails to start / `New(...)` returns an error for each insecure combination, and succeeds for valid ones.

2. **Rate limiting coverage**
   - Find the existing rate limiter (used on `/api/v1/users` per the README). Identify the middleware/package.
   - Apply the same middleware (with appropriate per-route limits) to: `POST /api/v1/auth/login`, `POST /api/v1/auth/refresh`, invitation-accept endpoint, and both webhook receivers (`/api/v1/webhooks/bitbucket`, `/api/v1/webhooks/github`).
   - Add tests: hammer `/api/v1/auth/login` past the limit and assert `429`.

3. **Dependency & static analysis**
   ```bash
   cd octbase-api
   go vet ./...
   golangci-lint run ./...
   go run golang.org/x/vuln/cmd/govulncheck@latest ./...
   ```
   - Fix anything trivial (version bumps, obvious lint fixes).
   - For anything non-trivial (breaking API change in a dependency), document it in the audit report under "Deferred items" with the CVE/issue ID.

4. **Log redaction spot-check**
   - Run the API locally (`OCTBASE_DEMO_MODE=true`), perform a login and a task update via curl, and capture stdout logs.
   - Grep the captured logs for `Authorization`, `password`, `token`, `Cookie`, email addresses. Anything sensitive that appears must be redacted in the logging middleware (likely `internal/shared`).
   - Add a unit test for the log redaction helper if one doesn't exist.

5. **Data retention documentation**
   - In `docs/operations.md`, add a "Data Retention & Deletion" section answering:
     - What happens to a user's data (tasks, comments, pages) when their account is deleted vs. disabled?
     - How long are audit log entries retained, and is there a purge job (if not, say "retained indefinitely — revisit if storage becomes a concern")?
     - What PII is stored (name, email) and where.
   - This is documentation only — do not implement a deletion/purge job unless the audit flagged it as a Blocker.

6. **CORS & headers final check**
   - Confirm `OCTBASE_CORS_ORIGIN` is a single explicit origin in production config, never `*` with credentials.
   - Confirm security headers (CSP, X-Content-Type-Options, Referrer-Policy, Permissions-Policy, HSTS) are set either by the Go middleware or the Caddy/reverse-proxy config in `octbase-frontend/caddy/`. If split across both, document which layer owns which header in `docs/operations.md` so they aren't silently dropped if one layer is removed.

## Tests to add

- Startup fails with insecure prod config (table-driven test over the 3 conditions above).
- Login rate limit returns 429 after N attempts, and resets after the window.
- Webhook endpoints rate-limited.
- Log redaction unit test.

## Deliverable

Append a short section to `prompts/_release-v01-audit.md`:
- What was fixed (file + brief description).
- What was checked and found already correct.
- Any `govulncheck`/lint findings deferred, with reasoning.

Verification commands to run before finishing:
```bash
cd octbase-api && go vet ./... && golangci-lint run ./... && go test ./...
```
