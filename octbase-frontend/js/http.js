// @ts-check
import { t } from '@octbase/shared/i18n.js';
import { Auth } from './auth.js';
import { API_BASE, BASE_PATH } from './env.js';
import { endSession } from './realtime.js';
import { router } from './router.js';

// Octbase SPA — split from the former single app.js (and later from api.js,
// which had grown to conflate auth, the HTTP client, the REST surface, the
// router, and permission helpers). One ES module among many, bundled by Vite
// (37b stage 2): its top-level declarations are file-private and its public
// surface is the `export { … }` block at the bottom. Imports carry the
// dependencies — there is no load order to keep in step (js/README.md).

// ═══════════════════════════════════════════════════════════
// HTTP CLIENT
// ═══════════════════════════════════════════════════════════

// ApiError carries the backend's human-readable message and machine code so
// callers can show a friendly toast (err.message) and still branch on err.code.
class ApiError extends Error {
  constructor(message, code, status, details) {
    super(message);
    this.name = 'ApiError';
    this.code = code;
    this.status = status;
    this.details = details;
    /**
     * Stable i18n key some errors carry alongside the human message, so the UI
     * can translate rather than echo the server's English. Declared here (not
     * only where it is assigned) so the checker knows the field exists.
     * @type {string|undefined}
     */
    this.messageKey = undefined;
    /** @type {string|undefined} */
    this.apiField = undefined;
    // The backend returns a field name in details.field for validation
    // errors so the frontend can highlight the matching form input
    // (WCAG 3.3.1 Error Identification). Modal forms map this to their
    // own element IDs via FIELD_ID_MAP (see showModal/setModalFieldError).
    if (details && typeof details.field === 'string') this.apiField = details.field;
  }
}

// errorFromResponse builds an ApiError from a failed response, preferring the
// API's standard { code, message, messageKey } JSON body and falling back to
// status text.
async function errorFromResponse(r) {
  const raw = await r.text();
  try {
    const body = JSON.parse(raw);
    if (body && body.message) {
      const err = new ApiError(body.message, body.code, r.status, body.details);
      if (body.messageKey) err.messageKey = String(body.messageKey);
      return err;
    }
  } catch { /* not JSON — fall through to raw/status text */ }
  return new ApiError(raw || r.statusText || `Request failed (${r.status})`, undefined, r.status);
}

// apiErrorMessage returns a localized message for an ApiError when the
// backend provided a stable messageKey, falling back to the raw English
// message for network errors or unmapped keys.
function apiErrorMessage(e) {
  if (e && e.messageKey) {
    const translated = t(e.messageKey);
    if (translated !== e.messageKey) return translated;
  }
  return e?.message || t('errors.generic');
}

// The routes the backend serves without a Bearer token (see CLAUDE.md "Auth").
// Everything else needs a live session, which is what lets requireSession below
// tell "this request can succeed signed out" from "this one can only 401".
//
// This list is NOT an authorization boundary and must never be treated as one:
// it never strips the Authorization header and never bypasses a server check —
// the backend authorizes every request either way. Getting an entry wrong costs
// at worst a missed auto-logout, never access. Routes that use raw fetch rather
// than this client (refresh, logout, invitation accept) are absent by design.
const PUBLIC_PATHS = [
  `${BASE_PATH}/auth/login`,
  `${BASE_PATH}/auth/mfa/verify`,
  `${BASE_PATH}/auth/forgot-password`,
  `${BASE_PATH}/auth/reset-password`,
  `${BASE_PATH}/config`,
];
// Invitation inspection is public but token-bearing, so it needs a pattern.
const PUBLIC_PATH_RE = new RegExp(`^${BASE_PATH}/invitations/[^/]+$`);

function isPublicPath(path) {
  const clean = path.split('?')[0];
  return PUBLIC_PATHS.includes(clean) || PUBLIC_PATH_RE.test(clean);
}

// Codes an AUTHENTICATED route may answer 401 with that say nothing about the
// session. The session is fine; a credential *in the request body* was wrong.
//
// Without this, change-password logged you out for a typo: the 401 looked like
// an expired session, so the client refreshed (which worked — the session was
// live), retried, got the same 401, and gave up by ending the session. The user
// mistyped their current password and landed on the login screen.
//
// Keep this list to codes that are demonstrably about a body field. Anything
// else on an authenticated route still means "your session is gone".
const CREDENTIAL_ANSWER_CODES = new Set(['CURRENT_PASSWORD_INVALID']);

// sessionExpired ends the session locally and sends the user to the login page.
// endSession() (realtime.js) stops the heartbeat, the notification poller, the
// idle timer and the SSE stream: leaving those armed is what turned a single
// expired session into a stream of 401s, each timer re-firing a request that
// could only fail the same way.
function sessionExpired() {
  endSession();
  router.go('/login');
  return new ApiError(t('errors.sessionExpired'), 'SESSION_EXPIRED', 401);
}

// requireSession returns an error when a request needs a session we demonstrably
// do not have, so the caller fails locally instead of provoking a 401. A null
// token alone is not proof: it is also the normal state during boot, before the
// refresh completes. The presence marker for the refresh cookie settles it — no
// token and no marker means signed out.
function requireSession(path) {
  if (isPublicPath(path)) return null;
  if (Auth.token || Auth.hasSessionHint()) return null;
  return sessionExpired();
}

const http = {
  async _fetch(method, path, body, isRetry = false) {
    const blocked = requireSession(path);
    if (blocked) throw blocked;
    /** @type {RequestInit} */
    const opts = { method, headers: Auth.headers(), credentials: 'include' };
    if (body !== undefined) opts.body = JSON.stringify(body);
    const r = await fetch(API_BASE + path, opts);
    // A 401 from a public route is the route's own answer — wrong password, bad
    // reset token — and must reach the caller verbatim, not be recast as an
    // expired session. Only authenticated routes mean "your session is gone".
    if (r.status === 401 && !isPublicPath(path)) {
      // Read the body before judging: an authenticated route can answer 401
      // about a credential in the request rather than about the session, and
      // recasting that as an expired session signs the user out for a typo.
      const err = await errorFromResponse(r);
      if (CREDENTIAL_ANSWER_CODES.has(err.code)) throw err;
      // Only a session that might still be restorable is worth a refresh round
      // trip; without the presence marker the refresh can only 401 in turn.
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

  // getBlob fetches binary content with the Authorization header attached and
  // returns a Blob. Plain <img src>/<a href> requests cannot carry the bearer
  // token, so authenticated content (uploaded attachments) must be fetched this
  // way and exposed to the DOM via an object URL. Mirrors _fetch's 401-refresh.
  async getBlob(path, isRetry = false) {
    const blocked = requireSession(path);
    if (blocked) throw blocked;
    /** @type {Record<string, string>} */
    const headers = {};
    if (Auth.token) headers['Authorization'] = 'Bearer ' + Auth.token;
    const r = await fetch(API_BASE + path, { method: 'GET', headers, credentials: 'include' });
    if (r.status === 401) {
      if (!isRetry && Auth.hasSessionHint()) {
        try {
          await Auth.refresh();
          return this.getBlob(path, true);
        } catch {
          throw sessionExpired();
        }
      }
      throw sessionExpired();
    }
    if (!r.ok) throw await errorFromResponse(r);
    return r.blob();
  },

  // upload sends a single file as multipart/form-data under the part name
  // "file". It must NOT set Content-Type so the browser adds the multipart
  // boundary; only the Authorization header is forwarded.
  async upload(path, file, isRetry = false) {
    const blocked = requireSession(path);
    if (blocked) throw blocked;
    const fd = new FormData();
    fd.append('file', file, file.name || 'upload');
    /** @type {Record<string, string>} */
    const headers = {};
    if (Auth.token) headers['Authorization'] = 'Bearer ' + Auth.token;
    const r = await fetch(API_BASE + path, { method: 'POST', headers, body: fd, credentials: 'include' });
    if (r.status === 401) {
      if (!isRetry && Auth.hasSessionHint()) {
        try {
          await Auth.refresh();
          return this.upload(path, file, true);
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
};

// qs builds a `?a=b&c=d` query string from a plain object, skipping null/
// undefined/empty values. Used by api.js's list/search endpoints.
function qs(p={}) {
  const s = new URLSearchParams();
  Object.entries(p).forEach(([k,v])=>{ if(v!=null && v!=='') s.append(k,v); });
  const str = s.toString();
  return str ? '?'+str : '';
}

export { apiErrorMessage, errorFromResponse, http, qs };
