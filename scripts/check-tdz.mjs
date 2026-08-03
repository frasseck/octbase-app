#!/usr/bin/env node
// TDZ guard for the ES-module SPAs (37b stage 2).
//
// WHAT IT CATCHES, and why nothing else does. ES modules tolerate import cycles
// for *function* references: declarations hoist and bindings are live, so a
// function body may call across a cycle freely. What breaks is a **top-level**
// read of a `const`/`let`/`class` from a module that has not finished
// evaluating — the binding is in its temporal dead zone and the read throws
// `ReferenceError` **at boot**.
//
// The bundler will not warn about it (the graph is valid), the unit tests will
// not see it (they evaluate one file against fake globals), and the e2e suite
// only catches it if the crash happens to break a tested path. Under classic
// scripts this class could not exist at all, because `index.html`'s load order
// made every reference safe and the retired export guard enforced that order.
// This is what replaced that rule, and it checks the sharper property.
//
// A load-time cross-module read is SAFE in exactly two cases:
//   (a) the source module cannot be in an import CYCLE with the reader — i.e.
//       nothing in the source's transitive imports leads back to the reading
//       module. Without a cycle, ESM guarantees the source's body has finished
//       evaluating before the reader's begins, so its bindings are initialised.
//       (A source that imports nothing at all is the trivial case of this, and
//       is why delegation.js, registry.js and env.js are deliberately
//       import-free — but it is not the only safe case, and treating it as the
//       only one reports hazards on apps that demonstrably boot.)
//   (b) the binding is a FUNCTION DECLARATION — hoisted and initialised at
//       instantiation, before any module body runs, so evaluation order and
//       cycles cannot reach it.
// Anything else is reported.
//
// The analysis is the one that gated the conversion itself (it lived in
// scripts/codemod-esm.mjs, which could only read classic scripts and so cannot
// run against the tree it produced). It uses scripts/lib/js-scope.mjs — the same
// free-variable resolver — because two disagreeing resolvers is exactly the
// drift this migration set out to end.
//
// Run: node scripts/check-tdz.mjs [--app octbase-frontend] [--verbose]

import { readFileSync, readdirSync, existsSync } from 'node:fs';
import { dirname, join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';
import * as acorn from './vendor/acorn.mjs';
import { BROWSER_GLOBALS, analyze } from './lib/js-scope.mjs';

const REPO = join(dirname(fileURLToPath(import.meta.url)), '..');
const APPS = ['octbase-frontend', 'octbase-mobile'];

// The shared package (37b stage 3): i18n.js, meta.js and richtext.js are real
// ES modules both SPAs import, so they are part of each app's graph and are
// analysed with it. There is no longer anything beside them — the package's two
// vendored UMD libraries were classic <script> tags publishing `DOMPurify` and
// `qrcode` as ambient globals, which this guard had to be told about; 37b stage
// 4 replaced them with npm dependencies, so those names now arrive through
// imports and are classified like any other binding.
const SHARED_DIR = join(REPO, 'octbase-shared');
const SHARED_SPEC = /^@octbase\/shared\/(.+\.js)$/;
// Not part of either module graph: single-script static pages, the pre-paint
// theme script, and the plain-Node test harness.
const NON_SPA = new Set([
  'docs-init.js', 'user-guide-nav.js', 'styleguide-icons.js', 'theme-init.js',
  'testutil.js',
]);

const isTest = (f) => f.endsWith('.test.js');

function spaFiles(jsDir) {
  return readdirSync(jsDir)
    .filter((f) => f.endsWith('.js') && !isTest(f) && !NON_SPA.has(f))
    .sort();
}

// resolveSpec — an import specifier to a file on disk, resolved against the
// directory of the module that wrote it (NOT the app's js/, which would send
// @octbase/shared's own relative imports looking in the wrong tree). The graph
// key is the repo-relative path, so two files never collide by basename.
// Anything that is neither relative nor the shared package — a future npm
// dependency — is deliberately outside the graph and its reads are unclassified.
function resolveSpec(spec, fromDir) {
  const shared = SHARED_SPEC.exec(spec);
  const path = shared ? join(SHARED_DIR, shared[1])
    : spec.startsWith('.') ? join(fromDir, spec)
    : null;
  return path === null ? null : { key: relative(REPO, path), path };
}

function parseModule(code, file) {
  try {
    return acorn.parse(code, { ecmaVersion: 'latest', sourceType: 'module' });
  } catch (e) {
    throw new Error(`${file}: parse error: ${e.message}`);
  }
}

// blankModuleSyntax — return the source with `import`/`export` *statements*
// blanked out, spaces for characters so every offset and line number still
// matches the original.
//
// This exists because the resolver in lib/js-scope.mjs reasons about a classic
// script: it has no notion of an import *binding*, so the identifiers inside
// `import { api } from './api.js';` look to it like top-level reads of `api` —
// i.e. every import statement would be reported as a load-time read on line 1.
// (That is not a hypothetical: it is what this guard did before this step, 52
// false hazards on an app that demonstrably boots.) Blanking the statements
// gives the resolver exactly the shape it was written for, so an imported name
// becomes a free reference only where the code actually *uses* it — which is the
// only place a TDZ read can happen.
//
// A declaration-form export (`export const x = …`) keeps its declaration and
// loses only the keyword, so `x` stays a top-level binding.
function blankModuleSyntax(code, ast) {
  const blanks = [];
  for (const node of ast.body) {
    if (node.type === 'ImportDeclaration') {
      blanks.push([node.start, node.end]);
    } else if (node.type === 'ExportNamedDeclaration') {
      blanks.push(node.declaration ? [node.start, node.declaration.start] : [node.start, node.end]);
    } else if (node.type === 'ExportDefaultDeclaration') {
      blanks.push([node.start, node.declaration.start]);
    } else if (node.type === 'ExportAllDeclaration') {
      blanks.push([node.start, node.end]);
    }
  }
  const out = code.split('');
  for (const [s, e] of blanks) {
    for (let i = s; i < e; i++) if (out[i] !== '\n') out[i] = ' ';
  }
  return out.join('');
}

// moduleShape — the import/export surface read straight from the syntax, which
// is the point of having done the conversion: no inference, no heuristics.
function moduleShape(ast) {
  const imports = new Map();   // local name -> source specifier ('./framework.js')
  const exports = new Set();
  // Every specifier that creates a graph edge — including side-effect-only
  // imports (`import './x.js'`) and re-exports (`export … from './x.js'`),
  // which bind no local name and so never appear in `imports`, but still
  // evaluate their target and therefore still close cycles.
  const sources = new Set();
  let importCount = 0;
  for (const node of ast.body) {
    if (node.type === 'ImportDeclaration') {
      importCount++;
      sources.add(node.source.value);
      for (const s of node.specifiers) imports.set(s.local.name, node.source.value);
    } else if (node.type === 'ExportNamedDeclaration') {
      if (node.source) sources.add(node.source.value);
      for (const s of node.specifiers) exports.add(s.exported.name);
      const d = node.declaration;
      if (d) {
        if (d.type === 'VariableDeclaration') {
          for (const decl of d.declarations) {
            if (decl.id.type === 'Identifier') exports.add(decl.id.name);
          }
        } else if (d.id) {
          exports.add(d.id.name);
        }
      }
    } else if (node.type === 'ExportDefaultDeclaration') {
      exports.add('default');
    } else if (node.type === 'ExportAllDeclaration') {
      sources.add(node.source.value);
    }
  }
  return { imports, exports, sources, importCount };
}

function checkApp(appName, { verbose }) {
  const jsDir = join(REPO, appName, 'js');
  if (!existsSync(jsDir)) {
    // A missing scan target must fail loudly: "clean ✓" over a tree that was
    // never read is the guard silently turning itself off.
    console.error(`check-tdz: ${relative(REPO, jsDir)} does not exist — nothing was scanned`);
    process.exit(1);
  }

  const files = spaFiles(jsDir);
  const info = new Map();
  const ambient = new Set(BROWSER_GLOBALS);

  // Load the app's own modules, then follow every import out of them until the
  // graph closes. That pulls in @octbase/shared without naming its files here,
  // so adding a module to the package needs no edit in this guard.
  const load = (path) => {
    const key = relative(REPO, path);
    if (info.has(key)) return key;
    const code = readFileSync(path, 'utf8');
    const ast = parseModule(code, key);
    const bare = blankModuleSyntax(code, ast);
    const a = analyze(acorn.parse(bare, { ecmaVersion: 'latest', sourceType: 'script' }), bare);
    const shape = moduleShape(ast);
    info.set(key, { ...shape, a, dir: dirname(path) });
    for (const spec of shape.sources) {
      const r = resolveSpec(spec, dirname(path));
      if (r && existsSync(r.path)) load(r.path);
    }
    return key;
  };
  for (const f of files) load(join(jsDir, f));

  // reaches — can `from` get back to `to` through its imports? That is exactly
  // the cycle question, and the only thing that can leave a binding in its TDZ.
  const reaches = (from, to, seen = new Set()) => {
    if (from === to) return true;
    if (seen.has(from)) return false;
    seen.add(from);
    const m = info.get(from);
    if (!m) return false;
    for (const spec of m.sources) {
      const r = resolveSpec(spec, m.dir);
      if (r && reaches(r.key, to, seen)) return true;
    }
    return false;
  };

  // Every load-time read of an imported binding is an edge to classify — in
  // EVERY module of the graph, not only the app's top-level files: a module
  // that arrives via imports (@octbase/shared, a js/ subdirectory) runs its
  // own top level the same way and can throw the same ReferenceError at boot.
  const hazards = [];
  let edges = 0;
  for (const [key, me] of info) {
    for (const [name, meta] of me.a.freeRefs) {
      if (!meta.loadTime) continue;              // function bodies are always fine
      if (ambient.has(name)) continue;           // browser or shared-module global
      const spec = me.imports.get(name);
      if (!spec) continue;                       // not an import: same-file or unknown
      const r = resolveSpec(spec, me.dir);
      if (!r) continue;                          // outside the graph
      const from = r.key;
      const src = info.get(from);
      if (!src) continue;                        // unreadable / not on disk
      edges++;
      const kind = src.a.topLevelDecls.get(name);
      const cyclic = reaches(from, key);
      if (!cyclic || kind === 'function') {
        if (verbose) {
          const why = !cyclic
            ? (src.importCount === 0 ? 'source imports nothing' : 'no import cycle back to the reader')
            : 'hoisted function declaration';
          console.log(`  ok   ${key} reads ${name} from ${from} (${why})`);
        }
        continue;
      }
      hazards.push({ file: key, name, from, kind: kind || 'unknown', line: meta.line });
    }
  }

  console.log(`${appName}: ${files.length} modules (+${info.size - files.length} reached ` +
              `via imports), ${edges} load-time cross-module read(s)`);
  if (hazards.length) {
    console.log(`  !! ${hazards.length} TDZ hazard(s) — a ReferenceError at boot:`);
    for (const h of hazards) {
      console.log(`     ${h.file}:${h.line} reads ${h.name} from ${h.from} ` +
                  `(${h.kind} — not hoisted, and ${h.from} imports its way back to ${h.file})`);
    }
  } else {
    console.log('  no TDZ hazards ✓');
  }
  return { hazards, files: files.length, edges };
}

const args = process.argv.slice(2);
const verbose = args.includes('--verbose');
const only = args.includes('--app') ? args[args.indexOf('--app') + 1] : null;

let total = 0;
let scanned = 0;
for (const app of APPS) {
  if (only && app !== only) continue;
  if (!existsSync(join(REPO, app))) {
    // Loud, not "clean ✓": a guard that skips a missing target is off, not green.
    console.error(`check-tdz: app directory ${app} does not exist — nothing was scanned`);
    process.exit(1);
  }
  total += checkApp(app, { verbose }).hazards.length;
  scanned++;
}
if (scanned === 0) {
  console.error(`check-tdz: no app matched --app ${only} (known: ${APPS.join(', ')})`);
  process.exit(1);
}

if (total) {
  console.error(
    `\ncheck-tdz: ${total} hazard(s). Fix by breaking the import cycle between the ` +
    'two modules, or by turning the binding into a function declaration, or by ' +
    'moving the read out of load time into a function body.',
  );
  process.exit(1);
}
console.log('TDZ guard: clean ✓');
