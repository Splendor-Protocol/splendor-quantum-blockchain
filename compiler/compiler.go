package compiler

import (
	"context"
	"encoding/binary"
	"fmt"
	"math/big"
	"strings"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
)

// ABIFunction is one entry point of a compiled contract.
type ABIFunction struct {
	Name      string
	Signature string
	Selector  [4]byte
	Params    []string // parameter type names, in order
	Returns   string   // "" when the function returns nothing
}

// ABIEvent is one event a compiled contract can emit.
type ABIEvent struct {
	Name      string
	Signature string
	Topic     [32]byte
	Args      int
}

// Contract is the result of compiling one source unit.
type Contract struct {
	Name      string
	Bytecode  []byte
	Functions []ABIFunction
	Events    []ABIEvent
}

// Compile turns QSol-subset source into deployable SQC-VM v4 bytecode plus an
// ABI describing how to call it. It is a pure function of the source text —
// deterministic, no I/O — so the same source always yields identical bytecode.
func Compile(src string) (*Contract, error) {
	return CompileContext(context.Background(), src)
}

// CompileContext is Compile that stops when ctx is cancelled.
func CompileContext(ctx context.Context, src string) (*Contract, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	toks, err := newLexer(src).tokenize()
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	node, err := newParser(toks).parseContract()
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := check(node); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	code, err := newCodegen(node, ctx).generate()
	if err != nil {
		return nil, err
	}

	out := &Contract{Name: node.name, Bytecode: code}
	for _, fn := range node.funcs {
		sig := signature(fn)
		f := ABIFunction{Name: fn.name, Signature: sig, Selector: FunctionSelector(sig)}
		for _, p := range fn.params {
			f.Params = append(f.Params, typeName(p.typ))
		}
		if fn.hasReturn {
			f.Returns = typeName(fn.returnType)
		}
		out.Functions = append(out.Functions, f)
	}
	if err := checkSelectorCollisions(out.Functions); err != nil {
		return nil, err
	}
	// Events are discovered from emit statements (the subset has no event
	// declarations); dedupe by signature.
	seen := map[string]bool{}
	for _, fn := range node.funcs {
		for _, s := range fn.body {
			collectEvents(s, seen, out)
		}
	}
	return out, nil
}

func collectEvents(s stmt, seen map[string]bool, out *Contract) {
	switch st := s.(type) {
	case *emitStmt:
		sig := eventSignature(st.event, len(st.args))
		if !seen[sig] {
			seen[sig] = true
			out.Events = append(out.Events, ABIEvent{
				Name: st.event, Signature: sig,
				Topic: qcrypto.Hash32([]byte(sig)), Args: len(st.args),
			})
		}
	case *ifStmt:
		// Both arms: an event emitted only from an else branch is still an
		// event this contract can log.
		for _, inner := range st.body {
			collectEvents(inner, seen, out)
		}
		for _, inner := range st.elseBody {
			collectEvents(inner, seen, out)
		}
	case *forStmt:
		for _, inner := range st.body {
			collectEvents(inner, seen, out)
		}
	case *whileStmt:
		for _, inner := range st.body {
			collectEvents(inner, seen, out)
		}
	}
}

// checkSelectorCollisions refuses a contract whose dispatcher could not reach
// one of its own functions.
//
// The dispatcher compares the incoming 4-byte selector against each function in
// source order and jumps to the first match, so two functions sharing a
// selector leave the second permanently unreachable — silently, and only for
// that contract. A selector is SHA3-256 of the signature truncated to 4 bytes,
// so a collision needs ~2^32 work to construct deliberately and will not happen
// by accident; the check exists because the failure it prevents is invisible.
func checkSelectorCollisions(fns []ABIFunction) error {
	bySelector := make(map[[4]byte]string, len(fns))
	for _, f := range fns {
		if prev, dup := bySelector[f.Selector]; dup {
			return fmt.Errorf("functions %s and %s share selector %x: "+
				"the dispatcher matches the first, so the second could never be called",
				prev, f.Signature, f.Selector)
		}
		bySelector[f.Selector] = f.Signature
	}
	return nil
}

// check is a small semantic pass: duplicate names, every referenced
// identifier resolving to a parameter or a state variable, and acyclic
// internal-call graphs.
func check(c *contractNode) error {
	states := map[string]*stateVar{}
	for _, s := range c.states {
		if states[s.name] != nil {
			return fmt.Errorf("duplicate state variable %q", s.name)
		}
		states[s.name] = s
	}
	funcs := map[string]bool{}
	for _, fn := range c.funcs {
		if funcs[signature(fn)] {
			return fmt.Errorf("duplicate function %q", signature(fn))
		}
		funcs[signature(fn)] = true
		locals := map[string]bool{}
		for _, p := range fn.params {
			locals[p.name] = true
		}
		for _, s := range fn.body {
			if err := checkStmt(s, states, locals); err != nil {
				return fmt.Errorf("function %s: %w", fn.name, err)
			}
		}
	}
	// Cycles and arity mismatches have to be verified before codegen, because
	// codegen inlines the callee body and would otherwise recurse without end.
	if err := checkCallGraph(c); err != nil {
		return err
	}
	return nil
}

// checkCallGraph verifies, on the resolved function map, that every internal
// call names a function with the same arity and that no chain of internal
// calls reaches back to its start. genInlineCall inlines bodies, so recursion
// is not a runtime call — it is unbounded compile-time expansion.
func checkCallGraph(c *contractNode) error {
	byName := map[string]*funcNode{}
	for _, fn := range c.funcs {
		byName[fn.name] = fn
	}
	// Resolve which function each (name, arity) call refers to, so the cycle
	// test follows real edges rather than names.
	edges := map[*funcNode][]*funcNode{}
	findCallee := func(name string, argc int) *funcNode {
		for _, fn := range c.funcs {
			if fn.name == name && fn.isInternal && len(fn.params) == argc {
				return fn
			}
		}
		return nil
	}
	var addStmts func(owner *funcNode, body []stmt) error
	var addExpr func(owner *funcNode, e expr) error
	addExpr = func(owner *funcNode, e expr) error {
		switch ex := e.(type) {
		case *callExpr:
			target := findCallee(ex.name, len(ex.args))
			for _, a := range ex.args {
				if err := addExpr(owner, a); err != nil {
					return err
				}
			}
			if target != nil {
				edges[owner] = append(edges[owner], target)
			}
		case *indexExpr:
			// `m[f(x)]`. The key is an expression like any other and can hold
			// a call; missing it let a cycle reach codegen, where it showed up
			// as an inlining-depth failure after 64 copies of the body had
			// already been emitted.
			if ex.key != nil {
				return addExpr(owner, ex.key)
			}
		case *binaryExpr:
			if err := addExpr(owner, ex.left); err != nil {
				return err
			}
			return addExpr(owner, ex.right)
		case *unaryExpr:
			return addExpr(owner, ex.operand)
		case *numberExpr, *stringExpr, *identExpr, *msgExpr, nil:
			// Leaves: nothing inside them can be a call. Listed rather than
			// defaulted so a new expression form fails this switch's audit
			// instead of quietly hiding calls from the cycle check.
		default:
			return fmt.Errorf("internal: call-graph walk does not handle %T", e)
		}
		return nil
	}
	addStmts = func(owner *funcNode, body []stmt) error {
		for _, s := range body {
			switch st := s.(type) {
			case *assignStmt:
				if st.target.key != nil {
					if err := addExpr(owner, st.target.key); err != nil {
						return err
					}
				}
				if err := addExpr(owner, st.value); err != nil {
					return err
				}
			case *requireStmt:
				if err := addExpr(owner, st.cond); err != nil {
					return err
				}
			case *returnStmt:
				if err := addExpr(owner, st.value); err != nil {
					return err
				}
			case *emitStmt:
				for _, a := range st.args {
					if err := addExpr(owner, a); err != nil {
						return err
					}
				}
			case *varDeclStmt:
				if err := addExpr(owner, st.value); err != nil {
					return err
				}
			case *callStmt:
				if target := findCallee(st.name, len(st.args)); target != nil {
					edges[owner] = append(edges[owner], target)
				}
				for _, a := range st.args {
					if err := addExpr(owner, a); err != nil {
						return err
					}
				}
			case *ifStmt:
				if err := addExpr(owner, st.cond); err != nil {
					return err
				}
				if err := addStmts(owner, st.body); err != nil {
					return err
				}
				if err := addStmts(owner, st.elseBody); err != nil {
					return err
				}
			case *forStmt:
				if st.init != nil {
					if err := addStmts(owner, []stmt{st.init}); err != nil {
						return err
					}
				}
				if st.cond != nil {
					if err := addExpr(owner, st.cond); err != nil {
						return err
					}
				}
				if st.post != nil {
					if err := addStmts(owner, []stmt{st.post}); err != nil {
						return err
					}
				}
				if err := addStmts(owner, st.body); err != nil {
					return err
				}
			case *whileStmt:
				if err := addExpr(owner, st.cond); err != nil {
					return err
				}
				if err := addStmts(owner, st.body); err != nil {
					return err
				}
			case *breakStmt, *continueStmt, nil:
				// Leaves: no expression inside them, so nothing can hide a call.
				// Listed rather than defaulted for the same reason addExpr lists
				// its leaves — a statement form added later must fail this
				// audit instead of quietly escaping the cycle check. A call the
				// check never sees is a recursive contract that reaches code
				// generation, which is what the budget exists to stop.
			default:
				return fmt.Errorf("internal: call-graph walk does not handle statement %T", s)
			}
		}
		return nil
	}
	for _, fn := range c.funcs {
		if err := addStmts(fn, fn.body); err != nil {
			return err
		}
	}

	// DFS over fn edges for a back edge.
	const (
		unvisited = iota
		visiting
		visited
	)
	state := map[*funcNode]int{}
	var visit func(fn *funcNode, path []string) error
	visit = func(fn *funcNode, path []string) error {
		switch state[fn] {
		case visiting:
			return fmt.Errorf("recursive internal-call graph: %s", strings.Join(path, " -> "))
		case visited:
			return nil
		}
		state[fn] = visiting
		path = append(path, fn.name)
		for _, next := range edges[fn] {
			if err := visit(next, path); err != nil {
				return err
			}
		}
		state[fn] = visited
		return nil
	}
	for _, fn := range c.funcs {
		if err := visit(fn, nil); err != nil {
			return err
		}
	}
	return nil
}

func checkStmt(s stmt, states map[string]*stateVar, locals map[string]bool) error {
	switch st := s.(type) {
	case *assignStmt:
		// A local is assignable too; only a name that is neither is unknown.
		if !locals[st.target.name] && states[st.target.name] == nil {
			return fmt.Errorf("assignment to unknown variable %q", st.target.name)
		}
		if st.target.key != nil {
			if err := checkExpr(st.target.key, states, locals); err != nil {
				return err
			}
		}
		return checkExpr(st.value, states, locals)
	case *varDeclStmt:
		// The initialiser is checked BEFORE the name is in scope, so
		// `uint256 x = x;` is caught rather than reading an uninitialised slot.
		if err := checkExpr(st.value, states, locals); err != nil {
			return err
		}
		locals[st.name] = true
		return nil
	case *forStmt:
		if st.init != nil {
			if err := checkStmt(st.init, states, locals); err != nil {
				return err
			}
		}
		if st.cond != nil {
			if err := checkExpr(st.cond, states, locals); err != nil {
				return err
			}
		}
		if st.post != nil {
			if err := checkStmt(st.post, states, locals); err != nil {
				return err
			}
		}
		for _, inner := range st.body {
			if err := checkStmt(inner, states, locals); err != nil {
				return err
			}
		}
		return nil
	case *whileStmt:
		if err := checkExpr(st.cond, states, locals); err != nil {
			return err
		}
		for _, inner := range st.body {
			if err := checkStmt(inner, states, locals); err != nil {
				return err
			}
		}
		return nil
	case *requireStmt:
		return checkExpr(st.cond, states, locals)
	case *returnStmt:
		return checkExpr(st.value, states, locals)
	case *emitStmt:
		for _, a := range st.args {
			if err := checkExpr(a, states, locals); err != nil {
				return err
			}
		}
		return nil
	case *ifStmt:
		if err := checkExpr(st.cond, states, locals); err != nil {
			return err
		}
		for _, inner := range st.body {
			if err := checkStmt(inner, states, locals); err != nil {
				return err
			}
		}
		for _, inner := range st.elseBody {
			if err := checkStmt(inner, states, locals); err != nil {
				return err
			}
		}
		return nil
	}
	return nil
}

func checkExpr(e expr, states map[string]*stateVar, locals map[string]bool) error {
	switch ex := e.(type) {
	case *numberExpr, *msgExpr:
		return nil
	case *identExpr:
		if locals[ex.name] {
			return nil
		}
		sv := states[ex.name]
		if sv == nil {
			return fmt.Errorf("unknown identifier %q", ex.name)
		}
		if sv.isMapping {
			return fmt.Errorf("mapping %q used without a key", ex.name)
		}
		return nil
	case *indexExpr:
		sv := states[ex.name]
		if sv == nil {
			return fmt.Errorf("unknown state variable %q", ex.name)
		}
		if sv.isMapping || sv.isArray {
			return checkExpr(ex.key, states, locals)
		}
		return fmt.Errorf("%q is not a mapping or array", ex.name)
	case *binaryExpr:
		if err := checkExpr(ex.left, states, locals); err != nil {
			return err
		}
		return checkExpr(ex.right, states, locals)
	case *unaryExpr:
		return checkExpr(ex.operand, states, locals)
	case *stringExpr:
		return nil // string literal always valid
	case *callExpr:
		// We can't fully resolve internal calls at check-time without a
		// function-map pass; genInlineCall does the resolution. Just check args.
		for _, a := range ex.args {
			if err := checkExpr(a, states, locals); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("unhandled expression in checker")
}

// EncodeCall builds calldata for one function: the 4-byte selector followed by
// each argument as a 32-byte big-endian word. Addresses are full 32-byte SQ
// words, so an address argument is its raw 32 bytes.
func EncodeCall(fn ABIFunction, args ...*big.Int) ([]byte, error) {
	if len(args) != len(fn.Params) {
		return nil, fmt.Errorf("%s expects %d args, got %d", fn.Name, len(fn.Params), len(args))
	}
	out := make([]byte, 4+32*len(args))
	copy(out[:4], fn.Selector[:])
	for i, a := range args {
		if a.Sign() < 0 {
			return nil, fmt.Errorf("arg %d is negative", i)
		}
		b := a.Bytes()
		if len(b) > 32 {
			return nil, fmt.Errorf("arg %d wider than 256 bits", i)
		}
		copy(out[4+i*32+32-len(b):4+(i+1)*32], b)
	}
	return out, nil
}

// EncodeAddressArg returns the 32-byte word for an SQ address argument.
func EncodeAddressArg(a qcrypto.Address) *big.Int { return new(big.Int).SetBytes(a[:]) }

// selectorU32 is a small helper used in tests/tools to view a selector as a
// number.
func selectorU32(s [4]byte) uint32 { return binary.BigEndian.Uint32(s[:]) }
