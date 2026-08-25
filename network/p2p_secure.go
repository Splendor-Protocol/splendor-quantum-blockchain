package network

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// Sealed P2P transport paths.
const (
	// PathIdentity serves a node's signed ML-DSA + ML-KEM identity document.
	PathIdentity = "/p2p/identity"
	// PathSealed accepts an encrypted envelope carrying a write-plane request.
	PathSealed = "/p2p/sealed"
)

// Sealed request kinds.
//
// Write kinds mutate this node and require the write-plane token. Read kinds
// only return chain data that is public once published, so they are sealed for
// confidentiality and peer authentication but are not token-gated: a syncing
// RPC node must be able to catch up without holding the validator's secret.
const (
	KindBlock    = "block"
	KindAnnounce = "announce"

	KindGetBlock  = "get_block"
	KindGetLatest = "get_latest"
)

// IsReadKind reports whether a sealed request only reads chain data.
func IsReadKind(kind string) bool {
	return kind == KindGetBlock || kind == KindGetLatest
}

// SealedRequest is the plaintext inside an Envelope.
//
// The write-plane token travels HERE rather than in an HTTP header. A header
// would be readable by anyone on the path, so the secret that authorises block
// ingest would be exposed on every push — encrypting the block body while
// leaking the credential that accepts it would be pointless.
type SealedRequest struct {
	Token string          `json:"token"`
	Kind  string          `json:"kind"`
	Body  json.RawMessage `json:"body"`

	// ReplyKEM is a one-shot ML-KEM-768 encapsulation key. When set, the
	// responder seals its reply to it. Absent for fire-and-forget pushes.
	ReplyKEM []byte `json:"reply_kem,omitempty"`
}

// GetBlockRequest is the body of a KindGetBlock query.
type GetBlockRequest struct {
	Height uint64 `json:"height"`
}

// ── Peer identity cache (client side) ────────────────────────────────────────

type cachedIdentity struct {
	doc      *IdentityDocument
	notAfter time.Time
}

// IdentityCache fetches and caches peers' signed identity documents, and pins
// each peer's node key on first contact.
//
// Pinning matters: verifying a document only proves the KEM key belongs to
// whoever signed it. On first contact we record that identity, and every later
// document from the same peer must match — so an attacker who takes over the
// network path later cannot silently substitute their own identity.
type IdentityCache struct {
	mu     sync.RWMutex
	docs   map[string]cachedIdentity // peer URL → document
	pinned map[string][]byte         // peer URL → pinned node pubkey
	client *http.Client
}

// NewIdentityCache creates an identity cache.
func NewIdentityCache() *IdentityCache {
	return &IdentityCache{
		docs:   make(map[string]cachedIdentity),
		pinned: make(map[string][]byte),
		client: &http.Client{Timeout: 5 * time.Second, CheckRedirect: noRedirect},
	}
}

// Pin records an expected node public key for a peer, so its identity is
// checked against operator-supplied truth instead of trust-on-first-use.
func (c *IdentityCache) Pin(peerURL string, nodePubKey []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pinned[peerURL] = nodePubKey
}

// Get returns a verified identity document for a peer, fetching it if needed.
func (c *IdentityCache) Get(peerURL string) (*IdentityDocument, error) {
	c.mu.RLock()
	entry, ok := c.docs[peerURL]
	pin := c.pinned[peerURL]
	c.mu.RUnlock()
	if ok && time.Now().Before(entry.notAfter) {
		return entry.doc, nil
	}

	resp, err := c.client.Get(peerURL + PathIdentity)
	if err != nil {
		return nil, fmt.Errorf("fetch peer identity: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("peer identity endpoint returned %d", resp.StatusCode)
	}

	var doc IdentityDocument
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("decode peer identity: %w", err)
	}
	if err := doc.Verify(pin); err != nil {
		return nil, fmt.Errorf("peer identity rejected: %w", err)
	}

	c.mu.Lock()
	if _, alreadyPinned := c.pinned[peerURL]; !alreadyPinned {
		// Trust on first use, then hold the peer to it.
		c.pinned[peerURL] = doc.NodePubKey
		log.Debug().Str("peer", peerURL).Msg("p2p: pinned peer node identity on first contact")
	}
	// Re-fetch a little before expiry so a push never races the rotation.
	refresh := time.Unix(doc.NotAfter, 0).Add(-1 * time.Minute)
	c.docs[peerURL] = cachedIdentity{doc: &doc, notAfter: refresh}
	c.mu.Unlock()

	return &doc, nil
}

// Forget drops a peer's cached document, forcing a re-fetch. Called when a
// sealed send fails, since the peer may have rotated early or restarted.
func (c *IdentityCache) Forget(peerURL string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.docs, peerURL)
}

// SendSealed encrypts a write-plane request to a peer and posts it.
func (c *IdentityCache) SendSealed(peerURL, token, kind string, body []byte) error {
	doc, err := c.Get(peerURL)
	if err != nil {
		return err
	}

	payload, err := json.Marshal(SealedRequest{Token: token, Kind: kind, Body: body})
	if err != nil {
		return fmt.Errorf("marshal sealed request: %w", err)
	}
	env, err := Seal(doc, payload)
	if err != nil {
		return fmt.Errorf("seal: %w", err)
	}
	wire, err := MarshalEnvelope(env)
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}

	resp, err := c.client.Post(peerURL+PathSealed, "application/json", bytes.NewReader(wire))
	if err != nil {
		c.Forget(peerURL)
		return fmt.Errorf("POST %s: %w", PathSealed, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		// A rejected envelope usually means our cached key is stale.
		c.Forget(peerURL)
		return fmt.Errorf("peer returned %d for sealed %s", resp.StatusCode, kind)
	}
	return nil
}

// QuerySealed performs a sealed request and returns the peer's sealed reply.
//
// Both legs are encrypted. The request is sealed to the peer's signed, pinned
// identity, so only that peer can read it; the reply is sealed to a one-shot
// key generated here, so only this request can read the answer. The reply is
// still content-verified by the caller — encryption authenticates the channel,
// not the chain data travelling over it.
func (c *IdentityCache) QuerySealed(peerURL, kind string, body []byte, maxReply int64) ([]byte, error) {
	doc, err := c.Get(peerURL)
	if err != nil {
		return nil, err
	}

	reply, err := NewEphemeralKEM()
	if err != nil {
		return nil, err
	}

	payload, err := json.Marshal(SealedRequest{
		Kind:     kind,
		Body:     body,
		ReplyKEM: reply.PublicKey(),
	})
	if err != nil {
		return nil, fmt.Errorf("marshal sealed query: %w", err)
	}
	env, err := Seal(doc, payload)
	if err != nil {
		return nil, fmt.Errorf("seal: %w", err)
	}
	wire, err := MarshalEnvelope(env)
	if err != nil {
		return nil, fmt.Errorf("marshal envelope: %w", err)
	}

	resp, err := c.client.Post(peerURL+PathSealed, "application/json", bytes.NewReader(wire))
	if err != nil {
		c.Forget(peerURL)
		return nil, fmt.Errorf("POST %s: %w", PathSealed, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		c.Forget(peerURL)
		return nil, fmt.Errorf("peer returned %d for sealed %s", resp.StatusCode, kind)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxReply))
	if err != nil {
		return nil, fmt.Errorf("read sealed reply: %w", err)
	}
	replyEnv, err := UnmarshalEnvelope(raw)
	if err != nil {
		return nil, fmt.Errorf("decode sealed reply: %w", err)
	}
	return reply.Open(replyEnv)
}
