package rpc

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
