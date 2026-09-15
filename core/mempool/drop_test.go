package mempool

import (
	"math/big"
	"testing"
	"time"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// Drop removes exactly the transactions it is given, resident or not, and
// releases the sender's per-address slot with them.
func TestDropRemovesOnlyTheGivenTransactions(t *testing.T) {
	sdb, err := state.NewStateDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sdb.Close()
	pub, priv, _ := qcrypto.GenerateKeypair()
	from := qcrypto.DeriveAddress(pub)
	acc := types.NewAccount(from)
	acc.AddBalance(big.NewInt(1_000_000_000))
	sdb.SetAccount(acc)
	if _, err := sdb.Commit(); err != nil {
		t.Fatal(err)
	}

	pool := New(100, sdb)
	var txs []*types.Transaction
	for nonce := uint64(0); nonce < 3; nonce++ {
		tx := types.NewTransfer(from, qcrypto.Address{1}, big.NewInt(1), big.NewInt(MinFee), nonce, time.Now().Unix())
		if err := tx.Sign(priv, true); err != nil {
			t.Fatal(err)
		}
		if err := pool.Add(tx); err != nil {
			t.Fatalf("add nonce %d: %v", nonce, err)
		}
		txs = append(txs, tx)
	}

	stranger := types.NewTransfer(from, qcrypto.Address{2}, big.NewInt(1), big.NewInt(MinFee), 9, time.Now().Unix())
	if n := pool.Drop([]*types.Transaction{txs[1], stranger}); n != 1 {
		t.Fatalf("Drop removed %d, want 1 (the stranger was never resident)", n)
	}
	if pool.Size() != 2 {
		t.Fatalf("pool holds %d after Drop, want 2", pool.Size())
	}
	for _, tx := range pool.Pending(10) {
		if tx.Nonce == 1 {
			t.Fatal("the dropped transaction is still pending")
		}
	}
	if got := len(pool.byAddr[from]); got != 2 {
		t.Fatalf("sender occupies %d slots after Drop, want 2", got)
	}
}
