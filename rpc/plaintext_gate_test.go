package rpc

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/block"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/mempool"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
)

func plaintextTestServer(t *testing.T, allowPlaintext bool) *Server {
	t.Helper()
	sdb, err := state.NewStateDB(t.TempDir())
	if err != nil {
		t.Fatalf("statedb: %v", err)
	}
	ex, err := block.NewExecutor(sdb, t.TempDir())
	if err != nil {
		t.Fatalf("executor: %v", err)
	}
	return New(Config{
		ListenAddr:        ":0",
		AuthToken:         "token",
		AllowPlaintextP2P: allowPlaintext,
	}, sdb, ex, mempool.New(100, sdb), nil, nil)
}

// --allow-plaintext-p2p must gate what this node ACCEPTS, not only what it sends.
//
// It used to govern the outbound fallback alone, so a node that refused to SEND
// cleartext still ACCEPTED a cleartext POST /block — which is not what "the
// channel cannot be downgraded" means to anyone reading it.
func TestPlaintextBlockRouteIsRefusedByDefault(t *testing.T) {
	rec := httptest.NewRecorder()
	plaintextTestServer(t, false).buildRouter().
		ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/block", nil))

	if rec.Code == http.StatusNotFound {
		t.Fatal("route is absent; it should refuse explicitly so a legacy peer is told why")
	}
	if rec.Code != http.StatusGone {
		t.Fatalf("POST /block returned %d, want 410 when plaintext p2p is off", rec.Code)
	}
}

func TestPlaintextBlockRouteIsServedWhenAllowed(t *testing.T) {
	rec := httptest.NewRecorder()
	plaintextTestServer(t, true).buildRouter().
		ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/block", nil))

	if rec.Code == http.StatusGone {
		t.Fatal("route still refuses although plaintext p2p is enabled")
	}
}
