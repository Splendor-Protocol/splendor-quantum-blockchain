package block

import (
	"errors"
	"math/big"
	"strings"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// C2: a state-write failure after storeBlock must not look like a successful
// apply, must discard the speculative overlay, must halt new work, and must
// become a fully applied block only after reconciliation.
func TestStateCommitFailureHaltsUntilReconcile(t *testing.T) {
	pub, priv, _ := qcrypto.GenerateKeypair()
	from := qcrypto.DeriveAddress(pub)
	to := testAddr(0x42)
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	vaddr := qcrypto.DeriveAddress(vpub)

	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()
	fund(t, sdb, from, 1_000_000)
	before, err := sdb.GetBalance(from)
	if err != nil {
		t.Fatal(err)
	}

	tx := signedTransfer(t, from, priv, to, 100, 0)
	b1, err := ex.BuildProposal([]*types.Transaction{tx}, vaddr, vpub.Bytes(), vpriv, [32]byte{}, 1)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	sdb.FailNextCommit(errors.New("injected state write failure"))
	if err := ex.ExecuteBlock(b1); err == nil {
		t.Fatal("ExecuteBlock succeeded after an injected state commit failure")
	} else if !strings.Contains(err.Error(), "state commit failed") {
		t.Fatalf("got %v, want a state commit failure", err)
	}

	if ex.BlockFullyApplied(b1) {
		t.Fatal("partial persist reported as fully applied")
	}
	if ex.CanProgress() {
		t.Fatal("executor allowed new work after a partial persist")
	}
	if sdb.HasPendingWrites() {
		t.Fatal("speculative overlay survived the failed commit")
	}
	head, err := ex.LatestBlock()
	if err != nil || head == nil || head.Hash != b1.Hash {
		t.Fatalf("head should already be the stored block: head=%v err=%v", head, err)
	}
	if sh, ok := sdb.AppliedHeight(); ok && sh == 1 {
		t.Fatal("applied height advanced despite the failed state write")
	}
	after, _ := sdb.GetBalance(from)
	if after.Cmp(before) != 0 {
		t.Fatalf("committed sender balance changed on a failed state write: %s -> %s", before, after)
	}
	if err := ex.ExecuteBlock(b1); !errors.Is(err, ErrApplyHalted) {
		t.Fatalf("retry while halted: %v, want ErrApplyHalted", err)
	}
	if _, err := ex.ProduceBlock(nil, vaddr, vpub.Bytes(), vpriv, b1.Hash, 2); !errors.Is(err, ErrApplyHalted) {
		t.Fatalf("produce while halted: %v, want ErrApplyHalted", err)
	}

	if err := ex.RecoverPartialApply(); err != nil {
		t.Fatalf("recover: %v", err)
	}
	if !ex.CanProgress() {
		t.Fatal("halt stayed set after a successful reconcile")
	}
	if !ex.BlockFullyApplied(b1) {
		t.Fatal("block not fully applied after reconcile")
	}
	got, _ := sdb.GetBalance(from)
	want := new(big.Int).Sub(before, tx.Amount)
	want.Sub(want, tx.Fee)
	if got.Cmp(want) != 0 {
		t.Fatalf("sender after recover = %s, want %s", got, want)
	}
}

func TestProduceBlockCommitFailureHaltsAndDiscards(t *testing.T) {
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()

	sdb.FailNextCommit(errors.New("injected state write failure"))
	_, err := ex.ProduceBlock(nil, qcrypto.DeriveAddress(vpub), vpub.Bytes(), vpriv, [32]byte{}, 1)
	if err == nil {
		t.Fatal("ProduceBlock succeeded after an injected state commit failure")
	}
	if ex.CanProgress() {
		t.Fatal("producer continued after a partial persist")
	}
	if sdb.HasPendingWrites() {
		t.Fatal("produce error left a dirty overlay")
	}
}

func TestBlockFullyAppliedRequiresHeightAndRoot(t *testing.T) {
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()

	b1, err := ex.BuildProposal(nil, qcrypto.DeriveAddress(vpub), vpub.Bytes(), vpriv, [32]byte{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if ex.BlockFullyApplied(b1) {
		t.Fatal("an unapplied proposal must not count as applied")
	}
	sdb.FailNextCommit(errors.New("injected"))
	_ = ex.ExecuteBlock(b1)
	if ex.BlockFullyApplied(b1) {
		t.Fatal("head-hash match alone must not count as applied")
	}
}
