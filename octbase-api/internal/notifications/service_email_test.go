package notifications_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/octbase/octbase-api/internal/notifications"
	"github.com/octbase/octbase-api/internal/sse"
	"github.com/octbase/octbase-api/internal/testutil"
)

type sentMail struct{ to, subject, body string }

// fakeMailer captures sent mail instead of delivering it.
type fakeMailer struct {
	mu   sync.Mutex
	sent []sentMail
}

func (f *fakeMailer) Send(_ context.Context, to, subject, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, sentMail{to, subject, body})
	return nil
}

func (f *fakeMailer) recipients() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var to []string
	for _, m := range f.sent {
		to = append(to, m.to)
	}
	return to
}

func contains(hay []string, needle string) bool {
	for _, s := range hay {
		if s == needle {
			return true
		}
	}
	return false
}

// taskChangedFixture builds a notifications Service wired to a capturing mailer.
func taskChangedFixture(t *testing.T) (*notifications.Service, *notifications.Repo, *fakeMailer) {
	t.Helper()
	db := testutil.NewTestDB(t)
	if db == nil {
		return nil, nil, nil
	}
	repo := notifications.NewRepo(db)
	hub := sse.NewHub()
	go hub.Run()
	fm := &fakeMailer{}
	return notifications.NewService(db, repo, hub, fm), repo, fm
}

func TestNotifyTaskChanged_EmailsReporterAndAssignee(t *testing.T) {
	svc, _, fm := taskChangedFixture(t)
	if svc == nil {
		return
	}
	reporter := testutil.DemoUserID   // demo@octbase.dev
	assignee := testutil.SecondUserID // second@octbase.dev
	actor := testutil.SuperAdminUserID

	svc.NotifyTaskChanged("task-1", "Fix the bug", "proj-1", &reporter, &assignee, actor,
		[]string{"Status: PLANNED → DONE"})

	to := fm.recipients()
	if len(to) != 2 {
		t.Fatalf("expected 2 emails, got %d (%v)", len(to), to)
	}
	if !contains(to, "demo@octbase.dev") || !contains(to, "second@octbase.dev") {
		t.Errorf("recipients = %v, want demo@ and second@", to)
	}
	m := fm.sent[0]
	if !strings.Contains(m.subject, "Fix the bug") {
		t.Errorf("subject = %q, want it to mention the task title", m.subject)
	}
	if !strings.Contains(m.body, "Status: PLANNED → DONE") || !strings.Contains(m.body, "/#/projects/proj-1") {
		t.Errorf("body = %q, want the change line and a task link", m.body)
	}
}

func TestNotifyTaskChanged_ExcludesActorAndDedupes(t *testing.T) {
	svc, _, fm := taskChangedFixture(t)
	if svc == nil {
		return
	}
	reporter := testutil.DemoUserID
	assignee := testutil.SecondUserID

	// Actor is the reporter -> only the assignee is emailed.
	svc.NotifyTaskChanged("task-1", "T", "p", &reporter, &assignee, reporter, []string{"Title"})
	if to := fm.recipients(); len(to) != 1 || to[0] != "second@octbase.dev" {
		t.Fatalf("actor-excluded recipients = %v, want [second@octbase.dev]", to)
	}

	// Reporter == assignee -> de-duplicated to a single email.
	fm.sent = nil
	svc.NotifyTaskChanged("task-1", "T", "p", &reporter, &reporter, testutil.SuperAdminUserID, []string{"Title"})
	if to := fm.recipients(); len(to) != 1 {
		t.Fatalf("dedup recipients = %v, want exactly one", to)
	}
}

func TestNotifyTaskChanged_NoChangesNoEmail(t *testing.T) {
	svc, _, fm := taskChangedFixture(t)
	if svc == nil {
		return
	}
	reporter := testutil.DemoUserID
	svc.NotifyTaskChanged("task-1", "T", "p", &reporter, nil, testutil.SuperAdminUserID, nil)
	if to := fm.recipients(); len(to) != 0 {
		t.Fatalf("empty-change emails = %v, want none", to)
	}
}

func TestNotifyTaskChanged_RespectsEmailPreferenceOff(t *testing.T) {
	svc, repo, fm := taskChangedFixture(t)
	if svc == nil {
		return
	}
	reporter := testutil.DemoUserID
	if err := repo.UpsertPreference(&notifications.NotificationPreference{
		UserID: reporter, Kind: notifications.KindTaskChanged, InApp: true, Email: false,
	}); err != nil {
		t.Fatalf("upsert pref: %v", err)
	}
	svc.NotifyTaskChanged("task-1", "T", "p", &reporter, nil, testutil.SuperAdminUserID, []string{"Title"})
	if to := fm.recipients(); len(to) != 0 {
		t.Fatalf("opted-out user emailed: %v", to)
	}
}
