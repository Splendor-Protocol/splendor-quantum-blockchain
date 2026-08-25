package mempool

import (
	"math/big"
	"strings"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// One funded address must not be able to occupy more than MaxPerAddr pending slots,
// even when the global pool cap is far larger.
func TestPerAddressCap(t *testing.T) {
	sdb, err := state.NewStateDB(t.TempDir())
	if err != nil {
		t.Fatalf("statedb: %v", err)
	}
	defer sdb.Close()

	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	from := qcrypto.DeriveAddress(pub)
	acc := types.NewAccount(from)
	acc.AddBalance(big.NewInt(1_000_000_000)) // enough for every tx's fee
	sdb.SetAccount(acc)
	if _, err := sdb.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var to qcrypto.Address
	for i := range to {
		to[i] = 0x55
	}

	m := New(10_000, sdb) // global cap >> per-address cap, so per-address is the limiter

	for i := 0; i < MaxPerAddr; i++ {
		tx := types.NewTransfer(from, to, big.NewInt(0), big.NewInt(int64(MinFee)), uint64(i), int64(1_700_000_000+i))
		if err := tx.Sign(priv, true); err != nil {
			t.Fatalf("sign %d: %v", i, err)
		}
		if err := m.Add(tx); err != nil {
			t.Fatalf("add %d unexpectedly rejected: %v", i, err)
		}
	}

	// The (MaxPerAddr+1)-th from the same sender must be rejected by the cap.
	over := types.NewTransfer(from, to, big.NewInt(0), big.NewInt(int64(MinFee)), uint64(MaxPerAddr), 1_700_099_999)
	if err := over.Sign(priv, true); err != nil {
		t.Fatalf("sign overflow tx: %v", err)
	}
	err = m.Add(over)
	if err == nil {
		t.Fatal("expected per-address cap rejection, got nil")
	}
	if !strings.Contains(err.Error(), "per-address") {
		t.Fatalf("expected per-address error, got: %v", err)
	}
}

func fundedMempool(t *testing.T) (*Mempool, *state.StateDB, qcrypto.Address, *qcrypto.PrivateKey) {
	t.Helper()
	sdb, err := state.NewStateDB(t.TempDir())
	if err != nil {
		t.Fatalf("statedb: %v", err)
	}
	pub, priv, _ := qcrypto.GenerateKeypair()
	from := qcrypto.DeriveAddress(pub)
	acc := types.NewAccount(from)
	acc.AddBalance(big.NewInt(1_000_000_000))
	sdb.SetAccount(acc)
	if _, err := sdb.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return New(10_000, sdb), sdb, from, priv
}

// A resubmitted (from, nonce) must be rejected so it can't pile up and hit the cap.
func TestDuplicateNonceRejected(t *testing.T) {
	m, _, from, priv := fundedMempool(t)
	var to qcrypto.Address
	for i := range to {
		to[i] = 0x55
	}
	tx1 := types.NewTransfer(from, to, big.NewInt(0), big.NewInt(int64(MinFee)), 0, 1_700_000_000)
	_ = tx1.Sign(priv, true)
	if err := m.Add(tx1); err != nil {
		t.Fatalf("first add: %v", err)
	}
	tx2 := types.NewTransfer(from, to, big.NewInt(5), big.NewInt(int64(MinFee)), 0, 1_700_000_001) // same nonce 0
	_ = tx2.Sign(priv, true)
	err := m.Add(tx2)
	if err == nil || !strings.Contains(err.Error(), "duplicate pending nonce") {
		t.Fatalf("expected duplicate-nonce rejection, got %v", err)
	}
	if m.Size() != 1 {
		t.Fatalf("size=%d, want 1", m.Size())
	}
}

// PruneStale must drop a pending tx once the sender's committed nonce passes it.
func TestPruneStale(t *testing.T) {
	m, sdb, from, priv := fundedMempool(t)
	var to qcrypto.Address
	for i := range to {
		to[i] = 0x55
	}
	tx := types.NewTransfer(from, to, big.NewInt(0), big.NewInt(int64(MinFee)), 0, 1_700_000_000)
	_ = tx.Sign(priv, true)
	if err := m.Add(tx); err != nil {
		t.Fatalf("add: %v", err)
	}
	// Simulate the tx being applied: advance the committed nonce past 0.
	acc, _ := sdb.GetAccount(from)
	acc.Nonce = 1
	sdb.SetAccount(acc)
	_, _ = sdb.Commit()

	if n := m.PruneStale(); n != 1 {
		t.Fatalf("pruned %d, want 1", n)
	}
	if m.Size() != 0 {
		t.Fatalf("size=%d after prune, want 0", m.Size())
	}
}
