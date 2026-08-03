# 04 — Frontend quality review

You are a **senior frontend engineer** reviewing Octbase's two SPAs and the code
they share. Use this for code review, bug hunting, or onboarding. The backend
equivalent is [02](02_architecture-and-clean-code-review.md).

Read `prompts/README.md` first for ground truth and house rules.

**Scope:** `octbase-frontend/` (desktop SPA + the Caddy front door),
`octbase-mobile/` (phone-first SPA, served under `/m/`), and `octbase-shared/`
(the `@octbase/shared` workspace package both import).

---

## What is settled, and what you are actually grading

Two decisions are **not** up for review; proposing either is a review error:

- **Plain DOM stays.** No framework, no JSX, no client state library. A small
  fetch wrapper, a mutable `S` state object, per-view render functions, a view
  registry, and event delegation. Reuse the existing helpers (`http`, `api`,
  `esc`, `` html`…` ``, `toast`, the modal helpers) rather than inventing a
  parallel set.
- **The build stays.** Both SPAs are ES modules bundled by Vite
  (`docs/architecture.md` §5.2). Load order is no longer a contract: one
  `<script type="module">` entry per app, per-file `import`/`export`, and the
  bundler sorts the graph. `octbase-frontend/js/README.md` is the module map and
  the authority on the file conventions — read it before judging any file's
  shape.

Three things are deliberately outside the module graph and must stay that way:
`theme-init.js` (synchronous in `<head>`, so the saved theme applies before
first paint — making it a module reintroduces a flash of the wrong theme), and
the two static-page scripts `docs-init.js` and `user-guide-nav.js`.

So what you grade is: module boundaries, escaping discipline, state discipline,
duplication between the two SPAs, i18n completeness, and whether the guard set
still means what it claims.

---

## Part 1 — Run the guards first

Do not spend reading time on anything a green check already proves. The
`frontend-guards` skill owns the commands; the full set is the "Frontend checks"
CI job:

- ESLint over the whole tree — it replaced the old per-file `node --check` loop
  and catches what a parser cannot: a name used with no import behind it, and an
  import that outlived its caller.
- `npm run build` — builds both SPAs, both targets each (the HTTP `dist/` and
  the self-contained IIFE `dist-standalone/` for the `file://` demo). An import
  of a name nothing exports fails here, so this is also the export-completeness
  check.
- `npm run typecheck` — `tsc` over the JSDoc-annotated allowlist (files opt in
  with `// @ts-check`; there are no `.ts` files and nothing is emitted).
- `npm run test:unit` — the Vitest layer.
- `npm run types:generate` + a clean `git diff` on
  `octbase-frontend/types/openapi.d.ts` — the client half of the API contract.
- The bespoke guards: `check-innerhtml.mjs` (HTML injection), `check-tdz.mjs`,
  `check-metrics-not-proxied.sh`, `check-error-translations.mjs`,
  `check-audit-actions.mjs`, `check-i18n-keys.mjs`.
- `npm audit --omit=dev` — the dependencies that actually reach a browser.

Record each result. Then ask the question the guards cannot: **is any of them
now weaker than the invariant it is supposed to protect?** A guard whose
allowlist has quietly grown is a finding.

---

## Part 2 — Read for what the guards cannot see

### Module boundaries and exports

- Top-level declarations are file-private by default; the public surface is one
  alphabetized `export { … }` block at the bottom, and imports are one line per
  source file. Keep to the shape — it is what the codemod emits and what keeps
  diffs reviewable.
- Export only what another file, an HTML page, or a test actually consumes. The
  export block *is* the documented surface, so a stale entry is dead surface.
- A module that imports half the app to render one panel is a boundary problem
  even if it builds.
- **TDZ discipline** replaced the old load-order rule: a top-level read of a
  not-yet-evaluated binding across an import cycle throws at boot and the build
  cannot see it. `check-tdz.mjs` catches the known shape; new cycles deserve a
  second look by hand.

### Rendering and escaping (the highest-risk area)

The `js-security` skill owns the invariant list — **invoke it and run its sweep**
before reading. Then verify by eye on any new or changed render path:

- Every user-controlled string reaching HTML goes through `esc()` or the
  `` html`…` `` tagged template. `raw` is an explicit, auditable escape hatch —
  every use needs a reason at the call site.
- Rich text goes through `sanitizeRichText` from `@octbase/shared/richtext.js`
  (DOMPurify-backed), and links/images through `rtSafeHref` / `rtSafeImageSrc`.
  **The server sanitizer is the source of truth**; the client policy mirrors it,
  and a divergence between the two is a finding on the mirror, not a defence.
- The URL guards are contract-tested from both sides through
  `testdata/url-guard-cases.json`. A new case belongs in that file, not in one
  language's test.
- No `eval`, no `new Function`, no `document.write`, no inline `<script>` — the
  edge CSP is `script-src 'self'` and inline execution would need it weakened.
- Tokens: the access token lives in memory only, the refresh token in an
  HttpOnly cookie, and `localStorage` holds preferences, never secrets.

### State and data flow

- `S` is the single mutable app state. Cross-file mutable state belongs on `S`
  rather than in a private `let` reached through an accessor — a reassigned
  exported binding is the classic stale-value trap.
- Every mutation path re-renders the views that display it. The common bug is a
  save function that updates the server and the panel but not the board behind
  it.
- Every `catch` surfaces something a user can act on — an error toast with a
  translated message, not a console line.
- Loading, empty, and permission-denied states exist for every list and every
  view, not just the happy path.

### Two SPAs, one product

- Code needed by both belongs in `octbase-shared/`, imported by name
  (`@octbase/shared/i18n.js`). It is a single copy — a helper hand-copied into
  both `js/` trees is a finding, and a change there reaches both apps at once,
  which makes it more visible than either, not less.
- The same job looks and behaves the same in both apps. Flag drift in shared
  patterns, while respecting the deliberate scope differences (the mobile app is
  a companion for core flows, not a port; some management surfaces are
  desktop-only on purpose).
- Design tokens only: no raw hex outside the `:root` / `[data-theme]` token
  blocks in `css/`. Every component class a feature introduces needs its section
  in `octbase-frontend/styleguide.html` — the living guide — and must be checked
  in every shipped theme.

### i18n

- Every user-visible string comes from `t()`; the `i18n` skill owns the
  workflow. English and German are the supported set.
- Keys are literals at the call site where possible; dynamic prefixes
  (`t('admin.action.' + kind)`) are legitimate but must be reachable, and the
  guards treat them as such.
- `t()` answers an unknown key with the key itself and a console warning nobody
  reads, so a wrong namespace ships an English label to a German reader and
  looks right in every screenshot. Distrust `t('x') !== 'x' ? … : 'English'`
  patterns — they paper over exactly that.

---

## Deliverable

```
# Octbase Frontend Review — <date> @ <git SHA>

## Verdict and headline risks

## Guard results
| Check | Result | Weaker than its invariant? |

## Findings (blocking first)
<file:line> — <what is wrong> — <which rule it breaks> — <fix> — <proof>

## Desktop/mobile drift
Shared patterns that diverged, and whether the divergence is deliberate.

## Style-guide gaps
Component classes shipped without a styleguide.html section, both directions.

## Applied fixes
Small and mechanical only, each with its re-run check.
```

Before running, screenshotting, or visually verifying anything, invoke the
`frontend-testing` skill — the browser setup on this system has gotchas that
will otherwise cost you an hour.
