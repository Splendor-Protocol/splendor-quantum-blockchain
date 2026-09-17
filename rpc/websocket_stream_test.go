package rpc

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHubRejectsExcessSubscribers(t *testing.T) {
	hub := NewHubWithLimit(1)
	hold, cancel := context.WithCancel(context.Background())
	defer cancel()

	started := make(chan struct{})
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/ws", nil).WithContext(hold)
		rec := httptest.NewRecorder()
		close(started)
		hub.HandleWebSocket(rec, req)
	}()
	<-started
	waitFor(t, func() bool { return hub.ClientCount() == 1 })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	hub.HandleWebSocket(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503 when the subscriber cap is full", rec.Code)
	}
	cancel()
}

func TestSSEBackfillLastEventID(t *testing.T) {
	hub := NewHub()
	hub.Broadcast(&WSEvent{Type: WSEventNewBlock, Timestamp: 1, Data: map[string]int{"n": 1}})
	hub.Broadcast(&WSEvent{Type: WSEventNewBlock, Timestamp: 2, Data: map[string]int{"n": 2}})
	hub.Broadcast(&WSEvent{Type: WSEventNewBlock, Timestamp: 3, Data: map[string]int{"n": 3}})

	srv := httptest.NewServer(http.HandlerFunc(hub.HandleWebSocket))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/ws?subscribe=new_block", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Last-Event-ID", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	got := readSSEData(t, resp.Body, 3)
	if !strings.Contains(got[0], `"connected"`) {
		t.Fatalf("first event %q, want connected", got[0])
	}
	joined := strings.Join(got[1:], "\n")
	if !strings.Contains(joined, `"n":2`) || !strings.Contains(joined, `"n":3`) {
		t.Fatalf("backfill missing events 2 and 3: %v", got[1:])
	}
	if strings.Contains(joined, `"n":1`) {
		t.Fatalf("Last-Event-ID=1 replayed event 1: %v", got[1:])
	}
}

func TestSSERenewsWriteDeadline(t *testing.T) {
	oldHB, oldIdle := sseHeartbeat, sseWriteIdle
	sseHeartbeat = 40 * time.Millisecond
	sseWriteIdle = 200 * time.Millisecond
	defer func() { sseHeartbeat, sseWriteIdle = oldHB, oldIdle }()

	hub := NewHub()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(hub.HandleWebSocket))
	srv.Config.WriteTimeout = 80 * time.Millisecond
	srv.Start()
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	deadline := time.Now().Add(250 * time.Millisecond)
	scanner := bufio.NewScanner(resp.Body)
	heartbeats := 0
	for time.Now().Before(deadline) && scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), ": heartbeat") {
			heartbeats++
		}
	}
	if heartbeats < 2 {
		t.Fatalf("got %d heartbeats past Server.WriteTimeout; write deadline was not renewed", heartbeats)
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for condition")
}

func readSSEData(t *testing.T, r io.Reader, n int) []string {
	t.Helper()
	scanner := bufio.NewScanner(r)
	var out []string
	deadline := time.After(3 * time.Second)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data: ") {
				out = append(out, strings.TrimPrefix(line, "data: "))
				if len(out) >= n {
					return
				}
			}
		}
	}()
	select {
	case <-done:
	case <-deadline:
		t.Fatalf("timed out reading %d SSE events, got %d", n, len(out))
	}
	return out
}
