#!/usr/bin/env node
// Guard: every literal key a t() call asks for exists in every locale file of
// the SPA that made the call.
//
// Why this is needed. t() answers a key it cannot find with the key itself,
// and warns to a console nobody is watching. Callers then paper over that with
//   t('common.back') !== 'common.back' ? t('common.back') : 'Back'
// which turns a missing key into a silently English label — correct-looking in
// every screenshot, wrong only for the German reader nobody screenshots. The
// mobile confirm sheet shipped exactly that: it read `common.cancel`, which
// mobile does not have (it is `form.cancel`), and the German dialog rendered
// "Cancel" under "Trotzdem erledigen". Nothing in CI saw it.
//
// So the check comes from both sources of truth: the keys grepped out of the
// call sites, and the keys read out of the locale JSON.
//
// It also re-checks what the locale files owe each other — every key present in
// one language of a site is present in the others — because a key missing from
// de.json alone is the same failure arriving by a different route: resolve()
// falls back to English rather than to the raw key path, so it never even warns.
//
// Not covered by design: keys built at runtime (`t('errors.' + code)`,
// `t(`status.${s}`)`). They have no literal to check. The error-code half of
// that family has its own guard — scripts/check-error-translations.mjs — and
// the audit-action half has scripts/check-audit-actions.mjs.

import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';

const ROOT = join(fileURLToPath(new URL('.', import.meta.url)), '..');

// Each SPA owns its own locales on purpose (the phone may ship a shorter label
// through its own key), so a key is checked against the site that uses it.
// octbase-shared is imported by both, so its keys are owed by both.
const SITES = [
  { name: 'octbase-frontend', sources: ['octbase-frontend/js', 'octbase-shared'], locales: ['en', 'de'] },
  { name: 'octbase-mobile', sources: ['octbase-mobile/js', 'octbase-shared'], locales: ['en', 'de'] },
];

// t('key'), t("key"), t(`key`), i18n.t('key'), window.t('key') — with an
// optional second argument. A backtick literal only counts when it has no
// ${...} in it; anything interpolated is a runtime key and is skipped.
const CALL_RE = /(?<![.\w])(?:i18n\.|window\.)?t\(\s*(['"`])([^'"`\n]*?)\1/g;

// A key is a dot path of identifier-ish segments. This rejects the leftovers of
// a concatenation (`t('errors.' + code)` yields 'errors.') and sentence-shaped
// arguments that belong to some other t().
const KEY_RE = /^[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z0-9_]+)*$/;

function jsFiles(dir, out = []) {
  for (const name of readdirSync(dir)) {
    if (name === 'node_modules') continue;
    const p = join(dir, name);
    if (statSync(p).isDirectory()) jsFiles(p, out);
    // The test layer asserts on missing keys on purpose ("t() falls back to the
    // key itself for unknown keys"), so it is not a call site.
    else if (name.endsWith('.js') && !name.endsWith('.test.js')) out.push(p);
  }
  return out;
}

function collectKeys(sources) {
  const found = new Map(); // key -> Set of "file:line"
  for (const src of sources) {
    for (const file of jsFiles(join(ROOT, src))) {
      const text = readFileSync(file, 'utf8');
      CALL_RE.lastIndex = 0;
      let m;
      while ((m = CALL_RE.exec(text)) !== null) {
        const key = m[2];
        if (!KEY_RE.test(key)) continue;
        const line = text.slice(0, m.index).split('\n').length;
        if (!found.has(key)) found.set(key, new Set());
        found.get(key).add(`${relative(ROOT, file)}:${line}`);
      }
    }
  }
  return found;
}

function lookup(data, key) {
  return key.split('.').reduce((node, part) => {
    if (node == null || typeof node !== 'object' || !(part in node)) return undefined;
    return node[part];
  }, data);
}

// Mirrors resolve(): a string is a translation, and so is a plural object — t()
// picks node.one / node.other off it. Anything else (a namespace reached by a
// too-short key) is not something a caller can render.
function isTranslation(node) {
  if (typeof node === 'string') return true;
  if (node && typeof node === 'object') return typeof node.one === 'string' || typeof node.other === 'string';
  return false;
}

function flatten(node, prefix, out) {
  for (const [k, v] of Object.entries(node)) {
    const path = prefix ? `${prefix}.${k}` : k;
    if (v && typeof v === 'object' && !isTranslation(v)) flatten(v, path, out);
    else out.add(path);
  }
  return out;
}

let failed = false;

for (const site of SITES) {
  const dicts = new Map();
  for (const lang of site.locales) {
    dicts.set(lang, JSON.parse(readFileSync(join(ROOT, site.name, 'locales', `${lang}.json`), 'utf8')));
  }

  // Half 1: every key the code asks for exists, in every language.
  const used = collectKeys(site.sources);
  const missing = [];
  for (const [key, sites] of [...used].sort()) {
    const absent = site.locales.filter(lang => !isTranslation(lookup(dicts.get(lang), key)));
    if (absent.length) missing.push({ key, absent, where: [...sites].sort() });
  }
  if (missing.length) {
    failed = true;
    console.error(`\n${site.name}: ${missing.length} t() key(s) with no translation`);
    for (const { key, absent, where } of missing) {
      console.error(`  ${key}  (missing in ${absent.join(', ')})`);
      for (const w of where) console.error(`      ${w}`);
    }
  }

  // Half 2: the locale files agree on which keys exist. A key in en.json alone
  // never renders as a raw key path — resolve() falls back to English — so this
  // failure is quieter than the one above, not louder.
  const flat = new Map(site.locales.map(lang => [lang, flatten(dicts.get(lang), '', new Set())]));
  const union = new Set([...flat.values()].flatMap(s => [...s]));
  const reported = new Set(missing.map(m => m.key));
  const gaps = [];
  for (const key of [...union].sort()) {
    if (reported.has(key)) continue; // already named above, with its call sites
    const absent = site.locales.filter(lang => !flat.get(lang).has(key));
    if (absent.length) gaps.push(`  ${key}  (missing in ${absent.join(', ')})`);
  }
  if (gaps.length) {
    failed = true;
    console.error(`\n${site.name}: ${gaps.length} key(s) not present in every locale file`);
    for (const g of gaps) console.error(g);
  }

  if (!missing.length && !gaps.length) {
    console.log(`i18n-key guard: ${site.name} clean ✓ (${used.size} literal keys × ${site.locales.length} locales)`);
  }
}

if (failed) {
  console.error('\nAdd the key to EVERY locale file of that SPA (en and de), or fix the key at the call site.');
  console.error('A key missing from one language ships that language\'s reader an English string, silently.');
  process.exit(1);
}
