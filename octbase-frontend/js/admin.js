import { getLocale, t } from '@octbase/shared/i18n.js';
import { api } from './api.js';
import { LIMITS } from './config.js';
import { _A0, _A1, registerActions, registerChanges, registerInputs } from './delegation.js';
import { confirmDelete, el, esc, filterCountLabel, initials, showModal, toast } from './framework.js';
import { apiErrorMessage } from './http.js';
import { AppPerms } from './permissions.js';
import { S } from './state.js';
import { renderSidebar, renderTopbar } from './views-shell.js';

// Octbase SPA — split from the former single app.js. One ES module among many,
// bundled by Vite (37b stage 2): its top-level declarations are file-private
// and its public surface is the `export { … }` block at the bottom. Imports
// carry the dependencies — there is no load order to keep in step
// (js/README.md).
const AdminState = { search:'', roleFilter:'', statusFilter:'', auditPage:0, auditAction:'' };

// ID-keyed user cache — lets onclick handlers reference full user objects
// without embedding raw data in HTML attributes.
const _adminUserMap = {};

// ── Shared helpers ────────────────────────────────────────

const ROLE_BADGE   = { SUPER_ADMIN:'badge-critical', ADMIN:'badge-in-progress', USER:'badge-planned', GUEST:'badge-archived' };
const STATUS_BADGE = { active:'badge-done', disabled:'badge-archived', invited:'badge-in-review' };
const ROLE_AVATAR_CLASS = { SUPER_ADMIN:' admin-user-avatar--SUPER_ADMIN', ADMIN:' admin-user-avatar--ADMIN', USER:' admin-user-avatar--USER', GUEST:' admin-user-avatar--GUEST' };

// Every action the backend writes to the audit log (auditlog.Action* in
// internal/auditlog/domain.go). It is the filter dropdown AND the label lookup,
// so an action missing here is unfilterable and renders as its raw enum —
// which is how nine of them, including every password and MFA event, sat
// invisible in the filter while being logged. scripts/check-audit-actions.mjs
// fails the build when the two lists drift apart again.
const ACTION_KEYS = [
  'LOGIN_SUCCESS','LOGIN_FAILED','REFRESH_TOKEN_REUSE',
  'USER_CREATED','USER_UPDATED','USER_DISABLED','USER_ENABLED','USER_ROLE_CHANGED',
  'USER_EMAIL_CHANGED','USER_DELETED',
  'USER_PASSWORD_RESET','USER_PASSWORD_CHANGED','USER_PASSWORD_CHANGE_FAILED',
  'PROJECT_CREATED','PROJECT_UPDATED','PROJECT_ARCHIVED','PROJECT_UNARCHIVED',
  'PROJECT_DELETED','PROJECT_MEMBER_ADDED','PROJECT_MEMBER_ROLE_CHANGED',
  'PROJECT_MEMBER_REMOVED','TASK_DELETED',
  'MFA_ENABLED','MFA_DISABLED','MFA_RECOVERY_CODES_REGENERATED',
];
function actionLabel(action) {
  return ACTION_KEYS.includes(action) ? t('admin.action.'+action) : action;
}
// Severity colouring. REFRESH_TOKEN_REUSE is danger, not warn: it is the
// signal that a refresh token was replayed, which revokes every session of that
// account — the one entry here that means "someone may be attacking this
// account" rather than "someone did something drastic".
const ACTION_SEV = {
  LOGIN_FAILED:'warn', USER_DISABLED:'warn', USER_PASSWORD_CHANGE_FAILED:'warn',
  MFA_DISABLED:'warn',
  USER_DELETED:'danger', PROJECT_DELETED:'danger', REFRESH_TOKEN_REUSE:'danger',
};

function _adminRelTime(iso) {
  if (!iso) return null;
  const d = Date.now() - new Date(iso).getTime();
  if (d < 60000)     return t('dates.justNow');
  if (d < 3600000)   return t('dates.minutesAgo',{count:Math.floor(d/60000)});
  if (d < 86400000)  return t('dates.hoursAgo',{count:Math.floor(d/3600000)});
  if (d < 2592000000)return t('dates.daysAgo',{count:Math.floor(d/86400000)});
  return new Date(iso).toLocaleDateString(getLocale());
}

// ── User Management ───────────────────────────────────────

async function renderAdminPanel() {
  if (!AppPerms.canManageAccounts()) {
    el('#content').innerHTML = `<div class="empty"><div class="empty-title">${t('errors.accessDenied')}</div><p>${t('errors.superAdminRequired')}</p></div>`;
    return;
  }
  S.project = null; S.view = 'admin';
  renderSidebar(); renderTopbar();

  const c = el('#content');
  c.innerHTML = `<div class="page-loader">Loading users...</div>`;
  let users;
  try { users = await api.users.list(); }
  catch(e) { c.innerHTML = `<div class="empty"><div class="empty-title">${t('errors.loadUsersFailed')}</div><p>${esc(apiErrorMessage(e))}</p></div>`; return; }

  users.forEach(u => { _adminUserMap[u.id] = u; });
  _renderUserTable(c, users);
}

function _renderUserTable(c, users) {
  const { search, roleFilter, statusFilter } = AdminState;
  const q = search.toLowerCase();
  const filtered = users.filter(u => {
    if (q && !u.displayName.toLowerCase().includes(q) && !u.email.toLowerCase().includes(q)) return false;
    if (roleFilter   && u.globalRole !== roleFilter)  return false;
    if (statusFilter && u.status     !== statusFilter) return false;
    return true;
  });

  const total    = users.length;
  const active   = users.filter(u => u.status === 'active').length;
  const disabled = users.filter(u => u.status === 'disabled').length;
  const admins   = users.filter(u => u.globalRole === 'ADMIN').length;
  const supers   = users.filter(u => u.globalRole === 'SUPER_ADMIN').length;

  c.innerHTML = `
    <div class="admin-panel">
      <div class="admin-header">
        <h2 class="admin-title">${t('nav.userManagement')}</h2>
        <button class="btn btn-primary" data-act="showCreateUserModal">${t('admin.newUser')}</button>
      </div>

      <div class="admin-stats">
        <div class="admin-stat">
          <span class="admin-stat-value">${LIMITS.maxUsers > 0 ? `${total} / ${LIMITS.maxUsers}` : total}</span>
          <span class="admin-stat-label">${t('admin.total')}</span>
        </div>
        <div class="admin-stat">
          <span class="admin-stat-value">${active}</span>
          <span class="admin-stat-label">${t('admin.active')}</span>
        </div>
        <div class="admin-stat ${disabled > 0 ? 'admin-stat--warn' : 'admin-stat--muted'}">
          <span class="admin-stat-value">${disabled}</span>
          <span class="admin-stat-label">${t('admin.disabled')}</span>
        </div>
        <div class="admin-stat">
          <span class="admin-stat-value">${supers}</span>
          <span class="admin-stat-label">${t('admin.superAdmin')}</span>
        </div>
        <div class="admin-stat">
          <span class="admin-stat-value">${admins}</span>
          <span class="admin-stat-label">${t('admin.admin')}</span>
        </div>
      </div>

      <div class="admin-filters">
        <input class="form-input admin-search" id="admin-search"
               placeholder="${t('admin.searchPlaceholder')}"
               value="${esc(search)}"
               data-input="adminSearch">
        <select class="form-select-sm" data-change="roleFilter">
          <option value="">${t('admin.allRoles')}</option>
          ${['SUPER_ADMIN','ADMIN','USER','GUEST'].map(r=>`
            <option value="${r}" ${roleFilter===r?'selected':''}>${r}</option>`).join('')}
        </select>
        <select class="form-select-sm" data-change="statusFilter">
          <option value="">${t('admin.allStatuses')}</option>
          ${['active','disabled','invited'].map(s=>`
            <option value="${s}" ${statusFilter===s?'selected':''}>${s}</option>`).join('')}
        </select>
        <span class="admin-filter-count" id="admin-filter-count">${filterCountLabel(filtered.length, total)}</span>
      </div>

      <div class="admin-user-list" id="admin-user-list">${_renderUserListItems(filtered)}</div>
    </div>`;
}

function _renderUserListItems(filtered) {
  if (filtered.length === 0) return `<div class="empty empty--sm"><p>${t('admin.noUsersMatch')}</p></div>`;
  return filtered.map(u => {
    const roleClass = ROLE_AVATAR_CLASS[u.globalRole] || '';
    return `
    <div class="admin-user-row${u.status === 'disabled' ? ' admin-user-row--disabled' : ''}">
      <div class="admin-user-avatar${roleClass}${u.avatarUpdatedAt ? ' has-avatar' : ''}">${esc(initials(u.displayName))}${u.avatarUpdatedAt ? `<img class="avatar-img" alt="" aria-hidden="true" data-avatar-user="${esc(u.id)}" data-avatar-v="${esc(u.avatarUpdatedAt)}">` : ''}</div>
      <div class="admin-user-info">
        <div class="admin-user-name">${esc(u.displayName)}</div>
        <div class="admin-user-email">${esc(u.email)}</div>
      </div>
      <div class="admin-user-meta">
        <span class="badge ${ROLE_BADGE[u.globalRole]||'badge-planned'}">${esc(u.globalRole)}</span>
        <span class="badge ${STATUS_BADGE[u.status]||'badge-planned'}">${esc(u.status)}</span>
      </div>
      <div class="admin-user-login">
        ${u.lastLoginAt
          ? `<span class="admin-login-time">${_adminRelTime(u.lastLoginAt)}</span>
             <span class="admin-login-abs">${new Date(u.lastLoginAt).toLocaleDateString(getLocale())}</span>`
          : `<span class="admin-login-never">${t('admin.never')}</span>`}
      </div>
      <div class="admin-user-actions">
        ${u.globalRole !== 'SUPER_ADMIN'
          ? `<button class="btn btn-secondary btn-sm" data-act="showEditUserModal" data-a0="${esc(u.id)}">${t('form.edit')}</button>
             ${u.status !== 'disabled'
               ? `<button class="btn btn-danger btn-sm" data-act="disableUser" data-a0="${esc(u.id)}">${t('admin.disableAction')}</button>`
               : `<button class="btn btn-success btn-sm" data-act="enableUser" data-a0="${esc(u.id)}">${t('admin.enableAction')}</button>`}
             <button class="btn btn-danger btn-sm" data-act="deleteUser" data-a0="${esc(u.id)}">${t('admin.deleteAction')}</button>`
          : `<span class="admin-protected">${t('admin.protected')}</span>`}
      </div>
    </div>`;
  }).join('');
}

// Re-filter using cached data and patch only the list + count — keeps focus/caret
// in #admin-search and the filter <select>s intact while the user types.
function _refilterUsers() {
  const cached = Object.values(_adminUserMap);
  if (!cached.length) return;
  const { search, roleFilter, statusFilter } = AdminState;
  const q = search.toLowerCase();
  const filtered = cached.filter(u => {
    if (q && !u.displayName.toLowerCase().includes(q) && !u.email.toLowerCase().includes(q)) return false;
    if (roleFilter   && u.globalRole !== roleFilter)  return false;
    if (statusFilter && u.status     !== statusFilter) return false;
    return true;
  });
  const list = el('#admin-user-list');
  const count = el('#admin-filter-count');
  if (list)  list.innerHTML = _renderUserListItems(filtered);
  if (count) count.textContent = filterCountLabel(filtered.length, cached.length);
}

function showCreateUserModal() {
  showModal(t('admin.createUser'), `
    <div class="form-group">
      <label class="form-label">${t('form.email')}</label>
      <input class="form-input" id="nu-email" type="email" placeholder="${t('form.emailPlaceholder')}" autofocus>
    </div>
    <div class="form-group">
      <label class="form-label">${t('admin.displayName')}</label>
      <input class="form-input" id="nu-name" placeholder="${t('admin.fullNamePlaceholder')}">
    </div>
    <div class="form-group">
      <label class="form-label">${t('form.password')}</label>
      <input class="form-input" id="nu-password" type="password" placeholder="${t('admin.minPasswordPlaceholder')}">
      <div class="form-hint">${t('admin.passwordHint')}</div>
    </div>
    <div class="form-group">
      <label class="form-label">${t('admin.role')}</label>
      <select class="form-select" id="nu-role">
        <option value="USER">${t('admin.roleUserDesc')}</option>
        <option value="ADMIN">${t('admin.roleAdminDesc')}</option>
        <option value="GUEST">${t('admin.roleGuestDesc')}</option>
      </select>
    </div>`,
    async () => {
      const email = el('#nu-email')?.value?.trim();
      const name  = el('#nu-name')?.value?.trim();
      const pass  = el('#nu-password')?.value;
      const role  = el('#nu-role')?.value;
      if (!email) throw new Error(t('validation.emailRequired'));
      if (!name)  throw new Error(t('validation.displayNameRequired'));
      if (!pass || pass.length < 12) throw new Error(t('validation.passwordTooShort'));
      const u = await api.users.create({ email, displayName: name, password: pass, globalRole: role });
      _adminUserMap[u.id] = u;
      toast(t('admin.userCreated',{name}), 'success');
      await renderAdminPanel();
    }, t('admin.createUser'));
}

// id-only argument avoids embedding user data in HTML attribute strings.
function showEditUserModal(id) {
  const u = _adminUserMap[id];
  if (!u) return;
  showModal(t('admin.editUserTitle',{name:esc(u.displayName)}), `
    <div class="form-group">
      <label class="form-label">${t('form.email')}</label>
      <input class="form-input" id="eu-email" type="email" value="${esc(u.email)}">
    </div>
    <div class="form-group">
      <label class="form-label">${t('admin.displayName')}</label>
      <input class="form-input" id="eu-name" value="${esc(u.displayName)}" autofocus>
    </div>
    <div class="form-group">
      <label class="form-label">${t('admin.role')}</label>
      <select class="form-select" id="eu-role">
        <option value="USER"  ${u.globalRole==='USER' ?'selected':''}>${t('admin.roleUser')}</option>
        <option value="ADMIN" ${u.globalRole==='ADMIN'?'selected':''}>${t('admin.roleAdmin')}</option>
        <option value="GUEST" ${u.globalRole==='GUEST'?'selected':''}>${t('admin.roleGuest')}</option>
      </select>
    </div>
    <div class="form-group">
      <label class="form-label">${t('admin.status')}</label>
      <select class="form-select" id="eu-status">
        <option value="active"   ${u.status==='active'  ?'selected':''}>${t('admin.statusActive')}</option>
        <option value="disabled" ${u.status==='disabled'?'selected':''}>${t('admin.statusDisabledHint')}</option>
        <option value="invited"  ${u.status==='invited' ?'selected':''}>${t('admin.statusInvited')}</option>
      </select>
    </div>`,
    async () => {
      const email  = el('#eu-email')?.value?.trim();
      const name   = el('#eu-name')?.value?.trim();
      const role   = el('#eu-role')?.value;
      const status = el('#eu-status')?.value;
      if (!email) throw new Error(t('validation.emailRequired'));
      if (!name) throw new Error(t('validation.displayNameRequired'));
      const updated = await api.users.update(id, { email, displayName: name, globalRole: role, status });
      _adminUserMap[id] = updated;
      toast(t('admin.userUpdated',{name}), 'success');
      await renderAdminPanel();
    }, t('admin.saveChanges'));
}

function disableUser(id) {
  const u = _adminUserMap[id];
  if (!u) return;
  confirmDelete(t('admin.disableAccountTitle'),
    t('admin.disableAccountConfirm',{name:esc(u.displayName), email:esc(u.email)}),
    async () => {
      await api.users.disable(id);
      if (_adminUserMap[id]) _adminUserMap[id] = { ..._adminUserMap[id], status:'disabled' };
      toast(t('admin.userDisabled',{name:u.displayName}), 'success');
      await renderAdminPanel();
    }, null, t('admin.disableAction'));
}

async function enableUser(id) {
  const u = _adminUserMap[id];
  if (!u) return;
  try {
    const updated = await api.users.update(id, { displayName: u.displayName, globalRole: u.globalRole, status: 'active' });
    _adminUserMap[id] = updated;
    toast(t('admin.userReenabled',{name:u.displayName}), 'success');
    await renderAdminPanel();
  } catch(e) { toast(apiErrorMessage(e), 'error'); }
}

// Deletion is GDPR erasure (anonymize-in-place) on the server, not a reversible
// disable — so it routes through the destructive-confirm dialog. Super Admin only,
// which the whole panel already is.
function deleteUser(id) {
  const u = _adminUserMap[id];
  if (!u) return;
  confirmDelete(t('admin.deleteAccountTitle'),
    t('admin.deleteAccountConfirm',{name:esc(u.displayName), email:esc(u.email)}),
    async () => {
      try {
        await api.users.del(id);
        delete _adminUserMap[id];
        toast(t('admin.userDeleted',{name:u.displayName}), 'success');
        await renderAdminPanel();
      } catch(e) { toast(apiErrorMessage(e), 'error'); }
    }, null, t('admin.deleteAction'));
}

// ── Audit Logs ────────────────────────────────────────────

async function renderAuditLogs() {
  if (!AppPerms.canViewAuditLogs()) {
    el('#content').innerHTML = `<div class="empty"><div class="empty-title">${t('errors.accessDenied')}</div></div>`;
    return;
  }
  S.project = null; S.view = 'audit-logs';
  renderSidebar(); renderTopbar();

  const c = el('#content');
  c.innerHTML = `<div class="page-loader">${t('admin.loadingAuditLogs')}</div>`;

  let result, users;
  try {
    const params = { page: AdminState.auditPage, size: 50 };
    if (AdminState.auditAction) params.action = AdminState.auditAction;
    [result, users] = await Promise.all([
      api.auditLogs(params),
      Object.keys(_adminUserMap).length > 0
        ? Promise.resolve(Object.values(_adminUserMap))
        : api.users.list(),
    ]);
  } catch(e) {
    c.innerHTML = `<div class="empty"><div class="empty-title">${t('errors.loadFailed')}</div><p>${esc(apiErrorMessage(e))}</p></div>`;
    return;
  }

  users.forEach(u => { _adminUserMap[u.id] = u; });
  const logs = result.logs || [];
  const totalPages = Math.ceil(result.total / 50);

  function actorLabel(id) {
    if (!id) return `<em class="text-muted">${t('admin.systemActor')}</em>`;
    const u = _adminUserMap[id];
    return u
      ? `<span title="${esc(u.email)}" class="font-semibold">${esc(u.displayName)}</span>`
      : `<code class="text-xs">${esc(id.slice(0,8))}...</code>`;
  }

  function metaChips(json) {
    try {
      const obj = JSON.parse(json || '{}');
      return Object.entries(obj).map(([k,v]) =>
        `<span class="audit-meta-chip"><span class="audit-meta-key">${esc(k)}</span> ${esc(String(v))}</span>`
      ).join('');
    } catch { return ''; }
  }

  const actionOpts = ACTION_KEYS.map(a => [a, t('admin.action.'+a)]).map(([a,l]) =>
    `<option value="${a}" ${AdminState.auditAction===a?'selected':''}>${l}</option>`
  ).join('');

  c.innerHTML = `
    <div class="admin-panel">
      <div class="admin-header">
        <h2 class="admin-title">${t('nav.auditLogs')}</h2>
        <span class="admin-entries-count">${t('admin.entriesCount',{count:result.total.toLocaleString(getLocale())})}</span>
      </div>

      <div class="admin-filters">
        <select class="form-select-sm" data-change="auditActionFilter">
          <option value="">${t('admin.allActions')}</option>
          ${actionOpts}
        </select>
        ${totalPages > 1 ? `
          <div class="admin-pagination">
            <button class="btn btn-ghost btn-sm"
              ${AdminState.auditPage === 0 ? 'disabled' : ''}
              data-act="auditPrev">${t('form.prev')}</button>
            <span>${t('admin.pageOf',{page:AdminState.auditPage + 1,total:totalPages})}</span>
            <button class="btn btn-ghost btn-sm"
              ${AdminState.auditPage >= totalPages - 1 ? 'disabled' : ''}
              data-act="auditNext">${t('form.next')}</button>
          </div>` : ''}
      </div>

      ${logs.length === 0
        ? `<div class="empty"><p>${t('admin.noAuditEntries',{forAction:AdminState.auditAction?t('admin.forActionType'):''})}</p></div>`
        : `<div class="audit-log-list">
            ${logs.map(l => {
              const sev = ACTION_SEV[l.action] ? ` audit-sev--${ACTION_SEV[l.action]}` : '';
              return `
              <div class="audit-log-row${sev}">
                <div class="audit-time">
                  <span class="audit-rel">${_adminRelTime(l.createdAt)}</span>
                  <span class="audit-abs">${new Date(l.createdAt).toLocaleString(getLocale())}</span>
                </div>
                <div class="audit-actor">${actorLabel(l.actorUserId)}</div>
                <div class="audit-action">
                  <span class="audit-action-label">${actionLabel(l.action) || esc(l.action)}</span>
                  ${l.targetType ? `<span class="audit-target-badge">${esc(l.targetType)}</span>` : ''}
                </div>
                <div class="audit-meta">${metaChips(l.metadata)}</div>
                <div class="audit-ip">${esc(l.ipAddress || '')}</div>
              </div>`;
            }).join('')}
          </div>`}
    </div>`;
}

// ═══════════════════════════════════════════════════════════
// ACTION REGISTRY — maps data-act / data-change / data-input /
// data-keydown / data-submit names to their handler functions.

// ── Delegation registration: this file's handlers ───────────────────────────
// (see js/README.md "Delegation registration".)
registerActions([showCreateUserModal], _A0);
registerActions([disableUser, deleteUser, enableUser, showEditUserModal], _A1);
registerActions({
  auditPrev: () => { AdminState.auditPage--; renderAuditLogs(); },
  auditNext: () => { AdminState.auditPage++; renderAuditLogs(); },
});
registerChanges({
  auditActionFilter: el => { AdminState.auditAction = el.value; AdminState.auditPage = 0; renderAuditLogs(); },
  roleFilter:        el => { AdminState.roleFilter = el.value; _refilterUsers(); },
  statusFilter:      el => { AdminState.statusFilter = el.value; _refilterUsers(); },
});
registerInputs({
  adminSearch: el => { AdminState.search = el.value; _refilterUsers(); },
});

export { AdminState, renderAdminPanel, renderAuditLogs };
