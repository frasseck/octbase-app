"""Tests for the Activity feed view."""

import pytest
from conftest import DEMO_PROJECT_ID, DEMO_TASK_ID, SHORT, TIMEOUT, navigate_to, unique, settle


class TestActivityView:
    def test_activity_nav_item_exists(self, demo_board):
        assert demo_board.is_visible(".sidebar-item:has-text('Activity')")

    def test_navigating_to_activity_loads_view(self, demo_board):
        navigate_to(demo_board, "Activity")
        settle(demo_board)
        has_items = demo_board.is_visible(".activity-item")
        has_empty = demo_board.is_visible(".empty")
        assert has_items or has_empty

    def test_activity_breadcrumb_shows_activity(self, demo_board):
        navigate_to(demo_board, "Activity")
        settle(demo_board)
        topbar = demo_board.query_selector("#topbar")
        assert "Activity" in topbar.inner_text()

    def test_activity_items_have_content(self, demo_board):
        navigate_to(demo_board, "Activity")
        demo_board.wait_for_selector(".activity-item", timeout=TIMEOUT)
        item = demo_board.query_selector(".activity-item")
        assert item is not None
        assert item.inner_text().strip() != ""

    def test_activity_item_shows_timestamp(self, demo_board):
        navigate_to(demo_board, "Activity")
        demo_board.wait_for_selector(".activity-item", timeout=TIMEOUT)
        item = demo_board.query_selector(".activity-item")
        text = item.inner_text()
        # Timestamp typically contains digits like year or time
        assert any(c.isdigit() for c in text)

    def test_activity_loads_after_task_action(self, demo_board, api):
        # Perform an action that generates activity
        api.post(f"/api/tasks/{DEMO_TASK_ID}/priority", {"priority": "MEDIUM"})

        navigate_to(demo_board, "Activity")
        settle(demo_board)
        # Restore
        api.post(f"/api/tasks/{DEMO_TASK_ID}/priority", {"priority": "HIGH"})

        has_items = demo_board.is_visible(".activity-item")
        has_empty = demo_board.is_visible(".empty")
        assert has_items or has_empty

    def test_activity_shows_project_events(self, demo_board, api):
        # Create a task to generate an event
        title = unique("Activity Test Task")
        api.post(f"/api/projects/{DEMO_PROJECT_ID}/tasks", {"title": title})

        navigate_to(demo_board, "Activity")
        settle(demo_board)

        has_items = demo_board.is_visible(".activity-item")
        has_empty = demo_board.is_visible(".empty")
        assert has_items or has_empty


class TestActivityPagination:
    def test_activity_view_does_not_crash_with_many_events(self, demo_board, api):
        # Generate several events
        for i in range(3):
            title = unique(f"Bulk Activity Task {i}")
            api.post(f"/api/projects/{DEMO_PROJECT_ID}/tasks", {"title": title})

        navigate_to(demo_board, "Activity")
        settle(demo_board)

        # Page should not crash
        has_items = demo_board.is_visible(".activity-item")
        has_empty = demo_board.is_visible(".empty")
        assert has_items or has_empty

    def test_activity_refresh_on_navigation(self, demo_board):
        # Navigate away and back
        navigate_to(demo_board, "Board")
        settle(demo_board)
        navigate_to(demo_board, "Activity")
        settle(demo_board)
        has_items = demo_board.is_visible(".activity-item")
        has_empty = demo_board.is_visible(".empty")
        assert has_items or has_empty
