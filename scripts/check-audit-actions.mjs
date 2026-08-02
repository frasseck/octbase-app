#!/usr/bin/env node
// Guard: the admin audit-log view knows every action the backend writes.
//
// internal/auditlog/domain.go declares the actions; octbase-frontend/js/admin.js
// lists them in ACTION_KEYS, which is both the filter dropdown and the label
// lookup. An action missing from that list cannot be filtered for and renders
// as its raw enum (LOGIN_FAILED instead of "Failed sign-in"), so it is present
// in the log and absent from the tool people use to read the log. Nine actions
// had drifted that way — including every password event, every MFA event, and
// the refresh-token-reuse alarm.
//
// Checks both directions: an action the frontend does not list, and an entry in
// ACTION_KEYS the backend never writes (a stale key produces a dropdown option
// that matches nothing). Also requires a label in every locale file.

import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

const ROOT = join(fileURLToPath(new URL('.', import.meta.url)), '..');
const DOMAIN = join(ROOT, 'octbase-api/internal/auditlog/domain.go');
const ADMIN_JS = join(ROOT, 'octbase-frontend/js/admin.js');
const LOCALES = ['octbase-frontend/locales/en.json', 'octbase-frontend/locales/de.json'];

// The Action* block: `ActionLoginFailed = "LOGIN_FAILED"`.
const backend = new Set(
  [...readFileSync(DOMAIN, 'utf8').matchAll(/^\s*Action\w+\s*=\s*"([A-Z][A-Z0-9_]+)"/gm)].map(m => m[1]),
);

const listed = readFileSync(ADMIN_JS, 'utf8').match(/const ACTION_KEYS = \[([\s\S]*?)\];/);
if (!listed) {
  console.error('audit-action guard: ACTION_KEYS not found in octbase-frontend/js/admin.js');
  process.exit(1);
}
const frontend = new Set([...listed[1].matchAll(/'([A-Z][A-Z0-9_]+)'/g)].map(m => m[1]));

let failed = false;
const missing = [...backend].filter(a => !frontend.has(a)).sort();
if (missing.length) {
  failed = true;
  console.error(`admin.js ACTION_KEYS is missing ${missing.length} action(s) the backend writes:`);
  for (const a of missing) console.error(`  ${a}`);
}
const stale = [...frontend].filter(a => !backend.has(a)).sort();
if (stale.length) {
  failed = true;
  console.error(`admin.js ACTION_KEYS lists ${stale.length} action(s) the backend never writes:`);
  for (const a of stale) console.error(`  ${a}`);
}
for (const rel of LOCALES) {
  const dict = JSON.parse(readFileSync(join(ROOT, rel), 'utf8'));
  const unlabelled = [...frontend].filter(a => typeof dict?.admin?.action?.[a] !== 'string').sort();
  if (unlabelled.length) {
    failed = true;
    console.error(`${rel}: ${unlabelled.length} action(s) without a label`);
    for (const a of unlabelled) console.error(`  admin.action.${a}`);
  }
}

if (failed) {
  console.error('\nAdd the action to ACTION_KEYS and to admin.action.* in every locale file.');
  process.exit(1);
}
console.log(`audit-action guard: clean ✓ (${backend.size} actions)`);
