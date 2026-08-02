"""Permission-driven UI tests for the PROJECT_OWNER / permission-matrix work
(prompts/23_project-based-permissions.md).

The seeded demo user is the creator of the Demo Project and therefore holds
PROJECT_OWNER on it. These tests verify:
  - GET /projects/{id}/permissions returns the expected permission map for
    an OWNER.
  - The task panel's delete button is shown when the cached permissions grant
    `task.delete`, and hidden when they don't — exercising the new
    `AppPerms.can()` / `S.permissionsByProject` frontend wiring.
"""

from conftest import DEMO_PROJECT_ID, DEMO_TASK_ID, TIMEOUT


# ══════════════════════════════════════════════════════════════════════════════
# Backend: GET /projects/{id}/permissions
# ══════════════════════════════════════════════════════════════════════════════

class TestPermissionsEndpoint:
    def test_owner_permissions(self, api):
        body = api.get(f"/api/projects/{DEMO_PROJECT_ID}/permissions")
        assert body["projectId"] == DEMO_PROJECT_ID
        assert body["role"] == "PROJECT_OWNER"
        perms = body["permissions"]
        for key in (
            "project.view", "project.update", "project.delete",
            "project.transfer_ownership", "project.change_roles",
            "task.create", "task.view", "task.update", "task.delete",
        ):
            assert perms[key] is True, f"PROJECT_OWNER should hold {key}"


# ══════════════════════════════════════════════════════════════════════════════
# Frontend: task delete button driven by cached permissions
# ══════════════════════════════════════════════════════════════════════════════

class TestTaskDeleteButtonPermissionGating:
    def test_delete_button_visible_for_owner(self, task_panel):
        """The seeded demo user is PROJECT_OWNER and holds task.delete."""
        assert task_panel.is_visible("#task-panel .btn-danger:has-text('Delete')")

    def test_delete_button_hidden_without_task_delete_permission(self, task_panel):
        """Simulate a role without task.delete (e.g. PROJECT_VIEWER) by
        overriding the cached permissions, then re-render the task panel and
        confirm the delete button disappears."""
        task_panel.evaluate(
            """([projectId]) => {
                S.permissionsByProject[projectId] = {
                    projectId,
                    role: 'PROJECT_VIEWER',
                    permissions: {
                        'project.view': true, 'task.view': true,
                        'project.update': false, 'project.delete': false,
                        'project.archive': false, 'project.invite_users': false,
                        'project.remove_users': false, 'project.change_roles': false,
                        'project.transfer_ownership': false,
                        'task.create': false, 'task.update': false,
                        'task.delete': false, 'task.assign': false, 'task.comment': false,
                    },
                };
            }""",
            [DEMO_PROJECT_ID],
        )
        task_panel.evaluate("([taskId]) => openTaskPanel(taskId)", [DEMO_TASK_ID])
        task_panel.wait_for_selector("#task-panel.open", timeout=TIMEOUT)
        assert not task_panel.is_visible("#task-panel .btn-danger:has-text('Delete')")
