package rpc

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/block"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/mempool"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

func statusOf(t *testing.T, s *Server) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/status = %d: %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode /status: %v", err)
	}
	return out
}

// /status used to offer one consensus fact, multi_validator, which is the
// schedule bit and was read as "BFT is running". The engine, the head's
// certificate and the fee unit are now reported explicitly, and every one of
// them must be present on every node, including one with no head yet, so a
// client can rely on the shape.
func TestStatusReportsConsensusEngineAndHeadCertificate(t *testing.T) {
	s := newTestServer(t, "")
	s.executor.SetForks(forks.Schedule{forks.MultiValidator: 1})

	st := statusOf(t, s)
	for _, k := range []string{
		"consensus_mode", "head_qc_present", "head_qc_valid", "head_qc_votes",
		"head_quorum_required", "validator_set_height", "fee_unit", "decimals", "multi_validator",
	} {
		if _, ok := st[k]; !ok {
			t.Fatalf("/status lacks %q: %v", k, st)
		}
	}
	// No ConsensusMode callback means this process produces nothing.
	if st["consensus_mode"] != ConsensusModeFollower {
		t.Fatalf("consensus_mode = %v, want %q", st["consensus_mode"], ConsensusModeFollower)
	}
	// The schedule bit stays what it always was (false here: the head is 0
	// and the rule activates at 1) and the head fields are reported beside
	// it, not derived from it. The executor-level test
	// TestHeadConsensusDistinguishesScheduleBitFromCertificate covers the
	// case where the bit is true and no certificate exists.
	if _, ok := st["multi_validator"].(bool); !ok {
		t.Fatalf("multi_validator = %v, want a bool", st["multi_validator"])
	}
	if st["head_qc_present"] != false || st["head_qc_valid"] != false {
		t.Fatalf("an empty chain reported a head certificate: %v", st)
	}
	if st["fee_unit"] != "smallest_spld" || st["decimals"].(float64) != float64(types.Decimals) {
		t.Fatalf("fee denomination misreported: unit=%v decimals=%v", st["fee_unit"], st["decimals"])
	}
	if _, ok := st["genesis_hash"]; ok {
		t.Fatalf("genesis_hash reported on a chain with no block 0: %v", st["genesis_hash"])
	}
}

// consensus_mode is read from the node on every request, not captured at
// construction: a producer that moves from the single path to BFT at the
// activation height must be reported as bft from then on, without a restart.
func TestStatusConsensusModeIsReadLive(t *testing.T) {
	sdb, err := state.NewStateDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ex, err := block.NewExecutor(sdb, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mode := ConsensusModeSingle
	s := New(Config{
		ListenAddr:    ":0",
		ConsensusMode: func() string { return mode },
	}, sdb, ex, mempool.New(100, sdb), nil, nil)

	if got := statusOf(t, s)["consensus_mode"]; got != ConsensusModeSingle {
		t.Fatalf("consensus_mode = %v, want %q", got, ConsensusModeSingle)
	}
	mode = ConsensusModeBFT
	if got := statusOf(t, s)["consensus_mode"]; got != ConsensusModeBFT {
		t.Fatalf("consensus_mode after the hand-over = %v, want %q", got, ConsensusModeBFT)
	}
}
