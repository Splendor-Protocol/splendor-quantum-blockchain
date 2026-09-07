package rpc

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
)

// A follower's schedule arrives with its peer-served genesis, minutes after the
// RPC server was built. /status must report what the executor runs under NOW,
// not what the server was handed at construction — an operator confirming that
// every node agrees on a schedule would otherwise read a stale "none" from a
// node that is in fact gated.
func TestStatusReportsScheduleInstalledAfterConstruction(t *testing.T) {
	s := newTestServer(t, "")

	status := func() map[string]any {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/status", nil)
		rec := httptest.NewRecorder()
		s.router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("/status = %d: %s", rec.Code, rec.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode /status: %v", err)
		}
		return out
	}

	// Constructed with no schedule: the field is absent, not empty, so the
	// response shape of a chain without one is unchanged.
	if got, ok := status()["forks"]; ok {
		t.Fatalf("a node with no schedule reported forks=%v", got)
	}

	s.executor.SetForks(forks.Schedule{forks.MultiValidator: 1234})

	got, ok := status()["forks"].(map[string]any)
	if !ok {
		t.Fatal("schedule installed on the live executor is missing from /status")
	}
	if at, _ := got[forks.MultiValidator].(float64); at != 1234 {
		t.Fatalf("/status.forks = %v, want %s at 1234", got, forks.MultiValidator)
	}
}
