package shared

import (
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

type fixedWindowEntry struct {
	count    int
	windowAt time.Time
}

type ipRateLimiter struct {
	mu      sync.Mutex
	entries map[string]*fixedWindowEntry
}

func newIPRateLimiter() *ipRateLimiter {
	return &ipRateLimiter{entries: make(map[string]*fixedWindowEntry)}
}

// allow returns true if the IP is within the rate limit.
// Uses a fixed-window counter per IP. Evicts stale entries when the map grows
// large to bound memory use.
func (l *ipRateLimiter) allow(ip string, maxReqs int, window time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	e, ok := l.entries[ip]
	if !ok || now.After(e.windowAt.Add(window)) {
		if len(l.entries) > 10_000 {
			cutoff := now.Add(-2 * window)
			for k, v := range l.entries {
				if v.windowAt.Before(cutoff) {
					delete(l.entries, k)
				}
			}
		}
		l.entries[ip] = &fixedWindowEntry{count: 1, windowAt: now}
		return true
	}
	if e.count >= maxReqs {
		return false
	}
	e.count++
	return true
}

// attemptLimiter tracks caller-keyed attempt budgets (e.g. MFA second-factor
// guesses per account). Its own instance, so per-key budgets and the per-IP
// middleware budgets can't collide on shared keys.
var attemptLimiter = newIPRateLimiter()

// AllowAttempt reports whether key still has budget within its fixed window.
// Unlike RateLimit it is keyed by an arbitrary caller-chosen string (prefix it
// per use case, e.g. "mfa-verify:<userID>") so throttling can follow the
// account rather than the client IP.
func AllowAttempt(key string, maxAttempts int, window time.Duration) bool {
	return attemptLimiter.allow(key, maxAttempts, window)
}

// RateLimit returns middleware that limits each remote IP to maxReqs requests
// per window duration. Relies on RealIP having already rewritten r.RemoteAddr
// to the real client IP.
//
// Each call owns an independent counter, so distinct budgets never share a
// bucket: an IP exhausting the auth budget still gets its full user-management
// budget. Wire one call per budget and reuse the returned middleware — calling
// this per request would hand every request a fresh counter and limit nothing.
func RateLimit(maxReqs int, window time.Duration) func(http.Handler) http.Handler {
	return rateLimitWith(newIPRateLimiter(), maxReqs, window)
}

func rateLimitWith(l *ipRateLimiter, maxReqs int, window time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				ip = r.RemoteAddr
			}
			if !l.allow(ip, maxReqs, window) {
				w.Header().Set("Retry-After", strconv.Itoa(int(window.Seconds())))
				WriteError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many requests, please try again later")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
