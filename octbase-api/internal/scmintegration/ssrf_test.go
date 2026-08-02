package scmintegration

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// TestMain relaxes the SSRF egress guard for the whole scmintegration test
// binary so the provider/handler integration tests can reach their loopback
// httptest servers. The guard's own blocking behaviour is verified below by
// calling the pure guard functions with an explicit allowPrivate=false, which
// does not depend on this global.
func TestMain(m *testing.M) {
	allowPrivateEgress = true
	os.Exit(m.Run())
}

func TestBlockedIPReason(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "::1", // loopback
		"169.254.169.254",         // cloud metadata (link-local)
		"10.1.2.3", "192.168.0.1", // RFC1918
		"172.16.5.5",    // RFC1918
		"100.64.0.1",    // CGNAT
		"0.0.0.0", "::", // unspecified
		"fe80::1",                 // link-local IPv6
		"fc00::1", "fd12:3456::1", // ULA (private IPv6)
		"224.0.0.1", "ff02::1", // multicast
	}
	for _, s := range blocked {
		if r := blockedIPReason(net.ParseIP(s)); r == "" {
			t.Errorf("%s should be blocked, got allowed", s)
		}
	}
	public := []string{"140.82.121.4", "8.8.8.8", "2606:4700:4700::1111"}
	for _, s := range public {
		if r := blockedIPReason(net.ParseIP(s)); r != "" {
			t.Errorf("%s should be allowed, got blocked (%s)", s, r)
		}
	}
	if blockedIPReason(nil) == "" {
		t.Errorf("nil IP must be treated as blocked")
	}
}

func TestCheckOutboundURL(t *testing.T) {
	reject := []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://127.0.0.1:9000",
		"http://[::1]:8080",
		"http://10.0.0.5",
		"https://192.168.1.1/api/v3",
		"ftp://example.com",
		"file:///etc/passwd",
		"gopher://example.com",
	}
	for _, u := range reject {
		perr := checkOutboundURL(u, false)
		if perr == nil {
			t.Errorf("%s should be rejected", u)
			continue
		}
		if perr.Code != CodeURLNotAllowed || perr.Status != http.StatusBadRequest {
			t.Errorf("%s: got code=%s status=%d, want %s/400", u, perr.Code, perr.Status, CodeURLNotAllowed)
		}
	}
	allow := []string{"", "https://api.github.com", "https://gitlab.example.com/api/v4", "http://github.enterprise.internal"}
	for _, u := range allow {
		if perr := checkOutboundURL(u, false); perr != nil {
			t.Errorf("%s should be allowed by scheme/host, got %v", u, perr.Message)
		}
	}
	// A blocked IP is permitted only when the private-egress override is on, but
	// a bad scheme is still rejected regardless.
	if perr := checkOutboundURL("http://127.0.0.1", true); perr != nil {
		t.Errorf("loopback should pass when allowPrivate=true, got %v", perr.Message)
	}
	if perr := checkOutboundURL("ftp://127.0.0.1", true); perr == nil {
		t.Errorf("bad scheme must be rejected even when allowPrivate=true")
	}
}

func TestDialGuardedBlocksLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")
	dialer := &net.Dialer{}

	// Guard active: the loopback target must be refused before connecting.
	if _, err := dialGuarded(context.Background(), dialer, "tcp", addr, false); err == nil {
		t.Fatalf("dialGuarded should refuse loopback %s when allowPrivate=false", addr)
	} else if !strings.Contains(err.Error(), "refused") {
		t.Errorf("unexpected error: %v", err)
	}

	// Override on: the same target connects (this is the test-harness path).
	conn, err := dialGuarded(context.Background(), dialer, "tcp", addr, true)
	if err != nil {
		t.Fatalf("dialGuarded should connect to %s when allowPrivate=true: %v", addr, err)
	}
	_ = conn.Close()
}

func TestNewProviderRejectsInternalBaseURL(t *testing.T) {
	// Exercise the preflight wiring in NewProvider with the guard forced on,
	// independent of the binary-wide TestMain override. Restored before return;
	// this test is not parallel so no concurrent reader observes the toggle.
	prev := allowPrivateEgress
	allowPrivateEgress = false
	defer func() { allowPrivateEgress = prev }()

	_, err := NewProvider(newConn(ProviderGitHub, "https://github.com/org/repo", "http://169.254.169.254"), "tok", nil)
	var pe *ProviderError
	if !errors.As(err, &pe) || pe.Code != CodeURLNotAllowed {
		t.Fatalf("NewProvider with internal base URL: got %v, want %s", err, CodeURLNotAllowed)
	}
	// The offline fake provider must never be gated by the egress guard.
	if _, err := NewProvider(newConn(ProviderFake, "https://x/y/z", "http://127.0.0.1"), "", nil); err != nil {
		t.Errorf("fake provider must not be blocked by egress guard: %v", err)
	}
}

// TestDialGuarded_RefusalRedactsAddressAndReason pins the oracle-removal
// invariant: the dial-refusal error can surface to the client via
// writeProviderError, so it must not echo the resolved IP, the supplied host,
// or the block reason — any of those would turn the guard into an SSRF oracle
// revealing internal DNS/topology.
func TestDialGuarded_RefusalRedactsAddressAndReason(t *testing.T) {
	dialer := &net.Dialer{}
	// localhost resolves to loopback, so the guard must refuse it — and the
	// error text must stay generic.
	_, err := dialGuarded(context.Background(), dialer, "tcp", "localhost:9999", false)
	if err == nil {
		t.Fatal("dialGuarded should refuse localhost when allowPrivate=false")
	}
	msg := err.Error()
	for _, leak := range []string{
		"127.0.0.1", "::1", // resolved IPs
		"loopback", "private", "link-local", "multicast", "unspecified", // blockedIPReason texts
		"9999", // supplied port is fine to omit too — the generic text has no target details
	} {
		if strings.Contains(msg, leak) {
			t.Errorf("dial-refusal error leaks %q: %q", leak, msg)
		}
	}
	if !strings.Contains(msg, "scm egress refused") {
		t.Errorf("expected the generic refusal text, got %q", msg)
	}
}
