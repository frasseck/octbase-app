"""E2E smoke suite for the mobile SPA (octbase-mobile/).

It lives in this directory, not under octbase-mobile/, because CI's e2e job
already runs pytest here and the session browser/api fixtures in conftest.py
are app-agnostic — so mobile coverage costs no workflow change. All
mobile-specific fixtures stay in this file.

The mobile app is hash-routed and honors ?apiBase= in dev contexts. Loaded
from file:// it auto-authenticates as the seeded demo user (standalone demo
mode), so all view/flow tests use a file:// URL like the desktop suite does.
The login flow is the exception: it only renders on a non-file origin, so
those tests serve octbase-mobile/ from a loopback http.server.

Since 37b stage 2 the file:// URL points at the **built standalone bundle**
(`octbase-mobile/dist-standalone/`), not the source tree: the app is ES modules
now, and a browser refuses `import` from a `file://` origin. That bundle is
exactly the artifact the standalone demo ships, so these tests still exercise the
real file:// code path — they just need `npm run build --workspace octbase-mobile`
to have run first, and say so rather than failing obscurely if it has not.
Override with OCTBASE_MOBILE_UI_URL.
"""

import os
import threading
from functools import partial
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import quote

import pytest

from conftest import (
    API_BASE,
    DEMO_PROJECT_ID,
    DEMO_PROJECT_NAME,
    DEMO_TASK_ID,
    DEMO_TASK_TITLE,
    DEMO_USER_EMAIL,
    DEMO_USER_PASSWORD,
    SHORT,
    TIMEOUT,
    unique, settle,)

MOBILE_DIR = Path(__file__).resolve().parent.parent.parent / "octbase-mobile"
_API_QS = f"apiBase={quote(API_BASE, safe=':/')}"

# The standalone bundle is the file:// artifact (see the module docstring).
_STANDALONE_INDEX = MOBILE_DIR / "dist-standalone" / "index.html"
# ...and the HTTP-served artifact is the ordinary build. It has to be the build,
# not the source tree: since 37b stage 3 the app imports @octbase/shared, a bare
# specifier no browser can resolve, so serving js/ directly leaves the page at
# its spinner. dist/ is also what the container ships, which makes these the
# only mobile tests running against the real deployed shape.
_DIST_DIR = MOBILE_DIR / "dist"
MOBILE_FILE_URL = os.getenv(
    "OCTBASE_MOBILE_UI_URL",
    f"{_STANDALONE_INDEX.as_uri()}?{_API_QS}",
)


@pytest.fixture(scope="session", autouse=True)
def _mobile_bundle_built():
    """Fail with the fix rather than 23 selector timeouts.

    Without the build there is no index.html to open at all, and every test in
    this file would die waiting for `#app-root .app` — a symptom that points
    nowhere near "you did not run the build".
    """
    if os.getenv("OCTBASE_MOBILE_UI_URL"):
        return
    missing = [p for p in (_STANDALONE_INDEX, _DIST_DIR / "index.html") if not p.exists()]
    if missing:
        pytest.fail(
            "mobile build output missing: " + ", ".join(str(p) for p in missing) + "\n"
            "Build it first:  npm run build --workspace octbase-mobile\n"
            "(37b stage 2: the mobile SPA is ES modules, so the file:// tests load "
            "the built IIFE bundle — the same artifact the standalone demo ships — "
            "and since stage 3 the login tests serve the built dist/ rather than "
            "the source tree, which no longer resolves its own imports. "
            "Set OCTBASE_MOBILE_UI_URL to point somewhere else.)",
            pytrace=False,
        )

# A real phone context: IS_PHONE (core.js) keys off the UA, and the layout is
# phone-first, so tests run at a phone viewport with a mobile user agent.
PHONE_VIEWPORT = {"width": 390, "height": 844}
PHONE_UA = (
    "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) "
    "AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1"
)


# ── Fixtures ───────────────────────────────────────────────────────────────────
@pytest.fixture
def phone(file_browser, api):  # api ensures the API-reachability check runs first
    """Fresh phone-shaped browser context per test.

    Uses `file_browser` (conftest): most of this file loads the standalone bundle
    from `file://`, which Chrome only allows with web security relaxed. The three
    login tests below serve dist/ over HTTP and would be happy with the strict
    browser — they share this one rather than launching a third Chrome.
    """
    ctx = file_browser.new_context(viewport=PHONE_VIEWPORT, user_agent=PHONE_UA, locale="en-US")
    p = ctx.new_page()
    yield p
    ctx.close()


def mobile_goto(page, route=""):
    """Load the mobile app from file:// at the given hash route."""
    page.goto(MOBILE_FILE_URL + (f"#{route}" if route else ""))
    page.wait_for_selector("#app-root .app", timeout=TIMEOUT)
    return page


@pytest.fixture
def mobile_app(phone):
    """Mobile app booted on the dashboard (standalone demo auth)."""
    mobile_goto(phone)
    phone.wait_for_selector(".bottom-nav", timeout=TIMEOUT)
    phone.wait_for_selector(".page-section", timeout=TIMEOUT)
    return phone


@pytest.fixture
def mobile_board(phone):
    """Demo Project board open with its column switcher rendered."""
    mobile_goto(phone, f"/projects/{DEMO_PROJECT_ID}/board")
    phone.wait_for_selector(".seg-scroll .seg", timeout=TIMEOUT)
    return phone


@pytest.fixture
def mobile_task(phone):
    """Seeded demo task open in the full-screen detail view."""
    mobile_goto(phone, f"/task/{DEMO_TASK_ID}")
    phone.wait_for_selector(f".detail-title:has-text('{DEMO_TASK_TITLE}')", timeout=TIMEOUT)
    return phone


@pytest.fixture
def board_task(api):
    """A throwaway task placed in the board's first column via the API."""
    task = api.post(f"/api/v1/projects/{DEMO_PROJECT_ID}/tasks", {"title": unique("Mobile board task")})
    board = api.get(f"/api/v1/projects/{DEMO_PROJECT_ID}/boards/default")
    first_col = board["columns"][0]
    api.post(
        f"/api/v1/boards/{board['id']}/move-task",
        {"taskId": task["id"], "boardColumnId": first_col["id"], "boardRank": 999_999},
    )
    yield task
    try:
        api.delete(f"/api/v1/tasks/{task['id']}")
    except Exception:
        pass  # cleanup is best-effort; a leftover task doesn't break other tests


class _QuietHandler(SimpleHTTPRequestHandler):
    def log_message(self, *args):
        pass


@pytest.fixture(scope="session")
def mobile_origin():
    """Serve the built mobile app from a loopback origin (the real login flow
    renders there — file:// short-circuits into standalone demo auth)."""
    server = ThreadingHTTPServer(("127.0.0.1", 0), partial(_QuietHandler, directory=str(_DIST_DIR)))
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    yield f"http://127.0.0.1:{server.server_address[1]}"
    server.shutdown()
    thread.join()


@pytest.fixture
def login_page(phone, mobile_origin):
    """Mobile app loaded from the loopback origin, sitting on the login form."""
    phone.goto(f"{mobile_origin}/index.html?{_API_QS}")
    phone.wait_for_selector("#login-form", timeout=TIMEOUT)
    return phone


# ── App shell ──────────────────────────────────────────────────────────────────
class TestMobileShell:
    def test_dashboard_loads_with_sections_and_nav(self, mobile_app):
        assert len(mobile_app.query_selector_all(".page-section")) >= 1
        assert mobile_app.query_selector(".bottom-nav") is not None
        assert mobile_app.inner_text(".appbar-title").strip()

    def test_bottom_nav_has_four_items(self, mobile_app):
        assert len(mobile_app.query_selector_all(".bottom-nav .nav-item")) == 4

    def test_nav_item_switches_to_projects(self, mobile_app):
        mobile_app.click(".bottom-nav .nav-item:has-text('Projects')")
        mobile_app.wait_for_selector(f".row-card:has-text('{DEMO_PROJECT_NAME}')", timeout=TIMEOUT)


# ── Projects list ──────────────────────────────────────────────────────────────
class TestMobileProjects:
    def test_projects_list_shows_demo_project(self, phone):
        mobile_goto(phone, "/projects")
        phone.wait_for_selector(f".row-card:has-text('{DEMO_PROJECT_NAME}')", timeout=TIMEOUT)

    def test_tapping_project_opens_board(self, phone):
        mobile_goto(phone, "/projects")
        phone.click(f".row-card:has-text('{DEMO_PROJECT_NAME}')")
        phone.wait_for_selector(".seg-scroll .seg", timeout=TIMEOUT)


# ── Board ──────────────────────────────────────────────────────────────────────
class TestMobileBoard:
    def test_board_shows_four_seeded_columns(self, mobile_board):
        segs = mobile_board.query_selector_all(".seg-scroll .seg")
        assert len(segs) == 4
        names = " ".join(s.inner_text() for s in segs)
        for col in ("Planned", "In Progress", "Review", "Done"):
            assert col in names

    def test_switching_column_updates_active_segment(self, mobile_board):
        mobile_board.click(".seg-scroll .seg:has-text('Review')")
        mobile_board.wait_for_selector(".seg-scroll .seg.active:has-text('Review')", timeout=TIMEOUT)

    def test_tapping_card_opens_task_detail(self, phone, board_task):
        mobile_goto(phone, f"/projects/{DEMO_PROJECT_ID}/board")
        card = f".card.task-card:has-text('{board_task['title']}')"
        phone.wait_for_selector(card, timeout=TIMEOUT)
        # tap the title area, not the trailing move (kebab) button inside the card
        phone.click(f"{card} .task-title")
        phone.wait_for_selector(f".detail-title:has-text('{board_task['title']}')", timeout=TIMEOUT)

    def test_board_has_create_task_fab(self, mobile_board):
        assert mobile_board.query_selector(".fab") is not None


# ── Task detail ────────────────────────────────────────────────────────────────
class TestMobileTaskDetail:
    def test_detail_shows_title_chips_and_props(self, mobile_task):
        assert mobile_task.query_selector(".detail-chips") is not None
        # status, priority and assignee property rows open edit sheets
        assert len(mobile_task.query_selector_all("button.prop")) >= 3
        assert mobile_task.query_selector("#comment-input") is not None

    def test_status_sheet_opens_and_escape_closes(self, mobile_task):
        mobile_task.click("button.prop >> nth=0")  # Status row
        mobile_task.wait_for_selector("#sheet-wrap .sheet", timeout=SHORT)
        assert len(mobile_task.query_selector_all(".sheet-opt")) >= 4  # one per status
        mobile_task.keyboard.press("Escape")
        mobile_task.wait_for_selector("#sheet-wrap", state="detached", timeout=SHORT)
        # nothing changed — still on the same task
        assert DEMO_TASK_TITLE in mobile_task.inner_text(".detail-title")

    def test_status_change_puts_an_off_board_task_on_the_board(self, phone, api):
        # Mobile carried the same defect the desktop panel did — and a comment
        # claiming status moved the task between lanes, which it never did. Work
        # started from the phone has to show up on the board like any other.
        task = api.post(f"/api/v1/projects/{DEMO_PROJECT_ID}/tasks",
                        {"title": unique("Phone off-board task")})
        assert task["boardColumnId"] is None, "fixture must start off the board"
        try:
            mobile_goto(phone, f"/task/{task['id']}")
            phone.wait_for_selector(".detail-title", timeout=TIMEOUT)
            phone.click("button.prop >> nth=0")  # Status row
            phone.wait_for_selector("#sheet-wrap .sheet", timeout=SHORT)
            phone.click(".sheet-opt >> nth=1")   # PLANNED is first; take IN_PROGRESS
            settle(phone)

            moved = api.get(f"/api/v1/tasks/{task['id']}")
            assert moved["status"] == "IN_PROGRESS"
            assert moved["boardColumnId"] is not None, "status change left it off the board"
            board = api.get(f"/api/v1/projects/{DEMO_PROJECT_ID}/boards/default")
            lane = next(c for c in board["columns"] if c["id"] == moved["boardColumnId"])
            assert lane["status"] == "IN_PROGRESS"
        finally:
            api.delete(f"/api/v1/tasks/{task['id']}")

    def test_add_comment_appends_to_list(self, mobile_task):
        text = unique("Mobile comment")
        mobile_task.fill("#comment-input", text)
        mobile_task.click(".comment-form button[type='submit']")
        mobile_task.wait_for_selector(f"#comment-list .comment:has-text('{text}')", timeout=TIMEOUT)


# ── Create task ────────────────────────────────────────────────────────────────
class TestMobileCreateTask:
    def test_empty_title_stays_on_form(self, phone):
        mobile_goto(phone, f"/projects/{DEMO_PROJECT_ID}/new")
        phone.wait_for_selector("#ct-title", timeout=TIMEOUT)
        phone.click("#ct-submit")
        # native `required` validation (or the JS backstop) blocks the submit
        settle(phone)
        assert phone.query_selector("#ct-title") is not None
        assert "/new" in phone.evaluate("() => location.hash")

    def test_create_task_navigates_to_detail(self, phone, api):
        title = unique("Mobile created task")
        mobile_goto(phone, f"/projects/{DEMO_PROJECT_ID}/new")
        phone.wait_for_selector("#ct-title", timeout=TIMEOUT)
        phone.fill("#ct-title", title)
        phone.click("#ct-submit")
        phone.wait_for_selector(f".detail-title:has-text('{title}')", timeout=TIMEOUT)
        task_id = phone.evaluate("() => location.hash.split('/').pop()")
        try:
            created = api.get(f"/api/v1/tasks/{task_id}")
            assert created["title"] == title
        finally:
            try:
                api.delete(f"/api/v1/tasks/{task_id}")
            except Exception:
                pass


# ── Backlog / search / inbox / settings ────────────────────────────────────────
class TestMobileBacklogSearchInboxSettings:
    def test_backlog_lists_task_cards(self, phone):
        mobile_goto(phone, f"/projects/{DEMO_PROJECT_ID}/backlog")
        phone.wait_for_selector(".card.task-card", timeout=TIMEOUT)

    def test_search_finds_seeded_task_and_opens_it(self, phone):
        mobile_goto(phone, "/search")
        phone.wait_for_selector("#search-input", timeout=TIMEOUT)
        phone.fill("#search-input", DEMO_TASK_TITLE)
        result = f"#search-results .task-card:has-text('{DEMO_TASK_TITLE}')"
        phone.wait_for_selector(result, timeout=TIMEOUT)
        phone.click(result)
        phone.wait_for_selector(f".detail-title:has-text('{DEMO_TASK_TITLE}')", timeout=TIMEOUT)

    def test_notifications_view_renders(self, phone):
        mobile_goto(phone, "/notifications")
        phone.wait_for_selector("#content .card-list, #content .state", timeout=TIMEOUT)
        # the error state renders a retry button — its absence means the view loaded
        assert phone.query_selector("#content [data-act='reloadRoute']") is None

    def test_settings_view_renders_switches(self, phone):
        mobile_goto(phone, "/settings")
        phone.wait_for_selector(".seg-switch", timeout=TIMEOUT)
        # language + theme + vocabulary
        assert len(phone.query_selector_all(".seg-switch")) >= 3

    def test_settings_offers_the_change_password_form(self, phone):
        """The phone gets the same capability as the desktop: POST
        /auth/change-password shipped in 1.0.8 with no UI on either surface."""
        mobile_goto(phone, "/settings")
        phone.wait_for_selector("#password-section-mobile", timeout=TIMEOUT)
        for sel in ("#pw-current-m", "#pw-new-m", "#pw-confirm-m"):
            assert phone.is_visible(sel), f"{sel} not visible"
        # No raw i18n key leaked into the new section.
        assert "settings.password" not in phone.inner_text("#password-section-mobile")

    def test_settings_password_mismatch_stays_inline(self, phone):
        """Client-side check, so it never reaches the API and cannot change the
        shared demo password — see TestSettingsPassword in test_settings.py for
        why the success path is not exercised in the browser."""
        mobile_goto(phone, "/settings")
        phone.wait_for_selector("#password-section-mobile", timeout=TIMEOUT)
        phone.fill("#pw-current-m", "whatever")
        phone.fill("#pw-new-m", "Str0ng-Temp-Passw0rd!")
        phone.fill("#pw-confirm-m", "Str0ng-Temp-Passw0rd!x")
        phone.click("#password-section-mobile button[type=submit]")
        phone.wait_for_selector("#pw-error-m:not(.hidden)", timeout=TIMEOUT)
        assert phone.inner_text("#pw-error-m").strip() != ""

    def test_settings_offers_the_vocabulary_picker(self, phone):
        """The classic vocabulary must be reachable from the phone.

        The mobile locales shipped the classic overlay while the app had no way
        to switch to it: the settings screen offered only language and theme,
        and the boot-time preference reconciliation applied only those two. So a
        phone-only user could not get the classic wording at all, and the user
        guide's promise that the choice "follows you across devices" was false
        for this surface.
        """
        mobile_goto(phone, "/settings")
        picker = phone.wait_for_selector(
            '.seg-switch[aria-label="Vocabulary"]', timeout=TIMEOUT)
        labels = [b.inner_text().strip()
                  for b in picker.query_selector_all("button")]
        # The phone says "Classic" where the desktop says "Classic project
        # management", and that is deliberate: the style guide allows the mobile
        # companion a shorter label where the desktop wording does not fit a
        # phone cell, through its OWN key in octbase-mobile/locales, provided
        # the short form means the same thing. This assertion is therefore about
        # the mobile wording on purpose — if it looks wrong to you, read the
        # style guide's "two surfaces" section before changing it back.
        assert labels == ["Agile", "Classic"], labels


# ── Login flow (loopback origin — file:// auto-authenticates) ──────────────────
class TestMobileLogin:
    def test_login_form_renders(self, login_page):
        for sel in ("#login-email", "#login-password", "#login-submit"):
            assert login_page.query_selector(sel) is not None

    def test_wrong_password_shows_error(self, login_page):
        login_page.fill("#login-email", DEMO_USER_EMAIL)
        login_page.fill("#login-password", "definitely-wrong")
        login_page.click("#login-submit")
        login_page.wait_for_selector("#login-error:not(.hidden)", timeout=TIMEOUT)
        assert login_page.query_selector("#login-form") is not None

    def test_valid_login_lands_on_dashboard(self, login_page):
        login_page.fill("#login-email", DEMO_USER_EMAIL)
        login_page.fill("#login-password", DEMO_USER_PASSWORD)
        login_page.click("#login-submit")
        login_page.wait_for_selector(".bottom-nav", timeout=TIMEOUT)
        login_page.wait_for_selector(".page-section", timeout=TIMEOUT)


# ── Completion warning over open children (OCT-301) ────────────────────────────
# The mobile half of the warning desktop got in OCT-300. Marking a container DONE
# while open work sits under it stays permitted — BLOCKER priority is the
# product's mechanism for holding a parent open, and widening the API guard once
# caused a permanent lockout. What these tests pin is that the phone SAYS so
# first, at both of its completion doors, and that saying "no" really writes
# nothing.
@pytest.fixture
def story_with_open_child(api):
    """A story carrying one still-open task, the story placed on the board."""
    story = api.post(
        f"/api/v1/projects/{DEMO_PROJECT_ID}/tasks",
        {"title": unique("Mobile parent story"), "taskType": "STORY"},
    )
    child = api.post(
        f"/api/v1/projects/{DEMO_PROJECT_ID}/tasks",
        {"title": unique("Mobile open child"), "taskType": "TASK", "parentId": story["id"]},
    )
    board = api.get(f"/api/v1/projects/{DEMO_PROJECT_ID}/boards/default")
    api.post(
        f"/api/v1/boards/{board['id']}/move-task",
        {"taskId": story["id"], "boardColumnId": board["columns"][0]["id"], "boardRank": 999_999},
    )
    yield story, child, board
    # Children before parents — deleting a task that still has one is refused
    # with TASK_HAS_CHILDREN (see KNOWN_FAILURES.md on the reap ordering).
    for task in (child, story):
        try:
            api.delete(f"/api/v1/tasks/{task['id']}")
        except Exception:
            pass


def _open_status_sheet(page, task_id):
    mobile_goto(page, f"/task/{task_id}")
    page.wait_for_selector(".prop[data-act='openStatusSheet']", timeout=TIMEOUT)
    page.click(".prop[data-act='openStatusSheet']")
    page.wait_for_selector(".sheet-opt[data-act='pickStatus'][data-a1='DONE']", timeout=TIMEOUT)
    page.click(".sheet-opt[data-act='pickStatus'][data-a1='DONE']")


class TestMobileCompletionWarning:
    def test_status_done_warns_and_names_the_open_child(self, phone, api, story_with_open_child):
        story, child, _ = story_with_open_child
        _open_status_sheet(phone, story["id"])
        body = phone.wait_for_selector(".confirm-body", timeout=TIMEOUT).inner_text()
        assert "1" in body
        # The child is named so the reader can go and look at what keeps running.
        assert child["title"] in body
        assert phone.query_selector("[data-act='confirmSheetYes']") is not None

    def test_cancelling_writes_nothing(self, phone, api, story_with_open_child):
        story, _, _ = story_with_open_child
        _open_status_sheet(phone, story["id"])
        phone.wait_for_selector("[data-act='confirmSheetNo']", timeout=TIMEOUT)
        phone.click("[data-act='confirmSheetNo']")
        settle(phone)
        assert api.get(f"/api/v1/tasks/{story['id']}")["status"] != "DONE"

    def test_dismissing_by_scrim_also_writes_nothing(self, phone, api, story_with_open_child):
        """The scrim, Escape and the confirm button all have to settle the
        promise — a dismissal that only closed the sheet would leave the caller
        awaiting forever, which reads as a dead tap."""
        story, _, _ = story_with_open_child
        _open_status_sheet(phone, story["id"])
        phone.wait_for_selector(".confirm-body", timeout=TIMEOUT)
        phone.click("#sheet-scrim")
        settle(phone)
        assert phone.query_selector(".confirm-body") is None
        assert api.get(f"/api/v1/tasks/{story['id']}")["status"] != "DONE"

    def test_confirming_completes_the_parent_and_leaves_the_child_running(
        self, phone, api, story_with_open_child
    ):
        story, child, _ = story_with_open_child
        _open_status_sheet(phone, story["id"])
        phone.wait_for_selector("[data-act='confirmSheetYes']", timeout=TIMEOUT)
        phone.click("[data-act='confirmSheetYes']")
        settle(phone)
        assert api.get(f"/api/v1/tasks/{story['id']}")["status"] == "DONE"
        # The whole point of the wording: the child is NOT closed with it.
        assert api.get(f"/api/v1/tasks/{child['id']}")["status"] != "DONE"

    def test_finishing_a_leaf_task_asks_nothing(self, phone, api, board_task):
        """No open descendants means no dialog — the ordinary case is untouched."""
        _open_status_sheet(phone, board_task["id"])
        settle(phone)
        assert phone.query_selector(".confirm-body") is None
        assert api.get(f"/api/v1/tasks/{board_task['id']}")["status"] == "DONE"

    def test_done_lane_move_warns_too(self, phone, api, story_with_open_child):
        """The second door: a drop into a Done lane completes the task
        server-side, so it has to ask the same question."""
        story, _, board = story_with_open_child
        done_col = next(c for c in board["columns"] if c["status"] == "DONE")
        mobile_goto(phone, f"/projects/{DEMO_PROJECT_ID}/board")
        card = phone.wait_for_selector(
            f".task-card:has-text('{story['title']}') button[data-act='openMoveSheet']",
            timeout=TIMEOUT,
        )
        card.click()
        phone.wait_for_selector(
            f".sheet-opt[data-act='pickMove'][data-a1='{done_col['id']}']", timeout=TIMEOUT)
        phone.click(f".sheet-opt[data-act='pickMove'][data-a1='{done_col['id']}']")
        phone.wait_for_selector(".confirm-body", timeout=TIMEOUT)
        phone.click("[data-act='confirmSheetNo']")
        settle(phone)
        assert api.get(f"/api/v1/tasks/{story['id']}")["status"] != "DONE"
