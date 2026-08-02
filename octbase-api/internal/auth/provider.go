// Package auth handles authentication: JWT issuance/validation, email+password
// login, refresh token rotation, invitation flow, and the Provider interface
// that lets the implementation be swapped for SAML/OIDC later.
package auth

import "context"

// User is the authenticated principal returned by a successful login.
type User struct {
	ID         string
	Email      string
	Name       string
	GlobalRole string
	IsActive   bool
}

// Provider is the authentication abstraction. Swap the implementation for
// SAML/OIDC without touching callers.
type Provider interface {
	// Login validates credentials and returns the user on success.
	Login(ctx context.Context, email, password string) (*User, error)
	// ValidateToken parses a JWT access token and returns the user ID.
	ValidateToken(token string) (userID string, err error)
}
