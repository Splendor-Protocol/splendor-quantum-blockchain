package compiler

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// expandingSource builds an ACYCLIC call graph that expands as fan^depth:
// f0 calls f1 `fan` times, f1 calls f2 `fan` times, and so on. There is no
// cycle, so checkCallGraph cannot reject it; the only thing that can stop it
// is a budget enforced WHILE generating.
func expandingSource(depth, fan int) string {
	var b strings.Builder
	b.WriteString("contract Boom {\n uint256 v;\n")
	// leaf
	fmt.Fprintf(&b, " function f%d(uint256 x) internal returns (uint256) { return x + 1; }\n", depth)
	for i := depth - 1; i >= 0; i-- {
		fmt.Fprintf(&b, " function f%d(uint256 x) internal returns (uint256) { return ", i)
		for j := 0; j < fan; j++ {
			if j > 0 {
				b.WriteString(" + ")
			}
			fmt.Fprintf(&b, "f%d(x)", i+1)
		}
		b.WriteString("; }\n")
	}
	b.WriteString(" function go(uint256 x) public { v = f0(x); }\n}\n")
	return b.String()
}

// TestQAExponentialExpansionIsBounded is the adversarial C6 case the review
// calls out explicitly: an acyclic but exponentially expanding call graph.
// It must return a controlled error (or compile) well inside a request
// deadline, never run unbounded.
func TestQAExponentialExpansionIsBounded(t *testing.T) {
	for _, tc := range []struct{ depth, fan int }{{24, 2}, {40, 2}, {16, 3}, {64, 2}} {
		t.Run(fmt.Sprintf("d%d_f%d", tc.depth, tc.fan), func(t *testing.T) {
			src := expandingSource(tc.depth, tc.fan)
			t.Logf("source bytes = %d", len(src))
			if len(src) > 128*1024 {
				t.Fatalf("source %d bytes exceeds the RPC cap; not a valid attack", len(src))
			}
			done := make(chan error, 1)
			start := time.Now()
			go func() {
				_, err := CompileContext(context.Background(), src)
				done <- err
			}()
			select {
			case err := <-done:
				t.Logf("finished in %s, err = %v", time.Since(start), err)
			case <-time.After(30 * time.Second):
				t.Fatalf("FINDING: compile of a %d-byte acyclic expanding source did not finish in 30s (unbounded codegen work)", len(src))
			}
		})
	}
}

// TestQACancellationDuringExpansion: the request deadline must actually stop
// the work, not merely be checked between phases.
func TestQACancellationDuringExpansion(t *testing.T) {
	src := expandingSource(40, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	start := time.Now()
	go func() {
		_, err := CompileContext(ctx, src)
		done <- err
	}()
	select {
	case err := <-done:
		el := time.Since(start)
		t.Logf("returned after %s: %v", el, err)
		if el > 5*time.Second {
			t.Errorf("FINDING: cancellation took %s to take effect", el)
		}
	case <-time.After(20 * time.Second):
		t.Fatalf("FINDING: cancelled compile still running after 20s")
	}
}

// TestQAMemoryBlowupFromEmittedItems measures how large the generated program
// gets before the budget bites — the review asked for byte limits WHILE
// generating, not only a post-hoc assembler size check.
func TestQAEmittedBytesBeforeBudgetBites(t *testing.T) {
	src := expandingSource(40, 2)
	start := time.Now()
	c, err := CompileContext(context.Background(), src)
	t.Logf("elapsed %s err=%v", time.Since(start), err)
	if err == nil {
		t.Logf("compiled, bytecode = %d bytes", len(c.Bytecode))
	}
}
