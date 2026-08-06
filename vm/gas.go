// Package vm — Configurable Gas Table for SQC-VM.
//
// Gas is the unit of computational work in SQC-VM. Every opcode costs gas.
// The gas table is configurable: operators can tune costs via the node config,
// or governance can update costs as the chain evolves.
//
// Gas design principles:
//   - Simple operations (ADD, MUL) cost 1–5 gas
//   - Storage operations (SLOAD, SSTORE) cost 100–20000 gas (disk I/O is expensive)
//   - Crypto operations (SHA3, VERIFY_DILITHIUM) cost proportionally to their compute
//   - Contract creation (CREATE) costs 32000 gas (expensive by design)
//   - Memory operations scale with memory size (prevents memory abuse)
//   - Token transfers (TRANSFER) cost 9000 gas (analogous to Ethereum's ETH transfer)
//
// The GasTable is passed to the Engine at construction time, making it
// easy to test with different costs or apply governance changes without
// changing the core execution logic.
package vm

// GasTable holds the gas cost for every opcode.
// Zero values fall back to DefaultGasTable.
type GasTable struct {
	// Arithmetic
	ADD     uint64
	SUB     uint64
	MUL     uint64
	DIV     uint64
	MOD     uint64
	EXP     uint64
	ADDMOD  uint64
	MULMOD  uint64
	ExpPerBit uint64 // additional gas per bit of exponent in EXP

	// Comparison (all cheap)
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
	PUSH32 uint64
	POP    uint64
	DUP    uint64
	SWAP   uint64

	// Memory
	MLOAD  uint64
	MSTORE uint64
	MSIZE  uint64
	MemPerWord uint64 // additional gas per 32-byte word allocated

	// Storage — these are the most expensive ops
	SLOAD  uint64
	SSTORE uint64

	// Control flow
	JUMP  uint64
	JUMPI uint64
	PC    uint64
	STOP  uint64

	// Context (cheap reads)
	CALLER      uint64
	ORIGIN      uint64
	CALLVALUE   uint64
	CODESIZE    uint64
	BLOCKHEIGHT uint64
	TIMESTAMP   uint64
	ADDRESS     uint64

	// Crypto — expensive
	SHA3              uint64
	SHA3PerByte       uint64 // additional gas per input byte
	VERIFY_DILITHIUM  uint64

	// Events (LOG) — moderate cost
	LOG0        uint64
	LOGPerTopic uint64
	LOGPerByte  uint64

	// Token
	TRANSFER uint64
	BALANCE  uint64
	TOTALGAS uint64

	// Contract interaction
	CREATE       uint64
	SELFDESTRUCT uint64
	CALL         uint64
	STATICCALL   uint64
	DELEGATECALL uint64

	// Return / Revert
	RETURN  uint64
	REVERT  uint64
	REQUIRE uint64

	// ABI
	ABI_ENCODE uint64
	ABI_DECODE uint64
	SELECTOR   uint64
}

// DefaultGasTable returns production-calibrated gas costs.
// These are inspired by Ethereum's gas schedule but tuned for SQC-VM:
//   - Dilithium verification is more expensive than ECDSA ecrecover
//   - Storage is equally expensive (disk I/O dominates)
//   - Simple arithmetic is cheaper (no EVM word-size overhead)
func DefaultGasTable() GasTable {
	return GasTable{
		// Arithmetic
		ADD:       3,
		SUB:       3,
		MUL:       5,
		DIV:       5,
		MOD:       5,
		EXP:       10,
		ADDMOD:    8,
		MULMOD:    8,
		ExpPerBit: 3,

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
		PUSH32: 3,
		POP:    2,
		DUP:    3,
		SWAP:   3,

		// Memory (relatively cheap, but grows with usage)
		MLOAD:      3,
		MSTORE:     3,
		MSIZE:      2,
		MemPerWord: 3,

		// Storage (EXPENSIVE — disk I/O)
		SLOAD:  100,
		SSTORE: 20_000,

		// Control flow
		JUMP:  8,
		JUMPI: 10,
		PC:    2,
		STOP:  0,

		// Context reads (cheap)
		CALLER:      2,
		ORIGIN:      2,
		CALLVALUE:   2,
		CODESIZE:    2,
		BLOCKHEIGHT: 2,
		TIMESTAMP:   2,
		ADDRESS:     2,

		// Crypto
		SHA3:             30,
		SHA3PerByte:      6,
		VERIFY_DILITHIUM: 5000, // Dilithium verification is expensive — ~3x ecrecover cost

		// Events
		LOG0:        375,
		LOGPerTopic: 375,
		LOGPerByte:  8,

		// Token operations
		TRANSFER: 9000, // analogous to ETH transfer cost
		BALANCE:  400,
		TOTALGAS: 2,

		// Contract operations
		CREATE:       32_000,
		SELFDESTRUCT: 5_000,
		CALL:         700,
		STATICCALL:   700,
		DELEGATECALL: 700,

		// Return / Revert
		RETURN:  0,
		REVERT:  0,
		REQUIRE: 3,

		// ABI
		ABI_ENCODE: 20,
		ABI_DECODE: 20,
		SELECTOR:   15,
	}
}

// TestnetGasTable returns cheaper gas costs for development/testnet.
// All costs are 10x cheaper than mainnet, making it easier to test contracts
// without burning through testnet funds.
func TestnetGasTable() GasTable {
	t := DefaultGasTable()
	// Halve all costs for testnet
	divideAll(&t, 5)
	// But keep a minimum of 1 for everything non-zero
	t.SLOAD = 20
	t.SSTORE = 4000
	t.VERIFY_DILITHIUM = 1000
	t.CREATE = 6400
	t.TRANSFER = 1800
	return t
}

// FreeGasTable returns zero-cost gas for testing purposes only.
// DO NOT use in production — this removes all gas limits.
func FreeGasTable() GasTable {
	return GasTable{} // all zeros
}

// divideAll divides all non-zero gas costs by divisor.
func divideAll(t *GasTable, divisor uint64) {
	fields := []*uint64{
		&t.ADD, &t.SUB, &t.MUL, &t.DIV, &t.MOD, &t.EXP, &t.ADDMOD, &t.MULMOD,
		&t.EQ, &t.NEQ, &t.LT, &t.GT, &t.LTE, &t.GTE,
		&t.AND, &t.OR, &t.XOR, &t.NOT, &t.SHL, &t.SHR,
		&t.PUSH32, &t.POP, &t.DUP, &t.SWAP,
		&t.MLOAD, &t.MSTORE, &t.MSIZE, &t.MemPerWord,
		&t.SLOAD, &t.SSTORE,
		&t.JUMP, &t.JUMPI, &t.PC,
		&t.CALLER, &t.ORIGIN, &t.CALLVALUE, &t.CODESIZE, &t.BLOCKHEIGHT, &t.TIMESTAMP, &t.ADDRESS,
		&t.SHA3, &t.SHA3PerByte, &t.VERIFY_DILITHIUM,
		&t.LOG0, &t.LOGPerTopic, &t.LOGPerByte,
		&t.TRANSFER, &t.BALANCE,
		&t.CREATE, &t.SELFDESTRUCT, &t.CALL, &t.STATICCALL, &t.DELEGATECALL,
		&t.REQUIRE,
		&t.ABI_ENCODE, &t.ABI_DECODE, &t.SELECTOR,
	}
	for _, f := range fields {
		if *f > 0 {
			*f = (*f + divisor - 1) / divisor // ceiling division, minimum 1
			if *f == 0 {
				*f = 1
			}
		}
	}
}

// GasOf returns the base gas cost of an opcode from the table.
// If the table has zero for an opcode, it returns the default cost.
func (g *GasTable) GasOf(op byte) uint64 {
	def := DefaultGasTable()
	cost := g.lookupOp(op)
	if cost == 0 {
		cost = def.lookupOp(op)
	}
	return cost
}

// lookupOp maps an opcode byte to its table entry.
func (g *GasTable) lookupOp(op byte) uint64 {
	switch op {
	case OpADD:
		return g.ADD
	case OpSUB:
		return g.SUB
	case OpMUL:
		return g.MUL
	case OpDIV:
		return g.DIV
	case OpMOD:
		return g.MOD
	case OpEXP:
		return g.EXP
	case OpADDMOD:
		return g.ADDMOD
	case OpMULMOD:
		return g.MULMOD
	case OpEQ:
		return g.EQ
	case OpNEQ:
		return g.NEQ
	case OpLT:
		return g.LT
	case OpGT:
		return g.GT
	case OpLTE:
		return g.LTE
	case OpGTE:
		return g.GTE
	case OpAND:
		return g.AND
	case OpOR:
		return g.OR
	case OpXOR:
		return g.XOR
	case OpNOT:
		return g.NOT
	case OpSHL:
		return g.SHL
	case OpSHR:
		return g.SHR
	case OpPUSH32:
		return g.PUSH32
	case OpPOP:
		return g.POP
	case OpDUP1, OpDUP2:
		return g.DUP
	case OpSWAP1, OpSWAP2:
		return g.SWAP
	case OpMLOAD:
		return g.MLOAD
	case OpMSTORE:
		return g.MSTORE
	case OpMSIZE:
		return g.MSIZE
	case OpSLOAD:
		return g.SLOAD
	case OpSSTORE:
		return g.SSTORE
	case OpJUMP:
		return g.JUMP
	case OpJUMPI:
		return g.JUMPI
	case OpPC:
		return g.PC
	case OpSTOP:
		return g.STOP
	case OpCALLER:
		return g.CALLER
	case OpORIGIN:
		return g.ORIGIN
	case OpCALLVALUE:
		return g.CALLVALUE
	case OpCODESIZE:
		return g.CODESIZE
	case OpBLOCKHEIGHT:
		return g.BLOCKHEIGHT
	case OpTIMESTAMP:
		return g.TIMESTAMP
	case OpADDRESS:
		return g.ADDRESS
	case OpSHA3:
		return g.SHA3
	case OpVERIFY_DILITHIUM:
		return g.VERIFY_DILITHIUM
	case OpLOG0:
		return g.LOG0
	case OpLOG1:
		return g.LOG0 + g.LOGPerTopic
	case OpLOG2:
		return g.LOG0 + g.LOGPerTopic*2
	case OpLOG3:
		return g.LOG0 + g.LOGPerTopic*3
	case OpLOG4:
		return g.LOG0 + g.LOGPerTopic*4
	case OpTRANSFER:
		return g.TRANSFER
	case OpBALANCE:
		return g.BALANCE
	case OpTOTALGAS:
		return g.TOTALGAS
	case OpCREATE:
		return g.CREATE
	case OpSELFDESTRUCT:
		return g.SELFDESTRUCT
	case OpCALL:
		return g.CALL
	case OpSTATICCALL:
		return g.STATICCALL
	case OpDELEGATECALL:
		return g.DELEGATECALL
	case OpRETURN:
		return g.RETURN
	case OpREVERT:
		return g.REVERT
	case OpREQUIRE:
		return g.REQUIRE
	case OpABI_ENCODE:
		return g.ABI_ENCODE
	case OpABI_DECODE:
		return g.ABI_DECODE
	case OpSELECTOR:
		return g.SELECTOR
	default:
		return 1 // unknown opcodes cost 1 gas (never free)
	}
}

// GasEstimate estimates the gas cost of executing a bytecode sequence.
// It does NOT execute the code — it scans statically and sums opcode costs.
// This is an approximation: dynamic costs (SSTORE, EXP, LOG) use base costs only.
func GasEstimate(bytecode []byte, table GasTable) uint64 {
	total := uint64(0)
	for i := 0; i < len(bytecode); {
		op := bytecode[i]
		total += table.GasOf(op)
		if op == OpPUSH32 {
			i += 33 // skip 32 data bytes
		} else {
			i++
		}
	}
	return total
}
