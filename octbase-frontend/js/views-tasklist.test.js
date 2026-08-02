// Unit tests for the Task view's bulk status action. Plain Node, no build:
//   npm run test:unit -- views-tasklist.test.js
//
// Bulk "set status: Done" is the third door that completes a task (the others
// are the panel's status control and a drop into the Done lane). A warning that
// only appears at two of the three is a warning users learn to route around, so
// this file exists to pin that the bulk path asks too — and asks ONCE, for the
// whole selection, rather than per task.

import { test } from 'vitest';
import assert from 'node:assert';
import { loadModule } from './testutil.js';

function bulkFixture({ confirm = true } = {}) {
  const calls = { asked: [], applied: [], barRepaints: 0 };
  const win = loadModule('views-tasklist.js', {
    globals: {
      Views: { register() {} },
      t: (k) => k,
      esc: (s) => String(s),
      icon: () => '',
      STATUS_META: {},
      STATUSES: ['PLANNED', 'DONE'],
      AppPerms: { can: () => true },
      resolveStatusBoard: () => null,
      updateBulkBar: () => { calls.barRepaints++; },
      applyBulkAction: async (action, value) => { calls.applied.push([action, value]); },
      confirmCompletionOverOpenDescendants: async (ids) => {
        calls.asked.push(Array.from(ids));
        return confirm;
      },
      S: { selectedTasks: new Set(['t1', 't2']), bulkInFlight: false, project: { id: 'p1' } },
    },
  });
  return { win, calls };
}

test('setting a selection to Done asks once, for the whole selection', async () => {
  const { win, calls } = bulkFixture();
  await win.bulkSetStatus('DONE');

  assert.strictEqual(calls.asked.length, 1, 'the bulk door asked per task instead of once');
  assert.deepStrictEqual(calls.asked[0].sort(), ['t1', 't2']);
  assert.deepStrictEqual(calls.applied, [['set_status', 'DONE']]);
});

test('declining leaves the selection untouched and resets the status select', async () => {
  const { win, calls } = bulkFixture({ confirm: false });
  await win.bulkSetStatus('DONE');

  assert.deepStrictEqual(calls.applied, [], 'a declined bulk completion still wrote');
  // Without the repaint the bar's select keeps reading "Done" over a selection
  // that was never completed.
  assert.strictEqual(calls.barRepaints, 1, 'the bulk bar was not repainted');
});

test('any other bulk status goes through without a warning', async () => {
  const { win, calls } = bulkFixture();
  await win.bulkSetStatus('IN_PROGRESS');

  assert.deepStrictEqual(calls.asked, []);
  assert.deepStrictEqual(calls.applied, [['set_status', 'IN_PROGRESS']]);
});
