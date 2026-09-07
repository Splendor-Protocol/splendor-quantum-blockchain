package compiler

import (
	"bytes"
	"math/big"
	"os"
	"strconv"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
)

// fleetSize is how many contracts the stress test builds. Override with
// FLEET_SIZE for a bigger sweep than CI wants to pay for on every run.
func fleetSize(t *testing.T) int {
	t.Helper()
	if v := os.Getenv("FLEET_SIZE"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			t.Fatalf("FLEET_SIZE=%q is not a positive integer", v)
		}
		return n
	}
	return 250
}

// A few hundred generated contracts exercise what one hand-written sample
// cannot: whether compilation stays deterministic across many distinct inputs,
// whether deploy addresses collide at volume, whether a contract can end up
// with two functions sharing a 4-byte selector (which would make one of them
// permanently unreachable behind the dispatcher), and whether verification —
// recompile and byte-compare — holds for bytecode nobody wrote by hand.
//
// It is the same pipeline sqc-deploy drives against a live node, run against
// the real engine so it can fail fast and reproducibly.
func TestFleetCompilesDeploysAndVerifies(t *testing.T) {
	n := fleetSize(t)
	fleet := GenerateFleet(1, n)
	if len(fleet) != n {
		t.Fatalf("generated %d contracts, want %d", len(fleet), n)
	}

	sdb, err := state.NewStateDB(t.TempDir())
	if err != nil {
		t.Fatalf("statedb: %v", err)
	}
	defer sdb.Close()
	engine := vm.NewEngine(sdb)

	deployer := acc(0xDE)
	caller := acc(0xCA)
	seen := map[qcrypto.Address]string{}

	var totalBytes int
	for i, gen := range fleet {
		c, err := Compile(gen.Source)
		if err != nil {
			t.Fatalf("%s failed to compile: %v\n--- source ---\n%s", gen.Name, err, gen.Source)
		}
		totalBytes += len(c.Bytecode)

		// Determinism: the property source verification rests on.
		again, err := Compile(gen.Source)
		if err != nil {
			t.Fatalf("%s compiled once but not twice: %v", gen.Name, err)
		}
		if !bytes.Equal(c.Bytecode, again.Bytecode) {
			t.Fatalf("%s compiled to different bytecode on a second run", gen.Name)
		}

		// Two functions sharing a selector would leave one unreachable behind
		// the dispatcher — silently, and only for that contract.
		bySelector := map[[4]byte]string{}
		for _, f := range c.Functions {
			if prev, dup := bySelector[f.Selector]; dup {
				t.Fatalf("%s: %q and %q share selector %x — one is unreachable",
					gen.Name, prev, f.Signature, f.Selector)
			}
			bySelector[f.Selector] = f.Signature
		}

		addr, err := engine.Deploy(deployer, c.Bytecode, uint64(i))
		if err != nil {
			t.Fatalf("%s failed to deploy: %v", gen.Name, err)
		}
		if prev, dup := seen[addr]; dup {
			t.Fatalf("deploy address collision: %s and %s both at %s", prev, gen.Name, addr)
		}
		seen[addr] = gen.Name
		if _, err := sdb.Commit(); err != nil {
			t.Fatalf("commit after %s: %v", gen.Name, err)
		}

		// Verification, exactly as POST /contract/verify does it.
		acct, err := sdb.GetAccount(addr)
		if err != nil {
			t.Fatalf("%s: read account: %v", gen.Name, err)
		}
		onchain, err := sdb.GetCode(acct.CodeHash)
		if err != nil {
			t.Fatalf("%s: read code: %v", gen.Name, err)
		}
		if !bytes.Equal(onchain, c.Bytecode) {
			t.Fatalf("%s: deployed code does not match its own source (%d on-chain vs %d compiled)",
				gen.Name, len(onchain), len(c.Bytecode))
		}

		fns := map[string]ABIFunction{}
		for _, f := range c.Functions {
			fns[f.Name] = f
		}
		call := func(fn string, args ...int64) *vm.ExecutionResult {
			t.Helper()
			f, ok := fns[fn]
			if !ok {
				t.Fatalf("%s has no function %q", gen.Name, fn)
			}
			bigArgs := make([]*big.Int, len(args))
			for i, a := range args {
				bigArgs[i] = big.NewInt(a)
			}
			data, err := EncodeCall(f, bigArgs...)
			if err != nil {
				t.Fatalf("%s: encode %s: %v", gen.Name, fn, err)
			}
			res, _ := engine.CallWithContext(caller, caller, addr, data, big.NewInt(0), 1, 0, false)
			return res
		}

		// Probes: results predictable from the source alone.
		for _, p := range gen.Probes {
			res := call(p.Fn, p.Args...)
			if res == nil || res.Reverted {
				t.Fatalf("%s: %s%v reverted: %v", gen.Name, p.Fn, p.Args, res.Err)
			}
			got := new(big.Int).SetBytes(res.ReturnValue)
			if got.Cmp(big.NewInt(p.Want)) != 0 {
				t.Fatalf("%s: %s%v = %s, want %d", gen.Name, p.Fn, p.Args, got, p.Want)
			}
		}

		// Stateful round-trips, where the shape provides them.
		if _, ok := fns["set"]; ok {
			if res := call("set", 4242); res.Reverted {
				t.Fatalf("%s: set reverted: %v", gen.Name, res.Err)
			}
			res := call("get")
			if got := new(big.Int).SetBytes(res.ReturnValue); got.Cmp(big.NewInt(4242)) != 0 {
				t.Fatalf("%s: get after set = %s, want 4242", gen.Name, got)
			}
		}
		if _, ok := fns["put"]; ok {
			if res := call("put", 7, 99); res.Reverted {
				t.Fatalf("%s: put reverted: %v", gen.Name, res.Err)
			}
			res := call("fetch", 7)
			if got := new(big.Int).SetBytes(res.ReturnValue); got.Cmp(big.NewInt(99)) != 0 {
				t.Fatalf("%s: fetch after put = %s, want 99", gen.Name, got)
			}
			res = call("fetch", 8)
			if got := new(big.Int).SetBytes(res.ReturnValue); got.Sign() != 0 {
				t.Fatalf("%s: fetch of an unwritten key = %s, want 0", gen.Name, got)
			}
		}

		// The uniform guard: claimable once, then refused.
		if res := call("claim"); res.Reverted {
			t.Fatalf("%s: first claim reverted: %v", gen.Name, res.Err)
		}
		if res := call("claim"); !res.Reverted {
			t.Fatalf("%s: second claim succeeded; the owner guard does not hold", gen.Name)
		}
	}

	t.Logf("fleet: %d contracts, %d unique addresses, %d bytes of bytecode (avg %d)",
		len(fleet), len(seen), totalBytes, totalBytes/len(fleet))
}

// A wrong source must fail verification for every member of the fleet, or
// "verified" means nothing. Pairs each contract's code with its neighbour's
// source, which is a valid contract but not that one.
func TestFleetRejectsMismatchedSources(t *testing.T) {
	fleet := GenerateFleet(2, 40)
	compiled := make([][]byte, len(fleet))
	for i, gen := range fleet {
		c, err := Compile(gen.Source)
		if err != nil {
			t.Fatalf("%s: %v", gen.Name, err)
		}
		compiled[i] = c.Bytecode
	}
	for i := range fleet {
		j := (i + 1) % len(fleet)
		if bytes.Equal(compiled[i], compiled[j]) {
			t.Fatalf("%s and %s compile identically; the fleet is not varied enough to test mismatch",
				fleet[i].Name, fleet[j].Name)
		}
	}
}

// Deploying the same fleet twice must produce identical bytecode: the property
// that lets anyone reproduce a deployed contract from its source.
func TestFleetGenerationIsReproducible(t *testing.T) {
	a := GenerateFleet(7, 25)
	b := GenerateFleet(7, 25)
	for i := range a {
		if a[i].Source != b[i].Source {
			t.Fatalf("contract %d differs between two generations from the same seed", i)
		}
	}
}
