#!/usr/bin/env node
// Frontend HTML-injection guard.
//
// The SPA renders by assigning HTML strings to .innerHTML. That is safe ONLY
// because every dynamic value is funnelled through an escaping/trusted producer:
//   - esc(x)            — HTML-escapes user/server data
//   - html`...`         — auto-escaping tagged template
//   - raw(x)            — explicitly-trusted fragment (opt-out, used sparingly)
//   - sanitizeRichText  — the rich-text allowlist sanitizer
//   - icon(), t(), and render helpers (fooInner(), fooHtml(), ...) — functions
//     that themselves return already-escaped HTML.
//
// This guard fails the build on the patterns that bypass that convention:
//   1. `.innerHTML += ...`            (append-concatenation — always a smell)
//   2. string concatenation into HTML (`'<b>' + x`)  — the classic XSS antipattern
//   3. document.write(...)
//   4. a template-literal interpolation that splices a known user-content field
//      (`.title`, `.name`, `.text`, `.description`, ...) straight in without
//      esc()/html`` — i.e. attacker-controlled text rendered unescaped.
//
// Rule 4 is deliberately targeted (high-risk data fields) rather than "every
// interpolation must be a call": the SPA legitimately interpolates pre-built
// HTML fragment variables (e.g. `${notifBtn}`) and safe enums/counts into
// untagged templates, so a blanket rule produces only false positives. The
// subtler "is this variable already safe?" judgement stays with code review;
// see js/README.md.
//
// Both SPAs render the same way and are both scanned: each has its own copy of
// the esc()/html``/raw() helpers, so an unguarded app is an unguarded sink.
// The scan recurses into subdirectories (skipping node_modules, dist*, tests)
// so a future js/ restructure can't silently drop files out of the guard, and
// it also covers octbase-shared's top-level modules: @octbase/shared code is
// imported by both SPAs, so a sink there is a sink in both apps at once.
//
// Exit non-zero on any violation. Run: node scripts/check-innerhtml.mjs

import { readFileSync, readdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const REPO = join(dirname(fileURLToPath(import.meta.url)), '..');
const APPS = ['octbase-frontend', 'octbase-mobile'];
const SINK = /\.(innerHTML|outerHTML)\s*(\+)?=\s*/g;

// User-content fields that must never be spliced into HTML unescaped. Limited
// to fields that hold free-form, attacker-influenceable text.
const USER_FIELDS = /\.(title|name|displayName|fullName|text|description|body|message|summary|email|comment|filename)\b/;

// Ratchet: files fully migrated to the auto-escaping html`` tagged template.
// In these files every `innerHTML =` template literal must be html``-tagged,
// so ALL interpolations are escaped by construction (trusted fragments opt out
// explicitly via raw()). Migrate a file, add it here — never remove an entry.
//
// Entries are app-scoped repo-relative paths, not bare basenames: the two SPAs
// have same-named files, and a basename would silently opt a future
// octbase-mobile/js/realtime.js into strict mode it was never migrated for.
const STRICT_FILES = new Set([
  'octbase-frontend/js/realtime.js',
  'octbase-frontend/js/views-mindmap.js',
  'octbase-frontend/js/views-settings.js',
  'octbase-frontend/js/views-stats.js',
  'octbase-mobile/js/app.js',
]);

// An interpolation is REJECTED when it reads a known user-content field but is
// not routed through esc() or an html`` tagged template. String-literal
// contents are stripped first so i18n keys like t('form.title') don't match.
function interpolationAllowed(expr) {
  const e = expr.trim().replace(/'[^']*'|"[^"]*"/g, "''"); // drop string contents
  if (!USER_FIELDS.test(e)) return true;        // not a high-risk field access
  if (/\besc\s*\(/.test(e)) return true;        // explicitly escaped
  if (/`/.test(e)) return true;                 // contains a tagged/nested template
  return false;
}

// Extract ${...} interpolations from a template literal body, respecting nested
// braces. `src` starts just after the opening backtick.
function interpolations(src) {
  const out = [];
  for (let i = 0; i < src.length; i++) {
    if (src[i] === '\\') { i++; continue; }
    if (src[i] === '`') break; // end of template
    if (src[i] === '$' && src[i + 1] === '{') {
      let depth = 1, j = i + 2;
      for (; j < src.length && depth > 0; j++) {
        if (src[j] === '{') depth++;
        else if (src[j] === '}') depth--;
      }
      out.push(src.slice(i + 2, j - 1));
      i = j - 1;
    }
  }
  return out;
}

const violations = [];
function report(file, idx, text, msg) {
  // crude line number from char offset
  const line = text.slice(0, idx).split('\n').length;
  violations.push(`${file}:${line}: ${msg}`);
}

// Directories never scanned: third-party trees, build output, test harnesses.
const SKIP_DIRS = new Set(['node_modules', 'tests']);
const skipDir = (name) => SKIP_DIRS.has(name) || name.startsWith('dist');

// Recursively collect .js files (excluding *.test.js) under absDir, reporting
// paths relative to relDir.
function collect(absDir, relDir, out) {
  for (const ent of readdirSync(absDir, { withFileTypes: true })) {
    if (ent.isDirectory()) {
      if (!skipDir(ent.name)) collect(join(absDir, ent.name), `${relDir}/${ent.name}`, out);
    } else if (ent.name.endsWith('.js') && !ent.name.endsWith('.test.js')) {
      out.push({ path: `${relDir}/${ent.name}`, abs: join(absDir, ent.name) });
    }
  }
}

const jsFiles = [];
for (const app of APPS) collect(join(REPO, app, 'js'), `${app}/js`, jsFiles);
// octbase-shared keeps its modules at the package top level; anything nested
// there today is tooling, so only the top-level files are runtime surface.
for (const ent of readdirSync(join(REPO, 'octbase-shared'), { withFileTypes: true })) {
  if (ent.isFile() && ent.name.endsWith('.js') && !ent.name.endsWith('.test.js')) {
    jsFiles.push({ path: `octbase-shared/${ent.name}`, abs: join(REPO, 'octbase-shared', ent.name) });
  }
}

for (const { path: f, abs } of jsFiles) {
  const text = readFileSync(abs, 'utf8');

  for (const m of text.matchAll(/document\.write\s*\(/g)) report(f, m.index, text, 'document.write() is forbidden');

  SINK.lastIndex = 0;
  let m;
  while ((m = SINK.exec(text)) !== null) {
    const append = m[2] === '+';
    const rhsStart = m.index + m[0].length;
    if (append) { report(f, m.index, text, '`.innerHTML +=` (append-concatenation) is forbidden'); continue; }

    const rest = text.slice(rhsStart);
    if (rest[0] === '`') {
      if (STRICT_FILES.has(f)) {
        report(f, m.index, text, 'untagged template assigned to innerHTML in a strict (html``-migrated) file — use html`` and wrap trusted fragments in raw()');
      }
      for (const expr of interpolations(rest.slice(1))) {
        if (!interpolationAllowed(expr)) {
          report(f, rhsStart, text, `unescaped interpolation in innerHTML: \${${expr.trim()}} — wrap in esc() or use html\`\``);
        }
      }
    } else {
      // Non-template RHS: scan to the statement end for string concatenation.
      const stmt = rest.slice(0, rest.search(/;\s*(\n|$)/) + 1 || rest.indexOf(';') + 1);
      if (/['"]\s*\+|\+\s*['"]/.test(stmt)) {
        report(f, rhsStart, text, 'string concatenation into innerHTML — use esc()/html``');
      }
    }
  }
}

if (violations.length) {
  console.error(`HTML-injection guard: ${violations.length} violation(s)\n`);
  for (const v of violations) console.error('  ' + v);
  process.exit(1);
}
console.log('HTML-injection guard: clean ✓');
