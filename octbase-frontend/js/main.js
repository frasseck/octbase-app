// Octbase SPA — the module entry point.
//
// Replaces the 26 ordered <script> tags index.html used to carry (37b stage 2).
// The imports below are SIDE-EFFECT imports in the old load order, and the
// order still matters even though the bundler resolves the dependency graph
// itself: view modules register themselves with the view registry and the
// delegation registries as they evaluate, so evaluation order decides sidebar
// order and which module claims a duplicate handler name. Dependencies are
// carried by each file's own imports; this list carries REGISTRATION order.
//
// bootstrap.js is last because it starts the app — everything it needs must
// have registered by the time it runs.
//
// Not imported here, deliberately:
//   - theme-init.js  — a classic, non-deferred <script> in <head>. Modules are
//     always deferred, so folding it in would ship a flash of the wrong theme,
//     and inlining it would need CSP 'unsafe-inline'. Both are forbidden.
//   - the @octbase/shared modules — no longer listed here at all. Since 37b
//     stage 3 i18n/meta/richtext are imported by name by the files that use
//     them, so they enter the graph through those imports rather than through
//     a registration-order list. The same now goes for DOMPurify and the QR
//     generator: 37b stage 4 made them the `dompurify` and `qrcode-generator`
//     npm packages, so the last two classic scripts index.html carried besides
//     theme-init.js are gone and nothing outside this graph publishes a global
//     the app depends on.

import './env.js';
import './config.js';
import './icons.js';
import './auth.js';
import './http.js';
import './api.js';
import './router.js';
import './permissions.js';
import './state.js';
import './registry.js';
import './delegation.js';
import './framework.js';
import './realtime.js';
import './views-shell.js';
import './views-board.js';
import './views-tasklist.js';
import './views-task.js';
import './views-agile.js';
import './views-stats.js';
import './views-content.js';
import './views-mindmap.js';
import './views-crud.js';
import './views-settings.js';
import './admin.js';
import './namespace.js';
import './bootstrap.js';
