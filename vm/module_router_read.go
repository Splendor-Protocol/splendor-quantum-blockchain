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
//   - A write action carries no signature on /call, so serving one there would
//     run an owner-only action for an anonymous caller. Module writes stage on
//     the StateDB pending layer (journaled, snapshot-revertable) and /call runs
//     on a Simulate() sandbox, so nothing would reach disk — but the action
//     would still report success. The read dispatch therefore serves only the
//     read set of KnownModuleActions and rejects every write action outright.
package vm

import (
	"encoding/json"
	"fmt"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
)

// ModuleActions is one module's action surface, split by what a call is
// allowed to do. Writes are the actions DispatchModuleCall executes inside a
// signed TxCall; Reads are the actions DispatchModuleReadCall serves on /call.
// The two never overlap: an action is either a state transition or a view.
type ModuleActions struct {
	// Writes maps a write action to the fork that gates it. "" means the
	// action has been routed since the module shipped and is never gated.
	Writes map[string]string
	// Reads is the set of value-returning view actions.
	Reads map[string]bool
}

// KnownModuleActions is the single list of every module action this build
// dispatches, keyed by module name. It is the authority for admission: the
// /tx/submit and /estimate preflight accepts only a write action, and the read
// dispatch serves only a read action. Keeping the two sets apart matters
// because the write dispatch treats an unknown action as HARD (see errors.go):
// a read action inside a signed TxCall would pass a mixed allowlist, hit that
// default, and jam the sender's account at that nonce forever.
//
// A coverage test pins every `case` in the dispatch switches to this table,
// so adding an action to a module without listing it here fails the build's
// tests rather than silently escaping admission.
var KnownModuleActions = map[string]ModuleActions{
	"qrc20": {
		Writes: map[string]string{
			"deploy": "", "mint": "", "transfer": "", "approve": "", "burn": "",
			// qrc20_v2: the permit/allowance surface and the owner controls.
			"permit": forks.QRC20V2, "transfer_from": forks.QRC20V2,
			"increase_allowance": forks.QRC20V2, "decrease_allowance": forks.QRC20V2,
			"burn_from": forks.QRC20V2, "transfer_ownership": forks.QRC20V2,
			"pause": forks.QRC20V2, "unpause": forks.QRC20V2,
		},
		Reads: map[string]bool{
			"balance_of": true, "total_supply": true, "allowance": true, "token_info": true,
			"permit_nonce": true, "paused": true, "owner": true,
		},
	},
	"dex": {
		Writes: map[string]string{
			"deploy_pool": "", "add_liquidity": "", "swap": "",
		},
		Reads: map[string]bool{
			"reserves": true, "pool_info": true,
		},
	},
}

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

// ReadContext is the advisory view of the chain head a read action may use to
// label its answer ("trading is open", "this permit has expired"). It is the
// read-side counterpart of ModuleContext but carries no authority: a read
// never gates anything on it, and it is zero whenever the caller has no
// executor to ask (a bare StateDB in a test, a node whose chain is empty).
// Every read must therefore be correct with Height == 0 and Time == 0.
type ReadContext struct {
	Height uint64
	Time   int64
}

// DispatchModuleReadCall decodes a ModuleCall envelope and executes a read-only
// (value-returning) module action against committed state. It never mutates
// state: only the read set of KnownModuleActions is served; a write action is
// rejected with an error before any module code runs. The returned value is a
// JSON-serialisable map.
//
// rctx describes the chain head the read runs against; it is advisory (see
// ReadContext) and no action served today consumes it.
func DispatchModuleReadCall(stateDB *state.StateDB, rctx ReadContext, data []byte) (interface{}, error) {
	var mc ModuleCall
	if err := json.Unmarshal(data, &mc); err != nil {
		return nil, fmt.Errorf("module call: invalid JSON envelope: %w", err)
	}
	acts, ok := KnownModuleActions[mc.Module]
	if !ok {
		return nil, fmt.Errorf("module call: unknown module %q", mc.Module)
	}
	if _, isWrite := acts.Writes[mc.Action]; isWrite {
		return nil, fmt.Errorf("%s: %q is a state-changing action — submit it as a signed TxCall to /tx/submit, not /call", mc.Module, mc.Action)
	}
	if !acts.Reads[mc.Action] {
		return nil, fmt.Errorf("%s: unknown read action %q", mc.Module, mc.Action)
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

	case "permit_nonce":
		// The nonce a signer must put in its next permit digest for this token.
		owner, err := qcrypto.AddressFromString(p.Owner)
		if err != nil {
			return nil, fmt.Errorf("qrc20 permit_nonce: owner: %w", err)
		}
		nonce, err := tok.PermitNonce(owner)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{
			"module": "qrc20", "action": "permit_nonce",
			"token": token.String(), "owner": owner.String(),
			"permit_nonce": nonce.String(), "exists": exists,
		}, nil

	case "paused":
		paused, err := tok.IsPaused()
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{
			"module": "qrc20", "action": "paused",
			"token": token.String(), "paused": paused, "exists": exists,
		}, nil

	case "owner":
		owner, err := tok.Owner()
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{
			"module": "qrc20", "action": "owner",
			"token": token.String(), "owner": owner.String(), "exists": exists,
		}, nil

	default:
		// Unreachable: DispatchModuleReadCall admits only the read set.
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

	default:
		// Unreachable: DispatchModuleReadCall admits only the read set.
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

// PreflightModuleCall validates a ModuleCall envelope destined for a signed
// TxCall WITHOUT executing it. It is the admission-time mirror of the HARD
// classes in DispatchModuleCall: an unparseable envelope, an unknown module,
// an action outside the module's WRITE set, and — when a schedule and height
// are given — an action whose fork is not active at that height. /tx/submit
// runs it against executor.Forks() and head+1 so a tx that could only ever be
// dropped is refused before it enters the mempool; /estimate runs it to say
// "not active" without mutating state. Pass a nil schedule (or height 0) to
// skip fork gating.
//
// A read action is refused here even though it parses: the write dispatch
// treats it as an unknown action, which is HARD, and a sender whose tx can
// never apply is jammed at that nonce.
func PreflightModuleCall(data []byte, schedule forks.Schedule, height uint64) (ModuleCall, error) {
	var mc ModuleCall
	if err := json.Unmarshal(data, &mc); err != nil {
		return mc, fmt.Errorf("module call: invalid JSON envelope: %w", err)
	}
	acts, ok := KnownModuleActions[mc.Module]
	if !ok {
		return mc, fmt.Errorf("module call: unknown module %q", mc.Module)
	}
	if acts.Reads[mc.Action] {
		return mc, fmt.Errorf("%s: %q is a read action — use /call", mc.Module, mc.Action)
	}
	gate, isWrite := acts.Writes[mc.Action]
	if !isWrite {
		return mc, fmt.Errorf("%s: unknown action %q", mc.Module, mc.Action)
	}
	if gate != "" && schedule != nil && height != 0 && !schedule.Active(gate, height) {
		return mc, fmt.Errorf("%s: %q: %w: %s is not scheduled at height %d", mc.Module, mc.Action, ErrForkInactive, gate, height)
	}
	return mc, nil
}
