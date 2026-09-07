package types

import (
	"encoding/hex"
	"fmt"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
)

const (
	// VerifyInstructionVersion is the only instruction version this build accepts.
	VerifyInstructionVersion = 1
	// MaxVerifyOriginBytes caps origin (chain / bank / api id).
	MaxVerifyOriginBytes = 128
	// MaxVerifyPurposeBytes caps the purpose string.
	MaxVerifyPurposeBytes = 64
	// MaxVerifyHintBytes caps settlement_hint.
	MaxVerifyHintBytes = 128
	// MaxVerifyPayloadBytes caps the opaque payload.
	MaxVerifyPayloadBytes = 4096
)

// VerifyInstruction is a native SQVL statement. It is not a transaction.
// Both ML-DSA-65 and SLH-DSA-SHA2-128s must verify under CtxVerify.
type VerifyInstruction struct {
	Version        int      `json:"version"`
	InstructionID  [32]byte `json:"instruction_id"`
	Purpose        string   `json:"purpose"`
	Origin         string   `json:"origin"`
	Payload        []byte   `json:"payload"`
	SettlementHint string   `json:"settlement_hint"`
	// SettlementAction is used when settling on Splendor QC:
	// native_transfer | qrc20_transfer | finalize_only | x402
	SettlementAction string `json:"settlement_action,omitempty"`
	From             string `json:"from,omitempty"`
	To               string `json:"to,omitempty"`
	Amount           string `json:"amount,omitempty"`
	Token            string `json:"token,omitempty"`

	MLDSAPubKey  []byte `json:"mldsa_pubkey"`
	MLDSASig     []byte `json:"sig_mldsa"`
	SLHPubKey    []byte `json:"slh_pubkey"`
	SLHSig       []byte `json:"sig_slh"`
}

var verifyDigestPrefix = []byte("sqvl:instruction:v1:")

// VerifyInstructionDigest is the message both schemes sign under CtxVerify.
//
//	"sqvl:instruction:v1:" ‖ u16be(len(chainID)) ‖ chainID
//	‖ instruction_id[32] ‖ u16be(len(purpose)) ‖ purpose
//	‖ u16be(len(origin)) ‖ origin ‖ u16be(len(hint)) ‖ hint
//	‖ u16be(len(action)) ‖ action ‖ u16be(len(payload)) ‖ payload
func VerifyInstructionDigest(chainID string, ins *VerifyInstruction) ([32]byte, error) {
	if ins == nil {
		return [32]byte{}, fmt.Errorf("verify instruction digest: nil instruction")
	}
	if err := ValidateChainID(chainID); err != nil {
		return [32]byte{}, fmt.Errorf("verify instruction digest: %w", err)
	}
	if err := validateVerifyFields(ins); err != nil {
		return [32]byte{}, err
	}
	msg := make([]byte, 0, 64+len(chainID)+len(ins.Purpose)+len(ins.Origin)+len(ins.SettlementHint)+len(ins.SettlementAction)+len(ins.Payload))
	msg = append(msg, verifyDigestPrefix...)
	msg = appendU16BE(msg, len(chainID))
	msg = append(msg, chainID...)
	msg = append(msg, ins.InstructionID[:]...)
	msg = appendU16BE(msg, len(ins.Purpose))
	msg = append(msg, ins.Purpose...)
	msg = appendU16BE(msg, len(ins.Origin))
	msg = append(msg, ins.Origin...)
	msg = appendU16BE(msg, len(ins.SettlementHint))
	msg = append(msg, ins.SettlementHint...)
	msg = appendU16BE(msg, len(ins.SettlementAction))
	msg = append(msg, ins.SettlementAction...)
	msg = appendU16BE(msg, len(ins.Payload))
	msg = append(msg, ins.Payload...)
	return qcrypto.Hash32(msg), nil
}

func validateVerifyFields(ins *VerifyInstruction) error {
	if ins.Version != VerifyInstructionVersion {
		return fmt.Errorf("verify instruction: version %d, want %d", ins.Version, VerifyInstructionVersion)
	}
	if ins.InstructionID == ([32]byte{}) {
		return fmt.Errorf("verify instruction: empty instruction_id")
	}
	if len(ins.Purpose) == 0 || len(ins.Purpose) > MaxVerifyPurposeBytes {
		return fmt.Errorf("verify instruction: purpose length %d", len(ins.Purpose))
	}
	if len(ins.Origin) == 0 || len(ins.Origin) > MaxVerifyOriginBytes {
		return fmt.Errorf("verify instruction: origin length %d", len(ins.Origin))
	}
	if len(ins.SettlementHint) == 0 || len(ins.SettlementHint) > MaxVerifyHintBytes {
		return fmt.Errorf("verify instruction: settlement_hint length %d", len(ins.SettlementHint))
	}
	if len(ins.Payload) > MaxVerifyPayloadBytes {
		return fmt.Errorf("verify instruction: payload %d bytes, cap %d", len(ins.Payload), MaxVerifyPayloadBytes)
	}
	if len(ins.SettlementAction) > MaxVerifyPurposeBytes {
		return fmt.Errorf("verify instruction: settlement_action too long")
	}
	return nil
}

// CheckVerifyInstruction verifies both PQ signatures. No state write.
func CheckVerifyInstruction(chainID string, ins *VerifyInstruction) (digest [32]byte, mldsaAddr qcrypto.Address, err error) {
	if ins == nil {
		return [32]byte{}, qcrypto.Address{}, fmt.Errorf("nil instruction")
	}
	digest, err = VerifyInstructionDigest(chainID, ins)
	if err != nil {
		return [32]byte{}, qcrypto.Address{}, err
	}
	mldsaPub, err := qcrypto.PublicKeyFromBytes(ins.MLDSAPubKey)
	if err != nil {
		return digest, qcrypto.Address{}, fmt.Errorf("mldsa pubkey: %w", err)
	}
	if !qcrypto.Verify(mldsaPub, digest[:], qcrypto.CtxVerify, ins.MLDSASig) {
		return digest, qcrypto.Address{}, fmt.Errorf("invalid ML-DSA-65 instruction signature")
	}
	slhPub, err := qcrypto.SLHPublicKeyFromBytes(ins.SLHPubKey)
	if err != nil {
		return digest, qcrypto.Address{}, fmt.Errorf("slh pubkey: %w", err)
	}
	if !qcrypto.VerifySLH(slhPub, digest[:], qcrypto.CtxVerify, ins.SLHSig) {
		return digest, qcrypto.Address{}, fmt.Errorf("invalid SLH-DSA instruction signature")
	}
	return digest, qcrypto.DeriveAddress(mldsaPub), nil
}

// SignVerifyInstruction fills both signatures. slhPriv may be nil only in tests
// that are not exercising dual-sig; production callers must pass both keys.
func SignVerifyInstruction(chainID string, ins *VerifyInstruction, mldsa *qcrypto.PrivateKey, slh *qcrypto.SLHPrivateKey) error {
	if ins == nil || mldsa == nil || slh == nil {
		return fmt.Errorf("sign verify instruction: missing key")
	}
	digest, err := VerifyInstructionDigest(chainID, ins)
	if err != nil {
		return err
	}
	ins.MLDSAPubKey = mldsa.Public().Bytes()
	ins.MLDSASig, err = qcrypto.Sign(mldsa, digest[:], qcrypto.CtxVerify)
	if err != nil {
		return err
	}
	ins.SLHPubKey = slh.Public().Bytes()
	ins.SLHSig, err = qcrypto.SignSLH(slh, digest[:], qcrypto.CtxVerify)
	return err
}

func appendU16BE(b []byte, n int) []byte {
	return append(b, byte(n>>8), byte(n))
}

// InstructionIDHex returns the 64-char hex of the instruction id (no 0x).
func InstructionIDHex(id [32]byte) string {
	return hex.EncodeToString(id[:])
}
