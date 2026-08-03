import qrcode from 'qrcode-generator';
import { getLocale, i18n, t } from '@octbase/shared/i18n.js';
import { api } from './api.js';
import { _A0, _A1, registerActions, registerChanges, registerSubmits } from './delegation.js';
import { THEME_KEY, THEME_ORDER, applyTheme, avatarHtml, el, esc, getThemePref, html, raw, renderAppShell, segSwitch, showModal, toast } from './framework.js';
import { apiErrorMessage } from './http.js';
import { icon } from './icons.js';
import { loadNotifPrefs, renderNotifPrefsSection } from './realtime.js';
import { handleRoute } from './router.js';
import { S } from './state.js';
import { applyUserToShell } from './views-crud.js';
import { renderSidebar, renderTopbar } from './views-shell.js';

// Octbase SPA — split from the former single app.js. One ES module among many,
// bundled by Vite (37b stage 2): its top-level declarations are file-private
// and its public surface is the `export { … }` block at the bottom. Imports
// carry the dependencies — there is no load order to keep in step
// (js/README.md).
//
// Personal settings dashboard: language + theme preferences (backend-persisted
// via GET/PATCH /users/me/preferences, internal/dashboard) and MFA enrollment/
// management (internal/security/mfa). Two independent backend modules behind
// one page — see docs/architecture.md.
//
// Preferences are cached locally (_settingsPrefs) the same way notification
// preferences are (_notifPrefs in realtime.js): the PATCH endpoint always takes
// the full {language, theme} object, so a change to one field re-sends both.
const _settingsPrefs = { language: null, theme: null, terminology: null };
// Set while an MFA enrollment is pending confirmation (secret generated, not
// yet verified with a code). Cleared on confirm, cancel, or navigating away.
let _mfaEnrollment = null;

// reconcilePreferences pulls the server-persisted preferences after login and
// makes them win over the local cache, so settings follow the user across
// devices. Re-caching to localStorage keeps theme-init.js's pre-CSS read
// correct on the next load. Called fire-and-forget from initApp — it must
// never block first paint, so it re-renders on its own when the server
// disagrees with what the cached values already painted.
async function reconcilePreferences() {
  let prefs;
  try { prefs = await api.preferences.get(); } catch { return; }
  applyServerPreferences(prefs);
}

// applyServerPreferences makes prefs the effective local state (theme applied
// + cached, locale switched + re-rendered). Shared by boot reconciliation and
// the settings page so neither can show a value that isn't in effect.
async function applyServerPreferences(prefs) {
  if (THEME_ORDER.includes(prefs.theme) && prefs.theme !== getThemePref()) {
    localStorage.setItem(THEME_KEY, prefs.theme);
    applyTheme(prefs.theme);
    renderTopbar();
  }
  // Vocabulary needs no fetch — the classic overlay travels inside the locale
  // file — so it is applied first and a language switch below repaints once for
  // both. When only the vocabulary changed, repaint here.
  const termChanged = i18n.TERMINOLOGIES.includes(prefs.terminology)
    && prefs.terminology !== i18n.getTerminology();
  if (termChanged) i18n.setTerminology(prefs.terminology);
  if (i18n.AVAILABLE_LOCALES.includes(prefs.language) && prefs.language !== getLocale()) {
    await i18n.setLocale(prefs.language); // persists octbase.lang itself
    renderAppShell();
    applyUserToShell();
    handleRoute();
  } else if (termChanged) {
    renderAppShell();
    applyUserToShell();
    handleRoute();
  }
}

async function renderSettingsPage() {
  S.project = null; S.view = 'settings';
  renderSidebar(); renderTopbar();
  _mfaEnrollment = null;

  const c = el('#content');
  c.innerHTML = html`<div class="page-loader">${raw(t('settings.loading'))}</div>`;

  // Notification preferences (internal/notifications) share this dashboard
  // with language/theme (internal/dashboard) and MFA (internal/security/mfa)
  // — three backend modules, one page. Fetch the two preference sets in
  // parallel; a notification-prefs failure only degrades its own section.
  let prefs;
  let notifPrefsError = null;
  try {
    [prefs] = await Promise.all([
      api.preferences.get(),
      loadNotifPrefs().catch(e => { notifPrefsError = e; }),
    ]);
  } catch (e) {
    c.innerHTML = html`<div class="empty"><div class="empty-title">${raw(t('errors.loadFailed'))}</div><p>${apiErrorMessage(e)}</p></div>`;
    return;
  }
  _settingsPrefs.language = prefs.language;
  _settingsPrefs.theme = prefs.theme;
  _settingsPrefs.terminology = prefs.terminology || i18n.DEFAULT_TERMINOLOGY;
  // Server value wins: apply it before rendering so the selectors below can
  // never show a selected value that isn't actually in effect (e.g. a stale
  // localStorage cache from before this device last synced). A locale switch
  // re-runs handleRoute, which re-enters this view with local and server in
  // agreement — let that pass do the rendering.
  const localeChanges = i18n.AVAILABLE_LOCALES.includes(prefs.language) && prefs.language !== getLocale();
  await applyServerPreferences(prefs);
  if (localeChanges) return;

  // Layout: the two short cards (preferences, MFA) stack in the grid's left
  // column so both columns fill and the page reads as one balanced dashboard.
  const notifSection = notifPrefsError
    ? `<h3 class="settings-section-title">${t('notifications.preferencesTitle')}</h3>
       <p class="settings-desc">${esc(apiErrorMessage(notifPrefsError))}</p>`
    : renderNotifPrefsSection();
  c.innerHTML = html`
    <div class="admin-panel">
      <div class="admin-header">
        <h2 class="admin-title">${raw(t('settings.title'))}</h2>
      </div>

      <div class="grid-2col">
      <div class="settings-col">
        <section class="settings-section" id="settings-profile-section">
          ${raw(renderProfileSection())}
        </section>

        <section class="settings-section" id="settings-mfa-section">
          ${raw(renderMfaSection())}
        </section>

        <section class="settings-section" id="settings-password-section">
          ${raw(renderPasswordSection())}
        </section>

        <section class="settings-section" id="settings-prefs-section">
          ${raw(renderPrefsSection())}
        </section>
      </div>

      <section class="settings-section" id="settings-notifications-section">
        ${raw(notifSection)}
      </section>
      </div>
    </div>`;
}

// The picker control is the shared segSwitch (framework.js); the handlers below
// re-render the section via refreshPrefsSection so aria-checked never goes stale.
function renderPrefsSection() {
  return `
    <h3 class="settings-section-title">${t('settings.preferencesTitle')}</h3>
    <p class="settings-desc">${t('settings.preferencesDesc')}</p>
    <div class="form-group">
      <div class="form-label">${t('settings.language')}</div>
      ${segSwitch(i18n.AVAILABLE_LOCALES.map(loc => ({ value: loc, label: t('settings.languages.' + loc) })),
                  _settingsPrefs.language, 'settingsLanguage', t('settings.language'))}
    </div>
    <div class="form-group">
      <div class="form-label">${t('settings.theme')}</div>
      ${segSwitch(THEME_ORDER.map(th => ({ value: th, label: t('theme.' + th) })),
                  _settingsPrefs.theme, 'settingsTheme', t('settings.theme'))}
    </div>
    <div class="form-group">
      <div class="form-label">${t('settings.terminology')}</div>
      ${segSwitch(i18n.TERMINOLOGIES.map(tm => ({ value: tm, label: t('settings.terminologies.' + tm) })),
                  _settingsPrefs.terminology, 'settingsTerminology', t('settings.terminology'))}
      <p class="settings-desc">${t('settings.terminologyHint')}</p>
    </div>`;
}

function refreshPrefsSection() {
  const mount = el('#settings-prefs-section');
  if (mount) mount.innerHTML = renderPrefsSection();
}

// Profile picture: upload (multipart), preview and remove. The avatar chip is
// rendered via avatarHtml so the shared hydration fills in the current image;
// the hidden file input is triggered by its <label>.
function renderProfileSection() {
  const u = S.user || {};
  const hasAvatar = !!u.avatarUpdatedAt;
  return `
    <h3 class="settings-section-title">${t('settings.profileTitle')}</h3>
    <p class="settings-desc">${t('settings.profileDesc')}</p>
    <div class="settings-profile">
      <div class="settings-profile-avatar">${avatarHtml(u.name || u.email || '', u.id, u.avatarUpdatedAt)}</div>
      <div class="settings-profile-actions">
        <div class="settings-profile-buttons">
          <label class="btn btn-secondary btn-sm" for="settings-avatar-input">${t('settings.profileUpload')}</label>
          <input type="file" id="settings-avatar-input" class="rt-file-input" accept="image/png,image/jpeg,image/gif,image/webp" data-change="settingsAvatarPick" aria-label="${t('settings.profileUpload')}">
          ${hasAvatar ? `<button type="button" class="btn btn-danger btn-sm" data-act="settingsAvatarRemove">${t('settings.profileRemove')}</button>` : ''}
        </div>
        <p class="settings-desc settings-profile-hint">${t('settings.profileHint')}</p>
      </div>
    </div>`;
}

function refreshProfileSection() {
  const mount = el('#settings-profile-section');
  if (mount) mount.innerHTML = renderProfileSection();
}

function _syncSelfAvatar(token) {
  if (S.user) S.user.avatarUpdatedAt = token;
  if (S.user && S.usersMap[S.user.id]) S.usersMap[S.user.id].avatarUpdatedAt = token;
  applyUserToShell();
  refreshProfileSection();
}

async function settingsAvatarPick(node) {
  const file = node.files && node.files[0];
  node.value = '';
  if (!file) return;
  try {
    const res = await api.users.uploadAvatar(file);
    _syncSelfAvatar(res.avatarUpdatedAt);
    toast(t('settings.profileUpdated'), 'success');
  } catch (e) {
    toast(apiErrorMessage(e), 'error');
  }
}

async function settingsAvatarRemove() {
  try {
    await api.users.deleteAvatar();
    _syncSelfAvatar(null);
    toast(t('settings.profileRemoved'), 'success');
  } catch (e) {
    toast(apiErrorMessage(e), 'error');
  }
}

async function settingsLanguage(language) {
  if (language === _settingsPrefs.language) return;
  const prev = _settingsPrefs.language;
  _settingsPrefs.language = language;
  refreshPrefsSection();
  try {
    await api.preferences.update({ language, theme: _settingsPrefs.theme, terminology: _settingsPrefs.terminology });
    // Same full-shell refresh as changeLocale (views-crud.js): sidebar and
    // topbar must switch language too, not just the routed content.
    await i18n.setLocale(language);
    renderAppShell();
    applyUserToShell();
    handleRoute();
    toast(t('form.saved'), 'success');
  } catch (e) {
    _settingsPrefs.language = prev;
    refreshPrefsSection();
    toast(apiErrorMessage(e), 'error');
  }
}

// settingsTerminology switches the interface vocabulary (agile ↔ classic
// project management). Nothing is refetched: the classic wording lives in the
// locale file already in memory, so this is a relabel of what is on screen —
// the same full-shell refresh a language change does, minus the network.
async function settingsTerminology(terminology) {
  if (terminology === _settingsPrefs.terminology) return;
  const prev = _settingsPrefs.terminology;
  _settingsPrefs.terminology = terminology;
  refreshPrefsSection();
  try {
    await api.preferences.update({
      language: _settingsPrefs.language, theme: _settingsPrefs.theme, terminology,
    });
    i18n.setTerminology(terminology); // persists octbase.terminology itself
    renderAppShell();
    applyUserToShell();
    handleRoute();
    toast(t('form.saved'), 'success');
  } catch (e) {
    _settingsPrefs.terminology = prev;
    refreshPrefsSection();
    toast(apiErrorMessage(e), 'error');
  }
}

async function settingsTheme(theme) {
  if (theme === _settingsPrefs.theme) return;
  const prev = _settingsPrefs.theme;
  _settingsPrefs.theme = theme;
  refreshPrefsSection();
  try {
    await api.preferences.update({ language: _settingsPrefs.language, theme, terminology: _settingsPrefs.terminology });
    localStorage.setItem(THEME_KEY, theme);
    applyTheme(theme);
    renderTopbar();
    toast(t('theme.changed', { mode: t('theme.' + theme) }), 'info');
  } catch (e) {
    _settingsPrefs.theme = prev;
    refreshPrefsSection();
    toast(apiErrorMessage(e), 'error');
  }
}

// ═══════════════════════════════════════════════════════════
// MFA
// ═══════════════════════════════════════════════════════════
function refreshMfaSection() {
  const mount = el('#settings-mfa-section');
  if (mount) mount.innerHTML = renderMfaSection();
}

function renderMfaSection() {
  const enabled = !!(S.user && S.user.mfaEnabled);
  if (enabled) {
    return `
      <h3 class="settings-section-title">${t('settings.mfa.title')}</h3>
      <p class="settings-desc">${t('settings.mfa.enabledDesc')}</p>
      <div class="mfa-status mfa-status--enabled">${icon('check')}<span>${t('settings.mfa.enabled')}</span></div>
      <div class="settings-actions">
        <button type="button" class="btn btn-secondary" data-act="openMfaRegenerateModal">${t('settings.mfa.regenerateCodes')}</button>
        <button type="button" class="btn btn-danger" data-act="openMfaDisableModal">${t('settings.mfa.disable')}</button>
      </div>`;
  }
  if (_mfaEnrollment) {
    return `
      <h3 class="settings-section-title">${t('settings.mfa.title')}</h3>
      <p class="settings-desc">${t('settings.mfa.enrollDesc')}</p>
      <div class="mfa-enroll">
        <div class="mfa-qr" role="img" aria-label="${t('settings.mfa.qrAria')}">${mfaEnrollmentQr()}</div>
        <div class="form-group">
          <div class="form-label">${t('settings.mfa.manualEntry')}</div>
          <div class="mfa-secret-row">
            <code class="mfa-secret" id="mfa-secret-value">${esc(_mfaEnrollment.secret)}</code>
            <button type="button" class="icon-btn icon-btn-sm" data-act="copyMfaSecret" title="${t('form.copy')}" aria-label="${t('form.copy')}">${icon('copy')}</button>
          </div>
          <p class="settings-desc">${t('settings.mfa.manualEntryHelp')}</p>
        </div>
        <form data-submit="confirmMfaEnrollmentSubmit" novalidate>
          <div class="form-group">
            <label class="form-label" for="mfa-confirm-code">${t('settings.mfa.enterCode')}</label>
            <input class="form-input" id="mfa-confirm-code" inputmode="numeric" autocomplete="one-time-code" required autofocus>
          </div>
          <button class="btn btn-primary" type="submit">${t('settings.mfa.confirm')}</button>
          <button class="btn btn-text" type="button" data-act="cancelMfaEnrollment">${t('form.cancel')}</button>
        </form>
      </div>`;
  }
  return `
    <h3 class="settings-section-title">${t('settings.mfa.title')}</h3>
    <p class="settings-desc">${t('settings.mfa.disabledDesc')}</p>
    <button type="button" class="btn btn-primary" data-act="startMfaEnrollment">${t('settings.mfa.enable')}</button>`;
}

// mfaEnrollmentQr renders the pending enrollment's otpauth:// URI as an SVG QR
// code, entirely client-side via the pinned qrcode-generator package (the
// secret never leaves the page — no external QR service, per the no-CDN rule).
// The `typeof qrcode !== 'function'` half of the guard retired at 37b stage 4:
// the generator was a classic <script> that might not have run yet, and is now
// a static import that cannot be missing. The manual-entry setup key remains
// the fallback if generation itself throws.
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

// Enabling MFA requires re-authenticating with the current password: the
// backend refuses enrollment from a bare access token (a stolen token must not
// be able to bind MFA to an attacker's authenticator). Collect the password in
// a modal, then start enrollment.
function startMfaEnrollment() {
  showModal(t('settings.mfa.enable'), reauthModalBody({ inputId: 'mfa-enroll-password', withCode: false }), doStartMfaEnrollment, t('settings.mfa.enable'));
}


async function doStartMfaEnrollment() {
  const password = el('#mfa-enroll-password').value;
  const data = await api.mfa.enroll(password);
  _mfaEnrollment = { secret: data.secret, otpauthUrl: data.otpauthUrl };
  // Returned to showModal so the in-page section re-render (which shows the
  // QR/secret step) runs after the modal teardown instead of being stepped on
  // by it (see the onSubmit contract in framework.js showModal).
  return refreshMfaSection;
}

function cancelMfaEnrollment() {
  _mfaEnrollment = null;
  refreshMfaSection();
}

function copyMfaSecret() {
  if (!_mfaEnrollment) return;
  navigator.clipboard.writeText(_mfaEnrollment.secret)
    .then(() => toast(t('form.copied'), 'success'))
    .catch(() => {});
}

async function confirmMfaEnrollmentSubmit(e) {
  e.preventDefault();
  const input = el('#mfa-confirm-code');
  const code = input.value.trim();
  try {
    const result = await api.mfa.confirm(code);
    _mfaEnrollment = null;
    if (S.user) S.user.mfaEnabled = true;
    refreshMfaSection();
    showRecoveryCodesModal(result.recoveryCodes || []);
  } catch (e2) {
    toast(apiErrorMessage(e2), 'error');
    input.setAttribute('aria-invalid', 'true');
    input.focus();
  }
}

// showRecoveryCodesModal displays a one-time-reveal batch of recovery codes.
// There is nothing to "submit" here — the single button just acknowledges and
// closes, reusing showModal's cascade-free footer for consistency with every
// other dialog in the app rather than inventing a second dialog primitive.
function showRecoveryCodesModal(codes) {
  const body = `
    <p class="settings-desc">${t('settings.mfa.recoveryCodesDesc')}</p>
    <ul class="recovery-codes-list">
      ${codes.map(c => `<li><code>${esc(c)}</code></li>`).join('')}
    </ul>
    <button type="button" class="btn btn-secondary" data-act="copyRecoveryCodes">${t('form.copyAll')}</button>`;
  _recoveryCodesForCopy = codes;
  showModal(t('settings.mfa.recoveryCodesTitle'), body, async () => {}, t('settings.mfa.recoveryCodesAck'));
}
let _recoveryCodesForCopy = [];
function copyRecoveryCodes() {
  navigator.clipboard.writeText(_recoveryCodesForCopy.join('\n'))
    .then(() => toast(t('form.copied'), 'success'))
    .catch(() => {});
}

// reauthModalBody backs all three MFA re-auth prompts so they cannot drift.
// Enable (enroll) collects only the password under its own input id — no MFA
// is active yet, so there is no code to offer; disable and regenerate also
// accept a TOTP/recovery code (see internal/security/mfa's re-auth rule:
// never a bare toggle).
function reauthModalBody({ inputId = 'mfa-reauth-password', withCode = true } = {}) {
  return `
    <p class="settings-desc">${t('settings.mfa.reauthDesc')}</p>
    <div class="form-group">
      <label class="form-label" for="${inputId}">${t('auth.password')}</label>
      <input class="form-input" id="${inputId}" type="password" autocomplete="current-password">
    </div>${withCode ? `
    <div class="form-group">
      <label class="form-label" for="mfa-reauth-code">${t('settings.mfa.orCode')}</label>
      <input class="form-input" id="mfa-reauth-code" inputmode="numeric" autocomplete="one-time-code">
    </div>` : ''}`;
}

function openMfaDisableModal() {
  showModal(t('settings.mfa.disableTitle'), reauthModalBody(), doDisableMfa, t('settings.mfa.disable'));
}

async function doDisableMfa() {
  const password = el('#mfa-reauth-password').value;
  const code = el('#mfa-reauth-code').value.trim();
  await api.mfa.disable({ password, code });
  if (S.user) S.user.mfaEnabled = false;
  refreshMfaSection();
  toast(t('settings.mfa.disabled'), 'success');
}

function openMfaRegenerateModal() {
  showModal(t('settings.mfa.regenerateTitle'), reauthModalBody(), doRegenerateRecoveryCodes, t('settings.mfa.regenerateCodes'));
}

async function doRegenerateRecoveryCodes() {
  const password = el('#mfa-reauth-password').value;
  const code = el('#mfa-reauth-code').value.trim();
  const result = await api.mfa.regenerateRecoveryCodes({ password, code });
  const codes = result.recoveryCodes || [];
  // Returned to showModal so the recovery-codes modal opens after this modal's
  // teardown instead of being closed by the same hideModal() (see the onSubmit
  // contract in framework.js showModal).
  return () => showRecoveryCodesModal(codes);
}

// ── Password ────────────────────────────────────────────────────────────────
// POST /auth/change-password (internal/auth). The backend proves the current
// password, revokes every OTHER session and re-issues this device's refresh
// cookie, so a successful change does not sign the user out here.
//
// Errors are shown inline rather than as a toast: two of the three failure
// modes belong to a specific field (a wrong current password, a new password
// the policy rejects), and a toast cannot say which input to fix. The
// confirmation mismatch never reaches the API at all.
function renderPasswordSection() {
  return `
    <h3 class="settings-section-title">${t('settings.password.title')}</h3>
    <p class="settings-desc">${t('settings.password.desc')}</p>
    <form data-submit="changePasswordSubmit" novalidate>
      <div class="form-group">
        <label class="form-label" for="settings-password-current">${t('settings.password.current')}</label>
        <input class="form-input" id="settings-password-current" type="password" autocomplete="current-password" required>
      </div>
      <div class="form-group">
        <label class="form-label" for="settings-password-new">${t('settings.password.new')}</label>
        <input class="form-input" id="settings-password-new" type="password" autocomplete="new-password" required>
        <p class="settings-desc">${t('settings.password.policyHint')}</p>
      </div>
      <div class="form-group">
        <label class="form-label" for="settings-password-confirm">${t('settings.password.confirm')}</label>
        <input class="form-input" id="settings-password-confirm" type="password" autocomplete="new-password" required>
      </div>
      <p class="form-error" id="settings-password-error" role="alert" hidden></p>
      <button class="btn btn-primary" type="submit">${t('settings.password.submit')}</button>
    </form>`;
}

// showPasswordError puts the message next to the form and moves focus to the
// input at fault, so a keyboard or screen-reader user lands on the thing they
// have to change rather than having to hunt for it.
function showPasswordError(message, focusId) {
  const box = el('#settings-password-error');
  if (box) {
    box.textContent = message;
    box.hidden = false;
  }
  const input = focusId && el('#' + focusId);
  if (input) {
    input.setAttribute('aria-invalid', 'true');
    input.focus();
  }
}

function clearPasswordError() {
  const box = el('#settings-password-error');
  if (box) {
    box.textContent = '';
    box.hidden = true;
  }
  ['settings-password-current', 'settings-password-new', 'settings-password-confirm']
    .forEach(id => { const i = el('#' + id); if (i) i.removeAttribute('aria-invalid'); });
}

async function changePasswordSubmit(e) {
  e.preventDefault();
  clearPasswordError();
  const current = el('#settings-password-current').value;
  const next = el('#settings-password-new').value;
  const confirm = el('#settings-password-confirm').value;

  if (!current || !next) {
    showPasswordError(t('settings.password.required'), current ? 'settings-password-new' : 'settings-password-current');
    return;
  }
  if (next !== confirm) {
    showPasswordError(t('settings.password.mismatch'), 'settings-password-confirm');
    return;
  }

  try {
    await api.auth.changePassword(current, next);
    // Re-render to clear the three inputs; there is nothing stateful to keep.
    refreshPasswordSection();
    toast(t('settings.password.changed'), 'success');
  } catch (err) {
    // CURRENT_PASSWORD_INVALID is the one failure that points at the first
    // field; every VALIDATION_ERROR from this endpoint carries details.field,
    // and each of those is about the new password (policy, blank, or identical
    // to the current one).
    const wrongCurrent = err && err.code === 'CURRENT_PASSWORD_INVALID';
    const field = err && err.details && err.details.field;
    const focus = wrongCurrent || field === 'currentPassword'
      ? 'settings-password-current'
      : 'settings-password-new';
    showPasswordError(apiErrorMessage(err), focus);
  }
}

function refreshPasswordSection() {
  const mount = el('#settings-password-section');
  if (mount) mount.innerHTML = renderPasswordSection();
}

// ── Delegation registration: this file's handlers ───────────────────────────
// (see js/README.md "Delegation registration".)
registerActions([
  startMfaEnrollment, cancelMfaEnrollment, copyMfaSecret, copyRecoveryCodes,
  openMfaDisableModal, openMfaRegenerateModal, settingsAvatarRemove,
], _A0);
registerActions([settingsLanguage, settingsTerminology, settingsTheme], _A1);
registerChanges({
  settingsAvatarPick: node => settingsAvatarPick(node),
});
registerSubmits({
  confirmMfaEnrollmentSubmit: (el, ev) => confirmMfaEnrollmentSubmit(ev),
  changePasswordSubmit: (el, ev) => changePasswordSubmit(ev),
});

export { openMfaDisableModal, openMfaRegenerateModal, reconcilePreferences, renderSettingsPage, startMfaEnrollment };
