package vm

import (
	"encoding/binary"
	"math/big"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

func csAddr(b byte) qcrypto.Address {
	var a qcrypto.Address
	for i := range a {
		a[i] = b
	}
	return a
}

// derivedDeployAddr mirrors Engine.Deploy's address derivation.
func derivedDeployAddr(deployer qcrypto.Address, nonce uint64) qcrypto.Address {
	nb := make([]byte, 8)
	binary.BigEndian.PutUint64(nb, nonce)
	h := qcrypto.Hash32(append(deployer[:], nb...))
	var a qcrypto.Address
	copy(a[:], h[:])
	return a
}

func csState(t *testing.T) *state.StateDB {
	t.Helper()
	sdb, err := state.NewStateDB(t.TempDir())
	if err != nil {
		t.Fatalf("statedb: %v", err)
	}
	t.Cleanup(func() { sdb.Close() })
	return sdb
}

func csFund(t *testing.T, sdb *state.StateDB, a qcrypto.Address, amount int64) {
	t.Helper()
	acc := types.NewAccount(a)
	acc.AddBalance(big.NewInt(amount))
	sdb.SetAccount(acc)
	if _, err := sdb.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// Deploy wrote a FRESH types.NewAccount at the derived address, and SetAccount
// replaces rather than merges — so any balance already there was destroyed out
// of total supply, and any code there was swapped while its storage (kept under
// a separate store:addr:slot leaf) survived: new code running on stale storage.
//
// CREATE2 already refuses this, with a comment saying the unguarded form "could
// destroy the SPLD held by any pre-funded address". Deploy — which both TxDeploy
// and OpCREATE call — never got the same guard.
func TestDeployRefusesToOverwriteAFundedAccount(t *testing.T) {
	sdb := csState(t)
	e := NewEngine(sdb)

	deployer := csAddr(0xDE)
	victim := derivedDeployAddr(deployer, 1)
	csFund(t, sdb, victim, 50_000)

	if _, err := e.Deploy(deployer, []byte{OpSTOP}, 1); err == nil {
		t.Fatal("deployed over a funded account")
	}
	bal, err := sdb.GetBalance(victim)
	if err != nil {
		t.Fatalf("read balance: %v", err)
	}
	if bal.Cmp(big.NewInt(50_000)) != 0 {
		t.Fatalf("balance is %s, want 50000 — the deploy destroyed it", bal)
	}
}

// The same guard must stop a second deploy replacing live code. Storage lives
// under its own leaf and survives, so overwriting code leaves the new contract
// reading the old one's state.
func TestDeployRefusesToOverwriteAnExistingContract(t *testing.T) {
	sdb := csState(t)
	e := NewEngine(sdb)
	deployer := csAddr(0xDE)

	first, err := e.Deploy(deployer, []byte{OpSTOP}, 7)
	if err != nil {
		t.Fatalf("first deploy: %v", err)
	}
	if _, err := sdb.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	before, err := sdb.GetAccount(first)
	if err != nil {
		t.Fatalf("read account: %v", err)
	}

	// Same deployer, same nonce -> same derived address.
	if _, err := e.Deploy(deployer, []byte{OpPUSH1, 0x01, OpSTOP}, 7); err == nil {
		t.Fatal("redeployed over a live contract")
	}
	after, err := sdb.GetAccount(first)
	if err != nil {
		t.Fatalf("re-read account: %v", err)
	}
	if after.CodeHash != before.CodeHash {
		t.Fatal("the existing contract's code was replaced")
	}
}

// A fresh address must still deploy — the guard must not break the normal path.
func TestDeployStillWorksOnAFreshAddress(t *testing.T) {
	sdb := csState(t)
	e := NewEngine(sdb)
	addr, err := e.Deploy(csAddr(0xDE), []byte{OpSTOP}, 3)
	if err != nil {
		t.Fatalf("deploy to a fresh address failed: %v", err)
	}
	acc, err := sdb.GetAccount(addr)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !acc.IsContract {
		t.Fatal("deployed account is not a contract")
	}
}

// An address holding only a nonce (an EOA that has sent transactions) is also
// occupied: overwriting it would erase that account's identity.
func TestDeployRefusesAnAddressWithANonce(t *testing.T) {
	sdb := csState(t)
	e := NewEngine(sdb)
	deployer := csAddr(0xDE)
	target := derivedDeployAddr(deployer, 2)

	acc := types.NewAccount(target)
	acc.IncrNonce()
	sdb.SetAccount(acc)
	if _, err := sdb.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if _, err := e.Deploy(deployer, []byte{OpSTOP}, 2); err == nil {
		t.Fatal("deployed over an account with a nonce")
	}
}

// CREATE deployed the child FIRST and only then attempted the endowment
// transfer. When the creator could not afford the value it pushed 0 — the
// universal "creation failed" signal — while the contract was already live and
// callable at the derived address. There is no snapshot around CREATE (contrast
// OpCALL), so the frame returned normally and the executor committed the orphan.
//
// A caller told creation failed must be able to rely on nothing having been
// created.
func TestCreateLeavesNothingBehindWhenTheEndowmentFails(t *testing.T) {
	sdb := csState(t)
	e := NewEngine(sdb)

	parent := csAddr(0xC0)
	// Child code: STOP. Placed in memory, then CREATE with a value the parent
	// cannot afford.
	//   MSTORE needs [offset, value]; we store one word then create from it.
	code := []byte{
		// mem[0] = 0x00 (the child's code is a single STOP byte we take from
		// offset 31 of that word)
		OpPUSH1, 0x00, // offset
		OpPUSH1, 0x00, // value
		OpMSTORE,
		// CREATE pops value, then offset, then size (popMemRangeReversed).
		OpPUSH1, 0x01, // size = 1 byte
		OpPUSH1, 0x1F, // offset = 31 -> the low byte of the word = 0x00 = STOP
		OpPUSH1 + 1, 0x27, 0x10, // PUSH2 value = 10000, far more than the parent holds
		OpCREATE,
		OpSTOP,
	}
	h := qcrypto.Hash32(code)
	sdb.SetCode(h, code)
	pacc := types.NewAccount(parent)
	pacc.IsContract = true
	pacc.CodeHash = h
	pacc.AddBalance(big.NewInt(1)) // nowhere near 10000
	sdb.SetAccount(pacc)
	if _, err := sdb.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	res, _ := e.CallWithContext(csAddr(0xAA), csAddr(0xAA), parent, nil, big.NewInt(0), 1, 0, false)
	if res == nil || res.Err != nil {
		t.Fatalf("parent call errored: %v", res)
	}

	// Whatever address CREATE would have used, nothing may be deployed there.
	// The old derivation used the pc of the CREATE instruction.
	for pc := 0; pc < len(code); pc++ {
		child := derivedDeployAddr(parent, uint64(pc))
		acc, err := sdb.GetAccount(child)
		if err != nil {
			continue
		}
		if acc.IsContract {
			t.Fatalf("a child contract survives at pc=%d despite CREATE reporting failure", pc)
		}
	}
}
