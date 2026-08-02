package scmintegration

import "testing"

// TestValidRepoURL pins the stored-metadata URL check: repositoryUrl is never
// dialed (apiBaseUrl is, guarded by checkOutboundURL), but only well-formed
// http(s) URLs with a host may be stored on a real-provider connection.
func TestValidRepoURL(t *testing.T) {
	valid := []string{
		"https://github.com/org/repo",
		"http://gitlab.example.com/group/project",
		"https://bitbucket.org/team/repo.git",
		"https://github.enterprise.internal:8443/org/repo",
	}
	for _, u := range valid {
		if !validRepoURL(u) {
			t.Errorf("validRepoURL(%q) = false, want true", u)
		}
	}
	invalid := []string{
		"",
		"not a url",
		"ftp://example.com/repo",
		"file:///etc/passwd",
		"gopher://example.com/repo",
		"javascript:alert(1)",
		"ssh://git@github.com/org/repo", // scheme allowlist is http/https only
		"https://",                      // no host
		"/org/repo",                     // relative, no scheme/host
		"http://%zz",                    // unparsable
	}
	for _, u := range invalid {
		if validRepoURL(u) {
			t.Errorf("validRepoURL(%q) = true, want false", u)
		}
	}
}
