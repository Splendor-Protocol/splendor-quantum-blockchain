package vm

import (
	"errors"
	"strings"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

type nftFixture struct {
	sdb        *state.StateDB
	sched      forks.Schedule
	owner      qcrypto.Address
	alice      qcrypto.Address
	bob        qcrypto.Address
	collection qcrypto.Address
}

func newNftFixture(t *testing.T) *nftFixture {
	t.Helper()
	f := &nftFixture{
		sdb:   newTestState(t),
		sched: forks.Schedule{forks.QRC721V1: 1, forks.ModuleRevertV1: 1},
		owner: addr(0xA1),
		alice: addr(0xA2),
		bob:   addr(0xA3),
	}
	rc := &types.Receipt{}
	if err := DispatchModuleCall(f.sdb, f.ctx(f.owner, 3, 1), moduleData(t, "qrc721", "deploy", qrc721DeployParams{Name: "Caps", Symbol: "CAP"}), rc); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	if rc.ContractAddress == nil {
		t.Fatal("deploy set no contract address")
	}
	f.collection = *rc.ContractAddress
	return f
}

func (f *nftFixture) ctx(caller qcrypto.Address, nonce, height uint64) ModuleContext {
	return ModuleContext{Caller: caller, Nonce: nonce, Height: height, Forks: f.sched}
}

func (f *nftFixture) call(t *testing.T, caller qcrypto.Address, action string, params map[string]any) (*types.Receipt, error) {
	t.Helper()
	params["collection"] = f.collection.String()
	rc := &types.Receipt{}
	return rc, DispatchModuleCall(f.sdb, f.ctx(caller, 0, 5), moduleData(t, "qrc721", action, params), rc)
}

func (f *nftFixture) mustCall(t *testing.T, caller qcrypto.Address, action string, params map[string]any) *types.Receipt {
	t.Helper()
	rc, err := f.call(t, caller, action, params)
	if err != nil {
		t.Fatalf("qrc721.%s: %v", action, err)
	}
	return rc
}

func (f *nftFixture) read(t *testing.T, action string, params map[string]any) map[string]interface{} {
	t.Helper()
	params["collection"] = f.collection.String()
	v, err := DispatchModuleReadCall(f.sdb, ReadContext{Height: 5}, moduleData(t, "qrc721", action, params))
	if err != nil {
		t.Fatalf("qrc721 read %s: %v", action, err)
	}
	return v.(map[string]interface{})
}

// Deploy is open, mint is the owner's, and the rest is ERC-721: transfer by
// owner or approved, operator approvals, burn, with the standard events.
func TestQRC721RoutesTheCollectionLifecycle(t *testing.T) {
	f := newNftFixture(t)
	info := f.read(t, "token_info", map[string]any{})
	if info["owner"] != f.owner.String() || info["name"] != "Caps" || info["symbol"] != "CAP" || info["total_supply"] != "0" {
		t.Fatalf("token_info = %v", info)
	}

	if _, err := f.call(t, f.alice, "mint", map[string]any{"to": f.alice.String(), "id": "1", "uri": "ipfs://one"}); err == nil || !strings.Contains(err.Error(), "not owner") {
		t.Fatalf("a stranger minted: %v", err)
	}
	rc := f.mustCall(t, f.owner, "mint", map[string]any{"to": f.alice.String(), "id": "1", "uri": "ipfs://one"})
	if len(rc.Logs) != 1 || rc.Logs[0].Topics[0] != EventTopic("Transfer(sqaddress,sqaddress,uint256)") {
		t.Fatalf("mint logs = %v", rc.Logs)
	}
	if _, err := f.call(t, f.owner, "mint", map[string]any{"to": f.alice.String(), "id": "1"}); err == nil {
		t.Fatal("minted the same id twice")
	}
	if got := f.read(t, "owner_of", map[string]any{"id": "1"}); got["owner"] != f.alice.String() || got["exists"] != true {
		t.Fatalf("owner_of = %v", got)
	}
	if got := f.read(t, "token_uri", map[string]any{"id": "1"}); got["uri"] != "ipfs://one" {
		t.Fatalf("token_uri = %v", got)
	}

	// Bob cannot move Alice's token; Alice's approval lets him.
	if _, err := f.call(t, f.bob, "transfer", map[string]any{"to": f.bob.String(), "id": "1"}); err == nil {
		t.Fatal("a stranger transferred a token")
	}
	f.mustCall(t, f.alice, "approve", map[string]any{"to": f.bob.String(), "id": "1"})
	if got := f.read(t, "get_approved", map[string]any{"id": "1"}); got["approved"] != f.bob.String() {
		t.Fatalf("get_approved = %v", got)
	}
	f.mustCall(t, f.bob, "transfer_from", map[string]any{"from": f.alice.String(), "to": f.bob.String(), "id": "1"})
	if got := f.read(t, "owner_of", map[string]any{"id": "1"}); got["owner"] != f.bob.String() {
		t.Fatalf("after transfer_from owner_of = %v", got)
	}
	if got := f.read(t, "balance_of", map[string]any{"owner": f.bob.String()}); got["balance"] != "1" {
		t.Fatalf("balance_of bob = %v", got)
	}

	// Operator approval for all, then burn by the operator.
	approved := true
	f.mustCall(t, f.bob, "set_approval_for_all", map[string]any{"operator": f.alice.String(), "approved": approved})
	if got := f.read(t, "is_approved_for_all", map[string]any{"owner": f.bob.String(), "operator": f.alice.String()}); got["approved"] != true {
		t.Fatalf("is_approved_for_all = %v", got)
	}
	if _, err := f.call(t, f.bob, "set_approval_for_all", map[string]any{"operator": f.alice.String()}); err == nil || !strings.Contains(err.Error(), "approved is required") {
		t.Fatalf("set_approval_for_all without approved: %v", err)
	}
	rc = f.mustCall(t, f.alice, "burn", map[string]any{"id": "1"})
	if len(rc.Logs) != 1 {
		t.Fatalf("burn logs = %v", rc.Logs)
	}
	if got := f.read(t, "owner_of", map[string]any{"id": "1"}); got["exists"] != false {
		t.Fatalf("burned token still exists: %v", got)
	}
	if got := f.read(t, "token_info", map[string]any{}); got["total_supply"] != "0" {
		t.Fatalf("total_supply after burn = %v", got)
	}
}

// An address that is not a collection is refused before any action, and the
// module is HARD-gated on qrc721_v1.
func TestQRC721RefusesUnknownCollectionsAndIsForkGated(t *testing.T) {
	f := newNftFixture(t)
	rc := &types.Receipt{}
	err := DispatchModuleCall(f.sdb, f.ctx(f.owner, 0, 5), moduleData(t, "qrc721", "mint", map[string]any{
		"collection": addr(0xEE).String(), "to": f.alice.String(), "id": "1",
	}), rc)
	if err == nil || !strings.Contains(err.Error(), "not a QRC-721 collection") {
		t.Fatalf("mint on a non-collection: %v", err)
	}

	f.sched = forks.Schedule{forks.QRC721V1: 100, forks.ModuleRevertV1: 1}
	_, err = f.call(t, f.owner, "mint", map[string]any{"to": f.alice.String(), "id": "2"})
	if !errors.Is(err, ErrForkInactive) || !IsHardModuleError(err) {
		t.Fatalf("before the fork: %v", err)
	}
}
