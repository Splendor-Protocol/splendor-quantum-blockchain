// Package rpc — state proof and history query routes.
//
// GET /state/root               — current state root + block height
// GET /state/account/{address}  — account state (balance, nonce, public key)
// GET /state/proof/{address}    — Merkle inclusion proof for an account
//
// Every answer here is one block's. The root and the applied height are
// captured together, the account bytes come from a LevelDB snapshot pinned at
// that same moment, and block_hash is the block AT that height whose header
// declares that root — not whatever the head happens to be by the time the
// response is written. A caller that verifies a proof against state_root can
// therefore take block_hash as naming the block the root belongs to, the way
// eth_getProof's proof and state root belong to one block tag.
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

// blockHashForHead resolves the hash of the block a committed view belongs to:
// the block at the view's EXACT height whose header declares the view's root.
// Anything else returns "" — a hash that names a different block is worse than
// no hash, because a verifier has no way to tell.
//
// It used to read executor.LatestBlock() after the fact, which paired the head
// at response-writing time with a root captured earlier; a commit landing in
// between was invisible in the response.
func blockHashForHead(executor *block.Executor, head state.CommittedHead) string {
	if executor == nil || !head.HeightKnown {
		return ""
	}
	blk, err := executor.GetBlockByHeight(head.Height)
	if err != nil || blk == nil {
		return ""
	}
	if blk.Header.StateRoot != head.Root {
		return ""
	}
	return hex.EncodeToString(blk.Hash[:])
}

// writeViewError reports a committed view that could not be created. It is a
// 503, not an empty answer: the node cannot currently speak for any block.
func writeViewError(w http.ResponseWriter, err error) {
	writeStateJSON(w, http.StatusServiceUnavailable, map[string]string{
		"error": "committed state view unavailable: " + err.Error(),
	})
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
		head, err := stateDB.CommittedHeadView()
		if err != nil {
			writeViewError(w, err)
			return
		}
		resp := map[string]interface{}{
			"state_root":   hex.EncodeToString(head.Root[:]),
			"block_height": head.Height,
		}
		if blockHash := blockHashForHead(executor, head); blockHash != "" {
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

		// One pinned view for the whole answer. GetAccount, which this used
		// to call, reads the dirty overlay first — the block being assembled
		// or the proposal being validated — and the response then wore the
		// committed root and applied height regardless, so a proposal that
		// DiscardPending threw away was served as chain state.
		//
		// A point read, as before this route existed alongside /state/proof.
		// Building the proof here would walk and hash every committed leaf
		// under the state read lock for an unauthenticated, unguarded route,
		// and each in-flight request would hold block commit off for the
		// length of that walk. Proofs come from /state/proof, behind the
		// simulation guard.
		view, err := stateDB.CommittedView()
		if err != nil {
			writeViewError(w, err)
			return
		}
		defer view.Release()

		acc, err := view.Account(addr)
		if err != nil {
			writeStateJSON(w, http.StatusNotFound, map[string]string{
				"error": fmt.Sprintf("account %s not found: %s", addrStr, err.Error()),
			})
			return
		}

		resp := map[string]interface{}{
			"address":        addrStr,
			"balance":        acc.Balance.String(),
			"nonce":          acc.Nonce,
			"has_public_key": acc.HasPublicKey(),
			"state_root":     hex.EncodeToString(view.Root[:]),
			"block_height":   view.Height,
		}
		if blockHash := blockHashForHead(executor, view.CommittedHead); blockHash != "" {
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

		proof, err := stateDB.CommittedAccountProof(r.Context(), addr)
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
		// The block the proof's own root and height name — resolved from the
		// proof, never from the head as it stands now.
		blockHash := blockHashForHead(executor, proof.Head())

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
