package compiler

import "math/big"

// The AST for the QSol subset. Every value type is a 256-bit word; `address`
// and `bool` are that word used differently, not distinct machine types.

type varType uint8

const (
	typeUint varType = iota
	typeAddress
	typeBool
)

// contractNode is a whole `contract Name { ... }`.
type contractNode struct {
	name   string
	states []*stateVar
	funcs  []*funcNode
}

// stateVar is one storage declaration. A scalar occupies one sequential slot;
// a mapping reserves its slot as the base of a SHA3-256-derived keyspace.
type stateVar struct {
	name      string
	slot      int64
	isMapping bool
	keyType   varType // mapping key type
	valType   varType // scalar type, or mapping value type
}

// funcNode is one function.
type funcNode struct {
	name       string
	params     []param
	hasReturn  bool
	returnType varType
	body       []stmt
}

type param struct {
	name string
	typ  varType
}

// ── statements ────────────────────────────────────────────────────────────────

type stmt interface{ isStmt() }

// assignStmt is `target = value` where target is a scalar or a mapping index.
type assignStmt struct {
	target *indexExpr // mapping[key], or a bare identifier wrapped with key==nil
	value  expr
}

type requireStmt struct{ cond expr }

type returnStmt struct{ value expr }

type emitStmt struct {
	event string
	args  []expr
}

type ifStmt struct {
	cond expr
	body []stmt
}

func (*assignStmt) isStmt()  {}
func (*requireStmt) isStmt() {}
func (*returnStmt) isStmt()  {}
func (*emitStmt) isStmt()    {}
func (*ifStmt) isStmt()      {}

// ── expressions ───────────────────────────────────────────────────────────────

type expr interface{ isExpr() }

type numberExpr struct{ value *big.Int }

// identExpr is a bare name: a parameter, or a scalar state variable.
type identExpr struct{ name string }

// indexExpr is `name[key]`; when key is nil it is a bare `name` (used as an
// assignment target so scalars and mappings share one shape).
type indexExpr struct {
	name string
	key  expr // nil for a scalar
}

// msgExpr is `msg.sender` or `msg.value`.
type msgExpr struct{ field string }

type binaryExpr struct {
	opText string
	left   expr
	right  expr
}

func (*numberExpr) isExpr() {}
func (*identExpr) isExpr()  {}
func (*indexExpr) isExpr()  {}
func (*msgExpr) isExpr()    {}
func (*binaryExpr) isExpr() {}
