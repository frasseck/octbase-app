"""Tests for the Projects list view and project creation dialog."""

import pytest
from conftest import (
    DEMO_PROJECT_NAME, SHORT, TIMEOUT,
    fill_modal, submit_modal, toast_text, unique,
)


@pytest.fixture
def app(app):
    """Projects list view (the base `app` fixture lands on the dashboard)."""
    app.evaluate("() => router.go('/projects')")
    app.wait_for_selector(".project-card", timeout=TIMEOUT)
    return app


class TestProjectsList:
    def test_sidebar_shows_all_projects(self, app):
        projects = app.query_selector_all("#sidebar-nav .sidebar-item")
        labels = [p.inner_text().strip() for p in projects]
        assert any(DEMO_PROJECT_NAME in l for l in labels)

    def test_main_area_shows_project_cards(self, app):
        cards = app.query_selector_all(".project-card")
        assert len(cards) >= 1

    def test_demo_project_card_visible(self, app):
        assert app.is_visible(f"text={DEMO_PROJECT_NAME}")

    def test_project_card_shows_name_and_description(self, app):
        card = app.query_selector(f".project-card:has-text('{DEMO_PROJECT_NAME}')")
        assert card is not None
        assert "demonstration project" in card.inner_text().lower()

    def test_project_status_badge_shows_active(self, app):
        card = app.query_selector(f".project-card:has-text('{DEMO_PROJECT_NAME}')")
        badge = card.query_selector(".badge")
        assert badge is not None
        assert "ACTIVE" in badge.inner_text()

    def test_project_card_has_letter_icon(self, app):
        card = app.query_selector(f".project-card:has-text('{DEMO_PROJECT_NAME}')")
        icon = card.query_selector(".project-icon")
        assert icon is not None
        assert icon.inner_text().strip() == "D"

    def test_new_project_button_in_toolbar(self, app):
        # The create-project affordance lives in the content toolbar (it used to
        # sit in the topbar). Identified by its stable data-act, not its label.
        assert app.is_visible("[data-act='showCreateProject']")

    def test_new_project_button_is_reachable(self, app):
        # There is a single create-project control; confirm it is present and
        # carries its translated label so the affordance is discoverable.
        btn = app.query_selector("[data-act='showCreateProject']")
        assert btn is not None
        assert "New Project" in btn.inner_text()


class TestProjectCreation:
    def test_create_project_dialog_opens(self, app):
        app.click("[data-act='showCreateProject']")
        app.wait_for_selector("#modal-backdrop:not(.hidden)", timeout=SHORT)
        assert app.is_visible(".modal-title")

    def test_create_project_appears_in_list(self, app, api):
        name = unique("UI Test Project")
        app.click("[data-act='showCreateProject']")
        app.wait_for_selector("#proj-name", timeout=SHORT)
        fill_modal(app, {"proj-name": name, "proj-desc": "Created by test"})
        submit_modal(app)
        app.wait_for_selector(f"text={name}", timeout=TIMEOUT)
        assert app.is_visible(f"text={name}")

    def test_create_project_requires_name(self, app):
        app.click("[data-act='showCreateProject']")
        app.wait_for_selector("#proj-name", timeout=SHORT)
        # Submit with empty name
        submit_modal(app)
        # Modal should still be open (or toast error shown)
        app.wait_for_timeout(500)
        still_open = app.is_visible("#modal-backdrop:not(.hidden)")
        error_toast = "required" in toast_text(app).lower() or "error" in toast_text(app).lower()
        assert still_open or error_toast

    def test_cancel_modal_dismisses_dialog(self, app):
        app.click("[data-act='showCreateProject']")
        app.wait_for_selector("#modal-backdrop:not(.hidden)", timeout=SHORT)
        app.click("button:has-text('Cancel')")
        app.wait_for_function(
            "() => document.querySelector('#modal-backdrop')?.classList.contains('hidden')",
            timeout=SHORT,
        )

    def test_backdrop_click_dismisses_dialog(self, app):
        app.click("[data-act='showCreateProject']")
        app.wait_for_selector("#modal-backdrop:not(.hidden)", timeout=SHORT)
        # Click the backdrop itself (outside modal box)
        app.click("#modal-backdrop", position={"x": 10, "y": 10})
        app.wait_for_function(
            "() => document.querySelector('#modal-backdrop')?.classList.contains('hidden')",
            timeout=SHORT,
        )


class TestProjectNavigation:
    def test_clicking_project_opens_board(self, app):
        app.click(f"text={DEMO_PROJECT_NAME}")
        app.wait_for_selector(".board-col", timeout=TIMEOUT)
        assert app.is_visible(".board-col")

    def test_back_to_projects_returns_to_list(self, app):
        app.click(f"text={DEMO_PROJECT_NAME}")
        app.wait_for_selector(".board-col", timeout=TIMEOUT)
        app.click("text=All Projects")
        app.wait_for_selector(".project-card", timeout=TIMEOUT)
        assert app.is_visible(".project-card")

    def test_project_name_in_topbar_breadcrumb(self, app):
        app.click(f"text={DEMO_PROJECT_NAME}")
        app.wait_for_selector(".board-col", timeout=TIMEOUT)
        topbar = app.query_selector("#topbar")
        assert DEMO_PROJECT_NAME in topbar.inner_text()
