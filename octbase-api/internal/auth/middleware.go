package auth

import (
	"context"
	"net/http"
	"strings"

	"github.com/octbase/octbase-api/internal/shared"
)

// JWTMiddleware validates the Bearer token in the Authorization header and
// stores the user ID in the request context. It rejects requests without a
// valid token with 401.
func JWTMiddleware(provider Provider) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := bearerToken(r)
			if token == "" {
				shared.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Bearer token required")
				return
			}
			userID, err := provider.ValidateToken(token)
			if err != nil {
				shared.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid or expired token")
				return
			}
			ctx := context.WithValue(r.Context(), shared.UserIDKey, userID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// EnrollmentOrAccessMiddleware guards the MFA enroll/confirm endpoints. It
// accepts EITHER a normal access token (a logged-in user enrolling voluntarily)
// OR a scoped MFA-enrollment token (a user forced to enroll at login because
// the deployment requires MFA). Both resolve to a user ID in the context; the
// enrollment token grants nothing beyond these two endpoints because no other
// route uses this middleware. secret is the JWT signing secret.
func EnrollmentOrAccessMiddleware(provider Provider, secret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := bearerToken(r)
			if token == "" {
				shared.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Bearer token required")
				return
			}
			viaEnrollment := false
			userID, err := provider.ValidateToken(token)
			if err != nil {
				// Not a valid access token — try an MFA-enrollment token.
				userID, err = ParseMFAEnrollmentToken(token, secret)
				if err != nil {
					shared.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid or expired token")
					return
				}
				viaEnrollment = true
			}
			ctx := context.WithValue(r.Context(), shared.UserIDKey, userID)
			rr := r.WithContext(ctx)
			if viaEnrollment {
				rr = shared.WithMFAEnrollmentToken(rr)
			}
			next.ServeHTTP(w, rr)
		})
	}
}

// OptionalJWTMiddleware sets the user ID if a valid token is present but does
// not reject unauthenticated requests. Used for public endpoints that benefit
// from knowing the caller.
func OptionalJWTMiddleware(provider Provider) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := bearerToken(r)
			if token != "" {
				if userID, err := provider.ValidateToken(token); err == nil {
					ctx := context.WithValue(r.Context(), shared.UserIDKey, userID)
					r = r.WithContext(ctx)
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(h, "Bearer ")
}
