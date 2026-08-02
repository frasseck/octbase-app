// Package activity records and retrieves audit log entries for projects and tasks.
// Each entry captures who did what and when; entries are written by other domain
// packages through the ActivityWriter interface and read back via Handler.
package activity

// ActivityEntry domain object. Type and Params let the frontend render a
// localized message via notifications.activity.<type>, interpolating Params.
//
// TaskID, ReleaseID and SprintID are the entry's reference to what it describes;
// at most one is ever set, and deleting that thing nulls it and sets
// TargetDeleted. An entry with TargetDeleted therefore reads "this happened to
// something that no longer exists" — the frontend renders it greyed out and
// without a link, rather than offering a click that opens nothing. A NULL
// reference with TargetDeleted false is an entry that never had one (a
// project-level event such as PAGE_PUBLISHED), which renders normally.
type ActivityEntry struct {
	ID            string         `json:"id"`
	ProjectID     string         `json:"projectId"`
	TaskID        *string        `json:"taskId"`
	ReleaseID     *string        `json:"releaseId"`
	SprintID      *string        `json:"sprintId"`
	TargetDeleted bool           `json:"targetDeleted"`
	ActorUserID   string         `json:"actorUserId"`
	Type          string         `json:"type"`
	Params        map[string]any `json:"params"`
	PayloadJSON   string         `json:"-"`
	CreatedAt     string         `json:"createdAt"`
}
