package block

import (
	"strings"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// HeadConsensus is what lets /status say something stronger than "the rule
// is scheduled". These tests pin the four answers it must give apart:
// no head, a head with no certificate, a head with a valid certificate, and a
// head whose certificate is present but does not verify.

func TestHeadConsensusOnEmptyChainReportsNoHead(t *testing.T) {
	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()

	hc := ex.HeadConsensus()
	if hc.QCPresent || hc.QCValid {
		t.Fatalf("empty chain reported a certificate: %+v", hc)
	}
	if !strings.Contains(hc.Reason, "no head") {
		t.Fatalf("reason = %q, want it to say there is no head", hc.Reason)
	}
}

// The mainnet shape before the BFT roll: multi_validator scheduled, blocks
// sealed on the single path with no certificate. The schedule bit is true and
// the head fields must say "present: false" — that is the whole point.
func TestHeadConsensusDistinguishesScheduleBitFromCertificate(t *testing.T) {
	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()
	ex.SetForks(forks.Schedule{forks.MultiValidator: 1})
	v1, priv1, _ := qcrypto.GenerateKeypair()
	addr1 := registerValidator(t, sdb, v1)
	if _, err := ex.ProduceBlock(nil, addr1, v1.Bytes(), priv1, [32]byte{}, 1); err != nil {
		t.Fatalf("single-path block: %v", err)
	}

	hc := ex.HeadConsensus()
	if hc.Height != 1 || !hc.MultiValidatorActive {
		t.Fatalf("head/schedule misreported: %+v", hc)
	}
	if hc.QCPresent || hc.QCValid || hc.QCVotes != 0 {
		t.Fatalf("a QC-less block was reported as certified: %+v", hc)
	}
	if hc.QuorumRequired != 1 || hc.ValidatorSetSize != 1 || hc.ValidatorSetHeight != 1 {
		t.Fatalf("set/quorum misreported: %+v", hc)
	}
	if !strings.Contains(hc.Reason, "no certificate") {
		t.Fatalf("reason = %q", hc.Reason)
	}
}

// The block a sole BFT validator self-commits: one vote, a qc_root. Present
// AND valid, with a quorum of one.
func TestHeadConsensusAcceptsValidOneVoteCertificate(t *testing.T) {
	v1, priv1, _ := qcrypto.GenerateKeypair()

	producer, sdbA := newTestExecutor(t)
	defer producer.Close()
	defer sdbA.Close()
	producer.SetForks(forks.Schedule{forks.MultiValidator: 1})
	addr1 := registerValidator(t, sdbA, v1)
	b, err := producer.BuildProposal(nil, addr1, v1.Bytes(), priv1, [32]byte{}, 1)
	if err != nil {
		t.Fatalf("proposal: %v", err)
	}
	b.Votes = []types.QCVote{signVote(t, b, addr1, priv1)}
	root := types.ComputeQCRoot(b.Votes)
	b.Header.QCRoot = root[:]

	follower, sdbB := newTestExecutor(t)
	defer follower.Close()
	defer sdbB.Close()
	follower.SetForks(forks.Schedule{forks.MultiValidator: 1})
	registerValidator(t, sdbB, v1)
	if err := follower.ExecuteBlock(b); err != nil {
		t.Fatalf("apply: %v", err)
	}

	hc := follower.HeadConsensus()
	if !hc.QCPresent || !hc.QCValid {
		t.Fatalf("valid one-vote certificate not recognised: %+v", hc)
	}
	if hc.QCVotes != 1 || hc.QuorumRequired != 1 || hc.Reason != "" {
		t.Fatalf("unexpected fields: %+v", hc)
	}
}

// The genesis-validator case: nothing registered at all, the producer
// certifies with its own key. Present and valid, quorum of one, set size 0.
func TestHeadConsensusAcceptsUnregisteredGenesisValidatorSelfCertificate(t *testing.T) {
	v1, priv1, _ := qcrypto.GenerateKeypair()
	addr1 := qcrypto.DeriveAddress(v1)

	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()
	ex.SetForks(forks.Schedule{forks.MultiValidator: 1})
	b, err := ex.BuildProposal(nil, addr1, v1.Bytes(), priv1, [32]byte{}, 1)
	if err != nil {
		t.Fatalf("proposal: %v", err)
	}
	b.Votes = []types.QCVote{signVote(t, b, addr1, priv1)}
	root := types.ComputeQCRoot(b.Votes)
	b.Header.QCRoot = root[:]
	if err := ex.ExecuteBlock(b); err != nil {
		t.Fatalf("apply: %v", err)
	}

	hc := ex.HeadConsensus()
	if !hc.QCValid || hc.ValidatorSetSize != 0 || hc.QuorumRequired != 1 {
		t.Fatalf("genesis validator's self-certificate misjudged: %+v", hc)
	}
}

// A certificate can be PRESENT and still be worthless. A vote from a key the
// registry does not know, a qc_root that does not cover the votes, or too
// few votes must each read as present-but-invalid, never as valid.
func TestHeadConsensusRejectsPresentButInvalidCertificate(t *testing.T) {
	v1, priv1, _ := qcrypto.GenerateKeypair()
	v2, priv2, _ := qcrypto.GenerateKeypair()
	stranger, strangerPriv, _ := qcrypto.GenerateKeypair()

	// Build a two-validator chain whose head carries a legitimate 2-vote QC,
	// then corrupt the STORED head in three ways and re-read the verdict.
	// The executor already judged the block at apply time; HeadConsensus is
	// a separate judgement of what is on disk against the committed set, so
	// the corruption goes straight into the chain DB.
	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()
	ex.SetForks(forks.Schedule{forks.MultiValidator: 1})
	addr1 := registerValidator(t, sdb, v1)
	addr2 := registerValidator(t, sdb, v2)
	b, err := ex.BuildProposal(nil, addr1, v1.Bytes(), priv1, [32]byte{}, 1)
	if err != nil {
		t.Fatalf("proposal: %v", err)
	}
	b.Votes = []types.QCVote{signVote(t, b, addr1, priv1), signVote(t, b, addr2, priv2)}
	root := types.ComputeQCRoot(b.Votes)
	b.Header.QCRoot = root[:]
	if err := ex.ExecuteBlock(b); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if hc := ex.HeadConsensus(); !hc.QCValid || hc.QCVotes != 2 || hc.QuorumRequired != 2 {
		t.Fatalf("legitimate two-vote certificate misjudged: %+v", hc)
	}

	restore := func() {
		if err := ex.storeBlock(b); err != nil {
			t.Fatalf("restore head: %v", err)
		}
	}

	// 1. A vote from a key outside the set, replacing a real one.
	forged := *b
	forged.Votes = []types.QCVote{signVote(t, b, addr1, priv1), signVote(t, b, qcrypto.DeriveAddress(stranger), strangerPriv)}
	r := types.ComputeQCRoot(forged.Votes)
	forged.Header.QCRoot = r[:]
	if err := ex.storeBlock(&forged); err != nil {
		t.Fatal(err)
	}
	if hc := ex.HeadConsensus(); !hc.QCPresent || hc.QCValid || !strings.Contains(hc.Reason, "not in the validator set") {
		t.Fatalf("stranger's vote accepted: %+v", hc)
	}
	restore()

	// 2. Votes intact, qc_root not covering them.
	mismatched := *b
	mismatched.Header.QCRoot = []byte{1, 2, 3}
	if err := ex.storeBlock(&mismatched); err != nil {
		t.Fatal(err)
	}
	if hc := ex.HeadConsensus(); !hc.QCPresent || hc.QCValid || !strings.Contains(hc.Reason, "qc_root") {
		t.Fatalf("qc_root mismatch accepted: %+v", hc)
	}
	restore()

	// 3. One genuine vote where two are required.
	short := *b
	short.Votes = []types.QCVote{signVote(t, b, addr1, priv1)}
	r = types.ComputeQCRoot(short.Votes)
	short.Header.QCRoot = r[:]
	if err := ex.storeBlock(&short); err != nil {
		t.Fatal(err)
	}
	if hc := ex.HeadConsensus(); !hc.QCPresent || hc.QCValid || hc.QCVotes != 1 || !strings.Contains(hc.Reason, "need 2") {
		t.Fatalf("under-quorum certificate accepted: %+v", hc)
	}
	restore()
	if hc := ex.HeadConsensus(); !hc.QCValid {
		t.Fatalf("restored head no longer valid: %+v", hc)
	}
}
