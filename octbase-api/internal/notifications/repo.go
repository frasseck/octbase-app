package notifications

import (
	"database/sql"
	"fmt"
	"strings"
)

// Repo handles notification persistence.
type Repo struct{ db *sql.DB }

// NewRepo creates a new notifications Repo.
func NewRepo(db *sql.DB) *Repo { return &Repo{db: db} }

// Create inserts a notification.
func (r *Repo) Create(n *Notification) error {
	_, err := r.db.Exec(`
		INSERT INTO notifications (id, user_id, kind, project_id, task_id, page_id, message, is_read)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		n.ID, n.UserID, n.Kind, n.ProjectID, n.TaskID, n.PageID, n.Message, n.IsRead,
	)
	return err
}

// CreateMany inserts a batch of notifications in a single multi-row INSERT.
// Used by the @mention fan-out, which would otherwise issue one round trip per
// recipient inside the comment request.
func (r *Repo) CreateMany(ns []Notification) error {
	if len(ns) == 0 {
		return nil
	}
	const cols = 8
	values := make([]string, 0, len(ns))
	args := make([]any, 0, len(ns)*cols)
	for i, n := range ns {
		base := i * cols
		values = append(values, fmt.Sprintf("($%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d)",
			base+1, base+2, base+3, base+4, base+5, base+6, base+7, base+8))
		args = append(args, n.ID, n.UserID, n.Kind, n.ProjectID, n.TaskID, n.PageID, n.Message, n.IsRead)
	}
	_, err := r.db.Exec( // #nosec G202 -- concatenates only generated $n placeholders; values are parameterized
		`
		INSERT INTO notifications (id, user_id, kind, project_id, task_id, page_id, message, is_read)
		VALUES `+strings.Join(values, ","), args...) // #nosec G202 -- joins generated $n placeholders only
	return err
}

// List returns paginated notifications for a user.
func (r *Repo) List(userID string, unreadOnly bool, page, size int) ([]Notification, error) {
	q := `SELECT id, user_id, kind, project_id, task_id, page_id, message, is_read, created_at
		    FROM notifications WHERE user_id = $1`
	args := []any{userID}
	if unreadOnly {
		q += " AND is_read = false"
	}
	q += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d", len(args)+1, len(args)+2) // #nosec G202 -- appends only computed placeholder indices
	args = append(args, size, page*size)

	rows, err := r.db.Query(q, args...) // #nosec G701 -- q is built above from static SQL plus $n placeholders only
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var ns []Notification
	for rows.Next() {
		var n Notification
		if err := rows.Scan(&n.ID, &n.UserID, &n.Kind, &n.ProjectID, &n.TaskID, &n.PageID, &n.Message, &n.IsRead, &n.CreatedAt); err != nil {
			return nil, err
		}
		ns = append(ns, n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if ns == nil {
		ns = []Notification{}
	}
	return ns, nil
}

// MarkRead marks a single notification as read.
func (r *Repo) MarkRead(id, userID string) error {
	return r.SetRead(id, userID, true)
}

// SetRead sets a single notification's read state to the given value.
func (r *Repo) SetRead(id, userID string, isRead bool) error {
	_, err := r.db.Exec(`UPDATE notifications SET is_read = $3 WHERE id = $1 AND user_id = $2`, id, userID, isRead)
	return err
}

// MarkAllRead marks all unread notifications for a user as read.
func (r *Repo) MarkAllRead(userID string) error {
	_, err := r.db.Exec(`UPDATE notifications SET is_read = true WHERE user_id = $1 AND is_read = false`, userID)
	return err
}

// UnreadCount returns the number of unread notifications for a user.
func (r *Repo) UnreadCount(userID string) (int, error) {
	var count int
	err := r.db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE user_id = $1 AND is_read = false`, userID).Scan(&count)
	return count, err
}

// GetPreferences retrieves a user's notification preferences.
func (r *Repo) GetPreferences(userID string) ([]NotificationPreference, error) {
	rows, err := r.db.Query(
		`SELECT user_id, kind, in_app, email FROM notification_preferences WHERE user_id = $1`, userID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var prefs []NotificationPreference
	for rows.Next() {
		var p NotificationPreference
		if err := rows.Scan(&p.UserID, &p.Kind, &p.InApp, &p.Email); err != nil {
			return nil, err
		}
		prefs = append(prefs, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if prefs == nil {
		prefs = []NotificationPreference{}
	}
	return prefs, nil
}

// UpsertPreference saves a notification preference.
func (r *Repo) UpsertPreference(p *NotificationPreference) error {
	_, err := r.db.Exec(`
		INSERT INTO notification_preferences (user_id, kind, in_app, email) VALUES ($1,$2,$3,$4)
		ON CONFLICT (user_id, kind) DO UPDATE SET in_app = EXCLUDED.in_app, email = EXCLUDED.email`,
		p.UserID, p.Kind, p.InApp, p.Email,
	)
	return err
}

// IsPreferenceEnabled returns whether a user has a given preference kind enabled for in-app delivery.
func (r *Repo) IsInAppEnabled(userID, kind string) (bool, error) {
	var enabled bool
	err := r.db.QueryRow(
		`SELECT COALESCE((SELECT in_app FROM notification_preferences WHERE user_id=$1 AND kind=$2), true)`,
		userID, kind,
	).Scan(&enabled)
	return enabled, err
}

// PreferencesForKind resolves both channels of one notification kind for a whole
// set of users in a single query (`= ANY($2)`), applying the same defaults as
// IsInAppEnabled/IsEmailEnabled to users with no stored row. It replaces two
// queries per recipient in fan-out paths such as @mentions.
func (r *Repo) PreferencesForKind(kind string, userIDs []string) (map[string]NotificationPreference, error) {
	prefs := make(map[string]NotificationPreference, len(userIDs))
	// Defaults first, so a user without a stored preference is still present.
	for _, id := range userIDs {
		prefs[id] = NotificationPreference{UserID: id, Kind: kind, InApp: true, Email: EmailDefaultOn(kind)}
	}
	if len(userIDs) == 0 {
		return prefs, nil
	}
	rows, err := r.db.Query(
		`SELECT user_id, in_app, email FROM notification_preferences
		  WHERE kind = $1 AND user_id = ANY($2)`, kind, userIDs)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var p NotificationPreference
		p.Kind = kind
		if err := rows.Scan(&p.UserID, &p.InApp, &p.Email); err != nil {
			return nil, err
		}
		prefs[p.UserID] = p
	}
	return prefs, rows.Err()
}

// IsEmailEnabled reports whether a user wants email delivery for a kind. With no
// stored preference it falls back to the per-kind default (see EmailDefaultOn).
func (r *Repo) IsEmailEnabled(userID, kind string) (bool, error) {
	var enabled bool
	err := r.db.QueryRow(
		`SELECT COALESCE((SELECT email FROM notification_preferences WHERE user_id=$1 AND kind=$2), $3)`,
		userID, kind, EmailDefaultOn(kind),
	).Scan(&enabled)
	return enabled, err
}
