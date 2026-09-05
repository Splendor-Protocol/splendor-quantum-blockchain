package compiler

import (
	"math/big"
	"strings"
	"testing"
)

// An internal call in statement position discards the callee's return value.
// It must discard it into a slot of its own: before this the discarded value
// was written to local slot 0, which is the caller's first local.
func TestDiscardedInternalReturnDoesNotClobberLocals(t *testing.T) {
	d := deploy(t, `
contract Discard {
    function helper() internal returns (uint256) { return 42; }
    function run() returns (uint256) {
        uint256 x = 7;
        helper();
        return x;
    }
}`)
	wantValue(t, d, "run", 7)
}

// An inlined callee's argument and return slots must sit BELOW the emit
// region: an emit inside the callee laid its data over the argument word.
func TestInlineCallSurvivesEmitInsideCallee(t *testing.T) {
	d := deploy(t, `
contract Emitter {
    function helper(uint256 a) internal returns (uint256) {
        emit Ping(99);
        return a;
    }
    function run() returns (uint256) { return helper(7); }
}`)
	res := d.call(acc(1), 0, "run")
	if res.Reverted {
		t.Fatalf("run reverted: %v", res.Err)
	}
	if got := new(big.Int).SetBytes(res.ReturnValue); got.Int64() != 7 {
		t.Fatalf("helper(7) = %s, want 7 (the emit inside the callee overwrote its argument)", got)
	}
	if len(res.Logs) != 1 || new(big.Int).SetBytes(res.Logs[0].Data).Int64() != 99 {
		t.Fatalf("logs = %+v, want one Ping(99)", res.Logs)
	}
}

// The caller's mapping-slot cache lives above its locals. An inlined call's
// slots were not counted, so its argument word landed on the cache word and
// every later cached access used the argument as a storage slot.
func TestInlineCallSlotsDoNotOverlapMappingCache(t *testing.T) {
	d := deploy(t, `
contract Cache {
    mapping(address => uint256) bal;
    function id(uint256 a) internal returns (uint256) { return a; }
    function run() returns (uint256) {
        bal[msg.sender] = 10;
        uint256 z = id(77);
        bal[msg.sender] = bal[msg.sender] + 1;
        return bal[msg.sender] + z - 77;
    }
}`)
	wantValue(t, d, "run", 11)
}

// A callee's own locals are part of the caller's frame too, and a call nested
// in another call's argument allocates for both.
func TestNestedInlineCallsAndCalleeLocalsAreCounted(t *testing.T) {
	d := deploy(t, `
contract Nested {
    mapping(address => uint256) bal;
    function twice(uint256 a) internal returns (uint256) {
        uint256 t = a * 2;
        return t;
    }
    function plusOne(uint256 a) internal returns (uint256) { return a + 1; }
    function run() returns (uint256) {
        bal[msg.sender] = 3;
        uint256 r = twice(plusOne(bal[msg.sender]));
        emit Ping(r);
        return bal[msg.sender] * 100 + r;
    }
}`)
	wantValue(t, d, "run", 308)
}

// The cache is keyed on the NAME of the key expression. A callee whose
// parameter shares a name with the caller's key must not be handed the
// caller's cached slot: its parameter is a different value.
func TestCalleeMappingAccessIgnoresCallerCache(t *testing.T) {
	d := deploy(t, `
contract Names {
    mapping(uint256 => uint256) bal;
    function readAt(uint256 x) internal view returns (uint256) { return bal[x]; }
    function run(uint256 x, uint256 y) returns (uint256) {
        bal[x] = 5;
        uint256 v = readAt(y);
        return v * 100 + bal[x];
    }
}`)
	wantValue(t, d, "run", 5, big.NewInt(1), big.NewInt(2))
}

// A key the callee reassigns is not stable across the call, so the caller
// must not keep using a slot derived from its old value.
func TestCalleeAssignmentInvalidatesCallerCache(t *testing.T) {
	d := deploy(t, `
contract Bump {
    uint256 k;
    mapping(uint256 => uint256) m;
    function bump() internal { k = k + 1; }
    function run() returns (uint256) {
        m[k] = 5;
        bump();
        m[k] = 7;
        return m[0] + m[1];
    }
}`)
	wantValue(t, d, "run", 12)
}

// Functions are inlined, so a recursive internal call has no finite
// expansion. It is refused at compile time rather than looping.
func TestRecursiveInternalCallIsRefused(t *testing.T) {
	_, err := Compile(`
contract Rec {
    function f(uint256 a) internal returns (uint256) { return f(a); }
}`)
	if err == nil || !strings.Contains(err.Error(), "recursive") {
		t.Fatalf("Compile = %v, want a recursion refusal", err)
	}
	_, err = Compile(`
contract Mutual {
    function a(uint256 n) internal returns (uint256) { return b(n); }
    function b(uint256 n) internal returns (uint256) { return a(n); }
}`)
	if err == nil || !strings.Contains(err.Error(), "recursive") {
		t.Fatalf("mutual recursion: Compile = %v, want a recursion refusal", err)
	}
}

// A callee with no return type has no return word to load.
func TestValueUseOfVoidInternalFunctionIsRefused(t *testing.T) {
	_, err := Compile(`
contract Void {
    uint256 n;
    function poke() internal { n = n + 1; }
    function run() returns (uint256) { return poke(); }
}`)
	if err == nil || !strings.Contains(err.Error(), "returns nothing") {
		t.Fatalf("Compile = %v, want a refusal", err)
	}
}
