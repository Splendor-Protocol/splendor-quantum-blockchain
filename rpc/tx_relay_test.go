package rpc

import (
	"bytes"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/block"
	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/mempool"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network"
)

// fundedTransfer builds a signed transfer from a funded account in sdb.
func fundedTransfer(t *testing.T, sdb *state.StateDB, nonce uint64) *types.Transaction {
	t.Helper()
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	from := qcrypto.DeriveAddress(pub)
	acc := types.NewAccount(from)
	acc.AddBalance(new(big.Int).SetUint64(1_000_000_000_000_000_000))
	sdb.SetAccount(acc)
	if _, err := sdb.Commit(); err != nil {
		t.Fatal(err)
	}
	tx := types.NewTransfer(from, qcrypto.Address{0x42}, big.NewInt(1000),
		new(big.Int).SetUint64(uint64(mempool.MinFee)), nonce, time.Now().Unix())
	if err := tx.Sign(priv, nonce == 0); err != nil {
		t.Fatal(err)
	}
	return tx
}

func relayServer(t *testing.T, relay func(*types.Transaction)) (*Server, *state.StateDB) {
	t.Helper()
	sdb, err := state.NewStateDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ex, err := block.NewExecutor(sdb, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ex.Close(); sdb.Close() })
	pool := mempool.New(100, sdb)
	return New(Config{ListenAddr: ":0", RelayTx: relay}, sdb, ex, pool, nil, nil), sdb
}

// A transaction lands in a block only when the node holding it leads a height.
// Without a relay, one submitted to any other node was never included — the
// shape an operator hits registering a validator through an RPC node.
func TestAdmittedTransactionIsRelayedToPeers(t *testing.T) {
	relayed := make(chan *types.Transaction, 4)
	s, sdb := relayServer(t, func(tx *types.Transaction) { relayed <- tx })
	tx := fundedTransfer(t, sdb, 0)

	body, err := json.Marshal(tx)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/tx/submit", bytes.NewReader(body)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("submit returned %d: %s", rec.Code, rec.Body.String())
	}

	select {
	case got := <-relayed:
		want, _ := tx.FullHash()
		have, _ := got.FullHash()
		if want != have {
			t.Fatal("a different transaction was relayed")
		}
	default:
		t.Fatal("an admitted transaction was not relayed to peers")
	}
}

// Only what this node itself accepted is passed on. A transaction the mempool
// refused must not be amplified across the network.
func TestRejectedTransactionIsNotRelayed(t *testing.T) {
	relayed := 0
	s, sdb := relayServer(t, func(*types.Transaction) { relayed++ })
	tx := fundedTransfer(t, sdb, 0)
	tx.Signature[0] ^= 0xff

	body, err := json.Marshal(tx)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/tx/submit", bytes.NewReader(body)))
	if rec.Code == http.StatusAccepted {
		t.Fatal("a transaction with a broken signature was accepted")
	}
	if relayed != 0 {
		t.Fatal("a rejected transaction was relayed to peers")
	}
}

// The relay rides the sealed write plane, so it needs the write-plane token
// like every other mutating kind, and it is refused without one.
func TestSealedTransactionRelayIsTokenGated(t *testing.T) {
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	identity, err := network.NewNodeIdentity(pub, priv)
	if err != nil {
		t.Fatal(err)
	}
	seen := make(chan []byte, 4)
	router := mux.NewRouter()
	RegisterP2PRoutes(router, P2PConfig{
		Identity: identity,
		Token:    "write-secret",
		HandleTx: func(b []byte) error { seen <- b; return nil },
	})
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	cache := network.NewIdentityCache()
	if err := cache.SendSealed(srv.URL, "write-secret", network.KindTx, []byte(`{"hello":1}`)); err != nil {
		t.Fatalf("sealed relay with the token: %v", err)
	}
	select {
	case got := <-seen:
		if string(got) != `{"hello":1}` {
			t.Fatalf("handler got %q", got)
		}
	default:
		t.Fatal("the sealed relay never reached the handler")
	}

	if err := network.NewIdentityCache().SendSealed(srv.URL, "wrong", network.KindTx, []byte(`{}`)); err == nil {
		t.Fatal("a relayed transaction was accepted without the write-plane token")
	}
	select {
	case <-seen:
		t.Fatal("an unauthenticated relay reached the handler")
	default:
	}
}
