// Package network — Block sync protocol for Splendor Quantum Chain.
//
// When a node starts or falls behind, it needs to download blocks from peers.
// The sync protocol:
//  1. Queries /status on all known peers to find the best chain height.
//  2. Picks the peer with the highest height.
//  3. Downloads blocks one by one over the ML-KEM-768 sealed channel.
//  4. Validates each block's ML-DSA-65 signature and hash chain before applying.
//  5. Continues until the local height matches the best peer height.
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
	"errors"
	"fmt"
	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
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

	// GenesisHash pins the expected hash of block 0. It is the only way a node
	// with no ValidatorPubKey may safely learn one from the network: block 0 is
	// fetched, its hash is checked against this pin, and only then is its
	// embedded validator key trusted for every later block.
	//
	// Without a pin, a node that adopts whatever key the first peer offers is
	// trusting that peer completely — the peer can serve a chain it signed
	// itself, and every signature will verify against its own key. Leave this
	// zero only when ValidatorPubKey is supplied directly.
	GenesisHash [32]byte

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
			Transport:     SafeTransport(cfg.RequestTimeout),
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
		// Pulling in cleartext from a peer we refused to authenticate would
		// undo the refusal: the pin says this is not the peer we meant, and the
		// fallback cannot tell us otherwise.
		if errors.Is(err, ErrPeerNotPinned) {
			return nil, err
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

// hasGenesisPin reports whether a genesis hash was configured.
func (c SyncConfig) hasGenesisPin() bool {
	return c.GenesisHash != [32]byte{}
}

// FetchGenesisValidatorPubKey fetches block 0 from a peer and extracts the
// validator's ML-DSA-65 public key, so an RPC node without a local genesis.json
// can bootstrap from the chain.
//
// This REQUIRES a configured GenesisHash. The key is only accepted if the
// fetched block hashes to the pin and its own signature verifies under the key
// it carries. A block signature proves nothing about identity on its own — a
// hostile peer can mint a genesis block, sign it with a key it controls and
// serve a whole fabricated chain that verifies end to end. The pinned hash is
// what ties the key to the real network.
func (f *BlockFetcher) FetchGenesisValidatorPubKey(peerAddr string) ([]byte, error) {
	if !f.cfg.hasGenesisPin() {
		return nil, fmt.Errorf("refusing to bootstrap a validator pubkey without a pinned genesis hash " +
			"(set --genesis-hash, or supply the key directly with --validator-pubkey-hex)")
	}

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
	if block.Header.Height != 0 {
		return nil, fmt.Errorf("peer %s served height %d in place of genesis", peerAddr, block.Header.Height)
	}

	// Recompute rather than trusting the served Hash field.
	computed, err := block.ComputeHash()
	if err != nil {
		return nil, fmt.Errorf("hash genesis block from %s: %w", peerAddr, err)
	}
	if computed != f.cfg.GenesisHash {
		return nil, fmt.Errorf("peer %s served a genesis block with hash %x, expected pinned %x — wrong chain or hostile peer",
			peerAddr, computed[:8], f.cfg.GenesisHash[:8])
	}

	if len(block.ValidatorPubKey) == 0 {
		return nil, fmt.Errorf("genesis block has no validator pubkey — validator must be running with a funded genesis")
	}

	// Bind the served Hash to the header before trusting a signature over it.
	//
	// VerifySignature checks the signature against b.Hash — the field the PEER
	// sent — and ComputeHash covers only the header. ValidatorPubKey sits outside
	// the header, so without this a peer could serve the genuine pinned header
	// (passing the check above), replace ValidatorPubKey with its own, set Hash to
	// any value it liked, and sign that value: both checks would pass and this
	// node would adopt the attacker's key as the validator for the whole chain.
	//
	// Validate() is what closes it — it recomputes the hash and rejects a block
	// whose Hash does not match its header. Every other block path already calls
	// it before verifying; this one did not.
	if err := block.Validate(); err != nil {
		return nil, fmt.Errorf("pinned genesis block from %s is invalid: %w", peerAddr, err)
	}

	// And bind the key to the header, which the pin has already made trustworthy.
	// ValidatorAddr is inside the header and therefore covered by the pinned hash,
	// so a key that does not derive to it is not this chain's validator whatever
	// it manages to sign.
	pub, err := qcrypto.PublicKeyFromBytes(block.ValidatorPubKey)
	if err != nil {
		return nil, fmt.Errorf("genesis block from %s carries an invalid validator pubkey: %w", peerAddr, err)
	}
	if qcrypto.DeriveAddress(pub) != block.Header.ValidatorAddr {
		return nil, fmt.Errorf("genesis block from %s carries a validator pubkey that does not derive to the header's validator address — hostile peer",
			peerAddr)
	}

	if err := block.VerifySignature(block.ValidatorPubKey); err != nil {
		return nil, fmt.Errorf("pinned genesis block failed self-verification: %w", err)
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

	// Fail closed: never APPLY a block we cannot authenticate against a key we
	// already trust. The pubkey a block carries is NOT evidence of anything —
	// verifying a block against its own embedded key always succeeds, so the
	// former "learn it from the first block" path accepted any chain the first
	// peer chose to fabricate. The only sanctioned way to acquire the key is
	// FetchGenesisValidatorPubKey, which checks block 0 against a pinned hash.
	if len(f.cfg.ValidatorPubKey) == 0 {
		return nil, fmt.Errorf("block %d: no trusted validator pubkey — refusing to apply an unverified block "+
			"(set --validator-pubkey-hex, or --genesis-hash to bootstrap it)", height)
	}
	if err := block.VerifySignature(f.cfg.ValidatorPubKey); err != nil {
		return nil, fmt.Errorf("block %d: invalid validator signature: %w", height, err)
	}

	// Height 0 must additionally match the pin when one is configured, so a peer
	// cannot serve a different chain's genesis under a key we happen to trust.
	if height == 0 && f.cfg.hasGenesisPin() {
		computed, herr := block.ComputeHash()
		if herr != nil {
			return nil, fmt.Errorf("hash genesis block: %w", herr)
		}
		if computed != f.cfg.GenesisHash {
			return nil, fmt.Errorf("genesis hash %x does not match pinned %x", computed[:8], f.cfg.GenesisHash[:8])
		}
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
	localHeight func() uint64            // returns current local chain height
	applyBlock  func(*types.Block) error // applies a validated block to local chain
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

	// A node with no pinned key may bootstrap one ONLY when a genesis hash is
	// pinned; that hash is what authenticates the key. Without either, sync
	// stays fail-closed rather than adopting whatever the first peer offers.
	switch {
	case len(cfg.ValidatorPubKey) > 0:
		// Already pinned.
	case cfg.hasGenesisPin() && len(pool.Addrs()) > 0:
		go s.bootstrapValidatorPubKey()
	default:
		log.Error().Msg("sync: no --validator-pubkey-hex and no --genesis-hash — " +
			"this node cannot authenticate any block and will not sync")
	}

	return s
}

// bootstrapValidatorPubKey learns the validator's pubkey from a peer's genesis
// block, accepting it only if that block matches the pinned genesis hash.
func (s *Syncer) bootstrapValidatorPubKey() {
	peers := s.pool.Addrs()
	for _, peer := range peers {
		pubkey, err := s.fetcher.FetchGenesisValidatorPubKey(peer)
		if err != nil {
			log.Warn().Err(err).Str("peer", peer).Msg("sync: rejected genesis bootstrap from peer")
			continue
		}
		if len(pubkey) > 0 {
			s.fetcher.cfg.ValidatorPubKey = pubkey
			log.Info().
				Str("peer", peer).
				Str("pubkey_prefix", fmt.Sprintf("%x", pubkey[:8])).
				Msg("sync: bootstrapped validator pubkey from pinned genesis block")
			return
		}
	}
	log.Error().Msg("sync: no peer served a genesis block matching the pinned hash — refusing to sync")
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
