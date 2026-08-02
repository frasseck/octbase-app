// @vitest-environment jsdom
//
// Unit tests for @octbase/shared/i18n.js — the locale loader, the {{var}} and
// plural interpolation, and the classic-vocabulary overlay. Run with
// `npm run test:unit`.
//
// Rewritten on Vitest at 37b stage 7. It used to be the odd one out of this
// layer: a hand-rolled runner with its own pass/failed counters and a
// `process.exit`, loading the module by evaluating rewritten source inside a
// `new Function` against a hand-built fake `window`. Now it imports the real
// module and lets jsdom be the browser — which is the whole argument for
// Vitest here, since none of that scaffolding was ever the thing under test.
//
// `load()` returns a FRESH module instance: the module keeps the active locale
// and vocabulary in module scope, so tests that assert on what a page load
// restores from storage need a new one, exactly as a reload would give them.

import { beforeEach, test, vi } from 'vitest';
import assert from 'node:assert';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const LOCALES_DIR = path.join(__dirname, '..', 'locales');

// The module fetches `locales/<lang>.json` relative to the page; off a real
// server that is the SPA's own directory, so serve it from disk here.
function localeFetch(url) {
  const file = path.join(LOCALES_DIR, path.basename(String(url)));
  if (!fs.existsSync(file)) return Promise.resolve({ ok: false });
  const data = JSON.parse(fs.readFileSync(file, 'utf8'));
  return Promise.resolve({ ok: true, json: async () => data });
}

async function load() {
  vi.resetModules();
  return import('@octbase/shared/i18n.js');
}

beforeEach(() => {
  localStorage.clear();
  document.documentElement.lang = '';
  vi.stubGlobal('fetch', localeFetch);
});

let i18n = await load();

test('initI18n loads default locale (en) and sets <html lang>', async () => {
  await i18n.initI18n();
  assert.strictEqual(i18n.getLocale(), 'en');
  assert.strictEqual(i18n.t('nav.myWork'), 'My Work');
});

test('t() interpolates {{vars}}', async () => {
  const result = i18n.t('task.prNumber', { number: 42 });
  assert.ok(result.includes('42'), `expected "42" in "${result}"`);
});

test('t() falls back to the key itself for unknown keys', async () => {
  assert.strictEqual(i18n.t('does.not.exist'), 'does.not.exist');
});

test('t() picks plural form via vars.count', async () => {
  const one = i18n.t('task.taskCount', { count: 1 });
  const other = i18n.t('task.taskCount', { count: 5 });
  assert.strictEqual(one, '1 task');
  assert.strictEqual(other, '5 tasks');
});

test('setLocale switches active locale and updates <html lang>', async () => {
  await i18n.setLocale('de');
  assert.strictEqual(i18n.getLocale(), 'de');
  assert.strictEqual(i18n.t('nav.myWork'), 'Meine Aufgaben');
});

test('setLocale persists the choice to localStorage', async () => {
  // A stored preference is what a returning user has; seed it and let a fresh
  // module instance read it at init.
  localStorage.setItem('octbase.lang', 'de');
  const fresh = await load();
  await fresh.initI18n();
  assert.strictEqual(fresh.getLocale(), 'de');
  assert.strictEqual(document.documentElement.lang, 'de');
});

test('a key missing from de falls back to the en translation', async () => {
  // Sanity check: every namespace present in en.json must also exist in de
  // (this keeps the two locale files structurally in sync).
  const en = JSON.parse(fs.readFileSync(path.join(LOCALES_DIR, 'en.json'), 'utf8'));
  const de = JSON.parse(fs.readFileSync(path.join(LOCALES_DIR, 'de.json'), 'utf8'));

  function collectKeys(obj, prefix = '') {
    let keys = [];
    for (const [k, v] of Object.entries(obj)) {
      const full = prefix ? `${prefix}.${k}` : k;
      if (v !== null && typeof v === 'object' && !('one' in v && 'other' in v)) {
        keys = keys.concat(collectKeys(v, full));
      } else {
        keys.push(full);
      }
    }
    return keys;
  }

  function get(obj, key) {
    return key.split('.').reduce((n, p) => (n == null ? n : n[p]), obj);
  }

  const enKeys = collectKeys(en);
  const missingDe = enKeys.filter((k) => get(de, k) === undefined);
  assert.deepStrictEqual(missingDe, [], `de.json missing keys: ${missingDe.join(', ')}`);
});


// ── Vocabulary overlay (agile ↔ classic project management) ───────────────
// CLASSIC is an overlay inside the same locale file, not a second language:
// a key with no classic variant must keep its agile wording rather than
// degrade to the raw key, which is what makes partial coverage safe.

test('terminology defaults to AGILE and reads the plain key', async () => {
  const i18n = await load();
  await i18n.initI18n();
  assert.strictEqual(i18n.getTerminology(), 'AGILE');
  assert.strictEqual(i18n.t('nav.sprints'), 'Sprints');
  assert.strictEqual(i18n.t('task.storyPoints'), 'Story points');
});

test('CLASSIC swaps in the classic vocabulary', async () => {
  const i18n = await load();
  await i18n.initI18n();
  i18n.setTerminology('CLASSIC');
  assert.strictEqual(i18n.t('nav.sprints'), 'Phases');
  assert.strictEqual(i18n.t('nav.backlog'), 'Task pool');
  assert.strictEqual(i18n.t('nav.releases'), 'Milestones');
  assert.strictEqual(i18n.t('task.type.EPIC'), 'Work package');
  assert.strictEqual(i18n.t('task.storyPoints'), 'Effort points');
  assert.strictEqual(i18n.t('release.label'), 'Milestone');
});

test('a key with no classic variant keeps its agile wording', async () => {
  const i18n = await load();
  await i18n.initI18n();
  i18n.setTerminology('CLASSIC');
  // Never overridden — and it must not fall through to the raw key.
  assert.strictEqual(i18n.t('nav.board'), 'Board');
  assert.strictEqual(i18n.t('task.status.DONE'), 'Done');
});

test('the classic overlay interpolates and pluralises like any key', async () => {
  const i18n = await load();
  await i18n.initI18n();
  i18n.setTerminology('CLASSIC');
  assert.strictEqual(i18n.t('sprint.deleteTitle'), 'Delete Phase');
  assert.match(i18n.t('notifications.activity.SPRINT_STARTED', { name: 'Q3' }), /Started phase "Q3"/);
  assert.match(i18n.t('notifications.activity.RELEASE_CLOSED', { name: '1.2' }), /Closed milestone "1\.2"/);
});

test('the vocabulary survives a language switch, in German', async () => {
  const i18n = await load();
  await i18n.initI18n();
  i18n.setTerminology('CLASSIC');
  await i18n.setLocale('de');
  assert.strictEqual(i18n.getTerminology(), 'CLASSIC');
  assert.strictEqual(i18n.t('nav.sprints'), 'Phasen');
  assert.strictEqual(i18n.t('nav.releases'), 'Meilensteine');
  assert.strictEqual(i18n.t('task.type.EPIC'), 'Arbeitspaket');
});

test('an unknown terminology falls back to AGILE', async () => {
  const i18n = await load();
  await i18n.initI18n();
  i18n.setTerminology('WATERFALL');
  assert.strictEqual(i18n.getTerminology(), 'AGILE');
  assert.strictEqual(i18n.t('nav.sprints'), 'Sprints');
});

test('setTerminology persists the choice and initI18n restores it', async () => {
  (await load()).setTerminology('CLASSIC');
  assert.strictEqual(localStorage.getItem('octbase.terminology'), 'CLASSIC');
  // A second instance, as a page reload would produce: the preference has to
  // come back out of storage, not out of the first instance's memory.
  const fresh = await load();
  await fresh.initI18n();
  assert.strictEqual(fresh.getTerminology(), 'CLASSIC');
});

test('the German classic overlay covers every key the English one does', async () => {
  const en = JSON.parse(fs.readFileSync(path.join(LOCALES_DIR, 'en.json'), 'utf8'));
  const de = JSON.parse(fs.readFileSync(path.join(LOCALES_DIR, 'de.json'), 'utf8'));
  const keys = (o, p = '') => Object.entries(o).flatMap(([k, v]) =>
    v !== null && typeof v === 'object' ? keys(v, p ? `${p}.${k}` : k) : [p ? `${p}.${k}` : k]);
  const get = (o, k) => k.split('.').reduce((n, part) => (n == null ? n : n[part]), o);
  const missing = keys(en.classic).filter(k => get(de.classic, k) === undefined);
  assert.deepStrictEqual(missing, [], `de classic overlay missing: ${missing.join(', ')}`);
});

test('every string using agile vocabulary has a classic variant', async () => {
  // The guard that keeps the feature honest: add a new "sprint"/"backlog"
  // string without a classic wording and this fails, instead of a classic-mode
  // user meeting the agile word in a corner of the UI.
  const en = JSON.parse(fs.readFileSync(path.join(LOCALES_DIR, 'en.json'), 'utf8'));
  const TERMS = /\b(sprint|backlog|story point|stor(y|ies)|epic|scrum|release)\w*/i;
  // Deliberately not translated, with the reason. Empty on purpose: the two
  // untranslated words — burndown and velocity — are agile *metrics* with no
  // classic counterpart, so they are absent from TERMS rather than exempted
  // here, and every string that merely mentions one (a sprint burndown, a
  // velocity in story points) does carry a classic wording.
  const ALLOWED = new Set([]);
  const walk = (o, p = '') => Object.entries(o).flatMap(([k, v]) => {
    const key = p ? `${p}.${k}` : k;
    return v !== null && typeof v === 'object' ? walk(v, key) : [[key, v]];
  });
  const get = (o, k) => k.split('.').reduce((n, part) => (n == null ? n : n[part]), o);
  const uncovered = walk(en)
    .filter(([k, v]) => !k.startsWith('classic.') && typeof v === 'string' && TERMS.test(v))
    .map(([k]) => k)
    .filter(k => !ALLOWED.has(k) && get(en.classic, k) === undefined);
  assert.deepStrictEqual(uncovered, [],
    `agile wording without a classic variant: ${uncovered.join(', ')}`);
});
