// Package x402 implements the native HTTP 402 micropayment protocol for
// Splendor Quantum Chain. All payment signatures use ML-DSA-65 (FIPS 204)
// under the CtxX402 domain-separation context, so a payment authorisation can
// never be replayed as a transaction or a block signature.
// Replay protection is provided by on-chain nonce registry in the StateDB.
package x402

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"time"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// PaymentRequirements is sent by a server in the HTTP 402 response body.
// It tells the client exactly what payment is needed.
type PaymentRequirements struct {
	// Splendor QC address of the payment receiver (API operator).
	PayTo qcrypto.Address `json:"pay_to"`

	// Payment amount in smallest SPLD unit.
	Amount *big.Int `json:"amount"`

	// Unix timestamp after which a signed payment is valid.
	ValidAfter int64 `json:"valid_after"`

	// Unix timestamp before which a signed payment must be settled.
	// Recommended window: 5 minutes from now.
	ValidBefore int64 `json:"valid_before"`

	// Human-readable description of what is being paid for.
	Description string `json:"description,omitempty"`
}

// PaymentPayload is the client-side payment authorisation.
// The payload is signed with the payer's ML-DSA-65 private key.
type PaymentPayload struct {
	// Version of the X402 protocol.
	Version int `json:"version"`

	// Chain identifier — must match the node's chain ID.
	ChainID string `json:"chain_id"`

	// Sender's SQ address.
	From qcrypto.Address `json:"from"`

	// Receiver's SQ address (must match PaymentRequirements.PayTo).
	To qcrypto.Address `json:"to"`

	// Payment amount (must match or exceed PaymentRequirements.Amount).
	Amount *big.Int `json:"amount"`

	// Validity window (must satisfy PaymentRequirements.ValidAfter/ValidBefore).
	ValidAfter  int64 `json:"valid_after"`
	ValidBefore int64 `json:"valid_before"`

	// 32-byte random nonce. Single-use. Tracked on-chain after settlement.
	Nonce [32]byte `json:"nonce"`

	// ML-DSA-65 signature (3309 bytes) over SHA3-256(canonical_payload_bytes).
	Signature []byte `json:"signature,omitempty"`
}

// payloadBody is the signable subset of PaymentPayload (excludes Signature).
type payloadBody struct {
	Version     int             `json:"version"`
	ChainID     string          `json:"chain_id"`
	From        qcrypto.Address `json:"from"`
	To          qcrypto.Address `json:"to"`
	Amount      *big.Int        `json:"amount"`
	ValidAfter  int64           `json:"valid_after"`
	ValidBefore int64           `json:"valid_before"`
	Nonce       [32]byte        `json:"nonce"`
}

// Hash computes SHA3-256 of the canonical payment payload body.
func (p *PaymentPayload) Hash() ([32]byte, error) {
	body := payloadBody{
		Version:     p.Version,
		ChainID:     p.ChainID,
		From:        p.From,
		To:          p.To,
		Amount:      p.Amount,
		ValidAfter:  p.ValidAfter,
		ValidBefore: p.ValidBefore,
		Nonce:       p.Nonce,
	}
	b, err := json.Marshal(body)
	if err != nil {
		return [32]byte{}, fmt.Errorf("failed to marshal payment payload: %w", err)
	}
	return qcrypto.Hash32(b), nil
}

// Sign signs the payment payload with the given ML-DSA-65 private key.
func (p *PaymentPayload) Sign(priv *qcrypto.PrivateKey) error {
	hash, err := p.Hash()
	if err != nil {
		return err
	}
	sig, err := qcrypto.Sign(priv, hash[:], qcrypto.CtxX402)
	if err != nil {
		return fmt.Errorf("x402 payment signing failed: %w", err)
	}
	p.Signature = sig
	return nil
}

// Processor handles X402 payment verification and settlement.
type Processor struct {
	state *state.StateDB
}

// NewProcessor creates a new X402 payment processor.
func NewProcessor(stateDB *state.StateDB) *Processor {
	return &Processor{state: stateDB}
}

// VerifyResult is returned by Verify.
type VerifyResult struct {
	Valid  bool   `json:"valid"`
	Reason string `json:"reason,omitempty"`
}

// Verify validates a signed payment payload against current chain state.
// This is a read-only operation — it does NOT modify state.
func (p *Processor) Verify(req *PaymentRequirements, payload *PaymentPayload) *VerifyResult {
	// 1. Chain ID check
	if payload.ChainID != types.ChainID {
		return &VerifyResult{Reason: fmt.Sprintf("wrong chain ID: %s", payload.ChainID)}
	}

	// 2. Receiver must match requirements
	if payload.To != req.PayTo {
		return &VerifyResult{Reason: "payment receiver does not match requirements"}
	}

	// 3. Amount must be sufficient. Guard req.Amount too: a null requirements
	// amount would make big.Int.Cmp(nil) panic and crash the request goroutine.
	if req.Amount == nil {
		return &VerifyResult{Reason: "requirements amount is missing"}
	}
	if payload.Amount == nil || payload.Amount.Cmp(req.Amount) < 0 {
		return &VerifyResult{
			Reason: fmt.Sprintf("payment amount insufficient: got %s, need %s",
				payload.Amount, req.Amount),
		}
	}

	// 4. Timestamp validity
	now := time.Now().Unix()
	if now < payload.ValidAfter {
		return &VerifyResult{Reason: "payment not yet valid"}
	}
	if now > payload.ValidBefore {
		return &VerifyResult{Reason: "payment expired"}
	}

	// 5. Nonce not already used
	nonceKey := buildNonceKey(payload.From, payload.Nonce)
	used, err := p.state.IsX402NonceUsed(nonceKey)
	if err != nil {
		return &VerifyResult{Reason: fmt.Sprintf("nonce registry error: %v", err)}
	}
	if used {
		return &VerifyResult{Reason: "payment nonce already used"}
	}

	// 6. Sender has sufficient balance
	balance, err := p.state.GetBalance(payload.From)
	if err != nil {
		return &VerifyResult{Reason: fmt.Sprintf("balance lookup failed: %v", err)}
	}
	if balance.Cmp(payload.Amount) < 0 {
		return &VerifyResult{Reason: "insufficient balance"}
	}

	// 7. Verify ML-DSA-65 signature
	pubKey, err := p.state.GetPublicKey(payload.From)
	if err != nil {
		return &VerifyResult{Reason: fmt.Sprintf("public key not registered for payer: %v", err)}
	}
	hash, err := payload.Hash()
	if err != nil {
		return &VerifyResult{Reason: "failed to compute payload hash"}
	}
	pub, err := qcrypto.PublicKeyFromBytes(pubKey)
	if err != nil {
		return &VerifyResult{Reason: "invalid payer public key"}
	}
	if !qcrypto.Verify(pub, hash[:], qcrypto.CtxX402, payload.Signature) {
		return &VerifyResult{Reason: "invalid ML-DSA signature"}
	}

	return &VerifyResult{Valid: true}
}

// SettleResult is returned by Settle.
type SettleResult struct {
	Success bool   `json:"success"`
	TxHash  string `json:"tx_hash,omitempty"`
	Error   string `json:"error,omitempty"`
}

// NewSettlementTx builds the UNSIGNED on-chain settlement transaction for a
// verified payment payload. The payer must sign it: settlement debits the
// payer's account and consumes the payer's account nonce, so only the payer's
// ML-DSA key can authorise it. No node-side key can stand in.
//
// Data carries the raw 32-byte payment nonce (NOT the derived registry key) —
// the executor recomputes the registry key as SHA3-256(from || Data), so
// anything else here would mark a different key than Verify checks and defeat
// replay protection.
func NewSettlementTx(payload *PaymentPayload, fee *big.Int, accountNonce uint64) *types.Transaction {
	return &types.Transaction{
		Version:   1,
		ChainID:   types.ChainID,
		Type:      types.TxX402,
		AlgID:     qcrypto.AlgMLDSA65,
		From:      payload.From,
		To:        payload.To,
		Amount:    new(big.Int).Set(payload.Amount),
		Fee:       new(big.Int).Set(fee),
		Nonce:     accountNonce,
		Timestamp: time.Now().Unix(),
		Data:      payload.Nonce[:],
	}
}

// BindSettlementTx checks that a payer-signed settlement transaction actually
// settles the payment that was verified — same payer, same payee, same amount,
// same payment nonce.
//
// Without this the settle endpoint would verify one payment and mempool an
// unrelated transaction: an attacker could present a valid small payment
// authorisation and attach a transaction moving a different amount elsewhere.
// The transaction's own signature, account nonce, and balance are checked by the
// mempool; this function only binds it to the authorisation.
func BindSettlementTx(payload *PaymentPayload, tx *types.Transaction) error {
	if tx == nil {
		return fmt.Errorf("settlement transaction is required")
	}
	if tx.Type != types.TxX402 {
		return fmt.Errorf("settlement tx must be type X402 (0x05), got 0x%02x", tx.Type)
	}
	if tx.From != payload.From {
		return fmt.Errorf("settlement tx sender does not match the payment payer")
	}
	if tx.To != payload.To {
		return fmt.Errorf("settlement tx receiver does not match the payment payee")
	}
	if tx.Amount == nil || payload.Amount == nil || tx.Amount.Cmp(payload.Amount) != 0 {
		return fmt.Errorf("settlement tx amount does not match the authorised payment amount")
	}
	if !bytes.Equal(tx.Data, payload.Nonce[:]) {
		return fmt.Errorf("settlement tx does not carry the authorised payment nonce")
	}
	return nil
}

// buildNonceKey derives the on-chain nonce registry key.
// key = SHA3-256(from_address || nonce_bytes)
func buildNonceKey(from qcrypto.Address, nonce [32]byte) [32]byte {
	combined := append(from[:], nonce[:]...)
	return qcrypto.Hash32(combined)
}

// NewPaymentRequirements is a convenience constructor for servers.
func NewPaymentRequirements(payTo qcrypto.Address, amountUnits *big.Int, description string) *PaymentRequirements {
	now := time.Now().Unix()
	return &PaymentRequirements{
		PayTo:       payTo,
		Amount:      amountUnits,
		ValidAfter:  now - 30,          // 30 seconds grace
		ValidBefore: now + 5*60,        // 5 minute window
		Description: description,
	}
}
