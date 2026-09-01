package compiler

import (
	"math/big"
	"testing"

	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
)

// countOp returns how many times an opcode appears in compiled bytecode.
func countOp(t *testing.T, src string, name string) int {
	t.Helper()
	c, err := Compile(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	n := 0
	for _, in := range vm.Disassemble(c.Bytecode) {
		if in.Name == name {
			n++
		}
	}
	return n
}

// A mapping slot is SHA3-256(pad32(key) ‖ pad32(baseSlot)), and the engine
// charges SHA3 per BYTE — 30 + 64*6 = 414 gas every time one is derived. That
// makes slot derivation, not SLOAD, the dominant cost of a mapping access, and
// `m[k] = m[k] + 1` derived the identical slot twice.
//
// The slot depends only on the key and the base slot, so it is invariant for as
// long as the key is: caching it costs one MSTORE and reuses cost one MLOAD (3
// gas each) against 414 to recompute.
func TestMappingSlotIsDerivedOncePerStatement(t *testing.T) {
	src := `
contract C {
    mapping(uint256 => uint256) m;
    function bump(uint256 k) public { m[k] = m[k] + 1; }
}`
	if got := countOp(t, src, "SHA3_MEM"); got != 1 {
		t.Fatalf("m[k] = m[k] + 1 derives the slot %d times, want 1", got)
	}
}

// The same across consecutive straight-line statements — the token `transfer`
// shape, which read the sender's balance three times.
func TestMappingSlotIsReusedAcrossStraightLineStatements(t *testing.T) {
	src := `
contract C {
    mapping(address => uint256) bal;
    function send(uint256 amount) public {
        require(bal[msg.sender] >= amount, "low");
        bal[msg.sender] = bal[msg.sender] - amount;
    }
}`
	if got := countOp(t, src, "SHA3_MEM"); got != 1 {
		t.Fatalf("three accesses to bal[msg.sender] derive the slot %d times, want 1", got)
	}
}

// Two DIFFERENT keys are two different slots and must each be derived.
func TestDistinctKeysStillDeriveDistinctSlots(t *testing.T) {
	src := `
contract C {
    mapping(address => uint256) bal;
    function move(address to, uint256 amount) public {
        bal[msg.sender] = bal[msg.sender] - amount;
        bal[to] = bal[to] + amount;
    }
}`
	if got := countOp(t, src, "SHA3_MEM"); got != 2 {
		t.Fatalf("two distinct keys derive the slot %d times, want 2", got)
	}
}

// A key that is REASSIGNED is not the same key any more, so its slot must not
// be reused. Caching on the name alone would send both writes to one slot.
func TestReassignedKeyIsNotCached(t *testing.T) {
	d := deploy(t, `
contract C {
    mapping(uint256 => uint256) m;
    function two(uint256 k) public {
        m[k] = 11;
        k = k + 1;
        m[k] = 22;
    }
    function get(uint256 k) public view returns (uint256) { return m[k]; }
}`)
	if res := d.call(acc(1), 0, "two", big.NewInt(5)); res.Reverted {
		t.Fatalf("two reverted: %v", res.Err)
	}
	wantValue(t, d, "get", 11, big.NewInt(5))
	wantValue(t, d, "get", 22, big.NewInt(6))
}

// Reuse must not leak across a branch: the slot the `if` derived is only in
// memory when the branch actually ran, so a later statement must derive its own
// rather than load a word that may never have been written.
func TestMappingSlotIsNotReusedAcrossABranch(t *testing.T) {
	d := deploy(t, `
contract C {
    mapping(uint256 => uint256) m;
    function maybe(uint256 k, uint256 flag) public {
        if (flag == 1) { m[k] = 5; }
        m[k] = m[k] + 1;
    }
    function get(uint256 k) public view returns (uint256) { return m[k]; }
}`)
	// flag = 0: the branch never runs, so m[k] must go 0 -> 1.
	if res := d.call(acc(1), 0, "maybe", big.NewInt(7), big.NewInt(0)); res.Reverted {
		t.Fatalf("maybe(7,0) reverted: %v", res.Err)
	}
	wantValue(t, d, "get", 1, big.NewInt(7))

	// flag = 1: the branch sets 5, so m[k] must go 5 -> 6.
	if res := d.call(acc(1), 0, "maybe", big.NewInt(8), big.NewInt(1)); res.Reverted {
		t.Fatalf("maybe(8,1) reverted: %v", res.Err)
	}
	wantValue(t, d, "get", 6, big.NewInt(8))
}

// The same inside a loop body, where a cached slot from a previous iteration
// must not be reused after the counter moved.
func TestMappingSlotIsCorrectAcrossLoopIterations(t *testing.T) {
	d := deploy(t, `
contract C {
    mapping(uint256 => uint256) m;
    function fill(uint256 n) public {
        for (uint256 i = 0; i < n; i += 1) { m[i] = m[i] + i; }
    }
    function get(uint256 k) public view returns (uint256) { return m[k]; }
}`)
	if res := d.call(acc(1), 0, "fill", big.NewInt(4)); res.Reverted {
		t.Fatalf("fill reverted: %v", res.Err)
	}
	for k := int64(0); k < 4; k++ {
		wantValue(t, d, "get", k, big.NewInt(k))
	}
	wantValue(t, d, "get", 0, big.NewInt(9)) // never written
}

// Parameters became memory locals so a function could assign to its own
// parameters, as Solidity allows. But the copy is only WORTH anything where an
// assignment actually happens: calldata reads cost the same 3 gas as memory
// reads, so every function that never writes its parameters was paying a
// PUSH/PUSH/CALLDATALOAD/MSTORE prologue per parameter for nothing.
func TestUnassignedParamsAreNotCopiedToMemory(t *testing.T) {
	src := `
contract C {
    function get(uint256 a) public view returns (uint256) { return a; }
}`
	// The only MSTORE left should be the one staging the return value.
	if got := countOp(t, src, "MSTORE"); got != 1 {
		t.Fatalf("MSTORE count = %d, want 1 — the parameter is never assigned, so it needs no copy", got)
	}
}

// Where the function DOES assign its parameter, the copy is required and must
// still be correct: calldata is read-only.
func TestAssignedParamIsStillCopiedAndWritable(t *testing.T) {
	d := deploy(t, `
contract C {
    function halve(uint256 n) public view returns (uint256) {
        n = n / 2;
        return n;
    }
    function untouched(uint256 n) public view returns (uint256) { return n + 1; }
}`)
	wantValue(t, d, "halve", 21, big.NewInt(42))
	wantValue(t, d, "untouched", 43, big.NewInt(42))
}

// A mapping keyed on an unassigned parameter is still cacheable — the key now
// comes from calldata rather than memory, which must not change the slot.
func TestMappingKeyedOnCalldataParamStillCaches(t *testing.T) {
	src := `
contract C {
    mapping(uint256 => uint256) m;
    function bump(uint256 k) public { m[k] = m[k] + 1; }
}`
	if got := countOp(t, src, "SHA3_MEM"); got != 1 {
		t.Fatalf("slot derived %d times, want 1", got)
	}
	d := deploy(t, src)
	d.call(acc(1), 0, "bump", big.NewInt(3))
	d.call(acc(1), 0, "bump", big.NewInt(3))
	d.call(acc(1), 0, "bump", big.NewInt(4))
	got := d.call(acc(1), 0, "bump", big.NewInt(3))
	if got.Reverted {
		t.Fatalf("bump reverted: %v", got.Err)
	}
}

// The token sample must keep behaving exactly as before, at lower gas.
func TestTokenStillCorrectAfterSlotCaching(t *testing.T) {
	d := deploy(t, `
contract Token {
    mapping(address => uint256) balances;
    address owner;
    uint256 totalSupply;
    function init() { require(owner == 0); owner = msg.sender; }
    function mint(address to, uint256 amount) {
        require(msg.sender == owner);
        balances[to] = balances[to] + amount;
        totalSupply = totalSupply + amount;
    }
    function transfer(address to, uint256 amount) {
        require(balances[msg.sender] >= amount);
        balances[msg.sender] = balances[msg.sender] - amount;
        balances[to] = balances[to] + amount;
    }
    function balanceOf(address who) view returns (uint256) { return balances[who]; }
}`)
	owner := acc(1)
	other := acc(2)
	otherWord := new(big.Int).SetBytes(other[:])
	ownerWord := new(big.Int).SetBytes(owner[:])

	d.call(owner, 0, "init")
	if res := d.call(owner, 0, "mint", ownerWord, big.NewInt(1000)); res.Reverted {
		t.Fatalf("mint reverted: %v", res.Err)
	}
	if res := d.call(owner, 0, "transfer", otherWord, big.NewInt(250)); res.Reverted {
		t.Fatalf("transfer reverted: %v", res.Err)
	}
	wantValue(t, d, "balanceOf", 750, ownerWord)
	wantValue(t, d, "balanceOf", 250, otherWord)

	// And the sender's slot is derived once, not three times.
	res := d.call(owner, 0, "transfer", otherWord, big.NewInt(1))
	if res.Reverted {
		t.Fatalf("transfer reverted: %v", res.Err)
	}
	t.Logf("transfer gas: %d", res.GasUsed)
}
