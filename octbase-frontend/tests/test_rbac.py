"""RBAC UI tests — verify role-based interface behaviour.

The demo user (demo@octbase.dev) has the ADMIN global role after migration 009.

Super-Admin UI tests require a super-admin user which is *not* part of the
seed.  Those tests use a separate conftest fixture (``superadmin_api``) that
creates a super-admin user via direct DB manipulation or a pre-seeded account.
If no super-admin credentials are configured the tests are skipped gracefully.

Environment variables:
  OCTBASE_SUPERADMIN_EMAIL    – email of a SUPER_ADMIN account (optional)
  OCTBASE_SUPERADMIN_PASSWORD – its password (optional)
"""

import os
import time

import pytest
import requests

from conftest import (
    API_BASE, API_PREFIX, desktop_url,
    DEMO_USER_EMAIL, DEMO_USER_PASSWORD,
    SHORT, TIMEOUT,
    unique, settle, sign_in_if_needed,)


SUPERADMIN_EMAIL    = os.getenv("OCTBASE_SUPERADMIN_EMAIL", "")
SUPERADMIN_PASSWORD = os.getenv("OCTBASE_SUPERADMIN_PASSWORD", "")


# ── helpers ────────────────────────────────────────────────────────────────────

class _ApiClient:
    """Minimal request client that logs in once and stores a Bearer token."""

    def __init__(self, email: str, password: str):
        s = requests.Session()
        s.headers["Content-Type"] = "application/json"
        r = s.post(
            f"{API_BASE}{API_PREFIX}/auth/login",
            json={"email": email, "password": password},
            timeout=5,
        )
        r.raise_for_status()
        token = r.json()["accessToken"]
        s.headers["Authorization"] = f"Bearer {token}"
        self.s = s

    def get(self, path: str):
        return self.s.get(f"{API_BASE}{API_PREFIX}{path}", timeout=5)

    def post(self, path: str, body: dict = None):
        return self.s.post(f"{API_BASE}{API_PREFIX}{path}", json=body or {}, timeout=5)

    def patch(self, path: str, body: dict):
        return self.s.patch(f"{API_BASE}{API_PREFIX}{path}", json=body, timeout=5)


def _login_page(page, email: str, password: str):
    """Navigate to the app and sign in if a login form is shown."""
    page.goto(desktop_url())
    if sign_in_if_needed(page, email, password):
        settle(page)
# ── fixtures ───────────────────────────────────────────────────────────────────

@pytest.fixture(scope="module")
def admin_api(api):
    """Raw-response client logged in as the ADMIN demo user (from conftest)."""
    return _ApiClient(DEMO_USER_EMAIL, DEMO_USER_PASSWORD)


@pytest.fixture(scope="module")
def superadmin_api():
    """ApiClient logged in as SUPER_ADMIN.  Skips if credentials not set."""
    if not SUPERADMIN_EMAIL or not SUPERADMIN_PASSWORD:
        pytest.skip("OCTBASE_SUPERADMIN_EMAIL / _PASSWORD not configured")
    try:
        return _ApiClient(SUPERADMIN_EMAIL, SUPERADMIN_PASSWORD)
    except Exception as exc:
        pytest.skip(f"super-admin login failed: {exc}")


# ══════════════════════════════════════════════════════════════════════════════
# Backend permission tests (no browser needed)
# ══════════════════════════════════════════════════════════════════════════════

class TestBackendPermissions:
    """Verify the API enforces RBAC independently of the UI."""

    def test_admin_cannot_list_users(self, admin_api):
        r = admin_api.get("/users")
        assert r.status_code == 403, f"expected 403, got {r.status_code}"

    def test_admin_cannot_view_audit_logs(self, admin_api):
        r = admin_api.get("/audit-logs")
        assert r.status_code == 403

    def test_admin_can_create_project(self, admin_api):
        name = unique("RBAC project")
        r = admin_api.post("/projects", {"name": name, "visibility": "PRIVATE"})
        assert r.status_code == 201, f"expected 201, got {r.status_code}: {r.text}"

    def test_superadmin_can_list_users(self, superadmin_api):
        r = superadmin_api.get("/users")
        assert r.status_code == 200

    def test_superadmin_can_view_audit_logs(self, superadmin_api):
        r = superadmin_api.get("/audit-logs")
        assert r.status_code == 200

    def test_superadmin_can_create_admin(self, superadmin_api):
        email = unique("newadmin") + "@rbactest.invalid"
        r = superadmin_api.post("/users", {
            "email": email, "displayName": "Test Admin",
            "password": "securepass123", "globalRole": "ADMIN",
        })
        assert r.status_code == 201
        assert r.json()["globalRole"] == "ADMIN"

    def test_superadmin_cannot_create_superadmin_via_api(self, superadmin_api):
        email = unique("evil") + "@rbactest.invalid"
        r = superadmin_api.post("/users", {
            "email": email, "displayName": "Evil",
            "password": "securepass123", "globalRole": "SUPER_ADMIN",
        })
        assert r.status_code == 403

    def test_admin_me_returns_global_role(self, admin_api):
        r = admin_api.get("/auth/me")
        assert r.status_code == 200
        body = r.json()
        assert body["globalRole"] == "ADMIN"
        assert "projectMemberships" in body


# ══════════════════════════════════════════════════════════════════════════════
# Frontend UI tests — ADMIN role
# ══════════════════════════════════════════════════════════════════════════════

class TestAdminUI:
    """The demo user is ADMIN.  Verify the admin-specific UI is present."""

    def test_new_project_button_visible_for_admin(self, app):
        """Admin sees the New Project sidebar item."""
        assert app.is_visible('[data-act="showCreateProject"]')

    def test_user_management_hidden_from_admin(self, app):
        """Admin does NOT see the User Management sidebar entry."""
        nav = app.inner_text("#sidebar-nav")
        assert "User Management" not in nav

    def test_audit_logs_link_hidden_from_admin(self, app):
        """Admin does NOT see the Audit Logs sidebar entry."""
        nav = app.inner_text("#sidebar-nav")
        assert "Audit Logs" not in nav

    def test_admin_can_open_create_project_modal(self, app):
        """Clicking New Project opens the modal without an error toast."""
        # data-act, not text=New Project: the "New project from export" button
        # also matches that text and would swallow the click.
        app.click('[data-act="showCreateProject"]')
        app.wait_for_selector("#modal", timeout=TIMEOUT)
        assert app.is_visible("#modal")
        # Close modal
        close = app.query_selector("#modal-cancel, #modal-close, .modal-overlay")
        if close:
            close.click()
            settle(app)
    def test_403_is_handled_gracefully(self, page, api):
        """Navigating to /admin shows an access-denied message, not a crash."""
        _login_page(page, DEMO_USER_EMAIL, DEMO_USER_PASSWORD)
        page.wait_for_selector("text=Demo Project", timeout=TIMEOUT)
        page.evaluate("router.go('/admin')")
        settle(page)
        text = page.inner_text("#content")
        assert "Access Denied" in text or "access" in text.lower()


# ══════════════════════════════════════════════════════════════════════════════
# Frontend UI tests — SUPER_ADMIN role
# ══════════════════════════════════════════════════════════════════════════════

class TestSuperAdminUI:
    """Requires OCTBASE_SUPERADMIN_EMAIL / _PASSWORD env vars."""

    @pytest.fixture(autouse=True)
    def _require_superadmin_creds(self):
        if not SUPERADMIN_EMAIL or not SUPERADMIN_PASSWORD:
            pytest.skip("super-admin credentials not configured")

    def test_user_management_visible_for_superadmin(self, page):
        """SUPER_ADMIN sees the User Management sidebar item."""
        _login_page(page, SUPERADMIN_EMAIL, SUPERADMIN_PASSWORD)
        page.wait_for_selector("#sidebar-nav", timeout=TIMEOUT)
        nav = page.inner_text("#sidebar-nav")
        assert "User Management" in nav

    def test_audit_logs_visible_for_superadmin(self, page):
        """SUPER_ADMIN sees the Audit Logs sidebar item."""
        _login_page(page, SUPERADMIN_EMAIL, SUPERADMIN_PASSWORD)
        page.wait_for_selector("#sidebar-nav", timeout=TIMEOUT)
        nav = page.inner_text("#sidebar-nav")
        assert "Audit Logs" in nav

    def test_superadmin_can_navigate_to_admin_panel(self, page):
        """SUPER_ADMIN can open the User Management view."""
        _login_page(page, SUPERADMIN_EMAIL, SUPERADMIN_PASSWORD)
        page.wait_for_selector("text=User Management", timeout=TIMEOUT)
        page.click("text=User Management")
        page.wait_for_selector("text=New User", timeout=TIMEOUT)

    def test_superadmin_can_navigate_to_audit_logs(self, page):
        """SUPER_ADMIN can open the Audit Logs view."""
        _login_page(page, SUPERADMIN_EMAIL, SUPERADMIN_PASSWORD)
        page.wait_for_selector("text=Audit Logs", timeout=TIMEOUT)
        page.click("text=Audit Logs")
        page.wait_for_selector("text=total", timeout=TIMEOUT)
