"""Tests for the per-project task settings: optional THEME/INITIATIVE
hierarchy levels, the effort-estimation unit and admin-defined custom
priorities (gear menu → "Task types & priorities")."""

import pytest
from conftest import desktop_url, SHORT, TIMEOUT, unique, settle


@pytest.fixture
def settings_project(app, api):
    """A fresh project opened in the UI (via its bookmarkable URL), deleted
    again afterwards."""
    project = api.post("/api/projects", {"name": unique("Settings UI")})
    app.goto(desktop_url(f"#/projects/{project['id']}/board"))
    app.wait_for_selector("#project-settings-btn", timeout=TIMEOUT)
    yield app, project
    api.delete(f"/api/projects/{project['id']}")


def open_settings_modal(page):
    page.click("#project-settings-btn")
    page.wait_for_timeout(300)
    page.click("button:has-text('Task types & priorities')")
    page.wait_for_selector("input[data-a0='themeEnabled']", timeout=TIMEOUT)


def stamp_repaint_probe(page):
    """Puts a sentinel node inside #content. renderContent() replaces that
    subtree wholesale, so the probe surviving proves the view behind the dialog
    was never repainted."""
    page.evaluate(
        "() => { const s = document.createElement('span');"
        " s.id = 'repaint-probe';"
        " document.querySelector('#content').appendChild(s); }"
    )


class TestHierarchyLevels:
    def test_toggles_enable_theme_and_initiative(self, settings_project, api):
        page, project = settings_project
        open_settings_modal(page)
        page.check("input[data-a0='themeEnabled']")
        settle(page)
        page.check("input[data-a0='initiativeEnabled']")
        settle(page)
        fresh = api.get(f"/api/projects/{project['id']}")
        assert fresh["themeEnabled"] is True
        assert fresh["initiativeEnabled"] is True

        # The create dialog now offers the extra types.
        page.click("#modal button:has-text('Close')")
        settle(page)
        page.click(".sidebar-item:has-text('Backlog')")
        settle(page)
        page.click("button:has-text('Create backlog item'), button:has-text('Create task')")
        page.wait_for_selector("#task-type", timeout=TIMEOUT)
        options = page.eval_on_selector_all("#task-type option", "os => os.map(o => o.value)")
        assert "THEME" in options and "INITIATIVE" in options

        # A theme task created through the dialog really is a THEME.
        page.fill("#task-title", "Theme via UI")
        page.select_option("#task-type", "THEME")
        page.click("#modal-submit")
        settle(page)
        tasks = api.get(f"/api/projects/{project['id']}/tasks")
        assert any(t["taskType"] == "THEME" and t["title"] == "Theme via UI" for t in tasks)

    def test_types_hidden_while_disabled(self, settings_project):
        page, _ = settings_project
        page.click(".sidebar-item:has-text('Backlog')")
        settle(page)
        page.click("button:has-text('Create backlog item'), button:has-text('Create task')")
        page.wait_for_selector("#task-type", timeout=TIMEOUT)
        options = page.eval_on_selector_all("#task-type option", "os => os.map(o => o.value)")
        assert options == ["TASK", "STORY", "EPIC", "SUBTASK"]


class TestEstimationUnit:
    def test_segmented_switch_picks_the_unit(self, settings_project, api):
        """The unit is chosen with the segmented switch the personal
        preferences use — not a dropdown — and the pick saves immediately."""
        page, project = settings_project
        open_settings_modal(page)
        seg = page.locator("#ts-estimation-unit .seg-switch")
        seg.wait_for(timeout=TIMEOUT)
        assert page.locator("#ts-estimation-unit select").count() == 0
        assert seg.locator("button").count() == 3
        # A fresh project does not estimate, so "No estimation" starts checked.
        assert seg.locator("button[data-a0='NONE']").get_attribute("aria-checked") == "true"

        seg.locator("button[data-a0='POINTS']").click()
        settle(page)
        assert api.get(f"/api/projects/{project['id']}")["estimationUnit"] == "POINTS"
        # The switch repaints from the saved project, so the check moves too.
        assert seg.locator("button[data-a0='POINTS']").get_attribute("aria-checked") == "true"
        assert seg.locator("button[data-a0='NONE']").get_attribute("aria-checked") == "false"

        # And the estimate field the unit unlocks really does appear.
        page.click("#modal button:has-text('Close')")
        settle(page)
        page.click(".sidebar-item:has-text('Backlog')")
        settle(page)
        page.click("button:has-text('Create backlog item'), button:has-text('Create task')")
        page.wait_for_selector("#task-estimate-create", timeout=TIMEOUT)


class TestNoBackgroundReload:
    def test_applying_settings_leaves_the_view_behind_the_dialog_alone(
        self, settings_project, api
    ):
        """Settings save immediately, but the board behind the open dialog must
        not blank itself and refetch on every toggle — the repaint is deferred
        to the moment the dialog closes."""
        page, project = settings_project
        open_settings_modal(page)
        stamp_repaint_probe(page)

        page.check("input[data-a0='themeEnabled']")
        settle(page)
        page.locator("#ts-estimation-unit button[data-a0='POINTS']").click()
        settle(page)

        # Both changes really were saved …
        fresh = api.get(f"/api/projects/{project['id']}")
        assert fresh["themeEnabled"] is True
        assert fresh["estimationUnit"] == "POINTS"
        # … and the dialog is still up, over an untouched board.
        assert page.locator("#ts-estimation-unit").count() == 1
        assert page.locator("#repaint-probe").count() == 1, (
            "the view behind the dialog reloaded while settings were being changed"
        )

        # Closing the dialog is what applies the change to the view.
        page.click("#modal button:has-text('Close')")
        settle(page)
        assert page.locator("#repaint-probe").count() == 0, (
            "closing the dialog must repaint the view once"
        )

    def test_dismissing_with_escape_also_repaints(self, settings_project):
        """Escape, Cancel and the backdrop bypass the submit handler, so the
        deferred repaint hangs off the dialog closing, not off its Close button."""
        page, _ = settings_project
        open_settings_modal(page)
        stamp_repaint_probe(page)
        page.check("input[data-a0='initiativeEnabled']")
        settle(page)
        assert page.locator("#repaint-probe").count() == 1

        page.keyboard.press("Escape")
        settle(page)
        assert page.locator("#repaint-probe").count() == 0

        # The now-repainted create dialog offers the level that was just enabled.
        page.click(".sidebar-item:has-text('Backlog')")
        settle(page)
        page.click("button:has-text('Create backlog item'), button:has-text('Create task')")
        page.wait_for_selector("#task-type", timeout=TIMEOUT)
        options = page.eval_on_selector_all("#task-type option", "os => os.map(o => o.value)")
        assert "INITIATIVE" in options

    def test_untouched_settings_do_not_repaint_at_all(self, settings_project):
        """Opening and closing the dialog without changing anything leaves the
        view exactly as it was."""
        page, _ = settings_project
        open_settings_modal(page)
        stamp_repaint_probe(page)
        page.click("#modal button:has-text('Close')")
        settle(page)
        assert page.locator("#repaint-probe").count() == 1


class TestCustomPriorities:
    def test_add_use_and_delete_custom_priority(self, settings_project, api):
        page, project = settings_project
        open_settings_modal(page)
        page.fill("#ts-prio-name", "urgent")
        page.click("#ts-prio-wrap button:has-text('Add')")
        page.wait_for_selector("#ts-prio-wrap .ts-prio-row", timeout=TIMEOUT)
        prios = api.get(f"/api/projects/{project['id']}/task-priorities")
        assert [p["name"] for p in prios] == ["URGENT"]
        page.click("#modal button:has-text('Close')")
        settle(page)
        # The custom priority is offered when creating a task …
        page.click(".sidebar-item:has-text('Backlog')")
        settle(page)
        page.click("button:has-text('Create backlog item'), button:has-text('Create task')")
        page.wait_for_selector("#task-priority", timeout=TIMEOUT)
        options = page.eval_on_selector_all("#task-priority option", "os => os.map(o => o.value)")
        assert "URGENT" in options
        page.fill("#task-title", "Urgent rollout")
        page.select_option("#task-priority", "URGENT")
        page.click("#modal-submit")
        settle(page)
        tasks = api.get(f"/api/projects/{project['id']}/tasks")
        task = next(t for t in tasks if t["title"] == "Urgent rollout")
        assert task["priority"] == "URGENT"

        # … and deleting it is blocked while that task uses it.
        open_settings_modal(page)
        page.click("#ts-prio-wrap .ts-prio-row button")
        settle(page)
        prios = api.get(f"/api/projects/{project['id']}/task-priorities")
        assert [p["name"] for p in prios] == ["URGENT"], "in-use priority must survive delete attempt"

        # After the task is gone the delete goes through.
        api.delete(f"/api/tasks/{task['id']}")
        page.click("#ts-prio-wrap .ts-prio-row button")
        settle(page)
        prios = api.get(f"/api/projects/{project['id']}/task-priorities")
        assert prios == []
