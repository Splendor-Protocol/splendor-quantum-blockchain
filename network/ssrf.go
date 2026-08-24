package network

import (
	"fmt"
	"net"
	"net/url"
)

// IsSafePeerURL rejects peer URLs that would let untrusted peer input (POST
// /peers/announce, or a PEX GET /peers response) drive the node into requests to
// internal/loopback/link-local hosts (SSRF), including the cloud instance-metadata
// endpoint. Only http/https with a public host is allowed.
//
// This is applied ONLY at untrusted ingress points — NOT to operator-configured
// bootstrap peers (--peer), which may legitimately be loopback/internal.
//
// NOTE: the check resolves at ingest time; a hostname could later re-resolve to an
// internal IP (DNS rebinding). Peers are IP literals or known hosts in practice,
// and the P2P HTTP clients also refuse to follow redirects (see noRedirect), which
// closes the redirect-to-internal variant. A dial-time re-check would be the
// complete fix — tracked as a follow-up.
func IsSafePeerURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("unparseable url")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("scheme must be http or https")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("missing host")
	}

	var ips []net.IP
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else {
		resolved, rerr := net.LookupIP(host)
		if rerr != nil || len(resolved) == 0 {
			return fmt.Errorf("host does not resolve")
		}
		ips = resolved
	}
	for _, ip := range ips {
		if isDisallowedIP(ip) {
			return fmt.Errorf("host resolves to a disallowed (internal) address")
		}
	}
	return nil
}

// isDisallowedIP reports whether an IP is in a range a peer must never point at:
// loopback, unspecified, link-local (covers 169.254.0.0/16 metadata + fe80::/10),
// private (RFC1918 + fc00::/7), multicast, and carrier-grade NAT (100.64.0.0/10).
func isDisallowedIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsPrivate() || ip.IsMulticast() {
		return true
	}
	if ip4 := ip.To4(); ip4 != nil {
		// Cloud metadata (defence-in-depth; also caught by link-local above).
		if ip4[0] == 169 && ip4[1] == 254 {
			return true
		}
		// Carrier-grade NAT 100.64.0.0/10.
		if ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
			return true
		}
	}
	return false
}
