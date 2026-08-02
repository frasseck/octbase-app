import { renderAdminPanel, renderAuditLogs } from './admin.js';
import { Auth } from './auth.js';
import { closeSidebar, hideModal, renderAcceptInvitationPage, renderForgotPasswordPage, renderLoginPage, renderResetPasswordPage } from './framework.js';
import { clearContentStale } from './realtime.js';
import { Views } from './registry.js';
import { S } from './state.js';
import { renderSettingsPage } from './views-settings.js';
import { loadProject, renderContent, renderDashboardPage, renderSearchPage, renderSidebar, renderTopbar, showProjectsView } from './views-shell.js';
import { openTaskPanel } from './views-task.js';

// Octbase SPA — split from the former single app.js (and later from api.js,
// which had grown to conflate auth, the HTTP client, the REST surface, the
// router, and permission helpers). One ES module among many, bundled by Vite
// (37b stage 2): its top-level declarations are file-private and its public
// surface is the `export { … }` block at the bottom. Imports carry the
// dependencies — there is no load order to keep in step (js/README.md).

// ═══════════════════════════════════════════════════════════
// ROUTER — hash-based, encodes view state in URL
// ═══════════════════════════════════════════════════════════
const router = {
  go(path) {
    window.location.hash = path;
  },
  // navigate routes to `path` exactly once, and is what boot and the post-login
  // handlers should use. Assigning location.hash fires `hashchange`, which
  // routes on its own — but assigning the value the hash already holds fires
  // nothing at all. Callers that covered both cases with `go(p)` *followed by*
  // `handleRoute()` therefore rendered the page twice whenever the hash really
  // changed: once against a half-loaded shell and again a round trip later.
  // That second full-body render is what looked like the browser reloading the
  // page on open. Call with no argument to route the URL as it stands.
  navigate(path) {
    if (path === undefined || path === this.current()) { handleRoute(); return; }
    this.go(path);
  },
  current() {
    return window.location.hash.slice(1) || '/';
  },
  params() {
    const [path, search] = this.current().split('?');
    return { path, query: new URLSearchParams(search || '') };
  },
};
window.router = router;

window.addEventListener('hashchange', handleRoute);

// resolveProjectView maps a URL sub-path to a known, enabled project view via
// the view registry: unknown or feature-disabled views fall back
// deterministically (the entry's `fallback` — e.g. the disabled Task view →
// its closest analog, the Backlog — anything else → the board) so a
// stale/garbage URL degrades gracefully instead of rendering a blank pane.
// The backend remains authoritative; this is UX hardening only.
function resolveProjectView(sub) {
  return Views.resolve(sub);
}

// _routeGen guards the /projects/:id branch below: two hashchanges in quick
// succession (e.g. clicking between two projects before the first resolves)
// otherwise let the slower, superseded call's loadProject/renderContent
// commit after the newer one, flipping the view back to stale data.
let _routeGen = 0;

async function handleRoute() {
  const routeGen = ++_routeGen;
  closeSidebar();
  hideModal();
  // Any navigation retires the banner: it describes the page being left, and
  // its reload button would repaint whatever view came next. renderContent()
  // clears it too, but not every page goes through it — the settings, admin and
  // dashboard pages write #content themselves, which is exactly where a banner
  // was left hanging over content it no longer referred to.
  clearContentStale();
  if (!Auth.isAuthenticated()) {
    const p = router.current();
    if (!p.startsWith('/login') && !p.startsWith('/invitations') &&
        !p.startsWith('/forgot-password') && !p.startsWith('/reset-password')) {
      router.go('/login');
      return;
    }
  }
  const { path, query } = router.params();

  if (path === '/login') {
    // Bounce to the dashboard only with a token actually in hand. In standalone
    // (file://) mode isAuthenticated() is true by construction, so a failed demo
    // login used to ping-pong /login → /dashboard → 401 → /login without end.
    if (Auth.token) { router.go('/dashboard'); return; }
    renderLoginPage();
    return;
  }
  if (path.startsWith('/invitations/') && path.endsWith('/accept')) {
    const token = path.split('/')[2];
    renderAcceptInvitationPage(token);
    return;
  }
  if (path === '/forgot-password') {
    renderForgotPasswordPage();
    return;
  }
  if (path.startsWith('/reset-password/')) {
    renderResetPasswordPage(path.split('/')[2]);
    return;
  }

  // Authenticated routes
  if (path === '/' || path === '/dashboard') { await renderDashboardPage(); return; }
  if (path === '/projects')                  { await showProjectsView(); return; }
  if (path === '/search')                    { await renderSearchPage(query.get('q')); return; }
  if (path === '/admin')                     { await renderAdminPanel(); return; }
  if (path === '/admin/audit-logs')          { await renderAuditLogs(); return; }
  // Notification preferences live on the settings dashboard now; redirect the
  // old standalone route so existing links/bookmarks keep working.
  if (path === '/preferences/notifications') { router.go('/settings'); return; }
  if (path === '/settings')                  { await renderSettingsPage(); return; }

  const projMatch = path.match(/^\/projects\/([^/]+)(?:\/(.+))?$/);
  if (projMatch) {
    const [,pid, sub] = projMatch;
    // Resolve the target view *before* loading the project so its data can be
    // requested now, travelling with the project bundle instead of a round trip
    // behind it (registry `prefetch`; the renderer collects it via
    // Prefetch.take). Resolution is synchronous and reads only the feature
    // flags, which boot has already settled.
    const view = resolveProjectView(sub ? sub.split('?')[0] : 'board');
    Views.get(view)?.prefetch?.(pid);
    await loadProject(pid);
    if (routeGen !== _routeGen) return;
    S.view = view;
    // Restore filters from URL.
    S.filters.priority = query.get('priority') || '';
    S.filters.status = query.get('status') || '';
    S.filters.type = query.get('type') || '';
    S.filters.q = query.get('q') || '';
    renderSidebar(); renderTopbar(); await renderContent();
    // Open task panel if task= param present.
    const taskId = query.get('task');
    if (taskId) openTaskPanel(taskId);
    return;
  }
  await showProjectsView();
}

export { handleRoute, router };
