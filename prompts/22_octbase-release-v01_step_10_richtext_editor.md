You are a senior frontend engineer adding a **simple rich-text editor for task descriptions** to Octbase. Read `prompts/_release-v01-audit.md` first. Treat `18_octbase-security.md` as the baseline — rich text means stored HTML, which is the highest-risk XSS surface in the app, so sanitization on both write and read is non-negotiable.

## Context

`task.description` is currently a plain-text field rendered via a `<textarea>` and escaped with `esc()` on display (`app.js` around line 2462). Task comments (`task_comments.text`) follow the same plain-text pattern. This step upgrades the **task description** editor only (comments stay plain text — note as a possible future follow-up but do not implement, to keep this change small).

## Phase 1 — Analysis (no code changes)

1. Read how `description` flows today: editor (`app.js` ~2406–2466), save (`saveTaskDescription`/`api.tasks.update`, ~2649), and render (`esc(task.description)`). Identify every place `task.description` is rendered (task panel, board cards if descriptions are previewed, task list, search results, exports/CSV).
2. Decide the storage format: a constrained HTML subset (recommended) vs. Markdown. Recommend **HTML subset** since it round-trips simply with `contenteditable` and avoids a Markdown parser dependency — but flag the tradeoff (Markdown is safer-by-default and diff-friendly) and let the implementation match whichever you pick consistently across backend and frontend.
3. Define the allowlist: block-level (`p`, `h3`, `h4`, `ul`, `ol`, `li`, `blockquote`, `pre`, `code`), inline (`strong`/`b`, `em`/`i`, `a` with `href` restricted to `http(s)`/relative, `code`), no `style`, `class`, `on*` attributes, `<script>`, `<iframe>`, `<img>` with arbitrary `src` (images are handled by `step_09`/`step_11` via attachments, not inline `<img>` tags, to avoid SSRF/tracking-pixel risk from arbitrary URLs).

## Phase 2 — Backend: server-side sanitization

1. Add a minimal Go HTML sanitizer applied to `description` on every create/update of a task (`internal/workmanagement/service.go`). If a small, well-maintained sanitizer library (e.g. `bluemonday`) is acceptable per the project's dependency policy, use it with a strict custom policy matching Phase 1's allowlist; otherwise implement a minimal allowlist-based tag/attribute stripper using `golang.org/x/net/html` (already likely an indirect dependency — check `go.mod`). Justify the choice in the deliverable.
2. The server is the source of truth: never trust client-side sanitization alone. Add unit tests in `internal/workmanagement` feeding known XSS payloads (`<script>`, `onerror=`, `javascript:` hrefs, malformed/nested tags) through `CreateTask`/`UpdateTask` and asserting the stored/returned description is clean.
3. Confirm the CSV import/export path (`jira_csv.go`) either sanitizes imported HTML the same way or strips it to plain text — imported content is attacker-controllable too.

## Phase 3 — Frontend: editor and rendering

1. Build a lightweight `contenteditable`-based editor component in `app.js` (or a new small module if it keeps the file organized — follow existing module conventions): a toolbar with Bold, Italic, Bullet list, Numbered list, Heading, Link, Code block, matching the allowlist from Phase 1. Use `document.execCommand` only if still reliable for the supported browser set, otherwise implement the small set of commands manually via Selection/Range APIs — pick whichever keeps the diff smallest and document the choice.
2. On save, run the same client-side sanitization logic (mirroring the server allowlist, e.g. via `DOMPurify`-equivalent hand-rolled allowlist using `DOMParser` — no new heavy dependency) before sending to the API, as defense-in-depth and to give the user immediate feedback if something was stripped.
3. On render, set the description via the sanitized HTML (not `esc()` as plain text anymore) — but only after sanitization; never insert raw `description` from the API into `innerHTML` without passing through the sanitizer first, even though the server also sanitizes (defense in depth against a compromised/old API).
4. Keep the existing dirty-state/save/unsaved-changes UX (`getTaskDraft`, `isTaskDraftDirty`, save button) working with the new editor's content model.
5. **Accessibility**: toolbar buttons need `aria-label`s (i18n'd), keyboard shortcuts (Ctrl/Cmd+B/I) must not conflict with existing app shortcuts (`README`'s "Navigation & UX" / command palette `Ctrl+K`), and the editor must be reachable and operable via keyboard alone (tab to toolbar, standard contenteditable text navigation). Re-run the WCAG spot-check from `step_05` on the task panel.
6. **i18n**: toolbar labels/tooltips go through the existing i18n system; verify the editor doesn't break RTL or other locales already supported.

## Constraints

- No new frontend framework. A new small, focused dependency for sanitization is acceptable on either side *only if* a hand-rolled allowlist proves meaningfully riskier — prefer hand-rolled if it's small (this is a constrained allowlist, not general HTML sanitization).
- Other task fields (title, comments) remain plain text in this step.
- Existing tasks with plain-text descriptions must continue to render correctly (plain text is valid input to the allowlist sanitizer — it passes through unchanged, just no longer needs `esc()` if rendered as sanitized HTML... but verify newline handling: plain text relies on `white-space` CSS or explicit `<br>`/`<p>` conversion, decide and document).

## Deliverable

Append to `prompts/_release-v01-audit.md` under "Rich text editor": chosen storage format, allowlist, sanitizer approach (client + server), and migration note for existing plain-text descriptions (should need none, but confirm).

## Verification

```bash
cd octbase-api && go vet ./... && go test -race ./internal/workmanagement/... -run Sanitiz
node --check octbase-frontend/js/app.js
cd octbase-frontend/tests && pytest -k description
```
