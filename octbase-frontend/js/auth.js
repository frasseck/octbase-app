import { API_BASE, BASE_PATH, DEMO_EMAIL, DEMO_PASSWORD, USE_STANDALONE_DEMO_AUTH } from './env.js';
import { endSession } from './realtime.js';
import { router } from './router.js';

// Octbase SPA — split from the former single app.js (and later from api.js,
// which had grown to conflate auth, the HTTP client, the REST surface, the
// router, and permission helpers). One ES module among many, bundled by Vite
// (37b stage 2): its top-level declarations are file-private and its public
// surface is the `export { … }` block at the bottom. Imports carry the
// dependencies — there is no load order to keep in step (js/README.md).
const Auth = (() => {
  let _accessToken = null;
  let _refreshing = null;

  // Presence marker for the HttpOnly refresh cookie. The refresh cookie itself
  // is invisible to JS, so the backend sets a companion `refresh_present=1`
  // cookie (non-HttpOnly, same expiry) alongside it on login/rotation and
  // clears it on logout or any rejected refresh. The browser auto-deletes the
  // marker when the refresh cookie expires, so bootstrap can skip the
  // /auth/refresh probe — and the 401 it would log — whenever no live session
  // exists, without the staleness of a never-expiring localStorage flag.
  const SESSION_PRESENT_COOKIE = 'refresh_present';

  return {
    get token() { return _accessToken; },
    set token(v) { _accessToken = v; },

    // hasSessionHint reports whether the refresh-cookie presence marker is still
    // set in this browser, i.e. whether a session may be restorable. Read from
    // document.cookie so it tracks the cookie's lifetime exactly.
    hasSessionHint() {
      return document.cookie.split('; ').some(c => c === SESSION_PRESENT_COOKIE + '=1');
    },

    headers() {
      const h = { 'Content-Type': 'application/json' };
      if (_accessToken) h['Authorization'] = 'Bearer ' + _accessToken;
      return h;
    },

    // demoLogin signs in as the seeded demo user to obtain a JWT. Used only in
    // standalone (file://) mode so the disk-served UI works against the
    // JWT-only backend without a manual login step.
    async demoLogin() {
      const r = await fetch(API_BASE + BASE_PATH + '/auth/login', {
        method: 'POST',
        credentials: 'include',
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
        method: 'POST',
        credentials: 'include',
      }).then(async r => {
        if (!r.ok) {
          // 401 means there is no (longer a) valid refresh cookie. The backend
          // clears the presence marker on a rejected refresh, so later loads
          // won't re-probe; transient failures (e.g. server down) leave the
          // marker intact so an outage doesn't drop a still-valid session.
          throw new Error('refresh_failed');
        }
        const d = await r.json();
        _accessToken = d.accessToken;
        return true;
      }).finally(() => { _refreshing = null; });
      return _refreshing;
    },

    async logout() {
      // The /auth/logout response clears the refresh + presence cookies; the
      // standalone (file://) path has no server session to tear down.
      // endSession (realtime.js) is the single teardown both this and an
      // expired session run, so neither can leave a poller or the SSE stream
      // armed against a session that no longer exists.
      endSession();
      if (USE_STANDALONE_DEMO_AUTH) {
        router.go('/login');
        return;
      }
      await fetch(API_BASE + BASE_PATH + '/auth/logout', {
        method: 'POST', credentials: 'include',
      }).catch(() => {});
      router.go('/login');
    },

    // isAuthenticated reports whether a request can be signed RIGHT NOW — it is
    // about the access token in hand, nothing else. Do not use it to decide
    // whether someone is signed IN: a null token is also the normal state during
    // boot, when the session may be perfectly valid.
    isAuthenticated() { return USE_STANDALONE_DEMO_AUTH || !!_accessToken; },

    // mayHaveSession reports whether a session may still exist, counting the
    // refresh-cookie presence marker as evidence. This is the question routing
    // has to ask, and it is the same one http.js:requireSession already asks
    // before failing a request locally. The two had drifted, and handleRoute's
    // bare isAuthenticated() check sent a valid session to /login (OCT-321).
    //
    // The window is BOOT, precisely: router.js arms the `hashchange` listener at
    // module load, while views-crud.js:init awaits Auth.refresh() before it
    // navigates. A hashchange arriving in between — a click, or a test driving
    // the app straight after load — reaches handleRoute with _accessToken still
    // null and a live refresh cookie. (There is no mid-session window: refresh()
    // assigns the token only on success and never clears it, and the one thing
    // that does clear it, endSession, means the session really is over.)
    //
    // Deliberately optimistic: when the marker is set but the refresh then
    // fails, the route's own request 401s, http.js recasts that as an expired
    // session, and endSession routes to /login. Answering late and correctly
    // beats answering early and wrongly.
    mayHaveSession() { return this.isAuthenticated() || this.hasSessionHint(); },
  };
})();

export { Auth };
