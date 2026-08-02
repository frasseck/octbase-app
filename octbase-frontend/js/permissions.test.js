// Unit tests for AppPerms — specifically the archived-project freeze.
//   npm run test:unit -- permissions.test.js
//
// Archiving a project freezes it: every write route answers 409
// PROJECT_ARCHIVED. Until 1.1.2 nothing in the UI said so, because archiving
// had no UI at all and the state was only reachable by driving the API — so the
// board still rendered "Add task" in every lane, the create button, the inline
// create, the lane edit/delete controls and the drag handles, and clicking any
// of them produced a 409 toast. Honest, but it reads as a broken app rather
// than as a deliberate state.
//
// The fix is one gate rather than a hunt through the views: can() answers false
// for every write permission while the freeze holds, and every affordance
// already asks can(). These tests pin that gate, including the two things it
// must NOT do — block reads, and block the way back out.

import { test } from 'vitest';
import assert from 'node:assert';
import { loadModule } from './testutil.js';

const OWNER = 'PROJECT_OWNER';

// fresh loads permissions.js with a stubbed state module, so each case gets its
// own project/user without leaking into the next.
function fresh({ status = 'ACTIVE', role = OWNER, cached = null, globalRole = 'USER' } = {}) {
  const project = { id: 'p1', name: 'Demo', status };
  const S = {
    project,
    user: { globalRole, projectMemberships: [{ projectId: 'p1', role }] },
    permissionsByProject: cached ? { p1: { permissions: cached } } : {},
  };
  const win = loadModule('permissions.js', { globals: { S, api: {} } });
  return { AppPerms: win.AppPerms, project, S };
}

test('an active project grants its owner the write permissions', () => {
  const { AppPerms, project } = fresh();
  for (const perm of ['task.create', 'task.update', 'task.delete', 'project.invite_users']) {
    assert.equal(AppPerms.can(perm, project), true, `owner should hold ${perm}`);
  }
  assert.equal(AppPerms.isArchivedProject(project), false);
  assert.equal(AppPerms.isReadOnlyProject(project), false);
});

test('an archived project denies every write permission, whatever the role', () => {
  const { AppPerms, project } = fresh({ status: 'ARCHIVED' });
  for (const perm of ['task.create', 'task.update', 'task.delete', 'task.assign',
    'task.comment', 'project.invite_users', 'project.change_roles', 'project.remove_users']) {
    assert.equal(AppPerms.can(perm, project), false,
      `${perm} must be denied on an archived project — the API answers 409 PROJECT_ARCHIVED`);
  }
  assert.equal(AppPerms.isArchivedProject(project), true);
  assert.equal(AppPerms.isReadOnlyProject(project), true);
});

test('not even a super-admin can write to an archived project', () => {
  // SUPER_ADMIN short-circuits the role lookup to PROJECT_ADMIN, so it is the
  // one path that could slip past a check placed in the role fallback.
  const { AppPerms, project } = fresh({ status: 'ARCHIVED', globalRole: 'SUPER_ADMIN' });
  assert.equal(AppPerms.can('task.create', project), false);
  assert.equal(AppPerms.can('project.change_roles', project), false);
});

test('an archived project still grants the read permissions', () => {
  // The freeze stops writes, not reads: the project stays fully browsable, and
  // a UI that hid it would be lying about what was archived.
  const { AppPerms, project } = fresh({ status: 'ARCHIVED' });
  assert.equal(AppPerms.can('project.view', project), true);
  assert.equal(AppPerms.can('task.view', project), true);
});

test('the freeze outranks the cached permission map from the API', () => {
  // GET /projects/{id}/permissions describes the member's ROLE and knows
  // nothing about the archived state, so a cached "yes" must not win. This is
  // why the check sits above the cache lookup rather than in the role fallback.
  const { AppPerms, project } = fresh({
    status: 'ARCHIVED',
    cached: { 'task.create': true, 'task.update': true, 'task.view': true },
  });
  assert.equal(AppPerms.can('task.create', project), false);
  assert.equal(AppPerms.can('task.update', project), false);
  assert.equal(AppPerms.can('task.view', project), true, 'a cached read permission still applies');
});

test('can() falls back to the open project when none is passed', () => {
  const { AppPerms } = fresh({ status: 'ARCHIVED' });
  assert.equal(AppPerms.can('task.create'), false);
  assert.equal(AppPerms.isArchivedProject(), true);
});
