"""Tests for the Sprints view: listing, creation, start/complete lifecycle."""

import pytest
from conftest import (
    DEMO_PROJECT_ID, SHORT, TIMEOUT,
    navigate_to, unique, submit_modal, fill_modal, poll_until, settle,)


class TestSprintsView:
    def test_sprints_nav_item_exists(self, demo_board):
        assert demo_board.is_visible(".sidebar-item:has-text('Sprints')")

    def test_sprints_view_loads(self, demo_board):
        navigate_to(demo_board, "Sprints")
        settle(demo_board)
        has_cards = demo_board.is_visible(".sprint-card")
        has_empty = demo_board.is_visible(".empty")
        assert has_cards or has_empty

    def test_sprints_view_shows_section_labels(self, demo_board, api):
        # Ensure at least one sprint exists so a section label is rendered.
        api.post(f"/api/projects/{DEMO_PROJECT_ID}/sprints", {"name": unique("SL Sprint")})
        navigate_to(demo_board, "Sprints")
        demo_board.wait_for_selector(".sprint-card", timeout=TIMEOUT)
        assert demo_board.is_visible(".sprint-section-label")

    def test_sprint_card_shows_name(self, demo_board, api):
        name = unique("Named Sprint")
        api.post(f"/api/projects/{DEMO_PROJECT_ID}/sprints", {"name": name})
        navigate_to(demo_board, "Sprints")
        demo_board.wait_for_selector(f"text={name}", timeout=TIMEOUT)
        assert demo_board.is_visible(f"text={name}")

    def test_sprint_card_has_actions(self, demo_board, api):
        name = unique("Actions Sprint")
        api.post(f"/api/projects/{DEMO_PROJECT_ID}/sprints", {"name": name})
        navigate_to(demo_board, "Sprints")
        demo_board.wait_for_selector(f"text={name}", timeout=TIMEOUT)
        card = demo_board.query_selector(f".sprint-card:has-text('{name}')")
        assert card is not None
        # Planned sprints should have an Edit button and a Start Sprint button.
        assert card.query_selector("button:has-text('Edit')") is not None
        assert card.query_selector("button:has-text('Start Sprint')") is not None


class TestSprintCreation:
    def test_create_sprint_button_visible(self, demo_board):
        navigate_to(demo_board, "Sprints")
        settle(demo_board)
        # When there are sprints the topbar has a New Sprint button;
        # when the list is empty the inline button is used instead.
        has_topbar = demo_board.is_visible("[data-act='showCreateSprint']")
        has_empty_btn = demo_board.is_visible("button:has-text('New Sprint')")
        assert has_topbar or has_empty_btn

    def test_create_sprint_appears_in_list(self, demo_board, api):
        name = unique("Created Sprint")
        navigate_to(demo_board, "Sprints")
        settle(demo_board)
        # Click whichever New Sprint button is visible.
        if demo_board.is_visible("[data-act='showCreateSprint']"):
            demo_board.click("[data-act='showCreateSprint']")
        else:
            demo_board.click("button:has-text('New Sprint')")

        demo_board.wait_for_selector("#sp-name", timeout=SHORT)
        demo_board.fill("#sp-name", name)
        submit_modal(demo_board)
        demo_board.wait_for_selector(f"text={name}", timeout=TIMEOUT)
        assert demo_board.is_visible(f"text={name}")

    def test_created_sprint_starts_planned(self, demo_board, api):
        name = unique("Planned Sprint")
        api.post(f"/api/projects/{DEMO_PROJECT_ID}/sprints", {"name": name})
        sprints = api.get(f"/api/projects/{DEMO_PROJECT_ID}/sprints")
        created = next((s for s in sprints if s["name"] == name), None)
        assert created is not None
        assert created["status"] == "PLANNED"


class TestSprintLifecycle:
    def test_start_sprint_changes_status(self, demo_board, api):
        name = unique("Start Me Sprint")
        sprint = api.post(f"/api/projects/{DEMO_PROJECT_ID}/sprints", {"name": name})
        navigate_to(demo_board, "Sprints")
        demo_board.wait_for_selector(f"text={name}", timeout=TIMEOUT)
        demo_board.click(f".sprint-card:has-text('{name}') button:has-text('Start Sprint')")

        def _active():
            s = api.get(f"/api/sprints/{sprint['id']}")
            return s if s["status"] == "ACTIVE" else None

        updated = poll_until(_active, message="sprint never reached ACTIVE via the API")
        assert updated["status"] == "ACTIVE"

    def test_started_sprint_shows_complete_button(self, demo_board, api):
        name = unique("Active Sprint")
        # Only one sprint may be ACTIVE per project at a time — complete any
        # sprint left active by an earlier test before starting this one.
        for s in api.get(f"/api/projects/{DEMO_PROJECT_ID}/sprints"):
            if s["status"] == "ACTIVE":
                api.post(f"/api/sprints/{s['id']}/complete", {})
        sprint = api.post(f"/api/projects/{DEMO_PROJECT_ID}/sprints", {"name": name})
        api.post(f"/api/sprints/{sprint['id']}/start", {})
        navigate_to(demo_board, "Sprints")
        demo_board.wait_for_selector(f"text={name}", timeout=TIMEOUT)
        card = demo_board.query_selector(f".sprint-card:has-text('{name}')")
        assert card is not None
        assert card.query_selector("button:has-text('Complete Sprint')") is not None

    def test_complete_sprint_changes_status(self, demo_board, api):
        name = unique("Complete Me Sprint")
        # Only one sprint may be ACTIVE per project at a time — complete any
        # sprint left active by an earlier test before starting this one.
        for s in api.get(f"/api/projects/{DEMO_PROJECT_ID}/sprints"):
            if s["status"] == "ACTIVE":
                api.post(f"/api/sprints/{s['id']}/complete", {})
        sprint = api.post(f"/api/projects/{DEMO_PROJECT_ID}/sprints", {"name": name})
        api.post(f"/api/sprints/{sprint['id']}/start", {})
        navigate_to(demo_board, "Sprints")
        demo_board.wait_for_selector(f".sprint-card:has-text('{name}')", timeout=TIMEOUT)
        demo_board.click(f".sprint-card:has-text('{name}') button:has-text('Complete Sprint')")
        # Completion opens a confirm dialog — confirm it.
        demo_board.wait_for_selector("#modal-backdrop:not(.hidden)", timeout=SHORT)
        submit_modal(demo_board)

        def _completed():
            s = api.get(f"/api/sprints/{sprint['id']}")
            return s if s["status"] == "COMPLETED" else None

        updated = poll_until(_completed, message="sprint never reached COMPLETED via the API")
        assert updated["status"] == "COMPLETED"
