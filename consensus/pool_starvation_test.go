package consensus

import (
	"math/big"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/mempool"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

func fundedTestSender(t *testing.T, sdb *state.StateDB) (*qcrypto.PrivateKey, qcrypto.Address) {
	t.Helper()
	priv, _, addr := testKey(t)
	acc := types.NewAccount(addr)
	acc.AddBalance(big.NewInt(1_000_000_000_000))
	sdb.SetAccount(acc)
	if _, err := sdb.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return priv, addr
}

func poolTransfer(t *testing.T, pool *mempool.Mempool, priv *qcrypto.PrivateKey, from qcrypto.Address, nonce uint64, fee int64) *types.Transaction {
	t.Helper()
	var to qcrypto.Address
	to[0] = 0x55
	tx := types.NewTransfer(from, to, big.NewInt(1), big.NewInt(fee), nonce, 1_700_000_000)
	if err := tx.Sign(priv, true); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := pool.Add(tx); err != nil {
		t.Fatalf("add nonce %d: %v", nonce, err)
	}
	return tx
}

// The path a proposer takes each block: a batch from the pool, handed to the
// builder. With more gapped transactions in the pool than the batch holds,
// all at a higher fee, the batch used to be nothing but gaps: the builder
// skipped every one, the pool kept every one, and the block came out empty on
// every height while an ordinary transfer at the floor fee waited behind them.
func TestProposalPacksExecutableTxBehindGapBacklog(t *testing.T) {
	ex, sdb, _ := newTestChain(t)
	pool := mempool.New(1_000, sdb)
	vpriv, vpk, vaddr := testKey(t)

	const batch = 100
	backlog := 0
	for s := 0; s < 2; s++ {
		priv, addr := fundedTestSender(t, sdb)
		// Nonce 0 is never sent, so none of these can execute.
		for n := uint64(1); n <= mempool.MaxPerAddr; n++ {
			poolTransfer(t, pool, priv, addr, n, mempool.MinFee*2)
			backlog++
		}
	}
	if backlog <= batch {
		t.Fatalf("backlog %d must exceed the batch %d", backlog, batch)
	}
	honestPriv, honest := fundedTestSender(t, sdb)
	want := poolTransfer(t, pool, honestPriv, honest, 0, mempool.MinFee)

	b, skipped, err := ex.BuildProposalReport(pool.Pending(batch), vaddr, vpk, vpriv, [32]byte{}, 1)
	if err != nil {
		t.Fatalf("BuildProposalReport: %v", err)
	}
	if len(b.Transactions) != 1 || b.Transactions[0] != want {
		t.Fatalf("block carries %d txs, want the one executable transfer", len(b.Transactions))
	}
	if len(skipped) != 0 {
		t.Fatalf("builder skipped %d candidates, want none: the pool must not offer what cannot execute", len(skipped))
	}
	if pool.Size() != backlog+1 {
		t.Fatalf("pool size %d, want %d: gapped transactions stay until their turn", pool.Size(), backlog+1)
	}
}
