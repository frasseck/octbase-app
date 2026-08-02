// Package usermgmt provides Super-Admin user-management endpoints.
package usermgmt

import "time"

// ManagedUser is the representation returned by management endpoints.
// password_hash is never included.
type ManagedUser struct {
	ID          string     `json:"id"`
	Email       string     `json:"email"`
	DisplayName string     `json:"displayName"`
	GlobalRole  string     `json:"globalRole"`
	Status      string     `json:"status"`
	CreatedAt   string     `json:"createdAt"`
	UpdatedAt   string     `json:"updatedAt"`
	LastLoginAt *time.Time `json:"lastLoginAt"`
	// AvatarUpdatedAt is non-nil when the user has a profile picture; its value
	// is the cache-busting token for GET /users/{id}/avatar.
	AvatarUpdatedAt *string `json:"avatarUpdatedAt,omitempty"`
}
