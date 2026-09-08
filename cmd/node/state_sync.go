package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/block"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	genesiscfg "github.com/splendor-protocol/splendor-quantum-blockchain/genesis"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network"
)

// validateStateSyncFlags refuses --state-sync configurations that cannot work,
// at start-up, naming the flag that fixes each.
func validateStateSyncFlags(isValidator bool, trustedPK []byte, genesisPin *[32]byte, genesisPath string, peers []string) error {
	if isValidator {
		return errors.New("--state-sync cannot be combined with --validator: a validator on an empty chain seals " +
			"its own block before the snapshot lands. Join as a follower first, then restart with --validator " +
			"on the synced data dir")
	}
	if len(trustedPK) == 0 && genesisPin == nil {
		return errors.New("--state-sync needs --validator-pubkey-hex or --genesis-hash: the snapshot's head block " +
			"is authenticated against the genesis validator key, and nothing else anchors an empty node")
	}
	if genesisPath == "" && len(peers) == 0 {
		return errors.New("--state-sync needs a genesis document for the fork schedule and minted supply: pass " +
			"--genesis <path>, or --peer plus --genesis-doc-hash so it is taken from the peer")
	}
	return nil
}

// prepareStateSyncGenesis does what applyLocalGenesis does EXCEPT credit the
// allocations: the fork schedule and chain id are adopted from the pinned
// document and the minted supply is recorded, so the state stays fresh for
// the import and later blocks are gated and supply-checked exactly as on the
// peer. The balances themselves arrive with the snapshot.
func prepareStateSyncGenesis(g *genesiscfg.Genesis, pin *[32]byte, source string, stateDB *state.StateDB, executor *block.Executor) error {
	if err := verifyGenesisDocument(g, pin, source); err != nil {
		return err
	}
	if err := persistForkSchedule(stateDB, executor, g.Forks); err != nil {
		return err
	}
	minted := g.SumAllocations()
	if err := stateDB.PutMeta(genesiscfg.MetaNativeMinted, []byte(minted.String())); err != nil {
		return fmt.Errorf("persist native minted supply: %w", err)
	}
	log.Info().Str("source", source).Str("genesis_doc_hash", g.HashHex()).Str("minted", minted.String()).
		Msg("state sync: fork schedule and minted supply taken from the genesis document; allocations left to the snapshot")
	return nil
}

// fetchPeerGenesis reads a peer's /genesis. It refuses before the request is
// made when nothing pins the document: no downstream check can establish that
// an arbitrary peer's genesis is this network's.
func fetchPeerGenesis(peerURL string, policy genesisPolicy) (*genesiscfg.Genesis, error) {
	if policy.docHash == nil && !policy.trustPeer {
		return nil, fmt.Errorf("%w: refusing to credit balances from %s because nothing says what that document should be. "+
			"Either copy the network's genesis.json to this host and pass --genesis <path>, "+
			"or pin it with --genesis-doc-hash <hex> (print the pin with 'sqc-node genesis-hash <path>'), "+
			"or pass --trust-peer-genesis if you control that peer and accept it can mint balances here",
			errGenesisUnpinned, peerURL)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(peerURL + "/genesis")
	if err != nil {
		return nil, fmt.Errorf("fetch /genesis from peer: %w", err)
	}
	defer resp.Body.Close()

	// Returning nil here would count as success and stop the caller trying the
	// remaining peers, leaving the node unfunded on the word of the one peer that
	// happened to answer first without the endpoint.
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("peer %s serves no /genesis (it was started without --genesis)", peerURL)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("peer /genesis returned %d", resp.StatusCode)
	}
	g, err := genesiscfg.LoadFromReader(io.LimitReader(resp.Body, maxGenesisDocBytes))
	if err != nil {
		return nil, fmt.Errorf("parse peer genesis.json: %w", err)
	}
	return g, nil
}

// stateSyncDeps is what runStateSync needs from the node.
type stateSyncDeps struct {
	// genesisPrepared is true when main already ran prepareStateSyncGenesis
	// from a local --genesis; otherwise the document is fetched from peers.
	genesisPrepared bool
	peers           []string
	policy          genesisPolicy
	stateDB         *state.StateDB
	executor        *block.Executor
	syncer          *network.Syncer
	retry           time.Duration
}

// runStateSync brings an empty node to a peer's head: the genesis document
// first (fork schedule, chain id, minted supply), then a snapshot, retried
// every d.retry until one imports or ctx ends. The trusted key may still be
// on its way (a node pinning only --genesis-hash learns it from block 0), so
// a refusal for lack of it is just another retry.
func runStateSync(ctx context.Context, d stateSyncDeps) error {
	if d.retry <= 0 {
		d.retry = 2 * time.Second
	}
	wait := func() bool {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(d.retry):
			return true
		}
	}

	for !d.genesisPrepared {
		for _, peer := range d.peers {
			g, err := fetchPeerGenesis(peer, d.policy)
			if errors.Is(err, errGenesisUnpinned) {
				return err // configuration, not a peer fault: no peer is any more trustworthy
			}
			if err != nil {
				log.Warn().Err(err).Str("peer", peer).Msg("state sync: genesis document not available yet")
				continue
			}
			if err := prepareStateSyncGenesis(g, d.policy.docHash, "peer "+peer, d.stateDB, d.executor); err != nil {
				return err // a document that fails the pin or names another chain is fatal
			}
			d.genesisPrepared = true
			break
		}
		if !d.genesisPrepared && !wait() {
			return ctx.Err()
		}
	}

	for {
		err := d.syncer.StateSync(d.executor.ImportStateSnapshot)
		if err == nil {
			return nil
		}
		if errors.Is(err, block.ErrChainNotEmpty) {
			return err
		}
		log.Warn().Err(err).Dur("retry_in", d.retry).Msg("state sync: no snapshot imported yet")
		if !wait() {
			return ctx.Err()
		}
	}
}
