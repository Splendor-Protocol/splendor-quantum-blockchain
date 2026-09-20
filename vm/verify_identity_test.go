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
		// The linkage resolves settlement records, so the rule that writes
		// them must be in force no later than the linkage (see Schedule.Validate).
		s[forks.X402SettleV1] = 1
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

// sampleX402Ins builds the shape an x402 proof must have: it names the payee
// and the amount it claims, both of which are inside the v2 instruction digest.
// An instruction carrying only a payer and a nonce proves nothing beyond "this
// payer consumed some nonce", which is why the linkage refuses it.
func sampleX402Ins(from, to, amount string, payload []byte) types.VerifyInstruction {
	ins := sampleVfyIns("x402", from, payload)
	ins.To = to
	ins.Amount = amount
	return ins
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
	ins := sampleX402Ins(owner.addr.String(), qcrypto.Address{0x55}.String(), "500", []byte("not-a-nonce"))
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
	payee := qcrypto.Address{0x55}
	ins := sampleX402Ins(owner.addr.String(), payee.String(), "500", nonce[:])
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
		To:      payee,
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

// The linkage must not degrade to payer + nonce. An x402 instruction that names
// no payee and no amount used to be accepted against ANY payment the same payer
// had settled — including one to an unrelated recipient, for an unrelated sum.
// Both fields are inside the signed v2 digest, so requiring them costs the payer
// nothing and makes the proof say what it claims to say.
func TestX402LinkageRequiresPayeeAndAmount(t *testing.T) {
	var nonce [32]byte
	nonce[0] = 0xC1

	cases := []struct {
		name    string
		to      string
		amount  string
		wantErr string
	}{
		{"no payee, no amount", "", "", "must name the payee and amount"},
		{"no amount", qcrypto.Address{0x55}.String(), "", "must name the payee and amount"},
		{"no payee", "", "500", "must name the payee and amount"},
		{"payee of an unrelated payment", qcrypto.Address{0x66}.String(), "500", "does not bind this payment"},
		{"amount of an unrelated payment", qcrypto.Address{0x55}.String(), "499", "does not bind this payment"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sdb := newTestState(t)
			owner := newVfyOp(t)
			op := newVfyOp(t)
			inst := deployVerify(t, sdb, owner, op, true)

			// A real payment of 500 from owner to 0x55 has settled.
			if err := sdb.MarkX402NonceSettled(x402.NonceKey(owner.addr, nonce), &types.X402Settlement{
				From:    owner.addr,
				To:      qcrypto.Address{0x55},
				Amount:  big.NewInt(500),
				ChainID: types.ChainID,
				Height:  1,
			}); err != nil {
				t.Fatalf("mark nonce: %v", err)
			}

			ins := sampleX402Ins(owner.addr.String(), tc.to, tc.amount, nonce[:])
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
			if err == nil {
				t.Fatal("a weakly-linked x402 proof was accepted against a settled payment")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not mention %q", err, tc.wantErr)
			}
		})
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

// operator_identities is recorded from the module's own activation, not from
// verify_identity_v1. Every build the network has run with verify_v1 active
// parses the field, refuses a malformed key, and writes the identities into the
// instance record — and that record is a state leaf. So a build that WITHHELD
// the identities pre-activation would be the one that forks: same deploy, a
// different leaf, a different state root, and a replay of live history in which
// it rejects its own chain.
//
// That early recording is also the key-registration migration the review asked
// for. Pre-activation the stored key binds nothing — attest neither looks it up
// nor compares against it — so instances accumulate their operators' SLH-DSA
// keys harmlessly, and verify_identity_v1 activates onto a registry that is
// already populated instead of demanding a separate migration first.
//
// This test therefore pins the opposite of what its previous version pinned:
// the field is honoured on BOTH sides of the fork, and only the requirements
// differ.
func TestVerifyIdentityRecordsOperatorKeysFromModuleActivation(t *testing.T) {
	owner := newVfyOp(t)
	op := newVfyOp(t)

	deploy := func(sdb *state.StateDB, identity bool, params map[string]any) *verifyInstance {
		t.Helper()
		rc, err := callVerify(t, sdb, vfyCtx(owner.addr, 1, identity), "deploy", params)
		if err != nil {
			t.Fatalf("deploy: %v", err)
		}
		inst, err := loadInstance(sdb, *rc.ContractAddress)
		if err != nil {
			t.Fatalf("load instance: %v", err)
		}
		return inst
	}

	// Pre-fork, a deploy carrying operator_identities records them.
	withField := deploy(newTestState(t), false, map[string]any{
		"threshold": 1,
		"operators": []string{op.addr.String()},
		"operator_identities": []map[string]any{
			{"operator": op.addr.String(), "slh_pubkey": hex.EncodeToString(op.slhPub.Bytes())},
		},
	})
	bound, ok := operatorSLH(withField, op.addr)
	if !ok || !bytes.Equal(bound, op.slhPub.Bytes()) {
		t.Fatalf("pre-fork deploy did not record the operator's SLH key: %d identities stored",
			len(withField.OperatorIdentities))
	}

	// And the same deploy under the fork records exactly the same thing: the
	// activation adds requirements, it does not add or reshape state.
	post := deploy(newTestState(t), true, map[string]any{
		"threshold": 1,
		"operators": []string{op.addr.String()},
		"operator_identities": []map[string]any{
			{"operator": op.addr.String(), "slh_pubkey": hex.EncodeToString(op.slhPub.Bytes())},
		},
	})
	preJSON, _ := json.Marshal(withField)
	postJSON, _ := json.Marshal(post)
	if !bytes.Equal(preJSON, postJSON) {
		t.Fatalf("the same deploy records a different instance either side of %s:\n pre:  %s\n post: %s",
			forks.VerifyIdentityV1, preJSON, postJSON)
	}

	// set_operator is the same: the key offered is decoded and recorded now,
	// so an instance that registers its operators before the activation height
	// needs no migration when the height arrives.
	sdb := newTestState(t)
	rc, err := callVerify(t, sdb, vfyCtx(owner.addr, 1, false), "deploy", map[string]any{
		"threshold": 1,
		"operators": []string{op.addr.String()},
	})
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	instAddr := *rc.ContractAddress
	other := newVfyOp(t)
	if _, err := callVerify(t, sdb, vfyCtx(owner.addr, 2, false), "set_operator", map[string]any{
		"verify":     instAddr.String(),
		"operator":   other.addr.String(),
		"slh_pubkey": hex.EncodeToString(other.slhPub.Bytes()),
		"active":     true,
	}); err != nil {
		t.Fatalf("pre-fork set_operator: %v", err)
	}
	inst, err := loadInstance(sdb, instAddr)
	if err != nil {
		t.Fatalf("load instance: %v", err)
	}
	if !isOperator(inst, other.addr) {
		t.Fatal("pre-fork set_operator did not add the operator")
	}
	got, ok := operatorSLH(inst, other.addr)
	if !ok || !bytes.Equal(got, other.slhPub.Bytes()) {
		t.Fatalf("pre-fork set_operator recorded no SLH key for the operator: %d identities stored",
			len(inst.OperatorIdentities))
	}

	// The recorded key is inert until the fork: attest pre-fork signs the bare
	// instruction digest and never consults the registry, which is why filling
	// it early is a migration rather than an early activation. An attestor whose
	// registered key is NOT the one it signs with still passes pre-fork.
	ins := sampleVfyIns("finalize_only", "", []byte("inert"))
	registerIns(t, sdb, owner, instAddr, &ins, false)
	digest, err := types.VerifyInstructionDigest(types.ChainID, &ins)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := callVerify(t, sdb, vfyCtx(op.addr, 3, false), "attest", map[string]any{
		"verify":         instAddr.String(),
		"instruction_id": types.InstructionIDHex(ins.InstructionID),
		"attestation":    signAttest(t, instAddr, digest, op, false),
	}); err != nil {
		t.Fatalf("pre-fork attest must ignore the identity registry: %v", err)
	}
}
