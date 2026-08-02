// Unit tests for the stale-content banner's event filter. Plain Node, no build:
//   npm run test:unit -- realtime.test.js
//
// The backend funnels every logged change through a single `board.changed`
// event, so one stream carries ~23 activityType values. The banner used to fire
// on all of them whenever the current view had `liveRefresh`, which meant a
// co-worker publishing a wiki page — or commenting on any task other than the
// one you had open — announced that your BOARD had changed. Reloading returned
// the identical screen.
//
// affectsViewContent is the filter that fixed it. The rule it encodes is
// asymmetric on purpose, and both directions are worth holding still:
//   - known-irrelevant families are suppressed (that is the bug fix), and
//   - everything else is announced, including event shapes this file has never
//     seen, because a missed change shows stale data as if it were current.

import { test } from 'vitest';
import assert from 'node:assert';
import { loadModule } from './testutil.js';

function load() {
  return loadModule('realtime.js', {
    globals: {
      // realtime.js registers its delegated handlers at load time; the harness
      // stubs the register* helpers, and these are the globals its top level
      // otherwise touches.
      S: {}, Views: { get: () => null }, Auth: { isAuthenticated: () => false, token: null },
      API_BASE: '', V: '', USE_STANDALONE_DEMO_AUTH: false,
      el: () => null, t: (k) => k, html: () => '', raw: (x) => x,
    },
  });
}

// The two families the fix suppresses. Verified against the render code when
// the fix landed: no liveRefresh view (board, sprintBoard, backlog, tasks,
// statistics) draws a wiki page, a comment, or a comment count.
const SUPPRESSED = [
  'PAGE_CREATED', 'PAGE_PUBLISHED', 'PAGE_ARCHIVED',
  'TASK_COMMENT_ADDED', 'TASK_COMMENT_UPDATED',
];

// Real activityType values the backend publishes that DO change task data.
const ANNOUNCED = [
  'TASK_CREATED', 'TASK_UPDATED', 'TASK_MOVED', 'TASK_STATUS_CHANGED',
  'TASK_ASSIGNED', 'TASK_PRIORITY_CHANGED', 'TASK_ARCHIVED', 'TASK_REOPENED',
  'TASK_COPIED', 'TASK_AUTO_ARCHIVED', 'TASK_REMOVED_FROM_BOARD', 'TASK_DELETED',
  'TASKS_IMPORTED', 'PROJECT_IMPORTED', 'BULK_STATUS',
  // Statistics draws the active sprint and the release plan; the sprint board
  // and backlog draw sprint membership.
  'SPRINT_STARTED', 'SPRINT_COMPLETED', 'RELEASE_CLOSED', 'RELEASE_REOPENED',
];

test('changes no task view can draw do not raise the banner', () => {
  const { affectsViewContent } = load();
  SUPPRESSED.forEach((actType) => {
    assert.strictEqual(affectsViewContent(actType), false,
      `${actType} changes nothing a liveRefresh view renders, so it must stay quiet`);
  });
});

test('every task-affecting change still raises the banner', () => {
  const { affectsViewContent } = load();
  ANNOUNCED.forEach((actType) => {
    assert.strictEqual(affectsViewContent(actType), true,
      `${actType} changes task data on screen — suppressing it would hide a real change`);
  });
});

test('unknown and absent activity types fail open', () => {
  const { affectsViewContent } = load();
  // The webhook publisher emits `task.status_changed` with no activityType at
  // all, and it is a genuine task change. A new backend activity type must not
  // need a frontend release to be noticed either.
  [undefined, null, '', 'SOMETHING_ADDED_NEXT_YEAR'].forEach((actType) => {
    assert.strictEqual(affectsViewContent(actType), true,
      `${String(actType)} is not known to be irrelevant, so it must announce`);
  });
});

// Guards the asymmetry itself rather than a specific list: a future edit that
// suppresses a whole prefix (say, every TASK_*) would still pass the two lists
// above if someone edited them to match. This one would not.
test('the filter suppresses only comment and page changes, never task ones', () => {
  const { affectsViewContent } = load();
  const suppressed = [...SUPPRESSED, ...ANNOUNCED].filter((a) => !affectsViewContent(a));
  assert.deepStrictEqual(suppressed.sort(), [...SUPPRESSED].sort(),
    'the suppression set drifted — a change that alters task data is being hidden');
});
