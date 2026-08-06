---
name: frontend-guards
description: The "Frontend checks" CI guards for octbase-frontend/octbase-mobile/octbase-shared — ESLint, the Vite build, the JS unit layer, HTML-injection (innerHTML) guard, module TDZ guard, metrics-not-proxied guard, and npm audit. Use before pushing any frontend change, when editing octbase-shared or a Caddyfile, or when the Frontend checks CI job fails.
---

# Frontend CI guards

CI's **Frontend checks** job (`.github/workflows/ci.yml`) runs the checks below.
Run them locally before pushing; all but the build are cheap.

## Run them all

```bash
cd /home/claude/dev.octbase.io

npm ci
npx eslint .                     # replaced the node --check loop (37b stage 6)
npm run types:generate && git diff --exit-code -- octbase-frontend/types/  # spec↔types freshness
npm run typecheck                # tsc --noEmit over the // @ts-check allowlist
npm run build                    # replaced the export-completeness guard (37b stage 2)
npm run test:unit               # vitest
node scripts/check-innerhtml.mjs
node scripts/check-tdz.mjs       # the one boot failure a valid build still allows
bash scripts/check-metrics-not-proxied.sh
node scripts/check-error-translations.mjs
node scripts/check-audit-actions.mjs
node scripts/check-i18n-keys.mjs  # every literal t() key exists, in every locale
npm audit --omit=dev --audit-level=low   # what ships; the dev chain is informational
```

## 1. ESLint — `npx eslint .`

Config: `eslint.config.mjs` at the repo root. **It replaced the `node --check`
loop at 37b stage 6**, which parsed a hand-maintained list of paths; the build
already fails on a parse error inside the module graph, so ESLint's job is
everything outside it (the classic scripts, the `js/*.test.js` layer, the `.mjs`
guards) plus the two errors a parser cannot see:

- **`no-undef`** — a name used with no `import` behind it. Under classic scripts
  that still resolved off `window`; under ES modules it is a ReferenceError at
  runtime and the build says nothing, because an unimported free identifier is
  just a global read.
- **`no-unused-vars`** — the import or helper that outlived its last caller.

Both found live bugs the first time they ran (37b stage 6): the login MFA
enrolment form's submit handler had never been registered, and the styleguide's
icon grid was still reading `window.ICONS`, which the ESM conversion removed.

`no-unsanitized/method` is on and gates — it covers `insertAdjacentHTML`, which
the innerHTML guard below does not. **`no-unsanitized/property` is deliberately
off**; the measurement and the condition for turning it on are in the config
file, next to the rule.

## 1b. Generated API types — `npm run types:generate` + `npm run typecheck`

`octbase-frontend/types/openapi.d.ts` is generated from
`octbase-api/api/openapi.yaml` and **committed**; CI regenerates it and diffs, so
a spec change that is not reflected in the committed types fails the build. Fix
by running `npm run types:generate` and committing — never by hand-editing the
generated file.

`npm run typecheck` is `tsc --noEmit` over the files carrying a `// @ts-check`
line (today `http.js`, `api.js`, `state.js`). It is not a TypeScript build: no
`.ts` files, nothing emitted. Adding `// @ts-check` to another file is how the
allowlist grows — expect to add a few JSDoc annotations when you do, and expand
one file at a time. Details in `octbase-frontend/js/README.md`.

## 2. HTML-injection guard — `scripts/check-innerhtml.mjs`

The SPA renders via `.innerHTML`, which is safe only because every dynamic
value goes through an escaping/trusted producer: `esc(x)`, the auto-escaping
`` html`…` `` tagged template, `raw(x)` (explicit opt-out, sparingly),
`sanitizeRichText`, or helpers that return already-escaped HTML (`icon()`,
`t()`, `fooInner()`/`fooHtml()` render helpers).

The guard fails on: `.innerHTML +=`, string concatenation into HTML
(`'<b>' + x`), `document.write(...)`, and template-literal interpolation of
known user-content fields (`.title`, `.name`, `.text`, `.description`, …)
without `esc()`/`` html`` ``. Files listed in the script's `STRICT_FILES`
ratchet are fully migrated to `` html`` `` and may not assign an untagged
template to `innerHTML` at all (trusted fragments opt out via `raw()`);
migrate more files over time and add them to the set — never remove an
entry. **Fix by escaping, never by weakening the guard.** Interpolating pre-built fragment variables and safe enums/counts is
legitimate; see `octbase-frontend/js/README.md` for the convention.

## 3. The build — `npm ci && npm run build`

**Replaced the export-completeness guard** (`scripts/check-exports.mjs`, deleted
in 37b stage 2). That guard was a hand-rolled reimplementation of
`import`/`export` — 203 lines of rules over the 364-line resolver in
`scripts/lib/js-scope.mjs`, itself on a vendored 6,304-line parser — and it
existed only because there was no build: it cross-referenced bare identifiers
against `Object.assign(window, …)` export blocks to catch the `ReferenceError`s
`node --check` could not see.

The resolver (`scripts/lib/js-scope.mjs`) and the vendored parser **stay**, and
this is a correction to 37b's cost ledger rather than an oversight: they were
listed as tooling a bundler would delete outright, but the TDZ guard below is a
genuine ongoing need that a bundler does *not* cover, and it is built on them.
What retired is the 203 lines of export rules. `scripts/codemod-esm.mjs` is spent
— it reads classic scripts, so it can no longer run against the tree it
produced — and is kept only as the record of how the conversion was performed.

With real ES modules the bundler checks that for real — **an import of a name
nothing exports is a build failure**, not a heuristic. Two of the old guard's
five rules needed a replacement rather than a deletion:

| Retired rule | What covers it now |
|---|---|
| unresolved cross-file reference | the build (hard error) |
| export name with no declaration | the build (hard error) |
| exported reassigned `let`/`var` | **nothing needed** — ESM exports are live bindings, so the stale-snapshot hazard cannot occur |
| dead public surface | the bundler's unused-export detection; ESLint arrives in 37b stage 6 |
| load-time ref to a later-loaded file | **`node scripts/check-tdz.mjs`** — checks the thing that actually breaks under ESM (a top-level read of a not-yet-evaluated `const`/`let`/`class` across an import cycle) and exits non-zero on a hazard |

Run that last one too when you touch a module's imports or add top-level code:

```bash
node scripts/check-tdz.mjs        # non-zero exit on a TDZ hazard
```

Both SPAs are covered: `octbase-mobile` was converted in the same stage.

### The dependency audit — `npm audit --omit=dev`

Added at **37b stage 4**, when the two libraries that reach a browser
(`dompurify`, `qrcode-generator`) stopped being vendored files and became pinned
npm dependencies. It is the capability that swap bought: a SHA-256 pin could
prove a vendored copy had not been tampered with, but never that an advisory had
been published against it. On its very first run it found one — GHSA-c2j3-45gr-mqc4
against `dompurify@3.4.11`, fixed upstream in 3.4.12 and unnoticed here until
then (`docs/architecture.md` §5.2 addendum).

`--omit=dev` is deliberate: a `vite`/`eslint` advisory is a real finding but is
not shipped code, and must not gate a frontend change. Nothing audits the dev
tree today — a gap, not a decision. Fix a runtime finding by bumping the pin
(`docs/operations.md` "Upgrading a frontend runtime dependency"), never by
relaxing `--audit-level`: the run-all block above uses `--audit-level=low`
precisely because the one advisory this guard has ever caught was a low.

## 4. Shared modules — no guard any more, and nothing to sync

**Retired at 37b stage 3.** `octbase-shared/` is the `@octbase/shared` workspace
package (`i18n.js`, `meta.js`, `richtext.js` — the vendored `purify.js` and
`qrcode.js` left it at stage 4, and are now the pinned `dompurify` and
`qrcode-generator` npm dependencies), and both SPAs `import` from it — there is
one copy, so there is no drift to guard.
`scripts/sync-shared.sh` and `scripts/check-shared-sync.sh` are gone; if you find
a call to either, it is stale and should be deleted, not restored.

- Edit the module in `octbase-shared/` and both SPAs pick it up at their next
  build. Adding a module means adding it to that package's `exports` map.
- Locales and the icon set are intentionally **not** shared (mobile ships a
  reduced set) — don't "helpfully" unify them.
- The two SPA images build from the **repository root** context for this reason
  (`podman build -f octbase-frontend/Containerfile .`) and ship the built
  `dist/`; `.containerignore` bounds what the context carries.

## 5. Metrics-not-proxied guard — `scripts/check-metrics-not-proxied.sh`

No Caddy config may reverse-proxy `/metrics`. The API registers
`promhttp.Handler()` with **no auth**, so the route is private only because
nothing in front of it proxies the path — Prometheus scrapes `octbase-api:8000`
directly (`docs/operations.md` §Prometheus Metrics). The guard checks all three
configs: `octbase-frontend/caddy/Caddyfile{,.tls}` and
`octbase-mobile/caddy/Caddyfile`.

**The mobile config is the trap, and it is why this guard exists.**
`octbase-mobile` is never published to the host, so listing `/metrics` in its
`@backend` set looks harmless. It is not: the front door serves that SPA via
`handle_path /m/*`, which **strips the prefix**, so a public request for
`/m/metrics` arrives there as `/metrics`. That regression shipped and left
`https://<host>/m/metrics` world-readable (fixed 2026-07-16) — stacks were
shielded only if the *optional* installation password happened to be on.

> Generalise it: **any route the front door refuses to proxy must also be refused
> by the mobile config**, or `/m/<route>` quietly reinstates it.

Fix by removing the path, never by weakening the guard. Do **not** try to gate
`/metrics` by source IP instead — rootless podman NATs published-port traffic, so
a `not remote_ip 10.0.0.0/8 …` deny sees a private container address for every
caller and never fires (that inert rule used to sit in `Caddyfile.tls`).

## 5b. Error-translation guard — `scripts/check-error-translations.mjs`

Every error code the API can emit must have an `errors.<camelCaseCode>` string
in **all four** locale files (`en`/`de` × both SPAs). The guard greps the codes
out of the Go handlers (`shared.Write*Error` calls and `DomainError` literals)
and checks each one against the `errors` block of each locale file.
`VALIDATION_ERROR` is exempt — it is one code covering many messages and is
keyed by message text under `errors.validation.*`.

It exists because a missing translation is **invisible in testing**: the API
answers `{code, message, messageKey}`, `apiErrorMessage()` translates
`messageKey` and falls back to the raw English `message`, so the UI still shows
a sentence — just the wrong language, and only on the code paths someone
happened to trigger. That is how 85 of 107 codes stayed untranslated.

Fix by adding the key to every locale file, never by exempting the code. If the
new string uses agile vocabulary (sprint, backlog, epic, story, story points,
release) it needs a `classic.errors.<key>` variant too — a separate unit test in
`i18n.test.js` enforces that, for the desktop locales.

## 5c. Audit-action guard — `scripts/check-audit-actions.mjs`

`octbase-frontend/js/admin.js`'s `ACTION_KEYS` must match the `Action*`
constants in `octbase-api/internal/auditlog/domain.go`, and every listed action
must have an `admin.action.<ACTION>` label in both desktop locales. The guard
checks **both** directions — an action the frontend omits is unfilterable and
renders as its raw enum; an action the frontend lists but the backend never
writes is a dropdown option that matches nothing.

Nine actions had drifted out of that list, including every password event, every
MFA event, and `REFRESH_TOKEN_REUSE` — the token-replay alarm. They were being
logged the whole time and were simply unreachable from the tool built to read
the log.

## 5d. i18n-key guard — `scripts/check-i18n-keys.mjs`

The general case of 5b and 5c, for every key written as a literal at the call
site. Two halves, one script:

1. **Every `t('literal')` key exists in every locale file of the SPA that calls
   it.** Keys are grepped out of `octbase-frontend/js`, `octbase-mobile/js` and
   `octbase-shared` (whose keys are owed by *both* SPAs, since both import it).
   Failures print the call sites.
2. **The locale files of a site carry the same key set** (`en` ↔ `de`).

Why both halves are silent failures rather than loud ones: `t()` answers a
missing key with the key itself and `console.warn`s where nobody is looking, and
the call sites paper over even that with
`t('common.back') !== 'common.back' ? t('common.back') : 'Back'` — so a wrong
key namespace renders correct English and is invisible in any screenshot. That
is not hypothetical: mobile's confirm sheet shipped reading `common.cancel`,
which mobile does not have (it is `form.cancel`), and the German dialog rendered
"Cancel" under "Trotzdem erledigen". Half 2 is quieter still — a key present in
`en.json` alone never reaches the raw-key path at all, because `resolve()` falls
back to English first, so it does not even warn.

Only literals are checkable. A key assembled at runtime (`t('errors.' + code)`,
`` t(`status.${s}`) ``) has no literal to read, which is exactly why the two
families that do that have their own guards above.

Fix by adding the key to every locale file of that SPA, or by correcting the key
at the call site — the guard has no exemption list, and prints the file:line of
each caller so the second option is checkable.

## 6. Asset cache-busting — no guard any more (37b stage 5)

There is nothing to run and nothing to keep in step. The Vite build
content-hashes every asset filename, so a changed file is a different URL by
construction.

That includes the classic scripts *outside* the module graph —
`theme-init.js`, `docs-init.js` and `user-guide-nav.js`. (The two vendored UMD
libraries were in this list until 37b stage 4 replaced them with npm
dependencies; they are inside the bundle now, hashed like any other module.)
`scripts/vite-hash-classic-assets.mjs`
emits each as `assets/<name>-<hash>.js` and rewrites the `<script src>` in every
HTML entry. **If you add a classic (non-module) script to an HTML entry, add it
to that config's `CLASSIC_ASSETS` list too** — otherwise it is not copied into
`dist/` at all and the page 404s on it the first time the build is served.

Because both `vite.config.js` files import that plugin from outside their own
directory, it is also the one thing in `scripts/` that the container build
context admits (`.containerignore`) and that both Containerfiles `COPY` — the
build stage cannot load its config without it.

What retired here: `scripts/stamp-assets.py` and its `--check` CI step, the
`?v=` queries in both `index.html` files, `scripts/merge-stamped-html.py`, the
`.gitattributes` `merge=stamphtml` driver, and the pre-commit/post-merge restamp
hooks. `scripts/setup-git.sh` still installs the security-sweep hooks.

The matching Caddy rule changed shape: `/assets/*` is `immutable` for a year
(hashed), while files served under a stable name — `vendor/swagger-ui/*`, the
verbatim `fonts/` copy — get `max-age=3600`. Keying that on the directory rather
than on `*.js`/`*.css` is the point; the old rule promised immutability for
files whose names never change. The `not path /assets/*` in the second matcher
is load-bearing: without it a hashed asset matches both rules and gets two
`Cache-Control` headers.

## Related

- Releasing → `release`
- Frontend e2e tests → `frontend-testing` · Translations → `i18n`
