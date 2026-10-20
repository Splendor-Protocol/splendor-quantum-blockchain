package block

import (
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
)

// revertForks schedules module_revert_v1 from block 1, the tier convention.
func revertForks() forks.Schedule { return forks.Schedule{forks.ModuleRevertV1: 1} }

// receiptSink captures the receipts ProduceBlock/ExecuteBlock hand to the tx
// index, so a test can read a module call's status without a real index.
type receiptSink struct{ receipts []*types.Receipt }

func (r *receiptSink) StoreReceipt(rc *types.Receipt) error {
	r.receipts = append(r.receipts, rc)
	return nil
}
func (r *receiptSink) StoreBlockReceipts(rcs []*types.Receipt) error {
	r.receipts = append(r.receipts, rcs...)
	return nil
}
func (r *receiptSink) IndexBlock(*types.Block) error { return nil }

// signedModuleCall builds a signed TxCall to the module router carrying data.
func signedModuleCall(t *testing.T, from qcrypto.Address, priv *qcrypto.PrivateKey, data []byte, nonce uint64) *types.Transaction {
	t.Helper()
	tx := types.NewCall(from, vm.ModuleRouterAddress, data, big.NewInt(0), big.NewInt(testFee), nonce, time.Now().Unix())
	if err := tx.Sign(priv, true); err != nil {
		t.Fatalf("sign module call: %v", err)
	}
	return tx
}

func envelope(t *testing.T, module, action string, params any) []byte {
	t.Helper()
	praw, _ := json.Marshal(params)
	data, err := json.Marshal(vm.ModuleCall{Module: module, Action: action, Params: praw})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return data
}

// softFailingCall is a well-formed qrc20 transfer of a token nobody deployed:
// the action itself fails ("insufficient balance"), which is SOFT.
func softFailingCall(t *testing.T, from qcrypto.Address, priv *qcrypto.PrivateKey, nonce uint64) *types.Transaction {
	t.Helper()
	return signedModuleCall(t, from, priv, envelope(t, "qrc20", "transfer", map[string]string{
		"token": testAddr(0x44).String(), "to": testAddr(0x55).String(), "amount": "1",
	}), nonce)
}

// forgeBlock signs a block carrying txs as-is (no execution), the way a
// producer running different rules would.
func forgeBlock(t *testing.T, txs []*types.Transaction, vaddr qcrypto.Address, vpriv *qcrypto.PrivateKey, height uint64, prev [32]byte) *types.Block {
	t.Helper()
	txRoot, err := types.ComputeTxMerkleRoot(txs)
	if err != nil {
		t.Fatalf("tx merkle root: %v", err)
	}
	blk := &types.Block{
		Header: types.BlockHeader{
			Height: height, Timestamp: time.Now().Unix(), ChainID: types.ChainID,
			AlgID: qcrypto.AlgMLDSA65, PrevHash: prev, TxMerkleRoot: txRoot,
			ValidatorAddr: vaddr, TxCount: uint32(len(txs)),
		},
		Transactions: txs,
	}
	h, _ := blk.ComputeHash()
	blk.Hash = h
	if err := blk.SignBlock(vpriv); err != nil {
		t.Fatalf("sign block: %v", err)
	}
	return blk
}

// Under module_revert_v1 a module call whose action fails is INCLUDED with a
// failed receipt: fee charged, nonce consumed, validator credited — the
// relayer's account moves on instead of jamming at that nonce. A follower on
// the same schedule reaches the same state; a follower without the schedule
// must reject the block as unappliable, never apply it to a different root.
func TestModuleRevertSoftFailureIncludedWithFailedReceipt(t *testing.T) {
	pub, priv, _ := qcrypto.GenerateKeypair()
	from := qcrypto.DeriveAddress(pub)
	to := testAddr(0x66)
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	vaddr := qcrypto.DeriveAddress(vpub)
	const start = int64(1_000_000)

	exA, sdbA := newTestExecutor(t)
	defer exA.Close()
	defer sdbA.Close()
	sink := &receiptSink{}
	exA.WithForks(revertForks()).WithTxIndex(sink)
	fund(t, sdbA, from, start)

	good := signedTransfer(t, from, priv, to, 100, 0)
	bad := softFailingCall(t, from, priv, 1)
	b1, err := exA.ProduceBlock([]*types.Transaction{good, bad}, vaddr, vpub.Bytes(), vpriv, [32]byte{}, 1)
	if err != nil {
		t.Fatalf("produce: %v", err)
	}
	if len(b1.Transactions) != 2 {
		t.Fatalf("block has %d txs, want 2 (a soft module failure is included)", len(b1.Transactions))
	}
	if n, _ := sdbA.GetNonce(from); n != 2 {
		t.Fatalf("sender nonce = %d, want 2 (the failed call consumes its nonce)", n)
	}
	if bal, _ := sdbA.GetBalance(from); bal.Cmp(big.NewInt(start-100-2*testFee)) != 0 {
		t.Fatalf("sender balance = %s, want %d (fee charged on the failed call)", bal, start-100-2*testFee)
	}
	if vb, _ := sdbA.GetBalance(vaddr); vb.Cmp(big.NewInt(2*testFee)) != 0 {
		t.Fatalf("validator balance = %s, want %d (credited both fees)", vb, 2*testFee)
	}
	if total := supplyOf(t, sdbA, from, to, vaddr); total.Cmp(big.NewInt(start)) != 0 {
		t.Fatalf("supply = %s, want %d", total, start)
	}
	if len(sink.receipts) != 2 {
		t.Fatalf("stored %d receipts, want 2", len(sink.receipts))
	}
	rc := sink.receipts[1]
	if rc.Status != types.TxStatusFailed {
		t.Fatalf("failed module call has status %d, want failed", rc.Status)
	}
	if !strings.Contains(rc.Error, "insufficient balance") {
		t.Fatalf("receipt error = %q, want the action's own message", rc.Error)
	}
	if len(rc.Logs) != 0 || rc.ContractAddress != nil {
		t.Fatalf("failed receipt must carry no logs or address: logs=%d addr=%v", len(rc.Logs), rc.ContractAddress)
	}
	if rc.Fee.Cmp(big.NewInt(testFee)) != 0 {
		t.Fatalf("receipt fee = %s, want %d", rc.Fee, testFee)
	}

	// Follower on the same schedule agrees.
	exB, sdbB := newTestExecutor(t)
	defer exB.Close()
	defer sdbB.Close()
	exB.WithForks(revertForks())
	fund(t, sdbB, from, start)
	if err := exB.ExecuteBlock(b1); err != nil {
		t.Fatalf("follower rejected the block: %v", err)
	}
	aRoot, _ := sdbA.StateRoot()
	bRoot, _ := sdbB.StateRoot()
	if aRoot != bRoot {
		t.Fatalf("state root mismatch: A=%x B=%x", aRoot[:8], bRoot[:8])
	}
	if n, _ := sdbB.GetNonce(from); n != 2 {
		t.Fatalf("follower nonce = %d, want 2", n)
	}
	if vb, _ := sdbB.GetBalance(vaddr); vb.Cmp(big.NewInt(2*testFee)) != 0 {
		t.Fatalf("follower validator balance = %s, want %d", vb, 2*testFee)
	}

	// Follower WITHOUT the schedule: the failed call is unappliable under its
	// rules, so it must refuse the block outright rather than reach a
	// different root.
	exC, sdbC := newTestExecutor(t)
	defer exC.Close()
	defer sdbC.Close()
	fund(t, sdbC, from, start)
	err = exC.ExecuteBlock(b1)
	if err == nil {
		t.Fatal("a follower without module_revert_v1 accepted a block with a failed module call")
	}
	if !strings.Contains(err.Error(), "unappliable tx") {
		t.Fatalf("want an unappliable-tx rejection, got: %v", err)
	}
	if strings.Contains(err.Error(), "state root mismatch") {
		t.Fatalf("must be rejected before the root comparison, got: %v", err)
	}
	if h, _ := exC.LatestHeight(); h != 0 {
		t.Fatalf("head advanced to %d on a rejected block", h)
	}
	if n, _ := sdbC.GetNonce(from); n != 0 {
		t.Fatalf("rejected block left residue: nonce %d", n)
	}
}

// HARD errors — unparseable envelope, unknown module, unknown action, a read
// action inside a TxCall — are still dropped by the producer under the fork,
// and a block carrying one is rejected by a fork-enabled follower. They are
// all detectable at admission; a block with one was not built honestly.
func TestModuleRevertHardErrorsStillDropped(t *testing.T) {
	pub, priv, _ := qcrypto.GenerateKeypair()
	from := qcrypto.DeriveAddress(pub)
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	vaddr := qcrypto.DeriveAddress(vpub)
	const start = int64(1_000_000)

	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"unparseable envelope", []byte("not-a-valid-module-envelope")},
		{"unknown module", envelope(t, "nope", "x", struct{}{})},
		{"unknown action", envelope(t, "qrc20", "explode", struct{}{})},
		{"read action", envelope(t, "qrc20", "balance_of", map[string]string{"token": testAddr(0x44).String(), "owner": from.String()})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exA, sdbA := newTestExecutor(t)
			defer exA.Close()
			defer sdbA.Close()
			exA.WithForks(revertForks())
			fund(t, sdbA, from, start)

			hardTx := signedModuleCall(t, from, priv, tc.data, 0)
			b1, err := exA.ProduceBlock([]*types.Transaction{hardTx}, vaddr, vpub.Bytes(), vpriv, [32]byte{}, 1)
			if err != nil {
				t.Fatalf("produce: %v", err)
			}
			if len(b1.Transactions) != 0 {
				t.Fatalf("producer packed a HARD-failing call")
			}
			if n, _ := sdbA.GetNonce(from); n != 0 {
				t.Fatalf("dropped tx consumed nonce: %d", n)
			}
			if bal, _ := sdbA.GetBalance(from); bal.Cmp(big.NewInt(start)) != 0 {
				t.Fatalf("dropped tx charged a fee: %s", bal)
			}

			// A follower on the same schedule refuses a forged block with it.
			exB, sdbB := newTestExecutor(t)
			defer exB.Close()
			defer sdbB.Close()
			exB.WithForks(revertForks())
			fund(t, sdbB, from, start)
			forged := forgeBlock(t, []*types.Transaction{hardTx}, vaddr, vpriv, 1, [32]byte{})
			err = exB.ExecuteBlock(forged)
			if err == nil || !strings.Contains(err.Error(), "unappliable tx") {
				t.Fatalf("want unappliable-tx rejection, got %v", err)
			}
			if n, _ := sdbB.GetNonce(from); n != 0 {
				t.Fatalf("rejected block left residue: nonce %d", n)
			}
		})
	}
}

// Without the fork the legacy rules hold: a failing module call is dropped,
// no fee, no nonce, and a follower without the fork agrees.
func TestModuleRevertLegacyWithoutForkUnchanged(t *testing.T) {
	pub, priv, _ := qcrypto.GenerateKeypair()
	from := qcrypto.DeriveAddress(pub)
	to := testAddr(0x66)
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	vaddr := qcrypto.DeriveAddress(vpub)
	const start = int64(1_000_000)

	for _, sched := range []forks.Schedule{nil, {forks.ModuleRevertV1: 5}} {
		exA, sdbA := newTestExecutor(t)
		defer exA.Close()
		defer sdbA.Close()
		exA.WithForks(sched)
		fund(t, sdbA, from, start)

		good := signedTransfer(t, from, priv, to, 100, 0)
		bad := softFailingCall(t, from, priv, 1)
		b1, err := exA.ProduceBlock([]*types.Transaction{good, bad}, vaddr, vpub.Bytes(), vpriv, [32]byte{}, 1)
		if err != nil {
			t.Fatalf("produce: %v", err)
		}
		if len(b1.Transactions) != 1 {
			t.Fatalf("schedule %v: block has %d txs, want 1 (legacy drops the failing call)", sched, len(b1.Transactions))
		}
		if n, _ := sdbA.GetNonce(from); n != 1 {
			t.Fatalf("schedule %v: nonce = %d, want 1", sched, n)
		}
		if vb, _ := sdbA.GetBalance(vaddr); vb.Cmp(big.NewInt(testFee)) != 0 {
			t.Fatalf("schedule %v: validator = %s, want %d", sched, vb, testFee)
		}

		exB, sdbB := newTestExecutor(t)
		defer exB.Close()
		defer sdbB.Close()
		exB.WithForks(sched)
		fund(t, sdbB, from, start)
		if err := exB.ExecuteBlock(b1); err != nil {
			t.Fatalf("schedule %v: follower rejected: %v", sched, err)
		}
		if n, _ := sdbB.GetNonce(from); n != 1 {
			t.Fatalf("schedule %v: follower nonce = %d, want 1", sched, n)
		}
	}
}

// withDispatch swaps the executor's module entry point for the test.
func withDispatch(t *testing.T, fn func(*state.StateDB, vm.ModuleContext, []byte, *types.Receipt) error) {
	t.Helper()
	prev := dispatchModuleCall
	dispatchModuleCall = fn
	t.Cleanup(func() { dispatchModuleCall = prev })
}

// A module that panics under the fork becomes a failed receipt with the fixed
// text "module panic" — not a dropped tx, not a halted node — and every node
// applying the block agrees. Without the fork it stays dropped.
func TestModuleRevertPanicBecomesFailedReceipt(t *testing.T) {
	withDispatch(t, func(*state.StateDB, vm.ModuleContext, []byte, *types.Receipt) error {
		var m map[string]int
		m["boom"] = 1 // nil-map write: a real runtime panic
		return nil
	})

	pub, priv, _ := qcrypto.GenerateKeypair()
	from := qcrypto.DeriveAddress(pub)
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	vaddr := qcrypto.DeriveAddress(vpub)
	const start = int64(1_000_000)

	exA, sdbA := newTestExecutor(t)
	defer exA.Close()
	defer sdbA.Close()
	sink := &receiptSink{}
	exA.WithForks(revertForks()).WithTxIndex(sink)
	fund(t, sdbA, from, start)

	tx := signedModuleCall(t, from, priv, envelope(t, "qrc20", "transfer", struct{}{}), 0)
	b1, err := exA.ProduceBlock([]*types.Transaction{tx}, vaddr, vpub.Bytes(), vpriv, [32]byte{}, 1)
	if err != nil {
		t.Fatalf("produce: %v", err)
	}
	if len(b1.Transactions) != 1 {
		t.Fatalf("panicking call was not included (%d txs)", len(b1.Transactions))
	}
	if len(sink.receipts) != 1 || sink.receipts[0].Status != types.TxStatusFailed || sink.receipts[0].Error != "module panic" {
		t.Fatalf("receipts = %+v, want one failed receipt with error \"module panic\"", sink.receipts)
	}
	if n, _ := sdbA.GetNonce(from); n != 1 {
		t.Fatalf("nonce = %d, want 1", n)
	}

	exB, sdbB := newTestExecutor(t)
	defer exB.Close()
	defer sdbB.Close()
	exB.WithForks(revertForks())
	fund(t, sdbB, from, start)
	if err := exB.ExecuteBlock(b1); err != nil {
		t.Fatalf("follower rejected the block: %v", err)
	}

	// Legacy: the outer recover turns the panic into an unappliable tx.
	exC, sdbC := newTestExecutor(t)
	defer exC.Close()
	defer sdbC.Close()
	fund(t, sdbC, from, start)
	b1c, err := exC.ProduceBlock([]*types.Transaction{tx}, vaddr, vpub.Bytes(), vpriv, [32]byte{}, 1)
	if err != nil {
		t.Fatalf("legacy produce: %v", err)
	}
	if len(b1c.Transactions) != 0 {
		t.Fatal("legacy rules included a panicking call")
	}
}

// A SOFT failure reverts everything the module wrote before it failed —
// storage, logs, a deploy address — while keeping the fee debit and nonce
// bump made before the inner snapshot. Producer and follower agree.
func TestModuleRevertRevertsPartialWrites(t *testing.T) {
	contract := testAddr(0x77)
	var slot [32]byte
	slot[0] = 0x01
	withDispatch(t, func(sdb *state.StateDB, ctx vm.ModuleContext, _ []byte, rc *types.Receipt) error {
		if err := sdb.SetStorageValue(contract, slot, []byte("written-then-failed")); err != nil {
			return err
		}
		acc, _ := sdb.GetAccount(contract)
		acc.IsContract = true
		sdb.SetAccount(acc)
		rc.ContractAddress = &contract
		rc.AddLog(&types.Log{Address: contract})
		return errors.New("failed after writing")
	})

	pub, priv, _ := qcrypto.GenerateKeypair()
	from := qcrypto.DeriveAddress(pub)
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	vaddr := qcrypto.DeriveAddress(vpub)
	const start = int64(1_000_000)

	exA, sdbA := newTestExecutor(t)
	defer exA.Close()
	defer sdbA.Close()
	sink := &receiptSink{}
	exA.WithForks(revertForks()).WithTxIndex(sink)
	fund(t, sdbA, from, start)

	tx := signedModuleCall(t, from, priv, envelope(t, "qrc20", "deploy", struct{}{}), 0)
	b1, err := exA.ProduceBlock([]*types.Transaction{tx}, vaddr, vpub.Bytes(), vpriv, [32]byte{}, 1)
	if err != nil {
		t.Fatalf("produce: %v", err)
	}
	if len(b1.Transactions) != 1 {
		t.Fatal("soft failure was not included")
	}
	if v, _ := sdbA.GetStorageValue(contract, slot); len(v) != 0 {
		t.Fatalf("module write survived the revert: %q", v)
	}
	if acc, _ := sdbA.GetAccount(contract); acc.IsContract {
		t.Fatal("account mutation survived the revert")
	}
	rc := sink.receipts[0]
	if rc.Status != types.TxStatusFailed || rc.Error != "failed after writing" {
		t.Fatalf("receipt = status %d error %q", rc.Status, rc.Error)
	}
	if len(rc.Logs) != 0 || rc.ContractAddress != nil {
		t.Fatalf("receipt kept logs=%d addr=%v from the reverted module", len(rc.Logs), rc.ContractAddress)
	}
	if n, _ := sdbA.GetNonce(from); n != 1 {
		t.Fatalf("nonce = %d, want 1", n)
	}
	if bal, _ := sdbA.GetBalance(from); bal.Cmp(big.NewInt(start-testFee)) != 0 {
		t.Fatalf("balance = %s, want %d", bal, start-testFee)
	}

	exB, sdbB := newTestExecutor(t)
	defer exB.Close()
	defer sdbB.Close()
	exB.WithForks(revertForks())
	fund(t, sdbB, from, start)
	if err := exB.ExecuteBlock(b1); err != nil {
		t.Fatalf("follower rejected the block: %v", err)
	}
}

// Crash replay (ReconcileState) runs the same gated path: a block with a
// failed module call replayed from the chain DB lands on the producer's root.
func TestModuleRevertCrashReplayParity(t *testing.T) {
	pub, priv, _ := qcrypto.GenerateKeypair()
	from := qcrypto.DeriveAddress(pub)
	to := testAddr(0x66)
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	vaddr := qcrypto.DeriveAddress(vpub)
	const start = int64(1_000_000)

	exA, sdbA := newTestExecutor(t)
	defer exA.Close()
	defer sdbA.Close()
	exA.WithForks(revertForks())
	fund(t, sdbA, from, start)

	var blocks []*types.Block
	var prev [32]byte
	b1, err := exA.ProduceBlock([]*types.Transaction{signedTransfer(t, from, priv, to, 100, 0)}, vaddr, vpub.Bytes(), vpriv, prev, 1)
	if err != nil {
		t.Fatalf("produce 1: %v", err)
	}
	blocks = append(blocks, b1)
	prev = b1.Hash
	b2, err := exA.ProduceBlock([]*types.Transaction{softFailingCall(t, from, priv, 1), signedTransfer(t, from, priv, to, 100, 2)}, vaddr, vpub.Bytes(), vpriv, prev, 2)
	if err != nil {
		t.Fatalf("produce 2: %v", err)
	}
	if len(b2.Transactions) != 2 {
		t.Fatalf("block 2 has %d txs, want 2", len(b2.Transactions))
	}
	blocks = append(blocks, b2)

	// Follower B applies block 1, then crashes with block 2 stored but not
	// committed to state (head=2, state=1). WithForks is what main.go installs
	// before ReconcileState, so the replay is gated the same way.
	exB, sdbB := newTestExecutor(t)
	defer exB.Close()
	defer sdbB.Close()
	exB.WithForks(revertForks())
	fund(t, sdbB, from, start)
	if err := exB.ExecuteBlock(blocks[0]); err != nil {
		t.Fatalf("apply block 1: %v", err)
	}
	if err := exB.storeBlock(blocks[1]); err != nil {
		t.Fatalf("storeBlock: %v", err)
	}
	if sh, ok := sdbB.AppliedHeight(); !ok || sh != 1 {
		t.Fatalf("state height = %d ok=%v, want 1", sh, ok)
	}
	if err := exB.ReconcileState(); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if sh, _ := sdbB.AppliedHeight(); sh != 2 {
		t.Fatalf("state height after reconcile = %d, want 2", sh)
	}
	aRoot, _ := sdbA.StateRoot()
	bRoot, _ := sdbB.StateRoot()
	if aRoot != bRoot {
		t.Fatalf("state root mismatch after replay: A=%x B=%x", aRoot[:8], bRoot[:8])
	}
	if n, _ := sdbB.GetNonce(from); n != 3 {
		t.Fatalf("nonce after replay = %d, want 3", n)
	}

	// The same crash on a node whose schedule was NOT installed before
	// reconcile is a corruption report, not a silent divergence.
	exC, sdbC := newTestExecutor(t)
	defer exC.Close()
	defer sdbC.Close()
	exC.WithForks(revertForks())
	fund(t, sdbC, from, start)
	if err := exC.ExecuteBlock(blocks[0]); err != nil {
		t.Fatalf("C apply block 1: %v", err)
	}
	if err := exC.storeBlock(blocks[1]); err != nil {
		t.Fatalf("C storeBlock: %v", err)
	}
	exC.SetForks(nil)
	if err := exC.ReconcileState(); err == nil || !strings.Contains(err.Error(), "reapply tx 0") {
		t.Fatalf("replay without the fork must fail on the module call, got %v", err)
	}
}
