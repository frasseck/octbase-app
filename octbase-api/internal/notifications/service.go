package notifications

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"

	"github.com/octbase/octbase-api/internal/mailer"
	"github.com/octbase/octbase-api/internal/shared"
	"github.com/octbase/octbase-api/internal/sse"
)

// Service creates notifications and broadcasts them via SSE.
type Service struct {
	db     *sql.DB
	repo   *Repo
	hub    *sse.Hub
	mailer mailer.Mailer
}

// NewService creates a new notifications Service. A nil mailer falls back to the
// default (SMTP when configured, otherwise dev-mode stdout logging).
func NewService(db *sql.DB, repo *Repo, hub *sse.Hub, mlr mailer.Mailer) *Service {
	if mlr == nil {
		mlr = mailer.New()
	}
	return &Service{db: db, repo: repo, hub: hub, mailer: mlr}
}

// Notify dispatches a notification across the two channels a user controls in
// their Notification Preferences: in-app (persisted + pushed over SSE) and
// email. The channels are independent — someone who turned off in-app but left
// email on still gets the email, and vice-versa.
//
// The two channels read the same event differently, which is why both message
// and params are passed. In-app is rendered by the client from kind + params,
// so it comes out in the reader's language. Email has no client and no browser
// locale, so it uses the composed English message; localizing it needs the
// recipient's stored language preference and a server-side catalogue, and is
// deliberately a separate piece of work (OCT-323 decision 2).
func (s *Service) Notify(userID, kind string, projectID, taskID, pageID *string, message string, params map[string]any) {
	s.notifyInApp(userID, kind, projectID, taskID, pageID, message, params)
	s.notifyEmail(userID, kind, projectID, taskID, pageID, message)
}

// notifyInApp creates the in-app notification and pushes it over SSE when the
// user has the in-app channel enabled for this kind.
func (s *Service) notifyInApp(userID, kind string, projectID, taskID, pageID *string, message string, params map[string]any) {
	enabled, err := s.repo.IsInAppEnabled(userID, kind)
	if err != nil || !enabled {
		return
	}
	n := &Notification{
		ID:        shared.NewUUID(),
		UserID:    userID,
		Kind:      kind,
		ProjectID: projectID,
		TaskID:    taskID,
		PageID:    pageID,
		Message:   message,
		Params:    params,
	}
	if err := s.repo.Create(n); err != nil {
		slog.Error("failed to create notification", "error", err)
		return
	}
	if projectID != nil {
		s.hub.Publish(*projectID, publishedNotification(userID, kind, message, params))
	}
}

// publishedNotification is the SSE payload for a new in-app notification. It
// carries kind + params so a client can render it localized, and keeps the
// English `message` it has always carried: today's receiver only bumps the
// unread badge, so dropping the field would break nothing visible here and
// silently break any client that does read it.
func publishedNotification(userID, kind, message string, params map[string]any) map[string]any {
	return map[string]any{
		"type":    "notification.created",
		"userId":  userID,
		"kind":    kind,
		"message": message,
		"params":  params,
	}
}

// notifyEmail sends an email for this kind when the user has the email channel
// enabled in their Notification Preferences and has a deliverable address. The
// in-app message doubles as the email lead line, with a deep link appended.
func (s *Service) notifyEmail(userID, kind string, projectID, taskID, pageID *string, message string) {
	enabled, err := s.repo.IsEmailEnabled(userID, kind)
	if err != nil || !enabled {
		return
	}
	email := s.emailAddr(userID)
	if email == "" {
		return
	}
	subject, body := notificationEmail(kind, message, projectID, taskID, pageID)
	if err := s.mailer.Send(context.Background(), email, subject, body); err != nil {
		slog.Error("notification email failed", "kind", kind, "userId", userID, "error", err)
	}
}

// NotifyTaskAssigned notifies the assignee when a task is assigned.
func (s *Service) NotifyTaskAssigned(taskID, taskTitle, projectID, assigneeID, actorID string) {
	if assigneeID == actorID {
		return
	}
	pid := projectID
	tid := taskID
	s.Notify(assigneeID, KindTaskAssigned, &pid, &tid, nil,
		"You were assigned to task: "+taskTitle,
		map[string]any{"title": taskTitle})
}

// NotifyReviewerSet notifies the reviewer when set on a task.
func (s *Service) NotifyReviewerSet(taskID, taskTitle, projectID, reviewerID, actorID string) {
	if reviewerID == actorID {
		return
	}
	pid := projectID
	tid := taskID
	s.Notify(reviewerID, KindReviewerSet, &pid, &tid, nil,
		"You were set as reviewer for task: "+taskTitle,
		map[string]any{"title": taskTitle})
}

// NotifyStatusChanged notifies the task reporter when status changes.
//
// It takes the status TWICE, and the two are not interchangeable:
//
//   - newStatus is the raw enum ("IN_REVIEW"). It goes into the render params,
//     where the client turns it into a status label in the reader's language —
//     the same treatment the activity feed gives TASK_STATUS_CHANGED. A custom
//     board-lane status is not in the client's enum table either, and falls
//     through to being printed as typed.
//   - newStatusLabel is the English display label ("In Review"), for the stored
//     message: that is the email lead line, and the fallback for a client too
//     old to read params. An enum here is what shipped "changed to IN_REVIEW".
//
// Callers derive the label with workmanagement.StatusLabel, which this package
// cannot call itself (modules do not import each other; see
// docs/architecture.md).
func (s *Service) NotifyStatusChanged(taskID, taskTitle, projectID, reporterID, actorID, newStatus, newStatusLabel string) {
	if reporterID == "" || reporterID == actorID {
		return
	}
	pid := projectID
	tid := taskID
	s.Notify(reporterID, KindStatusChanged, &pid, &tid, nil,
		"Task '"+taskTitle+"' status changed to "+newStatusLabel,
		map[string]any{"title": taskTitle, "status": newStatus})
}

// NotifyTaskChanged sends a brief email to the task's reporter and assignee
// summarizing changes made by someone else. It is email-only: the granular
// in-app notifications (assigned / status changed / mentioned) remain the in-app
// channel, so this never duplicates an in-app entry. The actor is never
// notified about their own edit, and reporter == assignee is de-duplicated.
func (s *Service) NotifyTaskChanged(taskID, taskTitle, projectID string, reporterID, assigneeID *string, actorID string, changes []string) {
	if len(changes) == 0 {
		return
	}
	seen := map[string]bool{"": true, actorID: true}
	var recipients []string
	for _, r := range []*string{reporterID, assigneeID} {
		if r != nil && !seen[*r] {
			seen[*r] = true
			recipients = append(recipients, *r)
		}
	}
	if len(recipients) == 0 {
		return
	}

	actorName := s.displayName(actorID)
	subject, body := taskChangedEmail(actorName, taskTitle, projectID, taskID, changes)
	for _, uid := range recipients {
		enabled, err := s.repo.IsEmailEnabled(uid, KindTaskChanged)
		if err != nil || !enabled {
			continue
		}
		email := s.emailAddr(uid)
		if email == "" {
			continue
		}
		if err := s.mailer.Send(context.Background(), email, subject, body); err != nil {
			slog.Error("task-changed email failed", "taskId", taskID, "userId", uid, "error", err)
		}
	}
}

// displayName returns a user's display name, falling back to "Someone".
func (s *Service) displayName(userID string) string {
	var name string
	if err := s.db.QueryRow(`SELECT display_name FROM users WHERE id = $1`, userID).Scan(&name); err != nil || name == "" {
		return "Someone"
	}
	return name
}

// emailAddr returns a user's email, or "" if the account is missing or inactive.
func (s *Service) emailAddr(userID string) string {
	var email string
	if err := s.db.QueryRow(
		`SELECT email FROM users WHERE id = $1 AND is_active = true`, userID,
	).Scan(&email); err != nil {
		return ""
	}
	return email
}

// taskChangedEmail builds the subject and plain-text body for a task-change
// notification. The mailer wraps the body in a text/html alternative.
func taskChangedEmail(actorName, taskTitle, projectID, taskID string, changes []string) (subject, body string) {
	subject = fmt.Sprintf("Task updated: %s", taskTitle)
	link := appLink(&projectID, &taskID, nil)

	var sb strings.Builder
	fmt.Fprintf(&sb, "%s updated the task \"%s\".\n\nWhat changed:\n", actorName, taskTitle)
	for _, c := range changes {
		fmt.Fprintf(&sb, "- %s\n", c)
	}
	fmt.Fprintf(&sb, "\nOpen it in Octbase:\n%s\n", link)
	return subject, sb.String()
}

// notificationEmail builds the subject and plain-text body for a per-kind
// notification email (task assigned / reviewer set / status changed / mentioned
// / release due). The in-app message is the lead line; a deep link to the
// relevant task or project is appended when available.
func notificationEmail(kind, message string, projectID, taskID, pageID *string) (subject, body string) {
	subject = notificationSubject(kind)
	var sb strings.Builder
	sb.WriteString(message)
	if link := appLink(projectID, taskID, pageID); link != "" {
		fmt.Fprintf(&sb, "\n\nOpen it in Octbase:\n%s\n", link)
	}
	return subject, sb.String()
}

// notificationSubject returns a short English subject line per notification kind.
func notificationSubject(kind string) string {
	switch kind {
	case KindTaskAssigned:
		return "You were assigned a task on Octbase"
	case KindReviewerSet:
		return "You were set as a reviewer on Octbase"
	case KindStatusChanged:
		return "A task status changed on Octbase"
	case KindMentioned:
		return "You were mentioned on Octbase"
	default:
		return "Octbase notification"
	}
}

// appLink returns a deep link into the SPA for the most specific context
// available, or the app root if none. OCTBASE_APP_URL should be set to the
// public host in any deployment; the localhost fallback is for local dev only.
func appLink(projectID, taskID, pageID *string) string {
	appURL := os.Getenv("OCTBASE_APP_URL")
	if appURL == "" {
		appURL = "http://localhost:8080"
	}
	switch {
	case projectID != nil && taskID != nil:
		return fmt.Sprintf("%s/#/projects/%s/board?task=%s", appURL, *projectID, *taskID)
	case projectID != nil && pageID != nil:
		return fmt.Sprintf("%s/#/projects/%s/pages/%s", appURL, *projectID, *pageID)
	case projectID != nil:
		return fmt.Sprintf("%s/#/projects/%s", appURL, *projectID)
	default:
		return appURL
	}
}

var mentionRE = regexp.MustCompile(`@(\S+)`)

// mentionMessage is the in-app text and email lead line for an @mention.
const mentionMessage = "You were mentioned in a comment"

// mentionParams is the render payload for an @mention. The sentence names
// nothing, so there is nothing to interpolate — but it must still be an empty
// object rather than nil, because nil is reserved for "written before params
// existed, fall back to the English message". A fresh mention is renderable
// from its kind alone.
func mentionParams() map[string]any { return map[string]any{} }

// mentionRecipient is a project member resolved from an @name token.
type mentionRecipient struct {
	userID string
	email  string
}

// NotifyMentions scans text for @name patterns and notifies matched project
// members.
//
// The text is user-supplied and unbounded, so recipients are resolved into a set
// before anything is written: "@alice" repeated five hundred times is one
// notification, and the fan-out can never exceed the project's member count.
// The batch is then handled set-wise — one preference query and one multi-row
// INSERT for all recipients — instead of the four queries plus an SMTP session
// per mention token this used to cost inside the comment request.
func (s *Service) NotifyMentions(text, projectID, taskID, actorID string) {
	matches := mentionRE.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return
	}

	members, err := s.projectMembersByName(projectID)
	if err != nil || len(members) == 0 {
		return
	}

	// Dedupe by resolved user ID (two display names can never collide, but the
	// same name appearing N times must still notify once), and never notify the
	// actor about their own mention.
	seen := map[string]bool{actorID: true, "": true}
	var recipients []mentionRecipient
	for _, m := range matches {
		name := m[1]
		if name == "" {
			continue
		}
		r, ok := members[name]
		if !ok || seen[r.userID] {
			continue
		}
		seen[r.userID] = true
		recipients = append(recipients, r)
	}
	if len(recipients) == 0 {
		return
	}

	userIDs := make([]string, 0, len(recipients))
	for _, r := range recipients {
		userIDs = append(userIDs, r.userID)
	}
	prefs, err := s.repo.PreferencesForKind(KindMentioned, userIDs)
	if err != nil {
		slog.Error("mention preference lookup failed", "projectId", projectID, "error", err)
		return
	}

	pid := projectID
	tid := taskID

	// In-app channel: one INSERT for the whole batch, then the SSE pushes (in
	// memory, no round trip).
	var inApp []Notification
	for _, r := range recipients {
		if !prefs[r.userID].InApp {
			continue
		}
		inApp = append(inApp, Notification{
			ID:        shared.NewUUID(),
			UserID:    r.userID,
			Kind:      KindMentioned,
			ProjectID: &pid,
			TaskID:    &tid,
			Message:   mentionMessage,
			Params:    mentionParams(),
		})
	}
	if len(inApp) > 0 {
		if err := s.repo.CreateMany(inApp); err != nil {
			slog.Error("failed to create mention notifications", "count", len(inApp), "error", err)
		} else {
			for _, n := range inApp {
				s.hub.Publish(pid, publishedNotification(n.UserID, KindMentioned, mentionMessage, n.Params))
			}
		}
	}

	// Email channel: the recipient's address came back with the member row, so
	// no extra lookup per user. Delivery itself is queued off this goroutine in
	// production (see mailer.Queue).
	subject, body := notificationEmail(KindMentioned, mentionMessage, &pid, &tid, nil)
	for _, r := range recipients {
		if !prefs[r.userID].Email || r.email == "" {
			continue
		}
		if err := s.mailer.Send(context.Background(), r.email, subject, body); err != nil {
			slog.Error("mention email failed", "userId", r.userID, "error", err)
		}
	}
}

// projectMembersByName returns the project's active members keyed by display
// name, carrying the email address so the email channel needs no per-recipient
// lookup.
func (s *Service) projectMembersByName(projectID string) (map[string]mentionRecipient, error) {
	rows, err := s.db.Query(`
		SELECT u.id, u.display_name, u.email
		  FROM users u
		  JOIN memberships m ON m.user_id = u.id AND m.project_id = $1
		 WHERE u.is_active = true`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	members := make(map[string]mentionRecipient)
	for rows.Next() {
		var id, name, email string
		if err := rows.Scan(&id, &name, &email); err != nil {
			continue
		}
		members[name] = mentionRecipient{userID: id, email: email}
	}
	return members, rows.Err()
}
