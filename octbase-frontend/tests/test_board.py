"""Tests for the Kanban board view: columns, cards, creation, drag-and-drop."""

import pytest
from conftest import (
    DEMO_PROJECT_ID, DEMO_PROJECT_NAME, SHORT, TIMEOUT,
    fill_modal, html5_drag, navigate_to, submit_modal, toast_text, unique, settle,)

EXPECTED_COLUMNS = ["PLANNED", "IN PROGRESS", "REVIEW", "DONE"]


class TestCrossProjectLinkedColumn:
    def test_linked_column_from_another_project(self, app, api):
        # Source project with a board, a column, and a task in that column.
        src = api.post("/api/projects", {"name": unique("Src Proj"), "visibility": "PRIVATE"})
        src_board = api.post(f"/api/projects/{src['id']}/boards",
                             {"name": "Src Board", "template": "kanban"})
        src_col = src_board["columns"][0]
        task = api.post(f"/api/projects/{src['id']}/tasks", {"title": unique("Linked Card")})
        api.post(f"/api/boards/{src_board['id']}/move-task",
                 {"taskId": task["id"], "boardColumnId": src_col["id"]})

        # Dest project (different project) links the source column.
        dst = api.post("/api/projects", {"name": unique("Dst Proj"), "visibility": "PRIVATE"})
        dst_board = api.post(f"/api/projects/{dst['id']}/boards",
                             {"name": "Dst Board", "template": "kanban", "isDefault": True})
        api.post(f"/api/boards/{dst_board['id']}/external-columns",
                 {"sourceColumnId": src_col["id"]})

        try:
            app.evaluate(f"() => router.go('/projects/{dst['id']}/board')")
            app.wait_for_selector(".board-col-external", timeout=TIMEOUT)
            ext = app.query_selector(".board-col-external")
            text = ext.inner_text()
            assert src["name"] in text, "linked column should show the source project name"
            assert task["title"] in text, "linked column should show the source column's task"
        finally:
            api.delete(f"/api/projects/{src['id']}")
            api.delete(f"/api/projects/{dst['id']}")


class TestBacklogColumn:
    def test_toggle_shows_hides_and_persists_backlog_column(self, demo_board):
        # Hidden by default.
        assert demo_board.query_selector(".board-col-backlog") is None

        # Toggle on -> leftmost backlog column appears.
        demo_board.click('[data-act="toggleBacklogColumn"]')
        demo_board.wait_for_selector(".board-col-backlog", timeout=TIMEOUT)
        first_col = demo_board.query_selector(".board-cols > .board-col")
        assert "board-col-backlog" in (first_col.get_attribute("class") or "")

        # Preference persists across reloads (localStorage).
        demo_board.reload()
        demo_board.wait_for_selector(".board-col-backlog", timeout=TIMEOUT)

        # Toggle off -> column removed.
        demo_board.click('[data-act="toggleBacklogColumn"]')
        demo_board.wait_for_selector(".board-col-backlog", state="detached", timeout=TIMEOUT)
        assert demo_board.query_selector(".board-col-backlog") is None

    def test_drag_lane_card_to_backlog_removes_from_board(self, demo_board, api):
        """Dragging a lane card back onto the backlog column takes it off the
        board (the reverse of dragging a backlog card onto a lane)."""
        page = demo_board
        board = api.get(f"/api/projects/{DEMO_PROJECT_ID}/boards/default")
        lane = board["columns"][0]

        # A fresh task placed on the first lane, to be dragged back to the backlog.
        task = api.post(f"/api/projects/{DEMO_PROJECT_ID}/tasks", {"title": unique("Back To Backlog")})
        api.post(f"/api/boards/{board['id']}/move-task",
                 {"taskId": task["id"], "boardColumnId": lane["id"]})

        # Show the backlog column; the toggle re-renders the board (re-fetching
        # tasks), so the freshly-added lane card appears too.
        page.click('[data-act="toggleBacklogColumn"]')
        page.wait_for_selector(".board-col-backlog", timeout=TIMEOUT)
        page.wait_for_selector(
            f".board-col:not(.board-col-backlog) .board-card[data-task-id='{task['id']}']", timeout=TIMEOUT
        )

        # Drive the app's real drag handlers (wired on document via S.dragging).
        # Playwright's mouse events do not fire native HTML5 drag, so dispatch the
        # dragstart/dragover/drop sequence with a shared DataTransfer directly.
        page.evaluate(
            """(taskId) => {
                const src = document.querySelector(
                    `.board-col:not(.board-col-backlog) .board-card[data-task-id='${taskId}']`);
                const backlog = document.querySelector('.board-col-backlog');
                const dt = new DataTransfer();
                const fire = (el, type) => el.dispatchEvent(
                    new DragEvent(type, { bubbles: true, cancelable: true, dataTransfer: dt, clientY: 300 }));
                fire(src, 'dragstart');
                fire(backlog, 'dragover');
                fire(backlog, 'drop');
                fire(src, 'dragend');
            }""",
            task["id"],
        )
        settle(page)
        # The task is off the board (BoardColumnID cleared -> back in the backlog).
        moved = api.get(f"/api/tasks/{task['id']}")
        assert moved.get("boardColumnId") is None, "task was not removed from the board"

        # And it now renders as a backlog card rather than a lane card.
        page.wait_for_selector(
            f".board-col-backlog .board-card[data-task-id='{task['id']}']", timeout=TIMEOUT
        )

    def test_drag_to_backlog_does_not_reload_the_board(self, demo_board, api):
        """The drop repaints from the write's own response, so nothing is
        refetched and the board is not rebuilt under the pointer.

        This is the regression guard for the drop that used to re-render
        unconditionally: it refetched the project's whole task list and replaced
        the content area — search box included — for a move remove-task had
        already described in its 200.
        """
        page = demo_board
        board = api.get(f"/api/projects/{DEMO_PROJECT_ID}/boards/default")
        lane = board["columns"][0]

        task = api.post(f"/api/projects/{DEMO_PROJECT_ID}/tasks", {"title": unique("No Reload")})
        api.post(f"/api/boards/{board['id']}/move-task",
                 {"taskId": task["id"], "boardColumnId": lane["id"]})

        page.click('[data-act="toggleBacklogColumn"]')
        page.wait_for_selector(".board-col-backlog", timeout=TIMEOUT)
        page.wait_for_selector(
            f".board-col:not(.board-col-backlog) .board-card[data-task-id='{task['id']}']", timeout=TIMEOUT
        )

        # Put the cursor in the board's search box. A full re-render replaces the
        # toolbar the input lives in, so surviving focus is the user-visible half
        # of "no reload" — and the half a user actually notices, mid-typing.
        page.focus("#board-search")

        # Only requests made from the drop onward count; the setup above legitimately
        # refetches (the backlog toggle re-renders).
        seen = []
        page.on("request", lambda r: seen.append(f"{r.method} {r.url}"))

        page.evaluate(
            """(taskId) => {
                const src = document.querySelector(
                    `.board-col:not(.board-col-backlog) .board-card[data-task-id='${taskId}']`);
                const backlog = document.querySelector('.board-col-backlog');
                const dt = new DataTransfer();
                const fire = (el, type) => el.dispatchEvent(
                    new DragEvent(type, { bubbles: true, cancelable: true, dataTransfer: dt, clientY: 300 }));
                fire(src, 'dragstart');
                fire(backlog, 'dragover');
                fire(backlog, 'drop');
                fire(src, 'dragend');
            }""",
            task["id"],
        )
        settle(page)
        page.wait_for_selector(
            f".board-col-backlog .board-card[data-task-id='{task['id']}']", timeout=TIMEOUT
        )

        # The write itself must be in the log, or the two assertions below pass
        # for a drag that never happened.
        assert [r for r in seen if "remove-task" in r], \
            f"the drop did not call remove-task; requests seen: {seen}"
        refetch = [r for r in seen
                   if f"/projects/{DEMO_PROJECT_ID}/tasks" in r or f"/projects/{DEMO_PROJECT_ID}/backlog" in r]
        assert not refetch, f"the drop refetched the board instead of patching it: {refetch}"

        assert page.evaluate("() => document.activeElement && document.activeElement.id") == "board-search", \
            "the board was re-rendered under the pointer — the search box lost focus"


class TestBoardStructure:
    def test_board_has_four_columns(self, demo_board):
        cols = demo_board.query_selector_all(".board-col")
        assert len(cols) == 4

    def test_column_names_match_statuses(self, demo_board):
        headers = demo_board.query_selector_all(".board-col-header")
        names = [h.inner_text().split("\n")[0].strip() for h in headers]
        for expected in EXPECTED_COLUMNS:
            assert any(expected in n.upper() for n in names), f"Column '{expected}' not found"

    def test_each_column_has_add_task_button(self, demo_board):
        btns = demo_board.query_selector_all(".col-add-btn")
        assert len(btns) == 4

    def test_column_shows_task_count_badge(self, demo_board):
        counts = demo_board.query_selector_all(".badge-muted")
        assert len(counts) == 4
        # At least one column has tasks
        values = [c.inner_text().strip() for c in counts]
        assert any(v != "0" for v in values)

    def test_create_task_button_in_topbar(self, demo_board):
        assert demo_board.is_visible("[data-act='showCreateTask']")


class TestBoardCards:
    def test_cards_visible_on_board(self, demo_board):
        cards = demo_board.query_selector_all(".board-card")
        assert len(cards) >= 2

    def test_card_shows_type_badge(self, demo_board):
        card = demo_board.query_selector(".board-card")
        badge = card.query_selector(".type-badge")
        assert badge is not None
        assert badge.inner_text().strip() != ""

    def test_card_shows_title(self, demo_board):
        card = demo_board.query_selector(".board-card")
        title = card.query_selector(".card-title")
        assert title is not None
        assert title.inner_text().strip() != ""

    def test_card_shows_priority_dot(self, demo_board):
        card = demo_board.query_selector(".board-card")
        dot = card.query_selector(".priority-dot")
        assert dot is not None

    def test_card_shows_release_tag_when_set(self, demo_board):
        # The seeded "Implement user authentication" task has release "v1.0 Launch"
        card = demo_board.query_selector(".board-card:has-text('Implement user authentication')")
        assert card is not None
        tag = card.query_selector(".release-tag")
        assert tag is not None
        assert "v1.0" in tag.inner_text()

    def test_card_shows_assignee_avatar_when_set(self, demo_board):
        card = demo_board.query_selector(".board-card:has-text('Implement user authentication')")
        avatar = card.query_selector(".avatar-sm")
        assert avatar is not None

    def test_card_click_opens_task_panel(self, demo_board):
        demo_board.click(".board-card")
        demo_board.wait_for_selector("#task-panel.open", timeout=TIMEOUT)
        assert demo_board.is_visible("#task-panel.open")

    def test_task_in_correct_column(self, demo_board):
        # "Implement user authentication" is IN_PROGRESS in seed data
        in_progress_col = demo_board.query_selector(
            ".board-col:has(.board-col-header:has-text('In Progress'))"
        )
        assert in_progress_col is not None
        card = in_progress_col.query_selector(".board-card:has-text('Implement user authentication')")
        assert card is not None


class TestCreateTaskFromBoard:
    def test_add_task_button_opens_modal(self, demo_board):
        demo_board.click(".col-add-btn")
        demo_board.wait_for_selector("#modal-backdrop:not(.hidden)", timeout=SHORT)
        assert demo_board.is_visible(".modal-title")

    def test_create_task_from_topbar_appears_on_board(self, demo_board):
        name = unique("Board Task")
        demo_board.click(".board-toolbar [data-act='showCreateTask']")
        demo_board.wait_for_selector("#task-title", timeout=SHORT)
        fill_modal(demo_board, {"task-title": name})
        submit_modal(demo_board)
        settle(demo_board)
        # Created from the board view, the task is auto-placed on the board
        assert demo_board.is_visible(f".board-card:has-text('{name}')")

    def test_add_task_from_column_places_task_on_board(self, demo_board):
        name = unique("Column Task")
        # Click "+ Add task" on the first column (Planned). There is one such
        # button per column, so ".col-add-btn" matches multiple elements and
        # demo_board.click() (strict mode) would fail — pick the first.
        demo_board.locator(".col-add-btn").first.click()
        demo_board.wait_for_selector("#task-title", timeout=SHORT)
        fill_modal(demo_board, {"task-title": name})
        submit_modal(demo_board)
        settle(demo_board)
        # Card should appear on the board
        card = demo_board.query_selector(f".board-card:has-text('{name}')")
        assert card is not None

    def test_add_task_from_column_lands_in_that_lane(self, demo_board):
        # Adding from a non-first lane's "+ Add task" must place the card in
        # that lane, not the default first lane.
        name = unique("Review Lane Task")
        review_col = demo_board.locator(
            ".board-col:has(.board-col-header:has-text('Review'))"
        )
        review_col.locator(".col-add-btn").click()
        demo_board.wait_for_selector("#task-title", timeout=SHORT)
        fill_modal(demo_board, {"task-title": name})
        submit_modal(demo_board)
        settle(demo_board)
        assert review_col.locator(f".board-card:has-text('{name}')").count() == 1
        planned_col = demo_board.locator(
            ".board-col:has(.board-col-header:has-text('Planned'))"
        )
        assert planned_col.locator(f".board-card:has-text('{name}')").count() == 0


class TestEndOfLaneAndPinning:
    def _lane_titles(self, lane):
        return lane.locator(".board-card .card-title").all_inner_texts()

    def test_new_task_appends_to_end_of_lane(self, demo_board):
        lane = demo_board.locator(".board-col").first
        a, b = unique("Lane A"), unique("Lane B")
        for name in (a, b):
            lane.locator(".col-add-btn").click()
            demo_board.wait_for_selector("#task-title", timeout=SHORT)
            fill_modal(demo_board, {"task-title": name})
            submit_modal(demo_board)
            settle(demo_board)
        titles = self._lane_titles(lane)
        # Both created here; the later one (b) must sit after a, and at the very end.
        assert titles.index(a) < titles.index(b)
        assert titles[-1] == b

    def test_pin_floats_card_to_top_then_unpin_restores(self, demo_board):
        lane = demo_board.locator(".board-col").first
        name = unique("Pin Me")
        lane.locator(".col-add-btn").click()
        demo_board.wait_for_selector("#task-title", timeout=SHORT)
        fill_modal(demo_board, {"task-title": name})
        submit_modal(demo_board)
        settle(demo_board)
        assert self._lane_titles(lane)[-1] == name  # appended at the end

        def pinned_titles():
            return lane.locator(".board-card-pinned .card-title").all_inner_texts()

        # Pin -> the card joins the pinned group; nothing unpinned sits above it.
        card = lane.locator(f".board-card:has-text('{name}')")
        card.hover()
        card.locator(".card-pin-btn").click()
        settle(demo_board)
        titles, pinned = self._lane_titles(lane), pinned_titles()
        assert name in pinned
        assert all(t in pinned for t in titles[:titles.index(name)])

        # Unpin -> the card leaves the pinned group and drops below all pinned cards.
        card = lane.locator(f".board-card:has-text('{name}')")
        card.hover()
        card.locator(".card-pin-btn").click()
        settle(demo_board)
        titles, pinned = self._lane_titles(lane), pinned_titles()
        assert name not in pinned
        assert all(t not in pinned for t in titles[titles.index(name):])


class TestBoardNotFound:
    def test_new_project_auto_creates_board(self, app, api):
        # Create a fresh project that has no board yet
        name = unique("No-Board Project")
        project = api.post("/api/projects", {"name": name, "visibility": "PRIVATE"})
        pid = project["id"]
        try:
            # Navigate to that project from the sidebar (reload page to pick it up)
            app.reload()
            app.wait_for_selector("text=Demo Project", timeout=TIMEOUT)
            app.click(f"text={name}")
            app.wait_for_selector(".board-col", timeout=TIMEOUT)

            # The board view auto-creates a default board with four columns
            cols = app.query_selector_all(".board-col")
            assert len(cols) == 4

            board = api.get(f"/api/projects/{pid}/boards/default")
            assert len(board["columns"]) == 4
        finally:
            api.delete(f"/api/projects/{pid}")

    def test_auto_created_board_has_expected_column_statuses(self, app, api):
        name = unique("Board Creation Project")
        project = api.post("/api/projects", {"name": name, "visibility": "PRIVATE"})
        pid = project["id"]
        try:
            app.reload()
            app.wait_for_selector("text=Demo Project", timeout=TIMEOUT)
            app.click(f"text={name}")
            app.wait_for_selector(".board-col", timeout=TIMEOUT)

            board = api.get(f"/api/projects/{pid}/boards/default")
            statuses = [c["status"] for c in board["columns"]]
            assert statuses == ["PLANNED", "IN_PROGRESS", "IN_REVIEW", "DONE"]
        finally:
            api.delete(f"/api/projects/{pid}")


class TestDragAndDrop:
    def test_drag_card_to_different_column(self, demo_board, api):
        # Find a card in Review and drag it to In Progress. (Done is excluded as
        # a target: tasks become immutable once DONE, so the status couldn't be
        # reverted afterwards to avoid polluting other tests.)
        source = demo_board.query_selector(
            ".board-col:has(.board-col-header:has-text('Review')) .board-card"
        )
        if source is None:
            pytest.skip("No card in Review column to drag")

        task_id = source.get_attribute("data-task-id")
        # Remember the card's original lane + board so it can be put back exactly,
        # not just its status (a status change alone doesn't move the board lane).
        orig_col = api.get(f"/api/tasks/{task_id}")["boardColumnId"]
        board_id = demo_board.evaluate("() => S.board && S.board.id")

        source_loc = demo_board.locator(
            ".board-col:has(.board-col-header:has-text('Review')) .board-card"
        ).first
        target_loc = demo_board.locator(
            ".board-col:has(.board-col-header:has-text('In Progress')) .board-col-tasks"
        )

        # Native HTML5 drag — Playwright's drag_to only fires mouse events, which
        # the board's DragEvent listeners ignore (see html5_drag).
        html5_drag(demo_board, source_loc, target_loc)
        settle(demo_board)
        # Verify via API that task moved
        task = api.get(f"/api/tasks/{task_id}")
        assert task["status"] == "IN_PROGRESS"
        # Check via UI: card should now be in In Progress column
        target_col = demo_board.query_selector(
            ".board-col:has(.board-col-header:has-text('In Progress'))"
        )
        assert target_col is not None
        moved_card = target_col.query_selector(f"[data-task-id='{task_id}']")
        assert moved_card is not None

        # Restore the original lane and status so other tests (and the seeded
        # board) are unaffected — move the card back, then revert the status.
        api.post(
            f"/api/boards/{board_id}/move-task",
            {"taskId": task_id, "boardColumnId": orig_col, "boardRank": 1000},
        )
        api.post(f"/api/tasks/{task_id}/status", {"status": "IN_REVIEW"})


class TestBoardReorderWithinLane:
    """Dragging a card within its own lane reorders it — writing only that card.

    The BOARD_RANK_STEP gaps between cards exist so one card can be slotted
    between its new neighbours. Re-ranking the whole lane instead made a drop
    cost one PATCH per card in it, which is what made dropping into a busy lane
    slow. Own project, so the drops don't disturb the seeded demo board.
    """

    def _seed_lane(self, api, cards=5):
        proj = api.post("/api/projects", {"name": unique("Reorder Proj"), "visibility": "PRIVATE"})
        board = api.post(f"/api/projects/{proj['id']}/boards",
                         {"name": "Board", "template": "kanban", "isDefault": True})
        col = board["columns"][0]
        task_ids = []
        for i in range(cards):
            task = api.post(f"/api/projects/{proj['id']}/tasks", {"title": unique(f"Card {i}")})
            api.post(f"/api/boards/{board['id']}/move-task",
                     {"taskId": task["id"], "boardColumnId": col["id"], "boardRank": (i + 1) * 1000})
            task_ids.append(task["id"])
        return proj, col, task_ids

    def _lane_order(self, page, col_id):
        return page.eval_on_selector_all(
            f".board-col[data-col-id='{col_id}'] .board-card",
            "els => els.map(e => e.dataset.taskId)")

    def test_drag_last_card_to_top_reorders_lane(self, app, api):
        proj, col, task_ids = self._seed_lane(api)
        moved = task_ids[-1]
        try:
            app.evaluate(f"() => router.go('/projects/{proj['id']}/board')")
            app.wait_for_selector(".board-card", timeout=TIMEOUT)
            settle(app)
            assert self._lane_order(app, col["id"]) == task_ids

            lane = app.locator(f".board-col[data-col-id='{col['id']}'] .board-col-tasks")
            last = app.locator(f".board-col[data-col-id='{col['id']}'] .board-card").last
            html5_drag(app, last, lane, at="top")
            settle(app)

            assert self._lane_order(app, col["id"])[0] == moved, \
                "card dropped at the top of the lane should render first"
            # And it persisted: the server ranks it ahead of the old first card.
            assert (api.get(f"/api/tasks/{moved}")["boardRank"]
                    < api.get(f"/api/tasks/{task_ids[0]}")["boardRank"])
        finally:
            api.delete(f"/api/projects/{proj['id']}")

    def test_drag_within_lane_writes_only_the_moved_card(self, app, api):
        proj, col, task_ids = self._seed_lane(api)
        moved = task_ids[-1]
        before = {tid: api.get(f"/api/tasks/{tid}")["version"] for tid in task_ids}
        try:
            app.evaluate(f"() => router.go('/projects/{proj['id']}/board')")
            app.wait_for_selector(".board-card", timeout=TIMEOUT)
            settle(app)

            lane = app.locator(f".board-col[data-col-id='{col['id']}'] .board-col-tasks")
            last = app.locator(f".board-col[data-col-id='{col['id']}'] .board-card").last
            html5_drag(app, last, lane, at="top")
            settle(app)

            after = {tid: api.get(f"/api/tasks/{tid}")["version"] for tid in task_ids}
            rewritten = [tid for tid in task_ids if after[tid] != before[tid]]
            assert rewritten == [moved], \
                f"a drop should rewrite only the dragged card, but wrote {len(rewritten)}"
        finally:
            api.delete(f"/api/projects/{proj['id']}")

    # ── Drop position after the dragover geometry rewrite ───────────────────
    # A lane's card midpoints are now measured once per drag and binary-searched,
    # instead of every card's rect being re-read on every dragover event. The
    # cases below are the ones a cached-geometry bug lands wrong: the tail of the
    # lane (where "past the last midpoint" has to mean "append", not "insert at
    # the last card"), a lane with no cards to measure at all, and a lane the
    # pointer arrives in from a different lane.

    def test_drag_first_card_to_bottom_appends_it(self, app, api):
        proj, col, task_ids = self._seed_lane(api)
        moved = task_ids[0]
        try:
            app.evaluate(f"() => router.go('/projects/{proj['id']}/board')")
            app.wait_for_selector(".board-card", timeout=TIMEOUT)
            settle(app)
            assert self._lane_order(app, col["id"]) == task_ids

            lane = app.locator(f".board-col[data-col-id='{col['id']}'] .board-col-tasks")
            first = app.locator(f".board-col[data-col-id='{col['id']}'] .board-card").first
            # "bottom" drops in the empty area below the last card.
            html5_drag(app, first, lane, at="bottom")
            settle(app)

            assert self._lane_order(app, col["id"])[-1] == moved, \
                "card dropped below the last card should render last"
            assert (api.get(f"/api/tasks/{moved}")["boardRank"]
                    > api.get(f"/api/tasks/{task_ids[-1]}")["boardRank"])
        finally:
            api.delete(f"/api/projects/{proj['id']}")

    def test_drag_into_an_empty_lane(self, app, api):
        proj, col, task_ids = self._seed_lane(api, cards=3)
        moved = task_ids[1]
        try:
            # A second, empty lane on the same board: nothing to measure in it.
            board = api.get(f"/api/projects/{proj['id']}/boards")[0]
            empty = api.post(f"/api/boards/{board['id']}/columns",
                             {"name": "Empty Lane", "status": "IN_REVIEW", "position": 1})
            empty_id = next(c["id"] for c in api.get(f"/api/boards/{board['id']}")["columns"]
                            if c["name"] == "Empty Lane")

            app.evaluate(f"() => router.go('/projects/{proj['id']}/board')")
            app.wait_for_selector(".board-card", timeout=TIMEOUT)
            settle(app)
            assert self._lane_order(app, empty_id) == []

            card = app.locator(f".board-card[data-task-id='{moved}']")
            target = app.locator(f".board-col[data-col-id='{empty_id}'] .board-col-tasks")
            html5_drag(app, card, target)
            settle(app)

            assert self._lane_order(app, empty_id) == [moved], \
                "a card dropped into an empty lane should be its only card"
            assert api.get(f"/api/tasks/{moved}")["boardColumnId"] == empty_id
            assert moved not in self._lane_order(app, col["id"])
            assert empty is not None
        finally:
            api.delete(f"/api/projects/{proj['id']}")

    def test_drag_across_lanes_lands_at_the_pointer(self, app, api):
        """A card dragged into a populated *other* lane lands where the pointer is.

        The snapshot is keyed on the lane, so arriving in a second lane has to
        measure that lane rather than reuse the first one's midpoints.
        """
        proj, col, task_ids = self._seed_lane(api, cards=3)
        try:
            board = api.get(f"/api/projects/{proj['id']}/boards")[0]
            api.post(f"/api/boards/{board['id']}/columns",
                     {"name": "Second Lane", "status": "IN_REVIEW", "position": 1})
            second_id = next(c["id"] for c in api.get(f"/api/boards/{board['id']}")["columns"]
                             if c["name"] == "Second Lane")
            # Give the second lane two cards of its own to land between.
            others = []
            for i in range(2):
                task = api.post(f"/api/projects/{proj['id']}/tasks", {"title": unique(f"Other {i}")})
                api.post(f"/api/boards/{board['id']}/move-task",
                         {"taskId": task["id"], "boardColumnId": second_id,
                          "boardRank": (i + 1) * 1000})
                others.append(task["id"])

            moved = task_ids[0]
            app.evaluate(f"() => router.go('/projects/{proj['id']}/board')")
            app.wait_for_selector(".board-card", timeout=TIMEOUT)
            settle(app)
            assert self._lane_order(app, second_id) == others

            card = app.locator(f".board-card[data-task-id='{moved}']")
            target = app.locator(f".board-col[data-col-id='{second_id}'] .board-col-tasks")
            html5_drag(app, card, target, at="top")
            settle(app)

            assert self._lane_order(app, second_id) == [moved] + others, \
                "dropping at the top of another lane should place the card first there"
            assert api.get(f"/api/tasks/{moved}")["boardColumnId"] == second_id
        finally:
            api.delete(f"/api/projects/{proj['id']}")


class TestBoardDropSlotMaths:
    """dropSlot, the drag-drop insertion-index search, driven in the real engine.

    The lane's card midpoints are measured once per drag and this binary-searches
    the pointer against them, replacing a per-event rect read of every card. The
    node unit suite (js/views-board.test.js) probes it exhaustively against the
    previous linear scan; this exercises the same function as actually shipped and
    loaded in the browser, over the boundaries a wrong search gets wrong.
    """

    def test_drop_slot_boundaries(self, app):
        cases = [
            # (midpoints, pointerY, expected index)
            ([], 0, 0),                 # empty lane
            ([], 5000, 0),
            ([100, 200, 300], 0, 0),    # above every card -> first
            ([100, 200, 300], 99, 0),
            ([100, 200, 300], 100, 1),  # past the first midpoint -> between 1 and 2
            ([100, 200, 300], 199, 1),
            ([100, 200, 300], 200, 2),
            ([100, 200, 300], 300, 3),  # past every midpoint -> append
            ([100, 200, 300], 9999, 3),
            ([50], 49, 0),
            ([50], 50, 1),
        ]
        for mids, y, expected in cases:
            got = app.evaluate("([mids, y]) => dropSlot(mids, y)", [mids, y])
            assert got == expected, f"dropSlot({mids}, {y}) returned {got}, expected {expected}"


class TestBoardSelectionRemoved:
    """The board's cards carry no selection checkboxes and the bottom bulk-action
    bar never shows on the board — bulk actions are a backlog-only affordance."""

    def test_board_cards_have_no_checkbox(self, demo_board):
        demo_board.wait_for_selector(".board-card", timeout=TIMEOUT)
        assert demo_board.query_selector(".board-card .task-checkbox") is None


class TestBulkActions:
    """Bottom bulk-action bar (backlog only): delete uses the modal overlay rather
    than a native confirm dialog."""

    def test_bulk_delete_uses_modal_overlay(self, demo_board, api):
        name = unique("Bulk Delete Task")
        api.post(f"/api/projects/{DEMO_PROJECT_ID}/tasks", {"title": name})

        navigate_to(demo_board, "Backlog")
        row = demo_board.wait_for_selector(f".backlog-row:has-text('{name}')", timeout=TIMEOUT)
        row.query_selector(".task-checkbox").click()
        demo_board.wait_for_selector("#bulk-bar:not(.hidden)", timeout=TIMEOUT)
        demo_board.click("[data-act='bulkDelete']")

        # The modal overlay must appear (same as the side panel's delete), with a
        # danger submit button — not a native confirm() dialog.
        demo_board.wait_for_selector("#modal-submit", timeout=SHORT)
        assert demo_board.is_visible("#modal-submit")
        assert "btn-danger" in (demo_board.get_attribute("#modal-submit", "class") or "")

        demo_board.click("#modal-submit")
        settle(demo_board)
        assert demo_board.locator(f".backlog-row:has-text('{name}')").count() == 0

    def test_bulk_archive_uses_modal_overlay(self, demo_board, api):
        name = unique("Bulk Archive Task")
        # No "status" here: a task is always created PLANNED, and create now
        # rejects the key instead of dropping it. This call used to send
        # status=DONE and silently get a PLANNED task — the test never noticed,
        # because bulk-archive only needs the row to exist.
        api.post(f"/api/projects/{DEMO_PROJECT_ID}/tasks", {"title": name})

        navigate_to(demo_board, "Backlog")
        row = demo_board.wait_for_selector(f".backlog-row:has-text('{name}')", timeout=TIMEOUT)
        row.query_selector(".task-checkbox").click()
        demo_board.wait_for_selector("#bulk-bar:not(.hidden)", timeout=TIMEOUT)
        demo_board.click("[data-act='bulkArchive']")

        # Same modal overlay as bulk delete, not a native confirm() dialog.
        demo_board.wait_for_selector("#modal-submit", timeout=SHORT)
        assert demo_board.is_visible("#modal-submit")

        demo_board.click("#modal-submit")
        settle(demo_board)
        assert demo_board.locator(f".backlog-row:has-text('{name}')").count() == 0
