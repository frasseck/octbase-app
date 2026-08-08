#!/usr/bin/env node
// Guard: every error the API can emit has a translation in every locale file
// of both SPAs — in two halves, because the API keys errors two different ways.
//
// The API answers errors as { code, message, messageKey }, where messageKey is
// derived from the code by shared.MessageKeyFor: "errors." + camelCase(code).
// Both SPAs' apiErrorMessage() translates that key and falls back to the raw
// English message when the key is missing — which is why a missing translation
// is invisible in testing: the UI still shows something, just in the wrong
// language, and only for the codes nobody happened to trigger.
//
// So the check has to come from the source of truth on both sides: the codes
// grepped out of the Go handlers, and the keys read out of the locale JSON.
//
// VALIDATION_ERROR is exempt from the by-code half: it is one code covering
// many messages, keyed by message TEXT through shared.validationMessageKeys
// rather than by code. TEST_CODE is a fixture.
//
// The second half covers exactly that map, and exists because leaving it
// uncovered cost every validation message its translation. The Go side spelt
// the keys "errors.validation.<name>" while all four locale files carry them
// at top level as "validation.<name>" — so `t()` resolved nothing and every
// VALIDATION_ERROR rendered its raw English message, in German too. All 17
// keys, all four files (measured 2026-08-08, OCT-27).
//
// Neither existing guard could see it: this one exempted VALIDATION_ERROR by
// design, and check-i18n-keys.mjs only reads literal t('…') call sites, while
// this key is assembled at runtime from the API response. That gap between two
// guards is the thing being closed here.
//
// The check deliberately reads the key strings VERBATIM out of the Go map and
// looks up that exact dotted path. It asserts the two sides AGREE; it does not
// hardcode which namespace is correct, so moving the keys wholesale stays a
// one-sided edit that this guard will catch if only one side moves.

import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

const ROOT = join(fileURLToPath(new URL('.', import.meta.url)), '..');
const GO_ROOTS = [join(ROOT, 'octbase-api/internal'), join(ROOT, 'octbase-api/cmd')];
const LOCALE_FILES = [
  'octbase-frontend/locales/en.json',
  'octbase-frontend/locales/de.json',
  'octbase-mobile/locales/en.json',
  'octbase-mobile/locales/de.json',
];
const EXEMPT = new Set(['VALIDATION_ERROR', 'TEST_CODE']);

// The three ways a stable code reaches a response: a shared.Write*Error call
// with the code as a literal, a DomainError literal (which the handlers map
// onto one), and a Go constant named Code<Something> whose value travels to a
// Write*Error call as an identifier. The third pattern exists because the
// first two only see literals at the call site — the SCM provider codes
// (scmintegration/provider.go, `CodeRepoNotFound = "SCM_REPO_NOT_FOUND"` etc.)
// shipped untranslated for weeks while this guard printed clean (2026-08-02
// review). Convention this encodes: name error-code constants Code*, and this
// guard will see them; a code smuggled through a differently-named constant or
// a variable still gets past it.
const PATTERNS = [
  // The argument scan tolerates ONE level of nested parens — a call like
  // WriteError(w, statusFor(x), "CODE", …) escaped a plain [^)]*? because the
  // scan stopped at statusFor's ")". Bounded ({0,300} chars, lazy, and the two
  // alternatives cannot match the same character) so it cannot backtrack
  // catastrophically; a second nesting level is out of scope on purpose.
  /Write(?:Error|ValidationError|UpdateError)\((?:[^()]|\([^()]*\)){0,300}?"([A-Z][A-Z0-9_]{2,})"/g,
  /Code:\s*"([A-Z][A-Z0-9_]{2,})"/g,
  /\bCode[A-Z]\w*\s*=\s*"([A-Z][A-Z0-9_]{2,})"/g,
];

function goFiles(dir, out = []) {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) goFiles(p, out);
    else if (name.endsWith('.go') && !name.endsWith('_test.go')) out.push(p);
  }
  return out;
}

function camelCase(code) {
  const parts = code.toLowerCase().split('_');
  return parts[0] + parts.slice(1).filter(Boolean).map(p => p[0].toUpperCase() + p.slice(1)).join('');
}

const codes = new Set();
for (const root of GO_ROOTS) {
  for (const file of goFiles(root)) {
    const src = readFileSync(file, 'utf8');
    for (const re of PATTERNS) {
      re.lastIndex = 0;
      let m;
      while ((m = re.exec(src)) !== null) {
        if (!EXEMPT.has(m[1])) codes.add(m[1]);
      }
    }
  }
}

// validationKeys reads the i18n keys out of shared.validationMessageKeys —
// the map that turns a VALIDATION_ERROR's English message into a messageKey.
// Values only: the map's Go-side keys are the English sentences, which are not
// what a client looks up.
function validationKeys() {
  const src = readFileSync(join(ROOT, 'octbase-api/internal/shared/i18nerrors.go'), 'utf8');
  const block = src.match(/validationMessageKeys\s*=\s*map\[string\]string\{([\s\S]*?)\n\}/);
  if (!block) {
    console.error('could not find validationMessageKeys in internal/shared/i18nerrors.go');
    console.error('If it was renamed or restructured, update this guard — do not delete the check.');
    process.exit(1);
  }
  return [...new Set([...block[1].matchAll(/:\s*"([^"]+)"/g)].map(m => m[1]))];
}

// lookup walks a dotted key path, the way the SPAs' i18n resolve() does.
function lookup(dict, key) {
  return key.split('.').reduce((node, part) => (node == null ? undefined : node[part]), dict);
}

const vKeys = validationKeys();

let failed = false;
for (const rel of LOCALE_FILES) {
  const dict = JSON.parse(readFileSync(join(ROOT, rel), 'utf8'));

  const missing = [...codes]
    .map(camelCase)
    .filter(key => typeof dict?.errors?.[key] !== 'string')
    .sort();
  if (missing.length) {
    failed = true;
    console.error(`${rel}: ${missing.length} untranslated error code(s)`);
    for (const key of missing) console.error(`  errors.${key}`);
  }

  const missingV = vKeys.filter(key => typeof lookup(dict, key) !== 'string').sort();
  if (missingV.length) {
    failed = true;
    console.error(`${rel}: ${missingV.length} untranslated validation message(s)`);
    for (const key of missingV) console.error(`  ${key}`);
  }
}

if (failed) {
  console.error('\nAdd the key in EVERY locale file of BOTH SPAs (en and de).');
  console.error('A key present in one language only renders as the raw key path;');
  console.error('a validation key the locales spell differently renders raw English in both.');
  process.exit(1);
}
console.log(`error-translation guard: clean ✓ (${codes.size} codes + ${vKeys.length} validation messages × ${LOCALE_FILES.length} locale files)`);
