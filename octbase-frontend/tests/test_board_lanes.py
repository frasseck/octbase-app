"""Tests for the board's lane paging — the per-project cap on how many cards a
lane draws at once (project setting `boardLaneLimit`), with the rest loaded on
scroll or by the load-more button.

These run against a project of their own rather than the seeded demo project:
the cap only shows itself on a lane longer than the limit, and building that
lane in the demo project would push it past the 200-row task window the whole
suite depends on (see the `testing` skill, "Don't leave data behind").
"""

import pytest
from conftest import TIMEOUT, navigate_to, settle, unique


@pytest.fixture
def capped_project(api):
    """A project whose board has one lane holding more cards than the limit.

    Yields (project, board, column, limit, total). The project is deleted
    afterwards, which takes its board and every task with it — so nothing here
    reaches the seeded demo data.
    """
    limit, total = 5, 12
    project = api.post("/api/projects", {"name": unique("Lane Paging"), "visibility": "PRIVATE"})
    board = api.post(f"/api/projects/{project['id']}/boards",
                     {"name": "Paging Board", "template": "kanban", "isDefault": True})
    column = board["columns"][0]
    for i in range(total):
        task = api.post(f"/api/projects/{project['id']}/tasks", {"title": f"Paged card {i:02d}"})
        api.post(f"/api/boards/{board['id']}/move-task",
                 {"taskId": task["id"], "boardColumnId": column["id"], "boardRank": 1000 + i})
    project = api.patch(f"/api/projects/{project['id']}",
                        {"boardLaneLimit": limit, "version": project["version"]})
    assert project["boardLaneLimit"] == limit, "the setting must read back before the UI is driven"
    try:
        yield project, board, column, limit, total
    finally:
        api.delete(f"/api/projects/{project['id']}")


def open_board(page, project):
    page.evaluate(f"() => router.go('/projects/{project['id']}/board')")
    page.wait_for_selector(".board-col", timeout=TIMEOUT)
    settle(page)


def lane_cards(page, column):
    return page.locator(f"#col-{column['id']} .board-card").count()


class TestBoardLaneLimit:
    def test_the_lane_count_badge_reports_the_whole_lane_not_the_drawn_page(
            self, app, capped_project):
        """The cap is a rendering decision; the badge must stay the honest total.

        This is the invariant that keeps the feature from reading as data loss —
        if capping ever leaked into the count, a reader would be told cards had
        disappeared.
        """
        project, _board, column, _limit, total = capped_project
        open_board(app, project)

        badge = app.locator(
            f".board-col[data-col-id='{column['id']}'] .board-col-header .badge "
            f"span[aria-hidden='true']").first
        assert badge.inner_text().strip() == str(total)
        # ...while strictly fewer cards are actually in the DOM.
        assert lane_cards(app, column) < total

    def test_a_capped_lane_offers_to_load_the_rest(self, app, capped_project):
        project, _board, column, _limit, total = capped_project
        open_board(app, project)

        more = app.locator(f"#col-{column['id']} .board-lane-more")
        assert more.count() == 1, "a lane longer than the limit must offer a load-more control"
        drawn = lane_cards(app, column)
        # The control names exactly what is held back, so the two always sum to
        # the real lane.
        assert str(total - drawn) in more.first.inner_text()

    def test_load_more_reveals_the_rest_of_the_lane(self, app, capped_project):
        project, _board, column, _limit, total = capped_project
        open_board(app, project)

        more = app.locator(f"#col-{column['id']} .board-lane-more")
        # Clicking repeatedly must converge on the whole lane and then stop
        # offering: the button is gone precisely when nothing is left.
        for _ in range(10):
            if more.count() == 0:
                break
            more.first.click()
            settle(app)
        assert more.count() == 0, "the load-more control must disappear once the lane is exhausted"
        assert lane_cards(app, column) == total

    def test_an_uncapped_project_draws_every_card(self, app, api, capped_project):
        """0 means unlimited — the opt-out, and the pre-1.1.2 behaviour."""
        project, _board, column, _limit, total = capped_project
        project = api.patch(f"/api/projects/{project['id']}",
                            {"boardLaneLimit": 0, "version": project["version"]})
        assert project["boardLaneLimit"] == 0

        open_board(app, project)
        assert lane_cards(app, column) == total
        assert app.locator(f"#col-{column['id']} .board-lane-more").count() == 0

    def test_the_cap_is_configurable_from_project_settings(self, app, api, capped_project):
        """The number input in Task settings writes the project setting."""
        project, _board, column, _limit, _total = capped_project
        open_board(app, project)

        app.evaluate("""() => {
            const b = [...document.querySelectorAll('[data-act]')]
                .find(e => e.dataset.act === 'openTaskSettings');
            b.click();
        }""")
        app.wait_for_selector("#ts-lane-limit", timeout=TIMEOUT)
        field = app.locator("#ts-lane-limit")
        assert field.input_value() == str(project["boardLaneLimit"]), \
            "the dialog must open showing the stored value"

        field.fill("7")
        field.dispatch_event("change")
        settle(app)
        assert api.get(f"/api/projects/{project['id']}")["boardLaneLimit"] == 7

    def test_a_card_created_into_a_capped_lane_is_still_drawn(self, app, capped_project):
        """A new card appends to the end of its lane — past the drawn page on a
        lane longer than the limit. It must be shown anyway: a create that
        displays nothing reads as a create that failed.
        """
        project, _board, column, _limit, total = capped_project
        open_board(app, project)
        assert lane_cards(app, column) < total, "precondition: the lane must be capped"

        title = unique("Created into a capped lane")
        app.click(f".board-col[data-col-id='{column['id']}'] .col-add-btn")
        app.wait_for_selector("#task-title", timeout=TIMEOUT)
        app.fill("#task-title", title)
        app.click(".modal button[type=submit], .modal .btn-primary")
        settle(app)

        # Wait for the card, then assert how many there are — the two do
        # different jobs. Creating is API-backed and the lane redraws when the
        # response lands, which is an API-length wait; settle() only budgets
        # SHORT and is documented never to raise, and locator.count() takes an
        # immediate snapshot with no retry. So a redraw arriving a moment later
        # used to read as "the card was never drawn" — this test failed exactly
        # that way on CI 2026-08-08 and passed on the identical tree minutes
        # later. wait_for_selector fixes the race; the count assertion still
        # earns its place by catching a card drawn twice, which waiting cannot.
        card_sel = f"#col-{column['id']} .board-card:has-text('{title}')"
        app.wait_for_selector(card_sel, timeout=TIMEOUT)
        card = app.locator(card_sel)
        assert card.count() == 1, "the newly created card must be visible in its lane"

    def test_an_out_of_range_limit_is_refused_and_the_field_snaps_back(
            self, app, api, capped_project):
        """The server bounds the value; the dialog must not leave a number on
        screen that the project does not have."""
        project, _board, _column, limit, _total = capped_project
        open_board(app, project)

        app.evaluate("""() => {
            const b = [...document.querySelectorAll('[data-act]')]
                .find(e => e.dataset.act === 'openTaskSettings');
            b.click();
        }""")
        app.wait_for_selector("#ts-lane-limit", timeout=TIMEOUT)
        field = app.locator("#ts-lane-limit")

        field.fill("9999")
        field.dispatch_event("change")
        settle(app)
        assert field.input_value() == str(limit), "the field must snap back to the stored value"
        assert api.get(f"/api/projects/{project['id']}")["boardLaneLimit"] == limit
