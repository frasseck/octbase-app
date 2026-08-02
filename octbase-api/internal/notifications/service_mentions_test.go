package notifications_test

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/octbase/octbase-api/internal/mailer"
	"github.com/octbase/octbase-api/internal/notifications"
	"github.com/octbase/octbase-api/internal/sse"
	"github.com/octbase/octbase-api/internal/testutil"
)

// erroringMailer fails every send, standing in for an unreachable/blackholed
// relay. It counts attempts so tests can prove delivery was tried.
type erroringMailer struct{ attempts atomic.Int32 }

func (m *erroringMailer) Send(_ context.Context, _, _, _ string) error {
	m.attempts.Add(1)
	return errors.New("smtp unavailable")
}

// mentionFixture builds a notifications service with a caller-supplied mailer,
// plus a project + task with SecondUserID as a member. Display names are single
// tokens because the @mention pattern matches non-space runs.
func mentionFixture(t *testing.T, mlr mailer.Mailer) (*notifications.Service, *notifications.Repo, *sql.DB, *httptest.Server, string, string) {
	t.Helper()
	db := testutil.NewTestDB(t)
	if db == nil {
		return nil, nil, nil, nil, "", ""
	}
	srv := testutil.NewTestServer(t, db)
	repo := notifications.NewRepo(db)
	hub := sse.NewHub()
	go hub.Run()

	pid := testutil.MustCreateProject(t, srv, "Mention Perf Proj")
	tid := testutil.MustCreateTask(t, srv, pid, "Mention Perf Task")
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_MEMBER")
	setDisplayName(t, db, testutil.SecondUserID, "secondhandle")
	setDisplayName(t, db, testutil.DemoUserID, "demohandle")

	return notifications.NewService(db, repo, hub, mlr), repo, db, srv, pid, tid
}

func enableMentionEmail(t *testing.T, repo *notifications.Repo, userID string, email bool) {
	t.Helper()
	if err := repo.UpsertPreference(&notifications.NotificationPreference{
		UserID: userID, Kind: notifications.KindMentioned, InApp: true, Email: email,
	}); err != nil {
		t.Fatalf("upsert pref: %v", err)
	}
}

// TestNotifyMentions_DedupesRepeatedMention is the regression test for the
// unbounded fan-out: `@name` repeated 500 times in one comment used to cost 500
// notifications, 2000 queries and 500 SMTP sessions inside the POST request.
func TestNotifyMentions_DedupesRepeatedMention(t *testing.T) {
	fm := &fakeMailer{}
	svc, repo, _, _, pid, tid := mentionFixture(t, fm)
	if svc == nil {
		return
	}
	enableMentionEmail(t, repo, testutil.SecondUserID, true)

	text := strings.Repeat("@secondhandle ", 500)
	svc.NotifyMentions(text, pid, tid, testutil.DemoUserID)

	if got := count(t, repo, testutil.SecondUserID); got != 1 {
		t.Fatalf("in-app notifications = %d, want 1 for 500 repeats of the same mention", got)
	}
	if to := fm.recipients(); len(to) != 1 {
		t.Fatalf("emails = %d (%v), want exactly 1", len(to), to)
	}
	if to := fm.recipients(); to[0] != "second@octbase.dev" {
		t.Errorf("email recipient = %q, want second@octbase.dev", to[0])
	}
}

// TestNotifyMentions_HonoursChannelPreferences: the two channels stay
// independent after the batching rewrite, and an unstored preference keeps its
// per-kind default (in-app on, email off for mentions).
func TestNotifyMentions_HonoursChannelPreferences(t *testing.T) {
	fm := &fakeMailer{}
	svc, repo, _, _, pid, tid := mentionFixture(t, fm)
	if svc == nil {
		return
	}

	// Default (no stored row): in-app on, mention email off.
	svc.NotifyMentions("hi @secondhandle", pid, tid, testutil.DemoUserID)
	if got := count(t, repo, testutil.SecondUserID); got != 1 {
		t.Fatalf("default in-app notifications = %d, want 1", got)
	}
	if to := fm.recipients(); len(to) != 0 {
		t.Fatalf("default mention emails = %v, want none", to)
	}

	// In-app off, email on -> email only, no new in-app row.
	if err := repo.UpsertPreference(&notifications.NotificationPreference{
		UserID: testutil.SecondUserID, Kind: notifications.KindMentioned, InApp: false, Email: true,
	}); err != nil {
		t.Fatalf("upsert pref: %v", err)
	}
	svc.NotifyMentions("hi again @secondhandle", pid, tid, testutil.DemoUserID)
	if got := count(t, repo, testutil.SecondUserID); got != 1 {
		t.Fatalf("in-app notifications = %d, want still 1 (in-app disabled)", got)
	}
	if to := fm.recipients(); len(to) != 1 {
		t.Fatalf("emails = %v, want exactly 1 after enabling the email channel", to)
	}

	// Both off -> nothing at all.
	if err := repo.UpsertPreference(&notifications.NotificationPreference{
		UserID: testutil.SecondUserID, Kind: notifications.KindMentioned, InApp: false, Email: false,
	}); err != nil {
		t.Fatalf("upsert pref: %v", err)
	}
	svc.NotifyMentions("and again @secondhandle", pid, tid, testutil.DemoUserID)
	if got := count(t, repo, testutil.SecondUserID); got != 1 {
		t.Errorf("in-app notifications = %d, want still 1 (both channels off)", got)
	}
	if to := fm.recipients(); len(to) != 1 {
		t.Errorf("emails = %v, want still 1 (both channels off)", to)
	}
}

// TestNotifyMentions_SkipsActorAndNonMembers preserves the pre-existing rules.
func TestNotifyMentions_SkipsActorAndNonMembers(t *testing.T) {
	fm := &fakeMailer{}
	svc, repo, _, _, pid, tid := mentionFixture(t, fm)
	if svc == nil {
		return
	}
	enableMentionEmail(t, repo, testutil.DemoUserID, true)

	svc.NotifyMentions("@demohandle @nobodyhere @@ ", pid, tid, testutil.DemoUserID)

	if got := count(t, repo, testutil.DemoUserID); got != 0 {
		t.Errorf("actor was notified about their own mention (%d notifications)", got)
	}
	if to := fm.recipients(); len(to) != 0 {
		t.Errorf("emails = %v, want none (actor + unknown handles only)", to)
	}
}

// TestNotifyMentions_MultipleRecipientsEachOnce covers the multi-row INSERT:
// several distinct mentions, some repeated, still yield one notification each.
func TestNotifyMentions_MultipleRecipientsEachOnce(t *testing.T) {
	fm := &fakeMailer{}
	svc, repo, db, srv, pid, tid := mentionFixture(t, fm)
	if svc == nil {
		return
	}
	testutil.MustAddMember(t, srv, pid, testutil.SuperAdminUserID, "PROJECT_MEMBER")
	setDisplayName(t, db, testutil.SuperAdminUserID, "adminhandle")
	enableMentionEmail(t, repo, testutil.SecondUserID, true)
	enableMentionEmail(t, repo, testutil.SuperAdminUserID, true)

	svc.NotifyMentions("@secondhandle @adminhandle @secondhandle @adminhandle", pid, tid, testutil.DemoUserID)

	if got := count(t, repo, testutil.SecondUserID); got != 1 {
		t.Errorf("second user notifications = %d, want 1", got)
	}
	if got := count(t, repo, testutil.SuperAdminUserID); got != 1 {
		t.Errorf("admin notifications = %d, want 1", got)
	}
	if to := fm.recipients(); len(to) != 2 {
		t.Errorf("emails = %v, want one per recipient", to)
	}
}

// TestNotifyMentions_MailFailureStillNotifiesInApp: a broken relay must not cost
// the in-app notification, and must not surface anywhere near the caller.
func TestNotifyMentions_MailFailureStillNotifiesInApp(t *testing.T) {
	em := &erroringMailer{}
	svc, repo, _, _, pid, tid := mentionFixture(t, em)
	if svc == nil {
		return
	}
	enableMentionEmail(t, repo, testutil.SecondUserID, true)

	svc.NotifyMentions("@secondhandle", pid, tid, testutil.DemoUserID)

	if em.attempts.Load() != 1 {
		t.Errorf("send attempts = %d, want 1", em.attempts.Load())
	}
	if got := count(t, repo, testutil.SecondUserID); got != 1 {
		t.Errorf("in-app notifications = %d, want 1 despite the mail failure", got)
	}
}

// TestNotifyMentions_QueuedMailIsAsyncAndDrainsCleanly wires the production
// mechanism (mailer.Queue) behind the notifier: the notification is persisted
// without waiting for SMTP, the failing delivery is contained in the worker, and
// the queue shuts down cleanly.
func TestNotifyMentions_QueuedMailIsAsyncAndDrainsCleanly(t *testing.T) {
	em := &erroringMailer{}
	q := mailer.NewQueue(em, 1, 8)
	svc, repo, _, _, pid, tid := mentionFixture(t, q)
	if svc == nil {
		return
	}
	enableMentionEmail(t, repo, testutil.SecondUserID, true)

	svc.NotifyMentions("@secondhandle", pid, tid, testutil.DemoUserID)

	// The in-app notification is already durable; delivery is still in flight.
	if got := count(t, repo, testutil.SecondUserID); got != 1 {
		t.Fatalf("in-app notifications = %d, want 1", got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := q.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if em.attempts.Load() != 1 {
		t.Errorf("queued send attempts = %d, want 1", em.attempts.Load())
	}
	if err := q.Close(ctx); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// TestCommentWithManyMentions_RequestSucceeds drives the real HTTP path the bug
// report came from: a comment stuffed with repeated mentions is accepted, and the
// mentioned member ends up with a single notification.
func TestCommentWithManyMentions_RequestSucceeds(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	repo := notifications.NewRepo(db)

	pid := testutil.MustCreateProject(t, srv, "Comment Mention Proj")
	tid := testutil.MustCreateTask(t, srv, pid, "Comment Mention Task")
	testutil.MustAddMember(t, srv, pid, testutil.SecondUserID, "PROJECT_MEMBER")
	setDisplayName(t, db, testutil.SecondUserID, "secondhandle")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/tasks/"+tid+"/comments",
		map[string]string{"text": strings.Repeat("@secondhandle ", 500)}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	_ = resp.Body.Close()

	if got := count(t, repo, testutil.SecondUserID); got != 1 {
		t.Errorf("notifications after a 500-mention comment = %d, want 1", got)
	}
}
