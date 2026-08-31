package compiler

import (
	"encoding/binary"
	"fmt"
	"math/big"

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
	toks, err := newLexer(src).tokenize()
	if err != nil {
		return nil, err
	}
	node, err := newParser(toks).parseContract()
	if err != nil {
		return nil, err
	}
	if err := check(node); err != nil {
		return nil, err
	}
	code, err := newCodegen(node).generate()
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
		for _, inner := range st.body {
			collectEvents(inner, seen, out)
		}
	}
}

// check is a small semantic pass: duplicate names, and every referenced
// identifier resolving to a parameter or a state variable.
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
		if sv == nil || !sv.isMapping {
			return fmt.Errorf("%q is not a mapping", ex.name)
		}
		return checkExpr(ex.key, states, locals)
	case *binaryExpr:
		if err := checkExpr(ex.left, states, locals); err != nil {
			return err
		}
		return checkExpr(ex.right, states, locals)
	case *unaryExpr:
		return checkExpr(ex.operand, states, locals)
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
