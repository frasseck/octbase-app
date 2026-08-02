"""Frontend tests for rich-text task descriptions, inline file uploads,
the attachment sidebar, the task preview overlay, and the image lightbox
(prompt 25 — rich-text tasks).

Test/class/method names deliberately include the words "attachment",
"description", or "preview" so they are selected by:
    pytest -k "attachment or description or preview"

These tests create their own throwaway tasks (rather than mutating the shared
seeded demo task) so they do not pollute other suites.
"""

import base64

import pytest
from conftest import DEMO_PROJECT_ID, SHORT, TIMEOUT, settle, unique

# A minimal valid 1x1 PNG.
_PNG_B64 = (
    "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAAC0lEQVR4nGNgYAAAAAQAAQ"
    "AAAAVfBQAAAAAASUVORK5CYII="
)


@pytest.fixture
def png_file(tmp_path):
    p = tmp_path / "screenshot.png"
    p.write_bytes(base64.b64decode(_PNG_B64))
    return str(p)


def _new_task(api, title, description=""):
    """Create a fresh task via the API and return its id."""
    t = api.post(
        f"/api/v1/projects/{DEMO_PROJECT_ID}/tasks",
        {"title": title, "description": description},
    )
    return t["id"]


def _open_panel(page, task_id):
    """Open the task panel for task_id and wait for the details editor."""
    page.evaluate("(id) => openTaskPanel(id)", task_id)
    page.wait_for_selector("#task-panel.open", timeout=TIMEOUT)
    page.wait_for_selector(".panel-tab", timeout=TIMEOUT)


class TestRichTextDescriptionEditor:
    def test_description_uses_contenteditable_editor(self, app, api):
        tid = _new_task(api, "RT editor task")
        _open_panel(app, tid)
        editor = app.query_selector("#pt-desc[contenteditable]")
        assert editor is not None, "rich-text contenteditable editor not found"
        assert editor.get_attribute("role") == "textbox"
        assert editor.get_attribute("aria-multiline") == "true"

    def test_description_toolbar_has_aria_labels(self, app, api):
        tid = _new_task(api, "RT toolbar task")
        _open_panel(app, tid)
        tools = app.query_selector_all(".rt-toolbar .rt-tool")
        assert len(tools) >= 6
        for tool in tools:
            assert (tool.get_attribute("aria-label") or "").strip() != ""

    def test_formatted_description_renders_without_script_execution(self, app, api):
        tid = _new_task(
            api,
            "RT xss task",
            "<p>safe <strong>bold</strong></p><script>window.__xss=1</script>",
        )
        _open_panel(app, tid)
        app.click("button:has-text('Preview')")
        app.wait_for_selector("#preview-overlay:not(.hidden)", timeout=TIMEOUT)
        overlay_html = app.query_selector(".preview-description").inner_html()
        assert "<script" not in overlay_html.lower()
        assert "bold" in overlay_html
        assert app.evaluate("() => window.__xss") in (None, 0)


class TestInlineAttachmentUpload:
    def test_uploading_file_shows_in_attachment_sidebar(self, app, api, png_file):
        tid = _new_task(api, "Attachment upload task")
        _open_panel(app, tid)
        app.wait_for_selector(".att-sidebar", timeout=TIMEOUT)
        before = len(app.query_selector_all("#att-sidebar-list .att-row"))
        # Image upload prompts (via the app's own modal overlay, not a native
        # confirm dialog) to insert inline — accept it.
        app.set_input_files("#rt-file-input", png_file)
        app.wait_for_selector(".toast-success", timeout=TIMEOUT)
        app.wait_for_selector("#modal-submit", timeout=SHORT)
        app.click("#modal-submit")
        settle(app)
        after = len(app.query_selector_all("#att-sidebar-list .att-row"))
        assert after >= before + 1, "uploaded attachment did not appear in the sidebar"
        # Uploaded (non-external) attachments are auth-gated: they render as a
        # viewAttachment button, not a plain <a href=".../content"> — a direct
        # link can't carry the Bearer token. (External-URL attachments keep an
        # <a href>.) Assert the upload is reachable via the view button.
        views = app.query_selector_all("#att-sidebar-list .att-name.att-view-btn")
        assert any(v.get_attribute("data-act") == "viewAttachment" for v in views), \
            "uploaded attachment did not render as an auth-gated view button"


class TestTaskPreviewAndLightbox:
    def _task_with_image(self, app, api, png_file, title):
        tid = _new_task(api, title)
        _open_panel(app, tid)
        app.set_input_files("#rt-file-input", png_file)
        app.wait_for_selector(".toast-success", timeout=TIMEOUT)
        app.wait_for_selector("#modal-submit", timeout=SHORT)
        app.click("#modal-submit")
        settle(app)
        return tid

    def test_preview_shows_image_attachment_inline(self, app, api, png_file):
        self._task_with_image(app, api, png_file, "Preview image task")
        app.click("button:has-text('Preview')")
        app.wait_for_selector("#preview-overlay:not(.hidden)", timeout=TIMEOUT)
        dialog = app.query_selector(".preview-dialog")
        assert dialog.get_attribute("role") == "dialog"
        assert dialog.get_attribute("aria-modal") == "true"
        thumbs = app.query_selector_all(".preview-thumb img")
        assert len(thumbs) >= 1, "expected at least one inline image thumbnail"
        assert thumbs[0].get_attribute("loading") == "lazy"

    def test_preview_lightbox_opens_and_closes_via_keyboard(self, app, api, png_file):
        self._task_with_image(app, api, png_file, "Preview lightbox task")
        app.click("button:has-text('Preview')")
        app.wait_for_selector("#preview-overlay:not(.hidden)", timeout=TIMEOUT)
        app.click(".preview-thumb")
        app.wait_for_selector("#lightbox:not(.hidden)", timeout=TIMEOUT)
        app.wait_for_selector(".lightbox-img", timeout=TIMEOUT)
        assert app.is_visible(".lightbox-img")
        app.keyboard.press("Escape")
        settle(app)
        assert not app.is_visible(".lightbox-img")

    def test_preview_overlay_does_not_change_hash(self, app, api):
        tid = _new_task(api, "Preview hash task", "<p>hi</p>")
        _open_panel(app, tid)
        hash_before = app.evaluate("() => location.hash")
        app.click("button:has-text('Preview')")
        app.wait_for_selector("#preview-overlay:not(.hidden)", timeout=TIMEOUT)
        hash_after = app.evaluate("() => location.hash")
        assert hash_before == hash_after, "preview overlay must not change the route/hash"

class TestAttachmentDelete:
    """Deleting an attachment must update the sidebar in place.

    It used to re-run renderTaskPanel(), which blanks the panel behind a
    spinner and refetches every panel endpoint — a flash the user reads as a
    page reload, and one that throws away an unsaved description draft.
    """

    def _task_with_upload(self, app, api, png_file, title):
        tid = _new_task(api, title)
        _open_panel(app, tid)
        app.wait_for_selector(".att-sidebar", timeout=TIMEOUT)
        app.set_input_files("#rt-file-input", png_file)
        app.wait_for_selector(".toast-success", timeout=TIMEOUT)
        # Decline the "insert inline?" prompt — this test is about the sidebar.
        app.wait_for_selector("#modal-submit", timeout=SHORT)
        app.click(".modal-footer .btn-secondary")
        settle(app)
        return tid

    def test_deleting_attachment_removes_the_row_without_rebuilding_the_panel(
        self, app, api, png_file
    ):
        self._task_with_upload(app, api, png_file, "Attachment delete task")
        before = len(app.query_selector_all("#att-sidebar-list .att-row"))
        assert before >= 1, "upload did not reach the sidebar"
        # Tag the description editor and the panel header: a full re-render
        # replaces those nodes, taking the marker with them.
        app.evaluate(
            "() => { document.querySelector('#pt-desc').dataset.probe = 'kept';"
            " document.querySelector('.panel-title-input').dataset.probe = 'kept'; }"
        )
        app.click("#att-sidebar-list .att-row [data-act='deleteAttachment']")
        app.wait_for_function(
            "(n) => document.querySelectorAll('#att-sidebar-list .att-row').length === n",
            arg=before - 1,
            timeout=TIMEOUT,
        )
        assert app.evaluate("() => document.querySelector('#pt-desc')?.dataset.probe") == "kept", \
            "the panel was rebuilt — attachment delete must update in place"
        assert app.evaluate(
            "() => document.querySelector('.panel-title-input')?.dataset.probe"
        ) == "kept", "the panel header was rebuilt on attachment delete"

    def test_deleting_attachment_keeps_an_unsaved_description_draft(
        self, app, api, png_file
    ):
        self._task_with_upload(app, api, png_file, "Attachment delete draft task")
        app.click("#pt-desc")
        app.keyboard.type("draft text")
        settle(app)
        app.click("#att-sidebar-list .att-row [data-act='deleteAttachment']")
        app.wait_for_function(
            "() => document.querySelectorAll('#att-sidebar-list .att-row').length === 0",
            timeout=TIMEOUT,
        )
        assert "draft text" in app.query_selector("#pt-desc").inner_text(), \
            "the unsaved description draft was lost when an attachment was deleted"

    def test_deleting_the_last_attachment_shows_the_empty_state(
        self, app, api, png_file
    ):
        self._task_with_upload(app, api, png_file, "Attachment delete empty task")
        app.click("#att-sidebar-list .att-row [data-act='deleteAttachment']")
        app.wait_for_selector("#att-sidebar-list .att-empty", timeout=TIMEOUT)
        assert len(app.query_selector_all("#att-sidebar-list .att-row")) == 0


class TestRepaintKeepsImagePreviews:
    """An edit elsewhere in the panel must not reload the attachment previews.

    Saving an effort estimate goes through applyTaskUpdate(panel: 'inplace'),
    which repaints the whole details tab. Rebuilding the attachment sidebar with
    it destroyed <img> elements that already held decoded pictures and recreated
    them empty, so every thumbnail blinked out and back — the previews looked
    like they were reloading on an edit that had nothing to do with them.
    """

    def _points_project_task(self, app, api, png_file):
        """A task with an image attachment, in a project that estimates in points."""
        project = api.post("/api/v1/projects", {"name": unique("Estimating project")})
        api.patch(
            f"/api/v1/projects/{project['id']}",
            {"estimationUnit": "POINTS", "version": project["version"]},
        )
        task = api.post(
            f"/api/v1/projects/{project['id']}/tasks", {"title": "Estimate repaint task"}
        )
        app.evaluate("(id) => { location.hash = `#/projects/${id}/board`; }", project["id"])
        app.wait_for_function(
            "(id) => window.S && S.project && S.project.id === id",
            arg=project["id"],
            timeout=TIMEOUT,
        )
        _open_panel(app, task["id"])
        app.set_input_files("#rt-file-input", png_file)
        app.wait_for_selector(".toast-success", timeout=TIMEOUT)
        app.wait_for_selector("#modal-submit", timeout=SHORT)
        app.click(".modal-footer .btn-secondary")  # decline the inline-insert prompt
        settle(app)
        # The thumbnail must be loaded before the edit, or "still loaded after"
        # proves nothing.
        app.wait_for_function(
            "() => { const i = document.querySelector('#att-sidebar-list img');"
            " return i && i.complete && i.naturalWidth > 0; }",
            timeout=TIMEOUT,
        )
        return task

    def test_saving_an_estimate_keeps_the_thumbnail_element_and_its_picture(
        self, app, api, png_file
    ):
        self._points_project_task(app, api, png_file)
        assert app.is_visible("#task-estimate"), "estimation is not switched on for this project"
        # Tag the live <img>: if the repaint rebuilds it, the marker goes with it
        # and the browser has to decode the picture again.
        app.evaluate(
            "() => { document.querySelector('#att-sidebar-list img').dataset.probe = 'kept'; }"
        )
        app.fill("#task-estimate", "5")
        app.press("#task-estimate", "Tab")
        app.wait_for_selector(".toast-success", timeout=TIMEOUT)
        settle(app)

        img = app.evaluate(
            "() => { const i = document.querySelector('#att-sidebar-list img');"
            " return i && {probe: i.dataset.probe || '', complete: i.complete,"
            " width: i.naturalWidth, blob: (i.src || '').startsWith('blob:')}; }"
        )
        assert img, "the attachment thumbnail disappeared on save"
        assert img["probe"] == "kept", \
            "the thumbnail was rebuilt by the repaint — previews reload on an unrelated edit"
        assert img["complete"] and img["width"] > 0 and img["blob"], \
            "the thumbnail lost its decoded picture"

    def test_the_estimate_itself_still_saves_and_repaints(self, app, api, png_file):
        """The preserved sidebar must not come at the cost of the repaint's job."""
        task = self._points_project_task(app, api, png_file)
        app.fill("#task-estimate", "8")
        app.press("#task-estimate", "Tab")
        app.wait_for_selector(".toast-success", timeout=TIMEOUT)
        settle(app)
        assert api.get(f"/api/v1/tasks/{task['id']}")["storyPoints"] == 8
        # The Fibonacci chip for the saved value is what the repaint is *for*.
        assert app.is_visible(".estimate-chip.is-active"), "the active chip did not update"
        assert app.eval_on_selector(".estimate-chip.is-active", "el => el.textContent.trim()") == "8"
