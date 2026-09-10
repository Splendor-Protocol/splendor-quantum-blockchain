// Package rpc — DEX HTTP API endpoints for Splendor Quantum Chain.
//
// Exposes the on-chain AMM pools through a REST interface.
// Useful for wallets, trading bots, and analytics dashboards.
//
// Endpoints:
//   GET  /dex/pool/{token0}/{token1}           — pool info (reserves, price, LP)
//   GET  /dex/pool/{token0}/{token1}/quote     — quote a swap
//   GET  /dex/pool/{token0}/{token1}/lp/{addr} — LP balance for an address
//   GET  /dex/price/{token0}/{token1}          — current spot price
//
// All addresses: SQ: format. No ECDSA. No keccak.
package rpc

import (
	"fmt"
	"math/big"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog/log"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
)

// RegisterDEXRoutes adds DEX endpoints to the router.
func RegisterDEXRoutes(r *mux.Router, stateDB *state.StateDB) {
	r.HandleFunc("/dex/pool/{token0}/{token1}", makeDEXPoolInfoHandler(stateDB)).Methods(http.MethodGet)
	r.HandleFunc("/dex/pool/{token0}/{token1}/quote", makeDEXQuoteHandler(stateDB)).Methods(http.MethodGet)
	r.HandleFunc("/dex/pool/{token0}/{token1}/lp/{addr}", makeDEXLPHandler(stateDB)).Methods(http.MethodGet)
	r.HandleFunc("/dex/price/{token0}/{token1}", makeDEXPriceHandler(stateDB)).Methods(http.MethodGet)
	r.HandleFunc("/dex/amount-out", makeDEXAmountOutHandler()).Methods(http.MethodGet)
	r.HandleFunc("/dex/amount-in", makeDEXAmountInHandler()).Methods(http.MethodGet)

	log.Debug().Msg("DEX RPC routes registered")
}

// parseTokenPair parses token0 and token1 from URL vars.
func parseTokenPair(vars map[string]string) (qcrypto.Address, qcrypto.Address, error) {
	t0, err := qcrypto.AddressFromString(vars["token0"])
	if err != nil {
		return qcrypto.Address{}, qcrypto.Address{}, fmt.Errorf("invalid token0: %v", err)
	}
	t1, err := qcrypto.AddressFromString(vars["token1"])
	if err != nil {
		return qcrypto.Address{}, qcrypto.Address{}, fmt.Errorf("invalid token1: %v", err)
	}
	return t0, t1, nil
}

// makeDEXPoolInfoHandler returns full pool info.
func makeDEXPoolInfoHandler(stateDB *state.StateDB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t0, t1, err := parseTokenPair(mux.Vars(r))
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		poolAddr := vm.PoolAddress(t0, t1)
		pool := vm.NewLiquidityPool(stateDB, poolAddr)

		info, err := pool.Info()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		writeJSON(w, http.StatusOK, info)
	}
}

// makeDEXQuoteHandler returns a swap quote without executing.
// Query params: ?amount_in=...&token_in=0|1
func makeDEXQuoteHandler(stateDB *state.StateDB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t0, t1, err := parseTokenPair(mux.Vars(r))
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		amountInStr := r.URL.Query().Get("amount_in")
		tokenInStr := r.URL.Query().Get("token_in")
		if amountInStr == "" {
			writeError(w, http.StatusBadRequest, "amount_in is required")
			return
		}

		amountIn, ok := new(big.Int).SetString(amountInStr, 10)
		if !ok || amountIn.Sign() <= 0 {
			writeError(w, http.StatusBadRequest, "invalid amount_in")
			return
		}

		tokenIn := uint8(0)
		if tokenInStr == "1" {
			tokenIn = 1
		}

		poolAddr := vm.PoolAddress(t0, t1)
		pool := vm.NewLiquidityPool(stateDB, poolAddr)

		r0, r1, err := pool.Reserves()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		var reserveIn, reserveOut *big.Int
		if tokenIn == 0 {
			reserveIn, reserveOut = r0, r1
		} else {
			reserveIn, reserveOut = r1, r0
		}

		amountOut, err := vm.GetAmountOut(amountIn, reserveIn, reserveOut)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		// Compute price impact as basis points
		// priceImpact = (1 - amountOut/reserveOut * reserveIn/amountIn) * 10000
		var priceImpactBPS int64
		if reserveIn.Sign() > 0 && reserveOut.Sign() > 0 {
			// midPrice = reserveOut / reserveIn
			// executionPrice = amountOut / amountIn
			// impact = (midPrice - executionPrice) / midPrice
			midPrice := new(big.Float).Quo(
				new(big.Float).SetInt(reserveOut),
				new(big.Float).SetInt(reserveIn),
			)
			execPrice := new(big.Float).Quo(
				new(big.Float).SetInt(amountOut),
				new(big.Float).SetInt(amountIn),
			)
			diff := new(big.Float).Sub(midPrice, execPrice)
			impactRatio := new(big.Float).Quo(diff, midPrice)
			impactBPS, _ := new(big.Float).Mul(impactRatio, big.NewFloat(10000)).Int64()
			priceImpactBPS = impactBPS
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"pool":              poolAddr.String(),
			"token_in":          tokenIn,
			"amount_in":         amountIn.String(),
			"amount_out":        amountOut.String(),
			"reserve_in":        reserveIn.String(),
			"reserve_out":       reserveOut.String(),
			"fee_bps":           vm.SwapFeeBPS,
			"price_impact_bps":  priceImpactBPS,
		})
	}
}

// makeDEXLPHandler returns LP balance for an address.
func makeDEXLPHandler(stateDB *state.StateDB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		t0, t1, err := parseTokenPair(vars)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		addr, err := qcrypto.AddressFromString(vars["addr"])
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid address: %v", err))
			return
		}

		poolAddr := vm.PoolAddress(t0, t1)
		pool := vm.NewLiquidityPool(stateDB, poolAddr)

		bal, err := pool.LPBalance(addr)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		total, _ := pool.TotalLPShares()

		var sharePercent string
		if total.Sign() > 0 {
			pct := new(big.Float).Quo(
				new(big.Float).SetInt(bal),
				new(big.Float).SetInt(total),
			)
			pct.Mul(pct, big.NewFloat(100))
			sharePercent = pct.Text('f', 4) + "%"
		} else {
			sharePercent = "0%"
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"pool":          poolAddr.String(),
			"address":       addr.String(),
			"lp_balance":    bal.String(),
			"lp_total":      total.String(),
			"pool_share":    sharePercent,
		})
	}
}

// makeDEXPriceHandler returns the current spot price.
func makeDEXPriceHandler(stateDB *state.StateDB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t0, t1, err := parseTokenPair(mux.Vars(r))
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		poolAddr := vm.PoolAddress(t0, t1)
		pool := vm.NewLiquidityPool(stateDB, poolAddr)

		r0, r1, err := pool.Reserves()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		if r0.Sign() == 0 || r1.Sign() == 0 {
			writeError(w, http.StatusNotFound, "pool has no liquidity")
			return
		}

		// token1_per_token0 (18 decimal fixed-point)
		spotNum, _, err := pool.SpotPrice()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		// Also compute token0_per_token1
		scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
		reverseNum := new(big.Int).Mul(r0, scale)
		reverseNum.Div(reverseNum, r1)

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"pool":                poolAddr.String(),
			"token0":              t0.String(),
			"token1":              t1.String(),
			"token1_per_token0":   spotNum.String(),   // how many token1 per 1 token0
			"token0_per_token1":   reverseNum.String(), // how many token0 per 1 token1
			"scale":               "1e18",
			"reserve0":            r0.String(),
			"reserve1":            r1.String(),
		})
	}
}

// makeDEXAmountOutHandler — stateless GetAmountOut calculation.
// GET /dex/amount-out?amount_in=...&reserve_in=...&reserve_out=...
func makeDEXAmountOutHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		amountIn, ok1 := new(big.Int).SetString(q.Get("amount_in"), 10)
		reserveIn, ok2 := new(big.Int).SetString(q.Get("reserve_in"), 10)
		reserveOut, ok3 := new(big.Int).SetString(q.Get("reserve_out"), 10)
		if !ok1 || !ok2 || !ok3 {
			writeError(w, http.StatusBadRequest, "amount_in, reserve_in, reserve_out required as decimal strings")
			return
		}

		amountOut, err := vm.GetAmountOut(amountIn, reserveIn, reserveOut)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{
			"amount_in":  amountIn.String(),
			"amount_out": amountOut.String(),
			"fee_bps":    fmt.Sprintf("%d", vm.SwapFeeBPS),
		})
	}
}

// makeDEXAmountInHandler — stateless GetAmountIn calculation.
// GET /dex/amount-in?amount_out=...&reserve_in=...&reserve_out=...
func makeDEXAmountInHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		amountOut, ok1 := new(big.Int).SetString(q.Get("amount_out"), 10)
		reserveIn, ok2 := new(big.Int).SetString(q.Get("reserve_in"), 10)
		reserveOut, ok3 := new(big.Int).SetString(q.Get("reserve_out"), 10)
		if !ok1 || !ok2 || !ok3 {
			writeError(w, http.StatusBadRequest, "amount_out, reserve_in, reserve_out required as decimal strings")
			return
		}

		amountIn, err := vm.GetAmountIn(amountOut, reserveIn, reserveOut)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{
			"amount_in":  amountIn.String(),
			"amount_out": amountOut.String(),
			"fee_bps":    fmt.Sprintf("%d", vm.SwapFeeBPS),
		})
	}
}
