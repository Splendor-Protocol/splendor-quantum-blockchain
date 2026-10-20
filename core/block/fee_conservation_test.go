package block

import (
	"math/big"
	"testing"
	"time"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
)

// supplyOf sums the balances of every address the test cares about.
func supplyOf(t *testing.T, sdb *state.StateDB, addrs ...qcrypto.Address) *big.Int {
	t.Helper()
	total := new(big.Int)
	for _, a := range addrs {
		b, err := sdb.GetBalance(a)
		if err != nil {
			t.Fatalf("balance %s: %v", a, err)
		}
		total.Add(total, b)
	}
	return total
}

// A block containing a transaction that cannot be applied must be REJECTED.
//
// Pre-fix, ExecuteBlock reverted the failed tx to its snapshot (so the sender
// was never debited) but still added tx.Fee to totalFees and credited it to the
// validator. A malicious producer could therefore pack blocks full of invalid
// transactions — bad nonce, bad signature, insufficient balance — and mint the
// fee for each one. The state root matched, because every follower ran the same
// buggy accounting, so the block propagated cleanly.
func TestExecuteBlockRejectsUnappliableTx(t *testing.T) {
	pub, priv, _ := qcrypto.GenerateKeypair()
	from := qcrypto.DeriveAddress(pub)
	to := testAddr(0x55)
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	vaddr := qcrypto.DeriveAddress(vpub)

	const start = int64(1_000_000)

	// The attacker builds the block the way the buggy apply path scores it:
	// the invalid tx contributes nothing but its fee, credited to the validator.
	exA, sdbA := newTestExecutor(t)
	defer exA.Close()
	defer sdbA.Close()
	fund(t, sdbA, from, start)

	// Nonce 9 against an account at nonce 0 — can never be applied.
	bad := types.NewTransfer(from, to, big.NewInt(100), big.NewInt(testFee), 9, time.Now().Unix())
	if err := bad.Sign(priv, true); err != nil {
		t.Fatalf("sign: %v", err)
	}

	va, _ := sdbA.GetAccount(vaddr)
	va.AddBalance(big.NewInt(testFee))
	sdbA.SetAccount(va)
	forgedRoot, err := sdbA.PendingStateRoot()
	if err != nil {
		t.Fatalf("forged root: %v", err)
	}
	sdbA.DiscardPending()

	txRoot, err := types.ComputeTxMerkleRoot([]*types.Transaction{bad})
	if err != nil {
		t.Fatalf("tx merkle root: %v", err)
	}
	blk := &types.Block{
		Header: types.BlockHeader{
			Height: 1, Timestamp: time.Now().Unix(), ChainID: types.ChainID,
			AlgID: qcrypto.AlgMLDSA65, TxMerkleRoot: txRoot,
			StateRoot: forgedRoot, ValidatorAddr: vaddr, TxCount: 1,
		},
		Transactions: []*types.Transaction{bad},
	}
	h, _ := blk.ComputeHash()
	blk.Hash = h
	if err := blk.SignBlock(vpriv); err != nil {
		t.Fatalf("sign block: %v", err)
	}

	// A follower must refuse it.
	exB, sdbB := newTestExecutor(t)
	defer exB.Close()
	defer sdbB.Close()
	fund(t, sdbB, from, start)

	applyErr := exB.ExecuteBlock(blk)

	after := supplyOf(t, sdbB, from, to, vaddr)
	if after.Cmp(big.NewInt(start)) != 0 {
		t.Errorf("supply changed from %d to %s — validator fee minted from an unappliable tx",
			start, after)
	}
	if applyErr == nil {
		t.Fatal("ExecuteBlock accepted a block whose only tx cannot be applied")
	}
	if hgt, _ := exB.LatestHeight(); hgt != 0 {
		t.Fatalf("head advanced to %d on a rejected block", hgt)
	}
}

// A tx whose signature does not verify must likewise sink the whole block
// rather than paying its fee to the validator.
func TestExecuteBlockRejectsBadSignatureTx(t *testing.T) {
	pub, priv, _ := qcrypto.GenerateKeypair()
	from := qcrypto.DeriveAddress(pub)
	_, otherPriv, _ := qcrypto.GenerateKeypair()
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	vaddr := qcrypto.DeriveAddress(vpub)

	const start = int64(1_000_000)

	exA, sdbA := newTestExecutor(t)
	defer exA.Close()
	defer sdbA.Close()
	fund(t, sdbA, from, start)

	// Signed by the WRONG key but carrying `from`'s public key.
	bad := types.NewTransfer(from, testAddr(0x66), big.NewInt(100), big.NewInt(testFee), 0, time.Now().Unix())
	if err := bad.Sign(otherPriv, false); err != nil {
		t.Fatalf("sign: %v", err)
	}
	bad.PublicKey = pub.Bytes()
	_ = priv

	va, _ := sdbA.GetAccount(vaddr)
	va.AddBalance(big.NewInt(testFee))
	sdbA.SetAccount(va)
	forgedRoot, _ := sdbA.PendingStateRoot()
	sdbA.DiscardPending()

	txRoot, _ := types.ComputeTxMerkleRoot([]*types.Transaction{bad})
	blk := &types.Block{
		Header: types.BlockHeader{
			Height: 1, Timestamp: time.Now().Unix(), ChainID: types.ChainID,
			AlgID: qcrypto.AlgMLDSA65, TxMerkleRoot: txRoot,
			StateRoot: forgedRoot, ValidatorAddr: vaddr, TxCount: 1,
		},
		Transactions: []*types.Transaction{bad},
	}
	h, _ := blk.ComputeHash()
	blk.Hash = h
	_ = blk.SignBlock(vpriv)

	exB, sdbB := newTestExecutor(t)
	defer exB.Close()
	defer sdbB.Close()
	fund(t, sdbB, from, start)

	if err := exB.ExecuteBlock(blk); err == nil {
		t.Error("ExecuteBlock accepted a block containing a badly-signed tx")
	}
	if vb, _ := sdbB.GetBalance(vaddr); vb.Sign() != 0 {
		t.Fatalf("validator credited %s for a badly-signed tx", vb)
	}
}

// A native self-transfer routed through a real block must not mint. This is the
// end-to-end form of the StateDB.Transfer aliasing bug: send SPLD to yourself
// and your balance grows by the amount while only the fee should leave.
func TestSelfTransferBlockDoesNotMint(t *testing.T) {
	pub, priv, _ := qcrypto.GenerateKeypair()
	from := qcrypto.DeriveAddress(pub)
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	vaddr := qcrypto.DeriveAddress(vpub)

	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()

	const start = int64(1_000_000)
	const amount = int64(400_000)
	fund(t, sdb, from, start)

	tx := types.NewTransfer(from, from, big.NewInt(amount), big.NewInt(testFee), 0, time.Now().Unix())
	if err := tx.Sign(priv, true); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, err := ex.ProduceBlock([]*types.Transaction{tx}, vaddr, vpub.Bytes(), vpriv, [32]byte{}, 1); err != nil {
		t.Fatalf("produce: %v", err)
	}

	fromBal, _ := sdb.GetBalance(from)
	if fromBal.Cmp(big.NewInt(start-testFee)) != 0 {
		t.Errorf("self-transfer balance = %s, want %d (amount should not move)", fromBal, start-testFee)
	}
	if total := supplyOf(t, sdb, from, vaddr); total.Cmp(big.NewInt(start)) != 0 {
		t.Fatalf("supply = %s, want %d — self-transfer minted SPLD", total, start)
	}
}

// A module call that fails under module_revert_v1 keeps its fee: the sender
// is debited, the validator credited, and nothing else moves — supply is
// conserved exactly as for a reverted bytecode call.
func TestFailedModuleCallConservesSupply(t *testing.T) {
	pub, priv, _ := qcrypto.GenerateKeypair()
	from := qcrypto.DeriveAddress(pub)
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	vaddr := qcrypto.DeriveAddress(vpub)

	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()
	ex.WithForks(revertForks())

	const start = int64(1_000_000)
	fund(t, sdb, from, start)

	tx := softFailingCall(t, from, priv, 0)
	b, err := ex.ProduceBlock([]*types.Transaction{tx}, vaddr, vpub.Bytes(), vpriv, [32]byte{}, 1)
	if err != nil {
		t.Fatalf("produce: %v", err)
	}
	if len(b.Transactions) != 1 {
		t.Fatal("the failed module call must be included under module_revert_v1")
	}

	fromBal, _ := sdb.GetBalance(from)
	if fromBal.Cmp(big.NewInt(start-testFee)) != 0 {
		t.Errorf("sender balance = %s, want %d (only the fee leaves)", fromBal, start-testFee)
	}
	vBal, _ := sdb.GetBalance(vaddr)
	if vBal.Cmp(big.NewInt(testFee)) != 0 {
		t.Errorf("validator balance = %s, want %d", vBal, testFee)
	}
	// The addresses the failing action named must be untouched.
	if total := supplyOf(t, sdb, from, vaddr, testAddr(0x44), testAddr(0x55), vm.ModuleRouterAddress); total.Cmp(big.NewInt(start)) != 0 {
		t.Fatalf("supply = %s, want %d — a failed module call minted or burned SPLD", total, start)
	}
}
