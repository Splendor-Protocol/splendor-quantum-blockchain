package rpc

import (
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network"
)

// TestStateSyncAbandonsACheckpointNoPeerCanServe is R3. A session that
// expires, or a peer that disappears, leaves the staged transfer pinned to a
// checkpoint the remaining peers have moved past. The height guard correctly
// refuses to splice pages from two different roots, but without an
// abandon-and-restart policy the client keeps reopening the same dead
// checkpoint and never syncs.
func TestStateSyncAbandonsACheckpointNoPeerCanServe(t *testing.T) {
	genesisPub, genesisPriv, _ := qcrypto.GenerateKeypair()

	live := []types.StateLeaf{
		{Key: []byte("acc:SQ:a"), Value: []byte(`{"balance":"1"}`)},
		{Key: []byte("acc:SQ:b"), Value: []byte(`{"balance":"2"}`)},
		{Key: []byte("acc:SQ:c"), Value: []byte(`{"balance":"3"}`)},
		{Key: []byte("acc:SQ:d"), Value: []byte(`{"balance":"4"}`)},
	}
	head := signedHead(t, 42, genesisPub, genesisPriv, state.SnapshotRoot(live))
	srv := chunkedSnapshotPeer(t, head, live)

	// Staging left behind by an interrupted transfer at a height the peers
	// have since moved past. Its leaves belong to a different root.
	dir := t.TempDir()
	stale := []types.StateLeaf{{Key: []byte("acc:SQ:a"), Value: []byte(`{"balance":"9"}`)}}
	staleHead := signedHead(t, 41, genesisPub, genesisPriv, state.SnapshotRoot(stale))
	d, err := state.OpenSnapshotDownload(dir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.AcceptHead(staleHead, 41); err != nil {
		t.Fatal(err)
	}
	if err := d.AppendPage(stale, stale[0].Key, false, "dead-session"); err != nil {
		t.Fatal(err)
	}

	cfg := network.DefaultSyncConfig(genesisPub.Bytes())
	cfg.Identities = network.NewIdentityCache()
	cfg.AllowPlaintext = false
	cfg.SnapshotStagingDir = dir
	cfg.MaxSnapshotDiskBytes = 1 << 20

	pool := network.NewPeerPool("http://self", 10)
	pool.Add(&network.PeerInfo{Addr: srv.URL})

	syncer := network.NewSyncer(cfg, pool, func() uint64 { return 0 }, func(*types.Block) error { return nil })

	var got *types.StateSnapshot
	if err := syncer.StateSync(func(sn *types.StateSnapshot) error { got = sn; return nil }); err != nil {
		t.Fatalf("state sync never abandoned a checkpoint no peer can serve: %v", err)
	}
	if got == nil || got.Height != 42 {
		t.Fatalf("imported snapshot = %v, want the peer's current head at 42", got)
	}
	if state.SnapshotRoot(got.Leaves) != state.SnapshotRoot(live) {
		t.Fatal("restarted transfer mixed leaves from the abandoned checkpoint with the new one")
	}
	if got.Block == nil || got.Block.Hash != head.Hash {
		t.Fatal("restarted transfer kept the abandoned head block")
	}
}
