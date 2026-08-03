package workmanagement_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
)

// A notification's message is composed on the server, stored as composed, and
// rendered verbatim by the SPA (realtime.js escapes it and prints it). So an
// enum handed to NotifyStatusChanged reaches the user's notification panel
// exactly as written — which is how "status changed to IN_REVIEW" shipped while
// the email for the same event, built one line below through StatusLabel, said
// "In Review". These tests pin the two call sites rather than the composer:
// passing the label is the caller's job, because the notifications module
// cannot import workmanagement to do it itself.

// statusNotification is one status_changed row as the SPA receives it.
type statusNotification struct {
	Kind    string         `json:"kind"`
	Message string         `json:"message"`
	Params  map[string]any `json:"params"`
}

// statusNotificationRows returns userID's status_changed notifications.
func statusNotificationRows(t *testing.T, srv *httptest.Server, userID string) []statusNotification {
	t.Helper()
	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/users/me/notifications", nil, userID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var body struct {
		Notifications []statusNotification `json:"notifications"`
	}
	testutil.DecodeJSON(t, resp, &body)
	var rows []statusNotification
	for _, n := range body.Notifications {
		if n.Kind == "status_changed" {
			rows = append(rows, n)
		}
	}
	return rows
}

// statusNotifications returns userID's status_changed notification messages.
func statusNotifications(t *testing.T, srv *httptest.Server, userID string) []string {
	t.Helper()
	var msgs []string
	for _, n := range statusNotificationRows(t, srv, userID) {
		msgs = append(msgs, n.Message)
	}
	return msgs
}

// columnWithStatus returns the id of the board column carrying the given status.
func columnWithStatus(t *testing.T, srv *httptest.Server, boardID, status string) string {
	t.Helper()
	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/boards/"+boardID, nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var b struct {
		Columns []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"columns"`
	}
	testutil.DecodeJSON(t, resp, &b)
	for _, c := range b.Columns {
		if c.Status == status {
			return c.ID
		}
	}
	t.Fatalf("board %s has no column with status %q", boardID, status)
	return ""
}

func TestStatusChangeNotification_UsesLabelNotEnum(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Notify Proj")
	// The reporter is the creator (DemoUser); the actor must be someone else or
	// the notification is deliberately suppressed.
	tid := testutil.MustCreateTask(t, srv, pid, "Notify Task")
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_MEMBER")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/status",
		map[string]string{"status": "IN_REVIEW"}, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	msgs := statusNotifications(t, srv, testutil.DemoUserID)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 status_changed notification, got %d (%v)", len(msgs), msgs)
	}
	if strings.Contains(msgs[0], "IN_REVIEW") {
		t.Errorf("notification leaks the raw enum: %q", msgs[0])
	}
	if !strings.Contains(msgs[0], "In Review") {
		t.Errorf("notification should read the display label, got %q", msgs[0])
	}
}

// The board's move-task door writes the same notification and had the same bug:
// dragging a card into another lane changes the task's status too.
func TestBoardMoveNotification_UsesLabelNotEnum(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Notify Board Proj")
	bid := testutil.MustCreateBoard(t, srv, pid)
	testutil.MustAddColumn(t, srv, bid, "Review", "IN_REVIEW", 3)
	tid := testutil.MustCreateTask(t, srv, pid, "Board Notify Task")
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_MEMBER")

	colID := columnWithStatus(t, srv, bid, "IN_REVIEW")
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+bid+"/move-task",
		map[string]any{"taskId": tid, "boardColumnId": colID}, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	msgs := statusNotifications(t, srv, testutil.DemoUserID)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 status_changed notification, got %d (%v)", len(msgs), msgs)
	}
	if strings.Contains(msgs[0], "IN_REVIEW") {
		t.Errorf("board move leaks the raw enum: %q", msgs[0])
	}
	if !strings.Contains(msgs[0], "In Review") {
		t.Errorf("board move should read the display label, got %q", msgs[0])
	}
}

// A custom board-lane status is a name a human typed, so it must pass through
// untouched — StatusLabel rewrites only the five built-ins.
func TestStatusChangeNotification_CustomStatusPassesThrough(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Custom Status Proj")
	bid := testutil.MustCreateBoard(t, srv, pid)
	testutil.MustAddColumn(t, srv, bid, "Blocked", "Blocked", 99)
	tid := testutil.MustCreateTask(t, srv, pid, "Custom Status Task")
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_MEMBER")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/status",
		map[string]string{"status": "Blocked"}, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	msgs := statusNotifications(t, srv, testutil.DemoUserID)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 status_changed notification, got %d (%v)", len(msgs), msgs)
	}
	if !strings.Contains(msgs[0], "Blocked") {
		t.Errorf("custom status should pass through unchanged, got %q", msgs[0])
	}
}

// The label alone is not enough: it is English, and the SPA now renders the
// notification from `kind` + `params` so a German reader gets German (OCT-323).
// That only works if the RAW enum reaches params — the client owns the status
// vocabulary. Both call sites pass the status twice, raw then labelled, and
// nothing but a test distinguishes a swap.
func TestStatusChangeNotification_ParamsCarryRawEnum(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Params Notify Proj")
	tid := testutil.MustCreateTask(t, srv, pid, "Params Notify Task")
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_MEMBER")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/status",
		map[string]string{"status": "IN_REVIEW"}, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	rows := statusNotificationRows(t, srv, testutil.DemoUserID)
	if len(rows) != 1 {
		t.Fatalf("expected 1 status_changed notification, got %d", len(rows))
	}
	n := rows[0]
	if n.Params == nil {
		t.Fatal("notification carries no params; the SPA would fall back to English")
	}
	if n.Params["status"] != "IN_REVIEW" {
		t.Errorf("params.status = %v, want the raw enum IN_REVIEW (the label belongs in message)", n.Params["status"])
	}
	if n.Params["title"] != "Params Notify Task" {
		t.Errorf("params.title = %v, want the task title", n.Params["title"])
	}
	if !strings.Contains(n.Message, "In Review") {
		t.Errorf("message should still read the English label for the email path, got %q", n.Message)
	}
}

// The board's move-task door writes the same notification through a different
// call site, so it needs the same pin.
func TestBoardMoveNotification_ParamsCarryRawEnum(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)
	pid := testutil.MustCreateProject(t, srv, "Params Board Proj")
	bid := testutil.MustCreateBoard(t, srv, pid)
	testutil.MustAddColumn(t, srv, bid, "Review", "IN_REVIEW", 3)
	tid := testutil.MustCreateTask(t, srv, pid, "Params Board Task")
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_MEMBER")

	colID := columnWithStatus(t, srv, bid, "IN_REVIEW")
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/boards/"+bid+"/move-task",
		map[string]any{"taskId": tid, "boardColumnId": colID}, testutil.SecondUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	rows := statusNotificationRows(t, srv, testutil.DemoUserID)
	if len(rows) != 1 {
		t.Fatalf("expected 1 status_changed notification, got %d", len(rows))
	}
	if rows[0].Params["status"] != "IN_REVIEW" {
		t.Errorf("board move params.status = %v, want the raw enum IN_REVIEW", rows[0].Params["status"])
	}
}
