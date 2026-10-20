package block

import (
	"encoding/binary"
	"math/big"
	"testing"
	"time"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
)

// vmRevertForks schedules vm_revert_v1 from block 1 (module_revert_v1 too, per
// the tier convention that later rules never activate before it).
func vmRevertForks() forks.Schedule {
	return forks.Schedule{forks.ModuleRevertV1: 1, forks.VMRevertV1: 1}
}

// writeLogRevert is a contract that SSTOREs slot0=1, emits an empty LOG0, then
// REVERTs in the entry frame — the exact shape the engine cannot roll back on
// its own (it snapshots sub-calls only).
var writeLogRevert = []byte{
	vm.OpPUSH1, 0x00, vm.OpPUSH1, 0x01, vm.OpSSTORE, // slot0 = 1
	vm.OpPUSH1, 0x00, vm.OpPUSH1, 0x00, vm.OpLOG0, // offset 0, size 0
	vm.OpPUSH1, 0x00, vm.OpPUSH1, 0x00, vm.OpREVERT,
}

// writeStop is the success twin: SSTOREs slot0=1 and stops.
var writeStop = []byte{
	vm.OpPUSH1, 0x00, vm.OpPUSH1, 0x01, vm.OpSSTORE,
	vm.OpSTOP,
}

func deployAddr(from qcrypto.Address, nonce uint64) qcrypto.Address {
	nb := make([]byte, 8)
	binary.BigEndian.PutUint64(nb, nonce)
	h := qcrypto.Hash32(append(from[:], nb...))
	var a qcrypto.Address
	copy(a[:], h[:])
	return a
}

func signedDeploy(t *testing.T, from qcrypto.Address, priv *qcrypto.PrivateKey, code []byte, nonce uint64) *types.Transaction {
	t.Helper()
	tx := types.NewDeploy(from, code, big.NewInt(testFee), nonce, time.Now().Unix())
	if err := tx.Sign(priv, true); err != nil {
		t.Fatalf("sign deploy: %v", err)
	}
	return tx
}

func signedBytecodeCall(t *testing.T, from qcrypto.Address, priv *qcrypto.PrivateKey, contract qcrypto.Address, nonce uint64) *types.Transaction {
	t.Helper()
	tx := types.NewCall(from, contract, nil, big.NewInt(0), big.NewInt(testFee), nonce, time.Now().Unix())
	if err := tx.Sign(priv, true); err != nil {
		t.Fatalf("sign call: %v", err)
	}
	return tx
}

// Under vm_revert_v1 a top-level bytecode REVERT is atomic: every entry-frame
// storage write and log is rolled back, while the fee stays charged and the
// nonce consumed — the same deal a reverted EVM transaction gets. A follower
// on the same schedule reaches the identical state.
func TestVMRevertRollsBackEntryFrameStorage(t *testing.T) {
	pub, priv, _ := qcrypto.GenerateKeypair()
	from := qcrypto.DeriveAddress(pub)
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	vaddr := qcrypto.DeriveAddress(vpub)
	const start = int64(1_000_000)

	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()
	sink := &receiptSink{}
	ex.WithForks(vmRevertForks()).WithTxIndex(sink)
	fund(t, sdb, from, start)

	dep := signedDeploy(t, from, priv, writeLogRevert, 0)
	contract := deployAddr(from, 0)
	call := signedBytecodeCall(t, from, priv, contract, 1)

	b1, err := ex.ProduceBlock([]*types.Transaction{dep, call}, vaddr, vpub.Bytes(), vpriv, [32]byte{}, 1)
	if err != nil {
		t.Fatalf("produce: %v", err)
	}
	if len(b1.Transactions) != 2 {
		t.Fatalf("block has %d txs, want 2 (a reverted call is included)", len(b1.Transactions))
	}

	var slot0 [32]byte
	v, err := sdb.GetStorageValue(contract, slot0)
	if err != nil {
		t.Fatalf("read slot0: %v", err)
	}
	if len(v) != 0 && new(big.Int).SetBytes(v).Sign() != 0 {
		t.Fatalf("slot0 = %x after top-level revert, want rolled back under vm_revert_v1", v)
	}
	if n, _ := sdb.GetNonce(from); n != 2 {
		t.Fatalf("sender nonce = %d, want 2 (the reverted call consumes its nonce)", n)
	}
	if bal, _ := sdb.GetBalance(from); bal.Cmp(big.NewInt(start-2*testFee)) != 0 {
		t.Fatalf("sender balance = %s, want %d (fees kept on revert)", bal, start-2*testFee)
	}
	if len(sink.receipts) != 2 {
		t.Fatalf("got %d receipts, want 2", len(sink.receipts))
	}
	rc := sink.receipts[1]
	if rc.Status != types.TxStatusFailed {
		t.Fatalf("reverted call has status %d, want failed", rc.Status)
	}
	if len(rc.Logs) != 0 {
		t.Fatalf("reverted call carries %d logs, want 0 (logs roll back with the frame)", len(rc.Logs))
	}

	// A follower on the same schedule accepts the block and reaches the same state.
	exB, sdbB := newTestExecutor(t)
	defer exB.Close()
	defer sdbB.Close()
	exB.WithForks(vmRevertForks())
	fund(t, sdbB, from, start)
	if err := exB.ExecuteBlock(b1); err != nil {
		t.Fatalf("follower rejected block with reverted call: %v", err)
	}
	vB, err := sdbB.GetStorageValue(contract, slot0)
	if err != nil {
		t.Fatalf("follower read slot0: %v", err)
	}
	if len(vB) != 0 && new(big.Int).SetBytes(vB).Sign() != 0 {
		t.Fatalf("follower slot0 = %x, want rolled back", vB)
	}
}

// A successful call under vm_revert_v1 commits its writes exactly as before —
// the inner snapshot must not over-revert.
func TestVMRevertSuccessfulCallStillWrites(t *testing.T) {
	pub, priv, _ := qcrypto.GenerateKeypair()
	from := qcrypto.DeriveAddress(pub)
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	vaddr := qcrypto.DeriveAddress(vpub)

	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()
	ex.WithForks(vmRevertForks())
	fund(t, sdb, from, 1_000_000)

	dep := signedDeploy(t, from, priv, writeStop, 0)
	contract := deployAddr(from, 0)
	call := signedBytecodeCall(t, from, priv, contract, 1)
	if _, err := ex.ProduceBlock([]*types.Transaction{dep, call}, vaddr, vpub.Bytes(), vpriv, [32]byte{}, 1); err != nil {
		t.Fatalf("produce: %v", err)
	}

	var slot0 [32]byte
	v, err := sdb.GetStorageValue(contract, slot0)
	if err != nil {
		t.Fatalf("read slot0: %v", err)
	}
	if new(big.Int).SetBytes(v).Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("slot0 = %x, want 1 (successful writes still commit under vm_revert_v1)", v)
	}
}

// Without the fork the legacy behavior is preserved bit-for-bit: a reverted
// call is UNAPPLIABLE — the producer drops it (no receipt, no fee, no nonce),
// and a follower rejects any block that carries one. This is the pre-fork rule
// replayed blocks depend on, and the jam vm_revert_v1 exists to fix: the
// sender's reverting call can never land, and the producer's work is unpaid.
func TestVMRevertLegacyWithoutForkUnchanged(t *testing.T) {
	pub, priv, _ := qcrypto.GenerateKeypair()
	from := qcrypto.DeriveAddress(pub)
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	vaddr := qcrypto.DeriveAddress(vpub)
	const start = int64(1_000_000)

	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()
	sink := &receiptSink{}
	ex.WithTxIndex(sink) // no forks: legacy rules
	fund(t, sdb, from, start)

	dep := signedDeploy(t, from, priv, writeLogRevert, 0)
	contract := deployAddr(from, 0)
	call := signedBytecodeCall(t, from, priv, contract, 1)
	b1, err := ex.ProduceBlock([]*types.Transaction{dep, call}, vaddr, vpub.Bytes(), vpriv, [32]byte{}, 1)
	if err != nil {
		t.Fatalf("produce: %v", err)
	}
	if len(b1.Transactions) != 1 {
		t.Fatalf("block has %d txs, want 1 (legacy: the reverted call is dropped)", len(b1.Transactions))
	}
	var slot0 [32]byte
	v, err := sdb.GetStorageValue(contract, slot0)
	if err != nil {
		t.Fatalf("read slot0: %v", err)
	}
	if len(v) != 0 && new(big.Int).SetBytes(v).Sign() != 0 {
		t.Fatalf("slot0 = %x, want empty (the per-tx snapshot reverts the dropped call)", v)
	}
	if n, _ := sdb.GetNonce(from); n != 1 {
		t.Fatalf("sender nonce = %d, want 1 (a dropped call consumes no nonce)", n)
	}
	if bal, _ := sdb.GetBalance(from); bal.Cmp(big.NewInt(start-1*testFee)) != 0 {
		t.Fatalf("sender balance = %s, want %d (a dropped call is not charged)", bal, start-1*testFee)
	}

	// A block forged to carry the reverted call must be rejected by a
	// legacy-rules follower, never applied to a different root.
	exB, sdbB := newTestExecutor(t)
	defer exB.Close()
	defer sdbB.Close()
	fund(t, sdbB, from, start)
	forged := forgeBlock(t, []*types.Transaction{dep, call}, vaddr, vpriv, 1, [32]byte{})
	if err := exB.ExecuteBlock(forged); err == nil {
		t.Fatal("legacy follower accepted a block carrying a reverted call")
	}
}
