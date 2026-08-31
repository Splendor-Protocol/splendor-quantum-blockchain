package main

import (
	"encoding/json"
	"fmt"
	"math/big"
	"time"

	"github.com/splendor-protocol/splendor-quantum-blockchain/compiler"
	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// runFleet deploys a generated contract fleet to a live node and verifies every
// member through the node's own /contract/verify endpoint.
//
// The in-repo fleet test proves the pipeline against the engine. This proves it
// against a real chain, where the things that actually bite are different:
// mempool admission, nonce sequencing, block inclusion, and whether the
// verification endpoint agrees with the compiler across hundreds of distinct
// bytecodes.
//
// Nonces are allocated locally from one starting read, because asking the node
// between submissions returns only the COMMITTED nonce and would reuse a slot
// still pending.
func runFleet(cl *client, seedPath string, seed int64, count, batch int, timeout time.Duration) error {
	fleet := compiler.GenerateFleet(seed, count)
	_, priv, from, err := loadKey(seedPath)
	if err != nil {
		return err
	}
	if err := cl.adoptChainID(); err != nil {
		return err
	}
	nonce, err := cl.nonce(from)
	if err != nil {
		return err
	}
	fmt.Printf("fleet: %d contracts, seed %d, from %s starting at nonce %d\n", count, seed, from, nonce)

	type member struct {
		name string
		src  string
		addr qcrypto.Address
	}
	var deployed []member
	var submitFailures int

	for i, gen := range fleet {
		c, err := compiler.Compile(gen.Source)
		if err != nil {
			return fmt.Errorf("%s failed to compile: %w", gen.Name, err)
		}
		tx := types.NewDeploy(from, c.Bytecode, big.NewInt(fleetFee), nonce, time.Now().Unix())
		if err := tx.Sign(priv, true); err != nil {
			return err
		}
		if _, err := cl.submit(tx); err != nil {
			// A rejected submission does not consume the nonce, so the slot is
			// reused by the next contract rather than leaving a hole that would
			// stall every later deploy.
			fmt.Printf("  submit %s failed: %v\n", gen.Name, err)
			submitFailures++
			continue
		}
		deployed = append(deployed, member{name: gen.Name, src: gen.Source, addr: deployAddr(from, nonce)})
		nonce++

		// Stay well inside the mempool's per-sender cap, and confirm the chain
		// is actually consuming what we send: a producer that silently skips a
		// tx would otherwise let us pile up hundreds behind a stuck nonce.
		if len(deployed)%batch == 0 {
			if err := cl.awaitNonce(from, nonce, timeout); err != nil {
				return fmt.Errorf("after %d/%d submitted: %w", i+1, count, err)
			}
			fmt.Printf("  %d/%d deployed\n", len(deployed), count)
		}
	}
	if err := cl.awaitNonce(from, nonce, timeout); err != nil {
		return fmt.Errorf("after all %d submitted: %w", len(deployed), err)
	}
	fmt.Printf("deployed %d/%d (%d submit failures)\n", len(deployed), count, submitFailures)

	// Verify every member through the node.
	var verified, mismatched, errored int
	for _, m := range deployed {
		ok, reason, err := cl.verify(m.addr, m.src)
		switch {
		case err != nil:
			errored++
			fmt.Printf("  verify %s (%s): %v\n", m.name, m.addr, err)
		case ok:
			verified++
		default:
			mismatched++
			fmt.Printf("  MISMATCH %s (%s): %s\n", m.name, m.addr, reason)
		}
	}
	fmt.Printf("verified %d, mismatched %d, errored %d\n", verified, mismatched, errored)
	if mismatched > 0 || errored > 0 || submitFailures > 0 {
		return fmt.Errorf("fleet run had failures: %d mismatched, %d errored, %d not submitted",
			mismatched, errored, submitFailures)
	}
	return nil
}

// fleetFee is the per-deploy fee. Above the mempool floor with room for the
// dynamic multiplier that kicks in as the pool fills.
const fleetFee = 100_000

// awaitNonce waits until the sender's committed nonce reaches want, so the
// caller never runs further ahead of the chain than the mempool allows.
//
// A nonce that stops advancing is reported as a stall rather than waited on
// forever: a transaction the producer skips is never evicted (PruneStale only
// drops nonces BELOW the committed one), so it blocks the sender permanently
// and no amount of further waiting helps.
func (c *client) awaitNonce(addr qcrypto.Address, want uint64, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	last := uint64(0)
	lastProgress := time.Now()
	for time.Now().Before(deadline) {
		got, err := c.nonce(addr)
		if err != nil {
			return err
		}
		if got >= want {
			return nil
		}
		if got != last {
			last, lastProgress = got, time.Now()
		} else if time.Since(lastProgress) > timeout/2 {
			return fmt.Errorf("nonce stuck at %d (want %d) for %s: the producer is not including this sender's "+
				"transactions, and a skipped tx is never evicted — the account is jammed until the node restarts",
				got, want, time.Since(lastProgress).Round(time.Second))
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("timed out waiting for nonce %d", want)
}

// verify asks the node whether src compiles to the code deployed at addr.
func (c *client) verify(addr qcrypto.Address, src string) (bool, string, error) {
	body, _ := json.Marshal(map[string]string{"address": addr.String(), "source": src})
	var out struct {
		Verified bool   `json:"verified"`
		Kind     string `json:"kind"`
		Reason   string `json:"reason"`
	}
	if err := c.postJSON("/contract/verify", body, &out); err != nil {
		return false, "", err
	}
	if out.Verified {
		return true, "", nil
	}
	return false, fmt.Sprintf("%s: %s", out.Kind, out.Reason), nil
}
