// Command netbench is the reproducible local benchmark harness Section 8.2
// of the Sept 2026 improvement plan calls for: it compares uncoded,
// deterministic-erasure and RLNC transfer of the same synthetic object
// under the same simulated loss, and reports the metrics Section 9's
// benchmark gate asks for — completion measured in symbols actually needed,
// useful vs total bytes moved (including SQCO proof frames), authenticate-
// before-decode CPU, and final decode CPU.
//
// Each trial uses the objects transfer controller: a finite codebook is
// signed once, every symbol is a Merkle proof, and a corrupt proof is
// rejected before Gaussian elimination. Loss is a LOCAL single-process
// simulation (a fixed fraction of proofs dropped before the receiver sees
// them), not a live multi-peer network test — Section 9's fuller scenario
// matrix (uneven bandwidth, peer disappearance, concurrent consensus load)
// still needs a real deployment.
//
// Decision record: RLNC is the default serve codec on sqc-node
// (--object-codec=rlnc-gf256-v1) and the coded-object snapshot path is the
// preferred transfer in state sync, with the plain single-shot/chunked path
// as fallback.
//
// The comparison that justified it was against a SINGLE-SHOT uncoded
// baseline -- "at 20% loss with 50% repair, erasure and RLNC recover 30/30
// while uncoded recovers 0/30 (no repair, no retransmission)". That is true
// and it is not a fair fight: no real transport gives up after one pass, and
// review item 7 asks for a baseline with selective retransmission. Hence
// --retransmit-rounds, and the honest numbers (32 sources, 16 KiB symbols,
// 20% loss, 50% repair, 30 trials, seed 7):
//
//	rounds  uncoded   rlnc     uncoded bytes  rlnc bytes  uncoded auth  rlnc auth
//	0       0/30      30/30    -              672164      -             2.53ms
//	2       23/30     30/30    -              672164      -             2.53ms
//	4       30/30     30/30    669822         672164      738us         2.50ms
//
// So coding does NOT buy recovery that retransmission cannot reach, and it
// does not buy bytes -- at 20% loss the retransmitting baseline moves
// slightly FEWER (669,822 vs 672,164) and costs a third of the authenticate
// CPU. What it buys is ROUND TRIPS: one pass instead of up to four. That is
// the whole case, and it is a case about latency, not loss. It matters on the
// join path this chain actually has -- a validator 200 ms away loses about a
// third of its leader slots to timeouts, and a snapshot join that needs four
// sequential rounds pays that latency four times. It would not justify coding
// on a LAN.
//
// The consensus-latency regression half of the gate is structural: the coded
// path is the join/snapshot transfer only, never the vote/proposal hot path.
// This tool remains the measurement harness; it does not decide.
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
	retransmitRounds := flag.Int("retransmit-rounds", 0, "selective-retransmission rounds for the baseline: after the first pass, re-request the symbols that were lost, up to this many extra rounds (each still subject to loss). 0 is single-shot. A real uncoded transport retransmits, so a coded-vs-uncoded comparison at 0 is not a comparison with anything anyone deploys")
	multigen := flag.Bool("multigen", false, "run the multi-generation transfer controller (production shape): the object is split across generations with a per-generation source cap of half the requested count")
	flag.Parse()

	if *sourceCount <= 0 || *symbolSize <= 0 || *trials <= 0 {
		fmt.Fprintln(os.Stderr, "netbench: --source-count, --symbol-size and --trials must all be positive")
		os.Exit(2)
	}
	if *multigen && *sourceCount < 4 {
		fmt.Fprintln(os.Stderr, "netbench: --multigen needs --source-count >= 4 so the object spans at least two generations")
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

	fmt.Printf("netbench: scenario=%s source_count=%d symbol_size=%d loss_pct=%.1f repair_overhead_pct=%.1f trials=%d seed=%d multigen=%v\n\n",
		*scenario, *sourceCount, *symbolSize, *lossPct, *repairOverheadPct, *trials, *seed, *multigen)

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "codec\trecovered\tfailed\trejected_auth\tavg_symbols_used\tavg_bytes_total\tavg_bytes_useful\tavg_manifest_bytes\tavg_auth_time\tavg_decode_time")
	for _, c := range codecs {
		res := runTrialsScenario(c, *sourceCount, *symbolSize, *lossPct, *repairOverheadPct, *trials, *seed, *scenario, priv, *multigen, *retransmitRounds)
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
	return runTrialsScenario(codec, sourceCount, symbolSize, lossPct, repairOverheadPct, trials, seed, scenarioLoss, nil, false, 0)
}

// trialEndpoint abstracts the single- and multi-generation transfer
// controllers behind one interface so the trial loop is identical for both —
// the per-generation codebook of the production path and the one-shot
// codebook of the classic path.
type trialEndpoint struct {
	multi bool

	single *objects.Sender
	recv   *objects.Receiver

	obj   *objects.ObjectSender
	orecv *objects.ObjectReceiver
}

func (e trialEndpoint) generations() int {
	if e.multi {
		return e.obj.GenerationCount()
	}
	return 1
}

func (e trialEndpoint) symbolCount(gen int) int {
	if e.multi {
		return e.obj.SymbolCount(gen)
	}
	return e.single.SymbolCount()
}

func (e trialEndpoint) proof(gen, idx int) (objects.SymbolProof, error) {
	if e.multi {
		return e.obj.Proof(gen, idx)
	}
	return e.single.Proof(idx)
}

func (e trialEndpoint) ready(gen int) bool {
	if e.multi {
		return e.orecv.GenReady(gen)
	}
	return e.recv.Ready()
}

func (e trialEndpoint) absorb(gen int, proof objects.SymbolProof) (bool, error) {
	if e.multi {
		return e.orecv.AbsorbProof(gen, proof)
	}
	return e.recv.AbsorbProof(proof)
}

func (e trialEndpoint) decode() ([]byte, error) {
	if e.multi {
		return e.orecv.Decode()
	}
	return e.recv.Decode()
}

// lostSymbol is a symbol the loss simulation dropped, remembered so a
// retransmitting baseline can ask for it again.
type lostSymbol struct {
	gen   int
	index int
}

// allReady reports whether every generation has enough symbols to decode.
func allReady(e trialEndpoint) bool {
	for g := 0; g < e.generations(); g++ {
		if !e.ready(g) {
			return false
		}
	}
	return true
}

func runTrialsScenario(codec coding.Codec, sourceCount, symbolSize int, lossPct, repairOverheadPct float64, trials int, seed int64, scenario string, signer *qcrypto.PrivateKey, multigen bool, retransmitRounds int) trialResult {
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
		desc := objects.ObjectDescriptor{
			Anchor:     objects.Anchor{Kind: "snapshot", ID: [32]byte{byte(trial)}, Height: uint64(trial)},
			Codec:      trialCodec.Name(),
			SymbolSize: uint32(symbolSize),
		}
		var (
			endpoint trialEndpoint
			mFrame   []byte
		)
		if multigen {
			// Force the object to span at least two generations, the shape the
			// production path uses for large objects: the per-generation
			// source cap is half the requested count.
			limits := objects.DefaultLimits()
			limits.MaxSourceCount = uint32(sourceCount/2 + sourceCount%2)
			send, err := objects.NewObjectSender(object, desc, trialCodec, repairCount, signer, limits)
			if err != nil {
				continue
			}
			recv, err := objects.NewObjectReceiver(send.Manifest(), limits, objects.AllowObjectSignerBytes(send.Manifest().SignerPubKey))
			if err != nil {
				continue
			}
			mFrame, err = objects.EncodeObjectManifest(send.Manifest())
			if err != nil {
				continue
			}
			endpoint = trialEndpoint{multi: true, obj: send, orecv: recv}
		} else {
			send, err := objects.NewSender(object, objects.Descriptor{
				Anchor:     desc.Anchor,
				Codec:      desc.Codec,
				SymbolSize: desc.SymbolSize,
			}, trialCodec, repairCount, signer, objects.DefaultLimits())
			if err != nil {
				continue
			}
			recv, err := objects.NewReceiver(send.Manifest(), objects.DefaultLimits(), objects.AllowSignerBytes(send.Manifest().SignerPubKey))
			if err != nil {
				continue
			}
			mFrame, err = objects.EncodeManifest(send.Manifest())
			if err != nil {
				continue
			}
			endpoint = trialEndpoint{single: send, recv: recv}
		}
		bytesTotal := int64(len(mFrame))
		bytesUseful := int64(0)
		symbolsUsed := 0
		var authCPU, decodeCPU time.Duration
		corrupted := false
		var lost []lostSymbol

		for gen := 0; gen < endpoint.generations(); gen++ {
			for i := 0; i < endpoint.symbolCount(gen); i++ {
				if endpoint.ready(gen) {
					break
				}
				proof, err := endpoint.proof(gen, i)
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
					lost = append(lost, lostSymbol{gen: gen, index: i})
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
				increased, err := endpoint.absorb(gen, proof)
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
			if bytesTotal < 0 {
				break
			}
		}
		// Selective retransmission. A real uncoded transport re-requests what
		// was lost; comparing a coded transfer against one that gives up after
		// a single pass measures the absence of retries, not the value of
		// coding. Every retransmitted frame is charged to bytesTotal, so the
		// cost of recovering this way stays visible next to the coded path's
		// repair overhead.
		for round := 0; round < retransmitRounds && bytesTotal >= 0 && len(lost) > 0; round++ {
			if allReady(endpoint) {
				break
			}
			var stillLost []lostSymbol
			for _, ls := range lost {
				if endpoint.ready(ls.gen) {
					continue
				}
				proof, err := endpoint.proof(ls.gen, ls.index)
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
					stillLost = append(stillLost, ls)
					continue
				}
				start := time.Now()
				increased, err := endpoint.absorb(ls.gen, proof)
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
			lost = stillLost
		}
		if bytesTotal < 0 || !endpoint.ready(endpoint.generations()-1) {
			continue
		}
		start := time.Now()
		got, err := endpoint.decode()
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
