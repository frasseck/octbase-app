"""Tests for hiding/showing the navigation sidebar on the Expanded layout.

At the test viewport (1400×900, > 1024px) the sidebar is a permanent rail with
the toggle in its own header; collapsing it reveals the topbar hamburger, which
is the only way to bring the rail back. The choice persists. (Below 1024px the
topbar hamburger drives the off-canvas drawer instead — a separate behaviour.)
"""

import pytest
from conftest import desktop_url, SHORT, TIMEOUT, settle, sign_in_if_needed

NAV_HIDDEN = "() => document.getElementById('app').classList.contains('nav-hidden')"


def _login(page):
    page.goto(desktop_url())
    sign_in_if_needed(page)
    # Exactly one of the two toggles is visible at any time: the sidebar-header
    # one while the rail shows, the topbar one while it is collapsed.
    page.wait_for_selector(".btn-hamburger:visible", timeout=TIMEOUT)


class TestSidebarToggle:
    def test_toggle_collapses_and_restores(self, app):
        assert app.is_visible("#sidebar")
        assert not app.evaluate(NAV_HIDDEN)
        # While the rail shows, its header holds the toggle; the topbar twin is hidden.
        assert app.is_visible("#sidebar .btn-hamburger")
        assert not app.is_visible("#topbar .btn-hamburger")
        app.click("#sidebar .btn-hamburger")
        settle(app)
        assert app.evaluate(NAV_HIDDEN)
        assert not app.is_visible("#sidebar")
        # Collapsed: the topbar hamburger appears and reopens the rail.
        assert app.is_visible("#topbar .btn-hamburger")
        assert app.get_attribute("#topbar .btn-hamburger", "aria-expanded") == "false"
        app.click("#topbar .btn-hamburger")
        settle(app)
        assert not app.evaluate(NAV_HIDDEN)
        assert app.is_visible("#sidebar")
        assert not app.is_visible("#topbar .btn-hamburger")
        assert app.get_attribute("#sidebar .btn-hamburger", "aria-expanded") == "true"

    def test_pref_persists_across_reload(self, page):
        _login(page)
        page.click("#sidebar .btn-hamburger")
        settle(page)
        assert page.evaluate(NAV_HIDDEN)
        # Reboot in the same context (localStorage persists) — choice is restored.
        _login(page)
        assert page.evaluate(NAV_HIDDEN)
        assert not page.is_visible("#sidebar")

    def test_logout_button_sits_in_topbar(self, app):
        assert app.is_visible("#topbar [data-act='logout']")
        assert app.query_selector("#sidebar [data-act='logout']") is None
