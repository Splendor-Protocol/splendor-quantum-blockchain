package compiler

import (
	"math/big"
	"testing"
)

// A local is a named 256-bit word that lives for one call. Without them there
// is no loop counter, and without a loop counter most real contract logic
// cannot be written at all.
func TestLocalVariables(t *testing.T) {
	d := deploy(t, `
contract Locals {
    uint256 stored;
    function compute(uint256 a, uint256 b) view returns (uint256) {
        uint256 sum = a + b;
        uint256 doubled = sum * 2;
        return doubled;
    }
    function shadowsNothing(uint256 a) returns (uint256) {
        uint256 stored2 = a + 1;
        stored = stored2;
        return stored;
    }
}`)
	wantValue(t, d, "compute", 14, big.NewInt(3), big.NewInt(4))
	wantValue(t, d, "shadowsNothing", 8, big.NewInt(7))
}

// A local is assignable after declaration, and reads see the latest value.
func TestLocalReassignment(t *testing.T) {
	d := deploy(t, `
contract L {
    function run() view returns (uint256) {
        uint256 x = 1;
        x = x + 10;
        x += 100;
        return x;
    }
}`)
	wantValue(t, d, "run", 111)
}

func TestForLoop(t *testing.T) {
	d := deploy(t, `
contract Sum {
    function upTo(uint256 n) view returns (uint256) {
        uint256 total = 0;
        for (uint256 i = 1; i <= n; i += 1) {
            total += i;
        }
        return total;
    }
}`)
	wantValue(t, d, "upTo", 15, big.NewInt(5)) // 1+2+3+4+5
	wantValue(t, d, "upTo", 1, big.NewInt(1))
	wantValue(t, d, "upTo", 0, big.NewInt(0)) // body never runs
}

func TestWhileLoop(t *testing.T) {
	d := deploy(t, `
contract W {
    function halvings(uint256 n) view returns (uint256) {
        uint256 steps = 0;
        while (n > 1) {
            n = n / 2;
            steps += 1;
        }
        return steps;
    }
}`)
	wantValue(t, d, "halvings", 3, big.NewInt(8))
	wantValue(t, d, "halvings", 0, big.NewInt(1))
}

func TestNestedLoops(t *testing.T) {
	d := deploy(t, `
contract N {
    function product(uint256 a, uint256 b) view returns (uint256) {
        uint256 total = 0;
        for (uint256 i = 0; i < a; i += 1) {
            for (uint256 j = 0; j < b; j += 1) {
                total += 1;
            }
        }
        return total;
    }
}`)
	wantValue(t, d, "product", 12, big.NewInt(3), big.NewInt(4))
	wantValue(t, d, "product", 0, big.NewInt(0), big.NewInt(5))
}

// A loop that writes storage each iteration — the shape a real contract uses,
// and a check that locals and storage do not interfere.
func TestLoopWritingStorage(t *testing.T) {
	d := deploy(t, `
contract Acc {
    mapping(uint256 => uint256) squares;
    function fill(uint256 n) {
        for (uint256 i = 0; i < n; i += 1) {
            squares[i] = i * i;
        }
    }
    function get(uint256 k) view returns (uint256) { return squares[k]; }
}`)
	d.call(acc(1), 0, "fill", big.NewInt(5))
	wantValue(t, d, "get", 0, big.NewInt(0))
	wantValue(t, d, "get", 9, big.NewInt(3))
	wantValue(t, d, "get", 16, big.NewInt(4))
	wantValue(t, d, "get", 0, big.NewInt(9)) // never written
}

// Locals live in memory, and so does the data an `emit` lays out. A function
// with both must not have one clobber the other.
func TestLocalsDoNotCollideWithEmitData(t *testing.T) {
	d := deploy(t, `
contract E {
    uint256 last;
    function go(uint256 a) {
        uint256 x = a + 1;
        uint256 y = x * 2;
        emit Two(x, y);
        last = y;
    }
    function get() view returns (uint256) { return last; }
}`)
	res := d.call(acc(1), 0, "go", big.NewInt(4))
	if res.Reverted {
		t.Fatalf("go reverted: %v", res.Err)
	}
	if len(res.Logs) != 1 {
		t.Fatalf("got %d logs, want 1", len(res.Logs))
	}
	data := res.Logs[0].Data
	if len(data) != 64 {
		t.Fatalf("log data is %d bytes, want 64", len(data))
	}
	if got := new(big.Int).SetBytes(data[:32]); got.Cmp(big.NewInt(5)) != 0 {
		t.Fatalf("log word 0 = %s, want 5 (x)", got)
	}
	if got := new(big.Int).SetBytes(data[32:]); got.Cmp(big.NewInt(10)) != 0 {
		t.Fatalf("log word 1 = %s, want 10 (y)", got)
	}
	wantValue(t, d, "get", 10)
}

// An unbounded loop is not a hang: the VM's gas ceiling ends it, and the call
// reverts rather than wedging the node.
func TestUnboundedLoopRunsOutOfGas(t *testing.T) {
	d := deploy(t, `
contract Spin {
    function forever() view returns (uint256) {
        uint256 i = 0;
        while (i >= 0) { i += 1; }
        return i;
    }
}`)
	res := d.call(acc(1), 0, "forever")
	if res.Err == nil {
		t.Fatal("an unbounded loop returned normally")
	}
}

// Using a name that was never declared is a compile error, not a silent zero.
func TestUndeclaredLocalIsRejected(t *testing.T) {
	if _, err := Compile(`
contract Bad {
    function f() view returns (uint256) { return notDeclared; }
}`); err == nil {
		t.Fatal("an undeclared identifier compiled")
	}
}

// Declaring the same local twice in one function is a mistake worth catching.
func TestDuplicateLocalIsRejected(t *testing.T) {
	if _, err := Compile(`
contract Bad {
    function f() view returns (uint256) {
        uint256 x = 1;
        uint256 x = 2;
        return x;
    }
}`); err == nil {
		t.Fatal("a duplicate local declaration compiled")
	}
}

// A local must not silently shadow a parameter.
func TestLocalShadowingParamIsRejected(t *testing.T) {
	if _, err := Compile(`
contract Bad {
    function f(uint256 a) view returns (uint256) {
        uint256 a = 2;
        return a;
    }
}`); err == nil {
		t.Fatal("a local shadowing a parameter compiled")
	}
}
