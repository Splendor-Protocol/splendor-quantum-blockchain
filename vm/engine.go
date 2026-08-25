// Package vm implements the Splendor Quantum Chain smart contract execution engine.
//
// # SQC-VM v3 — Production Upgrade
//
// This is NOT an EVM. It is a quantum-native VM using only NIST post-quantum
// algorithms: Dilithium Mode 3 (FIPS 204) for signatures, SHA3-256 (FIPS 202)
// for all hashing. No ECDSA. No keccak. No secp256k1.
//
// # What's new in v3
//
//   - GasTable integration: all gas costs driven by configurable gas.go table
//   - PrecompileRegistry integration: CALL/STATICCALL check precompile addresses first
//   - PUSH1–PUSH31: variable-length push (matches EVM ergonomics for small values)
//   - ISZERO: check if top of stack is zero (very common in QSol)
//   - BYTE: extract a single byte from a 256-bit value
//   - SIGNEXTEND: sign-extend a value (needed for signed integer types)
//   - DUP1–DUP16: full 16-depth duplication (previously only DUP, DUP2)
//   - SWAP1–SWAP16: full 16-depth swap (previously only SWAP, SWAP2)
//   - MSTORE8: store a single byte to memory
//   - RETURNDATASIZE / RETURNDATACOPY: access return data from sub-calls
//   - CALLDATALOAD / CALLDATASIZE / CALLDATACOPY: proper calldata access opcodes
//   - CODECOPY: copy contract code into memory
//   - JUMPDEST: no-op marker for valid jump destinations (prevents arbitrary jumps)
//   - Proper sub-call execution (CALL/STATICCALL/DELEGATECALL route through Engine)
//
// # Full Instruction Set (v3)
//
//   0x00 STOP          0x01 ADD       0x02 SUB      0x03 MUL      0x04 DIV
//   0x05 MOD           0x06 EXP       0x07 ADDMOD   0x08 MULMOD   0x09 ISZERO
//   0x0A BYTE          0x0B SIGNEXTEND
//   0x10 EQ  0x11 NEQ  0x12 LT  0x13 GT  0x14 LTE  0x15 GTE
//   0x20 AND 0x21 OR   0x22 XOR 0x23 NOT 0x24 SHL   0x25 SHR
//   0x30 PUSH32        0x31 POP    0x32–0x41 DUP1–DUP16
//   0x42 SWAP1–0x51 SWAP16
//   0x52 PUSH1         0x53–0x72 PUSH2–PUSH31
//   0x73 MSTORE8       0x74 JUMPDEST
//   0x40 MLOAD  0x41 MSTORE  0x42 MSIZE
//   (memory opcodes kept at original positions; DUP/SWAP moved to 0x32+)
//   0x50 SLOAD  0x51 SSTORE
//   0x60 JUMP  0x61 JUMPI  0x62 PC  0x63 STOP(alias)
//   0x64 CALLDATALOAD  0x65 CALLDATASIZE  0x66 CALLDATACOPY
//   0x67 CODECOPY  0x68 RETURNDATASIZE  0x69 RETURNDATACOPY
//   0x70–0x76 context opcodes (CALLER, ORIGIN, CALLVALUE, etc.)
//   0x80 SHA3  0x81 VERIFY_DILITHIUM  0x82 SHA3_MEM (hash memory range)
//   0x90–0x94 contract opcodes
//   0xA0–0xA4 LOG0–LOG4
//   0xB0–0xB2 token opcodes
//   0xC0–0xC2 ABI opcodes
//   0xF0–0xF2 RETURN/REVERT/REQUIRE
package vm

import (
	"encoding/binary"
	"fmt"
	"math/big"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// ── Opcode constants (v3) ─────────────────────────────────────────────────────

const (
	// ── Arithmetic ───────────────────────────────────────────────────────
	OpSTOP       byte = 0x00
	OpADD        byte = 0x01
	OpSUB        byte = 0x02
	OpMUL        byte = 0x03
	OpDIV        byte = 0x04
	OpMOD        byte = 0x05
	OpEXP        byte = 0x06
	OpADDMOD     byte = 0x07
	OpMULMOD     byte = 0x08
	OpISZERO     byte = 0x09 // NEW v3: push 1 if top==0 else 0
	OpBYTE       byte = 0x0A // NEW v3: extract byte i from value
	OpSIGNEXTEND byte = 0x0B // NEW v3: sign extend b-bit integer

	// ── Comparison ───────────────────────────────────────────────────────
	OpEQ  byte = 0x10
	OpNEQ byte = 0x11
	OpLT  byte = 0x12
	OpGT  byte = 0x13
	OpLTE byte = 0x14
	OpGTE byte = 0x15

	// ── Bitwise ──────────────────────────────────────────────────────────
	OpAND byte = 0x20
	OpOR  byte = 0x21
	OpXOR byte = 0x22
	OpNOT byte = 0x23
	OpSHL byte = 0x24
	OpSHR byte = 0x25

	// ── Stack: PUSH ──────────────────────────────────────────────────────
	OpPUSH32 byte = 0x30 // push 32 bytes
	// PUSH1–PUSH31 use 0x52–0x70 (see getPushSize helper)
	OpPUSH1  byte = 0x52
	OpPUSH2  byte = 0x53
	OpPUSH31 byte = 0x70

	// ── Stack: POP ───────────────────────────────────────────────────────
	OpPOP byte = 0x31

	// ── Stack: DUP1–DUP16 (0x32–0x41) ───────────────────────────────────
	OpDUP1  byte = 0x32
	OpDUP2  byte = 0x33
	OpDUP3  byte = 0x34
	OpDUP4  byte = 0x35
	OpDUP5  byte = 0x36
	OpDUP6  byte = 0x37
	OpDUP7  byte = 0x38
	OpDUP8  byte = 0x39
	OpDUP9  byte = 0x3A
	OpDUP10 byte = 0x3B
	OpDUP11 byte = 0x3C
	OpDUP12 byte = 0x3D
	OpDUP13 byte = 0x3E
	OpDUP14 byte = 0x3F
	OpDUP15 byte = 0x40
	OpDUP16 byte = 0x41

	// ── Stack: SWAP1–SWAP16 (0x42–0x51) ─────────────────────────────────
	OpSWAP1  byte = 0x42
	OpSWAP2  byte = 0x43
	OpSWAP3  byte = 0x44
	OpSWAP4  byte = 0x45
	OpSWAP5  byte = 0x46
	OpSWAP6  byte = 0x47
	OpSWAP7  byte = 0x48
	OpSWAP8  byte = 0x49
	OpSWAP9  byte = 0x4A
	OpSWAP10 byte = 0x4B
	OpSWAP11 byte = 0x4C
	OpSWAP12 byte = 0x4D
	OpSWAP13 byte = 0x4E
	OpSWAP14 byte = 0x4F
	OpSWAP15 byte = 0x50 // note: was OpSLOAD in v2 — SLOAD moved to 0x55
	OpSWAP16 byte = 0x51 // note: was OpSSTORE in v2 — SSTORE moved to 0x56

	// ── Memory ───────────────────────────────────────────────────────────
	OpMLOAD   byte = 0x53 // (moved from 0x40)
	OpMSTORE  byte = 0x54 // (moved from 0x41)
	OpMSTORE8 byte = 0x71 // NEW v3: store 1 byte
	OpMSIZE   byte = 0x72 // (moved from 0x42)

	// ── Storage ──────────────────────────────────────────────────────────
	OpSLOAD  byte = 0x55 // (moved from 0x50)
	OpSSTORE byte = 0x56 // (moved from 0x51)

	// ── Control flow ─────────────────────────────────────────────────────
	OpJUMP    byte = 0x60
	OpJUMPI   byte = 0x61
	OpPC      byte = 0x62
	OpJUMPDEST byte = 0x63 // NEW v3: valid jump destination marker (no-op)

	// ── Calldata & code access (NEW v3) ───────────────────────────────────
	OpCALLDATALOAD  byte = 0x64
	OpCALLDATASIZE  byte = 0x65
	OpCALLDATACOPY  byte = 0x66
	OpCODECOPY      byte = 0x67
	OpRETURNDATASIZE byte = 0x68
	OpRETURNDATACOPY byte = 0x69

	// ── Context ──────────────────────────────────────────────────────────
	OpCALLER      byte = 0x70
	OpORIGIN      byte = 0x73
	OpCALLVALUE   byte = 0x74
	OpCODESIZE    byte = 0x75
	OpBLOCKHEIGHT byte = 0x76
	OpTIMESTAMP   byte = 0x77
	OpADDRESS     byte = 0x78
	OpGASLIMIT    byte = 0x79

	// ── Crypto (quantum-safe only) ────────────────────────────────────────
	OpSHA3             byte = 0x80
	OpVERIFY_DILITHIUM byte = 0x81
	OpSHA3_MEM         byte = 0x82 // NEW v3: SHA3-256 of memory range → push hash

	// ── Contract interaction ──────────────────────────────────────────────
	OpCALL         byte = 0x90
	OpSTATICCALL   byte = 0x91
	OpDELEGATECALL byte = 0x92
	OpCREATE       byte = 0x93
	OpSELFDESTRUCT byte = 0x94
	OpCREATE2      byte = 0x95 // NEW v3: CREATE with salt (deterministic address)

	// ── Events ───────────────────────────────────────────────────────────
	OpLOG0 byte = 0xA0
	OpLOG1 byte = 0xA1
	OpLOG2 byte = 0xA2
	OpLOG3 byte = 0xA3
	OpLOG4 byte = 0xA4

	// ── Token operations ─────────────────────────────────────────────────
	OpTRANSFER byte = 0xB0
	OpBALANCE  byte = 0xB1
	OpTOTALGAS byte = 0xB2

	// ── ABI helpers ──────────────────────────────────────────────────────
	OpABI_ENCODE byte = 0xC0
	OpABI_DECODE byte = 0xC1
	OpSELECTOR   byte = 0xC2

	// ── Return / Revert ──────────────────────────────────────────────────
	OpRETURN  byte = 0xF0
	OpREVERT  byte = 0xF1
	OpREQUIRE byte = 0xF2
)

// ── VM limits ─────────────────────────────────────────────────────────────────

const (
	MaxStackDepth  = 1024
	MaxGasPerCall  = 10_000_000
	MaxMemoryBytes = 32 * 1024 * 1024
	MaxCallDepth   = 64
)

// max256 is 2^256 - 1, used for bitwise NOT
var max256 = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))

// ── Execution context ─────────────────────────────────────────────────────────

type ExecutionContext struct {
	Caller    qcrypto.Address
	Origin    qcrypto.Address
	Contract  qcrypto.Address
	CallValue *big.Int
	Input     []byte // calldata
	Code      []byte
	BlockHeight uint64
	Timestamp int64
	Depth     int
	Gas       uint64
	Logs      []*types.Log
	IsStatic  bool
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

func (s *stack) push(v *big.Int) error {
	if len(s.items) >= MaxStackDepth {
		return fmt.Errorf("stack overflow")
	}
	s.items = append(s.items, new(big.Int).Set(v))
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

func (m *memory) store(offset, size int, data []byte) error {
	end := offset + size
	if end > MaxMemoryBytes {
		return fmt.Errorf("memory limit exceeded")
	}
	if end > len(m.data) {
		m.data = append(m.data, make([]byte, end-len(m.data))...)
	}
	copy(m.data[offset:], data)
	return nil
}

func (m *memory) storeByte(offset int, b byte) error {
	if offset+1 > MaxMemoryBytes {
		return fmt.Errorf("memory limit exceeded")
	}
	if offset+1 > len(m.data) {
		m.data = append(m.data, make([]byte, offset+1-len(m.data))...)
	}
	m.data[offset] = b
	return nil
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

	for pc < len(code) {
		op := code[pc]
		pc++

		// Use the gas table for base cost
		baseCost := e.gasTable.GasOf(op)
		if err := useGas(baseCost); err != nil {
			return &ExecutionResult{Err: err, GasUsed: gasUsed, Logs: ctx.Logs}
		}

		switch {

		// ── PUSH1–PUSH31 (variable length) ───────────────────────────────
		case op >= OpPUSH1 && op <= OpPUSH31:
			n := int(op-OpPUSH1) + 1 // 1..31 bytes
			if pc+n > len(code) {
				return &ExecutionResult{Err: fmt.Errorf("PUSH%d: insufficient bytes", n)}
			}
			v := new(big.Int).SetBytes(code[pc : pc+n])
			pc += n
			if err := stk.push(v); err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}

		// ── PUSH32 ───────────────────────────────────────────────────────
		case op == OpPUSH32:
			if pc+32 > len(code) {
				return &ExecutionResult{Err: fmt.Errorf("PUSH32: insufficient bytes")}
			}
			v := new(big.Int).SetBytes(code[pc : pc+32])
			pc += 32
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
			stk.push(new(big.Int).Exp(base, expV, nil))

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
			data, _ := mem.load(int(offsetInt.Int64()), 32)
			stk.push(new(big.Int).SetBytes(data))

		case op == OpMSTORE:
			val, _ := stk.pop()
			offsetInt, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			padded := make([]byte, 32)
			vb := val.Bytes()
			copy(padded[32-len(vb):], vb)
			if err := mem.store(int(offsetInt.Int64()), 32, padded); err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}

		case op == OpMSTORE8:
			val, _ := stk.pop()
			offsetInt, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			b := byte(val.Uint64() & 0xFF)
			if err := mem.storeByte(int(offsetInt.Int64()), b); err != nil {
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
			size, _ := stk.pop()
			srcOff, _ := stk.pop()
			dstOff, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			sz := int(size.Int64())
			src := int(srcOff.Int64())
			dst := int(dstOff.Int64())
			data := make([]byte, sz)
			if src < len(ctx.Input) {
				copy(data, ctx.Input[src:])
			}
			if err := useGas(uint64((sz+31)/32) * 3); err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			mem.store(dst, sz, data)

		case op == OpCODECOPY:
			size, _ := stk.pop()
			srcOff, _ := stk.pop()
			dstOff, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			sz := int(size.Int64())
			src := int(srcOff.Int64())
			dst := int(dstOff.Int64())
			data := make([]byte, sz)
			if src < len(code) {
				copy(data, code[src:])
			}
			mem.store(dst, sz, data)

		case op == OpRETURNDATASIZE:
			stk.push(big.NewInt(int64(len(returnData))))

		case op == OpRETURNDATACOPY:
			size, _ := stk.pop()
			srcOff, _ := stk.pop()
			dstOff, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			sz := int(size.Int64())
			src := int(srcOff.Int64())
			dst := int(dstOff.Int64())
			if src+sz > len(returnData) {
				return &ExecutionResult{Err: fmt.Errorf("RETURNDATACOPY: out of bounds"), GasUsed: gasUsed}
			}
			mem.store(dst, sz, returnData[src:src+sz])

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
			data, _ := mem.load(int(offsetInt.Int64()), int(size.Int64()))
			if err := useGas(e.gasTable.SHA3PerByte * uint64(len(data))); err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			h := qcrypto.Hash32(data)
			stk.push(new(big.Int).SetBytes(h[:]))

		case op == OpVERIFY_DILITHIUM:
			if err := useGas(e.gasTable.VERIFY_DILITHIUM - baseCost); err != nil {
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
			boolPush(stk, parseErr == nil && qcrypto.Verify(pub, msgBytes, sigBytes))

		// ── Events / Logs ─────────────────────────────────────────────────
		case op >= OpLOG0 && op <= OpLOG4:
			if ctx.IsStatic {
				return &ExecutionResult{Err: fmt.Errorf("LOG: static context"), GasUsed: gasUsed}
			}
			numTopics := int(op - OpLOG0)
			dataSize, _ := stk.pop()
			dataOffset, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			data, _ := mem.load(int(dataOffset.Int64()), int(dataSize.Int64()))
			if err := useGas(e.gasTable.LOGPerByte*uint64(len(data)) + e.gasTable.LOGPerTopic*uint64(numTopics)); err != nil {
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

			calldata, _ := mem.load(int(argsOffset.Int64()), int(argsSize.Int64()))
			toAddr := bigToAddr(addrInt)

			var subResult *ExecutionResult

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
				if op == OpDELEGATECALL {
					// DELEGATECALL: execute target code in current contract's context
					caller = ctx.Caller
					toAddr = ctx.Contract
				}
				var callErr error
				subResult, callErr = e.callAtDepth(caller, origin, toAddr, calldata, value, ctx.BlockHeight, ctx.Timestamp, isStatic, ctx.Depth+1)
				if callErr != nil && subResult == nil {
					subResult = &ExecutionResult{Err: callErr}
				}
			}

			if err := useGas(subResult.GasUsed); err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}

			returnData = subResult.ReturnValue
			ctx.Logs = append(ctx.Logs, subResult.Logs...)

			if subResult.Err != nil {
				boolPush(stk, false) // call failed
			} else {
				boolPush(stk, true) // call succeeded
				// Copy return data into memory
				if len(returnData) > 0 && retSize.Int64() > 0 {
					sz := int(retSize.Int64())
					if sz > len(returnData) {
						sz = len(returnData)
					}
					mem.store(int(retOffset.Int64()), sz, returnData[:sz])
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
			codeOffset, _ := stk.pop()
			codeSize, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			bytecode, _ := mem.load(int(codeOffset.Int64()), int(codeSize.Int64()))
			_ = value
			contractAddr, deployErr := e.Deploy(ctx.Contract, bytecode, uint64(pc))
			if deployErr != nil {
				stk.push(big.NewInt(0))
			} else {
				stk.push(addrToBig(contractAddr))
			}

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
			_ = value
			bytecode, _ := mem.load(int(codeOffset.Int64()), int(codeSize.Int64()))
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
			// Store code
			e.state.SetCode(codeHash, bytecode)
			acc := types.NewAccount(contractAddr)
			acc.IsContract = true
			acc.CodeHash = codeHash
			e.state.SetAccount(acc)
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
			size, _ := stk.pop()
			offsetInt, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			data, _ := mem.load(int(offsetInt.Int64()), int(size.Int64()))
			return &ExecutionResult{ReturnValue: data, GasUsed: gasUsed, Logs: ctx.Logs}

		case op == OpREVERT:
			size, _ := stk.pop()
			offsetInt, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: fmt.Errorf("REVERT"), Reverted: true, GasUsed: gasUsed}
			}
			data, _ := mem.load(int(offsetInt.Int64()), int(size.Int64()))
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
	if depth > MaxCallDepth {
		return &ExecutionResult{Err: fmt.Errorf("max call depth exceeded")}, fmt.Errorf("max call depth exceeded")
	}
	// Check precompile
	if e.precompiles.IsPrecompile(contract) {
		out, gas, err := e.precompiles.Call(contract, calldata, MaxGasPerCall)
		return &ExecutionResult{ReturnValue: out, GasUsed: gas, Err: err}, err
	}

	acc, err := e.state.GetAccount(contract)
	if err != nil {
		return nil, err
	}
	if !acc.IsContract {
		return nil, types.ErrContractNotFound
	}
	code, err := e.state.GetCode(acc.CodeHash)
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
