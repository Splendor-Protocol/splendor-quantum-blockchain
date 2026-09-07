// Package vm — native module router.
//
// The chain ships fully-implemented native modules (QRC-20 tokens, the DEX
// liquidity pool, …) that write state directly through StateDB. Until this
// router existed they were unreachable: executeTx had no branch that invoked
// them, and the bytecode VM cannot (its persistent-storage opcodes are shadowed
// by the PUSH range on sqc-vm-v3). This router wires them to the existing
// TxCall (0x02) path via a single reserved address, with no new tx type and no
// VM change.
//
// Invocation: a TxCall whose `To` equals ModuleRouterAddress carries a JSON
// ModuleCall in its Data field. The executor detects that address and calls
// DispatchModuleCall instead of executing bytecode. One address, self-describing
// JSON — so external tooling needs only the standard transaction signer.
package vm

import (
	"encoding/json"
	"fmt"
	"math/big"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// ModuleRouterAddress is the reserved account that receives native-module calls.
// It is SHA3-256 of a fixed domain string, so (a) it can never collide with a
// ML-DSA-65-derived user address, and (b) any client can reproduce it offline.
var ModuleRouterAddress = deriveModuleRouterAddress()

func deriveModuleRouterAddress() qcrypto.Address {
	h := qcrypto.Hash32([]byte("splendor:module-router:v1"))
	var a qcrypto.Address
	copy(a[:], h[:])
	return a
}

// ModuleCall is the JSON envelope in a module TxCall's Data field.
type ModuleCall struct {
	Module string          `json:"module"` // "qrc20" | "qrc1155" | "dex" | "exchange" | "bridge" | "staking"
	Action string          `json:"action"` // e.g. "deploy", "mint", "transfer"
	Params json.RawMessage `json:"params"`
}

// ModuleContext is everything a native module may know about the transaction
// and block it runs in. It is filled by the executor from the transaction and
// from the block header being applied — the producer stamps the header once,
// followers read it back — so a module never consults the wall clock and every
// node sees the same values.
//
//	Caller — the tx sender (authorises owner-only actions like mint)
//	Nonce  — the sender's tx nonce (deterministic deploy-address derivation)
//	Height — the height of the block the tx is part of
//	Time   — that block's header timestamp (unix seconds); deadlines are
//	         judged against it, never against time.Now()
//	Forks  — the chain's rule-change schedule; a gate asks
//	         ctx.Forks.Active(name, ctx.Height), which is nil-safe and reads
//	         "never" for an unscheduled name, so a chain without a schedule
//	         runs the legacy rules unchanged
//
//	GenesisValidator — the address of the key that sealed block 0. It is the
//	         staking module's bootstrap authority: the initial admission admin,
//	         and the one key passed without an entry having been written (see
//	         vm/staking.go). Zero on a node that does not know it and on a bare
//	         StateDB in a test, which the module reads as "no anchor" rather
//	         than as an address.
type ModuleContext struct {
	Caller           qcrypto.Address
	Nonce            uint64
	Height           uint64
	Time             int64
	Forks            forks.Schedule
	GenesisValidator qcrypto.Address
}

// DispatchModuleCall decodes and executes a native-module call against stateDB.
//
//	ctx     — the caller and the block the call executes in (see ModuleContext)
//	data    — the raw ModuleCall JSON from tx.Data
//	receipt — module events are appended here; deploy sets receipt.ContractAddress
//
// A returned error is classified by the executor (see errors.go). A HARD
// error — unparseable envelope, unknown module or action, fork not active —
// makes the tx unappliable: the producer drops it and a follower rejects a
// block carrying it. Any other error is SOFT: under module_revert_v1 the
// module's writes are reverted and the tx is included with a failed receipt,
// the fee kept and the nonce consumed, exactly like a reverted bytecode call;
// before that fork every error drops the tx.
func DispatchModuleCall(stateDB *state.StateDB, ctx ModuleContext, data []byte, receipt *types.Receipt) error {
	var mc ModuleCall
	if err := json.Unmarshal(data, &mc); err != nil {
		return hard(fmt.Errorf("module call: invalid JSON envelope: %w", err))
	}
	switch mc.Module {
	case "qrc20":
		return dispatchQRC20(stateDB, ctx, mc.Action, mc.Params, receipt)
	case "qrc1155":
		return dispatchQRC1155(stateDB, ctx, mc.Action, mc.Params, receipt)
	case "dex":
		return dispatchDEX(stateDB, ctx, mc.Action, mc.Params, receipt)
	case "exchange":
		return dispatchExchange(stateDB, ctx, mc.Action, mc.Params, receipt)
	case "bridge":
		return dispatchBridge(stateDB, ctx, mc.Action, mc.Params, receipt)
	case "staking":
		return dispatchStaking(stateDB, ctx, mc.Action, mc.Params, receipt)
	case "verify":
		return dispatchVerify(stateDB, ctx, mc.Action, mc.Params, receipt)
	default:
		return hard(fmt.Errorf("module call: unknown module %q", mc.Module))
	}
}

// Caps on the free-form strings a deploy or mint persists. A module call pays
// the flat MinFee whatever it writes, so an unbounded name or uri was
// ~128 KiB of state per transaction at the minimum price. SOFT, checked in
// the router before the module runs. The qrc20 caps apply only once qrc20_v2
// is active so a pre-fork replay is unchanged; qrc1155_v1 and exchange_v1
// actions are new under their own forks and carry the cap from the start.
const (
	MaxModuleNameBytes = 64   // qrc20 name and symbol, qrc1155 and exchange name
	MaxModuleURIBytes  = 2048 // qrc1155 uri and uri_prefix, exchange uri_prefix
)

// capString refuses v when it is longer than max bytes.
func capString(what, field, v string, max int) error {
	if len(v) > max {
		return fmt.Errorf("%s: %s is %d bytes, above the cap of %d", what, field, len(v), max)
	}
	return nil
}

// ── QRC-20 ──────────────────────────────────────────────────────────────────

type qrc20DeployParams struct {
	Name        string `json:"name"`
	Symbol      string `json:"symbol"`
	Decimals    uint8  `json:"decimals"`
	TotalSupply string `json:"total_supply"`         // decimal string, smallest unit
	MaxSupply   string `json:"max_supply,omitempty"` // "" or "0" = uncapped
}

type qrc20TokenAction struct {
	Token    string `json:"token"`               // contract SQ: address
	To       string `json:"to,omitempty"`        // recipient (mint/transfer/transfer_from)
	From     string `json:"from,omitempty"`      // holder whose allowance is spent (transfer_from/burn_from)
	Spender  string `json:"spender,omitempty"`   // approve/increase_allowance/decrease_allowance
	NewOwner string `json:"new_owner,omitempty"` // transfer_ownership
	Amount   string `json:"amount"`              // decimal string, smallest unit
}

// qrc20PermitParams is the wire shape of the permit action (qrc20_v2). The
// two byte fields are Go []byte, so on the wire they are standard base64 —
// the same rule as a transaction's public_key/signature. deadline is unix
// seconds, judged against the block time. owner_pubkey is needed only for an
// owner with no registered key and must derive the owner address.
type qrc20PermitParams struct {
	Token       string `json:"token"`
	Owner       string `json:"owner"`
	Spender     string `json:"spender"`
	Amount      string `json:"amount"`                 // decimal string, smallest unit
	Deadline    int64  `json:"deadline"`               // unix seconds, > 0
	Signature   []byte `json:"signature"`              // std base64 on the wire
	OwnerPubKey []byte `json:"owner_pubkey,omitempty"` // std base64 on the wire
}

// requireToken is the IsContract guard for a non-deploy QRC-20 action: the
// address must have been deployed through the module (markContract). Before
// it, a mistyped token address was silently a fresh, empty token whose owner
// is the zero address, so an approve or transfer against it "succeeded"
// against nothing. SOFT: it is the action's own complaint.
func requireToken(stateDB *state.StateDB, token qcrypto.Address) error {
	acc, err := stateDB.GetAccount(token)
	if err != nil {
		return fmt.Errorf("qrc20: token %s: %w", token, err)
	}
	if !acc.IsContract {
		return fmt.Errorf("qrc20: token %s is not a deployed contract", token)
	}
	return nil
}

// qrc20Token resolves the token a non-deploy action names. The IsContract
// guard applies only under qrc20_v2: even a bug-fix change to a pre-existing
// action is fork-gated, so the rule lands at a height every node agrees on.
// The new v2 actions call requireFork first, so for them the guard is
// always on.
func qrc20Token(stateDB *state.StateDB, ctx ModuleContext, token qcrypto.Address) (*QRC20Token, error) {
	if ctx.Forks.Active(forks.QRC20V2, ctx.Height) {
		if err := requireToken(stateDB, token); err != nil {
			return nil, err
		}
	}
	return NewQRC20Token(stateDB, token), nil
}

func dispatchQRC20(stateDB *state.StateDB, ctx ModuleContext, action string, raw json.RawMessage, receipt *types.Receipt) error {
	caller, nonce := ctx.Caller, ctx.Nonce
	switch action {
	case "deploy":
		var p qrc20DeployParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return fmt.Errorf("qrc20 deploy: bad params: %w", err)
		}
		if ctx.Forks.Active(forks.QRC20V2, ctx.Height) {
			if err := capString("qrc20 deploy", "name", p.Name, MaxModuleNameBytes); err != nil {
				return err
			}
			if err := capString("qrc20 deploy", "symbol", p.Symbol, MaxModuleNameBytes); err != nil {
				return err
			}
		}
		supply, err := parseLegacyAmount(ctx, p.TotalSupply)
		if err != nil {
			return fmt.Errorf("qrc20 deploy: total_supply: %w", err)
		}
		maxSupply := big.NewInt(0)
		if p.MaxSupply != "" {
			if maxSupply, err = parseLegacyAmount(ctx, p.MaxSupply); err != nil {
				return fmt.Errorf("qrc20 deploy: max_supply: %w", err)
			}
		}
		addr, err := DeployQRC20Capped(stateDB, caller, p.Name, p.Symbol, p.Decimals, supply, maxSupply, nonce)
		if err != nil {
			return err
		}
		markContract(stateDB, addr)
		receipt.ContractAddress = &addr
		return nil

	case "mint":
		p, token, err := parseTokenAction(raw)
		if err != nil {
			return err
		}
		to, err := qcrypto.AddressFromString(p.To)
		if err != nil {
			return fmt.Errorf("qrc20 mint: to: %w", err)
		}
		amt, err := parseLegacyAmount(ctx, p.Amount)
		if err != nil {
			return fmt.Errorf("qrc20 mint: amount: %w", err)
		}
		tok, err := qrc20Token(stateDB, ctx, token)
		if err != nil {
			return err
		}
		return tok.MintWithEvents(caller, to, amt, receipt)

	case "transfer":
		p, token, err := parseTokenAction(raw)
		if err != nil {
			return err
		}
		to, err := qcrypto.AddressFromString(p.To)
		if err != nil {
			return fmt.Errorf("qrc20 transfer: to: %w", err)
		}
		amt, err := parseLegacyAmount(ctx, p.Amount)
		if err != nil {
			return fmt.Errorf("qrc20 transfer: amount: %w", err)
		}
		tok, err := qrc20Token(stateDB, ctx, token)
		if err != nil {
			return err
		}
		return tok.TransferWithEvents(caller, to, amt, receipt)

	case "approve":
		p, token, err := parseTokenAction(raw)
		if err != nil {
			return err
		}
		spender, err := qcrypto.AddressFromString(p.Spender)
		if err != nil {
			return fmt.Errorf("qrc20 approve: spender: %w", err)
		}
		amt, err := parseLegacyAmount(ctx, p.Amount)
		if err != nil {
			return fmt.Errorf("qrc20 approve: amount: %w", err)
		}
		tok, err := qrc20Token(stateDB, ctx, token)
		if err != nil {
			return err
		}
		return tok.ApproveWithEvents(caller, spender, amt, receipt)

	case "burn":
		p, token, err := parseTokenAction(raw)
		if err != nil {
			return err
		}
		amt, err := parseLegacyAmount(ctx, p.Amount)
		if err != nil {
			return fmt.Errorf("qrc20 burn: amount: %w", err)
		}
		tok, err := qrc20Token(stateDB, ctx, token)
		if err != nil {
			return err
		}
		return tok.BurnWithEvents(caller, amt, receipt)

	// ── qrc20_v2: the permit/allowance surface and the owner controls. Each
	// is fork-gated first (HARD before the height), then parsed with the
	// strict decoders, then resolved through the IsContract guard.

	case "permit":
		if err := requireFork(ctx, forks.QRC20V2); err != nil {
			return err
		}
		var p qrc20PermitParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return fmt.Errorf("qrc20 permit: bad params: %w", err)
		}
		token, err := qcrypto.AddressFromString(p.Token)
		if err != nil {
			return fmt.Errorf("qrc20 permit: token: %w", err)
		}
		owner, err := qcrypto.AddressFromString(p.Owner)
		if err != nil {
			return fmt.Errorf("qrc20 permit: owner: %w", err)
		}
		spender, err := qcrypto.AddressFromString(p.Spender)
		if err != nil {
			return fmt.Errorf("qrc20 permit: spender: %w", err)
		}
		amt, err := parseUint256(p.Amount)
		if err != nil {
			return fmt.Errorf("qrc20 permit: amount: %w", err)
		}
		tok, err := qrc20Token(stateDB, ctx, token)
		if err != nil {
			return err
		}
		return tok.Permit(owner, spender, amt, p.Deadline, p.Signature, p.OwnerPubKey, ctx.Time, receipt)

	case "transfer_from":
		if err := requireFork(ctx, forks.QRC20V2); err != nil {
			return err
		}
		p, token, err := parseTokenAction(raw)
		if err != nil {
			return err
		}
		from, err := qcrypto.AddressFromString(p.From)
		if err != nil {
			return fmt.Errorf("qrc20 transfer_from: from: %w", err)
		}
		to, err := qcrypto.AddressFromString(p.To)
		if err != nil {
			return fmt.Errorf("qrc20 transfer_from: to: %w", err)
		}
		amt, err := parseUint256(p.Amount)
		if err != nil {
			return fmt.Errorf("qrc20 transfer_from: amount: %w", err)
		}
		tok, err := qrc20Token(stateDB, ctx, token)
		if err != nil {
			return err
		}
		return tok.TransferFromWithEvents(caller, from, to, amt, receipt)

	case "increase_allowance":
		if err := requireFork(ctx, forks.QRC20V2); err != nil {
			return err
		}
		p, token, err := parseTokenAction(raw)
		if err != nil {
			return err
		}
		spender, err := qcrypto.AddressFromString(p.Spender)
		if err != nil {
			return fmt.Errorf("qrc20 increase_allowance: spender: %w", err)
		}
		amt, err := parseUint256(p.Amount)
		if err != nil {
			return fmt.Errorf("qrc20 increase_allowance: amount: %w", err)
		}
		tok, err := qrc20Token(stateDB, ctx, token)
		if err != nil {
			return err
		}
		return tok.IncreaseAllowanceWithEvents(caller, spender, amt, receipt)

	case "decrease_allowance":
		if err := requireFork(ctx, forks.QRC20V2); err != nil {
			return err
		}
		p, token, err := parseTokenAction(raw)
		if err != nil {
			return err
		}
		spender, err := qcrypto.AddressFromString(p.Spender)
		if err != nil {
			return fmt.Errorf("qrc20 decrease_allowance: spender: %w", err)
		}
		amt, err := parseUint256(p.Amount)
		if err != nil {
			return fmt.Errorf("qrc20 decrease_allowance: amount: %w", err)
		}
		tok, err := qrc20Token(stateDB, ctx, token)
		if err != nil {
			return err
		}
		return tok.DecreaseAllowanceWithEvents(caller, spender, amt, receipt)

	case "burn_from":
		if err := requireFork(ctx, forks.QRC20V2); err != nil {
			return err
		}
		p, token, err := parseTokenAction(raw)
		if err != nil {
			return err
		}
		from, err := qcrypto.AddressFromString(p.From)
		if err != nil {
			return fmt.Errorf("qrc20 burn_from: from: %w", err)
		}
		amt, err := parseUint256(p.Amount)
		if err != nil {
			return fmt.Errorf("qrc20 burn_from: amount: %w", err)
		}
		tok, err := qrc20Token(stateDB, ctx, token)
		if err != nil {
			return err
		}
		return tok.BurnFromWithEvents(caller, from, amt, receipt)

	case "transfer_ownership":
		if err := requireFork(ctx, forks.QRC20V2); err != nil {
			return err
		}
		p, token, err := parseTokenAction(raw)
		if err != nil {
			return err
		}
		newOwner, err := qcrypto.AddressFromString(p.NewOwner)
		if err != nil {
			return fmt.Errorf("qrc20 transfer_ownership: new_owner: %w", err)
		}
		tok, err := qrc20Token(stateDB, ctx, token)
		if err != nil {
			return err
		}
		return tok.TransferOwnershipWithEvents(caller, newOwner, receipt)

	case "pause":
		if err := requireFork(ctx, forks.QRC20V2); err != nil {
			return err
		}
		_, token, err := parseTokenAction(raw)
		if err != nil {
			return err
		}
		tok, err := qrc20Token(stateDB, ctx, token)
		if err != nil {
			return err
		}
		return tok.PauseWithEvents(caller, receipt)

	case "unpause":
		if err := requireFork(ctx, forks.QRC20V2); err != nil {
			return err
		}
		_, token, err := parseTokenAction(raw)
		if err != nil {
			return err
		}
		tok, err := qrc20Token(stateDB, ctx, token)
		if err != nil {
			return err
		}
		return tok.UnpauseWithEvents(caller, receipt)

	default:
		return hard(fmt.Errorf("qrc20: unknown action %q", action))
	}
}

func parseTokenAction(raw json.RawMessage) (qrc20TokenAction, qcrypto.Address, error) {
	var p qrc20TokenAction
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, qcrypto.Address{}, fmt.Errorf("qrc20: bad params: %w", err)
	}
	token, err := qcrypto.AddressFromString(p.Token)
	if err != nil {
		return p, qcrypto.Address{}, fmt.Errorf("qrc20: token: %w", err)
	}
	return p, token, nil
}

// ── QRC-1155 ────────────────────────────────────────────────────────────────
//
// Every qrc1155 write is fork-gated under qrc1155_v1 (HARD before the
// height). Amounts and ids are decimal strings through the strict
// parseUint256 decoder: canonical decimal, no sign, no leading zero, below
// 2^256. Batch lists are capped at MaxQRC1155Batch before anything is parsed.

type qrc1155DeployParams struct {
	Name      string `json:"name"`
	URIPrefix string `json:"uri_prefix,omitempty"` // e.g. "ipfs://QmBase/"; per-token uri takes precedence
}

// qrc1155Action is the wire shape shared by every non-deploy qrc1155 write.
// `from` defaults to the caller on transfer/batch_transfer/burn/burn_batch;
// naming another holder needs that holder's set_approval_for_all.
type qrc1155Action struct {
	Collection string   `json:"collection"`          // contract SQ: address
	To         string   `json:"to,omitempty"`        // recipient (mint/mint_batch/transfer/batch_transfer)
	From       string   `json:"from,omitempty"`      // holder (transfer/batch_transfer/burn/burn_batch); "" = caller
	Operator   string   `json:"operator,omitempty"`  // set_approval_for_all
	Approved   bool     `json:"approved,omitempty"`  // set_approval_for_all
	NewOwner   string   `json:"new_owner,omitempty"` // transfer_ownership
	ID         string   `json:"id,omitempty"`        // decimal string (mint/transfer/burn)
	Amount     string   `json:"amount,omitempty"`    // decimal string (mint/transfer/burn)
	URI        string   `json:"uri,omitempty"`       // per-token uri (mint)
	IDs        []string `json:"ids,omitempty"`       // decimal strings (batch paths, ≤ MaxQRC1155Batch)
	Amounts    []string `json:"amounts,omitempty"`   // decimal strings (batch paths)
	URIs       []string `json:"uris,omitempty"`      // per-token uris (mint_batch), may be shorter than ids
}

// requireCollection is the IsContract guard for a non-deploy QRC-1155 action:
// the address must have been deployed through the module (markContract), so
// a mistyped address is refused instead of being a fresh, empty, ownerless
// collection. SOFT: it is the action's own complaint.
func requireCollection(stateDB *state.StateDB, collection qcrypto.Address) error {
	acc, err := stateDB.GetAccount(collection)
	if err != nil {
		return fmt.Errorf("qrc1155: collection %s: %w", collection, err)
	}
	if !acc.IsContract {
		return fmt.Errorf("qrc1155: collection %s is not a deployed contract", collection)
	}
	return nil
}

// parseQRC1155Action is the first step of every non-deploy qrc1155 write:
// the fork gate (HARD before the height), then the shared params, then the
// collection (IsContract-guarded). `from` falls back to the caller.
func parseQRC1155Action(stateDB *state.StateDB, ctx ModuleContext, raw json.RawMessage) (qrc1155Action, *QRC1155Token, qcrypto.Address, error) {
	var p qrc1155Action
	if err := requireFork(ctx, forks.QRC1155V1); err != nil {
		return p, nil, qcrypto.Address{}, err
	}
	caller := ctx.Caller
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, nil, qcrypto.Address{}, fmt.Errorf("qrc1155: bad params: %w", err)
	}
	collection, err := qcrypto.AddressFromString(p.Collection)
	if err != nil {
		return p, nil, qcrypto.Address{}, fmt.Errorf("qrc1155: collection: %w", err)
	}
	if err := requireCollection(stateDB, collection); err != nil {
		return p, nil, qcrypto.Address{}, err
	}
	from := caller
	if p.From != "" {
		if from, err = qcrypto.AddressFromString(p.From); err != nil {
			return p, nil, qcrypto.Address{}, fmt.Errorf("qrc1155: from: %w", err)
		}
	}
	return p, NewQRC1155Token(stateDB, collection), from, nil
}

// parseUint256List decodes a batch of decimal strings, refusing a list over
// the cap before parsing any element.
func parseUint256List(what string, ss []string) ([]*big.Int, error) {
	if len(ss) > MaxQRC1155Batch {
		return nil, fmt.Errorf("%s: %d entries exceeds the batch cap of %d", what, len(ss), MaxQRC1155Batch)
	}
	out := make([]*big.Int, len(ss))
	for i, s := range ss {
		v, err := parseUint256(s)
		if err != nil {
			return nil, fmt.Errorf("%s[%d]: %w", what, i, err)
		}
		out[i] = v
	}
	return out, nil
}

// userCollection is the guard on the router's burn/transfer paths: a
// module-managed collection (owner == its own address) is mutated only by
// the owning module, so a holder can never destroy or move one leg of a
// position set the module accounts for.
func userCollection(action string, tok *QRC1155Token) error {
	if err := tok.requireUserManaged(); err != nil {
		return fmt.Errorf("qrc1155 %s: %w", action, err)
	}
	return nil
}

func dispatchQRC1155(stateDB *state.StateDB, ctx ModuleContext, action string, raw json.RawMessage, receipt *types.Receipt) error {
	caller, nonce := ctx.Caller, ctx.Nonce
	switch action {
	case "deploy":
		if err := requireFork(ctx, forks.QRC1155V1); err != nil {
			return err
		}
		var p qrc1155DeployParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return fmt.Errorf("qrc1155 deploy: bad params: %w", err)
		}
		if err := capString("qrc1155 deploy", "name", p.Name, MaxModuleNameBytes); err != nil {
			return err
		}
		if err := capString("qrc1155 deploy", "uri_prefix", p.URIPrefix, MaxModuleURIBytes); err != nil {
			return err
		}
		addr, err := DeployQRC1155(stateDB, caller, p.Name, p.URIPrefix, nonce)
		if err != nil {
			return err
		}
		markContract(stateDB, addr)
		receipt.ContractAddress = &addr
		return nil

	case "mint":
		p, tok, _, err := parseQRC1155Action(stateDB, ctx, raw)
		if err != nil {
			return err
		}
		to, err := qcrypto.AddressFromString(p.To)
		if err != nil {
			return fmt.Errorf("qrc1155 mint: to: %w", err)
		}
		id, err := parseUint256(p.ID)
		if err != nil {
			return fmt.Errorf("qrc1155 mint: id: %w", err)
		}
		amt, err := parseUint256(p.Amount)
		if err != nil {
			return fmt.Errorf("qrc1155 mint: amount: %w", err)
		}
		if err := capString("qrc1155 mint", "uri", p.URI, MaxModuleURIBytes); err != nil {
			return err
		}
		return tok.MintWithEvents(caller, to, id, amt, p.URI, receipt)

	case "mint_batch":
		p, tok, _, err := parseQRC1155Action(stateDB, ctx, raw)
		if err != nil {
			return err
		}
		to, err := qcrypto.AddressFromString(p.To)
		if err != nil {
			return fmt.Errorf("qrc1155 mint_batch: to: %w", err)
		}
		ids, err := parseUint256List("qrc1155 mint_batch: ids", p.IDs)
		if err != nil {
			return err
		}
		amts, err := parseUint256List("qrc1155 mint_batch: amounts", p.Amounts)
		if err != nil {
			return err
		}
		for i, u := range p.URIs {
			if err := capString("qrc1155 mint_batch", fmt.Sprintf("uris[%d]", i), u, MaxModuleURIBytes); err != nil {
				return err
			}
		}
		return tok.MintBatchWithEvents(caller, to, ids, amts, p.URIs, receipt)

	case "transfer":
		p, tok, from, err := parseQRC1155Action(stateDB, ctx, raw)
		if err != nil {
			return err
		}
		if err := userCollection(action, tok); err != nil {
			return err
		}
		to, err := qcrypto.AddressFromString(p.To)
		if err != nil {
			return fmt.Errorf("qrc1155 transfer: to: %w", err)
		}
		id, err := parseUint256(p.ID)
		if err != nil {
			return fmt.Errorf("qrc1155 transfer: id: %w", err)
		}
		amt, err := parseUint256(p.Amount)
		if err != nil {
			return fmt.Errorf("qrc1155 transfer: amount: %w", err)
		}
		return tok.SafeTransferFromWithEvents(caller, from, to, id, amt, receipt)

	case "batch_transfer":
		p, tok, from, err := parseQRC1155Action(stateDB, ctx, raw)
		if err != nil {
			return err
		}
		if err := userCollection(action, tok); err != nil {
			return err
		}
		to, err := qcrypto.AddressFromString(p.To)
		if err != nil {
			return fmt.Errorf("qrc1155 batch_transfer: to: %w", err)
		}
		ids, err := parseUint256List("qrc1155 batch_transfer: ids", p.IDs)
		if err != nil {
			return err
		}
		amts, err := parseUint256List("qrc1155 batch_transfer: amounts", p.Amounts)
		if err != nil {
			return err
		}
		return tok.SafeBatchTransferFromWithEvents(caller, from, to, ids, amts, receipt)

	case "set_approval_for_all":
		p, tok, _, err := parseQRC1155Action(stateDB, ctx, raw)
		if err != nil {
			return err
		}
		operator, err := qcrypto.AddressFromString(p.Operator)
		if err != nil {
			return fmt.Errorf("qrc1155 set_approval_for_all: operator: %w", err)
		}
		return tok.SetApprovalForAllWithEvents(caller, operator, p.Approved, receipt)

	case "burn":
		p, tok, from, err := parseQRC1155Action(stateDB, ctx, raw)
		if err != nil {
			return err
		}
		if err := userCollection(action, tok); err != nil {
			return err
		}
		id, err := parseUint256(p.ID)
		if err != nil {
			return fmt.Errorf("qrc1155 burn: id: %w", err)
		}
		amt, err := parseUint256(p.Amount)
		if err != nil {
			return fmt.Errorf("qrc1155 burn: amount: %w", err)
		}
		return tok.BurnWithEvents(caller, from, id, amt, receipt)

	case "burn_batch":
		p, tok, from, err := parseQRC1155Action(stateDB, ctx, raw)
		if err != nil {
			return err
		}
		if err := userCollection(action, tok); err != nil {
			return err
		}
		ids, err := parseUint256List("qrc1155 burn_batch: ids", p.IDs)
		if err != nil {
			return err
		}
		amts, err := parseUint256List("qrc1155 burn_batch: amounts", p.Amounts)
		if err != nil {
			return err
		}
		return tok.BurnBatchWithEvents(caller, from, ids, amts, receipt)

	case "transfer_ownership":
		p, tok, _, err := parseQRC1155Action(stateDB, ctx, raw)
		if err != nil {
			return err
		}
		newOwner, err := qcrypto.AddressFromString(p.NewOwner)
		if err != nil {
			return fmt.Errorf("qrc1155 transfer_ownership: new_owner: %w", err)
		}
		return tok.TransferOwnershipWithEvents(caller, newOwner, receipt)

	default:
		return hard(fmt.Errorf("qrc1155: unknown action %q", action))
	}
}

// ── DEX ─────────────────────────────────────────────────────────────────────

type dexDeployPoolParams struct {
	Token0 string `json:"token0"`
	Token1 string `json:"token1"`
}

type dexLiquidityParams struct {
	Pool           string `json:"pool"`
	Amount0Desired string `json:"amount0_desired"`
	Amount1Desired string `json:"amount1_desired"`
	Amount0Min     string `json:"amount0_min,omitempty"`
	Amount1Min     string `json:"amount1_min,omitempty"`
}

type dexSwapParams struct {
	Pool         string `json:"pool"`
	TokenIn      uint8  `json:"token_in"` // 0 = token0, 1 = token1
	AmountIn     string `json:"amount_in"`
	MinAmountOut string `json:"min_amount_out,omitempty"`
}

func dispatchDEX(stateDB *state.StateDB, ctx ModuleContext, action string, raw json.RawMessage, receipt *types.Receipt) error {
	caller, nonce := ctx.Caller, ctx.Nonce
	switch action {
	case "deploy_pool":
		var p dexDeployPoolParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return fmt.Errorf("dex deploy_pool: bad params: %w", err)
		}
		t0, err := qcrypto.AddressFromString(p.Token0)
		if err != nil {
			return fmt.Errorf("dex deploy_pool: token0: %w", err)
		}
		t1, err := qcrypto.AddressFromString(p.Token1)
		if err != nil {
			return fmt.Errorf("dex deploy_pool: token1: %w", err)
		}
		addr, err := DeployPool(stateDB, caller, t0, t1, nonce)
		if err != nil {
			return err
		}
		markContract(stateDB, addr)
		receipt.ContractAddress = &addr
		return nil

	case "add_liquidity":
		var p dexLiquidityParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return fmt.Errorf("dex add_liquidity: bad params: %w", err)
		}
		pool, err := qcrypto.AddressFromString(p.Pool)
		if err != nil {
			return fmt.Errorf("dex add_liquidity: pool: %w", err)
		}
		a0, err := parseBig(p.Amount0Desired)
		if err != nil {
			return fmt.Errorf("dex add_liquidity: amount0_desired: %w", err)
		}
		a1, err := parseBig(p.Amount1Desired)
		if err != nil {
			return fmt.Errorf("dex add_liquidity: amount1_desired: %w", err)
		}
		min0 := parseBigOrZero(p.Amount0Min)
		min1 := parseBigOrZero(p.Amount1Min)
		_, _, _, err = NewLiquidityPool(stateDB, pool).AddLiquidity(caller, a0, a1, min0, min1)
		return err

	case "swap":
		var p dexSwapParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return fmt.Errorf("dex swap: bad params: %w", err)
		}
		pool, err := qcrypto.AddressFromString(p.Pool)
		if err != nil {
			return fmt.Errorf("dex swap: pool: %w", err)
		}
		amtIn, err := parseBig(p.AmountIn)
		if err != nil {
			return fmt.Errorf("dex swap: amount_in: %w", err)
		}
		minOut := parseBigOrZero(p.MinAmountOut)
		_, err = NewLiquidityPool(stateDB, pool).SwapExactIn(caller, caller, p.TokenIn, amtIn, minOut)
		return err

	default:
		return hard(fmt.Errorf("dex: unknown action %q", action))
	}
}

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

// ── Bridge ──────────────────────────────────────────────────────────────────

// parseBridgeParams is the first step of every bridge write: the fork gate
// (HARD before the height), then the params (SOFT: the action's own "bad
// params" complaint).
func parseBridgeParams(ctx ModuleContext, action string, raw json.RawMessage, p any) error {
	if err := requireFork(ctx, forks.BridgeV1); err != nil {
		return err
	}
	if err := json.Unmarshal(raw, p); err != nil {
		return fmt.Errorf("bridge %s: bad params: %w", action, err)
	}
	return nil
}

// bridgeAt resolves the instance a non-deploy action names. Whether anything
// is deployed there is the action's own check (requireDeployed).
func bridgeAt(stateDB *state.StateDB, action, s string) (*Bridge, error) {
	a, err := qcrypto.AddressFromString(s)
	if err != nil {
		return nil, fmt.Errorf("bridge %s: bridge: %w", action, err)
	}
	return NewBridge(stateDB, a), nil
}

// bridgeAddressParam decodes one address field of a bridge action.
func bridgeAddressParam(action, field, s string) (qcrypto.Address, error) {
	a, err := qcrypto.AddressFromString(s)
	if err != nil {
		return qcrypto.Address{}, fmt.Errorf("bridge %s: %s: %w", action, field, err)
	}
	return a, nil
}

// dispatchBridge routes the migration bridge's writes (bridge_v1). Every
// action is gated first; everything after the gate is SOFT and prefixed
// "bridge <action>: ". The module methods snapshot and revert on their own.
func dispatchBridge(stateDB *state.StateDB, ctx ModuleContext, action string, raw json.RawMessage, receipt *types.Receipt) error {
	switch action {
	case "deploy":
		var p bridgeDeployParams
		if err := parseBridgeParams(ctx, action, raw, &p); err != nil {
			return err
		}
		if err := capString("bridge deploy", "splv_name", p.SplvName, MaxModuleNameBytes); err != nil {
			return err
		}
		if err := capString("bridge deploy", "splv_symbol", p.SplvSymbol, MaxModuleNameBytes); err != nil {
			return err
		}
		addr, err := DeployBridge(stateDB, ctx, p)
		if err != nil {
			return err
		}
		receipt.ContractAddress = &addr
		return nil

	case "claim":
		var p bridgeClaimParams
		if err := parseBridgeParams(ctx, action, raw, &p); err != nil {
			return err
		}
		b, err := bridgeAt(stateDB, action, p.Bridge)
		if err != nil {
			return err
		}
		return b.Claim(ctx, p, receipt)

	case "set_operator":
		var p bridgeActionParams
		if err := parseBridgeParams(ctx, action, raw, &p); err != nil {
			return err
		}
		b, err := bridgeAt(stateDB, action, p.Bridge)
		if err != nil {
			return err
		}
		operator, err := bridgeAddressParam(action, "operator", p.Operator)
		if err != nil {
			return err
		}
		if p.Active == nil {
			// Explicit on purpose: a missing flag must not silently revoke.
			return fmt.Errorf("bridge set_operator: active is required (true grants, false revokes)")
		}
		return b.SetOperator(ctx.Caller, operator, *p.Active, receipt)

	case "pause":
		var p bridgeActionParams
		if err := parseBridgeParams(ctx, action, raw, &p); err != nil {
			return err
		}
		b, err := bridgeAt(stateDB, action, p.Bridge)
		if err != nil {
			return err
		}
		return b.Pause(ctx.Caller, receipt)

	case "unpause":
		var p bridgeActionParams
		if err := parseBridgeParams(ctx, action, raw, &p); err != nil {
			return err
		}
		b, err := bridgeAt(stateDB, action, p.Bridge)
		if err != nil {
			return err
		}
		return b.Unpause(ctx.Caller, receipt)

	case "transfer_ownership":
		var p bridgeActionParams
		if err := parseBridgeParams(ctx, action, raw, &p); err != nil {
			return err
		}
		b, err := bridgeAt(stateDB, action, p.Bridge)
		if err != nil {
			return err
		}
		newOwner, err := bridgeAddressParam(action, "new_owner", p.NewOwner)
		if err != nil {
			return err
		}
		return b.TransferOwnership(ctx.Caller, newOwner, receipt)

	case "set_threshold":
		var p bridgeActionParams
		if err := parseBridgeParams(ctx, action, raw, &p); err != nil {
			return err
		}
		b, err := bridgeAt(stateDB, action, p.Bridge)
		if err != nil {
			return err
		}
		return b.SetThreshold(ctx.Caller, p.Threshold, receipt)

	default:
		return hard(fmt.Errorf("bridge: unknown action %q", action))
	}
}

// ── Staking ─────────────────────────────────────────────────────────────────

// stakingActionParams is the wire shape of every staking write.
//
// There is no deploy: the registry is a singleton at StakingAddress, because a
// chain has exactly one validator set and a second instance would be a second
// answer to the only question consensus asks here.
//
// pubkey is Go []byte, so standard base64 on the wire — the same rule as a
// transaction's public_key. Amounts are decimal strings through parseUint256.
type stakingActionParams struct {
	// register, set_pass: the 1952-byte ML-DSA-65 consensus key. register
	// requires it to be the CALLER's own key; set_pass names the key being
	// admitted or revoked.
	PubKey []byte `json:"pubkey,omitempty"`

	// register, add_stake, unstake: the amount, in smallest SPLD units.
	// unjail: an optional top-up ("" or absent = none). slash: the penalty.
	Amount string `json:"amount,omitempty"`

	// slash: whose bond to take from.
	Validator string `json:"validator,omitempty"`

	// set_pass: required, and a pointer on purpose — a missing flag must not
	// silently revoke an admission that a typo left out.
	Passed *bool `json:"passed,omitempty"`

	// set_pass: let the key produce without meeting the minimum stake. Only
	// meaningful together with passed=true.
	BondExempt bool `json:"bond_exempt,omitempty"`

	// set_admin: the address the admission authority moves to.
	Admin string `json:"admin,omitempty"`
}

// parseStakingParams gates the action (HARD before validator_gates_v1) and
// then decodes it (SOFT).
func parseStakingParams(ctx ModuleContext, action string, raw json.RawMessage, p *stakingActionParams) error {
	if err := requireFork(ctx, forks.ValidatorGatesV1); err != nil {
		return err
	}
	// withdraw takes no parameters, so an omitted params field is legitimate
	// here in a way it is not for the other modules.
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, p); err != nil {
		return fmt.Errorf("staking %s: bad params: %w", action, err)
	}
	return nil
}

// stakingAmount decodes a required amount field.
func stakingAmount(action, s string) (*big.Int, error) {
	v, err := parseUint256(s)
	if err != nil {
		return nil, fmt.Errorf("staking %s: amount: %w", action, err)
	}
	return v, nil
}

// dispatchStaking routes the staking module's writes (validator_gates_v1).
//
// Registering and bonding are OPEN — anyone who syncs the chain may stake
// behind their own key. set_pass, set_admin and slash are admin-only, checked
// inside the module against the admission authority. Every action is gated
// first; everything after the gate is SOFT, so a stake that cannot be paid or
// a pass a non-admin asked for is a failed receipt, never a jammed nonce.
func dispatchStaking(stateDB *state.StateDB, ctx ModuleContext, action string, raw json.RawMessage, receipt *types.Receipt) error {
	var p stakingActionParams
	if err := parseStakingParams(ctx, action, raw, &p); err != nil {
		return err
	}
	s := Staking(stateDB)

	switch action {
	case "register":
		amount, err := stakingAmount(action, p.Amount)
		if err != nil {
			return err
		}
		return s.Register(ctx.Caller, p.PubKey, amount, ctx.Height, receipt)

	case "add_stake":
		amount, err := stakingAmount(action, p.Amount)
		if err != nil {
			return err
		}
		return s.AddStake(ctx.Caller, amount, receipt)

	case "unstake":
		amount, err := stakingAmount(action, p.Amount)
		if err != nil {
			return err
		}
		return s.Unstake(ctx.Caller, ctx.GenesisValidator, amount, ctx.Height, receipt)

	case "withdraw":
		return s.Withdraw(ctx.Caller, ctx.Height, receipt)

	case "unjail":
		// The top-up is optional: a validator whose bond already clears the
		// floor returns with nothing added.
		top := big.NewInt(0)
		if p.Amount != "" {
			var err error
			if top, err = stakingAmount(action, p.Amount); err != nil {
				return err
			}
		}
		return s.Unjail(ctx.Caller, top, receipt)

	case "slash":
		validator, err := qcrypto.AddressFromString(p.Validator)
		if err != nil {
			return fmt.Errorf("staking slash: validator: %w", err)
		}
		amount, err := stakingAmount(action, p.Amount)
		if err != nil {
			return err
		}
		return s.Slash(ctx.Caller, ctx.GenesisValidator, validator, amount, ctx.Height, receipt)

	case "set_pass":
		if p.Passed == nil {
			// Explicit on purpose: a missing flag must not silently revoke.
			return fmt.Errorf("staking set_pass: passed is required (true admits, false revokes)")
		}
		return s.SetPass(ctx.Caller, ctx.GenesisValidator, p.PubKey, *p.Passed, p.BondExempt, ctx.Height, receipt)

	case "set_admin":
		next, err := qcrypto.AddressFromString(p.Admin)
		if err != nil {
			return fmt.Errorf("staking set_admin: admin: %w", err)
		}
		return s.SetAdmin(ctx.Caller, ctx.GenesisValidator, next, receipt)

	default:
		return hard(fmt.Errorf("staking: unknown action %q", action))
	}
}

// ── helpers ─────────────────────────────────────────────────────────────────

// markContract flags a module-deployed account as a contract so block explorers
// and the generic /contract endpoint recognise it. Native modules keep their
// state in storage slots (not bytecode), so there is no code to set — only the
// IsContract marker. Best-effort: a marking failure never fails the deploy.
func markContract(stateDB *state.StateDB, addr qcrypto.Address) {
	acc, err := stateDB.GetAccount(addr)
	if err != nil {
		return
	}
	acc.IsContract = true
	stateDB.SetAccount(acc)
}

func parseBig(s string) (*big.Int, error) {
	v, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return nil, fmt.Errorf("invalid decimal amount %q", s)
	}
	if v.Sign() < 0 {
		return nil, fmt.Errorf("negative amount %q", s)
	}
	return v, nil
}

// requireOwnableTarget refuses an ownership transfer to an address that can
// never sign an owner action: the contract itself (for a QRC-1155 that is
// also the module-managed marker, so the transfer would irreversibly freeze
// every holder), the module router, or any deployed contract. Mirrors the
// exchange's rule; SOFT, it is the action's own complaint.
func requireOwnableTarget(stateDB *state.StateDB, what string, contract, newOwner qcrypto.Address) error {
	if newOwner == contract {
		return fmt.Errorf("%s: new owner cannot be the contract itself", what)
	}
	if newOwner == ModuleRouterAddress {
		return fmt.Errorf("%s: new owner cannot be the module router", what)
	}
	acc, err := stateDB.GetAccount(newOwner)
	if err != nil {
		return fmt.Errorf("%s: new owner %s: %w", what, newOwner, err)
	}
	if acc.IsContract {
		return fmt.Errorf("%s: new owner %s is a contract and can never act as owner", what, newOwner)
	}
	return nil
}

// parseLegacyAmount decodes an amount on a qrc20 action that shipped before
// qrc20_v2 (deploy, mint, transfer, approve, burn). Those actions accepted any
// non-negative decimal, so a supply or balance above 2^256 could reach the
// event encoder; once qrc20_v2 is active they take the strict parseUint256
// like every newer action. Pre-fork the loose decoder stays, so a replayed
// block is gated exactly as it was when first applied. (Documented legacy
// behaviour: the event helpers truncate an oversized value to its low word
// rather than panic.)
func parseLegacyAmount(ctx ModuleContext, s string) (*big.Int, error) {
	if ctx.Forks.Active(forks.QRC20V2, ctx.Height) {
		return parseUint256(s)
	}
	return parseBig(s)
}

// maxUint256Plus1 is 2^256; every on-chain amount and id is strictly below it.
var maxUint256Plus1 = new(big.Int).Lsh(big.NewInt(1), 256)

// parseUint256 is the strict decoder for amounts and ids on new module wire
// shapes: a non-empty string of ASCII digits with no sign, no whitespace and
// no leading zero (other than "0" itself), whose value is below 2^256. Unlike
// parseBig it never accepts a "+" sign or a value that would not fit a
// 32-byte word, and unlike parseBigOrZero it never turns garbage into zero —
// a malformed amount is an error, not a free no-op.
func parseUint256(s string) (*big.Int, error) {
	if s == "" {
		return nil, fmt.Errorf("empty decimal amount")
	}
	if len(s) > 78 { // 2^256 has 78 decimal digits
		return nil, fmt.Errorf("decimal amount %q is too long", s)
	}
	if len(s) > 1 && s[0] == '0' {
		return nil, fmt.Errorf("decimal amount %q has a leading zero", s)
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return nil, fmt.Errorf("invalid decimal amount %q", s)
		}
	}
	v, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return nil, fmt.Errorf("invalid decimal amount %q", s)
	}
	if v.Cmp(maxUint256Plus1) >= 0 {
		return nil, fmt.Errorf("decimal amount %q does not fit in 256 bits", s)
	}
	return v, nil
}

func parseBigOrZero(s string) *big.Int {
	if s == "" {
		return big.NewInt(0)
	}
	if v, ok := new(big.Int).SetString(s, 10); ok {
		return v
	}
	return big.NewInt(0)
}

// ── Verify (SQVL) ────────────────────────────────────────────────────────────

// dispatchVerify routes the verify module (docs/modules/verify.md). Every
// action runs under one snapshot, so a half-applied one cannot outlive its own
// failure; the fork gate and an unknown action are HARD, everything after is
// SOFT (a failed receipt under module_revert_v1, which verify_v1 requires).
func dispatchVerify(stateDB *state.StateDB, ctx ModuleContext, action string, raw json.RawMessage, receipt *types.Receipt) error {
	if err := requireFork(ctx, forks.VerifyV1); err != nil {
		return err
	}
	return verifyAtomic(stateDB, receipt, func() error {
		switch action {
		case "deploy":
			return verifyDeploy(stateDB, ctx, raw, receipt)
		case "register":
			return verifyRegister(stateDB, ctx, raw, receipt)
		case "attest":
			return verifyAttest(stateDB, ctx, raw, receipt)
		case "finalize":
			return verifyFinalize(stateDB, ctx, raw, receipt)
		case "spend":
			return verifySpend(stateDB, ctx, raw, receipt)
		case "set_operator":
			return verifySetOperator(stateDB, ctx, raw, receipt)
		case "set_threshold":
			return verifySetThreshold(stateDB, ctx, raw, receipt)
		case "pause":
			return verifyPause(stateDB, ctx, raw, receipt, true)
		case "unpause":
			return verifyPause(stateDB, ctx, raw, receipt, false)
		default:
			return hard(fmt.Errorf("module call: unknown action %q", action))
		}
	})
}
