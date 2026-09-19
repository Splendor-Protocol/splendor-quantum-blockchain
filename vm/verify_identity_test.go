package vm

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/x402"
)

type vfyOp struct {
	addr     qcrypto.Address
	mldsa    *qcrypto.PrivateKey
	mldsaPub *qcrypto.PublicKey
	slh      *qcrypto.SLHPrivateKey
	slhPub   *qcrypto.SLHPublicKey
}

func newVfyOp(t *testing.T) *vfyOp {
	t.Helper()
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("mldsa: %v", err)
	}
	slhPub, slhPriv, err := qcrypto.GenerateSLHKeypair()
	if err != nil {
		t.Fatalf("slh: %v", err)
	}
	return &vfyOp{
		addr:     qcrypto.DeriveAddress(pub),
		mldsa:    priv,
		mldsaPub: pub,
		slh:      slhPriv,
		slhPub:   slhPub,
	}
}

func vfyForks(identity bool) forks.Schedule {
	s := forks.Schedule{forks.VerifyV1: 1, forks.ModuleRevertV1: 1}
	if identity {
		s[forks.VerifyIdentityV1] = 1
	}
	return s
}

func vfyCtx(caller qcrypto.Address, nonce uint64, identity bool) ModuleContext {
	return ModuleContext{Caller: caller, Nonce: nonce, Height: 1, Time: 1, Forks: vfyForks(identity)}
}

func callVerify(t *testing.T, sdb *state.StateDB, ctx ModuleContext, action string, params any) (*types.Receipt, error) {
	t.Helper()
	rc := &types.Receipt{}
	return rc, DispatchModuleCall(sdb, ctx, moduleData(t, "verify", action, params), rc)
}

func deployVerify(t *testing.T, sdb *state.StateDB, owner *vfyOp, op *vfyOp, identity bool) qcrypto.Address {
	t.Helper()
	params := map[string]any{"threshold": 1}
	if identity {
		params["operator_identities"] = []map[string]any{{
			"operator":   op.addr.String(),
			"slh_pubkey": hex.EncodeToString(op.slhPub.Bytes()),
		}}
	} else {
		params["operators"] = []string{op.addr.String()}
	}
	rc, err := callVerify(t, sdb, vfyCtx(owner.addr, 1, identity), "deploy", params)
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	if rc.ContractAddress == nil {
		t.Fatal("deploy set no address")
	}
	return *rc.ContractAddress
}

func sampleVfyIns(action, from string, payload []byte) types.VerifyInstruction {
	var id [32]byte
	id[0] = 0x42
	if action == "" {
		action = "finalize_only"
	}
	return types.VerifyInstruction{
		Version:          types.VerifyInstructionVersion,
		InstructionID:    id,
		Purpose:          "payment",
		Origin:           "test:origin",
		Payload:          payload,
		SettlementHint:   "splendor-qc",
		SettlementAction: action,
		From:             from,
	}
}

func registerIns(t *testing.T, sdb *state.StateDB, owner *vfyOp, inst qcrypto.Address, ins *types.VerifyInstruction, identity bool) {
	t.Helper()
	if err := types.SignVerifyInstruction(types.ChainID, ins, owner.mldsa, owner.slh); err != nil {
		t.Fatalf("sign instruction: %v", err)
	}
	raw, err := json.Marshal(ins)
	if err != nil {
		t.Fatalf("marshal instruction: %v", err)
	}
	if _, err := callVerify(t, sdb, vfyCtx(owner.addr, 2, identity), "register", map[string]any{
		"verify":      inst.String(),
		"instruction": json.RawMessage(raw),
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
}

func signAttest(t *testing.T, inst qcrypto.Address, instructionDigest [32]byte, op *vfyOp, identity bool) verifyAttestation {
	t.Helper()
	msg := instructionDigest
	if identity {
		msg = verifyAttestDigest(inst, instructionDigest)
	}
	sigM, err := qcrypto.Sign(op.mldsa, msg[:], qcrypto.CtxVerify)
	if err != nil {
		t.Fatalf("sign mldsa: %v", err)
	}
	sigS, err := qcrypto.SignSLH(op.slh, msg[:], qcrypto.CtxVerify)
	if err != nil {
		t.Fatalf("sign slh: %v", err)
	}
	return verifyAttestation{
		PubKey:   hex.EncodeToString(op.mldsaPub.Bytes()),
		SLHPub:   hex.EncodeToString(op.slhPub.Bytes()),
		SigMLDSA: hex.EncodeToString(sigM),
		SigSLH:   hex.EncodeToString(sigS),
	}
}

func TestX402SpendIsNoOpWithoutIdentityFork(t *testing.T) {
	sdb := newTestState(t)
	owner := newVfyOp(t)
	op := newVfyOp(t)
	inst := deployVerify(t, sdb, owner, op, false)
	ins := sampleVfyIns("x402", owner.addr.String(), []byte("not-a-nonce"))
	registerIns(t, sdb, owner, inst, &ins, false)
	digest, err := types.VerifyInstructionDigest(types.ChainID, &ins)
	if err != nil {
		t.Fatal(err)
	}
	att := signAttest(t, inst, digest, op, false)
	if _, err := callVerify(t, sdb, vfyCtx(op.addr, 3, false), "attest", map[string]any{
		"verify": inst.String(), "instruction_id": types.InstructionIDHex(ins.InstructionID), "attestation": att,
	}); err != nil {
		t.Fatalf("attest: %v", err)
	}
	if _, err := callVerify(t, sdb, vfyCtx(op.addr, 4, false), "spend", map[string]any{
		"verify": inst.String(), "instruction_id": types.InstructionIDHex(ins.InstructionID),
	}); err != nil {
		t.Fatalf("pre-fork x402 spend must stay a no-op: %v", err)
	}
}

func TestX402SpendRequiresUsedNonceUnderIdentityFork(t *testing.T) {
	sdb := newTestState(t)
	owner := newVfyOp(t)
	op := newVfyOp(t)
	inst := deployVerify(t, sdb, owner, op, true)

	var nonce [32]byte
	nonce[0] = 0xAB
	ins := sampleVfyIns("x402", owner.addr.String(), nonce[:])
	registerIns(t, sdb, owner, inst, &ins, true)
	digest, err := types.VerifyInstructionDigest(types.ChainID, &ins)
	if err != nil {
		t.Fatal(err)
	}
	att := signAttest(t, inst, digest, op, true)
	if _, err := callVerify(t, sdb, vfyCtx(op.addr, 3, true), "attest", map[string]any{
		"verify": inst.String(), "instruction_id": types.InstructionIDHex(ins.InstructionID), "attestation": att,
	}); err != nil {
		t.Fatalf("attest: %v", err)
	}

	_, err = callVerify(t, sdb, vfyCtx(op.addr, 4, true), "spend", map[string]any{
		"verify": inst.String(), "instruction_id": types.InstructionIDHex(ins.InstructionID),
	})
	if err == nil || !strings.Contains(err.Error(), "no settlement record") {
		t.Fatalf("x402 spend without a settled nonce: %v", err)
	}

	if err := sdb.MarkX402NonceSettled(x402.NonceKey(owner.addr, nonce), &types.X402Settlement{
		From:    owner.addr,
		To:      qcrypto.Address{0x55},
		Amount:  big.NewInt(500),
		ChainID: types.ChainID,
		Height:  1,
	}); err != nil {
		t.Fatalf("mark nonce: %v", err)
	}
	if _, err := callVerify(t, sdb, vfyCtx(op.addr, 5, true), "spend", map[string]any{
		"verify": inst.String(), "instruction_id": types.InstructionIDHex(ins.InstructionID),
	}); err != nil {
		t.Fatalf("x402 spend after nonce consumed: %v", err)
	}
}

func TestDeployWithoutSLHRefusedUnderIdentityFork(t *testing.T) {
	sdb := newTestState(t)
	owner := newVfyOp(t)
	op := newVfyOp(t)
	_, err := callVerify(t, sdb, vfyCtx(owner.addr, 1, true), "deploy", map[string]any{
		"threshold": 1,
		"operators": []string{op.addr.String()},
	})
	if err == nil || !strings.Contains(err.Error(), "operator_identities") {
		t.Fatalf("deploy without SLH identities: %v", err)
	}
}

func TestAttestRejectsUnboundSLHUnderIdentityFork(t *testing.T) {
	sdb := newTestState(t)
	owner := newVfyOp(t)
	op := newVfyOp(t)
	inst := deployVerify(t, sdb, owner, op, true)
	ins := sampleVfyIns("finalize_only", "", []byte("x"))
	registerIns(t, sdb, owner, inst, &ins, true)
	digest, err := types.VerifyInstructionDigest(types.ChainID, &ins)
	if err != nil {
		t.Fatal(err)
	}

	other := newVfyOp(t)
	msg := verifyAttestDigest(inst, digest)
	sigM, err := qcrypto.Sign(op.mldsa, msg[:], qcrypto.CtxVerify)
	if err != nil {
		t.Fatal(err)
	}
	sigS, err := qcrypto.SignSLH(other.slh, msg[:], qcrypto.CtxVerify)
	if err != nil {
		t.Fatal(err)
	}
	_, err = callVerify(t, sdb, vfyCtx(op.addr, 3, true), "attest", map[string]any{
		"verify":         inst.String(),
		"instruction_id": types.InstructionIDHex(ins.InstructionID),
		"attestation": verifyAttestation{
			PubKey:   hex.EncodeToString(op.mldsaPub.Bytes()),
			SLHPub:   hex.EncodeToString(other.slhPub.Bytes()),
			SigMLDSA: hex.EncodeToString(sigM),
			SigSLH:   hex.EncodeToString(sigS),
		},
	})
	if err == nil || !strings.Contains(err.Error(), "not bound") {
		t.Fatalf("unbound SLH accepted: %v", err)
	}
}

func TestAttestationIsBoundToInstance(t *testing.T) {
	sdb := newTestState(t)
	owner := newVfyOp(t)
	op := newVfyOp(t)
	a := deployVerify(t, sdb, owner, op, true)
	b, err := callVerify(t, sdb, vfyCtx(owner.addr, 7, true), "deploy", map[string]any{
		"threshold": 1,
		"operator_identities": []map[string]any{{
			"operator":   op.addr.String(),
			"slh_pubkey": hex.EncodeToString(op.slhPub.Bytes()),
		}},
	})
	if err != nil {
		t.Fatalf("deploy b: %v", err)
	}
	if b.ContractAddress == nil {
		t.Fatal("deploy b set no address")
	}

	ins := sampleVfyIns("finalize_only", "", []byte("shared"))
	registerIns(t, sdb, owner, a, &ins, true)
	if err := types.SignVerifyInstruction(types.ChainID, &ins, owner.mldsa, owner.slh); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(ins)
	if _, err := callVerify(t, sdb, vfyCtx(owner.addr, 8, true), "register", map[string]any{
		"verify": b.ContractAddress.String(), "instruction": json.RawMessage(raw),
	}); err != nil {
		t.Fatalf("register b: %v", err)
	}

	digest, err := types.VerifyInstructionDigest(types.ChainID, &ins)
	if err != nil {
		t.Fatal(err)
	}
	attA := signAttest(t, a, digest, op, true)
	if _, err := callVerify(t, sdb, vfyCtx(op.addr, 9, true), "attest", map[string]any{
		"verify": a.String(), "instruction_id": types.InstructionIDHex(ins.InstructionID), "attestation": attA,
	}); err != nil {
		t.Fatalf("attest a: %v", err)
	}
	_, err = callVerify(t, sdb, vfyCtx(op.addr, 10, true), "attest", map[string]any{
		"verify": b.ContractAddress.String(), "instruction_id": types.InstructionIDHex(ins.InstructionID), "attestation": attA,
	})
	if err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("replayed attestation across instances: %v", err)
	}
}

func TestSetOperatorRequiresSLHUnderIdentityFork(t *testing.T) {
	sdb := newTestState(t)
	owner := newVfyOp(t)
	op := newVfyOp(t)
	inst := deployVerify(t, sdb, owner, op, true)
	next := newVfyOp(t)
	_, err := callVerify(t, sdb, vfyCtx(owner.addr, 3, true), "set_operator", map[string]any{
		"verify":   inst.String(),
		"operator": next.addr.String(),
		"active":   true,
	})
	if err == nil || !strings.Contains(err.Error(), "slh_pubkey") {
		t.Fatalf("set_operator without SLH: %v", err)
	}
	if _, err := callVerify(t, sdb, vfyCtx(owner.addr, 4, true), "set_operator", map[string]any{
		"verify":     inst.String(),
		"operator":   next.addr.String(),
		"slh_pubkey": hex.EncodeToString(next.slhPub.Bytes()),
		"active":     true,
	}); err != nil {
		t.Fatalf("set_operator with SLH: %v", err)
	}
}

// OperatorIdentities is consensus-visible state that the un-forked chain does
// not persist. Before verify_identity_v1 a deploy or set_operator that names an
// slh_pubkey must NOT write it into instance state (state roots stay identical
// to pre-activation builds); the same call registers it once the fork is live.
func TestOperatorIdentitiesNotPersistedBeforeIdentityFork(t *testing.T) {
	sdb := newTestState(t)
	owner := newVfyOp(t)
	op := newVfyOp(t)

	// Deploy with operator_identities pre-fork is accepted but not persisted.
	if _, err := callVerify(t, sdb, vfyCtx(owner.addr, 1, false), "deploy", map[string]any{
		"threshold": 1,
		"operator_identities": []map[string]any{{
			"operator":   op.addr.String(),
			"slh_pubkey": hex.EncodeToString(op.slhPub.Bytes()),
		}},
	}); err != nil {
		t.Fatalf("pre-fork deploy with identities: %v", err)
	}
	// The deploy address derives from (owner, nonce=1, height=1).
	deployAddr := verifyDeployAddr(owner.addr, 1, 1)
	inst, err := loadInstance(sdb, deployAddr)
	if err != nil {
		t.Fatalf("loadInstance: %v", err)
	}
	if len(inst.OperatorIdentities) != 0 {
		t.Fatalf("pre-fork deploy persisted %d operator identities", len(inst.OperatorIdentities))
	}

	// set_operator pre-fork parses but does not persist the SLH key.
	if _, err := callVerify(t, sdb, vfyCtx(owner.addr, 2, false), "set_operator", map[string]any{
		"verify":     deployAddr.String(),
		"operator":   op.addr.String(),
		"slh_pubkey": hex.EncodeToString(op.slhPub.Bytes()),
		"active":     true,
	}); err != nil {
		t.Fatalf("pre-fork set_operator with slh: %v", err)
	}
	inst, err = loadInstance(sdb, deployAddr)
	if err != nil {
		t.Fatalf("loadInstance: %v", err)
	}
	if len(inst.OperatorIdentities) != 0 {
		t.Fatalf("pre-fork set_operator persisted %d operator identities", len(inst.OperatorIdentities))
	}
	if len(inst.Operators) != 1 || inst.Operators[0] != op.addr {
		t.Fatalf("pre-fork set_operator did not record the operator: %+v", inst.Operators)
	}

	// The migration path: once the fork is active, the same set_operator
	// registers the key.
	if _, err := callVerify(t, sdb, vfyCtx(owner.addr, 3, true), "set_operator", map[string]any{
		"verify":     deployAddr.String(),
		"operator":   op.addr.String(),
		"slh_pubkey": hex.EncodeToString(op.slhPub.Bytes()),
		"active":     true,
	}); err != nil {
		t.Fatalf("forked set_operator with slh: %v", err)
	}
	inst, err = loadInstance(sdb, deployAddr)
	if err != nil {
		t.Fatalf("loadInstance: %v", err)
	}
	if got, ok := operatorSLH(inst, op.addr); !ok || !bytes.Equal(got, op.slhPub.Bytes()) {
		t.Fatalf("forked set_operator did not register the SLH key (ok=%v)", ok)
	}
}

func TestLoadRecordPropagatesReadError(t *testing.T) {
	sdb := newTestState(t)
	owner := newVfyOp(t)
	op := newVfyOp(t)
	inst := deployVerify(t, sdb, owner, op, true)
	ins := sampleVfyIns("finalize_only", "", []byte("x"))
	registerIns(t, sdb, owner, inst, &ins, true)

	sdb.FailNextStorageRead(errors.New("disk gone"))
	_, err := loadRecord(sdb, inst, ins.InstructionID)
	if err == nil || !strings.Contains(err.Error(), "disk gone") {
		t.Fatalf("read error treated as absent: %v", err)
	}

	sdb.FailNextStorageRead(errors.New("disk gone"))
	_, err = callVerify(t, sdb, vfyCtx(owner.addr, 9, true), "register", map[string]any{
		"verify":      inst.String(),
		"instruction": json.RawMessage([]byte(`{}`)),
	})
	if err == nil {
		t.Fatal("register overwrote a record after a read error")
	}
	if strings.Contains(err.Error(), "already registered") {
		t.Fatal("read error was interpreted as an existing record")
	}

	slot := verifyRecordSlot(inst, ins.InstructionID)
	if err := sdb.SetStorageValue(inst, slot, []byte("not-json")); err != nil {
		t.Fatalf("corrupt: %v", err)
	}
	_, err = loadRecord(sdb, inst, ins.InstructionID)
	if err == nil || !strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("corrupt record treated as absent: %v", err)
	}
}

func TestVerifyIdentityForkUnscheduledOnEmptySchedule(t *testing.T) {
	if (forks.Schedule{}).Active(forks.VerifyIdentityV1, 1) {
		t.Fatal("empty schedule must not activate verify_identity_v1")
	}
}
