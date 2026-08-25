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
)

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

	// previous keeps the just-rotated key alive briefly so messages already in
	// flight against the old document still open. Without it, every rotation
	// would drop a burst of legitimate blocks.
	previous *mlkem.DecapsulationKey768
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
	dk, err := mlkem.GenerateKey768()
	if err != nil {
		return fmt.Errorf("generate ML-KEM-768 key: %w", err)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.previous = n.kemPriv
	n.kemPriv = dk
	n.notAfter = time.Now().Add(kemKeyLifetime)
	return nil
}

// Document returns the current signed identity document, rotating the KEM key
// first if it has expired.
func (n *NodeIdentity) Document() (*IdentityDocument, error) {
	n.mu.RLock()
	expired := time.Now().After(n.notAfter)
	n.mu.RUnlock()
	if expired {
		if err := n.rotate(); err != nil {
			return nil, err
		}
	}

	n.mu.RLock()
	defer n.mu.RUnlock()

	kemPub := n.kemPriv.EncapsulationKey().Bytes()
	notAfter := n.notAfter.Unix()

	sig, err := qcrypto.Sign(n.sigPriv, identityDigest(kemPub, notAfter), qcrypto.CtxPeerHandshake)
	if err != nil {
		return nil, fmt.Errorf("sign identity document: %w", err)
	}
	return &IdentityDocument{
		SigAlg:     qcrypto.AlgMLDSA65,
		KEMAlg:     "ML-KEM-768",
		NodePubKey: n.sigPub.Bytes(),
		KEMPubKey:  kemPub,
		NotAfter:   notAfter,
		Signature:  sig,
	}, nil
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
	return nil
}

// Seal encrypts plaintext to the peer described by a VERIFIED identity document.
//
// Callers must have called Verify first. Sealing to an unverified document
// encrypts to whoever produced it, which an active attacker is happy to be.
func Seal(doc *IdentityDocument, plaintext []byte) (*Envelope, error) {
	ek, err := mlkem.NewEncapsulationKey768(doc.KEMPubKey)
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

	n.mu.RLock()
	current, previous := n.kemPriv, n.previous
	n.mu.RUnlock()

	// Try the current key, then the just-rotated one, so a rotation mid-flight
	// does not drop legitimate traffic.
	for _, dk := range []*mlkem.DecapsulationKey768{current, previous} {
		if dk == nil {
			continue
		}
		sharedSecret, err := dk.Decapsulate(env.KEMCiphertext)
		if err != nil {
			continue
		}
		aead, err := channelAEAD(sharedSecret)
		if err != nil {
			return nil, err
		}
		pt, err := aead.Open(nil, env.Nonce, env.Ciphertext, envelopeAAD(env.KEMCiphertext, env.Timestamp))
		if err == nil {
			return pt, nil
		}
	}
	return nil, fmt.Errorf("envelope did not decrypt under any current node key")
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
