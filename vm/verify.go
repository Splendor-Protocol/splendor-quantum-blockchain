package vm

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

const MaxVerifyOperators = 16

type verifyInstance struct {
	Address   qcrypto.Address   `json:"address"`
	Owner     qcrypto.Address   `json:"owner"`
	Threshold int               `json:"threshold"`
	Operators []qcrypto.Address `json:"operators"`
	Paused    bool              `json:"paused"`
}

type verifyRecord struct {
	Instruction types.VerifyInstruction `json:"instruction"`
	Digest      [32]byte                `json:"digest"`
	Attestors   []qcrypto.Address       `json:"attestors"`
	Finalized   bool                    `json:"finalized"`
	Spent       bool                    `json:"spent"`
	TxHash      [32]byte                `json:"tx_hash,omitempty"`
	Height      uint64                  `json:"height,omitempty"`
}

func verifyInstanceSlot(addr qcrypto.Address) [32]byte {
	return qcrypto.Hash32(append([]byte("verify:inst:"), addr[:]...))
}

func verifyRecordSlot(inst qcrypto.Address, id [32]byte) [32]byte {
	key := append([]byte("verify:rec:"), inst[:]...)
	key = append(key, id[:]...)
	return qcrypto.Hash32(key)
}

func verifyDeployAddr(deployer qcrypto.Address, nonce, height uint64) qcrypto.Address {
	h := qcrypto.Hash32(append(append(deployer[:], []byte(fmt.Sprintf("%d", nonce))...), []byte(fmt.Sprintf("%d|verify", height))...))
	var a qcrypto.Address
	copy(a[:], h[:])
	return a
}

func dispatchVerify(stateDB *state.StateDB, ctx ModuleContext, action string, raw json.RawMessage, receipt *types.Receipt) error {
	if !ctx.Forks.Active(forks.VerifyV1, ctx.Height) {
		return hard(fmt.Errorf("verify: %w", ErrForkInactive))
	}
	switch action {
	case "deploy":
		return verifyDeploy(stateDB, ctx, raw, receipt)
	case "register":
		return verifyRegister(stateDB, ctx, raw, receipt)
	case "attest":
		return verifyAttest(stateDB, ctx, raw, receipt)
	case "finalize":
		return verifyFinalize(stateDB, ctx, raw, receipt)
	case "spend":
		return verifySpend(stateDB, ctx, raw, receipt)
	case "set_operator":
		return verifySetOperator(stateDB, ctx, raw)
	case "set_threshold":
		return verifySetThreshold(stateDB, ctx, raw)
	case "pause":
		return verifyPause(stateDB, ctx, raw, true)
	case "unpause":
		return verifyPause(stateDB, ctx, raw, false)
	default:
		return hard(fmt.Errorf("module call: unknown action %q", action))
	}
}

type verifyDeployParams struct {
	Threshold int      `json:"threshold"`
	Operators []string `json:"operators"`
}

func verifyDeploy(stateDB *state.StateDB, ctx ModuleContext, raw json.RawMessage, receipt *types.Receipt) error {
	var p verifyDeployParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("verify deploy: %w", err)
	}
	if p.Threshold < 1 {
		return fmt.Errorf("verify deploy: threshold must be >= 1")
	}
	ops := make([]qcrypto.Address, 0, len(p.Operators))
	for _, s := range p.Operators {
		a, err := qcrypto.AddressFromString(s)
		if err != nil {
			return fmt.Errorf("verify deploy: operator: %w", err)
		}
		ops = append(ops, a)
	}
	if len(ops) > MaxVerifyOperators {
		return fmt.Errorf("verify deploy: too many operators")
	}
	if p.Threshold > len(ops) && len(ops) > 0 {
		return fmt.Errorf("verify deploy: threshold > operators")
	}
	if p.Threshold < 1 {
		p.Threshold = 1
	}
	addr := verifyDeployAddr(ctx.Caller, ctx.Nonce, ctx.Height)
	inst := verifyInstance{Address: addr, Owner: ctx.Caller, Threshold: p.Threshold, Operators: ops}
	if err := putJSON(stateDB, addr, verifyInstanceSlot(addr), inst); err != nil {
		return err
	}
	receipt.ContractAddress = &addr
	return nil
}

type verifyInstParams struct {
	Verify           string              `json:"verify"`
	Instruction      json.RawMessage     `json:"instruction"`
	Attestations     []verifyAttestation `json:"attestations"`
	SettlementAction string              `json:"settlement_action,omitempty"`
}

type verifyAttestation struct {
	PubKey   string `json:"pubkey"`
	SLHPub   string `json:"slh_pubkey"`
	SigMLDSA string `json:"sig_mldsa"`
	SigSLH   string `json:"sig_slh"`
}

func loadInstance(stateDB *state.StateDB, addr qcrypto.Address) (*verifyInstance, error) {
	var inst verifyInstance
	if err := getJSON(stateDB, addr, verifyInstanceSlot(addr), &inst); err != nil {
		return nil, fmt.Errorf("verify: instance: %w", err)
	}
	if inst.Address.IsZero() {
		inst.Address = addr
	}
	return &inst, nil
}

func verifyRegister(stateDB *state.StateDB, ctx ModuleContext, raw json.RawMessage, receipt *types.Receipt) error {
	var p verifyInstParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("verify register: %w", err)
	}
	instAddr, err := qcrypto.AddressFromString(p.Verify)
	if err != nil {
		return fmt.Errorf("verify register: instance: %w", err)
	}
	inst, err := loadInstance(stateDB, instAddr)
	if err != nil {
		return err
	}
	if inst.Paused {
		return fmt.Errorf("verify: paused")
	}
	var ins types.VerifyInstruction
	if err := json.Unmarshal(p.Instruction, &ins); err != nil {
		return fmt.Errorf("verify register: instruction: %w", err)
	}
	digest, _, err := types.CheckVerifyInstruction(types.ChainID, &ins)
	if err != nil {
		return fmt.Errorf("verify register: %w", err)
	}
	existing := loadRecord(stateDB, instAddr, ins.InstructionID)
	if existing != nil {
		return fmt.Errorf("verify register: instruction already registered")
	}
	rec := verifyRecord{Instruction: ins, Digest: digest}
	return putJSON(stateDB, instAddr, verifyRecordSlot(instAddr, ins.InstructionID), rec)
}

func verifyAttest(stateDB *state.StateDB, ctx ModuleContext, raw json.RawMessage, receipt *types.Receipt) error {
	var p struct {
		Verify        string            `json:"verify"`
		InstructionID string            `json:"instruction_id"`
		Attestation   verifyAttestation `json:"attestation"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("verify attest: %w", err)
	}
	instAddr, rec, err := loadInstRec(stateDB, p.Verify, p.InstructionID)
	if err != nil {
		return err
	}
	inst, err := loadInstance(stateDB, instAddr)
	if err != nil {
		return err
	}
	if rec.Spent {
		return fmt.Errorf("verify attest: already spent")
	}
	mldsaRaw, err := b64orHex(p.Attestation.PubKey)
	if err != nil {
		return fmt.Errorf("verify attest: pubkey: %w", err)
	}
	pub, err := qcrypto.PublicKeyFromBytes(mldsaRaw)
	if err != nil {
		return fmt.Errorf("verify attest: pubkey: %w", err)
	}
	addr := qcrypto.DeriveAddress(pub)
	if !isOperator(inst, addr) {
		return fmt.Errorf("verify attest: not an operator")
	}
	for _, a := range rec.Attestors {
		if a == addr {
			return fmt.Errorf("verify attest: already attested")
		}
	}
	sigM, err := b64orHex(p.Attestation.SigMLDSA)
	if err != nil {
		return err
	}
	if !qcrypto.Verify(pub, rec.Digest[:], qcrypto.CtxVerify, sigM) {
		return fmt.Errorf("verify attest: invalid ML-DSA signature")
	}
	slhRaw, err := b64orHex(p.Attestation.SLHPub)
	if err != nil {
		return err
	}
	slhPub, err := qcrypto.SLHPublicKeyFromBytes(slhRaw)
	if err != nil {
		return err
	}
	sigS, err := b64orHex(p.Attestation.SigSLH)
	if err != nil {
		return err
	}
	if !qcrypto.VerifySLH(slhPub, rec.Digest[:], qcrypto.CtxVerify, sigS) {
		return fmt.Errorf("verify attest: invalid SLH-DSA signature")
	}
	rec.Attestors = append(rec.Attestors, addr)
	return putJSON(stateDB, instAddr, verifyRecordSlot(instAddr, rec.Instruction.InstructionID), rec)
}

func verifyFinalize(stateDB *state.StateDB, ctx ModuleContext, raw json.RawMessage, receipt *types.Receipt) error {
	var p struct {
		Verify        string `json:"verify"`
		InstructionID string `json:"instruction_id"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("verify finalize: %w", err)
	}
	instAddr, rec, err := loadInstRec(stateDB, p.Verify, p.InstructionID)
	if err != nil {
		return err
	}
	inst, err := loadInstance(stateDB, instAddr)
	if err != nil {
		return err
	}
	if rec.Spent {
		return fmt.Errorf("verify finalize: already spent")
	}
	if len(rec.Attestors) < inst.Threshold {
		return fmt.Errorf("verify finalize: %d attestations, need %d", len(rec.Attestors), inst.Threshold)
	}
	rec.Finalized = true
	rec.Height = ctx.Height
	return putJSON(stateDB, instAddr, verifyRecordSlot(instAddr, rec.Instruction.InstructionID), rec)
}

func verifySpend(stateDB *state.StateDB, ctx ModuleContext, raw json.RawMessage, receipt *types.Receipt) error {
	var p struct {
		Verify        string `json:"verify"`
		InstructionID string `json:"instruction_id"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("verify spend: %w", err)
	}
	instAddr, rec, err := loadInstRec(stateDB, p.Verify, p.InstructionID)
	if err != nil {
		return err
	}
	inst, err := loadInstance(stateDB, instAddr)
	if err != nil {
		return err
	}
	if rec.Spent {
		return fmt.Errorf("verify spend: already spent")
	}
	if !rec.Finalized && len(rec.Attestors) < inst.Threshold {
		return fmt.Errorf("verify spend: not finalized")
	}
	hint := rec.Instruction.SettlementHint
	if hint != "splendor-qc" && hint != "" && !strings.HasPrefix(hint, "splendor") {
		// External settlement: mark spent as a proof record only if they
		// explicitly asked to spend-on-chain; otherwise refuse value move.
		if rec.Instruction.SettlementAction != "finalize_only" && rec.Instruction.SettlementAction != "" {
			return fmt.Errorf("verify spend: settlement_hint %q is not splendor-qc", hint)
		}
	}
	if err := applyVerifySettlement(stateDB, ctx, &rec.Instruction); err != nil {
		return err
	}
	rec.Spent = true
	rec.Finalized = true
	rec.Height = ctx.Height
	return putJSON(stateDB, instAddr, verifyRecordSlot(instAddr, rec.Instruction.InstructionID), rec)
}

func applyVerifySettlement(stateDB *state.StateDB, ctx ModuleContext, ins *types.VerifyInstruction) error {
	action := ins.SettlementAction
	if action == "" || action == "finalize_only" {
		return nil
	}
	switch action {
	case "native_transfer":
		from, err := qcrypto.AddressFromString(ins.From)
		if err != nil {
			return fmt.Errorf("verify settle: from: %w", err)
		}
		if from != ctx.Caller {
			return fmt.Errorf("verify settle: caller must be from")
		}
		to, err := qcrypto.AddressFromString(ins.To)
		if err != nil {
			return fmt.Errorf("verify settle: to: %w", err)
		}
		amt, ok := new(big.Int).SetString(ins.Amount, 10)
		if !ok || amt.Sign() <= 0 {
			return fmt.Errorf("verify settle: bad amount")
		}
		if err := stateDB.Transfer(from, to, amt); err != nil {
			return fmt.Errorf("verify settle: %w", err)
		}
		return nil
	case "qrc20_transfer":
		token, err := qcrypto.AddressFromString(ins.Token)
		if err != nil {
			return fmt.Errorf("verify settle: token: %w", err)
		}
		from, err := qcrypto.AddressFromString(ins.From)
		if err != nil {
			return err
		}
		if from != ctx.Caller {
			return fmt.Errorf("verify settle: caller must be from")
		}
		to, err := qcrypto.AddressFromString(ins.To)
		if err != nil {
			return err
		}
		amt, ok := new(big.Int).SetString(ins.Amount, 10)
		if !ok || amt.Sign() <= 0 {
			return fmt.Errorf("verify settle: bad amount")
		}
		tok := NewQRC20Token(stateDB, token)
		return tok.Transfer(from, to, amt)
	case "x402":
		return nil
	default:
		return fmt.Errorf("verify settle: unknown action %q", action)
	}
}

func verifySetOperator(stateDB *state.StateDB, ctx ModuleContext, raw json.RawMessage) error {
	var p struct {
		Verify   string `json:"verify"`
		Operator string `json:"operator"`
		Active   bool   `json:"active"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return err
	}
	addr, err := qcrypto.AddressFromString(p.Verify)
	if err != nil {
		return err
	}
	inst, err := loadInstance(stateDB, addr)
	if err != nil {
		return err
	}
	if inst.Owner != ctx.Caller {
		return fmt.Errorf("verify: not owner")
	}
	op, err := qcrypto.AddressFromString(p.Operator)
	if err != nil {
		return err
	}
	if p.Active {
		if !isOperator(inst, op) {
			if len(inst.Operators) >= MaxVerifyOperators {
				return fmt.Errorf("verify: operator cap")
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
	}
	return putJSON(stateDB, addr, verifyInstanceSlot(addr), inst)
}

func verifySetThreshold(stateDB *state.StateDB, ctx ModuleContext, raw json.RawMessage) error {
	var p struct {
		Verify    string `json:"verify"`
		Threshold int    `json:"threshold"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return err
	}
	addr, err := qcrypto.AddressFromString(p.Verify)
	if err != nil {
		return err
	}
	inst, err := loadInstance(stateDB, addr)
	if err != nil {
		return err
	}
	if inst.Owner != ctx.Caller {
		return fmt.Errorf("verify: not owner")
	}
	if p.Threshold < 1 || p.Threshold > MaxVerifyOperators {
		return fmt.Errorf("verify: bad threshold")
	}
	inst.Threshold = p.Threshold
	return putJSON(stateDB, addr, verifyInstanceSlot(addr), inst)
}

func verifyPause(stateDB *state.StateDB, ctx ModuleContext, raw json.RawMessage, paused bool) error {
	var p struct {
		Verify string `json:"verify"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return err
	}
	addr, err := qcrypto.AddressFromString(p.Verify)
	if err != nil {
		return err
	}
	inst, err := loadInstance(stateDB, addr)
	if err != nil {
		return err
	}
	if inst.Owner != ctx.Caller {
		return fmt.Errorf("verify: not owner")
	}
	inst.Paused = paused
	return putJSON(stateDB, addr, verifyInstanceSlot(addr), inst)
}

func dispatchVerifyRead(stateDB *state.StateDB, action string, raw json.RawMessage) (interface{}, error) {
	switch action {
	case "proof":
		var p struct {
			Verify        string `json:"verify"`
			InstructionID string `json:"instruction_id"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		instAddr, rec, err := loadInstRec(stateDB, p.Verify, p.InstructionID)
		if err != nil {
			return nil, err
		}
		inst, _ := loadInstance(stateDB, instAddr)
		threshold := 1
		if inst != nil {
			threshold = inst.Threshold
		}
		return map[string]interface{}{
			"instruction":   rec.Instruction,
			"digest":        hex.EncodeToString(rec.Digest[:]),
			"attestors":     rec.Attestors,
			"threshold":     threshold,
			"threshold_met": inst != nil && len(rec.Attestors) >= inst.Threshold,
			"finalized":     rec.Finalized,
			"spent":         rec.Spent,
			"height":        rec.Height,
		}, nil
	case "instance":
		var p struct {
			Verify string `json:"verify"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		addr, err := qcrypto.AddressFromString(p.Verify)
		if err != nil {
			return nil, err
		}
		return loadInstance(stateDB, addr)
	default:
		return nil, fmt.Errorf("verify read: unknown action %q", action)
	}
}

func loadRecord(stateDB *state.StateDB, inst qcrypto.Address, id [32]byte) *verifyRecord {
	var rec verifyRecord
	if err := getJSON(stateDB, inst, verifyRecordSlot(inst, id), &rec); err != nil {
		return nil
	}
	if rec.Digest == ([32]byte{}) {
		return nil
	}
	return &rec
}

func loadInstRec(stateDB *state.StateDB, verifyStr, idHex string) (qcrypto.Address, *verifyRecord, error) {
	instAddr, err := qcrypto.AddressFromString(verifyStr)
	if err != nil {
		return qcrypto.Address{}, nil, err
	}
	raw, err := hex.DecodeString(idHex)
	if err != nil || len(raw) != 32 {
		return qcrypto.Address{}, nil, fmt.Errorf("verify: instruction_id")
	}
	var id [32]byte
	copy(id[:], raw)
	rec := loadRecord(stateDB, instAddr, id)
	if rec == nil {
		return instAddr, nil, fmt.Errorf("verify: instruction not found")
	}
	return instAddr, rec, nil
}

func isOperator(inst *verifyInstance, addr qcrypto.Address) bool {
	if inst.Owner == addr {
		return true
	}
	for _, a := range inst.Operators {
		if a == addr {
			return true
		}
	}
	return false
}

func putJSON(stateDB *state.StateDB, contract qcrypto.Address, slot [32]byte, v interface{}) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return stateDB.SetStorageValue(contract, slot, b)
}

func getJSON(stateDB *state.StateDB, contract qcrypto.Address, slot [32]byte, v interface{}) error {
	b, err := stateDB.GetStorageValue(contract, slot)
	if err != nil || len(b) == 0 {
		return fmt.Errorf("not found")
	}
	return json.Unmarshal(b, v)
}

func b64orHex(s string) ([]byte, error) {
	if s == "" {
		return nil, fmt.Errorf("empty encoding")
	}
	if b, err := hex.DecodeString(s); err == nil {
		return b, nil
	}
	// std encoding/base64
	return base64.StdEncoding.DecodeString(s)
}
