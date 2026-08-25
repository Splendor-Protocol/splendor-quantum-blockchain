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
	ABI_ENCODE uint64
	ABI_DECODE uint64
	SELECTOR   uint64

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
		ABI_ENCODE: 20,
		ABI_DECODE: 20,
		SELECTOR:   15,

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
// For PUSH1-31, DUP1-16, SWAP1-16 the base PUSH/DUP/SWAP cost is returned.
// Returns 1 for any unknown opcode (never free).
func (g *GasTable) GasOf(op byte) uint64 {
	switch {
	// PUSH1–PUSH31
	case op >= 0x52 && op <= 0x70:
		return g.PUSH
	// PUSH32
	case op == 0x30:
		return g.PUSH
	// DUP1–DUP16
	case op >= 0x32 && op <= 0x41:
		return g.DUP
	// SWAP1–SWAP16
	case op >= 0x42 && op <= 0x51:
		return g.SWAP
	}

	switch op {
	case 0x00: return g.STOP
	case 0x01: return g.ADD
	case 0x02: return g.SUB
	case 0x03: return g.MUL
	case 0x04: return g.DIV
	case 0x05: return g.MOD
	case 0x06: return g.EXP
	case 0x07: return g.ADDMOD
	case 0x08: return g.MULMOD
	case 0x09: return g.ISZERO
	case 0x0A: return g.BYTE
	case 0x0B: return g.SIGNEXTEND
	case 0x10: return g.EQ
	case 0x11: return g.NEQ
	case 0x12: return g.LT
	case 0x13: return g.GT
	case 0x14: return g.LTE
	case 0x15: return g.GTE
	case 0x20: return g.AND
	case 0x21: return g.OR
	case 0x22: return g.XOR
	case 0x23: return g.NOT
	case 0x24: return g.SHL
	case 0x25: return g.SHR
	case 0x31: return g.POP
	case 0x53: return g.MLOAD
	case 0x54: return g.MSTORE
	case 0x55: return 1  // SLOAD base=1; engine adds (SLOAD-1) extra
	case 0x56: return 1  // SSTORE base=1; engine adds (SSTORE-1) extra
	case 0x60: return g.JUMP
	case 0x61: return g.JUMPI
	case 0x62: return g.PC
	case 0x63: return g.JUMPDEST
	case 0x64: return g.CALLDATALOAD
	case 0x65: return g.CALLDATASIZE
	case 0x66: return g.CALLDATACOPY
	case 0x67: return g.CODECOPY
	case 0x68: return g.RETURNDATASIZE
	case 0x69: return g.RETURNDATACOPY
	case 0x70: return g.CALLER
	case 0x71: return g.MSTORE8
	case 0x72: return g.MSIZE
	case 0x73: return g.ORIGIN
	case 0x74: return g.CALLVALUE
	case 0x75: return g.CODESIZE
	case 0x76: return g.BLOCKHEIGHT
	case 0x77: return g.TIMESTAMP
	case 0x78: return g.ADDRESS
	case 0x79: return g.GASLIMIT
	case 0x80: return g.SHA3
	case 0x81: return 1  // VERIFY_MLDSA base=1; engine adds (VERIFY_MLDSA-1) extra
	case 0x82: return g.SHA3
	case 0x90: return g.CALL
	case 0x91: return g.STATICCALL
	case 0x92: return g.DELEGATECALL
	case 0x93: return g.CREATE
	case 0x94: return g.SELFDESTRUCT
	case 0x95: return g.CREATE2
	case 0xA0: return g.LOG0
	case 0xA1: return g.LOG0 + g.LOGPerTopic
	case 0xA2: return g.LOG0 + g.LOGPerTopic*2
	case 0xA3: return g.LOG0 + g.LOGPerTopic*3
	case 0xA4: return g.LOG0 + g.LOGPerTopic*4
	case 0xB0: return 1  // TRANSFER base=1; engine adds (TRANSFER-1) extra
	case 0xB1: return 1  // BALANCE base=1; engine adds (BALANCE-1) extra
	case 0xB2: return g.TOTALGAS
	case 0xC0: return g.ABI_ENCODE
	case 0xC1: return g.ABI_DECODE
	case 0xC2: return g.SELECTOR
	case 0xF0: return g.RETURN
	case 0xF1: return g.REVERT
	case 0xF2: return g.REQUIRE
	default:   return 1
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
		&t.REQUIRE, &t.ABI_ENCODE, &t.ABI_DECODE, &t.SELECTOR,
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
		if op == 0x30 { // PUSH32
			i += 33
		} else if op >= 0x52 && op <= 0x70 { // PUSH1..PUSH31
			n := int(op-0x52) + 1
			i += 1 + n
		} else {
			i++
		}
	}
	return total
}
