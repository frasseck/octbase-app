"""Tests for the personal Settings page: language/theme preferences
(server-persisted segmented switches) and TOTP-based MFA enrollment/management.

Closes the gap noted in the consistency review: the Settings/MFA feature
(CHANGELOG `## Unreleased`, internal/dashboard + internal/security/mfa) had
backend unit coverage but no frontend e2e coverage.

MFA tests mutate the shared demo user's MFA state, which every other test
file's login depends on. Every test that enables MFA goes through the
``mfa_cleanup`` fixture, which force-disables MFA via a direct API call in
its teardown (runs even if the test body raises) so a failure here can never
strand the rest of the suite unable to log in.
"""

import base64
import hashlib
import hmac
import struct
import time

import pytest
import requests
from conftest import (API_BASE, API_PREFIX, DEMO_USER_EMAIL, DEMO_USER_PASSWORD,
                      SHORT, TIMEOUT, settle)


def _totp(secret: str, offset: int = 0) -> str:
    """RFC 6238 TOTP code for ``secret`` (base32, no padding required).

    Matches the server's github.com/pquerna/otp/totp defaults (SHA1, 6
    digits, 30s step) — implemented by hand so the test suite doesn't need a
    new pyotp dependency for one helper.
    """
    key = base64.b32decode(secret.upper() + "=" * (-len(secret) % 8))
    counter = int(time.time() // 30) + offset
    digest = hmac.new(key, struct.pack(">Q", counter), hashlib.sha1).digest()
    o = digest[-1] & 0x0F
    code = (struct.unpack(">I", digest[o:o + 4])[0] & 0x7FFFFFFF) % 1_000_000
    return f"{code:06d}"


@pytest.fixture
def settings_page(app):
    """Settings page open, navigated via the topbar user icon (not router.go
    directly, so the nav entry point itself gets exercised)."""
    app.click("[data-act='nav'][data-a0='/settings']")
    app.wait_for_selector("#settings-mfa-section", timeout=TIMEOUT)
    app.wait_for_selector("#settings-prefs-section .seg-switch", timeout=TIMEOUT)
    return app


@pytest.fixture
def reset_preferences(api):
    """Restore language/theme to defaults after a test so later tests (in
    this file or others) don't inherit a switched preference."""
    yield
    try:
        api.patch("/api/users/me/preferences",
                  {"language": "en", "theme": "system", "terminology": "AGILE"})
    except Exception:
        pass


@pytest.fixture
def mfa_cleanup(api):
    """Guarantee MFA ends up disabled on the demo user, regardless of how the
    test body exits. Password-based re-auth works even if enrollment was left
    half-finished or a generated code already expired."""
    yield
    try:
        me = api.get("/api/auth/me")
        if me.get("mfaEnabled"):
            api.post("/api/users/me/mfa/disable", {"password": DEMO_USER_PASSWORD, "code": ""})
    except Exception:
        pass


class TestSettingsNavigation:
    def test_reachable_via_topbar_user_icon(self, app):
        app.click("[data-act='nav'][data-a0='/settings']")
        app.wait_for_selector(".admin-title", timeout=TIMEOUT)
        assert app.inner_text(".admin-title").strip() != ""

    def test_page_uses_grid_2col_layout(self, settings_page):
        assert settings_page.query_selector(".admin-panel .grid-2col") is not None

    def test_four_sections_present(self, settings_page):
        for sel in ("#settings-mfa-section", "#settings-password-section",
                    "#settings-prefs-section", "#settings-notifications-section"):
            assert settings_page.is_visible(sel), f"{sel} not visible"

    def test_no_raw_i18n_keys_leak(self, settings_page):
        text = settings_page.inner_text(".admin-panel")
        # A leaked i18n key reads as a dotted.path.like.this token.
        assert "settings." not in text
        assert "form." not in text


NEW_PASSWORD = "Str0ng-Temp-Passw0rd!"


def _demo_password_is_seeded() -> bool:
    return requests.post(
        f"{API_BASE}{API_PREFIX}/auth/login",
        json={"email": DEMO_USER_EMAIL, "password": DEMO_USER_PASSWORD},
    ).status_code == 200


class TestSettingsPassword:
    """POST /auth/change-password shipped in 1.0.8 with no UI at all; this is
    the form's coverage.

    The wrong-current-password case is the one that matters most. That endpoint
    answers **401**, and the SPA's http client treats a 401 on an authenticated
    route as a dead session — so before this was handled, mistyping your current
    password refreshed, retried, failed again and signed you out. The assertion
    that you are still on Settings afterwards is the regression guard.

    **The success path is deliberately not exercised here, and cannot be.** A
    test that changes the password cannot change it back: the seeded
    ``demopass1234`` is 8 characters and ``shared.ValidatePassword`` requires 12, so
    the restore is refused with 422 and the demo login is dead for every
    remaining test in the run. That is not hypothetical — it stranded a whole
    suite run while this was being written. The success path is covered by the
    backend's own tests; what only the browser can show, and what is covered
    above, is that a failure stays inline instead of ending the session."""

    def test_form_renders_with_three_inputs(self, settings_page):
        for sel in ("#settings-password-current", "#settings-password-new",
                    "#settings-password-confirm"):
            assert settings_page.is_visible(sel), f"{sel} not visible"

    def test_mismatched_confirmation_never_reaches_the_api(self, settings_page):
        settings_page.fill("#settings-password-current", DEMO_USER_PASSWORD)
        settings_page.fill("#settings-password-new", NEW_PASSWORD)
        settings_page.fill("#settings-password-confirm", NEW_PASSWORD + "x")
        settings_page.click("#settings-password-section button[type=submit]")
        settings_page.wait_for_selector("#settings-password-error:not([hidden])", timeout=TIMEOUT)
        assert settings_page.inner_text("#settings-password-error").strip() != ""
        # Purely client-side, so the confirm field is the one flagged.
        assert settings_page.get_attribute("#settings-password-confirm", "aria-invalid") == "true"

    def test_wrong_current_password_shows_inline_error_and_keeps_the_session(self, settings_page):
        settings_page.fill("#settings-password-current", "definitely-not-the-password")
        settings_page.fill("#settings-password-new", NEW_PASSWORD)
        settings_page.fill("#settings-password-confirm", NEW_PASSWORD)
        settings_page.click("#settings-password-section button[type=submit]")
        settings_page.wait_for_selector("#settings-password-error:not([hidden])", timeout=TIMEOUT)
        assert settings_page.inner_text("#settings-password-error").strip() != ""
        # The regression guard: a 401 from this route must not be read as an
        # expired session. Still on Settings, not bounced to the login screen.
        assert settings_page.is_visible("#settings-password-section")
        assert settings_page.query_selector("#login-email") is None
        assert settings_page.get_attribute("#settings-password-current", "aria-invalid") == "true"

    def test_policy_violation_is_reported_against_the_new_password(self, settings_page):
        settings_page.fill("#settings-password-current", DEMO_USER_PASSWORD)
        settings_page.fill("#settings-password-new", "short")
        settings_page.fill("#settings-password-confirm", "short")
        settings_page.click("#settings-password-section button[type=submit]")
        settings_page.wait_for_selector("#settings-password-error:not([hidden])", timeout=TIMEOUT)
        assert settings_page.inner_text("#settings-password-error").strip() != ""
        assert settings_page.query_selector("#login-email") is None
        assert settings_page.get_attribute("#settings-password-new", "aria-invalid") == "true"

    def test_the_seeded_demo_password_is_intact(self):
        """Guard for the trap that makes the success path untestable here, and
        an early, legible failure if some other run left the password changed
        (the symptom is otherwise 300+ opaque setup errors across the suite)."""
        assert _demo_password_is_seeded(), (
            "the demo user's password is not the seeded one; every test file's "
            "login depends on it. Reset with scripts/reset_db.sh --yes.")


class TestSettingsPreferences:
    def test_three_labelled_radiogroups(self, settings_page):
        groups = settings_page.query_selector_all("#settings-prefs-section [role='radiogroup']")
        assert len(groups) == 3  # language, theme, vocabulary
        for g in groups:
            assert g.get_attribute("aria-label")

    def test_theme_seg_switch_has_four_options(self, settings_page):
        theme_group = settings_page.locator("#settings-prefs-section .seg-switch").nth(1)
        options = theme_group.locator("button[role='radio']")
        assert options.count() == 4
        values = [options.nth(i).get_attribute("data-a0") for i in range(4)]
        assert values == ["system", "light", "dark", "octopus"]

    def test_switching_theme_updates_html_attribute_and_persists_server_side(
        self, settings_page, api, reset_preferences
    ):
        theme_group = settings_page.locator("#settings-prefs-section .seg-switch").nth(1)
        theme_group.locator("button[data-a0='dark']").click()
        settle(settings_page)
        assert settings_page.evaluate("() => document.documentElement.dataset.theme") == "dark"
        assert theme_group.locator("button[data-a0='dark'][aria-checked='true']").count() == 1

        prefs = api.get("/api/users/me/preferences")
        assert prefs["theme"] == "dark"

    def test_switching_language_updates_locale_and_persists_server_side(
        self, settings_page, api, reset_preferences
    ):
        lang_group = settings_page.locator("#settings-prefs-section .seg-switch").nth(0)
        lang_group.locator("button[data-a0='de']").click()
        settle(settings_page)
        assert settings_page.evaluate("() => document.documentElement.lang") == "de"
        prefs = api.get("/api/users/me/preferences")
        assert prefs["language"] == "de"

    def test_theme_persists_after_reload(self, settings_page, api, reset_preferences):
        theme_group = settings_page.locator("#settings-prefs-section .seg-switch").nth(1)
        theme_group.locator("button[data-a0='octopus']").click()
        settle(settings_page)
        settings_page.reload()
        settings_page.wait_for_selector("#settings-mfa-section", timeout=TIMEOUT)
        assert settings_page.evaluate("() => document.documentElement.dataset.theme") == "octopus"


class TestSettingsMFA:
    def test_disabled_state_shows_enable_button(self, settings_page):
        assert settings_page.is_visible("#settings-mfa-section button[data-act='startMfaEnrollment']")

    def test_full_enroll_confirm_recovery_codes_disable_cycle(self, settings_page, api, mfa_cleanup):
        # Enroll requires re-auth: a password modal gates the QR/secret step.
        settings_page.click("#settings-mfa-section button[data-act='startMfaEnrollment']")
        settings_page.wait_for_selector("#mfa-enroll-password", timeout=TIMEOUT)
        settings_page.fill("#mfa-enroll-password", DEMO_USER_PASSWORD)
        settings_page.click("#modal-submit")
        # QR + manual setup key appear alongside a pending secret.
        settings_page.wait_for_selector("#mfa-secret-value", timeout=TIMEOUT)
        assert settings_page.is_visible(".mfa-qr")
        secret = settings_page.inner_text("#mfa-secret-value").strip()
        assert secret

        # Confirm with a valid TOTP code generated from the same secret shown in the UI.
        settings_page.fill("#mfa-confirm-code", _totp(secret))
        settings_page.click("#settings-mfa-section button[type='submit']")

        # Recovery codes are shown exactly once, in a modal.
        settings_page.wait_for_selector(".recovery-codes-list li", timeout=TIMEOUT)
        codes = settings_page.eval_on_selector_all(
            ".recovery-codes-list code", "els => els.map(e => e.textContent)"
        )
        assert len(codes) >= 5
        settings_page.click("#modal-submit")
        settle(settings_page)
        # Enabled state: status line + Regenerate/Disable actions, confirmed server-side too.
        settings_page.wait_for_selector(".mfa-status--enabled", timeout=TIMEOUT)
        assert settings_page.is_visible("#settings-mfa-section button[data-act='openMfaRegenerateModal']")
        assert api.get("/api/auth/me")["mfaEnabled"] is True

        # Disable requires re-auth — there is no bare toggle, only this password/code-gated modal.
        settings_page.click("#settings-mfa-section button[data-act='openMfaDisableModal']")
        settings_page.wait_for_selector("#mfa-reauth-password", timeout=TIMEOUT)
        settings_page.fill("#mfa-reauth-password", DEMO_USER_PASSWORD)
        settings_page.click("#modal-submit")
        settings_page.wait_for_selector(
            "#settings-mfa-section button[data-act='startMfaEnrollment']", timeout=TIMEOUT
        )
        assert api.get("/api/auth/me")["mfaEnabled"] is False

    def test_confirm_rejects_wrong_code(self, settings_page, api, mfa_cleanup):
        settings_page.click("#settings-mfa-section button[data-act='startMfaEnrollment']")
        settings_page.wait_for_selector("#mfa-enroll-password", timeout=TIMEOUT)
        settings_page.fill("#mfa-enroll-password", DEMO_USER_PASSWORD)
        settings_page.click("#modal-submit")
        settings_page.wait_for_selector("#mfa-confirm-code", timeout=TIMEOUT)
        settings_page.fill("#mfa-confirm-code", "000000")
        settings_page.click("#settings-mfa-section button[type='submit']")
        settings_page.wait_for_selector("#mfa-confirm-code[aria-invalid='true']", timeout=TIMEOUT)
        # Still pending, not silently enabled.
        assert settings_page.is_visible("#mfa-confirm-code")
        assert api.get("/api/auth/me")["mfaEnabled"] is False

    def test_login_challenge_step_renders_code_input_and_back_link(self, settings_page):
        # A real logout->login round trip can't be driven from this harness: the
        # suite loads the SPA via file://, which trips USE_STANDALONE_DEMO_AUTH
        # (js/config.js) and makes Auth.isAuthenticated() always return true, so
        # the router bounces straight back to /dashboard instead of showing the
        # login form (no other test in this suite exercises logout, for the same
        # reason). Exercise the challenge screen's own render function directly
        # instead — it takes a challengeToken and builds the DOM; the token's
        # validity is the backend's concern (see internal/auth/mfa_login_test.go).
        settings_page.evaluate("(tok) => renderMfaChallengeStep(tok)", "test-challenge-token")
        settings_page.wait_for_selector("#mfa-login-code", timeout=TIMEOUT)
        assert settings_page.is_visible("#mfa-login-submit")
        assert settings_page.is_visible("[data-act='mfaBackToLogin']")
        assert settings_page.get_attribute("#mfa-login-code", "inputmode") == "numeric"


class TestSettingsTerminology:
    """The vocabulary preference: agile wording vs classic project management.

    It is a relabel of the interface only — no project, task or API field
    changes with it — so these tests check that the words move and the data
    does not.
    """

    def _vocab_group(self, page):
        return page.locator("#settings-prefs-section .seg-switch").nth(2)

    def _project_nav(self, page):
        """Open the demo project and return its sidebar text — the vocabulary
        shows up in the per-project views (Backlog, Sprints, Releases), not on
        /settings."""
        page.click("text=Demo Project")
        page.wait_for_selector("#sidebar", timeout=TIMEOUT)
        settle(page)
        return page.inner_text("#sidebar")

    def test_vocabulary_switch_offers_agile_and_classic(self, settings_page):
        options = self._vocab_group(settings_page).locator("button[role='radio']")
        assert options.count() == 2
        assert [options.nth(i).get_attribute("data-a0") for i in range(2)] == ["AGILE", "CLASSIC"]

    def test_agile_is_the_default(self, settings_page):
        checked = self._vocab_group(settings_page).locator("button[aria-checked='true']")
        assert checked.get_attribute("data-a0") == "AGILE"

    def test_switching_to_classic_relabels_the_ui_without_a_reload(
        self, settings_page, api, reset_preferences
    ):
        # A page load would clear this marker; the relabel must not need one.
        settings_page.evaluate("() => { window.__noReload = 'kept'; }")
        self._vocab_group(settings_page).locator("button[data-a0='CLASSIC']").click()
        settings_page.wait_for_selector(".toast-success", timeout=TIMEOUT)
        settle(settings_page)

        assert settings_page.evaluate("() => window.__noReload") == "kept", \
            "the vocabulary switch reloaded the page"
        assert self._vocab_group(settings_page).locator(
            "button[data-a0='CLASSIC'][aria-checked='true']").count() == 1

        nav = self._project_nav(settings_page)
        assert "Phases" in nav and "Task pool" in nav and "Milestones" in nav, \
            f"agile wording remained: {nav!r}"
        assert "Sprints" not in nav and "Backlog" not in nav and "Releases" not in nav

        assert api.get("/api/users/me/preferences")["terminology"] == "CLASSIC"

    def test_classic_renames_releases_to_milestones_inside_the_view(
        self, settings_page, reset_preferences
    ):
        """The overlay reaches a view's own labels, not just the sidebar."""
        self._vocab_group(settings_page).locator("button[data-a0='CLASSIC']").click()
        settings_page.wait_for_selector(".toast-success", timeout=TIMEOUT)
        settle(settings_page)
        self._project_nav(settings_page)
        settings_page.evaluate("() => setView('releases')")
        settle(settings_page)
        # Assert on the view's own labels, not on #content as a whole: release
        # *names* are user data ("Test Release 2621988" outlives other tests)
        # and the vocabulary deliberately leaves data alone.
        create = settings_page.inner_text("[data-act='showCreateRelease']")
        assert "New Milestone" == create.strip(), f"release wording remained: {create!r}"

    def test_classic_vocabulary_survives_a_reload(self, settings_page, reset_preferences):
        self._vocab_group(settings_page).locator("button[data-a0='CLASSIC']").click()
        settings_page.wait_for_selector(".toast-success", timeout=TIMEOUT)
        settle(settings_page)
        settings_page.reload()
        settings_page.wait_for_selector("#sidebar", timeout=TIMEOUT)
        settle(settings_page)
        assert "Phases" in self._project_nav(settings_page)

    def test_switching_back_restores_the_agile_wording(self, settings_page, reset_preferences):
        self._vocab_group(settings_page).locator("button[data-a0='CLASSIC']").click()
        settings_page.wait_for_selector(".toast-success", timeout=TIMEOUT)
        settle(settings_page)
        assert "Phases" in self._project_nav(settings_page)

        settings_page.click("[data-act='nav'][data-a0='/settings']")
        settings_page.wait_for_selector("#settings-prefs-section .seg-switch", timeout=TIMEOUT)
        self._vocab_group(settings_page).locator("button[data-a0='AGILE']").click()
        settings_page.wait_for_selector(".toast-success", timeout=TIMEOUT)
        settle(settings_page)
        nav = self._project_nav(settings_page)
        assert "Sprints" in nav and "Phases" not in nav
        assert "Releases" in nav and "Milestones" not in nav

    def test_the_data_itself_is_untouched_by_the_vocabulary(
        self, settings_page, api, reset_preferences
    ):
        """Classic mode renames labels, not fields: the API still speaks agile."""
        self._vocab_group(settings_page).locator("button[data-a0='CLASSIC']").click()
        settings_page.wait_for_selector(".toast-success", timeout=TIMEOUT)
        settle(settings_page)
        task = api.get("/api/tasks/00000000-0000-0000-0000-000000000201")
        assert "storyPoints" in task, "storyPoints must not be renamed by a display preference"
        assert "sprintId" in task
