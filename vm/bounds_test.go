package vm

import (
	"math/big"
	"testing"
	"time"
)

// maxResultBits bounds how large a VM arithmetic result may be. The VM is a
// 256-bit machine, so anything wider than a word is already out of contract.
const maxResultBits = 256

// EXP must terminate in bounded time and produce a word-sized result. Pre-fix
// the engine charged ExpPerBit * exponent.BitLen() — 91 gas for a 2^24 exponent
// — then ran big.Int.Exp with NO modulus, so a few bytes of bytecode bought
// megabytes of allocation and unbounded CPU on every validating node. A larger
// exponent stalls or OOMs the process outright.
func TestEXPIsBounded(t *testing.T) {
	sdb := newTestState(t)
	defer sdb.Close()
	e := NewEngine(sdb)

	// EXP pops the exponent then the base, so the base is pushed first.
	base := big.NewInt(3)
	exponent := new(big.Int).Lsh(big.NewInt(1), 24) // 16_777_216

	code := concat(sstoreSlot(0), push32(base), push32(exponent), []byte{OpEXP, OpSSTORE})

	done := make(chan *ExecutionResult, 1)
	go func() {
		done <- e.Execute(&ExecutionContext{
			Contract: addr(0x51), Code: code, CallValue: big.NewInt(0), Gas: MaxGasPerCall,
		})
	}()

	select {
	case res := <-done:
		if res.Err != nil {
			return // rejecting outright is also acceptable
		}
		raw, _ := sdb.GetStorageValue(addr(0x51), bigToSlot(big.NewInt(0)))
		if got := new(big.Int).SetBytes(raw); got.BitLen() > maxResultBits {
			t.Fatalf("EXP produced a %d-bit result, want <= %d", got.BitLen(), maxResultBits)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("EXP did not terminate within 20s — unbounded exponentiation")
	}
}

// EXP must agree with modular exponentiation over 2^256: 2^300 wraps to 0.
func TestEXPWrapsAtWordBoundary(t *testing.T) {
	sdb := newTestState(t)
	defer sdb.Close()
	e := NewEngine(sdb)

	code := concat(sstoreSlot(0), push32(big.NewInt(2)), push32(big.NewInt(300)), []byte{OpEXP, OpSSTORE})
	res := e.Execute(&ExecutionContext{
		Contract: addr(0x52), Code: code, CallValue: big.NewInt(0), Gas: MaxGasPerCall,
	})
	if res.Err != nil {
		t.Fatalf("EXP failed: %v", res.Err)
	}
	raw, _ := sdb.GetStorageValue(addr(0x52), bigToSlot(big.NewInt(0)))
	if got := new(big.Int).SetBytes(raw); got.Sign() != 0 {
		t.Fatalf("2^300 mod 2^256 = %s, want 0", got)
	}
}

// Arithmetic must wrap at 256 bits rather than growing big.Ints without bound.
// Repeated squaring via MUL is the cheapest way to blow up allocation: each MUL
// costs 5 gas but can double the operand width.
func TestMULStaysInWord(t *testing.T) {
	sdb := newTestState(t)
	defer sdb.Close()
	e := NewEngine(sdb)

	huge := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	// DUP1 duplicates the top of stack; unwrapped, 24 squarings would reach
	// roughly 2^24 * 256 bits.
	code := concat(sstoreSlot(0), push32(huge))
	for i := 0; i < 24; i++ {
		code = append(code, OpDUP1, OpMUL)
	}
	code = concat(code, []byte{OpSSTORE})

	done := make(chan *ExecutionResult, 1)
	go func() {
		done <- e.Execute(&ExecutionContext{
			Contract: addr(0x53), Code: code, CallValue: big.NewInt(0), Gas: MaxGasPerCall,
		})
	}()

	select {
	case res := <-done:
		if res.Err != nil {
			return
		}
		raw, _ := sdb.GetStorageValue(addr(0x53), bigToSlot(big.NewInt(0)))
		if got := new(big.Int).SetBytes(raw); got.BitLen() > maxResultBits {
			t.Fatalf("repeated MUL produced a %d-bit value, want <= %d — "+
				"arithmetic is not word-bounded", got.BitLen(), maxResultBits)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("repeated MUL did not terminate within 20s")
	}
}
