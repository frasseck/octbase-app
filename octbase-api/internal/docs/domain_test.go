package docs

import (
	"strings"
	"testing"
)

func TestRenderAsciiDoc_Headings(t *testing.T) {
	content := "= Title\n== Section\n=== Subsection\n====== Deep\n"
	html := RenderAsciiDoc(content)
	for _, want := range []string{"<h1", "Title</h1>", "<h2", "Section</h2>", "<h3", "Subsection</h3>", "<h6", "Deep</h6>"} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q in: %s", want, html)
		}
	}
}

func TestRenderAsciiDoc_HeadingAnchors(t *testing.T) {
	html := RenderAsciiDoc("== Getting Started")
	if !strings.Contains(html, `id="h-getting-started"`) {
		t.Errorf("expected heading anchor id, got: %s", html)
	}
}

func TestRenderAsciiDoc_Paragraph(t *testing.T) {
	html := RenderAsciiDoc("Hello world")
	if !strings.Contains(html, "<p>") || !strings.Contains(html, "Hello world") {
		t.Errorf("expected paragraph, got: %s", html)
	}
}

func TestRenderAsciiDoc_Bold(t *testing.T) {
	html := RenderAsciiDoc("This is *bold* text")
	if !strings.Contains(html, "<strong>bold</strong>") {
		t.Errorf("missing bold tag in: %s", html)
	}
}

func TestRenderAsciiDoc_DoubleAsteriskBold(t *testing.T) {
	// Behavior change vs. the old renderer: the old code special-cased the
	// Markdown "**text**" form. Real AsciiDoc treats "**" as the unconstrained
	// bold marker, so it now renders as clean <strong> with no stray asterisks.
	html := RenderAsciiDoc("This is **markdown** text")
	if !strings.Contains(html, "<strong>markdown</strong>") {
		t.Errorf("expected clean bold for **markdown**, got: %s", html)
	}
	if strings.Contains(html, "*markdown*") || strings.Contains(html, "*<strong>") {
		t.Errorf("stray asterisks should not survive, got: %s", html)
	}
}

func TestRenderAsciiDoc_Italic(t *testing.T) {
	html := RenderAsciiDoc("This is _italic_ text")
	if !strings.Contains(html, "<em>italic</em>") {
		t.Errorf("missing italic tag in: %s", html)
	}
}

func TestRenderAsciiDoc_Monospace(t *testing.T) {
	html := RenderAsciiDoc("Use `code` here")
	if !strings.Contains(html, "<code>code</code>") {
		t.Errorf("missing monospace tag in: %s", html)
	}
}

func TestRenderAsciiDoc_BulletList(t *testing.T) {
	html := RenderAsciiDoc("* item one\n* item two\n")
	if !strings.Contains(html, "<ul>") || !strings.Contains(html, "<li>item one</li>") || !strings.Contains(html, "<li>item two</li>") {
		t.Errorf("bad unordered list: %s", html)
	}
}

func TestRenderAsciiDoc_DashList(t *testing.T) {
	html := RenderAsciiDoc("- a\n- b\n")
	if !strings.Contains(html, "<ul>") || !strings.Contains(html, "<li>a</li>") {
		t.Errorf("bad dash list: %s", html)
	}
}

func TestRenderAsciiDoc_OrderedList(t *testing.T) {
	html := RenderAsciiDoc(". first\n. second\n")
	if !strings.Contains(html, "<ol>") || !strings.Contains(html, "<li>first</li>") || !strings.Contains(html, "<li>second</li>") {
		t.Errorf("bad ordered list: %s", html)
	}
}

func TestRenderAsciiDoc_NestedList(t *testing.T) {
	html := RenderAsciiDoc("* top\n** nested\n* top2\n")
	// Expect a nested <ul> inside the list.
	if strings.Count(html, "<ul>") < 2 {
		t.Errorf("expected nested list, got: %s", html)
	}
	if !strings.Contains(html, "<li>nested</li>") {
		t.Errorf("missing nested item: %s", html)
	}
}

func TestRenderAsciiDoc_Link(t *testing.T) {
	html := RenderAsciiDoc("See https://example.com[Example] now")
	if !strings.Contains(html, `<a href="https://example.com" class="page-link">Example</a>`) {
		t.Errorf("bad link: %s", html)
	}
}

func TestRenderAsciiDoc_LinkMacro(t *testing.T) {
	html := RenderAsciiDoc("link:https://example.com/x[X]")
	if !strings.Contains(html, `href="https://example.com/x"`) || !strings.Contains(html, ">X</a>") {
		t.Errorf("bad link macro: %s", html)
	}
}

func TestRenderAsciiDoc_BareURL(t *testing.T) {
	html := RenderAsciiDoc("visit https://example.org here")
	if !strings.Contains(html, `<a href="https://example.org"`) {
		t.Errorf("bare url not linkified: %s", html)
	}
}

func TestRenderAsciiDoc_CodeBlock(t *testing.T) {
	html := RenderAsciiDoc("----\nfn main() {}\n----\n")
	if !strings.Contains(html, "<pre><code>") || !strings.Contains(html, "fn main() {}") {
		t.Errorf("bad code block: %s", html)
	}
}

func TestRenderAsciiDoc_SourceBlock(t *testing.T) {
	html := RenderAsciiDoc("[source,go]\n----\nx := 1\n----\n")
	if !strings.Contains(html, `class="language-go"`) || !strings.Contains(html, "x := 1") {
		t.Errorf("bad source block: %s", html)
	}
}

func TestRenderAsciiDoc_CodeBlockNotRendered(t *testing.T) {
	// Markup inside a code block must be literal, not interpreted.
	html := RenderAsciiDoc("----\n*not bold*\n----\n")
	if strings.Contains(html, "<strong>not bold</strong>") {
		t.Errorf("code block content should be literal, got: %s", html)
	}
}

func TestRenderAsciiDoc_BlockQuote(t *testing.T) {
	html := RenderAsciiDoc("____\nTo be or not to be.\n____\n")
	if !strings.Contains(html, "<blockquote>") || !strings.Contains(html, "To be or not to be.") {
		t.Errorf("bad blockquote: %s", html)
	}
}

func TestRenderAsciiDoc_Admonitions(t *testing.T) {
	for _, name := range []string{"NOTE", "TIP", "WARNING", "IMPORTANT", "CAUTION"} {
		html := RenderAsciiDoc(name + ": be careful")
		if !strings.Contains(html, "admonition-"+strings.ToLower(name)) {
			t.Errorf("missing admonition %s: %s", name, html)
		}
		if !strings.Contains(html, "be careful") {
			t.Errorf("missing admonition text for %s: %s", name, html)
		}
	}
}

func TestRenderAsciiDoc_Table(t *testing.T) {
	content := "|===\n| H1 | H2\n\n| a | b\n| c | d\n|===\n"
	html := RenderAsciiDoc(content)
	if !strings.Contains(html, "<table") || !strings.Contains(html, "<thead>") || !strings.Contains(html, "<th") {
		t.Errorf("bad table structure: %s", html)
	}
	if !strings.Contains(html, "H1") || !strings.Contains(html, "<td>a</td>") {
		t.Errorf("table cells wrong: %s", html)
	}
}

func TestRenderAsciiDoc_BlockImageRelative(t *testing.T) {
	html := RenderAsciiDoc("image::/static/pic.png[A picture]")
	if !strings.Contains(html, `<img src="/static/pic.png" alt="A picture">`) {
		t.Errorf("relative image not rendered: %s", html)
	}
}

func TestRenderAsciiDoc_BlockImageExternalBlocked(t *testing.T) {
	html := RenderAsciiDoc("image::https://evil.example/tracker.png[x]")
	if strings.Contains(html, "evil.example") {
		t.Errorf("external image src should be stripped, got: %s", html)
	}
}

func TestRenderAsciiDoc_TaskReferenceRendered(t *testing.T) {
	uuid := "00000000-0000-0000-0000-000000000201"
	html := RenderAsciiDoc("See TASK-" + uuid + " please")
	if !strings.Contains(html, `class="task-ref"`) || !strings.Contains(html, "TASK-"+uuid) {
		t.Errorf("task ref not rendered as anchor: %s", html)
	}
}

func TestRenderAsciiDoc_EmptyContent(t *testing.T) {
	html := RenderAsciiDoc("")
	if !strings.Contains(html, "asciidoc-content") {
		t.Errorf("wrapper div missing in: %s", html)
	}
}

func TestRenderAsciiDoc_WrapperDiv(t *testing.T) {
	html := RenderAsciiDoc("content")
	if !strings.HasPrefix(html, `<div class="asciidoc-content">`) {
		t.Errorf("should start with wrapper div, got: %s", html)
	}
	if !strings.HasSuffix(html, "</div>") {
		t.Errorf("should end with </div>, got: %s", html)
	}
}

// ---- XSS / sanitization ----

func TestRenderAsciiDoc_ScriptNotExecutable(t *testing.T) {
	// Literal HTML in AsciiDoc source is rendered as visible (escaped) text, so
	// no executable <script> element is ever emitted.
	html := RenderAsciiDoc("<script>alert(1)</script>")
	if strings.Contains(html, "<script>") || strings.Contains(html, "<script ") {
		t.Errorf("no live <script> element may be emitted, got: %s", html)
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Errorf("script tag should be escaped to text, got: %s", html)
	}
}

func TestRenderAsciiDoc_OnErrorStripped(t *testing.T) {
	html := RenderAsciiDoc(`image::x.png[" onerror="alert(1)]`)
	if strings.Contains(html, "onerror") {
		t.Errorf("onerror must be stripped, got: %s", html)
	}
}

func TestRenderAsciiDoc_JavascriptLinkNotHref(t *testing.T) {
	// A javascript: link macro is not a supported scheme, so it never becomes a
	// real href; it remains inert escaped text.
	html := RenderAsciiDoc("link:javascript:alert(1)[click]")
	if strings.Contains(html, `href="javascript:`) {
		t.Errorf("javascript: must never appear in an href, got: %s", html)
	}
}

func TestRenderAsciiDoc_DataURIImageStripped(t *testing.T) {
	html := RenderAsciiDoc("image::data:text/html;base64,PHNjcmlwdD4=[x]")
	if strings.Contains(html, "data:") {
		t.Errorf("data: image src must be stripped, got: %s", html)
	}
}

func TestRenderAsciiDoc_PassthroughNotRaw(t *testing.T) {
	// AsciiDoc passthrough is deliberately unsupported; raw HTML must be escaped.
	html := RenderAsciiDoc("+++<b>raw</b>+++")
	if strings.Contains(html, "<b>raw</b>") {
		t.Errorf("passthrough raw HTML must not survive, got: %s", html)
	}
}

func TestRenderAsciiDoc_IframeNotLive(t *testing.T) {
	html := RenderAsciiDoc(`<iframe src="https://evil.example"></iframe>`)
	if strings.Contains(html, "<iframe") {
		t.Errorf("no live <iframe> element may be emitted, got: %s", html)
	}
}

// ---- task references ----

func TestExtractTaskReferences_FindsUUIDs(t *testing.T) {
	content := "See TASK-00000000-0000-0000-0000-000000000201 for details.\n" +
		"Also TASK-00000000-0000-0000-0000-000000000202 is related."
	ids := ExtractTaskReferences(content)
	if len(ids) != 2 {
		t.Fatalf("expected 2 refs, got %d: %v", len(ids), ids)
	}
	if ids[0] != "00000000-0000-0000-0000-000000000201" {
		t.Errorf("ids[0] = %q", ids[0])
	}
	if ids[1] != "00000000-0000-0000-0000-000000000202" {
		t.Errorf("ids[1] = %q", ids[1])
	}
}

func TestExtractTaskReferences_Deduplicates(t *testing.T) {
	uuid := "00000000-0000-0000-0000-000000000201"
	content := "TASK-" + uuid + " and again TASK-" + uuid
	ids := ExtractTaskReferences(content)
	if len(ids) != 1 {
		t.Errorf("expected 1 unique ref, got %d", len(ids))
	}
}

func TestExtractTaskReferences_NoMatch(t *testing.T) {
	ids := ExtractTaskReferences("no task references here")
	if len(ids) != 0 {
		t.Errorf("expected 0 refs, got %d", len(ids))
	}
}

func TestPageStatusConstants(t *testing.T) {
	if StatusDraft != "DRAFT" || StatusPublished != "PUBLISHED" || StatusArchived != "ARCHIVED" {
		t.Errorf("status constants wrong")
	}
}
