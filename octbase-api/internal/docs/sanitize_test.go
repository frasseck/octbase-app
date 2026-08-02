package docs

import (
	"strings"
	"testing"
)

func TestSanitizePageHTML_KeepsAllowedTags(t *testing.T) {
	in := `<h2 id="h-x">Hi</h2><p>a <strong>b</strong> <em>c</em></p>`
	out := sanitizePageHTML(in)
	for _, want := range []string{`<h2 id="h-x">`, "<strong>b</strong>", "<em>c</em>"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}
}

func TestSanitizePageHTML_DropsScript(t *testing.T) {
	out := sanitizePageHTML(`<p>ok</p><script>alert(1)</script>`)
	if strings.Contains(out, "<script") || strings.Contains(out, "alert(1)") {
		t.Errorf("script not dropped: %s", out)
	}
	if !strings.Contains(out, "<p>ok</p>") {
		t.Errorf("good content lost: %s", out)
	}
}

func TestSanitizePageHTML_DropsEventHandlers(t *testing.T) {
	out := sanitizePageHTML(`<a href="https://x.test" onclick="evil()">x</a>`)
	if strings.Contains(out, "onclick") {
		t.Errorf("event handler not dropped: %s", out)
	}
	if !strings.Contains(out, `href="https://x.test"`) {
		t.Errorf("safe href lost: %s", out)
	}
}

func TestSanitizePageHTML_DropsJavascriptHref(t *testing.T) {
	out := sanitizePageHTML(`<a href="javascript:alert(1)">x</a>`)
	if strings.Contains(out, "javascript:") {
		t.Errorf("javascript href not dropped: %s", out)
	}
}

func TestSanitizePageHTML_FragmentHrefAllowed(t *testing.T) {
	out := sanitizePageHTML(`<a href="#/tasks/abc">x</a>`)
	if !strings.Contains(out, `href="#/tasks/abc"`) {
		t.Errorf("fragment href should be allowed: %s", out)
	}
}

func TestSanitizePageHTML_DropsExternalImage(t *testing.T) {
	out := sanitizePageHTML(`<img src="https://evil.test/p.png" alt="x">`)
	if strings.Contains(out, "evil.test") {
		t.Errorf("external image src not dropped: %s", out)
	}
}

func TestSanitizePageHTML_AllowsRelativeImage(t *testing.T) {
	out := sanitizePageHTML(`<img src="/static/p.png" alt="x">`)
	if !strings.Contains(out, `src="/static/p.png"`) {
		t.Errorf("relative image dropped: %s", out)
	}
}

func TestSanitizePageHTML_DropsDataImage(t *testing.T) {
	out := sanitizePageHTML(`<img src="data:text/html,<script>" alt="x">`)
	if strings.Contains(out, "data:") {
		t.Errorf("data: image src not dropped: %s", out)
	}
}

func TestSanitizePageHTML_DropsStyleAttr(t *testing.T) {
	out := sanitizePageHTML(`<p style="position:fixed">x</p>`)
	if strings.Contains(out, "style") {
		t.Errorf("style attribute not dropped: %s", out)
	}
}

func TestSanitizePageHTML_IdempotentEntities(t *testing.T) {
	// Already-escaped text must not be double-escaped.
	in := `<p>a &amp; b &lt;c&gt;</p>`
	out := sanitizePageHTML(in)
	if strings.Contains(out, "&amp;amp;") || strings.Contains(out, "&amp;lt;") {
		t.Errorf("entities double-escaped: %s", out)
	}
	if !strings.Contains(out, "&amp; b") {
		t.Errorf("entity not preserved: %s", out)
	}
}

func TestSanitizePageHTML_DropsBadClass(t *testing.T) {
	out := sanitizePageHTML(`<div class="ok&quot; onmouseover=alert(1) x">y</div>`)
	if strings.Contains(out, "onmouseover") {
		t.Errorf("class breakout not blocked: %s", out)
	}
}

// Externally sourced AsciiDoc (single-asterisk bold, "=" headings) may carry
// leftover raw HTML; rendering it must be safe and formatted.
func TestRenderAsciiDoc_ExternalContentSanitized(t *testing.T) {
	content := "// Imported page\n\n= Imported Page\n\nThis is *important* text.\n\n* one\n* two\n\n<script>alert('xss')</script>\n"
	html := RenderAsciiDoc(content)
	if !strings.Contains(html, "Imported Page</h1>") {
		t.Errorf("heading not rendered: %s", html)
	}
	if !strings.Contains(html, "<strong>important</strong>") {
		t.Errorf("bold not rendered: %s", html)
	}
	if !strings.Contains(html, "<li>one</li>") {
		t.Errorf("list not rendered: %s", html)
	}
	if strings.Contains(html, "<script") {
		t.Errorf("imported script must not be live: %s", html)
	}
}

// TestSanitizePageHTML_EntitiesCannotSmuggleMarkup is the page-side mirror of
// TestSanitizeDescriptionHTML_EntitiesCannotSmuggleMarkup in
// internal/workmanagement. The two sanitizers had drifted in both directions —
// pages made escaping idempotent first, tasks learned to decode an attribute
// before validating it first — and this file is the half that was missing.
//
// Before that hardening, `<a href="&#106;avascript:alert(1)">` PASSED safeHref:
// the value reaching it began with "&", which is not a URL-scheme character, so
// it was accepted as a relative URL. It was inert only because EscapeAttr
// re-encoded the "&" afterwards — a property of the encoding, not a decision
// the validator made, and one that would have gone live the moment escaping
// here changed. Both guards now come from internal/shared, so a fix to either
// reaches both surfaces.
func TestSanitizePageHTML_EntitiesCannotSmuggleMarkup(t *testing.T) {
	cases := []struct {
		name           string
		in             string
		mustNotContain []string
	}{
		{
			name:           "numeric-entity script tag stays text",
			in:             `&#60;script&#62;alert(1)&#60;/script&#62;`,
			mustNotContain: []string{"<script", "</script"},
		},
		{
			name:           "entity-obfuscated javascript href rejected",
			in:             `<a href="&#106;avascript:alert(1)">x</a>`,
			mustNotContain: []string{"javascript:", "href="},
		},
		{
			name:           "fully entity-obfuscated javascript scheme rejected",
			in:             `<a href="&#106;avascript&#58;alert(1)">x</a>`,
			mustNotContain: []string{"javascript:", "href="},
		},
		{
			name:           "hex-entity javascript scheme rejected",
			in:             `<a href="&#x6a;avascript:alert(1)">x</a>`,
			mustNotContain: []string{"javascript:", "href="},
		},
		{
			name:           "tab-entity inside a scheme rejected",
			in:             `<a href="jav&#9;ascript:alert(1)">x</a>`,
			mustNotContain: []string{"href="},
		},
		{
			name:           "leading-space entity does not hide a scheme",
			in:             `<a href="&#32;javascript:alert(1)">x</a>`,
			mustNotContain: []string{"javascript:", "href="},
		},
		{
			name:           "entity-obfuscated data image rejected",
			in:             `<img src="&#100;ata:image/png;base64,AAAA">`,
			mustNotContain: []string{"data:", "src="},
		},
		{
			name:           "entity-obfuscated external image rejected",
			in:             `<img src="&#104;ttps://evil.example/pixel.gif">`,
			mustNotContain: []string{"evil.example", "src="},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := sanitizePageHTML(tc.in)
			for _, s := range tc.mustNotContain {
				if strings.Contains(strings.ToLower(out), strings.ToLower(s)) {
					t.Errorf("output must not contain %q; got %q", s, out)
				}
			}
			// Pages are re-rendered and re-sanitized on every save, so the
			// guarantee has to survive a second pass too.
			if again := sanitizePageHTML(out); again != out {
				t.Errorf("not idempotent: once %q, twice %q", out, again)
			}
		})
	}
}

// TestSanitizePageHTML_KeepsLegitimateAmpersandInHref pins the other side of
// decode-before-validate: decoding is one layer and is always followed by
// re-escaping, so a query string a user legitimately wrote survives unchanged
// rather than accumulating "&amp;" layers.
func TestSanitizePageHTML_KeepsLegitimateAmpersandInHref(t *testing.T) {
	in := `<a href="/search?a=1&amp;b=2">x</a>`
	out := sanitizePageHTML(in)
	if !strings.Contains(out, `href="/search?a=1&amp;b=2"`) {
		t.Errorf("legitimate ampersand not preserved: %s", out)
	}
	if again := sanitizePageHTML(out); again != out {
		t.Errorf("not idempotent: once %q, twice %q", out, again)
	}
}
