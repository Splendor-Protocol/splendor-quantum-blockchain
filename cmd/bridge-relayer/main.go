// bridge-relayer watches the legacy Splendor mainnet's burn contract and
// claims each finalized burn on the quantum chain's bridge module.
//
//	bridge-relayer \
//	  --evm-rpc https://mainnet-rpc.splendor.org/ --evm-chain-id 2691 \
//	  --evm-contract 0x... --start-block 12345678 \
//	  --node https://quantum-rpc.splendor.org --chain-id splendor-quantum-1 \
//	  --bridge SQ:... --wallet operator.sqwallet --state relayer-state.json
//
// The wallet password comes from SQC_WALLET_PASS. The loop is restart-safe:
// claim idempotency lives on-chain, the cursor in the state file only avoids
// re-scanning. --replay-id resubmits one recorded-as-skipped burn and exits.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

func main() {
	var (
		evmRPC        = flag.String("evm-rpc", "", "legacy chain JSON-RPC URL (required)")
		evmChainID    = flag.Uint64("evm-chain-id", 2691, "legacy chain id the RPC must report")
		evmContract   = flag.String("evm-contract", "", "burn contract address, 0x + 40 hex (required)")
		startBlock    = flag.Uint64("start-block", 0, "legacy block to scan from on the first run (the burn contract's deploy block; required when no state file exists)")
		node          = flag.String("node", "", "quantum node RPC base URL (required)")
		chainID       = flag.String("chain-id", types.DefaultChainID, "quantum chain id the node must report")
		bridgeAddr    = flag.String("bridge", "", "bridge instance SQ: address (required)")
		walletPath    = flag.String("wallet", "", "operator .sqwallet path (required)")
		statePath     = flag.String("state", "bridge-relayer-state.json", "cursor state file")
		attestDir     = flag.String("attestation-dir", "", "shared directory for M-of-N attestations (required when the bridge threshold > 1; leave empty for single-org threshold 1)")
		confirmations = flag.Uint64("confirmations", 120, "legacy blocks to wait before a burn is claimable")
		batch         = flag.Uint64("batch", 2000, "max legacy blocks per eth_getLogs")
		fee           = flag.Int64("fee", 2000, "claim tx fee in smallest SPLD units")
		maxAttempts   = flag.Int("max-attempts", 5, "claim attempts before a burn is recorded as skipped")
		receiptWait   = flag.Duration("receipt-wait", 30*time.Second, "how long to wait for one claim receipt")
		poll          = flag.Duration("poll", 15*time.Second, "pause between passes")
		replayID      = flag.Uint64("replay-id", 0, "resubmit one skipped burn id and exit")
		once          = flag.Bool("once", false, "run one pass and exit")
	)
	flag.Parse()

	fail := func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, "bridge-relayer: "+format+"\n", args...)
		os.Exit(1)
	}
	for name, v := range map[string]string{
		"--evm-rpc": *evmRPC, "--evm-contract": *evmContract,
		"--node": *node, "--bridge": *bridgeAddr, "--wallet": *walletPath,
	} {
		if v == "" {
			fail("%s is required", name)
		}
	}
	contract := strings.ToLower(*evmContract)
	if len(contract) != 42 || !strings.HasPrefix(contract, "0x") {
		fail("--evm-contract: want 0x + 40 hex chars")
	}
	bridge, err := qcrypto.AddressFromString(*bridgeAddr)
	if err != nil {
		fail("--bridge: %v", err)
	}
	if *batch == 0 {
		fail("--batch must be >= 1 (0 scans nothing, forever)")
	}
	if *confirmations == 0 {
		log.Printf("WARNING: --confirmations 0 removes reorg protection; a legacy reorg can strand claims")
	}
	if err := types.SetClientChainID(*chainID); err != nil {
		fail("--chain-id: %v", err)
	}
	pass := os.Getenv("SQC_WALLET_PASS")
	if pass == "" {
		fail("SQC_WALLET_PASS is not set")
	}
	pub, priv, err := qcrypto.LoadWallet(*walletPath, []byte(pass))
	if err != nil {
		fail("wallet: %v", err)
	}
	operator := qcrypto.DeriveAddress(pub)

	state, err := loadState(*statePath)
	if err != nil {
		fail("%v", err)
	}
	if state == nil {
		if *startBlock == 0 {
			fail("no state file at %s: --start-block is required on the first run", *statePath)
		}
		state = &relayerState{
			EvmChainID: *evmChainID, EvmContract: contract, Bridge: bridge.String(),
			LastProcessed: *startBlock - 1,
		}
	} else if err := state.verifyIdentity(*evmChainID, contract, bridge.String()); err != nil {
		fail("%v", err)
	}

	r := &Relayer{
		EVM:     NewEVMClient(*evmRPC),
		Quantum: NewQuantumClient(strings.TrimRight(*node, "/")),

		EvmContract: contract, Bridge: bridge, Operator: operator, Priv: priv,
		Confirmations: *confirmations, BatchBlocks: *batch, Fee: *fee,
		MaxAttempts: *maxAttempts, ReceiptWait: *receiptWait,
		AttestationDir: *attestDir,
		State:          state, StatePath: *statePath,
		attempts: map[uint64]int{},
		Logf:     log.Printf,
	}

	if err := r.Preflight(*evmChainID, *chainID); err != nil {
		fail("preflight: %v", err)
	}
	log.Printf("relaying %s burns on chain %d to bridge %s as %s (cursor %d, %d confirmations)",
		contract, *evmChainID, bridge, operator, state.LastProcessed, *confirmations)

	if *replayID != 0 {
		if err := r.Replay(*replayID); err != nil {
			fail("replay %d: %v", *replayID, err)
		}
		return
	}

	for {
		n, err := r.RunOnce()
		if err != nil {
			log.Printf("pass failed (will retry): %v", err)
		} else if n > 0 {
			log.Printf("pass complete: %d claims, cursor %d", n, r.State.LastProcessed)
		}
		if *once {
			if err != nil {
				// A oneshot (cron/systemd) caller must be able to see failure.
				os.Exit(1)
			}
			return
		}
		time.Sleep(*poll)
	}
}

// Replay rescans one skipped burn's block and settles just that id.
func (r *Relayer) Replay(id uint64) error {
	var block uint64
	found := false
	for _, s := range r.State.Skipped {
		if s.ID == id {
			block, found = s.Block, true
		}
	}
	if !found {
		return fmt.Errorf("id %d is not in the skipped list", id)
	}
	info, err := r.Quantum.Bridge(r.Bridge.String())
	if err != nil {
		return fmt.Errorf("bridge info: %w", err)
	}
	logs, err := r.EVM.BurnedLogs(r.EvmContract, block, block)
	if err != nil {
		return err
	}
	for _, l := range logs {
		burn, err := decodeBurn(l)
		if err != nil || burn.ID != id {
			continue
		}
		ok, err := r.settle(burn, info.Threshold)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("claim did not settle — see the log above")
		}
		kept := r.State.Skipped[:0]
		for _, s := range r.State.Skipped {
			if s.ID != id {
				kept = append(kept, s)
			}
		}
		r.State.Skipped = kept
		if r.StatePath != "" {
			return r.State.save(r.StatePath)
		}
		return nil
	}
	return fmt.Errorf("no Burned log with id %d in block %d", id, block)
}
