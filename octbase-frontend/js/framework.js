import qrcode from 'qrcode-generator';
import { getLocale, i18n, t } from '@octbase/shared/i18n.js';
import { STATUS_META, TYPE_META, estimateLabel, estimateText, priorityMeta, taskEstimatable } from '@octbase/shared/meta.js';
import { looksLikeHTML, sanitizeRichText } from '@octbase/shared/richtext.js';
import { V, api } from './api.js';
import { Auth } from './auth.js';
import { ACTIONS, CHANGES, INPUTS, KEYDOWNS, SUBMITS, _A0, _A1, _dispatch, registerActions, registerChanges, registerKeydowns, registerSubmits } from './delegation.js';
import { API_BASE, BASE_PATH } from './env.js';
import { apiErrorMessage, http } from './http.js';
import { icon } from './icons.js';
import { handleRoute, router } from './router.js';
import { S } from './state.js';
import { boardDragLeave, boardDragOver, clearDropIndicators, dropOnColumn, invalidateDragGeom } from './views-board.js';
import { changeLocale, initApp, showCreateTask } from './views-crud.js';
import { openProjectPage, openProjectTask, renderTopbar, setView, showInlineTaskCreate } from './views-shell.js';
import { closeLightbox, closeTaskPanel, lightboxNext, lightboxPrev, rtUploadFiles } from './views-task.js';

// Octbase SPA — split from the former single app.js. One ES module among many,
// bundled by Vite (37b stage 2): its top-level declarations are file-private
// and its public surface is the `export { … }` block at the bottom. Imports
// carry the dependencies — there is no load order to keep in step
// (js/README.md).
function renderLangLinks(changeFn) {
  const current = getLocale();
  const options = i18n.AVAILABLE_LOCALES.map(loc =>
    `<option value="${loc}" ${loc===current?'selected':''}>${esc(loc.toUpperCase())}</option>`
  ).join('');
  return `<select id="lang-select" class="lang-select form-select-sm" aria-label="${t('settings.languageSelector')}" data-change="langSelect" data-a0="${changeFn}">${options}</select>`;
}

function renderAuthHeader() {
  return `
    <header class="auth-header">
      <img class="auth-logo" src="img/octbase_logo.svg" alt="Octbase">
      ${renderLangLinks('switchAuthLocale')}
    </header>`;
}

async function switchAuthLocale(lang) {
  await i18n.setLocale(lang);
  handleRoute();
}

function renderLoginPage() {
  document.body.innerHTML = `
    ${renderAuthHeader()}
    <div class="login-page">
      <div class="login-box">
        <h2>${t('auth.signIn')}</h2>
        <form id="login-form" data-submit="doLogin" novalidate>
          <div id="login-error" class="login-error hidden" role="alert"></div>
          <div class="form-group">
            <label class="form-label" for="login-email">${t('auth.email')}</label>
            <input class="form-input" id="login-email" type="email" placeholder="${t('form.emailPlaceholder')}" required autofocus aria-describedby="login-error">
          </div>
          <div class="form-group">
            <label class="form-label" for="login-password">${t('auth.password')}</label>
            <input class="form-input" id="login-password" type="password" placeholder="${t('form.passwordPlaceholder')}" required aria-describedby="login-error">
          </div>
          <button class="btn btn-primary btn-lg" id="login-submit" type="submit">${t('auth.signIn')}</button>
        </form>
        <nav class="auth-legal">
          <a href="#/forgot-password">${t('auth.forgotPassword')}</a>
        </nav>
      </div>
    </div>
    <nav class="auth-footer">
      <a href="https://ocete.ch/privacy.html" target="_blank" rel="noopener">${t('auth.privacy')}</a>
      <span aria-hidden="true">·</span>
      <a href="https://ocete.ch/impressum.html" target="_blank" rel="noopener">${t('auth.imprint')}</a>
    </nav>
    <div class="app-version" aria-hidden="true">octbase ${S.appVersion}</div>`;
}

async function doLogin(e) {
  e.preventDefault();
  const emailInput = document.getElementById('login-email');
  const passwordInput = document.getElementById('login-password');
  const email = emailInput.value;
  const password = passwordInput.value;
  const errDiv = document.getElementById('login-error');
  const submitBtn = document.getElementById('login-submit');
  errDiv.className = 'login-error hidden';
  errDiv.textContent = '';
  emailInput.removeAttribute('aria-invalid');
  passwordInput.removeAttribute('aria-invalid');
  if (submitBtn) {
    submitBtn.disabled = true;
    submitBtn.textContent = t('auth.signingIn');
  }
  try {
    const result = await fetch(API_BASE + BASE_PATH + '/auth/login', {
      method: 'POST',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ email, password }),
    });
    if (!result.ok) {
      errDiv.textContent = t('auth.invalidCredentials');
      errDiv.className = 'login-error';
      emailInput.setAttribute('aria-invalid', 'true');
      passwordInput.setAttribute('aria-invalid', 'true');
      passwordInput.focus();
      return;
    }
    const data = await result.json();
    if (data.mfaRequired) {
      renderMfaChallengeStep(data.challengeToken);
      return;
    }
    if (data.mfaEnrollmentRequired) {
      await renderMfaEnrollStep(data.enrollmentToken);
      return;
    }
    Auth.token = data.accessToken;
    // initApp routes to the dashboard itself, once, with the user and project
    // list already loaded — navigating first made it render twice.
    await initApp('/dashboard');
  } catch(err) {
    errDiv.textContent = t('auth.connectionError', { message: err.message });
    errDiv.className = 'login-error';
    errDiv.focus?.();
  } finally {
    if (submitBtn) {
      submitBtn.disabled = false;
      submitBtn.textContent = t('auth.signIn');
    }
  }
}

// renderMfaChallengeStep swaps the login form for a second-factor code input
// once the backend has accepted the password but reports MFA is enabled
// (POST /auth/login returned a short-lived challengeToken instead of real
// tokens — see internal/auth/jwt.go's mfaChallengeIssuer). The challenge
// token round-trips through the form (data-a0) rather than a module-level
// variable, so a page reload simply drops back to a fresh login instead of
// holding onto a stale token in memory.
function renderMfaChallengeStep(challengeToken) {
  document.body.innerHTML = `
    ${renderAuthHeader()}
    <div class="login-page">
      <div class="login-box">
        <h2>${t('auth.mfa.title')}</h2>
        <p class="settings-desc">${t('auth.mfa.desc')}</p>
        <form data-submit="doVerifyMfaLogin" data-a0="${esc(challengeToken)}" novalidate>
          <div id="mfa-login-error" class="login-error hidden" role="alert"></div>
          <div class="form-group">
            <label class="form-label" for="mfa-login-code">${t('auth.mfa.codeLabel')}</label>
            <input class="form-input" id="mfa-login-code" inputmode="numeric" autocomplete="one-time-code" required autofocus aria-describedby="mfa-login-error">
          </div>
          <button class="btn btn-primary btn-lg" id="mfa-login-submit" type="submit">${t('auth.mfa.verify')}</button>
        </form>
        <nav class="auth-legal">
          <a href="#" data-act="mfaBackToLogin">${t('auth.mfa.back')}</a>
        </nav>
      </div>
    </div>
    <div class="app-version" aria-hidden="true">octbase ${S.appVersion}</div>`;
}

// mfaBackToLogin abandons the pending challenge and returns to the password
// step — the escape hatch for an expired challenge token (there is nothing to
// revoke server-side; the token simply lapses). handleRoute() re-renders the
// login page since no access token is set at this point.
function mfaBackToLogin() {
  Auth.token = null;
  _loginEnroll = null;
  handleRoute();
}

// _loginEnroll holds the pending forced-enrollment secret between the enroll
// call and confirmation. Cleared on confirm or when abandoning the flow.
let _loginEnroll = null;

// renderMfaEnrollStep drives forced MFA setup when the deployment requires MFA
// (OCTBASE_REQUIRE_MFA) and the account has none: POST /auth/login returned a
// scoped enrollmentToken instead of a session. The token is placed in
// Auth.token so the http client sends it for enroll/confirm (the only routes it
// unlocks — see internal/auth EnrollmentOrAccessMiddleware); it grants no app
// access, so we never navigate into the app until the user signs in again with
// their now-active second factor.
async function renderMfaEnrollStep(enrollmentToken) {
  Auth.token = enrollmentToken;
  try {
    const data = await api.mfa.enroll();
    _loginEnroll = { secret: data.secret, otpauthUrl: data.otpauthUrl };
  } catch (err) {
    Auth.token = null;
    document.body.innerHTML = `
      ${renderAuthHeader()}
      <div class="login-page"><div class="login-box">
        <h2>${t('auth.mfa.enrollTitle')}</h2>
        <div class="login-error" role="alert">${esc(apiErrorMessage(err))}</div>
        <button class="btn btn-primary btn-lg" type="button" data-act="mfaBackToLogin">${t('auth.mfa.back')}</button>
      </div></div>
      <div class="app-version" aria-hidden="true">octbase ${S.appVersion}</div>`;
    return;
  }
  document.body.innerHTML = `
    ${renderAuthHeader()}
    <div class="login-page">
      <div class="login-box">
        <h2>${t('auth.mfa.enrollTitle')}</h2>
        <p class="settings-desc">${t('auth.mfa.enrollDesc')}</p>
        <div class="mfa-qr" role="img" aria-label="${t('auth.mfa.qrAria')}">${loginEnrollQr()}</div>
        <div class="form-group">
          <div class="form-label">${t('auth.mfa.manualEntry')}</div>
          <code class="mfa-secret">${esc(_loginEnroll.secret)}</code>
        </div>
        <form data-submit="doConfirmMfaEnrollLogin" novalidate>
          <div id="mfa-enroll-error" class="login-error hidden" role="alert"></div>
          <div class="form-group">
            <label class="form-label" for="mfa-enroll-code">${t('auth.mfa.codeLabel')}</label>
            <input class="form-input" id="mfa-enroll-code" inputmode="numeric" autocomplete="one-time-code" required autofocus aria-describedby="mfa-enroll-error">
          </div>
          <button class="btn btn-primary btn-lg" id="mfa-enroll-submit" type="submit">${t('auth.mfa.finish')}</button>
        </form>
        <nav class="auth-legal">
          <a href="#" data-act="mfaBackToLogin">${t('auth.mfa.back')}</a>
        </nav>
      </div>
    </div>
    <div class="app-version" aria-hidden="true">octbase ${S.appVersion}</div>`;
}

// loginEnrollQr renders the pending enrollment's otpauth:// URI as an inline SVG
// QR via the pinned qrcode-generator package — mirrors settings' mfaEnrollmentQr
// so the secret never leaves the page. The manual-entry key is the fallback if
// generation throws (the library itself can no longer be missing: it is a
// static import since 37b stage 4, not a classic <script> that may not have run).
function loginEnrollQr() {
  if (!_loginEnroll) return '';
  try {
    const qr = qrcode(0, 'M');
    qr.addData(_loginEnroll.otpauthUrl);
    qr.make();
    return qr.createSvgTag({ cellSize: 4, margin: 3, scalable: true });
  } catch {
    return '';
  }
}

async function doConfirmMfaEnrollLogin(e) {
  e.preventDefault();
  const codeInput = document.getElementById('mfa-enroll-code');
  const errDiv = document.getElementById('mfa-enroll-error');
  const submitBtn = document.getElementById('mfa-enroll-submit');
  errDiv.className = 'login-error hidden';
  errDiv.textContent = '';
  codeInput.removeAttribute('aria-invalid');
  if (submitBtn) submitBtn.disabled = true;
  try {
    const result = await api.mfa.confirm(codeInput.value.trim());
    // MFA is now active. Drop the enrollment token — it is not a session — and
    // show the one-time recovery codes before sending the user to sign in.
    Auth.token = null;
    _loginEnroll = null;
    renderMfaEnrollRecoveryCodes(result.recoveryCodes || []);
  } catch (err) {
    errDiv.textContent = apiErrorMessage(err);
    errDiv.className = 'login-error';
    codeInput.setAttribute('aria-invalid', 'true');
    codeInput.focus();
  } finally {
    if (submitBtn) submitBtn.disabled = false;
  }
}

function renderMfaEnrollRecoveryCodes(codes) {
  document.body.innerHTML = `
    ${renderAuthHeader()}
    <div class="login-page">
      <div class="login-box">
        <h2>${t('auth.mfa.recoveryTitle')}</h2>
        <p class="settings-desc">${t('auth.mfa.recoveryDesc')}</p>
        <ul class="recovery-codes-list">
          ${codes.map(c => `<li><code>${esc(c)}</code></li>`).join('')}
        </ul>
        <button class="btn btn-primary btn-lg" type="button" data-act="mfaBackToLogin">${t('auth.mfa.continueToSignIn')}</button>
      </div>
    </div>
    <div class="app-version" aria-hidden="true">octbase ${S.appVersion}</div>`;
}

async function doVerifyMfaLogin(e, challengeToken) {
  e.preventDefault();
  const codeInput = document.getElementById('mfa-login-code');
  const errDiv = document.getElementById('mfa-login-error');
  const submitBtn = document.getElementById('mfa-login-submit');
  errDiv.className = 'login-error hidden';
  errDiv.textContent = '';
  codeInput.removeAttribute('aria-invalid');
  if (submitBtn) submitBtn.disabled = true;
  try {
    const data = await api.auth.verifyMfa(challengeToken, codeInput.value.trim());
    Auth.token = data.accessToken;
    await initApp('/dashboard');
  } catch(err) {
    errDiv.textContent = apiErrorMessage(err);
    errDiv.className = 'login-error';
    codeInput.setAttribute('aria-invalid', 'true');
    codeInput.focus();
  } finally {
    if (submitBtn) submitBtn.disabled = false;
  }
}

// ═══════════════════════════════════════════════════════════
// ACCEPT INVITATION PAGE
// ═══════════════════════════════════════════════════════════
async function renderAcceptInvitationPage(token) {
  let invInfo = {};
  try {
    invInfo = await http.get(`${V}/invitations/${token}`);
  } catch { /* ignore */ }

  document.body.innerHTML = `
    ${renderAuthHeader()}
    <div class="login-page">
      <div class="login-box">
        <h2>${t('auth.acceptInvitation')}</h2>
        ${invInfo.inviterName ? `<p class="text-muted">${t('auth.invitedBy', { name: esc(invInfo.inviterName) })}</p>` : ''}
        <form data-submit="doAcceptInvitation" data-a0="${esc(token)}" novalidate>
          <div id="inv-error" class="login-error hidden" role="alert"></div>
          <div class="form-group">
            <label class="form-label" for="inv-name">${t('auth.yourName')}</label>
            <input class="form-input" id="inv-name" placeholder="${t('auth.fullNamePlaceholder')}" required autofocus aria-describedby="inv-error">
          </div>
          <div class="form-group">
            <label class="form-label" for="inv-password">${t('auth.password')}</label>
            <input class="form-input" id="inv-password" type="password" placeholder="${t('auth.choosePasswordPlaceholder')}" required aria-describedby="inv-error">
          </div>
          <button class="btn btn-primary btn-lg" type="submit">${t('auth.createAccountSignIn')}</button>
        </form>
      </div>
    </div>
    <div class="app-version" aria-hidden="true">octbase ${S.appVersion}</div>`;
}

async function doAcceptInvitation(e, token) {
  e.preventDefault();
  const name = document.getElementById('inv-name').value;
  const password = document.getElementById('inv-password').value;
  const errDiv = document.getElementById('inv-error');
  errDiv.className = 'login-error hidden';
  try {
    const result = await fetch(API_BASE + `${V}/invitations/${token}/accept`, {
      method: 'POST', credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name, password }),
    });
    if (!result.ok) {
      const d = await result.json().catch(()=>({}));
      errDiv.textContent = d.message || t('auth.failedAcceptInvitation');
      errDiv.className = 'login-error';
      errDiv.focus?.();
      return;
    }
    const data = await result.json();
    Auth.token = data.accessToken;
    await initApp('/dashboard');
    // Confirm the account/project join. Shown after initApp so the freshly
    // rendered shell (and its #toast-container) is in place to receive it.
    toast(t('auth.invitationAccepted'), 'success');
  } catch(err) {
    errDiv.textContent = err.message;
    errDiv.className = 'login-error';
  }
}

// ═══════════════════════════════════════════════════════════
// PASSWORD RESET PAGES
// ═══════════════════════════════════════════════════════════
// Self-service flow: /forgot-password asks for the email; the backend answers
// 202 with the same body whether or not the account exists (no enumeration),
// so the confirmation shown here is deliberately generic. The emailed link
// lands on /reset-password/<token> below.
function renderForgotPasswordPage() {
  document.body.innerHTML = `
    ${renderAuthHeader()}
    <div class="login-page">
      <div class="login-box">
        <h2>${t('auth.forgotTitle')}</h2>
        <p class="settings-desc">${t('auth.forgotDesc')}</p>
        <form data-submit="doForgotPassword" novalidate>
          <div id="forgot-error" class="login-error hidden" role="alert"></div>
          <div class="form-group">
            <label class="form-label" for="forgot-email">${t('auth.email')}</label>
            <input class="form-input" id="forgot-email" type="email" placeholder="${t('form.emailPlaceholder')}" required autofocus aria-describedby="forgot-error">
          </div>
          <button class="btn btn-primary btn-lg" id="forgot-submit" type="submit">${t('auth.sendResetLink')}</button>
        </form>
        <nav class="auth-legal">
          <a href="#/login">${t('auth.backToSignIn')}</a>
        </nav>
      </div>
    </div>
    <div class="app-version" aria-hidden="true">octbase ${S.appVersion}</div>`;
}

async function doForgotPassword(e) {
  e.preventDefault();
  const email = document.getElementById('forgot-email').value.trim();
  const errDiv = document.getElementById('forgot-error');
  const submitBtn = document.getElementById('forgot-submit');
  errDiv.className = 'login-error hidden';
  errDiv.textContent = '';
  if (submitBtn) submitBtn.disabled = true;
  try {
    await http.post(`${V}/auth/forgot-password`, { email });
    document.body.querySelector('.login-box').innerHTML = `
      <h2>${t('auth.forgotTitle')}</h2>
      <p class="settings-desc" role="status">${t('auth.resetEmailSent')}</p>
      <a class="btn btn-primary btn-lg" href="#/login">${t('auth.backToSignIn')}</a>`;
  } catch (err) {
    errDiv.textContent = apiErrorMessage(err);
    errDiv.className = 'login-error';
    if (submitBtn) submitBtn.disabled = false;
  }
}

function renderResetPasswordPage(token) {
  document.body.innerHTML = `
    ${renderAuthHeader()}
    <div class="login-page">
      <div class="login-box">
        <h2>${t('auth.resetTitle')}</h2>
        <form data-submit="doResetPassword" data-a0="${esc(token)}" novalidate>
          <div id="reset-error" class="login-error hidden" role="alert"></div>
          <div class="form-group">
            <label class="form-label" for="reset-password">${t('auth.newPassword')}</label>
            <input class="form-input" id="reset-password" type="password" autocomplete="new-password" placeholder="${t('auth.choosePasswordPlaceholder')}" required autofocus aria-describedby="reset-error">
          </div>
          <button class="btn btn-primary btn-lg" id="reset-submit" type="submit">${t('auth.setNewPassword')}</button>
        </form>
        <nav class="auth-legal">
          <a href="#/login">${t('auth.backToSignIn')}</a>
        </nav>
      </div>
    </div>
    <div class="app-version" aria-hidden="true">octbase ${S.appVersion}</div>`;
}

async function doResetPassword(e, token) {
  e.preventDefault();
  const pwInput = document.getElementById('reset-password');
  const errDiv = document.getElementById('reset-error');
  const submitBtn = document.getElementById('reset-submit');
  errDiv.className = 'login-error hidden';
  errDiv.textContent = '';
  pwInput.removeAttribute('aria-invalid');
  if (submitBtn) submitBtn.disabled = true;
  try {
    await http.post(`${V}/auth/reset-password`, { token, newPassword: pwInput.value });
    // Success ends every session server-side; send the user to sign in fresh.
    document.body.querySelector('.login-box').innerHTML = `
      <h2>${t('auth.resetTitle')}</h2>
      <p class="settings-desc" role="status">${t('auth.resetSuccess')}</p>
      <a class="btn btn-primary btn-lg" href="#/login">${t('auth.backToSignIn')}</a>`;
  } catch (err) {
    errDiv.textContent = apiErrorMessage(err);
    errDiv.className = 'login-error';
    pwInput.setAttribute('aria-invalid', 'true');
    pwInput.focus();
    if (submitBtn) submitBtn.disabled = false;
  }
}

// ═══════════════════════════════════════════════════════════
// APP SHELL
// ═══════════════════════════════════════════════════════════
function renderAppShell() {
  document.body.innerHTML = `
    <a href="#" class="skip-link" data-act="focusContent">${t('accessibility.skipToContent')}</a>
    <div id="app">
      <aside id="sidebar" aria-label="${t('nav.mainNavigation')}">
        <div class="sidebar-logo">
          <img class="logo-img" src="img/octbase_logo.svg" alt="Octbase">
          <button class="btn-hamburger sidebar-toggle-btn" data-act="toggleSidebar" title="${t('nav.toggleNavigation')}" aria-label="${t('nav.toggleNavigation')}" aria-controls="sidebar" aria-expanded="true">${icon('sidebar',{size:'md'})}</button>
          <button class="btn-icon btn-icon-sm sidebar-close-btn" data-act="closeSidebar" title="${t('accessibility.close')}" aria-label="${t('accessibility.close')}">
            ${icon('close',{size:'md'})}
          </button>
        </div>
        <nav id="sidebar-nav" class="sidebar-projects" aria-label="${t('nav.projectsAndViews')}"></nav>
        <div id="sidebar-user">
          <div class="user-avatar" id="user-avatar" aria-hidden="true">TB</div>
          <div class="user-name" id="user-name">${t('app.loadingEllipsis')}</div>
        </div>
      </aside>
      <div id="sidebar-overlay" data-act="closeSidebar" aria-hidden="true"></div>
      <div id="main">
        <header id="topbar"></header>
        <div id="project-frozen-bar" class="project-frozen-bar hidden" role="status"></div>
        <main id="content" tabindex="-1"></main>
      </div>
      <img class="octopus-mascot" src="img/octopus_small.png" alt="" aria-hidden="true">
      <div class="app-version" aria-hidden="true">octbase ${S.appVersion}</div>
    </div>
    <div id="task-panel" role="dialog" aria-modal="false" aria-label="${t('accessibility.taskDetails')}"><div id="task-panel-content"></div></div>
    <div id="modal-backdrop" class="hidden"><div id="modal" role="dialog" aria-modal="true"></div></div>
    <div id="preview-overlay" class="hidden" data-act="closeTaskPreviewBackdrop"></div>
    <div id="lightbox" class="hidden" data-act="closeLightboxBackdrop" tabindex="-1"></div>
    <div id="toast-container" role="status" aria-live="polite" aria-atomic="true"></div>
    <div id="palette-overlay" class="hidden"></div>
    <div id="shortcut-help" class="hidden"></div>
    <div id="bulk-bar" class="hidden" role="region" aria-label="${t('accessibility.bulkActionsRegion')}"></div>
    <div id="content-stale-bar" class="content-stale-bar hidden" role="status" aria-live="polite"></div>
    <div id="notif-panel" class="hidden" role="region" aria-label="${t('notifications.title')}"></div>`;
  applyNavPref();
}

const NAV_HIDDEN_KEY = 'octbase.nav-hidden';
// On the Expanded layout (>1024px) the sidebar is a permanent rail; the hamburger
// collapses/expands it and the choice is remembered. Below 1024px it is an
// off-canvas drawer, where the hamburger slides it in/out instead. matchMedia
// keeps the two behaviours on the same control without overloading one class.
function _isExpandedLayout() {
  return window.matchMedia('(min-width: 1025px)').matches;
}

function toggleSidebar() {
  if (_isExpandedLayout()) {
    const app = el('#app');
    if (!app) return;
    const hidden = app.classList.toggle('nav-hidden');
    try { localStorage.setItem(NAV_HIDDEN_KEY, hidden ? '1' : '0'); } catch {}
  } else {
    const sidebar = el('#sidebar');
    const overlay = el('#sidebar-overlay');
    if (!sidebar) return;
    const isOpen = sidebar.classList.contains('mobile-open');
    sidebar.classList.toggle('mobile-open', !isOpen);
    if (overlay) overlay.classList.toggle('sidebar-overlay-visible', !isOpen);
  }
  syncHamburgerState();
}

function closeSidebar() {
  const sidebar = el('#sidebar');
  const overlay = el('#sidebar-overlay');
  if (sidebar) sidebar.classList.remove('mobile-open');
  if (overlay) overlay.classList.remove('sidebar-overlay-visible');
  syncHamburgerState();
}

// applyNavPref restores the persisted "navigation hidden" choice (Expanded layout
// only) after the shell is (re)built.
function applyNavPref() {
  const app = el('#app');
  if (!app) return;
  let hidden = false;
  try { hidden = localStorage.getItem(NAV_HIDDEN_KEY) === '1'; } catch {}
  app.classList.toggle('nav-hidden', hidden);
  syncHamburgerState();
}

// navExpanded reflects whether the navigation is currently visible, for the
// hamburger's aria-expanded.
function navExpanded() {
  if (_isExpandedLayout()) return !el('#app')?.classList.contains('nav-hidden');
  return !!el('#sidebar')?.classList.contains('mobile-open');
}

// Two toggles share the .btn-hamburger role: the one in the sidebar header and
// the topbar fallback that appears only while the sidebar is hidden — keep
// both buttons' aria-expanded in step.
function syncHamburgerState() {
  const expanded = navExpanded() ? 'true' : 'false';
  document.querySelectorAll('.btn-hamburger').forEach(btn => btn.setAttribute('aria-expanded', expanded));
}

// ═══════════════════════════════════════════════════════════
// UTILS
// ═══════════════════════════════════════════════════════════
function el(sel) { return document.querySelector(sel); }
function h(tag,attrs={},children=[]) {
  const e = document.createElement(tag);
  Object.entries(attrs).forEach(([k,v])=>{
    if(k==='class') e.className=v;
    else if(k==='style') e.style.cssText=v;
    else if(k.startsWith('on')) e[k]=v;
    else e.setAttribute(k,v);
  });
  children.forEach(c => {
    if(c==null) return;
    e.appendChild(typeof c==='string' ? document.createTextNode(c) : c);
  });
  return e;
}

// segSwitch renders a segmented switch (a radiogroup of buttons) — the app's
// one picker control for a small, fixed set of options (styleguide.html:
// segmented buttons, not per-field dropdowns). Buttons dispatch `act` with the
// option value in data-a0; the caller re-renders its section afterwards so
// aria-checked never goes stale. Used by the settings page (language, theme,
// terminology) and the project task settings (estimation unit).
function segSwitch(options, selected, act, ariaLabel) {
  return `<div class="seg-switch" role="radiogroup" aria-label="${esc(ariaLabel)}">
    ${options.map(o => `<button type="button" role="radio" aria-checked="${o.value === selected}" data-act="${act}" data-a0="${esc(o.value)}">${o.value === selected ? icon('check') : ''}<span>${esc(o.label)}</span></button>`).join('')}
  </div>`;
}

function typeBadge(type) {
  const m = TYPE_META[type] || {sym:'?',cls:'type-task',label:type};
  return `<span class="type-badge ${m.cls}" title="${m.label}">${m.sym}</span>`;
}
function statusBadge(s) {
  // Custom lane statuses have no STATUS_META entry; render them with a neutral
  // badge using the raw status text as the label.
  const m = STATUS_META[s] || {label:s,cls:'badge-muted'};
  return `<span class="badge ${m.cls}">${esc(m.label)}</span>`;
}
// estimateTag renders the small effort badge on a board card or backlog row.
// It returns '' — no element at all — when the project does not estimate, when
// the type cannot carry an estimate, or when the task is simply unestimated:
// an empty badge would read as "zero effort", which is a different claim.
// The title carries the unit so the bare number on a card is unambiguous.
function estimateTag(task) {
  if (!taskEstimatable(S.project, task)) return '';
  const text = estimateText(S.project, task);
  if (text === '') return '';
  return `<span class="estimate-tag" title="${esc(estimateLabel(S.project))}">${esc(text)}</span>`;
}
function priorityDot(p) {
  const m = priorityMeta(p);
  const label = m.label || p;
  // Color is supplemented with a screen-reader-only label (WCAG 1.4.1 Use of Color).
  // esc() on both sinks: for an admin-defined custom priority the label IS the raw
  // name, and t() interpolates its vars unescaped. Defense in depth over the
  // server's ValidPriorityName regex — see statusBadge above.
  return `<span class="priority-dot ${m.cls}" title="${esc(label)}" aria-hidden="true"></span><span class="sr-only">${esc(t('accessibility.priorityLabel',{label}))}</span>`;
}
// priorityInline renders the coloured dot next to a visible text label, for list
// rows where a dedicated Priority column has room (Backlog / Tasks). The compact
// board card keeps the label-less priorityDot. The visible label doubles as the
// accessible name, so no separate sr-only span is needed. Keeps the .priority-dot
// element the board/backlog tests assert on.
function priorityInline(p) {
  const m = priorityMeta(p);
  const label = m.label || p;
  return `<span class="priority-dot ${m.cls}" aria-hidden="true"></span><span class="prio-cell-label">${esc(label)}</span>`;
}
// initials computes a name's avatar initials (up to two letters, uppercased).
// Shared by avatarHtml here and the admin/project-member user rows
// (admin.js, views-crud.js) so all three don't each carry their own copy.
function initials(name) {
  return (name||'?').split(' ').map(w=>w[0]).join('').slice(0,2).toUpperCase();
}
// avatarHtml renders a user's avatar chip. With just a name it is the initials
// fallback (unchanged for name-only callers). Given a userId and the user's
// avatarUpdatedAt cache token it also emits an <img> overlay that
// hydrateAvatars() fills in with the authenticated profile picture once fetched;
// until then — and if the user has no picture or the fetch fails — the initials
// show through.
function avatarHtml(name, userId, avatarUpdatedAt) {
  const chip = esc(initials(name));
  const title = esc(name || '');
  if (userId && avatarUpdatedAt) {
    return `<span class="avatar-sm has-avatar" title="${title}">${chip}` +
      `<img class="avatar-img" alt="" aria-hidden="true" data-avatar-user="${esc(userId)}" data-avatar-v="${esc(avatarUpdatedAt)}"></span>`;
  }
  return `<span class="avatar-sm" title="${title}">${chip}</span>`;
}
// userAvatarHtml is the id-first convenience used wherever a userId is on hand
// (assignees, comment authors, member rows): it resolves the display name and
// avatar token from S.usersMap and renders the chip.
function userAvatarHtml(userId) {
  const u = userId ? S.usersMap[userId] : null;
  return avatarHtml(memberName(userId), userId, u && u.avatarUpdatedAt);
}

// Authenticated avatar images cannot ride a plain <img src> (no bearer token),
// so they are fetched once per user+version via http.getBlob and exposed as an
// object URL. Keyed by userId@token so a changed avatar re-fetches and repeated
// renders of the same user reuse one blob.
const _avatarObjectURLs = new Map();
function _avatarObjectURL(userId, token) {
  const key = userId + '@' + (token || '');
  let p = _avatarObjectURLs.get(key);
  if (!p) {
    p = api.users.avatarBlob(userId, token).then(b => URL.createObjectURL(b));
    p.catch(() => _avatarObjectURLs.delete(key)); // let a transient failure retry
    _avatarObjectURLs.set(key, p);
  }
  return p;
}
// hydrateAvatars fills every not-yet-loaded avatar <img> under root with its
// fetched image. Idempotent (marks each img done), so it is safe to call on
// every DOM mutation.
function hydrateAvatars(root) {
  const scope = root || document;
  scope.querySelectorAll('img.avatar-img[data-avatar-user]:not([data-avatar-done])').forEach(img => {
    img.setAttribute('data-avatar-done', '1');
    const uid = img.getAttribute('data-avatar-user');
    const token = img.getAttribute('data-avatar-v') || '';
    // On success the image gets a src and CSS fades it in over the initials;
    // on failure it stays src-less and transparent, so the initials remain.
    _avatarObjectURL(uid, token).then(url => { img.src = url; }).catch(() => {});
  });
}
// setUserAvatarImage points an existing avatar element (e.g. the sidebar chip)
// at the user's picture, adding an <img> child if missing. Used for the
// imperatively-rendered current-user chip that does not go through avatarHtml.
function setUserAvatarImage(chip, userId, token) {
  if (!chip || !userId || !token) return;
  let img = chip.querySelector('img.avatar-img');
  if (!img) {
    img = document.createElement('img');
    img.className = 'avatar-img';
    img.alt = '';
    img.setAttribute('aria-hidden', 'true');
    chip.appendChild(img);
  }
  img.setAttribute('data-avatar-user', userId);
  img.setAttribute('data-avatar-v', token);
  img.removeAttribute('data-avatar-done');
  chip.classList.add('has-avatar');
  hydrateAvatars(chip);
}
// filterCountLabel renders "N of M" only when a filter is actually narrowing
// the list; an unfiltered list (filteredCount === total) shows nothing. Shared
// by the admin user list (admin.js) and the project members list
// (views-crud.js), which use identical filter-count UI.
function filterCountLabel(filteredCount, total) {
  return filteredCount < total ? t('admin.filterCount',{filtered:filteredCount,total}) : '';
}
// ── Date formatting ─────────────────────────────────────────────────────────
// toLocaleDateString(getLocale(), {…}) looks free but is not: a fresh options
// object literal per call defeats the engine's format cache, so every call
// constructs an Intl.DateTimeFormat — the expensive part by far (measured 19ms
// for 400 calls vs 0.6ms through one cached formatter). boardCard calls fmtDate
// twice per card with a due date, so a 200-card board repaint spent that on
// nothing. One formatter is cached per (locale, shape) instead.
//
// The locale is read on every call, never captured: changeLocale() can switch it
// while the app runs, and the next call must format in the new language.
const _DATE_OPTS     = { day:'2-digit', month:'short', year:'numeric' };
const _DATETIME_OPTS = { day:'2-digit', month:'short', hour:'2-digit', minute:'2-digit' };
const _dateFormatters = new Map();
function _dateFormatter(shape, opts) {
  const locale = getLocale();
  const key = locale + '|' + shape;
  let fmt = _dateFormatters.get(key);
  if (!fmt) { fmt = new Intl.DateTimeFormat(locale, opts); _dateFormatters.set(key, fmt); }
  return fmt;
}
// _formatDate keeps the old output for every input, including the two edge cases
// where a formatter and toLocale*String disagree: an unparsable string formats as
// "Invalid Date" through the Date methods but throws through a DateTimeFormat, so
// that one case is still routed the old way.
function _formatDate(s, shape, opts, method) {
  if (!s) return '';
  try {
    const d = new Date(s);
    if (Number.isNaN(d.getTime())) return d[method](getLocale(), opts);
    return _dateFormatter(shape, opts).format(d);
  } catch { return s; }
}
function fmtDate(s) {
  return _formatDate(s, 'date', _DATE_OPTS, 'toLocaleDateString');
}
function fmtDateTime(s) {
  return _formatDate(s, 'datetime', _DATETIME_OPTS, 'toLocaleString');
}
function memberName(uid) {
  if(!uid) return '—';
  const u = S.usersMap[uid];
  if(u) return u.name || u.displayName || u.email || uid.slice(0,8)+'…';
  return uid.slice(0,8)+'…';
}
function releaseName(mid) {
  const m = S.releases.find(m=>m.id===mid);
  return m ? m.name : '—';
}
function sprintName(sid) {
  const s = S.sprints.find(s=>s.id===sid);
  return s ? s.name : '—';
}
function esc(s) {
  if(s == null) return '';
  return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}

// ═══════════════════════════════════════════════════════════
// RICH-TEXT (TASK DESCRIPTION)
// ═══════════════════════════════════════════════════════════
// The client-side sanitizer (sanitizeRichText, rtSafeHref, rtSafeImageSrc,
// looksLikeHTML) is the shared, DOMPurify-backed richtext.js module — see
// octbase-shared/richtext.js. Only the esc()-dependent display helper lives
// here.

// renderDescriptionHTML returns sanitized HTML for display. Legacy plain-text
// descriptions (no HTML tags) are escaped and newline-preserved so existing
// tasks render correctly with no data migration.
function renderDescriptionHTML(desc) {
  if (!desc) return '';
  if (looksLikeHTML(desc)) return sanitizeRichText(desc);
  // Plain text: escape and convert newlines to <br> for readable display.
  return esc(desc).replace(/\n/g, '<br>');
}

// renderedDescriptionOriginal is renderDescriptionHTML over the SAVED description
// of a task (S.taskDescriptionOriginals, set when the details tab renders),
// memoized. The description editor's dirty check compares the editor against this
// rendered form, and it used to re-render it from scratch on every keystroke — a
// full DOMPurify parse and tree walk of a document that by definition had not
// changed.
//
// Memoized on the source string, not merely stored: a save (or a reopened panel)
// replaces the original, and the next call notices the source differs and
// re-renders. So this cannot serve a stale comparison. The map holds one entry per
// task whose details tab has been opened, alongside S.taskDescriptionOriginals
// itself, and is dropped with it when the panel data is.
const _renderedOriginals = new Map();
function renderedDescriptionOriginal(taskId) {
  const src = S.taskDescriptionOriginals[taskId] || '';
  const hit = _renderedOriginals.get(taskId);
  if (hit && hit.src === src) return hit.html;
  const html = renderDescriptionHTML(src);
  _renderedOriginals.set(taskId, { src, html });
  return html;
}
function taskLabel(t) {
  if (!t) return '';
  const p = S.project;
  const prefix = p ? (p.abbreviation || p.slug.toUpperCase()) : '';
  const seq = t.seqNumber != null ? `${prefix}-${t.seqNumber}` : '';
  return seq ? `${seq} ${t.title}` : t.title;
}
function slugify(s) {
  return (s||'').toLowerCase().replace(/[^a-z0-9]+/g,'-').replace(/^-|-$/g,'');
}

// ═══════════════════════════════════════════════════════════
// HTML TEMPLATING — auto-escaping tagged template
// ═══════════════════════════════════════════════════════════
// html`...${value}...` HTML-escapes every interpolated value by default, so
// template literals can no longer inject markup from user/server data by
// accident. Trusted sub-fragments (badges, nested templates) must be wrapped
// in raw() to opt out of escaping. Arrays are joined. Null/undefined render
// as ''.
function raw(s) { return { __raw: s == null ? '' : String(s) }; }
function html(strings, ...values) {
  let out = strings[0];
  for (let i = 0; i < values.length; i++) {
    let v = values[i];
    if (Array.isArray(v)) v = v.map(x => (x && x.__raw !== undefined) ? x.__raw : esc(x)).join('');
    else if (v && v.__raw !== undefined) v = v.__raw;
    else v = esc(v == null ? '' : v);
    out += v + strings[i + 1];
  }
  return out;
}

// ═══════════════════════════════════════════════════════════
// DEBOUNCED CALLBACKS
// ═══════════════════════════════════════════════════════════
// debounced(ms, fn) returns a function that coalesces a burst of calls into one
// trailing call — the shape debouncePalette (below) and schedulePreview
// (views-content.js) already use by hand, factored out so per-keystroke work
// (search re-render + history.replaceState, the description dirty check) has one
// idiom instead of three.
//
// S.pendingDebounces counts callbacks scheduled but not yet run. It is the only
// way an outside observer can tell "the app is still going to do something" from
// "the app is idle": the e2e suite's settle() waits on it, so a test that types
// into a search box asserts against the coalesced result instead of racing the
// timer with a sleep.
function debounced(ms, fn) {
  let timer = null;
  return (...args) => {
    if (timer !== null) { clearTimeout(timer); S.pendingDebounces--; }
    S.pendingDebounces++;
    timer = setTimeout(() => {
      timer = null;
      S.pendingDebounces = Math.max(0, S.pendingDebounces - 1);
      fn(...args);
    }, ms);
  };
}


function initDelegation() {
  document.addEventListener('click',   e => {
    // Anchors with a placeholder href acting as buttons must not change the
    // URL/hash (this is a hash-routed SPA).
    const a = e.target.closest('a[href="#"][data-act]');
    if (a) e.preventDefault();
    _dispatch(ACTIONS, 'act', e);
  });
  document.addEventListener('change',  e => _dispatch(CHANGES,  'change',  e));
  document.addEventListener('input',   e => _dispatch(INPUTS,   'input',   e));
  document.addEventListener('keydown', e => _dispatch(KEYDOWNS, 'keydown', e));
  document.addEventListener('submit',  e => _dispatch(SUBMITS,  'submit',  e));

  // Drag & drop (board). Cards carry data-drag-card="<taskId>"; columns carry
  // data-drop-col="<columnName>". dragover/drop preventDefault to allow drops.
  document.addEventListener('dragstart', e => {
    const card = e.target.closest('[data-drag-card]');
    if (!card) return;
    S.dragging = card.dataset.dragCard;
    card.classList.add('dragging');
    // The lane geometry dragover binary-searches is measured once per drag, so a
    // new drag must not inherit the last one's snapshot (see views-board.js).
    invalidateDragGeom();
  });
  document.addEventListener('dragend', e => {
    const card = e.target.closest('[data-drag-card]');
    if (!card) return;
    S.dragging = null;
    card.classList.remove('dragging');
    clearDropIndicators();
    invalidateDragGeom();
  });
  document.addEventListener('dragover', e => {
    const col = e.target.closest('[data-drop-col]');
    if (!col) return;
    boardDragOver(e, col.dataset.dropCol);
  });
  document.addEventListener('dragleave', e => {
    if (e.target.closest('[data-drop-col]')) boardDragLeave(e);
  });
  document.addEventListener('drop', e => {
    const col = e.target.closest('[data-drop-col]');
    if (!col) return;
    dropOnColumn(e, col.dataset.dropCol);
  });

  // File drag-and-drop onto any rich-text editor or attachment drop zone, and
  // paste of images into an editor. Every editor takes files, not just the
  // description: dropping a screenshot into the comment box is the same gesture
  // and used to do nothing at all.
  const RT_DROP_ZONES = '[data-drop-attach], .rt-editor[contenteditable="true"]';

  // The task id rides on the description editor (data-a0) but not on the comment
  // composers, which only ever exist inside the open panel — so that is where
  // they read it from.
  function dropTargetTaskId(target) {
    const zone = target.closest && target.closest('[data-drop-attach]');
    if (zone) return zone.dataset.dropAttach;
    const editor = target.closest && target.closest('.rt-editor[contenteditable="true"]');
    if (editor) return editor.dataset.a0 || S.taskPanelData?.taskId || S.taskPanelId;
    return null;
  }
  // Where an uploaded image should be inserted: the editor the files landed on.
  // A drop on the attachment sidebar has no editor and returns '', which leaves
  // rtUploadFiles to fall back to the description.
  function dropTargetEditor(target) {
    const editor = target.closest && target.closest('.rt-editor[contenteditable="true"]');
    if (!editor) return '';
    if (editor.id) return '#' + editor.id;
    return editor.dataset.replyEditor ? `[data-reply-editor="${editor.dataset.replyEditor}"]` : '';
  }
  document.addEventListener('dragover', e => {
    if (dropTargetTaskId(e.target) && e.dataTransfer && Array.from(e.dataTransfer.types || []).includes('Files')) {
      e.preventDefault();
      const zone = e.target.closest(RT_DROP_ZONES);
      if (zone) zone.classList.add('rt-drop-active');
    }
  });
  document.addEventListener('dragleave', e => {
    const zone = e.target.closest && e.target.closest(RT_DROP_ZONES);
    if (zone) zone.classList.remove('rt-drop-active');
  });
  document.addEventListener('drop', e => {
    const taskId = dropTargetTaskId(e.target);
    if (!taskId || !e.dataTransfer || !e.dataTransfer.files || !e.dataTransfer.files.length) return;
    e.preventDefault();
    const zone = e.target.closest(RT_DROP_ZONES);
    if (zone) zone.classList.remove('rt-drop-active');
    rtUploadFiles(taskId, Array.from(e.dataTransfer.files), dropTargetEditor(e.target));
  });
  document.addEventListener('paste', e => {
    const editor = e.target.closest && e.target.closest('.rt-editor[contenteditable="true"]');
    if (!editor || !e.clipboardData) return;
    const files = Array.from(e.clipboardData.files || []);
    const images = files.filter(f => (f.type || '').startsWith('image/'));
    if (images.length) {
      e.preventDefault();
      rtUploadFiles(dropTargetTaskId(e.target), images, dropTargetEditor(e.target));
    }
  });

  // Global lightbox keyboard navigation (Arrow keys + Esc) when it is open.
  document.addEventListener('keydown', e => {
    const lb = el('#lightbox');
    if (!lb || lb.classList.contains('hidden')) return;
    if (e.key === 'Escape') { e.preventDefault(); closeLightbox(); }
    else if (e.key === 'ArrowLeft') { e.preventDefault(); lightboxPrev(); }
    else if (e.key === 'ArrowRight') { e.preventDefault(); lightboxNext(); }
  });

  // Reconcile JS-managed layout state when the viewport crosses a breakpoint
  // (e.g. a phone/tablet rotation). The off-canvas drawer + scrim are toggled
  // via classes that CSS only honours below 1024px; without this, rotating
  // from a drawer width into the expanded (>=1024px) layout would leave the
  // full-screen scrim (#sidebar-overlay) stuck over the whole UI, forcing a
  // reload. Layout itself is handled by the media queries on resize.
  let _resizeRaf = null;
  window.addEventListener('resize', () => {
    if (_resizeRaf) return;
    _resizeRaf = requestAnimationFrame(() => {
      _resizeRaf = null;
      if (window.innerWidth >= 1024) closeSidebar();
    });
  });

  // Avatar images are rendered as empty <img> placeholders (avatarHtml) and
  // filled in after each render. One debounced observer over the app root
  // hydrates any new avatar the views produce, so no view has to remember to
  // call hydrateAvatars itself.
  let _avatarRaf = null;
  const scheduleHydrate = () => {
    if (_avatarRaf) return;
    _avatarRaf = requestAnimationFrame(() => {
      _avatarRaf = null;
      hydrateAvatars(document);
    });
  };
  new MutationObserver(scheduleHydrate).observe(document.body, { childList: true, subtree: true });
  scheduleHydrate();
}

// ═══════════════════════════════════════════════════════════
// TOAST
// ═══════════════════════════════════════════════════════════
// ═══════════════════════════════════════════════════════════
// THEME (light / dark / system)
// ═══════════════════════════════════════════════════════════
// The chosen preference is persisted and applied by setting data-theme on
// <html>. The initial application happens via a tiny inline script in index.html
// (before CSS paints, to avoid a flash); these helpers drive the in-app toggle.
const THEME_KEY = 'octbase-theme';
const THEME_ORDER = ['system', 'light', 'dark', 'octopus'];

function getThemePref() {
  const v = localStorage.getItem(THEME_KEY);
  return THEME_ORDER.includes(v) ? v : 'system';
}
function applyTheme(pref) {
  // Every order entry except 'system' maps to an explicit [data-theme]; 'system'
  // leaves it off so the prefers-color-scheme media query decides.
  if (THEME_ORDER.includes(pref) && pref !== 'system') {
    document.documentElement.dataset.theme = pref;
  } else {
    delete document.documentElement.dataset.theme; // system → media query decides
  }
}
function themeToggleLabel() {
  return t('theme.toggle', { mode: t('theme.' + getThemePref()) });
}
// cycleTheme advances system → light → dark → system, persists the choice,
// applies it, and re-renders the topbar so the button's label reflects the state.
function cycleTheme() {
  const next = THEME_ORDER[(THEME_ORDER.indexOf(getThemePref()) + 1) % THEME_ORDER.length];
  localStorage.setItem(THEME_KEY, next);
  applyTheme(next);
  renderTopbar();
  toast(t('theme.changed', { mode: t('theme.' + next) }), 'info');
  // Keep the server-persisted preference in step (fire-and-forget) so the
  // settings page and other devices see the same value.
  if (S.user) api.preferences.update({ language: getLocale(), theme: next }).catch(() => {});
}

function toast(msg, type='info') {
  const t = document.createElement('div');
  t.className = `toast toast-${type}`;
  t.textContent = msg;
  const container = el('#toast-container');
  if (!container) return;
  // Errors are announced immediately (assertive); other messages politely (WCAG 4.1.3 Status Messages).
  container.setAttribute('aria-live', type === 'error' ? 'assertive' : 'polite');
  container.appendChild(t);
  setTimeout(()=>t.remove(), 3500);
}

// ═══════════════════════════════════════════════════════════
// MODAL
// ═══════════════════════════════════════════════════════════
let _modalReturnFocus = null;
let _modalKeydownHandler = null;
let _modalBackdropHandler = null;
// Stack of parent modals for cascaded popups — a dialog opened *over* an
// already-open one (e.g. a delete confirmation over the project-members modal).
// hideModal() pops back to the parent instead of closing the backdrop, so a
// cascaded popup returns to where it was opened. Cascading is opt-in per call
// via showModal's `opts.cascade` (confirmDelete uses it); an optional
// `opts.reopen` callback rebuilds the parent with fresh data instead of
// restoring its (possibly stale) snapshot.
const _modalStack = [];
// The open modal's `opts.onClose`, run once the dialog is really gone — via
// Cancel, Escape, the backdrop, submit, or being replaced. Work a dialog wants
// to defer until it is off the screen (typically a repaint of the view behind
// it, which would otherwise flash under the open modal) belongs here rather
// than in the handlers that mutate state while the dialog is still up.
let _modalOnClose = null;

// fieldMap maps backend field names (ApiError.apiField, from
// ErrorResponse.details.field) to the DOM ids of this modal's inputs, so
// server-side validation errors can be shown next to the right field
// (WCAG 3.3.1 Error Identification).
// opts: { cascade, reopen, onClose } — see _modalStack and _modalOnClose above.
function showModal(titleStr, bodyHtml, onSubmit, submitLabel=null, fieldMap={}, opts={}) {
  if (submitLabel == null) submitLabel = t('form.save');
  const bd = el('#modal-backdrop');
  const m  = el('#modal');
  if (!bd || !m) return;
  const alreadyOpen = !bd.classList.contains('hidden');
  // Detach the previous modal's listeners before attaching new ones whenever a
  // modal is already open — whether or not this call cascades — so a
  // non-cascading open never leaves the old keydown/backdrop listeners live.
  if (alreadyOpen) {
    if (_modalKeydownHandler) m.removeEventListener('keydown', _modalKeydownHandler);
    if (_modalBackdropHandler) bd.removeEventListener('click', _modalBackdropHandler);
  }
  // Cascade: if a modal is already open and this one should stack over it,
  // snapshot the current modal so hideModal() can return to it.
  const prevOnClose = alreadyOpen ? _modalOnClose : null;
  _modalOnClose = opts.onClose || null;
  if (opts.cascade && alreadyOpen) {
    _modalStack.push({
      html: m.innerHTML,
      className: m.className,
      submitOnClick: el('#modal-submit')?.onclick || null,
      keydownHandler: _modalKeydownHandler,
      returnFocus: _modalReturnFocus,
      reopen: opts.reopen || null,
      onClose: prevOnClose,
    });
  } else if (prevOnClose) {
    // Replaced outright rather than stacked over: that modal is gone, so its
    // close hook is due now — nothing will pop back to it.
    prevOnClose();
  }
  m.className = 'modal';
  m.setAttribute('role', 'dialog');
  m.setAttribute('aria-modal', 'true');
  m.setAttribute('aria-labelledby', 'modal-title');
  m.setAttribute('tabindex', '-1');
  m.innerHTML = `
    <div class="modal-title" id="modal-title">${titleStr}</div>
    <div id="modal-body">${bodyHtml}</div>
    <div class="modal-footer">
      <button class="btn btn-secondary" data-act="hideModal">${t('form.cancel')}</button>
      <button class="btn btn-primary" id="modal-submit">${submitLabel}</button>
    </div>`;
  bd.classList.remove('hidden');
  el('#modal-submit').onclick = async () => {
    try {
      // The resolved return value of onSubmit sequences the teardown:
      //   false        → keep the modal open (the handler owns what happens next);
      //   a function   → close the modal, THEN run it — for re-renders that must
      //                  not be stepped on by the modal teardown;
      //   anything else→ just close the modal (the common case).
      const after = await onSubmit();
      if (after === false) return;
      hideModal();
      if (typeof after === 'function') after();
    }
    catch(e) {
      // Field-level validation errors — either thrown locally as
      // {field:'task-title', message:'...'} or returned by the API as
      // ErrorResponse.details.field and mapped via fieldMap — are shown
      // next to the relevant input (WCAG 3.3.1, 3.3.2) instead of only as
      // a toast, and focus moves to the invalid field.
      const fieldId = e.field || (e.apiField && fieldMap[e.apiField]);
      if (fieldId && el('#'+fieldId)) setModalFieldError(fieldId, apiErrorMessage(e));
      else toast(apiErrorMessage(e),'error');
    }
  };
  _modalBackdropHandler = e => { if (e.target === bd) hideModal(); };
  bd.addEventListener('click', _modalBackdropHandler);

  // Focus management (WCAG 2.4.3 Focus Order, 2.1.2 No Keyboard Trap):
  // remember the element that opened the modal, move focus into the dialog,
  // and trap Tab navigation within it until the modal is closed.
  _modalReturnFocus = document.activeElement;
  const focusable = m.querySelectorAll('input, textarea, select, button, a[href], [tabindex]:not([tabindex="-1"])');
  (focusable[0] || m).focus();

  _modalKeydownHandler = (e) => {
    if (e.key !== 'Tab') return;
    const items = Array.from(m.querySelectorAll('input, textarea, select, button, a[href], [tabindex]:not([tabindex="-1"])'))
      .filter(elm => !elm.disabled && elm.offsetParent !== null);
    if (items.length === 0) return;
    const first = items[0], last = items[items.length-1];
    if (e.shiftKey && document.activeElement === first) {
      e.preventDefault(); last.focus();
    } else if (!e.shiftKey && document.activeElement === last) {
      e.preventDefault(); first.focus();
    }
  };
  m.addEventListener('keydown', _modalKeydownHandler);
}
// Shows an inline, field-associated validation error inside the open modal
// and moves focus to the field (WCAG 3.3.1 Error Identification, 3.3.2
// Labels or Instructions, 2.4.3 Focus Order).
function setModalFieldError(fieldId, message) {
  const field = el('#'+fieldId);
  if (!field) { toast(message, 'error'); return; }
  const errorId = fieldId+'-error';
  let err = el('#'+errorId);
  if (!err) {
    err = document.createElement('div');
    err.id = errorId;
    err.className = 'form-error';
    err.setAttribute('role', 'alert');
    field.insertAdjacentElement('afterend', err);
  }
  err.textContent = message;
  field.setAttribute('aria-invalid', 'true');
  const describedBy = (field.getAttribute('aria-describedby')||'').split(' ').filter(x=>x && x!==errorId);
  describedBy.push(errorId);
  field.setAttribute('aria-describedby', describedBy.join(' '));
  field.focus();
}
function hideModal() {
  const bd = el('#modal-backdrop');
  const m = el('#modal');
  // Escape calls hideModal() unconditionally, so only a dialog that was
  // actually open has a close hook to run. It fires after teardown, never
  // while the modal is still on screen.
  const onClose = bd && !bd.classList.contains('hidden') ? _modalOnClose : null;
  _modalOnClose = null;
  // Tear down the current (top) modal's listeners.
  if (m && _modalKeydownHandler) m.removeEventListener('keydown', _modalKeydownHandler);
  if (bd && _modalBackdropHandler) bd.removeEventListener('click', _modalBackdropHandler);
  _modalKeydownHandler = null;
  _modalBackdropHandler = null;

  // Cascaded popup: return to the parent modal instead of closing the backdrop.
  if (_modalStack.length) {
    const prev = _modalStack.pop();
    if (prev.reopen) {
      // Rebuild the parent with fresh data — it re-renders itself via showModal
      // (which installs the rebuilt parent's own close hook).
      prev.reopen();
      if (onClose) onClose();
      return;
    }
    _modalOnClose = prev.onClose || null;
    if (m) {
      m.innerHTML = prev.html;
      m.className = prev.className;
      const sub = el('#modal-submit');
      if (sub) sub.onclick = prev.submitOnClick;
      _modalKeydownHandler = prev.keydownHandler;
      if (_modalKeydownHandler) m.addEventListener('keydown', _modalKeydownHandler);
      _modalBackdropHandler = e => { if (e.target === bd) hideModal(); };
      if (bd) bd.addEventListener('click', _modalBackdropHandler);
      _modalReturnFocus = prev.returnFocus;
      const focusable = m.querySelectorAll('input, textarea, select, button, a[href], [tabindex]:not([tabindex="-1"])');
      (focusable[0] || m).focus();
    }
    if (onClose) onClose();
    return;
  }

  if (bd) bd.classList.add('hidden');
  if (_modalReturnFocus && document.body.contains(_modalReturnFocus)) {
    _modalReturnFocus.focus();
  }
  _modalReturnFocus = null;
  if (onClose) onClose();
}
// Cascading is enabled only when a `reopen` callback is supplied, so the
// confirmation returns to (and refreshes) the modal it was opened from. Plain
// confirmDelete calls — not opened over another modal — close as before.
function confirmDelete(title, body, onConfirm, reopen=null, confirmLabel=null) {
  showModal(title, `<p>${body}</p>`, onConfirm, confirmLabel || t('form.delete'), {}, { cascade: !!reopen, reopen });
  const sub = el('#modal-submit');
  if (sub) sub.className = 'btn btn-danger';
}

// confirmModal is the non-destructive counterpart to confirmDelete: a
// Promise-based yes/no dialog (resolves true on submit, false on cancel/
// backdrop click) for call sites that need to `await` the user's answer
// before continuing — e.g. a loop that must not show the next confirmation
// until the current one is answered. Native `confirm()` blocks synchronously
// and can't do that inside an async flow without freezing the whole page.
function confirmModal(title, body, confirmLabel) {
  return new Promise(resolve => {
    let answered = false;
    showModal(title, `<p>${body}</p>`, () => { answered = true; resolve(true); }, confirmLabel);
    const bd = el('#modal-backdrop');
    if (!bd) { resolve(false); return; }
    const observer = new MutationObserver(() => {
      if (bd.classList.contains('hidden')) {
        observer.disconnect();
        if (!answered) resolve(false);
      }
    });
    observer.observe(bd, { attributes: true, attributeFilter: ['class'] });
  });
}

// promptModal is a Promise-based single-text-field dialog (resolves to the
// trimmed input value, or null on cancel/backdrop close) — the async,
// app-styled equivalent of window.prompt(), which blocks synchronously and
// would look out of place next to this app's custom modals.
function promptModal(title, label, submitLabel) {
  return new Promise(resolve => {
    let answered = false;
    showModal(title, `
      <div class="form-group">
        <label class="form-label" for="prompt-modal-input">${label}</label>
        <input class="form-input" id="prompt-modal-input" autofocus>
      </div>`,
      () => { answered = true; resolve((el('#prompt-modal-input')?.value || '').trim()); },
      submitLabel);
    const bd = el('#modal-backdrop');
    if (!bd) { resolve(null); return; }
    const observer = new MutationObserver(() => {
      if (bd.classList.contains('hidden')) {
        observer.disconnect();
        if (!answered) resolve(null);
      }
    });
    observer.observe(bd, { attributes: true, attributeFilter: ['class'] });
  });
}

// ═══════════════════════════════════════════════════════════
// COMMAND PALETTE (Ctrl+K / Cmd+K)
// ═══════════════════════════════════════════════════════════
let _paletteSelectedIdx = -1;
let _paletteResults = [];

function openPalette() {
  const overlay = el('#palette-overlay');
  if (!overlay) return;
  overlay.innerHTML = `
    <div class="palette-box" role="dialog" aria-modal="true" aria-label="${t('palette.quickSearch')}">
      <label class="sr-only" for="palette-input">${t('palette.searchPlaceholder')}</label>
      <input class="palette-input" id="palette-input" placeholder="${t('palette.searchPlaceholderEllipsis')}" autocomplete="off" aria-label="${t('palette.searchPlaceholder')}" aria-controls="palette-results" aria-autocomplete="list">
      <div id="palette-results" class="palette-results" role="listbox" aria-label="${t('search.searchButton')}"></div>
    </div>`;
  overlay.classList.remove('hidden');
  const input = el('#palette-input');
  input.focus();
  input.addEventListener('input', debouncePalette(250));
  input.addEventListener('keydown', paletteKeyDown);
  // The input above is rebuilt by the innerHTML write, so its listeners go with
  // it — but #palette-overlay is part of the app shell and outlives every open.
  // Binding it per open leaked one closure per Ctrl+K (and dispatched the same
  // close N times). Marked on the node rather than in a module flag so a shell
  // re-render, which replaces the overlay, still gets its listener.
  if (!overlay.dataset.closeBound) {
    overlay.dataset.closeBound = '1';
    overlay.addEventListener('click', e => {
      if (e.target === overlay) closePalette();
    });
  }
}

function closePalette() {
  const overlay = el('#palette-overlay');
  if (overlay) overlay.classList.add('hidden');
}

function debouncePalette(ms) {
  let t;
  return (e) => {
    clearTimeout(t);
    t = setTimeout(() => runPaletteSearch(e.target.value), ms);
  };
}

async function runPaletteSearch(q) {
  if (q.length < 2) {
    el('#palette-results').innerHTML = '';
    return;
  }
  try {
    const pid = S.project ? S.project.id : null;
    const result = await api.search(q, pid);
    _paletteResults = [];
    let html = '';

    if (result.tasks && result.tasks.length) {
      html += `<div class="palette-group-label" role="presentation">${t('nav.tasks')}</div>`;
      result.tasks.forEach(t => {
        _paletteResults.push({type:'task', id:t.id, projectId:t.projectId || S.project?.id || null});
        html += `<div class="palette-item" id="palette-item-${_paletteResults.length-1}" role="option" aria-selected="false" data-idx="${_paletteResults.length-1}">
          ${statusBadge(t.status)} ${esc(t.title)}
          <span class="palette-meta">${esc(t.projectName)}</span>
        </div>`;
      });
    }
    if (result.pages && result.pages.length) {
      html += `<div class="palette-group-label" role="presentation">${t('nav.pages')}</div>`;
      result.pages.forEach(p => {
        _paletteResults.push({type:'page', id:p.id, slug:p.slug, projectId:p.projectId});
        html += `<div class="palette-item" id="palette-item-${_paletteResults.length-1}" role="option" aria-selected="false" data-idx="${_paletteResults.length-1}">
          ${icon('page',{size:'sm'})} ${esc(p.title)} <span class="palette-meta">${esc(p.projectName)}</span>
        </div>`;
      });
    }
    if (result.projects && result.projects.length) {
      html += `<div class="palette-group-label" role="presentation">${t('nav.projects')}</div>`;
      result.projects.forEach(p => {
        _paletteResults.push({type:'project', id:p.id, slug:p.slug});
        html += `<div class="palette-item" id="palette-item-${_paletteResults.length-1}" role="option" aria-selected="false" data-idx="${_paletteResults.length-1}">
          ${icon('project',{size:'sm'})} ${esc(p.name)}
        </div>`;
      });
    }
    if (!html) html = `<div class="palette-empty" role="status">${t('palette.noResults')}</div>`;

    const container = el('#palette-results');
    container.innerHTML = html;
    container.querySelectorAll('.palette-item').forEach(item => {
      item.addEventListener('click', () => paletteSelect(parseInt(item.dataset.idx)));
    });
    _paletteSelectedIdx = -1;
  } catch {}
}

function paletteKeyDown(e) {
  const items = el('#palette-results')?.querySelectorAll('.palette-item') || [];
  if (e.key === 'ArrowDown') {
    e.preventDefault();
    _paletteSelectedIdx = Math.min(_paletteSelectedIdx + 1, items.length - 1);
  } else if (e.key === 'ArrowUp') {
    e.preventDefault();
    _paletteSelectedIdx = Math.max(_paletteSelectedIdx - 1, -1);
  } else if (e.key === 'Enter' && _paletteSelectedIdx >= 0) {
    e.preventDefault();
    paletteSelect(_paletteSelectedIdx);
    return;
  } else if (e.key === 'Escape') {
    closePalette();
    return;
  }
  const input = el('#palette-input');
  items.forEach((item, i) => {
    const selected = i === _paletteSelectedIdx;
    item.classList.toggle('palette-selected', selected);
    item.setAttribute('aria-selected', selected ? 'true' : 'false');
  });
  if (input) input.setAttribute('aria-activedescendant', _paletteSelectedIdx >= 0 ? `palette-item-${_paletteSelectedIdx}` : '');
}

function paletteSelect(idx) {
  const r = _paletteResults[idx];
  if (!r) return;
  closePalette();
  if (r.type === 'task') {
    openProjectTask(r.id, r.projectId);
  } else if (r.type === 'page') {
    openProjectPage(r.projectId, r.id);
  } else if (r.type === 'project') {
    router.go(`/projects/${r.id}/board`);
  }
}

// ═══════════════════════════════════════════════════════════
// KEYBOARD SHORTCUTS
// ═══════════════════════════════════════════════════════════
function isTypingTarget(e) {
  const tag = e.target.tagName;
  return tag === 'INPUT' || tag === 'TEXTAREA' || e.target.isContentEditable;
}

document.addEventListener('keydown', (e) => {
  const ctrl = e.ctrlKey || e.metaKey;
  if (ctrl && e.key === 'k') { e.preventDefault(); openPalette(); return; }
  if (e.key === 'Escape') {
    // Escape during a task-title edit means discard: revert the input before
    // closeTaskPanel() restores focus — that focus move blurs the input,
    // which would otherwise fire change and commit the pending value.
    const active = document.activeElement;
    if (active && active.id === 'panel-title-input') active.value = active.defaultValue;
    closePalette();
    hideShortcutHelp();
    hideModal();
    closeTaskPanel();
    closeSidebar();
    return;
  }
  if (isTypingTarget(e)) return;

  switch (e.key) {
    case 'n': case 'N':
      if (S.project && (S.view === 'board' || S.view === 'backlog')) {
        e.preventDefault();
        showInlineTaskCreate();
      } else if (S.project) {
        e.preventDefault();
        showCreateTask();
      }
      break;
    case 'b': case 'B': if (S.project) setView('board'); break;
    case 'l': case 'L': if (S.project) setView('backlog'); break;
    case 's': case 'S': if (S.project) setView('sprints'); break;
    case 'r': case 'R': if (S.project) setView('releases'); break;
    case 'p': case 'P': if (S.project) setView('pages'); break;
    case '?': showShortcutHelp(); break;
    case 'e': case 'E':
      if (S.taskPanelId) {
        const titleEl = el('#panel-title-input');
        if (titleEl) titleEl.focus();
      }
      break;
    case 'a': case 'A':
      if (S.taskPanelId && S.user) {
        const assignee = el('#task-assignee');
        if (assignee) assignee.value = S.user.id;
        api.tasks.assign(S.taskPanelId, { assigneeId: S.user.id, version: S.taskPanelData?.task?.id === S.taskPanelId ? S.taskPanelData.task.version : undefined })
          .then(() => { toast(t('notifications.assignedToYou'), 'success'); })
          .catch(e => toast(apiErrorMessage(e), 'error'));
      }
      break;
  }
});

function showShortcutHelp() {
  const h = el('#shortcut-help');
  if (!h) return;
  h.innerHTML = `
    <div class="shortcut-overlay">
      <div class="shortcut-box">
        <div class="shortcut-title">${t('shortcuts.title')}</div>
        <div class="shortcut-grid">
          <kbd>N</kbd><span>${t('shortcuts.newTask')}</span>
          <kbd>B</kbd><span>${t('shortcuts.boardView')}</span>
          <kbd>L</kbd><span>${t('shortcuts.backlogView')}</span>
          <kbd>S</kbd><span>${t('shortcuts.sprintsView')}</span>
          <kbd>R</kbd><span>${t('shortcuts.releasesView')}</span>
          <kbd>P</kbd><span>${t('shortcuts.pagesView')}</span>
          <kbd>Ctrl+K</kbd><span>${t('shortcuts.commandPalette')}</span>
          <kbd>Esc</kbd><span>${t('shortcuts.closePanel')}</span>
          <kbd>E</kbd><span>${t('shortcuts.editTaskTitle')}</span>
          <kbd>A</kbd><span>${t('shortcuts.assignToMe')}</span>
          <kbd>?</kbd><span>${t('shortcuts.thisHelp')}</span>
        </div>
        <button class="btn btn-secondary btn-sm mt-2" data-act="hideShortcutHelp">${t('form.close')}</button>
      </div>
    </div>`;
  h.classList.remove('hidden');
}
function hideShortcutHelp() {
  const h = el('#shortcut-help');
  if (h) h.classList.add('hidden');
}

// ── Delegation registration: this file's handlers ───────────────────────────
// (see js/README.md "Delegation registration".)
registerActions([
  closeSidebar, cycleTheme, hideModal, hideShortcutHelp, toggleSidebar, mfaBackToLogin,
], _A0);
registerActions([switchAuthLocale], _A1);
registerActions({
  stop:          () => {},
  focusContent:  () => { const c = document.getElementById('content'); if (c) c.focus(); },
  // auth.js and router.js load before this file, so they cannot call the
  // registration API at load time. Their two delegated handlers are one-line
  // facades over core services and live here instead.
  logout:        () => Auth.logout(),
  nav:           el => router.go(el.dataset.a0),
});
registerChanges({
  // The auth pages' language picker; `changeLocale` belongs to views-crud.js,
  // which is loaded later — resolved inside the handler body, not at load time.
  langSelect: node => { ({ changeLocale, switchAuthLocale })[node.dataset.a0]?.(node.value); },
});
registerKeydowns({
  // Keyboard equivalent of a click on a non-button element: runs whatever
  // ACTIONS entry the element's data-act names, whichever module registered it.
  activateOnEnter: (el, ev) => {
    if ((ev.key === 'Enter' || ev.key === ' ') && ev.target === el) {
      ev.preventDefault();
      const fn = ACTIONS[el.dataset.act];
      if (fn) fn(el, ev);
    }
  },
});
registerSubmits({
  doLogin:            (el, ev) => doLogin(ev),
  doAcceptInvitation: (el, ev) => doAcceptInvitation(ev, el.dataset.a0),
  doForgotPassword:   (el, ev) => doForgotPassword(ev),
  doResetPassword:    (el, ev) => doResetPassword(ev, el.dataset.a0),
  doVerifyMfaLogin:   (el, ev) => doVerifyMfaLogin(ev, el.dataset.a0),
  // Missing until 37b stage 6, when ESLint's no-unused-vars noticed the
  // function had no callers: renderMfaEnrollStep's form says
  // data-submit="doConfirmMfaEnrollLogin", so without this entry the
  // submit dispatched to nothing and a user forced to enrol in MFA at
  // login could not finish. Every other data-submit name in this file is
  // listed here; this one was dropped in the stage-1 delegation refactor.
  doConfirmMfaEnrollLogin: (el, ev) => doConfirmMfaEnrollLogin(ev),
});

export { THEME_KEY, THEME_ORDER, applyTheme, avatarHtml, closeSidebar, confirmDelete, confirmModal, debounced, el, esc, estimateTag, filterCountLabel, fmtDate, fmtDateTime, getThemePref, h, hideModal, html, initDelegation, initials, memberName, navExpanded, priorityDot, priorityInline, promptModal, raw, releaseName, renderAcceptInvitationPage, renderAppShell, renderDescriptionHTML, renderForgotPasswordPage, renderLangLinks, renderLoginPage, renderMfaChallengeStep, renderResetPasswordPage, renderedDescriptionOriginal, segSwitch, setUserAvatarImage, showModal, slugify, sprintName, statusBadge, taskLabel, themeToggleLabel, toast, typeBadge, userAvatarHtml };
