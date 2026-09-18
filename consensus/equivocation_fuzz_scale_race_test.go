//go:build race

package consensus

// Under the race detector every engine operation costs far more, and this
// harness performs millions of them. The budget is cut so `go test -race
// ./consensus/` stays usable; the full search runs without -race.
const (
	fuzzSeeds = 40
	fuzzSteps = 400
)
