"""UI tests for the project "Members" page (project-based permissions).

The seeded demo user is the sole PROJECT_OWNER of the Demo Project. These
tests cover the Members page opened from the project settings menu (a full
page styled like the Admin user-management panel, reached via the 'members'
project sub-view):
  - the member list renders with the demo user's role
  - the last-owner protection surfaces the backend's 422 LAST_OWNER error
    and reverts the role <select> on failure
  - sending an invitation via the invite modal succeeds
  - removing a member refreshes the page without the removed user
"""

from conftest import SHORT, TIMEOUT, toast_text, unique, settle

# Deterministic seed user (the Super Admin) used as a removable member: the demo
# user is the sole owner of the Demo Project, so a second member must be seeded.
SEED_OTHER_USER_ID = "00000000-0000-0000-0000-000000000010"
SEED_OTHER_USER_EMAIL = "super@octbase.dev"


def open_members_page(demo_board):
    demo_board.click("#project-settings-btn")
    demo_board.wait_for_selector("#project-menu.open", timeout=SHORT)
    demo_board.click("text=Manage members")
    demo_board.wait_for_selector("#members-user-list", timeout=TIMEOUT)


class TestMembersPage:
    def test_owner_listed_with_role_select(self, demo_board):
        open_members_page(demo_board)
        assert demo_board.is_visible("text=Project members")
        assert demo_board.is_visible("text=demo@octbase.dev")
        role_select = demo_board.query_selector("select[aria-label='Role']")
        assert role_select is not None
        assert role_select.input_value() == "PROJECT_OWNER"

    def test_last_owner_role_change_rejected(self, demo_board):
        open_members_page(demo_board)
        demo_board.select_option("select[aria-label='Role']", "PROJECT_ADMIN")
        settle(demo_board)
        assert "at least one owner" in toast_text(demo_board)
        role_select = demo_board.query_selector("select[aria-label='Role']")
        assert role_select.input_value() == "PROJECT_OWNER"

    def test_last_owner_removal_rejected(self, demo_board):
        open_members_page(demo_board)
        demo_board.click("button:has-text('Remove')")
        demo_board.wait_for_selector("#modal-submit:has-text('Remove')", timeout=SHORT)
        demo_board.click("#modal-submit")
        settle(demo_board)
        assert "at least one owner" in toast_text(demo_board)

    def test_send_invitation(self, demo_board):
        open_members_page(demo_board)
        demo_board.click("button:has-text('Invite a teammate')")
        demo_board.wait_for_selector("#modal-backdrop:not(.hidden)", timeout=TIMEOUT)
        email = f"{unique('member').replace(' ', '')}@example.com"
        demo_board.fill("#member-invite-email", email)
        demo_board.select_option("#member-invite-role", "PROJECT_MEMBER")
        demo_board.click("#modal-submit")
        settle(demo_board)
        assert toast_text(demo_board) == "Invitation sent"

    def test_remove_member_refreshes_page(self, demo_board):
        # Removing a member opens a confirmation modal; confirming removes the
        # user and re-renders the members page without them. Seed a removable
        # member first — the demo user is the sole owner by default.
        demo_board.evaluate(
            """async (uid) => {
                const pid = App.state.project.id;
                const members = await App.api.members.list(pid);
                if (!members.some(m => m.userId === uid)) {
                    await App.api.members.add(pid, { userId: uid, role: 'PROJECT_MEMBER' });
                }
            }""",
            SEED_OTHER_USER_ID,
        )
        open_members_page(demo_board)
        row = demo_board.query_selector(f".admin-user-row:has-text('{SEED_OTHER_USER_EMAIL}')")
        assert row is not None
        row.query_selector("button:has-text('Remove')").click()
        demo_board.wait_for_selector("#modal-submit:has-text('Remove')", timeout=SHORT)
        demo_board.click("#modal-submit")
        settle(demo_board)
        assert toast_text(demo_board) == "Member removed"
        # The members page stays open and no longer lists the removed user.
        assert demo_board.is_visible("text=Project members")
        assert not demo_board.is_visible(f".admin-user-row:has-text('{SEED_OTHER_USER_EMAIL}')")
