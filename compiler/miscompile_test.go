package compiler

import (
	"math/big"
	"strings"
	"testing"
)

// Three miscompiles found by adversarial audit of the slot cache and locals
// work. All three are silent: the source reads correctly, nothing reverts, and
// the contract writes the WRONG storage slot.

// The right operand of && / || is emitted behind a JUMPI that skips it at
// runtime, so a mapping slot derived there is NOT written on the short-circuit
// path. genLogical marked it live at compile time anyway, so every later access
// to that key loaded a never-written memory word — zero — and used 0 as the
// storage slot. Writes then landed on the contract's FIRST state variable.
//
// The shape is ordinary: a guard whose right operand touches a mapping,
// followed by a write to that mapping.
func TestShortCircuitDoesNotLeakMappingSlotCache(t *testing.T) {
	d := deploy(t, `
contract C {
    uint256 totalSupply;
    mapping(uint256 => uint256) bal;
    function credit(uint256 to, uint256 amt) public {
        require(amt > 0 || bal[to] > 0, "bad");
        bal[to] = bal[to] + amt;
    }
    function supply() public view returns (uint256) { return totalSupply; }
    function balOf(uint256 k) public view returns (uint256) { return bal[k]; }
}`)
	if res := d.call(acc(1), 0, "credit", big.NewInt(9), big.NewInt(100)); res.Reverted {
		t.Fatalf("credit reverted: %v", res.Err)
	}
	wantValue(t, d, "balOf", 100, big.NewInt(9))
	// The tell: slot 0 (totalSupply) must be untouched. The bug wrote 100 here
	// and left the caller uncredited.
	wantValue(t, d, "supply", 0)
}

// The same through &&, and through an `if` condition rather than a require —
// genIf compiles its condition outside genBlock, so the leak escaped there too.
func TestShortCircuitAndInIfConditionIsAlsoSafe(t *testing.T) {
	d := deploy(t, `
contract C {
    uint256 first;
    mapping(uint256 => uint256) m;
    function go(uint256 k, uint256 flag) public {
        if (flag == 0 && m[k] == 0) { m[k] = 7; }
        m[k] = m[k] + 1;
    }
    function first_() public view returns (uint256) { return first; }
    function get(uint256 k) public view returns (uint256) { return m[k]; }
}`)
	if res := d.call(acc(1), 0, "go", big.NewInt(4), big.NewInt(0)); res.Reverted {
		t.Fatalf("go reverted: %v", res.Err)
	}
	wantValue(t, d, "get", 8, big.NewInt(4)) // 7 then +1
	wantValue(t, d, "first_", 0)
}

// A numeric literal wider than 256 bits reached asm.pushWidth, which PANICS.
// Compile is called on attacker-supplied source by POST /contract/verify, so a
// panic there is reachable by an unauthenticated caller. net/http recovers a
// handler panic so the node survives, but Compile must not panic on input it is
// designed to receive — and any caller without that recovery (a batch job, an
// indexer, a CLI in a service) would die.
func TestOversizeLiteralIsRejectedNotPanicked(t *testing.T) {
	for _, src := range []string{
		// 65 hex digits — 33 bytes.
		"contract C { function f() public view returns (uint256) { return 0x11111111111111111111111111111111111111111111111111111111111111111; } }",
		// 2^256 exactly, in decimal.
		"contract C { function f() public view returns (uint256) { return 115792089237316195423570985008687907853269984665640564039457584007913129639936; } }",
	} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Compile PANICKED on an oversize literal: %v", r)
				}
			}()
			_, err := Compile(src)
			if err == nil {
				t.Fatal("an oversize literal compiled")
			}
			if !strings.Contains(err.Error(), "256") {
				t.Fatalf("error does not explain the width limit: %v", err)
			}
		}()
	}
}

// The largest legal value must still compile.
func TestMaxUint256LiteralStillCompiles(t *testing.T) {
	if _, err := Compile(`
contract C {
    function f() public view returns (uint256) {
        return 115792089237316195423570985008687907853269984665640564039457584007913129639935;
    }
}`); err != nil {
		t.Fatalf("2^256-1 was rejected: %v", err)
	}
}

// A local that shadows an outer local or a state variable made the compiler
// resolve the SAME name to two different bindings — and the mapping-slot cache,
// keyed on the name text, handed the inner one the outer one's slot. It also
// made `uint256 s = s + 1;` read the not-yet-written inner slot (zero) instead
// of the outer binding.
//
// Solidity has rejected shadowing since 0.5. Rejecting it here removes both
// defects at the source rather than patching each symptom.
func TestShadowingAnOuterLocalIsRejected(t *testing.T) {
	_, err := Compile(`
contract C {
    mapping(uint256 => uint256) m;
    function f(uint256 a) public {
        uint256 k = 1;
        m[k] = 10;
        if (a > 0) { uint256 k = 2; m[k] = 99; }
    }
}`)
	if err == nil {
		t.Fatal("a local shadowing an outer local compiled")
	}
	if !strings.Contains(err.Error(), "shadow") {
		t.Fatalf("error does not explain the shadowing: %v", err)
	}
}

func TestShadowingAStateVariableIsRejected(t *testing.T) {
	_, err := Compile(`
contract C {
    uint256 s;
    function copy() public view returns (uint256) {
        uint256 s = s + 1;
        return s;
    }
}`)
	if err == nil {
		t.Fatal("a local shadowing a state variable compiled")
	}
	if !strings.Contains(err.Error(), "shadow") {
		t.Fatalf("error does not explain the shadowing: %v", err)
	}
}

// Shadowing a mapping's name is the same hazard.
func TestShadowingAMappingIsRejected(t *testing.T) {
	if _, err := Compile(`
contract C {
    mapping(uint256 => uint256) m;
    function f() public { uint256 m = 1; }
}`); err == nil {
		t.Fatal("a local shadowing a mapping compiled")
	}
}

// Two sequential blocks may still each declare the same name — they do not
// shadow one another, and forbidding that would be a real regression.
func TestSiblingBlocksMayReuseAName(t *testing.T) {
	d := deploy(t, `
contract C {
    uint256 out;
    function f(uint256 a) public {
        if (a > 0) { uint256 t = 1; out = out + t; }
        if (a > 0) { uint256 t = 2; out = out + t; }
        for (uint256 i = 0; i < 2; i += 1) { out = out + i; }
        for (uint256 i = 0; i < 2; i += 1) { out = out + i; }
    }
    function get() public view returns (uint256) { return out; }
}`)
	if res := d.call(acc(1), 0, "f", big.NewInt(1)); res.Reverted {
		t.Fatalf("f reverted: %v", res.Err)
	}
	wantValue(t, d, "get", 5) // 1 + 2 + (0+1) + (0+1)
}

// An initialiser must see the OUTER binding, not the slot being declared.
// Without shadowing this is the plain case, and it must keep working.
func TestInitialiserSeesTheOuterBinding(t *testing.T) {
	d := deploy(t, `
contract C {
    uint256 s;
    function set(uint256 v) public { s = v; }
    function derived() public view returns (uint256) {
        uint256 t = s + 1;
        return t;
    }
}`)
	d.call(acc(1), 0, "set", big.NewInt(41))
	wantValue(t, d, "derived", 42)
}
