package mailer

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
)

// Async delivery defaults. Two workers are enough to keep an SMTP relay busy
// without opening a connection per notification, and 256 queued mails is far
// more than a healthy instance ever has outstanding (a mail is normally handed
// to the relay in milliseconds) while still being a hard bound on memory.
const (
	DefaultWorkers   = 2
	DefaultQueueSize = 256
)

// job is one queued outbound mail. The originating request's context is
// deliberately not carried: it is cancelled as soon as the response is written,
// which would cancel the delivery we just moved off that goroutine.
type job struct{ to, subject, body string }

// Queue is a Mailer decorator that hands delivery to a small fixed pool of
// background workers, so sending never runs on a request goroutine. SMTP is a
// multi-round-trip network conversation against a host we do not control; inline
// sends made every task create/edit/comment as slow as the slowest relay, and a
// blackholed host stalled them past the server's write timeout.
//
// The queue is bounded and never blocks the caller: when it is full the mail is
// dropped with a warning. Notifications are a courtesy channel — losing one is
// strictly better than wedging the request that triggered it, and a full queue
// means the relay is already broken.
//
// Send always returns nil: by the time the caller returns, nothing is known
// about delivery yet. Failures are logged by the worker instead. This matches
// what every call site already did with the error (log it, never fail the
// request).
type Queue struct {
	inner   Mailer
	jobs    chan job
	workers sync.WaitGroup
	// pending counts mails accepted but not yet attempted, so Flush/Close can
	// wait for a quiet queue deterministically instead of sleeping.
	pending sync.WaitGroup

	// mu guards the closed flag against the jobs channel: enqueue holds it for
	// reading, Close holds it for writing while closing the channel, so a send
	// can never race a close (which would panic).
	mu      sync.RWMutex
	closed  bool
	dropped atomic.Int64
}

// NewQueue wraps inner in a bounded async queue and starts its workers.
// Non-positive workers or size fall back to the defaults. The caller owns the
// returned Queue's lifecycle and must Close it (see main.go).
func NewQueue(inner Mailer, workers, size int) *Queue {
	if workers <= 0 {
		workers = DefaultWorkers
	}
	if size <= 0 {
		size = DefaultQueueSize
	}
	q := &Queue{inner: inner, jobs: make(chan job, size)}
	q.workers.Add(workers)
	for i := 0; i < workers; i++ {
		go q.run()
	}
	return q
}

// run drains the queue until it is closed and empty.
func (q *Queue) run() {
	defer q.workers.Done()
	for j := range q.jobs {
		q.deliver(j)
	}
}

// deliver sends one queued mail under its own timeout, independent of the
// request that produced it.
func (q *Queue) deliver(j job) {
	defer q.pending.Done()
	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()
	if err := q.inner.Send(ctx, j.to, j.subject, j.body); err != nil {
		slog.Error("queued email send failed", "subject", j.subject, "error", err)
	}
}

// Send accepts a mail for background delivery. It never blocks and never
// returns an error; a full queue drops the mail with a warning.
func (q *Queue) Send(_ context.Context, to, subject, body string) error {
	q.mu.RLock()
	defer q.mu.RUnlock()
	if q.closed {
		slog.Warn("email dropped: mail queue closed", "subject", subject)
		q.dropped.Add(1)
		return nil
	}
	q.pending.Add(1)
	select {
	case q.jobs <- job{to: to, subject: subject, body: body}:
		return nil
	default:
		q.pending.Done()
		q.dropped.Add(1)
		slog.Warn("email dropped: mail queue full",
			"subject", subject, "queueSize", cap(q.jobs), "droppedTotal", q.dropped.Load())
		return nil
	}
}

// Dropped reports how many mails have been dropped because the queue was full
// or already closed. Exposed for tests and for operational visibility.
func (q *Queue) Dropped() int64 { return q.dropped.Load() }

// Flush waits until every accepted mail has been attempted, or ctx expires.
// Tests use it to await async delivery deterministically; production only needs
// it via Close.
func (q *Queue) Flush(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		q.pending.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close stops accepting mail, drains what was already accepted, and waits for
// the workers to exit so shutdown leaks no goroutines. It is safe to call more
// than once; later Sends are dropped with a warning rather than panicking on a
// closed channel. Returns ctx.Err() if the drain outruns ctx (each in-flight
// send is still bounded by the mailer's own timeouts).
func (q *Queue) Close(ctx context.Context) error {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return nil
	}
	q.closed = true
	close(q.jobs)
	q.mu.Unlock()

	done := make(chan struct{})
	go func() {
		q.workers.Wait()
		close(done)
	}()
	select {
	case <-done:
		if n := q.dropped.Load(); n > 0 {
			slog.Warn("mail queue closed with drops", "dropped", n)
		}
		return nil
	case <-ctx.Done():
		slog.Warn("mail queue did not drain before shutdown deadline")
		return ctx.Err()
	}
}
