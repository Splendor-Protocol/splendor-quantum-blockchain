package block

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// mintGenesis seals block 0 over the empty state every fresh executor starts
// from, so it applies on any of them.
func mintGenesis(t *testing.T, sdb *state.StateDB, vpub *qcrypto.PublicKey, vpriv *qcrypto.PrivateKey) *types.Block {
	t.Helper()
	root, err := sdb.StateRoot()
	if err != nil {
		t.Fatalf("state root: %v", err)
	}
	g := types.GenesisBlock(qcrypto.DeriveAddress(vpub), root, time.Now().Unix())
	g.ValidatorPubKey = vpub.Bytes()
	h, err := g.ComputeHash()
	if err != nil {
		t.Fatalf("hash genesis: %v", err)
	}
	g.Hash = h
	if err := g.SignBlock(vpriv); err != nil {
		t.Fatalf("sign genesis: %v", err)
	}
	return g
}

// reseal changes b's header and re-hashes and re-signs it, so the only thing
// wrong with the result is what mutate did.
func reseal(t *testing.T, b *types.Block, priv *qcrypto.PrivateKey, mutate func(*types.BlockHeader)) *types.Block {
	t.Helper()
	cp := *b
	mutate(&cp.Header)
	h, err := cp.ComputeVotedHash()
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	cp.Hash = h
	if err := cp.SignBlock(priv); err != nil {
		t.Fatalf("sign: %v", err)
	}
	return &cp
}

func headerHasGenesisKey(t *testing.T, h types.BlockHeader) bool {
	t.Helper()
	raw, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Contains(string(raw), `"genesis_hash"`)
}

// genesisHashStored was declared by #91 and never written, so the identity
// check could never run. It is the genesis of the chain this node is on: the
// stored block 0 (also after a restart), else the --genesis-hash pin, and a
// pin that contradicts the stored chain does not replace it.
func TestGenesisHashStoredIsAssignedWhereGenesisIsLoaded(t *testing.T) {
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	stateDir, chainDir := t.TempDir(), t.TempDir()

	sdb, err := state.NewStateDB(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	ex, err := NewExecutor(sdb, chainDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ex.GenesisHash(); ok {
		t.Fatal("an empty executor claims to know its genesis")
	}
	g := mintGenesis(t, sdb, vpub, vpriv)
	if err := ex.ExecuteBlock(g); err != nil {
		t.Fatalf("apply genesis: %v", err)
	}
	if got, ok := ex.GenesisHash(); !ok || got != g.Hash {
		t.Fatalf("after applying block 0: GenesisHash() = %x, %v; want %x", got[:8], ok, g.Hash[:8])
	}
	if err := ex.Close(); err != nil {
		t.Fatal(err)
	}
	if err := sdb.Close(); err != nil {
		t.Fatal(err)
	}

	sdb2, err := state.NewStateDB(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sdb2.Close() })
	ex2, err := NewExecutor(sdb2, chainDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ex2.Close() })
	if got, ok := ex2.GenesisHash(); !ok || got != g.Hash {
		t.Fatalf("after restart: GenesisHash() = %x, %v; want the stored block 0 %x", got[:8], ok, g.Hash[:8])
	}
	ex2.WithGenesisHash(testHash(0xcc))
	if got, _ := ex2.GenesisHash(); got != g.Hash {
		t.Fatalf("a contradicting pin replaced the stored genesis: %x", got[:8])
	}

	pinned, sdb3 := newTestExecutor(t)
	t.Cleanup(func() { _ = pinned.Close(); _ = sdb3.Close() })
	pinned.WithGenesisHash(testHash(0xaa))
	if got, ok := pinned.GenesisHash(); !ok || got != testHash(0xaa) {
		t.Fatalf("pinned empty executor: GenesisHash() = %x, %v; want the pin", got[:8], ok)
	}
}

// The chain-id comparison in #91 was inverted: a block from ANOTHER chain
// returned nil before anything was checked.
func TestVerifyChainIdentityRefusesAnotherChainID(t *testing.T) {
	ex, sdb := newTestExecutor(t)
	t.Cleanup(func() { _ = ex.Close(); _ = sdb.Close() })

	foreign := &types.Block{Header: types.BlockHeader{Height: 5, ChainID: "splendor-quantum-otherchain-1"}}
	err := ex.verifyChainIdentity(foreign)
	if err == nil || !strings.Contains(err.Error(), "chain id") {
		t.Fatalf("a block from another chain id passed the identity check: %v", err)
	}
	own := &types.Block{Header: types.BlockHeader{Height: 5, ChainID: types.ChainID}}
	if err := ex.verifyChainIdentity(own); err != nil {
		t.Fatalf("a legacy block on this chain id was refused: %v", err)
	}
}

// Before genesis_hash_v1 the producer seals the legacy header, and a block
// that carries genesis_hash is refused by both the vote check and the apply
// path — exactly as a node on the previous binary refuses it, since that node
// hashes the legacy bytes and gets a different hash.
func TestGenesisHashIsRefusedBeforeTheFork(t *testing.T) {
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	vaddr := qcrypto.DeriveAddress(vpub)
	producer, psdb := newTestExecutor(t)
	t.Cleanup(func() { _ = producer.Close(); _ = psdb.Close() })
	follower, fsdb := newTestExecutor(t)
	t.Cleanup(func() { _ = follower.Close(); _ = fsdb.Close() })

	g := mintGenesis(t, psdb, vpub, vpriv)
	for _, ex := range []*Executor{producer, follower} {
		if err := ex.ExecuteBlock(g); err != nil {
			t.Fatalf("apply genesis: %v", err)
		}
	}

	b1, err := producer.ProduceBlock(nil, vaddr, vpub.Bytes(), vpriv, g.Hash, 1)
	if err != nil {
		t.Fatalf("produce 1: %v", err)
	}
	if b1.Header.GenesisHash != ([32]byte{}) || headerHasGenesisKey(t, b1.Header) {
		t.Fatal("the producer bound genesis_hash before genesis_hash_v1 is scheduled")
	}

	bound := reseal(t, b1, vpriv, func(h *types.BlockHeader) { h.GenesisHash = g.Hash })
	if err := follower.ValidateProposal(bound); err == nil || !strings.Contains(err.Error(), forks.GenesisHashV1) {
		t.Fatalf("a proposal binding genesis_hash before the fork was not refused: %v", err)
	}
	if err := follower.ExecuteBlock(bound); err == nil || !strings.Contains(err.Error(), forks.GenesisHashV1) {
		t.Fatalf("a block binding genesis_hash before the fork was applied: %v", err)
	}
	if err := follower.ExecuteBlock(b1); err != nil {
		t.Fatalf("the legacy block was refused: %v", err)
	}
}

// From genesis_hash_v1 on, the producer stamps this chain's block 0 into every
// header, and a block that names no genesis, or another one, is refused before
// anyone votes for it and again when it would be applied.
func TestGenesisHashIsRequiredFromTheFork(t *testing.T) {
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	vaddr := qcrypto.DeriveAddress(vpub)
	producer, psdb := newTestExecutor(t)
	t.Cleanup(func() { _ = producer.Close(); _ = psdb.Close() })
	follower, fsdb := newTestExecutor(t)
	t.Cleanup(func() { _ = follower.Close(); _ = fsdb.Close() })

	schedule := forks.Schedule{forks.GenesisHashV1: 2}
	g := mintGenesis(t, psdb, vpub, vpriv)
	for _, ex := range []*Executor{producer, follower} {
		ex.SetForks(schedule)
		if err := ex.ExecuteBlock(g); err != nil {
			t.Fatalf("apply genesis: %v", err)
		}
	}

	b1, err := producer.ProduceBlock(nil, vaddr, vpub.Bytes(), vpriv, g.Hash, 1)
	if err != nil {
		t.Fatalf("produce 1: %v", err)
	}
	if headerHasGenesisKey(t, b1.Header) {
		t.Fatal("block 1 is below the activation height and must keep the legacy header")
	}
	if err := follower.ExecuteBlock(b1); err != nil {
		t.Fatalf("apply 1: %v", err)
	}

	b2, err := producer.ProduceBlock(nil, vaddr, vpub.Bytes(), vpriv, b1.Hash, 2)
	if err != nil {
		t.Fatalf("produce 2: %v", err)
	}
	if b2.Header.GenesisHash != g.Hash || !headerHasGenesisKey(t, b2.Header) {
		t.Fatalf("block 2 carries genesis_hash %x, want this chain's block 0 %x", b2.Header.GenesisHash[:8], g.Hash[:8])
	}

	foreign := reseal(t, b2, vpriv, func(h *types.BlockHeader) { h.GenesisHash = testHash(0xcc) })
	if err := follower.ValidateProposal(foreign); err == nil || !strings.Contains(err.Error(), "genesis") {
		t.Fatalf("a proposal naming another genesis was not refused: %v", err)
	}
	if err := follower.ExecuteBlock(foreign); err == nil || !strings.Contains(err.Error(), "genesis") {
		t.Fatalf("a block naming another genesis was applied: %v", err)
	}
	missing := reseal(t, b2, vpriv, func(h *types.BlockHeader) { h.GenesisHash = [32]byte{} })
	if err := follower.ExecuteBlock(missing); err == nil || !strings.Contains(err.Error(), "does not carry genesis_hash") {
		t.Fatalf("a post-fork block without genesis_hash was applied: %v", err)
	}
	if err := follower.ExecuteBlock(b2); err != nil {
		t.Fatalf("the bound block was refused: %v", err)
	}
	if h, _ := follower.LatestHeight(); h != 2 {
		t.Fatalf("follower head = %d, want 2", h)
	}
}

// A producer that cannot name its genesis must not seal a post-fork block that
// every other node would refuse; given a pin, it binds the pin.
func TestProducerNeedsItsGenesisToSealAfterTheFork(t *testing.T) {
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	vaddr := qcrypto.DeriveAddress(vpub)
	ex, sdb := newTestExecutor(t)
	t.Cleanup(func() { _ = ex.Close(); _ = sdb.Close() })
	ex.SetForks(forks.Schedule{forks.GenesisHashV1: 1})

	if _, err := ex.ProduceBlock(nil, vaddr, vpub.Bytes(), vpriv, testHash(0xaa), 1); err == nil || !strings.Contains(err.Error(), forks.GenesisHashV1) {
		t.Fatalf("a producer with no known genesis sealed a post-fork block: %v", err)
	}
	if head, _ := ex.LatestBlock(); head != nil {
		t.Fatalf("a refused production stored block %d", head.Header.Height)
	}

	ex.WithGenesisHash(testHash(0xaa))
	b, err := ex.ProduceBlock(nil, vaddr, vpub.Bytes(), vpriv, testHash(0xaa), 1)
	if err != nil {
		t.Fatalf("produce with a pin: %v", err)
	}
	if b.Header.GenesisHash != testHash(0xaa) {
		t.Fatalf("block 1 binds %x, want the pinned genesis", b.Header.GenesisHash[:8])
	}
}

// A node that holds no block 0 and no pin (a state-synced follower started
// with only --validator-pubkey-hex) learns its genesis from the first bound
// block it commits, then holds every later block to it.
func TestNodeWithoutAGenesisAdoptsTheOneItsChainBinds(t *testing.T) {
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	vaddr := qcrypto.DeriveAddress(vpub)
	schedule := forks.Schedule{forks.GenesisHashV1: 1}
	genesis := testHash(0xaa)

	producer, psdb := newTestExecutor(t)
	t.Cleanup(func() { _ = producer.Close(); _ = psdb.Close() })
	producer.SetForks(schedule)
	producer.WithGenesisHash(genesis)

	follower, fsdb := newTestExecutor(t)
	t.Cleanup(func() { _ = follower.Close(); _ = fsdb.Close() })
	follower.SetForks(schedule)

	b1, err := producer.ProduceBlock(nil, vaddr, vpub.Bytes(), vpriv, genesis, 1)
	if err != nil {
		t.Fatalf("produce 1: %v", err)
	}
	if err := follower.ValidateProposal(b1); err != nil {
		t.Fatalf("validate 1: %v", err)
	}
	if _, ok := follower.GenesisHash(); ok {
		t.Fatal("judging a proposal adopted a genesis; only a committed block may")
	}
	if err := follower.ExecuteBlock(b1); err != nil {
		t.Fatalf("apply 1: %v", err)
	}
	if got, ok := follower.GenesisHash(); !ok || got != genesis {
		t.Fatalf("follower did not adopt the genesis its chain binds: %x, %v", got[:8], ok)
	}

	b2, err := producer.ProduceBlock(nil, vaddr, vpub.Bytes(), vpriv, b1.Hash, 2)
	if err != nil {
		t.Fatalf("produce 2: %v", err)
	}
	foreign := reseal(t, b2, vpriv, func(h *types.BlockHeader) { h.GenesisHash = testHash(0xcc) })
	if err := follower.ExecuteBlock(foreign); err == nil {
		t.Fatal("after adopting a genesis the follower applied a block naming another")
	}
	if err := follower.ExecuteBlock(b2); err != nil {
		t.Fatalf("apply 2: %v", err)
	}
}

// State sync jumps straight to a head, so linkage never ties it to block 0;
// once headers are bound, the head itself does. A pinned node refuses a
// snapshot whose head names another genesis, an unpinned node adopts the
// genesis the authenticated head names, and a node whose schedule has no
// genesis_hash_v1 refuses a bound head as it would refuse the block.
func TestStateSnapshotHeadMustNameThisNodesGenesis(t *testing.T) {
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	vaddr := qcrypto.DeriveAddress(vpub)
	schedule := forks.Schedule{forks.GenesisHashV1: 1}
	genesis := testHash(0xaa)

	src, ssdb := newTestExecutor(t)
	t.Cleanup(func() { _ = src.Close(); _ = ssdb.Close() })
	src.SetForks(schedule)
	src.WithGenesisHash(genesis)
	fund(t, ssdb, testAddr(0x55), 1_000) // a snapshot needs at least one leaf
	prev := genesis
	for h := uint64(1); h <= 3; h++ {
		b, err := src.ProduceBlock(nil, vaddr, vpub.Bytes(), vpriv, prev, h)
		if err != nil {
			t.Fatalf("produce %d: %v", h, err)
		}
		prev = b.Hash
	}
	snap, err := src.ExportStateSnapshot()
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if snap.Block.Header.GenesisHash != genesis {
		t.Fatalf("snapshot head binds %x, want %x", snap.Block.Header.GenesisHash[:8], genesis[:8])
	}

	for _, tc := range []struct {
		name     string
		schedule forks.Schedule
		pin      *[32]byte
		wantErr  string
	}{
		{"pinned to another genesis", schedule, &[32]byte{0xcc}, "genesis"},
		{"pinned to this genesis", schedule, &genesis, ""},
		{"unpinned", schedule, nil, ""},
		{"schedule without the fork", nil, nil, forks.GenesisHashV1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dst, dsdb := newTestExecutor(t)
			t.Cleanup(func() { _ = dst.Close(); _ = dsdb.Close() })
			if tc.schedule != nil {
				dst.SetForks(tc.schedule)
			}
			if tc.pin != nil {
				dst.WithGenesisHash(*tc.pin)
			}
			err := dst.ImportStateSnapshot(snap)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("import = %v, want an error mentioning %q", err, tc.wantErr)
				}
				if head, _ := dst.LatestBlock(); head != nil {
					t.Fatalf("a refused snapshot stored head %d", head.Header.Height)
				}
				return
			}
			if err != nil {
				t.Fatalf("import: %v", err)
			}
			if got, ok := dst.GenesisHash(); !ok || got != genesis {
				t.Fatalf("after import GenesisHash() = %x, %v; want %x", got[:8], ok, genesis[:8])
			}
		})
	}
}
