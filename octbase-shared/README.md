# @octbase/shared

The JavaScript both SPAs use — the desktop (`octbase-frontend`) and the mobile
companion (`octbase-mobile`). A private npm workspace package since **37b stage
3**; before that it was a canonical directory whose contents were copied
byte-identically into each app's `js/` and guarded against drift in CI.

| Module | Contents |
|--------|----------|
| `i18n.js` | the i18n engine — `t()`, `setLocale()`, `getLocale()`, locale loading, and the `classic` vocabulary overlay |
| `meta.js` | task enum metadata — `STATUS_META`, `PRIORITY_META`, `TYPE_META`, the derived `STATUSES` / `PRIORITIES` / `TASK_TYPES`, the estimation helpers, and `openDescendantsOf()` (the task-tree walk behind both SPAs' "open work below this task" completion warning) |
| `notifications.js` | `notificationMessage()` — renders one notification from its `kind` + `params` through `notifications.messages.<kind>`, so the desktop bell and the mobile inbox read the same sentence in the reader's language. Falls back to the server's English `message` for a row written before params existed (`params` is null) or a kind this client does not know |
| `richtext.js` | the rich-text sanitizer — `sanitizeRichText()` (DOMPurify-backed), `rtSafeHref()`, `rtSafeImageSrc()`, `looksLikeHTML()`; mirrors the server's allowlist (`octbase-api` `internal/workmanagement/sanitize.go`) |

Four modules, all first-party. `qrcode.js` and `purify.js` were a fifth and
sixth until **37b stage 4** — vendored copies of `qrcode-generator` and
`dompurify` that lived here because there was no dependency graph to put them
in. They are npm dependencies now (see below), so this package holds only code
Octbase wrote.

The message keys `notifications.js` asks for are built at runtime, so
`scripts/check-i18n-keys.mjs` cannot see them. Their guard is a unit test per
SPA — `octbase-frontend/js/notifications.test.js` and its mobile counterpart —
which reads the notification kinds out of the Go source, so a kind added on the
backend without a translation fails there rather than reaching a user's bell.

## Using it

Each SPA declares `@octbase/shared` as a dependency and imports by module path:

```js
import { t } from '@octbase/shared/i18n.js';
import { STATUS_META, STATUSES } from '@octbase/shared/meta.js';
import { notificationMessage } from '@octbase/shared/notifications.js';
import { sanitizeRichText } from '@octbase/shared/richtext.js';
```

npm workspaces link this directory into `node_modules/@octbase/shared`, and Vite
resolves the specifier at build time — so there is **one copy of every module**,
in this directory. Edit it here and both SPAs pick it up at their next build.
A new module needs an entry in this package's `exports` map to be importable.

## Third-party libraries

Since **37b stage 4** the browser-shipped third-party code comes from npm,
pinned to exact versions (no `^`, no `~`) and integrity-checked by the root
`package-lock.json`:

| Package | Declared on | Imported by |
|---|---|---|
| `dompurify@3.4.12` | this package | `richtext.js` — `import DOMPurify from 'dompurify'` |
| `qrcode-generator@1.4.4` | each SPA | the MFA enrollment QR in `framework.js`, `views-settings.js` and mobile `app.js` |

Each package is declared where it is imported, which is why `qrcode-generator`
sits on the two SPAs and not here — this package does not use it. npm dedupes it
to a single installed copy; if the two pins ever drift apart that is a bug to
fix, not a supported configuration.

Until stage 4 both were **vendored files in this directory** (`purify.js`,
`qrcode.js`), pinned by SHA-256 in `scripts/vendor-manifest.txt`, and stage 3
could only get as far as de-duplicating them: they stayed classic `<script>`
tags publishing the globals `DOMPurify` and `qrcode`, because a UMD file
resolved as *source* is treated inconsistently — the production build runs it
through rollup's CommonJS interop while the dev server serves it raw, so it
takes a different branch of its own wrapper in each, and an `export` written for
one is `undefined` in the other, silently, until the first `sanitize()` call.
Resolved as an npm *dependency* it goes through Vite's dependency
pre-bundling instead, which handles UMD/CJS properly and identically in both
modes.

Both files were verified byte-identical to the published packages before being
deleted, so the swap changed packaging only. Upgrades are now ordinary
dependency bumps, and `npm audit --omit=dev` watches them in CI — the
capability the vendored arrangement never had, and the reason it ended.

## What is intentionally NOT shared

- **Locales** (`locales/*.json`) — the mobile app ships a reduced string set.
  `i18n.js` fetches them relative to the page it runs on, so each SPA serves its
  own.
- **Icons** — the mobile app ships a smaller glyph set than desktop.

These legitimately differ per app and are maintained separately.

## Why the copies existed (history)

Both apps are served from **separate Caddy containers**, and the pre-2026-07-30
architecture forbade a bundler — so there was no way to resolve a shared import
at build time, and no way to load ES modules over `file://` (the standalone demo
and the test suite). Each app therefore carried a physical copy under `js/`,
written by `scripts/sync-shared.sh` and drift-checked in CI by
`scripts/check-shared-sync.sh`. Both scripts were deleted at 37b stage 3; the
`file://` case is now served by each SPA's separate self-contained IIFE bundle
(`npm run build:standalone`).
