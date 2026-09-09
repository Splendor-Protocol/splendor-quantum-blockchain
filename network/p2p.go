// Package network — P2P block broadcasting for Splendor Quantum Chain.
//
// Architecture: Push-based (like geth), not poll-based.
//
// When a validator produces a block:
//   1. It calls Broadcaster.Broadcast(block) immediately
//   2. The broadcaster sends POST /block to every known peer
//   3. Each receiving peer calls ExecuteBlock() immediately
//   4. The RPC node is always within milliseconds of the validator
//
// This is true realtime sync — no polling, no intervals.
// The RPC node's 1s polling loop is a fallback for missed blocks only.
//
// Peer announcement:
//   When a node starts and connects to a peer, it sends POST /peers/announce
//   with its own address. This makes bidirectional peer tracking work.
//   Validator can then also push blocks to the RPC node.
package network

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/buildinfo"
	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// ProducerKeyLookup resolves the ML-DSA-65 public key of a validator the
// chain's registry lists as ACTIVE, by address. nil means unknown. The node
// wires it to the executor's committed-state registry, which for the next
// block to apply is the parent state — the same set ExecuteBlock judges the
// block's certificate against.
type ProducerKeyLookup func(addr qcrypto.Address) []byte

// VerifyBlockProducer authenticates a block's signature before it is applied.
//
// The pinned genesis validator key is tried first and is the only key block 0
// may verify under. Above height 0, when the pinned key does not match and a
// resolver is given, the block is checked against the active-set key of the
// address its header names — bound by DeriveAddress(key) == ValidatorAddr, so
// a registry entry can never authenticate a block sealed by someone else. A
// producer that is neither is refused, which is what keeps a single-producer
// chain on exactly today's rule while a rotating BFT leader can be followed.
//
// Nothing here trusts the key a block carries: verifying a block against its
// own embedded key always succeeds and proves nothing. ExecuteBlock remains
// the authority and still verifies the certificate.
func VerifyBlockProducer(b *types.Block, trustedPK []byte, lookup ProducerKeyLookup) error {
	if len(trustedPK) == 0 {
		return fmt.Errorf("block %d: no trusted validator pubkey configured — refusing to authenticate it "+
			"(set --validator-pubkey-hex, or --genesis-hash to bootstrap it)", b.Header.Height)
	}
	pinErr := b.VerifySignature(trustedPK)
	if pinErr == nil {
		return nil
	}
	if b.Header.Height == 0 || lookup == nil {
		return fmt.Errorf("block %d: invalid validator signature: %w", b.Header.Height, pinErr)
	}
	key := lookup(b.Header.ValidatorAddr)
	if len(key) == 0 {
		return fmt.Errorf("block %d: producer %s is neither the trusted genesis validator nor in the active validator set",
			b.Header.Height, b.Header.ValidatorAddr)
	}
	pub, err := qcrypto.PublicKeyFromBytes(key)
	if err != nil {
		return fmt.Errorf("block %d: registry key for %s is invalid: %w", b.Header.Height, b.Header.ValidatorAddr, err)
	}
	if derived := qcrypto.DeriveAddress(pub); derived != b.Header.ValidatorAddr {
		return fmt.Errorf("block %d: registry key for %s derives to %s — refusing it",
			b.Header.Height, b.Header.ValidatorAddr, derived)
	}
	if err := b.VerifySignature(key); err != nil {
		return fmt.Errorf("block %d: invalid signature from active validator %s: %w",
			b.Header.Height, b.Header.ValidatorAddr, err)
	}
	return nil
}

// Broadcaster pushes newly produced blocks to all known peers immediately.
// This provides true realtime sync — peers receive blocks within milliseconds
// of the validator producing them, without any polling interval.
type Broadcaster struct {
	mu          sync.RWMutex
	peers       map[string]struct{} // peer RPC URLs
	validatorPK []byte
	client      *http.Client
	applyBlock  func(*types.Block) error // local apply callback (for loopback, if needed)

	// authToken is the shared write-plane token. On the encrypted path it is
	// sealed inside the envelope; on the plaintext fallback it is sent as the
	// X-Node-Auth header.
	authToken string

	// identities fetches and pins peers' signed ML-KEM keys for the encrypted
	// transport.
	identities *IdentityCache

	// allowPlaintext permits falling back to unencrypted POST /block when a peer
	// has no sealed transport. Off by default: an attacker who can block
	// /p2p/identity could otherwise force every push into plaintext, which
	// downgrades the channel exactly when someone is tampering with it.
	allowPlaintext bool
}

// noRedirect stops HTTP clients from following redirects. Peer/P2P requests must
// hit exactly the announced host — following a 3xx would let an approved public
// peer URL bounce the node to an internal/metadata address (SSRF), defeating the
// announce-time host validation. Returning ErrUseLastResponse surfaces the 3xx as
// the response, which callers treat as a non-200 failure.
func noRedirect(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }

// NewBroadcaster creates a new block broadcaster. authToken is the shared
// write-plane secret; peers reject pushes and announcements without it.
// allowPlaintext permits the unencrypted fallback when a peer exposes no
// sealed transport.
func NewBroadcaster(validatorPK []byte, authToken string, allowPlaintext bool) *Broadcaster {
	return &Broadcaster{
		peers:          make(map[string]struct{}),
		validatorPK:    validatorPK,
		authToken:      authToken,
		identities:     NewIdentityCache(),
		allowPlaintext: allowPlaintext,
		client: &http.Client{
			Timeout:       5 * time.Second,
			CheckRedirect: noRedirect,
			Transport:     SafeTransport(5 * time.Second),
		},
	}
}

// Identities exposes the peer identity cache so operators can pin known node
// keys instead of relying on trust-on-first-use.
func (b *Broadcaster) Identities() *IdentityCache { return b.identities }

// postAuthed sends an authenticated write-plane POST to a peer.
func (b *Broadcaster) postAuthed(url string, body []byte) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if b.authToken != "" {
		req.Header.Set("X-Node-Auth", b.authToken)
	}
	return b.client.Do(req)
}

// maxBroadcastPeers caps the broadcast fan-out set. /peers/announce is a public
// endpoint, so without a cap an attacker could grow this set unboundedly and turn
// each produced block into an amplified fan-out of outbound POSTs.
const maxBroadcastPeers = 128

// AddPeer registers a peer URL to receive broadcast blocks.
func (b *Broadcaster) AddPeer(url string) {
	if url == "" {
		return
	}
	// One spelling per peer. This set is fed by /peers/announce, so without it
	// an announcer can occupy every slot below with respellings of one address
	// — and each spelling would be pin-checked as if it were a separate peer.
	url = canonicalPeerURL(url)
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, exists := b.peers[url]; !exists && len(b.peers) >= maxBroadcastPeers {
		log.Warn().Str("peer", url).Int("max", maxBroadcastPeers).
			Msg("broadcaster: peer set full, ignoring new peer")
		return
	}
	b.peers[url] = struct{}{}
	log.Debug().Str("peer", url).Msg("broadcaster: peer added")
}

// RemovePeer removes a peer from the broadcast list.
func (b *Broadcaster) RemovePeer(url string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.peers, canonicalPeerURL(url))
}

// PeerCount returns the number of broadcast targets.
func (b *Broadcaster) PeerCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.peers)
}

// Broadcast sends a block to all known peers concurrently.
// Each peer receives the block via POST /block.
// Failed peers are logged but do not cause errors.
func (b *Broadcaster) Broadcast(block *types.Block) {
	data, err := json.Marshal(block)
	if err != nil {
		log.Error().Err(err).Msg("broadcaster: failed to marshal block")
		return
	}

	b.mu.RLock()
	peers := make([]string, 0, len(b.peers))
	for peer := range b.peers {
		peers = append(peers, peer)
	}
	b.mu.RUnlock()

	if len(peers) == 0 {
		return
	}

	log.Debug().
		Uint64("height", block.Header.Height).
		Int("peers", len(peers)).
		Msg("broadcaster: pushing block to peers")

	for _, peer := range peers {
		go func(peerURL string) {
			if err := b.sendBlock(peerURL, data); err != nil {
				log.Warn().Err(err).Str("peer", peerURL).Msg("broadcaster: block push failed")
			} else {
				log.Debug().Str("peer", peerURL).Uint64("height", block.Header.Height).
					Msg("broadcaster: block pushed")
			}
		}(peer)
	}
}

// sendBlock sends a serialised block to a single peer.
func (b *Broadcaster) sendBlock(peerURL string, blockData []byte) error {
	// Preferred path: ML-KEM-768 sealed envelope, which also hides the
	// write-plane token from anyone on the network path.
	sealErr := b.identities.SendSealed(peerURL, b.authToken, KindBlock, blockData)
	if sealErr == nil {
		return nil
	}
	// A refused pin is a decision, not a transport hiccup. The fallback below
	// carries no peer identity and puts the write-plane token in a header, so
	// retrying there would hand the block and the credential to precisely the
	// peer the operator declined to trust.
	if errors.Is(sealErr, ErrPeerNotPinned) {
		return sealErr
	}
	if !b.allowPlaintext {
		return fmt.Errorf("encrypted block push failed and plaintext fallback is disabled "+
			"(enable with --allow-plaintext-p2p if the peer predates the sealed transport): %w", sealErr)
	}

	log.Warn().Err(sealErr).Str("peer", peerURL).
		Msg("broadcaster: falling back to PLAINTEXT block push — token and block body are exposed on the wire")

	resp, err := b.postAuthed(peerURL+"/block", blockData)
	if err != nil {
		return fmt.Errorf("POST /block failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusServiceUnavailable {
		return fmt.Errorf("peer rejected block push (%d): check that both nodes share --rpc-auth-token", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("peer returned %d", resp.StatusCode)
	}
	return nil
}

// BroadcastSealed pushes a sealed write-plane payload (vote, proposal, …) to peers.
func (b *Broadcaster) BroadcastSealed(kind string, data []byte) {
	b.mu.RLock()
	peers := make([]string, 0, len(b.peers))
	for peer := range b.peers {
		peers = append(peers, peer)
	}
	b.mu.RUnlock()
	for _, peer := range peers {
		go func(peerURL string) {
			if err := b.identities.SendSealed(peerURL, b.authToken, kind, data); err != nil {
				log.Debug().Err(err).Str("peer", peerURL).Str("kind", kind).Msg("sealed push failed")
			}
		}(peer)
	}
}

// RunBroadcastLoop reads blocks from blockCh and broadcasts each one.
// Run in a goroutine alongside the validator consensus loop.
func (b *Broadcaster) RunBroadcastLoop(blockCh <-chan *types.Block) {
	for block := range blockCh {
		b.Broadcast(block)
	}
}

// AnnounceSelf announces this node's own address to all known peers.
// Call this after adding bootstrap peers so the peers can add us back
// (enabling bidirectional block propagation).
func (b *Broadcaster) AnnounceSelf(selfURL string, selfHeight uint64) {
	b.mu.RLock()
	peers := make([]string, 0, len(b.peers))
	for peer := range b.peers {
		peers = append(peers, peer)
	}
	b.mu.RUnlock()

	announcement := &PeerInfo{
		Addr:     selfURL,
		LastSeen: time.Now().Unix(),
		Height:   selfHeight,
		Version:  buildinfo.Version(),
	}
	data, _ := json.Marshal(announcement)

	for _, peer := range peers {
		go func(peerURL string) {
			if err := b.identities.SendSealed(peerURL, b.authToken, KindAnnounce, data); err == nil {
				log.Debug().Str("peer", peerURL).Str("self", selfURL).Msg("announce: sent (sealed)")
				return
			} else if errors.Is(err, ErrPeerNotPinned) {
				// Announcing in cleartext would leak the token to a peer this
				// node just refused to authenticate.
				log.Debug().Err(err).Str("peer", peerURL).Msg("announce: peer is not pinned")
				return
			} else if !b.allowPlaintext {
				log.Debug().Err(err).Str("peer", peerURL).Msg("announce: sealed send failed")
				return
			}
			resp, err := b.postAuthed(peerURL+"/peers/announce", data)
			if err != nil {
				log.Debug().Err(err).Str("peer", peerURL).Msg("announce: failed")
				return
			}
			defer resp.Body.Close()
			log.Debug().Str("peer", peerURL).Str("self", selfURL).Msg("announce: sent (plaintext)")
		}(peer)
	}
}

// HandleIncomingBlock processes a raw block JSON received from a peer.
// It authenticates the block (VerifyBlockProducer) and calls applyBlock.
func HandleIncomingBlock(body []byte, validatorPK []byte, lookup ProducerKeyLookup, applyBlock func(*types.Block) error) error {
	var block types.Block
	if err := json.Unmarshal(body, &block); err != nil {
		return fmt.Errorf("invalid block JSON: %w", err)
	}

	// Structural validation
	if err := block.Validate(); err != nil {
		return fmt.Errorf("block validation failed: %w", err)
	}

	// ML-DSA-65 signature verification is MANDATORY on the inbound push path.
	// An empty validatorPK previously meant "skip verification", which let any
	// unauthenticated caller POST a forged block to /block. We fail closed: a
	// node with no trusted validator pubkey refuses pushed blocks and relies on
	// the pull-sync path (which bootstraps the key from the genesis block)
	// instead. Operators can pin the key explicitly via --validator-pubkey-hex.
	if err := VerifyBlockProducer(&block, validatorPK, lookup); err != nil {
		return fmt.Errorf("refusing inbound block: %w", err)
	}

	// Apply to local chain
	if err := applyBlock(&block); err != nil {
		return fmt.Errorf("block application failed: %w", err)
	}

	log.Debug().
		Uint64("height", block.Header.Height).
		Str("hash", fmt.Sprintf("%x", block.Hash[:8])).
		Msg("p2p: block received and applied")

	return nil
}
