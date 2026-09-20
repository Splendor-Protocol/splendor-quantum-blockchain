package compiler

import (
	"context"
	"strings"
	"testing"
	"time"
)

// checkCallGraph must catch every shape of cycle, not just the two in the
// commit's own tests. addExpr (compiler/compiler.go:196) descends into
// callExpr/binaryExpr/unaryExpr only — indexExpr's key is not walked, so a
// recursive call hidden in a mapping subscript is invisible to the checker.
func TestQACycleShapesAreAllRejected(t *testing.T) {
	cases := map[string]string{
		"direct":     `contract R { uint256 v; function f(uint256 a) internal { f(a); } function g() public { f(1); } }`,
		"mutual":     `contract R { uint256 v; function a(uint256 x) internal { b(x); } function b(uint256 x) internal { a(x); } function g() public { a(1); } }`,
		"three_hop":  `contract R { uint256 v; function a(uint256 x) internal { b(x); } function b(uint256 x) internal { c(x); } function c(uint256 x) internal { a(x); } function g() public { a(1); } }`,
		"in_if":      `contract R { uint256 v; function a(uint256 x) internal { if (x > 1) { a(x - 1); } } function g() public { a(9); } }`,
		"in_for":     `contract R { uint256 v; function a(uint256 x) internal { for (uint256 i = 0; i < 2; i += 1) { a(x); } } function g() public { a(9); } }`,
		"in_while":   `contract R { uint256 v; function a(uint256 x) internal { while (x > 0) { a(x); } } function g() public { a(9); } }`,
		"in_require": `contract R { uint256 v; function a(uint256 x) internal returns (uint256) { require(a(x) > 0, "r"); return 1; } function g() public { v = a(9); } }`,
		"in_emit":    `contract R { uint256 v; function a(uint256 x) internal returns (uint256) { emit E(a(x)); return 1; } function g() public { v = a(9); } }`,
		"in_vardecl": `contract R { uint256 v; function a(uint256 x) internal returns (uint256) { uint256 y = a(x); return y; } function g() public { v = a(9); } }`,
		"in_forpost": `contract R { uint256 v; function a(uint256 x) internal returns (uint256) { for (uint256 i = 0; i < 2; i += a(x)) { v = 1; } return 1; } function g() public { v = a(9); } }`,
		"in_mapkey_read": `contract R { mapping(uint256 => uint256) m; uint256 v;
			function a(uint256 x) internal returns (uint256) { return m[a(x)] + 1; }
			function g() public { v = a(9); } }`,
		"in_mapkey_assign": `contract R { mapping(uint256 => uint256) m; uint256 v;
			function a(uint256 x) internal returns (uint256) { m[a(x)] = 1; return 1; }
			function g() public { v = a(9); } }`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			done := make(chan error, 1)
			go func() { _, err := CompileContext(context.Background(), src); done <- err }()
			select {
			case err := <-done:
				if err == nil {
					t.Fatalf("FINDING: recursive source compiled with no error")
				}
				if strings.Contains(err.Error(), "recursive internal-call graph") {
					return // caught by the checker, as designed
				}
				t.Logf("NOT caught by checkCallGraph; stopped later by: %v", err)
			case <-time.After(20 * time.Second):
				t.Fatalf("FINDING: recursive source ran unbounded for 20s")
			}
		})
	}
}

// A cycle that routes through a PUBLIC function must not become an inlining
// cycle: genInlineCall only resolves internal functions, so this should be a
// plain "undefined internal function" error, never unbounded expansion.
func TestQACycleThroughPublicFunction(t *testing.T) {
	src := `contract R { uint256 v;
		function pub(uint256 x) public returns (uint256) { return inner(x); }
		function inner(uint256 x) internal returns (uint256) { return pub(x); }
		function g() public { v = inner(1); } }`
	done := make(chan error, 1)
	go func() { _, err := CompileContext(context.Background(), src); done <- err }()
	select {
	case err := <-done:
		t.Logf("public-hop cycle: %v", err)
		if err == nil {
			t.Fatal("FINDING: a cycle through a public function compiled")
		}
	case <-time.After(20 * time.Second):
		t.Fatal("FINDING: public-hop cycle ran unbounded")
	}
}

// Cancellation must be honoured no matter which phase is running. The commit
// polls ctx inside genStmt/genExpr/genInlineCall but NOT inside the lexer,
// parser or checker loops — those remain bounded only by the 128 KiB source cap.
func TestQAAlreadyCancelledContextIsRefused(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := CompileContext(ctx, "contract C { uint256 v; function f() public { v = 1; } }"); err == nil {
		t.Fatal("FINDING: a cancelled context still compiled")
	}
}

// The largest source the RPC accepts must still be handled inside a request
// deadline in every phase (lexer/parser/checker), since none of them polls ctx.
func TestQAMaxSourceThroughEveryPhase(t *testing.T) {
	var b strings.Builder
	b.WriteString("contract Big { uint256 v;\n")
	i := 0
	for b.Len() < 120*1024 {
		b.WriteString("function f")
		b.WriteString(pad(i))
		b.WriteString("(uint256 x) internal returns (uint256) { return x + 1; }\n")
		i++
	}
	b.WriteString("function g(uint256 x) public { v = x; }\n}\n")
	src := b.String()
	start := time.Now()
	_, err := CompileContext(context.Background(), src)
	el := time.Since(start)
	t.Logf("%d functions, %d bytes, elapsed %s, err=%v", i, len(src), el, err)
	if el > 10*time.Second {
		t.Errorf("FINDING: lexer/parser/checker took %s on a legal max-size source with no cancellation polling", el)
	}
}

func pad(i int) string {
	s := ""
	for n := i; ; n /= 10 {
		s = string(rune('0'+n%10)) + s
		if n < 10 {
			break
		}
	}
	return strings.Repeat("0", 7-len(s)) + s
}
