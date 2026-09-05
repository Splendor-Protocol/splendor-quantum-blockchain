package main

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/block"
	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	genesiscfg "github.com/splendor-protocol/splendor-quantum-blockchain/genesis"
)

// The genesis validator ANCHOR: the address of the key that sealed block 0.
//
// Under validator_gates_v1 the staking module needs one fact the chain state
// does not carry — which key the chain started under. That key is already the
// chain's root of trust (every block signature is verified against it), and
// the module uses it as its bootstrap authority: the initial admission admin,
// and the one key admitted without an entry having been written.
//
// It is handled exactly like the fork schedule, and for the same reason: both
// are facts about the network that a node must agree with everyone else on,
// both come out of the verified genesis document, and both have to survive a
// restart on a host that no longer has that document. So it is persisted in
// the state DB's node-local meta: namespace, next to meta:forks and outside
// the state root, and installed on the executor from there.
const metaGenesisValidatorKey = "genesis_validator"

// readStoredGenesisValidator returns the anchor persisted by a previous run.
func readStoredGenesisValidator(stateDB *state.StateDB) (qcrypto.Address, bool, error) {
	raw, ok, err := stateDB.GetMeta(metaGenesisValidatorKey)
	if err != nil {
		return qcrypto.Address{}, false, fmt.Errorf("read stored genesis validator: %w", err)
	}
	if !ok {
		return qcrypto.Address{}, false, nil
	}
	addr, err := qcrypto.AddressFromString(strings.TrimSpace(string(raw)))
	if err != nil {
		return qcrypto.Address{}, false, fmt.Errorf("stored genesis validator is corrupt: %w", err)
	}
	return addr, true, nil
}

// persistGenesisValidator records the anchor and installs it on the executor.
//
// Called from both genesis apply paths (local file and peer bootstrap) right
// after the fork schedule, so a node that learns its schedule has by then also
// learned the authority that schedule's rules are judged against. Re-recording
// the same address is a no-op; a DIFFERENT one is refused, because the address
// is a property of block 0 and a chain does not get a second genesis.
func persistGenesisValidator(stateDB *state.StateDB, executor *block.Executor, addr qcrypto.Address) error {
	if addr.IsZero() {
		return nil
	}
	stored, ok, err := readStoredGenesisValidator(stateDB)
	if err != nil {
		return err
	}
	if ok && stored != addr {
		return fmt.Errorf("this chain's genesis validator is %s but the document now says %s — "+
			"pointing a node at a different network's data directory would fork it", stored, addr)
	}
	if !ok {
		if err := stateDB.PutMeta(metaGenesisValidatorKey, []byte(addr.String())); err != nil {
			return fmt.Errorf("persist genesis validator: %w", err)
		}
	}
	executor.SetGenesisValidator(addr)
	return nil
}

// installGenesisValidator determines the anchor and installs it, or explains
// why the node must not start without one.
//
// Sources, strongest first:
//
//  1. The value a previous run persisted. Authoritative once recorded: a
//     peer-bootstrapped follower has no genesis file to re-read on restart.
//  2. Block 0's header, when this node has it. ValidatorAddr is inside the
//     header and therefore covered by --genesis-hash, so it is a fact about
//     the chain rather than a claim about it.
//  3. The --genesis document's validator_pubkey_hex, verified by the same
//     --genesis-doc-hash pin that guards its allocations.
//  4. --validator-pubkey-hex, which is documented as the trusted GENESIS
//     validator key and is what a full node already verifies every block
//     signature against.
//
// A node that finds none of these and whose chain schedules validator_gates_v1
// is refused rather than started: the anchor feeds a state transition
// (set_pass authorisation), so a node running without it would compute
// different state from its peers on any block containing one. That is a silent
// fork, and a startup error is the cheaper version of the same news.
//
// An EMPTY chain is exempt from that refusal: genesis has not been applied yet
// this run, and both apply paths persist the anchor as they go.
func installGenesisValidator(
	stateDB *state.StateDB,
	executor *block.Executor,
	genesisPath string,
	pinnedValidatorPK []byte,
	schedule forks.Schedule,
	chainEmpty bool,
) error {
	addr, source, err := resolveGenesisValidator(stateDB, executor, genesisPath, pinnedValidatorPK)
	if err != nil {
		return err
	}
	if !addr.IsZero() {
		if err := persistGenesisValidator(stateDB, executor, addr); err != nil {
			return err
		}
		log.Info().Str("genesis_validator", addr.String()).Str("source", source).
			Msg("Genesis validator anchor established")
		return nil
	}
	if _, gated := schedule.At(forks.ValidatorGatesV1); gated && !chainEmpty {
		return fmt.Errorf("this chain schedules %s but this node cannot tell which key sealed block 0. "+
			"That key is the validator registry's admission authority, so a node without it would "+
			"apply admission changes differently from its peers. Start with --genesis <path> "+
			"(or --validator-pubkey-hex <hex>) once, and it is recorded for every later start",
			forks.ValidatorGatesV1)
	}
	log.Debug().Msg("genesis validator anchor not yet known")
	return nil
}

// resolveGenesisValidator returns the anchor and where it came from, or the
// zero address when no source has it.
func resolveGenesisValidator(
	stateDB *state.StateDB,
	executor *block.Executor,
	genesisPath string,
	pinnedValidatorPK []byte,
) (qcrypto.Address, string, error) {
	if addr, ok, err := readStoredGenesisValidator(stateDB); err != nil {
		return qcrypto.Address{}, "", err
	} else if ok {
		return addr, "stored", nil
	}
	if b, err := executor.GetBlockByHeight(0); err == nil && b != nil && !b.Header.ValidatorAddr.IsZero() {
		return b.Header.ValidatorAddr, "block 0", nil
	}
	if genesisPath != "" {
		g, err := genesiscfg.LoadFile(genesisPath)
		if err == nil && g.ValidatorPubKeyHex != "" {
			addr, aerr := g.ValidatorAddress()
			if aerr != nil {
				return qcrypto.Address{}, "", fmt.Errorf("genesis %q has an unusable validator_pubkey_hex: %w", genesisPath, aerr)
			}
			return addr, "genesis document", nil
		}
	}
	if len(pinnedValidatorPK) > 0 {
		pub, err := qcrypto.PublicKeyFromBytes(pinnedValidatorPK)
		if err != nil {
			return qcrypto.Address{}, "", fmt.Errorf("pinned validator pubkey: %w", err)
		}
		return qcrypto.DeriveAddress(pub), "--validator-pubkey-hex", nil
	}
	return qcrypto.Address{}, "", nil
}

// pinnedGenesisValidatorKey decodes --validator-pubkey-hex, which names the
// GENESIS validator's key. It is deliberately NOT the validator's own wallet
// key: that is the key this node signs with, which is only the genesis
// validator's by coincidence on a single-producer chain, and anchoring to it
// would let a second producer declare itself the chain's authority.
func pinnedGenesisValidatorKey(hexKey string) ([]byte, error) {
	if hexKey == "" {
		return nil, nil
	}
	b, err := hex.DecodeString(strings.TrimPrefix(hexKey, "0x"))
	if err != nil {
		return nil, fmt.Errorf("invalid --validator-pubkey-hex: %w", err)
	}
	if _, err := qcrypto.PublicKeyFromBytes(b); err != nil {
		return nil, fmt.Errorf("invalid --validator-pubkey-hex: %w", err)
	}
	return b, nil
}
