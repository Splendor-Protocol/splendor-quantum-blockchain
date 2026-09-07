package rpc

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/splendor-protocol/splendor-quantum-blockchain/compiler"
	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

const counterSrc = `
contract Counter {
    uint256 count;
    function inc() public { count += 1; }
    function get() public view returns (uint256) { return count; }
}`

const tokenSrc = `
contract Tok {
    mapping(address => uint256) balances;
    function credit(address to, uint256 n) public { balances[to] += n; emit Credit(to, n); }
    function balanceOf(address who) public view returns (uint256) { return balances[who]; }
}`

// verifyFixture installs the compiled bytecode of src at a fresh address and
// returns the state DB and that address.
func verifyFixture(t *testing.T, src string) (*state.StateDB, string) {
	t.Helper()
	sdb, err := state.NewStateDB(t.TempDir())
	if err != nil {
		t.Fatalf("open statedb: %v", err)
	}
	t.Cleanup(func() { sdb.Close() })

	c, err := compiler.Compile(src)
	if err != nil {
		t.Fatalf("compile fixture: %v", err)
	}
	var addr qcrypto.Address
	for i := range addr {
		addr[i] = 0xC0
	}
	h := qcrypto.Hash32(c.Bytecode)
	sdb.SetCode(h, c.Bytecode)
	acc := types.NewAccount(addr)
	acc.IsContract = true
	acc.CodeHash = h
	sdb.SetAccount(acc)
	if _, err := sdb.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return sdb, addr.String()
}

func postVerify(t *testing.T, sdb *state.StateDB, address, source string) (int, verifyResponse) {
	t.Helper()
	body, _ := json.Marshal(verifyRequest{Address: address, Source: source})
	rec := httptest.NewRecorder()
	makeVerifyHandler(sdb)(rec, httptest.NewRequest(http.MethodPost, "/contract/verify", bytes.NewReader(body)))
	var out verifyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response (status %d): %v — body %s", rec.Code, err, rec.Body.String())
	}
	return rec.Code, out
}

// The whole point: recompiling the true source reproduces the deployed
// bytecode exactly, because Compile is deterministic. That equality IS the
// proof, and the ABI comes back so a caller learns how to call what it just
// verified.
func TestVerifyAcceptsTheTrueSource(t *testing.T) {
	sdb, addr := verifyFixture(t, counterSrc)
	code, out := postVerify(t, sdb, addr, counterSrc)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if !out.Verified || out.Kind != "verified" {
		t.Fatalf("response = %+v, want verified", out)
	}
	if out.BytecodeSize == 0 || out.CodeHash == "" {
		t.Fatalf("response = %+v, want the size and code hash reported", out)
	}
	var sigs []string
	for _, f := range out.Functions {
		sigs = append(sigs, f.Signature)
	}
	for _, want := range []string{"inc()", "get()"} {
		if !contains(sigs, want) {
			t.Fatalf("functions = %v, want %s", sigs, want)
		}
	}
}

// Events are part of what a verified contract tells you about itself.
func TestVerifyReportsEvents(t *testing.T) {
	sdb, addr := verifyFixture(t, tokenSrc)
	_, out := postVerify(t, sdb, addr, tokenSrc)
	if !out.Verified {
		t.Fatalf("response = %+v, want verified", out)
	}
	if len(out.Events) != 1 || !strings.HasPrefix(out.Events[0].Signature, "Credit(") {
		t.Fatalf("events = %+v, want the Credit event", out.Events)
	}
	if out.Events[0].Topic0 == "" {
		t.Fatal("event reported with no topic0")
	}
}

// A different source compiles fine but to different bytecode. That is a
// MISMATCH, not a compile error, and the caller must be able to tell the two
// apart without reading the HTTP status.
func TestVerifyRejectsADifferentSource(t *testing.T) {
	sdb, addr := verifyFixture(t, counterSrc)
	code, out := postVerify(t, sdb, addr, tokenSrc)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (the question was well formed)", code)
	}
	if out.Verified || out.Kind != "mismatch" {
		t.Fatalf("response = %+v, want kind=mismatch", out)
	}
	if out.OnchainSize == 0 || out.CompiledSize == 0 || out.OnchainSize == out.CompiledSize {
		t.Fatalf("response = %+v, want both sizes reported and differing", out)
	}
}

// Source that does not compile is its own outcome, carrying the compiler's
// message so the submitter can fix it.
func TestVerifyReportsCompileErrorsDistinctly(t *testing.T) {
	sdb, addr := verifyFixture(t, counterSrc)
	code, out := postVerify(t, sdb, addr, "contract Broken { function f() { x = ; } }")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if out.Verified || out.Kind != "compile_error" {
		t.Fatalf("response = %+v, want kind=compile_error", out)
	}
	if !strings.Contains(out.Reason, "line") {
		t.Fatalf("reason %q does not carry the compiler's message", out.Reason)
	}
}

// A plain account and a native module have no bytecode to match, and saying
// "not verified" about either would be misleading — they are different
// questions with different answers.
func TestVerifyDistinguishesNonBytecodeAddresses(t *testing.T) {
	sdb, err := state.NewStateDB(t.TempDir())
	if err != nil {
		t.Fatalf("open statedb: %v", err)
	}
	defer sdb.Close()

	var plain, native qcrypto.Address
	for i := range plain {
		plain[i] = 0x0A
		native[i] = 0x0B
	}
	sdb.SetAccount(types.NewAccount(plain))
	nativeAcc := types.NewAccount(native)
	nativeAcc.IsContract = true // contract with a zero code hash = native module
	sdb.SetAccount(nativeAcc)
	if _, err := sdb.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	for _, tc := range []struct{ addr, wantKind string }{
		{plain.String(), "not_a_contract"},
		{native.String(), "native_module"},
	} {
		_, out := postVerify(t, sdb, tc.addr, counterSrc)
		if out.Verified || out.Kind != tc.wantKind {
			t.Fatalf("%s: response = %+v, want kind=%s", tc.addr, out, tc.wantKind)
		}
	}
}

// Compiling is CPU this endpoint does on an anonymous caller's say-so, so the
// input is bounded before any of it is spent.
func TestVerifyRefusesOversizeSource(t *testing.T) {
	sdb, addr := verifyFixture(t, counterSrc)
	code, out := postVerify(t, sdb, addr, strings.Repeat("/* pad */\n", maxVerifySourceBytes/10+1))
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an oversize source", code)
	}
	if out.Verified {
		t.Fatalf("response = %+v, want refusal", out)
	}
}

// A malformed address is a bad request, not a verdict about a contract.
func TestVerifyRejectsMalformedAddress(t *testing.T) {
	sdb, _ := verifyFixture(t, counterSrc)
	if code, _ := postVerify(t, sdb, "not-an-address", counterSrc); code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
}

// Hex fields are bare, with no 0x prefix, so a caller that adds its own prefix
// does not render 0x0x…. Locked down because the explorer relies on it.
func TestVerifyHexFieldsAreUnprefixed(t *testing.T) {
	sdb, addr := verifyFixture(t, tokenSrc)
	_, out := postVerify(t, sdb, addr, tokenSrc)
	if strings.HasPrefix(out.CodeHash, "0x") {
		t.Fatalf("code_hash %q is 0x-prefixed", out.CodeHash)
	}
	for _, f := range out.Functions {
		if strings.HasPrefix(f.Selector, "0x") {
			t.Fatalf("selector %q is 0x-prefixed", f.Selector)
		}
	}
	for _, e := range out.Events {
		if strings.HasPrefix(e.Topic0, "0x") {
			t.Fatalf("topic0 %q is 0x-prefixed", e.Topic0)
		}
	}
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}
