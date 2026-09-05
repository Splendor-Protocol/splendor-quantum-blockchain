// Package rpc — state proof and history query routes.
//
// GET /state/root               — current state root + block height
// GET /state/account/{address}  — account state (balance, nonce, public key)
// GET /state/proof/{address}    — Merkle inclusion proof for an account
//
// These routes implement the "Sparse Merkle proof API" listed as not-built
// in the README verification brief. The proof endpoint returns the state root
// committed in the latest block header plus the account leaf, allowing external
// verifiers to confirm account data without downloading the full state.
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
)

// RegisterStateRoutes mounts the state proof endpoints on r.
func RegisterStateRoutes(r *mux.Router, stateDB *state.StateDB, executor *block.Executor) {
	r.HandleFunc("/state/root", stateRootHandler(stateDB, executor)).Methods(http.MethodGet)
	r.HandleFunc("/state/account/{address}", accountStateHandler(stateDB, executor)).Methods(http.MethodGet)
	r.HandleFunc("/state/proof/{address}", merkleProofHandler(stateDB, executor)).Methods(http.MethodGet)
}

func writeStateJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

// stateRootHandler returns the current committed state root and block context.
//
//	GET /state/root
//	{
//	  "state_root":   "<hex>",
//	  "block_height": N,
//	  "block_hash":   "<hex>",
//	  "chain_id":     "splendor-quantum-1"
//	}
func stateRootHandler(stateDB *state.StateDB, executor *block.Executor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		root, err := stateDB.StateRoot()
		if err != nil {
			writeStateJSON(w, http.StatusInternalServerError, map[string]string{
				"error": "state root unavailable: " + err.Error(),
			})
			return
		}

		resp := map[string]interface{}{
			"state_root":   hex.EncodeToString(root[:]),
			"block_height": uint64(0),
		}

		if latest, latErr := executor.LatestBlock(); latErr == nil && latest != nil {
			resp["block_height"] = latest.Header.Height
			resp["block_hash"] = hex.EncodeToString(latest.Hash[:])
			resp["chain_id"] = latest.Header.ChainID
			// Use the state root committed in the block header, which is the
			// canonical value rather than the live (possibly pending) state root.
			resp["state_root"] = hex.EncodeToString(latest.Header.StateRoot[:])
		}

		writeStateJSON(w, http.StatusOK, resp)
	}
}

// accountStateHandler returns the full account state for a given address.
//
//	GET /state/account/SQ:abc123
//	{
//	  "address":        "SQ:...",
//	  "balance":        "26000000000000000000000000000",
//	  "nonce":          5,
//	  "has_public_key": true,
//	  "public_key_hex": "<hex>",
//	  "state_root":     "<hex>",
//	  "block_height":   N
//	}
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

		acc, err := stateDB.GetAccount(addr)
		if err != nil {
			writeStateJSON(w, http.StatusNotFound, map[string]string{
				"error": fmt.Sprintf("account %s not found: %s", addrStr, err.Error()),
			})
			return
		}

		root, _ := stateDB.StateRoot()
		var blockHeight uint64
		if latest, latErr := executor.LatestBlock(); latErr == nil && latest != nil {
			blockHeight = latest.Header.Height
			root = latest.Header.StateRoot
		}

		resp := map[string]interface{}{
			"address":        addrStr,
			"balance":        acc.Balance.String(),
			"nonce":          acc.Nonce,
			"has_public_key": acc.HasPublicKey(),
			"state_root":     hex.EncodeToString(root[:]),
			"block_height":   blockHeight,
		}

		if acc.HasPublicKey() {
			resp["public_key_hex"] = hex.EncodeToString(acc.PublicKey)
			// Sanity check: rederive the address and confirm it matches.
			if pk, kerr := qcrypto.PublicKeyFromBytes(acc.PublicKey); kerr == nil {
				resp["address_verified"] = qcrypto.DeriveAddress(pk) == addr
			}
		}

		writeStateJSON(w, http.StatusOK, resp)
	}
}

// merkleProofHandler returns Merkle inclusion evidence for an account.
//
// The state root in every block header is a binary SHA3-256 Merkle commitment
// over all accounts, contracts, and X402 nonces. This endpoint returns that
// root (from the latest committed block) together with the account's data, so
// an external verifier can confirm the account exists without the full state.
//
// Full Merkle path proofs (sibling-hash list) require a MerkleProof() method
// on StateDB; if not available the endpoint falls back to returning the root
// and account data only.
//
//	GET /state/proof/SQ:abc123
//	{
//	  "address":         "SQ:...",
//	  "state_root":      "<hex>",
//	  "block_height":    N,
//	  "block_hash":      "<hex>",
//	  "balance":         "...",
//	  "nonce":           N,
//	  "proof_available": false,
//	  "note":            "..."
//	}
//
// With a proof, `proof_path` lists one {sibling, position} per level, where
// position is the side the SIBLING sits on, plus `leaf_index` and
// `leaf_count`; `proof` is the same siblings without sides.
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

		acc, err := stateDB.GetAccount(addr)
		if err != nil {
			writeStateJSON(w, http.StatusNotFound, map[string]string{
				"error": fmt.Sprintf("account %s not found", addrStr),
			})
			return
		}

		// Get the state root committed in the latest block header. A node
		// without an executor (tests, tools) falls back to the live root.
		var stateRoot [32]byte
		var blockHeight uint64
		var blockHash string
		haveHeader := false
		if executor != nil {
			if latest, latErr := executor.LatestBlock(); latErr == nil && latest != nil {
				stateRoot = latest.Header.StateRoot
				blockHeight = latest.Header.Height
				blockHash = hex.EncodeToString(latest.Hash[:])
				haveHeader = true
			}
		}
		if !haveHeader {
			stateRoot, _ = stateDB.StateRoot()
		}

		// Full Merkle path proof, with the side of every sibling. The side is
		// NOT a function of the leaf index: an odd node is carried up with no
		// sibling, so the path can be shorter than the depth and index parity
		// stops describing it (see StateDB.MerkleProofPath).
		leafHash, path, leafIndex, leafCount, proofErr := stateDB.MerkleProofPath(addr)

		resp := map[string]interface{}{
			"address":      addrStr,
			"state_root":   hex.EncodeToString(stateRoot[:]),
			"block_height": blockHeight,
			"block_hash":   blockHash,
			"balance":      acc.Balance.String(),
			"nonce":        acc.Nonce,
		}

		if proofErr == nil {
			proofHex := make([]string, len(path))
			steps := make([]map[string]interface{}, len(path))
			for i, st := range path {
				proofHex[i] = hex.EncodeToString(st.Sibling[:])
				position := "right"
				if st.SiblingOnLeft {
					position = "left"
				}
				steps[i] = map[string]interface{}{"sibling": proofHex[i], "position": position}
			}
			resp["proof_available"] = true
			resp["account_hash"] = hex.EncodeToString(leafHash[:])
			resp["proof"] = proofHex // sibling digests only, kept for older clients
			resp["proof_path"] = steps
			resp["proof_depth"] = len(path)
			resp["leaf_index"] = leafIndex
			resp["leaf_count"] = leafCount
			resp["verify_instruction"] = fmt.Sprintf(
				"1. node = leafDigest(\"acc:%s\", SHA3-256(account_json)); it must equal account_hash. "+
					"2. For each step of proof_path in order: node = internalHash(sibling, node) when position is \"left\", "+
					"node = internalHash(node, sibling) when it is \"right\". "+
					"3. node must equal state_root. "+
					"proof_path can be shorter than the tree depth (an odd node is carried up without a sibling), "+
					"so the parity of leaf_index does not give the sides — use position.",
				addrStr)
		} else {
			resp["proof_available"] = false
			resp["note"] = fmt.Sprintf("Proof generation failed: %s", proofErr.Error())
		}

		writeStateJSON(w, http.StatusOK, resp)
	}
}
