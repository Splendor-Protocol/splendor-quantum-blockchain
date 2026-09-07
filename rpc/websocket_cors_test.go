package rpc

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
)

// /ws streams live blocks and pending transactions, so it must obey the same
// origin allowlist as the rest of the RPC surface. It used to force
// `Access-Control-Allow-Origin: *`, which left it readable by any page on the
// internet no matter how the operator configured --cors-origin.

// With default flags nothing is allowlisted, so the stream must carry no
// allow-origin header at all — least of all a wildcard.
func TestWebSocketNoWildcardByDefault(t *testing.T) {
	rec := serveWS(t, NewCORSPolicy(nil), "https://evil.example")
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("/ws echoed Access-Control-Allow-Origin %q with an empty allowlist", got)
	}
}

// An allowlisted origin gets echoed back exactly, and an origin that is not on
// the list gets nothing — same contract as every other route.
func TestWebSocketHonoursAllowlist(t *testing.T) {
	const allowed = "https://quantum-explorer.splendor.org"
	policy := NewCORSPolicy([]string{allowed})

	rec := serveWS(t, policy, allowed)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != allowed {
		t.Errorf("allowlisted origin got %q, want %q", got, allowed)
	}

	rec = serveWS(t, policy, "https://evil.example")
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("non-allowlisted origin got %q, want no header", got)
	}
}

// An operator who genuinely wants the old behaviour opts in with
// `--cors-origin '*'`; the wildcard must come from that flag, not the handler.
func TestWebSocketWildcardOnlyOnOptIn(t *testing.T) {
	rec := serveWS(t, NewCORSPolicy([]string{"*"}), "https://anything.example")
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("explicit wildcard opt-in got %q, want \"*\"", got)
	}
}

// The header is only half the job: an allowed origin must still receive the
// stream itself, headers included, or the explorer's live updates break.
func TestWebSocketStreamsToAllowedOrigin(t *testing.T) {
	const origin = "https://quantum-explorer.splendor.org"

	hub := NewHub()
	srv := httptest.NewServer(NewCORSPolicy([]string{origin}).Middleware(http.HandlerFunc(hub.HandleWebSocket)))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/ws?subscribe=new_block", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Origin", origin)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer resp.Body.Close()

	// Response headers arrive with the first flushed event, so reaching here
	// already proves the stream is live.
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != origin {
		t.Fatalf("streaming response dropped the allow-origin header: got %q, want %q", got, origin)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type %q, want text/event-stream", got)
	}

	events := make(chan string, 8)
	go func() {
		defer close(events)
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			if line := scanner.Text(); strings.HasPrefix(line, "data: ") {
				events <- strings.TrimPrefix(line, "data: ")
			}
		}
	}()

	if got := nextEvent(t, events); !strings.Contains(got, `"connected"`) {
		t.Fatalf("first event %q, want the connection confirmation", got)
	}

	// The client is subscribed by the time it has seen "connected", so a
	// broadcast now must reach it.
	hub.Broadcast(&WSEvent{
		Type:      WSEventNewBlock,
		Timestamp: time.Now().Unix(),
		Data:      map[string]interface{}{"height": 42},
	})
	if got := nextEvent(t, events); !strings.Contains(got, string(WSEventNewBlock)) {
		t.Fatalf("second event %q, want a %s event", got, WSEventNewBlock)
	}
}

// Preflight is the one path the handler never sees: /ws is registered GET-only,
// so an OPTIONS request does not match the route and mux would answer 405 with
// no CORS headers at all. It works because cmd/node wraps the whole router in
// the policy rather than registering it as mux middleware — pin that wiring
// here, because moving the policy onto the router would silently break
// preflight for every allowlisted dapp.
func TestWebSocketPreflightFollowsTheAllowlist(t *testing.T) {
	const allowed = "https://quantum-explorer.splendor.org"

	for _, tc := range []struct {
		name   string
		policy *CORSPolicy
		origin string
		want   string
	}{
		{"empty allowlist", NewCORSPolicy(nil), "https://evil.example", ""},
		{"allowlisted", NewCORSPolicy([]string{allowed}), allowed, allowed},
		{"not allowlisted", NewCORSPolicy([]string{allowed}), "https://evil.example", ""},
	} {
		router := mux.NewRouter()
		router.HandleFunc("/ws", NewHub().HandleWebSocket).Methods(http.MethodGet)

		req := httptest.NewRequest(http.MethodOptions, "/ws", nil)
		req.Header.Set("Origin", tc.origin)
		req.Header.Set("Access-Control-Request-Method", "GET")

		rec := httptest.NewRecorder()
		tc.policy.Middleware(router).ServeHTTP(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Errorf("%s: preflight status %d, want %d", tc.name, rec.Code, http.StatusNoContent)
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != tc.want {
			t.Errorf("%s: preflight allow-origin %q, want %q", tc.name, got, tc.want)
		}
	}
}

// Near misses of an allowlisted origin must not open the stream. A subdomain, a
// suffix, a different scheme or port and a sandboxed iframe's "null" are all
// distinct origins to a browser, so they have to be distinct to the node too.
func TestWebSocketRejectsNearMissOrigins(t *testing.T) {
	const allowed = "https://explorer.splendor.org"
	policy := NewCORSPolicy([]string{allowed})

	for _, origin := range []string{
		"https://evil.explorer.splendor.org", // subdomain of the allowlisted host
		"https://explorer.splendor.org.evil", // allowlisted origin as a prefix
		"https://explorer.splendor.orgevil",  // no separator at all
		"http://explorer.splendor.org",       // scheme downgrade
		"https://explorer.splendor.org:8443", // explicit non-default port
		"https://explorer.splendor.org/path", // more than an origin
		" https://explorer.splendor.org",     // padded past the exact match
		"null",                               // sandboxed iframe, data: or file:
		"*",                                  // asking for the old default
	} {
		rec := serveWS(t, policy, origin)
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("origin %q got allow-origin %q, want no header", origin, got)
		}
	}
}

// An operator who writes the origin with a trailing slash still gets a working
// allowlist entry: the config is normalised, not taken literally.
func TestWebSocketAllowlistToleratesConfigSlash(t *testing.T) {
	const origin = "https://explorer.splendor.org"

	rec := serveWS(t, NewCORSPolicy([]string{origin + "/"}), origin)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != origin {
		t.Fatalf("origin allowlisted with a trailing slash got %q, want %q", got, origin)
	}
}

// Vary must be on the response whether or not the origin was allowed. A denied
// response with no Vary is cacheable under a key that ignores Origin, and the
// CDN in front of these nodes would then replay it — stripped of its
// allow-origin header — to a browser that is on the allowlist.
func TestWebSocketAlwaysVariesOnOrigin(t *testing.T) {
	const allowed = "https://explorer.splendor.org"
	policy := NewCORSPolicy([]string{allowed})

	for _, origin := range []string{allowed, "https://evil.example", ""} {
		rec := serveWS(t, policy, origin)
		if got := rec.Header().Get("Vary"); got != "Origin" {
			t.Errorf("origin %q got Vary %q, want %q", origin, got, "Origin")
		}
	}
}

// serveWS runs one /ws request through the policy the node applies to its whole
// router. The request context is already cancelled, so the handler emits its
// headers and connection confirmation and then returns instead of blocking on
// the heartbeat loop.
func serveWS(t *testing.T, p *CORSPolicy, origin string) *httptest.ResponseRecorder {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req := httptest.NewRequest(http.MethodGet, "/ws", nil).WithContext(ctx)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}

	rec := httptest.NewRecorder()
	p.Middleware(http.HandlerFunc(NewHub().HandleWebSocket)).ServeHTTP(rec, req)
	return rec
}

// nextEvent waits for one SSE payload, failing the test rather than hanging if
// the stream stalls.
func nextEvent(t *testing.T, events <-chan string) string {
	t.Helper()
	select {
	case ev, ok := <-events:
		if !ok {
			t.Fatal("stream closed before delivering an event")
		}
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for an SSE event")
		return ""
	}
}
