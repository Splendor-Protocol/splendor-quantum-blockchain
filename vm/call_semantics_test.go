package vm

import (
	"math/big"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// installContract seeds an account holding `code` with `balance` native SPLD.
func installContract(t *testing.T, sdb *state.StateDB, a qcrypto.Address, code []byte, balance int64) {
	t.Helper()
	h := qcrypto.Hash32(code)
	sdb.SetCode(h, code)
	acc := types.NewAccount(a)
	acc.IsContract = true
	acc.CodeHash = h
	if balance > 0 {
		acc.AddBalance(big.NewInt(balance))
	}
	sdb.SetAccount(acc)
	if _, err := sdb.Commit(); err != nil {
		t.Fatalf("install contract: %v", err)
	}
}

// callCode emits a CALL with the given target, value and gas. CALL pops
// gas, addr, value, argsOffset, argsSize, retOffset, retSize — so the pushes
// run in reverse order.
func callCode(op byte, target qcrypto.Address, value, subGas uint64) []byte {
	chunks := [][]byte{
		pushU(0), // retSize
		pushU(0), // retOffset
		pushU(0), // argsSize
		pushU(0), // argsOffset
	}
	if op == OpCALL {
		chunks = append(chunks, pushU(value))
	}
	chunks = append(chunks,
		push32(addrToBig(target)),
		pushU(subGas),
		[]byte{op},
	)
	return concat(chunks...)
}

// A CALL carrying value must actually move that value. Pre-fix the engine
// popped `value`, handed it to the callee as CALLVALUE, and never transferred
// it — the callee observed funds it had not received, so any contract gating on
// CALLVALUE could be drained by a caller that paid nothing.
func TestCALLTransfersValue(t *testing.T) {
	sdb := newTestState(t)
	defer sdb.Close()

	caller := addr(0x60)
	callee := addr(0x61)

	installContract(t, sdb, callee, []byte{OpSTOP}, 0)
	installContract(t, sdb, caller, callCode(OpCALL, callee, 500, 100_000), 1_000)

	e := NewEngine(sdb)
	res, _ := e.CallWithContext(addr(0x01), addr(0x01), caller, nil, big.NewInt(0), 1, 0, false)
	if res.Err != nil {
		t.Fatalf("outer call failed: %v", res.Err)
	}

	calleeBal, _ := sdb.GetBalance(callee)
	callerBal, _ := sdb.GetBalance(caller)
	if calleeBal.Cmp(big.NewInt(500)) != 0 {
		t.Fatalf("callee balance = %s, want 500 (CALL value never transferred)", calleeBal)
	}
	if callerBal.Cmp(big.NewInt(500)) != 0 {
		t.Fatalf("caller balance = %s, want 500", callerBal)
	}
}

// A CALL for more value than the caller holds must fail, not silently succeed.
func TestCALLRejectsValueAboveBalance(t *testing.T) {
	sdb := newTestState(t)
	defer sdb.Close()

	caller := addr(0x62)
	callee := addr(0x63)
	installContract(t, sdb, callee, []byte{OpSTOP}, 0)
	installContract(t, sdb, caller, callCode(OpCALL, callee, 5_000, 100_000), 100)

	e := NewEngine(sdb)
	if _, err := e.CallWithContext(addr(0x01), addr(0x01), caller, nil, big.NewInt(0), 1, 0, false); err != nil {
		return // outright failure is fine
	}
	calleeBal, _ := sdb.GetBalance(callee)
	if calleeBal.Sign() != 0 {
		t.Fatalf("callee received %s from an under-funded caller", calleeBal)
	}
}

// DELEGATECALL must execute the TARGET's code in the caller's storage context.
// Pre-fix the handler overwrote the popped target with ctx.Contract, so a
// contract delegatecalling a library re-entered ITSELF — running the wrong code
// and recursing until the depth limit.
func TestDELEGATECALLRunsTargetCode(t *testing.T) {
	sdb := newTestState(t)
	defer sdb.Close()

	proxy := addr(0x64)
	library := addr(0x65)

	// Library writes a sentinel into slot 1 of whatever storage context it runs in.
	installContract(t, sdb, library, concat(sstoreSlot(1), pushU(0xD00D), []byte{OpSSTORE}), 0)
	installContract(t, sdb, proxy, callCode(OpDELEGATECALL, library, 0, 200_000), 0)

	e := NewEngine(sdb)
	res, _ := e.CallWithContext(addr(0x01), addr(0x01), proxy, nil, big.NewInt(0), 1, 0, false)
	if res.Err != nil {
		t.Fatalf("proxy call failed: %v", res.Err)
	}

	// The library's write must land in the PROXY's storage.
	raw, _ := sdb.GetStorageValue(proxy, bigToSlot(big.NewInt(1)))
	if got := new(big.Int).SetBytes(raw); got.Cmp(big.NewInt(0xD00D)) != 0 {
		t.Fatalf("proxy slot 1 = %s, want 53261 — DELEGATECALL did not run the target's code", got)
	}
	// And must NOT have run against the library's own storage.
	libRaw, _ := sdb.GetStorageValue(library, bigToSlot(big.NewInt(1)))
	if new(big.Int).SetBytes(libRaw).Sign() != 0 {
		t.Fatal("DELEGATECALL wrote to the library's storage instead of the caller's")
	}
}
