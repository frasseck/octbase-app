import { ICONS, icon } from './icons.js';

// Renders the icon grid on styleguide.html.
//
// The grid is generated from the SHIPPED registry — js/icons.js, imported
// directly — so it cannot drift from the app. It used to be a
// hand-maintained NAMES array pointing at <use href="#i-…"> symbols defined in
// styleguide.html, and it had drifted in both directions: thirteen shipped
// icons were undocumented, the names had forked (chev-left here vs
// chevron-left in the app), and 'stats' was listed with no matching symbol, so
// that cell rendered blank. Reading Object.keys(ICONS) makes the guide *be* the
// registry rather than a second copy of it, which is the only version of this
// that stays true without a CI guard policing it.
//
// External file (not inline) so the Caddy CSP can stay script-src 'self'.
//
// Reads the IMPORTED bindings, not `window.ICONS`/`window.icon`. It read the
// globals until 37b stage 6: they existed for free while icons.js was a classic
// script, and stage 2's ESM conversion removed them without anything noticing —
// `Object.keys(undefined)` threw and the grid rendered empty. ESLint's
// no-unused-vars found it, by observing that the imports above had no readers.
const grid = document.getElementById('icongrid');
const names = Object.keys(ICONS).sort();
grid.innerHTML = names.map(n =>
  `<div class="icon-cell">${icon(n, { size: 'md' })}<span>${n}</span></div>`).join('');
const count = document.getElementById('icon-count');
if (count) count.textContent = String(names.length);
