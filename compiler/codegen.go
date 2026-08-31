package compiler

import (
	"fmt"
	"math/big"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
)

var (
	bigZero = big.NewInt(0)
	bigOne  = big.NewInt(1)
)

// Scratch memory layout used by codegen. The compiled contract only ever holds
// live values on the stack, so these fixed slots are safe to reuse between
// statements:
//
//	[0x00,0x20) — mapping key / return value / log word 0
//	[0x20,0x40) — mapping base slot
//
// emit uses a growing region from 0x00 for its data words; it never overlaps a
// live mapping computation because an emit statement evaluates its args to the
// stack first (see genEmit).
const (
	memKey  = 0x00
	memBase = 0x20
)

// codegen holds the per-contract compilation state.
type codegen struct {
	a        *asm
	c        *contractNode
	states   map[string]*stateVar
	fn       *funcNode      // function currently being compiled
	params   map[string]int // param name → calldata word index
	labelSeq int            // per-compilation counter so Compile stays race-free
}

func newCodegen(c *contractNode) *codegen {
	states := map[string]*stateVar{}
	for _, s := range c.states {
		states[s.name] = s
	}
	return &codegen{a: newAsm(), c: c, states: states}
}

// FunctionSelector is the 4-byte selector of a function signature, computed the
// same way the runtime dispatch does: the top 4 bytes of SHA3-256(signature).
func FunctionSelector(sig string) [4]byte {
	h := qcrypto.Hash32([]byte(sig))
	var s [4]byte
	copy(s[:], h[:4])
	return s
}

// signature renders a function's canonical signature, e.g. "transfer(address,uint256)".
func signature(fn *funcNode) string {
	sig := fn.name + "("
	for i, p := range fn.params {
		if i > 0 {
			sig += ","
		}
		sig += typeName(p.typ)
	}
	return sig + ")"
}

func typeName(t varType) string {
	switch t {
	case typeAddress:
		return "address"
	case typeBool:
		return "bool"
	default:
		return "uint256"
	}
}

// eventSignature renders an event signature from its argument expressions'
// inferred types. For the subset every logged value is a 256-bit word, so the
// signature is fixed to the arity; the topic is SHA3-256 of "Name(uint256,…)".
func eventSignature(name string, argc int) string {
	sig := name + "("
	for i := 0; i < argc; i++ {
		if i > 0 {
			sig += ","
		}
		sig += "uint256"
	}
	return sig + ")"
}

// generate emits the whole contract: a selector dispatcher that jumps to each
// function body, then the bodies.
func (g *codegen) generate() ([]byte, error) {
	// Dispatcher: load the 4-byte selector from calldata[0:4].
	g.a.pushInt(0)
	g.a.op(vm.OpCALLDATALOAD) // 32-byte word at offset 0
	g.a.pushInt(224)
	g.a.op(vm.OpSHR) // selector now in the low 4 bytes

	for i, fn := range g.c.funcs {
		sel := FunctionSelector(signature(fn))
		selInt := new(big.Int).SetBytes(sel[:])
		// stack: [selector]
		g.a.pushLabel(fnLabel(i)) // [selector, dest]
		g.a.op(vm.OpDUP1 + 1)     // DUP2: [selector, dest, selector]
		g.a.pushBig(selInt)       // [selector, dest, selector, selID]
		g.a.op(vm.OpEQ)           // [selector, dest, match]
		g.a.op(vm.OpJUMPI)        // pops match, dest; jumps if match. [selector]
	}
	// No function matched: revert with empty data.
	g.a.pushInt(0)
	g.a.pushInt(0)
	g.a.op(vm.OpREVERT)

	for i, fn := range g.c.funcs {
		g.a.label(fnLabel(i))
		if err := g.genFunc(fn); err != nil {
			return nil, fmt.Errorf("function %s: %w", fn.name, err)
		}
	}
	return g.a.assemble()
}

func fnLabel(i int) string { return fmt.Sprintf("fn%d", i) }

func (g *codegen) genFunc(fn *funcNode) error {
	g.fn = fn
	g.params = map[string]int{}
	for i, p := range fn.params {
		g.params[p.name] = i
	}
	// The selector left on the stack by the dispatcher is dead here.
	g.a.op(vm.OpPOP)

	for _, s := range fn.body {
		if err := g.genStmt(s); err != nil {
			return err
		}
	}
	// A function with no explicit return still halts cleanly.
	g.a.op(vm.OpSTOP)
	return nil
}

func (g *codegen) genStmt(s stmt) error {
	switch st := s.(type) {
	case *assignStmt:
		return g.genAssign(st)
	case *requireStmt:
		return g.genRequire(st)
	case *returnStmt:
		return g.genReturn(st)
	case *emitStmt:
		return g.genEmit(st)
	case *ifStmt:
		return g.genIf(st)
	default:
		return fmt.Errorf("unhandled statement")
	}
}

// genAssign writes to a scalar slot or a mapping element.
//
//	SSTORE pops value (top) then slot, so the sequence is: <push slot>; <push value>; SSTORE.
func (g *codegen) genAssign(st *assignStmt) error {
	sv, ok := g.states[st.target.name]
	if !ok {
		return fmt.Errorf("assignment to unknown state variable %q", st.target.name)
	}
	if st.target.key == nil {
		if sv.isMapping {
			return fmt.Errorf("%q is a mapping and needs a key", sv.name)
		}
		g.a.pushInt(sv.slot) // slot
		if err := g.genExpr(st.value); err != nil {
			return err
		}
		g.a.op(vm.OpSSTORE)
		return nil
	}
	if !sv.isMapping {
		return fmt.Errorf("%q is not a mapping", sv.name)
	}
	if err := g.genMappingSlot(sv, st.target.key); err != nil { // pushes slot
		return err
	}
	if err := g.genExpr(st.value); err != nil { // pushes value on top
		return err
	}
	g.a.op(vm.OpSSTORE)
	return nil
}

// genMappingSlot pushes SHA3-256( pad32(key) ‖ pad32(baseSlot) ) — a distinct,
// collision-resistant slot per (mapping, key). The two 32-byte words are laid
// into scratch memory, then SHA3_MEM hashes the 64-byte range.
//
//	SHA3_MEM pops size (top) then offset, so: <push offset>; <push size>; SHA3_MEM.
//	MSTORE pops value (top) then offset, so: <push offset>; <push value>; MSTORE.
func (g *codegen) genMappingSlot(sv *stateVar, key expr) error {
	// mem[memKey] = key
	g.a.pushInt(memKey)
	if err := g.genExpr(key); err != nil {
		return err
	}
	g.a.op(vm.OpMSTORE)
	// mem[memBase] = baseSlot
	g.a.pushInt(memBase)
	g.a.pushInt(sv.slot)
	g.a.op(vm.OpMSTORE)
	// hash mem[0:64]
	g.a.pushInt(memKey)   // offset
	g.a.pushInt(0x40)     // size = 64
	g.a.op(vm.OpSHA3_MEM) // → slot
	return nil
}

// genRequire reverts when cond is zero, with the statement's reason (possibly
// empty) as the REQUIRE message word.
func (g *codegen) genRequire(st *requireStmt) error {
	if err := g.genExpr(st.cond); err != nil {
		return err
	}
	g.requireTop(st.reason)
	return nil
}

// genReturn writes the value to memory[0:32] and returns that word.
//
//	RETURN pops size (top) then offset, so: <push offset>; <push size>; RETURN.
func (g *codegen) genReturn(st *returnStmt) error {
	g.a.pushInt(0) // mem offset
	if err := g.genExpr(st.value); err != nil {
		return err
	}
	g.a.op(vm.OpMSTORE)
	g.a.pushInt(0)  // offset
	g.a.pushInt(32) // size
	g.a.op(vm.OpRETURN)
	return nil
}

// genEmit lays each argument into memory as a 32-byte word, then LOG1 with
// topic0 = SHA3-256(eventSignature). Args are materialised to memory in order.
//
//	LOG1 pops size (top), offset, then one topic, so:
//	<push topic0>; <push offset=0>; <push size=32*n>; LOG1.
func (g *codegen) genEmit(st *emitStmt) error {
	for i, arg := range st.args {
		g.a.pushInt(int64(i * 32)) // offset
		if err := g.genExpr(arg); err != nil {
			return err
		}
		g.a.op(vm.OpMSTORE)
	}
	sig := eventSignature(st.event, len(st.args))
	topic := qcrypto.Hash32([]byte(sig))
	g.a.pushBytes32(topic)
	g.a.pushInt(0)                        // data offset
	g.a.pushInt(int64(len(st.args) * 32)) // data size
	g.a.op(vm.OpLOG1)
	return nil
}

// genIf runs body when cond is non-zero, elseBody (if any) otherwise.
// Implemented as: if cond==0 jump to else/end; body; jump end; else; end.
func (g *codegen) genIf(st *ifStmt) error {
	end := g.freshLabel("endif")
	skip := end
	if len(st.elseBody) > 0 {
		skip = g.freshLabel("else")
	}
	if err := g.genExpr(st.cond); err != nil {
		return err
	}
	g.a.op(vm.OpISZERO) // [cond==0]
	// JUMPI pops cond (top) then dest, so: <push dest>; <cond already on stack>… reorder:
	// stack is [cond==0]; we need [dest, cond==0].
	g.a.pushLabel(skip) // [cond0, dest]
	g.a.op(vm.OpSWAP1)  // [dest, cond0]
	g.a.op(vm.OpJUMPI)  // jump past the body if cond was zero
	for _, s := range st.body {
		if err := g.genStmt(s); err != nil {
			return err
		}
	}
	if len(st.elseBody) > 0 {
		g.a.pushLabel(end)
		g.a.op(vm.OpJUMP)
		g.a.label(skip)
		for _, s := range st.elseBody {
			if err := g.genStmt(s); err != nil {
				return err
			}
		}
	}
	g.a.label(end)
	return nil
}

func (g *codegen) freshLabel(prefix string) string {
	g.labelSeq++
	return fmt.Sprintf("%s_%d", prefix, g.labelSeq)
}

// reasonWord encodes a ≤32-byte message as the REQUIRE opcode's message word
// (the engine renders it with string(msg.Bytes())).
func reasonWord(s string) *big.Int { return new(big.Int).SetBytes([]byte(s)) }

// requireTop emits REQUIRE against the value on top of the stack, consuming it.
//
//	REQUIRE pops msg (top) then cond, so: <cond on stack>; <push msg>; REQUIRE.
func (g *codegen) requireTop(reason string) {
	g.a.pushBig(reasonWord(reason))
	g.a.op(vm.OpREQUIRE)
}

// genExpr pushes the value of e onto the stack.
func (g *codegen) genExpr(e expr) error {
	switch ex := e.(type) {
	case *numberExpr:
		g.a.pushBig(ex.value)
		return nil
	case *msgExpr:
		switch ex.field {
		case "sender":
			g.a.op(vm.OpCALLER)
		case "value":
			g.a.op(vm.OpCALLVALUE)
		}
		return nil
	case *identExpr:
		// A parameter, or a scalar state variable.
		if idx, ok := g.params[ex.name]; ok {
			g.a.pushInt(int64(4 + idx*32)) // calldata offset (4-byte selector prefix)
			g.a.op(vm.OpCALLDATALOAD)
			return nil
		}
		sv, ok := g.states[ex.name]
		if !ok {
			return fmt.Errorf("unknown identifier %q", ex.name)
		}
		if sv.isMapping {
			return fmt.Errorf("mapping %q used without a key", ex.name)
		}
		g.a.pushInt(sv.slot)
		g.a.op(vm.OpSLOAD)
		return nil
	case *indexExpr:
		sv, ok := g.states[ex.name]
		if !ok || !sv.isMapping {
			return fmt.Errorf("%q is not a mapping", ex.name)
		}
		if err := g.genMappingSlot(sv, ex.key); err != nil {
			return err
		}
		g.a.op(vm.OpSLOAD)
		return nil
	case *binaryExpr:
		return g.genBinary(ex)
	case *unaryExpr:
		if err := g.genExpr(ex.operand); err != nil {
			return err
		}
		g.a.op(vm.OpISZERO) // logical not: 0 → 1, non-zero → 0
		return nil
	default:
		return fmt.Errorf("unhandled expression")
	}
}

// genBinary evaluates left then right (left lower on the stack) and applies the
// operator. Every VM binary op computes `a OP b` where b is the top of stack,
// which is exactly right = top, left = below.
//
// Arithmetic is CHECKED, matching Solidity ≥0.8: overflow, underflow and
// division by zero revert (via REQUIRE, so the reason reaches the receipt)
// instead of wrapping or yielding 0 as the raw VM ops would.
func (g *codegen) genBinary(ex *binaryExpr) error {
	// && and || short-circuit, so they evaluate their own operands.
	if ex.opText == "&&" || ex.opText == "||" {
		return g.genLogical(ex)
	}
	if err := g.genExpr(ex.left); err != nil {
		return err
	}
	if err := g.genExpr(ex.right); err != nil {
		return err
	}
	switch ex.opText {
	case "+":
		// s = l + r; require(s >= l).
		// [l, r] DUP2 ADD → [l, s]; DUP1 SWAP2 → [s, s, l]; GTE → [s, s>=l].
		g.a.op(vm.OpDUP1 + 1) // DUP2
		g.a.op(vm.OpADD)
		g.a.op(vm.OpDUP1)
		g.a.op(vm.OpSWAP1 + 1) // SWAP2
		g.a.op(vm.OpGTE)
		g.requireTop("overflow")
	case "-":
		// require(l >= r) BEFORE subtracting.
		// [l, r] DUP2 DUP2 → [l, r, l, r]; GTE → [l, r, l>=r].
		g.a.op(vm.OpDUP1 + 1) // DUP2
		g.a.op(vm.OpDUP1 + 1) // DUP2
		g.a.op(vm.OpGTE)
		g.requireTop("underflow")
		g.a.op(vm.OpSUB)
	case "*":
		// s = l * r; require(l == 0 || s / l == r).
		// [l, r] DUP2 DUP2 MUL → [l, r, s];
		// DUP1 DUP4 DIV → [l, r, s, s/l] (DIV yields 0 when l == 0);
		// DUP3 EQ → [l, r, s, q==r]; DUP4 ISZERO OR → [l, r, s, ok];
		// require; SWAP2 POP POP → [s].
		g.a.op(vm.OpDUP1 + 1) // DUP2
		g.a.op(vm.OpDUP1 + 1) // DUP2
		g.a.op(vm.OpMUL)
		g.a.op(vm.OpDUP1)
		g.a.op(vm.OpDUP1 + 3) // DUP4
		g.a.op(vm.OpDIV)
		g.a.op(vm.OpDUP1 + 2) // DUP3
		g.a.op(vm.OpEQ)
		g.a.op(vm.OpDUP1 + 3) // DUP4
		g.a.op(vm.OpISZERO)
		g.a.op(vm.OpOR)
		g.requireTop("overflow")
		g.a.op(vm.OpSWAP1 + 1) // SWAP2
		g.a.op(vm.OpPOP)
		g.a.op(vm.OpPOP)
	case "/":
		// require(r != 0); the VM's DIV would yield 0 silently.
		g.a.op(vm.OpDUP1)
		g.requireTop("division by zero")
		g.a.op(vm.OpDIV)
	case "%":
		g.a.op(vm.OpDUP1)
		g.requireTop("division by zero")
		g.a.op(vm.OpMOD)
	case "==":
		g.a.op(vm.OpEQ)
	case "!=":
		g.a.op(vm.OpNEQ)
	case "<":
		g.a.op(vm.OpLT)
	case "<=":
		g.a.op(vm.OpLTE)
	case ">":
		g.a.op(vm.OpGT)
	case ">=":
		g.a.op(vm.OpGTE)
	default:
		return fmt.Errorf("unhandled operator %q", ex.opText)
	}
	return nil
}

// genLogical emits short-circuit && / ||: the right operand is not evaluated
// when the left already decides the result (so `b != 0 && a / b > 1` never
// divides by zero). The result is normalized to 0/1.
func (g *codegen) genLogical(ex *binaryExpr) error {
	end := g.freshLabel("bool")
	if err := g.genExpr(ex.left); err != nil {
		return err
	}
	g.a.op(vm.OpDUP1) // [a, a]
	if ex.opText == "&&" {
		g.a.op(vm.OpISZERO) // [a, a==0] — skip right when a is false
	}
	g.a.pushLabel(end) // [a, c, dest]
	g.a.op(vm.OpSWAP1) // [a, dest, c]
	g.a.op(vm.OpJUMPI) // [a] — jumped: a decides the result
	g.a.op(vm.OpPOP)   // []
	if err := g.genExpr(ex.right); err != nil {
		return err
	}
	g.a.label(end)
	g.a.op(vm.OpISZERO) // normalize to 0/1
	g.a.op(vm.OpISZERO)
	return nil
}
