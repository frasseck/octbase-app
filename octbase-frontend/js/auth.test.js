// Unit tests for the two session predicates on Auth. Plain node, no build:
//   npm run test:unit -- auth.test.js
//
// They exist because the two drifted apart and the drift was a live bug
// (OCT-321). http.js:requireSession asked "might a session still exist?" —
// counting the refresh-cookie presence marker — while router.js:handleRoute
// asked "is there a token right now?" and sent anyone without one to /login.
// A null token is the normal state during boot and for the width of a refresh,
// so a navigation landing in that window discarded the route it was given and
// showed a login page to someone who was signed in.
//
// The distinction is the point, so both directions are pinned: isAuthenticated
// must stay strict (it answers "can I sign this request now?") and
// mayHaveSession must stay optimistic (it answers "is this person signed in?").
// Collapsing either into the other reintroduces the bug or invents a new one.

import { test } from 'vitest';
import assert from 'node:assert';
import { loadModule } from './testutil.js';

// load returns the Auth built against a given cookie jar and standalone flag.
// auth.js reads document.cookie directly (deliberately — so the marker tracks
// the cookie's real lifetime), which is the only browser surface it needs here.
function load({ cookie = '', standalone = false } = {}) {
  const win = loadModule('auth.js', {
    globals: {
      document: { cookie },
      USE_STANDALONE_DEMO_AUTH: standalone,
      API_BASE: '', BASE_PATH: '', DEMO_EMAIL: '', DEMO_PASSWORD: '',
      endSession() {}, router: { go() {} },
      fetch: () => Promise.reject(new Error('no network in unit tests')),
    },
  });
  return win.Auth;
}

test('a token in hand answers yes to both questions', () => {
  const Auth = load();
  Auth.token = 'jwt';
  assert.strictEqual(Auth.isAuthenticated(), true);
  assert.strictEqual(Auth.mayHaveSession(), true);
});

test('no token and no marker is signed out, by both questions', () => {
  const Auth = load();
  assert.strictEqual(Auth.isAuthenticated(), false);
  assert.strictEqual(Auth.mayHaveSession(), false);
});

// The regression itself: mid-refresh, the token is null but the session is
// valid. handleRoute reads mayHaveSession, so it must not conclude "signed out".
test('mid-refresh — no token but the presence marker is set — is NOT signed out', () => {
  const Auth = load({ cookie: 'refresh_present=1' });
  assert.strictEqual(Auth.isAuthenticated(), false, 'no token: cannot sign a request yet');
  assert.strictEqual(Auth.mayHaveSession(), true, 'but the session may still be restorable');
});

test('the marker is matched exactly, not by substring', () => {
  // A cookie whose NAME merely ends in the marker's name, and one whose value
  // is not 1, must both read as absent — otherwise an unrelated cookie could
  // keep a signed-out user out of the login page.
  for (const cookie of ['not_refresh_present=1', 'refresh_present=0', 'refresh_presentx=1']) {
    const Auth = load({ cookie });
    assert.strictEqual(Auth.mayHaveSession(), false, `cookie ${cookie} should not count as a session`);
  }
});

test('the marker is found among other cookies', () => {
  const Auth = load({ cookie: 'theme=dark; refresh_present=1; lang=de' });
  assert.strictEqual(Auth.hasSessionHint(), true);
  assert.strictEqual(Auth.mayHaveSession(), true);
});

// The file:// standalone demo signs in by construction, so both predicates say
// yes with no token and no cookie. Routing depends on that: the demo bundle has
// no refresh cookie to mark.
test('standalone demo mode is authenticated by construction', () => {
  const Auth = load({ standalone: true });
  assert.strictEqual(Auth.isAuthenticated(), true);
  assert.strictEqual(Auth.mayHaveSession(), true);
});
