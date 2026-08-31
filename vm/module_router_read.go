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
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
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
	"qrc1155": {
		// qrc1155_v1: the whole module is routed at once, behind its owner slot.
		Writes: map[string]string{
			"deploy": forks.QRC1155V1, "mint": forks.QRC1155V1, "mint_batch": forks.QRC1155V1,
			"transfer": forks.QRC1155V1, "batch_transfer": forks.QRC1155V1,
			"set_approval_for_all": forks.QRC1155V1, "burn": forks.QRC1155V1,
			"burn_batch": forks.QRC1155V1, "transfer_ownership": forks.QRC1155V1,
		},
		Reads: map[string]bool{
			"balance_of": true, "balance_of_batch": true, "is_approved_for_all": true,
			"uri": true, "total_supply": true, "token_info": true,
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
	"exchange": {
		// exchange_v1: the settlement module, routed whole.
		Writes: map[string]string{
			"deploy": forks.ExchangeV1, "create_condition": forks.ExchangeV1,
			"split": forks.ExchangeV1, "merge": forks.ExchangeV1, "match": forks.ExchangeV1,
			"cancel_order": forks.ExchangeV1, "increment_nonce": forks.ExchangeV1,
			"resolve": forks.ExchangeV1, "redeem": forks.ExchangeV1, "redeem_for": forks.ExchangeV1,
			"pause": forks.ExchangeV1, "unpause": forks.ExchangeV1,
			"set_redeem_fee_bps": forks.ExchangeV1, "set_fee_recipient": forks.ExchangeV1,
			"set_operator": forks.ExchangeV1, "transfer_ownership": forks.ExchangeV1,
		},
		Reads: map[string]bool{
			"exchange_info": true, "condition_info": true, "order_status": true,
			"order_remaining": true, "order_hash": true, "nonce": true, "is_operator": true,
			"position": true, "position_id": true, "simulate_match": true,
		},
	},
	"bridge": {
		// bridge_v1: the legacy-mainnet migration bridge, routed whole.
		Writes: map[string]string{
			"deploy": forks.BridgeV1, "claim": forks.BridgeV1,
			"set_operator": forks.BridgeV1, "set_threshold": forks.BridgeV1,
			"pause": forks.BridgeV1, "unpause": forks.BridgeV1,
			"transfer_ownership": forks.BridgeV1,
		},
		Reads: map[string]bool{
			"bridge_info": true, "claim_status": true,
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

// qrc1155ReadParams is the parameter shape for qrc1155 reads. Ids are
// decimal strings through parseUint256; balance_of_batch pairs owners[i]
// with ids[i] and is capped at MaxQRC1155Batch.
type qrc1155ReadParams struct {
	Collection string   `json:"collection"`         // contract SQ: address (required)
	Owner      string   `json:"owner,omitempty"`    // balance_of / is_approved_for_all
	Operator   string   `json:"operator,omitempty"` // is_approved_for_all
	ID         string   `json:"id,omitempty"`       // balance_of / uri / total_supply
	Owners     []string `json:"owners,omitempty"`   // balance_of_batch
	IDs        []string `json:"ids,omitempty"`      // balance_of_batch
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
// ReadContext). The only action that consumes it is exchange.simulate_match,
// which dry-runs a settlement against the head time and reverts everything.
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
	case "qrc1155":
		return dispatchQRC1155Read(stateDB, mc.Action, mc.Params)
	case "dex":
		return dispatchDEXRead(stateDB, mc.Action, mc.Params)
	case "exchange":
		return dispatchExchangeRead(stateDB, rctx, mc.Action, mc.Params)
	case "bridge":
		return dispatchBridgeRead(stateDB, mc.Action, mc.Params)
	default:
		return nil, fmt.Errorf("module call: unknown module %q", mc.Module)
	}
}

// bridgeReadParams is the parameter shape for bridge reads.
type bridgeReadParams struct {
	Bridge string `json:"bridge"`       // instance SQ: address (required)
	ID     uint64 `json:"id,omitempty"` // claim_status: the EVM burn id
}

func dispatchBridgeRead(stateDB *state.StateDB, action string, raw json.RawMessage) (interface{}, error) {
	var p bridgeReadParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("bridge read: bad params: %w", err)
	}
	addr, err := qcrypto.AddressFromString(p.Bridge)
	if err != nil {
		return nil, fmt.Errorf("bridge read: bridge: %w", err)
	}
	b := NewBridge(stateDB, addr)

	switch action {
	case "bridge_info":
		if err := b.requireDeployed("bridge bridge_info: "); err != nil {
			return nil, err
		}
		info, err := b.Info()
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{
			"module": "bridge", "action": "bridge_info", "info": info,
		}, nil

	case "claim_status":
		if err := b.requireDeployed("bridge claim_status: "); err != nil {
			return nil, err
		}
		if p.ID == 0 {
			return nil, fmt.Errorf("bridge claim_status: id must be positive (burn ids start at 1)")
		}
		claimed, height, err := b.ClaimedAt(p.ID)
		if err != nil {
			return nil, err
		}
		out := map[string]interface{}{
			"module": "bridge", "action": "claim_status",
			"bridge": addr.String(), "id": p.ID, "claimed": claimed,
		}
		if claimed {
			out["height"] = height
		}
		return out, nil

	default:
		return nil, fmt.Errorf("bridge: unknown read action %q", action)
	}
}

func dispatchQRC1155Read(stateDB *state.StateDB, action string, raw json.RawMessage) (interface{}, error) {
	var p qrc1155ReadParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("qrc1155 read: bad params: %w", err)
	}
	collection, err := qcrypto.AddressFromString(p.Collection)
	if err != nil {
		return nil, fmt.Errorf("qrc1155 read: collection: %w", err)
	}
	tok := NewQRC1155Token(stateDB, collection)
	exists := false
	if acc, aerr := stateDB.GetAccount(collection); aerr == nil {
		exists = acc.IsContract
	}

	switch action {
	case "balance_of":
		owner, err := qcrypto.AddressFromString(p.Owner)
		if err != nil {
			return nil, fmt.Errorf("qrc1155 balance_of: owner: %w", err)
		}
		id, err := parseUint256(p.ID)
		if err != nil {
			return nil, fmt.Errorf("qrc1155 balance_of: id: %w", err)
		}
		bal, err := tok.BalanceOf(owner, id)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{
			"module": "qrc1155", "action": "balance_of",
			"collection": collection.String(), "owner": owner.String(), "id": id.String(),
			"balance": bal.String(), "exists": exists,
		}, nil

	case "balance_of_batch":
		if len(p.Owners) != len(p.IDs) {
			return nil, fmt.Errorf("qrc1155 balance_of_batch: owners and ids length mismatch")
		}
		if len(p.Owners) > MaxQRC1155Batch {
			return nil, fmt.Errorf("qrc1155 balance_of_batch: %d entries exceeds the batch cap of %d", len(p.Owners), MaxQRC1155Batch)
		}
		owners := make([]qcrypto.Address, len(p.Owners))
		for i, s := range p.Owners {
			if owners[i], err = qcrypto.AddressFromString(s); err != nil {
				return nil, fmt.Errorf("qrc1155 balance_of_batch: owners[%d]: %w", i, err)
			}
		}
		ids, err := parseUint256List("qrc1155 balance_of_batch: ids", p.IDs)
		if err != nil {
			return nil, err
		}
		bals, err := tok.BalanceOfBatch(owners, ids)
		if err != nil {
			return nil, err
		}
		out := make([]string, len(bals))
		for i, b := range bals {
			out[i] = b.String()
		}
		return map[string]interface{}{
			"module": "qrc1155", "action": "balance_of_batch",
			"collection": collection.String(), "owners": p.Owners, "ids": p.IDs,
			"balances": out, "exists": exists,
		}, nil

	case "is_approved_for_all":
		owner, err := qcrypto.AddressFromString(p.Owner)
		if err != nil {
			return nil, fmt.Errorf("qrc1155 is_approved_for_all: owner: %w", err)
		}
		operator, err := qcrypto.AddressFromString(p.Operator)
		if err != nil {
			return nil, fmt.Errorf("qrc1155 is_approved_for_all: operator: %w", err)
		}
		approved, err := tok.IsApprovedForAll(owner, operator)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{
			"module": "qrc1155", "action": "is_approved_for_all",
			"collection": collection.String(), "owner": owner.String(), "operator": operator.String(),
			"approved": approved, "exists": exists,
		}, nil

	case "uri":
		id, err := parseUint256(p.ID)
		if err != nil {
			return nil, fmt.Errorf("qrc1155 uri: id: %w", err)
		}
		uri, err := tok.URI(id)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{
			"module": "qrc1155", "action": "uri",
			"collection": collection.String(), "id": id.String(), "uri": uri, "exists": exists,
		}, nil

	case "total_supply":
		id, err := parseUint256(p.ID)
		if err != nil {
			return nil, fmt.Errorf("qrc1155 total_supply: id: %w", err)
		}
		supply, err := tok.TotalSupply(id)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{
			"module": "qrc1155", "action": "total_supply",
			"collection": collection.String(), "id": id.String(),
			"total_supply": supply.String(), "exists": exists,
		}, nil

	case "token_info":
		name, _ := tok.Name()
		prefix, _ := tok.URIPrefix()
		owner, _ := tok.Owner()
		managed, _ := tok.IsModuleManaged()
		return map[string]interface{}{
			"module": "qrc1155", "action": "token_info",
			"collection": collection.String(), "name": name, "uri_prefix": prefix,
			"owner": owner.String(), "module_managed": managed, "exists": exists,
		}, nil

	default:
		// Unreachable: DispatchModuleReadCall admits only the read set.
		return nil, fmt.Errorf("qrc1155: unknown read action %q", action)
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
// an action outside the module's WRITE set, and — given a height — an action
// whose fork is not active at that height. /tx/submit runs it against
// executor.Forks() and head+1 so a tx that could only ever be dropped is
// refused before it enters the mempool; /estimate runs it to say "not active"
// without mutating state.
//
// The fork gate mirrors requireFork exactly, nil schedule included: consensus
// reads a nil schedule as "never", so a schedule-less executor (a chain
// upgraded in place, a peer-bootstrapped follower before its genesis arrives)
// drops every gated write, and admitting one here would pin the sender's
// nonce in the pool. Height 0 is the one escape and it is not a consensus
// height: it means "no executor", the executor-less /estimate path, where
// there is no head to judge against and nothing to admit.
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
	if gate != "" && height != 0 && !schedule.Active(gate, height) {
		return mc, fmt.Errorf("%s: %q: %w: %s is not scheduled at height %d", mc.Module, mc.Action, ErrForkInactive, gate, height)
	}
	return mc, nil
}

// PreflightExchangeMatch is the local admission check /tx/submit runs after
// PreflightModuleCall on an exchange.match TxCall: the sender must be an
// active operator of the instance it names. A match from anyone else can only
// ever be a failed receipt (the operator gate is the module's first check), so
// refusing it at the door keeps mempool and block space for settlements that
// can settle. Every other envelope passes through untouched. Local and
// non-consensus: the executor re-judges the call.
func PreflightExchangeMatch(stateDB *state.StateDB, mc ModuleCall, sender qcrypto.Address) error {
	if mc.Module != "exchange" || mc.Action != "match" {
		return nil
	}
	var p struct {
		Exchange string `json:"exchange"`
	}
	if err := json.Unmarshal(mc.Params, &p); err != nil {
		return fmt.Errorf("exchange match: bad params: %w", err)
	}
	x, err := exchangeAt(stateDB, "match", p.Exchange)
	if err != nil {
		return err
	}
	return x.requireOperator("exchange match: ", sender)
}

// ── Exchange reads ──────────────────────────────────────────────────────────

// exchangeReadParams is the parameter shape of the exchange reads. Every read
// but position_id names the instance in `exchange`; the remaining fields
// belong to individual actions. simulate_match additionally decodes
// exchangeSimulateParams from the same envelope.
type exchangeReadParams struct {
	Exchange  string             `json:"exchange,omitempty"`  // instance SQ: address (required except for position_id)
	Condition uint64             `json:"condition,omitempty"` // condition_info / position / position_id
	Outcome   uint8              `json:"outcome,omitempty"`   // position_id
	Holder    string             `json:"holder,omitempty"`    // position
	Maker     string             `json:"maker,omitempty"`     // nonce
	Address   string             `json:"address,omitempty"`   // is_operator
	Hash      string             `json:"hash,omitempty"`      // order_status: 64 lowercase hex chars
	Order     *exchangeOrderWire `json:"order,omitempty"`     // order_remaining / order_hash
}

// exchangeSimulateParams is the simulate_match shape: exactly what match takes
// plus `caller`, the operator that would sign the TxCall — the operator gate
// is part of what is being simulated.
type exchangeSimulateParams struct {
	exchangeMatchParams
	Caller string `json:"caller"`
}

// dispatchExchangeRead serves the exchange read set. Reads are never
// fork-gated. simulate_match is the one read that runs module code: it
// dry-runs Match against the head time under a snapshot that is always
// reverted, so it reports exactly the error a real match would produce at
// this head and leaves nothing behind — on a /call sandbox or a bare StateDB.
func dispatchExchangeRead(stateDB *state.StateDB, rctx ReadContext, action string, raw json.RawMessage) (interface{}, error) {
	var p exchangeReadParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("exchange read: bad params: %w", err)
	}
	out := map[string]interface{}{"module": "exchange", "action": action}
	if action == "position_id" {
		// Pure: the id layout needs no instance.
		out["condition"] = p.Condition
		out["outcome"] = p.Outcome
		out["id"] = ExchangePositionID(p.Condition, p.Outcome).String()
		return out, nil
	}
	exchange, err := qcrypto.AddressFromString(p.Exchange)
	if err != nil {
		return nil, fmt.Errorf("exchange %s: exchange: %w", action, err)
	}
	exists := false
	if acc, aerr := stateDB.GetAccount(exchange); aerr == nil {
		exists = acc.IsContract
	}
	x := NewExchange(stateDB, exchange)
	out["exchange"] = exchange.String()
	out["exists"] = exists

	switch action {
	case "exchange_info":
		info, err := x.Info()
		if err != nil {
			return nil, err
		}
		out["owner"] = info.Owner
		out["operators"] = info.Operators
		out["paused"] = info.Paused
		out["redeem_fee_bps"] = info.RedeemFeeBps
		out["fee_recipient"] = info.FeeRecipient
		out["condition_count"] = info.ConditionCount
		return out, nil

	case "condition_info":
		c, err := x.Condition(p.Condition)
		if err != nil {
			return nil, fmt.Errorf("exchange condition_info: %w", err)
		}
		ids := make([]string, c.OutcomeCount)
		for o := uint8(0); o < c.OutcomeCount; o++ {
			ids[o] = ExchangePositionID(c.ID, o).String()
		}
		out["condition"] = c.ID
		out["collateral"] = c.Collateral.String()
		out["resolver"] = c.Resolver.String()
		out["end_time"] = c.EndTime
		out["outcome_count"] = c.OutcomeCount
		out["question_hash"] = hex.EncodeToString(c.QuestionHash[:])
		out["resolved"] = c.Resolved
		out["winning_outcome"] = c.WinningOutcome
		out["locked"] = c.Locked.String()
		out["position_ids"] = ids
		return out, nil

	case "order_status":
		h, err := parseHex32(p.Hash)
		if err != nil {
			return nil, fmt.Errorf("exchange order_status: hash: %w", err)
		}
		filled, err := x.OrderFilled(h)
		if err != nil {
			return nil, err
		}
		cancelled, err := x.OrderCancelled(h)
		if err != nil {
			return nil, err
		}
		out["hash"] = hex.EncodeToString(h[:])
		out["filled"] = filled.String()
		out["cancelled"] = cancelled
		return out, nil

	case "order_remaining":
		if p.Order == nil {
			return nil, fmt.Errorf("exchange order_remaining: order is required")
		}
		o, err := x.parseOrder("exchange order_remaining: ", *p.Order)
		if err != nil {
			return nil, err
		}
		filled, err := x.OrderFilled(o.hash)
		if err != nil {
			return nil, err
		}
		cancelled, err := x.OrderCancelled(o.hash)
		if err != nil {
			return nil, err
		}
		remaining := new(big.Int).Sub(o.ShareAmount, filled)
		if cancelled || remaining.Sign() < 0 {
			remaining = new(big.Int)
		}
		out["hash"] = hex.EncodeToString(o.hash[:])
		out["share_amount"] = o.ShareAmount.String()
		out["filled"] = filled.String()
		out["cancelled"] = cancelled
		out["remaining"] = remaining.String()
		return out, nil

	case "order_hash":
		if p.Order == nil {
			return nil, fmt.Errorf("exchange order_hash: order is required")
		}
		h, err := x.OrderHash(*p.Order)
		if err != nil {
			return nil, fmt.Errorf("exchange order_hash: %w", err)
		}
		out["hash"] = hex.EncodeToString(h[:])
		return out, nil

	case "nonce":
		maker, err := qcrypto.AddressFromString(p.Maker)
		if err != nil {
			return nil, fmt.Errorf("exchange nonce: maker: %w", err)
		}
		n, err := x.MakerNonce(maker)
		if err != nil {
			return nil, err
		}
		out["maker"] = maker.String()
		out["nonce"] = n
		return out, nil

	case "is_operator":
		a, err := qcrypto.AddressFromString(p.Address)
		if err != nil {
			return nil, fmt.Errorf("exchange is_operator: address: %w", err)
		}
		ok, err := x.IsOperator(a)
		if err != nil {
			return nil, err
		}
		out["address"] = a.String()
		out["is_operator"] = ok
		return out, nil

	case "position":
		holder, err := qcrypto.AddressFromString(p.Holder)
		if err != nil {
			return nil, fmt.Errorf("exchange position: holder: %w", err)
		}
		c, err := x.Condition(p.Condition)
		if err != nil {
			return nil, fmt.Errorf("exchange position: %w", err)
		}
		pos := x.positions()
		ids := make([]string, c.OutcomeCount)
		balances := make([]string, c.OutcomeCount)
		for o := uint8(0); o < c.OutcomeCount; o++ {
			id := ExchangePositionID(c.ID, o)
			bal, err := pos.BalanceOf(holder, id)
			if err != nil {
				return nil, err
			}
			ids[o] = id.String()
			balances[o] = bal.String()
		}
		out["condition"] = c.ID
		out["holder"] = holder.String()
		out["position_ids"] = ids
		out["balances"] = balances
		return out, nil

	case "position_id":
		// Handled above, before the instance is resolved.
		return out, nil

	case "simulate_match":
		var sp exchangeSimulateParams
		if err := json.Unmarshal(raw, &sp); err != nil {
			return nil, fmt.Errorf("exchange simulate_match: bad params: %w", err)
		}
		caller, err := qcrypto.AddressFromString(sp.Caller)
		if err != nil {
			return nil, fmt.Errorf("exchange simulate_match: caller: %w", err)
		}
		rc := &types.Receipt{}
		snap := stateDB.Snapshot()
		merr := x.Match(ModuleContext{Caller: caller, Height: rctx.Height, Time: rctx.Time}, sp.exchangeMatchParams, rc)
		if err := stateDB.RevertToSnapshot(snap); err != nil {
			return nil, fmt.Errorf("exchange simulate_match: %w", err)
		}
		out["caller"] = caller.String()
		out["head_height"] = rctx.Height
		out["head_time"] = rctx.Time
		out["ok"] = merr == nil
		out["error"] = ""
		if merr != nil {
			out["error"] = merr.Error()
		}
		out["logs"] = len(rc.Logs)
		return out, nil

	default:
		// Unreachable: DispatchModuleReadCall admits only the read set.
		return nil, fmt.Errorf("exchange: unknown read action %q", action)
	}
}
