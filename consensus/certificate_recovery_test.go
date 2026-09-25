package consensus

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// A prevote quorum (POL) is a certificate that must survive a crash: a
// restarted validator has to re-attach it to its round-change, or a new leader
// cannot verify the locked value and unlock peers locked to something else.
func TestPOLCertificatePersistsAcrossRestart(t *testing.T) {
	set, keys := testSet(t, 4)
	path := filepath.Join(t.TempDir(), "vote-journal.json")
	j1, err := OpenVoteJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	eng := twoPhaseFor(t, set, keys, 0, BFTConfig{VoteJournal: j1})
	eng.SetHeight(4) // (4+0)%4 == 0: set[0] leads round 0
	blockA := proposalAt(t, set, keys, 4, 0, 11).Block
	if !eng.handleProposal(&pendingProposal{block: blockA, local: true}) {
		t.Fatal("leader did not adopt A")
	}
	for _, i := range []int{1, 2} {
		v := &Vote{Height: 4, Round: 0, BlockHash: blockA.Hash, ValidatorAddr: set[i].Address, Timestamp: int64(100 + i), Kind: types.VoteKindPrevote}
		if err := v.Sign(keys[i]); err != nil {
			t.Fatal(err)
		}
		eng.handleVote(v)
	}
	if !eng.VoteLockedAt(4) {
		t.Fatal("engine did not POL-lock on A")
	}
	h, r, hash, votes, ok := j1.LoadPOL()
	if !ok || h != 4 || r != 0 || hash != blockA.Hash {
		t.Fatalf("LoadPOL = (%d,%d,%x,ok=%v), want (4,0,A,true)", h, r, hash[:4], ok)
	}
	if len(votes) < 3 {
		t.Fatalf("persisted POL has %d votes, want >= 3", len(votes))
	}
	// Reopen the journal on the same path: the certificate must survive.
	j2, err := OpenVoteJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	if h2, r2, hash2, _, ok2 := j2.LoadPOL(); !ok2 || h2 != 4 || r2 != 0 || hash2 != blockA.Hash {
		t.Fatalf("reopened LoadPOL = (%d,%d,%x,ok=%v), want (4,0,A,true)", h2, r2, hash2[:4], ok2)
	}
}

// The headline crash-recovery case: a validator witnesses a POL (certificate),
// crashes, and restarts with no in-memory state. It must re-derive its lock
// and re-attach the certificate to its round-change so a later leader can
// verify the locked value.
func TestCrashRecoveryReattachesPOLCertificate(t *testing.T) {
	set, keys := testSet(t, 4)
	path := filepath.Join(t.TempDir(), "vote-journal.json")
	j1, err := OpenVoteJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	first := twoPhaseFor(t, set, keys, 0, BFTConfig{VoteJournal: j1})
	first.SetHeight(4)
	blockA := proposalAt(t, set, keys, 4, 0, 11).Block
	if !first.handleProposal(&pendingProposal{block: blockA, local: true}) {
		t.Fatal("leader did not adopt A")
	}
	for _, i := range []int{1, 2} {
		v := &Vote{Height: 4, Round: 0, BlockHash: blockA.Hash, ValidatorAddr: set[i].Address, Timestamp: int64(100 + i), Kind: types.VoteKindPrevote}
		if err := v.Sign(keys[i]); err != nil {
			t.Fatal(err)
		}
		first.handleVote(v)
	}
	if !first.VoteLockedAt(4) {
		t.Fatal("engine did not POL-lock on A before the crash")
	}

	// Crash: a fresh engine on the same journal path has no in-memory state.
	j2, err := OpenVoteJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	second := twoPhaseFor(t, set, keys, 0, BFTConfig{VoteJournal: j2})
	second.SetHeight(4)

	// The restarted engine must re-derive its lock from the persisted state.
	hash, round, ok := second.lockAt(4)
	if !ok || hash != blockA.Hash || round != 0 {
		t.Fatalf("restarted lockAt = (%x,%d,ok=%v), want (A,0,true)", hash[:4], round, ok)
	}
	// And its round-change must carry the certificate, not a bare hash.
	rc, err := second.signRoundChange(4, 1)
	if err != nil {
		t.Fatal(err)
	}
	if rc.LockedHash != blockA.Hash || rc.LockedRound != 0 {
		t.Fatalf("round-change lock = (%x,%d), want (A,0)", rc.LockedHash[:4], rc.LockedRound)
	}
	if len(rc.POLVotes) < 3 {
		t.Fatalf("round-change carries %d POL votes, want >= 3", len(rc.POLVotes))
	}
	if !second.verifyPOL(4, 0, blockA.Hash, rc.POLVotes) {
		t.Fatal("restored certificate does not verify against the validator set")
	}
}

// A certificate that no longer verifies against the LIVE validator set (e.g.
// the set changed while the validator was down) must be dropped, not used to
// lock or to unlock peers.
func TestRestoredPOLThatFailsVerificationIsDropped(t *testing.T) {
	setA, keysA := testSet(t, 4)
	setB, keysB := testSet(t, 4) // a different set: the certificate cannot verify
	path := filepath.Join(t.TempDir(), "vote-journal.json")
	j, err := OpenVoteJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	blockHash := [32]byte{0xAA}
	votes := make([]*Vote, 3)
	for i := 0; i < 3; i++ {
		votes[i] = &Vote{Height: 4, Round: 0, BlockHash: blockHash, ValidatorAddr: setA[i].Address, Timestamp: int64(i), Kind: types.VoteKindPrevote}
		if err := votes[i].Sign(keysA[i]); err != nil {
			t.Fatal(err)
		}
	}
	if err := j.SavePOL(4, 0, blockHash, votes); err != nil {
		t.Fatal(err)
	}

	eng := twoPhaseFor(t, setB, keysB, 0, BFTConfig{VoteJournal: j})
	eng.SetHeight(4)
	if _, _, ok := eng.lockAt(4); ok {
		t.Fatal("a POL that cannot verify against the current set must be dropped, not locked to")
	}
}

// The certificate is a bft_rounds_v1 concern: the pre-fork single-vote path has
// no POL and must not write a POL sidecar.
func TestSingleVotePathWritesNoPOL(t *testing.T) {
	set, keys := testSet(t, 1)
	path := filepath.Join(t.TempDir(), "vote-journal.json")
	j, err := OpenVoteJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	var got *types.Block
	eng := engineFor(t, set, keys, 0, BFTConfig{
		VoteJournal: j,
		OnCommit:    func(b *types.Block) error { got = b; return nil },
	})
	eng.SetHeight(1)
	msg := proposalAt(t, set, keys, 1, 0, 1)
	if !eng.handleProposal(&pendingProposal{block: msg.Block, local: true}) {
		t.Fatal("legacy proposal refused")
	}
	if got == nil {
		t.Fatal("legacy solo path must commit")
	}
	if _, _, _, _, ok := j.LoadPOL(); ok {
		t.Fatal("the single-vote path must not write a POL certificate")
	}
	if _, err := os.Stat(j.polPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("POL sidecar exists under the single-vote path: %v", err)
	}
}
