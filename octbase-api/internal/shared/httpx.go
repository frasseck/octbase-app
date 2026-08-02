package shared

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
)

const jsonBodyLimit = 1 << 20 // 1 MiB

type contextKey string

const UserIDKey contextKey = "userID"

// mfaEnrollmentTokenKey marks a request that authenticated via a scoped MFA
// *enrollment* token (issued right after a successful password login for the
// forced-enrollment flow) rather than a full access token. Handlers use it to
// skip a redundant password re-auth on the forced-enrollment path while still
// requiring it for access-token callers.
const mfaEnrollmentTokenKey contextKey = "mfaEnrollmentToken"

// WithMFAEnrollmentToken marks the request as authenticated via an MFA
// enrollment token.
func WithMFAEnrollmentToken(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), mfaEnrollmentTokenKey, true))
}

// IsMFAEnrollmentToken reports whether the request authenticated via an MFA
// enrollment token (vs a normal access token).
func IsMFAEnrollmentToken(r *http.Request) bool {
	v, _ := r.Context().Value(mfaEnrollmentTokenKey).(bool)
	return v
}

// WriteJSON writes a JSON response with the given status code.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteError writes a standard error JSON response. The response includes a
// stable messageKey (derived via MessageKeyFor) alongside the English
// code/message, so the frontend can localize the error.
func WriteError(w http.ResponseWriter, status int, code, message string) {
	WriteJSON(w, status, ErrorResponse{Code: code, Message: message, MessageKey: MessageKeyFor(code, message)})
}

// RequireFound writes a 404 with the given stable code and returns false when
// found is false. It is the standard mapping for parent-scoped sub-resource
// lookups and deletes: the repo/service call is scoped to the guarded parent
// (WHERE id=… AND parent=…), so a child that belongs to a different parent is
// indistinguishable from one that never existed — the ownership guard for
// "child belongs to guarded parent" lives in the query, and handlers only map
// the miss:
//
//	if !shared.RequireFound(w, deleted, "TASK_LINK_NOT_FOUND", "link not found") {
//		return
//	}
func RequireFound(w http.ResponseWriter, found bool, code, message string) bool {
	if found {
		return true
	}
	WriteError(w, http.StatusNotFound, code, message)
	return false
}

// WriteValidationError writes a 422 error response whose Details include the
// name of the offending request/form field, so the frontend can associate
// the message with the correct input (WCAG 3.3.1 Error Identification).
func WriteValidationError(w http.ResponseWriter, code, message, field string) {
	WriteJSON(w, http.StatusUnprocessableEntity, ErrorResponse{
		Code: code, Message: message, MessageKey: MessageKeyFor(code, message), Details: map[string]string{"field": field},
	})
}

// WriteUpdateError writes the response for a failed version-guarded update:
// 409 VERSION_CONFLICT when the entity was changed (or deleted) since the
// caller's snapshot was read, otherwise a 500.
func WriteUpdateError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, ErrVersionConflict) {
		WriteError(w, http.StatusConflict, "VERSION_CONFLICT", "the item was changed by someone else; reload and try again")
		return
	}
	WriteServerError(w, r, err)
}

// DecodeJSON decodes the request body into v. Rejects bodies larger than 1 MiB.
func DecodeJSON(r *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(r.Body, jsonBodyLimit)).Decode(v)
}

// DecodeJSONOrBadRequest decodes the request body into v, writing the
// standard 400 BAD_REQUEST response and returning false on failure. Callers
// should return immediately when this returns false:
//
//	if !shared.DecodeJSONOrBadRequest(w, r, &req) {
//		return
//	}
func DecodeJSONOrBadRequest(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := DecodeJSON(r, v); err != nil {
		WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON")
		return false
	}
	return true
}

// GetUserID retrieves the user ID from the request context.
func GetUserID(r *http.Request) string {
	v, _ := r.Context().Value(UserIDKey).(string)
	return v
}

// ParsePagination parses page and size query params with sane defaults.
func ParsePagination(r *http.Request) PaginationParams {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	size, _ := strconv.Atoi(r.URL.Query().Get("size"))
	if size <= 0 {
		size = 20
	}
	if size > 200 {
		size = 200
	}
	if page < 0 {
		page = 0
	}
	return PaginationParams{Page: page, Size: size}
}

// corsAllowedOrigin returns the configured allowed CORS origin.
func corsAllowedOrigin() string {
	if o := os.Getenv("OCTBASE_CORS_ORIGIN"); o != "" {
		return o
	}
	return "http://localhost:8080"
}

// CORSHeaders sets CORS headers restricted to the provided allowed origin.
// Access-Control-Allow-Credentials is required because the SPA sends every
// request with credentials:'include' (for the HttpOnly refresh cookie); without
// it, browsers block cross-origin responses — which breaks the standalone
// file:// UI and any frontend served from a different origin than the API. It is
// safe to send because `origin` is always a specific value here, never "*".
func CORSHeaders(w http.ResponseWriter, origin string) {
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	w.Header().Set("Access-Control-Allow-Credentials", "true")
	w.Header().Set("Vary", "Origin")
}

func corsResponseOrigin(requestOrigin, allowedOrigin string) (string, bool) {
	switch requestOrigin {
	case "":
		return allowedOrigin, true
	case allowedOrigin:
		return requestOrigin, true
	default:
		// "null" origin (file://, sandboxed iframes) is intentionally rejected.
		// Set OCTBASE_CORS_ORIGIN to match the actual frontend host in production.
		return "", false
	}
}

// CORSMiddleware sets CORS headers on every response and short-circuits
// OPTIONS preflight requests. Register this at the top-level router so it
// runs before any route-specific middleware.
func CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		allowedOrigin := corsAllowedOrigin()
		origin := r.Header.Get("Origin")
		responseOrigin, ok := corsResponseOrigin(origin, allowedOrigin)
		if !ok && r.Method == http.MethodOptions {
			WriteError(w, http.StatusForbidden, "CORS_FORBIDDEN", "origin not allowed")
			return
		}
		CORSHeaders(w, responseOrigin)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireJSON is middleware that rejects POST/PATCH/PUT requests whose
// Content-Type header is present but not application/json.
func RequireJSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost || r.Method == http.MethodPatch || r.Method == http.MethodPut {
			ct := r.Header.Get("Content-Type")
			if ct != "" && !strings.HasPrefix(ct, "application/json") {
				WriteError(w, http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE",
					"Content-Type must be application/json")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// WriteServerError logs err and writes a generic 500 response.
func WriteServerError(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("request failed", "method", r.Method, "path", r.URL.Path, "error", err)
	WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "an internal error occurred")
}

// SecurityHeaders sets conservative security headers on every response.
// HSTS is omitted here; set it in the TLS-terminating reverse proxy only.
//
// The Content-Security-Policy is deliberately strict ("default-src 'none'"):
// every response this API emits is JSON, which a browser never executes, so a
// locked-down policy costs nothing and provides defense-in-depth against any
// HTML that might slip into a response. The one HTML route (/docs, Swagger UI)
// overrides this header with a route-specific policy.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "geolocation=(), camera=(), microphone=()")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'")
		next.ServeHTTP(w, r)
	})
}

// NoCache sets Cache-Control: no-store on sensitive responses (auth, tokens).
func NoCache(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
}
