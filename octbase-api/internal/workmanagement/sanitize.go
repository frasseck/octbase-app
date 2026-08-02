package workmanagement

import (
	"regexp"
	"strings"

	"github.com/octbase/octbase-api/internal/shared"
)

// This file implements a hand-rolled, allowlist-based HTML sanitizer for task
// descriptions. It deliberately avoids any third-party dependency
// (golang.org/x/net/html is not in go.mod, not even indirect; bluemonday would
// be a new dependency). The accepted markup is a small, constrained subset that
// round-trips cleanly with the frontend contenteditable editor, so a full
// HTML5 tokenizer is unnecessary. The strategy is "default deny":
//
//   - Tokenize the input into tags and text runs.
//   - Drop any tag not on the allowlist (and its attributes), keeping inner text.
//   - For allowed tags, keep only allowlisted attributes whose values pass a
//     strict scheme/path check.
//   - Re-escape all text so no stray markup can survive.
//   - Entire dangerous subtrees (<script>, <style>) have their text content
//     discarded, not just the tags.
//
// The server is the source of truth: SanitizeDescriptionHTML is applied on every
// create/update and on CSV import, regardless of what the client claims to send.

// allowedTags maps the lowercase tag name to the set of attributes permitted on
// it. A tag present here with an empty map allows no attributes.
var allowedTags = map[string]map[string]bool{
	"p":          {},
	"br":         {},
	"h3":         {},
	"h4":         {},
	"ul":         {},
	"ol":         {},
	"li":         {},
	"blockquote": {},
	"pre":        {},
	"code":       {},
	"strong":     {},
	"b":          {},
	"em":         {},
	"i":          {},
	"a":          {"href": true},
	"img":        {"src": true, "alt": true},
}

// voidTags never have a closing tag.
var voidTags = map[string]bool{"br": true, "img": true}

// dangerousTags have their entire text content discarded (not just the tag).
var dangerousTags = map[string]bool{
	"script": true, "style": true, "iframe": true, "object": true,
	"embed": true, "noscript": true, "template": true, "title": true,
}

// tagRe matches a single HTML tag: opening, closing, or self-closing. Group 1 is
// an optional "/", group 2 the tag name, group 3 the raw attribute string.
var tagRe = regexp.MustCompile(`(?s)<(/?)([a-zA-Z][a-zA-Z0-9]*)((?:[^>"']|"[^"]*"|'[^']*')*)>`)

// attrRe matches name="value", name='value', or bare name attributes.
var attrRe = regexp.MustCompile(`([a-zA-Z_:][-a-zA-Z0-9_:.]*)\s*(?:=\s*("[^"]*"|'[^']*'|[^\s"'>]+))?`)

// SanitizeDescriptionHTML returns a cleaned copy of the input HTML containing
// only allowlisted tags and attributes. All other markup is stripped; text is
// preserved and re-escaped. The result never contains scripts, event handlers,
// styles, javascript: URLs, or external image sources.
func SanitizeDescriptionHTML(input string) string {
	if input == "" {
		return ""
	}

	var b strings.Builder
	// dropDepth > 0 means we are inside a dangerous element whose text must be
	// discarded. We track it as a counter rather than a stack since the tags we
	// drop entirely do not legitimately nest in our content.
	dropDepth := 0
	last := 0

	for _, loc := range tagRe.FindAllStringSubmatchIndex(input, -1) {
		start, end := loc[0], loc[1]
		// Emit text between the previous tag and this one (re-escaped), unless we
		// are inside a dropped subtree.
		if dropDepth == 0 {
			b.WriteString(shared.EscapeText(input[last:start]))
		}
		last = end

		closing := input[loc[2]:loc[3]] == "/"
		name := strings.ToLower(input[loc[4]:loc[5]])
		rawAttrs := input[loc[6]:loc[7]]

		if dangerousTags[name] {
			if closing {
				if dropDepth > 0 {
					dropDepth--
				}
			} else if !voidTags[name] {
				dropDepth++
			}
			continue
		}
		if dropDepth > 0 {
			// Inside a dropped subtree: ignore everything.
			continue
		}

		attrs, ok := allowedTags[name]
		if !ok {
			// Unknown tag: drop the tag, keep surrounding text.
			continue
		}

		if closing {
			if !voidTags[name] {
				b.WriteString("</")
				b.WriteString(name)
				b.WriteString(">")
			}
			continue
		}

		b.WriteString("<")
		b.WriteString(name)
		b.WriteString(sanitizeAttrs(name, rawAttrs, attrs))
		if voidTags[name] {
			b.WriteString(">")
		} else {
			b.WriteString(">")
		}
	}

	if dropDepth == 0 {
		b.WriteString(shared.EscapeText(input[last:]))
	}
	return strings.TrimSpace(b.String())
}

// sanitizeAttrs returns the rendered, allowlisted attribute string (with a
// leading space per attribute) for a tag.
func sanitizeAttrs(tag, raw string, allowed map[string]bool) string {
	if raw == "" || len(allowed) == 0 {
		return ""
	}
	var b strings.Builder
	for _, m := range attrRe.FindAllStringSubmatch(raw, -1) {
		name := strings.ToLower(m[1])
		if !allowed[name] {
			continue
		}
		// Decode one layer of character references BEFORE validating, so a
		// scheme hidden behind entities ("&#106;avascript:") is judged as what
		// a browser would decode it to rather than as inert text. EscapeAttr
		// re-escapes the value below, so the stored form is unchanged in shape.
		val := shared.DecodeEntities(shared.UnquoteAttr(m[2]))
		switch name {
		case "href":
			if !shared.SafeHref(val) {
				continue
			}
		case "src":
			// Inline images may only reference our own authenticated attachment
			// content endpoint (a relative path). External/SSRF/tracking-pixel
			// sources are rejected.
			if !safeImageSrc(val) {
				continue
			}
		}
		b.WriteString(" ")
		b.WriteString(name)
		b.WriteString(`="`)
		b.WriteString(shared.EscapeAttr(val))
		b.WriteString(`"`)
	}
	return b.String()
}

// safeImageSrc only permits a relative path into our attachment content
// endpoint, e.g. "/api/v1/tasks/<id>/attachments/<id>/content". No scheme, no
// host, no "//" protocol-relative URL.
//
// This is the one URL rule that is genuinely a task-module policy rather than a
// shared primitive: internal/docs allows any rooted path for a wiki page image,
// while a task description may only inline an attachment the reader is already
// authorized to fetch. shared.IsRelativeURL covers everything both agree on.
func safeImageSrc(v string) bool {
	if !shared.IsRelativeURL(v) {
		return false
	}
	t := strings.TrimSpace(v)
	if !strings.HasPrefix(t, "/api/v1/tasks/") {
		return false
	}
	return strings.Contains(t, "/attachments/") && strings.HasSuffix(t, "/content")
}

// StripHTMLToText removes all tags and unescapes entities to produce a plain
// text approximation, used for CSV export so spreadsheets do not show raw
// markup. It is intentionally lossy.
func StripHTMLToText(input string) string {
	if input == "" {
		return ""
	}
	// Convert block-ish boundaries to newlines for readability.
	replacer := strings.NewReplacer(
		"</p>", "\n", "<br>", "\n", "<br/>", "\n", "<br />", "\n",
		"</li>", "\n", "</h3>", "\n", "</h4>", "\n", "</blockquote>", "\n",
	)
	s := replacer.Replace(input)
	s = tagRe.ReplaceAllString(s, "")
	s = strings.NewReplacer(
		"&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'", "&nbsp;", " ",
	).Replace(s)
	// Collapse excessive blank lines.
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(s)
}
