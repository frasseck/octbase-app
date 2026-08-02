// Unit tests for activityMessage() in views-task.js. Plain Node, no build:
//   npm run test:unit -- views-task.test.js
//
// The Activity tab and the project Activity view render every entry through
// activityMessage(), which looks up `notifications.activity.<type>`. The locale
// files had drifted behind the backend — 11 of the types the API writes had no
// key, so each one logged "[i18n] Missing translation key" on every render and
// fell back to the raw enum name (TASK_ASSIGNED, TASK_PRIORITY_CHANGED, …).
//
// So the coverage test below reads the activity types out of the Go source
// rather than hardcoding them: adding a new activity.Write on the backend
// without a matching key fails this test instead of reaching a user's console.

import { test } from 'vitest';
import assert from 'node:assert';
import fs from 'node:fs';
import path from 'node:path';
import vm from 'node:vm';
import { fileURLToPath } from 'node:url';
import { makeWindow, readAsClassicScript } from './testutil.js';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const LOCALES_DIR = path.join(__dirname, '..', 'locales');
const API_INTERNAL = path.join(__dirname, '..', '..', 'octbase-api', 'internal');

// ── Loading views-task.js with a real i18n on top of the real locale files ──

// load builds a window with the actual i18n module and locale JSON (so the test
// exercises the shipped translations, not a fixture), stubs the few globals
// views-task.js touches from other files, and captures console warnings —
// a missing key is a warning, not a throw, so the warnings ARE the assertion.
async function load(lang = 'en', extra = {}) {
  const warnings = [];
  // From meta.js / framework.js — stubbed so a label change there cannot break
  // this file's tests. Re-applied after meta.js runs below, because that module
  // defines the real STATUS_META/priorityMeta into the same window and would
  // otherwise win on load order.
  const stubs = {
    STATUS_META: { DONE: { label: 'Done' }, PLANNED: { label: 'Planned' } },
    ESTIMATION_UNITS: ['NONE', 'POINTS', 'HOURS'],
    priorityMeta: (p) => ({ label: `prio(${p})` }),
    memberName: (uid) => (uid ? `name(${uid})` : '—'),
    debounced: () => () => {},
    S: {},
    ...extra,
  };
  const win = makeWindow({
    fetch: async (url) => {
      const file = path.join(LOCALES_DIR, path.basename(url));
      if (!fs.existsSync(file)) return { ok: false };
      return { ok: true, json: async () => JSON.parse(fs.readFileSync(file, 'utf8')) };
    },
    console: { ...console, warn: (m) => warnings.push(String(m)) },
    ...stubs,
  });
  vm.createContext(win);
  vm.runInContext(readAsClassicScript('../../octbase-shared/i18n.js'), win);
  // Load English first so the fallback chain is populated for the German run.
  await win.i18n.setLocale('en');
  if (lang !== 'en') await win.i18n.setLocale(lang);
  // meta.js is loaded for real, not stubbed, because openDescendantsOf lives
  // there since OCT-301 — the subtree walk the tests below exercise is the
  // shipped one, shared with the mobile SPA.
  vm.runInContext(readAsClassicScript('../../octbase-shared/meta.js'), win);
  Object.assign(win, stubs);
  // Every file here is an ES module: views-task.js since 37b stage 2, the
  // @octbase/shared modules since stage 3 — all rewritten by the harness so
  // they share one window.
  vm.runInContext(readAsClassicScript('views-task.js'), win);
  return { win, warnings };
}

// ── The backend's activity vocabulary, read from the Go source ──────────────

const WRITE_CALL = /(writeActivityTx|writeActivity|writeBulkActivity|activity\.Write)\(/;
// scmintegration picks its type into a local before the call, so the literal is
// not on the call line: `event := "BRANCH_CREATED"` / `event = "BRANCH_LINKED"`.
const TYPE_VAR = /\b(?:event|actType|activityType)\s*:?=\s*"([A-Z][A-Z_]{3,})"/g;

function goFiles(dir) {
  return fs.readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) return goFiles(p);
    return e.name.endsWith('.go') && !e.name.endsWith('_test.go') ? [p] : [];
  });
}

function backendActivityTypes() {
  const types = new Set();
  for (const file of goFiles(API_INTERNAL)) {
    const src = fs.readFileSync(file, 'utf8');
    if (!WRITE_CALL.test(src)) continue;
    const lines = src.split('\n');
    lines.forEach((line, i) => {
      if (!WRITE_CALL.test(line)) return;
      // The params map often wraps, so read a small window, not just the line.
      const window = lines.slice(i, i + 3).join(' ');
      for (const m of window.matchAll(/"([A-Z][A-Z_]{3,})"/g)) types.add(m[1]);
    });
    for (const m of src.matchAll(TYPE_VAR)) types.add(m[1]);
  }
  return [...types].sort();
}

// Params mirroring what the backend actually writes. Only the shapes that
// change rendering need to be exact: `count` selects a plural form, and the
// assignment ids drive which message parts activityMessage() emits.
const SAMPLE_PARAMS = {
  TASK_STATUS_CHANGED: { status: 'DONE', from: 'PLANNED' },
  TASK_PRIORITY_CHANGED: { priority: 'HIGH' },
  TASK_ASSIGNED: { assigneeId: 'u-1' },
  TASKS_IMPORTED: { count: 3, attachments: 1, skipped: 0, warnings: 0, source: 'jira_csv' },
  PROJECT_IMPORTED: { tasks: 5, pages: 2 },
  PROJECT_ESTIMATION_UNIT_CHANGED: { from: 'NONE', to: 'POINTS' },
};

const GENERIC_PARAMS = { title: 'T', name: 'N', sourceTitle: 'S', branchName: 'B', status: 'DONE' };

test('every activity type the backend writes renders a translated message', async () => {
  const types = backendActivityTypes();
  // Guard the extraction itself: if the regex ever stops matching, the loop
  // below would pass vacuously.
  assert.ok(types.length >= 25, `expected the Go source to yield the activity vocabulary, got ${types.length}`);
  assert.ok(types.includes('TASK_ASSIGNED') && types.includes('BRANCH_LINKED'), 'extraction missed known types');

  for (const lang of ['en', 'de']) {
    const { win, warnings } = await load(lang);
    for (const type of types) {
      const params = { ...GENERIC_PARAMS, ...(SAMPLE_PARAMS[type] || {}) };
      const msg = win.activityMessage({ type, params });
      assert.notStrictEqual(msg, type, `[${lang}] ${type} fell back to the raw type`);
      assert.ok(!msg.startsWith('notifications.'), `[${lang}] ${type} rendered a raw key: ${msg}`);
      assert.ok(!msg.includes('{{'), `[${lang}] ${type} left a placeholder unfilled: ${msg}`);
    }
    assert.deepStrictEqual(warnings, [], `[${lang}] i18n warnings while rendering activity`);
  }
});

test('status and priority render as labels, not enum names', async () => {
  const { win, warnings } = await load();
  assert.strictEqual(
    win.activityMessage({ type: 'TASK_STATUS_CHANGED', params: { status: 'DONE' } }),
    'Changed status to Done');
  assert.strictEqual(
    win.activityMessage({ type: 'TASK_PRIORITY_CHANGED', params: { priority: 'HIGH' } }),
    'Changed priority to prio(HIGH)');
  // Custom priorities have no PRIORITY_META entry; priorityMeta() still names them.
  assert.strictEqual(
    win.activityMessage({ type: 'TASK_PRIORITY_CHANGED', params: { priority: 'Blocker-ish' } }),
    'Changed priority to prio(Blocker-ish)');
  assert.deepStrictEqual(warnings, []);
});

test('TASK_ASSIGNED names the people and reports only the fields that changed', async () => {
  const { win, warnings } = await load();
  const msg = (params) => win.activityMessage({ type: 'TASK_ASSIGNED', params });

  assert.strictEqual(msg({ assigneeId: 'u-1' }), 'Assigned task to name(u-1)');
  assert.strictEqual(msg({ reviewerId: 'u-2' }), 'Set name(u-2) as reviewer');
  assert.strictEqual(msg({ assigneeId: 'u-1', reviewerId: 'u-2' }),
    'Assigned task to name(u-1) · Set name(u-2) as reviewer');
  // A cleared field is written as an explicit null, which is not "unchanged".
  assert.strictEqual(msg({ assigneeId: null }), 'Removed the assignee');
  assert.strictEqual(msg({ reviewerId: null }), 'Removed the reviewer');
  assert.strictEqual(msg({ assigneeId: null, reviewerId: null }),
    'Removed the assignee · Removed the reviewer');
  // Neither field present (nothing to describe) falls back to the plain key.
  assert.strictEqual(msg({}), 'Updated assignment');
  assert.deepStrictEqual(warnings, []);
});

test('an unknown activity type still degrades to the raw type without throwing', async () => {
  const { win } = await load();
  assert.strictEqual(win.activityMessage({ type: 'NOT_A_REAL_TYPE', params: {} }), 'NOT_A_REAL_TYPE');
});

// ── Immutable (DONE/ARCHIVED) tasks: frozen editors render disabled ─────────
//
// The API rejects estimate and due-date edits on a finished task with
// 422 TASK_IMMUTABLE, so the panel must not offer those editors live. These
// tests load the real @octbase/shared/meta.js (the estimate gates and limits live there) and
// render through the exported renderTaskDates, which owns both editors.

async function loadWithMeta(project) {
  const win = makeWindow({
    fetch: async (url) => {
      const file = path.join(LOCALES_DIR, path.basename(url));
      if (!fs.existsSync(file)) return { ok: false };
      return { ok: true, json: async () => JSON.parse(fs.readFileSync(file, 'utf8')) };
    },
    esc: (s) => String(s),
    fmtDateTime: () => '2026-01-01',
    memberName: () => '—',
    debounced: () => () => {},
    S: { project },
  });
  vm.createContext(win);
  vm.runInContext(readAsClassicScript('../../octbase-shared/i18n.js'), win);
  await win.i18n.setLocale('en');
  vm.runInContext(readAsClassicScript('../../octbase-shared/meta.js'), win);
  // Every file here is an ES module: views-task.js since 37b stage 2, the
  // @octbase/shared modules since stage 3 — all rewritten by the harness so
  // they share one window.
  vm.runInContext(readAsClassicScript('views-task.js'), win);
  return win;
}

const DONE_TASK = { id: 't1', taskType: 'TASK', status: 'DONE', estimateHours: 4, storyPoints: 4,
  createdAt: '2026-01-01T00:00:00Z', updatedAt: '2026-01-01T00:00:00Z' };

function attrOf(html, idOrClass) {
  const m = html.match(new RegExp(`<(?:input|button)[^>]*${idOrClass}[^>]*>`));
  assert.ok(m, `expected an element matching ${idOrClass}`);
  return m[0];
}

test('a DONE task renders the estimate and due-date editors disabled', async () => {
  const win = await loadWithMeta({ id: 'p1', estimationUnit: 'HOURS' });
  const html = win.renderTaskDates(DONE_TASK, true);
  assert.match(attrOf(html, 'id="task-estimate"'), / disabled/);
  assert.match(attrOf(html, 'id="task-due-date"'), / disabled/);
});

test('a live task keeps the estimate and due-date editors enabled', async () => {
  const win = await loadWithMeta({ id: 'p1', estimationUnit: 'HOURS' });
  const html = win.renderTaskDates({ ...DONE_TASK, status: 'IN_PROGRESS' }, false);
  assert.doesNotMatch(attrOf(html, 'id="task-estimate"'), / disabled/);
  assert.doesNotMatch(attrOf(html, 'id="task-due-date"'), / disabled/);
});

test('a DONE task in a points project renders the Fibonacci chips disabled', async () => {
  const win = await loadWithMeta({ id: 'p1', estimationUnit: 'POINTS' });
  const chips = win.renderTaskDates(DONE_TASK, true).match(/<button[^>]*estimate-chip[^>]*>/g);
  assert.ok(chips && chips.length > 0, 'expected Fibonacci chips');
  for (const chip of chips) assert.match(chip, / disabled/);
});

// ── Deleting an attachment updates in place ─────────────────────────────────
// Attachment delete used to re-run renderTaskPanel(), which blanks the panel
// behind a spinner and refetches every panel endpoint — the user reads that
// flash as a page reload, and it discards scroll position and unsaved drafts.
// These tests pin the SPA behavior: the cached payload and the sidebar DOM are
// patched, and nothing is refetched.

// fakeNode is the little DOM surface the sidebar/preview renderers touch.
function fakeNode(overrides = {}) {
  return {
    innerHTML: '',
    dataset: {},
    classList: { contains: () => false, add() {}, remove() {}, toggle() {} },
    querySelector: () => null,
    querySelectorAll: () => [],
    ...overrides,
  };
}

// loadPanel loads views-task.js with a fake attachment API and a fake sidebar
// node, and records every API call so a hidden panel refetch is visible.
async function loadPanel() {
  const calls = [];
  const sidebar = fakeNode();
  const panel = fakeNode();
  const api = {
    attachments: {
      del: async (tid, aid) => { calls.push(`attachments.del(${tid},${aid})`); },
      list: async () => { calls.push('attachments.list'); return []; },
      contentPath: (tid, aid) => `/api/v1/tasks/${tid}/attachments/${aid}/content`,
    },
    tasks: {
      get: async () => { calls.push('tasks.get'); return { id: 't1', status: 'PLANNED' }; },
      activity: async () => { calls.push('tasks.activity'); return []; },
    },
    comments: { list: async () => { calls.push('comments.list'); return []; } },
    branches: { list: async () => { calls.push('branches.list'); return []; } },
    links: { list: async () => { calls.push('links.list'); return []; } },
    relations: { list: async () => { calls.push('relations.list'); return []; } },
  };
  const { win } = await load('en', {
    api,
    // A panel host must exist, or renderTaskPanel() would bail before fetching
    // anything and the "no refetch" assertion below would hold vacuously.
    el: (sel) => (sel === '#att-sidebar-list' ? sidebar : sel === '#task-panel-content' ? panel : null),
    esc: (s) => String(s ?? '').replace(/[&<>"']/g, (c) =>
      ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c])),
    icon: () => '',
    toast: (msg, kind) => { calls.push(`toast:${kind}`); },
    rtSafeHref: () => true,
    apiErrorMessage: (e) => String(e),
    URL: { createObjectURL: () => 'blob:x', revokeObjectURL() {} },
  });
  win.S.taskPanelData = {
    taskId: 't1',
    task: { id: 't1', status: 'PLANNED' },
    attachments: [
      { id: 'a1', filename: 'keep.png', contentType: 'image/png', sizeBytes: 10 },
      { id: 'a2', filename: 'gone.pdf', contentType: 'application/pdf', sizeBytes: 20 },
    ],
  };
  return { win, calls, sidebar };
}

test('deleting an attachment drops it from the cache and repaints the sidebar', async () => {
  const { win, sidebar } = await loadPanel();
  await win.deleteAttachment('t1', 'a2');

  assert.deepStrictEqual(win.S.taskPanelData.attachments.map((a) => a.id), ['a1']);
  assert.match(sidebar.innerHTML, /keep\.png/);
  assert.doesNotMatch(sidebar.innerHTML, /gone\.pdf/);
});

test('deleting an attachment does not refetch the task panel', async () => {
  const { win, calls } = await loadPanel();
  await win.deleteAttachment('t1', 'a2');

  assert.deepStrictEqual(
    calls.filter((c) => c !== 'toast:success'),
    ['attachments.del(t1,a2)'],
    'only the delete call itself should hit the API — a panel refetch is the reload bug',
  );
});

test('deleting the last attachment leaves the sidebar empty state, not a stale row', async () => {
  const { win, sidebar } = await loadPanel();
  await win.deleteAttachment('t1', 'a1');
  await win.deleteAttachment('t1', 'a2');

  assert.deepStrictEqual(win.S.taskPanelData.attachments, []);
  assert.doesNotMatch(sidebar.innerHTML, /keep\.png|gone\.pdf/);
  assert.match(sidebar.innerHTML, /att-empty/);
});

test('a failed attachment delete keeps the cached list intact', async () => {
  const { win, sidebar } = await loadPanel();
  win.api.attachments.del = async () => { throw new Error('boom'); };
  await win.deleteAttachment('t1', 'a2');

  assert.deepStrictEqual(win.S.taskPanelData.attachments.map((a) => a.id), ['a1', 'a2']);
  assert.strictEqual(sidebar.innerHTML, '');
});

// ── Inline images in a rich-text editor ─────────────────────────────────────
// An attachment's bytes need the bearer token, so an <img> pointing straight at
// the content endpoint cannot render — an image inserted into a description or a
// comment showed as a broken icon until it was saved and re-read. The editor now
// DISPLAYS a blob: URL and parks the real path in data-att-path; rtEditorHtml
// puts the path back on the way out.
//
// Getting that second half wrong is silent and destructive rather than merely
// ugly: rtSafeImageSrc rejects blob:, so a persisted blob URL is stripped by the
// sanitizer and the picture disappears on save. These tests pin the round trip.

// fakeEditor models just the DOM rtEditorHtml uses: a node that can be cloned,
// searched for hydrated images, and serialised. innerHTML is a getter that
// rebuilds the markup from the images' current state, which is what a browser
// does for this shape — so a src the code failed to restore shows up in the
// serialisation exactly as it would in the real thing.
function fakeEditor(images) {
  const mkImg = (img) => ({
    dataset: { ...img.dataset },
    src: img.src,
    setAttribute(k, v) { if (k === 'src') this.src = v; },
  });
  const mk = (imgs) => ({
    _imgs: imgs,
    querySelector: (sel) => mk(imgs)._imgs.filter((i) => 'attPath' in i.dataset)[0] || null,
    querySelectorAll: (sel) => imgs.filter((i) => 'attPath' in i.dataset),
    cloneNode: () => mk(imgs.map(mkImg)),
    get innerHTML() {
      return imgs.map((i) => `<img src="${i.src}">`).join('');
    },
  });
  return mk(images.map(mkImg));
}

test('rtEditorHtml saves the attachment path, never the blob URL it displays', async () => {
  const { win } = await load();
  const editor = fakeEditor([
    { src: 'blob:http://x/abc', dataset: { attPath: '/api/v1/tasks/t1/attachments/a1/content', attHydrated: '1' } },
  ]);

  const html = win.rtEditorHtml(editor);
  assert.match(html, /\/api\/v1\/tasks\/t1\/attachments\/a1\/content/);
  assert.doesNotMatch(html, /blob:/, 'a blob: URL would be stripped by the sanitizer, losing the image');
});

test('rtEditorHtml leaves an editor with no hydrated images untouched', async () => {
  const { win } = await load();
  const editor = fakeEditor([{ src: '/api/v1/tasks/t1/attachments/a1/content', dataset: {} }]);

  assert.strictEqual(win.rtEditorHtml(editor), editor.innerHTML);
});

test('rtEditorHtml does not mutate the live editor it reads', async () => {
  const { win } = await load();
  const editor = fakeEditor([
    { src: 'blob:http://x/abc', dataset: { attPath: '/api/v1/tasks/t1/attachments/a1/content', attHydrated: '1' } },
  ]);

  win.rtEditorHtml(editor);
  // The user is still looking at this editor: swapping its src back to the
  // unauthenticated path would blank the image they just inserted.
  assert.match(editor.innerHTML, /blob:/);
});

// ── Editing a comment can attach files ──────────────────────────────────────
// The edit composer mirrored the compose toolbar minus file attach, so an image
// could only be added to a comment while first writing it — correcting a comment
// afterwards had no way to upload at all.

const COMPOSER_STUBS = {
  esc: (v) => String(v ?? '').replace(/[&<>"']/g, (c) =>
    ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c])),
  icon: () => '',
  renderDescriptionHTML: (v) => String(v ?? ''),
};

test('the comment edit composer offers an attach control', async () => {
  const { win } = await load('en', COMPOSER_STUBS);
  const html = win.commentEditEditorHtml({ id: 't1' }, { id: 'c1', text: '<p>hi</p>' });

  assert.match(html, /data-act="rtAttach"/, 'no attach button in the comment edit composer');
  assert.match(html, /type="file"/, 'the attach button has no file input to open');
});

test('the comment edit composer has its own file input, not the composer\'s', async () => {
  const { win } = await load('en', COMPOSER_STUBS);
  const editHtml = win.commentEditEditorHtml({ id: 't1' }, { id: 'c1', text: '<p>hi</p>' });

  // Both composers are on screen at once while a comment is being edited. A
  // shared id would make document.querySelector hand both attach buttons the
  // same input, so one of them would silently fill the wrong editor.
  assert.match(editHtml, /id="rt-file-input-comment-edit"/);
  assert.match(editHtml, /data-a1="#comment-edit-editor"/, 'uploads must target the edit editor');
});

// ── A finished task is still open to discussion ─────────────────────────────
// The panel used to hide the comment composer and every Reply button once a task
// reached DONE/ARCHIVED. Nothing asked for that: the API has no immutability
// check on the comment routes, and immutability is scoped to what a finished
// task *says* (title, description, type, dates, status), not to notes about it.
// The effect was that a task could not be discussed from the moment it was
// finished — exactly when a retrospective remark gets written. These tests pin
// the fix so the gate cannot creep back in behind a refactor.

// DONE_TASK is the shared DONE fixture declared above with the estimate tests.
const DONE_COMMENT = { id: 'c1', authorId: 'u1', text: '<p>hi</p>', createdAt: '2026-07-01T10:00:00Z' };
// The comment thread pulls in a few more render helpers than the composer does.
const COMMENT_STUBS = {
  ...COMPOSER_STUBS,
  avatarHtml: () => '',
  fmtDateTime: () => '1 Jul 2026',
  S: { user: { id: 'u1' }, usersMap: {} },
};

test('a DONE task still offers the comment composer', async () => {
  const { win } = await load('en', COMMENT_STUBS);

  const html = win.renderTaskComments(DONE_TASK, [DONE_COMMENT]);
  assert.match(html, /id="comment-editor"|class="comment-compose"/,
    'the composer is missing on a DONE task — the frontend-only freeze is back');
});

test('a DONE task still offers Reply, Edit and Delete on its comments', async () => {
  const { win } = await load('en', COMMENT_STUBS);

  const html = win.renderTaskComments(DONE_TASK, [DONE_COMMENT]);
  assert.match(html, /data-act="replyComment"/, 'Reply vanished on a DONE task');
  assert.match(html, /data-act="editComment"/, 'Edit vanished on a DONE task');
  assert.match(html, /data-act="deleteComment"/, 'Delete vanished on a DONE task');
});

test('an ARCHIVED task is treated the same as a DONE one', async () => {
  const { win } = await load('en', COMMENT_STUBS);

  const html = win.renderTaskComments({ id: 't1', status: 'ARCHIVED' }, [DONE_COMMENT]);
  assert.match(html, /data-act="replyComment"/, 'Reply vanished on an ARCHIVED task');
});

// ── The Relations tab's dependency map ──────────────────────────────────────
// Direction is the whole point of the map: "blocked by" must sit on the side the
// work flows FROM. The API stores each relation twice (a row and its inverse),
// so a link can arrive from either end and the side has to be read from this
// task's own perspective — inverting that is silent and turns the map into a
// confident lie.

function relWin(win, { projectTasks = [], relations = [] } = {}) {
  win.S.taskPanelData = { projectTasks };
  win.S.project = { id: 'p1' };
  return relations;
}

test('a relation stored on the other task still lands on the correct side', async () => {
  const { win } = await load('en', {
    taskSeqLabel: () => '',
    typeChildOf: () => null,
    STATUS_META: { PLANNED: { label: 'Planned', cls: 'badge-planned' } },
  });
  const task = { id: 't1', title: 'Subject', taskType: 'TASK' };
  // Stored as "t2 BLOCKS t1" — from t1's side that reads "blocked by t2".
  const rows = relWin(win, { relations: [] });
  rows.push({ id: 'r1', sourceTaskId: 't2', targetTaskId: 't1', relationType: 'BLOCKS', _otherTitle: 'Upstream' });

  const { left, right } = win.relationMapSides(task, rows);
  assert.deepStrictEqual(Array.from(left, (n) => n.title), ['Upstream']);
  assert.deepStrictEqual(Array.from(right, (n) => n.title), []);
});

test('what this task blocks sits on the right, what it waits on sits on the left', async () => {
  const { win } = await load('en', {
    taskSeqLabel: () => '',
    typeChildOf: () => null,
    STATUS_META: { PLANNED: { label: 'Planned', cls: 'badge-planned' } },
  });
  const task = { id: 't1', title: 'Subject', taskType: 'TASK' };
  const rows = [
    { id: 'r1', sourceTaskId: 't1', targetTaskId: 't2', relationType: 'BLOCKS', _otherTitle: 'Downstream' },
    { id: 'r2', sourceTaskId: 't1', targetTaskId: 't3', relationType: 'BLOCKED_BY', _otherTitle: 'Upstream' },
    { id: 'r3', sourceTaskId: 't1', targetTaskId: 't4', relationType: 'RELATES_TO', _otherTitle: 'Sibling' },
  ];
  relWin(win, {});

  const { left, right } = win.relationMapSides(task, rows);
  assert.deepStrictEqual(Array.from(left, (n) => n.title), ['Upstream']);
  assert.deepStrictEqual(Array.from(right, (n) => n.title).sort(), ['Downstream', 'Sibling']);
});

test('the map places the parent upstream and children downstream', async () => {
  const { win } = await load('en', {
    taskSeqLabel: () => '',
    typeChildOf: () => 'SUBTASK',
    STATUS_META: { PLANNED: { label: 'Planned', cls: 'badge-planned' } },
  });
  const task = { id: 't1', title: 'Subject', taskType: 'TASK', parentId: 'p-1' };
  relWin(win, {
    projectTasks: [
      { id: 'p-1', title: 'Parent story', taskType: 'STORY', status: 'PLANNED' },
      { id: 'k-1', title: 'Child', taskType: 'SUBTASK', parentId: 't1', status: 'PLANNED' },
    ],
  });

  const { left, right } = win.relationMapSides(task, []);
  assert.deepStrictEqual(Array.from(left, (n) => n.title), ['Parent story']);
  assert.deepStrictEqual(Array.from(right, (n) => n.title), ['Child']);
});

test('the attachment sidebar stamps what it rendered, so a repaint can reuse it', async () => {
  // The details tab re-inserts the previously rendered list node instead of
  // building a new one when this key still matches — that is what stops the
  // thumbnails from being rebuilt (and visibly reloading) on an unrelated edit.
  const { win, sidebar } = await loadPanel();
  await win.deleteAttachment('t1', 'a2');

  assert.match(sidebar.dataset.attKey, /a1/);
  assert.doesNotMatch(sidebar.dataset.attKey, /a2/);
});

// ── Status owns board placement ─────────────────────────────────────────────
// The panel has one control for "where is this task": Status. resolveStatusBoard
// is the whole decision behind that — which board, if any, a status change is
// allowed to move the card on. Getting it wrong is silent in both directions: a
// task that never reaches the board (the bug this replaced), or a task quietly
// enrolled in a running sprint because a status was set.

const LANES = [
  { id: 'c-todo', status: 'PLANNED' },
  { id: 'c-doing', status: 'IN_PROGRESS' },
];

async function boardWin(board) {
  const { win } = await load('en');
  win.S.board = board;
  return win;
}

test('a task on no board joins the ordinary board, so a status change reaches it', async () => {
  const win = await boardWin({ id: 'b1', columns: LANES });
  assert.strictEqual(win.resolveStatusBoard({ id: 't1', boardColumnId: null })?.id, 'b1');
});

test('a card already on the visible board moves between its lanes', async () => {
  const win = await boardWin({ id: 'b1', columns: LANES });
  assert.strictEqual(win.resolveStatusBoard({ id: 't1', boardColumnId: 'c-todo' })?.id, 'b1');
});

test('a card belonging to another board is left alone', async () => {
  // Its lanes are not the ones on screen, so nothing here can place it correctly.
  const win = await boardWin({ id: 'b1', columns: LANES });
  assert.strictEqual(win.resolveStatusBoard({ id: 't1', boardColumnId: 'other-board-col' }), null);
});

test('a status change never enrolls a new task in a sprint board', async () => {
  // The API answers 422 SPRINT_SCOPE_LOCKED for a running sprint; joining a
  // sprint is a planning decision, not a side effect of setting a status.
  const win = await boardWin({ id: 'sb1', isSprintBoard: true, columns: LANES });
  assert.strictEqual(win.resolveStatusBoard({ id: 't1', boardColumnId: null }), null);
});

test('a card already in the sprint still moves between the sprint board lanes', async () => {
  const win = await boardWin({ id: 'sb1', isSprintBoard: true, columns: LANES });
  assert.strictEqual(win.resolveStatusBoard({ id: 't1', boardColumnId: 'c-todo' })?.id, 'sb1');
});

test('no loaded board means no placement, not a crash', async () => {
  const win = await boardWin(null);
  assert.strictEqual(win.resolveStatusBoard({ id: 't1', boardColumnId: null }), null);
});

// ── Completing a container over live work (OCT-300) ─────────────────────────
// The backend deliberately lets a parent go DONE over open children — BLOCKER
// priority is the mechanism for holding one open, and widening that guard
// locked a task out of DONE permanently once. So the warning is a UI
// affordance, and these tests pin the two properties that make it worth having:
// it counts what is genuinely still running (through finished intermediates,
// not counting what the same action is completing), and it never blocks a write
// when it cannot answer.

async function descendantWin({ tasks = [], answer = true, listAll } = {}) {
  const asked = [];
  const { win, warnings } = await load('en', {
    esc: (s) => String(s),
    taskLabel: (task) => `OCT-${task.seq} ${task.title}`,
    confirmModal: async (title, body, label) => { asked.push({ title, body, label }); return answer; },
    api: { tasks: { listAll: listAll || (async () => tasks) } },
    S: { project: { id: 'p1' } },
  });
  return { win, asked, warnings };
}

const TREE = [
  // epic → story (already DONE) → task (open): the shape that hid the original
  // bug, because a one-level check sees only the finished story.
  { id: 'story', seq: 1, title: 'Story', parentId: 'epic', status: 'DONE' },
  { id: 'live', seq: 2, title: 'Live task', parentId: 'story', status: 'IN_PROGRESS' },
  { id: 'shut', seq: 3, title: 'Finished task', parentId: 'story', status: 'DONE' },
  { id: 'elsewhere', seq: 4, title: 'Another branch', parentId: 'other-epic', status: 'PLANNED' },
];

test('open work is found through a finished intermediate, at any depth', async () => {
  const { win } = await descendantWin();
  // Array.from: the module runs in a vm realm, so its arrays are not this
  // realm's Array and deepStrictEqual would compare the prototypes.
  const open = Array.from(win.openDescendantsOf(['epic'], TREE), (t) => t.id);
  assert.deepStrictEqual(open, ['live']);
});

test('a task being completed by the same action is not work left running', async () => {
  // Bulk "set status: Done" over the whole subtree closes everything in it, so
  // warning about members of the selection would be noise.
  const { win } = await descendantWin();
  assert.deepStrictEqual(Array.from(win.openDescendantsOf(['epic', 'live'], TREE)), []);
});

test('another branch of the hierarchy is not counted', async () => {
  const { win } = await descendantWin();
  assert.deepStrictEqual(Array.from(win.openDescendantsOf(['story'], TREE), (t) => t.id), ['live']);
});

test('a parent_id cycle terminates instead of spinning', async () => {
  // Nothing validates that the parent hierarchy is acyclic — the SQL side caps
  // its recursion for the same reason.
  const { win } = await descendantWin();
  const cyclic = [
    { id: 'a', seq: 1, title: 'A', parentId: 'b', status: 'PLANNED' },
    { id: 'b', seq: 2, title: 'B', parentId: 'a', status: 'PLANNED' },
  ];
  assert.deepStrictEqual(Array.from(win.openDescendantsOf(['a'], cyclic), (t) => t.id), ['b']);
});

test('finishing a leaf task asks nothing', async () => {
  const { win, asked } = await descendantWin({ tasks: TREE });
  assert.strictEqual(await win.confirmCompletionOverOpenDescendants(['live']), true);
  assert.deepStrictEqual(asked, []);
});

test('closing a container over live work asks, and names what keeps running', async () => {
  const { win, asked, warnings } = await descendantWin({ tasks: TREE });
  assert.strictEqual(await win.confirmCompletionOverOpenDescendants(['epic']), true);
  assert.strictEqual(asked.length, 1);
  assert.match(asked[0].body, /1 task below this one is still open/);
  assert.match(asked[0].body, /it keeps running/);
  assert.match(asked[0].body, /OCT-2 Live task/);
  assert.strictEqual(asked[0].label, 'Mark done anyway');
  assert.deepStrictEqual(warnings, [], 'i18n warnings while rendering the confirmation');
});

test('declining leaves the caller to abandon the completion', async () => {
  const { win } = await descendantWin({ tasks: TREE, answer: false });
  assert.strictEqual(await win.confirmCompletionOverOpenDescendants(['epic']), false);
});

test('a long list is sampled, and says how many it did not name', async () => {
  const many = Array.from({ length: 6 }, (_, i) =>
    ({ id: `k${i}`, seq: i + 1, title: `Kid ${i}`, parentId: 'epic', status: 'PLANNED' }));
  const { win, asked, warnings } = await descendantWin({ tasks: many });
  await win.confirmCompletionOverOpenDescendants(['epic']);
  assert.match(asked[0].body, /6 tasks below this one are still open/);
  assert.match(asked[0].body, /and 3 more/);
  assert.strictEqual((asked[0].body.match(/OCT-\d/g) || []).length, 3);
  assert.deepStrictEqual(warnings, []);
});

test('a selection of several tasks is addressed as a selection', async () => {
  const { win, asked, warnings } = await descendantWin({ tasks: TREE });
  await win.confirmCompletionOverOpenDescendants(['epic', 'other-epic']);
  assert.match(asked[0].body, /below your selection/);
  assert.deepStrictEqual(warnings, []);
});

test('an unreadable task list lets the write through rather than blocking it', async () => {
  // loadProjectTasks answers [] on failure. The real guard (an open BLOCKER
  // anywhere below) runs server-side, so failing open costs a warning, not a
  // correctness property.
  const { win, asked } = await descendantWin({ listAll: async () => { throw new Error('offline'); } });
  assert.strictEqual(await win.confirmCompletionOverOpenDescendants(['epic']), true);
  assert.deepStrictEqual(asked, []);
});

test('the confirmation reads as German for a German user', async () => {
  const asked = [];
  const { win, warnings } = await load('de', {
    esc: (s) => String(s),
    taskLabel: (task) => `OCT-${task.seq} ${task.title}`,
    confirmModal: async (title, body, label) => { asked.push({ title, body, label }); return true; },
    api: { tasks: { listAll: async () => TREE } },
    S: { project: { id: 'p1' } },
  });
  await win.confirmCompletionOverOpenDescendants(['epic']);
  assert.strictEqual(asked.length, 1);
  assert.match(asked[0].title, /Offene Arbeit/);
  assert.match(asked[0].body, /noch offen/);
  assert.strictEqual(asked[0].label, 'Trotzdem erledigen');
  assert.ok(!asked[0].body.includes('{{'), 'a placeholder was left unfilled');
  assert.deepStrictEqual(warnings, []);
});
