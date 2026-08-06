// Package vm — SQC-VM Disassembler.
//
// Converts SQC bytecode into a human-readable listing.
// Useful for debugging contracts, auditing deployed bytecode, and
// inspecting what the compiler generated.
//
// Example output:
//   0000  PUSH1       0x01
//   0002  PUSH1       0x02
//   0004  ADD
//   0005  PUSH32      0x0000...0001  (32 bytes)
//   0038  SLOAD
//   0039  STOP
package vm

import (
	"encoding/hex"
	"fmt"
	"strings"
)

// Instruction represents a single decoded instruction.
type Instruction struct {
	// Offset is the byte position of this instruction in the bytecode.
	Offset int

	// Op is the opcode byte.
	Op byte

	// Name is the human-readable opcode name.
	Name string

	// Immediate holds the immediate data bytes (for PUSH opcodes).
	// Empty for non-PUSH opcodes.
	Immediate []byte

	// Size is the total byte size of this instruction (1 + len(Immediate)).
	Size int
}

// String returns a formatted disassembly line for this instruction.
func (inst *Instruction) String() string {
	if len(inst.Immediate) == 0 {
		return fmt.Sprintf("%04x  %s", inst.Offset, inst.Name)
	}
	hexStr := hex.EncodeToString(inst.Immediate)
	// Truncate very long immediates (PUSH32) to first 8 bytes + "..."
	if len(hexStr) > 20 {
		hexStr = hexStr[:20] + "…"
	}
	return fmt.Sprintf("%04x  %-16s 0x%s", inst.Offset, inst.Name, hexStr)
}

// Disassemble decodes a bytecode slice into a list of Instructions.
// Unknown opcodes are represented as "UNKNOWN_0xXX".
func Disassemble(bytecode []byte) []Instruction {
	var instructions []Instruction
	i := 0
	for i < len(bytecode) {
		op := bytecode[i]
		name := OpName(op)
		inst := Instruction{Offset: i, Op: op, Name: name, Size: 1}

		// Determine immediate size
		immSize := immediateSize(op)
		if immSize > 0 {
			end := i + 1 + immSize
			if end > len(bytecode) {
				// Truncated bytecode
				inst.Immediate = bytecode[i+1:]
				inst.Size = len(bytecode) - i
			} else {
				inst.Immediate = bytecode[i+1 : end]
				inst.Size = 1 + immSize
			}
		}

		instructions = append(instructions, inst)
		i += inst.Size
	}
	return instructions
}

// DisassembleString returns a complete human-readable disassembly of bytecode.
func DisassembleString(bytecode []byte) string {
	instructions := Disassemble(bytecode)
	lines := make([]string, len(instructions))
	for i, inst := range instructions {
		lines[i] = inst.String()
	}
	return strings.Join(lines, "\n")
}

// immediateSize returns the number of immediate bytes following an opcode.
// Only PUSH opcodes have immediate bytes.
func immediateSize(op byte) int {
	switch {
	case op == OpPUSH32:           // 0x30
		return 32
	case op >= OpPUSH1 && op <= OpPUSH31: // 0x52..0x70
		return int(op-OpPUSH1) + 1
	default:
		return 0
	}
}

// OpName returns the human-readable name for an opcode byte.
func OpName(op byte) string {
	switch {
	case op >= OpPUSH1 && op <= OpPUSH31:
		return fmt.Sprintf("PUSH%d", int(op-OpPUSH1)+1)
	case op == OpPUSH32:
		return "PUSH32"
	case op >= OpDUP1 && op <= OpDUP16:
		return fmt.Sprintf("DUP%d", int(op-OpDUP1)+1)
	case op >= OpSWAP1 && op <= OpSWAP16:
		return fmt.Sprintf("SWAP%d", int(op-OpSWAP1)+1)
	case op >= OpLOG0 && op <= OpLOG4:
		return fmt.Sprintf("LOG%d", int(op-OpLOG0))
	}

	switch op {
	case OpSTOP:             return "STOP"
	case OpADD:              return "ADD"
	case OpSUB:              return "SUB"
	case OpMUL:              return "MUL"
	case OpDIV:              return "DIV"
	case OpMOD:              return "MOD"
	case OpEXP:              return "EXP"
	case OpADDMOD:           return "ADDMOD"
	case OpMULMOD:           return "MULMOD"
	case OpISZERO:           return "ISZERO"
	case OpBYTE:             return "BYTE"
	case OpSIGNEXTEND:       return "SIGNEXTEND"
	case OpEQ:               return "EQ"
	case OpNEQ:              return "NEQ"
	case OpLT:               return "LT"
	case OpGT:               return "GT"
	case OpLTE:              return "LTE"
	case OpGTE:              return "GTE"
	case OpAND:              return "AND"
	case OpOR:               return "OR"
	case OpXOR:              return "XOR"
	case OpNOT:              return "NOT"
	case OpSHL:              return "SHL"
	case OpSHR:              return "SHR"
	case OpPOP:              return "POP"
	case OpMLOAD:            return "MLOAD"
	case OpMSTORE:           return "MSTORE"
	case OpMSTORE8:          return "MSTORE8"
	case OpMSIZE:            return "MSIZE"
	case OpSLOAD:            return "SLOAD"
	case OpSSTORE:           return "SSTORE"
	case OpJUMP:             return "JUMP"
	case OpJUMPI:            return "JUMPI"
	case OpPC:               return "PC"
	case OpJUMPDEST:         return "JUMPDEST"
	case OpCALLDATALOAD:     return "CALLDATALOAD"
	case OpCALLDATASIZE:     return "CALLDATASIZE"
	case OpCALLDATACOPY:     return "CALLDATACOPY"
	case OpCODECOPY:         return "CODECOPY"
	case OpRETURNDATASIZE:   return "RETURNDATASIZE"
	case OpRETURNDATACOPY:   return "RETURNDATACOPY"
	case OpCALLER:           return "CALLER"
	case OpORIGIN:           return "ORIGIN"
	case OpCALLVALUE:        return "CALLVALUE"
	case OpCODESIZE:         return "CODESIZE"
	case OpBLOCKHEIGHT:      return "BLOCKHEIGHT"
	case OpTIMESTAMP:        return "TIMESTAMP"
	case OpADDRESS:          return "ADDRESS"
	case OpGASLIMIT:         return "GASLIMIT"
	case OpSHA3:             return "SHA3"
	case OpVERIFY_DILITHIUM: return "VERIFY_DILITHIUM"
	case OpSHA3_MEM:         return "SHA3_MEM"
	case OpCALL:             return "CALL"
	case OpSTATICCALL:       return "STATICCALL"
	case OpDELEGATECALL:     return "DELEGATECALL"
	case OpCREATE:           return "CREATE"
	case OpSELFDESTRUCT:     return "SELFDESTRUCT"
	case OpCREATE2:          return "CREATE2"
	case OpTRANSFER:         return "TRANSFER"
	case OpBALANCE:          return "BALANCE"
	case OpTOTALGAS:         return "TOTALGAS"
	case OpABI_ENCODE:       return "ABI_ENCODE"
	case OpABI_DECODE:       return "ABI_DECODE"
	case OpSELECTOR:         return "SELECTOR"
	case OpRETURN:           return "RETURN"
	case OpREVERT:           return "REVERT"
	case OpREQUIRE:          return "REQUIRE"
	default:
		return fmt.Sprintf("UNKNOWN_0x%02X", op)
	}
}

// bytesToInt converts a big-endian byte slice to an int.
func bytesToInt(b []byte) int {
	v := 0
	for _, byt := range b {
		v = v*256 + int(byt)
	}
	return v
}

// FindJumpDests returns a set of all valid JUMPDEST positions in bytecode.
// This is used to validate JUMP/JUMPI targets before executing.
func FindJumpDests(bytecode []byte) map[int]bool {
	dests := make(map[int]bool)
	instructions := Disassemble(bytecode)
	for _, inst := range instructions {
		if inst.Op == OpJUMPDEST {
			dests[inst.Offset] = true
		}
	}
	return dests
}

// Validate checks bytecode for structural validity:
//   - No out-of-bounds PUSH immediates
//   - All JUMP/JUMPI targets are JUMPDEST (static analysis only)
//   - No obviously malformed sequences
//
// Returns a list of warnings (non-fatal issues) and errors (fatal issues).
func ValidateBytecode(bytecode []byte) (warnings []string, errors []string) {
	if len(bytecode) == 0 {
		errors = append(errors, "bytecode is empty")
		return
	}

	jumpDests := FindJumpDests(bytecode)
	instructions := Disassemble(bytecode)

	for i, inst := range instructions {
		// Check for truncated PUSH
		if immSize := immediateSize(inst.Op); immSize > 0 {
			if len(inst.Immediate) < immSize {
				errors = append(errors, fmt.Sprintf(
					"offset %04x: %s expects %d immediate bytes, got %d (truncated bytecode)",
					inst.Offset, inst.Name, immSize, len(inst.Immediate)))
			}
		}

		// Check for unknown opcodes
		if strings.HasPrefix(inst.Name, "UNKNOWN_") {
			warnings = append(warnings, fmt.Sprintf(
				"offset %04x: unknown opcode 0x%02X", inst.Offset, inst.Op))
		}

		// Check JUMP destinations (limited static analysis — dynamic jumps can't be checked)
		if (inst.Op == OpJUMP || inst.Op == OpJUMPI) && i > 0 {
			prev := instructions[i-1]
			if immSize := immediateSize(prev.Op); immSize > 0 && len(prev.Immediate) == immSize {
				// Previous instruction was a PUSH — we know the jump destination statically
				dest := bytesToInt(prev.Immediate)
				if !jumpDests[dest] {
					warnings = append(warnings, fmt.Sprintf(
						"offset %04x: %s target 0x%04x is not a JUMPDEST",
						inst.Offset, inst.Name, dest))
				}
			}
		}
	}

	return warnings, errors
}
