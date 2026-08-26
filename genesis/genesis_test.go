package genesis

import (
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// liveTierGenesis builds a document shaped exactly like the one the deploy
// entrypoint writes for devnet/testnet/mainnet: a self-genesis validator
// treasury plus a configured faucet allocation.
func liveTierGenesis(t *testing.T) *Genesis {
	t.Helper()
	pub, _, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	return &Genesis{
		ChainID:            types.ChainID,
		NetworkName:        "Splendor Quantum",
		Timestamp:          1786618272,
		ValidatorPubKeyHex: hex.EncodeToString(pub.Bytes()),
		Allocations: []Allocation{
			{
				Address: qcrypto.DeriveAddress(pub).String(),
				Balance: "1000000000000000000000000000",
				Comment: "Genesis validator treasury",
			},
			{
				Address: "SQ:0000000000000000000000000000000000000000000000000000000000000001",
				Balance: "1000000000000000000000000000",
				Comment: "Configured genesis allocation",
			},
		},
		// The sum of the two allocations above. The deployed documents declare
		// only the validator's half, which is the defect TestLiveTierGenesis-
		// SupplyMustMatchAllocations pins down.
		TotalSupply: "2000000000000000000000000000",
	}
}

// The pin an operator computes from a file has to survive that file being
// re-serialised — re-indented, saved, reloaded — or it is not an identity, it is
// a checksum of one particular byte layout.
func TestHashSurvivesJSONRoundTrip(t *testing.T) {
	g := liveTierGenesis(t)
	want := g.Hash()

	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"compact", mustMarshal(t, g, false)},
		{"indented", mustMarshal(t, g, true)},
	} {
		var back Genesis
		if err := json.Unmarshal(tc.data, &back); err != nil {
			t.Fatalf("%s: unmarshal: %v", tc.name, err)
		}
		if got := back.Hash(); got != want {
			t.Fatalf("%s: hash changed across round trip: %x != %x", tc.name, got[:8], want[:8])
		}
	}

	path := filepath.Join(t.TempDir(), "genesis.json")
	if err := g.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := LoadFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := loaded.Hash(); got != want {
		t.Fatalf("hash changed across Save/LoadFile: %x != %x", got[:8], want[:8])
	}
	if loaded.HashHex() != hex.EncodeToString(want[:]) {
		t.Fatalf("HashHex %q does not match Hash %x", loaded.HashHex(), want)
	}
}

// Every field that decides what the chain starts as must move the hash —
// otherwise a peer could hand over a document that differs where it matters and
// still satisfy the pin.
func TestHashCoversEveryConsensusField(t *testing.T) {
	base := liveTierGenesis(t)
	want := base.Hash()

	other, _, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}

	mutations := map[string]func(*Genesis){
		"chain id":      func(g *Genesis) { g.ChainID = "splendor-quantum-2" },
		"network name":  func(g *Genesis) { g.NetworkName = "Not Splendor" },
		"timestamp":     func(g *Genesis) { g.Timestamp++ },
		"validator key": func(g *Genesis) { g.ValidatorPubKeyHex = hex.EncodeToString(other.Bytes()) },
		"alloc address": func(g *Genesis) { g.Allocations[1].Address = "SQ:" + hex32("ff") },
		"alloc balance": func(g *Genesis) { g.Allocations[1].Balance = "2000000000000000000000000000" },
		"extra alloc": func(g *Genesis) {
			g.Allocations = append(g.Allocations, Allocation{Address: "SQ:" + hex32("ab"), Balance: "1"})
		},
		"dropped alloc":    func(g *Genesis) { g.Allocations = g.Allocations[:1] },
		"reordered allocs": func(g *Genesis) { g.Allocations[0], g.Allocations[1] = g.Allocations[1], g.Allocations[0] },
		"total supply":     func(g *Genesis) { g.TotalSupply = "26000000000000000000000000000" },
		"balance zero-pad": func(g *Genesis) { g.Allocations[1].Balance = "0" + g.Allocations[1].Balance },
	}

	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			g := cloneGenesis(t, base)
			mutate(g)
			if got := g.Hash(); got == want {
				t.Fatalf("changing the %s did not change the genesis hash", name)
			}
		})
	}
}

// A comment is an operator label with no consensus weight. Pinning it would mean
// a typo fix invalidates every deployed pin.
func TestHashIgnoresComments(t *testing.T) {
	base := liveTierGenesis(t)
	g := cloneGenesis(t, base)
	g.Allocations[0].Comment = "reworded label"
	g.Allocations[1].Comment = ""
	if g.Hash() != base.Hash() {
		t.Fatal("comment text changed the genesis hash")
	}
}

// Hex decodes case-insensitively, so an upper-case document describes the exact
// same chain and must not get a different identity.
func TestHashNormalizesHexCase(t *testing.T) {
	base := liveTierGenesis(t)
	g := cloneGenesis(t, base)
	g.ValidatorPubKeyHex = upper(g.ValidatorPubKeyHex)
	g.Allocations[0].Address = "SQ:" + upper(g.Allocations[0].Address[3:])
	if g.Hash() != base.Hash() {
		t.Fatal("upper-case hex produced a different genesis hash")
	}
}

// Length prefixes are what stop two different documents from sharing a preimage
// by moving bytes across a field boundary.
func TestHashFieldsAreLengthDelimited(t *testing.T) {
	a := &Genesis{ChainID: "ab", NetworkName: "c"}
	b := &Genesis{ChainID: "a", NetworkName: "bc"}
	if a.Hash() == b.Hash() {
		t.Fatal("field boundary is ambiguous: 'ab'+'c' hashes the same as 'a'+'bc'")
	}
}

// The three live tiers run on a document of exactly this shape. If it stopped
// validating, the redeploy path that regenerates it would stop too.
func TestLiveTierGenesisValidates(t *testing.T) {
	if err := liveTierGenesis(t).Validate(); err != nil {
		t.Fatalf("the deployed genesis shape no longer validates: %v", err)
	}
}

func cloneGenesis(t *testing.T, g *Genesis) *Genesis {
	t.Helper()
	var out Genesis
	if err := json.Unmarshal(mustMarshal(t, g, false), &out); err != nil {
		t.Fatalf("clone: %v", err)
	}
	return &out
}

func mustMarshal(t *testing.T, g *Genesis, indent bool) []byte {
	t.Helper()
	var (
		data []byte
		err  error
	)
	if indent {
		data, err = json.MarshalIndent(g, "", "    ")
	} else {
		data, err = json.Marshal(g)
	}
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

// hex32 returns a 32-byte address body of a repeated byte.
func hex32(b string) string {
	out := ""
	for i := 0; i < 32; i++ {
		out += b
	}
	return out
}

func upper(s string) string {
	out := []rune(s)
	for i, r := range out {
		if r >= 'a' && r <= 'f' {
			out[i] = r - 32
		}
	}
	return string(out)
}

// A pin is handed between humans and pasted into a systemd unit, while the
// genesis.json it names is copied, reformatted and re-serialised by whatever
// tool touches it last. So the identity has to survive the file being rewritten
// with the keys in a different order and the whitespace shuffled — anything less
// is a checksum of one byte layout, and operators would find it moving under
// them and stop trusting it. These are raw documents, not Go structs marshalled
// twice, because encoding/json always emits fields in struct order and so cannot
// exercise this at all.
func TestHashIgnoresJSONKeyOrderAndWhitespace(t *testing.T) {
	const canonical = `{
  "chain_id": "splendor-quantum-1",
  "network_name": "Splendor Quantum",
  "timestamp": 1786618272,
  "validator_pubkey_hex": "aabbccdd",
  "allocations": [
    { "address": "SQ:` + allocAddrBody + `", "balance": "1000", "comment": "treasury" }
  ],
  "total_supply": "1000"
}`

	// Same document: keys reversed, comment reworded, hex upper-cased, all
	// whitespace collapsed.
	const shuffled = `{"total_supply":"1000","allocations":[{"comment":"Treasury (reworded)",` +
		`"balance":"1000","address":"SQ:` + allocAddrBodyUpper + `"}],` +
		`"validator_pubkey_hex":"AABBCCDD","timestamp":1786618272,` +
		`"network_name":"Splendor Quantum","chain_id":"splendor-quantum-1"}`

	// And one that differs only where it counts.
	const doctored = `{"chain_id":"splendor-quantum-1","network_name":"Splendor Quantum",` +
		`"timestamp":1786618272,"validator_pubkey_hex":"aabbccdd","allocations":[` +
		`{"address":"SQ:` + allocAddrBody + `","balance":"1001"}],"total_supply":"1000"}`

	want := parseGenesis(t, canonical).Hash()
	if got := parseGenesis(t, shuffled).Hash(); got != want {
		t.Fatalf("key order/whitespace moved the hash: %x != %x", got[:8], want[:8])
	}
	if got := parseGenesis(t, doctored).Hash(); got == want {
		t.Fatalf("one unit of extra balance did not move the hash: %x", got[:8])
	}
}

const (
	allocAddrBody      = "0000000000000000000000000000000000000000000000000000000000ffabcd"
	allocAddrBodyUpper = "0000000000000000000000000000000000000000000000000000000000FFABCD"
)

func parseGenesis(t *testing.T, doc string) *Genesis {
	t.Helper()
	var g Genesis
	if err := json.Unmarshal([]byte(doc), &g); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return &g
}

// The shape every live tier actually deployed: total_supply naming only the
// validator treasury while a second allocation mints just as much again. It was
// accepted for as long as nothing checked, and mainnet has been running at
// 27e27 against a declared 26e27 ever since.
func TestLiveTierGenesisSupplyMustMatchAllocations(t *testing.T) {
	g := liveTierGenesis(t)
	g.TotalSupply = "1000000000000000000000000000" // as deployed: half the truth

	err := g.Validate()
	if err == nil {
		t.Fatal("a genesis that mints more than it declares must be refused")
	}
	if !strings.Contains(err.Error(), "more than it declares") {
		t.Fatalf("error should say what is wrong, got: %v", err)
	}
}

// The deploy entrypoint writes an empty total_supply on purpose: the node owns
// the arithmetic, because the alpine image has no python, no bc, and no integer
// wide enough to add 1e27 twice.
func TestEmptyTotalSupplyIsDerivedFromAllocations(t *testing.T) {
	g := liveTierGenesis(t)
	g.TotalSupply = ""

	if err := g.Validate(); err != nil {
		t.Fatalf("an omitted total_supply should be computed, not rejected: %v", err)
	}
	if g.TotalSupply != "2000000000000000000000000000" {
		t.Fatalf("total_supply = %q, want the sum of the allocations", g.TotalSupply)
	}
}

// The derived value is hashed into the genesis identity, so two nodes filling it
// in must land on the same document. Deriving then hashing must equal declaring
// the correct value up front.
func TestDerivedSupplyHashesLikeADeclaredOne(t *testing.T) {
	derived := liveTierGenesis(t)
	declared := liveTierGenesis(t)
	declared.ValidatorPubKeyHex = derived.ValidatorPubKeyHex
	declared.Allocations = derived.Allocations
	derived.TotalSupply = ""

	if err := derived.Validate(); err != nil {
		t.Fatalf("derive: %v", err)
	}
	if err := declared.Validate(); err != nil {
		t.Fatalf("declare: %v", err)
	}
	if derived.Hash() != declared.Hash() {
		t.Fatal("an omitted total_supply hashes differently from the same value written out, " +
			"so a --genesis-doc-hash pin would not match across the two")
	}
}
