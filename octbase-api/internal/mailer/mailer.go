// Package mailer provides email delivery via SMTP with a dev-mode fallback
// that logs emails to stdout when SMTP is not configured.
package mailer

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net"
	"net/smtp"
	"os"
	"regexp"
	"strings"
	"time"
)

// SMTP timeouts. Without them a blackholed SMTP host (packets dropped, no RST)
// stalls on the OS connect timeout — ~130s on Linux — which is well past the
// API's 60s http.Server WriteTimeout, so an inline notification mail could hang
// a whole task edit. Each bound is deliberately generous for a healthy relay and
// short relative to a request's lifetime.
const (
	// dialTimeout bounds establishing the TCP connection.
	dialTimeout = 10 * time.Second
	// opTimeout bounds every single read/write of the SMTP conversation
	// (greeting, EHLO, STARTTLS, AUTH, MAIL/RCPT/DATA, body, QUIT).
	opTimeout = 10 * time.Second
	// sendTimeout bounds the whole session, so a server that answers every
	// command just inside opTimeout still cannot hold the caller forever.
	sendTimeout = 30 * time.Second
)

// Mailer is the interface for sending email.
type Mailer interface {
	Send(ctx context.Context, to, subject, body string) error
}

// SMTPMailer sends email via SMTP. Falls back to stdout logging when SMTP is
// not configured (OCTBASE_SMTP_HOST is unset).
type SMTPMailer struct {
	host string
	port string
	from string
	user string
	pass string
}

// New creates a Mailer from environment variables. If OCTBASE_SMTP_HOST is
// not set, the mailer logs to stdout instead of sending.
func New() Mailer {
	return &SMTPMailer{
		host: os.Getenv("OCTBASE_SMTP_HOST"),
		port: envOrDefault("OCTBASE_SMTP_PORT", "587"),
		from: envOrDefault("OCTBASE_SMTP_FROM", "noreply@beyags.com"),
		user: os.Getenv("OCTBASE_SMTP_USER"),
		pass: os.Getenv("OCTBASE_SMTP_PASS"),
	}
}

// Send delivers an email or logs it in dev mode. It honours ctx (cancellation
// and deadline) and applies its own dial/per-operation/overall timeouts, so no
// caller can be pinned to an unresponsive SMTP host.
func (m *SMTPMailer) Send(ctx context.Context, to, subject, body string) error {
	if m.host == "" {
		// Never log the body: invitation mails carry a single-use accept token,
		// and container logs are neither access-controlled like the DB nor
		// covered by the retention purge. If this fires in production because
		// OCTBASE_SMTP_HOST was forgotten, only the recipient leaks, not a
		// credential. Dev mail inspection is Mailpit's job (podman-compose.dev.yml).
		slog.Info("[mailer dev-mode] email suppressed (no SMTP host configured)",
			"to", to, "subject", subject, "bodyBytes", len(body))
		return nil
	}
	msg := buildMessage(m.from, to, subject, body)
	addr := net.JoinHostPort(m.host, m.port)

	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()

	conn, err := (&net.Dialer{Timeout: dialTimeout}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("smtp dial %s: %w", addr, err)
	}
	// Closing the connection is what unblocks a read/write that is already in
	// flight, so the ctx watchdog closes it rather than trying to interrupt the
	// smtp.Client. The done channel stops the watchdog when the send finishes,
	// so the goroutine never outlives the call.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()

	if err := m.deliver(&deadlineConn{Conn: conn, timeout: opTimeout}, to, msg); err != nil {
		// A cancelled/expired ctx surfaces as a use-of-closed-connection error
		// from whichever operation was in flight; report the real cause.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("smtp send to %s: %w", addr, ctxErr)
		}
		return err
	}
	return nil
}

// deliver runs the SMTP conversation over an already-established connection:
// EHLO, STARTTLS when offered, AUTH when credentials are configured, then the
// envelope and message. It mirrors smtp.SendMail (which cannot be used because
// it dials itself, with no timeouts and no context).
func (m *SMTPMailer) deliver(conn net.Conn, to, msg string) error {
	c, err := smtp.NewClient(conn, m.host)
	if err != nil {
		return fmt.Errorf("smtp greeting: %w", err)
	}
	defer func() { _ = c.Close() }()

	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: m.host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("smtp starttls: %w", err)
		}
	}
	if m.user != "" {
		if ok, _ := c.Extension("AUTH"); !ok {
			return errors.New("smtp: server does not support AUTH but credentials are configured")
		}
		// PlainAuth itself refuses to hand credentials to a non-TLS,
		// non-localhost server, so a relay that skipped STARTTLS fails here
		// instead of leaking the password.
		if err := c.Auth(smtp.PlainAuth("", m.user, m.pass, m.host)); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := c.Mail(m.from); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("smtp rcpt to: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := w.Write([]byte(msg)); err != nil {
		return fmt.Errorf("smtp write body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp close body: %w", err)
	}
	return c.Quit()
}

// deadlineConn refreshes the socket deadline before every read and write, giving
// each step of the SMTP conversation its own timeout budget instead of one
// deadline for the whole session (which a slow-but-alive relay would trip).
type deadlineConn struct {
	net.Conn
	timeout time.Duration
}

// The Read/Write calls below must stay qualified as c.Conn.*: unqualified they
// would resolve to these same methods and recurse forever. Only the deadline
// calls, which are not overridden here, are reached through the embedded field.
func (c *deadlineConn) Read(b []byte) (int, error) {
	_ = c.SetReadDeadline(time.Now().Add(c.timeout))
	return c.Conn.Read(b)
}

func (c *deadlineConn) Write(b []byte) (int, error) {
	_ = c.SetWriteDeadline(time.Now().Add(c.timeout))
	return c.Conn.Write(b)
}

// sanitizeHeader removes CR and LF characters to prevent SMTP header injection.
func sanitizeHeader(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", "")
	return s
}

func buildMessage(from, to, subject, body string) string {
	to = sanitizeHeader(to)
	subject = sanitizeHeader(subject)
	from = sanitizeHeader(from)

	htmlBody := renderHTML(subject, body)
	var sb strings.Builder
	sb.WriteString("MIME-Version: 1.0\r\n")
	sb.WriteString("Content-Type: multipart/alternative; boundary=boundary42\r\n")
	sb.WriteString("From: " + from + "\r\n")
	sb.WriteString("To: " + to + "\r\n")
	sb.WriteString("Subject: " + subject + "\r\n\r\n")
	sb.WriteString("--boundary42\r\n")
	sb.WriteString("Content-Type: text/plain; charset=UTF-8\r\n\r\n")
	sb.WriteString(body + "\r\n")
	sb.WriteString("--boundary42\r\n")
	sb.WriteString("Content-Type: text/html; charset=UTF-8\r\n\r\n")
	sb.WriteString(htmlBody + "\r\n")
	sb.WriteString("--boundary42--\r\n")
	return sb.String()
}

// emailFontStack is a cross-client web-safe system font stack. Email clients
// can't load custom fonts reliably, so we lean on the recipient's OS fonts.
const emailFontStack = "-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif"

// urlRE matches bare http(s) URLs so they can be turned into clickable links in
// the HTML part. It stops at whitespace or an angle bracket.
var urlRE = regexp.MustCompile(`https?://[^\s<]+`)

// renderHTML wraps a plain-text body in a styled, responsive HTML email. The
// body is HTML-escaped (so task titles etc. can't inject markup), newlines
// become <br>, and bare URLs become styled <a> links. All styling is inline
// because email clients strip <style> blocks and don't load external CSS.
func renderHTML(subject, body string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en" style="margin:0;padding:0;">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="x-apple-disable-message-reformatting">
</head>
<body style="margin:0;padding:0;width:100%%;background-color:#f4f5f7;font-family:%[1]s;-webkit-font-smoothing:antialiased;">
<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" border="0" style="width:100%%;background-color:#f4f5f7;">
<tr><td align="center" style="padding:32px 16px;">
<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" border="0" style="width:100%%;max-width:680px;background-color:#ffffff;border-radius:14px;overflow:hidden;border:1px solid #e6e8eb;box-shadow:0 1px 3px rgba(16,24,40,0.06);">
<tr><td style="background-color:#005A42;padding:24px 36px;">
<span style="font-family:%[1]s;font-size:20px;font-weight:700;color:#ffffff;letter-spacing:0.4px;">Octbase</span>
</td></tr>
<tr><td style="padding:36px;font-family:%[1]s;color:#1a1a1a;">
<h1 style="margin:0 0 20px;font-family:%[1]s;font-size:22px;line-height:1.3;font-weight:600;color:#005A42;">%[2]s</h1>
<div style="font-family:%[1]s;font-size:15px;line-height:1.65;color:#33373b;">%[3]s</div>
</td></tr>
<tr><td style="padding:20px 36px 28px;border-top:1px solid #eef0f2;font-family:%[1]s;">
<p style="margin:0;font-family:%[1]s;font-size:12px;line-height:1.5;color:#9aa0a6;">This is an automated message from Octbase. Please do not reply.</p>
</td></tr>
</table>
</td></tr>
</table>
</body>
</html>`, emailFontStack, html.EscapeString(subject), linkifyHTML(body))
}

// linkifyHTML HTML-escapes plain text and turns bare http(s) URLs into styled
// anchors, preserving line breaks as <br>.
func linkifyHTML(text string) string {
	var sb strings.Builder
	last := 0
	for _, loc := range urlRE.FindAllStringIndex(text, -1) {
		sb.WriteString(escapeWithBreaks(text[last:loc[0]]))
		u := html.EscapeString(text[loc[0]:loc[1]])
		sb.WriteString(`<a href="` + u + `" style="color:#008F69;font-weight:600;text-decoration:underline;word-break:break-all;">` + u + `</a>`)
		last = loc[1]
	}
	sb.WriteString(escapeWithBreaks(text[last:]))
	return sb.String()
}

// escapeWithBreaks HTML-escapes a fragment and converts newlines to <br>.
func escapeWithBreaks(s string) string {
	return strings.ReplaceAll(html.EscapeString(s), "\n", "<br>")
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
