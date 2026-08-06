import { i18n, t } from '@octbase/shared/i18n.js';
import { ESTIMATION_UNITS, PRIORITIES, TYPE_META, boardLaneLimit, estimableType, estimateLabel, estimateLimits, estimationEnabled, estimationField, estimationUnit, parseEstimateInput, priorityMeta, priorityNames, projectTaskTypes, typeParentRule } from '@octbase/shared/meta.js';
import { api } from './api.js';
import { Auth } from './auth.js';
import { loadFeatureConfig } from './config.js';
import { _A0, _A1, _A2, _A3, _VAL0, registerActions, registerChanges, registerInputs, registerKeydowns } from './delegation.js';
import { USE_STANDALONE_DEMO_AUTH } from './env.js';
import { confirmDelete, confirmModal, el, esc, filterCountLabel, getThemePref, initials, renderAppShell, segSwitch, setUserAvatarImage, showModal, toast, typeBadge } from './framework.js';
import { apiErrorMessage } from './http.js';
import { icon } from './icons.js';
import { AppPerms } from './permissions.js';
import { disconnectSSE, markSession, startIdleTimer, startNotifPolling, startSessionHeartbeat } from './realtime.js';
import { Views } from './registry.js';
import { handleRoute, router } from './router.js';
import { S, taskSeqLabel } from './state.js';
import { maybePlaceOnBoard } from './views-content.js';
import { reconcilePreferences } from './views-settings.js';
import { hideProjectMenu, prefetchProject, renderContent, renderProjects, renderSidebar, renderTopbar, selectProject } from './views-shell.js';
import { invalidateProjectTasks, personOptions } from './views-task.js';

// Octbase SPA — split from the former single app.js. One ES module among many,
// bundled by Vite (37b stage 2): its top-level declarations are file-private
// and its public surface is the `export { … }` block at the bottom. Imports
// carry the dependencies — there is no load order to keep in step
// (js/README.md).

// Live, non-archived project tasks cached while the create-task modal is open,
// so re-rendering the parent select on a type change needs no refetch.
let _createTaskParents = [];

// taskParentSelectHtml renders the parent <select> for a task of the given
// type: the options are the candidate tasks of the type one level up in the
// project's hierarchy chain (typeParentRule). Empty string for the chain's
// top type, which cannot have a parent. A required parent (SUBTASK) gets no
// "no parent" option.
function taskParentSelectHtml(taskType, candidates, selectedId, excludeId) {
  const rule = typeParentRule(S.project, taskType);
  if (!rule || !rule.parentType) return '';
  const opts = candidates.filter(tk => tk.taskType === rule.parentType && tk.id !== excludeId);
  const label = (tk) => (taskSeqLabel(tk) ? taskSeqLabel(tk) + ' — ' : '') + tk.title;
  return `
      <label class="form-label" for="task-parent">${t('task.parentLabel')} (${TYPE_META[rule.parentType].label})</label>
      <select class="form-select" id="task-parent">
        ${rule.required ? '' : `<option value="">${t('task.noParent')}</option>`}
        ${opts.map(tk=>`<option value="${esc(tk.id)}" ${selectedId===tk.id?'selected':''}>${esc(label(tk))}</option>`).join('')}
      </select>`;
}

// createTaskEstimateHtml renders the create modal's estimate field, and renders
// nothing when the project does not estimate or the chosen type is a container
// (EPIC/INITIATIVE/THEME) — the same two conditions the task panel applies, so
// the dialog can never offer an estimate the API would reject with
// ESTIMATION_UNIT_INACTIVE or ESTIMATION_NOT_ALLOWED_FOR_TYPE. Left empty the
// task is created unestimated, which is a different state from a typed 0.
function createTaskEstimateHtml(taskType, value = '') {
  if (!estimationEnabled(S.project) || !estimableType(taskType)) return '';
  const unit   = estimationUnit(S.project);
  const label  = estimateLabel(S.project);
  const limits = estimateLimits(unit);
  return `
      <label class="form-label" for="task-estimate-create">${label}</label>
      <input class="form-input" id="task-estimate-create" type="number" inputmode="decimal"
        placeholder="${t('task.estimateNone')}" min="0" max="${limits.max}"
        step="${limits.step}" value="${esc(String(value))}">`;
}

// createTaskTypeChanged re-renders the create modal's parent field whenever
// the chosen type moves the task to another hierarchy level, and re-renders the
// estimate field with it: switching to a container type must take the estimate
// box away, while a move between two leaf types keeps whatever was typed.
function createTaskTypeChanged(taskType) {
  const group = el('#task-parent-group');
  if (group) group.innerHTML = taskParentSelectHtml(taskType, _createTaskParents, '', '');
  const estGroup = el('#task-estimate-group');
  if (estGroup) estGroup.innerHTML = createTaskEstimateHtml(taskType, el('#task-estimate-create')?.value || '');
}

async function showCreateTask(columnId) {
  // Every page: the parent select must offer the project's older epics and
  // stories, which a single 200-row page (created_at DESC) drops first.
  _createTaskParents = ((await api.tasks.listAll(S.project.id).catch(() => [])) || [])
    .filter(tk => tk.status !== 'ARCHIVED');
  showModal(t('task.create'), `
    <div class="form-group"><label class="form-label" for="task-title">${t('form.title')}</label><input class="form-input" id="task-title" placeholder="${t('task.titlePlaceholder')}" autofocus></div>
    <div class="form-group"><label class="form-label" for="task-type">${t('task.typeLabel')}</label>
      <select class="form-select" id="task-type" data-change="createTaskTypeChanged">${projectTaskTypes(S.project).map(tt=>`<option value="${tt}">${TYPE_META[tt].label}</option>`).join('')}</select>
    </div>
    <div class="form-group" id="task-parent-group">${taskParentSelectHtml('TASK', _createTaskParents, '', '')}</div>
    <div class="form-group"><label class="form-label" for="task-priority">${t('task.priorityLabel')}</label>
      <select class="form-select" id="task-priority">${priorityNames(S.priorities).map(p=>`<option value="${esc(p)}" ${p==='MEDIUM'?'selected':''}>${esc(priorityMeta(p).label)}</option>`).join('')}</select>
    </div>
    <div class="form-group" id="task-estimate-group">${createTaskEstimateHtml('TASK')}</div>
    <div class="form-group"><label class="form-label" for="task-release">${t('release.label')}</label>
      <select class="form-select" id="task-release"><option value="">${t('form.none')}</option>
        ${S.releases.filter(m=>m.status==='PLANNED').map(m=>`<option value="${m.id}">${esc(m.name)}</option>`).join('')}
      </select>
    </div>
    <div class="form-group"><label class="form-label" for="task-sprint-create">${t('sprint.label')}</label>
      <select class="form-select" id="task-sprint-create">
        <option value="">${t('sprint.none')}</option>
        ${S.sprints.filter(s=>s.status!=='COMPLETED').map(s=>`<option value="${s.id}"${s.status==='ACTIVE'?' selected':''}>${esc(s.name)}${s.status==='ACTIVE'?' '+t('sprint.active'):''}</option>`).join('')}
      </select>
    </div>
    <div class="form-group"><label class="form-label" for="task-assignee-create">${t('task.assignee')}</label>
      <select class="form-select" id="task-assignee-create">
        <option value="">${t('task.unassigned')}</option>
        ${personOptions('')}
      </select>
    </div>
    <div class="form-group"><label class="form-label" for="task-create-due">${t('task.dueDateLabel')}</label>
      <input class="form-input" id="task-create-due" type="date">
    </div>
    <div class="form-group"><label class="form-label" for="task-create-desc">${t('task.description')}</label><textarea class="form-input" id="task-create-desc" rows="3" placeholder="${t('task.descriptionPlaceholder')}"></textarea></div>`,
    async () => {
      const title = el('#task-title')?.value?.trim();
      if(!title) throw Object.assign(new Error(t('validation.titleRequired')), {field:'task-title'});
      const taskType = el('#task-type')?.value || 'TASK';
      const parentId = el('#task-parent')?.value || '';
      if (typeParentRule(S.project, taskType).required && !parentId) {
        throw Object.assign(new Error(t('task.parentRequired')), {field:'task-parent'});
      }
      const d = {
        title,
        taskType,
        priority: el('#task-priority')?.value || 'MEDIUM',
        description: el('#task-create-desc')?.value || '',
      };
      if (parentId) d.parentId = parentId;
      const ms = el('#task-release')?.value;
      if(ms) d.releaseId = ms;
      const sprintId = el('#task-sprint-create')?.value;
      if (sprintId) d.sprintId = sprintId;
      const dueDate = el('#task-create-due')?.value;
      if (dueDate) d.dueDate = dueDate;
      const assigneeId = el('#task-assignee-create')?.value;
      if (assigneeId) d.assigneeId = assigneeId;
      // The estimate reaches the API as a number or not at all (see
      // parseEstimateInput's null-vs-0 contract). The field is absent for a
      // container type, which is why estimableType gates the read as well as
      // the render.
      if (estimationEnabled(S.project) && estimableType(taskType)) {
        const value = parseEstimateInput(el('#task-estimate-create')?.value);
        if (value === undefined) {
          throw Object.assign(new Error(t('task.estimateInvalid')), {field: 'task-estimate-create'});
        }
        if (value !== null) d[estimationField(S.project)] = value;
      }
      const task = await api.tasks.create(S.project.id, d);
      invalidateProjectTasks();
      await maybePlaceOnBoard(task.id, columnId);
      toast(t('task.created'),'success');
      await renderContent();
    }, t('form.save'), {title: 'task-title'});
}

// ═══════════════════════════════════════════════════════════
// PROJECT CRUD
// ═══════════════════════════════════════════════════════════
function showCreateProject() {
  if (!AppPerms.canCreateProject()) { toast(t('errors.onlyAdminsCreateProjects'), 'error'); return; }
  showModal(t('project.new'), `
    <div class="form-group"><label class="form-label" for="proj-name">${t('form.name')}</label><input class="form-input" id="proj-name" placeholder="${t('project.namePlaceholder')}" autofocus></div>
    <div class="form-group"><label class="form-label" for="proj-abbr">${t('project.abbreviation')}</label><input class="form-input input-abbr" id="proj-abbr" maxlength="4" placeholder="${t('project.abbreviationPlaceholder')}"></div>
    <div class="form-group"><label class="form-label" for="proj-desc">${t('form.description')}</label><textarea class="form-input" id="proj-desc" rows="2" placeholder="${t('task.descriptionPlaceholder')}"></textarea></div>
    <div class="form-group"><label class="form-label" for="proj-vis">${t('project.visibility')}</label>
      <select class="form-select" id="proj-vis"><option value="PRIVATE">${t('project.private')}</option><option value="PUBLIC">${t('project.public')}</option></select>
    </div>`,
    async () => {
      const name = el('#proj-name')?.value?.trim();
      if(!name) throw new Error(t('validation.nameRequired'));
      const p = await api.projects.create({
        name, abbreviation: el('#proj-abbr')?.value?.trim()?.toUpperCase()||'', description: el('#proj-desc')?.value||'', visibility: el('#proj-vis')?.value||'PRIVATE',
      });
      toast(t('project.created'),'success');
      S.projects.push(p);
      await selectProject(p.id);
    });
}

function showEditProject(projectId) {
  const p = S.projects.find(p => p.id === projectId);
  if (!p) return;
  showModal(t('project.edit'), `
    <div class="form-group"><label class="form-label" for="proj-name">${t('form.name')}</label><input class="form-input" id="proj-name" value="${esc(p.name)}" autofocus></div>
    <div class="form-group"><label class="form-label" for="proj-abbr">${t('project.abbreviation')}</label><input class="form-input input-abbr" id="proj-abbr" value="${esc(p.abbreviation||'')}" maxlength="4" placeholder="${t('project.abbreviationPlaceholder')}"></div>
    <div class="form-group"><label class="form-label" for="proj-desc">${t('form.description')}</label><textarea class="form-input" id="proj-desc" rows="2">${esc(p.description||'')}</textarea></div>
    <div class="form-group"><label class="form-label" for="proj-vis">${t('project.visibility')}</label>
      <select class="form-select" id="proj-vis">
        <option value="PRIVATE" ${p.visibility==='PRIVATE'?'selected':''}>${t('project.private')}</option>
        <option value="PUBLIC"  ${p.visibility==='PUBLIC' ?'selected':''}>${t('project.public')}</option>
      </select>
    </div>`,
    async () => {
      const name = el('#proj-name')?.value?.trim();
      if (!name) throw new Error(t('validation.nameRequired'));
      const updated = await api.projects.update(projectId, {
        name, abbreviation: el('#proj-abbr')?.value?.trim()?.toUpperCase() || '', description: el('#proj-desc')?.value || '', visibility: el('#proj-vis')?.value || 'PRIVATE',
        // Reject stale edits with 409; the cache below is replaced from the
        // response, which carries the incremented version.
        version: p.version,
      });
      const idx = S.projects.findIndex(x => x.id === projectId);
      if (idx !== -1) S.projects[idx] = updated;
      if (S.project?.id === projectId) S.project = updated;
      toast(t('project.updated'), 'success');
      renderSidebar(); renderTopbar();
      if (S.view === 'projects') await renderProjects();
    });
}

// ═══════════════════════════════════════════════════════════
// PROJECT MEMBERS & PERMISSIONS
// ═══════════════════════════════════════════════════════════
const PROJECT_ROLES = ['PROJECT_OWNER', 'PROJECT_ADMIN', 'PROJECT_MEMBER', 'PROJECT_VIEWER'];

// Page-level filter state and an id-keyed cache, mirroring the Admin
// user-management page (admin.js) so project membership gets the same stat
// cards, search/role filtering, and card-row layout. _membersCtx holds the
// resolved permission flags + add-candidate list so row handlers, the live
// re-filter, and the invite/add modals can read them without re-fetching.
const MembersPageState = { search: '', roleFilter: '' };
const _memberMap = {};
let _membersCtx = null;

const PROJECT_ROLE_BADGE = {
  PROJECT_OWNER: 'badge-critical', PROJECT_ADMIN: 'badge-in-progress',
  PROJECT_MEMBER: 'badge-planned', PROJECT_VIEWER: 'badge-archived',
};

// renderProjectMembersPage renders project membership as a full page (the
// 'members' project sub-view, reached from the project settings menu) styled
// like the Admin user-management panel. Mutations (role change, remove, invite,
// add-existing) are gated by the cached project permissions; the backend remains
// authoritative. Re-invoked after every mutation to refresh stats and the list.
async function renderProjectMembersPage() {
  const project = S.project;
  if (!project) return;
  const c = el('#content');
  if (!c) return;
  c.innerHTML = `<div class="page-loader">${t('admin.loadingUsers')}</div>`;

  let members;
  try {
    members = await api.members.list(project.id);
  } catch (e) {
    c.innerHTML = `<div class="empty"><div class="empty-title">${t('errors.loadFailed')}</div><p>${esc(apiErrorMessage(e))}</p></div>`;
    return;
  }
  await AppPerms.loadPermissions(project.id);
  // Keep the shared member list in sync, and with it the wider candidate list
  // the assignee/reviewer dropdowns actually read.
  S.members = members;
  await refreshAssignables(project.id);

  // Super Admins may add existing accounts directly (no invitation email). The
  // user directory is a Super-Admin-only endpoint, so only fetch it for them.
  const isSuperAdmin = S.user?.globalRole === 'SUPER_ADMIN';
  let allUsers = [];
  if (isSuperAdmin) {
    try { allUsers = await api.users.list(); } catch { allUsers = []; }
  }

  const perms = S.permissionsByProject[project.id];
  const isOwner = perms?.role === 'PROJECT_OWNER' || isSuperAdmin;
  const memberIds = new Set(members.map(m => m.userId));
  _membersCtx = {
    projectId: project.id,
    canChangeRoles: AppPerms.can('project.change_roles', project),
    canRemove: AppPerms.can('project.remove_users', project),
    canInvite: AppPerms.can('project.invite_users', project),
    isSuperAdmin,
    assignableRoles: PROJECT_ROLES.filter(r => r !== 'PROJECT_OWNER' || isOwner),
    // Existing accounts not already in the project — candidates for direct add.
    addableUsers: isSuperAdmin ? allUsers.filter(u => !memberIds.has(u.id) && u.status !== 'disabled') : [],
  };

  Object.keys(_memberMap).forEach(k => delete _memberMap[k]);
  members.forEach(m => { _memberMap[m.userId] = m; });

  _renderMembersPanel(c, members);
}

function _renderMembersPanel(c, members) {
  const { search, roleFilter } = MembersPageState;
  const ctx = _membersCtx;
  const filtered = _filterMembers(members);

  const total   = members.length;
  const owners  = members.filter(m => m.role === 'PROJECT_OWNER').length;
  const admins  = members.filter(m => m.role === 'PROJECT_ADMIN').length;
  const regular = members.filter(m => m.role === 'PROJECT_MEMBER').length;
  const viewers = members.filter(m => m.role === 'PROJECT_VIEWER').length;

  const headerActions = [
    ctx.canInvite
      ? `<button class="btn btn-primary" data-act="showInviteMemberModal" data-a0="${esc(ctx.projectId)}">${t('members.invite')}</button>` : '',
    (ctx.isSuperAdmin && ctx.addableUsers.length)
      ? `<button class="btn btn-secondary" data-act="showAddExistingMemberModal" data-a0="${esc(ctx.projectId)}">${t('members.addExisting')}</button>` : '',
  ].join('');

  c.innerHTML = `
    <div class="admin-panel">
      <div class="admin-header">
        <h2 class="admin-title">${t('members.title')}</h2>
        <div class="admin-header-actions">${headerActions}</div>
      </div>

      <div class="admin-stats">
        <div class="admin-stat">
          <span class="admin-stat-value">${total}</span>
          <span class="admin-stat-label">${t('admin.total')}</span>
        </div>
        <div class="admin-stat">
          <span class="admin-stat-value">${owners}</span>
          <span class="admin-stat-label">${t('members.role.PROJECT_OWNER')}</span>
        </div>
        <div class="admin-stat">
          <span class="admin-stat-value">${admins}</span>
          <span class="admin-stat-label">${t('members.role.PROJECT_ADMIN')}</span>
        </div>
        <div class="admin-stat">
          <span class="admin-stat-value">${regular}</span>
          <span class="admin-stat-label">${t('members.role.PROJECT_MEMBER')}</span>
        </div>
        <div class="admin-stat">
          <span class="admin-stat-value">${viewers}</span>
          <span class="admin-stat-label">${t('members.role.PROJECT_VIEWER')}</span>
        </div>
      </div>

      <div class="admin-filters">
        <input class="form-input admin-search" id="members-search"
               placeholder="${t('admin.searchPlaceholder')}"
               value="${esc(search)}"
               data-input="membersSearch">
        <select class="form-select-sm" data-change="membersRoleFilter">
          <option value="">${t('admin.allRoles')}</option>
          ${PROJECT_ROLES.map(r => `
            <option value="${r}" ${roleFilter===r?'selected':''}>${t('members.role.'+r)}</option>`).join('')}
        </select>
        <span class="admin-filter-count" id="members-filter-count">${filterCountLabel(filtered.length, total)}</span>
      </div>

      <div class="admin-user-list" id="members-user-list">${_renderMemberListItems(filtered)}</div>
    </div>`;
}

function _filterMembers(members) {
  const { search, roleFilter } = MembersPageState;
  const q = search.toLowerCase();
  return members.filter(m => {
    if (q && !(m.name || '').toLowerCase().includes(q) && !(m.email || '').toLowerCase().includes(q)) return false;
    if (roleFilter && m.role !== roleFilter) return false;
    return true;
  });
}

function _renderMemberListItems(filtered) {
  if (filtered.length === 0) return `<div class="empty empty--sm"><p>${t('admin.noUsersMatch')}</p></div>`;
  const ctx = _membersCtx;
  return filtered.map(m => {
    const roleCell = ctx.canChangeRoles
      ? `<select class="form-select-sm" aria-label="${t('members.roleHeader')}" data-prev="${m.role}" data-change="changeMemberRole" data-a0="${esc(ctx.projectId)}" data-a1="${esc(m.userId)}">
          ${PROJECT_ROLES.filter(r => ctx.assignableRoles.includes(r) || r === m.role)
            .map(r => `<option value="${r}" ${m.role===r?'selected':''}>${t('members.role.'+r)}</option>`).join('')}
        </select>`
      : `<span class="badge ${PROJECT_ROLE_BADGE[m.role] || 'badge-planned'}">${t('members.role.'+m.role)}</span>`;
    const removeBtn = ctx.canRemove
      ? `<button class="btn btn-danger btn-sm" data-act="removeMember" data-a0="${esc(ctx.projectId)}" data-a1="${esc(m.userId)}" data-a2="${esc(m.name)}">${t('members.remove')}</button>`
      : '';
    const youTag = m.userId === S.user?.id ? ` <span class="text-muted text-sm">${t('members.you')}</span>` : '';
    return `
    <div class="admin-user-row">
      <div class="admin-user-avatar${m.avatarUpdatedAt ? ' has-avatar' : ''}">${esc(initials(m.name))}${m.avatarUpdatedAt ? `<img class="avatar-img" alt="" aria-hidden="true" data-avatar-user="${esc(m.userId)}" data-avatar-v="${esc(m.avatarUpdatedAt)}">` : ''}</div>
      <div class="admin-user-info">
        <div class="admin-user-name">${esc(m.name)}${youTag}</div>
        <div class="admin-user-email">${esc(m.email)}</div>
      </div>
      <div class="admin-user-meta">${roleCell}</div>
      <div class="admin-user-actions">${removeBtn}</div>
    </div>`;
  }).join('');
}

// Re-filter from the cache and patch only the list + count — keeps focus/caret
// in #members-search and the role <select> intact while the user types.
function _refilterMembers() {
  if (!_membersCtx) return;
  const cached = Object.values(_memberMap);
  const filtered = _filterMembers(cached);
  const list = el('#members-user-list');
  const count = el('#members-filter-count');
  if (list)  list.innerHTML = _renderMemberListItems(filtered);
  if (count) count.textContent = filterCountLabel(filtered.length, cached.length);
}

// showInviteMemberModal sends an email invitation to a (possibly not-yet-
// registered) address. On success the page is re-rendered so a freshly invited
// member shows up once they accept.
function showInviteMemberModal(projectId) {
  const ctx = _membersCtx;
  showModal(t('members.invite'), `
    <div class="form-group">
      <label class="form-label" for="member-invite-email">${t('members.inviteEmail')}</label>
      <input class="form-input" id="member-invite-email" type="email" placeholder="teammate@example.com" autofocus>
    </div>
    <div class="form-group">
      <label class="form-label" for="member-invite-role">${t('members.inviteRoleLabel')}</label>
      <select class="form-select" id="member-invite-role">
        ${ctx.assignableRoles.map(r => `<option value="${r}" ${r==='PROJECT_MEMBER'?'selected':''}>${t('members.role.'+r)}</option>`).join('')}
      </select>
    </div>`, async () => {
    const email = el('#member-invite-email')?.value?.trim();
    if (!email) throw { field: 'member-invite-email', message: t('validation.emailRequired') };
    const role = el('#member-invite-role')?.value || 'PROJECT_MEMBER';
    const res = await api.invitations.create({ email, projectId, role });
    toast(t('members.inviteSent'), 'success');
    if (res?.acceptURL) {
      try { await navigator.clipboard.writeText(res.acceptURL); toast(t('members.inviteLinkCopied'), 'info'); } catch {}
    }
    await renderProjectMembersPage();
  }, t('members.sendInvite'));
}

// showAddExistingMemberModal adds an already-registered account to the project
// directly, without an invitation email (Super-Admin only).
function showAddExistingMemberModal(projectId) {
  const ctx = _membersCtx;
  const userOptions = ctx.addableUsers
    .map(u => `<option value="${esc(u.id)}">${esc(u.displayName || u.email)} (${esc(u.email)})</option>`)
    .join('');
  showModal(t('members.addExisting'), `
    <div class="form-group">
      <label class="form-label" for="member-add-user">${t('members.addExistingUser')}</label>
      <select class="form-select" id="member-add-user">${userOptions}</select>
    </div>
    <div class="form-group">
      <label class="form-label" for="member-add-role">${t('members.inviteRoleLabel')}</label>
      <select class="form-select" id="member-add-role">
        ${ctx.assignableRoles.map(r => `<option value="${r}" ${r==='PROJECT_MEMBER'?'selected':''}>${t('members.role.'+r)}</option>`).join('')}
      </select>
    </div>`, async () => {
    const userId = el('#member-add-user')?.value;
    if (!userId) throw { field: 'member-add-user', message: t('members.addExistingNone') };
    const role = el('#member-add-role')?.value || 'PROJECT_MEMBER';
    const membership = await api.members.add(projectId, { userId, role });
    toast(t('members.added'), 'success');
    // Patch the new member into the cached list and re-render the panel in place,
    // rather than forcing a full reload (network refetch + loading spinner flash).
    _addMemberInPlace(userId, membership);
  }, t('members.addExistingButton'));
}

// _addMemberInPlace inserts a freshly added existing account into the members
// cache and re-renders the panel without the visible page reload. The add
// endpoint returns the bare membership, so the user's name/email are taken from
// the addable-users directory we already loaded for the modal.
function _addMemberInPlace(userId, membership) {
  const ctx = _membersCtx;
  if (!ctx) { renderProjectMembersPage(); return; }
  const u = ctx.addableUsers.find(x => x.id === userId);
  _memberMap[userId] = {
    id: membership?.id,
    projectId: ctx.projectId,
    userId,
    name: u?.displayName || u?.email || '',
    email: u?.email || '',
    role: membership?.role || 'PROJECT_MEMBER',
    joinedAt: membership?.createdAt,
  };
  // The account is now a member, so drop it from the "add existing" candidates.
  ctx.addableUsers = ctx.addableUsers.filter(x => x.id !== userId);
  const members = Object.values(_memberMap);
  S.members = members;
  // Fire-and-forget: the panel re-renders in place now, and the pickers pick up
  // the new candidate list when the refresh lands.
  refreshAssignables(ctx.projectId);
  const c = el('#content');
  if (c) _renderMembersPanel(c, members);
}

// refreshAssignables re-reads the assignee/reviewer candidate list after a
// membership change. It is a superset of S.members — it also carries the global
// admins — so it cannot be derived from the incremental member-map updates the
// members page keeps. A failed refresh leaves the previous list in place rather
// than emptying the pickers.
async function refreshAssignables(projectId) {
  try { S.assignables = await api.members.assignable(projectId); } catch { /* keep the previous candidates */ }
}

async function changeMemberRole(projectId, userId, role, selectEl) {
  const previous = selectEl.dataset.prev;
  try {
    await api.members.updateRole(projectId, userId, role);
    selectEl.dataset.prev = role;
    toast(t('members.roleUpdated'), 'success');
    if (userId === S.user?.id) {
      await AppPerms.loadPermissions(projectId);
      renderTopbar();
    }
  } catch (e) {
    toast(apiErrorMessage(e), 'error');
    selectEl.value = previous;
  }
}

function removeMember(projectId, userId, name) {
  confirmDelete(t('members.removeTitle'), t('members.removeConfirm', { name }), async () => {
    await api.members.remove(projectId, userId);
    toast(t('members.removed'), 'success');
    // Patch the removed member out of the cached list and re-render the panel in
    // place, rather than forcing a full reload (network refetch + loading spinner
    // flash). Mirrors _addMemberInPlace.
    _removeMemberInPlace(userId);
  }, null, t('members.remove'));
}

// _removeMemberInPlace drops a member from the cache and re-renders the panel
// without the visible page reload. The removed account becomes a candidate for
// the Super-Admin "add existing" flow again, mirroring how _addMemberInPlace
// drops it from that list on add.
function _removeMemberInPlace(userId) {
  const ctx = _membersCtx;
  if (!ctx) { renderProjectMembersPage(); return; }
  const removed = _memberMap[userId];
  delete _memberMap[userId];
  if (ctx.isSuperAdmin && removed && !ctx.addableUsers.some(u => u.id === userId)) {
    ctx.addableUsers = ctx.addableUsers.concat({ id: userId, displayName: removed.name, email: removed.email });
  }
  const members = Object.values(_memberMap);
  S.members = members;
  // Fire-and-forget: the panel re-renders in place now, and the pickers pick up
  // the new candidate list when the refresh lands.
  refreshAssignables(ctx.projectId);
  const c = el('#content');
  if (c) _renderMembersPanel(c, members);
}

// archiveProject freezes the project: every write route then answers 409
// PROJECT_ARCHIVED while reads keep working. Reversible — see unarchiveProject
// — which is why this confirms rather than warns like a delete does.
function archiveProject(projectId, name) {
  confirmModal(t('project.archiveTitle'), t('project.archiveConfirm', { name }), t('project.archive'))
    .then(async ok => {
      if (!ok) return;
      try {
        const project = await api.projects.archive(projectId);
        _applyProjectStatus(project);
        toast(t('project.archived'), 'success');
      } catch (e) {
        toast(apiErrorMessage(e), 'error');
      }
    });
}

// unarchiveProject is the way back. It is not a PATCH: PATCH /projects/{id} is
// itself frozen while the project is archived, which is what made archiving a
// one-way door before the dedicated route existed.
async function unarchiveProject(projectId) {
  try {
    const project = await api.projects.unarchive(projectId);
    _applyProjectStatus(project);
    toast(t('project.unarchived'), 'success');
  } catch (e) {
    toast(apiErrorMessage(e), 'error');
  }
}

// _applyProjectStatus writes the new status into both places the shell reads it
// from — the open project and the project list — and repaints. Without the list
// copy the projects screen would keep showing the old state until a reload.
function _applyProjectStatus(project) {
  if (S.project?.id === project.id) S.project = { ...S.project, ...project };
  S.projects = (S.projects || []).map(p => (p.id === project.id ? { ...p, ...project } : p));
  // The topbar is not optional here: the gear menu lives in it, and it is what
  // renders Archive-vs-Unarchive off the project's status. Without this the
  // menu keeps offering Archive on a project that was just archived, until
  // something else happens to repaint the topbar.
  renderTopbar();
  renderSidebar();
  renderContent();
}

function deleteProject(projectId, name) {
  confirmDelete(t('project.deleteTitle'),
    t('project.deleteConfirm',{name}),
    async () => {
      await api.projects.del(projectId);
      S.projects = S.projects.filter(p => p.id !== projectId);
      toast(t('project.deleted'), 'success');
      if (S.project?.id === projectId) {
        S.project = null;
        disconnectSSE();
        router.go('/projects');
      } else {
        renderSidebar();
        await renderProjects();
      }
    });
}

// ═══════════════════════════════════════════════════════════
// TASK SETTINGS — per-project hierarchy levels & custom priorities
// (gear menu → "Task types & priorities"; project admins only)
// ═══════════════════════════════════════════════════════════

// taskSettingsPrioHtml renders the priorities section: the fixed built-in
// set, the project's deletable custom priorities, and the add form.
function taskSettingsPrioHtml() {
  return `
    <p class="ts-hint">${t('taskSettings.prioHint')}</p>
    <div class="ts-prio-builtins">
      ${PRIORITIES.map(p=>`<span class="badge prio-badge ${priorityMeta(p).cls}">${esc(priorityMeta(p).label)}</span>`).join(' ')}
    </div>
    ${S.priorities.map(cp => `
      <div class="ts-prio-row">
        <span class="badge prio-badge prio-custom">${esc(cp.name)}</span>
        <button type="button" class="icon-btn" data-act="taskSettingsDeletePriority" data-a0="${esc(cp.id)}"
          aria-label="${t('form.delete')} ${esc(cp.name)}" title="${t('form.delete')}">${icon('delete',{size:'sm'})}</button>
      </div>`).join('')}
    <div class="ts-prio-add">
      <label class="sr-only" for="ts-prio-name">${t('taskSettings.prioNameLabel')}</label>
      <input class="form-input" id="ts-prio-name" placeholder="${t('taskSettings.prioPlaceholder')}"
        maxlength="20" data-keydown="tsPrioKeydown">
      <button type="button" class="btn btn-sm btn-secondary" data-act="taskSettingsAddPriority">${t('form.add')}</button>
    </div>`;
}

// Settings apply immediately, but the view behind the dialog is repainted only
// once the dialog is gone: renderContent() blanks #content to a spinner and
// refetches, which under an open modal reads as the app reloading itself in the
// background on every toggle. Each applied change flags the view stale instead,
// and openTaskSettings' onClose does the single repaint — the settings that
// change what a view shows (hierarchy levels, estimation unit) are exactly the
// ones the user is about to look at, not the one they are still editing.
let _taskSettingsViewStale = false;

// markTaskSettingsViewStale records that an applied setting changed what the
// view behind the dialog would render.
function markTaskSettingsViewStale() {
  _taskSettingsViewStale = true;
}

async function repaintAfterTaskSettings() {
  if (!_taskSettingsViewStale) return;
  _taskSettingsViewStale = false;
  renderTopbar();
  await renderContent();
}

function openTaskSettings() {
  hideProjectMenu();
  _taskSettingsViewStale = false;
  const levelRow = (key, type) => `
    <label class="ts-level">
      <input type="checkbox" data-change="taskSettingsToggleLevel" data-a0="${key}" ${S.project[key] ? 'checked' : ''}>
      ${typeBadge(type)} <span>${t('taskSettings.' + key)}</span>
    </label>`;
  showModal(t('taskSettings.title'), `
    <h3 class="ts-heading">${t('taskSettings.levels')}</h3>
    <p class="ts-hint">${t('taskSettings.levelsHint')}</p>
    <div class="form-group">
      ${levelRow('themeEnabled', 'THEME')}
      ${levelRow('initiativeEnabled', 'INITIATIVE')}
    </div>
    <h3 class="ts-heading">${t('taskSettings.estimation')}</h3>
    <p class="ts-hint">${t('taskSettings.estimationHint')}</p>
    <div class="form-group" id="ts-estimation-unit">${taskSettingsEstimationHtml()}</div>
    <h3 class="ts-heading">${t('taskSettings.boardLane')}</h3>
    <p class="ts-hint">${t('taskSettings.boardLaneHint')}</p>
    <div class="form-group">
      <label class="form-label" for="ts-lane-limit">${t('taskSettings.boardLaneLimit')}</label>
      <input type="number" class="form-input form-input-sm" id="ts-lane-limit"
        min="0" max="500" step="1" value="${esc(String(boardLaneLimit(S.project)))}"
        aria-describedby="ts-lane-limit-hint" data-change="taskSettingsSetBoardLaneLimit">
      <p class="ts-hint" id="ts-lane-limit-hint">${t('taskSettings.boardLaneLimitUnlimited')}</p>
    </div>
    <h3 class="ts-heading">${t('taskSettings.priorities')}</h3>
    <div id="ts-prio-wrap">${taskSettingsPrioHtml()}</div>`,
    async () => {}, t('form.close'), {}, { onClose: repaintAfterTaskSettings });
}

// The estimation unit is picked with the same segmented switch the personal
// preferences use (framework.js segSwitch, styleguide `.seg-switch`) rather
// than a dropdown — three fixed options, all worth seeing at once.
function taskSettingsEstimationHtml() {
  return segSwitch(ESTIMATION_UNITS.map(u => ({ value: u, label: t('taskSettings.estimationUnitOption.' + u) })),
                   estimationUnit(S.project), 'taskSettingsSetEstimationUnit', t('taskSettings.estimationUnit'));
}

// taskSettingsSetEstimationUnit applies the estimation unit immediately, the
// same shape as the level toggles above: the version rides along so a
// concurrent project edit gets a clean 409. The switch is repainted from
// S.project *after* the server agrees, so a refusal leaves the stored unit
// checked with nothing to snap back. Switching is non-destructive — estimates
// in the unit being left keep their values and simply stop being shown.
async function taskSettingsSetEstimationUnit(unit) {
  if (unit === estimationUnit(S.project)) return;
  try {
    S.project = await api.projects.update(S.project.id, { estimationUnit: unit, version: S.project.version });
    const mount = el('#ts-estimation-unit');
    if (mount) mount.innerHTML = taskSettingsEstimationHtml();
    toast(t('form.saved'), 'success');
    markTaskSettingsViewStale();
  } catch (e) {
    toast(apiErrorMessage(e), 'error');
  }
}

// taskSettingsSetBoardLaneLimit applies the lane cap immediately, like the
// controls above. The input is a number field, so `change` fires on blur/Enter
// rather than per keystroke — one write per edit, not one per digit.
//
// A blank or non-numeric value is not sent: it would serialize as 0 and read as
// "show every card", which is the opposite of what someone clearing the field to
// retype it means. The input is snapped back to the stored value instead, so it
// never sits showing a number the project does not have.
async function taskSettingsSetBoardLaneLimit(value) {
  const input = el('#ts-lane-limit');
  const stored = boardLaneLimit(S.project);
  const n = Number.parseInt(value, 10);
  if (!Number.isInteger(n) || n < 0 || n > 500) {
    if (input) input.value = String(stored);
    return;
  }
  if (n === stored) return;
  try {
    S.project = await api.projects.update(S.project.id, { boardLaneLimit: n, version: S.project.version });
    if (input) input.value = String(boardLaneLimit(S.project));
    toast(t('form.saved'), 'success');
    markTaskSettingsViewStale();
  } catch (e) {
    if (input) input.value = String(stored);
    toast(apiErrorMessage(e), 'error');
  }
}

// taskSettingsToggleLevel applies a hierarchy-level toggle immediately (the
// version rides along so a concurrent project edit gets a clean 409). The
// server blocks disabling a level whose type is still in use.
async function taskSettingsToggleLevel(node) {
  const key = node.dataset.a0;
  try {
    S.project = await api.projects.update(S.project.id, { [key]: node.checked, version: S.project.version });
    toast(t('form.saved'), 'success');
    markTaskSettingsViewStale();
  } catch (e) {
    node.checked = !node.checked;
    toast(apiErrorMessage(e), 'error');
  }
}

async function taskSettingsAddPriority() {
  const input = el('#ts-prio-name');
  const name = input?.value?.trim();
  if (!name) return;
  try {
    await api.priorities.add(S.project.id, { name, rank: S.priorities.length });
    S.priorities = await api.priorities.list(S.project.id);
    const wrap = el('#ts-prio-wrap');
    if (wrap) wrap.innerHTML = taskSettingsPrioHtml();
    toast(t('form.saved'), 'success');
  } catch (e) {
    toast(apiErrorMessage(e), 'error');
  }
}

async function taskSettingsDeletePriority(id) {
  try {
    await api.priorities.del(id);
    S.priorities = S.priorities.filter(p => p.id !== id);
    const wrap = el('#ts-prio-wrap');
    if (wrap) wrap.innerHTML = taskSettingsPrioHtml();
    toast(t('form.deleted'), 'success');
  } catch (e) {
    toast(apiErrorMessage(e), 'error');
  }
}

// ═══════════════════════════════════════════════════════════
// INIT
// ═══════════════════════════════════════════════════════════
function applyUserToShell() {
  if (!S.user) return;
  const nameEl = el('#user-name');
  const avatarEl = el('#user-avatar');
  if (nameEl) nameEl.textContent = S.user.name;
  if (avatarEl) {
    avatarEl.textContent = initials(S.user.name);
    if (S.user.avatarUpdatedAt) {
      setUserAvatarImage(avatarEl, S.user.id, S.user.avatarUpdatedAt);
    } else {
      avatarEl.querySelector('img.avatar-img')?.remove();
      avatarEl.classList.remove('has-avatar');
    }
  }
}

// initApp boots the authenticated app. `destination`, when given, is the route
// to land on — it is applied as part of the *first* route render rather than
// before it, so the app paints once, already holding the session user and the
// project list. (Setting the hash first, as the login/MFA/invitation handlers
// used to, fired `hashchange` on top of the handleRoute() below: two full
// renders of the same page, the first with an empty sidebar.)
async function initApp(destination) {
  renderAppShell();
  // Landing straight on a project (a deep link, a reload, a bookmark) is the
  // common case, and that project's id is in the URL from the first moment —
  // so its data is requested now, alongside the three calls below, rather than
  // a full round trip behind them once the router gets there. Only a token is
  // needed for this, and one is already in hand (Auth.refresh / demoLogin ran
  // before initApp). loadProject collects these via Prefetch; if the route
  // turns out to be something else, they expire unused.
  prefetchLandingProject(destination);
  // The feature flags, the session user and the project list are independent of
  // each other, so they are fetched together: awaiting them one after another
  // put three serial round trips between the page loading and anything
  // appearing. All three still resolve before the first route render, which is
  // what the sidebar and the router need (the flags decide which views exist).
  const [, me, projects] = await Promise.all([
    loadFeatureConfig(),                       // never rejects; keeps defaults
    api.auth.me().catch(() => null),
    api.projects.list().catch(() => []),
  ]);
  S.user = me;
  markSession(!!me);
  if (S.user) {
    S.user.name = S.user.displayName || S.user.name || S.user.email || t('app.defaultUserName');
    applyUserToShell();
  }
  S.projects = projects || [];

  startNotifPolling();
  startSessionHeartbeat();
  startIdleTimer();
  router.navigate(destination);
  // Pull the server-persisted language/theme after first paint (server value
  // wins and follows the user across devices). Deliberately not awaited —
  // theme-init.js already applied the cached value before CSS, and
  // reconciliation re-renders on its own if the server disagrees.
  if (S.user) reconcilePreferences();
}

// prefetchLandingProject starts the landing route's project data when boot is
// heading for a project URL. `destination` is the route initApp was given (the
// post-login handlers pass one); with none, the current hash is where the
// router will go. The view's own data is left to the router's registry
// prefetch, which runs once the feature flags have settled and the target view
// is therefore known.
function prefetchLandingProject(destination) {
  const path = (destination || router.current() || '').split('?')[0];
  const m = path.match(/^\/projects\/([^/]+)/);
  if (m) prefetchProject(m[1]);
}

// Octbase is desktop-first. On a touch-primary mobile device we don't boot the
// app at all — we redirect to the marketing site instead. A narrowed *desktop*
// window has a fine pointer, so it still boots and gets the hamburger drawer.
const MOBILE_REDIRECT_URL = 'https://www.octbase.io';

function isMobileDevice() {
  return window.matchMedia(
    '(hover: none) and (pointer: coarse) and (max-width: 1024px)'
  ).matches;
}

async function init() {
  if (isMobileDevice()) {
    // replace() so the back button doesn't bounce straight back into the app.
    window.location.replace(MOBILE_REDIRECT_URL);
    return;
  }
  await i18n.initI18n();
  if (USE_STANDALONE_DEMO_AUTH) {
    // Auto-sign-in as the demo user to obtain a JWT for the disk-served UI.
    try { await Auth.demoLogin(); } catch { /* backend may be down; initApp will surface errors */ }
    await initApp();
    return;
  }
  // Restore a session silently only if one is hinted for this browser. Probing
  // /auth/refresh unconditionally returns a 401 for logged-out visitors (the
  // refresh cookie is HttpOnly, so we can't check for it directly) — an
  // unnecessary error on every load. The hint, set on login and cleared on
  // logout/expiry, lets us skip the probe when there's plainly no session.
  if (Auth.hasSessionHint()) {
    try {
      await Auth.refresh();
      await initApp();
      return;
    } catch { /* hinted session is gone — fall through to login */ }
  }
  // No session — show login. /config is public; fetch it in the background so
  // the pre-auth screens' version tag updates from the pre-boot default once it
  // resolves (loadFeatureConfig patches the rendered .app-version element in
  // place).
  loadFeatureConfig();
  // Pre-auth deep links carry their token in the hash and must survive boot:
  // invitation accepts, and the password-reset links mailed to users
  // (`<app>/#/reset-password/<token>`, auth/password_reset.go). Only invitations
  // were spared here, so a reset link — and the forgot-password page it is
  // requested from — was rewritten to /login and the token dropped.
  const path = router.current();
  const preAuth = path.startsWith('/invitations') || path.startsWith('/forgot-password') ||
    path.startsWith('/reset-password');
  router.navigate(preAuth ? undefined : '/login');
}

async function changeLocale(lang) {
  await i18n.setLocale(lang);
  renderAppShell();
  applyUserToShell();
  handleRoute();
  // Keep the server-persisted preference in step (fire-and-forget) so the
  // settings page and other devices see the same value.
  if (S.user) api.preferences.update({ language: lang, theme: getThemePref() }).catch(() => {});
}

// ── view registration (see registry.js for the contract) ──
// Routable but deliberately not in the sidebar: reached via the project menu
// (menuMembers), which is gated by AppPerms.canManageProjectMembers.
Views.register('members', { render: renderProjectMembersPage });

// ═══════════════════════════════════════════════════════════
// ADMIN PANEL — User Management & Audit Logs (Super Admin only)
// ═══════════════════════════════════════════════════════════

// Per-view state (persists while navigating between admin and audit-logs).

// ── Delegation registration: this file's handlers ───────────────────────────
// (see js/README.md "Delegation registration".)
registerActions([showCreateProject, taskSettingsAddPriority], _A0);
registerActions([
  showCreateTask, showEditProject, changeLocale, showInviteMemberModal, showAddExistingMemberModal,
  openTaskSettings, taskSettingsDeletePriority, taskSettingsSetEstimationUnit,
], _A1);
registerActions([deleteProject], _A2);
registerActions([removeMember], _A3);
registerChanges([createTaskTypeChanged, taskSettingsSetBoardLaneLimit], _VAL0);
registerChanges({
  membersRoleFilter: el => { MembersPageState.roleFilter = el.value; _refilterMembers(); },
  changeMemberRole:  el => changeMemberRole(el.dataset.a0, el.dataset.a1, el.value, el),
  taskSettingsToggleLevel: node => taskSettingsToggleLevel(node),
});
registerInputs({
  membersSearch: el => { MembersPageState.search = el.value; _refilterMembers(); },
});
registerKeydowns({
  tsPrioKeydown: (el, ev) => { if (ev.key === 'Enter') { ev.preventDefault(); taskSettingsAddPriority(); } },
});

export { applyUserToShell, archiveProject, changeLocale, createTaskTypeChanged, deleteProject, init, initApp, showCreateProject, showCreateTask, showEditProject, taskSettingsSetEstimationUnit, unarchiveProject };
