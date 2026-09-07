package signer

import (
	"testing"

	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
)

// The signer recomputes the module router address offline so the WASM never
// links vm. It must be the account the node actually routes module calls to,
// and the literal every client (SDK, wallet extension, docs) pins.
func TestModuleRouterAddressMatchesVM(t *testing.T) {
	const want = "SQ:b07043f4ef749951893985d66494d0f4e44c8812053361c07ea24bc70a444dcd"
	got := ModuleRouterAddress()
	if got != vm.ModuleRouterAddress {
		t.Fatalf("signer router address %s != vm.ModuleRouterAddress %s", got, vm.ModuleRouterAddress)
	}
	if got.String() != want {
		t.Fatalf("router address literal moved:\n got %s\nwant %s", got, want)
	}
	if vm.ModuleRouterAddress.String() != want {
		t.Fatalf("vm router address literal moved:\n got %s\nwant %s", vm.ModuleRouterAddress, want)
	}
}
