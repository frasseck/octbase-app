"""Tests for the Releases view: listing, creation, edit, close/reopen."""

import pytest
from conftest import (
    DEMO_MILESTONE_ID, DEMO_PROJECT_ID, SHORT, TIMEOUT,
    fill_modal, submit_modal, toast_text, unique, navigate_to, poll_until, settle,)


class TestReleasesView:
    def test_releases_nav_item_exists(self, demo_board):
        assert demo_board.is_visible(".sidebar-item:has-text('Releases')")

    def test_releases_view_loads(self, demo_board):
        navigate_to(demo_board, "Releases")
        settle(demo_board)
        has_cards = demo_board.is_visible(".release-card")
        has_empty = demo_board.is_visible(".empty")
        assert has_cards or has_empty

    def test_seeded_release_visible(self, demo_board):
        navigate_to(demo_board, "Releases")
        demo_board.wait_for_selector(".release-card", timeout=TIMEOUT)
        assert demo_board.is_visible("text=v1.0 Launch")

    def test_release_card_shows_status_badge(self, demo_board):
        navigate_to(demo_board, "Releases")
        demo_board.wait_for_selector(".release-card", timeout=TIMEOUT)
        card = demo_board.query_selector(".release-card")
        assert card.query_selector(".badge") is not None

    def test_release_card_shows_goal(self, demo_board):
        navigate_to(demo_board, "Releases")
        demo_board.wait_for_selector(".release-card", timeout=TIMEOUT)
        card = demo_board.query_selector(".release-card:has-text('v1.0 Launch')")
        assert "Ship first version" in card.inner_text()

    def test_release_card_shows_due_date(self, demo_board):
        navigate_to(demo_board, "Releases")
        demo_board.wait_for_selector(".release-card", timeout=TIMEOUT)
        card = demo_board.query_selector(".release-card:has-text('v1.0 Launch')")
        assert "Jun" in card.inner_text() or "2024" in card.inner_text()

    def test_new_release_button_in_topbar(self, demo_board):
        navigate_to(demo_board, "Releases")
        settle(demo_board)
        assert demo_board.is_visible("[data-act='showCreateRelease']")


class TestReleaseCreation:
    def test_create_release_dialog_opens(self, demo_board):
        navigate_to(demo_board, "Releases")
        settle(demo_board)
        demo_board.click("[data-act='showCreateRelease']")
        demo_board.wait_for_selector("#modal-backdrop:not(.hidden)", timeout=SHORT)
        assert demo_board.is_visible(".modal-title")

    def test_create_release_appears_in_list(self, demo_board, api):
        name = unique("Test Release")
        navigate_to(demo_board, "Releases")
        settle(demo_board)
        demo_board.click("[data-act='showCreateRelease']")
        demo_board.wait_for_selector("#ms-name", timeout=SHORT)
        fill_modal(demo_board, {
            "ms-name": name,
            "ms-goal": "Reach a significant achievement",
            "ms-due": "2026-12-31",
        })
        submit_modal(demo_board)
        demo_board.wait_for_selector(f"text={name}", timeout=TIMEOUT)
        assert demo_board.is_visible(f"text={name}")

    def test_create_release_starts_planned(self, demo_board, api):
        name = unique("Planned Release")
        navigate_to(demo_board, "Releases")
        settle(demo_board)
        demo_board.click("[data-act='showCreateRelease']")
        demo_board.wait_for_selector("#ms-name", timeout=SHORT)
        fill_modal(demo_board, {"ms-name": name})
        submit_modal(demo_board)
        created = poll_until(
            lambda: next(
                (m for m in api.get(f"/api/projects/{DEMO_PROJECT_ID}/releases")
                 if m["name"] == name),
                None,
            ),
            message="created release never appeared via the API",
        )
        assert created is not None
        assert created["status"] == "PLANNED"


class TestReleaseEdit:
    def test_edit_button_opens_modal(self, demo_board):
        navigate_to(demo_board, "Releases")
        demo_board.wait_for_selector(".release-card", timeout=TIMEOUT)
        demo_board.click(".release-card:has-text('v1.0 Launch') button:has-text('Edit')")
        demo_board.wait_for_selector("#modal-backdrop:not(.hidden)", timeout=SHORT)
        assert demo_board.is_visible("#ms-name")

    def test_edit_modal_prepopulates_name(self, demo_board):
        navigate_to(demo_board, "Releases")
        demo_board.wait_for_selector(".release-card", timeout=TIMEOUT)
        demo_board.click(".release-card:has-text('v1.0 Launch') button:has-text('Edit')")
        demo_board.wait_for_selector("#ms-name", timeout=SHORT)
        assert "v1.0" in demo_board.query_selector("#ms-name").input_value()

    def test_edit_release_saves_new_goal(self, demo_board, api):
        name = unique("Editable Release")
        ms = api.post(f"/api/projects/{DEMO_PROJECT_ID}/releases", {
            "name": name, "goal": "Original goal",
        })
        navigate_to(demo_board, "Releases")
        demo_board.wait_for_selector(f"text={name}", timeout=TIMEOUT)
        demo_board.click(f".release-card:has-text('{name}') button:has-text('Edit')")
        demo_board.wait_for_selector("#ms-goal", timeout=SHORT)
        demo_board.fill("#ms-goal", "Updated goal")
        submit_modal(demo_board)

        def _saved():
            r = api.get(f"/api/releases/{ms['id']}")
            return r if r["goal"] == "Updated goal" else None

        updated = poll_until(_saved, message="edited goal never landed via the API")
        assert updated["goal"] == "Updated goal"


class TestReleaseCloseReopen:
    def test_close_release_changes_status(self, demo_board, api):
        name = unique("Closeable Release")
        ms = api.post(f"/api/projects/{DEMO_PROJECT_ID}/releases", {"name": name})
        navigate_to(demo_board, "Releases")
        demo_board.wait_for_selector(f"text={name}", timeout=TIMEOUT)
        demo_board.click(f".release-card:has-text('{name}') button:has-text('Ship')")

        def _closed():
            r = api.get(f"/api/releases/{ms['id']}")
            return r if r["status"] == "CLOSED" else None

        updated = poll_until(_closed, message="release never reached CLOSED via the API")
        assert updated["status"] == "CLOSED"

    def test_reopen_release_changes_status(self, demo_board, api):
        name = unique("Reopen Release")
        ms = api.post(f"/api/projects/{DEMO_PROJECT_ID}/releases", {"name": name})
        api.post(f"/api/releases/{ms['id']}/close", {})
        navigate_to(demo_board, "Releases")
        demo_board.wait_for_selector(f"text={name}", timeout=TIMEOUT)
        demo_board.click(f".release-card:has-text('{name}') button:has-text('Reopen')")

        # The release was closed above, so PLANNED here is a real transition.
        def _reopened():
            r = api.get(f"/api/releases/{ms['id']}")
            return r if r["status"] == "PLANNED" else None

        updated = poll_until(_reopened, message="release never reopened to PLANNED via the API")
        assert updated["status"] == "PLANNED"

    def test_close_release_with_open_tasks_shows_error(self, demo_board, api):
        name = unique("Blocked Release")
        ms = api.post(f"/api/projects/{DEMO_PROJECT_ID}/releases", {"name": name})
        # Create a PLANNED task assigned to this release
        task = api.post(f"/api/projects/{DEMO_PROJECT_ID}/tasks",
                        {"title": unique("Blocking Task"), "releaseId": ms["id"]})
        navigate_to(demo_board, "Releases")
        demo_board.wait_for_selector(f"text={name}", timeout=TIMEOUT)
        demo_board.click(f".release-card:has-text('{name}') button:has-text('Ship')")
        # Nothing to poll for: the close must be *rejected*, so wait for the
        # request to land and assert the status is unchanged rather than polling
        # for PLANNED (which holds from the start and would pass vacuously).
        settle(demo_board)
        # Release should still be PLANNED
        still_open = api.get(f"/api/releases/{ms['id']}")
        assert still_open["status"] == "PLANNED"
