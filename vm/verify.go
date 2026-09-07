package vm

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// ── Verify (SQVL): dual-signed instructions, attestations, settlement ────────
//
// An instance is a policy container: an owner, up to MaxVerifyOperators
// operators, and a threshold. An external party dual-signs a
// types.VerifyInstruction (ML-DSA-65 + SLH-DSA under CtxVerify); `register`
// records it under the instance; operators `attest` it (each attestation is
// itself dual-signed and bound to this instance); once `threshold`
// attestations are in, `finalize` records the fact or `spend` settles it —
// moving native SPLD or a QRC-20 out of `from`, and only when `from` is the
// caller — and marks the instruction spent for good.
//
// What the module cannot do: mint anything, move a balance its caller does not
// own, or settle the same instruction twice. What it deliberately does not do:
// settle x402 — that flow lives behind POST /x402/settle, and an on-chain
// action that marked an instruction settled while moving nothing would be a
// false proof, so it is refused at register.

const (
	// MaxVerifyOperators caps the operator list of one instance. The owner is
	// always an attestor too, so an instance can carry at most 17.
	MaxVerifyOperators = 16
)

// verifyInstance is one deployed policy container.
type verifyInstance struct {
	Owner     qcrypto.Address   `json:"owner"`
	Threshold int               `json:"threshold"`
	Operators []qcrypto.Address `json:"operators"`
	Paused    bool              `json:"paused"`
	Height    uint64            `json:"height"` // deployed at
}

// verifyRecord is one registered instruction and everything that happened to it.
type verifyRecord struct {
	Instruction      types.VerifyInstruction `json:"instruction"`
	Digest           [32]byte                `json:"digest"`
	Signer           qcrypto.Address         `json:"signer"` // the instruction's ML-DSA key, as an address
	Attestors        []qcrypto.Address       `json:"attestors"`
	Finalized        bool                    `json:"finalized"`
	Spent            bool                    `json:"spent"`
	RegisteredHeight uint64                  `json:"registered_height"`
	Height           uint64                  `json:"height,omitempty"` // finalized / spent at
}

func verifyInstanceSlot(addr qcrypto.Address) [32]byte {
	return qcrypto.Hash32(append([]byte("verify:inst:"), addr[:]...))
}

func verifyRecordSlot(inst qcrypto.Address, id types.InstructionID) [32]byte {
	key := append([]byte("verify:rec:"), inst[:]...)
	key = append(key, id[:]...)
	return qcrypto.Hash32(key)
}

// VerifyAddress is where verify.deploy by `deployer` at tx nonce `nonce` in the
// block at `height` lands: SHA3(deployer ‖ decimal(nonce) ‖ u64be(height) ‖
// "verify"). Same shape as BridgeAddress and ExchangeAddress — the height is
// inside so instances on different tiers can never share an address — and
// the SDK derives it the same way (verifyAddress in addresses/index.ts).
func VerifyAddress(deployer qcrypto.Address, nonce, height uint64) qcrypto.Address {
	input := append(deployer[:], []byte(fmt.Sprintf("%d", nonce))...)
	input = append(input, u64be(height)...)
	input = append(input, []byte("verify")...)
	h := qcrypto.Hash32(input)
	var a qcrypto.Address
	copy(a[:], h[:])
	return a
}

// ── Wire shapes ──────────────────────────────────────────────────────────────

// verifyDeployParams: {"threshold": n, "operators": ["SQ:…", …]}
type verifyDeployParams struct {
	Threshold int      `json:"threshold"`
	Operators []string `json:"operators"`
}

// verifyAttestation is an operator's dual signature over
// types.VerifyAttestationDigest. Each field is hex or standard base64; the
// decoder settles which by the exact byte length the field must have.
type verifyAttestation struct {
	PubKey    string `json:"pubkey"`     // ML-DSA-65 public key, 1952 bytes
	SLHPubKey string `json:"slh_pubkey"` // SLH-DSA public key, 32 bytes
	SigMLDSA  string `json:"sig_mldsa"`  // 3309 bytes
	SigSLH    string `json:"sig_slh"`    // 7856 bytes
}

// verifyParams is the parameter shape of every instance action. `verify`
// names the instance; the rest is per action (see docs/modules/verify.md).
type verifyParams struct {
	Verify        string             `json:"verify"`
	Instruction   json.RawMessage    `json:"instruction,omitempty"`    // register
	InstructionID string             `json:"instruction_id,omitempty"` // attest, finalize, spend
	Attestation   *verifyAttestation `json:"attestation,omitempty"`    // attest
	Operator      string             `json:"operator,omitempty"`       // set_operator
	Active        *bool              `json:"active,omitempty"`         // set_operator (required)
	Threshold     int                `json:"threshold,omitempty"`      // set_threshold
}

// ── Actions ──────────────────────────────────────────────────────────────────

func verifyDeploy(stateDB *state.StateDB, ctx ModuleContext, raw json.RawMessage, receipt *types.Receipt) error {
	var p verifyDeployParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("verify deploy: %w", err)
	}
	ops := make([]qcrypto.Address, 0, len(p.Operators))
	seen := map[qcrypto.Address]bool{ctx.Caller: true}
	for i, s := range p.Operators {
		a, err := qcrypto.AddressFromString(s)
		if err != nil {
			return fmt.Errorf("verify deploy: operators[%d]: %w", i, err)
		}
		if a.IsZero() {
			return fmt.Errorf("verify deploy: operators[%d] is the zero address", i)
		}
		if seen[a] {
			return fmt.Errorf("verify deploy: operators[%d] %s is listed twice (the owner is an attestor already)", i, a)
		}
		seen[a] = true
		ops = append(ops, a)
	}
	if len(ops) > MaxVerifyOperators {
		return fmt.Errorf("verify deploy: %d operators exceeds the cap of %d", len(ops), MaxVerifyOperators)
	}
	if err := checkVerifyThreshold(p.Threshold, 1+len(ops)); err != nil {
		return fmt.Errorf("verify deploy: %w", err)
	}
	addr := VerifyAddress(ctx.Caller, ctx.Nonce, ctx.Height)
	if existing, _ := loadVerifyInstance(stateDB, addr); existing != nil {
		return fmt.Errorf("verify deploy: an instance already exists at %s", addr)
	}
	inst := verifyInstance{Owner: ctx.Caller, Threshold: p.Threshold, Operators: ops, Height: ctx.Height}
	if err := verifyPutJSON(stateDB, addr, verifyInstanceSlot(addr), inst); err != nil {
		return err
	}
	receipt.ContractAddress = &addr
	verifyLog(receipt, verifyDeployedEvent(addr, ctx.Caller, p.Threshold))
	return nil
}

// verifyRegister records a dual-signed instruction under an instance. The
// caller must be the instruction's signer or one of the instance's attestors:
// registration is fee-paid state, and an unrelated third party has no
// business creating it under somebody else's instance.
func verifyRegister(stateDB *state.StateDB, ctx ModuleContext, raw json.RawMessage, receipt *types.Receipt) error {
	var p verifyParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("verify register: %w", err)
	}
	instAddr, inst, err := loadVerifyInstanceByString(stateDB, p.Verify)
	if err != nil {
		return fmt.Errorf("verify register: %w", err)
	}
	if inst.Paused {
		return fmt.Errorf("verify register: instance %s is paused", instAddr)
	}
	if len(p.Instruction) == 0 {
		return fmt.Errorf("verify register: instruction is required")
	}
	var ins types.VerifyInstruction
	if err := json.Unmarshal(p.Instruction, &ins); err != nil {
		return fmt.Errorf("verify register: instruction: %w", err)
	}
	digest, signer, err := types.CheckVerifyInstruction(types.ChainID, &ins)
	if err != nil {
		return fmt.Errorf("verify register: %w", err)
	}
	if ctx.Caller != signer && !isVerifyAttestor(inst, ctx.Caller) {
		return fmt.Errorf("verify register: caller %s is neither the instruction signer %s nor an attestor of %s", ctx.Caller, signer, instAddr)
	}
	if err := validateVerifySettlement(&ins); err != nil {
		return fmt.Errorf("verify register: %w", err)
	}
	if existing := loadVerifyRecord(stateDB, instAddr, ins.InstructionID); existing != nil {
		return fmt.Errorf("verify register: instruction %s is already registered under %s", ins.InstructionID, instAddr)
	}
	rec := verifyRecord{Instruction: ins, Digest: digest, Signer: signer, RegisteredHeight: ctx.Height}
	if err := verifyPutJSON(stateDB, instAddr, verifyRecordSlot(instAddr, ins.InstructionID), rec); err != nil {
		return err
	}
	verifyLog(receipt, verifyRegisteredEvent(instAddr, ins.InstructionID, signer))
	return nil
}

// verifyAttest appends one operator's dual-signed attestation. The
// attestation is self-authenticating — it carries the operator's keys — so
// anyone may relay it; what it must be is bound to this instance and this
// instruction's digest, from an attestor not yet counted.
func verifyAttest(stateDB *state.StateDB, ctx ModuleContext, raw json.RawMessage, receipt *types.Receipt) error {
	var p verifyParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("verify attest: %w", err)
	}
	instAddr, inst, rec, err := loadVerifyInstanceAndRecord(stateDB, p.Verify, p.InstructionID)
	if err != nil {
		return fmt.Errorf("verify attest: %w", err)
	}
	if inst.Paused {
		return fmt.Errorf("verify attest: instance %s is paused", instAddr)
	}
	if rec.Spent {
		return fmt.Errorf("verify attest: instruction is already spent")
	}
	if rec.Finalized {
		return fmt.Errorf("verify attest: instruction is already finalized")
	}
	if p.Attestation == nil {
		return fmt.Errorf("verify attest: attestation is required")
	}
	a := p.Attestation
	pubRaw, err := decodeVerifyBytes("pubkey", a.PubKey, qcrypto.PublicKeySize)
	if err != nil {
		return fmt.Errorf("verify attest: %w", err)
	}
	pub, err := qcrypto.PublicKeyFromBytes(pubRaw)
	if err != nil {
		return fmt.Errorf("verify attest: pubkey: %w", err)
	}
	attestor := qcrypto.DeriveAddress(pub)
	if !isVerifyAttestor(inst, attestor) {
		return fmt.Errorf("verify attest: %s is not an attestor of %s", attestor, instAddr)
	}
	for _, prev := range rec.Attestors {
		if prev == attestor {
			return fmt.Errorf("verify attest: %s has already attested", attestor)
		}
	}
	msg, err := types.VerifyAttestationDigest(types.ChainID, instAddr, rec.Digest)
	if err != nil {
		return fmt.Errorf("verify attest: %w", err)
	}
	sigM, err := decodeVerifyBytes("sig_mldsa", a.SigMLDSA, qcrypto.SignatureSize)
	if err != nil {
		return fmt.Errorf("verify attest: %w", err)
	}
	if !qcrypto.Verify(pub, msg[:], qcrypto.CtxVerify, sigM) {
		return fmt.Errorf("verify attest: invalid ML-DSA-65 attestation signature")
	}
	slhRaw, err := decodeVerifyBytes("slh_pubkey", a.SLHPubKey, qcrypto.SLHPublicKeySize)
	if err != nil {
		return fmt.Errorf("verify attest: %w", err)
	}
	slhPub, err := qcrypto.SLHPublicKeyFromBytes(slhRaw)
	if err != nil {
		return fmt.Errorf("verify attest: %w", err)
	}
	sigS, err := decodeVerifyBytes("sig_slh", a.SigSLH, qcrypto.SLHSignatureSize)
	if err != nil {
		return fmt.Errorf("verify attest: %w", err)
	}
	if !qcrypto.VerifySLH(slhPub, msg[:], qcrypto.CtxVerify, sigS) {
		return fmt.Errorf("verify attest: invalid SLH-DSA attestation signature")
	}
	rec.Attestors = append(rec.Attestors, attestor)
	if err := verifyPutJSON(stateDB, instAddr, verifyRecordSlot(instAddr, rec.Instruction.InstructionID), rec); err != nil {
		return err
	}
	verifyLog(receipt, verifyAttestedEvent(instAddr, rec.Instruction.InstructionID, attestor, len(rec.Attestors)))
	return nil
}

// verifyFinalize records that threshold was reached, without moving value.
// Anyone may call it once the attestations are in.
func verifyFinalize(stateDB *state.StateDB, ctx ModuleContext, raw json.RawMessage, receipt *types.Receipt) error {
	var p verifyParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("verify finalize: %w", err)
	}
	instAddr, inst, rec, err := loadVerifyInstanceAndRecord(stateDB, p.Verify, p.InstructionID)
	if err != nil {
		return fmt.Errorf("verify finalize: %w", err)
	}
	if inst.Paused {
		return fmt.Errorf("verify finalize: instance %s is paused", instAddr)
	}
	if rec.Spent {
		return fmt.Errorf("verify finalize: instruction is already spent")
	}
	if rec.Finalized {
		return fmt.Errorf("verify finalize: instruction is already finalized")
	}
	if len(rec.Attestors) < inst.Threshold {
		return fmt.Errorf("verify finalize: %d attestations, need %d", len(rec.Attestors), inst.Threshold)
	}
	rec.Finalized = true
	rec.Height = ctx.Height
	if err := verifyPutJSON(stateDB, instAddr, verifyRecordSlot(instAddr, rec.Instruction.InstructionID), rec); err != nil {
		return err
	}
	verifyLog(receipt, verifyFinalizedEvent(instAddr, rec.Instruction.InstructionID))
	return nil
}

// verifySpend settles a finalized (or threshold-met) instruction exactly
// once. A value move needs a splendor* settlement hint and the caller to be
// `from`; finalize_only records the settlement and moves nothing.
func verifySpend(stateDB *state.StateDB, ctx ModuleContext, raw json.RawMessage, receipt *types.Receipt) error {
	var p verifyParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("verify spend: %w", err)
	}
	instAddr, inst, rec, err := loadVerifyInstanceAndRecord(stateDB, p.Verify, p.InstructionID)
	if err != nil {
		return fmt.Errorf("verify spend: %w", err)
	}
	if inst.Paused {
		return fmt.Errorf("verify spend: instance %s is paused", instAddr)
	}
	if rec.Spent {
		return fmt.Errorf("verify spend: instruction is already spent")
	}
	if !rec.Finalized && len(rec.Attestors) < inst.Threshold {
		return fmt.Errorf("verify spend: not finalized (%d attestations, need %d)", len(rec.Attestors), inst.Threshold)
	}
	action := settlementAction(&rec.Instruction)
	if action != types.SettleFinalizeOnly && !strings.HasPrefix(rec.Instruction.SettlementHint, "splendor") {
		return fmt.Errorf("verify spend: settlement_hint %q is not a splendor-qc hint; only finalize_only may settle here", rec.Instruction.SettlementHint)
	}
	if err := applyVerifySettlement(stateDB, ctx, &rec.Instruction); err != nil {
		return err
	}
	rec.Spent = true
	rec.Finalized = true
	rec.Height = ctx.Height
	if err := verifyPutJSON(stateDB, instAddr, verifyRecordSlot(instAddr, rec.Instruction.InstructionID), rec); err != nil {
		return err
	}
	verifyLog(receipt, verifySpentEvent(instAddr, rec.Instruction.InstructionID, action))
	return nil
}

func applyVerifySettlement(stateDB *state.StateDB, ctx ModuleContext, ins *types.VerifyInstruction) error {
	switch settlementAction(ins) {
	case types.SettleFinalizeOnly:
		return nil
	case types.SettleNativeTransfer:
		from, to, amt, err := verifyTransferFields(ins)
		if err != nil {
			return fmt.Errorf("verify spend: %w", err)
		}
		if from != ctx.Caller {
			return fmt.Errorf("verify spend: caller %s is not from %s", ctx.Caller, from)
		}
		if err := stateDB.Transfer(from, to, amt); err != nil {
			return fmt.Errorf("verify spend: %w", err)
		}
		return nil
	case types.SettleQRC20Transfer:
		from, to, amt, err := verifyTransferFields(ins)
		if err != nil {
			return fmt.Errorf("verify spend: %w", err)
		}
		if from != ctx.Caller {
			return fmt.Errorf("verify spend: caller %s is not from %s", ctx.Caller, from)
		}
		token, err := qcrypto.AddressFromString(ins.Token)
		if err != nil {
			return fmt.Errorf("verify spend: token: %w", err)
		}
		if err := NewQRC20Token(stateDB, token).Transfer(from, to, amt); err != nil {
			return fmt.Errorf("verify spend: %w", err)
		}
		return nil
	default:
		// validateVerifySettlement refused it at register; a record can only
		// hold this if the rules changed under it.
		return fmt.Errorf("verify spend: unknown settlement_action %q", ins.SettlementAction)
	}
}

func verifySetOperator(stateDB *state.StateDB, ctx ModuleContext, raw json.RawMessage, receipt *types.Receipt) error {
	var p verifyParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("verify set_operator: %w", err)
	}
	instAddr, inst, err := loadVerifyInstanceByString(stateDB, p.Verify)
	if err != nil {
		return fmt.Errorf("verify set_operator: %w", err)
	}
	if inst.Owner != ctx.Caller {
		return fmt.Errorf("verify set_operator: caller %s is not the owner of %s", ctx.Caller, instAddr)
	}
	if p.Active == nil {
		return fmt.Errorf("verify set_operator: active is required (true grants, false revokes)")
	}
	op, err := qcrypto.AddressFromString(p.Operator)
	if err != nil {
		return fmt.Errorf("verify set_operator: operator: %w", err)
	}
	if op.IsZero() {
		return fmt.Errorf("verify set_operator: operator is the zero address")
	}
	if op == inst.Owner {
		return fmt.Errorf("verify set_operator: the owner is an attestor already")
	}
	if *p.Active {
		if !isVerifyOperator(inst, op) {
			if len(inst.Operators) >= MaxVerifyOperators {
				return fmt.Errorf("verify set_operator: operator cap of %d reached", MaxVerifyOperators)
			}
			inst.Operators = append(inst.Operators, op)
		}
	} else {
		next := inst.Operators[:0]
		for _, a := range inst.Operators {
			if a != op {
				next = append(next, a)
			}
		}
		inst.Operators = next
		if err := checkVerifyThreshold(inst.Threshold, 1+len(inst.Operators)); err != nil {
			return fmt.Errorf("verify set_operator: revoking %s would leave %w; lower the threshold first", op, err)
		}
	}
	return verifyPutJSON(stateDB, instAddr, verifyInstanceSlot(instAddr), inst)
}

func verifySetThreshold(stateDB *state.StateDB, ctx ModuleContext, raw json.RawMessage, receipt *types.Receipt) error {
	var p verifyParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("verify set_threshold: %w", err)
	}
	instAddr, inst, err := loadVerifyInstanceByString(stateDB, p.Verify)
	if err != nil {
		return fmt.Errorf("verify set_threshold: %w", err)
	}
	if inst.Owner != ctx.Caller {
		return fmt.Errorf("verify set_threshold: caller %s is not the owner of %s", ctx.Caller, instAddr)
	}
	if err := checkVerifyThreshold(p.Threshold, 1+len(inst.Operators)); err != nil {
		return fmt.Errorf("verify set_threshold: %w", err)
	}
	inst.Threshold = p.Threshold
	return verifyPutJSON(stateDB, instAddr, verifyInstanceSlot(instAddr), inst)
}

// verifyPause / unpause. A paused instance refuses every state-advancing
// action — register, attest, finalize, spend — until unpaused; the owner-only
// configuration actions stay available so the owner can repair and resume.
func verifyPause(stateDB *state.StateDB, ctx ModuleContext, raw json.RawMessage, receipt *types.Receipt, paused bool) error {
	var p verifyParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("verify pause: %w", err)
	}
	instAddr, inst, err := loadVerifyInstanceByString(stateDB, p.Verify)
	if err != nil {
		return fmt.Errorf("verify pause: %w", err)
	}
	if inst.Owner != ctx.Caller {
		return fmt.Errorf("verify pause: caller %s is not the owner of %s", ctx.Caller, instAddr)
	}
	inst.Paused = paused
	return verifyPutJSON(stateDB, instAddr, verifyInstanceSlot(instAddr), inst)
}

// ── Reads ────────────────────────────────────────────────────────────────────

func verifyProofRead(stateDB *state.StateDB, raw json.RawMessage) (interface{}, error) {
	var p verifyParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("verify proof: %w", err)
	}
	instAddr, inst, rec, err := loadVerifyInstanceAndRecord(stateDB, p.Verify, p.InstructionID)
	if err != nil {
		return nil, fmt.Errorf("verify proof: %w", err)
	}
	attestors := make([]string, 0, len(rec.Attestors))
	for _, a := range rec.Attestors {
		attestors = append(attestors, a.String())
	}
	return map[string]interface{}{
		"module":            "verify",
		"action":            "proof",
		"instance":          instAddr.String(),
		"instruction_id":    rec.Instruction.InstructionID.Hex(),
		"digest":            hex.EncodeToString(rec.Digest[:]),
		"signer":            rec.Signer.String(),
		"instruction":       rec.Instruction,
		"settlement_action": settlementAction(&rec.Instruction),
		"attestors":         attestors,
		"attestations":      len(rec.Attestors),
		"threshold":         inst.Threshold,
		"threshold_met":     len(rec.Attestors) >= inst.Threshold,
		"finalized":         rec.Finalized,
		"spent":             rec.Spent,
		"registered_height": rec.RegisteredHeight,
		"height":            rec.Height,
	}, nil
}

func verifyInstanceRead(stateDB *state.StateDB, raw json.RawMessage) (interface{}, error) {
	var p verifyParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("verify instance: %w", err)
	}
	instAddr, inst, err := loadVerifyInstanceByString(stateDB, p.Verify)
	if err != nil {
		return nil, fmt.Errorf("verify instance: %w", err)
	}
	ops := make([]string, 0, len(inst.Operators))
	for _, a := range inst.Operators {
		ops = append(ops, a.String())
	}
	return map[string]interface{}{
		"module":    "verify",
		"action":    "instance",
		"address":   instAddr.String(),
		"owner":     inst.Owner.String(),
		"threshold": inst.Threshold,
		"operators": ops,
		"attestors": 1 + len(inst.Operators),
		"paused":    inst.Paused,
		"height":    inst.Height,
	}, nil
}

// ── Helpers ──────────────────────────────────────────────────────────────────

// verifyAtomic runs fn inside a StateDB snapshot and reverts it — and any logs
// fn appended to receipt — when fn errors. Same contract as Bridge.atomic.
func verifyAtomic(stateDB *state.StateDB, receipt *types.Receipt, fn func() error) error {
	snap := stateDB.Snapshot()
	logs := 0
	if receipt != nil {
		logs = len(receipt.Logs)
	}
	if err := fn(); err != nil {
		if rerr := stateDB.RevertToSnapshot(snap); rerr != nil {
			return hard(fmt.Errorf("verify: %w", rerr))
		}
		if receipt != nil {
			receipt.Logs = receipt.Logs[:logs]
		}
		return err
	}
	if err := stateDB.CommitSnapshot(snap); err != nil {
		return hard(fmt.Errorf("verify: %w", err))
	}
	return nil
}

func verifyLog(receipt *types.Receipt, l *types.Log) {
	if receipt != nil {
		receipt.AddLog(l)
	}
}

// checkVerifyThreshold: 1 ≤ threshold ≤ attestors, where attestors counts the
// owner plus the operators. A threshold nobody can reach is an instance
// nothing can ever settle on.
func checkVerifyThreshold(threshold, attestors int) error {
	if threshold < 1 {
		return fmt.Errorf("threshold %d below 1", threshold)
	}
	if threshold > attestors {
		return fmt.Errorf("threshold %d above the %d attestors (owner + operators)", threshold, attestors)
	}
	return nil
}

func settlementAction(ins *types.VerifyInstruction) string {
	if ins.SettlementAction == "" {
		return types.SettleFinalizeOnly
	}
	return ins.SettlementAction
}

// validateVerifySettlement checks, at register, that the settlement the
// instruction asks for is one the module can perform and that its parameters
// parse. Refusing here means a proof never records an instruction that could
// not have settled.
func validateVerifySettlement(ins *types.VerifyInstruction) error {
	switch settlementAction(ins) {
	case types.SettleFinalizeOnly:
		return nil
	case types.SettleNativeTransfer:
		_, _, _, err := verifyTransferFields(ins)
		return err
	case types.SettleQRC20Transfer:
		if _, _, _, err := verifyTransferFields(ins); err != nil {
			return err
		}
		if _, err := qcrypto.AddressFromString(ins.Token); err != nil {
			return fmt.Errorf("token: %w", err)
		}
		return nil
	case types.SettleX402:
		return fmt.Errorf("settlement_action %q is not an on-chain action: register with %q and settle the payment through POST /x402/settle", types.SettleX402, types.SettleFinalizeOnly)
	default:
		return fmt.Errorf("unknown settlement_action %q", ins.SettlementAction)
	}
}

func verifyTransferFields(ins *types.VerifyInstruction) (from, to qcrypto.Address, amt *big.Int, err error) {
	if from, err = qcrypto.AddressFromString(ins.From); err != nil {
		return from, to, nil, fmt.Errorf("from: %w", err)
	}
	if to, err = qcrypto.AddressFromString(ins.To); err != nil {
		return from, to, nil, fmt.Errorf("to: %w", err)
	}
	if to.IsZero() {
		return from, to, nil, fmt.Errorf("to is the zero address")
	}
	if amt, err = parseUint256(ins.Amount); err != nil {
		return from, to, nil, fmt.Errorf("amount: %w", err)
	}
	if amt.Sign() <= 0 {
		return from, to, nil, fmt.Errorf("amount must be positive")
	}
	return from, to, amt, nil
}

func isVerifyOperator(inst *verifyInstance, a qcrypto.Address) bool {
	for _, op := range inst.Operators {
		if op == a {
			return true
		}
	}
	return false
}

// isVerifyAttestor: the owner or any operator.
func isVerifyAttestor(inst *verifyInstance, a qcrypto.Address) bool {
	return inst.Owner == a || isVerifyOperator(inst, a)
}

// decodeVerifyBytes decodes a field that must be exactly `want` bytes, given
// as hex (optionally 0x-prefixed) or standard base64. The length settles the
// ambiguity: hex of `want` bytes is exactly 2*want characters, base64 of the
// same is about 4/3*want, and neither can decode to `want` bytes under the
// other reading.
func decodeVerifyBytes(field, s string, want int) ([]byte, error) {
	if s == "" {
		return nil, fmt.Errorf("%s is required", field)
	}
	h := strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
	if len(h) == 2*want {
		if b, err := hex.DecodeString(h); err == nil {
			return b, nil
		}
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("%s: not %d bytes of hex or base64", field, want)
	}
	if len(b) != want {
		return nil, fmt.Errorf("%s: %d bytes, want %d", field, len(b), want)
	}
	return b, nil
}

func loadVerifyInstance(stateDB *state.StateDB, addr qcrypto.Address) (*verifyInstance, error) {
	var inst verifyInstance
	if err := verifyGetJSON(stateDB, addr, verifyInstanceSlot(addr), &inst); err != nil {
		return nil, fmt.Errorf("instance %s not found", addr)
	}
	return &inst, nil
}

func loadVerifyInstanceByString(stateDB *state.StateDB, s string) (qcrypto.Address, *verifyInstance, error) {
	addr, err := qcrypto.AddressFromString(s)
	if err != nil {
		return qcrypto.Address{}, nil, fmt.Errorf("verify: %w", err)
	}
	inst, err := loadVerifyInstance(stateDB, addr)
	if err != nil {
		return addr, nil, err
	}
	return addr, inst, nil
}

func loadVerifyRecord(stateDB *state.StateDB, inst qcrypto.Address, id types.InstructionID) *verifyRecord {
	var rec verifyRecord
	if err := verifyGetJSON(stateDB, inst, verifyRecordSlot(inst, id), &rec); err != nil {
		return nil
	}
	if rec.Digest == ([32]byte{}) {
		return nil
	}
	return &rec
}

func loadVerifyInstanceAndRecord(stateDB *state.StateDB, verifyStr, idStr string) (qcrypto.Address, *verifyInstance, *verifyRecord, error) {
	instAddr, inst, err := loadVerifyInstanceByString(stateDB, verifyStr)
	if err != nil {
		return qcrypto.Address{}, nil, nil, err
	}
	id, err := types.ParseInstructionID(idStr)
	if err != nil {
		return instAddr, nil, nil, err
	}
	rec := loadVerifyRecord(stateDB, instAddr, id)
	if rec == nil {
		return instAddr, nil, nil, fmt.Errorf("instruction %s is not registered under %s", id, instAddr)
	}
	return instAddr, inst, rec, nil
}

func verifyPutJSON(stateDB *state.StateDB, contract qcrypto.Address, slot [32]byte, v interface{}) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return stateDB.SetStorageValue(contract, slot, b)
}

func verifyGetJSON(stateDB *state.StateDB, contract qcrypto.Address, slot [32]byte, v interface{}) error {
	b, err := stateDB.GetStorageValue(contract, slot)
	if err != nil || len(b) == 0 {
		return fmt.Errorf("not found")
	}
	return json.Unmarshal(b, v)
}

// ── Events ───────────────────────────────────────────────────────────────────

func verifyDeployedEvent(inst, owner qcrypto.Address, threshold int) *types.Log {
	return NewEvent(inst, "VerifyDeployed(sqaddress,sqaddress,uint256)").
		IndexAddr(inst).
		DataAddr(owner).
		DataUint256(big.NewInt(int64(threshold))).
		Build()
}

func verifyRegisteredEvent(inst qcrypto.Address, id types.InstructionID, signer qcrypto.Address) *types.Log {
	return NewEvent(inst, "VerifyRegistered(sqaddress,bytes32,sqaddress)").
		IndexAddr(inst).
		IndexHash([32]byte(id)).
		DataAddr(signer).
		Build()
}

func verifyAttestedEvent(inst qcrypto.Address, id types.InstructionID, attestor qcrypto.Address, count int) *types.Log {
	return NewEvent(inst, "VerifyAttested(sqaddress,bytes32,sqaddress,uint256)").
		IndexAddr(inst).
		IndexHash([32]byte(id)).
		DataAddr(attestor).
		DataUint256(big.NewInt(int64(count))).
		Build()
}

func verifyFinalizedEvent(inst qcrypto.Address, id types.InstructionID) *types.Log {
	return NewEvent(inst, "VerifyFinalized(sqaddress,bytes32)").
		IndexAddr(inst).
		IndexHash([32]byte(id)).
		Build()
}

func verifySpentEvent(inst qcrypto.Address, id types.InstructionID, action string) *types.Log {
	return NewEvent(inst, "VerifySpent(sqaddress,bytes32,string)").
		IndexAddr(inst).
		IndexHash([32]byte(id)).
		DataString(action).
		Build()
}
