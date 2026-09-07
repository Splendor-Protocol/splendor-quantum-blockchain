package vm

import (
	"strings"
	"testing"
)

// abiOpcodes are the three the v4 table declared but never implemented.
var abiOpcodes = []byte{0x43, 0x44, 0x45}

// 0x43-0x45 were declared in the opcode table, named by the disassembler,
// priced by the gas table and advertised by GET /vm/opcodes — but no case in
// Execute ever ran one, so bytecode reaching them dies on the unknown-opcode
// path. Verified against the live devnet, whose /vm/opcodes still lists
// ABI_ENCODE, ABI_DECODE and SELECTOR.
//
// An opcode that is advertised and priced but cannot run is a trap: tooling
// prices it, validation blesses it, and the contract fails only once deployed.
// There is also nothing to implement them AGAINST — this chain's calling
// convention is defined by the compiler (32-byte words, SHA3-256 selectors),
// not by consensus, so freezing an encoding into an opcode would pick a
// convention before one exists. SELECTOR is four bytes of CALLDATALOAD+SHR the
// compiler already emits.
//
// So they are removed from the advertised surface. Execution is unaffected:
// they failed as unknown opcodes before and they fail as unknown opcodes now.
// They can come back when something specifies and implements them.
func TestUnimplementedABIOpcodesAreNotAdvertised(t *testing.T) {
	for _, op := range abiOpcodes {
		if name := OpName(op); !strings.HasPrefix(name, "UNKNOWN_") {
			t.Errorf("opcode 0x%02X is advertised as %q but no handler executes it", op, name)
		}
	}
}

// Validation must now say so, which moves the failure from "reverts in
// production" to "flagged before deploy" — the point of removing the names.
func TestValidateFlagsTheUnimplementedABIOpcodes(t *testing.T) {
	for _, op := range abiOpcodes {
		warnings, _ := ValidateBytecode([]byte{op})
		found := false
		for _, w := range warnings {
			if strings.Contains(w, "unknown opcode") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("bytecode using 0x%02X drew no unknown-opcode warning: %v", op, warnings)
		}
	}
}

// Regression guard on the property that makes the removal safe: these bytes
// were never executable, so nothing that used to run stops running.
func TestUnimplementedABIOpcodesStillDoNotExecute(t *testing.T) {
	for _, op := range abiOpcodes {
		ctx := &ExecutionContext{
			Code: []byte{op},
			Gas:  MaxGasPerCall,
		}
		res := NewEngine(nil).Execute(ctx)
		if res.Err == nil {
			t.Errorf("opcode 0x%02X executed without error; it has no handler", op)
		}
	}
}
