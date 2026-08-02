package shared

import (
	"regexp"
	"strconv"
	"strings"
)

// HTML-safety primitives shared by every module that sanitizes user HTML.
//
// Two hand-rolled, allowlist-based sanitizers exist — internal/workmanagement
// for task descriptions and internal/docs for wiki pages. Their *allowlists*
// differ on purpose (pages allow tables, class, id, scope; tasks allow only
// images pointing at the authenticated attachment endpoint), and that
// difference is a policy decision worth keeping per module. Everything below
// the allowlist is not a policy decision: how an attribute value is decoded
// before it is judged, what counts as a URL scheme, and how a text run is
// escaped are the same question with the same answer in both places.
//
// They were written twice, and they drifted — which is the reason this file
// exists rather than a tidiness argument:
//
//   - internal/docs made EscapeText idempotent (the entityRe check) months
//     before internal/workmanagement did. The same class of fix reached one
//     module and not the other because nothing tied them together, and the gap
//     corrupted stored task descriptions one save at a time.
//   - Then the drift ran the other way: workmanagement learned to decode an
//     attribute value one layer *before* validating it, so an entity-obfuscated
//     scheme is judged as a browser would decode it, while docs still validated
//     the raw value and was inert only because EscapeAttr re-encoded it
//     afterwards — a property of the encoding, not a decision the validator
//     made, and one that would have gone live the moment docs' escaping became
//     idempotent too.
//
// A fix applied here now reaches both surfaces at once. Callers keep their own
// allowlists, tokenizer loops and image policy.

// namedEntities are the character references DecodeEntities understands: the
// set EscapeAttr can *produce*, plus &apos;/&nbsp; which browsers emit from a
// contenteditable. Anything else is left alone, which is safe because the
// caller re-escapes and a stray "&" is not a URL scheme character.
var namedEntities = map[string]string{
	"amp": "&", "lt": "<", "gt": ">", "quot": `"`, "apos": "'", "nbsp": " ",
}

// DecodeEntities undoes exactly ONE layer of character-reference encoding. Run
// it on attribute values *before* they are validated, so a URL scheme hidden
// behind entities is judged as what a browser would decode it to. Without it
// "&#106;avascript:alert(1)" reaches SafeHref starting with "&", which is not a
// scheme character, so it passes the relative-URL branch; such a payload is
// inert only because EscapeAttr re-encodes the "&" afterwards. That is a
// coincidence of encoding, not an access control.
//
// One layer, never more: repeatedly decoding would unravel content a user
// legitimately wrote (and would un-escape historic double-escaped data into
// live markup). Decoding here is always followed by re-escaping, so a decoded
// "<" becomes "&lt;" again rather than opening a tag — the caller's tokenizer
// has already run by this point and never sees the decoded form.
func DecodeEntities(s string) string {
	if !strings.Contains(s, "&") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] != '&' {
			b.WriteByte(s[i])
			i++
			continue
		}
		// Find the terminating ';' within a bounded window; a longer run is not
		// a character reference we recognize.
		end := -1
		for j := i + 1; j < len(s) && j <= i+10; j++ {
			if s[j] == ';' {
				end = j
				break
			}
		}
		if end < 0 {
			b.WriteByte('&')
			i++
			continue
		}
		if decoded, ok := decodeEntityBody(s[i+1 : end]); ok {
			b.WriteString(decoded)
			i = end + 1
			continue
		}
		b.WriteByte('&')
		i++
	}
	return b.String()
}

// decodeEntityBody decodes the text between "&" and ";", reporting whether it
// was a character reference this sanitizer recognizes.
func decodeEntityBody(body string) (string, bool) {
	if body == "" {
		return "", false
	}
	if body[0] == '#' {
		digits, base := body[1:], 10
		if len(digits) > 0 && (digits[0] == 'x' || digits[0] == 'X') {
			digits, base = digits[1:], 16
		}
		if digits == "" {
			return "", false
		}
		n, err := strconv.ParseInt(digits, base, 32)
		// Reject NUL and anything outside the Unicode range or in the surrogate
		// block: those are not valid content and would only obscure a payload.
		if err != nil || n <= 0 || n > 0x10FFFF || (n >= 0xD800 && n <= 0xDFFF) {
			return "", false
		}
		return string(rune(n)), true
	}
	if v, ok := namedEntities[strings.ToLower(body)]; ok {
		return v, true
	}
	return "", false
}

// isSchemeChar reports whether c is valid inside a URL scheme name.
func isSchemeChar(c rune) bool {
	return (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '+' || c == '-' || c == '.'
}

// HasURLScheme reports whether s begins with a "scheme:" prefix. Pass a
// lowercased value.
func HasURLScheme(s string) bool {
	for i, c := range s {
		if c == ':' {
			return i > 0
		}
		if !isSchemeChar(c) {
			return false
		}
	}
	return false
}

// SafeHref reports whether an anchor href is allowed: http(s), mailto, a
// same-document fragment ("#anchor", which reaches the relative branch), or any
// other relative URL. Anything carrying a scheme — javascript:, data:,
// vbscript: — is rejected, as is a value containing control characters that
// could be used to break a scheme up.
//
// Pass the value through DecodeEntities first; this function judges what it is
// given, and an encoded scheme is not one it can see.
func SafeHref(v string) bool {
	t := strings.TrimSpace(v)
	if t == "" {
		return false
	}
	lower := strings.ToLower(t)
	if strings.ContainsAny(lower, "\x00\n\r\t") {
		return false
	}
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "mailto:") {
		return true
	}
	// Relative URLs (no scheme). Reject if a scheme is present.
	return !HasURLScheme(lower)
}

// IsRelativeURL reports whether v is a same-origin relative reference: non-
// empty, free of control characters, carrying no scheme, and not
// protocol-relative ("//host"). It is the common half of every <img src>
// policy; the path policy on top of it is the caller's, because the two
// sanitizers genuinely differ there — wiki pages allow any rooted path, task
// descriptions only the authenticated attachment content endpoint.
func IsRelativeURL(v string) bool {
	t := strings.TrimSpace(v)
	if t == "" {
		return false
	}
	lower := strings.ToLower(t)
	if strings.ContainsAny(lower, "\x00\n\r\t") {
		return false
	}
	return !strings.HasPrefix(t, "//") && !HasURLScheme(lower)
}

// UnquoteAttr strips one matched pair of surrounding quotes from a raw
// attribute value.
func UnquoteAttr(v string) string {
	if len(v) >= 2 {
		if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
			return v[1 : len(v)-1]
		}
	}
	return v
}

// entityRe matches an already-encoded character reference at the start of the
// input, so EscapeText can leave it alone instead of encoding its "&" again.
var entityRe = regexp.MustCompile(`^&(#[0-9]+|#[xX][0-9a-fA-F]+|[a-zA-Z][a-zA-Z0-9]*);`)

// EscapeText encodes a text run so no markup can survive it, and is idempotent:
// a character reference a previous pass already wrote is preserved rather than
// re-encoded. Without that, sanitizing is not a fixed point and every save adds
// another "&amp;" layer, which is exactly how stored task descriptions degraded
// until "->" read as "-&amp;gt;".
//
// Preserving a reference cannot smuggle markup: the caller resolves text runs
// only after tokenizing, so "&#60;script&#62;" is displayed, never parsed.
func EscapeText(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		switch s[i] {
		case '&':
			if m := entityRe.FindString(s[i:]); m != "" {
				b.WriteString(m)
				i += len(m)
				continue
			}
			b.WriteString("&amp;")
			i++
		case '<':
			b.WriteString("&lt;")
			i++
		case '>':
			b.WriteString("&gt;")
			i++
		default:
			b.WriteByte(s[i])
			i++
		}
	}
	return b.String()
}

// EscapeAttr encodes an attribute value. Unlike EscapeText it is deliberately
// NOT idempotent-by-entity: an attribute is always written back inside quotes
// we control, and a literal "&" in a URL query must survive as "&amp;".
func EscapeAttr(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;")
	return r.Replace(s)
}
