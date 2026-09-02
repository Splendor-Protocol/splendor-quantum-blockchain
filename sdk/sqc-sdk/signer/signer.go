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
// Keep in sync with the TypeScript SDK (sdk/sqc-sdk/src/derive.ts).
//
// The trailing "chain: splendor-quantum-1" is PERMANENT. It names the
// derivation scheme, not the network the signer talks to: every SQ: address
// an EVM key has ever derived is SHA3(personal_sign(this exact message)), so
// changing one byte of it would change every EVM-derived address on every
// tier. The network a signature targets comes from types.ChainID (set through
// SetClientChainID / sqSetChainID) and is bound into each transaction, permit
// and order digest separately. Do not "fix" this string when a tier is given
// a new chain id.
const DerivationDomainMessage = "Splendor Quantum Chain\nKey derivation v1\nchain: splendor-quantum-1"

// SeedSize is the ML-DSA-65 seed length (32 bytes).
const SeedSize = mldsa65.SeedSize

// DerivedWallet holds an ML-DSA-65 keypair derived from a seed.
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

// DeriveFromSeed deterministically derives an ML-DSA-65 keypair from a 32-byte seed
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
	To        string `json:"to"`     // SQ: address or raw 64-char hex
	Amount    string `json:"amount"` // decimal string in smallest units
	Fee       string `json:"fee"`    // decimal string; default "1000"
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
	// Outside the signed body (see x402.PaymentPayload.PublicKey): lets a payer
	// whose key is not yet registered on chain verify. The node ignores it for
	// a registered payer and refuses it unless it derives From.
	payload.PublicKey = w.PublicKey
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

// ── Off-chain authorisations: QRC-20 permit and exchange orders ─────────────
//
// None of these builds a transaction. Each signs a digest from core/types
// bound to types.ChainID (as set by SetClientChainID) and returns the params
// object the matching module action accepts verbatim — a relayer or operator
// wraps it in the module envelope and pays the fee. The signer's public key is
// attached so an address that has never sent a transaction can be verified
// (the module checks that it derives the address; a registered key wins).

// PermitParams are inputs for SignPermit: grant spender an allowance of
// amount over this wallet's balance at token. Nonce is the owner's current
// permit nonce at token (qrc20 read `permit_nonce`); Deadline is a block
// time in seconds and must be positive (the chain rejects 0).
type PermitParams struct {
	Token    string `json:"token"`
	Spender  string `json:"spender"`
	Amount   string `json:"amount"` // decimal string in smallest units
	Nonce    uint64 `json:"nonce"`
	Deadline int64  `json:"deadline"`
}

// SignedPermit is what SignPermit returns: the params of qrc20.permit
// (token, owner, spender, amount, deadline, signature, owner_pubkey) plus
// nonce, chain_id and digest_hex, which the module ignores and a client
// records.
type SignedPermit struct {
	Owner       string `json:"owner"`
	Token       string `json:"token"`
	Spender     string `json:"spender"`
	Amount      string `json:"amount"`
	Nonce       uint64 `json:"nonce"`
	Deadline    int64  `json:"deadline"`
	ChainID     string `json:"chain_id"`
	DigestHex   string `json:"digest_hex"`
	Signature   []byte `json:"signature"`
	OwnerPubKey []byte `json:"owner_pubkey"`
}

// SignPermit signs types.PermitDigest(types.ChainID, …) under CtxPermit with
// this wallet as the owner and returns the qrc20.permit params as JSON.
func (w *DerivedWallet) SignPermit(p PermitParams) ([]byte, error) {
	token, err := parseAddress(p.Token)
	if err != nil {
		return nil, fmt.Errorf("permit: token: %w", err)
	}
	spender, err := parseAddress(p.Spender)
	if err != nil {
		return nil, fmt.Errorf("permit: spender: %w", err)
	}
	amount, err := parseUint256(p.Amount, "amount")
	if err != nil {
		return nil, fmt.Errorf("permit: %w", err)
	}
	if p.Deadline <= 0 {
		return nil, fmt.Errorf("permit: deadline must be positive (block time in seconds)")
	}
	digest, err := types.PermitDigest(types.ChainID, token, w.Address, spender, amount, p.Nonce, p.Deadline)
	if err != nil {
		return nil, err
	}
	sig, err := qcrypto.Sign(w.Private, digest[:], qcrypto.CtxPermit)
	if err != nil {
		return nil, err
	}
	return json.Marshal(SignedPermit{
		Owner:       w.Address.String(),
		Token:       token.String(),
		Spender:     spender.String(),
		Amount:      amount.String(),
		Nonce:       p.Nonce,
		Deadline:    p.Deadline,
		ChainID:     types.ChainID,
		DigestHex:   hex.EncodeToString(digest[:]),
		Signature:   sig,
		OwnerPubKey: w.PublicKey,
	})
}

// OrderParams are inputs for SignOrder and SignCancel: an exchange order
// with this wallet as the maker. Salt is 32 bytes of hex; empty means a
// random salt (SignOrder only — a cancel must name the exact order). Nonce is
// the maker's current order nonce at exchange (exchange read `nonce`).
// Recipient empty means "pay the maker". Price is 1e6-scaled, in (0, 1e6).
type OrderParams struct {
	Exchange    string `json:"exchange"`
	Salt        string `json:"salt,omitempty"` // 64 hex chars; empty = random
	Condition   uint64 `json:"condition"`
	Outcome     uint8  `json:"outcome"`
	Side        uint8  `json:"side"` // types.OrderSideBuy / types.OrderSideSell
	ShareAmount string `json:"share_amount"`
	Price       string `json:"price"`
	Expiration  int64  `json:"expiration"` // block time in seconds; 0 = never
	Nonce       uint64 `json:"nonce"`
	Recipient   string `json:"recipient,omitempty"`
}

// OrderWire is the order object the exchange module accepts in
// match.taker_order, match.maker_orders[] and cancel_order.order. Field
// names, encodings and omissions match vm.exchangeOrderWire exactly.
type OrderWire struct {
	Salt        string `json:"salt"`
	Maker       string `json:"maker"`
	Exchange    string `json:"exchange"`
	Condition   uint64 `json:"condition"`
	Outcome     uint8  `json:"outcome"`
	Side        uint8  `json:"side"`
	ShareAmount string `json:"share_amount"`
	Price       string `json:"price"`
	Expiration  int64  `json:"expiration"`
	Nonce       uint64 `json:"nonce"`
	Recipient   string `json:"recipient,omitempty"`
	Signature   []byte `json:"signature,omitempty"`
	MakerPubKey []byte `json:"maker_pubkey,omitempty"`
}

// SignedOrder is what SignOrder returns: the OrderWire with signature and
// maker_pubkey set, plus hash (the order's on-chain id, hex) and chain_id,
// which the module ignores.
type SignedOrder struct {
	OrderWire
	Hash    string `json:"hash"`
	ChainID string `json:"chain_id"`
}

// SignOrder signs types.OrderDigest(types.ChainID, order) under CtxOrder
// with this wallet as the maker and returns the wire order as JSON.
func (w *DerivedWallet) SignOrder(p OrderParams) ([]byte, error) {
	o, err := w.buildOrder(p, true)
	if err != nil {
		return nil, fmt.Errorf("order: %w", err)
	}
	hash, err := types.OrderDigest(types.ChainID, o)
	if err != nil {
		return nil, err
	}
	sig, err := qcrypto.Sign(w.Private, hash[:], qcrypto.CtxOrder)
	if err != nil {
		return nil, err
	}
	wire := orderWireOf(o)
	wire.Signature = sig
	wire.MakerPubKey = w.PublicKey
	return json.Marshal(SignedOrder{
		OrderWire: wire,
		Hash:      hex.EncodeToString(hash[:]),
		ChainID:   types.ChainID,
	})
}

// SignedCancel is what SignCancel returns: the params of
// exchange.cancel_order (exchange, order, cancel_signature, maker_pubkey)
// that anyone may submit on the maker's behalf, plus order_hash, digest_hex
// and chain_id, which the module ignores. The nested order carries no
// signature: a cancel needs only the hash.
type SignedCancel struct {
	Exchange        string    `json:"exchange"`
	Order           OrderWire `json:"order"`
	CancelSignature []byte    `json:"cancel_signature"`
	MakerPubKey     []byte    `json:"maker_pubkey"`
	OrderHash       string    `json:"order_hash"`
	DigestHex       string    `json:"digest_hex"`
	ChainID         string    `json:"chain_id"`
}

// SignCancel signs types.CancelDigest(order hash) under CtxOrder for the
// order p describes (Salt required) and returns the cancel_order params as
// JSON. Idempotent on chain, so it can be signed once and kept.
func (w *DerivedWallet) SignCancel(p OrderParams) ([]byte, error) {
	o, err := w.buildOrder(p, false)
	if err != nil {
		return nil, fmt.Errorf("cancel: %w", err)
	}
	hash, err := types.OrderDigest(types.ChainID, o)
	if err != nil {
		return nil, err
	}
	digest := types.CancelDigest(hash)
	sig, err := qcrypto.Sign(w.Private, digest[:], qcrypto.CtxOrder)
	if err != nil {
		return nil, err
	}
	return json.Marshal(SignedCancel{
		Exchange:        o.Exchange.String(),
		Order:           orderWireOf(o),
		CancelSignature: sig,
		MakerPubKey:     w.PublicKey,
		OrderHash:       hex.EncodeToString(hash[:]),
		DigestHex:       hex.EncodeToString(digest[:]),
		ChainID:         types.ChainID,
	})
}

// IncrementNonceParams are inputs for SignIncrementNonce. ExpectedNonce must
// equal the maker's current order nonce at exchange (exchange read `nonce`);
// the chain refuses anything else, which is what makes the signature
// single-use.
type IncrementNonceParams struct {
	Exchange      string `json:"exchange"`
	ExpectedNonce uint64 `json:"expected_nonce"`
}

// SignedIncrementNonce is what SignIncrementNonce returns: the params of
// exchange.increment_nonce (exchange, maker, expected_nonce, signature,
// maker_pubkey) that anyone may submit on the maker's behalf, plus chain_id
// and digest_hex, which the module ignores.
type SignedIncrementNonce struct {
	Exchange      string `json:"exchange"`
	Maker         string `json:"maker"`
	ExpectedNonce uint64 `json:"expected_nonce"`
	Signature     []byte `json:"signature"`
	MakerPubKey   []byte `json:"maker_pubkey"`
	ChainID       string `json:"chain_id"`
	DigestHex     string `json:"digest_hex"`
}

// SignIncrementNonce signs types.IncrementNonceDigest(types.ChainID,
// exchange, maker, expected_nonce) under CtxOrder and returns the
// increment_nonce params as JSON. Bumping the nonce invalidates every order
// this wallet signed at exchange with the old one.
func (w *DerivedWallet) SignIncrementNonce(p IncrementNonceParams) ([]byte, error) {
	exchange, err := parseAddress(p.Exchange)
	if err != nil {
		return nil, fmt.Errorf("increment_nonce: exchange: %w", err)
	}
	if exchange.IsZero() {
		return nil, fmt.Errorf("increment_nonce: exchange is the zero address")
	}
	digest, err := types.IncrementNonceDigest(types.ChainID, exchange, w.Address, p.ExpectedNonce)
	if err != nil {
		return nil, err
	}
	sig, err := qcrypto.Sign(w.Private, digest[:], qcrypto.CtxOrder)
	if err != nil {
		return nil, err
	}
	return json.Marshal(SignedIncrementNonce{
		Exchange:      exchange.String(),
		Maker:         w.Address.String(),
		ExpectedNonce: p.ExpectedNonce,
		Signature:     sig,
		MakerPubKey:   w.PublicKey,
		ChainID:       types.ChainID,
		DigestHex:     hex.EncodeToString(digest[:]),
	})
}

// OrderPriceScale is the exchange's price denominator: prices are in
// (0, OrderPriceScale) and mean price/OrderPriceScale collateral per share.
// Mirrors vm.ExchangePriceScale; the signer cannot import vm.
const OrderPriceScale = 1_000_000

// buildOrder validates OrderParams the way the module's parseOrder will and
// returns the types.Order this wallet is the maker of. randomSalt allows an
// empty salt to be filled from crypto/rand.
func (w *DerivedWallet) buildOrder(p OrderParams, randomSalt bool) (types.Order, error) {
	var o types.Order
	exchange, err := parseAddress(p.Exchange)
	if err != nil {
		return o, fmt.Errorf("exchange: %w", err)
	}
	if exchange.IsZero() {
		return o, fmt.Errorf("exchange is the zero address")
	}
	var salt [32]byte
	switch {
	case p.Salt != "":
		if salt, err = parseNonceHex(p.Salt); err != nil {
			return o, fmt.Errorf("salt must be 32-byte hex")
		}
	case randomSalt:
		if _, err := rand.Read(salt[:]); err != nil {
			return o, err
		}
	default:
		return o, fmt.Errorf("salt is required (the order's own 32-byte salt)")
	}
	if p.Side != types.OrderSideBuy && p.Side != types.OrderSideSell {
		return o, fmt.Errorf("side must be %d (buy) or %d (sell), got %d", types.OrderSideBuy, types.OrderSideSell, p.Side)
	}
	shares, err := parseUint256(p.ShareAmount, "share_amount")
	if err != nil {
		return o, err
	}
	if shares.Sign() == 0 {
		return o, fmt.Errorf("share_amount must be positive")
	}
	price, err := parseUint256(p.Price, "price")
	if err != nil {
		return o, err
	}
	if price.Sign() == 0 || price.Cmp(big.NewInt(OrderPriceScale)) >= 0 {
		return o, fmt.Errorf("price must be in (0, %d), got %s", OrderPriceScale, price)
	}
	if p.Expiration < 0 {
		return o, fmt.Errorf("expiration must not be negative")
	}
	var recipient qcrypto.Address
	if p.Recipient != "" {
		if recipient, err = parseAddress(p.Recipient); err != nil {
			return o, fmt.Errorf("recipient: %w", err)
		}
	}
	if recipient == exchange || recipient == ModuleRouterAddress() {
		return o, fmt.Errorf("recipient %s is a module address; funds sent there are stranded", recipient)
	}
	return types.Order{
		Salt: salt, Maker: w.Address, Exchange: exchange, Condition: p.Condition,
		Outcome: p.Outcome, Side: p.Side, ShareAmount: shares, Price: price,
		Expiration: p.Expiration, Nonce: p.Nonce, Recipient: recipient,
	}, nil
}

// orderWireOf renders o in the module's wire shape, unsigned.
func orderWireOf(o types.Order) OrderWire {
	w := OrderWire{
		Salt: hex.EncodeToString(o.Salt[:]), Maker: o.Maker.String(), Exchange: o.Exchange.String(),
		Condition: o.Condition, Outcome: o.Outcome, Side: o.Side,
		ShareAmount: o.ShareAmount.String(), Price: o.Price.String(),
		Expiration: o.Expiration, Nonce: o.Nonce,
	}
	if !o.Recipient.IsZero() {
		w.Recipient = o.Recipient.String()
	}
	return w
}

// ModuleRouterAddress is the reserved account every native-module call is
// addressed to (the `contract` of a SignCall carrying a module envelope):
// SHA3-256("splendor:module-router:v1"), recomputed here so the signer and
// the WASM never link vm. router_address_test pins it to vm.ModuleRouterAddress.
func ModuleRouterAddress() qcrypto.Address {
	h := qcrypto.Hash32([]byte("splendor:module-router:v1"))
	var a qcrypto.Address
	copy(a[:], h[:])
	return a
}

// parseUint256 is the strict decoder the module actions use for amounts,
// prices and ids: canonical decimal only (ASCII digits, no sign, no leading
// zero except "0" itself) and below 2^256. A value the chain would refuse is
// refused here, before anything is signed.
func parseUint256(s, name string) (*big.Int, error) {
	if s == "" {
		return nil, fmt.Errorf("%s is required", name)
	}
	if len(s) > 1 && s[0] == '0' {
		return nil, fmt.Errorf("invalid %s: %q (leading zero)", name, s)
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return nil, fmt.Errorf("invalid %s: %q (canonical decimal only)", name, s)
		}
	}
	v, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return nil, fmt.Errorf("invalid %s: %q", name, s)
	}
	if v.BitLen() > 256 {
		return nil, fmt.Errorf("invalid %s: %q does not fit in 256 bits", name, s)
	}
	return v, nil
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
	PublicKey   []byte          `json:"public_key,omitempty"`
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
