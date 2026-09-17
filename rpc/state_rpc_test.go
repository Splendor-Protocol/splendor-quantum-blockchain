package rpc

import (
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

func TestStateProofReturnsDirectionsAndPreimage(t *testing.T) {
	sdb, err := state.NewStateDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sdb.Close()

	var target qcrypto.Address
	for i := 0; i < 3; i++ {
		var a qcrypto.Address
		a[31] = byte(0x30 + i)
		acc := types.NewAccount(a)
		acc.AddBalance(big.NewInt(int64(100 + i)))
		sdb.SetAccount(acc)
		if i == 1 {
			target = a
		}
	}
	if _, err := sdb.CommitWithHeight(4); err != nil {
		t.Fatal(err)
	}

	r := mux.NewRouter()
	RegisterStateRoutes(r, sdb, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/state/proof/"+target.String(), nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("proof status %d body %s", rec.Code, rec.Body.String())
	}

	var body struct {
		Address      string `json:"address"`
		StateRoot    string `json:"state_root"`
		BlockHeight  uint64 `json:"block_height"`
		LeafKey      string `json:"leaf_key"`
		LeafPreimage string `json:"leaf_preimage"`
		AccountHash  string `json:"account_hash"`
		Balance      string `json:"balance"`
		ProofSteps   []struct {
			Sibling string `json:"sibling"`
			Left    bool   `json:"left"`
		} `json:"proof_steps"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Balance != "101" {
		t.Fatalf("balance = %s, want 101", body.Balance)
	}
	if body.BlockHeight != 4 {
		t.Fatalf("height = %d, want 4", body.BlockHeight)
	}
	if body.LeafPreimage == "" || body.LeafKey == "" {
		t.Fatal("proof omitted leaf_key or leaf_preimage")
	}
	if len(body.ProofSteps) == 0 {
		t.Fatal("proof omitted sibling steps")
	}

	preimage, err := hex.DecodeString(body.LeafPreimage)
	if err != nil {
		t.Fatal(err)
	}
	leafHash, err := hex.DecodeString(body.AccountHash)
	if err != nil || len(leafHash) != 32 {
		t.Fatalf("account_hash: %v", err)
	}
	root, err := hex.DecodeString(body.StateRoot)
	if err != nil || len(root) != 32 {
		t.Fatalf("state_root: %v", err)
	}
	proof := &state.AccountProof{
		Address:      target,
		LeafKey:      body.LeafKey,
		LeafPreimage: preimage,
	}
	copy(proof.LeafHash[:], leafHash)
	copy(proof.Root[:], root)
	for _, step := range body.ProofSteps {
		sib, err := hex.DecodeString(step.Sibling)
		if err != nil || len(sib) != 32 {
			t.Fatalf("sibling: %v", err)
		}
		var h [32]byte
		copy(h[:], sib)
		proof.Steps = append(proof.Steps, state.MerkleStep{Sibling: h, Left: step.Left})
	}
	if err := state.VerifyAccountProof(proof); err != nil {
		t.Fatalf("HTTP proof failed VerifyAccountProof: %v", err)
	}

	accReq := httptest.NewRequest(http.MethodGet, "/state/account/"+target.String(), nil)
	accRec := httptest.NewRecorder()
	r.ServeHTTP(accRec, accReq)
	if accRec.Code != http.StatusOK {
		t.Fatalf("account status %d", accRec.Code)
	}
	var accBody struct {
		Balance     string `json:"balance"`
		StateRoot   string `json:"state_root"`
		BlockHeight uint64 `json:"block_height"`
	}
	if err := json.Unmarshal(accRec.Body.Bytes(), &accBody); err != nil {
		t.Fatal(err)
	}
	if accBody.Balance != body.Balance || accBody.StateRoot != body.StateRoot || accBody.BlockHeight != body.BlockHeight {
		t.Fatalf("account view %+v diverged from proof %+v", accBody, body)
	}
}
