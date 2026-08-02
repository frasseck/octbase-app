// Octbase SPA — the event-delegation registries and their registration API.
//
// Split out of framework.js on 2026-07-30 (37b stage 2) for a load-order
// reason that only bites under ES modules, and bites hard: seven view modules
// read the `_A*`/`_VAL*`/`_CHK*` adapters AT LOAD TIME to register their
// handlers, and those adapters are `const` arrows. framework.js sits inside an
// import cycle (framework.js -> views-crud.js -> framework.js), so ESM's
// depth-first evaluation runs a view module's body BEFORE framework.js's — the
// adapters would still be in the temporal dead zone, and the failure would be a
// runtime ReferenceError rather than a build error.
//
// THIS FILE IMPORTS NOTHING, which is the whole point: a module with no
// dependencies is always evaluated before anything that imports it, so the dead
// zone is closed by construction rather than by getting the order right. Keep
// it that way — adding an import here re-opens the hazard.
//
// `initDelegation()` deliberately stays in framework.js: it wires the board's
// drag-and-drop and so depends on state and view modules, which would drag those
// dependencies in here.

// Inline on*="" attributes are replaced by data-act / data-change /
// data-input / data-keydown / data-submit attributes dispatched from a single
// set of document-level listeners. This keeps handler functions out of the
// global window namespace and removes the string-interpolation/escaping
// hazards of building executable JS inside HTML attributes. Handler arguments
// travel in data-a0 / data-a1 / data-a2 attributes (HTML-escaped via esc),
// and input handlers read this.value / this.checked from the element itself.
const ACTIONS  = {}; // data-act     -> click  handler (el, ev) => {}
const CHANGES  = {}; // data-change  -> change handler
const INPUTS   = {}; // data-input   -> input  handler
const KEYDOWNS = {}; // data-keydown -> keydown handler
const SUBMITS  = {}; // data-submit  -> submit handler

function _dispatch(registry, key, ev) {
  const node = ev.target.closest('[data-' + key + ']');
  if (!node) return;
  const name = node.dataset[key];
  const fn = registry[name];
  // A data-* attribute naming a handler nobody registered is always a bug, and
  // it used to be an invisible one: the element simply did nothing. That cost a
  // full session's debugging when the bundler's function-name mangling
  // unregistered every array-form handler at once (see vite.config.js
  // `keepNames`) — 13 e2e failures whose only symptom was dead buttons. Name
  // the missing handler instead of shrugging.
  if (!fn) { console.error(`[delegation] no ${key} handler registered: ${name}`); return; }
  fn(node, ev);
}

// Bucket helpers register a list of plain functions under their own .name, so
// markup uses data-act="functionName". Adapters map data-* / element state to
// each function's positional arguments.
const _A0 = (fn, el) => fn();
const _A1 = (fn, el) => fn(el.dataset.a0);
const _A2 = (fn, el) => fn(el.dataset.a0, el.dataset.a1);
const _A3 = (fn, el) => fn(el.dataset.a0, el.dataset.a1, el.dataset.a2);
const _VAL  = (fn, el) => fn(el.dataset.a0, el.value);           // change: fn(id, this.value)
const _VAL0 = (fn, el) => fn(el.value);                          // change: fn(this.value)
const _CHK  = (fn, el) => fn(el.dataset.a0, el.checked);         // change: fn(id, this.checked)
const _CHK0 = (fn, el) => fn(el.checked);                        // change: fn(this.checked)
function _claim(registry, name, fn) {
  // Registration is spread across the view modules, so nothing centrally
  // notices two modules claiming one handler name — the later <script> would
  // silently win. Say so instead.
  if (registry[name]) console.warn(`[delegation] duplicate handler name: ${name}`);
  registry[name] = fn;
}
// Both registration forms in one call. A list of plain functions is keyed by
// each function's own .name, with the adapter mapping data-a* / element state
// to its positional arguments; a { name: (el, ev) => … } object registers
// bespoke handlers that take the raw element and event.
function _bind(registry, fns, adapter) {
  if (Array.isArray(fns)) fns.forEach(fn => _claim(registry, fn.name, (el, ev) => adapter(fn, el, ev)));
  else Object.entries(fns).forEach(([name, fn]) => _claim(registry, name, fn));
}

// ── Registration API ────────────────────────────────────────────────────────
// Each module registers its own delegation handlers at load time from one
// block above its export block (js/README.md "Delegation registration"); the
// shell knows no view's handler names. Modules loaded BEFORE framework.js
// cannot call these — their few handlers are registered here (see `logout` /
// `nav` below).
function registerActions (fns, adapter) { _bind(ACTIONS,  fns, adapter); }
function registerChanges (fns, adapter) { _bind(CHANGES,  fns, adapter); }
function registerInputs  (fns, adapter) { _bind(INPUTS,   fns, adapter); }
function registerKeydowns(fns, adapter) { _bind(KEYDOWNS, fns, adapter); }
function registerSubmits (fns, adapter) { _bind(SUBMITS,  fns, adapter); }

export { ACTIONS, CHANGES, INPUTS, KEYDOWNS, SUBMITS, _A0, _A1, _A2, _A3, _CHK, _CHK0, _VAL, _VAL0, _dispatch, registerActions, registerChanges, registerInputs, registerKeydowns, registerSubmits };
