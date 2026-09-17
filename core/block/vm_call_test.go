package block

import (
	"math/big"
	"testing"
	"time"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
)

func vmCallForks() forks.Schedule {
	return forks.Schedule{forks.VMCallV1: 1}
}

func vmCallRevertForks() forks.Schedule {
	return forks.Schedule{forks.ModuleRevertV1: 1, forks.VMRevertV1: 1, forks.VMCallV1: 1}
}

func spendCallValueCode(sink qcrypto.Address) []byte {
	// CALL(gas, sink, CALLVALUE, 0, 0, 0, 0) then STOP.
	var code []byte
	code = append(code, vm.OpPUSH1, 0x00) // retSize
	code = append(code, vm.OpPUSH1, 0x00) // retOffset
	code = append(code, vm.OpPUSH1, 0x00) // argsSize
	code = append(code, vm.OpPUSH1, 0x00) // argsOffset
	code = append(code, vm.OpCALLVALUE)
	code = append(code, pushAddr(sink)...)
	code = append(code, vm.OpPUSH1+2, 0x01, 0x86, 0xA0) // PUSH3 gas = 100000
	code = append(code, vm.OpCALL, vm.OpSTOP)
	return code
}

func pushAddr(a qcrypto.Address) []byte {
	out := make([]byte, 33)
	out[0] = vm.OpPUSH32
	copy(out[1:], a[:])
	return out
}

func installStop(t *testing.T, sdb *state.StateDB, addr qcrypto.Address, code []byte) {
	t.Helper()
	h := qcrypto.Hash32(code)
	sdb.SetCode(h, code)
	acc := types.NewAccount(addr)
	acc.IsContract = true
	acc.CodeHash = h
	sdb.SetAccount(acc)
	if _, err := sdb.Commit(); err != nil {
		t.Fatalf("install: %v", err)
	}
}

func signedValueCall(t *testing.T, from qcrypto.Address, priv *qcrypto.PrivateKey, contract qcrypto.Address, amount, nonce uint64) *types.Transaction {
	t.Helper()
	tx := types.NewCall(from, contract, nil, big.NewInt(int64(amount)), big.NewInt(testFee), nonce, time.Now().Unix())
	if err := tx.Sign(priv, true); err != nil {
		t.Fatalf("sign: %v", err)
	}
	return tx
}

// Under vm_call_v1 the attached value is credited before the bytecode runs, so
// a contract can forward CALLVALUE in the same transaction.
func TestVMCallValueAvailableDuringExecution(t *testing.T) {
	pub, priv, _ := qcrypto.GenerateKeypair()
	from := qcrypto.DeriveAddress(pub)
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	vaddr := qcrypto.DeriveAddress(vpub)
	spender := testAddr(0x81)
	sink := testAddr(0x82)
	const start = int64(1_000_000)
	const amount = int64(50_000)

	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()
	ex.WithForks(vmCallForks())
	installStop(t, sdb, sink, []byte{vm.OpSTOP})
	installStop(t, sdb, spender, spendCallValueCode(sink))
	fund(t, sdb, from, start)

	tx := signedValueCall(t, from, priv, spender, uint64(amount), 0)
	if _, err := ex.ProduceBlock([]*types.Transaction{tx}, vaddr, vpub.Bytes(), vpriv, [32]byte{}, 1); err != nil {
		t.Fatalf("produce: %v", err)
	}

	sinkBal, _ := sdb.GetBalance(sink)
	spenderBal, _ := sdb.GetBalance(spender)
	if sinkBal.Cmp(big.NewInt(amount)) != 0 {
		t.Fatalf("sink balance = %s, want %d (value was not spendable during exec)", sinkBal, amount)
	}
	if spenderBal.Sign() != 0 {
		t.Fatalf("spender balance = %s, want 0 after forwarding CALLVALUE", spenderBal)
	}
}

// Pre-fork: value is credited only after a successful run, so the inner CALL
// cannot move the attached funds and they land on the spender.
func TestVMCallValueAfterExecWithoutFork(t *testing.T) {
	pub, priv, _ := qcrypto.GenerateKeypair()
	from := qcrypto.DeriveAddress(pub)
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	vaddr := qcrypto.DeriveAddress(vpub)
	spender := testAddr(0x83)
	sink := testAddr(0x84)
	const start = int64(1_000_000)
	const amount = int64(50_000)

	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()
	installStop(t, sdb, sink, []byte{vm.OpSTOP})
	installStop(t, sdb, spender, spendCallValueCode(sink))
	fund(t, sdb, from, start)

	tx := signedValueCall(t, from, priv, spender, uint64(amount), 0)
	if _, err := ex.ProduceBlock([]*types.Transaction{tx}, vaddr, vpub.Bytes(), vpriv, [32]byte{}, 1); err != nil {
		t.Fatalf("produce: %v", err)
	}

	sinkBal, _ := sdb.GetBalance(sink)
	spenderBal, _ := sdb.GetBalance(spender)
	if sinkBal.Sign() != 0 {
		t.Fatalf("pre-fork sink balance = %s, want 0", sinkBal)
	}
	if spenderBal.Cmp(big.NewInt(amount)) != 0 {
		t.Fatalf("pre-fork spender balance = %s, want %d (credited after exec)", spenderBal, amount)
	}
}

// Combined with vm_revert_v1, a reverting entry frame must roll the pre-exec
// value transfer back with the rest of the snapshot.
func TestVMCallValueRollsBackOnRevert(t *testing.T) {
	pub, priv, _ := qcrypto.GenerateKeypair()
	from := qcrypto.DeriveAddress(pub)
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	vaddr := qcrypto.DeriveAddress(vpub)
	const start = int64(1_000_000)
	const amount = int64(40_000)

	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()
	sink := &receiptSink{}
	ex.WithForks(vmCallRevertForks()).WithTxIndex(sink)
	fund(t, sdb, from, start)

	dep := signedDeploy(t, from, priv, writeLogRevert, 0)
	contract := deployAddr(from, 0)
	call := signedValueCall(t, from, priv, contract, uint64(amount), 1)
	if _, err := ex.ProduceBlock([]*types.Transaction{dep, call}, vaddr, vpub.Bytes(), vpriv, [32]byte{}, 1); err != nil {
		t.Fatalf("produce: %v", err)
	}

	cBal, _ := sdb.GetBalance(contract)
	if cBal.Sign() != 0 {
		t.Fatalf("contract kept %s after revert; pre-exec value must roll back", cBal)
	}
	want := start - 2*testFee
	if bal, _ := sdb.GetBalance(from); bal.Cmp(big.NewInt(want)) != 0 {
		t.Fatalf("sender balance = %s, want %d (value returned, fees kept)", bal, want)
	}
	if len(sink.receipts) < 2 || sink.receipts[1].Status != types.TxStatusFailed {
		t.Fatal("reverted value-bearing call must be a failed receipt")
	}
}
