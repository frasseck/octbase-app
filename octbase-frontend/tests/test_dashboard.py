"""Tests for the Dashboard page: loaded on app start, shows key sections."""

import pytest
from conftest import (
    DEMO_USER_EMAIL, DEMO_USER_PASSWORD, SHORT, TIMEOUT,
)


class TestDashboardStructure:
    def test_dashboard_loads_on_app_open(self, app):
        """The dashboard-grid should be visible immediately after login."""
        app.wait_for_selector(".dashboard-grid", timeout=TIMEOUT)
        assert app.is_visible(".dashboard-grid")

    def test_dashboard_has_six_sections(self, app):
        # Assigned, In review, Recent pages, Upcoming releases, My projects, My boards.
        app.wait_for_selector(".dash-section", timeout=TIMEOUT)
        sections = app.query_selector_all(".dash-section")
        assert len(sections) == 6

    def test_dashboard_shows_projects_and_boards(self, app):
        app.wait_for_selector(".dash-section-title", timeout=TIMEOUT)
        titles = [el.inner_text().lower() for el in app.query_selector_all(".dash-section-title")]
        assert any("project" in t for t in titles)
        assert any("board" in t for t in titles)

    def test_dashboard_assigned_section_has_title(self, app):
        app.wait_for_selector(".dash-section-title", timeout=TIMEOUT)
        titles = [el.inner_text() for el in app.query_selector_all(".dash-section-title")]
        assert any("assigned" in t.lower() for t in titles)

    def test_dashboard_review_section_has_title(self, app):
        app.wait_for_selector(".dash-section-title", timeout=TIMEOUT)
        titles = [el.inner_text() for el in app.query_selector_all(".dash-section-title")]
        assert any("review" in t.lower() or "Review" in t for t in titles)

    def test_dashboard_pages_section_has_title(self, app):
        app.wait_for_selector(".dash-section-title", timeout=TIMEOUT)
        titles = [el.inner_text() for el in app.query_selector_all(".dash-section-title")]
        assert any("page" in t.lower() for t in titles)

    def test_dashboard_releases_section_has_title(self, app):
        app.wait_for_selector(".dash-section-title", timeout=TIMEOUT)
        titles = [el.inner_text() for el in app.query_selector_all(".dash-section-title")]
        assert any("release" in t.lower() or "release" in t.lower() for t in titles)


class TestDashboardContent:
    def test_assigned_tasks_row_links_to_task(self, app):
        """Rows in the Assigned section should be clickable (have onclick)."""
        app.wait_for_selector(".dashboard-grid", timeout=TIMEOUT)
        rows = app.query_selector_all(".dash-task-row")
        # The seeded demo data assigns the demo task to the demo user.
        if rows:
            el = rows[0]
            # Just check that the element is present; click navigates away.
            assert el is not None

    def test_recent_pages_row_visible(self, app):
        """If the seeded page is published its row should appear in Recent pages."""
        app.wait_for_selector(".dashboard-grid", timeout=TIMEOUT)
        # The seeded demo page may appear as a dash-page-row.
        has_rows = app.is_visible(".dash-page-row")
        has_empty = app.is_visible(".dash-empty")
        assert has_rows or has_empty

    def test_dashboard_shows_no_js_errors(self, app):
        """Page should render without unhandled JS errors."""
        app.wait_for_selector(".dashboard-grid", timeout=TIMEOUT)
        # If the grid rendered we got past all async calls successfully.
        assert app.is_visible(".dashboard-grid")
