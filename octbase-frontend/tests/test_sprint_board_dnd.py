"""Regression test: dragging a backlog task onto the sprint board must keep the
sprint board in view (it used to snap to the project's main/default board)."""

import pytest
from conftest import DEMO_PROJECT_ID, SHORT, TIMEOUT, unique, settle


def _reset_active_sprint(api):
    for s in api.get(f"/api/projects/{DEMO_PROJECT_ID}/sprints"):
        if s["status"] == "ACTIVE":
            api.post(f"/api/sprints/{s['id']}/complete", {})


def _open_sprint_board(page):
    """Switch to the sprint-board view and wait for its banner."""
    page.evaluate("() => setView('sprintBoard')")
    page.wait_for_selector(".board-sprint-banner", timeout=TIMEOUT)


def _enable_backlog_column(page, board_id):
    page.evaluate(
        "(id) => localStorage.setItem('octbase.board.backlog.' + id, '1')", board_id
    )
    _open_sprint_board(page)
    page.wait_for_selector(".board-col-backlog", timeout=TIMEOUT)


def test_drag_backlog_task_onto_sprint_board_stays_on_sprint_board(demo_board, api):
    page = demo_board
    _reset_active_sprint(api)

    # A sprint board is provisioned on creation so the sprint can be planned
    # (tasks dragged from the backlog) while it is still PLANNED — scope is
    # locked only once the sprint is started.
    sprint = api.post(f"/api/projects/{DEMO_PROJECT_ID}/sprints", {"name": unique("DnD Sprint")})

    # A fresh backlog task (no board column) to drag in.
    title = unique("Drag Me")
    task = api.post(f"/api/projects/{DEMO_PROJECT_ID}/tasks", {"title": title})

    # loadProject cached S.sprints before the sprint existed; refresh it and aim
    # the sprint-board view at this planned sprint's board.
    page.evaluate(
        # App.api, not a bare `api`: under ES modules the REST client is module-
        # scoped and only the documented facade reaches it (as test_members.py
        # already does). window.S stays bare — it is published for the suite.
        "async ([pid, sid]) => { S.sprints = await App.api.sprints.list(pid); S.sprintBoardSprintId = sid; }",
        [DEMO_PROJECT_ID, sprint["id"]],
    )

    # Open the sprint board with the backlog column shown.
    _open_sprint_board(page)
    board = page.evaluate("() => ({ id: S.board.id, isSprint: !!S.board.isSprintBoard })")
    assert board["isSprint"], "sprint-board view should resolve the sprint board"
    sprint_board_id = board["id"]
    _enable_backlog_column(page, sprint_board_id)

    # Source: the backlog card. Target: the first sprint lane's drop column.
    page.wait_for_selector(
        f".board-col-backlog .board-card[data-task-id='{task['id']}']", timeout=TIMEOUT
    )

    # Drive the app's real drag handlers (wired on document via S.dragging).
    # Playwright's mouse events do not fire native HTML5 drag, so dispatch the
    # dragstart/dragover/drop sequence with a shared DataTransfer directly.
    page.evaluate(
        """(taskId) => {
            const src = document.querySelector(
                `.board-col-backlog .board-card[data-task-id='${taskId}']`);
            // The backlog column is also a [data-drop-col] (dropping there removes
            // a card from the board), so target a real lane explicitly.
            const lane = document.querySelector('.board-col[data-drop-col]:not(.board-col-backlog) .board-col-tasks')
                       || document.querySelector('.board-col[data-drop-col]:not(.board-col-backlog)');
            const dt = new DataTransfer();
            const fire = (el, type) => el.dispatchEvent(
                new DragEvent(type, { bubbles: true, cancelable: true, dataTransfer: dt, clientY: 300 }));
            fire(src, 'dragstart');
            fire(lane, 'dragover');
            fire(lane, 'drop');
            fire(src, 'dragend');
        }""",
        task["id"],
    )
    settle(page)
    # 1) The view must still be the sprint board (the bug snapped to main board).
    assert page.is_visible(".board-sprint-banner"), "view snapped away from the sprint board"
    still = page.evaluate("() => ({ view: S.view, isSprint: !!S.board.isSprintBoard, id: S.board.id })")
    assert still["view"] == "sprintBoard"
    assert still["isSprint"] and still["id"] == sprint_board_id

    # 2) The task must actually be on the sprint board now (not left in backlog).
    moved = api.get(f"/api/tasks/{task['id']}")
    assert moved.get("boardColumnId"), "task was not added to the board"

    # 3) Adding it to the sprint board must enroll it in the sprint, so the
    #    sprint's task count stops reading 0/0. (Requires the MoveTask auto-link.)
    assert moved.get("sprintId") == sprint["id"], "task was not linked to the sprint"

    # Clean up the planned sprint (and its board) so it doesn't accumulate.
    api.delete(f"/api/sprints/{sprint['id']}")
