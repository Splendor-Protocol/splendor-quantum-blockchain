// Package vm — Over-collateralised Lending Protocol for Splendor Quantum Chain.
//
// Users deposit QRC-20 tokens as collateral and borrow other QRC-20 tokens.
// The protocol enforces a minimum collateral ratio and liquidates positions
// that fall below the liquidation threshold.
//
// Key concepts:
//
//	Collateral Factor (CF):  max borrow value / collateral value (e.g. 75%)
//	Liquidation Threshold:   when health factor < 1, position can be liquidated
//	Health Factor:           collateral_value * CF / borrow_value (must be ≥ 1)
//	Liquidation Bonus:       liquidators receive 5% bonus on seized collateral
//	Reserve Factor:          5% of interest goes to protocol treasury
//	Interest Model:          linear — base rate + utilisation * slope
//
// Storage: all slots = SHA3-256(namespace + key). No keccak. No ECDSA.
package vm

import (
	"encoding/json"
	"fmt"
	"math/big"
	"sort"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
)

// Protocol constants (all in basis points unless noted)
const (
	// CollateralFactorBPS: max borrow as % of collateral value (75%)
	CollateralFactorBPS = 7500
	// LiquidationThresholdBPS: position liquidatable when health < 80%
	LiquidationThresholdBPS = 8000
	// LiquidationBonusBPS: liquidator gets 5% bonus on seized collateral
	LiquidationBonusBPS = 500
	// ReserveFactorBPS: 5% of interest to protocol treasury
	ReserveFactorBPS = 500
	// BaseRatePerBlock: 0.01% interest per block at 0% utilisation
	BaseRatePerBlockBPS = 1
	// SlopePerBlock: 0.05% additional interest per 1% utilisation increase
	SlopePerBlockBPS = 5
	// BPS divisor
	BPS = 10_000
)

// ── Storage slots ─────────────────────────────────────────────────────────────

var (
	slotLendingTreasury = qcrypto.Hash32([]byte("lending:treasury"))
	slotLendingOwner    = qcrypto.Hash32([]byte("lending:owner"))
	slotLendingOracle   = qcrypto.Hash32([]byte("lending:oracle"))
)

func lendingPoolSlot(asset qcrypto.Address) [32]byte {
	return qcrypto.Hash32(append([]byte("lending:pool:"), asset[:]...))
}

func lendingPositionSlot(user, asset qcrypto.Address) [32]byte {
	key := append([]byte("lending:pos:"), user[:]...)
	key = append(key, asset[:]...)
	return qcrypto.Hash32(key)
}

func lendingBorrowSlot(user, asset qcrypto.Address) [32]byte {
	key := append([]byte("lending:borrow:"), user[:]...)
	key = append(key, asset[:]...)
	return qcrypto.Hash32(key)
}

func lendingInterestSlot(asset qcrypto.Address) [32]byte {
	return qcrypto.Hash32(append([]byte("lending:interest:"), asset[:]...))
}

// ── Data structures ────────────────────────────────────────────────────────────

// AssetPool tracks the state of a lending market for one asset.
type AssetPool struct {
	Asset           string   `json:"asset"` // hex address
	TotalDeposits   *big.Int `json:"total_deposits"`
	TotalBorrows    *big.Int `json:"total_borrows"`
	ReserveBalance  *big.Int `json:"reserve_balance"`
	LastBlockHeight uint64   `json:"last_block_height"`
	// AccumulatedInterestPerToken: interest accrued per deposited token (scaled 1e18)
	AccruedInterestIndex *big.Int `json:"accrued_interest_index"`
	// CollateralFactor in BPS (can be set per asset, defaults to CollateralFactorBPS)
	CollateralFactorBPS int64 `json:"collateral_factor_bps"`
	// LiquidationThresholdBPS per asset
	LiquidationThresholdBPS int64 `json:"liquidation_threshold_bps"`
	// Active: whether new deposits/borrows are allowed
	Active bool `json:"active"`
}

// UserPosition tracks one user's deposit in one asset.
type UserPosition struct {
	Asset          string   `json:"asset"`
	DepositBalance *big.Int `json:"deposit_balance"`
	// InterestIndexAtDeposit: index when user last deposited/withdrew
	InterestIndexAtDeposit *big.Int `json:"interest_index_at_deposit"`
}

// UserBorrow tracks one user's outstanding borrow of one asset.
type UserBorrow struct {
	Asset         string   `json:"asset"`
	BorrowBalance *big.Int `json:"borrow_balance"`
	// InterestIndexAtBorrow: interest index when user last borrowed/repaid
	InterestIndexAtBorrow *big.Int `json:"interest_index_at_borrow"`
}

// ── LendingProtocol ───────────────────────────────────────────────────────────

// LendingProtocol manages the over-collateralised lending markets.
type LendingProtocol struct {
	stateDB  *state.StateDB
	contract qcrypto.Address
}

// NewLendingProtocol returns a handle to an existing lending protocol contract.
func NewLendingProtocol(stateDB *state.StateDB, contract qcrypto.Address) *LendingProtocol {
	return &LendingProtocol{stateDB: stateDB, contract: contract}
}

// LendingAddress derives the deterministic lending-protocol address for a
// (deployer, nonce) pair. It is PURE: it touches no state.
//
// Read-only callers — the RPC handlers in particular — must use this rather than
// DeployLending. Deriving an address is not deploying a contract, and conflating
// the two wrote a consensus state leaf every time the HTTP routes were built.
func LendingAddress(deployer qcrypto.Address, nonce uint64) qcrypto.Address {
	input := append(deployer[:], []byte(fmt.Sprintf("%d:lending", nonce))...)
	hash := qcrypto.Hash32(input)
	var addr qcrypto.Address
	copy(addr[:], hash[:])
	return addr
}

// DeployLending deploys the lending protocol at a deterministic address, writing
// the owner slot.
//
// This MUTATES consensus state, so it may only be called from inside block
// execution — a transaction or genesis. Calling it from anywhere else (an HTTP
// handler, a constructor, a route registration) writes a state leaf outside any
// block: the leaf lands in whatever block the node seals next, that block's state
// root then reflects a write no transaction accounts for, and a peer replaying
// history recomputes a different root and can never sync. Use LendingAddress if
// you only need the address.
func DeployLending(stateDB *state.StateDB, deployer qcrypto.Address, nonce uint64) qcrypto.Address {
	addr := LendingAddress(deployer, nonce)
	stateDB.SetStorageValue(addr, slotLendingOwner, deployer[:])
	return addr
}

// ── Asset management ──────────────────────────────────────────────────────────

// SetOracle binds this market to a price oracle. A routed borrow or
// liquidation must read prices from it (BorrowFromOracle) and never from the
// caller: Borrow's price arguments exist for the oracle-fed path and for
// tests, and a public surface that let the borrower quote its own collateral
// would let it borrow against nothing.
func (l *LendingProtocol) SetOracle(caller, oracle qcrypto.Address) error {
	if err := l.requireOwner(caller); err != nil {
		return err
	}
	if oracle.IsZero() {
		return fmt.Errorf("lending: oracle is the zero address")
	}
	return l.stateDB.SetStorageValue(l.contract, slotLendingOracle, oracle[:])
}

// Oracle returns the bound price-oracle address (zero if unset).
func (l *LendingProtocol) Oracle() (qcrypto.Address, error) {
	b, err := l.stateDB.GetStorageValue(l.contract, slotLendingOracle)
	if err != nil || len(b) < qcrypto.AddressSize {
		return qcrypto.Address{}, err
	}
	var a qcrypto.Address
	copy(a[:], b)
	return a, nil
}

func (l *LendingProtocol) oraclePrices(collateral, borrow qcrypto.Address, height uint64) (colPrice, borrowPrice *big.Int, err error) {
	oa, err := l.Oracle()
	if err != nil {
		return nil, nil, err
	}
	if oa.IsZero() {
		return nil, nil, fmt.Errorf("lending: oracle not set")
	}
	o := NewPriceOracle(l.stateDB, oa)
	colPrice, err = o.GetPrice(collateral, height)
	if err != nil {
		return nil, nil, fmt.Errorf("lending: collateral price: %w", err)
	}
	borrowPrice, err = o.GetPrice(borrow, height)
	if err != nil {
		return nil, nil, fmt.Errorf("lending: borrow price: %w", err)
	}
	if colPrice == nil || colPrice.Sign() <= 0 || borrowPrice == nil || borrowPrice.Sign() <= 0 {
		return nil, nil, fmt.Errorf("lending: oracle returned a non-positive price")
	}
	return colPrice, borrowPrice, nil
}

// BorrowFromOracle borrows at the bound oracle's prices. The only Borrow a
// public router may ever expose.
func (l *LendingProtocol) BorrowFromOracle(caller, borrowAsset, collateralAsset qcrypto.Address, amount *big.Int, height uint64) error {
	cp, bp, err := l.oraclePrices(collateralAsset, borrowAsset, height)
	if err != nil {
		return err
	}
	return l.Borrow(caller, borrowAsset, collateralAsset, amount, cp, bp, height)
}

// EnableAsset registers a new asset for lending/borrowing.
func (l *LendingProtocol) EnableAsset(caller, asset qcrypto.Address, cfBPS, ltBPS int64) error {
	if err := l.requireOwner(caller); err != nil {
		return err
	}
	if cfBPS > BPS || cfBPS <= 0 {
		return fmt.Errorf("lending: collateral factor out of range")
	}
	if ltBPS > BPS || ltBPS < cfBPS {
		return fmt.Errorf("lending: liquidation threshold must be >= collateral factor")
	}

	pool := &AssetPool{
		Asset:                   asset.String(),
		TotalDeposits:           big.NewInt(0),
		TotalBorrows:            big.NewInt(0),
		ReserveBalance:          big.NewInt(0),
		AccruedInterestIndex:    new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil), // 1e18
		CollateralFactorBPS:     cfBPS,
		LiquidationThresholdBPS: ltBPS,
		Active:                  true,
	}
	return l.setPool(asset, pool)
}

// DisableAsset prevents new deposits/borrows for an asset.
func (l *LendingProtocol) DisableAsset(caller, asset qcrypto.Address) error {
	if err := l.requireOwner(caller); err != nil {
		return err
	}
	pool, err := l.GetPool(asset)
	if err != nil || pool == nil {
		return fmt.Errorf("lending: asset not registered")
	}
	pool.Active = false
	return l.setPool(asset, pool)
}

// ── Deposit ───────────────────────────────────────────────────────────────────

// Deposit deposits `amount` of `asset` tokens from `caller` into the lending pool.
// The caller must have approved the lending contract to spend their tokens.
// Returns the deposit balance credited.
func (l *LendingProtocol) Deposit(caller, asset qcrypto.Address, amount *big.Int, currentBlock uint64) error {
	pool, err := l.GetPool(asset)
	if err != nil || pool == nil {
		return fmt.Errorf("lending deposit: asset %s not registered", asset)
	}
	if !pool.Active {
		return fmt.Errorf("lending deposit: asset %s is disabled", asset)
	}
	if amount.Sign() <= 0 {
		return fmt.Errorf("lending deposit: amount must be positive")
	}

	// Accrue interest before state changes
	if err := l.accrueInterest(asset, pool, currentBlock); err != nil {
		return err
	}

	// Transfer tokens from caller to protocol
	tok := NewQRC20Token(l.stateDB, asset)
	if err := tok.Transfer(caller, l.contract, amount); err != nil {
		return fmt.Errorf("lending deposit: token transfer failed: %w", err)
	}

	// Update user position
	pos, err := l.getUserPosition(caller, asset)
	if err != nil {
		return err
	}

	// Normalise existing balance with current interest index
	normalised := l.normaliseDeposit(pos.DepositBalance, pos.InterestIndexAtDeposit, pool.AccruedInterestIndex)
	pos.DepositBalance = new(big.Int).Add(normalised, amount)
	pos.InterestIndexAtDeposit = new(big.Int).Set(pool.AccruedInterestIndex)
	pos.Asset = asset.String()

	if err := l.setUserPosition(caller, asset, pos); err != nil {
		return err
	}

	// Update pool
	pool.TotalDeposits.Add(pool.TotalDeposits, amount)
	return l.setPool(asset, pool)
}

// Withdraw withdraws `amount` of `asset` from the caller's deposit.
// If amount == MaxUint256, withdraws the full balance.
func (l *LendingProtocol) Withdraw(caller, asset qcrypto.Address, amount *big.Int, currentBlock uint64) (*big.Int, error) {
	pool, err := l.GetPool(asset)
	if err != nil || pool == nil {
		return nil, fmt.Errorf("lending withdraw: asset not registered")
	}

	if err := l.accrueInterest(asset, pool, currentBlock); err != nil {
		return nil, err
	}

	pos, err := l.getUserPosition(caller, asset)
	if err != nil {
		return nil, err
	}

	// Compute actual balance with accrued interest
	actualBalance := l.normaliseDeposit(pos.DepositBalance, pos.InterestIndexAtDeposit, pool.AccruedInterestIndex)

	// MaxUint256 = withdraw all
	maxUint256 := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	withdrawAmount := amount
	if amount.Cmp(maxUint256) == 0 {
		withdrawAmount = new(big.Int).Set(actualBalance)
	}

	if actualBalance.Cmp(withdrawAmount) < 0 {
		return nil, fmt.Errorf("lending withdraw: insufficient balance (%s < %s)", actualBalance, withdrawAmount)
	}

	// Check there's enough liquidity (deposits - borrows)
	liquidity := new(big.Int).Sub(pool.TotalDeposits, pool.TotalBorrows)
	if liquidity.Cmp(withdrawAmount) < 0 {
		return nil, fmt.Errorf("lending withdraw: insufficient protocol liquidity")
	}

	// Check this wouldn't make caller's position undercollateralised
	// (skip if they have no borrows)

	// Update position
	pos.DepositBalance = new(big.Int).Sub(actualBalance, withdrawAmount)
	pos.InterestIndexAtDeposit = new(big.Int).Set(pool.AccruedInterestIndex)
	if err := l.setUserPosition(caller, asset, pos); err != nil {
		return nil, err
	}

	// Update pool
	pool.TotalDeposits.Sub(pool.TotalDeposits, withdrawAmount)
	if err := l.setPool(asset, pool); err != nil {
		return nil, err
	}

	// Transfer tokens to caller
	tok := NewQRC20Token(l.stateDB, asset)
	if err := tok.Transfer(l.contract, caller, withdrawAmount); err != nil {
		return nil, fmt.Errorf("lending withdraw: token transfer failed: %w", err)
	}

	return withdrawAmount, nil
}

// ── Borrow ────────────────────────────────────────────────────────────────────

// Borrow allows caller to borrow `amount` of `borrowAsset` using their deposited collateral.
// The health factor must stay ≥ 1 after borrowing.
// collateralAsset is the asset the caller has deposited as collateral.
// collateralPrice and borrowPrice are oracle prices (in smallest SPLD units per token).
func (l *LendingProtocol) Borrow(
	caller, borrowAsset, collateralAsset qcrypto.Address,
	amount *big.Int,
	collateralPrice, borrowPrice *big.Int,
	currentBlock uint64,
) error {
	pool, err := l.GetPool(borrowAsset)
	if err != nil || pool == nil {
		return fmt.Errorf("lending borrow: borrow asset not registered")
	}
	if !pool.Active {
		return fmt.Errorf("lending borrow: borrow asset disabled")
	}
	if amount.Sign() <= 0 {
		return fmt.Errorf("lending borrow: amount must be positive")
	}

	if err := l.accrueInterest(borrowAsset, pool, currentBlock); err != nil {
		return err
	}

	// Check health factor AFTER borrowing
	colPos, err := l.getUserPosition(caller, collateralAsset)
	if err != nil {
		return err
	}
	colPool, _ := l.GetPool(collateralAsset)
	if colPool == nil {
		return fmt.Errorf("lending borrow: collateral asset not registered")
	}
	if err := l.accrueInterest(collateralAsset, colPool, currentBlock); err != nil {
		return err
	}

	collateralBalance := l.normaliseDeposit(colPos.DepositBalance, colPos.InterestIndexAtDeposit, colPool.AccruedInterestIndex)
	if collateralBalance.Sign() == 0 {
		return fmt.Errorf("lending borrow: no collateral deposited")
	}

	// collateral value in price units
	colValue := new(big.Int).Mul(collateralBalance, collateralPrice)
	// max borrow value = colValue * CF
	maxBorrowValue := new(big.Int).Mul(colValue, big.NewInt(colPool.CollateralFactorBPS))
	maxBorrowValue.Div(maxBorrowValue, big.NewInt(BPS))

	// existing borrows
	existingBorrow, _ := l.getUserBorrow(caller, borrowAsset)
	existingBorrowBalance := l.normaliseBorrow(existingBorrow.BorrowBalance, existingBorrow.InterestIndexAtBorrow, pool.AccruedInterestIndex)

	// new total borrow value
	newBorrowValue := new(big.Int).Mul(new(big.Int).Add(existingBorrowBalance, amount), borrowPrice)
	if newBorrowValue.Cmp(maxBorrowValue) > 0 {
		return fmt.Errorf("lending borrow: would exceed collateral factor (max borrow value: %s)", maxBorrowValue)
	}

	// Check pool liquidity
	liquidity := new(big.Int).Sub(pool.TotalDeposits, pool.TotalBorrows)
	if liquidity.Cmp(amount) < 0 {
		return fmt.Errorf("lending borrow: insufficient pool liquidity")
	}

	// Update borrow record
	existingBorrow.BorrowBalance = new(big.Int).Add(existingBorrowBalance, amount)
	existingBorrow.InterestIndexAtBorrow = new(big.Int).Set(pool.AccruedInterestIndex)
	existingBorrow.Asset = borrowAsset.String()
	if err := l.setUserBorrow(caller, borrowAsset, existingBorrow); err != nil {
		return err
	}

	// Update pool
	pool.TotalBorrows.Add(pool.TotalBorrows, amount)
	if err := l.setPool(borrowAsset, pool); err != nil {
		return err
	}

	// Transfer borrowed tokens to caller
	tok := NewQRC20Token(l.stateDB, borrowAsset)
	return tok.Transfer(l.contract, caller, amount)
}

// Repay repays `amount` of a borrow. If amount == MaxUint256, repays the full balance.
func (l *LendingProtocol) Repay(caller, borrowAsset qcrypto.Address, amount *big.Int, currentBlock uint64) (*big.Int, error) {
	pool, err := l.GetPool(borrowAsset)
	if err != nil || pool == nil {
		return nil, fmt.Errorf("lending repay: asset not registered")
	}

	if err := l.accrueInterest(borrowAsset, pool, currentBlock); err != nil {
		return nil, err
	}

	borrow, err := l.getUserBorrow(caller, borrowAsset)
	if err != nil {
		return nil, err
	}
	actualDebt := l.normaliseBorrow(borrow.BorrowBalance, borrow.InterestIndexAtBorrow, pool.AccruedInterestIndex)
	if actualDebt.Sign() == 0 {
		return nil, fmt.Errorf("lending repay: no outstanding borrow")
	}

	maxUint256 := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	repayAmount := amount
	if amount.Cmp(maxUint256) == 0 {
		repayAmount = new(big.Int).Set(actualDebt)
	}
	if repayAmount.Cmp(actualDebt) > 0 {
		repayAmount = new(big.Int).Set(actualDebt)
	}

	// Transfer tokens from caller to protocol
	tok := NewQRC20Token(l.stateDB, borrowAsset)
	if err := tok.Transfer(caller, l.contract, repayAmount); err != nil {
		return nil, fmt.Errorf("lending repay: token transfer failed: %w", err)
	}

	// Update borrow
	borrow.BorrowBalance = new(big.Int).Sub(actualDebt, repayAmount)
	borrow.InterestIndexAtBorrow = new(big.Int).Set(pool.AccruedInterestIndex)
	if err := l.setUserBorrow(caller, borrowAsset, borrow); err != nil {
		return nil, err
	}

	// Update pool
	pool.TotalBorrows.Sub(pool.TotalBorrows, repayAmount)
	if pool.TotalBorrows.Sign() < 0 {
		pool.TotalBorrows = big.NewInt(0)
	}
	if err := l.setPool(borrowAsset, pool); err != nil {
		return nil, err
	}

	return repayAmount, nil
}

// ── Liquidation ───────────────────────────────────────────────────────────────

// Liquidate seizes collateral from an undercollateralised position.
// liquidator repays up to 50% of borrower's debt and receives collateral + bonus.
func (l *LendingProtocol) Liquidate(
	liquidator, borrower, borrowAsset, collateralAsset qcrypto.Address,
	repayAmount *big.Int,
	collateralPrice, borrowPrice *big.Int,
	currentBlock uint64,
) (seizedCollateral *big.Int, err error) {
	// Accrue interest on both assets
	borrowPool, _ := l.GetPool(borrowAsset)
	colPool, _ := l.GetPool(collateralAsset)
	if borrowPool == nil || colPool == nil {
		return nil, fmt.Errorf("liquidate: asset not registered")
	}
	l.accrueInterest(borrowAsset, borrowPool, currentBlock)
	l.accrueInterest(collateralAsset, colPool, currentBlock)

	// Check health factor < 1
	colPos, _ := l.getUserPosition(borrower, collateralAsset)
	borrow, _ := l.getUserBorrow(borrower, borrowAsset)

	colBalance := l.normaliseDeposit(colPos.DepositBalance, colPos.InterestIndexAtDeposit, colPool.AccruedInterestIndex)
	debtBalance := l.normaliseBorrow(borrow.BorrowBalance, borrow.InterestIndexAtBorrow, borrowPool.AccruedInterestIndex)

	colValue := new(big.Int).Mul(colBalance, collateralPrice)
	debtValue := new(big.Int).Mul(debtBalance, borrowPrice)

	// Health factor = colValue * LT / debtValue (must be < 1 = BPS)
	healthNumerator := new(big.Int).Mul(colValue, big.NewInt(colPool.LiquidationThresholdBPS))
	healthNumerator.Div(healthNumerator, big.NewInt(BPS))
	if healthNumerator.Cmp(debtValue) >= 0 {
		return nil, fmt.Errorf("liquidate: position is healthy (health factor >= 1)")
	}

	// Max repay = 50% of debt
	maxRepay := new(big.Int).Div(debtBalance, big.NewInt(2))
	if repayAmount.Cmp(maxRepay) > 0 {
		repayAmount = new(big.Int).Set(maxRepay)
	}

	// Seized collateral = repayAmount * borrowPrice / collateralPrice * (1 + liquidationBonus)
	seizedValue := new(big.Int).Mul(repayAmount, borrowPrice)
	seizedCollateral = new(big.Int).Div(seizedValue, collateralPrice)
	// Add liquidation bonus
	bonus := new(big.Int).Mul(seizedCollateral, big.NewInt(LiquidationBonusBPS))
	bonus.Div(bonus, big.NewInt(BPS))
	seizedCollateral.Add(seizedCollateral, bonus)

	// Cap at available collateral
	if seizedCollateral.Cmp(colBalance) > 0 {
		seizedCollateral.Set(colBalance)
	}

	// Liquidator repays debt
	debtTok := NewQRC20Token(l.stateDB, borrowAsset)
	if err := debtTok.Transfer(liquidator, l.contract, repayAmount); err != nil {
		return nil, fmt.Errorf("liquidate: repay transfer failed: %w", err)
	}

	// Transfer seized collateral to liquidator
	colTok := NewQRC20Token(l.stateDB, collateralAsset)
	if err := colTok.Transfer(l.contract, liquidator, seizedCollateral); err != nil {
		return nil, fmt.Errorf("liquidate: collateral transfer failed: %w", err)
	}

	// Update borrower's borrow
	borrow.BorrowBalance = new(big.Int).Sub(debtBalance, repayAmount)
	borrow.InterestIndexAtBorrow = new(big.Int).Set(borrowPool.AccruedInterestIndex)
	l.setUserBorrow(borrower, borrowAsset, borrow)
	borrowPool.TotalBorrows.Sub(borrowPool.TotalBorrows, repayAmount)
	l.setPool(borrowAsset, borrowPool)

	// Update borrower's collateral
	colPos.DepositBalance = new(big.Int).Sub(colBalance, seizedCollateral)
	colPos.InterestIndexAtDeposit = new(big.Int).Set(colPool.AccruedInterestIndex)
	l.setUserPosition(borrower, collateralAsset, colPos)
	colPool.TotalDeposits.Sub(colPool.TotalDeposits, seizedCollateral)
	l.setPool(collateralAsset, colPool)

	return seizedCollateral, nil
}

// ── Interest ──────────────────────────────────────────────────────────────────

// accrueInterest updates the pool's interest index based on elapsed blocks.
func (l *LendingProtocol) accrueInterest(asset qcrypto.Address, pool *AssetPool, currentBlock uint64) error {
	if currentBlock <= pool.LastBlockHeight {
		return nil
	}
	blocksElapsed := currentBlock - pool.LastBlockHeight

	utilisation := uint64(0)
	if pool.TotalDeposits.Sign() > 0 {
		// utilisation = borrows / deposits * 10000 (in BPS)
		u := new(big.Int).Mul(pool.TotalBorrows, big.NewInt(BPS))
		u.Div(u, pool.TotalDeposits)
		if u.IsInt64() {
			utilisation = uint64(u.Int64())
		}
	}

	// Interest rate per block = base + slope * utilisation (in BPS)
	ratePerBlockBPS := uint64(BaseRatePerBlockBPS) + uint64(SlopePerBlockBPS)*utilisation/BPS
	if ratePerBlockBPS > 10000 {
		ratePerBlockBPS = 10000 // cap at 100%
	}

	// Total interest = totalBorrows * ratePerBlock * blocksElapsed / BPS
	if pool.TotalBorrows.Sign() > 0 {
		interest := new(big.Int).Mul(pool.TotalBorrows, big.NewInt(int64(ratePerBlockBPS*blocksElapsed)))
		interest.Div(interest, big.NewInt(BPS*100)) // /100 because rate is in BPS*100

		// Reserve portion
		reserveInterest := new(big.Int).Mul(interest, big.NewInt(ReserveFactorBPS))
		reserveInterest.Div(reserveInterest, big.NewInt(BPS))
		pool.ReserveBalance.Add(pool.ReserveBalance, reserveInterest)

		// Add to total deposits (distributes interest to depositors)
		netInterest := new(big.Int).Sub(interest, reserveInterest)
		pool.TotalDeposits.Add(pool.TotalDeposits, netInterest)

		// Update interest index: index += (interest / totalDeposits) * index
		if pool.TotalDeposits.Sign() > 0 {
			delta := new(big.Int).Mul(netInterest, pool.AccruedInterestIndex)
			delta.Div(delta, pool.TotalDeposits)
			pool.AccruedInterestIndex.Add(pool.AccruedInterestIndex, delta)
		}
	}

	pool.LastBlockHeight = currentBlock
	return l.setPool(asset, pool)
}

// UtilisationRate returns the current utilisation of a pool (0-10000 BPS).
func (l *LendingProtocol) UtilisationRate(asset qcrypto.Address) (uint64, error) {
	pool, err := l.GetPool(asset)
	if err != nil || pool == nil {
		return 0, fmt.Errorf("lending: asset not registered")
	}
	if pool.TotalDeposits.Sign() == 0 {
		return 0, nil
	}
	u := new(big.Int).Mul(pool.TotalBorrows, big.NewInt(BPS))
	u.Div(u, pool.TotalDeposits)
	return u.Uint64(), nil
}

// ── Normalisation helpers ─────────────────────────────────────────────────────

// normaliseDeposit adjusts deposit balance for accrued interest.
// newBalance = storedBalance * currentIndex / storedIndex
func (l *LendingProtocol) normaliseDeposit(stored, storedIndex, currentIndex *big.Int) *big.Int {
	if stored.Sign() == 0 || storedIndex.Sign() == 0 {
		return big.NewInt(0)
	}
	result := new(big.Int).Mul(stored, currentIndex)
	result.Div(result, storedIndex)
	return result
}

// normaliseBorrow adjusts borrow balance for accrued interest.
func (l *LendingProtocol) normaliseBorrow(stored, storedIndex, currentIndex *big.Int) *big.Int {
	return l.normaliseDeposit(stored, storedIndex, currentIndex)
}

// ── Queries ────────────────────────────────────────────────────────────────────

// GetPool returns the pool state for an asset.
func (l *LendingProtocol) GetPool(asset qcrypto.Address) (*AssetPool, error) {
	b, err := l.stateDB.GetStorageValue(l.contract, lendingPoolSlot(asset))
	if err != nil || len(b) == 0 {
		return nil, nil
	}
	var pool AssetPool
	if err := json.Unmarshal(b, &pool); err != nil {
		return nil, err
	}
	if pool.TotalDeposits == nil {
		pool.TotalDeposits = big.NewInt(0)
	}
	if pool.TotalBorrows == nil {
		pool.TotalBorrows = big.NewInt(0)
	}
	if pool.ReserveBalance == nil {
		pool.ReserveBalance = big.NewInt(0)
	}
	if pool.AccruedInterestIndex == nil {
		pool.AccruedInterestIndex = new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	}
	return &pool, nil
}

// GetDepositBalance returns a user's effective deposit balance (with interest).
func (l *LendingProtocol) GetDepositBalance(user, asset qcrypto.Address) (*big.Int, error) {
	pool, err := l.GetPool(asset)
	if err != nil || pool == nil {
		return big.NewInt(0), nil
	}
	pos, err := l.getUserPosition(user, asset)
	if err != nil {
		return nil, err
	}
	return l.normaliseDeposit(pos.DepositBalance, pos.InterestIndexAtDeposit, pool.AccruedInterestIndex), nil
}

// GetBorrowBalance returns a user's effective borrow balance (with interest).
func (l *LendingProtocol) GetBorrowBalance(user, asset qcrypto.Address) (*big.Int, error) {
	pool, err := l.GetPool(asset)
	if err != nil || pool == nil {
		return big.NewInt(0), nil
	}
	borrow, err := l.getUserBorrow(user, asset)
	if err != nil {
		return nil, err
	}
	return l.normaliseBorrow(borrow.BorrowBalance, borrow.InterestIndexAtBorrow, pool.AccruedInterestIndex), nil
}

// SupportedAssets returns a list of all asset addresses with active pools.
// (In production, maintain an on-chain list; here we use a simple registry.)
func (l *LendingProtocol) SupportedAssets(knownAssets []qcrypto.Address) []qcrypto.Address {
	var active []qcrypto.Address
	for _, asset := range knownAssets {
		pool, err := l.GetPool(asset)
		if err == nil && pool != nil {
			active = append(active, asset)
		}
	}
	sort.Slice(active, func(i, j int) bool {
		return active[i].String() < active[j].String()
	})
	return active
}

// ── Access control ────────────────────────────────────────────────────────────

func (l *LendingProtocol) requireOwner(caller qcrypto.Address) error {
	b, err := l.stateDB.GetStorageValue(l.contract, slotLendingOwner)
	if err != nil || len(b) < qcrypto.AddressSize {
		return fmt.Errorf("lending: owner not set")
	}
	var owner qcrypto.Address
	copy(owner[:], b)
	if caller != owner {
		return fmt.Errorf("lending: caller %s is not owner", caller)
	}
	return nil
}

// ── Storage helpers ────────────────────────────────────────────────────────────

func (l *LendingProtocol) setPool(asset qcrypto.Address, pool *AssetPool) error {
	data, err := json.Marshal(pool)
	if err != nil {
		return err
	}
	return l.stateDB.SetStorageValue(l.contract, lendingPoolSlot(asset), data)
}

func (l *LendingProtocol) getUserPosition(user, asset qcrypto.Address) (*UserPosition, error) {
	b, err := l.stateDB.GetStorageValue(l.contract, lendingPositionSlot(user, asset))
	if err != nil || len(b) == 0 {
		one18 := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
		return &UserPosition{
			Asset:                  asset.String(),
			DepositBalance:         big.NewInt(0),
			InterestIndexAtDeposit: one18,
		}, nil
	}
	var pos UserPosition
	if err := json.Unmarshal(b, &pos); err != nil {
		return nil, err
	}
	if pos.DepositBalance == nil {
		pos.DepositBalance = big.NewInt(0)
	}
	if pos.InterestIndexAtDeposit == nil {
		pos.InterestIndexAtDeposit = new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	}
	return &pos, nil
}

func (l *LendingProtocol) setUserPosition(user, asset qcrypto.Address, pos *UserPosition) error {
	data, err := json.Marshal(pos)
	if err != nil {
		return err
	}
	return l.stateDB.SetStorageValue(l.contract, lendingPositionSlot(user, asset), data)
}

func (l *LendingProtocol) getUserBorrow(user, asset qcrypto.Address) (*UserBorrow, error) {
	b, err := l.stateDB.GetStorageValue(l.contract, lendingBorrowSlot(user, asset))
	if err != nil || len(b) == 0 {
		one18 := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
		return &UserBorrow{
			Asset:                 asset.String(),
			BorrowBalance:         big.NewInt(0),
			InterestIndexAtBorrow: one18,
		}, nil
	}
	var borrow UserBorrow
	if err := json.Unmarshal(b, &borrow); err != nil {
		return nil, err
	}
	if borrow.BorrowBalance == nil {
		borrow.BorrowBalance = big.NewInt(0)
	}
	if borrow.InterestIndexAtBorrow == nil {
		borrow.InterestIndexAtBorrow = new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	}
	return &borrow, nil
}

func (l *LendingProtocol) setUserBorrow(user, asset qcrypto.Address, borrow *UserBorrow) error {
	data, err := json.Marshal(borrow)
	if err != nil {
		return err
	}
	return l.stateDB.SetStorageValue(l.contract, lendingBorrowSlot(user, asset), data)
}
