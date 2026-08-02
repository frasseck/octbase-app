import { t } from '@octbase/shared/i18n.js';
import { TYPE_META, priorityMeta, priorityNames, projectTaskTypes } from '@octbase/shared/meta.js';
import { Prefetch, api } from './api.js';
import { Auth } from './auth.js';
import { FEATURES } from './config.js';
import { _A0, _A1, _A2, _CHK, _CHK0, registerActions, registerChanges, registerInputs, registerKeydowns } from './delegation.js';
import { API_BASE, BASE_PATH } from './env.js';
import { closeSidebar, confirmDelete, confirmModal, debounced, el, esc, fmtDate, hideModal, navExpanded, priorityDot, renderLangLinks, statusBadge, themeToggleLabel, toast, typeBadge } from './framework.js';
import { apiErrorMessage, errorFromResponse, http } from './http.js';
import { icon } from './icons.js';
import { AppPerms } from './permissions.js';
import { _hideNotifPanel, clearContentStale, connectSSE, disconnectSSE, updateLiveIndicator, updateNotifBadge } from './realtime.js';
import { Views } from './registry.js';
import { router } from './router.js';
import { S, currentAppTitle, rememberProjectVisit, sidebarProjects, taskFilterParams } from './state.js';
import { maybePlaceOnBoard, renderPages } from './views-content.js';
import { archiveProject, deleteProject, showEditProject, unarchiveProject } from './views-crud.js';
import { invalidateProjectTasks, openTaskPanel, personOptions } from './views-task.js';
import { bulkSetStatus, taskViewBulkStatusOptions, taskViewStatusFilterOptions } from './views-tasklist.js';

// Octbase SPA — split from the former single app.js. One ES module among many,
// bundled by Vite (37b stage 2): its top-level declarations are file-private
// and its public surface is the `export { … }` block at the bottom. Imports
// carry the dependencies — there is no load order to keep in step
// (js/README.md).
function renderSidebar() {
  const nav = el('#sidebar-nav');
  if (!nav) return;
  let main;
  if(!S.project) {
    const recentProjects = sidebarProjects();
    main = `
      <div class="sidebar-section">
        <button type="button" class="sidebar-item ${S.view==='dashboard'?'active':''}" ${S.view==='dashboard'?'aria-current="page"':''} data-act="goToDashboard">
          <span class="icon" aria-hidden="true">${icon('home')}</span>${t('nav.myWork')}
        </button>
        <div class="sidebar-label" id="sidebar-projects-label">${t('nav.projects')}</div>
        ${recentProjects.map(p=>`
          <button type="button" class="sidebar-item" aria-describedby="sidebar-projects-label" data-act="selectProject" data-a0="${esc(p.id)}">
            <span class="icon" aria-hidden="true">${icon('project')}</span>${esc(p.name)}
          </button>`).join('')}
        ${S.projects.length > recentProjects.length ? `
          <button type="button" class="sidebar-item" data-act="nav" data-a0="/projects">
            <span class="icon" aria-hidden="true">${icon('more')}</span>${t('nav.seeAll')}
          </button>` : ''}
        ${AppPerms.canManageAccounts() ? `
        <div class="sidebar-divider" role="separator"></div>
        <div class="sidebar-label" id="sidebar-admin-label">${t('nav.administration')}</div>
        <button type="button" class="sidebar-item ${S.view==='admin'?'active':''}" ${S.view==='admin'?'aria-current="page"':''} aria-describedby="sidebar-admin-label" data-act="nav" data-a0="/admin">
          <span class="icon" aria-hidden="true">${icon('user',{size:'md'})}</span>${t('nav.userManagement')}
        </button>
        <button type="button" class="sidebar-item ${S.view==='audit-logs'?'active':''}" ${S.view==='audit-logs'?'aria-current="page"':''} aria-describedby="sidebar-admin-label" data-act="nav" data-a0="/admin/audit-logs">
          <span class="icon" aria-hidden="true">${icon('doc')}</span>${t('nav.auditLogs')}
        </button>` : ''}
      </div>`;
  } else {
    // The project nav comes from the view registry: each views-* module
    // registers its entry (icon, label, order, visibility/feature gates) —
    // see registry.js for the contract.
    const views = Views.sidebarEntries().map(d => ({
      id: d.id, icon: d.sidebar.icon, label: d.sidebar.label(), key: d.sidebar.key,
    }));
    main = `
      <div class="sidebar-section">
        <button type="button" class="sidebar-item" data-act="goToDashboard">
          <span class="icon" aria-hidden="true">${icon('home')}</span>${t('nav.myWork')}
        </button>
        <button type="button" class="sidebar-item" data-act="backToProjects">
          <span class="icon" aria-hidden="true">${icon('project')}</span>${t('nav.allProjects')}
        </button>
        <div class="sidebar-divider" role="separator"></div>
        <div class="sidebar-label" id="sidebar-project-label">${esc(S.project.name)}</div>
        ${views.map(v=>`
          <button type="button" class="sidebar-item ${S.view===v.id?'active':''}" ${S.view===v.id?'aria-current="page"':''} aria-describedby="sidebar-project-label" data-act="setView" data-a0="${esc(v.id)}">
            <span class="icon" aria-hidden="true">${icon(v.icon)}</span>${v.label}
            ${v.key ? `<span class="kbd-hint" aria-hidden="true">${v.key}</span>` : ''}
          </button>`).join('')}
      </div>`;
  }
  nav.innerHTML = main + sidebarHelpSection();
}

// sidebarHelpSection renders the always-present documentation links shown at the
// bottom of the sidebar in every context (project or not): the user guide, the
// API spec explorer, and the UI style guide. All three are static pages served
// alongside the app (see the frontend Caddyfile), so they are plain links that
// open in a new tab.
function sidebarHelpSection() {
  return `
      <div class="sidebar-section">
        <div class="sidebar-divider" role="separator"></div>
        <div class="sidebar-label" id="sidebar-resources-label">${t('nav.resources')}</div>
        <a class="sidebar-item" href="/user-guide.html" target="_blank" rel="noopener" aria-describedby="sidebar-resources-label">
          <span class="icon" aria-hidden="true">${icon('doc')}</span>${t('nav.userGuide')}
        </a>
        <a class="sidebar-item" href="/docs.html" target="_blank" rel="noopener" aria-describedby="sidebar-resources-label">
          <span class="icon" aria-hidden="true">${icon('external')}</span>${t('nav.apiDocs')}
        </a>
        <a class="sidebar-item" href="/styleguide.html" target="_blank" rel="noopener" aria-describedby="sidebar-resources-label">
          <span class="icon" aria-hidden="true">${icon('palette')}</span>${t('nav.styleGuide')}
        </a>
      </div>`;
}

// ═══════════════════════════════════════════════════════════
// TOPBAR
// ═══════════════════════════════════════════════════════════
// renderFilterBar renders the task filters (type / priority, plus status on the
// Task view) and the search box, shown at the top of the Backlog and Task views.
// Status is offered only on the Task view: it is the grouping there, and the
// filter narrows to one status; the Backlog intentionally has no status filter.
function renderFilterBar() {
  const statusFilter = (S.view === 'tasks') ? `
      <label class="sr-only" for="filter-status">${t('accessibility.filterByStatus')}</label>
      <select class="form-select-sm" id="filter-status" aria-label="${t('accessibility.filterByStatus')}" data-change="setFilter" data-a0="status">
        <option value="">${t('task.allStatuses')}</option>
        ${taskViewStatusFilterOptions()}
      </select>` : '';
  return `
    <div class="content-filters">
      <label class="sr-only" for="task-search">${t('search.taskSearchLabel')}</label>
      <input type="search" class="form-input form-input-sm task-search-input" id="task-search" value="${esc(S.filters.q || '')}"
        placeholder="${t('search.taskSearchPlaceholder')}" aria-label="${t('search.taskSearchLabel')}"
        data-input="setSearchFilter" autocomplete="off">
      ${statusFilter}
      <label class="sr-only" for="filter-type">${t('accessibility.filterByType')}</label>
      <select class="form-select-sm" id="filter-type" aria-label="${t('accessibility.filterByType')}" data-change="setFilter" data-a0="type">
        <option value="">${t('task.allTypes')}</option>
        ${projectTaskTypes(S.project).map(tt=>`<option value="${tt}" ${S.filters.type===tt?'selected':''}>${TYPE_META[tt].label}</option>`).join('')}
      </select>
      <label class="sr-only" for="filter-priority">${t('accessibility.filterByPriority')}</label>
      <select class="form-select-sm" id="filter-priority" aria-label="${t('accessibility.filterByPriority')}" data-change="setFilter" data-a0="priority">
        <option value="">${t('task.allPriorities')}</option>
        ${priorityNames(S.priorities).map(p=>`<option value="${esc(p)}" ${S.filters.priority===p?'selected':''}>${esc(priorityMeta(p).label)}</option>`).join('')}
      </select>
    </div>`;
}

// viewCreateButton returns the primary "create" button for the current view's
// main content area (it used to sit in the topbar). The no-project views
// (dashboard / projects) create a project; project views supply theirs via
// the registry entry's createButton.
function viewCreateButton() {
  if (!S.project) return `<button class="btn btn-secondary btn-sm" data-act="importNewProject">${icon('upload',{size:'md'})} ${t('project.importNew')}</button>
      <button class="btn btn-primary btn-sm" data-act="showCreateProject">${icon('add',{size:'md'})} ${t('nav.newProject')}</button>`;
  // Every view's create button is a project-scoped write, and two different
  // states refuse it: a PROJECT_VIEWER holds no write permission (403), and an
  // archived project is frozen for everyone (409 PROJECT_ARCHIVED). One
  // predicate covers both — isReadOnlyProject asks can('task.create'), which
  // answers false for a viewer and false for anyone while the freeze holds.
  // Task creation is gated by requirePermission(task.create); sprint, release
  // and page creation go through writerGuard, which rejects exactly
  // PROJECT_VIEWER — the same population, so the one question is honest for
  // all six views. Offering the button anyway is what made an archived project
  // read as broken rather than deliberate, and a viewer saw the same thing.
  if (AppPerms.isReadOnlyProject(S.project)) return '';
  const def = Views.get(S.view);
  return def?.createButton ? def.createButton() : '';
}

// contentToolbar is the top row of the main content area: task filters (left,
// backlog only) and the create button (right). When a view is
// empty its centred empty-state CTA already offers "create", so the toolbar
// button is omitted there to avoid a duplicate — except the filter selects,
// which stay so an over-restrictive filter can always be cleared.
function contentToolbar(view, isEmpty) {
  const wantFilters = !!Views.get(view)?.listToolbar;
  const btn = isEmpty ? '' : viewCreateButton();
  if (!wantFilters && !btn) return '';
  return `<div class="content-toolbar">${wantFilters ? renderFilterBar() : ''}${btn ? `<div class="content-toolbar-actions">${btn}</div>` : ''}</div>`;
}

// prependListToolbar inserts the list toolbar (filters + create button) at the
// top of the freshly-rendered #content for a listToolbar view. It is idempotent
// per render and is called both by renderContent after the initial render and by
// in-place re-renders (e.g. column sort) that replace all of #content and so
// would otherwise drop the toolbar.
function prependListToolbar() {
  const c = el('#content');
  const def = Views.get(S.view);
  if (!c || !def?.listToolbar) return;
  const isEmpty = !!c.querySelector('.empty');
  // A genuinely-empty list (no filter set) drops the toolbar so its centred
  // empty state sits like every other view's. The toolbar stays when a filter is
  // hiding everything, so it can always be cleared.
  if (!isEmpty || taskFilterParams().toString()) {
    c.insertAdjacentHTML('afterbegin', contentToolbar(S.view, isEmpty));
  }
}

function renderTopbar() {
  const tb = el('#topbar');
  if (!tb) return;
  // Simple status dot — the .live-indicator state classes drive the colour
  // (green when online, red when disconnected, grey when connecting).
  const liveDot = `<span id="live-indicator" class="live-indicator" title="${t('app.connecting')}" role="status" aria-label="${t('app.connecting')}"><span class="live-dot"></span></span>`;
  const bellIcon = icon('bell');
  const notifBtn = `
    <button class="btn-icon notif-btn" data-act="toggleNotifPanel" title="${t('notifications.title')}">
      ${bellIcon}<span id="notif-badge" class="notif-badge hidden"></span>
    </button>`;

  const themeBtn = `
    <button class="btn-icon" data-act="cycleTheme" title="${esc(themeToggleLabel())}" aria-label="${esc(themeToggleLabel())}">
      ${icon('theme')}
    </button>`;

  // Personal settings entry: a user icon in the topbar's right corner (the
  // gear stays reserved for per-project settings, see projectActions below).
  const userBtn = `
    <button class="btn-icon" data-act="nav" data-a0="/settings" title="${t('nav.settings')}" aria-label="${t('nav.settings')}">
      ${icon('user')}
    </button>`;

  const logoutBtn = `
    <button class="btn-icon" data-act="logout" title="${t('auth.signOut')}" aria-label="${t('auth.signOut')}">
      ${icon('logout')}
    </button>`;

  // The sidebar toggle lives in the sidebar header; this topbar twin sits at
  // the topbar's left edge (where the hidden sidebar's toggle would be) and is
  // shown (via CSS) only while the sidebar is hidden, to bring the nav back.
  const hamburger = `<button class="btn-hamburger" data-act="toggleSidebar" aria-label="${t('nav.toggleNavigation')}" title="${t('nav.toggleNavigation')}" aria-controls="sidebar" aria-expanded="${navExpanded()}">${icon('sidebar',{size:'md'})}</button>`;

  if(!S.project) {
    tb.innerHTML = `
      ${hamburger}
      <h1 class="topbar-title">${currentAppTitle()}</h1>
      <span class="topbar-actions">
        ${renderLangLinks('changeLocale')}
        ${themeBtn}
        ${liveDot}
        ${notifBtn}
        ${userBtn}
        ${logoutBtn}
      </span>`;
    updateLiveIndicator();
    // Leaving a project has to take its frozen banner with it, or the projects
    // list inherits a banner about a project that is no longer open.
    renderFrozenBar();
    return;
  }

  // The members management page is restricted to project owners/admins and
  // super-admins (canManageProjectMembers === can('project.remove_users')), so
  // hide the menu entry for everyone else rather than letting them navigate in.
  const membersMenu = AppPerms.canManageProjectMembers(S.project) ? `
        <div class="project-menu-divider" role="separator"></div>
        <div class="project-menu-label" role="presentation">${t('members.menuLabel')}</div>
        <button type="button" class="project-menu-item" role="menuitem" data-act="menuMembers" data-a0="${esc(S.project.id)}">
          ${icon('user',{size:'sm'})}
          ${t('members.menuItem')}
        </button>` : '';

  // Project statistics: a chart icon sitting immediately left of the gear.
  // Deliberately a topbar button rather than a sidebar entry — the sidebar
  // lists the places work is *done*, this is a view onto the project as a
  // whole, like the settings menu it sits next to.
  const statsBtn = `
    <button class="btn-icon${S.view === 'statistics' ? ' active' : ''}" data-act="setView" data-a0="statistics" title="${t('nav.statistics')}" aria-label="${t('nav.statistics')}"${S.view === 'statistics' ? ' aria-current="page"' : ''}>
      ${icon('stats')}
    </button>`;

  // The freeze is stated in three places, deliberately: the badge says WHICH
  // project is frozen (it sits next to the name), the banner says WHY the
  // buttons are gone and how to undo it, and the project list marks it before
  // you ever open it. One of the three alone leaves a user who lands mid-app
  // with a read-only screen and no explanation.
  const archived = AppPerms.isArchivedProject(S.project);
  const projectActions = `
    ${statsBtn}
    <span class="project-settings-wrap" id="project-settings-wrap">
      <button class="btn-icon" id="project-settings-btn" data-act="toggleProjectMenu" title="${t('nav.projectSettings')}" aria-label="${t('nav.projectSettings')}" aria-haspopup="true" aria-expanded="false">
        ${icon('settings',{size:'md'})}
      </button>
      <div class="project-menu" id="project-menu" role="menu">
        <div class="project-menu-label" role="presentation">${t('project.export')}</div>
        <button type="button" class="project-menu-item" role="menuitem" data-act="exportProject" data-a0="${esc(S.project.id)}">
          ${icon('download',{size:'sm'})}
          ${t('project.exportProject')}
        </button>
        ${AppPerms.isReadOnlyProject(S.project) ? '' : `
        <button type="button" class="project-menu-item" role="menuitem" data-act="importProject" data-a0="${esc(S.project.id)}">
          ${icon('upload',{size:'sm'})}
          ${t('project.importProject')}
        </button>
        ${FEATURES.jiraCsvImport ? `
        <button type="button" class="project-menu-item" role="menuitem" data-act="importJiraCsv" data-a0="${esc(S.project.id)}">
          ${icon('upload',{size:'sm'})}
          ${t('project.importJiraCsv')}
        </button>` : ''}`}
        ${AppPerms.canManageProjectMembers(S.project) ? `
        <div class="project-menu-divider" role="separator"></div>
        <div class="project-menu-label" role="presentation">${t('taskSettings.menuLabel')}</div>
        <button type="button" class="project-menu-item" role="menuitem" data-act="openTaskSettings" data-a0="${esc(S.project.id)}">
          ${icon('settings',{size:'sm'})}
          ${t('taskSettings.menuItem')}
        </button>` : ''}
        ${membersMenu}
        <div class="project-menu-divider" role="separator"></div>
        <div class="project-menu-label" role="presentation">${t('nav.projects')}</div>
        <button type="button" class="project-menu-item" role="menuitem" data-act="menuEdit" data-a0="${esc(S.project.id)}"${archived ? ` disabled title="${esc(t('project.frozenHint'))}"` : ''}>
          ${icon('edit',{size:'sm'})}
          ${t('project.edit')}
        </button>
        ${S.project.status === 'ARCHIVED' ? `
        <button type="button" class="project-menu-item" role="menuitem" data-act="menuUnarchive" data-a0="${esc(S.project.id)}">
          ${icon('refresh',{size:'sm'})}
          ${t('project.unarchive')}
        </button>` : `
        <button type="button" class="project-menu-item" role="menuitem" data-act="menuArchive" data-a0="${esc(S.project.id)}" data-a1="${esc(esc(S.project.name))}">
          ${icon('archive',{size:'sm'})}
          ${t('project.archive')}
        </button>`}
        <button type="button" class="project-menu-item danger" role="menuitem" data-act="menuDelete" data-a0="${esc(S.project.id)}" data-a1="${esc(esc(S.project.name))}">
          ${icon('delete',{size:'sm'})}
          ${t('project.delete')}
        </button>
      </div>
    </span>`;

  tb.innerHTML = `
    ${hamburger}
    <h1 class="topbar-breadcrumb"><button type="button" class="breadcrumb-link" data-act="backToProjects">${t('nav.allProjects')}</button> / <span>${esc(S.project.name)}</span>${archived ? ` <span class="badge badge-archived project-archived-badge">${t('project.archivedBadge')}</span>` : ''} / ${currentAppTitle()}</h1>
    <span class="topbar-actions">
      ${renderLangLinks('changeLocale')}
      ${themeBtn}
      ${liveDot}
      ${notifBtn}
      ${userBtn}
      ${projectActions}
      ${logoutBtn}
    </span>`;
  updateLiveIndicator();
  updateNotifBadge();
  renderFrozenBar();
}

// renderFrozenBar states the archived project's read-only state in the one place
// a user is looking when a button they expected is missing: above the content,
// in flow, so it never covers anything and never has to be dismissed.
//
// The affordances themselves are not hidden one by one — AppPerms.can() answers
// false for every write permission while the freeze holds, so the board's
// per-lane "Add task", the create button, inline create, the lane edit/delete
// controls and the drag handles all stop rendering through the checks they
// already had. What is left here is saying so, and offering the way out to the
// owner who can take it.
function renderFrozenBar() {
  const bar = el('#project-frozen-bar');
  if (!bar) return;
  if (!S.project || !AppPerms.isArchivedProject(S.project)) {
    bar.classList.add('hidden');
    bar.innerHTML = '';
    return;
  }
  // Unarchive is owner-only (memberGuard + RequireOwner, not writerGuard —
  // writerGuard is exactly what the freeze blocks), and the project's own menu
  // gates it the same way, so a member who cannot undo it is told the state
  // without being offered a button that would 403.
  const canUndo = AppPerms._projectRole(S.project) === 'PROJECT_OWNER'
    || S.user?.globalRole === 'SUPER_ADMIN';
  bar.innerHTML = `
    ${icon('archive', { size: 'sm' })}
    <span class="project-frozen-text">${esc(t('project.frozenBanner'))}</span>
    ${canUndo ? `<button type="button" class="btn btn-sm" data-act="menuUnarchive" data-a0="${esc(S.project.id)}">${esc(t('project.unarchive'))}</button>` : ''}`;
  bar.classList.remove('hidden');
}

// _projectMenuClickHandler is tracked so hideProjectMenu() can always detach
// the pending outside-click listener, however the menu closes (outside click,
// the toggle button, or a menu action) — otherwise closing via a menu action
// leaves a stale listener stacked on document every time the menu reopens.
let _projectMenuClickHandler = null;

function toggleProjectMenu(e) {
  e.stopPropagation();
  const menu = el('#project-menu');
  const btn  = el('#project-settings-btn');
  if (!menu) return;
  const isOpen = menu.classList.contains('open');
  if (isOpen) { hideProjectMenu(); return; }
  menu.classList.add('open');
  if (btn) btn.setAttribute('aria-expanded', 'true');
  _hideNotifPanel();
  _projectMenuClickHandler = () => hideProjectMenu();
  document.addEventListener('click', _projectMenuClickHandler, { once: true });
}

function hideProjectMenu() {
  const menu = el('#project-menu');
  const btn  = el('#project-settings-btn');
  if (menu) menu.classList.remove('open');
  if (btn) btn.setAttribute('aria-expanded', 'false');
  if (_projectMenuClickHandler) {
    document.removeEventListener('click', _projectMenuClickHandler);
    _projectMenuClickHandler = null;
  }
}

// exportProject downloads the whole project as a re-importable ZIP archive
// ("Projekt exportieren"): tasks with their comments, links and files, the doc
// pages, and the planning structure they sit in — releases, sprints, boards,
// categories and templates.
async function exportProject(projectId) {
  hideProjectMenu();
  try {
    const url = `${API_BASE}${BASE_PATH}/projects/${projectId}/export`;
    const headers = {};
    if (Auth.token) headers['Authorization'] = 'Bearer ' + Auth.token;
    const r = await fetch(url, { headers, credentials: 'include' });
    if (!r.ok) throw await errorFromResponse(r);
    const blob = await r.blob();
    const cd = r.headers.get('Content-Disposition') || '';
    const fnMatch = cd.match(/filename[^;=\n]*=(?:(['"])([^'"]*)\1|([^;\n]*))/);
    const filename = (fnMatch && (fnMatch[2] || fnMatch[3])) || `${projectId}-export.zip`;
    const a = document.createElement('a');
    a.href = URL.createObjectURL(blob);
    a.download = filename;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(a.href);
    toast(t('project.exported'), 'success');
  } catch(e) {
    toast(e.message ? apiErrorMessage(e) : t('project.exportFailed'), 'error');
  }
}

// importProject uploads a project export ZIP into the current project and
// re-renders the view so the imported content appears immediately.
function importProject(projectId) {
  hideProjectMenu();
  const input = document.createElement('input');
  input.type = 'file';
  input.accept = '.zip,application/zip';
  input.addEventListener('change', async () => {
    const file = input.files && input.files[0];
    if (!file) return;
    try {
      const res = await http.upload(`${BASE_PATH}/projects/${projectId}/import`, file);
      toast(t('project.imported'), 'success');
      reportImportWarnings(res);
      await renderContent();
    } catch(e) {
      toast(e.message ? apiErrorMessage(e) : t('project.importFailed'), 'error');
    }
  });
  input.click();
}

// reportImportWarnings surfaces a project import's per-item warnings the way
// the CSV import does. They are not cosmetic: an archive whose default board or
// active sprint collided with one the target project already has is imported
// demoted, and this toast is the only place that says so.
function reportImportWarnings(result) {
  const count = result?.warnings?.length || 0;
  if (!count) return;
  console.warn('Project import report:', result.warnings);
  toast(t('project.importWarnings', { count }), 'info');
}

// importJiraCsv uploads a Jira-compatible CSV export into the current project
// and reports the imported/skipped row counts. Edition-gated: the menu entry
// only renders when FEATURES.jiraCsvImport is on (included in ENTERPRISE,
// bookable option in BUSINESS via OCTBASE_OPTION_JIRA_IMPORT, never in TEAM)
// and the backend rejects the call with FEATURE_DISABLED otherwise.
function importJiraCsv(projectId) {
  hideProjectMenu();
  const input = document.createElement('input');
  input.type = 'file';
  input.accept = '.csv,text/csv';
  input.addEventListener('change', async () => {
    const file = input.files && input.files[0];
    if (!file) return;
    try {
      const res = await http.upload(`${BASE_PATH}/projects/${projectId}/import/jira-csv`, file);
      const imported = res?.imported ?? 0;
      const skipped = res?.skipped ?? 0;
      const attachments = res?.attachmentsImported ?? 0;
      if (skipped > 0) {
        toast(t('project.jiraCsvImportedPartial', { imported, skipped }), 'info');
      } else {
        toast(t('project.jiraCsvImported', { count: imported }), 'success');
      }
      if (attachments > 0) {
        // Imported attachments are links back into Jira, not copies — say so,
        // or a team that decommissions Jira right after migrating loses them.
        toast(t('project.jiraCsvAttachmentNote', { count: attachments }), 'info');
      }
      if (res?.warnings?.length) {
        console.warn('Jira CSV import report:', res.warnings);
        toast(t('project.jiraCsvImportWarnings', { count: res.warnings.length }), 'info');
      }
      await renderContent();
    } catch(e) {
      toast(e.message ? apiErrorMessage(e) : t('project.importFailed'), 'error');
    }
  });
  input.click();
}

// importNewProject creates a brand-new project from a project export ZIP
// (name, description etc. come from the archive) and jumps into it.
function importNewProject() {
  const input = document.createElement('input');
  input.type = 'file';
  input.accept = '.zip,application/zip';
  input.addEventListener('change', async () => {
    const file = input.files && input.files[0];
    if (!file) return;
    try {
      const res = await http.upload(`${BASE_PATH}/projects/import`, file);
      toast(t('project.imported'), 'success');
      reportImportWarnings(res?.import);
      if (res?.project?.id) await selectProject(res.project.id);
      else await renderContent();
    } catch(e) {
      toast(e.message ? apiErrorMessage(e) : t('project.importFailed'), 'error');
    }
  });
  input.click();
}

function setFilter(key, value) {
  S.filters[key] = value;
  // Update URL to reflect filter state.
  const base = `/projects/${S.project.id}/${S.view}`;
  const params = taskFilterParams();
  const search = params.toString();
  const newHash = base + (search ? '?' + search : '');
  history.replaceState(null, '', '#' + newHash);
  // The type/status/priority filters are applied client-side (applyTaskFilters),
  // so — like setSearchFilter — re-render only the affected list region
  // (the registry entry's refreshList) instead of calling renderContent(),
  // which would blank #content to a spinner and refetch the whole view
  // (reads as a full page reload).
  const def = Views.get(S.view);
  if (def?.refreshList) def.refreshList();
  else renderContent();
}

// _searchTail is the expensive half of setSearchFilter, coalesced across a burst
// of keystrokes: re-rendering the list region and rewriting the URL.
//
// Both were being done per keystroke. The re-render rebuilds every visible row or
// card from the cached task set; the URL rewrite is worse than it looks, because
// Safari throttles history.replaceState to roughly 100 calls per 30 seconds and
// then THROWS a SecurityError — a fast typist searching a board could break the
// URL bar (and take the keystroke handler down with it) without doing anything
// unusual.
//
// The view and project are captured at keystroke time and re-checked here: a
// navigation during the debounce window must not have its URL rewritten by the
// tail of a search that belonged to the previous view.
const _searchTail = debounced(140, (projectId, view) => {
  if (!S.project || S.project.id !== projectId || S.view !== view) return;
  const search = taskFilterParams().toString();
  history.replaceState(null, '', `#/projects/${projectId}/${view}` + (search ? '?' + search : ''));
  Views.get(view)?.refreshList?.();
});

// setSearchFilter handles the free-text task search on the backlog (full text)
// and the board (filter by name). Unlike the select filters it re-renders only
// the affected list region so the input keeps focus while typing, and reflects
// the query in the URL so it survives a reload / is shareable.
//
// The query itself is recorded synchronously, so no keystroke can be lost and
// anything reading S.filters.q sees the current text; only the rebuild and the
// URL write are deferred (see _searchTail). The input element is never
// re-rendered by either, so the caret never moves.
function setSearchFilter(node) {
  S.filters.q = node.value;
  if (!S.project) return;
  _searchTail(S.project.id, S.view);
}

// ═══════════════════════════════════════════════════════════
// ROUTER / VIEW SWITCHER
// ═══════════════════════════════════════════════════════════
async function setView(v) {
  closeSidebar();
  hideModal();
  // Close panel WITHOUT updating the URL (closeTaskPanel would replaceState to the
  // new URL, making router.go a no-op and suppressing the hashchange event).
  S.taskPanelId = null;
  const panel = el('#task-panel');
  if (panel) panel.classList.remove('open');

  S.view = v;
  // Status filtering exists only on the Task view; clear it when leaving so it
  // doesn't ride along in another view's URL or narrow it with no visible control.
  if (v !== 'tasks') S.filters.status = '';
  // Same reasoning one level wider: the type and priority selects live in the
  // list toolbar (Task view, Backlog). The board applies both — applyTaskFilters
  // is shared — but renders no control for either, so a filter carried over from
  // the list hid every card with nothing on screen to explain it or clear it: a
  // board that simply read as empty. The registry decides, so a future view that
  // grows a toolbar keeps its filters without another special case here.
  if (!Views.get(v)?.listToolbar) { S.filters.priority = ''; S.filters.type = ''; }
  S.selectedTasks.clear();
  updateBulkBar();

  if (S.project) {
    // Update URL via replaceState (avoids duplicate hashchange) and render directly.
    const params = taskFilterParams();
    const search = params.toString();
    history.replaceState(null, '', `#/projects/${S.project.id}/${v}` + (search ? '?' + search : ''));
    renderSidebar(); renderTopbar(); await renderContent();
  } else {
    renderSidebar(); renderTopbar(); await renderContent();
  }
}

async function renderContent() {
  const c = el('#content');
  if (!c) return;
  // Supersede any in-flight render from a previous call (rapid navigation):
  // the view renderers below check this against S.contentGen before their
  // final DOM write and bail out if a newer call has already started.
  const gen = ++S.contentGen;
  // Whatever prompted this repaint, the data about to be drawn is fresh, so the
  // stale banner has nothing left to announce. Clearing here means navigation,
  // filtering and the banner's own reload button all dismiss it for free.
  clearContentStale();
  c.classList.remove('content-board', 'content-pages');
  c.style.padding = '';
  c.innerHTML = `<div class="loading"><div class="spinner"></div></div>`;
  // The view registry supplies the renderer (registry.js) — the shell has no
  // per-view branches. An unknown/disabled S.view renders nothing, exactly like
  // the old switch's missing default.
  const def = Views.get(S.view);
  if (!def) return;
  try {
    // A standalone view (the dashboard) owns its full lifecycle including
    // sidebar/topbar — dispatch and stop.
    if (def.standalone) { await def.render(); return; }
    await def.render();
    if (gen !== S.contentGen) return;
    // The create button (and, for task-list views, the filters) live in a
    // toolbar at the top of the main content area rather than the topbar.
    // Prepended after the view renders for registry entries with listToolbar;
    // the board renders its own toolbar inline, and the dashboard and pages
    // views inject their create button in their own render functions.
    if (def.listToolbar) prependListToolbar();
  } catch(e) {
    if (gen !== S.contentGen) return;
    c.innerHTML = `<div class="empty"><div class="empty-icon">${icon('warning',{size:'hero'})}</div><div class="empty-title">${t('errors.viewLoadFailed')}</div><p>${esc(apiErrorMessage(e))}</p></div>`;
  }
}

// ═══════════════════════════════════════════════════════════
// DASHBOARD ("My Work")
// ═══════════════════════════════════════════════════════════
async function goToDashboard() {
  S.project = null;
  S.view = 'dashboard';
  router.go('/dashboard');
}

async function renderDashboardPage() {
  if (!el('#content')) return;
  const c = el('#content');
  // The dashboard is reachable both via renderContent (which resets these) and
  // directly from the router, so clear any board/pages layout classes and inline
  // padding here too — otherwise they leak in and break the dashboard padding.
  c.classList.remove('content-board', 'content-pages');
  c.style.padding = '';
  c.innerHTML = `<div class="loading"><div class="spinner"></div></div>`;
  S.project = null;
  S.view = 'dashboard';
  renderSidebar();
  renderTopbar();

  try {
    const dash = await api.dashboard();
    const { assignedTasks, reviewingTasks, recentPages, upcomingReleases, projects = [], boards = [] } = dash;
    const projectName = Object.fromEntries(projects.map(p => [p.id, p.name]));

    const taskRow = (t) => `
      <button type="button" class="dash-task-row" data-act="openProjectTask" data-a0="${esc(t.id)}" data-a1="${esc(t.projectId)}">
        ${typeBadge(t.taskType)} ${priorityDot(t.priority)}
        <span class="dash-task-title">${esc(t.title)}</span>
        ${statusBadge(t.status)}
      </button>`;
    const pageRow = (p) => `
      <button type="button" class="dash-page-row" data-act="openProjectPage" data-a0="${esc(p.projectId)}" data-a1="${esc(p.id)}">
        <span aria-hidden="true">${icon('page',{size:'sm'})}</span> ${esc(p.title)} <span class="text-muted text-sm">${esc(p.projectName)}</span>
      </button>`;
    const releaseRow = (m) => `
      <button type="button" class="dash-release-row" data-act="openProjectReleases" data-a0="${esc(m.projectId)}">
        <span aria-hidden="true">${icon('milestone',{size:'sm'})}</span> ${esc(m.name)} <span class="text-muted text-sm">${fmtDate(m.dueDate)}</span>
      </button>`;
    const projectRow = (p) => `
      <button type="button" class="dash-project-row" data-act="nav" data-a0="/projects/${esc(p.id)}/board">
        <span aria-hidden="true">${icon('project',{size:'sm'})}</span> ${esc(p.name)}
      </button>`;
    const boardRow = (b) => `
      <button type="button" class="dash-board-row" data-act="nav" data-a0="/projects/${esc(b.projectId)}/board">
        <span aria-hidden="true">${icon('board',{size:'sm'})}</span> ${esc(b.name)} <span class="text-muted text-sm">${esc(projectName[b.projectId]||'')}</span>
      </button>`;

    c.innerHTML = `
      ${contentToolbar('dashboard', false)}
      <div class="dashboard-grid grid-2col">
        <section class="dash-section" aria-labelledby="dash-assigned-title">
          <h2 class="dash-section-title" id="dash-assigned-title">${t('dashboard.assignedToMe',{count:assignedTasks.length})}</h2>
          ${assignedTasks.length ? assignedTasks.map(taskRow).join('') : `<div class="dash-empty">${t('dashboard.nothingAssigned')}</div>`}
        </section>
        <section class="dash-section" aria-labelledby="dash-review-title">
          <h2 class="dash-section-title" id="dash-review-title">${t('dashboard.inReview',{count:reviewingTasks.length})}</h2>
          ${reviewingTasks.length ? reviewingTasks.map(taskRow).join('') : `<div class="dash-empty">${t('dashboard.noReviewsPending')}</div>`}
        </section>
        <section class="dash-section" aria-labelledby="dash-pages-title">
          <h2 class="dash-section-title" id="dash-pages-title">${t('dashboard.recentPages')}</h2>
          ${recentPages.length ? recentPages.map(pageRow).join('') : `<div class="dash-empty">${t('dashboard.noRecentPages')}</div>`}
        </section>
        <section class="dash-section" aria-labelledby="dash-releases-title">
          <h2 class="dash-section-title" id="dash-releases-title">${t('dashboard.upcomingReleases')}</h2>
          ${upcomingReleases.length ? upcomingReleases.map(releaseRow).join('') : `<div class="dash-empty">${t('dashboard.noReleasesDue')}</div>`}
        </section>
        <section class="dash-section" aria-labelledby="dash-projects-title">
          <h2 class="dash-section-title" id="dash-projects-title">${t('dashboard.myProjects')}</h2>
          ${projects.length ? projects.map(projectRow).join('') : `<div class="dash-empty">${t('dashboard.noProjects')}</div>`}
        </section>
        <section class="dash-section" aria-labelledby="dash-boards-title">
          <h2 class="dash-section-title" id="dash-boards-title">${t('dashboard.myBoards')}</h2>
          ${boards.length ? boards.map(boardRow).join('') : `<div class="dash-empty">${t('dashboard.noBoards')}</div>`}
        </section>
      </div>`;
  } catch(e) {
    c.innerHTML = `<div class="empty"><div class="empty-title">${t('errors.generic')}</div><p>${esc(apiErrorMessage(e))}</p></div>`;
  }
}

// ═══════════════════════════════════════════════════════════
// SEARCH PAGE (/search)
// ═══════════════════════════════════════════════════════════
async function renderSearchPage(initialQ) {
  const c = el('#content');
  if (!c) return;
  S.project = null;
  S.view = 'search';
  renderSidebar(); renderTopbar();
  c.innerHTML = `
    <div class="search-page">
      <div class="search-header">
        <label class="sr-only" for="sp-input">${t('palette.searchPlaceholder')}</label>
        <input class="form-input search-page-input" id="sp-input" value="${esc(initialQ||'')}" placeholder="${t('palette.searchPlaceholderEllipsis')}" data-keydown="searchEnter">
        <button class="btn btn-primary" data-act="runSearchPage">${t('search.searchButton')}</button>
      </div>
      <div id="sp-results" role="region" aria-live="polite" aria-label="${t('search.searchButton')}"></div>
    </div>`;
  if (initialQ) runSearchPage();
}

async function runSearchPage() {
  const input = el('#sp-input');
  if (!input) return;
  const q = input.value.trim();
  if (!q) return;
  history.replaceState(null, '', '#/search?q=' + encodeURIComponent(q));
  const results = el('#sp-results');
  results.innerHTML = `<div class="loading"><div class="spinner"></div></div>`;
  try {
    const r = await api.search(q);
    let html = '';
    if (r.tasks.length) {
      html += `<h2>${t('nav.tasks')}</h2>`;
      html += r.tasks.map(task=>`<button type="button" class="search-result" data-act="openProjectTask" data-a0="${esc(task.id)}" data-a1="${esc(task.projectId)}">
        ${statusBadge(task.status)} ${esc(task.title)} <span class="text-muted text-sm">${esc(task.projectName)}</span>
      </button>`).join('');
    }
    if (r.pages.length) {
      html += `<h2>${t('nav.pages')}</h2>`;
      html += r.pages.map(p=>`<button type="button" class="search-result" data-act="openProjectPage" data-a0="${esc(p.projectId)}" data-a1="${esc(p.id)}"><span aria-hidden="true">${icon('page',{size:'sm'})}</span> ${esc(p.title)} <span class="text-muted text-sm">${esc(p.projectName)}</span></button>`).join('');
    }
    if (r.projects.length) {
      html += `<h2>${t('nav.projects')}</h2>`;
      html += r.projects.map(p=>`<button type="button" class="search-result" data-act="nav" data-a0="/projects/${p.id}/board"><span aria-hidden="true">${icon('project',{size:'sm'})}</span> ${esc(p.name)}</button>`).join('');
    }
    results.innerHTML = html || `<div class="dash-empty">${t('search.noResults')}</div>`;
  } catch(e) { results.innerHTML = `<div class="empty">${esc(apiErrorMessage(e))}</div>`; }
}

// ═══════════════════════════════════════════════════════════
// PROJECT HELPERS
// ═══════════════════════════════════════════════════════════
// _loadProjectGen guards against out-of-order concurrent calls (e.g. rapid
// navigation between two projects): each call captures the generation it
// started at and checks it again after each await, so a slower, superseded
// call can never overwrite S.project/releases/…/SSE with stale data after a
// newer call has already committed its own.
let _loadProjectGen = 0;

// The project's own record, and the bundle of project-scoped lists every view
// draws from. Split out from loadProject so boot can start them before it runs
// (prefetchProject, below) — the requests are identical either way.
//
// Every call here needs the project *id*, not the project object, so they all go
// out together. Awaiting api.projects.get first — as loadProject did — bought
// nothing and cost a full round trip, since the bundle could not start until it
// came back.
function _fetchProject(pid) {
  return api.projects.get(pid);
}

function _fetchProjectBundle(pid) {
  return Promise.all([
    api.releases.list(pid).catch(() => []),
    api.sprints.list(pid).catch(() => []),
    api.members.list(pid).catch(() => []),
    api.members.assignable(pid).catch(() => []),
    api.repos.list(pid).catch(() => []),
    api.boards.getDefault(pid).catch(() => null),
    api.priorities.list(pid).catch(() => []),
    AppPerms.loadPermissions(pid),
  ]);
}

// prefetchProject starts loadProject's requests without committing anything to
// S. Boot calls it for a project landing route while the session/user/project-
// list round trip is still in flight, so the project the user actually asked for
// travels with that wave instead of a full round trip behind it. Nothing here
// depends on those three responses — only on a token, which is already in hand
// by the time initApp runs.
function prefetchProject(pid) {
  Prefetch.start('project:' + pid, () => _fetchProject(pid));
  Prefetch.start('projectBundle:' + pid, () => _fetchProjectBundle(pid));
}

async function loadProject(pid) {
  if (S.project && S.project.id === pid) return;
  const gen = ++_loadProjectGen;
  // The project is still awaited before anything is committed to S, so a
  // 403/404 on it aborts exactly as before (the bundle members each absorb
  // their own failure, and an unawaited rejection cannot escape).
  const projectP = Prefetch.take('project:' + pid, () => _fetchProject(pid));
  const bundleP = Prefetch.take('projectBundle:' + pid, () => _fetchProjectBundle(pid));
  bundleP.catch(() => {});   // never unhandled if the project rejects first
  const project = await projectP;
  if (gen !== _loadProjectGen) return;
  S.project = project;
  rememberProjectVisit(pid);
  const [releases, sprints, members, assignables, repos, board, priorities] = await bundleP;
  if (gen !== _loadProjectGen) return;
  S.releases = releases;
  S.sprints = sprints;
  S.members = members;
  // Fall back to the members list if the assignable-users call failed, so the
  // pickers degrade to their old contents rather than going empty.
  S.assignables = assignables.length ? assignables : members;
  S.repos = repos;
  S.board = board;
  S.priorities = priorities;
  // Seed the name/avatar lookup from the wider list: it is a superset of the
  // members, and a task's creator or reviewer may be a non-member global admin.
  S.assignables.forEach(m => { S.usersMap[m.userId] = { name: m.name, email: m.email, avatarUpdatedAt: m.avatarUpdatedAt }; });
  members.forEach(m => { S.usersMap[m.userId] = { name: m.name, email: m.email, avatarUpdatedAt: m.avatarUpdatedAt }; });
  connectSSE(pid);
}

async function showProjectsView() {
  S.project = null;
  disconnectSSE();
  S.view = 'projects';
  S.selectedPage = null;
  S.pageEditMode = false;
  renderSidebar(); renderTopbar(); await renderProjects();
}

function backToProjects() {
  S.project = null;
  disconnectSSE();
  router.go('/projects');
}

async function selectProject(id) {
  S.selectedPage = null;
  S.pageEditMode = false;
  await loadProject(id);
  router.go(`/projects/${id}/board`);
}

function openProjectTask(taskId, projectId) {
  if (!projectId) {
    openTaskPanel(taskId);
    return;
  }
  router.go(`/projects/${projectId}/board?task=${encodeURIComponent(taskId)}`);
}

async function openProjectPage(projectId, pageId) {
  S.selectedPage = pageId;
  S.pageEditMode = false;
  if (S.project?.id === projectId && S.view === 'pages') {
    await renderPages();
    return;
  }
  router.go(`/projects/${projectId}/pages`);
}

function openProjectReleases(projectId) {
  if (S.project?.id === projectId && S.view === 'releases') return;
  router.go(`/projects/${projectId}/releases`);
}

// ═══════════════════════════════════════════════════════════
// PROJECTS VIEW
// ═══════════════════════════════════════════════════════════
async function renderProjects() {
  S.projects = await api.projects.list();
  const c = el('#content');
  if(!c) return;
  if(!S.projects.length) {
    c.innerHTML = `<div class="empty"><div class="empty-icon">${icon('project',{size:'hero'})}</div><div class="empty-title">${t('project.emptyTitle')}</div><p>${t('project.emptyBody')}</p><br><button class="btn btn-primary" data-act="showCreateProject">${icon('add',{size:'md'})} ${t('nav.newProject')}</button> <button class="btn btn-secondary" data-act="importNewProject">${icon('upload',{size:'md'})} ${t('project.importNew')}</button></div>`;
    return;
  }
  c.innerHTML = `
    ${contentToolbar('projects', false)}
    <div class="projects-grid grid-2col">
      ${S.projects.map(p=>`
        <div class="project-card" role="button" tabindex="0" aria-label="${t('task.openProject',{name:esc(p.name)})}" data-act="selectProject" data-a0="${esc(p.id)}" data-keydown="activateOnEnter">
          <div class="project-card-header">
            <div class="project-card-icon project-icon" aria-hidden="true">${esc(p.name.charAt(0).toUpperCase())}</div>
            <div class="project-card-body">
              <div class="project-card-name">${esc(p.name)}</div>
              <div class="project-card-desc">${esc(p.description||t('project.noDescription'))}</div>
            </div>
          </div>
          <div class="project-card-meta">
            <span class="badge ${p.status==='ARCHIVED'?'badge-archived':'badge-done'}">${t('project.status.'+p.status)}</span>
            <span class="badge ${p.visibility==='PUBLIC'?'badge-in-progress':'badge-planned'}">${p.visibility==='PUBLIC'?t('project.public'):t('project.private')}</span>
            <div class="project-card-actions" data-act="stop">
              <button class="btn btn-secondary btn-sm" data-act="showEditProject" data-a0="${esc(p.id)}">${t('form.edit')}</button>
              <button class="btn btn-danger btn-sm" data-act="deleteProject" data-a0="${esc(p.id)}" data-a1="${esc(esc(p.name))}">${t('form.delete')}</button>
            </div>
          </div>
        </div>`).join('')}
    </div>`;
}

// ═══════════════════════════════════════════════════════════
// INLINE TASK CREATION
// ═══════════════════════════════════════════════════════════
function showInlineTaskCreate() {
  // Reachable by keyboard shortcut as well as by a button, so the check is
  // repeated here — hiding the button is not the same as closing the door.
  // isReadOnlyProject, not isArchivedProject: a viewer reaching this by
  // shortcut got the creator, typed a title and collected a 403 for it.
  if (AppPerms.isReadOnlyProject(S.project)) return;
  const existing = el('.inline-task-create');
  if (existing) { existing.querySelector('input')?.focus(); return; }

  const row = document.createElement('div');
  row.className = 'inline-task-create';
  row.innerHTML = `
    <label class="sr-only" for="inline-task-input">${t('task.newTaskTitle')}</label>
    <input class="form-input inline-task-input" id="inline-task-input" placeholder="${t('task.inlineCreatePlaceholder')}" autofocus>`;
  const input = row.querySelector('input');

  input.addEventListener('keydown', async (e) => {
    if (e.key === 'Enter') {
      const title = input.value.trim();
      if (!title) return;
      try {
        const task = await api.tasks.create(S.project.id, { title });
        invalidateProjectTasks();
        await maybePlaceOnBoard(task.id);
        toast(t('task.created'), 'success');
        input.value = '';
        input.focus();
        await renderContent();
      } catch(err) { toast(err.message, 'error'); }
    } else if (e.key === 'Escape') {
      row.remove();
    }
  });

  // Insert at top of the content area or first column.
  const firstCol = el('.board-col-tasks') || el('.backlog-list') || el('#content');
  firstCol.insertBefore(row, firstCol.firstChild);
  input.focus();
  input.value = '';
}

// ═══════════════════════════════════════════════════════════
// BULK TASK ACTIONS
// ═══════════════════════════════════════════════════════════
// The re-entrancy guard lives in S.bulkInFlight (state.js) because the
// task-list engine's bulkSetStatus (views-tasklist.js) shares it.

function toggleTaskSelect(taskId, checked) {
  if (checked) S.selectedTasks.add(taskId);
  else S.selectedTasks.delete(taskId);
  syncSelectAllCheckbox();
  updateBulkBar();
}

// toggleSelectAll selects/clears every task checkbox currently in the list
// (backlog or task list) and syncs the bulk-action bar.
function toggleSelectAll(checked) {
  document.querySelectorAll('.task-checkbox').forEach(cb => {
    const id = cb.dataset.taskId;
    if (checked) S.selectedTasks.add(id); else S.selectedTasks.delete(id);
    cb.checked = checked;
  });
  updateBulkBar();
}

// syncSelectAllCheckbox keeps the header "select all" box in sync: checked when
// every visible row is selected, indeterminate on a partial selection.
function syncSelectAllCheckbox() {
  const master = el('.select-all-checkbox');
  if (!master) return;
  const boxes = [...document.querySelectorAll('.task-checkbox')];
  const selected = boxes.filter(cb => cb.checked).length;
  master.checked = boxes.length > 0 && selected === boxes.length;
  master.indeterminate = selected > 0 && selected < boxes.length;
}

function syncBulkSelectionWithVisible() {
  const visibleIds = new Set([...document.querySelectorAll('.task-checkbox')].map(cb => cb.dataset.taskId));
  for (const id of [...S.selectedTasks]) {
    if (!visibleIds.has(id)) S.selectedTasks.delete(id);
  }
}

function updateBulkBar() {
  const bar = el('#bulk-bar');
  if (!bar) return;
  // The task panel (side sheet) takes precedence over the bottom action bar —
  // hide the bulk bar while a task is open so the two don't overlap. The board
  // has no selection at all (cards carry no checkboxes), so the bar never shows
  // there; bulk actions are a backlog-only affordance.
  if (S.taskPanelId || S.view === 'board') { bar.classList.add('hidden'); return; }
  const count = S.selectedTasks.size;
  if (count === 0) { bar.classList.add('hidden'); return; }
  bar.classList.remove('hidden');

  // "Add to board" moves the selection onto the board as Planned items (see
  // bulkAddToBoard) — a single button rather than a per-column picker. It is a
  // Backlog-only affordance (the Task view already includes board tasks).
  const addToBoard = (S.view === 'backlog' && S.board?.columns?.length)
    ? `<button class="btn btn-sm btn-secondary" data-act="bulkAddToBoard">${t('task.addToBoard')}</button>` : '';

  // The Task view groups by status, so it re-enables bulk "Set status" — the one
  // extra option the management layer adds to the bottom bar. It rides the
  // existing backend set_status bulk action (no new endpoint); the backend
  // validates the value and requires write permission, so this is UX only.
  const setStatus = (S.view === 'tasks')
    ? `<label class="sr-only" for="bulk-status">${t('accessibility.setStatusSelected')}</label>
       <select class="form-select-sm" id="bulk-status" aria-label="${t('accessibility.setStatusSelected')}">
         <option value="">${t('task.setStatus')}</option>
         ${taskViewBulkStatusOptions()}
       </select>` : '';

  // The assignee control shares personOptions with the task panel, so the bulk
  // bar offers the same people the panel does. It used to map S.members, which
  // silently dropped every global admin who reaches the project without a
  // membership row — the Administrator was simply not in the list.
  bar.innerHTML = `
    <div class="bulk-bar-content">
      <label class="sr-only" for="bulk-assignee">${t('accessibility.assignSelectedTo')}</label>
      <select class="form-select-sm" id="bulk-assignee" aria-label="${t('accessibility.assignSelectedTo')}">
        <option value="">${t('task.assignTo')}</option>
        ${personOptions(null)}
      </select>
      <label class="sr-only" for="bulk-priority">${t('accessibility.setPrioritySelected')}</label>
      <select class="form-select-sm" id="bulk-priority" aria-label="${t('accessibility.setPrioritySelected')}">
        <option value="">${t('task.setPriority')}</option>
        ${priorityNames(S.priorities).map(p=>`<option value="${esc(p)}">${esc(priorityMeta(p).label)}</option>`).join('')}
      </select>
      ${setStatus}
      ${addToBoard}
      <button class="btn btn-sm btn-secondary" data-act="bulkArchive">${t('task.archive')}</button>
      ${AppPerms.can('task.delete') ? `<button class="btn btn-sm btn-danger" data-act="bulkDelete">${t('form.delete')}</button>` : ''}
    </div>`;
  el('#bulk-assignee').onchange = (e) => { if(e.target.value) applyBulkAction('set_assignee', e.target.value); };
  el('#bulk-priority').onchange = (e) => { if(e.target.value) applyBulkAction('set_priority', e.target.value); };
  const bulkStatus = el('#bulk-status');
  // bulkSetStatus reconciles board placement (a board task's status is owned by
  // its lane) before applying the bulk status update; see views-tasklist.js.
  if (bulkStatus) bulkStatus.onchange = (e) => { if(e.target.value) bulkSetStatus(e.target.value); };
}

// applyBulkAction runs one bulk action over the current selection. When the
// server changes fewer tasks than were selected — the delete action silently
// keeps a task that still has subtasks, for instance — reporting a plain
// success ("Deleted 0 tasks") reads as "nothing happened". So when a
// `partialKey` is supplied and there is a shortfall, it surfaces an honest
// partial/failure message instead. Without a `partialKey` the behaviour is
// unchanged.
async function applyBulkAction(action, value, toastKey='task.updatedCount', partialKey=null) {
  if (S.bulkInFlight) return;
  S.bulkInFlight = true;
  const taskIds = [...S.selectedTasks];
  try {
    const result = await api.tasks.bulk(S.project.id, { taskIds, action, value });
    const skipped = taskIds.length - result.updated;
    if (partialKey && skipped > 0) {
      toast(t(partialKey, { updated: result.updated, total: taskIds.length, skipped }),
            result.updated ? 'info' : 'error');
    } else {
      toast(t(toastKey, { count: result.updated }), 'success');
    }
    await renderContent();
    syncBulkSelectionWithVisible();
    updateBulkBar();
  } catch(e) { toast(apiErrorMessage(e), 'error'); }
  finally { S.bulkInFlight = false; }
}

async function bulkArchive() {
  const count = S.selectedTasks.size;
  // Reuses confirmModal (framework.js) rather than a hand-rolled showModal
  // call, same as rtUploadFiles' image-insert prompt.
  if (await confirmModal(t('task.archive'), t('task.confirmArchive',{count}), t('task.archive'))) {
    await applyBulkAction('archive', '');
  }
}

async function bulkDelete() {
  const count = S.selectedTasks.size;
  if (!count) return;
  // Same modal overlay as the task panel's delete (confirmDelete), not a native
  // confirm dialog.
  confirmDelete(t('task.deleteTitle'), t('task.confirmDeleteBulk',{count}), async () => {
    await applyBulkAction('delete', '', 'task.deletedCount', 'task.deletedPartial');
  });
}

// bulkAddToBoard moves the selected backlog items onto the board as Planned
// items. They land in the Planned lane (the column whose status is PLANNED), or
// the first lane if the board has no Planned column; either way each card's
// status is set to that lane's status so the board stays consistent.
async function bulkAddToBoard() {
  if (S.bulkInFlight || !S.board) return;
  const col = (S.board.columns || []).find(c => c.status === 'PLANNED') || S.board.columns?.[0];
  if (!col) return;
  S.bulkInFlight = true;
  const taskIds = [...S.selectedTasks];
  try {
    const results = await Promise.allSettled(
      taskIds.map(taskId =>
        api.boards.move(S.board.id, { taskId, boardColumnId: col.id, boardRank: 1000 })
          .then(movedTask => api.tasks.status(taskId, col.status, movedTask?.version).catch(() => {}))
      )
    );
    const moved = results.filter(r => r.status === 'fulfilled').length;
    const failed = results.length - moved;
    toast(failed ? t('task.movedCountFailed',{moved,total:taskIds.length,failed}) : t('task.movedCount',{count:moved,target:col.name || t('nav.board')}), failed ? 'error' : 'success');
    await renderContent();
    syncBulkSelectionWithVisible();
    updateBulkBar();
  } catch(e) { toast(apiErrorMessage(e), 'error'); }
  finally { S.bulkInFlight = false; }
}

function taskCheckbox(taskId, title) {
  const label = title != null ? ` aria-label="${t('task.selectTask',{title:esc(title)})}"` : '';
  return `<input type="checkbox" class="task-checkbox" data-task-id="${esc(taskId)}"${label} data-change="toggleTaskSelect" data-a0="${esc(taskId)}" ${S.selectedTasks.has(taskId)?'checked':''} data-act="stop">`;
}

// selectAllCheckbox renders the master "select all" box for a checkbox list.
// ids are the task ids in the list, used to reflect the all/partial state.
function selectAllCheckbox(ids = []) {
  const all = ids.length > 0 && ids.every(id => S.selectedTasks.has(id));
  return `<input type="checkbox" class="select-all-checkbox" aria-label="${t('task.selectAll')}" title="${t('task.selectAll')}" data-change="toggleSelectAll" data-act="stop" ${all?'checked':''}>`;
}

// ── view registration (see registry.js for the contract) ──
// The dashboard re-renders sidebar/topbar itself (it is also reachable
// straight from the router), so it dispatches as standalone.
Views.register('dashboard', { scope: 'global', standalone: true, render: renderDashboardPage });
Views.register('projects',  { scope: 'global', render: renderProjects });

// ── Delegation registration: this file's handlers ───────────────────────────
// (see js/README.md "Delegation registration".)
registerActions([
  backToProjects, bulkAddToBoard, bulkArchive, bulkDelete, goToDashboard, importNewProject,
  runSearchPage,
], _A0);
registerActions([
  exportProject, importJiraCsv, importProject, openProjectReleases, selectProject, setView,
], _A1);
registerActions([openProjectPage, openProjectTask], _A2);
registerActions({
  toggleProjectMenu: (el, ev) => toggleProjectMenu(ev),
  menuMembers:   () => { hideProjectMenu(); setView('members'); },
  menuEdit:      el => { hideProjectMenu(); showEditProject(el.dataset.a0); },
  menuArchive:   el => { hideProjectMenu(); archiveProject(el.dataset.a0, el.dataset.a1); },
  menuUnarchive: el => { hideProjectMenu(); unarchiveProject(el.dataset.a0); },
  menuDelete:    el => { hideProjectMenu(); deleteProject(el.dataset.a0, el.dataset.a1); },
});
registerChanges([toggleTaskSelect], _CHK);
registerChanges([toggleSelectAll], _CHK0);
registerChanges({
  setFilter: el => setFilter(el.dataset.a0, el.value),
});
registerInputs({
  setSearchFilter: el => setSearchFilter(el),
});
registerKeydowns({
  searchEnter: (el, ev) => { if (ev.key === 'Enter') runSearchPage(); },
});

export { applyBulkAction, bulkAddToBoard, bulkArchive, bulkDelete, contentToolbar, hideProjectMenu, loadProject, openProjectPage, openProjectTask, prefetchProject, prependListToolbar, renderContent, renderDashboardPage, renderProjects, renderSearchPage, renderSidebar, renderTopbar, selectAllCheckbox, selectProject, setView, showInlineTaskCreate, showProjectsView, taskCheckbox, updateBulkBar, viewCreateButton };
