// Splendor Quantum Chain — Wallet CLI
// All keys use ML-DSA-65 (FIPS-204). No ECDSA anywhere.
//
// Commands:
//   sqc-wallet generate  --out wallet.sqwallet
//   sqc-wallet address   --wallet wallet.sqwallet
//   sqc-wallet balance   --wallet wallet.sqwallet --node http://localhost:8080
//   sqc-wallet send      --wallet wallet.sqwallet --to SQ:... --amount 10 --node http://localhost:8080
//   sqc-wallet export-key --wallet wallet.sqwallet --out dilithium_key.bin
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})
	zerolog.SetGlobalLevel(zerolog.InfoLevel)

	root := &cobra.Command{
		Use:   "sqc-wallet",
		Short: "Splendor Quantum Chain wallet CLI",
		Long: `sqc-wallet manages Splendor Quantum Chain wallets.
Keys use ML-DSA-65 (NIST FIPS 204).
Addresses are derived as SHA3-256(ml_dsa_65_public_key) — no ECDSA.`,
	}

	root.AddCommand(
		cmdGenerate(),
		cmdAddress(),
		cmdPubkey(),
		cmdBalance(),
		cmdSend(),
		cmdExportKey(),
	)

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// ── generate ──────────────────────────────────────────────────────────────

func cmdGenerate() *cobra.Command {
	var outPath string
	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate a new ML-DSA-65 quantum wallet",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Generate keypair
			fmt.Fprintln(os.Stderr, "Generating ML-DSA-65 (FIPS-204) keypair...")
			pub, priv, err := qcrypto.GenerateKeypair()
			if err != nil {
				return fmt.Errorf("key generation failed: %w", err)
			}

			addr := qcrypto.DeriveAddress(pub)

			// Get password
			pass, err := readPassword("Enter wallet password: ")
			if err != nil {
				return fmt.Errorf("password read error: %w", err)
			}
			pass2, err := readPassword("Confirm password: ")
			if err != nil {
				return fmt.Errorf("password confirm error: %w", err)
			}
			if string(pass) != string(pass2) {
				return fmt.Errorf("passwords do not match")
			}

			// Save encrypted wallet
			if err := qcrypto.SaveWallet(outPath, pub, priv, pass); err != nil {
				return fmt.Errorf("failed to save wallet: %w", err)
			}

			fmt.Printf("\n✅ Wallet created successfully\n")
			fmt.Printf("   Address:  %s\n", addr.String())
			fmt.Printf("   Saved to: %s\n", outPath)
			fmt.Printf("\n⚠️  IMPORTANT: Back up %s securely.\n", outPath)
			fmt.Printf("   There is no seed phrase. The wallet file IS the key.\n")
			fmt.Printf("   Private key size: %d bytes (ML-DSA-65)\n", qcrypto.PrivateKeySize)

			return nil
		},
	}
	cmd.Flags().StringVarP(&outPath, "out", "o", "wallet.sqwallet", "Output wallet file path")
	return cmd
}

// ── address ───────────────────────────────────────────────────────────────

func cmdAddress() *cobra.Command {
	var walletPath string
	cmd := &cobra.Command{
		Use:   "address",
		Short: "Show the SQ address for a wallet",
		RunE: func(cmd *cobra.Command, args []string) error {
			pub, _, err := loadWalletInteractive(walletPath)
			if err != nil {
				return err
			}
			addr := qcrypto.DeriveAddress(pub)
			fmt.Println(addr.String())
			return nil
		},
	}
	cmd.Flags().StringVarP(&walletPath, "wallet", "w", "wallet.sqwallet", "Wallet file path")
	return cmd
}

// ── pubkey ────────────────────────────────────────────────────────────────

// cmdPubkey prints the hex-encoded ML-DSA-65 public key.
// This is used by install.sh to populate genesis.json's validator_pubkey_hex field.
func cmdPubkey() *cobra.Command {
	var walletPath string
	cmd := &cobra.Command{
		Use:   "pubkey",
		Short: "Show the ML-DSA-65 public key in hex (for genesis.json)",
		Long: `Prints the 1952-byte ML-DSA-65 public key in hexadecimal.
This is needed for the validator_pubkey_hex field in genesis.json.
The pubkey is 3904 hex characters (1952 bytes × 2).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			pub, _, err := loadWalletInteractive(walletPath)
			if err != nil {
				return err
			}
			pubBytes := pub.Bytes()
			// Print as hex — no newline prefix, pure hex for easy capture
			fmt.Printf("%x\n", pubBytes)
			return nil
		},
	}
	cmd.Flags().StringVarP(&walletPath, "wallet", "w", "wallet.sqwallet", "Wallet file path")
	return cmd
}

// ── balance ───────────────────────────────────────────────────────────────

func cmdBalance() *cobra.Command {
	var walletPath, nodeAddr string
	cmd := &cobra.Command{
		Use:   "balance",
		Short: "Query account balance from the node",
		RunE: func(cmd *cobra.Command, args []string) error {
			pub, _, err := loadWalletInteractive(walletPath)
			if err != nil {
				return err
			}
			addr := qcrypto.DeriveAddress(pub)

			url := fmt.Sprintf("%s/balance/%s", nodeAddr, addr.String())
			resp, err := http.Get(url)
			if err != nil {
				return fmt.Errorf("node request failed: %w", err)
			}
			defer resp.Body.Close()

			var result map[string]string
			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				return fmt.Errorf("failed to parse response: %w", err)
			}

			if errMsg, ok := result["error"]; ok {
				return fmt.Errorf("node error: %s", errMsg)
			}

			balanceStr := result["balance"]
			balance, ok := new(big.Int).SetString(balanceStr, 10)
			if !ok {
				return fmt.Errorf("invalid balance: %s", balanceStr)
			}

			// Convert to SPLD (1 SPLD = 10^18 units)
			divisor := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
			spld := new(big.Float).Quo(
				new(big.Float).SetInt(balance),
				new(big.Float).SetInt(divisor),
			)

			fmt.Printf("Address: %s\n", addr.String())
			fmt.Printf("Balance: %s SPLD (%s smallest units)\n", spld.Text('f', 6), balanceStr)
			return nil
		},
	}
	cmd.Flags().StringVarP(&walletPath, "wallet", "w", "wallet.sqwallet", "Wallet file path")
	cmd.Flags().StringVar(&nodeAddr, "node", "http://localhost:8080", "Node RPC address")
	return cmd
}

// ── send ──────────────────────────────────────────────────────────────────

func cmdSend() *cobra.Command {
	var walletPath, nodeAddr, toAddr, amountSPLD, memo string
	var feeUnits int64

	cmd := &cobra.Command{
		Use:   "send",
		Short: "Send SPLD to another address",
		RunE: func(cmd *cobra.Command, args []string) error {
			pub, priv, err := loadWalletInteractive(walletPath)
			if err != nil {
				return err
			}
			fromAddr := qcrypto.DeriveAddress(pub)

			// Parse recipient
			to, err := qcrypto.AddressFromString(toAddr)
			if err != nil {
				return fmt.Errorf("invalid recipient address: %w", err)
			}

			// Parse amount (SPLD → smallest units)
			amountFloat, ok := new(big.Float).SetString(amountSPLD)
			if !ok {
				return fmt.Errorf("invalid amount: %s", amountSPLD)
			}
			divisor := new(big.Float).SetInt(
				new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil),
			)
			amountUnitsFloat := new(big.Float).Mul(amountFloat, divisor)
			amountUnits, _ := amountUnitsFloat.Int(nil)

			// Get current nonce from node
			nonceURL := fmt.Sprintf("%s/nonce/%s", nodeAddr, fromAddr.String())
			nonceResp, err := http.Get(nonceURL)
			if err != nil {
				return fmt.Errorf("failed to get nonce: %w", err)
			}
			var nonceResult map[string]interface{}
			json.NewDecoder(nonceResp.Body).Decode(&nonceResult)
			nonceResp.Body.Close()

			nonce := uint64(0)
			if v, ok := nonceResult["nonce"].(float64); ok {
				nonce = uint64(v)
			}

			// Build transaction
			tx := types.NewTransfer(
				fromAddr,
				to,
				amountUnits,
				big.NewInt(feeUnits),
				nonce,
				time.Now().Unix(),
			)
			if memo != "" {
				tx.Data = []byte(memo)
			}

			// Sign (include public key on first tx, i.e. nonce==0)
			includeKey := nonce == 0
			if err := tx.Sign(priv, includeKey); err != nil {
				return fmt.Errorf("signing failed: %w", err)
			}

			// Submit to node
			txData, err := json.Marshal(tx)
			if err != nil {
				return fmt.Errorf("failed to marshal tx: %w", err)
			}
			submitURL := fmt.Sprintf("%s/tx/submit", nodeAddr)
			resp, err := http.Post(submitURL, "application/json", bytes.NewReader(txData))
			if err != nil {
				return fmt.Errorf("failed to submit tx: %w", err)
			}
			defer resp.Body.Close()

			var submitResult map[string]string
			json.NewDecoder(resp.Body).Decode(&submitResult)

			if errMsg, ok := submitResult["error"]; ok {
				return fmt.Errorf("tx rejected: %s", errMsg)
			}

			txHash, _ := tx.FullHash()
			fmt.Printf("✅ Transaction submitted\n")
			fmt.Printf("   From:   %s\n", fromAddr.String())
			fmt.Printf("   To:     %s\n", to.String())
			fmt.Printf("   Amount: %s SPLD\n", amountSPLD)
			fmt.Printf("   Nonce:  %d\n", nonce)
			fmt.Printf("   Hash:   %x\n", txHash[:])
			return nil
		},
	}
	cmd.Flags().StringVarP(&walletPath, "wallet", "w", "wallet.sqwallet", "Wallet file path")
	cmd.Flags().StringVar(&nodeAddr, "node", "http://localhost:8080", "Node RPC address")
	cmd.Flags().StringVar(&toAddr, "to", "", "Recipient SQ address (required)")
	cmd.Flags().StringVar(&amountSPLD, "amount", "", "Amount in SPLD (e.g. 10.5) (required)")
	cmd.Flags().Int64Var(&feeUnits, "fee", 1000, "Transaction fee in smallest SPLD units")
	cmd.Flags().StringVar(&memo, "memo", "", "Optional memo")
	cmd.MarkFlagRequired("to")
	cmd.MarkFlagRequired("amount")
	return cmd
}

// ── export-key ────────────────────────────────────────────────────────────

func cmdExportKey() *cobra.Command {
	var walletPath, outPath string
	cmd := &cobra.Command{
		Use:   "export-key",
		Short: "Export raw ML-DSA-65 private key bytes (for NFC card provisioning)",
		Long: `Exports the 4032-byte ML-DSA-65 private key to a binary file.
This file is used to provision a physical NFC smart card secure element.
KEEP THIS FILE SECURE. Delete it after card provisioning.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, priv, err := loadWalletInteractive(walletPath)
			if err != nil {
				return err
			}

			privBytes := priv.Bytes()
			if len(privBytes) != qcrypto.PrivateKeySize {
				return fmt.Errorf("unexpected key size: %d", len(privBytes))
			}

			if err := os.WriteFile(outPath, privBytes, 0600); err != nil {
				return fmt.Errorf("failed to write key file: %w", err)
			}

			// Zero out in-memory copy
			for i := range privBytes {
				privBytes[i] = 0
			}

			hash := qcrypto.Hash32(privBytes)
			fmt.Printf("✅ ML-DSA-65 private key exported\n")
			fmt.Printf("   File:  %s\n", outPath)
			fmt.Printf("   Size:  %d bytes\n", qcrypto.PrivateKeySize)
			fmt.Printf("   SHA3:  %x\n", hash[:])
			fmt.Printf("\n⚠️  DELETE this file after provisioning your NFC card.\n")
			return nil
		},
	}
	cmd.Flags().StringVarP(&walletPath, "wallet", "w", "wallet.sqwallet", "Wallet file path")
	cmd.Flags().StringVarP(&outPath, "out", "o", "dilithium_key.bin", "Output binary file path")
	return cmd
}

// ── helpers ───────────────────────────────────────────────────────────────

func loadWalletInteractive(path string) (*qcrypto.PublicKey, *qcrypto.PrivateKey, error) {
	pass, err := readPassword(fmt.Sprintf("Password for %s: ", path))
	if err != nil {
		return nil, nil, fmt.Errorf("password read error: %w", err)
	}
	pub, priv, err := qcrypto.LoadWallet(path, pass)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open wallet: %w", err)
	}
	return pub, priv, nil
}

func readPassword(prompt string) ([]byte, error) {
	fmt.Fprint(os.Stderr, prompt)
	// Use terminal raw mode for secure password entry (no echo)
	pass, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Fprintln(os.Stderr) // newline after hidden input
	if err != nil {
		// Fallback for non-terminal environments (e.g. piped input)
		var line string
		fmt.Scanln(&line)
		return []byte(line), nil
	}
	return pass, nil
}
