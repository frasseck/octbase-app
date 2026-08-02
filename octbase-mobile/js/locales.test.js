// Unit tests for the mobile locale files:
//   npm run test:unit -- locales.test.js
//
// Why this file exists. Mobile keeps its OWN locales (deliberately — the style
// guide lets the phone use a shorter label where the desktop wording does not
// fit a cell), so a string added to octbase-frontend/locales does NOT reach the
// phone. And `t()` answers a missing key with the key itself, which the app then
// often papers over with `t('x') !== 'x' ? t('x') : 'English fallback'` — so a
// wrong key namespace does not throw, does not warn where anyone looks, and
// simply ships English to a German user.
//
// That is not hypothetical: the confirm sheet below first shipped reading
// `common.cancel`, which does not exist here (it is `form.cancel`), and the
// German dialog rendered "Cancel" under "Trotzdem erledigen". These assertions
// pin the keys that dialog actually reads.

import { test } from 'vitest';
import assert from 'node:assert';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const LOCALES = path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'locales');
const load = (lang) => JSON.parse(fs.readFileSync(path.join(LOCALES, `${lang}.json`), 'utf8'));
const lookup = (obj, key) => key.split('.').reduce((node, part) => (node == null ? undefined : node[part]), obj);

// Every key the completion-warning sheet reads (js/app.js,
// confirmCompletionOverOpenDescendants + confirmSheet).
const COMPLETION_KEYS = [
  'task.openDescendantsTitle',
  'task.openDescendantsBody',
  'task.openDescendantsList',
  'task.openDescendantsMore',
  'task.completeAnyway',
  'form.cancel',
];

for (const lang of ['en', 'de']) {
  test(`${lang}: every key the completion warning reads exists`, () => {
    const locale = load(lang);
    for (const key of COMPLETION_KEYS) {
      assert.notStrictEqual(lookup(locale, key), undefined, `${lang}.json is missing ${key}`);
    }
  });
}

test('the count-bearing body carries both plural forms in both locales', () => {
  // t() picks node[count === 1 ? 'one' : 'other']; a missing form warns and
  // renders the raw key, which is how a plural bug reaches a user.
  for (const lang of ['en', 'de']) {
    const body = lookup(load(lang), 'task.openDescendantsBody');
    assert.strictEqual(typeof body, 'object', `${lang}: openDescendantsBody must be a plural object`);
    for (const form of ['one', 'other']) {
      assert.ok(body[form], `${lang}.json openDescendantsBody is missing the "${form}" form`);
      assert.ok(body[form].includes('{{count}}'), `${lang}/${form} should interpolate {{count}}`);
    }
  }
});

test('the German strings are translated, not copied from English', () => {
  // The bug this catches is silent: a key present in de.json but still holding
  // the English sentence reads as "translated" to every automated check.
  const en = load('en'), de = load('de');
  for (const key of COMPLETION_KEYS) {
    const a = lookup(en, key), b = lookup(de, key);
    if (typeof a === 'object') {
      for (const form of ['one', 'other']) {
        assert.notStrictEqual(b[form], a[form], `de ${key}.${form} is still the English string`);
      }
    } else {
      assert.notStrictEqual(b, a, `de ${key} is still the English string`);
    }
  }
});

test('the names placeholder survives in the list and overflow strings', () => {
  for (const lang of ['en', 'de']) {
    const locale = load(lang);
    assert.ok(lookup(locale, 'task.openDescendantsList').includes('{{names}}'), `${lang}: list needs {{names}}`);
    const more = lookup(locale, 'task.openDescendantsMore');
    assert.ok(more.includes('{{names}}') && more.includes('{{count}}'), `${lang}: overflow needs both vars`);
  }
});
