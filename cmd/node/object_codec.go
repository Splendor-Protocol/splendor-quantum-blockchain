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

func repairFor(sourceCount int) int {
	n := sourceCount / 4
	if n < 1 {
		return 1
	}
	return n
}

// publishSnapshotObject encodes the node's current state snapshot as a
// (possibly multi-generation) coded object signed by the node identity.
// Existing snapshot validation still runs on the reconstructed bytes at the
// receiver; this only authenticates the transfer.
func publishSnapshotObject(hub *objects.Hub, codec coding.Codec, exec *block.Executor, signer *qcrypto.PrivateKey) error {
	if hub.Current() != nil {
		return nil
	}
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
	if hub.Current() != nil {
		return nil
	}
	if !gate.CompareAndSwap(false, true) {
		return errors.New("a coded-object publish is already in progress, retry shortly")
	}
	defer gate.Store(false)
	if hub.Current() != nil {
		return nil
	}
	_, priv := ident.SigningKeys()
	return publishSnapshotObject(hub, codec, exec, priv)
}
