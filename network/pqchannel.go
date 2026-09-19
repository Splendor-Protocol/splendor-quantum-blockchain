// Package network — post-quantum authenticated channel for node-to-node traffic.
//
// ML-DSA protects what is IN a block. This file protects the block while it is
// in flight. Without it, P2P traffic is plaintext HTTP: anyone on the path can
// read blocks and transactions before they are public, and — more seriously —
// the shared write-plane token travels in a header where a passive observer can
// lift it and then push blocks themselves.
//
// The construction is a standard KEM-DEM hybrid, entirely post-quantum:
//
//  1. Every node has a long-term ML-DSA-65 identity key.
//  2. It publishes an identity document containing a short-lived ML-KEM-768
//     encapsulation key, SIGNED by the identity key. The signature is what stops
//     a machine-in-the-middle from substituting its own KEM key — an unsigned
//     KEM key would give encryption with no authentication, which is no defence
//     against an active attacker.
//  3. A sender encapsulates to that key, yielding a fresh 32-byte shared secret
//     per message, and derives an AES-256-GCM key from it with SHA3-256.
//  4. The receiver decapsulates and opens the envelope.
//
// Forward secrecy comes from rotating the KEM key: a compromised identity key
// cannot retroactively decrypt traffic sealed to a rotated-out KEM key, because
// the identity key only signs KEM keys, it never derives shared secrets.
package network

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/mlkem"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
)

// bytesEqualConstantTime compares two byte slices without leaking their
// contents through timing.
func bytesEqualConstantTime(a, b []byte) bool {
	return subtle.ConstantTimeCompare(a, b) == 1
}

const (
	// kemKeyLifetime is how long a published ML-KEM key stays valid. Short
	// enough to bound the window a stolen decapsulation key can decrypt,
	// long enough that peers rarely re-fetch.
	kemKeyLifetime = 1 * time.Hour

	// maxEnvelopeAge bounds replay of a captured envelope.
	maxEnvelopeAge = 2 * time.Minute

	// channelKeyDomain separates the AES key derivation from every other use of
	// SHA3-256 on this chain.
	channelKeyDomain = "SPLENDOR-QC-P2P-KEY-v1"

	// gcmNonceSize is the standard GCM nonce length. Validating this before
	// passing to aead.Open prevents panic attacks where a malformed envelope
	// crashes the node.
	gcmNonceSize = 12

	// gcmTagSize is the GCM authentication tag length. A ciphertext shorter
	// than this cannot possibly be valid.
	gcmTagSize = 16

	// kemPreviousGrace is how long the just-rotated-out KEM key still opens
	// in-flight envelopes. After this the previous private key is dropped.
	kemPreviousGrace = 2 * time.Minute

	// maxEnvelopeReplayCache bounds remembered envelope IDs inside the
	// timestamp window so a capture cannot be reused as a new message.
	maxEnvelopeReplayCache = 4096
)

// ErrEnvelopeReplay means this exact envelope was already opened. A
// retransmission is distinct from a new message (different nonce/ciphertext).
var ErrEnvelopeReplay = errors.New("envelope already opened (retransmission)")

// IdentityDocument is a node's signed, publicly fetchable key material.
type IdentityDocument struct {
	// SigAlg / KEMAlg name the schemes, so a future migration is explicit
	// rather than inferred from byte lengths.
	SigAlg string `json:"sig_alg"`
	KEMAlg string `json:"kem_alg"`

	// NodePubKey is the long-term ML-DSA-65 identity key (1952 bytes).
	NodePubKey []byte `json:"node_pubkey"`

	// KEMPubKey is the current ML-KEM-768 encapsulation key (1184 bytes).
	KEMPubKey []byte `json:"kem_pubkey"`

	// NotAfter is when KEMPubKey expires (unix seconds).
	NotAfter int64 `json:"not_after"`

	// Signature is ML-DSA-65 over SHA3-256(KEMPubKey || NotAfter) under
	// CtxPeerHandshake, proving the KEM key belongs to NodePubKey.
	Signature []byte `json:"signature"`

	// CodedObjects, when non-nil, declares whether this node serves coded
	// objects (get_object_manifest_v1 / get_object_symbol_v1). nil means the
	// field predates this document version — the peer may or may not serve,
	// so the fetcher probes. A declaration of false lets a fetcher skip the
	// coded attempt and go straight to the plain paths.
	CodedObjects *bool `json:"coded_objects,omitempty"`

	// CodedBlocks, when non-nil, declares whether this node serves coded
	// block batches (get_block_batch_v1), with the same probe semantics as
	// CodedObjects.
	CodedBlocks *bool `json:"coded_blocks,omitempty"`

	// CapsSignature authenticates CodedObjects and CodedBlocks under the node
	// identity key with its own domain (CtxPeerCaps). It is a separate
	// signature so peers that predate the capability fields — which ignore
	// unknown JSON keys and verify only Signature — still accept this
	// document.
	CapsSignature []byte `json:"caps_signature,omitempty"`
}

// Envelope is a sealed message: an ML-KEM ciphertext carrying the shared
// secret, plus the AES-256-GCM ciphertext of the payload.
type Envelope struct {
	KEMCiphertext []byte `json:"kem_ct"`
	Nonce         []byte `json:"nonce"`
	Ciphertext    []byte `json:"ciphertext"`
	// Timestamp is authenticated as additional data, bounding replay.
	Timestamp int64 `json:"timestamp"`
}

// NodeIdentity is a node's own key material: the long-term ML-DSA identity and
// the rotating ML-KEM decapsulation key.
type NodeIdentity struct {
	mu sync.RWMutex

	sigPub  *qcrypto.PublicKey
	sigPriv *qcrypto.PrivateKey

	kemPriv  *mlkem.DecapsulationKey768
	notAfter time.Time

	// caps is the node's declared capability set, or nil while undecided.
	// Included in Document() (with CapsSignature) only when non-nil.
	caps *IdentityCaps

	// previous keeps the just-rotated key alive briefly so messages already in
	// flight against the old document still open. Without it, every rotation
	// would drop a burst of legitimate blocks.
	previous      *mlkem.DecapsulationKey768
	previousUntil time.Time

	replayMu      sync.Mutex
	seenEnvelopes map[[32]byte]time.Time
	seenOrder     [][32]byte
}

// IdentityCaps is the capability set a node declares in its identity document.
type IdentityCaps struct {
	// CodedObjects says whether this node serves coded objects over the
	// sealed channel (get_object_manifest_v1 / get_object_symbol_v1).
	CodedObjects bool
	// CodedBlocks says whether this node serves coded block batches
	// (get_block_batch_v1).
	CodedBlocks bool
}

// SetCapabilities declares (or replaces) the node's capability set. Call
// before the first peer fetches the identity document.
func (n *NodeIdentity) SetCapabilities(caps IdentityCaps) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.caps = &caps
}

// NewNodeIdentity creates a node identity from an existing ML-DSA keypair,
// generating the first ML-KEM key.
func NewNodeIdentity(pub *qcrypto.PublicKey, priv *qcrypto.PrivateKey) (*NodeIdentity, error) {
	if pub == nil || priv == nil {
		return nil, fmt.Errorf("node identity requires an ML-DSA keypair")
	}
	n := &NodeIdentity{sigPub: pub, sigPriv: priv}
	if err := n.rotate(); err != nil {
		return nil, err
	}
	return n, nil
}

// rotate generates a fresh ML-KEM-768 keypair and retires the current one.
func (n *NodeIdentity) rotate() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.rotateLocked()
}

func (n *NodeIdentity) rotateLocked() error {
	dk, err := mlkem.GenerateKey768()
	if err != nil {
		return fmt.Errorf("generate ML-KEM-768 key: %w", err)
	}
	n.previous = n.kemPriv
	if n.kemPriv != nil {
		n.previousUntil = time.Now().Add(kemPreviousGrace)
	} else {
		n.previousUntil = time.Time{}
	}
	n.kemPriv = dk
	n.notAfter = time.Now().Add(kemKeyLifetime)
	return nil
}

func (n *NodeIdentity) retirePreviousLocked() {
	if n.previous != nil && !n.previousUntil.IsZero() && time.Now().After(n.previousUntil) {
		n.previous = nil
		n.previousUntil = time.Time{}
	}
}

// Document returns the current signed identity document, rotating the KEM key
// first if it has expired.
// SigningKeys exposes the long-term ML-DSA identity keypair, for signing
// messages other than the identity document itself (see SignAnnounce).
func (n *NodeIdentity) SigningKeys() (*qcrypto.PublicKey, *qcrypto.PrivateKey) {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.sigPub, n.sigPriv
}

func (n *NodeIdentity) Document() (*IdentityDocument, error) {
	n.mu.Lock()
	n.retirePreviousLocked()
	if time.Now().After(n.notAfter) {
		if err := n.rotateLocked(); err != nil {
			n.mu.Unlock()
			return nil, err
		}
	}
	kemPub := n.kemPriv.EncapsulationKey().Bytes()
	notAfter := n.notAfter.Unix()
	sigPub := n.sigPub
	sigPriv := n.sigPriv
	caps := n.caps
	n.mu.Unlock()

	sig, err := qcrypto.Sign(sigPriv, identityDigest(kemPub, notAfter), qcrypto.CtxPeerHandshake)
	if err != nil {
		return nil, fmt.Errorf("sign identity document: %w", err)
	}
	doc := &IdentityDocument{
		SigAlg:     qcrypto.AlgMLDSA65,
		KEMAlg:     "ML-KEM-768",
		NodePubKey: sigPub.Bytes(),
		KEMPubKey:  kemPub,
		NotAfter:   notAfter,
		Signature:  sig,
	}
	if caps != nil {
		codedObj := caps.CodedObjects
		codedBlk := caps.CodedBlocks
		doc.CodedObjects = &codedObj
		doc.CodedBlocks = &codedBlk
		capsSig, err := qcrypto.Sign(sigPriv, identityCapsDigest(doc.NodePubKey, codedObj, codedBlk), qcrypto.CtxPeerCaps)
		if err != nil {
			return nil, fmt.Errorf("sign identity capabilities: %w", err)
		}
		doc.CapsSignature = capsSig
	}
	return doc, nil
}

// identityDigest is the message signed by the identity key. Binding NotAfter
// into the digest stops an attacker from replaying an old signed KEM key with a
// fresh expiry after that key has been retired.
func identityDigest(kemPub []byte, notAfter int64) []byte {
	buf := make([]byte, 0, len(kemPub)+8)
	buf = append(buf, kemPub...)
	for i := 0; i < 8; i++ {
		buf = append(buf, byte(notAfter>>(8*i)))
	}
	d := qcrypto.Hash32(buf)
	return d[:]
}

// identityCapsDigest is the message signed under CtxPeerCaps. It binds the
// capability declaration to the node identity key so a capability block cannot
// be spliced from one peer's document onto another's.
func identityCapsDigest(nodePubKey []byte, codedObjects, codedBlocks bool) []byte {
	buf := make([]byte, 0, len(nodePubKey)+2)
	buf = append(buf, nodePubKey...)
	if codedObjects {
		buf = append(buf, 0x01)
	} else {
		buf = append(buf, 0x00)
	}
	if codedBlocks {
		buf = append(buf, 0x01)
	} else {
		buf = append(buf, 0x00)
	}
	d := qcrypto.Hash32(buf)
	return d[:]
}

// Verify checks an identity document against an expected node public key.
//
// expectedNodePubKey is the caller's pinned identity for this peer. Passing nil
// accepts whatever key the document carries, which authenticates the KEM key to
// that identity but does NOT tell you the identity is the peer you meant —
// only use nil during first contact, then pin.
func (doc *IdentityDocument) Verify(expectedNodePubKey []byte) error {
	if doc.SigAlg != qcrypto.AlgMLDSA65 {
		return fmt.Errorf("unsupported identity signature algorithm %q", doc.SigAlg)
	}
	if doc.KEMAlg != "ML-KEM-768" {
		return fmt.Errorf("unsupported KEM algorithm %q", doc.KEMAlg)
	}
	if len(doc.KEMPubKey) != mlkem.EncapsulationKeySize768 {
		return fmt.Errorf("bad ML-KEM key size %d, want %d", len(doc.KEMPubKey), mlkem.EncapsulationKeySize768)
	}
	if time.Now().After(time.Unix(doc.NotAfter, 0)) {
		return fmt.Errorf("identity document expired at %s", time.Unix(doc.NotAfter, 0).UTC())
	}
	if expectedNodePubKey != nil &&
		!bytesEqualConstantTime(doc.NodePubKey, expectedNodePubKey) {
		return fmt.Errorf("identity key does not match the pinned key for this peer")
	}

	pub, err := qcrypto.PublicKeyFromBytes(doc.NodePubKey)
	if err != nil {
		return fmt.Errorf("invalid node public key: %w", err)
	}
	if !qcrypto.Verify(pub, identityDigest(doc.KEMPubKey, doc.NotAfter), qcrypto.CtxPeerHandshake, doc.Signature) {
		return fmt.Errorf("identity document signature is invalid: the KEM key is not authenticated")
	}
	// The capability block is optional, but when any half of it is present
	// both must be, and the declaration must verify under the node key.
	if doc.CodedObjects != nil || doc.CodedBlocks != nil || doc.CapsSignature != nil {
		if doc.CodedObjects == nil || doc.CodedBlocks == nil || doc.CapsSignature == nil {
			return fmt.Errorf("identity document capability declaration is incomplete")
		}
		if !qcrypto.Verify(pub, identityCapsDigest(doc.NodePubKey, *doc.CodedObjects, *doc.CodedBlocks), qcrypto.CtxPeerCaps, doc.CapsSignature) {
			return fmt.Errorf("identity document capability signature is invalid")
		}
	}
	return nil
}

// ServesCodedObjects reports whether this document's peer serves coded
// objects. A document that predates the capability field returns true: the
// peer may or may not serve, and the fetcher probes rather than assumes a
// downgrade.
func (doc *IdentityDocument) ServesCodedObjects() bool {
	return doc.CodedObjects == nil || *doc.CodedObjects
}

// ServesCodedBlocks reports whether this document's peer serves coded block
// batches, with the same probe semantics as ServesCodedObjects.
func (doc *IdentityDocument) ServesCodedBlocks() bool {
	return doc.CodedBlocks == nil || *doc.CodedBlocks
}

// Seal encrypts plaintext to the peer described by a VERIFIED identity document.
//
// Callers must have called Verify first. Sealing to an unverified document
// encrypts to whoever produced it, which an active attacker is happy to be.
func Seal(doc *IdentityDocument, plaintext []byte) (*Envelope, error) {
	return SealToKEM(doc.KEMPubKey, plaintext)
}

// SealToKEM encrypts plaintext to a raw ML-KEM-768 encapsulation key.
//
// Used for the reply leg of a sealed query, where the responder seals to the
// one-shot key the requester supplied rather than to a published identity. The
// requester's key is not signed, which is fine in that direction: it arrived
// inside an envelope only the responder could open, so it cannot have been
// substituted by anyone on the path.
func SealToKEM(kemPub, plaintext []byte) (*Envelope, error) {
	ek, err := mlkem.NewEncapsulationKey768(kemPub)
	if err != nil {
		return nil, fmt.Errorf("parse ML-KEM key: %w", err)
	}
	sharedSecret, kemCT := ek.Encapsulate()

	ts := time.Now().Unix()
	aead, err := channelAEAD(sharedSecret)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("nonce: %w", err)
	}

	ct := aead.Seal(nil, nonce, plaintext, envelopeAAD(kemCT, ts))
	return &Envelope{
		KEMCiphertext: kemCT,
		Nonce:         nonce,
		Ciphertext:    ct,
		Timestamp:     ts,
	}, nil
}

// Open decrypts an envelope sealed to this node.
func (n *NodeIdentity) Open(env *Envelope) ([]byte, error) {
	if env == nil {
		return nil, fmt.Errorf("nil envelope")
	}
	if len(env.KEMCiphertext) != mlkem.CiphertextSize768 {
		return nil, fmt.Errorf("bad ML-KEM ciphertext size %d, want %d",
			len(env.KEMCiphertext), mlkem.CiphertextSize768)
	}
	// Reject stale envelopes so a captured message cannot be replayed later.
	age := time.Since(time.Unix(env.Timestamp, 0))
	if age > maxEnvelopeAge || age < -maxEnvelopeAge {
		return nil, fmt.Errorf("envelope timestamp outside the ±%s replay window", maxEnvelopeAge)
	}

	n.mu.Lock()
	n.retirePreviousLocked()
	current, previous := n.kemPriv, n.previous
	n.mu.Unlock()

	// Try the current key, then the just-rotated one, so a rotation mid-flight
	// does not drop legitimate traffic.
	for _, dk := range []*mlkem.DecapsulationKey768{current, previous} {
		if dk == nil {
			continue
		}
		if pt, err := openWith(dk, env); err == nil {
			if rerr := n.rememberEnvelope(env); rerr != nil {
				return nil, rerr
			}
			return pt, nil
		}
	}
	return nil, fmt.Errorf("envelope did not decrypt under any current node key")
}

func envelopeReplayID(env *Envelope) [32]byte {
	buf := make([]byte, 0, len(env.KEMCiphertext)+len(env.Nonce)+8+len(env.Ciphertext))
	buf = append(buf, env.KEMCiphertext...)
	buf = append(buf, env.Nonce...)
	ts := env.Timestamp
	for i := 0; i < 8; i++ {
		buf = append(buf, byte(ts>>(8*i)))
	}
	buf = append(buf, env.Ciphertext...)
	return qcrypto.Hash32(buf)
}

func (n *NodeIdentity) rememberEnvelope(env *Envelope) error {
	id := envelopeReplayID(env)
	now := time.Now()
	n.replayMu.Lock()
	defer n.replayMu.Unlock()
	if n.seenEnvelopes == nil {
		n.seenEnvelopes = make(map[[32]byte]time.Time)
	}
	cutoff := now.Add(-maxEnvelopeAge)
	kept := n.seenOrder[:0]
	for _, old := range n.seenOrder {
		if exp, ok := n.seenEnvelopes[old]; ok && exp.After(cutoff) {
			kept = append(kept, old)
			continue
		}
		delete(n.seenEnvelopes, old)
	}
	n.seenOrder = kept
	if exp, ok := n.seenEnvelopes[id]; ok && exp.After(cutoff) {
		return ErrEnvelopeReplay
	}
	n.seenEnvelopes[id] = now
	n.seenOrder = append(n.seenOrder, id)
	if len(n.seenOrder) > maxEnvelopeReplayCache {
		old := n.seenOrder[0]
		n.seenOrder = n.seenOrder[1:]
		delete(n.seenEnvelopes, old)
	}
	return nil
}

// openWith decapsulates and authenticates an envelope under one ML-KEM key.
func openWith(dk *mlkem.DecapsulationKey768, env *Envelope) ([]byte, error) {
	// SECURITY: Validate envelope structure BEFORE any cryptographic operations.
	// Invalid nonce length causes aead.Open to panic; rejecting it here converts
	// a node-crash DOS into a bounded error return.
	if len(env.Nonce) != gcmNonceSize {
		return nil, fmt.Errorf("invalid nonce length %d, expected %d", len(env.Nonce), gcmNonceSize)
	}
	// Ciphertext must be at least as long as the authentication tag.
	if len(env.Ciphertext) < gcmTagSize {
		return nil, fmt.Errorf("ciphertext too short: %d bytes, minimum %d", len(env.Ciphertext), gcmTagSize)
	}

	sharedSecret, err := dk.Decapsulate(env.KEMCiphertext)
	if err != nil {
		return nil, err
	}
	aead, err := channelAEAD(sharedSecret)
	if err != nil {
		return nil, err
	}
	return aead.Open(nil, env.Nonce, env.Ciphertext, envelopeAAD(env.KEMCiphertext, env.Timestamp))
}

// EphemeralKEM is a one-shot ML-KEM-768 keypair a requester uses to receive a
// single sealed reply. Generating a fresh key per query means a reply can only
// be read by the exact request that asked for it, and nothing is retained
// afterwards that could decrypt a captured response later.
type EphemeralKEM struct {
	dk *mlkem.DecapsulationKey768
}

// NewEphemeralKEM generates a one-shot reply keypair.
func NewEphemeralKEM() (*EphemeralKEM, error) {
	dk, err := mlkem.GenerateKey768()
	if err != nil {
		return nil, fmt.Errorf("generate reply key: %w", err)
	}
	return &EphemeralKEM{dk: dk}, nil
}

// PublicKey returns the encapsulation key to send to the responder.
func (e *EphemeralKEM) PublicKey() []byte {
	return e.dk.EncapsulationKey().Bytes()
}

// Open decrypts a reply sealed to this one-shot key.
func (e *EphemeralKEM) Open(env *Envelope) ([]byte, error) {
	if env == nil {
		return nil, fmt.Errorf("nil envelope")
	}
	if len(env.KEMCiphertext) != mlkem.CiphertextSize768 {
		return nil, fmt.Errorf("bad ML-KEM ciphertext size %d, want %d",
			len(env.KEMCiphertext), mlkem.CiphertextSize768)
	}
	age := time.Since(time.Unix(env.Timestamp, 0))
	if age > maxEnvelopeAge || age < -maxEnvelopeAge {
		return nil, fmt.Errorf("reply timestamp outside the ±%s replay window", maxEnvelopeAge)
	}
	pt, err := openWith(e.dk, env)
	if err != nil {
		return nil, fmt.Errorf("reply did not decrypt under the request's reply key: %w", err)
	}
	return pt, nil
}

// channelAEAD derives the AES-256-GCM key from an ML-KEM shared secret.
func channelAEAD(sharedSecret []byte) (cipher.AEAD, error) {
	keyed := qcrypto.Hash32(append([]byte(channelKeyDomain), sharedSecret...))
	blk, err := aes.NewCipher(keyed[:])
	if err != nil {
		return nil, fmt.Errorf("aes: %w", err)
	}
	return cipher.NewGCM(blk)
}

// envelopeAAD binds the KEM ciphertext and timestamp into the AEAD tag, so
// neither can be swapped without the open failing.
func envelopeAAD(kemCT []byte, ts int64) []byte {
	aad := make([]byte, 0, len(kemCT)+8)
	aad = append(aad, kemCT...)
	for i := 0; i < 8; i++ {
		aad = append(aad, byte(ts>>(8*i)))
	}
	return aad
}

// MarshalEnvelope / UnmarshalEnvelope are helpers for HTTP transport.
func MarshalEnvelope(e *Envelope) ([]byte, error) { return json.Marshal(e) }

func UnmarshalEnvelope(b []byte) (*Envelope, error) {
	var e Envelope
	if err := json.Unmarshal(b, &e); err != nil {
		return nil, fmt.Errorf("invalid envelope JSON: %w", err)
	}
	return &e, nil
}
