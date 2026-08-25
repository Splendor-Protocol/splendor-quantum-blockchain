// Package signer provides deterministic ML-DSA-65 key derivation and
// wire-compatible transaction signing for the Splendor QC bridge SDK.
//
// It reuses core/crypto and core/types so marshaled JSON matches exactly
// what the Go node expects (Address as [32]byte JSON arrays, *big.Int numbers).
package signer

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"time"

	mldsa65 "github.com/cloudflare/circl/sign/mldsa/mldsa65"
	"golang.org/x/crypto/sha3"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// Domain message used for EVM personal_sign → PQ seed derivation.
// Keep in sync with the TypeScript SDK (sdk/bridge/src/derive.ts).
const DerivationDomainMessage = "Splendor Quantum Chain\nKey derivation v1\nchain: splendor-quantum-1"

// SeedSize is the ML-DSA-65 seed length (32 bytes).
const SeedSize = mldsa65.SeedSize

// DerivedWallet holds a ML-DSA-65 keypair derived from a seed.
type DerivedWallet struct {
	Address   qcrypto.Address
	PublicKey []byte
	Private   *qcrypto.PrivateKey
	Public    *qcrypto.PublicKey
}

// SeedFromEVMSignature derives a 32-byte ML-DSA-65 seed from an EVM personal_sign signature.
// seed = SHA3-256(signatureBytes)
func SeedFromEVMSignature(sig []byte) [SeedSize]byte {
	h := sha3.Sum256(sig)
	var seed [SeedSize]byte
	copy(seed[:], h[:])
	return seed
}

// SeedFromHex parses a 32-byte hex seed (with or without 0x prefix).
func SeedFromHex(hexStr string) ([SeedSize]byte, error) {
	s := hexStr
	if len(s) >= 2 && (s[:2] == "0x" || s[:2] == "0X") {
		s = s[2:]
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return [SeedSize]byte{}, fmt.Errorf("invalid seed hex: %w", err)
	}
	if len(b) != SeedSize {
		return [SeedSize]byte{}, fmt.Errorf("seed must be %d bytes, got %d", SeedSize, len(b))
	}
	var seed [SeedSize]byte
	copy(seed[:], b)
	return seed, nil
}

// DeriveFromSeed deterministically derives a ML-DSA-65 Mode 3 keypair from a 32-byte seed
// using the same circl mldsa65.NewKeyFromSeed the chain node uses.
func DeriveFromSeed(seed [SeedSize]byte) (*DerivedWallet, error) {
	pub, priv := mldsa65.NewKeyFromSeed(&seed)

	qPub, err := qcrypto.PublicKeyFromBytes(pub.Bytes())
	if err != nil {
		return nil, fmt.Errorf("wrap public key: %w", err)
	}
	qPriv, err := qcrypto.PrivateKeyFromBytes(priv.Bytes())
	if err != nil {
		return nil, fmt.Errorf("wrap private key: %w", err)
	}

	addr := qcrypto.DeriveAddress(qPub)
	return &DerivedWallet{
		Address:   addr,
		PublicKey: qPub.Bytes(),
		Private:   qPriv,
		Public:    qPub,
	}, nil
}

// DeriveFromEVMSignature is a convenience wrapper: SHA3-256(sig) → DeriveFromSeed.
func DeriveFromEVMSignature(sig []byte) (*DerivedWallet, error) {
	return DeriveFromSeed(SeedFromEVMSignature(sig))
}

// DeriveResult is the JSON-friendly result of derivation (no private key).
type DeriveResult struct {
	Address      string `json:"address"`
	PublicKeyB64 string `json:"public_key_b64"`
	PublicKeyHex string `json:"public_key_hex"`
}

// PublicResult returns address + public key without exposing the private key.
func (w *DerivedWallet) PublicResult() DeriveResult {
	return DeriveResult{
		Address:      w.Address.String(),
		PublicKeyB64: base64.StdEncoding.EncodeToString(w.PublicKey),
		PublicKeyHex: hex.EncodeToString(w.PublicKey),
	}
}

// Zeroize clears ML-DSA-65 private key material and public key bytes from memory.
func (w *DerivedWallet) Zeroize() {
	if w == nil {
		return
	}
	if w.Private != nil {
		w.Private.Zeroize()
		w.Private = nil
	}
	if w.PublicKey != nil {
		for i := range w.PublicKey {
			w.PublicKey[i] = 0
		}
		w.PublicKey = nil
	}
	w.Public = nil
	w.Address = qcrypto.Address{}
}

// TransferParams are inputs for building and signing a transfer.
type TransferParams struct {
	To        string `json:"to"`        // SQ: address or raw 64-char hex
	Amount    string `json:"amount"`    // decimal string in smallest units
	Fee       string `json:"fee"`       // decimal string; default "1000"
	Nonce     uint64 `json:"nonce"`
	Timestamp int64  `json:"timestamp"` // 0 = now
	Memo      string `json:"memo"`      // optional
}

// CallParams are inputs for a contract call.
type CallParams struct {
	Contract  string `json:"contract"` // SQ: address
	Calldata  []byte `json:"calldata,omitempty"`
	Value     string `json:"value"` // smallest units; default "0"
	Fee       string `json:"fee"`
	Nonce     uint64 `json:"nonce"`
	Timestamp int64  `json:"timestamp"`
}

// DeployParams are inputs for contract deploy.
type DeployParams struct {
	Bytecode  []byte `json:"bytecode,omitempty"`
	Fee       string `json:"fee"`
	Nonce     uint64 `json:"nonce"`
	Timestamp int64  `json:"timestamp"`
}

// X402Params are inputs for signing an X402 payment payload.
type X402Params struct {
	To          string `json:"to"`
	Amount      string `json:"amount"`
	ValidAfter  int64  `json:"valid_after"`
	ValidBefore int64  `json:"valid_before"`
	NonceHex    string `json:"nonce_hex"` // 32-byte hex; empty = random

	// Fee and AccountNonce are only used by SignX402Settlement, which builds the
	// on-chain transaction that actually moves the funds.
	Fee          string `json:"fee"`
	AccountNonce uint64 `json:"account_nonce"`
}

// SignTransfer builds and signs a transfer transaction, returning wire JSON.
func (w *DerivedWallet) SignTransfer(p TransferParams) ([]byte, error) {
	to, err := parseAddress(p.To)
	if err != nil {
		return nil, err
	}
	amount, err := parseBig(p.Amount, "amount")
	if err != nil {
		return nil, err
	}
	fee, err := parseBigDefault(p.Fee, "1000")
	if err != nil {
		return nil, err
	}
	if err := assertMinFee(fee); err != nil {
		return nil, err
	}
	ts := p.Timestamp
	if ts == 0 {
		ts = time.Now().Unix()
	}

	tx := types.NewTransfer(w.Address, to, amount, fee, p.Nonce, ts)
	if p.Memo != "" {
		tx.Data = []byte(p.Memo)
	}
	includeKey := p.Nonce == 0
	if err := tx.Sign(w.Private, includeKey); err != nil {
		return nil, err
	}
	return json.Marshal(tx)
}

// SignCall builds and signs a contract call transaction.
func (w *DerivedWallet) SignCall(p CallParams) ([]byte, error) {
	contract, err := parseAddress(p.Contract)
	if err != nil {
		return nil, err
	}
	value, err := parseBigDefault(p.Value, "0")
	if err != nil {
		return nil, err
	}
	fee, err := parseBigDefault(p.Fee, "1000")
	if err != nil {
		return nil, err
	}
	if err := assertMinFee(fee); err != nil {
		return nil, err
	}
	ts := p.Timestamp
	if ts == 0 {
		ts = time.Now().Unix()
	}

	tx := types.NewCall(w.Address, contract, p.Calldata, value, fee, p.Nonce, ts)
	includeKey := p.Nonce == 0
	if err := tx.Sign(w.Private, includeKey); err != nil {
		return nil, err
	}
	return json.Marshal(tx)
}

// SignDeploy builds and signs a contract deploy transaction.
func (w *DerivedWallet) SignDeploy(p DeployParams) ([]byte, error) {
	fee, err := parseBigDefault(p.Fee, "1000")
	if err != nil {
		return nil, err
	}
	if err := assertMinFee(fee); err != nil {
		return nil, err
	}
	ts := p.Timestamp
	if ts == 0 {
		ts = time.Now().Unix()
	}

	tx := types.NewDeploy(w.Address, p.Bytecode, fee, p.Nonce, ts)
	includeKey := p.Nonce == 0
	if err := tx.Sign(w.Private, includeKey); err != nil {
		return nil, err
	}
	return json.Marshal(tx)
}

// SignX402 builds and signs an X402 payment payload, returning wire JSON.
// Implemented here (not via x402 package) so WASM builds avoid LevelDB deps.
func (w *DerivedWallet) SignX402(p X402Params) ([]byte, error) {
	to, err := parseAddress(p.To)
	if err != nil {
		return nil, err
	}
	amount, err := parseBig(p.Amount, "amount")
	if err != nil {
		return nil, err
	}

	var nonce [32]byte
	if p.NonceHex != "" {
		nonce, err = parseNonceHex(p.NonceHex)
		if err != nil {
			return nil, err
		}
	} else {
		if _, err := rand.Read(nonce[:]); err != nil {
			return nil, err
		}
	}

	now := time.Now().Unix()
	validAfter := p.ValidAfter
	validBefore := p.ValidBefore
	if validAfter == 0 {
		validAfter = now - 30
	}
	if validBefore == 0 {
		validBefore = now + 300
	}

	payload := x402Payload{
		Version:     1,
		ChainID:     types.ChainID,
		From:        w.Address,
		To:          to,
		Amount:      amount,
		ValidAfter:  validAfter,
		ValidBefore: validBefore,
		Nonce:       nonce,
	}

	body := x402PayloadBody{
		Version:     payload.Version,
		ChainID:     payload.ChainID,
		From:        payload.From,
		To:          payload.To,
		Amount:      payload.Amount,
		ValidAfter:  payload.ValidAfter,
		ValidBefore: payload.ValidBefore,
		Nonce:       payload.Nonce,
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	hash := qcrypto.Hash32(bodyBytes)
	sig, err := qcrypto.Sign(w.Private, hash[:], qcrypto.CtxX402)
	if err != nil {
		return nil, err
	}
	payload.Signature = sig
	return json.Marshal(payload)
}

// SignX402Settlement builds and signs the ON-CHAIN settlement transaction for
// an X402 payment, returning wire JSON.
//
// The payment payload authorises a transfer; this transaction performs it. Both
// are signed by the payer, because settlement debits the payer's account and
// consumes the payer's account nonce — no node-side key can substitute.
//
// p.NonceHex MUST be the same 32-byte payment nonce used for the payload: the
// node binds the two together, and the executor derives the on-chain replay key
// from it. A random nonce here would produce a transaction the node refuses.
func (w *DerivedWallet) SignX402Settlement(p X402Params) ([]byte, error) {
	to, err := parseAddress(p.To)
	if err != nil {
		return nil, err
	}
	amount, err := parseBig(p.Amount, "amount")
	if err != nil {
		return nil, err
	}
	fee, err := parseBigDefault(p.Fee, "1000")
	if err != nil {
		return nil, err
	}
	if err := assertMinFee(fee); err != nil {
		return nil, err
	}
	if p.NonceHex == "" {
		return nil, fmt.Errorf("x402 settlement requires the payment nonce_hex used for the payload")
	}
	nonce, err := parseNonceHex(p.NonceHex)
	if err != nil {
		return nil, err
	}

	tx := &types.Transaction{
		Version:   1,
		ChainID:   types.ChainID,
		Type:      types.TxX402,
		AlgID:     qcrypto.AlgMLDSA65,
		From:      w.Address,
		To:        to,
		Amount:    amount,
		Fee:       fee,
		Nonce:     p.AccountNonce,
		Timestamp: time.Now().Unix(),
		// The raw payment nonce: the executor derives the replay-registry key as
		// SHA3-256(from || Data).
		Data: nonce[:],
	}
	if err := tx.Sign(w.Private, p.AccountNonce == 0); err != nil {
		return nil, err
	}
	return json.Marshal(tx)
}

// parseNonceHex decodes a 32-byte X402 payment nonce.
func parseNonceHex(s string) ([32]byte, error) {
	var nonce [32]byte
	nb, err := hex.DecodeString(strip0x(s))
	if err != nil || len(nb) != 32 {
		return nonce, fmt.Errorf("nonce must be 32-byte hex")
	}
	copy(nonce[:], nb)
	return nonce, nil
}

// Local copies of x402 types — keep JSON tags identical to x402.PaymentPayload.
type x402Payload struct {
	Version     int             `json:"version"`
	ChainID     string          `json:"chain_id"`
	From        qcrypto.Address `json:"from"`
	To          qcrypto.Address `json:"to"`
	Amount      *big.Int        `json:"amount"`
	ValidAfter  int64           `json:"valid_after"`
	ValidBefore int64           `json:"valid_before"`
	Nonce       [32]byte        `json:"nonce"`
	Signature   []byte          `json:"signature,omitempty"`
}

type x402PayloadBody struct {
	Version     int             `json:"version"`
	ChainID     string          `json:"chain_id"`
	From        qcrypto.Address `json:"from"`
	To          qcrypto.Address `json:"to"`
	Amount      *big.Int        `json:"amount"`
	ValidAfter  int64           `json:"valid_after"`
	ValidBefore int64           `json:"valid_before"`
	Nonce       [32]byte        `json:"nonce"`
}

func parseAddress(s string) (qcrypto.Address, error) {
	if len(s) > 3 && s[:3] == "SQ:" {
		return qcrypto.AddressFromString(s)
	}
	// Accept raw 64-char hex
	s = strip0x(s)
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != qcrypto.AddressSize {
		return qcrypto.Address{}, fmt.Errorf("invalid address %q", s)
	}
	var addr qcrypto.Address
	copy(addr[:], b)
	return addr, nil
}

func parseBig(s, name string) (*big.Int, error) {
	if s == "" {
		return nil, fmt.Errorf("%s is required", name)
	}
	v, ok := new(big.Int).SetString(s, 10)
	if !ok || v.Sign() < 0 {
		return nil, fmt.Errorf("invalid %s: %q", name, s)
	}
	return v, nil
}

func parseBigDefault(s, def string) (*big.Int, error) {
	if s == "" {
		s = def
	}
	return parseBig(s, "value")
}

// MinFee matches SQDP-1 / TS PROTOCOL.minFee (1000 base units).
const MinFee int64 = 1000

func assertMinFee(fee *big.Int) error {
	if fee == nil || fee.Cmp(big.NewInt(MinFee)) < 0 {
		got := "nil"
		if fee != nil {
			got = fee.String()
		}
		return fmt.Errorf("fee %s below minimum %d", got, MinFee)
	}
	return nil
}

func strip0x(s string) string {
	if len(s) >= 2 && (s[:2] == "0x" || s[:2] == "0X") {
		return s[2:]
	}
	return s
}
