package scmintegration

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// allowPrivateEgress relaxes the SSRF egress guard below. It defaults to false
// (guard active) and is set to true ONLY by the test binary's TestMain, so
// integration tests can point providers at loopback httptest servers. No code
// path outside *_test files sets it; it must never be enabled in production.
var allowPrivateEgress = false

// blockedIPReason reports why an IP is not a permitted SCM egress target, or ""
// if it is a routable public address. It rejects loopback, the unspecified
// address, RFC1918 private ranges, RFC6598 carrier-grade NAT, link-local
// (including the 169.254.169.254 cloud-metadata endpoint), and multicast, for
// both IPv4 and IPv6. This is the authoritative allow/deny decision.
func blockedIPReason(ip net.IP) string {
	switch {
	case ip == nil:
		return "unresolvable address"
	case ip.IsLoopback():
		return "loopback address"
	case ip.IsUnspecified():
		return "unspecified address"
	case ip.IsPrivate():
		return "private address"
	case ip.IsLinkLocalUnicast():
		return "link-local address"
	case ip.IsLinkLocalMulticast(), ip.IsInterfaceLocalMulticast(), ip.IsMulticast():
		return "multicast address"
	}
	// RFC6598 100.64.0.0/10 (carrier-grade NAT) is not covered by IsPrivate.
	if ip4 := ip.To4(); ip4 != nil && ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
		return "carrier-grade NAT address"
	}
	return ""
}

// checkOutboundURL vets a user-supplied SCM API base URL before any request is
// made: it must parse, use http/https, and — unless allowPrivate is set — must
// not point at a literal blocked IP. Hostnames that resolve to blocked IPs are
// caught later by dialGuarded. An empty URL (the provider's public default) is
// always allowed. Kept pure (allowPrivate passed in) so it is race-free to test.
func checkOutboundURL(raw string, allowPrivate bool) *ProviderError {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return &ProviderError{Code: CodeURLNotAllowed, Status: http.StatusBadRequest, Message: "SCM API base URL is not a valid URL"}
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return &ProviderError{Code: CodeURLNotAllowed, Status: http.StatusBadRequest, Message: "SCM API base URL must use http or https"}
	}
	if allowPrivate {
		return nil
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		if reason := blockedIPReason(ip); reason != "" {
			return &ProviderError{Code: CodeURLNotAllowed, Status: http.StatusBadRequest, Message: "SCM API base URL points at a disallowed " + reason}
		}
	}
	return nil
}

// dialGuarded resolves addr and refuses to open a connection to any blocked
// (internal) IP, defeating SSRF via user-supplied SCM hosts — including DNS
// names that resolve to internal addresses and rebinding across redirects. It
// dials the validated IP directly so the address checked is the address
// connected to; TLS verification still uses the original hostname from the URL.
// allowPrivate is passed explicitly to keep the function race-free in tests.
func dialGuarded(ctx context.Context, dialer *net.Dialer, network, addr string, allowPrivate bool) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	if !allowPrivate {
		for _, ip := range ips {
			if blockedIPReason(ip.IP) != "" {
				// Do not echo the resolved IP or the specific reason: the error
				// text can surface to the client (writeProviderError), where it
				// would be an SSRF oracle revealing internal DNS/topology.
				return nil, &net.OpError{Op: "dial", Net: network, Err: errors.New("scm egress refused: destination address is not allowed")}
			}
		}
	}
	var lastErr error
	for _, ip := range ips {
		conn, derr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
		if derr == nil {
			return conn, nil
		}
		lastErr = derr
	}
	return nil, lastErr
}

// newGuardedHTTPClient builds the shared outbound client used for all real SCM
// provider traffic, with the SSRF egress guard installed on its transport and a
// bounded redirect chain (each redirect hop is re-validated by dialGuarded).
func newGuardedHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialGuarded(ctx, dialer, network, addr, allowPrivateEgress)
			},
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          10,
			IdleConnTimeout:       30 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: time.Second,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("scm: stopped after 5 redirects")
			}
			return nil
		},
	}
}
