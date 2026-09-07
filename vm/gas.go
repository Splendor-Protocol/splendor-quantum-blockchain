// Package vm — Configurable Gas Table for SQC-VM v3.
//
// IMPORTANT: The engine charges gas in two passes for some opcodes:
//   1. baseCost = gasTable.GasOf(op) — charged for every opcode
//   2. Extra gas — charged additionally inside the opcode handler for dynamic costs
//
// For SLOAD/SSTORE, TRANSFER, BALANCE, etc. the engine does:
//   useGas(e.gasTable.SLOAD - baseCost)   (extra on top of base)
// So GasOf(SLOAD) should return 1 (the base cost) and the SLOAD field in GasTable
// represents the TOTAL cost. The engine adds (TOTAL - 1) as extra.
//
// For simple opcodes (ADD, SUB, etc.) the base cost IS the full cost.
package vm

// GasTable holds the gas cost for every opcode.
// For opcodes with dynamic costs (SLOAD, SSTORE, EXP, LOG*, TRANSFER),
// the field represents the TOTAL base cost; the engine adds dynamic extras.
type GasTable struct {
	// Arithmetic
	ADD       uint64
	SUB       uint64
	MUL       uint64
	DIV       uint64
	MOD       uint64
	EXP       uint64
	ADDMOD    uint64
	MULMOD    uint64
	ISZERO    uint64
	BYTE      uint64
	SIGNEXTEND uint64
	ExpPerBit uint64 // additional gas per bit of exponent

	// Comparison
	EQ  uint64
	NEQ uint64
	LT  uint64
	GT  uint64
	LTE uint64
	GTE uint64

	// Bitwise
	AND uint64
	OR  uint64
	XOR uint64
	NOT uint64
	SHL uint64
	SHR uint64

	// Stack
	PUSH   uint64 // covers PUSH1..PUSH32
	POP    uint64
	DUP    uint64 // covers DUP1..DUP16
	SWAP   uint64 // covers SWAP1..SWAP16

	// Memory
	MLOAD      uint64
	MSTORE     uint64
	MSTORE8    uint64
	MSIZE      uint64
	MemPerWord uint64

	// Storage — TOTAL cost (base=1, engine adds SLOAD-1 as extra)
	SLOAD  uint64
	SSTORE uint64

	// Control flow
	JUMP     uint64
	JUMPI    uint64
	JUMPDEST uint64
	PC       uint64
	STOP     uint64

	// Calldata / code
	CALLDATALOAD  uint64
	CALLDATASIZE  uint64
	CALLDATACOPY  uint64
	CODECOPY      uint64
	RETURNDATASIZE uint64
	RETURNDATACOPY uint64

	// Context (cheap reads)
	CALLER      uint64
	ORIGIN      uint64
	CALLVALUE   uint64
	CODESIZE    uint64
	BLOCKHEIGHT uint64
	TIMESTAMP   uint64
	ADDRESS     uint64
	GASLIMIT    uint64

	// Crypto (quantum-safe)
	SHA3             uint64
	SHA3PerByte      uint64
	VERIFY_MLDSA uint64

	// Events
	LOG0        uint64
	LOGPerTopic uint64
	LOGPerByte  uint64

	// Token — TOTAL cost (base=1, engine adds TRANSFER-1 as extra)
	TRANSFER uint64
	BALANCE  uint64
	TOTALGAS uint64

	// Contract
	CREATE       uint64
	SELFDESTRUCT uint64
	CALL         uint64
	STATICCALL   uint64
	DELEGATECALL uint64
	CREATE2      uint64

	// ABI

	// Return / Revert
	RETURN  uint64
	REVERT  uint64
	REQUIRE uint64
}

// DefaultGasTable returns production-calibrated gas costs for SQC-VM v3.
func DefaultGasTable() GasTable {
	return GasTable{
		// Arithmetic
		ADD:        3,
		SUB:        3,
		MUL:        5,
		DIV:        5,
		MOD:        5,
		EXP:        10,
		ADDMOD:     8,
		MULMOD:     8,
		ISZERO:     3,
		BYTE:       3,
		SIGNEXTEND: 5,
		ExpPerBit:  3,

		// Comparison
		EQ:  3,
		NEQ: 3,
		LT:  3,
		GT:  3,
		LTE: 3,
		GTE: 3,

		// Bitwise
		AND: 3,
		OR:  3,
		XOR: 3,
		NOT: 3,
		SHL: 3,
		SHR: 3,

		// Stack
		PUSH: 3,
		POP:  2,
		DUP:  3,
		SWAP: 3,

		// Memory
		MLOAD:      3,
		MSTORE:     3,
		MSTORE8:    3,
		MSIZE:      2,
		MemPerWord: 3,

		// Storage — TOTAL cost (base 1 is already included via GasOf returning 1 for unknown/base)
		// Engine does: useGas(SLOAD - baseCost) where baseCost = GasOf(op) = 1
		// So effective SLOAD cost = 1 + (100 - 1) = 100
		SLOAD:  100,
		SSTORE: 20_000,

		// Control flow
		JUMP:     8,
		JUMPI:    10,
		JUMPDEST: 1,
		PC:       2,
		STOP:     0,

		// Calldata / code
		CALLDATALOAD:   3,
		CALLDATASIZE:   2,
		CALLDATACOPY:   3,
		CODECOPY:       3,
		RETURNDATASIZE: 2,
		RETURNDATACOPY: 3,

		// Context reads
		CALLER:      2,
		ORIGIN:      2,
		CALLVALUE:   2,
		CODESIZE:    2,
		BLOCKHEIGHT: 2,
		TIMESTAMP:   2,
		ADDRESS:     2,
		GASLIMIT:    2,

		// Crypto
		SHA3:             30,
		SHA3PerByte:      6,
		VERIFY_MLDSA: 5000,

		// Events
		LOG0:        375,
		LOGPerTopic: 375,
		LOGPerByte:  8,

		// Token
		TRANSFER: 9000,
		BALANCE:  400,
		TOTALGAS: 2,

		// Contract
		CREATE:       32_000,
		SELFDESTRUCT: 5_000,
		CALL:         700,
		STATICCALL:   700,
		DELEGATECALL: 700,
		CREATE2:      32_000,

		// ABI

		// Return
		RETURN:  0,
		REVERT:  0,
		REQUIRE: 3,
	}
}

// TestnetGasTable returns 5x cheaper costs for development/testing.
func TestnetGasTable() GasTable {
	t := DefaultGasTable()
	divideAll(&t, 5)
	// Keep minimums for critical ops
	t.SLOAD = 20
	t.SSTORE = 4_000
	t.VERIFY_MLDSA = 1_000
	t.CREATE = 6_400
	t.TRANSFER = 1_800
	return t
}

// FreeGasTable returns zero-cost gas (for unit testing only).
func FreeGasTable() GasTable {
	return GasTable{}
}

// GasOf returns the base cost of an opcode.
// For PUSH1-32, DUP1-16, SWAP1-16 the base PUSH/DUP/SWAP cost is returned.
// Returns 1 for any unknown opcode (never free).
//
// The range tests below are disjoint from every single-byte case. In the v3
// table the PUSH range (0x52–0x70) was tested first and overlapped SLOAD,
// SSTORE, JUMP, CALLDATALOAD and ten others, so those cases were unreachable
// and SSTORE was billed 3 gas instead of 20000 — unmetered persistent storage.
func (g *GasTable) GasOf(op byte) uint64 {
	switch {
	// PUSH1–PUSH32 (0x60–0x7F)
	case op >= OpPUSH1 && op <= OpPUSH32:
		return g.PUSH
	// DUP1–DUP16 (0x80–0x8F)
	case op >= OpDUP1 && op <= OpDUP16:
		return g.DUP
	// SWAP1–SWAP16 (0x90–0x9F)
	case op >= OpSWAP1 && op <= OpSWAP16:
		return g.SWAP
	}

	switch op {
	case OpSTOP: return g.STOP
	case OpADD: return g.ADD
	case OpSUB: return g.SUB
	case OpMUL: return g.MUL
	case OpDIV: return g.DIV
	case OpMOD: return g.MOD
	case OpEXP: return g.EXP
	case OpADDMOD: return g.ADDMOD
	case OpMULMOD: return g.MULMOD
	case OpISZERO: return g.ISZERO
	case OpBYTE: return g.BYTE
	case OpSIGNEXTEND: return g.SIGNEXTEND
	case OpEQ: return g.EQ
	case OpNEQ: return g.NEQ
	case OpLT: return g.LT
	case OpGT: return g.GT
	case OpLTE: return g.LTE
	case OpGTE: return g.GTE
	case OpAND: return g.AND
	case OpOR: return g.OR
	case OpXOR: return g.XOR
	case OpNOT: return g.NOT
	case OpSHL: return g.SHL
	case OpSHR: return g.SHR
	case OpSHA3: return g.SHA3
	case OpSHA3_MEM: return g.SHA3
	case OpVERIFY_MLDSA: return 1 // base=1; engine adds (VERIFY_MLDSA-1) extra
	case OpADDRESS: return g.ADDRESS
	case OpBALANCE: return 1 // base=1; engine adds (BALANCE-1) extra
	case OpORIGIN: return g.ORIGIN
	case OpCALLER: return g.CALLER
	case OpCALLVALUE: return g.CALLVALUE
	case OpCALLDATALOAD: return g.CALLDATALOAD
	case OpCALLDATASIZE: return g.CALLDATASIZE
	case OpCALLDATACOPY: return g.CALLDATACOPY
	case OpCODESIZE: return g.CODESIZE
	case OpCODECOPY: return g.CODECOPY
	case OpRETURNDATASIZE: return g.RETURNDATASIZE
	case OpRETURNDATACOPY: return g.RETURNDATACOPY
	case OpTRANSFER: return 1 // base=1; engine adds (TRANSFER-1) extra
	case OpBLOCKHEIGHT: return g.BLOCKHEIGHT
	case OpTIMESTAMP: return g.TIMESTAMP
	case OpGASLIMIT: return g.GASLIMIT
	case OpPOP: return g.POP
	case OpMLOAD: return g.MLOAD
	case OpMSTORE: return g.MSTORE
	case OpMSTORE8: return g.MSTORE8
	case OpSLOAD: return 1 // base=1; engine adds (SLOAD-1) extra
	case OpSSTORE: return 1 // base=1; engine adds (SSTORE-1) extra
	case OpJUMP: return g.JUMP
	case OpJUMPI: return g.JUMPI
	case OpPC: return g.PC
	case OpMSIZE: return g.MSIZE
	case OpTOTALGAS: return g.TOTALGAS
	case OpJUMPDEST: return g.JUMPDEST
	case OpLOG0: return g.LOG0
	case OpLOG1: return g.LOG0 + g.LOGPerTopic
	case OpLOG2: return g.LOG0 + g.LOGPerTopic*2
	case OpLOG3: return g.LOG0 + g.LOGPerTopic*3
	case OpLOG4: return g.LOG0 + g.LOGPerTopic*4
	case OpCREATE: return g.CREATE
	case OpCALL: return g.CALL
	case OpRETURN: return g.RETURN
	case OpDELEGATECALL: return g.DELEGATECALL
	case OpCREATE2: return g.CREATE2
	case OpSTATICCALL: return g.STATICCALL
	case OpREVERT: return g.REVERT
	case OpREQUIRE: return g.REQUIRE
	case OpSELFDESTRUCT: return g.SELFDESTRUCT
	default: return 1
	}
}

// divideAll divides all non-zero gas costs by divisor (ceiling division, minimum 1).
func divideAll(t *GasTable, divisor uint64) {
	fields := []*uint64{
		&t.ADD, &t.SUB, &t.MUL, &t.DIV, &t.MOD, &t.EXP, &t.ADDMOD, &t.MULMOD,
		&t.ISZERO, &t.BYTE, &t.SIGNEXTEND,
		&t.EQ, &t.NEQ, &t.LT, &t.GT, &t.LTE, &t.GTE,
		&t.AND, &t.OR, &t.XOR, &t.NOT, &t.SHL, &t.SHR,
		&t.PUSH, &t.POP, &t.DUP, &t.SWAP,
		&t.MLOAD, &t.MSTORE, &t.MSTORE8, &t.MSIZE, &t.MemPerWord,
		&t.SLOAD, &t.SSTORE,
		&t.JUMP, &t.JUMPI,
		&t.CALLDATALOAD, &t.CALLDATASIZE, &t.CALLDATACOPY,
		&t.CODECOPY, &t.RETURNDATASIZE, &t.RETURNDATACOPY,
		&t.CALLER, &t.ORIGIN, &t.CALLVALUE, &t.CODESIZE,
		&t.BLOCKHEIGHT, &t.TIMESTAMP, &t.ADDRESS, &t.GASLIMIT,
		&t.SHA3, &t.SHA3PerByte, &t.VERIFY_MLDSA,
		&t.LOG0, &t.LOGPerTopic, &t.LOGPerByte,
		&t.TRANSFER, &t.BALANCE,
		&t.CREATE, &t.SELFDESTRUCT, &t.CALL, &t.STATICCALL, &t.DELEGATECALL, &t.CREATE2,
		&t.REQUIRE,
	}
	for _, f := range fields {
		if *f > 0 {
			*f = (*f + divisor - 1) / divisor
			if *f == 0 {
				*f = 1
			}
		}
	}
}

// GasEstimate performs a static scan of bytecode and returns an estimated total gas.
// Dynamic costs (SSTORE, EXP, LOG*) use their base values only.
// This is useful for pre-flight checks before executing a contract call.
func GasEstimate(bytecode []byte, table GasTable) uint64 {
	total := uint64(0)
	i := 0
	for i < len(bytecode) {
		op := bytecode[i]
		total += table.GasOf(op)
		// Skip immediate bytes for PUSH opcodes
		if op >= OpPUSH1 && op <= OpPUSH32 {
			i += 1 + int(op-OpPUSH1) + 1
		} else {
			i++
		}
	}
	return total
}
