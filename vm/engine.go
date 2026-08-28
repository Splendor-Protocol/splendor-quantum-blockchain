// Package vm implements the Splendor Quantum Chain smart contract execution engine.
//
// # SQC-VM v4
//
// This is NOT an EVM. It is a quantum-native VM using only NIST post-quantum
// algorithms: ML-DSA-65 (FIPS 204) for signatures, SHA3-256 (FIPS 202)
// for all hashing. No ECDSA. No keccak. No secp256k1.
//
// # Why v4 replaces the v3 encoding
//
// v3 assigned PUSH1..PUSH31 to 0x52..0x70, a range that also held MLOAD, MSTORE,
// MSTORE8, SLOAD, SSTORE, JUMP, JUMPI, PC, JUMPDEST, CALLDATALOAD/SIZE/COPY,
// CODECOPY, RETURNDATASIZE/COPY and CALLER. The interpreter matched the PUSH
// range first, so all sixteen of those instructions were unreachable — SSTORE
// (0x56) decoded as PUSH5 and silently consumed the following five bytes as
// immediate data instead of writing storage. The same shadowing in GasOf priced
// SSTORE at 3 gas instead of 20000. Contract storage, branching and calldata
// were therefore inaccessible from bytecode, and no gas metering applied to
// them.
//
// v4 gives PUSH, DUP and SWAP the contiguous, non-overlapping blocks they need
// (0x60-0x7F, 0x80-0x8F, 0x90-0x9F) and relocates everything they used to
// shadow. Ranges are disjoint by construction, and TestOpcodeTableHasNoCollisions
// asserts that no byte is claimed twice. Where a name exists in the EVM the
// numbering matches it, so the table reads the way an auditor expects.
//
// The encoding is not compatible with v3, so a chain carrying v3 bytecode
// contracts must be re-genesised rather than upgraded in place.
//
// # Instruction set (v4)
//
//	0x00       STOP
//	0x01-0x0B  ADD SUB MUL DIV MOD EXP ADDMOD MULMOD ISZERO BYTE SIGNEXTEND
//	0x10-0x15  EQ NEQ LT GT LTE GTE
//	0x20-0x25  AND OR XOR NOT SHL SHR
//	0x30-0x32  SHA3 SHA3_MEM VERIFY_MLDSA
//	0x33-0x3F  ADDRESS BALANCE ORIGIN CALLER CALLVALUE CALLDATALOAD CALLDATASIZE
//	           CALLDATACOPY CODESIZE CODECOPY RETURNDATASIZE RETURNDATACOPY TRANSFER
//	0x40-0x45  BLOCKHEIGHT TIMESTAMP GASLIMIT ABI_ENCODE ABI_DECODE SELECTOR
//	0x50-0x5B  POP MLOAD MSTORE MSTORE8 SLOAD SSTORE JUMP JUMPI PC MSIZE TOTALGAS JUMPDEST
//	0x60-0x7F  PUSH1-PUSH32
//	0x80-0x8F  DUP1-DUP16
//	0x90-0x9F  SWAP1-SWAP16
//	0xA0-0xA4  LOG0-LOG4
//	0xF0-0xF5  CREATE CALL — RETURN DELEGATECALL CREATE2
//	0xFA       STATICCALL
//	0xFD-0xFF  REVERT REQUIRE SELFDESTRUCT
package vm

import (
	"encoding/binary"
	"fmt"
	"math/big"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// ── Opcode constants (v4) ─────────────────────────────────────────────────────
//
// Every range below is disjoint. PUSH/DUP/SWAP occupy whole aligned blocks so a
// range test can never shadow a single-byte instruction, which is exactly how
// the v3 table lost storage, branching and calldata.

const (
	// ── Arithmetic (0x00–0x0B) ───────────────────────────────────────────
	OpSTOP       byte = 0x00
	OpADD        byte = 0x01
	OpSUB        byte = 0x02
	OpMUL        byte = 0x03
	OpDIV        byte = 0x04
	OpMOD        byte = 0x05
	OpEXP        byte = 0x06
	OpADDMOD     byte = 0x07
	OpMULMOD     byte = 0x08
	OpISZERO     byte = 0x09
	OpBYTE       byte = 0x0A
	OpSIGNEXTEND byte = 0x0B

	// ── Comparison (0x10–0x15) ───────────────────────────────────────────
	OpEQ  byte = 0x10
	OpNEQ byte = 0x11
	OpLT  byte = 0x12
	OpGT  byte = 0x13
	OpLTE byte = 0x14
	OpGTE byte = 0x15

	// ── Bitwise (0x20–0x25) ──────────────────────────────────────────────
	OpAND byte = 0x20
	OpOR  byte = 0x21
	OpXOR byte = 0x22
	OpNOT byte = 0x23
	OpSHL byte = 0x24
	OpSHR byte = 0x25

	// ── Crypto, quantum-safe only (0x30–0x32) ────────────────────────────
	OpSHA3         byte = 0x30
	OpSHA3_MEM     byte = 0x31 // SHA3-256 over a memory range
	OpVERIFY_MLDSA byte = 0x32

	// ── Environment & calldata (0x33–0x3F) ───────────────────────────────
	OpADDRESS        byte = 0x33
	OpBALANCE        byte = 0x34
	OpORIGIN         byte = 0x35
	OpCALLER         byte = 0x36
	OpCALLVALUE      byte = 0x37
	OpCALLDATALOAD   byte = 0x38
	OpCALLDATASIZE   byte = 0x39
	OpCALLDATACOPY   byte = 0x3A
	OpCODESIZE       byte = 0x3B
	OpCODECOPY       byte = 0x3C
	OpRETURNDATASIZE byte = 0x3D
	OpRETURNDATACOPY byte = 0x3E
	OpTRANSFER       byte = 0x3F

	// ── Block context & ABI helpers (0x40–0x45) ──────────────────────────
	OpBLOCKHEIGHT byte = 0x40
	OpTIMESTAMP   byte = 0x41
	OpGASLIMIT    byte = 0x42
	OpABI_ENCODE  byte = 0x43
	OpABI_DECODE  byte = 0x44
	OpSELECTOR    byte = 0x45

	// ── Memory, storage, control flow (0x50–0x5B) ────────────────────────
	OpPOP      byte = 0x50
	OpMLOAD    byte = 0x51
	OpMSTORE   byte = 0x52
	OpMSTORE8  byte = 0x53
	OpSLOAD    byte = 0x54
	OpSSTORE   byte = 0x55
	OpJUMP     byte = 0x56
	OpJUMPI    byte = 0x57
	OpPC       byte = 0x58
	OpMSIZE    byte = 0x59
	OpTOTALGAS byte = 0x5A
	OpJUMPDEST byte = 0x5B

	// ── PUSH1–PUSH32 (0x60–0x7F) ─────────────────────────────────────────
	OpPUSH1  byte = 0x60
	OpPUSH32 byte = 0x7F

	// ── DUP1–DUP16 (0x80–0x8F) ───────────────────────────────────────────
	OpDUP1  byte = 0x80
	OpDUP16 byte = 0x8F

	// ── SWAP1–SWAP16 (0x90–0x9F) ─────────────────────────────────────────
	OpSWAP1  byte = 0x90
	OpSWAP16 byte = 0x9F

	// ── Events (0xA0–0xA4) ───────────────────────────────────────────────
	OpLOG0 byte = 0xA0
	OpLOG1 byte = 0xA1
	OpLOG2 byte = 0xA2
	OpLOG3 byte = 0xA3
	OpLOG4 byte = 0xA4

	// ── Contract interaction & halting (0xF0–0xFF) ───────────────────────
	OpCREATE       byte = 0xF0
	OpCALL         byte = 0xF1
	OpRETURN       byte = 0xF3
	OpDELEGATECALL byte = 0xF4
	OpCREATE2      byte = 0xF5
	OpSTATICCALL   byte = 0xFA
	OpREVERT       byte = 0xFD
	OpREQUIRE      byte = 0xFE
	OpSELFDESTRUCT byte = 0xFF
)

// ── VM limits ─────────────────────────────────────────────────────────────────

const (
	MaxStackDepth  = 1024
	MaxGasPerCall  = 10_000_000
	MaxMemoryBytes = 32 * 1024 * 1024
	MaxCallDepth   = 64

	// WordBits is the machine width. Every stack value is reduced modulo
	// 2^WordBits, so no operand or result can grow without bound.
	WordBits = 256
)

// two256 is 2^256: the modulus every stack value is reduced by.
var two256 = new(big.Int).Lsh(big.NewInt(1), WordBits)

// max256 is 2^256 - 1, used for bitwise NOT.
var max256 = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), WordBits), big.NewInt(1))

// wrap256 reduces v into [0, 2^256). Go's Mod is Euclidean, so a negative
// intermediate (from SUB) comes back as its two's-complement representation.
func wrap256(v *big.Int) *big.Int {
	if v.Sign() >= 0 && v.BitLen() <= WordBits {
		return v
	}
	return v.Mod(v, two256)
}

// ── Execution context ─────────────────────────────────────────────────────────

type ExecutionContext struct {
	Caller      qcrypto.Address
	Origin      qcrypto.Address
	Contract    qcrypto.Address
	CallValue   *big.Int
	Input       []byte // calldata
	Code        []byte
	BlockHeight uint64
	Timestamp   int64
	Depth       int
	Gas         uint64
	Logs        []*types.Log
	IsStatic    bool
	// Return data from most recent sub-call
	returnData []byte
}

type ExecutionResult struct {
	ReturnValue []byte
	GasUsed     uint64
	Logs        []*types.Log
	Err         error
	Reverted    bool
}

// ── Stack ─────────────────────────────────────────────────────────────────────

type stack struct{ items []*big.Int }

func newStack() *stack { return &stack{items: make([]*big.Int, 0, 64)} }

// push stores v reduced to a 256-bit word. Wrapping HERE is what bounds VM
// arithmetic: ADD/SUB/MUL/SHL operands are therefore always < 2^256, so no
// sequence of instructions can grow a big.Int without limit. Before this, 24
// bytes of `DUP1 MUL` — about 120 gas — squared a 256-bit value repeatedly and
// never terminated.
func (s *stack) push(v *big.Int) error {
	if len(s.items) >= MaxStackDepth {
		return fmt.Errorf("stack overflow")
	}
	s.items = append(s.items, wrap256(new(big.Int).Set(v)))
	return nil
}

func (s *stack) pop() (*big.Int, error) {
	if len(s.items) == 0 {
		return nil, fmt.Errorf("stack underflow")
	}
	v := s.items[len(s.items)-1]
	s.items = s.items[:len(s.items)-1]
	return v, nil
}

func (s *stack) peek() (*big.Int, error) {
	if len(s.items) == 0 {
		return nil, fmt.Errorf("stack underflow (peek)")
	}
	return s.items[len(s.items)-1], nil
}

func (s *stack) peekN(n int) (*big.Int, error) {
	if len(s.items) <= n {
		return nil, fmt.Errorf("stack underflow (peekN %d)", n)
	}
	return s.items[len(s.items)-1-n], nil
}

func (s *stack) len() int { return len(s.items) }

// ── Memory ────────────────────────────────────────────────────────────────────

type memory struct{ data []byte }

func newMemory() *memory { return &memory{} }

// store writes data at offset, growing memory as needed. offset and size are
// derived from 256-bit stack words, so both are validated before any allocation:
// a truncated negative offset would panic the slice copy and a huge one would
// allocate gigabytes.
func (m *memory) store(offset, size int, data []byte) error {
	if offset < 0 || size < 0 || offset > MaxMemoryBytes || size > MaxMemoryBytes {
		return fmt.Errorf("memory store out of range: offset=%d size=%d", offset, size)
	}
	end := offset + size
	if end > MaxMemoryBytes {
		return fmt.Errorf("memory limit exceeded")
	}
	if end > len(m.data) {
		m.data = append(m.data, make([]byte, end-len(m.data))...)
	}
	copy(m.data[offset:end], data)
	return nil
}

func (m *memory) storeByte(offset int, b byte) error {
	if offset < 0 || offset >= MaxMemoryBytes {
		return fmt.Errorf("memory store out of range: offset=%d", offset)
	}
	if offset+1 > len(m.data) {
		m.data = append(m.data, make([]byte, offset+1-len(m.data))...)
	}
	m.data[offset] = b
	return nil
}

// wordsFor returns the number of 32-byte words spanned by n bytes, for
// memory-expansion gas.
func wordsFor(n int) uint64 {
	if n <= 0 {
		return 0
	}
	return uint64((n + 31) / 32)
}

// memOperand converts a 256-bit stack word into an in-range memory offset or
// length. Values wider than the memory ceiling are rejected rather than
// silently truncated by Int64().
func memOperand(v *big.Int) (int, error) {
	if v.Sign() < 0 || v.BitLen() > 63 || v.Int64() > MaxMemoryBytes {
		return 0, fmt.Errorf("memory operand out of range: %s", v)
	}
	return int(v.Int64()), nil
}

// popMemRange pops the (offset, size) pair used by LOG*, RETURN, REVERT and
// SHA3_MEM, validating both against the memory ceiling. Stack order is size
// then offset.
func popMemRange(stk *stack) (offset, size int, err error) {
	sizeV, _ := stk.pop()
	offsetV, err := stk.pop()
	if err != nil {
		return 0, 0, err
	}
	if size, err = memOperand(sizeV); err != nil {
		return 0, 0, err
	}
	if offset, err = memOperand(offsetV); err != nil {
		return 0, 0, err
	}
	return offset, size, nil
}

// popMemRangeReversed pops an (offset, size) pair pushed in offset-then-size
// order, as CREATE does.
func popMemRangeReversed(stk *stack) (offset, size int, err error) {
	offsetV, _ := stk.pop()
	sizeV, err := stk.pop()
	if err != nil {
		return 0, 0, err
	}
	if offset, err = memOperand(offsetV); err != nil {
		return 0, 0, err
	}
	if size, err = memOperand(sizeV); err != nil {
		return 0, 0, err
	}
	return offset, size, nil
}

// copyOperands pops the (dstOffset, srcOffset, size) triple shared by
// CALLDATACOPY, CODECOPY and RETURNDATACOPY, validating each against the memory
// ceiling. Stack order is size, src, dst.
func copyOperands(stk *stack) (dst, src, size int, err error) {
	sizeV, _ := stk.pop()
	srcV, _ := stk.pop()
	dstV, err := stk.pop()
	if err != nil {
		return 0, 0, 0, err
	}
	if size, err = memOperand(sizeV); err != nil {
		return 0, 0, 0, err
	}
	if src, err = memOperand(srcV); err != nil {
		return 0, 0, 0, err
	}
	if dst, err = memOperand(dstV); err != nil {
		return 0, 0, 0, err
	}
	return dst, src, size, nil
}

func (m *memory) load(offset, size int) ([]byte, error) {
	// Reject negative/oversized offsets and sizes BEFORE make(): a 256-bit length
	// truncated by int(bigVal.Int64()) can be negative (→ make panics) or huge
	// (→ a multi-GiB allocation). Bound both to the memory ceiling.
	if offset < 0 || size < 0 || size > MaxMemoryBytes || offset > MaxMemoryBytes {
		return nil, fmt.Errorf("memory load out of range: offset=%d size=%d", offset, size)
	}
	result := make([]byte, size)
	if offset < len(m.data) {
		n := copy(result, m.data[offset:])
		_ = n
	}
	return result, nil
}

func (m *memory) size() int { return len(m.data) }

// ── Engine ────────────────────────────────────────────────────────────────────

// Engine is the SQC-VM bytecode execution engine (v3).
type Engine struct {
	state       *state.StateDB
	gasTable    GasTable
	precompiles *PrecompileRegistry
}

// NewEngine creates a new VM engine with default gas table and precompile registry.
func NewEngine(stateDB *state.StateDB) *Engine {
	return &Engine{
		state:       stateDB,
		gasTable:    DefaultGasTable(),
		precompiles: NewPrecompileRegistry(),
	}
}

// NewEngineWithConfig creates an engine with custom gas table and precompile registry.
func NewEngineWithConfig(stateDB *state.StateDB, gas GasTable, precompiles *PrecompileRegistry) *Engine {
	if precompiles == nil {
		precompiles = NewPrecompileRegistry()
	}
	return &Engine{state: stateDB, gasTable: gas, precompiles: precompiles}
}

// Execute runs SQC bytecode and returns the result.
func (e *Engine) Execute(ctx *ExecutionContext) *ExecutionResult {
	if ctx.Depth > MaxCallDepth {
		return &ExecutionResult{Err: fmt.Errorf("max call depth exceeded")}
	}

	stk := newStack()
	mem := newMemory()
	pc := 0
	gas := ctx.Gas
	if gas == 0 {
		gas = MaxGasPerCall
	}
	gasUsed := uint64(0)
	code := ctx.Code
	var returnData []byte // return data from most recent sub-call

	useGas := func(n uint64) error {
		if gasUsed+n > gas {
			return fmt.Errorf("out of gas (used %d / limit %d)", gasUsed+n, gas)
		}
		gasUsed += n
		return nil
	}

	// chargeMemory bills expansion BEFORE the allocation happens. MemPerWord was
	// present in the gas table but never charged by any opcode, so bytecode
	// could grow memory to the 32 MiB ceiling — and force the same allocation on
	// every validating node — for the base cost of an MSTORE.
	chargeMemory := func(offset, size int) error {
		if size <= 0 {
			return nil
		}
		end := offset + size
		if end <= mem.size() {
			return nil
		}
		return useGas(wordsFor(end-mem.size()) * e.gasTable.MemPerWord)
	}

	for pc < len(code) {
		op := code[pc]
		pc++

		// Use the gas table for base cost
		baseCost := e.gasTable.GasOf(op)
		if err := useGas(baseCost); err != nil {
			return &ExecutionResult{Err: err, GasUsed: gasUsed, Logs: ctx.Logs}
		}

		switch {

		// ── PUSH1–PUSH32 (0x60–0x7F, one contiguous block) ───────────────
		case op >= OpPUSH1 && op <= OpPUSH32:
			n := int(op-OpPUSH1) + 1 // 1..32 bytes
			if pc+n > len(code) {
				return &ExecutionResult{Err: fmt.Errorf("PUSH%d: insufficient bytes", n), GasUsed: gasUsed}
			}
			v := new(big.Int).SetBytes(code[pc : pc+n])
			pc += n
			if err := stk.push(v); err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}

		// ── DUP1–DUP16 ───────────────────────────────────────────────────
		case op >= OpDUP1 && op <= OpDUP16:
			n := int(op - OpDUP1) // depth: DUP1=0 (top), DUP2=1, etc.
			v, err := stk.peekN(n)
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			stk.push(v)

		// ── SWAP1–SWAP16 ─────────────────────────────────────────────────
		case op >= OpSWAP1 && op <= OpSWAP16:
			n := int(op-OpSWAP1) + 1 // SWAP1 swaps top with 2nd (n=1), etc.
			if stk.len() <= n {
				return &ExecutionResult{Err: fmt.Errorf("SWAP%d: need %d items", n, n+1), GasUsed: gasUsed}
			}
			top := stk.len() - 1
			stk.items[top], stk.items[top-n] = stk.items[top-n], stk.items[top]

		// ── STOP ─────────────────────────────────────────────────────────
		case op == OpSTOP:
			return &ExecutionResult{GasUsed: gasUsed, Logs: ctx.Logs}

		// ── Arithmetic ───────────────────────────────────────────────────
		case op == OpADD:
			b, _ := stk.pop()
			a, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			stk.push(new(big.Int).Add(a, b))

		case op == OpSUB:
			b, _ := stk.pop()
			a, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			stk.push(new(big.Int).Sub(a, b))

		case op == OpMUL:
			b, _ := stk.pop()
			a, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			stk.push(new(big.Int).Mul(a, b))

		case op == OpDIV:
			b, _ := stk.pop()
			a, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			if b.Sign() == 0 {
				stk.push(big.NewInt(0))
			} else {
				stk.push(new(big.Int).Div(a, b))
			}

		case op == OpMOD:
			b, _ := stk.pop()
			a, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			if b.Sign() == 0 {
				stk.push(big.NewInt(0))
			} else {
				stk.push(new(big.Int).Mod(a, b))
			}

		case op == OpEXP:
			expV, _ := stk.pop()
			base, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			if err := useGas(uint64(expV.BitLen()) * e.gasTable.ExpPerBit); err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			// Exponentiate MODULO 2^256. Without the modulus, big.Int.Exp
			// computes the full result first: base 3 with a 2^24 exponent cost
			// 91 gas and allocated megabytes, and a larger exponent stalls or
			// OOMs every node executing the block. With it, the intermediate
			// never exceeds one word and the cost is bounded by the exponent's
			// bit length, which is what ExpPerBit already charges for.
			stk.push(new(big.Int).Exp(base, expV, two256))

		case op == OpADDMOD:
			mod, _ := stk.pop()
			b, _ := stk.pop()
			a, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			if mod.Sign() == 0 {
				stk.push(big.NewInt(0))
			} else {
				stk.push(new(big.Int).Mod(new(big.Int).Add(a, b), mod))
			}

		case op == OpMULMOD:
			mod, _ := stk.pop()
			b, _ := stk.pop()
			a, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			if mod.Sign() == 0 {
				stk.push(big.NewInt(0))
			} else {
				stk.push(new(big.Int).Mod(new(big.Int).Mul(a, b), mod))
			}

		case op == OpISZERO:
			a, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			boolPush(stk, a.Sign() == 0)

		case op == OpBYTE:
			// BYTE i x: extract byte i from 32-byte big-endian representation of x
			x, _ := stk.pop()
			i, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			idx := i.Int64()
			if idx < 0 || idx >= 32 {
				stk.push(big.NewInt(0))
			} else {
				xBytes := make([]byte, 32)
				xb := x.Bytes()
				copy(xBytes[32-len(xb):], xb)
				stk.push(new(big.Int).SetUint64(uint64(xBytes[idx])))
			}

		case op == OpSIGNEXTEND:
			// SIGNEXTEND b x: sign-extend x from (b+1)*8 bits
			x, _ := stk.pop()
			b, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			bv := b.Int64()
			if bv < 31 {
				bit := uint(bv*8 + 7)
				xBytes := make([]byte, 32)
				xb := x.Bytes()
				copy(xBytes[32-len(xb):], xb)
				// check the sign bit
				byteIdx := 31 - int(bit/8)
				bitInByte := bit % 8
				if xBytes[byteIdx]&(1<<bitInByte) != 0 {
					// negative: fill upper bytes with 0xFF
					for i := 0; i < byteIdx; i++ {
						xBytes[i] = 0xFF
					}
					xBytes[byteIdx] |= ^byte((1 << (bitInByte + 1)) - 1)
				} else {
					// positive: mask to b+1 bytes
					for i := 0; i < byteIdx; i++ {
						xBytes[i] = 0
					}
					xBytes[byteIdx] &= byte((1 << (bitInByte + 1)) - 1)
				}
				stk.push(new(big.Int).SetBytes(xBytes))
			} else {
				stk.push(x) // >= 31: no change
			}

		// ── Comparison ───────────────────────────────────────────────────
		case op == OpEQ:
			b, _ := stk.pop()
			a, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			boolPush(stk, a.Cmp(b) == 0)

		case op == OpNEQ:
			b, _ := stk.pop()
			a, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			boolPush(stk, a.Cmp(b) != 0)

		case op == OpLT:
			b, _ := stk.pop()
			a, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			boolPush(stk, a.Cmp(b) < 0)

		case op == OpGT:
			b, _ := stk.pop()
			a, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			boolPush(stk, a.Cmp(b) > 0)

		case op == OpLTE:
			b, _ := stk.pop()
			a, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			boolPush(stk, a.Cmp(b) <= 0)

		case op == OpGTE:
			b, _ := stk.pop()
			a, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			boolPush(stk, a.Cmp(b) >= 0)

		// ── Bitwise ──────────────────────────────────────────────────────
		case op == OpAND:
			b, _ := stk.pop()
			a, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			stk.push(new(big.Int).And(a, b))

		case op == OpOR:
			b, _ := stk.pop()
			a, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			stk.push(new(big.Int).Or(a, b))

		case op == OpXOR:
			b, _ := stk.pop()
			a, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			stk.push(new(big.Int).Xor(a, b))

		case op == OpNOT:
			a, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			stk.push(new(big.Int).Xor(a, max256))

		case op == OpSHL:
			shift, _ := stk.pop()
			val, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			if shift.BitLen() > 8 {
				stk.push(big.NewInt(0))
			} else {
				stk.push(new(big.Int).Lsh(val, uint(shift.Uint64())))
			}

		case op == OpSHR:
			shift, _ := stk.pop()
			val, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			if shift.BitLen() > 8 {
				stk.push(big.NewInt(0))
			} else {
				stk.push(new(big.Int).Rsh(val, uint(shift.Uint64())))
			}

		// ── Stack: POP ───────────────────────────────────────────────────
		case op == OpPOP:
			if _, err := stk.pop(); err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}

		// ── Memory ───────────────────────────────────────────────────────
		case op == OpMLOAD:
			offsetInt, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			offset, err := memOperand(offsetInt)
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			if err := chargeMemory(offset, 32); err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			data, err := mem.load(offset, 32)
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			stk.push(new(big.Int).SetBytes(data))

		case op == OpMSTORE:
			val, _ := stk.pop()
			offsetInt, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			offset, err := memOperand(offsetInt)
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			if err := chargeMemory(offset, 32); err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			padded := make([]byte, 32)
			vb := val.Bytes()
			copy(padded[32-len(vb):], vb)
			if err := mem.store(offset, 32, padded); err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}

		case op == OpMSTORE8:
			val, _ := stk.pop()
			offsetInt, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			offset, err := memOperand(offsetInt)
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			if err := chargeMemory(offset, 1); err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			b := byte(val.Uint64() & 0xFF)
			if err := mem.storeByte(offset, b); err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}

		case op == OpMSIZE:
			stk.push(big.NewInt(int64(mem.size())))

		// ── Storage ──────────────────────────────────────────────────────
		case op == OpSLOAD:
			if err := useGas(e.gasTable.SLOAD - baseCost); err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			slotInt, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			val, err := e.state.GetStorageValue(ctx.Contract, bigToSlot(slotInt))
			if err != nil || len(val) == 0 {
				stk.push(big.NewInt(0))
			} else {
				stk.push(new(big.Int).SetBytes(val))
			}

		case op == OpSSTORE:
			if ctx.IsStatic {
				return &ExecutionResult{Err: fmt.Errorf("SSTORE: static context"), GasUsed: gasUsed}
			}
			if err := useGas(e.gasTable.SSTORE - baseCost); err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			val, _ := stk.pop()
			slotInt, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			if err := e.state.SetStorageValue(ctx.Contract, bigToSlot(slotInt), val.Bytes()); err != nil {
				return &ExecutionResult{Err: fmt.Errorf("SSTORE: %w", err), GasUsed: gasUsed}
			}

		// ── Control flow ─────────────────────────────────────────────────
		case op == OpJUMP:
			dest, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			newPC := int(dest.Int64())
			if newPC < 0 || newPC >= len(code) || code[newPC] != OpJUMPDEST {
				return &ExecutionResult{Err: fmt.Errorf("JUMP to invalid/non-JUMPDEST pc=%d", newPC), GasUsed: gasUsed}
			}
			pc = newPC + 1 // skip JUMPDEST

		case op == OpJUMPI:
			cond, _ := stk.pop()
			dest, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			if cond.Sign() != 0 {
				newPC := int(dest.Int64())
				if newPC < 0 || newPC >= len(code) || code[newPC] != OpJUMPDEST {
					return &ExecutionResult{Err: fmt.Errorf("JUMPI to invalid pc=%d", newPC), GasUsed: gasUsed}
				}
				pc = newPC + 1
			}

		case op == OpJUMPDEST:
			// No-op — just a valid jump target marker

		case op == OpPC:
			stk.push(big.NewInt(int64(pc - 1)))

		// ── Calldata & code access ────────────────────────────────────────
		case op == OpCALLDATALOAD:
			offsetInt, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			offset := int(offsetInt.Int64())
			data := make([]byte, 32)
			if offset < len(ctx.Input) {
				copy(data, ctx.Input[offset:])
			}
			stk.push(new(big.Int).SetBytes(data))

		case op == OpCALLDATASIZE:
			stk.push(big.NewInt(int64(len(ctx.Input))))

		case op == OpCALLDATACOPY:
			dst, src, sz, err := copyOperands(stk)
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			// Charge for the copy AND the memory growth before allocating: `sz`
			// comes straight off the stack, so billing afterwards let a contract
			// force an arbitrary allocation it could not pay for.
			if err := useGas(wordsFor(sz) * e.gasTable.MemPerWord); err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			if err := chargeMemory(dst, sz); err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			data := make([]byte, sz)
			if src < len(ctx.Input) {
				copy(data, ctx.Input[src:])
			}
			if err := mem.store(dst, sz, data); err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}

		case op == OpCODECOPY:
			dst, src, sz, err := copyOperands(stk)
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			if err := useGas(wordsFor(sz) * e.gasTable.MemPerWord); err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			if err := chargeMemory(dst, sz); err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			data := make([]byte, sz)
			if src < len(code) {
				copy(data, code[src:])
			}
			if err := mem.store(dst, sz, data); err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}

		case op == OpRETURNDATASIZE:
			stk.push(big.NewInt(int64(len(returnData))))

		case op == OpRETURNDATACOPY:
			dst, src, sz, err := copyOperands(stk)
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			if src+sz > len(returnData) {
				return &ExecutionResult{Err: fmt.Errorf("RETURNDATACOPY: out of bounds"), GasUsed: gasUsed}
			}
			if err := useGas(wordsFor(sz) * e.gasTable.MemPerWord); err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			if err := chargeMemory(dst, sz); err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			if err := mem.store(dst, sz, returnData[src:src+sz]); err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}

		// ── Context ──────────────────────────────────────────────────────
		case op == OpCALLER:
			stk.push(addrToBig(ctx.Caller))
		case op == OpORIGIN:
			stk.push(addrToBig(ctx.Origin))
		case op == OpCALLVALUE:
			stk.push(new(big.Int).Set(ctx.CallValue))
		case op == OpCODESIZE:
			stk.push(big.NewInt(int64(len(code))))
		case op == OpBLOCKHEIGHT:
			stk.push(new(big.Int).SetUint64(ctx.BlockHeight))
		case op == OpTIMESTAMP:
			stk.push(big.NewInt(ctx.Timestamp))
		case op == OpADDRESS:
			stk.push(addrToBig(ctx.Contract))
		case op == OpGASLIMIT:
			stk.push(new(big.Int).SetUint64(gas))

		// ── Crypto ───────────────────────────────────────────────────────
		case op == OpSHA3:
			if err := useGas(e.gasTable.SHA3 - baseCost); err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			data, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			h := qcrypto.Hash32(data.Bytes())
			stk.push(new(big.Int).SetBytes(h[:]))

		case op == OpSHA3_MEM:
			// SHA3_MEM offset size → SHA3-256 of memory[offset:offset+size]
			if err := useGas(e.gasTable.SHA3 - baseCost); err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			size, _ := stk.pop()
			offsetInt, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			offset, err := memOperand(offsetInt)
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			sz, err := memOperand(size)
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			// Bill per byte before hashing, not after.
			if err := useGas(e.gasTable.SHA3PerByte * uint64(sz)); err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			data, err := mem.load(offset, sz)
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			h := qcrypto.Hash32(data)
			stk.push(new(big.Int).SetBytes(h[:]))

		case op == OpVERIFY_MLDSA:
			if err := useGas(e.gasTable.VERIFY_MLDSA - baseCost); err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			sigSlot, _ := stk.pop()
			msgSlot, _ := stk.pop()
			pubSlot, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			pubKeyBytes, _ := e.state.GetStorageValue(ctx.Contract, bigToSlot(pubSlot))
			msgBytes, _ := e.state.GetStorageValue(ctx.Contract, bigToSlot(msgSlot))
			sigBytes, _ := e.state.GetStorageValue(ctx.Contract, bigToSlot(sigSlot))
			pub, parseErr := qcrypto.PublicKeyFromBytes(pubKeyBytes)
			boolPush(stk, parseErr == nil && qcrypto.Verify(pub, msgBytes, qcrypto.CtxMessage, sigBytes))

		// ── Events / Logs ─────────────────────────────────────────────────
		case op >= OpLOG0 && op <= OpLOG4:
			if ctx.IsStatic {
				return &ExecutionResult{Err: fmt.Errorf("LOG: static context"), GasUsed: gasUsed}
			}
			numTopics := int(op - OpLOG0)
			dataOffset, dataSize, err := popMemRange(stk)
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			// Bill the payload before materialising it.
			if err := useGas(e.gasTable.LOGPerByte*uint64(dataSize) + e.gasTable.LOGPerTopic*uint64(numTopics)); err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			data, err := mem.load(dataOffset, dataSize)
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			topics := make([][32]byte, numTopics)
			for i := 0; i < numTopics; i++ {
				topicInt, err := stk.pop()
				if err != nil {
					return &ExecutionResult{Err: err, GasUsed: gasUsed}
				}
				tb := topicInt.Bytes()
				var topic [32]byte
				copy(topic[32-len(tb):], tb)
				topics[i] = topic
			}
			ctx.Logs = append(ctx.Logs, &types.Log{
				Address: ctx.Contract,
				Topics:  topics,
				Data:    data,
			})

		// ── Token ─────────────────────────────────────────────────────────
		case op == OpTRANSFER:
			if ctx.IsStatic {
				return &ExecutionResult{Err: fmt.Errorf("TRANSFER: static context"), GasUsed: gasUsed}
			}
			if err := useGas(e.gasTable.TRANSFER - baseCost); err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			amount, _ := stk.pop()
			addrInt, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			if err := e.state.Transfer(ctx.Contract, bigToAddr(addrInt), amount); err != nil {
				return &ExecutionResult{Err: fmt.Errorf("TRANSFER: %w", err), GasUsed: gasUsed}
			}

		case op == OpBALANCE:
			if err := useGas(e.gasTable.BALANCE - baseCost); err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			addrInt, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			bal, _ := e.state.GetBalance(bigToAddr(addrInt))
			if bal == nil {
				bal = big.NewInt(0)
			}
			stk.push(bal)

		case op == OpTOTALGAS:
			stk.push(new(big.Int).SetUint64(gas - gasUsed))

		// ── Contract interaction ─────────────────────────────────────────
		case op == OpCALL || op == OpSTATICCALL || op == OpDELEGATECALL:
			isStatic := ctx.IsStatic || op == OpSTATICCALL
			if ctx.IsStatic && op != OpSTATICCALL {
				return &ExecutionResult{Err: fmt.Errorf("CALL: static context"), GasUsed: gasUsed}
			}

			// Stack: [gas, addr, value(CALL only), argsOffset, argsSize, retOffset, retSize]
			subGas, _ := stk.pop()
			addrInt, _ := stk.pop()
			var value *big.Int
			if op == OpCALL {
				value, _ = stk.pop()
			} else {
				value = big.NewInt(0)
			}
			argsOffset, _ := stk.pop()
			argsSize, _ := stk.pop()
			retOffset, _ := stk.pop()
			retSize, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}

			callGas := subGas.Uint64()
			if callGas > gas-gasUsed {
				callGas = gas - gasUsed
			}

			argsOff, err := memOperand(argsOffset)
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			argsSz, err := memOperand(argsSize)
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			calldata, err := mem.load(argsOff, argsSz)
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			toAddr := bigToAddr(addrInt)

			var subResult *ExecutionResult

			// Snapshot state before the sub-call so a FAILED callee leaves no
			// residue: its storage (SSTORE / module writes), account and nonce
			// changes are rolled back, matching EVM CALL semantics. Without this a
			// callee that wrote storage and then reverted left that storage durable
			// if the outer tx succeeded — now state-root-visible, so it must revert.
			subSnap := e.state.Snapshot()

			// Check precompile registry first
			if e.precompiles.IsPrecompile(toAddr) {
				out, pcGas, pcErr := e.precompiles.Call(toAddr, calldata, callGas)
				subResult = &ExecutionResult{
					ReturnValue: out,
					GasUsed:     pcGas,
					Err:         pcErr,
				}
			} else {
				caller := ctx.Contract
				origin := ctx.Origin
				// codeSource is the account whose CODE runs; toAddr is the
				// account whose STORAGE and identity the code sees. They differ
				// only for DELEGATECALL.
				codeSource := toAddr
				var valueErr error

				switch op {
				case OpDELEGATECALL:
					// DELEGATECALL runs the TARGET's code against the CALLER's
					// storage and identity. Overwriting toAddr with ctx.Contract
					// discarded the requested library and re-entered this same
					// contract, so a proxy recursed into itself up to the depth
					// limit instead of running the delegate's code.
					caller = ctx.Caller
					toAddr = ctx.Contract
				case OpCALL:
					// Move the value BEFORE handing over control. The callee saw
					// it as CALLVALUE, but the SPLD never left the caller — so a
					// contract gating on CALLVALUE could be driven by a caller
					// that paid nothing.
					if value.Sign() > 0 {
						valueErr = e.state.Transfer(ctx.Contract, toAddr, value)
					}
				}

				if valueErr != nil {
					subResult = &ExecutionResult{Err: fmt.Errorf("CALL: value transfer failed: %w", valueErr)}
				} else {
					var callErr error
					subResult, callErr = e.callAtDepthWithCode(caller, origin, toAddr, codeSource, calldata, value, ctx.BlockHeight, ctx.Timestamp, isStatic, ctx.Depth+1)
					if callErr != nil && subResult == nil {
						subResult = &ExecutionResult{Err: callErr}
					}
				}
			}

			if err := useGas(subResult.GasUsed); err != nil {
				e.state.RevertToSnapshot(subSnap)
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}

			returnData = subResult.ReturnValue

			if subResult.Err != nil {
				// Sub-call reverted: undo its state writes and DROP its logs.
				e.state.RevertToSnapshot(subSnap)
				boolPush(stk, false) // call failed
			} else {
				e.state.CommitSnapshot(subSnap)
				ctx.Logs = append(ctx.Logs, subResult.Logs...)
				boolPush(stk, true) // call succeeded
				// Copy return data into memory
				retOff, offErr := memOperand(retOffset)
				retSz, szErr := memOperand(retSize)
				if offErr != nil || szErr != nil {
					return &ExecutionResult{Err: fmt.Errorf("CALL: return data range out of bounds"), GasUsed: gasUsed}
				}
				if len(returnData) > 0 && retSz > 0 {
					if retSz > len(returnData) {
						retSz = len(returnData)
					}
					if err := chargeMemory(retOff, retSz); err != nil {
						return &ExecutionResult{Err: err, GasUsed: gasUsed}
					}
					if err := mem.store(retOff, retSz, returnData[:retSz]); err != nil {
						return &ExecutionResult{Err: err, GasUsed: gasUsed}
					}
				}
			}

		case op == OpCREATE:
			if ctx.IsStatic {
				return &ExecutionResult{Err: fmt.Errorf("CREATE: static context"), GasUsed: gasUsed}
			}
			if err := useGas(e.gasTable.CREATE - baseCost); err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			value, _ := stk.pop()
			codeOff, codeSz, err := popMemRangeReversed(stk)
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			bytecode, err := mem.load(codeOff, codeSz)
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			contractAddr, deployErr := e.Deploy(ctx.Contract, bytecode, uint64(pc))
			if deployErr != nil {
				stk.push(big.NewInt(0))
				break
			}
			// Endow the new contract. The value was popped and discarded before,
			// so a constructor could never be funded.
			if value.Sign() > 0 {
				if terr := e.state.Transfer(ctx.Contract, contractAddr, value); terr != nil {
					stk.push(big.NewInt(0))
					break
				}
			}
			stk.push(addrToBig(contractAddr))

		case op == OpCREATE2:
			if ctx.IsStatic {
				return &ExecutionResult{Err: fmt.Errorf("CREATE2: static context"), GasUsed: gasUsed}
			}
			if err := useGas(e.gasTable.CREATE - baseCost); err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			salt, _ := stk.pop()
			codeOffset, _ := stk.pop()
			codeSize, _ := stk.pop()
			value, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			codeOff, offErr := memOperand(codeOffset)
			codeSz, szErr := memOperand(codeSize)
			if offErr != nil || szErr != nil {
				return &ExecutionResult{Err: fmt.Errorf("CREATE2: code range out of bounds"), GasUsed: gasUsed}
			}
			bytecode, err := mem.load(codeOff, codeSz)
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			// CREATE2: address = SHA3-256(deployer || salt || SHA3-256(bytecode))
			codeHash := qcrypto.Hash32(bytecode)
			saltBytes := make([]byte, 32)
			sb := salt.Bytes()
			copy(saltBytes[32-len(sb):], sb)
			input := append(ctx.Contract[:], saltBytes...)
			input = append(input, codeHash[:]...)
			addrHash := qcrypto.Hash32(input)
			var contractAddr qcrypto.Address
			copy(contractAddr[:], addrHash[:])

			// Refuse to deploy over an account that already exists. The previous
			// code wrote a fresh types.NewAccount at the derived address, which
			// reset the balance to zero — a chosen salt could therefore destroy
			// the SPLD held by any pre-funded address and overwrite live code.
			if existing, gerr := e.state.GetAccount(contractAddr); gerr == nil {
				if existing.IsContract || existing.Nonce > 0 ||
					(existing.Balance != nil && existing.Balance.Sign() > 0) {
					stk.push(big.NewInt(0))
					break
				}
			}

			e.state.SetCode(codeHash, bytecode)
			acc := types.NewAccount(contractAddr)
			acc.IsContract = true
			acc.CodeHash = codeHash
			e.state.SetAccount(acc)
			if value.Sign() > 0 {
				if terr := e.state.Transfer(ctx.Contract, contractAddr, value); terr != nil {
					stk.push(big.NewInt(0))
					break
				}
			}
			stk.push(addrToBig(contractAddr))

		case op == OpSELFDESTRUCT:
			if ctx.IsStatic {
				return &ExecutionResult{Err: fmt.Errorf("SELFDESTRUCT: static context"), GasUsed: gasUsed}
			}
			benefInt, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			bal, _ := e.state.GetBalance(ctx.Contract)
			if bal != nil && bal.Sign() > 0 {
				e.state.Transfer(ctx.Contract, bigToAddr(benefInt), bal)
			}
			return &ExecutionResult{GasUsed: gasUsed, Logs: ctx.Logs}

		// ── Return / Revert ──────────────────────────────────────────────
		case op == OpRETURN:
			offset, size, err := popMemRange(stk)
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			data, err := mem.load(offset, size)
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			return &ExecutionResult{ReturnValue: data, GasUsed: gasUsed, Logs: ctx.Logs}

		case op == OpREVERT:
			offset, size, err := popMemRange(stk)
			if err != nil {
				return &ExecutionResult{Err: fmt.Errorf("REVERT"), Reverted: true, GasUsed: gasUsed}
			}
			data, err := mem.load(offset, size)
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			return &ExecutionResult{ReturnValue: data, Err: fmt.Errorf("execution reverted"), Reverted: true, GasUsed: gasUsed}

		case op == OpREQUIRE:
			msg, _ := stk.pop()
			cond, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			if cond.Sign() == 0 {
				errMsg := "require failed"
				if msg.Sign() > 0 {
					errMsg = fmt.Sprintf("require: %s", string(msg.Bytes()))
				}
				return &ExecutionResult{Err: fmt.Errorf("%s", errMsg), Reverted: true, GasUsed: gasUsed}
			}

		default:
			return &ExecutionResult{
				Err:     fmt.Errorf("unknown opcode 0x%02x at pc=%d", op, pc-1),
				GasUsed: gasUsed,
			}
		}
	}

	return &ExecutionResult{GasUsed: gasUsed, Logs: ctx.Logs}
}

// ── High-level API ────────────────────────────────────────────────────────────

func (e *Engine) Deploy(deployer qcrypto.Address, bytecode []byte, nonce uint64) (qcrypto.Address, error) {
	if len(bytecode) == 0 {
		return qcrypto.Address{}, fmt.Errorf("cannot deploy empty bytecode")
	}
	nonceBuf := make([]byte, 8)
	binary.BigEndian.PutUint64(nonceBuf, nonce)
	input := append(deployer[:], nonceBuf...)
	hash := qcrypto.Hash32(input)
	var addr qcrypto.Address
	copy(addr[:], hash[:])

	codeHash := qcrypto.Hash32(bytecode)
	e.state.SetCode(codeHash, bytecode)

	acc := types.NewAccount(addr)
	acc.IsContract = true
	acc.CodeHash = codeHash
	e.state.SetAccount(acc)
	return addr, nil
}

func (e *Engine) Call(caller, contract qcrypto.Address, calldata []byte, value *big.Int) (*ExecutionResult, error) {
	return e.CallWithContext(caller, caller, contract, calldata, value, 0, 0, false)
}

func (e *Engine) CallWithContext(caller, origin, contract qcrypto.Address, calldata []byte, value *big.Int, blockHeight uint64, timestamp int64, isStatic bool) (*ExecutionResult, error) {
	return e.callAtDepth(caller, origin, contract, calldata, value, blockHeight, timestamp, isStatic, 0)
}

// callAtDepth runs a (sub)call at an explicit call depth so recursion is bounded
// by MaxCallDepth. The CALL opcode passes ctx.Depth+1; top-level entries pass 0.
// Previously every subcall built a fresh Depth=0 context, so MaxCallDepth never
// triggered and a self-recursive contract exhausted the node's stack/heap.
func (e *Engine) callAtDepth(caller, origin, contract qcrypto.Address, calldata []byte, value *big.Int, blockHeight uint64, timestamp int64, isStatic bool, depth int) (*ExecutionResult, error) {
	return e.callAtDepthWithCode(caller, origin, contract, contract, calldata, value, blockHeight, timestamp, isStatic, depth)
}

// callAtDepthWithCode runs codeSource's bytecode in contract's storage and
// identity context. The two addresses are equal for CALL and STATICCALL; they
// differ only for DELEGATECALL, which is the whole point of that instruction.
func (e *Engine) callAtDepthWithCode(caller, origin, contract, codeSource qcrypto.Address, calldata []byte, value *big.Int, blockHeight uint64, timestamp int64, isStatic bool, depth int) (*ExecutionResult, error) {
	if depth > MaxCallDepth {
		return &ExecutionResult{Err: fmt.Errorf("max call depth exceeded")}, fmt.Errorf("max call depth exceeded")
	}
	// Check precompile
	if e.precompiles.IsPrecompile(codeSource) {
		out, gas, err := e.precompiles.Call(codeSource, calldata, MaxGasPerCall)
		return &ExecutionResult{ReturnValue: out, GasUsed: gas, Err: err}, err
	}

	codeAcc, err := e.state.GetAccount(codeSource)
	if err != nil {
		return nil, err
	}
	if !codeAcc.IsContract {
		return nil, types.ErrContractNotFound
	}
	code, err := e.state.GetCode(codeAcc.CodeHash)
	if err != nil {
		return nil, fmt.Errorf("load code: %w", err)
	}
	ctx := &ExecutionContext{
		Caller: caller, Origin: origin, Contract: contract,
		CallValue: value, Input: calldata, Code: code,
		BlockHeight: blockHeight, Timestamp: timestamp,
		IsStatic: isStatic, Gas: MaxGasPerCall, Depth: depth,
		Logs: make([]*types.Log, 0),
	}
	result := e.Execute(ctx)
	return result, result.Err
}

func (e *Engine) StaticCall(caller, contract qcrypto.Address, calldata []byte) (*ExecutionResult, error) {
	return e.CallWithContext(caller, caller, contract, calldata, big.NewInt(0), 0, 0, true)
}

// ── ABI helpers ───────────────────────────────────────────────────────────────

func SelectorFromSignature(sig string) [4]byte {
	hash := qcrypto.Hash32([]byte(sig))
	var sel [4]byte
	copy(sel[:], hash[:4])
	return sel
}

func EncodeCall(signature string, args ...*big.Int) []byte {
	sel := SelectorFromSignature(signature)
	result := make([]byte, 4+len(args)*32)
	copy(result[:4], sel[:])
	for i, arg := range args {
		argBytes := arg.Bytes()
		offset := 4 + i*32
		copy(result[offset+32-len(argBytes):offset+32], argBytes)
	}
	return result
}

func DecodeUint256(data []byte, offset int) *big.Int {
	if offset+32 > len(data) {
		return big.NewInt(0)
	}
	return new(big.Int).SetBytes(data[offset : offset+32])
}

func DecodeAddress(data []byte, offset int) qcrypto.Address {
	if offset+32 > len(data) {
		return qcrypto.Address{}
	}
	var addr qcrypto.Address
	copy(addr[:], data[offset:offset+32])
	return addr
}

// ── Internal helpers ──────────────────────────────────────────────────────────

func boolPush(stk *stack, b bool) {
	if b {
		stk.push(big.NewInt(1))
	} else {
		stk.push(big.NewInt(0))
	}
}

func bigToSlot(v *big.Int) [32]byte {
	var slot [32]byte
	b := v.Bytes()
	copy(slot[32-len(b):], b)
	return slot
}

func bigToAddr(v *big.Int) qcrypto.Address {
	var addr qcrypto.Address
	b := v.Bytes()
	if len(b) > qcrypto.AddressSize {
		b = b[len(b)-qcrypto.AddressSize:]
	}
	copy(addr[qcrypto.AddressSize-len(b):], b)
	return addr
}

func addrToBig(addr qcrypto.Address) *big.Int {
	return new(big.Int).SetBytes(addr[:])
}
