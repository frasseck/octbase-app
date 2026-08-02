You are a Senior Frontend Engineer and JavaScript expert reviewing `octbase-frontend/js/app.js`. Be precise and critical — find real usability bugs, not stylistic nitpicks.

## Bug 1 — User Management search field loses focus on every keystroke

In `_renderUserTable()` (admin user list), the search `<input id="admin-search">` has
`oninput="AdminState.search=this.value;_refilterUsers()"`. `_refilterUsers()` calls
`_renderUserTable(el('#content'), cached)`, which rewrites `c.innerHTML` for the **entire**
admin panel — including the search input itself. Replacing `innerHTML` destroys and recreates
the `<input>` DOM node, so the browser drops focus and the caret position after every single
character typed. The user can effectively only type one character at a time.

Fix this so the input keeps focus (and cursor position/selection) while the user types,
without doing a full-page re-render on every keystroke. Prefer the smallest correct fix:
re-render only the user list/stats/filter-count subtree (e.g. give the list and stats areas
their own container(s) and update those via `innerHTML`, or update the DOM surgically),
leaving the `#admin-search` input and the filter `<select>` elements untouched. Preserve
existing behavior (role/status filters, the "filtered/total" count, empty state).

## Bug 2 — Notification panel does not close on outside click

`toggleNotifPanel()` shows/hides `#notif-panel` but there is no listener that closes it when
the user clicks elsewhere on the page (the project-settings menu at `toggleProjectMenu()`
already implements this correctly via a one-time `document` click listener with
`e.stopPropagation()` on the toggle button — use the same pattern). Currently the panel only
closes via the bell button toggle, "mark all read", clicking a notification, or navigating to
preferences. Add outside-click-to-close behavior consistent with the project menu, without
breaking clicks on items inside the panel (notif items, "mark all read", preferences link) or
re-introducing the focus-loss pattern from Bug 1.

## General review

Scan the rest of `app.js` for:
- Other places that call `el(...).innerHTML = ...` (or full-section re-renders) on a container
  that currently holds a focused `<input>`/`<textarea>`/`<select>`, which would cause the same
  focus/cursor loss as Bug 1 (e.g. search/filter inputs on other pages, inline-edit fields,
  modal forms triggered by `oninput`/`onkeyup`).
- Other dropdowns/panels/popovers opened via a toggle (besides the project menu) that should
  close on outside click but don't, or that register `document` click listeners without
  cleanup/`stopPropagation`, risking duplicate listeners or immediate self-closing.
- Any other small usability bugs around keyboard focus (e.g. modals not focusing/restoring
  focus, Escape not closing things that Escape closes elsewhere, focus traps).

List findings with file:line references, then fix the ones that are genuine, low-risk bugs
matching the patterns above. Don't refactor unrelated code or add new abstractions/tests
beyond what's needed to verify the fixes manually.
