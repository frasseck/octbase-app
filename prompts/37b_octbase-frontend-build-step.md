Act as a senior full-stack engineer working on Octbase, an existing Go (chi + PostgreSQL) task-management app whose two SPAs (`octbase-frontend/`, `octbase-mobile/`) are today **bundler-free, plain-DOM, classic-script** applications. This prompt migrates them to **ES modules with a bundler (Vite, esbuild underneath)** — while keeping the plain-DOM architecture exactly as it is. It executes the architecture revision described in `docs/architecture.md` §5.1.

> **Framing correction (2026-07-16).** This prompt was written as "no build →
> build". That is not the actual delta: the images already minify with esbuild
> (`npx --yes esbuild@0.24.2`), so a build step and an npm artifact are already
> in the shipping path. **What this prompt actually adds is a bundler, a module
> graph, and a `package-lock.json` dependency tree** — which is still a real
> architecture change worth gating, just a smaller one than the original framing
> implied. The cost ledger below has been corrected accordingly; the decision
> gate is unaffected (minification fires none of the four conditions, because it
> pre-dates them).

> **Status: gated, not scheduled.** Re-measured **2026-07-29** (runbook step 5):
> the §5.1 trigger conditions are **still not met on the merits** — see "Decision
> gate" below for the current figures. Do not run this prompt because the plan
> looks ready; run it when the gate opens. **Stage 1 was the exception and has
> now shipped** (`28bc707`, 2026-07-29) — it was independently valuable; the rest
> of this prompt remains gated.
>
> **Start at `prompts/37_octbase-frontend-execution-runbook.md`, not here** — it is
> the entry point for this group of prompts (37 → 37a → 37b in execution order) and
> sequences the ungated work, including this prompt's stage 1.
>
> **While the gate is closed, `prompts/37a_octbase-no-build-value-capture.md` is
> the prompt to run instead of this one** — it captures what conditions 1 and 4 are
> reaching for without adopting a build, and re-measures the gate afterwards. If 37a
> fails to capture that value, the failure is the real trigger for this prompt.

## Decision gate — measure, don't argue

`docs/architecture.md` §5.1 lists four revisit conditions. "Has one occurred" is a
**measurement**, not a judgement call. Re-run this table before starting; the
migration needs at least one row genuinely `FIRED` **on the merits** (not on a
technicality — see condition 1) **and** an explicit maintainer sign-off recorded
in the PR description. Sign-off is not inferable from how attractive the tooling is.

> **Re-measured 2026-07-29** (runbook step 5, after step 4 landed). The table
> below now carries those figures; the 2026-07-15 baseline it replaces differed
> only where noted in the cells. **No row moved to FIRED on the merits** — the
> gate is still CLOSED, and the go/no-go task records the sign-off position.

| # | §5.1 condition | How to measure | Measured (2026-07-29) | Status |
|---|---|---|---|---|
| 1 | Vendored dep ships a security fix noticed late, **or** vendored surface grows beyond the then-current two libraries | `ls octbase-shared/{purify,qrcode}.js scripts/vendor/*.mjs`; check each against upstream advisories | **4 vendored libs, unchanged since 2026-07-15**: DOMPurify 3.4.11 + qrcode-generator 1.4.4 (runtime, both current & CVE-free per `docs/security-assessment-2026-07-14.md` §290-291), **plus** `scripts/vendor/acorn.mjs` + `acorn-walk.mjs` (6,771 LOC, added `d7e7b33` 2026-07-14, **build-time only**). No late-noticed security fix has occurred. | ⚠️ **Literally fired, on a technicality.** The clause counts libraries; the *rationale* (cost #1) is unshipped-code CVE exposure, and acorn never reaches a browser. Do **not** treat this alone as the trigger — but see "What this actually signals". |
| 2 | An API contract change breaks a SPA in a way generated types would have caught — **more than once** | `git log --grep` for contract-break fixes; count incidents | Still **no recorded incident**. (`52c82c6` hardened PATCH decoding *before* any break shipped, which is a prevention, not an incident.) | **NOT FIRED** |
| 3 | `octbase-shared/` grows beyond five modules or gains a third consumer | `ls octbase-shared/*.js \| wc -l`; `grep -rl 'js/i18n.js' --include='*.html'` | Still exactly **5** modules; still exactly **2** consumers (`octbase-frontend/index.html`, `octbase-mobile/index.html`). Trend 2 → 3 → 5 since `f1992db` has **not continued** in the two weeks since the last measurement. | **NOT FIRED** (still the one most likely to fire next) |
| 4 | Frontend logic grows complex enough to genuinely need a unit-test layer | `node --test octbase-frontend/js/*.test.js` | **Premise is falsified, and more decisively than at baseline.** The layer is now **10 test files / 112 tests**, run by CI (`ci.yml` "JS unit tests"), on an **86-LOC** plain-Node harness (`js/testutil.js`) that fakes `window`/`document`/`localStorage`. At baseline this was one file that CI did not run. | **NOT FIRED** — and a build is still not what unlocks this |

**What this actually signals (read before deciding).** The honest reading of the
baseline is not "one condition fired, go". It is that **the no-build stance is now
sustained by more bespoke tooling than §5.1 anticipated**, and that is the trend
that will actually decide this:

- §5.1 cost #4 used to say "**three** bespoke tooling systems". It is **five**:
  `?v=` stamping (`scripts/stamp-assets.py`, 69 LOC) + its merge driver
  (`scripts/merge-stamped-html.py`, 71) + shared sync/drift
  (`sync-shared.sh` 31 + `check-shared-sync.sh` 25) + `node --check` +
  **`scripts/check-exports.mjs` (557 LOC)** — the last one added *after* §5.1 was
  written, and never folded back into the cost list. **Corrected in §5.1 on
  2026-07-29 with the measured LOC**; the total a bundler would delete outright
  is **7,524 LOC** (753 of scripts + 6,771 of vendored parser).
- `check-exports.mjs` is a hand-rolled reimplementation of what `import`/`export`
  do natively: its rules 1–3 are "unresolved import", rule 4 is tree-shaking /
  unused-export lint, and **rule 5 ("never export a reassigned binding") is a
  hazard that exists *only* because `Object.assign` snapshots values — ESM live
  bindings delete the entire class.**
- To build that guard without a package.json, the team **vendored a 6,771-LOC
  JavaScript parser**. That is the reductio of the stance, and it is why
  condition 1 fired on a technicality.

If a future reviewer wants a defensible trigger, **propose adding it to §5.1
first** (as its own change, with sign-off) rather than stretching condition 1:

> 5. the compensating tooling required to sustain the no-build stance exceeds
>    the tooling a bundler would introduce — measured as bespoke guard LOC +
>    vendored build-time dependency LOC.

**Also required before starting:** the desktop SPA's IIFE/export-block refactor
(2026-07-14) is in place — it is the mechanical basis of this migration.

## Cost ledger (the side §5.1 says a build gives up)

A decision prompt that lists only the benefits biases toward "yes". Weigh these
explicitly in the sign-off:

- **Scope:** 7 PRs across ~24.5k LOC of JS (17.0k desktop excluding the test layer, 7.5k mobile — measured 2026-07-29), CI, two
  `Containerfile`s, `podman-compose*.yml`, 4 docs, and the `frontend-guards` +
  `release` skills.
- **What is permanently given up** (§5.1 "what it buys"): no frontend toolchain
  to maintain or have rot; no npm dependency tree to audit; any-editor-works
  ends.
  - **Corrected 2026-07-16 — this bullet used to claim more than it could.** It
    previously also listed "`git pull` is no longer a deploy" and "the running
    code is no longer byte-for-byte the reviewed code (sourcemaps become
    load-bearing)". **Neither is a cost of this migration, because both were
    already true before it.** Deploys are image builds, not `git pull`; and the
    images have long run an esbuild minify stage, so a deployed stack already
    serves non-reviewed bytes with no sourcemaps. §5.1 asserted the byte-fidelity
    property and this ledger inherited it — see the correction now in
    `docs/architecture.md` §5/§5.1. **The real remaining cost is the npm graph
    and the toolchain, and the sign-off should be argued on that alone.** Do not
    re-inflate this list; an overstated cost ledger biases toward "no" exactly as
    a benefits-only prompt biases toward "yes".
- **New recurring costs:** npm supply-chain surface (the thing condition 1 exists
  to bound — a bundler *trades* 2 audited runtime vendored files for a
  transitive dep tree), lockfile churn, Node version upgrades, Dependabot noise,
  and toolchain rot — the failure mode `docs/security-assessment-2026-07-14.md`
  H5 already demonstrated on the Go side.

**Abort criteria.** Stop and revert to the last shipped stage if: the stage-2
codemod needs more than incidental hand-editing (the export blocks were supposed
to make it mechanical — if they don't, the premise is wrong); the e2e known-
failures baseline grows and cannot be restored within the stage; or the CSP /
escaping guards would have to be weakened to make a stage pass (see Constraints).

---

The **overriding requirement is behavior preservation with the same rendering model**: plain DOM, the view registry, the `esc()`/`` html`…` ``/`sanitizeRichText` escaping producers, and the existing Playwright e2e suite as the regression gate. A framework (React/Vue/…), JSX, a client state library, or a mass TypeScript rewrite are all **out of scope** — a build changes how files are joined and checked, not how the UI renders. Every stage below must leave the app shippable; land them as separate PRs in order.

## Current state (read before designing)

- **The module boundaries already exist.** Measured 2026-07-29 (after stage 1
  shipped): **24 of the 33** source files in `octbase-frontend/js/` are
  IIFE-wrapped, **22** of them ending in one `Object.assign(window, { … })`
  export block (**216 exports** total; largest: `framework.js` 60,
  `views-task.js` 30, `views-shell.js` 25). At the 2026-07-15 baseline this read
  21 files / 290 exports / 65-49-36 — the drop is stage 1 removing handlers that
  were only on `window` so the shell could find them by name, so what remains is
  closer to a true import surface. The export block *is* the file's public
  surface; the imports each file needs are exactly its references to other
  files' exported names (derivable with an acorn AST walk —
  `scripts/check-exports.mjs` already does this cross-reference and is the
  natural basis for the codemod). See `js/README.md` "File scope & exports".
- **The other 12 files are not SPA modules — scope them now, not at stage 5:**
  - 5 are the synced `octbase-shared/` copies (`i18n.js`, `meta.js`, `qrcode.js`,
    `purify.js`, `richtext.js`) → handled by stage 3.
  - 3 are **single-script static pages outside the SPA graph**: `docs-init.js`
    (`/docs.html`), `user-guide-nav.js` (`/user-guide.html`), `styleguide-icons.js`
    (`/styleguide.html`). **Decide in stage 2, not stage 5:** either 3 extra Vite
    entries or leave them as classic scripts hashed by other means. They are one
    `<script>` each — the cheap, low-risk answer is to leave them alone.
  - `theme-init.js` → see the CSP/FOUC constraint below.
  - `i18n.test.js` (plain-Node unit test), `views-mindmap.js` and `bootstrap.js`
    (export nothing).
- `octbase-mobile/js/` (8 files) was **not** wrapped and has no export blocks;
  derive its module graph with the same AST cross-reference analysis before
  converting it.
- **Load order** is dependency order in `index.html` (**29** deferred `js/` script tags — re-confirmed 2026-07-29; a 30th, `theme-init.js`, is loaded synchronously in `<head>` and is deliberately not part of this list, see below —
  `bootstrap.js` last); the contract lives in `js/README.md`. With ESM this whole
  contract disappears — the bundler topologically sorts, and circular imports
  become build errors.
- **⚠️ `theme-init.js` is a hard constraint, not a load-order entry.** It is
  loaded **synchronously in `<head>` before the stylesheet** so the saved theme
  applies before first paint, and it is deliberately an **external file — not
  inline — so the Caddy CSP can stay `script-src 'self'` without
  `'unsafe-inline'`** (read its header comment). `<script type="module">` is
  **always deferred**, so folding it into the module graph ships a flash of the
  wrong theme, and the obvious fix (inlining it) **breaks the CSP** — which the
  Constraints below forbid. It must stay a classic, non-module, non-`defer`
  `<script>` outside the bundle; hash it via a separate mechanism if needed.
  Verify no theme flash on a cold load at every stage.
- **~~Two things~~ One thing still resolves through bare window globals:** ~~(a) the delegation registries~~ — **removed by stage 1 on 2026-07-29**; handlers are registered per module and the registries are file-private. What remains is (b): the Playwright suite drives `window.router` (and reads `window.t`, `window.getLocale`); `window.App` (`namespace.js`) is the existing deliberate facade. **(b) is now the only intentional window surface**, as stage 1 intended.
- **`file://` is a workaround, not a requirement.** `tests/conftest.py:21` builds the UI URL as `file://…/index.html?apiBase=…` but honors an **`OCTBASE_UI_URL`** env override — the seam for serving `dist/` already exists. The standalone demo (`USE_STANDALONE_DEMO_AUTH = location.protocol === 'file:'` in `config.js`) is the one genuine `file://` consumer.
- **Bespoke tooling the bundler obsoletes — 7,524 LOC measured 2026-07-29 (753 of scripts + 6,771 of vendored parser):**
  - `?v=<sha256[:12]>` params stamped by `scripts/stamp-assets.py` (69 LOC, CI
    `--check`), restamped by a pre-commit hook, plus the custom merge driver
    `scripts/merge-stamped-html.py` (71 LOC) wired via `.gitattributes` and
    `scripts/setup-git.sh`.
  - `scripts/sync-shared.sh` (31) + `scripts/check-shared-sync.sh` (25).
  - **`scripts/check-exports.mjs` (557 LOC) + its vendored `scripts/vendor/acorn.mjs`
    (6,304) and `acorn-walk.mjs` (467)** — obsoleted wholesale by real `import`/
    `export` (see "What this actually signals"). This is the single largest
    tooling win of the migration and must be in the delete list.
  - The `node --check` loop (replaced by the build).
  - **Not obsoleted:** `check-innerhtml.mjs` (144 LOC) — the escaping guard
    survives; see stage 6.
- **Shared modules to promote:** `octbase-shared/` (`i18n.js`, `meta.js`, `qrcode.js`, `purify.js`, `richtext.js`) is canonical; both SPAs carry byte-identical copies synced by `scripts/sync-shared.sh` and drift-guarded by `scripts/check-shared-sync.sh`. `i18n.js` is already UMD-ish (`global.x = x` exports). Locales and icons are intentionally **not** shared (mobile ships a reduced set) — keep it that way.
- **Vendored libraries to replace:** runtime — `purify.js` = `dompurify@3.4.11` (MPL-2.0/Apache-2.0), `qrcode.js` = `qrcode-generator@1.4.4` (MIT). Build-time — `scripts/vendor/acorn.mjs` + `acorn-walk.mjs` (MIT), which are **deleted outright** with `check-exports.mjs` rather than replaced.
- **CI (`.github/workflows/ci.yml`):** "Frontend checks" job is **five** steps —
  `node --check` loop, `scripts/check-innerhtml.mjs`, **`scripts/check-exports.mjs`**,
  shared-drift guard, stamp `--check`. "Frontend E2E (Playwright)" job builds the
  API, seeds demo mode, runs pytest; "Build image" job builds three container
  images from per-repo `Containerfile`s (the frontend one is a shell-less Caddy
  image serving the repo files directly — see `octbase-frontend/caddy/`).
  Note: `js/i18n.test.js` is **not** run by CI today.
- **API types source:** `octbase-api/api/openapi.yaml`, kept honest by the backend's `internal/apicontract` route↔spec parity test.
- **Escaping discipline** (`js/README.md` "HTML-safety convention") is enforced by `scripts/check-innerhtml.mjs`; the i18n en/de key parity by `js/i18n.test.js`.

## Goal

Migrate both SPAs to ES modules built by Vite, delete the bespoke tooling the bundler obsoletes (including the export guard and its vendored parser), replace vendored runtime libraries with audited npm dependencies, and add the checks a build unlocks (generated OpenAPI types + checked JSDoc, ESLint with `no-unsanitized`). The Playwright suite must pass against the built output with the same known-failures baseline as before the migration.

---

### 0. Decision gate & docs (part of the first PR)

- Record the trigger (which row of the gate table fired, with its measurement) and the sign-off. Rewrite `docs/architecture.md` §5/§5.1: the no-build stance becomes historical context; the new normative statements are "plain DOM, no framework" and the build conventions you establish below.
- Update `CLAUDE.md` ("Frontend stays plain DOM" keeps its meaning but its "no build step" clause changes), `docs/technical_documentation.md`, and `octbase-frontend/js/README.md` as each stage lands — not as a final cleanup commit.

### 1. Remove the bare-name window dependency (ships independently, still no build)

> ## ✅ SHIPPED 2026-07-29 — `28bc707`, runbook step 4. **Do not redo.**
> `framework.js` exports `registerActions`/`registerChanges`/`registerInputs`/
> `registerKeydowns`/`registerSubmits`; each module registers its own handlers
> from one block above its export block; `bootstrap.js` is
> `initDelegation(); init();`. The five dispatch registries are now
> **file-private** to `framework.js`, and the export blocks fell from **332
> names to 216**. Gate met exactly: the delegation registry is identical
> before/after (**184 handlers**, sorted keys of all five registries dumped from
> a real browser load), e2e **372 passed / 22 skipped / 0 failed**. Contract
> documented in `octbase-frontend/js/README.md` "Delegation registration".
> Two deviations from the text below, both deliberate: registration is one block
> per file rather than literally adjacent to each handler (it matches the
> export-block convention and keeps the adapter mapping auditable), and
> `window.App` needed **no** extension — the Playwright suite is green on
> `window.router`/`t`/`getLocale`, so the leak was removed rather than the
> facade grown.
>
> **This stage was not gated.** It was the outstanding "step 2" of the SPA
> modularization roadmap and was worth shipping on its own merits whether or not
> the migration ever happens.

ESM later makes it mandatory (module-scoped handlers no longer land on `window`), but its value today is that the shell stops knowing every view's handler names.

- Give the delegation system an explicit registration API (e.g. `registerAction(name, fn)` / `registerChange(…)` on the registries in `framework.js`), and have **each module register its own handlers next to their definitions** at load time. Empty `bootstrap.js`'s `registerActions()` file by file; `bootstrap.js` shrinks to `initDelegation(); init();`.
- Shrink the export blocks accordingly: a handler that only ever ran via delegation no longer needs exporting. Cross-file *calls* keep their exports.
- Expect `check-exports.mjs` rule 4 (dead public surface) to start firing as exports shrink — that is the guard working, not a regression.
- Define the deliberate test/console surface: `window.App` (extend it if the Playwright suite needs more than `router`; `t`/`getLocale` stay globals via the shared i18n module until stage 3).
- **Gate:** full e2e suite green with the same baseline; the delegation registry contents before/after are identical (assert by dumping `Object.keys(ACTIONS|CHANGES|…).sort()` in a throwaway check).

### 2. Workspace, bundler, and the ESM codemod

- Root `package.json` with npm workspaces: `octbase-frontend`, `octbase-mobile`, `octbase-shared`. Vite for dev server and production build, one config per SPA. Pin the Node version (`.nvmrc` or `engines`) — CI uses Node 22 today.
- **Codemod, don't hand-edit:** for each desktop file, turn the `Object.assign(window, { … })` block into `export { … }`, and generate its `import` statements from the AST cross-reference (which other file exports each referenced name) — reuse `check-exports.mjs`'s existing resolver rather than writing a new one. Delete the IIFE wrappers. `index.html` gets a single `<script type="module" src="js/main.js">`; `main.js` imports the former load-order list (imports, not side-effect ordering, now carry the dependencies — keep explicit side-effect imports only where load-time registration matters, e.g. view modules).
  - **If the codemod needs non-incidental hand-editing, stop** — see Abort criteria.
- **`theme-init.js` stays outside the bundle** as a classic non-module `<script>` in `<head>` (CSP/FOUC constraint above). Confirm on a cold load, both themes.
- **Static pages:** apply the stage-2 decision for `docs.html` / `user-guide.html` / `styleguide.html` (default: leave them as classic scripts).
- Mobile: run the same analysis to derive its graph, then the same codemod.
- The **standalone demo** stays a `file://` artifact: add a second build target producing one self-contained IIFE-format bundle (Vite lib/iife or a direct esbuild invocation) and verify the seeded demo sign-in works when opened from disk.
- Keep the deliberate window surface as an explicit module (`namespace.js` → assigns `window.App`, `window.router` if the tests keep using it).
- **Gate:** e2e suite green against `vite preview` of `dist/` via `OCTBASE_UI_URL`; `npm run build` reproducible; no theme flash.

### 3. `octbase-shared` becomes a real package

- Convert the five shared modules to ESM in `octbase-shared/` (package name e.g. `@octbase/shared`), import them from both SPAs, and **delete** `scripts/sync-shared.sh`, `scripts/check-shared-sync.sh`, the CI drift step, and both SPAs' physical copies. Locales/icons stay per-SPA.
- **Gate:** both SPAs build and pass e2e; `git grep sync-shared` finds only history/docs.

### 4. Vendored runtime libraries → npm dependencies

- Replace `purify.js`/`qrcode.js` with `dompurify@3.4.11` and `qrcode-generator@1.4.4` (pin exactly first, upgrade later as its own change; diff the vendored copy against the published package to confirm no local patches before deleting). Commit the lockfile; add `npm audit --omit=dev` (or an audit action) to CI.
- **Gate:** rich-text sanitization e2e tests and the MFA-enrollment QR test pass; audit step green or findings triaged in the PR.

### 5. Delete the compensating tooling; ship hashed builds

- **Delete `scripts/check-exports.mjs` (557 LOC) and `scripts/vendor/acorn*.mjs` (6,771 LOC)** — real imports/exports now cover its rules 1–3 at build time, rule 4 becomes an unused-export lint, and rule 5's hazard (snapshotted `Object.assign` exports) no longer exists under ESM live bindings. Drop its CI step. Record this in `js/README.md` (the "never export a reassigned binding" rule retires with it).
- Bundler emits content-hashed filenames; remove the `?v=` params, `scripts/stamp-assets.py` (both SPAs), `scripts/merge-stamped-html.py`, the `.gitattributes` merge-driver entry, the pre-commit restamp, and `scripts/setup-git.sh`'s wiring for them; update the `frontend-guards` and `release` skills and the CI job. Leave the rest of `setup-git.sh`'s hook wiring (the security sweeps) intact.
- Frontend/mobile `Containerfile`s: multi-stage — `node` build stage producing `dist/`, final shell-less Caddy stage copying `dist/` (Caddyfile paths update; `/m/`, `/api` proxying, docs/user-guide/styleguide static pages must keep working per the stage-2 decision). Keep the CSP header unchanged.
- **Gate:** container images build in CI; a compose stack serves the app, `/m/`, `/docs.html`, `/user-guide.html`, `/styleguide.html`; browser cache behavior verified (changed chunk → new hash; unchanged → cached); CSP unchanged and no theme flash.

### 6. CI and test-suite rewiring

- "Frontend checks" job becomes: `npm ci && npm run build` (replaces `node --check` **and** the export guard), `check-innerhtml.mjs` pointed at the source tree (port it if its heuristics assume the old file layout), ESLint (add `eslint-plugin-no-unsanitized` — keep the bespoke guard until the ESLint rule demonstrably covers its cases, then decide), `npm audit`.
- "Frontend E2E" job: build `dist/`, serve it (static server or `vite preview`), set `OCTBASE_UI_URL`; drop the `file://`-specific Chrome flags from `conftest.py` for the served path.
- **Gate:** all CI jobs green; e2e baseline unchanged.

### 7. What the build unlocks (each ships separately)

- **Generated API types:** `openapi-typescript` from `octbase-api/api/openapi.yaml` (generated file committed or built in CI — pick one and document it), consumed via JSDoc `@type`/`@param` annotations + `tsc --noEmit --checkJs` on an allowlist starting with `http.js`, `api.js`, `state.js`. Expand file by file; **no mass `.ts` conversion.** Wire spec→types freshness into CI (regenerate and `git diff --exit-code`, mirroring the backend's `apicontract` parity idea). **This is the one genuinely build-gated benefit** — and it addresses §5.1 condition 2, which has never actually fired.
- **Vitest unit layer** for pure logic: `applyTaskFilters`/`filterTasksBySearch`, the task-list engine's grouping, `richtext.js` policy decisions, i18n loader (migrating `js/i18n.test.js` into it).
  - **Framing check:** a build does **not** "unlock" unit testing here —
    `js/i18n.test.js` already unit-tests a shared module in plain Node with mocked
    browser globals, and the same harness generalizes to the export-block files.
    Vitest buys **ergonomics** (jsdom, watch mode, coverage reporting, less mock
    boilerplate) over a pattern that already works. Sell it on that, and do not
    cite it as a reason to adopt a build.
  - Unit tests complement, never replace, the e2e suite (`docs/architecture.md` §6 stance).

---

**Deliverables:** the npm workspace + Vite setup with per-SPA builds and the `file://`-capable standalone-demo bundle; both SPAs converted to ESM via the export-block codemod; per-module action registration (empty `registerActions`); `@octbase/shared` consumed by both SPAs with the sync/drift tooling deleted; vendored DOMPurify/qrcode replaced by pinned npm deps + lockfile + audit; **`check-exports.mjs` + vendored acorn deleted**; the stamping/merge-driver/hooks machinery deleted and multi-stage container builds shipping `dist/`; rewired CI (build, lint incl. `no-unsanitized`, innerHTML guard, audit, e2e against served `dist/`); generated OpenAPI types + incremental `checkJs`; a Vitest unit layer; updated `docs/architecture.md` §5, `CLAUDE.md`, `docs/technical_documentation.md`, `js/README.md`, the `frontend-guards`/`release` skills, and a `CHANGELOG.md` entry per stage.

**Constraints:** plain DOM and the escaping-producer discipline stay (no framework, no JSX, no state library); every stage lands green and shippable on its own; the Playwright e2e suite is the regression gate at every stage (same known-failures baseline); the standalone demo keeps working from `file://`; **`theme-init.js` stays a classic non-deferred external script — no theme flash, and the CSP stays `script-src 'self'` without `'unsafe-inline'`**; exact-version pinning when swapping vendored libs; **no weakening of any security guard (CSP, escaping, sanitizer policy) to make a stage pass** — that is an abort condition, not a trade-off; the decision gate is measured (not argued) and maintainer sign-off is recorded before any of it starts.
