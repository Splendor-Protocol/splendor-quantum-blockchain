package vm

import (
	"encoding/hex"
	"math/big"
	"strings"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// brAttestor is a bridge operator with a real ML-DSA key, so it can both be
// registered as an operator and sign claim attestations.
type brAttestor struct {
	addr qcrypto.Address
	pub  *qcrypto.PublicKey
	priv *qcrypto.PrivateKey
}

func newBrAttestor(t *testing.T) *brAttestor {
	t.Helper()
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	return &brAttestor{addr: qcrypto.DeriveAddress(pub), pub: pub, priv: priv}
}

// ── Fixture ─────────────────────────────────────────────────────────────────

const (
	brEvmContract = "0x00000000000000000000000000000000000dea11"
	brEvmTx       = "0xabcd00000000000000000000000000000000000000000000000000000000beef"
	brSplvMax     = "1000000000000000000000" // 1000 SPLV
	brMaxClaim    = "100000000000000000000"  // 100 per claim
	brPoolFunding = int64(500_000)
)

// brFixture is one deployed bridge (threshold 1) with a funded escrow pool, one
// operator/attestor and a user recipient.
type brFixture struct {
	t        *testing.T
	sdb      *state.StateDB
	owner    qcrypto.Address
	op       *brAttestor // the registered operator + attestor
	operator qcrypto.Address
	user     qcrypto.Address
	b        *Bridge
	splv     *QRC20Token
}

func brForks() forks.Schedule {
	return forks.Schedule{forks.BridgeV1: 1, forks.ModuleRevertV1: 1}
}

func newBrFixture(t *testing.T) *brFixture {
	t.Helper()
	sdb := newTestState(t)
	op := newBrAttestor(t)
	f := &brFixture{t: t, sdb: sdb, owner: addr(0x21), op: op, operator: op.addr, user: addr(0x33)}
	rc := &types.Receipt{}
	if err := DispatchModuleCall(sdb, f.ctx(f.owner), moduleData(t, "bridge", "deploy", bridgeDeployParams{
		EvmChainID: 2691, EvmContract: brEvmContract,
		SplvName: "Vested Splendor", SplvSymbol: "SPLV",
		SplvMaxSupply: brSplvMax, MaxClaimAmount: brMaxClaim, Threshold: 1,
	}), rc); err != nil {
		t.Fatalf("deploy bridge: %v", err)
	}
	if rc.ContractAddress == nil {
		t.Fatal("deploy set no contract address")
	}
	f.b = NewBridge(sdb, *rc.ContractAddress)
	splv, err := f.b.SplvToken()
	if err != nil {
		t.Fatalf("splv token: %v", err)
	}
	f.splv = NewQRC20Token(sdb, splv)
	if err := f.b.SetOperator(f.owner, f.operator, true, nil); err != nil {
		t.Fatalf("set_operator: %v", err)
	}
	f.fundPool(t, brPoolFunding)
	return f
}

// attest signs a claim digest with a's key. It reads the (possibly overridden)
// claim fields; on a malformed field it returns a placeholder attestation so
// the field-refusal tests still fail at the earlier check (which precedes the
// attestation check in Claim).
func (f *brFixture) attest(t *testing.T, a *brAttestor, p map[string]any) bridgeAttestation {
	t.Helper()
	placeholder := bridgeAttestation{PubKey: a.pub.Bytes()}
	bridge, err := qcrypto.AddressFromString(p["bridge"].(string))
	if err != nil {
		return placeholder
	}
	id, _ := p["id"].(uint64)
	assetWord, err := bridgeAssetWord(p["asset"].(string))
	if err != nil {
		return placeholder
	}
	amount, ok := new(big.Int).SetString(p["amount"].(string), 10)
	if !ok {
		return placeholder
	}
	recipient, err := qcrypto.AddressFromString(p["recipient"].(string))
	if err != nil {
		return placeholder
	}
	var evmTx [32]byte
	if b, err := hex.DecodeString(strings.TrimPrefix(p["evm_tx"].(string), "0x")); err == nil && len(b) == 32 {
		copy(evmTx[:], b)
	}
	digest, err := types.BridgeClaimDigest(types.ChainID, bridge, id, assetWord, amount, recipient, evmTx)
	if err != nil {
		return placeholder
	}
	sig, err := qcrypto.Sign(a.priv, digest[:], qcrypto.CtxBridge)
	if err != nil {
		t.Fatalf("sign attestation: %v", err)
	}
	return bridgeAttestation{PubKey: a.pub.Bytes(), Signature: sig}
}

func (f *brFixture) ctx(caller qcrypto.Address) ModuleContext {
	return ModuleContext{Caller: caller, Nonce: 1, Height: 1, Time: 1_000_000, Forks: brForks()}
}

// fundPool credits the escrow the way ops does: value arriving at the bridge
// address (a plain TxTransfer in production; a direct credit in a bare state).
func (f *brFixture) fundPool(t *testing.T, amount int64) {
	t.Helper()
	acc, err := f.sdb.GetAccount(f.b.Address())
	if err != nil {
		t.Fatalf("pool account: %v", err)
	}
	acc.AddBalance(big.NewInt(amount))
	f.sdb.SetAccount(acc)
}

// call runs a bridge write through DispatchModuleCall.
func (f *brFixture) call(t *testing.T, caller qcrypto.Address, action string, params any) (*types.Receipt, error) {
	t.Helper()
	rc := &types.Receipt{}
	return rc, DispatchModuleCall(f.sdb, f.ctx(caller), moduleData(t, "bridge", action, params), rc)
}

// claimFields builds the claim fields with sane defaults, overridable per
// field — WITHOUT attestations, so a test can attest over them explicitly.
func (f *brFixture) claimFields(over map[string]any) map[string]any {
	p := map[string]any{
		"bridge": f.b.Address().String(), "id": uint64(1), "asset": BridgeAssetSPLD,
		"amount": "1000", "recipient": f.user.String(), "evm_tx": brEvmTx,
	}
	for k, v := range over {
		p[k] = v
	}
	return p
}

// claimParams is claimFields plus a valid attestation from the fixture operator
// over the resulting fields (unless attestations are overridden explicitly).
func (f *brFixture) claimParams(over map[string]any) map[string]any {
	p := f.claimFields(over)
	if _, set := over["attestations"]; !set {
		p["attestations"] = []bridgeAttestation{f.attest(f.t, f.op, p)}
	} else {
		p["attestations"] = over["attestations"]
	}
	return p
}

// read runs a bridge read through DispatchModuleReadCall.
func (f *brFixture) read(t *testing.T, action string, params any) (map[string]interface{}, error) {
	t.Helper()
	v, err := DispatchModuleReadCall(f.sdb, ReadContext{}, moduleData(t, "bridge", action, params))
	if err != nil {
		return nil, err
	}
	return v.(map[string]interface{}), nil
}

func (f *brFixture) nativeBalance(t *testing.T, a qcrypto.Address) int64 {
	t.Helper()
	v, err := f.sdb.GetBalance(a)
	if err != nil {
		t.Fatalf("balance %s: %v", a, err)
	}
	return v.Int64()
}

func mustSoftBridge(t *testing.T, err error, want string) {
	t.Helper()
	mustContain(t, err, want)
	if IsHardModuleError(err) {
		t.Fatalf("bridge action errors must be SOFT: %v", err)
	}
	if !strings.HasPrefix(err.Error(), "bridge ") {
		t.Fatalf("error lacks the \"bridge <action>: \" prefix: %v", err)
	}
}

// ── Deploy ──────────────────────────────────────────────────────────────────

func TestBridgeDeploy(t *testing.T) {
	f := newBrFixture(t)
	if want := BridgeAddress(f.owner, 1, 1); f.b.Address() != want {
		t.Fatalf("bridge at %s, want %s", f.b.Address(), want)
	}
	if acc, _ := f.sdb.GetAccount(f.b.Address()); !acc.IsContract {
		t.Fatal("bridge account not marked is_contract")
	}
	owner, _ := f.b.Owner()
	if owner != f.owner {
		t.Fatalf("owner = %s, want %s", owner, f.owner)
	}
	chainID, _ := f.b.EvmChainID()
	if chainID != 2691 {
		t.Fatalf("evm_chain_id = %d, want 2691", chainID)
	}

	// The SPLV token is module-managed: owned by the bridge instance, zero
	// supply, capped, marked as a contract at its derived address.
	splvAddr, _ := f.b.SplvToken()
	if want := BridgeSplvAddress(f.b.Address()); splvAddr != want {
		t.Fatalf("splv at %s, want %s", splvAddr, want)
	}
	if acc, _ := f.sdb.GetAccount(splvAddr); !acc.IsContract {
		t.Fatal("splv account not marked is_contract")
	}
	tokOwner, _ := f.splv.Owner()
	if tokOwner != f.b.Address() {
		t.Fatalf("splv owner = %s, want the bridge %s", tokOwner, f.b.Address())
	}
	supply, _ := f.splv.TotalSupply()
	if supply.Sign() != 0 {
		t.Fatalf("splv initial supply = %s, want 0", supply)
	}
	maxSupply, _ := f.splv.MaxSupply()
	if maxSupply.String() != brSplvMax {
		t.Fatalf("splv max supply = %s, want %s", maxSupply, brSplvMax)
	}
	dec, _ := f.splv.Decimals()
	if dec != 18 {
		t.Fatalf("splv decimals = %d, want 18", dec)
	}

	// No external key can ever mint: requireOwner wants the instance address.
	err := f.splv.Mint(f.owner, f.user, big.NewInt(1))
	mustContain(t, err, "not owner")
}

func TestBridgeDeployRefusals(t *testing.T) {
	f := newBrFixture(t)
	base := bridgeDeployParams{
		EvmChainID: 2691, EvmContract: brEvmContract,
		SplvName: "Vested Splendor", SplvSymbol: "SPLV",
		SplvMaxSupply: brSplvMax, MaxClaimAmount: brMaxClaim, Threshold: 1,
	}
	cases := []struct {
		name string
		mut  func(*bridgeDeployParams)
		want string
	}{
		{"zero chain id", func(p *bridgeDeployParams) { p.EvmChainID = 0 }, "evm_chain_id"},
		{"bad contract hex", func(p *bridgeDeployParams) { p.EvmContract = "0xNOPE" }, "evm_contract"},
		{"uppercase contract hex", func(p *bridgeDeployParams) { p.EvmContract = strings.ToUpper(brEvmContract[2:]) }, "evm_contract"},
		{"short contract", func(p *bridgeDeployParams) { p.EvmContract = "0xdead" }, "evm_contract"},
		{"zero splv max", func(p *bridgeDeployParams) { p.SplvMaxSupply = "0" }, "splv_max_supply"},
		{"bad splv max", func(p *bridgeDeployParams) { p.SplvMaxSupply = "01" }, "splv_max_supply"},
		{"zero max claim", func(p *bridgeDeployParams) { p.MaxClaimAmount = "0" }, "max_claim_amount"},
		{"empty max claim", func(p *bridgeDeployParams) { p.MaxClaimAmount = "" }, "max_claim_amount"},
		{"zero threshold", func(p *bridgeDeployParams) { p.Threshold = 0 }, "threshold"},
		{"threshold over cap", func(p *bridgeDeployParams) { p.Threshold = MaxBridgeOperators + 1 }, "threshold"},
	}
	for _, tc := range cases {
		p := base
		tc.mut(&p)
		if _, err := f.call(t, f.owner, "deploy", p); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want mention of %q", tc.name, err, tc.want)
		}
	}

	// Name caps are enforced by the router.
	long := strings.Repeat("x", MaxModuleNameBytes+1)
	p := base
	p.SplvName = long
	if _, err := f.call(t, f.owner, "deploy", p); err == nil || !strings.Contains(err.Error(), "splv_name") {
		t.Errorf("long name: err = %v", err)
	}
	p = base
	p.SplvSymbol = long
	if _, err := f.call(t, f.owner, "deploy", p); err == nil || !strings.Contains(err.Error(), "splv_symbol") {
		t.Errorf("long symbol: err = %v", err)
	}

	// Same deployer, nonce and height: the instance already exists.
	if _, err := f.call(t, f.owner, "deploy", base); err == nil || !strings.Contains(err.Error(), "already deployed") {
		t.Errorf("redeploy: err = %v, want already deployed", err)
	}
}

func TestBridgeAddressesVaryByHeight(t *testing.T) {
	d := addr(0x21)
	a1 := BridgeAddress(d, 1, 1)
	a2 := BridgeAddress(d, 1, 2)
	if a1 == a2 {
		t.Fatal("bridge address must fold in the height (cross-tier collision)")
	}
	if BridgeSplvAddress(a1) == BridgeSplvAddress(a2) {
		t.Fatal("splv address must vary with the instance")
	}
}

// Every bridge write is HARD fork-inactive with no schedule, and reachable
// (SOFT or success) once bridge_v1 is active.
func TestBridgeForkGate(t *testing.T) {
	f := newBrFixture(t)
	acts := KnownModuleActions["bridge"]
	if len(acts.Writes) != 7 || len(acts.Reads) != 2 {
		t.Fatalf("bridge lists %d writes and %d reads, want 7 and 2", len(acts.Writes), len(acts.Reads))
	}
	for _, action := range sortedKeys(acts.Writes) {
		if acts.Writes[action] != forks.BridgeV1 {
			t.Errorf("%s is gated by %q, want bridge_v1", action, acts.Writes[action])
		}
		ctx := f.ctx(f.owner)
		ctx.Forks = nil
		rc := &types.Receipt{}
		err := DispatchModuleCall(f.sdb, ctx, moduleData(t, "bridge", action, f.claimParams(nil)), rc)
		if err == nil || !IsHardModuleError(err) {
			t.Errorf("%s without the fork: err = %v, want HARD fork-inactive", action, err)
		}
	}
}

// ── Operators & owner controls ──────────────────────────────────────────────

func TestBridgeOperatorControls(t *testing.T) {
	f := newBrFixture(t)
	base := map[string]any{"bridge": f.b.Address().String()}

	// Owner-only.
	p := map[string]any{"bridge": f.b.Address().String(), "operator": addr(0x44).String(), "active": true}
	_, err := f.call(t, f.user, "set_operator", p)
	mustSoftBridge(t, err, "not the owner")

	// Explicit active flag.
	_, err = f.call(t, f.owner, "set_operator", map[string]any{"bridge": f.b.Address().String(), "operator": addr(0x44).String()})
	mustContain(t, err, "active is required")

	// Grant, revoke, re-grant.
	if _, err := f.call(t, f.owner, "set_operator", p); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if ok, _ := f.b.IsOperator(addr(0x44)); !ok {
		t.Fatal("grant did not take")
	}
	p["active"] = false
	if _, err := f.call(t, f.owner, "set_operator", p); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if ok, _ := f.b.IsOperator(addr(0x44)); ok {
		t.Fatal("revoke did not take")
	}

	// The list is bounded; the fixture operator and 0x44 hold two slots.
	for i := 0; i < MaxBridgeOperators-2; i++ {
		q := map[string]any{"bridge": f.b.Address().String(), "operator": addr(byte(0x50 + i)).String(), "active": true}
		if _, err := f.call(t, f.owner, "set_operator", q); err != nil {
			t.Fatalf("grant %d: %v", i, err)
		}
	}
	q := map[string]any{"bridge": f.b.Address().String(), "operator": addr(0xef).String(), "active": true}
	_, err = f.call(t, f.owner, "set_operator", q)
	mustSoftBridge(t, err, "operator list is full")

	// Pause and unpause are owner-only and flip the flag.
	_, err = f.call(t, f.operator, "pause", base)
	mustSoftBridge(t, err, "not the owner")
	if _, err := f.call(t, f.owner, "pause", base); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if paused, _ := f.b.IsPaused(); !paused {
		t.Fatal("pause did not take")
	}
	if _, err := f.call(t, f.owner, "unpause", base); err != nil {
		t.Fatalf("unpause: %v", err)
	}

	// Ownership refuses contracts and module addresses; accepts a fresh key.
	_, err = f.call(t, f.owner, "transfer_ownership", map[string]any{"bridge": f.b.Address().String(), "new_owner": f.b.Address().String()})
	mustSoftBridge(t, err, "module address")
	splvAddr, _ := f.b.SplvToken()
	_, err = f.call(t, f.owner, "transfer_ownership", map[string]any{"bridge": f.b.Address().String(), "new_owner": splvAddr.String()})
	mustSoftBridge(t, err, "is a contract")
	if _, err := f.call(t, f.owner, "transfer_ownership", map[string]any{"bridge": f.b.Address().String(), "new_owner": addr(0x99).String()}); err != nil {
		t.Fatalf("transfer_ownership: %v", err)
	}
	owner, _ := f.b.Owner()
	if owner != addr(0x99) {
		t.Fatalf("owner = %s, want %s", owner, addr(0x99))
	}
}

// ── Claim ───────────────────────────────────────────────────────────────────

func TestBridgeClaimSpld(t *testing.T) {
	f := newBrFixture(t)
	poolBefore := f.nativeBalance(t, f.b.Address())

	rc, err := f.call(t, f.operator, "claim", f.claimParams(nil))
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if got := f.nativeBalance(t, f.user); got != 1000 {
		t.Fatalf("recipient balance = %d, want 1000", got)
	}
	if got := f.nativeBalance(t, f.b.Address()); got != poolBefore-1000 {
		t.Fatalf("pool = %d, want %d", got, poolBefore-1000)
	}
	// The pool released exactly what the recipient gained: conservation.
	total, _ := f.b.ClaimedTotal(BridgeAssetSPLD)
	if total.Int64() != 1000 {
		t.Fatalf("claimed_total_spld = %s, want 1000", total)
	}
	claimed, height, _ := f.b.ClaimedAt(1)
	if !claimed || height != 1 {
		t.Fatalf("claimed=%v height=%d, want true/1", claimed, height)
	}
	if len(rc.Logs) != 1 || rc.Logs[0].Topics[0] != TopicBridgeClaimed {
		t.Fatalf("want one BridgeClaimed log, got %d", len(rc.Logs))
	}

	// Idempotent: the same id is refused however it is retried.
	_, err = f.call(t, f.operator, "claim", f.claimParams(nil))
	mustSoftBridge(t, err, "already claimed")
	_, err = f.call(t, f.operator, "claim", f.claimParams(map[string]any{"asset": BridgeAssetSPLV}))
	mustSoftBridge(t, err, "already claimed")
}

func TestBridgeClaimSplv(t *testing.T) {
	f := newBrFixture(t)
	rc, err := f.call(t, f.operator, "claim", f.claimParams(map[string]any{"asset": BridgeAssetSPLV, "amount": "2000"}))
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	bal, _ := f.splv.BalanceOf(f.user)
	if bal.Int64() != 2000 {
		t.Fatalf("splv balance = %s, want 2000", bal)
	}
	supply, _ := f.splv.TotalSupply()
	if supply.Int64() != 2000 {
		t.Fatalf("splv supply = %s, want 2000", supply)
	}
	total, _ := f.b.ClaimedTotal(BridgeAssetSPLV)
	if total.Int64() != 2000 {
		t.Fatalf("claimed_total_splv = %s, want 2000", total)
	}
	// Mint (on the token) then BridgeClaimed (on the bridge).
	if len(rc.Logs) != 2 {
		t.Fatalf("want 2 logs, got %d", len(rc.Logs))
	}
	splvAddr, _ := f.b.SplvToken()
	if rc.Logs[0].Address != splvAddr || rc.Logs[1].Topics[0] != TopicBridgeClaimed {
		t.Fatal("want the qrc20 Mint at the token then BridgeClaimed at the bridge")
	}

	// The cap holds: one claim may never mint past splv_max_supply, and a
	// refused mint leaves no residue.
	_, err = f.call(t, f.operator, "claim", f.claimParams(map[string]any{
		"id": uint64(2), "asset": BridgeAssetSPLV, "amount": brMaxClaim,
	}))
	if err != nil {
		t.Fatalf("claim 2: %v", err)
	}
	supplyBefore, _ := f.splv.TotalSupply()
	for id := uint64(3); ; id++ {
		_, err = f.call(t, f.operator, "claim", f.claimParams(map[string]any{
			"id": id, "asset": BridgeAssetSPLV, "amount": brMaxClaim,
		}))
		if err != nil {
			mustSoftBridge(t, err, "max supply")
			// The failed claim burned nothing and stays claimable.
			claimed, _, _ := f.b.ClaimedAt(id)
			if claimed {
				t.Fatal("failed claim marked claimed")
			}
			break
		}
		if id > 20 {
			t.Fatal("cap never hit")
		}
	}
	_ = supplyBefore
	supplyAfter, _ := f.splv.TotalSupply()
	max, _ := f.splv.MaxSupply()
	if supplyAfter.Cmp(max) > 0 {
		t.Fatalf("supply %s exceeds cap %s", supplyAfter, max)
	}
}

func TestBridgeClaimRefusals(t *testing.T) {
	f := newBrFixture(t)

	// Auth: a stranger cannot claim; the owner can.
	_, err := f.call(t, f.user, "claim", f.claimParams(nil))
	mustSoftBridge(t, err, "neither the owner nor an operator")
	if _, err := f.call(t, f.owner, "claim", f.claimParams(map[string]any{"id": uint64(90)})); err != nil {
		t.Fatalf("owner claim: %v", err)
	}

	// Paused blocks claims until unpause.
	base := map[string]any{"bridge": f.b.Address().String()}
	if _, err := f.call(t, f.owner, "pause", base); err != nil {
		t.Fatalf("pause: %v", err)
	}
	_, err = f.call(t, f.operator, "claim", f.claimParams(nil))
	mustSoftBridge(t, err, "paused")
	if _, err := f.call(t, f.owner, "unpause", base); err != nil {
		t.Fatalf("unpause: %v", err)
	}

	cases := []struct {
		name string
		over map[string]any
		want string
	}{
		{"zero id", map[string]any{"id": uint64(0)}, "id must be positive"},
		{"bad asset", map[string]any{"asset": "gold"}, "unknown asset"},
		{"zero amount", map[string]any{"amount": "0"}, "amount must be positive"},
		{"bad amount", map[string]any{"amount": "12.5"}, "amount"},
		{"over cap", map[string]any{"amount": "100000000000000000001"}, "exceeds max_claim_amount"},
		{"bad recipient", map[string]any{"recipient": "nope"}, "recipient"},
		{"zero recipient", map[string]any{"recipient": qcrypto.Address{}.String()}, "zero address"},
		{"bridge recipient", map[string]any{"recipient": f.b.Address().String()}, "module address"},
		{"router recipient", map[string]any{"recipient": ModuleRouterAddress.String()}, "module address"},
		{"bad evm tx", map[string]any{"evm_tx": "0x1234"}, "evm_tx"},
	}
	for _, tc := range cases {
		_, err := f.call(t, f.operator, "claim", f.claimParams(tc.over))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want mention of %q", tc.name, err, tc.want)
		}
	}

	// A contract recipient could never spend the credit.
	splvAddr, _ := f.b.SplvToken()
	_, err = f.call(t, f.operator, "claim", f.claimParams(map[string]any{"recipient": splvAddr.String()}))
	mustSoftBridge(t, err, "is a contract")

	// An unfunded pool refuses the claim, leaves no residue, and the id can
	// be retried once the pool is funded.
	big1 := "400000000000" // far above the fixture pool
	userBefore := f.nativeBalance(t, f.user)
	_, err = f.call(t, f.operator, "claim", f.claimParams(map[string]any{"id": uint64(7), "amount": big1}))
	mustSoftBridge(t, err, "escrow pool holds")
	if claimed, _, _ := f.b.ClaimedAt(7); claimed {
		t.Fatal("refused claim marked claimed")
	}
	if got := f.nativeBalance(t, f.user); got != userBefore {
		t.Fatalf("refused claim paid %d", got-userBefore)
	}
	f.fundPool(t, 400_000_000_000)
	if _, err := f.call(t, f.operator, "claim", f.claimParams(map[string]any{"id": uint64(7), "amount": big1})); err != nil {
		t.Fatalf("retry after funding: %v", err)
	}

	// A claim against an undeployed instance is the action's own complaint.
	_, err = f.call(t, f.operator, "claim", f.claimParams(map[string]any{"bridge": addr(0xaa).String()}))
	mustSoftBridge(t, err, "no bridge is deployed")
}

// The escrow model conserves native supply: a claim moves value, mints none.
func TestBridgeClaimConservesNativeSupply(t *testing.T) {
	f := newBrFixture(t)
	sum := func() int64 {
		return f.nativeBalance(t, f.b.Address()) + f.nativeBalance(t, f.user) + f.nativeBalance(t, f.owner) + f.nativeBalance(t, f.operator)
	}
	before := sum()
	if _, err := f.call(t, f.operator, "claim", f.claimParams(nil)); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if after := sum(); after != before {
		t.Fatalf("native supply moved: %d -> %d", before, after)
	}
}

// ── Attestations (M-of-N) ───────────────────────────────────────────────────

// A claim is authorised by the attestations, not the submitter: no attestation,
// a non-operator attestation, or a bad signature is refused — even from a valid
// operator submitter — so a lone operator key cannot move funds.
func TestBridgeClaimRequiresAttestations(t *testing.T) {
	f := newBrFixture(t)

	// No attestations at threshold 1.
	_, err := f.call(t, f.operator, "claim", f.claimParams(map[string]any{"attestations": []bridgeAttestation{}}))
	mustSoftBridge(t, err, "need 1")

	// An attestation from a key that is not a registered operator.
	stranger := newBrAttestor(t)
	fields := f.claimFields(nil)
	_, err = f.call(t, f.operator, "claim", f.claimParams(map[string]any{
		"attestations": []bridgeAttestation{f.attest(t, stranger, fields)},
	}))
	mustSoftBridge(t, err, "not an active operator")

	// A well-formed attestation from the operator but with a corrupted
	// signature (signed over a DIFFERENT burn id).
	wrong := f.attest(t, f.op, f.claimFields(map[string]any{"id": uint64(999)}))
	_, err = f.call(t, f.operator, "claim", f.claimParams(map[string]any{
		"attestations": []bridgeAttestation{wrong},
	}))
	mustSoftBridge(t, err, "invalid signature")

	// A revoked operator's attestation no longer counts.
	if err := f.b.SetOperator(f.owner, f.operator, false, nil); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	_, err = f.call(t, f.owner, "claim", f.claimParams(nil))
	mustSoftBridge(t, err, "not an active operator")
}

// With threshold 2, a claim needs two DISTINCT operator attestations; one, or
// two from the same operator, is refused; two distinct credits.
func TestBridgeThresholdTwoOfN(t *testing.T) {
	f := newBrFixture(t)
	op2 := newBrAttestor(t)
	if err := f.b.SetOperator(f.owner, op2.addr, true, nil); err != nil {
		t.Fatalf("add op2: %v", err)
	}

	// set_threshold is owner-only.
	thParams := map[string]any{"bridge": f.b.Address().String(), "threshold": uint64(2)}
	_, err := f.call(t, f.operator, "set_threshold", thParams)
	mustSoftBridge(t, err, "not the owner")
	if _, err := f.call(t, f.owner, "set_threshold", thParams); err != nil {
		t.Fatalf("set_threshold: %v", err)
	}
	if th, _ := f.b.Threshold(); th != 2 {
		t.Fatalf("threshold = %d, want 2", th)
	}

	fields := f.claimFields(nil)

	// One attestation: below threshold.
	_, err = f.call(t, f.operator, "claim", f.claimParams(map[string]any{
		"attestations": []bridgeAttestation{f.attest(t, f.op, fields)},
	}))
	mustSoftBridge(t, err, "need 2")

	// Two attestations from the SAME operator: counts once, still below.
	_, err = f.call(t, f.operator, "claim", f.claimParams(map[string]any{
		"attestations": []bridgeAttestation{f.attest(t, f.op, fields), f.attest(t, f.op, fields)},
	}))
	mustSoftBridge(t, err, "need 2")

	// Two DISTINCT operators: credited.
	if _, err := f.call(t, f.operator, "claim", f.claimParams(map[string]any{
		"attestations": []bridgeAttestation{f.attest(t, f.op, fields), f.attest(t, op2, fields)},
	})); err != nil {
		t.Fatalf("2-of-N claim: %v", err)
	}
	if got := f.nativeBalance(t, f.user); got != 1000 {
		t.Fatalf("recipient balance = %d, want 1000", got)
	}
}

// ── Reads ───────────────────────────────────────────────────────────────────

func TestBridgeReads(t *testing.T) {
	f := newBrFixture(t)

	info, err := f.read(t, "bridge_info", map[string]any{"bridge": f.b.Address().String()})
	if err != nil {
		t.Fatalf("bridge_info: %v", err)
	}
	bi := info["info"].(*BridgeInfo)
	if bi.EvmChainID != 2691 || bi.EvmContract != brEvmContract || bi.Paused {
		t.Fatalf("info = %+v", bi)
	}
	if bi.PoolBalance != "500000" || len(bi.Operators) != 1 || bi.Operators[0] != f.operator.String() {
		t.Fatalf("info = %+v", bi)
	}
	if bi.MaxClaimAmount != brMaxClaim || bi.SplvToken != BridgeSplvAddress(f.b.Address()).String() {
		t.Fatalf("info = %+v", bi)
	}
	if bi.Threshold != 1 {
		t.Fatalf("threshold = %d, want 1", bi.Threshold)
	}

	// claim_status flips with the claim.
	st, err := f.read(t, "claim_status", map[string]any{"bridge": f.b.Address().String(), "id": uint64(1)})
	if err != nil {
		t.Fatalf("claim_status: %v", err)
	}
	if st["claimed"] != false {
		t.Fatalf("status = %+v", st)
	}
	if _, err := f.call(t, f.operator, "claim", f.claimParams(nil)); err != nil {
		t.Fatalf("claim: %v", err)
	}
	st, err = f.read(t, "claim_status", map[string]any{"bridge": f.b.Address().String(), "id": uint64(1)})
	if err != nil {
		t.Fatalf("claim_status: %v", err)
	}
	if st["claimed"] != true || st["height"] != uint64(1) {
		t.Fatalf("status = %+v", st)
	}

	// Reads refuse an undeployed instance, a zero id, and every write action.
	if _, err := f.read(t, "bridge_info", map[string]any{"bridge": addr(0xaa).String()}); err == nil {
		t.Fatal("bridge_info on nothing must fail")
	}
	if _, err := f.read(t, "claim_status", map[string]any{"bridge": f.b.Address().String(), "id": uint64(0)}); err == nil {
		t.Fatal("claim_status id 0 must fail")
	}
	if _, err := f.read(t, "claim", f.claimParams(nil)); err == nil || !strings.Contains(err.Error(), "state-changing") {
		t.Fatalf("read of a write: %v", err)
	}
}
