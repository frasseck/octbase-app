# octbase-mobile

A **mobile-first companion** front end for Octbase. It gives phone users a native-feeling,
thumb-driven experience for the core day-to-day flows, while **tablets and desktops keep using the
full desktop app** (`octbase-frontend/`). It is an **enhancement, not a replacement** — the desktop
app is untouched and both talk to the same backend (`octbase-api`, `/api/v1`).

> Spec / design rationale: see [`../prompts/31_octbase-mobile.md`](../prompts/31_octbase-mobile.md).

## Activation — force phones to mobile, everyone else to desktop

Device routing is done **server-side in the desktop app's Caddy front door**
(`octbase-frontend/caddy/Caddyfile`), same origin, mobile served under `/m/`:

- **Phones** (UA matches `iphone|ipod|android.+mobile|…`) hitting `/` get a `302` to **`/m/`** (mobile app).
- **Everything else** — desktops, laptops, and **tablets incl. iPad** (their UAs omit the `Mobile`
  token) — stay on the desktop app at `/`. A non-phone landing on `/m/` is bounced back to `/`.
- `/api/*` is **never** device-redirected, so the mobile app shares the same-origin API + JWT cookies.

Wiring (already committed):
- `octbase-frontend/caddy/Caddyfile` — `@phoneEntry` / `@desktopOnMobile` User-Agent matchers, the
  `redir`s, and `handle_path /m/* { reverse_proxy octbase-mobile:8080 }`.
- `podman-compose.yml` — adds the `octbase-mobile` service; the `octbase-frontend` front door
  `depends_on` it.

Deploy: `podman-compose up -d --build` (or `docker compose …`). Then open the site root on a phone →
you land on `/m/`; on a desktop → you stay on `/`.

**Preview the mobile app on a desktop:** open the browser's device-emulation (it sends a phone
User-Agent) and reload `/` — you'll be routed to `/m/`. Requesting `/m/` directly from a real desktop
UA bounces you to `/` by design.

Verified locally (Caddy 2.6 mirror): phone-UA `/`→`302 /m/`, desktop/iPad/Android-tablet UA `/`→`200`,
desktop-UA `/m/`→`302 /`, `/api/*` passes through for every UA, and a phone-UA browser boots the
mobile SPA at `/m/` and logs in through the proxied origin.

## Runs on every viewport

The mobile UI loads on **all** screen sizes. It is authored mobile-first for a 360–430 px phone; on
tablet/desktop widths it presents as a **centered phone-width frame** rather than stretching
edge-to-edge (see the `@media (min-width: 768px)` block in `css/mobile.css`).

> **History:** an earlier build had an automatic device handoff — `< 768px` ran the mobile app and
> `≥ 768px` was redirected to the desktop app (`octbase-frontend`). That redirect was **removed on
> request** so the mobile version is usable everywhere. To re-introduce per-device routing later, an
> operator can add the optional snippet below to the desktop app instead. `DESKTOP_URL` (used by the
> "Open on desktop" links) is still configurable via `?desktop=…`, and the API base via `?apiBase=…`
> — but both overrides are **dev-only**: they are honored solely from `file://` or a loopback host,
> and `?desktop=` additionally accepts only http(s)/file targets (the value lands in `href`s, so a
> `javascript:` URL would otherwise execute). On a deployed origin both parameters are ignored.

## What it does

**Included:** sign in/out · My Work (dashboard) · projects list · board (column switcher + per-card
move) · backlog / task list with filters · full-screen task detail (status / priority / assignee via
bottom sheets, comments) · create task · search · notifications (+ unread badge) · profile sheet
(language, sign out, open desktop) · **Settings page** (language/theme preferences, TOTP MFA
enrollment/disable/recovery codes — reached from the profile sheet) · the **completion warning**
(marking a task DONE over open work below it asks first, at both the status sheet and a move into a
Done column — the phone half of the desktop's three-door warning).

Sheets come in two shapes: `openSheet()` paints and returns, and `confirmSheet()` returns a promise
that resolves true only on its confirm button — dismissal by scrim, Escape, or another sheet opening
over it all settle it false, via `closeSheet()`. Reach for the second whenever an action needs an
answer before it writes.

**Intentionally omitted on phone** (each offers *"Open on desktop"* — never a dead end):
admin / user management, audit logs, repository & branch management, board **configuration**
(columns), bulk operations, drag-reorder, attachment upload, AsciiDoc/rich-text **authoring**, sprint
planning & release management (these are reduced to read-only summaries or links). Cutting them is
deliberate — a smaller, sharper phone app beats a cramped full port.

## Architecture

Plain-DOM SPA — no framework, no JSX, no client state library. Hash-routed; all markup generated
in JS. Since **37b stage 2** it is built from **ES modules by Vite** (`vite.config.js`), so it is
no longer servable straight from the source tree: `index.html` loads one module entry and the
imports resolve through the npm workspace. Decision record: `docs/architecture.md` §5.2.

```
index.html              viewport guard + one <script type="module" src="js/app.js">
css/mobile.css          M3 green tokens (shared with desktop) + mobile-first layout
js/app.js               entry — mobile view layer: router, views, components, flows
js/core.js              shared infra extracted from octbase-frontend: Auth, http, api, icon set,
                        sanitizer, event-delegation primitives, toast
js/theme-init.js        the one classic script — runs synchronously in <head> before first paint
locales/*.json          en/de (a reduced set + a small `mobile.*` namespace) — NOT shared
fonts/ img/             IBM Plex Sans + favicon/logo
vite.config.js          site build → dist/
vite.standalone.config.js  self-contained IIFE bundle → dist-standalone/ (the file:// demo)
Containerfile           Vite build stage, then Caddy serving dist/ (mangling OFF)
caddy/Caddyfile         same CSP + /api reverse proxy
```

The i18n engine, task-enum metadata and rich-text sanitizer are **not** in this directory — they
come from the `@octbase/shared` workspace package (`import { t } from '@octbase/shared/i18n.js'`),
one copy for both SPAs since 37b stage 3. The byte-identical copies that used to sit in `js/`, and
the sync script and drift guard that maintained them, are gone. Locales and icons stayed per-SPA on
purpose: the phone app ships a smaller string set and a smaller glyph set.

Auth is the same JWT-in-memory + httpOnly refresh-cookie model as desktop. `file://` opens in
standalone demo mode (auto sign-in as the seeded demo user) — that is what `dist-standalone/` is
for, since a browser refuses `import` from a `file://` origin. The Playwright mobile suite loads
that artifact, because `file://` is the code path it exists to exercise.

> ⚠️ The `data-act` event dispatch keys handlers by `fn.name` — identifier **mangling must stay
> OFF**. `vite.config.js` sets `esbuild: { keepNames: true }` for exactly this reason; removing
> that line is not a size optimization, it silently kills every delegated tap in the app.

## Local development

Node ≥ 22. All commands run from the **repository root** — this app is one workspace of three.

```bash
npm ci                                      # once

npm run build --workspace @octbase/mobile   # → dist/ (site) and dist-standalone/ (file:// demo)
npm run preview --workspace @octbase/mobile # serves dist/ on :4174
```

Then open `http://localhost:4174/`. `vite preview` reverse-proxies `/api` to
`http://127.0.0.1:8000`, so the previewed app is **same-origin with its API** the way the deployed
one is (in production the desktop Caddy serves this app under `/m/` and proxies `/api`). That
matters: the session lives in an HttpOnly refresh cookie, which a cross-origin frontend never gets
back. Point it at a stack on another port with `OCTBASE_API_ORIGIN`.

`npm run dev --workspace @octbase/mobile` gives hot reload but has **no** `/api` proxy — fine for
markup and styling, not for anything that exercises a real session.

Opening `dist-standalone/index.html` via `file://` also works (standalone demo auth). Opening the
**source** `index.html` from disk does not, and no longer can: a browser refuses `import` from a
`file://` origin, which is precisely why the standalone bundle is built as a self-contained IIFE.

## Deployment

Built and served exactly like `octbase-frontend`: a Vite build stage produces `dist/`, which a Caddy
container serves with `/api/*` reverse-proxied to `octbase-api`. The image ships the built output,
not the source tree — an unbundled source tree would 404 on its own imports.

> ⚠️ The **build context is the repository root**, not this directory:
> `podman build -f octbase-mobile/Containerfile .` — the app imports `@octbase/shared`, which lives
> outside here. `podman-compose.yml` already sets it correctly.

Deploy at its own host (e.g. `m.example.com`) or behind a path and set `DESKTOP_URL` by editing
`core.js` to the desktop app's URL (`?desktop=` is a dev-only override — it is ignored on deployed
origins, see above).

**Optional** (not enabled, keeps desktop unchanged): to auto-send phones from the desktop app to the
mobile app, the operator may add to `octbase-frontend/index.html`:

```html
<script>
  if (window.matchMedia('(max-width: 767px)').matches) location.replace('/m/');
</script>
```
