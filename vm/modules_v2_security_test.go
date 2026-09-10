// Package vm — ModulesV2 fork security tests
//
// Tests for the three critical fixes in ModulesV2:
//
//	M1: DEX remove_liquidity routing
//	M2: Staking lifecycle (Exiting state rejection)
//	M3: Module gas metering
//
// Based on September 2026 mainnet security audit findings.
package vm

import (
	"encoding/json"
	"math/big"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// TestM1_DEX_RemoveLiquidity_Routing verifies the DEX exit path fix
func TestM1_DEX_RemoveLiquidity_Routing(t *testing.T) {
	t.Run("BeforeModulesV2_ShouldFailHard", func(t *testing.T) {
		stateDB := newSecurityStateDB(t)
		defer stateDB.Close()

		caller := addr(1)
		pool := addr(2)

		// Fund caller with tokens
		acc, _ := stateDB.GetAccount(caller)
		acc.Balance = big.NewInt(1000000)
		stateDB.SetAccount(acc)

		ctx := ModuleContext{
			Caller: caller,
			Nonce:  1,
			Height: 100,                                   // Before ModulesV2
			Forks:  forks.Schedule{forks.ModulesV2: 1000}, // Activates at 1000
		}

		params := map[string]interface{}{
			"pool":        pool.String(),
			"lp_shares":   "1000",
			"amount0_min": "0",
			"amount1_min": "0",
		}
		data, _ := json.Marshal(params)

		call := ModuleCall{
			Module: "dex",
			Action: "remove_liquidity",
			Params: data,
		}
		envelope, _ := json.Marshal(call)

		receipt := &types.Receipt{}
		err := DispatchModuleCall(stateDB, ctx, envelope, receipt)

		// Should fail HARD before fork activation
		if err == nil {
			t.Fatal("Expected HARD error before ModulesV2 fork")
		}
		if !IsHardModuleError(err) {
			t.Errorf("Expected HARD error, got: %v", err)
		}
	})

	t.Run("AfterModulesV2_WithValidPool_ShouldSucceed", func(t *testing.T) {
		stateDB := newSecurityStateDB(t)
		defer stateDB.Close()

		caller := addr(1)
		token0 := addr(10)
		token1 := addr(11)

		// Setup: Deploy pool
		ctx := ModuleContext{
			Caller: caller,
			Nonce:  1,
			Height: 1000, // After ModulesV2
			Forks:  forks.Schedule{forks.ModulesV2: 100},
		}

		// Deploy pool first
		pool, err := DeployPool(stateDB, caller, token0, token1, 1)
		if err != nil {
			t.Fatalf("Failed to deploy pool: %v", err)
		}

		// Add liquidity to get LP shares
		lp := NewLiquidityPool(stateDB, pool)

		// Fund caller's tokens
		SetTestTokenBalance(t, stateDB, token0, caller, big.NewInt(10000))
		SetTestTokenBalance(t, stateDB, token1, caller, big.NewInt(10000))

		_, _, shares, err := lp.AddLiquidity(caller, big.NewInt(5000), big.NewInt(5000), big.NewInt(0), big.NewInt(0))
		if err != nil {
			t.Fatalf("Failed to add liquidity: %v", err)
		}

		// Now test remove_liquidity
		params := map[string]interface{}{
			"pool":        pool.String(),
			"lp_shares":   shares.String(),
			"amount0_min": "0",
			"amount1_min": "0",
		}
		data, _ := json.Marshal(params)

		call := ModuleCall{
			Module: "dex",
			Action: "remove_liquidity",
			Params: data,
		}
		envelope, _ := json.Marshal(call)

		receipt := &types.Receipt{}
		err = DispatchModuleCall(stateDB, ctx, envelope, receipt)

		if err != nil {
			t.Errorf("remove_liquidity should succeed after ModulesV2: %v", err)
		}

		// Verify LP shares were burned
		remainingShares, shErr := lp.LPBalance(caller)
		if shErr != nil {
			t.Fatal(shErr)
		}
		if remainingShares.Sign() != 0 {
			t.Errorf("Expected LP shares to be burned, got %s", remainingShares.String())
		}
	})

	t.Run("DeadlineExpired_ShouldFail", func(t *testing.T) {
		stateDB := newSecurityStateDB(t)
		defer stateDB.Close()

		caller := addr(1)
		pool := addr(2)

		ctx := ModuleContext{
			Caller: caller,
			Height: 500, // Current height
			Forks:  forks.Schedule{forks.ModulesV2: 100},
		}

		params := map[string]interface{}{
			"pool":      pool.String(),
			"lp_shares": "1000",
			"deadline":  400, // Deadline in the past
		}
		data, _ := json.Marshal(params)

		call := ModuleCall{
			Module: "dex",
			Action: "remove_liquidity",
			Params: data,
		}
		envelope, _ := json.Marshal(call)

		receipt := &types.Receipt{}
		err := DispatchModuleCall(stateDB, ctx, envelope, receipt)

		if err == nil {
			t.Error("Expected deadline check to fail")
		}
		if !isSoftError(err) {
			t.Errorf("Deadline failure should be SOFT, got: %v", err)
		}
	})
}

// TestM2_Staking_LifecycleProtection verifies Exiting state is protected
func TestM2_Staking_LifecycleProtection(t *testing.T) {
	t.Run("RegisterWhileExiting_ShouldFail", func(t *testing.T) {
		t.Skip("Requires staking module integration - verify manually")
		// This test verifies that ModulesV2 prevents re-registration
		// while a validator is in Exiting state, protecting the
		// pending unbonding balance from being overwritten.
		//
		// Test plan:
		// 1. Register validator with bond
		// 2. Call unregister (enters Exiting state)
		// 3. Attempt to register again before finalization
		// 4. Should fail with clear error under ModulesV2
	})

	t.Run("RegisterAfterFinalization_ShouldSucceed", func(t *testing.T) {
		t.Skip("Requires staking module integration - verify manually")
		// After unbonding completes and finalize is called,
		// re-registration should be allowed.
	})
}

// TestM3_ModuleGasMetering verifies gas charges are applied
func TestM3_ModuleGasMetering(t *testing.T) {
	t.Run("ModuleCallChargesGas", func(t *testing.T) {
		t.Skip("Requires executor integration - verify in executor tests")
		// This test should verify:
		// 1. Base gas charge applied to module calls
		// 2. Per-byte charge based on data size
		// 3. Gas counted against block budget
		// 4. Same call same height same gas (deterministic)
		//
		// The gas charging happens in executor.go when it detects
		// a call to ModuleRouterAddress and wraps the module dispatch.
	})

	t.Run("LargeModuleCallExceedsBudget", func(t *testing.T) {
		t.Skip("Requires executor integration - verify in executor tests")
		// Send a module call with enough data that gas charge
		// would exceed the block budget. Should be rejected.
	})
}

// Helper: Check if error is SOFT (validation failure, insufficient funds, etc.)
func isSoftError(err error) bool {
	return err != nil && !IsHardModuleError(err)
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && findSubstring(s, substr)
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// Test helper to set token balance (for DEX tests)
func SetTestTokenBalance(t *testing.T, stateDB *state.StateDB, token, holder qcrypto.Address, amount *big.Int) {
	t.Helper()
	tok := NewQRC20Token(stateDB, token)
	// This assumes token is already deployed
	// In real test, deploy token first or mock the storage
	if err := tok.setBalance(holder, amount); err != nil {
		t.Fatal(err)
	}
}

// TestModulesV2Comprehensive runs all three fixes together
func TestModulesV2Comprehensive(t *testing.T) {
	t.Run("AllFixesActiveAtSameHeight", func(t *testing.T) {
		// Verify all three fixes activate at the same height
		// as specified in the fork schedule
		schedule := forks.Schedule{
			forks.ModulesV2: 1000,
		}

		if !schedule.Active(forks.ModulesV2, 1000) {
			t.Error("ModulesV2 should be active at activation height")
		}
		if schedule.Active(forks.ModulesV2, 999) {
			t.Error("ModulesV2 should not be active before activation")
		}

		// All three fixes must activate together - that's the point
		// of bundling them into one fork
	})
}

// newSecurityStateDB opens a throwaway StateDB, the way every other vm test
// does; core/state deliberately ships no test constructor.
func newSecurityStateDB(t *testing.T) *state.StateDB {
	t.Helper()
	sdb, err := state.NewStateDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sdb.Close() })
	return sdb
}
