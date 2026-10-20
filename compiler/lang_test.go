package compiler

import (
	"math/big"
	"strings"
	"testing"
)

// maxU256 is 2^256 - 1.
var maxU256 = new(big.Int).Sub(new(big.Int).Lsh(bigOne, 256), bigOne)

func wantValue(t *testing.T, d *deployed, fn string, want int64, args ...*big.Int) {
	t.Helper()
	res := d.call(acc(1), 0, fn, args...)
	if res.Reverted {
		t.Fatalf("%s(%v) reverted: %v", fn, args, res.Err)
	}
	got := new(big.Int).SetBytes(res.ReturnValue)
	if got.Cmp(big.NewInt(want)) != 0 {
		t.Fatalf("%s(%v) = %s, want %d", fn, args, got, want)
	}
}

func wantRevert(t *testing.T, d *deployed, fn string, msgPart string, args ...*big.Int) {
	t.Helper()
	res := d.call(acc(1), 0, fn, args...)
	if !res.Reverted {
		t.Fatalf("%s(%v) did not revert (returned %x)", fn, args, res.ReturnValue)
	}
	if msgPart != "" && (res.Err == nil || !strings.Contains(res.Err.Error(), msgPart)) {
		t.Fatalf("%s(%v) reverted with %v, want message containing %q", fn, args, res.Err, msgPart)
	}
}

// `*` binds tighter than `+` and `-` — Solidity precedence, not left-to-right.
func TestOperatorPrecedence(t *testing.T) {
	d := deploy(t, `
contract Prec {
    function calc() view returns (uint256) { return 2 + 3 * 4; }
    function calc2() view returns (uint256) { return 20 - 6 / 2; }
}`)
	wantValue(t, d, "calc", 14)
	wantValue(t, d, "calc2", 17)
}

// Division and modulo exist and match Solidity 0.8: /0 and %0 revert (the VM
// alone would yield 0, silently).
func TestDivisionAndModulo(t *testing.T) {
	d := deploy(t, `
contract Math {
    function div(uint256 a, uint256 b) view returns (uint256) { return a / b; }
    function mod(uint256 a, uint256 b) view returns (uint256) { return a % b; }
}`)
	wantValue(t, d, "div", 3, big.NewInt(7), big.NewInt(2))
	wantValue(t, d, "mod", 1, big.NewInt(7), big.NewInt(2))
	wantRevert(t, d, "div", "division by zero", big.NewInt(1), big.NewInt(0))
	wantRevert(t, d, "mod", "division by zero", big.NewInt(1), big.NewInt(0))
}

// + - * are checked like Solidity 0.8: overflow reverts instead of wrapping.
// This closes the semantic divergence the EVM differential test exposed.
func TestCheckedArithmetic(t *testing.T) {
	d := deploy(t, `
contract Math {
    function add(uint256 a, uint256 b) view returns (uint256) { return a + b; }
    function sub(uint256 a, uint256 b) view returns (uint256) { return a - b; }
    function mul(uint256 a, uint256 b) view returns (uint256) { return a * b; }
}`)
	wantValue(t, d, "add", 5, big.NewInt(2), big.NewInt(3))
	wantValue(t, d, "sub", 2, big.NewInt(5), big.NewInt(3))
	wantValue(t, d, "mul", 42, big.NewInt(6), big.NewInt(7))

	wantRevert(t, d, "add", "overflow", maxU256, bigOne)
	wantRevert(t, d, "sub", "underflow", bigZero, bigOne)
	half := new(big.Int).Lsh(bigOne, 128)
	wantRevert(t, d, "mul", "overflow", half, half)

	// Edge cases that must NOT revert.
	res := d.call(acc(1), 0, "add", maxU256, bigZero)
	if res.Reverted {
		t.Fatalf("max + 0 reverted: %v", res.Err)
	}
	res = d.call(acc(1), 0, "mul", bigZero, maxU256)
	if res.Reverted {
		t.Fatalf("0 * max reverted: %v", res.Err)
	}
}

// && and || short-circuit: with b == 0, `b != 0 && a / b > 1` must NOT evaluate
// the division (which would revert). ! is logical not.
func TestLogicalOperators(t *testing.T) {
	d := deploy(t, `
contract Logic {
    function safeDiv(uint256 a, uint256 b) view returns (uint256) {
        if (b != 0 && a / b > 1) { return 1; }
        return 0;
    }
    function either(uint256 a) view returns (uint256) {
        if (a == 1 || a == 2) { return 1; }
        return 0;
    }
    function invert(uint256 a) view returns (uint256) {
        if (!(a == 3)) { return 1; }
        return 0;
    }
}`)
	wantValue(t, d, "safeDiv", 0, big.NewInt(10), big.NewInt(0)) // short-circuit: no revert
	wantValue(t, d, "safeDiv", 1, big.NewInt(10), big.NewInt(2))
	wantValue(t, d, "safeDiv", 0, big.NewInt(10), big.NewInt(9))
	wantValue(t, d, "either", 1, big.NewInt(1))
	wantValue(t, d, "either", 1, big.NewInt(2))
	wantValue(t, d, "either", 0, big.NewInt(3))
	wantValue(t, d, "invert", 1, big.NewInt(4))
	wantValue(t, d, "invert", 0, big.NewInt(3))
}

func TestElseBranches(t *testing.T) {
	d := deploy(t, `
contract Branch {
    function pick(uint256 a) view returns (uint256) {
        if (a == 1) { return 10; }
        else if (a == 2) { return 20; }
        else { return 30; }
    }
}`)
	wantValue(t, d, "pick", 10, big.NewInt(1))
	wantValue(t, d, "pick", 20, big.NewInt(2))
	wantValue(t, d, "pick", 30, big.NewInt(7))
}

func TestCompoundAssignments(t *testing.T) {
	d := deploy(t, `
contract Acc {
    uint256 total;
    mapping(address => uint256) bal;
    function bump(uint256 n) { total += n; }
    function shrink(uint256 n) { total -= n; }
    function scale(uint256 n) { total *= n; }
    function credit(address who, uint256 n) { bal[who] += n; }
    function get() view returns (uint256) { return total; }
    function balOf(address who) view returns (uint256) { return bal[who]; }
}`)
	d.call(acc(1), 0, "bump", big.NewInt(10))
	d.call(acc(1), 0, "bump", big.NewInt(5))
	wantValue(t, d, "get", 15)
	d.call(acc(1), 0, "shrink", big.NewInt(3))
	wantValue(t, d, "get", 12)
	d.call(acc(1), 0, "scale", big.NewInt(2))
	wantValue(t, d, "get", 24)

	whoAddr := acc(0x77)
	who := new(big.Int).SetBytes(whoAddr[:])
	d.call(acc(1), 0, "credit", who, big.NewInt(9))
	d.call(acc(1), 0, "credit", who, big.NewInt(1))
	wantValue(t, d, "balOf", 10, who)

	// Compound assignment inherits checked arithmetic.
	wantRevert(t, d, "shrink", "underflow", big.NewInt(1000))
}

// require with a reason string surfaces the reason in the revert error (and so
// in the receipt under vm_revert_v1).
func TestRequireWithReason(t *testing.T) {
	d := deploy(t, `
contract Guard {
    uint256 x;
    function set(uint256 v) {
        require(v > 10, "too small");
        x = v;
    }
    function get() view returns (uint256) { return x; }
}`)
	wantRevert(t, d, "set", "too small", big.NewInt(5))
	d.call(acc(1), 0, "set", big.NewInt(11))
	wantValue(t, d, "get", 11)
}

// Visibility and mutability keywords parse and are ignored: they gate nothing
// in the subset (every function is externally dispatchable), but real-world
// sources are full of them.
func TestVisibilityKeywordsIgnored(t *testing.T) {
	d := deploy(t, `
contract Vis {
    uint256 public count;
    mapping(address => uint256) public score;
    function inc() public { count += 1; }
    function get() external view returns (uint256) { return count; }
    function pureCalc(uint256 a) public pure returns (uint256) { return a * 2; }
    function pay() public payable { count += msg.value; }
}`)
	d.call(acc(1), 0, "inc")
	d.call(acc(1), 0, "inc")
	wantValue(t, d, "get", 2)
	wantValue(t, d, "pureCalc", 8, big.NewInt(4))
}
