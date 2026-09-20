package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"

	"github.com/syndtr/goleveldb/leveldb"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// ErrNoCommittedView is returned when an immutable view of committed state
// cannot be created. It is never papered over with a read of the live
// database: a caller that asked for one committed block's state and silently
// got "whatever is current" has no way to tell the difference, and every
// height, root and proof it then publishes is unanchored.
var ErrNoCommittedView = errors.New("state: cannot create an immutable committed view")

// CommittedHead is the identity of the committed state: the Merkle root and
// the block height that root belongs to, read together.
//
// Read together is the whole point. StateRoot and AppliedHeight each take the
// state lock on their own, so calling them one after the other can straddle a
// commit and return a root from one block with the height of another. Commit
// holds the write lock across the batch and the leaf-index update, so a pair
// captured under one read lock is always one block's.
type CommittedHead struct {
	Root        [32]byte
	Height      uint64
	HeightKnown bool
}

// CommittedView is an immutable read of committed state at one height and one
// root. Account bytes come from a pinned LevelDB snapshot, so neither a later
// commit nor the in-flight pending layer can change what it answers — the
// equivalent of Ethereum serving eth_getProof and eth_call against a fixed
// block tag rather than "current", or a Cosmos SDK query pinned with
// x-cosmos-block-height.
//
// Release it when the response is written: the snapshot holds LevelDB
// compaction off for as long as it lives.
type CommittedView struct {
	CommittedHead
	snap *leveldb.Snapshot
}

// CommittedHeadView returns the committed root and height captured together,
// without pinning a snapshot. Use it when only the identity of the committed
// state is needed (readiness, /state/root); use CommittedView when account
// bytes must come from the same block.
func (s *StateDB) CommittedHeadView() (CommittedHead, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.committedHeadLocked()
}

func (s *StateDB) committedHeadLocked() (CommittedHead, error) {
	if s.sandbox {
		return CommittedHead{}, ErrSandboxCommit
	}
	return CommittedHead{
		Root:        s.committedRootLocked(),
		Height:      s.appliedHeight,
		HeightKnown: s.heightKnown,
	}, nil
}

// CommittedView pins the committed state: root, applied height and a LevelDB
// snapshot, all captured under one read lock. It fails rather than falling
// back to the live database when the snapshot cannot be taken.
func (s *StateDB) CommittedView() (*CommittedView, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	head, err := s.committedHeadLocked()
	if err != nil {
		return nil, err
	}
	if s.db == nil {
		return nil, fmt.Errorf("%w: no database", ErrNoCommittedView)
	}
	snap, err := s.db.GetSnapshot()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoCommittedView, err)
	}
	return &CommittedView{CommittedHead: head, snap: snap}, nil
}

// Release drops the pinned snapshot. Safe to call more than once.
func (v *CommittedView) Release() {
	if v == nil || v.snap == nil {
		return
	}
	v.snap.Release()
	v.snap = nil
}

// Account returns the account as committed at this view. An address the
// committed state does not hold reads as a zero-balance account, matching
// GetAccount — absence and emptiness are the same thing on this chain.
func (v *CommittedView) Account(addr qcrypto.Address) (*types.Account, error) {
	if v == nil || v.snap == nil {
		return nil, ErrNoCommittedView
	}
	data, err := v.snap.Get([]byte(prefixAccount+addr.String()), nil)
	if err == leveldb.ErrNotFound {
		return types.NewAccount(addr), nil
	}
	if err != nil {
		return nil, fmt.Errorf("committed state read for %s: %w", addr, err)
	}
	var acc types.Account
	if err := json.Unmarshal(data, &acc); err != nil {
		return nil, fmt.Errorf("failed to decode committed account %s: %w", addr, err)
	}
	if acc.Balance == nil {
		acc.Balance = big.NewInt(0)
	}
	return &acc, nil
}

// AccountProof returns the inclusion proof for addr against this StateDB's
// committed state. Leaf preimage, leaf hash, sibling directions, root and
// height are taken under one read lock, so the returned proof is one block's.
//
// It is AccountProofContext under the name that says which view it belongs to;
// callers pair it with BlockForHead to resolve the block hash.
func (s *StateDB) CommittedAccountProof(ctx context.Context, addr qcrypto.Address) (*AccountProof, error) {
	return s.AccountProofContext(ctx, addr)
}

// Head is the committed root and height the proof was built against.
func (p *AccountProof) Head() CommittedHead {
	if p == nil {
		return CommittedHead{}
	}
	return CommittedHead{Root: p.Root, Height: p.Height, HeightKnown: p.HeightKnown}
}
