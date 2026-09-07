// Package vm — State Journal for atomic sub-call rollback in SQC-VM.
//
// The journal records every state change made during contract execution.
// When a sub-call reverts, the journal can roll back only that sub-call's changes
// without affecting the parent call's state.
//
// This is the SQC equivalent of Ethereum's state journal/snapshot mechanism.
// All operations are quantum-safe — no conventional crypto involved.
package vm

import (
	"math/big"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// JournalEntry represents a single reversible state change.
type JournalEntry interface {
	// Revert undoes this state change.
	Revert(state journalStateAccessor)
}

// journalStateAccessor is the minimal interface the journal needs from StateDB.
type journalStateAccessor interface {
	GetAccount(addr qcrypto.Address) (*types.Account, error)
	SetAccount(acc *types.Account)
	SetStorageValue(contract qcrypto.Address, slot [32]byte, value []byte) error
}

// Journal is a list of state changes that can be reverted atomically.
// It supports snapshots: you can take a snapshot, make changes, and then
// either commit (discard the snapshot) or revert to the snapshot.
type Journal struct {
	entries   []JournalEntry
	snapshots []int // each snapshot stores the entry count at the time of the snapshot
}

// NewJournal creates a new empty journal.
func NewJournal() *Journal {
	return &Journal{}
}

// Append adds a new entry to the journal.
func (j *Journal) Append(entry JournalEntry) {
	j.entries = append(j.entries, entry)
}

// Snapshot creates a new checkpoint and returns its ID.
// Changes made after a snapshot can be reverted independently.
func (j *Journal) Snapshot() int {
	id := len(j.snapshots)
	j.snapshots = append(j.snapshots, len(j.entries))
	return id
}

// RevertToSnapshot undoes all journal entries added since snapshot `id`.
func (j *Journal) RevertToSnapshot(id int, state journalStateAccessor) {
	if id >= len(j.snapshots) {
		return
	}
	revertTo := j.snapshots[id]

	// Revert entries in reverse order (newest first)
	for i := len(j.entries) - 1; i >= revertTo; i-- {
		j.entries[i].Revert(state)
	}
	j.entries = j.entries[:revertTo]
	j.snapshots = j.snapshots[:id]
}

// CommitSnapshot discards a snapshot (keeps the changes, removes the checkpoint).
func (j *Journal) CommitSnapshot(id int) {
	if id >= len(j.snapshots) {
		return
	}
	j.snapshots = j.snapshots[:id]
}

// Clear removes all entries and snapshots.
func (j *Journal) Clear() {
	j.entries = j.entries[:0]
	j.snapshots = j.snapshots[:0]
}

// EntryCount returns the number of journal entries.
func (j *Journal) EntryCount() int { return len(j.entries) }

// ── Journal entry types ───────────────────────────────────────────────────────

// BalanceChange records a balance modification for rollback.
type BalanceChange struct {
	Addr    qcrypto.Address
	OldBal  *big.Int
}

func (e *BalanceChange) Revert(state journalStateAccessor) {
	acc, err := state.GetAccount(e.Addr)
	if err != nil {
		return
	}
	acc.Balance = new(big.Int).Set(e.OldBal)
	state.SetAccount(acc)
}

// NonceChange records a nonce increment for rollback.
type NonceChange struct {
	Addr     qcrypto.Address
	OldNonce uint64
}

func (e *NonceChange) Revert(state journalStateAccessor) {
	acc, err := state.GetAccount(e.Addr)
	if err != nil {
		return
	}
	acc.Nonce = e.OldNonce
	state.SetAccount(acc)
}

// StorageChange records a storage write for rollback.
type StorageChange struct {
	Contract qcrypto.Address
	Slot     [32]byte
	OldValue []byte
}

func (e *StorageChange) Revert(state journalStateAccessor) {
	if len(e.OldValue) == 0 {
		// Value was zero before — write zero back (effectively delete)
		state.SetStorageValue(e.Contract, e.Slot, nil)
	} else {
		state.SetStorageValue(e.Contract, e.Slot, e.OldValue)
	}
}

// AccountCreation records the creation of a new account (for rollback via deletion).
type AccountCreation struct {
	Addr qcrypto.Address
}

func (e *AccountCreation) Revert(state journalStateAccessor) {
	// Reset to zero account (effectively marks as non-existent)
	state.SetAccount(types.NewAccount(e.Addr))
}

// PublicKeyRegistration records registering an ML-DSA-65 public key.
type PublicKeyRegistration struct {
	Addr      qcrypto.Address
	OldPubKey []byte
}

func (e *PublicKeyRegistration) Revert(state journalStateAccessor) {
	acc, err := state.GetAccount(e.Addr)
	if err != nil {
		return
	}
	acc.PublicKey = e.OldPubKey
	state.SetAccount(acc)
}

// LogAddition records an event log emission (logs are removed on revert).
// Note: we store a reference to the slice in the execution context
// and restore its length.
type LogAddition struct {
	// OriginalLen is the length of ctx.Logs before this log was added.
	// On revert, we truncate ctx.Logs back to this length.
	ContextLogs *[]*types.Log
	OriginalLen int
}

func (e *LogAddition) Revert(_ journalStateAccessor) {
	if e.ContextLogs != nil && len(*e.ContextLogs) > e.OriginalLen {
		*e.ContextLogs = (*e.ContextLogs)[:e.OriginalLen]
	}
}

// ── JournaledState: StateDB wrapper that auto-journals all writes ──────────────

// JournaledState wraps journalStateAccessor and automatically records
// every state change into the journal. Use this instead of StateDB directly
// inside the VM to get free rollback support.
type JournaledState struct {
	inner   journalStateAccessor
	journal *Journal
}

// NewJournaledState wraps a StateDB with journaling.
func NewJournaledState(inner journalStateAccessor, journal *Journal) *JournaledState {
	return &JournaledState{inner: inner, journal: journal}
}

// AddBalance adds amount to an account's balance, recording the change.
func (s *JournaledState) AddBalance(addr qcrypto.Address, amount *big.Int) error {
	acc, err := s.inner.GetAccount(addr)
	if err != nil {
		return err
	}
	oldBal := new(big.Int).Set(acc.Balance)
	s.journal.Append(&BalanceChange{Addr: addr, OldBal: oldBal})
	acc.AddBalance(amount)
	s.inner.SetAccount(acc)
	return nil
}

// SubBalance subtracts amount from an account's balance, recording the change.
func (s *JournaledState) SubBalance(addr qcrypto.Address, amount *big.Int) error {
	acc, err := s.inner.GetAccount(addr)
	if err != nil {
		return err
	}
	oldBal := new(big.Int).Set(acc.Balance)
	if err := acc.SubBalance(amount); err != nil {
		return err
	}
	s.journal.Append(&BalanceChange{Addr: addr, OldBal: oldBal})
	s.inner.SetAccount(acc)
	return nil
}

// SetStorage writes a storage value, recording the old value for rollback.
func (s *JournaledState) SetStorage(contract qcrypto.Address, slot [32]byte, value []byte) error {
	oldValue, _ := s.inner.(interface {
		GetStorageValue(qcrypto.Address, [32]byte) ([]byte, error)
	}).GetStorageValue(contract, slot)

	s.journal.Append(&StorageChange{
		Contract: contract,
		Slot:     slot,
		OldValue: oldValue,
	})
	return s.inner.SetStorageValue(contract, slot, value)
}

// IncrNonce increments an account's nonce, recording it for rollback.
func (s *JournaledState) IncrNonce(addr qcrypto.Address) error {
	acc, err := s.inner.GetAccount(addr)
	if err != nil {
		return err
	}
	s.journal.Append(&NonceChange{Addr: addr, OldNonce: acc.Nonce})
	acc.IncrNonce()
	s.inner.SetAccount(acc)
	return nil
}

// Snapshot creates a journal snapshot and returns its ID.
func (s *JournaledState) Snapshot() int {
	return s.journal.Snapshot()
}

// RevertToSnapshot rolls back all state changes since snapshot `id`.
func (s *JournaledState) RevertToSnapshot(id int) {
	s.journal.RevertToSnapshot(id, s.inner)
}

// CommitSnapshot discards the snapshot checkpoint (keeps all changes).
func (s *JournaledState) CommitSnapshot(id int) {
	s.journal.CommitSnapshot(id)
}
