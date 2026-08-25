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

// fundedSender returns a funded address plus its signing key.
func fundedSender(t *testing.T, sdb *state.StateDB) (qcrypto.Address, *qcrypto.PrivateKey) {
	t.Helper()
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	addr := qcrypto.DeriveAddress(pub)
	acc := types.NewAccount(addr)
	acc.AddBalance(big.NewInt(1_000_000_000_000))
	sdb.SetAccount(acc)
	if _, err := sdb.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return addr, priv
}

// The fee floor must rise as the pool fills, so sustaining congestion costs an
// attacker progressively more than filling an empty pool did.
func TestMinFeeRisesWithLoad(t *testing.T) {
	sdb, err := state.NewStateDB(t.TempDir())
	if err != nil {
		t.Fatalf("statedb: %v", err)
	}
	defer sdb.Close()

	m := New(100, sdb)
	if got := m.MinFeeNow(); got != MinFee {
		t.Fatalf("empty pool floor=%d, want %d", got, MinFee)
	}

	var to qcrypto.Address
	for i := range to {
		to[i] = 0x55
	}

	// Fill to 90% using many senders, since MaxPerAddr bounds each one.
	added := 0
	for s := 0; added < 90; s++ {
		from, priv := fundedSender(t, sdb)
		for n := 0; n < MaxPerAddr && added < 90; n++ {
			tx := types.NewTransfer(from, to, big.NewInt(0), big.NewInt(MinFee*64), uint64(n), int64(1_700_000_000+added))
			if err := tx.Sign(priv, true); err != nil {
				t.Fatalf("sign: %v", err)
			}
			if err := m.Add(tx); err != nil {
				t.Fatalf("add %d rejected: %v", added, err)
			}
			added++
		}
	}

	if got, want := m.MinFeeNow(), int64(MinFee*32); got != want {
		t.Fatalf("floor at 90%% load=%d, want %d", got, want)
	}

	// A transaction paying the old flat MinFee must now be refused.
	from, priv := fundedSender(t, sdb)
	cheap := types.NewTransfer(from, to, big.NewInt(0), big.NewInt(MinFee), 0, 1_700_500_000)
	if err := cheap.Sign(priv, true); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := m.Add(cheap); err == nil || !strings.Contains(err.Error(), "fee too low") {
		t.Fatalf("expected congestion fee rejection, got %v", err)
	}
}

// A full pool must not lock out a higher-paying transaction: the cheapest
// resident is displaced instead. Otherwise a spammer who filled every slot
// cheaply could block all later traffic regardless of what it paid.
func TestFullPoolEvictsCheapestForHigherBid(t *testing.T) {
	sdb, err := state.NewStateDB(t.TempDir())
	if err != nil {
		t.Fatalf("statedb: %v", err)
	}
	defer sdb.Close()

	const cap = 8
	m := New(cap, sdb)

	var to qcrypto.Address
	for i := range to {
		to[i] = 0x55
	}

	// Fill every slot. The floor at full load is MinFee*32, so bid above it.
	squatter, squatterKey := fundedSender(t, sdb)
	for n := 0; n < cap; n++ {
		tx := types.NewTransfer(squatter, to, big.NewInt(0), big.NewInt(MinFee*32), uint64(n), int64(1_700_000_000+n))
		if err := tx.Sign(squatterKey, true); err != nil {
			t.Fatalf("sign: %v", err)
		}
		if err := m.Add(tx); err != nil {
			t.Fatalf("fill %d rejected: %v", n, err)
		}
	}
	if m.Size() != cap {
		t.Fatalf("size=%d, want %d", m.Size(), cap)
	}

	// An equal bid must not displace an incumbent (no free churn).
	rival, rivalKey := fundedSender(t, sdb)
	equal := types.NewTransfer(rival, to, big.NewInt(0), big.NewInt(MinFee*32), 0, 1_700_400_000)
	if err := equal.Sign(rivalKey, true); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := m.Add(equal); err != types.ErrMempoolFull {
		t.Fatalf("equal bid: got %v, want ErrMempoolFull", err)
	}

	// A strictly higher bid must get in, displacing one incumbent.
	higher := types.NewTransfer(rival, to, big.NewInt(0), big.NewInt(MinFee*64), 0, 1_700_400_001)
	if err := higher.Sign(rivalKey, true); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := m.Add(higher); err != nil {
		t.Fatalf("higher bid rejected: %v", err)
	}
	if m.Size() != cap {
		t.Fatalf("size=%d after eviction, want %d", m.Size(), cap)
	}

	// The admitted transaction must actually be resident.
	hash, err := higher.FullHash()
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if _, ok := m.txs[hash]; !ok {
		t.Fatal("higher-paying tx was not admitted")
	}
}
