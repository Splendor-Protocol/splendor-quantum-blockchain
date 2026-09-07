// Package vm — SQC DEX: Quantum-native Automated Market Maker.
//
// This implements the constant-product AMM (x * y = k) — the same model as
// Uniswap v2 — but entirely quantum-safe: SHA3-256 storage slots, SQ: addresses,
// ML-DSA-65-signed transactions, zero ECDSA.
//
// Each liquidity pool is a pair of two QRC-20 tokens.
// Liquidity providers deposit both tokens and receive LP shares.
// Traders swap one token for another, paying a 0.3% fee to LPs.
//
// Pool mechanics:
//   Invariant:     token0_reserve * token1_reserve = k
//   Swap formula:  amountOut = reserve1 * amountIn_after_fee / (reserve0 + amountIn_after_fee)
//   Fee:           0.3% (30 basis points) deducted from amountIn before swap
//   LP shares:     minted proportionally to liquidity added, first LP sets exchange rate
//   Min liquidity: 1000 units locked forever (prevents precision attacks)
//
// All state: SHA3-256(namespace+key) storage slots. No keccak. No ECDSA.
package vm

import (
	"encoding/json"
	"fmt"
	"math/big"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
)

const (
	// SwapFeeBPS is the LP fee in basis points (30 = 0.30%)
	SwapFeeBPS = 30
	// MinLiquidity is locked permanently on first deposit (prevents inflation attacks)
	MinLiquidity = 1000
)

// ── Pool storage slots ─────────────────────────────────────────────────────────
//
// reserve0          = SHA3-256("dex:r0")
// reserve1          = SHA3-256("dex:r1")
// token0            = SHA3-256("dex:t0")
// token1            = SHA3-256("dex:t1")
// total_lp_shares   = SHA3-256("dex:lp_total")
// lp_balance[addr]  = SHA3-256("dex:lp:" + addr)
// lp_allowance[o][s]= SHA3-256("dex:lpa:" + owner + spender)
// cumulative_fee0   = SHA3-256("dex:fee0")  (cumulative fees in token0)
// cumulative_fee1   = SHA3-256("dex:fee1")

var (
	slotDexReserve0   = qcrypto.Hash32([]byte("dex:r0"))
	slotDexReserve1   = qcrypto.Hash32([]byte("dex:r1"))
	slotDexToken0     = qcrypto.Hash32([]byte("dex:t0"))
	slotDexToken1     = qcrypto.Hash32([]byte("dex:t1"))
	slotDexLPTotal    = qcrypto.Hash32([]byte("dex:lp_total"))
	slotDexFee0       = qcrypto.Hash32([]byte("dex:fee0"))
	slotDexFee1       = qcrypto.Hash32([]byte("dex:fee1"))
	slotDexBlockLast  = qcrypto.Hash32([]byte("dex:block_last"))
)

func dexLPSlot(addr qcrypto.Address) [32]byte {
	return qcrypto.Hash32(append([]byte("dex:lp:"), addr[:]...))
}

func dexLPAllowanceSlot(owner, spender qcrypto.Address) [32]byte {
	key := append([]byte("dex:lpa:"), owner[:]...)
	key = append(key, spender[:]...)
	return qcrypto.Hash32(key)
}

// ── LiquidityPool ─────────────────────────────────────────────────────────────

// LiquidityPool is a constant-product AMM pool for two QRC-20 tokens.
type LiquidityPool struct {
	stateDB  *state.StateDB
	contract qcrypto.Address
}

// NewLiquidityPool returns a handle to an existing pool contract.
func NewLiquidityPool(stateDB *state.StateDB, contract qcrypto.Address) *LiquidityPool {
	return &LiquidityPool{stateDB: stateDB, contract: contract}
}

// DeployPool deploys a new liquidity pool for token0/token1.
// Token addresses must be in canonical order (token0 < token1 by address bytes).
func DeployPool(
	stateDB *state.StateDB,
	deployer qcrypto.Address,
	token0, token1 qcrypto.Address,
	deployerNonce uint64,
) (qcrypto.Address, error) {
	// Enforce canonical token order (prevents duplicate pools)
	for i := 0; i < qcrypto.AddressSize; i++ {
		if token0[i] < token1[i] {
			break
		}
		if token0[i] > token1[i] {
			token0, token1 = token1, token0
			break
		}
	}

	// Deterministic pool address: SHA3-256(token0 + token1 + "pool")
	input := append(token0[:], token1[:]...)
	input = append(input, []byte("pool")...)
	hash := qcrypto.Hash32(input)
	var poolAddr qcrypto.Address
	copy(poolAddr[:], hash[:])

	// The address depends only on the token pair, so anyone can recompute it and
	// call DeployPool again on a live pool. Re-running the initialisation below
	// would zero reserve0, reserve1 and lp_total: the reserves would stop
	// matching the tokens the pool actually holds, every existing LP position
	// would be orphaned, and the next depositor would be treated as the first
	// and could claim the whole pool. Initialise once only.
	if existing, err := stateDB.GetStorageValue(poolAddr, slotDexToken0); err == nil && len(existing) > 0 {
		return qcrypto.Address{}, fmt.Errorf("dex deploy_pool: pool %s already exists for this token pair", poolAddr)
	}

	p := NewLiquidityPool(stateDB, poolAddr)
	if err := stateDB.SetStorageValue(poolAddr, slotDexToken0, token0[:]); err != nil {
		return qcrypto.Address{}, err
	}
	if err := stateDB.SetStorageValue(poolAddr, slotDexToken1, token1[:]); err != nil {
		return qcrypto.Address{}, err
	}
	if err := p.setBigInt(slotDexReserve0, big.NewInt(0)); err != nil {
		return qcrypto.Address{}, err
	}
	if err := p.setBigInt(slotDexReserve1, big.NewInt(0)); err != nil {
		return qcrypto.Address{}, err
	}
	if err := p.setBigInt(slotDexLPTotal, big.NewInt(0)); err != nil {
		return qcrypto.Address{}, err
	}
	return poolAddr, nil
}

// ── Pool info ─────────────────────────────────────────────────────────────────

// Token0 returns the address of the first token.
func (p *LiquidityPool) Token0() (qcrypto.Address, error) {
	return p.getAddress(slotDexToken0)
}

// Token1 returns the address of the second token.
func (p *LiquidityPool) Token1() (qcrypto.Address, error) {
	return p.getAddress(slotDexToken1)
}

// Reserves returns the current pool reserves (reserve0, reserve1).
func (p *LiquidityPool) Reserves() (*big.Int, *big.Int, error) {
	r0, err := p.getBigInt(slotDexReserve0)
	if err != nil {
		return nil, nil, err
	}
	r1, err := p.getBigInt(slotDexReserve1)
	if err != nil {
		return nil, nil, err
	}
	return r0, r1, nil
}

// LPBalance returns the LP token balance of an address.
func (p *LiquidityPool) LPBalance(addr qcrypto.Address) (*big.Int, error) {
	return p.getBigInt(dexLPSlot(addr))
}

// TotalLPShares returns the total LP shares outstanding.
func (p *LiquidityPool) TotalLPShares() (*big.Int, error) {
	return p.getBigInt(slotDexLPTotal)
}

// ── Price oracle ──────────────────────────────────────────────────────────────

// SpotPrice returns the current price of token0 in terms of token1.
// price = reserve1 / reserve0 (as a rational number: numerator / denominator)
func (p *LiquidityPool) SpotPrice() (*big.Int, *big.Int, error) {
	r0, r1, err := p.Reserves()
	if err != nil {
		return nil, nil, err
	}
	if r0.Sign() == 0 {
		return nil, nil, fmt.Errorf("dex: pool has no liquidity")
	}
	// price = r1 * 10^18 / r0 (fixed-point with 18 decimal places)
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	num := new(big.Int).Mul(r1, scale)
	num.Div(num, r0)
	return num, scale, nil
}

// GetAmountOut calculates the output amount for a given input,
// accounting for the 0.3% swap fee.
// amountOut = reserve_out * amountIn * (10000 - fee) / (reserve_in * 10000 + amountIn * (10000 - fee))
func GetAmountOut(amountIn, reserveIn, reserveOut *big.Int) (*big.Int, error) {
	if amountIn.Sign() <= 0 {
		return nil, fmt.Errorf("dex: insufficient input amount")
	}
	if reserveIn.Sign() <= 0 || reserveOut.Sign() <= 0 {
		return nil, fmt.Errorf("dex: insufficient liquidity")
	}

	// Apply fee: effective amountIn = amountIn * (10000 - 30) = amountIn * 9970
	amountInWithFee := new(big.Int).Mul(amountIn, big.NewInt(10000-SwapFeeBPS))

	// numerator = reserveOut * amountInWithFee
	numerator := new(big.Int).Mul(reserveOut, amountInWithFee)

	// denominator = reserveIn * 10000 + amountInWithFee
	denominator := new(big.Int).Mul(reserveIn, big.NewInt(10000))
	denominator.Add(denominator, amountInWithFee)

	if denominator.Sign() == 0 {
		return nil, fmt.Errorf("dex: zero denominator")
	}

	result := new(big.Int).Div(numerator, denominator)
	return result, nil
}

// GetAmountIn calculates the required input for a desired output.
func GetAmountIn(amountOut, reserveIn, reserveOut *big.Int) (*big.Int, error) {
	if amountOut.Sign() <= 0 {
		return nil, fmt.Errorf("dex: insufficient output amount")
	}
	if reserveIn.Sign() <= 0 || reserveOut.Sign() <= 0 {
		return nil, fmt.Errorf("dex: insufficient liquidity")
	}
	if amountOut.Cmp(reserveOut) >= 0 {
		return nil, fmt.Errorf("dex: insufficient liquidity for desired output")
	}

	// amountIn = reserveIn * amountOut * 10000 / ((reserveOut - amountOut) * (10000 - fee)) + 1
	numerator := new(big.Int).Mul(reserveIn, amountOut)
	numerator.Mul(numerator, big.NewInt(10000))

	denominator := new(big.Int).Sub(reserveOut, amountOut)
	denominator.Mul(denominator, big.NewInt(10000-SwapFeeBPS))

	result := new(big.Int).Div(numerator, denominator)
	result.Add(result, big.NewInt(1)) // ceiling
	return result, nil
}

// ── Swap ──────────────────────────────────────────────────────────────────────

// Swap executes a token swap. Caller specifies which token they send (amount0In or amount1In)
// and which they want back (amount0Out or amount1Out).
// Exactly one of {amount0In, amount1In} must be > 0.
// Exactly one of {amount0Out, amount1Out} must be > 0.
func (p *LiquidityPool) Swap(
	caller, recipient qcrypto.Address,
	amount0In, amount1In *big.Int,
	amount0Out, amount1Out *big.Int,
) error {
	if amount0Out.Sign() == 0 && amount1Out.Sign() == 0 {
		return fmt.Errorf("dex swap: must specify at least one output amount")
	}

	r0, r1, err := p.Reserves()
	if err != nil {
		return err
	}

	if amount0Out.Cmp(r0) >= 0 {
		return fmt.Errorf("dex swap: insufficient token0 liquidity")
	}
	if amount1Out.Cmp(r1) >= 0 {
		return fmt.Errorf("dex swap: insufficient token1 liquidity")
	}

	t0, err := p.Token0()
	if err != nil {
		return err
	}
	t1, err := p.Token1()
	if err != nil {
		return err
	}

	tok0 := NewQRC20Token(p.stateDB, t0)
	tok1 := NewQRC20Token(p.stateDB, t1)

	// Transfer inputs from caller to pool
	if amount0In.Sign() > 0 {
		if err := tok0.Transfer(caller, p.contract, amount0In); err != nil {
			return fmt.Errorf("dex swap: token0 input transfer failed: %w", err)
		}
	}
	if amount1In.Sign() > 0 {
		if err := tok1.Transfer(caller, p.contract, amount1In); err != nil {
			return fmt.Errorf("dex swap: token1 input transfer failed: %w", err)
		}
	}

	// Transfer outputs from pool to recipient
	if amount0Out.Sign() > 0 {
		if err := tok0.Transfer(p.contract, recipient, amount0Out); err != nil {
			return fmt.Errorf("dex swap: token0 output transfer failed: %w", err)
		}
	}
	if amount1Out.Sign() > 0 {
		if err := tok1.Transfer(p.contract, recipient, amount1Out); err != nil {
			return fmt.Errorf("dex swap: token1 output transfer failed: %w", err)
		}
	}

	// Compute new reserves and verify invariant
	newR0 := new(big.Int).Add(r0, amount0In)
	newR0.Sub(newR0, amount0Out)
	newR1 := new(big.Int).Add(r1, amount1In)
	newR1.Sub(newR1, amount1Out)

	if newR0.Sign() <= 0 || newR1.Sign() <= 0 {
		return fmt.Errorf("dex swap: reserves would go non-positive")
	}

	// Verify: new_r0 * new_r1 >= old_r0 * old_r1 (adjusted for fee)
	// Adjusted: balance0Adjusted = newR0 * 10000 - amount0In * fee
	adj0 := new(big.Int).Mul(newR0, big.NewInt(10000))
	adj0.Sub(adj0, new(big.Int).Mul(amount0In, big.NewInt(SwapFeeBPS)))
	adj1 := new(big.Int).Mul(newR1, big.NewInt(10000))
	adj1.Sub(adj1, new(big.Int).Mul(amount1In, big.NewInt(SwapFeeBPS)))

	kNew := new(big.Int).Mul(adj0, adj1)
	kOld := new(big.Int).Mul(r0, r1)
	kOld.Mul(kOld, big.NewInt(10000*10000))

	if kNew.Cmp(kOld) < 0 {
		return fmt.Errorf("dex swap: invariant K violated")
	}

	// Update reserves
	if err := p.setBigInt(slotDexReserve0, newR0); err != nil {
		return err
	}
	return p.setBigInt(slotDexReserve1, newR1)
}

// SwapExactIn swaps an exact input amount for the maximum possible output.
// tokenIn = 0 means swap token0 for token1; tokenIn = 1 means swap token1 for token0.
func (p *LiquidityPool) SwapExactIn(
	caller, recipient qcrypto.Address,
	tokenIn uint8,
	amountIn *big.Int,
	minAmountOut *big.Int,
) (*big.Int, error) {
	r0, r1, err := p.Reserves()
	if err != nil {
		return nil, err
	}

	var amountOut *big.Int
	var amount0In, amount1In, amount0Out, amount1Out *big.Int

	if tokenIn == 0 {
		amountOut, err = GetAmountOut(amountIn, r0, r1)
		if err != nil {
			return nil, err
		}
		amount0In, amount1In = amountIn, big.NewInt(0)
		amount0Out, amount1Out = big.NewInt(0), amountOut
	} else {
		amountOut, err = GetAmountOut(amountIn, r1, r0)
		if err != nil {
			return nil, err
		}
		amount0In, amount1In = big.NewInt(0), amountIn
		amount0Out, amount1Out = amountOut, big.NewInt(0)
	}

	if amountOut.Cmp(minAmountOut) < 0 {
		return nil, fmt.Errorf("dex: slippage exceeded (got %s, min %s)", amountOut, minAmountOut)
	}

	return amountOut, p.Swap(caller, recipient, amount0In, amount1In, amount0Out, amount1Out)
}

// ── Liquidity management ──────────────────────────────────────────────────────

// AddLiquidity deposits token0 and token1 into the pool and mints LP shares.
// First depositor sets the exchange rate. Subsequent depositors must maintain ratio.
// Returns the actual amounts deposited and LP shares minted.
func (p *LiquidityPool) AddLiquidity(
	caller qcrypto.Address,
	amount0Desired, amount1Desired *big.Int,
	amount0Min, amount1Min *big.Int,
) (amount0, amount1, lpShares *big.Int, err error) {
	r0, r1, err := p.Reserves()
	if err != nil {
		return nil, nil, nil, err
	}
	totalLP, err := p.TotalLPShares()
	if err != nil {
		return nil, nil, nil, err
	}

	if totalLP.Sign() == 0 {
		// First depositor: use desired amounts as-is
		amount0 = new(big.Int).Set(amount0Desired)
		amount1 = new(big.Int).Set(amount1Desired)
		// Initial LP shares = sqrt(amount0 * amount1) - MinLiquidity
		product := new(big.Int).Mul(amount0, amount1)
		lpShares = sqrtBigInt(product)
		lpShares.Sub(lpShares, big.NewInt(MinLiquidity))
		if lpShares.Sign() <= 0 {
			return nil, nil, nil, fmt.Errorf("dex addLiquidity: insufficient initial liquidity")
		}
		// Lock MinLiquidity forever at zero address
		var zeroAddr qcrypto.Address
		p.setBigInt(dexLPSlot(zeroAddr), big.NewInt(MinLiquidity))
		p.setBigInt(slotDexLPTotal, big.NewInt(MinLiquidity))
	} else {
		// Subsequent depositors: maintain ratio
		// Optimal amount1 for desired amount0
		amount1Optimal := new(big.Int).Mul(amount0Desired, r1)
		amount1Optimal.Div(amount1Optimal, r0)

		if amount1Optimal.Cmp(amount1Desired) <= 0 {
			// amount0Desired limits; use all of amount0Desired
			if amount1Optimal.Cmp(amount1Min) < 0 {
				return nil, nil, nil, fmt.Errorf("dex addLiquidity: insufficient token1 (slippage)")
			}
			amount0 = new(big.Int).Set(amount0Desired)
			amount1 = amount1Optimal
		} else {
			// amount1Desired limits; use all of amount1Desired
			amount0Optimal := new(big.Int).Mul(amount1Desired, r0)
			amount0Optimal.Div(amount0Optimal, r1)
			if amount0Optimal.Cmp(amount0Min) < 0 {
				return nil, nil, nil, fmt.Errorf("dex addLiquidity: insufficient token0 (slippage)")
			}
			amount0 = amount0Optimal
			amount1 = new(big.Int).Set(amount1Desired)
		}

		// LP shares = min(amount0/r0, amount1/r1) * totalLP
		lp0 := new(big.Int).Mul(amount0, totalLP)
		lp0.Div(lp0, r0)
		lp1 := new(big.Int).Mul(amount1, totalLP)
		lp1.Div(lp1, r1)
		if lp0.Cmp(lp1) <= 0 {
			lpShares = lp0
		} else {
			lpShares = lp1
		}
		if lpShares.Sign() <= 0 {
			return nil, nil, nil, fmt.Errorf("dex addLiquidity: zero LP shares")
		}
	}

	// Transfer tokens into pool
	t0, _ := p.Token0()
	t1, _ := p.Token1()
	tok0 := NewQRC20Token(p.stateDB, t0)
	tok1 := NewQRC20Token(p.stateDB, t1)

	if err := tok0.Transfer(caller, p.contract, amount0); err != nil {
		return nil, nil, nil, fmt.Errorf("dex addLiquidity: token0 transfer failed: %w", err)
	}
	if err := tok1.Transfer(caller, p.contract, amount1); err != nil {
		return nil, nil, nil, fmt.Errorf("dex addLiquidity: token1 transfer failed: %w", err)
	}

	// Mint LP shares
	callerBal, _ := p.LPBalance(caller)
	p.setBigInt(dexLPSlot(caller), new(big.Int).Add(callerBal, lpShares))
	total, _ := p.TotalLPShares()
	p.setBigInt(slotDexLPTotal, new(big.Int).Add(total, lpShares))

	// Update reserves
	newR0 := new(big.Int).Add(r0, amount0)
	newR1 := new(big.Int).Add(r1, amount1)
	p.setBigInt(slotDexReserve0, newR0)
	p.setBigInt(slotDexReserve1, newR1)

	return amount0, amount1, lpShares, nil
}

// RemoveLiquidity burns LP shares and returns proportional token0 + token1 amounts.
func (p *LiquidityPool) RemoveLiquidity(
	caller qcrypto.Address,
	lpShares *big.Int,
	amount0Min, amount1Min *big.Int,
) (amount0, amount1 *big.Int, err error) {
	callerBal, err := p.LPBalance(caller)
	if err != nil {
		return nil, nil, err
	}
	if callerBal.Cmp(lpShares) < 0 {
		return nil, nil, fmt.Errorf("dex removeLiquidity: insufficient LP balance")
	}

	totalLP, _ := p.TotalLPShares()
	r0, r1, _ := p.Reserves()

	// amount = (lpShares / totalLP) * reserve
	amount0 = new(big.Int).Mul(lpShares, r0)
	amount0.Div(amount0, totalLP)
	amount1 = new(big.Int).Mul(lpShares, r1)
	amount1.Div(amount1, totalLP)

	if amount0.Cmp(amount0Min) < 0 {
		return nil, nil, fmt.Errorf("dex removeLiquidity: token0 below minimum")
	}
	if amount1.Cmp(amount1Min) < 0 {
		return nil, nil, fmt.Errorf("dex removeLiquidity: token1 below minimum")
	}

	// Burn LP shares
	p.setBigInt(dexLPSlot(caller), new(big.Int).Sub(callerBal, lpShares))
	p.setBigInt(slotDexLPTotal, new(big.Int).Sub(totalLP, lpShares))

	// Transfer tokens out
	t0, _ := p.Token0()
	t1, _ := p.Token1()
	tok0 := NewQRC20Token(p.stateDB, t0)
	tok1 := NewQRC20Token(p.stateDB, t1)

	if err := tok0.Transfer(p.contract, caller, amount0); err != nil {
		return nil, nil, fmt.Errorf("dex removeLiquidity: token0 transfer failed: %w", err)
	}
	if err := tok1.Transfer(p.contract, caller, amount1); err != nil {
		return nil, nil, fmt.Errorf("dex removeLiquidity: token1 transfer failed: %w", err)
	}

	// Update reserves
	p.setBigInt(slotDexReserve0, new(big.Int).Sub(r0, amount0))
	p.setBigInt(slotDexReserve1, new(big.Int).Sub(r1, amount1))

	return amount0, amount1, nil
}

// ── Pool registry ─────────────────────────────────────────────────────────────

// PoolAddress computes the deterministic address for a token pair.
// Token order is canonicalised (smaller address first).
func PoolAddress(token0, token1 qcrypto.Address) qcrypto.Address {
	for i := 0; i < qcrypto.AddressSize; i++ {
		if token0[i] > token1[i] {
			token0, token1 = token1, token0
			break
		}
		if token0[i] < token1[i] {
			break
		}
	}
	input := append(token0[:], token1[:]...)
	input = append(input, []byte("pool")...)
	hash := qcrypto.Hash32(input)
	var addr qcrypto.Address
	copy(addr[:], hash[:])
	return addr
}

// ── Helpers ────────────────────────────────────────────────────────────────────

// sqrtBigInt computes the integer square root using Newton's method.
func sqrtBigInt(n *big.Int) *big.Int {
	if n.Sign() == 0 {
		return big.NewInt(0)
	}
	// Initial estimate
	x := new(big.Int).Set(n)
	y := new(big.Int).Add(new(big.Int).Rsh(x, 1), big.NewInt(1))
	for y.Cmp(x) < 0 {
		x.Set(y)
		y.Add(new(big.Int).Div(n, x), x)
		y.Rsh(y, 1)
	}
	return x
}

func (p *LiquidityPool) setBigInt(slot [32]byte, v *big.Int) error {
	return p.stateDB.SetStorageValue(p.contract, slot, v.Bytes())
}

func (p *LiquidityPool) getBigInt(slot [32]byte) (*big.Int, error) {
	b, err := p.stateDB.GetStorageValue(p.contract, slot)
	if err != nil || len(b) == 0 {
		return big.NewInt(0), nil
	}
	return new(big.Int).SetBytes(b), nil
}

func (p *LiquidityPool) getAddress(slot [32]byte) (qcrypto.Address, error) {
	b, err := p.stateDB.GetStorageValue(p.contract, slot)
	if err != nil || len(b) < qcrypto.AddressSize {
		return qcrypto.Address{}, nil
	}
	var addr qcrypto.Address
	copy(addr[:], b)
	return addr, nil
}

// PoolInfo is a serialisable summary of a pool's state.
type PoolInfo struct {
	Address   string `json:"address"`
	Token0    string `json:"token0"`
	Token1    string `json:"token1"`
	Reserve0  string `json:"reserve0"`
	Reserve1  string `json:"reserve1"`
	LPTotal   string `json:"lp_total"`
	SpotPrice string `json:"spot_price"` // token1 per token0, fixed-point 18 decimals
}

// Info returns a serialisable summary of the pool.
func (p *LiquidityPool) Info() (*PoolInfo, error) {
	t0, _ := p.Token0()
	t1, _ := p.Token1()
	r0, r1, _ := p.Reserves()
	lp, _ := p.TotalLPShares()

	info := &PoolInfo{
		Address:  p.contract.String(),
		Token0:   t0.String(),
		Token1:   t1.String(),
		Reserve0: r0.String(),
		Reserve1: r1.String(),
		LPTotal:  lp.String(),
	}

	if r0.Sign() > 0 {
		spotNum, _, err := p.SpotPrice()
		if err == nil {
			info.SpotPrice = spotNum.String()
		}
	}
	return info, nil
}

// poolInfoJSON is used for storage only (not exported).
type poolInfoJSON = PoolInfo

// compile-time interface check
var _ = json.Marshal
