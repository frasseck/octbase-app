package notifications_test

import (
	"testing"

	"github.com/octbase/octbase-api/internal/notifications"
	"github.com/octbase/octbase-api/internal/testutil"
)

// A notification's text used to be English prose composed on the server and
// rendered verbatim by both SPAs, so a German reader read English in the bell
// (OCT-323). It now carries `kind` + `params` the way an activity entry does,
// and the client renders `notifications.kinds.<kind>.message` from them.
//
// These tests pin the server half of that contract: which parameters each kind
// emits, and — the part that decides how every historical row renders — that a
// nil params map is stored as SQL NULL while an empty one is stored as `{}`.

// firstOf returns userID's single notification of the given kind, failing if
// there is not exactly one.
func firstOf(t *testing.T, repo *notifications.Repo, userID, kind string) notifications.Notification {
	t.Helper()
	ns, err := repo.List(userID, false, 0, 50)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var found []notifications.Notification
	for _, n := range ns {
		if n.Kind == kind {
			found = append(found, n)
		}
	}
	if len(found) != 1 {
		t.Fatalf("expected exactly 1 %s notification, got %d", kind, len(found))
	}
	return found[0]
}

// assertParams checks a notification's params against an expected string map.
// Params round-trip through JSON, so every value arrives as a string here.
func assertParams(t *testing.T, n notifications.Notification, want map[string]string) {
	t.Helper()
	if n.Params == nil {
		t.Fatalf("%s: params are nil; the client would fall back to the English message", n.Kind)
	}
	if len(n.Params) != len(want) {
		t.Errorf("%s: params %v, want %v", n.Kind, n.Params, want)
	}
	for k, v := range want {
		got, ok := n.Params[k]
		if !ok {
			t.Errorf("%s: params missing %q (have %v)", n.Kind, k, n.Params)
			continue
		}
		if got != v {
			t.Errorf("%s: params[%q] = %v, want %q", n.Kind, k, got, v)
		}
	}
}

func TestNotifyTaskAssigned_CarriesTitleParam(t *testing.T) {
	svc, repo, _, srv := svcFixture(t)
	if svc == nil {
		return
	}
	pid := testutil.MustCreateProject(t, srv, "Params Proj")
	tid := testutil.MustCreateTask(t, srv, pid, "Params Task")

	svc.NotifyTaskAssigned(tid, "Params Task", pid, testutil.SecondUserID, testutil.DemoUserID)

	n := firstOf(t, repo, testutil.SecondUserID, notifications.KindTaskAssigned)
	assertParams(t, n, map[string]string{"title": "Params Task"})
}

func TestNotifyReviewerSet_CarriesTitleParam(t *testing.T) {
	svc, repo, _, srv := svcFixture(t)
	if svc == nil {
		return
	}
	pid := testutil.MustCreateProject(t, srv, "Reviewer Proj")
	tid := testutil.MustCreateTask(t, srv, pid, "Reviewer Task")

	svc.NotifyReviewerSet(tid, "Reviewer Task", pid, testutil.SecondUserID, testutil.DemoUserID)

	n := firstOf(t, repo, testutil.SecondUserID, notifications.KindReviewerSet)
	assertParams(t, n, map[string]string{"title": "Reviewer Task"})
}

// The status arrives twice and the two must not be confused: params carry the
// RAW enum, because the client owns the status vocabulary and translates it,
// while the stored message carries the English LABEL, because that is the email
// lead line. Swapping the two arguments compiles — this is what catches it.
func TestNotifyStatusChanged_ParamsCarryEnum_MessageCarriesLabel(t *testing.T) {
	svc, repo, _, srv := svcFixture(t)
	if svc == nil {
		return
	}
	pid := testutil.MustCreateProject(t, srv, "Status Params Proj")
	tid := testutil.MustCreateTask(t, srv, pid, "Status Params Task")

	svc.NotifyStatusChanged(tid, "Status Params Task", pid, testutil.SecondUserID, testutil.DemoUserID,
		"IN_REVIEW", "In Review")

	n := firstOf(t, repo, testutil.SecondUserID, notifications.KindStatusChanged)
	assertParams(t, n, map[string]string{"title": "Status Params Task", "status": "IN_REVIEW"})
	if n.Message != "Task 'Status Params Task' status changed to In Review" {
		t.Errorf("message should read the English label, got %q", n.Message)
	}
}

// A custom board-lane status is a name a human typed. It has no entry in the
// client's status table either, so it must reach params verbatim and be printed
// as-is rather than mangled into a lookup miss.
func TestNotifyStatusChanged_CustomStatusReachesParamsVerbatim(t *testing.T) {
	svc, repo, _, srv := svcFixture(t)
	if svc == nil {
		return
	}
	pid := testutil.MustCreateProject(t, srv, "Custom Params Proj")
	tid := testutil.MustCreateTask(t, srv, pid, "Custom Params Task")

	svc.NotifyStatusChanged(tid, "Custom Params Task", pid, testutil.SecondUserID, testutil.DemoUserID,
		"Waiting on legal", "Waiting on legal")

	n := firstOf(t, repo, testutil.SecondUserID, notifications.KindStatusChanged)
	assertParams(t, n, map[string]string{"title": "Custom Params Task", "status": "Waiting on legal"})
}

// `mentioned` names nothing, so it has no parameters — but it must still store
// an empty object. nil means "written before params existed, fall back to the
// English sentence", and a fresh mention is not that.
func TestNotifyMentions_StoresEmptyParamsNotNull(t *testing.T) {
	svc, repo, _, _, pid, tid := mentionFixture(t, nil)
	if svc == nil {
		return
	}
	svc.NotifyMentions("hey @secondhandle take a look", pid, tid, testutil.DemoUserID)

	n := firstOf(t, repo, testutil.SecondUserID, notifications.KindMentioned)
	if n.Params == nil {
		t.Fatal("mention params are nil; the client would fall back to the English message")
	}
	if len(n.Params) != 0 {
		t.Errorf("mention should carry no parameters, got %v", n.Params)
	}
}

// The pre-change rows. Nothing writes nil params any more, but every
// notification stored before this change has them, and the whole migration
// strategy is that those rows keep rendering their English sentence rather than
// coming back blank. Storing nil must therefore survive the round trip as nil,
// distinguishably from the empty object above.
func TestNilParamsRoundTripAsNull(t *testing.T) {
	svc, repo, _, _ := svcFixture(t)
	if svc == nil {
		return
	}
	svc.Notify(testutil.DemoUserID, notifications.KindTaskAssigned, nil, nil, nil,
		"You were assigned to task: Legacy", nil)

	n := firstOf(t, repo, testutil.DemoUserID, notifications.KindTaskAssigned)
	if n.Params != nil {
		t.Errorf("params should stay nil so the client falls back to the message, got %v", n.Params)
	}
	if n.Message != "You were assigned to task: Legacy" {
		t.Errorf("the fallback message must survive, got %q", n.Message)
	}
}
