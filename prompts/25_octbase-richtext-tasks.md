You are a senior full-stack engineer adding a **simple rich-text editor with inline file uploads to tasks** in Octbase. Treat `18_octbase-security.md` as the security baseline: this feature touches the two highest-risk surfaces in the app at once — stored HTML (XSS) and user-uploaded binary files (path traversal, unrestricted upload types, stored payloads served back to other users). Sanitization on write **and** read, and strict file handling, are non-negotiable.

This prompt supersedes the never-landed release-v01 steps 9–11 (`22_octbase-release-v01_step_09_file_uploads.md`, `_step_10_richtext_editor.md`, `_step_11_task_preview_overlay.md`); reuse their reasoning where useful, but implement against the **current** codebase, which has none of that work.

## Goal

A user editing a task can:

1. Write the description in a **simple rich-text editor** (bold, italic, lists, headings, links, code) instead of a plain `<textarea>`.
2. **Upload files directly from the editor** (toolbar button + drag-and-drop / paste), with no separate "add attachment" detour.
3. See every uploaded file **listed in a sidebar** alongside the task.
4. Open a **task preview** that renders the formatted text together with its attachments, and **displays attached images (e.g. screenshots) inline** rather than as plain download links.

## Current state (verified — read before changing)

- **Description is plain text.** `task.description` is a plain string, validated in `internal/workmanagement/domain.go` (`ValidateTaskInput`, ≤ 50 000 chars) and rendered through `esc()` in `octbase-frontend/js/app.js`. There is **no** rich-text editor, no stored HTML, and no HTML sanitizer anywhere.
- **Attachments are metadata only — there is no binary storage.** `migrations/001_initial.up.sql` defines `task_attachments(id, task_id, filename, content_type, size_bytes, external_url, created_at)`. `task_handler.go` `AddAttachment` / `ListAttachments` / `DeleteAttachment` only persist a row for a caller-supplied `external_url` via `TaskAttachmentRepo` (`repo.go`). No file is ever written to or read from disk; there is **no `storage_key`, upload, or download endpoint.**
- **Frontend** `api.attachments` (`app.js` ~line 240) exposes only `list/add/del`. The task panel (`#task-panel` / `#task-panel-content`, render ~line 2735+) loads attachments (~line 2744) and has a tabbed layout including an **attachments tab** (~line 2760). There is **no preview overlay** (step 11 never landed).
- **Stack & deps.** Backend is Go standard library + `github.com/go-chi/chi/v5` only — no `bluemonday`, no `google/uuid`. Frontend is vanilla JS (`app.js`), with `esc()`, an existing toast pattern, and i18n via `js/i18n.js` + `locales/`. Latest migration is `012_board_config`; there is **no `_release-v01-audit.md`**.

## Phase 1 — Analysis (no code changes)

1. Trace `task.description` end to end: validation (`domain.go`), persistence (`service.go` / `repo.go` create/update), API serialization (`domain.go` json tags), and **every** render site in `app.js` (task panel, board cards, task list rows, search/command-palette results, CSV export in `jira_csv.go`). List them — each must move from "escape plain text" to "render sanitized HTML" consistently.
2. Trace the attachment path: `AddAttachment`/`ListAttachments`/`DeleteAttachment` (`task_handler.go`), `TaskAttachmentRepo` (`repo.go`), the cascade deletes on task/project deletion (`service.go`), and `CopyTask`'s attachment copy (`service.go` ~line 306–339) — uploaded files must be handled correctly by copy and delete, not just the DB rows.
3. Decide and document, in `docs/operations.md`:
   - **Storage backend** appropriate for the single-client podman-compose deployment: a local filesystem volume (e.g. `/data/attachments`, mounted via `podman-compose.yml`), keyed by a random `attachment.id` / storage key — **never** by the user-supplied filename. Object storage (S3/MinIO) is out of scope; note it only as a future multi-instance option.
   - **File constraints**: max upload size env var (e.g. `OCTBASE_MAX_UPLOAD_MB`, default 25) and a content-type/extension **allowlist** (images, PDF, common office docs, text, zip — **no executables/scripts**).
4. Decide the **description storage format**: a constrained **HTML subset** (recommended — round-trips simply with `contenteditable`, no Markdown parser dependency) vs. Markdown. Pick one and apply it consistently across backend and frontend; justify briefly in the deliverable.
5. Define the **HTML allowlist** precisely: block (`p`, `h3`, `h4`, `ul`, `ol`, `li`, `blockquote`, `pre`, `code`), inline (`strong`/`b`, `em`/`i`, `code`, `a` with `href` restricted to `http(s)`/relative). Disallow `style`, `class`, `on*`, `<script>`, `<iframe>`. Decide how inline images are represented (see Phase 4): images come from **attachments served by our authenticated endpoint**, never arbitrary external `<img src>` (avoids SSRF / tracking-pixel risk).

## Phase 2 — Backend: file storage

1. **Migration `013_attachment_storage`** (with `up` + `down`): add nullable `storage_key TEXT` to `task_attachments`, distinct from `external_url`. An attachment has **either** a `storage_key` (uploaded file) **or** an `external_url` (link), never both. Keep existing link-style rows working.
2. **Upload endpoint** — extend/replace `POST /api/v1/tasks/{taskId}/attachments` to also accept `multipart/form-data` (branch on `Content-Type`; keep the JSON external-link path):
   - Enforce the size limit early with `http.MaxBytesReader`.
   - Validate content type against the allowlist using **both** the declared `Content-Type` **and** a sniff of the actual bytes (`http.DetectContentType`); reject mismatches.
   - Generate a random storage key (do not trust the client filename for the on-disk path); write under the configured storage dir.
   - Persist original filename (display only, always escaped on render), content type, size, and storage key.
   - Reuse the existing membership/role guard already on this endpoint (`RequireWriter`-equivalent).
3. **Download endpoint** — `GET /api/v1/tasks/{taskId}/attachments/{attachmentId}/content`: enforce the **same** task-visibility guard (an attachment must not be reachable by anyone who can't see the task), stream with the stored `content_type` and a `Content-Disposition` using the sanitized filename. Do **not** expose the storage directory via a static file server. This endpoint also backs inline image rendering, so support inline display (`Content-Disposition: inline` for image types is acceptable) without weakening auth.
4. **Delete** — `DeleteAttachment` must remove the file from disk when `storage_key` is set, tolerating an already-missing file. **Cascade**: task/project deletion and `CopyTask` must handle underlying files (delete removes them; copy duplicates the bytes or shares safely — pick and document) — not just DB rows.

## Phase 3 — Backend: description sanitization

1. Sanitize `description` on **every** create/update (`service.go` / `domain.go` validation path). Server is the source of truth — never rely on the client. Prefer a hand-rolled allowlist over `golang.org/x/net/html` (check `go.mod`; it's likely already indirect) given the small, constrained allowlist; a vetted library (`bluemonday`) is acceptable only if hand-rolling proves meaningfully riskier — justify the choice.
2. Add unit tests feeding XSS payloads (`<script>`, `onerror=`, `javascript:` hrefs, malformed/nested tags) through create/update and asserting the stored/returned HTML is clean.
3. Ensure the CSV import/export path (`jira_csv.go`) sanitizes or strips HTML the same way — imported content is attacker-controllable.

## Phase 4 — Frontend: editor, sidebar, preview

1. **Rich-text editor.** Replace the description `<textarea>` in the task panel with a lightweight `contenteditable` editor + toolbar (Bold, Italic, Bullet list, Numbered list, Heading, Link, Code), matching the Phase 1 allowlist. Keep the existing dirty-state / save / unsaved-changes UX working with the new content model. On save, run client-side sanitization mirroring the server allowlist (via `DOMParser`, no heavy dependency) as defense-in-depth and immediate feedback.
2. **Inline upload from the editor.** A toolbar "attach" button **plus** drag-and-drop onto the editor **plus** paste (e.g. pasted screenshots) all upload via the multipart endpoint. Show progress and a success/failure toast (existing pattern). On a successful **image** upload, offer to insert it inline in the description (referencing the authenticated download endpoint, per the Phase 1 image decision) — never an arbitrary external URL.
3. **Attachment sidebar.** Render every attachment for the task in a **sidebar/side-column of the task view** (reuse the existing attachments-tab data and the app's panel/layout + CSS tokens — don't invent a new layering system): filename, human-readable size, type icon. Uploaded files link to the download endpoint; external links keep current behavior. Apply the existing long-filename truncation/ellipsis pattern. Delete uses the existing guard.
4. **Rendering.** Render description as **sanitized HTML** (no longer `esc()` as plain text), but only after passing API HTML through the client sanitizer — never `innerHTML` raw API content, even though the server also sanitizes. Verify newline handling for legacy plain-text descriptions (decide `white-space` CSS vs. `<br>`/`<p>` conversion; existing tasks must still render correctly with no data migration).
5. **Task preview.** Add a read-mostly preview surface (or extend the existing task view) that shows the **formatted description together with its attachments**, and renders **image attachments inline** (thumbnail strip/grid; clicking opens a minimal vanilla lightbox — Esc / click-out to close, prev/next for multiple). Non-image attachments show as a compact name + size list linking to download. Lazy-load attachment data when the preview opens; use `loading="lazy"` on thumbnails. If a preview overlay is built, it must **not** change the route/hash and must reuse existing modal focus-management (focus trap, return focus on close).

## Constraints

- **Backend**: no new Go dependencies beyond stdlib + existing `go-chi/chi` unless content-sniffing/sanitization is genuinely impractical without one — justify any addition.
- **Frontend**: no new framework, no lightbox/editor/sanitizer library — hand-roll the small allowlist and the image viewer with vanilla JS/CSS. Reuse existing CSS design tokens (`--space-*`, color/typography); no ad-hoc values.
- **RBAC**: upload / download / delete / description edit all use the same role checks as other task-mutation/read endpoints. No new unauthenticated image-serving path.
- **Other fields** (title, comments) stay plain text in this change — note rich-text comments as a possible follow-up only.
- **Accessibility**: toolbar buttons need i18n'd `aria-label`s; Ctrl/Cmd+B/I must not clash with existing shortcuts (e.g. command palette `Ctrl+K`); editor, sidebar, and preview/lightbox must be fully keyboard-operable; images need `alt` falling back to filename; preview overlay uses `role="dialog"` + `aria-modal`. Re-run the project's WCAG spot-check on the task screens.
- **i18n**: all new user-facing strings go through `js/i18n.js` + `locales/` — no hardcoded text; verify RTL/other locales don't break.
- **Backups**: uploaded files are user data — add the attachments storage volume to the backup/restore section of `docs/operations.md`.

## Deliverable

Create `prompts/_release-v2-audit.md` (or append to it if present) under a "Rich-text tasks" heading documenting: chosen description format + allowlist, sanitizer approach (client + server), storage backend/layout + env vars + endpoints, how inline images are referenced, the preview/sidebar UX, and any deferred items (virus scanning, object-storage migration, rich-text comments).

## Verification

```bash
cd octbase-api && go vet ./... && go test -race ./internal/workmanagement/...
node --check octbase-frontend/js/app.js
cd octbase-frontend/tests && pytest -k "attachment or description or preview"
```

Add/extend tests covering, at minimum:

- **Go**: upload→download round trip; oversized upload rejected; disallowed content type rejected; path-traversal filename neutralized; non-member cannot download; description XSS payloads sanitized on create/update; copy/delete of a task handles the underlying file.
- **Frontend (pytest/Playwright)**: uploading a file from the editor and seeing it in the sidebar; an attached image rendering inline in the preview; lightbox open/close via keyboard; formatted description rendering without script execution.

Before running, screenshotting, or visually verifying any frontend, invoke the `frontend-testing` skill first (per `CLAUDE.md`).
