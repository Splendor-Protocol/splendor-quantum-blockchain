package main

import (
	"encoding/hex"
	"testing"
	"time"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	genesiscfg "github.com/splendor-protocol/splendor-quantum-blockchain/genesis"
)

// Which path seals the next block is decided per block by consensus.Producer;
// see consensus/producer_test.go for the boot-at-activation, boot-below-
// activation and never-scheduled cases.

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

// A --validator handed a genesis document that names somebody else used to
// exit at start-up, which is why the only documented way onto a running chain
// was "start a full node, then restart it as a validator". With a peer to take
// the chain from it now joins instead; without one it still refuses, because
// there it could only mint a second, incompatible chain.
func TestValidatorStartRole(t *testing.T) {
	ourPub, _, _ := qcrypto.GenerateKeypair()
	theirPub, _, _ := qcrypto.GenerateKeypair()
	docFor := func(pub *qcrypto.PublicKey) *genesiscfg.Genesis {
		return &genesiscfg.Genesis{ValidatorPubKeyHex: hex.EncodeToString(pub.Bytes())}
	}
	peers := []string{"http://127.0.0.1:9999"}

	role, err := validatorStartRole(docFor(ourPub), ourPub.Bytes(), nil)
	if err != nil || role != roleMintGenesis {
		t.Fatalf("the document's own validator must mint: role=%v err=%v", role, err)
	}
	role, err = validatorStartRole(docFor(ourPub), ourPub.Bytes(), peers)
	if err != nil || role != roleMintGenesis {
		t.Fatalf("the founder mints even with peers configured: role=%v err=%v", role, err)
	}
	role, err = validatorStartRole(docFor(theirPub), ourPub.Bytes(), peers)
	if err != nil || role != roleJoin {
		t.Fatalf("a validator with a peer must join, not exit: role=%v err=%v", role, err)
	}
	if _, err := validatorStartRole(docFor(theirPub), ourPub.Bytes(), nil); err == nil {
		t.Fatal("a document naming someone else with NO peer must still be refused: " +
			"minting there forks the network the document describes")
	}
	role, err = validatorStartRole(nil, ourPub.Bytes(), nil)
	if err != nil || role != roleMintGenesis {
		t.Fatalf("no document and no peer is the solo dev node: role=%v err=%v", role, err)
	}
	role, err = validatorStartRole(nil, ourPub.Bytes(), peers)
	if err != nil || role != roleJoin {
		t.Fatalf("no document but a peer: the chain is the peer's, join it: role=%v err=%v", role, err)
	}
	if _, err := validatorStartRole(&genesiscfg.Genesis{ValidatorPubKeyHex: "zz"}, ourPub.Bytes(), peers); err == nil {
		t.Fatal("an unusable validator_pubkey_hex must be an error, not a silent join")
	}
}

// The joiner's readiness gate. syncer.IsSynced answers "synced" while no peer
// height is known, which is right for the chain's own producer and wrong for a
// node that has just been told to take someone else's chain: it would seal its
// own block 1 over genesis before the first pull ever ran.
func TestJoiningValidatorIsNotReadyUntilItHasABlock(t *testing.T) {
	_, ex := newBootstrapNode(t)
	ready := joiningValidatorReady(ex, func() bool { return true })
	if ready() {
		t.Fatal("a joining validator with an empty chain reported ready to propose")
	}

	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	b1, err := ex.ProduceBlock(nil, qcrypto.DeriveAddress(vpub), vpub.Bytes(), vpriv, [32]byte{}, 1)
	if err != nil {
		t.Fatalf("produce: %v", err)
	}
	_ = b1
	if !ready() {
		t.Fatal("with a block applied the gate must defer to the syncer")
	}
	if joiningValidatorReady(ex, func() bool { return false })() {
		t.Fatal("the gate must still refuse while the syncer says the node is behind")
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
