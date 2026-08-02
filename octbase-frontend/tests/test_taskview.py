"""Tests for the Task view (the configurable task-list engine's management layer)
and its feature flag, in BOTH states.

The Task view is gated by FEATURES.taskView (backend GET /config, env
OCTBASE_FEATURE_TASKVIEW). The backend here runs with the flag ON; tests force
the OFF state per-page with the ?taskView=off URL override (the same hook the
flag exposes for previews), so both states are covered without restarting the API.
"""

import pytest
from urllib.parse import urlparse
from conftest import (
    desktop_url, DEMO_PROJECT_ID,
    SHORT, TIMEOUT, unique, settle, sign_in_if_needed,)


def _load(page, extra_query=""):
    """Boot a fresh app page, optionally with extra URL params (e.g. taskView=off),
    and log in. Returns once the projects list is up."""
    url = desktop_url(extra_query)
    page.goto(url)
    sign_in_if_needed(page)
    page.wait_for_selector("text=Demo Project", timeout=TIMEOUT)
    return page


def _open_tasks(page):
    page.evaluate(f"() => router.go('/projects/{DEMO_PROJECT_ID}/tasks')")
    page.wait_for_selector(".backlog-wrap, .empty", timeout=TIMEOUT)


# ── Flag ON ────────────────────────────────────────────────────────────────────
class TestTaskViewEnabled:
    def test_sidebar_entry_present(self, app):
        # The project sidebar only exists inside a project context.
        app.evaluate(f"() => router.go('/projects/{DEMO_PROJECT_ID}/board')")
        app.wait_for_selector(".board-col", timeout=TIMEOUT)
        assert app.is_visible(".sidebar-item:has-text('Tasks')")

    def test_groups_by_status(self, app):
        _open_tasks(app)
        # Status is the grouping: each group header carries a status badge.
        assert app.query_selector(".release-label .badge") is not None
        # The Tasks sidebar entry is the active nav item.
        active = app.query_selector(".sidebar-item.active")
        assert active is not None and "Tasks" in active.inner_text()

    def test_status_filter_narrows_to_one_group(self, app):
        _open_tasks(app)
        assert app.is_visible("#filter-status")
        app.select_option("#filter-status", "PLANNED")
        settle(app)
        assert app.evaluate("() => S.filters.status") == "PLANNED"
        groups = app.query_selector_all(".release-label")
        # Only the matching group remains (empty group headers dropped when
        # filtering). The label is uppercased by CSS, so compare case-insensitively.
        assert len(groups) == 1 and "PLANNED" in groups[0].inner_text().upper()
        # Clearing the filter brings the other groups back.
        app.select_option("#filter-status", "")
        settle(app)
        assert len(app.query_selector_all(".release-label")) > 1

    def test_backlog_has_no_status_filter(self, app):
        # Status is a Task-view affordance only; the Backlog must not show it, and
        # leaving the Task view must not leak a status filter into the Backlog.
        _open_tasks(app)
        app.select_option("#filter-status", "DONE")
        settle(app)
        app.evaluate(f"() => router.go('/projects/{DEMO_PROJECT_ID}/backlog')")
        app.wait_for_selector(".backlog-wrap, .empty", timeout=TIMEOUT)
        assert app.query_selector("#filter-status") is None
        assert app.evaluate("() => S.filters.status") == ""

    def test_search_filters_and_keeps_focus(self, app, api):
        api.post(f"/api/projects/{DEMO_PROJECT_ID}/tasks",
                 {"title": unique("ZZUNLIKELY")})
        _open_tasks(app)
        before = len(app.query_selector_all(".backlog-row"))
        app.fill("#task-search", "ZZUNLIKELY")
        settle(app)
        after = len(app.query_selector_all(".backlog-row"))
        assert after < before
        # The body-only refresh must keep the search box focused while typing.
        assert app.evaluate("() => document.activeElement && document.activeElement.id") == "task-search"

    def test_search_finds_a_task_by_its_key(self, app, api):
        # The Task view spans board + backlog, so a key typed here has to find a
        # task whichever of the two it currently sits in.
        mine = api.post(f"/api/projects/{DEMO_PROJECT_ID}/tasks", {"title": unique("KeyLookup")})
        _open_tasks(app)
        prefix = app.evaluate("() => S.project.abbreviation || (S.project.slug || '').toUpperCase()")

        app.fill("#task-search", f"{prefix}-{mine['seqNumber']}")
        settle(app)
        rows = app.query_selector_all(".backlog-row")
        assert len(rows) == 1 and mine["title"] in rows[0].inner_text()

    def test_bulk_status_control_is_taskview_only(self, app):
        # Present on the Task view…
        _open_tasks(app)
        app.wait_for_selector(".task-checkbox", timeout=TIMEOUT)
        app.check(".task-checkbox >> nth=0")
        app.wait_for_selector("#bulk-bar:not(.hidden)", timeout=TIMEOUT)
        assert app.is_visible("#bulk-status")
        # …and absent on the Backlog (where Add-to-board is the extra instead).
        app.evaluate(f"() => router.go('/projects/{DEMO_PROJECT_ID}/backlog')")
        app.wait_for_selector(".backlog-wrap, .empty", timeout=TIMEOUT)
        if app.query_selector(".task-checkbox"):
            app.check(".task-checkbox >> nth=0")
            app.wait_for_selector("#bulk-bar:not(.hidden)", timeout=TIMEOUT)
            assert app.query_selector("#bulk-status") is None

    def test_row_does_not_open_panel_while_selected(self, app):
        # Selection mode: with a checkbox checked, clicking a row must NOT open the
        # task-edit side sheet (it would hide the bulk bar). Deselecting restores it.
        _open_tasks(app)
        app.wait_for_selector(".task-checkbox", timeout=TIMEOUT)
        app.check(".task-checkbox >> nth=0")
        app.wait_for_selector("#bulk-bar:not(.hidden)", timeout=TIMEOUT)
        # Click a different row's title; the panel must stay closed.
        app.locator(".backlog-row").nth(1).locator(".backlog-title").click()
        settle(app)
        assert not app.is_visible("#task-panel.open")
        # With nothing selected, the row opens the panel as normal.
        app.uncheck(".task-checkbox >> nth=0")
        settle(app)
        app.locator(".backlog-row").nth(1).locator(".backlog-title").click()
        app.wait_for_selector("#task-panel.open", timeout=TIMEOUT)
        assert app.is_visible("#task-panel.open")

    def test_bulk_set_status_moves_tasks(self, app, api):
        # Two fresh backlog tasks (default PLANNED, not on board → visible here).
        t1 = api.post(f"/api/projects/{DEMO_PROJECT_ID}/tasks", {"title": unique("BulkA")})
        t2 = api.post(f"/api/projects/{DEMO_PROJECT_ID}/tasks", {"title": unique("BulkB")})
        ids = [t1["id"], t2["id"]]
        try:
            _open_tasks(app)
            for tid in ids:
                app.check(f".task-checkbox[data-task-id='{tid}']")
            app.wait_for_selector("#bulk-status", timeout=TIMEOUT)
            app.select_option("#bulk-status", "IN_REVIEW")
            # Re-render after the bulk call; then assert the source of truth.
            settle(app)
            for tid in ids:
                assert api.get(f"/api/tasks/{tid}")["status"] == "IN_REVIEW"
        finally:
            for tid in ids:
                api.delete(f"/api/tasks/{tid}")

    def test_bulk_set_status_reconciles_board(self, app, api):
        # A board task's status is owned by its lane. Bulk-setting its status from
        # the Task view must also move its card to the matching column, so the board
        # doesn't diverge (status DONE while still sitting in the Planned lane).
        board = api.get(f"/api/projects/{DEMO_PROJECT_ID}/boards/default")
        cols = {c["status"]: c for c in board["columns"]}
        planned, review = cols.get("PLANNED"), cols.get("IN_REVIEW")
        assert planned and review, "demo board needs Planned + Review lanes"
        task = api.post(f"/api/projects/{DEMO_PROJECT_ID}/tasks", {"title": unique("Recon")})
        tid = task["id"]
        try:
            api.post(f"/api/boards/{board['id']}/move-task",
                     {"taskId": tid, "boardColumnId": planned["id"], "boardRank": 1000})
            assert api.get(f"/api/tasks/{tid}")["boardColumnId"] == planned["id"]
            _open_tasks(app)
            app.check(f".task-checkbox[data-task-id='{tid}']")
            app.wait_for_selector("#bulk-status", timeout=TIMEOUT)
            app.select_option("#bulk-status", "IN_REVIEW")
            settle(app)
            after = api.get(f"/api/tasks/{tid}")
            assert after["status"] == "IN_REVIEW"
            assert after["boardColumnId"] == review["id"], "card must follow status to the Review lane"
        finally:
            api.delete(f"/api/tasks/{tid}")

    def _title_button(self, page):
        # inner_text() applies the header's CSS uppercase transform, so match
        # case-insensitively.
        for b in page.query_selector_all(".th-sort"):
            if b.inner_text().lower().startswith("title"):
                return b
        raise AssertionError("Title sort header not found")

    def test_column_header_sorts_and_flattens(self, app):
        _open_tasks(app)
        # Default: grouped by status (status-badge group headers present).
        assert len(app.query_selector_all(".release-label")) > 0
        # Click "Title": the status grouping flattens into one sorted list.
        self._title_button(app).click()
        settle(app)
        assert app.query_selector(".release-label") is None, "sort must flatten the groups"
        titles = app.eval_on_selector_all(".backlog-title", "els => els.map(e => e.textContent.trim().toLowerCase())")
        assert titles == sorted(titles), "rows must be ascending by title"
        active = app.query_selector(".th-sort--active")
        assert active is not None and "▲" in active.inner_text()
        # Second click flips to descending.
        self._title_button(app).click()
        settle(app)
        titles = app.eval_on_selector_all(".backlog-title", "els => els.map(e => e.textContent.trim().toLowerCase())")
        assert titles == sorted(titles, reverse=True), "rows must be descending by title"
        assert "▼" in app.query_selector(".th-sort--active").inner_text()
        # Third click clears the sort and restores the status grouping.
        self._title_button(app).click()
        settle(app)
        assert len(app.query_selector_all(".release-label")) > 0
        assert app.query_selector(".th-sort--active") is None

    def test_sorting_keeps_search_and_status_filter(self, app):
        # Regression: sorting re-renders all of #content, which used to drop the
        # list toolbar (search box + status filter) prepended after the initial
        # render. Sorting must re-insert it so the controls stay usable.
        _open_tasks(app)
        assert app.is_visible("#task-search")
        assert app.is_visible("#filter-status")
        self._title_button(app).click()
        settle(app)
        assert app.query_selector(".release-label") is None, "sort must flatten the groups"
        assert app.is_visible("#task-search"), "search box must survive a sort"
        assert app.is_visible("#filter-status"), "status filter must survive a sort"

    def test_row_delete_button_removes_task(self, app, api):
        task = api.post(f"/api/projects/{DEMO_PROJECT_ID}/tasks", {"title": unique("RowDel")})
        tid = task["id"]
        deleted = False
        try:
            _open_tasks(app)
            app.click(f".backlog-row:has(input[data-task-id='{tid}']) .row-delete")
            app.click("#modal-submit")
            settle(app)
            with pytest.raises(Exception):
                api.get(f"/api/tasks/{tid}")  # 404 → raises
            deleted = True
        finally:
            if not deleted:
                api.delete(f"/api/tasks/{tid}")

    def test_row_delete_of_parent_shows_clear_error(self, app, api):
        # A task with subtasks cannot be deleted outright. The per-row delete must
        # surface the backend's TASK_HAS_CHILDREN reason (not fail silently).
        story = api.post(f"/api/projects/{DEMO_PROJECT_ID}/tasks",
                         {"title": unique("Parent"), "taskType": "STORY"})
        child = api.post(f"/api/projects/{DEMO_PROJECT_ID}/tasks",
                         {"title": unique("Child"), "taskType": "TASK", "parentId": story["id"]})
        try:
            _open_tasks(app)
            app.click(f".backlog-row:has(input[data-task-id='{story['id']}']) .row-delete")
            app.click("#modal-submit")
            toast = app.wait_for_selector(".toast-error", timeout=TIMEOUT)
            assert "subtask" in toast.inner_text().lower()
            assert api.get(f"/api/tasks/{story['id']}")["id"] == story["id"], "parent must survive"
        finally:
            api.delete(f"/api/tasks/{child['id']}")
            api.delete(f"/api/tasks/{story['id']}")

    def test_bulk_delete_of_parent_reports_partial(self, app, api):
        # Bulk-deleting only a parent used to report a plain success while deleting
        # nothing. It must now report an honest partial/failure and keep the task.
        story = api.post(f"/api/projects/{DEMO_PROJECT_ID}/tasks",
                         {"title": unique("BulkParent"), "taskType": "STORY"})
        child = api.post(f"/api/projects/{DEMO_PROJECT_ID}/tasks",
                         {"title": unique("BulkChild"), "taskType": "TASK", "parentId": story["id"]})
        try:
            _open_tasks(app)
            app.check(f".task-checkbox[data-task-id='{story['id']}']")
            app.wait_for_selector("[data-act='bulkDelete']", timeout=TIMEOUT)
            app.click("[data-act='bulkDelete']")
            app.click("#modal-submit")
            toast = app.wait_for_selector(".toast-error, .toast-info", timeout=TIMEOUT)
            assert "subtask" in toast.inner_text().lower()
            assert api.get(f"/api/tasks/{story['id']}")["id"] == story["id"], "parent must survive"
        finally:
            api.delete(f"/api/tasks/{child['id']}")
            api.delete(f"/api/tasks/{story['id']}")


# ── Flag OFF ─────────────────────────────────────────────────────────────────
class TestTaskViewDisabled:
    def test_no_sidebar_entry(self, page):
        _load(page, "&taskView=off")
        page.evaluate(f"() => router.go('/projects/{DEMO_PROJECT_ID}/board')")
        page.wait_for_selector(".board-col", timeout=TIMEOUT)
        assert page.query_selector(".sidebar-item:has-text('Tasks')") is None
        assert page.query_selector(".view-switch") is None

    def test_stale_tasks_url_falls_back_gracefully(self, page):
        # JS exceptions and real console errors only — file:// pages emit spurious
        # ERR_FILE_NOT_FOUND resource noise (favicon/fonts) that is unrelated to the
        # fallback and present on every page.
        errors = []
        page.on("pageerror", lambda e: errors.append(str(e)))
        page.on("console", lambda m: errors.append(m.text)
                if (m.type == "error" and "Failed to load resource" not in m.text) else None)
        _load(page, "&taskView=off")
        page.evaluate(f"() => router.go('/projects/{DEMO_PROJECT_ID}/tasks')")
        # Never blank, never the Task view — degrades to the Backlog.
        page.wait_for_selector(".backlog-wrap, .empty", timeout=TIMEOUT)
        assert page.evaluate("() => S.view") == "backlog"
        assert errors == [], f"console/page errors on fallback: {errors}"
