You are a senior full-stack engineer adding **real file uploads on tasks** to Octbase. Read `prompts/_release-v01-audit.md` first for current release status, and treat `18_octbase-security.md` as the security baseline for anything you add here (file handling is a classic attack surface — path traversal, unrestricted upload types, stored payloads served back to other users).

## Context

`octbase-api/migrations/001_initial.up.sql` already defines a `task_attachments` table (`filename`, `content_type`, `size_bytes`, `external_url`), and `internal/workmanagement` already has `AddAttachment` / `ListAttachments` / `DeleteAttachment` endpoints — but these only record metadata for a caller-supplied `externalUrl`. There is **no actual file storage**. This step adds real binary upload/download/storage on top of the existing metadata model rather than replacing it.

## Phase 1 — Analysis (no code changes)

1. Confirm the above by reading `internal/workmanagement/task_handler.go` (`AddAttachment`/`ListAttachments`/`DeleteAttachment`), `repo.go` (`TaskAttachmentRepo`), and the frontend's attachment UI in `app.js` (search for `attachments`).
2. Decide and document a storage backend appropriate for a single-client podman-compose deployment: a local filesystem volume (e.g. `/data/attachments`, mounted via `podman-compose.yml`) keyed by `attachment.id`, NOT by user-supplied filename (avoid path traversal). Object storage (S3/MinIO) is out of scope for v0.1 — note it as a future option only if the audit suggests multi-instance deployment is planned.
3. Decide file constraints and document them in `docs/operations.md`: max upload size (env var, e.g. `OCTBASE_MAX_UPLOAD_MB`, sensible default like 25MB), and an allowlist of content types/extensions (images, PDFs, common office docs, text, zip — no executables/scripts).

## Phase 2 — Backend implementation

1. **Migration** `010_attachment_storage`: add a `storage_key TEXT` column to `task_attachments` (nullable, distinct from `external_url`) plus `down` migration. Keep `external_url` working for existing link-style attachments — an attachment has either a `storage_key` (uploaded file) or `external_url` (link), not both.
2. **Upload endpoint**: extend or add `POST /api/v1/tasks/{taskId}/attachments` to accept `multipart/form-data` (in addition to the existing JSON body for external links — branch on `Content-Type`). On upload:
   - Enforce `OCTBASE_MAX_UPLOAD_MB` (reject oversized bodies early via `http.MaxBytesReader`).
   - Validate content type against the allowlist using both the declared `Content-Type` and a sniff of the actual bytes (`http.DetectContentType`) — reject mismatches.
   - Generate a random storage key (UUID-based filename), write to the configured storage directory, never trust the client-supplied filename for the on-disk path.
   - Store the original filename (for display only, always HTML-escaped on render), content type, size, and storage key in `task_attachments`.
   - Reuse the existing `memberGuard`/`RequireWriter` authorization already on this endpoint.
3. **Download endpoint**: add `GET /api/v1/tasks/{taskId}/attachments/{attachmentId}/content` — checks the same membership/role guard as the task itself (an attachment must not be downloadable by someone who can't see the task), streams the file with the stored `content_type` and a `Content-Disposition` header using the sanitized original filename. Do not serve the storage directory via a static file server.
4. **Delete**: confirm the existing `DeleteAttachment` also removes the file from disk when `storage_key` is set (not just the DB row). Handle the case where the file is already missing without erroring the whole request.
5. **Cascade cleanup**: confirm project/task deletion (`repo.go` already has `DELETE FROM task_attachments WHERE ...`) also removes the underlying files — add this cleanup at the service layer where the deletion is orchestrated.

## Phase 3 — Frontend implementation

1. In the task panel (`app.js`, where attachments are rendered), add a file input / drop zone for uploading files directly (in addition to the existing "add link" flow if present). Show upload progress and a toast on success/failure using the existing toast pattern.
2. List attachments with filename, size (human-readable), and an icon by type; uploaded files link to the new download endpoint, external links keep their existing behavior.
3. Long filenames: apply the same truncation/ellipsis pattern used elsewhere (per `step_06`).
4. i18n: add any new strings to the existing i18n system (`js/i18n.js` and locale files) — no hardcoded user-facing text.

## Constraints

- No new Go dependencies beyond the standard library unless something in the allowlist/content-sniffing is genuinely impractical without one — justify any addition.
- No new frontend dependencies/frameworks.
- Respect existing RBAC: uploading/downloading/deleting attachments uses the same role checks as other task-mutation endpoints.
- Add `octbase_attachments_total` to the storage volume's backup/restore story in `docs/operations.md` — uploaded files are user data and must be covered by the backup plan from `step_02`.

## Deliverable

Summarize in `prompts/_release-v01-audit.md` under "File uploads": what changed (migration, endpoints, storage layout, env vars), and any deferred items (e.g. virus scanning, object storage migration path).

## Verification

```bash
cd octbase-api && go vet ./... && go test -race ./internal/workmanagement/...
node --check octbase-frontend/js/app.js
cd octbase-frontend/tests && pytest -k attachment
```
Add/extend Go tests covering: successful upload+download round trip, oversized upload rejection, disallowed content type rejection, path-traversal attempt in filename, and authorization (non-member cannot download).
