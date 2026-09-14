package objects

import (
	"math/rand"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
)

// Every symbol's proof must verify against the codebook's own root, at
// every generation size (including 1, and sizes that are and aren't powers
// of two — the carry-up-odd-node rule must hold at every level).
func TestCodebookEveryProofVerifies(t *testing.T) {
	descID := qcrypto.Hash32([]byte("test-descriptor"))
	for _, n := range []int{1, 2, 3, 5, 8, 17, 32, 65} {
		rng := rand.New(rand.NewSource(int64(n)))
		symbols := genSymbols(rng, n)
		cb, err := BuildCodebook(descID, symbols)
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		root := cb.Root()
		for i := 0; i < n; i++ {
			proof, err := cb.BuildProof(i)
			if err != nil {
				t.Fatalf("n=%d proof %d: %v", n, i, err)
			}
			if !VerifySymbolProof(root, descID, proof) {
				t.Fatalf("n=%d: proof for symbol %d did not verify", n, i)
			}
		}
	}
}

// A tampered payload, tampered coefficients, or a proof carried under the
// wrong descriptor ID must all fail verification — these are exactly the
// "corrupt payload/proof/coefficients" and cross-manifest replay scenarios
// the plan's benchmark gate calls out.
func TestCodebookProofRejectsTampering(t *testing.T) {
	descID := qcrypto.Hash32([]byte("test-descriptor"))
	rng := rand.New(rand.NewSource(99))
	symbols := genSymbols(rng, 16)
	cb, err := BuildCodebook(descID, symbols)
	if err != nil {
		t.Fatal(err)
	}
	root := cb.Root()

	proof, err := cb.BuildProof(3)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifySymbolProof(root, descID, proof) {
		t.Fatal("baseline proof should verify")
	}

	tamperedPayload := proof
	tamperedPayload.Symbol.Payload = append([]byte(nil), proof.Symbol.Payload...)
	tamperedPayload.Symbol.Payload[0] ^= 0xff
	if VerifySymbolProof(root, descID, tamperedPayload) {
		t.Fatal("proof verified after payload was tampered")
	}

	tamperedCoeffs := proof
	tamperedCoeffs.Symbol.Coefficients = append([]byte(nil), proof.Symbol.Coefficients...)
	tamperedCoeffs.Symbol.Coefficients[0] ^= 0xff
	if VerifySymbolProof(root, descID, tamperedCoeffs) {
		t.Fatal("proof verified after coefficients were tampered")
	}

	tamperedIndex := proof
	tamperedIndex.Symbol.Index = proof.Symbol.Index + 1
	if VerifySymbolProof(root, descID, tamperedIndex) {
		t.Fatal("proof verified after symbol index was changed")
	}

	wrongDescID := qcrypto.Hash32([]byte("a different descriptor"))
	if VerifySymbolProof(root, wrongDescID, proof) {
		t.Fatal("proof verified under a different descriptor ID (cross-manifest replay)")
	}

	tamperedSibling := proof
	if len(tamperedSibling.Siblings) > 0 {
		tamperedSibling.Siblings = append([][32]byte(nil), proof.Siblings...)
		tamperedSibling.Siblings[0][0] ^= 0xff
		if VerifySymbolProof(root, descID, tamperedSibling) {
			t.Fatal("proof verified after a sibling hash was tampered")
		}
	}
}

// A proof built against one codebook must not verify against a different
// codebook's root, even if both happen to hold the same number of symbols.
func TestCodebookProofDoesNotCrossRoots(t *testing.T) {
	descID := qcrypto.Hash32([]byte("shared-descriptor"))
	rngA := rand.New(rand.NewSource(1))
	rngB := rand.New(rand.NewSource(2))
	cbA, err := BuildCodebook(descID, genSymbols(rngA, 8))
	if err != nil {
		t.Fatal(err)
	}
	cbB, err := BuildCodebook(descID, genSymbols(rngB, 8))
	if err != nil {
		t.Fatal(err)
	}
	proof, err := cbA.BuildProof(0)
	if err != nil {
		t.Fatal(err)
	}
	if VerifySymbolProof(cbB.Root(), descID, proof) {
		t.Fatal("a symbol from codebook A verified against codebook B's root")
	}
}

func TestCodebookBuildProofRejectsOutOfRangeIndex(t *testing.T) {
	descID := qcrypto.Hash32([]byte("d"))
	rng := rand.New(rand.NewSource(5))
	cb, err := BuildCodebook(descID, genSymbols(rng, 4))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cb.BuildProof(-1); err == nil {
		t.Fatal("negative index accepted")
	}
	if _, err := cb.BuildProof(4); err == nil {
		t.Fatal("out-of-range index accepted")
	}
}

func TestBuildCodebookRejectsEmptySymbolSet(t *testing.T) {
	descID := qcrypto.Hash32([]byte("d"))
	if _, err := BuildCodebook(descID, nil); err == nil {
		t.Fatal("empty symbol set accepted")
	}
}
