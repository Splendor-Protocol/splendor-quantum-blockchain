package rpc

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/block"
	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// A response labelled with a committed state_root and block_height must be
// answerable from that block alone. The pending layer holds a proposal that
// has not committed and may never commit — ValidateProposal populates it,
// DiscardPending throws it away — so a balance taken from it, wearing a
// committed root, tells a caller that a discarded proposal is chain state.
func TestStateAccountNeverServesPendingOverlayAsCommitted(t *testing.T) {
	sdb, err := state.NewStateDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sdb.Close()

	var addr qcrypto.Address
	addr[31] = 0x41
	acc := types.NewAccount(addr)
	acc.AddBalance(big.NewInt(100))
	sdb.SetAccount(acc)
	if _, err := sdb.CommitWithHeight(7); err != nil {
		t.Fatal(err)
	}
	committedRoot, err := sdb.StateRoot()
	if err != nil {
		t.Fatal(err)
	}

	// A proposal under evaluation: staged in the pending layer, not committed.
	speculative := types.NewAccount(addr)
	speculative.AddBalance(big.NewInt(999))
	sdb.SetAccount(speculative)

	r := mux.NewRouter()
	RegisterStateRoutes(r, sdb, nil, nil)

	get := func() map[string]any {
		t.Helper()
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/state/account/"+addr.String(), nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}

	withProposal := get()
	if withProposal["balance"] != "100" {
		t.Fatalf("balance = %v, want the committed 100: the pending proposal leaked into a response labelled committed", withProposal["balance"])
	}
	if withProposal["state_root"] != hex.EncodeToString(committedRoot[:]) {
		t.Fatalf("state_root = %v, want the committed root %x", withProposal["state_root"], committedRoot[:])
	}
	if h, ok := withProposal["block_height"].(float64); !ok || uint64(h) != 7 {
		t.Fatalf("block_height = %v, want 7", withProposal["block_height"])
	}

	// The proposal is discarded. Nothing about the committed answer may move.
	sdb.DiscardPending()
	afterDiscard := get()
	if withProposal["balance"] != afterDiscard["balance"] ||
		withProposal["state_root"] != afterDiscard["state_root"] ||
		withProposal["block_height"] != afterDiscard["block_height"] {
		t.Fatalf("a discarded proposal changed a committed response:\nbefore %v\nafter  %v", withProposal, afterDiscard)
	}
}

// block_hash, block_height and state_root on a proof must name one block. The
// hash used to be read from the current head after the proof was built, so a
// commit landing in between paired a later block's hash with an earlier
// proof's root — exactly what a verifier cannot detect.
func TestStateProofBlockHashIdentifiesTheProofRoot(t *testing.T) {
	sdb, err := state.NewStateDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sdb.Close()
	ex, err := block.NewExecutor(sdb, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer ex.Close()

	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	from := qcrypto.DeriveAddress(pub)
	funded := types.NewAccount(from)
	funded.AddBalance(big.NewInt(1_000_000))
	sdb.SetAccount(funded)
	if _, err := sdb.Commit(); err != nil {
		t.Fatal(err)
	}
	vpub, vpriv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	tx := types.NewTransfer(from, qcrypto.Address{0x51}, big.NewInt(100), big.NewInt(1000), 0, 1)
	if err := tx.Sign(priv, true); err != nil {
		t.Fatal(err)
	}
	if _, err := ex.ProduceBlock([]*types.Transaction{tx}, qcrypto.DeriveAddress(vpub), vpub.Bytes(), vpriv, [32]byte{}, 1); err != nil {
		t.Fatalf("produce: %v", err)
	}

	// The committed root moves on without the head moving — what a reader sees
	// when a commit lands between the proof walk and the head lookup.
	var other qcrypto.Address
	other[31] = 0x77
	extra := types.NewAccount(other)
	extra.AddBalance(big.NewInt(5))
	sdb.SetAccount(extra)
	if _, err := sdb.CommitWithHeight(1); err != nil {
		t.Fatal(err)
	}

	r := mux.NewRouter()
	RegisterStateRoutes(r, sdb, ex, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/state/proof/"+from.String(), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("proof status %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		StateRoot   string `json:"state_root"`
		BlockHeight uint64 `json:"block_height"`
		BlockHash   string `json:"block_hash"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.BlockHash == "" {
		return // no hash claimed, nothing to contradict
	}
	blk, err := ex.GetBlockByHeight(body.BlockHeight)
	if err != nil || blk == nil {
		t.Fatalf("proof named block_height %d, which this node does not have: %v", body.BlockHeight, err)
	}
	if got := hex.EncodeToString(blk.Hash[:]); got != body.BlockHash {
		t.Fatalf("block_hash %s is not the block at block_height %d (%s)", body.BlockHash, body.BlockHeight, got)
	}
	if got := hex.EncodeToString(blk.Header.StateRoot[:]); got != body.StateRoot {
		t.Fatalf("proof state_root %s is not the state root of the block it names (%s): root, height and hash identify different blocks",
			body.StateRoot, got)
	}
}

// The speculative view is the explicit opt-in, and it says so in the response.
// Nothing about the default answer changes when the pending layer moves.
func TestCallViewIsCommittedUnlessAskedOtherwise(t *testing.T) {
	sdb, err := state.NewStateDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sdb.Close()

	var addr qcrypto.Address
	addr[31] = 0x5a
	committed := types.NewAccount(addr)
	committed.AddBalance(big.NewInt(5000))
	sdb.SetAccount(committed)
	if _, err := sdb.CommitWithHeight(12); err != nil {
		t.Fatal(err)
	}
	// Uncommitted: the block being assembled spends the whole balance.
	drained := types.NewAccount(addr)
	sdb.SetAccount(drained)

	h := makeEstimateHandler(sdb, nil, nil)
	estimate := func(view string) map[string]any {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"from": addr.String(), "value": "900", "view": view})
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(http.MethodPost, "/estimate", bytes.NewReader(body)))
		if rec.Code != http.StatusOK {
			t.Fatalf("view %q: status %d: %s", view, rec.Code, rec.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	def := estimate("")
	if def["view"] != "committed" {
		t.Fatalf("default view = %v, want committed", def["view"])
	}
	if h, ok := def["view_height"].(float64); !ok || uint64(h) != 12 {
		t.Fatalf("view_height = %v, want 12", def["view_height"])
	}
	if def["reverted"] == true {
		t.Fatalf("committed balance 5000 covers value 900 plus fee, yet the answer used the pending layer: %v", def)
	}

	spec := estimate("speculative")
	if spec["view"] != "speculative" {
		t.Fatalf("view = %v, want speculative", spec["view"])
	}
	if spec["reverted"] != true {
		t.Fatalf("speculative view ignored the drained pending balance: %v", spec)
	}

	body, _ := json.Marshal(map[string]any{"from": addr.String(), "view": "yesterday"})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/estimate", bytes.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown view accepted: %d %s", rec.Code, rec.Body.String())
	}
}

// A committed view that cannot be created is an error, never a read of live
// state wearing a committed label.
func TestSimulateCommittedFailsClosedOnAClosedDB(t *testing.T) {
	sdb, err := state.NewStateDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := sdb.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := sdb.SimulateCommitted(); err == nil {
		t.Fatal("SimulateCommitted returned a sandbox over a closed database")
	}
	if _, err := sdb.CommittedView(); err == nil {
		t.Fatal("CommittedView returned a view over a closed database")
	}
}
