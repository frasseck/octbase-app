# Octbase — deferred security remediation (from the 2026-07-14 assessment)

> **STATUS: DONE (verified in code 2026-07-27).** All four items are landed:
> M7 (every `FROM` digest-pinned in all three Containerfiles + the postgres
> image in `podman-compose.yml`), L10 (`OCTBASE_MIGRATE_DATABASE_URL` split),
> L11 (non-root `USER 10001` + `/data/attachments` skeleton, volume `:U`),
> L13 (every `priorityMeta().label` sink `esc()`-wrapped).
> `docs/security-assessment-2026-07-14.md` §9 records them as fixed. This
> prompt is historical — do not re-execute it.

You are a senior application-security + platform engineer. This prompt finishes
the **four findings deliberately deferred** by the 2026-07-14 security
remediation pass (see `docs/security-assessment-2026-07-14.md` §9 and
`CHANGELOG.md` `## Unreleased` `### Security`). Everything else from that
assessment (H1–H5, M1–M6, M8, M9, L1–L9, L12) is already fixed and tested; do
**not** re-do those. These four were held back because each needs a coordinated
infra/ops change rather than a safe drop-in — the risk was breaking builds,
uploads, or in-flight frontend work. Do them properly now, one small reviewable
change at a time, each verified before moving on.

Follow the repo's normal change discipline (`CLAUDE.md`): a test or a concrete
verification for anything with runtime behavior, run `go test ./...` + the
coverage floor (`coverage` skill) and the frontend guards (`frontend-guards`
skill) as applicable, keep `CHANGELOG.md` (`## Unreleased` `### Security`) and
docs in sync (mandatory-change-checks), and never weaken a CI guard or lower the
coverage floor. Do not touch a real client deployment.

---

## M7 (Medium) — Pin container base images by digest

**Problem.** Every `FROM` in the Containerfiles uses a floating tag
(`registry.access.redhat.com/hi/go:latest`, `ubi9/ubi-micro:latest`,
`hi/nodejs:latest`, `hi/caddy`, `hi/postgresql:18`) and the frontend build does
`npx --yes esbuild@0.24.2` (version-pinned, not integrity-pinned). Builds are not
reproducible; a regressed/compromised upstream tag flows in silently.

**Files.** `octbase-api/Containerfile`, `octbase-frontend/Containerfile`,
`octbase-mobile/Containerfile`, and the `hi/postgresql:18` reference in
`podman-compose.yml`.

**Do.**
1. Resolve the current digest for each base image (`podman image inspect
   <image> --format '{{index .RepoDigests 0}}'` on the images already pulled, or
   `skopeo inspect docker://<image>` where available) — do **not** guess digests.
2. Change each `FROM` to `image@sha256:<digest>` (keep the human tag in a
   trailing comment for readability). Keep the two-stage builder/runtime split.
3. Document the refresh procedure (how to re-pin deliberately) in
   `docs/technical_documentation.md` or `docs/operations.md`.
4. Consider adding a periodic/manual "bump base images" checklist rather than
   automating silent updates.

**Acceptance.** All three images still build (`docker/podman build`); every
`FROM` is digest-pinned; the H5 CI toolchain-pin check still passes; a short doc
note explains how to refresh the pins.

**If truly blocked** (no network/tooling to resolve a digest in this
environment): stop and report exactly which digests you could not resolve rather
than committing a placeholder — a wrong digest breaks the build.

---

## L10 (Low) — Least-privilege runtime DB role

**Problem.** The API connects as the database-owning bootstrap Postgres role
(a superuser in the stock image) and also runs migrations with it — no
separation between migrate-time DDL and runtime DML, so SQL injection or app
compromise yields full DB-server privileges.

**Files.** `podman-compose.yml` (`OCTBASE_DATABASE_URL`, `POSTGRES_USER`),
`.env.example`, `docs/hosting-concept.md` / `docs/operations.md`, and the
migration bootstrap in `octbase-api/cmd/octbase-api/main.go` (migrations run on
startup via golang-migrate).

**Do (design first, it's a topology decision).** Recommended shape: keep
migrations running as the owner/DDL role, but have the *serving* code connect as
a restricted role with only `SELECT/INSERT/UPDATE/DELETE` on the app tables (no
DDL, no superuser). Options:
- A second env var (e.g. `OCTBASE_DATABASE_URL` for runtime + a separate
  `OCTBASE_MIGRATE_DATABASE_URL` for the owner) so the split is opt-in and
  backward-compatible (fall back to one URL when the migrate URL is unset).
- A bootstrap SQL/migration that `CREATE ROLE octbase_app` + `GRANT`s on the
  app schema, run once by the owner.
This is primarily for external/managed databases; the bundled single-container
Postgres can keep one role. **Do not** break the existing single-URL default.

**Acceptance.** With the split configured, the app serves normally as the
restricted role and migrations still apply as the owner; with only the legacy
single URL set, behavior is unchanged. Documented in the hosting/ops docs and
`.env.example`. A test or a documented manual verification that the runtime role
cannot run DDL.

---

## L11 (Low) — Run the API container as non-root

**Problem.** `octbase-api/Containerfile`'s final stage (`ubi9/ubi-micro`) has no
`USER`, so the API runs as root in-container. The blocker: the image is
shell-less (no `RUN`/`useradd`), the attachment storage default is
`/data/attachments` with `/` root-owned, and there is **no attachments volume**
in `podman-compose.yml`, so simply adding `USER 1001` breaks uploads (the
non-root user can't create `/data/attachments`).

**Files.** `octbase-api/Containerfile`, `podman-compose.yml` (add an attachments
volume), `.env.example` (`OCTBASE_ATTACHMENTS_DIR`),
`octbase-api/cmd/octbase-api/main.go` (attachments dir default at ~L295).

**Do.**
1. Pick a numeric non-root UID (e.g. 10001). In the runtime stage, create the
   data dir with correct ownership **without a shell** — options: `COPY
   --chown=10001:0` an empty `data/` dir into the image, or `WORKDIR` +
   ubi-micro's supported layer tricks; verify the chosen approach actually
   yields a writable `/data/attachments` for UID 10001.
2. Add `USER 10001` to the final stage.
3. Add a named volume (or bind mount) for the attachments dir in
   `podman-compose.yml`, owned/writable by the runtime UID (mirror the `:U`
   pattern used for `pgdata`), and confirm `OCTBASE_ATTACHMENTS_DIR` points at
   it. Uploads currently have **no** persistent volume — this also fixes that
   latent data-loss-on-recreate gap; call that out.
4. Verify the CA bundle path and OpenAPI/migrations/web assets are still readable
   by the non-root user.

**Acceptance.** Container starts as non-root (`podman inspect` shows the UID);
`/health` is green; an end-to-end **attachment upload + download** succeeds
against a freshly-built image (use the `run-octbase` skill); attachments survive
a container recreate. Update the assessment's L11 note and the CHANGELOG.

---

## L13 (Low) — Escape custom-priority names on the frontend

**Problem.** `priorityMeta(p).label` returns the raw admin-defined priority
string and reaches several `innerHTML` sinks **without `esc()`**
(`octbase-frontend/js/views-crud.js` `<option>`/badges,
`js/views-shell.js` filter/bulk selects, `js/views-task.js` panel select,
`js/framework.js` `priorityDot` `title=`/`t('accessibility.priorityLabel',…)`).
It is **not exploitable today** — the backend `ValidPriorityName`
(`internal/workmanagement/domain.go` ~L512) restricts names to
`[A-Z][A-Z0-9_]{0,19}`, so no HTML metacharacters are possible — but the XSS
safety silently depends on a regex in another module that `check-innerhtml.mjs`
cannot see. Harden it so a future backend relaxation can't become stored XSS.

**Coordinate with in-flight frontend work.** The `octbase-frontend/js/` tree was
being actively refactored (IIFE file-scope / explicit exports, "view registry"
steps). **Check `git log`/status first** and rebase your edits onto the current
files; do not fight or revert that refactor.

**Do.** Wrap each of those `priorityMeta(p).label` interpolations in `esc()`
(and, where it lands in an attribute like `title=`, keep `esc()` — the codebase
already does this for the sibling `statusBadge`/custom-status paths, mirror
them). Add a brief note that this is defense-in-depth over the server regex.

**Acceptance.** `frontend-guards` (syntax, innerHTML guard, shared-sync, asset
cache-busting) all pass; assets re-stamped if any JS content changed; the
priority selects/badges/dots still render correctly (screenshot via the
`frontend-testing`/`run-octbase` skills). No behavior change.

---

## Wrap-up

- One small commit per finding (or a tight logical group), each with its
  test/verification green before the next.
- `CHANGELOG.md` `## Unreleased` `### Security` gets an entry per change; move the
  corresponding item in `docs/security-assessment-2026-07-14.md` §9 from
  "Deferred" to "Fixed."
- Run `go test ./...` + coverage floor and the frontend guards at the end; report
  anything that genuinely cannot be completed in this environment (e.g. a digest
  that could not be resolved) rather than shipping a guess.
