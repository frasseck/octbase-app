package notifications_test

import (
	"net/http"
	"testing"

	"github.com/octbase/octbase-api/internal/notifications"
	"github.com/octbase/octbase-api/internal/shared"
	"github.com/octbase/octbase-api/internal/testutil"
)

// insertTestNotification inserts a notification directly into the DB for test setup.
func insertTestNotification(t *testing.T, repo *notifications.Repo, userID, kind, message string) string {
	t.Helper()
	n := &notifications.Notification{
		ID:      shared.NewUUID(),
		UserID:  userID,
		Kind:    kind,
		Message: message,
		IsRead:  false,
	}
	if err := repo.Create(n); err != nil {
		t.Fatalf("insert notification: %v", err)
	}
	return n.ID
}

func TestListNotifications_Empty(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/users/me/notifications", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	var result map[string]interface{}
	testutil.DecodeJSON(t, resp, &result)
	ns, ok := result["notifications"].([]interface{})
	if !ok || ns == nil {
		// an empty slice may decode as nil — that's acceptable
	} else if len(ns) != 0 {
		t.Errorf("expected 0 notifications, got %d", len(ns))
	}
	if result["unreadCount"] == nil {
		t.Error("expected unreadCount in response")
	}
}

func TestListNotifications_HasEntries(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	repo := notifications.NewRepo(db)

	insertTestNotification(t, repo, testutil.DemoUserID, notifications.KindTaskAssigned, "You were assigned a task")
	insertTestNotification(t, repo, testutil.DemoUserID, notifications.KindStatusChanged, "Task status changed")

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/users/me/notifications", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	var result map[string]interface{}
	testutil.DecodeJSON(t, resp, &result)
	ns, _ := result["notifications"].([]interface{})
	if len(ns) < 2 {
		t.Errorf("expected >= 2 notifications, got %d", len(ns))
	}
	unread, _ := result["unreadCount"].(float64)
	if unread < 2 {
		t.Errorf("expected unreadCount >= 2, got %v", result["unreadCount"])
	}
}

func TestListNotifications_UnreadOnly(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	repo := notifications.NewRepo(db)

	nid := insertTestNotification(t, repo, testutil.DemoUserID, notifications.KindTaskAssigned, "Unread notification")
	// mark it read directly
	if err := repo.MarkRead(nid, testutil.DemoUserID); err != nil {
		t.Fatalf("mark read: %v", err)
	}
	insertTestNotification(t, repo, testutil.DemoUserID, notifications.KindMentioned, "Unread mention")

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/users/me/notifications?unreadOnly=true", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	var result map[string]interface{}
	testutil.DecodeJSON(t, resp, &result)
	ns, _ := result["notifications"].([]interface{})
	if len(ns) != 1 {
		t.Errorf("expected 1 unread notification, got %d", len(ns))
	}
}

func TestMarkAllRead(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	repo := notifications.NewRepo(db)

	insertTestNotification(t, repo, testutil.DemoUserID, notifications.KindTaskAssigned, "Notif 1")
	insertTestNotification(t, repo, testutil.DemoUserID, notifications.KindStatusChanged, "Notif 2")

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/users/me/notifications/read-all", map[string]string{}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusNoContent)
	_ = resp.Body.Close()

	// Verify unread count is now 0.
	resp2 := testutil.Do(t, srv, http.MethodGet, "/api/v1/users/me/notifications", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp2, http.StatusOK)
	var result map[string]interface{}
	testutil.DecodeJSON(t, resp2, &result)
	unread, _ := result["unreadCount"].(float64)
	if unread != 0 {
		t.Errorf("expected unreadCount = 0 after mark-all-read, got %v", result["unreadCount"])
	}
}

func TestMarkRead_SingleNotification(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)
	repo := notifications.NewRepo(db)

	nid := insertTestNotification(t, repo, testutil.DemoUserID, notifications.KindMentioned, "Mentioned in a page")

	resp := testutil.Do(t, srv, http.MethodPatch,
		"/api/v1/users/me/notifications/"+nid,
		map[string]bool{"isRead": true},
		testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	var result map[string]interface{}
	testutil.DecodeJSON(t, resp, &result)
	if result["isRead"] != true {
		t.Errorf("expected isRead=true, got %v", result["isRead"])
	}
}

func TestMarkRead_NotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)

	// Unknown ID — MarkRead is a silent no-op (no row matched), still 200.
	resp := testutil.Do(t, srv, http.MethodPatch,
		"/api/v1/users/me/notifications/00000000-0000-0000-0000-000000000000",
		map[string]bool{"isRead": true},
		testutil.DemoUserID)
	// The handler does not verify the notification belongs to the user before responding.
	testutil.AssertStatus(t, resp, http.StatusOK)
	_ = resp.Body.Close()
}

func TestGetPreferences_ReturnsDefaults(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/users/me/notification-preferences", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	// The preferences list may be empty initially or contain defaults — just verify a valid response.
	var prefs interface{}
	testutil.DecodeJSON(t, resp, &prefs)
	if prefs == nil {
		t.Error("expected non-nil preferences response")
	}
}

func TestUpdatePreference_OK(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPatch,
		"/api/v1/users/me/notification-preferences",
		map[string]interface{}{
			"kind":  notifications.KindTaskAssigned,
			"inApp": true,
			"email": false,
		},
		testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	var pref map[string]interface{}
	testutil.DecodeJSON(t, resp, &pref)
	if pref["kind"] != notifications.KindTaskAssigned {
		t.Errorf("kind = %v, want %s", pref["kind"], notifications.KindTaskAssigned)
	}
	if pref["inApp"] != true {
		t.Errorf("inApp = %v, want true", pref["inApp"])
	}
	if pref["email"] != false {
		t.Errorf("email = %v, want false", pref["email"])
	}
}

// TestUpdatePreference_RejectsUnknownKind covers what retiring `release_due`
// actually required. The endpoint accepted any string, so removing the toggle
// from the settings page would only have hidden the setting: a client — or the
// previous version of the app still open in a tab — could keep writing rows for
// a notification nothing sends.
func TestUpdatePreference_RejectsUnknownKind(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)

	for _, kind := range []string{
		"release_due", // the retired kind
		"taks_assigned",
		"",
	} {
		resp := testutil.Do(t, srv, http.MethodPatch,
			"/api/v1/users/me/notification-preferences",
			map[string]interface{}{"kind": kind, "inApp": true, "email": true},
			testutil.DemoUserID)
		testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
		var body map[string]interface{}
		testutil.DecodeJSON(t, resp, &body)
		if body["code"] != "INVALID_NOTIFICATION_KIND" {
			t.Errorf("kind %q: code = %v, want INVALID_NOTIFICATION_KIND", kind, body["code"])
		}
	}

	// The rejected writes left nothing behind.
	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/users/me/notification-preferences", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)
	var prefs []map[string]interface{}
	testutil.DecodeJSON(t, resp, &prefs)
	for _, p := range prefs {
		if !notifications.ValidKind(p["kind"].(string)) {
			t.Errorf("stored preference for unsendable kind %v", p["kind"])
		}
	}
}

// TestValidKinds_MatchesTheSettingsPage pins the set itself: every kind the
// backend will store must be one the settings page can render, and vice versa.
// The frontend list lives in octbase-frontend/js/realtime.js.
func TestValidKinds_MatchesTheSettingsPage(t *testing.T) {
	want := map[string]bool{
		notifications.KindTaskChanged: true, notifications.KindTaskAssigned: true,
		notifications.KindReviewerSet: true, notifications.KindMentioned: true,
		notifications.KindStatusChanged: true,
	}
	got := notifications.ValidKinds()
	if len(got) != len(want) {
		t.Fatalf("ValidKinds() = %v, want %d kinds", got, len(want))
	}
	for _, k := range got {
		if !want[k] {
			t.Errorf("unexpected kind %q — add it to NOTIFICATION_KINDS in realtime.js too", k)
		}
	}
}
