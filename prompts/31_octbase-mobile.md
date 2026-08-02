# Octbase — Mobile-First Companion App (`octbase-mobile`)

**Role:** Act as a Senior Frontend Engineer and Mobile UX specialist.

**Purpose:** Octbase ships a polished **desktop** SPA (`octbase-frontend/`) that is excellent
on tablets, laptops and wide screens but is cramped and awkward on a phone. This work adds a
**separate, mobile-first** front end in `octbase-mobile/` that gives phone users a native-feeling,
thumb-driven experience for the *core* day-to-day flows. It is an **enhancement, not a replacement**:
the desktop app is left untouched and remains authoritative; the mobile app is purely additive and
talks to the **same backend** (`octbase-api`, `/api/v1`).

> **Hard rule — one device, one experience.**
> - **Phones (viewport `< 768px`)** → the **mobile** app is shown.
> - **Tablets and larger (`≥ 768px`)** → the **desktop** app (`octbase-frontend`) is shown.
> The mobile app is *strictly mobile-first*: it is designed for a 360 px phone first and never tries
> to grow into a desktop layout. When a tablet-or-wider viewport is detected, the mobile entry point
> hands off to the desktop app rather than scaling itself up.

---

## 0. Principles (be strict)

1. **Mobile-first, phone-only.** Author every rule for a 360–430 px phone in portrait. No desktop
   grid, no multi-column body, no hover-only affordances. Touch is the only input assumed.
2. **Reduce, don't shrink.** Where a desktop feature is too heavy for a phone, *cut it* from the
   mobile app and offer "Open on desktop" rather than cramming it in. A smaller, sharper feature set
   beats a faithful-but-unusable port.
3. **Reuse the design language.** Same Material Design 3 green theme, same tokens, same icon set,
   same i18n keys and copy as the desktop app. The mobile app must feel like the *same product*.
4. **Reuse the contracts, not the layout.** Reuse the desktop app's `Auth`, HTTP client, `api`
   surface, `icon()`/`ICONS`, and `i18n.js` verbatim (extracted into a shared `core.js`). Build a
   fresh, lean view layer — do **not** import `octbase-frontend/css/app.css` or its render functions.
5. **Thumb-reachable & native-feeling.** Primary navigation lives in a **bottom navigation bar**.
   Secondary actions live in **bottom sheets**. Detail screens are **full-screen** with a back
   affordance. Hit targets ≥ 48 px. Respect `safe-area-inset-*` (notches / home indicator).
6. **Accessible (WCAG 2.2 AA).** Every icon-only control has `aria-label` + `title`; focus is always
   visible; color is never the only signal; dynamic regions announce via `role`/`aria-live`.

---

## 1. Architecture & device routing

- `octbase-mobile/` is a **standalone static SPA** served exactly like `octbase-frontend/`
  (Caddy container, same CSP, reverse-proxied `/api/*`). It can be deployed at its own host
  (e.g. `m.octbase…`) or behind a path; it does **not** require the desktop app to be modified.
- **Device handoff (the "one device, one experience" rule):**
  - `octbase-mobile/index.html` runs a tiny inline guard **before** loading the app: if
    `window.matchMedia('(min-width: 768px)').matches`, it redirects to the desktop app URL
    (`DESKTOP_URL`, configurable; defaults to `../` / same origin root) — tablets and desktops never
    see the mobile UI.
  - The handoff also fires on resize/orientation change so rotating a small tablet into a ≥ 768px
    width crosses over to desktop.
  - **Optional (opt-in, documented but not enabled by default):** a one-line snippet the desktop
    `index.html` *could* add to redirect `< 768px` phones to the mobile app. Left to the operator so
    the desktop app stays byte-for-byte unchanged unless they choose to wire it up.
- **Auth is shared.** Same JWT access-token-in-memory + httpOnly refresh-cookie model. A user signs
  in on the mobile app the same way; `file://` standalone demo auto-login is preserved for local dev.
- **No build step required** to run; mobile mirrors the desktop Containerfile (optional esbuild
  whitespace/syntax minify, **no identifier mangling** — the `data-act` dispatch keys on `fn.name`).

### File layout
```
octbase-mobile/
  index.html              # viewport guard + app shell bootstrap
  css/mobile.css          # M3 tokens (shared subset) + mobile-first layout
  js/core.js              # EXTRACTED shared: config, Auth, http, api, ICONS/icon, helpers
  js/i18n.js              # copied verbatim from octbase-frontend
  js/app.js               # mobile SPA: router, views, components, event dispatch
  locales/en.json,de.json # copied from octbase-frontend (same keys/copy)
  fonts/                  # IBM Plex Sans woff2 (copied)
  img/                    # favicon / logo (copied)
  Containerfile           # mirrors octbase-frontend serving
  caddy/Caddyfile         # mirrors CSP + /api reverse proxy
  README.md               # scope, dev, handoff, what's intentionally omitted
```

---

## 2. Scope — what the mobile app does (and deliberately does not)

### ✅ Included (the core day-to-day phone flows)
1. **Sign in / sign out** (+ accept-invitation deep link is fine to defer; sign-in is required).
2. **My Work (dashboard):** assigned-to-me, in-review, my projects — as tappable cards.
3. **Projects list** → open a project.
4. **Board:** a phone-friendly board. Columns are **not** side-by-side; use a **column switcher**
   (segmented control / chips) that shows one column's cards at a time as a vertical card list, with
   per-card quick **status change** (advance / move via bottom sheet). Horizontal column scrolling is
   acceptable only if it stays smooth and obviously scrollable; the switcher is preferred.
5. **Backlog / task list:** vertical **card list** with filter chips (status/priority/type) in a
   bottom sheet.
6. **Task detail (full-screen):** title, status, priority, type, assignee, description (rendered),
   comments (read + add), basic metadata. Editing the core fields (status/priority/assignee) inline
   via bottom sheets.
7. **Create task:** full-screen single-column form (title, type, priority, description, assignee).
8. **Search:** project/global task search with a results card list.
9. **Notifications:** list, mark-read / mark-all-read, unread badge on the bottom nav.
10. **Profile sheet:** current user, language switch (en/de), sign out, "Open desktop version".

### ⚠️ Reduced / view-only on mobile
- **Sprints, Releases, Pages, Activity:** show **read-only** summaries where cheap; otherwise link to
  "Open on desktop". No sprint planning, no release management, no AsciiDoc page editing on phone.
- **Comments:** add + read. Editing/deleting others' comments can be deferred to desktop.

### ⛔ Intentionally omitted on mobile (offer "Open on desktop")
- Admin / User Management, Audit Logs.
- Repository connections & branch management, board **configuration** (columns/external columns).
- Bulk task operations, drag-and-drop reordering, attachments **upload** (view/download links ok).
- Rich AsciiDoc/markdown **authoring**, multi-pane layouts.

Cutting these is a feature, not a gap — document each omission in `README.md` with the desktop path.

---

## 3. Mobile UI system

Reuse `:root` tokens from desktop (`--md-*`, `--space-*`, shape, elevation, IBM Plex Sans). Add a
small set of **mobile-only** tokens and components:

- **Tokens:** `--app-bar-h: 3.5rem`, `--bottom-nav-h: 3.5rem`, `--touch: 2.75rem`(min 44–48px),
  `--sheet-radius: var(--md-xl)`, plus `env(safe-area-inset-*)` padding on the app bar and bottom nav.
- **App bar (top):** sticky, title + contextual back button + 1 trailing action (search/profile).
- **Bottom navigation:** fixed, 4–5 destinations (My Work · Projects · Search · Notifications ·
  Profile) with icons + labels; active state in `--md-primary`; unread badge on Notifications.
- **Bottom sheet:** for filters, status/priority/assignee pickers, profile, confirmations — slides up,
  rounded top, scrim, swipe/scrim-tap to dismiss, focus-trapped.
- **Cards:** task cards (type glyph, title, status & priority chips, assignee avatar, due hint),
  project cards, dashboard section cards. Whole card is the tap target.
- **Full-screen detail / form** screens replace desktop side panels and modals.
- **FAB or app-bar `+`** for the primary create action on board/backlog.
- **States:** every list has a friendly empty state, a spinner loading state, and an error state with
  retry — at phone size.

All HTML is generated by `app.js`; CSS class names in `mobile.css` must match exactly what `app.js`
emits. Reference tokens — no new hardcoded hex/spacing/radius values.

---

## 4. Deliverables

1. `31_octbase-mobile.md` — this document.
2. `octbase-mobile/` — the working mobile-first SPA per the file layout above.
3. `octbase-mobile/README.md` — scope, what's omitted (with desktop equivalents), local dev/test,
   the 768 px handoff contract, and deployment notes.

---

## 5. Acceptance criteria

- On a **390 px phone** viewport the mobile app loads, signs in, and the core flows (My Work →
  project → board → open task → change status → create task → search → notifications) all work with
  **no horizontal overflow** and ≥ 48 px touch targets.
- On a **≥ 768 px** viewport the mobile entry **hands off to the desktop app** (no mobile UI shown);
  rotating/resizing across 768 px switches experiences.
- The desktop app (`octbase-frontend`) is **unchanged** and not regressed.
- Visual language matches desktop (M3 green tokens, IBM Plex Sans, shared icon set, shared i18n copy).
- Omitted features are clearly surfaced as "Open on desktop", never as dead ends or broken UI.
- Accessibility: icon buttons labelled, focus visible, color not sole signal, sheets focus-trapped.
- Mobile app talks only to `/api/v1` (no new endpoints); backend remains authoritative for permissions.

## 6. Out of scope
- Any API/database/backend change. Any change to `octbase-frontend` source (beyond the documented,
  opt-in redirect snippet that the operator may choose to add). Native packaging (PWA/installable is a
  nice future follow-up, not required here).
