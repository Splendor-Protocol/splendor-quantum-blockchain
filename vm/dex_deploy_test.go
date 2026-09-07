package vm

import (
	"math/big"
	"testing"
)

// A pool address is deterministic in its token pair, so DeployPool is callable
// by anyone at any time on an already-live pool. It must refuse. Pre-fix it
// unconditionally rewrote reserve0/reserve1/lp_total to zero, which stranded
// every LP's shares and let the next depositor claim the whole pool as the
// "first" one.
func TestDeployPoolRejectsExistingPool(t *testing.T) {
	sdb := newTestState(t)
	defer sdb.Close()

	deployer := addr(0x70)
	attacker := addr(0x71)

	t0, err := DeployQRC20(sdb, deployer, "Alpha", "ALP", 18, big.NewInt(1_000_000), 0)
	if err != nil {
		t.Fatalf("deploy token0: %v", err)
	}
	t1, err := DeployQRC20(sdb, deployer, "Beta", "BET", 18, big.NewInt(1_000_000), 1)
	if err != nil {
		t.Fatalf("deploy token1: %v", err)
	}

	poolAddr, err := DeployPool(sdb, deployer, t0, t1, 2)
	if err != nil {
		t.Fatalf("deploy pool: %v", err)
	}
	pool := NewLiquidityPool(sdb, poolAddr)

	if _, _, _, err := pool.AddLiquidity(deployer,
		big.NewInt(100_000), big.NewInt(100_000), big.NewInt(1), big.NewInt(1)); err != nil {
		t.Fatalf("add liquidity: %v", err)
	}

	lpBefore, _ := pool.LPBalance(deployer)
	r0Before, r1Before, _ := pool.Reserves()
	if lpBefore.Sign() == 0 || r0Before.Sign() == 0 {
		t.Fatal("precondition: pool should hold liquidity")
	}

	// Anyone can recompute the pool address and call DeployPool again.
	if _, err := DeployPool(sdb, attacker, t0, t1, 99); err == nil {
		t.Error("DeployPool re-deployed a live pool without error")
	}

	r0After, r1After, _ := pool.Reserves()
	if r0After.Cmp(r0Before) != 0 || r1After.Cmp(r1Before) != 0 {
		t.Fatalf("redeploy reset reserves: (%s,%s) -> (%s,%s)",
			r0Before, r1Before, r0After, r1After)
	}
	totalAfter, _ := pool.TotalLPShares()
	if totalAfter.Sign() == 0 {
		t.Fatal("redeploy zeroed total LP shares — existing LP positions orphaned")
	}
}
