// Splendor Quantum Chain — Wallet CLI
// All keys use ML-DSA-65 (FIPS-204). No ECDSA anywhere.
//
// Commands:
//   sqc-wallet generate  --out wallet.sqwallet
//   sqc-wallet generate  --out wallet.sqwallet --seed "24 words or 64 hex"
//   sqc-wallet address   --wallet wallet.sqwallet
//   sqc-wallet balance   --wallet wallet.sqwallet --node http://localhost:8080
//   sqc-wallet send      --wallet wallet.sqwallet --to SQ:... --amount 10 --node http://localhost:8080
//   sqc-wallet export-key --wallet wallet.sqwallet --out mldsa_key.bin
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"strings"
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
		cmdMigrate(),
	)

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// ── generate ──────────────────────────────────────────────────────────────

func cmdGenerate() *cobra.Command {
	var outPath, seedIn, passFile string
	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate a new ML-DSA-65 wallet (recovery phrase on stderr)",
		Long: `Creates an ML-DSA-65 key from a 32-byte seed — the same derivation
the browser extension uses. You get a 64-character seed hex and a 24-word
BIP39 phrase that import into the extension as the same SQ: address.

Pass --seed to restore an existing phrase or hex instead of creating a new one.
Wallets created before this (raw GenerateKeypair) have no seed and cannot
gain one.

Pass --pass-file to read the encryption password from a mode-0600 file
instead of prompting. The file must not be readable by other users.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := refuseExistingWallet(outPath); err != nil {
				return err
			}
			var seed [qcrypto.SeedSize]byte
			var err error
			if strings.TrimSpace(seedIn) != "" {
				seed, err = parseSeedInput(seedIn)
				if err != nil {
					return err
				}
				fmt.Fprintln(os.Stderr, "Restoring ML-DSA-65 key from seed...")
			} else {
				fmt.Fprintln(os.Stderr, "Generating ML-DSA-65 seed and keypair...")
				seed, err = qcrypto.GenerateSeed()
				if err != nil {
					return fmt.Errorf("key generation failed: %w", err)
				}
			}

			pub, priv := qcrypto.KeypairFromSeed(seed)
			addr := qcrypto.DeriveAddress(pub)
			phrase, err := seedToMnemonic(seed)
			if err != nil {
				return fmt.Errorf("mnemonic: %w", err)
			}

			pass, err := readGeneratePassword(passFile)
			if err != nil {
				return err
			}

			if err := qcrypto.SaveWallet(outPath, pub, priv, pass); err != nil {
				return fmt.Errorf("failed to save wallet: %w", err)
			}

			fmt.Printf("\n✅ Wallet created successfully\n")
			fmt.Printf("   Address:  %s\n", addr.String())
			fmt.Printf("   Saved to: %s\n", outPath)
			fmt.Fprintf(os.Stderr, "\n   Seed hex (64 chars, no 0x):\n   %x\n", seed[:])
			fmt.Fprintf(os.Stderr, "\n   24-word recovery phrase:\n   %s\n", phrase)
			fmt.Fprintf(os.Stderr, "\n⚠️  Save the phrase or seed hex. Anyone with either controls this address.\n")
			fmt.Fprintf(os.Stderr, "   The .sqwallet file is an encrypted copy of the same key.\n")
			fmt.Fprintf(os.Stderr, "   Paste the phrase or hex into the extension to import this account.\n")

			return nil
		},
	}
	cmd.Flags().StringVarP(&outPath, "out", "o", "wallet.sqwallet", "Output wallet file path")
	cmd.Flags().StringVar(&seedIn, "seed", "", "Restore from 24-word phrase or 64-char seed hex")
	cmd.Flags().StringVar(&passFile, "pass-file", "", "Read the wallet password from a file (must not be readable by other users)")
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
	var walletPath, nodeAddr, toAddr, amountSPLD, memo, chainID string
	var feeUnits int64
	var nonce uint64

	cmd := &cobra.Command{
		Use:          "send",
		Short:        "Send SPLD to another address",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if chainID != "" {
				if err := types.SetClientChainID(chainID); err != nil {
					return fmt.Errorf("invalid --chain-id: %w", err)
				}
			}
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

			if !cmd.Flags().Changed("nonce") {
				nonce, err = fetchNonce(walletHTTP, nodeAddr, fromAddr)
				if err != nil {
					return err
				}
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
			resp, err := walletHTTP.Post(submitURL, "application/json", bytes.NewReader(txData))
			if err != nil {
				return fmt.Errorf("failed to submit tx: %w", err)
			}
			defer resp.Body.Close()
			submitBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			if err != nil {
				return fmt.Errorf("submit: %w", err)
			}
			if err := submitError(resp.StatusCode, submitBody); err != nil {
				return err
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
	cmd.Flags().StringVar(&chainID, "chain-id", "", "Chain ID to sign for (e.g. splendor-quantum-testnet-1); defaults to mainnet")
	cmd.Flags().StringVar(&toAddr, "to", "", "Recipient SQ address (required)")
	cmd.Flags().StringVar(&amountSPLD, "amount", "", "Amount in SPLD (e.g. 10.5) (required)")
	cmd.Flags().Int64Var(&feeUnits, "fee", 1000, "Transaction fee in smallest SPLD units")
	cmd.Flags().Uint64Var(&nonce, "nonce", 0, "Account nonce (default: fetch from the node)")
	cmd.Flags().StringVar(&memo, "memo", "", "Optional memo")
	cmd.MarkFlagRequired("to")
	cmd.MarkFlagRequired("amount")
	return cmd
}

// ── migrate ───────────────────────────────────────────────────────────────
//
// Splendor's original onboarding path let a wallet derive its ML-DSA-65 seed
// from an EVM personal_sign signature (seed = SHA3-256(sig)) so a MetaMask
// user could get an SQ: address without any new key material. The improvement
// plan (Sept 2026, Section 6) calls that out as a real weakness: the
// post-quantum key's security is then bounded by classical secp256k1 — break
// or leak the EVM key (or the signature itself, which travels in the
// derivation flow) and the ML-DSA-65 key is recomputed trivially, no quantum
// computer required, defeating the entire point of a PQ chain.
//
// This command does not try to detect whether an existing wallet file was
// EVM-derived — the .sqwallet format has no such marker, deliberately, since
// derivation happens client-side and the file only ever holds the resulting
// ML-DSA-65 key, indistinguishable from one made with `generate --seed`. What
// it DOES give any holder of a wallet they no longer trust is a concrete,
// one-command path off it: a brand-new wallet from fresh CSPRNG entropy
// (never accepts --seed — that would defeat the purpose) and, optionally, a
// move of the old wallet's entire balance to it in one signed transfer.
func cmdMigrate() *cobra.Command {
	var oldWalletPath, newWalletPath, nodeAddr, chainID string
	var feeUnits int64
	var doTransfer bool

	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Create a fresh, natively-random wallet and (optionally) move funds off an old one",
		Long: `Generates a brand-new ML-DSA-65 wallet from fresh CSPRNG entropy — never
derived from an EVM signature — and prints its address.

This is the recommended move for any wallet created via EVM-signature
derivation (the SQDP-1 "evm-derived" path, or the SDK's seedFromEvmSignature):
that key's post-quantum security is only as strong as the classical
secp256k1 key that produced it. See Splendor Chain Improvement Plan
(Sept 2026), Section 6 - Wallet Security.

With --transfer and --node, the ENTIRE balance of --old-wallet is moved to
the new wallet's address in one signed transfer (amount = balance - fee).
Without --transfer, only the new wallet is created; move funds yourself with
'sqc-wallet send' once you are ready.`,
		RunE: runMigrate(&oldWalletPath, &newWalletPath, &nodeAddr, &chainID, &feeUnits, &doTransfer),
	}
	cmd.Flags().StringVar(&oldWalletPath, "old-wallet", "wallet.sqwallet", "Wallet file to migrate away from")
	cmd.Flags().StringVar(&newWalletPath, "new-wallet", "wallet.migrated.sqwallet", "Output path for the new, natively-random wallet")
	cmd.Flags().StringVar(&nodeAddr, "node", "http://localhost:8080", "Node RPC address (needed for --transfer)")
	cmd.Flags().StringVar(&chainID, "chain-id", "", "Chain ID to sign for (e.g. splendor-quantum-testnet-1); defaults to mainnet")
	cmd.Flags().Int64Var(&feeUnits, "fee", 1000, "Transaction fee in smallest SPLD units, subtracted from the transferred balance")
	cmd.Flags().BoolVar(&doTransfer, "transfer", false, "Also move the old wallet's entire balance to the new address")
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
	cmd.Flags().StringVarP(&outPath, "out", "o", "mldsa_key.bin", "Output binary file path")
	return cmd
}

var walletHTTP = &http.Client{Timeout: 20 * time.Second}

func fetchNonce(client *http.Client, node string, addr qcrypto.Address) (uint64, error) {
	if client == nil {
		client = walletHTTP
	}
	resp, err := client.Get(strings.TrimRight(node, "/") + "/nonce/" + addr.String())
	if err != nil {
		return 0, fmt.Errorf("failed to get nonce: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, fmt.Errorf("nonce: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("nonce: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var result struct {
		Nonce *uint64 `json:"nonce"`
		Error string  `json:"error"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, fmt.Errorf("nonce: not JSON: %w", err)
	}
	if result.Error != "" {
		return 0, fmt.Errorf("nonce: %s", result.Error)
	}
	if result.Nonce == nil {
		return 0, fmt.Errorf("nonce: missing nonce field")
	}
	return *result.Nonce, nil
}

func submitError(status int, body []byte) error {
	var result map[string]string
	_ = json.Unmarshal(body, &result)
	if msg := result["error"]; msg != "" {
		return fmt.Errorf("tx rejected: %s", msg)
	}
	if status != http.StatusOK && status != http.StatusAccepted {
		return fmt.Errorf("tx rejected: HTTP %d: %s", status, strings.TrimSpace(string(body)))
	}
	return nil
}

// ── helpers ───────────────────────────────────────────────────────────────

func readGeneratePassword(passFile string) ([]byte, error) {
	if strings.TrimSpace(passFile) != "" {
		if err := qcrypto.CheckKeyFilePermissions(passFile); err != nil {
			return nil, err
		}
		b, err := os.ReadFile(passFile)
		if err != nil {
			return nil, fmt.Errorf("read --pass-file: %w", err)
		}
		pass := bytes.TrimRight(b, "\r\n")
		if len(pass) == 0 {
			return nil, fmt.Errorf("--pass-file %s is empty", passFile)
		}
		return pass, nil
	}

	pass, err := readPassword("Enter wallet password: ")
	if err != nil {
		return nil, fmt.Errorf("password read error: %w", err)
	}
	pass2, err := readPassword("Confirm password: ")
	if err != nil {
		return nil, fmt.Errorf("password confirm error: %w", err)
	}
	if string(pass) != string(pass2) {
		return nil, fmt.Errorf("passwords do not match")
	}
	if len(pass) == 0 {
		return nil, fmt.Errorf("empty password")
	}
	return pass, nil
}

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
		if _, serr := fmt.Scanln(&line); serr != nil && line == "" {
			return nil, fmt.Errorf("no password on stdin: %w", serr)
		}
		return []byte(line), nil
	}
	return pass, nil
}
