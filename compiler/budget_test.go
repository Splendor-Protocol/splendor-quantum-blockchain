package compiler

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ── (a) the cycle check must walk every expression form ──────────────────────

// checkCallGraph's expression walk descends into call, binary and unary
// expressions. It never walks an index expression's KEY, so a call sitting in
// `m[f(x)]` is invisible to the cycle test: the recursion is then found only by
// the inline-depth ceiling, after codegen has already expanded 64 copies of the
// body, and it is reported as an inlining-depth failure rather than as the
// recursion it is.
func TestRecursionHiddenInAnIndexKeyIsRejectedAsRecursion(t *testing.T) {
	cases := map[string]string{
		"direct through an index key": `contract R { mapping(uint256 => uint256) m;
			function f(uint256 a) internal returns (uint256) { return m[f(a)] + 1; }
			function g(uint256 a) public returns (uint256) { return f(a); } }`,
		"mutual through an index key": `contract R { mapping(uint256 => uint256) m;
			function a(uint256 x) internal returns (uint256) { return m[b(x)]; }
			function b(uint256 x) internal returns (uint256) { return m[a(x)]; }
			function g(uint256 x) public returns (uint256) { return a(x); } }`,
		"index key in an assignment target": `contract R { mapping(uint256 => uint256) m;
			function f(uint256 a) internal returns (uint256) { m[f(a)] = 1; return a; }
			function g(uint256 a) public returns (uint256) { return f(a); } }`,
		"index key nested under a binary operand": `contract R { mapping(uint256 => uint256) m;
			function f(uint256 a) internal returns (uint256) { return 1 + m[2 * f(a)]; }
			function g(uint256 a) public returns (uint256) { return f(a); } }`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Compile(src)
			if err == nil {
				t.Fatal("recursion hidden in an index key compiled without error")
			}
			if !strings.Contains(err.Error(), "recursive") {
				t.Fatalf("not rejected as recursion, got: %v", err)
			}
		})
	}
}

// The same walk must not start rejecting legal code: an acyclic call in an
// index key is ordinary QSol and has to keep compiling.
func TestAcyclicCallInAnIndexKeyStillCompiles(t *testing.T) {
	src := `contract A { mapping(uint256 => uint256) m; uint256 v;
		function leaf(uint256 x) internal returns (uint256) { return x + 1; }
		function top(uint256 x) public { v = m[leaf(x)]; m[leaf(x) + 1] = 2; }
		function get() public view returns (uint256) { return v; } }`
	if _, err := Compile(src); err != nil {
		t.Fatalf("acyclic call in an index key rejected: %v", err)
	}
}

// ── (b) the generation budget must bound bytes, not just walk steps ──────────

// expandingSrc builds an ACYCLIC call graph that doubles at every level, so a
// source of a couple of kilobytes asks codegen for 2^depth copies of the leaf.
// The cycle check passes — correctly, there is no cycle — so only the
// generation budget stands between this and unbounded output.
func expandingSrc(depth int) string {
	var b strings.Builder
	b.WriteString("contract X { uint256 v;\n")
	b.WriteString("function f0(uint256 x) internal returns (uint256) { return x + 1; }\n")
	for i := 1; i <= depth; i++ {
		fmt.Fprintf(&b, "function f%d(uint256 x) internal returns (uint256) { return f%d(x) + f%d(x); }\n", i, i-1, i-1)
	}
	fmt.Fprintf(&b, "function top(uint256 x) public { v = f%d(x); }\n}\n", depth)
	return b.String()
}

// peakHeapDuring samples the live heap while fn runs and returns the peak above
// a collected baseline.
func peakHeapDuring(fn func()) uint64 {
	runtime.GC()
	var base runtime.MemStats
	runtime.ReadMemStats(&base)

	var peak, done atomic.Uint64
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		var ms runtime.MemStats
		for done.Load() == 0 {
			runtime.ReadMemStats(&ms)
			if ms.HeapAlloc > peak.Load() {
				peak.Store(ms.HeapAlloc)
			}
			time.Sleep(time.Millisecond)
		}
	}()
	fn()
	done.Store(1)
	<-stopped
	runtime.GC()
	if peak.Load() < base.HeapAlloc {
		return 0
	}
	return peak.Load() - base.HeapAlloc
}

// The budget counts walk steps, not output. One 2 KiB request therefore holds
// hundreds of megabytes of asm items before it stops — multiplied by the
// in-flight request cap, which is what makes it a denial of service rather
// than a slow compile.
func TestOneRequestCannotOccupyHundredsOfMegabytes(t *testing.T) {
	// The cap a single request may occupy. Generation is bounded in bytes and
	// in items, so the asm buffer, its growth slack and the constants hanging
	// off it all have to fit well inside this.
	const maxHeap = 128 << 20

	for _, depth := range []int{20, 24, 30} {
		src := expandingSrc(depth)
		var err error
		var elapsed time.Duration
		peak := peakHeapDuring(func() {
			start := time.Now()
			_, err = CompileContext(context.Background(), src)
			elapsed = time.Since(start)
		})
		t.Logf("depth=%d src=%dB elapsed=%v peakHeap=%.1fMiB", depth, len(src), elapsed, float64(peak)/(1<<20))
		if err == nil {
			t.Fatalf("depth=%d: an expanding call graph compiled without a limit", depth)
		}
		if peak > maxHeap {
			t.Fatalf("depth=%d: one request peaked at %.1fMiB, above the %dMiB a single compile may hold",
				depth, float64(peak)/(1<<20), maxHeap>>20)
		}
	}
}

// The byte budget is the one that should stop an expanding graph, and it must
// say so: "too big" and "too many steps" send an author to different fixes.
func TestExpandingGraphIsRefusedByTheSizeBudget(t *testing.T) {
	_, err := Compile(expandingSrc(24))
	if err == nil {
		t.Fatal("expanding call graph compiled without error")
	}
	if !strings.Contains(err.Error(), "ceiling") && !strings.Contains(err.Error(), "budget") {
		t.Fatalf("refusal does not name a limit: %v", err)
	}
	if !strings.Contains(err.Error(), "byte") {
		t.Fatalf("expanding output was not refused on size, got: %v", err)
	}
}

// The budgets codegen is held to, restated here so the headroom claim is
// checked against a number this test owns rather than against whatever the
// compiler currently defines.
const (
	budgetBytes = 512 << 10 // 8x the 64 KiB label ceiling
	budgetItems = 1 << 18   // 262144 asm items
)

// Headroom, stated as a test: every contract the repository ships, and the
// largest contract that can assemble at all (the 64 KiB label ceiling makes
// anything above ~1500 trivial functions undeployable), must sit far inside
// both budgets. If a future codegen change makes real contracts approach
// these numbers, this fails before the budget starts rejecting real work.
func TestRealContractsSitFarInsideTheGenerationBudget(t *testing.T) {
	if MaxGeneratedBytes != budgetBytes || MaxAsmItems != budgetItems {
		t.Fatalf("budgets moved to %d bytes / %d items: re-measure the corpus before changing them",
			MaxGeneratedBytes, MaxAsmItems)
	}
	measure := func(t *testing.T, name, src string) (bytes, items int) {
		t.Helper()
		toks, err := newLexer(src).tokenize()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		node, err := newParser(toks).parseContract()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if err := check(node); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		g := newCodegen(node, context.Background())
		code, err := g.generate()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return len(code), len(g.a.items)
	}

	corpus := map[string]string{}
	for _, f := range []string{"counter", "loops", "mathdemo", "token"} {
		b, err := os.ReadFile("examples/" + f + ".qsol")
		if err != nil {
			t.Fatalf("read example %s: %v", f, err)
		}
		corpus["examples/"+f] = string(b)
	}
	// The largest contract that still fits under the 64 KiB label ceiling.
	corpus["bigContract(1500)"] = bigContract(1500)

	for name, src := range corpus {
		bytes, items := measure(t, name, src)
		t.Logf("%-20s bytes=%-7d (%.0f%% of budget)  items=%-7d (%.0f%% of budget)",
			name, bytes, 100*float64(bytes)/float64(budgetBytes),
			items, 100*float64(items)/float64(budgetItems))
		if bytes*5 > budgetBytes {
			t.Fatalf("%s emits %d bytes, above a fifth of the %d-byte budget: too little headroom",
				name, bytes, budgetBytes)
		}
		if items*5 > budgetItems {
			t.Fatalf("%s emits %d items, above a fifth of the %d-item budget: too little headroom",
				name, items, budgetItems)
		}
	}
}
