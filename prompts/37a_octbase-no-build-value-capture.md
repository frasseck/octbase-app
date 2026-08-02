Act as a senior full-stack engineer working on Octbase, an existing Go (chi + PostgreSQL) task-management app whose two SPAs (`octbase-frontend/`, `octbase-mobile/`) are **bundler-free, plain-DOM, classic-script** applications by deliberate architectural decision (`docs/architecture.md` §5, §5.1).

> **Correction (2026-07-16): "no-build" was never literally true, and this prompt
> originally said it was.** Both `Containerfile`s have long run an esbuild minify
> stage (`npx --yes esbuild@0.24.2 --minify-whitespace --minify-syntax`) — an npm
> package, fetched at image build, shipping minified JS to every deployed stack.
> `docs/operations.md` and `docs/technical_documentation.md` documented it;
> `docs/architecture.md` §5 did not, and this prompt inherited the blind spot.
> §5 has since been corrected. **The stance this prompt defends is real but
> narrower than "no build": no *bundler*, no *npm dependency graph*, no
> `package-lock.json`, no `node_modules`.** Read every "no build step" below as
> that. Nothing else in the prompt changes — the value it captures and the gate
> it re-measures are unaffected.

## Why this prompt exists

`prompts/37b_octbase-frontend-build-step.md` migrates both SPAs to ES modules + Vite. It is **gated on the §5.1 revisit conditions, and as of 2026-07-15 the gate is closed** — measured baseline in that prompt's "Decision gate" table: conditions 2 and 3 have not fired, condition 4's premise is falsified, and condition 1 fired only on a technicality (a build-time vendored parser, not shipped code).

This prompt captures the value conditions **1 and 4 are actually reaching for — without adopting a build.** It is deliberately small.

**This is not a stepping stone to prompt 37b.** It is the alternative that may keep 37b unnecessary indefinitely. Two honest outcomes, both useful:

- It works → the no-build stance keeps paying, and 37b stays shelved on the merits.
- It fails to capture the value (see "The gap this cannot close") → *that failure is itself the evidence* that fires a §5.1 condition on the merits, and 37b goes ahead with a real trigger instead of a stretched one.

**Preconditions: none.** Nothing here is an architecture revision; no sign-off needed. Everything below is additive tooling within the existing stance.

**To execute this prompt, use `prompts/37_octbase-frontend-execution-runbook.md`** — it sequences these stages against prompt 37b's ungated stage 1, assigns a model per stage, and captures the e2e known-failures baseline that every gate below refers to but nothing currently records.

## Already landed — do NOT redo

Check these before writing anything; the assessment's M8 remediation is mostly done:

- ✅ `govulncheck ./...` runs in CI (`.github/workflows/ci.yml` "Security scan" job) — no longer only the bypassable `scripts/security-heavy.sh` pre-push hook.
- ✅ The H5 class is guarded in CI: "Container Go toolchain pin matches go.mod" asserts `Containerfile` `GOTOOLCHAIN` == `go.mod` `toolchain` (both `go1.26.5` today).
- ✅ Secret scanning (gitleaks, full history) in CI.
- ✅ `permissions: contents: read` set workflow-wide.

**Also separately shippable and ungated:** prompt 37b's **stage 1** (per-module action registration — the outstanding "step 2" of the SPA modularization roadmap). It is worth doing whether or not the migration ever happens. It is not duplicated here; ship it from 37b directly (the runbook sequences it).

---

### 1. A unit-test layer in plain Node (§5.1 condition 4's value, no build)

§5.1 cost #3 claims "no unit-testable modules". **That is false today** — `octbase-frontend/js/i18n.test.js` already unit-tests a shared module in plain Node, no framework, by mocking `window`/`document`/`localStorage`/`fetch` and loading the file via `new Function(...)`. The 21 IIFE/export-block files in `octbase-frontend/js/` are testable by the same trick: eval into a fake `window`, read the export block off it.

- **First, wire the existing test into CI.** `js/i18n.test.js` passes and CI does not run it — a pure oversight. Add it as a "Frontend checks" step.
- **Generalize the loader** into one small harness (e.g. `octbase-frontend/js/testutil.js`) that loads any IIFE/export-block file into a fake window with configurable globals and returns its exports. Model it on `i18n.test.js`'s existing `load()`.
- **Use `node:test` + `node:assert`** — both built into Node 22 (CI's version). **Do not add Vitest, jsdom, or any npm dependency**: importing the toolchain this prompt exists to avoid would defeat the entire point.
- **Target the pure, security-critical functions first** (no DOM, no state — testable as-is):
  - `richtext.js`: `rtSafeHref`, `rtSafeImageSrc`, `looksLikeHTML` — URL-scheme allowlists guarding against `javascript:`/`data:`/protocol-relative/control-character bypasses. `rtSafeImageSrc`'s comment says it *mirrors the server's `sanitize.go safeImageSrc`* — **nothing enforces that parity today.** Consider a shared case table both the Go and JS tests read, so drift fails a test rather than shipping.
  - `framework.js`: `esc`.
- **Then the state-dependent ones:** `state.js:137` `applyTaskFilters` and `state.js:158` `filterTasksBySearch` read the module-global `S` rather than taking filters as arguments — seed `window.S.filters` in the harness and they test fine. Cover the archived-task default, each filter dimension, `boardOnly`/`backlogOnly`, and the empty-query no-op.
- **Gate:** `node --test` green as a new "Frontend checks" CI step; the functions above covered; `git grep '"dependencies"'` still finds no `package.json`.

**Explicitly out of scope:** `sanitizeRichText` needs DOMPurify against a real DOM. Plain Node cannot cover it and **the e2e suite already does** — leave it there. This is the honest boundary of the no-build approach and the one place jsdom would genuinely help; if the untestable-in-Node surface grows past this single function, say so in the PR — that is condition 4 starting to fire for real.

### 2. Machine-checkable vendored-dependency integrity (§5.1 condition 1's value, no npm)

`docs/security-assessment-2026-07-14.md` (§290-293) confirms both runtime vendored libs are current and untampered, and **explicitly recommends: "record upstream SHA-256 of the two vendored files for machine-checkable integrity."** That is still undone, and the vendored surface has since doubled.

- Add a manifest + checker (e.g. `scripts/vendor-manifest.txt` + `scripts/check-vendor-integrity.sh`) pinning, for each vendored file: **upstream package, exact version, upstream URL, and SHA-256**. Cover all four:
  - `octbase-shared/purify.js` — dompurify **3.4.11** (runtime)
  - `octbase-shared/qrcode.js` — qrcode-generator **1.4.4** (runtime)
  - `scripts/vendor/acorn.mjs` — acorn (build-time, added `d7e7b33`)
  - `scripts/vendor/acorn-walk.mjs` — acorn-walk (build-time, added `d7e7b33`)
- Fetch each upstream artifact, diff against the vendored copy, and **record any deliberate local patch explicitly** (the assessment found none — a clean diff is the expected result; a surprise here is a finding).
- **Not in scope, and deliberately so:** `npx --yes esbuild@0.24.2` in the two `Containerfile`s is the one npm artifact that reaches a build. It is version-pinned but not integrity-pinned, and `docs/operations.md` ("Refreshing container base-image pins") already records that as a considered decision — published npm versions are immutable, so a version pin is much stronger than a floating tag, and integrity-pinning it would mean introducing the `package-lock.json` this stance exists to avoid. Leave it; do not "discover" it as a finding. If you disagree, that is a separate argument to have in the PR, not a change to smuggle in here.
- Run the checker in the CI "Security scan" job. It must fail on drift in either direction.
- **Add an image scan** (`trivy`) on the built images in the same job — the remaining untouched M8 item, related to M7 (floating image tags).
- **Gate:** CI fails if any vendored file's SHA-256 drifts from its manifest pin; trivy green or findings triaged in the PR.

### 3. Re-measure the gate and correct the record

> **✅ DONE 2026-07-29 (runbook step 5). Do not redo.** §5.1 costs #3 and #4 now
> carry measured figures (7,524 LOC of bundler-obsoleted tooling; 10 test files
> / 112 tests on an 86-LOC harness), and 37b's gate table, cost ledger and
> "current state" numbers were re-measured — **no condition fired on the
> merits**. Condition 5 was written up for sign-off and deliberately **not**
> merged into the normative text.

- **Factual correction (lands without sign-off):** `docs/architecture.md` §5.1 cost #4 says "**three** bespoke tooling systems". It is **five** — `?v=` stamping (`scripts/stamp-assets.py`, 69 LOC) + its merge driver (`scripts/merge-stamped-html.py`, 71) + shared sync/drift (`sync-shared.sh` 31 + `check-shared-sync.sh` 25) + `node --check` + **`scripts/check-exports.mjs` (551 LOC at the time of writing, **557** when actually measured on 2026-07-29, added after §5.1 was written, plus 6,771 LOC of vendored acorn)**. Update the count and the LOC.
- **Correct cost #3** ("No unit-testable modules") to reflect what stage 1 establishes: pure logic *is* unit-testable without a build; only DOM-dependent sanitization needs the e2e suite.
- **Propose (do not land unilaterally — this is normative, it needs sign-off)** a fifth revisit condition, since the trend the baseline actually revealed is not covered by conditions 1–4:

  > 5. the compensating tooling required to sustain the no-build stance exceeds
  >    the tooling a bundler would introduce — measured as bespoke guard LOC +
  >    vendored build-time dependency LOC.

  Rationale for the PR description: `check-exports.mjs` is a hand-rolled reimplementation of what `import`/`export` do natively (its rules 1–3 are "unresolved import", rule 4 is an unused-export lint, and **rule 5 — "never export a reassigned binding" — is a hazard that exists *only* because `Object.assign` snapshots values, which ESM live bindings delete outright**), and sustaining it required vendoring a 6,771-LOC JavaScript parser to avoid adding a `package.json`.
- Update prompt 37b's "Decision gate" baseline table with the re-measured values.
- **Gate:** §5.1's cost list matches a fresh measurement; the proposed condition 5 is in the PR description awaiting sign-off, not merged into the normative text.

---

## The gap this cannot close (state it plainly in the PR)

The SHA-256 manifest catches **tampering and drift** — it proves the vendored copy is still the upstream 3.4.11. It does **not** catch a **newly disclosed CVE in a correctly-pinned version**; that still depends on a human checking advisories via the `js-security`/`go-security` skills, which is exactly what §5.1 cost #1 warns about and what `npm audit`/Dependabot would automate.

**If that gap ever bites — a vendored-dependency security fix noticed late — §5.1 condition 1 has fired on the merits, and `prompts/37b_octbase-frontend-build-step.md` is on.** Record the incident when it happens; that is the trigger, not the library count.

**Deliverables:** `i18n.test.js` wired into CI; a plain-Node export-block test harness + `node:test` coverage of `rtSafeHref`/`rtSafeImageSrc`/`looksLikeHTML`/`esc`/`applyTaskFilters`/`filterTasksBySearch` (optionally a Go↔JS `safeImageSrc` parity table); a vendored-integrity manifest + CI checker over all four vendored files; a trivy image scan; corrected `docs/architecture.md` §5.1 costs #3 and #4; a proposed condition 5 awaiting sign-off; an updated baseline table in prompt 37b; `CHANGELOG.md` entries.

**Constraints:** **no `package.json`, no `node_modules`, no npm dependency graph, no bundler** — that is the point of this prompt; the pre-existing esbuild minify stage in the two `Containerfile`s is *not* a violation of it and must be left alone (see the correction note at the top); the bundler-free stance stays intact and normative; use only Node 22 built-ins (`node:test`, `node:assert`); unit tests complement, never replace, the e2e suite (`docs/architecture.md` §6); do not weaken any existing security guard; do not redo the already-landed items listed above.
