package vm

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
)

func TestListActiveEmptyIndexIsNotAnError(t *testing.T) {
	sdb := newTestState(t)
	reg := NewStakingContract(sdb, DeployStaking(nil))
	active, err := reg.ListActive()
	if err != nil {
		t.Fatalf("empty index: %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("empty index listed %d validators", len(active))
	}
}

func TestListActiveStorageErrorIsNotEmptySet(t *testing.T) {
	sdb := newTestState(t)
	reg := NewStakingContract(sdb, DeployStaking(nil))
	sdb.FailNextStorageRead(errors.New("injected registry read"))
	active, err := reg.ListActive()
	if err == nil || !strings.Contains(err.Error(), "injected registry read") {
		t.Fatalf("want a storage error, got active=%v err=%v", active, err)
	}
}

func TestGetValidatorStorageErrorIsNotAbsent(t *testing.T) {
	sdb := newTestState(t)
	reg := NewStakingContract(sdb, DeployStaking(nil))
	sdb.FailNextStorageRead(errors.New("injected record read"))
	rec, err := reg.GetValidator(qcrypto.Address{})
	if rec != nil || err == nil || !strings.Contains(err.Error(), "injected record read") {
		t.Fatalf("want a storage error, got rec=%v err=%v", rec, err)
	}
}

func TestListActiveCorruptIndexIsError(t *testing.T) {
	sdb := newTestState(t)
	reg := NewStakingContract(sdb, DeployStaking(nil))
	if err := sdb.SetStorageValue(DeployStaking(nil), slotStakingIndex, []byte("not-json")); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.ListActive(); err == nil {
		t.Fatal("corrupt index was treated as an empty set")
	}
}

func TestListActiveMissingRecordIsError(t *testing.T) {
	sdb := newTestState(t)
	reg := NewStakingContract(sdb, DeployStaking(nil))
	var missing qcrypto.Address
	missing[0] = 0x11
	raw, err := json.Marshal([]qcrypto.Address{missing})
	if err != nil {
		t.Fatal(err)
	}
	if err := sdb.SetStorageValue(DeployStaking(nil), slotStakingIndex, raw); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.ListActive(); err == nil {
		t.Fatal("an index entry without a record was skipped instead of failing closed")
	}
}

func TestCommittedViewIgnoresPendingRegister(t *testing.T) {
	sdb := newTestState(t)
	v, pk := fundedValidator(t, sdb)
	if _, err := sdb.Commit(); err != nil {
		t.Fatal(err)
	}
	reg := NewStakingContract(sdb, DeployStaking(nil))
	if err := reg.Register(v, pk, 1); err != nil {
		t.Fatal(err)
	}
	pending, err := reg.ListActive()
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending ListActive = %d err=%v, want 1", len(pending), err)
	}
	committed, err := reg.CommittedView().ListActive()
	if err != nil {
		t.Fatal(err)
	}
	if len(committed) != 0 {
		t.Fatalf("committed view saw a pending registration: %+v", committed)
	}
}

func TestCommittedStorageReadSkipsOverlay(t *testing.T) {
	sdb := newTestState(t)
	slot := slotStakingIndex
	addr := DeployStaking(nil)
	if err := sdb.SetStorageValue(addr, slot, []byte("pending")); err != nil {
		t.Fatal(err)
	}
	got, err := sdb.GetStorageValue(addr, slot)
	if err != nil || string(got) != "pending" {
		t.Fatalf("pending read = %q err=%v", got, err)
	}
	got, err = sdb.GetCommittedStorageValue(addr, slot)
	if err != nil || len(got) != 0 {
		t.Fatalf("committed read saw overlay: %q err=%v", got, err)
	}
}

func TestFailNextStorageReadIsConsumedOnce(t *testing.T) {
	sdb := newTestState(t)
	sdb.FailNextStorageRead(errors.New("once"))
	if _, err := sdb.GetCommittedStorageValue(DeployStaking(nil), slotStakingIndex); err == nil {
		t.Fatal("expected injected error")
	}
	if _, err := sdb.GetCommittedStorageValue(DeployStaking(nil), slotStakingIndex); err != nil {
		t.Fatalf("injection leaked to a second read: %v", err)
	}
}
