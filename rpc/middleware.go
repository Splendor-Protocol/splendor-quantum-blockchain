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
	"context"
	"crypto/subtle"
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
		// Vary goes on every response, allowed or not. These nodes answer through
		// a tunnel and a CDN, and a denied response that carries no Vary is
		// cacheable under a key that ignores Origin — the cache then replays it,
		// stripped of its allow-origin header, to a browser that is on the
		// allowlist.
		w.Header().Add("Vary", "Origin")
		if allow := p.resolve(origin); allow != "" {
			w.Header().Set("Access-Control-Allow-Origin", allow)
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

	// clientIP decides which address identifies the caller: the socket peer,
	// or — behind a proxy the operator configured — the right-most forwarded
	// entry that proxy did not write. See ClientIPPolicy.
	clientIP ClientIPPolicy
}

type tokenBucket struct {
	tokens float64
	last   time.Time
}

// NewRateLimiter creates a limiter allowing ratePerSec sustained requests with
// the given burst. A non-positive rate disables limiting.
func NewRateLimiter(ratePerSec, burst float64, clientIP ClientIPPolicy) *RateLimiter {
	rl := &RateLimiter{
		buckets:    make(map[string]*tokenBucket),
		ratePerSec: ratePerSec,
		burst:      burst,
		clientIP:   clientIP,
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
		if rl.clientIP.forwardedChainIsAbusive(r) {
			writeError(w, http.StatusBadRequest, "X-Forwarded-For is too long to attribute")
			return
		}
		if !rl.Allow(rl.clientKey(r)) {
			w.Header().Set("Retry-After", "1")
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientKey identifies the caller for rate-limiting purposes. The selection
// rule lives in ClientIPPolicy, shared with the sealed transport's per-source
// cap, because a caller able to choose its key defeats either budget alone.
func (rl *RateLimiter) clientKey(r *http.Request) string {
	return rl.clientIP.clientKey(r)
}

// ── Simulation plane ─────────────────────────────────────────────────────────

// ExecGuard meters routes that run the VM on behalf of an anonymous caller:
// /call, /estimate, and the receipt-index scan behind /logs.
//
// A rate limit alone is not enough for these. Each request can occupy a core
// for the whole gas budget, so N concurrent slow calls pin N cores regardless
// of how few requests per second any single IP is allowed. The guard therefore
// layers a global in-flight cap on top of the per-IP bucket, and bounds how
// long a request may hold a slot so a wedged execution cannot retire one
// permanently.
type ExecGuard struct {
	limiter *RateLimiter
	slots   chan struct{}
	timeout time.Duration
}

// NewExecGuard builds a guard allowing ratePerSec requests per IP with the
// given burst, at most maxInFlight concurrent executions, each bounded by
// timeout. Non-positive maxInFlight or timeout disables that dimension.
func NewExecGuard(ratePerSec, burst float64, maxInFlight int, timeout time.Duration, clientIP ClientIPPolicy) *ExecGuard {
	g := &ExecGuard{
		limiter: NewRateLimiter(ratePerSec, burst, clientIP),
		timeout: timeout,
	}
	if maxInFlight > 0 {
		g.slots = make(chan struct{}, maxInFlight)
	}
	return g
}

// Wrap applies the guard to a handler. A nil guard passes the handler through
// unchanged, which keeps tests that register routes directly working.
func (g *ExecGuard) Wrap(h http.HandlerFunc) http.Handler {
	if g == nil {
		return h
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if g.limiter != nil && g.limiter.clientIP.forwardedChainIsAbusive(r) {
			writeError(w, http.StatusBadRequest, "X-Forwarded-For is too long to attribute")
			return
		}
		if !g.limiter.Allow(g.limiter.clientKey(r)) {
			w.Header().Set("Retry-After", "1")
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}

		if g.slots != nil {
			select {
			case g.slots <- struct{}{}:
				defer func() { <-g.slots }()
			default:
				// Shed rather than queue: a queued request still holds a
				// connection and a goroutine, which is most of the cost.
				w.Header().Set("Retry-After", "1")
				writeError(w, http.StatusServiceUnavailable, "node busy: too many concurrent simulations")
				return
			}
		}

		if g.timeout > 0 {
			ctx, cancel := context.WithTimeout(r.Context(), g.timeout)
			defer cancel()
			r = r.WithContext(ctx)
		}
		h.ServeHTTP(w, r)
	})
}

// ── Request counting ─────────────────────────────────────────────────────────

// Middleware counts every request the listener takes into
// sqc_rpc_requests_total. It is meant to be the outermost wrapper on the
// server: every route, every method, preflight included, is one atomic add
// with no labels, so the counter says how much traffic the node takes rather
// than how much the RPC routes answered. The counter was exported long before
// anything drove it, so the "RPC requests" panel read zero by construction.
// A nil receiver passes the handler through unchanged.
func (m *NodeMetrics) Middleware(next http.Handler) http.Handler {
	if m == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.IncrRPCRequests()
		next.ServeHTTP(w, r)
	})
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
