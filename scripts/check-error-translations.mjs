#!/usr/bin/env node
// Guard: every error code the API can emit has a translation in every locale
// file of both SPAs.
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
// VALIDATION_ERROR is exempt: it is one code covering many messages and is
// keyed by message text through shared.validationMessageKeys, not by code (its
// keys live under errors.validation.*). TEST_CODE is a fixture.

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

// The two ways a stable code reaches a response: a shared.Write*Error call and
// a DomainError literal (which the handlers map onto one).
const PATTERNS = [
  /Write(?:Error|ValidationError|UpdateError)\([^)]*?"([A-Z][A-Z0-9_]{2,})"/g,
  /Code:\s*"([A-Z][A-Z0-9_]{2,})"/g,
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
}

if (failed) {
  console.error('\nAdd the key to errors.* in EVERY locale file of BOTH SPAs (en and de).');
  console.error('A key present in one language only renders as the raw key path.');
  process.exit(1);
}
console.log(`error-translation guard: clean ✓ (${codes.size} codes × ${LOCALE_FILES.length} locale files)`);
