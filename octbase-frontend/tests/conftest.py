"""Shared fixtures and helpers for Octbase frontend tests.

By default tests target the live API at http://127.0.0.1:8000.
Set OCTBASE_API_BASE to point at an isolated test server when needed.
If the API is unreachable the entire session is skipped.
A session-scoped browser is shared; each test gets a fresh page context.

The desktop page itself has no default: set OCTBASE_UI_URL to a *served*, built
app (see `desktop_url` below and tests/KNOWN_FAILURES.md). The mobile tests carry
their own OCTBASE_MOBILE_UI_URL and still run without it.
"""

import os
import time

import pytest
import requests
from playwright.sync_api import sync_playwright

# ── Constants ──────────────────────────────────────────────────────────────────
API_BASE         = os.getenv("OCTBASE_API_BASE", "http://127.0.0.1:8000")

# The desktop page under test. There is deliberately NO default any more.
#
# Until 37b stage 2 this fell back to loading the source `octbase-frontend/
# index.html` over a file:// URL, and that was correct while the desktop app was
# ordered classic scripts. It cannot work now: index.html carries a single module
# entry (js/main.js) whose imports are bare specifiers such as
# `@octbase/shared/i18n.js`, and a browser can neither resolve a bare specifier
# without the bundler nor import anything from a file:// origin. The page loads
# blank, and the suite reports a wall of fixture timeouts that names nothing.
#
# So the desktop tests skip — loudly, once, before burning a timeout — rather
# than drive a page that cannot boot. `pytest_collection_modifyitems` below does
# the skipping, so `test_mobile.py` (which has its own MOBILE_FILE_URL and a
# still-valid file:// target in dist-standalone/) and the `no_stack` helper tests
# keep running with nothing configured.
UI_URL_ENV = "OCTBASE_UI_URL"
FILE_URL   = os.getenv(UI_URL_ENV, "")
_UI_URL_SET = bool(FILE_URL)
_UI_URL_HELP = (
    f"{UI_URL_ENV} is not set, and the desktop suite has no working default since "
    "37b stage 2: index.html is a module entry with bare specifiers, which a "
    "browser cannot load from file://. Build and serve the app, then point the "
    "suite at it:\n"
    "    npm run build\n"
    "    cd octbase-frontend && OCTBASE_API_ORIGIN=<api> \\\n"
    "        npx vite preview --port 4173 --strictPort &\n"
    f"    {UI_URL_ENV}='http://localhost:4173/index.html?e2e=1' pytest -q\n"
    "Run the preview from octbase-frontend/ (no vite config exists at the repo "
    "root) and confirm it is your server on that port — see "
    "tests/KNOWN_FAILURES.md, 'The preview lies in two ways'."
)


def desktop_url(extra_query: str = "") -> str:
    """The desktop page to navigate to — or a legible skip if none is configured.

    Every desktop navigation goes through here so that an unset OCTBASE_UI_URL
    reports itself once, by name, instead of loading a blank page and letting the
    caller time out on a selector. Under CI it is an error rather than a skip: a
    job that forgot the variable would otherwise report a green run in which
    nothing was actually driven.
    """
    if not _UI_URL_SET:
        if os.getenv("CI"):
            pytest.fail(_UI_URL_HELP, pytrace=False)
        pytest.skip(_UI_URL_HELP)
    return FILE_URL + extra_query


DEMO_USER_ID     = "00000000-0000-0000-0000-000000000001"
DEMO_USER_EMAIL  = "demo@octbase.dev"
DEMO_USER_PASSWORD = "demopass1234"
DEMO_PROJECT_ID  = "00000000-0000-0000-0000-000000000101"
DEMO_PROJECT_NAME = "Demo Project"
DEMO_TASK_TITLE  = "Implement user authentication"
DEMO_TASK_ID     = "00000000-0000-0000-0000-000000000201"
DEMO_TASK2_ID    = "00000000-0000-0000-0000-000000000202"
DEMO_MILESTONE_ID = "00000000-0000-0000-0000-000000000401"
DEMO_PAGE_TITLE  = "Getting Started"

TIMEOUT = 8_000   # ms — for API-backed waits
SHORT   = 2_000   # ms — for UI-only transitions


# ── API client ─────────────────────────────────────────────────────────────────
API_PREFIX = "/api/v1"


def _normalize(path: str) -> str:
    """Map legacy ``/api/<x>`` paths onto the current ``/api/v1/<x>`` routes so
    individual tests do not each need editing."""
    if path.startswith("/api/v1/"):
        return path
    if path.startswith("/api/"):
        return API_PREFIX + path[len("/api"):]
    return path


class ApiClient:
    """Thin requests wrapper for test setup and state verification.

    The backend is JWT-only, so the client signs in as the seeded demo user and
    sends a Bearer token on every request. The client is session-scoped while
    access tokens expire after OCTBASE_JWT_ACCESS_TTL (default 15m) — a full
    suite run outlives that — so a 401 triggers a one-shot re-login + retry.
    """

    def __init__(self, base_url=API_BASE):
        self.base_url = base_url
        self.s = requests.Session()
        self.s.headers.update({"Content-Type": "application/json"})
        self._login()

    def _login(self):
        resp = self.s.post(
            f"{self.base_url}{API_PREFIX}/auth/login",
            json={"email": DEMO_USER_EMAIL, "password": DEMO_USER_PASSWORD},
        )
        resp.raise_for_status()
        token = resp.json()["accessToken"]
        self.s.headers.update({"Authorization": f"Bearer {token}"})

    def _request(self, method, path, **kwargs):
        r = self.s.request(method, f"{self.base_url}{_normalize(path)}", **kwargs)
        if r.status_code == 401:
            self._login()
            r = self.s.request(method, f"{self.base_url}{_normalize(path)}", **kwargs)
        r.raise_for_status()
        return r

    def get(self, path):
        return self._request("GET", path).json()

    def post(self, path, data=None):
        return self._request("POST", path, json=data or {}).json()

    def patch(self, path, data):
        return self._request("PATCH", path, json=data).json()

    def delete(self, path):
        self._request("DELETE", path)


def unique(prefix: str) -> str:
    """Return a name that is unique within the test run."""
    return f"{prefix} {int(time.time() * 1000) % 10_000_000}"


# ── Waiting on conditions, not on the clock ────────────────────────────────────
# The app talks to a local API that answers in single-digit milliseconds, so a
# fixed `wait_for_timeout(SHORT)` after an action spent ~2s to cover work that
# had already finished. The two helpers below wait for the actual condition —
# they return as soon as it holds and only spend the full budget when something
# is genuinely wrong.

_INFLIGHT_ATTR = "_octbase_inflight"


def _track_inflight(page):
    """Count API requests currently in flight for `page`.

    Playwright fires request/requestfinished/requestfailed on the page, so the
    counter needs no cooperation from the app under test. Only API calls are
    counted; file:// asset loads are irrelevant to what tests wait for.
    """
    state = {"n": 0}

    def _is_api(req):
        # The SSE stream (/events) is deliberately long-lived: it starts on the
        # board view and never "finishes", so counting it would peg the counter
        # at 1 forever and silently turn every settle() back into a full sleep.
        # Same reasoning for any websocket. Neither carries work a test waits on.
        if req.resource_type in ("eventsource", "websocket"):
            return False
        if "/events" in req.url:
            return False
        return req.url.startswith(API_BASE)

    def _start(req):
        if _is_api(req):
            state["n"] += 1

    def _end(req):
        if _is_api(req):
            state["n"] = max(0, state["n"] - 1)

    page.on("request", _start)
    page.on("requestfinished", _end)
    page.on("requestfailed", _end)
    setattr(page, _INFLIGHT_ATTR, state)
    return state


def _pending_debounces(page) -> int:
    """Debounced callbacks the app has scheduled but not yet run.

    `debounced()` (js/framework.js) keeps this count on `S.pendingDebounces`. It
    matters because work the app used to do synchronously per keystroke is now
    coalesced: the task search re-renders the list and rewrites the URL ~140ms
    after typing stops, and the description editor's dirty flag repaints ~200ms
    after. Both are UI-only, so no request appears in flight and the in-flight
    counter alone would report an app that is still about to repaint as idle —
    `settle()` would race the timer exactly the way the `wait_for_timeout` calls
    it replaced did.

    Returns 0 for a page with no app on it (the login screen, a dead context).
    """
    try:
        return page.evaluate("() => (window.S && window.S.pendingDebounces) || 0")
    except Exception:
        return 0


def settle(page, timeout: int = SHORT):
    """Wait until the app is quiet: no API request in flight, no debounced
    callback pending, and a frame painted.

    Replaces `wait_for_timeout(SHORT)` after an action. Typically returns in a
    few tens of milliseconds (a debounce in flight makes it that debounce long).
    Never raises: callers assert the outcome themselves, and a still-busy app
    just means the following assertion fails on its own terms rather than on an
    opaque timeout here.
    """
    state = getattr(page, _INFLIGHT_ATTR, None)
    deadline = time.monotonic() + timeout / 1000
    # An action's request is issued from a JS task that may not have run yet;
    # give it a beat to appear before concluding the app is idle.
    page.wait_for_timeout(15)
    while time.monotonic() < deadline:
        if (state is None or state["n"] == 0) and _pending_debounces(page) == 0:
            # Let queued renders paint, then re-check: a completed fetch often
            # schedules the next one from its .then().
            try:
                page.evaluate(
                    "() => new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r)))"
                )
            except Exception:
                return  # page/context went away — nothing to settle
            if (state is None or state["n"] == 0) and _pending_debounces(page) == 0:
                return
        page.wait_for_timeout(15)


def await_next_second():
    """Block until the wall clock crosses into the next second.

    The API serialises createdAt/updatedAt at second precision, so a write that
    lands in the same second as the row's creation is indistinguishable from one
    that never bumped the timestamp. Tests asserting "updatedAt moved" need the
    two writes in different seconds; this buys that for under a second, where a
    flat multi-second sleep used to buy it by accident.
    """
    time.sleep(1.0 - (time.time() % 1.0))


def poll_until(fn, timeout: int = TIMEOUT, interval: int = 50, message: str = ""):
    """Poll `fn` until it returns something truthy, then return it.

    Replaces `wait_for_timeout(TIMEOUT)` before reading API state back: the
    write has usually landed within a few milliseconds, so polling turns a flat
    8s sleep into ~50ms while keeping the same worst case. Raises AssertionError
    on timeout so a genuine failure still reports as a failure.
    """
    deadline = time.monotonic() + timeout / 1000
    while True:
        value = fn()
        if value:
            return value
        if time.monotonic() >= deadline:
            raise AssertionError(
                message or f"condition still false after {timeout}ms"
            )
        time.sleep(interval / 1000)


# ── Session-scoped fixtures ────────────────────────────────────────────────────
@pytest.fixture(scope="session")
def api():
    """Direct API client. Skips the whole session if the API is unreachable."""
    try:
        requests.get(f"{API_BASE}/health", timeout=3).raise_for_status()
    except Exception as exc:
        pytest.skip(f"API not reachable at {API_BASE}: {exc}")
    return ApiClient()


@pytest.fixture(scope="session")
def _pw():
    with sync_playwright() as p:
        yield p


# file:// pages have a "null" origin; Chrome enforces CORS for it (Firefox does
# not), so a page loaded from disk needs web security relaxed. Firefox needs
# neither flag, which is why this list is only ever passed to the chromium
# channels.
FILE_ORIGIN_ARGS = ["--disable-web-security", "--allow-file-access-from-files"]
# Whether the DESKTOP suite still loads from disk. Since 37b stage 6 CI serves
# the built dist/ over HTTP (OCTBASE_UI_URL), and a served page must be tested
# with the same-origin policy switched ON — otherwise the suite cannot see a CORS
# or CSP regression at all, which is precisely the class the deployed app is
# exposed to. The flags survive only for the tests that genuinely open a file://
# artifact; see `file_browser`.
#
# An unset OCTBASE_UI_URL leaves this False, which is right: the desktop tests
# skip, and the only file:// artifact left in the run is the mobile standalone
# bundle, which takes the relaxed browser from `file_browser`.
_UI_IS_FILE = FILE_URL.startswith("file:")


def _launch(_pw, name, args):
    if name == "chrome":
        return _pw.chromium.launch(channel="chrome", headless=True, args=args)
    if name == "chromium":
        return _pw.chromium.launch(headless=True, args=args)
    return _pw.firefox.launch(headless=True)


@pytest.fixture(scope="session")
def browser(_pw):
    # OCTBASE_BROWSER lets environments without Playwright's bundled Firefox
    # (e.g. unsupported host OS versions) fall back to the system Chrome via
    # the "chrome" channel.
    name = os.getenv("OCTBASE_BROWSER", "firefox")
    b = _launch(_pw, name, FILE_ORIGIN_ARGS if _UI_IS_FILE else [])
    yield b
    b.close()


@pytest.fixture(scope="session")
def file_browser(_pw, browser):
    """A browser that may open `file://` artifacts.

    The standalone demo is a real deliverable and the mobile suite drives it from
    disk on purpose, so those tests still need the relaxed flags. Keeping them in
    a separate browser is what lets the rest of the suite run with web security
    on. When the desktop URL is itself a file:// URL (the pre-stage-6 default, and
    what you get by running the suite with no OCTBASE_UI_URL), the main browser
    already carries the flags and this hands back the same instance rather than
    paying for a second Chrome.
    """
    if _UI_IS_FILE:
        yield browser
        return
    b = _launch(_pw, os.getenv("OCTBASE_BROWSER", "firefox"), FILE_ORIGIN_ARGS)
    yield b
    b.close()


# ── Per-test fixtures ──────────────────────────────────────────────────────────
@pytest.fixture
def page(browser):
    """Fresh browser context per test, instrumented for `settle()`.

    Every page carries an in-flight request counter so tests can wait for the
    app to go quiet (see `settle`) instead of sleeping a fixed interval.
    """
    ctx = browser.new_context(viewport={"width": 1400, "height": 900}, locale="en-US")
    p = ctx.new_page()
    _track_inflight(p)
    yield p
    ctx.close()


@pytest.fixture(autouse=True)
def _reap_created_entities(request):
    """Delete whatever a test leaves behind in the seeded demo project.

    The suite drives a long-lived stack, and `internal/seed` data is public
    surface the tests pin to by fixed id. Tests create tasks/pages/projects and
    used not to remove them, so every run grew the demo project. That is not
    just untidy: the tasks endpoint caps a page at 200 rows, and the seeded
    demo task is the oldest, so once the project passed 200 tasks the task fell
    out of the board's fetch window entirely — every test clicking its card then
    burned Playwright's 30s default timeout instead of failing fast.

    Snapshotting ids and removing the new ones afterwards (rather than tracking
    ApiClient calls) also reaps entities the tests create by driving the UI,
    which is most of them.

    `api` is resolved lazily so that a test marked `no_stack` — one that drives
    this module's own helpers rather than a running instance — is not skipped
    along with the session when no API is reachable.
    """
    if request.node.get_closest_marker("no_stack"):
        yield
        return
    api = request.getfixturevalue("api")
    before = _entity_snapshot(api)
    yield
    after = _entity_snapshot(api)
    # Projects first: deleting one cascades to the tasks and pages inside it, so
    # the per-entity passes below have less to do (and skip cleanly on 404).
    for pid in after["projects"] - before["projects"]:
        _delete_quietly(api, f"/api/projects/{pid}")
    new_tasks = {tid: parent for tid, parent in after["tasks"].items() if tid not in before["tasks"]}
    _reap_tasks(api, new_tasks)
    for gid in after["pages"] - before["pages"]:
        _delete_quietly(api, f"/api/pages/{gid}")


@pytest.fixture(scope="session")
def _demo_seed_placement(api):
    """(board_id, in_progress_column_id) for the demo board, resolved once.

    The seeded demo task lives on this board in the "In Progress" lane; the
    restore fixture below uses these ids to put it back after a test moves it.
    """
    boards = api.get(f"/api/projects/{DEMO_PROJECT_ID}/boards")
    board = next((b for b in boards if b.get("isDefault")), boards[0])
    cols = api.get(f"/api/boards/{board['id']}")["columns"]
    in_progress = next(c for c in cols if c["status"] == "IN_PROGRESS")
    return board["id"], in_progress["id"]


def _put_demo_task_back(api, board_id, col_id):
    """Move the seeded demo task to its seed placement: IN_PROGRESS in the
    board's "In Progress" lane. Returns True if it is there afterwards.

    Cheap in the common case — one GET when nothing has drifted.
    """
    task = api.get(f"/api/tasks/{DEMO_TASK_ID}")
    if task.get("status") == "IN_PROGRESS" and task.get("boardColumnId") == col_id:
        return True
    # DONE/ARCHIVED tasks are immutable; reopen (→ PLANNED) before editing.
    if task.get("status") in ("DONE", "ARCHIVED"):
        api.post(f"/api/tasks/{DEMO_TASK_ID}/reopen", {})
        task = api.get(f"/api/tasks/{DEMO_TASK_ID}")
    if task.get("boardColumnId") != col_id:
        api.post(f"/api/boards/{board_id}/move-task",
                 {"taskId": DEMO_TASK_ID, "boardColumnId": col_id,
                  "boardRank": 1000, "version": task["version"]})
        task = api.get(f"/api/tasks/{DEMO_TASK_ID}")
    if task.get("status") != "IN_PROGRESS":
        api.post(f"/api/tasks/{DEMO_TASK_ID}/status",
                 {"status": "IN_PROGRESS", "version": task["version"]})
    task = api.get(f"/api/tasks/{DEMO_TASK_ID}")
    return task.get("status") == "IN_PROGRESS" and task.get("boardColumnId") == col_id


@pytest.fixture(autouse=True)
def _restore_demo_task_placement(request):
    """Put the seeded demo task at its seed placement BEFORE and AFTER each
    test: IN_PROGRESS in the board's "In Progress" lane.

    `internal/seed` places "Implement user authentication" there, and
    test_board.py pins to that lane by name. Several tests move it: task-panel
    tests archive and reopen it or mark it DONE and reopen (reopen lands it in
    PLANNED), and board tests drag its card between lanes, which changes its
    status.

    **Why it restores on the way IN as well** (the fix for the defect this
    docstring used to describe from the other side): cleaning up afterwards is
    only as reliable as the cleanup itself. This body deliberately never fails a
    test on bookkeeping — a version conflict against a concurrent write, say, is
    swallowed — and the cost of that was paid by the NEXT test, which started
    from a state nobody had checked and failed on a missing button with no hint
    as to why. `test_archive_and_reopen_task` waits for a button the panel only
    offers on an open task; `test_immutable_done_task_hides_edit_controls` wants
    the opposite. Whichever met the wrong leftover lost, which is why the pair
    passed alone and failed in a suite, and swapped which one failed between
    runs. Establishing the precondition costs one GET and removes the ordering
    dependency entirely.

    `api` is resolved lazily for the same reason the reap fixture does it: a
    `no_stack` test drives this module's helpers and must not be skipped along
    with the session when no instance is reachable.
    """
    if request.node.get_closest_marker("no_stack"):
        yield
        return
    api = request.getfixturevalue("api")
    board_id, col_id = request.getfixturevalue("_demo_seed_placement")
    try:
        _put_demo_task_back(api, board_id, col_id)
    except Exception:
        pass  # never fail a test in setup on bookkeeping
    yield
    try:
        _put_demo_task_back(api, board_id, col_id)
    except Exception:
        pass


def _entity_snapshot(api):
    """Ids of the entities tests create most often, in the shared demo project.

    Tasks are a {id: parentId} map rather than a set of ids: the reap has to
    delete children before parents (see _reap_tasks), and the task list already
    carries the parent, so ordering costs no extra request.
    """
    try:
        return {
            "tasks": {t["id"]: t.get("parentId") for t in api.get(f"/api/projects/{DEMO_PROJECT_ID}/tasks?size=200")},
            "pages": {p["id"] for p in api.get(f"/api/projects/{DEMO_PROJECT_ID}/pages")},
            "projects": {p["id"] for p in api.get("/api/projects")},
        }
    except Exception:
        # Never let bookkeeping fail a test: a snapshot that cannot be taken just
        # means nothing is reaped for this test.
        return {"tasks": {}, "pages": set(), "projects": set()}


def _reap_tasks(api, parents):
    """Delete the given tasks ({id: parentId}), children before parents.

    Deleting a task that still has children is refused with 422
    TASK_HAS_CHILDREN, and _delete_quietly swallows the refusal. The reap used
    to iterate a *set*, so the order was arbitrary: whenever it happened to
    reach a parent before its child, the parent survived the run and stayed in
    the demo project forever. That is how a story "MM parent 644730" outlived a
    full suite run on the dev stack, and it feeds the 200-task cap trap in
    KNOWN_FAILURES.md — the seeded demo task is the oldest row, so a project
    that keeps growing eventually pushes it out of the board's fetch window.

    Each pass deletes the tasks that nothing else still-to-be-deleted calls
    parent, which frees the level above for the next pass. A task whose delete
    fails stays in the map and is retried on the pass after (something else may
    have been blocking it — a child created outside the 200-row snapshot, say);
    a pass that deletes nothing at all stops the loop rather than spinning.
    """
    remaining = dict(parents)
    while remaining:
        still_parents = {p for p in remaining.values() if p in remaining}
        leaves = [tid for tid in remaining if tid not in still_parents] or list(remaining)
        deleted = [tid for tid in leaves if _delete_quietly(api, f"/api/tasks/{tid}")]
        if not deleted:
            break
        for tid in deleted:
            remaining.pop(tid)


def _delete_quietly(api, path):
    """Delete, reporting whether the entity is gone. Never raises.

    True also covers the 404 a test that cleaned up in its own try/finally
    leaves behind — the entity is absent either way, which is what callers care
    about. False means it is still there (e.g. 422 TASK_HAS_CHILDREN).
    """
    try:
        api.delete(path)
        return True
    except Exception as exc:
        # requests raises HTTPError carrying the response; read the status off
        # it rather than off the message, which also contains the URL.
        resp = getattr(exc, "response", None)
        return getattr(resp, "status_code", None) == 404


def sign_in_if_needed(page, email=DEMO_USER_EMAIL, password=DEMO_USER_PASSWORD):
    """Sign in when the app booted to its login screen. Returns whether it did.

    Call this instead of sampling ``query_selector("#login-form")`` directly.
    ``page.goto()`` resolves on the `load` event, but the SPA only chooses
    between the login view and the app shell once its boot requests have
    settled — so a one-shot check can run before *either* exists, read "no login
    form", conclude the session is already authenticated, and leave the caller
    waiting out its timeout for content that needed a sign-in first.

    Loading from ``file://`` hid this: assets came off disk, so boot finished
    within `goto` in practice. Serving the built app over HTTP (37b stage 2) made
    the boot genuinely asynchronous and the race real — about 1 load in 20, which
    read as ~6-9 unrelated fixture timeouts scattered through a full run.

    Waiting for whichever view boot produced removes the race without adding a
    fixed delay: `#login-form` and `#sidebar` are the two mutually exclusive
    outcomes.

    ``state="attached"`` matters — the default waits for *visibility*, and the
    signed-in shell legitimately renders with a hidden `#sidebar` when the user's
    saved preference is a collapsed rail (`test_navigation.py`). Presence, not
    visibility, is what says boot has finished choosing.
    """
    page.wait_for_selector("#login-form, #sidebar", state="attached", timeout=TIMEOUT)
    if not page.query_selector("#login-form"):
        return False
    page.fill("#login-email", email)
    page.fill("#login-password", password)
    page.click("#login-submit")
    return True


@pytest.fixture
def app(page, api):  # api ensures API check runs first
    """App loaded at the projects list."""
    page.goto(desktop_url())
    sign_in_if_needed(page)
    page.wait_for_selector("text=Demo Project", timeout=TIMEOUT)
    return page


@pytest.fixture
def demo_board(app):
    """Demo Project board open with at least one column visible.

    Navigates via the router directly rather than clicking the sidebar's
    "Demo Project" link: the sidebar only shows the 5 most-recently-visited
    projects, and the seeded Demo Project (oldest by createdAt) can be pushed
    out of that list once enough other projects exist.
    """
    app.evaluate(f"() => router.go('/projects/{DEMO_PROJECT_ID}/board')")
    app.wait_for_selector(".board-col", timeout=TIMEOUT)
    return app


@pytest.fixture
def task_panel(demo_board):
    """Task panel open on the seeded demo task of the Demo Project."""
    demo_board.click(f".board-card:has-text('{DEMO_TASK_TITLE}')")
    demo_board.wait_for_selector("#task-panel.open", timeout=TIMEOUT)
    demo_board.wait_for_selector(".panel-tab", timeout=TIMEOUT)
    return demo_board


# ── Helpers ────────────────────────────────────────────────────────────────────
def navigate_to(page, view_label: str):
    """Click a sidebar nav item by its visible label and wait for the view."""
    page.click(f".sidebar-item:has-text('{view_label}')")
    settle(page)


def fill_modal(page, fields: dict):
    """Fill a modal's inputs by their id and submit."""
    for fid, value in fields.items():
        el = page.query_selector(f"#{fid}")
        if el:
            tag = el.evaluate("e => e.tagName.toLowerCase()")
            if tag == "select":
                el.select_option(value)
            elif tag == "textarea":
                el.fill(value)
            else:
                el.fill(value)


def submit_modal(page):
    """Submit the open modal and wait for it to close.

    A successful submit hides #modal-backdrop; a rejected one leaves the modal
    up with a validation message. Tests assert both outcomes, so this waits for
    the app to go quiet rather than requiring the modal to have closed.
    """
    page.click("#modal-submit")
    settle(page)


def toast_text(page) -> str:
    """Return the text of the most recent toast, or ''."""
    t = page.query_selector(".toast")
    return t.inner_text() if t else ""


def html5_drag(page, source, target, at="center"):
    """Perform a native HTML5 drag-and-drop from source to target.

    The board uses the HTML5 drag-and-drop API (draggable cards + dragstart/
    dragover/drop listeners that share a DataTransfer). Playwright's
    ``locator.drag_to`` / mouse APIs synthesise *mouse* events, which do NOT
    trigger native ``DragEvent``s, so the board's handlers never fire. This
    dispatches the real drag sequence with a shared DataTransfer and the drop
    coordinates set to the target's centre (the board uses clientY to choose the
    insertion index).

    ``source`` and ``target`` are Playwright locators. ``at`` picks the drop
    point inside the target — "center" (default), "top" or "bottom". Reordering
    *within* a lane needs it: the insertion index comes from clientY, so a drop
    on the lane's centre lands in the middle of it rather than at either end.
    """
    src = source.element_handle()
    tgt = target.element_handle()
    page.evaluate(
        """([src, tgt, at]) => {
            const dt = new DataTransfer();
            const r = tgt.getBoundingClientRect();
            const cx = r.left + r.width / 2;
            const cy = at === 'top' ? r.top + 4
                     : at === 'bottom' ? r.bottom - 4
                     : r.top + r.height / 2;
            const fire = (el, type) => el.dispatchEvent(new DragEvent(type, {
                bubbles: true, cancelable: true, dataTransfer: dt,
                clientX: cx, clientY: cy,
            }));
            fire(src, 'dragstart');
            fire(tgt, 'dragenter');
            fire(tgt, 'dragover');
            fire(tgt, 'drop');
            fire(src, 'dragend');
        }""",
        [src, tgt, at],
    )
