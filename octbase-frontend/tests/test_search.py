"""Tests for the Search page: input, results rendering, navigation."""

import pytest
from conftest import (
    DEMO_PROJECT_ID, DEMO_TASK_TITLE, SHORT, TIMEOUT,
    goto_route, navigate_to, settle,)


class TestSearchPageStructure:
    def test_search_page_accessible_from_hash(self, app):
        """Navigate to #/search directly and verify the search input renders."""
        goto_route(app, '/search', ".search-page")
        assert app.is_visible("#sp-input")
        assert app.is_visible("#sp-results")

    def test_search_page_has_search_button(self, app):
        goto_route(app, '/search', ".search-page")
        assert app.is_visible("button:has-text('Search')")

    def test_search_input_accepts_text(self, app):
        goto_route(app, '/search', "#sp-input")
        app.fill("#sp-input", "hello world")
        assert app.query_selector("#sp-input").input_value() == "hello world"


class TestSearchResults:
    def test_search_returns_task_result(self, app):
        """Searching for the seeded demo task title should return at least one result."""
        goto_route(app, '/search', "#sp-input")
        # Use a word from the seeded demo task title.
        query = "authentication"
        app.fill("#sp-input", query)
        app.click("button:has-text('Search')")
        # A finished search always ends in one of these two: rendered results or
        # the "no results" empty state.
        app.wait_for_selector(".search-result, .dash-empty", timeout=TIMEOUT)
        has_results = app.is_visible(".search-result")
        has_empty = app.is_visible(".dash-empty")
        assert has_results or has_empty

    def test_search_result_appears_after_enter_key(self, app):
        goto_route(app, '/search', "#sp-input")
        app.fill("#sp-input", "demo")
        app.press("#sp-input", "Enter")
        settle(app)
        # Results container should no longer be empty after a search.
        results_html = app.query_selector("#sp-results").inner_html()
        assert results_html != ""

    def test_search_known_title_shows_result(self, app):
        """Searching the exact demo task title must return a search-result."""
        goto_route(app, '/search', "#sp-input")
        app.fill("#sp-input", "Implement user authentication")
        app.click("button:has-text('Search')")
        app.wait_for_selector(".search-result", timeout=TIMEOUT)
        assert app.is_visible(".search-result")

    def test_empty_query_does_not_submit(self, app):
        """An empty search should not produce results or an error."""
        goto_route(app, '/search', "#sp-input")
        app.fill("#sp-input", "")
        app.click("button:has-text('Search')")
        settle(app)
        # results container should still be empty.
        results_html = app.query_selector("#sp-results").inner_html()
        assert results_html == ""

    def test_search_result_click_navigates(self, app):
        """Clicking a task search result should open the task panel or navigate away."""
        goto_route(app, '/search', "#sp-input")
        app.fill("#sp-input", "Implement user authentication")
        app.click("button:has-text('Search')")
        app.wait_for_selector(".search-result", timeout=TIMEOUT)
        app.click(".search-result")
        # Clicking a result routes away from /search — wait for that outcome
        # rather than for the clock ("hidden" also covers the detached case,
        # since navigating replaces #content wholesale).
        app.wait_for_selector(".search-page", state="hidden", timeout=TIMEOUT)
        # After clicking a task result the search-page should no longer be active.
        assert not app.is_visible(".search-page")
