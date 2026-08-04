package shared

import (
	"log"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"

	"github.com/go-chi/chi/v5/middleware"
)

// Credentials that travel in a URL instead of a header or body. Each of these
// is a bearer credential in its own right, and chi's request logger prints the
// full request target, so every one of them used to land in the API's stdout in
// cleartext — readable by anyone who can read the container's logs (root, the
// instance's own account, anything that ships or backs up the journal).
//
//   - token — the SSE fallback for EventSource clients, which cannot set an
//     Authorization header (internal/sse/handler.go resolveUser). It is a normal
//     access token, valid for the whole OCTBASE_JWT_ACCESS_TTL.
//   - code  — the OAuth authorization code on the SCM callback, exchangeable for
//     the provider access/refresh tokens we then store encrypted.
//   - state — the one-time CSRF state paired with that code.
//
// The invitation token is the same class but travels as a PATH segment
// (/api/v1/invitations/{token}[/accept]), so it needs its own rule below rather
// than a query-parameter lookup.
var sensitiveQueryParams = map[string]bool{
	"token": true,
	"code":  true,
	"state": true,
}

const redactedPlaceholder = "REDACTED"

// invitationPathPrefix is the one route family whose secret is in the path.
const invitationPathPrefix = "/api/v1/invitations/"

// RedactRequestTarget returns target with every URL-borne credential replaced by
// REDACTED, preserving the rest of the line so the log stays useful for
// debugging: the route, the remaining parameters and the shape of the request
// are all still there. target is a request target as it appears on the request
// line (origin-form, "/path?query"); an absolute-form target is handled too,
// because chi's formatter prints scheme://host + RequestURI.
//
// Redaction is by parameter NAME, not by looking for token-shaped values: a
// value-sniffing rule silently stops working the moment a credential's format
// changes, and this one fails visibly instead (an unredacted parameter is one
// that nobody added to sensitiveQueryParams).
func RedactRequestTarget(target string) string {
	path, query, hasQuery := strings.Cut(target, "?")
	path = redactInvitationPath(path)
	if !hasQuery {
		return path
	}
	return path + "?" + redactQuery(query)
}

// redactInvitationPath replaces the token segment of an invitation URL. The
// token is the entire credential: with it, an unaccepted invitation can be
// accepted and an account created, for the whole 7-day invitationTTL.
func redactInvitationPath(path string) string {
	if !strings.HasPrefix(path, invitationPathPrefix) {
		return path
	}
	rest := strings.TrimPrefix(path, invitationPathPrefix)
	if rest == "" {
		return path
	}
	// Keep whatever follows the token (today only "/accept") so the two routes
	// stay distinguishable in the log.
	token, suffix, hasSuffix := strings.Cut(rest, "/")
	if token == "" {
		return path
	}
	out := invitationPathPrefix + redactedPlaceholder
	if hasSuffix {
		out += "/" + suffix
	}
	return out
}

// redactQuery rewrites the raw query string, replacing the values of
// sensitiveQueryParams. It works on the raw string rather than url.ParseQuery +
// Encode so that a malformed or unusually-ordered query is passed through
// recognisably instead of being silently reordered or dropped — a log line
// should show what was actually sent.
func redactQuery(rawQuery string) string {
	parts := strings.Split(rawQuery, "&")
	for i, p := range parts {
		name, _, hasValue := strings.Cut(p, "=")
		if !hasValue {
			continue
		}
		// Parameter names may be percent-encoded; compare the decoded name so
		// "%74oken=..." cannot slip a credential past the list.
		decoded, err := url.QueryUnescape(name)
		if err != nil {
			decoded = name
		}
		if sensitiveQueryParams[strings.ToLower(decoded)] {
			parts[i] = name + "=" + redactedPlaceholder
		}
	}
	return strings.Join(parts, "&")
}

// redactingLogFormatter wraps chi's DefaultLogFormatter, handing it a shallow
// copy of the request whose target has been redacted. The copy exists only for
// the duration of building the log entry — the real request, with its intact
// query string, is what reaches the handler, so the SSE token lookup and the
// OAuth code exchange are unaffected.
type redactingLogFormatter struct {
	inner middleware.LogFormatter
}

func (f *redactingLogFormatter) NewLogEntry(r *http.Request) middleware.LogEntry {
	redacted := RedactRequestTarget(r.RequestURI)
	if redacted == r.RequestURI {
		return f.inner.NewLogEntry(r)
	}
	rc := *r // shallow copy: shares the context, so the request ID still resolves
	rc.RequestURI = redacted
	if r.URL != nil {
		u := *r.URL
		u.RawQuery = redactQuery(u.RawQuery)
		u.Path = redactInvitationPath(u.Path)
		rc.URL = &u
	}
	return f.inner.NewLogEntry(&rc)
}

// RequestLogger is chi's request logger with URL-borne credentials redacted.
// Use it in place of middleware.Logger; the output is otherwise byte-identical,
// because the redaction happens to the request the formatter reads rather than
// to the formatter itself, and the formatter is constructed exactly as chi's
// own init does it (same writer, same flags, same colour rule — chi additionally
// gates colour on stdout being a TTY, which it is not in a container).
func RequestLogger() func(http.Handler) http.Handler {
	return middleware.RequestLogger(&redactingLogFormatter{
		inner: &middleware.DefaultLogFormatter{
			Logger:  log.New(os.Stdout, "", log.LstdFlags),
			NoColor: runtime.GOOS == "windows",
		},
	})
}
