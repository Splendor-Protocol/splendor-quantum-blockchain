package main

import (
	"encoding/hex"
	"testing"
	"time"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// The producer decides BFT for the block it is ABOUT to seal, not for the
// head it already has: on the boot that mints a chain the head is 0, and an
// activation at 1 was never active at 0, so the producer stayed single-path
// for the whole life of the chain.
func TestBFTChosenForNextHeight(t *testing.T) {
	if !bftForNextBlock(forks.Schedule{forks.MultiValidator: 1}, 0) {
		t.Fatal("head 0 with activation at 1 must run BFT for block 1")
	}
	if bftForNextBlock(forks.Schedule{forks.MultiValidator: 5}, 3) {
		t.Fatal("block 4 is before the activation at 5")
	}
	if !bftForNextBlock(forks.Schedule{forks.MultiValidator: 5}, 4) {
		t.Fatal("block 5 is the first BFT block")
	}
	if bftForNextBlock(nil, 100) {
		t.Fatal("a nil schedule never activates")
	}
	if bftForNextBlock(forks.Schedule{forks.MultiValidator: 0}, 100) {
		t.Fatal("height 0 means never")
	}
}

// --validator-pubkey-hex names the trusted genesis key on every node. A
// validator without it trusts its own key (the producer that minted the
// chain); a full node without it has nothing to trust yet.
func TestResolveTrustedProducerKey(t *testing.T) {
	genesisPub, _, _ := qcrypto.GenerateKeypair()
	localPub, _, _ := qcrypto.GenerateKeypair()
	flag := hex.EncodeToString(genesisPub.Bytes())

	got, err := resolveTrustedProducerKey(flag, true, localPub.Bytes())
	if err != nil || string(got) != string(genesisPub.Bytes()) {
		t.Fatalf("validator with the flag: got %v err=%v, want the flag key", got != nil, err)
	}
	got, err = resolveTrustedProducerKey("0x"+flag, false, nil)
	if err != nil || string(got) != string(genesisPub.Bytes()) {
		t.Fatalf("full node with the 0x flag: err=%v", err)
	}
	got, err = resolveTrustedProducerKey("", true, localPub.Bytes())
	if err != nil || string(got) != string(localPub.Bytes()) {
		t.Fatalf("validator without the flag must trust its own key: err=%v", err)
	}
	got, err = resolveTrustedProducerKey("", false, nil)
	if err != nil || got != nil {
		t.Fatalf("full node without the flag: got %v err=%v, want nil", got, err)
	}
	if _, err := resolveTrustedProducerKey("zz", false, nil); err == nil {
		t.Fatal("bad hex accepted")
	}
	if _, err := resolveTrustedProducerKey("abcd", false, nil); err == nil {
		t.Fatal("wrong-size key accepted")
	}
}

// The BFT fallback set names whoever sealed block 0. Block 0 itself is the
// record when the chain DB holds it; a joined node never has it (sync starts
// at height 1), so the trusted key stands in.
func TestGenesisValidatorInfo(t *testing.T) {
	sdb, ex := newBootstrapNode(t)
	trustedPub, _, _ := qcrypto.GenerateKeypair()

	if _, ok := genesisValidatorInfo(ex, nil); ok {
		t.Fatal("no block 0 and no trusted key: nothing to fall back to")
	}
	info, ok := genesisValidatorInfo(ex, trustedPub.Bytes())
	if !ok || info.Address != qcrypto.DeriveAddress(trustedPub) || string(info.PublicKey) != string(trustedPub.Bytes()) {
		t.Fatalf("trusted key not used as the fallback: ok=%v info=%+v", ok, info)
	}

	genPub, genPriv, _ := qcrypto.GenerateKeypair()
	root, _ := sdb.StateRoot()
	g := types.GenesisBlock(qcrypto.DeriveAddress(genPub), root, time.Now().Unix())
	g.ValidatorPubKey = genPub.Bytes()
	h, _ := g.ComputeHash()
	g.Hash = h
	if err := g.SignBlock(genPriv); err != nil {
		t.Fatal(err)
	}
	if err := ex.ExecuteBlock(g); err != nil {
		t.Fatalf("commit genesis: %v", err)
	}
	info, ok = genesisValidatorInfo(ex, trustedPub.Bytes())
	if !ok || info.Address != qcrypto.DeriveAddress(genPub) {
		t.Fatalf("stored block 0 must win over the trusted key: ok=%v addr=%s", ok, info.Address)
	}
	if !info.IsActive {
		t.Fatal("fallback validator must be marked active")
	}
}

// A BFT commit races the pull-sync loop and the block push for the same
// block. If the other path applied it first the commit is still a commit,
// not a failure that leaves the engine believing the height is open.
func TestApplyCommittedBlockToleratesAlreadyApplied(t *testing.T) {
	_, ex := newBootstrapNode(t)
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	b1, err := ex.ProduceBlock(nil, qcrypto.DeriveAddress(vpub), vpub.Bytes(), vpriv, [32]byte{}, 1)
	if err != nil {
		t.Fatalf("produce: %v", err)
	}
	if err := applyCommittedBlock(ex, b1); err != nil {
		t.Fatalf("the head block itself must count as committed: %v", err)
	}

	_, other := newBootstrapNode(t)
	opub, opriv, _ := qcrypto.GenerateKeypair()
	rival, err := other.ProduceBlock(nil, qcrypto.DeriveAddress(opub), opub.Bytes(), opriv, [32]byte{}, 1)
	if err != nil {
		t.Fatalf("produce rival: %v", err)
	}
	if err := applyCommittedBlock(ex, rival); err == nil {
		t.Fatal("a different block at the head height was reported committed")
	}
	if h, _ := ex.LatestHeight(); h != 1 {
		t.Fatalf("head = %d, want 1", h)
	}
}
