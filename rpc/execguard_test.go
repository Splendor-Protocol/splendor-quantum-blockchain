package rpc

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func guardRequest(t *testing.T, h http.Handler) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/call", nil)
	req.RemoteAddr = "203.0.113.7:5555"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

// A nil guard must pass traffic through untouched, so route registration in
// tests and embedders that do not build a Server keep working.
func TestExecGuardNilPassesThrough(t *testing.T) {
	var g *ExecGuard
	h := g.Wrap(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	if code := guardRequest(t, h); code != http.StatusOK {
		t.Fatalf("nil guard returned %d, want 200", code)
	}
}

func TestExecGuardRateLimits(t *testing.T) {
	g := NewExecGuard(1, 2, 0, 0, false)
	h := g.Wrap(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

	if code := guardRequest(t, h); code != http.StatusOK {
		t.Fatalf("first request: got %d, want 200", code)
	}
	if code := guardRequest(t, h); code != http.StatusOK {
		t.Fatalf("second request (within burst): got %d, want 200", code)
	}
	if code := guardRequest(t, h); code != http.StatusTooManyRequests {
		t.Fatalf("third request: got %d, want 429", code)
	}
}

// The concurrency cap is the control that actually bounds CPU: it must shed
// load once the in-flight limit is reached, not queue behind it.
func TestExecGuardShedsBeyondConcurrencyCap(t *testing.T) {
	const cap = 2
	g := NewExecGuard(0, 0, cap, 0, false) // rate limiting off, concurrency only

	release := make(chan struct{})
	entered := make(chan struct{}, cap)
	h := g.Wrap(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
		w.WriteHeader(http.StatusOK)
	})

	var wg sync.WaitGroup
	for i := 0; i < cap; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); guardRequest(t, h) }()
	}
	// Wait until both slots are genuinely occupied before probing the overflow.
	for i := 0; i < cap; i++ {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("handlers did not start")
		}
	}

	if code := guardRequest(t, h); code != http.StatusServiceUnavailable {
		t.Fatalf("request over the cap: got %d, want 503", code)
	}

	close(release)
	wg.Wait()

	// Slots are returned, so the guard accepts work again.
	if code := guardRequest(t, h); code != http.StatusOK {
		t.Fatalf("after slots freed: got %d, want 200", code)
	}
}

// Unset config values must not leave these routes unmetered.
func TestNewSimGuardFillsDefaults(t *testing.T) {
	g := newSimGuard(Config{})
	if g == nil {
		t.Fatal("newSimGuard returned nil")
	}
	if g.slots == nil || cap(g.slots) != defaultSimMaxInFlight {
		t.Fatalf("concurrency cap not applied: %v", g.slots)
	}
	if g.timeout != defaultSimTimeout {
		t.Fatalf("timeout = %v, want %v", g.timeout, defaultSimTimeout)
	}
	if g.limiter.ratePerSec != defaultSimRatePerSec {
		t.Fatalf("rate = %v, want %v", g.limiter.ratePerSec, defaultSimRatePerSec)
	}
}

func TestNewSimGuardHonoursOverrides(t *testing.T) {
	g := newSimGuard(Config{
		SimRatePerSec:  5,
		SimBurst:       9,
		SimMaxInFlight: 3,
		SimTimeout:     2 * time.Second,
	})
	if g.limiter.ratePerSec != 5 || g.limiter.burst != 9 {
		t.Fatalf("rate/burst = %v/%v, want 5/9", g.limiter.ratePerSec, g.limiter.burst)
	}
	if cap(g.slots) != 3 {
		t.Fatalf("in-flight cap = %d, want 3", cap(g.slots))
	}
	if g.timeout != 2*time.Second {
		t.Fatalf("timeout = %v, want 2s", g.timeout)
	}
}
