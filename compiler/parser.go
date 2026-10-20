package compiler

import "fmt"

type parser struct {
	toks []token
	pos  int
}

func newParser(toks []token) *parser { return &parser{toks: toks} }

func (p *parser) cur() token  { return p.toks[p.pos] }
func (p *parser) next() token { t := p.toks[p.pos]; p.pos++; return t }

func (p *parser) expectPunct(s string) error {
	t := p.cur()
	if t.kind != tPunct || t.text != s {
		return fmt.Errorf("line %d: expected %q, got %q", t.line, s, t.text)
	}
	p.pos++
	return nil
}

func (p *parser) expectKeyword(s string) error {
	t := p.cur()
	if t.kind != tKeyword || t.text != s {
		return fmt.Errorf("line %d: expected %q, got %q", t.line, s, t.text)
	}
	p.pos++
	return nil
}

func (p *parser) isPunct(s string) bool {
	t := p.cur()
	return t.kind == tPunct && t.text == s
}
func (p *parser) isKeyword(s string) bool {
	t := p.cur()
	return t.kind == tKeyword && t.text == s
}
func (p *parser) isOp(s string) bool {
	t := p.cur()
	return t.kind == tOp && t.text == s
}

// skipModifiers consumes any run of the listed modifier keywords. They are
// parsed and IGNORED: the subset dispatches every function externally and has
// no inheritance, so visibility/mutability gate nothing — but real-world
// sources are full of them.
func (p *parser) skipModifiers(names ...string) {
	for {
		t := p.cur()
		if t.kind != tKeyword {
			return
		}
		matched := false
		for _, n := range names {
			if t.text == n {
				matched = true
				break
			}
		}
		if !matched {
			return
		}
		p.next()
	}
}

var varModifiers = []string{"public", "private", "internal"}
var fnModifiers = []string{"public", "private", "internal", "external", "view", "pure", "payable"}

func parseType(t token) (varType, bool) {
	switch t.text {
	case "uint256":
		return typeUint, true
	case "address":
		return typeAddress, true
	case "bool":
		return typeBool, true
	case "string", "bytes32":
		return typeString, true
	}
	return 0, false
}

// parseContract parses the single top-level contract.
func (p *parser) parseContract() (*contractNode, error) {
	if err := p.expectKeyword("contract"); err != nil {
		return nil, err
	}
	nameTok := p.next()
	if nameTok.kind != tIdent {
		return nil, fmt.Errorf("line %d: expected contract name", nameTok.line)
	}
	c := &contractNode{name: nameTok.text}
	if err := p.expectPunct("{"); err != nil {
		return nil, err
	}
	var nextSlot int64
	for !p.isPunct("}") {
		if p.isKeyword("function") {
			fn, err := p.parseFunc()
			if err != nil {
				return nil, err
			}
			c.funcs = append(c.funcs, fn)
			continue
		}
		sv, err := p.parseStateVar(&nextSlot)
		if err != nil {
			return nil, err
		}
		c.states = append(c.states, sv)
	}
	if err := p.expectPunct("}"); err != nil {
		return nil, err
	}
	if p.cur().kind != tEOF {
		return nil, fmt.Errorf("line %d: trailing tokens after contract", p.cur().line)
	}
	return c, nil
}

// parseStateVar parses `mapping(K => V) name;` or `T name;`.
func (p *parser) parseStateVar(nextSlot *int64) (*stateVar, error) {
	if p.isKeyword("mapping") {
		p.next()
		if err := p.expectPunct("("); err != nil {
			return nil, err
		}
		kt, ok := parseType(p.next())
		if !ok {
			return nil, fmt.Errorf("mapping key must be a value type")
		}
		if !p.isOp("=") { // "=>" is lexed as "=" then ">"
			return nil, fmt.Errorf("line %d: expected => in mapping", p.cur().line)
		}
		p.next()
		if !p.isOp(">") {
			return nil, fmt.Errorf("line %d: expected => in mapping", p.cur().line)
		}
		p.next()
		vt, ok := parseType(p.next())
		if !ok {
			return nil, fmt.Errorf("mapping value must be a value type")
		}
		if err := p.expectPunct(")"); err != nil {
			return nil, err
		}
		p.skipModifiers(varModifiers...)
		nameTok := p.next()
		if nameTok.kind != tIdent {
			return nil, fmt.Errorf("line %d: expected mapping name", nameTok.line)
		}
		if err := p.expectPunct(";"); err != nil {
			return nil, err
		}
		sv := &stateVar{name: nameTok.text, slot: *nextSlot, isMapping: true, keyType: kt, valType: vt}
		*nextSlot++
		return sv, nil
	}
	typeTok := p.next()
	vt, ok := parseType(typeTok)
	if !ok {
		return nil, fmt.Errorf("line %d: expected a type or 'function'", typeTok.line)
	}

	// Fixed-size array: uint256[N] name — reserves N consecutive storage slots.
	isArr := false
	var arrSize int64
	if p.isPunct("[") {
		p.next()
		sizeTok := p.next()
		if sizeTok.kind != tNumber || sizeTok.num == nil || !sizeTok.num.IsInt64() {
			return nil, fmt.Errorf("line %d: expected integer array size", sizeTok.line)
		}
		arrSize = sizeTok.num.Int64()
		if arrSize <= 0 || arrSize > 1024 {
			return nil, fmt.Errorf("line %d: array size must be 1–1024 (got %d)", sizeTok.line, arrSize)
		}
		if err := p.expectPunct("]"); err != nil {
			return nil, err
		}
		isArr = true
	}

	p.skipModifiers(varModifiers...)
	nameTok := p.next()
	if nameTok.kind != tIdent {
		return nil, fmt.Errorf("line %d: expected variable name", nameTok.line)
	}
	if err := p.expectPunct(";"); err != nil {
		return nil, err
	}
	sv := &stateVar{
		name: nameTok.text, slot: *nextSlot, valType: vt,
		isArray: isArr, arraySize: arrSize,
	}
	if isArr {
		*nextSlot += arrSize // reserves N consecutive slots from sv.slot
	} else {
		*nextSlot++
	}
	return sv, nil
}

func (p *parser) parseFunc() (*funcNode, error) {
	if err := p.expectKeyword("function"); err != nil {
		return nil, err
	}
	nameTok := p.next()
	if nameTok.kind != tIdent {
		return nil, fmt.Errorf("line %d: expected function name", nameTok.line)
	}
	fn := &funcNode{name: nameTok.text}
	if err := p.expectPunct("("); err != nil {
		return nil, err
	}
	for !p.isPunct(")") {
		pt, ok := parseType(p.next())
		if !ok {
			return nil, fmt.Errorf("line %d: expected parameter type", p.toks[p.pos-1].line)
		}
		pn := p.next()
		if pn.kind != tIdent {
			return nil, fmt.Errorf("line %d: expected parameter name", pn.line)
		}
		fn.params = append(fn.params, param{name: pn.text, typ: pt})
		if p.isPunct(",") {
			p.next()
		}
	}
	if err := p.expectPunct(")"); err != nil {
		return nil, err
	}
	// Optional modifiers: consume and record visibility for isInternal.
	for {
		t := p.cur()
		if t.kind != tKeyword {
			break
		}
		matched := false
		for _, n := range fnModifiers {
			if t.text == n {
				matched = true
				break
			}
		}
		if !matched {
			break
		}
		if t.text == "internal" || t.text == "private" {
			fn.isInternal = true
		}
		p.next()
	}
	if p.isKeyword("returns") {
		p.next()
		if err := p.expectPunct("("); err != nil {
			return nil, err
		}
		rt, ok := parseType(p.next())
		if !ok {
			return nil, fmt.Errorf("line %d: expected return type", p.toks[p.pos-1].line)
		}
		fn.hasReturn = true
		fn.returnType = rt
		if err := p.expectPunct(")"); err != nil {
			return nil, err
		}
	}
	if err := p.expectPunct("{"); err != nil {
		return nil, err
	}
	for !p.isPunct("}") {
		s, err := p.parseStmt()
		if err != nil {
			return nil, err
		}
		fn.body = append(fn.body, s)
	}
	if err := p.expectPunct("}"); err != nil {
		return nil, err
	}
	return fn, nil
}

func (p *parser) parseStmt() (stmt, error) {
	switch {
	case p.isKeyword("require"):
		p.next()
		if err := p.expectPunct("("); err != nil {
			return nil, err
		}
		cond, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		reason := ""
		if p.isPunct(",") {
			p.next()
			rt := p.next()
			if rt.kind != tString {
				return nil, fmt.Errorf("line %d: require reason must be a string literal", rt.line)
			}
			if len(rt.text) > 32 {
				return nil, fmt.Errorf("line %d: require reason %q exceeds 32 bytes (the REQUIRE message word)", rt.line, rt.text)
			}
			reason = rt.text
		}
		if err := p.expectPunct(")"); err != nil {
			return nil, err
		}
		if err := p.expectPunct(";"); err != nil {
			return nil, err
		}
		return &requireStmt{cond: cond, reason: reason}, nil
	case p.isKeyword("return"):
		p.next()
		v, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if err := p.expectPunct(";"); err != nil {
			return nil, err
		}
		return &returnStmt{value: v}, nil
	case p.isKeyword("emit"):
		p.next()
		nameTok := p.next()
		if nameTok.kind != tIdent {
			return nil, fmt.Errorf("line %d: expected event name", nameTok.line)
		}
		if err := p.expectPunct("("); err != nil {
			return nil, err
		}
		var args []expr
		for !p.isPunct(")") {
			e, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			args = append(args, e)
			if p.isPunct(",") {
				p.next()
			}
		}
		if err := p.expectPunct(")"); err != nil {
			return nil, err
		}
		if err := p.expectPunct(";"); err != nil {
			return nil, err
		}
		return &emitStmt{event: nameTok.text, args: args}, nil
	case p.isKeyword("if"):
		return p.parseIf()
	case p.isKeyword("for"):
		return p.parseFor()
	case p.isKeyword("while"):
		p.next()
		if err := p.expectPunct("("); err != nil {
			return nil, err
		}
		cond, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if err := p.expectPunct(")"); err != nil {
			return nil, err
		}
		body, err := p.parseBlock()
		if err != nil {
			return nil, err
		}
		return &whileStmt{cond: cond, body: body}, nil
	case p.isKeyword("break"):
		p.next()
		if err := p.expectPunct(";"); err != nil {
			return nil, err
		}
		return &breakStmt{}, nil
	case p.isKeyword("continue"):
		p.next()
		if err := p.expectPunct(";"); err != nil {
			return nil, err
		}
		return &continueStmt{}, nil
	default:
		s, err := p.parseSimpleStmt()
		if err != nil {
			return nil, err
		}
		if err := p.expectPunct(";"); err != nil {
			return nil, err
		}
		return s, nil
	}
}

// parseSimpleStmt parses a declaration or an assignment WITHOUT consuming the
// trailing ';'. A `for` header needs exactly this: its clauses are separated by
// semicolons the loop parser owns.
//
//	declaration: T name = expr
//	assignment:  name ('[' key ']')? ('='|'+='|'-='|'*='|'/='|'%=') expr
func (p *parser) parseSimpleStmt() (stmt, error) {
	// A type keyword in statement position starts a local declaration.
	if t := p.cur(); t.kind == tKeyword {
		if vt, ok := parseType(t); ok {
			p.next()
			nameTok := p.next()
			if nameTok.kind != tIdent {
				return nil, fmt.Errorf("line %d: expected a name after %s", nameTok.line, t.text)
			}
			if !p.isOp("=") {
				return nil, fmt.Errorf("line %d: local %q must be initialised (%s %s = ...)",
					p.cur().line, nameTok.text, t.text, nameTok.text)
			}
			p.next()
			val, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			return &varDeclStmt{name: nameTok.text, typ: vt, value: val}, nil
		}
	}

	nameTok := p.next()
	if nameTok.kind != tIdent {
		return nil, fmt.Errorf("line %d: expected a statement", nameTok.line)
	}

	// Internal function call as a statement: name(args) — discards the return value.
	if p.isPunct("(") {
		p.next()
		var args []expr
		for !p.isPunct(")") {
			e, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			args = append(args, e)
			if p.isPunct(",") {
				p.next()
			}
		}
		if err := p.expectPunct(")"); err != nil {
			return nil, err
		}
		return &callStmt{name: nameTok.text, args: args}, nil
	}

	target := &indexExpr{name: nameTok.text}
	if p.isPunct("[") {
		p.next()
		k, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if err := p.expectPunct("]"); err != nil {
			return nil, err
		}
		target.key = k
	}
	opTok := p.cur()
	if opTok.kind != tOp {
		return nil, fmt.Errorf("line %d: expected '=' in assignment", opTok.line)
	}
	var binOp string
	switch opTok.text {
	case "=":
	case "+=", "-=", "*=", "/=", "%=":
		binOp = opTok.text[:1]
	default:
		return nil, fmt.Errorf("line %d: expected '=' in assignment", opTok.line)
	}
	p.next()
	val, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if binOp != "" {
		// Desugar `t op= e` to `t = t op e`. The subset's expressions are
		// side-effect-free, so re-evaluating a mapping key is safe.
		var cur expr
		if target.key == nil {
			cur = &identExpr{name: target.name}
		} else {
			cur = &indexExpr{name: target.name, key: target.key}
		}
		val = &binaryExpr{opText: binOp, left: cur, right: val}
	}
	return &assignStmt{target: target, value: val}, nil
}

// parseFor parses `for (init; cond; post) { body }`. Every clause is optional:
// `for (;;) { }` is a bare loop the gas ceiling ends.
func (p *parser) parseFor() (stmt, error) {
	p.next() // 'for'
	if err := p.expectPunct("("); err != nil {
		return nil, err
	}
	st := &forStmt{}
	if !p.isPunct(";") {
		init, err := p.parseSimpleStmt()
		if err != nil {
			return nil, err
		}
		st.init = init
	}
	if err := p.expectPunct(";"); err != nil {
		return nil, err
	}
	if !p.isPunct(";") {
		cond, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		st.cond = cond
	}
	if err := p.expectPunct(";"); err != nil {
		return nil, err
	}
	if !p.isPunct(")") {
		post, err := p.parseSimpleStmt()
		if err != nil {
			return nil, err
		}
		st.post = post
	}
	if err := p.expectPunct(")"); err != nil {
		return nil, err
	}
	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	st.body = body
	return st, nil
}

// parseIf parses `if (cond) { body }` with an optional `else { … }` or
// `else if …` chain (which nests as a single-statement elseBody).
func (p *parser) parseIf() (stmt, error) {
	p.next() // 'if'
	if err := p.expectPunct("("); err != nil {
		return nil, err
	}
	cond, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if err := p.expectPunct(")"); err != nil {
		return nil, err
	}
	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	st := &ifStmt{cond: cond, body: body}
	if p.isKeyword("else") {
		p.next()
		if p.isKeyword("if") {
			nested, err := p.parseIf()
			if err != nil {
				return nil, err
			}
			st.elseBody = []stmt{nested}
		} else {
			st.elseBody, err = p.parseBlock()
			if err != nil {
				return nil, err
			}
		}
	}
	return st, nil
}

// parseBlock parses `{ stmt* }`.
func (p *parser) parseBlock() ([]stmt, error) {
	if err := p.expectPunct("{"); err != nil {
		return nil, err
	}
	var body []stmt
	for !p.isPunct("}") {
		s, err := p.parseStmt()
		if err != nil {
			return nil, err
		}
		body = append(body, s)
	}
	if err := p.expectPunct("}"); err != nil {
		return nil, err
	}
	return body, nil
}

// Expression grammar (lowest to highest precedence, Solidity order):
//   or         := and ( '||' and )*
//   and        := comparison ( '&&' comparison )*
//   comparison := additive ( (==|!=|<|<=|>|>=) additive )?
//   additive   := multiplicative ( (+|-) multiplicative )*
//   mult       := unary ( (*|/|%) unary )*
//   unary      := '!' unary | primary
//   primary    := number | true | false | msg.field | ident ('[' expr ']')? | '(' expr ')'

func (p *parser) parseExpr() (expr, error) { return p.parseOr() }

func (p *parser) parseOr() (expr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.isOp("||") {
		p.next()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &binaryExpr{opText: "||", left: left, right: right}
	}
	return left, nil
}

func (p *parser) parseAnd() (expr, error) {
	left, err := p.parseComparison()
	if err != nil {
		return nil, err
	}
	for p.isOp("&&") {
		p.next()
		right, err := p.parseComparison()
		if err != nil {
			return nil, err
		}
		left = &binaryExpr{opText: "&&", left: left, right: right}
	}
	return left, nil
}

func (p *parser) parseComparison() (expr, error) {
	left, err := p.parseAdditive()
	if err != nil {
		return nil, err
	}
	if t := p.cur(); t.kind == tOp {
		switch t.text {
		case "==", "!=", "<", "<=", ">", ">=":
			p.next()
			right, err := p.parseAdditive()
			if err != nil {
				return nil, err
			}
			return &binaryExpr{opText: t.text, left: left, right: right}, nil
		}
	}
	return left, nil
}

func (p *parser) parseAdditive() (expr, error) {
	left, err := p.parseMultiplicative()
	if err != nil {
		return nil, err
	}
	for {
		t := p.cur()
		if t.kind == tOp && (t.text == "+" || t.text == "-") {
			p.next()
			right, err := p.parseMultiplicative()
			if err != nil {
				return nil, err
			}
			left = &binaryExpr{opText: t.text, left: left, right: right}
			continue
		}
		return left, nil
	}
}

func (p *parser) parseMultiplicative() (expr, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		t := p.cur()
		if t.kind == tOp && (t.text == "*" || t.text == "/" || t.text == "%") {
			p.next()
			right, err := p.parseUnary()
			if err != nil {
				return nil, err
			}
			left = &binaryExpr{opText: t.text, left: left, right: right}
			continue
		}
		return left, nil
	}
}

func (p *parser) parseUnary() (expr, error) {
	if p.isOp("!") {
		p.next()
		operand, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &unaryExpr{opText: "!", operand: operand}, nil
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() (expr, error) {
	t := p.cur()
	switch {
	case p.isPunct("("):
		p.next()
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if err := p.expectPunct(")"); err != nil {
			return nil, err
		}
		return e, nil
	case t.kind == tNumber:
		p.next()
		return &numberExpr{value: t.num}, nil
	case p.isKeyword("true"):
		p.next()
		return &numberExpr{value: bigOne}, nil
	case p.isKeyword("false"):
		p.next()
		return &numberExpr{value: bigZero}, nil
	case p.isKeyword("msg"):
		p.next()
		if err := p.expectPunct("."); err != nil {
			return nil, err
		}
		field := p.next()
		if field.kind != tIdent || (field.text != "sender" && field.text != "value") {
			return nil, fmt.Errorf("line %d: only msg.sender and msg.value are supported", field.line)
		}
		return &msgExpr{field: field.text}, nil
	case t.kind == tString:
		// String literal — encoded as bytes32 in codegen.
		p.next()
		return &stringExpr{value: t.text}, nil
	case t.kind == tIdent:
		p.next()
		if p.isPunct("(") {
			// Function call expression: name(arg, ...) — inlined by codegen.
			p.next()
			var args []expr
			for !p.isPunct(")") {
				e, err := p.parseExpr()
				if err != nil {
					return nil, err
				}
				args = append(args, e)
				if p.isPunct(",") {
					p.next()
				}
			}
			if err := p.expectPunct(")"); err != nil {
				return nil, err
			}
			return &callExpr{name: t.text, args: args}, nil
		}
		if p.isPunct("[") {
			p.next()
			key, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			if err := p.expectPunct("]"); err != nil {
				return nil, err
			}
			return &indexExpr{name: t.text, key: key}, nil
		}
		return &identExpr{name: t.text}, nil
	default:
		return nil, fmt.Errorf("line %d: unexpected token %q in expression", t.line, t.text)
	}
}
