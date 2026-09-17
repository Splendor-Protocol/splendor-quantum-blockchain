// Command netbench is the reproducible local benchmark harness Section 8.2
// of the Sept 2026 improvement plan calls for: it compares uncoded,
// deterministic-erasure and RLNC transfer of the same synthetic object
// under the same simulated loss, and reports the metrics Section 9's
// benchmark gate asks for — completion measured in symbols actually needed,
// useful vs total bytes moved, and decode CPU time — so an adoption
// decision can be made from numbers instead of assumption.
//
// This is a LOCAL, single-process simulation of network loss (a fixed
// fraction of symbols dropped before the decoder ever sees them), not a
// live multi-peer network test — Section 9's fuller scenario matrix
// (uneven bandwidth, peer disappearance, concurrent consensus load, etc.)
// requires a real multi-node deployment this harness does not attempt to
// replace. What it DOES answer honestly: for a given loss rate and
// generation shape, how much data and CPU each codec actually needs to
// fully reconstruct the object.
//
// Per the plan: this tool measures, it does not decide. RLNC ships only if
// a real benchmark run against the plan's declared targets (15% improvement
// on a declared workload, no more than 5% consensus-latency regression)
// passes — nothing in this repository enables RLNC on any live path.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"text/tabwriter"
	"time"

	"github.com/splendor-protocol/splendor-quantum-blockchain/network/coding"
)

func main() {
	sourceCount := flag.Int("source-count", 32, "source symbols per generation (plan candidates: 16, 32, 64)")
	symbolSize := flag.Int("symbol-size", 16*1024, "symbol payload size in bytes (plan candidates: 16384, 65536)")
	lossPct := flag.Float64("loss-pct", 20, "percentage of systematic symbols simulated as lost, 0-100")
	repairOverheadPct := flag.Float64("repair-overhead-pct", 25, "repair symbols requested, as a percentage of source count (plan candidates: 12.5, 25, 50)")
	trials := flag.Int("trials", 20, "number of independent trials to average over")
	seed := flag.Int64("seed", 1, "PRNG seed, for reproducible trials")
	flag.Parse()

	if *sourceCount <= 0 || *symbolSize <= 0 || *trials <= 0 {
		fmt.Fprintln(os.Stderr, "netbench: --source-count, --symbol-size and --trials must all be positive")
		os.Exit(2)
	}
	if *lossPct < 0 || *lossPct >= 100 {
		fmt.Fprintln(os.Stderr, "netbench: --loss-pct must be in [0, 100)")
		os.Exit(2)
	}

	// RLNC repair coefficients are crypto/rand in production, which would make
	// the RLNC column differ run-to-run even with a fixed --seed. Inject a
	// deterministic reader seeded from --seed so the whole run — source data,
	// loss pattern AND RLNC coefficients — is reproducible. This is benchmark
	// only; production never uses a deterministic coefficient source.
	rng := rand.New(rand.NewSource(*seed))
	codecs := []coding.Codec{
		coding.NewUncodedCodec(),
		coding.NewErasureCodec(),
		coding.NewRLNCCodecWithReader(rng),
	}

	fmt.Printf("netbench: source_count=%d symbol_size=%d loss_pct=%.1f repair_overhead_pct=%.1f trials=%d seed=%d\n\n",
		*sourceCount, *symbolSize, *lossPct, *repairOverheadPct, *trials, *seed)

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "codec\trecovered\tfailed\tavg_symbols_used\tavg_bytes_total\tavg_bytes_useful\tavg_decode_time")
	for _, c := range codecs {
		res := runTrials(c, *sourceCount, *symbolSize, *lossPct, *repairOverheadPct, *trials, *seed)
		fmt.Fprintf(w, "%s\t%d/%d\t%d\t%.1f\t%.0f\t%.0f\t%s\n",
			c.Name(), res.recovered, *trials, res.failed,
			res.avgSymbolsUsed, res.avgBytesTotal, res.avgBytesUseful, res.avgDecodeTime)
	}
	w.Flush()
}

type trialResult struct {
	recovered      int
	failed         int
	avgSymbolsUsed float64
	// avgBytesTotal is every byte the sender put on the wire (delivered or
	// lost), the real bandwidth cost. avgBytesUseful is the subset of those
	// bytes that reached the decoder and increased its rank — the fraction
	// comparing them gives the scheme's useful-bandwidth efficiency.
	avgBytesTotal  float64
	avgBytesUseful float64
	avgDecodeTime  time.Duration
}

func runTrials(codec coding.Codec, sourceCount, symbolSize int, lossPct, repairOverheadPct float64, trials int, seed int64) trialResult {
	var (
		totalSymbolsUsed int
		totalBytesTotal  int64
		totalBytesUseful int64
		totalDecodeTime  time.Duration
		recovered        int
	)
	for trial := 0; trial < trials; trial++ {
		rng := rand.New(rand.NewSource(seed + int64(trial)))
		source := make([][]byte, sourceCount)
		for i := range source {
			source[i] = make([]byte, symbolSize)
			rng.Read(source[i])
		}
		enc, err := codec.NewEncoder(source, symbolSize)
		if err != nil {
			continue
		}
		dec, err := codec.NewDecoder(sourceCount, symbolSize)
		if err != nil {
			continue
		}

		symbolsUsed := 0
		bytesTotal := int64(0)
		bytesUseful := int64(0)
		var decodeCPU time.Duration

		// deliver simulates one symbol crossing the link: its bytes are added
		// to the transmitted total first (the sender paid for them whether or
		// not they arrive), then loss is applied, and only a delivered symbol
		// reaches Absorb. Decoder CPU (elimination + final solve) is timed
		// here so the reported decode time reflects the work that actually
		// reconstructs the object, not just the final Decode() call.
		deliver := func(s coding.Symbol) {
			bytesTotal += int64(len(s.Payload) + len(s.Coefficients))
			if rng.Float64()*100 < lossPct {
				return // lost in transit
			}
			start := time.Now()
			increased, err := dec.Absorb(s)
			decodeCPU += time.Since(start)
			if err != nil || !increased {
				return // delivered but redundant/independent-of-progress
			}
			symbolsUsed++
			bytesUseful += int64(len(s.Payload) + len(s.Coefficients))
		}

		for _, s := range enc.Systematic() {
			deliver(s)
		}
		if dec.Rank() < sourceCount {
			repairBudget := int(float64(sourceCount) * repairOverheadPct / 100)
			if repairBudget < 1 {
				repairBudget = 1
			}
			for _, s := range enc.Repair(repairBudget) {
				if dec.Rank() == sourceCount {
					break
				}
				deliver(s)
			}
		}

		if dec.Rank() < sourceCount {
			continue // this trial did not recover — counted as failed below
		}
		start := time.Now()
		decoded, err := dec.Decode()
		decodeCPU += time.Since(start)
		if err != nil {
			continue
		}
		if !equalBytes(source, decoded) {
			continue // decoder returned wrong bytes: recovered is NOT the same as correct
		}
		totalDecodeTime += decodeCPU
		totalSymbolsUsed += symbolsUsed
		totalBytesTotal += bytesTotal
		totalBytesUseful += bytesUseful
		recovered++
	}

	res := trialResult{recovered: recovered, failed: trials - recovered}
	if recovered > 0 {
		res.avgSymbolsUsed = float64(totalSymbolsUsed) / float64(recovered)
		res.avgBytesTotal = float64(totalBytesTotal) / float64(recovered)
		res.avgBytesUseful = float64(totalBytesUseful) / float64(recovered)
		res.avgDecodeTime = totalDecodeTime / time.Duration(recovered)
	}
	return res
}

// equalBytes reports whether two symbol slices carry identical bytes, used to
// verify that a successful Decode actually reproduced the source — recovery
// that returns the wrong object is not recovery.
func equalBytes(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}
