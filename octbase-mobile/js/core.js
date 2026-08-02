// ═══════════════════════════════════════════════════════════
// octbase-mobile — SHARED CORE
// ───────────────────────────────────────────────────────────
// Auth, HTTP client, API surface, icon set, event delegation and
// toast — ported (behaviour-wise) from the desktop app
// (octbase-frontend/js/, now split across api.js, framework.js,
// icons.js, views-task.js, etc. — the former single app.js no
// longer exists) so the mobile app speaks the exact same backend
// contracts and design language. The rich-text sanitizer is no
// longer duplicated here: it is the shared richtext.js module.
//
// Unlike the @octbase/shared modules (one package both SPAs import,
// so there is nothing left to drift), this file has no automated
// drift check against the desktop sources — review it manually
// whenever the desktop Auth/API logic changes.
// ═══════════════════════════════════════════════════════════
import { t } from '@octbase/shared/i18n.js';
import { looksLikeHTML, sanitizeRichText } from '@octbase/shared/richtext.js';

// ── CONFIG ──
const URL_PARAMS = new URLSearchParams(window.location.search);
// DEV_CONTEXT: the page runs from disk (standalone demo, tests) or a loopback
// host (local stacks/previews). URL overrides that redirect traffic or links
// (?apiBase=…, ?desktop=…) are honored only here — on a deployed origin a
// crafted link could otherwise point API calls (credentials included) or the
// "Open on desktop" links at an attacker-controlled target.
const DEV_CONTEXT = window.location.protocol === 'file:'
  || ['localhost', '127.0.0.1', '[::1]', '::1'].includes(window.location.hostname);
const API_BASE = (DEV_CONTEXT && URL_PARAMS.get('apiBase')) || (window.location.protocol === 'file:'
  ? 'http://127.0.0.1:8000'
  : window.location.origin);
const BASE_PATH = '/api/v1';
const V = BASE_PATH;
// Standalone demo mode: opened from disk (file://) → auto sign-in as the seeded
// demo user to obtain a real JWT (backend is JWT-only).
const USE_STANDALONE_DEMO_AUTH = window.location.protocol === 'file:';
const DEMO_EMAIL = 'demo@octbase.dev';
const DEMO_PASSWORD = 'demopass1234';

// Where to hand off to the full desktop app for tablet/desktop widths and for
// "Open on desktop" links. Overridable with ?desktop=… in dev contexts only,
// and only with http(s)/file targets — the value lands in href attributes, so
// a javascript: URL would execute on tap. Defaults to same-origin root (the
// desktop SPA is expected to be served there).
const DESKTOP_URL = (() => {
  const fallback = window.location.protocol === 'file:' ? '../octbase-frontend/index.html' : '/';
  const override = DEV_CONTEXT ? URL_PARAMS.get('desktop') : null;
  if (!override) return fallback;
  try {
    const u = new URL(override, window.location.href);
    if (u.protocol === 'http:' || u.protocol === 'https:' || u.protocol === 'file:') return override;
  } catch (e) { /* unparsable → fallback */ }
  return fallback;
})();

// Mirrors the Caddy front door's @phoneEntry User-Agent regex (see
// octbase-frontend/caddy/Caddyfile): a phone navigating to DESKTOP_URL (`/`)
// gets 302'd straight back to /m/, so "open on desktop" links are a dead end
// for them — hide those links on a real phone rather than offer a bounce.
// Tablets (their UA omits "Mobile") are unaffected and still see the links.
const IS_PHONE = /(iphone|ipod|android.+mobile|windows phone|blackberry|bb10|opera mini|iemobile|webos|mobile.+firefox)/i
  .test(navigator.userAgent);

function goLogin() { window.location.hash = '#/login'; }

// STATUS_META / PRIORITY_META / TYPE_META and the derived STATUSES / PRIORITIES /
// TASK_TYPES live in the shared js/meta.js (canonical source: octbase-shared/),
// loaded before this file in index.html.

// ═══════════════════════════════════════════════════════════
// ICON SET (identical registry to desktop)
// ═══════════════════════════════════════════════════════════
const ICONS = {
  home:           '<path d="M15 21v-8a1 1 0 0 0-1-1h-4a1 1 0 0 0-1 1v8"/><path d="M3 10a2 2 0 0 1 .709-1.528l7-6a2 2 0 0 1 2.582 0l7 6A2 2 0 0 1 21 10v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/>',
  board:          '<rect width="18" height="18" x="3" y="3" rx="2"/><path d="M9 3v18"/><path d="M15 3v18"/>',
  backlog:        '<path d="M3 5h.01"/><path d="M3 12h.01"/><path d="M3 19h.01"/><path d="M8 5h13"/><path d="M8 12h13"/><path d="M8 19h13"/>',
  sprint:         '<path d="M4 14a1 1 0 0 1-.78-1.63l9.9-10.2a.5.5 0 0 1 .86.46l-1.92 6.02A1 1 0 0 0 13 10h7a1 1 0 0 1 .78 1.63l-9.9 10.2a.5.5 0 0 1-.86-.46l1.92-6.02A1 1 0 0 0 11 14z"/>',
  release:        '<path d="M12.586 2.586A2 2 0 0 0 11.172 2H4a2 2 0 0 0-2 2v7.172a2 2 0 0 0 .586 1.414l8.704 8.704a2.426 2.426 0 0 0 3.42 0l6.58-6.58a2.426 2.426 0 0 0 0-3.42z"/><circle cx="7.5" cy="7.5" r=".5" fill="currentColor"/>',
  project:        '<path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z"/>',
  page:           '<path d="M6 22a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h8a2.4 2.4 0 0 1 1.704.706l3.588 3.588A2.4 2.4 0 0 1 20 8v12a2 2 0 0 1-2 2z"/><path d="M14 2v5a1 1 0 0 0 1 1h5"/><path d="M10 9H8"/><path d="M16 13H8"/><path d="M16 17H8"/>',
  search:         '<path d="m21 21-4.34-4.34"/><circle cx="11" cy="11" r="8"/>',
  settings:       '<path d="M9.671 4.136a2.34 2.34 0 0 1 4.659 0 2.34 2.34 0 0 0 3.319 1.915 2.34 2.34 0 0 1 2.33 4.033 2.34 2.34 0 0 0 0 3.831 2.34 2.34 0 0 1-2.33 4.033 2.34 2.34 0 0 0-3.319 1.915 2.34 2.34 0 0 1-4.659 0 2.34 2.34 0 0 0-3.32-1.915 2.34 2.34 0 0 1-2.33-4.033 2.34 2.34 0 0 0 0-3.831A2.34 2.34 0 0 1 6.35 6.051a2.34 2.34 0 0 0 3.319-1.915"/><circle cx="12" cy="12" r="3"/>',
  sliders:        '<path d="M10 5H3"/><path d="M12 19H3"/><path d="M14 3v4"/><path d="M16 17v4"/><path d="M21 12h-9"/><path d="M21 19h-5"/><path d="M21 5h-7"/><path d="M8 10v4"/><path d="M8 12H3"/>',
  bell:           '<path d="M10.268 21a2 2 0 0 0 3.464 0"/><path d="M3.262 15.326A1 1 0 0 0 4 17h16a1 1 0 0 0 .74-1.673C19.41 13.956 18 12.499 18 8A6 6 0 0 0 6 8c0 4.499-1.411 5.956-2.738 7.326"/>',
  menu:           '<path d="M4 5h16"/><path d="M4 12h16"/><path d="M4 19h16"/>',
  close:          '<path d="M18 6 6 18"/><path d="m6 6 12 12"/>',
  add:            '<path d="M5 12h14"/><path d="M12 5v14"/>',
  edit:           '<path d="M13 21h8"/><path d="m15 5 4 4"/><path d="M21.174 6.812a1 1 0 0 0-3.986-3.987L3.842 16.174a2 2 0 0 0-.5.83l-1.321 4.352a.5.5 0 0 0 .623.622l4.353-1.32a2 2 0 0 0 .83-.497z"/>',
  delete:         '<path d="M10 11v6"/><path d="M14 11v6"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6"/><path d="M3 6h18"/><path d="M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>',
  copy:           '<rect width="14" height="14" x="8" y="8" rx="2" ry="2"/><path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2"/>',
  refresh:        '<path d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/><path d="M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16"/><path d="M8 16H3v5"/>',
  filter:         '<path d="M10 20a1 1 0 0 0 .553.895l2 1A1 1 0 0 0 14 21v-7a2 2 0 0 1 .517-1.341L21.74 4.67A1 1 0 0 0 21 3H3a1 1 0 0 0-.742 1.67l7.225 7.989A2 2 0 0 1 10 14z"/>',
  sort:           '<path d="m3 16 4 4 4-4"/><path d="M7 20V4"/><path d="M11 4h10"/><path d="M11 8h7"/><path d="M11 12h4"/>',
  kebab:          '<circle cx="12" cy="12" r="1"/><circle cx="12" cy="5" r="1"/><circle cx="12" cy="19" r="1"/>',
  more:           '<circle cx="12" cy="12" r="1"/><circle cx="19" cy="12" r="1"/><circle cx="5" cy="12" r="1"/>',
  attach:         '<path d="m16 6-8.414 8.586a2 2 0 0 0 2.829 2.829l8.414-8.586a4 4 0 1 0-5.657-5.657l-8.379 8.551a6 6 0 1 0 8.485 8.485l8.379-8.551"/>',
  link:           '<path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/><path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>',
  view:           '<path d="M2.062 12.348a1 1 0 0 1 0-.696 10.75 10.75 0 0 1 19.876 0 1 1 0 0 1 0 .696 10.75 10.75 0 0 1-19.876 0"/><circle cx="12" cy="12" r="3"/>',
  comment:        '<path d="M22 17a2 2 0 0 1-2 2H6.828a2 2 0 0 0-1.414.586l-2.202 2.202A.71.71 0 0 1 2 21.286V5a2 2 0 0 1 2-2h16a2 2 0 0 1 2 2z"/>',
  branch:         '<path d="M15 6a9 9 0 0 0-9 9V3"/><circle cx="18" cy="6" r="3"/><circle cx="6" cy="18" r="3"/>',
  check:          '<path d="M20 6 9 17l-5-5"/>',
  'chevron-left': '<path d="m15 18-6-6 6-6"/>',
  'chevron-right':'<path d="m9 18 6-6-6-6"/>',
  'chevron-down': '<path d="m6 9 6 6 6-6"/>',
  'chevron-up':   '<path d="m18 15-6-6-6 6"/>',
  warning:        '<path d="m21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3"/><path d="M12 9v4"/><path d="M12 17h.01"/>',
  logout:         '<path d="m16 17 5-5-5-5"/><path d="M21 12H9"/><path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4"/>',
  user:           '<path d="M19 21v-2a4 4 0 0 0-4-4H9a4 4 0 0 0-4 4v2"/><circle cx="12" cy="7" r="4"/>',
  calendar:       '<path d="M8 2v4"/><path d="M16 2v4"/><rect width="18" height="18" x="3" y="4" rx="2"/><path d="M3 10h18"/>',
  time:           '<circle cx="12" cy="12" r="10"/><path d="M12 6v6l4 2"/>',
  archive:        '<rect width="20" height="5" x="2" y="3" rx="1"/><path d="M4 8v11a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8"/><path d="M10 12h4"/>',
  external:       '<path d="M15 3h6v6"/><path d="M10 14 21 3"/><path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/>',
  download:       '<path d="M12 15V3"/><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><path d="m7 10 5 5 5-5"/>',
  dot:            '<circle cx="12.1" cy="12.1" r="1"/>',
  pin:            '<path d="M12 17v5"/><path d="M9 10.76a2 2 0 0 1-1.11 1.79l-1.78.9A2 2 0 0 0 5 15.24V16a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1v-.76a2 2 0 0 0-1.11-1.79l-1.78-.9A2 2 0 0 1 15 10.76V7a1 1 0 0 1 1-1 2 2 0 0 0 0-4H8a2 2 0 0 0 0 4 1 1 0 0 1 1 1z"/>',
};
const ICON_PX = { sm: 16, md: 20, hero: 48 };
function iconPx(size) {
  if (typeof size === 'number') {
    if (size >= 34) return ICON_PX.hero;
    return size <= 17 ? ICON_PX.sm : ICON_PX.md;
  }
  return ICON_PX[size] ?? ICON_PX.md;
}
function icon(name, { size = 'md', cls = '' } = {}) {
  const px = iconPx(size);
  const tok = px === ICON_PX.sm ? 'sm' : px === ICON_PX.hero ? 'hero' : 'md';
  const path = ICONS[name] || ICONS.dot;
  return `<svg class="icon-svg icon-svg--${tok}${cls ? ' ' + cls : ''}" width="${px}" height="${px}" `
    + `viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" `
    + `stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false">${path}</svg>`;
}
function iconBtn(name, label, { attrs = '', size = 'md', cls = '' } = {}) {
  const e = String(label).replace(/"/g, '&quot;');
  return `<button type="button" class="icon-btn${cls ? ' ' + cls : ''}" `
    + `${attrs} aria-label="${e}" title="${e}">${icon(name, { size })}</button>`;
}

// ═══════════════════════════════════════════════════════════
// AUTH MODULE — token in memory only (never localStorage)
// ═══════════════════════════════════════════════════════════
const Auth = (() => {
  let _accessToken = null;
  let _refreshing = null;
  // Non-HttpOnly companion cookie the backend sets next to the HttpOnly refresh
  // cookie (same expiry). Reading it lets bootstrap skip the /auth/refresh probe
  // — and the 401 it would log — for visitors with no live session.
  const SESSION_PRESENT_COOKIE = 'refresh_present';
  return {
    get token() { return _accessToken; },
    set token(v) { _accessToken = v; },
    // hasSessionHint reports whether the refresh-cookie presence marker is set,
    // i.e. whether a session may be restorable. Tracks the cookie's lifetime.
    hasSessionHint() {
      return document.cookie.split('; ').some(c => c === SESSION_PRESENT_COOKIE + '=1');
    },
    headers() {
      const h = { 'Content-Type': 'application/json' };
      if (_accessToken) h['Authorization'] = 'Bearer ' + _accessToken;
      return h;
    },
    async demoLogin() {
      const r = await fetch(API_BASE + BASE_PATH + '/auth/login', {
        method: 'POST', credentials: 'include',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ email: DEMO_EMAIL, password: DEMO_PASSWORD }),
      });
      if (!r.ok) throw new Error('demo_login_failed');
      const d = await r.json();
      _accessToken = d.accessToken;
      return true;
    },
    async refresh() {
      if (_refreshing) return _refreshing;
      _refreshing = fetch(API_BASE + BASE_PATH + '/auth/refresh', {
        method: 'POST', credentials: 'include',
      }).then(async r => {
        if (!r.ok) throw new Error('refresh_failed');
        const d = await r.json();
        _accessToken = d.accessToken;
        return true;
      }).finally(() => { _refreshing = null; });
      return _refreshing;
    },
    async logout() {
      _accessToken = null;
      if (USE_STANDALONE_DEMO_AUTH) { goLogin(); return; }
      await fetch(API_BASE + BASE_PATH + '/auth/logout', {
        method: 'POST', credentials: 'include',
      }).catch(() => {});
      goLogin();
    },
    isAuthenticated() { return USE_STANDALONE_DEMO_AUTH || !!_accessToken; },
  };
})();

// ═══════════════════════════════════════════════════════════
// HTTP CLIENT
// ═══════════════════════════════════════════════════════════
class ApiError extends Error {
  constructor(message, code, status, details) {
    super(message);
    this.name = 'ApiError';
    this.code = code;
    this.status = status;
    this.details = details;
    if (details && typeof details.field === 'string') this.apiField = details.field;
  }
}
async function errorFromResponse(r) {
  const raw = await r.text();
  try {
    const body = JSON.parse(raw);
    if (body && body.message) {
      const err = new ApiError(body.message, body.code, r.status, body.details);
      if (body.messageKey) err.messageKey = body.messageKey;
      return err;
    }
  } catch { /* not JSON */ }
  return new ApiError(raw || r.statusText || `Request failed (${r.status})`, undefined, r.status);
}
function apiErrorMessage(e) {
  if (e && e.messageKey) {
    const translated = t(e.messageKey);
    if (translated !== e.messageKey) return translated;
  }
  return e?.message || t('errors.generic');
}

// The routes the backend serves without a Bearer token (see CLAUDE.md "Auth").
// Everything else needs a live session, so requesting one while signed out can
// only produce a 401. Mirrors octbase-frontend/js/http.js; this SPA reaches a
// smaller public surface (no config or invitation routes).
const PUBLIC_PATHS = [
  `${BASE_PATH}/auth/login`,
  `${BASE_PATH}/auth/mfa/verify`,
  `${BASE_PATH}/auth/forgot-password`,
  `${BASE_PATH}/auth/reset-password`,
];

function isPublicPath(path) {
  return PUBLIC_PATHS.includes(path.split('?')[0]);
}

function sessionExpired() {
  Auth.token = null;
  goLogin();
  return new ApiError(t('errors.sessionExpired'), 'SESSION_EXPIRED', 401);
}

// requireSession fails a request locally when it needs a session we demonstrably
// do not have, instead of provoking a 401. A null token alone is not proof — it
// is also the normal state during boot, before the refresh resolves — so the
// refresh-cookie presence marker settles it.
function requireSession(path) {
  if (isPublicPath(path)) return null;
  if (Auth.token || Auth.hasSessionHint()) return null;
  return sessionExpired();
}

// Codes an AUTHENTICATED route may answer 401 with that say nothing about the
// session — a credential in the request body was wrong, not the session.
// Without this, change-password logged the user out for a mistyped current
// password. Mirrors octbase-frontend/js/http.js; keep the two in step.
const CREDENTIAL_ANSWER_CODES = new Set(['CURRENT_PASSWORD_INVALID']);

const http = {
  async _fetch(method, path, body, isRetry = false) {
    const blocked = requireSession(path);
    if (blocked) throw blocked;
    const opts = { method, headers: Auth.headers(), credentials: 'include' };
    if (body !== undefined) opts.body = JSON.stringify(body);
    const r = await fetch(API_BASE + path, opts);
    // A 401 from a public route is that route's own answer — a wrong password,
    // a bad reset token — and must reach the caller instead of being recast as
    // an expired session.
    if (r.status === 401 && !isPublicPath(path)) {
      // Read the body before judging: an authenticated route can answer 401
      // about a credential in the request rather than about the session, and
      // recasting that as an expired session signs the user out for a typo.
      // Mirrors octbase-frontend/js/http.js.
      const err = await errorFromResponse(r);
      if (CREDENTIAL_ANSWER_CODES.has(err.code)) throw err;
      // Only a session that might still be restorable is worth a refresh round
      // trip; without the marker the refresh can only 401 in turn.
      if (!isRetry && Auth.hasSessionHint()) {
        try {
          await Auth.refresh();
          return this._fetch(method, path, body, true);
        } catch {
          throw sessionExpired();
        }
      }
      throw sessionExpired();
    }
    if (!r.ok) throw await errorFromResponse(r);
    if (r.status === 204) return null;
    return r.json();
  },
  get(path)        { return this._fetch('GET', path); },
  post(path, body) { return this._fetch('POST', path, body); },
  patch(path,body) { return this._fetch('PATCH', path, body); },
  del(path)        { return this._fetch('DELETE', path); },

  // getBlob fetches binary content (e.g. an avatar image) with the bearer token
  // attached, since a plain <img src> cannot carry it. Mirrors the desktop SPA.
  async getBlob(path, isRetry = false) {
    const blocked = requireSession(path);
    if (blocked) throw blocked;
    const headers = {};
    if (Auth.token) headers['Authorization'] = 'Bearer ' + Auth.token;
    const r = await fetch(API_BASE + path, { method: 'GET', headers, credentials: 'include' });
    if (r.status === 401 && !isRetry && Auth.hasSessionHint()) {
      try { await Auth.refresh(); return this.getBlob(path, true); } catch { throw sessionExpired(); }
    }
    if (!r.ok) throw await errorFromResponse(r);
    return r.blob();
  },

  // upload posts a single file as multipart/form-data under the "file" part.
  // Content-Type is left unset so the browser adds the multipart boundary.
  async upload(path, file, isRetry = false) {
    const blocked = requireSession(path);
    if (blocked) throw blocked;
    const fd = new FormData();
    fd.append('file', file, file.name || 'upload');
    const headers = {};
    if (Auth.token) headers['Authorization'] = 'Bearer ' + Auth.token;
    const r = await fetch(API_BASE + path, { method: 'POST', headers, body: fd, credentials: 'include' });
    if (r.status === 401 && !isRetry && Auth.hasSessionHint()) {
      try { await Auth.refresh(); return this.upload(path, file, true); } catch { throw sessionExpired(); }
    }
    if (!r.ok) throw await errorFromResponse(r);
    return r.status === 204 ? null : r.json();
  },
};

function qs(p={}) {
  const s = new URLSearchParams();
  Object.entries(p).forEach(([k,v])=>{ if(v!=null && v!=='') s.append(k,v); });
  const str = s.toString();
  return str ? '?'+str : '';
}

// ═══════════════════════════════════════════════════════════
// API — all paths prefixed with /api/v1 (mirrors desktop)
// ═══════════════════════════════════════════════════════════
const api = {
  auth: {
    login: (email, password) => http.post(`${V}/auth/login`, {email,password}),
    verifyMfa: (challengeToken, code) => http.post(`${V}/auth/mfa/verify`, {challengeToken, code}),
    me:    ()                 => http.get(`${V}/auth/me`),
    logout:()                 => Auth.logout(),
    // 204 on success. Other sessions are revoked server-side; this device's
    // refresh cookie is re-issued so the user stays signed in here.
    changePassword: (currentPassword, newPassword) =>
      http.post(`${V}/auth/change-password`, {currentPassword, newPassword}),
  },
  // Personal dashboard: language/theme (internal/dashboard).
  preferences: {
    get:    ()   => http.get(`${V}/users/me/preferences`),
    update: (d)  => http.patch(`${V}/users/me/preferences`, d),
  },
  // MFA enrollment/management (internal/security/mfa) — a separate backend
  // module from preferences above; mirrors octbase-frontend/js/api.js.
  mfa: {
    enroll:  (password) => http.post(`${V}/users/me/mfa/enroll`, {password}),
    confirm: (code)  => http.post(`${V}/users/me/mfa/confirm`, {code}),
    disable: (d)     => http.post(`${V}/users/me/mfa/disable`, d),
    regenerateRecoveryCodes: (d) => http.post(`${V}/users/me/mfa/recovery-codes/regenerate`, d),
  },
  projects: {
    list:   ()      => http.get(`${V}/projects`),
    get:    (id)    => http.get(`${V}/projects/${id}`),
  },
  priorities: {
    // The project's custom priorities; the built-in set is static (PRIORITIES).
    list: (pid) => http.get(`${V}/projects/${pid}/task-priorities`),
  },
  tasks: {
    list:    (pid,p={}) => http.get(`${V}/projects/${pid}/tasks${qs(p)}`),
    // listAll pages through the whole project instead of reading the API's
    // 200-row maximum once. The list is sorted created_at DESC, so a single
    // page drops a large project's OLDEST tasks — the parents everything else
    // hangs from — with nothing on screen saying so. Mirrors the desktop
    // helper; pages until a short page arrives, so a project under 200 tasks
    // still costs exactly one request. MAX_PAGES is the runaway guard.
    listAll: async (pid, p = {}) => {
      const MAX_PAGES = 25;
      const size = 200;
      const out = [];
      for (let page = 0; page < MAX_PAGES; page++) {
        const batch = await http.get(`${V}/projects/${pid}/tasks${qs({ ...p, page, size })}`);
        if (!Array.isArray(batch) || batch.length === 0) break;
        out.push(...batch);
        if (batch.length < size) break;
      }
      return out;
    },
    get:     (id)       => http.get(`${V}/tasks/${id}`),
    create:  (pid,d)    => http.post(`${V}/projects/${pid}/tasks`, d),
    update:  (id,d)     => http.patch(`${V}/tasks/${id}`, d),
    // version (optional) is the task version the change is based on; a stale
    // version is rejected with 409 VERSION_CONFLICT instead of overwriting a
    // concurrent editor (undefined is dropped by JSON.stringify).
    status:  (id,s,version)     => http.post(`${V}/tasks/${id}/status`, {status:s, version}),
    priority:(id,p,version)     => http.post(`${V}/tasks/${id}/priority`, {priority:p, version}),
    assign:  (id,d)     => http.post(`${V}/tasks/${id}/assign`, d),
    reopen:  (id)       => http.post(`${V}/tasks/${id}/reopen`, {}),
    archive: (id)       => http.post(`${V}/tasks/${id}/archive`, {}),
  },
  comments: {
    list:   (tid)       => http.get(`${V}/tasks/${tid}/comments`),
    add:    (tid,txt)   => http.post(`${V}/tasks/${tid}/comments`, {text:txt}),
  },
  boards: {
    getDefault: (pid)    => http.get(`${V}/projects/${pid}/boards/default`),
    move:       (bid,d)  => http.post(`${V}/boards/${bid}/move-task`, d),
  },
  backlog: { get: (pid) => http.get(`${V}/projects/${pid}/backlog`) },
  sprints: { list: (pid) => http.get(`${V}/projects/${pid}/sprints`) },
  releases:{ list: (pid) => http.get(`${V}/projects/${pid}/releases`) },
  members: {
    list: (pid) => http.get(`${V}/projects/${pid}/members`),
    // Assignee candidates: the project's members plus the global admins, who
    // reach the project without holding a membership row.
    assignable: (pid) => http.get(`${V}/projects/${pid}/assignable-users`),
  },
  users: {
    // v is the avatarUpdatedAt cache token: the server sends the bytes with a
    // 24h Cache-Control, so without a per-version URL the browser would keep
    // serving a replaced avatar's old bytes. The token makes each version a
    // distinct, hard-cacheable URL.
    avatarBlob:   (id,v) => http.getBlob(`${V}/users/${id}/avatar${v ? `?v=${encodeURIComponent(v)}` : ''}`),
    uploadAvatar: (file) => http.upload(`${V}/users/me/avatar`, file),
    deleteAvatar: ()     => http.del(`${V}/users/me/avatar`),
  },
  notifications: {
    list:    (p={})  => http.get(`${V}/users/me/notifications${qs(p)}`),
    readAll: ()      => http.post(`${V}/users/me/notifications/read-all`, {}),
    markRead:(id)    => http.patch(`${V}/users/me/notifications/${id}`, {isRead:true}),
  },
  search:   (q, projectId) => http.get(`${V}/search${qs(projectId ? {q, projectId} : {q})}`),
  dashboard:() => http.get(`${V}/users/me/dashboard`),
};

// ═══════════════════════════════════════════════════════════
// HTML ESCAPE + TEMPLATING
// ═══════════════════════════════════════════════════════════
function esc(s) {
  if(!s) return '';
  return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}
function raw(s) { return { __raw: s == null ? '' : String(s) }; }
function html(strings, ...values) {
  let out = strings[0];
  for (let i = 0; i < values.length; i++) {
    let v = values[i];
    if (Array.isArray(v)) v = v.map(x => (x && x.__raw !== undefined) ? x.__raw : esc(x)).join('');
    else if (v && v.__raw !== undefined) v = v.__raw;
    else v = esc(v == null ? '' : v);
    out += v + strings[i + 1];
  }
  return out;
}

// ═══════════════════════════════════════════════════════════
// RICH-TEXT (TASK DESCRIPTION)
// ═══════════════════════════════════════════════════════════
// The sanitizer (sanitizeRichText, rtSafeHref, rtSafeImageSrc, looksLikeHTML)
// is the shared, DOMPurify-backed richtext.js module — see
// octbase-shared/richtext.js. Only the esc()-dependent display helper lives
// here.
function renderDescriptionHTML(desc) {
  if (!desc) return '';
  if (looksLikeHTML(desc)) return sanitizeRichText(desc);
  return esc(desc).replace(/\n/g, '<br>');
}

// ═══════════════════════════════════════════════════════════
// EVENT DELEGATION (data-act / data-change / data-input / data-submit)
//   — registry keyed by fn.name; do NOT mangle identifiers when minifying.
// ═══════════════════════════════════════════════════════════
const ACTIONS  = {};
const CHANGES  = {};
const INPUTS   = {};
const SUBMITS  = {};
function _dispatch(registry, key, ev) {
  const node = ev.target.closest('[data-' + key + ']');
  if (!node) return;
  const fn = registry[node.dataset[key]];
  if (fn) fn(node, ev);
}
const _A0 = (fn, el) => fn();
const _A1 = (fn, el) => fn(el.dataset.a0);
const _A2 = (fn, el) => fn(el.dataset.a0, el.dataset.a1);
const _A3 = (fn, el) => fn(el.dataset.a0, el.dataset.a1, el.dataset.a2);
const _VAL  = (fn, el) => fn(el.dataset.a0, el.value);
const _VAL0 = (fn, el) => fn(el.value);
function _register(registry, fns, adapter) {
  fns.forEach(fn => { registry[fn.name] = (el, ev) => adapter(fn, el, ev); });
}

// ═══════════════════════════════════════════════════════════
// DOM HELPERS + TOAST
// ═══════════════════════════════════════════════════════════
function el(sel) { return document.querySelector(sel); }
function els(sel) { return Array.from(document.querySelectorAll(sel)); }
function toast(msg, type='info') {
  const node = document.createElement('div');
  node.className = `toast toast-${type}`;
  node.textContent = msg;
  const container = el('#toast-container');
  if (!container) return;
  container.setAttribute('aria-live', type === 'error' ? 'assertive' : 'polite');
  container.appendChild(node);
  setTimeout(() => node.remove(), 3500);
}

// Initials for an avatar from a display name / email.
function initials(nameOrEmail) {
  const s = (nameOrEmail || '').trim();
  if (!s) return '?';
  if (s.includes('@')) return s[0].toUpperCase();
  const parts = s.split(/\s+/).filter(Boolean);
  return ((parts[0]?.[0] || '') + (parts[1]?.[0] || '')).toUpperCase() || s[0].toUpperCase();
}

export { ACTIONS, API_BASE, ApiError, Auth, BASE_PATH, CHANGES, DEMO_EMAIL, DEMO_PASSWORD, DESKTOP_URL, DEV_CONTEXT, ICONS, ICON_PX, INPUTS, IS_PHONE, PUBLIC_PATHS, SUBMITS, URL_PARAMS, USE_STANDALONE_DEMO_AUTH, V, _A0, _A1, _A2, _A3, _VAL, _VAL0, _dispatch, _register, api, apiErrorMessage, el, els, errorFromResponse, esc, goLogin, html, http, icon, iconBtn, iconPx, initials, isPublicPath, qs, raw, renderDescriptionHTML, requireSession, sessionExpired, toast };
