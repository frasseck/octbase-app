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
import { fileURLToPath, pathToFileURL } from 'node:url';

const REPO = join(dirname(fileURLToPath(import.meta.url)), '..');
const APPS = ['octbase-frontend', 'octbase-mobile'];
// The (?!=) keeps comparisons (`el.innerHTML === x`) from matching as sinks.
// Logical assignments (||= &&= ??=) assign too — they were unmatched, so
// `el.innerHTML ||= userHtml` sailed past every rule; their RHS gets the same
// scrutiny as a plain `=`.
const SINK = /\.(innerHTML|outerHTML)\s*(\+|\|\||&&|\?\?)?=(?!=)\s*/g;

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
// Calls whose ARGUMENTS are their own responsibility: the escaping/trusted
// producers from the header (esc, t, icon, raw, sanitizeRichText, and the
// fooHtml()/fooInner() render-helper convention). A user-content field passed
// INTO one of these is not an unescaped splice, so the argument list is
// dropped before the field check. Applied twice to cover one nesting level.
const TRUSTED_CALL = /\b(?:esc|t|icon|raw|sanitizeRichText|\w+(?:Html|HTML|Inner))\s*\([^()]*\)/g;

// Remove html`…` tagged-template spans from an expression. Everything inside
// the tagged template is escaped by construction, so it can't be an unescaped
// splice — but the mere PRESENCE of one proves nothing about the rest of the
// expression: `${item.title + html`x`}` still splices item.title raw. Cutting
// the safe spans out and testing what remains closes that hole.
function stripTaggedTemplates(e) {
  let m;
  while ((m = /\bhtml\s*`/.exec(e)) !== null) {
    const bodyStart = m.index + m[0].length;
    const end = templateEnd(e.slice(bodyStart));
    e = e.slice(0, m.index) + "''" + e.slice(Math.min(bodyStart + end + 1, e.length));
  }
  return e;
}

function interpolationAllowed(expr) {
  let e = expr.trim().replace(/'[^']*'|"[^"]*"/g, "''"); // drop string contents
  e = stripTaggedTemplates(e);
  e = e.replace(TRUSTED_CALL, "''").replace(TRUSTED_CALL, "''");
  if (!USER_FIELDS.test(e)) return true;        // not a high-risk field access
  if (/\besc\s*\(/.test(e)) return true;        // explicitly escaped
  return false;
}

// Index of the closing backtick of a template literal whose body starts at
// src[0] (the opening backtick already consumed), respecting escapes and
// ${...} brace nesting — the same fidelity as interpolations() below.
function templateEnd(src) {
  for (let i = 0; i < src.length; i++) {
    if (src[i] === '\\') { i++; continue; }
    if (src[i] === '`') return i;
    if (src[i] === '$' && src[i + 1] === '{') {
      let depth = 1, j = i + 2;
      for (; j < src.length && depth > 0; j++) {
        if (src[j] === '{') depth++;
        else if (src[j] === '}') depth--;
      }
      i = j - 1;
    }
  }
  return src.length;
}

// Blank the BODIES of template literals (keeping the backticks) so the
// concatenation scan sees `x + `…`` without tripping over a harmless
// `${a + 'b'}` inside a template.
function blankTemplateBodies(s) {
  let out = '';
  for (let i = 0; i < s.length; i++) {
    if (s[i] === '`') {
      const end = templateEnd(s.slice(i + 1));
      out += '`' + ' '.repeat(end);
      if (i + 1 + end < s.length) out += '`';
      i = i + 1 + end;
    } else {
      out += s[i];
    }
  }
  return out;
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

// Scan one source file's text; returns the violation strings for it. Split
// out (and exported) so the unit tests can probe the rules against fixture
// snippets without touching the repo scan below.
export function scanSource(f, text) {
  const out = [];
  const report = (idx, msg) => {
    // crude line number from char offset
    const line = text.slice(0, idx).split('\n').length;
    out.push(`${f}:${line}: ${msg}`);
  };

  for (const m of text.matchAll(/document\.write\s*\(/g)) report(m.index, 'document.write() is forbidden');

  SINK.lastIndex = 0;
  let m;
  while ((m = SINK.exec(text)) !== null) {
    const append = m[2] === '+';
    const rhsStart = m.index + m[0].length;
    if (append) { report(m.index, '`.innerHTML +=` (append-concatenation) is forbidden'); continue; }

    const rest = text.slice(rhsStart);
    if (rest[0] === '`') {
      if (STRICT_FILES.has(f)) {
        report(m.index, 'untagged template assigned to innerHTML in a strict (html``-migrated) file — use html`` and wrap trusted fragments in raw()');
      }
      for (const expr of interpolations(rest.slice(1))) {
        if (!interpolationAllowed(expr)) {
          report(rhsStart, `unescaped interpolation in innerHTML: \${${expr.trim()}} — wrap in esc() or use html\`\``);
        }
      }
    }

    // The concatenation rule applies regardless of what the RHS starts with:
    // `` el.innerHTML = `<b>` + userText `` is the same antipattern with the
    // string literal spelled as a template, so the branch above is no
    // exemption. Template bodies are blanked first (a `${a + 'b'}` inside a
    // template is not concatenation into the sink, and a multi-line template
    // collapses to one line), and the blanked text is what the statement end
    // is found in, so a `;` inside a template body (say, an entity like
    // &nbsp;) cannot truncate the scan. A statement without a semicolon (ASI)
    // extends past a newline while a line ENDS in a continuation token — or
    // while the NEXT line STARTS with one: JS also parses leading-operator
    // style (`= foo\n  + '<b>'`) as one expression, and scanning only line 1
    // let that spelling of the concat antipattern through unexamined.
    const blanked = blankTemplateBodies(rest.slice(0, 1000));
    const ext = (blanked.match(/^(?:[^\n]*[+,(=?:&|][ \t]*\n|[^\n]*\n(?=[ \t]*[+.?:&|]))*[^\n]*/) || [''])[0];
    const semi = ext.indexOf(';');
    const stmt = semi >= 0 ? ext.slice(0, semi + 1) : ext;
    // A `+` inside a trusted producer's own argument list (`esc(prefix + '!')`)
    // builds the string BEFORE it is escaped — that is not concatenation into
    // the sink, and flagging it forced pointless rewrites. Same double
    // application as interpolationAllowed to cover one nesting level.
    const stmtOutsideTrusted = stmt.replace(TRUSTED_CALL, "''").replace(TRUSTED_CALL, "''");
    if (/['"`]\s*\+|\+\s*['"`]/.test(stmtOutsideTrusted)) {
      report(rhsStart, 'string concatenation into innerHTML — use esc()/html``');
    }
  }
  return out;
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

// Run the repo scan only when invoked as a script — importing this module
// (the unit tests do) must stay side-effect-free.
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const jsFiles = [];
  for (const app of APPS) collect(join(REPO, app, 'js'), `${app}/js`, jsFiles);
  // octbase-shared keeps its modules at the package top level; anything nested
  // there today is tooling, so only the top-level files are runtime surface.
  for (const ent of readdirSync(join(REPO, 'octbase-shared'), { withFileTypes: true })) {
    if (ent.isFile() && ent.name.endsWith('.js') && !ent.name.endsWith('.test.js')) {
      jsFiles.push({ path: `octbase-shared/${ent.name}`, abs: join(REPO, 'octbase-shared', ent.name) });
    }
  }

  const violations = [];
  for (const { path: f, abs } of jsFiles) {
    violations.push(...scanSource(f, readFileSync(abs, 'utf8')));
  }

  if (violations.length) {
    console.error(`HTML-injection guard: ${violations.length} violation(s)\n`);
    for (const v of violations) console.error('  ' + v);
    process.exit(1);
  }
  console.log('HTML-injection guard: clean ✓');
}
