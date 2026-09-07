package types

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
)

// SQVL — the native verify / settlement layer.
//
// A VerifyInstruction is a statement an external party (a bank, another
// chain's bridge, a payments app) signs with BOTH ML-DSA-65 and SLH-DSA under
// CtxVerify. It is not a transaction: it carries no nonce and no fee, and the
// chain never applies it on its own. The `verify` module (vm/verify.go)
// records it, collects operator attestations, and either settles it here or
// leaves a proof other books can accept.

const (
	// VerifyInstructionVersion is the only instruction version this build accepts.
	VerifyInstructionVersion = 1
	// MaxVerifyOriginBytes caps origin (chain / bank / api id).
	MaxVerifyOriginBytes = 128
	// MaxVerifyPurposeBytes caps the purpose string and the settlement action.
	MaxVerifyPurposeBytes = 64
	// MaxVerifyHintBytes caps settlement_hint.
	MaxVerifyHintBytes = 128
	// MaxVerifyPayloadBytes caps the opaque payload.
	MaxVerifyPayloadBytes = 4096
)

// Settlement actions the verify module understands. Anything else is refused
// at register, not silently ignored at spend.
const (
	// SettleFinalizeOnly records the proof and moves nothing. Also what an
	// empty settlement_action means.
	SettleFinalizeOnly = "finalize_only"
	// SettleNativeTransfer moves `amount` of native SPLD from `from` to `to`
	// when `from` itself calls spend.
	SettleNativeTransfer = "native_transfer"
	// SettleQRC20Transfer moves `amount` of the QRC-20 at `token` the same way.
	SettleQRC20Transfer = "qrc20_transfer"
	// SettleX402 names the HTTP payment flow. It is NOT an on-chain action:
	// the module refuses it at register, because a spend that moved nothing
	// while marking the instruction settled would be a lie in the proof.
	SettleX402 = "x402"
)

// InstructionID identifies one VerifyInstruction inside one verify instance.
//
// On the wire it is a 64-character hex string (a 0x prefix is accepted on
// input, never produced). Go's default JSON encoding of a [32]byte is an
// array of 32 integers, which no client speaks and the SDK never did — the
// TypeScript type declared a string from day one — so the codec is explicit.
type InstructionID [32]byte

// ParseInstructionID decodes 64 hex characters (optionally 0x-prefixed).
func ParseInstructionID(s string) (InstructionID, error) {
	var id InstructionID
	s = strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
	if len(s) != 64 {
		return id, fmt.Errorf("instruction_id: want 64 hex characters, got %d", len(s))
	}
	raw, err := hex.DecodeString(s)
	if err != nil {
		return id, fmt.Errorf("instruction_id: %w", err)
	}
	copy(id[:], raw)
	return id, nil
}

// Hex is the wire form: 64 lowercase hex characters, no prefix.
func (id InstructionID) Hex() string { return hex.EncodeToString(id[:]) }

// String is Hex.
func (id InstructionID) String() string { return id.Hex() }

// IsZero reports an all-zero id, which is never a valid one.
func (id InstructionID) IsZero() bool { return id == InstructionID{} }

// MarshalText encodes the id as hex.
func (id InstructionID) MarshalText() ([]byte, error) { return []byte(id.Hex()), nil }

// UnmarshalText decodes hex.
func (id *InstructionID) UnmarshalText(b []byte) error {
	parsed, err := ParseInstructionID(string(b))
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}

// MarshalJSON encodes the id as a JSON hex string.
func (id InstructionID) MarshalJSON() ([]byte, error) { return json.Marshal(id.Hex()) }

// UnmarshalJSON accepts only a JSON hex string. An array of integers — what
// an unannotated [32]byte would have produced — is refused, so no client can
// come to depend on the accidental encoding.
func (id *InstructionID) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || b[0] != '"' {
		return fmt.Errorf("instruction_id: must be a JSON string of 64 hex characters")
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("instruction_id: %w", err)
	}
	return id.UnmarshalText([]byte(s))
}

// VerifyInstruction is a native SQVL statement. It is not a transaction.
// Both ML-DSA-65 and SLH-DSA-SHA2-128s must verify under CtxVerify.
//
// Byte fields (payload, the two public keys, the two signatures) are base64
// on the wire — Go's encoding of []byte — and the SDK type says so.
type VerifyInstruction struct {
	Version        int           `json:"version"`
	InstructionID  InstructionID `json:"instruction_id"`
	Purpose        string        `json:"purpose"`
	Origin         string        `json:"origin"`
	Payload        []byte        `json:"payload"`
	SettlementHint string        `json:"settlement_hint"`
	// SettlementAction is used when settling on Splendor QC: one of the
	// Settle* constants above (empty means SettleFinalizeOnly).
	SettlementAction string `json:"settlement_action,omitempty"`
	From             string `json:"from,omitempty"`
	To               string `json:"to,omitempty"`
	Amount           string `json:"amount,omitempty"`
	Token            string `json:"token,omitempty"`

	MLDSAPubKey []byte `json:"mldsa_pubkey"`
	MLDSASig    []byte `json:"sig_mldsa"`
	SLHPubKey   []byte `json:"slh_pubkey"`
	SLHSig      []byte `json:"sig_slh"`
}

var (
	verifyDigestPrefix = []byte("sqvl:instruction:v1:")
	verifyAttestPrefix = []byte("sqvl:attest:v1:")
)

// VerifyInstructionDigest is the message both schemes sign under CtxVerify.
//
//	"sqvl:instruction:v1:" ‖ u16be(len(chainID)) ‖ chainID
//	‖ instruction_id[32] ‖ u16be(len(purpose)) ‖ purpose
//	‖ u16be(len(origin)) ‖ origin ‖ u16be(len(hint)) ‖ hint
//	‖ u16be(len(action)) ‖ action ‖ u16be(len(payload)) ‖ payload
//
// from / to / amount / token are deliberately NOT part of the digest: they
// are settlement parameters the module checks at spend, and folding them in
// would make a proof-only instruction (which has none) hash differently from
// the same statement settled here.
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

// VerifyAttestationDigest is what an operator signs (both schemes, CtxVerify)
// to attest one instruction inside one instance:
//
//	"sqvl:attest:v1:" ‖ u16be(len(chainID)) ‖ chainID ‖ instance[32] ‖ digest[32]
//
// The instance is inside so an attestation given to one instance cannot be
// replayed to reach threshold on another that happens to list the same
// operator and the same instruction.
func VerifyAttestationDigest(chainID string, instance qcrypto.Address, digest [32]byte) ([32]byte, error) {
	if err := ValidateChainID(chainID); err != nil {
		return [32]byte{}, fmt.Errorf("verify attestation digest: %w", err)
	}
	if instance.IsZero() {
		return [32]byte{}, fmt.Errorf("verify attestation digest: zero instance address")
	}
	msg := make([]byte, 0, len(verifyAttestPrefix)+2+len(chainID)+64)
	msg = append(msg, verifyAttestPrefix...)
	msg = appendU16BE(msg, len(chainID))
	msg = append(msg, chainID...)
	msg = append(msg, instance[:]...)
	msg = append(msg, digest[:]...)
	return qcrypto.Hash32(msg), nil
}

func validateVerifyFields(ins *VerifyInstruction) error {
	if ins.Version != VerifyInstructionVersion {
		return fmt.Errorf("verify instruction: version %d, want %d", ins.Version, VerifyInstructionVersion)
	}
	if ins.InstructionID.IsZero() {
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

// CheckVerifyInstruction verifies both PQ signatures. No state write. On
// success it returns the digest and the address of the ML-DSA key — the
// instruction's signer, which on this chain is also an account address.
func CheckVerifyInstruction(chainID string, ins *VerifyInstruction) (digest [32]byte, signer qcrypto.Address, err error) {
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

// SignVerifyInstruction fills both public keys and both signatures. Both keys
// are required: an instruction with one signature is not an SQVL instruction.
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

// SignVerifyAttestation produces an operator's two signatures over
// VerifyAttestationDigest(chainID, instance, digest).
func SignVerifyAttestation(chainID string, instance qcrypto.Address, digest [32]byte, mldsa *qcrypto.PrivateKey, slh *qcrypto.SLHPrivateKey) (mldsaSig, slhSig []byte, err error) {
	if mldsa == nil || slh == nil {
		return nil, nil, fmt.Errorf("sign verify attestation: missing key")
	}
	msg, err := VerifyAttestationDigest(chainID, instance, digest)
	if err != nil {
		return nil, nil, err
	}
	mldsaSig, err = qcrypto.Sign(mldsa, msg[:], qcrypto.CtxVerify)
	if err != nil {
		return nil, nil, err
	}
	slhSig, err = qcrypto.SignSLH(slh, msg[:], qcrypto.CtxVerify)
	if err != nil {
		return nil, nil, err
	}
	return mldsaSig, slhSig, nil
}

func appendU16BE(b []byte, n int) []byte {
	return append(b, byte(n>>8), byte(n))
}
