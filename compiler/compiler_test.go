package compiler

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
)

// ── harness ───────────────────────────────────────────────────────────────────

func newState(t *testing.T) *state.StateDB {
	t.Helper()
	sdb, err := state.NewStateDB(t.TempDir())
	if err != nil {
		t.Fatalf("open statedb: %v", err)
	}
	t.Cleanup(func() { _ = sdb.Close() })
	return sdb
}

func acc(b byte) qcrypto.Address {
	var a qcrypto.Address
	for i := range a {
		a[i] = b
	}
	return a
}

// deployed compiles src, installs the runtime code at a fixed address, and
// returns a handle for calling it.
type deployed struct {
	t        *testing.T
	sdb      *state.StateDB
	engine   *vm.Engine
	contract *Contract
	addr     qcrypto.Address
	fns      map[string]ABIFunction
}

func deploy(t *testing.T, src string) *deployed {
	t.Helper()
	c, err := Compile(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	sdb := newState(t)
	at := acc(0xC0)
	h := qcrypto.Hash32(c.Bytecode)
	sdb.SetCode(h, c.Bytecode)
	a := types.NewAccount(at)
	a.IsContract = true
	a.CodeHash = h
	sdb.SetAccount(a)
	if _, err := sdb.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	fns := map[string]ABIFunction{}
	for _, f := range c.Functions {
		fns[f.Name] = f
	}
	return &deployed{t: t, sdb: sdb, engine: vm.NewEngine(sdb), contract: c, addr: at, fns: fns}
}

// call invokes fn as caller, returns the execution result.
func (d *deployed) call(caller qcrypto.Address, value int64, fn string, args ...*big.Int) *vm.ExecutionResult {
	d.t.Helper()
	f, ok := d.fns[fn]
	if !ok {
		d.t.Fatalf("no function %q", fn)
	}
	data, err := EncodeCall(f, args...)
	if err != nil {
		d.t.Fatalf("encode %s: %v", fn, err)
	}
	res, _ := d.engine.CallWithContext(caller, caller, d.addr, data, big.NewInt(value), 1, 0, false)
	return res
}

func (d *deployed) mustCall(caller qcrypto.Address, value int64, fn string, args ...*big.Int) *vm.ExecutionResult {
	d.t.Helper()
	res := d.call(caller, value, fn, args...)
	if res.Err != nil {
		d.t.Fatalf("call %s: %v", fn, res.Err)
	}
	return res
}

func mustBig(s string) *big.Int {
	v, ok := new(big.Int).SetString(s, 10)
	if !ok {
		panic("bad number " + s)
	}
	return v
}

// ── Counter ───────────────────────────────────────────────────────────────────

const counterSrc = `
contract Counter {
    uint256 count;
    function inc() { count = count + 1; }
    function add(uint256 n) { count = count + n; }
    function get() view returns (uint256) { return count; }
}
`

func TestCounter(t *testing.T) {
	d := deploy(t, counterSrc)
	user := acc(0x11)

	// get on a fresh contract → 0
	res := d.mustCall(user, 0, "get")
	if got := new(big.Int).SetBytes(res.ReturnValue); got.Sign() != 0 {
		t.Fatalf("initial get = %s, want 0", got)
	}

	d.mustCall(user, 0, "inc")
	d.mustCall(user, 0, "inc")
	d.mustCall(user, 0, "add", big.NewInt(40))

	res = d.mustCall(user, 0, "get")
	if got := new(big.Int).SetBytes(res.ReturnValue); got.Cmp(big.NewInt(42)) != 0 {
		t.Fatalf("get = %s, want 42", got)
	}
}

// ── Token ─────────────────────────────────────────────────────────────────────

const tokenSrc = `
contract Token {
    mapping(address => uint256) balances;
    address owner;
    uint256 totalSupply;

    function init() {
        require(owner == 0);
        owner = msg.sender;
    }
    function mint(address to, uint256 amount) {
        require(msg.sender == owner);
        balances[to] = balances[to] + amount;
        totalSupply = totalSupply + amount;
        emit Mint(to, amount);
    }
    function transfer(address to, uint256 amount) {
        require(balances[msg.sender] >= amount);
        balances[msg.sender] = balances[msg.sender] - amount;
        balances[to] = balances[to] + amount;
        emit Transfer(msg.sender, to, amount);
    }
    function balanceOf(address who) view returns (uint256) {
        return balances[who];
    }
    function supply() view returns (uint256) { return totalSupply; }
}
`

func addrBig(a qcrypto.Address) *big.Int { return new(big.Int).SetBytes(a[:]) }

func TestToken(t *testing.T) {
	d := deploy(t, tokenSrc)
	owner := acc(0x21)
	alice := acc(0x22)
	bob := acc(0x23)

	// init claims ownership; a second init reverts (owner already set).
	d.mustCall(owner, 0, "init")
	if res := d.call(alice, 0, "init"); res.Err == nil || !res.Reverted {
		t.Fatal("second init must revert")
	}

	// Non-owner mint reverts; owner mint succeeds and logs.
	if res := d.call(alice, 0, "mint", addrBig(alice), big.NewInt(1000)); res.Err == nil || !res.Reverted {
		t.Fatal("non-owner mint must revert")
	}
	res := d.mustCall(owner, 0, "mint", addrBig(alice), big.NewInt(1000))
	if len(res.Logs) != 1 {
		t.Fatalf("mint emitted %d logs, want 1", len(res.Logs))
	}
	mintTopic := qcrypto.Hash32([]byte("Mint(uint256,uint256)"))
	if res.Logs[0].Topics[0] != mintTopic {
		t.Fatalf("mint topic = %x, want %x", res.Logs[0].Topics[0], mintTopic)
	}
	// Log data is the two 32-byte args: to, amount.
	if len(res.Logs[0].Data) != 64 {
		t.Fatalf("mint data = %d bytes, want 64", len(res.Logs[0].Data))
	}
	gotTo := new(big.Int).SetBytes(res.Logs[0].Data[0:32])
	gotAmt := new(big.Int).SetBytes(res.Logs[0].Data[32:64])
	if gotTo.Cmp(addrBig(alice)) != 0 || gotAmt.Cmp(big.NewInt(1000)) != 0 {
		t.Fatalf("mint log = (%s,%s)", gotTo, gotAmt)
	}

	assertBal := func(who qcrypto.Address, want int64) {
		t.Helper()
		r := d.mustCall(who, 0, "balanceOf", addrBig(who))
		if got := new(big.Int).SetBytes(r.ReturnValue); got.Cmp(big.NewInt(want)) != 0 {
			t.Fatalf("balanceOf(%x)=%s, want %d", who[0], got, want)
		}
	}
	assertBal(alice, 1000)

	// transfer moves balance and logs Transfer.
	res = d.mustCall(alice, 0, "transfer", addrBig(bob), big.NewInt(400))
	transferTopic := qcrypto.Hash32([]byte("Transfer(uint256,uint256,uint256)"))
	if len(res.Logs) != 1 || res.Logs[0].Topics[0] != transferTopic {
		t.Fatalf("transfer log wrong: %+v", res.Logs)
	}
	assertBal(alice, 600)
	assertBal(bob, 400)

	// Overspend reverts and changes nothing.
	if res := d.call(bob, 0, "transfer", addrBig(alice), big.NewInt(9999)); res.Err == nil || !res.Reverted {
		t.Fatal("overspend must revert")
	}
	assertBal(bob, 400)

	// supply reflects the single mint.
	if r := d.mustCall(owner, 0, "supply"); new(big.Int).SetBytes(r.ReturnValue).Cmp(big.NewInt(1000)) != 0 {
		t.Fatalf("supply = %s, want 1000", new(big.Int).SetBytes(r.ReturnValue))
	}
}

// A call whose selector matches nothing reverts (the dispatcher's fallthrough).
func TestUnknownSelectorReverts(t *testing.T) {
	d := deploy(t, counterSrc)
	res, _ := d.engine.CallWithContext(acc(1), acc(1), d.addr, []byte{0xde, 0xad, 0xbe, 0xef}, big.NewInt(0), 1, 0, false)
	if res.Err == nil || !res.Reverted {
		t.Fatalf("unknown selector should revert, got err=%v reverted=%v", res.Err, res.Reverted)
	}
}

// ── ABI / selectors ───────────────────────────────────────────────────────────

func TestCanonicalAddressSelectorGolden(t *testing.T) {
	// Independently: SHA3-256("transfer(address,uint256)")[:4] = 4b40e901
	// SHA3-256("transfer(sqaddress,uint256)")[:4] = 14cde9ab
	got := FunctionSelector("transfer(address,uint256)")
	if hex := fmt.Sprintf("%x", got[:]); hex != "4b40e901" {
		t.Fatalf("canonical transfer(address,uint256) selector = %s, want 4b40e901", hex)
	}
	legacy := FunctionSelector("transfer(sqaddress,uint256)")
	if hex := fmt.Sprintf("%x", legacy[:]); hex != "14cde9ab" {
		t.Fatalf("legacy transfer(sqaddress,uint256) selector = %s, want 14cde9ab", hex)
	}
	c, err := Compile(tokenSrc)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	found := false
	for _, f := range c.Functions {
		if f.Name != "transfer" {
			continue
		}
		found = true
		if f.Signature != "transfer(address,uint256)" {
			t.Fatalf("compiler ABI spelling = %q, want transfer(address,uint256)", f.Signature)
		}
	}
	if !found {
		t.Fatal("compiled Token has no transfer function")
	}
}

func TestSelectorsAreSha3Based(t *testing.T) {
	c, err := Compile(tokenSrc)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	// The runtime dispatcher derives the selector as the top 4 bytes of
	// SHA3-256(signature); the ABI must agree byte-for-byte.
	for _, f := range c.Functions {
		want := qcrypto.Hash32([]byte(f.Signature))
		if selectorU32(f.Selector) != uint32(want[0])<<24|uint32(want[1])<<16|uint32(want[2])<<8|uint32(want[3]) {
			t.Fatalf("%s selector mismatch", f.Signature)
		}
	}
}

// ── determinism ───────────────────────────────────────────────────────────────

func TestCompileDeterministic(t *testing.T) {
	a, err := Compile(tokenSrc)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Compile(tokenSrc)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Bytecode) != len(b.Bytecode) {
		t.Fatalf("nondeterministic length %d vs %d", len(a.Bytecode), len(b.Bytecode))
	}
	for i := range a.Bytecode {
		if a.Bytecode[i] != b.Bytecode[i] {
			t.Fatalf("nondeterministic byte at %d", i)
		}
	}
}

// ── parse / semantic errors ───────────────────────────────────────────────────

func TestCompileErrors(t *testing.T) {
	cases := []string{
		`contract A { function f() { x = 1; } }`,                              // unknown state var
		`contract A { uint256 x; function f() { y = x; } }`,                   // wait: y unknown target
		`contract A { mapping(address=>uint256) m; function f() { x = m; } }`, // mapping without key
		`contract A { function f( { } }`,                                      // parse error
	}
	for i, src := range cases {
		if _, err := Compile(src); err == nil {
			t.Errorf("case %d compiled but should have failed", i)
		}
	}
}

// mustBig is exercised so the linter does not flag it as unused across builds.
var _ = mustBig

// TestTopLevelRevertDoesNotRollBackStorage documents a load-bearing VM fact the
// compiler designs around: at the ENGINE level, a REVERT in the top-level call
// frame does NOT undo the SSTOREs made before it — the engine snapshots
// sub-calls (OpCALL) but runs the entry frame directly against state. The
// EXECUTOR is what cleans up: pre-fork it treats the reverted call as
// unappliable (per-tx snapshot reverts everything, tx dropped); under
// vm_revert_v1 it wraps the frame in a nested snapshot and includes the tx
// with a failed receipt. Generated code still places every `require` BEFORE
// any storage write (checks-effects) — good hygiene, and the only protection
// for callers that embed the engine without the executor's snapshots.
// Hand-written bytecode proves the underlying engine behavior.
func TestTopLevelRevertDoesNotRollBackStorage(t *testing.T) {
	sdb := newState(t)
	at := acc(0xD0)
	// PUSH1 0 (slot); PUSH1 1 (val); SSTORE; PUSH1 0; PUSH1 0; REVERT
	code := []byte{
		vm.OpPUSH1, 0x00, vm.OpPUSH1, 0x01, vm.OpSSTORE,
		vm.OpPUSH1, 0x00, vm.OpPUSH1, 0x00, vm.OpREVERT,
	}
	h := qcrypto.Hash32(code)
	sdb.SetCode(h, code)
	a := types.NewAccount(at)
	a.IsContract = true
	a.CodeHash = h
	sdb.SetAccount(a)
	if _, err := sdb.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	e := vm.NewEngine(sdb)
	res, _ := e.CallWithContext(acc(1), acc(1), at, nil, big.NewInt(0), 1, 0, false)
	if !res.Reverted {
		t.Fatalf("expected revert, got %+v", res)
	}
	var slot0 [32]byte
	v, err := sdb.GetStorageValue(at, slot0)
	if err != nil {
		t.Fatalf("read slot0: %v", err)
	}
	// The write survived the revert — the behavior the compiler designs around.
	if len(v) == 0 || new(big.Int).SetBytes(v).Sign() == 0 {
		t.Fatal("expected slot0 to persist through the top-level revert (checks-effects rule rationale)")
	}
}

// A function that calls itself (directly or through another internal function)
// inlines its own body, so recursion is unbounded compile-time expansion, not
// a runtime call. It must be rejected by name in the semantic pass.
func TestRecursiveCallGraphIsRejected(t *testing.T) {
	cases := map[string]string{
		"direct": `contract R { uint256 v; function f(uint256 a) internal { f(a - 1); } function g() public { f(9); } }`,
		"mutual": `contract R { uint256 v; function a(uint256 x) internal { b(x); } function b(uint256 x) internal { a(x); } function g() public { a(1); } }`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Compile(src)
			if err == nil {
				t.Fatalf("recursive internal-call graph compiled without error")
			}
			if !strings.Contains(err.Error(), "recursive") {
				t.Fatalf("recursion was not rejected as such, got: %v", err)
			}
		})
	}
}

// A legal chain of DISTINCT internal calls (no cycle) must still compile: the
// cycle check must not reject ordinary inlining.
func TestAcyclicCallChainStillCompiles(t *testing.T) {
	src := `contract A { uint256 v;
		function leaf(uint256 x) internal returns (uint256) { return x + 1; }
		function mid(uint256 x) internal returns (uint256) { return leaf(x) + 1; }
		function top() public { v = mid(1); }
		function get() public view returns (uint256) { return v; } }`
	if _, err := Compile(src); err != nil {
		t.Fatalf("acyclic call chain rejected: %v", err)
	}
}

// A cancelled compile context must stop codegen with the context error, not
// keep emitting bytecode to completion. This is the deadline-becomes-effective
// property the source-verify route relies on.
func TestCompileContextCancellationStopsCodegen(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled before codegen runs
	// A reasonably sized contract that would otherwise compile fine.
	var b strings.Builder
	b.WriteString("contract C { uint256 v;\n")
	for i := 0; i < 50; i++ {
		fmt.Fprintf(&b, "function f%d(uint256 a) public view returns (uint256) { return a + %d; }\n", i, i)
	}
	b.WriteString("}")
	if _, err := CompileContext(ctx, b.String()); err == nil {
		t.Fatal("compile completed despite a cancelled context")
	}
}
