package network

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"sync"
	"time"

	"golang.org/x/crypto/sha3"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
)

// Signed peer announcements.
//
// POST /peers/announce was authorised by the shared write-plane token. That
// token says "someone who holds the cluster secret sent this", which is not the
// question the handler needs answered — an announcement adds an outbound target
// for block pushes and sync GETs, so what matters is which node is claiming an
// address. A symmetric secret cannot answer that: every node holding it can
// announce as any address, and one leaked copy anywhere lets an attacker steer
// the peer set of every node in the cluster, with no way to tell which node the
// forgery came from or to revoke it without rotating the secret everywhere.
//
// A signature under the announcer's own ML-DSA-65 node identity — the same key
// the sealed transport already pins — makes the announcement speak for exactly
// one node. Compromise is contained to that node and revoked by unpinning it.
//
// The signature alone still replays, so the signed payload carries a timestamp
// and a nonce: the timestamp bounds how long a captured announcement is worth
// anything, and the nonce cache rejects reuse inside that window.

const (
	// announceMaxSkew is how far an announcement's timestamp may sit from local
	// time in either direction. It has to absorb ordinary clock drift between
	// hosts while keeping the replay window short; a captured announcement is
	// worthless outside it.
	announceMaxSkew = 2 * time.Minute

	// announceNonceTTL is how long a spent nonce is remembered. It must exceed
	// the full accept window (skew in both directions), or a replay could be
	// accepted again after its nonce was forgotten but before its timestamp
	// went stale.
	announceNonceTTL = 2*announceMaxSkew + time.Minute

	announceNonceLen = 16
)

// SignedAnnounce is a peer announcement bound to the announcing node's key.
type SignedAnnounce struct {
	Peer PeerInfo `json:"peer"`

	// NodePubKey is the announcer's long-term ML-DSA-65 identity key — the same
	// key served at /p2p/identity and pinned by IdentityCache.
	NodePubKey []byte `json:"node_pubkey"`

	Timestamp int64  `json:"timestamp"`
	Nonce     []byte `json:"nonce"`
	Signature []byte `json:"signature"`
}

// announceDigest is the message actually signed.
//
// Every field the receiver acts on is covered, with each length-prefixed so
// that no two different announcements can produce the same bytes by shifting a
// boundary — an address ending in digits and a height, for instance.
func announceDigest(p PeerInfo, nodePubKey []byte, ts int64, nonce []byte) []byte {
	h := sha3.New256()
	writeField := func(b []byte) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(b)))
		h.Write(n[:])
		h.Write(b)
	}
	var num [8]byte

	writeField([]byte("SPLENDOR-QC-ANNOUNCE-v1"))
	writeField([]byte(p.Addr))
	writeField([]byte(p.Version))
	binary.BigEndian.PutUint64(num[:], p.Height)
	writeField(num[:])
	writeField(nodePubKey)
	binary.BigEndian.PutUint64(num[:], uint64(ts))
	writeField(num[:])
	writeField(nonce)

	return h.Sum(nil)
}

// SignAnnounce produces an announcement signed by this node's identity key.
func SignAnnounce(id *NodeIdentity, self PeerInfo) (*SignedAnnounce, error) {
	if id == nil {
		return nil, fmt.Errorf("signing an announcement requires a node identity")
	}
	pub, priv := id.SigningKeys()
	if pub == nil || priv == nil {
		return nil, fmt.Errorf("node identity has no ML-DSA keypair")
	}

	nonce := make([]byte, announceNonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate announce nonce: %w", err)
	}
	ts := time.Now().Unix()
	pubBytes := pub.Bytes()

	sig, err := qcrypto.Sign(priv, announceDigest(self, pubBytes, ts, nonce), qcrypto.CtxPeerHandshake)
	if err != nil {
		return nil, fmt.Errorf("sign announcement: %w", err)
	}
	return &SignedAnnounce{
		Peer:       self,
		NodePubKey: pubBytes,
		Timestamp:  ts,
		Nonce:      nonce,
		Signature:  sig,
	}, nil
}

// Verify checks that the announcement was signed by the key it presents, is
// fresh, and has not been seen before.
//
// It deliberately does NOT establish that NodePubKey really owns Peer.Addr.
// Nothing in a self-signed message can: an attacker can sign a truthful-looking
// claim about someone else's address with a key it does generate. What this
// proves is that the announcement is attributable and unreplayable. Ownership
// is settled later, by IdentityCache, when the node actually connects to that
// address and checks the key the address serves against its pin — a direct
// connection is the only place that question can honestly be answered.
func (sa *SignedAnnounce) Verify(seen *AnnounceNonceCache, now time.Time) error {
	if sa == nil {
		return fmt.Errorf("missing announcement")
	}
	if sa.Peer.Addr == "" {
		return fmt.Errorf("peer addr required")
	}
	if len(sa.Nonce) != announceNonceLen {
		return fmt.Errorf("nonce must be %d bytes", announceNonceLen)
	}

	skew := now.Sub(time.Unix(sa.Timestamp, 0))
	if skew < 0 {
		skew = -skew
	}
	if skew > announceMaxSkew {
		return fmt.Errorf("announcement timestamp is %s out of date", skew.Round(time.Second))
	}

	pub, err := qcrypto.PublicKeyFromBytes(sa.NodePubKey)
	if err != nil {
		return fmt.Errorf("invalid node public key: %w", err)
	}
	digest := announceDigest(sa.Peer, sa.NodePubKey, sa.Timestamp, sa.Nonce)
	if !qcrypto.Verify(pub, digest, qcrypto.CtxPeerHandshake, sa.Signature) {
		return fmt.Errorf("announcement signature does not verify")
	}

	// Last, so a replayed nonce is only burned by an otherwise-valid message.
	// Recording it earlier would let anyone spend a nonce with a junk signature
	// and knock out the genuine announcement carrying it.
	if seen != nil && !seen.Use(sa.Nonce, now) {
		return fmt.Errorf("announcement replayed")
	}
	return nil
}

// AnnounceNonceCache remembers recently used announcement nonces.
type AnnounceNonceCache struct {
	mu   sync.Mutex
	seen map[[announceNonceLen]byte]time.Time
}

// NewAnnounceNonceCache creates an empty nonce cache.
func NewAnnounceNonceCache() *AnnounceNonceCache {
	return &AnnounceNonceCache{seen: make(map[[announceNonceLen]byte]time.Time)}
}

// Use records a nonce, reporting false if it was already spent.
func (c *AnnounceNonceCache) Use(nonce []byte, now time.Time) bool {
	if len(nonce) != announceNonceLen {
		return false
	}
	var key [announceNonceLen]byte
	copy(key[:], nonce)

	c.mu.Lock()
	defer c.mu.Unlock()

	// Sweep on write. Entries are only reachable for announceNonceTTL and the
	// map is bounded by the announce rate over that window, so a background
	// goroutine would buy nothing.
	for k, t := range c.seen {
		if now.Sub(t) > announceNonceTTL {
			delete(c.seen, k)
		}
	}
	if _, dup := c.seen[key]; dup {
		return false
	}
	c.seen[key] = now
	return true
}
