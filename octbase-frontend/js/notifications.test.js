// @vitest-environment jsdom
//
// Unit tests for @octbase/shared/notifications.js against the DESKTOP locale
// files. Run with `npm run test:unit -- notifications.test.js`.
//
// The notification panel used to print `n.message` — an English sentence the
// server composed and stored — so a German reader read English in the bell
// (OCT-323). It now renders `notifications.messages.<kind>` from the kind and
// params the API sends.
//
// The key is built at runtime, so scripts/check-i18n-keys.mjs cannot see it:
// a kind whose translation is missing would fall back to the raw key path and
// warn to a console nobody watches. This file is that guard instead, and it
// reads the kind vocabulary out of the Go source rather than hardcoding it —
// adding a notification kind on the backend without a matching key fails here
// instead of reaching a user's bell.
//
// octbase-mobile/js/notifications.test.js is the same coverage against the
// phone's own locale files, which are a separate set and can drift apart.

import { beforeEach, test, vi } from 'vitest';
import assert from 'node:assert';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const LOCALES_DIR = path.join(__dirname, '..', 'locales');
const NOTIFICATIONS_GO = path.join(
  __dirname, '..', '..', 'octbase-api', 'internal', 'notifications', 'domain.go');

// The module fetches `locales/<lang>.json` relative to the page; serve the
// shipped files from disk so the test exercises the real translations.
function localeFetch(url) {
  const file = path.join(LOCALES_DIR, path.basename(String(url)));
  if (!fs.existsSync(file)) return Promise.resolve({ ok: false });
  return Promise.resolve({ ok: true, json: async () => JSON.parse(fs.readFileSync(file, 'utf8')) });
}

// load returns a fresh module graph in the given language. i18n keeps the
// active locale in module scope, and notifications.js and meta.js both read
// through it, so all three must come from the same reset graph.
async function load(lang = 'en') {
  vi.resetModules();
  const i18n = await import('@octbase/shared/i18n.js');
  const notifications = await import('@octbase/shared/notifications.js');
  await i18n.initI18n();
  if (lang !== 'en') await i18n.setLocale(lang);
  return { ...notifications, t: i18n.t };
}

// backendKinds reads the notification kinds out of the Go constant block, so
// the vocabulary comes from the backend rather than from this file.
function backendKinds() {
  const src = fs.readFileSync(NOTIFICATIONS_GO, 'utf8');
  return [...src.matchAll(/^\tKind\w+\s*=\s*"([a-z_]+)"/gm)].map((m) => m[1]);
}

// task_changed is email-only: it writes no in-app row, so it has no in-app
// message to translate. Listing it here rather than filtering silently means a
// future kind that IS delivered in-app cannot slip through unlisted.
const EMAIL_ONLY_KINDS = ['task_changed'];

const SAMPLE_PARAMS = {
  task_assigned: { title: 'Ship the thing' },
  reviewer_set: { title: 'Ship the thing' },
  status_changed: { title: 'Ship the thing', status: 'IN_REVIEW' },
  mentioned: {},
};

let warnings = [];

beforeEach(() => {
  localStorage.clear();
  vi.stubGlobal('fetch', localeFetch);
  warnings = [];
  vi.spyOn(console, 'warn').mockImplementation((msg) => warnings.push(msg));
});

test('the renderable kinds are exactly the backend kinds that reach an inbox', async () => {
  const { RENDERABLE_KINDS } = await load();
  const kinds = backendKinds();
  // Guard the extraction: a regex that stops matching would make the loops below
  // pass vacuously.
  assert.ok(kinds.length >= 5, `expected the Go source to yield the kinds, got ${kinds.length}`);
  assert.ok(kinds.includes('task_assigned') && kinds.includes('task_changed'), 'extraction missed known kinds');

  const inApp = kinds.filter((k) => !EMAIL_ONLY_KINDS.includes(k));
  assert.deepStrictEqual([...RENDERABLE_KINDS].sort(), inApp.sort());
});

test('every in-app kind renders a translated message in every language', async () => {
  for (const lang of ['en', 'de']) {
    const { notificationMessage } = await load(lang);
    for (const kind of backendKinds().filter((k) => !EMAIL_ONLY_KINDS.includes(k))) {
      const msg = notificationMessage({
        kind, params: SAMPLE_PARAMS[kind], message: 'SERVER ENGLISH',
      });
      assert.ok(!msg.startsWith('notifications.'), `[${lang}] ${kind} rendered a raw key: ${msg}`);
      assert.ok(!msg.includes('{{'), `[${lang}] ${kind} left a placeholder unfilled: ${msg}`);
      assert.notStrictEqual(msg, 'SERVER ENGLISH', `[${lang}] ${kind} fell back to the server sentence`);
    }
    assert.deepStrictEqual(warnings, [], `[${lang}] i18n warnings while rendering notifications`);
  }
});

test('German is actually German, not the English fallback', async () => {
  const { notificationMessage } = await load('de');
  const msg = notificationMessage({ kind: 'task_assigned', params: { title: 'Ship the thing' } });
  assert.ok(msg.includes('zugewiesen'), `expected the German wording, got "${msg}"`);
  assert.ok(msg.includes('Ship the thing'), `expected the title interpolated, got "${msg}"`);
});

test('status renders as a localized label, not the raw enum', async () => {
  const en = await load('en');
  const enMsg = en.notificationMessage({ kind: 'status_changed', params: { title: 'T', status: 'IN_REVIEW' } });
  assert.ok(!enMsg.includes('IN_REVIEW'), `the enum leaked: ${enMsg}`);
  assert.ok(enMsg.includes('In Review'), `expected the English label, got "${enMsg}"`);

  const de = await load('de');
  const deMsg = de.notificationMessage({ kind: 'status_changed', params: { title: 'T', status: 'IN_REVIEW' } });
  assert.ok(!deMsg.includes('IN_REVIEW'), `the enum leaked in German: ${deMsg}`);
  assert.strictEqual(deMsg.includes(de.t('task.status.IN_REVIEW')), true,
    `expected the German status label, got "${deMsg}"`);
});

// A custom board-lane status is a name a human typed. It has no entry in the
// client's status table, and inventing one would be worse than printing it.
test('a custom board-lane status passes through as typed', async () => {
  const { notificationMessage } = await load();
  const msg = notificationMessage({ kind: 'status_changed', params: { title: 'T', status: 'Waiting on legal' } });
  assert.ok(msg.includes('Waiting on legal'), `expected the custom status verbatim, got "${msg}"`);
});

// The migration strategy in one test: a notification written before params
// existed has none to recover, so it must keep rendering the sentence the
// server stored rather than coming back blank or as a raw key.
test('a pre-params notification falls back to the stored message', async () => {
  const { notificationMessage } = await load('de');
  const msg = notificationMessage({
    kind: 'task_assigned', params: null, message: 'You were assigned to task: Legacy',
  });
  assert.strictEqual(msg, 'You were assigned to task: Legacy');
});

// An unknown kind means this client is older than the server. English text
// beats "notifications.messages.whatever".
test('an unknown kind falls back to the stored message', async () => {
  const { notificationMessage } = await load();
  const msg = notificationMessage({ kind: 'invented_later', params: {}, message: 'Something happened' });
  assert.strictEqual(msg, 'Something happened');
});

test('a missing notification renders nothing rather than throwing', async () => {
  const { notificationMessage } = await load();
  assert.strictEqual(notificationMessage(null), '');
  assert.strictEqual(notificationMessage({ kind: 'task_assigned', params: null }), '');
});

// A board lane's status is a free string, so it can collide with a name on
// Object.prototype. `STATUS_META.constructor` is truthy with an undefined
// `label`, which a naive lookup would render as "changed to undefined".
test('a board lane named after a prototype property still prints its name', async () => {
  const { notificationMessage } = await load();
  for (const status of ['constructor', 'toString', '__proto__']) {
    const msg = notificationMessage({ kind: 'status_changed', params: { title: 'T', status } });
    assert.ok(msg.includes(status), `lane "${status}" rendered as "${msg}"`);
    assert.ok(!msg.includes('undefined'), `lane "${status}" rendered as "${msg}"`);
  }
});
