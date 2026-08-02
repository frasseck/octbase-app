# Release v2 audit

## Rich-text tasks

Implements prompt `25_octbase-richtext-tasks.md`: a constrained rich-text editor
for task descriptions, inline file uploads with local-disk storage, an attachment
side-column, and a read-mostly task preview with an inline image lightbox. The two
highest-risk surfaces (stored HTML / XSS and user-uploaded binaries) are handled
with sanitization on write **and** read and strict file handling.

### Description storage format

A **constrained HTML subset** (not Markdown). It round-trips cleanly with a
`contenteditable` editor and needs no Markdown parser dependency on either side.
The server is the source of truth: every write path sanitizes against the
allowlist regardless of what the client sends.

Allowlist (block): `p`, `br`, `h3`, `h4`, `ul`, `ol`, `li`, `blockquote`, `pre`,
`code`. Inline: `strong`/`b`, `em`/`i`, `code`, `a` (href restricted to
`http(s)`/`mailto`/relative), `img` (`src` restricted to our own attachment
content endpoint + `alt`). Disallowed: `style`, `class`, `on*` handlers,
`<script>`, `<style>`, `<iframe>`, `<object>`, `<embed>`, arbitrary external
`<img src>` (avoids SSRF / tracking-pixel risk), `data:`/`javascript:` URLs.

### Sanitizer approach (client + server)

- **Server** (`internal/workmanagement/sanitize.go`): a hand-rolled, default-deny,
  allowlist tokenizer. No new Go dependency — `golang.org/x/net/html` is not in
  `go.mod` (not even indirect) and `bluemonday` would be a new dependency; the
  allowlist is small and constrained enough that a full HTML5 tokenizer is
  unnecessary. Applied via `CleanTaskDescription` on **create, update, template
  instantiation, and CSV import**. CSV **export** strips HTML to plain text
  (`StripHTMLToText`) so spreadsheets do not show raw markup and a re-import
  round-trips through the same sanitizer.
- **Client** (`app.js` `sanitizeRichText`): mirrors the same allowlist using
  `DOMParser` (no library) as defense-in-depth and for immediate editor feedback.
  API HTML is **never** assigned to `innerHTML` raw — it always passes through
  `renderDescriptionHTML` first. Legacy plain-text descriptions (no tags) are
  escaped and newline-converted, so existing tasks render correctly with no data
  migration.

### Storage backend / layout + env vars + endpoints

- **Backend**: local filesystem volume (`internal/workmanagement/storage.go`),
  appropriate for the single-instance podman-compose deployment. Object storage is
  out of scope (documented as a future multi-instance option in
  `docs/operations.md`).
- **Layout**: files addressed by a random 256-bit hex `storage_key` (never the
  user filename), sharded into two-char subdirectories. `pathFor` rejects
  non-hex/wrong-length keys and verifies the resolved path stays within root.
- **DB**: migration `013_attachment_storage` adds nullable `storage_key` to
  `task_attachments`. An attachment is **either** an uploaded file (`storage_key`)
  **or** an external link (`external_url`), never both; existing link rows keep
  working. `storage_key` is `json:"-"` (never serialized).
- **Env vars**: `OCTBASE_ATTACHMENTS_DIR` (default `/data/attachments`),
  `OCTBASE_MAX_UPLOAD_MB` (default 25).
- **Endpoints**:
  - `POST /api/v1/tasks/{taskId}/attachments/upload` — multipart upload. Size
    capped early with `http.MaxBytesReader`; content-type validated against an
    allowlist using **both** declared type and `http.DetectContentType` sniff;
    same writer/membership guard as other task mutations.
  - `GET /api/v1/tasks/{taskId}/attachments/{attachmentId}/content` — streams the
    bytes with the stored content-type, `X-Content-Type-Options: nosniff`, and
    `Content-Disposition: inline` for images / `attachment` otherwise. Enforces
    the same task-visibility guard as task reads; the storage dir is never served
    statically.
  - The existing `POST .../attachments` JSON path (external links) is unchanged.
- **Allowlist**: PNG/JPEG/GIF/WebP, PDF, plain text, CSV, zip, Word/Excel/
  PowerPoint (legacy + OOXML). SVG is intentionally excluded (can carry script).
  Executables/scripts rejected.
- **Lifecycle**: delete of a task/project and bulk task delete remove the
  underlying files (tolerating already-missing files). `CopyTask` duplicates the
  bytes under a new key so each task owns an independent file lifecycle (deleting
  one never orphans the other).

### Inline image referencing

Inline images come **only** from attachments served by our authenticated content
endpoint, referenced by the **relative path**
`/api/v1/tasks/<id>/attachments/<id>/content`. Both the server and client
sanitizers permit exactly this `src` shape and reject any scheme, host,
protocol-relative `//`, or `data:` URL. After a successful image upload the editor
offers to insert it inline at the caret.

### Preview / sidebar UX

- **Editor**: `contenteditable` with a toolbar (Bold, Italic, Bullet/Numbered
  list, Heading, Link, Code) using `document.execCommand`; i18n'd `aria-label`s;
  `Ctrl/Cmd+B`/`+I` are scoped to the editor so they don't clash with the command
  palette (`Ctrl+K`). Dirty-state / save UX preserved; save re-sanitizes client
  side then persists.
- **Inline upload**: toolbar attach button **plus** drag-and-drop onto the editor
  / attachment side-column **plus** paste (e.g. screenshots), all via the
  multipart endpoint, with progress/success/failure toasts.
- **Sidebar**: an attachment side-column next to the details editor (and a full
  attachments tab) listing each file with a type icon, filename (truncated with
  ellipsis) linking to the download endpoint, and human-readable size. External
  links keep their existing behavior. Delete uses the existing guard.
- **Preview**: a read-mostly overlay (`role="dialog"` + `aria-modal`) that renders
  the sanitized description together with attachments, shows image attachments
  inline as a `loading="lazy"` thumbnail grid, and lists non-image files as
  name + size download links. It does **not** change the route/hash and reuses
  modal-style focus management (focus trap, Esc to close, focus restored on
  close). Clicking a thumbnail opens a minimal vanilla **lightbox** (Esc / click
  -out to close, ArrowLeft/Right + prev/next for multiple images).
- **i18n**: all new strings added to `en`/`de`/`fr` locales. Other fields (title,
  comments) stay plain text.

### Deferred items

- **Virus/malware scanning** of uploads (e.g. ClamAV) — not implemented.
- **Object-storage migration** (S3/MinIO) for multi-instance deployments — local
  filesystem only for now.
- **Rich-text comments** — comments remain plain text; a possible follow-up.

## AsciiDoc pages

Implements prompt `26_octbase-asciidoc-pages.md`: the project wiki now renders a
genuine, defined subset of AsciiDoc (replacing the old minimal, partly-Markdown
renderer) with mandatory allowlist sanitization of all rendered HTML. The public
JSON shape (`content`, `renderedHtml`) and all routes are unchanged.

### Chosen renderer — hand-rolled (no new dependency)

A native-Go AsciiDoc library (`libasciidoc`) was rejected: it pulls a large
transitive dependency tree for fidelity the product does not need, and the prompt
favours minimal, owned code. Instead the renderer was hand-rolled
(`internal/docs/domain.go` + `inline.go`), so the backend keeps its
stdlib + `go-chi` only footprint (**zero new dependencies**). We own correctness
and document the exact supported surface.

**Supported AsciiDoc surface** (anything else is rendered as escaped text):

- Section titles `=` … `======` → `<h1>`…`<h6>`, each with a stable `id="h-…"`
  anchor (drives the read-view TOC; hierarchical for accessibility).
- Inline: bold `*text*` (and `**text**` as the unconstrained form), italic
  `_text_`, monospace `` `text` ``, macro links `https://…[label]` /
  `link:href[label]`, bare `http(s)` URLs, and `TASK-<uuid>` references rendered
  as `#/tasks/<id>` anchors (`ExtractTaskReferences` is unchanged and still
  drives the references table).
- Lists: unordered (`*` / `-`) and ordered (`.`) with nesting by marker depth.
- Blocks: listing/literal `----` and `....`, `[source,lang]` (adds
  `class="language-…"`), block quotes `____`, tables `|===` (first row → header),
  and admonitions `NOTE: / TIP: / WARNING: / IMPORTANT: / CAUTION:`.
- Block images `image::target[alt]` — relative targets only.

**Deliberately unsupported** (escaped, never executed): AsciiDoc passthrough
(`+++…+++`, `pass:[…]`, `++++` blocks), raw embedded HTML, `include::`,
attribute substitution, footnotes, and external/`data:` image sources.

### Sanitizer — docs-specific (justified), shared design

The rendered HTML is passed through `sanitizePageHTML` (`internal/docs/sanitize.go`),
a hand-rolled, default-deny, tokenize-and-rebuild allowlist sanitizer. It is a
**separate sanitizer from `workmanagement.SanitizeDescriptionHTML`** rather than a
promoted shared one, because the two have intentionally different allowlists:

- the task sanitizer permits a tiny contenteditable tag set and only allows
  `<img src>` pointing at the task attachment content endpoint;
- pages need the broader element set AsciiDoc emits (`h1`–`h6`, `table`/`tr`/`th`/
  `td`, `hr`, admonition `div`/`span` wrappers, `blockquote`/`cite`) and a
  different image policy (any rooted relative path, no external/`data:` sources).

Promoting one sanitizer would have meant either overloading the task allowlist
with page concerns or loosening its strict image rule. Both files share the same
proven design, so the security posture is identical. **Policy:** drop
`<script>/<iframe>/<style>/…` subtrees (content discarded), strip all `on*` and
`style` attributes, allow `href` only for `http(s)`/`mailto`/`#fragment`/relative
(reject `javascript:`/`data:`), allow `<img src>` only for rooted relative paths,
and re-escape every text run (idempotent — already-encoded entities are not
double-escaped). Sanitization is applied identically on create/update/publish,
render-on-read, and `RenderPreview`, so the live preview can never execute markup.
Defense-in-depth: the renderer also HTML-escapes all author source text itself,
so literal `<b>`/`<script>` in content renders as visible text.

### Existing pages — render-on-read, no migration

`RenderedHTML` is recomputed from the stored `Content` on every read (in the repo
`scanPage`/`scanPageRow`), so existing pages — whose `rendered_html` was produced
by the old renderer — display correctly with **no DB migration and no data
rewrite** (migration sequence is untouched; the next free number, 014, was *not*
needed here — 013 belongs to the rich-text task work). The `rendered_html` column
is still kept in sync on write but is not trusted on read.

**`**bold**` behavior change:** the old renderer special-cased the Markdown form
`**text**`. Under real AsciiDoc, `*text*` is bold and `**text**` is the
*unconstrained* bold marker — so `**text**` now renders as clean `<strong>` (no
stray asterisks) via AsciiDoc semantics rather than a Markdown special case. No
authored `Content` is ever rewritten; only the rendering changes. This is noted in
the README and user guide.

### Confluence import

The HTML→AsciiDoc importer already emits real AsciiDoc (`*strong*`, `=` headings)
and strips tags; since render-on-read sanitizes every page, imported content
(attacker-controllable) is rendered safely with no extra code path.

### Frontend & docs

Split-pane editor and 300 ms debounced preview are unchanged against the same
`renderedHtml` / `render-preview` contract (server is the single sanitization
source of truth). Added: a collapsible **Syntax help** cheatsheet in the editor
header (vanilla JS, existing CSS tokens, all strings via i18n in en/de/fr); CSS for
the newly emitted elements (admonitions, tables, code/source blocks, blockquotes,
task-ref/page links) scoped under `.asciidoc-content`; and `buildTOC` widened to
h1–h4 with anchor ids. `README.md` and `user-guide.html` were reconciled to match
the actually-supported surface and the `**bold**` note.
