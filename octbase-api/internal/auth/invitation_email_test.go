package auth

import (
	"strings"
	"testing"
)

func TestInvitationEmail(t *testing.T) {
	const url = "https://app.example/#/invitations/tok/accept"

	subj, body := invitationEmail("Acme", url)
	if !strings.Contains(subj, "Acme") {
		t.Errorf("project subject = %q, want it to mention the project", subj)
	}
	if !strings.Contains(body, url) || !strings.Contains(body, "Acme") {
		t.Errorf("project body = %q, want it to contain the project and accept URL", body)
	}

	subj, body = invitationEmail("", url)
	if strings.Contains(subj, "Acme") || subj == "" {
		t.Errorf("platform subject = %q, want a generic non-empty subject", subj)
	}
	if !strings.Contains(body, url) {
		t.Errorf("platform body = %q, want it to contain the accept URL", body)
	}
}
