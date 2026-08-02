import { i18n, t } from '@octbase/shared/i18n.js';
import { DEFAULT_BOARD_LANE_LIMIT, STATUSES, STATUS_META, boardLaneLimit } from '@octbase/shared/meta.js';
import { api, prefetchProjectTasks, takeProjectTasks } from './api.js';
import { _A0, _A1, registerActions, registerChanges } from './delegation.js';
import { confirmDelete, el, esc, estimateTag, fmtDate, memberName, priorityDot, priorityInline, releaseName, showModal, sprintName, statusBadge, taskLabel, toast, typeBadge, userAvatarHtml } from './framework.js';
import { apiErrorMessage } from './http.js';
import { icon } from './icons.js';
import { AppPerms } from './permissions.js';
import { Views } from './registry.js';
import { S, filterTasksBySearch } from './state.js';
import { setView, taskCheckbox, viewCreateButton } from './views-shell.js';
import { confirmCompletionOverOpenDescendants } from './views-task.js';
import { refreshTaskList, renderTaskList, taskSortableHeader } from './views-tasklist.js';

// Octbase SPA — split from the former single app.js. One ES module among many,
// bundled by Vite (37b stage 2): its top-level declarations are file-private
// and its public surface is the `export { … }` block at the bottom. Imports
// carry the dependencies — there is no load order to keep in step
// (js/README.md).

// Cached, unfiltered task set for the backlog so its search input can
// re-filter the rendered list in place (without refetching or losing input
// focus). Populated by renderBacklog; file-private. The board's counterpart is
// cross-file state and lives on S (S.boardTasks, state.js) — views-content.js
// reads it for board-rank math.
let _backlogTasks = [];

// ── lane paging ──────────────────────────────────────────────
// A lane draws at most S.project.boardLaneLimit cards; the rest load as the
// reader scrolls (or clicks "load more"). This is a rendering cap, not a fetch
// cap: the whole task set is already in memory from the one project-tasks
// request, so paging here buys DOM nodes, not round trips. A Done lane with 800
// finished tasks used to build 800 cards on every repaint — and the board
// repaints on every drag, every SSE patch and every keystroke in the search box.
//
// _laneShown holds the per-column card count the reader has expanded to, keyed
// by column id (BACKLOG_COL for the backlog column, the external column's id for
// a mirrored one). It deliberately survives refreshBoardCards so a drag or an
// incoming SSE patch does not collapse a lane the reader had expanded.
const _laneShown = new Map();
// The counts belong to one search query: filtering changes which cards are in a
// lane, so an expansion made against the unfiltered set means nothing after it.
let _laneShownQuery = '';
let _laneObserver = null;
// Tasks that must be drawn wherever they land, cap or no cap. A card the user
// just put on the board — created into a lane, or dragged in from elsewhere —
// is appended at the end, which on a lane longer than the limit is a position
// the cap does not draw. Nothing would appear, and a create that shows nothing
// reads as a create that failed. So an arriving card is pinned visible until
// the reader navigates away or filters (resetLanePaging clears it).
const _laneReveal = new Set();
// The board the reveals above belong to (see resetLanePaging).
let _laneBoardId = null;

// laneLimit is the project's cap. The defensive read lives in shared/meta.js
// next to estimationUnit, so the board and the settings dialog cannot disagree
// about what an absent value means. 0 means unlimited.
function laneLimit() {
  return boardLaneLimit(S.project);
}

// laneSlice caps `tasks` to what the lane should draw now, and reports how many
// are held back. `full` stays the honest total: the count badge shows the whole
// lane, so a cap can never be mistaken for cards having gone missing.
function laneSlice(colId, tasks) {
  const limit = laneLimit();
  const full = tasks.length;
  if (!limit || full <= limit) return { shown: tasks, hidden: 0, full };
  let shown = Math.min(Math.max(_laneShown.get(colId) || limit, limit), full);
  // Stretch the page to reach a card that must stay visible (see _laneReveal).
  // Only cards past the current page matter, so this is a no-op in the common
  // case and never shrinks the page.
  if (_laneReveal.size) {
    for (let i = shown; i < full; i++) {
      if (_laneReveal.has(tasks[i].id)) shown = i + 1;
    }
  }
  return { shown: tasks.slice(0, shown), hidden: full - shown, full };
}

// laneMoreHtml is the load-more control at the foot of a capped lane. It is a
// real button, not a bare scroll sentinel: scrolling is what triggers it for a
// mouse user, but a keyboard user tabbing through the lane needs something
// focusable and a screen reader needs something that says what is held back
// (WCAG 2.1.1 — the same reasoning as the card's keyboard move path above).
function laneMoreHtml(colId, hidden) {
  if (!hidden) return '';
  return `<button type="button" class="board-lane-more" data-lane-more="${esc(colId)}"
    data-act="loadMoreLane" data-a0="${esc(colId)}">${t('board.loadMoreCards', { count: hidden })}</button>`;
}

// revealBoardCard marks a task as one the cap may not hide. The board's own
// paths (drag, SSE patch) mark their arrivals in patchBoardCaches, but creating
// a task places it through maybePlaceOnBoard (views-content.js) and then
// re-renders the whole view, which never goes near those caches — so that path
// says so explicitly.
function revealBoardCard(taskId) {
  if (taskId) _laneReveal.add(taskId);
}

// loadMoreLane grows one lane by another page and repaints the lanes. The other
// lanes keep their own expansion because _laneShown is keyed per column.
function loadMoreLane(colId) {
  const limit = laneLimit() || DEFAULT_BOARD_LANE_LIMIT;
  _laneShown.set(colId, (_laneShown.get(colId) || limit) + limit);
  refreshBoardCards();
}

// observeLaneSentinels wires the scroll half: each visible load-more button is
// watched, and entering the board's scroll viewport expands its lane. The
// observer is rebuilt after every repaint because the buttons are recreated by
// innerHTML — an observer holding freed nodes would silently stop firing, which
// reads exactly like "lazy loading is broken".
//
// Expanding repaints, which re-observes, which fires again while the new button
// is still on screen: that is the intended loop, and it terminates because each
// pass either removes the button (lane exhausted) or pushes it below the fold.
function observeLaneSentinels() {
  if (typeof IntersectionObserver === 'undefined') return;  // jsdom, older engines: the button still works
  _laneObserver?.disconnect();
  const root = el('.board-cols-scroll');
  if (!root) return;
  _laneObserver = new IntersectionObserver(entries => {
    for (const entry of entries) {
      if (entry.isIntersecting) loadMoreLane(entry.target.dataset.laneMore);
    }
  }, { root, rootMargin: '200px' });
  document.querySelectorAll('.board-lane-more').forEach(node => _laneObserver.observe(node));
}

// resetLanePaging collapses every lane back to the first page — for a new board,
// and for a changed search query (whose lanes hold a different set of cards).
// resetLanePaging collapses every lane back to the first page — for a new board,
// and for a changed search query (whose lanes hold a different set of cards).
//
// The reveal set is NOT cleared here, and that is the whole reason this takes an
// argument. Creating a task ends in renderContent(), which re-renders the board
// from scratch — so clearing the reveals on every render would drop the card
// that was just created before a single frame had drawn it. Reveals are keyed by
// task id, so they stop matching anything once the board changes; `forget` is
// passed by the two callers where that is true.
function resetLanePaging({ forget = false } = {}) {
  _laneShown.clear();
  if (forget) _laneReveal.clear();
  _laneShownQuery = S.filters.q || '';
}

async function renderBoard() {
  const c = el('#content');
  c.classList.add('content-board');

  // Use the cached board only when it is the default board (the sprint-board
  // view caches its own board into S.board, so guard against reusing it here).
  let board = (S.board && !S.board.isSprintBoard) ? S.board : null;
  if (!board) {
    try { board = await api.boards.getDefault(S.project.id); }
    catch { board = null; }
  }

  if(!board) {
    // Auto-create the project's default board, seeded with a localized Scrum
    // column set via the server-side template (owner/admin only).
    board = await api.boards.create(S.project.id, {name:'Main Board', isDefault:true, template:'scrum', locale:i18n.getLocale()});
    board = await api.boards.getDefault(S.project.id);
  }
  await renderBoardInto(board, c);
}

// openSprintBoard targets a specific sprint's board (e.g. the "Plan"/"Open
// board" button on a sprint card) and navigates to the sprint-board view. A
// sprint board exists from sprint creation (PLANNED) until completion, so both
// planned and active sprints can be opened.
async function openSprintBoard(sprintId) {
  S.sprintBoardSprintId = sprintId;
  S.board = null;
  await setView('sprintBoard');
}

// renderSprintBoard renders the board owned by a sprint. The board is created on
// sprint creation and removed on completion, so this view shows the explicitly
// targeted sprint (openSprintBoard), otherwise the running sprint, otherwise the
// next planned sprint.
async function renderSprintBoard() {
  const c = el('#content');
  c.classList.add('content-board');
  const gen = S.contentGen;

  let board = (S.board && S.board.isSprintBoard) ? S.board : null;
  if (!board) {
    const sprints = S.sprints || [];
    const target = (S.sprintBoardSprintId && sprints.find(s => s.id === S.sprintBoardSprintId && s.status !== 'COMPLETED'))
      || sprints.find(s => s.status === 'ACTIVE')
      || sprints.find(s => s.status === 'PLANNED');
    if (target) {
      S.sprintBoardSprintId = target.id;
      const boards = await api.boards.list(S.project.id).catch(() => []);
      const sb = boards.find(b => b.isSprintBoard && b.sprintId === target.id);
      // list() boards carry no columns; fetch the full board to get its lanes.
      if (sb) board = await api.boards.get(sb.id).catch(() => null);
    }
  }
  if (!board) {
    if (gen !== S.contentGen) return;
    c.innerHTML = `<div class="empty"><div class="empty-icon">${icon('sprint',{size:'hero'})}</div><div class="empty-title">${t('board.noSprintBoardTitle')}</div><p>${t('board.noSprintBoardBody')}</p></div>`;
    return;
  }
  await renderBoardInto(board, c);
}

// renderBoardInto renders a resolved board (default or sprint) into the content
// area. Shared by the board and sprint-board views so both behave identically
// (lanes, drag, backlog toggle); the caller decides which board to show.
async function renderBoardInto(board, c) {
  const gen = S.contentGen;
  S.board = board;
  S.showBacklog = backlogPref(board.id);

  // Both card sets go out together. The backlog used to be fetched *after* the
  // task list had come back, which put a second round trip in front of the paint
  // for anyone with the column switched on — the two are independent.
  // takeProjectTasks collects the request the router already started (api.js).
  const tasksP = takeProjectTasks(S.project.id);
  const backlogP = S.showBacklog ? api.backlog.get(S.project.id) : Promise.resolve([]);

  const canManage = AppPerms.canEditTask(S.project);
  const cols = board.columns || [];
  const atMax = cols.length >= (board.maxColumns || 10);
  const atMin = cols.length <= (board.minColumns || 1);

  // First paint: the toolbar and the lanes are drawn from the board object,
  // which is already in hand, so the board's structure goes up now instead of
  // waiting on the cards a round trip away. The caches are cleared for the
  // pending window so nothing reads the previous board's cards (and so an SSE
  // patch arriving mid-load falls back to a full re-render rather than
  // patching a set that is about to be replaced).
  S.boardTasks = null;
  S.boardBacklog = null;
  S.tasksByCol = null;
  // A different board means different lanes; nothing about the previous board's
  // expansion carries over, and a stale entry would silently over-draw a lane
  // that happens to reuse a column id. The reveals are dropped only when the
  // board actually changes — re-rendering the SAME board is what happens right
  // after a create, and must not forget the card it just created.
  const sameBoard = _laneBoardId === board.id;
  resetLanePaging({ forget: !sameBoard });
  _laneBoardId = board.id;
  if (gen !== S.contentGen) return;
  c.innerHTML = `
    ${boardToolbar(board, canManage, atMax)}
    <div class="board-cols-scroll"><div class="board-cols" aria-busy="true">${boardColsInner(board, null, canManage, atMin)}</div></div>`;

  let tasks, backlog;
  try {
    [tasks, backlog] = await Promise.all([tasksP, backlogP]);
  } catch (e) {
    if (gen !== S.contentGen) return;
    // The chrome is already on screen, so the failure is reported into the lanes
    // rather than by throwing to renderContent's handler, which would replace
    // the board that is now partly drawn.
    const wrap = el('.board-cols');
    if (wrap) {
      wrap.removeAttribute('aria-busy');
      wrap.innerHTML = `<div class="board-load-error" role="alert">${esc(apiErrorMessage(e))}</div>`;
    }
    return;
  }
  if (gen !== S.contentGen) return;

  // The board has no type/priority/status filter UI, so it shows every
  // (non-archived) board card; the name filter (S.filters.q) is applied on top.
  // The unfiltered set is cached so the search box can re-filter the lanes in
  // place without refetching.
  S.boardTasks = tasks.filter(task => !!task.boardColumnId && task.status !== 'ARCHIVED');
  // Optional backlog column (left side). Its tasks are not on the board, so they
  // come from their own endpoint, and only when the per-board toggle is on.
  S.boardBacklog = S.showBacklog ? backlog.filter(task => task.status !== 'ARCHIVED') : [];

  const wrap = el('.board-cols');
  if (!wrap) return;
  wrap.removeAttribute('aria-busy');
  refreshBoardCards();
}

// computeBoardTasksByCol indexes tasks by their board column. Own columns are
// seeded so empty lanes still render. External (read-only) columns are not
// indexed here: their tasks are supplied by the server (ec.tasks), since the
// source column may live in another project not loaded into S.boardTasks. Each
// lane is ordered by board rank (drag-sort position), tie-broken by creation
// time so cards without an explicit rank stay stable.
function computeBoardTasksByCol(board, tasks) {
  const tasksByCol = {};
  board.columns?.forEach(col => tasksByCol[col.id] = []);
  tasks.forEach(t => {
    if(t.boardColumnId) (tasksByCol[t.boardColumnId] = tasksByCol[t.boardColumnId] || []).push(t);
  });
  // Pinned cards float to the top of each lane; within the pinned and unpinned
  // groups the usual board-rank (then creation-time) order applies.
  Object.values(tasksByCol).forEach(list => list.sort((a,b) =>
    ((b.pinned?1:0) - (a.pinned?1:0)) ||
    (a.boardRank - b.boardRank) || (a.createdAt < b.createdAt ? -1 : 1)));
  return tasksByCol;
}

// boardColsInner renders the lanes (and external columns) inside .board-cols.
// Split out from renderBoard so the name filter can re-render only this region,
// leaving the toolbar (and its focused search input) untouched.
//
// `tasksByCol` is null while the card sets are still in flight: the lanes are
// drawn from the board alone (names, order, management actions) so the board's
// structure is on screen a round trip before its cards, and the lane bodies stay
// empty until the real cards arrive. Placeholder cards were tried here and read
// as real (empty) tasks rather than as loading, so the pending lane shows
// nothing; the count badge is blanked and the region carries aria-busy.
function boardColsInner(board, tasksByCol, canManage, atMin) {
  const cols = board.columns || [];
  const pending = !tasksByCol;
  const backlog = S.showBacklog ? (pending ? backlogColumnPendingHtml() : backlogColumnHtml(S.boardBacklog || [])) : '';
  return `${backlog}${cols.map(col => {
        const tasks = pending ? [] : (tasksByCol[col.id] || []);
        // The lane draws at most a page of cards; the count badge below still
        // reports `tasks.length`, the whole lane, so the cap never reads as
        // cards having disappeared.
        const page = laneSlice(col.id, tasks);
        // For an empty lane the create-task button floats to the top (right under
        // the header) rather than sitting below the empty drop zone; once the lane
        // has cards it stays at the bottom, after them.
        const addBtn = canManage ? `<button class="col-add-btn" data-act="showCreateTask" data-a0="${esc(col.id)}">${icon('add',{size:'sm'})} ${t('task.addTask')}</button>` : '';
        return `
        <div class="board-col" data-col-id="${esc(col.id)}" data-col-status="${col.status}"
             data-drop-col="${esc(col.id)}">
          <div class="board-col-header">
            <h2 class="board-col-title">${esc(col.name)}</h2>
            ${pending
              ? `<span class="badge badge-muted board-col-count-pending" aria-hidden="true">–</span>`
              : `<span class="badge badge-muted"><span class="sr-only">${t('task.taskCount',{count:tasks.length})}</span><span aria-hidden="true">${tasks.length}</span></span>`}
            ${canManage ? `<div class="board-col-actions">
              <button class="icon-btn" title="${t('board.renameLane')}" aria-label="${t('board.renameLane')}" data-act="renameLane" data-a0="${esc(col.id)}">${icon('edit')}</button>
              <button class="icon-btn" title="${t('board.removeLane')}" aria-label="${t('board.removeLane')}" ${atMin?'disabled':''} data-act="removeLane" data-a0="${esc(col.id)}">${icon('close')}</button>
            </div>` : ''}
          </div>
          ${pending || tasks.length ? '' : addBtn}
          <div class="board-col-tasks" id="col-${col.id}">
            ${pending ? '' : page.shown.map(task => boardCard(task)).join('')}
            ${pending ? '' : laneMoreHtml(col.id, page.hidden)}
          </div>
          ${pending || tasks.length ? addBtn : ''}
        </div>`;}).join('')}
      ${(board.externalColumns||[]).map(ec => externalColumnHtml(ec, ec.tasks||[], canManage)).join('')}`;
}

// backlogColumnPendingHtml is the backlog column's counterpart to a pending
// lane: the same header (so the column does not appear late and shove the lanes
// sideways) over an empty body. It deliberately omits the "backlog is empty"
// hint the loaded column shows — that claim is not known yet.
function backlogColumnPendingHtml() {
  return `
    <div class="board-col board-col-backlog" data-col-backlog="1" data-drop-col="${BACKLOG_COL}">
      <div class="board-col-header">
        <h2 class="board-col-title"><span aria-hidden="true">${icon('backlog',{size:'sm'})}</span> ${t('nav.backlog')}</h2>
        <span class="badge badge-muted board-col-count-pending" aria-hidden="true">–</span>
      </div>
      <div class="board-col-tasks" id="col-${BACKLOG_COL}"></div>
    </div>`;
}

// refreshBoardCards re-filters the cached board tasks by the current name query
// and re-renders only the lanes (with updated counts), keeping the search input
// focused while typing. It is a no-op until the card sets have landed — the
// search box is live during the pending window, and re-rendering from a null
// cache would throw (and would leave the board permanently empty).
function refreshBoardCards() {
  const board = S.board;
  const wrap = el('.board-cols');
  if (!board || !wrap || !Array.isArray(S.boardTasks)) return;
  // A changed query means the lanes hold a different set of cards, so an
  // expansion measured against the old set no longer describes anything. Every
  // other repaint (drag, SSE patch, sidebar edit) deliberately keeps it.
  if ((S.filters.q || '') !== _laneShownQuery) resetLanePaging({ forget: true });
  const tasksByCol = computeBoardTasksByCol(board, filterTasksBySearch(S.boardTasks));
  S.tasksByCol = tasksByCol;
  const canManage = AppPerms.canEditTask(S.project);
  const atMin = (board.columns || []).length <= (board.minColumns || 1);
  wrap.innerHTML = boardColsInner(board, tasksByCol, canManage, atMin);
  observeLaneSentinels();
}

// applyBoardTaskUpdate patches the cached board row for a task with a server
// snapshot (e.g. after an edit in the sidebar changed its assignee, or a status
// change moved it to another lane) and re-renders just the lanes in place — so
// the card updates without a full content repaint (spinner + refetch of the
// whole task list). Every field the card face shows is taken from this cache, so
// one patch is enough for all of them. Returns false when no board is in view,
// so the caller can fall back to renderContent for the view that is actually
// showing (tasklist, backlog, …).
function applyBoardTaskUpdate(taskId, task) {
  return applyBoardTaskUpdates([{ ...task, id: taskId }]);
}

// applyBoardTaskUpdates is the same patch for a batch of snapshots, repainting
// the lanes once at the end — a drag/drop can change more than one card (a move
// plus its status alignment, or a lane re-spread), and each of those repainting
// on its own would flash the board.
function applyBoardTaskUpdates(tasks) {
  if (!boardCachesLoaded()) return false;
  tasks.forEach(task => patchBoardCaches(task.id, task));
  refreshBoardCards();
  return true;
}

// patchBoardCaches applies one server snapshot to the board/backlog caches
// without repainting; the two entry points above own the repaint.
function patchBoardCaches(taskId, task) {
  const i = S.boardTasks.findIndex(t => t.id === taskId);
  const onBoard = !!task.boardColumnId && task.status !== 'ARCHIVED';
  if (i >= 0) {
    // Changing lane re-ranks the card in a lane whose drawn page it may fall
    // outside of, exactly like a fresh arrival — so it is revealed the same way.
    if (onBoard && S.boardTasks[i].boardColumnId !== task.boardColumnId) _laneReveal.add(taskId);
    if (onBoard) S.boardTasks[i] = { ...S.boardTasks[i], ...task };
    else S.boardTasks.splice(i, 1);   // left the board (removed from it or archived)
  } else if (onBoard) {
    S.boardTasks.push(task);          // entered this board
    // A card that just arrived is drawn even if the cap would hide it: it lands
    // at the end of its lane, which on a long lane is past the drawn page.
    _laneReveal.add(taskId);
  }
  applyBacklogColumnUpdate(taskId, task, onBoard);
}

// applyBacklogColumnUpdate mirrors the same patch into the optional backlog
// column, whose cards are drawn by boardCard too — an edit made from the sidebar
// has to reach them as well. Board and backlog are complementary (a task is on
// one or the other), so a card that leaves the board lands here and vice versa.
// New arrivals are appended: the server order is a fetch-time snapshot anyway.
function applyBacklogColumnUpdate(taskId, task, onBoard) {
  if (!S.showBacklog || !Array.isArray(S.boardBacklog)) return;
  const inBacklog = !onBoard && task.status !== 'ARCHIVED';
  const j = S.boardBacklog.findIndex(t => t.id === taskId);
  if (j >= 0) {
    if (inBacklog) S.boardBacklog[j] = { ...S.boardBacklog[j], ...task };
    else S.boardBacklog.splice(j, 1);
  } else if (inBacklog) {
    S.boardBacklog.push(task);
  }
}

// applyBoardTaskRemoval drops a deleted task from the board caches and
// re-renders the lanes in place. Deletion leaves no server snapshot to patch
// with, so it gets its own entry point rather than a synthetic one.
function applyBoardTaskRemoval(taskId) {
  if (!boardCachesLoaded()) return false;
  const i = S.boardTasks.findIndex(t => t.id === taskId);
  if (i >= 0) S.boardTasks.splice(i, 1);
  const j = (S.boardBacklog || []).findIndex(t => t.id === taskId);
  if (j >= 0) S.boardBacklog.splice(j, 1);
  refreshBoardCards();
  return true;
}

// boardCachesLoaded reports whether a board view is on screen with its card set
// in memory — the precondition for patching a card instead of re-rendering.
function boardCachesLoaded() {
  return (S.view === 'board' || S.view === 'sprintBoard') && !!S.board && Array.isArray(S.boardTasks);
}

// boardToolbar renders the board name, optional sprint-timing banner, and the
// lane / external-column / settings management actions (writers only).
function boardToolbar(board, canManage, atMax) {
  let sprintBanner = '';
  if (board.isSprintBoard) {
    const sp = board.sprint;
    if (sp) {
      const dates = (sp.startDate || sp.endDate)
        ? `${sp.startDate?fmtDate(sp.startDate):'—'} → ${sp.endDate?fmtDate(sp.endDate):'—'}`
        : t('sprint.noDatesSet');
      sprintBanner = `<div class="board-sprint-banner" role="status">
        <button type="button" class="sprint-tag sprint-tag-link" data-act="setView" data-a0="sprints" title="${t('nav.sprints')}" aria-label="${t('nav.sprints')}: ${esc(sp.name)}">${icon('sprint',{size:'sm'})} ${esc(sp.name)}</button>
        <span class="board-sprint-dates">${dates}</span>
        <span class="badge badge-muted">${t('sprint.status.'+(sp.status||'PLANNED'))}</span>
      </div>`;
    } else {
      sprintBanner = `<div class="board-sprint-banner" role="status"><span class="sprint-tag">${icon('sprint',{size:'sm'})} ${t('board.sprintEnabled')}</span></div>`;
    }
  }
  return `
    <div class="board-toolbar">
      <div class="board-toolbar-info">
        <span class="board-name">${esc(board.name)}</span>
        ${sprintBanner}
      </div>
      <div class="board-toolbar-controls">
        <label class="sr-only" for="board-search">${t('board.filterByNameLabel')}</label>
        <input type="search" class="form-input form-input-sm board-search-input" id="board-search" value="${esc(S.filters.q || '')}"
          placeholder="${t('board.filterByName')}" aria-label="${t('board.filterByNameLabel')}"
          data-input="setSearchFilter" autocomplete="off">
        <div class="board-toolbar-actions">
          ${viewCreateButton()}
          <button class="btn-icon ${S.showBacklog?'active':''}" data-act="toggleBacklogColumn" title="${t('board.toggleBacklog')}" aria-label="${t('board.toggleBacklog')}" aria-pressed="${S.showBacklog?'true':'false'}">
            ${icon('backlog')}
          </button>
          ${canManage ? `
          <button class="btn-icon" ${atMax?'disabled':''} data-act="addLane" title="${t('board.addLane')}" aria-label="${t('board.addLane')}">
            ${icon('add')}
          </button>
          <button class="btn-icon" data-act="showAddExternalColumn" title="${t('board.addExternalColumn')}" aria-label="${t('board.addExternalColumn')}">
            ${icon('external')}
          </button>
          <button class="btn-icon" data-act="showBoardSettings" title="${t('board.settings')}" aria-label="${t('board.settings')}">
            ${icon('sliders',{size:'md'})}
          </button>` : ''}
        </div>
      </div>
    </div>`;
}

// backlogPref reads the per-board "show backlog column" UI preference. It is a
// client-side preference (localStorage), so it does not need a backend round-trip.
function backlogPref(boardId) {
  return localStorage.getItem('octbase.board.backlog.' + boardId) === '1';
}

// toggleBacklogColumn flips the backlog column on/off for the current board and
// re-renders so the leftmost backlog column appears/disappears. It re-renders the
// board currently in view (default *or* sprint board) rather than always the
// project's default board, so the toggle stays board-sensitive.
async function toggleBacklogColumn() {
  const board = S.board;
  if (!board) return;
  localStorage.setItem('octbase.board.backlog.' + board.id, backlogPref(board.id) ? '0' : '1');
  await renderBoardInto(board, el('#content'));
}

// backlogColumnHtml renders the backlog as a read-from-backlog column pinned to
// the left of the board. Cards reuse boardCard so a writer can drag an item into
// a lane (which moves it onto the board); dropping a lane card back here removes
// it from the board (BACKLOG_COL sentinel, handled in dropOnColumn).
function backlogColumnHtml(list) {
  // When empty, the placeholder floats to the top (right under the header) and
  // carries the same box model as a lane's create-task button, so the backlog
  // column's empty state aligns with the other columns rather than sitting
  // indented and pushed down inside the tasks container.
  const empty = list.length === 0;
  // The backlog column is capped like a lane — it is the one most likely to hold
  // hundreds of rows, since everything not yet on the board sits here.
  const page = laneSlice(BACKLOG_COL, list);
  return `
    <div class="board-col board-col-backlog" data-col-backlog="1" data-drop-col="${BACKLOG_COL}">
      <div class="board-col-header">
        <h2 class="board-col-title"><span aria-hidden="true">${icon('backlog',{size:'sm'})}</span> ${t('nav.backlog')}</h2>
        <span class="badge badge-muted"><span class="sr-only">${t('task.taskCount',{count:list.length})}</span><span aria-hidden="true">${list.length}</span></span>
      </div>
      ${empty ? `<div class="col-empty-hint">${t('task.backlogEmptyTitle')}</div>` : ''}
      <div class="board-col-tasks" id="col-${BACKLOG_COL}">
        ${empty ? '' : page.shown.map(task => boardCard(task)).join('')}
        ${laneMoreHtml(BACKLOG_COL, page.hidden)}
      </div>
    </div>`;
}

// externalColumnHtml renders a read-only column mirroring a column from another
// board (possibly in another project). Cards are not draggable and carry no
// move/remove controls, so tasks cannot be modified through the consuming board.
// When ec.accessible is false the current viewer cannot read the source project,
// so the tasks are withheld and a hint is shown instead.
function externalColumnHtml(ec, list, canManage) {
  // The source label shows the board, plus the project when it differs from ours.
  const crossProject = ec.sourceProjectId && S.project && ec.sourceProjectId !== S.project.id;
  const sourceLabel = crossProject ? `${esc(ec.sourceProjectName)} · ${esc(ec.sourceBoardName)}` : esc(ec.sourceBoardName);
  // A mirrored column is capped too: its cards are supplied by the server, but
  // they cost the same DOM as our own and its source lane can be just as long.
  const page = laneSlice(ec.id, list);
  const body = ec.accessible === false
    ? `<div class="board-col-empty">${t('board.externalNoAccess')}</div>`
    : (list.length
        ? page.shown.map(task => boardCard(task, {readOnly:true})).join('') + laneMoreHtml(ec.id, page.hidden)
        : `<div class="board-col-empty">${t('board.noTasks')}</div>`);
  return `
    <div class="board-col board-col-external" data-external-id="${esc(ec.id)}">
      <div class="board-col-header">
        <div class="board-col-external-titles">
          <h2 class="board-col-title">${esc(ec.sourceColumnName)}</h2>
          <span class="board-col-source" title="${t('board.externalSource',{board:sourceLabel,column:esc(ec.sourceColumnName)})}">${icon('external',{size:'sm'})} ${sourceLabel}</span>
        </div>
        <span class="badge badge-muted">${ec.accessible === false ? '—' : list.length}</span>
        <span class="badge badge-readonly">${t('board.readOnly')}</span>
        ${canManage ? `<div class="board-col-actions">
          <button class="icon-btn" title="${t('board.removeExternalColumn')}" aria-label="${t('board.removeExternalColumn')}" data-act="removeExternalColumn" data-a0="${esc(ec.id)}">${icon('close')}</button>
        </div>` : ''}
      </div>
      <div class="board-col-tasks board-col-tasks-readonly">
        ${body}
      </div>
    </div>`;
}

// readOnly suppresses the drag handle and the per-card write affordances. Two
// things set it: a mirrored external column (whose cards live in another
// project), and an archived project — every move-task/update route answers 409
// PROJECT_ARCHIVED there, so a card that still offers to be dragged is offering
// an error toast. The archived case is decided here rather than at each call
// site so no caller can forget it.
function boardCard(task, {readOnly=false}={}) {
  readOnly = readOnly || AppPerms.isArchivedProject(S.project);
  const seq = task.seqNumber != null ? `<span class="task-seq">${esc(S.project?.abbreviation||S.project?.slug?.toUpperCase()||'')}-${task.seqNumber}</span>` : '';
  // Note: a card's column is already conveyed by the lane it sits in, so the card
  // face shows no status/column dropdown. Keyboard users move a card via the
  // Status select in the task panel — status owns board placement, and move-task
  // adopts the lane's status server-side (WCAG 2.5.7 / 2.1.1 alternative).
  return `
    <div class="board-card ${readOnly?'board-card-readonly':''} ${task.pinned?'board-card-pinned':''}"
         ${readOnly?'':`draggable="true" data-drag-card="${esc(task.id)}"`} data-task-id="${esc(task.id)}"
         role="group" tabindex="0" aria-label="${t('task.openTask',{title:esc(taskLabel(task))})}"
         data-act="openTaskPanel" data-a0="${esc(task.id)}"
         data-keydown="activateOnEnter">
      <div class="card-top">
        ${typeBadge(task.taskType)} ${seq}
        ${/* Assignee avatar and the pin sit together at the top-right, pin left of
              the avatar. Pin floats a card to the top of its lane, so it only
              applies to cards actually on a lane. Backlog cards reuse boardCard
              (draggable, not readOnly) but have no boardColumnId — toggleTaskPin
              can't find them in S.tasksByCol, so the affordance would silently
              no-op there. */''}
        <div class="card-top-right">
          ${readOnly||!task.boardColumnId?'':pinButton(task)}
          ${task.assigneeId ? userAvatarHtml(task.assigneeId) : ''}
        </div>
      </div>
      <div class="card-title">${esc(task.title)}</div>
      <div class="card-meta">
        ${priorityDot(task.priority)}
        ${estimateTag(task)}
        ${task.dueDate ? `<span class="due-tag ${isOverdue(task.dueDate)?'due-overdue':''}" title="${t('task.dueDate',{date:fmtDate(task.dueDate)})}">${icon('time',{size:'sm'})} ${fmtDate(task.dueDate)}</span>` : ''}
        ${task.releaseId ? `<span class="release-tag">${esc(releaseName(task.releaseId))}</span>` : ''}
        ${task.sprintId ? `<span class="sprint-tag">${esc(sprintName(task.sprintId))}</span>` : ''}
      </div>
    </div>`;
}

// pinButton renders the per-card pin toggle. Hidden for members without write
// access (the backend rejects them too). The pinned state drives the icon's
// active styling and the aria-pressed / label text.
function pinButton(task) {
  if (!AppPerms.canEditTask(S.project)) return '';
  const pinned = !!task.pinned;
  const label = pinned ? t('task.unpin') : t('task.pin');
  return `<button type="button" class="card-pin-btn${pinned?' pinned':''}" data-act="toggleTaskPin" data-a0="${esc(task.id)}" title="${label}" aria-label="${label}" aria-pressed="${pinned}">${icon('pin',{size:'sm'})}</button>`;
}

// toggleTaskPin flips a card's pinned flag, then re-sorts the lanes in place so
// the card jumps to (or leaves) the top of its lane without a full board reload.
async function toggleTaskPin(taskId) {
  const task = findBoardTask(taskId);
  if (!task) return;
  const pinned = !task.pinned;
  try {
    await api.tasks.setPin(taskId, pinned);
  } catch (e) {
    toast(apiErrorMessage(e), 'error');
    return;
  }
  task.pinned = pinned;
  const cached = (S.boardTasks || []).find(t => t.id === taskId);
  if (cached) cached.pinned = pinned;
  refreshBoardCards();
}

function isOverdue(dateStr) {
  if (!dateStr) return false;
  try { return new Date(dateStr) < new Date(new Date().toDateString()); } catch { return false; }
}

const BOARD_RANK_STEP = 1000;

// boardRank reads a card's sort position defensively: cards that predate board
// ranking (or that arrived without one) sort as 0, which is where
// computeBoardTasksByCol already places them.
function boardRank(task) {
  return Number.isFinite(task?.boardRank) ? task.boardRank : 0;
}

// rankBetween returns a rank that sorts strictly between two adjacent cards —
// the point of the gaps BOARD_RANK_STEP leaves between them. Either neighbour
// may be absent (dropping at the top or bottom of a lane, or into an empty one).
// Returns null when repeated inserts have squeezed the neighbours together until
// no integer fits, which tells the caller to re-spread the lane instead.
function rankBetween(before, after) {
  const lo = before ? boardRank(before) : 0;
  const hi = after ? boardRank(after) : lo + 2 * BOARD_RANK_STEP;
  if (hi - lo < 2) return null;
  return Math.floor((lo + hi) / 2);
}

// Sentinel data-drop-col value for the backlog column. Real lanes use their (UUID)
// column id, so this can't collide; dropOnColumn special-cases it to remove the
// dropped card from the board rather than reorder it within a lane.
const BACKLOG_COL = '__backlog__';

// ── Drag geometry ───────────────────────────────────────────────────────────
// dragover fires continuously — tens of events a second, for the whole duration
// of a drag. It used to build the lane's card array TWICE per event (once in
// boardDragOver, once more inside the boardDropIndex it called) and read
// getBoundingClientRect for every card in the lane. Because the previous event
// had written a classList, the first of those reads flushed a synchronous layout
// recalculation: a 100-card lane cost on the order of 6,000 rect reads a second
// and a relayout per event.
//
// The lane is measured ONCE per drag into _dragGeom instead, and each dragover
// binary-searches the pointer against those midpoints. Card measurements are
// stored in the lane's own content space (viewport offset removed), so the
// snapshot survives the two things that legitimately move cards under the
// pointer mid-drag without any card being remeasured: the lane scrolling (drag
// auto-scroll near an edge — the reason the naive "cache clientY midpoints"
// version is wrong) and the lane itself moving on screen.
//
// Two reads of the CONTAINER (its rect top and scrollTop, one layout flush) are
// therefore still done per event to place the pointer in that space. That is
// O(1) in the lane's size instead of O(cards) — the point of the exercise — and
// keeping it is what makes scrolling during a drag come out right.
let _dragGeom = null;

// invalidateDragGeom drops the snapshot. Called from the document-level
// dragstart/dragend handlers (framework.js) so a drag never inherits the
// previous one's measurements.
function invalidateDragGeom() {
  _dragGeom = null;
}

// laneOrigin is the viewport y that the lane's content-space y=0 sits at right
// now, so `clientY - laneOrigin(lane)` converts a pointer into the space the
// snapshot's midpoints are in.
function laneOrigin(container) {
  return container.getBoundingClientRect().top - container.scrollTop;
}

// laneGeom returns the measured card midpoints for one lane, measuring on first
// use per drag. The snapshot is re-measured when the lane element, the dragged
// card or the lane's child count changes, which covers a lane whose contents are
// rebuilt or re-ordered mid-drag (refreshBoardCards replaces the lane nodes
// outright, so that case is caught by the element identity).
function laneGeom(container, draggedId) {
  if (_dragGeom && _dragGeom.container === container && _dragGeom.draggedId === draggedId
      && _dragGeom.childCount === container.childElementCount) {
    return _dragGeom;
  }
  const origin = laneOrigin(container);
  const cards = [];
  const mids = [];
  container.querySelectorAll('.board-card').forEach(card => {
    if (card.dataset.taskId === draggedId) return;
    const r = card.getBoundingClientRect();
    cards.push(card);
    mids.push(r.top + r.height / 2 - origin);
  });
  _dragGeom = { container, draggedId, childCount: container.childElementCount, cards, mids };
  return _dragGeom;
}

// dropSlot is the insertion index for a pointer at lane-space `y`: the first card
// whose midpoint the pointer is above, or the end of the lane when it is below
// them all (dropping into the empty area under the last card, or into an empty
// lane, both land there). Cards run top to bottom in DOM order, so the midpoints
// ascend and a binary search finds the same slot the old linear scan did.
function dropSlot(mids, y) {
  let lo = 0, hi = mids.length;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if (y < mids[mid]) hi = mid;
    else lo = mid + 1;
  }
  return lo;
}

// boardDropIndex returns the insertion index within a column's card list for a
// pointer at clientY, ignoring the card currently being dragged.
function boardDropIndex(container, draggedId, clientY) {
  const geom = laneGeom(container, draggedId);
  return dropSlot(geom.mids, clientY - laneOrigin(container));
}

// boardDragOver shows a drop indicator at the position the card would land. The
// lane is measured once per drag (see above) and the DOM is touched only when the
// indicator position actually changes.
function boardDragOver(ev, colId) {
  ev.preventDefault();
  if (!S.dragging) return;
  const container = el('#col-' + colId);
  if (!container) return;
  const geom = laneGeom(container, S.dragging);
  const count = geom.cards.length;
  const index = dropSlot(geom.mids, ev.clientY - laneOrigin(container));
  const target = index < count ? geom.cards[index] : (count ? geom.cards[count - 1] : null);
  const cls = target ? (index < count ? 'drop-before' : 'drop-after') : 'drop-empty';
  // Skip redundant DOM writes while the pointer stays in the same slot.
  if (S._dropTarget === target && S._dropCls === cls && S._dropCol === container) return;
  clearDropIndicators();
  if (target) target.classList.add(cls);
  else container.classList.add('drop-empty');
  S._dropTarget = target; S._dropCls = cls; S._dropCol = container;
}

function boardDragLeave(ev) {
  // Only clear when leaving the column entirely, not when crossing child cards.
  if (ev.currentTarget.contains(ev.relatedTarget)) return;
  clearDropIndicators();
}

// clearDropIndicators removes the indicator from the nodes it was put on. Those
// are already tracked (boardDragOver records both, and is the only thing that
// sets these classes), so the two document-wide querySelectorAll sweeps this used
// to run — across every card of every lane, on every indicator move — bought
// nothing. Removing a class from a node a mid-drag rebuild has since detached is
// harmless, and the rebuilt lanes carry no indicator to begin with.
function clearDropIndicators() {
  if (S._dropTarget) S._dropTarget.classList.remove('drop-before', 'drop-after');
  if (S._dropCol) S._dropCol.classList.remove('drop-empty');
  S._dropTarget = null; S._dropCls = null; S._dropCol = null;
}

function findBoardTask(taskId) {
  for (const list of Object.values(S.tasksByCol || {})) {
    const t = list.find(x => x.id === taskId);
    if (t) return t;
  }
  return null;
}

// dropOnColumn places the dragged task at the drop position within colId and
// persists the new order by reassigning evenly-spaced board ranks. The status
// follows the lane server-side (move-task adopts the target lane's status), so
// no separate status write is needed here.
async function dropOnColumn(ev, colId) {
  ev.preventDefault();
  ev.stopPropagation();
  clearDropIndicators();
  if (!S.dragging || !S.board) return;
  const taskId = S.dragging;
  S.dragging = null;

  // The card may come from a lane (already on the board) or from the optional
  // backlog column (not yet on the board) — dropping a backlog card onto a lane
  // moves it onto the board, so look in both sources.
  const moved = findBoardTask(taskId) || (S.boardBacklog || []).find(t => t.id === taskId);
  if (!moved) { await rerenderBoardView(); return; }

  // Dropping onto the backlog column takes the card off the board (back to the
  // backlog). A card that is already in the backlog has no lane to leave, so the
  // drop is a no-op — and a no-op must not repaint anything.
  if (colId === BACKLOG_COL) {
    if (!moved.boardColumnId) return;
    let snapshot;
    try {
      snapshot = await api.boards.remove(S.board.id, taskId);
    } catch (e) {
      // A refusal (an immutable card, a stale version) leaves the server state
      // unknown to us, so this is the one path that still refetches — the
      // rerender is what snaps the card back to the lane it came from.
      toast(apiErrorMessage(e), 'error');
      await rerenderBoardView();
      return;
    }
    // remove-task answers with the updated task, so the card can cross from its
    // lane into the backlog column from that snapshot — the same in-place patch
    // the lane drops below take. This used to re-render unconditionally, which
    // refetched the project's whole task list and reset the board (scroll
    // position, the search box's focus) for a move the server had already
    // described in its response.
    if (!snapshot || !applyBoardTaskUpdates([snapshot])) await rerenderBoardView();
    return;
  }

  // Crossing into a Done lane completes the task, so it is one of the three
  // doors that has to warn about live work underneath (views-task.js,
  // confirmCompletionOverOpenDescendants). Nothing has moved on screen yet — the
  // card is repainted from the write's response — so declining just returns and
  // the card stays in the lane it was dragged from.
  const target = (S.board.columns || []).find(c => c.id === colId);
  if (target?.status === 'DONE' && moved.status !== 'DONE'
      && !await confirmCompletionOverOpenDescendants([taskId])) return;

  const container = el('#col-' + colId);
  const index = container ? boardDropIndex(container, taskId, ev.clientY) : 0;

  // The lane as it stands without the dragged card: `index` is an insertion
  // point into *this* list, so rest[index-1] and rest[index] are the cards the
  // dropped one lands between.
  const rest = (S.tasksByCol[colId] || []).filter(t => t.id !== taskId);

  // Only the dragged card actually changed place, so only it needs a new rank —
  // slotted between its new neighbours, which is what the BOARD_RANK_STEP gaps
  // are for. Re-ranking the whole lane made every drop cost one PATCH per card
  // in the target column, so dropping into a busy lane was the slowest thing on
  // the board.
  //
  // Each move carries the version of the board snapshot the drag was based on:
  // if someone else moved one of these cards in the meantime, that write 409s
  // instead of silently overwriting, and the rerender below shows their state.
  const rank = rankBetween(rest[index - 1], rest[index]);
  let updates;
  if (rank === null) {
    // Ranks here have been squeezed together until nothing fits between the drop's
    // neighbours. Re-spread the whole lane, which restores the gaps for later
    // drops — rare, and self-healing.
    const target = rest.slice();
    target.splice(index, 0, moved);
    updates = [];
    target.forEach((t, i) => {
      const r = (i + 1) * BOARD_RANK_STEP;
      if (t.boardColumnId !== colId || boardRank(t) !== r) {
        updates.push({ taskId: t.id, rank: r, version: t.version });
      }
    });
  } else if (moved.boardColumnId === colId && boardRank(moved) === rank) {
    updates = [];   // dropped back exactly where it already was
  } else {
    updates = [{ taskId, rank, version: moved.version }];
  }
  if (!updates.length) { refreshBoardCards(); return; }

  let results;
  try {
    results = await Promise.all(updates.map(u =>
      api.boards.move(S.board.id, { taskId: u.taskId, boardColumnId: colId, boardRank: u.rank, version: u.version })));
  } catch (e) {
    // Crossing lanes is a status change, so this now surfaces real refusals
    // too: a DONE/ARCHIVED card refuses to leave its lane (TASK_IMMUTABLE)
    // and a card with an open BLOCKER child refuses the Done lane
    // (TASK_HAS_BLOCKER) — the rerender snaps the card back.
    toast(apiErrorMessage(e), 'error');
    await rerenderBoardView();
    return;
  }

  const snapshots = results.filter(Boolean);
  // Repaint from the snapshots those writes just returned rather than refetching
  // the project's whole task list: the server has already said exactly what
  // changed, so the extra round trip only sat between the drop and the card
  // landing. Fall back to a full re-render if the board went off screen mid-drop.
  if (!applyBoardTaskUpdates(snapshots)) await rerenderBoardView();
}

// ═══════════════════════════════════════════════════════════
// BOARD CONFIGURATION — lanes, settings, external columns
// ═══════════════════════════════════════════════════════════

// rerenderBoardView re-renders whichever board the current view owns. Board
// mutations (drops, lane edits) persist against S.board.id, which may be the
// running sprint's board, so re-rendering must stay on that board rather than
// always falling back to the default board (renderBoard discards a cached
// sprint board, so calling it from the sprint-board view would snap to main).
async function rerenderBoardView() {
  if (S.view === 'sprintBoard') { await renderSprintBoard(); return; }
  await renderBoard();
}

// reloadBoard clears the cached board (forcing a fresh fetch of columns,
// external columns and linked sprint) and re-renders the current board view.
// Used after any board mutation that changes board structure.
async function reloadBoard() {
  S.board = null;
  await rerenderBoardView();
}

// addLane creates a new column. A lane's name *is* its task status: the built-in
// statuses are offered as localized name suggestions, but any custom name can be
// typed. A name matching a built-in — by label, status code, or a template lane
// name in either locale — maps back to that status code (so DONE semantics
// still apply); otherwise the typed name becomes the status. ARCHIVED is
// excluded: the board hides archived cards, so an ARCHIVED lane could only ever
// swallow whatever is dropped into it. The status must be unique on the board
// (backend enforces this).
function addLane() {
  const cols = S.board?.columns || [];
  if (cols.length >= (S.board?.maxColumns || 10)) { toast(t('board.atMaxLanes'), 'error'); return; }
  const builtins = laneBuiltins();
  const usedStatus = cols.map(c => c.status);
  const suggestions = builtins.filter(o => !usedStatus.includes(o.status));
  showModal(t('board.addLane'), `
    <div class="form-group"><label class="form-label" for="lane-name">${t('board.laneName')}</label>
      <input class="form-input" id="lane-name" list="lane-name-options" placeholder="${t('board.laneNamePlaceholder')}" autocomplete="off" autofocus>
      <datalist id="lane-name-options">
        ${suggestions.map(o=>`<option value="${esc(o.label)}"></option>`).join('')}
      </datalist>
      <p class="form-hint">${t('board.laneNameHint')}</p>
    </div>`,
    async () => {
      const name = el('#lane-name')?.value?.trim();
      if (!name) { const e = new Error(t('board.laneNameRequired')); e.field = 'lane-name'; throw e; }
      const match = laneBuiltinFor(name);
      const status = match ? match.status : name;
      await api.boards.addColumn(S.board.id, { name, status, position: cols.length });
      toast(t('board.laneAdded'), 'success');
      await reloadBoard();
    });
}

// The seeded template lane names per built-in status, both locales. A user
// re-typing a template lane name ("To Do", "Zu erledigen") expects that lane's
// built-in semantics back, not a lookalike custom status — and the mapping must
// not depend on which language happens to be active.
const TEMPLATE_LANE_NAMES = {
  PLANNED: ['to do', 'zu erledigen'],
  IN_PROGRESS: ['in progress', 'in arbeit'],
  IN_REVIEW: ['in review', 'in prüfung'],
  DONE: ['done', 'erledigt'],
};

// laneBuiltins lists the built-in statuses offerable as lanes (ARCHIVED is
// excluded — the board hides archived cards) with the names that map to each:
// the status code, the active locale's label, and the template lane names.
function laneBuiltins() {
  return STATUSES.filter(s => s !== 'ARCHIVED').map(s => ({
    status: s,
    label: STATUS_META[s].label,
    aliases: [s.toLowerCase(), STATUS_META[s].label.toLowerCase(), ...(TEMPLATE_LANE_NAMES[s] || [])],
  }));
}

// laneBuiltinFor maps a typed lane name to its built-in status, or null when
// the name is genuinely custom.
function laneBuiltinFor(name) {
  const needle = name.toLowerCase();
  return laneBuiltins().find(o => o.aliases.includes(needle)) || null;
}

// renameLane edits a lane's display name. A built-in lane (PLANNED, DONE, …)
// keeps its status — the rename is display-only, so e.g. "Done" renamed to
// "Shipped" keeps DONE semantics. A custom lane's name *is* its status (see
// addLane), so the rename carries the status along and the backend retags the
// lane's tasks; without that, the lane header and the status badges of the
// tasks inside it would diverge permanently.
function renameLane(colId) {
  const col = (S.board?.columns||[]).find(c => c.id === colId);
  if (!col) return;
  showModal(t('board.renameLane'), `
    <div class="form-group"><label class="form-label" for="lane-name">${t('board.laneName')}</label>
      <input class="form-input" id="lane-name" value="${esc(col.name)}" autofocus></div>`,
    async () => {
      const name = el('#lane-name')?.value?.trim();
      if (!name) { const e = new Error(t('board.laneNameRequired')); e.field = 'lane-name'; throw e; }
      const patch = { name, version: col.version };
      if (!STATUSES.includes(col.status)) patch.status = name;
      await api.boards.updateColumn(S.board.id, colId, patch);
      toast(t('board.laneRenamed'), 'success');
      await reloadBoard();
    });
}

// removeLane deletes a lane, honouring the board's minimum-lane limit (the
// backend rejects with BOARD_MIN_LANES, surfaced as a toast).
function removeLane(colId) {
  const col = (S.board?.columns||[]).find(c => c.id === colId);
  if (!col) return;
  confirmDelete(t('board.removeLane'), t('board.removeLaneConfirm', { name: esc(col.name) }), async () => {
    await api.boards.deleteColumn(S.board.id, colId);
    toast(t('board.laneRemoved'), 'success');
    await reloadBoard();
  }, null, t('board.removeLane'));
}

// showBoardSettings configures the board name, lane limits (1–10) and Scrum
// sprint linkage.
function showBoardSettings() {
  const b = S.board;
  if (!b) return;
  const sprints = (S.sprints||[]).filter(s => s.status !== 'COMPLETED');
  showModal(t('board.settings'), `
    <div class="form-group"><label class="form-label" for="bs-name">${t('board.name')}</label>
      <input class="form-input" id="bs-name" value="${esc(b.name)}" autofocus></div>
    <div class="form-row">
      <div class="form-group"><label class="form-label" for="bs-min">${t('board.minLanes')}</label>
        <input class="form-input" id="bs-min" type="number" min="1" max="10" value="${b.minColumns||1}"></div>
      <div class="form-group"><label class="form-label" for="bs-max">${t('board.maxLanes')}</label>
        <input class="form-input" id="bs-max" type="number" min="1" max="10" value="${b.maxColumns||10}"></div>
    </div>
    <div class="form-group">
      <label class="form-check"><input type="checkbox" id="bs-sprint" ${b.isSprintBoard?'checked':''} data-change="toggleSprintRow"> ${t('board.sprintBoard')}</label>
    </div>
    <div class="form-group${b.isSprintBoard?'':' hidden'}" id="bs-sprint-row">
      <label class="form-label" for="bs-sprint-id">${t('board.linkedSprint')}</label>
      <select class="form-select" id="bs-sprint-id">
        <option value="">${t('board.noSprint')}</option>
        ${sprints.map(s=>`<option value="${s.id}" ${b.sprintId===s.id?'selected':''}>${esc(s.name)}</option>`).join('')}
      </select>
    </div>`,
    async () => {
      const name = el('#bs-name')?.value?.trim();
      if (!name) { const e = new Error(t('board.nameRequired')); e.field = 'bs-name'; throw e; }
      const min = parseInt(el('#bs-min')?.value, 10);
      const max = parseInt(el('#bs-max')?.value, 10);
      if (!(min >= 1 && min <= 10) || !(max >= 1 && max <= 10) || min > max) {
        const e = new Error(t('board.limitsInvalid')); e.field = 'bs-min'; throw e;
      }
      const isSprint = !!el('#bs-sprint')?.checked;
      const sprintId = isSprint ? (el('#bs-sprint-id')?.value || null) : null;
      await api.boards.update(b.id, { name, minColumns: min, maxColumns: max, isSprintBoard: isSprint, sprintId, version: b.version });
      toast(t('board.settingsSaved'), 'success');
      await reloadBoard();
    }, null, { minColumns: 'bs-min', maxColumns: 'bs-max', sprintId: 'bs-sprint-id' });
}

// showAddExternalColumn lets the user mirror a column from any board they can
// read as a read-only column — including boards in other projects they have
// access to. Columns are grouped by project · board.
async function showAddExternalColumn() {
  let projects;
  try { projects = await api.projects.list(); }
  catch (e) { toast(apiErrorMessage(e), 'error'); return; }

  // Gather every board across all readable projects (carrying the project name).
  const boardLists = await Promise.all(projects.map(p =>
    api.boards.list(p.id).then(bs => bs.map(b => ({ ...b, projectName: p.name }))).catch(() => [])));
  const others = boardLists.flat().filter(bd => bd.id !== S.board.id);
  if (!others.length) { toast(t('board.noOtherBoards'), 'error'); return; }

  // Resolve each board's columns so the picker can list them.
  const detailed = await Promise.all(others.map(bd =>
    api.boards.get(bd.id).then(full => ({ ...full, projectName: bd.projectName })).catch(() => null)));
  const referenced = new Set((S.board.externalColumns||[]).map(ec => ec.sourceColumnId));
  const optgroups = detailed.filter(Boolean).map(bd => {
    const opts = (bd.columns||[])
      .filter(col => !referenced.has(col.id))
      .map(col => `<option value="${col.id}">${esc(col.name)}</option>`).join('');
    const label = bd.projectId === S.project.id ? bd.name : `${bd.projectName} · ${bd.name}`;
    return opts ? `<optgroup label="${esc(label)}">${opts}</optgroup>` : '';
  }).join('');
  if (!optgroups) { toast(t('board.noColumnsToAdd'), 'error'); return; }

  showModal(t('board.addExternalColumn'), `
    <p class="text-muted text-sm">${t('board.externalColumnHint')}</p>
    <div class="form-group"><label class="form-label" for="ext-col">${t('board.sourceColumn')}</label>
      <select class="form-select" id="ext-col" autofocus>${optgroups}</select></div>`,
    async () => {
      const sourceColumnId = el('#ext-col')?.value;
      if (!sourceColumnId) { const e = new Error(t('board.sourceColumnRequired')); e.field = 'ext-col'; throw e; }
      await api.boards.addExternalColumn(S.board.id, { sourceColumnId, position: (S.board.externalColumns||[]).length });
      toast(t('board.externalColumnAdded'), 'success');
      await reloadBoard();
    });
}

// removeExternalColumn drops a cross-board read-only column reference.
function removeExternalColumn(extId) {
  confirmDelete(t('board.removeExternalColumn'), t('board.removeExternalColumnConfirm'), async () => {
    await api.boards.delExternalColumn(S.board.id, extId);
    toast(t('board.externalColumnRemoved'), 'success');
    await reloadBoard();
  }, null, t('board.removeExternalColumn'));
}

// ═══════════════════════════════════════════════════════════
// BACKLOG VIEW
// ═══════════════════════════════════════════════════════════
// The Backlog is config #1 of the shared task-list engine (see views-tasklist.js):
// not-on-board tasks, grouped by release. renderBacklog just loads the data and
// hands a config to the engine — all list/group/search-refresh mechanics live
// there, so the Backlog and the Task view cannot drift apart.
async function renderBacklog() {
  const gen = S.contentGen;
  const tasks = await takeProjectTasks(S.project.id);
  if (gen !== S.contentGen) return;
  _backlogTasks = tasks;
  // The backlog has no status filter (status filtering isn't useful there); clear
  // any status filter carried over from the Task view so it can't silently narrow
  // the backlog with no control to undo it.
  S.filters.status = '';
  renderTaskList(backlogListConfig());
}

function backlogListConfig() {
  return {
    listId: 'backlog-list',
    scope: { backlogOnly: true },
    cache: () => _backlogTasks,
    sortable: true,
    header: taskSortableHeader('backlog-list'),
    group: backlogReleaseGroups,
    row: backlogRow,
    // The empty state renders INSTEAD of the toolbar's create button
    // (contentToolbar omits it to avoid a duplicate), so it has to ask the same
    // question viewCreateButton asks — otherwise a viewer sees on an empty
    // backlog exactly the button that was hidden everywhere else.
    emptyState: () => `
      <div class="empty">
        <div class="empty-icon">${icon('backlog',{size:'hero'})}</div>
        <div class="empty-title">${t('task.backlogEmptyTitle')}</div>
        <p class="empty-body">${t('task.backlogEmptyBody')}</p>
        ${AppPerms.isReadOnlyProject(S.project) ? '' : `<button class="btn btn-primary" data-act="showCreateTask">${icon('add',{size:'md'})} ${t('task.createBacklogItem')}</button>`}
      </div>`,
  };
}

// refreshBacklogList re-filters the cached backlog by the current search query
// and re-renders only the list body, keeping the search input focused.
function refreshBacklogList() {
  refreshTaskList(backlogListConfig());
}

function backlogReleaseGroups(tasks) {
  const groups = new Map();
  tasks.forEach(task => {
    const key = task.releaseId || '';
    if (!groups.has(key)) groups.set(key, []);
    groups.get(key).push(task);
  });
  const ordered = [...groups.keys()].sort((a, b) => {
    if (!a) return 1;
    if (!b) return -1;
    return releaseName(a).localeCompare(releaseName(b));
  });
  return ordered.map(key => ({
    key,
    label: key ? esc(releaseName(key)) : t('task.noRelease'),
    tasks: groups.get(key),
  }));
}

// backlogRow renders one list row. `actionsCell`, when supplied, is appended as
// a trailing grid cell for a per-row action (the Task view's delete button);
// the Backlog calls backlogRow(task) with no second argument and gets no extra
// cell. The caller that passes actions must also widen the grid (the Task view
// adds a trailing column via .backlog-wrap.has-row-actions) so header and rows
// stay aligned.
function backlogRow(task, actionsCell = '') {
  const seq = task.seqNumber != null ? `${esc(S.project?.abbreviation||S.project?.slug?.toUpperCase()||'')}-${task.seqNumber}` : '';
  // Each direct child is one grid cell, in the same column order as the header,
  // so the row aligns like the tasks-view table. Empty cells are kept (not
  // omitted) so optional fields don't shift the columns.
  return `
    <div class="backlog-row" role="button" tabindex="0" aria-label="${t('task.openTask',{title:esc(taskLabel(task))})}"
         data-act="openTaskListRow" data-a0="${esc(task.id)}"
         data-keydown="activateOnEnter">
      <span class="backlog-cell">${taskCheckbox(task.id, taskLabel(task))}</span>
      <span class="backlog-cell">${typeBadge(task.taskType)}</span>
      <span class="backlog-cell task-seq text-muted">${seq}</span>
      <span class="backlog-title">${esc(task.title)}</span>
      <span class="backlog-cell prio-cell">${priorityInline(task.priority)}${estimateTag(task)}</span>
      <span class="backlog-cell">${statusBadge(task.status)}</span>
      <span class="backlog-cell text-muted text-sm">${task.assigneeId ? esc(memberName(task.assigneeId)) : ''}</span>
      <span class="backlog-cell">${task.dueDate ? `<span class="due-tag ${isOverdue(task.dueDate)?'due-overdue':''}">${fmtDate(task.dueDate)}</span>` : ''}</span>
      ${actionsCell}
    </div>`;
}

// ── view registration (see registry.js for the contract) ──
Views.register('board', {
  render: renderBoard,
  prefetch: pid => prefetchProjectTasks(pid),
  refreshList: refreshBoardCards,
  liveRefresh: true,
  sidebar: { icon: 'board', label: () => t('nav.board'), key: 'B', order: 10 },
  createButton: () => `<button class="btn btn-primary btn-sm" data-act="showCreateTask">${icon('add',{size:'md'})} ${t('task.create')}</button>`,
});
// A sprint board exists from sprint creation until completion, so its nav
// entry is shown while the project has an ACTIVE or PLANNED sprint (the board
// is provisioned on creation for planning and torn down on completion).
Views.register('sprintBoard', {
  render: renderSprintBoard,
  prefetch: pid => prefetchProjectTasks(pid),
  liveRefresh: true,
  sidebar: {
    icon: 'sprint', label: () => t('nav.sprintBoard'), order: 15,
    when: () => (S.sprints || []).some(s => s.status === 'ACTIVE' || s.status === 'PLANNED'),
  },
});
Views.register('backlog', {
  render: renderBacklog,
  prefetch: pid => prefetchProjectTasks(pid),
  refreshList: refreshBacklogList,
  listConfig: backlogListConfig,
  liveRefresh: true,
  listToolbar: true,
  sidebar: { icon: 'backlog', label: () => t('nav.backlog'), key: 'L', order: 20 },
  createButton: () => `<button class="btn btn-primary btn-sm" data-act="showCreateTask">${icon('add',{size:'md'})} ${t('task.createBacklogItem')}</button>`,
});

// ═══════════════════════════════════════════════════════════
// TASK PANEL
// ═══════════════════════════════════════════════════════════

// ── Delegation registration: this file's handlers ───────────────────────────
// (see js/README.md "Delegation registration".)
registerActions([addLane, showAddExternalColumn, showBoardSettings, toggleBacklogColumn], _A0);
registerActions([
  removeExternalColumn, openSprintBoard, renameLane, removeLane, toggleTaskPin,
  loadMoreLane,
], _A1);
registerChanges({
  toggleSprintRow: node => { const r = document.querySelector('#bs-sprint-row'); if (r) r.classList.toggle('hidden', !node.checked); },
});

export { BOARD_RANK_STEP, addLane, applyBoardTaskRemoval, applyBoardTaskUpdate, backlogRow, boardColsInner, boardDragLeave, boardDragOver, clearDropIndicators, dropOnColumn, dropSlot, invalidateDragGeom, laneBuiltinFor, renameLane, renderBacklog, revealBoardCard, toggleBacklogColumn };
