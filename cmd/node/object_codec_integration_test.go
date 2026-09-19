package main

import (
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/block"
	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network/coding"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network/objects"
)

// commitGenesis commits block 0 sealed by pub and returns its hash. root must
// be the live DB's state root at genesis time.
func commitGenesis(t *testing.T, ex *block.Executor, pub *qcrypto.PublicKey, priv *qcrypto.PrivateKey, root [32]byte) [32]byte {
	t.Helper()
	genesis := types.GenesisBlock(qcrypto.DeriveAddress(pub), root, time.Now().Unix())
	genesis.ValidatorPubKey = pub.Bytes()
	h, err := genesis.ComputeHash()
	if err != nil {
		t.Fatal(err)
	}
	genesis.Hash = h
	if err := genesis.SignBlock(priv); err != nil {
		t.Fatal(err)
	}
	if err := ex.ExecuteBlock(genesis); err != nil {
		t.Fatalf("commit genesis: %v", err)
	}
	return genesis.Hash
}

// extendChain commits count more blocks at heights from..from+count-1, sealed
// by pub, linked to prevHash, with the unchanged state root (no txs).
// Returns the last block's hash.
func extendChain(t *testing.T, ex *block.Executor, pub *qcrypto.PublicKey, priv *qcrypto.PrivateKey, root [32]byte, prev [32]byte, from, count uint64) [32]byte {
	t.Helper()
	for i := from; i < from+count; i++ {
		b := &types.Block{Header: types.BlockHeader{
			Height: i, Timestamp: time.Now().Unix(), ChainID: types.ChainID,
			AlgID: qcrypto.AlgMLDSA65, TxMerkleRoot: qcrypto.Hash32([]byte{}),
			StateRoot: root, PrevHash: prev,
			ValidatorAddr: qcrypto.DeriveAddress(pub),
		}}
		b.ValidatorPubKey = pub.Bytes()
		bh, err := b.ComputeHash()
		if err != nil {
			t.Fatal(err)
		}
		b.Hash = bh
		if err := b.SignBlock(priv); err != nil {
			t.Fatal(err)
		}
		if err := ex.ExecuteBlock(b); err != nil {
			t.Fatalf("commit block %d: %v", i, err)
		}
		prev = bh
	}
	return prev
}

// decodePublishedSnapshot reconstructs the hub's published snapshot object
// pinned to signerPub and returns the decoded StateSnapshot, exercising the
// full publish + manifest + symbol-proof + authenticate-before-decode path
// against a REAL executor export.
func decodePublishedSnapshot(t *testing.T, hub *objects.Hub, signerPub *qcrypto.PublicKey) *types.StateSnapshot {
	t.Helper()
	cur := hub.Current()
	if cur == nil {
		t.Fatal("hub has no published snapshot")
	}
	frame, err := hub.ServeManifest(nil)
	if err != nil {
		t.Fatal(err)
	}
	m, err := objects.DecodeObjectManifest(frame)
	if err != nil {
		t.Fatal(err)
	}
	recv, err := objects.NewObjectReceiver(m, objects.DefaultLimits(), objects.AllowObjectSignerBytes(signerPub.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	descID := m.DescriptorID()
	for gen := 0; gen < cur.GenerationCount(); gen++ {
		for i := uint32(0); i < uint32(cur.SymbolCount(gen)); i++ {
			proofRaw, err := hub.ServeSymbol(descID[:], uint32(gen), i)
			if err != nil {
				t.Fatal(err)
			}
			proof, err := objects.DecodeSymbolProof(proofRaw)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := recv.AbsorbProof(gen, proof); err != nil {
				t.Fatal(err)
			}
		}
	}
	raw, err := recv.Decode()
	if err != nil {
		t.Fatal(err)
	}
	var snap types.StateSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatal(err)
	}
	return &snap
}

// The node-side serve path end to end with a real executor: the exported
// state snapshot is coded with RLNC, and the published manifest + symbol
// proofs reconstruct the exact snapshot anchored at the head block.
func TestPublishSnapshotObjectWithRealExecutor(t *testing.T) {
	sdb, ex := newBootstrapNode(t)
	pub, priv, _ := qcrypto.GenerateKeypair()
	root, err := sdb.StateRoot()
	if err != nil {
		t.Fatal(err)
	}
	genesisHash := commitGenesis(t, ex, pub, priv, root)
	headHash := extendChain(t, ex, pub, priv, root, genesisHash, 1, 2)

	hub := objects.NewHub()
	if err := publishSnapshotObject(hub, coding.NewRLNCCodec(), ex, priv); err != nil {
		t.Fatalf("publish coded snapshot: %v", err)
	}
	cur := hub.Current()
	if cur == nil {
		t.Fatal("no snapshot published")
	}
	if cur.Manifest().Anchor.Height != 2 || cur.Manifest().Anchor.ID != headHash {
		t.Fatalf("manifest anchors height %d, want 2 (head %x)", cur.Manifest().Anchor.Height, headHash[:8])
	}

	snap := decodePublishedSnapshot(t, hub, pub)
	if snap.Height != 2 || snap.Block == nil || snap.Block.Hash != headHash {
		t.Fatalf("decoded snapshot = height %d head %v, want 2 %x", snap.Height, snap.Block, headHash[:8])
	}
	liveRoot, lerr := sdb.StateRoot()
	if lerr != nil {
		t.Fatal(lerr)
	}
	if snap.Block.Header.StateRoot != liveRoot {
		t.Fatal("decoded snapshot head commits a different state root than the live DB")
	}
}

// A stale published snapshot is republished at the current head once the head
// has advanced past the lag, so late joiners never pay a large catch-up.
func TestEnsurePublishedSnapshotRepublishesWhenStale(t *testing.T) {
	sdb, ex := newBootstrapNode(t)
	pub, priv, _ := qcrypto.GenerateKeypair()
	ident, err := network.NewNodeIdentity(pub, priv)
	if err != nil {
		t.Fatal(err)
	}

	oldLag := objectSnapshotRepublishLag
	objectSnapshotRepublishLag = 3
	t.Cleanup(func() { objectSnapshotRepublishLag = oldLag })

	root, srootErr := sdb.StateRoot()
	if srootErr != nil {
		t.Fatal(srootErr)
	}
	genesisHash := commitGenesis(t, ex, pub, priv, root)
	head := extendChain(t, ex, pub, priv, root, genesisHash, 1, 1)
	hub := objects.NewHub()
	var gate atomic.Bool
	if err := ensurePublishedSnapshot(hub, coding.NewUncodedCodec(), ex, ident, &gate); err != nil {
		t.Fatalf("first publish: %v", err)
	}
	if got := hub.Current().Manifest().Anchor.Height; got != 1 {
		t.Fatalf("first publish anchored at %d, want 1", got)
	}

	// Fresh: head == published, no republish.
	if err := ensurePublishedSnapshot(hub, coding.NewUncodedCodec(), ex, ident, &gate); err != nil {
		t.Fatal(err)
	}
	if got := hub.Current().Manifest().Anchor.Height; got != 1 {
		t.Fatalf("fresh snapshot wrongly republished at %d", got)
	}

	// Advance the head 3 more blocks: published 1, head 4 — exactly at the
	// lag boundary it is still fresh; one past it, the next request
	// republishes.
	head = extendChain(t, ex, pub, priv, root, head, 2, 3)
	if err := ensurePublishedSnapshot(hub, coding.NewUncodedCodec(), ex, ident, &gate); err != nil {
		t.Fatal(err)
	}
	if got := hub.Current().Manifest().Anchor.Height; got != 1 {
		t.Fatalf("snapshot exactly at the lag boundary wrongly republished at %d", got)
	}

	headHash := extendChain(t, ex, pub, priv, root, head, 5, 1) // head 5, one past the boundary
	if err := ensurePublishedSnapshot(hub, coding.NewUncodedCodec(), ex, ident, &gate); err != nil {
		t.Fatal(err)
	}
	if got := hub.Current().Manifest().Anchor.Height; got != 5 {
		t.Fatalf("stale snapshot not republished: anchored at %d, want 5", got)
	}
	if hub.Current().Manifest().Anchor.ID != headHash {
		t.Fatal("republished snapshot anchors the wrong head hash")
	}
	_ = sdb
}

// The batch range comes from an untrusted request body: an over-cap count or
// a wrapping range must be refused before any block lookup (the nil executor
// proves the guard fires first).
func TestPublishBlockBatchRejectsDegenerateRanges(t *testing.T) {
	if _, err := publishBlockBatch(nil, coding.NewUncodedCodec(), nil, 10, network.MaxCodedBlockBatchBlocks+1); err == nil {
		t.Fatal("over-cap count accepted")
	}
	if _, err := publishBlockBatch(nil, coding.NewUncodedCodec(), nil, ^uint64(0), 2); err == nil {
		t.Fatal("wrapping range accepted")
	}
}
