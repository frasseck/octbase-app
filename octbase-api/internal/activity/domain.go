// Package activity records and retrieves audit log entries for projects and tasks.
// Each entry captures who did what and when; entries are written by other domain
// packages through the ActivityWriter interface and read back via Handler.
package activity

// ActivityEntry domain object. Type and Params let the frontend render a
// localized message via notifications.activity.<type>, interpolating Params.
type ActivityEntry struct {
	ID          string         `json:"id"`
	ProjectID   string         `json:"projectId"`
	TaskID      *string        `json:"taskId"`
	ActorUserID string         `json:"actorUserId"`
	Type        string         `json:"type"`
	Params      map[string]any `json:"params"`
	PayloadJSON string         `json:"-"`
	CreatedAt   string         `json:"createdAt"`
}
