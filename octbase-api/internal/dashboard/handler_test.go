package dashboard_test

import (
	"net/http"
	"testing"

	"github.com/octbase/octbase-api/internal/testutil"
)

func TestGetPreferences_DefaultsOnFirstRead(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/users/me/preferences", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	var p map[string]interface{}
	testutil.DecodeJSON(t, resp, &p)
	if p["language"] != "en" {
		t.Errorf("expected default language 'en', got %v", p["language"])
	}
	if p["theme"] != "system" {
		t.Errorf("expected default theme 'system', got %v", p["theme"])
	}
}

func TestUpdatePreferences_RoundTrip(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/users/me/preferences",
		map[string]string{"language": "de", "theme": "dark"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	var p map[string]interface{}
	testutil.DecodeJSON(t, resp, &p)
	if p["language"] != "de" || p["theme"] != "dark" {
		t.Fatalf("expected {de, dark}, got %v", p)
	}

	// Reading again returns the persisted values, not the defaults.
	resp2 := testutil.Do(t, srv, http.MethodGet, "/api/v1/users/me/preferences", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp2, http.StatusOK)
	var p2 map[string]interface{}
	testutil.DecodeJSON(t, resp2, &p2)
	if p2["language"] != "de" || p2["theme"] != "dark" {
		t.Fatalf("expected persisted {de, dark}, got %v", p2)
	}
}

func TestUpdatePreferences_InvalidLanguageRejected(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/users/me/preferences",
		map[string]string{"language": "fr", "theme": "dark"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)

	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "INVALID_PREFERENCE_VALUE" {
		t.Errorf("expected code INVALID_PREFERENCE_VALUE, got %v", e["code"])
	}
}

func TestUpdatePreferences_InvalidThemeRejected(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/users/me/preferences",
		map[string]string{"language": "en", "theme": "neon"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
}

func TestPreferences_ScopedToCallingUser(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/users/me/preferences",
		map[string]string{"language": "de", "theme": "octopus"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	resp2 := testutil.Do(t, srv, http.MethodGet, "/api/v1/users/me/preferences", nil, testutil.SecondUserID)
	testutil.AssertStatus(t, resp2, http.StatusOK)
	var p map[string]interface{}
	testutil.DecodeJSON(t, resp2, &p)
	if p["language"] != "en" || p["theme"] != "system" {
		t.Fatalf("expected SecondUserID to have untouched defaults, got %v", p)
	}
}

func TestGetPreferences_DefaultsToAgileTerminology(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodGet, "/api/v1/users/me/preferences", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	var p map[string]interface{}
	testutil.DecodeJSON(t, resp, &p)
	if p["terminology"] != "AGILE" {
		t.Errorf("expected default terminology 'AGILE', got %v", p["terminology"])
	}
}

func TestUpdatePreferences_TerminologyRoundTrip(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/users/me/preferences",
		map[string]string{"language": "en", "theme": "system", "terminology": "CLASSIC"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	resp2 := testutil.Do(t, srv, http.MethodGet, "/api/v1/users/me/preferences", nil, testutil.DemoUserID)
	testutil.AssertStatus(t, resp2, http.StatusOK)
	var p map[string]interface{}
	testutil.DecodeJSON(t, resp2, &p)
	if p["terminology"] != "CLASSIC" {
		t.Fatalf("expected persisted terminology 'CLASSIC', got %v", p)
	}
}

func TestUpdatePreferences_RejectsUnknownTerminology(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/users/me/preferences",
		map[string]string{"language": "en", "theme": "system", "terminology": "WATERFALL"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusUnprocessableEntity)
	var e map[string]interface{}
	testutil.DecodeJSON(t, resp, &e)
	if e["code"] != "INVALID_PREFERENCE_VALUE" {
		t.Errorf("code = %v, want INVALID_PREFERENCE_VALUE", e["code"])
	}
}

// A client that predates the vocabulary preference (the mobile app, a cached
// frontend) PATCHes only language and theme. That must not silently reset the
// user's vocabulary back to agile — an omitted field means "leave it alone".
func TestUpdatePreferences_OmittedTerminologyIsPreserved(t *testing.T) {
	db := testutil.NewTestDB(t)
	if db == nil {
		return
	}
	srv := testutil.NewTestServer(t, db)

	resp := testutil.Do(t, srv, http.MethodPatch, "/api/v1/users/me/preferences",
		map[string]string{"language": "en", "theme": "system", "terminology": "CLASSIC"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp, http.StatusOK)

	resp2 := testutil.Do(t, srv, http.MethodPatch, "/api/v1/users/me/preferences",
		map[string]string{"language": "de", "theme": "dark"}, testutil.DemoUserID)
	testutil.AssertStatus(t, resp2, http.StatusOK)
	var p map[string]interface{}
	testutil.DecodeJSON(t, resp2, &p)
	if p["terminology"] != "CLASSIC" {
		t.Fatalf("omitted terminology must be preserved, got %v", p)
	}
}
