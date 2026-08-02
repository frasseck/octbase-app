package shared

import (
	"net"
	"net/http"
	"os"
	"strings"
)

// ParseTrustedProxies parses a comma-separated list of CIDRs or bare IPs into IP
// networks. A bare IP becomes a /32 (IPv4) or /128 (IPv6). Invalid entries are
// skipped. The argument is the raw config string; nothing is read from the env.
func ParseTrustedProxies(raw string) []*net.IPNet {
	var nets []*net.IPNet
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if !strings.Contains(part, "/") {
			if strings.Contains(part, ":") {
				part += "/128"
			} else {
				part += "/32"
			}
		}
		if _, n, err := net.ParseCIDR(part); err == nil {
			nets = append(nets, n)
		}
	}
	return nets
}

// TrustedProxiesFromEnv reads the OCTBASE_TRUSTED_PROXIES configuration. When
// unset, no proxy is trusted and client-supplied forwarding headers are ignored.
func TrustedProxiesFromEnv() []*net.IPNet {
	return ParseTrustedProxies(os.Getenv("OCTBASE_TRUSTED_PROXIES"))
}

func ipInNets(ip net.IP, nets []*net.IPNet) bool {
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// remoteIP returns the host portion of r.RemoteAddr as a net.IP, or nil.
func remoteIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return net.ParseIP(strings.TrimSpace(host))
}

// RealIP returns middleware that rewrites r.RemoteAddr to the real client IP
// taken from X-Forwarded-For (or X-Real-IP) ONLY when the immediate peer is a
// configured trusted proxy. With no trusted proxies (the secure default), or
// when the peer is untrusted, the client-supplied forwarding headers are
// ignored — so they cannot be spoofed to bypass per-IP rate limiting or to forge
// audit-log source IPs.
//
// When the peer is trusted, the real client is the right-most X-Forwarded-For
// address that is not itself a trusted proxy: a client may prepend fake entries,
// but cannot remove the genuine ones appended to its right by trusted proxies.
//
// This replaces chi's middleware.RealIP, which trusts forwarding headers from
// any caller.
func RealIP(trusted []*net.IPNet) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(trusted) > 0 {
				if peer := remoteIP(r); peer != nil && ipInNets(peer, trusted) {
					if ip := clientFromXFF(r.Header.Get("X-Forwarded-For"), trusted); ip != "" {
						r.RemoteAddr = net.JoinHostPort(ip, "0")
					} else if xr := strings.TrimSpace(r.Header.Get("X-Real-IP")); xr != "" && net.ParseIP(xr) != nil {
						r.RemoteAddr = net.JoinHostPort(xr, "0")
					}
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// clientFromXFF returns the right-most IP in an X-Forwarded-For chain that is
// not a trusted proxy, or "" when there is none.
func clientFromXFF(xff string, trusted []*net.IPNet) string {
	if xff == "" {
		return ""
	}
	parts := strings.Split(xff, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		p := strings.TrimSpace(parts[i])
		ip := net.ParseIP(p)
		if ip == nil || ipInNets(ip, trusted) {
			continue
		}
		return p
	}
	return ""
}

// ClientIP returns the client IP recorded on the request: the host portion of
// r.RemoteAddr, after RealIP has applied any trusted-proxy rewriting. Use this
// for audit logging instead of reading X-Forwarded-For directly, which is
// spoofable.
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
