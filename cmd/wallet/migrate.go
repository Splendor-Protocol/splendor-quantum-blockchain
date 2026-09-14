package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// runMigrate implements `sqc-wallet migrate`. Split out of cmdMigrate's RunE
// closure for readability; see cmdMigrate's doc comment for the security
// rationale (moving off an EVM-signature-derived key).
func runMigrate(oldWalletPath, newWalletPath, nodeAddr, chainID *string, feeUnits *int64, doTransfer *bool) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		if err := refuseExistingWallet(*newWalletPath); err != nil {
			return err
		}
		if *chainID != "" {
			if err := types.SetClientChainID(*chainID); err != nil {
				return fmt.Errorf("invalid --chain-id: %w", err)
			}
		}

		fmt.Fprintln(os.Stderr, "Step 1/2: opening the wallet to migrate away from...")
		oldPub, oldPriv, err := loadWalletInteractive(*oldWalletPath)
		if err != nil {
			return fmt.Errorf("open --old-wallet: %w", err)
		}
		oldAddr := qcrypto.DeriveAddress(oldPub)

		fmt.Fprintln(os.Stderr, "\nStep 2/2: generating a new wallet from fresh CSPRNG entropy (not derived from any signature)...")
		newSeed, err := qcrypto.GenerateSeed()
		if err != nil {
			return fmt.Errorf("key generation failed: %w", err)
		}
		newPub, newPriv := qcrypto.KeypairFromSeed(newSeed)
		newAddr := qcrypto.DeriveAddress(newPub)
		phrase, err := seedToMnemonic(newSeed)
		if err != nil {
			return fmt.Errorf("mnemonic: %w", err)
		}
		newPass, err := readGeneratePassword("")
		if err != nil {
			return err
		}
		if err := qcrypto.SaveWallet(*newWalletPath, newPub, newPriv, newPass); err != nil {
			return fmt.Errorf("failed to save new wallet: %w", err)
		}

		fmt.Printf("\n✅ New wallet created\n")
		fmt.Printf("   Old address: %s\n", oldAddr.String())
		fmt.Printf("   New address: %s\n", newAddr.String())
		fmt.Printf("   Saved to:    %s\n", *newWalletPath)
		fmt.Fprintf(os.Stderr, "\n   New wallet seed hex (64 chars, no 0x):\n   %x\n", newSeed[:])
		fmt.Fprintf(os.Stderr, "\n   New wallet 24-word recovery phrase:\n   %s\n", phrase)
		fmt.Fprintf(os.Stderr, "\n⚠️  Save the phrase or seed hex now. It was generated fresh and has never touched an EVM key.\n")

		if !*doTransfer {
			fmt.Fprintf(os.Stderr, "\nNo --transfer requested. Move funds with:\n")
			fmt.Fprintf(os.Stderr, "   sqc-wallet send --wallet %s --to %s --amount <SPLD> --node %s\n",
				*oldWalletPath, newAddr.String(), *nodeAddr)
			return nil
		}

		return doMigrateTransfer(*nodeAddr, oldAddr, oldPriv, newAddr, *feeUnits)
	}
}

// doMigrateTransfer moves the old wallet's entire spendable balance (minus
// fee) to the new address in one signed transfer.
func doMigrateTransfer(nodeAddr string, oldAddr qcrypto.Address, oldPriv *qcrypto.PrivateKey, newAddr qcrypto.Address, feeUnits int64) error {
	fmt.Fprintf(os.Stderr, "\nTransferring the old wallet's full balance to the new address...\n")
	balance, err := fetchBalanceUnits(nodeAddr, oldAddr)
	if err != nil {
		return fmt.Errorf("fetch old wallet balance: %w", err)
	}
	fee := big.NewInt(feeUnits)
	amount := new(big.Int).Sub(balance, fee)
	if amount.Sign() <= 0 {
		return fmt.Errorf("old wallet balance (%s units) does not cover the fee (%s units); nothing to transfer",
			balance, fee)
	}
	nonce, err := fetchNonce(walletHTTP, nodeAddr, oldAddr)
	if err != nil {
		return fmt.Errorf("fetch old wallet nonce: %w", err)
	}
	tx := types.NewTransfer(oldAddr, newAddr, amount, fee, nonce, time.Now().Unix())
	if err := tx.Sign(oldPriv, nonce == 0); err != nil {
		return fmt.Errorf("signing migration transfer failed: %w", err)
	}
	txData, err := json.Marshal(tx)
	if err != nil {
		return fmt.Errorf("failed to marshal migration transfer: %w", err)
	}
	resp, err := walletHTTP.Post(strings.TrimRight(nodeAddr, "/")+"/tx/submit", "application/json", bytes.NewReader(txData))
	if err != nil {
		return fmt.Errorf("failed to submit migration transfer: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("submit: %w", err)
	}
	if err := submitError(resp.StatusCode, body); err != nil {
		return fmt.Errorf("migration transfer rejected: %w", err)
	}
	txHash, _ := tx.FullHash()
	fmt.Printf("\n✅ Migration transfer submitted\n")
	fmt.Printf("   From:   %s\n", oldAddr.String())
	fmt.Printf("   To:     %s\n", newAddr.String())
	fmt.Printf("   Amount: %s units (balance minus fee)\n", amount)
	fmt.Printf("   Hash:   %x\n", txHash[:])
	return nil
}

// fetchBalanceUnits queries an address's balance in smallest units.
func fetchBalanceUnits(nodeAddr string, addr qcrypto.Address) (*big.Int, error) {
	url := fmt.Sprintf("%s/balance/%s", strings.TrimRight(nodeAddr, "/"), addr.String())
	resp, err := walletHTTP.Get(url)
	if err != nil {
		return nil, fmt.Errorf("node request failed: %w", err)
	}
	defer resp.Body.Close()
	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	if errMsg, ok := result["error"]; ok {
		return nil, fmt.Errorf("node error: %s", errMsg)
	}
	balance, ok := new(big.Int).SetString(result["balance"], 10)
	if !ok {
		return nil, fmt.Errorf("invalid balance: %s", result["balance"])
	}
	return balance, nil
}
