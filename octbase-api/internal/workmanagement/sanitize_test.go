package workmanagement

import (
	"strings"
	"testing"

	"github.com/octbase/octbase-api/internal/shared"
)

func TestSanitizeDescriptionHTML_StripsScript(t *testing.T) {
	cases := []struct {
		name string
		in   string
		// mustNotContain are substrings that must be absent from the output.
		mustNotContain []string
		// mustContain are substrings that must be present.
		mustContain []string
	}{
		{
			name:           "script tag and its body removed",
			in:             `<p>hello</p><script>alert(1)</script>`,
			mustNotContain: []string{"<script", "alert(1)"},
			mustContain:    []string{"<p>hello</p>"},
		},
		{
			name:           "onerror attribute stripped",
			in:             `<img src="/api/v1/tasks/t/attachments/a/content" onerror="alert(1)">`,
			mustNotContain: []string{"onerror", "alert(1)"},
			mustContain:    []string{"<img"},
		},
		{
			name:           "javascript href neutralized",
			in:             `<a href="javascript:alert(1)">x</a>`,
			mustNotContain: []string{"javascript:", "href"},
			mustContain:    []string{">x</a>"},
		},
		{
			name:           "style tag body removed",
			in:             `<style>body{display:none}</style><p>ok</p>`,
			mustNotContain: []string{"<style", "display:none"},
			mustContain:    []string{"<p>ok</p>"},
		},
		{
			name:           "iframe removed",
			in:             `<iframe src="https://evil.example"></iframe><p>ok</p>`,
			mustNotContain: []string{"<iframe", "evil.example"},
			mustContain:    []string{"<p>ok</p>"},
		},
		{
			name:           "class and style attributes dropped",
			in:             `<p class="x" style="color:red">t</p>`,
			mustNotContain: []string{"class", "style", "color:red"},
			mustContain:    []string{"<p>t</p>"},
		},
		{
			name:           "external image src rejected",
			in:             `<img src="https://evil.example/pixel.gif">`,
			mustNotContain: []string{"evil.example", "src="},
			mustContain:    []string{"<img"},
		},
		{
			name:           "data uri image rejected",
			in:             `<img src="data:image/png;base64,AAAA">`,
			mustNotContain: []string{"data:", "src="},
			mustContain:    []string{"<img"},
		},
		{
			name: "nested malformed script tag neutralized",
			in:   `<scr<script>ipt>alert(1)</script>`,
			// The real <script> token is removed; any residual text is inert
			// (escaped), never an executable <script> element.
			mustNotContain: []string{"<script", "<scr "},
		},
		{
			name:           "allowed formatting kept",
			in:             `<p><strong>bold</strong> and <em>italic</em> and <code>x</code></p><ul><li>one</li></ul>`,
			mustContain:    []string{"<strong>bold</strong>", "<em>italic</em>", "<code>x</code>", "<ul>", "<li>one</li>"},
			mustNotContain: []string{"<script"},
		},
		{
			name:        "valid attachment image kept",
			in:          `<img src="/api/v1/tasks/abc/attachments/def/content" alt="shot">`,
			mustContain: []string{`src="/api/v1/tasks/abc/attachments/def/content"`, `alt="shot"`},
		},
		{
			name:        "http link kept",
			in:          `<a href="https://example.com">link</a>`,
			mustContain: []string{`href="https://example.com"`, ">link</a>"},
		},
		{
			name:           "angle brackets in text escaped",
			in:             `a < b && c > d`,
			mustContain:    []string{"a &lt; b &amp;&amp; c &gt; d"},
			mustNotContain: []string{"<b"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := SanitizeDescriptionHTML(tc.in)
			for _, s := range tc.mustNotContain {
				if strings.Contains(out, s) {
					t.Errorf("output must not contain %q; got %q", s, out)
				}
			}
			for _, s := range tc.mustContain {
				if !strings.Contains(out, s) {
					t.Errorf("output must contain %q; got %q", s, out)
				}
			}
			// Output must never contain a raw <script or on*= handler.
			lower := strings.ToLower(out)
			if strings.Contains(lower, "<script") || strings.Contains(lower, "javascript:") {
				t.Errorf("sanitized output still dangerous: %q", out)
			}
		})
	}
}

// TestSanitizeDescriptionHTML_Idempotent pins the property that makes the edit
// round-trip safe: sanitizing an already-sanitized description must be a no-op.
// Without it every save re-escaped the previous save's entities, so "&" grew a
// layer per edit (&amp; -> &amp;amp; -> &amp;amp;amp;) and descriptions with
// punctuation degraded until they were unreadable.
func TestSanitizeDescriptionHTML_Idempotent(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"ampersand in text", `<p>Tom & Jerry</p>`},
		{"pre-escaped entities", `<p>a &gt; b &lt; c &amp; d</p>`},
		{"the reported repro", `a > b < c & d`},
		{"arrow in text", `<p>stage 1 -> stage 2</p>`},
		{"query string in href", `<a href="https://example.com/?a=1&b=2">link</a>`},
		{"pre-escaped href", `<a href="https://example.com/?a=1&amp;b=2">link</a>`},
		{"attachment image", `<img src="/api/v1/tasks/abc/attachments/def/content" alt="a & b">`},
		{"formatting with entities", `<ul><li>x &amp; y</li><li>p &lt; q</li></ul>`},
		{"stripped tag leaves text", `<div>kept & text</div>`},
		{"named entity we do not decode", `<p>&copy; 2026</p>`},
		{"numeric entity", `<p>arrow &#8594; here</p>`},
		{"non-breaking space from a contenteditable", `<p>a&nbsp;b</p>`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			once := SanitizeDescriptionHTML(tc.in)
			twice := SanitizeDescriptionHTML(once)
			if once != twice {
				t.Errorf("not idempotent:\n  once:  %q\n  twice: %q", once, twice)
			}
			// A third pass guards against a two-cycle rather than a fixed point.
			if thrice := SanitizeDescriptionHTML(twice); thrice != once {
				t.Errorf("not stable at pass 3:\n  once:   %q\n  thrice: %q", once, thrice)
			}
			// Idempotence must not be bought by mangling the first save: an
			// entity the user's editor sent has to survive as an entity.
			if strings.Contains(tc.in, "&") && strings.Contains(once, "&amp;amp;") {
				t.Errorf("first save already double-encoded: %q", once)
			}
		})
	}
}

// TestSanitizeDescriptionHTML_EntitiesCannotSmuggleMarkup guards the security
// half of the idempotence fix. Decoding entities before re-escaping them is what
// makes the round trip stable, but it must never let an entity-obfuscated
// payload decode into live markup or a live URL scheme.
func TestSanitizeDescriptionHTML_EntitiesCannotSmuggleMarkup(t *testing.T) {
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
			name:           "named-entity script tag stays text",
			in:             `&lt;script&gt;alert(1)&lt;/script&gt;`,
			mustNotContain: []string{"<script", "</script"},
		},
		{
			name:           "entity-obfuscated javascript href rejected",
			in:             `<a href="&#106;avascript:alert(1)">x</a>`,
			mustNotContain: []string{"javascript:", "href="},
		},
		{
			name:           "entity-obfuscated data image rejected",
			in:             `<img src="&#100;ata:image/png;base64,AAAA">`,
			mustNotContain: []string{"data:", "src="},
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
			name:           "entity-obfuscated external image rejected",
			in:             `<img src="&#104;ttps://evil.example/pixel.gif">`,
			mustNotContain: []string{"evil.example", "src="},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := SanitizeDescriptionHTML(tc.in)
			for _, s := range tc.mustNotContain {
				if strings.Contains(strings.ToLower(out), strings.ToLower(s)) {
					t.Errorf("output must not contain %q; got %q", s, out)
				}
			}
			// And the guarantee still has to hold after a second pass, since
			// that is what the edit round trip does.
			if again := SanitizeDescriptionHTML(out); again != out {
				t.Errorf("not idempotent: once %q, twice %q", out, again)
			}
		})
	}
}

func TestStripHTMLToText(t *testing.T) {
	in := `<p>Hello <strong>world</strong></p><ul><li>a</li><li>b</li></ul>`
	out := StripHTMLToText(in)
	if strings.Contains(out, "<") {
		t.Errorf("expected no tags, got %q", out)
	}
	for _, want := range []string{"Hello", "world", "a", "b"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in %q", want, out)
		}
	}
}

func TestSafeHref(t *testing.T) {
	allow := []string{"https://x.com", "http://x.com", "mailto:a@b.com", "/relative/path", "page.html"}
	deny := []string{"javascript:alert(1)", "data:text/html,x", "vbscript:x", " javascript:x"}
	for _, v := range allow {
		if !shared.SafeHref(v) {
			t.Errorf("expected %q to be allowed", v)
		}
	}
	for _, v := range deny {
		if shared.SafeHref(v) {
			t.Errorf("expected %q to be denied", v)
		}
	}
}
