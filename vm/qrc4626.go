// Package vm — QRC-4626: Splendor Quantum Chain Tokenised Vault Standard.
//
// QRC-4626 is the quantum-native equivalent of ERC-4626 (Tokenised Vaults).
// A vault accepts deposits of an underlying asset (any QRC-20 token) and
// issues shares representing proportional ownership of the vault's assets.
// As the vault earns yield, each share becomes worth more of the underlying asset.
//
// This is the foundational DeFi primitive for:
//   - Yield aggregators (auto-compounding vaults)
//   - Lending protocol interest-bearing tokens
//   - Liquid staking (wrap staked SPLD into tradeable vault shares)
//   - Index funds (diversified asset baskets)
//
// Interface:
//   asset() → sqaddress                                  underlying token
//   totalAssets() → uint256                              total underlying managed
//   convertToShares(assets uint256) → uint256            asset → shares (no rounding)
//   convertToAssets(shares uint256) → uint256            shares → asset (no rounding)
//   maxDeposit(receiver sqaddress) → uint256             max assets depositable
//   previewDeposit(assets uint256) → uint256             shares minted for deposit
//   deposit(assets uint256, receiver sqaddress) → shares actually deposited
//   maxMint(receiver sqaddress) → uint256                max shares mintable
//   previewMint(shares uint256) → uint256                assets needed for shares
//   mint(shares uint256, receiver sqaddress) → assets    mint exact shares
//   maxWithdraw(owner sqaddress) → uint256               max assets withdrawable
//   previewWithdraw(assets uint256) → uint256            shares burned for withdraw
//   withdraw(assets, receiver, owner sqaddress) → shares withdrawn
//   maxRedeem(owner sqaddress) → uint256                 max shares redeemable
//   previewRedeem(shares uint256) → uint256              assets for redeeming shares
//   redeem(shares, receiver, owner sqaddress) → assets  redeem shares for assets
//
// All hashing: SHA3-256. All addresses: SQ: format. No ECDSA. No keccak.
package vm

import (
	"encoding/json"
	"fmt"
	"math/big"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
)

// ── Storage slot derivation ────────────────────────────────────────────────────
//
// share_balance[addr]   = SHA3-256("vault:share:" + addr)
// allowance[own][sp]    = SHA3-256("vault:allowance:" + owner + spender)
// total_shares          = SHA3-256("vault:total_shares")
// total_assets          = SHA3-256("vault:total_assets")
// asset_address         = SHA3-256("vault:asset")
// name                  = SHA3-256("vault:name")
// symbol                = SHA3-256("vault:symbol")

var (
	slotVaultTotalShares = qcrypto.Hash32([]byte("vault:total_shares"))
	slotVaultTotalAssets = qcrypto.Hash32([]byte("vault:total_assets"))
	slotVaultAsset       = qcrypto.Hash32([]byte("vault:asset"))
	slotVaultName        = qcrypto.Hash32([]byte("vault:name"))
	slotVaultSymbol      = qcrypto.Hash32([]byte("vault:symbol"))
)

func vaultShareSlot(addr qcrypto.Address) [32]byte {
	return qcrypto.Hash32(append([]byte("vault:share:"), addr[:]...))
}

func vaultAllowanceSlot(owner, spender qcrypto.Address) [32]byte {
	key := append([]byte("vault:allowance:"), owner[:]...)
	key = append(key, spender[:]...)
	return qcrypto.Hash32(key)
}

// ── QRC4626Vault ──────────────────────────────────────────────────────────────

// QRC4626Vault is the native Go implementation of the QRC-4626 tokenised vault.
// It manages share issuance and redemption against an underlying QRC-20 asset.
type QRC4626Vault struct {
	stateDB  *state.StateDB
	contract qcrypto.Address
	// Underlying QRC-20 token handle (loaded lazily)
}

// NewQRC4626Vault returns a handle to an existing vault contract.
func NewQRC4626Vault(stateDB *state.StateDB, contract qcrypto.Address) *QRC4626Vault {
	return &QRC4626Vault{stateDB: stateDB, contract: contract}
}

// DeployQRC4626 deploys a new tokenised vault.
// assetAddr is the underlying QRC-20 token address.
func DeployQRC4626(
	stateDB *state.StateDB,
	deployer qcrypto.Address,
	name, symbol string,
	assetAddr qcrypto.Address,
	deployerNonce uint64,
) (qcrypto.Address, error) {
	nonce := []byte(fmt.Sprintf("%d", deployerNonce))
	input := append(deployer[:], nonce...)
	input = append(input, []byte("qrc4626")...)
	hash := qcrypto.Hash32(input)
	var contractAddr qcrypto.Address
	copy(contractAddr[:], hash[:])

	v := NewQRC4626Vault(stateDB, contractAddr)
	if err := v.setString(slotVaultName, name); err != nil {
		return qcrypto.Address{}, err
	}
	if err := v.setString(slotVaultSymbol, symbol); err != nil {
		return qcrypto.Address{}, err
	}
	// Store asset address as 32 bytes
	if err := stateDB.SetStorageValue(contractAddr, slotVaultAsset, assetAddr[:]); err != nil {
		return qcrypto.Address{}, err
	}
	if err := v.setBigInt(slotVaultTotalShares, big.NewInt(0)); err != nil {
		return qcrypto.Address{}, err
	}
	if err := v.setBigInt(slotVaultTotalAssets, big.NewInt(0)); err != nil {
		return qcrypto.Address{}, err
	}
	return contractAddr, nil
}

// ── Metadata ──────────────────────────────────────────────────────────────────

func (v *QRC4626Vault) Name() (string, error)   { return v.getString(slotVaultName) }
func (v *QRC4626Vault) Symbol() (string, error) { return v.getString(slotVaultSymbol) }

// Asset returns the address of the underlying QRC-20 token.
func (v *QRC4626Vault) Asset() (qcrypto.Address, error) {
	b, err := v.stateDB.GetStorageValue(v.contract, slotVaultAsset)
	if err != nil || len(b) < qcrypto.AddressSize {
		return qcrypto.Address{}, fmt.Errorf("vault: asset not set")
	}
	var addr qcrypto.Address
	copy(addr[:], b)
	return addr, nil
}

// TotalAssets returns the total underlying assets managed by the vault.
func (v *QRC4626Vault) TotalAssets() (*big.Int, error) {
	return v.getBigInt(slotVaultTotalAssets)
}

// TotalShares returns the total shares (vault tokens) issued.
func (v *QRC4626Vault) TotalShares() (*big.Int, error) {
	return v.getBigInt(slotVaultTotalShares)
}

// ── Share accounting ──────────────────────────────────────────────────────────

// ShareBalance returns the vault share balance of an address.
func (v *QRC4626Vault) ShareBalance(addr qcrypto.Address) (*big.Int, error) {
	return v.getBigInt(vaultShareSlot(addr))
}

// ConvertToShares converts an asset amount to the equivalent shares.
// Uses floor division (conservative — protects vault from inflation attacks).
func (v *QRC4626Vault) ConvertToShares(assets *big.Int) (*big.Int, error) {
	totalAssets, err := v.TotalAssets()
	if err != nil {
		return nil, err
	}
	totalShares, err := v.TotalShares()
	if err != nil {
		return nil, err
	}

	// If vault is empty: 1 asset = 1 share (initial exchange rate)
	if totalAssets.Sign() == 0 || totalShares.Sign() == 0 {
		return new(big.Int).Set(assets), nil
	}

	// shares = assets * totalShares / totalAssets (floor)
	result := new(big.Int).Mul(assets, totalShares)
	result.Div(result, totalAssets)
	return result, nil
}

// ConvertToAssets converts a share amount to the equivalent assets.
func (v *QRC4626Vault) ConvertToAssets(shares *big.Int) (*big.Int, error) {
	totalAssets, err := v.TotalAssets()
	if err != nil {
		return nil, err
	}
	totalShares, err := v.TotalShares()
	if err != nil {
		return nil, err
	}

	if totalShares.Sign() == 0 {
		return new(big.Int).Set(shares), nil
	}

	// assets = shares * totalAssets / totalShares (floor)
	result := new(big.Int).Mul(shares, totalAssets)
	result.Div(result, totalShares)
	return result, nil
}

// PreviewDeposit returns the shares that would be minted for a given asset deposit.
func (v *QRC4626Vault) PreviewDeposit(assets *big.Int) (*big.Int, error) {
	return v.ConvertToShares(assets)
}

// PreviewMint returns the assets needed to mint a given number of shares.
func (v *QRC4626Vault) PreviewMint(shares *big.Int) (*big.Int, error) {
	totalAssets, err := v.TotalAssets()
	if err != nil {
		return nil, err
	}
	totalShares, err := v.TotalShares()
	if err != nil {
		return nil, err
	}

	if totalShares.Sign() == 0 {
		return new(big.Int).Set(shares), nil
	}

	// assets = ceil(shares * totalAssets / totalShares)
	result := new(big.Int).Mul(shares, totalAssets)
	remainder := new(big.Int)
	result.DivMod(result, totalShares, remainder)
	if remainder.Sign() > 0 {
		result.Add(result, big.NewInt(1)) // ceil
	}
	return result, nil
}

// PreviewWithdraw returns the shares burned to withdraw a given asset amount.
func (v *QRC4626Vault) PreviewWithdraw(assets *big.Int) (*big.Int, error) {
	totalAssets, err := v.TotalAssets()
	if err != nil {
		return nil, err
	}
	totalShares, err := v.TotalShares()
	if err != nil {
		return nil, err
	}

	if totalAssets.Sign() == 0 {
		return new(big.Int).Set(assets), nil
	}

	// shares = ceil(assets * totalShares / totalAssets)
	result := new(big.Int).Mul(assets, totalShares)
	remainder := new(big.Int)
	result.DivMod(result, totalAssets, remainder)
	if remainder.Sign() > 0 {
		result.Add(result, big.NewInt(1))
	}
	return result, nil
}

// PreviewRedeem returns the assets received for redeeming a given number of shares.
func (v *QRC4626Vault) PreviewRedeem(shares *big.Int) (*big.Int, error) {
	return v.ConvertToAssets(shares)
}

// ── Deposit / Mint ────────────────────────────────────────────────────────────

// Deposit accepts `assets` from `caller` and mints shares to `receiver`.
// The caller must have already approved the vault to spend their asset tokens.
// Returns the number of shares minted.
func (v *QRC4626Vault) Deposit(caller, receiver qcrypto.Address, assets *big.Int) (*big.Int, error) {
	if assets.Sign() <= 0 {
		return nil, fmt.Errorf("vault deposit: assets must be positive")
	}
	if receiver.IsZero() {
		return nil, fmt.Errorf("vault deposit: cannot deposit to zero address")
	}

	shares, err := v.PreviewDeposit(assets)
	if err != nil {
		return nil, err
	}
	if shares.Sign() == 0 {
		return nil, fmt.Errorf("vault deposit: zero shares would be minted (too small deposit)")
	}

	// Transfer assets from caller to vault
	assetAddr, err := v.Asset()
	if err != nil {
		return nil, err
	}
	asset := NewQRC20Token(v.stateDB, assetAddr)
	if err := asset.Transfer(caller, v.contract, assets); err != nil {
		return nil, fmt.Errorf("vault deposit: asset transfer failed: %w", err)
	}

	// Mint shares to receiver
	if err := v.mintShares(receiver, shares); err != nil {
		return nil, err
	}

	// Update total assets
	totalAssets, _ := v.TotalAssets()
	v.setBigInt(slotVaultTotalAssets, new(big.Int).Add(totalAssets, assets))

	return shares, nil
}

// Mint mints exactly `shares` to `receiver`, pulling the required assets from `caller`.
func (v *QRC4626Vault) Mint(caller, receiver qcrypto.Address, shares *big.Int) (*big.Int, error) {
	if shares.Sign() <= 0 {
		return nil, fmt.Errorf("vault mint: shares must be positive")
	}

	assets, err := v.PreviewMint(shares)
	if err != nil {
		return nil, err
	}

	assetAddr, err := v.Asset()
	if err != nil {
		return nil, err
	}
	asset := NewQRC20Token(v.stateDB, assetAddr)
	if err := asset.Transfer(caller, v.contract, assets); err != nil {
		return nil, fmt.Errorf("vault mint: asset transfer failed: %w", err)
	}

	if err := v.mintShares(receiver, shares); err != nil {
		return nil, err
	}

	totalAssets, _ := v.TotalAssets()
	v.setBigInt(slotVaultTotalAssets, new(big.Int).Add(totalAssets, assets))

	return assets, nil
}

// ── Withdraw / Redeem ─────────────────────────────────────────────────────────

// Withdraw burns shares from `owner` and sends `assets` to `receiver`.
// `caller` must be `owner` or have allowance.
func (v *QRC4626Vault) Withdraw(caller, receiver, owner qcrypto.Address, assets *big.Int) (*big.Int, error) {
	if assets.Sign() <= 0 {
		return nil, fmt.Errorf("vault withdraw: assets must be positive")
	}

	shares, err := v.PreviewWithdraw(assets)
	if err != nil {
		return nil, err
	}

	if err := v.spendAllowance(caller, owner, shares); err != nil {
		return nil, err
	}

	if err := v.burnShares(owner, shares); err != nil {
		return nil, err
	}

	// Transfer assets from vault to receiver
	assetAddr, err := v.Asset()
	if err != nil {
		return nil, err
	}
	asset := NewQRC20Token(v.stateDB, assetAddr)
	if err := asset.Transfer(v.contract, receiver, assets); err != nil {
		return nil, fmt.Errorf("vault withdraw: asset transfer failed: %w", err)
	}

	totalAssets, _ := v.TotalAssets()
	newTotal := new(big.Int).Sub(totalAssets, assets)
	if newTotal.Sign() < 0 {
		newTotal = big.NewInt(0)
	}
	v.setBigInt(slotVaultTotalAssets, newTotal)

	return shares, nil
}

// Redeem burns exactly `shares` from `owner` and sends assets to `receiver`.
func (v *QRC4626Vault) Redeem(caller, receiver, owner qcrypto.Address, shares *big.Int) (*big.Int, error) {
	if shares.Sign() <= 0 {
		return nil, fmt.Errorf("vault redeem: shares must be positive")
	}

	assets, err := v.PreviewRedeem(shares)
	if err != nil {
		return nil, err
	}

	if err := v.spendAllowance(caller, owner, shares); err != nil {
		return nil, err
	}

	if err := v.burnShares(owner, shares); err != nil {
		return nil, err
	}

	assetAddr, err := v.Asset()
	if err != nil {
		return nil, err
	}
	asset := NewQRC20Token(v.stateDB, assetAddr)
	if err := asset.Transfer(v.contract, receiver, assets); err != nil {
		return nil, fmt.Errorf("vault redeem: asset transfer failed: %w", err)
	}

	totalAssets, _ := v.TotalAssets()
	newTotal := new(big.Int).Sub(totalAssets, assets)
	if newTotal.Sign() < 0 {
		newTotal = big.NewInt(0)
	}
	v.setBigInt(slotVaultTotalAssets, newTotal)

	return assets, nil
}

// ── Yield reporting ────────────────────────────────────────────────────────────

// ReportYield increases the vault's total assets (records earned yield).
// Called by the vault manager after harvesting yield from the underlying protocol.
// This causes each share to be worth more assets — the core mechanic of yield vaults.
func (v *QRC4626Vault) ReportYield(yieldAmount *big.Int) error {
	if yieldAmount.Sign() <= 0 {
		return nil
	}
	totalAssets, err := v.TotalAssets()
	if err != nil {
		return err
	}
	return v.setBigInt(slotVaultTotalAssets, new(big.Int).Add(totalAssets, yieldAmount))
}

// ExchangeRate returns the current assets per share ratio as a fixed-point value.
// Returned as (assets_per_share_numerator, denominator=10^18).
func (v *QRC4626Vault) ExchangeRate() (*big.Int, *big.Int, error) {
	totalAssets, err := v.TotalAssets()
	if err != nil {
		return nil, nil, err
	}
	totalShares, err := v.TotalShares()
	if err != nil {
		return nil, nil, err
	}

	denom := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)

	if totalShares.Sign() == 0 {
		return denom, denom, nil // 1:1
	}

	numerator := new(big.Int).Mul(totalAssets, denom)
	numerator.Div(numerator, totalShares)
	return numerator, denom, nil
}

// ── Share approval (QRC-20-like for vault shares) ─────────────────────────────

// Approve allows `spender` to use `amount` of `owner`'s vault shares.
func (v *QRC4626Vault) Approve(owner, spender qcrypto.Address, amount *big.Int) error {
	return v.setBigInt(vaultAllowanceSlot(owner, spender), amount)
}

// Allowance returns the amount of vault shares `spender` can use on behalf of `owner`.
func (v *QRC4626Vault) Allowance(owner, spender qcrypto.Address) (*big.Int, error) {
	return v.getBigInt(vaultAllowanceSlot(owner, spender))
}

// ── Internal helpers ──────────────────────────────────────────────────────────

func (v *QRC4626Vault) mintShares(to qcrypto.Address, shares *big.Int) error {
	bal, _ := v.getBigInt(vaultShareSlot(to))
	if err := v.setBigInt(vaultShareSlot(to), new(big.Int).Add(bal, shares)); err != nil {
		return err
	}
	total, _ := v.TotalShares()
	return v.setBigInt(slotVaultTotalShares, new(big.Int).Add(total, shares))
}

func (v *QRC4626Vault) burnShares(from qcrypto.Address, shares *big.Int) error {
	bal, _ := v.getBigInt(vaultShareSlot(from))
	if bal.Cmp(shares) < 0 {
		return fmt.Errorf("vault: insufficient shares (have %s, want %s)", bal, shares)
	}
	if err := v.setBigInt(vaultShareSlot(from), new(big.Int).Sub(bal, shares)); err != nil {
		return err
	}
	total, _ := v.TotalShares()
	newTotal := new(big.Int).Sub(total, shares)
	if newTotal.Sign() < 0 {
		newTotal = big.NewInt(0)
	}
	return v.setBigInt(slotVaultTotalShares, newTotal)
}

func (v *QRC4626Vault) spendAllowance(caller, owner qcrypto.Address, shares *big.Int) error {
	if caller == owner {
		return nil // owner can always spend their own shares
	}
	allowance, err := v.Allowance(owner, caller)
	if err != nil {
		return err
	}
	if allowance.Cmp(shares) < 0 {
		return fmt.Errorf("vault: insufficient allowance (have %s, want %s)", allowance, shares)
	}
	return v.setBigInt(vaultAllowanceSlot(owner, caller), new(big.Int).Sub(allowance, shares))
}

func (v *QRC4626Vault) setBigInt(slot [32]byte, val *big.Int) error {
	return v.stateDB.SetStorageValue(v.contract, slot, val.Bytes())
}

func (v *QRC4626Vault) getBigInt(slot [32]byte) (*big.Int, error) {
	b, err := v.stateDB.GetStorageValue(v.contract, slot)
	if err != nil || len(b) == 0 {
		return big.NewInt(0), nil
	}
	return new(big.Int).SetBytes(b), nil
}

func (v *QRC4626Vault) setString(slot [32]byte, s string) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return v.stateDB.SetStorageValue(v.contract, slot, b)
}

func (v *QRC4626Vault) getString(slot [32]byte) (string, error) {
	b, err := v.stateDB.GetStorageValue(v.contract, slot)
	if err != nil || len(b) == 0 {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return string(b), nil
	}
	return s, nil
}
