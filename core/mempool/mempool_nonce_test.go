package mempool

import (
	"errors"
	"math/big"
	"strings"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// A nonce below the sender's committed nonce can never be included: the executor
// requires tx.Nonce == account.Nonce and an account nonce never moves backwards.
// Admitting it anyway spends an ML-DSA verification and a pool slot on a tx that
// only PruneStale will ever remove, while telling the sender it was accepted.
func TestAddRejectsNonceBelowCommitted(t *testing.T) {
	m, sdb, from, priv := fundedMempool(t)
	acc, _ := sdb.GetAccount(from)
	acc.Nonce = 207
	sdb.SetAccount(acc)
	if _, err := sdb.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	var to qcrypto.Address
	to[0] = 0x55

	stale := types.NewTransfer(from, to, big.NewInt(0), big.NewInt(int64(MinFee)), 0, 1_700_000_000)
	_ = stale.Sign(priv, true)
	err := m.Add(stale)
	if !errors.Is(err, ErrNonceTooLow) {
		t.Fatalf("stale nonce: want ErrNonceTooLow, got %v", err)
	}
	if !strings.Contains(err.Error(), "nonce too low") {
		t.Fatalf("error should say nonce too low: %v", err)
	}
	if m.Size() != 0 {
		t.Fatalf("stale tx took a pool slot: size=%d", m.Size())
	}

	// The committed nonce itself and a future nonce (gap) are still admitted:
	// only the past is refused.
	current := types.NewTransfer(from, to, big.NewInt(0), big.NewInt(int64(MinFee)), 207, 1_700_000_001)
	_ = current.Sign(priv, true)
	if err := m.Add(current); err != nil {
		t.Fatalf("current nonce rejected: %v", err)
	}
	future := types.NewTransfer(from, to, big.NewInt(0), big.NewInt(int64(MinFee)), 209, 1_700_000_002)
	_ = future.Sign(priv, true)
	if err := m.Add(future); err != nil {
		t.Fatalf("future nonce rejected: %v", err)
	}
}

// Stress tests for the mempool ordering gap described in the tester report:
// a single wallet submitting a large burst of transactions must have its
// nonces handed to the block producer in strict order, so the executor never
// skips them for a nonce gap and the per-address backlog drains at full speed.

// TestPendingStressBurstSameFeeTimestamp simulates the reported stress test:
// one sender submits many transactions in a tight burst (same fee, same
// one-second timestamp). The old sort fell back to tx-hash tiebreak, which
// scrambled nonces and caused the executor to skip nearly everything. Pending
// must re-slot so every sender's nonces come out strictly ascending.
func TestPendingStressBurstSameFeeTimestamp(t *testing.T) {
	m, sdb, from, priv := fundedMempool(t)
	defer func() {
		if err := sdb.Close(); err != nil {
			t.Fatalf("sdb.Close: %v", err)
		}
	}()

	var to qcrypto.Address
	for i := range to {
		to[i] = 0x55
	}

	const count = MaxPerAddr
	for n := uint64(0); n < count; n++ {
		addTransfer(t, m, from, priv, n, MinFee, 1_700_000_000)
	}

	if m.Size() != count {
		t.Fatalf("size=%d, want %d", m.Size(), count)
	}

	got := m.Pending(count)
	if len(got) != count {
		t.Fatalf("Pending(%d)=%d, want %d", count, len(got), count)
	}
	for i, tx := range got {
		if tx.From != from {
			t.Fatalf("position %d: from=%s, want %s", i, tx.From, from)
		}
		if tx.Nonce != uint64(i) {
			t.Fatalf("position %d: nonce=%d, want %d", i, tx.Nonce, i)
		}
	}

	for run := 0; run < 10; run++ {
		again := m.Pending(count)
		if len(again) != len(got) {
			t.Fatalf("run %d: %d txs, want %d", run, len(again), len(got))
		}
		for i := range again {
			if again[i].Nonce != got[i].Nonce || again[i].From != got[i].From {
				t.Fatalf("run %d: position %d diverged", run, i)
			}
		}
	}
}

// TestPendingStressMaxPerAddrDrain verifies the full MaxPerAddr (64) backlog
// from a single sender drains in strict nonce order, so the per-address cap
// does not turn into a stall: once block N includes nonces 0..k, block N+1
// can include k+1..., and the pool never fills with unusable high nonces.
func TestPendingStressMaxPerAddrDrain(t *testing.T) {
	m, sdb, from, priv := fundedMempool(t)
	defer func() {
		if err := sdb.Close(); err != nil {
			t.Fatalf("sdb.Close: %v", err)
		}
	}()

	var to qcrypto.Address
	for i := range to {
		to[i] = 0x55
	}

	for n := uint64(0); n < MaxPerAddr; n++ {
		addTransfer(t, m, from, priv, n, MinFee, int64(1_700_000_000+n))
	}
	if m.Size() != MaxPerAddr {
		t.Fatalf("size=%d, want %d", m.Size(), MaxPerAddr)
	}

	remaining := MaxPerAddr
	block := 0
	for remaining > 0 {
		want := Min(10, remaining)
		got := m.Pending(want)
		if len(got) != want {
			t.Fatalf("block %d: Pending(%d)=%d, want %d", block, want, len(got), want)
		}
		base := uint64(MaxPerAddr - remaining)
		for i, tx := range got {
			wantNonce := base + uint64(i)
			if tx.Nonce != wantNonce {
				t.Fatalf("block %d position %d: nonce=%d, want %d", block, i, tx.Nonce, wantNonce)
			}
		}
		// Simulate RemoveBlock: included txs leave the pool so the next
		// Pending() call advances to the following nonces.
		for _, tx := range got {
			hash, _ := tx.FullHash()
			m.Remove(hash)
		}
		remaining -= want
		block++
	}

	if m.Size() != 0 {
		t.Fatalf("size=%d after full drain, want 0", m.Size())
	}
}

// TestPendingStressMultiSenderFeePriority verifies that when multiple senders
// each have a burst of transactions, fee priority across senders is preserved
// while nonce order within each sender is maintained.
func TestPendingStressMultiSenderFeePriority(t *testing.T) {
	m, sdb, from, priv := fundedMempool(t)
	defer func() {
		if err := sdb.Close(); err != nil {
			t.Fatalf("sdb.Close: %v", err)
		}
	}()

	other, otherKey := fundedSender(t, sdb)

	var to qcrypto.Address
	for i := range to {
		to[i] = 0x55
	}

	for n := uint64(0); n < 3; n++ {
		addTransfer(t, m, from, priv, n, MinFee, 1_700_000_000)
	}
	for n := uint64(0); n < 3; n++ {
		addTransfer(t, m, other, otherKey, n, MinFee*10, 1_700_000_000)
	}

	got := m.Pending(6)
	if len(got) != 6 {
		t.Fatalf("Pending(6)=%d, want 6", len(got))
	}

	bSeen := 0
	aSeen := 0
	for _, tx := range got {
		switch {
		case tx.From == other:
			if tx.Nonce != uint64(bSeen) {
				t.Fatalf("B nonce=%d at position %d, want %d", tx.Nonce, bSeen, bSeen)
			}
			bSeen++
		case tx.From == from:
			if tx.Nonce != uint64(aSeen) {
				t.Fatalf("A nonce=%d at position %d, want %d", tx.Nonce, aSeen, aSeen)
			}
			aSeen++
		default:
			t.Fatalf("unexpected sender %s", tx.From)
		}
	}
	if bSeen != 3 || aSeen != 3 {
		t.Fatalf("B=%d A=%d, want 3 each", bSeen, aSeen)
	}
	if got[0].From != other {
		t.Fatalf("first tx from %s, want %s (higher fee)", got[0].From, other)
	}
}

// TestPendingStressOutOfOrderAdd verifies that transactions added in random
// nonce order still come out of Pending() in strict ascending nonce order.
func TestPendingStressOutOfOrderAdd(t *testing.T) {
	m, sdb, from, priv := fundedMempool(t)
	defer func() {
		if err := sdb.Close(); err != nil {
			t.Fatalf("sdb.Close: %v", err)
		}
	}()

	var to qcrypto.Address
	for i := range to {
		to[i] = 0x55
	}

	order := []uint64{50, 0, 25, 10, 99, 5, 75, 1, 60, 30}
	for _, n := range order {
		addTransfer(t, m, from, priv, n, MinFee, 1_700_000_000)
	}

	got := m.Pending(len(order))
	if len(got) != len(order) {
		t.Fatalf("Pending=%d, want %d", len(got), len(order))
	}
	for i := 1; i < len(got); i++ {
		if got[i].Nonce <= got[i-1].Nonce {
			t.Fatalf("not ascending: position %d nonce=%d after %d", i, got[i].Nonce, got[i-1].Nonce)
		}
	}

	want := []uint64{0, 1, 5, 10, 25, 30, 50, 60, 75, 99}
	for i, tx := range got {
		if tx.Nonce != want[i] {
			t.Fatalf("position %d: nonce=%d, want %d", i, tx.Nonce, want[i])
		}
	}
}

// TestPendingExecutorGapFree simulates the executor's nonce tracking against
// Pending() output to confirm no nonce gaps are ever produced. The executor
// (core/block/executor.go assembleBlock) reads each sender's next expected
// nonce once from state, then advances it as each tx is applied. Pending()
// must supply each sender's txs in strict nonce order so every candidate
// matches tx.Nonce == nextExpected.
func TestPendingExecutorGapFree(t *testing.T) {
	m, sdb, from, priv := fundedMempool(t)
	defer func() {
		if err := sdb.Close(); err != nil {
			t.Fatalf("sdb.Close: %v", err)
		}
	}()

	other, otherKey := fundedSender(t, sdb)

	var to qcrypto.Address
	for i := range to {
		to[i] = 0x55
	}

	for n := uint64(0); n < 5; n++ {
		addTransfer(t, m, from, priv, n, MinFee, 1_700_000_000)
	}
	for n := uint64(0); n < 3; n++ {
		addTransfer(t, m, other, otherKey, n, MinFee, 1_700_000_000)
	}

	got := m.Pending(10)

	nextNonce := make(map[qcrypto.Address]uint64)
	for _, tx := range got {
		next, known := nextNonce[tx.From]
		if !known {
			acc, err := sdb.GetAccount(tx.From)
			if err != nil {
				t.Fatalf("state read for %s: %v", tx.From, err)
			}
			next = acc.Nonce
			nextNonce[tx.From] = next
		}
		if tx.Nonce != next {
			t.Fatalf("nonce gap: sender %s tx nonce=%d, executor expects %d",
				tx.From, tx.Nonce, next)
		}
		nextNonce[tx.From] = next + 1
	}

	aNonces := []uint64{}
	bNonces := []uint64{}
	for _, tx := range got {
		if tx.From == from {
			aNonces = append(aNonces, tx.Nonce)
		} else {
			bNonces = append(bNonces, tx.Nonce)
		}
	}
	if len(aNonces) != 5 {
		t.Fatalf("sender A: %d txs, want 5", len(aNonces))
	}
	if len(bNonces) != 3 {
		t.Fatalf("sender B: %d txs, want 3", len(bNonces))
	}
}

// Min returns the smaller of two ints.
func Min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
