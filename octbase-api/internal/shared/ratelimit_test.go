package shared

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

// okHandler is the terminal handler for the middleware under test.
func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

// hit sends one request from ip through h and returns the status code.
func hit(h http.Handler, ip string) int {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = ip + ":1234"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

// TestRateLimit_BudgetsAreIndependent is the regression test for the budget
// collision: main.go wires two RateLimit budgets (auth 120/min, usermgmt
// 60/min). They previously shared one package-level counter keyed by IP alone,
// so ordinary login traffic exhausted the smaller user-management budget and
// 429'd admins out of it (surfaced as test_admin_cannot_list_users flapping
// 403 -> 429 once the e2e suite got fast enough to push 60+ logins into one
// window). Each RateLimit call must own its counter.
func TestRateLimit_BudgetsAreIndependent(t *testing.T) {
	const ip = "203.0.113.10"
	authBudget := RateLimit(120, time.Minute)(okHandler())
	usermgmtBudget := RateLimit(60, time.Minute)(okHandler())

	// Exhaust the auth budget from this IP.
	for i := 0; i < 120; i++ {
		if code := hit(authBudget, ip); code != http.StatusOK {
			t.Fatalf("auth request %d: status = %d, want 200", i+1, code)
		}
	}
	if code := hit(authBudget, ip); code != http.StatusTooManyRequests {
		t.Fatalf("auth over budget: status = %d, want 429", code)
	}

	// The user-management budget must be untouched by that traffic.
	if code := hit(usermgmtBudget, ip); code != http.StatusOK {
		t.Fatalf("usermgmt budget consumed by auth traffic: status = %d, want 200", code)
	}
}

// TestRateLimit_SameBudgetSharesCounterAcrossRequests guards the other
// direction: independence must come from separate RateLimit calls, not from
// each request getting a fresh counter (which would limit nothing).
func TestRateLimit_SameBudgetSharesCounterAcrossRequests(t *testing.T) {
	const ip = "203.0.113.11"
	h := RateLimit(3, time.Minute)(okHandler())

	for i := 0; i < 3; i++ {
		if code := hit(h, ip); code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200", i+1, code)
		}
	}
	if code := hit(h, ip); code != http.StatusTooManyRequests {
		t.Fatalf("4th request: status = %d, want 429 (counter not shared across requests)", code)
	}
}

// TestRateLimit_IndependentIPsThroughWrapper covers per-IP isolation through
// the exported wrapper rather than an injected limiter.
func TestRateLimit_IndependentIPsThroughWrapper(t *testing.T) {
	h := RateLimit(2, time.Minute)(okHandler())

	for i := 0; i < 2; i++ {
		hit(h, "198.51.100.1")
	}
	if code := hit(h, "198.51.100.1"); code != http.StatusTooManyRequests {
		t.Fatalf("exhausted IP: status = %d, want 429", code)
	}
	if code := hit(h, "198.51.100.2"); code != http.StatusOK {
		t.Fatalf("second IP blocked by first IP's traffic: status = %d, want 200", code)
	}
}

// TestRateLimit_AttemptBudgetIsSeparate pins that the caller-keyed AllowAttempt
// budget (MFA guesses, forgot-password) shares no counter with the middleware.
func TestRateLimit_AttemptBudgetIsSeparate(t *testing.T) {
	const ip = "203.0.113.12"
	h := RateLimit(2, time.Minute)(okHandler())

	for i := 0; i < 2; i++ {
		hit(h, ip)
	}
	if code := hit(h, ip); code != http.StatusTooManyRequests {
		t.Fatalf("middleware budget: status = %d, want 429", code)
	}
	if !AllowAttempt("forgot-password:"+ip, 1, time.Minute) {
		t.Fatal("attempt budget consumed by middleware traffic on the same IP")
	}
}

// TestRateLimit_RejectionShape pins the 429 contract: Retry-After plus the
// standard {code,message} error body with the stable RATE_LIMITED code.
func TestRateLimit_RejectionShape(t *testing.T) {
	const ip = "203.0.113.13"
	window := 90 * time.Second
	h := RateLimit(1, window)(okHandler())

	hit(h, ip)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = ip + ":1234"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	if got, want := rec.Header().Get("Retry-After"), strconv.Itoa(int(window.Seconds())); got != want {
		t.Errorf("Retry-After = %q, want %q", got, want)
	}
	var body struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Code != "RATE_LIMITED" {
		t.Errorf("code = %q, want RATE_LIMITED", body.Code)
	}
	if body.Message == "" {
		t.Error("message is empty")
	}
}

// TestRateLimit_WindowResets covers the fixed-window rollover: once the window
// elapses an exhausted IP gets a fresh budget.
func TestRateLimit_WindowResets(t *testing.T) {
	const ip = "203.0.113.14"
	window := 40 * time.Millisecond
	h := RateLimit(1, window)(okHandler())

	if code := hit(h, ip); code != http.StatusOK {
		t.Fatalf("first request: status = %d, want 200", code)
	}
	if code := hit(h, ip); code != http.StatusTooManyRequests {
		t.Fatalf("second request in window: status = %d, want 429", code)
	}

	// allow() rolls the window over strictly after windowAt+window.
	time.Sleep(window + 20*time.Millisecond)

	if code := hit(h, ip); code != http.StatusOK {
		t.Fatalf("after window reset: status = %d, want 200", code)
	}
}
