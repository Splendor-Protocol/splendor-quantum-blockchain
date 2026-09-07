package vm

import (
	"math/big"
	"strings"
	"testing"
)

func TestQRC721MintRequiresOwner(t *testing.T) {
	sdb := newTestState(t)
	defer sdb.Close()

	owner := addr(0xA1)
	stranger := addr(0xB2)
	contract, err := DeployQRC721(sdb, owner, "Caps", "CAP", 0)
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	tok := NewQRC721Token(sdb, contract)

	got, err := tok.Owner()
	if err != nil || got != owner {
		t.Fatalf("owner = %s (%v), want %s", got, err, owner)
	}

	if err := tok.Mint(stranger, stranger, big.NewInt(1), "ipfs://no"); err == nil {
		t.Fatal("non-owner mint must be refused")
	} else if !strings.Contains(err.Error(), "not owner") {
		t.Fatalf("error should name owner, got: %v", err)
	}

	if err := tok.Mint(owner, stranger, big.NewInt(1), "ipfs://yes"); err != nil {
		t.Fatalf("owner mint: %v", err)
	}
	holder, err := tok.OwnerOf(big.NewInt(1))
	if err != nil || holder != stranger {
		t.Fatalf("token owner = %s (%v), want %s", holder, err, stranger)
	}
}
