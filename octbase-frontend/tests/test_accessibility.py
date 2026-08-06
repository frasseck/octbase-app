"""Automated accessibility regression tests (WCAG 2.2 AA).

Covers:
- axe-core scans of the key views (login, dashboard, board, task panel, modal)
- keyboard-only navigation (skip link, focus order, focus trap, Escape)
- form error identification (3.3.1) and field association (3.3.2)
- live-region / status message presence (4.1.3)
"""

import pytest
import requests
from axe_playwright_python.sync_playwright import Axe
from conftest import (
    ApiClient, DEMO_TASK_TITLE, DEMO_USER_EMAIL, SHORT, TIMEOUT,
    navigate_to, settle, sign_in_if_needed,)

# Rules that are not relevant for an internal SPA shell or are covered by
# manual review (see "Manuelle WCAG-AA-Checkliste").
AXE_DISABLED_RULES = ["region"]

# These tests rely on the real login form, which the SPA only renders when
# served over http(s) — opened via file:// it auto-signs-in as the demo user
# (see USE_STANDALONE_DEMO_AUTH in app.js) and the login page never appears.
# Run against the hosted dev deployment instead of the file:// + local API
# defaults used by the rest of the suite.
import os

# These tests need the SPA served over http(s) (the login page only renders off
# file://). They default to the hosted dev deployment but are overridable so the
# same suite can run against a local build in CI:
#   OCTBASE_ACCESS_API_BASE — API origin (proxied behind the UI host)
#   OCTBASE_ACCESS_UI_URL    — fully-qualified index.html URL
# Keep the UI same-origin with the API to avoid the CORS rejection a cross-port
# apiBase would trigger.
ACCESS_API_BASE = os.getenv("OCTBASE_ACCESS_API_BASE", "http://dev.octbase.io:8001")
ACCESS_UI_URL = os.getenv("OCTBASE_ACCESS_UI_URL", "http://dev.octbase.io:8081/index.html")


@pytest.fixture(scope="session")
def api():
    try:
        requests.get(f"{ACCESS_API_BASE}/health", timeout=3).raise_for_status()
    except Exception as exc:
        pytest.skip(f"API not reachable at {ACCESS_API_BASE}: {exc}")
    return ApiClient(base_url=ACCESS_API_BASE)


@pytest.fixture
def app(page, api):
    """App loaded at the projects list, served from the dev deployment."""
    page.goto(ACCESS_UI_URL)
    sign_in_if_needed(page)
    page.wait_for_selector("text=Demo Project", timeout=TIMEOUT)
    return page


def run_axe(page):
    axe = Axe()
    results = axe.run(page, options={"rules": {r: {"enabled": False} for r in AXE_DISABLED_RULES}})
    violations = results.response.get("violations", [])
    serious = [v for v in violations if v["impact"] in ("serious", "critical")]
    assert not serious, "\n".join(
        f"{v['id']} ({v['impact']}): {v['description']} -> "
        f"{[n['target'] for n in v['nodes']]}" for v in serious
    )


class TestAxeScans:
    # `api` gates on the access deployment being reachable: without it these
    # page-only tests hang 30s in page.goto and FAIL (instead of skipping like
    # every fixture-based sibling) when the suite runs where dev.octbase.io is
    # not reachable, e.g. in the GitHub pipeline.
    def test_login_page(self, page, api):
        page.goto(ACCESS_UI_URL)
        page.wait_for_selector("#login-email", timeout=TIMEOUT)
        run_axe(page)

    def test_dashboard(self, app):
        app.wait_for_selector("#content", timeout=TIMEOUT)
        run_axe(app)

    def test_board(self, demo_board):
        run_axe(demo_board)

    def test_task_panel(self, task_panel):
        run_axe(task_panel)

    def test_create_task_modal(self, demo_board):
        demo_board.click(".board-toolbar [data-act='showCreateTask']")
        demo_board.wait_for_selector("#modal-backdrop:not(.hidden)", timeout=TIMEOUT)
        run_axe(demo_board)


class TestKeyboardNavigation:
    def test_skip_link_focuses_main_content(self, app):
        # First Tab from the top of the page should reach the skip link.
        app.keyboard.press("Tab")
        focused = app.evaluate("document.activeElement.className")
        assert "skip-link" in focused
        app.keyboard.press("Enter")
        focused_id = app.evaluate("document.activeElement.id")
        assert focused_id == "content"

    def test_modal_traps_focus_and_escape_closes(self, demo_board):
        demo_board.click(".board-toolbar [data-act='showCreateTask']")
        demo_board.wait_for_selector("#modal-backdrop:not(.hidden)", timeout=TIMEOUT)
        # Focus should start inside the modal.
        in_modal = demo_board.evaluate("!!document.getElementById('modal').contains(document.activeElement)")
        assert in_modal
        demo_board.keyboard.press("Escape")
        settle(demo_board)
        assert demo_board.is_hidden("#modal-backdrop")

    def test_task_panel_opens_with_keyboard(self, demo_board):
        card = demo_board.query_selector(".board-card")
        card.focus()
        demo_board.keyboard.press("Enter")
        demo_board.wait_for_selector("#task-panel.open", timeout=TIMEOUT)
        # Focus should move into the panel (close button) once the panel finishes rendering.
        demo_board.wait_for_function(
            "document.activeElement.classList.contains('panel-close')", timeout=TIMEOUT
        )
        focused = demo_board.evaluate("document.activeElement.className")
        assert "panel-close" in focused

    def test_board_column_move_is_keyboard_alternative_to_drag(self, demo_board):
        # The card face no longer carries a status/column dropdown (the lane already
        # conveys the column). The keyboard alternative to drag is the "Board column"
        # select in the task panel — WCAG 2.5.7 (Dragging) / 2.1.1 (Keyboard).
        card = demo_board.query_selector(".board-card")
        card.focus()
        demo_board.keyboard.press("Enter")
        demo_board.wait_for_selector("#task-panel.open", timeout=TIMEOUT)
        select = demo_board.wait_for_selector(
            "select[data-change='moveTaskToColumnSelect']", timeout=TIMEOUT
        )
        assert select is not None
        assert select.get_attribute("aria-label")


class TestFormErrors:
    # See TestAxeScans.test_login_page: `api` makes this skip, not fail, when
    # the access deployment is unreachable (as in CI).
    def test_login_error_is_announced_and_focused(self, page, api):
        page.goto(ACCESS_UI_URL)
        page.wait_for_selector("#login-email", timeout=TIMEOUT)
        page.fill("#login-email", DEMO_USER_EMAIL)
        page.fill("#login-password", "wrong-password")
        page.click("#login-submit")
        page.wait_for_selector("#login-error:not(:empty)", timeout=TIMEOUT)
        err = page.query_selector("#login-error")
        assert err.get_attribute("role") == "alert"
        # The password field references the error via aria-describedby.
        describedby = page.get_attribute("#login-password", "aria-describedby")
        assert "login-error" in (describedby or "")

    def test_create_task_without_title_shows_inline_error(self, demo_board):
        demo_board.click(".board-toolbar [data-act='showCreateTask']")
        demo_board.wait_for_selector("#modal-backdrop:not(.hidden)", timeout=TIMEOUT)
        demo_board.fill("#task-title", "")
        demo_board.click("#modal-submit")
        demo_board.wait_for_selector("#task-title-error", timeout=TIMEOUT)
        err = demo_board.query_selector("#task-title-error")
        assert err.get_attribute("role") == "alert"
        assert demo_board.get_attribute("#task-title", "aria-invalid") == "true"
        # Focus moves back to the invalid field.
        focused_id = demo_board.evaluate("document.activeElement.id")
        assert focused_id == "task-title"


class TestLiveRegions:
    def test_toast_container_is_live_region(self, app):
        container = app.query_selector("#toast-container")
        assert container.get_attribute("aria-live") in ("polite", "assertive")
        assert container.get_attribute("role") == "status"

    def test_bulk_bar_is_labelled_region(self, demo_board):
        # Selection (and the bulk bar) is a backlog-only affordance — the board's
        # cards carry no checkboxes.
        navigate_to(demo_board, "Backlog")
        demo_board.wait_for_selector(".task-checkbox", timeout=TIMEOUT)
        checkbox = demo_board.query_selector(".task-checkbox")
        checkbox.check(force=True)
        demo_board.wait_for_selector("#bulk-bar:not(.hidden)", timeout=TIMEOUT)
        bar = demo_board.query_selector("#bulk-bar")
        assert bar.get_attribute("role") == "region"
        assert bar.get_attribute("aria-label")
        for select_id in ("bulk-assignee", "bulk-priority"):
            sel = demo_board.query_selector(f"#{select_id}")
            assert sel.get_attribute("aria-label")
