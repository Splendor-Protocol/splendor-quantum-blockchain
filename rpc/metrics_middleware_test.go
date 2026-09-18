package rpc

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Every request the listener takes is one count, whatever the method or the
// route, and that count is what /metrics exposes as sqc_rpc_requests_total.
// The counter was exported before anything drove it, so the "RPC requests"
// panel read zero by construction.
func TestMetricsMiddlewareCountsEveryRequest(t *testing.T) {
	m := NewNodeMetrics()
	h := m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	for _, rq := range []struct{ method, path string }{
		{http.MethodGet, "/status"},
		{http.MethodPost, "/tx/submit"},
		{http.MethodOptions, "/balance/SQ:00"},
		{http.MethodGet, "/no/such/route"},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(rq.method, rq.path, nil))
		if rec.Code != http.StatusTeapot {
			t.Fatalf("%s %s: the wrapped handler did not answer: %d", rq.method, rq.path, rec.Code)
		}
	}
	if got := m.RPCRequests.Load(); got != 4 {
		t.Fatalf("sqc_rpc_requests_total = %d, want 4", got)
	}

	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(rec.Body.String(), "sqc_rpc_requests_total 4\n") {
		t.Fatalf("/metrics does not expose the count:\n%s", rec.Body.String())
	}
}

// A nil collector passes traffic through untouched, like the other wrappers,
// so embedders that build no metrics keep working.
func TestMetricsMiddlewareNilPassesThrough(t *testing.T) {
	var m *NodeMetrics
	h := m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("nil metrics returned %d, want 200", rec.Code)
	}
}
