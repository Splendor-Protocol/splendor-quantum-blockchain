package main

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/splendor-protocol/splendor-quantum-blockchain/consensus"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/block"
	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// bftForNextBlock reports whether the block this node is about to seal
// (head+1) falls under multi_validator.
//
// The decision was judged at the head itself, once, at boot. On the boot that
// minted a chain the head is 0, an activation at height 1 was never active at
// 0, and nothing re-evaluated it: the producer stayed on the single path for
// the life of the chain, sealing QC-less blocks that every follower refused
// once a second validator registered (mainnet from 5786).
func bftForNextBlock(s forks.Schedule, head uint64) bool {
	return s.Active(forks.MultiValidator, head+1)
}

// resolveTrustedProducerKey decides which genesis validator key this node
// authenticates blocks with.
//
// --validator-pubkey-hex wins on every node. It used to be honoured only on
// full nodes, so a validator joining a running chain trusted its OWN key —
// which seals nothing on that chain — and refused every block the producer
// pushed. A validator without the flag trusts its own key (the producer that
// minted the chain); a full node without it has nothing to trust until a
// pinned genesis block supplies a key.
func resolveTrustedProducerKey(flagHex string, isValidator bool, localPK []byte) ([]byte, error) {
	if flagHex != "" {
		pk, err := hex.DecodeString(strings.TrimPrefix(flagHex, "0x"))
		if err != nil {
			return nil, fmt.Errorf("invalid --validator-pubkey-hex: %w", err)
		}
		if _, err := qcrypto.PublicKeyFromBytes(pk); err != nil {
			return nil, fmt.Errorf("invalid --validator-pubkey-hex: %w", err)
		}
		return pk, nil
	}
	if isValidator && len(localPK) > 0 {
		return append([]byte(nil), localPK...), nil
	}
	return nil, nil
}

// genesisValidatorInfo names whoever sealed block 0, for the BFT engine's
// empty-registry fallback (consensus.Config.GenesisValidator).
//
// Block 0 is the record when the chain DB holds it. A node that joined by
// pull-sync never has it — sync starts at height 1 — so the trusted key,
// which is that validator's key by definition, stands in. ok is false when
// neither is available.
func genesisValidatorInfo(ex *block.Executor, trustedPK []byte) (consensus.ValidatorInfo, bool) {
	if g, err := ex.GetBlockByHeight(0); err == nil && g != nil && len(g.ValidatorPubKey) > 0 {
		if pub, perr := qcrypto.PublicKeyFromBytes(g.ValidatorPubKey); perr == nil &&
			qcrypto.DeriveAddress(pub) == g.Header.ValidatorAddr {
			return consensus.ValidatorInfo{
				Address:   g.Header.ValidatorAddr,
				PublicKey: append([]byte(nil), g.ValidatorPubKey...),
				IsActive:  true,
			}, true
		}
	}
	if len(trustedPK) > 0 {
		if pub, err := qcrypto.PublicKeyFromBytes(trustedPK); err == nil {
			return consensus.ValidatorInfo{
				Address:   qcrypto.DeriveAddress(pub),
				PublicKey: append([]byte(nil), trustedPK...),
				IsActive:  true,
			}, true
		}
	}
	return consensus.ValidatorInfo{}, false
}

// applyCommittedBlock is the BFT engine's commit callback.
//
// The pull-sync loop and the block push both race the engine for the same
// block: whichever lands first makes the other's ExecuteBlock fail on
// linkage. When the block that failed is already the head — same hash, so
// the same header and transactions — the commit still happened, and the
// engine must advance rather than report a failed commit and hold the height
// open.
func applyCommittedBlock(ex *block.Executor, b *types.Block) error {
	err := ex.ExecuteBlock(b)
	if err == nil {
		return nil
	}
	if head, herr := ex.LatestBlock(); herr == nil && head != nil && head.Hash == b.Hash {
		return nil
	}
	return err
}
