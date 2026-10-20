package block

import (
	"testing"
	"time"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// Block 1 is the one block with no stored parent to link against. Unpinned, a
// node with an empty chain will build on any genesis a peer cares to invent; a
// pinned node must refuse every block 1 that does not name the pinned genesis.
func TestBlockOneOnEmptyChainMustNameThePinnedGenesis(t *testing.T) {
	genesisHash := testHash(0xaa)
	imposter := testHash(0xbb)

	for _, tc := range []struct {
		name       string
		prevHash   [32]byte
		wantReject bool
	}{
		{"builds on the pinned genesis", genesisHash, false},
		{"builds on someone else's genesis", imposter, true},
		{"builds on nothing at all", [32]byte{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := produceBlockOne(t, tc.prevHash)

			ex, sdb := newTestExecutor(t)
			defer ex.Close()
			defer sdb.Close()
			ex.WithGenesisHash(genesisHash)

			err := ex.ExecuteBlock(b)
			if tc.wantReject && err == nil {
				t.Fatal("block 1 on a foreign genesis was accepted")
			}
			if !tc.wantReject && err != nil {
				t.Fatalf("block 1 on the pinned genesis was rejected: %v", err)
			}
		})
	}
}

// Without a pin the check does not exist — this is the behaviour every node
// running today has, and the change must not alter it.
func TestUnpinnedExecutorStillAcceptsBlockOne(t *testing.T) {
	b := produceBlockOne(t, testHash(0xbb))

	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()

	if err := ex.ExecuteBlock(b); err != nil {
		t.Fatalf("unpinned executor rejected block 1: %v", err)
	}
}

// A pinned node must also refuse to commit a genesis block that is not the
// pinned one — including one it built itself after its data dir was wiped.
func TestPinnedExecutorRejectsAForeignGenesisBlock(t *testing.T) {
	vpub, vpriv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	matching, sdb1 := newTestExecutor(t)
	defer matching.Close()
	defer sdb1.Close()

	// An unfunded genesis, so the header commits to the empty state root every
	// fresh node computes.
	stateRoot, err := sdb1.StateRoot()
	if err != nil {
		t.Fatalf("state root: %v", err)
	}
	g := types.GenesisBlock(qcrypto.DeriveAddress(vpub), stateRoot, time.Now().Unix())
	g.ValidatorPubKey = vpub.Bytes()
	hash, err := g.ComputeHash()
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	g.Hash = hash
	if err := g.SignBlock(vpriv); err != nil {
		t.Fatalf("sign: %v", err)
	}

	matching.WithGenesisHash(hash)
	if err := matching.ExecuteBlock(g); err != nil {
		t.Fatalf("the pinned genesis block was rejected: %v", err)
	}

	foreign, sdb2 := newTestExecutor(t)
	defer foreign.Close()
	defer sdb2.Close()
	foreign.WithGenesisHash(testHash(0xcc))
	if err := foreign.ExecuteBlock(g); err == nil {
		t.Fatal("a genesis block that is not the pinned one was committed")
	}
}

// produceBlockOne seals an empty block 1 on top of prevHash.
func produceBlockOne(t *testing.T, prevHash [32]byte) *types.Block {
	t.Helper()
	vpub, vpriv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	producer, sdb := newTestExecutor(t)
	defer producer.Close()
	defer sdb.Close()

	b, err := producer.ProduceBlock(nil, qcrypto.DeriveAddress(vpub), vpub.Bytes(), vpriv, prevHash, 1)
	if err != nil {
		t.Fatalf("produce block 1: %v", err)
	}
	return b
}

func testHash(b byte) [32]byte {
	var h [32]byte
	for i := range h {
		h[i] = b
	}
	return h
}
