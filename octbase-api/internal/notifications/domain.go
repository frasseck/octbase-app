// Package notifications manages in-app and email notifications.
package notifications

// Notification domain object.
type Notification struct {
	ID        string  `json:"id"`
	UserID    string  `json:"userId"`
	Kind      string  `json:"kind"`
	ProjectID *string `json:"projectId,omitempty"`
	TaskID    *string `json:"taskId,omitempty"`
	PageID    *string `json:"pageId,omitempty"`
	Message   string  `json:"message"`
	IsRead    bool    `json:"isRead"`
	CreatedAt string  `json:"createdAt"`
}

// NotificationPreference stores per-user, per-kind delivery settings.
type NotificationPreference struct {
	UserID string `json:"userId"`
	Kind   string `json:"kind"`
	InApp  bool   `json:"inApp"`
	Email  bool   `json:"email"`
}

// Notification kind constants.
//
// A kind belongs here only once something emits it. `release_due` was declared,
// given a subject line and a preference toggle, and never emitted by anything —
// there is no scheduler, and a release has no owner, so "a release you own is
// due" was not even expressible. It was retired on 2026-07-31 rather than
// implemented (Lars's call); ValidKind below is what keeps a retired or
// misspelled kind from being written back into the preferences table.
const (
	KindTaskAssigned  = "task_assigned"
	KindReviewerSet   = "reviewer_set"
	KindMentioned     = "mentioned"
	KindStatusChanged = "status_changed"
	// KindTaskChanged is an email-only notification sent to a task's reporter and
	// assignee summarizing edits made by someone else. Email defaults to ON.
	KindTaskChanged = "task_changed"
)

// ValidKind reports whether kind is a notification this build actually sends.
// The preferences endpoint used to accept any string, so a client could store a
// preference for a kind that does not exist — which is how `release_due` could
// have kept accumulating rows after its toggle was removed.
func ValidKind(kind string) bool {
	switch kind {
	case KindTaskAssigned, KindReviewerSet, KindMentioned, KindStatusChanged, KindTaskChanged:
		return true
	}
	return false
}

// ValidKinds returns the sendable kinds in the order the settings page lists
// them, so the UI and the validator cannot disagree about the set.
func ValidKinds() []string {
	return []string{KindTaskAssigned, KindReviewerSet, KindMentioned, KindStatusChanged, KindTaskChanged}
}

// EmailDefaultOn reports whether a notification kind delivers email by default
// when the user has no stored preference. Only task_changed opts in by default;
// every other kind stays email-off until the user enables it. The frontend
// preferences page mirrors these defaults.
func EmailDefaultOn(kind string) bool {
	return kind == KindTaskChanged
}
