// Test harness for the SPA's modules — the vm shim that survived 37b stage 7.
//
// Vitest is the runner now, and a test that needs no stubbed collaborators
// imports its module for real (see richtext.test.js, state.test.js,
// i18n.test.js). What is left for this file is the other shape: the eight
// view-module tests that substitute a collaborator — a fake `el()`, a stub
// `Views.register`, a two-line `esc` — by putting it on the fake window. That
// is the model below, and porting those tests to `vi.mock()` would rewrite them
// rather than move them, so it was not done. It loads any of the js/ files into a fake
// `window` and returns that window with the file's public surface attached, so
// pure logic can be unit-tested with node:test + node:assert without a browser
// or DOMPurify.
//
// Two module shapes exist in js/, and both load the same way here:
//   - ES modules (framework.js, state.js, … — 37b stage 2): a leading block of
//     `import { … } from './x.js';` lines and a trailing `export { … };`.
//   - files loaded from @octbase/shared (pass the relative path, e.g.
//     `loadModule('../../octbase-shared/richtext.js')`): also ES modules since
//     37b stage 3, and rewritten the same way.
// Either may also import a bare npm package (37b stage 4) — blanked like any
// other import, so the package is NOT loaded and the name is whatever the test
// puts on the fake window.
//
// node:vm reproduces the browser's global-scope semantics: the fake window IS
// the script's global object, so `window.foo`, `document`, and bare
// `function foo(){}` all resolve against it. `const`/`let` at top level stay
// file-private, as in the browser.
//
// ── Why this harness rewrites module syntax instead of importing modules ──────
//
// A real `await import()` would drag in the file's whole transitive import
// graph — framework.js alone pulls a dozen modules — turning every unit test
// into a small integration test that needs a fake DOM deep enough for all of
// them, and it would cache one evaluation per URL, so views-agile.test.js could
// not load its module twice with different globals as it does today.
//
// So `loadModule` keeps evaluating ONE file, and reproduces exactly the
// pre-conversion contract: the names a module imports are resolved as globals
// off the fake window (stubbed via `globals`, `undefined` otherwise, which is
// harmless for the usual case of a name referenced only inside a function body),
// and the names it exports are published onto the fake window. That is what the
// classic-script harness did, so no test body changed when the conversion
// landed. The trade-off is deliberate and bounded: these tests assert pure
// logic, and the real module graph is exercised by the Playwright suite against
// the built bundle.

import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { createContext, runInContext } from 'node:vm';

const HERE = dirname(fileURLToPath(import.meta.url));

// makeWindow builds a fake browser window with just enough surface that the
// modules load and their pure functions run. Pass `overrides` to add or replace
// globals a specific file needs (e.g. a DOMPurify stub, seeded localStorage).
function makeWindow(overrides = {}) {
  const store = {};
  const noopClassList = { add() {}, remove() {}, toggle() {}, contains() { return false; } };
  const win = {
    localStorage: {
      getItem: (k) => (k in store ? store[k] : null),
      setItem: (k, v) => { store[k] = String(v); },
      removeItem: (k) => { delete store[k]; },
    },
    document: {
      documentElement: { lang: '', classList: noopClassList, setAttribute() {}, style: {} },
      addEventListener() {},
      querySelector() { return null; },
      getElementById() { return null; },
      createElement() { return { classList: noopClassList, setAttribute() {}, style: {}, appendChild() {} }; },
    },
    navigator: { language: 'en-US' },
    location: { protocol: 'http:', href: 'http://localhost/', search: '', hash: '' },
    matchMedia: () => ({ matches: false, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }),
    addEventListener() {},
    removeEventListener() {},
    setTimeout, clearTimeout, setInterval, clearInterval,
    console,
    // Every view module registers its delegated handlers at load time
    // (js/README.md "Delegation registration"). Stubbed by default so loading a
    // module needs no per-test boilerplate; a test that wants to inspect what a
    // module registered overrides these through `globals`. The argument
    // adapters are the real ones from framework.js, so an overriding test sees
    // handlers that behave as they do in the browser.
    registerActions() {}, registerChanges() {}, registerInputs() {},
    registerKeydowns() {}, registerSubmits() {},
    _A0: (fn) => fn(),
    _A1: (fn, el) => fn(el.dataset.a0),
    _A2: (fn, el) => fn(el.dataset.a0, el.dataset.a1),
    _A3: (fn, el) => fn(el.dataset.a0, el.dataset.a1, el.dataset.a2),
    _VAL:  (fn, el) => fn(el.dataset.a0, el.value),
    _VAL0: (fn, el) => fn(el.value),
    _CHK:  (fn, el) => fn(el.dataset.a0, el.checked),
    _CHK0: (fn, el) => fn(el.checked),
  };
  win.window = win;
  win.globalThis = win;
  win.self = win;
  Object.assign(win, overrides);
  return win;
}

// The exact shapes the ESM codemod emits (scripts/codemod-esm.mjs `rewrite`),
// anchored so a hand-written variant this harness cannot handle fails loudly in
// `toClassicScript` rather than being silently mistranslated.
// Three specifier shapes reach this harness: a sibling module (`./x.js`), a
// module of the shared package (`@octbase/shared/x.js`), and — since 37b stage
// 4 — a bare npm package (`dompurify`, `qrcode-generator`). The bare form has
// no extension and no leading dot, so it needs its own alternative; without it
// `toClassicScript` throws on richtext.js and the ten test files fail as a
// batch, which is neither the build's nor the e2e suite's job to notice.
const FROM = /(?:(?:\.\/|@octbase\/shared\/)[a-z0-9-]+\.js|[a-z0-9-]+(?:\/[a-z0-9-]+)*)/.source;
const IMPORT_LINE = new RegExp(`^import \\{ ([A-Za-z0-9_, ]+) \\} from '${FROM}';$`);
// A default import — how the two third-party libraries are consumed
// (`import DOMPurify from 'dompurify'`). Same treatment as a named one: the
// name resolves as a global off the fake window, which is where the tests'
// DOMPurify stub already lives. Note what that means for a bare specifier —
// the real package is never loaded here, so a test that needs its behaviour
// must stub it explicitly rather than expect the npm module.
const DEFAULT_IMPORT_LINE = new RegExp(`^import ([A-Za-z0-9_]+) from '${FROM}';$`);
const SIDE_EFFECT_IMPORT = new RegExp(`^import '${FROM}';$`);
const EXPORT_LINE = /^export \{ ([A-Za-z0-9_, ]+) \};$/;

// toClassicScript turns one generated ES module back into the equivalent
// classic script: imports become nothing (their names resolve as globals) and
// the export block becomes the window assignment it was converted from. Line
// numbers are preserved so a stack trace still points at the right source line.
function toClassicScript(code, file) {
  const imported = [];
  const out = code.split('\n').map((line) => {
    const imp = IMPORT_LINE.exec(line);
    if (imp) {
      imported.push(...imp[1].split(',').map((s) => s.trim()));
      return '';
    }
    const def = DEFAULT_IMPORT_LINE.exec(line);
    if (def) {
      imported.push(def[1]);
      return '';
    }
    if (SIDE_EFFECT_IMPORT.test(line)) return '';
    const exp = EXPORT_LINE.exec(line);
    if (exp) return `Object.assign(window, { ${exp[1]} });`;
    if (/^\s*(?:import|export)\b/.test(line)) {
      throw new Error(
        `${file}: module syntax this harness cannot rewrite: ${line.trim()}\n` +
        'Keep to the single-line `import { … } from \'./x.js\';` / `export { … };` ' +
        'forms the codemod emits, or load the file with your own vm context.',
      );
    }
    return line;
  });
  return { code: out.join('\n'), imported };
}

// readAsClassicScript — the source of one js/ file, ready for
// `vm.runInContext`. For the tests that build their own context because they
// need several files in one window (views-crud, views-task); `loadModule` is
// the one-file path.
function readAsClassicScript(relFile) {
  return toClassicScript(readFileSync(join(HERE, relFile), 'utf8'), relFile).code;
}

// loadModule reads <relFile> relative to octbase-frontend/js/ — so a shared
// module is '../../octbase-shared/<name>.js' — runs it in a fresh fake
// window, and returns that window. Read the module's exports off it, e.g.
//   const win = loadModule('framework.js'); win.esc('<a>');
function loadModule(relFile, { globals = {} } = {}) {
  const win = makeWindow(globals);
  createContext(win);
  runInContext(readAsClassicScript(relFile), win, { filename: join(HERE, relFile) });
  return win;
}

export { makeWindow, loadModule, readAsClassicScript };
