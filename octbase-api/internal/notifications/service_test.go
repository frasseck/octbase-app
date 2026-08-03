package notifications_test

import (
	"context"
	"database/sql"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/octbase/octbase-api/internal/notifications"
	"github.com/octbase/octbase-api/internal/sse"
	"github.com/octbase/octbase-api/internal/testutil"
)

// captureMailer records sends instead of delivering, so tests can assert on the
// email channel without an SMTP server.
type captureMailer struct{ sent []capturedMail }

type capturedMail struct{ to, subject, body string }

func (m *captureMailer) Send(_ context.Context, to, subject, body string) error {
	m.sent = append(m.sent, capturedMail{to, subject, body})
	return nil
}

// svcFixture spins up the notifications service backed by a live SSE hub,
// plus an HTTP server used only to create projects/tasks/memberships.
func svcFixture(t *testing.T) (*notifications.Service, *notifications.Repo, *sql.DB, *httptest.Server) {
	t.Helper()
	db := testutil.NewTestDB(t)
	if db == nil {
		return nil, nil, nil, nil
	}
	srv := testutil.NewTestServer(t, db)
	repo := notifications.NewRepo(db)
	hub := sse.NewHub()
	go hub.Run()
	return notifications.NewService(db, repo, hub, nil), repo, db, srv
}

func count(t *testing.T, repo *notifications.Repo, userID string) int {
	t.Helper()
	ns, err := repo.List(userID, false, 0, 50)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	return len(ns)
}

func setDisplayName(t *testing.T, db *sql.DB, userID, name string) {
	t.Helper()
	if _, err := db.Exec(`UPDATE users SET display_name = $1 WHERE id = $2`, name, userID); err != nil {
		t.Fatalf("set display name: %v", err)
	}
}

func TestService_Notify_CreatesNotification(t *testing.T) {
	svc, repo, _, _ := svcFixture(t)
	if svc == nil {
		return
	}
	svc.Notify(testutil.DemoUserID, notifications.KindTaskAssigned, nil, nil, nil, "hello", map[string]any{"title": "T"})

	ns, err := repo.List(testutil.DemoUserID, false, 0, 20)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(ns) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(ns))
	}
	if ns[0].Message != "hello" || ns[0].Kind != notifications.KindTaskAssigned {
		t.Errorf("unexpected notification %+v", ns[0])
	}
}

func TestService_Notify_RespectsDisabledPreference(t *testing.T) {
	svc, repo, _, _ := svcFixture(t)
	if svc == nil {
		return
	}
	if err := repo.UpsertPreference(&notifications.NotificationPreference{
		UserID: testutil.DemoUserID, Kind: notifications.KindStatusChanged, InApp: false, Email: false,
	}); err != nil {
		t.Fatalf("upsert pref: %v", err)
	}

	svc.Notify(testutil.DemoUserID, notifications.KindStatusChanged, nil, nil, nil, "should not appear", nil)

	if got := count(t, repo, testutil.DemoUserID); got != 0 {
		t.Fatalf("expected 0 notifications when in-app disabled, got %d", got)
	}
}

func TestRepo_IsInAppEnabled(t *testing.T) {
	_, repo, _, _ := svcFixture(t)
	if repo == nil {
		return
	}
	en, err := repo.IsInAppEnabled(testutil.DemoUserID, notifications.KindMentioned)
	if err != nil {
		t.Fatalf("IsInAppEnabled: %v", err)
	}
	if !en {
		t.Error("expected default in-app enabled = true")
	}
	if err := repo.UpsertPreference(&notifications.NotificationPreference{
		UserID: testutil.DemoUserID, Kind: notifications.KindMentioned, InApp: false,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	en, err = repo.IsInAppEnabled(testutil.DemoUserID, notifications.KindMentioned)
	if err != nil {
		t.Fatalf("IsInAppEnabled: %v", err)
	}
	if en {
		t.Error("expected in-app enabled = false after disabling")
	}
}

func TestRepo_GetPreferences_RoundTrip(t *testing.T) {
	_, repo, _, _ := svcFixture(t)
	if repo == nil {
		return
	}
	if err := repo.UpsertPreference(&notifications.NotificationPreference{
		UserID: testutil.DemoUserID, Kind: notifications.KindReviewerSet, InApp: true, Email: true,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	prefs, err := repo.GetPreferences(testutil.DemoUserID)
	if err != nil {
		t.Fatalf("get prefs: %v", err)
	}
	if len(prefs) != 1 || prefs[0].Kind != notifications.KindReviewerSet || !prefs[0].Email {
		t.Errorf("unexpected prefs %+v", prefs)
	}
}

func TestService_NotifyTaskAssigned(t *testing.T) {
	svc, repo, _, srv := svcFixture(t)
	if svc == nil {
		return
	}
	pid := testutil.MustCreateProject(t, srv, "Assign Proj")
	tid := testutil.MustCreateTask(t, srv, pid, "Assign Task")
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_MEMBER")

	svc.NotifyTaskAssigned(tid, "Assign Task", pid, testutil.DemoUserID, testutil.DemoUserID) // self → no-op
	if got := count(t, repo, testutil.DemoUserID); got != 0 {
		t.Fatalf("self-assign should not notify, got %d", got)
	}
	svc.NotifyTaskAssigned(tid, "Assign Task", pid, testutil.SecondUserID, testutil.DemoUserID)
	if got := count(t, repo, testutil.SecondUserID); got != 1 {
		t.Fatalf("expected 1 notification for assignee, got %d", got)
	}
}

func TestService_NotifyReviewerSet(t *testing.T) {
	svc, repo, _, srv := svcFixture(t)
	if svc == nil {
		return
	}
	pid := testutil.MustCreateProject(t, srv, "Rev Proj")
	tid := testutil.MustCreateTask(t, srv, pid, "Rev Task")
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_MEMBER")

	svc.NotifyReviewerSet(tid, "Rev Task", pid, testutil.DemoUserID, testutil.DemoUserID) // self → no-op
	if got := count(t, repo, testutil.DemoUserID); got != 0 {
		t.Fatalf("self reviewer should not notify, got %d", got)
	}
	svc.NotifyReviewerSet(tid, "Rev Task", pid, testutil.SecondUserID, testutil.DemoUserID)
	if got := count(t, repo, testutil.SecondUserID); got != 1 {
		t.Fatalf("expected 1 reviewer notification, got %d", got)
	}
}

func TestService_NotifyStatusChanged(t *testing.T) {
	svc, repo, _, srv := svcFixture(t)
	if svc == nil {
		return
	}
	pid := testutil.MustCreateProject(t, srv, "Status Proj")
	tid := testutil.MustCreateTask(t, srv, pid, "Status Task")
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_MEMBER")

	svc.NotifyStatusChanged(tid, "Status Task", pid, "", testutil.DemoUserID, "DONE", "Done")                  // empty reporter → no-op
	svc.NotifyStatusChanged(tid, "Status Task", pid, testutil.DemoUserID, testutil.DemoUserID, "DONE", "Done") // reporter==actor → no-op
	if got := count(t, repo, testutil.DemoUserID); got != 0 {
		t.Fatalf("expected no self/empty notifications, got %d", got)
	}
	svc.NotifyStatusChanged(tid, "Status Task", pid, testutil.SecondUserID, testutil.DemoUserID, "DONE", "Done")
	if got := count(t, repo, testutil.SecondUserID); got != 1 {
		t.Fatalf("expected 1 status notification, got %d", got)
	}
}

// TestService_Notify_EmailChannelHonoursPreference proves a granular kind now
// emails when (and only when) the recipient enabled the email channel in their
// Notification Preferences, independent of the in-app channel.
func TestService_Notify_EmailChannelHonoursPreference(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	repo := notifications.NewRepo(db)
	hub := sse.NewHub()
	go hub.Run()
	mm := &captureMailer{}
	svc := notifications.NewService(db, repo, hub, mm)

	pid := testutil.MustCreateProject(t, srv, "Email Proj")
	tid := testutil.MustCreateTask(t, srv, pid, "Email Task")
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_MEMBER")

	// Disable both channels first: the email default for task_assigned is OFF,
	// and we turn in-app off too so the channel under test is isolated.
	if err := repo.UpsertPreference(&notifications.NotificationPreference{
		UserID: testutil.SecondUserID, Kind: notifications.KindTaskAssigned, InApp: false, Email: false,
	}); err != nil {
		t.Fatalf("upsert pref: %v", err)
	}
	svc.NotifyTaskAssigned(tid, "Email Task", pid, testutil.SecondUserID, testutil.DemoUserID)
	if len(mm.sent) != 0 {
		t.Fatalf("expected no email when email preference off, got %d", len(mm.sent))
	}

	// Opt the recipient into email-only for this kind.
	if err := repo.UpsertPreference(&notifications.NotificationPreference{
		UserID: testutil.SecondUserID, Kind: notifications.KindTaskAssigned, InApp: false, Email: true,
	}); err != nil {
		t.Fatalf("upsert pref: %v", err)
	}

	svc.NotifyTaskAssigned(tid, "Email Task", pid, testutil.SecondUserID, testutil.DemoUserID)
	if len(mm.sent) != 1 {
		t.Fatalf("expected 1 email after enabling preference, got %d", len(mm.sent))
	}
	if mm.sent[0].to != "second@octbase.dev" {
		t.Errorf("email sent to %q, want second@octbase.dev", mm.sent[0].to)
	}
	if !strings.Contains(mm.sent[0].body, "Email Task") {
		t.Errorf("email body missing task title: %q", mm.sent[0].body)
	}
	// In-app was disabled for this user/kind, so no in-app entry was created.
	if got := count(t, repo, testutil.SecondUserID); got != 0 {
		t.Fatalf("expected 0 in-app notifications (disabled), got %d", got)
	}
}

func TestService_NotifyMentions(t *testing.T) {
	svc, repo, db, srv := svcFixture(t)
	if svc == nil {
		return
	}
	pid := testutil.MustCreateProject(t, srv, "Mention Proj")
	tid := testutil.MustCreateTask(t, srv, pid, "Mention Task")
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_MEMBER")
	// The @mention regex matches non-space tokens, so give members single-token names.
	setDisplayName(t, db, testutil.SecondUserID, "secondhandle")
	setDisplayName(t, db, testutil.DemoUserID, "demohandle")

	svc.NotifyMentions("nothing to see here", pid, tid, testutil.DemoUserID) // no token → no-op
	if got := count(t, repo, testutil.SecondUserID); got != 0 {
		t.Fatalf("expected no notification without mention, got %d", got)
	}

	svc.NotifyMentions("hey @secondhandle and @demohandle", pid, tid, testutil.DemoUserID)
	if got := count(t, repo, testutil.SecondUserID); got != 1 {
		t.Fatalf("expected 1 mention notification for member, got %d", got)
	}
	if got := count(t, repo, testutil.DemoUserID); got != 0 {
		t.Fatalf("actor should not be notified of own mention, got %d", got)
	}
}
