package main

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/block"
	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	genesiscfg "github.com/splendor-protocol/splendor-quantum-blockchain/genesis"
)

func anchorNode(t *testing.T, stateDir string) (*state.StateDB, *block.Executor) {
	t.Helper()
	sdb, err := state.NewStateDB(stateDir)
	if err != nil {
		t.Fatalf("statedb: %v", err)
	}
	ex, err := block.NewExecutor(sdb, t.TempDir())
	if err != nil {
		t.Fatalf("executor: %v", err)
	}
	t.Cleanup(func() { _ = ex.Close(); _ = sdb.Close() })
	return sdb, ex
}

func writeGenesisFile(t *testing.T, g *genesiscfg.Genesis) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "genesis.json")
	if err := g.Save(path); err != nil {
		t.Fatalf("save genesis: %v", err)
	}
	return path
}

func gatedGenesis(t *testing.T) *genesiscfg.Genesis {
	t.Helper()
	g := honestGenesis(t)
	g.Forks = forks.Schedule{forks.ModuleRevertV1: 1, forks.ValidatorGatesV1: 1}
	return g
}

// The document names the chain's authority, so applying it must record it —
// the same moment, and for the same reason, as the fork schedule.
func TestApplyLocalGenesisRecordsTheAnchor(t *testing.T) {
	sdb, ex := anchorNode(t, t.TempDir())
	g := gatedGenesis(t)
	want, err := g.ValidatorAddress()
	if err != nil {
		t.Fatalf("validator address: %v", err)
	}
	pin := g.Hash()

	if _, err := applyLocalGenesis(writeGenesisFile(t, g), sdb, ex, genesisPolicy{docHash: &pin}); err != nil {
		t.Fatalf("applyLocalGenesis: %v", err)
	}
	if got := ex.GenesisValidator(); got != want {
		t.Fatalf("executor anchor = %s, want %s", got, want)
	}
	stored, ok, err := readStoredGenesisValidator(sdb)
	if err != nil || !ok || stored != want {
		t.Fatalf("stored anchor = (%s, %v, %v), want %s", stored, ok, err, want)
	}
}

// A follower that learns its rules from a peer must learn the authority those
// rules are judged against in the same breath.
func TestPeerBootstrapRecordsTheAnchor(t *testing.T) {
	sdb, ex := anchorNode(t, t.TempDir())
	g := gatedGenesis(t)
	want, _ := g.ValidatorAddress()
	peer, _ := genesisPeer(t, g)
	pin := g.Hash()

	if err := bootstrapGenesisFromPeer(peer, sdb, ex, genesisPolicy{docHash: &pin}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if got := ex.GenesisValidator(); got != want {
		t.Fatalf("executor anchor = %s, want %s", got, want)
	}
}

// The recorded value survives a restart on a host that no longer has the
// document — the case the fork schedule is persisted for, and the case that
// would otherwise leave a follower judging admissions differently.
func TestAnchorSurvivesARestartWithoutTheDocument(t *testing.T) {
	stateDir := t.TempDir()
	sdb, ex := anchorNode(t, stateDir)
	g := gatedGenesis(t)
	want, _ := g.ValidatorAddress()
	peer, _ := genesisPeer(t, g)
	pin := g.Hash()
	if err := bootstrapGenesisFromPeer(peer, sdb, ex, genesisPolicy{docHash: &pin}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if err := ex.Close(); err != nil {
		t.Fatalf("close executor: %v", err)
	}
	if err := sdb.Close(); err != nil {
		t.Fatalf("close statedb: %v", err)
	}

	sdb2, ex2 := anchorNode(t, stateDir)
	if err := installGenesisValidator(sdb2, ex2, "", nil, g.Forks, true); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if got := ex2.GenesisValidator(); got != want {
		t.Fatalf("anchor after restart = %s, want %s", got, want)
	}
}

// --validator-pubkey-hex names the GENESIS validator's key, which is what a
// full node already verifies every block signature against. A node with only
// that flag can still anchor.
func TestAnchorFromThePinnedValidatorKey(t *testing.T) {
	sdb, ex := anchorNode(t, t.TempDir())
	pub, _, _ := qcrypto.GenerateKeypair()
	pinned, err := pinnedGenesisValidatorKey(hex.EncodeToString(pub.Bytes()))
	if err != nil {
		t.Fatalf("decode pin: %v", err)
	}
	if err := installGenesisValidator(sdb, ex, "", pinned,
		forks.Schedule{forks.ModuleRevertV1: 1, forks.ValidatorGatesV1: 1}, false); err != nil {
		t.Fatalf("install: %v", err)
	}
	if got, want := ex.GenesisValidator(), qcrypto.DeriveAddress(pub); got != want {
		t.Fatalf("anchor = %s, want %s", got, want)
	}
	if _, err := pinnedGenesisValidatorKey("not hex"); err == nil {
		t.Error("a malformed pin must be refused")
	}
	if b, err := pinnedGenesisValidatorKey(""); err != nil || b != nil {
		t.Errorf("an absent pin is not an error: %v, %v", b, err)
	}
}

// The refusal that keeps a misconfigured node from forking silently: the
// anchor feeds a state transition, so running without one on a gated chain is
// a divergence waiting for the first set_pass.
func TestGatedChainRefusesToStartWithoutAnAnchor(t *testing.T) {
	sdb, ex := anchorNode(t, t.TempDir())
	gated := forks.Schedule{forks.ModuleRevertV1: 1, forks.ValidatorGatesV1: 10}

	err := installGenesisValidator(sdb, ex, "", nil, gated, false)
	if err == nil {
		t.Fatal("a gated chain with no anchor must refuse to start")
	}
	if !strings.Contains(err.Error(), "--genesis") {
		t.Errorf("the refusal must say how to fix it: %v", err)
	}

	// An EMPTY chain is exempt: genesis has not been applied yet this run, and
	// both apply paths record the anchor as they go.
	if err := installGenesisValidator(sdb, ex, "", nil, gated, true); err != nil {
		t.Fatalf("an empty chain must be allowed to proceed to genesis: %v", err)
	}
	// A chain that does not schedule the gates never needs one.
	if err := installGenesisValidator(sdb, ex, "", nil, forks.Schedule{forks.MultiValidator: 5}, false); err != nil {
		t.Fatalf("an ungated chain must not require an anchor: %v", err)
	}
}

// A chain does not get a second genesis. A document naming a different key is
// the wrong network's document, or the wrong data directory.
func TestAnchorRefusesToChange(t *testing.T) {
	sdb, ex := anchorNode(t, t.TempDir())
	first, _, _ := qcrypto.GenerateKeypair()
	second, _, _ := qcrypto.GenerateKeypair()

	if err := persistGenesisValidator(sdb, ex, qcrypto.DeriveAddress(first)); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := persistGenesisValidator(sdb, ex, qcrypto.DeriveAddress(first)); err != nil {
		t.Fatalf("re-recording the same anchor must be a no-op: %v", err)
	}
	err := persistGenesisValidator(sdb, ex, qcrypto.DeriveAddress(second))
	if err == nil {
		t.Fatal("a different anchor must be refused")
	}
	if !strings.Contains(err.Error(), "fork it") {
		t.Errorf("refusal = %v", err)
	}
	if got := ex.GenesisValidator(); got != qcrypto.DeriveAddress(first) {
		t.Errorf("the refused anchor was installed anyway: %s", got)
	}
}

// A file this node was pointed at is a claim; block 0 is a fact, and its
// validator address is inside the header the --genesis-hash pin covers.
func TestAnchorPrefersBlockZeroOverTheDocument(t *testing.T) {
	sdb, ex := anchorNode(t, t.TempDir())
	g := gatedGenesis(t)
	path := writeGenesisFile(t, g)

	pub, priv, _ := qcrypto.GenerateKeypair()
	root, _ := sdb.StateRoot()
	genesisBlock := types.GenesisBlock(qcrypto.DeriveAddress(pub), root, 1)
	genesisBlock.ValidatorPubKey = pub.Bytes()
	hash, err := genesisBlock.ComputeHash()
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	genesisBlock.Hash = hash
	if err := genesisBlock.SignBlock(priv); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := ex.ExecuteBlock(genesisBlock); err != nil {
		t.Fatalf("seal block 0: %v", err)
	}

	addr, source, err := resolveGenesisValidator(sdb, ex, path, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if source != "block 0" || addr != qcrypto.DeriveAddress(pub) {
		t.Fatalf("resolved (%s, %s), want block 0's validator", addr, source)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the document should still be there: %v", err)
	}
}
