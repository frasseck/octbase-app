// Unit tests for the create-task dialog's estimate field in views-crud.js.
//   npm run test:unit -- views-crud.test.js
//
// POST /projects/{id}/tasks accepts storyPoints/estimateHours, but the dialog
// used to drop them on the floor: a task could only be estimated in a second
// step, from the panel. The field added here has to obey exactly the two gates
// the API enforces, or the dialog offers a box whose value comes back 422:
//   - the project must estimate at all      (else ESTIMATION_UNIT_INACTIVE)
//   - the chosen type must be an estimable leaf (else ESTIMATION_NOT_ALLOWED_FOR_TYPE)
//
// Both gates live in @octbase/shared/meta.js, so this file loads the real one and the real
// locale files rather than stubs: a renamed helper or a mistyped translation
// key fails here instead of shipping an empty label to a user.

import { test } from 'vitest';
import assert from 'node:assert';
import fs from 'node:fs';
import path from 'node:path';
import vm from 'node:vm';
import { fileURLToPath } from 'node:url';
import { makeWindow, readAsClassicScript } from './testutil.js';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const LOCALES_DIR = path.join(__dirname, '..', 'locales');

// load builds a window holding the real @octbase/shared i18n + meta and the parts of
// framework.js that views-crud.js reaches for, with a DOM stub that records
// what each el() target had written into it. Returns helpers to drive the
// dialog's type-change path and read back the resulting HTML.
async function load(project, lang = 'en') {
  const warnings = [];
  // The fake DOM is a flat map of id -> node; innerHTML written by the code
  // under test stays readable, and `value` is what a user would have typed.
  const nodes = {
    '#task-parent-group': { innerHTML: '' },
    '#task-estimate-group': { innerHTML: '' },
  };
  const win = makeWindow({
    fetch: async (url) => {
      const file = path.join(LOCALES_DIR, path.basename(url));
      if (!fs.existsSync(file)) return { ok: false };
      return { ok: true, json: async () => JSON.parse(fs.readFileSync(file, 'utf8')) };
    },
    console: { ...console, warn: (m) => warnings.push(String(m)) },
    // From framework.js — the dialog only needs escaping and node lookup here.
    esc: (s) => String(s).replace(/[&<>"']/g, (c) => (
      { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c])),
    el: (sel) => nodes[sel] || null,
    // views-crud.js registers views at load time.
    Views: { register() {} },
    S: { project, priorities: [], releases: [], sprints: [] },
  });
  vm.createContext(win);
  vm.runInContext(readAsClassicScript('../../octbase-shared/i18n.js'), win);
  await win.i18n.setLocale('en');
  if (lang !== 'en') await win.i18n.setLocale(lang);
  vm.runInContext(readAsClassicScript('../../octbase-shared/meta.js'), win);
  // Every file here is an ES module: views-crud.js since 37b stage 2, the two
  // @octbase/shared modules since stage 3. The harness rewrites all of them
  // back to the fake-global model, so they still share one window.
  vm.runInContext(readAsClassicScript('views-crud.js'), win);
  return {
    win,
    warnings,
    nodes,
    // typeChanged drives the real handler, optionally with a value already
    // typed into the estimate box.
    typeChanged(taskType, typed) {
      if (typed !== undefined) nodes['#task-estimate-create'] = { value: typed };
      win.createTaskTypeChanged(taskType);
      return nodes['#task-estimate-group'].innerHTML;
    },
  };
}

const POINTS = { id: 'p1', estimationUnit: 'POINTS' };
const HOURS  = { id: 'p1', estimationUnit: 'HOURS' };
const NONE   = { id: 'p1', estimationUnit: 'NONE' };

// ── The two gates ───────────────────────────────────────────────────────────

test('no estimate field when the project does not estimate', async () => {
  const { typeChanged } = await load(NONE);
  for (const type of ['STORY', 'TASK', 'SUBTASK']) {
    assert.equal(typeChanged(type), '', `${type} should have no estimate field`);
  }
});

test('a project with no estimationUnit at all reads as NONE', async () => {
  const { typeChanged } = await load({ id: 'p1' });
  assert.equal(typeChanged('TASK'), '');
});

test('no estimate field for a container type, in either unit', async () => {
  for (const project of [POINTS, HOURS]) {
    const { typeChanged } = await load(project);
    for (const type of ['EPIC', 'INITIATIVE', 'THEME']) {
      assert.equal(typeChanged(type), '',
        `${type} is a container and must not be estimable in ${project.estimationUnit}`);
    }
  }
});

test('estimate field appears for every estimable leaf type', async () => {
  for (const project of [POINTS, HOURS]) {
    const { typeChanged } = await load(project);
    for (const type of ['STORY', 'TASK', 'SUBTASK']) {
      assert.match(typeChanged(type), /id="task-estimate-create"/,
        `${type} should be estimable in ${project.estimationUnit}`);
    }
  }
});

// ── The input matches the unit's server-side range ──────────────────────────

test('points offer a whole-number 0-100 box', async () => {
  const { typeChanged } = await load(POINTS);
  const html = typeChanged('TASK');
  assert.match(html, /min="0"/);
  assert.match(html, /max="100"/);
  assert.match(html, /step="1"/);
});

test('hours offer a fractional 0-1000 box', async () => {
  const { typeChanged } = await load(HOURS);
  const html = typeChanged('TASK');
  assert.match(html, /min="0"/);
  assert.match(html, /max="1000"/);
  assert.match(html, /step="0.25"/);
});

// ── Switching type keeps a typed estimate, unless it becomes illegal ────────

test('a typed estimate survives a move between two leaf types', async () => {
  const { typeChanged } = await load(POINTS);
  typeChanged('TASK');
  assert.match(typeChanged('STORY', '8'), /value="8"/,
    'switching TASK -> STORY should keep the 8 the user typed');
});

test('switching to a container type takes the estimate away', async () => {
  const { typeChanged } = await load(POINTS);
  typeChanged('TASK', '8');
  assert.equal(typeChanged('EPIC'), '', 'an epic cannot carry the estimate forward');
});

test('a typed zero is preserved, not folded into empty', async () => {
  // 0 is a deliberate estimate of no effort and must survive the re-render as
  // a value, not collapse to the unestimated (empty) box.
  const { typeChanged } = await load(POINTS);
  assert.match(typeChanged('STORY', '0'), /value="0"/);
});

// ── Labels come from the shipped locales, in both languages ────────────────

test('the label is the unit label, in English and German, with no missing keys', async () => {
  const cases = [
    [POINTS, 'en'], [HOURS, 'en'],
    [POINTS, 'de'], [HOURS, 'de'],
  ];
  for (const [project, lang] of cases) {
    const { typeChanged, warnings, win } = await load(project, lang);
    const html = typeChanged('TASK');
    const key = project.estimationUnit === 'HOURS' ? 'task.estimateHours' : 'task.storyPoints';
    const label = win.t(key);
    assert.ok(label && label !== key, `${key} missing in ${lang}`);
    assert.ok(html.includes(label), `${lang}/${project.estimationUnit} label should be ${label}`);
    assert.ok(html.includes(win.t('task.estimateNone')), 'placeholder should be the unestimated hint');
    assert.deepEqual(warnings.filter(w => /Missing translation key/.test(w)), [],
      `no missing keys in ${lang}`);
  }
});
