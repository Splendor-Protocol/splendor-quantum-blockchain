package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/splendor-protocol/splendor-quantum-blockchain/consensus"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/block"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	genesiscfg "github.com/splendor-protocol/splendor-quantum-blockchain/genesis"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network"
	"github.com/splendor-protocol/splendor-quantum-blockchain/rpc"
)

// nodeReadyStatus is /readyz: applied height, index coverage, store alignment,
// consensus progress, and not-in-recovery. /healthz stays process liveness only.
func nodeReadyStatus(ex *block.Executor, sdb *state.StateDB, syncer *network.Syncer, stateSyncPending bool, bftEngine func() *consensus.BFTEngine) rpc.ReadyStatus {
	st := rpc.ReadyStatus{}
	if sdb != nil {
		h, ok := sdb.AppliedHeight()
		st.AppliedHeight = h
		st.AppliedKnown = ok
	}
	if ex != nil {
		ih, iok, cov, cok := ex.TxIndexStatus()
		st.IndexHeight = ih
		st.IndexKnown = iok
		if cok {
			st.IndexCoverageFrom = cov
		}
		// Recovering is apply recovery: an interrupted snapshot import or a
		// partial persist that has halted further apply. Distinct from the BFT
		// vote lock reported in ValidatorLocked.
		st.Recovering = ex.HasIncompleteSnapshotImport() || !ex.CanProgress()
		st.StoresAligned = ex.StoresAligned()
	} else {
		st.StoresAligned = false
	}
	var head uint64
	if ex != nil {
		head, _ = ex.LatestHeight()
		if hb, err := ex.LatestBlock(); err == nil && hb != nil {
			st.LastCommitTime = hb.Header.Timestamp
		}
	}
	if bftEngine != nil {
		if eng := bftEngine(); eng != nil {
			cs := eng.Status()
			st.ConsensusHeight = cs.Height
			st.ConsensusRound = cs.Round
			st.ValidatorLocked = cs.Locked
		}
	}
	st.Syncing = stateSyncPending || (syncer != nil && !syncer.IsSynced())

	switch {
	case st.Recovering:
		st.Reason = "recovery in progress"
	case stateSyncPending:
		st.Reason = "state sync in progress"
	case st.Syncing:
		st.Reason = "sync in progress"
	case !st.AppliedKnown:
		st.Reason = "applied height unknown"
	case ex != nil && !st.StoresAligned:
		st.Reason = "committed state not aligned with head"
	case head == 0:
		st.Reason = "chain not yet synced"
	case ex != nil && ex.HasTxIndex() && st.AppliedKnown && (!st.IndexKnown || st.IndexHeight < st.AppliedHeight):
		st.Reason = "transaction index behind applied height"
	default:
		st.Ready = true
	}
	return st
}

// reconcileSnapshotImportAtStartup runs ReconcileSnapshotImport. A retryable
// incomplete import is not fatal when allowRetry is set (--state-sync), so
// the normal retry path can repair a legacy interrupted import.
func reconcileSnapshotImportAtStartup(ex *block.Executor, allowRetry bool) error {
	if err := classifyReconcileStartupError(ex.ReconcileSnapshotImport(), allowRetry); err != nil {
		return err
	}
	return nil
}

func classifyReconcileStartupError(err error, allowRetry bool) error {
	if err == nil {
		return nil
	}
	if allowRetry && errors.Is(err, block.ErrSnapshotImportIncomplete) {
		log.Warn().Err(err).Msg("snapshot import recovery needs a retry; continuing so --state-sync can resume")
		return nil
	}
	return fmt.Errorf("snapshot import recovery: %w", err)
}

// shouldRunStateSync is true when --state-sync is set and the chain is empty
// or a previous snapshot import is still incomplete (head stored, state not).
func shouldRunStateSync(flagStateSync bool, latest *types.Block, incomplete bool) bool {
	return flagStateSync && (latest == nil || incomplete)
}

// validateStateSyncFlags refuses --state-sync configurations that cannot work,
// at start-up, naming the flag that fixes each.
func validateStateSyncFlags(isValidator bool, trustedPK []byte, genesisPin *[32]byte, genesisPath string, peers []string) error {
	// A validator may state-sync. It used to be refused because a validator on
	// an empty chain sealed its own block 1 before the snapshot landed; a
	// validator that did not mint the chain it was started against now holds
	// production until it has a stored head (joiningValidatorReady), so the
	// case the refusal existed for cannot happen. It still needs somewhere to
	// take the snapshot FROM.
	if isValidator && len(peers) == 0 {
		return errors.New("--state-sync --validator needs at least one --peer: the snapshot, and every block " +
			"after it, comes from a peer, and this node must not seal anything until it has them")
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

	// Same client discipline as every other peer request: a 3xx is returned,
	// not followed, so an approved peer URL cannot bounce this node to an
	// internal address, and the transport refuses internal targets outright.
	client := network.PeerHTTPClient(10 * time.Second)
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
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return nil, fmt.Errorf("peer %s redirected /genesis to %q; redirects are not followed", peerURL, resp.Header.Get("Location"))
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
