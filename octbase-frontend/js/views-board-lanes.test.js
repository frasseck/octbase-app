// Unit tests for the board's lane paging — the per-project cap on how many
// cards a lane draws at once, with the rest loaded on scroll (or by the
// load-more button). Plain Node, no build:
//   npm run test:unit -- views-board-lanes.test.js
//
// The cap is a rendering decision made from data that is entirely in memory, so
// all of it is pure: laneSlice(colId, tasks) → { shown, hidden, full }. The
// invariant every test below is really checking is that `full` stays the honest
// size of the lane. The count badge renders from it, so if capping ever leaked
// into that number, a reader would be told cards had vanished.

import { test } from 'vitest';
import assert from 'node:assert';
import { loadModule } from './testutil.js';

// views-board.js is loaded through the vm harness, which strips its imports —
// so the two it needs from @octbase/shared/meta.js are injected as globals,
// along with the S state object the cap reads the project's setting from.
// Views is stubbed because every view module registers itself at load time.
function fresh(boardLaneLimit = 20, query = '') {
  const S = { project: { boardLaneLimit }, filters: { q: query } };
  return loadModule('views-board.js', {
    globals: {
      Views: { register() {} },
      S,
      DEFAULT_BOARD_LANE_LIMIT: 20,
      boardLaneLimit: (project) => {
        const n = project && project.boardLaneLimit;
        return Number.isInteger(n) && n >= 0 ? n : 20;
      },
      // The real escaper, so the escaping assertion below tests the call site
      // rather than a stub that happens to be safe.
      esc: (s) => String(s ?? '')
        .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;').replace(/'/g, '&#39;'),
      t: (key, vars) => `${key}${vars ? ':' + JSON.stringify(vars) : ''}`,
      // No board is mounted in this fake window, so refreshBoardCards finds no
      // wrapper and returns without painting — the pure paging state is what
      // these tests read, and it is updated before the repaint either way.
      el: () => null,
    },
  });
}

// tasks(n) builds a lane of n identifiable cards.
function tasks(n) {
  return Array.from({ length: n }, (_, i) => ({ id: `t${i}`, title: `Task ${i}` }));
}

test('a lane shorter than the limit is drawn whole, with nothing held back', () => {
  const { laneSlice } = fresh(20);
  const page = laneSlice('col-a', tasks(7));
  assert.strictEqual(page.shown.length, 7);
  assert.strictEqual(page.hidden, 0);
  assert.strictEqual(page.full, 7);
});

test('a lane exactly at the limit is drawn whole — the cap is not off-by-one', () => {
  const { laneSlice } = fresh(20);
  const page = laneSlice('col-a', tasks(20));
  assert.strictEqual(page.shown.length, 20);
  assert.strictEqual(page.hidden, 0);
});

test('a long lane draws one page and reports the rest as held back', () => {
  const { laneSlice } = fresh(20);
  const page = laneSlice('col-a', tasks(800));
  assert.strictEqual(page.shown.length, 20);
  assert.strictEqual(page.hidden, 780);
  // The full count is what the lane's badge shows: capping must never make the
  // board claim the lane is smaller than it is.
  assert.strictEqual(page.full, 800);
  // The page is the head of the lane, in the lane's own order (pinned first,
  // then board rank) — not an arbitrary subset.
  assert.strictEqual(page.shown[0].id, 't0');
  assert.strictEqual(page.shown[19].id, 't19');
});

test('a limit of 0 means unlimited and draws every card', () => {
  const { laneSlice } = fresh(0);
  const page = laneSlice('col-a', tasks(500));
  assert.strictEqual(page.shown.length, 500);
  assert.strictEqual(page.hidden, 0);
});

test('a project with no setting falls back to the default of 20', () => {
  // An API response predating the field — the board must still cap rather than
  // read the missing value as 0/unlimited.
  const { laneSlice } = fresh(undefined);
  const page = laneSlice('col-a', tasks(50));
  assert.strictEqual(page.shown.length, 20);
  assert.strictEqual(page.hidden, 30);
});

test('loading more grows that lane by another page', () => {
  const win = fresh(20);
  const { laneSlice, loadMoreLane } = win;
  // refreshBoardCards repaints the DOM; with no board mounted in this fake
  // window it is a no-op, which is exactly what the pure test wants.
  loadMoreLane('col-a');
  let page = laneSlice('col-a', tasks(100));
  assert.strictEqual(page.shown.length, 40);
  assert.strictEqual(page.hidden, 60);
  loadMoreLane('col-a');
  page = laneSlice('col-a', tasks(100));
  assert.strictEqual(page.shown.length, 60);
});

test('expanding past the end of a lane clamps instead of reporting negative hidden', () => {
  const win = fresh(20);
  const { laneSlice, loadMoreLane } = win;
  for (let i = 0; i < 10; i++) loadMoreLane('col-a');   // asks for 220 in a lane of 25
  const page = laneSlice('col-a', tasks(25));
  assert.strictEqual(page.shown.length, 25);
  assert.strictEqual(page.hidden, 0);
});

test('lanes expand independently — one lane opening does not open the others', () => {
  const win = fresh(20);
  const { laneSlice, loadMoreLane } = win;
  loadMoreLane('done');
  assert.strictEqual(laneSlice('done', tasks(100)).shown.length, 40);
  assert.strictEqual(laneSlice('planned', tasks(100)).shown.length, 20);
});

test('resetting lane paging collapses every lane back to one page', () => {
  const win = fresh(20);
  const { laneSlice, loadMoreLane, resetLanePaging } = win;
  loadMoreLane('done');
  assert.strictEqual(laneSlice('done', tasks(100)).shown.length, 40);
  resetLanePaging();
  assert.strictEqual(laneSlice('done', tasks(100)).shown.length, 20);
});

test('the load-more control is rendered only when cards are actually held back', () => {
  const { laneMoreHtml } = fresh(20);
  assert.strictEqual(laneMoreHtml('col-a', 0), '');
  const html = laneMoreHtml('col-a', 5);
  assert.ok(html.includes('data-act="loadMoreLane"'), 'load-more must be a delegated action');
  assert.ok(html.includes('data-a0="col-a"'), 'load-more must carry its column id');
  // A real <button>, not a bare scroll sentinel: a keyboard user has to be able
  // to reach the rest of the lane without a pointer (WCAG 2.1.1).
  assert.ok(/^\s*<button/.test(html), 'load-more must be a focusable button');
});

test('the column id is escaped into the load-more control', () => {
  const { laneMoreHtml } = fresh(20);
  // Column ids are server-issued UUIDs, so this is defence in depth rather than
  // a live injection path — but the control is built by string concatenation
  // like every other card, and the guard script holds it to the same rule.
  const html = laneMoreHtml('a"><img src=x onerror=alert(1)>', 3);
  assert.ok(!html.includes('<img'), `unescaped column id reached the markup: ${html}`);
});
