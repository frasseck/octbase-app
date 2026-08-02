# 100 — Octbase Frontend: app.js maintainability refactor

Refactor the vanilla-JS single-page app at `octbase-frontend/js/app.js` (~4.4k
lines) to be more maintainable and less error-prone, **without** adding a
framework or build step. The two highest-leverage problems were:

1. ~140 inline `on*="..."` handlers that built executable JS inside HTML
   attributes (e.g. `onclick="deleteTask('${id}','${title}')"`). This forced
   handler functions to live on the global namespace and created a string-
   interpolation / XSS hazard — any value containing a quote or markup could
   break the handler or inject code.
2. Inconsistent HTML escaping of user/server data interpolated into the ~57
   `innerHTML` template strings.

## What was done

### Event delegation (replaces all inline handlers)
- Added a document-level delegation system: a single set of listeners for
  `click` / `change` / `input` / `keydown` / `submit` plus drag events, in
  `initDelegation()`.
- Elements now declare behaviour via `data-act` / `data-change` / `data-input`
  / `data-keydown` / `data-submit`, with arguments in `data-a0` / `data-a1` /
  `data-a2`. Input handlers read `value` / `checked` from the element.
- Handlers are looked up in registries (`ACTIONS`, `CHANGES`, `INPUTS`,
  `KEYDOWNS`, `SUBMITS`) populated once at startup by `registerActions()`.
  Handler functions no longer need to be global.
- All **~140** inline handlers were converted (109 `onclick`, 22 `onchange`,
  3 `oninput`, 4 `onkeydown`, 2 `onsubmit`, 5 drag). Zero `on*=` attributes
  remain.
- Argument values now travel in HTML-attribute context escaped via `esc()`,
  removing the JS-injection hazard.
- A no-op `stop` action preserves the click-isolation that
  `event.stopPropagation()` used to provide (e.g. a checkbox inside a clickable
  card), since delegation halts at the nearest `data-act`.

### Escaping
- Added an auto-escaping `` html`` `` tagged template plus a `raw()` opt-out, so
  future templates escape interpolations by default.
- Fixed concrete unescaped user-text interpolations: `avatarHtml` title +
  initials, project-abbreviation sequence labels, admin avatar initials, and a
  double-`esc` introduced during the mechanical conversion.

### Language switcher
- Converted the locale switcher from `<a>` links to `<select id="lang-select">`
  dispatched through the same delegation path (`data-change="langSelect"`).

### Tests
- Updated `tests/test_task_panel.py` selectors from `select[onchange*='...']`
  to `select[data-change='...']` to match the new markup.
- Validated against the full Playwright suite (system Chrome): board
  drag/cards/lanes, task-panel status/priority/type dropdowns, members
  role-change, pages view/edit toggle, search navigation, and i18n language
  switching all pass.

## Explicitly out of scope
- No framework / library (jQuery, etc.) and no build step were introduced —
  native delegation + ESM-ready structure was the right fit.
- The wholesale migration of all ~57 `innerHTML` templates to the new `` html`` ``
  helper was **not** done in this pass; it carries double-escaping regression
  risk and is better done incrementally. The `html` / `raw` helpers are in
  place for that future work.

## Result
- `node --check js/app.js` clean; no inline `on*=` handlers remain.
- Committed on branch `refactor/event-delegation-escaping` (off `release_v2`)
  as `refactor(frontend): replace inline on* handlers with event delegation`,
  touching only `js/app.js` and `tests/test_task_panel.py`.
