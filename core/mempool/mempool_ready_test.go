package mempool

import (
	"errors"
	"math/big"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// Pending hands the producer a fixed number of slots. A transaction whose
// nonce is ahead of its sender's next nonce cannot be packed, and the pool
// keeps it, so it must not take one of those slots: enough of them at a
// higher fee would hold every slot on every block and no cheaper transaction
// would ever be offered. These tests pin the selection to what is due: each
// sender's unbroken run of nonces from its committed nonce.

func setCommittedNonce(t *testing.T, sdb *state.StateDB, addr qcrypto.Address, nonce uint64) {
	t.Helper()
	acc, err := sdb.GetAccount(addr)
	if err != nil {
		t.Fatalf("account: %v", err)
	}
	acc.Nonce = nonce
	sdb.SetAccount(acc)
	if _, err := sdb.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func closeState(t *testing.T, sdb *state.StateDB) {
	t.Helper()
	if err := sdb.Close(); err != nil {
		t.Fatalf("sdb.Close: %v", err)
	}
}

// One slot, two candidates: a higher-fee transaction behind a nonce gap and a
// cheaper one that can execute. The slot belongs to the one that can execute.
func TestPendingSkipsNonceGapForExecutableTx(t *testing.T) {
	m, sdb, gapped, gappedKey := fundedMempool(t)
	defer closeState(t, sdb)
	ready, readyKey := fundedSender(t, sdb)

	addTransfer(t, m, gapped, gappedKey, 5, MinFee*4, 1_700_000_000)
	want := addTransfer(t, m, ready, readyKey, 0, MinFee, 1_700_000_001)

	got := m.Pending(1)
	if len(got) != 1 || got[0] != want {
		t.Fatalf("Pending(1) = %v (nonces %v), want the executable transfer", got, noncesOf(got))
	}
	// The gapped transaction is not offered and not removed either.
	if m.Size() != 2 {
		t.Fatalf("size=%d, want 2: a nonce gap must stay in the pool", m.Size())
	}
}

// The reported stall: a backlog of gapped transactions at a higher fee, larger
// than the producer's batch, in front of one ordinary transfer at the floor
// fee. Before the fix every batch was filled by the backlog, the executor
// skipped all of it for the gap, and the transfer was never offered.
func TestPendingGapBacklogDoesNotStarveLowerFee(t *testing.T) {
	m, sdb, honest, honestKey := fundedMempool(t)
	defer closeState(t, sdb)

	const (
		gapSenders = 3
		batch      = 100
	)
	for s := 0; s < gapSenders; s++ {
		addr, key := fundedSender(t, sdb)
		// Nonce 0 never arrives, so nonces 1..MaxPerAddr can never execute.
		for n := uint64(1); n <= MaxPerAddr; n++ {
			addTransfer(t, m, addr, key, n, MinFee*2, 1_700_000_000)
		}
	}
	if backlog := gapSenders * MaxPerAddr; backlog <= batch {
		t.Fatalf("backlog %d must exceed the batch %d for this test to mean anything", backlog, batch)
	}
	want := addTransfer(t, m, honest, honestKey, 0, MinFee, 1_700_000_500)

	for run := 0; run < 5; run++ {
		got := m.Pending(batch)
		if len(got) != 1 || got[0] != want {
			t.Fatalf("run %d: Pending(%d) returned %d txs (nonces %v), want only the executable transfer",
				run, batch, len(got), noncesOf(got))
		}
	}
	if m.Size() != gapSenders*MaxPerAddr+1 {
		t.Fatalf("size=%d: Pending must not remove anything", m.Size())
	}
}

// Only the unbroken run from the sender's next nonce is offered; what sits
// past a hole waits for the hole to be filled.
func TestPendingOffersOnlyTheContiguousRun(t *testing.T) {
	m, sdb, from, priv := fundedMempool(t)
	defer closeState(t, sdb)

	for _, n := range []uint64{4, 0, 3, 1} { // nonce 2 is missing
		addTransfer(t, m, from, priv, n, MinFee, 1_700_000_000)
	}
	got := noncesOf(m.Pending(10))
	if len(got) != 2 || got[0] != 0 || got[1] != 1 {
		t.Fatalf("Pending = %v, want [0 1]", got)
	}

	// Filling the hole releases the rest, in order.
	addTransfer(t, m, from, priv, 2, MinFee, 1_700_000_001)
	got = noncesOf(m.Pending(10))
	if len(got) != 5 {
		t.Fatalf("Pending = %v, want nonces 0..4", got)
	}
	for i, n := range got {
		if n != uint64(i) {
			t.Fatalf("Pending = %v, want nonces 0..4 in order", got)
		}
	}
}

// A sender with nothing at its next nonce is not offered at all, and becomes
// eligible as soon as that nonce is admitted.
func TestPendingGapClearsWhenMissingNonceArrives(t *testing.T) {
	m, sdb, from, priv := fundedMempool(t)
	defer closeState(t, sdb)

	addTransfer(t, m, from, priv, 1, MinFee, 1_700_000_000)
	addTransfer(t, m, from, priv, 2, MinFee, 1_700_000_000)
	if got := m.Pending(10); len(got) != 0 {
		t.Fatalf("Pending = nonces %v, want nothing while nonce 0 is missing", noncesOf(got))
	}

	addTransfer(t, m, from, priv, 0, MinFee, 1_700_000_001)
	got := noncesOf(m.Pending(10))
	if len(got) != 3 || got[0] != 0 || got[1] != 1 || got[2] != 2 {
		t.Fatalf("Pending = %v, want [0 1 2]", got)
	}
}

// The run starts at the nonce the state holds, not at the lowest nonce in the
// pool: an entry the chain has already passed is left for PruneStale and the
// sender's live transactions are still offered.
func TestPendingStartsAtTheCommittedNonce(t *testing.T) {
	m, sdb, from, priv := fundedMempool(t)
	defer closeState(t, sdb)

	for n := uint64(0); n < 4; n++ {
		addTransfer(t, m, from, priv, n, MinFee, 1_700_000_000)
	}
	// Nonces 0 and 1 land in a block; the pool has not been pruned yet.
	setCommittedNonce(t, sdb, from, 2)

	got := noncesOf(m.Pending(10))
	if len(got) != 2 || got[0] != 2 || got[1] != 3 {
		t.Fatalf("Pending = %v, want [2 3]", got)
	}
	if m.Size() != 4 {
		t.Fatalf("size=%d, want 4: Pending must not prune", m.Size())
	}
}

// A sender whose committed nonce is ahead of the pool's view by a hole: the
// chain moved to nonce 3 while the pool holds 1 and 4. Neither can execute.
func TestPendingStaleAndGappedTogether(t *testing.T) {
	m, sdb, from, priv := fundedMempool(t)
	defer closeState(t, sdb)
	other, otherKey := fundedSender(t, sdb)

	addTransfer(t, m, from, priv, 1, MinFee*3, 1_700_000_000)
	addTransfer(t, m, from, priv, 4, MinFee*3, 1_700_000_000)
	setCommittedNonce(t, sdb, from, 3)
	want := addTransfer(t, m, other, otherKey, 0, MinFee, 1_700_000_001)

	got := m.Pending(10)
	if len(got) != 1 || got[0] != want {
		t.Fatalf("Pending = nonces %v, want only the other sender's transfer", noncesOf(got))
	}
}

// Fee priority and the per-sender re-slot still hold among what can execute,
// including across the cut: the gapped sender's higher fee buys it nothing,
// and the two live senders are ordered as before.
func TestPendingFeeOrderAmongExecutable(t *testing.T) {
	m, sdb, a, aKey := fundedMempool(t)
	defer closeState(t, sdb)
	b, bKey := fundedSender(t, sdb)
	gapped, gappedKey := fundedSender(t, sdb)

	addTransfer(t, m, gapped, gappedKey, 7, MinFee*9, 1_700_000_000)
	addTransfer(t, m, a, aKey, 0, MinFee, 1_700_000_000)   // cheapest, lowest nonce
	addTransfer(t, m, a, aKey, 1, MinFee*5, 1_700_000_001) // expensive
	addTransfer(t, m, b, bKey, 0, MinFee*3, 1_700_000_002)

	want := []struct {
		from  qcrypto.Address
		nonce uint64
	}{{a, 0}, {b, 0}, {a, 1}}
	check := func(got []*types.Transaction, n int) {
		t.Helper()
		if len(got) != n {
			t.Fatalf("Pending returned %d (nonces %v), want %d", len(got), noncesOf(got), n)
		}
		for i := 0; i < n; i++ {
			if got[i].From != want[i].from || got[i].Nonce != want[i].nonce {
				t.Fatalf("Pending[%d] = %s nonce %d, want %s nonce %d",
					i, got[i].From, got[i].Nonce, want[i].from, want[i].nonce)
			}
		}
	}
	check(m.Pending(10), 3)
	check(m.Pending(2), 2)
	check(m.Pending(1), 1)
}

// Zero or a negative count asks for nothing and must not panic.
func TestPendingNonPositiveCount(t *testing.T) {
	m, sdb, from, priv := fundedMempool(t)
	defer closeState(t, sdb)
	addTransfer(t, m, from, priv, 0, MinFee, 1_700_000_000)

	for _, n := range []int{0, -1} {
		if got := m.Pending(n); len(got) != 0 {
			t.Fatalf("Pending(%d) returned %d txs, want none", n, len(got))
		}
	}
}

// Pending runs outside the executor's lock, so the state's pending overlay can
// hold the half-applied nonce of a proposal being built or validated at that
// moment. Those writes are discarded before the producer assembles, and the
// executor then wants the committed nonce: selecting from the overlay would
// hand it nonce 1 without nonce 0.
func TestPendingIgnoresSpeculativeNonce(t *testing.T) {
	m, sdb, from, priv := fundedMempool(t)
	defer closeState(t, sdb)
	addTransfer(t, m, from, priv, 0, MinFee, 1_700_000_000)
	addTransfer(t, m, from, priv, 1, MinFee, 1_700_000_000)

	// An execution in flight has applied nonce 0 to the overlay, uncommitted.
	acc, err := sdb.GetAccount(from)
	if err != nil {
		t.Fatalf("account: %v", err)
	}
	acc.Nonce = 1
	sdb.SetAccount(acc)

	got := noncesOf(m.Pending(10))
	sdb.DiscardPending()
	if len(got) != 2 || got[0] != 0 || got[1] != 1 {
		t.Fatalf("Pending = %v, want [0 1] from the committed nonce", got)
	}
}

// A sender whose nonce cannot be read is offered whole, as it was before the
// selection existed, and the executor judges each entry. Guessing a run from
// the lowest pending nonce instead could hide the entry that is really due.
func TestPendingNonceReadFailureOffersWholeSender(t *testing.T) {
	m, sdb, from, priv := fundedMempool(t)
	defer closeState(t, sdb)
	addTransfer(t, m, from, priv, 0, MinFee, 1_700_000_000)
	addTransfer(t, m, from, priv, 3, MinFee, 1_700_000_000)
	setCommittedNonce(t, sdb, from, 3) // nonce 0 is now stale, nonce 3 is due

	m.nonceOf = func(qcrypto.Address) (uint64, error) { return 0, errors.New("injected state read") }
	got := noncesOf(m.Pending(10))
	if len(got) != 2 || got[0] != 0 || got[1] != 3 {
		t.Fatalf("Pending with an unreadable nonce = %v, want the whole sender [0 3]", got)
	}

	m.nonceOf = nil
	got = noncesOf(m.Pending(10))
	if len(got) != 1 || got[0] != 3 {
		t.Fatalf("Pending = %v, want [3]", got)
	}
}

// The nonce reads are bounded by the senders met before the batch fills, not
// by the pool: with every sender due, a batch of 5 reads 5 senders out of 40.
// A sender is read once however many entries it has.
func TestPendingReadsOnlyTheSendersItMeets(t *testing.T) {
	m, sdb, from, priv := fundedMempool(t)
	defer closeState(t, sdb)

	const senders = 40
	addTransfer(t, m, from, priv, 0, MinFee, 1_700_000_000)
	for i := 1; i < senders; i++ {
		addr, key := fundedSender(t, sdb)
		addTransfer(t, m, addr, key, 0, MinFee, 1_700_000_000)
	}
	reads := 0
	m.nonceOf = func(addr qcrypto.Address) (uint64, error) {
		reads++
		return sdb.CommittedNonce(addr)
	}

	if got := m.Pending(5); len(got) != 5 {
		t.Fatalf("Pending(5) returned %d", len(got))
	}
	if reads != 5 {
		t.Fatalf("Pending(5) read %d sender nonces, want 5", reads)
	}

	reads = 0
	for n := uint64(1); n < 8; n++ {
		addTransfer(t, m, from, priv, n, MinFee*2, 1_700_000_000)
	}
	if got := m.Pending(7); len(got) != 7 {
		t.Fatalf("Pending(7) returned %d", len(got))
	}
	if reads != 1 {
		t.Fatalf("one sender filling the batch was read %d times, want 1", reads)
	}
}

// BenchmarkPendingGapBacklog measures a selection that has to walk past a
// large backlog of gapped senders, the case that costs one nonce read per
// sender. Entries are placed in the pool directly: signing is not what is
// being measured.
func BenchmarkPendingGapBacklog(b *testing.B) {
	sdb, err := state.NewStateDB(b.TempDir())
	if err != nil {
		b.Fatalf("statedb: %v", err)
	}
	defer func() {
		if err := sdb.Close(); err != nil {
			b.Fatalf("sdb.Close: %v", err)
		}
	}()
	m := New(DefaultMaxSize, sdb)

	const senders = 20_000
	put := func(i int, nonce uint64, fee int64) {
		var addr qcrypto.Address
		addr[0], addr[1], addr[2], addr[3] = byte(i>>24), byte(i>>16), byte(i>>8), byte(i)
		acc := types.NewAccount(addr)
		acc.AddBalance(big.NewInt(1_000_000))
		acc.PublicKey = make([]byte, qcrypto.PublicKeySize)
		sdb.SetAccount(acc)
		tx := types.NewTransfer(addr, addr, big.NewInt(0), big.NewInt(fee), nonce, 1_700_000_000)
		e := &txEntry{tx: tx, size: 1}
		e.hash[0], e.hash[1], e.hash[2], e.hash[3] = byte(i>>24), byte(i>>16), byte(i>>8), byte(i)
		m.txs[e.hash] = e
		m.byAddr[addr] = append(m.byAddr[addr], e)
	}
	for i := 0; i < senders; i++ {
		put(i, 1, MinFee*2) // nonce 0 missing: gapped, and bidding more
	}
	put(senders, 0, MinFee) // the one transaction that is due
	if _, err := sdb.Commit(); err != nil {
		b.Fatalf("commit: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if got := m.Pending(363); len(got) != 1 {
			b.Fatalf("Pending returned %d, want 1", len(got))
		}
	}
}
