// Package docs manages AsciiDoc pages, revision history, and task references
// for projects.  It provides CRUD operations for pages, tracks publish revisions,
// and maintains a reference table that links pages to the tasks they mention.
package docs

import (
	"fmt"
	"regexp"
	"strings"
)

var taskRefRe = regexp.MustCompile(`TASK-([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})`)

// ExtractTaskReferences scans AsciiDoc content for TASK-<uuid> patterns and
// returns a deduplicated list of referenced task IDs.
func ExtractTaskReferences(content string) []string {
	matches := taskRefRe.FindAllStringSubmatch(content, -1)
	seen := map[string]bool{}
	var ids []string
	for _, m := range matches {
		if len(m) > 1 && !seen[m[1]] {
			seen[m[1]] = true
			ids = append(ids, m[1])
		}
	}
	return ids
}

// Page domain object.
type Page struct {
	ID           string  `json:"id"`
	ProjectID    string  `json:"projectId"`
	ParentPageID *string `json:"parentPageId"`
	Title        string  `json:"title"`
	Slug         string  `json:"slug"`
	Content      string  `json:"content"`
	RenderedHTML string  `json:"renderedHtml"`
	Status       string  `json:"status"`
	CreatedAt    string  `json:"createdAt"`
	UpdatedAt    string  `json:"updatedAt"`
	Version      int     `json:"version"`
}

// PageRevision domain object.
type PageRevision struct {
	ID        string `json:"id"`
	PageID    string `json:"pageId"`
	Content   string `json:"content"`
	Message   string `json:"message"`
	AuthorID  string `json:"authorId"`
	CreatedAt string `json:"createdAt"`
}

// PageReference domain object.
type PageReference struct {
	ID        string `json:"id"`
	PageID    string `json:"pageId"`
	TaskID    string `json:"taskId"`
	CreatedAt string `json:"createdAt"`
}

// PageSearchResult is a lean page summary used for search and dashboard queries.
type PageSearchResult struct {
	ID          string `json:"id"`
	ProjectID   string `json:"projectId"`
	Title       string `json:"title"`
	Slug        string `json:"slug"`
	ProjectName string `json:"projectName"`
}

// Page status constants.
const (
	StatusDraft     = "DRAFT"
	StatusPublished = "PUBLISHED"
	StatusArchived  = "ARCHIVED"
)

// RenderAsciiDoc converts a defined subset of AsciiDoc to HTML and runs the
// result through an allowlist sanitizer, so the output is safe to embed for
// every project member regardless of the input (authored content or imported
// HTML).
//
// Supported AsciiDoc surface:
//   - Section titles "=" .. "======" -> <h1>..<h6> (with stable id anchors).
//   - Inline: bold "*text*", italic "_text_", monospace "`text`",
//     macro links "https://...[label]" / "link:href[label]", bare URLs, and
//     "TASK-<uuid>" references (rendered as a labelled anchor).
//   - Lists: unordered ("*"/"-") and ordered (".") with nesting by marker depth.
//   - Delimited blocks: listing/source "----" (with optional "[source,lang]"),
//     example blocks treated literally, and block quotes "____".
//   - Admonitions: NOTE:/TIP:/WARNING:/IMPORTANT:/CAUTION: paragraphs.
//   - Tables: "|===" with "|" cell rows; a leading row becomes the header.
//   - Block image macro "image::target[alt]" (relative targets only).
//
// Deliberately unsupported (stripped/escaped, never executed): passthrough
// (+++...+++, pass:[...], "++++" blocks), raw HTML, includes, attributes/macros
// other than the above, and footnotes.
func RenderAsciiDoc(content string) string {
	r := &renderer{}
	body := r.render(content)
	wrapped := `<div class="asciidoc-content">` + body + `</div>`
	return sanitizePageHTML(wrapped)
}

type renderer struct {
	sb strings.Builder
}

func (r *renderer) render(content string) string {
	lines := strings.Split(content, "\n")
	i := 0
	for i < len(lines) {
		line := lines[i]
		stripped := strings.TrimSpace(line)

		// Skip blank lines between blocks.
		if stripped == "" {
			i++
			continue
		}

		// Comments.
		if strings.HasPrefix(stripped, "//") {
			i++
			continue
		}

		// Delimited listing / source block: ---- ... ----
		if stripped == "----" || stripped == "...." {
			delim := stripped
			i++
			var code []string
			for i < len(lines) && strings.TrimSpace(lines[i]) != delim {
				code = append(code, lines[i])
				i++
			}
			if i < len(lines) {
				i++ // consume closing delimiter
			}
			r.sb.WriteString("<pre><code>")
			r.sb.WriteString(escTxt(strings.Join(code, "\n")))
			r.sb.WriteString("</code></pre>")
			continue
		}

		// [source,lang] attribute line preceding a listing block.
		if strings.HasPrefix(stripped, "[source") && strings.HasSuffix(stripped, "]") {
			lang := parseSourceLang(stripped)
			// Expect a following ---- block.
			if i+1 < len(lines) && strings.TrimSpace(lines[i+1]) == "----" {
				i += 2
				var code []string
				for i < len(lines) && strings.TrimSpace(lines[i]) != "----" {
					code = append(code, lines[i])
					i++
				}
				if i < len(lines) {
					i++
				}
				cls := ""
				if lang != "" {
					cls = ` class="language-` + lang + `"`
				}
				r.sb.WriteString("<pre><code" + cls + ">")
				r.sb.WriteString(escTxt(strings.Join(code, "\n")))
				r.sb.WriteString("</code></pre>")
				continue
			}
			i++
			continue
		}

		// Block quote: ____ ... ____
		if stripped == "____" {
			i++
			var quote []string
			for i < len(lines) && strings.TrimSpace(lines[i]) != "____" {
				quote = append(quote, lines[i])
				i++
			}
			if i < len(lines) {
				i++
			}
			r.sb.WriteString("<blockquote><p>")
			r.sb.WriteString(applyInline(strings.Join(quote, " ")))
			r.sb.WriteString("</p></blockquote>")
			continue
		}

		// Table: |=== ... |===
		if stripped == "|===" {
			i = r.renderTable(lines, i)
			continue
		}

		// Block image: image::target[alt]
		if m := blockImageRe.FindStringSubmatch(stripped); m != nil {
			r.sb.WriteString(`<img src="`)
			r.sb.WriteString(m[1])
			r.sb.WriteString(`" alt="`)
			r.sb.WriteString(m[2])
			r.sb.WriteString(`">`)
			i++
			continue
		}

		// Headings: = .. ======
		if lvl, text, ok := parseHeading(stripped); ok {
			id := slugifyHeading(text)
			fmt.Fprintf(&r.sb, "<h%d id=\"%s\">", lvl, id)
			r.sb.WriteString(applyInline(text))
			fmt.Fprintf(&r.sb, "</h%d>", lvl)
			i++
			continue
		}

		// Admonitions: NOTE: ...
		if name, text, ok := parseAdmonition(stripped); ok {
			r.sb.WriteString(`<div class="admonition admonition-` + strings.ToLower(name) + `">`)
			r.sb.WriteString(`<span class="admonition-label">` + name + `</span> `)
			r.sb.WriteString(`<span class="admonition-content">` + applyInline(text) + `</span>`)
			r.sb.WriteString("</div>")
			i++
			continue
		}

		// Lists (unordered/ordered, nestable).
		if _, _, ok := parseListItem(stripped); ok {
			i = r.renderList(lines, i, 0)
			continue
		}

		// Paragraph: collect consecutive non-blank, non-block lines.
		var para []string
		for i < len(lines) {
			ls := strings.TrimSpace(lines[i])
			if ls == "" || isBlockStart(ls) {
				break
			}
			para = append(para, ls)
			i++
		}
		r.sb.WriteString("<p>")
		r.sb.WriteString(applyInline(strings.Join(para, " ")))
		r.sb.WriteString("</p>")
	}
	return r.sb.String()
}

// renderList renders a (possibly nested) list starting at lines[i]. minDepth is
// the marker depth of the enclosing list. Returns the index after the list.
func (r *renderer) renderList(lines []string, i, minDepth int) int {
	_, depth, _ := parseListItem(strings.TrimSpace(lines[i]))
	ordered := isOrdered(strings.TrimSpace(lines[i]))
	if ordered {
		r.sb.WriteString("<ol>")
	} else {
		r.sb.WriteString("<ul>")
	}
	for i < len(lines) {
		ls := strings.TrimSpace(lines[i])
		if ls == "" {
			i++
			continue
		}
		text, d, ok := parseListItem(ls)
		if !ok || d < depth {
			break
		}
		if d > depth {
			// Nested list: render it inside the current (last) <li>.
			i = r.renderList(lines, i, depth)
			continue
		}
		if isOrdered(ls) != ordered {
			break
		}
		r.sb.WriteString("<li>")
		r.sb.WriteString(applyInline(text))
		// Look ahead for a nested list belonging to this item.
		if i+1 < len(lines) {
			next := strings.TrimSpace(lines[i+1])
			if _, nd, nok := parseListItem(next); nok && nd > depth {
				i = r.renderList(lines, i+1, depth+1)
				r.sb.WriteString("</li>")
				continue
			}
		}
		r.sb.WriteString("</li>")
		i++
	}
	if ordered {
		r.sb.WriteString("</ol>")
	} else {
		r.sb.WriteString("</ul>")
	}
	return i
}

// renderTable renders a |=== table starting at lines[i] (the |=== line). The
// first row of cells becomes the header. Returns the index after the table.
func (r *renderer) renderTable(lines []string, i int) int {
	i++ // skip opening |===
	var rows [][]string
	var cur []string
	for i < len(lines) {
		ls := strings.TrimSpace(lines[i])
		if ls == "|===" {
			i++
			break
		}
		if ls == "" {
			// Blank line separates rows in our simplified model.
			if len(cur) > 0 {
				rows = append(rows, cur)
				cur = nil
			}
			i++
			continue
		}
		if strings.HasPrefix(ls, "|") {
			cells := splitTableCells(ls)
			cur = append(cur, cells...)
		}
		i++
	}
	if len(cur) > 0 {
		rows = append(rows, cur)
	}
	if len(rows) == 0 {
		return i
	}
	r.sb.WriteString(`<table class="page-table">`)
	r.sb.WriteString("<thead><tr>")
	for _, h := range rows[0] {
		r.sb.WriteString(`<th scope="col">`)
		r.sb.WriteString(applyInline(h))
		r.sb.WriteString("</th>")
	}
	r.sb.WriteString("</tr></thead><tbody>")
	for _, row := range rows[1:] {
		r.sb.WriteString("<tr>")
		for _, c := range row {
			r.sb.WriteString("<td>")
			r.sb.WriteString(applyInline(c))
			r.sb.WriteString("</td>")
		}
		r.sb.WriteString("</tr>")
	}
	r.sb.WriteString("</tbody></table>")
	return i
}

// ---- line classification helpers ----

var (
	headingRe    = regexp.MustCompile(`^(={1,6})\s+(.*)$`)
	admonitionRe = regexp.MustCompile(`^(NOTE|TIP|WARNING|IMPORTANT|CAUTION):\s+(.*)$`)
	blockImageRe = regexp.MustCompile(`^image::([^\[]*)\[([^\]]*)\]$`)
	sourceLangRe = regexp.MustCompile(`^\[source\s*,\s*([a-zA-Z0-9_+-]+)\s*\]$`)
)

func parseHeading(s string) (int, string, bool) {
	m := headingRe.FindStringSubmatch(s)
	if m == nil {
		return 0, "", false
	}
	return len(m[1]), m[2], true
}

func parseAdmonition(s string) (string, string, bool) {
	m := admonitionRe.FindStringSubmatch(s)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

func parseSourceLang(s string) string {
	m := sourceLangRe.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	return m[1]
}

// parseListItem returns the item text, marker depth (1-based), and ok. Both
// unordered ("* "/"- "/"** ") and ordered (". "/".. ") markers are recognized.
func parseListItem(s string) (string, int, bool) {
	if s == "" {
		return "", 0, false
	}
	// Unordered: one or more '*' or one or more '-' followed by a space.
	if s[0] == '*' || s[0] == '-' {
		marker := s[0]
		n := 0
		for n < len(s) && s[n] == marker {
			n++
		}
		if n < len(s) && s[n] == ' ' {
			return strings.TrimSpace(s[n+1:]), n, true
		}
		return "", 0, false
	}
	// Ordered: one or more '.' followed by a space.
	if s[0] == '.' {
		n := 0
		for n < len(s) && s[n] == '.' {
			n++
		}
		if n < len(s) && s[n] == ' ' {
			return strings.TrimSpace(s[n+1:]), n, true
		}
		return "", 0, false
	}
	return "", 0, false
}

func isOrdered(s string) bool {
	return len(s) > 0 && s[0] == '.'
}

// isBlockStart reports whether a stripped line begins a non-paragraph block, so
// paragraph accumulation stops there.
func isBlockStart(s string) bool {
	if s == "" {
		return true
	}
	if _, _, ok := parseHeading(s); ok {
		return true
	}
	if _, _, ok := parseListItem(s); ok {
		return true
	}
	if _, _, ok := parseAdmonition(s); ok {
		return true
	}
	if s == "----" || s == "...." || s == "____" || s == "|===" {
		return true
	}
	if strings.HasPrefix(s, "[source") {
		return true
	}
	if blockImageRe.MatchString(s) {
		return true
	}
	if strings.HasPrefix(s, "//") {
		return true
	}
	return false
}

// splitTableCells splits a "| a | b | c" row into trimmed cell strings.
func splitTableCells(s string) []string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "|")
	parts := strings.Split(s, "|")
	cells := make([]string, 0, len(parts))
	for _, p := range parts {
		cells = append(cells, strings.TrimSpace(p))
	}
	return cells
}

func slugifyHeading(s string) string {
	s = strings.ToLower(s)
	var out []rune
	prevDash := false
	for _, c := range s {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			out = append(out, c)
			prevDash = false
		} else if !prevDash {
			out = append(out, '-')
			prevDash = true
		}
	}
	res := strings.Trim(string(out), "-")
	if res == "" {
		res = "section"
	}
	return "h-" + res
}
