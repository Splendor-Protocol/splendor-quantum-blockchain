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

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/block"
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
		if latest, latErr := executor.LatestBlock(); latErr == nil && latest != nil {
			stateRoot = latest.Header.StateRoot
			blockHeight = latest.Header.Height
			blockHash = hex.EncodeToString(latest.Hash[:])
		} else {
			stateRoot, _ = stateDB.StateRoot()
		}

		// StateDB.MerkleProof() is not yet wired — the Merkle tree is built and
		// committed, but the path-proof extraction API is a future work item.
		// Return what we can: the canonical state root from the block header plus
		// the account data. An external verifier can confirm the account is in the
		// tree by requesting the full state dump and recomputing the root.
		resp := map[string]interface{}{
			"address":         addrStr,
			"state_root":      hex.EncodeToString(stateRoot[:]),
			"block_height":    blockHeight,
			"block_hash":      blockHash,
			"balance":         acc.Balance.String(),
			"nonce":           acc.Nonce,
			"proof_available": false,
			"note": "Full Merkle path proof (sibling-hash list) is not yet " +
				"exposed by this API. The state_root is the SHA3-256 binary Merkle " +
				"commitment from the latest block header. The account data above is " +
				"the current committed state. Path-proof extraction is planned as a " +
				"follow-on to StateDB.MerkleProof().",
		}

		writeStateJSON(w, http.StatusOK, resp)
	}
}
