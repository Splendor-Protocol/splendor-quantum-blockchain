package vm

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"math/big"
	"strings"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// vfActor holds both post-quantum identities one SQVL party needs. Keys are
// derived from a seed byte so the fixture is fast and reproducible.
type vfActor struct {
	addr    qcrypto.Address
	pub     *qcrypto.PublicKey
	priv    *qcrypto.PrivateKey
	slhPub  *qcrypto.SLHPublicKey
	slhPriv *qcrypto.SLHPrivateKey
}

func newVfActor(t *testing.T, seedByte byte) *vfActor {
	t.Helper()
	var seed [qcrypto.SeedSize]byte
	for i := range seed {
		seed[i] = seedByte
	}
	pub, priv := qcrypto.KeypairFromSeed(seed)
	slhPub, slhPriv, err := qcrypto.SLHKeypairFromSeed(seed)
	if err != nil {
		t.Fatalf("slh keygen: %v", err)
	}
	return &vfActor{addr: qcrypto.DeriveAddress(pub), pub: pub, priv: priv, slhPub: slhPub, slhPriv: slhPriv}
}

// vfFixture is one deployed verify instance (owner + one operator, threshold
// 1) plus a funded payer and a recipient, driven through the router.
type vfFixture struct {
	sdb   *state.StateDB
	sched forks.Schedule
	owner *vfActor
	op    *vfActor
	payer *vfActor
	user  qcrypto.Address
	inst  qcrypto.Address
}

func vfForks() forks.Schedule {
	return forks.Schedule{forks.VerifyV1: 1, forks.ModuleRevertV1: 1}
}

func newVfFixture(t *testing.T) *vfFixture {
	t.Helper()
	f := &vfFixture{
		sdb:   newTestState(t),
		sched: vfForks(),
		owner: newVfActor(t, 0x21),
		op:    newVfActor(t, 0x22),
		payer: newVfActor(t, 0x23),
		user:  addr(0x44),
	}
	fund(t, f.sdb, f.payer.addr, big.NewInt(1_000_000))
	f.inst = f.deploy(t, f.owner, 7, 1, []string{f.op.addr.String()})
	return f
}

func (f *vfFixture) ctx(caller qcrypto.Address, nonce, height uint64) ModuleContext {
	return ModuleContext{Caller: caller, Nonce: nonce, Height: height, Forks: f.sched}
}

func (f *vfFixture) call(t *testing.T, caller qcrypto.Address, height uint64, action string, params any) (*types.Receipt, error) {
	t.Helper()
	rc := &types.Receipt{}
	err := DispatchModuleCall(f.sdb, f.ctx(caller, 0, height), moduleData(t, "verify", action, params), rc)
	return rc, err
}

func (f *vfFixture) mustCall(t *testing.T, caller qcrypto.Address, height uint64, action string, params any) *types.Receipt {
	t.Helper()
	rc, err := f.call(t, caller, height, action, params)
	if err != nil {
		t.Fatalf("verify.%s: %v", action, err)
	}
	return rc
}

func (f *vfFixture) deploy(t *testing.T, by *vfActor, nonce uint64, threshold int, operators []string) qcrypto.Address {
	t.Helper()
	rc := &types.Receipt{}
	params := verifyDeployParams{Threshold: threshold, Operators: operators}
	if err := DispatchModuleCall(f.sdb, f.ctx(by.addr, nonce, 1), moduleData(t, "verify", "deploy", params), rc); err != nil {
		t.Fatalf("verify.deploy: %v", err)
	}
	if rc.ContractAddress == nil {
		t.Fatal("deploy set no contract address")
	}
	if want := VerifyAddress(by.addr, nonce, 1); *rc.ContractAddress != want {
		t.Fatalf("instance landed at %s, VerifyAddress says %s", rc.ContractAddress, want)
	}
	return *rc.ContractAddress
}

func (f *vfFixture) read(t *testing.T, action string, params any) map[string]interface{} {
	t.Helper()
	v, err := DispatchModuleReadCall(f.sdb, ReadContext{Height: 10}, moduleData(t, "verify", action, params))
	if err != nil {
		t.Fatalf("verify read %s: %v", action, err)
	}
	m, ok := v.(map[string]interface{})
	if !ok {
		t.Fatalf("verify read %s returned %T", action, v)
	}
	return m
}

// instruction builds and dual-signs an instruction with idByte as its id.
func (f *vfFixture) instruction(t *testing.T, signer *vfActor, idByte byte, mutate func(*types.VerifyInstruction)) *types.VerifyInstruction {
	t.Helper()
	var id types.InstructionID
	id[0] = idByte
	ins := &types.VerifyInstruction{
		Version:          types.VerifyInstructionVersion,
		InstructionID:    id,
		Purpose:          "payment",
		Origin:           "bank:test",
		Payload:          []byte("invoice-1"),
		SettlementHint:   "splendor-qc",
		SettlementAction: types.SettleFinalizeOnly,
	}
	if mutate != nil {
		mutate(ins)
	}
	if err := types.SignVerifyInstruction(types.ChainID, ins, signer.priv, signer.slhPriv); err != nil {
		t.Fatalf("sign instruction: %v", err)
	}
	return ins
}

// attestation dual-signs the record's digest for `inst` as actor a.
func (f *vfFixture) attestation(t *testing.T, a *vfActor, inst qcrypto.Address, ins *types.VerifyInstruction) *verifyAttestation {
	t.Helper()
	digest, err := types.VerifyInstructionDigest(types.ChainID, ins)
	if err != nil {
		t.Fatal(err)
	}
	sigM, sigS, err := types.SignVerifyAttestation(types.ChainID, inst, digest, a.priv, a.slhPriv)
	if err != nil {
		t.Fatalf("sign attestation: %v", err)
	}
	return &verifyAttestation{
		PubKey:    hex.EncodeToString(a.pub.Bytes()),
		SLHPubKey: hex.EncodeToString(a.slhPub.Bytes()),
		SigMLDSA:  hex.EncodeToString(sigM),
		SigSLH:    hex.EncodeToString(sigS),
	}
}

func registerParams(inst qcrypto.Address, ins *types.VerifyInstruction) map[string]any {
	return map[string]any{"verify": inst.String(), "instruction": ins}
}

func idParams(inst qcrypto.Address, ins *types.VerifyInstruction) map[string]any {
	return map[string]any{"verify": inst.String(), "instruction_id": ins.InstructionID.Hex()}
}

func attestParams(inst qcrypto.Address, ins *types.VerifyInstruction, a *verifyAttestation) map[string]any {
	p := idParams(inst, ins)
	p["attestation"] = a
	return p
}

func hasEvent(rc *types.Receipt, sig string) bool {
	want := EventTopic(sig)
	for _, l := range rc.Logs {
		if len(l.Topics) > 0 && l.Topics[0] == want {
			return true
		}
	}
	return false
}

// The whole point: register → attest → finalize → spend through the router,
// with the proof readable at each step and the replay refused at the end.
func TestVerifyRoutesTheFullFlow(t *testing.T) {
	f := newVfFixture(t)
	ins := f.instruction(t, f.payer, 0x01, nil)

	rc := f.mustCall(t, f.payer.addr, 2, "register", registerParams(f.inst, ins))
	if !hasEvent(rc, "VerifyRegistered(sqaddress,bytes32,sqaddress)") {
		t.Fatal("register emitted no VerifyRegistered event")
	}
	proof := f.read(t, "proof", idParams(f.inst, ins))
	if proof["instruction_id"] != ins.InstructionID.Hex() || proof["signer"] != f.payer.addr.String() {
		t.Fatalf("proof after register = %v", proof)
	}
	if proof["threshold_met"] != false || proof["finalized"] != false || proof["spent"] != false {
		t.Fatalf("fresh proof should be unmet/unfinalized/unspent: %v", proof)
	}

	rc = f.mustCall(t, f.op.addr, 3, "attest", attestParams(f.inst, ins, f.attestation(t, f.op, f.inst, ins)))
	if !hasEvent(rc, "VerifyAttested(sqaddress,bytes32,sqaddress,uint256)") {
		t.Fatal("attest emitted no VerifyAttested event")
	}
	if _, err := f.call(t, f.op.addr, 3, "attest", attestParams(f.inst, ins, f.attestation(t, f.op, f.inst, ins))); err == nil || !strings.Contains(err.Error(), "already attested") {
		t.Fatalf("double attest: %v", err)
	}
	proof = f.read(t, "proof", idParams(f.inst, ins))
	if proof["threshold_met"] != true || proof["attestations"] != 1 {
		t.Fatalf("proof after attest = %v", proof)
	}

	rc = f.mustCall(t, f.user, 4, "finalize", idParams(f.inst, ins))
	if !hasEvent(rc, "VerifyFinalized(sqaddress,bytes32)") {
		t.Fatal("finalize emitted no VerifyFinalized event")
	}
	if _, err := f.call(t, f.user, 4, "finalize", idParams(f.inst, ins)); err == nil {
		t.Fatal("a second finalize must be refused")
	}

	rc = f.mustCall(t, f.payer.addr, 5, "spend", idParams(f.inst, ins))
	if !hasEvent(rc, "VerifySpent(sqaddress,bytes32,string)") {
		t.Fatal("spend emitted no VerifySpent event")
	}
	proof = f.read(t, "proof", idParams(f.inst, ins))
	if proof["spent"] != true || proof["finalized"] != true || proof["height"] != uint64(5) {
		t.Fatalf("proof after spend = %v", proof)
	}

	// Replay: the same id cannot settle twice, and cannot be re-registered.
	if _, err := f.call(t, f.payer.addr, 6, "spend", idParams(f.inst, ins)); err == nil || !strings.Contains(err.Error(), "already spent") {
		t.Fatalf("replayed spend: %v", err)
	}
	if _, err := f.call(t, f.payer.addr, 6, "register", registerParams(f.inst, ins)); err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("re-register: %v", err)
	}
}

// A value move settles from the caller's own balance and nobody else's.
func TestVerifyNativeTransferSettlesFromTheCaller(t *testing.T) {
	f := newVfFixture(t)
	ins := f.instruction(t, f.payer, 0x02, func(i *types.VerifyInstruction) {
		i.SettlementAction = types.SettleNativeTransfer
		i.From = f.payer.addr.String()
		i.To = f.user.String()
		i.Amount = "250000"
	})
	f.mustCall(t, f.payer.addr, 2, "register", registerParams(f.inst, ins))
	f.mustCall(t, f.op.addr, 3, "attest", attestParams(f.inst, ins, f.attestation(t, f.op, f.inst, ins)))

	// Threshold met but not finalized: spend may proceed directly.
	if _, err := f.call(t, f.op.addr, 4, "spend", idParams(f.inst, ins)); err == nil || !strings.Contains(err.Error(), "is not from") {
		t.Fatalf("spend by someone other than from: %v", err)
	}
	before := balanceOf(t, f.sdb, f.payer.addr)
	f.mustCall(t, f.payer.addr, 4, "spend", idParams(f.inst, ins))
	after := balanceOf(t, f.sdb, f.payer.addr)
	if diff := new(big.Int).Sub(before, after); diff.Cmp(big.NewInt(250000)) != 0 {
		t.Fatalf("payer paid %s, want 250000", diff)
	}
	if got := balanceOf(t, f.sdb, f.user); got.Cmp(big.NewInt(250000)) != 0 {
		t.Fatalf("recipient holds %s, want 250000", got)
	}
	if _, err := f.call(t, f.payer.addr, 5, "spend", idParams(f.inst, ins)); err == nil {
		t.Fatal("the same instruction settled twice")
	}
	if got := balanceOf(t, f.sdb, f.user); got.Cmp(big.NewInt(250000)) != 0 {
		t.Fatalf("recipient balance moved on the replay: %s", got)
	}
}

// A failed settlement leaves nothing behind: the spend that cannot pay is a
// failed receipt with the record untouched.
func TestVerifyFailedSpendIsAtomic(t *testing.T) {
	f := newVfFixture(t)
	ins := f.instruction(t, f.payer, 0x03, func(i *types.VerifyInstruction) {
		i.SettlementAction = types.SettleNativeTransfer
		i.From = f.payer.addr.String()
		i.To = f.user.String()
		i.Amount = "5000000" // more than the payer holds
	})
	f.mustCall(t, f.payer.addr, 2, "register", registerParams(f.inst, ins))
	f.mustCall(t, f.op.addr, 3, "attest", attestParams(f.inst, ins, f.attestation(t, f.op, f.inst, ins)))
	rc, err := f.call(t, f.payer.addr, 4, "spend", idParams(f.inst, ins))
	if err == nil {
		t.Fatal("an unfunded spend succeeded")
	}
	if IsHardModuleError(err) {
		t.Fatalf("an unfunded spend must be SOFT, got hard: %v", err)
	}
	if len(rc.Logs) != 0 {
		t.Fatalf("failed spend left %d logs on the receipt", len(rc.Logs))
	}
	proof := f.read(t, "proof", idParams(f.inst, ins))
	if proof["spent"] != false {
		t.Fatalf("failed spend marked the record spent: %v", proof)
	}
}

// Registration is for the instruction's signer or the instance's attestors.
func TestVerifyRegisterRequiresSignerOrAttestor(t *testing.T) {
	f := newVfFixture(t)
	stranger := addr(0x99)
	ins := f.instruction(t, f.payer, 0x04, nil)
	if _, err := f.call(t, stranger, 2, "register", registerParams(f.inst, ins)); err == nil || !strings.Contains(err.Error(), "neither the instruction signer") {
		t.Fatalf("stranger register: %v", err)
	}
	f.mustCall(t, f.op.addr, 2, "register", registerParams(f.inst, ins))

	other := f.instruction(t, f.payer, 0x05, nil)
	f.mustCall(t, f.owner.addr, 3, "register", registerParams(f.inst, other))
}

// An attestation given to one instance is worthless on another, even when
// the same instruction is registered there and the same operator is listed.
func TestVerifyAttestationIsBoundToTheInstance(t *testing.T) {
	f := newVfFixture(t)
	second := f.deploy(t, f.owner, 8, 1, []string{f.op.addr.String()})
	ins := f.instruction(t, f.payer, 0x06, nil)
	f.mustCall(t, f.payer.addr, 2, "register", registerParams(f.inst, ins))
	f.mustCall(t, f.payer.addr, 2, "register", registerParams(second, ins))

	forFirst := f.attestation(t, f.op, f.inst, ins)
	if _, err := f.call(t, f.op.addr, 3, "attest", attestParams(second, ins, forFirst)); err == nil || !strings.Contains(err.Error(), "invalid ML-DSA-65 attestation") {
		t.Fatalf("attestation replayed across instances: %v", err)
	}
	f.mustCall(t, f.op.addr, 3, "attest", attestParams(f.inst, ins, forFirst))

	// A signer who is not an attestor is refused before any verification.
	if _, err := f.call(t, f.payer.addr, 3, "attest", attestParams(second, ins, f.attestation(t, f.payer, second, ins))); err == nil || !strings.Contains(err.Error(), "is not an attestor") {
		t.Fatalf("non-operator attestation: %v", err)
	}
}

// Every field of an attestation may be hex or base64; the length decides,
// and a wrong length is refused before any cryptography.
func TestVerifyAttestationEncodingsAreLengthChecked(t *testing.T) {
	f := newVfFixture(t)
	ins := f.instruction(t, f.payer, 0x07, nil)
	f.mustCall(t, f.payer.addr, 2, "register", registerParams(f.inst, ins))

	a := f.attestation(t, f.op, f.inst, ins)
	toB64 := func(h string) string {
		raw, err := hex.DecodeString(h)
		if err != nil {
			t.Fatal(err)
		}
		return base64.StdEncoding.EncodeToString(raw)
	}
	b64 := &verifyAttestation{PubKey: toB64(a.PubKey), SLHPubKey: toB64(a.SLHPubKey), SigMLDSA: toB64(a.SigMLDSA), SigSLH: toB64(a.SigSLH)}

	short := *a
	short.SigSLH = short.SigSLH[:len(short.SigSLH)-2]
	if _, err := f.call(t, f.op.addr, 3, "attest", attestParams(f.inst, ins, &short)); err == nil || !strings.Contains(err.Error(), "sig_slh") {
		t.Fatalf("short SLH signature: %v", err)
	}
	empty := *a
	empty.PubKey = ""
	if _, err := f.call(t, f.op.addr, 3, "attest", attestParams(f.inst, ins, &empty)); err == nil || !strings.Contains(err.Error(), "pubkey is required") {
		t.Fatalf("empty pubkey: %v", err)
	}
	f.mustCall(t, f.op.addr, 3, "attest", attestParams(f.inst, ins, b64))
}

// Pause stops every state-advancing action; the owner can still configure.
func TestVerifyPauseStopsEveryAdvance(t *testing.T) {
	f := newVfFixture(t)
	ins := f.instruction(t, f.payer, 0x08, nil)
	f.mustCall(t, f.payer.addr, 2, "register", registerParams(f.inst, ins))
	inst := map[string]any{"verify": f.inst.String()}

	if _, err := f.call(t, f.op.addr, 3, "pause", inst); err == nil {
		t.Fatal("an operator paused the instance")
	}
	f.mustCall(t, f.owner.addr, 3, "pause", inst)
	if f.read(t, "instance", inst)["paused"] != true {
		t.Fatal("instance read does not show paused")
	}
	later := f.instruction(t, f.payer, 0x09, nil)
	for action, params := range map[string]any{
		"register": registerParams(f.inst, later),
		"attest":   attestParams(f.inst, ins, f.attestation(t, f.op, f.inst, ins)),
		"finalize": idParams(f.inst, ins),
		"spend":    idParams(f.inst, ins),
	} {
		if _, err := f.call(t, f.op.addr, 4, action, params); err == nil || !strings.Contains(err.Error(), "is paused") {
			t.Fatalf("%s while paused: %v", action, err)
		}
	}
	active := true
	f.mustCall(t, f.owner.addr, 4, "set_operator", map[string]any{"verify": f.inst.String(), "operator": addr(0x55).String(), "active": active})
	f.mustCall(t, f.owner.addr, 5, "unpause", inst)
	f.mustCall(t, f.op.addr, 6, "attest", attestParams(f.inst, ins, f.attestation(t, f.op, f.inst, ins)))
}

// x402 is not an on-chain settlement; it is refused when the instruction is
// registered, not silently no-op'd when it is spent. Unknown actions and a
// value move whose hint points elsewhere are refused too.
func TestVerifyRefusesSettlementsItCannotPerform(t *testing.T) {
	f := newVfFixture(t)
	x402 := f.instruction(t, f.payer, 0x0A, func(i *types.VerifyInstruction) { i.SettlementAction = types.SettleX402 })
	if _, err := f.call(t, f.payer.addr, 2, "register", registerParams(f.inst, x402)); err == nil || !strings.Contains(err.Error(), "/x402/settle") {
		t.Fatalf("x402 register: %v", err)
	}
	unknown := f.instruction(t, f.payer, 0x0B, func(i *types.VerifyInstruction) { i.SettlementAction = "teleport" })
	if _, err := f.call(t, f.payer.addr, 2, "register", registerParams(f.inst, unknown)); err == nil || !strings.Contains(err.Error(), "unknown settlement_action") {
		t.Fatalf("unknown action register: %v", err)
	}
	malformed := f.instruction(t, f.payer, 0x0C, func(i *types.VerifyInstruction) {
		i.SettlementAction = types.SettleNativeTransfer
		i.From = f.payer.addr.String()
		i.To = f.user.String()
		i.Amount = "-1"
	})
	if _, err := f.call(t, f.payer.addr, 2, "register", registerParams(f.inst, malformed)); err == nil || !strings.Contains(err.Error(), "amount") {
		t.Fatalf("malformed amount register: %v", err)
	}
	elsewhere := f.instruction(t, f.payer, 0x0D, func(i *types.VerifyInstruction) {
		i.SettlementHint = "bitcoin"
		i.SettlementAction = types.SettleNativeTransfer
		i.From = f.payer.addr.String()
		i.To = f.user.String()
		i.Amount = "1"
	})
	f.mustCall(t, f.payer.addr, 2, "register", registerParams(f.inst, elsewhere))
	f.mustCall(t, f.op.addr, 3, "attest", attestParams(f.inst, elsewhere, f.attestation(t, f.op, f.inst, elsewhere)))
	if _, err := f.call(t, f.payer.addr, 4, "spend", idParams(f.inst, elsewhere)); err == nil || !strings.Contains(err.Error(), "not a splendor-qc hint") {
		t.Fatalf("value move with a foreign hint: %v", err)
	}
	// finalize_only with a foreign hint is the proof-elsewhere flow: allowed.
	proofOnly := f.instruction(t, f.payer, 0x0E, func(i *types.VerifyInstruction) { i.SettlementHint = "bitcoin" })
	f.mustCall(t, f.payer.addr, 5, "register", registerParams(f.inst, proofOnly))
	f.mustCall(t, f.op.addr, 6, "attest", attestParams(f.inst, proofOnly, f.attestation(t, f.op, f.inst, proofOnly)))
	f.mustCall(t, f.user, 7, "spend", idParams(f.inst, proofOnly))
}

// A threshold nobody can reach is refused at every door that could set one.
func TestVerifyThresholdIsBoundedByTheAttestors(t *testing.T) {
	f := newVfFixture(t)
	rc := &types.Receipt{}
	err := DispatchModuleCall(f.sdb, f.ctx(f.owner.addr, 9, 1), moduleData(t, "verify", "deploy", verifyDeployParams{Threshold: 3, Operators: []string{f.op.addr.String()}}), rc)
	if err == nil || !strings.Contains(err.Error(), "above the 2 attestors") {
		t.Fatalf("deploy with threshold 3 over 2 attestors: %v", err)
	}
	if _, err := f.call(t, f.owner.addr, 2, "set_threshold", map[string]any{"verify": f.inst.String(), "threshold": 3}); err == nil {
		t.Fatal("set_threshold above the attestor count accepted")
	}
	f.mustCall(t, f.owner.addr, 2, "set_threshold", map[string]any{"verify": f.inst.String(), "threshold": 2})
	inactive := false
	if _, err := f.call(t, f.owner.addr, 3, "set_operator", map[string]any{"verify": f.inst.String(), "operator": f.op.addr.String(), "active": inactive}); err == nil || !strings.Contains(err.Error(), "lower the threshold first") {
		t.Fatalf("revoking the operator a threshold of 2 depends on: %v", err)
	}
	if _, err := f.call(t, f.owner.addr, 3, "set_operator", map[string]any{"verify": f.inst.String(), "operator": f.op.addr.String()}); err == nil || !strings.Contains(err.Error(), "active is required") {
		t.Fatalf("set_operator without active: %v", err)
	}
	// With threshold 2 the owner's own attestation counts as the second.
	ins := f.instruction(t, f.payer, 0x0F, nil)
	f.mustCall(t, f.payer.addr, 4, "register", registerParams(f.inst, ins))
	f.mustCall(t, f.op.addr, 5, "attest", attestParams(f.inst, ins, f.attestation(t, f.op, f.inst, ins)))
	if _, err := f.call(t, f.user, 6, "finalize", idParams(f.inst, ins)); err == nil || !strings.Contains(err.Error(), "need 2") {
		t.Fatalf("finalize with 1 of 2: %v", err)
	}
	f.mustCall(t, f.owner.addr, 6, "attest", attestParams(f.inst, ins, f.attestation(t, f.owner, f.inst, ins)))
	f.mustCall(t, f.user, 7, "finalize", idParams(f.inst, ins))
}

// The fork gate is HARD before verify_v1, like every routed module.
func TestVerifyIsGatedOnTheFork(t *testing.T) {
	f := newVfFixture(t)
	f.sched = forks.Schedule{forks.VerifyV1: 100, forks.ModuleRevertV1: 1}
	_, err := f.call(t, f.owner.addr, 50, "pause", map[string]any{"verify": f.inst.String()})
	if !errors.Is(err, ErrForkInactive) || !IsHardModuleError(err) {
		t.Fatalf("before the fork: %v", err)
	}
	if _, err := PreflightModuleCall(moduleData(t, "verify", "pause", map[string]any{"verify": f.inst.String()}), f.sched, 50); !errors.Is(err, ErrForkInactive) {
		t.Fatalf("preflight before the fork: %v", err)
	}
}
