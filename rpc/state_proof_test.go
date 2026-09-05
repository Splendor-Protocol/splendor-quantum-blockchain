package rpc

import (
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

type proofResponse struct {
	ProofAvailable bool     `json:"proof_available"`
	StateRoot      string   `json:"state_root"`
	AccountHash    string   `json:"account_hash"`
	Proof          []string `json:"proof"`
	ProofPath      []struct {
		Sibling  string `json:"sibling"`
		Position string `json:"position"`
	} `json:"proof_path"`
	ProofDepth int    `json:"proof_depth"`
	LeafIndex  int    `json:"leaf_index"`
	LeafCount  int    `json:"leaf_count"`
	Verify     string `json:"verify_instruction"`
}

func hash32(t *testing.T, h string) [32]byte {
	t.Helper()
	b, err := hex.DecodeString(h)
	if err != nil || len(b) != 32 {
		t.Fatalf("bad 32-byte hex %q: %v", h, err)
	}
	var out [32]byte
	copy(out[:], b)
	return out
}

// The proof endpoint must give a verifier the side of every sibling: the leaf
// index alone does not determine them once an odd node has been carried up.
// Even and odd leaf counts, every leaf, and no executor (the handler must not
// dereference a nil one).
func TestMerkleProofHandlerGivesSiblingSides(t *testing.T) {
	for _, n := range []int{1, 2, 3, 4, 5, 6, 7} {
		sdb, err := state.NewStateDB(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		var addrs []qcrypto.Address
		for i := 0; i < n; i++ {
			var a qcrypto.Address
			a[0] = byte(i + 1)
			acc := types.NewAccount(a)
			acc.AddBalance(big.NewInt(int64(i + 1)))
			sdb.SetAccount(acc)
			addrs = append(addrs, a)
		}
		root, err := sdb.Commit()
		if err != nil {
			t.Fatal(err)
		}
		h := merkleProofHandler(sdb, nil)

		for _, a := range addrs {
			req := httptest.NewRequest(http.MethodGet, "/state/proof/"+a.String(), nil)
			req = mux.SetURLVars(req, map[string]string{"address": a.String()})
			rec := httptest.NewRecorder()
			h(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("n=%d: status %d: %s", n, rec.Code, rec.Body.String())
			}
			var resp proofResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("n=%d: decode: %v", n, err)
			}
			if !resp.ProofAvailable {
				t.Fatalf("n=%d: proof unavailable: %s", n, rec.Body.String())
			}
			if resp.StateRoot != hex.EncodeToString(root[:]) {
				t.Fatalf("n=%d: state_root = %s, want the committed root", n, resp.StateRoot)
			}
			if resp.LeafCount != n || resp.LeafIndex < 0 || resp.LeafIndex >= n {
				t.Fatalf("n=%d: leaf_index/leaf_count = %d/%d", n, resp.LeafIndex, resp.LeafCount)
			}
			if resp.ProofDepth != len(resp.ProofPath) || len(resp.Proof) != len(resp.ProofPath) {
				t.Fatalf("n=%d: depth %d, proof %d, proof_path %d disagree", n, resp.ProofDepth, len(resp.Proof), len(resp.ProofPath))
			}
			path := make([]state.ProofStep, len(resp.ProofPath))
			for i, st := range resp.ProofPath {
				switch st.Position {
				case "left":
					path[i].SiblingOnLeft = true
				case "right":
				default:
					t.Fatalf("n=%d: step %d position %q", n, i, st.Position)
				}
				path[i].Sibling = hash32(t, st.Sibling)
				if resp.Proof[i] != st.Sibling {
					t.Fatalf("n=%d: proof[%d] and proof_path[%d] name different siblings", n, i, i)
				}
			}
			if !state.VerifyMerkleProof(root, hash32(t, resp.AccountHash), path) {
				t.Fatalf("n=%d leaf %d: the served path does not reach the root", n, resp.LeafIndex)
			}
			if !strings.Contains(resp.Verify, "proof_path") || !strings.Contains(resp.Verify, "position") {
				t.Fatalf("verify_instruction should point at proof_path positions: %q", resp.Verify)
			}
		}
		sdb.Close()
	}
}
