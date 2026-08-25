// Package network — Block sync protocol for Splendor Quantum Chain.
//
// When a node starts or falls behind, it needs to download blocks from peers.
// The sync protocol:
//   1. Queries /status on all known peers to find the best chain height.
//   2. Picks the peer with the highest height.
//   3. Downloads blocks one by one over the ML-KEM-768 sealed channel.
//   4. Validates each block's ML-DSA-65 signature and hash chain before applying.
//   5. Continues until the local height matches the best peer height.
//
// Catch-up uses the same encrypted transport as block push, so a node that
// falls behind does not drop back onto cleartext HTTP for the bulk of its
// traffic. Plaintext GET remains available only behind --allow-plaintext-p2p,
// for peers that predate the sealed transport.
//
// All downloaded blocks are verified — no block is trusted without a valid
// ML-DSA-65 signature from the registered genesis validator. Encryption
// authenticates the channel; the signature is what authenticates the chain.
package network

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// maxSyncBodyBytes bounds a block/genesis body pulled from a (possibly malicious)
// peer, so an unbounded JSON decode can't OOM the node. A full block is at most
// ~MaxTxPerBlock * max-tx-bytes; 128 MiB is a generous ceiling well above that.
const maxSyncBodyBytes = 128 << 20

// SyncConfig configures the block sync process.
type SyncConfig struct {
	// ValidatorPubKey is the genesis validator's ML-DSA-65 public key bytes.
	// Every downloaded block must be signed by this key.
	ValidatorPubKey []byte

	// MaxBlocksPerBatch is how many blocks to download per peer before switching.
	MaxBlocksPerBatch int

	// RequestTimeout is the timeout for individual block download requests.
	RequestTimeout time.Duration

	// RetryDelay is how long to wait before retrying a failed download.
	RetryDelay time.Duration

	// Identities pulls blocks over the ML-KEM-768 sealed channel. When nil the
	// fetcher can only use plaintext HTTP.
	Identities *IdentityCache

	// AllowPlaintext permits falling back to plaintext GET when a peer serves
	// no sealed transport. Off by default so the channel cannot be downgraded
	// by an attacker who simply refuses to answer /p2p/identity.
	AllowPlaintext bool
}

// DefaultSyncConfig returns sensible defaults.
func DefaultSyncConfig(validatorPubKey []byte) SyncConfig {
	return SyncConfig{
		ValidatorPubKey:   validatorPubKey,
		MaxBlocksPerBatch: 100,
		RequestTimeout:    15 * time.Second,
		RetryDelay:        2 * time.Second,
	}
}

// BlockFetcher downloads blocks from remote peers.
type BlockFetcher struct {
	cfg    SyncConfig
	client *http.Client
}

// NewBlockFetcher creates a fetcher with the given config.
func NewBlockFetcher(cfg SyncConfig) *BlockFetcher {
	return &BlockFetcher{
		cfg: cfg,
		client: &http.Client{
			Timeout:       cfg.RequestTimeout,
			CheckRedirect: noRedirect,
		},
	}
}

// fetchBlockJSON retrieves a block document from a peer.
//
// The sealed ML-KEM channel is tried first. Falling back to plaintext is opt-in
// because an attacker who can silence /p2p/identity would otherwise be able to
// downgrade every sync to cleartext just by refusing to answer.
func (f *BlockFetcher) fetchBlockJSON(peerAddr, kind string, body []byte, plainPath string) ([]byte, error) {
	if f.cfg.Identities != nil {
		raw, err := f.cfg.Identities.QuerySealed(peerAddr, kind, body, maxSyncBodyBytes)
		if err == nil {
			return raw, nil
		}
		if !f.cfg.AllowPlaintext {
			return nil, fmt.Errorf("sealed sync from %s failed and plaintext sync is disabled "+
				"(enable with --allow-plaintext-p2p if the peer predates the sealed transport): %w",
				peerAddr, err)
		}
		log.Debug().Err(err).Str("peer", peerAddr).
			Msg("sync: sealed fetch failed, falling back to plaintext")
	} else if !f.cfg.AllowPlaintext {
		return nil, fmt.Errorf("sync from %s refused: no sealed transport is configured and "+
			"plaintext sync is disabled", peerAddr)
	}

	resp, err := f.client.Get(peerAddr + plainPath)
	if err != nil {
		return nil, fmt.Errorf("fetch %s from %s: %w", plainPath, peerAddr, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%s not found at %s", plainPath, peerAddr)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: peer returned %d", plainPath, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxSyncBodyBytes))
}

// FetchGenesisValidatorPubKey fetches block 0 from a peer and extracts
// the validator's ML-DSA-65 public key. This is used by RPC nodes that
// don't have a local genesis.json — they bootstrap the pubkey from the chain.
func (f *BlockFetcher) FetchGenesisValidatorPubKey(peerAddr string) ([]byte, error) {
	req, err := json.Marshal(GetBlockRequest{Height: 0})
	if err != nil {
		return nil, err
	}
	raw, err := f.fetchBlockJSON(peerAddr, KindGetBlock, req, "/block/0")
	if err != nil {
		return nil, fmt.Errorf("fetch genesis block from %s: %w", peerAddr, err)
	}

	var block types.Block
	if err := json.Unmarshal(raw, &block); err != nil {
		return nil, fmt.Errorf("decode genesis block: %w", err)
	}

	// The genesis block's ValidatorSignature contains the pubkey implicitly,
	// but it's cleaner to get it from the state — the validator registered
	// their pubkey in the genesis allocation. Extract from block's ValidatorAddr.
	// Since we don't have state here, return the pubkey embedded in the block.
	if len(block.ValidatorPubKey) == 0 {
		return nil, fmt.Errorf("genesis block has no validator pubkey — validator must be running with a funded genesis")
	}
	return block.ValidatorPubKey, nil
}

// FetchBlock downloads a single block by height from the given peer.
// If ValidatorPubKey is empty, signature verification is skipped
// (the block will still be structurally validated).
// SECURITY: Always set ValidatorPubKey in production.
func (f *BlockFetcher) FetchBlock(peerAddr string, height uint64) (*types.Block, error) {
	req, err := json.Marshal(GetBlockRequest{Height: height})
	if err != nil {
		return nil, err
	}
	raw, err := f.fetchBlockJSON(peerAddr, KindGetBlock, req, fmt.Sprintf("/block/%d", height))
	if err != nil {
		return nil, fmt.Errorf("fetch block %d from %s: %w", height, peerAddr, err)
	}

	var block types.Block
	if err := json.Unmarshal(raw, &block); err != nil {
		return nil, fmt.Errorf("decode block %d: %w", height, err)
	}

	// Validate structural integrity
	if err := block.Validate(); err != nil {
		return nil, fmt.Errorf("block %d failed validation: %w", height, err)
	}

	// Verify ML-DSA-65 signature only if we have the pubkey.
	// If not set, try to learn it from the block itself (for genesis bootstrap).
	if len(f.cfg.ValidatorPubKey) > 0 {
		if err := block.VerifySignature(f.cfg.ValidatorPubKey); err != nil {
			return nil, fmt.Errorf("block %d: invalid validator signature: %w", height, err)
		}
	} else if len(block.ValidatorPubKey) > 0 {
		// First block with an embedded pubkey — learn and store for future use
		f.cfg.ValidatorPubKey = block.ValidatorPubKey
		log.Info().
			Str("pubkey_prefix", fmt.Sprintf("%x", block.ValidatorPubKey[:8])).
			Msg("sync: learned validator pubkey from first block")
		if err := block.VerifySignature(f.cfg.ValidatorPubKey); err != nil {
			return nil, fmt.Errorf("block %d: self-signed block failed verification: %w", height, err)
		}
	} else {
		// Fail closed (matching the push path): never APPLY a block we can't
		// authenticate. A non-genesis block carries no embedded pubkey, so without
		// a pinned key we'd otherwise accept an attacker-fabricated chain (TOFU).
		return nil, fmt.Errorf("block %d: no trusted validator pubkey — refusing to apply an unverified block (set --validator-pubkey-hex)", height)
	}

	return &block, nil
}

// FetchLatestBlock fetches the latest block from a peer.
func (f *BlockFetcher) FetchLatestBlock(peerAddr string) (*types.Block, error) {
	raw, err := f.fetchBlockJSON(peerAddr, KindGetLatest, []byte(`{}`), "/block/latest")
	if err != nil {
		return nil, fmt.Errorf("fetch latest block from %s: %w", peerAddr, err)
	}

	var block types.Block
	if err := json.Unmarshal(raw, &block); err != nil {
		return nil, fmt.Errorf("decode latest block: %w", err)
	}

	// Verify signature; fail closed with no pinned key (matches FetchBlock).
	if len(f.cfg.ValidatorPubKey) == 0 {
		return nil, fmt.Errorf("latest block: no trusted validator pubkey")
	}
	if err := block.VerifySignature(f.cfg.ValidatorPubKey); err != nil {
		return nil, fmt.Errorf("latest block: invalid validator signature: %w", err)
	}

	return &block, nil
}

// Syncer orchestrates the full block sync process.
type Syncer struct {
	fetcher     *BlockFetcher
	pool        *PeerPool
	localHeight func() uint64                // returns current local chain height
	applyBlock  func(*types.Block) error     // applies a validated block to local chain
}

// NewSyncer creates a syncer.
func NewSyncer(
	cfg SyncConfig,
	pool *PeerPool,
	localHeight func() uint64,
	applyBlock func(*types.Block) error,
) *Syncer {
	s := &Syncer{
		fetcher:     NewBlockFetcher(cfg),
		pool:        pool,
		localHeight: localHeight,
		applyBlock:  applyBlock,
	}

	// If no validator pubkey configured, try to learn it from peers at startup
	if len(cfg.ValidatorPubKey) == 0 && len(pool.Addrs()) > 0 {
		go s.bootstrapValidatorPubKey()
	}

	return s
}

// bootstrapValidatorPubKey fetches the validator's pubkey from the first
// available peer's genesis block. This lets RPC nodes start without any
// manual pubkey configuration.
func (s *Syncer) bootstrapValidatorPubKey() {
	peers := s.pool.Addrs()
	for _, peer := range peers {
		pubkey, err := s.fetcher.FetchGenesisValidatorPubKey(peer)
		if err != nil {
			log.Debug().Err(err).Str("peer", peer).Msg("sync: could not fetch pubkey from peer")
			continue
		}
		if len(pubkey) > 0 {
			s.fetcher.cfg.ValidatorPubKey = pubkey
			log.Info().
				Str("peer", peer).
				Str("pubkey_prefix", fmt.Sprintf("%x", pubkey[:8])).
				Msg("sync: bootstrapped validator pubkey from peer's genesis block")
			return
		}
	}
	log.Warn().Msg("sync: could not bootstrap validator pubkey — signature verification will be skipped until genesis block is downloaded")
}

// SyncOnce performs one sync pass: check peers, download missing blocks.
// Returns the number of blocks applied, and any error.
func (s *Syncer) SyncOnce() (int, error) {
	localH := s.localHeight()
	bestH := s.pool.BestPeerHeight()

	if bestH <= localH {
		return 0, nil // already synced
	}

	log.Info().
		Uint64("local", localH).
		Uint64("best_peer", bestH).
		Uint64("behind_by", bestH-localH).
		Msg("sync: starting catchup")

	// Find best peer
	peers := s.pool.PeersAboveHeight(localH)
	if len(peers) == 0 {
		return 0, fmt.Errorf("sync: no peers above local height %d", localH)
	}
	peerAddr := peers[0] // pick first (TODO: pick fastest)

	applied := 0
	for h := localH + 1; h <= bestH; h++ {
		block, err := s.fetcher.FetchBlock(peerAddr, h)
		if err != nil {
			log.Warn().Err(err).Uint64("height", h).Str("peer", peerAddr).
				Msg("sync: block fetch failed, will retry")
			time.Sleep(s.fetcher.cfg.RetryDelay)
			// Try next peer if available
			if len(peers) > 1 {
				peerAddr = peers[1]
				peers = peers[1:]
			}
			block, err = s.fetcher.FetchBlock(peerAddr, h)
			if err != nil {
				return applied, fmt.Errorf("sync: failed to fetch block %d: %w", h, err)
			}
		}

		if err := s.applyBlock(block); err != nil {
			return applied, fmt.Errorf("sync: failed to apply block %d: %w", h, err)
		}

		// Update peer's known height
		s.pool.UpdatePeerHeight(peerAddr, h)
		applied++

		if applied%10 == 0 {
			log.Info().
				Uint64("height", h).
				Uint64("target", bestH).
				Int("applied", applied).
				Msg("sync: progress")
		}
	}

	log.Info().
		Int("applied", applied).
		Uint64("final_height", bestH).
		Msg("sync: catchup complete")

	return applied, nil
}

// IsSynced returns true if the local chain is within 1 block of the best peer.
func (s *Syncer) IsSynced() bool {
	localH := s.localHeight()
	bestH := s.pool.BestPeerHeight()
	return bestH <= localH+1
}
