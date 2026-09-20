package network

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
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
	KindBlock       = "block"
	KindAnnounce    = "announce"
	KindVote        = "vote"
	KindProposal    = "proposal"
	KindRoundChange = "round_change"
	// KindTx relays a transaction one hop to the node's peers.
	//
	// Only the leader of a height puts transactions into a block, and there
	// was no path from one mempool to another: a transaction submitted to any
	// node other than the current leader — an RPC node, or the validator that
	// happens not to be leading — simply never landed. That is how an operator
	// registering a new validator watched a correctly signed transaction go
	// nowhere. Relayed transactions are never re-relayed, so the fan-out is
	// one hop and cannot storm.
	KindTx = "tx"

	KindGetBlock  = "get_block"
	KindGetLatest = "get_latest"
	// KindGetStateSnapshot asks for the peer's committed state at its head,
	// for a node joining a chain whose history it cannot replay. Public data
	// like a block, so not token-gated; served sealed only.
	KindGetStateSnapshot = "get_state_snapshot"
	// KindGetStateSnapshotChunk pages through the same state one bounded
	// slice of leaves at a time (see GetStateSnapshotChunkRequest), so a
	// state past MaxStateSnapshotBytes does not require one node to hold the
	// whole thing in memory and the wire body at once. Same trust and
	// read-plane rules as KindGetStateSnapshot: sealed only, no token.
	KindGetStateSnapshotChunk = "get_state_snapshot_chunk"
	// KindGetObjectManifest asks for the SQCO encoding of a coded object's
	// signed manifest (served by default behind --object-codec, disabled
	// with --object-codec=none). Public once published, so sealed but not
	// token-gated.
	KindGetObjectManifest = "get_object_manifest_v1"
	// KindGetObjectSymbol asks for one authenticated symbol proof of that
	// object. Same trust and read-plane rules as KindGetObjectManifest.
	KindGetObjectSymbol = "get_object_symbol_v1"
	// KindGetBlockBatch asks for a coded batch of consecutive blocks. The
	// reply is the batch's object manifest; the block bytes travel as coded
	// symbols over KindGetObjectSymbol. Same trust and read-plane rules as
	// KindGetObjectManifest.
	KindGetBlockBatch = "get_block_batch_v1"
)

// IsReadKind reports whether a sealed request only reads chain data.
func IsReadKind(kind string) bool {
	return kind == KindGetBlock || kind == KindGetLatest ||
		kind == KindGetStateSnapshot || kind == KindGetStateSnapshotChunk ||
		kind == KindGetObjectManifest || kind == KindGetObjectSymbol ||
		kind == KindGetBlockBatch
}

// GetObjectManifestRequest is the body of a KindGetObjectManifest query.
// DescriptorID empty means the peer's current object.
type GetObjectManifestRequest struct {
	DescriptorID []byte `json:"descriptor_id,omitempty"`
}

// GetObjectSymbolRequest is the body of a KindGetObjectSymbol query. Generation
// selects which generation of a multi-generation object the index belongs to;
// 0 is the first (and only) generation of a single-generation object.
type GetObjectSymbolRequest struct {
	DescriptorID []byte `json:"descriptor_id"`
	Generation   uint32 `json:"generation"`
	Index        uint32 `json:"index"`
}

// GetStateSnapshotChunkRequest is the body of a KindGetStateSnapshotChunk
// query. AfterKey is nil/empty for the first chunk; every later request
// carries the NextKey the previous chunk returned.
type GetStateSnapshotChunkRequest struct {
	// SessionID pins all pages to one immutable snapshot; a client sends it
	// back on every page after the first.
	SessionID string `json:"session_id,omitempty"`
	AfterKey  []byte `json:"after_key,omitempty"`
	MaxLeaves int    `json:"max_leaves"`
}

// StateSnapshotChunk is one page of a chunked state transfer. Height must be
// identical across every chunk of one transfer; a change mid-transfer means
// the state moved (a validator committed) and the caller must abort and
// retry the whole thing, since a state_root check spanning two different
// heights can never legitimately pass.
type StateSnapshotChunk struct {
	SessionID string            `json:"session_id,omitempty"`
	Height    uint64            `json:"height"`
	Block     *types.Block      `json:"block,omitempty"` // set only on the first chunk
	Leaves    []types.StateLeaf `json:"leaves"`
	NextKey   []byte            `json:"next_key,omitempty"`
	Done      bool              `json:"done"`
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

// GetBlockBatchRequest is the body of a KindGetBlockBatch query: a coded
// batch of count consecutive blocks starting at FromHeight. The reply is the
// SQCO object manifest for the batch; the blocks themselves travel as coded
// symbol proofs over get_object_symbol_v1.
type GetBlockBatchRequest struct {
	FromHeight uint64 `json:"from_height"`
	Count      int    `json:"count"`
}

// MaxCodedBlockBatchBlocks bounds a coded block-batch request on BOTH sides:
// the serve side refuses to encode an unbounded object on demand, and the
// fetch side refuses a count that could overflow the fromHeight+count-1
// anchor arithmetic.
const MaxCodedBlockBatchBlocks = 128

// ValidateBlockBatchRange is the single guard for a get_block_batch_v1 range,
// shared by the fetch side (before any network work), the serve side (before
// any block lookup) and the fuzz targets. A count outside
// [1, MaxCodedBlockBatchBlocks], or a range whose end (fromHeight+count)
// would wrap uint64, is refused: the serve side iterates h in
// [fromHeight, fromHeight+count), so a wrap would silently truncate the
// batch (or worse, let a forged anchor name the wrong range).
func ValidateBlockBatchRange(fromHeight uint64, count int) error {
	if count < 1 || count > MaxCodedBlockBatchBlocks {
		return fmt.Errorf("block batch count %d outside [1,%d]", count, MaxCodedBlockBatchBlocks)
	}
	if fromHeight > math.MaxUint64-uint64(count) {
		return fmt.Errorf("block batch range [%d, %d) overflows uint64", fromHeight, fromHeight+uint64(count))
	}
	return nil
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

	// codedRefused remembers peers that answered a coded-object probe with a
	// refusal. Only peers whose identity document predates the capability
	// block (no declaration) are remembered — a declared refusal is handled
	// by the signed capability itself, and a declared service that fails
	// transiently must not be poisoned. Entries expire after
	// codedProbeRefusalTTL.
	codedRefused map[string]time.Time
}

// codedProbeRefusalTTL is how long a coded-object probe refusal is remembered
// for a peer that made no capability declaration, so the node does not pay a
// wasted sealed request on every join against legacy peers. A var (not a
// const) so tests can shrink it; never mutated in production.
var codedProbeRefusalTTL = 2 * time.Minute

// maxCodedRefusedPeers bounds the refusal map, whose keys are peer URLs that
// arrive from untrusted places.
const maxCodedRefusedPeers = 256

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
		client: &http.Client{
			Timeout:       5 * time.Second,
			CheckRedirect: noRedirect,
			Transport:     SafeTransport(5 * time.Second),
		},
		codedRefused: make(map[string]time.Time),
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

// NoteCodedRefusal records that a peer answered a coded-object probe with a
// refusal. Callers must only note refusals for peers whose identity document
// made no capability declaration; a declared service that fails transiently
// must not be poisoned, and a declared absence never gets probed at all.
func (c *IdentityCache) NoteCodedRefusal(peerURL string) {
	peerURL = canonicalPeerURL(peerURL)
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pruneCodedRefusedLocked(now)
	if _, ok := c.codedRefused[peerURL]; ok {
		c.codedRefused[peerURL] = now.Add(codedProbeRefusalTTL)
		return
	}
	if len(c.codedRefused) >= maxCodedRefusedPeers {
		// Evict the entry expiring soonest; the map's keys are peer URLs from
		// untrusted places, so it must never grow past the cap.
		var oldestKey string
		var oldestExp time.Time
		for k, exp := range c.codedRefused {
			if oldestKey == "" || exp.Before(oldestExp) {
				oldestKey, oldestExp = k, exp
			}
		}
		delete(c.codedRefused, oldestKey)
	}
	c.codedRefused[peerURL] = now.Add(codedProbeRefusalTTL)
}

// CodedRecentlyRefused reports whether a coded-object probe to this peer was
// recently refused, so the caller can skip the wasted request.
func (c *IdentityCache) CodedRecentlyRefused(peerURL string) bool {
	peerURL = canonicalPeerURL(peerURL)
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	exp, ok := c.codedRefused[peerURL]
	if !ok {
		return false
	}
	if now.After(exp) {
		delete(c.codedRefused, peerURL)
		return false
	}
	return true
}

// ForgetCodedRefusal clears a peer's refusal entry, e.g. after a successful
// coded transfer or a fresh capability declaration.
func (c *IdentityCache) ForgetCodedRefusal(peerURL string) {
	peerURL = canonicalPeerURL(peerURL)
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.codedRefused, peerURL)
}

func (c *IdentityCache) pruneCodedRefusedLocked(now time.Time) {
	for k, exp := range c.codedRefused {
		if now.After(exp) {
			delete(c.codedRefused, k)
		}
	}
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
	return c.QuerySealedContext(context.Background(), peerURL, kind, body, maxReply)
}

// QuerySealedContext is QuerySealed with a cancellable request context.
func (c *IdentityCache) QuerySealedContext(ctx context.Context, peerURL, kind string, body []byte, maxReply int64) ([]byte, error) {
	return c.querySealed(ctx, c.client, peerURL, kind, body, maxReply)
}

// QuerySealedTimeout is QuerySealed with its own request timeout, for the one
// read whose reply is legitimately large — a state snapshot — and would never
// finish inside the 5 s the block-sized queries get.
func (c *IdentityCache) QuerySealedTimeout(peerURL, kind string, body []byte, maxReply int64, timeout time.Duration) ([]byte, error) {
	client := &http.Client{
		Timeout:       timeout,
		CheckRedirect: noRedirect,
		Transport:     SafeTransport(timeout),
	}
	return c.querySealed(context.Background(), client, peerURL, kind, body, maxReply)
}

func (c *IdentityCache) querySealed(ctx context.Context, client *http.Client, peerURL, kind string, body []byte, maxReply int64) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
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

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, peerURL+PathSealed, bytes.NewReader(wire))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
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
