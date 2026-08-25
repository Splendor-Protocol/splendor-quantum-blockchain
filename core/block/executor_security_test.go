package block

import (
	"math/big"
	"strings"
	"testing"
	"time"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
)

// A tx that FAILS after mutating state (fee deducted + nonce bumped, then the op
// errors) must leave NO residue: the producer excludes it, and a follower seeded
// identically must still ACCEPT the produced block (roots match). Pre-fix, the
// skipped tx's fee/nonce mutation stayed in the producer's state → the root
// reflected a tx absent from Transactions → every follower rejected the block.
func TestExecuteBlockFailedTxLeavesNoResidue(t *testing.T) {
	pub, priv, _ := qcrypto.GenerateKeypair()
	from := qcrypto.DeriveAddress(pub)
	to := testAddr(0x55)
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	vaddr := qcrypto.DeriveAddress(vpub)

	// Producer: a GOOD transfer (nonce 0) + a BAD module-router call (nonce 1)
	// that errors inside DispatchModuleCall after fee/nonce were mutated.
	exA, sdbA := newTestExecutor(t)
	defer exA.Close()
	defer sdbA.Close()
	fund(t, sdbA, from, 1_000_000)
	good := signedTransfer(t, from, priv, to, 100, 0)
	bad := types.NewCall(from, vm.ModuleRouterAddress, []byte("not-a-valid-module-envelope"),
		big.NewInt(0), big.NewInt(testFee), 1, time.Now().Unix())
	if err := bad.Sign(priv, true); err != nil {
		t.Fatalf("sign bad call: %v", err)
	}
	b1, err := exA.ProduceBlock([]*types.Transaction{good, bad}, vaddr, vpub.Bytes(), vpriv, [32]byte{}, 1)
	if err != nil {
		t.Fatalf("produce: %v", err)
	}
	if len(b1.Transactions) != 1 {
		t.Fatalf("block has %d txs, want 1 (the failing call must be excluded)", len(b1.Transactions))
	}

	// Follower seeded identically must ACCEPT the block (no residue → roots match).
	exB, sdbB := newTestExecutor(t)
	defer exB.Close()
	defer sdbB.Close()
	fund(t, sdbB, from, 1_000_000)
	if err := exB.ExecuteBlock(b1); err != nil {
		t.Fatalf("follower rejected block from a failed-tx producer (residue leaked): %v", err)
	}
	if n, _ := sdbB.GetNonce(from); n != 1 {
		t.Fatalf("sender nonce = %d, want 1 (a failed tx must not consume a nonce)", n)
	}
}

func newTestExecutor(t *testing.T) (*Executor, *state.StateDB) {
	t.Helper()
	sdb, err := state.NewStateDB(t.TempDir())
	if err != nil {
		t.Fatalf("statedb: %v", err)
	}
	ex, err := NewExecutor(sdb, t.TempDir())
	if err != nil {
		t.Fatalf("executor: %v", err)
	}
	return ex, sdb
}

func testAddr(b byte) qcrypto.Address {
	var a qcrypto.Address
	for i := range a {
		a[i] = b
	}
	return a
}

func fund(t *testing.T, sdb *state.StateDB, addr qcrypto.Address, amt int64) {
	t.Helper()
	acc := types.NewAccount(addr)
	acc.AddBalance(big.NewInt(amt))
	sdb.SetAccount(acc)
	if _, err := sdb.Commit(); err != nil {
		t.Fatalf("fund commit: %v", err)
	}
}

const testFee = int64(1000)

// signedTransfer builds a signed transfer with a NONZERO fee. With the fee-credit
// fix in ExecuteBlock, ProduceBlock (producer) and ExecuteBlock (follower) now
// reach the IDENTICAL post-state for a fee-bearing block — both credit the fee to
// the validator — so a produced block applies cleanly on a fresh node instead of
// diverging on state root.
func signedTransfer(t *testing.T, from qcrypto.Address, priv *qcrypto.PrivateKey, to qcrypto.Address, amount int64, nonce uint64) *types.Transaction {
	t.Helper()
	tx := types.NewTransfer(from, to, big.NewInt(amount), big.NewInt(testFee), nonce, time.Now().Unix())
	if err := tx.Sign(priv, true); err != nil {
		t.Fatalf("sign tx: %v", err)
	}
	return tx
}

// HAPPY PATH (the anti-wedge guard): a valid, state-mutating block must be ACCEPTED,
// its state committed, and the head advanced. Produced on executor A, applied on a
// fresh executor B seeded identically.
func TestExecuteBlockAcceptsValidBlockAndAdvancesHead(t *testing.T) {
	pub, priv, _ := qcrypto.GenerateKeypair()
	from := qcrypto.DeriveAddress(pub)
	to := testAddr(0x55)
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	vaddr := qcrypto.DeriveAddress(vpub)

	// Producer A.
	exA, sdbA := newTestExecutor(t)
	defer exA.Close()
	defer sdbA.Close()
	fund(t, sdbA, from, 1_000_000)
	tx := signedTransfer(t, from, priv, to, 100, 0)
	b1, err := exA.ProduceBlock([]*types.Transaction{tx}, vaddr, vpub.Bytes(), vpriv, [32]byte{}, 1)
	if err != nil {
		t.Fatalf("produce: %v", err)
	}

	// Consumer B, seeded identically.
	exB, sdbB := newTestExecutor(t)
	defer exB.Close()
	defer sdbB.Close()
	fund(t, sdbB, from, 1_000_000)

	if err := exB.ExecuteBlock(b1); err != nil {
		t.Fatalf("valid fee-bearing block was rejected by ExecuteBlock: %v (fee-credit fix regressed?)", err)
	}
	if h, _ := exB.LatestHeight(); h != 1 {
		t.Fatalf("head did not advance: got %d, want 1", h)
	}
	fromBal, _ := sdbB.GetBalance(from)
	if fromBal.Cmp(big.NewInt(1_000_000-100-testFee)) != 0 {
		t.Fatalf("sender balance = %s, want %d", fromBal, 1_000_000-100-testFee)
	}
	toBal, _ := sdbB.GetBalance(to)
	if toBal.Cmp(big.NewInt(100)) != 0 {
		t.Fatalf("recipient balance = %s, want 100", toBal)
	}
	// The follower must have credited the fee to the validator, exactly like the
	// producer did — this is the fee-credit fix (item 2). Without it, the block's
	// state root would not have matched and ExecuteBlock above would have failed.
	vBal, _ := sdbB.GetBalance(vaddr)
	if vBal.Cmp(big.NewInt(testFee)) != 0 {
		t.Fatalf("validator fee credit = %s, want %d (fee not credited on apply path)", vBal, testFee)
	}
}

// Crash recovery: if a block's body + chain head were persisted but the node
// crashed before the state commit, boot reconciliation must replay the stored
// block so state catches up to the head — instead of wedging on a nonce mismatch.
func TestReconcileReplaysHeadAheadOfState(t *testing.T) {
	pub, priv, _ := qcrypto.GenerateKeypair()
	from := qcrypto.DeriveAddress(pub)
	to := testAddr(0x77)
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	vaddr := qcrypto.DeriveAddress(vpub)

	// Producer A: fund, produce a 3-block chain.
	exA, sdbA := newTestExecutor(t)
	defer exA.Close()
	defer sdbA.Close()
	fund(t, sdbA, from, 1_000_000)
	var blocks []*types.Block
	var prev [32]byte
	for h := uint64(1); h <= 3; h++ {
		tx := signedTransfer(t, from, priv, to, 100, h-1)
		b, err := exA.ProduceBlock([]*types.Transaction{tx}, vaddr, vpub.Bytes(), vpriv, prev, h)
		if err != nil {
			t.Fatalf("produce %d: %v", h, err)
		}
		blocks = append(blocks, b)
		prev = b.Hash
	}

	// Follower B: apply blocks 1..2 cleanly.
	exB, sdbB := newTestExecutor(t)
	defer exB.Close()
	defer sdbB.Close()
	fund(t, sdbB, from, 1_000_000)
	for i := 0; i < 2; i++ {
		if err := exB.ExecuteBlock(blocks[i]); err != nil {
			t.Fatalf("apply block %d: %v", i+1, err)
		}
	}

	// Simulate the crash window: block 3's body + head are durably stored, but
	// the state commit never happened (head=3, state=2).
	if err := exB.storeBlock(blocks[2]); err != nil {
		t.Fatalf("storeBlock: %v", err)
	}
	if h, _ := exB.LatestHeight(); h != 3 {
		t.Fatalf("chain head = %d, want 3", h)
	}
	if sh, ok := sdbB.AppliedHeight(); !ok || sh != 2 {
		t.Fatalf("state height = %d ok=%v, want 2", sh, ok)
	}

	// Reconcile must replay block 3 and land exactly on A's height-3 state.
	if err := exB.ReconcileState(); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if sh, _ := sdbB.AppliedHeight(); sh != 3 {
		t.Fatalf("state height after reconcile = %d, want 3", sh)
	}
	aRoot, _ := sdbA.StateRoot()
	bRoot, _ := sdbB.StateRoot()
	if aRoot != bRoot {
		t.Fatalf("state root mismatch after reconcile: A=%x B=%x", aRoot[:8], bRoot[:8])
	}
	aBal, _ := sdbA.GetBalance(from)
	bBal, _ := sdbB.GetBalance(from)
	if aBal.Cmp(bBal) != 0 {
		t.Fatalf("sender balance mismatch: A=%s B=%s", aBal, bBal)
	}
}

// A value-bearing TxCall must MOVE the value sender->contract, never BURN it.
// Before the fix execCall debited tx.Amount from the sender and never credited
// tx.To, destroying the coins out of total supply on every value-bearing call.
func TestExecCallConservesCallValue(t *testing.T) {
	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()

	// Install a trivial STOP contract at `to` so the call succeeds (non-reverted).
	code := []byte{0x00} // OpSTOP
	codeHash := qcrypto.Hash32(code)
	sdb.SetCode(codeHash, code)
	to := testAddr(0x22)
	cacc := types.NewAccount(to)
	cacc.IsContract = true
	cacc.CodeHash = codeHash
	sdb.SetAccount(cacc)
	if _, err := sdb.Commit(); err != nil {
		t.Fatalf("seed contract: %v", err)
	}

	pub, priv, _ := qcrypto.GenerateKeypair()
	from := qcrypto.DeriveAddress(pub)
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	vaddr := qcrypto.DeriveAddress(vpub)

	const start = int64(1_000_000)
	const amount = int64(50_000)
	fund(t, sdb, from, start)

	tx := types.NewCall(from, to, nil, big.NewInt(amount), big.NewInt(testFee), 0, time.Now().Unix())
	if err := tx.Sign(priv, true); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, err := ex.ProduceBlock([]*types.Transaction{tx}, vaddr, vpub.Bytes(), vpriv, [32]byte{}, 1); err != nil {
		t.Fatalf("produce: %v", err)
	}

	fromBal, _ := sdb.GetBalance(from)
	toBal, _ := sdb.GetBalance(to)
	vBal, _ := sdb.GetBalance(vaddr)

	if toBal.Cmp(big.NewInt(amount)) != 0 {
		t.Fatalf("contract balance = %s, want %d (call value burned, not credited)", toBal, amount)
	}
	if fromBal.Cmp(big.NewInt(start-amount-testFee)) != 0 {
		t.Fatalf("sender balance = %s, want %d", fromBal, start-amount-testFee)
	}
	if vBal.Cmp(big.NewInt(testFee)) != 0 {
		t.Fatalf("validator fee = %s, want %d", vBal, testFee)
	}
	// Conservation: nothing minted, nothing burned.
	total := new(big.Int).Add(fromBal, toBal)
	total.Add(total, vBal)
	if total.Cmp(big.NewInt(start)) != 0 {
		t.Fatalf("total supply = %s, want %d (value minted or burned)", total, start)
	}
}

// A block that MUTATES accounts but declares a wrong state root must be rejected
// AND leave committed state untouched. Uses a real transfer so the pre-fix
// commit-then-check code would have persisted the -100 before rejecting.
func TestExecuteBlockRejectsBadStateRootWithoutPersisting(t *testing.T) {
	pub, priv, _ := qcrypto.GenerateKeypair()
	from := qcrypto.DeriveAddress(pub)
	to := testAddr(0x55)
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	vaddr := qcrypto.DeriveAddress(vpub)

	exA, sdbA := newTestExecutor(t)
	defer exA.Close()
	defer sdbA.Close()
	fund(t, sdbA, from, 1_000_000)
	tx := signedTransfer(t, from, priv, to, 100, 0)
	b1, err := exA.ProduceBlock([]*types.Transaction{tx}, vaddr, vpub.Bytes(), vpriv, [32]byte{}, 1)
	if err != nil {
		t.Fatalf("produce: %v", err)
	}

	// Tamper the declared state root, re-hash and re-sign so only the root is wrong.
	var badRoot [32]byte
	badRoot[0] = 0xFF
	b1.Header.StateRoot = badRoot
	h, _ := b1.ComputeHash()
	b1.Hash = h
	_ = b1.SignBlock(vpriv)

	exC, sdbC := newTestExecutor(t)
	defer exC.Close()
	defer sdbC.Close()
	fund(t, sdbC, from, 1_000_000)

	if err := exC.ExecuteBlock(b1); err == nil {
		t.Fatal("expected state-root mismatch rejection, got nil")
	}
	// The transfer's -100 must NOT have persisted.
	fromBal, _ := sdbC.GetBalance(from)
	if fromBal.Cmp(big.NewInt(1_000_000)) != 0 {
		t.Fatalf("rejected block persisted state: sender balance = %s, want 1000000", fromBal)
	}
	if toBal, _ := sdbC.GetBalance(to); toBal.Sign() != 0 {
		t.Fatalf("rejected block credited recipient: %s, want 0", toBal)
	}
	if hgt, _ := exC.LatestHeight(); hgt != 0 {
		t.Fatalf("head advanced to %d on a rejected block", hgt)
	}
}

// verifyBlockLinkage must reject wrong-chain-id, out-of-range-height-on-empty-chain,
// gap, and prev-hash-mismatch — and the ERROR must show linkage is the cause (not an
// incidental state-root failure).
func TestExecuteBlockLinkageRejections(t *testing.T) {
	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()
	pub, priv, _ := qcrypto.GenerateKeypair()
	vaddr := qcrypto.DeriveAddress(pub)

	sign := func(b *types.Block) *types.Block {
		h, _ := b.ComputeHash()
		b.Hash = h
		_ = b.SignBlock(priv)
		return b
	}
	mustRejectWith := func(name string, b *types.Block, want string) {
		err := ex.ExecuteBlock(b)
		if err == nil {
			t.Fatalf("%s: expected rejection, got nil", name)
		}
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: error %q does not mention %q (linkage may not be the cause)", name, err.Error(), want)
		}
	}

	mustRejectWith("wrong-chain-id", sign(&types.Block{Header: types.BlockHeader{
		Height: 1, Timestamp: time.Now().Unix(), ChainID: "evil-chain",
		AlgID:        qcrypto.AlgMLDSA65,
		TxMerkleRoot: qcrypto.Hash32([]byte{}), ValidatorAddr: vaddr,
	}}), "wrong chain id")

	mustRejectWith("unknown-alg-id", sign(&types.Block{Header: types.BlockHeader{
		Height: 1, Timestamp: time.Now().Unix(), ChainID: types.ChainID,
		AlgID:        "ECDSA-secp256k1",
		TxMerkleRoot: qcrypto.Hash32([]byte{}), ValidatorAddr: vaddr,
	}}), "unsupported block alg_id")

	mustRejectWith("future-timestamp", sign(&types.Block{Header: types.BlockHeader{
		Height: 1, Timestamp: time.Now().Add(time.Hour).Unix(), ChainID: types.ChainID,
		AlgID:        qcrypto.AlgMLDSA65,
		TxMerkleRoot: qcrypto.Hash32([]byte{}), ValidatorAddr: vaddr,
	}}), "ahead of local clock")

	mustRejectWith("height-not-1-on-empty", sign(&types.Block{Header: types.BlockHeader{
		Height: 7, Timestamp: time.Now().Unix(), ChainID: types.ChainID,
		AlgID:        qcrypto.AlgMLDSA65,
		TxMerkleRoot: qcrypto.Hash32([]byte{}), ValidatorAddr: vaddr,
	}}), "empty chain")

	// Establish head at height 1 (empty state → empty root).
	b1, err := ex.ProduceBlock(nil, vaddr, pub.Bytes(), priv, [32]byte{}, 1)
	if err != nil {
		t.Fatalf("produce block 1: %v", err)
	}
	if hgt, _ := ex.LatestHeight(); hgt != 1 {
		t.Fatalf("head = %d after produce, want 1", hgt)
	}

	mustRejectWith("gap-height", sign(&types.Block{Header: types.BlockHeader{
		Height: 3, Timestamp: time.Now().Unix(), ChainID: types.ChainID,
		AlgID:    qcrypto.AlgMLDSA65,
		PrevHash: b1.Hash, TxMerkleRoot: qcrypto.Hash32([]byte{}), ValidatorAddr: vaddr,
	}}), "non-contiguous")

	var wrongPrev [32]byte
	wrongPrev[0] = 0xAB
	mustRejectWith("bad-prev-hash", sign(&types.Block{Header: types.BlockHeader{
		Height: 2, Timestamp: time.Now().Unix(), ChainID: types.ChainID,
		AlgID:    qcrypto.AlgMLDSA65,
		PrevHash: wrongPrev, TxMerkleRoot: qcrypto.Hash32([]byte{}), ValidatorAddr: vaddr,
	}}), "prev-hash mismatch")

	// Block time must not run backwards relative to the parent.
	mustRejectWith("timestamp-before-parent", sign(&types.Block{Header: types.BlockHeader{
		Height: 2, Timestamp: b1.Header.Timestamp - 60, ChainID: types.ChainID,
		AlgID:    qcrypto.AlgMLDSA65,
		PrevHash: b1.Hash, TxMerkleRoot: qcrypto.Hash32([]byte{}), ValidatorAddr: vaddr,
	}}), "non-monotonic timestamp")

	if hgt, _ := ex.LatestHeight(); hgt != 1 {
		t.Fatalf("head moved to %d after rejected blocks, want 1", hgt)
	}
}
