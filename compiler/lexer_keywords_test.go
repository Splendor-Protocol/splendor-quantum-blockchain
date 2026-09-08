package compiler

import (
	"math/big"
	"strings"
	"testing"
)

// The parser has had break/continue branches for a while; the lexer did not
// tokenise the words as keywords, so `break;` reached the assignment parser
// and failed with "expected '=' in assignment".
func TestBreakAndContinueFromSource(t *testing.T) {
	d := deploy(t, `
contract Loops {
    function firstOver(uint256 limit) view returns (uint256) {
        uint256 found = 0;
        for (uint256 i = 1; i <= 100; i += 1) {
            if (i * i > limit) {
                found = i;
                break;
            }
        }
        return found;
    }
    function sumOdd(uint256 n) view returns (uint256) {
        uint256 total = 0;
        uint256 i = 0;
        while (i < n) {
            i += 1;
            if (i % 2 == 0) {
                continue;
            }
            total += i;
        }
        return total;
    }
}`)
	wantValue(t, d, "firstOver", 4, big.NewInt(10))
	wantValue(t, d, "sumOdd", 25, big.NewInt(10))
}

// bytes32 and string are one 32-byte word each; the ABI must say so. Before
// this both rendered as uint256, so f(bytes32) and f(uint256) were reported as
// a duplicate function and the selector of f(bytes32) was the wrong hash.
func TestBytes32AndStringABI(t *testing.T) {
	c, err := Compile(`
contract Words {
    function tag(bytes32 s) view returns (bytes32) { return s; }
    function tag(uint256 n) view returns (uint256) { return n; }
    function greet() view returns (string) {
        string s = "hi";
        return s;
    }
}`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	byName := map[string]ABIFunction{}
	for _, f := range c.Functions {
		byName[f.Signature] = f
	}
	f, ok := byName["tag(bytes32)"]
	if !ok {
		t.Fatalf("no tag(bytes32) in ABI: %+v", c.Functions)
	}
	if f.Selector != FunctionSelector("tag(bytes32)") {
		t.Fatalf("selector = %x, want SHA3(tag(bytes32))[:4]", f.Selector)
	}
	if strings.Join(f.Params, ",") != "bytes32" || f.Returns != "bytes32" {
		t.Fatalf("params/returns = %v/%q, want bytes32/bytes32", f.Params, f.Returns)
	}
	if _, ok := byName["tag(uint256)"]; !ok {
		t.Fatalf("tag(uint256) missing: %+v", c.Functions)
	}
	if g := byName["greet()"]; g.Returns != "bytes32" {
		t.Fatalf("string return rendered as %q, want bytes32", g.Returns)
	}

	d := deploy(t, `
contract Greeter {
    function greet() view returns (string) {
        string s = "hi";
        return s;
    }
}`)
	res := d.mustCall(acc(1), 0, "greet")
	if len(res.ReturnValue) != 32 || string(res.ReturnValue[:2]) != "hi" || res.ReturnValue[2] != 0 {
		t.Fatalf("greet() = %x, want \"hi\" left-aligned in a zero-padded word", res.ReturnValue)
	}
}
