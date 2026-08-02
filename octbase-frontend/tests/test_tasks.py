"""Tests for task filtering and search on the Backlog and Board views.

The original standalone Tasks list (in the monolithic app.js) was removed as
redundant with the Backlog; its type/priority filters moved to the Backlog
(status filtering dropped) and a free-text search was added: full-text (title +
description) on the Backlog and filter-by-name on the Board. It has since been
reintroduced as a *flag-gated* Task view built on the shared task-list engine —
its enabled/disabled behaviour (sidebar entry, graceful fallback) lives in
test_taskview.py.
"""

import pytest
from conftest import DEMO_PROJECT_ID, DEMO_TASK_TITLE, SHORT, TIMEOUT, navigate_to, unique, settle


@pytest.fixture
def backlog_task(api):
    """One task in the Demo Project's backlog.

    The seed puts both of its tasks on board columns, so a freshly seeded
    project has an *empty* backlog and the view renders its empty state — which
    has no filter bar and no search box (`renderTaskList` only draws those when
    something is in scope). Tests that need the toolbar must put a task in the
    backlog themselves; the ones below used to rely on tasks left behind by
    earlier runs. A task created with no boardColumnId lands in the backlog.
    """
    return api.post(f"/api/projects/{DEMO_PROJECT_ID}/tasks", {"title": unique("Backlog Item")})


class TestTasksView:
    def test_no_task_table_styling_used(self, demo_board):
        # The new Task view reuses the .backlog-* grid, so the orphaned .task-table
        # styling from the old standalone view is still rendered nowhere.
        navigate_to(demo_board, "Backlog")
        settle(demo_board)
        assert not demo_board.is_visible(".task-table")


class TestBacklogFilters:
    def test_filter_bar_has_no_status_select(self, demo_board, backlog_task):
        # The status filter was removed from the backlog (it isn't useful there).
        navigate_to(demo_board, "Backlog")
        demo_board.wait_for_selector("#filter-priority", timeout=TIMEOUT)
        assert not demo_board.is_visible("#filter-status")

    def test_filter_bar_has_priority_select(self, demo_board, backlog_task):
        navigate_to(demo_board, "Backlog")
        demo_board.wait_for_selector("#filter-priority", timeout=TIMEOUT)
        assert demo_board.is_visible("#filter-priority")

    def test_filter_bar_has_type_select(self, demo_board, backlog_task):
        navigate_to(demo_board, "Backlog")
        demo_board.wait_for_selector("#filter-type", timeout=TIMEOUT)
        assert demo_board.is_visible("#filter-type")


class TestBacklogSearch:
    def test_backlog_has_search_input(self, demo_board, backlog_task):
        navigate_to(demo_board, "Backlog")
        demo_board.wait_for_selector("#task-search", timeout=TIMEOUT)
        assert demo_board.is_visible("#task-search")

    def test_search_filters_backlog_rows(self, demo_board, api):
        name = unique("FindMeBacklogTask")
        api.post(f"/api/projects/{DEMO_PROJECT_ID}/tasks", {"title": name})
        navigate_to(demo_board, "Backlog")
        demo_board.wait_for_selector(".backlog-row", timeout=TIMEOUT)

        demo_board.fill("#task-search", name)
        settle(demo_board)
        rows = demo_board.query_selector_all(".backlog-row")
        assert len(rows) >= 1
        for row in rows:
            assert name in row.inner_text()

    def test_search_matches_description(self, demo_board, api):
        marker = unique("descmarker")
        api.post(
            f"/api/projects/{DEMO_PROJECT_ID}/tasks",
            {"title": unique("Plain title"), "description": f"contains {marker} inside body"},
        )
        navigate_to(demo_board, "Backlog")
        demo_board.wait_for_selector(".backlog-row", timeout=TIMEOUT)

        # The marker only appears in the description, so a full-text match proves
        # the search looks beyond the title.
        demo_board.fill("#task-search", marker)
        settle(demo_board)
        assert len(demo_board.query_selector_all(".backlog-row")) >= 1

    def test_search_no_match_shows_message(self, demo_board, backlog_task):
        navigate_to(demo_board, "Backlog")
        demo_board.wait_for_selector("#task-search", timeout=TIMEOUT)
        demo_board.fill("#task-search", "zzz_no_task_should_match_xyz")
        settle(demo_board)
        assert demo_board.is_visible(".backlog-empty-search")
        assert len(demo_board.query_selector_all(".backlog-row")) == 0

    def test_clearing_search_restores_rows(self, demo_board, backlog_task):
        navigate_to(demo_board, "Backlog")
        demo_board.wait_for_selector(".backlog-row", timeout=TIMEOUT)
        total = len(demo_board.query_selector_all(".backlog-row"))

        demo_board.fill("#task-search", "zzz_no_task_should_match_xyz")
        settle(demo_board)
        demo_board.fill("#task-search", "")
        settle(demo_board)
        assert len(demo_board.query_selector_all(".backlog-row")) == total


class TestBoardSearch:
    def test_board_has_search_input(self, demo_board):
        navigate_to(demo_board, "Board")
        demo_board.wait_for_selector("#board-search", timeout=TIMEOUT)
        assert demo_board.is_visible("#board-search")

    def test_board_search_filters_cards_by_name(self, demo_board):
        navigate_to(demo_board, "Board")
        demo_board.wait_for_selector(".board-card", timeout=TIMEOUT)

        demo_board.fill("#board-search", DEMO_TASK_TITLE)
        settle(demo_board)
        titles = [c.inner_text().strip() for c in demo_board.query_selector_all(".board-card .card-title")]
        assert titles
        assert all(DEMO_TASK_TITLE.lower() in tt.lower() for tt in titles)

    def test_board_search_no_match_hides_cards(self, demo_board):
        navigate_to(demo_board, "Board")
        demo_board.wait_for_selector(".board-card", timeout=TIMEOUT)

        demo_board.fill("#board-search", "zzz_no_card_should_match_xyz")
        settle(demo_board)
        assert len(demo_board.query_selector_all(".board-card")) == 0

    def test_search_query_reaches_the_url_after_typing(self, demo_board):
        """The search re-render and the URL write are debounced, not dropped.

        Both used to run per keystroke — including one history.replaceState each,
        which Safari throttles to ~100 per 30s and then throws on. They are now
        coalesced into one trailing call; this pins that the coalesced call still
        happens, and that S.filters.q is recorded immediately either way.
        """
        navigate_to(demo_board, "Board")
        demo_board.wait_for_selector(".board-card", timeout=TIMEOUT)

        demo_board.fill("#board-search", "auth")
        # Recorded synchronously: no keystroke waits on the debounce.
        assert demo_board.evaluate("() => S.filters.q") == "auth"

        settle(demo_board)
        assert "q=auth" in demo_board.evaluate("() => location.hash"), \
            "the debounced tail must still reflect the query in the URL"

    def test_typing_a_burst_leaves_the_caret_alone(self, demo_board):
        """The input is never re-rendered, so the caret cannot move.

        The board search re-renders only the lanes (.board-cols), never the
        toolbar holding the input — that is what lets it stay focused while typing.
        """
        navigate_to(demo_board, "Board")
        demo_board.wait_for_selector(".board-card", timeout=TIMEOUT)
        demo_board.click("#board-search")
        demo_board.keyboard.type("authx", delay=0)
        settle(demo_board)
        assert demo_board.evaluate("() => document.activeElement && document.activeElement.id") == "board-search"
        assert demo_board.evaluate(
            "() => { const i = document.getElementById('board-search'); return i.selectionStart; }") == 5, \
            "the caret must stay at the end of the typed text"

    def test_clearing_board_search_restores_cards(self, demo_board):
        navigate_to(demo_board, "Board")
        demo_board.wait_for_selector(".board-card", timeout=TIMEOUT)
        total = len(demo_board.query_selector_all(".board-card"))

        demo_board.fill("#board-search", "zzz_no_card_should_match_xyz")
        settle(demo_board)
        demo_board.fill("#board-search", "")
        settle(demo_board)
        assert len(demo_board.query_selector_all(".board-card")) == total


class TestSearchByTaskId:
    """Searching by the task key (DP-2) on the Backlog and the Board.

    The key is derived in the browser (project abbreviation + seqNumber) and is
    never sent as a field, so these drive the real views rather than asserting on
    a payload: the key each test types is the one the DOM is showing.
    """

    @staticmethod
    def _prefix(page):
        """The project's key prefix ('DP'), read from the app rather than hardcoded."""
        return page.evaluate(
            "() => S.project.abbreviation || (S.project.slug || '').toUpperCase()")

    def test_backlog_search_finds_a_task_by_its_full_key(self, demo_board, api):
        mine = api.post(f"/api/projects/{DEMO_PROJECT_ID}/tasks", {"title": unique("KeyLookup")})
        api.post(f"/api/projects/{DEMO_PROJECT_ID}/tasks", {"title": unique("OtherRow")})
        navigate_to(demo_board, "Backlog")
        demo_board.wait_for_selector(".backlog-row", timeout=TIMEOUT)

        key = f"{self._prefix(demo_board)}-{mine['seqNumber']}"
        demo_board.fill("#task-search", key)
        settle(demo_board)
        rows = demo_board.query_selector_all(".backlog-row")
        assert len(rows) == 1, f"{key} must select exactly its own row"
        assert mine["title"] in rows[0].inner_text()

    def test_backlog_search_finds_a_task_by_its_bare_number(self, demo_board, api):
        mine = api.post(f"/api/projects/{DEMO_PROJECT_ID}/tasks", {"title": unique("BareNumber")})
        navigate_to(demo_board, "Backlog")
        demo_board.wait_for_selector(".backlog-row", timeout=TIMEOUT)

        demo_board.fill("#task-search", str(mine["seqNumber"]))
        settle(demo_board)
        titles = [r.inner_text() for r in demo_board.query_selector_all(".backlog-row")]
        # Other rows may legitimately match the digits as text; the point is that
        # the task carrying that number is among them, which a title/description
        # search alone would never manage.
        assert any(mine["title"] in tt for tt in titles)

    def test_backlog_search_is_case_insensitive_and_tolerates_a_hash(self, demo_board, api):
        mine = api.post(f"/api/projects/{DEMO_PROJECT_ID}/tasks", {"title": unique("KeySpelling")})
        navigate_to(demo_board, "Backlog")
        demo_board.wait_for_selector(".backlog-row", timeout=TIMEOUT)

        prefix = self._prefix(demo_board)
        for spelling in (f"{prefix.lower()}-{mine['seqNumber']}", f"#{prefix}-{mine['seqNumber']}"):
            demo_board.fill("#task-search", spelling)
            settle(demo_board)
            rows = demo_board.query_selector_all(".backlog-row")
            assert len(rows) == 1 and mine["title"] in rows[0].inner_text(), spelling

    def test_board_search_finds_a_card_by_its_key(self, demo_board):
        navigate_to(demo_board, "Board")
        demo_board.wait_for_selector(".board-card .task-seq", timeout=TIMEOUT)
        # Take the key off a card that is actually on screen.
        key = demo_board.query_selector(".board-card .task-seq").inner_text().strip()

        demo_board.fill("#board-search", key)
        settle(demo_board)
        shown = [c.inner_text().strip() for c in demo_board.query_selector_all(".board-card .task-seq")]
        assert shown == [key], f"{key} must leave exactly its own card on the board"

    def test_board_search_ignores_a_key_from_another_project(self, demo_board):
        navigate_to(demo_board, "Board")
        demo_board.wait_for_selector(".board-card .task-seq", timeout=TIMEOUT)
        seq = demo_board.query_selector(".board-card .task-seq").inner_text().strip().split("-")[-1]

        # Same number, a prefix naming some other project: answering with this
        # project's task would be a confidently wrong answer, so nothing matches.
        demo_board.fill("#board-search", f"ZZZ-{seq}")
        settle(demo_board)
        assert len(demo_board.query_selector_all(".board-card")) == 0
