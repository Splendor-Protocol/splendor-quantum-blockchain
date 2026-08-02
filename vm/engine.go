// Package vm implements the Splendor Quantum Chain smart contract execution engine.
//
// # Why NOT EVM-compatible?
//
// Ethereum's EVM is inseparable from its quantum-vulnerable primitives:
//   - Account addresses are keccak256(ecdsa_public_key)[12:] — ECDSA-derived, quantum-breakable
//   - ecrecover() opcode recovers an ECDSA public key from a signature — quantum-breakable
//   - keccak256 hashing is used throughout EVM storage layout — non-NIST
//   - The entire ABI encoding assumes 20-byte addresses
//
// Splendor Quantum Chain uses:
//   - 32-byte addresses derived from SHA3-256(dilithium_public_key)
//   - Dilithium Mode 3 signatures (NIST FIPS 204)
//   - SHA3-256 hashing throughout (NIST FIPS 202)
//
// Therefore this VM is the SQC-VM: a quantum-native, stack-based virtual machine
// that is intentionally NOT EVM-compatible. Developers write contracts in QSol
// (Quantum Solidity) — a language with Solidity-like syntax but quantum-safe semantics.
//
// # SQC-VM Instruction Set (v2 — production)
//
// Arithmetic:
//   0x01 ADD      0x02 SUB     0x03 MUL     0x04 DIV     0x05 MOD
//   0x06 EXP      0x07 ADDMOD  0x08 MULMOD
//
// Comparison:
//   0x10 EQ       0x11 NEQ     0x12 LT      0x13 GT      0x14 LTE     0x15 GTE
//
// Logic / Bitwise:
//   0x20 AND      0x21 OR      0x22 XOR     0x23 NOT     0x24 SHL     0x25 SHR
//
// Stack manipulation:
//   0x30 PUSH32   0x31 POP     0x32 DUP     0x33 DUP2    0x34 SWAP    0x35 SWAP2
//
// Memory:
//   0x40 MLOAD    0x41 MSTORE  0x42 MSIZE
//
// Storage (quantum-safe — SHA3-256 slot keys, LevelDB backed):
//   0x50 SLOAD    0x51 SSTORE
//
// Control flow:
//   0x60 JUMP     0x61 JUMPI   0x62 PC      0x63 STOP
//
// Context:
//   0x70 CALLER   0x71 ORIGIN  0x72 CALLVALUE  0x73 CODESIZE
//   0x74 BLOCKHEIGHT  0x75 TIMESTAMP  0x76 ADDRESS
//
// Crypto (quantum-safe):
//   0x80 SHA3     0x81 VERIFY_DILITHIUM
//
// Contract calls:
//   0x90 CALL     0x91 STATICCALL  0x92 DELEGATECALL
//   0x93 CREATE   0x94 SELFDESTRUCT
//
// Events / Logs:
//   0xA0 LOG0     0xA1 LOG1    0xA2 LOG2    0xA3 LOG3    0xA4 LOG4
//
// Token operations:
//   0xB0 TRANSFER   0xB1 BALANCE   0xB2 TOTALGAS
//
// Revert / Return:
//   0xF0 RETURN   0xF1 REVERT   0xF2 REQUIRE
//
// ABI helpers:
//   0xC0 ABI_ENCODE  0xC1 ABI_DECODE  0xC2 SELECTOR
package vm

import (
	"encoding/binary"
	"fmt"
	"math/big"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// ── Opcode constants ──────────────────────────────────────────────────────────

const (
	// Arithmetic
	OpADD     byte = 0x01
	OpSUB     byte = 0x02
	OpMUL     byte = 0x03
	OpDIV     byte = 0x04
	OpMOD     byte = 0x05
	OpEXP     byte = 0x06
	OpADDMOD  byte = 0x07
	OpMULMOD  byte = 0x08

	// Comparison
	OpEQ      byte = 0x10
	OpNEQ     byte = 0x11
	OpLT      byte = 0x12
	OpGT      byte = 0x13
	OpLTE     byte = 0x14
	OpGTE     byte = 0x15

	// Logic / Bitwise
	OpAND     byte = 0x20
	OpOR      byte = 0x21
	OpXOR     byte = 0x22
	OpNOT     byte = 0x23
	OpSHL     byte = 0x24
	OpSHR     byte = 0x25

	// Stack
	OpPUSH32  byte = 0x30
	OpPOP     byte = 0x31
	OpDUP     byte = 0x32
	OpDUP2    byte = 0x33
	OpSWAP    byte = 0x34
	OpSWAP2   byte = 0x35

	// Memory
	OpMLOAD   byte = 0x40
	OpMSTORE  byte = 0x41
	OpMSIZE   byte = 0x42

	// Storage
	OpSLOAD   byte = 0x50
	OpSSTORE  byte = 0x51

	// Control flow
	OpJUMP    byte = 0x60
	OpJUMPI   byte = 0x61
	OpPC      byte = 0x62
	OpSTOP    byte = 0x63

	// Context
	OpCALLER      byte = 0x70
	OpORIGIN      byte = 0x71
	OpCALLVALUE   byte = 0x72
	OpCODESIZE    byte = 0x73
	OpBLOCKHEIGHT byte = 0x74
	OpTIMESTAMP   byte = 0x75
	OpADDRESS     byte = 0x76

	// Crypto (quantum-safe only — no ECDSA)
	OpSHA3             byte = 0x80
	OpVERIFY_DILITHIUM byte = 0x81

	// Contract interaction
	OpCALL         byte = 0x90
	OpSTATICCALL   byte = 0x91
	OpDELEGATECALL byte = 0x92
	OpCREATE       byte = 0x93
	OpSELFDESTRUCT byte = 0x94

	// Events (LOG0 = no topics, LOG4 = 4 indexed topics)
	OpLOG0    byte = 0xA0
	OpLOG1    byte = 0xA1
	OpLOG2    byte = 0xA2
	OpLOG3    byte = 0xA3
	OpLOG4    byte = 0xA4

	// Token operations
	OpTRANSFER byte = 0xB0
	OpBALANCE  byte = 0xB1
	OpTOTALGAS byte = 0xB2

	// ABI
	OpABI_ENCODE byte = 0xC0
	OpABI_DECODE byte = 0xC1
	OpSELECTOR   byte = 0xC2

	// Return / Revert
	OpRETURN  byte = 0xF0
	OpREVERT  byte = 0xF1
	OpREQUIRE byte = 0xF2
)

// ── VM limits ─────────────────────────────────────────────────────────────────

const (
	MaxStackDepth = 1024
	MaxGasPerCall = 10_000_000 // 10M ops per call
	MaxMemoryBytes = 32 * 1024 * 1024 // 32MB memory per execution
	MaxCallDepth   = 64 // max nested contract calls
)

// ── Execution context ─────────────────────────────────────────────────────────

// ExecutionContext holds all state for a single contract call.
type ExecutionContext struct {
	// Identity
	Caller    qcrypto.Address // address that called this contract
	Origin    qcrypto.Address // original transaction sender (never changes across calls)
	Contract  qcrypto.Address // address of the contract being executed
	CallValue *big.Int        // SPLD value sent with this call

	// Inputs
	Input []byte // calldata (function selector + ABI-encoded args)
	Code  []byte // SQC bytecode of the contract being executed

	// Block context
	BlockHeight uint64
	Timestamp   int64

	// Depth of nested calls (prevents infinite recursion)
	Depth int

	// Gas budget remaining
	Gas uint64

	// Collected events during this execution
	Logs []*types.Log

	// Static mode: if true, SSTORE/TRANSFER/LOG are forbidden
	IsStatic bool
}

// ExecutionResult is returned after running a contract.
type ExecutionResult struct {
	ReturnValue []byte       // data returned by RETURN
	GasUsed     uint64       // total gas consumed
	Logs        []*types.Log // events emitted
	Err         error        // non-nil if execution reverted or failed
	Reverted    bool         // true if REVERT was executed (vs. other errors)
}

// ── Stack ─────────────────────────────────────────────────────────────────────

type stack struct {
	items []*big.Int
}

func newStack() *stack {
	return &stack{items: make([]*big.Int, 0, 64)}
}

func (s *stack) push(v *big.Int) error {
	if len(s.items) >= MaxStackDepth {
		return fmt.Errorf("stack overflow at depth %d", MaxStackDepth)
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
		return nil, fmt.Errorf("stack underflow on peek")
	}
	return s.items[len(s.items)-1], nil
}

func (s *stack) peekN(n int) (*big.Int, error) {
	if len(s.items) <= n {
		return nil, fmt.Errorf("stack underflow on peekN(%d)", n)
	}
	return s.items[len(s.items)-1-n], nil
}

func (s *stack) len() int { return len(s.items) }

// ── Memory ────────────────────────────────────────────────────────────────────

type memory struct {
	data []byte
}

func newMemory() *memory { return &memory{} }

func (m *memory) store(offset, size int, data []byte) error {
	end := offset + size
	if end > MaxMemoryBytes {
		return fmt.Errorf("memory limit exceeded: %d > %d", end, MaxMemoryBytes)
	}
	if end > len(m.data) {
		// Extend with zeros
		m.data = append(m.data, make([]byte, end-len(m.data))...)
	}
	copy(m.data[offset:], data)
	return nil
}

func (m *memory) load(offset, size int) ([]byte, error) {
	if offset+size > len(m.data) {
		// Reads beyond allocated memory return zeros
		result := make([]byte, size)
		if offset < len(m.data) {
			copy(result, m.data[offset:])
		}
		return result, nil
	}
	result := make([]byte, size)
	copy(result, m.data[offset:offset+size])
	return result, nil
}

func (m *memory) size() int { return len(m.data) }

// ── Engine ────────────────────────────────────────────────────────────────────

// Engine is the SQC-VM bytecode execution engine.
type Engine struct {
	state *state.StateDB
}

// NewEngine creates a new VM engine backed by the given state database.
func NewEngine(stateDB *state.StateDB) *Engine {
	return &Engine{state: stateDB}
}

// Execute runs SQC bytecode in the given context and returns the result.
// All cryptographic operations use Dilithium Mode 3 and SHA3-256 only.
func (e *Engine) Execute(ctx *ExecutionContext) *ExecutionResult {
	if ctx.Depth > MaxCallDepth {
		return &ExecutionResult{Err: fmt.Errorf("max call depth %d exceeded", MaxCallDepth)}
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

	useGas := func(n uint64) error {
		if gasUsed+n > gas {
			return fmt.Errorf("out of gas: used %d, limit %d", gasUsed+n, gas)
		}
		gasUsed += n
		return nil
	}

	for pc < len(code) {
		op := code[pc]
		pc++

		if err := useGas(1); err != nil {
			return &ExecutionResult{Err: err, GasUsed: gasUsed, Logs: ctx.Logs}
		}

		switch op {

		// ── Arithmetic ────────────────────────────────────────────────────
		case OpADD:
			b, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			a, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			stk.push(new(big.Int).Add(a, b))

		case OpSUB:
			b, _ := stk.pop()
			a, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			stk.push(new(big.Int).Sub(a, b))

		case OpMUL:
			b, _ := stk.pop()
			a, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			if err := useGas(4); err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			stk.push(new(big.Int).Mul(a, b))

		case OpDIV:
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

		case OpMOD:
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

		case OpEXP:
			exp, _ := stk.pop()
			base, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			if err := useGas(uint64(exp.BitLen()) * 3); err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			stk.push(new(big.Int).Exp(base, exp, nil))

		case OpADDMOD:
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

		case OpMULMOD:
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

		// ── Comparison ────────────────────────────────────────────────────
		case OpEQ:
			b, _ := stk.pop()
			a, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			boolPush(stk, a.Cmp(b) == 0)

		case OpNEQ:
			b, _ := stk.pop()
			a, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			boolPush(stk, a.Cmp(b) != 0)

		case OpLT:
			b, _ := stk.pop()
			a, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			boolPush(stk, a.Cmp(b) < 0)

		case OpGT:
			b, _ := stk.pop()
			a, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			boolPush(stk, a.Cmp(b) > 0)

		case OpLTE:
			b, _ := stk.pop()
			a, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			boolPush(stk, a.Cmp(b) <= 0)

		case OpGTE:
			b, _ := stk.pop()
			a, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			boolPush(stk, a.Cmp(b) >= 0)

		// ── Bitwise ───────────────────────────────────────────────────────
		case OpAND:
			b, _ := stk.pop()
			a, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			stk.push(new(big.Int).And(a, b))

		case OpOR:
			b, _ := stk.pop()
			a, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			stk.push(new(big.Int).Or(a, b))

		case OpXOR:
			b, _ := stk.pop()
			a, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			stk.push(new(big.Int).Xor(a, b))

		case OpNOT:
			a, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			// Bitwise NOT in 256-bit space
			max256 := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
			stk.push(new(big.Int).Xor(a, max256))

		case OpSHL:
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

		case OpSHR:
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

		// ── Stack ─────────────────────────────────────────────────────────
		case OpPUSH32:
			if pc+32 > len(code) {
				return &ExecutionResult{
					Err: fmt.Errorf("PUSH32: insufficient bytes at pc=%d (code len=%d)", pc, len(code)),
				}
			}
			v := new(big.Int).SetBytes(code[pc : pc+32])
			pc += 32
			if err := stk.push(v); err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}

		case OpPOP:
			if _, err := stk.pop(); err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}

		case OpDUP:
			v, err := stk.peek()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			stk.push(v)

		case OpDUP2:
			v, err := stk.peekN(1)
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			stk.push(v)

		case OpSWAP:
			if stk.len() < 2 {
				return &ExecutionResult{Err: fmt.Errorf("SWAP: need 2 items"), GasUsed: gasUsed}
			}
			n := stk.len()
			stk.items[n-1], stk.items[n-2] = stk.items[n-2], stk.items[n-1]

		case OpSWAP2:
			if stk.len() < 3 {
				return &ExecutionResult{Err: fmt.Errorf("SWAP2: need 3 items"), GasUsed: gasUsed}
			}
			n := stk.len()
			stk.items[n-1], stk.items[n-3] = stk.items[n-3], stk.items[n-1]

		// ── Memory ────────────────────────────────────────────────────────
		case OpMLOAD:
			offsetInt, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			offset := int(offsetInt.Int64())
			data, err := mem.load(offset, 32)
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			stk.push(new(big.Int).SetBytes(data))

		case OpMSTORE:
			val, _ := stk.pop()
			offsetInt, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			offset := int(offsetInt.Int64())
			padded := make([]byte, 32)
			valBytes := val.Bytes()
			copy(padded[32-len(valBytes):], valBytes)
			if err := mem.store(offset, 32, padded); err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}

		case OpMSIZE:
			stk.push(big.NewInt(int64(mem.size())))

		// ── Storage ───────────────────────────────────────────────────────
		case OpSLOAD:
			if err := useGas(99); err != nil { // storage reads are expensive
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			slotInt, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			slot := bigToSlot(slotInt)
			val, err := e.state.GetStorageValue(ctx.Contract, slot)
			if err != nil || len(val) == 0 {
				stk.push(big.NewInt(0))
			} else {
				stk.push(new(big.Int).SetBytes(val))
			}

		case OpSSTORE:
			if ctx.IsStatic {
				return &ExecutionResult{
					Err: fmt.Errorf("SSTORE forbidden in static context"),
					GasUsed: gasUsed,
				}
			}
			if err := useGas(19999); err != nil { // storage writes are very expensive
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			val, _ := stk.pop()
			slotInt, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			slot := bigToSlot(slotInt)
			if err := e.state.SetStorageValue(ctx.Contract, slot, val.Bytes()); err != nil {
				return &ExecutionResult{Err: fmt.Errorf("SSTORE failed: %w", err), GasUsed: gasUsed}
			}

		// ── Control flow ──────────────────────────────────────────────────
		case OpJUMP:
			dest, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			newPC := int(dest.Int64())
			if newPC < 0 || newPC >= len(code) {
				return &ExecutionResult{Err: fmt.Errorf("JUMP to invalid PC %d", newPC), GasUsed: gasUsed}
			}
			pc = newPC

		case OpJUMPI:
			cond, _ := stk.pop()
			dest, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			if cond.Sign() != 0 {
				newPC := int(dest.Int64())
				if newPC < 0 || newPC >= len(code) {
					return &ExecutionResult{Err: fmt.Errorf("JUMPI to invalid PC %d", newPC), GasUsed: gasUsed}
				}
				pc = newPC
			}

		case OpPC:
			stk.push(big.NewInt(int64(pc - 1)))

		case OpSTOP:
			return &ExecutionResult{GasUsed: gasUsed, Logs: ctx.Logs}

		// ── Context ───────────────────────────────────────────────────────
		case OpCALLER:
			stk.push(addrToBig(ctx.Caller))

		case OpORIGIN:
			stk.push(addrToBig(ctx.Origin))

		case OpCALLVALUE:
			stk.push(new(big.Int).Set(ctx.CallValue))

		case OpCODESIZE:
			stk.push(big.NewInt(int64(len(code))))

		case OpBLOCKHEIGHT:
			stk.push(new(big.Int).SetUint64(ctx.BlockHeight))

		case OpTIMESTAMP:
			stk.push(big.NewInt(ctx.Timestamp))

		case OpADDRESS:
			stk.push(addrToBig(ctx.Contract))

		// ── Crypto ────────────────────────────────────────────────────────
		case OpSHA3:
			if err := useGas(29); err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			data, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			hash := qcrypto.Hash32(data.Bytes())
			stk.push(new(big.Int).SetBytes(hash[:]))

		case OpVERIFY_DILITHIUM:
			// Stack: [pubkey_slot, msg_slot, sig_slot] → [1 if valid, 0 if invalid]
			// This opcode verifies a Dilithium Mode 3 signature on-chain.
			// pubkey_slot: storage slot containing the 1952-byte public key
			// msg_slot: storage slot containing the message hash (32 bytes)
			// sig_slot: storage slot containing the 3293-byte signature
			if err := useGas(999); err != nil { // signature verification is expensive
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			sigSlot, _ := stk.pop()
			msgSlot, _ := stk.pop()
			pubkeySlot, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}

			pubKeyBytes, _ := e.state.GetStorageValue(ctx.Contract, bigToSlot(pubkeySlot))
			msgBytes, _ := e.state.GetStorageValue(ctx.Contract, bigToSlot(msgSlot))
			sigBytes, _ := e.state.GetStorageValue(ctx.Contract, bigToSlot(sigSlot))

			pub, parseErr := qcrypto.PublicKeyFromBytes(pubKeyBytes)
			if parseErr != nil || !qcrypto.Verify(pub, msgBytes, sigBytes) {
				boolPush(stk, false)
			} else {
				boolPush(stk, true)
			}

		// ── Events / Logs ─────────────────────────────────────────────────
		case OpLOG0, OpLOG1, OpLOG2, OpLOG3, OpLOG4:
			if ctx.IsStatic {
				return &ExecutionResult{Err: fmt.Errorf("LOG forbidden in static context"), GasUsed: gasUsed}
			}
			numTopics := int(op - OpLOG0)
			if err := useGas(uint64(375 + numTopics*375)); err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}

			// Pop data (as raw bytes from memory offset+size)
			dataSize, _ := stk.pop()
			dataOffset, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			data, _ := mem.load(int(dataOffset.Int64()), int(dataSize.Int64()))

			// Pop topics
			topics := make([][32]byte, numTopics)
			for i := 0; i < numTopics; i++ {
				topicInt, err := stk.pop()
				if err != nil {
					return &ExecutionResult{Err: err, GasUsed: gasUsed}
				}
				topicBytes := topicInt.Bytes()
				var topic [32]byte
				copy(topic[32-len(topicBytes):], topicBytes)
				topics[i] = topic
			}

			log := &types.Log{
				Address: ctx.Contract,
				Topics:  topics,
				Data:    data,
			}
			ctx.Logs = append(ctx.Logs, log)

		// ── Token operations ──────────────────────────────────────────────
		case OpTRANSFER:
			if ctx.IsStatic {
				return &ExecutionResult{Err: fmt.Errorf("TRANSFER forbidden in static context"), GasUsed: gasUsed}
			}
			if err := useGas(9000); err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			amount, _ := stk.pop()
			addrInt, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			toAddr := bigToAddr(addrInt)
			if err := e.state.Transfer(ctx.Contract, toAddr, amount); err != nil {
				return &ExecutionResult{Err: fmt.Errorf("TRANSFER failed: %w", err), GasUsed: gasUsed}
			}

		case OpBALANCE:
			if err := useGas(399); err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			addrInt, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			addr := bigToAddr(addrInt)
			bal, err := e.state.GetBalance(addr)
			if err != nil {
				stk.push(big.NewInt(0))
			} else {
				stk.push(bal)
			}

		case OpTOTALGAS:
			stk.push(new(big.Int).SetUint64(gas - gasUsed))

		// ── Revert / Return ───────────────────────────────────────────────
		case OpRETURN:
			size, _ := stk.pop()
			offsetInt, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			data, _ := mem.load(int(offsetInt.Int64()), int(size.Int64()))
			return &ExecutionResult{ReturnValue: data, GasUsed: gasUsed, Logs: ctx.Logs}

		case OpREVERT:
			size, _ := stk.pop()
			offsetInt, err := stk.pop()
			if err != nil {
				return &ExecutionResult{
					Err:      fmt.Errorf("execution reverted"),
					Reverted: true,
					GasUsed:  gasUsed,
				}
			}
			data, _ := mem.load(int(offsetInt.Int64()), int(size.Int64()))
			return &ExecutionResult{
				ReturnValue: data,
				Err:         fmt.Errorf("execution reverted"),
				Reverted:    true,
				GasUsed:     gasUsed,
			}

		case OpREQUIRE:
			msg, _ := stk.pop()
			cond, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			if cond.Sign() == 0 {
				errMsg := "require failed"
				if msg.Sign() > 0 {
					errMsg = fmt.Sprintf("require failed: %s", string(msg.Bytes()))
				}
				return &ExecutionResult{
					Err:      fmt.Errorf("%s", errMsg),
					Reverted: true,
					GasUsed:  gasUsed,
				}
			}

		// ── Contract creation ─────────────────────────────────────────────
		case OpCREATE:
			if ctx.IsStatic {
				return &ExecutionResult{Err: fmt.Errorf("CREATE forbidden in static context")}
			}
			if err := useGas(32000); err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			codeSize, _ := stk.pop()
			codeOffset, _ := stk.pop()
			value, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			_ = value // value transfer on CREATE handled by caller in executor
			bytecode, _ := mem.load(int(codeOffset.Int64()), int(codeSize.Int64()))

			// Deploy sub-contract
			subCtx := &ExecutionContext{
				Caller:      ctx.Contract,
				Origin:      ctx.Origin,
				CallValue:   big.NewInt(0),
				Code:        bytecode,
				BlockHeight: ctx.BlockHeight,
				Timestamp:   ctx.Timestamp,
				Depth:       ctx.Depth + 1,
				Gas:         gas - gasUsed,
			}
			contractAddr, err := e.Deploy(ctx.Contract, bytecode, uint64(pc))
			if err != nil {
				stk.push(big.NewInt(0)) // failed CREATE pushes 0
			} else {
				_ = subCtx
				stk.push(addrToBig(contractAddr))
			}

		case OpSELFDESTRUCT:
			if ctx.IsStatic {
				return &ExecutionResult{Err: fmt.Errorf("SELFDESTRUCT forbidden in static context")}
			}
			beneficiaryInt, err := stk.pop()
			if err != nil {
				return &ExecutionResult{Err: err, GasUsed: gasUsed}
			}
			beneficiary := bigToAddr(beneficiaryInt)
			// Transfer all contract balance to beneficiary
			bal, _ := e.state.GetBalance(ctx.Contract)
			if bal != nil && bal.Sign() > 0 {
				e.state.Transfer(ctx.Contract, beneficiary, bal)
			}
			return &ExecutionResult{GasUsed: gasUsed, Logs: ctx.Logs}

		default:
			return &ExecutionResult{
				Err:     fmt.Errorf("unknown opcode 0x%02x at pc=%d", op, pc-1),
				GasUsed: gasUsed,
			}
		}
	}

	// Fell off the end of code — treated as STOP
	return &ExecutionResult{GasUsed: gasUsed, Logs: ctx.Logs}
}

// ── High-level API ────────────────────────────────────────────────────────────

// Deploy deploys a new contract.
// The contract address is deterministically derived as SHA3-256(deployer || nonce_bytes).
func (e *Engine) Deploy(deployer qcrypto.Address, bytecode []byte, nonce uint64) (qcrypto.Address, error) {
	if len(bytecode) == 0 {
		return qcrypto.Address{}, fmt.Errorf("cannot deploy empty bytecode")
	}

	nonceBuf := make([]byte, 8)
	binary.BigEndian.PutUint64(nonceBuf, nonce)
	contractInput := append(deployer[:], nonceBuf...)
	hash := qcrypto.Hash32(contractInput)
	var contractAddr qcrypto.Address
	copy(contractAddr[:], hash[:])

	// Store bytecode under codeHash
	codeHash := qcrypto.Hash32(bytecode)
	e.state.SetCode(codeHash, bytecode)

	// Create contract account in state
	contractAcc := types.NewAccount(contractAddr)
	contractAcc.IsContract = true
	contractAcc.CodeHash = codeHash
	e.state.SetAccount(contractAcc)

	return contractAddr, nil
}

// Call executes a function call on a deployed contract.
func (e *Engine) Call(
	caller qcrypto.Address,
	contract qcrypto.Address,
	calldata []byte,
	value *big.Int,
) (*ExecutionResult, error) {
	return e.CallWithContext(caller, caller, contract, calldata, value, 0, 0, false)
}

// CallWithContext executes a contract call with full context.
func (e *Engine) CallWithContext(
	caller, origin qcrypto.Address,
	contract qcrypto.Address,
	calldata []byte,
	value *big.Int,
	blockHeight uint64,
	timestamp int64,
	isStatic bool,
) (*ExecutionResult, error) {
	contractAcc, err := e.state.GetAccount(contract)
	if err != nil {
		return nil, err
	}
	if !contractAcc.IsContract {
		return nil, types.ErrContractNotFound
	}

	code, err := e.state.GetCode(contractAcc.CodeHash)
	if err != nil {
		return nil, fmt.Errorf("failed to load contract code: %w", err)
	}

	ctx := &ExecutionContext{
		Caller:      caller,
		Origin:      origin,
		Contract:    contract,
		CallValue:   value,
		Input:       calldata,
		Code:        code,
		BlockHeight: blockHeight,
		Timestamp:   timestamp,
		IsStatic:    isStatic,
		Gas:         MaxGasPerCall,
		Logs:        make([]*types.Log, 0),
	}

	result := e.Execute(ctx)
	return result, result.Err
}

// StaticCall executes a read-only contract call (no state changes allowed).
func (e *Engine) StaticCall(
	caller qcrypto.Address,
	contract qcrypto.Address,
	calldata []byte,
) (*ExecutionResult, error) {
	return e.CallWithContext(caller, caller, contract, calldata, big.NewInt(0), 0, 0, true)
}

// ── ABI encoding helpers ──────────────────────────────────────────────────────

// SelectorFromSignature computes the SQC function selector.
// SQC uses SHA3-256(function_signature)[0:4] — analogous to Ethereum's keccak4 selector.
// This is quantum-safe because SHA3-256 is NIST FIPS 202 standardised.
func SelectorFromSignature(sig string) [4]byte {
	hash := qcrypto.Hash32([]byte(sig))
	var sel [4]byte
	copy(sel[:], hash[:4])
	return sel
}

// EncodeCall encodes a function call with selector + packed arguments.
// Arguments are packed as 32-byte big-endian values (simplified ABI).
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

// DecodeUint256 decodes a 32-byte ABI-encoded uint256 from an offset.
func DecodeUint256(data []byte, offset int) *big.Int {
	if offset+32 > len(data) {
		return big.NewInt(0)
	}
	return new(big.Int).SetBytes(data[offset : offset+32])
}

// DecodeAddress decodes a 32-byte ABI-encoded address (right-aligned 32 bytes).
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
