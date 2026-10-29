package types

import (
	"encoding/json"
	"strings"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
)

// headerJSONBeforeQCRoot is the exact serialisation the header produced before
// QCRoot existed, for a header with every other field at a fixed value.
//
// It is pinned as a literal rather than recomputed, because a test that derives
// the expected bytes from the same struct it is checking would agree with any
// change, including the one this exists to catch.
const headerJSONBeforeQCRoot = `{"height":7,"timestamp":1700000000,"chain_id":"splendor-quantum-1","alg_id":"ML-DSA-65","prev_hash":[1,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0],"tx_merkle_root":[2,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0],"state_root":[3,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0],"validator_addr":[4,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0],"tx_count":9,"genesis_hash":[0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0]}`

func fixedHeader() BlockHeader {
	var prev, txroot, state [32]byte
	var addr qcrypto.Address
	prev[0], txroot[0], state[0], addr[0] = 1, 2, 3, 4
	return BlockHeader{
		Height:        7,
		Timestamp:     1700000000,
		ChainID:       "splendor-quantum-1",
		AlgID:         qcrypto.AlgMLDSA65,
		PrevHash:      prev,
		TxMerkleRoot:  txroot,
		StateRoot:     state,
		ValidatorAddr: addr,
		TxCount:       9,
	}
}

// The whole point of reserving QCRoot now is that reserving it costs nothing.
// If a nil QCRoot ever starts appearing in the JSON, every block hash on every
// live chain changes and the field can only be introduced by resetting the
// chain — which is the outcome this field exists to avoid.
func TestNilQCRootDoesNotChangeHeaderSerialisation(t *testing.T) {
	got, err := json.Marshal(fixedHeader())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(got) != headerJSONBeforeQCRoot {
		t.Fatalf("header serialisation changed while QCRoot is nil.\n got: %s\nwant: %s",
			got, headerJSONBeforeQCRoot)
	}
	if strings.Contains(string(got), "qc_root") {
		t.Fatal("qc_root must not appear in the JSON while it is nil")
	}
}

func TestNilQCRootDoesNotChangeBlockHash(t *testing.T) {
	b := &Block{Header: fixedHeader()}
	withField, err := b.ComputeHash()
	if err != nil {
		t.Fatalf("compute: %v", err)
	}

	// Hash the pinned pre-QCRoot bytes the same way ComputeHash does.
	before := qcrypto.Hash32([]byte(headerJSONBeforeQCRoot))
	if withField != before {
		t.Fatalf("block hash changed by adding a nil QCRoot:\n got %x\nwant %x", withField, before)
	}
}

func TestPopulatedQCRootChangesHashAndNeedsVotes(t *testing.T) {
	h := fixedHeader()
	h.QCRoot = []byte{0xaa, 0xbb}
	b := &Block{Header: h}
	hash, err := b.ComputeHash()
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if hash == qcrypto.Hash32([]byte(headerJSONBeforeQCRoot)) {
		t.Fatal("a populated QCRoot must be covered by ComputeHash")
	}

	// The voted hash ignores QCRoot; Validate uses that identity and refuses
	// a qc_root that is not backed by votes.
	voted, err := b.ComputeVotedHash()
	if err != nil {
		t.Fatal(err)
	}
	b.Hash = voted
	b.ValidatorSignature = make([]byte, qcrypto.SignatureSize)
	err = b.Validate()
	if err == nil {
		t.Fatal("a qc_root without votes must be rejected")
	}
	if !strings.Contains(err.Error(), "qc_root") {
		t.Fatalf("want a qc_root refusal, got %v", err)
	}
}
