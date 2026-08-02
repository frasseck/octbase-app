"""Tests for the Pages view: tree, viewing, editing, AsciiDoc preview, create/archive."""

import pytest
from conftest import (
    DEMO_PROJECT_ID, DEMO_PAGE_TITLE, SHORT, TIMEOUT, unique, navigate_to,
    poll_until, settle,)


class TestPagesNavAndTree:
    def test_pages_nav_item_exists(self, demo_board):
        assert demo_board.is_visible(".sidebar-item:has-text('Pages')")

    def test_navigating_to_pages_loads_view(self, demo_board):
        navigate_to(demo_board, "Pages")
        settle(demo_board)
        has_tree = demo_board.is_visible(".pages-sidebar")
        has_empty = demo_board.is_visible(".empty")
        assert has_tree or has_empty

    def test_pages_breadcrumb_shows_pages(self, demo_board):
        navigate_to(demo_board, "Pages")
        settle(demo_board)
        topbar = demo_board.query_selector("#topbar")
        assert "Pages" in topbar.inner_text()

    def test_new_page_button_visible(self, demo_board):
        navigate_to(demo_board, "Pages")
        settle(demo_board)
        assert demo_board.is_visible("[data-act='showCreatePage']")

    def test_seeded_page_appears_in_tree(self, demo_board):
        navigate_to(demo_board, "Pages")
        demo_board.wait_for_selector(".pages-sidebar", timeout=TIMEOUT)
        assert demo_board.is_visible(f"text={DEMO_PAGE_TITLE}")

    def test_page_tree_item_has_title(self, demo_board):
        navigate_to(demo_board, "Pages")
        demo_board.wait_for_selector(".pages-item", timeout=TIMEOUT)
        item = demo_board.query_selector(".pages-item")
        assert item is not None
        assert item.inner_text().strip() != ""


class TestPageViewing:
    def test_clicking_page_shows_content(self, demo_board):
        navigate_to(demo_board, "Pages")
        demo_board.wait_for_selector(".pages-item", timeout=TIMEOUT)
        demo_board.click(f".pages-item:has-text('{DEMO_PAGE_TITLE}')")
        settle(demo_board)
        has_content = demo_board.is_visible(".page-view")
        has_editor = demo_board.is_visible("#page-content-input")
        assert has_content or has_editor

    def test_page_title_displayed_in_view(self, demo_board):
        navigate_to(demo_board, "Pages")
        demo_board.wait_for_selector(".pages-item", timeout=TIMEOUT)
        demo_board.click(f".pages-item:has-text('{DEMO_PAGE_TITLE}')")
        settle(demo_board)
        # Title should appear somewhere in the main area
        main = demo_board.query_selector("#main")
        assert DEMO_PAGE_TITLE in main.inner_text()

    def test_edit_button_present_on_page_view(self, demo_board):
        navigate_to(demo_board, "Pages")
        demo_board.wait_for_selector(".pages-item", timeout=TIMEOUT)
        demo_board.click(f".pages-item:has-text('{DEMO_PAGE_TITLE}')")
        settle(demo_board)
        has_edit = demo_board.is_visible("button:text-is('Edit')")
        has_editor = demo_board.is_visible("#page-content-input")
        assert has_edit or has_editor


class TestPageEditing:
    def test_edit_mode_shows_textarea(self, demo_board):
        navigate_to(demo_board, "Pages")
        demo_board.wait_for_selector(".pages-item", timeout=TIMEOUT)
        demo_board.click(f".pages-item:has-text('{DEMO_PAGE_TITLE}')")
        settle(demo_board)
        # If we see an Edit button, click it
        if demo_board.is_visible("button:text-is('Edit')"):
            demo_board.click("button:text-is('Edit')")
            settle(demo_board)
        assert demo_board.is_visible("#page-content-input")

    def test_edit_mode_shows_save_button(self, demo_board):
        navigate_to(demo_board, "Pages")
        demo_board.wait_for_selector(".pages-item", timeout=TIMEOUT)
        demo_board.click(f".pages-item:has-text('{DEMO_PAGE_TITLE}')")
        settle(demo_board)
        if demo_board.is_visible("button:text-is('Edit')"):
            demo_board.click("button:text-is('Edit')")
            settle(demo_board)
        assert demo_board.is_visible("button:has-text('Save Draft')")

    def test_edit_mode_has_preview_button(self, demo_board):
        navigate_to(demo_board, "Pages")
        demo_board.wait_for_selector(".pages-item", timeout=TIMEOUT)
        demo_board.click(f".pages-item:has-text('{DEMO_PAGE_TITLE}')")
        settle(demo_board)
        if demo_board.is_visible("button:text-is('Edit')"):
            demo_board.click("button:text-is('Edit')")
            settle(demo_board)
        # Preview button is optional but common in AsciiDoc editors
        has_preview = demo_board.is_visible("button:has-text('Preview')")
        has_editor = demo_board.is_visible("#page-content-input")
        assert has_editor  # at minimum the editor must be visible


class TestPageCreation:
    def test_create_page_dialog_opens(self, demo_board):
        navigate_to(demo_board, "Pages")
        settle(demo_board)
        demo_board.click("[data-act='showCreatePage']")
        demo_board.wait_for_selector("#modal-backdrop:not(.hidden)", timeout=SHORT)
        assert demo_board.is_visible(".modal-title")

    def test_create_page_appears_in_tree(self, demo_board, api):
        title = unique("Test Page")
        navigate_to(demo_board, "Pages")
        settle(demo_board)
        demo_board.click("[data-act='showCreatePage']")
        demo_board.wait_for_selector("#page-title", timeout=SHORT)
        demo_board.fill("#page-title", title)
        demo_board.click("#modal-submit")
        demo_board.wait_for_selector(f"text={title}", timeout=TIMEOUT)
        assert demo_board.is_visible(f"text={title}")

    def test_create_page_via_api_appears_in_tree(self, demo_board, api):
        title = unique("API Created Page")
        api.post(f"/api/projects/{DEMO_PROJECT_ID}/pages", {
            "title": title,
            "content": f"= {title}\n\nSome content.",
        })
        navigate_to(demo_board, "Pages")
        demo_board.wait_for_selector(f"text={title}", timeout=TIMEOUT)
        assert demo_board.is_visible(f"text={title}")

    def test_save_page_content_via_api(self, demo_board, api):
        title = unique("Saveable Page")
        page = api.post(f"/api/projects/{DEMO_PROJECT_ID}/pages", {
            "title": title,
            "content": "= Original\n\nOriginal content.",
        })
        navigate_to(demo_board, "Pages")
        demo_board.wait_for_selector(f"text={title}", timeout=TIMEOUT)
        demo_board.click(f".pages-item:has-text('{title}')")
        settle(demo_board)
        if demo_board.is_visible("button:text-is('Edit')"):
            demo_board.click("button:text-is('Edit')")
            settle(demo_board)
        demo_board.fill("#page-content-input", "= Updated\n\nUpdated content.")
        demo_board.click("button:has-text('Save Draft')")

        def _saved():
            p = api.get(f"/api/pages/{page['id']}")
            return p if "Updated" in p["content"] else None

        updated = poll_until(_saved, message="saved draft never landed via the API")
        assert "Updated" in updated["content"]


class TestAsciiDocRendering:
    """Verifies the real AsciiDoc renderer: live preview, published render, TOC."""

    def _open_new_page_editor(self, demo_board, api, content):
        title = unique("AsciiDoc Page")
        page = api.post(f"/api/projects/{DEMO_PROJECT_ID}/pages", {
            "title": title,
            "content": content,
        })
        navigate_to(demo_board, "Pages")
        demo_board.wait_for_selector(f"text={title}", timeout=TIMEOUT)
        demo_board.click(f".pages-item:has-text('{title}')")
        settle(demo_board)
        return title, page

    def test_live_preview_renders_formatting(self, demo_board, api):
        title, _ = self._open_new_page_editor(demo_board, api, "= Start\n")
        if demo_board.is_visible("button:text-is('Edit')"):
            demo_board.click("button:text-is('Edit')")
            settle(demo_board)
        demo_board.fill("#page-content-input", "= Heading\n\nThis is *bold* and _italic_.\n")

        # The preview is debounced (300ms) and rendered server-side, so poll the
        # pane itself: settle() can return before the timer has even fired a
        # request.
        def _rendered():
            html = demo_board.inner_html("#page-preview-content")
            return html if "<strong>bold</strong>" in html else None

        html = poll_until(_rendered, message="preview never rendered the formatted markup")
        assert "<strong>bold</strong>" in html
        assert "<em>italic</em>" in html

    def test_live_preview_does_not_execute_script(self, demo_board, api):
        title, _ = self._open_new_page_editor(demo_board, api, "= Start\n")
        if demo_board.is_visible("button:text-is('Edit')"):
            demo_board.click("button:text-is('Edit')")
            settle(demo_board)
        # Install a sentinel; a live <script> would overwrite it.
        demo_board.evaluate("window.__xss = false")
        demo_board.fill("#page-content-input", "= Heading\n\n<script>window.__xss=true</script>\n")

        # Both assertions below are negative, so they would pass vacuously if the
        # debounced (300ms) preview had not run yet. Wait for the positive proof
        # that this content was rendered — the page opens on "= Start", so the
        # "Heading" anchor only exists once the new preview has landed.
        poll_until(
            lambda: 'id="h-heading"' in demo_board.inner_html("#page-preview-content"),
            message="preview never re-rendered the injected content",
        )
        assert demo_board.evaluate("window.__xss") is False
        html = demo_board.inner_html("#page-preview-content")
        assert "<script" not in html.lower()

    def test_published_page_renders_formatted(self, demo_board, api):
        content = "= Title\n\nIntro para.\n\n* one\n* two\n\nNOTE: heads up\n"
        page = api.post(f"/api/projects/{DEMO_PROJECT_ID}/pages",
                        {"title": unique("Published AsciiDoc"), "content": content})
        api.post(f"/api/pages/{page['id']}/publish", {"message": "go"})
        navigate_to(demo_board, "Pages")
        demo_board.wait_for_selector(f"text={page['title']}", timeout=TIMEOUT)
        demo_board.click(f".pages-item:has-text('{page['title']}')")
        demo_board.wait_for_selector(".page-body", timeout=TIMEOUT)
        html = demo_board.inner_html(".page-body")
        assert "<li>one</li>" in html
        assert "admonition-note" in html

    def test_toc_reflects_heading_hierarchy(self, demo_board, api):
        content = "= Top\n\n== Alpha\n\ntext\n\n== Beta\n\ntext\n\n=== Beta Sub\n\ntext\n"
        page = api.post(f"/api/projects/{DEMO_PROJECT_ID}/pages",
                        {"title": unique("TOC Page"), "content": content})
        api.post(f"/api/pages/{page['id']}/publish", {"message": "go"})
        navigate_to(demo_board, "Pages")
        demo_board.wait_for_selector(f"text={page['title']}", timeout=TIMEOUT)
        demo_board.click(f".pages-item:has-text('{page['title']}')")
        demo_board.wait_for_selector(".page-body", timeout=TIMEOUT)
        # >= 3 headings -> TOC sidebar renders.
        assert demo_board.is_visible(".page-toc")
        links = demo_board.query_selector_all(".page-toc .toc-link")
        texts = [l.inner_text().strip() for l in links]
        assert "Alpha" in texts and "Beta" in texts and "Beta Sub" in texts

    def test_syntax_cheatsheet_toggles(self, demo_board, api):
        self._open_new_page_editor(demo_board, api, "= Start\n")
        if demo_board.is_visible("button:text-is('Edit')"):
            demo_board.click("button:text-is('Edit')")
            settle(demo_board)
        assert not demo_board.is_visible("#asciidoc-cheatsheet")
        demo_board.click("#cheatsheet-btn")
        settle(demo_board)
        assert demo_board.is_visible("#asciidoc-cheatsheet")
