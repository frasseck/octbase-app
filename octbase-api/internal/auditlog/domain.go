// Package auditlog records Super-Admin actions for compliance and traceability.
package auditlog

import "time"

// AuditLog is a single immutable audit record.
type AuditLog struct {
	ID           string    `json:"id"`
	ActorUserID  string    `json:"actorUserId"`
	Action       string    `json:"action"`
	TargetType   string    `json:"targetType"`
	TargetID     string    `json:"targetId"`
	MetadataJSON string    `json:"metadata"`
	IPAddress    string    `json:"ipAddress"`
	UserAgent    string    `json:"userAgent"`
	CreatedAt    time.Time `json:"createdAt"`
}

// Action constants — kept here so callers import a single package.
const (
	ActionLoginSuccess                = "LOGIN_SUCCESS"
	ActionLoginFailed                 = "LOGIN_FAILED"
	ActionRefreshReuse                = "REFRESH_TOKEN_REUSE"
	ActionUserCreated                 = "USER_CREATED"
	ActionUserUpdated                 = "USER_UPDATED"
	ActionUserDisabled                = "USER_DISABLED"
	ActionUserEnabled                 = "USER_ENABLED"
	ActionUserRoleChanged             = "USER_ROLE_CHANGED"
	ActionUserEmailChanged            = "USER_EMAIL_CHANGED"
	ActionUserDeleted                 = "USER_DELETED"
	ActionUserPasswordReset           = "USER_PASSWORD_RESET"
	ActionUserPasswordChanged         = "USER_PASSWORD_CHANGED"
	ActionUserPasswordChangeFailed    = "USER_PASSWORD_CHANGE_FAILED"
	ActionProjectCreated              = "PROJECT_CREATED"
	ActionProjectUpdated              = "PROJECT_UPDATED"
	ActionProjectArchived             = "PROJECT_ARCHIVED"
	ActionProjectUnarchived           = "PROJECT_UNARCHIVED"
	ActionProjectDeleted              = "PROJECT_DELETED"
	ActionMemberAdded                 = "PROJECT_MEMBER_ADDED"
	ActionMemberRoleChanged           = "PROJECT_MEMBER_ROLE_CHANGED"
	ActionMemberRemoved               = "PROJECT_MEMBER_REMOVED"
	ActionTaskDeleted                 = "TASK_DELETED"
	ActionMFAEnabled                  = "MFA_ENABLED"
	ActionMFADisabled                 = "MFA_DISABLED"
	ActionMFARecoveryCodesRegenerated = "MFA_RECOVERY_CODES_REGENERATED"
)
