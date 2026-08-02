"""The autouse reap fixture must delete child tasks before their parents.

Deleting a task that still has children is refused with 422 TASK_HAS_CHILDREN
and the refusal is swallowed, so a reap in arbitrary order silently leaves
parents behind in the seeded demo project — which is how the project creeps
past the 200-row task window and takes the board tests down with it
(KNOWN_FAILURES.md, "polluted-stack signature").

These drive `_reap_tasks` directly against a fake client rather than a live
stack: the ordering rule is the thing under test, and it should be checked on
every run, not only when an API happens to be reachable.
"""

import pytest

from conftest import _delete_quietly, _reap_tasks

# These drive conftest's own helpers, so they must run even when no instance is
# up — the marker keeps the autouse reap fixture (and its session skip) away.
pytestmark = pytest.mark.no_stack


class FakeHTTPError(Exception):
    def __init__(self, status_code):
        super().__init__(f"{status_code} Client Error for url: /api/tasks/x")
        self.response = type("Resp", (), {"status_code": status_code})()


class FakeApi:
    """Enough of ApiClient to refuse deleting a task that still has children."""

    def __init__(self, parents, missing=()):
        # id -> parentId, exactly the shape _entity_snapshot now produces.
        self.parents = dict(parents)
        self.missing = set(missing)
        self.deleted = []

    def delete(self, path):
        task_id = path.rsplit("/", 1)[-1]
        if task_id in self.missing:
            raise FakeHTTPError(404)
        if any(parent == task_id for parent in self.parents.values()):
            raise FakeHTTPError(422)  # TASK_HAS_CHILDREN
        self.parents.pop(task_id, None)
        self.deleted.append(task_id)


class TestReapTasks:
    def test_a_parent_is_deleted_after_its_child(self):
        api = FakeApi({"parent": None, "child": "parent"})
        _reap_tasks(api, {"parent": None, "child": "parent"})
        assert api.deleted == ["child", "parent"]
        assert api.parents == {}

    def test_a_three_level_hierarchy_unwinds_from_the_leaf(self):
        tasks = {"epic": None, "story": "epic", "task": "story"}
        api = FakeApi(tasks)
        _reap_tasks(api, tasks)
        assert api.deleted == ["task", "story", "epic"]

    def test_siblings_and_unrelated_tasks_all_go(self):
        tasks = {"p": None, "c1": "p", "c2": "p", "loner": None}
        api = FakeApi(tasks)
        _reap_tasks(api, tasks)
        assert sorted(api.deleted) == ["c1", "c2", "loner", "p"]

    def test_a_task_already_gone_does_not_block_its_parent(self):
        # A test that cleaned up its own child in a try/finally leaves a 404.
        api = FakeApi({"parent": None}, missing=["child"])
        _reap_tasks(api, {"parent": None, "child": "parent"})
        assert "parent" in api.deleted

    def test_an_undeletable_task_stops_the_loop_instead_of_spinning(self):
        # The child is outside the snapshot, so the parent can never be freed.
        # The reap must give up rather than retry forever.
        api = FakeApi({"parent": None, "ghost-child": "parent"})
        _reap_tasks(api, {"parent": None})
        assert api.deleted == []


class TestDeleteQuietly:
    def test_reports_success(self):
        assert _delete_quietly(FakeApi({"t": None}), "/api/tasks/t") is True

    def test_treats_a_missing_entity_as_gone(self):
        assert _delete_quietly(FakeApi({}, missing=["t"]), "/api/tasks/t") is True

    def test_reports_a_refusal(self):
        api = FakeApi({"p": None, "c": "p"})
        assert _delete_quietly(api, "/api/tasks/p") is False


if __name__ == "__main__":  # pragma: no cover
    raise SystemExit(pytest.main([__file__]))
