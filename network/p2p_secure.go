package network

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
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
//
// First contact is the weak moment: an attacker already on the path when the
// node boots gets pinned as the peer. RequirePins closes it by demanding the
// operator name each peer's key up front. A pin that is present is always
// enforced, required or not — a mismatch is a rejection either way.
type IdentityCache struct {
	mu     sync.RWMutex
	docs   map[string]cachedIdentity // peer URL → document
	pinned map[string][]byte         // peer URL → pinned node pubkey
	client *http.Client

	// requirePins turns trust-on-first-use off entirely: a peer with no
	// operator-supplied pin is refused instead of being pinned to whatever it
	// serves. See RequirePins.
	requirePins bool

	// warned remembers which peers have already been reported as unpinned, so
	// the refusal is shouted once per peer rather than once per produced block.
	warned map[string]struct{}
}

// ErrPeerNotPinned is returned when pins are required and a peer has none.
//
// It is a sentinel because callers must be able to tell a policy refusal apart
// from a transport failure: a peer we refused for being unauthenticated must
// never be retried over a channel that authenticates it even less, such as the
// plaintext fallback.
var ErrPeerNotPinned = errors.New("peer node identity is not pinned")

// maxWarnedPeers bounds the log-once set below. Peer URLs arrive from places
// this node does not control — a /peers/announce body, another peer's /peers
// list — so a map keyed by them and never emptied is a leak whose rate an
// attacker picks.
const maxWarnedPeers = 1024

// canonicalPeerURL reduces a peer's base URL to a single spelling.
//
// A pin is the operator's statement about a HOST, but the map enforcing it is
// keyed by a STRING, and peer URLs reach this node from untrusted places. Left
// literal, "HTTP://host" and "http://host/" are separate keys for the same box:
// the pinned key is never consulted and trust-on-first-use quietly pins whoever
// answers, so a pin the operator did supply is bypassed by respelling it.
//
// The dial uses the canonical form for the same reason it must: a base URL kept
// with its trailing slash builds "host//p2p/identity", which a peer's mux answers
// with a redirect the P2P client refuses to follow (see noRedirect) — the sealed
// channel then fails for a reason that looks like the peer being down.
func canonicalPeerURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		// Nothing to reason about — keep the caller's spelling rather than
		// invent one. It simply will not match a pin, which is the safe way to
		// be wrong.
		return raw
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	// A default port is the same endpoint as no port at all.
	if port := u.Port(); (u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443") {
		host := u.Hostname()
		if strings.Contains(host, ":") {
			host = "[" + host + "]" // IPv6 literals lose their brackets to Hostname()
		}
		u.Host = host
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	// A peer base URL carries neither of these, and both would land in the
	// middle of the concatenated path.
	u.RawQuery, u.Fragment, u.ForceQuery = "", "", false
	return u.String()
}

// NewIdentityCache creates an identity cache.
func NewIdentityCache() *IdentityCache {
	return &IdentityCache{
		docs:   make(map[string]cachedIdentity),
		pinned: make(map[string][]byte),
		warned: make(map[string]struct{}),
		client: &http.Client{Timeout: 5 * time.Second, CheckRedirect: noRedirect},
	}
}

// Pin records an expected node public key for a peer, so its identity is
// checked against operator-supplied truth instead of trust-on-first-use.
//
// An empty key is refused rather than stored. Stored, it would be the worst of
// both states: Verify treats a zero-length expectation as "no expectation" and
// accepts any identity, while the peer looks pinned and so is never pinned on
// first contact either — the peer would stay permanently unauthenticated under
// a name that says otherwise.
func (c *IdentityCache) Pin(peerURL string, nodePubKey []byte) error {
	if len(nodePubKey) == 0 {
		return fmt.Errorf("refusing to pin %s to an empty node public key", peerURL)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pinned[canonicalPeerURL(peerURL)] = nodePubKey
	return nil
}

// RequirePins makes an operator-supplied pin mandatory for every peer.
//
// Trust-on-first-use is only safe if the first contact is honest, and nothing
// tells you whether it was. An attacker sitting on the path at bootstrap gets
// pinned as the peer and keeps that position for the life of the process — the
// pin then works against the operator, holding the node to the impostor. With
// this set, an unpinned peer is simply not a peer.
//
// This governs who this node TALKS TO. Inbound traffic is a different boundary
// and is not pinned: a sealed envelope carries no sender identity — anyone may
// seal to a published KEM key — so ingest stays authenticated by the write-plane
// token and by the mandatory validator signature on the block itself.
func (c *IdentityCache) RequirePins(required bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requirePins = required
}

// PinsRequired reports whether unpinned peers are refused.
func (c *IdentityCache) PinsRequired() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.requirePins
}

// Get returns a verified identity document for a peer, fetching it if needed.
func (c *IdentityCache) Get(peerURL string) (*IdentityDocument, error) {
	peerURL = canonicalPeerURL(peerURL)

	c.mu.RLock()
	entry, ok := c.docs[peerURL]
	pin := c.pinned[peerURL]
	requirePins := c.requirePins
	c.mu.RUnlock()

	// Refuse before the peer is even asked for a document. This is the single
	// point where a peer identity becomes trusted — every sealed path (block
	// push, announce, catch-up query) reaches the network through here — so
	// enforcing anywhere else would leave a way in.
	if requirePins && len(pin) == 0 {
		c.warnUnpinned(peerURL)
		// Say exactly how to obtain the value. %s serves node_pubkey as BASE64,
		// not hex, so an operator told only to "supply the pubkey hex" copies the
		// wrong encoding and gets a pin that never matches.
		return nil, fmt.Errorf("%w: %s — supply --peer-pin %s=<hex>, where <hex> is the peer's "+
			"node_pubkey from %s decoded from base64 to hex:\n"+
			"    curl -fsS %s | python3 -c \"import base64,json,sys;print(base64.b64decode(json.load(sys.stdin)['node_pubkey']).hex())\"\n"+
			"or drop --require-peer-pins to fall back to trust-on-first-use",
			ErrPeerNotPinned, peerURL, peerURL, peerURL+PathIdentity, peerURL+PathIdentity)
	}

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
	if len(c.pinned[peerURL]) == 0 {
		// Trust on first use, then hold the peer to it. Unreachable while pins
		// are required — that check above returns before any fetch. Keyed on
		// length, not presence, so an empty entry can never stand in for a pin.
		c.pinned[peerURL] = doc.NodePubKey
		log.Debug().Str("peer", peerURL).Msg("p2p: pinned peer node identity on first contact")
	}
	// Re-fetch a little before expiry so a push never races the rotation.
	refresh := time.Unix(doc.NotAfter, 0).Add(-1 * time.Minute)
	c.docs[peerURL] = cachedIdentity{doc: &doc, notAfter: refresh}
	c.mu.Unlock()

	return &doc, nil
}

// warnUnpinned logs a refused peer once. A validator broadcasts every block, so
// warning on each refusal would bury the message in its own repetition; once per
// peer keeps the operator's missing --peer-pin visible in the log.
func (c *IdentityCache) warnUnpinned(peerURL string) {
	c.mu.Lock()
	_, seen := c.warned[peerURL]
	if !seen {
		if len(c.warned) >= maxWarnedPeers {
			// Past the cap, stay quiet rather than grow: the caller still gets
			// the refusal as an error, and a node refusing this many distinct
			// peers has already been told what it needs to know.
			seen = true
		} else {
			c.warned[peerURL] = struct{}{}
		}
	}
	c.mu.Unlock()
	if seen {
		return
	}
	log.Warn().Str("peer", peerURL).Str("identity_url", peerURL+PathIdentity).
		Msg("p2p: REFUSED unpinned peer — --require-peer-pins is set and this peer has no " +
			"--peer-pin, so no blocks will be pushed to it and no blocks will be pulled from it")
}

// Forget drops a peer's cached document, forcing a re-fetch. Called when a
// sealed send fails, since the peer may have rotated early or restarted.
func (c *IdentityCache) Forget(peerURL string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.docs, canonicalPeerURL(peerURL))
}

// SendSealed encrypts a write-plane request to a peer and posts it.
func (c *IdentityCache) SendSealed(peerURL, token, kind string, body []byte) error {
	// The same spelling that was pin-checked is the one dialled below.
	peerURL = canonicalPeerURL(peerURL)
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
	peerURL = canonicalPeerURL(peerURL)
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
