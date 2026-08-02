// Unit tests for the board's drag-drop slot maths. Plain Node, no build:
//   npm run test:unit -- views-board.test.js
//
// dropSlot replaced a linear scan over freshly-read getBoundingClientRects with a
// binary search over midpoints measured once per drag (see the "Drag geometry"
// block in views-board.js). The two must agree for every pointer position, or a
// card lands in the wrong place — so the reference implementation below IS the
// old scan, and the tests hold the new one against it.

import { test } from 'vitest';
import assert from 'node:assert';
import { loadModule } from './testutil.js';

// views-board.js registers its views at load time; Views is the only global its
// top-level code touches.
function fresh() {
  return loadModule('views-board.js', {
    globals: {
      Views: { register() {} },
      // Lane paging reads the project's card cap through shared/meta.js, and
      // the harness strips imports — so the reader is injected the way this
      // file already injects STATUS_META below. 0 keeps every lane uncapped,
      // which is what the render assertions here are written against; the cap
      // itself is covered in views-board-lanes.test.js.
      boardLaneLimit: () => 0,
      DEFAULT_BOARD_LANE_LIMIT: 20,
    },
  });
}

// linearSlot is the pre-change implementation: the first card whose midpoint the
// pointer is above, else the end of the lane.
function linearSlot(mids, y) {
  for (let i = 0; i < mids.length; i++) {
    if (y < mids[i]) return i;
  }
  return mids.length;
}

test('dropSlot matches the linear scan for every position around each card', () => {
  const { dropSlot } = fresh();
  // A lane of 40 cards, 50px tall with a 10px gap: midpoints 25, 85, 145, …
  const mids = Array.from({ length: 40 }, (_, i) => 25 + i * 60);
  // Probe every boundary: well above, just above, exactly on and just below each
  // midpoint, plus the extremes far outside the lane.
  const probes = [-1000, -1, 0];
  mids.forEach((m) => probes.push(m - 1, m, m + 1, m + 30));
  probes.push(1e6);
  probes.forEach((y) => {
    assert.strictEqual(dropSlot(mids, y), linearSlot(mids, y),
      `dropSlot disagreed with the linear scan at y=${y}`);
  });
});

test('dropSlot puts a pointer below the last card at the end of the lane', () => {
  const { dropSlot } = fresh();
  const mids = [25, 85, 145];
  // Dragging over the empty area under the last card appends.
  assert.strictEqual(dropSlot(mids, 200), 3);
  assert.strictEqual(dropSlot(mids, 146), 3);
});

test('dropSlot puts a pointer above the first card at the top of the lane', () => {
  const { dropSlot } = fresh();
  assert.strictEqual(dropSlot([25, 85, 145], 0), 0);
  assert.strictEqual(dropSlot([25, 85, 145], 24), 0);
});

test('dropSlot returns 0 for an empty lane', () => {
  const { dropSlot } = fresh();
  // An empty lane (or one holding only the card being dragged) has no midpoints;
  // every pointer position is the sole insertion point.
  assert.strictEqual(dropSlot([], 0), 0);
  assert.strictEqual(dropSlot([], 5000), 0);
});

test('dropSlot lands between two adjacent cards on the midpoint boundary', () => {
  const { dropSlot } = fresh();
  const mids = [100, 200, 300];
  // Above card 1's midpoint -> before it; past it -> between 1 and 2, and so on.
  assert.strictEqual(dropSlot(mids, 99), 0);
  assert.strictEqual(dropSlot(mids, 100), 1);
  assert.strictEqual(dropSlot(mids, 199), 1);
  assert.strictEqual(dropSlot(mids, 200), 2);
  assert.strictEqual(dropSlot(mids, 300), 3);
});

test('dropSlot handles a single-card lane', () => {
  const { dropSlot } = fresh();
  assert.strictEqual(dropSlot([50], 49), 0);
  assert.strictEqual(dropSlot([50], 50), 1);
});

// ── The pending paint ─────────────────────────────────────────────────────────
// The board draws its lanes from the board object a round trip before the cards
// (docs/architecture.md §5). That window must not put anything card-shaped in a
// lane: placeholder cards were tried and read as real, empty tasks. The lane
// bodies stay empty and the region is marked pending instead — the count badge
// is blanked, and renderBoard sets aria-busy on the wrapper.
function boardFixture() {
  const win = fresh();
  Object.assign(win, {
    S: { showBacklog: false },
    esc: (s) => String(s),
    icon: () => '<svg></svg>',
    t: (k) => k,
  });
  return win;
}

const PENDING_BOARD = {
  id: 'b1', minColumns: 1, maxColumns: 10,
  columns: [{ id: 'c1', name: 'Planned', status: 'PLANNED' },
            { id: 'c2', name: 'In Progress', status: 'IN_PROGRESS' }],
};

test('a pending board renders its lanes with no cards in them', () => {
  const { boardColsInner } = boardFixture();
  const html = boardColsInner(PENDING_BOARD, null, true, false);
  // The lanes themselves are up — the point of painting before the cards.
  assert.match(html, /Planned/);
  assert.match(html, /In Progress/);
  // …but nothing card-shaped is in them, placeholder or otherwise.
  assert.ok(!html.includes('board-card'), 'pending lane rendered a card element');
  assert.ok(!html.includes('skeleton'), 'pending lane rendered a placeholder card');
  // Each lane body is empty.
  const bodies = html.match(/<div class="board-col-tasks" id="col-[^"]*">([\s\S]*?)<\/div>/g) || [];
  assert.strictEqual(bodies.length, 2);
  bodies.forEach((b) => assert.match(b, /">\s*<\/div>$/, `lane body not empty: ${b}`));
});

test('a pending board blanks the task count instead of claiming zero', () => {
  const { boardColsInner } = boardFixture();
  const html = boardColsInner(PENDING_BOARD, null, true, false);
  // "0" would be a claim about data still in flight; the placeholder badge holds
  // the box so the header does not shift when the real count lands.
  assert.strictEqual((html.match(/board-col-count-pending/g) || []).length, 2);
  assert.ok(!html.includes('task.taskCount'), 'pending header rendered a task count');
});

test('a pending backlog column is empty too, and claims nothing', () => {
  const win = boardFixture();
  win.S.showBacklog = true;
  const html = win.boardColsInner(PENDING_BOARD, null, true, false);
  assert.match(html, /board-col-backlog/);
  assert.ok(!html.includes('board-card'), 'pending backlog rendered a card element');
  // No "the backlog is empty" hint either — that is not known yet.
  assert.ok(!html.includes('col-empty-hint'), 'pending backlog claimed it was empty');
});

test('a loaded board with no tasks renders empty lanes, not pending ones', () => {
  const { boardColsInner } = boardFixture();
  const html = boardColsInner(PENDING_BOARD, { c1: [], c2: [] }, true, false);
  // Same empty lane bodies, but now the zero counts are real and stated.
  assert.ok(!html.includes('board-col-count-pending'), 'loaded board still blanked its counts');
  assert.strictEqual((html.match(/task\.taskCount/g) || []).length, 2);
});

// ── Lane name → status mapping ────────────────────────────────────────────────
// addLane/renameLane resolve a typed lane name to a built-in status; anything
// unresolved becomes a custom status carried by the lane. The mapping must be
// locale-independent (a template lane name in either language maps back) and
// must never resolve to ARCHIVED — the board hides archived cards, so an
// ARCHIVED lane could only swallow whatever is dropped into it.
function laneFixture() {
  const win = fresh();
  Object.assign(win, {
    STATUSES: ['PLANNED', 'IN_PROGRESS', 'IN_REVIEW', 'DONE', 'ARCHIVED'],
    STATUS_META: {
      PLANNED: { label: 'Planned' }, IN_PROGRESS: { label: 'In Progress' },
      IN_REVIEW: { label: 'In Review' }, DONE: { label: 'Done' },
      ARCHIVED: { label: 'Archived' },
    },
  });
  return win;
}

test('laneBuiltinFor maps localized labels and status codes, case-insensitively', () => {
  const { laneBuiltinFor } = laneFixture();
  assert.strictEqual(laneBuiltinFor('Planned').status, 'PLANNED');
  assert.strictEqual(laneBuiltinFor('planned').status, 'PLANNED');
  assert.strictEqual(laneBuiltinFor('IN_REVIEW').status, 'IN_REVIEW');
  assert.strictEqual(laneBuiltinFor('done').status, 'DONE');
});

test('laneBuiltinFor maps the template lane names in both locales', () => {
  const { laneBuiltinFor } = laneFixture();
  // The seeded lanes are named "To Do"/"Zu erledigen", not "Planned"/"Geplant";
  // re-creating one must restore built-in semantics, whatever the active locale.
  assert.strictEqual(laneBuiltinFor('To Do').status, 'PLANNED');
  assert.strictEqual(laneBuiltinFor('Zu erledigen').status, 'PLANNED');
  assert.strictEqual(laneBuiltinFor('In Arbeit').status, 'IN_PROGRESS');
  assert.strictEqual(laneBuiltinFor('In Prüfung').status, 'IN_REVIEW');
  assert.strictEqual(laneBuiltinFor('Erledigt').status, 'DONE');
});

test('laneBuiltinFor leaves genuinely custom names unmapped', () => {
  const { laneBuiltinFor } = laneFixture();
  assert.strictEqual(laneBuiltinFor('QA'), null);
  assert.strictEqual(laneBuiltinFor('Waiting on customer'), null);
});

test('laneBuiltinFor never resolves to ARCHIVED', () => {
  const { laneBuiltinFor } = laneFixture();
  assert.strictEqual(laneBuiltinFor('Archived'), null);
  assert.strictEqual(laneBuiltinFor('ARCHIVED'), null);
});

// ── Dropping a card onto the backlog column ───────────────────────────────────
// Every other drop repaints from the snapshot the write returned; this one used
// to re-render unconditionally, which refetched the project's whole task list
// and reset the board for a move the server had already described. The tests
// below hold it to the same contract as a lane drop: the caches move, and
// nothing re-renders unless the write actually failed.

// The sentinel data-drop-col value of the backlog column is file-private, so it
// is read back out of the rendered board rather than duplicated here — a renamed
// sentinel then fails in views-board.js, not silently in this file.
// A pending render (null card sets) is used because it draws the backlog column
// with no cards in it — the sentinel is on the column, not on a card.
function backlogSentinel(win) {
  const html = win.boardColsInner({ id: 'b1', minColumns: 1, maxColumns: 10, columns: [] }, null, true, false);
  return /data-drop-col="([^"]+)"/.exec(html)[1];
}

function dropFixture({ removeResult, removeError, onBoard = true } = {}) {
  const calls = { removed: [], rerendered: 0 };
  const card = onBoard
    ? { id: 't1', boardColumnId: 'c1', status: 'PLANNED', version: 3 }
    : { id: 't1', boardColumnId: null, status: 'PLANNED', version: 3 };
  const win = fresh();
  Object.assign(win, {
    // No DOM: refreshBoardCards bails on the missing wrapper, so what the drop
    // did is visible in the caches alone.
    el: () => null,
    toast: () => {},
    apiErrorMessage: (e) => String(e),
    esc: (s) => String(s), icon: () => '', t: (k) => k,
    api: {
      boards: {
        remove: (bid, tid) => {
          calls.removed.push([bid, tid]);
          return removeError ? Promise.reject(removeError) : Promise.resolve(removeResult);
        },
      },
    },
    S: {
      view: 'board',
      board: { id: 'b1', columns: [{ id: 'c1', name: 'To Do', status: 'PLANNED' }] },
      showBacklog: true,
      dragging: 't1',
      filters: {},
      boardTasks: onBoard ? [card] : [],
      boardBacklog: onBoard ? [] : [card],
      tasksByCol: onBoard ? { c1: [card] } : { c1: [] },
    },
  });
  // rerenderBoardView dispatches to these; reassigning after load replaces the
  // module's own declarations, which are plain properties of the fake window.
  win.renderBoard = async () => { calls.rerendered++; };
  win.renderSprintBoard = async () => { calls.rerendered++; };
  return { win, calls };
}

const DROP_EV = { preventDefault() {}, stopPropagation() {}, clientY: 0 };

test('dropping a lane card onto the backlog moves it in place, without re-rendering', async () => {
  const { win, calls } = dropFixture({
    removeResult: { id: 't1', boardColumnId: null, status: 'PLANNED', version: 4 },
  });
  await win.dropOnColumn(DROP_EV, backlogSentinel(win));

  assert.deepStrictEqual(calls.removed, [['b1', 't1']], 'remove-task was not called once');
  assert.strictEqual(calls.rerendered, 0, 'a successful drop still re-rendered the whole board');
  // The card crossed from the lanes into the backlog column, carrying the
  // version the write returned so the next edit does not 409.
  assert.deepStrictEqual(win.S.boardTasks, [], 'card stayed in the board cache');
  assert.strictEqual(win.S.boardBacklog.length, 1);
  assert.strictEqual(win.S.boardBacklog[0].id, 't1');
  assert.strictEqual(win.S.boardBacklog[0].boardColumnId, null);
  assert.strictEqual(win.S.boardBacklog[0].version, 4);
});

test('dropping a backlog card back onto the backlog writes nothing and repaints nothing', async () => {
  const { win, calls } = dropFixture({ onBoard: false });
  await win.dropOnColumn(DROP_EV, backlogSentinel(win));

  // No lane to leave — the drop is a no-op, and a no-op that re-renders is the
  // same defect in miniature.
  assert.deepStrictEqual(calls.removed, [], 'a no-op drop called remove-task');
  assert.strictEqual(calls.rerendered, 0, 'a no-op drop re-rendered the board');
  assert.strictEqual(win.S.boardBacklog.length, 1);
});

test('a refused backlog drop re-renders, so the card snaps back to its lane', async () => {
  const { win, calls } = dropFixture({ removeError: new Error('TASK_IMMUTABLE') });
  await win.dropOnColumn(DROP_EV, backlogSentinel(win));

  // The write failed, so the server's state is not known from the response —
  // this is the one path that still refetches.
  assert.deepStrictEqual(calls.removed, [['b1', 't1']]);
  assert.strictEqual(calls.rerendered, 1, 'a refused drop did not re-render');
  // Nothing was patched on the strength of a write that did not happen.
  assert.strictEqual(win.S.boardTasks.length, 1);
  assert.strictEqual(win.S.boardBacklog.length, 0);
});

// ── Dropping a card into the Done lane (OCT-300) ──────────────────────────────
// Crossing into a Done lane completes the task, so it is one of the three doors
// that warns when live work sits underneath. The card has not moved on screen at
// this point — the board repaints from the write's response — so declining is
// simply "write nothing", and that is what these tests hold it to.

function doneDropFixture({ confirm = true, status = 'PLANNED' } = {}) {
  const calls = { moved: [], asked: [], rerendered: 0 };
  const card = { id: 't1', boardColumnId: 'c1', status, version: 3 };
  const win = fresh();
  Object.assign(win, {
    el: () => null,
    toast: () => {},
    apiErrorMessage: (e) => String(e),
    esc: (s) => String(s), icon: () => '', t: (k) => k,
    confirmCompletionOverOpenDescendants: async (ids) => { calls.asked.push(ids); return confirm; },
    api: {
      boards: {
        move: (bid, body) => {
          calls.moved.push(body);
          return Promise.resolve({ ...card, boardColumnId: body.boardColumnId, status: 'DONE', version: 4 });
        },
      },
    },
    S: {
      view: 'board',
      board: { id: 'b1', columns: [
        { id: 'c1', name: 'To Do', status: 'PLANNED' },
        { id: 'c2', name: 'Done', status: 'DONE' },
      ] },
      dragging: 't1',
      filters: {},
      boardTasks: [card],
      boardBacklog: [],
      tasksByCol: { c1: [card], c2: [] },
    },
  });
  win.renderBoard = async () => { calls.rerendered++; };
  win.renderSprintBoard = async () => { calls.rerendered++; };
  return { win, calls };
}

test('a drop into the Done lane asks before completing the task', async () => {
  const { win, calls } = doneDropFixture();
  await win.dropOnColumn(DROP_EV, 'c2');

  // Flattened because the id list is built in the module's vm realm, where
  // Array is not this realm's Array.
  assert.deepStrictEqual(calls.asked.flatMap((ids) => Array.from(ids)), ['t1']);
  assert.strictEqual(calls.moved.length, 1, 'the confirmed drop did not move the card');
});

test('declining the warning leaves the card where it was, with nothing written', async () => {
  const { win, calls } = doneDropFixture({ confirm: false });
  await win.dropOnColumn(DROP_EV, 'c2');

  assert.deepStrictEqual(calls.moved, [], 'a declined drop still wrote to the board');
  assert.strictEqual(calls.rerendered, 0, 'a declined drop repainted the board');
  assert.strictEqual(win.S.tasksByCol.c1[0].id, 't1', 'the card left its lane anyway');
});

test('a drop into an ordinary lane never asks', async () => {
  const { win, calls } = doneDropFixture();
  await win.dropOnColumn(DROP_EV, 'c1');
  assert.deepStrictEqual(calls.asked, []);
});

test('a card already Done that is re-dropped into the Done lane does not ask again', async () => {
  // Re-ordering inside the lane it already sits in completes nothing.
  const { win, calls } = doneDropFixture({ status: 'DONE' });
  win.S.board.columns[0].status = 'DONE';
  await win.dropOnColumn(DROP_EV, 'c2');
  assert.deepStrictEqual(calls.asked, []);
});
