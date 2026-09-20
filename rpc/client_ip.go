package rpc

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"

	"github.com/rs/zerolog/log"
)

// ClientIPPolicy decides which address identifies the caller of an HTTP
// request. Every per-client budget on this node keys off it: the sealed
// transport's per-source in-flight slots, the transaction/x402 token bucket,
// and the simulation guard's per-IP rate. If a caller can choose the value,
// it can mint a fresh budget per request, or aim a request at another
// caller's budget and hold it down.
//
// The rule is the "right-most untrusted" selection (RFC 7239 §7.1, and the
// algorithm set out at https://adam-p.ca/blog/2022/03/x-forwarded-for/): walk
// X-Forwarded-For from the RIGHT, skip entries belonging to proxies the
// operator configured, and take the first entry that is left.
//
// Right, not left, because of how the header is written. An appending proxy
// puts its own view of the peer at the end and passes what the client sent
// through unchanged in front of it — nginx's $proxy_add_x_forwarded_for is
// literally "$http_x_forwarded_for, $remote_addr", and Cloudflare (the
// deployed ingress here is a cloudflared tunnel) appends the same way. So the
// last entry is written by infrastructure and every entry before it may have
// been written by the caller. Taking the left-most entry — what Express's
// bare `trust proxy` does, and what this node used to do — hands the caller
// the key to its own rate limit.
//
// Two further guards:
//
//   - Trusted proxies are a CIDR list, not a bare boolean, so multi-hop
//     ingresses (cloudflared in front of nginx) can be resolved by skipping
//     the hops that are ours. Envoy spells the same idea as a hop COUNT
//     (xff_num_trusted_hops); a CIDR list is used here because it stays
//     correct when the hop count changes.
//   - Where the ingress sets a single-valued header it always overwrites —
//     CF-Connecting-IP for Cloudflare, X-Real-IP for nginx — that header can
//     be named and is preferred, because it has no chain to reason about. It
//     is honoured only from a trusted peer: an unscoped CF-Connecting-IP is
//     free-form text the first time somebody reaches the origin directly.
//
// RFC 7239's own `Forwarded` header is deliberately NOT read. It has the same
// trust properties as X-Forwarded-For with more parsing surface, and neither
// cloudflared nor nginx emits it here; reading it would only add a second way
// to assert an identity.
type ClientIPPolicy struct {
	// trust is the operator opt-in. Off means the socket address is the only
	// thing that identifies a caller.
	trust bool
	// trusted are the proxies whose appended entries may be skipped. Empty
	// with trust on is the legacy --trust-proxy meaning: the operator asserts
	// every connection arrives through their proxy, so the last entry in the
	// header is that proxy's view of the peer.
	trusted []netip.Prefix
	// header names a single-valued header the ingress controls. Empty reads
	// X-Forwarded-For instead.
	header string
}

// Key namespaces. A forwarded claim and a socket address never collide, so a
// caller naming another caller's address cannot select that caller's budget
// even where the forwarded value itself is not fully trustworthy.
const (
	socketKeyPrefix    = "ip:"
	forwardedKeyPrefix = "fwd:"
)

const (
	// headerXFF is the multi-valued forwarded header.
	headerXFF = "X-Forwarded-For"
	// maxForwardedBytes and maxForwardedHops bound the work one request can
	// ask of the parse. Both are far above any real chain; a header past
	// either is treated as unusable and the socket address stands.
	maxForwardedBytes = 4096
	maxForwardedHops  = 32
)

// NewClientIPPolicy builds a policy. trustProxy is the operator opt-in;
// trustedProxies is an optional list of IPs or CIDRs identifying the
// operator's own proxies (naming any implies the opt-in); clientIPHeader
// optionally names a single-valued header the ingress always overwrites, such
// as CF-Connecting-IP.
//
// The zero ClientIPPolicy trusts nothing, which is the safe default for every
// caller that does not configure one.
func NewClientIPPolicy(trustProxy bool, trustedProxies []string, clientIPHeader string) (ClientIPPolicy, error) {
	var p ClientIPPolicy
	for _, raw := range trustedProxies {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		if strings.Contains(entry, "/") {
			pfx, err := netip.ParsePrefix(entry)
			if err != nil {
				return ClientIPPolicy{}, fmt.Errorf("trusted proxy %q is not a CIDR: %w", raw, err)
			}
			p.trusted = append(p.trusted, pfx.Masked())
			continue
		}
		addr, err := netip.ParseAddr(entry)
		if err != nil {
			return ClientIPPolicy{}, fmt.Errorf("trusted proxy %q is neither an IP nor a CIDR: %w", raw, err)
		}
		addr = addr.Unmap()
		p.trusted = append(p.trusted, netip.PrefixFrom(addr, addr.BitLen()))
	}
	// Trust follows the entries that PARSED, not the length of the slice handed
	// in. Reading the raw length meant a single blank entry — a trailing comma in
	// a list, an unset variable expanded into the flag — turned trust on while
	// leaving the prefix list empty, and an empty list means "every peer is my
	// proxy". So --trusted-proxy "" silently honoured the forwarded header from
	// anyone, and did it without even the warning below, because trustProxy was
	// false. Blank entries are inert now.
	p.trust = trustProxy || len(p.trusted) > 0
	if p.trust && len(p.trusted) == 0 {
		// The legacy form: trust means "everything reaching me came through my
		// proxy". It is a real deployment, but it cannot distinguish a proxied
		// request from a direct one, so a caller that reaches the port directly
		// can mint forwarded identities at will and can aim one at a victim who
		// does arrive through the proxy. Loud, not fatal: an operator relying on
		// it should see this and name the proxy.
		log.Warn().Msg("--trust-proxy with no --trusted-proxy trusts the forwarded header from ANY peer; " +
			"a direct connection can then choose its own rate-limit identity, or another caller's. " +
			"Name the proxy with --trusted-proxy <ip|cidr>.")
	}
	p.header = strings.TrimSpace(clientIPHeader)
	if p.header != "" && !p.trust {
		return ClientIPPolicy{}, fmt.Errorf(
			"client-ip header %q needs a trusted proxy: without one the header is client-supplied", p.header)
	}
	return p, nil
}

// clientKey returns the identity every per-client budget keys off.
//
// This is the single implementation behind both call sites — the sealed
// transport's per-source cap and the rate limiter. They were two copies that
// happened to agree; one of them drifting is the same bug twice, so there is
// now nothing to drift.
func (p ClientIPPolicy) clientKey(r *http.Request) string {
	peer := socketHost(r.RemoteAddr)
	// Both namespaces are aggregated the same way. Keying the socket address
	// per /128 while keying the forwarded one per /64 would just move the hole
	// to whichever plane an operator happens not to be proxying.
	socketKey := socketKeyPrefix + budgetKeyForIP(peer)

	// Not opted in, or this connection did not arrive through a proxy we
	// named: the socket address is the only thing we know.
	if !p.trust || !p.peerIsTrustedProxy(peer) {
		return socketKey
	}

	if p.header != "" {
		if ip, ok := normalizeIP(r.Header.Get(p.header)); ok {
			return forwardedKeyPrefix + budgetKeyForIP(ip)
		}
		// A configured single-valued header that is missing or unparseable
		// means the ingress did not set it. Fall back rather than reaching
		// for the chain we were told not to use.
		return socketKey
	}

	if ip, ok := p.rightmostUntrusted(r.Header.Values(headerXFF)); ok {
		return forwardedKeyPrefix + budgetKeyForIP(ip)
	}
	return socketKey
}

// forwardedChainIsAbusive reports a forwarded chain that cannot be attributed
// to anyone AND is the caller's own doing: too long, or too many hops.
//
// Falling back to the socket key for such a chain is what the walk does, and it
// is safe for honest traffic, but it hands the caller a choice: send a chain
// nobody can read and be keyed as the proxy — the one bucket every
// unattributable request shares — rather than as yourself. Refusing the request
// removes the choice. A chain that is merely malformed is NOT abusive: a proxy
// can emit one, and the fallback is the right answer there.
func (p ClientIPPolicy) forwardedChainIsAbusive(r *http.Request) bool {
	if !p.trust || p.header != "" {
		return false
	}
	if !p.peerIsTrustedProxy(socketHost(r.RemoteAddr)) {
		return false
	}
	values := r.Header.Values(headerXFF)
	joined := strings.Join(values, ",")
	if joined == "" {
		return false
	}
	if len(joined) > maxForwardedBytes {
		return true
	}
	return strings.Count(joined, ",")+1 > maxForwardedHops
}

// peerIsTrustedProxy reports whether the socket peer is one of the operator's
// proxies. With no list configured, trust means "everything reaching me comes
// through my proxy" — the legacy --trust-proxy contract.
func (p ClientIPPolicy) peerIsTrustedProxy(peer string) bool {
	if len(p.trusted) == 0 {
		return true
	}
	return p.isTrustedProxy(peer)
}

func (p ClientIPPolicy) isTrustedProxy(ip string) bool {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	for _, pfx := range p.trusted {
		if pfx.Contains(addr) {
			return true
		}
	}
	return false
}

// rightmostUntrusted walks the forwarded chain from the right and returns the
// first entry that is not one of the operator's proxies.
//
// A malformed or empty entry stops the walk: entries are appended left to
// right, so everything already skipped was written by a trusted proxy and the
// first thing that is not a valid address is the start of the region a caller
// could have written. Nothing further left can be attributed to anyone. The
// caller then falls back to the socket address, which is coarse — several
// clients share one budget — but never wrong in the direction that matters.
func (p ClientIPPolicy) rightmostUntrusted(values []string) (string, bool) {
	joined := strings.Join(values, ",")
	if joined == "" || len(joined) > maxForwardedBytes {
		return "", false
	}
	entries := strings.Split(joined, ",")
	for i, hops := len(entries)-1, 0; i >= 0 && hops < maxForwardedHops; i, hops = i-1, hops+1 {
		ip, ok := normalizeIP(entries[i])
		if !ok {
			return "", false
		}
		if p.isTrustedProxy(ip) {
			continue
		}
		return ip, true
	}
	return "", false
}

// socketHost is the address the connection actually came from, normalised the
// same way header entries are so the two are comparable.
func socketHost(remoteAddr string) string {
	host := remoteAddr
	if h, _, err := net.SplitHostPort(remoteAddr); err == nil {
		host = h
	}
	if ip, ok := normalizeIP(host); ok {
		return ip
	}
	return strings.TrimSpace(host)
}

// normalizeIP parses one forwarded entry and returns its canonical text.
//
// It accepts what proxies actually emit: a bare address, a bracketed IPv6
// address, and either with a port (Azure's front ends append "ip:port"). It
// drops any zone and unmaps IPv4-in-IPv6, so ::ffff:192.0.2.1 and 192.0.2.1
// are one identity rather than two budgets.
func normalizeIP(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	if s[0] == '[' {
		end := strings.IndexByte(s, ']')
		if end < 0 {
			return "", false
		}
		if rest := s[end+1:]; rest != "" && !strings.HasPrefix(rest, ":") {
			return "", false
		}
		s = s[1:end]
	} else if strings.Count(s, ":") == 1 {
		// Exactly one colon cannot be an IPv6 address, so it is host:port.
		if h, _, err := net.SplitHostPort(s); err == nil {
			s = h
		}
	}
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return "", false
	}
	return addr.WithZone("").Unmap().String(), true
}

// budgetKeyForIP is the identity a per-client budget is keyed by, which is not
// always the address itself.
//
// IPv6 is keyed per /64, not per /128. A single host is routinely handed a /64
// — that is the standard residential and cloud allocation — so keying the full
// address gives one tenant 18 quintillion identities and every per-source
// budget becomes decorative. IPv4 is keyed exactly, because there a single
// address is genuinely the unit an operator is handed.
//
// Prior art: this is how Cloudflare and nginx's limit_req geo modules aggregate
// IPv6, and why RFC 6177 stopped recommending anything longer than a /64 to a
// single site.
func budgetKeyForIP(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	addr = addr.Unmap()
	if !addr.Is6() {
		return addr.String()
	}
	pfx, err := addr.Prefix(64)
	if err != nil {
		return addr.String()
	}
	return pfx.String()
}
