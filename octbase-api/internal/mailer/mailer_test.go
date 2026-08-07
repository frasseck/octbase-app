package mailer

import (
	"context"
	"net"
	"strconv"
	"strings"
	"testing"
)

func TestNew_DefaultsToDevMode(t *testing.T) {
	t.Setenv("OCTBASE_SMTP_HOST", "")
	t.Setenv("OCTBASE_SMTP_PORT", "")
	t.Setenv("OCTBASE_SMTP_FROM", "")
	t.Setenv("OCTBASE_SMTP_USER", "")
	t.Setenv("OCTBASE_SMTP_PASS", "")

	m, ok := New().(*SMTPMailer)
	if !ok {
		t.Fatal("New() did not return *SMTPMailer")
	}
	if m.host != "" {
		t.Errorf("host = %q, want empty", m.host)
	}
	if m.port != "587" {
		t.Errorf("port = %q, want default 587", m.port)
	}
	if m.from != "noreply@octbase.io" {
		t.Errorf("from = %q, want default", m.from)
	}
}

func TestSend_DevModeReturnsNil(t *testing.T) {
	m := &SMTPMailer{} // empty host → dev mode
	if err := m.Send(context.Background(), "a@b.com", "Subject", "Body"); err != nil {
		t.Errorf("dev-mode Send should not error, got %v", err)
	}
}

// closedPort returns a port that is guaranteed to be closed, so smtp.SendMail
// fails fast while still executing the full send code path.
func closedPort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().(*net.TCPAddr)
	_ = ln.Close()
	return strconv.Itoa(addr.Port)
}

func TestSend_SMTPPath_WithAuth(t *testing.T) {
	m := &SMTPMailer{host: "127.0.0.1", port: closedPort(t), from: "from@x.com", user: "u", pass: "p"}
	if err := m.Send(context.Background(), "to@x.com", "Subject", "line1\nline2"); err == nil {
		t.Error("expected an error connecting to a closed port")
	}
}

func TestSend_SMTPPath_NoAuth(t *testing.T) {
	m := &SMTPMailer{host: "127.0.0.1", port: closedPort(t), from: "from@x.com"}
	if err := m.Send(context.Background(), "to@x.com", "Subject", "Body"); err == nil {
		t.Error("expected an error connecting to a closed port")
	}
}

func TestSanitizeHeader(t *testing.T) {
	if got := sanitizeHeader("a\r\nbcc: evil@x.com"); got != "abcc: evil@x.com" {
		t.Errorf("sanitizeHeader = %q", got)
	}
}

func TestBuildMessage(t *testing.T) {
	msg := buildMessage("from@x.com", "to@x.com\r\nBcc: evil@x.com", "Subj\nect", "hello\nworld")

	// CRLF is stripped so the attacker text cannot start a new header line.
	if strings.Contains(msg, "\nBcc:") {
		t.Errorf("CRLF injection not sanitized (Bcc became its own header):\n%s", msg)
	}
	for _, want := range []string{
		"MIME-Version: 1.0",
		"multipart/alternative; boundary=boundary42",
		"Subject: Subject", // newline removed -> "Sub" + "ject"
		"text/plain; charset=UTF-8",
		"text/html; charset=UTF-8",
		"hello<br>world", // html body newline -> <br>
		"--boundary42--",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q:\n%s", want, msg)
		}
	}
}

func TestRenderHTML_FontLinkAndEscaping(t *testing.T) {
	html := renderHTML("Task updated", "Open it:\nhttps://dev.octbase.io/#/projects/p1?task=t1 <b>x</b>")

	// A web-safe font stack is applied inline (clients can't load custom fonts).
	if !strings.Contains(html, "-apple-system,BlinkMacSystemFont") {
		t.Errorf("expected system font stack in HTML:\n%s", html)
	}
	// Bare URLs become clickable anchors pointing at the same URL.
	if !strings.Contains(html, `<a href="https://dev.octbase.io/#/projects/p1?task=t1"`) {
		t.Errorf("URL was not linkified:\n%s", html)
	}
	// Body content is HTML-escaped, so injected markup is inert text.
	if strings.Contains(html, "<b>x</b>") {
		t.Errorf("body markup was not escaped:\n%s", html)
	}
	if !strings.Contains(html, "&lt;b&gt;x&lt;/b&gt;") {
		t.Errorf("expected escaped body markup:\n%s", html)
	}
	// Newlines in the body are preserved as <br>.
	if !strings.Contains(html, "Open it:<br>") {
		t.Errorf("newline not converted to <br>:\n%s", html)
	}
}

func TestEnvOrDefault(t *testing.T) {
	t.Setenv("MAILER_TEST_KEY", "")
	if got := envOrDefault("MAILER_TEST_KEY", "fallback"); got != "fallback" {
		t.Errorf("envOrDefault unset = %q, want fallback", got)
	}
	t.Setenv("MAILER_TEST_KEY", "actual")
	if got := envOrDefault("MAILER_TEST_KEY", "fallback"); got != "actual" {
		t.Errorf("envOrDefault set = %q, want actual", got)
	}
}
