package auditlog_test

import (
	"net/http"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
)

func TestList_SuperAdmin_OK(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	// Trigger an audit-logged action: Super Admin creates a user.
	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/users", map[string]interface{}{
		"email":       "audited@test.dev",
		"displayName": "Audited User",
		"password":    "securepass123",
		"globalRole":  "USER",
	}, testutil.SuperAdminUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	_ = resp.Body.Close()

	resp = testutil.Do(t, srv, http.MethodGet, "/api/v1/audit-logs", nil, testutil.SuperAdminUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	var result map[string]interface{}
	testutil.DecodeJSON(t, resp, &result)

	logs, ok := result["logs"].([]interface{})
	if !ok || len(logs) == 0 {
		t.Fatalf("expected non-empty logs, got %v", result["logs"])
	}

	found := false
	for _, l := range logs {
		entry := l.(map[string]interface{})
		if entry["action"] == "USER_CREATED" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected a USER_CREATED entry in audit logs")
	}
}

func TestList_ActionFilter(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPost, "/api/v1/users", map[string]interface{}{
		"email":       "audited2@test.dev",
		"displayName": "Audited User Two",
		"password":    "securepass123",
		"globalRole":  "USER",
	}, testutil.SuperAdminUserID)
	testutil.AssertStatus(t, resp, http.StatusCreated)
	_ = resp.Body.Close()

	resp = testutil.Do(t, srv, http.MethodGet, "/api/v1/audit-logs?action=USER_CREATED", nil, testutil.SuperAdminUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	var result map[string]interface{}
	testutil.DecodeJSON(t, resp, &result)

	logs, _ := result["logs"].([]interface{})
	if len(logs) == 0 {
		t.Fatal("expected at least one USER_CREATED log")
	}
	for _, l := range logs {
		entry := l.(map[string]interface{})
		if entry["action"] != "USER_CREATED" {
			t.Errorf("expected only USER_CREATED entries, got %v", entry["action"])
		}
	}
}

func TestList_NonSuperAdmin_Forbidden(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/audit-logs", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusForbidden)
	_ = resp.Body.Close()
}

func TestList_AnonymousUser_Unauthorized(t *testing.T) {
	db := testutil.NewTestDB(t)
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/audit-logs", nil, "")
	testutil.AssertStatus(t, resp, http.StatusUnauthorized)
	_ = resp.Body.Close()
}
