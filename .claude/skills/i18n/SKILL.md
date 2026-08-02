---
name: i18n
description: Add or change UI translation strings across Octbase's static frontends (octbase-frontend, octbase-mobile). Covers the locales/*.json files, the language loader and localStorage key, and the supported locale set (English and German). Use whenever adding user-facing text, a new string key, or a new language.
---

# Internationalization (i18n)

The static frontends each carry their own `locales/<lang>.json` dictionaries.
There is **no shared locale package** — each site ships its own set on purpose
(mobile ships a reduced one), and that stayed true when the *engine* became
shared: `@octbase/shared/i18n.js` is one module both SPAs import (37b stage 3),
but it fetches locales relative to the page it runs on.

| Site | Locales present |
|---|---|
| `octbase-frontend/locales/` | `en`, `de` |
| `octbase-mobile/locales/`   | `en`, `de` |

⚠️ **French was removed.** Both sites ship only `en` and `de`; the loader's
`AVAILABLE_LOCALES = ['en', 'de']` (`octbase-shared/i18n.js`) and a stored
`fr` preference falls back to English. Do **not** add an `fr.json` — there is no
French support to keep in sync.

## How language loading works (`octbase-shared/i18n.js`)

- Active language is stored in `localStorage` under key **`octbase.lang`**.
- On load it fetches `locales/<lang>.json` (via `fetch`, falling back to XHR for
  `file://`). `en` is the default.
- `i18n.js` has unit coverage in `octbase-frontend/js/i18n.test.js` (which loads
  it from `../../octbase-shared/`) — keep it green when changing the loader.

## The `classic` vocabulary overlay

Terminology is a **second axis, orthogonal to language**: the same locale file
carries a `classic` namespace that mirrors any key whose wording is agile
(sprint, backlog, epic, story, story points, release). A user picks the vocabulary in
personal settings (`terminology`: `AGILE` | `CLASSIC`, stored per user in
`user_preferences` and locally under **`octbase.terminology`**).

- `resolve()` in `octbase-shared/i18n.js` tries `classic.<key>` in the active
  language first, then the plain key, then English. **A key with no classic
  variant keeps its agile wording** — partial coverage degrades to the base
  string, never to a raw key path.
- The overlay is looked up in the active language only: for a German reader a
  missing classic term falls back to the German agile word, not an English
  classic one.
- `setTerminology()` loads nothing (the overlay ships inside the locale file
  already in memory), so switching is a synchronous relabel — callers repaint.

⚠️ **Adding a string that says "sprint", "backlog", "epic", "story", "story
points" or "release" means adding its `classic` counterpart too**, in every locale file for
that site. A unit test in `i18n.test.js` enumerates agile vocabulary in
`en.json` and fails when a classic variant is missing; its allowlist (with
reasons) is the only escape hatch, and it is currently **empty** — keep it that
way if you can. `burndown` and `velocity` are untranslated by being absent from
the term list, not exempted: they are agile metrics with no classic name, while
every string that merely mentions one (a sprint burndown, a velocity in story
points) does carry a classic wording.

The mapping in use: sprint → phase, backlog → task pool, epic → work package,
story → requirement, story points → effort points, release → milestone.

## Adding or changing a string

1. Add the key to **every** locale file for that site (`en` and `de`, on both
   octbase-frontend and octbase-mobile), keeping the JSON key structure identical
   across files. A key present in one language but missing in another renders as
   the raw key path in the UI.

   Both halves of that rule are enforced by `scripts/check-i18n-keys.mjs` (CI,
   "Frontend checks"): every literal `t('…')` key in that site's JS — plus
   `octbase-shared`, whose keys both SPAs owe — must exist in each of that
   site's locale files, and the locale files must carry the same key set. Run
   it before pushing; it prints the call sites of anything it cannot resolve.
   Neither failure is visible by looking at the app: a missing key renders
   English (the fallback locale, or the `t('x') !== 'x' ? … : 'English'` idiom
   at the call site), never something obviously broken.

   ⚠️ **Identical key structure does not mean identical wording.** The mobile
   companion **may** ship a shorter label where the desktop wording does not fit
   a phone cell — through **its own key** in `octbase-mobile/locales`, never by
   editing a shared one. Shipped divergences: `settings.terminologies.CLASSIC`
   is "Classic project management" on desktop and "Classic" on mobile; enabling
   MFA is "Enable two-factor authentication" vs "Enable 2FA"; `nav.notifications`
   is German "Nachrichten" while the view title stays "Benachrichtigungen".
   The limit is strict — **the short form must mean the same thing**: abbreviate
   or drop a qualifier, never rename the concept, and never let the two surfaces
   use different vocabulary for the same object. Both of that site's locales move
   together, and the change gets a changelog entry like any other user-visible
   text. Normative wording: `octbase-frontend/styleguide.html`, design principle
   4. **A test that asserts a mobile label must assert the mobile string** — the
   e2e suite was red for a week because one compared the phone against the
   desktop wording.
2. Use the key in markup/JS the same way existing strings are referenced.
3. Verify visually per language by setting `localStorage['octbase.lang']` and
   reloading — see the `frontend-testing` skill for the Playwright setup. If
   i18n keys render raw (e.g. `impressum.sections.info.heading`), the page was
   loaded from `file://` — serve over HTTP (`python3 -m http.server`) instead,
   since Chrome blocks `fetch()` of local JSON under `file://`.

## Adding a new language

Copy `en.json` to `<lang>.json` in each site you're adding it to, translate all
values, and add the language to `AVAILABLE_LOCALES` in `octbase-shared/i18n.js`
— one edit now covers both SPAs (the switcher is data-driven off it), so a
language added there must have a locale file in **every** site or that site
offers a language it cannot load. Both sites currently ship the same set
(`en`, `de`); keep them aligned unless there's a reason to diverge.

## Related

- Visual verification / screenshots → `frontend-testing` skill
