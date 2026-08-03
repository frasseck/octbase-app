import { t } from '@octbase/shared/i18n.js';
import { notificationMessage } from '@octbase/shared/notifications.js';
import { V, api } from './api.js';
import { Auth } from './auth.js';
import { _A0, _A2, registerActions, registerChanges } from './delegation.js';
import { API_BASE, USE_STANDALONE_DEMO_AUTH } from './env.js';
import { el, esc, fmtDateTime, html, raw, toast } from './framework.js';
import { apiErrorMessage } from './http.js';
import { Views } from './registry.js';
import { S } from './state.js';
import { hideProjectMenu, renderContent } from './views-shell.js';
import { openTaskPanel, renderTaskPanel } from './views-task.js';

// Octbase SPA — split from the former single app.js. One ES module among many,
// bundled by Vite (37b stage 2): its top-level declarations are file-private
// and its public surface is the `export { … }` block at the bottom. Imports
// carry the dependencies — there is no load order to keep in step
// (js/README.md).
let _sseBackoff = 1000;

function connectSSE(projectId) {
  disconnectSSE();
  // EventSource cannot set an Authorization header, so the token rides in the
  // query string — which means an absent token would be sent as the literal
  // "?token=null" and answered with a 401. Require a real one.
  if (!Auth.isAuthenticated() || !Auth.token) return;
  const url = `${API_BASE}${V}/projects/${projectId}/events`;
  try {
    const source = new EventSource(url + '?token=' + Auth.token);
    source.onmessage = (e) => {
      // Any event off the stream is proof the session is alive.
      markSession(true);
      try { handleSSEEvent(JSON.parse(e.data)); } catch {}
    };
    source.onopen = () => {
      _sseBackoff = 1000;
      markSession(true);
    };
    source.onerror = () => {
      // A dropped project stream isn't necessarily a dead session, so re-probe
      // the session rather than flipping the indicator red on a transient blip.
      checkSession();
      source.close();
      setTimeout(() => {
        // Re-check the session as well as the project: without this the stream
        // kept reconnecting after the session ended, each attempt a fresh 401.
        if (S.project && Auth.isAuthenticated()) connectSSE(S.project.id);
      }, _sseBackoff);
      _sseBackoff = Math.min(_sseBackoff * 2, 30000);
    };
    S.sseSource = source;
  } catch {}
}

function disconnectSSE() {
  // Leaving a project closes its event stream but the session is still alive,
  // so we deliberately don't touch the live indicator here — the heartbeat
  // keeps it accurate on project-less pages.
  if (S.sseSource) { S.sseSource.close(); S.sseSource = null; }
}

// Close the SSE stream before the page unloads so the browser does not log the
// aborted streaming request ("connection interrupted while the page was loading")
// on reload or navigation.
window.addEventListener('beforeunload', () => {
  if (S.sseSource) S.sseSource.close();
});

// The live indicator reflects the user's session health (not a single
// project's event stream): green "Live" while we can reach the server with a
// valid session, grey "Disconnected" once the session goes stale, and a neutral
// "Connecting…" until the first probe resolves. It is shown on every page.
function updateLiveIndicator() {
  const indicator = el('#live-indicator');
  if (!indicator) return;
  let state, label;
  if (!S.sessionKnown)    { state = '';               label = t('app.connecting');   }
  else if (S.sessionLive) { state = ' live-connected'; label = t('app.live');         }
  else                    { state = ' live-stale';     label = t('app.disconnected'); }
  indicator.className = 'live-indicator' + state;
  indicator.title = label;
  indicator.setAttribute('aria-label', label);
}

// ═══════════════════════════════════════════════════════════
// SESSION HEARTBEAT
// ═══════════════════════════════════════════════════════════
// A lightweight authenticated probe keeps the live indicator honest on every
// page, including those with no SSE stream (projects list, admin, settings).
let _sessionHeartbeatTimer = null;

function startSessionHeartbeat() {
  clearInterval(_sessionHeartbeatTimer);
  _sessionHeartbeatTimer = setInterval(checkSession, 30000);
}

function stopSessionHeartbeat() {
  clearInterval(_sessionHeartbeatTimer);
  _sessionHeartbeatTimer = null;
}

// checkSession probes the session via /auth/me. http auto-refreshes the access
// token on a 401, so a rejection here means the session is genuinely stale
// (server unreachable, or the refresh cookie expired).
async function checkSession() {
  // mayHaveSession, not isAuthenticated: a heartbeat firing before boot's
  // refresh has resolved would otherwise short-circuit on the null token and
  // paint the indicator dead for a session that is fine. Probing is what this
  // function is for — the comment above says the 401 retry is what tells stale
  // from restorable, and the bare token test returned before it could (OCT-321).
  if (!Auth.mayHaveSession()) { markSession(false); return; }
  try {
    await api.auth.me();
    markSession(true);
  } catch {
    markSession(false);
  }
}

// endSession tears down everything that assumes a live session: the access
// token, the three timers, and the event stream. Both paths out of a session
// must run it. Explicit logout always did the equivalent; a session that ended
// by *expiry* did not — it only dropped the token, leaving the heartbeat, the
// notification poller and the SSE reconnect armed. Each then kept firing
// authenticated requests that could only 401, which is where the stream of 401s
// on a signed-out client came from.
function endSession() {
  Auth.token = null;
  stopSessionHeartbeat();
  stopNotifPolling();
  stopIdleTimer();
  disconnectSSE();
  clearTimeout(_staleTimer);
  // Clearing the project stops the SSE error handler from reconnecting to it.
  S.project = null;
  clearContentStale();
  markSession(false);
}

// markSession records the latest known session health and refreshes the
// indicator. SSE open/message events and heartbeat probes all flow through it.
function markSession(alive) {
  S.sessionLive = alive;
  S.sessionKnown = true;
  updateLiveIndicator();
}

// ═══════════════════════════════════════════════════════════
// IDLE SESSION TIMEOUT
// ═══════════════════════════════════════════════════════════
// Sign the user out after a sustained stretch of inactivity. Any genuine user
// interaction resets the countdown. The backend caps the refresh token to the
// same window (OCTBASE_JWT_REFRESH_TTL), so the session can't outlive this even
// if this client-side timer is bypassed.
const SESSION_IDLE_TIMEOUT_MS = 60 * 60 * 1000; // 60 minutes
const IDLE_ACTIVITY_EVENTS = ['mousedown', 'keydown', 'scroll', 'touchstart', 'pointerdown'];
let _idleTimer = null;
let _idleLastReset = 0;

function onIdleTimeout() {
  toast(t('errors.sessionTimedOut'), 'info');
  Auth.logout();
}

function resetIdleTimer() {
  clearTimeout(_idleTimer);
  _idleTimer = setTimeout(onIdleTimeout, SESSION_IDLE_TIMEOUT_MS);
}

// Throttle the reset so high-frequency events (scroll) don't reschedule the
// timer on every tick — once per 30s is ample given the 60-minute window.
function handleIdleActivity() {
  const now = Date.now();
  if (now - _idleLastReset < 30000) return;
  _idleLastReset = now;
  resetIdleTimer();
}

function startIdleTimer() {
  if (USE_STANDALONE_DEMO_AUTH) return; // demo mode auto-signs-in; no timeout
  IDLE_ACTIVITY_EVENTS.forEach(ev =>
    window.addEventListener(ev, handleIdleActivity, { passive: true }));
  _idleLastReset = Date.now();
  resetIdleTimer();
}

function stopIdleTimer() {
  clearTimeout(_idleTimer);
  _idleTimer = null;
  IDLE_ACTIVITY_EVENTS.forEach(ev =>
    window.removeEventListener(ev, handleIdleActivity));
}

// ═══════════════════════════════════════════════════════════
// STALE-CONTENT BANNER
// ═══════════════════════════════════════════════════════════
// A co-worker's change used to re-render the board on its own. Repainting under
// someone who is reading or mid-edit is disruptive and costs them their scroll
// position and selection, so the change is now *announced* instead: a banner
// says the content moved on and the user reloads when it suits them. Nothing
// repaints unasked.
//
// The banner is debounced only so a burst of edits (or one action logging
// several activity entries) doesn't thrash the DOM node; it is idempotent, so
// the delay never loses an event.
let _staleTimer = null;

function markContentStale() {
  clearTimeout(_staleTimer);
  _staleTimer = setTimeout(() => {
    S.contentStale = true;
    showContentStaleBar();
  }, 400);
}

// clearContentStale drops the flag and hides the banner. renderContent() calls
// it on every repaint, so navigating, filtering or reloading all clear the
// banner as a side effect of showing fresh data — there is no second path to
// keep in sync.
function clearContentStale() {
  clearTimeout(_staleTimer);
  S.contentStale = false;
  const bar = el('#content-stale-bar');
  if (bar) bar.classList.add('hidden');
}

function showContentStaleBar() {
  const bar = el('#content-stale-bar');
  if (!bar) return;
  bar.innerHTML = html`
    <span class="content-stale-text">${raw(t('app.contentChanged'))}</span>
    <button class="btn btn-sm" data-act="reloadStaleContent">${raw(t('app.reload'))}</button>`;
  bar.classList.remove('hidden');
}

// reloadStaleContent is the banner's button: refetch whatever is on screen. The
// open task panel is refreshed too, since it can be the only thing that changed.
async function reloadStaleContent() {
  clearContentStale();
  if (S.taskPanelId) renderTaskPanel(S.taskPanelId).catch(() => {});
  await renderContent().catch(() => {});
}

// The backend funnels every logged change through one event type
// (`board.changed`), so a single stream carries ~23 different `activityType`
// values. `liveRefresh` says a view shows live project content; it does not say
// that *this* change is content that view draws. These two families are not:
//
//   PAGE_*          — wiki pages. No liveRefresh view renders a page.
//   TASK_COMMENT_*  — board cards carry no comment count, task-list rows carry
//                     none, and statistics is built from status/type/assignee
//                     counts and sprint data. Comments change none of it.
//
// Announcing those raised "this content has changed" over a screen that came
// back identical when reloaded, which is how a banner teaches people to ignore
// the one that will matter.
//
// A DENY list, not an allow list, deliberately: an activityType this file has
// never heard of still raises the banner, and so does the webhook publisher,
// whose payload carries no activityType at all yet is a real task change. A
// spurious banner costs one redundant reload; a suppressed one shows stale data
// as if it were current. When in doubt, announce.
const VIEW_BLIND_ACTIVITY = new Set([
  'PAGE_CREATED', 'PAGE_PUBLISHED', 'PAGE_ARCHIVED',
  'TASK_COMMENT_ADDED', 'TASK_COMMENT_UPDATED',
]);

// affectsViewContent — can this change alter what a liveRefresh view draws?
// Judges the event alone, so it stays pure and unit-testable; the caller pairs
// it with the view's own liveRefresh flag. The open task panel is judged
// separately and on purpose: the panel DOES show comments, so a comment on the
// task you have open is a real change to what is on your screen.
function affectsViewContent(activityType) {
  return !VIEW_BLIND_ACTIVITY.has(activityType);
}

function handleSSEEvent(event) {
  if (!event || !event.type) return;
  if (event.type === 'notification.created') {
    S.notifCount++;
    updateNotifBadge();
    return;
  }
  // Skip our own changes: the acting client already reflected them locally
  // (drag/drop re-renders on drop, panel edits update in place), so announcing
  // them back would tell the user their own edit was somebody else's.
  if (event.actorId && S.user && event.actorId === S.user.id) return;
  // Views opt in via the registry's liveRefresh flag (board, backlog, tasks) so
  // the shell keeps its no-per-view-branches rule, and the change must be one
  // that view can actually draw. The open task panel counts too: it can be the
  // only affected thing on screen, on any view — including for a comment.
  const def = Views.get(S.view);
  const panelAffected = !!(event.taskId && S.taskPanelId === event.taskId);
  const viewAffected = !!(def && def.liveRefresh) && affectsViewContent(event.activityType);
  if (panelAffected || viewAffected) markContentStale();
}

// ═══════════════════════════════════════════════════════════
// NOTIFICATION BELL
// ═══════════════════════════════════════════════════════════
let _notifPollTimer = null;

function startNotifPolling() {
  clearInterval(_notifPollTimer);
  _notifPollTimer = setInterval(pollNotifications, 60000);
  pollNotifications();
}

function stopNotifPolling() {
  clearInterval(_notifPollTimer);
  _notifPollTimer = null;
}

async function pollNotifications() {
  // The poller outlives a session that ends by expiry rather than by an explicit
  // logout, so it has to check for itself rather than trust that it was stopped.
  // Gate on the token, not isAuthenticated(): the latter is true by construction
  // in standalone (file://) demo mode, which would make this guard inert exactly
  // where a failed demo login leaves us with no token.
  if (!Auth.token) return;
  try {
    const result = await api.notifications.list({ unreadOnly: true, size: 1 });
    S.notifCount = result.unreadCount || 0;
    updateNotifBadge();
  } catch {}
}

function updateNotifBadge() {
  const badge = el('#notif-badge');
  if (!badge) return;
  badge.textContent = S.notifCount > 0 ? (S.notifCount > 99 ? '99+' : S.notifCount) : '';
  badge.className = S.notifCount > 0 ? 'notif-badge' : 'notif-badge hidden';
}

// Closes the panel and detaches the outside-click listener registered in toggleNotifPanel.
function _hideNotifPanel() {
  const panel = el('#notif-panel');
  if (panel) panel.classList.add('hidden');
  document.removeEventListener('click', _onNotifPanelOutsideClick);
}

function _onNotifPanelOutsideClick(ev) {
  const panel = el('#notif-panel');
  if (panel && !panel.contains(ev.target)) _hideNotifPanel();
}

async function toggleNotifPanel(e) {
  if (e) e.stopPropagation();
  const panel = el('#notif-panel');
  if (!panel) return;
  if (!panel.classList.contains('hidden')) {
    _hideNotifPanel();
    return;
  }
  hideProjectMenu();
  try {
    const result = await api.notifications.list({ size: 10 });
    const ns = result.notifications || [];
    const listHtml = ns.length === 0 ? `<div class="notif-empty">${t('notifications.empty')}</div>` :
      ns.map(n => `
        <button type="button" class="notif-item ${n.isRead?'':'notif-unread'}" data-act="openNotif" data-a0="${esc(n.id)}" data-a1="${esc(n.taskId||'')}">
          ${n.isRead ? '' : '<span class="sr-only">Unread: </span>'}
          <span class="notif-msg">${esc(notificationMessage(n))}</span>
          <span class="notif-time">${fmtDateTime(n.createdAt)}</span>
        </button>`).join('');
    panel.innerHTML = html`
      <div class="notif-header">
        <span>${raw(t('notifications.title'))}</span>
        <button class="btn-text" data-act="markAllNotifsRead">${raw(t('notifications.markAllRead'))}</button>
      </div>
      <div class="notif-list">
        ${raw(listHtml)}
      </div>
      <div class="notif-footer">
        <a href="#/settings" class="btn-text" data-act="_hideNotifPanel">${raw(t('notifications.preferences'))}</a>
      </div>`;
    panel.classList.remove('hidden');
    S.notifCount = 0;
    updateNotifBadge();
    document.addEventListener('click', _onNotifPanelOutsideClick);
  } catch(e) { toast(apiErrorMessage(e), 'error'); }
}

async function markAllNotifsRead() {
  try {
    await api.notifications.readAll();
    S.notifCount = 0;
    updateNotifBadge();
    _hideNotifPanel();
    toast(t('notifications.allMarkedRead'), 'success');
  } catch(e) { toast(apiErrorMessage(e), 'error'); }
}

function openNotif(notifId, taskId) {
  api.notifications.markRead(notifId).catch(() => {});
  _hideNotifPanel();
  if (taskId) openTaskPanel(taskId);
}

// ═══════════════════════════════════════════════════════════
// NOTIFICATION PREFERENCES
// ═══════════════════════════════════════════════════════════
// The kinds the backend actually sends (notifications.ValidKinds). `release_due`
// was listed here for three releases while nothing ever emitted it, so the
// settings page offered a switch that could not change anything; it was retired
// on 2026-07-31 and the API now rejects it.
const NOTIFICATION_KINDS = ['task_changed', 'task_assigned', 'reviewer_set', 'mentioned', 'status_changed'];
// Email-only kinds have no in-app channel; their in-app cell is rendered inert.
const EMAIL_ONLY_KINDS = new Set(['task_changed']);
// Per-kind default delivery when the user has no stored preference. Mirrors the
// backend defaults (notifications.EmailDefaultOn): task_changed emails by default.
function defaultPref(kind) {
  return EMAIL_ONLY_KINDS.has(kind) ? { inApp: false, email: true } : { inApp: true, email: false };
}
const _notifPrefs = {};

// loadNotifPrefs fetches the stored per-kind preferences into _notifPrefs
// (falling back to the per-kind defaults) for renderNotifPrefsSection.
async function loadNotifPrefs() {
  const prefs = await api.notifications.getPreferences();
  NOTIFICATION_KINDS.forEach(kind => {
    const p = (prefs || []).find(x => x.kind === kind);
    _notifPrefs[kind] = p ? { inApp: p.inApp, email: p.email } : defaultPref(kind);
  });
}

// renderNotifPrefsSection returns the notification-preferences table, shown
// as a section of the settings page (views-settings.js) so all personal
// settings live on one dashboard. The former standalone
// /preferences/notifications page now redirects there (router.js). Requires
// loadNotifPrefs() to have populated _notifPrefs first.
function renderNotifPrefsSection() {
  return `
    <h3 class="settings-section-title">${t('notifications.preferencesTitle')}</h3>
    <p class="settings-desc">${t('notifications.preferencesDesc')}</p>
    <table class="pref-table">
      <thead>
        <tr>
          <th scope="col">${t('notifications.eventType')}</th>
          <th scope="col">${t('notifications.channelInApp')}</th>
          <th scope="col">${t('notifications.channelEmail')}</th>
        </tr>
      </thead>
      <tbody>
        ${NOTIFICATION_KINDS.map(kind => `
          <tr>
            <th scope="row">${t('notifications.kinds.'+kind)}</th>
            <td>${EMAIL_ONLY_KINDS.has(kind)
              ? `<span class="text-muted" title="${t('notifications.emailOnly')}">—</span>`
              : `<input type="checkbox" aria-label="${t('notifications.channelInApp')} – ${t('notifications.kinds.'+kind)}" ${_notifPrefs[kind].inApp?'checked':''} data-change="updateNotifPreference" data-a0="${esc(kind)}" data-a1="inApp">`}</td>
            <td><input type="checkbox" aria-label="${t('notifications.channelEmail')} – ${t('notifications.kinds.'+kind)}" ${_notifPrefs[kind].email?'checked':''} data-change="updateNotifPreference" data-a0="${esc(kind)}" data-a1="email"></td>
          </tr>`).join('')}
      </tbody>
    </table>`;
}

async function updateNotifPreference(kind, channel, checkbox) {
  const prev = _notifPrefs[kind][channel];
  const checked = checkbox.checked;
  _notifPrefs[kind][channel] = checked;
  try {
    await api.notifications.updatePreference({ kind, ..._notifPrefs[kind] });
    toast(t('form.saved'), 'success');
  } catch(e) {
    _notifPrefs[kind][channel] = prev;
    checkbox.checked = prev;
    toast(apiErrorMessage(e), 'error');
  }
}

// ── Delegation registration: this file's handlers ───────────────────────────
// (see js/README.md "Delegation registration".)
registerActions([_hideNotifPanel, markAllNotifsRead, reloadStaleContent], _A0);
registerActions([openNotif], _A2);
registerActions({
  toggleNotifPanel: (el, ev) => toggleNotifPanel(ev),
});
registerChanges({
  updateNotifPreference: el => updateNotifPreference(el.dataset.a0, el.dataset.a1, el),
});

export { _hideNotifPanel, affectsViewContent, clearContentStale, connectSSE, disconnectSSE, endSession, loadNotifPrefs, markSession, renderNotifPrefsSection, startIdleTimer, startNotifPolling, startSessionHeartbeat, updateLiveIndicator, updateNotifBadge };
