import { api } from './api.js';
import { S } from './state.js';

// Octbase SPA — split from the former single app.js (and later from api.js,
// which had grown to conflate auth, the HTTP client, the REST surface, the
// router, and permission helpers). One ES module among many, bundled by Vite
// (37b stage 2): its top-level declarations are file-private and its public
// surface is the `export { … }` block at the bottom. Imports carry the
// dependencies — there is no load order to keep in step (js/README.md).

// ═══════════════════════════════════════════════════════════
// PERMISSION HELPERS (frontend UX only — backend is authoritative)
// ═══════════════════════════════════════════════════════════

// The permissions that survive an archived project. Everything else is a write,
// and every write route answers 409 PROJECT_ARCHIVED while the freeze holds.
// Archiving, unarchiving and deleting a project are deliberately NOT gated on
// this map — they are owner-only routes guarded by memberGuard rather than
// writerGuard (unarchive has to work in exactly the state writerGuard blocks),
// and the topbar menu renders them off the project's status directly.
const READ_PERMISSIONS = new Set(['project.view', 'task.view']);
const AppPerms = {
  canManageAccounts()  { return S.user?.globalRole === 'SUPER_ADMIN'; },
  canCreateProject()   { return S.user?.globalRole === 'SUPER_ADMIN' || S.user?.globalRole === 'ADMIN'; },
  canViewAuditLogs()   { return S.user?.globalRole === 'SUPER_ADMIN'; },

  _projectRole(project) {
    if (!project || !S.user) return '';
    if (S.user.globalRole === 'SUPER_ADMIN') return 'PROJECT_ADMIN';
    const m = (S.user.projectMemberships || []).find(m => m.projectId === project.id);
    return m?.role || '';
  },

  // Fetches and caches the permission map for a project from
  // GET /projects/{projectId}/permissions. Backend remains authoritative;
  // this cache only drives UI show/hide decisions.
  async loadPermissions(projectId) {
    try {
      const res = await api.permissions.get(projectId);
      S.permissionsByProject[projectId] = res;
      return res;
    } catch {
      delete S.permissionsByProject[projectId];
      return null;
    }
  },

  // isArchivedProject reports the frozen state. Reads keep working; every write
  // route answers 409 PROJECT_ARCHIVED (writerGuard / requirePermission in
  // internal/workmanagement/handler.go).
  isArchivedProject(project) {
    return (project || S.project)?.status === 'ARCHIVED';
  },

  // Returns true if the cached permissions for `project` grant `permission`.
  // Falls back to the pre-permissions-endpoint role heuristics if the
  // permissions haven't loaded yet, so the UI doesn't flash/break.
  can(permission, project) {
    project = project || S.project;
    if (!project) return false;
    // An archived project is frozen server-side, so no write permission holds
    // however senior the member is. Answering that here rather than in each
    // view is what keeps the UI honest: archiving had no UI at all until
    // 1.1.2, so the state was only reachable by driving the API and every
    // write affordance stayed on screen — clicking one produced a 409 toast,
    // which is truthful but reads as a broken app rather than a deliberate
    // state. Every affordance already asks this function, so they all follow.
    //
    // The permission map from GET /projects/{id}/permissions describes the
    // member's ROLE and knows nothing about the freeze, which is why this sits
    // above the cache lookup rather than inside the fallback.
    if (this.isArchivedProject(project) && !READ_PERMISSIONS.has(permission)) return false;
    const cached = S.permissionsByProject[project.id];
    if (cached) return !!cached.permissions[permission];

    const r = this._projectRole(project);
    switch (permission) {
      case 'project.view':
      case 'task.view':
        return r !== '';
      case 'project.remove_users':
      case 'project.invite_users':
      case 'project.change_roles':
        return r === 'PROJECT_OWNER' || r === 'PROJECT_ADMIN';
      case 'task.delete':
      case 'task.create':
      case 'task.update':
      case 'task.assign':
      case 'task.comment':
        return r === 'PROJECT_OWNER' || r === 'PROJECT_ADMIN' || r === 'PROJECT_MEMBER';
      default:
        return r === 'PROJECT_OWNER' || r === 'PROJECT_ADMIN';
    }
  },

  canManageProjectMembers(project) {
    return this.can('project.remove_users', project);
  },

  canEditTask(project) {
    return this.can('task.update', project);
  },

  isReadOnlyProject(project) {
    return !this.can('task.create', project);
  },
};
window.AppPerms = AppPerms;

export { AppPerms };
