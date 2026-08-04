package shared

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5/middleware"
)

// The route that started this: the SSE line shape lifted from the dev stack's
// container log on 2026-08-04, where a complete and then-valid access token sat
// in the query string.
//
// The token itself is a placeholder on purpose. A real JWT pasted into a test
// fixture is a credential committed to the repository — the secret scanner is
// right to reject it, and "add it to .gitleaksignore" would be suppressing the
// tool that is doing its job. Nothing in this test depends on the value being a
// real token: redaction keys off the parameter NAME, so the fixture only needs
// to be long, distinctive and full of the characters a JWT would carry (dots,
// dashes, underscores) so that a redaction which mangles the rest of the line
// still shows up.
const placeholderToken = "ACCESS.TOKEN-PLACEHOLDER_NOT-A-REAL-CREDENTIAL"

const observedSSETarget = "/api/v1/projects/4ee55edc-1646-4d35-8d3e-67f0065c61c9/events?token=" + placeholderToken

func TestRedactRequestTarget(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "sse token, the observed leak",
			in:   observedSSETarget,
			want: "/api/v1/projects/4ee55edc-1646-4d35-8d3e-67f0065c61c9/events?token=REDACTED",
		},
		{
			name: "a jwt-shaped value is replaced whole, dots and all",
			in:   "/api/v1/projects/p1/events?token=aaa.bbb-ccc_ddd.eee",
			want: "/api/v1/projects/p1/events?token=REDACTED",
		},
		{
			name: "oauth callback code and state",
			in:   "/api/v1/oauth/github/callback?code=abc123secret&state=onetimestate",
			want: "/api/v1/oauth/github/callback?code=REDACTED&state=REDACTED",
		},
		{
			name: "invitation token lives in the path",
			in:   "/api/v1/invitations/9f3c1b7e5a2d4c8f",
			want: "/api/v1/invitations/REDACTED",
		},
		{
			name: "invitation accept keeps the action visible",
			in:   "/api/v1/invitations/9f3c1b7e5a2d4c8f/accept",
			want: "/api/v1/invitations/REDACTED/accept",
		},
		{
			name: "harmless parameters survive untouched",
			in:   "/api/v1/projects/p1/tasks?page=0&size=200&sortBy=priority&order=asc",
			want: "/api/v1/projects/p1/tasks?page=0&size=200&sortBy=priority&order=asc",
		},
		{
			name: "only the sensitive parameter is replaced",
			in:   "/api/v1/projects/p1/events?projectId=p1&token=secret&size=20",
			want: "/api/v1/projects/p1/events?projectId=p1&token=REDACTED&size=20",
		},
		{
			name: "no query string",
			in:   "/api/v1/health",
			want: "/api/v1/health",
		},
		{
			name: "percent-encoded parameter name does not evade the list",
			in:   "/api/v1/projects/p1/events?%74oken=secret",
			want: "/api/v1/projects/p1/events?%74oken=REDACTED",
		},
		{
			name: "uppercase parameter name does not evade the list",
			in:   "/api/v1/projects/p1/events?TOKEN=secret",
			want: "/api/v1/projects/p1/events?TOKEN=REDACTED",
		},
		{
			name: "valueless parameter is left alone",
			in:   "/api/v1/projects/p1/tasks?token",
			want: "/api/v1/projects/p1/tasks?token",
		},
		{
			name: "an empty value stays empty rather than gaining a placeholder",
			in:   "/api/v1/projects/p1/events?token=",
			want: "/api/v1/projects/p1/events?token=REDACTED",
		},
		{
			name: "a path that merely mentions invitations is not touched",
			in:   "/api/v1/admin/invitations",
			want: "/api/v1/admin/invitations",
		},
		{
			name: "absolute-form target",
			in:   "http://octbase.example/api/v1/projects/p1/events?token=secret",
			want: "http://octbase.example/api/v1/projects/p1/events?token=REDACTED",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := RedactRequestTarget(c.in); got != c.want {
				t.Errorf("RedactRequestTarget(%q)\n got %q\nwant %q", c.in, got, c.want)
			}
		})
	}
}

// The guarantee that actually matters: the token must be absent from what the
// logger writes, and present in what the handler reads. A test that only checked
// the string helper would pass even if the middleware were never wired up.
func TestRequestLoggerRedactsTokenButHandlerStillSeesIt(t *testing.T) {
	var logged bytes.Buffer
	formatter := &redactingLogFormatter{
		inner: &middleware.DefaultLogFormatter{
			Logger:  log.New(&logged, "", 0),
			NoColor: true,
		},
	}

	var seenByHandler string
	h := middleware.RequestLogger(formatter)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenByHandler = r.URL.Query().Get("token")
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, observedSSETarget, nil)
	req.RequestURI = observedSSETarget
	h.ServeHTTP(httptest.NewRecorder(), req)

	out := logged.String()
	if strings.Contains(out, placeholderToken) {
		t.Errorf("access token leaked into the request log:\n%s", out)
	}
	if !strings.Contains(out, "token=REDACTED") {
		t.Errorf("expected a redacted token in the log line, got:\n%s", out)
	}
	if !strings.Contains(out, "/events") {
		t.Errorf("redaction ate the route, leaving an unusable log line:\n%s", out)
	}
	if seenByHandler != placeholderToken {
		t.Errorf("handler must still receive the real token, got %q", seenByHandler)
	}
}

// A request with nothing to redact must take the untouched path and log exactly
// what chi would have logged.
func TestRequestLoggerLeavesOrdinaryRequestsAlone(t *testing.T) {
	var redacted, plain bytes.Buffer
	target := "/api/v1/projects/p1/tasks?page=0&size=200"

	run := func(f middleware.LogFormatter) {
		h := middleware.RequestLogger(f)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.RequestURI = target
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	run(&redactingLogFormatter{inner: &middleware.DefaultLogFormatter{Logger: log.New(&redacted, "", 0), NoColor: true}})
	run(&middleware.DefaultLogFormatter{Logger: log.New(&plain, "", 0), NoColor: true})

	// Timings differ per run; compare everything up to " in ".
	trim := func(s string) string { return strings.SplitN(s, " in ", 2)[0] }
	if trim(redacted.String()) != trim(plain.String()) {
		t.Errorf("redacting logger changed an ordinary log line\n got %q\nwant %q",
			trim(redacted.String()), trim(plain.String()))
	}
}
