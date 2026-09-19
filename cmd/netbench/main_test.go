package main

import (
	"testing"

	"github.com/splendor-protocol/splendor-quantum-blockchain/network/coding"
)

// At zero loss, every codec must recover every trial — the healthy case
// costs nothing extra for any of them.
func TestRunTrialsNoLossAlwaysRecovers(t *testing.T) {
	for _, c := range []coding.Codec{coding.NewUncodedCodec(), coding.NewErasureCodec(), coding.NewRLNCCodec()} {
		res := runTrials(c, 8, 64, 0, 25, 10, 1)
		if res.recovered != 10 {
			t.Fatalf("%s: recovered %d/10 at 0%% loss, want 10", c.Name(), res.recovered)
		}
		if res.avgSymbolsUsed != 8 {
			t.Fatalf("%s: avg symbols used %.1f at 0%% loss, want exactly 8 (systematic only)", c.Name(), res.avgSymbolsUsed)
		}
	}
}

// Uncoded, with no repair concept, must fail every trial once loss is
// certain (100% would be degenerate; use a loss rate high enough that
// losing at least one of a small generation is overwhelmingly likely across
// many trials, and confirm the SPECIFIC uncoded behaviour: it cannot
// recover from ANY loss, unlike the coded alternatives).
func TestRunTrialsUncodedFailsWithAnyLoss(t *testing.T) {
	res := runTrials(coding.NewUncodedCodec(), 4, 32, 50, 25, 100, 7)
	if res.recovered == 100 {
		t.Fatal("uncoded codec recovered every trial despite 50% simulated loss — loss simulation is not working")
	}
	if res.failed == 0 {
		t.Fatal("uncoded codec never failed despite lossy trials")
	}
}

// RLNC and erasure coding must both actually benefit from repair symbols:
// at a loss rate they cannot survive using ONLY systematic symbols, giving
// them a repair budget must raise their recovery rate versus giving them
// none.
func TestRunTrialsRepairBudgetImprovesRecovery(t *testing.T) {
	for _, c := range []coding.Codec{coding.NewErasureCodec(), coding.NewRLNCCodec()} {
		noRepair := runTrials(c, 16, 64, 30, 0, 200, 42)
		withRepair := runTrials(c, 16, 64, 30, 50, 200, 42)
		if withRepair.recovered <= noRepair.recovered {
			t.Fatalf("%s: recovery with repair (%d/200) did not improve over no repair (%d/200)",
				c.Name(), withRepair.recovered, noRepair.recovered)
		}
	}
}

// A codec that fails every trial must report zero averages, not divide by
// zero or report stale data from a partially-completed trial.
func TestRunTrialsAllFailedReportsZeroAverages(t *testing.T) {
	res := runTrials(coding.NewUncodedCodec(), 8, 32, 99, 0, 5, 3)
	if res.recovered == 5 {
		t.Skip("test relies on high loss causing failures; got lucky with this seed, not a real failure")
	}
	if res.failed == 5 {
		if res.avgSymbolsUsed != 0 || res.avgBytesTotal != 0 || res.avgBytesUseful != 0 || res.avgDecodeTime != 0 {
			t.Fatalf("all trials failed but averages are nonzero: %+v", res)
		}
	}
}

// Under loss, the transmitted-byte total must exceed the useful-byte count:
// dropped symbols and dependent symbols still cost the sender bandwidth but
// never contribute to reconstruction. This is the accounting split the
// benchmark reports as avg_bytes_total vs avg_bytes_useful.
func TestRunTrialsBytesTotalExceedsUsefulUnderLoss(t *testing.T) {
	for _, c := range []coding.Codec{coding.NewErasureCodec(), coding.NewRLNCCodec()} {
		res := runTrials(c, 16, 64, 30, 50, 50, 9)
		if res.recovered == 0 {
			t.Skipf("%s: no trials recovered at 30%% loss; cannot compare byte totals", c.Name())
		}
		if res.avgBytesTotal <= res.avgBytesUseful {
			t.Fatalf("%s: avg total bytes %.0f not greater than useful bytes %.0f under loss",
				c.Name(), res.avgBytesTotal, res.avgBytesUseful)
		}
	}
}

// The content-equality helper must distinguish a correct reconstruction from
// one that returned the wrong bytes — the difference between "recovered" and
// "actually correct".
func TestEqualBytes(t *testing.T) {
	src := [][]byte{[]byte("abc"), []byte("def")}
	if !equalBytes(src, [][]byte{[]byte("abc"), []byte("def")}) {
		t.Fatal("identical slices reported unequal")
	}
	if equalBytes(src, [][]byte{[]byte("abc"), []byte("deX")}) {
		t.Fatal("differing last byte reported equal")
	}
	if equalBytes(src, [][]byte{[]byte("abc")}) {
		t.Fatal("different length reported equal")
	}
}

func TestCorruptProofIsRejectedBeforeDecode(t *testing.T) {
	for _, c := range []coding.Codec{coding.NewErasureCodec(), coding.NewRLNCCodec()} {
		res := runTrialsScenario(c, 8, 32, 0, 50, 5, 11, scenarioCorruptProof, nil, false)
		if res.rejectedAuth == 0 {
			t.Fatalf("%s: corrupt-proof scenario never rejected a proof", c.Name())
		}
		// The rest of the finite codebook can still recover after the bad
		// proof is dropped — that is authenticate-before-decode, not
		// "one bad symbol fails the object".
		if res.recovered == 0 {
			t.Fatalf("%s: coded transfer did not recover after rejecting a corrupt proof", c.Name())
		}
	}
}

func TestManifestAndProofBytesAreCounted(t *testing.T) {
	res := runTrials(coding.NewUncodedCodec(), 4, 32, 0, 0, 3, 2)
	if res.recovered != 3 {
		t.Fatalf("recovered %d/3", res.recovered)
	}
	if res.avgManifestBytes == 0 {
		t.Fatal("manifest bytes were not counted")
	}
	if res.avgBytesTotal <= res.avgBytesUseful {
		t.Fatalf("total bytes %.0f should exceed useful bytes %.0f because the manifest never increases rank",
			res.avgBytesTotal, res.avgBytesUseful)
	}
	if res.avgAuthTime == 0 {
		t.Fatal("auth time was not measured")
	}
}

// The multi-generation endpoint must recover a clean-link object exactly like
// the single-generation one — the production shape must not regress recovery.
func TestMultiGenTrialsRecover(t *testing.T) {
	for _, c := range []coding.Codec{coding.NewErasureCodec(), coding.NewRLNCCodec()} {
		res := runTrialsScenario(c, 8, 64, 0, 50, 4, 21, scenarioLoss, nil, true)
		if res.recovered != 4 {
			t.Fatalf("%s multigen: recovered %d/4, want 4 on a clean link", c.Name(), res.recovered)
		}
	}
}
