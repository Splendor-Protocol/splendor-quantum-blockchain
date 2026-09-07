package vm

import (
	"math/big"
	"testing"
)

// The TRANSFER opcode (0xB0) is NOT shadowed by the PUSH range, so it is the
// live path into StateDB.Transfer's self-transfer aliasing bug. A contract that
// sends its own balance to itself doubles it, minting native SPLD. Note the
// plain wallet-level transfer path (execTransfer) is NOT affected — it re-reads
// the recipient after debiting the sender — so this opcode is the reachable
// exploit for the native currency.
func TestOpTRANSFERToSelfDoesNotMintNativeSPLD(t *testing.T) {
	sdb := newTestState(t)
	defer sdb.Close()

	contract := addr(0x80)
	// ADDRESS pushes the contract's own address; TRANSFER pops amount then addr.
	code := concat([]byte{OpADDRESS}, pushU(1_000), []byte{OpTRANSFER})
	installContract(t, sdb, contract, code, 1_000)

	e := NewEngine(sdb)
	for i := 0; i < 4; i++ {
		res, _ := e.CallWithContext(addr(0x01), addr(0x01), contract, nil, big.NewInt(0), 1, 0, false)
		if res.Err != nil {
			t.Fatalf("self-transfer call %d: %v", i, res.Err)
		}
	}
	if _, err := sdb.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	bal, _ := sdb.GetBalance(contract)
	if bal.Cmp(big.NewInt(1_000)) != 0 {
		t.Fatalf("TRANSFER-to-self minted native SPLD: balance = %s, want 1000", bal)
	}
}

// SELFDESTRUCT naming its own address as beneficiary hits the same aliasing bug.
func TestSelfDestructToSelfDoesNotMint(t *testing.T) {
	sdb := newTestState(t)
	defer sdb.Close()

	contract := addr(0x81)
	code := concat([]byte{OpADDRESS, OpSELFDESTRUCT})
	installContract(t, sdb, contract, code, 1_000)

	e := NewEngine(sdb)
	res, _ := e.CallWithContext(addr(0x01), addr(0x01), contract, nil, big.NewInt(0), 1, 0, false)
	if res.Err != nil {
		t.Fatalf("selfdestruct call: %v", res.Err)
	}
	if _, err := sdb.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	bal, _ := sdb.GetBalance(contract)
	if bal.Cmp(big.NewInt(1_000)) > 0 {
		t.Fatalf("SELFDESTRUCT-to-self minted native SPLD: balance = %s, want <= 1000", bal)
	}
}

// QRC-20 self-transfer must not create tokens. Pre-fix transferWithEvents read
// senderBal AND recipBal before writing either, so for sender==recipient both
// reads saw B; it then wrote B-amount followed by B+amount to the SAME slot and
// the second write won. Each self-transfer of the full balance therefore doubled
// it while totalSupply stayed constant.
func TestQRC20SelfTransferDoesNotMint(t *testing.T) {
	sdb := newTestState(t)
	defer sdb.Close()

	holder := addr(0x11)
	contract, err := DeployQRC20(sdb, holder, "Probe", "PRB", 18, big.NewInt(1_000), 0)
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	tok := NewQRC20Token(sdb, contract)

	for i := 0; i < 8; i++ {
		bal, _ := tok.BalanceOf(holder)
		if err := tok.Transfer(holder, holder, bal); err != nil {
			t.Fatalf("self-transfer %d: %v", i, err)
		}
	}

	bal, _ := tok.BalanceOf(holder)
	supply, _ := tok.TotalSupply()
	if bal.Cmp(big.NewInt(1_000)) != 0 {
		t.Fatalf("QRC20 self-transfer minted: balance = %s, want 1000", bal)
	}
	if bal.Cmp(supply) > 0 {
		t.Fatalf("holder balance %s exceeds totalSupply %s", bal, supply)
	}
}

// The same slot-aliasing bug in QRC-1155: SafeTransferFrom reads both balances
// before writing either.
func TestQRC1155SelfTransferDoesNotMint(t *testing.T) {
	sdb := newTestState(t)
	defer sdb.Close()

	holder := addr(0x22)
	contract, err := DeployQRC1155(sdb, holder, "Probe", "https://probe/{id}", 0)
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	tok := NewQRC1155Token(sdb, contract)

	id := big.NewInt(1)
	if err := tok.Mint(holder, holder, id, big.NewInt(1_000), ""); err != nil {
		t.Fatalf("mint: %v", err)
	}

	for i := 0; i < 8; i++ {
		bal, _ := tok.BalanceOf(holder, id)
		if err := tok.SafeTransferFrom(holder, holder, holder, id, bal); err != nil {
			t.Fatalf("self-transfer %d: %v", i, err)
		}
	}

	bal, _ := tok.BalanceOf(holder, id)
	if bal.Cmp(big.NewInt(1_000)) != 0 {
		t.Fatalf("QRC1155 self-transfer minted: balance = %s, want 1000", bal)
	}
}

// QRC-721 self-transfer must leave both ownership and the balance counter intact.
func TestQRC721SelfTransferKeepsBalance(t *testing.T) {
	sdb := newTestState(t)
	defer sdb.Close()

	holder := addr(0x33)
	contract, err := DeployQRC721(sdb, holder, "Probe", "PRB", 0)
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	tok := NewQRC721Token(sdb, contract)

	id := big.NewInt(1)
	if err := tok.Mint(holder, holder, id, "ipfs://probe"); err != nil {
		t.Fatalf("mint: %v", err)
	}

	for i := 0; i < 4; i++ {
		if err := tok.Transfer(holder, holder, id); err != nil {
			t.Fatalf("self-transfer %d: %v", i, err)
		}
	}

	bal, _ := tok.BalanceOf(holder)
	if bal.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("QRC721 self-transfer changed balance: got %s, want 1", bal)
	}
	owner, _ := tok.OwnerOf(id)
	if owner != holder {
		t.Fatalf("owner = %s, want %s", owner, holder)
	}
}
