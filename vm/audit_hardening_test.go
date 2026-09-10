package vm

import (
	"bytes"
	"math/big"
	"strings"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// mustCapped asserts a SOFT "above the cap" refusal.
func mustCapped(t *testing.T, err error, field string) {
	t.Helper()
	mustContain(t, err, field+" is ")
	mustContain(t, err, "above the cap of")
	if IsHardModuleError(err) {
		t.Fatalf("a string cap is the action's own complaint, must be SOFT: %v", err)
	}
}

// A qrc1155 mint persists its uri per token and a deploy its name and
// uri_prefix; both paid the flat fee whatever their size. 2048 bytes of uri
// is the cap; one more is refused before anything is written.
func TestQRC1155StringCaps(t *testing.T) {
	f := newMTFixture(t)
	ok := strings.Repeat("u", MaxModuleURIBytes)
	over := ok + "u"

	if _, err := f.call(t, f.owner, "mint", f.act(qrc1155Action{To: f.alice.String(), ID: "1", Amount: "1", URI: ok})); err != nil {
		t.Fatalf("a %d-byte uri must mint: %v", MaxModuleURIBytes, err)
	}
	before, _ := f.sdb.PendingStateRoot()
	_, err := f.call(t, f.owner, "mint", f.act(qrc1155Action{To: f.alice.String(), ID: "2", Amount: "1", URI: over}))
	mustCapped(t, err, "uri")
	if after, _ := f.sdb.PendingStateRoot(); after != before {
		t.Fatal("a refused mint wrote state")
	}
	if f.balance(t, f.alice, 2).Sign() != 0 {
		t.Fatal("a refused mint minted")
	}
	_, err = f.call(t, f.owner, "mint_batch", f.act(qrc1155Action{To: f.alice.String(), IDs: []string{"3", "4"}, Amounts: []string{"1", "1"}, URIs: []string{ok, over}}))
	mustCapped(t, err, "uris[1]")

	deploy := func(name, prefix string) error {
		_, err := f.call(t, f.owner, "deploy", qrc1155DeployParams{Name: name, URIPrefix: prefix})
		return err
	}
	if err := deploy(strings.Repeat("n", MaxModuleNameBytes), ok); err != nil {
		t.Fatalf("a deploy at the caps: %v", err)
	}
	mustCapped(t, deploy(strings.Repeat("n", MaxModuleNameBytes+1), ""), "name")
	mustCapped(t, deploy("ok", over), "uri_prefix")
}

// The qrc20 caps are a rule change on an action that already shipped, so
// they are gated under qrc20_v2: a pre-fork replay of a long symbol must
// still apply exactly as it did.
func TestQRC20DeployStringCapsGatedByV2(t *testing.T) {
	sdb := newTestState(t)
	caller := addr(0x11)
	long := qrc20DeployParams{Name: "Long", Symbol: strings.Repeat("S", MaxModuleNameBytes+1), Decimals: 0, TotalSupply: "10"}

	pre := ModuleContext{Caller: caller, Nonce: 1, Height: 1}
	if err := DispatchModuleCall(sdb, pre, moduleData(t, "qrc20", "deploy", long), &types.Receipt{}); err != nil {
		t.Fatalf("pre-fork, a long symbol must keep the legacy behaviour: %v", err)
	}
	early := ModuleContext{Caller: caller, Nonce: 2, Height: 1, Forks: forks.Schedule{forks.QRC20V2: 2}}
	if err := DispatchModuleCall(sdb, early, moduleData(t, "qrc20", "deploy", long), &types.Receipt{}); err != nil {
		t.Fatalf("one block before the fork must keep the legacy behaviour: %v", err)
	}

	post := ModuleContext{Caller: caller, Nonce: 3, Height: 1, Forks: v2Forks()}
	mustCapped(t, DispatchModuleCall(sdb, post, moduleData(t, "qrc20", "deploy", long), &types.Receipt{}), "symbol")
	long.Symbol, long.Name = "OK", strings.Repeat("N", MaxModuleNameBytes+1)
	mustCapped(t, DispatchModuleCall(sdb, post, moduleData(t, "qrc20", "deploy", long), &types.Receipt{}), "name")
	long.Name = strings.Repeat("N", MaxModuleNameBytes)
	dep := &types.Receipt{}
	if err := DispatchModuleCall(sdb, post, moduleData(t, "qrc20", "deploy", long), dep); err != nil {
		t.Fatalf("a deploy at the cap under the fork: %v", err)
	}
	if dep.ContractAddress == nil {
		t.Fatal("deploy set no contract address")
	}
}

// ── L13: event word bounds ──────────────────────────────────────────────────

// An event helper must never be able to take a block down: a value at or
// above 2^256 encodes as its low 32 bytes instead of slicing past the word.
// Values that fit are encoded exactly as before.
func TestEventUint256HelpersNeverPanicAboveTheWord(t *testing.T) {
	two256 := new(big.Int).Lsh(big.NewInt(1), 256)
	max := new(big.Int).Sub(two256, big.NewInt(1))

	var allFF [32]byte
	for i := range allFF {
		allFF[i] = 0xff
	}
	if got := Uint256Topic(max); got != allFF {
		t.Fatalf("2^256-1 topic = %x", got)
	}
	if got := Uint256Topic(two256); got != [32]byte{} {
		t.Fatalf("2^256 topic = %x, want the low word (zero)", got)
	}
	var five [32]byte
	five[31] = 5
	if got := Uint256Topic(new(big.Int).Add(two256, big.NewInt(5))); got != five {
		t.Fatalf("2^256+5 topic = %x, want the low word (5)", got)
	}
	if got := Uint256Topic(new(big.Int).Lsh(big.NewInt(1), 300)); got != [32]byte{} {
		t.Fatalf("2^300 topic = %x, want zero", got)
	}

	l := NewEvent(addr(0x01), "X(uint256,uint256)").
		DataUint256(two256).
		DataUint256(new(big.Int).Add(two256, big.NewInt(5))).
		IndexUint256(new(big.Int).Lsh(big.NewInt(1), 300)).
		Build()
	if !bytes.Equal(l.Data, append(make([]byte, 32), five[:]...)) {
		t.Fatalf("data = %x, want two low words", l.Data)
	}
	if len(l.Topics) != 2 || l.Topics[1] != [32]byte{} {
		t.Fatalf("topics = %x", l.Topics)
	}
}

// The legacy qrc20 actions decoded with parseBig, which admits a supply above
// 2^256 that later reaches the event encoder. Under qrc20_v2 they take the
// strict decoder; pre-fork the loose one stays (documented legacy behaviour),
// and the oversized value now emits a truncated word instead of panicking.
func TestQRC20LegacyAmountsStrictUnderV2LooseBefore(t *testing.T) {
	owner := addr(0x11)
	huge := new(big.Int).Lsh(big.NewInt(1), 300)
	deploy := qrc20DeployParams{Name: "Huge", Symbol: "HUG", Decimals: 0, TotalSupply: huge.String()}

	sdb := newTestState(t)
	err := DispatchModuleCall(sdb, v2Ctx(owner, 1, 1), moduleData(t, "qrc20", "deploy", deploy), &types.Receipt{})
	mustContain(t, err, "total_supply")
	mustContain(t, err, "is too long") // 91 digits: refused on length before the 256-bit compare
	if IsHardModuleError(err) {
		t.Fatalf("an oversized amount is the action's own complaint, must be SOFT: %v", err)
	}
	two256 := new(big.Int).Lsh(big.NewInt(1), 256)
	err = DispatchModuleCall(sdb, v2Ctx(owner, 1, 1), moduleData(t, "qrc20", "deploy", qrc20DeployParams{
		Name: "Edge", Symbol: "EDG", TotalSupply: two256.String(),
	}), &types.Receipt{})
	mustContain(t, err, "does not fit in 256 bits")

	// Pre-fork (no schedule): the deploy applies exactly as it always did,
	// and a transfer of that balance logs the low word rather than panicking.
	legacy := func(nonce uint64) ModuleContext {
		return ModuleContext{Caller: owner, Nonce: nonce, Height: 1, Time: 1}
	}
	dep := &types.Receipt{}
	if err := DispatchModuleCall(sdb, legacy(1), moduleData(t, "qrc20", "deploy", deploy), dep); err != nil {
		t.Fatalf("pre-fork deploy of a 2^300 supply must apply as before: %v", err)
	}
	token := *dep.ContractAddress
	rc := &types.Receipt{}
	if err := DispatchModuleCall(sdb, legacy(2), moduleData(t, "qrc20", "transfer", qrc20TokenAction{
		Token: token.String(), To: addr(0x22).String(), Amount: huge.String(),
	}), rc); err != nil {
		t.Fatalf("pre-fork transfer of the 2^300 balance: %v", err)
	}
	if len(rc.Logs) != 1 || !bytes.Equal(rc.Logs[0].Data, make([]byte, 32)) {
		t.Fatalf("logs = %+v, want one Transfer carrying the (zero) low word", rc.Logs)
	}
	if bal, _ := NewQRC20Token(sdb, token).BalanceOf(addr(0x22)); bal.Cmp(huge) != 0 {
		t.Fatalf("balance = %s, want 2^300", bal)
	}

	// Under v2 every legacy amount goes through the strict decoder.
	for _, tc := range []struct{ action, field string }{
		{"mint", "amount"}, {"transfer", "amount"}, {"approve", "amount"}, {"burn", "amount"},
	} {
		err := DispatchModuleCall(sdb, v2Ctx(owner, 3, 1), moduleData(t, "qrc20", tc.action, qrc20TokenAction{
			Token: token.String(), To: addr(0x22).String(), Spender: addr(0x22).String(), Amount: two256.String(),
		}), &types.Receipt{})
		mustContain(t, err, "qrc20 "+tc.action+": "+tc.field)
		mustContain(t, err, "does not fit in 256 bits")
	}
}

// ── L14: ownership targets ──────────────────────────────────────────────────

// An ownership transfer to the contract itself (for a QRC-1155 the
// module-managed marker, freezing every holder), the router, or any deployed
// contract can never be undone by the target, so it is refused; a normal
// address still takes over.
func TestTransferOwnershipRefusesUnownableTargets(t *testing.T) {
	f := newMTFixture(t)
	other := &types.Receipt{}
	if err := DispatchModuleCall(f.sdb, mtCtx(f.owner, 99), moduleData(t, "qrc1155", "deploy", qrc1155DeployParams{Name: "Other"}), other); err != nil {
		t.Fatalf("deploy other: %v", err)
	}
	for _, tc := range []struct {
		name string
		to   qcrypto.Address
		want string
	}{
		{"self", f.collection, "contract itself"},
		{"router", ModuleRouterAddress, "module router"},
		{"contract", *other.ContractAddress, "is a contract"},
	} {
		_, err := f.call(t, f.owner, "transfer_ownership", f.act(qrc1155Action{NewOwner: tc.to.String()}))
		mustContain(t, err, tc.want)
		if IsHardModuleError(err) {
			t.Fatalf("%s: must be SOFT: %v", tc.name, err)
		}
		if got, _ := f.tok.Owner(); got != f.owner {
			t.Fatalf("%s: refused transfer changed the owner to %s", tc.name, got)
		}
		if managed, _ := f.tok.IsModuleManaged(); managed {
			t.Fatalf("%s: collection became module-managed", tc.name)
		}
	}
	if _, err := f.call(t, f.owner, "transfer_ownership", f.act(qrc1155Action{NewOwner: f.bob.String()})); err != nil {
		t.Fatalf("transfer to a normal address: %v", err)
	}
	if got, _ := f.tok.Owner(); got != f.bob {
		t.Fatalf("owner = %s, want bob", got)
	}

	// qrc20 (v2 surface) carries the same rule.
	r := newRouterFixture(t)
	otherTok := &types.Receipt{}
	if err := DispatchModuleCall(r.sdb, v2Ctx(r.holder, 9, 1), moduleData(t, "qrc20", "deploy", qrc20DeployParams{
		Name: "Other", Symbol: "OTH", TotalSupply: "1",
	}), otherTok); err != nil {
		t.Fatalf("deploy other token: %v", err)
	}
	for _, to := range []qcrypto.Address{r.token, ModuleRouterAddress, *otherTok.ContractAddress} {
		_, err := r.call(t, v2Ctx(r.holder, 5, 1), "transfer_ownership", qrc20TokenAction{Token: r.token.String(), NewOwner: to.String()})
		if err == nil {
			t.Fatalf("qrc20 transfer_ownership to %s accepted", to)
		}
		if got, _ := r.tok.Owner(); got != r.holder {
			t.Fatalf("qrc20 owner changed to %s", got)
		}
	}
	if _, err := r.call(t, v2Ctx(r.holder, 6, 1), "transfer_ownership", qrc20TokenAction{Token: r.token.String(), NewOwner: addr(0x77).String()}); err != nil {
		t.Fatalf("qrc20 transfer to a normal address: %v", err)
	}
}
