package vm

import (
	"math/big"
	"testing"
)

// push32 emits PUSH32 <32-byte big-endian v>.
func push32(v *big.Int) []byte {
	out := make([]byte, 33)
	out[0] = OpPUSH32
	b := v.Bytes()
	copy(out[33-len(b):], b)
	return out
}

func pushU(n uint64) []byte { return push32(new(big.Int).SetUint64(n)) }

func concat(chunks ...[]byte) []byte {
	var out []byte
	for _, c := range chunks {
		out = append(out, c...)
	}
	return out
}

// sstoreSlot pushes the destination slot. SSTORE pops the VALUE first and the
// SLOT second, so the slot has to be on the stack before whatever produces the
// value. Emit sstoreSlot(n), then the value-producing code, then OpSSTORE.
func sstoreSlot(slot uint64) []byte { return pushU(slot) }

// The opcode space must be unambiguous: no single byte may decode as two
// different instructions. Pre-fix PUSH1..PUSH31 occupied 0x52..0x70, which
// swallowed SLOAD (0x55), SSTORE (0x56), MLOAD, MSTORE, JUMP, JUMPI, JUMPDEST,
// CALLDATALOAD, CODECOPY and CALLER — every one of them was unreachable because
// the PUSH range was matched first.
func TestOpcodeTableHasNoCollisions(t *testing.T) {
	named := map[byte]string{
		OpSTOP: "STOP", OpADD: "ADD", OpSUB: "SUB", OpMUL: "MUL", OpDIV: "DIV",
		OpMOD: "MOD", OpEXP: "EXP", OpADDMOD: "ADDMOD", OpMULMOD: "MULMOD",
		OpISZERO: "ISZERO", OpBYTE: "BYTE", OpSIGNEXTEND: "SIGNEXTEND",
		OpEQ: "EQ", OpNEQ: "NEQ", OpLT: "LT", OpGT: "GT", OpLTE: "LTE", OpGTE: "GTE",
		OpAND: "AND", OpOR: "OR", OpXOR: "XOR", OpNOT: "NOT", OpSHL: "SHL", OpSHR: "SHR",
		OpPOP: "POP", OpMLOAD: "MLOAD", OpMSTORE: "MSTORE", OpMSTORE8: "MSTORE8",
		OpMSIZE: "MSIZE", OpSLOAD: "SLOAD", OpSSTORE: "SSTORE",
		OpJUMP: "JUMP", OpJUMPI: "JUMPI", OpPC: "PC", OpJUMPDEST: "JUMPDEST",
		OpCALLDATALOAD: "CALLDATALOAD", OpCALLDATASIZE: "CALLDATASIZE",
		OpCALLDATACOPY: "CALLDATACOPY", OpCODECOPY: "CODECOPY",
		OpRETURNDATASIZE: "RETURNDATASIZE", OpRETURNDATACOPY: "RETURNDATACOPY",
		OpCALLER: "CALLER", OpORIGIN: "ORIGIN", OpCALLVALUE: "CALLVALUE",
		OpCODESIZE: "CODESIZE", OpBLOCKHEIGHT: "BLOCKHEIGHT", OpTIMESTAMP: "TIMESTAMP",
		OpADDRESS: "ADDRESS", OpGASLIMIT: "GASLIMIT",
		OpSHA3: "SHA3", OpVERIFY_MLDSA: "VERIFY_MLDSA", OpSHA3_MEM: "SHA3_MEM",
		OpCALL: "CALL", OpSTATICCALL: "STATICCALL", OpDELEGATECALL: "DELEGATECALL",
		OpCREATE: "CREATE", OpCREATE2: "CREATE2", OpSELFDESTRUCT: "SELFDESTRUCT",
		OpTRANSFER: "TRANSFER", OpBALANCE: "BALANCE", OpTOTALGAS: "TOTALGAS",
		OpRETURN: "RETURN", OpREVERT: "REVERT", OpREQUIRE: "REQUIRE",
	}

	seen := make(map[byte]string, 256)
	claim := func(op byte, name string) {
		if prev, ok := seen[op]; ok {
			t.Errorf("opcode 0x%02x is claimed by both %s and %s", op, prev, name)
			return
		}
		seen[op] = name
	}

	for op, name := range named {
		claim(op, name)
	}
	for i := byte(0); i < 32; i++ {
		claim(OpPUSH1+i, "PUSH")
	}
	for i := byte(0); i < 16; i++ {
		claim(OpDUP1+i, "DUP")
	}
	for i := byte(0); i < 16; i++ {
		claim(OpSWAP1+i, "SWAP")
	}
	for i := byte(0); i <= 4; i++ {
		claim(OpLOG0+i, "LOG")
	}
}

// SSTORE followed by SLOAD must round-trip a value through contract storage.
// Pre-fix SSTORE (0x56) decoded as PUSH5 and SLOAD (0x55) as PUSH4, so this
// program could not even be decoded — storage was unreachable from bytecode.
func TestSSTOREAndSLOADAreReachable(t *testing.T) {
	sdb := newTestState(t)
	defer sdb.Close()

	contract := addr(0x44)
	e := NewEngine(sdb)

	code := concat(
		sstoreSlot(7), pushU(0xBEEF), []byte{OpSSTORE},
		pushU(7), []byte{OpSLOAD},
	)
	res := e.Execute(&ExecutionContext{
		Contract: contract, Code: code, CallValue: big.NewInt(0), Gas: MaxGasPerCall,
	})
	if res.Err != nil {
		t.Fatalf("SSTORE/SLOAD program failed: %v", res.Err)
	}

	raw, err := sdb.GetStorageValue(contract, bigToSlot(big.NewInt(7)))
	if err != nil {
		t.Fatalf("read slot: %v", err)
	}
	if got := new(big.Int).SetBytes(raw); got.Cmp(big.NewInt(0xBEEF)) != 0 {
		t.Fatalf("slot 7 = %s, want 48879 (SSTORE did not execute)", got)
	}
}

// JUMP must reach a JUMPDEST. Pre-fix JUMP (0x60) decoded as PUSH15 and
// JUMPDEST (0x63) as PUSH18, making all branching unreachable.
func TestJUMPIsReachable(t *testing.T) {
	sdb := newTestState(t)
	defer sdb.Close()
	e := NewEngine(sdb)

	// PUSH32 <dest>; JUMP; <filler>; JUMPDEST; store 1 into slot 0
	dest := uint64(len(pushU(0)) + 1 + 1) // PUSH32 + JUMP + one filler byte
	code := concat(
		pushU(dest), []byte{OpJUMP},
		[]byte{OpREVERT}, // filler that must be jumped OVER
		[]byte{OpJUMPDEST},
		sstoreSlot(0), pushU(1), []byte{OpSSTORE},
	)
	res := e.Execute(&ExecutionContext{
		Contract: addr(0x45), Code: code, CallValue: big.NewInt(0), Gas: MaxGasPerCall,
	})
	if res.Err != nil {
		t.Fatalf("JUMP program failed: %v", res.Err)
	}
	raw, _ := sdb.GetStorageValue(addr(0x45), bigToSlot(big.NewInt(0)))
	if new(big.Int).SetBytes(raw).Sign() == 0 {
		t.Fatal("JUMP did not land on JUMPDEST")
	}
}

// CALLDATALOAD must read calldata. Pre-fix it decoded as PUSH19.
func TestCALLDATALOADIsReachable(t *testing.T) {
	sdb := newTestState(t)
	defer sdb.Close()
	e := NewEngine(sdb)

	input := make([]byte, 32)
	input[31] = 0x2A

	code := concat(sstoreSlot(0), pushU(0), []byte{OpCALLDATALOAD, OpSSTORE})
	res := e.Execute(&ExecutionContext{
		Contract: addr(0x46), Code: code, Input: input,
		CallValue: big.NewInt(0), Gas: MaxGasPerCall,
	})
	if res.Err != nil {
		t.Fatalf("CALLDATALOAD program failed: %v", res.Err)
	}
	raw, _ := sdb.GetStorageValue(addr(0x46), bigToSlot(big.NewInt(0)))
	if got := new(big.Int).SetBytes(raw); got.Cmp(big.NewInt(0x2A)) != 0 {
		t.Fatalf("calldata word = %s, want 42", got)
	}
}

// The gas table must price each opcode as itself. Pre-fix the PUSH range check
// ran first in GasOf, so SSTORE was billed 3 gas (PUSH) instead of 20000 — every
// specific case from 0x53..0x70 was dead code.
func TestGasTableDoesNotPriceStorageAsPush(t *testing.T) {
	g := DefaultGasTable()
	if got := g.GasOf(OpSSTORE); got == g.PUSH {
		t.Fatalf("SSTORE priced as PUSH (%d) — gas table shadowed by PUSH range", got)
	}
	if got := g.GasOf(OpSLOAD); got == g.PUSH {
		t.Fatalf("SLOAD priced as PUSH (%d)", got)
	}
	if got := g.GasOf(OpJUMP); got != g.JUMP {
		t.Fatalf("JUMP priced %d, want %d", got, g.JUMP)
	}
	if got := g.GasOf(OpCALLER); got != g.CALLER {
		t.Fatalf("CALLER priced %d, want %d", got, g.CALLER)
	}
}
