// @ts-check
import { Auth } from './auth.js';
import { API_BASE, BASE_PATH } from './env.js';
import { http, qs } from './http.js';

// Octbase SPA — split from the former single app.js (and later split further:
// auth, the HTTP client, the router, and permission helpers now live in
// auth.js, http.js, router.js and permissions.js respectively — this file is
// the REST API surface only). An ES module like the rest of js/ since 37b
// stage 2 (js/README.md).
//
// The reads below carry `@returns` annotations pointing at the types generated
// from octbase-api/api/openapi.yaml (37b stage 7). `http` itself resolves to
// `any`, so these are the point where an untyped response becomes a typed one —
// which makes them assertions about the contract rather than proofs of it. That
// is the right place for the assertion: it is checked against the spec by
// construction (a renamed field fails to compile at every reader), and the spec
// is checked against the router by the backend's apicontract parity test. Only
// the reads whose shape the views actually destructure are annotated; add one
// when a view starts needing it, not pre-emptively.

// ═══════════════════════════════════════════════════════════
// API — all paths prefixed with /api/v1
// ═══════════════════════════════════════════════════════════
const V = BASE_PATH;
const api = {
  auth: {
    login: (email, password) => http.post(`${V}/auth/login`, {email,password}),
    verifyMfa: (challengeToken, code) => http.post(`${V}/auth/mfa/verify`, {challengeToken, code}),
    me:    ()                 => http.get(`${V}/auth/me`),
    logout:()                 => Auth.logout(),
    // 204 on success. Every OTHER session is revoked server-side and the
    // caller's refresh cookie is re-issued, so this device stays signed in.
    changePassword: (currentPassword, newPassword) =>
      http.post(`${V}/auth/change-password`, {currentPassword, newPassword}),
  },
  // Public, non-secret runtime config (optional-feature flags) the SPA reads once
  // at boot. Backend is the source of truth (env-driven); see FEATURES in config.js.
  config: ()                  => http.get(`${V}/config`),
  projects: {
    /** @returns {Promise<import('../types/api').Project[]>} */
    list:   ()      => http.get(`${V}/projects`),
    /** @returns {Promise<import('../types/api').Project>} */
    get:    (id)    => http.get(`${V}/projects/${id}`),
    create: (d)     => http.post(`${V}/projects`, d),
    update: (id,d)  => http.patch(`${V}/projects/${id}`, d),
    archive:(id)    => http.post(`${V}/projects/${id}/archive`, {}),
    unarchive:(id)  => http.post(`${V}/projects/${id}/unarchive`, {}),
    del:    (id)    => http.del(`${V}/projects/${id}`),
  },
  tasks: {
    /** @returns {Promise<import('../types/api').Task[]>} */
    list:    (pid,p={}) => http.get(`${V}/projects/${pid}/tasks${qs(p)}`),
    /**
     * Every task in the project, not just the first page.
     *
     * The API caps a page at 200 rows (shared.ParsePagination) and sorts by
     * created_at DESC, so a single `size:200` read of a larger project drops
     * its OLDEST tasks — which are exactly the epics and stories the newer ones
     * hang from. The mindmap did not truncate visibly when that happened: the
     * orphaned children fell into its "stories without epic" / "unlinked tasks"
     * branches, so missing data read as a badly-parented backlog.
     *
     * Pages until a short page arrives (fewer rows than asked for), which needs
     * no X-Total-Count and costs exactly one request for the projects under 200
     * tasks that are the common case. MAX_PAGES bounds it so a server that kept
     * answering full pages could not spin here forever.
     * @returns {Promise<import('../types/api').Task[]>}
     */
    listAll: async (pid, p = {}) => {
      const MAX_PAGES = 25;
      const size = 200;
      const out = [];
      for (let page = 0; page < MAX_PAGES; page++) {
        const batch = await http.get(`${V}/projects/${pid}/tasks${qs({ ...p, page, size })}`);
        if (!Array.isArray(batch) || batch.length === 0) break;
        out.push(...batch);
        if (batch.length < size) break;
      }
      return out;
    },
    /** @returns {Promise<import('../types/api').Task>} */
    get:     (id)       => http.get(`${V}/tasks/${id}`),
    create:  (pid,d)    => http.post(`${V}/projects/${pid}/tasks`, d),
    update:  (id,d)     => http.patch(`${V}/tasks/${id}`, d),
    // version (optional) is the task version the change is based on; a stale
    // version is rejected with 409 VERSION_CONFLICT instead of overwriting a
    // concurrent editor (undefined is dropped by JSON.stringify).
    status:  (id,s,version)     => http.post(`${V}/tasks/${id}/status`, {status:s, version}),
    priority:(id,p,version)     => http.post(`${V}/tasks/${id}/priority`, {priority:p, version}),
    setPin:  (id,pinned)=> http.post(`${V}/tasks/${id}/pin`, {pinned}),
    assign:  (id,d)     => http.post(`${V}/tasks/${id}/assign`, d),
      archive: (id)       => http.post(`${V}/tasks/${id}/archive`, {}),
    reopen:  (id)       => http.post(`${V}/tasks/${id}/reopen`, {}),
    del:     (id)       => http.del(`${V}/tasks/${id}`),
    search:  (pid,q)    => http.get(`${V}/projects/${pid}/search/tasks?q=${encodeURIComponent(q)}`),
    bulk:    (pid,d)    => http.post(`${V}/projects/${pid}/tasks/bulk`, d),
    activity:(id,p={})  => http.get(`${V}/tasks/${id}/activity${qs(p)}`),
  },
  comments: {
    list:   (tid)       => http.get(`${V}/tasks/${tid}/comments`),
    add:    (tid,t,parentId) => http.post(`${V}/tasks/${tid}/comments`, {text:t, parentId: parentId || null}),
    update: (tid,id,t,version)  => http.patch(`${V}/tasks/${tid}/comments/${id}`, {text:t, version}),
    del:    (tid,id)    => http.del(`${V}/tasks/${tid}/comments/${id}`),
  },
  links: {
    list: (tid)    => http.get(`${V}/tasks/${tid}/links`),
    add:  (tid,d)  => http.post(`${V}/tasks/${tid}/links`, d),
    del:  (tid,id) => http.del(`${V}/tasks/${tid}/links/${id}`),
  },
  priorities: {
    // The project's custom priorities; the built-in set is static (PRIORITIES).
    list: (pid)  => http.get(`${V}/projects/${pid}/task-priorities`),
    add:  (pid,d)=> http.post(`${V}/projects/${pid}/task-priorities`, d),
    del:  (id)   => http.del(`${V}/task-priorities/${id}`),
  },
  relations: {
    // All relations of a project in one call (mindmap hierarchy).
    listForProject: (pid) => http.get(`${V}/projects/${pid}/relations`),
    list: (tid)    => http.get(`${V}/tasks/${tid}/relations`),
    add:  (tid,d)  => http.post(`${V}/tasks/${tid}/relations`, d),
    del:  (tid,id) => http.del(`${V}/tasks/${tid}/relations/${id}`),
  },
  attachments: {
    list: (tid)    => http.get(`${V}/tasks/${tid}/attachments`),
    add:  (tid,d)  => http.post(`${V}/tasks/${tid}/attachments`, d),
    upload: (tid, file) => http.upload(`${V}/tasks/${tid}/attachments/upload`, file),
    del:  (tid,id) => http.del(`${V}/tasks/${tid}/attachments/${id}`),
    // contentPath returns the relative path to an uploaded attachment's bytes.
    // Used for download links and (for images) inline rendering. The server
    // sanitizer only permits this exact relative-path shape as an <img src>.
    contentPath: (tid, id) => `${V}/tasks/${tid}/attachments/${id}/content`,
    contentUrl:  (tid, id) => `${API_BASE}${V}/tasks/${tid}/attachments/${id}/content`,
    // content fetches the attachment bytes as a Blob with auth attached, so the
    // result can be turned into an object URL for inline display or download.
    content:     (tid, id) => http.getBlob(`${V}/tasks/${tid}/attachments/${id}/content`),
  },
  branches: {
    list:   (tid)    => http.get(`${V}/tasks/${tid}/branches`),
    create: (tid,d)  => http.post(`${V}/tasks/${tid}/branches`, d),
    del:    (tid,id) => http.del(`${V}/tasks/${tid}/branches/${id}`),
    createPullRequest: (tid,bid,d) => http.post(`${V}/tasks/${tid}/branches/${bid}/pull-request`, d),
  },
  boards: {
    list:       (pid)    => http.get(`${V}/projects/${pid}/boards`),
    getDefault: (pid)    => http.get(`${V}/projects/${pid}/boards/default`),
    get:        (bid)    => http.get(`${V}/boards/${bid}`),
    create:     (pid,d)  => http.post(`${V}/projects/${pid}/boards`, d),
    update:     (bid,d)  => http.patch(`${V}/boards/${bid}`, d),
    addColumn:  (bid,d)  => http.post(`${V}/boards/${bid}/columns`, d),
    updateColumn:(bid,cid,d)=> http.patch(`${V}/boards/${bid}/columns/${cid}`, d),
    deleteColumn:(bid,cid)=> http.del(`${V}/boards/${bid}/columns/${cid}`),
    move:       (bid,d)  => http.post(`${V}/boards/${bid}/move-task`, d),
    remove:     (bid,tid)=> http.post(`${V}/boards/${bid}/remove-task`, {taskId:tid}),
    listExternalColumns: (bid)   => http.get(`${V}/boards/${bid}/external-columns`),
    addExternalColumn:   (bid,d)  => http.post(`${V}/boards/${bid}/external-columns`, d),
    delExternalColumn:   (bid,id) => http.del(`${V}/boards/${bid}/external-columns/${id}`),
  },
  backlog: { get: (pid) => http.get(`${V}/projects/${pid}/backlog`) },
  releases: {
    list:   (pid)   => http.get(`${V}/projects/${pid}/releases`),
    create: (pid,d) => http.post(`${V}/projects/${pid}/releases`, d),
    get:    (id)    => http.get(`${V}/releases/${id}`),
    update: (id,d)  => http.patch(`${V}/releases/${id}`, d),
    close:  (id)    => http.post(`${V}/releases/${id}/close`, {}),
    reopen: (id)    => http.post(`${V}/releases/${id}/reopen`, {}),
    del:    (id)    => http.del(`${V}/releases/${id}`),
  },
  sprints: {
    list:     (pid)   => http.get(`${V}/projects/${pid}/sprints`),
    create:   (pid,d) => http.post(`${V}/projects/${pid}/sprints`, d),
    get:      (id)    => http.get(`${V}/sprints/${id}`),
    update:   (id,d)  => http.patch(`${V}/sprints/${id}`, d),
    start:    (id)    => http.post(`${V}/sprints/${id}/start`, {}),
    complete: (id)    => http.post(`${V}/sprints/${id}/complete`, {}),
    del:      (id)    => http.del(`${V}/sprints/${id}`),
    // unit: omitted (or 'tasks') counts tickets; 'points'/'hours' burn down
    // effort and must match the project's active estimation unit — the API
    // answers 422 ESTIMATION_UNIT_INACTIVE rather than silently counting.
    burndown: (id, unit) => http.get(`${V}/sprints/${id}/burndown` + (unit && unit !== 'tasks' ? `?unit=${encodeURIComponent(unit)}` : '')),
  },
  reports: {
    velocity:   (pid) => http.get(`${V}/projects/${pid}/reports/velocity`),
    statistics: (pid) => http.get(`${V}/projects/${pid}/reports/statistics`),
  },
  pages: {
    list:     (pid)    => http.get(`${V}/projects/${pid}/pages`),
    get:      (id)     => http.get(`${V}/pages/${id}`),
    create:   (pid,d)  => http.post(`${V}/projects/${pid}/pages`, d),
    update:   (id,d)   => http.patch(`${V}/pages/${id}`, d),
    publish:  (id,msg) => http.post(`${V}/pages/${id}/publish`, {message:msg}),
    archive:  (id)     => http.post(`${V}/pages/${id}/archive`, {}),
    del:      (id)     => http.del(`${V}/pages/${id}`),
    revisions:(id)     => http.get(`${V}/pages/${id}/revisions`),
    preview:  (id,c)   => http.post(`${V}/pages/${id}/render-preview`, {content:c}),
    search:   (pid,q)  => http.get(`${V}/projects/${pid}/search/pages?q=${encodeURIComponent(q)}`),
  },
  // Both feeds are newest-first and paged (50 per page server-side). Callers
  // pass `page` and read a short page as "that was the last one", the same
  // no-X-Total-Count contract tasks.listAll relies on.
  activity: {
    project:(pid, p={}) => http.get(`${V}/projects/${pid}/activity${qs(p)}`),
    task:   (tid, p={}) => http.get(`${V}/tasks/${tid}/activity${qs(p)}`),
  },
  members: {
    list:   (pid)    => http.get(`${V}/projects/${pid}/members`),
    // Candidates for a task's assignee/reviewer: the project's members plus the
    // global admins, who reach the project without a membership row.
    assignable:(pid) => http.get(`${V}/projects/${pid}/assignable-users`),
    memberships:(pid)=> http.get(`${V}/projects/${pid}/memberships`),
    add:    (pid,d)  => http.post(`${V}/projects/${pid}/memberships`, d),
    updateRole:(pid,uid,role) => http.patch(`${V}/projects/${pid}/memberships/${uid}`, {role}),
    remove: (pid,uid)=> http.del(`${V}/projects/${pid}/memberships/${uid}`),
  },
  permissions: {
    get: (pid) => http.get(`${V}/projects/${pid}/permissions`),
  },
  invitations: {
    create: (d) => http.post(`${V}/admin/invitations`, d),
  },
  repos: {
    list:   (pid)   => http.get(`${V}/projects/${pid}/repository-connections`),
    create: (pid,d) => http.post(`${V}/projects/${pid}/repository-connections`, d),
    update: (id,d)  => http.patch(`${V}/repository-connections/${id}`, d),
    del:    (id)    => http.del(`${V}/repository-connections/${id}`),
    oauthAuthorize: (id) => http.get(`${V}/repository-connections/${id}/oauth/authorize`),
    oauthRefresh:   (id) => http.post(`${V}/repository-connections/${id}/oauth/refresh`, {}),
  },
  notifications: {
    list:    (p={})  => http.get(`${V}/users/me/notifications${qs(p)}`),
    readAll: ()      => http.post(`${V}/users/me/notifications/read-all`, {}),
    markRead:(id)    => http.patch(`${V}/users/me/notifications/${id}`, {isRead:true}),
    getPreferences:    ()    => http.get(`${V}/users/me/notification-preferences`),
    updatePreference:  (d)   => http.patch(`${V}/users/me/notification-preferences`, d),
  },
  // Personal dashboard: language/theme (internal/dashboard).
  preferences: {
    get:    ()   => http.get(`${V}/users/me/preferences`),
    update: (d)  => http.patch(`${V}/users/me/preferences`, d),
  },
  // MFA enrollment/management (internal/security/mfa) — a separate backend
  // module from preferences above by design (see docs/architecture.md).
  mfa: {
    enroll:  (password) => http.post(`${V}/users/me/mfa/enroll`, {password}),
    confirm: (code)  => http.post(`${V}/users/me/mfa/confirm`, {code}),
    disable: (d)     => http.post(`${V}/users/me/mfa/disable`, d),
    regenerateRecoveryCodes: (d) => http.post(`${V}/users/me/mfa/recovery-codes/regenerate`, d),
  },
  search: (q, projectId) => {
    const p = {q};
    if(projectId) p.projectId = projectId;
    return http.get(`${V}/search${qs(p)}`);
  },
  dashboard: () => http.get(`${V}/users/me/dashboard`),
  // User management — the CRUD ops are Super Admin only; the avatar ops below
  // are open to every user (upload/delete act on the caller; the blob GET works
  // for any user id, since avatars are shown across the app).
  users: {
    list:    ()      => http.get(`${V}/users`),
    get:     (id)    => http.get(`${V}/users/${id}`),
    create:  (d)     => http.post(`${V}/users`, d),
    update:  (id,d)  => http.patch(`${V}/users/${id}`, d),
    disable: (id)    => http.patch(`${V}/users/${id}/disable`, {}),
    del:     (id)    => http.del(`${V}/users/${id}`),
    // v is the avatarUpdatedAt cache token: the server sends the bytes with a
    // 24h Cache-Control, so without a per-version URL the browser would keep
    // serving a replaced avatar's old bytes. The token makes each version a
    // distinct, hard-cacheable URL.
    avatarBlob:   (id,v) => http.getBlob(`${V}/users/${id}/avatar${v ? `?v=${encodeURIComponent(v)}` : ''}`),
    uploadAvatar: (file) => http.upload(`${V}/users/me/avatar`, file),
    deleteAvatar: ()     => http.del(`${V}/users/me/avatar`),
  },
  auditLogs: (p={}) => http.get(`${V}/audit-logs${qs(p)}`),
};

// ═══════════════════════════════════════════════════════════
// PREFETCH — start a view's data before its renderer is reached
// ═══════════════════════════════════════════════════════════
// A project view's data only ever needed the project id, but it was fetched
// from inside the renderer — which runs after loadProject has resolved. That
// put the view's own request a full round trip behind the project bundle, for
// no reason other than call order: opening a board did
// project → (bundle) → tasks as three waves when the last two could have
// travelled together.
//
// Prefetch lets boot and the router start those requests the moment the id is
// known, and the renderer collect them a moment later.
//
// It is a hand-off, not a cache, and is kept honest by two rules: take() removes
// the entry, so a re-render (reloadBoard, a repaint after an edit) issues a
// fresh request instead of replaying an old response; and an entry is only
// handed over while it is younger than TTL, so one left unconsumed — a render
// that errored, a navigation abandoned mid-flight — expires instead of
// surfacing minutes later under a navigation it was never started for.
const PREFETCH_TTL_MS = 10_000;

const Prefetch = {
  _entries: new Map(),

  // start kicks off `fn()` under `key` unless a live entry already exists.
  // The rejection is absorbed here so an unconsumed prefetch cannot surface as
  // an unhandled rejection; take()'s caller still sees the original outcome.
  start(key, fn) {
    const live = this._live(key);
    if (live) return live.p;
    const p = fn();
    p.catch(() => {});
    this._entries.set(key, { p, at: Date.now() });
    return p;
  },

  // take hands over the in-flight request for `key`, or falls back to `fn()`
  // when there is none — a re-render, an expired entry, or a path that was
  // reached without a prefetch. Callers must work either way.
  take(key, fn) {
    const live = this._live(key);
    this._entries.delete(key);
    return live ? live.p : fn();
  },

  _live(key) {
    const e = this._entries.get(key);
    if (!e) return null;
    if (Date.now() - e.at > PREFETCH_TTL_MS) { this._entries.delete(key); return null; }
    return e;
  },

  // drop discards a pending entry so a consumer that just invalidated its own
  // cache cannot be handed a prefetch started before the mutation.
  drop(key) {
    this._entries.delete(key);
  },
};

// The project task set behind the board, task list, backlog and mindmap views.
// It reads EVERY page (api.tasks.listAll), not just the API's 200-row maximum:
// the four views share this response, and the mindmap in particular needs the
// whole set or it draws a hierarchy with its oldest parents missing. One key
// per project — a key that left the project out would hand project A's tasks to
// a board opened on project B moments later.
function _tasksKey(pid) {
  return 'tasks:' + pid;
}

function prefetchProjectTasks(pid) {
  return Prefetch.start(_tasksKey(pid), () => api.tasks.listAll(pid));
}

function takeProjectTasks(pid) {
  return Prefetch.take(_tasksKey(pid), () => api.tasks.listAll(pid));
}

function dropProjectTasksPrefetch(pid) {
  Prefetch.drop(_tasksKey(pid));
}

export { Prefetch, V, api, dropProjectTasksPrefetch, prefetchProjectTasks, takeProjectTasks };
