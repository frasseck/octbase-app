---
name: js-security
description: Security-review the Octbase static frontends (octbase-frontend, octbase-mobile, octbase-shared) — innerHTML/escaping discipline, DOMPurify rich-text policy, browser-side token handling, link/URL hardening — plus a grep-driven regression sweep. Use when reviewing a frontend diff/PR for security, adding a view/render path or code that touches user content, URLs, or auth tokens, or asked to security-check the frontend.
---

# JS security review (octbase-frontend / octbase-mobile / octbase-shared)

Both SPAs are no-build plain-DOM apps that render via `innerHTML`, so the XSS
posture rests on **escaping discipline plus CI guards**, not a framework. The
codebase is already security-mature — the goal of a review is **catching
regressions against the invariants below**, not re-deriving them. Cite findings
by `file:line`. The backend counterpart is the `go-security` skill; the edge
CSP/Caddy invariants live there and are not repeated here.

## Established invariants — verify these still hold

| Area | Invariant | Where |
|---|---|---|
| HTML injection | All rendering is `innerHTML`, safe only because **every dynamic value passes a trusted producer**: `esc(x)`, the auto-escaping `` html`…` `` tag, `raw(x)` (explicit opt-out, sparingly), `sanitizeRichText`, or helpers that return already-escaped HTML (`icon()`, `fooInner()`/`fooHtml()`). The `check-innerhtml.mjs` CI guard enforces this — **fix findings by escaping, never by weakening the guard.** | `scripts/check-innerhtml.mjs`, `esc()` in `octbase-frontend/js/framework.js` / `octbase-mobile/js/core.js` |
| i18n | `t()` does **not** escape interpolation vars (`interpolate()` is a plain string replace) — callers wrap any dynamic var in `esc()` when the result lands in HTML (`t('x',{name:esc(u.displayName)})`). `textContent` sinks (`toast()`) are exempt. Locale JSON is developer-authored code: user input must never be written into locale files. | `octbase-shared/i18n.js` |
| Rich text | DOMPurify (the pinned `dompurify` npm dependency since 37b stage 4) + the shared Octbase policy: tag allowlist; global `ALLOWED_ATTR` narrowed **per tag** by the `afterSanitizeAttributes` hook; `rtSafeHref` allows only `http(s):`/`mailto:`/relative and rejects control chars (no `javascript:`/`data:`); `rtSafeImageSrc` allows **only our own relative attachment-content path** (no external, `data:`, or protocol-relative `//`); links forced to `rel="noopener noreferrer" target="_blank"`; an `<img>` whose `src` fails validation is removed. **The server sanitizer stays the source of truth** — any policy change must be mirrored in `internal/docs/sanitize.go` first. | `octbase-shared/richtext.js` |
| Shared-module drift | **Structurally impossible since 37b stage 3**: `richtext.js` and the other shared modules live once, in the `@octbase/shared` package, imported by both SPAs — no second copy, no sync step. Since stage 4 that holds for the sanitizer's engine too: DOMPurify is one pinned npm dependency rather than a vendored file each build copied out of the package. Drift is how sanitizer holes appear (the pre-DOMPurify mobile sanitizer had drifted) — what to check now is that neither SPA has grown a *local* re-implementation of a shared module. | `git grep -l "sanitizeRichText\|DOMPurify" octbase-frontend/js octbase-mobile/js` |
| Token storage | The access token lives in a **closure variable, memory only** — never `localStorage`/`sessionStorage`. The refresh token is an `HttpOnly` `SameSite=Strict` cookie the JS never reads; the only JS-visible companion is the presence-marker cookie, which holds no secret. `localStorage` carries preferences only (theme, locale, recent project IDs). | `octbase-frontend/js/auth.js`, `octbase-mobile/js/core.js` |
| Tokens in URLs | Exactly one allowed case: the SSE `EventSource` appends `?token=` (EventSource cannot set headers; the server side is `OptionalJWTMiddleware`). Emailed reset/invitation tokens travel in the **`#` fragment** (never the query — fragments don't reach server logs or `Referer`). Never add a credential to any other URL. | `octbase-frontend/js/realtime.js`, `router.js` |
| Attachments | Uploaded files are viewed via an **authed fetch → `blob:` object URL** (revoked after 60 s), popup-blocked fallback uses a transient `rel="noopener"` anchor. Never render a direct `/content` link that would need a token in the URL. | `octbase-frontend/js/views-task.js` (`viewAttachment`) |
| External links | `target="_blank"` always pairs with `rel="noopener"` (the rich-text hook adds `noreferrer` too). A **user-controlled** URL landing in an `href` must pass `rtSafeHref` first — `esc()` alone does not stop `javascript:`. Server-vetted URLs (e.g. SCM `prUrl`) still get `esc()` + `noopener`. | `octbase-frontend/js/views-task.js`, `octbase-shared/richtext.js` |
| No dynamic code | No `eval`, `new Function`, `document.write`, and no inline `<script>` blocks in the HTML — the edge CSP is `script-src 'self'` (see `go-security`). New JS is a classic script added to the load order and cache-stamped. | enforced by the sweep below + `frontend-guards` |
| URL overrides | `?apiBase=` (both SPAs) and `?desktop=` (mobile) are honored **only in dev contexts** (`file://`/loopback, the `DEV_CONTEXT` gate); `?desktop=` additionally allows only http(s)/file targets because it lands in `href`s. Never add a URL param that redirects traffic or links on deployed origins. | `octbase-frontend/js/config.js`, `octbase-mobile/js/core.js` |

## Regression sweep

Run from the repo root. First the two cheap CI guards (syntax, innerHTML) — see
the `frontend-guards` skill for the full set, including the build itself:

```bash
for f in octbase-frontend/js/*.js octbase-mobile/js/*.js octbase-shared/*.js; do
  node --check "$f" || echo "SYNTAX: $f"
done
node scripts/check-innerhtml.mjs
```

Then the security greps. Each should return nothing (or only the noted
known-safe hits):

```bash
grep -rn 'eval(\|new Function\|document\.write' octbase-frontend/js octbase-mobile/js octbase-shared --include='*.js' | grep -v test   # no dynamic code
grep -rniE '(localStorage|sessionStorage)\.setItem' octbase-frontend/js octbase-mobile/js --include='*.js'   # UI prefs only (theme, lang, nav/backlog toggles, recent projects) — never token/jwt/secret
grep -rn '_blank' octbase-frontend/js octbase-mobile/js --include='*.js' | grep -v noopener   # known-safe: viewAttachment's window.open('', '_blank') blob viewer; richtext.js:81 (rel set on the line above)
grep -rn '?token=' octbase-frontend/js octbase-mobile/js --include='*.js' | grep -v realtime  # SSE is the only token-in-URL
grep -rn 'innerHTML' octbase-shared --include='*.js' | grep -v 'richtext'                       # shared modules stay sink-free (richtext IS the sanitizer)
```

Also confirm on a diff: new render code escapes every interpolated user field
(the guard catches known field names, not new ones — read the template);
new `t()` calls with dynamic vars wrap them in `esc()` when destined for HTML;
any `sanitizeRichText` policy change landed **server-side first**; new URL
params on deployed origins don't redirect traffic or links; no new
`document.cookie` reads/writes (auth cookies are the server's).

## Deployment items (not code — flag, don't "fix")

- The XSS posture assumes the edge CSP (`script-src 'self'`, no
  `unsafe-inline`) is actually being served — covered by `go-security`'s
  frontend-edge sweep; verify after any Caddy change.
- The standalone `file://` demo mode (`USE_STANDALONE_DEMO_AUTH`) and the
  `DEV_CONTEXT` gate must never activate on a deployed origin — spot-check
  `window.location` gating if either code path changes.

## Related
- CI guards in depth → `frontend-guards` · Backend/edge invariants → `go-security`
- e2e verification → `frontend-testing` · Translations → `i18n`
