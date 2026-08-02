package docs

import (
	"regexp"
	"strings"
)

// Inline AsciiDoc formatting. The output of these functions is HTML that is
// subsequently passed through sanitizePageHTML, which re-escapes all text runs.
// Therefore text content is emitted raw here (NOT pre-escaped): pre-escaping
// would be double-escaped by the sanitizer. The generated tags (<strong>, <em>,
// <code>, <a>) survive sanitization; everything between them is escaped by the
// sanitizer as plain text.

var (
	// Macro link with label: https://host/path[label] or link:target[label].
	linkMacroRe = regexp.MustCompile(`\b(?:link:)?((?:https?|mailto):[^\s\[\]]+)\[([^\]]*)\]`)
	// Bare URL (no label). Stops at whitespace, brackets, angle brackets and
	// "&" so it does not swallow escaped entities like &quot; produced when the
	// surrounding text was HTML-escaped.
	bareURLRe = regexp.MustCompile(`\b(https?://[^\s\[\]<>&"']+)`)
	// TASK-<uuid> reference.
	inlineTaskRe = regexp.MustCompile(`TASK-([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})`)
)

// escTxt escapes HTML-significant characters in author-supplied source text.
// The renderer escapes its own text so literal markup such as "<b>" from the
// author or an import is rendered as text, not interpreted. sanitizePageHTML
// runs afterwards as an idempotent defense-in-depth layer.
func escTxt(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;")
	return r.Replace(s)
}

// applyInline converts inline AsciiDoc markup in a single logical line to HTML.
// The input is author source text; it is HTML-escaped first, then inline markup
// (links/task refs, monospace, bold, italic) is applied. Escaping first is safe
// because the markup delimiters (* _ ` [ ]) are untouched by escaping and link
// URLs are validated by the sanitizer.
func applyInline(s string) string {
	s = escTxt(s)

	// 1. Monospace `code` — protect contents from link/format passes.
	s = replaceSpans(s, '`', func(inner string) string {
		return "<code>" + inner + "</code>"
	})

	// 2. Macro links with explicit label.
	s = linkMacroRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := linkMacroRe.FindStringSubmatch(m)
		href := sub[1]
		label := sub[2]
		if label == "" {
			label = href
		}
		return `<a href="` + href + `" class="page-link">` + label + `</a>`
	})

	// 3. TASK-<uuid> references -> anchor into the task view. Use a relative
	//    fragment route that the SPA understands; sanitizer allows "#...".
	s = inlineTaskRe.ReplaceAllStringFunc(s, func(m string) string {
		id := inlineTaskRe.FindStringSubmatch(m)[1]
		return `<a href="#/tasks/` + id + `" class="task-ref">` + m + `</a>`
	})

	// 4. Bare URLs not already inside an anchor.
	s = linkifyBareURLs(s)

	// 5. Bold *text* and italic _text_.
	s = replaceSpans(s, '*', func(inner string) string {
		return "<strong>" + applyEmphasis(inner) + "</strong>"
	})
	s = applyEmphasis(s)

	return s
}

func applyEmphasis(s string) string {
	return replaceSpans(s, '_', func(inner string) string {
		return "<em>" + inner + "</em>"
	})
}

// replaceSpans replaces balanced single-character delimited spans (e.g. *bold*)
// with the result of fn(inner). It skips spans that span existing tag markup so
// already-generated HTML is not corrupted. A delimiter that is not balanced is
// left as a literal.
func replaceSpans(s string, delim byte, fn func(string) string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		c := s[i]
		// Don't descend into already-emitted tags.
		if c == '<' {
			end := strings.IndexByte(s[i:], '>')
			if end >= 0 {
				b.WriteString(s[i : i+end+1])
				i += end + 1
				continue
			}
		}
		if c == delim {
			// Count the opening run of delimiters (AsciiDoc treats "**" as the
			// unconstrained form of "*"); collapse a doubled marker.
			open := 1
			if i+1 < len(s) && s[i+1] == delim {
				open = 2
			}
			start := i + open
			// Find the matching closing run on the same text run, not crossing
			// a '<' (so we never split generated tags).
			j := start
			closing := -1
			for j < len(s) {
				if s[j] == '<' {
					break
				}
				if s[j] == delim {
					closing = j
					break
				}
				j++
			}
			if closing > start {
				inner := s[start:closing]
				b.WriteString(fn(inner))
				skip := 1
				if closing+1 < len(s) && open == 2 && s[closing+1] == delim {
					skip = 2
				}
				i = closing + skip
				continue
			}
		}
		b.WriteByte(c)
		i++
	}
	return b.String()
}

// linkifyBareURLs wraps bare http(s) URLs in anchors, but only in text segments
// outside existing tags (so href values are not re-linked).
func linkifyBareURLs(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '<' {
			end := strings.IndexByte(s[i:], '>')
			if end >= 0 {
				b.WriteString(s[i : i+end+1])
				i += end + 1
				continue
			}
		}
		// Find next tag boundary.
		next := strings.IndexByte(s[i:], '<')
		var segment string
		if next < 0 {
			segment = s[i:]
			i = len(s)
		} else {
			segment = s[i : i+next]
			i += next
		}
		b.WriteString(bareURLRe.ReplaceAllString(segment, `<a href="$1" class="page-link">$1</a>`))
	}
	return b.String()
}
