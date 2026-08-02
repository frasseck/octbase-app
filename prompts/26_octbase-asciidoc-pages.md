You are a senior full-stack engineer making Octbase's project **wiki pages render real AsciiDoc**. Treat `18_octbase-security.md` as the baseline: page content is stored and served back to every member of a project, so rendered page HTML is a **stored-XSS surface** — sanitization of the rendered output is non-negotiable, and richer AsciiDoc features (passthrough blocks, link/image macros) widen that surface.

## Goal

The pages feature already *claims* to be AsciiDoc (README, `user-guide.html`, the split-pane editor), but the actual renderer supports only a tiny, partly-incorrect subset. Make it render **genuine AsciiDoc** so authored pages behave the way the product promises — without regressing the existing page lifecycle (drafts, publish, revisions, task references, search, Confluence import) or opening an XSS hole.

## Current state (verified — read before changing)

The pages feature lives in `octbase-api/internal/docs`:

- **`domain.go`** — `Page{ Content, RenderedHTML, ... }`, statuses `DRAFT`/`PUBLISHED`/`ARCHIVED`, `ExtractTaskReferences` (matches `TASK-<uuid>`), and **`RenderAsciiDoc(content) string`** — the renderer to replace.
- **`handler.go`** — `CreatePage`, `UpdatePage`, `PublishPage`, `ArchivePage`, `DeletePage`, `ListPages`, `GetPage`, `ListRevisions`, `ListReferences`, `RebuildReferences`, `SearchPages`, and **`RenderPreview`** (`POST /api/v1/pages/{pageId}/render-preview`). `RenderedHTML` is recomputed via `RenderAsciiDoc(...)` on create/update/publish; revisions snapshot `Content`; references come from `ExtractTaskReferences`.
- **`repo.go`** — persistence for pages, revisions, references.

**`RenderAsciiDoc` today is a minimal, partly-wrong subset** (`domain.go`):

- Headings only `=`/`==`/`===` → `<h1>/<h2>/<h3>` (no `====`+).
- "Bold" is `**text**` — that's **Markdown**, not AsciiDoc (AsciiDoc bold is `*text*`).
- Unordered lists only via `* ` (no ordered `. `, no nesting).
- No italic (`_text_`), monospace (`` `text` ``), links/xrefs, code/literal blocks, tables, admonitions (NOTE/TIP/WARNING…), block quotes, or images.
- It HTML-escapes everything, so it is currently XSS-safe by construction but feature-poor.

**Frontend** (`octbase-frontend/js/app.js`): `api.pages` (~line 285) incl. `preview` → `render-preview`; the split-pane editor + TOC (~line 3428) renders the server's `renderedHtml` directly into `innerHTML` (~line 3488) and refreshes a debounced preview as you type. `openProjectPage` (~line 1955) opens a page.

**Docs already over-promise**: `README.md` ("AsciiDoc editor with split-pane live preview", "best-effort HTML → AsciiDoc"), `octbase-frontend/user-guide.html` (AsciiDoc wiki, 300 ms debounce, Confluence HTML → AsciiDoc import). Bring the implementation up to what these claim.

**Deps**: backend is Go stdlib + `github.com/go-chi/chi/v5` only — no AsciiDoc/Markdown/sanitizer library present.

## Phase 1 — Analysis (no code changes)

1. Enumerate every place page content is rendered or transformed: `RenderAsciiDoc` callers in `handler.go` (create/update/publish/preview), the Confluence HTML→AsciiDoc import path (search `docs`/`admin` for the importer), `ExtractTaskReferences`, search snippets, and the frontend preview/TOC injection (`app.js` ~3428–3492). Each must keep working under the new renderer.
2. **Choose the rendering approach** and justify it in the deliverable:
   - **(a) A native-Go AsciiDoc library** (e.g. `github.com/bytesparadise/libasciidoc`) for real fidelity — recommended given the feature is already advertised as full AsciiDoc — accepting one well-scoped dependency.
   - **(b) Extend the hand-rolled parser** to a defined, larger subset — no new dependency, but you own correctness and must document exactly which AsciiDoc constructs are supported.
   Pick one; if (a), pin the version and confirm its license/maintenance are acceptable. Either way, **define the supported AsciiDoc surface explicitly** (see Phase 2).
3. **Define the security policy for rendered output.** AsciiDoc can emit raw HTML via passthrough (`+++…+++`, `pass:[…]`, passthrough blocks) and arbitrary URLs via `link:`/`image::`/`xref`. Decide: disable passthrough/raw-HTML entirely (recommended), restrict `link:` hrefs to `http(s)`/relative (no `javascript:`), and constrain `image::` targets (block arbitrary external URLs — SSRF/tracking-pixel risk; allow only relative/whitelisted sources, or route through an authenticated endpoint). Plan to run the rendered HTML through an allowlist sanitizer regardless of which renderer produces it.
4. **Plan for existing stored content.** `RenderedHTML` for current pages was produced by the old renderer, and some content uses the old `**bold**` convention (Markdown) which real AsciiDoc will *not* bold. Decide handling: re-render all pages' `RenderedHTML` from stored `Content` under the new renderer (a one-time backfill or render-on-read), and document the behavior change for `**…**`. **Do not** silently rewrite users' authored `Content`; if you offer a `**` → `*` conversion, make it explicit/opt-in and reversible. Note this clearly in the deliverable.

## Phase 2 — Backend: real AsciiDoc rendering

1. Replace `RenderAsciiDoc` (keep the signature, or wrap it) so it supports, at minimum:
   - Headings `=` … `======` (mapped to a sensible `<h1>`–`<h6>` range).
   - Inline: bold `*text*`, italic `_text_`, monospace `` `text` ``, links (`https://…[label]`, `link:…[]`), and the existing `TASK-<uuid>` references (consider a proper AsciiDoc-friendly form while keeping `ExtractTaskReferences` working).
   - Lists: unordered (`*`/`-`), ordered (`.`), and nesting.
   - Blocks: literal/code blocks (`----`, `[source,lang]`), block quotes, tables (`|===`), and admonitions (`NOTE:`/`TIP:`/`WARNING:`/`IMPORTANT:`/`CAUTION:`).
   - Images per the Phase 1 policy (restricted sources only).
2. **Sanitize the rendered HTML** before storing/returning it — strip `<script>`/`<iframe>`/`on*`/`style`, neutralize `javascript:`/`data:` URLs, drop disallowed tags/attrs — using an allowlist (prefer `golang.org/x/net/html`-based hand-rolled allowlist, or a vetted sanitizer; justify). Apply identically in create/update/publish **and** `RenderPreview` so the live preview can never execute injected markup.
3. Keep `ExtractTaskReferences` accurate against the new syntax; ensure `RebuildReferences` and the references table stay correct.
4. Ensure the **Confluence HTML→AsciiDoc import** still produces content that renders correctly under the new renderer; sanitize imported content the same way (imported HTML is attacker-controllable).
5. **Backfill / migration**: implement the Phase 1 plan so existing pages display correctly (re-render `RenderedHTML` from `Content`). If a DB migration is needed, add it as the next sequential migration (`013_*`) with `up`+`down`; if render-on-read instead, document why no migration is required.

## Phase 3 — Frontend & docs

1. The split-pane editor (`app.js` ~3428) and debounced preview should work unchanged against the same `renderedHtml`/`render-preview` contract — but **never inject `renderedHtml` without trusting that the server sanitized it**; since the server is now the sanitization source of truth, confirm the injection point and keep it consistent (no raw, unsanitized HTML path).
2. Add an AsciiDoc **quick-reference / cheatsheet** affordance near the editor (the supported syntax from Phase 2), and optionally a small toolbar — keep it lightweight, vanilla JS, reuse existing CSS tokens. All new strings via `js/i18n.js` + `locales/` (no hardcoded text).
3. Add CSS for the newly emitted elements (admonitions, tables, code blocks, blockquotes) consistent with existing design tokens — the current `.asciidoc-content` wrapper has little styling for these.
4. **Reconcile the docs** that already advertise AsciiDoc (`README.md`, `octbase-frontend/user-guide.html`) so the described feature set matches what's now actually supported (and note any deliberately unsupported constructs).

## Constraints

- Backend: at most **one** new, well-scoped dependency (the AsciiDoc renderer) plus possibly a sanitizer — justify each; prefer hand-rolled sanitization given the constrained allowlist. No change to the public JSON shape (`content`, `renderedHtml`) or existing routes.
- Frontend: no new framework; reuse existing editor, CSS tokens, toast, i18n.
- Preserve the full page lifecycle and RBAC: create/update/publish/archive/delete/preview/search and the existing membership/role guards in `handler.go` are unchanged in behavior.
- Rendered output must be safe regardless of input — assume page authors and Confluence imports can be malicious.
- Accessibility: rendered headings must be hierarchical for the TOC; tables/admonitions need accessible markup; re-run the project's WCAG spot-check on the pages screen. i18n all new UI strings.

## Deliverable

Append to (or create) `prompts/_release-v2-audit.md` under "AsciiDoc pages": chosen renderer (library vs. hand-rolled, version/license if a library), the exact supported AsciiDoc surface, the sanitization policy (passthrough disabled, link/image restrictions), how existing pages were handled (backfill vs. render-on-read) and the `**bold**` behavior change, and any deliberately unsupported constructs.

## Verification

```bash
cd octbase-api && go vet ./... && go test -race ./internal/docs/...
node --check octbase-frontend/js/app.js
cd octbase-frontend/tests && pytest -k "page or asciidoc"
```

Add/extend tests covering, at minimum:

- **Go**: rendering of each supported construct (headings, bold/italic/monospace, ordered+nested lists, links, code blocks, tables, admonitions); `TASK-<uuid>` still extracted; **XSS payloads sanitized** (passthrough raw HTML, `<script>`, `onerror=`, `javascript:`/`data:` links, malicious `image::` src) through create/update/publish **and** render-preview; Confluence-imported HTML renders + sanitizes correctly; existing-page backfill produces expected HTML.
- **Frontend (pytest/Playwright)**: editing a page shows a correct live preview (no script execution); a published page renders formatted AsciiDoc; the TOC reflects the heading hierarchy.

Before running, screenshotting, or visually verifying any frontend, invoke the `frontend-testing` skill first (per `CLAUDE.md`).
