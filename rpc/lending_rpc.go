// Package rpc — Lending Protocol HTTP API endpoints.
//
// Exposes the on-chain over-collateralised lending protocol through REST.
//
// Endpoints:
//   GET  /lending/pool/{asset}              — pool info (deposits, borrows, rate)
//   GET  /lending/position/{user}/{asset}   — user's deposit balance
//   GET  /lending/borrow/{user}/{asset}     — user's outstanding borrow
//   GET  /lending/health/{user}             — health factor across all positions
//   GET  /lending/utilisation/{asset}       — current utilisation rate
package rpc

import (
	"fmt"
	"math/big"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog/log"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
)

// RegisterLendingRoutes adds lending protocol endpoints to the router.
func RegisterLendingRoutes(r *mux.Router, stateDB *state.StateDB) {
	// Derive the well-known lending address. This must NOT deploy: registering
	// HTTP routes is not block execution, and DeployLending writes a consensus
	// state leaf. Doing that here put a write into the next sealed block that no
	// transaction accounted for, so block 1's state root could not be reproduced
	// by a peer replaying history — which is why followers failed to sync with
	// "state root mismatch" at block 1.
	lendingAddr := vm.LendingAddress(qcrypto.Address{}, 1)
	lending := vm.NewLendingProtocol(stateDB, lendingAddr)

	r.HandleFunc("/lending/pool/{asset}", makeLendingPoolHandler(lending)).Methods(http.MethodGet)
	r.HandleFunc("/lending/position/{user}/{asset}", makeLendingPositionHandler(lending)).Methods(http.MethodGet)
	r.HandleFunc("/lending/borrow/{user}/{asset}", makeLendingBorrowHandler(lending)).Methods(http.MethodGet)
	r.HandleFunc("/lending/utilisation/{asset}", makeLendingUtilHandler(lending)).Methods(http.MethodGet)
	r.HandleFunc("/lending/rates/{asset}", makeLendingRatesHandler(lending)).Methods(http.MethodGet)

	log.Debug().Str("lending_addr", lendingAddr.String()).Msg("lending RPC routes registered")
}

func makeLendingPoolHandler(lending *vm.LendingProtocol) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		assetAddr, err := qcrypto.AddressFromString(mux.Vars(r)["asset"])
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid asset address: %v", err))
			return
		}
		pool, err := lending.GetPool(assetAddr)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if pool == nil {
			writeError(w, http.StatusNotFound, "asset not registered in lending protocol")
			return
		}
		util, _ := lending.UtilisationRate(assetAddr)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"asset":                    assetAddr.String(),
			"total_deposits":           pool.TotalDeposits.String(),
			"total_borrows":            pool.TotalBorrows.String(),
			"reserve_balance":          pool.ReserveBalance.String(),
			"utilisation_bps":          util,
			"collateral_factor_bps":    pool.CollateralFactorBPS,
			"liquidation_threshold_bps": pool.LiquidationThresholdBPS,
			"active":                   pool.Active,
			"last_block_height":        pool.LastBlockHeight,
			"updated_at":               time.Now().Unix(),
		})
	}
}

func makeLendingPositionHandler(lending *vm.LendingProtocol) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		user, err := qcrypto.AddressFromString(vars["user"])
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid user address")
			return
		}
		asset, err := qcrypto.AddressFromString(vars["asset"])
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid asset address")
			return
		}
		bal, err := lending.GetDepositBalance(user, asset)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{
			"user":            user.String(),
			"asset":           asset.String(),
			"deposit_balance": bal.String(),
		})
	}
}

func makeLendingBorrowHandler(lending *vm.LendingProtocol) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		user, err := qcrypto.AddressFromString(vars["user"])
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid user address")
			return
		}
		asset, err := qcrypto.AddressFromString(vars["asset"])
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid asset address")
			return
		}
		bal, err := lending.GetBorrowBalance(user, asset)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{
			"user":           user.String(),
			"asset":          asset.String(),
			"borrow_balance": bal.String(),
		})
	}
}

func makeLendingUtilHandler(lending *vm.LendingProtocol) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		assetAddr, err := qcrypto.AddressFromString(mux.Vars(r)["asset"])
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid asset address")
			return
		}
		util, err := lending.UtilisationRate(assetAddr)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		pct := new(big.Float).Quo(
			new(big.Float).SetUint64(util),
			big.NewFloat(100),
		)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"asset":           assetAddr.String(),
			"utilisation_bps": util,
			"utilisation_pct": pct.Text('f', 2) + "%",
		})
	}
}

func makeLendingRatesHandler(lending *vm.LendingProtocol) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		assetAddr, err := qcrypto.AddressFromString(mux.Vars(r)["asset"])
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid asset address")
			return
		}
		util, _ := lending.UtilisationRate(assetAddr)
		// rate = base + slope * utilisation / BPS  (in BPS)
		rateBPS := uint64(vm.BaseRatePerBlockBPS) + uint64(vm.SlopePerBlockBPS)*util/vm.BPS
		// Approximate APR: rate * blocks_per_year (assuming 2s blocks, 365 days)
		blocksPerYear := uint64(365 * 24 * 3600 / 2)
		aprBPS := rateBPS * blocksPerYear

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"asset":                assetAddr.String(),
			"utilisation_bps":      util,
			"borrow_rate_bps":      rateBPS,
			"supply_rate_bps":      rateBPS * util / uint64(vm.BPS) * (vm.BPS - vm.ReserveFactorBPS) / vm.BPS,
			"borrow_apr_bps":       aprBPS,
			"base_rate_per_block":  vm.BaseRatePerBlockBPS,
			"slope_per_block":      vm.SlopePerBlockBPS,
			"reserve_factor_bps":   vm.ReserveFactorBPS,
		})
	}
}
