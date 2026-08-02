package sse

import (
	"strings"
	"testing"
	"time"
)

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", msg)
}

func recv(t *testing.T, c *client) ([]byte, bool) {
	t.Helper()
	select {
	case b, ok := <-c.Chan():
		return b, ok
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for message")
		return nil, false
	}
}

func TestHub_PublishDeliversToMatchingProject(t *testing.T) {
	h := NewHub()
	go h.Run()

	c := h.Subscribe("p1", "u1")
	waitFor(t, func() bool { return len(h.Presence("p1")) == 1 }, "client to register")

	h.Publish("p1", map[string]interface{}{"type": "ping", "n": 1})
	msg, ok := recv(t, c)
	if !ok {
		t.Fatal("channel closed unexpectedly")
	}
	got := string(msg)
	if !strings.HasPrefix(got, "data: ") || !strings.HasSuffix(got, "\n\n") {
		t.Errorf("unexpected SSE framing: %q", got)
	}
	if !strings.Contains(got, `"type":"ping"`) {
		t.Errorf("payload missing: %q", got)
	}
}

func TestHub_PublishIgnoresOtherProjects(t *testing.T) {
	h := NewHub()
	go h.Run()

	c := h.Subscribe("p1", "u1")
	waitFor(t, func() bool { return len(h.Presence("p1")) == 1 }, "client to register")

	h.Publish("other", map[string]interface{}{"type": "nope"})
	select {
	case msg := <-c.Chan():
		t.Fatalf("should not receive cross-project event, got %q", msg)
	case <-time.After(150 * time.Millisecond):
		// expected: nothing delivered
	}
}

func TestHub_Presence(t *testing.T) {
	h := NewHub()
	go h.Run()

	// Empty project → non-nil empty slice.
	if v := h.Presence("empty"); v == nil || len(v) != 0 {
		t.Fatalf("expected empty non-nil presence, got %#v", v)
	}

	h.Subscribe("p1", "u1")
	waitFor(t, func() bool { return len(h.Presence("p1")) == 1 }, "client to register")

	if h.Presence("p1")[0]["userId"] != "u1" {
		t.Errorf("presence userId = %v, want u1", h.Presence("p1")[0]["userId"])
	}
}

func TestHub_UnsubscribeClosesChannel(t *testing.T) {
	h := NewHub()
	go h.Run()

	c := h.Subscribe("p1", "u1")
	waitFor(t, func() bool { return len(h.Presence("p1")) == 1 }, "client to register")

	h.Unsubscribe(c)
	waitFor(t, func() bool { return len(h.Presence("p1")) == 0 }, "client to unregister")

	// The client channel is closed on unsubscribe.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-c.Chan():
			if !ok {
				return // closed as expected
			}
		case <-deadline:
			t.Fatal("channel was not closed after Unsubscribe")
		}
	}
}

func TestHub_PublishWithNoSubscribersDoesNotBlock(t *testing.T) {
	h := NewHub()
	go h.Run()
	done := make(chan struct{})
	go func() {
		h.Publish("nobody", map[string]interface{}{"x": 1})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked with no subscribers")
	}
}
