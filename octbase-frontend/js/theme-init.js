'use strict';
// Loaded synchronously in <head> (before the stylesheet) so the saved theme is
// applied before CSS paints, avoiding a flash of the wrong theme. Kept as an
// external file (not an inline <script>) so the Caddy CSP can stay
// script-src 'self' without 'unsafe-inline'. Mirrors getThemePref()/applyTheme()
// in js/framework.js. 'system' (or unset) leaves data-theme off so the
// prefers-color-scheme media query decides.
try {
  var _t = localStorage.getItem('octbase-theme');
  if (_t === 'light' || _t === 'dark' || _t === 'octopus') document.documentElement.dataset.theme = _t;
} catch (e) {}
