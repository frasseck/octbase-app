// Unit tests for mindmapScope() in views-mindmap.js. Plain Node, no build:
//   npm run test:unit -- views-mindmap.test.js
//
// The mindmap draws open work by default. Getting "open" wrong is quiet: the
// map still renders, it just shows the wrong project — either buried under
// finished tasks, or with running ones missing because their parent was done.
// So the cases below pin the three decisions the filter makes: what counts as
// closed, that ARCHIVED never appears in either mode, and that a done task
// carrying open children survives as a branch (a "ghost") instead of stranding
// them in the synthetic "without parent" groups.

import { test } from 'vitest';
import assert from 'node:assert';
import { loadModule } from './testutil.js';

// views-mindmap.js registers its view and its click actions at load time; that
// registration pair is the only thing its top-level code touches.
function fresh() {
  return loadModule('views-mindmap.js', { globals: { Views: { register() {} }, registerActions() {} } });
}

// task builds a minimal task: the filter reads id, status and parentId only.
function task(id, status, parentId = null) {
  return { id, status, parentId, title: id, taskType: 'TASK' };
}

const ids = (result) => result.tasks.map(t => t.id).sort();

test('by default the map holds the open tasks and nothing else', () => {
  const { mindmapScope } = fresh();
  const r = mindmapScope([
    task('planned', 'PLANNED'),
    task('wip', 'IN_PROGRESS'),
    task('review', 'IN_REVIEW'),
    task('done', 'DONE'),
    task('archived', 'ARCHIVED'),
  ], false);
  assert.deepStrictEqual(ids(r), ['planned', 'review', 'wip']);
  assert.strictEqual(r.hiddenDone, 1, 'only the DONE task counts as hidden-done');
  assert.strictEqual(r.ghosts.size, 0);
});

test('a custom board-lane status is open work, not done', () => {
  const { mindmapScope } = fresh();
  // Admins add lanes freely; only the two built-ins mean finished, so a task
  // parked in a "Blocked" or "QA" lane must keep showing on the default map.
  const r = mindmapScope([task('qa', 'QA'), task('blocked', 'Blocked')], false);
  assert.deepStrictEqual(ids(r), ['blocked', 'qa']);
  assert.strictEqual(r.hiddenDone, 0);
});

test('showing done tasks brings back DONE but never ARCHIVED', () => {
  const { mindmapScope } = fresh();
  const r = mindmapScope([
    task('open', 'PLANNED'),
    task('done', 'DONE'),
    task('archived', 'ARCHIVED'),
  ], true);
  assert.deepStrictEqual(ids(r), ['done', 'open'], 'archived stays out in both modes');
  assert.strictEqual(r.ghosts.size, 0, 'nothing is a ghost when everything done is shown');
  assert.strictEqual(r.hiddenDone, 0);
});

test('a done ancestor is kept as a branch when open work still hangs from it', () => {
  const { mindmapScope } = fresh();
  // epic(DONE) → story(DONE) → t(PLANNED): both closed levels are the only path
  // from the root to a task that is still running.
  const r = mindmapScope([
    task('epic', 'DONE'),
    task('story', 'DONE', 'epic'),
    task('t', 'PLANNED', 'story'),
  ], false);
  assert.deepStrictEqual(ids(r), ['epic', 'story', 't']);
  assert.deepStrictEqual([...r.ghosts].sort(), ['epic', 'story']);
  assert.strictEqual(r.hiddenDone, 0, 'a done task drawn as a branch is not hidden');
});

test('a done task with no open descendant is dropped, ghost or not', () => {
  const { mindmapScope } = fresh();
  // Same epic, but its two stories are finished too — the whole branch goes.
  const r = mindmapScope([
    task('epic', 'DONE'),
    task('s1', 'DONE', 'epic'),
    task('s2', 'DONE', 'epic'),
    task('elsewhere', 'PLANNED'),
  ], false);
  assert.deepStrictEqual(ids(r), ['elsewhere']);
  assert.strictEqual(r.hiddenDone, 3);
});

test('the walk up stops at the first open ancestor', () => {
  const { mindmapScope } = fresh();
  // done-epic → open-story → done-task → open-subtask. The open story keeps
  // itself and its done epic; the done task is a ghost for the subtask.
  const r = mindmapScope([
    task('epic', 'DONE'),
    task('story', 'IN_PROGRESS', 'epic'),
    task('mid', 'DONE', 'story'),
    task('sub', 'PLANNED', 'mid'),
  ], false);
  assert.deepStrictEqual(ids(r), ['epic', 'mid', 'story', 'sub']);
  assert.deepStrictEqual([...r.ghosts].sort(), ['epic', 'mid']);
});

test('an archived ancestor is not resurrected to carry an open child', () => {
  const { mindmapScope } = fresh();
  // Archived is out of the data set before any ancestor walk, so the open child
  // falls through to the synthetic "without parent" branch the tree builder
  // already has for exactly this case.
  const r = mindmapScope([
    task('archivedParent', 'ARCHIVED'),
    task('child', 'PLANNED', 'archivedParent'),
  ], false);
  assert.deepStrictEqual(ids(r), ['child']);
  assert.strictEqual(r.ghosts.size, 0);
  assert.strictEqual(r.hiddenDone, 0, 'archived is not counted as hidden done work');
});

test('a dangling parentId is harmless', () => {
  const { mindmapScope } = fresh();
  const r = mindmapScope([task('child', 'PLANNED', 'no-such-task')], false);
  assert.deepStrictEqual(ids(r), ['child']);
});

test('a cyclic parent chain terminates instead of hanging the render', () => {
  const { mindmapScope } = fresh();
  // The backend guards against cycles; the renderer must not depend on that,
  // because an unterminated walk here is a frozen tab, not a failed request.
  const r = mindmapScope([
    task('a', 'DONE', 'b'),
    task('b', 'DONE', 'a'),
    task('open', 'PLANNED', 'a'),
  ], false);
  assert.deepStrictEqual(ids(r), ['a', 'b', 'open']);
  assert.deepStrictEqual([...r.ghosts].sort(), ['a', 'b']);
});

test('an empty project yields an empty scope', () => {
  const { mindmapScope } = fresh();
  const r = mindmapScope([], false);
  assert.deepStrictEqual(r.tasks, []);
  assert.strictEqual(r.hiddenDone, 0);
  assert.strictEqual(r.ghosts.size, 0);
});
