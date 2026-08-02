"""Tests for the Mindmap view's done-task filter.

The map draws open work by default and offers a toggle for the rest. The unit
tests in js/views-mindmap.test.js pin the filter itself; these pin the wiring
around it — that the toggle is reachable, that it survives a reload (the
preference is per project in localStorage), and that a done task carrying open
children is still drawn as their branch.
"""

import pytest
from conftest import (
    DEMO_PROJECT_ID, TIMEOUT, navigate_to, settle, unique,)


def open_mindmap(page):
    navigate_to(page, "Mindmap")
    page.wait_for_selector(".mm-wrap", timeout=TIMEOUT)
    settle(page)


def node(page, task_id):
    return page.query_selector(f".mm-task[data-a0='{task_id}']")


def toggle(page):
    return page.query_selector(".mm-done-toggle")


@pytest.fixture
def open_and_done(api):
    """One open and one done task in the demo project, both parentless."""
    opened = api.post(f"/api/projects/{DEMO_PROJECT_ID}/tasks", {"title": unique("MM open")})
    done = api.post(f"/api/projects/{DEMO_PROJECT_ID}/tasks", {"title": unique("MM done")})
    api.post(f"/api/tasks/{done['id']}/status", {"status": "DONE", "version": done["version"]})
    return opened, done


@pytest.fixture
def done_parent_with_open_child(api):
    """A DONE story with a still-open task under it, torn down child-first.

    The autouse reap fixture now orders its deletions (children before parents,
    since deleting a task that still has children is refused with 422
    TASK_HAS_CHILDREN), so this teardown is no longer load-bearing — it used to
    be the local workaround for exactly that gap. It stays because a fixture
    that unmakes what it made is the clearer contract, and because it removes
    these two rows immediately rather than at the end of the test.
    """
    parent = api.post(f"/api/projects/{DEMO_PROJECT_ID}/tasks",
                      {"title": unique("MM parent"), "taskType": "STORY"})
    child = api.post(f"/api/projects/{DEMO_PROJECT_ID}/tasks",
                     {"title": unique("MM child"), "parentId": parent["id"]})
    parent = api.get(f"/api/tasks/{parent['id']}")
    api.post(f"/api/tasks/{parent['id']}/status",
             {"status": "DONE", "version": parent["version"]})
    yield parent, child
    for task_id in (child["id"], parent["id"]):
        try:
            api.delete(f"/api/tasks/{task_id}")
        except Exception:
            pass  # already reaped


class TestDoneFilter:
    def test_done_tasks_are_hidden_by_default(self, demo_board, open_and_done):
        opened, done = open_and_done
        open_mindmap(demo_board)
        assert node(demo_board, opened["id"]), "an open task must be on the map"
        assert node(demo_board, done["id"]) is None, "a done task must not be"

    def test_toggle_shows_and_hides_done_tasks(self, demo_board, open_and_done):
        opened, done = open_and_done
        open_mindmap(demo_board)
        btn = toggle(demo_board)
        assert btn, "the mindmap needs a done-task toggle"
        assert btn.get_attribute("aria-pressed") == "false"

        btn.click()
        settle(demo_board)
        assert node(demo_board, done["id"]), "the done task must appear once shown"
        assert node(demo_board, opened["id"]), "the open task must stay"
        assert toggle(demo_board).get_attribute("aria-pressed") == "true"

        toggle(demo_board).click()
        settle(demo_board)
        assert node(demo_board, done["id"]) is None, "and disappear again"

    def test_the_choice_survives_a_reload(self, demo_board, open_and_done):
        _, done = open_and_done
        open_mindmap(demo_board)
        toggle(demo_board).click()
        settle(demo_board)

        demo_board.reload()
        demo_board.wait_for_selector(".mm-wrap", timeout=TIMEOUT)
        settle(demo_board)
        assert node(demo_board, done["id"]), "the preference is per project, not per render"
        # Leave the project as the next test expects to find it.
        toggle(demo_board).click()
        settle(demo_board)

    def test_the_count_on_the_button_matches_what_is_hidden(self, demo_board, api, open_and_done):
        """The label promises a number; pressing it must produce that number.

        Counted against the DOM rather than against the API's DONE total,
        because a done task kept as a branch for its open children is already
        on the map and so is not among the hidden.
        """
        open_mindmap(demo_board)
        drawn_done = len(demo_board.query_selector_all(".mm-task.mm-ghost"))
        all_done = len([t for t in api.get(f"/api/projects/{DEMO_PROJECT_ID}/tasks?size=200")
                        if t["status"] == "DONE"])
        label = toggle(demo_board).inner_text()
        assert f"({all_done - drawn_done})" in label, \
            f"button said {label!r} for {all_done} done tasks, {drawn_done} of them drawn"


class TestHierarchy:
    def test_a_done_parent_stays_as_the_branch_of_its_open_child(
            self, demo_board, done_parent_with_open_child):
        """A finished story with a running task under it keeps its place.

        Dropping it would push the open task into the synthetic "Unlinked"
        group — the map would still render, and would misdescribe the project.
        """
        parent, child = done_parent_with_open_child
        open_mindmap(demo_board)
        branch = node(demo_board, parent["id"])
        assert branch, "the done parent must still be drawn"
        assert "mm-ghost" in (branch.get_attribute("class") or ""), \
            "and must be dimmed, so it reads as context rather than as open work"
        assert node(demo_board, child["id"]), "its open child must be on the map"

    def test_an_archived_task_is_absent_in_both_modes(self, demo_board, api):
        archived = api.post(f"/api/projects/{DEMO_PROJECT_ID}/tasks", {"title": unique("MM archived")})
        api.post(f"/api/tasks/{archived['id']}/archive")

        open_mindmap(demo_board)
        assert node(demo_board, archived["id"]) is None
        toggle(demo_board).click()
        settle(demo_board)
        assert node(demo_board, archived["id"]) is None, \
            "showing done tasks must not resurrect archived ones"
        toggle(demo_board).click()
        settle(demo_board)
