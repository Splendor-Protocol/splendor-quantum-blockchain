package vm

import (
	"testing"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

func TestDEXMovesEmitTokenAndPoolLogs(t *testing.T) {
	f := newDexFixture(t)
	sched := v2ForksWithModules(10)

	addRc := &types.Receipt{}
	if err := DispatchModuleCall(f.sdb, ctxAt(f.lp, 6, 10, sched), moduleData(t, "dex", "add_liquidity", dexLiquidityParams{
		Pool: f.pool.String(), Amount0Desired: "1000", Amount1Desired: "250",
	}), addRc); err != nil {
		t.Fatalf("add_liquidity: %v", err)
	}
	if n := countTopic(addRc, TopicTransfer); n != 2 {
		t.Fatalf("add_liquidity Transfer logs: got %d want 2", n)
	}
	if n := countTopic(addRc, TopicMint); n != 1 {
		t.Fatalf("add_liquidity Mint logs: got %d want 1", n)
	}

	swapRc := &types.Receipt{}
	if err := DispatchModuleCall(f.sdb, ctxAt(f.lp, 7, 10, sched), moduleData(t, "dex", "swap", dexSwapParams{
		Pool: f.pool.String(), TokenIn: 0, AmountIn: "10",
	}), swapRc); err != nil {
		t.Fatalf("swap: %v", err)
	}
	if n := countTopic(swapRc, TopicTransfer); n != 2 {
		t.Fatalf("swap Transfer logs: got %d want 2", n)
	}
	if n := countTopic(swapRc, TopicSwap); n != 1 {
		t.Fatalf("swap Swap logs: got %d want 1", n)
	}

	remRc := &types.Receipt{}
	if err := DispatchModuleCall(f.sdb, ctxAt(f.lp, 8, 10, sched), moduleData(t, "dex", "remove_liquidity", dexRemoveLiquidityParams{
		Pool: f.pool.String(), LPShares: "100",
	}), remRc); err != nil {
		t.Fatalf("remove_liquidity: %v", err)
	}
	if n := countTopic(remRc, TopicTransfer); n != 2 {
		t.Fatalf("remove_liquidity Transfer logs: got %d want 2", n)
	}
	if n := countTopic(remRc, TopicBurn); n != 1 {
		t.Fatalf("remove_liquidity Burn logs: got %d want 1", n)
	}
}

func countTopic(rc *types.Receipt, topic [32]byte) int {
	n := 0
	for _, lg := range rc.Logs {
		if len(lg.Topics) > 0 && lg.Topics[0] == topic {
			n++
		}
	}
	return n
}
