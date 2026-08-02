package shared

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIsValidUUID_Valid(t *testing.T) {
	valid := []string{
		"00000000-0000-0000-0000-000000000000",
		"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		"AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE",
		"12345678-1234-1234-1234-123456789abc",
	}
	for _, s := range valid {
		if !IsValidUUID(s) {
			t.Errorf("IsValidUUID(%q) = false, want true", s)
		}
	}
}

func TestIsValidUUID_Invalid(t *testing.T) {
	invalid := []string{
		"",
		"not-a-uuid",
		"00000000-0000-0000-0000-00000000000",   // too short
		"00000000-0000-0000-0000-0000000000000", // too long
		"00000000 0000 0000 0000 000000000000",  // spaces instead of dashes
		"gggggggg-0000-0000-0000-000000000000",  // invalid hex chars
		"00000000-0000-0000-0000-00000000000g",  // 'g' not hex
	}
	for _, s := range invalid {
		if IsValidUUID(s) {
			t.Errorf("IsValidUUID(%q) = true, want false", s)
		}
	}
}

func TestNewUUID_Format(t *testing.T) {
	id := NewUUID()
	if !IsValidUUID(id) {
		t.Errorf("NewUUID() = %q, not a valid UUID", id)
	}
	if len(id) != 36 {
		t.Errorf("NewUUID() length = %d, want 36", len(id))
	}
	if id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		t.Errorf("NewUUID() dashes at wrong positions: %q", id)
	}
}

func TestNewUUID_Unique(t *testing.T) {
	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := NewUUID()
		if ids[id] {
			t.Fatalf("NewUUID() returned duplicate: %q", id)
		}
		ids[id] = true
	}
}

func TestNow_RFC3339Format(t *testing.T) {
	ts := Now()
	if ts == "" {
		t.Error("Now() returned empty string")
	}
	// RFC3339 looks like "2006-01-02T15:04:05Z"
	if len(ts) < 20 {
		t.Errorf("Now() = %q looks too short for RFC3339", ts)
	}
	if !strings.Contains(ts, "T") {
		t.Errorf("Now() = %q missing T separator", ts)
	}
	if !strings.HasSuffix(ts, "Z") {
		t.Errorf("Now() = %q should end with Z (UTC)", ts)
	}
}

func TestParsePagination_Defaults(t *testing.T) {
	// Create a minimal request with no query params.
	req := fakeRequest("")
	p := ParsePagination(req)
	if p.Page != 0 {
		t.Errorf("Page = %d, want 0", p.Page)
	}
	if p.Size != 20 {
		t.Errorf("Size = %d, want 20 (default)", p.Size)
	}
}

func TestParsePagination_CustomValues(t *testing.T) {
	req := fakeRequest("page=2&size=50")
	p := ParsePagination(req)
	if p.Page != 2 {
		t.Errorf("Page = %d, want 2", p.Page)
	}
	if p.Size != 50 {
		t.Errorf("Size = %d, want 50", p.Size)
	}
}

func TestParsePagination_NegativePage(t *testing.T) {
	req := fakeRequest("page=-5&size=10")
	p := ParsePagination(req)
	if p.Page != 0 {
		t.Errorf("Page = %d, want 0 (clamped from negative)", p.Page)
	}
}

func TestParsePagination_ZeroSize(t *testing.T) {
	req := fakeRequest("page=0&size=0")
	p := ParsePagination(req)
	if p.Size != 20 {
		t.Errorf("Size = %d, want 20 (default when zero)", p.Size)
	}
}

func TestParsePagination_InvalidValues(t *testing.T) {
	req := fakeRequest("page=abc&size=xyz")
	p := ParsePagination(req)
	if p.Page != 0 {
		t.Errorf("Page = %d, want 0 for invalid input", p.Page)
	}
	if p.Size != 20 {
		t.Errorf("Size = %d, want 20 for invalid input", p.Size)
	}
}

func TestCORSMiddleware_RejectsNullOrigin(t *testing.T) {
	handler := CORSMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/projects", nil)
	req.Header.Set("Origin", "null")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// null origin (sandboxed iframes, file://) must be rejected to prevent
	// credentialed cross-origin requests from arbitrary sandboxed contexts.
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d (null origin should be rejected)", rec.Code, http.StatusForbidden)
	}
}

func TestCORSMiddleware_SetsAllowCredentials(t *testing.T) {
	t.Setenv("OCTBASE_CORS_ORIGIN", "http://localhost:8080")
	handler := CORSMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
	req.Header.Set("Origin", "http://localhost:8080")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("Access-Control-Allow-Credentials = %q, want %q", got, "true")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got == "*" || got == "" {
		t.Fatalf("Access-Control-Allow-Origin = %q, must be a specific origin with credentials", got)
	}
}

func TestCORSMiddleware_RejectsUnexpectedOriginPreflight(t *testing.T) {
	handler := CORSMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/projects", nil)
	req.Header.Set("Origin", "http://evil.example")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestSecurityHeaders_Set(t *testing.T) {
	handler := SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	checks := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
	}
	for header, want := range checks {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}

func TestRateLimit_AllowsUnderLimit(t *testing.T) {
	limiter := newIPRateLimiter()
	handler := rateLimitWith(limiter, 5, time.Minute)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200", i+1, rec.Code)
		}
	}
}

func TestRateLimit_BlocksOverLimit(t *testing.T) {
	limiter := newIPRateLimiter()
	handler := rateLimitWith(limiter, 3, time.Minute)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.0.0.2:5678"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}
	// 4th request should be rate limited.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.2:5678"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
}

func TestRateLimit_IndependentIPs(t *testing.T) {
	limiter := newIPRateLimiter()
	handler := rateLimitWith(limiter, 2, time.Minute)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	// Exhaust limit for IP A.
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "1.2.3.4:1"
		httptest.NewRecorder()
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}
	// IP B should still be allowed.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "5.6.7.8:1"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("different IP blocked unexpectedly: status = %d", rec.Code)
	}
}

func TestDecodeJSON_BodyTooLarge(t *testing.T) {
	// Build a JSON body that exceeds the 1 MiB limit.
	big := make([]byte, jsonBodyLimit+1)
	big[0] = '"'
	for i := 1; i < len(big)-1; i++ {
		big[i] = 'a'
	}
	big[len(big)-1] = '"'

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(big)))
	var v interface{}
	if err := DecodeJSON(req, &v); err == nil {
		t.Fatal("expected error for oversized body, got nil")
	}
}

func TestWriteError_IncludesMessageKey(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, http.StatusNotFound, "PROJECT_NOT_FOUND", "project not found")

	var body ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != "PROJECT_NOT_FOUND" {
		t.Errorf("Code = %q, want %q", body.Code, "PROJECT_NOT_FOUND")
	}
	if body.Message != "project not found" {
		t.Errorf("Message = %q, want %q", body.Message, "project not found")
	}
	if want := "errors.projectNotFound"; body.MessageKey != want {
		t.Errorf("MessageKey = %q, want %q", body.MessageKey, want)
	}
}

func TestWriteValidationError_IncludesMessageKey(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteValidationError(rec, "VALIDATION_ERROR", "displayName is required", "displayName")

	var body ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if want := "errors.validation.displayNameRequired"; body.MessageKey != want {
		t.Errorf("MessageKey = %q, want %q", body.MessageKey, want)
	}
	details, ok := body.Details.(map[string]interface{})
	if !ok {
		t.Fatalf("Details = %#v, want map", body.Details)
	}
	if details["field"] != "displayName" {
		t.Errorf("Details[field] = %v, want %q", details["field"], "displayName")
	}
}

func TestWriteValidationError_UnknownMessageFallsBackToGeneric(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteValidationError(rec, "VALIDATION_ERROR", "some unmapped message", "field")

	var body ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if want := "errors.validation.generic"; body.MessageKey != want {
		t.Errorf("MessageKey = %q, want %q", body.MessageKey, want)
	}
}

// fakeRequest constructs a minimal *http.Request with the given raw query string.
func fakeRequest(rawQuery string) *http.Request {
	req, _ := http.NewRequest("GET", "/?"+rawQuery, nil)
	return req
}
