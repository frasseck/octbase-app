# Octbase Architecture Decisions

> Status: Normative — code review holds changes against this document
> Audience: Backend/frontend engineers, reviewers
> Companion: [`hosting-concept.md`](hosting-concept.md) for deployment topology,
> `CLAUDE.md` (repo root) for the working conventions this document justifies.

This document records **what architectural style Octbase deliberately follows,
what it deliberately does not, and when to revisit that choice**. It exists so
the trade-offs are a decision, not an accident.

---

## 1. The style: modular monolith with strategic DDD

The backend is a single Go binary organised as a **modular monolith by bounded
context** under `octbase-api/internal/`:

- `workmanagement` (projects, tasks, boards, sprints, releases, categories,
  templates), `docs` (pages, revisions), `identityaccess`/`auth`/`rbac`,
  `scmintegration`, `activity`, `notifications`, `sse`, `webhooks`, `admin`,
  `auditlog`, `usermgmt`, `mailer`, `dashboard` (user preferences),
  `security` (TOTP MFA), `retention` (GDPR data purge), `bootstrap` (first-run
  admin provisioning), plus `shared`, `seed`, `testutil`, `archtest` (test-only
  dependency-direction check), `apicontract` (test-only route↔OpenAPI parity).
- `cmd/octbase-api/main.go` is the **composition root**: it is the only place
  that opens the database and wires repositories → services → handlers, and it
  reads the core configuration (ports, database, demo mode, app version).
  Feature-scoped settings are read by the module that owns the feature —
  `auth` (token/reset/MFA TTLs), `mailer` (SMTP), `scmintegration` (OAuth
  endpoints), `webhooks` (secrets), `shared` (trusted proxies, crypto key),
  `retention`, `notifications`, `bootstrap` — every supported variable is
  catalogued in `.env.example`.
- Contexts talk to each other through narrow consumer-defined interfaces
  (e.g. `workmanagement.PageSearcher`, `ActivityWriter`, `Notifier`), never by
  reaching into another context's tables.

From Domain-Driven Design, Octbase practices the **strategic** patterns and the
parts of the tactical toolbox that pay for themselves here:

- **Bounded contexts** with an enforced dependency direction (everything may
  use the core packages — `shared`, `rbac`, `auth`, `auditlog`, `mailer`,
  `sse` — while contexts do not import each other's internals). The rule is
  executable: `internal/archtest` fails on unclassified packages, on core
  importing a context, and on any context→context import outside its reviewed
  allowlist (currently `workmanagement→docs` and `auth→security/mfa`).
- A **ubiquitous language** carried through code, API, UI, and tests: Sprint,
  Release, Backlog, Board lane, Task key.
- **Explicit domain rules** with stable, machine-readable error codes
  (`TASK_IMMUTABLE`, `RELEASE_HAS_OPEN_TASKS`, `SPRINT_ALREADY_ACTIVE`,
  `VERSION_CONFLICT`, …) that integration tests assert exactly — the tests are
  the executable contract.
- **Invariant helpers on the domain types** (`IsImmutable`, `ValidateLaneLimits`,
  `ValidateTaskInput`) and a `Service` layer where rules span aggregates
  (relation cycles, sprint board provisioning, release close guards).

## 2. The explicit non-goal: hexagonal architecture

Octbase is **deliberately not hexagonal** (ports & adapters), and it does not
maintain a tactical-DDD aggregate model. Concretely:

- **Domain structs are the contract.** The same struct is the database row, the
  domain object, and the JSON wire shape. There is no DTO-mapping layer; a JSON
  tag change is an API change, reviewed as such.
- **Handlers orchestrate directly.** HTTP handlers validate, enforce
  permissions, apply domain rules, call repositories, and write activity —
  without an intermediate application-service port for every use case.
- **Repositories are concrete.** They take `*sql.DB`/`*sql.Tx` and are not
  hidden behind context-wide storage interfaces; interfaces exist only where a
  consumer needs a seam (Go's consumer-side interface idiom).

**Why.** The system has exactly one transport (REST/JSON), one database
(PostgreSQL), and one write model. Every layer hexagonal would add — ports,
DTO mappers, application services per use case — would today have exactly one
implementation on each side. The test strategy makes the usual "testability"
argument for ports moot: handler tests run the **real router against real
migrations on real PostgreSQL** (`internal/testutil`), which verifies more than
mock-based unit tests would, at similar cost. The savings are spent where they
matter for this product: contract stability, seed determinism, and end-to-end
verification.

**What it costs.** Swapping the persistence or transport layer would be
expensive; domain rules are spread between `domain.go`, `Service`, and
handlers; the domain model cannot be exercised without a database. These are
accepted costs, not oversights.

**When to revisit.** Introduce ports/adapters (start with the affected context
only, `workmanagement` first) if any of these become real:

1. a second transport (gRPC, CLI, message consumer) must reuse the same rules;
2. a second storage backend (other than the planned attachments object store);
3. domain rules grow interdependent enough that handler-level orchestration
   produces duplicated or diverging logic between endpoints.

Until then, **do not** introduce speculative interfaces, DTO layers, or
per-use-case services; review feedback should point here.

## 3. Concurrency model: optimistic locking

Every mutable aggregate root (task, project, release, sprint, page, board,
board column, comment, category, template, repository connection) carries a
`version` column:

- Repository updates are **version-guarded**
  (`UPDATE … WHERE id=$x AND version=$y`); zero affected rows yields
  `shared.ErrVersionConflict`, which handlers map to **HTTP 409
  `VERSION_CONFLICT`**. This also catches updates to concurrently deleted rows.
- PATCH endpoints — and the task quick actions (`status`, `priority`,
  `assign`, `move-task`) — accept an optional client-supplied `version`; the
  SPAs send the version of the snapshot the user acted on, so a stale write is
  rejected instead of silently overwriting a concurrent editor.
- Requests without a `version` stay valid (last-write-wins across requests)
  but are still guarded against races within the handler's read-modify-write.
  First-party clients are expected to send it on every mutation of a
  versioned aggregate; the optionality exists for API back-compat and is a
  candidate for tightening in a future major release.
- Successful updates return the incremented `version`; clients must refresh
  their cached copy from the response (the SPA helpers do).
- **Cross-row invariants live in the database, not in check-then-act code**:
  one ACTIVE sprint per project (partial unique index
  `idx_sprints_one_active`, surfaced as `SPRINT_ALREADY_ACTIVE`), unique page
  slugs per project (`idx_pages_project_slug`, surfaced as `SLUG_CONFLICT`),
  and "a release only closes while no open task references it" (the
  condition is part of the close UPDATE itself). Handler pre-checks remain
  for friendly error messages, but the constraint is what holds under
  concurrency. Multi-row state transitions (sprint completion) run in one
  transaction so a version conflict rolls the whole transition back.

## 4. Runtime state and scaling stance

The API is stateless toward clients (JWT), but three things live in process
memory: the **SSE hub**, the **rate limiter**, and the **sweep throttle**.
MFA login preserves this: when an account has TOTP enabled, `POST
/auth/login` returns a short-lived, single-purpose **MFA challenge token**
(same HS256 signing secret as the access token, but a distinct `iss` claim,
`internal/auth/jwt.go`'s `mfaChallengeIssuer`) instead of real tokens. `POST
/auth/mfa/verify` exchanges that token plus a TOTP/recovery code for the
normal access/refresh pair. No server-side "pending login" record is created
— the challenge token *is* the state, self-contained and stateless like every
other JWT here, so this adds no new item to the in-process-memory list above.
The distinct issuer is enough to keep the two token kinds from being used in
place of each other; see `internal/auth/jwt.go` for the validation. The
supported production shape is therefore **one API container per deployment**,
scaled by deploying one stack per client (hosting-concept Models A/B). Running
multiple API replicas against one database additionally requires shared
attachment storage, an SSE bus, and a shared rate limit — see
`hosting-concept.md` §6.2 before attempting Model C.

## 5. Frontend

Both SPAs are **plain-DOM** applications. "Plain DOM, no framework" is the
normative statement and is not up for revision: no framework, no JSX, no client
state library, rendering through the `esc()` / `` html`…` `` /
`sanitizeRichText` escaping producers, and view modules self-registering in a
view registry. The safety invariant that makes this viable — every dynamic value
passes through an escaping producer — is enforced by CI
(`scripts/check-innerhtml.mjs`).

**Bundler-free was the other half, and it was retired on 2026-07-30 — see §5.2
for the decision and `prompts/37b_octbase-frontend-build-step.md` for the staged
migration.** All seven stages landed by 2026-07-31; both SPAs and the code they
share are ES modules built by Vite:

| | Module system | Build |
|---|---|---|
| `octbase-frontend` (desktop) | **ES modules** — one `<script type="module">` entry (`js/main.js`), per-file `import`/`export`, no load-order contract | **Vite** (`octbase-frontend/vite.config.js`), content-hashed assets |
| `octbase-mobile` | **ES modules** — one `<script type="module">` entry (`js/app.js`), which imports `js/core.js` | **Vite** (`octbase-mobile/vite.config.js`) |
| `octbase-shared` (3 modules) | the private **`@octbase/shared` npm workspace package** — `i18n.js`, `meta.js` and `richtext.js`, imported by name | resolved into each SPA's bundle like any other module |

**The two runtime libraries are npm dependencies, not vendored files** (stage 4):
`dompurify` on `@octbase/shared`, `qrcode-generator` on both SPAs, both
exact-pinned. They are inside the module graph, so `npm audit --omit=dev` covers
them — which is how the DOMPurify advisory in §5.2's addendum was found on that
step's first meaningful run. The lesson generalizes: the problem was never that
the libraries were UMD, it was that they were UMD **as source**. A UMD file
inside the repository is a module rollup transforms, differently in the build
than on the dev server; the same library as a dependency goes through Vite's
pre-bundling and behaves identically in both.

Two kinds of file stay outside the bundles on purpose, and the reasons are
constraints rather than preferences: `theme-init.js` (synchronous in `<head>`
before first paint, so it cannot be a deferred module, and inlining it would
need `'unsafe-inline'` in the CSP), and the desktop's two single-script static
pages (`docs-init.js`, `user-guide-nav.js`). Since stage 5 neither is copied
verbatim either: each is emitted as `assets/<name>-<hash>.js` with its
`<script src>` rewritten, so it is cache-busted by filename exactly like the
bundled output. See `octbase-frontend/js/README.md` for the table.

**Each SPA also builds a second artifact: one self-contained IIFE bundle**
(`npm run build:standalone` → `dist-standalone/`). A browser refuses `import`
from a `file://` origin, and both SPAs auto-sign-in as the seeded demo user when
`location.protocol === 'file:'` (`USE_STANDALONE_DEMO_AUTH`), so the module build
alone cannot be opened from disk at all. It is a packaging difference only — same
source, same modules. The Playwright mobile suite loads that artifact, because
`file://` is the code path it is there to exercise.

**The build was never the line it was described as.** Even while the stance
held, both `Containerfile`s ran an esbuild minify stage (`npx --yes
esbuild@0.24.2 --minify-whitespace --minify-syntax`, version-pinned only),
so a deployed stack already served non-reviewed bytes. (Since 37b stage 3
those images run the real Vite build against the committed lockfile and ship
`dist/`; the unpinnable `npx --yes` invocation is gone with them.) What
actually changed in 2026-07-30 is a **bundler, a module graph, and a
`package-lock.json`**; §5.1 below and its cost ledger must be read with that
correction in mind.

**Identifier mangling must stay off, and it is now load-bearing in a second
place.** Delegated dispatch keys handlers by `fn.name` (`_dispatch` in
`js/delegation.js`, `_register` in mobile's `js/core.js`), so renaming top-level
functions silently unregisters every delegated click, change, input, keydown and
submit in the app — no exception, no console output, just dead buttons. The
esbuild minify stage never mangled; Vite's minifier would, so
`rollupOptions.output: { keepNames: true }` in the Vite config is not a size
trade-off but a correctness requirement. The option **must live on the rolldown
output options**: Vite 8 bundles with rolldown and no longer uses esbuild at
all, so the earlier `esbuild: { keepNames: true }` became a silent no-op on
upgrade — a green build that shipped mangled action names (see the comment in
`octbase-frontend/vite.config.js`). It cost a full session's debugging to learn
once (13 e2e failures whose only symptom was dead buttons); `_dispatch` now
logs the missing handler name so it cannot be silent again.

Within the desktop SPA, the shell (core) holds no per-view knowledge: view
modules self-register in a view registry (`octbase-frontend/js/registry.js`)
that drives the sidebar, content dispatch, toolbar and route fallback, and
deployment feature gates plug in as a registry entry's `enabled`/`fallback`.
Delegated event handlers follow the same direction: each module registers its
own `data-act`/`data-change`/… handlers at load time through the registration
API in `js/framework.js`, so the shell knows no view's handler names and the
five dispatch registries are file-private rather than global
(`octbase-frontend/js/README.md` "Delegation registration").
This mirrors the backend's core/modules dependency direction (§1) at frontend
scale — adding a view must not require editing the shell.

**Data loading is parallel-first, and paints in stages.** Requests are issued
as soon as their *inputs* exist rather than where their results are consumed:
boot starts a project landing route's data alongside the session/config calls,
the router starts the target view's data (registry `prefetch`) before
`loadProject`, and the renderer collects those in-flight requests through the
`Prefetch` hand-off (`api.js`) instead of issuing its own. A view then paints
whatever it can already draw — the board writes its toolbar and lanes from the
board object and fills the cards when they land, rather than holding the screen
until everything has arrived. The constraint this trades against is that a
staged paint needs an honest pending state: a view must never assert "loaded but
empty" for data that is still in flight. Honest means marking the region
(`aria-busy`, a blanked count) and leaving it empty — *not* filling it with
placeholder cards, which read as real, empty tasks rather than as loading.

### 5.1 The deliberate non-goal (today): a bundler and a module toolchain

Like §2, this is a decision with revisit conditions, not dogma. The non-goal is
a **bundler + npm dependency graph** (ES modules, Vite/webpack, a
`package-lock.json`, a `node_modules`) — not a build step in the literal sense;
see the minify stage noted in §5.

**What it buys.** No frontend toolchain to maintain, upgrade, or have rot; no
npm dependency tree to audit; any editor works; the module graph is readable
without tooling.

**What it does not buy, contrary to earlier wording here.** This section
previously claimed "`git pull` is a deploy" and "the running code is
byte-for-byte the reviewed code (debugger fidelity, no sourcemaps)". Both are
false and were false when written:

- Deploys are **image builds**, not `git pull` — the frontend ships as a Caddy
  container built from `octbase-frontend/Containerfile` (`docs/operations.md`
  "Deploy").
- The running code on a deployed stack is **minified esbuild output**, so the
  bytes are not the reviewed bytes and there are no sourcemaps to recover them.
  Debugger fidelity holds where it actually gets exercised — the source tree,
  `file://`, and the e2e suite — not on a client stack.

The honest form of the benefit is the first paragraph: **no toolchain and no
npm graph.** The migration in `prompts/37b_octbase-frontend-build-step.md`
should be weighed against *that*, not against a byte-fidelity property this
codebase does not have.

**What it costs — and where the pressure will come from:**

1. **Vendored dependencies are invisible to audit tooling.** `purify.js`
   (DOMPurify 3.4.11) and `qrcode.js` (qrcode-generator 1.4.4) are vendored
   copies; noticing a CVE relies on the security-review skills, not on
   `npm audit`/Dependabot.
2. **The API contract is unchecked on the client.** "Structs are the
   contract" (§2) means a renamed Go JSON tag breaks the SPA silently at
   runtime; only the e2e suite can catch it. Generated types from
   `octbase-api/api/openapi.yaml` would catch it at build time.
3. **Unit testing is bolted on, not designed in.** A `node --test` unit layer
   now exists for pure logic — filtering, state helpers, i18n, rich-text
   policy: **10 `octbase-frontend/js/*.test.js` files, 112 tests, run in CI**
   against an 86-LOC hand-rolled harness (`js/testutil.js`) that fakes
   `window`/`document`/`localStorage` (measured 2026-07-29). But files must
   keep their logic separable from the DOM by discipline, and anything
   DOM-coupled is still exercised only through the browser e2e suite.
   > **Outcome (37b stage 7).** The layer runs on Vitest now — 11 files, 131
   > tests. The count rose because `i18n.test.js` had been a hand-rolled runner
   > whose 16 assertions the reporter counted as one. Three files import their
   > module for real and need no harness; the other eight keep it because they
   > stub collaborators. So the cost above is halved rather than removed, and
   > the sentence that still stands is the last one: DOM-coupled code is still
   > only covered by the browser suite. **This was never a reason to adopt a
   > build** — the plain-Node harness worked — and it is recorded here as
   > ergonomics, which is what it is.
4. **A family of bespoke tooling systems** exists solely to compensate for
   the missing bundler. Measured 2026-07-29: `?v=` content-hash stamping
   (`scripts/stamp-assets.py` 69 LOC + its git merge driver
   `scripts/merge-stamped-html.py` 71, plus a pre-commit hook), the
   `octbase-shared/` byte-identical sync + drift guard (`sync-shared.sh` 31
   + `check-shared-sync.sh` 25 — both **deleted by 37b stage 3**, which made
   `octbase-shared/` an imported package with no second copy to drift), the
   `node --check` syntax gate, and the
   export-completeness guard (`scripts/check-exports.mjs` 557) — which
   required **vendoring a JavaScript parser** (`scripts/vendor/acorn.mjs`
   6,304) to avoid adding a `package.json`. That is
   **7,057 LOC a bundler would delete outright** (re-measured 2026-07-30,
   after the unused `acorn-walk.mjs` 467 was dropped). Two further guards sit
   alongside them that a build would *not* remove, and they are not part of
   that figure: the innerHTML escaping check (144) and the vendored-file
   integrity manifest (56).
   > **Being collected, and the figures above are the pre-migration snapshot.**
   > 37b stage 2 deleted `check-exports.mjs`'s rules (203 lines as it stood — the
   > rest of the 557 had already been factored into `scripts/lib/js-scope.mjs`)
   > because an unresolved import is now a build error. **Stage 3 deleted the
   > shared sync pair (56).** **Stage 5 deleted the `?v=` stamping system
   > (`stamp-assets.py` 69 + `merge-stamped-html.py` 71 + a 29-line post-merge
   > hook + the restamp half of the pre-commit hook), along with the
   > `.gitattributes` merge-driver entry and its `.git/config` registration** —
   > replaced by 100 lines of build config (`scripts/vite-hash-classic-assets.mjs`)
   > that hash the assets Vite leaves alone, which is the substitution this
   > prediction was about. The resolver (364) and the vendored parser (6,304) do
   > *not* leave with any of it: they now carry `scripts/check-tdz.mjs`, the
   > standing guard for the one boot failure a valid module graph still allows.
   > **The runbook's instruction to delete them at stage 5 is superseded** — a
   > build removes the *need* for an export guard, not the need for a parser.
   > Do not re-measure this list against the current tree and conclude the
   > prediction was wrong — it is being collected in stages.

**When to revisit — superseded 2026-07-30. See §5.2.** These four conditions
governed the decision until it was taken. They are kept because §5.2 records
that *none of them fired*, and that record is only readable next to the
conditions themselves:

1. a vendored dependency ships a security fix we notice late, or the vendored
   surface grows beyond the current two libraries;
2. an API contract change breaks a SPA in a way generated types would have
   caught at build time — more than once;
3. `octbase-shared/` grows beyond the current five modules or gains a third
   consumer;
4. frontend logic grows complex enough to genuinely need a unit-test layer.

The 2026-07-14 IIFE/export-block refactor was done with this migration in
mind: each file's `Object.assign(window, { … })` block is a machine-readable
module boundary, so the ESM conversion is a mechanical codemod, not a
rewrite. The 2026-07-29 per-module handler registration sharpened that
boundary rather than blurring it — with delegated handlers no longer forced
onto `window` to be found by name, the export blocks fell from **332 names to
216** and now list only genuine cross-file calls, which is what an `export`
statement would have to carry. **What stays decided even with a build: plain DOM, no framework** —
the registry + escaping-producer rendering model is not up for revision here;
a build changes how files are joined and checked, not how the UI renders.

### 5.2 Decision record — the bundler non-goal is retired (2026-07-30)

**Decision.** Both SPAs migrate to ES modules built by Vite, executed in stages
per `prompts/37b_octbase-frontend-build-step.md`. §5.1 above is **historical
context from here on**, not a live constraint. It is kept rather than deleted
because a decision reversal is only auditable next to the reasoning it reversed.

**Who and when.** Lars (maintainer), 2026-07-30, in session, after being shown
the measurement below and restating the instruction.

**The trigger was maintainer direction — no revisit condition fired.** This is
the part worth being exact about, because §5.1's own rule was that adoption is a
measurement and not a judgement call, and 37b's precondition was a fired
condition **and** sign-off. Only the second half was met. Re-measured
2026-07-30, immediately before the decision:

| §5.1 condition | Measured 2026-07-30 | Status |
|---|---|---|
| 1 — vendored security fix noticed late, or surface grows past two libraries | **3** vendored files, down from 4: the unused `acorn-walk.mjs` was deleted that morning (`cb892b2`). No late-noticed security fix. | **NOT FIRED**, and moved further from firing |
| 2 — contract break generated types would have caught, more than once | zero recorded incidents | **NOT FIRED** — and now moot: 37b stage 7 generated the types anyway, so the condition can no longer fire for want of them |
| 3 — `octbase-shared/` past five modules or a third consumer | exactly 5 modules, exactly 2 consumers | **NOT FIRED** |
| 4 — frontend logic needs a unit-test layer | 10 test files, 116 tests, all green, no build involved | **NOT FIRED** (premise falsified) |

No row is relabelled to make the paperwork agree with the outcome. The honest
record is that the stance was retired by decision while its own trigger
conditions read closed.

**The proposed fifth condition is moot and not adopted.** A tooling-cost
condition ("compensating tooling exceeds the tooling a bundler would introduce")
was drafted on 2026-07-29 and left awaiting sign-off. It measured a stance that
no longer exists, so it is retired undecided rather than adopted. Its one
durable finding is worth keeping: raw LOC overstated the burden, because 6,304
of the 7,057 LOC were a vendored parser nobody maintained — deleting the 467-LOC
`acorn-walk.mjs` moved the headline number without moving the argument at all.

**What is given up, accepted knowingly** (from 37b's cost ledger): an npm
dependency tree and lockfile to audit and keep current, toolchain rot and Node
upgrades, and any-editor-works. It does **not** cost byte-fidelity or
`git pull`-as-deploy — §5 already records that both were given up long ago to
the esbuild minify stage.

**What does not change.** Plain DOM, no framework, no JSX, no client state
library; the view registry; the `esc()` / `` html`…` `` / `sanitizeRichText`
escaping producers; the CSP staying `script-src 'self'` without
`'unsafe-inline'`; the Playwright suite as the regression gate at every stage. A
build changes how files are joined and checked, not how the UI renders. Any
stage that would weaken a security guard to pass is an abort, not a trade-off.

**Status — complete, 2026-07-31.** All seven stages landed on
`wip/37b-stage2-esm`, each green and shippable on its own, with the Playwright
suite as the gate at every one (final measurement on the merged tree: 370
passed / 22 skipped / 2 failed, both failures known and pre-existing). Stage 4
merged last despite being the earliest open stage, because stages 5–7 landed
while it was being verified. §5 above is now descriptive of what is, not a
progress report.

**Addendum, 2026-07-31 — condition 1 fired, after the fact (37b stage 4).**
Recorded because it is evidence, not because it changes anything: the decision
was already taken and executing, and the advisory is closed in the same stage
(DOMPurify 3.4.12). Stage 4 replaced the two vendored runtime
libraries with npm dependencies, and the `npm audit --omit=dev` step it added
immediately reported **GHSA-c2j3-45gr-mqc4** against the pinned
`dompurify@3.4.11` — a fix published upstream in 3.4.12 that nobody here had
noticed. That is §5.1 condition 1 (*"a vendored dependency ships a security fix
we notice late"*) in its literal terms, and on the merits this time rather than
on the acorn technicality the 2026-07-29 measurement had to reject.

Two honest qualifications. The advisory is **low severity and not exploitable
in Octbase** — it concerns `CUSTOM_ELEMENT_HANDLING`, which the rich-text policy
never enables (`octbase-shared/richtext.js`) — so this was a latent audit gap,
not an incident. And it was found *because* the migration happened: the finding
is the new capability working on its first run, which is precisely what
condition 1 was written to predict and what the vendored SHA-256 manifest could
never have surfaced. The 2026-07-30 table above stands as measured; nothing in
it is relabelled.

## 6. Testing stance

Development is test-first against the API contract: new behavior lands with
integration tests that drive the real HTTP router against real migrations on
PostgreSQL, asserting exact status codes and stable error codes. CI enforces a
one-way coverage ratchet (see `.github/workflows/ci.yml`); the Playwright suite
drives the built SPA against a seeded stack. Unit tests are reserved for pure
logic (sanitizers, slug/abbreviation derivation, i18n loader).
