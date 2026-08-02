"""Tests for the task detail panel: header, all tabs, editing, actions."""

import pytest
from conftest import (
    DEMO_PROJECT_ID, DEMO_TASK_ID, DEMO_TASK2_ID, DEMO_TASK_TITLE, DEMO_USER_ID,
    SHORT, TIMEOUT, toast_text, unique, poll_until, settle, await_next_second,)


class TestPanelOpensAndCloses:
    def test_panel_opens_on_card_click(self, demo_board):
        demo_board.click(".board-card")
        demo_board.wait_for_selector("#task-panel.open", timeout=TIMEOUT)
        assert demo_board.is_visible("#task-panel.open")

    def test_panel_close_button_hides_panel(self, task_panel):
        task_panel.click(".panel-close")
        settle(task_panel)
        assert not task_panel.is_visible("#task-panel.open")

    def test_panel_shows_task_title(self, task_panel):
        title_el = task_panel.query_selector(".panel-title-input")
        assert title_el is not None
        assert DEMO_TASK_TITLE in title_el.input_value()

    def test_panel_shows_status_badge(self, task_panel):
        badge = task_panel.query_selector("#task-panel .badge")
        assert badge is not None
        assert badge.inner_text().strip() != ""

    def test_panel_shows_priority(self, task_panel):
        panel_text = task_panel.query_selector("#task-panel").inner_text()
        assert "HIGH" in panel_text

    def test_panel_shows_archive_button_for_active_task(self, task_panel):
        assert task_panel.is_visible("#task-panel button:has-text('Archive')")

    def test_panel_shows_backlog_button_for_board_task(self, task_panel):
        assert task_panel.is_visible("#task-panel button:has-text('Backlog')")


class TestPanelTabs:
    def test_all_six_tabs_present(self, task_panel):
        tabs = task_panel.query_selector_all(".panel-tab")
        labels = [t.inner_text().strip().lower() for t in tabs]
        for expected in ["details", "comments", "links", "relations",
                         "branches", "activity"]:
            assert expected in labels, f"Tab '{expected}' not found"

    def test_no_attachments_tab(self, task_panel):
        # Attachments live in the details sidebar; the dedicated tab was removed.
        tabs = task_panel.query_selector_all(".panel-tab")
        labels = [t.inner_text().strip().lower() for t in tabs]
        assert "attachments" not in labels

    def test_details_tab_active_by_default(self, task_panel):
        active = task_panel.query_selector(".panel-tab.active")
        assert active is not None
        assert "details" in active.inner_text().lower()

    def test_switching_tabs_updates_content(self, task_panel):
        task_panel.click(".panel-tab:has-text('Comments')")
        settle(task_panel)
        active = task_panel.query_selector(".panel-tab.active")
        assert "comments" in active.inner_text().lower()


class TestDetailsTab:
    def test_description_shows_task_text(self, task_panel):
        # #pt-desc is now a contenteditable rich-text editor (a div), not a
        # textarea, so read its rendered text via inner_text().
        desc = task_panel.query_selector("#pt-desc")
        assert desc is not None
        assert "auth" in desc.inner_text().lower()

    def test_status_is_the_only_placement_control(self, task_panel):
        # Status and board column used to be two controls for one idea, free to
        # disagree with each other. Status is now the single one: it is shown for
        # every task (including one on a board) and the board-column select is gone.
        assert task_panel.is_visible("select[data-change='changeStatus']")
        assert not task_panel.is_visible("select[data-change='moveTaskToColumnSelect']")

    def test_status_change_relocates_card_in_place(self, task_panel, api):
        # Changing the status from the sidebar must move the card to the lane that
        # carries it, in place — no full board reload / navigation. The seed task
        # starts in "In Progress"; set IN_REVIEW and assert it lands in "Review"
        # while the board stays rendered.
        def lane_has_card(col_name):
            col = task_panel.query_selector(
                f".board-col:has(.board-col-header:has-text('{col_name}'))"
            )
            return bool(col and col.query_selector(
                f".board-card:has-text('{DEMO_TASK_TITLE}')"))

        assert lane_has_card("In Progress")
        task_panel.query_selector(
            "select[data-change='changeStatus']").select_option("IN_REVIEW")
        settle(task_panel)
        # The board is still on screen (in-place refresh, not a navigation).
        assert task_panel.is_visible(".board-cols")
        poll_until(lambda: lane_has_card("Review"), timeout=TIMEOUT)
        assert not lane_has_card("In Progress")
        task = api.get(f"/api/tasks/{DEMO_TASK_ID}")
        assert task["status"] == "IN_REVIEW"
        # The autouse _restore_demo_task_placement fixture returns it to the seed
        # lane afterwards, so board tests later in the session still find it.

    def test_status_change_refreshes_panel_in_place(self, task_panel, api):
        # Changing the status from the sidebar must repaint the open panel from
        # cache (no spinner blank, no navigation): the panel stays open and its
        # header status badge updates in place to the new status.
        def panel_badge_text():
            badges = task_panel.query_selector_all(".panel-title-meta .badge")
            return " ".join(b.inner_text().strip() for b in badges)

        assert "In Progress" in panel_badge_text()
        task_panel.query_selector(
            "select[data-change='changeStatus']").select_option("IN_REVIEW")
        settle(task_panel)
        # The panel never blanks with a spinner and stays open on the same task.
        assert task_panel.is_visible("#task-panel.open")
        assert not task_panel.is_visible("#task-panel-content .loading")
        assert DEMO_TASK_TITLE in task_panel.query_selector(
            ".panel-title-input").input_value()
        # The header status badge reflects the new status in place.
        poll_until(lambda: "In Review" in panel_badge_text(), timeout=TIMEOUT)
        assert api.get(f"/api/tasks/{DEMO_TASK_ID}")["status"] == "IN_REVIEW"
        # The autouse _restore_demo_task_placement fixture returns it to the seed
        # lane afterwards, so board tests later in the session still find it.

    def test_status_change_puts_a_backlog_task_on_the_board(self, demo_board, api):
        # The bug this replaced: a task that was not on the board took its new
        # status silently in the backlog, so work that had genuinely started
        # stayed invisible to everyone reading the board. Status owns placement
        # now, so setting one has to put the card in the matching lane.
        task = api.post(f"/api/projects/{DEMO_PROJECT_ID}/tasks",
                        {"title": unique("Off-board task"), "taskType": "TASK"})
        assert task["boardColumnId"] is None, "fixture must start off the board"

        demo_board.evaluate("(id) => openTaskPanel(id)", task["id"])
        demo_board.wait_for_selector("#task-panel.open", timeout=TIMEOUT)
        demo_board.query_selector(
            "select[data-change='changeStatus']").select_option("IN_PROGRESS")
        settle(demo_board)

        poll_until(lambda: api.get(f"/api/tasks/{task['id']}")["boardColumnId"] is not None,
                   timeout=TIMEOUT)
        moved = api.get(f"/api/tasks/{task['id']}")
        assert moved["status"] == "IN_PROGRESS"
        board = api.get(f"/api/projects/{DEMO_PROJECT_ID}/boards/default")
        lane = next(c for c in board["columns"] if c["id"] == moved["boardColumnId"])
        assert lane["status"] == "IN_PROGRESS", "landed in a lane that carries another status"
        api.delete(f"/api/tasks/{task['id']}")

    def test_priority_dropdown_present(self, task_panel):
        assert task_panel.is_visible("select[data-change='changePriority']")

    def test_type_dropdown_present(self, task_panel):
        assert task_panel.is_visible("select[data-change='changeType']")

    def test_assignee_shows_demo_user(self, task_panel):
        rows = task_panel.query_selector_all(".detail-row")
        assignee_row = next(
            (r for r in rows if "Assignee" in r.query_selector(".detail-label").inner_text()),
            None,
        )
        assert assignee_row is not None
        assert "Demo User" in assignee_row.query_selector(".detail-val").inner_text()

    def test_release_shows_v1_launch(self, task_panel):
        rows = task_panel.query_selector_all(".detail-row")
        ms_row = next(
            (r for r in rows if "Release" in r.query_selector(".detail-label").inner_text()),
            None,
        )
        assert ms_row is not None
        selected = ms_row.query_selector(".detail-select option:checked")
        value_text = selected.inner_text() if selected else ms_row.query_selector(".detail-val").inner_text()
        assert "v1.0" in value_text

    def test_created_and_updated_dates_present(self, task_panel):
        # Created/Updated are shown compactly as side-by-side date columns
        # ("Created: <date>" / "Updated: <date>"), not as labelled detail rows.
        cols = task_panel.query_selector_all("#task-panel .detail-date-col")
        texts = [c.inner_text() for c in cols]
        assert any("Created" in s for s in texts)
        assert any("Updated" in s for s in texts)

    def test_save_button_visible(self, task_panel):
        assert task_panel.is_visible("#panel-tab-content button:has-text('Save')")

    def test_focus_does_not_resize_editors(self, task_panel):
        # Focusing an editor must not change its box size: the M3 focus style
        # once thickened the border with the large input's compensation
        # padding, growing small inputs (estimate, due date) by 8px on click
        # and shoving the layout.
        deltas = task_panel.evaluate(
            """() => {
              const els = [...document.querySelectorAll(
                '#panel-tab-content select, #panel-tab-content input:not([type=file])')];
              return els.filter(el => !el.disabled).map(el => {
                const b = el.getBoundingClientRect();
                el.focus();
                const a = el.getBoundingClientRect();
                el.blur();
                return {id: el.id || el.className,
                        dw: Math.abs(a.width - b.width),
                        dh: Math.abs(a.height - b.height)};
              });
            }"""
        )
        assert deltas, "no editors found on the details tab"
        grew = [d for d in deltas if d["dw"] > 0.5 or d["dh"] > 0.5]
        assert not grew, f"editors changed size on focus: {grew}"

    def test_description_save_calls_api(self, task_panel, api):
        new_text = unique("Updated description")
        # The editor is a contenteditable div; select all + type to replace.
        editor = task_panel.query_selector("#pt-desc")
        editor.click()
        task_panel.keyboard.press("Control+A")
        task_panel.keyboard.type(new_text)
        # Dispatch an input event so the dirty-state / save button update.
        settle(task_panel)
        task_panel.click("#panel-tab-content button:has-text('Save')")
        settle(task_panel)
        # Verify via API (stored as sanitized HTML; the typed text survives).
        task = api.get(f"/api/tasks/{DEMO_TASK_ID}")
        assert new_text in task["description"]
        # Restore original description
        api.patch(f"/api/tasks/{DEMO_TASK_ID}", {"description": "Add JWT-based auth to the API"})


class TestDescriptionDirtyFlag:
    """The Save button and status line track the editor's real state.

    The dirty check used to run a full DOMPurify sanitize of the editor AND
    re-render the (unchanged) saved description from scratch on every keystroke.
    Both are now coalesced behind a debounce and the rendered original is
    memoized. The flag itself must stay exactly right — a wrong one either blocks
    a real save or claims unsaved changes that do not exist — so these tests
    exercise it in both directions.

    settle() waits on S.pendingDebounces, so it covers the debounce window; a
    plain sleep would race it.
    """

    def _editor(self, page):
        editor = page.query_selector("#pt-desc")
        editor.click()
        return editor

    def test_save_is_disabled_until_something_is_typed(self, task_panel):
        settle(task_panel)
        assert task_panel.query_selector("#pt-desc-save").is_disabled(), \
            "an untouched description must not offer a save"
        assert "Unsaved" not in task_panel.query_selector("#pt-desc-status").inner_text()

    def test_typing_marks_the_editor_dirty(self, task_panel):
        self._editor(task_panel)
        task_panel.keyboard.type("x")
        settle(task_panel)
        assert not task_panel.query_selector("#pt-desc-save").is_disabled(), \
            "a typed change must enable the save"
        assert "Unsaved" in task_panel.query_selector("#pt-desc-status").inner_text()

    def test_undoing_the_typing_marks_it_clean_again(self, task_panel):
        """Typing then removing the character is not a change.

        This is the case the comparison has to normalize both sides for: the
        editor holds the rendered form of the description, so it is only equal to
        the saved text once that text is rendered the same way.
        """
        self._editor(task_panel)
        task_panel.keyboard.press("End")
        task_panel.keyboard.type("zz")
        settle(task_panel)
        assert not task_panel.query_selector("#pt-desc-save").is_disabled()

        task_panel.keyboard.press("Backspace")
        task_panel.keyboard.press("Backspace")
        settle(task_panel)
        assert task_panel.query_selector("#pt-desc-save").is_disabled(), \
            "removing the typed characters again must clear the dirty flag"
        assert "Unsaved" not in task_panel.query_selector("#pt-desc-status").inner_text()

    def test_no_keystroke_is_lost_to_the_debounce(self, task_panel, api):
        """A fast burst of keystrokes is fully saved.

        The draft is recorded synchronously per keystroke while only the dirty
        flag is debounced, so a save issued right after typing carries every
        character. Typing with no delay is the point of the test.
        """
        marker = unique("BurstTyped").replace(" ", "")
        editor = self._editor(task_panel)
        task_panel.keyboard.press("Control+A")
        editor.type(marker, delay=0)
        settle(task_panel)
        task_panel.click("#panel-tab-content button:has-text('Save')")
        settle(task_panel)
        poll_until(
            lambda: marker in (api.get(f"/api/tasks/{DEMO_TASK_ID}")["description"] or ""),
            message="the typed text never reached the server")
        api.patch(f"/api/tasks/{DEMO_TASK_ID}", {"description": "Add JWT-based auth to the API"})


class TestStatusAndPriorityChanges:
    def test_change_priority_via_dropdown(self, task_panel, api):
        sel = task_panel.query_selector("select[data-change='changePriority']")
        sel.select_option("MEDIUM")
        settle(task_panel)
        # Check API
        task = api.get(f"/api/tasks/{DEMO_TASK_ID}")
        assert task["priority"] == "MEDIUM"
        # Restore
        api.post(f"/api/tasks/{DEMO_TASK_ID}/priority", {"priority": "HIGH"})

    def test_change_type_via_dropdown(self, task_panel, api):
        sel = task_panel.query_selector("select[data-change='changeType']")
        sel.select_option("STORY")
        settle(task_panel)
        task = api.get(f"/api/tasks/{DEMO_TASK_ID}")
        assert task["taskType"] == "STORY"
        # Restore
        api.patch(f"/api/tasks/{DEMO_TASK_ID}", {"taskType": "TASK"})

    def test_change_type_to_subtask_defers_until_parent_picked(self, task_panel, api):
        # DEMO_TASK is a parentless TASK: picking SUBTASK must not save yet —
        # the server only accepts taskType+parentId in one PATCH, so the panel
        # switches the Parent select to task-level candidates instead.
        task_panel.query_selector("select[data-change='changeType']").select_option("SUBTASK")
        settle(task_panel)
        task = api.get(f"/api/tasks/{DEMO_TASK_ID}")
        assert task["taskType"] == "TASK"  # nothing saved yet
        parent_sel = task_panel.query_selector("select[data-change='changeParent']")
        assert parent_sel.input_value() == ""  # "Select parent…" placeholder

        # Picking a parent task saves type and parent together.
        parent_sel.select_option(DEMO_TASK2_ID)
        settle(task_panel)
        task = api.get(f"/api/tasks/{DEMO_TASK_ID}")
        assert task["taskType"] == "SUBTASK"
        assert task["parentId"] == DEMO_TASK2_ID

        # Going back to TASK must clear the now-wrong-level parent with it.
        task_panel.query_selector("select[data-change='changeType']").select_option("TASK")
        settle(task_panel)
        task = api.get(f"/api/tasks/{DEMO_TASK_ID}")
        assert task["taskType"] == "TASK"
        assert task["parentId"] is None


class TestPanelEditsReachTheBoardCard:
    """Every edit made in the task panel must show on the board card at once.

    "At once" is stronger than "eventually correct": the board is patched in
    place, so these tests tag the rendered `.board-cols` element first and assert
    the tag survives. A full `renderContent()` would replace that element (and
    flash a spinner), so a surviving tag proves the card was updated in place
    rather than by re-rendering the whole view.
    """

    CARD = f".board-card:has-text('{DEMO_TASK_TITLE}')"

    @staticmethod
    def _tag_board(page):
        page.evaluate("document.querySelector('.board-cols').dataset.inplace = 'yes'")

    @staticmethod
    def _board_kept(page):
        return page.evaluate("!!document.querySelector('.board-cols[data-inplace=\"yes\"]')")

    def test_assignee_change_updates_card_avatar_in_place(self, task_panel, api):
        # The seeded task is assigned to Demo User, so its card carries an avatar.
        assert task_panel.query_selector(f"{self.CARD} .card-top-right .avatar-sm")
        self._tag_board(task_panel)

        task_panel.query_selector("#task-assignee").select_option("")
        settle(task_panel)
        poll_until(
            lambda: task_panel.query_selector(f"{self.CARD} .card-top-right .avatar-sm") is None,
            timeout=TIMEOUT, message="assignee avatar still on the card")
        assert self._board_kept(task_panel)
        assert api.get(f"/api/tasks/{DEMO_TASK_ID}")["assigneeId"] is None

        # Reassigning brings the avatar back, again without a board re-render.
        task_panel.query_selector("#task-assignee").select_option(DEMO_USER_ID)
        settle(task_panel)
        poll_until(
            lambda: task_panel.query_selector(f"{self.CARD} .card-top-right .avatar-sm") is not None,
            timeout=TIMEOUT, message="assignee avatar did not return to the card")
        assert self._board_kept(task_panel)
        assert api.get(f"/api/tasks/{DEMO_TASK_ID}")["assigneeId"] == DEMO_USER_ID

    def test_due_date_change_shows_on_card_in_place(self, task_panel, api):
        # The seeded task has no due date, so the card carries no due tag yet.
        assert task_panel.query_selector(f"{self.CARD} .due-tag") is None
        self._tag_board(task_panel)

        task_panel.fill("input[data-change='updateTaskField'][data-a1='dueDate']", "2031-03-04")
        settle(task_panel)
        # Confirm the write landed before blaming the card for not showing it.
        poll_until(
            lambda: (api.get(f"/api/tasks/{DEMO_TASK_ID}")["dueDate"] or "").startswith("2031-03-04"),
            timeout=TIMEOUT, message=f"due date was not saved ({toast_text(task_panel)})")
        tag = poll_until(
            lambda: task_panel.query_selector(f"{self.CARD} .due-tag"),
            timeout=TIMEOUT, message="due date did not appear on the card")
        assert "2031" in tag.inner_text()
        assert self._board_kept(task_panel)

        # Restore the seed state (no due date).
        task = api.get(f"/api/tasks/{DEMO_TASK_ID}")
        api.patch(f"/api/tasks/{DEMO_TASK_ID}", {"dueDate": None, "version": task["version"]})

    def test_title_change_shows_on_card_in_place(self, task_panel, api):
        new_title = unique("Renamed from the panel")
        self._tag_board(task_panel)

        task_panel.fill("#panel-title-input", new_title)
        task_panel.keyboard.press("Enter")
        settle(task_panel)
        poll_until(
            lambda: task_panel.query_selector(f".board-card:has-text('{new_title}')"),
            timeout=TIMEOUT, message="renamed card did not appear on the board")
        assert self._board_kept(task_panel)
        assert api.get(f"/api/tasks/{DEMO_TASK_ID}")["title"] == new_title

        # Restore the seed title — later tests locate the card by it.
        task = api.get(f"/api/tasks/{DEMO_TASK_ID}")
        api.patch(f"/api/tasks/{DEMO_TASK_ID}",
                  {"title": DEMO_TASK_TITLE, "version": task["version"]})

    def test_move_to_backlog_updates_both_columns_in_place(self, demo_board, api):
        # The backlog column is drawn from its own cache, so taking a card off the
        # board has to move it between the two in place — lane card gone, backlog
        # card present, without re-rendering the board. The toggle sits in the
        # board toolbar, which the open panel covers, so switch it on first.
        page = demo_board
        page.click('[data-act="toggleBacklogColumn"]')
        page.wait_for_selector(".board-col-backlog", timeout=TIMEOUT)
        try:
            page.click(f".board-card:has-text('{DEMO_TASK_TITLE}')")
            page.wait_for_selector("#task-panel button:has-text('Backlog')", timeout=TIMEOUT)
            lane_card = f".board-col:not(.board-col-backlog) .board-card[data-task-id='{DEMO_TASK_ID}']"
            backlog_card = f".board-col-backlog .board-card[data-task-id='{DEMO_TASK_ID}']"
            assert page.query_selector(lane_card)
            self._tag_board(page)

            page.click("#task-panel button:has-text('Backlog')")
            settle(page)
            poll_until(lambda: page.query_selector(backlog_card),
                       timeout=TIMEOUT, message="card did not land in the backlog column")
            assert page.query_selector(lane_card) is None
            assert self._board_kept(page)
            assert api.get(f"/api/tasks/{DEMO_TASK_ID}")["boardColumnId"] is None
        finally:
            # Hide the backlog column again — the toggle is remembered per board
            # (localStorage) and test_board.py starts from it being hidden. The
            # autouse placement fixture puts the task back on its seed lane.
            if page.is_visible("#task-panel.open"):
                page.click(".panel-close")
                settle(page)
            page.click('[data-act="toggleBacklogColumn"]')
            settle(page)

    def test_priority_change_keeps_focus_on_the_select(self, task_panel, api):
        # The panel repaints itself from cache after the save, which replaces the
        # select the user just operated: focus has to land back on its twin, or
        # keyboard users lose their place mid-edit.
        sel = "select[data-change='changePriority']"
        # select_option alone does not focus the control the way a click does, so
        # focus it first — that is the state this test is about.
        task_panel.focus(sel)
        task_panel.query_selector(sel).select_option("LOW")
        settle(task_panel)
        poll_until(
            lambda: api.get(f"/api/tasks/{DEMO_TASK_ID}")["priority"] == "LOW",
            timeout=TIMEOUT)
        focused = task_panel.evaluate(
            "document.activeElement?.getAttribute('data-change') || ''")
        assert focused == "changePriority"
        api.post(f"/api/tasks/{DEMO_TASK_ID}/priority", {"priority": "HIGH"})


class TestCopyAndArchive:
    def test_archive_and_reopen_task(self, task_panel, api):
        # This test needs a non-terminal task: a DONE or ARCHIVED one offers
        # Reopen where it expects Archive. Don't inherit whatever a sibling
        # left — the two tests in this class share demo task 201, and each
        # used to assume the status the other left behind.
        if api.get(f"/api/tasks/{DEMO_TASK_ID}")["status"] in ("DONE", "ARCHIVED"):
            api.post(f"/api/tasks/{DEMO_TASK_ID}/reopen", {})
            task_panel.reload()
            task_panel.click(f".board-card:has-text('{DEMO_TASK_TITLE}')")
            task_panel.wait_for_selector("#task-panel.open", timeout=TIMEOUT)
        # Scope to #task-panel: the project sidebar also has an "Archive" nav
        # button (the archived-tasks view), so an unscoped has-text('Archive')
        # is ambiguous and would click the sidebar instead of the panel action.
        # Archive
        task_panel.wait_for_selector(
            "#task-panel button:has-text('Archive')", timeout=TIMEOUT)
        task_panel.click("#task-panel button:has-text('Archive')")
        task_panel.wait_for_selector("#task-panel button:has-text('Reopen')", timeout=TIMEOUT)
        task = api.get(f"/api/tasks/{DEMO_TASK_ID}")
        assert task["status"] == "ARCHIVED"

        # Reopen
        task_panel.click("#task-panel button:has-text('Reopen')")
        task_panel.wait_for_selector("#task-panel button:has-text('Archive')", timeout=TIMEOUT)
        task = api.get(f"/api/tasks/{DEMO_TASK_ID}")
        assert task["status"] == "PLANNED"

    def test_immutable_done_task_hides_edit_controls(self, demo_board, api):
        # Drive the task to DONE from a KNOWN state rather than inheriting one.
        # A terminal task (DONE/ARCHIVED) rejects a status change with
        # TASK_IMMUTABLE, so a run that inherited ARCHIVED from a sibling test
        # used to fail here for a reason that had nothing to do with what this
        # test is about. Reopen first, then set DONE: the result is the same
        # whatever the previous test left behind.
        if api.get(f"/api/tasks/{DEMO_TASK_ID}")["status"] in ("DONE", "ARCHIVED"):
            api.post(f"/api/tasks/{DEMO_TASK_ID}/reopen", {})
        api.post(f"/api/tasks/{DEMO_TASK_ID}/status", {"status": "DONE"})

        try:
            demo_board.reload()
            if not demo_board.is_visible(".board-col"):
                demo_board.click("text=Demo Project")
                demo_board.wait_for_selector(".board-col", timeout=TIMEOUT)

            # Open the DONE task from its board card (the standalone Tasks list
            # view was removed; the task stays on the board once marked DONE —
            # the server moves it into the Done column).
            demo_board.click(f".board-card:has-text('{DEMO_TASK_TITLE}')")
            demo_board.wait_for_selector("#task-panel.open", timeout=TIMEOUT)
            # `.open` lands on the container BEFORE the panel renders its body,
            # so sampling here reads an empty panel: every `is_visible` below
            # would be False and the negative Save assertion would pass
            # vacuously — which is exactly how this failed, looking like a
            # state bug rather than the race it is. Wait for a control the
            # detail tab owns before asserting anything about the tab.
            demo_board.wait_for_selector(
                "select.detail-select[data-change='changeType']", timeout=TIMEOUT)

            # Save button should NOT be present, Reopen should be visible
            assert not demo_board.is_visible("#panel-tab-content button:has-text('Save')")
            assert demo_board.is_visible("#task-panel button:has-text('Reopen')")

            # Editors the API rejects with TASK_IMMUTABLE render disabled (the
            # status select is absent here — a boarded task has no status row);
            # placement fields stay live, matching the API's carve-out.
            assert demo_board.eval_on_selector(
                "select.detail-select[data-change='changeType']", "e => e.disabled")
            assert demo_board.get_attribute("#task-due-date", "disabled") is not None
            assert not demo_board.eval_on_selector(
                "select.detail-select[data-change='updateTaskField'][data-a1='sprintId']",
                "e => e.disabled")
        finally:
            # Restore in `finally`: when this test failed mid-way it used to
            # leave the task DONE, and the next test to touch it inherited a
            # state it could not change. One failure became two.
            if api.get(f"/api/tasks/{DEMO_TASK_ID}")["status"] in ("DONE", "ARCHIVED"):
                api.post(f"/api/tasks/{DEMO_TASK_ID}/reopen", {})


class TestCommentsTab:
    def test_comments_tab_shows_existing_comment(self, task_panel):
        task_panel.click(".panel-tab:has-text('Comments')")
        settle(task_panel)
        assert task_panel.is_visible(".comment")

    def test_existing_comment_text(self, task_panel):
        task_panel.click(".panel-tab:has-text('Comments')")
        settle(task_panel)
        comment = task_panel.query_selector(".comment-text")
        assert "Working on this now" in comment.inner_text()

    def test_add_comment_appears_in_list(self, task_panel, api):
        task_panel.click(".panel-tab:has-text('Comments')")
        settle(task_panel)
        text = unique("Test comment")
        # The composer is a rich-text contenteditable (#comment-editor), not a
        # plain input — focus it and type rather than .fill().
        task_panel.click("#comment-editor")
        task_panel.keyboard.type(text)
        task_panel.click("#panel-tab-content button:has-text('Comment')")
        added = poll_until(
            lambda: next(
                (c for c in api.get(f"/api/tasks/{DEMO_TASK_ID}/comments") if c["text"] == text),
                None,
            ),
            message="comment never appeared via the API",
        )
        assert added is not None
        # The author's display name is resolved server-side (no raw author id).
        assert added.get("authorName")

    def test_comment_shows_author_name(self, task_panel):
        task_panel.click(".panel-tab:has-text('Comments')")
        settle(task_panel)
        author = task_panel.query_selector(".comment-author")
        assert "Demo User" in author.inner_text()

    def test_reply_creates_threaded_comment(self, task_panel, api):
        task_panel.click(".panel-tab:has-text('Comments')")
        settle(task_panel)
        # Open the inline reply composer on the first comment and submit a reply.
        task_panel.click("[data-act='replyComment']")
        settle(task_panel)
        text = unique("Threaded reply")
        task_panel.click("[data-reply-editor]")
        task_panel.keyboard.type(text)
        task_panel.click("[data-act='addComment'][data-a1]")
        reply = poll_until(
            lambda: next(
                (c for c in api.get(f"/api/tasks/{DEMO_TASK_ID}/comments") if c["text"] == text),
                None,
            ),
            message="reply never appeared via the API",
        )
        assert reply is not None
        # The reply is threaded under a parent comment.
        assert reply["parentId"]
        # And it renders nested in the tree. The poll above returns as soon as the
        # API has the reply, which can precede the list re-render.
        settle(task_panel)
        assert task_panel.is_visible(".comment-replies")

    def test_edit_comment_updates_text(self, task_panel, api):
        task_panel.click(".panel-tab:has-text('Comments')")
        settle(task_panel)
        # Add a comment we own, then edit it in place.
        original = unique("Editable comment")
        task_panel.click("#comment-editor")
        task_panel.keyboard.type(original)
        task_panel.click("#panel-tab-content button:has-text('Comment')")
        added = poll_until(
            lambda: next(
                (c for c in api.get(f"/api/tasks/{DEMO_TASK_ID}/comments") if original in c["text"]),
                None,
            ),
            message="comment never appeared via the API",
        )
        assert added is not None
        cid = added["id"]
        # createdAt/updatedAt are second-precision: without this the edit lands in
        # the same second as the create and the updatedAt assertion below cannot
        # tell a real bump from none.
        await_next_second()
        # Open the inline editor for that comment, clear it, and type new text.
        # The poll above only proves the API has it; wait for the list to re-render
        # before reaching for the node.
        settle(task_panel)
        node = task_panel.query_selector(f".comment-node[data-comment-id='{cid}']")
        node.query_selector("[data-act='editComment']").click()
        settle(task_panel)
        task_panel.click("#comment-edit-editor")
        task_panel.keyboard.press("Control+a")
        task_panel.keyboard.press("Delete")
        edited = unique("Edited comment")
        task_panel.keyboard.type(edited)
        task_panel.click("[data-act='saveEditComment']")
        # The comment already exists, so poll on the edited text landing rather
        # than on the id: matching the id alone would return before the save.
        updated = poll_until(
            lambda: next(
                (c for c in api.get(f"/api/tasks/{DEMO_TASK_ID}/comments")
                 if c["id"] == cid and edited in c["text"]),
                None,
            ),
            message="comment edit never landed via the API",
        )
        assert updated is not None
        assert edited in updated["text"]
        assert original not in updated["text"]
        # The server bumps updatedAt, and the UI flags the comment as edited.
        assert updated["updatedAt"] != updated["createdAt"]
        settle(task_panel)
        assert task_panel.is_visible(
            f".comment-node[data-comment-id='{cid}'] .comment-edited")


class TestLinksTab:
    def test_links_tab_shows_existing_link(self, task_panel):
        task_panel.click(".panel-tab:has-text('Links')")
        settle(task_panel)
        assert task_panel.is_visible(".link-row")

    def test_existing_link_shows_jwt_reference(self, task_panel):
        task_panel.click(".panel-tab:has-text('Links')")
        settle(task_panel)
        link_row = task_panel.query_selector(".link-row")
        assert "JWT" in link_row.inner_text() or "jwt" in link_row.inner_text()

    def test_add_link_with_enter_key(self, task_panel, api):
        task_panel.click(".panel-tab:has-text('Links')")
        settle(task_panel)
        before = api.get(f"/api/tasks/{DEMO_TASK_ID}/links")
        task_panel.fill("#link-url", "https://example.com/enter-key")
        task_panel.press("#link-url", "Enter")

        def link_added():
            links = api.get(f"/api/tasks/{DEMO_TASK_ID}/links")
            return links if len(links) == len(before) + 1 else None

        after = poll_until(link_added, message="Enter did not submit the link form")
        new = next(l for l in after if l["url"] == "https://example.com/enter-key")
        api.delete(f"/api/tasks/{DEMO_TASK_ID}/links/{new['id']}")

    def test_add_and_delete_link(self, task_panel, api):
        task_panel.click(".panel-tab:has-text('Links')")
        settle(task_panel)
        before = api.get(f"/api/tasks/{DEMO_TASK_ID}/links")

        task_panel.fill("#link-url", "https://example.com/test")
        task_panel.fill("#link-title", "Test Link")
        task_panel.click("#panel-tab-content button:has-text('Add')")

        def link_added():
            links = api.get(f"/api/tasks/{DEMO_TASK_ID}/links")
            return links if len(links) == len(before) + 1 else None

        after = poll_until(link_added, message="link never appeared via the API")
        assert len(after) == len(before) + 1
        new_link = next(l for l in after if l["url"] == "https://example.com/test")

        # Delete it. The row is only removed once the DELETE has gone out, so wait
        # for the app to go quiet rather than polling for the absence.
        task_panel.locator("#panel-tab-content .link-row").last.locator(".btn-icon").click()
        settle(task_panel)
        final = api.get(f"/api/tasks/{DEMO_TASK_ID}/links")
        assert not any(l["id"] == new_link["id"] for l in final)


class TestRelationsTab:
    def test_relations_tab_shows_existing_relation(self, task_panel):
        task_panel.click(".panel-tab:has-text('Relations')")
        settle(task_panel)
        assert task_panel.is_visible(".relation-row")

    def test_existing_relation_type_shown(self, task_panel):
        task_panel.click(".panel-tab:has-text('Relations')")
        settle(task_panel)
        row = task_panel.query_selector(".relation-row")
        assert row is not None
        assert row.inner_text().strip() != ""

    def test_relation_label_is_direction_aware(self, task_panel):
        # Seed: DP-1 BLOCKS DP-2. The row on DP-1 (the source) must read
        # "Blocks"; opening DP-2 must show the same relation as "Blocked by".
        task_panel.click(".panel-tab:has-text('Relations')")
        settle(task_panel)
        assert "Blocks" in task_panel.query_selector(".relation-type").inner_text()
        task_panel.evaluate(f"() => openTaskPanel('{DEMO_TASK2_ID}')")
        settle(task_panel)
        task_panel.wait_for_selector(".panel-tab", timeout=TIMEOUT)
        task_panel.click(".panel-tab:has-text('Relations')")
        settle(task_panel)
        assert "Blocked by" in task_panel.query_selector(".relation-type").inner_text()

    def test_relation_target_opens_other_task(self, task_panel):
        task_panel.click(".panel-tab:has-text('Relations')")
        settle(task_panel)
        task_panel.click(".relation-target")
        settle(task_panel)
        title = task_panel.input_value("#panel-title-input")
        assert title != DEMO_TASK_TITLE

    def test_add_relation_form_present(self, task_panel):
        task_panel.click(".panel-tab:has-text('Relations')")
        settle(task_panel)
        assert task_panel.is_visible("#rel-type")
        assert task_panel.is_visible("#rel-target")

    def test_add_relation_via_form(self, task_panel, api):
        before = {r["id"] for r in api.get(f"/api/tasks/{DEMO_TASK_ID}/relations")}
        task_panel.click(".panel-tab:has-text('Relations')")
        settle(task_panel)
        task_panel.select_option("#rel-type", "DUPLICATES")
        task_panel.select_option("#rel-target", DEMO_TASK2_ID)
        task_panel.click(".relation-form button:has-text('Add')")

        def relation_added():
            # One add writes two rows (the relation plus its symmetric
            # inverse); wait for the source-side row specifically.
            rels = api.get(f"/api/tasks/{DEMO_TASK_ID}/relations")
            return [r for r in rels if r["id"] not in before
                    and r["sourceTaskId"] == DEMO_TASK_ID] or None

        new = poll_until(relation_added, message="relation never appeared via the API")[0]
        try:
            # The API historically defaulted a mis-named field to RELATES_TO;
            # assert the exact type so a silent fallback fails loudly here.
            assert new["relationType"] == "DUPLICATES"
            assert new["targetTaskId"] == DEMO_TASK2_ID
            # The panel must show the new relation once, not once per stored
            # direction.
            #
            # Wait for the PANEL, not for quiescence. `poll_until` above proves
            # only that the SERVER has the relation — it queries the API
            # directly, so it can return before the page's own POST response has
            # even come back. `settle()` then samples the in-flight counter and
            # can catch the momentary zero *between* that POST finishing and the
            # re-render's GET starting, returning while the list is still the
            # pre-add render; the count is then 0 and the assertion fails for a
            # reason that has nothing to do with what it is testing. Polling the
            # row count waits for the state actually being asserted.
            dup_rows = task_panel.locator(".relation-type:has-text('Duplicates')")
            poll_until(lambda: dup_rows.count() > 0 or None,
                       message="the panel never rendered the added relation")
            assert dup_rows.count() == 1, \
                "the relation must render once, not once per stored direction"
        finally:
            # Deleting either row removes both directions server-side.
            api.delete(f"/api/tasks/{DEMO_TASK_ID}/relations/{new['id']}")


class TestAttachmentSidebar:
    # Attachments are managed entirely from the details-tab sidebar; the
    # dedicated Attachments tab was removed as redundant.
    def test_sidebar_shows_existing(self, task_panel):
        assert task_panel.is_visible(".att-sidebar .att-row")

    def test_existing_attachment_filename(self, task_panel):
        row = task_panel.query_selector(".att-sidebar .att-row")
        assert "auth-diagram" in row.inner_text()

    def test_sidebar_has_upload_button(self, task_panel):
        assert task_panel.is_visible(".att-sidebar-actions button")

    def test_no_external_link_form_in_sidebar(self, task_panel):
        # External URLs have exactly one home — the Links tab. The sidebar's
        # add-external-link form (attachments-with-URL) was removed.
        assert task_panel.query_selector(".att-sidebar .link-form") is None


class TestBranchesTab:
    def test_branches_tab_shows_existing_branch(self, task_panel):
        task_panel.click(".panel-tab:has-text('Branches')")
        settle(task_panel)
        assert task_panel.is_visible(".link-row")

    def test_existing_branch_name_shown(self, task_panel):
        task_panel.click(".panel-tab:has-text('Branches')")
        settle(task_panel)
        row = task_panel.query_selector(".link-row")
        assert "feature" in row.inner_text()

    def test_branch_create_form_present(self, task_panel):
        task_panel.click(".panel-tab:has-text('Branches')")
        settle(task_panel)
        assert task_panel.is_visible("#br-name") or task_panel.is_visible(
            "text=No repository connected"
        )


class TestActivityTab:
    def test_activity_tab_shows_entries(self, task_panel):
        task_panel.click(".panel-tab:has-text('Activity')")
        settle(task_panel)
        # Should either show activity items or "No activity yet"
        has_items = task_panel.is_visible(".activity-item")
        has_empty = "activity" in task_panel.query_selector(
            "#panel-tab-content"
        ).inner_text().lower()
        assert has_items or has_empty
