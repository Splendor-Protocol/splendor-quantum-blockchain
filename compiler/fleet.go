package compiler

import (
	"fmt"
	"math/rand"
	"strings"
)

// Fleet generation: build many distinct, valid QSol contracts from a seed.
//
// The point is volume with variety. One hand-written sample proves a feature
// works; a few hundred generated ones exercise the parts that only break at
// scale — selector collisions inside a contract, address derivation across many
// deploys, gas ceilings on generated loops, and whether verification (recompile
// + byte-compare) holds for bytecode nobody wrote by hand.
//
// Generation is a pure function of the seed, so a failing contract is
// reproducible from its index alone.

// GeneratedContract is one synthetic contract plus what a caller needs to
// exercise it without re-deriving anything.
type GeneratedContract struct {
	Name   string
	Source string
	// Probes are calls whose result is predictable from the source alone, so a
	// harness can assert real behaviour rather than merely "it did not revert".
	Probes []Probe
}

// Probe is a call to make and the value it must return.
type Probe struct {
	Fn   string
	Args []int64
	Want int64
}

// GenerateFleet builds n distinct contracts from seed.
func GenerateFleet(seed int64, n int) []GeneratedContract {
	r := rand.New(rand.NewSource(seed))
	out := make([]GeneratedContract, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, generateContract(r, i))
	}
	return out
}

// shape records what a generated contract contains, so the body builders and
// the probe builders agree without passing the source text around.
type shape struct {
	name     string
	scalars  []string
	mappings []string
}

func generateContract(r *rand.Rand, idx int) GeneratedContract {
	s := shape{name: fmt.Sprintf("Gen%03d", idx)}
	for i := 0; i < 1+r.Intn(3); i++ {
		s.scalars = append(s.scalars, fmt.Sprintf("s%d", i))
	}
	for i := 0; i < r.Intn(3); i++ {
		s.mappings = append(s.mappings, fmt.Sprintf("m%d", i))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "// generated fleet member %d\ncontract %s {\n", idx, s.name)
	for _, name := range s.scalars {
		fmt.Fprintf(&b, "    uint256 %s;\n", name)
	}
	for _, name := range s.mappings {
		fmt.Fprintf(&b, "    mapping(uint256 => uint256) %s;\n", name)
	}
	fmt.Fprintln(&b, "    address owner;")

	var probes []Probe
	// Every contract gets an ownership guard: it exercises require + msg.sender
	// and gives the fleet a uniform way to test a revert path.
	fmt.Fprintln(&b, `
    function claim() public {
        require(owner == 0, "claimed");
        owner = msg.sender;
    }`)

	// A body per generator, chosen at random, each contributing its own probes.
	builders := []func(*strings.Builder, *shape, *rand.Rand) []Probe{
		genSetGet, genArith, genLoopSum, genMappingRoundTrip, genBranch, genEmitter, genGuard,
	}
	r.Shuffle(len(builders), func(i, j int) { builders[i], builders[j] = builders[j], builders[i] })
	count := 2 + r.Intn(4)
	if count > len(builders) {
		count = len(builders)
	}
	for _, build := range builders[:count] {
		probes = append(probes, build(&b, &s, r)...)
	}

	fmt.Fprintln(&b, "}")
	return GeneratedContract{Name: s.name, Source: b.String(), Probes: probes}
}

func genSetGet(b *strings.Builder, s *shape, _ *rand.Rand) []Probe {
	v := s.scalars[0]
	fmt.Fprintf(b, `
    function set(uint256 n) public { %s = n; }
    function get() public view returns (uint256) { return %s; }
`, v, v)
	return nil // stateful: the harness drives set/get itself
}

func genArith(b *strings.Builder, _ *shape, r *rand.Rand) []Probe {
	k := int64(1 + r.Intn(9))
	fmt.Fprintf(b, `
    function scale(uint256 n) public view returns (uint256) { return n * %d + %d; }
`, k, k)
	n := int64(3)
	return []Probe{{Fn: "scale", Args: []int64{n}, Want: n*k + k}}
}

func genLoopSum(b *strings.Builder, _ *shape, _ *rand.Rand) []Probe {
	fmt.Fprint(b, `
    function sumTo(uint256 n) public view returns (uint256) {
        uint256 total = 0;
        for (uint256 i = 1; i <= n; i += 1) { total += i; }
        return total;
    }
`)
	return []Probe{
		{Fn: "sumTo", Args: []int64{5}, Want: 15},
		{Fn: "sumTo", Args: []int64{0}, Want: 0},
	}
}

func genMappingRoundTrip(b *strings.Builder, s *shape, _ *rand.Rand) []Probe {
	if len(s.mappings) == 0 {
		return nil
	}
	m := s.mappings[0]
	fmt.Fprintf(b, `
    function put(uint256 k, uint256 v) public { %s[k] = v; }
    function fetch(uint256 k) public view returns (uint256) { return %s[k]; }
`, m, m)
	return nil // stateful: driven by the harness
}

func genBranch(b *strings.Builder, _ *shape, r *rand.Rand) []Probe {
	pivot := int64(1 + r.Intn(50))
	fmt.Fprintf(b, `
    function classify(uint256 n) public view returns (uint256) {
        if (n < %d) { return 1; }
        else if (n == %d) { return 2; }
        else { return 3; }
    }
`, pivot, pivot)
	return []Probe{
		{Fn: "classify", Args: []int64{0}, Want: 1},
		{Fn: "classify", Args: []int64{pivot}, Want: 2},
		{Fn: "classify", Args: []int64{pivot + 1}, Want: 3},
	}
}

func genEmitter(b *strings.Builder, s *shape, _ *rand.Rand) []Probe {
	v := s.scalars[0]
	fmt.Fprintf(b, `
    function bump(uint256 n) public {
        %s += n;
        emit Bumped(n, %s);
    }
`, v, v)
	return nil
}

func genGuard(b *strings.Builder, _ *shape, r *rand.Rand) []Probe {
	floor := int64(10 + r.Intn(40))
	fmt.Fprintf(b, `
    function atLeast(uint256 n) public view returns (uint256) {
        require(n >= %d, "too small");
        return n;
    }
`, floor)
	return []Probe{{Fn: "atLeast", Args: []int64{floor}, Want: floor}}
}
