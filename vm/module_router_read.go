// Package vm — read-only dispatch for native-module view calls (eth_call).
//
// DispatchModuleCall (module_router.go) executes STATE-MUTATING module actions
// as part of block application; it returns only `error` and surfaces results via
// the receipt. This file adds the read-only counterpart used by the node's
// POST /call endpoint: it decodes the same ModuleCall envelope but serves only
// value-returning *read* actions and REJECTS every write action.
//
// Why a separate entry point rather than a new case in DispatchModuleCall:
//   - A view call must return a value; DispatchModuleCall returns only an error.
//   - Module writes go straight to LevelDB (StateDB.SetStorageValue → db.Put),
//     bypassing StateDB snapshots. Routing a *write* action through a read
//     endpoint would therefore mutate canonical state with no way to revert.
//     The read dispatch hard-rejects any action not on the read allowlist, so
//     /call can never write.
package vm

import (
	"encoding/json"
	"fmt"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
)

// qrc20ReadParams / dexReadParams are the parameter shapes for read actions.
// Reads need an explicit owner because a view call has no authenticated caller
// (writes take the owner implicitly from the tx sender).
type qrc20ReadParams struct {
	Token   string `json:"token"`             // contract SQ: address (required)
	Owner   string `json:"owner,omitempty"`   // balance_of / allowance
	Spender string `json:"spender,omitempty"` // allowance
}

type dexReadParams struct {
	Pool   string `json:"pool,omitempty"`   // explicit pool SQ: address, or…
	Token0 string `json:"token0,omitempty"` // …a token pair (pool derived via PoolAddress)
	Token1 string `json:"token1,omitempty"`
}

// DispatchModuleReadCall decodes a ModuleCall envelope and executes a read-only
// (value-returning) module action against committed state. It never mutates
// state: only the read allowlist is served; any write action is rejected with an
// error. The returned value is a JSON-serialisable map.
func DispatchModuleReadCall(stateDB *state.StateDB, data []byte) (interface{}, error) {
	var mc ModuleCall
	if err := json.Unmarshal(data, &mc); err != nil {
		return nil, fmt.Errorf("module call: invalid JSON envelope: %w", err)
	}
	switch mc.Module {
	case "qrc20":
		return dispatchQRC20Read(stateDB, mc.Action, mc.Params)
	case "dex":
		return dispatchDEXRead(stateDB, mc.Action, mc.Params)
	default:
		return nil, fmt.Errorf("module call: unknown module %q", mc.Module)
	}
}

func dispatchQRC20Read(stateDB *state.StateDB, action string, raw json.RawMessage) (interface{}, error) {
	var p qrc20ReadParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("qrc20 read: bad params: %w", err)
	}
	token, err := qcrypto.AddressFromString(p.Token)
	if err != nil {
		return nil, fmt.Errorf("qrc20 read: token: %w", err)
	}
	tok := NewQRC20Token(stateDB, token)
	exists := false
	if acc, aerr := stateDB.GetAccount(token); aerr == nil {
		exists = acc.IsContract
	}

	switch action {
	case "balance_of":
		owner, err := qcrypto.AddressFromString(p.Owner)
		if err != nil {
			return nil, fmt.Errorf("qrc20 balance_of: owner: %w", err)
		}
		bal, err := tok.BalanceOf(owner)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{
			"module": "qrc20", "action": "balance_of",
			"token": token.String(), "owner": owner.String(),
			"balance": bal.String(), "exists": exists,
		}, nil

	case "total_supply":
		supply, err := tok.TotalSupply()
		if err != nil {
			return nil, err
		}
		name, _ := tok.Name()
		symbol, _ := tok.Symbol()
		decimals, _ := tok.Decimals()
		return map[string]interface{}{
			"module": "qrc20", "action": "total_supply",
			"token": token.String(), "total_supply": supply.String(),
			"name": name, "symbol": symbol, "decimals": decimals, "exists": exists,
		}, nil

	case "allowance":
		owner, err := qcrypto.AddressFromString(p.Owner)
		if err != nil {
			return nil, fmt.Errorf("qrc20 allowance: owner: %w", err)
		}
		spender, err := qcrypto.AddressFromString(p.Spender)
		if err != nil {
			return nil, fmt.Errorf("qrc20 allowance: spender: %w", err)
		}
		amt, err := tok.Allowance(owner, spender)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{
			"module": "qrc20", "action": "allowance",
			"token": token.String(), "owner": owner.String(), "spender": spender.String(),
			"allowance": amt.String(), "exists": exists,
		}, nil

	case "token_info":
		name, _ := tok.Name()
		symbol, _ := tok.Symbol()
		decimals, _ := tok.Decimals()
		supply, _ := tok.TotalSupply()
		maxSupply, _ := tok.MaxSupply()
		owner, _ := tok.Owner()
		return map[string]interface{}{
			"module": "qrc20", "action": "token_info",
			"token": token.String(), "name": name, "symbol": symbol, "decimals": decimals,
			"total_supply": supply.String(), "max_supply": maxSupply.String(),
			"owner": owner.String(), "exists": exists,
		}, nil

	case "deploy", "mint", "transfer", "approve", "burn":
		return nil, fmt.Errorf("qrc20: %q is a state-changing action — submit it as a signed TxCall to /tx/submit, not /call", action)
	default:
		return nil, fmt.Errorf("qrc20: unknown read action %q", action)
	}
}

func dispatchDEXRead(stateDB *state.StateDB, action string, raw json.RawMessage) (interface{}, error) {
	switch action {
	case "reserves", "pool_info":
		var p dexReadParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("dex read: bad params: %w", err)
		}
		pool, err := resolvePoolAddress(p)
		if err != nil {
			return nil, err
		}
		exists := false
		if acc, aerr := stateDB.GetAccount(pool); aerr == nil {
			exists = acc.IsContract
		}
		info, err := NewLiquidityPool(stateDB, pool).Info()
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{
			"module": "dex", "action": action,
			"pool_info": info, "exists": exists,
		}, nil

	case "deploy_pool", "add_liquidity", "swap":
		return nil, fmt.Errorf("dex: %q is a state-changing action — submit it as a signed TxCall to /tx/submit, not /call", action)
	default:
		return nil, fmt.Errorf("dex: unknown read action %q", action)
	}
}

// resolvePoolAddress accepts either an explicit pool address or a token pair
// (from which the deterministic pool address is derived).
func resolvePoolAddress(p dexReadParams) (qcrypto.Address, error) {
	if p.Pool != "" {
		pool, err := qcrypto.AddressFromString(p.Pool)
		if err != nil {
			return qcrypto.Address{}, fmt.Errorf("dex read: pool: %w", err)
		}
		return pool, nil
	}
	if p.Token0 != "" && p.Token1 != "" {
		t0, err := qcrypto.AddressFromString(p.Token0)
		if err != nil {
			return qcrypto.Address{}, fmt.Errorf("dex read: token0: %w", err)
		}
		t1, err := qcrypto.AddressFromString(p.Token1)
		if err != nil {
			return qcrypto.Address{}, fmt.Errorf("dex read: token1: %w", err)
		}
		return PoolAddress(t0, t1), nil
	}
	return qcrypto.Address{}, fmt.Errorf("dex read: provide either \"pool\" or (\"token0\",\"token1\")")
}

// PreflightModuleCall validates a ModuleCall envelope (module + action are known)
// WITHOUT executing it — used by /estimate to flag a malformed module call
// without mutating state.
func PreflightModuleCall(data []byte) (ModuleCall, error) {
	var mc ModuleCall
	if err := json.Unmarshal(data, &mc); err != nil {
		return mc, fmt.Errorf("module call: invalid JSON envelope: %w", err)
	}
	known := map[string]map[string]bool{
		"qrc20": {
			"deploy": true, "mint": true, "transfer": true, "approve": true, "burn": true,
			"balance_of": true, "total_supply": true, "allowance": true, "token_info": true,
		},
		"dex": {
			"deploy_pool": true, "add_liquidity": true, "swap": true,
			"reserves": true, "pool_info": true,
		},
	}
	actions, ok := known[mc.Module]
	if !ok {
		return mc, fmt.Errorf("module call: unknown module %q", mc.Module)
	}
	if !actions[mc.Action] {
		return mc, fmt.Errorf("%s: unknown action %q", mc.Module, mc.Action)
	}
	return mc, nil
}
