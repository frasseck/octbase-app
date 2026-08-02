// Unit tests for the create affordances' permission gate. Plain Node, no build:
//   npm run test:unit -- views-shell.test.js
//
// The defect these hold shut: viewCreateButton dispatched straight to the active
// view's createButton() with no permission check, so a PROJECT_VIEWER — who
// holds none of task.create / writerGuard — was offered "Create task", "New
// sprint", "New release" and "New page" on every view, and got a 403 for
// clicking. What is asserted is which branch runs, not the markup: the gate is
// one question (isReadOnlyProject) asked before the view is consulted at all.

import { test } from 'vitest';
import assert from 'node:assert';
import { loadModule } from './testutil.js';

// A create button for every view that defines one, keyed the way Views.get
// resolves them. The strings only need to be recognisable.
const CREATE_BUTTONS = {
  board: '<button data-act="showCreateTask">create task</button>',
  backlog: '<button data-act="showCreateTask">create backlog item</button>',
  tasks: '<button data-act="showCreateTask">create task</button>',
  sprints: '<button data-act="showCreateSprint">new sprint</button>',
  releases: '<button data-act="showCreateRelease">new release</button>',
  pages: '<button data-act="showCreatePage">new page</button>',
};

// views-shell.js touches a wide surface at render time; stub exactly the names
// these two functions reach for. `readOnly` is what AppPerms.isReadOnlyProject
// answers — in the real module that is !can('task.create'), which is false for
// a PROJECT_VIEWER and false for everyone while a project is archived.
function fresh({ view = 'board', project = { id: 'p1' }, readOnly = false } = {}) {
  return loadModule('views-shell.js', {
    globals: {
      S: { project, view },
      Views: {
        register() {},
        get: (v) => (CREATE_BUTTONS[v] ? { createButton: () => CREATE_BUTTONS[v] } : undefined),
      },
      AppPerms: {
        isReadOnlyProject: () => readOnly,
        isArchivedProject: () => false,
        can: () => !readOnly,
      },
      t: (key) => key,
      icon: () => '<svg class="icon-svg"></svg>',
      esc: (s) => String(s == null ? '' : s),
      el: () => null,
      api: {},
      debounced: (_ms, fn) => fn,
      registerActions() {}, registerChanges() {}, registerInputs() {}, registerKeydowns() {},
    },
  });
}

test('a writer is offered the create button on every view that defines one', () => {
  for (const view of Object.keys(CREATE_BUTTONS)) {
    const { viewCreateButton } = fresh({ view });
    assert.strictEqual(viewCreateButton(), CREATE_BUTTONS[view],
      `writer lost the create button on the ${view} view`);
  }
});

test('a read-only member is offered it on none of them', () => {
  for (const view of Object.keys(CREATE_BUTTONS)) {
    const { viewCreateButton } = fresh({ view, readOnly: true });
    assert.strictEqual(viewCreateButton(), '',
      `read-only member was still offered the create button on the ${view} view`);
  }
});

test('the gate is asked before the view is consulted', () => {
  // Regression shape: a later refactor that calls def.createButton() first and
  // filters afterwards would still pass the assertion above, but would run view
  // code on behalf of someone who may not create. Views.get must not be reached.
  let consulted = false;
  const win = loadModule('views-shell.js', {
    globals: {
      S: { project: { id: 'p1' }, view: 'board' },
      Views: { register() {}, get: () => { consulted = true; return { createButton: () => 'x' }; } },
      AppPerms: { isReadOnlyProject: () => true, isArchivedProject: () => false, can: () => false },
      t: (key) => key, icon: () => '', esc: (s) => String(s ?? ''), el: () => null, api: {},
      debounced: (_ms, fn) => fn,
      registerActions() {}, registerChanges() {}, registerInputs() {}, registerKeydowns() {},
    },
  });
  assert.strictEqual(win.viewCreateButton(), '');
  assert.strictEqual(consulted, false, 'the view was consulted despite the member being read-only');
});

test('the no-project views still offer project creation', () => {
  // Creating a PROJECT is a global-role decision, not a project-scoped one, so
  // the read-only gate must not reach it — it returns before the check.
  const { viewCreateButton } = fresh({ project: null, readOnly: true });
  const html = viewCreateButton();
  assert.match(html, /data-act="showCreateProject"/);
  assert.match(html, /data-act="importNewProject"/);
});

test('inline task creation is refused for a read-only member', () => {
  // Reachable by keyboard shortcut, so hiding the button is not enough: the
  // function has to refuse on its own. It returns before touching the DOM,
  // which el() returning null would otherwise throw on.
  const { showInlineTaskCreate } = fresh({ readOnly: true });
  assert.strictEqual(showInlineTaskCreate(), undefined);
});
