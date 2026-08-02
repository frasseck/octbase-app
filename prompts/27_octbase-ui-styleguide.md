# Octbase — UI Style Guide & Design-System Consolidation

**Role:** Act as a Senior UX Designer and Frontend Engineer.

**Purpose:** Define one consistent, modern design language for Octbase and make the
whole application compliant with it. The app already ships a Material Design 3 (M3)
green theme; this work does **not** restart the design — it *formalizes* the existing
tokens into a documented style guide, replaces the current grab-bag of emoji / Unicode
glyph icons with a single coherent SVG icon set, prefers **icons over text buttons**
wherever an icon is unambiguous, and guarantees the UI is **100 % responsive** from a
360 px phone to a wide desktop.

**Scope:** `octbase-frontend/` only (`index.html`, `css/app.css`, `js/app.js`,
`locales/*.json`). No API, database, or backend changes. All HTML is generated
dynamically by `app.js`; CSS class names in `app.css` must match exactly what `app.js`
emits — never rename or remove a class without updating both sides.

**Deliverables:**
1. `prompts/27_octbase-ui-styleguide.md` — this document.
2. `docs/octbase-ui-styleguide.pdf` — the rendered, shareable style guide.
3. The application, fully compliant with the guide.

**Reference:** https://m3.material.io/

---

## 0. Design principles

1. **Consistent before clever.** One token, one component, one pattern per job. If two
   places do the same thing, they look and behave identically.
2. **Icons carry recurring actions.** Recurring/iconographic actions (close, edit,
   delete, add, refresh, filter, menu, settings, attach, link, copy, view) render as
   **icon buttons** with an accessible label and tooltip — not as text buttons. Reserve
   text (or icon + text) buttons for primary, page-level, or ambiguous actions
   (e.g. *Save*, *Create project*, *Sign in*).
3. **Modern & calm.** Generous whitespace on the standardized spacing scale, soft M3
   elevation, fully rounded interactive surfaces, restrained green palette.
4. **Responsive by default.** Every view works at 360 px. Nothing is desktop-only;
   nothing requires horizontal scrolling except the deliberately scrollable board.
5. **Accessible.** WCAG 2.2 AA: every icon-only control has `aria-label` + `title`,
   focus is always visible, hit targets are ≥ 40 px, color is never the sole signal.

---

## 1. Design tokens (already in `:root`, keep authoritative)

Do not introduce new hardcoded hex values, spacing numbers, radii, or shadows in
`app.js` markup or `app.css` rules — reference the tokens.

### 1.1 Color — M3 green
- Primary `--md-primary #006C4F`, on-primary `#FFFFFF`, primary-container `#B0EDD0`.
- Secondary (sage), Tertiary (olive), Error `#BA1A1A`.
- Surfaces: `--md-surface-cl … --md-surface-c-highest` (white → green-tinted).
- Outline `--md-outline-variant #BBCABD`. Status/priority/type swatches as defined.

### 1.2 Typography — IBM Plex Sans (self-hosted)
| Role | Size | Weight |
|------|------|--------|
| Display / page title | 1.375–1.5rem | 600 |
| Section title | 1rem | 600 |
| Body | 0.875rem | 400 |
| Label / button | 0.875rem | 500 |
| Caption / overline | 0.6875–0.75rem | 500, +letter-spacing |
Monospace (branch names, code, kbd): system monospace stack.

### 1.3 Spacing scale (4-pt grid)
`--space-1 4` · `-2 8` · `-3 12` · `-4 16` · `-6 24` · `-8 32` · `-12 48` · `-16 64`.

### 1.4 Shape
`--md-xs 4` · `-sm 8` · `-md 12` · `-lg 16` · `-xl 28` · `-full 9999`.
Cards & dialogs `md`/`lg`; chips, buttons, icon buttons, nav items `full`.

### 1.5 Elevation
`--md-e1 … --md-e4`. Resting cards e0–e1; menus/dialogs/side-sheet e2–e3; snackbar e3.

---

## 2. Icon system (the central change)

**Today** icons are a mix of emoji and Unicode glyphs (`⌂ ☰ ✕ 🗑 📄 ⚑ 📎 ⚙ 👁 🔗 ✎ ⟳
⋯` …) plus a handful of ad-hoc inline `<svg>`. Replace all of them with one registry.

### 2.1 Registry + helper
In `app.js` add an `ICONS` map of name → SVG path markup (24×24 viewBox, `fill`/`stroke`
`currentColor`, `stroke-width:2`, round joins — Material-Symbols / Lucide style, optically
consistent) and a helper:

```js
function icon(name, { size = 20, cls = '' } = {}) {
  const path = ICONS[name] || ICONS.dot;
  return `<svg class="icon-svg ${cls}" width="${size}" height="${size}"
    viewBox="0 0 24 24" aria-hidden="true" focusable="false">${path}</svg>`;
}
```

Minimum icon set (name → meaning): `home, board, backlog, sprint, milestone, release,
project, page, doc, search, settings, bell, menu, close, add, edit, delete, copy,
refresh, filter, sort, more, attach, link, view, comment, branch, check, chevron-left,
chevron-right, chevron-down, warning, logout, user, drag, calendar, time, image,
archive, external, expand, collapse, kebab`. One glyph per concept, reused everywhere.

### 2.2 Icon buttons (icons instead of buttons)
Single component, used for every icon-only action:

```html
<button type="button" class="icon-btn" data-act="…"
        aria-label="{{label}}" title="{{label}}">{{icon(name)}}</button>
```

- `.icon-btn`: transparent, `border-radius: var(--md-full)`, `padding: var(--space-2)`,
  color `--md-on-surface-variant`; hover tint `rgba(25,28,26,.08)` → `--md-on-surface`;
  disabled `opacity:.38`. `.icon-btn-sm` = `padding: var(--space-1)`.
- Merge the two legacy classes `.btn-icon` and `.icon-btn` into **one** (`.icon-btn`);
  update every emitter. Min hit target 40 px (pad the SVG, not the glyph).
- Convert these recurring text buttons to icon buttons (label kept as tooltip + aria):
  close/dismiss, edit, delete, copy/duplicate, refresh/reload, add row/inline-add,
  filter, sort, settings/config, attach, link/relations, view/preview, comment,
  overflow menu (kebab), expand/collapse, drawer toggle, theme/notifications.
- Keep text (icon + text) buttons for: Save, Cancel in dialogs, Create/New <entity>,
  Sign in/out confirmation, and any destructive confirm inside a dialog.

### 2.3 Tooltips
Icon buttons rely on the native `title` for the tooltip and `aria-label` for
assistive tech — both pull from the same i18n string. Never ship an icon-only control
without both.

---

## 3. Components (canonical specs)

- **App bar (`#topbar`)**: 64 px, surface-c-low, bottom hairline. Left: drawer toggle
  (mobile) + breadcrumb/title. Right: contextual icon buttons (search, filter, refresh,
  notifications) then the primary text button for the view, then avatar.
- **Navigation drawer (`#sidebar`)**: 260 px; items are `--md-full` pills, 44 px tall,
  leading `icon()` + label; active = secondary-container. Off-canvas under 768 px.
- **Buttons**: `.btn` (full radius) with `-primary/-secondary/-ghost/-danger/-warning/
  -success`. Three sizes — all sized to content, **never 100 % width**:
  | Class | Height | Padding | Use |
  |-------|--------|---------|-----|
  | `.btn-sm` | 32 px | `space-1 / space-4` | compact, inline & table-row actions |
  | `.btn` (md) | 40 px | `0.625rem / space-6` | default |
  | `.btn-lg` | 48 px | `space-3 / space-8` | prominent page-level CTAs (e.g. auth submit) |
  Icon-in-button uses `icon(name,{size:18})` + gap-2. There is no full-width button
  class; a button that must read as primary uses `.btn-lg`, not `width:100%`.
- **Chips/badges**: status, priority dot, type badge — unchanged palette, full radius.
- **Cards**: surface white, radius `md`, e1, `--space-4` padding.
- **Side sheet / Task panel**: right side-sheet on desktop; **full-screen sheet** under
  768 px. Header = title + close icon button.
- **Dialog**: centered, radius `lg`, e3, max-width 32rem, `width: min(32rem, calc(100vw
  - 2rem))`; actions right-aligned, primary last.
- **Forms**: M3 outlined text fields, 1px outline → 2px primary on focus, label above,
  helper/error below, error in `--md-error`.
- **Toast/snackbar**: inverse-surface, e3, bottom-center, auto-dismiss.
- **Tables/lists**: zebra-free, hairline row dividers, hover tint, sticky header.
- **Empty states**: centered icon + one line + (optional) primary action.

---

## 4. Responsiveness — single breakpoint system

Adopt three breakpoints and **remove the ad-hoc/overlapping media queries** currently in
`app.css` (`800/900/1160/767/768-1023…`), consolidating into:

- **Compact `≤ 600px`** (phone): drawer off-canvas with scrim + toggle; topbar collapses
  to toggle + title + overflow kebab; task panel & dialogs full-screen; board scrolls
  horizontally, columns `min-width: 80vw`; tables → stacked cards or horizontal scroll
  with sticky first column; padding drops to `--space-3/-4`.
- **Medium `601–1023px`** (tablet): drawer collapsible (toggle), 2-up grids, side-sheet
  at `min(90vw, var(--panel-w))`.
- **Expanded `≥ 1024px`** (desktop): permanent drawer, full layout, side-sheet pushes
  content (`#main.panel-open`).

After the rewrite **every `@media` query uses only `600px` or `1024px`** — no other
breakpoint value remains (`docs.html`/`user-guide.html` static docs excepted).

**Dialogs & overlays.** Size every dialog (`.modal`, including confirm/delete) as
`width: min(30rem, calc(100vw - 2 * var(--space-4)))` (wide variant `38rem`) so it never
exceeds the viewport nor sits edge-to-edge on desktop; give `#modal-backdrop` `--space-4`
padding. Cap height and keep the body scrollable so the **`.modal-footer` actions are
always reachable**. On Compact present the dialog as a bottom sheet (full width,
top-rounded `--md-xl`, internal scroll, footer pinned with `position: sticky`,
`env(safe-area-inset-bottom)` respected). The command palette (`#palette-overlay`),
shortcuts, notifications (`#notif-panel`) and login box scale the same way.

**Task-panel tab strip.** `.panel-tabs` keeps its desktop/tablet look but must never wrap
into a broken multi-row strip on Compact: make it a horizontally scrollable strip
(`flex-wrap: nowrap; overflow-x: auto; scroll-snap-type: x proximity`, hidden scrollbar,
momentum scroll), each `.panel-tab` `flex: 0 0 auto; white-space: nowrap`, the active tab
scrolled into view, touch targets ≥ 40 px.

Rules: no fixed pixel widths that exceed the viewport; use `min()/clamp()/%`; images and
the logo are fluid; `min-height: 0` on flex children that scroll; touch targets ≥ 40 px;
the board and the tab strip are the only intentional horizontal scrollers;
test at 360, 414, 768, 1024, 1440 px.

---

## 5. Implementation order

1. **Tokens & icon CSS** in `app.css`: confirm `:root`, add `.icon-svg` (sizing,
   `vertical-align`, `flex-shrink:0`), consolidate `.icon-btn`, add tooltip-friendly
   focus ring; replace the scattered media queries with the §4 set.
2. **Icon registry + `icon()` helper** in `app.js`.
3. **Sweep `app.js`**: replace every emoji/Unicode glyph and ad-hoc `<svg>` with
   `icon(...)`; convert §2.2 recurring text buttons to `.icon-btn` with `aria-label`+
   `title` from i18n; ensure each `data-act` still resolves.
4. **i18n**: add any missing `a11y.*`/tooltip strings to `en/de/fr.json` (keep all three
   in sync; never leave a hardcoded English label in markup).
5. **Responsive pass**: drawer scrim + toggle, full-screen sheets/dialogs on compact,
   board/table behavior.

---

## 6. Acceptance criteria

- [ ] No emoji or decorative Unicode glyph icons remain in `app.js`; all icons come from
      `ICONS` via `icon()`.
- [ ] Every icon-only control has matching `aria-label` **and** `title`, both i18n-driven.
- [ ] Exactly one icon-button class (`.icon-btn`) and one button component (`.btn`)
      with three sizes (`-sm` / md / `-lg`); no button uses `width:100%`.
- [ ] No redundant controls: a value already conveyed by context isn't also offered as
      a duplicate control (e.g. board cards show no status/column dropdown — the lane
      conveys it; the task panel has no duplicate preview button).
- [ ] Checkbox lists (backlog, task list) have a "select all" control wired to the
      bulk-action bar.
- [ ] No hardcoded hex/spacing/radius/shadow literals in new/changed CSS or JS markup.
- [ ] Layout is usable with no unintended horizontal scroll at 360/414/768/1024/1440 px;
      drawer, task panel, and dialogs behave per §4.
- [ ] All `octbase-frontend/tests` Playwright tests pass; visual check at mobile + desktop.
- [ ] `docs/octbase-ui-styleguide.pdf` reflects the shipped system.

---

## 7. Build / verify

```bash
# Frontend is a static bundle served by Caddy/nginx; rebuild the container after edits:
podman-compose build octbase-frontend
podman stop octbase_octbase-frontend_1 && podman rm octbase_octbase-frontend_1
podman-compose up -d octbase-frontend
```
Run the Playwright suite in `octbase-frontend/tests` and screenshot key views at compact
and expanded widths before sign-off.
