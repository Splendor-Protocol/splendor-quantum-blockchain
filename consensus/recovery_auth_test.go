package consensus

import (
	"path/filepath"
	"testing"
)

func TestRecoveryWithoutValidatorSetCannotCreateRoundChangeQC(t *testing.T) {
	set, keys := testSet(t, 4)
	j, err := OpenVoteJournal(filepath.Join(t.TempDir(), "votes.json"))
	if err != nil {
		t.Fatal(err)
	}
	var hash [32]byte
	hash[0] = 1
	if err := j.RecordPrevote(5, 3, hash, -1); err != nil {
		t.Fatal(err)
	}

	eng := twoPhaseFor(t, set, keys, 0, BFTConfig{VoteJournal: j})
	eng.UpdateValidatorSet(nil)
	eng.SetHeight(5)
	eng.UpdateValidatorSet(set)

	if eng.CanPropose() {
		t.Fatal("one local recovery announcement became a quorum while the validator set was empty")
	}
}

func TestRoundChangeRejectsBodyWithInvalidProducerSignature(t *testing.T) {
	set, keys := testSet(t, 4)
	const height = uint64(4)

	eng := twoPhaseFor(t, set, keys, 2, BFTConfig{})
	eng.SetHeight(height)
	eng.handleTimeout()
	eng.handleTimeout()

	body := proposalAt(t, set, keys, height, 1, 77).Block
	body.ValidatorSignature[0] ^= 0xff
	rc := lockedRoundChangeFrom(t, set, keys, 1, height, 2, 1, body, []int{1, 2, 3})
	eng.handleRoundChange(rc)

	rec := roundChangeRecordOf(eng, 2, set[1].Address)
	if rec == nil {
		t.Fatal("round-change was not counted")
	}
	if rec.justified || rec.rc.LockedBlock != nil || rec.rc.LockedCert != nil {
		t.Fatal("a prepared certificate selected a body whose producer signature is invalid")
	}
	if got := eng.SafeProposal(); got != nil {
		t.Fatal("invalidly signed round-change body became the safe proposal")
	}
}
