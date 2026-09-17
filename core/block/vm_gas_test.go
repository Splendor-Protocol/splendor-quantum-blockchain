package block

import (
	"math/big"
	"strings"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
)

func vmGasForks() forks.Schedule {
	return forks.Schedule{forks.VMGasV1: 1}
}

// gasLoop burns MaxGasPerCall then OOGs. Used to prove failed execution is
// included and counted instead of producing an empty-gas block.
var gasLoop = []byte{vm.OpJUMPDEST, vm.OpPUSH1, 0x00, vm.OpPUSH1, 0x01, vm.OpJUMPI}

func TestVMGasFailedCallIsIncludedReceipt(t *testing.T) {
	pub, priv, _ := qcrypto.GenerateKeypair()
	from := qcrypto.DeriveAddress(pub)
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	vaddr := qcrypto.DeriveAddress(vpub)
	const start = int64(1_000_000)

	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()
	sink := &receiptSink{}
	ex.WithForks(vmGasForks()).WithTxIndex(sink)
	fund(t, sdb, from, start)

	dep := signedDeploy(t, from, priv, []byte{0xEE}, 0)
	contract := deployAddr(from, 0)
	call := signedBytecodeCall(t, from, priv, contract, 1)

	b1, err := ex.ProduceBlock([]*types.Transaction{dep, call}, vaddr, vpub.Bytes(), vpriv, [32]byte{}, 1)
	if err != nil {
		t.Fatalf("produce: %v", err)
	}
	if len(b1.Transactions) != 2 {
		t.Fatalf("block has %d txs, want 2 (unknown-opcode call is a failed receipt)", len(b1.Transactions))
	}
	if n, _ := sdb.GetNonce(from); n != 2 {
		t.Fatalf("sender nonce = %d, want 2", n)
	}
	if bal, _ := sdb.GetBalance(from); bal.Cmp(big.NewInt(start-2*testFee)) != 0 {
		t.Fatalf("sender balance = %s, want %d (fee kept on failed execution)", bal, start-2*testFee)
	}
	if len(sink.receipts) < 2 {
		t.Fatalf("got %d receipts, want at least 2", len(sink.receipts))
	}
	rc := sink.receipts[len(sink.receipts)-1]
	if rc.Status != types.TxStatusFailed {
		t.Fatalf("status %d, want failed", rc.Status)
	}
	if rc.GasUsed == 0 {
		t.Fatal("failed execution recorded 0 gas")
	}

	exB, sdbB := newTestExecutor(t)
	defer exB.Close()
	defer sdbB.Close()
	exB.WithForks(vmGasForks())
	fund(t, sdbB, from, start)
	if err := exB.ExecuteBlock(b1); err != nil {
		t.Fatalf("follower rejected failed-receipt block: %v", err)
	}
	if n, _ := sdbB.GetNonce(from); n != 2 {
		t.Fatalf("follower nonce = %d, want 2", n)
	}
}

func TestVMGasLegacyDropsFailedCall(t *testing.T) {
	pub, priv, _ := qcrypto.GenerateKeypair()
	from := qcrypto.DeriveAddress(pub)
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	vaddr := qcrypto.DeriveAddress(vpub)
	const start = int64(1_000_000)

	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()
	sink := &receiptSink{}
	ex.WithTxIndex(sink)
	fund(t, sdb, from, start)

	dep := signedDeploy(t, from, priv, []byte{0xEE}, 0)
	contract := deployAddr(from, 0)
	call := signedBytecodeCall(t, from, priv, contract, 1)
	b1, err := ex.ProduceBlock([]*types.Transaction{dep, call}, vaddr, vpub.Bytes(), vpriv, [32]byte{}, 1)
	if err != nil {
		t.Fatalf("produce: %v", err)
	}
	if len(b1.Transactions) != 1 {
		t.Fatalf("block has %d txs, want 1 (legacy drops the failed call)", len(b1.Transactions))
	}
	if n, _ := sdb.GetNonce(from); n != 1 {
		t.Fatalf("sender nonce = %d, want 1", n)
	}
}

func TestVMGasRepeatedOOGCountsTowardWorkBudget(t *testing.T) {
	pub, priv, _ := qcrypto.GenerateKeypair()
	from := qcrypto.DeriveAddress(pub)
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	vaddr := qcrypto.DeriveAddress(vpub)
	const start = int64(10_000_000)

	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()
	sink := &receiptSink{}
	ex.WithForks(vmGasForks()).WithTxIndex(sink).WithProposalWorkLimit(15_000_000)
	fund(t, sdb, from, start)

	dep := signedDeploy(t, from, priv, gasLoop, 0)
	contract := deployAddr(from, 0)
	var calls []*types.Transaction
	calls = append(calls, dep)
	for i := uint64(1); i <= 3; i++ {
		calls = append(calls, signedBytecodeCall(t, from, priv, contract, i))
	}
	b1, err := ex.ProduceBlock(calls, vaddr, vpub.Bytes(), vpriv, [32]byte{}, 1)
	if err != nil {
		t.Fatalf("produce: %v", err)
	}
	if len(b1.Transactions) < 2 {
		t.Fatalf("block has %d txs, want deploy + at least one OOG call", len(b1.Transactions))
	}
	if len(b1.Transactions) > 2 {
		t.Fatalf("block packed %d txs; proposal work limit should stop after the first 10M-gas OOG", len(b1.Transactions))
	}

	var total uint64
	var oog int
	for _, rc := range sink.receipts {
		total += rc.GasUsed
		if rc.Status == types.TxStatusFailed && rc.GasUsed > 0 {
			oog++
		}
	}
	if total == 0 {
		t.Fatal("repeated OOG produced an empty-gas block")
	}
	if oog == 0 {
		t.Fatal("no failed OOG receipt was included")
	}
	if total > 15_000_000 {
		t.Fatalf("packed gas %d exceeds the proposal work limit", total)
	}
	if n, _ := sdb.GetNonce(from); n != 2 {
		t.Fatalf("nonce = %d, want 2 (one included OOG call)", n)
	}
}

func TestVMGasCallToEOAStillDropped(t *testing.T) {
	pub, priv, _ := qcrypto.GenerateKeypair()
	from := qcrypto.DeriveAddress(pub)
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	vaddr := qcrypto.DeriveAddress(vpub)

	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()
	ex.WithForks(vmGasForks())
	fund(t, sdb, from, 1_000_000)

	eoa := qcrypto.Address{}
	eoa[31] = 0xAB
	call := signedBytecodeCall(t, from, priv, eoa, 0)
	b1, err := ex.ProduceBlock([]*types.Transaction{call}, vaddr, vpub.Bytes(), vpriv, [32]byte{}, 1)
	if err != nil {
		t.Fatalf("produce: %v", err)
	}
	if len(b1.Transactions) != 0 {
		t.Fatalf("call to a non-contract was packed (%d txs); statically invalid txs stay dropped", len(b1.Transactions))
	}
	if n, _ := sdb.GetNonce(from); n != 0 {
		t.Fatalf("nonce = %d, want 0", n)
	}
}

func TestVMGasSuccessfulCallStillWrites(t *testing.T) {
	pub, priv, _ := qcrypto.GenerateKeypair()
	from := qcrypto.DeriveAddress(pub)
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	vaddr := qcrypto.DeriveAddress(vpub)

	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()
	ex.WithForks(vmGasForks())
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
		t.Fatalf("slot0 = %x, want 1 (success path must commit the vm_gas frame)", v)
	}
}

func TestVMGasForkName(t *testing.T) {
	if !strings.EqualFold(forks.VMGasV1, "vm_gas_v1") {
		t.Fatalf("fork name = %q", forks.VMGasV1)
	}
	if (forks.Schedule{}).Active(forks.VMGasV1, 1) {
		t.Fatal("unscheduled vm_gas_v1 must stay off")
	}
}
