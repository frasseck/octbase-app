package notifications_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/octbase/octbase-api/internal/notifications"
	"github.com/octbase/octbase-api/internal/testutil"
)

// These exercise the handler error branches directly (no middleware), so the
// request carries no authenticated user in its context.

func TestHandlerList_Unauthorized(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	h := notifications.NewHandler(db, notifications.NewRepo(db))
	rec := httptest.NewRecorder()
	h.List(rec, httptest.NewRequest(http.MethodGet, "/api/v1/users/me/notifications", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestHandlerMarkRead_InvalidJSON(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	h := notifications.NewHandler(db, notifications.NewRepo(db))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/users/me/notifications/x", strings.NewReader("{"))
	h.MarkRead(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandlerUpdatePreference_InvalidJSON(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	h := notifications.NewHandler(db, notifications.NewRepo(db))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/users/me/notification-preferences", strings.NewReader("{"))
	h.UpdatePreference(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
