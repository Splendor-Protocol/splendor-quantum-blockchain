package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/block"
	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	genesiscfg "github.com/splendor-protocol/splendor-quantum-blockchain/genesis"
)

// attackerAddr is the account a hostile bootstrap peer tries to fund by serving
// a doctored genesis.json.
const attackerAddr = "SQ:00000000000000000000000000000000000000000000000000000000000000ff"

const oneBillionSPLD = "1000000000000000000000000000"

// honestGenesis is the document the real network publishes.
func honestGenesis(t *testing.T) *genesiscfg.Genesis {
	t.Helper()
	pub, _, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	return &genesiscfg.Genesis{
		ChainID:            types.ChainID,
		NetworkName:        "Splendor Quantum",
		Timestamp:          1786618272,
		ValidatorPubKeyHex: hex.EncodeToString(pub.Bytes()),
		Allocations: []genesiscfg.Allocation{
			{Address: qcrypto.DeriveAddress(pub).String(), Balance: oneBillionSPLD, Comment: "Genesis validator treasury"},
		},
		TotalSupply: oneBillionSPLD,
	}
}

func TestVerifyGenesisDocumentAcceptsThePinnedDocument(t *testing.T) {
	g := honestGenesis(t)
	pin := g.Hash()
	if err := verifyGenesisDocument(g, &pin, "test"); err != nil {
		t.Fatalf("the pinned document was rejected: %v", err)
	}
}

// The attack the pin exists for: a peer that serves the real network's genesis
// with one extra allocation minting itself a balance.
func TestVerifyGenesisDocumentRejectsHostileAllocations(t *testing.T) {
	honest := honestGenesis(t)
	pin := honest.Hash()

	hostile := *honest
	hostile.Allocations = append(append([]genesiscfg.Allocation{}, honest.Allocations...),
		genesiscfg.Allocation{Address: attackerAddr, Balance: oneBillionSPLD, Comment: "Genesis validator treasury"})

	if err := verifyGenesisDocument(&hostile, &pin, "peer"); err == nil {
		t.Fatal("a genesis with an injected allocation satisfied the pin")
	}
}

func TestVerifyGenesisDocumentRejectsWrongChainID(t *testing.T) {
	g := honestGenesis(t)
	g.ChainID = "splendor-quantum-2"
	// Unpinned, so chain id is the only thing standing between this document and
	// the state database.
	if err := verifyGenesisDocument(g, nil, "peer"); err == nil {
		t.Fatal("a genesis for another chain was accepted")
	}
}

func TestVerifyGenesisDocumentRejectsInvalidDocument(t *testing.T) {
	for name, mutate := range map[string]func(*genesiscfg.Genesis){
		"no allocations":  func(g *genesiscfg.Genesis) { g.Allocations = nil },
		"bad address":     func(g *genesiscfg.Genesis) { g.Allocations[0].Address = "0xdeadbeef" },
		"negative amount": func(g *genesiscfg.Genesis) { g.Allocations[0].Balance = "-1" },
		"no validator":    func(g *genesiscfg.Genesis) { g.ValidatorPubKeyHex = "" },
	} {
		t.Run(name, func(t *testing.T) {
			g := honestGenesis(t)
			mutate(g)
			if err := verifyGenesisDocument(g, nil, "peer"); err == nil {
				t.Fatalf("%s passed validation", name)
			}
		})
	}
}

// Default policy: no local genesis, no pin, no opt-in — the node must refuse
// before it even asks the peer.
func TestBootstrapRefusesUnpinnedPeerGenesis(t *testing.T) {
	sdb, ex := newBootstrapNode(t)
	peer, hits := hostilePeer(t)

	err := bootstrapGenesisFromPeer(peer, sdb, ex, "", genesisPolicy{})
	if !errors.Is(err, errGenesisUnpinned) {
		t.Fatalf("want errGenesisUnpinned, got %v", err)
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("peer was contacted %d times before the policy check", n)
	}
	assertUnfunded(t, sdb, attackerAddr)

	// The operator has to be told how to proceed, not just refused.
	for _, want := range []string{"--genesis ", "--genesis-doc-hash", "--trust-peer-genesis"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal does not mention %q: %v", want, err)
		}
	}
}

// A pinned node applies the document the pin names.
func TestBootstrapAppliesPinnedPeerGenesis(t *testing.T) {
	sdb, ex := newBootstrapNode(t)
	honest := honestGenesis(t)
	peer, _ := genesisPeer(t, honest)
	pin := honest.Hash()

	if err := bootstrapGenesisFromPeer(peer, sdb, ex, "", genesisPolicy{docHash: &pin}); err != nil {
		t.Fatalf("pinned bootstrap failed: %v", err)
	}
	assertFunded(t, sdb, honest.Allocations[0].Address)
}

// ...and rejects the doctored one, without writing a byte of its allocations.
func TestBootstrapRejectsHostilePeerGenesisUnderPin(t *testing.T) {
	sdb, ex := newBootstrapNode(t)
	honest := honestGenesis(t)
	pin := honest.Hash()

	hostile := *honest
	hostile.Allocations = append(append([]genesiscfg.Allocation{}, honest.Allocations...),
		genesiscfg.Allocation{Address: attackerAddr, Balance: oneBillionSPLD})
	peer, _ := genesisPeer(t, &hostile)

	if err := bootstrapGenesisFromPeer(peer, sdb, ex, "", genesisPolicy{docHash: &pin}); err == nil {
		t.Fatal("hostile peer genesis was applied under a pin")
	}
	assertUnfunded(t, sdb, attackerAddr)
}

// The lab escape hatch stays open, deliberately and explicitly.
func TestBootstrapTrustPeerGenesisOptIn(t *testing.T) {
	sdb, ex := newBootstrapNode(t)
	honest := honestGenesis(t)
	peer, hits := genesisPeer(t, honest)

	if err := bootstrapGenesisFromPeer(peer, sdb, ex, "", genesisPolicy{trustPeer: true}); err != nil {
		t.Fatalf("opt-in bootstrap failed: %v", err)
	}
	if hits.Load() == 0 {
		t.Fatal("opt-in bootstrap never contacted the peer")
	}
	assertFunded(t, sdb, honest.Allocations[0].Address)
}

// A node holding the genesis on local disk uses it and never asks a peer —
// which is the branch every live tier is on.
func TestBootstrapPrefersLocalGenesisOverPeer(t *testing.T) {
	sdb, ex := newBootstrapNode(t)
	honest := honestGenesis(t)
	path := filepath.Join(t.TempDir(), "genesis.json")
	if err := honest.Save(path); err != nil {
		t.Fatalf("save genesis: %v", err)
	}
	peer, hits := hostilePeer(t)

	if err := bootstrapGenesisFromPeer(peer, sdb, ex, path, genesisPolicy{}); err != nil {
		t.Fatalf("local genesis bootstrap failed: %v", err)
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("a node with a local genesis contacted the peer %d times", n)
	}
	assertFunded(t, sdb, honest.Allocations[0].Address)
	assertUnfunded(t, sdb, attackerAddr)
}

// The live tiers are validators started with --genesis and no --peer. Neither
// shape reaches the peer-genesis path at all, whatever the pinning policy says.
func TestValidatorsNeverBootstrapGenesisFromPeers(t *testing.T) {
	cases := []struct {
		name        string
		isValidator bool
		peers       []string
		want        bool
	}{
		{"live tier: validator, no peers", true, nil, false},
		{"validator that was given peers", true, []string{"http://peer:8080"}, false},
		{"rpc node with no peers", false, nil, false},
		{"rpc node with peers", false, []string{"http://peer:8080"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := bootstrapsGenesisFromPeers(tc.isValidator, tc.peers); got != tc.want {
				t.Fatalf("bootstrapsGenesisFromPeers = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParsePinnedHash(t *testing.T) {
	full := "11" + hex.EncodeToString(make([]byte, 31))
	if pin, err := parsePinnedHash("--genesis-doc-hash", ""); err != nil || pin != nil {
		t.Fatalf("unset flag: got %v, %v", pin, err)
	}
	pin, err := parsePinnedHash("--genesis-doc-hash", "0x"+full)
	if err != nil {
		t.Fatalf("0x-prefixed pin rejected: %v", err)
	}
	if pin[0] != 0x11 {
		t.Fatalf("decoded pin %x", pin[:4])
	}
	if _, err := parsePinnedHash("--genesis-doc-hash", "abcd"); err == nil {
		t.Fatal("a short pin was accepted")
	}
	if _, err := parsePinnedHash("--genesis-doc-hash", "zz"+full[2:]); err == nil {
		t.Fatal("a non-hex pin was accepted")
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func newBootstrapNode(t *testing.T) (*state.StateDB, *block.Executor) {
	t.Helper()
	sdb, err := state.NewStateDB(t.TempDir())
	if err != nil {
		t.Fatalf("statedb: %v", err)
	}
	t.Cleanup(func() { sdb.Close() })
	ex, err := block.NewExecutor(sdb, t.TempDir())
	if err != nil {
		t.Fatalf("executor: %v", err)
	}
	t.Cleanup(func() { ex.Close() })
	return sdb, ex
}

// genesisPeer serves a genesis document and counts how often it was asked for it.
func genesisPeer(t *testing.T, g *genesiscfg.Genesis) (string, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_ = json.NewEncoder(w).Encode(g)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &hits
}

// hostilePeer serves the real chain id with an allocation minting attackerAddr
// a billion SPLD.
func hostilePeer(t *testing.T) (string, *atomic.Int64) {
	t.Helper()
	g := honestGenesis(t)
	g.Allocations = append(g.Allocations, genesiscfg.Allocation{
		Address: attackerAddr,
		Balance: oneBillionSPLD,
		Comment: "Genesis validator treasury",
	})
	return genesisPeer(t, g)
}

func assertFunded(t *testing.T, sdb *state.StateDB, addr string) {
	t.Helper()
	if balanceOf(t, sdb, addr).Sign() == 0 {
		t.Fatalf("%s was not funded", addr)
	}
}

func assertUnfunded(t *testing.T, sdb *state.StateDB, addr string) {
	t.Helper()
	if bal := balanceOf(t, sdb, addr); bal.Sign() != 0 {
		t.Fatalf("%s was minted %s from an unverified genesis", addr, bal)
	}
}

func balanceOf(t *testing.T, sdb *state.StateDB, addr string) *big.Int {
	t.Helper()
	a, err := qcrypto.AddressFromString(addr)
	if err != nil {
		t.Fatalf("address %q: %v", addr, err)
	}
	bal, err := sdb.GetBalance(a)
	if err != nil {
		return new(big.Int)
	}
	return bal
}
