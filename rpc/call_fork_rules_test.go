package rpc

import (
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/block"
	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
)

// simCallArgsSize is the STATICCALL input the copy charge is measured on:
// 4 KiB = 128 words, which vm_gas_v1 bills at MemPerWord each.
const simCallArgsSize = 4096

func simPush32(v *big.Int) []byte {
	out := make([]byte, 33)
	out[0] = vm.OpPUSH32
	b := v.Bytes()
	copy(out[33-len(b):], b)
	return out
}

func simPushU(n uint64) []byte { return simPush32(new(big.Int).SetUint64(n)) }

func installSimContract(t *testing.T, sdb *state.StateDB, a qcrypto.Address, code []byte) {
	t.Helper()
	h := qcrypto.Hash32(code)
	sdb.SetCode(h, code)
	acc := types.NewAccount(a)
	acc.IsContract = true
	acc.CodeHash = h
	sdb.SetAccount(acc)
	if _, err := sdb.Commit(); err != nil {
		t.Fatalf("install contract: %v", err)
	}
}

// /call and /estimate must run under the rules consensus is running under.
// Both built a bare vm.NewEngine, whose gas/call fork predicates are nil and
// therefore always false, so every simulation answered with pre-fork
// semantics no matter what the chain had activated — and both asked at the
// head height, one block before the height a submitted transaction executes
// at, so even a correctly wired predicate would miss a fork that switches on
// at head+1. The observable here is the vm_gas_v1 input-copy charge: 4 KiB of
// STATICCALL input costs 128 words of gas after the fork and nothing before.
func TestSimulationUsesConsensusForkRules(t *testing.T) {
	sdb, err := state.NewStateDB(t.TempDir())
	if err != nil {
		t.Fatalf("statedb: %v", err)
	}
	defer sdb.Close()
	ex, err := block.NewExecutor(sdb, t.TempDir())
	if err != nil {
		t.Fatalf("executor: %v", err)
	}
	defer ex.Close()

	caller := qcrypto.Address{0x31}
	callee := qcrypto.Address{0x32}
	installSimContract(t, sdb, callee, []byte{vm.OpSTOP})
	// STATICCALL pops gas, addr, argsOffset, argsSize, retOffset, retSize.
	// /call runs static, so this is the only sub-call form it can make.
	installSimContract(t, sdb, caller, concatBytes(
		simPushU(0), // retSize
		simPushU(0), // retOffset
		simPushU(simCallArgsSize),
		simPushU(0), // argsOffset
		simPush32(new(big.Int).SetBytes(callee[:])),
		simPushU(vm.MaxGasPerCall), // asks for more than remains → clamped
		[]byte{vm.OpSTATICCALL, vm.OpSTOP},
	))

	r := mux.NewRouter()
	RegisterCallRoutes(r, sdb, ex, nil, nil)

	gasUsed := func(t *testing.T) uint64 {
		t.Helper()
		body := strings.NewReader(`{"to":"` + caller.String() + `"}`)
		req := httptest.NewRequest(http.MethodPost, "/call", body)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("/call = %d: %s", rec.Code, rec.Body.String())
		}
		var resp callResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode /call: %v", err)
		}
		if resp.Reverted {
			t.Fatalf("/call reverted: %s", resp.RevertReason)
		}
		return resp.GasUsed
	}

	gt := vm.DefaultGasTable()
	setup := 6*gt.PUSH + gt.GasOf(vm.OpSTATICCALL)
	copyCost := uint64(simCallArgsSize/32) * gt.MemPerWord

	// No schedule: pre-fork rules, no copy charge.
	if got := gasUsed(t); got != setup {
		t.Fatalf("unforked /call gas_used = %d, want %d (STATICCALL setup, no copy charge)", got, setup)
	}

	// A fresh executor is at height 0, so a transaction sent now executes in
	// block 1. Scheduling the fork there must be visible to the simulation;
	// asking at the head (0) reports it inactive and disagrees with consensus.
	ex.SetForks(forks.Schedule{forks.VMGasV1: 1})
	if got := gasUsed(t); got != setup+copyCost {
		t.Fatalf("/call gas_used = %d under vm_gas_v1 at head+1, want %d (setup %d + copy %d); "+
			"the simulation is not running the rules consensus runs", got, setup+copyCost, setup, copyCost)
	}

	// Scheduled beyond the block a transaction sent now lands in: still
	// inactive, so the alignment is a boundary, not an unconditional enable.
	ex.SetForks(forks.Schedule{forks.VMGasV1: 2})
	if got := gasUsed(t); got != setup {
		t.Fatalf("/call gas_used = %d with vm_gas_v1 scheduled at head+2, want the pre-fork %d", got, setup)
	}
}

func concatBytes(chunks ...[]byte) []byte {
	var out []byte
	for _, c := range chunks {
		out = append(out, c...)
	}
	return out
}
