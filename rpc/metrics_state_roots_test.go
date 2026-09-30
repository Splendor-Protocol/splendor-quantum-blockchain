package rpc

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The state layer's root counts reach /metrics, so an operator can see on a
// live node whether each block's state is hashed once (hashed grows by at most
// one per block) or more.
func TestMetricsExposeStateRootCounts(t *testing.T) {
	m := NewNodeMetrics()
	m.SetStateRoots(7, 12)

	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	for _, want := range []string{
		"sqc_state_roots_hashed_total 7\n",
		"sqc_state_roots_reused_total 12\n",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("/metrics lacks %q:\n%s", strings.TrimSpace(want), body)
		}
	}
}
