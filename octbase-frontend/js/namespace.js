import { getLocale, t } from '@octbase/shared/i18n.js';
import { AdminState } from './admin.js';
import { api } from './api.js';
import { Auth } from './auth.js';
import { FEATURES } from './config.js';
import { http } from './http.js';
import { AppPerms } from './permissions.js';
import { sanitizeRichText } from '@octbase/shared/richtext.js';
import { renderDescriptionHTML, renderMfaChallengeStep } from './framework.js';
import { Views } from './registry.js';
import { router } from './router.js';
import { S } from './state.js';
import { dropSlot } from './views-board.js';
import { setView } from './views-shell.js';
import { openTaskPanel } from './views-task.js';

// Octbase SPA — explicit namespace facade.
//
// The app is a graph of ES modules bundled by Vite (37b stage 2); module scope
// is the default and nothing reaches window unless a file puts it there. That
// inverts what this file used to be for. Under classic scripts it was a tidier
// alternative to a window namespace that already existed by accident; now it is
// the place where the few genuinely global names are created on purpose.
// Delegated event handlers are not among them — each module registers its own
// into the file-private dispatch registries, so a handler reached through
// `data-act`/`data-change`/… is not a window global at all (js/README.md
// "Delegation registration").
//
// This object is the explicit, documented home for the core singletons and the
// mutable application state. Prefer `App.*` from the devtools console and from
// any new code instead of reaching for ambient globals. Read-only getters keep
// it in lock-step with the live bindings (no stale copies).
const App = {
  get version() { return '0.3.0'; },
  get api()    { return api; },        // REST client (api.js)
  get http()   { return http; },       // low-level fetch wrapper (http.js)
  get auth()   { return Auth; },       // session/token state (auth.js)
  get router()  { return router; },     // hash router (router.js)
  get state()  { return S; },          // mutable app state (state.js)
  get perms()  { return AppPerms; },   // permission helpers (permissions.js)
  get views()  { return Views; },      // view registry (registry.js)
  get features() { return FEATURES; }, // optional-feature flags (config.js)
  get admin()  { return AdminState; }, // admin-panel view state (admin.js)
};

// The deliberate window surface — and under ES modules it is now the ONLY one.
//
// Under classic scripts every export block put its names on window as a side
// effect, so `App`, `router` and `S` were global whether or not anyone meant
// them to be. An ESM export is module-scoped, so each of these has to be
// published explicitly or it silently disappears — no build-time check can see
// it going, because nothing in the source references it.
//
// What this file has to publish, and why (do not grow the list casually):
//   App — the documented devtools facade, this file's reason to exist.
//   S   — the Playwright suite reads `window.S` to assert on app state.
//   t, getLocale — test_i18n.py calls both directly to read the active
//         translation without scraping the DOM. They used to arrive for free:
//         the shared i18n.js was a classic script that assigned them to
//         `window` itself. Since 37b stage 3 it is an ES module in
//         @octbase/shared with no window side effects at all, so the SPA that
//         wants them global has to say so — here, with the rest of the
//         deliberate surface, rather than inside a module both SPAs share.
//         (The mobile SPA does not publish them; nothing asks it to.)
// The rest of the surface is published by the module that owns it and was
// already explicit before the conversion, so it survived it: `window.router`
// (router.js), `window.Views` (registry.js), `window.AppPerms`
// (permissions.js).
window.App = App;
window.S = S;
window.t = t;
window.getLocale = getLocale;

// ── The e2e test surface ────────────────────────────────────────────────────
// Five module internals the Playwright suite calls directly rather than
// clicking its way to. They were ambient globals for free under classic
// scripts; under ES modules they have to be published on purpose, so they are
// listed here — with their callers — instead of quietly re-exported wherever
// they happen to be defined:
//   setView               — test_agile/test_board drive view switches
//   openTaskPanel         — opens the panel for a known task id
//   renderDescriptionHTML — asserts rich-text escaping without a round trip
//   dropSlot              — the board's drop-index maths, checked as a pure
//                           function over a list of midpoints
//   renderMfaChallengeStep — renders the login MFA step for a synthetic token
//   sanitizeRichText      — test_sanitizer.py feeds ~23 hostile vectors straight
//                           through the client XSS boundary and compares the
//                           output. It was an ambient global while
//                           @octbase/shared was a set of classic scripts; the
//                           tests are worth more than that accident, so the
//                           name is published here on purpose. App code still
//                           imports it — reading it off window is not allowed.
// These are a test affordance, not app API — no app code may read them off
// window. Nothing the `App` facade already reaches belongs here: a test wanting
// the REST client uses `App.api`, as test_members.py does. `dropSlot` and
// `renderDescriptionHTML` are pure functions whose natural home is the
// js/*.test.js unit layer; moving them there would let this list shrink
// (37b stage 7).
window.setView = setView;
window.openTaskPanel = openTaskPanel;
window.renderDescriptionHTML = renderDescriptionHTML;
window.renderMfaChallengeStep = renderMfaChallengeStep;
window.dropSlot = dropSlot;
window.sanitizeRichText = sanitizeRichText;

export { App };
