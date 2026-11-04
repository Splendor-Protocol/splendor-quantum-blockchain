package types

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
)

// headerJSONWithGenesisHash is the serialisation of fixedHeader once a
// genesis_hash is bound into it (32 bytes of 0xaa). It pins where the key sits
// in the preimage — after tx_count, before qc_root — because once
// genesis_hash_v1 activates on a chain that position is consensus: moving the
// field reorders the JSON and changes the hash of every bound block.
const headerJSONWithGenesisHash = `{"height":7,"timestamp":1700000000,"chain_id":"splendor-quantum-1","alg_id":"ML-DSA-65","prev_hash":[1,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0],"tx_merkle_root":[2,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0],"state_root":[3,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0],"validator_addr":[4,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0],"tx_count":9,"genesis_hash":[170,170,170,170,170,170,170,170,170,170,170,170,170,170,170,170,170,170,170,170,170,170,170,170,170,170,170,170,170,170,170,170]}`

func boundGenesis() [32]byte {
	var g [32]byte
	for i := range g {
		g[i] = 0xaa
	}
	return g
}

// An unset genesis hash must leave the header exactly as every live block was
// hashed: no key at all, not a key holding zeros.
func TestZeroGenesisHashIsOmittedFromTheHeader(t *testing.T) {
	got, err := json.Marshal(fixedHeader())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "genesis_hash") {
		t.Fatalf("an unset genesis_hash reached the header JSON: %s", got)
	}
	if string(got) != headerJSONBeforeQCRoot {
		t.Fatalf("header bytes changed:\n got: %s\nwant: %s", got, headerJSONBeforeQCRoot)
	}
}

// A bound genesis hash is part of the block's identity: it is covered by the
// full hash AND by the voted hash validators sign, and it sits at a fixed
// position in the preimage.
func TestBoundGenesisHashIsCoveredByBothHashes(t *testing.T) {
	legacy := &Block{Header: fixedHeader()}
	legacyFull, _ := legacy.ComputeHash()
	legacyVoted, _ := legacy.ComputeVotedHash()

	h := fixedHeader()
	h.GenesisHash = boundGenesis()
	got, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != headerJSONWithGenesisHash {
		t.Fatalf("bound header bytes changed:\n got: %s\nwant: %s", got, headerJSONWithGenesisHash)
	}

	bound := &Block{Header: h}
	full, _ := bound.ComputeHash()
	voted, _ := bound.ComputeVotedHash()
	if full == legacyFull || voted == legacyVoted {
		t.Fatal("a bound genesis_hash is not covered by the block hash")
	}
	if want := qcrypto.Hash32([]byte(headerJSONWithGenesisHash)); full != want || voted != want {
		t.Fatalf("hash is not SHA3-256 over the pinned bound header: full %x voted %x want %x", full, voted, want)
	}

	// With a certificate attached the full hash covers qc_root too, but the
	// voted identity is still the bound header without it.
	h.QCRoot = []byte{0x01}
	withQC := &Block{Header: h}
	votedQC, _ := withQC.ComputeVotedHash()
	if votedQC != voted {
		t.Fatal("qc_root leaked into the voted hash of a bound header")
	}
	raw, _ := json.Marshal(h)
	if i, j := bytes.Index(raw, []byte(`"genesis_hash"`)), bytes.Index(raw, []byte(`"qc_root"`)); i < 0 || j < 0 || i > j {
		t.Fatalf("genesis_hash must precede qc_root in the preimage: %s", raw)
	}
}

// A header serialised before the field existed decodes with no genesis hash
// and re-encodes to the same bytes, so a stored or peer-served legacy block
// round-trips through this binary unchanged.
func TestLegacyHeaderRoundTripsByteForByte(t *testing.T) {
	var h BlockHeader
	if err := json.Unmarshal([]byte(headerJSONBeforeQCRoot), &h); err != nil {
		t.Fatal(err)
	}
	if h.GenesisHash != ([32]byte{}) {
		t.Fatalf("legacy header decoded with a genesis hash: %x", h.GenesisHash)
	}
	again, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != headerJSONBeforeQCRoot {
		t.Fatalf("legacy header did not round-trip:\n got: %s\nwant: %s", again, headerJSONBeforeQCRoot)
	}
}

// The header #91 shipped hashed an explicit run of 32 zeros. No live chain
// holds such a block, and none may be accepted: a node on the previous binary
// ignores the unknown key and hashes the legacy bytes, so it refuses the block,
// and this binary must refuse it too or the two would disagree about validity.
func TestHeaderHashedOverExplicitZeroGenesisHashIsRefused(t *testing.T) {
	pr91 := strings.TrimSuffix(headerJSONBeforeQCRoot, "}") +
		`,"genesis_hash":[0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0]}`
	var h BlockHeader
	if err := json.Unmarshal([]byte(pr91), &h); err != nil {
		t.Fatal(err)
	}
	b := &Block{
		Header:             h,
		Hash:               qcrypto.Hash32([]byte(pr91)),
		ValidatorSignature: make([]byte, qcrypto.SignatureSize),
	}
	err := b.Validate()
	if err == nil || !strings.Contains(err.Error(), "block hash mismatch") {
		t.Fatalf("a block hashed over an explicit zero genesis_hash was not refused: %v", err)
	}
}

// previousBinaryHeader is BlockHeader as the release on every tier (v1.2.2)
// declares it: no GenesisHash. A node on that binary decodes a block into this
// shape — dropping keys it does not know — and hashes what is left, so this is
// how it judges a block sealed by this binary during a rolling upgrade.
type previousBinaryHeader struct {
	Height        uint64          `json:"height"`
	Timestamp     int64           `json:"timestamp"`
	ChainID       string          `json:"chain_id"`
	AlgID         string          `json:"alg_id"`
	PrevHash      [32]byte        `json:"prev_hash"`
	TxMerkleRoot  [32]byte        `json:"tx_merkle_root"`
	StateRoot     [32]byte        `json:"state_root"`
	ValidatorAddr qcrypto.Address `json:"validator_addr"`
	TxCount       uint32          `json:"tx_count"`
	QCRoot        []byte          `json:"qc_root,omitempty"`
}

func previousBinaryVotedHash(t *testing.T, wire []byte) [32]byte {
	t.Helper()
	var h previousBinaryHeader
	if err := json.Unmarshal(wire, &h); err != nil {
		t.Fatal(err)
	}
	h.QCRoot = nil
	raw, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	return qcrypto.Hash32(raw)
}

// Until genesis_hash_v1, a block this binary seals must hash identically on a
// node still running the previous release, or a mixed-version network splits
// on the first block. A bound header is exactly what the previous release
// cannot hash, which is why binding waits for the fork.
func TestPreviousBinaryAgreesOnUnboundHeadersOnly(t *testing.T) {
	h := fixedHeader()
	h.QCRoot = []byte{0x01} // every live block carries a certificate
	wire, _ := json.Marshal(h)
	voted, _ := (&Block{Header: h}).ComputeVotedHash()
	if previousBinaryVotedHash(t, wire) != voted {
		t.Fatal("a node on the previous binary computes a different hash for an unbound header this binary seals")
	}

	h.GenesisHash = boundGenesis()
	wire, _ = json.Marshal(h)
	voted, _ = (&Block{Header: h}).ComputeVotedHash()
	if previousBinaryVotedHash(t, wire) == voted {
		t.Fatal("the previous binary agrees on a bound header; the fork gate would be unnecessary")
	}
}

// liveGenesisHashes are the block 0 hashes the three public networks report in
// /status — the values operators pin with --genesis-hash. A header change that
// moves any of them strands every node pinned to it.
var liveGenesisHashes = map[string]string{
	"splendor-quantum-devnet-1":  "7eebab40716e1f884a331b8902ee99d447ff4f98c8479d56a7e0f1a94c7ac04c",
	"splendor-quantum-testnet-1": "324d7f561493ae6d08062288cb99adfbb3f83297d352b783f611171251a1289f",
	"splendor-quantum-1":         "d865193d3bd88fb4446626940e63b5658275342a0153638079cf9e962332a257",
}

// Real blocks, fetched from the public devnet, testnet and mainnet RPCs on
// 2026-10-30 (block 0 and the head of each), must still validate: hash, voted
// hash, qc_root against the votes, merkle root, and the producer's ML-DSA-65
// signature over the stored hash. Every one of them failed with "block hash
// mismatch" under #91.
func TestRealChainBlocksStillValidate(t *testing.T) {
	names, err := filepath.Glob(filepath.Join("testdata", "compat", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(names) < 6 {
		t.Fatalf("expected the six compat fixtures, found %d", len(names))
	}
	for _, name := range names {
		t.Run(filepath.Base(name), func(t *testing.T) {
			raw, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			var b Block
			if err := json.Unmarshal(raw, &b); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if b.Header.GenesisHash != ([32]byte{}) {
				t.Fatal("a live block carries genesis_hash; these fixtures predate the field")
			}
			if err := b.Validate(); err != nil {
				t.Fatalf("height %d no longer validates: %v", b.Header.Height, err)
			}
			if err := b.VerifySignature(b.ValidatorPubKey); err != nil {
				t.Fatalf("height %d: producer signature over the stored hash: %v", b.Header.Height, err)
			}
			// The certificate's votes sign the block hash too; on these
			// single-validator tiers the producer is the voter.
			for _, v := range b.Votes {
				if err := v.Verify(b.ValidatorPubKey); err != nil {
					t.Fatalf("height %d: vote from %s: %v", b.Header.Height, v.ValidatorAddr, err)
				}
			}
			if b.Header.Height == 0 {
				h, err := b.ComputeHash()
				if err != nil {
					t.Fatal(err)
				}
				want, ok := liveGenesisHashes[b.Header.ChainID]
				if !ok {
					t.Fatalf("no published genesis hash for chain %q", b.Header.ChainID)
				}
				if got := hex.EncodeToString(h[:]); got != want {
					t.Fatalf("%s block 0 hashes to %s, published --genesis-hash is %s", b.Header.ChainID, got, want)
				}
			}
		})
	}
}
