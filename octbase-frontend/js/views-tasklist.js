import { t } from '@octbase/shared/i18n.js';
import { STATUSES, STATUS_META, priorityNames, typeChain } from '@octbase/shared/meta.js';
import { api, prefetchProjectTasks, takeProjectTasks } from './api.js';
import { FEATURES } from './config.js';
import { _A1, registerActions } from './delegation.js';
import { el, esc, memberName, statusBadge } from './framework.js';
import { icon } from './icons.js';
import { AppPerms } from './permissions.js';
import { Views } from './registry.js';
import { S, applyTaskFilters } from './state.js';
import { backlogRow, renderBacklog } from './views-board.js';
import { applyBulkAction, prependListToolbar, selectAllCheckbox, updateBulkBar } from './views-shell.js';
import { confirmCompletionOverOpenDescendants, openTaskPanel, resolveStatusBoard } from './views-task.js';

// Octbase SPA — split from the former single app.js. One ES module among many,
// bundled by Vite (37b stage 2): its top-level declarations are file-private
// and its public surface is the `export { … }` block at the bottom. Imports
// carry the dependencies — there is no load order to keep in step
// (js/README.md).
//
// ═══════════════════════════════════════════════════════════════════════════
// TASK-LIST ENGINE  +  TASK VIEW
// ═══════════════════════════════════════════════════════════════════════════
// A single, configurable list of tasks that BOTH the Backlog and the Task view
// are built on. Task management is the core of Octbase, so this engine — and the
// task endpoints it reads (`api.tasks.list/status/bulk`) — are FOUNDATIONAL, not
// an optional add-on: the Backlog (`views-board.js`) delegates to it. The only
// thing that is flag-gated is the Task *view* (the sidebar entry + the `/tasks`
// route), via `FEATURES.taskView` (sourced from the backend `GET /config`, which
// reads `OCTBASE_FEATURE_TASKVIEW`). Removing the feature means turning that flag
// off — never deleting this module (that would break the Backlog).
//
// ── The config contract ────────────────────────────────────────────────────
// One config object describes one list; the engine has no view-specific
// branches. Each config provides:
//   listId   — id of the list <div> the focus-preserving search refresh targets
//   scope    — extra applyTaskFilters() options (e.g. {backlogOnly:true}); search
//              is layered on top by the engine
//   cache    — () => the cached, unfiltered task array for this view
//   header   — column header cell labels (after the select-all checkbox)
//   group    — (tasks) => ordered [{key,label,tasks}] groups; empty groups are
//              allowed and render their header (a valid section even when empty)
//   row      — (task) => row HTML (both views reuse backlogRow)
//   emptyState — () => HTML shown when the project has nothing in scope at all
//
// Drag-and-drop is intentionally out of scope for this MVP: status changes are
// made through the task panel or the bulk "Set status" control. The grouping and
// empty-group headers are already shaped so a future `cfg.dnd` can wire drops
// onto status sections without restructuring the engine.

// _taskViewTasks caches the Task view's unfiltered task set (board + backlog),
// so its search box can re-filter in place without refetching or losing focus —
// the same role _backlogTasks plays for the Backlog.
let _taskViewTasks = [];

// taskListBase returns the in-scope tasks ignoring the free-text query — drives
// the choice between the project-empty state and the searchable list.
function taskListBase(cfg) {
  return applyTaskFilters(cfg.cache(), { ...cfg.scope, ignoreSearch: true });
}

// taskListFiltered applies scope AND the current search query.
function taskListFiltered(cfg) {
  return applyTaskFilters(cfg.cache(), { ...cfg.scope });
}

// listGroups resolves the row groups for a render: when the list is sortable and
// a column sort is active, it collapses everything into one flat, sorted group;
// otherwise it defers to the config's own grouping (status for the Task view,
// release for the Backlog). Centralising the sort branch here means every
// sortable config gets column sorting for free (see taskListColumnSort).
function listGroups(cfg, tasks) {
  if (cfg.sortable) {
    const st = listSort(cfg.listId);
    if (st.col) return [{ key: 'sorted', label: '', tasks: [...tasks].sort(taskComparator(st.col, st.dir)) }];
  }
  return cfg.group(tasks);
}

// taskListBody renders the grouped rows, or an inline "no match" message when the
// text search hides everything (the list container + search box stay put so the
// query can be refined or cleared) — mirrors backlogListInner's contract.
function taskListBody(cfg, tasks) {
  if (!tasks.length) return `<div class="backlog-empty-search">${t('search.noTasksMatch')}</div>`;
  // A falsy group label renders no header row — used by the flat, column-sorted
  // mode (one unlabelled group). Grouped modes (status, release) always supply a
  // label, so their headers are unaffected.
  return listGroups(cfg, tasks).map(g => `
    ${g.label ? `<div class="release-label">${g.label}</div>` : ''}
    ${g.tasks.map(task => cfg.row(task)).join('')}
  `).join('');
}

// renderTaskList renders a full list (header + grouped body) into #content from a
// config. Both renderBacklog and renderTaskView call this.
function renderTaskList(cfg) {
  const c = el('#content');
  if (!c) return;
  const base = taskListBase(cfg);
  if (!base.length) { c.innerHTML = cfg.emptyState(); return; }
  const tasks = taskListFiltered(cfg);
  const ids = tasks.map(task => task.id);
  c.innerHTML = `
    <div class="backlog-wrap${cfg.wrapClass ? ' ' + cfg.wrapClass : ''}">
      <div class="backlog-header" role="row">
        <span class="backlog-cell">${selectAllCheckbox(ids)}</span>
        ${cfg.header.map(h => `<span>${h}</span>`).join('')}
      </div>
      <div class="backlog-list" id="${esc(cfg.listId)}">
        ${taskListBody(cfg, tasks)}
      </div>
    </div>`;
}

// refreshTaskList re-filters the cached tasks by the current search query and
// re-renders only the list body, keeping the search input focused.
function refreshTaskList(cfg) {
  const list = el('#' + cfg.listId);
  if (!list) return;
  list.innerHTML = taskListBody(cfg, taskListFiltered(cfg));
}

// ── In-place patching of a rendered list ────────────────────────────────────
// applyBoardTaskUpdate (views-board.js) lets a panel edit repaint one board card
// instead of the whole view. The list views had no equivalent, so on the Backlog,
// the Task view and the Archive every single panel edit — priority, assignee,
// reviewer, release, sprint, due date, type, title — fell through to
// renderContent(): #content blanked to a spinner, api.tasks.list(size:200) ran
// again, and the entire list was rebuilt to change one cell. This is the other
// half of the same fix, sharing its shape (patch the cache, refresh the region).
//
// listPatchTarget resolves the current view's list into the pieces a patch needs,
// or null when there is nothing to patch: a view with no list, a cache that was
// never loaded, or a list that is not actually on screen (the config's empty state
// is showing instead, in which case a fresh row has to change the whole region and
// the caller's renderContent fallback is the right answer).
function listPatchTarget() {
  const def = Views.get(S.view);
  const cfg = def?.listConfig?.();
  const cache = cfg?.cache?.();
  if (!def?.refreshList || !cfg || !Array.isArray(cache)) return null;
  if (!el('#' + cfg.listId)) return null;
  return { def, cache };
}

// applyListTaskUpdate patches the on-screen list's cached row with a server
// snapshot and re-renders just the list body. The refresh runs the view's own
// filters, search and sort over the patched cache (refreshTaskList →
// taskListFiltered), so an edit that takes a task out of the active filter really
// does remove its row, one that brings it in adds it, and a re-sorted column
// re-orders it — the patch changes the data, not the presentation. Returns false
// when no list is in view, so the caller can fall back to renderContent().
function applyListTaskUpdate(taskId, task) {
  const target = listPatchTarget();
  if (!target) return false;
  const i = target.cache.findIndex(t => t.id === taskId);
  if (i >= 0) target.cache[i] = { ...target.cache[i], ...task };
  else target.cache.push(task);
  target.def.refreshList();
  return true;
}

// applyListTaskRemoval is the deletion counterpart: a deleted task leaves no
// snapshot to patch with, so archive/delete drop the row instead of updating it.
function applyListTaskRemoval(taskId) {
  const target = listPatchTarget();
  if (!target) return false;
  const i = target.cache.findIndex(t => t.id === taskId);
  if (i >= 0) target.cache.splice(i, 1);
  target.def.refreshList();
  return true;
}

// openTaskListRow is the click/Enter target for a list row (Backlog + Task view).
// While a bulk selection is active the list is in "selection mode" — the bottom
// action bar is the focus and the task-edit side sheet would hide it — so a row
// click must NOT open the editor. The row only opens the panel when nothing is
// selected; checkboxes (data-act="stop") always toggle selection regardless.
function openTaskListRow(taskId) {
  if (S.selectedTasks.size > 0) return;
  openTaskPanel(taskId);
}

// ── Task view (config #2 — the classic management layer) ────────────────────
// A cross-cutting status overview over the whole project: board AND backlog
// tasks (unlike the Backlog, which is not-on-board only), grouped by status, with
// ARCHIVED excluded (that is the Archive view). Status is the grouping, so the
// toolbar shows no status filter and the view clears any stale status filter from
// the URL. Within a group, order is a stable sort (priority desc, then seq).

// ── Task-list column sorting ────────────────────────────────────────────────
// A sortable list (Task view, Backlog) is grouped by its own default (status /
// release). Clicking a column header switches it to a single flat list sorted by
// that column; clicking the same header cycles ascending → descending → back to
// the default grouping. Each list owns an independent sort state keyed by its
// listId, so the Task view and the Backlog sort separately and neither carries
// the other's sort when you switch views.
const _listSort = {};
function listSort(listId) {
  return _listSort[listId] || (_listSort[listId] = { col: null, dir: 'asc' });
}

// The sortable columns, in header order (the leading select-all box is not a
// column). `key` is the sort key carried in data-a0; `label` is re-read each
// render so a language switch relabels the headers.
const TASK_SORT_COLUMNS = [
  { key: 'type',     label: () => t('task.typeLabel') },
  { key: 'seq',      label: () => '#' },
  { key: 'title',    label: () => t('form.title') },
  { key: 'priority', label: () => t('task.priorityLabel') },
  { key: 'status',   label: () => t('task.statusLabel') },
  { key: 'assignee', label: () => t('task.assignee') },
  { key: 'due',      label: () => t('task.dueDateLabel') },
];

// taskSortValue extracts the comparable value for one column. Returns null for
// "no value" (unassigned, no due date, unknown enum) so the comparator can sort
// those to the end regardless of direction.
function taskSortValue(task, col) {
  switch (col) {
    case 'type': {
      const i = typeChain(S.project).indexOf(task.taskType);
      return i === -1 ? null : i;
    }
    case 'seq':      return task.seqNumber != null ? task.seqNumber : null;
    case 'title':    return (task.title || '').toLowerCase();
    case 'priority': {
      const i = priorityNames(S.priorities).indexOf(task.priority);
      return i === -1 ? null : i;
    }
    case 'status': {
      const i = STATUSES.indexOf(task.status);
      // Custom (board-column) statuses sort after the built-ins, in data order.
      return i === -1 ? STATUSES.length : i;
    }
    case 'assignee': return task.assigneeId ? memberName(task.assigneeId).toLowerCase() : null;
    case 'due':      return task.dueDate || null;
    default:         return null;
  }
}

// taskComparator sorts by one column in the given direction, always pushing
// empty values last and tie-breaking by sequence number so the order is stable.
function taskComparator(col, dir) {
  const mul = dir === 'desc' ? -1 : 1;
  const seq = t => (t.seqNumber || 0);
  return (a, b) => {
    const va = taskSortValue(a, col), vb = taskSortValue(b, col);
    const ea = va === null || va === undefined || va === '';
    const eb = vb === null || vb === undefined || vb === '';
    if (ea && eb) return seq(a) - seq(b);
    if (ea) return 1;
    if (eb) return -1;
    const c = typeof va === 'number' ? va - vb : String(va).localeCompare(String(vb));
    return (c * mul) || (seq(a) - seq(b));
  };
}

// taskSortableHeader builds a sortable list's header cells: each column is a
// button that re-sorts on click and shows an ▲/▼ indicator when it is the active
// sort for that list (`listId` keys the per-list sort state). `trailingCells`
// appends that many empty cells so the header aligns with any per-row action
// columns (the Task view's delete column adds one).
function taskSortableHeader(listId, trailingCells = 0) {
  const st = listSort(listId);
  const cells = TASK_SORT_COLUMNS.map(c => {
    const active = st.col === c.key;
    const arrow = active ? (st.dir === 'asc' ? ' ▲' : ' ▼') : '';
    const label = c.label();
    return `<button type="button" class="th-sort${active ? ' th-sort--active' : ''}"` +
      ` data-act="sortTaskView" data-a0="${esc(c.key)}"` +
      (active ? ` aria-sort="${st.dir === 'asc' ? 'ascending' : 'descending'}"` : '') +
      ` aria-label="${t('task.sortByColumn', { col: label })}">${esc(label)}${arrow}</button>`;
  });
  for (let i = 0; i < trailingCells; i++) cells.push('');
  return cells;
}

// sortTaskView is the header-click handler for the current sortable list (Task
// view or Backlog). First click on a column sorts it ascending; the second flips
// to descending; the third clears the sort and returns to the default grouping.
// It re-renders from the cached task set (no refetch) so sorting is instant, and
// re-inserts the list toolbar (search + filters) that the full-content re-render
// replaces — otherwise sorting would drop those controls.
function sortTaskView(col) {
  const def = Views.get(S.view);
  const cfg = def?.listConfig?.();
  if (!cfg) return;
  const st = listSort(cfg.listId);
  if (st.col === col) {
    if (st.dir === 'asc') st.dir = 'desc';
    else { st.col = null; st.dir = 'asc'; }
  } else {
    st.col = col; st.dir = 'asc';
  }
  // Re-read the config after mutating the sort state: the header cells (with the
  // active ▲/▼ marker) are built eagerly when listConfig() runs, so the render
  // must use a config produced from the new state — not the one captured above.
  renderTaskList(def.listConfig());
  prependListToolbar();
}

// taskRowActions renders the Task view's per-row delete button. Deleting a
// single task goes through deleteTask → api.tasks.del, which returns a clear
// TASK_HAS_CHILDREN error for a parent task (surfaced as a toast) rather than
// the silent no-op the bulk endpoint gives. The button carries its own
// data-act, so the delegation resolves it (not the row's openTaskListRow).
function taskRowActions(task) {
  return `<span class="backlog-cell row-actions">` +
    `<button type="button" class="btn-icon row-delete" data-act="deleteTask"` +
    ` data-a0="${esc(task.id)}" data-a1="${esc(task.title)}"` +
    ` aria-label="${t('task.deleteTaskTitled', { title: esc(task.title) })}"` +
    ` title="${t('form.delete')}">${icon('delete')}</button></span>`;
}

// taskViewStatusGroups is the Task view's default grouping: one section per
// status. Column sorting (when active) is applied by the list engine's
// listGroups before this is reached, so this only handles the grouped mode.
function taskViewStatusGroups(tasks) {
  const builtins = STATUSES.filter(s => s !== 'ARCHIVED');
  // Custom (board-column) statuses are valid too — the backend's statusAllowed
  // accepts any status that exists as a project board column, not just the
  // canonical built-ins. Include any custom status that appears in the data,
  // after the built-ins, so those tasks are grouped and visible rather than
  // silently dropped. statusBadge renders unknown statuses with a neutral badge.
  const custom = [...new Set(tasks.map(t => t.status))]
    .filter(s => s && s !== 'ARCHIVED' && !STATUS_META[s]);
  const order = [...builtins, ...custom];
  const byStatus = new Map(order.map(s => [s, []]));
  tasks.forEach(task => { if (byStatus.has(task.status)) byStatus.get(task.status).push(task); });
  return order.map(status => {
    const rows = byStatus.get(status);
    // Stable sort: highest priority first (built-ins, then customs in their
    // configured order), then sequence number. No within-status reordering
    // exists — this is a status overview, not a ranked board.
    const prioOrder = priorityNames(S.priorities);
    rows.sort((a, b) =>
      (prioOrder.indexOf(b.priority) - prioOrder.indexOf(a.priority)) ||
      ((a.seqNumber || 0) - (b.seqNumber || 0)));
    return {
      key: status,
      label: `${statusBadge(status)} <span class="release-count text-muted">${rows.length}</span>`,
      tasks: rows,
    };
  // When a status filter is active the view is narrowed to one status, so drop
  // the now-empty other group headers; with no filter, empty groups still show
  // so the full status spectrum reads at a glance.
  }).filter(g => !S.filters.status || g.tasks.length > 0);
}

// taskViewStatusFilterOptions builds the <option>s for the Task view's status
// filter: built-ins + custom board-column statuses (ARCHIVED excluded — that's the
// Archive view), reflecting the current selection. Matches the grouping and the
// bulk-status control so every reachable status can be filtered.
function taskViewStatusFilterOptions() {
  const builtins = STATUSES.filter(s => s !== 'ARCHIVED');
  const laneStatuses = [...new Set((S.board?.columns || []).map(c => c.status).filter(Boolean))];
  const custom = laneStatuses.filter(s => !STATUS_META[s] && s !== 'ARCHIVED');
  return [...builtins, ...custom]
    .map(s => `<option value="${esc(s)}" ${S.filters.status===s?'selected':''}>${esc(STATUS_META[s]?.label ?? s)}</option>`).join('');
}

// taskViewBulkStatusOptions lists the statuses the bottom-bar "Set status" control
// offers: the canonical built-ins plus any custom board-column statuses (ARCHIVED
// excluded — archiving is its own action). Mirrors the data model statusAllowed
// enforces on the backend, so the control can target every reachable status.
function taskViewBulkStatusOptions() {
  const builtins = STATUSES.filter(s => s !== 'ARCHIVED');
  const laneStatuses = [...new Set((S.board?.columns || []).map(c => c.status).filter(Boolean))];
  const custom = laneStatuses.filter(s => !STATUS_META[s] && s !== 'ARCHIVED');
  return [...builtins, ...custom]
    .map(s => `<option value="${esc(s)}">${esc(STATUS_META[s]?.label ?? s)}</option>`).join('');
}

// bulkSetStatus applies a status to the current Task-view selection. Status owns
// board placement, so each selected task is first moved to the lane carrying that
// status — including one that was on no board yet, which now joins the board
// instead of taking the new status out of sight in the backlog. resolveStatusBoard
// (views-task.js) makes that decision per task, so the panel and the bulk bar
// cannot drift apart, and neither of them enrolls a task in a running sprint. The
// status update itself still goes through the bulk endpoint; the backend validates
// the value and requires write permission, so this is reconciliation + UX, never
// authorization.
async function bulkSetStatus(status) {
  if (!status || S.bulkInFlight) return;
  // The third completion door (panel status control, Done-lane drop, this one).
  // Asked once for the whole selection, over the union of what sits below it:
  // tasks inside the selection are being completed by this same action, so they
  // don't count as work left running (openDescendantsOf excludes them).
  if (status === 'DONE' && !await confirmCompletionOverOpenDescendants([...S.selectedTasks])) {
    updateBulkBar();   // repaint the bar so its status select drops back to the placeholder
    return;
  }
  const byId = new Map((_taskViewTasks || []).map(tk => [tk.id, tk]));
  const moves = [...S.selectedTasks]
    .map(id => byId.get(id))
    .filter(Boolean)
    .map(tk => {
      const board = resolveStatusBoard(tk);
      const col = board && board.columns.find(c => c.status === status);
      return (col && col.id !== tk.boardColumnId)
        ? api.boards.move(board.id, { taskId: tk.id, boardColumnId: col.id, boardRank: 1000, version: tk.version })
        : null;
    })
    .filter(Boolean);
  if (moves.length) {
    S.bulkInFlight = true;
    try { await Promise.allSettled(moves); }
    finally { S.bulkInFlight = false; }
  }
  await applyBulkAction('set_status', status);
}

function taskViewConfig() {
  // The per-row delete column is added only when the user may delete; without
  // it the Task view keeps the Backlog's column set exactly.
  const canDelete = AppPerms.can('task.delete');
  return {
    listId: 'taskview-list',
    scope: {}, // board + backlog; applyTaskFilters drops ARCHIVED when no status filter
    cache: () => _taskViewTasks,
    sortable: true,
    wrapClass: canDelete ? 'has-row-actions' : '',
    header: taskSortableHeader('taskview-list', canDelete ? 1 : 0),
    group: taskViewStatusGroups,
    row: canDelete ? (task) => backlogRow(task, taskRowActions(task)) : backlogRow,
    emptyState: () => `
      <div class="empty">
        <div class="empty-icon">${icon('sort', { size: 'hero' })}</div>
        <div class="empty-title">${t('task.taskViewEmptyTitle')}</div>
        <p class="empty-body">${t('task.taskViewEmptyBody')}</p>
        ${AppPerms.isReadOnlyProject(S.project) ? '' : `<button class="btn btn-primary" data-act="showCreateTask">${icon('add', { size: 'md' })} ${t('task.create')}</button>`}
      </div>`,
  };
}

async function renderTaskView() {
  // Defence in depth: the sidebar entry and /tasks route are already gated, but
  // never render the management layer when the feature is off.
  if (!FEATURES.taskView) { S.view = 'backlog'; await renderBacklog(); return; }
  const gen = S.contentGen;
  const tasks = await takeProjectTasks(S.project.id);
  if (gen !== S.contentGen) return;
  _taskViewTasks = tasks;
  renderTaskList(taskViewConfig());
}

function refreshTaskViewList() {
  refreshTaskList(taskViewConfig());
}

// ── view registration (see registry.js for the contract) ──
// The Task view is the deployment-gated management layer over the always-on
// list engine above (see the file header): `enabled` removes the sidebar
// entry, the dispatch and the /tasks route (→ Backlog) in one place when
// OCTBASE_FEATURE_TASKVIEW is off — the engine itself is never gated.
Views.register('tasks', {
  render: renderTaskView,
  prefetch: pid => prefetchProjectTasks(pid),
  refreshList: refreshTaskViewList,
  listConfig: taskViewConfig,
  liveRefresh: true,
  listToolbar: true,
  enabled: () => FEATURES.taskView,
  fallback: 'backlog',
  sidebar: { icon: 'sort', label: () => t('nav.tasks'), order: 30 },
  createButton: () => `<button class="btn btn-primary btn-sm" data-act="showCreateTask">${icon('add',{size:'md'})} ${t('task.create')}</button>`,
});

// ── Delegation registration: this file's handlers ───────────────────────────
// (see js/README.md "Delegation registration".)
registerActions([openTaskListRow, sortTaskView], _A1);

export { applyListTaskRemoval, applyListTaskUpdate, bulkSetStatus, refreshTaskList, renderTaskList, taskSortableHeader, taskViewBulkStatusOptions, taskViewStatusFilterOptions };
