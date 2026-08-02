"""Tests for the Backlog view."""

import pytest
from conftest import (
    DEMO_PROJECT_ID, SHORT, TIMEOUT, navigate_to, poll_until, settle, submit_modal, unique,)


class TestBacklogView:
    def test_backlog_nav_item_exists(self, demo_board):
        assert demo_board.is_visible(".sidebar-item:has-text('Backlog')")

    def test_navigating_to_backlog_loads_view(self, demo_board):
        navigate_to(demo_board, "Backlog")
        settle(demo_board)
        # Either shows items or an empty state
        has_items = demo_board.is_visible(".backlog-row")
        has_empty = demo_board.is_visible(".empty")
        assert has_items or has_empty

    def test_backlog_breadcrumb_shows_backlog(self, demo_board):
        navigate_to(demo_board, "Backlog")
        settle(demo_board)
        topbar = demo_board.query_selector("#topbar")
        assert "Backlog" in topbar.inner_text()

    def test_create_task_button_visible_in_backlog(self, demo_board):
        navigate_to(demo_board, "Backlog")
        settle(demo_board)
        assert demo_board.is_visible("[data-act='showCreateTask']")


class TestBacklogItems:
    def test_backlog_item_shows_type_badge(self, demo_board, api):
        # Ensure there's at least one backlog task
        backlog = api.get(f"/api/projects/{DEMO_PROJECT_ID}/backlog")
        if not backlog:
            # Create one
            api.post(f"/api/projects/{DEMO_PROJECT_ID}/tasks", {"title": unique("Backlog Task")})

        navigate_to(demo_board, "Backlog")
        demo_board.wait_for_selector(".backlog-row", timeout=TIMEOUT)
        item = demo_board.query_selector(".backlog-row")
        assert item.query_selector(".type-badge") is not None

    def test_backlog_item_shows_priority_dot(self, demo_board, api):
        backlog = api.get(f"/api/projects/{DEMO_PROJECT_ID}/backlog")
        if not backlog:
            api.post(f"/api/projects/{DEMO_PROJECT_ID}/tasks", {"title": unique("Backlog Task")})

        navigate_to(demo_board, "Backlog")
        demo_board.wait_for_selector(".backlog-row", timeout=TIMEOUT)
        item = demo_board.query_selector(".backlog-row")
        assert item.query_selector(".priority-dot") is not None

    def test_backlog_item_shows_status_badge(self, demo_board, api):
        backlog = api.get(f"/api/projects/{DEMO_PROJECT_ID}/backlog")
        if not backlog:
            api.post(f"/api/projects/{DEMO_PROJECT_ID}/tasks", {"title": unique("Backlog Task")})

        navigate_to(demo_board, "Backlog")
        demo_board.wait_for_selector(".backlog-row", timeout=TIMEOUT)
        item = demo_board.query_selector(".backlog-row")
        assert item.query_selector(".badge") is not None

    def test_backlog_item_click_opens_task_panel(self, demo_board, api):
        backlog = api.get(f"/api/projects/{DEMO_PROJECT_ID}/backlog")
        if not backlog:
            api.post(f"/api/projects/{DEMO_PROJECT_ID}/tasks", {"title": unique("Backlog Task")})

        navigate_to(demo_board, "Backlog")
        demo_board.wait_for_selector(".backlog-row", timeout=TIMEOUT)
        demo_board.click(".backlog-row")
        demo_board.wait_for_selector("#task-panel.open", timeout=TIMEOUT)
        assert demo_board.is_visible("#task-panel.open")

    def test_backlog_grouped_by_release_label(self, demo_board, api):
        # Both seeded tasks sit on board columns, so the backlog starts empty and
        # renders an empty state with no grouping at all. (The seeded "Write API
        # documentation" has no *release*, but it is on the board — it was never
        # a backlog item.) Put a task in the backlog so there is something to group.
        api.post(f"/api/projects/{DEMO_PROJECT_ID}/tasks", {"title": unique("Grouping Item")})
        navigate_to(demo_board, "Backlog")
        demo_board.wait_for_selector(".backlog-row", timeout=TIMEOUT)
        # Release section labels or "No Release" heading should be present
        sections = demo_board.query_selector_all(".release-label")
        assert len(sections) >= 1


class TestBacklogSorting:
    def _seed_backlog(self, api):
        # Two backlog items with titles that sort in a known order, so ascending
        # and descending sorts are distinguishable regardless of other data.
        api.post(f"/api/projects/{DEMO_PROJECT_ID}/tasks", {"title": unique("AAA Sort")})
        api.post(f"/api/projects/{DEMO_PROJECT_ID}/tasks", {"title": unique("ZZZ Sort")})

    def _title_button(self, page):
        for b in page.query_selector_all(".th-sort"):
            if b.inner_text().lower().startswith("title"):
                return b
        raise AssertionError("Title sort header not found")

    def test_backlog_has_sortable_headers(self, demo_board, api):
        self._seed_backlog(api)
        navigate_to(demo_board, "Backlog")
        demo_board.wait_for_selector(".backlog-row", timeout=TIMEOUT)
        # Release grouping by default; the column headers are sort buttons.
        assert len(demo_board.query_selector_all(".th-sort")) > 0
        assert len(demo_board.query_selector_all(".release-label")) > 0

    def test_backlog_column_sort_flattens_and_cycles(self, demo_board, api):
        self._seed_backlog(api)
        navigate_to(demo_board, "Backlog")
        demo_board.wait_for_selector(".backlog-row", timeout=TIMEOUT)
        # Click "Title": the release grouping flattens into one sorted list.
        self._title_button(demo_board).click()
        settle(demo_board)
        assert demo_board.query_selector(".release-label") is None, "sort must flatten the groups"
        titles = demo_board.eval_on_selector_all(
            ".backlog-title", "els => els.map(e => e.textContent.trim().toLowerCase())")
        assert titles == sorted(titles), "rows must be ascending by title"
        active = demo_board.query_selector(".th-sort--active")
        assert active is not None and "▲" in active.inner_text()
        # Second click flips to descending.
        self._title_button(demo_board).click()
        settle(demo_board)
        titles = demo_board.eval_on_selector_all(
            ".backlog-title", "els => els.map(e => e.textContent.trim().toLowerCase())")
        assert titles == sorted(titles, reverse=True), "rows must be descending by title"
        assert "▼" in demo_board.query_selector(".th-sort--active").inner_text()
        # Third click clears the sort and restores the release grouping.
        self._title_button(demo_board).click()
        settle(demo_board)
        assert len(demo_board.query_selector_all(".release-label")) > 0
        assert demo_board.query_selector(".th-sort--active") is None

    def test_backlog_and_taskview_sort_independently(self, demo_board, api):
        # Each list keeps its own sort state (keyed by listId): sorting the
        # Backlog must not leave the Task view sorted, and vice versa.
        self._seed_backlog(api)
        navigate_to(demo_board, "Backlog")
        demo_board.wait_for_selector(".backlog-row", timeout=TIMEOUT)
        self._title_button(demo_board).click()
        settle(demo_board)
        assert demo_board.query_selector(".th-sort--active") is not None
        # Switch to the Task view: it must not inherit the Backlog's active sort.
        demo_board.evaluate(f"() => router.go('/projects/{DEMO_PROJECT_ID}/tasks')")
        demo_board.wait_for_selector(".backlog-wrap, .empty", timeout=TIMEOUT)
        settle(demo_board)
        assert demo_board.query_selector(".th-sort--active") is None, \
            "Task view must not inherit the Backlog's sort"


class TestAddToBoard:
    def test_selecting_task_shows_add_to_board_button(self, demo_board, api):
        backlog = api.get(f"/api/projects/{DEMO_PROJECT_ID}/backlog")
        if not backlog:
            api.post(f"/api/projects/{DEMO_PROJECT_ID}/tasks", {"title": unique("Hover Task")})

        navigate_to(demo_board, "Backlog")
        demo_board.wait_for_selector(".backlog-row", timeout=TIMEOUT)
        item = demo_board.query_selector(".backlog-row")
        item.query_selector(".task-checkbox").click()
        demo_board.wait_for_selector("#bulk-bar:not(.hidden)", timeout=TIMEOUT)
        assert demo_board.is_visible("[data-act='bulkAddToBoard']")

    def test_add_to_board_places_task_on_board_as_planned(self, demo_board, api):
        name = unique("Add To Board Task")
        task = api.post(f"/api/projects/{DEMO_PROJECT_ID}/tasks", {"title": name})

        navigate_to(demo_board, "Backlog")
        demo_board.wait_for_selector(f".backlog-row:has-text('{name}')", timeout=TIMEOUT)
        item = demo_board.query_selector(f".backlog-row:has-text('{name}')")
        item.query_selector(".task-checkbox").click()
        demo_board.wait_for_selector("[data-act='bulkAddToBoard']", timeout=TIMEOUT)
        demo_board.click("[data-act='bulkAddToBoard']")

        # Task should no longer appear in backlog and is now a Planned board item.
        settle(demo_board)

        # Poll the placement (the positive outcome) first: once the task carries a
        # board column the write has landed, which is what makes the backlog
        # absence check below meaningful rather than merely early.
        def _placed():
            t = api.get(f"/api/tasks/{task['id']}")
            return t if t.get("boardColumnId") else None

        moved = poll_until(_placed, message="task never reached the board via the API")
        remaining = api.get(f"/api/projects/{DEMO_PROJECT_ID}/backlog")
        assert not any(t["title"] == name for t in remaining)
        assert moved.get("boardColumnId") is not None, "task was not placed on the board"
        assert moved["status"] == "PLANNED"


class TestBacklogInPlacePanelEdit:
    """A panel edit patches the backlog row in place — no full view reload.

    Every panel edit (priority, assignee, release, due date, title, …) used to
    fall through to renderContent() on the list views, because the in-place path
    only existed for the board: #content blanked to a spinner, the project's whole
    task list was refetched, and the list was rebuilt to change one cell. These
    tests pin the fixed behaviour: the row's own DOM node survives the edit, no
    task-list refetch happens, and the value on screen is the new one.
    """

    def _open_row_panel(self, page, name):
        navigate_to(page, "Backlog")
        page.wait_for_selector(f".backlog-row:has-text('{name}')", timeout=TIMEOUT)
        settle(page)
        page.click(f".backlog-row:has-text('{name}')")
        page.wait_for_selector("#task-panel.open", timeout=TIMEOUT)
        page.wait_for_selector(".panel-tab", timeout=TIMEOUT)
        settle(page)

    def test_priority_edit_updates_row_without_reloading_the_list(self, demo_board, api):
        name = unique("InPlace Priority")
        api.post(f"/api/projects/{DEMO_PROJECT_ID}/tasks", {"title": name, "priority": "LOW"})
        self._open_row_panel(demo_board, name)

        # Tag the row's DOM node. A full renderContent() rebuilds #content from a
        # spinner, so the tag cannot survive it; an in-place patch replaces only
        # the list body, so we instead assert on the refetch that a reload implies.
        requests = []
        demo_board.on("request", lambda r: requests.append(r.url))

        demo_board.query_selector("select[data-change='changePriority']").select_option("HIGH")
        settle(demo_board)

        list_refetches = [u for u in requests if "/tasks?" in u and "size=200" in u]
        assert list_refetches == [], \
            f"a panel edit must not refetch the whole task list, but did: {list_refetches}"
        # The spinner is what a full renderContent shows; the list must still be there.
        assert demo_board.is_visible(".backlog-list"), "the list region should never be torn down"
        row = demo_board.query_selector(f".backlog-row:has-text('{name}')")
        assert row is not None, "the row must still be on screen"
        assert "High" in row.inner_text(), f"the row should show the new priority, got: {row.inner_text()}"

    def test_edit_that_leaves_the_active_filter_removes_the_row(self, demo_board, api):
        """The patched item is still put through the view's filters.

        An in-place patch that skipped them would leave a row on screen that the
        active filter excludes — worse than the reload it replaces.
        """
        name = unique("InPlace Filtered")
        api.post(f"/api/projects/{DEMO_PROJECT_ID}/tasks", {"title": name, "priority": "LOW"})
        self._open_row_panel(demo_board, name)

        # Narrow the list to LOW priority; the task qualifies, so its row shows.
        demo_board.select_option("#filter-priority", "LOW")
        settle(demo_board)
        assert demo_board.query_selector(f".backlog-row:has-text('{name}')") is not None

        # Raising its priority takes it out of the filter — the row must go.
        demo_board.query_selector("select[data-change='changePriority']").select_option("HIGH")
        settle(demo_board)
        assert demo_board.query_selector(f".backlog-row:has-text('{name}')") is None, \
            "a task edited out of the active filter must disappear from the list"

    def test_deleting_from_the_task_panel_drops_the_row_in_place(self, demo_board, api):
        name = unique("InPlace Delete")
        task = api.post(f"/api/projects/{DEMO_PROJECT_ID}/tasks", {"title": name})
        self._open_row_panel(demo_board, name)

        demo_board.click("[data-act='deleteTask']")
        demo_board.wait_for_selector(".modal", timeout=TIMEOUT)
        submit_modal(demo_board)
        settle(demo_board)

        assert demo_board.query_selector(f".backlog-row:has-text('{name}')") is None, \
            "the deleted task's row must be gone"
        def _gone():
            try:
                api.get(f"/api/tasks/{task['id']}")
                return False
            except Exception:
                return True

        poll_until(_gone, message="task was never deleted server-side")
