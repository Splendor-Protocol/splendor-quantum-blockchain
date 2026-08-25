package block

import (
	"fmt"
	"math/big"
	"testing"
	"time"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// TestMLDSAVerifyRate isolates the dominant per-tx cost: ML-DSA-65 signature
// verification. Run: go test ./core/block/ -run TestMLDSAVerifyRate -v
func TestMLDSAVerifyRate(t *testing.T) {
	pub, priv, _ := qcrypto.GenerateKeypair()
	msg := qcrypto.Hash32([]byte("throughput probe message"))
	sig, err := qcrypto.Sign(priv, msg[:])
	if err != nil {
		t.Fatal(err)
	}
	const n = 2000
	start := time.Now()
	for i := 0; i < n; i++ {
		if !qcrypto.Verify(pub, msg[:], sig) {
			t.Fatal("verify failed")
		}
	}
	d := time.Since(start)
	perOp := d / n
	t.Logf("ML-DSA-65 Verify: %d ops in %s => %.1f µs/op => %.0f verifies/s (1 verify per tx)",
		n, d, float64(perOp.Nanoseconds())/1000.0, float64(n)/d.Seconds())
}

// TestBlockThroughput measures end-to-end ProduceBlock throughput (verify +
// execute + Merkle root + atomic commit) for realistic blocks of N distinct
// senders each sending one tx — the shape that actually fills a block.
// Run: go test ./core/block/ -run TestBlockThroughput -v -timeout 600s
func TestBlockThroughput(t *testing.T) {
	if testing.Short() {
		t.Skip("throughput test skipped in -short")
	}
	for _, n := range []int{1000, 5000, 10000} {
		measureBlock(t, n)
	}
}

func measureBlock(t *testing.T, n int) {
	sdb, err := state.NewStateDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sdb.Close()
	ex, err := NewExecutor(sdb, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer ex.Close()

	// Validator.
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	vaddr := qcrypto.DeriveAddress(vpub)

	// N senders, funded in ONE commit (not timed).
	type acct struct {
		pub  *qcrypto.PublicKey
		priv *qcrypto.PrivateKey
		addr qcrypto.Address
	}
	accts := make([]*acct, n)
	for i := 0; i < n; i++ {
		p, s, _ := qcrypto.GenerateKeypair()
		a := &acct{pub: p, priv: s, addr: qcrypto.DeriveAddress(p)}
		accts[i] = a
		ga := types.NewAccount(a.addr)
		ga.AddBalance(big.NewInt(1_000_000))
		sdb.SetAccount(ga)
	}
	if _, err := sdb.Commit(); err != nil {
		t.Fatal(err)
	}

	// Pre-sign one tx per sender (ring transfer), nonce 0 (carries the pubkey).
	// Signing is setup, not timed.
	txs := make([]*types.Transaction, n)
	for i := 0; i < n; i++ {
		to := accts[(i+1)%n].addr
		tx := types.NewTransfer(accts[i].addr, to, big.NewInt(1), big.NewInt(1000), 0, 1700000000+int64(i))
		if err := tx.Sign(accts[i].priv, true); err != nil {
			t.Fatal(err)
		}
		txs[i] = tx
	}

	// Time ONLY block production over the full batch.
	start := time.Now()
	b, err := ex.ProduceBlock(txs, vaddr, vpub.Bytes(), vpriv, [32]byte{}, 1)
	if err != nil {
		t.Fatalf("produce %d: %v", n, err)
	}
	d := time.Since(start)

	included := len(b.Transactions)
	tps := float64(included) / d.Seconds()
	fmt.Printf("[throughput] block of %5d txs: included=%d produce=%s => %.0f tx/s (verify+execute+root+commit, single-thread)\n",
		n, included, d.Round(time.Millisecond), tps)
	if included != n {
		t.Logf("note: only %d/%d txs included (gas budget or validation)", included, n)
	}
}
