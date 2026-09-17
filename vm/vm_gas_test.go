package vm

import (
	"fmt"
	"math/big"
	"strings"
	"testing"
)

func enableGasV1(e *Engine) {
	e.SetGasRules(func(uint64) bool { return true })
}

// tightLoop spins until it runs out of gas. Each iteration is JUMPDEST+PUSH+PUSH+JUMPI.
var tightLoop = []byte{OpJUMPDEST, OpPUSH1, 0x00, OpPUSH1, 0x01, OpJUMPI}

func TestChildCannotExceedParentCallGas(t *testing.T) {
	sdb := newTestState(t)
	defer sdb.Close()

	caller := addr(0x70)
	callee := addr(0x71)
	const childBudget uint64 = 500

	installContract(t, sdb, callee, tightLoop, 0)
	installContract(t, sdb, caller, concat(callCode(OpCALL, callee, 0, childBudget), []byte{OpSTOP}), 0)

	e := NewEngine(sdb)
	enableGasV1(e)
	res, err := e.CallWithContext(addr(0x01), addr(0x01), caller, nil, big.NewInt(0), 1, 0, false)
	if err != nil {
		t.Fatalf("parent should finish after a capped child: %v", err)
	}
	// Parent pays CALL setup plus at most the child's allocation.
	if res.GasUsed < 700 {
		t.Fatalf("parent gas %d is below the CALL base; the child was not invoked", res.GasUsed)
	}
	if res.GasUsed > 700+7*3+childBudget+50 {
		t.Fatalf("parent gas %d exceeds CALL setup + child budget %d — child ran past its allocation", res.GasUsed, childBudget)
	}

	// Pre-fork: the same child starts at MaxGasPerCall and the parent OOGs
	// charging that work afterward.
	sdb2 := newTestState(t)
	defer sdb2.Close()
	installContract(t, sdb2, callee, tightLoop, 0)
	installContract(t, sdb2, caller, concat(callCode(OpCALL, callee, 0, childBudget), []byte{OpSTOP}), 0)
	legacy := NewEngine(sdb2)
	_, legacyErr := legacy.CallWithContext(addr(0x01), addr(0x01), caller, nil, big.NewInt(0), 1, 0, false)
	if legacyErr == nil {
		t.Fatal("pre-fork parent must OOG after an uncapped child burns MaxGasPerCall")
	}
	if !strings.Contains(legacyErr.Error(), "out of gas") {
		t.Fatalf("pre-fork error = %v, want out of gas", legacyErr)
	}
	// useGas reports the attempted total (parent setup + child GasUsed).
	if !strings.Contains(legacyErr.Error(), fmt.Sprintf("/ limit %d", MaxGasPerCall)) {
		t.Fatalf("pre-fork error = %v, want parent limit MaxGasPerCall", legacyErr)
	}
}

// CALL with remaining gas 0 must stay 0 under vm_gas_v1. Pre-fork, 0 meant
// MaxGasPerCall, so a child could SSTORE on a "zero" stipend.
func TestExplicitZeroCallGasIsNotDefault(t *testing.T) {
	sdb := newTestState(t)
	defer sdb.Close()

	caller := addr(0x72)
	callee := addr(0x73)
	child := concat(sstoreSlot(1), pushU(0x42), []byte{OpSSTORE})
	installContract(t, sdb, callee, child, 0)
	installContract(t, sdb, caller, concat(callCode(OpCALL, callee, 0, 0), []byte{OpSTOP}), 0)

	e := NewEngine(sdb)
	enableGasV1(e)
	if _, err := e.CallWithContext(addr(0x01), addr(0x01), caller, nil, big.NewInt(0), 1, 0, false); err != nil {
		t.Fatalf("parent with a zero-gas child should still return: %v", err)
	}
	raw, _ := sdb.GetStorageValue(callee, bigToSlot(big.NewInt(1)))
	if new(big.Int).SetBytes(raw).Sign() != 0 {
		t.Fatal("zero-gas child wrote storage — 0 was treated as MaxGasPerCall")
	}

	sdb2 := newTestState(t)
	defer sdb2.Close()
	installContract(t, sdb2, callee, child, 0)
	installContract(t, sdb2, caller, concat(callCode(OpCALL, callee, 0, 0), []byte{OpSTOP}), 0)
	legacy := NewEngine(sdb2)
	if _, err := legacy.CallWithContext(addr(0x01), addr(0x01), caller, nil, big.NewInt(0), 1, 0, false); err != nil {
		t.Fatalf("pre-fork zero stipend should expand to MaxGasPerCall: %v", err)
	}
	raw2, _ := sdb2.GetStorageValue(callee, bigToSlot(big.NewInt(1)))
	if new(big.Int).SetBytes(raw2).Cmp(big.NewInt(0x42)) != 0 {
		t.Fatalf("pre-fork child slot = %x, want 0x42 (0 still meant default gas)", raw2)
	}
}

// A 32 MiB RETURN used to allocate the buffer for the base cost of RETURN (0)
// plus two PUSHes. chargeCopy must OOG first so the node never makes() that
// buffer on a tiny budget.
func TestReturnCopyChargedBeforeLoad(t *testing.T) {
	sdb := newTestState(t)
	defer sdb.Close()
	e := NewEngine(sdb)
	enableGasV1(e)
	code := concat(pushU(0), pushU(MaxMemoryBytes), []byte{OpRETURN})
	res := e.Execute(&ExecutionContext{
		Caller:      addr(0x01),
		Origin:      addr(0x01),
		Contract:    addr(0x74),
		CallValue:   big.NewInt(0),
		Code:        code,
		BlockHeight: 1,
		Gas:         1_000,
		ExplicitGas: true,
	})
	if res.Err == nil {
		t.Fatal("32 MiB RETURN on a 1000-gas budget must OOG")
	}
	if !strings.Contains(res.Err.Error(), "out of gas") {
		t.Fatalf("error = %v, want out of gas (copy charged before mem.load)", res.Err)
	}
	copyCost := wordsFor(MaxMemoryBytes) * DefaultGasTable().MemPerWord
	if copyCost <= 1_000 {
		t.Fatalf("test budget is not below the 32 MiB copy charge (%d)", copyCost)
	}
}

func TestCallInputCopyChargedBeforeLoad(t *testing.T) {
	sdb := newTestState(t)
	defer sdb.Close()
	callee := addr(0x75)
	installContract(t, sdb, callee, []byte{OpSTOP}, 0)

	// CALL argsSize = MaxMemoryBytes, empty memory. chargeCopy must fail
	// before allocating the 32 MiB calldata buffer.
	code := concat(
		pushU(0),              // retSize
		pushU(0),              // retOffset
		pushU(MaxMemoryBytes), // argsSize
		pushU(0),              // argsOffset
		pushU(0),              // value
		push32(addrToBig(callee)),
		pushU(100_000), // subGas
		[]byte{OpCALL},
	)
	e := NewEngine(sdb)
	enableGasV1(e)
	res := e.Execute(&ExecutionContext{
		Caller:      addr(0x01),
		Origin:      addr(0x01),
		Contract:    addr(0x76),
		CallValue:   big.NewInt(0),
		Code:        code,
		BlockHeight: 1,
		Gas:         2_000,
		ExplicitGas: true,
	})
	if res.Err == nil || !strings.Contains(res.Err.Error(), "out of gas") {
		t.Fatalf("32 MiB CALL args must OOG before mem.load, got %+v", res)
	}
}

func TestTopLevelExplicitZeroGasIsHonoured(t *testing.T) {
	sdb := newTestState(t)
	defer sdb.Close()
	e := NewEngine(sdb)
	enableGasV1(e)
	res := e.Execute(&ExecutionContext{
		Caller:      addr(0x01),
		Origin:      addr(0x01),
		Contract:    addr(0x77),
		CallValue:   big.NewInt(0),
		Code:        []byte{OpJUMPDEST},
		BlockHeight: 1,
		Gas:         0,
		ExplicitGas: true,
	})
	if res.Err == nil || !strings.Contains(res.Err.Error(), "out of gas") {
		t.Fatalf("explicit 0 must not expand to MaxGasPerCall, got %+v", res)
	}
}

func TestLegacyZeroGasStillMeansDefault(t *testing.T) {
	sdb := newTestState(t)
	defer sdb.Close()
	e := NewEngine(sdb)
	res := e.Execute(&ExecutionContext{
		Caller:      addr(0x01),
		Origin:      addr(0x01),
		Contract:    addr(0x78),
		CallValue:   big.NewInt(0),
		Code:        []byte{OpJUMPDEST},
		BlockHeight: 1,
		Gas:         0,
	})
	if res.Err != nil {
		t.Fatalf("pre-fork Gas==0 is MaxGasPerCall: %v", res.Err)
	}
}

func TestWordsForMatchesCopyCharge(t *testing.T) {
	if wordsFor(0) != 0 || wordsFor(1) != 1 || wordsFor(32) != 1 || wordsFor(33) != 2 {
		t.Fatalf("wordsFor edge cases: 0=%d 1=%d 32=%d 33=%d", wordsFor(0), wordsFor(1), wordsFor(32), wordsFor(33))
	}
	if wordsFor(MaxMemoryBytes) != uint64(MaxMemoryBytes+31)/32 {
		t.Fatal("32 MiB word count drifted")
	}
}
