# Octbase SPA — frontend JS architecture

The app is a vanilla-JS single-page app built from **ES modules by Vite**
(`docs/architecture.md` §5.2). It was originally one 5,866-line `app.js`; it is
now split into cohesive modules whose top-level declarations are **file-private
by default**, with each file's public surface published through a single
`export { … }` block at the bottom — see "File scope & exports" below.

**Plain DOM has not changed and is not up for revision.** The build changes how
files are joined and checked, not how the UI renders: no framework, no JSX, no
client state library, the same `esc()` / `` html`…` `` / `sanitizeRichText`
escaping producers, and the same view registry.

## Why ES modules + Vite — and what deliberately stays classic

The no-bundler stance was retired on 2026-07-30 (decision record:
`docs/architecture.md` §5.2, migration plan:
`prompts/37b_octbase-frontend-build-step.md`). Its central technical objection
was real and had to be answered rather than waved away: **Chrome blocks `import`
over `file://`** (origin `null`, CORS), and two things loaded the app from
`file://` — the **standalone demo**
(`USE_STANDALONE_DEMO_AUTH = location.protocol === 'file:'` in `config.js`) and
the **Playwright suite**. Both are handled, differently:

- the standalone demo ships as a **single self-contained IIFE bundle**
  (`npm run build:standalone` → `dist-standalone/`, config in
  `vite.standalone.config.js`), which has no `import` at runtime and so loads
  from disk exactly as before. Verified against the pre-conversion commit:
  identical behaviour from `file://` in both browser configurations, including
  the two limitations that were already there (plain Chrome blocks the locale
  XHR and the API preflight from origin `null`);
- the Playwright suite now drives the **built** app over HTTP via
  `OCTBASE_UI_URL` (the seam already existed in `tests/conftest.py`), which is
  also closer to what a user gets than `file://` ever was.

Two kinds of file are still **not** in the module graph, each for a stated
reason. There was a third — the vendored DOMPurify and QR-generator UMD builds,
which **37b stage 4 removed** by making them npm dependencies; a UMD file
resolved as *source* takes a different branch of its own wrapper in the dev
server than in the build, but resolved as a *dependency* it goes through Vite's
pre-bundling and behaves identically in both:

| What | Why it stays classic |
|---|---|
| `theme-init.js` | Runs **synchronously in `<head>` before the stylesheet** so the saved theme applies before first paint. `<script type="module">` is always deferred, which would ship a flash of the wrong theme, and inlining it would need `'unsafe-inline'` in the CSP. It stays an external, non-`defer`, non-module script. |
| `docs-init.js`, `user-guide-nav.js` | One `<script>` each on static pages outside the SPA (`/docs.html`, `/user-guide.html`). Bundling them would buy nothing. |

The five shared modules used to be one row here covering all of them. **37b
stage 3 moved three of them out**: `octbase-shared/` is the `@octbase/shared`
workspace package, each SPA imports `i18n.js`, `meta.js` and `richtext.js` by
name (`import { t } from '@octbase/shared/i18n.js'`), and **all ten physical
copies under `js/` — with `scripts/sync-shared.sh` and its drift guard — are
gone**. Editing a shared module means editing the one file in `octbase-shared/`.
The other two shared entries were the vendored libraries, and **stage 4 removed
them from this file entirely**: they are `dompurify` and `qrcode-generator` from
npm now, imported by name like any other module.

## The modules (inventory, no longer a load order)

**Load order is no longer a contract.** `index.html` has one
`<script type="module" src="js/main.js">`; `main.js` imports the module list and
the bundler topologically sorts it, so the numbering below is a reading order
and a map of responsibilities, not something that must match markup. What
replaces the old ordering rule is the **TDZ discipline** in "Adding a module"
below — a load-time read of a not-yet-evaluated binding is the one failure the
old order prevented for free.

| # | File | Responsibility |
|---|------|----------------|
| 0 | `theme-init.js` | applies the saved theme synchronously in `<head>`, before the stylesheet, to avoid a flash of the wrong theme (app-local — deliberately *not* a shared module) |
| — | `@octbase/shared/i18n.js` | translation loader + `t()`, `setLocale()`, the vocabulary overlay |
| — | `@octbase/shared/meta.js` | task/enum metadata + the estimation helpers |
| — | `@octbase/shared/richtext.js` | rich-text sanitizer: `sanitizeRichText` (DOMPurify-backed), `rtSafeHref`, `rtSafeImageSrc`, `looksLikeHTML` |
| — | `dompurify@3.4.12` | npm (MPL-2.0/Apache-2.0) — imported by `richtext.js`; vendored + a classic script until 37b stage 4 |
| — | `qrcode-generator@1.4.4` | npm (MIT) — MFA enrollment QR, imported by `framework.js` and `views-settings.js`; same history |
| 6 | `config.js` | env/config, status/priority/type enums + metadata |
| 7 | `icons.js` | SVG icon set + `icon()` |
| 8 | `auth.js` | `Auth` — session/token state, login/refresh/logout |
| 9 | `http.js` | `ApiError`, `http` (fetch wrapper with 401-refresh retry), `qs()` |
| 10 | `api.js` | `api` — the REST API surface (all `/api/v1` endpoints); `Prefetch` + `prefetchProjectTasks`/`takeProjectTasks` — the request hand-off that lets boot/the router start a view's data before its renderer runs |
| 11 | `router.js` | `router`, `handleRoute()` — hash-based routing |
| 12 | `permissions.js` | `AppPerms` — frontend permission-check helpers |
| 13 | `state.js` | `S` (mutable app state) + state helpers |
| 14 | `registry.js` | `Views` — the view registry the shell renders from (see "The view registry" below) |
| 15 | `delegation.js` | the five event-delegation registries, the `_A*`/`_VAL*`/`_CHK*` argument adapters and the `register*` API. **Imports nothing, by design** — see "Delegation registration" |
| 16 | `framework.js` | DOM/format helpers, `esc`, `html\`\``, `raw`, `initDelegation()`, toast, modal, palette, shortcuts |
| 17 | `realtime.js` | SSE, session heartbeat, idle timeout, notifications |
| 18 | `views-shell.js` | sidebar, topbar, filter bar, content router, dashboard, search, projects, bulk actions |
| 19 | `views-board.js` | board + backlog (the Backlog is config #1 of the task-list engine) |
| 20 | `views-tasklist.js` | configurable **task-list engine** + the Task view (config #2) |
| 21 | `views-task.js` | task panel, details, rich-text editor, preview, lightbox, task actions |
| 22 | `views-agile.js` | releases + sprints; the sprint report panel (burndown + velocity SVGs and the tasks ⇄ effort unit toggle) |
| 23 | `views-stats.js` | project statistics: the PM overview reached from the topbar's chart icon (KPI tiles, distributions, throughput, cycle time, workload, releases) and the effort burndown. Reuses `views-agile.js`'s chart renderers, so it loads after it |
| 24 | `views-content.js` | pages, repositories, activity, archive |
| 25 | `views-mindmap.js` | mindmap view: epics → user stories → tasks → subtasks, nested by each task's `parentId` |
| 26 | `views-crud.js` | create/edit task, project, members; `init()` |
| 27 | `views-settings.js` | personal settings dashboard: language/theme preferences (`internal/dashboard`) + MFA enrollment/management (`internal/security/mfa`) — two backend modules, one page |
| 28 | `admin.js` | admin panel, user management, audit logs |
| 29 | `namespace.js` | `App` facade over the core singletons, and the app's whole deliberate `window` surface (see below) |
| 30 | `bootstrap.js` | `initDelegation()`, `init()` — imported last by `main.js` |

`auth.js`, `http.js`, `api.js`, `router.js` and `permissions.js` were split out
of a single `api.js` that had grown to conflate five unrelated concerns.

**Three files in `js/` are deliberately absent from that table**, because they
belong to the static pages rather than to the SPA and nothing in `main.js`
imports them: `docs-init.js` (`/docs.html`), `user-guide-nav.js`
(`/user-guide.html`) — both classic scripts, see the table above — and
`styleguide-icons.js`, which *is* an ES module (it imports the shipped
`icons.js` so the icon grid cannot drift from the app) and reaches the browser
as `styleguide.html`'s own Vite entry. The load-order/TDZ discipline below is
about the SPA's module graph and does not apply to them.

`main.js` is the entry: it imports every module above for its side effects (view
registration, delegation registration) in the old load order. That order is now
**documentation of intent, not a dependency mechanism** — each module imports
what it actually uses, and those imports are what the bundler sorts by.

Each file has the shape

```js
import { esc, html } from './framework.js';
import { S } from './state.js';

// … declarations (file-private by default) …

export { a, b, c };
```

Imports are one line per source file, alphabetized by module then by name;
exports are one alphabetized block at the bottom. Both shapes are what
`scripts/codemod-esm.mjs` emits, so the conversion stayed mechanical and the
diffs stayed reviewable — keep to them.

## File scope & exports

- **Private by default.** A top-level `function`/`const`/`let` is visible only
  inside its own file. To use a symbol from another file, add it to the defining
  file's `export { … }` block and `import` it where you need it. Keep both lists
  alphabetized; export only what is actually consumed elsewhere — the block *is*
  the file's documented public surface.
- **Module scope is now real, and `window` is not a bus.** Under classic scripts
  an export block landed its names on `window` as a side effect, so anything
  could reach anything by bare name and the Playwright suite got a test surface
  for free. That is gone: an export is module-scoped, and the only names on
  `window` are the ones `namespace.js` (plus `router.js`, `registry.js`,
  `permissions.js`) put there on purpose — see "The `App` namespace". If a symbol
  seems to have vanished at runtime, it was an accidental global; import it
  properly rather than re-publishing it.
- **Cross-file mutable state lives on `S` — one idiom.** Anything that changes
  while the app runs and is read by more than one file is a property on the
  exported `S` state object (`state.js`): `S.bulkInFlight`, `S.boardTasks`,
  `S.appVersion`, …. Give it a default and a comment in `state.js`. The single
  exception is **boot-resolved deployment config**: `FEATURES` and `LIMITS`
  (`config.js`) are exported `const` objects whose properties are overwritten
  once by `loadFeatureConfig()` and are read as constants afterwards — config,
  not state, so it does not live on `S`. Do not add a third pattern (no
  accessor functions over file-private mutables).
- **"Never export a reassigned binding" has retired.** It existed only because
  `Object.assign` **snapshotted** the value at load time, so an exported `let`
  the file later reassigned left external readers stale. ES module exports are
  **live bindings** — importers see the current value — so the whole hazard class
  is gone, along with the guard that policed it. Keep using `S` for shared
  mutable state anyway: one idiom beats two, and an importer cannot write a live
  binding, only read it.
- **Cross-file writes are impossible by construction.** An imported binding is
  read-only for the importer; shared mutable state lives on `S`, where writes are
  property mutations.
- Zero-export files are valid and desirable: `views-mindmap.js` and
  `views-stats.js` (both register their view via `Views.register` at load and
  are reached only through the registry) and `bootstrap.js` export nothing.
- **A handler reached only through delegation is not exported.** It is
  registered by its own file (next section) and called by name from nowhere, so
  it stays file-private. Export it only if another file *calls* it directly.
- **The build enforces most of this now.** `scripts/check-exports.mjs` — a
  hand-rolled reimplementation of `import`/`export` (203 lines of rules over the
  364-line resolver in `scripts/lib/js-scope.mjs`, itself on a 6,304-line
  vendored parser) — was retired with the conversion: an import of a name
  nothing exports is a **build error**, not a lint finding, and the
  reassigned-export rule above no longer describes anything possible. The rules
  file is gone; the resolver and the vendored parser **stay**, because
  `scripts/check-tdz.mjs` below is an ongoing need built on them (37b listed them
  as tooling a bundler deletes outright — that part of its ledger was wrong).
  Two of its five rules needed a replacement rather than a deletion, and got one:
  - *dead public surface* (its rule 4) → the bundler's unused-export detection,
    and the ESLint pass 37b stage 6 adds;
  - *no load-time reference to a later file* (its rule 2) → **`scripts/check-tdz.mjs`**,
    which is strictly stronger: it checks the thing that actually breaks under ESM
    (see "Adding a module"). It runs in CI and in the pre-commit sweep.

## Delegation registration

Markup wires behavior through `data-act` / `data-change` / `data-input` /
`data-keydown` / `data-submit` attributes, never inline `on*=""` (see
"HTML-safety convention"). A single set of document-level listeners, wired by `initDelegation()` in
`framework.js`, dispatches each event to a handler looked up in one of five
registries.

**Every module registers its own handlers.** The registries and the
registration API live in `delegation.js`; modules reach them through it:

```js
registerActions([addLane, showBoardSettings], _A0);   // fn.name is the data-act value
registerActions([renameLane, removeLane], _A1);       // adapter passes data-a0
registerChanges({                                     // bespoke: raw (el, ev)
  toggleSprintRow: node => { … },
});
```

- Two forms, one call. A **list of functions** is keyed by each function's own
  `.name`, with an adapter mapping the element's `data-a0`/`a1`/`a2` (or
  `.value`/`.checked`) onto its positional arguments: `_A0`…`_A3`, `_VAL`,
  `_VAL0`, `_CHK`, `_CHK0`. A **`{ name: (el, ev) => … }` object** registers
  handlers that want the element and event themselves.
- `registerActions` / `registerChanges` / `registerInputs` / `registerKeydowns`
  / `registerSubmits` — one per registry, all exported by `delegation.js`.
- **Where it goes:** one block per file, directly above the export block, under
  a `── Delegation registration ──` header. Same convention as the export
  block: one place per file that states what the file publishes.
- Registering a name that is already taken logs a `[delegation] duplicate
  handler name` warning — with registration spread across modules, nothing else
  would notice the later `<script>` silently winning.
- **A `data-*` attribute naming an unregistered handler is reported, not
  ignored.** `_dispatch` logs `[delegation] no <kind> handler registered: <name>`.
  It used to do nothing at all, and that silence cost a full session: Vite's
  minifier renamed the top-level functions the registry is keyed by, which
  unregistered every array-form handler at once and surfaced only as dead
  buttons. (The rename itself is prevented by `rollupOptions.output:
  { keepNames: true }` in the Vite config — it must sit on the rolldown output
  options, because Vite 8 no longer uses esbuild and the older
  `esbuild: { keepNames: true }` is a silent no-op there; see the config's
  comment.)
- **`delegation.js` must not grow an import.** It was split out of
  `framework.js` precisely so it depends on nothing: the `_A*` adapters are
  `const` arrows that seven view modules read *at load time*, and `framework.js`
  sits in an import cycle. A module with no imports always evaluates first, so
  those bindings can never be read from their temporal dead zone. Under classic
  scripts this was merely tidy; under ES modules it is what keeps the app from
  throwing `ReferenceError` at boot. `registry.js` and `env.js` are
  dependency-free for the same reason.
- The plain-Node test harness (`testutil.js`) stubs all five registrars, so a
  unit test loading a view module needs no boilerplate for them.

### Adding a module

- **Import what you use.** There is no load order to slot into: add the file to
  `main.js`'s side-effect import list if it registers something at load time
  (a view, delegated handlers), and let its own `import` statements carry its
  dependencies.
- **The one real hazard is a load-time read across a cycle (TDZ).** ESM tolerates
  import cycles for *function* references — declarations hoist and bindings are
  live — but a **top-level** read of a `const`/`let`/`class` from a module that
  has not finished evaluating throws `ReferenceError` at boot. Nothing about this
  is a build error, so it is checked explicitly:

  ```bash
  node scripts/check-tdz.mjs          # exits non-zero on a hazard
  ```

  A load-time cross-module read is safe in exactly two cases — the source module
  **imports nothing** (so it always evaluates first), or the binding is a
  **function declaration** (hoisted). Anything else is a hazard. This is what
  replaced the old "load-time refs may only point at earlier files" rule, and it
  is the reason `delegation.js`, `registry.js` and `env.js` stay import-free.
- `bootstrap.js` is imported last by `main.js`: it calls `initDelegation()` and
  `init()`, which assume every module has already registered its handlers.
- Every asset is cache-busted by its **filename**, including the classic scripts
  outside the bundle — see "Cache-busting". There is nothing derived in
  `index.html` and nothing to stamp.

## Testing (unit layer — Vitest)

Pure logic in these files is unit-tested with Vitest + `node:assert`. Run them:

```bash
npm run test:unit                    # whole layer
npm run test:unit -- state.test.js   # one file
```

CI runs the same command in the "Frontend checks" job. **These tests complement
the Playwright suite, they never replace it** (`docs/architecture.md` §6): they
assert pure logic, and anything DOM-coupled is exercised in a real browser.

**Two shapes live here, and the difference is worth understanding before adding
a test.**

*Real import* — `import { rtSafeHref } from '@octbase/shared/richtext.js'`. Vite
resolves the specifier exactly as the app does, so the test runs the shipped
module with nothing in between. Use this whenever the code under test needs no
stubbed collaborators. A file that needs browser globals declares
`// @vitest-environment jsdom` on its first line and gets a real DOM instead of
a hand-built fake one. `richtext.test.js`, `state.test.js` and `i18n.test.js`
are this shape.

*Harness* — `loadModule('framework.js')` (`testutil.js`) still evaluates **one**
file in a `node:vm` context whose global object is a fake `window`, rewriting
the module syntax on the way in (`toClassicScript`): `import` lines are dropped,
so those names resolve as globals off the fake window and a test can stub them
through `globals`; the `export { … }` block becomes the window assignment it was
converted from. The eight view-module tests still use it, because what they
actually rely on is **substituting collaborators** — a fake `el()`, a stub
`Views.register`, a two-line `esc` — and rewriting that as `vi.mock()` calls is a
rewrite of the tests, not a port of them. It buys nothing the tests do not
already have, so it has not been done.

**When 37b stage 7 adopted Vitest it deliberately did not finish the job**, and
this is the honest state: the shim survives for the files that stub, and the
files that do not stub no longer pay for it. If you convert one of the remaining
eight, convert it because you are changing the test anyway.

One trap the shim keeps: it **throws** on an import shape it cannot rewrite, and
it is imported by every harness-based file, so a single new import form takes out
that whole set at once. Read `testutil.js` before introducing one. The shapes it
rewrites today are a sibling module (`./x.js`), a module of the shared package
(`@octbase/shared/x.js`), and — since 37b stage 4 — a bare npm package
(`dompurify`, `qrcode-generator`), whose name then resolves to whatever stub the
test put on the fake window rather than to the real package.

Covered today: `richtext.js` URL guards (`rtSafeHref`, `rtSafeImageSrc`,
`looksLikeHTML` — the client mirror of the server's `sanitize.go`),
`framework.js` `esc`, `state.js` filtering (`applyTaskFilters`,
`filterTasksBySearch`), `views-board.js` drag geometry (`dropSlot`) and its
pending paint (`boardColsInner` with no cards yet — the lanes must not claim a
count or show anything card-shaped while the cards are in flight), and
`views-task.js` `activityMessage()` — that last one loads the real i18n module
against the real `locales/*.json` and reads the activity vocabulary out of
`octbase-api/internal/`, so a backend `activity.Write` added without a
translation fails the build rather than warning in a user's console. The
completion warning is covered across three files on purpose — the subtree walk
and its wording in `views-task.test.js`, the Done-lane drop in
`views-board.test.js`, the bulk action in `views-tasklist.test.js` — because
what makes it worth having is that it is on *every* door that completes a task,
and a door left silent is exactly the regression a single-file test would miss.
**Out of scope by construction:** `sanitizeRichText`
needs DOMPurify against a real DOM — the Playwright e2e suite covers it.

`testutil.js` and `*.test.js` are **dev-only**: nothing imports them from
`main.js`, so the bundler never reaches them and they cannot ship.

## Type-checking (JSDoc + generated API types)

There are no `.ts` files and nothing is compiled. `npm run typecheck` runs
`tsc --noEmit` over the files that opt in with a **`// @ts-check`** line at the
top — currently `http.js`, `api.js`, `state.js`. Adding that one line is how a
file joins; `checkJs` is off globally so a file's imports are not dragged in
with it, which is what keeps this expandable one file at a time.

What it buys is the API contract. `octbase-frontend/types/openapi.d.ts` is
generated from `octbase-api/api/openapi.yaml` by `npm run types:generate` and
**committed**; `types/api.d.ts` gives the schemas readable names
(`Project`, `Task`, …) so app code annotates with
`/** @type {import('../types/api').Project[]} */` instead of reaching into the
generated file. `state.js` types every API-shaped field of `S` that way, and
`api.js` annotates the reads whose shape views destructure.

The check is real, not decorative: renaming a field in the spec and regenerating
turns every site that reads it into a compile error. That was the failure mode
`docs/architecture.md` §5.1 condition 2 described — a Go `json:` tag change
breaking the SPA silently — and it is the one benefit of the build that nothing
else could have delivered.

CI runs two steps for this. **Freshness**: regenerate and `git diff
--exit-code`, so the committed types cannot drift from the spec (the mirror of
the backend's `internal/apicontract` route↔spec parity test). **Type-check**:
`npm run typecheck`. If the freshness step fails, run `npm run types:generate`
and commit the result — never hand-edit `openapi.d.ts`.

Every field in the generated schemas is optional, because the spec marks few of
them `required`. That is weaker than it looks in one direction (it will not
catch "this can be absent") and exactly right in the other: it catches a field
that has been renamed or removed, which is the change a Go struct tag actually
produces.

## The view registry (core vs. modules)

Files 1–16 plus `namespace.js`/`bootstrap.js` are the **core** (config, auth,
HTTP, routing, state, DOM framework, shell chrome); the `views-*` files and
`admin.js` are **view modules**. The shell has no per-view branches: each
module registers its views with `Views.register(id, entry)` (`registry.js`)
at load time, and the shell renders from the registry —

- `renderSidebar` builds the project nav from `Views.sidebarEntries()`
  (icon/label/shortcut/`order`, plus `when` for conditional entries like the
  sprint board);
- `renderContent` dispatches to the entry's `render`;
- `viewCreateButton`/`contentToolbar` use the entry's `createButton` and
  `listToolbar`;
- `setFilter`/`setSearchFilter` call the entry's `refreshList` for
  focus-preserving in-place refreshes;
- the router's `resolveProjectView` uses `Views.resolve` — unknown or
  feature-disabled views fall back to the entry's `fallback` (default the
  board);
- the router calls the entry's `prefetch(projectId)` *before* `loadProject`,
  so the view's own request travels with the project bundle instead of a round
  trip behind it; `render` collects it with `Prefetch.take` (`api.js`) and
  falls back to fetching itself when it was reached without one.

The full entry contract is documented in the `registry.js` header. **To add a
view**: write its `render*` function in a module file and register it there —
no shell edit. A deployment-gated view supplies `enabled` (e.g.
`() => FEATURES.taskView`) and vanishes everywhere at once when the flag is
off. Registration happens as a side effect of `main.js` importing the module,
and handlers resolve by their function's own `.name` through the delegation
registry (see below) — not through any shared global scope.

## The `App` namespace

`namespace.js` exposes `window.App`, an explicit, read-only facade over the core
singletons and state (`App.api`, `App.http`, `App.auth`, `App.router`,
`App.state`, `App.perms`, `App.views`, `App.admin`). Prefer it from the console
and from new code.

**Under ES modules this file inverted its purpose, and that is worth
understanding before adding to it.** It used to be a tidier alternative to a
window namespace that already existed by accident — every classic-script export
block published its names on `window` as a side effect. Now module scope is the
default and *nothing* reaches `window` unless a file puts it there, so
`namespace.js` is where the few genuinely global names are created on purpose:

- `window.App` — the documented devtools facade, this file's reason to exist;
- `window.S` — the Playwright suite asserts on app state through it;
- a short, commented list of **five module internals the Playwright suite calls
  directly** (`setView`, `openTaskPanel`, `renderDescriptionHTML`, `dropSlot`,
  `renderMfaChallengeStep`). They were ambient globals for free before; now they
  are an explicit test affordance, listed with their callers. **No app code may
  read them off `window`**, and nothing the `App` facade already reaches belongs
  here — a test wanting the REST client uses `App.api`.

`window.router`, `window.Views` and `window.AppPerms` are published by the
modules that own them and were already explicit before the conversion.
`window.t` / `window.getLocale` are published by `namespace.js` too, and listed
there with the reason: they used to arrive for free from the shared `i18n.js`
while it was a classic script, and since 37b stage 3 made it a real ES module
the SPA that wants them global has to say so. Do not grow this surface casually: `dropSlot` and
`renderDescriptionHTML` are pure functions whose natural home is the `js/*.test.js`
unit layer, and moving them there would let the list shrink (37b stage 7).

## HTML-safety convention (enforced)

Rendering assigns HTML strings to `.innerHTML`. That is safe **only** because
every dynamic value is routed through an escaping/trusted producer:

- `esc(x)` — HTML-escapes user/server data
- `html\`…\`` — auto-escaping tagged template (interpolations escaped unless
  wrapped in `raw()`)
- `sanitizeRichText(…)` — the rich-text allowlist sanitizer (`richtext.js`, a
  shared module backed by the vendored DOMPurify; the Octbase policy — tag/
  attribute allowlist and strict href/src validation mirroring the server —
  lives in its config + hook)
- `icon()`, `t()`, and `*Inner()` / `*Html()` render helpers, which return
  already-escaped HTML

**Never** splice raw user-content fields (`.title`, `.name`, `.text`,
`.description`, `.email`, `.filename`, …) straight into a template — wrap them in
`esc()` or use `html\`\``.

`scripts/check-innerhtml.mjs` (repo root) enforces this in CI over **both** SPAs:
it fails on `innerHTML +=`, string concatenation into `innerHTML`,
`document.write`, and unescaped interpolation of known user-content fields. Run
from the repo root with:

```bash
node scripts/check-innerhtml.mjs
```

## Cache-busting

Every asset ships under a content-hashed filename, so its URL changes if and
only if its bytes change: no stale assets, and unchanged assets keep their URL
and stay cached. Caddy serves `/assets/*` as `immutable` for a year on that
basis.

Vite does this for everything in the module graph. The classic scripts
*outside* it — `theme-init.js` (must run before first paint, and the CSP forbids
inlining it), `docs-init.js` and `user-guide-nav.js` — are hashed by
`scripts/vite-hash-classic-assets.mjs`, which emits each as
`assets/<name>-<hash>.js` and rewrites the `<script src>` in every HTML entry.

**Adding a classic (non-module) script:** add the `<script src="js/…">` tag and
add a matching entry to `CLASSIC_ASSETS` in that SPA's `vite.config.js`. Miss
the second half and the file is not copied into `dist/` at all, so the page
404s on it as soon as the build is served. (A module — `import`ed rather than
tagged — needs none of this.)

The `file://` standalone build deliberately does **not** hash: there is no HTTP
cache to bust on disk, and stable names are what make the copied folder
re-openable.

Until 37b stage 5 this worked the other way round: a `?v=<sha256>` query stamped
into the HTML by `scripts/stamp-assets.py`, kept correct by a pre-commit hook, a
post-merge hook, a `merge=stamphtml` merge driver and a CI drift check — about
140 lines of tooling plus three pieces of git configuration to imitate what the
bundler already did for everything else. All of it is gone, and with it the
spurious merge conflicts on hash-only lines: `index.html` now holds no generated
content at all.

**One-time per clone:** `bash scripts/setup-git.sh` sets `core.hooksPath` (it
lives in `.git/config` and can't be committed).

## Configuring views & features (the task-list engine)

`views-tasklist.js` is a **configurable task-list engine** that both the Backlog
and the Task view are built on — two configs of one engine, so they cannot drift
apart. It is **foundational, not optional**: the Backlog delegates to it, so it is
never removed. What *is* optional is the **Task view** (the management layer:
status-grouped, cross-cutting, with bulk "Set status").

- **The config contract** lives in the `views-tasklist.js` header. A config object
  supplies `{ listId, scope, cache, header, group, row, emptyState }`; the engine
  (`renderTaskList` / `refreshTaskList`) has no view-specific branches. The
  Backlog's config is `backlogListConfig()` (`views-board.js`); the Task view's is
  `taskViewConfig()`. To add another list view, write a config and a `render*`
  entry — don't fork the engine.
- **The feature flag.** `FEATURES.taskView` (`config.js`) gates the Task view only.
  Its source of truth is the **backend**: `GET /api/v1/config` returns
  `{features:{taskView}}`, driven by the env var **`OCTBASE_FEATURE_TASKVIEW`**
  (default **on**; set to `false` to hide it). The SPA fetches it once at boot
  (`loadFeatureConfig`, called from `initApp`). A URL param **`?taskView=on|off`**
  overrides it for tests/previews (the Playwright suite uses this to exercise both
  states without changing backend env).
- **The seams.** The gate lives in one place: the Task view's registry entry
  (`views-tasklist.js`) declares `enabled: () => FEATURES.taskView` and
  `fallback: 'backlog'`, and the registry-driven shell honours it everywhere —
  sidebar entry, content dispatch, toolbar filters, search refresh, and the
  router fall-back (a disabled/unknown `/tasks` URL goes to the Backlog instead
  of a blank pane). Still hand-guarded (view-conditional chrome, not dispatch):
  the bulk "Set status" control (`updateBulkBar`), the status filter
  (`renderFilterBar`), and the status-filter reset when leaving the view
  (`setView`) — all keyed on `S.view === 'tasks'`. Navigation between the
  Backlog and the Task view is the sidebar (no in-content switch).
- **To disable the Task view in a deployment:** set `OCTBASE_FEATURE_TASKVIEW=false`
  in the API's environment. No frontend change, no dead UI: the sidebar entry, the
  switch and the route all disappear, and a stale `/projects/:id/tasks` URL falls
  back to the Backlog.

## Open follow-ups

- Extend the `html\`\``-migration ratchet: `realtime.js`, `views-mindmap.js`
  and `views-settings.js` here, plus the whole of `octbase-mobile/js/app.js`,
  are fully migrated to `html\`\``-tagged templates (every interpolation escaped
  by construction; trusted fragments opt out via `raw()`), and
  `scripts/check-innerhtml.mjs` lists them in `STRICT_FILES` so an untagged
  `innerHTML = \`…\`` can't creep back in. Migrate the remaining files a bounded
  set at a time, adding each to `STRICT_FILES` — entries are never removed.
  `STRICT_FILES` entries are app-scoped repo-relative paths (`octbase-frontend/js/…`),
  not bare basenames: both SPAs have same-named files.
- **The 37b migration is mid-flight** — this SPA is converted (stage 2),
  `octbase-shared` is a real package (stage 3, which also brought the
  multi-stage container builds forward because an unbundled image could no
  longer resolve its own imports), DOMPurify and the QR generator are pinned npm
  dependencies (stage 4, which also removed the last two classic `<script>` tags
  the module graph had to work around), and the `?v=` stamping machinery is
  retired in favour of content-hashed filenames (stage 5 — see "Cache-busting").
  The stages still open are: CI rewiring (6), and generated OpenAPI types plus a
  Vitest layer (7). Plan and status:
  `prompts/37b_octbase-frontend-build-step.md`; decision record:
  `docs/architecture.md` §5.2.
