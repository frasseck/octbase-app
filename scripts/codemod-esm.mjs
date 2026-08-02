#!/usr/bin/env node
// ESM codemod — 37b stage 2. One-shot tool, kept in the tree so the conversion
// is reproducible and reviewable rather than a pile of hand edits.
//
// SPENT as of 2026-07-30: both SPAs are converted, and this reads classic
// scripts (`sourceType: 'script'`), so running it against the current tree only
// produces parse errors. It is history, not a guard. The TDZ analysis it carried
// — the one part that had ongoing value — now lives in `scripts/check-tdz.mjs`,
// which reads ES modules and runs in CI and the pre-commit sweep.
//
// It turns each classic-script SPA file into an ES module:
//   - strips the IIFE wrapper (desktop files),
//   - rewrites `Object.assign(window, { … })` into `export { … }`,
//   - generates `import { … } from './other.js'` for every free identifier
//     another file in the same app exports.
//
// It reuses scripts/lib/js-scope.mjs — the SAME resolver the export guard uses.
// A second, disagreeing resolver would generate imports the guard does not
// believe in, which is exactly the class of drift this migration is meant to end.
//
// STAGE BOUNDARY, deliberate: the five octbase-shared modules (i18n, meta,
// qrcode, purify, richtext) are NOT converted here. Stage 3 promotes them to a
// real package; until then they stay classic <script> tags loaded before the
// module bundle, and their exports are treated as ambient globals — the same
// way browser globals are. purify.js and qrcode.js are additionally vendored
// third-party bytes pinned by SHA-256 in scripts/vendor-manifest.txt, so
// rewriting them here would break the integrity guard for no benefit; stage 4
// replaces them with npm packages.
//
// Run: node scripts/codemod-esm.mjs [--app octbase-frontend] [--write]
// Without --write it reports what it would do and changes nothing.

import { readFileSync, writeFileSync, readdirSync, existsSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import * as acorn from './vendor/acorn.mjs';
import { BROWSER_GLOBALS, analyze } from './lib/js-scope.mjs';

const REPO = join(dirname(fileURLToPath(import.meta.url)), '..');

// The shared modules: left as classic scripts by this stage (see header).
const SHARED = ['i18n.js', 'meta.js', 'qrcode.js', 'purify.js', 'richtext.js'];

// Vendored UMD bundles whose exports static analysis cannot see.
const VENDORED_EXPORTS = { 'qrcode.js': ['qrcode'], 'purify.js': ['DOMPurify'] };

// Files that are not part of the SPA module graph: single-script static pages
// and the plain-Node test layer. Left exactly as they are (37b stage 2:
// "the cheap, low-risk answer is to leave them alone").
const NON_SPA = new Set([
  'docs-init.js', 'user-guide-nav.js', 'styleguide-icons.js', 'theme-init.js',
  'testutil.js',
]);

const isTest = (f) => f.endsWith('.test.js');

function appFiles(dir) {
  return readdirSync(join(dir, 'js'))
    .filter((f) => f.endsWith('.js') && !isTest(f) && !NON_SPA.has(f) && !SHARED.includes(f))
    .sort();
}

// scriptsOfPage — the load order recorded in index.html, which is the
// dependency order the module graph must reproduce.
function scriptsOfPage(htmlPath) {
  const html = readFileSync(htmlPath, 'utf8');
  const out = [];
  const re = /<script[^>]*\bsrc="(js\/[A-Za-z0-9._-]+\.js)(?:\?[^"]*)?"/g;
  let m;
  while ((m = re.exec(html)) !== null) out.push(m[1].slice(3));
  return out;
}

function parse(code, file) {
  try {
    return acorn.parse(code, { ecmaVersion: 'latest', sourceType: 'script' });
  } catch (e) {
    throw new Error(`${file}: parse error: ${e.message}`);
  }
}

// exportedSurface — what a file publishes. The explicit Object.assign block for
// IIFE files; every top-level declaration for plain scripts (they land on the
// global object exactly as a classic script does).
function exportedSurface(a, file) {
  if (VENDORED_EXPORTS[file]) return new Set(VENDORED_EXPORTS[file]);
  const out = new Set(a.explicitExports.keys());
  for (const n of a.windowAssigns) out.add(n);
  if (!a.iife && out.size === 0) for (const n of a.topLevelDecls.keys()) out.add(n);
  return out;
}

function processApp(appName, { write }) {
  const dir = join(REPO, appName);
  const jsDir = join(dir, 'js');
  const order = scriptsOfPage(join(dir, 'index.html'));
  const files = appFiles(dir);

  // Analyse every file once, including the shared ones (we need their exported
  // names to treat them as ambient).
  const info = new Map();
  for (const f of readdirSync(jsDir).filter((x) => x.endsWith('.js') && !isTest(x))) {
    const code = readFileSync(join(jsDir, f), 'utf8');
    const a = analyze(parse(code, f), code);
    info.set(f, { code, a, exports: exportedSurface(a, f) });
  }

  // Ambient names for this stage: browser globals + everything the shared
  // modules publish.
  const ambient = new Set(BROWSER_GLOBALS);
  for (const s of SHARED) {
    const i = info.get(s);
    if (i) for (const n of i.exports) ambient.add(n);
  }
  // Names published by the non-SPA single-script pages are irrelevant here, but
  // theme-init.js runs before everything and publishes nothing.

  // owner: exported name -> file that exports it (SPA modules only).
  const owner = new Map();
  for (const f of files) {
    for (const n of info.get(f).exports) {
      if (owner.has(n)) {
        console.error(`  ! ${appName}: '${n}' exported by both ${owner.get(n)} and ${f}`);
      }
      owner.set(n, f);
    }
  }

  const results = [];
  for (const f of files) {
    const { code, a, exports } = info.get(f);

    // Imports: free references this file makes that another SPA module owns.
    const needed = new Map(); // file -> Set(names)
    const unresolved = [];
    for (const name of a.freeRefs.keys()) {
      if (ambient.has(name)) continue;
      const from = owner.get(name);
      if (from && from !== f) {
        if (!needed.has(from)) needed.set(from, new Set());
        needed.get(from).add(name);
      } else if (!from) {
        unresolved.push(name);
      }
    }

    results.push({ file: f, exports, needed, unresolved, code, a });
  }

  // Report.
  console.log(`\n=== ${appName} — ${files.length} SPA modules (load order: ${order.length} tags) ===`);
  let totalImports = 0;
  for (const r of results) {
    const imps = [...r.needed.values()].reduce((n, s) => n + s.size, 0);
    totalImports += imps;
    const flag = r.unresolved.length ? `  UNRESOLVED: ${r.unresolved.join(', ')}` : '';
    console.log(`  ${r.file.padEnd(22)} exports ${String(r.exports.size).padStart(3)}  imports ${String(imps).padStart(3)} from ${String(r.needed.size).padStart(2)} files${flag}`);
  }
  console.log(`  total import bindings: ${totalImports}`);

  // Cycle detection over the generated import graph.
  const graph = new Map(results.map((r) => [r.file, [...r.needed.keys()]]));
  const cycles = findCycles(graph);
  console.log(cycles.length ? `  import cycles: ${cycles.length} (benign iff no LOAD-TIME ref crosses one — measured below)`
                            : '  no import cycles');

  // THE MEASUREMENT THAT DECIDES THIS STAGE.
  //
  // Under classic scripts, load order made every reference safe and the export
  // guard's rule 2 enforced that load-time refs only point at earlier files.
  // Under ESM the evaluation order is a depth-first walk from the entry, which
  // is NOT the old script order — so a cycle is only harmless if nothing reads
  // an imported binding while a module is still evaluating. Function bodies are
  // fine at any depth (hoisted declarations + live bindings); top-level reads
  // are not.
  const loadTimeEdges = [];
  for (const r of results) {
    for (const [name, meta] of r.a.freeRefs) {
      if (!meta.loadTime || ambient.has(name)) continue;
      const from = owner.get(name);
      if (from && from !== r.file) loadTimeEdges.push([r.file, from, name]);
    }
  }
  const ltGraph = new Map(results.map((r) => [r.file, []]));
  for (const [a2, b2] of loadTimeEdges) ltGraph.get(a2).push(b2);
  const ltCycles = findCycles(ltGraph);
  console.log(`  load-time cross-module refs: ${loadTimeEdges.length}`);

  // Classify each load-time edge. A load-time read is SAFE when it cannot
  // observe an uninitialised binding, which happens in exactly two ways:
  //
  //   (a) the source module imports nothing — it is always evaluated before
  //       anything that imports it, so its bindings are initialised. This is
  //       why delegation.js, registry.js and env.js are deliberately
  //       dependency-free; adding one import to any of them re-opens the hazard.
  //   (b) the binding is a FUNCTION DECLARATION — hoisted and initialised at
  //       instantiation, before any module body runs, so evaluation order and
  //       cycles cannot reach it.
  //
  // Anything else is a const/let/class read at load time from a module that may
  // still be evaluating: a runtime ReferenceError at boot that no build step and
  // no existing guard would catch. Under classic scripts this class could not
  // exist, because load order made every reference safe.
  const hazards = [];
  for (const [f, from, name] of loadTimeEdges) {
    const src = info.get(from);
    const depFree = (results.find((r) => r.file === from)?.needed.size ?? 0) === 0;
    const kind = src.a.topLevelDecls.get(name);
    const safe = depFree || kind === 'function';
    const why = depFree ? 'source imports nothing' : kind === 'function' ? 'hoisted function declaration' : `${kind || 'unknown'} — NOT hoisted`;
    if (!safe) hazards.push([f, from, name, why]);
  }
  console.log(ltCycles.length
    ? `  load-time cycles: ${ltCycles.length}`
    : '  load-time graph is ACYCLIC');
  if (hazards.length) {
    console.log(`  !! TDZ HAZARDS (${hazards.length}) — would be a boot-time ReferenceError under ESM:`);
    for (const [f, from, name, why] of hazards) console.log(`     ${f} reads ${name} from ${from} (${why})`);
  } else {
    console.log(`  no TDZ hazards: all ${loadTimeEdges.length} load-time reads hit a dependency-free module or a hoisted function`);
  }
  const hazardCount = hazards.length;

  if (write) {
    for (const r of results) writeFileSync(join(jsDir, r.file), rewrite(r));
    console.log(`  WROTE ${results.length} files`);
  }
  return hazardCount;
}

// findCycles — every elementary cycle matters here: ESM tolerates cycles for
// function references but breaks on load-time use of a not-yet-initialised
// binding, so each one has to be looked at rather than counted.
function findCycles(graph) {
  const cycles = [];
  const seen = new Set();
  const stack = [];
  const onStack = new Set();
  const visit = (n) => {
    if (onStack.has(n)) {
      const i = stack.indexOf(n);
      const cyc = stack.slice(i).concat(n);
      const key = [...cyc].sort().join('|');
      if (!seen.has(key)) { seen.add(key); cycles.push(cyc); }
      return;
    }
    stack.push(n); onStack.add(n);
    for (const m of graph.get(n) || []) visit(m);
    stack.pop(); onStack.delete(n);
  };
  for (const n of graph.keys()) visit(n);
  return cycles;
}

// rewrite — produce the ESM source for one file.
function rewrite({ file, exports, needed, code, a }) {
  let src = code;

  // 1. Remove the explicit export block; ESM gets an `export { … }` instead.
  src = stripExportBlock(src);

  // 2. Unwrap the IIFE. The bodies are authored at column 0 inside the wrapper
  //    (verified across the tree), so no reindentation is needed — which is
  //    what keeps this a mechanical transform rather than a reformat.
  if (a.iife) src = unwrapIIFE(src);

  // 3. Imports, in load order of the source file name for a stable diff.
  const importLines = [...needed.entries()]
    .sort(([x], [y]) => x.localeCompare(y))
    .map(([from, names]) => `import { ${[...names].sort().join(', ')} } from './${from}';`);

  const exportLine = exports.size
    ? `\nexport { ${[...exports].sort().join(', ')} };\n`
    : '';

  const head = importLines.length ? importLines.join('\n') + '\n\n' : '';
  return head + src.trim() + '\n' + exportLine;
}

function stripExportBlock(src) {
  // The block is the last `Object.assign(window, { … });` in the file.
  const i = src.lastIndexOf('Object.assign(window, {');
  if (i === -1) return src;
  let j = src.indexOf('{', i);
  let depth = 0;
  for (; j < src.length; j++) {
    if (src[j] === '{') depth++;
    else if (src[j] === '}') { depth--; if (depth === 0) break; }
  }
  const end = src.indexOf(';', j);
  // Also drop the comment banner that introduces the block, if present.
  let start = i;
  const before = src.slice(0, i);
  const banner = before.lastIndexOf('// ── Exports:');
  if (banner !== -1 && before.slice(banner).split('\n').every((l) => l.startsWith('//') || l.trim() === '')) {
    start = banner;
  }
  return src.slice(0, start) + src.slice(end + 1);
}

function unwrapIIFE(src) {
  const open = src.search(/\(\s*(?:\(\s*\)\s*=>|function\s*\(\s*\))\s*\{/);
  if (open === -1) return src;
  const braceAt = src.indexOf('{', open);
  const close = src.lastIndexOf('})();');
  if (close === -1) return src;
  let body = src.slice(braceAt + 1, close);
  // Drop the leading 'use strict' directive: modules are always strict.
  body = body.replace(/^\s*'use strict';\n/, '');
  return src.slice(0, open).trimEnd() + '\n' + body;
}

const args = process.argv.slice(2);
const write = args.includes('--write');
const only = args.includes('--app') ? args[args.indexOf('--app') + 1] : null;
let hazards = 0;
for (const app of ['octbase-frontend', 'octbase-mobile']) {
  if (only && app !== only) continue;
  if (!existsSync(join(REPO, app))) continue;
  hazards += processApp(app, { write });
}
// Non-zero exit on a TDZ hazard: this is the migration's real gate, and it must
// fail a pipeline rather than print a warning nobody reads.
if (hazards) {
  console.error(`\ncodemod: ${hazards} TDZ hazard(s) — fix by making the source module dependency-free or the binding a function declaration.`);
  process.exit(1);
}
