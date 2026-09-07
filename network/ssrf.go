package network

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
)

// AllowPrivatePeers, when true, disables the loopback/private/link-local rejection
// in IsSafePeerURL. Set it ONLY for trusted private-network or local multi-node
// deployments where peers legitimately use private/loopback IPs (e.g. nodes on one
// VPC, or a local test cluster). Default false keeps the public SSRF protection
// (block metadata/loopback/internal) on /peers/announce and PEX discovery.
var AllowPrivatePeers bool

// IsSafePeerURL rejects peer URLs that would let untrusted peer input (POST
// /peers/announce, or a PEX GET /peers response) drive the node into requests to
// internal/loopback/link-local hosts (SSRF), including the cloud instance-metadata
// endpoint. Only http/https with a public host is allowed — unless AllowPrivatePeers
// is set for a trusted private-network deployment.
//
// This is applied ONLY at untrusted ingress points — NOT to operator-configured
// bootstrap peers (--peer), which may legitimately be loopback/internal.
//
// The ingest-time check is not sufficient on its own: a hostname that resolves
// to a public address here can resolve to an internal one by the time the node
// dials it (DNS rebinding). SafeTransport closes that window at connect time;
// the P2P clients all use it.
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

	// Private-network / local deployments opt out of the internal-IP rejection.
	if AllowPrivatePeers {
		return nil
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

// SafeTransport returns an HTTP transport that re-validates where a connection
// is actually going, at the moment it is made.
//
// IsSafePeerURL runs when a peer address is accepted, which leaves a gap: a
// hostname that resolved to a public address then can resolve to 169.254.169.254
// now, and the ingest check has already passed. That is DNS rebinding, and it
// turns an approved peer entry into a way to make this node issue requests
// against cloud metadata and other internal services.
//
// The guard applies to hostnames only. An IP-literal peer has nothing to
// re-resolve — IsSafePeerURL already checked that exact address and it cannot
// change underneath us — so literals dial straight through. That keeps operator
// clusters on internal IP literals and loopback test servers working unchanged,
// while covering the case that can actually move.
//
// Resolution happens once here and the connection is made to the address that
// was checked, rather than handing the hostname back to the dialer to resolve a
// second time. Checking one lookup and connecting on the result of another is
// the same race in a smaller window.
func SafeTransport(timeout time.Duration) *http.Transport {
	base := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
	return &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, fmt.Errorf("unparseable dial address %q", addr)
			}
			if net.ParseIP(host) != nil || AllowPrivatePeers {
				return base.DialContext(ctx, network, addr)
			}

			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, fmt.Errorf("resolve %s: %w", host, err)
			}
			if len(ips) == 0 {
				return nil, fmt.Errorf("host %s does not resolve", host)
			}
			// Every answer must be acceptable. Dialing only the good ones would
			// let a host that returns one public and one internal address get
			// its internal address reached on a later attempt or a retry.
			for _, ip := range ips {
				if isDisallowedIP(ip.IP) {
					return nil, fmt.Errorf(
						"refusing to dial %s: it resolves to internal address %s "+
							"(possible DNS rebinding; pass --allow-private-peers if this is a trusted private network)",
						host, ip.IP)
				}
			}

			var lastErr error
			for _, ip := range ips {
				conn, derr := base.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
				if derr == nil {
					return conn, nil
				}
				lastErr = derr
			}
			return nil, lastErr
		},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
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
