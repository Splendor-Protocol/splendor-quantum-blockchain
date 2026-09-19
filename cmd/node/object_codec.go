package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/block"
	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network/coding"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network/objects"
)

// objectSymbolSize is the live-path generation payload size. Snapshots larger
// than one generation (MaxSourceSymbols of these) are split across generations
// by NewObjectSender, so a snapshot of any supported size is served coded.
const objectSymbolSize = 16 * 1024

// repairFor commits repair symbols at 50% of the source count. The netbench
// harness shows why this is the default: at 20% simulated loss, 25% repair
// leaves both coded codecs starved of rank in most trials, while 50% repair
// recovers every trial (uncoded, with no repair, recovers none). Repair
// symbols are only requested after systematic ones are lost, so a clean link
// pays only the one-time publish cost, not extra transfer.
func repairFor(sourceCount int) int {
	n := sourceCount / 2
	if n < 1 {
		return 1
	}
	return n
}

// objectSnapshotRepublishLag is how far the published snapshot's anchor may
// fall behind the chain head before the next coded request republishes at the
// current head. A node that has been running for hours otherwise serves a
// snapshot anchored at an old head and every joiner pays the block catch-up.
// A var (not a const) so tests can shrink the lag; it is never mutated in
// production.
var objectSnapshotRepublishLag = uint64(256)

// objectSnapshotStale reports whether a snapshot published at height
// published is too old to serve given a chain head of head.
func objectSnapshotStale(published, head uint64) bool {
	return head > published+objectSnapshotRepublishLag
}

// publishSnapshotObject encodes the node's current state snapshot as a
// (possibly multi-generation) coded object signed by the node identity.
// Existing snapshot validation still runs on the reconstructed bytes at the
// receiver; this only authenticates the transfer. Put swaps atomically, so a
// republish never leaves the hub in a half-published state.
func publishSnapshotObject(hub *objects.Hub, codec coding.Codec, exec *block.Executor, signer *qcrypto.PrivateKey) error {
	snap, err := exec.ExportStateSnapshot()
	if err != nil {
		return fmt.Errorf("state snapshot not available: %w", err)
	}
	if snap == nil || snap.Block == nil {
		return errors.New("state snapshot carries no head block")
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	desc := objects.ObjectDescriptor{
		Anchor: objects.Anchor{
			Kind:   "snapshot",
			ID:     snap.Block.Hash,
			Height: snap.Height,
		},
		Codec:      codec.Name(),
		SymbolSize: objectSymbolSize,
	}
	repair := repairFor((len(raw) + objectSymbolSize - 1) / objectSymbolSize)
	send, err := objects.NewObjectSender(raw, desc, codec, repair, signer, objects.DefaultLimits())
	if err != nil {
		return fmt.Errorf("encode coded snapshot: %w", err)
	}
	hub.Put(send)
	return nil
}

func ensurePublishedSnapshot(hub *objects.Hub, codec coding.Codec, exec *block.Executor, ident *network.NodeIdentity, gate *atomic.Bool) error {
	if cur := hub.Current(); cur != nil {
		head, err := exec.LatestHeight()
		if err != nil || !objectSnapshotStale(cur.Manifest().Anchor.Height, head) {
			return nil
		}
		// Stale: republish at the current head. The old object keeps serving
		// until the new one is Put, so the swap is atomic from a request's
		// point of view.
	}
	if !gate.CompareAndSwap(false, true) {
		return errors.New("a coded-object publish is already in progress, retry shortly")
	}
	defer gate.Store(false)
	if cur := hub.Current(); cur != nil {
		head, err := exec.LatestHeight()
		if err != nil || !objectSnapshotStale(cur.Manifest().Anchor.Height, head) {
			return nil // someone else republished while we waited for the gate
		}
	}
	_, priv := ident.SigningKeys()
	return publishSnapshotObject(hub, codec, exec, priv)
}

// blockBatchCache holds recently built coded block batches. Each requested
// range is a distinct object, so a bounded cache (oldest evicted first) keeps
// the hot catch-up ranges served without letting an attacker fill memory with
// arbitrary ranges: a cached entry is replaced or evicted, never grown past
// the cap. A batch lost to eviction mid-transfer just falls back to per-block
// fetching on the client.
var blockBatchCache = objects.NewCache(8)

// maxServedBlockBatchBlocks bounds the range one on-demand batch covers.
const maxServedBlockBatchBlocks = 128

// publishBlockBatch encodes count consecutive blocks starting at fromHeight as
// a coded object signed by the node identity and stashes it in the batch
// cache. Returns the SQCO object manifest frame; the block bytes themselves
// are served as symbol proofs by descriptor ID. Anchor kind "block" with the
// LAST block of the range, so a client refuses a manifest that names a
// different range before spending symbol work.
func publishBlockBatch(exec *block.Executor, codec coding.Codec, signer *qcrypto.PrivateKey, fromHeight uint64, count int) ([]byte, error) {
	if count < 1 || count > maxServedBlockBatchBlocks {
		return nil, fmt.Errorf("block batch count %d outside [1,%d]", count, maxServedBlockBatchBlocks)
	}
	blocks := make([]*types.Block, 0, count)
	for h := fromHeight; h < fromHeight+uint64(count); h++ {
		b, err := exec.GetBlockByHeight(h)
		if err != nil {
			return nil, fmt.Errorf("block %d unavailable: %w", h, err)
		}
		if b == nil {
			return nil, fmt.Errorf("block %d unavailable", h)
		}
		blocks = append(blocks, b)
	}
	raw, err := json.Marshal(blocks)
	if err != nil {
		return nil, err
	}
	last := blocks[len(blocks)-1]
	desc := objects.ObjectDescriptor{
		Anchor: objects.Anchor{
			Kind:   "block",
			ID:     last.Hash,
			Height: last.Header.Height,
		},
		Codec:      codec.Name(),
		SymbolSize: objectSymbolSize,
	}
	repair := repairFor((len(raw) + objectSymbolSize - 1) / objectSymbolSize)
	send, err := objects.NewObjectSender(raw, desc, codec, repair, signer, objects.DefaultLimits())
	if err != nil {
		return nil, fmt.Errorf("encode coded block batch: %w", err)
	}
	blockBatchCache.Put(send)
	return objects.EncodeObjectManifest(send.Manifest())
}

// serveBatchSymbol returns the symbol proof for a cached block-batch object,
// so get_object_symbol_v1 requests resolve against the batch cache when the
// snapshot hub does not hold the descriptor.
func serveBatchSymbol(descID []byte, generation uint32, index uint32) ([]byte, error) {
	send, err := blockBatchCache.Get(descID)
	if err != nil {
		return nil, err
	}
	gen := int(generation)
	if gen < 0 || gen >= send.GenerationCount() {
		return nil, fmt.Errorf("objects: generation %d out of range [0,%d)", gen, send.GenerationCount())
	}
	if uint64(index) >= uint64(send.SymbolCount(gen)) {
		return nil, fmt.Errorf("objects: symbol index %d out of range [0,%d) in generation %d", index, send.SymbolCount(gen), gen)
	}
	proof, err := send.Proof(gen, int(index))
	if err != nil {
		return nil, err
	}
	return objects.EncodeSymbolProof(proof)
}
