package shared

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/octbase/octbase-api/internal/rbac"
)

func TestGetUserID(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if got := GetUserID(req); got != "" {
		t.Errorf("GetUserID with no context value = %q, want empty", got)
	}
	req = req.WithContext(context.WithValue(req.Context(), UserIDKey, "user-1"))
	if got := GetUserID(req); got != "user-1" {
		t.Errorf("GetUserID = %q, want user-1", got)
	}
}

func TestGetGlobalRole(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if got := GetGlobalRole(req); got != "" {
		t.Errorf("GetGlobalRole with no context value = %q, want empty", got)
	}
	req = req.WithContext(context.WithValue(req.Context(), globalRoleKey, rbac.GlobalAdmin))
	if got := GetGlobalRole(req); got != rbac.GlobalAdmin {
		t.Errorf("GetGlobalRole = %q, want %q", got, rbac.GlobalAdmin)
	}
}

func TestIsValidID(t *testing.T) {
	valid := []string{"00000000-0000-0000-0000-000000000001", "abc123"}
	for _, s := range valid {
		if !IsValidID(s) {
			t.Errorf("IsValidID(%q) = false, want true", s)
		}
	}
	invalid := []string{
		"",                                       // empty
		"this-id-is-way-too-long-to-be-a-uuid-x", // > 36 chars
		"has space",
		"has'quote",
		"has;semicolon",
	}
	for _, s := range invalid {
		if IsValidID(s) {
			t.Errorf("IsValidID(%q) = true, want false", s)
		}
	}
}

func TestRequireOwner(t *testing.T) {
	for _, role := range []string{rbac.ProjectOwner, rbac.ProjectAdmin} {
		if err := RequireOwner(role); err != nil {
			t.Errorf("RequireOwner(%q) = %v, want nil", role, err)
		}
	}
	for _, role := range []string{rbac.ProjectMember, rbac.ProjectViewer, ""} {
		if err := RequireOwner(role); err == nil {
			t.Errorf("RequireOwner(%q) = nil, want error", role)
		}
	}
}

func TestRequireWriter(t *testing.T) {
	for _, role := range []string{rbac.ProjectOwner, rbac.ProjectAdmin, rbac.ProjectMember} {
		if err := RequireWriter(role); err != nil {
			t.Errorf("RequireWriter(%q) = %v, want nil", role, err)
		}
	}
	if err := RequireWriter(rbac.ProjectViewer); err == nil {
		t.Error("RequireWriter(PROJECT_VIEWER) = nil, want error")
	}
}

func TestRequireJSON(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := RequireJSON(next)

	cases := []struct {
		name        string
		method      string
		contentType string
		wantStatus  int
	}{
		{"POST wrong content-type", http.MethodPost, "text/plain", http.StatusUnsupportedMediaType},
		{"POST json content-type", http.MethodPost, "application/json", http.StatusOK},
		{"POST json with charset", http.MethodPost, "application/json; charset=utf-8", http.StatusOK},
		{"POST empty content-type", http.MethodPost, "", http.StatusOK},
		{"GET ignored", http.MethodGet, "text/plain", http.StatusOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(c.method, "/", nil)
			if c.contentType != "" {
				req.Header.Set("Content-Type", c.contentType)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != c.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, c.wantStatus)
			}
		})
	}
}

func TestWriteServerError(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/boom", nil)
	WriteServerError(rec, req, context.DeadlineExceeded)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if got := rec.Body.String(); !strings.Contains(got, "INTERNAL_ERROR") {
		t.Errorf("body = %q, want it to include INTERNAL_ERROR", got)
	}
}

func TestNoCache(t *testing.T) {
	rec := httptest.NewRecorder()
	NoCache(rec)
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if got := rec.Header().Get("Pragma"); got != "no-cache" {
		t.Errorf("Pragma = %q, want no-cache", got)
	}
}

func TestParsePagination_ClampsLargeSize(t *testing.T) {
	p := ParsePagination(fakeRequest("size=9999"))
	if p.Size != 200 {
		t.Errorf("Size = %d, want 200 (clamped)", p.Size)
	}
}

func TestCORSMiddleware_EmptyOriginAllowed(t *testing.T) {
	handler := CORSMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil) // no Origin header
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for empty origin", rec.Code)
	}
}

func TestCORSMiddleware_DisallowedOriginNonPreflightProceeds(t *testing.T) {
	called := false
	handler := CORSMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
	req.Header.Set("Origin", "http://evil.example")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	// A non-preflight request with a disallowed origin is not short-circuited;
	// the browser enforces the missing Allow-Origin. The handler still runs.
	if !called || rec.Code != http.StatusOK {
		t.Fatalf("expected handler to run with 200, got called=%v status=%d", called, rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Allow-Origin = %q, want empty for disallowed origin", got)
	}
}

func TestCORSMiddleware_AllowedOriginPreflight(t *testing.T) {
	t.Setenv("OCTBASE_CORS_ORIGIN", "http://localhost:8080")
	handler := CORSMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("preflight should be short-circuited, handler must not run")
	}))
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/projects", nil)
	req.Header.Set("Origin", "http://localhost:8080")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 for allowed-origin preflight", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:8080" {
		t.Errorf("Allow-Origin = %q, want the request origin", got)
	}
}

func TestRateLimit_WrapperRuns(t *testing.T) {
	handler := RateLimit(1000, time.Minute)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.7:9999"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestRateLimit_NoPortRemoteAddr(t *testing.T) {
	limiter := newIPRateLimiter()
	handler := rateLimitWith(limiter, 1, time.Minute)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	// RemoteAddr without a port exercises the SplitHostPort error fallback.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "no-port-here"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestRateLimit_EvictsStaleEntries(t *testing.T) {
	limiter := newIPRateLimiter()
	window := time.Minute
	// Seed > 10k stale entries so the next miss triggers the eviction branch.
	stale := time.Now().Add(-10 * window)
	for i := 0; i < 10_001; i++ {
		limiter.entries[strconv.Itoa(i)] = &fixedWindowEntry{count: 1, windowAt: stale}
	}
	if !limiter.allow("fresh-ip", 5, window) {
		t.Fatal("allow should return true for a fresh IP")
	}
	if len(limiter.entries) > 10_001 {
		t.Errorf("stale entries were not evicted: %d remain", len(limiter.entries))
	}
}

func TestMessageKeyFor_DoubleUnderscore(t *testing.T) {
	// A code with an empty segment exercises the skip branch.
	if got := MessageKeyFor("FOO__BAR", "x"); got != "errors.fooBar" {
		t.Errorf("MessageKeyFor(FOO__BAR) = %q, want errors.fooBar", got)
	}
}

// TestMessageKeyFor_ValidationNamespace pins the namespace VALIDATION_ERROR
// messages resolve in (OCT-27). These keys had been spelt
// "errors.validation.<name>" while all four locale files carry them at top
// level as "validation.<name>", so t() resolved nothing and every validation
// message rendered its raw English text — in German too. The locale side is
// the one to match: it is what the classic-terminology overlay was written
// against (classic.validation.sprintNameRequired), and re-prefixing here would
// need that overlay moved as well.
//
// scripts/check-error-translations.mjs checks the same agreement against the
// locale files; this test is the cheap Go-side half that fails without a
// Node run.
func TestMessageKeyFor_ValidationNamespace(t *testing.T) {
	// A mapped message, and the fallback for an unmapped one.
	cases := map[string]string{
		"email is required":               "validation.emailRequired",
		"something nobody put in the map": "validation.generic",
	}
	for msg, want := range cases {
		if got := MessageKeyFor("VALIDATION_ERROR", msg); got != want {
			t.Errorf("MessageKeyFor(VALIDATION_ERROR, %q) = %q, want %q", msg, got, want)
		}
	}

	// Every mapped key sits under validation., never errors.validation. —
	// the prefix is the whole bug, so assert it on the map itself rather than
	// on a sample.
	for msg, key := range validationMessageKeys {
		if strings.HasPrefix(key, "errors.") {
			t.Errorf("validationMessageKeys[%q] = %q: validation keys live under validation., not errors.", msg, key)
		}
		if !strings.HasPrefix(key, "validation.") {
			t.Errorf("validationMessageKeys[%q] = %q: want a validation.* key", msg, key)
		}
	}

	// Codes other than VALIDATION_ERROR keep the errors. prefix — this change
	// must not have leaked into the by-code path.
	if got := MessageKeyFor("PROJECT_NOT_FOUND", ""); got != "errors.projectNotFound" {
		t.Errorf("MessageKeyFor(PROJECT_NOT_FOUND) = %q, want errors.projectNotFound", got)
	}
}
