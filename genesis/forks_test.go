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
		TotalSupply: "100",
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
