package compiler

import (
	"context"
	"fmt"
	"math/big"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
)

var (
	bigZero = big.NewInt(0)
	bigOne  = big.NewInt(1)
)

// Scratch memory layout used by codegen:
//
//	[0x00,0x20) — mapping key / return value
//	[0x20,0x40) — mapping base slot
//	[0x40,0x40+32n) — the function's n locals, one word each
//	[0x40+32n, …)   — the data words an `emit` lays out
//
// Locals sit above the mapping scratch so a mapping computation inside a loop
// cannot clobber the loop counter, and emit data sits above the locals so
// logging cannot clobber them either. n is the function's total declaration
// count (counted before emitting), so the emit region is fixed per function.
const (
	memKey     = 0x00
	memBase    = 0x20
	localsBase = 0x40
)

// localAddr is the memory offset of local slot i.
func localAddr(i int) int64 { return localsBase + int64(i)*32 }

// codegen holds the per-contract compilation state.
type codegen struct {
	a        *asm
	c        *contractNode
	states   map[string]*stateVar
	fn       *funcNode      // function currently being compiled
	params   map[string]int // param name → calldata word index
	labelSeq int            // per-compilation counter so Compile stays race-free

	// scopes is a stack of local name → slot maps, innermost last. Blocks push
	// and pop, so two sequential loops may each declare `i` without colliding,
	// while a redeclaration inside one scope is an error.
	scopes []map[string]int
	// nextLocal allocates slots monotonically: a slot is never reused, so a
	// function needs exactly countLocals(body) of them and no liveness
	// analysis is required to keep two locals apart.
	nextLocal int
	// emitBase is where this function's emit data words start, above its locals
	// and its mapping-slot cache.
	emitBase int64

	// loopBreakLabel and loopContinueLabel are the targets for break/continue
	// inside the innermost loop. Empty string when not inside a loop.
	loopBreakLabel    string
	loopContinueLabel string

	// inlineRetSlot and inlineRetLabel are used when inlining an internal
	// function call. When non-zero/non-empty, a `return expr` statement writes
	// expr to the slot and jumps to the label rather than emitting RETURN.
	inlineRetSlot  int
	inlineRetLabel string
	inlineActive   bool // true when we are inside an inlined call

	// assigned names every variable this function ever writes, so a mapping
	// keyed on one is never cached.
	assigned map[string]bool
	// slotIdx maps a reusable mapping access to its word in the cache region;
	// slotBase is where that region starts.
	slotIdx  map[string]int
	slotBase int64
	// slotLive marks the cached slots already written on every path reaching
	// here. Saved and restored around branches and loop bodies.
	slotLive map[string]bool

	// ctx bounds codegen: emittedItemBudget counts down every asm item and
	// inlineDepth bounds nested inlining. Recursion is rejected in the checker,
	// but an expanding acyclic call graph (f0→f1→f2→…) still grows bytecode
	// geometrically with no per-call source size, so a budget and a depth
	// ceiling are the only bound that make the deadline meaningful.
	ctx               context.Context
	emittedItemBudget int
	inlineDepth       int
}

// slotAddr is the memory offset of cache word i.
func (g *codegen) slotAddr(i int) int64 { return g.slotBase + int64(i)*32 }

// saveLive copies the live set, for restoring after a branch or loop body.
func (g *codegen) saveLive() map[string]bool {
	out := make(map[string]bool, len(g.slotLive))
	for k, v := range g.slotLive {
		out[k] = v
	}
	return out
}

func (g *codegen) pushScope() { g.scopes = append(g.scopes, map[string]int{}) }
func (g *codegen) popScope()  { g.scopes = g.scopes[:len(g.scopes)-1] }

// declareLocal allocates a slot for a new local in the innermost scope.
func (g *codegen) declareLocal(name string) (int, error) {
	if _, ok := g.params[name]; ok {
		return 0, fmt.Errorf("local %q shadows a parameter of the same name", name)
	}
	cur := g.scopes[len(g.scopes)-1]
	if _, ok := cur[name]; ok {
		return 0, fmt.Errorf("local %q is declared twice in the same scope", name)
	}
	// Shadowing is refused, as Solidity has refused it since 0.5.
	//
	// It is not a style rule here. One name resolving to two bindings broke two
	// things at once: the mapping-slot cache is keyed on the NAME, so the inner
	// binding was handed the outer one's derived slot and wrote to it; and an
	// initialiser like `uint256 s = s + 1` read the slot being declared —
	// zero — instead of the binding it shadows. Both are silent wrong-slot
	// writes, so the source reads correctly and nothing reverts.
	if _, ok := g.lookupLocal(name); ok {
		return 0, fmt.Errorf("local %q shadows an enclosing local of the same name", name)
	}
	if _, ok := g.states[name]; ok {
		return 0, fmt.Errorf("local %q shadows a state variable of the same name", name)
	}
	slot := g.nextLocal
	g.nextLocal++
	cur[name] = slot
	return slot, nil
}

// lookupLocal resolves a name against the scope stack, innermost first.
func (g *codegen) lookupLocal(name string) (int, bool) {
	for i := len(g.scopes) - 1; i >= 0; i-- {
		if slot, ok := g.scopes[i][name]; ok {
			return slot, true
		}
	}
	return 0, false
}

// ── mapping-slot caching ─────────────────────────────────────────────────────
//
// A mapping slot is SHA3-256(pad32(key) ‖ pad32(baseSlot)). The engine charges
// SHA3 per BYTE, so deriving one over 64 bytes costs 30 + 64*6 = 414 gas —
// more than the SLOAD it exists to address, and far more than the EVM's
// per-word 42. `m[k] = m[k] + 1` derived the identical slot twice; the token's
// transfer derived the sender's three times.
//
// The slot depends only on the key and the base slot, never on what is stored
// there, so it stays valid for as long as the KEY does. Caching one costs a
// DUP/PUSH/SWAP/MSTORE (12 gas) and each reuse a PUSH/MLOAD (6), against 414 to
// derive again.
//
// Two restrictions keep it honest:
//
//   - Only keys that cannot change are cached: msg.sender/msg.value, a literal,
//     or a name never assigned anywhere in the function. A reassigned key names
//     a different slot, so it is never cached at all.
//   - A cached word is only read where it was certainly written. The live set
//     is saved before a branch or loop body and restored after, so a slot
//     derived inside an `if` is never loaded by code that runs when the branch
//     did not.

// assignedNames collects every name used as an assignment target anywhere in a
// statement tree — locals, parameters and state variables alike. A name in here
// is not stable, so a mapping keyed on it cannot have its slot reused.
func assignedNames(body []stmt, into map[string]bool) {
	for _, s := range body {
		switch st := s.(type) {
		case *assignStmt:
			into[st.target.name] = true
		case *ifStmt:
			assignedNames(st.body, into)
			assignedNames(st.elseBody, into)
		case *forStmt:
			if st.init != nil {
				assignedNames([]stmt{st.init}, into)
			}
			if st.post != nil {
				assignedNames([]stmt{st.post}, into)
			}
			assignedNames(st.body, into)
		case *whileStmt:
			assignedNames(st.body, into)
		}
	}
}

// mappingCacheKey identifies a mapping access whose slot can be reused, or ""
// when it cannot.
func mappingCacheKey(mapping string, key expr, assigned map[string]bool) string {
	switch k := key.(type) {
	case *msgExpr:
		return mapping + "|msg." + k.field
	case *numberExpr:
		return mapping + "|#" + k.value.String()
	case *identExpr:
		if assigned[k.name] {
			return ""
		}
		return mapping + "|" + k.name
	}
	return ""
}

// walkMappingAccesses visits every mapping access in a statement tree.
func walkMappingAccesses(body []stmt, visit func(mapping string, key expr)) {
	var expr_ func(expr)
	expr_ = func(e expr) {
		switch ex := e.(type) {
		case *indexExpr:
			if ex.key != nil {
				visit(ex.name, ex.key)
				expr_(ex.key)
			}
		case *binaryExpr:
			expr_(ex.left)
			expr_(ex.right)
		case *unaryExpr:
			expr_(ex.operand)
		}
	}
	var stmts func([]stmt)
	stmts = func(ss []stmt) {
		for _, s := range ss {
			switch st := s.(type) {
			case *assignStmt:
				if st.target.key != nil {
					visit(st.target.name, st.target.key)
					expr_(st.target.key)
				}
				expr_(st.value)
			case *varDeclStmt:
				expr_(st.value)
			case *requireStmt:
				expr_(st.cond)
			case *returnStmt:
				expr_(st.value)
			case *emitStmt:
				for _, a := range st.args {
					expr_(a)
				}
			case *ifStmt:
				expr_(st.cond)
				stmts(st.body)
				stmts(st.elseBody)
			case *forStmt:
				if st.init != nil {
					stmts([]stmt{st.init})
				}
				if st.cond != nil {
					expr_(st.cond)
				}
				if st.post != nil {
					stmts([]stmt{st.post})
				}
				stmts(st.body)
			case *whileStmt:
				expr_(st.cond)
				stmts(st.body)
			}
		}
	}
	stmts(body)
}

// planSlotCache assigns a memory word to each reusable mapping slot that the
// function derives MORE THAN ONCE. A single-use slot is left alone: caching it
// would cost 12 gas to save a derivation that never happens again.
func planSlotCache(fn *funcNode, assigned map[string]bool) map[string]int {
	counts := map[string]int{}
	var order []string
	walkMappingAccesses(fn.body, func(mapping string, key expr) {
		ck := mappingCacheKey(mapping, key, assigned)
		if ck == "" {
			return
		}
		if counts[ck] == 0 {
			order = append(order, ck)
		}
		counts[ck]++
	})
	idx := map[string]int{}
	for _, ck := range order {
		if counts[ck] > 1 {
			idx[ck] = len(idx)
		}
	}
	return idx
}

// countLocals totals the declarations in a statement tree, including nested
// blocks. Slots are never reused, so this is exactly how many a function needs.
func countLocals(body []stmt) int {
	n := 0
	for _, s := range body {
		switch st := s.(type) {
		case *varDeclStmt:
			n++
		case *ifStmt:
			n += countLocals(st.body) + countLocals(st.elseBody)
		case *forStmt:
			if st.init != nil {
				n += countLocals([]stmt{st.init})
			}
			if st.post != nil {
				n += countLocals([]stmt{st.post})
			}
			n += countLocals(st.body)
		case *whileStmt:
			n += countLocals(st.body)
		}
	}
	return n
}

// MaxEmittedItems bounds the number of asm items codegen may produce for one
// contract. Inlined body expansion is the only way a small source becomes a
// large compile, and this is the ceiling that makes the compile-time budget
// (and, with ctx, the HTTP deadline) actually bind. Chosen far above any real
// contract in the subset — a 64 KiB bytecode ceiling needs, in practice, well
// under this many items — but well under what an expanding call graph can
// try to emit.
const MaxEmittedItems = 1 << 20

// MaxInlineDepth bounds nested inlining of internal functions. Recursive call
// graphs are rejected by the checker, so this is defence in depth: a chain of
// distinct internal calls (f0→f1→f2→…) is legal but grows bytecode with the
// product of body sizes, and this ceiling stops pathological chains.
const MaxInlineDepth = 64

// spend consumes one unit of the emitted-item budget and returns an error when
// the budget is exhausted or the compile context is cancelled. Called from the
// statement/expression walks so a runaway inlining expansion stops with a
// controlled error instead of running past the deadline.
func (g *codegen) spend() error {
	if err := g.ctx.Err(); err != nil {
		return err
	}
	if g.emittedItemBudget <= 0 {
		return fmt.Errorf("compiler: emitted-item budget exhausted (source expands too far)")
	}
	g.emittedItemBudget--
	return nil
}

func newCodegen(c *contractNode, ctx context.Context) *codegen {
	states := map[string]*stateVar{}
	for _, s := range c.states {
		states[s.name] = s
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return &codegen{a: newAsm(), c: c, states: states, ctx: ctx, emittedItemBudget: MaxEmittedItems}
}

// FunctionSelector is the 4-byte selector of a function signature, computed the
// same way the runtime dispatch does: the top 4 bytes of SHA3-256(signature).
func FunctionSelector(sig string) [4]byte {
	h := qcrypto.Hash32([]byte(sig))
	var s [4]byte
	copy(s[:], h[:4])
	return s
}

// signature renders a function's canonical ABI signature, e.g.
// "transfer(address,uint256)". `address` is the canonical spelling (I2);
// `sqaddress` is a legacy SDK alias that hashes to a different selector.
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
	g.scopes = []map[string]int{{}}
	g.nextLocal = 0

	g.assigned = map[string]bool{}
	assignedNames(fn.body, g.assigned)

	// The selector left on the stack by the dispatcher is dead here.
	g.a.op(vm.OpPOP)

	// Solidity lets a function assign to its own parameters, and calldata is
	// read-only, so an ASSIGNED parameter is copied into a memory word on
	// entry. One that is never written stays where it is: a calldata read costs
	// the same 3 gas as a memory read, so copying it would be a
	// PUSH/PUSH/CALLDATALOAD/MSTORE prologue paid on every call for nothing.
	for i, p := range fn.params {
		if !g.assigned[p.name] {
			continue // read straight from calldata (see genExpr)
		}
		slot := g.nextLocal
		g.nextLocal++
		g.scopes[0][p.name] = slot
		g.a.pushInt(localAddr(slot)) // MSTORE offset
		g.a.pushInt(int64(4 + i*32)) // calldata offset (4-byte selector prefix)
		g.a.op(vm.OpCALLDATALOAD)    // value
		g.a.op(vm.OpMSTORE)
	}
	// Memory above the locals holds this function's mapping-slot cache, and the
	// emit region sits above both.
	g.slotIdx = planSlotCache(fn, g.assigned)
	g.slotLive = map[string]bool{}
	g.slotBase = localAddr(g.nextLocal + countLocals(fn.body))
	g.emitBase = g.slotBase + int64(len(g.slotIdx))*32

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
	if err := g.spend(); err != nil {
		return err
	}
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
	case *varDeclStmt:
		return g.genVarDecl(st)
	case *forStmt:
		return g.genFor(st)
	case *whileStmt:
		return g.genLoop(nil, st.cond, nil, st.body)
	case *breakStmt:
		if g.loopBreakLabel == "" {
			return fmt.Errorf("break outside loop")
		}
		g.a.pushLabel(g.loopBreakLabel)
		g.a.op(vm.OpJUMP)
		return nil
	case *continueStmt:
		if g.loopContinueLabel == "" {
			return fmt.Errorf("continue outside loop")
		}
		g.a.pushLabel(g.loopContinueLabel)
		g.a.op(vm.OpJUMP)
		return nil
	case *callStmt:
		// Internal function call as a statement: inline the callee, discard return.
		_, err := g.genInlineCall(st.name, st.args, false)
		return err
	default:
		return fmt.Errorf("unhandled statement")
	}
}

// genVarDecl allocates a slot and stores the initialiser into it.
//
//	MSTORE pops value (top) then offset, so: <push offset>; <push value>; MSTORE.
func (g *codegen) genVarDecl(st *varDeclStmt) error {
	slot, err := g.declareLocal(st.name)
	if err != nil {
		return err
	}
	g.a.pushInt(localAddr(slot))
	if err := g.genExpr(st.value); err != nil {
		return err
	}
	g.a.op(vm.OpMSTORE)
	return nil
}

// genFor scopes the init clause to the loop, so two sequential loops may each
// declare their own counter.
func (g *codegen) genFor(st *forStmt) error {
	g.pushScope()
	defer g.popScope()
	return g.genLoop(st.init, st.cond, st.post, st.body)
}

// genLoop emits the shared shape of `for` and `while`:
//
//	init; top: cond? ISZERO -> end; body; post; JUMP top; end:
//
// A nil cond loops forever, which is not a hang: the VM's gas ceiling ends it
// and the call reverts.
func (g *codegen) genLoop(init stmt, cond expr, post stmt, body []stmt) error {
	if init != nil {
		if err := g.genStmt(init); err != nil {
			return err
		}
	}
	// Nothing derived inside the loop may be assumed on entry (the first
	// iteration has not run it) or after it (the loop may run zero times), so
	// the whole construct gets its own view and the caller's is restored.
	outer := g.saveLive()
	defer func() { g.slotLive = outer }()
	g.slotLive = map[string]bool{}

	top := g.freshLabel("loop")
	end := g.freshLabel("endloop")
	postLabel := g.freshLabel("looppost") // continue jumps here (before post)

	// Save and set break/continue targets for this loop.
	outerBreak := g.loopBreakLabel
	outerContinue := g.loopContinueLabel
	g.loopBreakLabel = end
	g.loopContinueLabel = postLabel
	defer func() {
		g.loopBreakLabel = outerBreak
		g.loopContinueLabel = outerContinue
	}()

	g.a.label(top)
	if cond != nil {
		if err := g.genExpr(cond); err != nil {
			return err
		}
		g.a.op(vm.OpISZERO) // [cond==0]
		// JUMPI pops cond (top) then dest: stack is [cond0], we need [dest, cond0].
		g.a.pushLabel(end)
		g.a.op(vm.OpSWAP1)
		g.a.op(vm.OpJUMPI)
	}
	g.pushScope()
	for _, s := range body {
		if err := g.genStmt(s); err != nil {
			g.popScope()
			return err
		}
	}
	g.popScope()
	// postLabel: continue jumps here; post runs before the next condition check.
	g.a.label(postLabel)
	if post != nil {
		if err := g.genStmt(post); err != nil {
			return err
		}
	}
	g.a.pushLabel(top)
	g.a.op(vm.OpJUMP)
	g.a.label(end)
	return nil
}

// genAssign writes to a scalar slot or a mapping element.
//
//	SSTORE pops value (top) then slot, so the sequence is: <push slot>; <push value>; SSTORE.
func (g *codegen) genAssign(st *assignStmt) error {
	// A local is a memory word, and cannot be indexed.
	if slot, ok := g.lookupLocal(st.target.name); ok {
		if st.target.key != nil {
			return fmt.Errorf("local %q is not a mapping", st.target.name)
		}
		g.a.pushInt(localAddr(slot))
		if err := g.genExpr(st.value); err != nil {
			return err
		}
		g.a.op(vm.OpMSTORE)
		return nil
	}
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
	if sv.isMapping {
		if err := g.genMappingSlot(sv, st.target.key); err != nil {
			return err
		}
		if err := g.genExpr(st.value); err != nil {
			return err
		}
		g.a.op(vm.OpSSTORE)
		return nil
	}
	if sv.isArray {
		// Array write: bounds check, compute slot, SSTORE.
		if err := g.genExpr(st.target.key); err != nil { // index
			return err
		}
		// Bounds check: require(index < arraySize, "array out of bounds")
		g.a.op(vm.OpDUP1)
		g.a.pushBig(new(big.Int).SetInt64(sv.arraySize))
		g.a.op(vm.OpLT)
		g.requireTop("array out of bounds")
		// Slot = base + index.
		g.a.pushInt(sv.slot)
		g.a.op(vm.OpADD)
		if err := g.genExpr(st.value); err != nil {
			return err
		}
		g.a.op(vm.OpSSTORE)
		return nil
	}
	return fmt.Errorf("%q is not a mapping or array", sv.name)
}

// genMappingSlot pushes SHA3-256( pad32(key) ‖ pad32(baseSlot) ) — a distinct,
// collision-resistant slot per (mapping, key). The two 32-byte words are laid
// into scratch memory, then SHA3_MEM hashes the 64-byte range.
//
//	SHA3_MEM pops size (top) then offset, so: <push offset>; <push size>; SHA3_MEM.
//	MSTORE pops value (top) then offset, so: <push offset>; <push value>; MSTORE.
func (g *codegen) genMappingSlot(sv *stateVar, key expr) error {
	ck := mappingCacheKey(sv.name, key, g.assigned)
	idx, cached := g.slotIdx[ck]
	if ck != "" && cached && g.slotLive[ck] {
		// Already derived on every path to here: load it back for 6 gas.
		g.a.pushInt(g.slotAddr(idx))
		g.a.op(vm.OpMLOAD)
		return nil
	}
	if err := g.emitMappingSlot(sv, key); err != nil {
		return err
	}
	if ck != "" && cached {
		// Keep a copy for the next access. Stack is [slot]; MSTORE pops value
		// then offset, so: DUP1 -> [slot, slot]; PUSH addr -> [slot, slot,
		// addr]; SWAP1 -> [slot, addr, slot]; MSTORE -> [slot].
		g.a.op(vm.OpDUP1)
		g.a.pushInt(g.slotAddr(idx))
		g.a.op(vm.OpSWAP1)
		g.a.op(vm.OpMSTORE)
		g.slotLive[ck] = true
	}
	return nil
}

// emitMappingSlot derives the slot unconditionally.
func (g *codegen) emitMappingSlot(sv *stateVar, key expr) error {
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

// genReturn writes the return value. When inside an inlined call, stores the
// value to the inline return slot and jumps to the inline return label instead
// of emitting a real RETURN opcode.
func (g *codegen) genReturn(st *returnStmt) error {
	if g.inlineActive {
		// Inlined context: write value to return slot and jump to exit label.
		g.a.pushInt(localAddr(g.inlineRetSlot))
		if err := g.genExpr(st.value); err != nil {
			return err
		}
		g.a.op(vm.OpMSTORE)
		g.a.pushLabel(g.inlineRetLabel)
		g.a.op(vm.OpJUMP)
		return nil
	}
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
		g.a.pushInt(g.emitBase + int64(i*32)) // offset, above this function's locals
		if err := g.genExpr(arg); err != nil {
			return err
		}
		g.a.op(vm.OpMSTORE)
	}
	sig := eventSignature(st.event, len(st.args))
	topic := qcrypto.Hash32([]byte(sig))
	g.a.pushBytes32(topic)
	g.a.pushInt(g.emitBase)               // data offset
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
	if err := g.genBlock(st.body); err != nil {
		return err
	}
	if len(st.elseBody) > 0 {
		g.a.pushLabel(end)
		g.a.op(vm.OpJUMP)
		g.a.label(skip)
		if err := g.genBlock(st.elseBody); err != nil {
			return err
		}
	}
	g.a.label(end)
	return nil
}

// genBlock emits a braced body in its own scope, so a local declared inside it
// does not leak out — and with its own view of the slot cache, so a slot
// derived inside a body that may not run is not loaded by code after it.
func (g *codegen) genBlock(body []stmt) error {
	g.pushScope()
	live := g.saveLive()
	defer func() { g.popScope(); g.slotLive = live }()
	for _, s := range body {
		if err := g.genStmt(s); err != nil {
			return err
		}
	}
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
	if err := g.spend(); err != nil {
		return err
	}
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
		// A local or an assigned parameter (memory), an unassigned parameter
		// (still in calldata), or a scalar state variable.
		if slot, ok := g.lookupLocal(ex.name); ok {
			g.a.pushInt(localAddr(slot))
			g.a.op(vm.OpMLOAD)
			return nil
		}
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
		if !ok {
			return fmt.Errorf("unknown state variable %q", ex.name)
		}
		if sv.isMapping {
			if err := g.genMappingSlot(sv, ex.key); err != nil {
				return err
			}
			g.a.op(vm.OpSLOAD)
			return nil
		}
		if sv.isArray {
			// Array read: bounds check, then slot = base + index.
			if err := g.genExpr(ex.key); err != nil {
				return err
			}
			// Bounds check: require(index < arraySize, "array out of bounds")
			g.a.op(vm.OpDUP1)
			g.a.pushBig(new(big.Int).SetInt64(sv.arraySize))
			g.a.op(vm.OpLT)
			g.requireTop("array out of bounds")
			// Slot = base + index (index still on stack from DUP1).
			g.a.pushInt(sv.slot)
			g.a.op(vm.OpADD)
			g.a.op(vm.OpSLOAD)
			return nil
		}
		return fmt.Errorf("%q is not a mapping or array", ex.name)
	case *binaryExpr:
		return g.genBinary(ex)
	case *unaryExpr:
		if err := g.genExpr(ex.operand); err != nil {
			return err
		}
		g.a.op(vm.OpISZERO) // logical not: 0 → 1, non-zero → 0
		return nil
	case *stringExpr:
		// Encode as bytes32: left-aligned, right-zero-padded. Max 32 chars.
		b := []byte(ex.value)
		if len(b) > 32 {
			b = b[:32]
		}
		padded := make([]byte, 32)
		copy(padded[:len(b)], b)
		g.a.pushBig(new(big.Int).SetBytes(padded))
		return nil
	case *callExpr:
		// Internal function call in expression position: inline and load return.
		retSlot, err := g.genInlineCall(ex.name, ex.args, true)
		if err != nil {
			return err
		}
		g.a.pushInt(localAddr(retSlot))
		g.a.op(vm.OpMLOAD)
		return nil
	default:
		return fmt.Errorf("unhandled expression")
	}
}

// genInlineCall inlines an internal (private) function call at the call site.
// Arguments are evaluated and written to fresh local slots, then the callee's
// body is emitted with those slots substituted for its parameters. A return
// inside the inlined body writes to inlineRetSlot and jumps to inlineRetLabel
// rather than emitting a real RETURN. If wantReturn is true, a slot is
// allocated for the return value and its index is returned.
func (g *codegen) genInlineCall(name string, args []expr, wantReturn bool) (int, error) {
	// Inlining is the recursion that turns a small source into an unbounded
	// compile, so it is the recursion that must be budgeted. Recursive call
	// graphs are rejected by the checker; an expanding acyclic graph is stopped
	// here by depth plus the per-item budget in genStmt/genExpr.
	g.inlineDepth++
	defer func() { g.inlineDepth-- }()
	if g.inlineDepth > MaxInlineDepth {
		return 0, fmt.Errorf("compiler: internal-call inlining exceeded %d levels", MaxInlineDepth)
	}
	if err := g.spend(); err != nil {
		return 0, err
	}

	// Find the callee among internal functions.
	var target *funcNode
	for _, fn := range g.c.funcs {
		if fn.name == name && fn.isInternal && len(fn.params) == len(args) {
			target = fn
			break
		}
	}
	if target == nil {
		return 0, fmt.Errorf("undefined internal function %q (wrong name, arg count, or not marked internal)", name)
	}

	// Allocate one local slot per argument; evaluate args before entering the
	// callee's scope so they see the caller's bindings, not the callee's.
	argSlots := make([]int, len(args))
	for i := range args {
		argSlots[i] = g.nextLocal
		g.nextLocal++
	}
	for i, arg := range args {
		g.a.pushInt(localAddr(argSlots[i]))
		if err := g.genExpr(arg); err != nil {
			return 0, fmt.Errorf("inline call %s arg %d: %w", name, i, err)
		}
		g.a.op(vm.OpMSTORE)
	}

	// Allocate a return-value slot if the callee has a return type.
	retSlot := 0
	if wantReturn && target.hasReturn {
		retSlot = g.nextLocal
		g.nextLocal++
	}

	// Enter a fresh scope with the callee's params bound to the arg slots.
	g.pushScope()
	for i, p := range target.params {
		g.scopes[len(g.scopes)-1][p.name] = argSlots[i]
	}

	// Save and set inline context: return stmts write to retSlot/retLabel.
	prevActive, prevSlot, prevLabel := g.inlineActive, g.inlineRetSlot, g.inlineRetLabel
	retLabel := g.freshLabel("inlineret")
	g.inlineActive, g.inlineRetSlot, g.inlineRetLabel = true, retSlot, retLabel

	// Emit the callee body.
	for _, s := range target.body {
		if err := g.genStmt(s); err != nil {
			g.popScope()
			g.inlineActive, g.inlineRetSlot, g.inlineRetLabel = prevActive, prevSlot, prevLabel
			return 0, fmt.Errorf("inline %s: %w", name, err)
		}
	}

	// Return label: fall-through from the body lands here; explicit returns jump here.
	g.a.label(retLabel)

	g.popScope()
	g.inlineActive, g.inlineRetSlot, g.inlineRetLabel = prevActive, prevSlot, prevLabel
	return retSlot, nil
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

	// The right operand sits behind that JUMPI, so on the short-circuit path it
	// does NOT run. A mapping slot derived here is therefore not written on
	// every path reaching the code after this expression, and must not be
	// marked live — exactly as for a branch body (genBlock) or a loop
	// (genLoop). Without this, a later access to that key loaded a
	// never-written memory word, read 0 as the slot, and wrote the contract's
	// FIRST state variable instead of the mapping.
	live := g.saveLive()
	if err := g.genExpr(ex.right); err != nil {
		return err
	}
	g.slotLive = live

	g.a.label(end)
	g.a.op(vm.OpISZERO) // normalize to 0/1
	g.a.op(vm.OpISZERO)
	return nil
}
