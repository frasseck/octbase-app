package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestVersionHandler verifies the /api/v1/version endpoint returns expected fields.
func TestVersionHandler(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/version", nil)
	w := httptest.NewRecorder()
	versionHandler("1.2.3")(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var result map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result["version"] != "1.2.3" {
		t.Errorf("version = %v, want %q", result["version"], "1.2.3")
	}
	if result["name"] != "Octbase API" {
		t.Errorf("name = %v, want 'Octbase API'", result["name"])
	}
}

// TestConfigHandler verifies /api/v1/config reflects the feature-flag values
// and edition it was constructed with, in both states (the SPA reads this at
// boot to show/hide optional views; the flags must round-trip exactly).
func TestConfigHandler(t *testing.T) {
	for _, want := range []bool{true, false} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
		w := httptest.NewRecorder()
		edition := "ENTERPRISE"
		if !want {
			edition = "TEAM"
		}
		configHandler(want, want, edition, "1.2.3", 5, 10, 512)(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("taskView=%v: expected 200, got %d", want, w.Code)
		}
		var result struct {
			Features map[string]bool `json:"features"`
			Edition  string          `json:"edition"`
			Version  string          `json:"version"`
			Limits   map[string]int  `json:"limits"`
		}
		if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if got, ok := result.Features["taskView"]; !ok || got != want {
			t.Errorf("features.taskView = %v (present=%v), want %v", got, ok, want)
		}
		if got, ok := result.Features["jiraCsvImport"]; !ok || got != want {
			t.Errorf("features.jiraCsvImport = %v (present=%v), want %v", got, ok, want)
		}
		if result.Edition != edition {
			t.Errorf("edition = %q, want %q", result.Edition, edition)
		}
		if result.Version != "1.2.3" {
			t.Errorf("version = %q, want %q", result.Version, "1.2.3")
		}
		wantLimits := map[string]int{"maxUsers": 5, "maxUploadMb": 10, "maxUserStorageMb": 512}
		for k, v := range wantLimits {
			if got, ok := result.Limits[k]; !ok || got != v {
				t.Errorf("limits.%s = %v (present=%v), want %v", k, got, ok, v)
			}
		}
	}
}

// TestEditionFromEnv verifies OCTBASE_EDITION parsing: valid values pass
// through case-insensitively, missing and invalid values fall back to
// ENTERPRISE (full app), matching the default-on stance of the feature flags.
func TestEditionFromEnv(t *testing.T) {
	cases := []struct {
		env  string
		want string
	}{
		{"TEAM", "TEAM"},
		{"BUSINESS", "BUSINESS"},
		{"ENTERPRISE", "ENTERPRISE"},
		{"business", "BUSINESS"},
		{" enterprise ", "ENTERPRISE"},
		{"", "ENTERPRISE"},
		{"XL", "ENTERPRISE"},
	}
	for _, tc := range cases {
		t.Setenv("OCTBASE_EDITION", tc.env)
		if got := editionFromEnv(); got != tc.want {
			t.Errorf("editionFromEnv() with OCTBASE_EDITION=%q = %q, want %q", tc.env, got, tc.want)
		}
	}
}

// TestJiraImportEnabled verifies the Jira import add-on matrix: included in
// ENTERPRISE regardless of the option flag, activatable in BUSINESS only via
// OCTBASE_OPTION_JIRA_IMPORT=true (default off), and never active in TEAM
// even when the flag is set.
func TestJiraImportEnabled(t *testing.T) {
	cases := []struct {
		edition string
		option  string
		want    bool
	}{
		{editionEnterprise, "", true},
		{editionEnterprise, "true", true},
		{editionBusiness, "", false},
		{editionBusiness, "true", true},
		{editionBusiness, "TRUE", true},
		{editionBusiness, " true ", true},
		{editionBusiness, "false", false},
		{editionBusiness, "yes", false},
		{editionTeam, "", false},
		{editionTeam, "true", false},
	}
	for _, tc := range cases {
		t.Setenv("OCTBASE_OPTION_JIRA_IMPORT", tc.option)
		if got := jiraImportEnabled(tc.edition); got != tc.want {
			t.Errorf("jiraImportEnabled(%q) with OCTBASE_OPTION_JIRA_IMPORT=%q = %v, want %v", tc.edition, tc.option, got, tc.want)
		}
	}
}

// TestEnumsHandler verifies the /api/v1/meta/enums endpoint returns all expected enum categories.
func TestEnumsHandler(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/meta/enums", nil)
	w := httptest.NewRecorder()
	enumsHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var result map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	for _, key := range []string{
		"taskStatuses", "taskPriorities", "taskTypes",
		"roles", "relationTypes", "visibilities",
		"releaseStatuses", "pageStatuses", "branchTypes", "scmProviders",
	} {
		if result[key] == nil {
			t.Errorf("expected enum key %q in response", key)
		}
	}
	statuses, _ := result["taskStatuses"].([]interface{})
	if len(statuses) == 0 {
		t.Error("expected at least one taskStatus")
	}
}
