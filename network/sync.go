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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/block"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network/objects"
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

	// MaxParallelFetch bounds how many blocks SyncOnce downloads concurrently
	// within a batch. Fetching is safe to parallelise — FetchBlock only reads
	// from a peer and validates the block against keys already trusted, it
	// never touches local state — but APPLYING blocks is not: ExecuteBlock
	// must see height h-1 committed before h, since h's state root is computed
	// over the account set h-1 left behind. SyncOnce therefore fetches with up
	// to this many workers in flight but always calls applyBlock strictly in
	// ascending height order, so a fast peer cannot let a later block apply
	// before an earlier one.
	//
	// 0 or 1 preserves the original one-at-a-time behaviour.
	MaxParallelFetch int

	// MaxFetchLookahead caps how far ahead of the next apply height the
	// pipeline may fetch. 0 uses 16.
	MaxFetchLookahead int

	// MaxFetchBufferBytes caps the block bytes the pipeline holds at once:
	// bodies being read plus bodies buffered out of order while the applier
	// waits for an earlier height. 0 uses 16 MiB. Every byte is reserved
	// before it can be read and the read is capped at the reservation, so
	// this is an enforced ceiling rather than an estimate — with one
	// documented exception, the height the applier is waiting for (see
	// syncByteBudget).
	MaxFetchBufferBytes int64

	// FetchReserveBytes is the opening per-block reservation the pipeline
	// makes before a body is read. 0 uses defaultFetchReserveBytes. The
	// pipeline raises its own working estimate above this when a peer serves
	// bigger blocks; it never lowers it below.
	FetchReserveBytes int64

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

	// LookupProducerKey resolves the key of an ACTIVE registered validator, so
	// a block sealed by a rotating BFT leader other than the genesis validator
	// can be authenticated (see VerifyBlockProducer). nil keeps the single
	// pinned-key rule. Block 0 is never resolved through it.
	LookupProducerKey ProducerKeyLookup

	// SnapshotStagingDir is the on-disk staging area for a chunked state
	// snapshot download. Empty uses a temporary directory (no resume).
	SnapshotStagingDir string

	// MaxSnapshotDiskBytes caps the whole staged snapshot (raw key+value
	// bytes), separate from the per-page wire cap. Zero uses
	// state.DefaultMaxSnapshotDiskBytes.
	MaxSnapshotDiskBytes int64

	// AdoptSnapshotDownload imports a completed staging directory without
	// assembling every leaf in RAM. When nil, StateSync assembles and calls
	// the supplied importer.
	AdoptSnapshotDownload func(dir string) error

	// CodedObjects enables the coded-object snapshot path in StateSync: the
	// peer's snapshot is fetched as an authenticated coded object (any size,
	// multi-generation) before the plain single-shot/chunked paths are tried.
	// Every failure falls back, so a peer without the coded live path still
	// serves a join. Default true in DefaultSyncConfig; zero-value structs
	// (tests) leave it off.
	CodedObjects bool

	// CodedBlockBatches enables the coded block-batch pre-fill in SyncOnce:
	// when catching up by at least MinCodedBatchBlocks, one coded batch of up
	// to CodedBatchSize blocks is fetched and applied before the per-block
	// pipeline takes over. Any failure falls back to per-block fetching.
	// Default true in DefaultSyncConfig; zero-value structs (tests) leave it
	// off.
	CodedBlockBatches bool

	// CodedBatchSize is how many blocks one coded batch carries. 0 uses 32.
	CodedBatchSize int

	// MinCodedBatchBlocks is the catch-up gap below which SyncOnce does not
	// bother with a coded batch (small gaps finish faster one-by-one). 0 uses
	// 16.
	MinCodedBatchBlocks int
}

// DefaultSyncConfig returns sensible defaults.
func DefaultSyncConfig(validatorPubKey []byte) SyncConfig {
	return SyncConfig{
		ValidatorPubKey:   validatorPubKey,
		MaxBlocksPerBatch: 100,
		MaxParallelFetch:  8,
		RequestTimeout:    15 * time.Second,
		RetryDelay:        2 * time.Second,
		CodedObjects:      true,
		CodedBlockBatches: true,
	}
}

// BlockFetcher downloads blocks from remote peers.
type BlockFetcher struct {
	cfg    SyncConfig
	client *http.Client

	// trustedPK is the genesis validator key every block is authenticated
	// against (with the active set as the second key ring, see
	// VerifyBlockProducer). It starts as cfg.ValidatorPubKey and may be
	// learned later from a pinned genesis block, on another goroutine, so it
	// lives behind a lock rather than in cfg.
	mu        sync.RWMutex
	trustedPK []byte
}

// NewBlockFetcher creates a fetcher with the given config.
func NewBlockFetcher(cfg SyncConfig) *BlockFetcher {
	return &BlockFetcher{
		cfg:       cfg,
		trustedPK: append([]byte(nil), cfg.ValidatorPubKey...),
		client: &http.Client{
			Timeout:       cfg.RequestTimeout,
			CheckRedirect: noRedirect,
			Transport:     SafeTransport(cfg.RequestTimeout),
		},
	}
}

// TrustedPubKey returns the genesis validator key blocks are verified with,
// or nil while none is known.
func (f *BlockFetcher) TrustedPubKey() []byte {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if len(f.trustedPK) == 0 {
		return nil
	}
	return append([]byte(nil), f.trustedPK...)
}

func (f *BlockFetcher) setTrustedPubKey(pk []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.trustedPK = append([]byte(nil), pk...)
}

// fetchBlockJSON retrieves a block document from a peer.
//
// The sealed ML-KEM channel is tried first. Falling back to plaintext is opt-in
// because an attacker who can silence /p2p/identity would otherwise be able to
// downgrade every sync to cleartext just by refusing to answer.
//
// limit is the caller's byte allowance for this one body: the pipeline
// reserves it from the fetch budget BEFORE the read, so the read must not be
// allowed to exceed it. A body that does is reported as errFetchBodyTooLarge
// rather than truncated, so the caller can reserve more and ask again.
func (f *BlockFetcher) fetchBlockJSON(ctx context.Context, peerAddr, kind string, body []byte, plainPath string, limit int64) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if limit < 1 || limit > maxSyncBodyBytes {
		limit = maxSyncBodyBytes
	}
	if f.cfg.Identities != nil {
		// The allowance is in DECODED body bytes — that is what the caller
		// holds and what its budget was charged for. A sealed reply carries
		// the same bytes base64'd inside an envelope, so the wire form is
		// allowed to be bigger; the decoded body is then checked against the
		// allowance itself. Opening one reply therefore touches up to about
		// twice the allowance transiently, per in-flight request.
		raw, err := f.cfg.Identities.QuerySealedContext(ctx, peerAddr, kind, body, sealedWireAllowance(limit))
		if err == nil {
			if int64(len(raw)) > limit {
				return nil, fmt.Errorf("%w: sealed body from %s is over %d bytes", errFetchBodyTooLarge, peerAddr, limit)
			}
			return raw, nil
		}
		if errors.Is(err, ErrSealedReplyTooLarge) {
			return nil, fmt.Errorf("%w: %v", errFetchBodyTooLarge, err)
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

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, peerAddr+plainPath, nil)
	if err != nil {
		return nil, err
	}
	resp, err := f.client.Do(req)
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
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("%w: body from %s%s is over %d bytes", errFetchBodyTooLarge, peerAddr, plainPath, limit)
	}
	return raw, nil
}

// sealedWireAllowance is the envelope-sized cap for a sealed reply carrying
// at most limit decoded bytes: base64 is 4/3, plus the envelope's own JSON
// and KEM ciphertext. Generous on purpose — a body that fits the allowance
// must never be refused for its packaging, or every fetch would pay a retry.
func sealedWireAllowance(limit int64) int64 {
	if limit >= maxSyncBodyBytes/2 {
		return maxSyncBodyBytes
	}
	return limit*2 + 4096
}

// errFetchBodyTooLarge means a peer's block body did not fit the byte
// allowance reserved for it. It is not a peer fault on its own: a legitimate
// block can simply be bigger than the pipeline's working reservation.
var errFetchBodyTooLarge = errors.New("sync: block body exceeds the reserved byte allowance")

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
	raw, err := f.fetchBlockJSON(context.Background(), peerAddr, KindGetBlock, req, "/block/0", maxSyncBodyBytes)
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
	return f.FetchBlockContext(context.Background(), peerAddr, height)
}

// FetchBlockContext is FetchBlock with a cancellable in-flight request.
func (f *BlockFetcher) FetchBlockContext(ctx context.Context, peerAddr string, height uint64) (*types.Block, error) {
	b, _, err := f.fetchBlockLimited(ctx, peerAddr, height, maxSyncBodyBytes)
	return b, err
}

// fetchBlockLimited is FetchBlockContext with an explicit byte allowance for
// the body, returning the wire size that was actually read so the caller can
// charge its memory budget for what it is holding rather than for a guess.
func (f *BlockFetcher) fetchBlockLimited(ctx context.Context, peerAddr string, height uint64, limit int64) (*types.Block, int64, error) {
	req, err := json.Marshal(GetBlockRequest{Height: height})
	if err != nil {
		return nil, 0, err
	}
	raw, err := f.fetchBlockJSON(ctx, peerAddr, KindGetBlock, req, fmt.Sprintf("/block/%d", height), limit)
	if err != nil {
		return nil, 0, fmt.Errorf("fetch block %d from %s: %w", height, peerAddr, err)
	}
	// The sealed reply bound (maxSyncBodyBytes = 128 MiB) sits far above the
	// 32 MiB wire cap a producer may legitimately sign, so enforce the wire
	// cap here: an oversized block is refused at the transport layer instead
	// of being parsed, validated and applied.
	if len(raw) > block.MaxBlockWireBytes {
		return nil, 0, fmt.Errorf("block %d from %s is %d bytes, over the %d-byte wire cap",
			height, peerAddr, len(raw), block.MaxBlockWireBytes)
	}

	var blk types.Block
	if err := json.Unmarshal(raw, &blk); err != nil {
		return nil, 0, fmt.Errorf("decode block %d: %w", height, err)
	}
	// The peer must serve the block we asked for. A real, validly-signed block
	// at the WRONG height passes the structural and producer checks below, and
	// the pipeline applies by requested height — so without this, a peer could
	// hand back height 7 for a request of height 5 and the applier would run
	// it into slot 5. Reject the mismatch here, before any of that.
	if blk.Header.Height != height {
		return nil, 0, fmt.Errorf("peer %s served height %d in place of requested height %d",
			peerAddr, blk.Header.Height, height)
	}

	// Validate structural integrity
	if err := blk.Validate(); err != nil {
		return nil, 0, fmt.Errorf("block %d failed validation: %w", height, err)
	}

	// Fail closed: never APPLY a block we cannot authenticate against a key we
	// already trust. The pubkey a block carries is NOT evidence of anything —
	// verifying a block against its own embedded key always succeeds, so the
	// former "learn it from the first block" path accepted any chain the first
	// peer chose to fabricate. The only sanctioned way to acquire the key is
	// FetchGenesisValidatorPubKey, which checks block 0 against a pinned hash.
	// Above height 0 the active validator set is the second key ring, so a
	// rotating BFT leader can be followed without trusting what it carries.
	if err := VerifyBlockProducer(&blk, f.TrustedPubKey(), f.cfg.LookupProducerKey); err != nil {
		// Only defer a missing registry entry when a resolver is wired —
		// the parent state may not contain that registration yet. A nil
		// resolver or a key that does not bind to ValidatorAddr stays fatal.
		if !errors.Is(err, ErrProducerNotYetKnown) || f.cfg.LookupProducerKey == nil {
			return nil, 0, err
		}
	}

	// Height 0 must additionally match the pin when one is configured, so a peer
	// cannot serve a different chain's genesis under a key we happen to trust.
	if height == 0 && f.cfg.hasGenesisPin() {
		computed, herr := blk.ComputeHash()
		if herr != nil {
			return nil, 0, fmt.Errorf("hash genesis block: %w", herr)
		}
		if computed != f.cfg.GenesisHash {
			return nil, 0, fmt.Errorf("genesis hash %x does not match pinned %x", computed[:8], f.cfg.GenesisHash[:8])
		}
	}

	return &blk, int64(len(raw)), nil
}

// FetchLatestBlock fetches the latest block from a peer.
func (f *BlockFetcher) FetchLatestBlock(peerAddr string) (*types.Block, error) {
	raw, err := f.fetchBlockJSON(context.Background(), peerAddr, KindGetLatest, []byte(`{}`), "/block/latest", maxSyncBodyBytes)
	if err != nil {
		return nil, fmt.Errorf("fetch latest block from %s: %w", peerAddr, err)
	}

	var block types.Block
	if err := json.Unmarshal(raw, &block); err != nil {
		return nil, fmt.Errorf("decode latest block: %w", err)
	}

	// Same rule as FetchBlock: pinned key, then the active set; fail closed.
	if err := VerifyBlockProducer(&block, f.TrustedPubKey(), f.cfg.LookupProducerKey); err != nil {
		return nil, fmt.Errorf("latest block: %w", err)
	}

	return &block, nil
}

// FetchBlockBatch downloads a coded batch of count consecutive blocks starting
// at fromHeight over the sealed channel: the peer returns the batch's object
// manifest in reply to get_block_batch_v1, the blocks travel as coded symbols,
// and every decoded block is validated exactly like FetchBlock (structural,
// then the pinned key / active producer set) before any of them is returned.
// Any failure returns an error so the caller falls back to per-block fetches —
// the batch is a transport accelerator, never a weaker trust path.
func (f *BlockFetcher) FetchBlockBatch(peerAddr string, fromHeight uint64, count int) ([]*types.Block, error) {
	// Reject degenerate ranges before any network work: an out-of-cap count
	// or a range end that would wrap would otherwise let a forged manifest
	// anchor match a wrapped height or a serve-side loop truncate the batch.
	if err := ValidateBlockBatchRange(fromHeight, count); err != nil {
		return nil, fmt.Errorf("coded block batch: %w", err)
	}
	if f.cfg.Identities == nil {
		return nil, fmt.Errorf("coded block batch from %s: no sealed transport configured", peerAddr)
	}
	doc, err := f.cfg.Identities.Get(peerAddr)
	if err != nil {
		return nil, fmt.Errorf("coded block batch from %s: peer identity: %w", peerAddr, err)
	}
	if !doc.ServesCodedBlocks() {
		return nil, fmt.Errorf("coded block batch from %s: peer declares no coded block-batch service", peerAddr)
	}
	if doc.CodedBlocks == nil && f.cfg.Identities.CodedRecentlyRefused(peerAddr, CodedBatches) {
		return nil, fmt.Errorf("coded block batch from %s: peer recently refused coded-object service", peerAddr)
	}
	req, err := json.Marshal(GetBlockBatchRequest{FromHeight: fromHeight, Count: count})
	if err != nil {
		return nil, err
	}
	raw, err := f.cfg.Identities.QuerySealedTimeout(peerAddr, KindGetBlockBatch, req,
		sealedReplyBound(maxObjectManifestReplyBytes), objectFetchTimeout)
	if err != nil {
		if doc.CodedBlocks == nil {
			f.cfg.Identities.NoteCodedRefusal(peerAddr, CodedBatches)
		}
		return nil, fmt.Errorf("fetch coded block batch from %s: %w", peerAddr, err)
	}
	f.cfg.Identities.ForgetCodedRefusal(peerAddr, CodedBatches)
	manifest, err := objects.DecodeObjectManifest(raw)
	if err != nil {
		return nil, fmt.Errorf("decode block batch manifest from %s: %w", peerAddr, err)
	}
	// The manifest's anchor is the peer's signed statement about WHICH range
	// the object carries. Refuse a mismatch before spending symbol work; the
	// decoded blocks are re-checked per height below anyway.
	if manifest.Anchor.Kind != "block" {
		return nil, fmt.Errorf("block batch manifest from %s anchors kind %q", peerAddr, manifest.Anchor.Kind)
	}
	if manifest.Anchor.Height != fromHeight+uint64(count)-1 {
		return nil, fmt.Errorf("block batch manifest from %s anchors height %d, want %d",
			peerAddr, manifest.Anchor.Height, fromHeight+uint64(count)-1)
	}
	decoded, err := f.fetchDecodedObject(peerAddr, manifest, objects.DefaultLimits())
	if err != nil {
		return nil, fmt.Errorf("coded block batch from %s: %w", peerAddr, err)
	}
	return parseBlockBatch(peerAddr, fromHeight, count, decoded, block.MaxBlockWireBytes, func(b *types.Block) error {
		if err := VerifyBlockProducer(b, f.TrustedPubKey(), f.cfg.LookupProducerKey); err != nil {
			// Only defer a missing registry entry when a resolver is wired.
			if !errors.Is(err, ErrProducerNotYetKnown) || f.cfg.LookupProducerKey == nil {
				return err
			}
		}
		return nil
	})
}

// parseBlockBatch decodes a coded batch payload — the JSON array of count
// consecutive blocks — refusing anything that is not exactly count blocks at
// exactly the requested heights, each within maxBlockBytes of wire size,
// structurally valid and authenticated by verify. This is the batch analogue
// of the per-block checks FetchBlockContext performs; every refusal makes the
// caller fall back to per-block fetching, never to a weaker trust path.
func parseBlockBatch(peerAddr string, fromHeight uint64, count int, decoded []byte, maxBlockBytes int, verify func(*types.Block) error) ([]*types.Block, error) {
	var raws []json.RawMessage
	if err := json.Unmarshal(decoded, &raws); err != nil {
		return nil, fmt.Errorf("decode coded block batch from %s: %w", peerAddr, err)
	}
	if len(raws) != count {
		return nil, fmt.Errorf("coded block batch from %s: got %d blocks, asked for %d", peerAddr, len(raws), count)
	}
	blocks := make([]*types.Block, 0, count)
	for i, rb := range raws {
		want := fromHeight + uint64(i)
		// The batch object itself is bounded by objects.DefaultMaxObjectSize,
		// but any single block inside it must also respect the per-block wire
		// cap a producer may legitimately sign.
		if len(rb) > maxBlockBytes {
			return nil, fmt.Errorf("coded block batch from %s: block %d is %d bytes, over the %d-byte wire cap",
				peerAddr, want, len(rb), maxBlockBytes)
		}
		var b *types.Block
		if err := json.Unmarshal(rb, &b); err != nil {
			return nil, fmt.Errorf("decode block %d from coded batch: %w", want, err)
		}
		if b == nil {
			return nil, fmt.Errorf("coded block batch from %s: block %d is null", peerAddr, want)
		}
		// Same rule as FetchBlock: a validly-signed block at the wrong height
		// must never slip into the pipeline's slot for `want`.
		if b.Header.Height != want {
			return nil, fmt.Errorf("peer %s served height %d in place of requested height %d",
				peerAddr, b.Header.Height, want)
		}
		if err := b.Validate(); err != nil {
			return nil, fmt.Errorf("block %d from coded batch failed validation: %w", want, err)
		}
		if err := verify(b); err != nil {
			return nil, fmt.Errorf("coded block batch block %d: %w", want, err)
		}
		blocks = append(blocks, b)
	}
	return blocks, nil
}

// Syncer orchestrates the full block sync process.
type Syncer struct {
	fetcher     *BlockFetcher
	pool        *PeerPool
	localHeight func() uint64            // returns current local chain height
	applyBlock  func(*types.Block) error // applies a validated block to local chain

	// resident meters the decoded block bytes the fetch pipeline holds.
	resident fetchResidency
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
			s.fetcher.setTrustedPubKey(pubkey)
			log.Info().
				Str("peer", peer).
				Str("pubkey_prefix", fmt.Sprintf("%x", pubkey[:8])).
				Msg("sync: bootstrapped validator pubkey from pinned genesis block")
			return
		}
	}
	log.Error().Msg("sync: no peer served a genesis block matching the pinned hash — refusing to sync")
}

// TrustedPubKey returns the genesis validator key this syncer authenticates
// blocks with — the configured one, or the one learned from a pinned genesis
// block — so the push path can apply the same rule. nil while none is known.
func (s *Syncer) TrustedPubKey() []byte {
	return s.fetcher.TrustedPubKey()
}

// fetchResidency meters the block bytes the fetch pipeline is actually
// holding: bodies decoded by a worker plus those buffered out of order while
// the applier waits for an earlier height. The byte budget exists to bound
// exactly this quantity, so the meter is what the bound is asserted against.
type fetchResidency struct {
	mu   sync.Mutex
	cur  int64
	peak int64
}

func (r *fetchResidency) add(n int64) {
	if n <= 0 {
		return
	}
	r.mu.Lock()
	r.cur += n
	if r.cur > r.peak {
		r.peak = r.cur
	}
	r.mu.Unlock()
}

func (r *fetchResidency) sub(n int64) {
	if n <= 0 {
		return
	}
	r.mu.Lock()
	r.cur -= n
	if r.cur < 0 {
		r.cur = 0
	}
	r.mu.Unlock()
}

func (r *fetchResidency) reset() {
	r.mu.Lock()
	r.cur, r.peak = 0, 0
	r.mu.Unlock()
}

// PeakFetchResidentBytes is the high-water mark of decoded block bytes the
// last sync pass held at once.
func (s *Syncer) PeakFetchResidentBytes() int64 {
	s.resident.mu.Lock()
	defer s.resident.mu.Unlock()
	return s.resident.peak
}

// fetchResult carries one height's outcome back to the ordered applier.
type fetchResult struct {
	height uint64
	block  *types.Block
	peer   string
	err    error
	// held is the byte allowance this result still owns in the budget. The
	// applier releases it once the block is applied or given up on.
	held int64
}

// defaultFetchReserveBytes is the opening per-block reservation. It is only
// a starting point: the pipeline raises its working reservation to the
// largest body it has actually seen, so the reservation converges on real
// block sizes instead of staying a guess.
const defaultFetchReserveBytes int64 = 2 << 20
const typicalFetchBlockBytes int64 = 4 << 20

// syncByteBudget bounds the block bytes the fetch pipeline holds at once:
// bodies being read plus bodies already decoded and buffered out of order
// while the applier waits for an earlier height. Every byte is reserved
// BEFORE it can be read and charged at its real size once it has been, so a
// block cannot be held against a smaller guess.
//
// DOCUMENTED BOUNDED EXCEPTION. The one height the applier is waiting for is
// admitted even when the budget is full. Nothing releases budget until that
// height is applied, so making it wait is a deadlock rather than
// backpressure. Exactly one height is in that position at a time and its
// allowance is capped at maxSyncBodyBytes, so the overrun is at most one
// block — and only a block that is itself larger than the whole budget can
// cause any overrun at all.
//
// Prior art: geth's downloader keeps a running estimate of real result sizes
// (resultSize) and throttles new fetches against a byte ceiling derived from
// it, while never throttling the item at the head of the queue — the same two
// ideas. Splendor differs by making the estimate a hard read cap rather than
// only a scheduling hint: a body over its allowance is refused and re-asked
// for under a larger one, so a peer cannot make the node hold more than it
// reserved by simply serving a bigger block.
type syncByteBudget struct {
	mu   sync.Mutex
	cond *sync.Cond
	used int64
	max  int64
	// head is the height the applier is waiting for: the exception above.
	head uint64
	// grant is the working per-fetch reservation — the largest body seen so
	// far, never below the configured opening reserve, never above max.
	grant int64
}

func newSyncByteBudget(max, reserve int64) *syncByteBudget {
	if max < 1 {
		max = 16 << 20
	}
	if reserve < 1 {
		reserve = defaultFetchReserveBytes
	}
	if reserve > max {
		reserve = max
	}
	b := &syncByteBudget{max: max, grant: reserve}
	b.cond = sync.NewCond(&b.mu)
	return b
}

// setHead names the height the applier is blocked on. It is the only height
// exempt from the budget, and moving it wakes whoever was waiting for it.
func (b *syncByteBudget) setHead(h uint64) {
	b.mu.Lock()
	b.head = h
	b.cond.Broadcast()
	b.mu.Unlock()
}

// fetchHeadroomShift gives the reservation room above the largest body actually
// seen, as a right shift: 3 is one eighth.
//
// Blocks a few bytes apart are the normal case, and a reservation set to the
// exact size of the largest body so far is re-escalated by the very next block
// that is one byte bigger — two requests for that height instead of one. The
// headroom is applied when the allowance is handed out and never stored, so
// grant stays what it says it is: a measurement. Nothing is ever learned from
// the headroom itself.
const fetchHeadroomShift = 3

// reservation is the allowance a new fetch should ask for: the largest body
// really seen, plus headroom, never above the budget.
func (b *syncByteBudget) reservation() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := b.grant + b.grant>>fetchHeadroomShift
	if n > b.max {
		n = b.max
	}
	if n < b.grant {
		return b.grant
	}
	return n
}

// wouldRaise reports whether observing n would move the working reservation.
//
// The caller uses it to decide whether a reply is worth measuring properly:
// establishing a body's real size costs a re-encode, and it is only worth
// paying when the answer would change the allowance every later fetch runs
// under.
func (b *syncByteBudget) wouldRaise(n int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return n > b.grant && b.grant < b.max
}

// observe raises the working reservation towards a body size that was really
// seen. It never lowers it inside one pass, and never exceeds the budget:
// one outsized block must not serialise every fetch after it.
//
// "Really seen" is the whole point, and it is why nothing here is ever fed a
// projection. The escalation path used to widen its own guess by a factor of
// four and hand THAT to this method, so two replies too large to fit — which
// any peer can produce at will, and which say nothing about how big a block
// actually is — raised the reservation to the entire budget. After that admit
// allowed one outstanding fetch for the rest of the pass, and parallel sync
// became serial for as long as such a peer stayed in the pool.
func (b *syncByteBudget) observe(n int64) {
	if n < 1 {
		return
	}
	b.mu.Lock()
	if n > b.grant {
		b.grant = n
		if b.grant > b.max {
			b.grant = b.max
		}
	}
	b.mu.Unlock()
}

// admit reserves n bytes for height h, waiting until the budget has room —
// unless h is the height the applier is waiting for, which is admitted at
// once (see the exception above).
func (b *syncByteBudget) admit(ctx context.Context, h uint64, n int64) error {
	if n < 1 {
		n = 1
	}
	stop := context.AfterFunc(ctx, func() {
		b.mu.Lock()
		b.cond.Broadcast()
		b.mu.Unlock()
	})
	defer stop()

	b.mu.Lock()
	defer b.mu.Unlock()
	for b.head != h && b.used+n > b.max {
		if err := ctx.Err(); err != nil {
			return err
		}
		b.cond.Wait()
	}
	b.used += n
	return nil
}

// settle replaces a reservation with the size that actually arrived. actual
// is never above reserved — the read was capped at the reservation — so this
// only ever gives budget back.
func (b *syncByteBudget) settle(reserved, actual int64) {
	if actual < 1 {
		actual = 1
	}
	if actual > reserved {
		actual = reserved
	}
	b.mu.Lock()
	b.used -= reserved - actual
	if b.used < 0 {
		b.used = 0
	}
	b.cond.Broadcast()
	b.mu.Unlock()
}

func (b *syncByteBudget) release(n int64) {
	if n < 1 {
		return
	}
	b.mu.Lock()
	b.used -= n
	if b.used < 0 {
		b.used = 0
	}
	b.cond.Broadcast()
	b.mu.Unlock()
}

// learnedSize is the body size worth remembering for later fetches.
//
// The wire size is what the budget is CHARGED, because that is what was read,
// and charging it is conservative. It is not always what should be LEARNED: a
// reply may be padded — JSON tolerates insignificant whitespace, and a block's
// hash and signature cover the decoded struct rather than the byte count — so a
// peer can inflate the wire size of a perfectly valid small block. The read cap
// keeps that harmless most of the time, since a body is refused above the
// allowance it was given, but an allowance widened by escalation opens a window
// where a padded body could teach the reservation a size no block needs.
//
// So when a reply claims to justify a larger reservation, its real encoded size
// is established before that claim is believed, and the smaller of the two is
// what is remembered. The re-encode is paid only on that path, which is rare:
// the reservation rises a handful of times per pass at most, and never once the
// budget's ceiling is reached.
func learnedSize(b *types.Block, wire int64, budget *syncByteBudget) int64 {
	if b == nil || !budget.wouldRaise(wire) {
		return wire
	}
	enc, err := json.Marshal(b)
	if err != nil {
		return wire
	}
	if canon := int64(len(enc)); canon < wire {
		return canon
	}
	return wire
}

// peerPenalties counts, for one sync pass, the replies a peer sent that no
// valid block could account for.
//
// Bounded by the peer set and discarded with the pass, so there is no growing
// state and no lasting ban: a peer that misbehaved during one pass is asked
// again in the next. peerFor prefers unpenalised candidates and falls back to
// the whole set when every candidate is penalised, because refusing to talk to
// anyone is worse than talking to a peer that wastes some work.
type peerPenalties struct {
	mu       sync.Mutex
	overruns map[string]int
}

// overrunTolerance is how many unaccountable replies a peer gets before other
// peers are preferred. Above one, because a single such reply can be a
// truncation or a genuine oddity, and the cost of one is a wasted fetch.
const overrunTolerance = 2

func newPeerPenalties() *peerPenalties {
	return &peerPenalties{overruns: make(map[string]int)}
}

func (p *peerPenalties) overran(addr string) {
	if addr == "" {
		return
	}
	p.mu.Lock()
	if len(p.overruns) < 1024 {
		p.overruns[addr]++
	}
	p.mu.Unlock()
}

// suspect reports whether addr has exhausted its tolerance this pass.
func (p *peerPenalties) suspect(addr string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.overruns[addr] >= overrunTolerance
}

// prefer returns the candidates worth asking: those still inside their
// tolerance, or all of them when that would leave nobody.
func (p *peerPenalties) prefer(cands []string) []string {
	keep := make([]string, 0, len(cands))
	for _, c := range cands {
		if !p.suspect(c) {
			keep = append(keep, c)
		}
	}
	if len(keep) == 0 {
		return cands
	}
	return keep
}

// fetchWithBudget reserves before it reads and never reads more than it
// reserved, so what the pipeline holds is what the budget was charged for.
//
// A body that does not fit its allowance is not a failure: the allowance is
// widened (x4, capped at the wire limit) and the block is asked for again.
// While the widened allowance is above the whole budget only the head of the
// line can be admitted, so an oversized block waits its turn instead of
// squeezing in ahead of the applier. Ordering therefore cannot deadlock: the
// height the applier wants is always admissible, applying it releases its
// bytes, and the line moves.
func (s *Syncer) fetchWithBudget(ctx context.Context, budget *syncByteBudget, h uint64,
	peerFor func(uint64) string, onOverrun func(string)) (*types.Block, string, int64, error) {
	want := budget.reservation()
	peerAddr := ""
	for {
		if err := budget.admit(ctx, h, want); err != nil {
			return nil, peerAddr, 0, err
		}
		peerAddr = peerFor(h)
		block, wire, err := s.fetcher.fetchBlockLimited(ctx, peerAddr, h, want)
		if err != nil && !errors.Is(err, errFetchBodyTooLarge) {
			log.Warn().Err(err).Uint64("height", h).Str("peer", peerAddr).
				Msg("sync: block fetch failed, will retry")
			select {
			case <-ctx.Done():
				err = ctx.Err()
			case <-time.After(s.fetcher.cfg.RetryDelay):
				retryPeer := peerFor(h)
				block, wire, err = s.fetcher.fetchBlockLimited(ctx, retryPeer, h, want)
				peerAddr = retryPeer
			}
		}
		if errors.Is(err, errFetchBodyTooLarge) {
			budget.release(want)
			if want >= maxSyncBodyBytes {
				// A reply that does not fit the wire limit is not a big block;
				// no valid block is that size. Whoever sent it is either broken
				// or doing it deliberately, and this is the only overrun that
				// can be attributed with certainty — an overrun at a smaller
				// allowance is what an honest peer serving a genuinely large
				// block looks like.
				onOverrun(peerAddr)
				return nil, peerAddr, 0, fmt.Errorf("sync: block %d does not fit the %d-byte wire limit",
					h, int64(maxSyncBodyBytes))
			}
			if want *= 4; want > maxSyncBodyBytes {
				want = maxSyncBodyBytes
			}
			// Deliberately nothing learned here. want is this fetch's retry
			// allowance, not a measurement: see observe.
			continue
		}
		if err != nil {
			budget.release(want)
			return nil, peerAddr, 0, err
		}
		budget.settle(want, wire)
		budget.observe(learnedSize(block, wire, budget))
		return block, peerAddr, wire, nil
	}
}

// SyncOnce performs one sync pass: check peers, download missing blocks.
// Returns the number of blocks applied, and any error.
//
// RESUME: SyncOnce always starts from s.localHeight()+1, which only advances
// once applyBlock has actually committed a block. A call that returns early —
// whether from a fetch error, an apply error, or the process dying mid-batch —
// leaves localHeight exactly where the last successful apply left it, so the
// very next call (the next tick, or the next process on restart) resumes at
// the same height with no bookkeeping of its own. There is no separate résumé
// marker to go stale or need reconciling: the chain's own head IS the résumé
// point.
func (s *Syncer) SyncOnce() (int, error) {
	return s.SyncOnceContext(context.Background())
}

// SyncOnceContext is SyncOnce with a cancellable context. Cancellation
// stops issuing new heights, interrupts retry sleeps, and cancels
// in-flight block fetches.
func (s *Syncer) SyncOnceContext(ctx context.Context) (int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
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

	if len(s.pool.PeersAboveHeight(localH)) == 0 {
		return 0, fmt.Errorf("sync: no peers above local height %d", localH)
	}

	target := bestH
	if s.fetcher.cfg.MaxBlocksPerBatch > 0 && target-localH > uint64(s.fetcher.cfg.MaxBlocksPerBatch) {
		target = localH + uint64(s.fetcher.cfg.MaxBlocksPerBatch)
	}
	workers := s.fetcher.cfg.MaxParallelFetch
	if workers < 1 {
		workers = 1
	}
	if span := target - localH; uint64(workers) > span {
		workers = int(span)
	}

	// Coded batch pre-fill: when the catch-up gap is large enough, pull one
	// coded batch from a peer above the local height and apply it in order
	// before the per-block pipeline takes the rest. Any failure — peer
	// without the service, transfer error, an unauthenticated block — falls
	// back to per-block fetching, which re-validates every block it touches.
	// The batch is a transport accelerator, never a weaker trust path.
	applied := 0
	batchSize := s.fetcher.cfg.CodedBatchSize
	if batchSize < 1 {
		batchSize = 32
	}
	minBatch := s.fetcher.cfg.MinCodedBatchBlocks
	if minBatch < 1 {
		minBatch = 16
	}
	if s.fetcher.cfg.CodedBlockBatches && target-localH >= uint64(minBatch) {
		if cands := s.pool.PeersAboveHeight(localH); len(cands) > 0 {
			peer := cands[0]
			n := int(target - localH)
			if n > batchSize {
				n = batchSize
			}
			blocks, err := s.fetcher.FetchBlockBatch(peer, localH+1, n)
			if err != nil {
				log.Debug().Err(err).Str("peer", peer).
					Msg("sync: coded block batch unavailable, falling back to per-block fetching")
			} else {
				batchApplied := 0
				for _, b := range blocks {
					if err := ctx.Err(); err != nil {
						return applied, err
					}
					// Fail closed: a block that does not authenticate is never
					// applied. Stop here and let the pipeline surface the same
					// refusal at this height.
					if err := VerifyBlockProducer(b, s.fetcher.TrustedPubKey(), s.fetcher.cfg.LookupProducerKey); err != nil {
						log.Warn().Err(err).Uint64("height", b.Header.Height).
							Msg("sync: coded batch block failed producer authentication, falling back to per-block fetching")
						break
					}
					if err := s.applyBlock(b); err != nil {
						log.Warn().Err(err).Uint64("height", b.Header.Height).
							Msg("sync: coded batch block failed to apply, falling back to per-block fetching")
						break
					}
					s.pool.UpdatePeerHeight(peer, b.Header.Height)
					applied++
					batchApplied++
					localH = b.Header.Height
				}
				log.Debug().Int("blocks", batchApplied).Uint64("height", localH).
					Msg("sync: applied coded block batch")
			}
		}
	}

	// Fold the pre-fill into the per-round block budget: target was clamped to
	// localH + MaxBlocksPerBatch BEFORE the pre-fill, and the pre-fill only
	// advances localH toward target, so pre-fill + pipeline together can never
	// exceed MaxBlocksPerBatch in one SyncOnce. The re-clamp below states that
	// invariant explicitly so a future edit to the pre-fill cannot quietly
	// blow the budget. The byte budget is folded the same way: the batch
	// object is bounded by objects.DefaultMaxObjectSize and every block inside
	// it by MaxBlockWireBytes (parseBlockBatch), so the pre-fill holds at most
	// one bounded object at a time.
	if s.fetcher.cfg.MaxBlocksPerBatch > 0 && target-localH > uint64(s.fetcher.cfg.MaxBlocksPerBatch) {
		target = localH + uint64(s.fetcher.cfg.MaxBlocksPerBatch)
	}

	pApplied, err := s.pipelineFetchAndApply(ctx, localH, target, workers)
	applied += pApplied
	if err != nil {
		return applied, err
	}

	log.Info().
		Int("applied", applied).
		Uint64("final_height", target).
		Msg("sync: catchup complete")

	return applied, nil
}

// pipelineFetchAndApply downloads (localH, target] with up to `workers`
// concurrent fetchers, then applies every block strictly in ascending height
// order. Fetching overlaps network latency across many in-flight requests;
// applying never does, because ExecuteBlock at height h depends on the state
// h-1 committed.
func (s *Syncer) pipelineFetchAndApply(ctx context.Context, localH, target uint64, workers int) (int, error) {
	heights := make(chan uint64)
	results := make(chan fetchResult, workers)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.resident.reset()

	lookahead := s.fetcher.cfg.MaxFetchLookahead
	if lookahead < 1 {
		lookahead = 16
	}
	maxBytes := s.fetcher.cfg.MaxFetchBufferBytes
	if maxBytes < 1 {
		maxBytes = 16 << 20
	}
	if est := int(maxBytes / typicalFetchBlockBytes); est > 0 && lookahead > est {
		lookahead = est
	}
	if lookahead < 1 {
		lookahead = 1
	}
	preReserve := s.fetcher.cfg.FetchReserveBytes
	if preReserve < 1 {
		preReserve = defaultFetchReserveBytes
	}
	if preReserve > maxBytes {
		preReserve = maxBytes
	}
	if preReserve < 1 {
		preReserve = 1
	}
	budget := newSyncByteBudget(maxBytes, preReserve)
	budget.setHead(localH + 1)
	window := make(chan struct{}, lookahead)
	for i := 0; i < lookahead; i++ {
		window <- struct{}{}
	}

	// Heights are dispatched strictly in ascending order, so the height the
	// applier is waiting for has always been handed to a worker before any
	// later one. That is what makes the head-of-line exception enough to keep
	// the pipeline moving: whoever holds it is never blocked on the budget.
	// Reservation happens in the worker, where the body is actually read.
	go func() {
		defer close(heights)
		for h := localH + 1; h <= target; h++ {
			select {
			case <-ctx.Done():
				return
			case <-window:
			}
			select {
			case <-ctx.Done():
				return
			case heights <- h:
			}
		}
	}()

	var peerIdx int32 = -1
	pen := newPeerPenalties()
	peerFor := func(h uint64) string {
		cands := s.pool.PeersAboveHeight(0)
		if h > 0 {
			cands = s.pool.PeersAboveHeight(h - 1)
		}
		if len(cands) == 0 {
			cands = s.pool.Addrs()
		}
		if len(cands) == 0 {
			return ""
		}
		cands = pen.prefer(cands)
		i := atomic.AddInt32(&peerIdx, 1)
		return cands[int(i)%len(cands)]
	}

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for h := range heights {
				if err := ctx.Err(); err != nil {
					select {
					case results <- fetchResult{height: h, err: err}:
					default:
					}
					return
				}
				block, peerAddr, held, err := s.fetchWithBudget(ctx, budget, h, peerFor, pen.overran)
				if block != nil {
					s.resident.add(held)
				}
				select {
				case results <- fetchResult{height: h, block: block, peer: peerAddr, err: err, held: held}:
				case <-ctx.Done():
					budget.release(held)
					if block != nil {
						s.resident.sub(held)
					}
					return
				}
			}
		}()
	}
	go func() {
		wg.Wait()
		close(results)
	}()

	pending := make(map[uint64]fetchResult)
	next := localH + 1
	applied := 0
	defer func() {
		for _, r := range pending {
			if r.block != nil {
				s.resident.sub(r.held)
			}
		}
	}()

	for next <= target {
		if err := ctx.Err(); err != nil {
			return applied, err
		}
		budget.setHead(next)
		res, haveBuffered := pending[next]
		if !haveBuffered {
			r, ok := <-results
			if !ok {
				return applied, fmt.Errorf("sync: fetch pipeline closed before height %d arrived", next)
			}
			if r.height != next {
				pending[r.height] = r
				continue
			}
			res = r
		} else {
			delete(pending, next)
		}

		releaseHeld := func() {
			budget.release(res.held)
			if res.block != nil {
				s.resident.sub(res.held)
			}
		}
		if res.err != nil {
			releaseHeld()
			return applied, fmt.Errorf("sync: failed to fetch block %d: %w", next, res.err)
		}
		if err := VerifyBlockProducer(res.block, s.fetcher.TrustedPubKey(), s.fetcher.cfg.LookupProducerKey); err != nil {
			releaseHeld()
			return applied, fmt.Errorf("sync: apply authentication failed for block %d: %w", next, err)
		}
		if err := s.applyBlock(res.block); err != nil {
			releaseHeld()
			return applied, fmt.Errorf("sync: failed to apply block %d: %w", next, err)
		}
		releaseHeld()
		s.pool.UpdatePeerHeight(res.peer, next)
		applied++
		select {
		case window <- struct{}{}:
		default:
		}
		if applied%10 == 0 {
			log.Info().
				Uint64("height", next).
				Uint64("target", target).
				Int("applied", applied).
				Msg("sync: progress")
		}
		next++
	}

	return applied, nil
}

// IsSynced returns true if the local chain is within 1 block of the best peer.
func (s *Syncer) IsSynced() bool {
	localH := s.localHeight()
	bestH := s.pool.BestPeerHeight()
	return bestH <= localH+1
}

func blockHeldBytes(b *types.Block) int64 {
	if b == nil {
		return 1
	}
	raw, err := json.Marshal(b)
	if err != nil {
		return 1
	}
	if len(raw) == 0 {
		return 1
	}
	return int64(len(raw))
}
