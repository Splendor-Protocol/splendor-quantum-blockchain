package vm

import (
	"math/big"
	"strings"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
)

// callCodeArgs emits a CALL carrying an explicit calldata range. CALL pops
// gas, addr, value, argsOffset, argsSize, retOffset, retSize — so the pushes
// run in reverse order. callCode (call_semantics_test.go) always passes an
// empty range; the input-copy charge only exists for a non-empty one.
func callCodeArgs(target qcrypto.Address, subGas, argsOffset, argsSize uint64) []byte {
	return concat(
		pushU(0), // retSize
		pushU(0), // retOffset
		pushU(argsSize),
		pushU(argsOffset),
		pushU(0), // value
		push32(addrToBig(target)),
		pushU(subGas),
		[]byte{OpCALL},
	)
}

const (
	// 4 KiB of calldata = 128 words = 384 gas of copy charge at MemPerWord 3.
	orderArgsSize  = 4096
	orderParentGas = 1_000_000
)

// callSetupGas is what the parent has spent by the time OpCALL has been
// decoded and its seven operands pushed: seven PUSH32 plus the CALL base.
func callSetupGas(gt GasTable) uint64 { return 7*gt.PUSH + gt.GasOf(OpCALL) }

// gasRecorder is a callee that writes the budget it was handed into slot 1.
// TOTALGAS pushes gas-gasUsed *after* its own base cost has been charged, so
// the budget at entry is the stored value plus the PUSH and TOTALGAS costs.
func gasRecorder() []byte { return concat(sstoreSlot(1), []byte{OpTOTALGAS, OpSSTORE}) }

func recordedChildBudget(t *testing.T, sdb interface {
	GetStorageValue(qcrypto.Address, [32]byte) ([]byte, error)
}, callee qcrypto.Address, gt GasTable) uint64 {
	t.Helper()
	raw, err := sdb.GetStorageValue(callee, bigToSlot(big.NewInt(1)))
	if err != nil {
		t.Fatalf("read recorded child budget: %v", err)
	}
	observed := new(big.Int).SetBytes(raw)
	if observed.Sign() == 0 {
		t.Fatal("child never recorded its budget — it was not invoked")
	}
	return observed.Uint64() + gt.PUSH + gt.GasOf(OpTOTALGAS)
}

// The child allowance must be derived from what the parent holds *after* the
// caller-side costs of the CALL itself, input copying included. The engine
// clamped callGas against gas-gasUsed first and charged chargeCopy(argsSz)
// second, so a 4 KiB input handed the child 384 gas the parent no longer had.
func TestCallChildBudgetExcludesInputCopyCost(t *testing.T) {
	sdb := newTestState(t)
	defer sdb.Close()

	caller := addr(0x78)
	callee := addr(0x79)
	installContract(t, sdb, callee, gasRecorder(), 0)

	gt := DefaultGasTable()
	e := NewEngine(sdb)
	enableGasV1(e)

	res := e.Execute(&ExecutionContext{
		Caller:      addr(0x01),
		Origin:      addr(0x01),
		Contract:    caller,
		CallValue:   big.NewInt(0),
		Code:        concat(callCodeArgs(callee, MaxGasPerCall, 0, orderArgsSize), []byte{OpSTOP}),
		BlockHeight: 1,
		Gas:         orderParentGas,
		ExplicitGas: true,
	})
	if res.Err != nil {
		t.Fatalf("parent frame failed: %v", res.Err)
	}

	copyCost := wordsFor(orderArgsSize) * gt.MemPerWord
	if copyCost == 0 {
		t.Fatal("test is vacuous: input copying is free")
	}
	// What the parent actually has left at the moment it dispatches the child.
	available := uint64(orderParentGas) - callSetupGas(gt) - copyCost
	childBudget := recordedChildBudget(t, sdb, callee, gt)

	if childBudget > available {
		t.Fatalf("child was handed %d gas while the parent held %d at dispatch; "+
			"%d gas of input copying was charged after the clamp (excess %d)",
			childBudget, available, copyCost, childBudget-available)
	}
	if childBudget != available {
		t.Fatalf("child budget = %d, want the parent's full remaining %d", childBudget, available)
	}
}

// The parent must not be able to overdraw itself through a child. Pre-fix the
// child was handed the pre-copy remainder, burned all of it, and the parent
// only discovered the excess at useGas(subResult.GasUsed) — after the work had
// already been done by every validating node.
func TestCallInputCopyCannotOverdrawParent(t *testing.T) {
	sdb := newTestState(t)
	defer sdb.Close()

	caller := addr(0x7a)
	callee := addr(0x7b)
	installContract(t, sdb, callee, tightLoop, 0)

	gt := DefaultGasTable()
	e := NewEngine(sdb)
	enableGasV1(e)

	res := e.Execute(&ExecutionContext{
		Caller:      addr(0x01),
		Origin:      addr(0x01),
		Contract:    caller,
		CallValue:   big.NewInt(0),
		Code:        concat(callCodeArgs(callee, MaxGasPerCall, 0, orderArgsSize), []byte{OpSTOP}),
		BlockHeight: 1,
		Gas:         orderParentGas,
		ExplicitGas: true,
	})
	if res.Err != nil {
		if strings.Contains(res.Err.Error(), "out of gas") {
			t.Fatalf("parent OOG'd settling a child it over-funded by the %d gas of input copying: %v",
				wordsFor(orderArgsSize)*gt.MemPerWord, res.Err)
		}
		t.Fatalf("parent frame failed: %v", res.Err)
	}
	if res.GasUsed > orderParentGas {
		t.Fatalf("parent charged %d gas against a %d limit", res.GasUsed, orderParentGas)
	}
}

// The reordering must be invisible on a chain where vm_gas_v1 is not active:
// chargeCopy is a no-op there and nothing else is charged between the two
// positions, so the pre-fork gas schedule is byte-for-byte what it was.
func TestPreForkCallGasOrderingUnchanged(t *testing.T) {
	gt := DefaultGasTable()
	// The recorder child spends PUSH + TOTALGAS + SSTORE and stops.
	childUsed := gt.PUSH + gt.GasOf(OpTOTALGAS) + gt.SSTORE
	copyCost := wordsFor(orderArgsSize) * gt.MemPerWord

	run := func(t *testing.T, forkActive bool) (budget, parentUsed uint64) {
		t.Helper()
		sdb := newTestState(t)
		defer sdb.Close()
		caller, callee := addr(0x7c), addr(0x7d)
		installContract(t, sdb, callee, gasRecorder(), 0)

		e := NewEngine(sdb)
		if forkActive {
			enableGasV1(e)
		}
		res := e.Execute(&ExecutionContext{
			Caller:      addr(0x01),
			Origin:      addr(0x01),
			Contract:    caller,
			CallValue:   big.NewInt(0),
			Code:        concat(callCodeArgs(callee, MaxGasPerCall, 0, orderArgsSize), []byte{OpSTOP}),
			BlockHeight: 1,
			Gas:         orderParentGas,
			ExplicitGas: true,
		})
		if res.Err != nil {
			t.Fatalf("parent frame failed (fork=%v): %v", forkActive, res.Err)
		}
		return recordedChildBudget(t, sdb, callee, gt), res.GasUsed
	}

	preBudget, preUsed := run(t, false)
	// Pre-fork a child still gets the default budget, never an explicit one,
	// and the parent is billed the CALL setup plus the child's usage only —
	// no input-copy charge, exactly as before the reorder.
	if preBudget != MaxGasPerCall {
		t.Fatalf("pre-fork child budget = %d, want the MaxGasPerCall default %d", preBudget, MaxGasPerCall)
	}
	if want := callSetupGas(gt) + childUsed; preUsed != want {
		t.Fatalf("pre-fork parent gas = %d, want %d (CALL setup %d + child %d, no copy charge)",
			preUsed, want, callSetupGas(gt), childUsed)
	}

	postBudget, postUsed := run(t, true)
	if want := uint64(orderParentGas) - callSetupGas(gt) - copyCost; postBudget != want {
		t.Fatalf("post-fork child budget = %d, want %d", postBudget, want)
	}
	if want := callSetupGas(gt) + copyCost + childUsed; postUsed != want {
		t.Fatalf("post-fork parent gas = %d, want %d (setup %d + copy %d + child %d)",
			postUsed, want, callSetupGas(gt), copyCost, childUsed)
	}
	if postUsed-preUsed != copyCost {
		t.Fatalf("fork changed parent gas by %d, want exactly the input-copy charge %d",
			postUsed-preUsed, copyCost)
	}
}
