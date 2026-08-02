package docs

import (
	"regexp"
	"strings"

	"github.com/octbase/octbase-api/internal/shared"
)

// This file implements a hand-rolled, allowlist-based HTML sanitizer for the
// HTML emitted by RenderAsciiDoc. It deliberately avoids any third-party
// dependency.
//
// Why a docs-specific sanitizer rather than reusing
// workmanagement.SanitizeDescriptionHTML: that sanitizer is tuned to the small
// tag set produced by the task contenteditable editor and, critically, only
// permits <img src> pointing at the task attachment content endpoint
// (/api/v1/tasks/<id>/attachments/<id>/content). AsciiDoc rendering emits a
// broader element set (h1..h6, tables, hr, admonition wrappers, blockquotes
// with citations) and a different, intentionally stricter image policy. Rather
// than overload the task sanitizer with page concerns (or relax its tight image
// rule), pages own an allowlist scoped to exactly what RenderAsciiDoc produces.
// Both sanitizers share the same proven "default deny" tokenize-and-rebuild
// design, so the security posture is identical.
//
// Strategy ("default deny"):
//   - Tokenize the input into tags and text runs.
//   - Drop any tag not on the allowlist (and its attributes), keeping inner text.
//   - For allowed tags, keep only allowlisted attributes whose values pass a
//     strict scheme/path check.
//   - Re-escape all text so no stray markup can survive.
//   - Dangerous subtrees (<script>, <style>, <iframe>, ...) have their text
//     content discarded, not just the tags.
//
// The server is the source of truth: sanitizePageHTML wraps every render so the
// stored RenderedHTML, the render-on-read output, and the live preview are all
// guaranteed safe regardless of authored content or imported HTML.

// allowedTags maps the lowercase tag name to the set of attributes permitted on
// it. A tag present here with an empty map allows no attributes.
var allowedTags = map[string]map[string]bool{
	"div":        {"class": true},
	"span":       {"class": true},
	"p":          {},
	"br":         {},
	"hr":         {},
	"h1":         {"id": true},
	"h2":         {"id": true},
	"h3":         {"id": true},
	"h4":         {"id": true},
	"h5":         {"id": true},
	"h6":         {"id": true},
	"ul":         {},
	"ol":         {},
	"li":         {},
	"blockquote": {},
	"cite":       {},
	"pre":        {"class": true},
	"code":       {"class": true},
	"strong":     {},
	"b":          {},
	"em":         {},
	"i":          {},
	"a":          {"href": true, "class": true},
	"img":        {"src": true, "alt": true},
	"table":      {"class": true},
	"thead":      {},
	"tbody":      {},
	"tr":         {},
	"th":         {"scope": true},
	"td":         {},
	"caption":    {},
}

// voidTags never have a closing tag.
var voidTags = map[string]bool{"br": true, "img": true, "hr": true}

// dangerousTags have their entire text content discarded (not just the tag).
var dangerousTags = map[string]bool{
	"script": true, "style": true, "iframe": true, "object": true,
	"embed": true, "noscript": true, "template": true, "title": true,
}

// classRe restricts class attribute values to a safe character set so a crafted
// class cannot be used to break out of the attribute.
var classRe = regexp.MustCompile(`^[a-zA-Z0-9 _-]+$`)

// idRe restricts id attribute values (used by heading anchors / TOC).
var idRe = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// tagRe matches a single HTML tag: opening, closing, or self-closing. Group 1 is
// an optional "/", group 2 the tag name, group 3 the raw attribute string.
var tagRe = regexp.MustCompile(`(?s)<(/?)([a-zA-Z][a-zA-Z0-9]*)((?:[^>"']|"[^"]*"|'[^']*')*)/?>`)

// attrRe matches name="value", name='value', or bare name attributes.
var attrRe = regexp.MustCompile(`([a-zA-Z_:][-a-zA-Z0-9_:.]*)\s*(?:=\s*("[^"]*"|'[^']*'|[^\s"'>]+))?`)

// sanitizePageHTML returns a cleaned copy of the input HTML containing only
// allowlisted tags and attributes. All other markup is stripped; text is
// preserved and re-escaped. The result never contains scripts, event handlers,
// styles, javascript:/data: URLs, or external image sources.
func sanitizePageHTML(input string) string {
	if input == "" {
		return ""
	}

	var b strings.Builder
	// dropDepth > 0 means we are inside a dangerous element whose text must be
	// discarded.
	dropDepth := 0
	last := 0

	for _, loc := range tagRe.FindAllStringSubmatchIndex(input, -1) {
		start, end := loc[0], loc[1]
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
		b.WriteString(">")
	}

	if dropDepth == 0 {
		b.WriteString(shared.EscapeText(input[last:]))
	}
	return b.String()
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
		// a browser would decode it to rather than as inert text. Without this
		// a page link written as "&#106;avascript:alert(1)" passed safeHref —
		// the value reaching it starts with "&", which is not a scheme
		// character, so it was accepted as a relative URL — and was inert only
		// because EscapeAttr re-encoded it afterwards. That is a property of
		// the encoding, not a decision this validator made, and it would have
		// gone live the moment escaping here became idempotent. Tasks were
		// hardened first (commit 0b0d928); pages are the other half.
		val := shared.DecodeEntities(shared.UnquoteAttr(m[2]))
		switch name {
		case "href":
			if !shared.SafeHref(val) {
				continue
			}
		case "src":
			if !safeImageSrc(val) {
				continue
			}
		case "class":
			if !classRe.MatchString(val) {
				continue
			}
		case "id":
			if !idRe.MatchString(val) {
				continue
			}
		case "scope":
			if val != "row" && val != "col" {
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

// safeImageSrc constrains <img src> to relative paths only. External URLs
// (SSRF / tracking-pixel risk) and any scheme (including data:) are rejected, as
// are protocol-relative ("//host") URLs.
//
// This is the one URL rule pages own rather than share: a wiki page may
// reference any rooted path served by this app, while a task description may
// only inline the authenticated attachment content endpoint.
// shared.IsRelativeURL covers everything the two policies agree on.
func safeImageSrc(v string) bool {
	return shared.IsRelativeURL(v) && strings.HasPrefix(strings.TrimSpace(v), "/")
}
