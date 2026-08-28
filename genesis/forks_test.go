package genesis

import (
	"strings"
	"testing"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
)

func baseGenesis() *Genesis {
	return &Genesis{
		ChainID:            "splendor-quantum-1",
		NetworkName:        "test",
		Timestamp:          1700000000,
		ValidatorPubKeyHex: "aabbcc",
		Allocations: []Allocation{
			{Address: "SQ:1111111111111111111111111111111111111111111111111111111111111111", Balance: "100"},
		},
		// Derived by Validate; a present value is refused unless it matches.
		TotalSupply: "",
	}
}

// The pins operators have already deployed were computed before the fork
// schedule existed. A chain that does not use one has to keep hashing to
// exactly what it hashed to before, or every --genesis-doc-hash in the field
// stops verifying the moment this build ships.
func TestEmptyForkScheduleDoesNotChangeGenesisHash(t *testing.T) {
	without := baseGenesis()
	withEmpty := baseGenesis()
	withEmpty.Forks = forks.Schedule{}
	withNil := baseGenesis()
	withNil.Forks = nil

	want := without.HashHex()
	if got := withEmpty.HashHex(); got != want {
		t.Fatalf("an empty schedule changed the genesis hash: %s != %s", got, want)
	}
	if got := withNil.HashHex(); got != want {
		t.Fatalf("a nil schedule changed the genesis hash: %s != %s", got, want)
	}
}

// A schedule decides which rules the chain runs, so two documents differing
// only in an activation height describe networks that will disagree. They must
// not share an identity.
func TestForkScheduleIsPartOfGenesisIdentity(t *testing.T) {
	plain := baseGenesis()
	scheduled := baseGenesis()
	scheduled.Forks = forks.Schedule{forks.MultiValidator: 1_000_000}
	later := baseGenesis()
	later.Forks = forks.Schedule{forks.MultiValidator: 2_000_000}

	if scheduled.HashHex() == plain.HashHex() {
		t.Fatal("a scheduled fork must change the genesis hash")
	}
	if scheduled.HashHex() == later.HashHex() {
		t.Fatal("two different activation heights must not share a genesis hash")
	}
}

// A map has no iteration order, so an identity derived from one directly would
// differ between two runs over the same file.
func TestGenesisHashStableAcrossMapOrder(t *testing.T) {
	g := baseGenesis()
	g.Forks = forks.Schedule{forks.MultiValidator: 1_000_000}
	first := g.HashHex()
	for i := 0; i < 200; i++ {
		if got := g.HashHex(); got != first {
			t.Fatalf("genesis hash is not stable across runs: %s != %s", got, first)
		}
	}
}

// A typo in a fork name is otherwise invisible: the node reports a healthy
// schedule, the rule never fires, and nobody notices until the height passes.
func TestUnknownForkNameRejected(t *testing.T) {
	g := baseGenesis()
	g.Forks = forks.Schedule{"multivalidator": 100}
	err := g.Validate()
	if err == nil {
		t.Fatal("an unknown fork name must be refused, not ignored")
	}
	if !strings.Contains(err.Error(), "unknown upgrade") {
		t.Fatalf("want an unknown-upgrade error, got %v", err)
	}
}

func TestKnownForkNameAccepted(t *testing.T) {
	g := baseGenesis()
	g.Forks = forks.Schedule{forks.MultiValidator: 100}
	if err := g.Validate(); err != nil {
		t.Fatalf("a known fork name should validate: %v", err)
	}
}

// One row per upgrade name a tier document may schedule, and whether THIS
// build honours it. A document must never schedule a name the deployed binary
// does not implement — forks.Known is a promise, not a stub — so each row flips
// to accepted only in the change that lands the gate, and a document scheduling
// it before then is refused at load.
func TestScheduledUpgradeNamesAgainstThisBuild(t *testing.T) {
	for _, tc := range []struct {
		name  string
		known bool
	}{
		{forks.MultiValidator, true},
		{forks.ModuleRevertV1, true}, // PR-3
		{forks.QRC20V2, true},        // PR-4
		{forks.QRC1155V1, true},      // PR-5
		{forks.ExchangeV1, true},     // PR-6
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := baseGenesis()
			g.Forks = forks.Schedule{tc.name: 1}
			if tc.name == forks.ExchangeV1 {
				// Its own dependency rule; what is on trial here is the name.
				g.Forks[forks.ModuleRevertV1] = 1
			}
			err := g.Validate()
			switch {
			case tc.known && err != nil:
				t.Fatalf("%s is implemented by this build but was refused: %v", tc.name, err)
			case !tc.known && err == nil:
				t.Fatalf("%s is not implemented by this build but a document scheduling it was accepted", tc.name)
			case !tc.known && !strings.Contains(err.Error(), "unknown upgrade"):
				t.Fatalf("want an unknown-upgrade refusal for %s, got %v", tc.name, err)
			}
		})
	}
}

// exchange_v1 depends on module_revert_v1 being in force no later than
// itself: a failed match must be a failed receipt, never a dropped tx that
// jams the operator's nonce. Now that both names are honoured the rule is
// reachable from a genesis document, and a document that gets it wrong is
// refused at load like an unknown name — never honoured in part.
func TestGenesisRefusesExchangeWithoutModuleRevert(t *testing.T) {
	for name, tc := range map[string]struct {
		forks forks.Schedule
		ok    bool
	}{
		"both at 1":             {forks.Schedule{forks.ExchangeV1: 1, forks.ModuleRevertV1: 1}, true},
		"revert earlier":        {forks.Schedule{forks.ExchangeV1: 10, forks.ModuleRevertV1: 5}, true},
		"exchange alone":        {forks.Schedule{forks.ExchangeV1: 1}, false},
		"revert never":          {forks.Schedule{forks.ExchangeV1: 1, forks.ModuleRevertV1: 0}, false},
		"revert one block late": {forks.Schedule{forks.ExchangeV1: 5, forks.ModuleRevertV1: 6}, false},
	} {
		t.Run(name, func(t *testing.T) {
			g := baseGenesis()
			g.Forks = tc.forks
			err := g.Validate()
			if tc.ok && err != nil {
				t.Fatalf("want accepted, got %v", err)
			}
			if !tc.ok {
				if err == nil {
					t.Fatal("want refused, got nil")
				}
				if !strings.Contains(err.Error(), forks.ModuleRevertV1) {
					t.Fatalf("the refusal must name the missing dependency, got %v", err)
				}
			}
		})
	}
}

// The document hash a tier pins is computed over the schedule, so the tier
// documents this plan describes — all four names at height 1, mainnet without
// the exchange — get distinct pins. The hash is computable regardless (Hash
// does not validate) and stable.
func TestTierScheduleShapesHashDistinctly(t *testing.T) {
	devnet := baseGenesis()
	devnet.Forks = forks.Schedule{
		forks.ModuleRevertV1: 1, forks.QRC20V2: 1, forks.QRC1155V1: 1, forks.ExchangeV1: 1,
	}
	mainnet := baseGenesis()
	mainnet.Forks = forks.Schedule{
		forks.ModuleRevertV1: 1, forks.QRC20V2: 1, forks.QRC1155V1: 1,
	}
	if devnet.HashHex() == mainnet.HashHex() {
		t.Fatal("omitting exchange_v1 must change the document pin")
	}
	if devnet.HashHex() == baseGenesis().HashHex() {
		t.Fatal("a scheduled document must not hash like an unscheduled one")
	}
}
