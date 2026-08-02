import { initDelegation } from './framework.js';
import { init } from './views-crud.js';

// Octbase SPA — split from the former single app.js. One ES module among many,
// bundled by Vite (37b stage 2): its top-level declarations are file-private
// and it exports nothing. Imports carry the dependencies, so there is no load
// order to keep in step (js/README.md).
//
// main.js imports this file LAST and does so for its side effect: it starts the
// app. That position is the one piece of ordering that still matters — every
// module must have registered by the time this evaluates.
// Delegated event handlers are NOT listed here: every module registers its own
// at load time through framework.js's registerActions/Changes/Inputs/Keydowns/
// Submits (js/README.md "Delegation registration"), so the shell knows no
// view's handler names. By the time this runs, every registry is populated.
initDelegation();
init();

// No exports — bootstrap only starts the app; nothing here is referenced by
// name elsewhere.
