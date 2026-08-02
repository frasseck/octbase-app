// Octbase SPA — split from the former single app.js. One ES module among many,
// bundled by Vite (37b stage 2): its top-level declarations are file-private
// and its public surface is the `export { … }` block at the bottom. Imports
// carry the dependencies — there is no load order to keep in step
// (js/README.md).
//
// ═══════════════════════════════════════════════════════════
// VIEW REGISTRY — the shell knows no view by name
// ═══════════════════════════════════════════════════════════
// Each views-* module registers its views here at load time (this file loads
// before all view modules; load-time code may reference earlier-loaded symbols,
// see js/README.md). The shell renders FROM the registry: the sidebar list, the
// content dispatch, the create button, the list-refresh on filter changes and
// the /projects/:id/:view route fallback are all registry-driven. Adding a view
// means registering it in its own module file — not editing the shell.
//
// ── The entry contract ──
// Views.register(id, {
//   scope        — 'project' (default: needs S.project, routable under
//                  /projects/:id/…) or 'global' (dashboard / projects — core
//                  navigation targets, never resolved from a project URL)
//   render       — async () => renders into #content (the shell owns the
//                  spinner and the error state around it)
//   prefetch     — (projectId) => start the requests render() will need, from
//                  the router, as soon as the project id is known. render()
//                  collects them via Prefetch.take (api.js) and falls back to
//                  fetching itself when it was reached without one. Optional:
//                  a view without it simply fetches inside render() as before.
//   standalone   — render() owns the full lifecycle incl. sidebar/topbar
//                  (the dashboard); the shell dispatches and stops
//   enabled      — () => bool; a disabled view vanishes entirely: no sidebar
//                  entry, no dispatch, and its route resolves to `fallback`.
//                  This is how deployment feature gates plug in (FEATURES.*).
//   fallback     — view id a disabled/unknown project route falls back to
//                  (default 'board')
//   sidebar      — { icon, label: () => t(…), key?, order, when?: () => bool }
//                  project-nav entry; `when` hides without disabling (e.g. the
//                  sprint board while no sprint exists); omit for routable-only
//                  views (members)
//   createButton — () => HTML for the content toolbar's primary create action
//                  (consumed via viewCreateButton / contentToolbar)
//   listToolbar  — true → the shell prepends contentToolbar (task filters +
//                  create button) after render (the task-list views)
//   refreshList  — () => re-render only the list region, preserving input
//                  focus, when a client-side filter/search changes; views
//                  without it get a full renderContent()
//   listConfig   — () => the task-list engine config for this view (see
//                  views-tasklist.js). Present on the sortable list views so
//                  the shared column-sort handler (sortTaskView) can re-render
//                  the current view's list from cache without a per-view branch.
//   liveRefresh  — true → this view shows project content that a co-worker's
//                  change can invalidate, so a `board.changed` SSE event raises
//                  the "content changed" reload banner while it is on screen
//                  (realtime.js). Views that show no live project data (admin,
//                  settings, pages) omit it and stay quiet. The flag is
//                  necessary but not sufficient: realtime.js also checks the
//                  event's activityType, since these views all project *task*
//                  data and a wiki-page or comment change alters nothing they
//                  draw. Every liveRefresh view is task-derived, so that test
//                  stays in realtime.js rather than becoming a per-view list.
// })
const Views = {
  _defs: Object.create(null),

  register(id, def) {
    this._defs[id] = Object.assign({ id, scope: 'project' }, def);
  },

  // get returns the enabled entry for id, or null (unknown or feature-disabled).
  get(id) {
    const d = this._defs[id];
    return d && (!d.enabled || d.enabled()) ? d : null;
  },

  // resolve maps a /projects/:id sub-path to an enabled project view. Unknown
  // or disabled views fall back deterministically (the entry's `fallback`,
  // default the board) so a stale/garbage URL degrades gracefully instead of
  // rendering a blank pane. The backend remains authoritative; this is UX
  // hardening only.
  resolve(sub) {
    const d = this._defs[sub];
    if (d && d.scope === 'project' && (!d.enabled || d.enabled())) return sub;
    return (d && d.fallback) || 'board';
  },

  // sidebarEntries returns the project-nav views in display order.
  sidebarEntries() {
    return Object.values(this._defs)
      .filter(d => d.scope === 'project' && d.sidebar &&
                   (!d.enabled || d.enabled()) &&
                   (!d.sidebar.when || d.sidebar.when()))
      .sort((a, b) => a.sidebar.order - b.sidebar.order);
  },
};
window.Views = Views;

export { Views };
