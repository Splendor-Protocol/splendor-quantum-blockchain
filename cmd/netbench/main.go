// Command netbench is the reproducible local benchmark harness Section 8.2
// of the Sept 2026 improvement plan calls for: it compares uncoded,
// deterministic-erasure and RLNC transfer of the same synthetic object
// under the same simulated loss, and reports the metrics Section 9's
// benchmark gate asks for — completion measured in symbols actually needed,
// useful vs total bytes moved (including SQCO proof frames), authenticate-
// before-decode CPU, and final decode CPU — so an adoption decision can be
// made from numbers instead of assumption.
//
// Each trial now uses the objects transfer controller: a finite codebook is
// signed once, every symbol is a Merkle proof, and a corrupt proof is
// rejected before Gaussian elimination. Loss is still a LOCAL single-process
// simulation (a fixed fraction of proofs dropped before the receiver sees
// them), not a live multi-peer network test — Section 9's fuller scenario
// matrix (uneven bandwidth, peer disappearance, concurrent consensus load)
// still needs a real deployment. The sealed live path is --object-codec on
// sqc-node; this tool does not enable it.
//
// Per the plan: this tool measures, it does not decide. RLNC ships as the
// default large-object codec only if a real run against the plan's declared
// targets (15% improvement on a declared workload, no more than 5%
// consensus-latency regression) passes.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"text/tabwriter"
	"time"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network/coding"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network/objects"
)

const (
	scenarioLoss         = "loss"
	scenarioCorruptProof = "corrupt-proof"
	scenarioCorruptCoeff = "corrupt-coeff"
)

func main() {
	sourceCount := flag.Int("source-count", 32, "source symbols per generation (plan candidates: 16, 32, 64)")
	symbolSize := flag.Int("symbol-size", 16*1024, "symbol payload size in bytes (plan candidates: 16384, 65536)")
	lossPct := flag.Float64("loss-pct", 20, "percentage of codebook proofs simulated as lost, 0-100")
	repairOverheadPct := flag.Float64("repair-overhead-pct", 25, "finite repair symbols committed, as a percentage of source count (plan candidates: 12.5, 25, 50)")
	trials := flag.Int("trials", 20, "number of independent trials to average over")
	seed := flag.Int64("seed", 1, "PRNG seed, for reproducible trials")
	scenario := flag.String("scenario", scenarioLoss, "loss, corrupt-proof, or corrupt-coeff")
	flag.Parse()

	if *sourceCount <= 0 || *symbolSize <= 0 || *trials <= 0 {
		fmt.Fprintln(os.Stderr, "netbench: --source-count, --symbol-size and --trials must all be positive")
		os.Exit(2)
	}
	if *lossPct < 0 || *lossPct >= 100 {
		fmt.Fprintln(os.Stderr, "netbench: --loss-pct must be in [0, 100)")
		os.Exit(2)
	}
	switch *scenario {
	case scenarioLoss, scenarioCorruptProof, scenarioCorruptCoeff:
	default:
		fmt.Fprintf(os.Stderr, "netbench: unknown --scenario %q\n", *scenario)
		os.Exit(2)
	}

	_, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		fmt.Fprintf(os.Stderr, "netbench: keygen: %v\n", err)
		os.Exit(1)
	}

	codecs := []coding.Codec{
		coding.NewUncodedCodec(),
		coding.NewErasureCodec(),
		coding.NewRLNCCodec(),
	}

	fmt.Printf("netbench: scenario=%s source_count=%d symbol_size=%d loss_pct=%.1f repair_overhead_pct=%.1f trials=%d seed=%d\n\n",
		*scenario, *sourceCount, *symbolSize, *lossPct, *repairOverheadPct, *trials, *seed)

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "codec\trecovered\tfailed\trejected_auth\tavg_symbols_used\tavg_bytes_total\tavg_bytes_useful\tavg_manifest_bytes\tavg_auth_time\tavg_decode_time")
	for _, c := range codecs {
		res := runTrialsScenario(c, *sourceCount, *symbolSize, *lossPct, *repairOverheadPct, *trials, *seed, *scenario, priv)
		fmt.Fprintf(w, "%s\t%d/%d\t%d\t%d\t%.1f\t%.0f\t%.0f\t%.0f\t%s\t%s\n",
			c.Name(), res.recovered, *trials, res.failed, res.rejectedAuth,
			res.avgSymbolsUsed, res.avgBytesTotal, res.avgBytesUseful, res.avgManifestBytes,
			res.avgAuthTime, res.avgDecodeTime)
	}
	w.Flush()
}

type trialResult struct {
	recovered      int
	failed         int
	rejectedAuth   int
	avgSymbolsUsed float64
	// avgBytesTotal is every SQCO byte the sender put on the wire (delivered
	// or lost), including the manifest. avgBytesUseful is the subset of
	// proof frames that arrived and increased decoder rank.
	avgBytesTotal    float64
	avgBytesUseful   float64
	avgManifestBytes float64
	avgAuthTime      time.Duration
	avgDecodeTime    time.Duration
}

func runTrials(codec coding.Codec, sourceCount, symbolSize int, lossPct, repairOverheadPct float64, trials int, seed int64) trialResult {
	return runTrialsScenario(codec, sourceCount, symbolSize, lossPct, repairOverheadPct, trials, seed, scenarioLoss, nil)
}

func runTrialsScenario(codec coding.Codec, sourceCount, symbolSize int, lossPct, repairOverheadPct float64, trials int, seed int64, scenario string, signer *qcrypto.PrivateKey) trialResult {
	if signer == nil {
		_, priv, err := qcrypto.GenerateKeypair()
		if err != nil {
			return trialResult{failed: trials}
		}
		signer = priv
	}
	var (
		totalSymbolsUsed   int
		totalBytesTotal    int64
		totalBytesUseful   int64
		totalManifestBytes int64
		totalAuthTime      time.Duration
		totalDecodeTime    time.Duration
		recovered          int
		rejectedAuth       int
	)
	repairCount := int(float64(sourceCount) * repairOverheadPct / 100)
	for trial := 0; trial < trials; trial++ {
		rng := rand.New(rand.NewSource(seed + int64(trial)))
		object := make([]byte, sourceCount*symbolSize)
		rng.Read(object)
		trialCodec := codec
		if codec.Name() == coding.NewRLNCCodec().Name() {
			coeffRng := rand.New(rand.NewSource(seed + int64(trial) + 1_000_000_003))
			trialCodec = coding.NewRLNCCodecWithReader(coeffRng)
		}
		desc := objects.Descriptor{
			Anchor:     objects.Anchor{Kind: "snapshot", ID: [32]byte{byte(trial)}, Height: uint64(trial)},
			Codec:      trialCodec.Name(),
			SymbolSize: uint32(symbolSize),
		}
		send, err := objects.NewSender(object, desc, trialCodec, repairCount, signer, objects.DefaultLimits())
		if err != nil {
			continue
		}
		recv, err := objects.NewReceiver(send.Manifest(), objects.DefaultLimits(), objects.AllowSignerBytes(send.Manifest().SignerPubKey))
		if err != nil {
			continue
		}
		mFrame, err := objects.EncodeManifest(send.Manifest())
		if err != nil {
			continue
		}
		bytesTotal := int64(len(mFrame))
		bytesUseful := int64(0)
		symbolsUsed := 0
		var authCPU, decodeCPU time.Duration
		corrupted := false

		for i := 0; i < send.SymbolCount(); i++ {
			if recv.Ready() {
				break
			}
			proof, err := send.Proof(i)
			if err != nil {
				bytesTotal = -1
				break
			}
			frame, err := objects.EncodeSymbolProof(proof)
			if err != nil {
				bytesTotal = -1
				break
			}
			bytesTotal += int64(len(frame))
			if rng.Float64()*100 < lossPct {
				continue
			}
			switch scenario {
			case scenarioCorruptProof:
				if !corrupted && len(proof.Symbol.Payload) > 0 {
					proof.Symbol.Payload = append([]byte(nil), proof.Symbol.Payload...)
					proof.Symbol.Payload[0] ^= 0xff
					corrupted = true
				}
			case scenarioCorruptCoeff:
				if !corrupted && len(proof.Symbol.Coefficients) > 0 {
					proof.Symbol.Coefficients = append([]byte(nil), proof.Symbol.Coefficients...)
					proof.Symbol.Coefficients[0] ^= 0xff
					corrupted = true
				}
			}
			start := time.Now()
			increased, err := recv.AbsorbProof(proof)
			authCPU += time.Since(start)
			if errors.Is(err, objects.ErrUnauthenticatedSymbol) {
				rejectedAuth++
				continue
			}
			if err != nil || !increased {
				continue
			}
			symbolsUsed++
			bytesUseful += int64(len(frame))
		}
		if bytesTotal < 0 || !recv.Ready() {
			continue
		}
		start := time.Now()
		got, err := recv.Decode()
		decodeCPU += time.Since(start)
		if err != nil || !bytes.Equal(got, object) {
			continue
		}
		totalDecodeTime += decodeCPU
		totalAuthTime += authCPU
		totalSymbolsUsed += symbolsUsed
		totalBytesTotal += bytesTotal
		totalBytesUseful += bytesUseful
		totalManifestBytes += int64(len(mFrame))
		recovered++
	}

	res := trialResult{recovered: recovered, failed: trials - recovered, rejectedAuth: rejectedAuth}
	if recovered > 0 {
		res.avgSymbolsUsed = float64(totalSymbolsUsed) / float64(recovered)
		res.avgBytesTotal = float64(totalBytesTotal) / float64(recovered)
		res.avgBytesUseful = float64(totalBytesUseful) / float64(recovered)
		res.avgManifestBytes = float64(totalManifestBytes) / float64(recovered)
		res.avgAuthTime = totalAuthTime / time.Duration(recovered)
		res.avgDecodeTime = totalDecodeTime / time.Duration(recovered)
	}
	return res
}

// equalBytes reports whether two symbol slices carry identical bytes.
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
