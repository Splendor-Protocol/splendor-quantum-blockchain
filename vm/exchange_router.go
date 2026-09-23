// Package vm — exchange (exchange_v1) router surface: the write dispatch
// DispatchModuleCall hands `module: "exchange"` to, the read dispatch
// DispatchModuleReadCall hands it to, and the /tx/submit admission check for
// exchange.match. The module itself is exchange.go; the action tables in
// module_router_read.go (KnownModuleActions) are the admission authority and
// vm/module_actions_test.go pins every `case` below to them.
//
// Blockpeak settles its prediction markets here, on the chain's own module:
// BPC collateral is a routed QRC-20, positions a module-managed QRC-1155.
// Nothing in this file touches a pool, a vault or a loan — the DEX, lending
// and the rest of the removed DeFi surface stay unrouted.
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

// ── Exchange ────────────────────────────────────────────────────────────────

// exchangeActionParams is the shared wire shape of the single-purpose exchange
// writes; each action reads only the fields named for it. The match, cancel,
// nonce, deploy and condition actions have their own shapes in exchange.go.
type exchangeActionParams struct {
	Exchange     string `json:"exchange"`
	Condition    uint64 `json:"condition,omitempty"`      // split / merge / resolve / redeem / redeem_for
	Amount       string `json:"amount,omitempty"`         // split / merge: decimal string, smallest unit
	Outcome      uint8  `json:"outcome,omitempty"`        // resolve
	Holder       string `json:"holder,omitempty"`         // redeem_for
	RedeemFeeBps uint64 `json:"redeem_fee_bps,omitempty"` // set_redeem_fee_bps
	FeeRecipient string `json:"fee_recipient,omitempty"`  // set_fee_recipient
	Operator     string `json:"operator,omitempty"`       // set_operator
	Active       *bool  `json:"active,omitempty"`         // set_operator (required: true grants, false revokes)
	NewOwner     string `json:"new_owner,omitempty"`      // transfer_ownership
}

// parseExchangeParams is the first step of every exchange write: the fork
// gate (HARD before the height), then the params (SOFT: the action's own
// "bad params" complaint).
func parseExchangeParams(ctx ModuleContext, action string, raw json.RawMessage, p any) error {
	if err := requireFork(ctx, forks.ExchangeV1); err != nil {
		return err
	}
	if err := json.Unmarshal(raw, p); err != nil {
		return fmt.Errorf("exchange %s: bad params: %w", action, err)
	}
	return nil
}

// exchangeAt resolves the instance a non-deploy action names. Whether anything
// is deployed there is the action's own check (requireDeployed).
func exchangeAt(stateDB *state.StateDB, action, s string) (*Exchange, error) {
	a, err := qcrypto.AddressFromString(s)
	if err != nil {
		return nil, fmt.Errorf("exchange %s: exchange: %w", action, err)
	}
	return NewExchange(stateDB, a), nil
}

// exchangeAddressParam decodes one address field of an exchange action.
func exchangeAddressParam(action, field, s string) (qcrypto.Address, error) {
	a, err := qcrypto.AddressFromString(s)
	if err != nil {
		return qcrypto.Address{}, fmt.Errorf("exchange %s: %s: %w", action, field, err)
	}
	return a, nil
}

// dispatchExchange routes the settlement module's writes (exchange_v1). Every
// action is gated first; everything after the gate is SOFT and prefixed
// "exchange <action>: ". The module methods snapshot and revert on their own,
// so a failed action leaves no residue whether or not the executor wraps the
// call in a module_revert_v1 snapshot.
func dispatchExchange(stateDB *state.StateDB, ctx ModuleContext, action string, raw json.RawMessage, receipt *types.Receipt) error {
	switch action {
	case "deploy":
		var p exchangeDeployParams
		if err := parseExchangeParams(ctx, action, raw, &p); err != nil {
			return err
		}
		if err := capString("exchange deploy", "name", p.Name, MaxModuleNameBytes); err != nil {
			return err
		}
		if err := capString("exchange deploy", "uri_prefix", p.URIPrefix, MaxModuleURIBytes); err != nil {
			return err
		}
		addr, err := DeployExchange(stateDB, ctx, p)
		if err != nil {
			return err
		}
		receipt.ContractAddress = &addr
		return nil

	case "create_condition":
		var p exchangeConditionParams
		if err := parseExchangeParams(ctx, action, raw, &p); err != nil {
			return err
		}
		x, err := exchangeAt(stateDB, action, p.Exchange)
		if err != nil {
			return err
		}
		_, err = x.CreateCondition(ctx, p, receipt)
		return err

	case "split":
		var p exchangeActionParams
		if err := parseExchangeParams(ctx, action, raw, &p); err != nil {
			return err
		}
		x, err := exchangeAt(stateDB, action, p.Exchange)
		if err != nil {
			return err
		}
		amount, err := parseUint256(p.Amount)
		if err != nil {
			return fmt.Errorf("exchange split: amount: %w", err)
		}
		return x.Split(ctx, p.Condition, amount, receipt)

	case "merge":
		var p exchangeActionParams
		if err := parseExchangeParams(ctx, action, raw, &p); err != nil {
			return err
		}
		x, err := exchangeAt(stateDB, action, p.Exchange)
		if err != nil {
			return err
		}
		amount, err := parseUint256(p.Amount)
		if err != nil {
			return fmt.Errorf("exchange merge: amount: %w", err)
		}
		return x.Merge(ctx, p.Condition, amount, receipt)

	case "match":
		var p exchangeMatchParams
		if err := parseExchangeParams(ctx, action, raw, &p); err != nil {
			return err
		}
		x, err := exchangeAt(stateDB, action, p.Exchange)
		if err != nil {
			return err
		}
		return x.Match(ctx, p, receipt)

	case "cancel_order":
		var p exchangeCancelParams
		if err := parseExchangeParams(ctx, action, raw, &p); err != nil {
			return err
		}
		x, err := exchangeAt(stateDB, action, p.Exchange)
		if err != nil {
			return err
		}
		return x.CancelOrder(ctx, p, receipt)

	case "increment_nonce":
		var p exchangeNonceParams
		if err := parseExchangeParams(ctx, action, raw, &p); err != nil {
			return err
		}
		x, err := exchangeAt(stateDB, action, p.Exchange)
		if err != nil {
			return err
		}
		return x.IncrementNonce(ctx, p, receipt)

	case "resolve":
		var p exchangeActionParams
		if err := parseExchangeParams(ctx, action, raw, &p); err != nil {
			return err
		}
		x, err := exchangeAt(stateDB, action, p.Exchange)
		if err != nil {
			return err
		}
		return x.Resolve(ctx, p.Condition, p.Outcome, receipt)

	case "redeem":
		var p exchangeActionParams
		if err := parseExchangeParams(ctx, action, raw, &p); err != nil {
			return err
		}
		x, err := exchangeAt(stateDB, action, p.Exchange)
		if err != nil {
			return err
		}
		return x.Redeem(ctx, p.Condition, ctx.Caller, receipt)

	case "redeem_for":
		var p exchangeActionParams
		if err := parseExchangeParams(ctx, action, raw, &p); err != nil {
			return err
		}
		x, err := exchangeAt(stateDB, action, p.Exchange)
		if err != nil {
			return err
		}
		holder, err := exchangeAddressParam(action, "holder", p.Holder)
		if err != nil {
			return err
		}
		return x.Redeem(ctx, p.Condition, holder, receipt)

	case "pause":
		var p exchangeActionParams
		if err := parseExchangeParams(ctx, action, raw, &p); err != nil {
			return err
		}
		x, err := exchangeAt(stateDB, action, p.Exchange)
		if err != nil {
			return err
		}
		return x.Pause(ctx.Caller, receipt)

	case "unpause":
		var p exchangeActionParams
		if err := parseExchangeParams(ctx, action, raw, &p); err != nil {
			return err
		}
		x, err := exchangeAt(stateDB, action, p.Exchange)
		if err != nil {
			return err
		}
		return x.Unpause(ctx.Caller, receipt)

	case "set_redeem_fee_bps":
		var p exchangeActionParams
		if err := parseExchangeParams(ctx, action, raw, &p); err != nil {
			return err
		}
		x, err := exchangeAt(stateDB, action, p.Exchange)
		if err != nil {
			return err
		}
		return x.SetRedeemFeeBps(ctx.Caller, p.RedeemFeeBps, receipt)

	case "set_fee_recipient":
		var p exchangeActionParams
		if err := parseExchangeParams(ctx, action, raw, &p); err != nil {
			return err
		}
		x, err := exchangeAt(stateDB, action, p.Exchange)
		if err != nil {
			return err
		}
		recipient, err := exchangeAddressParam(action, "fee_recipient", p.FeeRecipient)
		if err != nil {
			return err
		}
		return x.SetFeeRecipient(ctx.Caller, recipient, receipt)

	case "set_operator":
		var p exchangeActionParams
		if err := parseExchangeParams(ctx, action, raw, &p); err != nil {
			return err
		}
		x, err := exchangeAt(stateDB, action, p.Exchange)
		if err != nil {
			return err
		}
		operator, err := exchangeAddressParam(action, "operator", p.Operator)
		if err != nil {
			return err
		}
		if p.Active == nil {
			// Explicit on purpose: a missing flag must not silently revoke.
			return fmt.Errorf("exchange set_operator: active is required (true grants, false revokes)")
		}
		return x.SetOperator(ctx.Caller, operator, *p.Active, receipt)

	case "transfer_ownership":
		var p exchangeActionParams
		if err := parseExchangeParams(ctx, action, raw, &p); err != nil {
			return err
		}
		x, err := exchangeAt(stateDB, action, p.Exchange)
		if err != nil {
			return err
		}
		newOwner, err := exchangeAddressParam(action, "new_owner", p.NewOwner)
		if err != nil {
			return err
		}
		return x.TransferOwnership(ctx.Caller, newOwner, receipt)

	default:
		return hard(fmt.Errorf("exchange: unknown action %q", action))
	}
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
