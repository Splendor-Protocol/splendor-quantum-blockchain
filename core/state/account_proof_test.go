package state

import (
	"bytes"
	"context"
	"encoding/json"
	"math/big"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

func commitAccounts(t *testing.T, sdb *StateDB, bals ...int64) []qcrypto.Address {
	t.Helper()
	addrs := make([]qcrypto.Address, len(bals))
	for i, bal := range bals {
		addrs[i] = addr(byte(0xA0 + i))
		acc := types.NewAccount(addrs[i])
		acc.AddBalance(big.NewInt(bal))
		sdb.SetAccount(acc)
	}
	if _, err := sdb.CommitWithHeight(uint64(len(bals))); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return addrs
}

func TestVerifyAccountProofOddLeafTree(t *testing.T) {
	sdb, err := NewStateDB(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer sdb.Close()

	// Three account leaves: the last is carried upward with no sibling.
	addrs := commitAccounts(t, sdb, 11, 22, 33)
	root, err := sdb.StateRoot()
	if err != nil {
		t.Fatalf("StateRoot: %v", err)
	}

	var sawCarry bool
	for i, a := range addrs {
		p, err := sdb.AccountProofContext(context.Background(), a)
		if err != nil {
			t.Fatalf("proof %d: %v", i, err)
		}
		if p.Root != root {
			t.Fatalf("proof %d root %x != committed %x", i, p.Root[:8], root[:8])
		}
		if p.Height != 3 || !p.HeightKnown {
			t.Fatalf("proof %d height = %d known=%v, want 3/true", i, p.Height, p.HeightKnown)
		}
		var acc types.Account
		if err := json.Unmarshal(p.LeafPreimage, &acc); err != nil {
			t.Fatalf("preimage %d: %v", i, err)
		}
		if acc.Balance == nil || acc.Balance.Cmp(big.NewInt(int64(11*(i+1)))) != 0 {
			t.Fatalf("preimage %d balance = %s", i, acc.Balance)
		}
		if err := VerifyAccountProof(p); err != nil {
			t.Fatalf("VerifyAccountProof %d: %v", i, err)
		}
		if len(p.Steps) < 2 {
			sawCarry = true
		}
		tampered := *p
		tampered.Steps = append([]MerkleStep(nil), p.Steps...)
		if len(tampered.Steps) > 0 {
			tampered.Steps[0].Left = !tampered.Steps[0].Left
			if err := VerifyAccountProof(&tampered); err == nil {
				t.Fatal("flipped Left direction still verified")
			}
		}
	}
	if !sawCarry {
		t.Fatal("expected at least one odd-leaf proof to omit a carried-up sibling")
	}
}

func TestAccountProofIgnoresPendingOverlay(t *testing.T) {
	sdb, err := NewStateDB(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer sdb.Close()

	a := addr(0xB1)
	acc := types.NewAccount(a)
	acc.AddBalance(big.NewInt(100))
	sdb.SetAccount(acc)
	if _, err := sdb.CommitWithHeight(1); err != nil {
		t.Fatal(err)
	}

	pending := types.NewAccount(a)
	pending.AddBalance(big.NewInt(999))
	sdb.SetAccount(pending)

	p, err := sdb.AccountProofContext(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	var committed types.Account
	if err := json.Unmarshal(p.LeafPreimage, &committed); err != nil {
		t.Fatal(err)
	}
	if committed.Balance.Cmp(big.NewInt(100)) != 0 {
		t.Fatalf("proof preimage balance = %s, want committed 100 (pending 999 leaked)", committed.Balance)
	}
	if live, _ := sdb.GetBalance(a); live.Cmp(big.NewInt(999)) != 0 {
		t.Fatalf("live balance = %s, want pending 999", live)
	}
}

// The committed read used by /state/account must ignore the pending overlay
// exactly like the proof preimage does, so a response that also carries a
// committed root/height never mixes in a speculative write.
//
// Originally written against a bare StateDB.CommittedAccount. That read took
// no lock and was one of TWO unsynchronized reads behind a single response --
// the account came from one view and the root/height it was labelled with
// from another, so a commit landing between them produced an answer that was
// internally consistent with neither. The read now goes through a pinned
// CommittedView, which is where the guarantee this test describes actually
// lives; the assertion is unchanged.
func TestCommittedAccountIgnoresPendingOverlay(t *testing.T) {
	sdb, err := NewStateDB(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer sdb.Close()

	a := addr(0xB2)
	acc := types.NewAccount(a)
	acc.AddBalance(big.NewInt(40))
	sdb.SetAccount(acc)
	if _, err := sdb.CommitWithHeight(1); err != nil {
		t.Fatal(err)
	}

	pending := types.NewAccount(a)
	pending.AddBalance(big.NewInt(500))
	sdb.SetAccount(pending)

	view, err := sdb.CommittedView()
	if err != nil {
		t.Fatalf("CommittedView: %v", err)
	}
	defer view.Release()
	got, err := view.Account(a)
	if err != nil {
		t.Fatalf("CommittedView.Account: %v", err)
	}
	if got.Balance == nil || got.Balance.Cmp(big.NewInt(40)) != 0 {
		t.Fatalf("committed balance = %s, want committed 40 (pending 500 leaked)", got.Balance)
	}
	// GetAccount (the live read) still sees the pending value.
	if live, _ := sdb.GetAccount(a); live.Balance.Cmp(big.NewInt(500)) != 0 {
		t.Fatalf("GetAccount balance = %s, want pending 500", live.Balance)
	}
}

func TestVerifyAccountProofRejectsWrongPreimage(t *testing.T) {
	sdb, err := NewStateDB(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer sdb.Close()
	addrs := commitAccounts(t, sdb, 5, 6)
	p, err := sdb.AccountProofContext(context.Background(), addrs[0])
	if err != nil {
		t.Fatal(err)
	}
	p.LeafPreimage = bytes.Repeat([]byte{1}, len(p.LeafPreimage))
	if err := VerifyAccountProof(p); err == nil {
		t.Fatal("tampered leaf preimage verified")
	}
}
