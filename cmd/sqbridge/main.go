// sqbridge is an interactive console for the plug-and-play cross-chain bridge
// between the legacy EVM Splendor chain and the Splendor Quantum Chain. It
// exercises the bridge package's Chain adapters and relayer engine: show both
// chains' status and balances, list the asset registry, and run a relay pass in
// each direction.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/splendor-protocol/splendor-quantum-blockchain/bridge"
)

func main() {
	var (
		evmRPC        = flag.String("evm-rpc", "", "legacy EVM JSON-RPC endpoint")
		burnContract  = flag.String("burn-contract", "", "legacy escrow contract emitting Burned")
		quantumBase   = flag.String("quantum-base", "", "quantum node REST API base URL")
		bridgeAddr    = flag.String("bridge", "", "deployed bridge_v1 instance address (SQ:...)")
		evmOwner      = flag.String("evm-owner", "", "legacy address to show balances for")
		sqOwner       = flag.String("sq-owner", "", "quantum SQ: address to show balances for")
		confirmLegacy = flag.Uint64("confirm-legacy", 120, "legacy confirmation depth")
	)
	flag.Parse()

	if *evmRPC == "" || *quantumBase == "" {
		fmt.Fprintln(os.Stderr, "usage: sqbridge -evm-rpc <url> -quantum-base <url> [-burn-contract 0x..] [-bridge SQ:..] [-evm-owner 0x..] [-sq-owner SQ:..]")
		os.Exit(2)
	}

	legacy := bridge.NewEVMChain(bridge.EVMConfig{
		RPCURL:            *evmRPC,
		BurnContract:      *burnContract,
		ConfirmDepthValue: *confirmLegacy,
	})
	quantum := bridge.NewQuantumChain(bridge.QuantumConfig{
		BaseURL:       *quantumBase,
		BridgeAddress: *bridgeAddr,
	})
	relayer := bridge.NewRelayer(legacy, quantum)
	ctx := context.Background()

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("\nsqbridge> ")
		if !scanner.Scan() {
			break
		}
		switch strings.TrimSpace(scanner.Text()) {
		case "status", "s":
			printStatus(ctx, legacy)
			printStatus(ctx, quantum)
		case "assets", "a":
			printAssets(legacy)
			printAssets(quantum)
		case "balances", "b":
			printBalance(ctx, legacy, *evmOwner)
			printBalance(ctx, quantum, *sqOwner)
		case "relay", "r":
			relay(ctx, relayer)
		case "help", "h", "?":
			fmt.Println("commands: status(s) assets(a) balances(b) relay(r) help(h) quit(q)")
		case "quit", "q", "exit":
			return
		default:
			fmt.Println("unknown command (help for the list)")
		}
	}
}

func printStatus(ctx context.Context, c bridge.Chain) {
	h, err := c.Height(ctx)
	if err != nil {
		fmt.Printf("%s: height error: %v\n", c.Name(), err)
		return
	}
	fmt.Printf("%s: chain %s, height %d, confirm depth %d\n", c.Name(), c.ChainID(), h, c.ConfirmDepth())
}

func printAssets(c bridge.Chain) {
	for asset, info := range c.Assets() {
		fmt.Printf("%s: %s -> %s (wrapped=%v)\n", c.Name(), asset, info.Symbol, info.Wrapped)
	}
}

func printBalance(ctx context.Context, c bridge.Chain, owner string) {
	if owner == "" {
		fmt.Printf("%s: no owner configured, skipping balances\n", c.Name())
		return
	}
	for _, asset := range []bridge.Asset{bridge.AssetNative, bridge.AssetSPLV} {
		if _, ok := c.Assets()[asset]; !ok {
			continue
		}
		bal, err := c.Balance(ctx, owner, asset)
		if err != nil {
			fmt.Printf("%s %s balance: %v\n", c.Name(), asset, err)
			continue
		}
		fmt.Printf("%s %s balance of %s: %s\n", c.Name(), asset, owner, bal.String())
	}
}

func relay(ctx context.Context, r *bridge.Relayer) {
	for _, dir := range []bridge.Direction{bridge.LeftToRight, bridge.RightToLeft} {
		label := "legacy -> quantum"
		if dir == bridge.RightToLeft {
			label = "quantum -> legacy"
		}
		settled, err := r.RunOnce(ctx, dir)
		if err != nil {
			fmt.Printf("%s: %v\n", label, err)
			continue
		}
		if len(settled) == 0 {
			fmt.Printf("%s: nothing to settle\n", label)
			continue
		}
		for _, e := range settled {
			fmt.Printf("%s: settled id %s (asset %s, %s -> %s)\n", label, e.ID, e.Asset, e.Amount, e.Recipient)
		}
	}
}
