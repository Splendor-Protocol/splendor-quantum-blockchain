package compiler

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// amplifiedSource pads the contract with many same-arity internal decoys whose
// names sort before the real targets, so both checkCallGraph.findCallee and
// genInlineCall's linear scan over c.funcs pay O(#funcs) per call site, then
// drives an exponential inline expansion until the emitted-item budget bites.
func amplifiedSource(decoys, depth, fan int) string {
	var b strings.Builder
	b.WriteString("contract Amp {\nuint256 v;\n")
	for i := 0; i < decoys; i++ {
		fmt.Fprintf(&b, "function dcy%07d(uint256 x) internal returns (uint256) { return x; }\n", i)
	}
	fmt.Fprintf(&b, "function zzz%07d(uint256 x) internal returns (uint256) { return x + 1; }\n", depth)
	for i := depth - 1; i >= 0; i-- {
		fmt.Fprintf(&b, "function zzz%07d(uint256 x) internal returns (uint256) { return ", i)
		for j := 0; j < fan; j++ {
			if j > 0 {
				b.WriteString("+")
			}
			fmt.Fprintf(&b, "zzz%07d(x)", i+1)
		}
		b.WriteString("; }\n")
	}
	fmt.Fprintf(&b, "function go(uint256 x) public { v = zzz%07d(x); }\n}\n", 0)
	return b.String()
}

// TestQAWorstCaseCompileWallClock: the budget is an ITEM budget, not a time
// budget. Per-item work is O(#functions) in genInlineCall's linear target
// scan, so a 128 KiB source can multiply the budget by the function count.
// Measures the real wall clock and peak heap a single anonymous /contract/verify
// request can cost.
func TestQAWorstCaseCompileWallClock(t *testing.T) {
	for _, tc := range []struct{ decoys, depth, fan int }{
		{0, 40, 2},
		{800, 40, 2},
		{1600, 40, 2},
	} {
		name := fmt.Sprintf("decoys%d", tc.decoys)
		t.Run(name, func(t *testing.T) {
			src := amplifiedSource(tc.decoys, tc.depth, tc.fan)
			if len(src) > 128*1024 {
				t.Skipf("source %d bytes over the 128 KiB RPC cap", len(src))
			}
			var m0, m1 runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&m0)
			start := time.Now()
			_, err := CompileContext(context.Background(), src)
			el := time.Since(start)
			runtime.ReadMemStats(&m1)
			t.Logf("src=%d bytes  elapsed=%s  totalAlloc=%d MiB  err=%v",
				len(src), el, (m1.TotalAlloc-m0.TotalAlloc)/(1<<20), err)
			if el > 10*time.Second {
				t.Errorf("FINDING: one request burned %s of CPU", el)
			}
		})
	}
}

// TestQAPeakHeapForOneCompile samples the live heap while one budget-exhausting
// compile runs: the emitted-item budget is 1<<20 items, so a few-KiB request
// retains an item slice orders of magnitude larger than the 64 KiB bytecode
// ceiling. Recorded so the memory cost of the chosen budget is visible.
func TestQAPeakHeapForOneCompile(t *testing.T) {
	src := amplifiedSource(0, 40, 2)
	stop := make(chan struct{})
	var peak atomic.Uint64
	go func() {
		var m runtime.MemStats
		for {
			select {
			case <-stop:
				return
			default:
			}
			runtime.ReadMemStats(&m)
			if m.HeapInuse > peak.Load() {
				peak.Store(m.HeapInuse)
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()
	_, err := CompileContext(context.Background(), src)
	close(stop)
	time.Sleep(20 * time.Millisecond)
	t.Logf("peak HeapInuse during one %d-byte compile = %d MiB (err=%v)", len(src), peak.Load()/(1<<20), err != nil)
}
