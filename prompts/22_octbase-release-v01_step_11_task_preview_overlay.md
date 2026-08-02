You are a senior frontend engineer adding a **task preview overlay** ("quick view") to Octbase — a fast way to inspect a task's details and image attachments without leaving the current view (board, backlog, list, search results). Read `prompts/_release-v01-audit.md` first. This step builds on `step_09` (file uploads/attachments) and `step_10` (rich-text description rendering) — if either hasn't landed yet, implement against the *current* attachment/description model and note the upgrade path; do not block on them.

## Context

Octbase already has a full task panel (`#task-panel` / `#task-panel-content` in `app.js`) for editing a task, and a generic modal (`#modal-backdrop` / `#modal`) used for confirmations and forms. The preview overlay is a **third, lighter-weight surface**: read-mostly, optimized for quickly checking "what is this task about and what does it look like" from a card in the board/backlog without triggering full navigation/route change (which the task panel currently does, per the README's bookmarkable-URL behavior).

## Phase 1 — Analysis (no code changes)

1. Identify every place a task is shown as a compact item that could benefit from a preview trigger: board cards, backlog rows, task list rows, search/command-palette results, notification references.
2. Confirm the bookmarkable-URL/hash-routing behavior (`README`'s "Navigation & UX") — the preview overlay must NOT change the route/hash (unlike opening the full task panel), so back/forward and bookmarks remain unaffected. Decide the trigger: e.g. a dedicated "preview" icon button on hover/focus per card, distinct from clicking the card itself (which opens the full panel).
3. Decide what the preview shows: title, type/status/priority badges, assignee, description (rendered per `step_10` if available, else plain escaped text), and an image gallery/strip for image attachments (per `step_09` if available, else `task_attachments` with `external_url` pointing at image content types).

## Phase 2 — Implementation

1. **Overlay component**: add a new lightweight overlay (separate DOM node, e.g. `#task-preview-overlay`, not reusing `#modal-backdrop` if the modal stack needs to remain available underneath — but reuse the existing overlay/backdrop CSS patterns and z-index scheme from `css/app.css`, don't invent a new layering system).
2. **Trigger and dismissal**: open on the preview affordance (click/Enter on the preview icon, or a keyboard shortcut consistent with existing patterns); close on Esc, click-outside, and an explicit close button. Implement a focus trap and return focus to the trigger element on close (mirror the existing modal's focus-management code — search for `_modalReturnFocus`/`_modalKeydownHandler`).
3. **Content**:
   - Render title, type/status/priority/assignee using the same badge components/styles used elsewhere (no new visual language).
   - Render description read-only (sanitized HTML from `step_10`, or escaped plain text as fallback).
   - **Image attachments**: fetch `GET /api/v1/tasks/{taskId}/attachments`, filter to image content types (`image/*`), and render as a thumbnail strip/grid. Clicking a thumbnail opens a simple lightbox (full-size image, Esc/click to close, prev/next if multiple) — implement as a nested layer within the same overlay system, not a separate modal stack.
   - Non-image attachments: show as a compact file list (name + size), linking to the download endpoint from `step_09` if present, or `external_url` otherwise.
   - Provide a clear "Open full task" action that performs the existing full-panel navigation (changing the route/hash as today).
4. **Performance**: lazy-load attachment data only when the overlay opens (don't fetch for every card up front); use `loading="lazy"` for thumbnail `<img>` tags.
5. **Empty/loading states**: while attachments load, show a skeleton/spinner consistent with `step_05`'s empty/loading-state patterns; if a task has no description/no images, show appropriate empty copy (i18n'd), not blank space.

## Constraints

- No new frontend dependencies/frameworks; no lightbox library — build the minimal image viewer with vanilla JS/CSS.
- Must not alter the URL/hash (route stays on the board/backlog/list); must not break bookmarkable URLs or the existing full task panel.
- Reuse existing CSS design tokens (`--space-*`, color/typography tokens from `step_21`/`step_06`) — no ad-hoc values.
- Keyboard accessible end-to-end (open, navigate images, close) and screen-reader friendly (`role="dialog"`, `aria-modal`, `aria-label`, image `alt` text falling back to filename). Re-run the WCAG spot-check from `step_05` on the screens where the trigger is added.
- i18n all new strings.
- Respect existing image-attachment authorization — the overlay must use the same authenticated endpoints as the full task panel, no new unauthenticated image-serving path.

## Deliverable

Append to `prompts/_release-v01-audit.md` under "Task preview overlay": which surfaces got the preview trigger, before/after description of the interaction, and any deferred surfaces with reason.

## Verification

```bash
node --check octbase-frontend/js/app.js
cd octbase-frontend/tests && pytest -k preview
```
Add/extend a Python UI test covering: opening the preview from a board card without changing the URL, image thumbnail rendering and lightbox open/close, Esc closing the overlay and returning focus, and "Open full task" navigating to the full panel.
