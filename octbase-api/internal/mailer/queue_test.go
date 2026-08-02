package mailer

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"
)

// flushTimeout bounds every Flush/Close in these tests: they must complete as
// soon as the workers are idle, so a timeout means a real deadlock, not slowness.
const flushTimeout = 10 * time.Second

// gateMailer lets a test control exactly when an inner send completes, so the
// async behaviour can be asserted without sleeping.
type gateMailer struct {
	started chan string   // buffered: signals "worker is inside Send"
	release chan struct{} // closed by the test to let sends finish
	err     error

	mu   sync.Mutex
	sent []string
}

func newGateMailer() *gateMailer {
	return &gateMailer{started: make(chan string, 16), release: make(chan struct{})}
}

func (g *gateMailer) Send(_ context.Context, to, _, _ string) error {
	g.started <- to
	<-g.release
	g.mu.Lock()
	g.sent = append(g.sent, to)
	g.mu.Unlock()
	return g.err
}

func (g *gateMailer) delivered() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.sent...)
}

// countingMailer records deliveries and can fail every send.
type countingMailer struct {
	err error

	mu   sync.Mutex
	sent []string
}

func (c *countingMailer) Send(_ context.Context, to, _, _ string) error {
	c.mu.Lock()
	c.sent = append(c.sent, to)
	c.mu.Unlock()
	return c.err
}

func (c *countingMailer) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.sent)
}

func mustFlush(t *testing.T, q *Queue) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), flushTimeout)
	defer cancel()
	if err := q.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}
}

func mustClose(t *testing.T, q *Queue) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), flushTimeout)
	defer cancel()
	if err := q.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestQueue_SendReturnsBeforeDelivery proves the send left the caller's
// goroutine: Send has already returned while the inner mailer is still parked
// inside its own Send.
func TestQueue_SendReturnsBeforeDelivery(t *testing.T) {
	g := newGateMailer()
	q := NewQueue(g, 1, 4)

	if err := q.Send(context.Background(), "a@x.com", "s", "b"); err != nil {
		t.Fatalf("Send returned an error: %v", err)
	}
	// The worker is now inside the inner Send, blocked on release — and we are
	// executing after Send returned, so delivery is not on this goroutine.
	<-g.started
	if got := g.delivered(); len(got) != 0 {
		t.Fatalf("mail delivered before release: %v", got)
	}

	close(g.release)
	mustFlush(t, q)
	if got := g.delivered(); len(got) != 1 || got[0] != "a@x.com" {
		t.Errorf("delivered = %v, want [a@x.com]", got)
	}
	mustClose(t, q)
}

// TestQueue_DropsWhenFull pins the overflow policy: a full queue drops the mail
// (with a warning) rather than blocking the request that produced it.
func TestQueue_DropsWhenFull(t *testing.T) {
	g := newGateMailer()
	q := NewQueue(g, 1, 1) // one worker, one slot

	ctx := context.Background()
	_ = q.Send(ctx, "in-flight@x.com", "s", "b")
	<-g.started // the single worker is busy; the single slot is free

	_ = q.Send(ctx, "queued@x.com", "s", "b") // fills the slot
	if n := q.Dropped(); n != 0 {
		t.Fatalf("dropped %d before the queue was full", n)
	}

	if err := q.Send(ctx, "overflow@x.com", "s", "b"); err != nil {
		t.Fatalf("Send on a full queue must not error, got %v", err)
	}
	if n := q.Dropped(); n != 1 {
		t.Fatalf("Dropped() = %d, want 1", n)
	}

	close(g.release)
	mustClose(t, q)
	if got := g.delivered(); len(got) != 2 {
		t.Errorf("delivered %v, want the two accepted mails only", got)
	}
}

// TestQueue_CloseDrainsAndStopsWorkers covers the shutdown contract: accepted
// mail is still delivered, the workers exit (no goroutine leak), a later Send is
// dropped instead of panicking on a closed channel, and Close is idempotent.
func TestQueue_CloseDrainsAndStopsWorkers(t *testing.T) {
	before := runtime.NumGoroutine()
	c := &countingMailer{}
	q := NewQueue(c, 2, 8)

	for i := 0; i < 5; i++ {
		if err := q.Send(context.Background(), "x@x.com", "s", "b"); err != nil {
			t.Fatalf("Send: %v", err)
		}
	}
	mustClose(t, q)

	if got := c.count(); got != 5 {
		t.Errorf("delivered %d of 5 accepted mails; Close did not drain", got)
	}
	if err := q.Send(context.Background(), "late@x.com", "s", "b"); err != nil {
		t.Errorf("Send after Close must not error, got %v", err)
	}
	if c.count() != 5 {
		t.Error("a mail was delivered after Close")
	}
	if q.Dropped() != 1 {
		t.Errorf("Dropped() = %d, want 1 (the post-Close send)", q.Dropped())
	}
	mustClose(t, q) // idempotent

	waitForGoroutines(t, before)
}

// TestQueue_SendNeverSurfacesDeliveryFailure is the guarantee the request path
// depends on: a broken relay is logged by the worker, never returned to (or
// timed against) the handler that triggered the mail.
func TestQueue_SendNeverSurfacesDeliveryFailure(t *testing.T) {
	c := &countingMailer{err: errors.New("smtp unavailable")}
	q := NewQueue(c, 1, 4)

	if err := q.Send(context.Background(), "a@x.com", "s", "b"); err != nil {
		t.Fatalf("Send surfaced a delivery error: %v", err)
	}
	mustFlush(t, q)
	if c.count() != 1 {
		t.Errorf("inner mailer attempts = %d, want 1", c.count())
	}
	mustClose(t, q)
}

// TestQueue_IgnoresCallerContext: the request context is cancelled as soon as the
// response is written, so the queue must not carry it into the delivery it just
// moved off that goroutine.
func TestQueue_IgnoresCallerContext(t *testing.T) {
	c := &countingMailer{}
	q := NewQueue(c, 1, 4)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // caller is already gone
	if err := q.Send(ctx, "a@x.com", "s", "b"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	mustFlush(t, q)
	if c.count() != 1 {
		t.Errorf("attempts = %d, want 1 (cancelled caller context must not cancel delivery)", c.count())
	}
	mustClose(t, q)
}

func TestNewQueue_DefaultsOnNonPositiveArgs(t *testing.T) {
	q := NewQueue(&countingMailer{}, 0, 0)
	if cap(q.jobs) != DefaultQueueSize {
		t.Errorf("queue size = %d, want default %d", cap(q.jobs), DefaultQueueSize)
	}
	mustClose(t, q)
}

// waitForGoroutines yields until the goroutine count is back at (or below) the
// baseline, so the leak check does not depend on a sleep. A worker that has
// returned may still be counted for a moment after Close observed its Done.
func waitForGoroutines(t *testing.T, baseline int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got := runtime.NumGoroutine()
		if got <= baseline {
			return
		}
		if time.Now().After(deadline) {
			t.Errorf("goroutines = %d, want <= %d (mail queue leaked workers)", got, baseline)
			return
		}
		runtime.Gosched()
	}
}
