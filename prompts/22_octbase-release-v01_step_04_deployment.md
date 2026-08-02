You are a senior platform engineer making Octbase's CI/CD and deployment story real instead of aspirational. Read `prompts/_release-v01-audit.md` first, especially the CI and container findings.

## Practical steps

1. **Versioning**
   - Decide a scheme: git tags `v0.1.0`, `v0.1.1`, ... matching this release series.
   - In `octbase-api/cmd/octbase-api/main.go`, add build-time version injection via `-ldflags`:
     ```go
     var version = "dev" // overridden via -ldflags "-X main.version=v0.1.0"
     ```
   - Surface `version` in `/api/v1/health` (coordinate with `step_03`).
   - Update `octbase-api/Containerfile` to accept a `VERSION` build arg and pass it through `-ldflags`:
     ```dockerfile
     ARG VERSION=dev
     RUN go build -ldflags "-X main.version=${VERSION}" -o octbase-api ./cmd/octbase-api
     ```

2. **CI: make it a real gate**
   - In `.github/workflows/ci.yml`, the `build` job's push step is a placeholder. Two options — pick based on what's actually available:
     - **No registry available**: remove the misleading placeholder, keep `build` as a build-only sanity check (`docker build` succeeds), and add a comment explaining that image publishing is deferred until a registry is configured, with the exact env vars/secrets (`REGISTRY_URL`, `REGISTRY_USER`, `REGISTRY_TOKEN`) that would need to be added to GitHub Actions secrets to enable it.
     - **Registry available** (check `.env`/`docs/operations.md` for `registry.example.com` being a real placeholder vs. real value): wire up `docker/login-action` + `docker/build-push-action`, tagging with both `${{ github.sha }}` and the git tag (`v0.1.x`) on tag pushes.
   - Add a `govulncheck` step to CI (from `step_01`) so future PRs can't silently introduce known-vulnerable dependencies:
     ```yaml
     - name: govulncheck
       working-directory: octbase-api
       run: go run golang.org/x/vuln/cmd/govulncheck@latest ./...
     ```
   - Add a job (or step) running the frontend syntax check and any frontend tests that don't require a live API:
     ```bash
     node --check octbase-frontend/js/app.js
     node --check octbase-frontend/js/i18n.js
     ```
   - Note in the PR/commit description that branch protection on `main` (require CI passing before merge) is a repo-settings change outside this codebase — document the required setting in `docs/operations.md` so a human can apply it.

3. **Production compose / deployment artifact**
   - The repo currently has `podman-compose.yml` (dev-oriented, per `.env.example`'s comments about `pgdata` dirs). Check whether it's safe to use as-is for production or needs a production variant.
   - If needed, add `podman-compose.prod.yml` (or `docker-compose.prod.yml`) that:
     - Does not mount source code as volumes (uses built images only).
     - Sets `OCTBASE_DEMO_MODE=false`, `OCTBASE_SECURE_COOKIES=true`.
     - Exposes only the reverse proxy (Caddy) port externally; API and DB stay on the internal network.
     - References the `deploy/prometheus/` stack from `step_03` as an optional overlay (`docker-compose.monitoring.yml`).
   - Update `docs/operations.md`'s "Deploy (Docker Compose)" and "Rolling back a deployment" sections to match whatever compose files actually exist in the repo — don't leave docs describing files that don't exist.

4. **Rollback verification**
   - With the versioning from step 1 in place, build two image tags locally (e.g. `v0.1.0` and `v0.1.1` with a trivial change), deploy `v0.1.1`, then follow the documented rollback steps to `v0.1.0` exactly as written in `docs/operations.md`. Fix any command in the docs that doesn't actually work (wrong compose syntax, wrong service name, etc.).
   - Confirm `migrate down 1` works against the actual migrations directory if a rollback needs to undo the latest migration (cross-check with `step_02`'s up/down verification).

5. **Container hardening check**
   - For both `octbase-api/Containerfile` and `octbase-frontend/Containerfile`:
     - Confirm multi-stage build (build stage discarded, only the binary/static assets in the final image).
     - Confirm the final stage runs as a non-root user (`USER` directive) — add one if missing (e.g. `USER 65532:65532` with a `nonroot` or distroless base, or create a dedicated user in the final stage).
     - Confirm no `.env`, `.git`, or source files end up in the final image (check `.containerignore` covers them).
   - Run:
     ```bash
     docker build -f octbase-api/Containerfile -t octbase-api:check octbase-api/
     docker run --rm octbase-api:check id
     ```
     Confirm `id` doesn't show `uid=0(root)`.

6. **Zero/low-downtime rolling restart**
   - With the prod compose stack running, open a browser SSE connection (or `curl -N` the events endpoint), then run a rolling restart of the API container only (`--no-deps` per the docs).
   - Observe and document: how long until the SSE connection drops, how long until the frontend's exponential-backoff reconnect succeeds, and whether any in-flight request during the restart returns an error to the user. This becomes the "user-visible impact" note in the go-live checklist.

## Deliverable

Append to `prompts/_release-v01-audit.md`:
- Version scheme + where it's surfaced.
- CI diff summary (what was added/changed in `ci.yml`).
- New/changed compose files and what they're for.
- Rollback test result (commands run, outcome).
- Container non-root verification output.
- Rolling-restart user-impact observation (in plain language, for the go-live checklist).

Verification:
```bash
cd octbase-api && go build -ldflags "-X main.version=v0.1.0-test" ./cmd/octbase-api
docker build -f octbase-api/Containerfile -t octbase-api:v0.1.0-test octbase-api/
```
