package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/block"
	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
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
const objectSnapshotRepublishLag = 256

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
