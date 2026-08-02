"""Tests for the Repositories view: listing, adding, deleting connections."""

import pytest
from conftest import (
    DEMO_PROJECT_ID, SHORT, TIMEOUT,
    navigate_to, unique, settle,)


class TestReposView:
    def test_repos_nav_item_exists(self, demo_board):
        assert demo_board.is_visible(".sidebar-item:has-text('Repositories')")

    def test_repos_view_loads(self, demo_board):
        navigate_to(demo_board, "Repositories")
        settle(demo_board)
        assert demo_board.is_visible(".repos-wrap")

    def test_repos_view_shows_add_form(self, demo_board):
        navigate_to(demo_board, "Repositories")
        settle(demo_board)
        assert demo_board.is_visible("#repo-name")
        assert demo_board.is_visible("#repo-url")
        assert demo_board.is_visible("#repo-provider")
        assert demo_board.is_visible("button:has-text('Add Repository')")

    def test_existing_repo_shown_in_list(self, demo_board, api):
        name = unique("PreExisting Repo")
        api.post(
            f"/api/projects/{DEMO_PROJECT_ID}/repository-connections",
            {"displayName": name, "repositoryUrl": "https://example.com/existing"},
        )
        navigate_to(demo_board, "Repositories")
        demo_board.wait_for_selector(f"text={name}", timeout=TIMEOUT)
        assert demo_board.is_visible(f"text={name}")

    def test_repo_item_shows_url(self, demo_board, api):
        name = unique("URL Repo")
        url = "https://example.com/url-test"
        api.post(
            f"/api/projects/{DEMO_PROJECT_ID}/repository-connections",
            {"displayName": name, "repositoryUrl": url},
        )
        navigate_to(demo_board, "Repositories")
        demo_board.wait_for_selector(f"text={name}", timeout=TIMEOUT)
        item = demo_board.query_selector(f".repo-item:has-text('{name}')")
        assert item is not None
        assert url in item.inner_text()


class TestRepoCreation:
    def test_add_repo_appears_in_list(self, demo_board):
        navigate_to(demo_board, "Repositories")
        settle(demo_board)
        name = unique("UI Added Repo")
        demo_board.fill("#repo-name", name)
        demo_board.fill("#repo-url", "https://github.com/example/ui-test")
        demo_board.click("button:has-text('Add Repository')")
        demo_board.wait_for_selector(f"text={name}", timeout=TIMEOUT)
        assert demo_board.is_visible(f"text={name}")

    def test_add_repo_persisted_in_api(self, demo_board, api):
        navigate_to(demo_board, "Repositories")
        settle(demo_board)
        name = unique("API Persisted Repo")
        demo_board.fill("#repo-name", name)
        demo_board.fill("#repo-url", "https://github.com/example/api-test")
        demo_board.click("button:has-text('Add Repository')")
        demo_board.wait_for_selector(f"text={name}", timeout=TIMEOUT)
        repos = api.get(f"/api/projects/{DEMO_PROJECT_ID}/repository-connections")
        assert any(r["displayName"] == name for r in repos)

    def test_add_repo_without_name_shows_error(self, demo_board):
        navigate_to(demo_board, "Repositories")
        settle(demo_board)
        demo_board.fill("#repo-name", "")
        demo_board.fill("#repo-url", "https://github.com/example/no-name")
        demo_board.click("button:has-text('Add Repository')")
        settle(demo_board)
        assert demo_board.is_visible(".toast")


class TestRepoDelete:
    def test_delete_repo_removes_from_list(self, demo_board, api):
        name = unique("Delete Repo")
        api.post(
            f"/api/projects/{DEMO_PROJECT_ID}/repository-connections",
            {"displayName": name, "repositoryUrl": "https://example.com/delete"},
        )
        navigate_to(demo_board, "Repositories")
        demo_board.wait_for_selector(f"text={name}", timeout=TIMEOUT)
        demo_board.click(f".repo-item:has-text('{name}') button[title='Delete']")
        # Confirm the deletion dialog.
        demo_board.wait_for_selector("#modal-backdrop:not(.hidden)", timeout=SHORT)
        demo_board.click("#modal-submit")
        # Absence assertion: wait for the delete and the list re-render to land,
        # then assert the row is gone — polling for "gone" could pass before the
        # delete was even issued.
        settle(demo_board)
        assert not demo_board.is_visible(f"text={name}")
