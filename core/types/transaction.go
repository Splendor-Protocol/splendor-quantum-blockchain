// Package types defines the core data structures for Splendor Quantum Chain.
// All hashing uses SHA-3-256. All signing uses ML-DSA-65.
package types

import (
	"encoding/json"
	"fmt"
	"math/big"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
)

// TxType identifies the kind of transaction.
type TxType uint8

const (
	TxTransfer TxType = 0x01 // Transfer SPLD between accounts
	TxDeploy   TxType = 0x02 // Deploy a new smart contract
	TxCall     TxType = 0x03 // Call a smart contract function
	TxRegister TxType = 0x04 // Register public key on first use (auto on first Transfer)
	TxX402     TxType = 0x05 // X402 micropayment settlement
)

// DefaultChainID is the id this binary assumes when no genesis document names
// one. It is the id every tier shared before per-tier ids existed, so an
// unconfigured node still speaks to the network it always spoke to.
const DefaultChainID = "splendor-quantum-1"

// ChainID is the chain this node belongs to. It is signed into every
// transaction body (see TransactionBody) and stamped into every block header,
// so it is what separates one network's transactions from another's: a
// transaction is only valid on the chain whose id it was signed for.
//
// It is a variable rather than a constant because one binary has to serve
// devnet, testnet and mainnet, and because the id these tiers ran on was
// identical — which made a transaction signed on devnet byte-for-byte valid on
// mainnet as soon as the nonces lined up. Sourcing it from the genesis document
// is what lets the tiers be told apart without shipping three binaries.
//
// Write it exactly once, through SetChainID, during startup and before any
// goroutine that reads it exists. It is deliberately not mutex-guarded: after
// startup it is read on every transaction admission and block apply, and a lock
// on that path would cost more than it protects. Never assign to it directly.
var ChainID = DefaultChainID

// chainIDAdopted records that SetChainID has run, so a second, contradictory
// call is an error rather than a silent switch of networks mid-process.
var chainIDAdopted bool

// SetChainID adopts the chain id named by a validated genesis document.
//
// Calling it twice with the same id is a no-op, so a node that verifies its
// genesis on more than one path does not trip over itself. Calling it twice
// with DIFFERENT ids is refused: that would mean two genesis documents
// disagreed about which network this is, and continuing would let blocks from
// one chain be admitted under the identity of another.
func SetChainID(id string) error {
	if err := ValidateChainID(id); err != nil {
		return err
	}
	if chainIDAdopted && ChainID != id {
		return fmt.Errorf("chain id is already %q and cannot be changed to %q: "+
			"two genesis documents disagree about which network this node is on", ChainID, id)
	}
	ChainID = id
	chainIDAdopted = true
	return nil
}

// SetClientChainID points a SIGNING CLIENT at a chain, and unlike SetChainID it
// allows switching.
//
// The difference is what the caller is. A node has one identity for its whole
// life: switching would let blocks from one chain be admitted under another's
// name, so SetChainID refuses it. A signing client — the browser WASM signer, a
// wallet, an SDK — has no chain state to corrupt and legitimately signs for
// whichever network the user picked a moment ago. Refusing there would simply
// make network switching impossible.
//
// Use this only in a process that signs and never validates or stores blocks.
func SetClientChainID(id string) error {
	if err := ValidateChainID(id); err != nil {
		return err
	}
	ChainID = id
	chainIDAdopted = true
	return nil
}

// ValidateChainID rejects ids that could not have come from a genuine genesis.
//
// The id is signed into every transaction and stamped into every header, so it
// travels far and is compared byte-for-byte in a lot of places. Keeping it to a
// short, boring alphabet means it cannot smuggle whitespace, a newline or a
// homoglyph past a log line or a config file and be mistaken for another tier.
func ValidateChainID(id string) error {
	if id == "" {
		return fmt.Errorf("chain id is empty")
	}
	if len(id) > 64 {
		return fmt.Errorf("chain id %q is longer than 64 characters", id)
	}
	for _, r := range id {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.'
		if !ok {
			return fmt.Errorf("chain id %q contains %q; only letters, digits, '-', '_' and '.' are allowed", id, r)
		}
	}
	return nil
}

// Transaction is the fundamental unit of state change on Splendor Quantum Chain.
// All transactions are signed with ML-DSA-65 (NIST FIPS 204).
// The hash is computed as SHA3-256(JSON-serialised transaction body).
type Transaction struct {
	// Header
	Version uint8  `json:"version"`
	ChainID string `json:"chain_id"`
	Type    TxType `json:"type"`

	// AlgID names the signature scheme this transaction is signed with.
	// Part of the signed body: a verifier that does not implement the named
	// scheme rejects the tx rather than guessing, and an attacker cannot
	// downgrade a tx to a weaker scheme without invalidating the signature.
	AlgID string `json:"alg_id"`

	// Sender / Receiver
	From qcrypto.Address `json:"from"`
	To   qcrypto.Address `json:"to"`

	// Value in smallest SPLD unit (1 SPLD = 10^18 units)
	Amount *big.Int `json:"amount"`

	// Fee in smallest unit
	Fee *big.Int `json:"fee"`

	// Replay protection: monotonically increasing per-account counter
	Nonce uint64 `json:"nonce"`

	// Unix timestamp in seconds
	Timestamp int64 `json:"timestamp"`

	// Optional memo / data payload
	// For TxDeploy: contains compiled QSol bytecode
	// For TxCall:   contains ABI-encoded function call
	// For TxTransfer/TxX402: optional memo string
	Data []byte `json:"data,omitempty"`

	// Authentication
	// The sender's ML-DSA-65 public key (1952 bytes).
	// Included on first transaction; subsequent transactions look it up from state.
	PublicKey []byte `json:"public_key,omitempty"`

	// ML-DSA-65 signature (3309 bytes) over SHA3-256(canonical_body_bytes).
	Signature []byte `json:"signature,omitempty"`
}

// TransactionBody is the serialisable body that is hashed and signed.
// It excludes PublicKey and Signature fields.
type TransactionBody struct {
	Version   uint8           `json:"version"`
	ChainID   string          `json:"chain_id"`
	Type      TxType          `json:"type"`
	AlgID     string          `json:"alg_id"`
	From      qcrypto.Address `json:"from"`
	To        qcrypto.Address `json:"to"`
	Amount    *big.Int        `json:"amount"`
	Fee       *big.Int        `json:"fee"`
	Nonce     uint64          `json:"nonce"`
	Timestamp int64           `json:"timestamp"`
	Data      []byte          `json:"data,omitempty"`
}

// Hash computes SHA3-256 of the canonical transaction body.
// This is the value that must be signed by the sender.
func (tx *Transaction) Hash() ([32]byte, error) {
	body := TransactionBody{
		Version:   tx.Version,
		ChainID:   tx.ChainID,
		Type:      tx.Type,
		AlgID:     tx.AlgID,
		From:      tx.From,
		To:        tx.To,
		Amount:    tx.Amount,
		Fee:       tx.Fee,
		Nonce:     tx.Nonce,
		Timestamp: tx.Timestamp,
		Data:      tx.Data,
	}
	b, err := json.Marshal(body)
	if err != nil {
		return [32]byte{}, fmt.Errorf("failed to marshal transaction body: %w", err)
	}
	return qcrypto.Hash32(b), nil
}

// FullHash computes SHA3-256 of the complete signed transaction (including signature).
// This is used as the transaction ID stored on chain.
func (tx *Transaction) FullHash() ([32]byte, error) {
	b, err := json.Marshal(tx)
	if err != nil {
		return [32]byte{}, fmt.Errorf("failed to marshal transaction: %w", err)
	}
	return qcrypto.Hash32(b), nil
}

// Validate performs basic structural validation of a transaction.
// It does NOT verify the signature (use VerifySignature for that).
func (tx *Transaction) Validate() error {
	if tx.Version == 0 {
		return fmt.Errorf("invalid version 0")
	}
	if tx.ChainID != ChainID {
		return fmt.Errorf("wrong chain ID: got %q, want %q", tx.ChainID, ChainID)
	}
	if tx.AlgID != qcrypto.AlgMLDSA65 {
		return fmt.Errorf("unsupported signature algorithm %q: this node only verifies %q",
			tx.AlgID, qcrypto.AlgMLDSA65)
	}
	if tx.From.IsZero() {
		return fmt.Errorf("zero from address")
	}
	if tx.Amount == nil || tx.Amount.Sign() < 0 {
		return fmt.Errorf("invalid amount")
	}
	if tx.Fee == nil || tx.Fee.Sign() < 0 {
		return fmt.Errorf("invalid fee")
	}
	if len(tx.Signature) != qcrypto.SignatureSize {
		return fmt.Errorf("wrong signature length: got %d, want %d", len(tx.Signature), qcrypto.SignatureSize)
	}
	return nil
}

// VerifySignature verifies the ML-DSA-65 signature on a transaction.
// pubKeyBytes must be the sender's registered ML-DSA-65 public key.
func (tx *Transaction) VerifySignature(pubKeyBytes []byte) error {
	pub, err := qcrypto.PublicKeyFromBytes(pubKeyBytes)
	if err != nil {
		return fmt.Errorf("invalid public key: %w", err)
	}
	hash, err := tx.Hash()
	if err != nil {
		return fmt.Errorf("failed to compute tx hash: %w", err)
	}
	if !qcrypto.Verify(pub, hash[:], qcrypto.CtxTransaction, tx.Signature) {
		return fmt.Errorf("invalid ML-DSA signature")
	}
	return nil
}

// Sign signs the transaction using the given ML-DSA-65 private key.
// It sets the Signature field and optionally the PublicKey field.
func (tx *Transaction) Sign(priv *qcrypto.PrivateKey, includePublicKey bool) error {
	if tx.AlgID == "" {
		tx.AlgID = qcrypto.AlgMLDSA65
	}
	hash, err := tx.Hash()
	if err != nil {
		return err
	}
	sig, err := qcrypto.Sign(priv, hash[:], qcrypto.CtxTransaction)
	if err != nil {
		return fmt.Errorf("signing failed: %w", err)
	}
	tx.Signature = sig
	if includePublicKey {
		tx.PublicKey = priv.Public().Bytes()
	}
	return nil
}

// NewTransfer creates an unsigned transfer transaction.
func NewTransfer(from, to qcrypto.Address, amount, fee *big.Int, nonce uint64, timestamp int64) *Transaction {
	return &Transaction{
		Version:   1,
		ChainID:   ChainID,
		Type:      TxTransfer,
		AlgID:     qcrypto.AlgMLDSA65,
		From:      from,
		To:        to,
		Amount:    new(big.Int).Set(amount),
		Fee:       new(big.Int).Set(fee),
		Nonce:     nonce,
		Timestamp: timestamp,
	}
}

// NewDeploy creates an unsigned contract deployment transaction.
func NewDeploy(from qcrypto.Address, bytecode []byte, fee *big.Int, nonce uint64, timestamp int64) *Transaction {
	return &Transaction{
		Version:   1,
		ChainID:   ChainID,
		Type:      TxDeploy,
		AlgID:     qcrypto.AlgMLDSA65,
		From:      from,
		Amount:    big.NewInt(0),
		Fee:       new(big.Int).Set(fee),
		Nonce:     nonce,
		Timestamp: timestamp,
		Data:      bytecode,
	}
}

// NewCall creates an unsigned smart contract call transaction.
func NewCall(from, contract qcrypto.Address, calldata []byte, value, fee *big.Int, nonce uint64, timestamp int64) *Transaction {
	return &Transaction{
		Version:   1,
		ChainID:   ChainID,
		Type:      TxCall,
		AlgID:     qcrypto.AlgMLDSA65,
		From:      from,
		To:        contract,
		Amount:    new(big.Int).Set(value),
		Fee:       new(big.Int).Set(fee),
		Nonce:     nonce,
		Timestamp: timestamp,
		Data:      calldata,
	}
}

// ChainIDAdoptedForTest and RestoreChainIDForTest exist so tests in other
// packages can save and restore this package's chain identity. Production code
// must go through SetChainID, which refuses to switch networks.
func ChainIDAdoptedForTest() bool { return chainIDAdopted }

// RestoreChainIDForTest puts the identity back to a previously captured value.
func RestoreChainIDForTest(id string, adopted bool) {
	ChainID, chainIDAdopted = id, adopted
}

// ── QRC-20 permit digest ────────────────────────────────────────────────────
//
// This belongs in the leaf package because the signing clients (SDK, WASM
// signer) must compute it without linking vm → core/state → leveldb. It sits
// in this file rather than its own because core/types is linked into the
// pinned WASM signer and, even with every new function dead-code-eliminated,
// adding a source file to the package changes the linked bytes and breaks the
// wasmSha256 pin. Move it into its own file only in a change that rebuilds and
// re-pins the signer.

// permitDigestPrefix is the domain tag of the v2 permit preimage. v1 was
// "qrc20:permit:" and did not commit to a chain id; it was never reachable
// from a transaction (no router action), so v2 replaces it outright instead of
// living beside it.
const permitDigestPrefix = "qrc20:permit:v2:"

// PermitDigest is the message an owner signs (under crypto.CtxPermit) to grant
// spender an allowance of amount over its QRC-20 balance at contract, valid
// while the block time is at most deadline and only while the owner's permit
// nonce at that contract equals nonce.
//
// Preimage, fixed-width so no field can borrow bytes from its neighbour:
//
//	"qrc20:permit:v2:" ‖ u16be(len(chainID)) ‖ chainID
//	‖ contract[32] ‖ owner[32] ‖ spender[32]
//	‖ amount[32, big-endian] ‖ nonce[8, big-endian] ‖ deadline[8, big-endian]
//
// digest = SHA3-256(preimage).
//
// Why the chain id is inside the digest: a QRC-20 address is derived from the
// deployer and its nonce only, and the same tokens are replayed by the same
// deployer on every tier, so the same token has the same address on devnet
// and mainnet. Without the chain id a permit signed against a devnet token
// would verify byte-for-byte against the mainnet one.
//
// It lives in core/types rather than vm so a signing client (the SDK, the WASM
// signer) can compute it without linking the state database. It errors rather
// than panics on every malformed input because the vm hands it caller-supplied
// values: a nil, negative or >= 2^256 amount, a negative deadline, or a chain
// id ValidateChainID refuses (which also keeps the u16 length prefix from
// wrapping).
func PermitDigest(
	chainID string,
	contract, owner, spender qcrypto.Address,
	amount *big.Int,
	nonce uint64,
	deadline int64,
) ([32]byte, error) {
	if err := ValidateChainID(chainID); err != nil {
		return [32]byte{}, fmt.Errorf("permit digest: %w", err)
	}
	if amount == nil {
		return [32]byte{}, fmt.Errorf("permit digest: nil amount")
	}
	if amount.Sign() < 0 {
		return [32]byte{}, fmt.Errorf("permit digest: negative amount")
	}
	if amount.BitLen() > 256 {
		return [32]byte{}, fmt.Errorf("permit digest: amount does not fit in 256 bits")
	}
	if deadline < 0 {
		return [32]byte{}, fmt.Errorf("permit digest: negative deadline")
	}

	msg := make([]byte, 0, len(permitDigestPrefix)+2+len(chainID)+3*qcrypto.AddressSize+32+8+8)
	msg = append(msg, permitDigestPrefix...)
	msg = append(msg, byte(len(chainID)>>8), byte(len(chainID)))
	msg = append(msg, chainID...)
	msg = append(msg, contract[:]...)
	msg = append(msg, owner[:]...)
	msg = append(msg, spender[:]...)
	var amountBytes [32]byte
	amount.FillBytes(amountBytes[:])
	msg = append(msg, amountBytes[:]...)
	msg = appendUint64BE(msg, nonce)
	msg = appendUint64BE(msg, uint64(deadline))
	return qcrypto.Hash32(msg), nil
}

// appendUint64BE appends v as 8 big-endian bytes.
func appendUint64BE(b []byte, v uint64) []byte {
	return append(b,
		byte(v>>56), byte(v>>48), byte(v>>40), byte(v>>32),
		byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

// ── Exchange order-signing digests ──────────────────────────────────────────
//
// Same home as the permit digest, for the same reason: the SDK and the WASM
// signer compute these without linking vm → core/state → leveldb, and a new
// source file in this package would move the pinned signer bytes. Each is
// signed under crypto.CtxOrder. The exchange module (vm) verifies them; the
// range rules it enforces on top (price in (0, 1e6), side in {buy, sell},
// outcome below the condition's outcome count) are module rules, not digest
// rules — the digest commits to whatever the maker signed.

const (
	orderDigestPrefix          = "exchange:order:v1:"
	cancelDigestPrefix         = "exchange:cancel:v1:"
	incrementNonceDigestPrefix = "exchange:nonce:v1:"
	bridgeClaimDigestPrefix    = "bridge:claim:v1:"
)

// Order sides as signed into an order and carried on the wire.
const (
	OrderSideBuy  uint8 = 0
	OrderSideSell uint8 = 1
)

// Order is what a maker signs to authorise the exchange module to move
// exactly the amounts a settlement fills from the maker's balances — no
// allowance, no approval. Every field is committed to by OrderDigest.
//
// Recipient is signed on purpose: an unsigned recipient would let the operator
// redirect shares. A zero Recipient means "pay the maker" and still hashes as
// thirty-two zero bytes. Expiration is a block time in seconds, 0 = never.
// Price is 1e6-scaled.
type Order struct {
	Salt        [32]byte
	Maker       qcrypto.Address
	Exchange    qcrypto.Address
	Condition   uint64
	Outcome     uint8
	Side        uint8 // OrderSideBuy or OrderSideSell
	ShareAmount *big.Int
	Price       *big.Int
	Expiration  int64
	Nonce       uint64
	Recipient   qcrypto.Address
}

// OrderDigest is the message a maker signs (under crypto.CtxOrder) for an
// order, and the hash an order is tracked by (filled / cancelled) on-chain.
//
// Preimage, fixed-width so no field can borrow bytes from its neighbour:
//
//	"exchange:order:v1:" ‖ u16be(len(chainID)) ‖ chainID
//	‖ exchange[32] ‖ salt[32] ‖ maker[32]
//	‖ condition[8, big-endian] ‖ outcome[1] ‖ side[1]
//	‖ share_amount[32, big-endian] ‖ price[32, big-endian]
//	‖ expiration[8, big-endian] ‖ nonce[8, big-endian] ‖ recipient[32]
//
// digest = SHA3-256(preimage).
//
// The chain id is inside for the same reason as the permit's: exchange
// instances and their collateral tokens can share an address across tiers.
// The module computes the digest from the exchange it is called on, not from
// the order's own field, so an order signed for one instance never verifies
// at another. Errors rather than panics on a nil, negative or >= 2^256
// share amount or price, a negative expiration, or a chain id ValidateChainID
// refuses (which also keeps the u16 length prefix from wrapping).
func OrderDigest(chainID string, o Order) ([32]byte, error) {
	if err := ValidateChainID(chainID); err != nil {
		return [32]byte{}, fmt.Errorf("order digest: %w", err)
	}
	shareAmount, err := uint256Word("order digest", "share_amount", o.ShareAmount)
	if err != nil {
		return [32]byte{}, err
	}
	price, err := uint256Word("order digest", "price", o.Price)
	if err != nil {
		return [32]byte{}, err
	}
	if o.Expiration < 0 {
		return [32]byte{}, fmt.Errorf("order digest: negative expiration")
	}

	msg := make([]byte, 0, len(orderDigestPrefix)+2+len(chainID)+4*qcrypto.AddressSize+32+8+1+1+32+32+8+8)
	msg = append(msg, orderDigestPrefix...)
	msg = append(msg, byte(len(chainID)>>8), byte(len(chainID)))
	msg = append(msg, chainID...)
	msg = append(msg, o.Exchange[:]...)
	msg = append(msg, o.Salt[:]...)
	msg = append(msg, o.Maker[:]...)
	msg = appendUint64BE(msg, o.Condition)
	msg = append(msg, o.Outcome, o.Side)
	msg = append(msg, shareAmount[:]...)
	msg = append(msg, price[:]...)
	msg = appendUint64BE(msg, uint64(o.Expiration))
	msg = appendUint64BE(msg, o.Nonce)
	msg = append(msg, o.Recipient[:]...)
	return qcrypto.Hash32(msg), nil
}

// CancelDigest is the message a maker signs (under crypto.CtxOrder) so that
// anyone — not only the maker or the operator — can submit the cancellation of
// the order hashed by orderHash. Cancelling is idempotent and the order hash
// already commits to the chain id and the exchange, so the preimage needs
// nothing else and cannot fail:
//
//	digest = SHA3-256("exchange:cancel:v1:" ‖ order_hash[32])
func CancelDigest(orderHash [32]byte) [32]byte {
	msg := make([]byte, 0, len(cancelDigestPrefix)+32)
	msg = append(msg, cancelDigestPrefix...)
	msg = append(msg, orderHash[:]...)
	return qcrypto.Hash32(msg)
}

// IncrementNonceDigest is the message a maker signs (under crypto.CtxOrder) so
// that anyone can bump the maker's order nonce at exchange, invalidating every
// order signed with the old one. It binds expectedNonce, which the module
// requires to equal the maker's current nonce, so each signature is single-use
// — without it a captured signature could bulk-cancel the maker's future
// orders every block.
//
//	"exchange:nonce:v1:" ‖ u16be(len(chainID)) ‖ chainID
//	‖ exchange[32] ‖ maker[32] ‖ expected_nonce[8, big-endian]
//
// digest = SHA3-256(preimage). Errors on a chain id ValidateChainID refuses.
func IncrementNonceDigest(chainID string, exchange, maker qcrypto.Address, expectedNonce uint64) ([32]byte, error) {
	if err := ValidateChainID(chainID); err != nil {
		return [32]byte{}, fmt.Errorf("increment-nonce digest: %w", err)
	}
	msg := make([]byte, 0, len(incrementNonceDigestPrefix)+2+len(chainID)+2*qcrypto.AddressSize+8)
	msg = append(msg, incrementNonceDigestPrefix...)
	msg = append(msg, byte(len(chainID)>>8), byte(len(chainID)))
	msg = append(msg, chainID...)
	msg = append(msg, exchange[:]...)
	msg = append(msg, maker[:]...)
	msg = appendUint64BE(msg, expectedNonce)
	return qcrypto.Hash32(msg), nil
}

// BridgeClaimDigest is the message a migration-bridge attestor signs (under
// crypto.CtxBridge) to authorise crediting one legacy-chain burn. A claim on
// the bridge module is accepted only when at least the instance's threshold of
// distinct registered attestors have signed this digest, so no single operator
// key — and no single legacy-chain RPC feeding one — can move funds.
//
// Preimage, fixed-width so no field can borrow bytes from its neighbour:
//
//	"bridge:claim:v1:" ‖ u16be(len(chainID)) ‖ chainID
//	‖ bridge[32] ‖ id[8, big-endian] ‖ asset[1]
//	‖ amount[32, big-endian] ‖ recipient[32] ‖ evm_tx[32]
//
// digest = SHA3-256(preimage). The chain id is folded in for the same reason
// as the permit digest (a bridge address can repeat across a re-genesis that
// keeps its chain id only if height also repeated — belt and braces), and the
// bridge address and id pin the attestation to exactly one burn so it can
// never be replayed for another amount, recipient or instance. It lives here,
// not in vm, so the relayer/attestor can compute it without linking the state
// database. It errors rather than panics on caller-supplied input.
func BridgeClaimDigest(
	chainID string,
	bridge qcrypto.Address,
	id uint64,
	asset uint8,
	amount *big.Int,
	recipient qcrypto.Address,
	evmTx [32]byte,
) ([32]byte, error) {
	if err := ValidateChainID(chainID); err != nil {
		return [32]byte{}, fmt.Errorf("bridge claim digest: %w", err)
	}
	amountWord, err := uint256Word("bridge claim digest", "amount", amount)
	if err != nil {
		return [32]byte{}, err
	}
	msg := make([]byte, 0, len(bridgeClaimDigestPrefix)+2+len(chainID)+qcrypto.AddressSize+8+1+32+qcrypto.AddressSize+32)
	msg = append(msg, bridgeClaimDigestPrefix...)
	msg = append(msg, byte(len(chainID)>>8), byte(len(chainID)))
	msg = append(msg, chainID...)
	msg = append(msg, bridge[:]...)
	msg = appendUint64BE(msg, id)
	msg = append(msg, asset)
	msg = append(msg, amountWord[:]...)
	msg = append(msg, recipient[:]...)
	msg = append(msg, evmTx[:]...)
	return qcrypto.Hash32(msg), nil
}

// uint256Word encodes v as a 32-byte big-endian word, refusing anything a
// caller-supplied value could smuggle in: nil, negative, or wider than 256
// bits. what/field name the digest and the field in the error.
func uint256Word(what, field string, v *big.Int) ([32]byte, error) {
	var word [32]byte
	if v == nil {
		return word, fmt.Errorf("%s: nil %s", what, field)
	}
	if v.Sign() < 0 {
		return word, fmt.Errorf("%s: negative %s", what, field)
	}
	if v.BitLen() > 256 {
		return word, fmt.Errorf("%s: %s does not fit in 256 bits", what, field)
	}
	v.FillBytes(word[:])
	return word, nil
}
