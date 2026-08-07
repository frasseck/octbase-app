// Unit tests for the task-hierarchy helpers in @octbase/shared/meta.js.
//   npm run test:unit -- meta-hierarchy.test.js
//
// These mirror the backend's ParentTaskTypeFor / ChildTaskTypeFor /
// TaskParentTypeAllowed, and both SPAs build their parent pickers from them —
// so a drift here offers the user a parent the API answers with 422
// TASK_PARENT_TYPE_INVALID, or hides one it would have accepted.
//
// The rule under test (OCT-12): a parent may be any level ABOVE the child, not
// only the level directly above, so a task can sit straight under an epic.
// SUBTASK is the exception and keeps its exactly-a-TASK parent.

import { test } from 'vitest';
import assert from 'node:assert';
import { typeChain, typeChildOf, typeParentAllowed, typeParentRule } from '@octbase/shared/meta.js';

// The core chain is always on; THEME and INITIATIVE are opt-in per project.
const core = {};
const full = { themeEnabled: true, initiativeEnabled: true };

test('typeChain reflects the project’s enabled levels', () => {
  assert.deepStrictEqual(typeChain(core), ['EPIC', 'STORY', 'TASK', 'SUBTASK']);
  assert.deepStrictEqual(typeChain(full),
    ['THEME', 'INITIATIVE', 'EPIC', 'STORY', 'TASK', 'SUBTASK']);
  // A null project is the core chain, not a crash.
  assert.deepStrictEqual(typeChain(null), ['EPIC', 'STORY', 'TASK', 'SUBTASK']);
});

test('typeParentRule still reports the NEAREST parent type and requiredness', () => {
  assert.deepStrictEqual(typeParentRule(core, 'TASK'), { parentType: 'STORY', required: false });
  assert.deepStrictEqual(typeParentRule(core, 'SUBTASK'), { parentType: 'TASK', required: true });
  // The chain's top type can have no parent at all.
  assert.deepStrictEqual(typeParentRule(core, 'EPIC'), { parentType: null, required: false });
  assert.deepStrictEqual(typeParentRule(full, 'EPIC'), { parentType: 'INITIATIVE', required: false });
});

test('a parent may be any level above the child', () => {
  // The point of the change: a task under an epic, not only under a story.
  assert.ok(typeParentAllowed(core, 'TASK', 'STORY'));
  assert.ok(typeParentAllowed(core, 'TASK', 'EPIC'));
  assert.ok(typeParentAllowed(core, 'STORY', 'EPIC'));
  // …including across the optional levels when the project enables them.
  assert.ok(typeParentAllowed(full, 'TASK', 'THEME'));
  assert.ok(typeParentAllowed(full, 'EPIC', 'THEME'));
});

test('a parent may never be at or below the child’s own level', () => {
  assert.ok(!typeParentAllowed(core, 'STORY', 'TASK'));
  assert.ok(!typeParentAllowed(core, 'EPIC', 'STORY'));
  // A type may not parent its own type.
  assert.ok(!typeParentAllowed(core, 'TASK', 'TASK'));
  // The chain's top type has nothing above it.
  assert.ok(!typeParentAllowed(core, 'EPIC', 'EPIC'));
});

test('SUBTASK does not get to skip: its parent stays exactly a TASK', () => {
  assert.ok(typeParentAllowed(core, 'SUBTASK', 'TASK'));
  assert.ok(!typeParentAllowed(core, 'SUBTASK', 'STORY'));
  assert.ok(!typeParentAllowed(core, 'SUBTASK', 'EPIC'));
});

test('a level the project has not enabled is never a parent', () => {
  // THEME/INITIATIVE are off in `core`, so they are not in its chain at all.
  assert.ok(!typeParentAllowed(core, 'TASK', 'THEME'));
  assert.ok(!typeParentAllowed(core, 'TASK', 'INITIATIVE'));
  assert.ok(!typeParentAllowed(core, 'INITIATIVE', 'THEME'));
  // An unknown type is refused rather than resolving to index -1 arithmetic.
  assert.ok(!typeParentAllowed(core, 'TASK', 'WIZARD'));
  assert.ok(!typeParentAllowed(core, 'WIZARD', 'EPIC'));
});

test('typeChildOf still answers the can-this-type-have-children question', () => {
  // It reports the NEAREST child type; the picker uses it only to decide
  // whether children are possible at all, which SUBTASK answers with no.
  assert.strictEqual(typeChildOf(core, 'EPIC'), 'STORY');
  assert.strictEqual(typeChildOf(core, 'TASK'), 'SUBTASK');
  assert.strictEqual(typeChildOf(core, 'SUBTASK'), '');
});
