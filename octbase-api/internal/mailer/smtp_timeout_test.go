package mailer

import (
	"bufio"
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSMTP is a minimal ESMTP server good enough to exercise the whole send
// conversation (greeting, EHLO, optional AUTH, envelope, DATA, QUIT) and record
// what the client actually sent. It never offers STARTTLS.
type fakeSMTP struct {
	ln            net.Listener
	advertiseAuth bool
	// silent accepts connections and then says nothing, simulating a blackholed
	// relay: the client hangs waiting for the 220 greeting.
	silent bool

	mu    sync.Mutex
	lines []string
	held  []net.Conn
}

func newFakeSMTP(t *testing.T, advertiseAuth, silent bool) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	f := &fakeSMTP{ln: ln, advertiseAuth: advertiseAuth, silent: silent}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			if f.silent {
				f.mu.Lock()
				f.held = append(f.held, conn)
				f.mu.Unlock()
				continue
			}
			f.handle(conn)
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		f.mu.Lock()
		for _, c := range f.held {
			_ = c.Close()
		}
		f.mu.Unlock()
		<-done
	})
	return f
}

func (f *fakeSMTP) addr() (host, port string) {
	host, port, _ = net.SplitHostPort(f.ln.Addr().String())
	return host, port
}

func (f *fakeSMTP) record(s string) {
	f.mu.Lock()
	f.lines = append(f.lines, s)
	f.mu.Unlock()
}

func (f *fakeSMTP) transcript() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.lines, "\n")
}

func (f *fakeSMTP) handle(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	br := bufio.NewReader(conn)
	write := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }

	write("220 fake ESMTP ready")
	inData := false
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if inData {
			if line == "." {
				inData = false
				write("250 2.0.0 queued")
				continue
			}
			f.record("DATA> " + line)
			continue
		}
		f.record(line)
		switch up := strings.ToUpper(line); {
		case strings.HasPrefix(up, "EHLO"):
			if f.advertiseAuth {
				write("250-fake greets you")
				write("250 AUTH PLAIN")
			} else {
				write("250 fake greets you")
			}
		case strings.HasPrefix(up, "HELO"):
			write("250 fake greets you")
		case strings.HasPrefix(up, "AUTH"):
			write("235 2.7.0 authenticated")
		case up == "DATA":
			inData = true
			write("354 end data with <CR><LF>.<CR><LF>")
		case up == "QUIT":
			write("221 bye")
			return
		default: // MAIL FROM / RCPT TO / anything else
			write("250 2.0.0 ok")
		}
	}
}

// TestSend_DeliversOverSMTP guards the hand-rolled conversation that replaced
// smtp.SendMail (which cannot be used: it dials itself, with no timeouts and no
// context).
func TestSend_DeliversOverSMTP(t *testing.T) {
	f := newFakeSMTP(t, false, false)
	host, port := f.addr()
	m := &SMTPMailer{host: host, port: port, from: "from@x.com"}

	if err := m.Send(context.Background(), "to@x.com", "Subj", "Body line"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	tr := f.transcript()
	for _, want := range []string{
		"EHLO",
		"MAIL FROM:<from@x.com>",
		"RCPT TO:<to@x.com>",
		"DATA",
		"DATA> Subject: Subj",
		"DATA> Body line",
		"QUIT",
	} {
		if !strings.Contains(tr, want) {
			t.Errorf("transcript missing %q:\n%s", want, tr)
		}
	}
}

// TestSend_AuthenticatesWhenOffered covers the credentialed path end to end.
func TestSend_AuthenticatesWhenOffered(t *testing.T) {
	f := newFakeSMTP(t, true, false)
	host, port := f.addr()
	m := &SMTPMailer{host: host, port: port, from: "from@x.com", user: "u", pass: "p"}

	if err := m.Send(context.Background(), "to@x.com", "Subj", "Body"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if tr := f.transcript(); !strings.Contains(tr, "AUTH PLAIN") {
		t.Errorf("expected an AUTH PLAIN command:\n%s", tr)
	}
}

// TestSend_ErrorsWhenAuthConfiguredButUnsupported: silently sending unauthenticated
// mail would look like success while the relay rejects it.
func TestSend_ErrorsWhenAuthConfiguredButUnsupported(t *testing.T) {
	f := newFakeSMTP(t, false, false)
	host, port := f.addr()
	m := &SMTPMailer{host: host, port: port, from: "from@x.com", user: "u", pass: "p"}

	if err := m.Send(context.Background(), "to@x.com", "Subj", "Body"); err == nil {
		t.Error("expected an error when credentials are set but AUTH is unsupported")
	}
}

// TestSend_HonoursContextDeadline is the regression test for the original bug:
// Send discarded its context and used smtp.SendMail's dialer-less connect, so a
// blackholed relay held the caller for the OS connect timeout (~130s), well past
// the server's 60s WriteTimeout.
func TestSend_HonoursContextDeadline(t *testing.T) {
	f := newFakeSMTP(t, false, true) // accepts, then never speaks
	host, port := f.addr()
	m := &SMTPMailer{host: host, port: port, from: "from@x.com"}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := m.Send(ctx, "to@x.com", "Subj", "Body")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error from a silent SMTP host")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want it to wrap context.DeadlineExceeded", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("Send blocked for %v; the context deadline was not honoured", elapsed)
	}
}

// TestSend_HonoursCancelledContext: an already-cancelled context must abort at
// the dial rather than opening a connection.
func TestSend_HonoursCancelledContext(t *testing.T) {
	f := newFakeSMTP(t, false, true)
	host, port := f.addr()
	m := &SMTPMailer{host: host, port: port, from: "from@x.com"}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := m.Send(ctx, "to@x.com", "Subj", "Body")
	if err == nil {
		t.Fatal("expected an error for a cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want it to wrap context.Canceled", err)
	}
}

// TestSMTPTimeoutsAreBoundedBelowServerWriteTimeout keeps the constants honest:
// a send must not be able to outlive the request that triggered it (main.go sets
// http.Server WriteTimeout to 60s).
func TestSMTPTimeoutsAreBoundedBelowServerWriteTimeout(t *testing.T) {
	const serverWriteTimeout = 60 * time.Second
	if dialTimeout <= 0 || dialTimeout >= serverWriteTimeout {
		t.Errorf("dialTimeout = %v, want a positive value well below %v", dialTimeout, serverWriteTimeout)
	}
	if opTimeout <= 0 || opTimeout >= serverWriteTimeout {
		t.Errorf("opTimeout = %v, want a positive value well below %v", opTimeout, serverWriteTimeout)
	}
	if sendTimeout < dialTimeout || sendTimeout >= serverWriteTimeout {
		t.Errorf("sendTimeout = %v, want between dialTimeout and %v", sendTimeout, serverWriteTimeout)
	}
}
