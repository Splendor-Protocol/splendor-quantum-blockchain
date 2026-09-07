package compiler

import (
	"fmt"
	"math/big"
	"strings"
	"testing"

	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
)

// A uniform fleet exercises the common path. These probe the edges, where a
// compiler either reports a clear limit or corrupts its output silently.

// bigContract builds a contract with n trivial functions.
func bigContract(n int) string {
	var b strings.Builder
	b.WriteString("contract Big {\n    uint256 v;\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "    function f%d(uint256 a) public view returns (uint256) { return a + %d; }\n", i, i)
	}
	b.WriteString("}\n")
	return b.String()
}

// Jump targets are fixed-width PUSH2, so a contract above 64 KiB cannot address
// its own labels. That limit must be REPORTED, not silently truncated into
// bytecode whose jumps land on the wrong instruction.
func TestOversizeContractIsRefusedNotTruncated(t *testing.T) {
	// Find the smallest function count that trips the ceiling, so the test
	// still means something if codegen's size per function changes.
	var lastGood int
	var firstBad int
	for n := 256; n <= 16384; n *= 2 {
		c, err := Compile(bigContract(n))
		if err != nil {
			firstBad = n
			break
		}
		if len(c.Bytecode) > 0xFFFF {
			t.Fatalf("%d functions compiled to %d bytes, above the 64 KiB label ceiling, without an error",
				n, len(c.Bytecode))
		}
		lastGood = n
	}
	if firstBad == 0 {
		t.Skipf("no size in range tripped the ceiling (largest good: %d functions)", lastGood)
	}
	_, err := Compile(bigContract(firstBad))
	if err == nil {
		t.Fatalf("%d functions compiled without error", firstBad)
	}
	if !strings.Contains(err.Error(), "64 KiB") && !strings.Contains(err.Error(), "ceiling") {
		t.Fatalf("oversize contract failed with an unhelpful error: %v", err)
	}
	t.Logf("largest compiling contract: %d functions; refused at %d", lastGood, firstBad)
}

// A large-but-legal contract must dispatch correctly all the way to its LAST
// function: the dispatcher is a linear chain of comparisons, and an off-by-one
// in label resolution would show up at the far end.
func TestLargeContractDispatchesToItsLastFunction(t *testing.T) {
	const n = 400
	d := deploy(t, bigContract(n))
	for _, i := range []int{0, 1, n / 2, n - 2, n - 1} {
		fn := fmt.Sprintf("f%d", i)
		wantValue(t, d, fn, int64(7+i), big.NewInt(7))
	}
}

// Deep nesting must not blow the parser's or codegen's stack. A contract this
// shape is pathological, but "the node's compiler panics" is not an acceptable
// answer to pathological input.
func TestDeeplyNestedBlocksDoNotPanic(t *testing.T) {
	const depth = 200
	var b strings.Builder
	b.WriteString("contract Deep {\n    uint256 v;\n    function f(uint256 a) public {\n")
	for i := 0; i < depth; i++ {
		fmt.Fprintf(&b, "%sif (a > %d) {\n", strings.Repeat("  ", i+2), i)
	}
	fmt.Fprintf(&b, "%sv = a;\n", strings.Repeat("  ", depth+2))
	for i := depth - 1; i >= 0; i-- {
		fmt.Fprintf(&b, "%s}\n", strings.Repeat("  ", i+2))
	}
	b.WriteString("    }\n}\n")

	// Either it compiles or it reports a limit — it must not panic.
	if _, err := Compile(b.String()); err != nil {
		t.Logf("deep nesting refused with: %v", err)
	}
}

// The same for expression nesting, which recurses through parsePrimary.
func TestDeeplyNestedExpressionDoesNotPanic(t *testing.T) {
	const depth = 500
	expr := strings.Repeat("(", depth) + "1" + strings.Repeat(")", depth)
	src := fmt.Sprintf("contract E {\n    function f() public view returns (uint256) { return %s; }\n}\n", expr)
	if _, err := Compile(src); err != nil {
		t.Logf("deep expression refused with: %v", err)
	}
}

// Many locals push the emit region far up memory. Memory is charged per word,
// so this is about correctness of the offsets, not cost: the last local and a
// following emit must still round-trip.
func TestManyLocalsKeepDistinctSlots(t *testing.T) {
	const n = 120
	var b strings.Builder
	b.WriteString("contract Many {\n    uint256 out;\n    function f() public {\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "        uint256 x%d = %d;\n", i, i)
	}
	// Sum the first and last to prove neither was clobbered by the others.
	fmt.Fprintf(&b, "        out = x0 + x%d;\n", n-1)
	fmt.Fprintf(&b, "        emit Done(x%d);\n", n-1)
	b.WriteString("    }\n    function get() public view returns (uint256) { return out; }\n}\n")

	d := deploy(t, b.String())
	res := d.call(acc(1), 0, "f")
	if res.Reverted {
		t.Fatalf("f reverted: %v", res.Err)
	}
	if len(res.Logs) != 1 {
		t.Fatalf("got %d logs, want 1", len(res.Logs))
	}
	if got := new(big.Int).SetBytes(res.Logs[0].Data); got.Cmp(big.NewInt(n-1)) != 0 {
		t.Fatalf("logged last local = %s, want %d (emit region overlaps the locals)", got, n-1)
	}
	wantValue(t, d, "get", int64(0+(n-1)))
}

// A loop whose body writes storage every iteration is the shape most likely to
// exhaust gas in production. It must end as a clean revert, with the storage
// writes rolled back or kept per the engine's rules — never as a hang.
func TestGasExhaustionInStorageLoopIsCleanRevert(t *testing.T) {
	d := deploy(t, `
contract Burn {
    mapping(uint256 => uint256) m;
    function fill(uint256 n) public {
        for (uint256 i = 0; i < n; i += 1) { m[i] = i + 1; }
    }
}`)
	res := d.call(acc(1), 0, "fill", new(big.Int).Lsh(big.NewInt(1), 40))
	if res == nil {
		t.Fatal("no result from an over-long loop")
	}
	if res.Err == nil {
		t.Fatal("an over-long storage loop completed; the gas ceiling did not bind")
	}
	if res.GasUsed > vm.MaxGasPerCall {
		t.Fatalf("gas used %d exceeds the per-call ceiling %d", res.GasUsed, vm.MaxGasPerCall)
	}
}

// Selector collisions inside one contract would leave a function permanently
// unreachable behind the dispatcher. Real collisions need 2^32 work to find, so
// the CHECK is tested directly rather than by searching for one.
func TestSelectorCollisionIsRejected(t *testing.T) {
	shared := [4]byte{0xAA, 0xBB, 0xCC, 0xDD}
	err := checkSelectorCollisions([]ABIFunction{
		{Name: "alpha", Signature: "alpha()", Selector: shared},
		{Name: "beta", Signature: "beta(uint256)", Selector: shared},
	})
	if err == nil {
		t.Fatal("two functions sharing a selector were accepted")
	}
	if !strings.Contains(err.Error(), "alpha()") || !strings.Contains(err.Error(), "beta(uint256)") {
		t.Fatalf("error does not name both colliding functions: %v", err)
	}
	if err := checkSelectorCollisions([]ABIFunction{
		{Name: "a", Signature: "a()", Selector: [4]byte{1, 2, 3, 4}},
		{Name: "b", Signature: "b()", Selector: [4]byte{4, 3, 2, 1}},
	}); err != nil {
		t.Fatalf("distinct selectors were rejected: %v", err)
	}
}
