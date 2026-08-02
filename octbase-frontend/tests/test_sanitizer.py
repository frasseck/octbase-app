"""Unit-style tests for the shared rich-text sanitizer (octbase-shared/richtext.js).

sanitizeRichText is the client-side XSS boundary for task descriptions (the
server sanitizes independently on write; see octbase-api
internal/workmanagement/sanitize.go). These tests load the real app page so
the vendored DOMPurify and richtext.js execute exactly as in production, then
feed hostile vectors through page.evaluate.

Since 37b stage 3 richtext.js is an ES module in @octbase/shared rather than a
classic script, so `sanitizeRichText` no longer lands on `window` by accident:
`js/namespace.js` publishes it deliberately, with these tests named as the
reason. (DOMPurify is still a classic script and still a global.)

No login/API interaction is needed — the sanitizer is a pure function — but
the page is still loaded via desktop_url() like the rest of the suite.
"""

import pytest

from conftest import desktop_url, TIMEOUT


@pytest.fixture
def rt(page):
    """Page with the sanitizer loaded; returns a callable vector -> output."""
    page.goto(desktop_url())
    page.wait_for_function(
        "() => typeof window.sanitizeRichText === 'function'",
        timeout=TIMEOUT,
    )

    def run(html: str) -> str:
        return page.evaluate("v => sanitizeRichText(v)", html)

    return run


class TestScriptInjection:
    def test_script_tag_dropped_with_content(self, rt):
        assert rt("<script>alert(1)</script>") == ""

    def test_style_tag_dropped_with_content(self, rt):
        assert rt("<style>body{background:url(x)}</style>") == ""

    def test_event_handler_stripped(self, rt):
        assert rt('<p onclick="alert(1)">hi</p>') == "<p>hi</p>"

    def test_img_onerror_vector_removed(self, rt):
        # src fails the attachment-endpoint check → attribute stripped → the
        # now-srcless <img> is removed entirely.
        out = rt('<img src="x" onerror="alert(1)">')
        assert "img" not in out and "onerror" not in out

    def test_svg_subtree_dropped(self, rt):
        out = rt('<svg><style><img src="x" onerror="alert(1)"></style></svg>')
        assert "img" not in out and "svg" not in out and "onerror" not in out

    def test_iframe_dropped(self, rt):
        assert rt('<iframe src="https://evil.example"></iframe>') == ""


class TestHrefPolicy:
    def test_javascript_href_stripped(self, rt):
        out = rt('<a href="javascript:alert(1)">x</a>')
        assert "javascript" not in out and ">x</a>" in out

    def test_data_href_stripped(self, rt):
        assert "href" not in rt('<a href="data:text/html,<script>1</script>">x</a>')

    def test_https_href_kept_and_hardened(self, rt):
        out = rt('<a href="https://example.com/a?b=c">x</a>')
        assert 'href="https://example.com/a?b=c"' in out
        assert 'rel="noopener noreferrer"' in out and 'target="_blank"' in out

    def test_mailto_and_relative_kept(self, rt):
        assert 'href="mailto:a@b.ch"' in rt('<a href="mailto:a@b.ch">m</a>')
        assert 'href="/docs/page"' in rt('<a href="/docs/page">r</a>')

    def test_control_chars_hiding_scheme_rejected(self, rt):
        # Mirrors the server: control characters may not obscure a scheme.
        assert "href" not in rt('<a href="java\tscript:alert(1)">x</a>')

    def test_href_only_valid_on_anchor(self, rt):
        # Per-tag scoping: href is meaningless (and stripped) on non-<a> tags.
        assert "href" not in rt('<p href="https://example.com">t</p>')


class TestImageSrcPolicy:
    ATTACHMENT = "/api/v1/tasks/00000000-0000-0000-0000-000000000201/attachments/1/content"

    def test_own_attachment_endpoint_kept_and_hardened(self, rt):
        out = rt(f'<img src="{self.ATTACHMENT}">')
        assert f'src="{self.ATTACHMENT}"' in out
        assert 'loading="lazy"' in out and 'alt=""' in out

    def test_external_src_removes_image(self, rt):
        assert "img" not in rt('<img src="https://evil.example/pixel.gif">')

    def test_protocol_relative_src_removes_image(self, rt):
        assert "img" not in rt('<img src="//evil.example/x">')

    def test_src_only_valid_on_img(self, rt):
        out = rt(f'<a src="{self.ATTACHMENT}" href="https://example.com">x</a>')
        assert "src" not in out and 'href="https://example.com"' in out


class TestAllowlistShape:
    def test_unknown_tags_unwrapped_text_kept(self, rt):
        assert rt("<div><span>keep me</span></div>") == "keep me"

    def test_allowed_formatting_survives(self, rt):
        src = "<h3>t</h3><ul><li><strong>a</strong> <em>b</em></li></ul><pre><code>c</code></pre>"
        assert rt(src) == src

    def test_data_and_aria_attributes_stripped(self, rt):
        assert rt('<p data-x="1" aria-label="y">t</p>') == "<p>t</p>"

    def test_comments_removed(self, rt):
        assert rt("<p>a<!-- secret --></p>") == "<p>a</p>"

    def test_empty_input(self, rt):
        assert rt("") == ""


class TestDisplayHelpers:
    def test_legacy_plain_text_escaped_with_linebreaks(self, rt, page):
        # "5 < 10" must not trip the looksLikeHTML heuristic (no tag name
        # follows the "<"), so it takes the escape-and-<br> legacy path.
        out = page.evaluate("() => renderDescriptionHTML('5 < 10\\nnext')")
        assert out == "5 &lt; 10<br>next"

    def test_html_descriptions_routed_through_sanitizer(self, page, rt):
        out = page.evaluate(
            "() => renderDescriptionHTML('<p onclick=\"x\">ok</p>')"
        )
        assert out == "<p>ok</p>"


class TestCustomPriorityEscaping:
    """Admin-defined custom priority names must never reach a sink unescaped.

    priorityMeta() returns the raw name as the label for a custom priority (it
    has no PRIORITY_META entry), and that label lands in <option> text, option
    values, badge text and the priority dot's title=. The server's
    ValidPriorityName regex ([A-Z][A-Z0-9_]{0,19}) makes a hostile name
    impossible today, so this is defense in depth: the guarantee must not
    silently depend on a regex in another module that check-innerhtml.mjs
    cannot see. Security assessment 2026-07-14, finding L13.
    """

    XSS = '<img src=x onerror="window.__prioPwned=1">'

    def test_hostile_custom_priority_name_is_inert(self, demo_board):
        import json

        from conftest import DEMO_PROJECT_ID, settle

        page = demo_board

        # Simulate a future backend that relaxed ValidPriorityName by stubbing
        # the priorities response, so the real render path receives the name.
        page.route(
            "**/task-priorities",
            lambda route: route.fulfill(
                status=200,
                content_type="application/json",
                body=json.dumps([{
                    "id": "00000000-0000-0000-0000-0000000009a1",
                    "projectId": DEMO_PROJECT_ID,
                    "name": self.XSS,
                    "position": 0,
                }]),
            ),
        )
        # loadProject() early-returns for the already-loaded project; drop the
        # cache so the stubbed request is actually made. ?priority= keeps the
        # toolbar rendered even though the backlog list is empty.
        page.evaluate("() => { S.project = null; }")
        page.evaluate(
            f"() => router.go('/projects/{DEMO_PROJECT_ID}/backlog?priority=HIGH')"
        )
        settle(page)
        page.wait_for_selector("#filter-priority", timeout=15000)

        opts = page.eval_on_selector_all(
            "#filter-priority option", "els => els.map(e => [e.value, e.textContent])"
        )
        hostile = [o for o in opts if "img" in o[1]]

        assert hostile, f"stubbed custom priority never rendered; got {opts}"
        # Escaped: the name survives as literal text, in both value and label...
        assert hostile[0] == [self.XSS, self.XSS]
        # ...and no element was created from it, so onerror never runs.
        assert page.evaluate(
            "() => document.querySelectorAll('#filter-priority img').length"
        ) == 0
        assert page.evaluate("() => !!window.__prioPwned") is False
