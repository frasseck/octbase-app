import { t } from '@octbase/shared/i18n.js';
import { STATUS_META, TYPE_META, typeChain } from '@octbase/shared/meta.js';
import { prefetchProjectTasks, takeProjectTasks } from './api.js';
import { registerActions } from './delegation.js';
import { el, esc, html, raw } from './framework.js';
import { icon } from './icons.js';
import { Views } from './registry.js';
import { S, taskSeqLabel } from './state.js';
import { openTaskPanel } from './views-task.js';

// Octbase SPA — mindmap view. One ES module among many, bundled by Vite (37b
// stage 2): its top-level declarations are file-private and its public surface
// is the `export { … }` block at the bottom. Imports carry the dependencies —
// there is no load order to keep in step (js/README.md).
//
// Renders the project's task hierarchy (the active chain, up to
// THEME → INITIATIVE → EPIC → STORY → TASK → SUBTASK) as a left-to-right
// mindmap, following each task's parentId. Mid-level tasks without a parent
// collect under synthetic branches ("Stories without epic", …); a child whose
// parent is missing from the live task set (archived, filtered) falls back to
// those branches too, so every open task appears exactly once.
//
// The map shows OPEN work by default — a project's finished tasks outnumber its
// running ones over time, and a mindmap that renders both answers "what did we
// ever do here?" when the question is "what is left?". A toolbar toggle brings
// the done ones back; see mindmapScope for what "open" means and why some done
// tasks are drawn anyway.

// Task ids whose children are hidden (toggled by double-click). Session-local
// view state, like a board's scroll position — deliberately not persisted.
const mmCollapsed = new Set();

// A task is closed once it is DONE or ARCHIVED — the same pair the backend
// calls immutable (workmanagement.IsImmutable). Everything else counts as open,
// custom board-lane statuses included: a lane an admin added is work in flight,
// and only the two built-ins mean "finished".
const mmIsClosed = (task) => task.status === 'DONE' || task.status === 'ARCHIVED';

// mmShowDonePref reads the per-project "show done tasks" preference. Client-side
// (localStorage) like the board's backlog-column toggle: it changes what this
// browser draws, not what the project is, so it needs no backend round-trip.
// Absent key ⇒ off ⇒ open tasks only.
function mmShowDonePref(projectId) {
  try {
    return localStorage.getItem('octbase.mindmap.done.' + projectId) === '1';
  } catch {
    return false;
  }
}

// mindmapScope decides which tasks the map draws.
//
// ARCHIVED is never drawn, in either mode — archived work has its own view, and
// the auto-sweep archives DONE tasks a month on, so including it would refill
// the map with exactly what the archive exists to take out. (Deleted tasks need
// no rule: DELETE /tasks/{id} is a hard delete, so they are already gone.)
//
// With done tasks off, a closed task is still drawn when an open task descends
// from it — a finished epic whose story is still running is the branch that
// story hangs from, and dropping it would strand the story in the synthetic
// "Stories without epic" group, i.e. destroy the very hierarchy the map is for.
// Those retained ancestors come back in `ghosts` so they can be drawn muted.
//
// Returns { tasks, ghosts, hiddenDone } — the pure part of the view, unit-tested
// in views-mindmap.test.js.
function mindmapScope(tasks, showDone) {
  const live = tasks.filter(task => task.status !== 'ARCHIVED');
  if (showDone) return { tasks: live, ghosts: new Set(), hiddenDone: 0 };

  const byId = new Map(live.map(task => [task.id, task]));
  const keep = new Set();
  const ghosts = new Set();
  for (const task of live) {
    if (mmIsClosed(task)) continue;
    keep.add(task.id);
    // Walk up retaining closed ancestors as connectors. An open ancestor stops
    // the walk: it keeps itself (and its own ancestors) on its turn through the
    // outer loop. The seen set makes a cyclic parentId chain terminate rather
    // than hang the render, however it got into the data.
    const seen = new Set([task.id]);
    let parent = byId.get(task.parentId);
    while (parent && !seen.has(parent.id) && mmIsClosed(parent)) {
      seen.add(parent.id);
      keep.add(parent.id);
      ghosts.add(parent.id);
      parent = byId.get(parent.parentId);
    }
  }
  return {
    tasks: live.filter(task => keep.has(task.id)),
    ghosts,
    hiddenDone: live.filter(task => mmIsClosed(task) && !keep.has(task.id)).length,
  };
}

// buildMindmapTree builds the display tree from the tasks' parentId links.
function buildMindmapTree(tasks) {
  const byId = new Map(tasks.map(task => [task.id, task]));
  const byTitle = (a, b) => a.title.localeCompare(b.title);
  const hasLiveParent = (task) => task.parentId && byId.has(task.parentId);

  const childrenOf = new Map(); // parent task id -> [child tasks]
  for (const task of tasks) {
    if (!hasLiveParent(task)) continue;
    if (!childrenOf.has(task.parentId)) childrenOf.set(task.parentId, []);
    childrenOf.get(task.parentId).push(task);
  }
  childrenOf.forEach(list => list.sort(byTitle));

  const taskNode = (task) => {
    const kids = childrenOf.get(task.id) || [];
    const collapsed = mmCollapsed.has(task.id) && kids.length > 0;
    return {
      kind: 'task', task,
      hiddenKids: collapsed ? kids.length : 0,
      children: collapsed ? [] : kids.map(taskNode),
    };
  };

  // The chain's top type roots the map; every level between the top and TASK
  // gets its own "without parent" branch. Parentless tasks/subtasks — and any
  // task whose type is not in the chain (e.g. imported before a level was
  // switched off) — land under "Unlinked".
  const chain = typeChain(S.project);
  const midTypes = chain.slice(1, chain.indexOf('TASK'));
  const children = tasks.filter(task => task.taskType === chain[0]).sort(byTitle).map(taskNode);
  for (const tt of midTypes) {
    const orphans = tasks.filter(task => task.taskType === tt && !hasLiveParent(task)).sort(byTitle);
    if (!orphans.length) continue;
    const label = tt === 'STORY' ? t('mindmap.noEpic') : t('mindmap.noParentType', { type: TYPE_META[tt].label });
    children.push({ kind: 'group', label, children: orphans.map(taskNode) });
  }
  const orphanTasks = tasks.filter(task =>
    (!chain.includes(task.taskType) || task.taskType === 'TASK' || task.taskType === 'SUBTASK')
    && task.taskType !== chain[0] && !hasLiveParent(task)).sort(byTitle);
  if (orphanTasks.length) children.push({ kind: 'group', label: t('mindmap.unlinked'), children: orphanTasks.map(taskNode) });
  return { kind: 'root', label: S.project.name, children };
}

// layoutMindmap assigns each node a column (depth) and a row: leaves stack in
// document order, every parent centres on its children. Returns the flat node
// list plus the parent→child edges and the grid size.
function layoutMindmap(root) {
  const nodes = [], edges = [];
  let nextRow = 0, maxDepth = 0;
  (function place(node, depth) {
    maxDepth = Math.max(maxDepth, depth);
    node.depth = depth;
    if (!node.children.length) {
      node.row = nextRow++;
    } else {
      node.children.forEach(child => { place(child, depth + 1); edges.push([node, child]); });
      node.row = (node.children[0].row + node.children[node.children.length - 1].row) / 2;
    }
    nodes.push(node);
    return node;
  })(root, 0);
  return { nodes, edges, rows: Math.max(nextRow, 1), cols: maxDepth + 1 };
}

const MM_COL_W = 260, MM_ROW_H = 52, MM_NODE_W = 220, MM_NODE_H = 40, MM_PAD = 16;

// mindmapNodeHtml draws one node. `ghosts` holds the ids of done tasks kept
// only to carry their open children (see mindmapScope) — they are dimmed and
// say so in their accessible name, so a muted node reads as "context", not as
// a rendering glitch.
function mindmapNodeHtml(node, ghosts) {
  const x = node.depth * MM_COL_W + MM_PAD;
  const y = node.row * MM_ROW_H + MM_PAD;
  const pos = `left:${x}px;top:${y}px;`;
  if (node.kind === 'root') {
    return `<div class="mm-node mm-root" style="${pos}"><span class="icon" aria-hidden="true">${icon('project', { size: 'md' })}</span><span class="mm-label">${esc(node.label)}</span></div>`;
  }
  if (node.kind === 'group') {
    return `<div class="mm-node mm-group" style="${pos}"><span class="mm-label">${esc(node.label)}</span></div>`;
  }
  const task = node.task;
  const type = TYPE_META[task.taskType] || TYPE_META.TASK;
  const status = STATUS_META[task.status];
  const seq = taskSeqLabel(task);
  const branching = node.children.length > 0 || node.hiddenKids > 0;
  const collapsedHint = node.hiddenKids > 0 ? ` — ${t('mindmap.hiddenChildren', { n: node.hiddenKids })}` : '';
  const ghost = ghosts.has(task.id) ? ` — ${t('mindmap.doneAncestor')}` : '';
  return `
    <button type="button" class="mm-node mm-task${node.hiddenKids ? ' mm-collapsed' : ''}${ghosts.has(task.id) ? ' mm-ghost' : ''}" style="${pos}"
      data-act="mmNodeClick" data-a0="${esc(task.id)}" ${branching ? 'data-a1="1"' : ''}
      aria-label="${esc(`${type.label}: ${task.title}${status ? ` — ${status.label}` : ''}`)}${esc(ghost)}${esc(collapsedHint)}" title="${esc(task.title)}">
      <span class="mm-type ${esc(type.cls)}" aria-hidden="true">${esc(type.sym)}</span>
      ${seq ? `<span class="task-seq">${esc(seq)}</span>` : ''}
      <span class="mm-label">${esc(task.title)}</span>
      ${node.hiddenKids ? `<span class="mm-kids" aria-hidden="true">+${node.hiddenKids}</span>` : ''}
      ${status ? `<span class="mm-status ${esc(status.cls)}" aria-hidden="true"></span>` : ''}
    </button>`;
}

// A node click means two different things: open the task (single click) or
// fold/unfold its subtree (double click). The browser fires click twice before
// dblclick, so a leaf opens immediately while a branching node defers the open
// one beat (mmClickDelay) and treats a second click inside it as the toggle —
// otherwise a double-click would open the panel AND fold the subtree.
const mmClickDelay = 250;
let mmClickTimer = null;

function mmNodeClick(el, ev) {
  const id = el.dataset.a0;
  if (!el.dataset.a1) { openTaskPanel(id); return; }
  if (ev.detail >= 2) {
    clearTimeout(mmClickTimer);
    mmClickTimer = null;
    if (mmCollapsed.has(id)) mmCollapsed.delete(id); else mmCollapsed.add(id);
    renderMindmap();
    return;
  }
  clearTimeout(mmClickTimer);
  mmClickTimer = setTimeout(() => { mmClickTimer = null; openTaskPanel(id); }, mmClickDelay);
}

// mmToggleDone flips the done filter for the current project and re-renders
// from the list the view already fetched — the toggle only changes the filter,
// so it must not cost a network round trip. Folded subtrees survive the flip
// (mmCollapsed is keyed by task id), so turning done tasks on does not undo
// the user's folding.
function mmToggleDone() {
  const pid = S.project && S.project.id;
  if (!pid) return;
  try {
    localStorage.setItem('octbase.mindmap.done.' + pid, mmShowDonePref(pid) ? '0' : '1');
  } catch {}
  renderMindmap(mmLastTasks);
}

// The last task list this view drew, so filter toggles re-render without
// refetching. Never read across projects: renderMindmap always refreshes it
// before drawing.
let mmLastTasks = null;

async function renderMindmap(cached) {
  const gen = S.contentGen;
  const tasks = (cached && S.project) ? cached : await takeProjectTasks(S.project.id);
  mmLastTasks = tasks;
  if (gen !== S.contentGen) return;
  const c = el('#content');
  const showDone = mmShowDonePref(S.project.id);
  const { tasks: live, ghosts, hiddenDone } = mindmapScope(tasks || [], showDone);
  // Nothing at all to map (an empty project, or one whose every task is
  // archived) is the view's own empty state. Nothing to map *because the filter
  // hid it all* is not — that one keeps the toolbar, or the toggle that would
  // bring the work back would be gone with it.
  if (!live.length && !hiddenDone) {
    c.innerHTML = html`<div class="empty"><div class="empty-icon">${raw(icon('mindmap', { size: 'hero' }))}</div><div class="empty-title">${raw(t('mindmap.emptyTitle'))}</div><p>${raw(t('mindmap.hint'))}</p></div>`;
    return;
  }
  const { nodes, edges, rows, cols } = layoutMindmap(buildMindmapTree(live));
  const width = cols * MM_COL_W + MM_PAD;
  const height = rows * MM_ROW_H + MM_PAD;
  const paths = edges.map(([parent, child]) => {
    const x1 = parent.depth * MM_COL_W + MM_PAD + MM_NODE_W;
    const y1 = parent.row * MM_ROW_H + MM_PAD + MM_NODE_H / 2;
    const x2 = child.depth * MM_COL_W + MM_PAD;
    const y2 = child.row * MM_ROW_H + MM_PAD + MM_NODE_H / 2;
    const mx = (x1 + x2) / 2;
    return `<path class="mm-link" d="M ${x1} ${y1} C ${mx} ${y1}, ${mx} ${y2}, ${x2} ${y2}"/>`;
  }).join('');
  // The button label states what a press will do, and carries how many tasks
  // that is — "Show done tasks (59)" is a decision the user can make from the
  // label alone, where a bare "Done" toggle is a question.
  const toggle = `
    <button type="button" class="btn btn-secondary btn-sm mm-done-toggle${showDone ? ' active' : ''}"
      data-act="mmToggleDone" aria-pressed="${showDone ? 'true' : 'false'}">
      ${esc(showDone ? t('mindmap.hideDone')
        : hiddenDone ? t('mindmap.showDoneCount', { n: hiddenDone })
        : t('mindmap.showDone'))}
    </button>`;
  const canvas = live.length
    ? `<div class="mm-scroll" role="group" aria-label="${esc(t('nav.mindmap'))}">
         <div class="mm-canvas" style="width:${width}px;height:${height}px">
           <svg class="mm-links" width="${width}" height="${height}" aria-hidden="true">${paths}</svg>
           ${nodes.map(node => mindmapNodeHtml(node, ghosts)).join('')}
         </div>
       </div>`
    : `<div class="empty"><div class="empty-icon">${icon('mindmap', { size: 'hero' })}</div>
         <div class="empty-title">${esc(t('mindmap.allDoneTitle'))}</div>
         <p>${esc(t('mindmap.allDoneBody'))}</p></div>`;
  c.innerHTML = html`
    <div class="mm-wrap">
      <div class="mm-toolbar">
        <p class="mm-hint">${raw(t('mindmap.hint'))}</p>
        ${raw(toggle)}
      </div>
      ${raw(canvas)}
    </div>`;
}

// ── view registration (see registry.js for the contract) ──
Views.register('mindmap', {
  render: () => renderMindmap(),
  prefetch: pid => prefetchProjectTasks(pid),
  sidebar: { icon: 'mindmap', label: () => t('nav.mindmap'), order: 40 },
});

// ── Delegation registration: this file's handlers ───────────────────────────
// (see js/README.md "Delegation registration".) Both take (el, ev) as they
// come, so they register in the bespoke form rather than through an adapter.
registerActions({ mmNodeClick, mmToggleDone });

export { mindmapScope };
