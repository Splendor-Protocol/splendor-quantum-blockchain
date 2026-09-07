package mempool

import (
	"math/big"
	"strings"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
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

// addTransfer signs and admits one transfer, failing the test on rejection.
func addTransfer(t *testing.T, m *Mempool, from qcrypto.Address, priv *qcrypto.PrivateKey, nonce uint64, fee int64, ts int64) *types.Transaction {
	t.Helper()
	var to qcrypto.Address
	for i := range to {
		to[i] = 0x55
	}
	tx := types.NewTransfer(from, to, big.NewInt(0), big.NewInt(fee), nonce, ts)
	if err := tx.Sign(priv, true); err != nil {
		t.Fatalf("sign nonce %d: %v", nonce, err)
	}
	if err := m.Add(tx); err != nil {
		t.Fatalf("add nonce %d: %v", nonce, err)
	}
	return tx
}

func noncesOf(txs []*types.Transaction) []uint64 {
	out := make([]uint64, len(txs))
	for i, tx := range txs {
		out[i] = tx.Nonce
	}
	return out
}

// With equal fee and timestamp the old sort was a coin flip between a sender's
// nonces, and the producer rejects nonce 2 before nonce 1 ("want 1"). Pending
// must emit a sender's transactions in nonce order every time, whatever order
// they were added in and however the map iterates.
func TestPendingEmitsSenderNoncesInOrder(t *testing.T) {
	m, sdb, from, priv := fundedMempool(t)
	defer sdb.Close()

	// Added out of order, all at the same fee and timestamp.
	for _, n := range []uint64{3, 0, 4, 1, 2} {
		addTransfer(t, m, from, priv, n, MinFee, 1_700_000_000)
	}
	for run := 0; run < 50; run++ {
		got := noncesOf(m.Pending(10))
		for i, n := range got {
			if n != uint64(i) {
				t.Fatalf("run %d: Pending = %v, want nonces 0..4 in order", run, got)
			}
		}
	}
}

// Fee priority across senders is unchanged: the higher bidder goes first even
// if it arrived later.
func TestPendingFeePriorityAcrossSenders(t *testing.T) {
	m, sdb, cheap, cheapKey := fundedMempool(t)
	defer sdb.Close()
	rich, richKey := fundedSender(t, sdb)

	addTransfer(t, m, cheap, cheapKey, 0, MinFee, 1_700_000_000)
	addTransfer(t, m, rich, richKey, 0, MinFee*4, 1_700_000_001)

	got := m.Pending(10)
	if len(got) != 2 || got[0].From != rich || got[1].From != cheap {
		t.Fatalf("Pending order = %v, want the higher fee first", got)
	}
}

// Re-slotting must happen BEFORE the cut at n. A sender whose lowest nonce is
// its cheapest transaction, asked for fewer slots than it has pending, must
// still get that lowest nonce out first — substituting after truncation
// would emit nonce 1 and 2 without nonce 0, which the producer cannot apply.
func TestPendingTruncationKeepsLowestNonceFirst(t *testing.T) {
	m, sdb, from, priv := fundedMempool(t)
	defer sdb.Close()
	other, otherKey := fundedSender(t, sdb)

	addTransfer(t, m, from, priv, 0, MinFee, 1_700_000_000)   // cheapest, lowest nonce
	addTransfer(t, m, from, priv, 1, MinFee*5, 1_700_000_001) // expensive
	addTransfer(t, m, from, priv, 2, MinFee*5, 1_700_000_002) // expensive
	addTransfer(t, m, other, otherKey, 0, MinFee*3, 1_700_000_003)

	// Fee order is [from@5x, from@5x, other@3x, from@1x]; the sender's two
	// top slots become nonce 0 and nonce 1, then the cut.
	got := m.Pending(2)
	if len(got) != 2 {
		t.Fatalf("Pending(2) returned %d", len(got))
	}
	if got[0].From != from || got[0].Nonce != 0 || got[1].From != from || got[1].Nonce != 1 {
		t.Fatalf("Pending(2) = %v (nonces %v), want the sender's nonces 0 then 1", got, noncesOf(got))
	}

	// The full list keeps the other sender in its fee slot and never emits a
	// nonce before a lower one from the same sender.
	all := m.Pending(10)
	want := []struct {
		from  qcrypto.Address
		nonce uint64
	}{{from, 0}, {from, 1}, {other, 0}, {from, 2}}
	if len(all) != len(want) {
		t.Fatalf("Pending(10) returned %d, want %d", len(all), len(want))
	}
	for i, w := range want {
		if all[i].From != w.from || all[i].Nonce != w.nonce {
			t.Fatalf("Pending(10)[%d] = %s nonce %d, want %s nonce %d", i, all[i].From, all[i].Nonce, w.from, w.nonce)
		}
	}
}

// Two pools holding the same transactions, filled in different orders, must
// hand the producer the same list: the order is total (fee, timestamp, hash),
// not whatever the map iteration happened to be.
func TestPendingIsDeterministic(t *testing.T) {
	sdb, err := state.NewStateDB(t.TempDir())
	if err != nil {
		t.Fatalf("statedb: %v", err)
	}
	defer sdb.Close()

	type sender struct {
		addr qcrypto.Address
		key  *qcrypto.PrivateKey
	}
	var senders []sender
	for i := 0; i < 4; i++ {
		a, k := fundedSender(t, sdb)
		senders = append(senders, sender{a, k})
	}
	var to qcrypto.Address
	for i := range to {
		to[i] = 0x55
	}
	// Same fee and timestamp for everyone: only the hash breaks ties.
	var txs []*types.Transaction
	for _, s := range senders {
		for n := uint64(0); n < 3; n++ {
			tx := types.NewTransfer(s.addr, to, big.NewInt(0), big.NewInt(MinFee), n, 1_700_000_000)
			if err := tx.Sign(s.key, true); err != nil {
				t.Fatalf("sign: %v", err)
			}
			txs = append(txs, tx)
		}
	}

	forward := New(100, sdb)
	for _, tx := range txs {
		if err := forward.Add(tx); err != nil {
			t.Fatalf("forward add: %v", err)
		}
	}
	backward := New(100, sdb)
	for i := len(txs) - 1; i >= 0; i-- {
		if err := backward.Add(txs[i]); err != nil {
			t.Fatalf("backward add: %v", err)
		}
	}

	first := forward.Pending(100)
	for run := 0; run < 20; run++ {
		for name, m := range map[string]*Mempool{"forward": forward, "backward": backward} {
			got := m.Pending(100)
			if len(got) != len(first) {
				t.Fatalf("%s run %d: %d txs, want %d", name, run, len(got), len(first))
			}
			for i := range got {
				if got[i].From != first[i].From || got[i].Nonce != first[i].Nonce {
					t.Fatalf("%s run %d: position %d = %s/%d, first call had %s/%d", name, run, i, got[i].From, got[i].Nonce, first[i].From, first[i].Nonce)
				}
			}
		}
	}
	// And every sender's nonces come out ascending.
	last := map[qcrypto.Address]int64{}
	for _, tx := range first {
		if prev, ok := last[tx.From]; ok && int64(tx.Nonce) <= prev {
			t.Fatalf("sender %s emitted nonce %d after %d", tx.From, tx.Nonce, prev)
		}
		last[tx.From] = int64(tx.Nonce)
	}
}

// A native-module call carries no value: the executor's module branch never
// moves tx.Amount, so admitting one would let the sender believe they paid
// coins that stayed put. Refused at the door.
func TestAddRejectsRouterCallWithAmount(t *testing.T) {
	m, sdb, from, priv := fundedMempool(t)
	defer sdb.Close()

	data := []byte(`{"module":"qrc20","action":"transfer","params":{}}`)
	withValue := types.NewCall(from, vm.ModuleRouterAddress, data, big.NewInt(1), big.NewInt(MinFee), 0, 1_700_000_000)
	if err := withValue.Sign(priv, true); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := m.Add(withValue); err == nil || !strings.Contains(err.Error(), "amount 0") {
		t.Fatalf("want an amount-0 rejection for a valued router call, got %v", err)
	}
	if m.Size() != 0 {
		t.Fatalf("size = %d after rejection, want 0", m.Size())
	}

	zero := types.NewCall(from, vm.ModuleRouterAddress, data, big.NewInt(0), big.NewInt(MinFee), 0, 1_700_000_000)
	if err := zero.Sign(priv, true); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := m.Add(zero); err != nil {
		t.Fatalf("a zero-value router call must be admitted: %v", err)
	}
}
