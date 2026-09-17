package main

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/splendor-protocol/splendor-quantum-blockchain/consensus"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/block"
	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	genesiscfg "github.com/splendor-protocol/splendor-quantum-blockchain/genesis"
)

// Which path seals the next block — single or BFT — is no longer decided
// here at boot. consensus.Producer judges it per block against the live
// schedule, so a validator that boots below the multi_validator activation
// moves onto BFT when the height arrives instead of stopping there.

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

// validatorRole is what a --validator does with an EMPTY data directory:
// start the chain, or take one that already exists.
type validatorRole int

const (
	// roleMintGenesis — this node is the chain's founder. It applies the
	// genesis allocations and seals block 0 under its own key.
	roleMintGenesis validatorRole = iota

	// roleJoin — the chain exists somewhere else. Seal nothing; pull it from a
	// peer, and only produce once there is a head to produce on top of.
	roleJoin
)

// validatorStartRole decides which of the two a --validator on an empty chain
// is, and refuses the configuration that can only be a fork.
//
// A validator whose --genesis named a different key used to exit at start-up.
// That is why the documented way to add a validator to a running chain was to
// start it as a full node and restart it as a validator once it had caught up:
// the second process was a consequence of this check, not of the problem. With
// a peer to take the chain from, the node joins instead — same data directory,
// one launch.
//
// The refusal stays for the case that really is unrecoverable: a document
// naming somebody else and no peer to sync from. A node there can do nothing
// but mint a second chain under the same document, which is the failure the
// original check existed to prevent.
func validatorStartRole(doc *genesiscfg.Genesis, localPK []byte, peers []string) (validatorRole, error) {
	if doc == nil {
		// No document to compare against. With a peer, the chain is the peer's;
		// without one this is the solo development node that mints its own.
		if len(peers) > 0 {
			return roleJoin, nil
		}
		return roleMintGenesis, nil
	}
	docPub, err := doc.ValidatorPublicKey()
	if err != nil {
		return 0, fmt.Errorf("genesis document has an unusable validator_pubkey_hex: %w", err)
	}
	if bytes.Equal(docPub.Bytes(), localPK) {
		return roleMintGenesis, nil
	}
	if len(peers) == 0 {
		return 0, fmt.Errorf(
			"this node's wallet is %s but the genesis document declares validator %s, "+
				"and no --peer was given: minting block 0 here would start a SECOND chain "+
				"under the same document. Pass --peer <url of a node on that chain> to join it",
			qcrypto.DeriveAddress(mustPublic(localPK)), qcrypto.DeriveAddress(docPub))
	}
	return roleJoin, nil
}

// mustPublic is only used to render an address in the error above; an
// unparseable local key cannot reach it (the wallet loaded it).
func mustPublic(pk []byte) *qcrypto.PublicKey {
	pub, err := qcrypto.PublicKeyFromBytes(pk)
	if err != nil {
		return &qcrypto.PublicKey{}
	}
	return pub
}


// checkJoinerValidatorPubkey guards the JOIN-REJECT footgun: a --validator
// without --validator-pubkey-hex trusts its own wallet as the genesis sealer.
// Under validators_admission_v1 with set size < 2 that makes the joiner reject
// the real producer's blocks.
//
// Hard error when we already know this process is joining (roleJoin / genesis
// document names somebody else). Soft warning when a validator lists --peer
// but omitted the flag — the genesis producer may also list peers for mesh,
// so that case cannot be a hard error.
func checkJoinerValidatorPubkey(flagHex string, isValidator bool, joining bool, peers []string, doc *genesiscfg.Genesis, localPK []byte) (warn string, err error) {
	if !isValidator || flagHex != "" {
		return "", nil
	}
	if doc != nil && len(localPK) > 0 {
		if docPub, derr := doc.ValidatorPublicKey(); derr == nil && !bytes.Equal(docPub.Bytes(), localPK) {
			return "", fmt.Errorf(
				"--validator with a genesis document naming %s but this wallet is %s requires --validator-pubkey-hex <genesis ML-DSA-65 hex>; "+
					"without it this node treats its own key as genesis and rejects the real producer under validators_admission_v1",
				qcrypto.DeriveAddress(docPub), qcrypto.DeriveAddress(mustPublic(localPK)))
		}
	}
	if joining {
		return "", fmt.Errorf(
			"joining --validator requires --validator-pubkey-hex <genesis ML-DSA-65 public key hex>; "+
				"omitting it makes this node trust its own wallet as the genesis sealer and reject the real producer")
	}
	if len(peers) > 0 {
		return "--validator with --peer but no --validator-pubkey-hex: if this node is joining (not the genesis sealer), " +
			"it will treat its own wallet as genesis and reject the real producer under validators_admission_v1 with set size < 2. " +
			"Pass the genesis ML-DSA-65 hex (same as scripts/validator-join --validator-pubkey-hex).", nil
	}
	return "", nil
}

// joiningValidatorReady gates proposals on a validator that was pointed at
// somebody else's chain.
//
// syncer.IsSynced reports "synced" while no peer height is known yet, which is
// the right answer for the chain's own producer and the wrong one here: a
// joiner would pass the gate in the first second of its life, before any pull
// had run, and seal its own block 1 on top of genesis. Requiring a stored head
// first means the node has taken at least one block from the network before it
// is allowed to add one.
func joiningValidatorReady(ex *block.Executor, synced func() bool) func() bool {
	return func() bool {
		head, err := ex.LatestBlock()
		if err != nil || head == nil {
			return false
		}
		return synced == nil || synced()
	}
}

// applyCommittedBlock is the BFT engine's commit callback.
//
// The pull-sync loop and the block push both race the engine for the same
// block: whichever lands first makes the other's ExecuteBlock fail on
// linkage. A duplicate is success only when the block is fully applied —
// stored head hash, applied height, and committed state root all match.
// Head-hash alone is not enough: ExecuteBlock writes the head before
// CommitWithHeight, so a failed state write already satisfies the old check.
func applyCommittedBlock(ex *block.Executor, b *types.Block) error {
	err := ex.ExecuteBlock(b)
	if err == nil {
		return nil
	}
	if ex.BlockFullyApplied(b) {
		return nil
	}
	return err
}

// applyStoresReady stops produce (and, with BFT CanProgress, vote) after a
// partial persist. It tries deterministic reconciliation first so a recoverable
// split does not need a process restart.
func applyStoresReady(ex *block.Executor, ready func() bool) func() bool {
	return func() bool {
		if !ex.CanProgress() {
			if err := ex.RecoverPartialApply(); err != nil || !ex.CanProgress() {
				return false
			}
		}
		return ready == nil || ready()
	}
}

// applyVoteOnly wraps Ready so the node still votes on peer proposals but never
// proposes. Used by --vote-only for ordered recovery after a split vote-journal
// unlock at set≥3, where a follower that races a divergent next-height proposal
// leaves the scheduled leader unable to solo-seal.
func applyVoteOnly(ready func() bool, voteOnly bool) func() bool {
	if !voteOnly {
		return ready
	}
	return func() bool { return false }
}

