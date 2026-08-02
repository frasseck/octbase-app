// Package identityaccess manages users and their project memberships.
package identityaccess

// User represents an application user (public fields only, no password_hash).
type User struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"displayName"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
	// AvatarUpdatedAt is non-nil when the user has uploaded a profile picture;
	// its value is the client's cache-busting token for GET /users/{id}/avatar.
	AvatarUpdatedAt *string `json:"avatarUpdatedAt,omitempty"`
}

// Membership represents a user's membership in a project.
type Membership struct {
	ID               string `json:"id"`
	ProjectID        string `json:"projectId"`
	UserID           string `json:"userId"`
	Role             string `json:"role"`
	AssignedByUserID string `json:"assignedByUserId,omitempty"`
	CreatedAt        string `json:"createdAt"`
	UpdatedAt        string `json:"updatedAt"`
}

// Project-level roles — canonical names after migration 010.
const (
	RoleProjectOwner  = "PROJECT_OWNER"
	RoleProjectAdmin  = "PROJECT_ADMIN"
	RoleProjectMember = "PROJECT_MEMBER"
	RoleProjectViewer = "PROJECT_VIEWER"
)

// AssignableUser is a candidate for a task assignment or review on one project:
// either a member of that project, or a global ADMIN/SUPER_ADMIN, who reaches
// every project without holding a membership row and would otherwise be absent
// from the pickers. Role is the project role and is empty for the latter;
// Member says which of the two a row is.
type AssignableUser struct {
	UserID          string  `json:"userId"`
	Name            string  `json:"name"`
	Email           string  `json:"email"`
	Role            string  `json:"role"`
	GlobalRole      string  `json:"globalRole"`
	Member          bool    `json:"member"`
	AvatarUpdatedAt *string `json:"avatarUpdatedAt,omitempty"`
}

// MemberWithUser combines membership data with the user's profile for list views.
type MemberWithUser struct {
	ID              string  `json:"id"`
	ProjectID       string  `json:"projectId"`
	UserID          string  `json:"userId"`
	Name            string  `json:"name"`
	Email           string  `json:"email"`
	Role            string  `json:"role"`
	JoinedAt        string  `json:"joinedAt"`
	AvatarUpdatedAt *string `json:"avatarUpdatedAt,omitempty"`
}
