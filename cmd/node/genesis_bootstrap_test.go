package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/block"
	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
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
	withCleanChainID(t)
	g := honestGenesis(t)
	g.ChainID = "splendor-quantum-2"

	// Since per-tier ids, the document names the chain and --chain-id is what
	// contradicts it. The deploy entrypoint always passes it, so this is the
	// production shape.
	flagChainID = types.DefaultChainID

	if err := verifyGenesisDocument(g, nil, "peer"); err == nil {
		t.Fatal("a genesis for another chain was accepted despite --chain-id naming this one")
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

	err := bootstrapGenesisFromPeer(peer, sdb, ex, genesisPolicy{})
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

	if err := bootstrapGenesisFromPeer(peer, sdb, ex, genesisPolicy{docHash: &pin}); err != nil {
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

	if err := bootstrapGenesisFromPeer(peer, sdb, ex, genesisPolicy{docHash: &pin}); err == nil {
		t.Fatal("hostile peer genesis was applied under a pin")
	}
	assertUnfunded(t, sdb, attackerAddr)
}

// The lab escape hatch stays open, deliberately and explicitly.
func TestBootstrapTrustPeerGenesisOptIn(t *testing.T) {
	sdb, ex := newBootstrapNode(t)
	honest := honestGenesis(t)
	peer, hits := genesisPeer(t, honest)

	if err := bootstrapGenesisFromPeer(peer, sdb, ex, genesisPolicy{trustPeer: true}); err != nil {
		t.Fatalf("opt-in bootstrap failed: %v", err)
	}
	if hits.Load() == 0 {
		t.Fatal("opt-in bootstrap never contacted the peer")
	}
	assertFunded(t, sdb, honest.Allocations[0].Address)
}

// A node holding the genesis on local disk credits that, and never asks a peer —
// which is the branch every live tier is on.
func TestLocalGenesisIsAppliedWithoutAskingAnyPeer(t *testing.T) {
	sdb, ex := newBootstrapNode(t)
	honest := honestGenesis(t)
	path := saveGenesis(t, honest)

	if bootstrapsGenesisFromPeers(false, []string{"http://peer:8080"}, path) {
		t.Fatal("a node with a local genesis still takes the peer-genesis path")
	}
	if _, err := applyLocalGenesis(path, sdb, ex, genesisPolicy{}); err != nil {
		t.Fatalf("local genesis apply failed: %v", err)
	}
	assertFunded(t, sdb, honest.Allocations[0].Address)
	assertUnfunded(t, sdb, attackerAddr)
}

// The local document is the one path that reaches ApplyToState on every live
// tier, so it has to be gated too — a genesis for another chain is a
// misconfiguration that must stop the node, not a supply it mints in silence.
func TestLocalGenesisForAnotherChainIsRefusedWhenChainIDIsAsserted(t *testing.T) {
	sdb, ex := newBootstrapNode(t)
	g := honestGenesis(t)
	g.ChainID = "splendor-quantum-2"
	g.Allocations = append(g.Allocations, genesiscfg.Allocation{Address: attackerAddr, Balance: oneBillionSPLD})
	g.TotalSupply = "" // derived from the allocations; the chain id is what is on trial here

	// --chain-id is what the deploy entrypoint always passes. It is the operator
	// stating which network this node is, so a document naming another one is a
	// misconfiguration to stop on, not a network to join.
	prev := flagChainID
	flagChainID = types.DefaultChainID
	t.Cleanup(func() { flagChainID = prev })

	_, err := applyLocalGenesis(saveGenesis(t, g), sdb, ex, genesisPolicy{})
	if err == nil {
		t.Fatal("a genesis for another chain was credited despite --chain-id naming this one")
	}
	if !strings.Contains(err.Error(), types.DefaultChainID) {
		t.Fatalf("refusal does not name the chain this node was told it is on: %v", err)
	}
	assertUnfunded(t, sdb, attackerAddr)
	assertUnfunded(t, sdb, g.Allocations[0].Address)
}

// The other half of the same rule. Per-tier chain ids exist precisely so a
// tier's own genesis can name a chain this binary has never heard of, so on an
// empty chain with nothing asserting otherwise the document is the authority.
// Refusing here would make it impossible to start devnet or testnet at all.
func TestLocalGenesisNamesTheChainWhenNothingContradictsIt(t *testing.T) {
	sdb, ex := newBootstrapNode(t)
	g := honestGenesis(t)
	g.ChainID = "splendor-quantum-devnet-1"
	g.TotalSupply = ""

	prevFlag, prevID, prevAdopted := flagChainID, types.ChainID, types.ChainIDAdoptedForTest()
	flagChainID = ""
	t.Cleanup(func() {
		flagChainID = prevFlag
		types.RestoreChainIDForTest(prevID, prevAdopted)
	})
	types.RestoreChainIDForTest(types.DefaultChainID, false)

	if _, err := applyLocalGenesis(saveGenesis(t, g), sdb, ex, genesisPolicy{}); err != nil {
		t.Fatalf("a tier's own genesis must be creditable: %v", err)
	}
	if types.ChainID != "splendor-quantum-devnet-1" {
		t.Fatalf("node did not adopt the chain named by its genesis: ChainID = %q", types.ChainID)
	}
}

// A pin an operator sets governs the local file too. Otherwise the wrong
// genesis.json on disk — a stale copy, another tier's — is credited before
// anything notices, and ApplyToState never removes an account it wrote.
func TestLocalGenesisMustMatchThePin(t *testing.T) {
	sdb, ex := newBootstrapNode(t)
	honest := honestGenesis(t)
	pin := honest.Hash()

	stale := *honest
	stale.Allocations = append(append([]genesiscfg.Allocation{}, honest.Allocations...),
		genesiscfg.Allocation{Address: attackerAddr, Balance: oneBillionSPLD})

	if _, err := applyLocalGenesis(saveGenesis(t, &stale), sdb, ex, genesisPolicy{docHash: &pin}); err == nil {
		t.Fatal("a local genesis that fails the pin was credited")
	}
	assertUnfunded(t, sdb, attackerAddr)

	// ...and the document the pin names still applies.
	if _, err := applyLocalGenesis(saveGenesis(t, honest), sdb, ex, genesisPolicy{docHash: &pin}); err != nil {
		t.Fatalf("the pinned local genesis was rejected: %v", err)
	}
	assertFunded(t, sdb, honest.Allocations[0].Address)
}

// A peer that answers 404 has nothing to give. It must not count as success:
// that would stop the caller trying the peers behind it.
func TestPeerWithoutGenesisEndpointCreditsNothingAndYieldsToTheNextPeer(t *testing.T) {
	sdb, ex := newBootstrapNode(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	honest := honestGenesis(t)
	pin := honest.Hash()

	err := bootstrapGenesisFromPeer(srv.URL, sdb, ex, genesisPolicy{docHash: &pin})
	if err == nil {
		t.Fatal("a peer with no /genesis reported success")
	}
	if errors.Is(err, errGenesisUnpinned) {
		t.Fatalf("a missing endpoint was reported as a policy fault: %v", err)
	}
	assertUnfunded(t, sdb, honest.Allocations[0].Address)
}

// A peer that answers /genesis with a redirect is trying to make this node fetch
// a document from somewhere it never approved. The redirect must not be
// followed, the target must never be contacted, and nothing may be credited.
func TestPeerGenesisRedirectIsNotFollowed(t *testing.T) {
	sdb, ex := newBootstrapNode(t)
	honest := honestGenesis(t)
	pin := honest.Hash()

	// The redirect target serves a document that passes the pin, so a client
	// that followed the hop would fund the node and the test would not notice
	// the SSRF; the hit counter is what catches it.
	target, targetHits := genesisPeer(t, honest)
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target+"/genesis", http.StatusFound)
	}))
	t.Cleanup(redirector.Close)

	err := bootstrapGenesisFromPeer(redirector.URL, sdb, ex, genesisPolicy{docHash: &pin})
	if err == nil {
		t.Fatal("a redirecting /genesis was accepted")
	}
	if !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("the error does not say a redirect was refused: %v", err)
	}
	if n := targetHits.Load(); n != 0 {
		t.Fatalf("the redirect target was contacted %d time(s); redirects must not be followed", n)
	}
	assertUnfunded(t, sdb, honest.Allocations[0].Address)
}

// The chain-id gate is only worth anything if it compares against something the
// DOCUMENT did not supply. That anchor used to be a compiled-in constant; since
// per-tier ids it is the operator's --chain-id, because the constant would make
// devnet and testnet unstartable. A document that supplies the id it is checked
// against still proves nothing, and that is what this pins.
func TestChainIDIsCheckedAgainstTheOperatorsAssertion(t *testing.T) {
	withCleanChainID(t)
	g := honestGenesis(t)
	g.ChainID = "splendor-quantum-1-evil"
	flagChainID = types.DefaultChainID

	err := verifyGenesisDocument(g, nil, "peer")
	if err == nil {
		t.Fatal("a self-declared chain id was accepted")
	}
	if !strings.Contains(err.Error(), types.DefaultChainID) {
		t.Fatalf("the error does not quote the id this node was told it is on %q: %v", types.DefaultChainID, err)
	}
}

// And the converse, which is the whole reason the constant had to go: with no
// operator assertion, a tier's own genesis names a chain this binary has never
// heard of and must be accepted.
func TestUnassertedChainIDIsAdoptedFromTheDocument(t *testing.T) {
	withCleanChainID(t)
	g := honestGenesis(t)
	g.ChainID = "splendor-quantum-devnet-1"
	flagChainID = ""

	if err := verifyGenesisDocument(g, nil, "local file"); err != nil {
		t.Fatalf("a tier's own genesis must be creditable: %v", err)
	}
	if types.ChainID != "splendor-quantum-devnet-1" {
		t.Fatalf("the document's chain was not adopted: ChainID = %q", types.ChainID)
	}
}

// withCleanChainID isolates a test from the package-level chain identity.
//
// Without it these tests pass or fail depending on which other test ran first:
// the identity is process-wide and set-once, so one test adopting an id silently
// decides the outcome of the next. Two of these tests were doing exactly that --
// green in a package run, failing when run alone.
func withCleanChainID(t *testing.T) {
	t.Helper()
	prevFlag, prevID, prevAdopted := flagChainID, types.ChainID, types.ChainIDAdoptedForTest()
	t.Cleanup(func() {
		flagChainID = prevFlag
		types.RestoreChainIDForTest(prevID, prevAdopted)
	})
	flagChainID = ""
	types.RestoreChainIDForTest(types.DefaultChainID, false)
}

// The live tiers are validators started with --genesis and no --peer. Neither
// shape reaches the peer-genesis path at all, whatever the pinning policy says.
func TestValidatorsNeverBootstrapGenesisFromPeers(t *testing.T) {
	const localGenesis = "/var/sqc/genesis.json"
	cases := []struct {
		name        string
		isValidator bool
		peers       []string
		genesisPath string
		want        bool
	}{
		{"live tier: validator, --genesis, no peers", true, nil, localGenesis, false},
		{"validator that was given peers", true, []string{"http://peer:8080"}, localGenesis, false},
		{"rpc node with no peers", false, nil, "", false},
		{"documented full node: --genesis and --peer", false, []string{"http://peer:8080"}, localGenesis, false},
		{"rpc node with peers and no local genesis", false, []string{"http://peer:8080"}, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := bootstrapsGenesisFromPeers(tc.isValidator, tc.peers, tc.genesisPath); got != tc.want {
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

// ── fork schedule persistence ─────────────────────────────────────────────────

// scheduledGenesis is the honest document with a rule change scheduled.
func scheduledGenesis(t *testing.T, at uint64) *genesiscfg.Genesis {
	t.Helper()
	g := honestGenesis(t)
	g.Forks = forks.Schedule{forks.MultiValidator: at}
	return g
}

func assertSchedule(t *testing.T, got forks.Schedule, wantAt uint64) {
	t.Helper()
	if at, ok := got.At(forks.MultiValidator); !ok || at != wantAt {
		t.Fatalf("schedule = %v, want %s at %d", got, forks.MultiValidator, wantAt)
	}
}

// The production path: a validator (or documented full node) applies its own
// --genesis. The schedule must land in the state DB and on the live executor.
func TestLocalGenesisApplyPersistsTheForkSchedule(t *testing.T) {
	sdb, ex := newBootstrapNode(t)
	g := scheduledGenesis(t, 7)

	if _, err := applyLocalGenesis(saveGenesis(t, g), sdb, ex, genesisPolicy{}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertSchedule(t, ex.Forks(), 7)

	stored, ok, err := readStoredForkSchedule(sdb)
	if err != nil || !ok {
		t.Fatalf("stored schedule = (%v, %v, %v), want recorded", stored, ok, err)
	}
	assertSchedule(t, stored, 7)
}

// The other apply site: a follower with no local file takes the document from
// a peer. Its schedule has to be persisted and installed exactly the same way —
// this node has nothing else to read it from on its next boot.
func TestPeerBootstrapPersistsTheForkSchedule(t *testing.T) {
	sdb, ex := newBootstrapNode(t)
	g := scheduledGenesis(t, 7)
	peer, _ := genesisPeer(t, g)
	pin := g.Hash()

	if err := bootstrapGenesisFromPeer(peer, sdb, ex, genesisPolicy{docHash: &pin}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	assertSchedule(t, ex.Forks(), 7)

	stored, ok, err := readStoredForkSchedule(sdb)
	if err != nil || !ok {
		t.Fatalf("stored schedule = (%v, %v, %v), want recorded", stored, ok, err)
	}
	assertSchedule(t, stored, 7)
}

// A peer-bootstrapped follower restarts with no --genesis at all. Before the
// schedule was persisted it came back with nil — and would have applied the
// next block under no rules while the validator applied it under the fork.
func TestFollowerRestartWithoutAFileKeepsTheSchedule(t *testing.T) {
	stateDir := t.TempDir()
	sdb, err := state.NewStateDB(stateDir)
	if err != nil {
		t.Fatalf("statedb: %v", err)
	}
	ex, err := block.NewExecutor(sdb, t.TempDir())
	if err != nil {
		t.Fatalf("executor: %v", err)
	}
	g := scheduledGenesis(t, 7)
	peer, _ := genesisPeer(t, g)
	pin := g.Hash()
	if err := bootstrapGenesisFromPeer(peer, sdb, ex, genesisPolicy{docHash: &pin}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	// Restart: close everything and reopen the same state directory.
	if err := ex.Close(); err != nil {
		t.Fatalf("close executor: %v", err)
	}
	if err := sdb.Close(); err != nil {
		t.Fatalf("close statedb: %v", err)
	}
	reopened, err := state.NewStateDB(stateDir)
	if err != nil {
		t.Fatalf("reopen statedb: %v", err)
	}
	t.Cleanup(func() { reopened.Close() })

	got, err := loadForkSchedule("", reopened, true, forkPolicy{})
	if err != nil {
		t.Fatalf("restart without --genesis: %v", err)
	}
	assertSchedule(t, got, 7)
}

// Once a chain has recorded its schedule, the --genesis file on disk is checked
// against it rather than trusted. A file that cannot be read used to yield a
// silent nil schedule; on a chain that IS scheduled that is a node about to
// run different rules from its peers.
func TestValidatorRestartWithCorruptGenesisFileIsRefused(t *testing.T) {
	sdb, ex := newBootstrapNode(t)
	path := saveGenesis(t, scheduledGenesis(t, 7))
	if _, err := applyLocalGenesis(path, sdb, ex, genesisPolicy{}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("corrupt file: %v", err)
	}
	for _, chainEmpty := range []bool{false, true} {
		if _, err := loadForkSchedule(path, sdb, chainEmpty, forkPolicy{}); err == nil {
			t.Fatalf("chainEmpty=%v: a corrupt --genesis on a scheduled chain booted", chainEmpty)
		}
	}

	// A missing file is the same fault.
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove file: %v", err)
	}
	if _, err := loadForkSchedule(path, sdb, false, forkPolicy{}); err == nil {
		t.Fatal("a missing --genesis on a scheduled chain booted")
	}
}

// A file whose schedule differs from the one the chain was started under is
// refused; the same document (and one with no schedule against a chain that
// recorded none) is accepted and the stored copy is what the node runs.
func TestGenesisFileMustMatchTheStoredSchedule(t *testing.T) {
	sdb, ex := newBootstrapNode(t)
	same := scheduledGenesis(t, 7)
	if _, err := applyLocalGenesis(saveGenesis(t, same), sdb, ex, genesisPolicy{}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	got, err := loadForkSchedule(saveGenesis(t, same), sdb, false, forkPolicy{})
	if err != nil {
		t.Fatalf("the document the chain was started from was refused: %v", err)
	}
	assertSchedule(t, got, 7)

	differing := scheduledGenesis(t, 8)
	_, err = loadForkSchedule(saveGenesis(t, differing), sdb, false, forkPolicy{})
	if err == nil {
		t.Fatal("a --genesis scheduling a different height booted on a chain started under another")
	}
	if !strings.Contains(err.Error(), "started under") {
		t.Fatalf("refusal does not say what the chain was started under: %v", err)
	}

	unscheduled := honestGenesis(t)
	if _, err := loadForkSchedule(saveGenesis(t, unscheduled), sdb, false, forkPolicy{}); err == nil {
		t.Fatal("a --genesis with no schedule booted on a chain started with one")
	}

	// And the mirror image: a chain started with no schedule accepts a file
	// with none (nil and {} are the same schedule) and refuses one with any.
	sdb2, ex2 := newBootstrapNode(t)
	if _, err := applyLocalGenesis(saveGenesis(t, unscheduled), sdb2, ex2, genesisPolicy{}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got, err := loadForkSchedule(saveGenesis(t, honestGenesis(t)), sdb2, false, forkPolicy{}); err != nil || len(got) != 0 {
		t.Fatalf("unscheduled chain with unscheduled file: (%v, %v), want empty schedule", got, err)
	}
	if _, err := loadForkSchedule(saveGenesis(t, same), sdb2, false, forkPolicy{}); err == nil {
		t.Fatal("a --genesis with a schedule booted on a chain started without one")
	}
}

// With nothing stored, the file decides — and only an empty chain may proceed
// past a file it cannot read, because the apply path is about to report the
// real error. A chain with blocks and no stored schedule predates persistence;
// booting it blind would be the old silent-nil behaviour.
func TestLoadForkScheduleWithoutAStoredMap(t *testing.T) {
	sdb, _ := newBootstrapNode(t)

	if got, err := loadForkSchedule("", sdb, true, forkPolicy{}); err != nil || got != nil {
		t.Fatalf("no file, empty chain: (%v, %v), want (nil, nil)", got, err)
	}
	if got, err := loadForkSchedule("", sdb, false, forkPolicy{}); err != nil || got != nil {
		t.Fatalf("no file, legacy chain: (%v, %v), want (nil, nil)", got, err)
	}

	unreadable := filepath.Join(t.TempDir(), "missing.json")
	if got, err := loadForkSchedule(unreadable, sdb, true, forkPolicy{}); err != nil || got != nil {
		t.Fatalf("unreadable file, empty chain: (%v, %v), want (nil, nil) — the apply path reports it", got, err)
	}
	if _, err := loadForkSchedule(unreadable, sdb, false, forkPolicy{}); err == nil {
		t.Fatal("unreadable --genesis on a non-empty chain with no stored schedule booted")
	}

	got, err := loadForkSchedule(saveGenesis(t, scheduledGenesis(t, 7)), sdb, false, forkPolicy{})
	if err != nil {
		t.Fatalf("readable file, nothing stored: %v", err)
	}
	assertSchedule(t, got, 7)
	if _, ok, _ := readStoredForkSchedule(sdb); ok {
		t.Fatal("reading a schedule must not persist it; only applying genesis does")
	}

	unknown := honestGenesis(t)
	unknown.Forks = forks.Schedule{"not_a_real_upgrade": 1}
	if _, err := loadForkSchedule(saveGenesis(t, unknown), sdb, true, forkPolicy{}); err == nil {
		t.Fatal("a file scheduling an unknown upgrade booted")
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func saveGenesis(t *testing.T, g *genesiscfg.Genesis) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "genesis.json")
	if err := g.Save(path); err != nil {
		t.Fatalf("save genesis: %v", err)
	}
	return path
}

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
