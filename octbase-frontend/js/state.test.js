// @vitest-environment jsdom
//
// Unit tests for state.js's task-filtering logic.
//   npm run test:unit -- state.test.js
//
// Imports the real module (37b stage 7): no vm harness, no module-syntax
// rewriting, no fake window — jsdom supplies the browser globals state.js
// touches, and Vite resolves '@octbase/shared/i18n.js' the same way the app
// does. What the harness bought here was isolation between tests, and that is
// what `fresh()` still provides: applyTaskFilters and filterTasksBySearch read
// the module-global S rather than taking the filters as arguments, so each test
// seeds S.filters before calling (the functions close over the same object, so
// the mutation is visible).

import { test } from 'vitest';
import assert from 'node:assert';
import { S, applyTaskFilters, filterTasksBySearch } from './state.js';

// fresh(filters) seeds S.filters and hands back the two functions under test.
// One module instance is shared by every test in the file — safe because each
// call overwrites the whole filters object rather than merging into it.
function fresh(filters = {}) {
  S.filters = { status: '', priority: '', type: '', q: '', ...filters };
  return { S, applyTaskFilters, filterTasksBySearch };
}

const T = (over) => ({ id: 'x', title: 't', status: 'PLANNED', priority: 'MEDIUM', taskType: 'TASK', boardColumnId: null, ...over });
// applyTaskFilters builds its result array inside the vm context, whose
// Array.prototype differs from this test realm's; deepStrictEqual checks
// prototypes, so pull the ids into a test-realm array via Array.from first.
const ids = (list) => Array.from(list, (t) => t.id);

test('applyTaskFilters hides ARCHIVED tasks by default', () => {
  const win = fresh();
  const tasks = [T({ id: 'a' }), T({ id: 'b', status: 'ARCHIVED' }), T({ id: 'c', status: 'DONE' })];
  assert.deepStrictEqual(ids(win.applyTaskFilters(tasks)), ['a', 'c']);
});

test('applyTaskFilters with an explicit status shows only that status (incl. ARCHIVED)', () => {
  const win = fresh({ status: 'ARCHIVED' });
  const tasks = [T({ id: 'a' }), T({ id: 'b', status: 'ARCHIVED' })];
  assert.deepStrictEqual(ids(win.applyTaskFilters(tasks)), ['b']);
});

test('applyTaskFilters filters by priority and type', () => {
  const tasks = [
    T({ id: 'a', priority: 'HIGH', taskType: 'TASK' }),
    T({ id: 'b', priority: 'LOW', taskType: 'BUG' }),
  ];
  // Two filter states, seeded one at a time. They used to be two separately
  // loaded copies of the module held side by side, which only worked because
  // the old harness re-evaluated the file per call; with the real module there
  // is one S, so each assertion sets the filters it is about. Same two
  // properties asserted, one fewer thing pretended.
  assert.deepStrictEqual(ids(fresh({ priority: 'HIGH' }).applyTaskFilters(tasks)), ['a']);
  assert.deepStrictEqual(ids(fresh({ type: 'BUG' }).applyTaskFilters(tasks)), ['b']);
});

test('applyTaskFilters boardOnly keeps only tasks on a board column', () => {
  const win = fresh();
  const tasks = [T({ id: 'a', boardColumnId: 'col1' }), T({ id: 'b', boardColumnId: null })];
  assert.deepStrictEqual(ids(win.applyTaskFilters(tasks, { boardOnly: true })), ['a']);
});

test('applyTaskFilters backlogOnly drops board tasks and (unfiltered) DONE/ARCHIVED', () => {
  const win = fresh();
  const tasks = [
    T({ id: 'a', boardColumnId: null, status: 'PLANNED' }),
    T({ id: 'b', boardColumnId: 'col1', status: 'PLANNED' }), // on a board → out
    T({ id: 'c', boardColumnId: null, status: 'DONE' }),      // done → out
    T({ id: 'd', boardColumnId: null, status: 'ARCHIVED' }),  // archived → out
  ];
  assert.deepStrictEqual(ids(win.applyTaskFilters(tasks, { backlogOnly: true })), ['a']);
});

test('filterTasksBySearch is a no-op for an empty query', () => {
  const win = fresh({ q: '   ' });
  const tasks = [T({ id: 'a' }), T({ id: 'b' })];
  assert.strictEqual(win.filterTasksBySearch(tasks), tasks); // same array reference
});

test('filterTasksBySearch matches title always and description only in fulltext mode', () => {
  const win = fresh({ q: 'needle' });
  const tasks = [
    T({ id: 'title', title: 'has needle here', description: '' }),
    T({ id: 'desc', title: 'plain', description: 'buried needle deep' }),
    T({ id: 'none', title: 'plain', description: 'nothing' }),
  ];
  assert.deepStrictEqual(ids(win.filterTasksBySearch(tasks, { fulltext: true })), ['title', 'desc']);
  assert.deepStrictEqual(ids(win.filterTasksBySearch(tasks, { fulltext: false })), ['title']);
});

test('filterTasksBySearch is case-insensitive', () => {
  const win = fresh({ q: 'NeEdLe' });
  const tasks = [T({ id: 'a', title: 'a NEEDLE b' }), T({ id: 'b', title: 'x' })];
  assert.deepStrictEqual(ids(win.filterTasksBySearch(tasks, { fulltext: true })), ['a']);
});

test('applyTaskFilters applies the search query too (via ignoreSearch=false default)', () => {
  const win = fresh({ q: 'keep' });
  const tasks = [T({ id: 'a', title: 'keep me' }), T({ id: 'b', title: 'drop me' })];
  assert.deepStrictEqual(ids(win.applyTaskFilters(tasks)), ['a']);
  assert.deepStrictEqual(ids(win.applyTaskFilters(tasks, { ignoreSearch: true })), ['a', 'b']);
});

// ── Searching by task key (OCT-202) ─────────────────────────────────────────
// The key is derived (project abbreviation + seqNumber), never stored on the
// task, so these assert the parse of the QUERY rather than a text match: which
// spellings resolve to a sequence number, and which deliberately do not.

// seqFresh seeds S.project too, since the key's letter part comes from it.
function seqFresh(q, project = { abbreviation: 'OCT' }) {
  const win = fresh({ q });
  win.S.project = project;
  return win;
}
const seqTasks = () => [
  T({ id: 'a', title: 'first', seqNumber: 2 }),
  T({ id: 'b', title: 'second', seqNumber: 20 }),
  T({ id: 'c', title: 'third', seqNumber: 202 }),
];

test('filterTasksBySearch matches the full task key, in either case', () => {
  for (const q of ['OCT-202', 'oct-202', '  OCT-202  ', '#OCT-202']) {
    const win = seqFresh(q);
    assert.deepStrictEqual(ids(win.filterTasksBySearch(seqTasks(), { fulltext: true })), ['c'], q);
  }
});

test('filterTasksBySearch matches a bare sequence number', () => {
  const win = seqFresh('202');
  assert.deepStrictEqual(ids(win.filterTasksBySearch(seqTasks(), { fulltext: true })), ['c']);
});

test('filterTasksBySearch matches the key on the board too (title-only mode)', () => {
  // The board passes fulltext:false; ID lookup must still work there.
  const win = seqFresh('OCT-202');
  assert.deepStrictEqual(ids(win.filterTasksBySearch(seqTasks())), ['c']);
});

test('filterTasksBySearch ignores a key naming another project', () => {
  const win = seqFresh('FOO-202');
  assert.deepStrictEqual(ids(win.filterTasksBySearch(seqTasks(), { fulltext: true })), []);
});

test('filterTasksBySearch falls back to the project slug when there is no abbreviation', () => {
  const win = seqFresh('proj-20', { slug: 'proj' });
  assert.deepStrictEqual(ids(win.filterTasksBySearch(seqTasks(), { fulltext: true })), ['b']);
});

test('filterTasksBySearch matches a key exactly, not by prefix', () => {
  const win = seqFresh('OCT-20');
  assert.deepStrictEqual(ids(win.filterTasksBySearch(seqTasks(), { fulltext: true })), ['b']);
});

test('filterTasksBySearch keeps text matches alongside the ID match', () => {
  // "202" is both a sequence number and text someone may have typed in a title;
  // the ID branch is additive, so both tasks come back.
  const win = seqFresh('202');
  const tasks = [...seqTasks(), T({ id: 'd', title: 'error 202 on save', seqNumber: 7 })];
  assert.deepStrictEqual(ids(win.filterTasksBySearch(tasks, { fulltext: true })), ['c', 'd']);
});

test('filterTasksBySearch treats non-key text as plain text', () => {
  // Nothing here parses as a key, so an unestimated/unnumbered task is safe and
  // ordinary words never accidentally select a task by ID.
  const win = seqFresh('oct');
  const tasks = [...seqTasks(), T({ id: 'd', title: 'octopus', seqNumber: null })];
  assert.deepStrictEqual(ids(win.filterTasksBySearch(tasks, { fulltext: true })), ['d']);
});

// ── The lowercased search haystack ──────────────────────────────────────────
// filterTasksBySearch caches the lowercased title/description per task. The cache
// is derived from the task's own fields on every call rather than maintained at
// write sites, so these tests are the proof that no write site needs to know about
// it: they mutate a task the way the in-place patch paths do (applyListTaskUpdate,
// patchBoardCaches — both `{...row, ...snapshot}` or a direct field write) and
// assert the next search sees the new text. A stale haystack returns wrong search
// results silently, which is why this is tested rather than reasoned about.

test('filterTasksBySearch sees a title changed in place after a first search', () => {
  const win = fresh({ q: 'renamed' });
  const task = T({ id: 'a', title: 'original' });
  const tasks = [task];
  assert.deepStrictEqual(ids(win.filterTasksBySearch(tasks, { fulltext: true })), []);
  task.title = 'now renamed';
  assert.deepStrictEqual(ids(win.filterTasksBySearch(tasks, { fulltext: true })), ['a']);
});

test('filterTasksBySearch sees a description changed in place after a first search', () => {
  const win = fresh({ q: 'added' });
  const task = T({ id: 'a', title: 'plain', description: 'nothing here' });
  const tasks = [task];
  assert.deepStrictEqual(ids(win.filterTasksBySearch(tasks, { fulltext: true })), []);
  task.description = 'an added word';
  assert.deepStrictEqual(ids(win.filterTasksBySearch(tasks, { fulltext: true })), ['a']);
});

test('filterTasksBySearch stops matching text that a change removed', () => {
  const win = fresh({ q: 'needle' });
  const task = T({ id: 'a', title: 'has needle', description: 'needle again' });
  const tasks = [task];
  assert.deepStrictEqual(ids(win.filterTasksBySearch(tasks, { fulltext: true })), ['a']);
  task.title = 'clean';
  task.description = 'clean too';
  assert.deepStrictEqual(ids(win.filterTasksBySearch(tasks, { fulltext: true })), []);
});

test('filterTasksBySearch handles a spread copy of an already-searched task', () => {
  // The board/list patch paths replace a cached row with `{...row, ...snapshot}`,
  // a NEW object carrying the old one's fields. The copy must be matched on its
  // own current text, not on anything the original had cached.
  const win = fresh({ q: 'fresh' });
  const original = T({ id: 'a', title: 'stale title' });
  assert.deepStrictEqual(ids(win.filterTasksBySearch([original], { fulltext: true })), []);
  const patched = { ...original, title: 'fresh title' };
  assert.deepStrictEqual(ids(win.filterTasksBySearch([patched], { fulltext: true })), ['a']);
});

test('filterTasksBySearch tolerates missing title/description', () => {
  const win = fresh({ q: 'x' });
  const tasks = [T({ id: 'a', title: undefined, description: undefined })];
  assert.deepStrictEqual(ids(win.filterTasksBySearch(tasks, { fulltext: true })), []);
});
