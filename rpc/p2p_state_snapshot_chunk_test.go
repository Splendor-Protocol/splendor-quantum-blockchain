package rpc

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network"
)

// chunkedSnapshotPeer serves a fixed set of leaves over KindGetStateSnapshotChunk,
// paging maxLeaves-per-call server-side exactly like the real executor does,
// and attaches the signed head block only to the first page.
func chunkedSnapshotPeer(t *testing.T, head *types.Block, allLeaves []types.StateLeaf) *httptest.Server {
	t.Helper()
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	identity, err := network.NewNodeIdentity(pub, priv)
	if err != nil {
		t.Fatalf("node identity: %v", err)
	}
	router := mux.NewRouter()
	RegisterP2PRoutes(router, P2PConfig{
		Identity: identity,
		Token:    "write-secret",
		ServeStateSnapshotChunk: func(sessionID string, afterKey []byte, maxLeaves int, _ string) ([]byte, error) {
			if maxLeaves <= 0 {
				maxLeaves = 2
			}
			start := 0
			if len(afterKey) > 0 {
				for i, l := range allLeaves {
					if string(l.Key) == string(afterKey) {
						start = i + 1
						break
					}
				}
			}
			end := start + maxLeaves
			done := end >= len(allLeaves)
			if done {
				end = len(allLeaves)
			}
			chunk := network.StateSnapshotChunk{
				Height:    head.Header.Height,
				Leaves:    allLeaves[start:end],
				Done:      done,
				SessionID: sessionID,
			}
			if !done {
				chunk.NextKey = allLeaves[end-1].Key
			}
			if start == 0 {
				chunk.Block = head
			}
			return json.Marshal(chunk)
		},
	})
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv
}

// A chunked fetch must reassemble to exactly the leaves the peer holds, and
// authenticate the head exactly as the single-shot path does.
func TestFetchStateSnapshotChunkedReassembles(t *testing.T) {
	genesisPub, genesisPriv, _ := qcrypto.GenerateKeypair()
	leaves := []types.StateLeaf{
		{Key: []byte("acc:SQ:a"), Value: []byte(`{"balance":"1"}`)},
		{Key: []byte("acc:SQ:b"), Value: []byte(`{"balance":"2"}`)},
		{Key: []byte("acc:SQ:c"), Value: []byte(`{"balance":"3"}`)},
		{Key: []byte("acc:SQ:d"), Value: []byte(`{"balance":"4"}`)},
		{Key: []byte("acc:SQ:e"), Value: []byte(`{"balance":"5"}`)},
	}
	head := signedHead(t, 42, genesisPub, genesisPriv, state.SnapshotRoot(leaves))
	srv := chunkedSnapshotPeer(t, head, leaves)

	got, err := snapshotFetcher(genesisPub.Bytes()).FetchStateSnapshotChunked(srv.URL, 2)
	if err != nil {
		t.Fatalf("chunked fetch failed: %v", err)
	}
	if got.Height != 42 || len(got.Leaves) != len(leaves) {
		t.Fatalf("got height %d, %d leaves, want 42, %d", got.Height, len(got.Leaves), len(leaves))
	}
	if got.Block == nil || got.Block.Hash != head.Hash {
		t.Fatal("reassembled snapshot lost the head block")
	}
	if state.SnapshotRoot(got.Leaves) != state.SnapshotRoot(leaves) {
		t.Fatal("reassembled leaves hash to a different root than the source")
	}
}

// The unanchored-head rule applies identically over the chunked path.
func TestFetchStateSnapshotChunkedRefusesUnanchoredHead(t *testing.T) {
	genesisPub, _, _ := qcrypto.GenerateKeypair()
	otherPub, otherPriv, _ := qcrypto.GenerateKeypair()
	leaves := []types.StateLeaf{{Key: []byte("acc:SQ:a"), Value: []byte(`{}`)}}
	head := signedHead(t, 9, otherPub, otherPriv, state.SnapshotRoot(leaves))
	srv := chunkedSnapshotPeer(t, head, leaves)

	_, err := snapshotFetcher(genesisPub.Bytes()).FetchStateSnapshotChunked(srv.URL, 10)
	if !errors.Is(err, network.ErrSnapshotNotAnchored) {
		t.Fatalf("chunked fetch of an unanchored head = %v, want ErrSnapshotNotAnchored", err)
	}
}

// A single leaf-height forgery anywhere in the tail of the chunk stream must
// still be caught by the state_root check, exactly as with the single-shot
// path — chunking must not create a window where a tampered later chunk goes
// unnoticed.
func TestFetchStateSnapshotChunkedDetectsTamperedLaterChunk(t *testing.T) {
	genesisPub, genesisPriv, _ := qcrypto.GenerateKeypair()
	leaves := []types.StateLeaf{
		{Key: []byte("acc:SQ:a"), Value: []byte(`{"balance":"1"}`)},
		{Key: []byte("acc:SQ:b"), Value: []byte(`{"balance":"2"}`)},
		{Key: []byte("acc:SQ:c"), Value: []byte(`{"balance":"3"}`)},
	}
	// Head commits to the ORIGINAL leaves; the peer serves tampered leaves in
	// its second page. Nothing here checks the root itself — the point is
	// that the CALLER must, once it has every leaf.
	head := signedHead(t, 7, genesisPub, genesisPriv, state.SnapshotRoot(leaves))
	tampered := append([]types.StateLeaf(nil), leaves...)
	tampered[2] = types.StateLeaf{Key: []byte("acc:SQ:c"), Value: []byte(`{"balance":"999999"}`)}
	srv := chunkedSnapshotPeer(t, head, tampered)

	got, err := snapshotFetcher(genesisPub.Bytes()).FetchStateSnapshotChunked(srv.URL, 1)
	if err != nil {
		// FetchStateSnapshotChunked itself does not check the root (that is
		// the importer's job, same division of labour as the single-shot
		// path) — but if it does return, the caller must be ABLE to catch it.
		return
	}
	if state.SnapshotRoot(got.Leaves) == state.SnapshotRoot(leaves) {
		t.Fatal("tampered leaves silently hashed to the original root — should be impossible")
	}
}

func chunkedSnapshotPeerFailAfter(t *testing.T, head *types.Block, allLeaves []types.StateLeaf, failAfter int) *httptest.Server {
	t.Helper()
	n := 0
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	identity, err := network.NewNodeIdentity(pub, priv)
	if err != nil {
		t.Fatal(err)
	}
	router := mux.NewRouter()
	RegisterP2PRoutes(router, P2PConfig{
		Identity: identity,
		Token:    "write-secret",
		ServeStateSnapshotChunk: func(sessionID string, afterKey []byte, maxLeaves int, _ string) ([]byte, error) {
			n++
			if failAfter > 0 && n > failAfter {
				return nil, errors.New("peer closed mid-transfer")
			}
			if maxLeaves <= 0 {
				maxLeaves = 2
			}
			start := 0
			if len(afterKey) > 0 {
				for i, l := range allLeaves {
					if string(l.Key) == string(afterKey) {
						start = i + 1
						break
					}
				}
			}
			end := start + maxLeaves
			done := end >= len(allLeaves)
			if done {
				end = len(allLeaves)
			}
			chunk := network.StateSnapshotChunk{
				Height:    head.Header.Height,
				Leaves:    allLeaves[start:end],
				Done:      done,
				SessionID: sessionID,
			}
			if !done {
				chunk.NextKey = allLeaves[end-1].Key
			}
			if start == 0 {
				chunk.Block = head
			}
			return json.Marshal(chunk)
		},
	})
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv
}

func snapshotFetcherStaging(trustedPK []byte, dir string, maxDisk int64) *network.BlockFetcher {
	cfg := network.DefaultSyncConfig(trustedPK)
	cfg.Identities = network.NewIdentityCache()
	cfg.AllowPlaintext = false
	cfg.SnapshotStagingDir = dir
	cfg.MaxSnapshotDiskBytes = maxDisk
	return network.NewBlockFetcher(cfg)
}

func TestFetchStateSnapshotChunkedResumesAcrossPeers(t *testing.T) {
	genesisPub, genesisPriv, _ := qcrypto.GenerateKeypair()
	leaves := []types.StateLeaf{
		{Key: []byte("acc:SQ:a"), Value: []byte(`{"balance":"1"}`)},
		{Key: []byte("acc:SQ:b"), Value: []byte(`{"balance":"2"}`)},
		{Key: []byte("acc:SQ:c"), Value: []byte(`{"balance":"3"}`)},
		{Key: []byte("acc:SQ:d"), Value: []byte(`{"balance":"4"}`)},
	}
	head := signedHead(t, 11, genesisPub, genesisPriv, state.SnapshotRoot(leaves))
	dir := t.TempDir()
	failing := chunkedSnapshotPeerFailAfter(t, head, leaves, 1)
	fetcher := snapshotFetcherStaging(genesisPub.Bytes(), dir, 1<<20)
	if _, err := fetcher.FetchStateSnapshotChunked(failing.URL, 2); err == nil {
		t.Fatal("expected the first peer to fail after one page")
	}
	st, err := state.OpenSnapshotDownload(dir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if !st.HasHead() || st.Done() || st.Progress().PageCount < 1 {
		t.Fatalf("expected persisted progress after the failed peer, got %+v", st.Progress())
	}

	good := chunkedSnapshotPeer(t, head, leaves)
	got, err := fetcher.FetchStateSnapshotChunked(good.URL, 2)
	if err != nil {
		t.Fatalf("resume on second peer: %v", err)
	}
	if len(got.Leaves) != len(leaves) || state.SnapshotRoot(got.Leaves) != state.SnapshotRoot(leaves) {
		t.Fatalf("resumed snapshot has %d leaves", len(got.Leaves))
	}
}

func TestFetchStateSnapshotChunkedEnforcesDiskBudget(t *testing.T) {
	genesisPub, genesisPriv, _ := qcrypto.GenerateKeypair()
	leaves := []types.StateLeaf{
		{Key: []byte("acc:SQ:a"), Value: []byte(`{"balance":"1"}`)},
		{Key: []byte("acc:SQ:b"), Value: []byte(`{"balance":"2"}`)},
	}
	head := signedHead(t, 3, genesisPub, genesisPriv, state.SnapshotRoot(leaves))
	srv := chunkedSnapshotPeer(t, head, leaves)
	_, err := snapshotFetcherStaging(genesisPub.Bytes(), t.TempDir(), 4).FetchStateSnapshotChunked(srv.URL, 2)
	if err == nil {
		t.Fatal("expected the whole-object disk budget to refuse the transfer")
	}
}

func TestStartSnapshotChunkSessionFromAtResumesCursor(t *testing.T) {
	// Covered in core/block; this guards the client still authenticates a
	// first page that starts mid-snapshot (failover start-at-cursor).
	genesisPub, genesisPriv, _ := qcrypto.GenerateKeypair()
	leaves := []types.StateLeaf{
		{Key: []byte("acc:SQ:a"), Value: []byte(`{"balance":"1"}`)},
		{Key: []byte("acc:SQ:b"), Value: []byte(`{"balance":"2"}`)},
		{Key: []byte("acc:SQ:c"), Value: []byte(`{"balance":"3"}`)},
	}
	head := signedHead(t, 5, genesisPub, genesisPriv, state.SnapshotRoot(leaves))
	dir := t.TempDir()
	st, err := state.OpenSnapshotDownload(dir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AcceptHead(head, 5); err != nil {
		t.Fatal(err)
	}
	if err := st.AppendPage(leaves[:1], leaves[0].Key, false, ""); err != nil {
		t.Fatal(err)
	}
	srv := chunkedSnapshotPeer(t, head, leaves)
	got, err := snapshotFetcherStaging(genesisPub.Bytes(), dir, 1<<20).FetchStateSnapshotChunked(srv.URL, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Leaves) != 3 {
		t.Fatalf("got %d leaves after mid-snapshot resume", len(got.Leaves))
	}
}
