//go:build !race

package consensus

// The search budget for TestTwoPhaseNoSelfEquivocationUnderRandomInterleavings.
// Both defects it was written for are found well inside this: the same-round
// re-mint within a couple of seeds, the failed-apply view wipe at seed 117.
const (
	fuzzSeeds = 400
	fuzzSteps = 1500
)
