// Package rpc — state proof and history query routes.
//
// GET /state/root               — current state root + block height
// GET /state/account/{address}  — account state (balance, nonce, public key)
// GET /state/proof/{address}    — Merkle inclusion proof for an account
//
// Account, proof, root and height come from one committed StateDB view so a
// verifier never mixes pending overlay with a later head.
package rpc

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gorilla/mux"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/block"
	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// RegisterStateRoutes mounts the state proof endpoints on r.
//
// guard may be nil in tests. /state/proof walks and sorts the committed leaf
// set, so production must pass Server.SimGuard() — the same meter as /call.
func RegisterStateRoutes(r *mux.Router, stateDB *state.StateDB, executor *block.Executor, guard *ExecGuard) {
	r.HandleFunc("/state/root", stateRootHandler(stateDB, executor)).Methods(http.MethodGet)
	r.HandleFunc("/state/account/{address}", accountStateHandler(stateDB, executor)).Methods(http.MethodGet)
	r.Handle("/state/proof/{address}", guard.Wrap(merkleProofHandler(stateDB, executor))).Methods(http.MethodGet)
}

func writeStateJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

func committedHead(stateDB *state.StateDB, executor *block.Executor) (root [32]byte, height uint64, blockHash string) {
	root, _ = stateDB.StateRoot()
	if h, ok := stateDB.AppliedHeight(); ok {
		height = h
	}
	if executor != nil {
		if latest, latErr := executor.LatestBlock(); latErr == nil && latest != nil && latest.Header.Height == height {
			blockHash = hex.EncodeToString(latest.Hash[:])
		}
	}
	return root, height, blockHash
}

func decodeCommittedAccount(preimage []byte) (*types.Account, error) {
	var acc types.Account
	if err := json.Unmarshal(preimage, &acc); err != nil {
		return nil, err
	}
	if acc.Balance == nil {
		acc.Balance = types.NewAccount(acc.Address).Balance
	}
	return &acc, nil
}

// stateRootHandler returns the current committed state root and block context.
func stateRootHandler(stateDB *state.StateDB, executor *block.Executor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		root, height, blockHash := committedHead(stateDB, executor)
		resp := map[string]interface{}{
			"state_root":   hex.EncodeToString(root[:]),
			"block_height": height,
		}
		if blockHash != "" {
			resp["block_hash"] = blockHash
		}
		if executor != nil {
			if latest, latErr := executor.LatestBlock(); latErr == nil && latest != nil {
				resp["chain_id"] = latest.Header.ChainID
			}
		}
		writeStateJSON(w, http.StatusOK, resp)
	}
}

func accountStateHandler(stateDB *state.StateDB, executor *block.Executor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		addrStr := mux.Vars(r)["address"]
		addr, err := qcrypto.AddressFromString(addrStr)
		if err != nil {
			writeStateJSON(w, http.StatusBadRequest, map[string]string{
				"error": "invalid address: " + err.Error(),
			})
			return
		}

		proof, err := stateDB.AccountProofContext(r.Context(), addr)
		if err != nil {
			writeStateJSON(w, http.StatusNotFound, map[string]string{
				"error": fmt.Sprintf("account %s not found: %s", addrStr, err.Error()),
			})
			return
		}
		acc, err := decodeCommittedAccount(proof.LeafPreimage)
		if err != nil {
			writeStateJSON(w, http.StatusInternalServerError, map[string]string{
				"error": "committed account preimage: " + err.Error(),
			})
			return
		}

		_, _, blockHash := committedHead(stateDB, executor)
		resp := map[string]interface{}{
			"address":        addrStr,
			"balance":        acc.Balance.String(),
			"nonce":          acc.Nonce,
			"has_public_key": acc.HasPublicKey(),
			"state_root":     hex.EncodeToString(proof.Root[:]),
			"block_height":   proof.Height,
		}
		if blockHash != "" {
			resp["block_hash"] = blockHash
		}
		if acc.HasPublicKey() {
			resp["public_key_hex"] = hex.EncodeToString(acc.PublicKey)
			if pk, kerr := qcrypto.PublicKeyFromBytes(acc.PublicKey); kerr == nil {
				resp["address_verified"] = qcrypto.DeriveAddress(pk) == addr
			}
		}
		writeStateJSON(w, http.StatusOK, resp)
	}
}

func merkleProofHandler(stateDB *state.StateDB, executor *block.Executor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		addrStr := mux.Vars(r)["address"]
		addr, err := qcrypto.AddressFromString(addrStr)
		if err != nil {
			writeStateJSON(w, http.StatusBadRequest, map[string]string{
				"error": "invalid address: " + err.Error(),
			})
			return
		}

		proof, err := stateDB.AccountProofContext(r.Context(), addr)
		if err != nil {
			if r.Context().Err() != nil {
				writeStateJSON(w, http.StatusRequestTimeout, map[string]string{
					"error": "proof generation cancelled",
				})
				return
			}
			writeStateJSON(w, http.StatusNotFound, map[string]string{
				"error": fmt.Sprintf("account %s not found", addrStr),
			})
			return
		}
		if vErr := state.VerifyAccountProof(proof); vErr != nil {
			writeStateJSON(w, http.StatusInternalServerError, map[string]string{
				"error": "proof failed local verify: " + vErr.Error(),
			})
			return
		}

		acc, err := decodeCommittedAccount(proof.LeafPreimage)
		if err != nil {
			writeStateJSON(w, http.StatusInternalServerError, map[string]string{
				"error": "committed account preimage: " + err.Error(),
			})
			return
		}
		_, _, blockHash := committedHead(stateDB, executor)

		steps := make([]map[string]interface{}, len(proof.Steps))
		for i, step := range proof.Steps {
			steps[i] = map[string]interface{}{
				"sibling": hex.EncodeToString(step.Sibling[:]),
				"left":    step.Left,
			}
		}

		resp := map[string]interface{}{
			"address":         addrStr,
			"state_root":      hex.EncodeToString(proof.Root[:]),
			"block_height":    proof.Height,
			"block_hash":      blockHash,
			"balance":         acc.Balance.String(),
			"nonce":           acc.Nonce,
			"proof_available": true,
			"leaf_key":        proof.LeafKey,
			"leaf_preimage":   hex.EncodeToString(proof.LeafPreimage),
			"account_hash":    hex.EncodeToString(proof.LeafHash[:]),
			"proof_steps":     steps,
			"proof_depth":     len(steps),
			"verify_instruction": "1. leafHash = leafDigest(leaf_key, SHA3-256(leaf_preimage)). " +
				"2. For each proof_steps[i]: if left then node=internalHash(sibling,node) else node=internalHash(node,sibling). " +
				"3. Final node must equal state_root. Odd carried-up nodes have no step.",
		}
		writeStateJSON(w, http.StatusOK, resp)
	}
}
