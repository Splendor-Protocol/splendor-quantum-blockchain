package consensus

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
)

func testVoter(t *testing.T) (qcrypto.Address, []byte, *qcrypto.PrivateKey) {
	t.Helper()
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	return qcrypto.DeriveAddress(pub), pub.Bytes(), priv
}

func signedVote(t *testing.T, height uint64, round uint32, blockHash [32]byte) (*Vote, []byte) {
	t.Helper()
	addr, pubBytes, priv := testVoter(t)
	v := &Vote{
		Height:        height,
		Round:         round,
		BlockHash:     blockHash,
		ValidatorAddr: addr,
		Timestamp:     1_700_000_000,
	}
	if err := v.Sign(priv); err != nil {
		t.Fatalf("sign vote: %v", err)
	}
	return v, pubBytes
}

// A vote must sign and verify, and must record the algorithm it used.
func TestVoteRoundTrip(t *testing.T) {
	v, pub := signedVote(t, 10, 0, [32]byte{1})

	if v.AlgID != qcrypto.AlgMLDSA65 {
		t.Fatalf("vote alg_id = %q, want %q", v.AlgID, qcrypto.AlgMLDSA65)
	}
	if len(v.Signature) != qcrypto.SignatureSize {
		t.Fatalf("signature is %d bytes, want %d", len(v.Signature), qcrypto.SignatureSize)
	}
	if err := v.Verify(pub); err != nil {
		t.Fatalf("freshly signed vote did not verify: %v", err)
	}
}

// Every signed field must be covered by the signature.
func TestVoteRejectsTamperedFields(t *testing.T) {
	for _, c := range []struct {
		name   string
		mutate func(*Vote)
	}{
		{"height", func(v *Vote) { v.Height++ }},
		{"round", func(v *Vote) { v.Round++ }},
		{"block hash", func(v *Vote) { v.BlockHash[0] ^= 0xFF }},
		{"timestamp", func(v *Vote) { v.Timestamp++ }},
		{"alg id", func(v *Vote) { v.AlgID = "Ed25519" }},
		{"signature", func(v *Vote) { v.Signature[0] ^= 0xFF }},
	} {
		t.Run(c.name, func(t *testing.T) {
			v, pub := signedVote(t, 10, 0, [32]byte{1})
			c.mutate(v)
			if err := v.Verify(pub); err == nil {
				t.Fatalf("a vote with a tampered %s was accepted", c.name)
			}
		})
	}
}

// A vote must not verify under a key belonging to a different validator, even
// if that signature is otherwise perfectly valid.
func TestVoteCannotBeAttributedToAnotherValidator(t *testing.T) {
	v, _ := signedVote(t, 10, 0, [32]byte{1})
	_, otherPub, _ := testVoter(t)

	if err := v.Verify(otherPub); err == nil {
		t.Fatal("a vote verified against an unrelated validator's key")
	}
}

// A vote signature must not be reusable as a block or transaction signature.
// This is the property the domain-separation contexts exist to provide.
func TestVoteSignatureIsDomainSeparated(t *testing.T) {
	v, pub := signedVote(t, 10, 0, [32]byte{1})
	hash, err := v.Hash()
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	pk, err := qcrypto.PublicKeyFromBytes(pub)
	if err != nil {
		t.Fatalf("pubkey: %v", err)
	}

	for name, ctx := range map[string][]byte{
		"transaction": qcrypto.CtxTransaction,
		"block":       qcrypto.CtxBlock,
		"x402":        qcrypto.CtxX402,
		"message":     qcrypto.CtxMessage,
	} {
		if qcrypto.Verify(pk, hash[:], ctx, v.Signature) {
			t.Errorf("SECURITY: a consensus vote verified as a %s signature", name)
		}
	}
	if !qcrypto.Verify(pk, hash[:], qcrypto.CtxVote, v.Signature) {
		t.Error("vote does not verify under its own context")
	}
}

// The quorum threshold must be 2/3+1 of the validator set.
func TestQuorumThreshold(t *testing.T) {
	for _, c := range []struct{ validators, want int }{
		{1, 1}, {3, 3}, {4, 3}, {7, 5}, {10, 7}, {100, 67},
	} {
		vs := NewVoteSet(1, 0, [32]byte{}, c.validators)
		if vs.requiredWeight != c.want {
			t.Errorf("%d validators: quorum = %d, want %d",
				c.validators, vs.requiredWeight, c.want)
		}
	}
}

// A vote set must only accept votes for its own height, round, and block, and
// must count each validator once.
func TestVoteSetRejectsMismatchedAndDuplicateVotes(t *testing.T) {
	hash := [32]byte{7}
	vs := NewVoteSet(10, 2, hash, 4)

	good, _ := signedVote(t, 10, 2, hash)
	if _, err := vs.AddVote(good); err != nil {
		t.Fatalf("valid vote rejected: %v", err)
	}
	if _, err := vs.AddVote(good); err == nil {
		t.Fatal("the same validator voted twice and both were counted")
	}

	wrongHeight, _ := signedVote(t, 11, 2, hash)
	if _, err := vs.AddVote(wrongHeight); err == nil {
		t.Fatal("a vote for another height was counted")
	}
	wrongRound, _ := signedVote(t, 10, 3, hash)
	if _, err := vs.AddVote(wrongRound); err == nil {
		t.Fatal("a vote from another round was counted")
	}
	wrongBlock, _ := signedVote(t, 10, 2, [32]byte{9})
	if _, err := vs.AddVote(wrongBlock); err == nil {
		t.Fatal("a vote for a different block was counted")
	}

	if vs.VoteCount() != 1 {
		t.Fatalf("vote count = %d, want 1", vs.VoteCount())
	}
}

// Quorum must be reported exactly once, on the vote that reaches it.
func TestQuorumReportedOnce(t *testing.T) {
	hash := [32]byte{3}
	vs := NewVoteSet(5, 0, hash, 4) // needs 3

	reached := 0
	for i := 0; i < 4; i++ {
		v, _ := signedVote(t, 5, 0, hash)
		hit, err := vs.AddVote(v)
		if err != nil {
			t.Fatalf("vote %d rejected: %v", i, err)
		}
		if hit {
			reached++
			if vs.VoteCount() != 3 {
				t.Errorf("quorum announced at %d votes, want 3", vs.VoteCount())
			}
		}
	}
	if reached != 1 {
		t.Fatalf("quorum reported %d times, want exactly 1", reached)
	}
	if !vs.HasQuorum() {
		t.Fatal("quorum not held after enough votes")
	}
}

// Consensus must not gain a classical signature scheme. Speed is not a reason
// to make the chain forgeable by a quantum adversary, so this is enforced
// rather than left to review.
func TestConsensusImportsNoClassicalCrypto(t *testing.T) {
	banned := []string{
		"crypto/ecdsa", "crypto/ed25519", "crypto/rsa", "crypto/dsa",
		"crypto/elliptic", "golang.org/x/crypto/ed25519",
		"github.com/ethereum/go-ethereum/crypto",
	}

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse consensus package: %v", err)
	}
	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			if strings.HasSuffix(name, "_test.go") {
				continue
			}
			for _, imp := range file.Imports {
				path := strings.Trim(imp.Path.Value, `"`)
				for _, b := range banned {
					if path == b {
						t.Errorf("%s imports %s: consensus must be post-quantum only", name, b)
					}
				}
			}
		}
	}
}
