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

func parseType(t token) (varType, bool) {
	switch t.text {
	case "uint256":
		return typeUint, true
	case "address":
		return typeAddress, true
	case "bool":
		return typeBool, true
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
	vt, ok := parseType(p.next())
	if !ok {
		return nil, fmt.Errorf("line %d: expected a type or 'function'", p.toks[p.pos-1].line)
	}
	nameTok := p.next()
	if nameTok.kind != tIdent {
		return nil, fmt.Errorf("line %d: expected variable name", nameTok.line)
	}
	if err := p.expectPunct(";"); err != nil {
		return nil, err
	}
	sv := &stateVar{name: nameTok.text, slot: *nextSlot, valType: vt}
	*nextSlot++
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
	// Optional `view` and `returns (T)`.
	if p.isKeyword("view") {
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
		if err := p.expectPunct(")"); err != nil {
			return nil, err
		}
		if err := p.expectPunct(";"); err != nil {
			return nil, err
		}
		return &requireStmt{cond: cond}, nil
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
		return &ifStmt{cond: cond, body: body}, nil
	default:
		// assignment: target ('[' key ']')? '=' expr ';'
		nameTok := p.next()
		if nameTok.kind != tIdent {
			return nil, fmt.Errorf("line %d: expected a statement", nameTok.line)
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
		if !p.isOp("=") {
			return nil, fmt.Errorf("line %d: expected '=' in assignment", p.cur().line)
		}
		p.next()
		val, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if err := p.expectPunct(";"); err != nil {
			return nil, err
		}
		return &assignStmt{target: target, value: val}, nil
	}
}

// Expression grammar (lowest to highest precedence):
//   comparison := additive ( (==|!=|<|<=|>|>=) additive )?
//   additive   := primary ( (+|-|*) primary )*
//   primary    := number | msg.field | ident ('[' expr ']')? | '(' expr ')'

func (p *parser) parseExpr() (expr, error) { return p.parseComparison() }

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
	left, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	for {
		t := p.cur()
		if t.kind == tOp && (t.text == "+" || t.text == "-" || t.text == "*") {
			p.next()
			right, err := p.parsePrimary()
			if err != nil {
				return nil, err
			}
			left = &binaryExpr{opText: t.text, left: left, right: right}
			continue
		}
		return left, nil
	}
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
	case t.kind == tIdent:
		p.next()
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
