// @vitest-environment jsdom
//
// Notification message coverage against the MOBILE locale files.
// Run with `npm run test:unit -- notifications.test.js`.
//
// The renderer itself (@octbase/shared/notifications.js — fallbacks, the status
// label, unknown kinds) is tested once, in octbase-frontend/js/notifications.test.js.
// What cannot be tested once is the vocabulary: mobile keeps its own locales on
// purpose, so a key added to octbase-frontend/locales does NOT reach the phone,
// and `t()` answers a missing key with the key itself — no throw, and a warning
// in a console nobody is watching. The phone's inbox had no notification
// vocabulary at all before OCT-323, which is exactly how it would drift back.
//
// The key is built at runtime (`notifications.messages.` + kind), so
// scripts/check-i18n-keys.mjs cannot see it. This is that guard.

import { beforeEach, test, vi } from 'vitest';
import assert from 'node:assert';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const LOCALES_DIR = path.join(__dirname, '..', 'locales');
const NOTIFICATIONS_GO = path.join(
  __dirname, '..', '..', 'octbase-api', 'internal', 'notifications', 'domain.go');

function localeFetch(url) {
  const file = path.join(LOCALES_DIR, path.basename(String(url)));
  if (!fs.existsSync(file)) return Promise.resolve({ ok: false });
  return Promise.resolve({ ok: true, json: async () => JSON.parse(fs.readFileSync(file, 'utf8')) });
}

async function load(lang) {
  vi.resetModules();
  const i18n = await import('@octbase/shared/i18n.js');
  const notifications = await import('@octbase/shared/notifications.js');
  await i18n.initI18n();
  if (lang !== 'en') await i18n.setLocale(lang);
  return notifications;
}

// The kinds come from the Go constant block, so adding one on the backend
// without a phone translation fails here rather than shipping English.
function backendKinds() {
  const src = fs.readFileSync(NOTIFICATIONS_GO, 'utf8');
  return [...src.matchAll(/^\tKind\w+\s*=\s*"([a-z_]+)"/gm)].map((m) => m[1]);
}

// task_changed is email-only and writes no in-app row (see the desktop file).
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

test('the phone has a translation for every in-app kind, in every language', async () => {
  const kinds = backendKinds().filter((k) => !EMAIL_ONLY_KINDS.includes(k));
  assert.ok(kinds.length >= 4, `expected the Go source to yield the kinds, got ${kinds.length}`);

  for (const lang of ['en', 'de']) {
    const { notificationMessage } = await load(lang);
    for (const kind of kinds) {
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

// The phone needs the status vocabulary too, not just the message templates:
// the label comes from `task.status.<enum>` via STATUS_META.
test('the phone renders the status as a localized label', async () => {
  const { notificationMessage } = await load('de');
  const msg = notificationMessage({ kind: 'status_changed', params: { title: 'T', status: 'IN_REVIEW' } });
  assert.ok(!msg.includes('IN_REVIEW'), `the enum leaked on mobile: ${msg}`);
  assert.deepStrictEqual(warnings, [], 'i18n warnings while rendering a status change');
});
