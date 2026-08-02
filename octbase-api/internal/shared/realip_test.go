package shared

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newReq(remoteAddr, xff, xrealip string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = remoteAddr
	if xff != "" {
		r.Header.Set("X-Forwarded-For", xff)
	}
	if xrealip != "" {
		r.Header.Set("X-Real-IP", xrealip)
	}
	return r
}

func runRealIP(trusted []*net.IPNet, r *http.Request) string {
	var got string
	RealIP(trusted)(http.HandlerFunc(func(_ http.ResponseWriter, rr *http.Request) {
		got = ClientIP(rr)
	})).ServeHTTP(httptest.NewRecorder(), r)
	return got
}

func TestRealIP(t *testing.T) {
	trusted := ParseTrustedProxies("10.0.0.0/8, 192.168.1.5")

	tests := []struct {
		name       string
		trusted    []*net.IPNet
		remoteAddr string
		xff        string
		xrealip    string
		want       string
	}{
		{
			name:       "no trusted proxies ignores XFF",
			trusted:    nil,
			remoteAddr: "203.0.113.9:1234",
			xff:        "1.2.3.4",
			want:       "203.0.113.9",
		},
		{
			name:       "untrusted peer ignores XFF",
			trusted:    trusted,
			remoteAddr: "203.0.113.9:1234",
			xff:        "1.2.3.4",
			want:       "203.0.113.9",
		},
		{
			name:       "trusted peer honors XFF",
			trusted:    trusted,
			remoteAddr: "10.1.2.3:443",
			xff:        "203.0.113.7",
			want:       "203.0.113.7",
		},
		{
			name:       "trusted peer takes rightmost untrusted (anti-spoof)",
			trusted:    trusted,
			remoteAddr: "10.1.2.3:443",
			xff:        "9.9.9.9, 203.0.113.7, 10.0.0.4",
			want:       "203.0.113.7",
		},
		{
			name:       "trusted peer falls back to X-Real-IP when no XFF",
			trusted:    trusted,
			remoteAddr: "192.168.1.5:443",
			xrealip:    "198.51.100.2",
			want:       "198.51.100.2",
		},
		{
			name:       "all XFF entries trusted yields none, keeps peer",
			trusted:    trusted,
			remoteAddr: "10.1.2.3:443",
			xff:        "10.0.0.4, 10.0.0.5",
			want:       "10.1.2.3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runRealIP(tt.trusted, newReq(tt.remoteAddr, tt.xff, tt.xrealip))
			if got != tt.want {
				t.Errorf("ClientIP = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestRealIP_DeployedTopology pins the chain shape the API actually receives on
// a stack deployed behind the bundled Caddy front door:
//
//	client → host/edge Caddy (:443) → frontend Caddy container → API
//
// The edge Caddy sets X-Forwarded-For to the real client; the frontend Caddy
// appends its own peer address and forwards to the API. Rootless podman NATs
// published-port traffic, so the address the frontend appends is a container
// address on the compose network (here 10.89.4.5), never the edge's. The API's
// immediate peer is the frontend container, so OCTBASE_TRUSTED_PROXIES is scoped
// to the compose network subnet and the right-most untrusted entry is the client.
func TestRealIP_DeployedTopology(t *testing.T) {
	// What OCTBASE_TRUSTED_PROXIES holds on a deployed stack: the compose
	// network subnet only. Deliberately NOT the host's public IP — see
	// TestRealIP_TrustingAPublicEdgeIPIsUnsafe below.
	trusted := ParseTrustedProxies("10.89.4.0/24")
	const frontendPeer = "10.89.4.5:34567"

	tests := []struct {
		name       string
		trusted    []*net.IPNet
		remoteAddr string
		xff        string
		want       string
	}{
		{
			// The good path: the real client survives to the rate limiter.
			name:       "edge+frontend chain yields the real client",
			trusted:    trusted,
			remoteAddr: frontendPeer,
			xff:        "203.0.113.7, 10.89.4.5",
			want:       "203.0.113.7",
		},
		{
			// A client that prepends a forged entry cannot displace the genuine
			// one appended to its right. (In the deployed stack the edge Caddy
			// has no trusted_proxies and so replaces a client-supplied XFF
			// outright — this is defence in depth behind that.)
			name:       "client-prepended forged entry cannot win",
			trusted:    trusted,
			remoteAddr: frontendPeer,
			xff:        "9.9.9.9, 203.0.113.7, 10.89.4.5",
			want:       "203.0.113.7",
		},
		{
			// Anything that reaches the API without coming through the frontend
			// container is an untrusted peer: its XFF is ignored entirely, so it
			// cannot choose its own rate-limit bucket.
			name:       "untrusted peer hitting the API directly cannot set XFF",
			trusted:    trusted,
			remoteAddr: "198.51.100.9:1234",
			xff:        "203.0.113.7, 10.89.4.5",
			want:       "198.51.100.9",
		},
		{
			// Regression guard for the shared-bucket bug this replaces: before
			// the frontend Caddy was configured to preserve the inbound chain,
			// every request arrived with only the container's own address in
			// XFF, every entry was trusted, and all clients keyed into one
			// bucket. If this ever returns the container IP again, per-client
			// rate limiting has silently collapsed back to global.
			name:       "container-only chain falls back to the peer (the old shared bucket)",
			trusted:    trusted,
			remoteAddr: frontendPeer,
			xff:        "10.89.4.5",
			want:       "10.89.4.5",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runRealIP(tt.trusted, newReq(tt.remoteAddr, tt.xff, ""))
			if got != tt.want {
				t.Errorf("ClientIP = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestRealIP_TrustingAPublicEdgeIPIsUnsafe documents why OCTBASE_TRUSTED_PROXIES
// must list only addresses on the private compose network, never the host's
// public IP — even though an edge proxy legitimately reaches the stack from it.
//
// clientFromXFF skips every entry it trusts and returns the right-most one it
// does not. Trusting a public address therefore turns that address into a
// terminator: any entry an attacker can place to its LEFT becomes "the client".
// A private container subnet is not injectable from outside the host; a public
// IP is just a string an attacker can put in a header. Scope the trust list to
// the compose network and let unreachability, not the header, be the boundary.
func TestRealIP_TrustingAPublicEdgeIPIsUnsafe(t *testing.T) {
	unsafe := ParseTrustedProxies("10.89.4.0/24,178.105.142.1")
	safe := ParseTrustedProxies("10.89.4.0/24")

	// An attacker-supplied chain that names the trusted public edge IP to the
	// right of the address it wants attributed to itself.
	const forged = "9.9.9.9, 178.105.142.1, 10.89.4.5"

	if got := runRealIP(unsafe, newReq("10.89.4.5:34567", forged, "")); got != "9.9.9.9" {
		t.Errorf("precondition: with the public edge IP trusted the forged entry should win, got %q", got)
	}
	// With the trust list scoped to the compose network, the injected
	// "178.105.142.1" is just another untrusted hop and wins instead of the
	// attacker's chosen 9.9.9.9 — the attacker no longer picks the bucket.
	if got := runRealIP(safe, newReq("10.89.4.5:34567", forged, "")); got != "178.105.142.1" {
		t.Errorf("ClientIP = %q, want %q", got, "178.105.142.1")
	}
}

func TestParseTrustedProxies(t *testing.T) {
	nets := ParseTrustedProxies(" 10.0.0.0/8 , 192.168.1.5 , , bogus , ::1 ")
	// 10.0.0.0/8, 192.168.1.5/32, ::1/128 -> 3 valid; "bogus" skipped.
	if len(nets) != 3 {
		t.Fatalf("got %d nets, want 3", len(nets))
	}
}
