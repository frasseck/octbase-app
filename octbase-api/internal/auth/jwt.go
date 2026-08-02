package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ErrTokenExpired is returned when a JWT access token has expired.
var ErrTokenExpired = errors.New("token expired")

// ErrTokenInvalid is returned for any other JWT validation failure.
var ErrTokenInvalid = errors.New("token invalid")

const jwtIssuer = "octbase-api"

// mfaChallengeIssuer marks a short-lived MFA-challenge token as distinct from
// a normal access token. Both IssueAccessToken/ParseAccessToken and
// IssueMFAChallengeToken/ParseMFAChallengeToken validate the "iss" claim
// (jwt.WithIssuer), so a challenge token issued with this issuer is rejected
// by JWTMiddleware (wrong issuer for a bearer token) and a normal access
// token is rejected by ParseMFAChallengeToken (wrong issuer for a challenge)
// — no server-side session state is needed to keep the two kinds apart.
const mfaChallengeIssuer = "octbase-api-mfa-challenge"

// mfaEnrollmentIssuer marks a token that proves "this caller authenticated with
// a correct password for userID, but the deployment requires MFA and the
// account has none yet". It is scoped to the MFA enroll/confirm endpoints only
// (see EnrollmentOrAccessMiddleware) and grants no API or session access — the
// user must log in again once MFA is active. Kept distinct from the challenge
// issuer so an enrollment token cannot be replayed at /auth/mfa/verify.
const mfaEnrollmentIssuer = "octbase-api-mfa-enrollment"

// issueToken creates a signed JWT carrying only the registered claims
// (subject = userID, issuer, issued-at, expiry). Both IssueAccessToken and
// IssueMFAChallengeToken are thin wrappers around this with different
// issuer/TTL values — the distinct issuer alone is enough to scope what each
// kind of token can be used for, so no extra custom claims are needed.
func issueToken(userID, secret, issuer string, ttl time.Duration) (string, error) {
	now := time.Now()
	c := jwt.RegisteredClaims{
		Issuer:    issuer,
		Subject:   userID,
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, c)
	signed, err := t.SignedString([]byte(secret))
	if err != nil {
		return "", fmt.Errorf("sign jwt: %w", err)
	}
	return signed, nil
}

// parseToken validates a JWT signed by issueToken and checks it carries the
// expected issuer, returning its registered claims.
func parseToken(tokenStr, secret, issuer string) (*jwt.RegisteredClaims, error) {
	t, err := jwt.ParseWithClaims(tokenStr, &jwt.RegisteredClaims{},
		func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
			}
			return []byte(secret), nil
		},
		jwt.WithIssuedAt(),
		jwt.WithIssuer(issuer),
	)
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrTokenExpired
		}
		return nil, ErrTokenInvalid
	}
	c, ok := t.Claims.(*jwt.RegisteredClaims)
	if !ok || !t.Valid {
		return nil, ErrTokenInvalid
	}
	return c, nil
}

// IssueMFAChallengeToken creates a short-lived, single-purpose token proving
// "this caller already presented a correct password for userID" without
// granting API access — the second factor must still be verified at
// POST /api/v1/auth/mfa/verify before a real access/refresh pair is issued.
func IssueMFAChallengeToken(userID, secret string, ttl time.Duration) (string, error) {
	return issueToken(userID, secret, mfaChallengeIssuer, ttl)
}

// ParseMFAChallengeToken validates a challenge token and returns the subject
// (user ID). Rejects a normal access token (different issuer) just as
// ParseAccessToken rejects a challenge token.
func ParseMFAChallengeToken(tokenStr, secret string) (string, error) {
	c, err := parseToken(tokenStr, secret, mfaChallengeIssuer)
	if err != nil {
		return "", err
	}
	return c.Subject, nil
}

// IssueMFAEnrollmentToken creates a short-lived token that authorizes only the
// MFA enroll/confirm endpoints for userID, used to force enrollment when the
// deployment requires MFA and the account has none. It is not an access token
// and does not carry a session.
func IssueMFAEnrollmentToken(userID, secret string, ttl time.Duration) (string, error) {
	return issueToken(userID, secret, mfaEnrollmentIssuer, ttl)
}

// ParseMFAEnrollmentToken validates an enrollment token and returns the subject
// (user ID). Rejects access and challenge tokens (different issuers).
func ParseMFAEnrollmentToken(tokenStr, secret string) (string, error) {
	c, err := parseToken(tokenStr, secret, mfaEnrollmentIssuer)
	if err != nil {
		return "", err
	}
	return c.Subject, nil
}

// IssueAccessToken creates a signed JWT with the given TTL.
func IssueAccessToken(userID, secret string, ttl time.Duration) (string, error) {
	return issueToken(userID, secret, jwtIssuer, ttl)
}

// ParseAccessToken validates a JWT and returns the subject (user ID).
func ParseAccessToken(tokenStr, secret string) (string, error) {
	c, err := parseToken(tokenStr, secret, jwtIssuer)
	if err != nil {
		return "", err
	}
	return c.Subject, nil
}
