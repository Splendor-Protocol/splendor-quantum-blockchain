// Package rpc — HTTP middleware: origin policy, per-IP rate limiting, and the
// authenticated write plane.
//
// The node separates two classes of route:
//
//   - READ plane  — balances, blocks, receipts, status. Safe to expose publicly.
//   - WRITE plane — block ingest and peer announce. These mutate the chain or
//     the outbound peer set, so they require the shared node token and fail
//     closed when no token is configured.
//
// Transaction submission sits in between: it is public (users must be able to
// broadcast) but rate-limited, because every submission costs an ML-DSA
// verification.
package rpc

import (
	"crypto/subtle"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// ── Origin policy ────────────────────────────────────────────────────────────

// CORSPolicy decides which browser origins may call this node.
//
// The previous default was `Access-Control-Allow-Origin: *`, which let any page
// on the internet script a user's browser into calling their node. The default
// is now an empty allowlist: no CORS headers are emitted at all, so browsers
// refuse cross-origin reads. Operators opt in per origin.
type CORSPolicy struct {
	// allowed is the set of exact origins permitted. A single "*" entry restores
	// the old permissive behaviour for operators who explicitly want it.
	allowed map[string]struct{}
	wild    bool
}

// NewCORSPolicy builds a policy from a list of allowed origins.
func NewCORSPolicy(origins []string) *CORSPolicy {
	p := &CORSPolicy{allowed: make(map[string]struct{}, len(origins))}
	for _, o := range origins {
		o = strings.TrimSpace(o)
		if o == "" {
			continue
		}
		if o == "*" {
			p.wild = true
			continue
		}
		p.allowed[strings.ToLower(strings.TrimSuffix(o, "/"))] = struct{}{}
	}
	return p
}

// Middleware applies the origin policy and answers preflight requests.
func (p *CORSPolicy) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if allow := p.resolve(origin); allow != "" {
			w.Header().Set("Access-Control-Allow-Origin", allow)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Payment, X-Node-Auth")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// resolve returns the value for Access-Control-Allow-Origin, or "" to omit it.
func (p *CORSPolicy) resolve(origin string) string {
	if p.wild {
		return "*"
	}
	if origin == "" {
		return ""
	}
	if _, ok := p.allowed[strings.ToLower(strings.TrimSuffix(origin, "/"))]; ok {
		return origin
	}
	return ""
}

// ── Rate limiting ────────────────────────────────────────────────────────────

// RateLimiter is a per-client token bucket.
//
// Every /tx/submit costs a ~3 KB signature verification, and the Go node
// previously had no inbound limit at all — one host could saturate a validator's
// CPU with well-formed but junk submissions. Buckets are keyed by client IP and
// swept periodically so the map cannot grow without bound.
type RateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*tokenBucket

	ratePerSec float64
	burst      float64

	// trustProxy makes the limiter read X-Forwarded-For. Only enable behind a
	// reverse proxy you control: otherwise any client can spoof the header and
	// mint a fresh bucket per request, bypassing the limit entirely.
	trustProxy bool
}

type tokenBucket struct {
	tokens float64
	last   time.Time
}

// NewRateLimiter creates a limiter allowing ratePerSec sustained requests with
// the given burst. A non-positive rate disables limiting.
func NewRateLimiter(ratePerSec, burst float64, trustProxy bool) *RateLimiter {
	rl := &RateLimiter{
		buckets:    make(map[string]*tokenBucket),
		ratePerSec: ratePerSec,
		burst:      burst,
		trustProxy: trustProxy,
	}
	if ratePerSec > 0 {
		go rl.sweep()
	}
	return rl
}

// sweep evicts buckets that have been idle long enough to have fully refilled,
// so an idle client costs nothing to remember.
func (rl *RateLimiter) sweep() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		cutoff := time.Now().Add(-10 * time.Minute)
		rl.mu.Lock()
		for k, b := range rl.buckets {
			if b.last.Before(cutoff) {
				delete(rl.buckets, k)
			}
		}
		rl.mu.Unlock()
	}
}

// Allow reports whether a request from this client may proceed.
func (rl *RateLimiter) Allow(clientKey string) bool {
	if rl == nil || rl.ratePerSec <= 0 {
		return true
	}
	now := time.Now()

	rl.mu.Lock()
	defer rl.mu.Unlock()

	b, ok := rl.buckets[clientKey]
	if !ok {
		rl.buckets[clientKey] = &tokenBucket{tokens: rl.burst - 1, last: now}
		return true
	}
	b.tokens += now.Sub(b.last).Seconds() * rl.ratePerSec
	if b.tokens > rl.burst {
		b.tokens = rl.burst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// Middleware rejects over-limit callers with 429 before any expensive work.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !rl.Allow(rl.clientKey(r)) {
			w.Header().Set("Retry-After", "1")
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientKey identifies the caller for rate-limiting purposes.
func (rl *RateLimiter) clientKey(r *http.Request) string {
	if rl.trustProxy {
		if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
			// Left-most entry is the original client.
			if i := strings.IndexByte(fwd, ','); i >= 0 {
				fwd = fwd[:i]
			}
			if ip := strings.TrimSpace(fwd); ip != "" {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ── Write plane ──────────────────────────────────────────────────────────────

// WriteAuth guards routes that mutate the chain or the outbound peer set.
//
// It fails closed: with no token configured, every write-plane route returns
// 503 rather than running unauthenticated. That way a node that is missing its
// token is inert instead of silently open.
type WriteAuth struct {
	token string
}

// NewWriteAuth creates a write-plane guard for the given shared token.
func NewWriteAuth(token string) *WriteAuth {
	return &WriteAuth{token: token}
}

// Enabled reports whether a token is configured.
func (a *WriteAuth) Enabled() bool { return a != nil && a.token != "" }

// Check reports whether the request carries the write-plane token, compared in
// constant time so the token cannot be recovered by timing the comparison.
func (a *WriteAuth) Check(r *http.Request) bool {
	if !a.Enabled() {
		return false
	}
	got := r.Header.Get("X-Node-Auth")
	if got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(a.token)) == 1
}

// Middleware enforces the write-plane token.
func (a *WriteAuth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.Enabled() {
			writeError(w, http.StatusServiceUnavailable,
				"write plane disabled: this node has no --rpc-auth-token configured")
			return
		}
		if !a.Check(r) {
			log.Warn().
				Str("path", r.URL.Path).
				Str("remote", r.RemoteAddr).
				Msg("rejected unauthenticated write-plane request")
			writeError(w, http.StatusUnauthorized, "missing or invalid X-Node-Auth token")
			return
		}
		next.ServeHTTP(w, r)
	})
}
