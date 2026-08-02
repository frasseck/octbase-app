'use strict';
// Loaded synchronously in <head> (before the stylesheet) so the saved theme is
// applied before CSS paints, avoiding a flash of the wrong theme. Copied
// verbatim from octbase-frontend/js/theme-init.js — mobile and desktop are
// reverse-proxied under the same origin (/m/ vs /), so they share the
// 'octbase-theme' localStorage key already used by desktop's THEME_KEY.
// 'system' (or unset) leaves data-theme off so the prefers-color-scheme
// media query decides. Mirrors getThemePref()/applyTheme() in js/app.js.
try {
  var _t = localStorage.getItem('octbase-theme');
  if (_t === 'light' || _t === 'dark' || _t === 'octopus') document.documentElement.dataset.theme = _t;
} catch (e) {}
