package compiler

import (
	"fmt"
	"math/big"
	"strings"
)

type tokKind uint8

const (
	tEOF tokKind = iota
	tIdent
	tNumber
	tString // "..." literal (text holds the decoded bytes)
	tPunct  // one of ( ) { } [ ] ; , .
	tOp     // + - * / % ! == != < <= > >= = && || += -= *= /= %=
	tKeyword
)

type token struct {
	kind tokKind
	text string
	num  *big.Int
	line int
}

var keywords = map[string]bool{
	"contract": true, "function": true, "returns": true, "return": true,
	"require": true, "emit": true, "mapping": true, "if": true, "else": true,
	"for": true, "while": true,
	"uint256": true, "address": true, "bool": true, "view": true,
	"msg": true, "true": true, "false": true,
	// Visibility / mutability modifiers: parsed and ignored — they gate
	// nothing in the subset, but real-world sources are full of them.
	"public": true, "private": true, "internal": true, "external": true,
	"pure": true, "payable": true,
}

type lexer struct {
	src  string
	pos  int
	line int
}

func newLexer(src string) *lexer { return &lexer{src: src, line: 1} }

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
func isIdentPart(c byte) bool { return isIdentStart(c) || (c >= '0' && c <= '9') }
func isDigit(c byte) bool     { return c >= '0' && c <= '9' }

func (l *lexer) tokenize() ([]token, error) {
	var out []token
	for {
		l.skipTrivia()
		if l.pos >= len(l.src) {
			out = append(out, token{kind: tEOF, line: l.line})
			return out, nil
		}
		c := l.src[l.pos]
		switch {
		case isIdentStart(c):
			start := l.pos
			for l.pos < len(l.src) && isIdentPart(l.src[l.pos]) {
				l.pos++
			}
			text := l.src[start:l.pos]
			k := tIdent
			if keywords[text] {
				k = tKeyword
			}
			out = append(out, token{kind: k, text: text, line: l.line})
		case isDigit(c):
			start := l.pos
			for l.pos < len(l.src) && isIdentPart(l.src[l.pos]) {
				l.pos++
			}
			raw := l.src[start:l.pos]
			n, ok := new(big.Int).SetString(raw, 0) // 0 → accept 0x…, decimal
			if !ok {
				return nil, fmt.Errorf("line %d: invalid number %q", l.line, raw)
			}
			out = append(out, token{kind: tNumber, text: raw, num: n, line: l.line})
		case strings.IndexByte("(){}[];,.", c) >= 0:
			out = append(out, token{kind: tPunct, text: string(c), line: l.line})
			l.pos++
		case c == '"':
			s, err := l.stringLit()
			if err != nil {
				return nil, err
			}
			out = append(out, token{kind: tString, text: s, line: l.line})
		default:
			// Multi-char operators first.
			two := ""
			if l.pos+1 < len(l.src) {
				two = l.src[l.pos : l.pos+2]
			}
			switch two {
			case "==", "!=", "<=", ">=", "&&", "||", "+=", "-=", "*=", "/=", "%=":
				out = append(out, token{kind: tOp, text: two, line: l.line})
				l.pos += 2
				continue
			}
			switch c {
			case '+', '-', '*', '/', '%', '!', '<', '>', '=':
				out = append(out, token{kind: tOp, text: string(c), line: l.line})
				l.pos++
			default:
				return nil, fmt.Errorf("line %d: unexpected character %q", l.line, string(c))
			}
		}
	}
}

// stringLit consumes a double-quoted literal (opening quote at l.pos) and
// returns its decoded bytes. Escapes: \" \\ \n \t. Newlines are not allowed
// inside a literal.
func (l *lexer) stringLit() (string, error) {
	l.pos++ // opening quote
	var b strings.Builder
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		switch c {
		case '"':
			l.pos++
			return b.String(), nil
		case '\n':
			return "", fmt.Errorf("line %d: newline in string literal", l.line)
		case '\\':
			if l.pos+1 >= len(l.src) {
				return "", fmt.Errorf("line %d: unterminated string literal", l.line)
			}
			l.pos++
			switch l.src[l.pos] {
			case '"':
				b.WriteByte('"')
			case '\\':
				b.WriteByte('\\')
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			default:
				return "", fmt.Errorf("line %d: unsupported escape \\%s", l.line, string(l.src[l.pos]))
			}
			l.pos++
		default:
			b.WriteByte(c)
			l.pos++
		}
	}
	return "", fmt.Errorf("line %d: unterminated string literal", l.line)
}

func (l *lexer) skipTrivia() {
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		switch {
		case c == '\n':
			l.line++
			l.pos++
		case c == ' ' || c == '\t' || c == '\r':
			l.pos++
		case c == '/' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '/':
			for l.pos < len(l.src) && l.src[l.pos] != '\n' {
				l.pos++
			}
		case c == '/' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '*':
			l.pos += 2
			for l.pos+1 < len(l.src) && !(l.src[l.pos] == '*' && l.src[l.pos+1] == '/') {
				if l.src[l.pos] == '\n' {
					l.line++
				}
				l.pos++
			}
			l.pos += 2
		default:
			return
		}
	}
}
