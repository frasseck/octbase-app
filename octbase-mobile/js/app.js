import qrcode from 'qrcode-generator';
import { getLocale, i18n, t } from '@octbase/shared/i18n.js';
import { FIBONACCI_POINTS, STATUSES, STATUS_META, TYPE_META, estimableType, estimateLabel, estimateLimits, estimateText, estimationEnabled, estimationField, estimationUnit, openDescendantsOf, parseEstimateInput, priorityMeta, priorityNames, projectTaskTypes, taskEstimatable, taskEstimate, typeParentRule } from '@octbase/shared/meta.js';
import { ACTIONS, API_BASE, Auth, BASE_PATH, CHANGES, DESKTOP_URL, INPUTS, IS_PHONE, SUBMITS, USE_STANDALONE_DEMO_AUTH, V, _A0, _A1, _A2, _A3, _dispatch, _register, api, apiErrorMessage, el, esc, html, http, icon, initials, raw, renderDescriptionHTML, toast } from './core.js';

// ═══════════════════════════════════════════════════════════
// octbase-mobile — phone-first SPA view layer
// The bundle entry: imports js/core.js (Auth, http, api, icon,
// esc, html, …) and @octbase/shared (t, setLocale, task
// metadata, the rich-text sanitizer). Hash-routed; all markup is
// generated here. Class names must match css/mobile.css.
// ═══════════════════════════════════════════════════════════

const S = {
  user: null,
  project: null,        // { id, name, abbreviation, slug, themeEnabled, initiativeEnabled }
  members: [],          // [{ userId, name, email }]
  assignables: [],      // members + global admins — the assignee picker's candidates
  priorities: [],       // the project's custom priorities (built-ins live in PRIORITIES)
  membersMap: {},       // userId -> { name, email }
  board: null,          // { id, columns:[{id,name,status}] }
  boardCol: null,       // selected column id (board view)
  notifCount: 0,
  filters: { status:'', priority:'', type:'' },
  lastList: 'dashboard',// remembered top-level destination for the bottom nav
  // One project's fetched board + live tasks, so the two pure client-side view
  // changes (board column tabs, backlog filters) can re-render without a round
  // trip. { projectId, board, tasks, byCol } — see taskCacheFor below.
  taskCache: null,
};
window.S = S;

// ═══════════════════════════════════════════════════════════
// BOARD / BACKLOG TASK CACHE
// ───────────────────────────────────────────────────────────
// Tapping between board columns is the primary board navigation on a phone, and
// the backlog filters are applied entirely client-side — yet both used to
// re-enter the whole view, costing two round trips plus a skeleton flash for a
// change that needs neither. The cache is written on every route entry into the
// board/backlog and read ONLY by selectBoardCol / applyFilter / clearFilters;
// every mutation drops it (invalidateTaskCache), and each reader falls back to a
// full re-fetch when it is missing or belongs to another project, so a stale
// list can never be rendered.
// ═══════════════════════════════════════════════════════════
function invalidateTaskCache() { S.taskCache = null; }

// taskCacheFor returns the cache entry only when it is this project's.
function taskCacheFor(pid) {
  const c = S.taskCache;
  return (c && pid && c.projectId === pid) ? c : null;
}

// boardBuckets groups live tasks into their board columns, sorted the way the
// board shows them (pinned first, then boardRank).
function boardBuckets(cols, tasks) {
  const byCol = {}; cols.forEach(c => byCol[c.id] = []);
  tasks.forEach(tk => { if (tk.boardColumnId && byCol[tk.boardColumnId]) byCol[tk.boardColumnId].push(tk); });
  Object.values(byCol).forEach(list => list.sort((a,b) =>
    ((b.pinned?1:0)-(a.pinned?1:0)) || ((a.boardRank||0)-(b.boardRank||0))));
  return byCol;
}

// ── small formatters ──
// Intl.DateTimeFormat construction dominates the cost of formatting a date, and
// `toLocaleDateString(locale, {…})` with a fresh options-object literal builds a
// new one on every call — the engine's format cache cannot match a new object.
// The card lists format one date per row, so that shows up: ~29ms/400 calls
// versus ~0.8ms memoized. Cache one formatter per (locale, shape) instead.
// The locale is resolved on every call, never captured at load time, so a
// runtime language switch still changes the format. Output is byte-identical:
// with day/month/year all specified, toLocaleDateString's date defaults never
// kick in, so it and DateTimeFormat.format produce the same string.
const DATE_SHAPES = { medium: { day:'numeric', month:'short', year:'numeric' } };
const _dateFormatters = new Map();
function dateFormatter(shape) {
  const loc = getLocale();
  const key = loc + '|' + shape;
  let f = _dateFormatters.get(key);
  if (!f) { f = new Intl.DateTimeFormat(loc, DATE_SHAPES[shape]); _dateFormatters.set(key, f); }
  return f;
}
function fmtDate(d) {
  if (!d) return '';
  const dt = new Date(d);
  if (isNaN(dt)) return '';
  return dateFormatter('medium').format(dt);
}
function fmtRelative(d) {
  if (!d) return '';
  const dt = new Date(d); if (isNaN(dt)) return '';
  const diff = (Date.now() - dt.getTime()) / 1000;
  if (diff < 60) return t('time.justNow');
  const mins = Math.floor(diff/60); if (mins < 60) return `${mins}m`;
  const hrs = Math.floor(mins/60); if (hrs < 24) return `${hrs}h`;
  return fmtDate(d);
}
function memberName(userId) {
  if (!userId) return t('task.unassigned');
  return S.membersMap[userId]?.name || S.membersMap[userId]?.email || '—';
}
function projectPrefix(p) { return p ? (p.abbreviation || (p.slug ? p.slug.toUpperCase() : '')) : ''; }
function taskKey(task, p) {
  const pre = projectPrefix(p);
  return (task.seqNumber != null) ? `${pre}-${task.seqNumber}` : '';
}

// ── shared badge fragments (return raw HTML strings) ──
function statusBadge(status) {
  if (!status) return '';
  // Custom lane statuses have no STATUS_META entry; render them with a neutral
  // badge using the raw status text as the label (parity with the desktop).
  const m = STATUS_META[status] || { label: status, cls: 'badge-muted' };
  return `<span class="badge ${m.cls}">${esc(m.label)}</span>`;
}
function prioBadge(priority) {
  if (!priority) return '';
  const m = priorityMeta(priority); // custom project priorities get a neutral badge
  return `<span class="badge prio-badge ${m.cls}">${esc(m.label)}</span>`;
}
function typeGlyph(type) {
  const m = TYPE_META[type] || TYPE_META.TASK;
  return `<span class="type-glyph ${m.cls}" title="${esc(m.label)}" aria-label="${esc(m.label)}">${m.sym}</span>`;
}
// estimateChip mirrors the desktop's estimateTag: nothing at all when the
// project does not estimate, when the type cannot carry an estimate, or when
// the task is unestimated — an empty chip would read as "zero effort", which is
// a different claim. The title carries the unit so a bare number is unambiguous.
function estimateChip(task) {
  if (!taskEstimatable(S.project, task)) return '';
  const text = estimateText(S.project, task);
  if (text === '') return '';
  return `<span class="badge est-chip" title="${esc(estimateLabel(S.project))}">${esc(text)}</span>`;
}
function avatar(userId, sm) {
  const name = userId ? memberName(userId) : '';
  const title = esc(name || t('task.unassigned'));
  const chip = esc(initials(name));
  const token = userId && S.membersMap[userId] ? S.membersMap[userId].avatarUpdatedAt : null;
  if (userId && token) {
    return `<span class="avatar${sm?' sm':''} has-avatar" title="${title}">${chip}` +
      `<img class="avatar-img" alt="" aria-hidden="true" data-avatar-user="${esc(userId)}" data-avatar-v="${esc(token)}"></span>`;
  }
  return `<span class="avatar${sm?' sm':''}" title="${title}">${chip}</span>`;
}

// Avatar images ride the bearer token, so they are fetched once per user+version
// via http.getBlob and shown as an object URL. hydrateAvatars fills every
// not-yet-loaded avatar <img>; a debounced observer (installed in initDelegation)
// calls it after each render. Mirrors the desktop SPA.
const _avatarObjectURLs = new Map();
function _avatarObjectURL(userId, token) {
  const key = userId + '@' + (token || '');
  let p = _avatarObjectURLs.get(key);
  if (!p) {
    p = api.users.avatarBlob(userId, token).then(b => URL.createObjectURL(b));
    p.catch(() => _avatarObjectURLs.delete(key));
    _avatarObjectURLs.set(key, p);
  }
  return p;
}
function hydrateAvatars(root) {
  (root || document).querySelectorAll('img.avatar-img[data-avatar-user]:not([data-avatar-done])').forEach(img => {
    img.setAttribute('data-avatar-done', '1');
    _avatarObjectURL(img.getAttribute('data-avatar-user'), img.getAttribute('data-avatar-v') || '')
      .then(url => { img.src = url; }).catch(() => {});
  });
}

// ═══════════════════════════════════════════════════════════
// APP FRAME (top bar + content + bottom nav)
// ═══════════════════════════════════════════════════════════
function appbar(title, { back = null, action = '' } = {}) {
  const lead = back
    ? `<button class="icon-btn" data-act="nav" data-a0="${esc(back)}" aria-label="${t('common.back')}">${icon('chevron-left')}</button>`
    : `<button class="icon-btn" data-act="openProfile" aria-label="${t('nav.profile')}">${icon('user')}</button>`;
  return `<header class="appbar">
    ${lead}
    <h1 class="appbar-title">${esc(title)}</h1>
    ${action}
  </header>`;
}

function bottomNav(active) {
  const items = [
    { id:'dashboard',     path:'/dashboard',     ic:'home',   label:t('nav.myWork') },
    { id:'projects',      path:'/projects',      ic:'project',label:t('nav.projects') },
    { id:'search',        path:'/search',        ic:'search', label:t('nav.search') },
    // Its own key, not notifications.title: a tab cell is a quarter of the
    // screen, so the nav needs the short wording ("Nachrichten") while the
    // view title stays the full one ("Benachrichtigungen").
    { id:'notifications', path:'/notifications', ic:'bell',   label:t('nav.notifications') },
  ];
  return `<nav class="bottom-nav" aria-label="${t('nav.mainNavigation')}">
    ${items.map(it => `
      <button type="button" class="nav-item${active===it.id?' active':''}" data-act="nav" data-a0="${esc(it.path)}"
        ${active===it.id?'aria-current="page"':''}>
        <span class="nav-ic">
          ${icon(it.ic)}
        </span>
        ${it.id==='notifications' && S.notifCount ? `<span class="nav-badge">${S.notifCount>99?'99+':S.notifCount}</span>` : ''}
        <span class="nav-lbl">${esc(it.label)}</span>
      </button>`).join('')}
  </nav>`;
}

function render(title, bodyHtml, { back=null, action='', nav=null, flush=false } = {}) {
  const root = el('#app-root');
  root.innerHTML = html`<div class="app">
    ${raw(appbar(title, { back, action }))}
    <main class="content${flush?' flush':''}" id="content">${raw(bodyHtml)}</main>
    ${nav ? raw(bottomNav(nav)) : ''}
  </div>`;
  document.querySelector('.content')?.scrollTo?.(0,0);
}

function loadingState() { return `<div class="loading"><div class="spinner"></div></div>`; }
function emptyState(iconName, title, body) {
  return `<div class="state">
    <div class="state-icon">${icon(iconName,{size:'hero'})}</div>
    <div class="state-title">${esc(title)}</div>
    ${body?`<p>${esc(body)}</p>`:''}
  </div>`;
}
function errorState(e, retryAct) {
  return `<div class="state">
    <div class="state-icon">${icon('warning',{size:'hero'})}</div>
    <div class="state-title">${t('errors.generic')}</div>
    <p>${esc(apiErrorMessage(e))}</p>
    ${retryAct?`<button class="btn" data-act="${retryAct}">${icon('refresh',{size:'sm'})} ${t('common.retry')}</button>`:''}
  </div>`;
}
function desktopCta(label) {
  if (IS_PHONE) return '';
  return `<a class="desktop-cta" href="${esc(DESKTOP_URL)}">${icon('external',{size:'sm'})} ${esc(label || t('mobile.openDesktop'))}</a>`;
}

// ═══════════════════════════════════════════════════════════
// BOTTOM SHEET
// ═══════════════════════════════════════════════════════════
let _sheetReturnFocus = null;
// Declared up here with the sheet's other state, not down with confirmSheet:
// closeSheet() settles it, and closeSheet is defined above that point.
let _confirmResolve = null;
function _settleConfirm(answer) {
  const resolve = _confirmResolve;
  _confirmResolve = null;
  if (resolve) resolve(answer);
}
function openSheet(titleStr, bodyHtml) {
  closeSheet();
  _sheetReturnFocus = document.activeElement;
  const wrap = document.createElement('div');
  wrap.id = 'sheet-wrap';
  wrap.innerHTML = html`
    <div class="scrim" id="sheet-scrim" data-act="closeSheetAct"></div>
    <div class="sheet" role="dialog" aria-modal="true" aria-label="${titleStr}">
      <div class="sheet-grip"></div>
      ${titleStr ? raw(html`<div class="sheet-title">${titleStr}</div>`) : ''}
      <div class="sheet-body">${raw(bodyHtml)}</div>
    </div>`;
  // Append inside .app so the desktop phone-frame (a transformed containing
  // block) clips the scrim/sheet to the frame; fall back to body pre-login.
  (el('.app') || document.body).appendChild(wrap);
  requestAnimationFrame(() => {
    wrap.querySelector('.scrim')?.classList.add('open');
    wrap.querySelector('.sheet')?.classList.add('open');
  });
  document.addEventListener('keydown', _sheetEsc);
}
function _sheetEsc(e) { if (e.key === 'Escape') closeSheet(); }
function closeSheet() {
  const wrap = el('#sheet-wrap');
  if (!wrap) return;
  document.removeEventListener('keydown', _sheetEsc);
  wrap.remove();
  if (_sheetReturnFocus && document.contains(_sheetReturnFocus)) _sheetReturnFocus.focus();
  _sheetReturnFocus = null;
  // Dismissing a confirm sheet any other way than its confirm button — scrim,
  // Escape, or another sheet opening over it — is an answer of "no". Settling
  // here rather than per-path is what stops a caller awaiting forever.
  _settleConfirm(false);
}
function closeSheetAct() { closeSheet(); }

// ── confirm sheet ───────────────────────────────────────────────────────────
// Mobile's answer to the desktop's confirmModal. There was no promise-based
// yes/no here at all — openSheet paints and returns, so every caller that
// needed an answer had to be written inside-out around a data-act callback.
//
// The resolver lives in a module-level slot rather than in the sheet's markup
// because every path that dismisses a sheet has to answer: the two buttons, the
// scrim, Escape, and the next openSheet (which calls closeSheet first). Those
// last three all funnel through closeSheet, so settling there is what makes a
// dismissal mean "no" instead of hanging the caller forever.
// confirmSheet resolves true only when the confirm button is tapped. bodyHtml is
// trusted markup the caller has already escaped — same contract as openSheet.
function confirmSheet(titleStr, bodyHtml, confirmLabel) {
  return new Promise(resolve => {
    // A confirm replacing a confirm answers the first one "no" before the slot
    // is overwritten; openSheet → closeSheet would otherwise settle the promise
    // we are about to store.
    _settleConfirm(false);
    // A plain template literal, not the `html` tag: raw() is a marker object the
    // tag unwraps, so interpolating it here would print [object Object]. The
    // sheet options above build their markup the same way — bodyHtml arrives
    // already escaped by the caller, and the two labels are escaped here.
    openSheet(titleStr, `
      <div class="confirm-body">${bodyHtml}</div>
      <button type="button" class="btn btn-primary btn-block" data-act="confirmSheetYes">${esc(confirmLabel)}</button>
      <button type="button" class="btn btn-block" style="margin-top:var(--space-2)" data-act="confirmSheetNo">${esc(t('form.cancel'))}</button>`);
    _confirmResolve = resolve;
  });
}
function confirmSheetYes() { const r = _confirmResolve; _confirmResolve = null; closeSheet(); if (r) r(true); }
function confirmSheetNo()  { closeSheet(); }

// ── completion warning ──────────────────────────────────────────────────────
// The mobile half of OCT-300: completing a task that still has open work under
// it asks first. Desktop covers three doors; the two here are the status sheet
// and the move-to-column sheet, and the reasoning is the same — a warning
// missing from one door is one users learn to route around, and the phone is
// where a status gets tapped in a hurry.
const OPEN_DESCENDANT_SAMPLE = 3;
// Like the desktop version this fails OPEN: if the task list can't be read the
// write goes through rather than being blocked on a failed side fetch. It is an
// affordance, not a guard — the real guard (an open BLOCKER below) is server-side.
async function confirmCompletionOverOpenDescendants(taskId, projectId) {
  const pid = projectId || _task?.projectId || S.project?.id;
  if (!taskId || !pid) return true;
  const tasks = (await api.tasks.listAll(pid).catch(() => [])) || [];
  const open = openDescendantsOf([taskId], tasks);
  if (!open.length) return true;
  // The task number is what a reader needs to go and look at what is still
  // running, so it leads — same shape as the desktop's taskLabel().
  const label = tk => { const key = taskKey(tk, S.project); return key ? `${key} ${tk.title}` : tk.title; };
  const names = open.slice(0, OPEN_DESCENDANT_SAMPLE).map(tk => esc(label(tk))).join(', ');
  const rest = open.length - Math.min(open.length, OPEN_DESCENDANT_SAMPLE);
  const listed = rest ? t('task.openDescendantsMore', { names, count: rest }) : names;
  const body = `${t('task.openDescendantsBody', { count: open.length })}`
    + `<div class="confirm-hint">${t('task.openDescendantsList', { names: listed })}</div>`;
  return confirmSheet(t('task.openDescendantsTitle'), body, t('task.completeAnyway'));
}

function sheetOptions(opts, currentValue, actName) {
  return opts.map(o => `
    <button type="button" class="sheet-opt${o.value===currentValue?' selected':''}"
      data-act="${actName}" data-a0="${esc(o.id)}" data-a1="${esc(o.value)}">
      ${o.glyph||''}
      <span>${o.labelHtml ? o.labelHtml : esc(o.label)}</span>
      ${o.value===currentValue?`<span class="check">${icon('check',{size:'sm'})}</span>`:''}
    </button>`).join('');
}

// ═══════════════════════════════════════════════════════════
// ROUTER
// ═══════════════════════════════════════════════════════════
function navTo(path) { window.location.hash = '#' + path; }
function nav(path) { navTo(path); }

window.addEventListener('hashchange', handleRoute);

function currentRoute() {
  const raw = window.location.hash.slice(1) || '/';
  const [path, search] = raw.split('?');
  return { path, query: new URLSearchParams(search || '') };
}

async function handleRoute() {
  closeSheet();
  const { path, query } = currentRoute();

  if (path === '/forgot-password') { renderForgotPassword(); return; }
  if (path.startsWith('/reset-password/')) { renderResetPassword(path.split('/')[2]); return; }
  // mayHaveSession, not isAuthenticated — see core.js. A navigation arriving
  // before boot's refresh has resolved otherwise renders the login page to
  // someone who is signed in, and the route they asked for is gone.
  if (!Auth.mayHaveSession()) {
    if (path !== '/login') { navTo('/login'); return; }
    renderLogin(); return;
  }
  // Bounce off /login only with a token actually in hand. Gating this on
  // mayHaveSession instead would let a stale marker ping-pong /login →
  // /dashboard → 401 → /login; the desktop router gates it the same way.
  if (path === '/login' && Auth.token) { navTo('/dashboard'); return; }
  if (path === '/login') { renderLogin(); return; }

  if (path === '/' || path === '/dashboard') { S.lastList='dashboard'; return viewDashboard(); }
  if (path === '/projects')                   { S.lastList='projects';  return viewProjects(); }
  if (path === '/search')                     { S.lastList='search';    return viewSearch(query.get('q') || ''); }
  if (path === '/notifications')              { S.lastList='notifications'; return viewNotifications(); }
  if (path === '/settings')                   { return viewSettings(); }

  let m;
  if ((m = path.match(/^\/projects\/([^/]+)\/board$/)))   return viewBoard(m[1]);
  if ((m = path.match(/^\/projects\/([^/]+)\/backlog$/))) return viewBacklog(m[1]);
  if ((m = path.match(/^\/projects\/([^/]+)\/new$/)))     return viewCreateTask(m[1]);
  if ((m = path.match(/^\/projects\/([^/]+)$/)))          return viewBoard(m[1]);
  if ((m = path.match(/^\/task\/([^/]+)$/)))              return viewTask(m[1]);

  return viewDashboard();
}

// ═══════════════════════════════════════════════════════════
// PROJECT CONTEXT LOADER
// ═══════════════════════════════════════════════════════════
async function ensureProject(pid) {
  if (S.project && S.project.id === pid && S.members.length) return;
  // Switching project invalidates the cached board/task list by construction
  // (taskCacheFor keys on projectId), but drop it explicitly so a half-loaded
  // switch cannot leave the previous project's list reachable.
  if (!S.project || S.project.id !== pid) invalidateTaskCache();
  const [project, members, assignables, priorities] = await Promise.all([
    api.projects.get(pid),
    api.members.list(pid).catch(() => []),
    api.members.assignable(pid).catch(() => []),
    api.priorities.list(pid).catch(() => []),
  ]);
  S.project = project;
  S.members = members;
  // Assignee candidates are a superset of the members — global admins included.
  // Fall back to the members when the call fails, so the picker keeps working.
  S.assignables = assignables.length ? assignables : members;
  S.priorities = priorities;
  S.membersMap = {};
  // Seed the name/avatar lookup from the wider list: a task's assignee or
  // creator may be a global admin who is not a member of this project.
  S.assignables.forEach(m => { S.membersMap[m.userId] = { name:m.name, email:m.email, avatarUpdatedAt:m.avatarUpdatedAt }; });
  members.forEach(m => { S.membersMap[m.userId] = { name:m.name, email:m.email, avatarUpdatedAt:m.avatarUpdatedAt }; });
}

// ═══════════════════════════════════════════════════════════
// LOGIN
// ═══════════════════════════════════════════════════════════
function renderLogin() {
  el('#app-root').innerHTML = html`
    <div class="login">
      <div class="login-logo">
        <img src="img/octbase_logo.svg" alt="Octbase">
      </div>
      <form id="login-form" data-submit="doLogin" novalidate>
        <div class="login-alert hidden" id="login-error" role="alert"></div>
        <div class="field">
          <label class="field-label" for="login-email">${raw(t('auth.email'))}</label>
          <input class="input" id="login-email" type="email" inputmode="email" autocomplete="username" required autofocus>
        </div>
        <div class="field">
          <label class="field-label" for="login-password">${raw(t('auth.password'))}</label>
          <input class="input" id="login-password" type="password" autocomplete="current-password" required>
        </div>
        <button class="btn btn-primary btn-block" id="login-submit" type="submit">${raw(t('auth.signIn'))}</button>
      </form>
      <a class="btn btn-block" style="margin-top:var(--space-2)" href="#/forgot-password">${raw(t('auth.forgotPassword'))}</a>
      <nav class="auth-legal">
        <a href="https://ocete.ch/privacy.html" target="_blank" rel="noopener">${raw(t('auth.privacy'))}</a>
        <span aria-hidden="true">·</span>
        <a href="https://ocete.ch/impressum.html" target="_blank" rel="noopener">${raw(t('auth.imprint'))}</a>
      </nav>
    </div>`;
}

async function doLogin(form, ev) {
  ev.preventDefault();
  const email = el('#login-email').value;
  const password = el('#login-password').value;
  const err = el('#login-error');
  const btn = el('#login-submit');
  err.className = 'login-alert hidden'; err.textContent = '';
  btn.disabled = true; btn.textContent = t('auth.signingIn');
  try {
    const r = await fetch(API_BASE + BASE_PATH + '/auth/login', {
      method:'POST', credentials:'include',
      headers:{ 'Content-Type':'application/json' },
      body: JSON.stringify({ email, password }),
    });
    if (!r.ok) {
      err.textContent = t('auth.invalidCredentials');
      err.className = 'login-alert';
      return;
    }
    const data = await r.json();
    if (data.mfaRequired) {
      renderMfaChallengeStep(data.challengeToken);
      return;
    }
    Auth.token = data.accessToken;
    await bootSession();
    navTo('/dashboard');
  } catch (e) {
    err.textContent = t('auth.connectionError', { message: e.message });
    err.className = 'login-alert';
  } finally {
    btn.disabled = false; btn.textContent = t('auth.signIn');
  }
}

// ═══════════════════════════════════════════════════════════
// PASSWORD RESET (mirrors octbase-frontend/js/framework.js)
// ═══════════════════════════════════════════════════════════
// The backend answers 202 with the same body whether or not the account
// exists (no enumeration), so the confirmation shown here is generic.
function renderForgotPassword() {
  el('#app-root').innerHTML = html`
    <div class="login">
      <div class="login-logo">
        <img src="img/octbase_logo.svg" alt="Octbase">
      </div>
      <h1>${raw(t('auth.forgotTitle'))}</h1>
      <p class="row-sub" style="white-space:normal;text-align:center">${raw(t('auth.forgotDesc'))}</p>
      <form data-submit="doForgotPasswordMobile" novalidate>
        <div class="login-alert hidden" id="forgot-error" role="alert"></div>
        <div class="field">
          <label class="field-label" for="forgot-email">${raw(t('auth.email'))}</label>
          <input class="input" id="forgot-email" type="email" inputmode="email" autocomplete="username" required autofocus>
        </div>
        <button class="btn btn-primary btn-block" id="forgot-submit" type="submit">${raw(t('auth.sendResetLink'))}</button>
        <a class="btn btn-block" style="margin-top:var(--space-2)" href="#/login">${raw(t('auth.backToSignIn'))}</a>
      </form>
    </div>`;
}

async function doForgotPasswordMobile(form, ev) {
  ev.preventDefault();
  const email = el('#forgot-email').value.trim();
  const err = el('#forgot-error');
  const btn = el('#forgot-submit');
  err.className = 'login-alert hidden'; err.textContent = '';
  btn.disabled = true;
  try {
    await http.post(`${V}/auth/forgot-password`, { email });
    el('#app-root').querySelector('.login').innerHTML = html`
      <div class="login-logo"><img src="img/octbase_logo.svg" alt="Octbase"></div>
      <h1>${raw(t('auth.forgotTitle'))}</h1>
      <p class="row-sub" style="white-space:normal;text-align:center" role="status">${raw(t('auth.resetEmailSent'))}</p>
      <a class="btn btn-primary btn-block" href="#/login">${raw(t('auth.backToSignIn'))}</a>`;
  } catch (e) {
    err.textContent = apiErrorMessage(e);
    err.className = 'login-alert';
    btn.disabled = false;
  }
}

function renderResetPassword(token) {
  el('#app-root').innerHTML = html`
    <div class="login">
      <div class="login-logo">
        <img src="img/octbase_logo.svg" alt="Octbase">
      </div>
      <h1>${raw(t('auth.resetTitle'))}</h1>
      <form data-submit="doResetPasswordMobile" data-a0="${token}" novalidate>
        <div class="login-alert hidden" id="reset-error" role="alert"></div>
        <div class="field">
          <label class="field-label" for="reset-password">${raw(t('auth.newPassword'))}</label>
          <input class="input" id="reset-password" type="password" autocomplete="new-password" required autofocus>
        </div>
        <button class="btn btn-primary btn-block" id="reset-submit" type="submit">${raw(t('auth.setNewPassword'))}</button>
        <a class="btn btn-block" style="margin-top:var(--space-2)" href="#/login">${raw(t('auth.backToSignIn'))}</a>
      </form>
    </div>`;
}

async function doResetPasswordMobile(form, ev, token) {
  ev.preventDefault();
  const pw = el('#reset-password');
  const err = el('#reset-error');
  const btn = el('#reset-submit');
  err.className = 'login-alert hidden'; err.textContent = '';
  btn.disabled = true;
  try {
    await http.post(`${V}/auth/reset-password`, { token, newPassword: pw.value });
    el('#app-root').querySelector('.login').innerHTML = html`
      <div class="login-logo"><img src="img/octbase_logo.svg" alt="Octbase"></div>
      <h1>${raw(t('auth.resetTitle'))}</h1>
      <p class="row-sub" style="white-space:normal;text-align:center" role="status">${raw(t('auth.resetSuccess'))}</p>
      <a class="btn btn-primary btn-block" href="#/login">${raw(t('auth.backToSignIn'))}</a>`;
  } catch (e) {
    err.textContent = apiErrorMessage(e);
    err.className = 'login-alert';
    btn.disabled = false;
  }
}

// renderMfaChallengeStep swaps the login form for a second-factor code input
// once the backend has accepted the password but reports MFA is enabled (see
// octbase-frontend/js/framework.js's renderMfaChallengeStep — mirrors it).
// The challenge token round-trips through the form's data-a0 rather than a
// module-level variable, so a page reload simply drops back to a fresh login.
function renderMfaChallengeStep(challengeToken) {
  el('#app-root').innerHTML = html`
    <div class="login">
      <div class="login-logo">
        <img src="img/octbase_logo.svg" alt="Octbase">
      </div>
      <h1>${raw(t('auth.mfa.title'))}</h1>
      <p class="row-sub" style="white-space:normal;text-align:center">${raw(t('auth.mfa.desc'))}</p>
      <form data-submit="doVerifyMfaLoginMobile" data-a0="${challengeToken}" novalidate>
        <div class="login-alert hidden" id="mfa-login-error" role="alert"></div>
        <div class="field">
          <label class="field-label" for="mfa-login-code">${raw(t('auth.mfa.codeLabel'))}</label>
          <input class="input" id="mfa-login-code" inputmode="numeric" autocomplete="one-time-code" required autofocus>
        </div>
        <button class="btn btn-primary btn-block" id="mfa-login-submit" type="submit">${raw(t('auth.mfa.verify'))}</button>
        <button class="btn btn-block" type="button" style="margin-top:var(--space-2)" data-act="mfaBackToLogin">${raw(t('auth.mfa.back'))}</button>
      </form>
    </div>`;
}

// mfaBackToLogin abandons the pending challenge and returns to the password
// step — the escape hatch for an expired challenge token (nothing to revoke
// server-side; the token simply lapses).
function mfaBackToLogin() {
  renderLogin();
}

async function doVerifyMfaLoginMobile(form, ev, challengeToken) {
  ev.preventDefault();
  const codeInput = el('#mfa-login-code');
  const err = el('#mfa-login-error');
  const btn = el('#mfa-login-submit');
  err.className = 'login-alert hidden'; err.textContent = '';
  btn.disabled = true;
  try {
    const data = await api.auth.verifyMfa(challengeToken, codeInput.value.trim());
    Auth.token = data.accessToken;
    await bootSession();
    navTo('/dashboard');
  } catch (e) {
    err.textContent = apiErrorMessage(e);
    err.className = 'login-alert';
  } finally {
    btn.disabled = false;
  }
}

// ═══════════════════════════════════════════════════════════
// DASHBOARD ("My Work")
// ═══════════════════════════════════════════════════════════
async function viewDashboard() {
  render(t('nav.myWork'), loadingState(), { nav:'dashboard' });
  try {
    const dash = await api.dashboard();
    const { assignedTasks=[], reviewingTasks=[], projects=[], upcomingReleases=[] } = dash;
    const projName = Object.fromEntries(projects.map(p => [p.id, p.name]));

    const dashTaskCard = (tk) => `
      <button type="button" class="card task-card" data-act="openTask" data-a0="${esc(tk.id)}" data-a1="${esc(tk.projectId)}">
        <div class="task-card-top">
          ${typeGlyph(tk.taskType)}
          <span class="task-title">${esc(tk.title)}</span>
        </div>
        <div class="task-card-meta">
          ${statusBadge(tk.status)} ${prioBadge(tk.priority)}
          <span class="meta-spacer muted" style="font-size:.6875rem">${esc(projName[tk.projectId]||'')}</span>
        </div>
      </button>`;

    const section = (title, count, items, renderItem, emptyMsg) => `
      <section class="page-section">
        <h2 class="section-head">${esc(title)} <span class="count">${count}</span></h2>
        ${items.length ? items.map(renderItem).join('') : `<p class="muted" style="font-size:.875rem">${esc(emptyMsg)}</p>`}
      </section>`;

    let body = '';
    body += section(t('dashboard.assignedToMe',{count:assignedTasks.length}).replace(/\s*\(\d+\)$/,''), assignedTasks.length, assignedTasks, dashTaskCard, t('dashboard.nothingAssigned'));
    body += section(t('dashboard.inReview',{count:reviewingTasks.length}).replace(/\s*\(\d+\)$/,''), reviewingTasks.length, reviewingTasks, dashTaskCard, t('dashboard.noReviewsPending'));
    body += `<section class="page-section">
      <h2 class="section-head">${esc(t('dashboard.myProjects'))} <span class="count">${projects.length}</span>
        <a class="section-link" data-act="nav" data-a0="/projects" href="#">${t('nav.seeAll')}</a></h2>
      ${projects.length ? projects.slice(0,5).map(projectRow).join('') : `<p class="muted" style="font-size:.875rem">${esc(t('dashboard.noProjects'))}</p>`}
    </section>`;
    if (upcomingReleases.length) {
      body += `<section class="page-section">
        <h2 class="section-head">${esc(t('dashboard.upcomingReleases'))}</h2>
        ${upcomingReleases.map(rel => `<div class="card row-card">
          <span class="row-icon">${icon('release',{size:'sm'})}</span>
          <div class="row-body"><div class="row-title">${esc(rel.name)}</div><div class="row-sub">${esc(fmtDate(rel.dueDate))}</div></div>
        </div>`).join('')}
      </section>`;
    }
    el('#content').innerHTML = body;
  } catch (e) {
    el('#content').innerHTML = errorState(e, 'reloadRoute');
  }
}

function projectRow(p) {
  return `<button type="button" class="card row-card" data-act="nav" data-a0="/projects/${esc(p.id)}/board">
    <span class="row-icon">${icon('project',{size:'sm'})}</span>
    <div class="row-body">
      <div class="row-title">${esc(p.name)}</div>
      <div class="row-sub">${esc(p.abbreviation || p.slug || '')}</div>
    </div>
    <span class="chev">${icon('chevron-right',{size:'sm'})}</span>
  </button>`;
}

// ═══════════════════════════════════════════════════════════
// PROJECTS LIST
// ═══════════════════════════════════════════════════════════
async function viewProjects() {
  render(t('nav.projects'), loadingState(), { nav:'projects' });
  try {
    const projects = await api.projects.list();
    el('#content').innerHTML = projects.length
      ? `<div class="card-list">${projects.map(projectRow).join('')}</div>`
      : emptyState('project', t('dashboard.noProjects'), t('mobile.noProjectsBody'));
  } catch (e) {
    el('#content').innerHTML = errorState(e, 'reloadRoute');
  }
}

// ═══════════════════════════════════════════════════════════
// BOARD (column switcher)
// ═══════════════════════════════════════════════════════════
async function viewBoard(pid) {
  render('…', loadingState(), { back:`/projects`, nav:S.lastList,
    action:'' });
  try {
    await ensureProject(pid);
    const [board, tasks] = await Promise.all([
      api.boards.getDefault(pid).catch(() => null),
      api.tasks.listAll(pid),
    ]);
    S.board = board;
    const cols = board?.columns || [];
    const live = tasks.filter(tk => tk.status !== 'ARCHIVED');
    const byCol = boardBuckets(cols, live);
    // Route entry is the refetch point: cache what the column tabs need so a tap
    // between lanes costs no network and no full #app-root rewrite.
    S.taskCache = { projectId: pid, board, tasks: live, byCol };

    if (!cols.length) {
      renderProjectShell(t('nav.board'), pid, 'board',
        emptyState('board', t('errors.noBoardAvailable'), '') + desktopCta());
      return;
    }
    if (!S.boardCol || !byCol[S.boardCol]) S.boardCol = cols[0].id;

    renderProjectShell(S.project.name, pid, 'board',
      `<div class="seg-scroll" role="tablist">${boardSegsHtml(cols, byCol)}</div>
       <div class="content" style="padding-top:var(--space-2)"><div class="card-list">${boardListHtml(byCol)}</div></div>`,
      true);
  } catch (e) {
    el('#content').innerHTML = errorState(e, 'reloadRoute');
  }
}

// boardSegsHtml / boardListHtml are the two pieces a column tap changes. Shared
// by the full board render and the in-place update below so they cannot drift.
function boardSegsHtml(cols, byCol) {
  return cols.map(c => `
      <button type="button" class="seg${S.boardCol===c.id?' active':''}" data-act="selectBoardCol" data-a0="${esc(c.id)}">
        ${esc(c.name)} <span class="seg-count">${byCol[c.id].length}</span>
      </button>`).join('');
}
function boardListHtml(byCol) {
  const colTasks = byCol[S.boardCol] || [];
  return colTasks.length
    ? colTasks.map(boardCard).join('')
    : emptyState('board', t('task.emptyState'), '');
}

// project view shell with board/backlog toggle in the app bar + FAB
function renderProjectShell(title, pid, sub, bodyHtml, flush) {
  const action = `
    <button class="icon-btn" data-act="nav" data-a0="/projects/${esc(pid)}/${sub==='board'?'backlog':'board'}"
      aria-label="${sub==='board'?t('nav.backlog'):t('nav.board')}" title="${sub==='board'?t('nav.backlog'):t('nav.board')}">
      ${icon(sub==='board'?'backlog':'board')}
    </button>`;
  render(typeof title==='string'?title:'', '', { back:'/projects', action, nav:S.lastList, flush });
  el('#content').innerHTML = bodyHtml;
  // FAB to create a task
  const fab = document.createElement('button');
  fab.className = 'fab';
  fab.setAttribute('aria-label', t('task.create'));
  fab.dataset.act = 'nav'; fab.dataset.a0 = `/projects/${pid}/new`;
  fab.innerHTML = icon('add');
  el('.app').appendChild(fab);
}

function boardCard(tk) {
  return `<div class="card task-card" data-act="openTask" data-a0="${esc(tk.id)}" data-a1="${esc(tk.projectId||S.project?.id)}">
    <div class="task-card-top">
      ${typeGlyph(tk.taskType)}
      <span class="task-title">${esc(tk.title)}</span>
      <button class="icon-btn" data-act="openMoveSheet" data-a0="${esc(tk.id)}" aria-label="${t('mobile.move')}" title="${t('mobile.move')}">${icon('more',{size:'sm'})}</button>
    </div>
    <div class="task-card-meta">
      ${tk.seqNumber!=null?`<span class="task-key">${esc(taskKey(tk,S.project))}</span>`:''}
      ${prioBadge(tk.priority)}
      ${estimateChip(tk)}
      ${tk.assigneeId?avatar(tk.assigneeId,true):''}
      ${tk.dueDate?`<span class="meta-spacer muted" style="font-size:.6875rem">${icon('calendar',{size:'sm'})} ${esc(fmtDate(tk.dueDate))}</span>`:''}
    </div>
  </div>`;
}

// selectBoardCol switches board lanes. The lanes are already in hand — the
// bucket map cached by viewBoard — so it repaints only the segment strip (the
// counts stay, the active class moves) and the card list, with no fetch, no
// skeleton and no #app-root rewrite. Anything unexpected (no cache, another
// project's cache, a column the board no longer has, a DOM that isn't the board)
// falls back to the full view, which refetches.
function selectBoardCol(colId) {
  const pid = S.project?.id;
  const cache = taskCacheFor(pid);
  const segWrap = el('#content .seg-scroll');
  const listWrap = el('#content .card-list');
  const cols = cache?.board?.columns || [];
  if (!cache || !cache.byCol || !cols.length || !cache.byCol[colId] || !segWrap || !listWrap) {
    S.boardCol = colId;
    if (pid) viewBoard(pid);
    return;
  }
  S.boardCol = colId;
  segWrap.innerHTML = boardSegsHtml(cols, cache.byCol);
  listWrap.innerHTML = boardListHtml(cache.byCol);
  document.querySelector('.content')?.scrollTo?.(0,0);
}

// ═══════════════════════════════════════════════════════════
// BACKLOG / TASK LIST
// ═══════════════════════════════════════════════════════════
async function viewBacklog(pid) {
  render('…', loadingState(), { back:'/projects', nav:S.lastList });
  try {
    await ensureProject(pid);
    const tasks = (await api.tasks.listAll(pid)).filter(tk => tk.status !== 'ARCHIVED');
    // Route entry is the refetch point. Keep the board fields the previous entry
    // may have cached only if they belong to this project; otherwise drop them,
    // so selectBoardCol never reads a board that was never fetched here.
    const prev = taskCacheFor(pid);
    S.taskCache = { projectId: pid, board: prev?.board, tasks, byCol: prev?.board ? boardBuckets(prev.board.columns || [], tasks) : null };

    render(S.project.name, '', { back:'/projects', nav:S.lastList, flush:true,
      action:`${backlogFilterAction()}<button class="icon-btn" data-act="nav" data-a0="/projects/${esc(pid)}/board" aria-label="${t('nav.board')}" title="${t('nav.board')}">${icon('board')}</button>` });
    el('#content').innerHTML = backlogBodyHtml(tasks);
    // FAB
    const fab = document.createElement('button');
    fab.className = 'fab'; fab.setAttribute('aria-label', t('task.create'));
    fab.dataset.act='nav'; fab.dataset.a0=`/projects/${pid}/new`; fab.innerHTML=icon('add');
    el('.app').appendChild(fab);
  } catch (e) {
    el('#content').innerHTML = errorState(e, 'reloadRoute');
  }
}

function hasActiveFilters() { return !!(S.filters.status || S.filters.priority || S.filters.type); }

function backlogFilterAction() {
  return `<button class="icon-btn${hasActiveFilters()?' active':''}" data-act="openFilterSheet"
      aria-label="${t('filter.title')}" title="${t('filter.title')}">${icon('filter')}</button>`;
}

// backlogBodyHtml is chips + card list for a given task list. The filters are
// pure predicates over that list, which is why changing one needs no fetch.
function backlogBodyHtml(tasks) {
  const activeFilters = hasActiveFilters();
  let shown = tasks;
  if (S.filters.status)   shown = shown.filter(tk => tk.status === S.filters.status);
  if (S.filters.priority) shown = shown.filter(tk => tk.priority === S.filters.priority);
  if (S.filters.type)     shown = shown.filter(tk => tk.taskType === S.filters.type);
  const list = shown.length
    ? `<div class="content"><div class="card-list">${shown.map(tk=>listCard(tk)).join('')}</div></div>`
    : emptyState('backlog', t('task.emptyState'), activeFilters ? t('filter.noMatch') : t('task.emptyBody'));
  return (activeFilters?filterChips():'') + list;
}

// refilterBacklog re-renders the backlog body from the cached list. The appbar's
// filter button is the only thing outside #content that a filter change touches,
// so its active class is toggled by hand rather than rebuilding the shell.
function refilterBacklog() {
  const pid = S.project?.id;
  if (!pid) return;
  const cache = taskCacheFor(pid);
  const content = el('#content');
  const filterBtn = el('[data-act="openFilterSheet"]');
  if (!cache || !content || !filterBtn) { viewBacklog(pid); return; }
  filterBtn.classList.toggle('active', hasActiveFilters());
  content.innerHTML = backlogBodyHtml(cache.tasks);
  document.querySelector('.content')?.scrollTo?.(0,0);
}

function filterChips() {
  const chips = [];
  if (S.filters.status)   chips.push(STATUS_META[S.filters.status]?.label);
  if (S.filters.priority) chips.push(priorityMeta(S.filters.priority).label);
  if (S.filters.type)     chips.push(TYPE_META[S.filters.type]?.label);
  return `<div class="seg-scroll">
    ${chips.map(c=>`<span class="seg active">${esc(c)}</span>`).join('')}
    <button class="seg" data-act="clearFilters">${t('filter.clear')}</button>
  </div>`;
}

function listCard(tk) {
  return `<div class="card task-card" data-act="openTask" data-a0="${esc(tk.id)}" data-a1="${esc(tk.projectId||S.project?.id)}">
    <div class="task-card-top">
      ${typeGlyph(tk.taskType)}
      <span class="task-title">${esc(tk.title)}</span>
    </div>
    <div class="task-card-meta">
      ${tk.seqNumber!=null?`<span class="task-key">${esc(taskKey(tk,S.project))}</span>`:''}
      ${statusBadge(tk.status)} ${prioBadge(tk.priority)}
      ${estimateChip(tk)}
      ${tk.assigneeId?avatar(tk.assigneeId,true):''}
    </div>
  </div>`;
}

function openFilterSheet() {
  const group = (label, key, opts) => `
    <div class="sheet-title" style="margin-top:var(--space-3)">${esc(label)}</div>
    <button type="button" class="sheet-opt${!S.filters[key]?' selected':''}" data-act="applyFilter" data-a0="${key}" data-a1="">
      <span>${t('filter.all')}</span>${!S.filters[key]?`<span class="check">${icon('check',{size:'sm'})}</span>`:''}
    </button>
    ${opts.map(o=>`<button type="button" class="sheet-opt${S.filters[key]===o.v?' selected':''}" data-act="applyFilter" data-a0="${key}" data-a1="${o.v}">
      <span>${o.labelHtml||esc(o.label)}</span>${S.filters[key]===o.v?`<span class="check">${icon('check',{size:'sm'})}</span>`:''}</button>`).join('')}`;
  // Built-ins plus the project's custom lane statuses — from the cached board
  // when the board view fetched it, and from the statuses actually on the
  // cached tasks otherwise, so a custom stage is filterable either way.
  const cache = taskCacheFor(S.project?.id);
  const laneStatuses = (cache?.board?.columns || []).map(c => c.status);
  const taskStatuses = (cache?.tasks || []).map(tk => tk.status);
  const statusOpts = [...new Set([...STATUSES, ...laneStatuses, ...taskStatuses])]
    .filter(s => s && s !== 'ARCHIVED');
  openSheet(t('filter.title'),
    group(t('task.statusLabel'), 'status', statusOpts.map(s=>({v:s,labelHtml:statusBadge(s)})))
    + group(t('task.priorityLabel'), 'priority', priorityNames(S.priorities).map(p=>({v:p,labelHtml:prioBadge(p)})))
    + group(t('task.typeLabel'), 'type', projectTaskTypes(S.project).map(t2=>({v:t2,label:TYPE_META[t2].label}))));
}
function applyFilter(key, value) {
  S.filters[key] = value;
  closeSheet();
  refilterBacklog();
}
function clearFilters() {
  S.filters = { status:'', priority:'', type:'' };
  refilterBacklog();
}

// ═══════════════════════════════════════════════════════════
// TASK DETAIL (full screen)
// ═══════════════════════════════════════════════════════════
let _task = null;
async function viewTask(taskId) {
  render('…', loadingState(), { back: backForTask() });
  try {
    // The comments only need the task id, so they no longer wait behind the task
    // and the project: three serial round trips become two stages. The .catch is
    // attached here, synchronously, so a comments failure can neither escape as
    // an unhandled rejection while we await the task nor fail-fast the Promise.all
    // below into a blank task page — a missing comment list degrades to an empty
    // one, while a failing tasks.get still lands in errorState as before.
    const commentsPromise = api.comments.list(taskId).catch(() => []);
    const task = await api.tasks.get(taskId);
    _task = task;
    // ensureProject genuinely depends on the task's projectId, so it cannot start
    // any earlier; run it alongside the already-in-flight comments fetch.
    const [comments] = await Promise.all([
      commentsPromise,
      task.projectId ? ensureProject(task.projectId).catch(() => {}) : Promise.resolve(),
    ]);
    renderTask(task, comments);
  } catch (e) {
    el('#content').innerHTML = errorState(e, 'reloadRoute');
  }
}
function backForTask() {
  if (S.project) return `/projects/${S.project.id}/${S.lastList==='projects'?'board':'board'}`;
  return '/dashboard';
}

function renderTask(task, comments) {
  const desc = renderDescriptionHTML(task.description);
  const action = `<button class="icon-btn" data-act="openTaskMenu" aria-label="${t('common.more')}" title="${t('common.more')}">${icon('kebab')}</button>`;
  render(taskKey(task, S.project) || t('nav.tasks'), '', { back: backForTask(), action });
  el('#content').innerHTML = html`
    <div class="detail-head">
      <div style="flex:1;min-width:0">
        ${task.seqNumber!=null?raw(html`<div class="detail-key">${taskKey(task,S.project)}</div>`):''}
        <div class="detail-title">${task.title}</div>
      </div>
    </div>
    <div class="detail-chips">
      ${raw(typeGlyph(task.taskType))}
      ${raw(statusBadge(task.status))}
      ${raw(prioBadge(task.priority))}
    </div>

    <div class="prop-list">
      <button class="prop" data-act="openStatusSheet" data-a0="${task.id}">
        <span class="prop-label">${raw(t('task.statusLabel'))}</span>
        <span class="prop-value">${raw(statusBadge(task.status))}</span>
        <span class="chev">${raw(icon('chevron-right',{size:'sm'}))}</span>
      </button>
      <button class="prop" data-act="openPrioritySheet" data-a0="${task.id}">
        <span class="prop-label">${raw(t('task.priorityLabel'))}</span>
        <span class="prop-value">${raw(prioBadge(task.priority))}</span>
        <span class="chev">${raw(icon('chevron-right',{size:'sm'}))}</span>
      </button>
      <button class="prop" data-act="openAssigneeSheet" data-a0="${task.id}">
        <span class="prop-label">${raw(t('task.assignee'))}</span>
        <span class="prop-value">${task.assigneeId?raw(avatar(task.assigneeId,true)):''} ${task.assigneeId?memberName(task.assigneeId):t('task.unassigned')}</span>
        <span class="chev">${raw(icon('chevron-right',{size:'sm'}))}</span>
      </button>
      ${taskEstimatable(S.project, task)?raw(html`<button class="prop" data-act="openEstimateSheet" data-a0="${task.id}">
        <span class="prop-label">${raw(estimateLabel(S.project))}</span>
        <span class="prop-value">${estimateText(S.project, task) || t('task.estimateNone')}</span>
        <span class="chev">${raw(icon('chevron-right',{size:'sm'}))}</span>
      </button>`):''}
      ${task.dueDate?raw(html`<div class="prop">
        <span class="prop-label">${raw(t('task.dueDateLabel'))}</span>
        <span class="prop-value">${raw(icon('calendar',{size:'sm'}))} ${fmtDate(task.dueDate)}</span>
      </div>`):''}
      <div class="prop">
        <span class="prop-label">${raw(t('task.creator'))}</span>
        <span class="prop-value">${task.reporterId?raw(avatar(task.reporterId,true)):''} ${memberName(task.reporterId)}</span>
      </div>
    </div>

    ${desc ? raw(html`<div class="detail-block-title">${raw(t('task.description'))}</div><div class="rich">${raw(desc)}</div>`) : ''}

    <div class="detail-block-title">${raw(t('task.comments'))} (${comments.length})</div>
    <div id="comment-list">${comments.length?raw(comments.map(commentItem).join('')):raw(html`<p class="muted" style="font-size:.875rem">${raw(t('task.noComments'))}</p>`)}</div>
    <form class="comment-form" data-submit="submitComment" data-a0="${task.id}">
      <textarea class="textarea" id="comment-input" rows="2" placeholder="${t('task.addComment')}"></textarea>
      <button class="btn btn-primary" type="submit">${raw(icon('comment',{size:'sm'}))} ${raw(t('task.comment'))}</button>
    </form>`;
}

function commentItem(c) {
  // Prefer the author display name the API now ships with each comment; fall
  // back to the project-member lookup for older payloads so the author is never
  // shown as a raw id.
  const name = c.authorName || memberName(c.authorId);
  const token = c.authorId && S.membersMap[c.authorId] ? S.membersMap[c.authorId].avatarUpdatedAt : null;
  const avatarChip = c.authorId && token
    ? `<span class="avatar sm has-avatar" title="${esc(name||t('task.unassigned'))}">${esc(initials(name))}<img class="avatar-img" alt="" aria-hidden="true" data-avatar-user="${esc(c.authorId)}" data-avatar-v="${esc(token)}"></span>`
    : `<span class="avatar sm" title="${esc(name||t('task.unassigned'))}">${esc(initials(name))}</span>`;
  return `<div class="comment">
    <div class="comment-head">
      ${avatarChip}
      <span class="comment-author">${esc(name)}</span>
      <span class="comment-time">${esc(fmtRelative(c.createdAt))}</span>
    </div>
    <div class="comment-body">${renderDescriptionHTML(c.text)}</div>
  </div>`;
}

async function submitComment(form, ev) {
  ev.preventDefault();
  const ta = el('#comment-input');
  const text = ta.value.trim();
  if (!text) return;
  const taskId = form.dataset.a0;
  ta.disabled = true;
  invalidateTaskCache();   // no card field changes, but never leave a write uncovered
  try {
    await api.comments.add(taskId, text);
    ta.value = '';
    const comments = await api.comments.list(taskId).catch(() => []);
    el('#comment-list').innerHTML = comments.length?comments.map(commentItem).join(''):'';
    toast(t('task.commentAdded'), 'success');
  } catch (e) { toast(apiErrorMessage(e), 'error'); }
  finally { ta.disabled = false; }
}

// ── property edit sheets ──
async function openStatusSheet(taskId) {
  const title = t('task.statusLabel');
  // DONE/ARCHIVED tasks are immutable on the API (TASK_IMMUTABLE) — offer the
  // dedicated reopen action instead of status options that can only fail.
  if (_task && _task.id === taskId && (_task.status === 'DONE' || _task.status === 'ARCHIVED')) {
    openSheet(title, `
      <p class="row-sub" style="white-space:normal;margin-bottom:var(--space-2)">${esc(t('task.immutableHint'))}</p>
      <button type="button" class="sheet-opt" data-act="pickReopen" data-a0="${esc(taskId)}">${icon('refresh')}<span>${esc(t('task.reopen'))}</span></button>`);
    return;
  }
  // ARCHIVED is not offered as a target: archiving goes through its own
  // endpoint/lifecycle, and the status endpoint treats it as a normal write.
  // Built-ins plus the board's custom lane statuses (plus the task's own, so
  // the current selection renders even when its lane is gone) — a custom lane
  // is a real stage of the workflow and must be reachable from the phone too.
  const board = (S.board && S.board.projectId === _task?.projectId)
    ? S.board
    : await api.boards.getDefault(_task?.projectId).catch(() => null);
  const laneStatuses = (board?.columns || []).map(c => c.status);
  const options = [...new Set([...STATUSES, ...laneStatuses, _task?.status])]
    .filter(s => s && s !== 'ARCHIVED');
  openSheet(title,
    sheetOptions(options.map(s=>({id:taskId, value:s, labelHtml:statusBadge(s)})), _task?.status, 'pickStatus'));
}
// statusLane resolves the board column a status change has to move the task
// into, mirroring the desktop panel: status owns board placement, so setting one
// moves the card to the lane carrying it — and a task on no board joins the
// board rather than taking its new status out of sight in the backlog.
//
// Mobile only ever loads a project's *default* board, so there is no sprint
// board to guard against here. The cached board is still re-read when it belongs
// to another project: a task can be opened by deep link without the board view
// ever having run.
async function statusLane(task, status) {
  if (!task?.projectId) return null;
  const board = (S.board && S.board.projectId === task.projectId)
    ? S.board
    : await api.boards.getDefault(task.projectId).catch(() => null);
  const col = (board?.columns || []).find(c => c.status === status);
  return (col && col.id !== task.boardColumnId) ? { board, col } : null;
}
async function pickStatus(taskId, status) {
  closeSheet();
  // Ask before the write, not after: the sheet is already closed, so a "no"
  // simply leaves the task where it was and nothing needs repainting (unlike
  // the desktop's select, which has to snap back).
  if (status === 'DONE' && !await confirmCompletionOverOpenDescendants(taskId)) return;
  invalidateTaskCache();   // status moves the task between board lanes
  try {
    const task = (_task && _task.id === taskId) ? _task : null;
    const target = await statusLane(task, status);
    if (target) {
      // The move bumps the task's version, so the status write has to carry the
      // new one — sending the pre-move version would 409 against our own write.
      const moved = await api.boards.move(target.board.id,
        { taskId, boardColumnId: target.col.id, boardRank: 1000, version: taskVersionFor(taskId) });
      if (_task && _task.id === taskId && moved) _task = moved;
    }
    await api.tasks.status(taskId, status, taskVersionFor(taskId));
    toast(t('form.updated'), 'success');
    viewTask(taskId);
  }
  catch (e) { toast(apiErrorMessage(e), 'error'); viewTask(taskId); }
}
async function pickReopen(taskId) {
  closeSheet();
  invalidateTaskCache();   // reopen is a status change
  try { await api.tasks.reopen(taskId); toast(t('task.reopened'), 'success'); viewTask(taskId); }
  catch (e) { toast(apiErrorMessage(e), 'error'); viewTask(taskId); }
}
// taskVersionFor returns the loaded task's version so property edits carry it
// and a stale snapshot 409s instead of overwriting a concurrent editor. The
// error paths above re-render the task so the state that won becomes visible.
function taskVersionFor(taskId) {
  return (_task && _task.id === taskId) ? _task.version : undefined;
}
function openPrioritySheet(taskId) {
  openSheet(t('task.priorityLabel'),
    sheetOptions(priorityNames(S.priorities).map(p=>({id:taskId, value:p, labelHtml:prioBadge(p)})), _task?.priority, 'pickPriority'));
}
async function pickPriority(taskId, priority) {
  closeSheet();
  invalidateTaskCache();   // the priority badge is on every card
  try { await api.tasks.priority(taskId, priority, taskVersionFor(taskId)); toast(t('form.updated'), 'success'); viewTask(taskId); }
  catch (e) { toast(apiErrorMessage(e), 'error'); viewTask(taskId); }
}
// ── Effort estimate ─────────────────────────────────────────────────────────
// The estimate has no dedicated endpoint: it is a field on the version-guarded
// PATCH /tasks/{id}, like the desktop panel. The sheet is a free number box for
// both units — the Fibonacci row above it in the points case is a one-tap
// shortcut over that same field, not a scale the server enforces.
function openEstimateSheet(taskId) {
  const unit    = estimationUnit(S.project);
  const current = taskEstimate(S.project, _task);
  const chips = unit === 'POINTS'
    ? `<div class="est-presets">${FIBONACCI_POINTS.map(n => `
        <button type="button" class="est-preset${current === n ? ' selected' : ''}"
          data-act="pickEstimate" data-a0="${esc(taskId)}" data-a1="${n}">${n}</button>`).join('')}</div>`
    : '';
  openSheet(estimateLabel(S.project), `
    ${chips}
    <form class="est-form" data-submit="saveEstimate" data-a0="${esc(taskId)}">
      <input class="input" id="est-input" type="number" inputmode="decimal"
        aria-label="${esc(estimateLabel(S.project))}" placeholder="${esc(t('task.estimateNone'))}"
        min="0" max="${estimateLimits(unit).max}" step="${estimateLimits(unit).step}"
        value="${current === null ? '' : esc(String(current))}">
      <button class="btn btn-primary" type="submit">${esc(t('form.save'))}</button>
    </form>
    ${current === null ? '' : `<button type="button" class="sheet-opt" data-act="clearEstimate" data-a0="${esc(taskId)}">
      <span>${esc(t('task.estimateClear'))}</span>
    </button>`}`);
}
// pickEstimate applies a Fibonacci chip. Tapping the active chip clears the
// estimate, so the shortcut undoes itself without reaching for the input.
async function pickEstimate(taskId, n) {
  const value = taskEstimate(S.project, _task) === Number(n) ? null : Number(n);
  await saveEstimateValue(taskId, value);
}
function clearEstimate(taskId) { return saveEstimateValue(taskId, null); }
// saveEstimate reads the box: empty means *unestimated* (null), while "0" is a
// deliberate estimate of no effort, so the two must not collapse the way
// `value || null` would fold them.
async function saveEstimate(form, ev) {
  ev.preventDefault();
  const taskId = form.dataset.a0;
  const value = parseEstimateInput(el('#est-input')?.value);
  if (value === undefined) { toast(t('task.estimateInvalid'), 'error'); return; }
  await saveEstimateValue(taskId, value);
}
async function saveEstimateValue(taskId, value) {
  closeSheet();
  invalidateTaskCache();   // the estimate chip is on every card
  try {
    await api.tasks.update(taskId, { [estimationField(S.project)]: value, version: taskVersionFor(taskId) });
    toast(t('form.updated'), 'success');
    viewTask(taskId);
  } catch (e) { toast(apiErrorMessage(e), 'error'); viewTask(taskId); }
}
// assignableLabel names an assignee candidate, marking the global admins who
// are not members of this project — project access still follows membership for
// everyone below Super Admin.
function assignableLabel(m) {
  const name = m.name || m.email;
  return m.member === false ? t('task.globalAdmin', { name }) : name;
}

// warnIfNotMember flags a global admin who was just given work but holds no
// membership on this project: project access follows membership for everyone
// below Super Admin, so they would get a 403 on the task. The desktop app offers
// to add them; on a phone this is a heads-up only — granting project membership
// is not a one-handed action.
function warnIfNotMember(userId) {
  const person = (S.assignables || []).find(m => m.userId === userId);
  if (!person || person.member !== false || person.globalRole === 'SUPER_ADMIN') return;
  toast(t('task.notMemberAsk', { name: person.name || person.email }), 'info');
}

function openAssigneeSheet(taskId) {
  const opts = [{ id:taskId, value:'', label:t('task.unassigned') }]
    .concat(S.assignables.map(m => ({ id:taskId, value:m.userId, label:assignableLabel(m) })));
  openSheet(t('task.assignee'),
    sheetOptions(opts, _task?.assigneeId || '', 'pickAssignee'));
}
async function pickAssignee(taskId, userId) {
  closeSheet();
  invalidateTaskCache();   // the assignee avatar is on every card
  try { await api.tasks.assign(taskId, { assigneeId: userId || null, version: taskVersionFor(taskId) }); toast(t('form.updated'), 'success'); warnIfNotMember(userId); viewTask(taskId); }
  catch (e) { toast(apiErrorMessage(e), 'error'); viewTask(taskId); }
}
function openTaskMenu() {
  if (!_task) return;
  openSheet(_task.title, `
    <button type="button" class="sheet-opt" data-act="openStatusSheet" data-a0="${esc(_task.id)}">${icon('check')}<span>${t('task.statusLabel')}</span></button>
    <button type="button" class="sheet-opt" data-act="openMoveSheet" data-a0="${esc(_task.id)}">${icon('board')}<span>${t('mobile.move')}</span></button>
    ${IS_PHONE ? '' : `<a class="sheet-opt" href="${esc(DESKTOP_URL)}">${icon('external')}<span>${t('mobile.editOnDesktop')}</span></a>`}`);
}

// ── board move sheet ──
async function openMoveSheet(taskId) {
  let board = S.board;
  if (!board && S.project) board = await api.boards.getDefault(S.project.id).catch(()=>null);
  if (!board || !board.columns?.length) { toast(t('errors.noBoardAvailable'), 'error'); return; }
  openSheet(t('mobile.moveToColumn'),
    sheetOptions(board.columns.map(c=>({id:taskId, value:c.id, label:c.name})), null, 'pickMove'));
}
async function pickMove(taskId, colId) {
  closeSheet();
  const board = S.board;
  if (!board) return;
  // Dropping into a Done lane sets the task's status server-side — the same
  // completion this warns about, reached through a different door.
  const target = (board.columns || []).find(c => c.id === colId);
  if (target?.status === 'DONE' && !await confirmCompletionOverOpenDescendants(taskId, board.projectId)) return;
  invalidateTaskCache();   // the move re-buckets and re-ranks the lanes
  try {
    await api.boards.move(board.id, { taskId, boardColumnId: colId, boardRank: 1000, version: taskVersionFor(taskId) });
    toast(t('form.updated'), 'success');
    if (S.project) viewBoard(S.project.id);
  } catch (e) { toast(apiErrorMessage(e), 'error'); if (S.project) viewBoard(S.project.id); }
}

// ═══════════════════════════════════════════════════════════
// CREATE TASK (full screen form)
// ═══════════════════════════════════════════════════════════
async function viewCreateTask(pid) {
  render(t('task.create'), loadingState(), { back:`/projects/${pid}/board` });
  try {
    await ensureProject(pid);
    // Live tasks feed the hierarchy parent picker (see typeParentRule in meta.js).
    _ctParents = ((await api.tasks.listAll(pid).catch(()=>[])) || [])
      .filter(tk => tk.status !== 'ARCHIVED');
    el('#content').innerHTML = html`
      <form data-submit="submitCreateTask" data-a0="${pid}">
        <div class="field">
          <label class="field-label" for="ct-title">${raw(t('form.title'))}</label>
          <input class="input" id="ct-title" required autofocus placeholder="${t('task.titlePlaceholder')}">
          <div class="form-error hidden" id="ct-title-err">${raw(t('validation.titleRequired'))}</div>
        </div>
        <div class="field">
          <label class="field-label" for="ct-type">${raw(t('task.typeLabel'))}</label>
          <select class="select" id="ct-type" data-change="ctTypeChanged">${projectTaskTypes(S.project).map(tt=>raw(html`<option value="${tt}">${TYPE_META[tt].label}</option>`))}</select>
        </div>
        <div class="field" id="ct-parent-field">${raw(ctParentFieldHtml('TASK'))}</div>
        <div class="field">
          <label class="field-label" for="ct-priority">${raw(t('task.priorityLabel'))}</label>
          <select class="select" id="ct-priority">${priorityNames(S.priorities).map(p=>raw(html`<option value="${p}" ${raw(p==='MEDIUM'?'selected':'')}>${priorityMeta(p).label}</option>`))}</select>
        </div>
        <div class="field" id="ct-estimate-field">${raw(ctEstimateFieldHtml('TASK'))}</div>
        <div class="field">
          <label class="field-label" for="ct-assignee">${raw(t('task.assignee'))}</label>
          <select class="select" id="ct-assignee">
            <option value="">${raw(t('task.unassigned'))}</option>
            ${S.assignables.map(m=>raw(html`<option value="${m.userId}">${assignableLabel(m)}</option>`))}
          </select>
        </div>
        <div class="field">
          <label class="field-label" for="ct-due">${raw(t('task.dueDateLabel'))}</label>
          <input class="input" id="ct-due" type="date">
        </div>
        <div class="field">
          <label class="field-label" for="ct-desc">${raw(t('task.description'))}</label>
          <textarea class="textarea" id="ct-desc" placeholder="${t('task.descriptionPlaceholder')}"></textarea>
        </div>
        <button class="btn btn-primary btn-block" type="submit" id="ct-submit">${raw(t('task.create'))}</button>
      </form>`;
  } catch (e) {
    el('#content').innerHTML = errorState(e, 'reloadRoute');
  }
}

// Live, non-archived project tasks cached while the create form is open, so
// the parent picker re-renders on a type change without refetching.
let _ctParents = [];

// ctParentFieldHtml renders the parent picker for the chosen type: candidates
// are the tasks of the type one level up in the project's chain
// (typeParentRule); empty for the chain's top type.
function ctParentFieldHtml(taskType) {
  const rule = typeParentRule(S.project, taskType);
  if (!rule.parentType) return '';
  const opts = _ctParents.filter(tk => tk.taskType === rule.parentType);
  const label = t('task.parentLabel');
  return `
          <label class="field-label" for="ct-parent">${esc(label)} (${esc(TYPE_META[rule.parentType].label)})</label>
          <select class="select" id="ct-parent">
            ${rule.required ? '' : `<option value="">${esc(t('task.noParent'))}</option>`}
            ${opts.map(tk=>`<option value="${esc(tk.id)}">${esc((tk.seqNumber!=null?taskKey(tk,S.project)+' — ':'')+tk.title)}</option>`).join('')}
          </select>`;
}

// ctEstimateFieldHtml renders the create form's estimate box under the same two
// gates the API enforces — the project must estimate, and the chosen type must
// be an estimable leaf — so the form can never offer a value that comes back
// 422. Left empty, the task is created unestimated.
function ctEstimateFieldHtml(taskType, value = '') {
  if (!estimationEnabled(S.project) || !estimableType(taskType)) return '';
  const unit = estimationUnit(S.project);
  return `
          <label class="field-label" for="ct-estimate">${esc(estimateLabel(S.project))}</label>
          <input class="input" id="ct-estimate" type="number" inputmode="decimal"
            placeholder="${esc(t('task.estimateNone'))}" min="0"
            max="${estimateLimits(unit).max}" step="${estimateLimits(unit).step}"
            value="${esc(String(value))}">`;
}

function ctTypeChanged(node) {
  const field = el('#ct-parent-field');
  if (field) field.innerHTML = ctParentFieldHtml(node.value);
  // Switching to a container type takes the estimate box away; a move between
  // two leaf types keeps whatever was typed.
  const est = el('#ct-estimate-field');
  if (est) est.innerHTML = ctEstimateFieldHtml(node.value, el('#ct-estimate')?.value || '');
}

async function submitCreateTask(form, ev) {
  ev.preventDefault();
  const pid = form.dataset.a0;
  const title = el('#ct-title').value.trim();
  const errEl = el('#ct-title-err');
  if (!title) { errEl.classList.remove('hidden'); el('#ct-title').focus(); return; }
  errEl.classList.add('hidden');
  const taskType = el('#ct-type').value || 'TASK';
  const parentId = el('#ct-parent')?.value || '';
  if (typeParentRule(S.project, taskType).required && !parentId) {
    toast(t('task.parentRequired'), 'error');
    return;
  }
  const d = {
    title,
    taskType,
    priority: el('#ct-priority').value || 'MEDIUM',
    description: el('#ct-desc').value || '',
  };
  if (parentId) d.parentId = parentId;
  const a = el('#ct-assignee').value; if (a) d.assigneeId = a;
  const due = el('#ct-due').value; if (due) d.dueDate = due;
  // The estimate reaches the API as a number or not at all: an empty box means
  // unestimated, while a typed "0" is a deliberate estimate of no effort.
  if (estimationEnabled(S.project) && estimableType(taskType)) {
    const value = parseEstimateInput(el('#ct-estimate')?.value);
    if (value === undefined) { toast(t('task.estimateInvalid'), 'error'); return; }
    if (value !== null) d[estimationField(S.project)] = value;
  }
  const btn = el('#ct-submit'); btn.disabled = true;
  invalidateTaskCache();   // a new task joins the list and the board's first lane
  try {
    const task = await api.tasks.create(pid, d);
    // place at the end of the board's first column so it shows on the board
    try {
      const board = S.board || await api.boards.getDefault(pid).catch(()=>null);
      const col = board?.columns?.[0];
      if (board && col) await api.boards.move(board.id, { taskId: task.id, boardColumnId: col.id, boardRank: 1000 });
    } catch {}
    toast(t('task.created'), 'success');
    navTo(`/task/${task.id}`);
  } catch (e) {
    toast(apiErrorMessage(e), 'error');
    btn.disabled = false;
  }
}

// ═══════════════════════════════════════════════════════════
// SEARCH
// ═══════════════════════════════════════════════════════════
let _searchTimer = null;
async function viewSearch(initialQ) {
  render(t('nav.search'), `
    <div class="field" style="margin-bottom:var(--space-3)">
      <input class="input" id="search-input" type="search" inputmode="search"
        placeholder="${t('search.placeholder')}"
        value="${esc(initialQ||'')}" autofocus aria-label="${t('nav.search')}">
    </div>
    <div id="search-results"></div>`, { nav:'search' });
  if (initialQ) runSearch(initialQ);
}
function onSearchInput(value) {
  if (_searchTimer) clearTimeout(_searchTimer);
  _searchTimer = setTimeout(() => runSearch(value), 300);
}
async function runSearch(q) {
  const out = el('#search-results');
  if (!out) return;
  if (!q || q.trim().length < 2) { out.innerHTML = html`<p class="muted" style="font-size:.875rem">${raw(t('search.hint'))}</p>`; return; }
  out.innerHTML = loadingState();
  try {
    const res = await api.search(q.trim());
    const tasks = (res.tasks || res.results || []).filter(Boolean);
    out.innerHTML = tasks.length
      ? html`<div class="card-list">${tasks.map(tk => raw(searchCard(tk)))}</div>`
      : emptyState('search', t('search.noResults'), '');
  } catch (e) {
    out.innerHTML = errorState(e);
  }
}
function searchCard(tk) {
  return `<button type="button" class="card task-card" data-act="openTask" data-a0="${esc(tk.id)}" data-a1="${esc(tk.projectId)}">
    <div class="task-card-top">${typeGlyph(tk.taskType)}<span class="task-title">${esc(tk.title)}</span></div>
    <div class="task-card-meta">${statusBadge(tk.status)} ${tk.priority?prioBadge(tk.priority):''}</div>
  </button>`;
}

// ═══════════════════════════════════════════════════════════
// NOTIFICATIONS
// ═══════════════════════════════════════════════════════════
async function viewNotifications() {
  const action = `<button class="icon-btn" data-act="markAllRead" aria-label="${t('notifications.markAllRead')}" title="${t('notifications.markAllRead')}">${icon('check')}</button>`;
  render(t('notifications.title'), loadingState(), { nav:'notifications', action });
  try {
    const res = await api.notifications.list({ size:50 });
    const items = res.notifications || res.items || res || [];
    S.notifCount = items.filter(n => !n.isRead).length;
    el('#content').innerHTML = items.length
      ? `<div class="card-list">${items.map(notifCard).join('')}</div>`
      : emptyState('bell', t('notifications.empty'), '');
    // refresh the nav badge
    el('.nav-item.active .nav-badge')?.remove();
  } catch (e) {
    el('#content').innerHTML = errorState(e, 'reloadRoute');
  }
}
function notifCard(n) {
  const title = n.title || n.message || n.type || '';
  const body = n.body || (n.title ? n.message : '') || '';
  return `<button type="button" class="card row-card${n.isRead?'':' notif-unread'}" data-act="openNotif" data-a0="${esc(n.id)}" data-a1="${esc(n.taskId||'')}" data-a2="${esc(n.projectId||'')}">
    <span class="row-icon">${icon('bell',{size:'sm'})}</span>
    <div class="row-body">
      <div class="row-title" style="white-space:normal">${esc(title)}</div>
      ${body?`<div class="row-sub" style="white-space:normal">${esc(body)}</div>`:''}
      <div class="row-sub">${esc(fmtRelative(n.createdAt))}</div>
    </div>
  </button>`;
}
async function openNotif(id, taskId, projectId) {
  try { await api.notifications.markRead(id); } catch {}
  if (taskId) navTo(`/task/${taskId}`);
  else if (projectId) navTo(`/projects/${projectId}/board`);
  else viewNotifications();
}
async function markAllRead() {
  try { await api.notifications.readAll(); S.notifCount = 0; toast(t('form.updated'), 'success'); viewNotifications(); }
  catch (e) { toast(apiErrorMessage(e), 'error'); }
}

// ═══════════════════════════════════════════════════════════
// PROFILE SHEET
// ═══════════════════════════════════════════════════════════
async function avatarPickMobile(node) {
  const file = node.files && node.files[0];
  node.value = '';
  if (!file) return;
  try {
    const res = await api.users.uploadAvatar(file);
    _applySelfAvatarMobile(res.avatarUpdatedAt);
    toast(t('settings.profileUpdated'), 'success');
    openProfile();
  } catch (e) {
    toast(apiErrorMessage(e), 'error');
  }
}
async function avatarRemoveMobile() {
  try {
    await api.users.deleteAvatar();
    _applySelfAvatarMobile(null);
    toast(t('settings.profileRemoved'), 'success');
    openProfile();
  } catch (e) {
    toast(apiErrorMessage(e), 'error');
  }
}
function _applySelfAvatarMobile(token) {
  if (S.user) S.user.avatarUpdatedAt = token;
  if (S.user && S.membersMap[S.user.id]) S.membersMap[S.user.id].avatarUpdatedAt = token;
}

function openProfile() {
  const u = S.user || {};
  const avatarChip = u.avatarUpdatedAt
    ? `<span class="avatar has-avatar" style="width:2.5rem;height:2.5rem">${esc(initials(u.name||u.email))}<img class="avatar-img" alt="" aria-hidden="true" data-avatar-user="${esc(u.id)}" data-avatar-v="${esc(u.avatarUpdatedAt)}"></span>`
    : `<span class="avatar" style="width:2.5rem;height:2.5rem">${esc(initials(u.name||u.email))}</span>`;
  openSheet('', `
    <div class="row-card" style="margin-bottom:var(--space-4)">
      ${avatarChip}
      <div class="row-body">
        <div class="row-title">${esc(u.name||'')}</div>
        <div class="row-sub">${esc(u.email||'')}</div>
      </div>
    </div>
    <label class="sheet-opt" for="avatar-input-mobile">${icon('edit')}<span>${t('settings.profileUpload')}</span></label>
    <input type="file" id="avatar-input-mobile" accept="image/png,image/jpeg,image/gif,image/webp" data-change="avatarPickMobile" style="display:none" aria-label="${t('settings.profileUpload')}">
    ${u.avatarUpdatedAt ? `<button type="button" class="sheet-opt" data-act="avatarRemoveMobile">${icon('delete')}<span>${t('settings.profileRemove')}</span></button>` : ''}
    <button type="button" class="sheet-opt" data-act="nav" data-a0="/settings">${icon('settings')}<span>${t('nav.settings')}</span></button>
    ${IS_PHONE ? '' : `<a class="sheet-opt" href="${esc(DESKTOP_URL)}">${icon('external')}<span>${t('mobile.openDesktop')}</span></a>`}
    <button type="button" class="sheet-opt" data-act="doLogout" style="color:var(--md-error)">${icon('logout')}<span>${t('auth.signOut')}</span></button>`);
}
async function doLogout() {
  closeSheet();
  invalidateTaskCache();   // never carry one session's task list into the next
  await Auth.logout();
}

// ═══════════════════════════════════════════════════════════
// THEME (light / dark / system) — first theme support on mobile.
// Mirrors octbase-frontend/js/framework.js's getThemePref/applyTheme; the two
// apps share the 'octbase-theme' localStorage key (same origin, / vs /m/).
// ═══════════════════════════════════════════════════════════
const THEME_KEY = 'octbase-theme';
const THEME_ORDER = ['system', 'light', 'dark', 'octopus'];
function getThemePref() {
  const v = localStorage.getItem(THEME_KEY);
  return THEME_ORDER.includes(v) ? v : 'system';
}
function applyTheme(pref) {
  if (THEME_ORDER.includes(pref) && pref !== 'system') {
    document.documentElement.dataset.theme = pref;
  } else {
    delete document.documentElement.dataset.theme;
  }
}
// themeLabel/languageLabel fall back to the raw value if a translation is
// missing, same defensive style as the rest of this file's t()!==key checks.
function themeLabel(pref) {
  const key = 'theme.' + pref;
  const label = t(key);
  return label !== key ? label : pref;
}
function languageLabel(lang) { return lang === 'de' ? 'Deutsch' : 'English'; }
// Same shape as themeLabel: fall back to the raw value if the locale lacks the
// key, so a new vocabulary can never render as a bare key path.
function terminologyLabel(tm) {
  const key = 'settings.terminologies.' + tm;
  const label = t(key);
  return label !== key ? label : tm;
}

// ═══════════════════════════════════════════════════════════
// SETTINGS (language/theme preferences + MFA) — full-screen view, reached
// from the profile sheet. Two independent backend modules (internal/
// dashboard, internal/security/mfa) behind one page, mirroring
// octbase-frontend/js/views-settings.js.
// ═══════════════════════════════════════════════════════════
const _settingsPrefs = { language: null, theme: null, terminology: null };
let _mfaEnrollment = null; // { secret, otpauthUrl } while enrollment is pending confirmation
let _recoveryCodesForCopy = [];

async function viewSettings() {
  render(t('settings.title'), loadingState(), { back: '/dashboard' });
  _mfaEnrollment = null;
  let prefs;
  try {
    prefs = await api.preferences.get();
  } catch (e) {
    el('#content').innerHTML = errorState(e, 'reloadSettingsView');
    return;
  }
  _settingsPrefs.language = prefs.language;
  _settingsPrefs.theme = prefs.theme;
  _settingsPrefs.terminology = prefs.terminology || i18n.DEFAULT_TERMINOLOGY;
  // Server value wins: apply it before rendering so the rows below can never
  // show a value that isn't actually in effect (e.g. a stale localStorage
  // cache from before this device last synced). A locale switch re-runs the
  // route, which re-enters this view with local and server in agreement.
  if (THEME_ORDER.includes(prefs.theme) && prefs.theme !== getThemePref()) {
    localStorage.setItem(THEME_KEY, prefs.theme);
    applyTheme(prefs.theme);
  }
  applyTerminologyPref(prefs.terminology);
  if (i18n.AVAILABLE_LOCALES.includes(prefs.language) && prefs.language !== getLocale()) {
    await i18n.setLocale(prefs.language);
    handleRoute();
    return;
  }
  renderSettingsBody();
}
function reloadSettingsView() { viewSettings(); }

// reconcilePreferences pulls the server-persisted preferences after login and
// makes them win over the local cache, so settings follow the user across
// devices (mirrors octbase-frontend's reconcilePreferences). Fired without
// await from bootSession — theme-init.js already applied the cached value
// before CSS, and this re-renders on its own if the server disagrees.
async function reconcilePreferences() {
  let prefs;
  try { prefs = await api.preferences.get(); } catch { return; }
  if (THEME_ORDER.includes(prefs.theme) && prefs.theme !== getThemePref()) {
    localStorage.setItem(THEME_KEY, prefs.theme);
    applyTheme(prefs.theme);
  }
  // Vocabulary needs no fetch — the classic overlay travels inside the locale
  // file this app already loaded — so it is applied first and a language switch
  // below repaints once for both. When only the vocabulary changed, repaint here.
  const termChanged = applyTerminologyPref(prefs.terminology);
  if (i18n.AVAILABLE_LOCALES.includes(prefs.language) && prefs.language !== getLocale()) {
    await i18n.setLocale(prefs.language); // persists octbase.lang itself
    handleRoute();
  } else if (termChanged) {
    handleRoute();
  }
}

// applyTerminologyPref makes the account's vocabulary the one in effect on this
// device, and reports whether it changed so the caller can repaint. Without it
// the phone shipped the classic overlay in its locale files but could never
// switch to it: the desktop reconciles this preference at boot and the mobile
// companion did not, so a phone-only user was stuck on the agile wording no
// matter what they had chosen — see the user guide's promise that the choice
// "follows you across devices".
function applyTerminologyPref(terminology) {
  if (!i18n.TERMINOLOGIES.includes(terminology)) return false;
  if (terminology === i18n.getTerminology()) return false;
  i18n.setTerminology(terminology); // persists octbase.terminology itself
  return true;
}

// segSwitch renders a segmented switch (a radiogroup of buttons) — the same
// unified picker control the desktop settings page uses (views-settings.js),
// full-width here. Buttons dispatch `act` with the value in data-a0; the
// handlers re-render the settings body so aria-checked never goes stale.
function segSwitch(options, selected, act, ariaLabel) {
  return `<div class="seg-switch" role="radiogroup" aria-label="${esc(ariaLabel)}">
    ${options.map(o => `<button type="button" role="radio" aria-checked="${o.value === selected}" data-act="${act}" data-a0="${esc(o.value)}">${o.value === selected ? icon('check', { size: 'sm' }) : ''}<span>${esc(o.label)}</span></button>`).join('')}
  </div>`;
}

function renderSettingsBody() {
  const langs = i18n.AVAILABLE_LOCALES;
  const terms = i18n.TERMINOLOGIES;
  el('#content').innerHTML = html`
    <div class="section-head">${raw(t('settings.mfa.title'))}</div>
    <div id="mfa-section-mobile">${raw(renderMfaSectionMobile())}</div>
    <div class="section-head">${raw(t('settings.password.title'))}</div>
    <div id="password-section-mobile">${raw(renderPasswordSectionMobile())}</div>
    <div class="section-head">${raw(t('settings.preferencesTitle'))}</div>
    <div class="card">
      <div class="field-label">${raw(t('settings.language'))}</div>
      ${raw(segSwitch(langs.map(l => ({ value: l, label: languageLabel(l) })),
                      _settingsPrefs.language, 'pickLanguage', t('settings.language')))}
      <div class="field-label seg-switch-follow">${raw(t('settings.theme'))}</div>
      ${raw(segSwitch(THEME_ORDER.map(th => ({ value: th, label: themeLabel(th) })),
                      _settingsPrefs.theme, 'pickTheme', t('settings.theme')))}
      <div class="field-label seg-switch-follow">${raw(t('settings.terminology'))}</div>
      ${raw(segSwitch(terms.map(tm => ({ value: tm, label: terminologyLabel(tm) })),
                      _settingsPrefs.terminology, 'pickTerminology', t('settings.terminology')))}
      <p class="row-sub" style="white-space:normal">${raw(t('settings.terminologyHint'))}</p>
    </div>`;
}

async function pickLanguage(value) {
  if (value === _settingsPrefs.language) return;
  const prev = _settingsPrefs.language;
  _settingsPrefs.language = value;
  renderSettingsBody();
  try {
    await api.preferences.update({ language: value, theme: _settingsPrefs.theme, terminology: _settingsPrefs.terminology });
    await i18n.setLocale(value);
    viewSettings();
  } catch (e) {
    _settingsPrefs.language = prev;
    renderSettingsBody();
    toast(apiErrorMessage(e), 'error');
  }
}

async function pickTheme(value) {
  if (value === _settingsPrefs.theme) return;
  const prev = _settingsPrefs.theme;
  _settingsPrefs.theme = value;
  renderSettingsBody();
  try {
    await api.preferences.update({ language: _settingsPrefs.language, theme: value, terminology: _settingsPrefs.terminology });
    localStorage.setItem(THEME_KEY, value);
    applyTheme(value);
  } catch (e) {
    _settingsPrefs.theme = prev;
    renderSettingsBody();
    toast(apiErrorMessage(e), 'error');
  }
}

// Vocabulary switches the words, not the data. Unlike a language change it
// loads nothing — the classic overlay is already inside the loaded locale — so
// the whole route is simply re-rendered once the preference is stored.
async function pickTerminology(value) {
  if (value === _settingsPrefs.terminology) return;
  const prev = _settingsPrefs.terminology;
  _settingsPrefs.terminology = value;
  renderSettingsBody();
  try {
    await api.preferences.update({ language: _settingsPrefs.language, theme: _settingsPrefs.theme, terminology: value });
    i18n.setTerminology(value); // persists octbase.terminology itself
    handleRoute();
  } catch (e) {
    _settingsPrefs.terminology = prev;
    renderSettingsBody();
    toast(apiErrorMessage(e), 'error');
  }
}

// ── MFA ──
function refreshMfaSectionMobile() {
  const mount = el('#mfa-section-mobile');
  if (mount) mount.innerHTML = renderMfaSectionMobile();
}

function renderMfaSectionMobile() {
  const enabled = !!(S.user && S.user.mfaEnabled);
  if (enabled) {
    return `
      <div class="card">
        <p class="row-sub" style="white-space:normal;margin-bottom:var(--space-3)">${t('settings.mfa.enabledDesc')}</p>
        <div class="mfa-status-mobile">${icon('check',{size:'sm'})}<span>${t('settings.mfa.enabled')}</span></div>
      </div>
      <button type="button" class="sheet-opt" data-act="openMfaRegenerateSheet">${icon('refresh')}<span>${t('settings.mfa.regenerateCodes')}</span></button>
      <button type="button" class="sheet-opt" style="color:var(--md-error)" data-act="openMfaDisableSheet">${icon('close')}<span>${t('settings.mfa.disable')}</span></button>`;
  }
  if (_mfaEnrollment) {
    return `
      <div class="card">
        <p class="row-sub" style="white-space:normal;margin-bottom:var(--space-3)">${t('settings.mfa.enrollDesc')}</p>
        <div class="mfa-qr" role="img" aria-label="${t('settings.mfa.qrAria')}">${mfaEnrollmentQr()}</div>
        <div class="field">
          <label class="field-label">${t('settings.mfa.manualEntry')}</label>
          <div class="mfa-secret-row">
            <code class="mfa-secret" id="mfa-secret-value-mobile">${esc(_mfaEnrollment.secret)}</code>
            <button type="button" class="icon-btn" data-act="copyMfaSecretMobile" aria-label="${t('form.copy')}">${icon('copy',{size:'sm'})}</button>
          </div>
        </div>
        <form data-submit="confirmMfaEnrollmentMobile" novalidate>
          <div class="field">
            <label class="field-label" for="mfa-confirm-code-m">${t('settings.mfa.enterCode')}</label>
            <input class="input" id="mfa-confirm-code-m" inputmode="numeric" autocomplete="one-time-code" required autofocus>
          </div>
          <button class="btn btn-primary btn-block" type="submit">${t('settings.mfa.confirm')}</button>
          <button class="btn btn-block" type="button" style="margin-top:var(--space-2)" data-act="cancelMfaEnrollmentMobile">${t('form.cancel')}</button>
        </form>
      </div>`;
  }
  return `
    <div class="card">
      <p class="row-sub" style="white-space:normal;margin-bottom:var(--space-3)">${t('settings.mfa.disabledDesc')}</p>
      <button type="button" class="btn btn-primary btn-block" data-act="startMfaEnrollmentMobile">${t('settings.mfa.enable')}</button>
    </div>`;
}

// mfaEnrollmentQr renders the pending enrollment's otpauth:// URI as an SVG QR
// code client-side via the pinned qrcode-generator package (mirrors
// octbase-frontend's views-settings.js). Useful when this UI runs on a
// tablet/laptop; on a phone the manual-entry setup key below stays the primary
// path. The library can no longer be missing — it is a static import since 37b
// stage 4, not a classic <script> that may not have run yet.
function mfaEnrollmentQr() {
  if (!_mfaEnrollment) return '';
  try {
    const qr = qrcode(0, 'M'); // type 0 = auto-size to the data
    qr.addData(_mfaEnrollment.otpauthUrl);
    qr.make();
    return qr.createSvgTag({ cellSize: 4, margin: 3, scalable: true });
  } catch {
    return '';
  }
}

// Enrollment from a normal access token requires a password re-auth (the
// backend's REAUTH_REQUIRED rule: a stolen access token must not be able to
// bind MFA to an attacker's authenticator). Prompt for the password first, then
// enroll — mirrors the desktop enroll flow and the disable/regenerate sheets.
function startMfaEnrollmentMobile() {
  openSheet(t('settings.mfa.enable'), reauthSheetBody('enroll'));
}
async function submitMfaEnrollReauthMobile(form, ev) {
  ev.preventDefault();
  const password = el('#mfa-enroll-password-m').value;
  try {
    const data = await api.mfa.enroll(password);
    _mfaEnrollment = { secret: data.secret, otpauthUrl: data.otpauthUrl };
    closeSheet();
    refreshMfaSectionMobile();
  } catch (e) {
    toast(apiErrorMessage(e), 'error');
  }
}
function cancelMfaEnrollmentMobile() {
  _mfaEnrollment = null;
  refreshMfaSectionMobile();
}
function copyMfaSecretMobile() {
  if (!_mfaEnrollment) return;
  navigator.clipboard.writeText(_mfaEnrollment.secret)
    .then(() => toast(t('form.copied'), 'success')).catch(() => {});
}
async function confirmMfaEnrollmentMobile(form, ev) {
  ev.preventDefault();
  const input = el('#mfa-confirm-code-m');
  try {
    const result = await api.mfa.confirm(input.value.trim());
    _mfaEnrollment = null;
    if (S.user) S.user.mfaEnabled = true;
    refreshMfaSectionMobile();
    showRecoveryCodesSheet(result.recoveryCodes || []);
  } catch (e) {
    toast(apiErrorMessage(e), 'error');
  }
}

// ── Password (POST /auth/change-password, internal/auth) ────────────────────
// Mirrors the desktop Settings card. Errors land inline rather than in a toast:
// a wrong current password and a policy-rejected new password point at
// different inputs, and a toast cannot say which. On a phone the inline message
// also stays put while the on-screen keyboard is open.
function renderPasswordSectionMobile() {
  return `
    <div class="card">
      <p class="row-sub" style="white-space:normal">${t('settings.password.desc')}</p>
      <form data-submit="changePasswordMobile" novalidate>
        <label class="field-label" for="pw-current-m">${t('settings.password.current')}</label>
        <input class="input" id="pw-current-m" type="password" autocomplete="current-password" required>
        <label class="field-label" for="pw-new-m">${t('settings.password.new')}</label>
        <input class="input" id="pw-new-m" type="password" autocomplete="new-password" required>
        <p class="row-sub" style="white-space:normal">${t('settings.password.policyHint')}</p>
        <label class="field-label" for="pw-confirm-m">${t('settings.password.confirm')}</label>
        <input class="input" id="pw-confirm-m" type="password" autocomplete="new-password" required>
        <div class="login-alert hidden" id="pw-error-m" role="alert"></div>
        <button class="btn btn-primary btn-block" type="submit">${t('settings.password.submit')}</button>
      </form>
    </div>`;
}

function refreshPasswordSectionMobile() {
  const mount = el('#password-section-mobile');
  if (mount) mount.innerHTML = renderPasswordSectionMobile();
}

function showPasswordErrorMobile(message, focusId) {
  const box = el('#pw-error-m');
  if (box) {
    box.textContent = message;
    box.classList.remove('hidden');
  }
  const input = focusId && el('#' + focusId);
  if (input) {
    input.setAttribute('aria-invalid', 'true');
    input.focus();
  }
}

async function changePasswordMobile(form, ev) {
  ev.preventDefault();
  const box = el('#pw-error-m');
  if (box) { box.textContent = ''; box.classList.add('hidden'); }
  ['pw-current-m', 'pw-new-m', 'pw-confirm-m']
    .forEach(id => { const i = el('#' + id); if (i) i.removeAttribute('aria-invalid'); });

  const current = el('#pw-current-m').value;
  const next = el('#pw-new-m').value;
  const confirm = el('#pw-confirm-m').value;

  if (!current || !next) {
    showPasswordErrorMobile(t('settings.password.required'), current ? 'pw-new-m' : 'pw-current-m');
    return;
  }
  if (next !== confirm) {
    showPasswordErrorMobile(t('settings.password.mismatch'), 'pw-confirm-m');
    return;
  }

  try {
    await api.auth.changePassword(current, next);
    refreshPasswordSectionMobile();
    toast(t('settings.password.changed'), 'success');
  } catch (e) {
    const wrongCurrent = e && e.code === 'CURRENT_PASSWORD_INVALID';
    const field = e && e.details && e.details.field;
    const focus = wrongCurrent || field === 'currentPassword' ? 'pw-current-m' : 'pw-new-m';
    showPasswordErrorMobile(apiErrorMessage(e), focus);
  }
}

function showRecoveryCodesSheet(codes) {
  _recoveryCodesForCopy = codes;
  openSheet(t('settings.mfa.recoveryCodesTitle'), `
    <p class="row-sub" style="white-space:normal;margin-bottom:var(--space-3)">${t('settings.mfa.recoveryCodesDesc')}</p>
    <ul class="recovery-codes-list">${codes.map(c => `<li><code>${esc(c)}</code></li>`).join('')}</ul>
    <button type="button" class="btn btn-block" style="margin-bottom:var(--space-2)" data-act="copyRecoveryCodesMobile">${t('form.copyAll')}</button>
    <button type="button" class="btn btn-primary btn-block" data-act="closeSheetAct">${t('settings.mfa.recoveryCodesAck')}</button>`);
}
function copyRecoveryCodesMobile() {
  navigator.clipboard.writeText(_recoveryCodesForCopy.join('\n'))
    .then(() => toast(t('form.copied'), 'success')).catch(() => {});
}

// reauthSheetBody backs all three MFA re-auth sheets (enroll, disable,
// regenerate) so they cannot drift. 'enroll' collects only the password — no
// MFA is active yet, so there is no code to offer — under its own input id and
// submit handler; 'disable' and 'regenerate' also accept a TOTP/recovery code
// (internal/security/mfa's re-auth rule: never a bare toggle).
function reauthSheetBody(action) {
  const enroll = action === 'enroll';
  const inputId = enroll ? 'mfa-enroll-password-m' : 'mfa-reauth-password-m';
  const submit = enroll ? 'submitMfaEnrollReauthMobile' : 'submitMfaReauthMobile';
  const label = enroll ? t('settings.mfa.enable')
    : action === 'disable' ? t('settings.mfa.disable') : t('settings.mfa.regenerateCodes');
  return `
    <p class="row-sub" style="white-space:normal;margin-bottom:var(--space-3)">${t('settings.mfa.reauthDesc')}</p>
    <form data-submit="${submit}" data-a0="${action}" novalidate>
      <div class="field">
        <label class="field-label" for="${inputId}">${t('auth.password')}</label>
        <input class="input" id="${inputId}" type="password" autocomplete="current-password">
      </div>${enroll ? '' : `
      <div class="field">
        <label class="field-label" for="mfa-reauth-code-m">${t('settings.mfa.orCode')}</label>
        <input class="input" id="mfa-reauth-code-m" inputmode="numeric" autocomplete="one-time-code">
      </div>`}
      <button class="btn btn-primary btn-block" type="submit">${label}</button>
    </form>`;
}
function openMfaDisableSheet() {
  openSheet(t('settings.mfa.disableTitle'), reauthSheetBody('disable'));
}
function openMfaRegenerateSheet() {
  openSheet(t('settings.mfa.regenerateTitle'), reauthSheetBody('regenerate'));
}
async function submitMfaReauthMobile(form, ev, action) {
  ev.preventDefault();
  const password = el('#mfa-reauth-password-m').value;
  const code = el('#mfa-reauth-code-m').value.trim();
  try {
    if (action === 'disable') {
      await api.mfa.disable({ password, code });
      closeSheet();
      if (S.user) S.user.mfaEnabled = false;
      refreshMfaSectionMobile();
      toast(t('settings.mfa.disabled'), 'success');
    } else {
      const result = await api.mfa.regenerateRecoveryCodes({ password, code });
      showRecoveryCodesSheet(result.recoveryCodes || []);
    }
  } catch (e) {
    toast(apiErrorMessage(e), 'error');
  }
}

// ═══════════════════════════════════════════════════════════
// MISC ACTIONS
// ═══════════════════════════════════════════════════════════
function reloadRoute() { handleRoute(); }

// ═══════════════════════════════════════════════════════════
// EVENT DELEGATION WIRING
// ═══════════════════════════════════════════════════════════
function initDelegation() {
  document.addEventListener('click', e => {
    const a = e.target.closest('a[href="#"][data-act]');
    if (a) e.preventDefault();
    _dispatch(ACTIONS, 'act', e);
  });
  document.addEventListener('change', e => _dispatch(CHANGES, 'change', e));
  document.addEventListener('input',  e => _dispatch(INPUTS,  'input',  e));
  document.addEventListener('submit', e => _dispatch(SUBMITS, 'submit', e));

  _register(ACTIONS, [
    openProfile, closeSheetAct, confirmSheetYes, confirmSheetNo, doLogout, reloadRoute, markAllRead, clearFilters,
    openFilterSheet, openTaskMenu, reloadSettingsView, mfaBackToLogin,
    startMfaEnrollmentMobile, cancelMfaEnrollmentMobile,
    copyMfaSecretMobile, copyRecoveryCodesMobile, openMfaDisableSheet, openMfaRegenerateSheet,
    avatarRemoveMobile,
  ], _A0);
  _register(ACTIONS, [
    nav, selectBoardCol, openStatusSheet, openPrioritySheet, openAssigneeSheet,
    openMoveSheet, pickLanguage, pickTheme, pickTerminology, pickReopen, openEstimateSheet, clearEstimate,
  ], _A1);
  _register(ACTIONS, [openTask, applyFilter, pickStatus, pickPriority, pickAssignee, pickMove, pickEstimate], _A2);
  _register(ACTIONS, [openNotif], _A3);

  CHANGES.ctTypeChanged = node => ctTypeChanged(node);
  CHANGES.avatarPickMobile = node => avatarPickMobile(node);

  // Hydrate avatar images after each render (see hydrateAvatars).
  let _avatarRaf = null;
  const scheduleHydrate = () => {
    if (_avatarRaf) return;
    _avatarRaf = requestAnimationFrame(() => { _avatarRaf = null; hydrateAvatars(document); });
  };
  new MutationObserver(scheduleHydrate).observe(document.body, { childList: true, subtree: true });
  scheduleHydrate();

  _register(SUBMITS, [doLogin, submitComment, submitCreateTask, saveEstimate], (fn, el, ev) => fn(el, ev));
  SUBMITS.doVerifyMfaLoginMobile = (el, ev) => doVerifyMfaLoginMobile(el, ev, el.dataset.a0);
  SUBMITS.doForgotPasswordMobile = (el, ev) => doForgotPasswordMobile(el, ev);
  SUBMITS.doResetPasswordMobile = (el, ev) => doResetPasswordMobile(el, ev, el.dataset.a0);
  SUBMITS.confirmMfaEnrollmentMobile = (el, ev) => confirmMfaEnrollmentMobile(el, ev);
  SUBMITS.changePasswordMobile = (el, ev) => changePasswordMobile(el, ev);
  SUBMITS.submitMfaEnrollReauthMobile = (el, ev) => submitMfaEnrollReauthMobile(el, ev);
  SUBMITS.submitMfaReauthMobile = (el, ev) => submitMfaReauthMobile(el, ev, el.dataset.a0);

  // The search box re-queries as you type (debounced).
  document.addEventListener('input', e => {
    if (e.target.id === 'search-input') onSearchInput(e.target.value);
  });
}

function openTask(taskId, projectId) {
  if (projectId && (!S.project || S.project.id !== projectId)) {
    // project will be (re)loaded inside viewTask via task.projectId
  }
  navTo(`/task/${taskId}`);
}

// ═══════════════════════════════════════════════════════════
// SESSION BOOTSTRAP
// ═══════════════════════════════════════════════════════════
async function bootSession() {
  try { S.user = await api.auth.me(); } catch { S.user = null; }
  // best-effort unread count for the nav badge
  try {
    const res = await api.notifications.list({ size:50 });
    const items = res.notifications || res.items || res || [];
    S.notifCount = items.filter(n => !n.isRead).length;
  } catch { S.notifCount = 0; }
  // Pull the server-persisted language/theme after first paint (server value
  // wins across devices). Deliberately not awaited — see reconcilePreferences.
  if (S.user) reconcilePreferences();
}

async function init() {
  await i18n.initI18n();
  initDelegation();

  if (USE_STANDALONE_DEMO_AUTH) {
    try { await Auth.demoLogin(); } catch { /* show login */ }
  } else if (Auth.hasSessionHint()) {
    // Only probe when a refresh cookie may still be present; otherwise we'd log
    // a 401 on every load for logged-out visitors.
    try { await Auth.refresh(); } catch { /* not signed in */ }
  }

  if (Auth.isAuthenticated()) {
    await bootSession();
    if (!window.location.hash || window.location.hash === '#/login') navTo('/dashboard');
    else handleRoute();
  } else {
    navTo('/login');
    renderLogin();
  }
}

let _booted = false;
function boot() { if (_booted) return; _booted = true; init(); }
document.addEventListener('DOMContentLoaded', boot);
if (document.readyState !== 'loading') boot();

// No export block: this file is the entry, so nothing imports it. `S` reaches
// the Playwright suite through `window.S` above. Keeping an unused `export`
// here is not harmless — it makes rollup wrap the standalone IIFE bundle in
// `function (exports) { … }`, and the vendored qrcode UMD then sees a real
// `exports` object, takes its CommonJS branch and dies on an undefined
// `module` before a line of app code runs. `output.exports: 'none'` in
// vite.standalone.config.js now fails the build if one comes back.
