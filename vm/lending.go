// Package vm — Over-collateralised Lending Protocol for Splendor Quantum Chain.
//
// Users deposit QRC-20 tokens as collateral and borrow other QRC-20 tokens.
// The protocol enforces a minimum collateral ratio and liquidates positions
// that fall below the liquidation threshold.
//
// Key concepts:
//   Collateral Factor (CF):  max borrow value / collateral value (e.g. 75%)
//   Liquidation Threshold:   when health factor < 1, position can be liquidated
//   Health Factor:           collateral_value * CF / borrow_value (must be ≥ 1)
//   Liquidation Bonus:       liquidators receive 5% bonus on seized collateral
//   Reserve Factor:          5% of interest goes to protocol treasury
//   Interest Model:          linear — base rate + utilisation * slope
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
	// slotLendingAssets is the list of every asset EnableAsset ever registered.
	// Positions are keyed per (user, asset), so without this list the protocol
	// could not see a user's other collateral or other debts, and health was
	// judged against one pair at a time.
	slotLendingAssets = qcrypto.Hash32([]byte("lending:assets"))
)

// MaxLendingAssets bounds the registry: every health check walks it.
const MaxLendingAssets = 64

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

// SetOracle binds this market to a price oracle. Borrow and liquidate
// refuse caller-supplied prices; they read this oracle only.
func (l *LendingProtocol) SetOracle(caller, oracle qcrypto.Address) error {
	if err := l.requireOwner(caller); err != nil {
		return err
	}
	if oracle.IsZero() {
		return fmt.Errorf("lending: oracle is zero address")
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

// BorrowFromOracle borrows using on-chain oracle prices. The public router
// must use this — never a caller-supplied price.
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

	if existing, _ := l.GetPool(asset); existing != nil {
		return fmt.Errorf("lending: asset %s already registered", asset)
	}
	assets, err := l.RegisteredAssets()
	if err != nil {
		return err
	}
	if len(assets) >= MaxLendingAssets {
		return fmt.Errorf("lending: %d assets registered, cap %d", len(assets), MaxLendingAssets)
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
	if err := l.setPool(asset, pool); err != nil {
		return err
	}
	return l.setRegisteredAssets(append(assets, asset))
}

// RegisteredAssets lists every asset ever enabled, in registration order.
func (l *LendingProtocol) RegisteredAssets() ([]qcrypto.Address, error) {
	b, err := l.stateDB.GetStorageValue(l.contract, slotLendingAssets)
	if err != nil {
		return nil, err
	}
	if len(b) == 0 {
		return nil, nil
	}
	var strs []string
	if err := json.Unmarshal(b, &strs); err != nil {
		return nil, fmt.Errorf("lending: asset registry: %w", err)
	}
	out := make([]qcrypto.Address, 0, len(strs))
	for _, s := range strs {
		a, err := qcrypto.AddressFromString(s)
		if err != nil {
			return nil, fmt.Errorf("lending: asset registry: %w", err)
		}
		out = append(out, a)
	}
	return out, nil
}

func (l *LendingProtocol) setRegisteredAssets(assets []qcrypto.Address) error {
	strs := make([]string, 0, len(assets))
	for _, a := range assets {
		strs = append(strs, a.String())
	}
	b, err := json.Marshal(strs)
	if err != nil {
		return err
	}
	return l.stateDB.SetStorageValue(l.contract, slotLendingAssets, b)
}

// accountHealth values a user's whole position at oracle prices, across every
// registered asset, after accruing interest on each pool it touches.
//
//	borrowCap  = Σ deposit_i × price_i × CF_i / BPS   (what may be borrowed against)
//	ltValue    = Σ deposit_i × price_i × LT_i / BPS   (below this debt, liquidatable)
//	debtValue  = Σ debt_j × price_j
//
// deltaAsset/delta let a caller ask "and after this deposit changes by delta?"
// (a withdrawal passes a negative delta). Prices are fetched only for assets
// the user actually holds or owes, so an account with no debt anywhere is
// judged without an oracle and can always withdraw.
type accountHealth struct {
	borrowCap, ltValue, debtValue *big.Int
	hasDebt                       bool
}

func (l *LendingProtocol) health(user qcrypto.Address, height uint64, deltaAsset qcrypto.Address, delta *big.Int) (*accountHealth, error) {
	return l.healthOpt(user, height, deltaAsset, delta, false, true)
}

// pricedCapacity is health() with every deposit priced even when the user
// has no debt yet: the first borrow needs the capacity number.
func (l *LendingProtocol) pricedCapacity(user qcrypto.Address, height uint64) (*accountHealth, error) {
	return l.healthOpt(user, height, qcrypto.Address{}, nil, true, true)
}

// persist=false is the read side: interest is not accrued (accrual writes the
// pool) and the position is valued at the stored indexes.
func (l *LendingProtocol) healthOpt(user qcrypto.Address, height uint64, deltaAsset qcrypto.Address, delta *big.Int, priceAlways, persist bool) (*accountHealth, error) {
	assets, err := l.RegisteredAssets()
	if err != nil {
		return nil, err
	}
	h := &accountHealth{borrowCap: new(big.Int), ltValue: new(big.Int), debtValue: new(big.Int)}
	type leg struct {
		asset    qcrypto.Address
		deposit  *big.Int
		debt     *big.Int
		cf, lt   int64
	}
	var legs []leg
	for _, asset := range assets {
		pool, err := l.GetPool(asset)
		if err != nil || pool == nil {
			continue
		}
		// accrueInterest stores the pool itself; the read side skips it and
		// values the position at the stored indexes.
		if persist {
			if err := l.accrueInterest(asset, pool, height); err != nil {
				return nil, err
			}
		}
		pos, err := l.getUserPosition(user, asset)
		if err != nil {
			return nil, err
		}
		dep := l.normaliseDeposit(pos.DepositBalance, pos.InterestIndexAtDeposit, pool.AccruedInterestIndex)
		if asset == deltaAsset && delta != nil {
			dep = new(big.Int).Add(dep, delta)
			if dep.Sign() < 0 {
				return nil, fmt.Errorf("lending: withdrawal exceeds the deposit")
			}
		}
		bor, err := l.getUserBorrow(user, asset)
		if err != nil {
			return nil, err
		}
		debt := l.normaliseBorrow(bor.BorrowBalance, bor.InterestIndexAtBorrow, pool.AccruedInterestIndex)
		if dep.Sign() == 0 && debt.Sign() == 0 {
			continue
		}
		if debt.Sign() > 0 {
			h.hasDebt = true
		}
		legs = append(legs, leg{asset: asset, deposit: dep, debt: debt, cf: pool.CollateralFactorBPS, lt: pool.LiquidationThresholdBPS})
	}
	if !h.hasDebt && !priceAlways {
		return h, nil
	}
	oa, err := l.Oracle()
	if err != nil {
		return nil, err
	}
	if oa.IsZero() {
		return nil, fmt.Errorf("lending: oracle not set, cannot value the position")
	}
	o := NewPriceOracle(l.stateDB, oa)
	for _, g := range legs {
		price, err := o.GetPrice(g.asset, height)
		if err != nil {
			return nil, fmt.Errorf("lending: price of %s: %w", g.asset, err)
		}
		if price == nil || price.Sign() <= 0 {
			return nil, fmt.Errorf("lending: oracle returned a non-positive price for %s", g.asset)
		}
		if g.deposit.Sign() > 0 {
			v := new(big.Int).Mul(g.deposit, price)
			h.borrowCap.Add(h.borrowCap, new(big.Int).Div(new(big.Int).Mul(v, big.NewInt(g.cf)), big.NewInt(BPS)))
			h.ltValue.Add(h.ltValue, new(big.Int).Div(new(big.Int).Mul(v, big.NewInt(g.lt)), big.NewInt(BPS)))
		}
		if g.debt.Sign() > 0 {
			h.debtValue.Add(h.debtValue, new(big.Int).Mul(g.debt, price))
		}
	}
	return h, nil
}

// AccountHealth is the read-side view of health: values and whether the
// position can be liquidated (debt above the liquidation-threshold value).
func (l *LendingProtocol) AccountHealth(user qcrypto.Address, height uint64) (map[string]any, error) {
	h, err := l.healthOpt(user, height, qcrypto.Address{}, nil, false, false)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"borrow_capacity_value":       h.borrowCap.String(),
		"liquidation_threshold_value": h.ltValue.String(),
		"debt_value":                  h.debtValue.String(),
		"has_debt":                    h.hasDebt,
		"liquidatable":                h.hasDebt && h.debtValue.Cmp(h.ltValue) > 0,
	}, nil
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

	// The withdrawal must leave every debt the caller has covered by the
	// collateral factor of what remains. This used to be a comment. A caller
	// with no debt anywhere is not priced and always may withdraw.
	if withdrawAmount.Sign() <= 0 {
		return nil, fmt.Errorf("lending withdraw: amount must be positive")
	}
	after, err := l.health(caller, currentBlock, asset, new(big.Int).Neg(withdrawAmount))
	if err != nil {
		return nil, fmt.Errorf("lending withdraw: %w", err)
	}
	if after.hasDebt && after.debtValue.Cmp(after.borrowCap) > 0 {
		return nil, fmt.Errorf("lending withdraw: would leave debt value %s above the borrow capacity %s of the remaining collateral",
			after.debtValue, after.borrowCap)
	}
	// health() accrued and stored the pool; reload so the write below carries
	// the same index.
	if pool, err = l.GetPool(asset); err != nil || pool == nil {
		return nil, fmt.Errorf("lending withdraw: asset not registered")
	}

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

	// Health AFTER borrowing, over the caller's WHOLE position: every
	// collateral deposit at its own collateral factor against every debt,
	// all at oracle prices. The named collateralAsset must be a registered
	// pool the caller has deposited into (the API still names it), but it is
	// no longer the only collateral counted, and other debts are no longer
	// ignored.
	colPool, _ := l.GetPool(collateralAsset)
	if colPool == nil {
		return fmt.Errorf("lending borrow: collateral asset not registered")
	}
	colPos, err := l.getUserPosition(caller, collateralAsset)
	if err != nil {
		return err
	}
	if err := l.accrueInterest(collateralAsset, colPool, currentBlock); err != nil {
		return err
	}
	if l.normaliseDeposit(colPos.DepositBalance, colPos.InterestIndexAtDeposit, colPool.AccruedInterestIndex).Sign() == 0 {
		return fmt.Errorf("lending borrow: no collateral deposited")
	}
	if collateralPrice == nil || collateralPrice.Sign() <= 0 || borrowPrice == nil || borrowPrice.Sign() <= 0 {
		return fmt.Errorf("lending borrow: non-positive price")
	}
	h, err := l.health(caller, currentBlock, qcrypto.Address{}, nil)
	if err != nil {
		return fmt.Errorf("lending borrow: %w", err)
	}
	// health() prices only assets with a position; the new debt is priced
	// with the borrow price the caller supplied through the oracle path.
	newDebtValue := new(big.Int).Add(h.debtValue, new(big.Int).Mul(amount, borrowPrice))
	if !h.hasDebt {
		// No debt yet, so health() did not price the collateral. Do it now,
		// for every deposit the caller holds.
		h, err = l.pricedCapacity(caller, currentBlock)
		if err != nil {
			return fmt.Errorf("lending borrow: %w", err)
		}
	}
	if newDebtValue.Cmp(h.borrowCap) > 0 {
		return fmt.Errorf("lending borrow: would exceed collateral factor (debt value %s, borrow capacity %s)", newDebtValue, h.borrowCap)
	}
	// Reload the borrow pool: health() may have stored a newer index.
	if pool, err = l.GetPool(borrowAsset); err != nil || pool == nil {
		return fmt.Errorf("lending borrow: borrow asset not registered")
	}
	existingBorrow, _ := l.getUserBorrow(caller, borrowAsset)
	existingBorrowBalance := l.normaliseBorrow(existingBorrow.BorrowBalance, existingBorrow.InterestIndexAtBorrow, pool.AccruedInterestIndex)

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
	if liquidator == borrower {
		return nil, fmt.Errorf("liquidate: cannot liquidate yourself")
	}
	if repayAmount == nil || repayAmount.Sign() <= 0 {
		return nil, fmt.Errorf("liquidate: repay amount must be positive")
	}
	if collateralPrice == nil || collateralPrice.Sign() <= 0 || borrowPrice == nil || borrowPrice.Sign() <= 0 {
		return nil, fmt.Errorf("liquidate: non-positive price")
	}
	// Eligibility is judged on the borrower's WHOLE position: total debt above
	// the liquidation-threshold value of all collateral. A borrower who is
	// under water on one pair but well covered elsewhere is not liquidatable.
	// health() also accrues interest on every pool, so the balances read
	// below are current.
	h, err := l.health(borrower, currentBlock, qcrypto.Address{}, nil)
	if err != nil {
		return nil, fmt.Errorf("liquidate: %w", err)
	}
	if !h.hasDebt || h.debtValue.Cmp(h.ltValue) <= 0 {
		return nil, fmt.Errorf("liquidate: position is healthy (debt value %s, liquidation threshold value %s)", h.debtValue, h.ltValue)
	}
	borrowPool, _ := l.GetPool(borrowAsset)
	colPool, _ := l.GetPool(collateralAsset)
	if borrowPool == nil || colPool == nil {
		return nil, fmt.Errorf("liquidate: asset not registered")
	}
	colPos, err := l.getUserPosition(borrower, collateralAsset)
	if err != nil {
		return nil, err
	}
	borrow, err := l.getUserBorrow(borrower, borrowAsset)
	if err != nil {
		return nil, err
	}
	colBalance := l.normaliseDeposit(colPos.DepositBalance, colPos.InterestIndexAtDeposit, colPool.AccruedInterestIndex)
	debtBalance := l.normaliseBorrow(borrow.BorrowBalance, borrow.InterestIndexAtBorrow, borrowPool.AccruedInterestIndex)
	if debtBalance.Sign() == 0 {
		return nil, fmt.Errorf("liquidate: borrower owes nothing in %s", borrowAsset)
	}
	if colBalance.Sign() == 0 {
		return nil, fmt.Errorf("liquidate: borrower holds no %s collateral", collateralAsset)
	}

	// Close factor: at most 50% of THIS debt per liquidation.
	maxRepay := new(big.Int).Div(debtBalance, big.NewInt(2))
	if maxRepay.Sign() == 0 {
		maxRepay = new(big.Int).Set(debtBalance)
	}
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
	if err := l.setUserBorrow(borrower, borrowAsset, borrow); err != nil {
		return nil, err
	}
	borrowPool.TotalBorrows.Sub(borrowPool.TotalBorrows, repayAmount)
	if borrowPool.TotalBorrows.Sign() < 0 {
		borrowPool.TotalBorrows = big.NewInt(0)
	}
	if err := l.setPool(borrowAsset, borrowPool); err != nil {
		return nil, err
	}

	// Update borrower's collateral
	colPos.DepositBalance = new(big.Int).Sub(colBalance, seizedCollateral)
	colPos.InterestIndexAtDeposit = new(big.Int).Set(colPool.AccruedInterestIndex)
	if err := l.setUserPosition(borrower, collateralAsset, colPos); err != nil {
		return nil, err
	}
	colPool.TotalDeposits.Sub(colPool.TotalDeposits, seizedCollateral)
	if colPool.TotalDeposits.Sign() < 0 {
		colPool.TotalDeposits = big.NewInt(0)
	}
	if err := l.setPool(collateralAsset, colPool); err != nil {
		return nil, err
	}

	return seizedCollateral, nil
}

// LiquidateFromOracle is Liquidate at on-chain oracle prices. The public
// router must use this — never a caller-supplied price.
func (l *LendingProtocol) LiquidateFromOracle(liquidator, borrower, borrowAsset, collateralAsset qcrypto.Address, repayAmount *big.Int, height uint64) (*big.Int, error) {
	cp, bp, err := l.oraclePrices(collateralAsset, borrowAsset, height)
	if err != nil {
		return nil, err
	}
	return l.Liquidate(liquidator, borrower, borrowAsset, collateralAsset, repayAmount, cp, bp, height)
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
