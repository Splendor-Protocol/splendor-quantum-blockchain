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

		if executor != nil {
			if latest, latErr := executor.LatestBlock(); latErr == nil && latest != nil {
				resp["block_height"] = latest.Header.Height
				resp["block_hash"] = hex.EncodeToString(latest.Hash[:])
				resp["chain_id"] = latest.Header.ChainID
				// Use the state root committed in the block header, which is the
				// canonical value rather than the live (possibly pending) state root.
				resp["state_root"] = hex.EncodeToString(latest.Header.StateRoot[:])
			}
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
		if executor != nil {
			if latest, latErr := executor.LatestBlock(); latErr == nil && latest != nil {
				blockHeight = latest.Header.Height
				root = latest.Header.StateRoot
			}
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

		// Get the state root committed in the latest block header.
		var stateRoot [32]byte
		var blockHeight uint64
		var blockHash string
		if executor != nil {
			if latest, latErr := executor.LatestBlock(); latErr == nil && latest != nil {
				stateRoot = latest.Header.StateRoot
				blockHeight = latest.Header.Height
				blockHash = hex.EncodeToString(latest.Hash[:])
			} else {
				stateRoot, _ = stateDB.StateRoot()
			}
		} else {
			stateRoot, _ = stateDB.StateRoot()
		}

		leafHash, siblings, proofErr := stateDB.MerkleProofContext(r.Context(), addr)
		if proofErr != nil && r.Context().Err() != nil {
			writeStateJSON(w, http.StatusRequestTimeout, map[string]string{
				"error": "proof generation cancelled",
			})
			return
		}

		resp := map[string]interface{}{
			"address":      addrStr,
			"state_root":   hex.EncodeToString(stateRoot[:]),
			"block_height": blockHeight,
			"block_hash":   blockHash,
			"balance":      acc.Balance.String(),
			"nonce":        acc.Nonce,
		}

		if proofErr == nil {
			proofHex := make([]string, len(siblings))
			for i, s := range siblings {
				proofHex[i] = hex.EncodeToString(s[:])
			}
			resp["proof_available"] = true
			resp["account_hash"] = hex.EncodeToString(leafHash[:])
			resp["proof"] = proofHex
			resp["proof_depth"] = len(proofHex)
			resp["verify_instruction"] = fmt.Sprintf(
				"1. leafHash = leafDigest(\"acc:%s\", SHA3-256(account_json)). "+
					"2. For each sibling at index i: if (leafIndex >> i) & 1 == 0 "+
					"then node = internalHash(node, sibling), else node = internalHash(sibling, node). "+
					"3. Final node must equal state_root.",
				addrStr)
		} else {
			resp["proof_available"] = false
			resp["note"] = fmt.Sprintf("Proof generation failed: %s", proofErr.Error())
		}

		writeStateJSON(w, http.StatusOK, resp)
	}
}
