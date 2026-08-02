// @ts-check
// Octbase SPA — split from the former single app.js. An ES module like the
// rest of js/ since 37b stage 2: what it needs it imports, what it offers it
// exports, and the bundler decides the order (js/README.md).
import { t } from '@octbase/shared/i18n.js';

// The API-shaped fields carry their generated types (37b stage 7). They come
// from octbase-api/api/openapi.yaml by way of types/openapi.d.ts, so a renamed
// or removed field on the Go side stops being a runtime `undefined` and becomes
// a type error in every view that reads it. Fields that are pure UI state
// (view, dragging, the draft maps) are deliberately left untyped — there is no
// contract to check them against.
/** @typedef {import('../types/api').Project} Project */
/** @typedef {import('../types/api').Task} Task */
/** @typedef {import('../types/api').User} User */

const S = {
  /** @type {User|null} */
  user: null,
  /** @type {Project[]} */
  projects:[],
  /** @type {Project|null} */
  project:null,
  /** @type {import('../types/api').Board|null} */
  board:null,
  /** @type {import('../types/api').Release[]} */
  releases:[],
  /** @type {import('../types/api').Sprint[]} */
  sprints:[],
  /** @type {import('../types/api').ProjectPriority[]} */
  priorities:[],   // the project's custom priorities (built-ins live in PRIORITIES)
  /** @type {import('../types/api').Page[]} */
  pages:[],
  /** @type {import('../types/api').MemberWithUser[]} */
  members:[],
  // Assignee/reviewer candidates: S.members plus the global admins. Kept apart
  // from S.members because the members page shows real memberships only.
  /** @type {import('../types/api').AssignableUser[]} */
  assignables:[],
  usersMap:{},
  repos:[],
  view:'dashboard',
  taskPanelId:null,
  taskPanelTab:'details',
  taskPanelData:null,   // cached payload for the open task so tab switches don't refetch
  dragging:null,
  // Set when a co-worker's change arrived over SSE and the on-screen data is
  // known to be behind. Drives the reload banner (realtime.js); cleared by any
  // repaint via renderContent().
  contentStale:false,
  selectedPage:null,
  pageEditMode:false,
  pageSearch:'',
  filters:{ status:'', priority:'', type:'', q:'' },
  taskDescriptionDrafts:{},
  taskDescriptionOriginals:{},
  // taskDescriptionDraftVersions maps taskId → the task version the draft in
  // taskDescriptionDrafts was started against. Sent with the description save
  // instead of the panel snapshot's version, because an SSE refresh of the open
  // panel advances the snapshot to the concurrent editor's version — saving
  // with that fresh version would silently overwrite their change. Cleared on
  // save and after a conflict is surfaced (a repeated save is then a
  // deliberate overwrite).
  taskDescriptionDraftVersions:{},
  // pageVersions maps pageId → the version last loaded into the page editor,
  // sent with page saves so a stale edit gets 409 instead of overwriting a
  // concurrent editor's changes.
  pageVersions:{},
  selectedTasks: new Set(),
  // Re-entrancy guard shared by the bulk task actions (views-shell.js applyBulkAction /
  // bulkAddToBoard, views-tasklist.js bulkSetStatus) so a double-click can't fire twice.
  bulkInFlight: false,
  sseSource: null,
  sessionLive: false,   // last known session health (drives the live indicator)
  sessionKnown: false,  // false until the first heartbeat/probe resolves
  notifCount: 0,
  permissionsByProject: {},
  // The version string behind the small "octbase X.Y" tag. Pre-boot default
  // "beta" matches the backend's unstamped default; loadFeatureConfig
  // (config.js) overwrites it from GET /api/v1/config at boot.
  appVersion: 'beta',
  // Cached, unfiltered task set for the board so its search input can
  // re-filter the rendered list in place (without refetching or losing input
  // focus). Populated by renderBoard (views-board.js); views-content.js reads
  // it for board-rank math.
  boardTasks: [],
  // contentGen is bumped on every renderContent() call. View renderers that
  // await a fetch before writing into #content capture it on entry and check
  // it again before their final DOM write, so a slow response from a
  // superseded navigation can't overwrite a newer view (see views-board.js,
  // views-tasklist.js, views-mindmap.js).
  contentGen: 0,
  // pendingDebounces counts callbacks that debounced() (framework.js) has
  // scheduled but not yet run. It is how an outside observer tells "the app is
  // still going to do something" from "the app is idle": the e2e suite's
  // settle() waits on it, so a test that types into a search box asserts
  // against the coalesced result instead of racing the timer with a sleep.
  pendingDebounces: 0,
};

const RECENT_PROJECTS_KEY = 'octbase.recent-projects';

function currentAppTitle() {
  if (!S.project) {
    if (S.view === 'dashboard') return t('nav.myWork');
    if (S.view === 'search') return t('nav.search');
    if (S.view === 'admin') return t('nav.userManagement');
    if (S.view === 'audit-logs') return t('nav.auditLogs');
    if (S.view === 'settings') return t('nav.settings');
    if (S.view === 'preferences') return t('notifications.preferencesTitle');
    return t('nav.projects');
  }
  return ({ board:t('nav.board'), sprintBoard:t('nav.sprintBoard'), backlog:t('nav.backlog'), tasks:t('nav.tasks'), mindmap:t('nav.mindmap'), releases:t('nav.releases'), sprints:t('nav.sprints'), pages:t('nav.pages'), repos:t('nav.repositories'), activity:t('nav.activity'), archive:t('nav.archive'), members:t('members.title') })[S.view] || S.view;
}

function loadRecentProjectIds() {
  try {
    return JSON.parse(localStorage.getItem(RECENT_PROJECTS_KEY) || '[]');
  } catch {
    return [];
  }
}

function rememberProjectVisit(projectId) {
  if (!projectId) return;
  const ids = loadRecentProjectIds().filter(id => id !== projectId);
  ids.unshift(projectId);
  try {
    localStorage.setItem(RECENT_PROJECTS_KEY, JSON.stringify(ids.slice(0, 10)));
  } catch {}
}

function sidebarProjects() {
  const byId = new Map((S.projects || []).map(project => [project.id, project]));
  const ordered = [];
  loadRecentProjectIds().forEach(id => {
    const project = byId.get(id);
    if (project) ordered.push(project);
  });
  S.projects.forEach(project => {
    if (!ordered.some(candidate => candidate.id === project.id)) ordered.push(project);
  });
  return ordered.slice(0, 5);
}

function taskFilterParams() {
  const params = new URLSearchParams();
  if (S.filters.priority) params.set('priority', S.filters.priority);
  if (S.filters.status) params.set('status', S.filters.status);
  if (S.filters.type) params.set('type', S.filters.type);
  if (S.filters.q) params.set('q', S.filters.q);
  return params;
}

// projectSeqPrefix is the letter part of a task key ("OCT" in OCT-202): the
// current project's abbreviation, falling back to its slug. Shared by the label
// and by the ID search that has to recognise the same key (parseSeqQuery).
function projectSeqPrefix() {
  return S.project?.abbreviation || S.project?.slug?.toUpperCase() || '';
}

function taskSeqLabel(task) {
  return task?.seqNumber != null ? `${projectSeqPrefix()}-${task.seqNumber}` : '';
}

function getTaskDraft(task) {
  if (Object.prototype.hasOwnProperty.call(S.taskDescriptionDrafts, task.id)) {
    return S.taskDescriptionDrafts[task.id];
  }
  return task.description || '';
}


function applyTaskFilters(tasks, { boardOnly = false, backlogOnly = false, ignoreSearch = false } = {}) {
  let filtered = [...(tasks || [])];
  if (!S.filters.status) {
    filtered = filtered.filter(task => task.status !== 'ARCHIVED');
  }
  if (S.filters.status) filtered = filtered.filter(task => task.status === S.filters.status);
  if (S.filters.priority) filtered = filtered.filter(task => task.priority === S.filters.priority);
  if (S.filters.type) filtered = filtered.filter(task => task.taskType === S.filters.type);
  if (boardOnly) filtered = filtered.filter(task => !!task.boardColumnId);
  if (backlogOnly) {
    filtered = filtered.filter(task => !task.boardColumnId);
    if (!S.filters.status) filtered = filtered.filter(task => !['DONE', 'ARCHIVED'].includes(task.status));
  }
  if (!ignoreSearch) filtered = filterTasksBySearch(filtered, { fulltext: true });
  return filtered;
}

// ── The lowercased search haystack ──────────────────────────────────────────
// filterTasksBySearch runs on every keystroke over the whole cached task set, and
// lowercasing a description allocates a copy of it: 200 tasks meant 200 fresh
// strings per keystroke, dominated by the descriptions (the longest field in the
// model).
//
// The cache is DERIVED, not maintained: the lowercased text is rebuilt whenever
// the task's own title/description no longer match the ones it was built from. So
// there is no invalidation to get wrong — not for the in-place list patch
// (applyListTaskUpdate), not for patchBoardCaches, not for an SSE refresh, not
// for a field this code has never heard of. An eager cache updated at each known
// write site would be faster to read but would go stale the first time someone
// added a write site and forgot; a stale haystack silently returns wrong search
// results, which is a worse bug than the allocation it saves.
//
// Keyed in a WeakMap on the task object rather than in fields on it, so the
// derived text can never ride along into a request body via a `{...task}` spread
// and so entries die with the tasks they describe. String comparison against the
// source hits the engine's identical-reference fast path, since a task that
// nobody rewrote carries the very same string objects.
const _haystacks = new WeakMap();
function taskHaystack(task) {
  const title = task.title || '';
  const desc = task.description || '';
  let hay = _haystacks.get(task);
  if (!hay || hay.srcTitle !== title || hay.srcDesc !== desc) {
    hay = { srcTitle: title, srcDesc: desc, title: title.toLowerCase(), desc: desc.toLowerCase() };
    _haystacks.set(task, hay);
  }
  return hay;
}

// ── Matching a task key (OCT-202) ───────────────────────────────────────────
// The key printed on every card and row is not a stored field: it is the
// project's abbreviation joined to the task's seqNumber (taskSeqLabel). Looking
// a task up by it therefore cannot be a substring test over task text — the
// query is parsed once per call and compared numerically per task, so the ID
// branch costs no allocation on the per-keystroke path.
//
// Both spellings resolve to the same task: the full key ("OCT-202", any case,
// a leading "#" tolerated) and the bare number ("202"). A key naming a
// DIFFERENT project ("FOO-202") matches nothing — these views are all scoped to
// one project, so answering it with the current project's 202 would be a
// confidently wrong answer rather than a near miss.
//
// The match is exact, not a prefix: "oct-20" does not find OCT-202. An ID lookup
// is a targeted one, and a prefix rule would make the bare-number form pull in
// every task numbered 2xx while the user is still typing "202" — noise layered
// on top of the text search that is already running alongside it.
//
// Returns the sequence number to match, or null when the query is not a key
// (the overwhelmingly common case: ordinary search text).
function parseSeqQuery(needle) {
  const m = /^#?(?:([a-z0-9]+)-)?(\d{1,9})$/.exec(needle);
  if (!m) return null;
  if (m[1] != null && m[1] !== projectSeqPrefix().toLowerCase()) return null;
  return Number(m[2]);
}

// filterTasksBySearch narrows tasks by the free-text query in S.filters.q. The
// backlog searches full text (title + description); the board filters by name
// (title only), matching the "filter by name" control there. Either way a query
// that reads as a task key also matches that task by ID, so the key shown on a
// row or card can be pasted back into the box to find it. Empty query is a
// no-op so callers can apply it unconditionally.
function filterTasksBySearch(tasks, { fulltext = false } = {}) {
  const needle = (S.filters.q || '').trim().toLowerCase();
  if (!needle) return tasks;
  const seq = parseSeqQuery(needle);
  return tasks.filter(task => {
    if (seq !== null && task.seqNumber === seq) return true;
    const hay = taskHaystack(task);
    return hay.title.includes(needle) || (fulltext && hay.desc.includes(needle));
  });
}

function taskMetaById(tasks) {
  const meta = new Map();
  (tasks || []).forEach(task => meta.set(task.id, { title: task.title, seq: taskSeqLabel(task) }));
  return meta;
}

// ═══════════════════════════════════════════════════════════
// LOGIN PAGE
// ═══════════════════════════════════════════════════════════
// renderLangLinks renders the locale switcher as a <select>. changeFn names the
// handler to invoke with the chosen locale ('changeLocale' in-app, or
// 'switchAuthLocale' on the login/invite pages). Only one switcher is ever in
// the DOM at a time, so the fixed id is safe.

export { S, applyTaskFilters, currentAppTitle, filterTasksBySearch, getTaskDraft, rememberProjectVisit, sidebarProjects, taskFilterParams, taskMetaById, taskSeqLabel };
