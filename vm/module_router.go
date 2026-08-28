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
	Module string          `json:"module"` // "qrc20" | "dex"
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
type ModuleContext struct {
	Caller qcrypto.Address
	Nonce  uint64
	Height uint64
	Time   int64
}

// DispatchModuleCall decodes and executes a native-module call against stateDB.
//
//	ctx     — the caller and the block the call executes in (see ModuleContext)
//	data    — the raw ModuleCall JSON from tx.Data
//	receipt — module events are appended here; deploy sets receipt.ContractAddress
//
// A returned error is recorded as a failed receipt by the executor (the fee is
// still charged and the nonce still advances, exactly like a reverted call).
func DispatchModuleCall(stateDB *state.StateDB, ctx ModuleContext, data []byte, receipt *types.Receipt) error {
	var mc ModuleCall
	if err := json.Unmarshal(data, &mc); err != nil {
		return fmt.Errorf("module call: invalid JSON envelope: %w", err)
	}
	switch mc.Module {
	case "qrc20":
		return dispatchQRC20(stateDB, ctx, mc.Action, mc.Params, receipt)
	case "dex":
		return dispatchDEX(stateDB, ctx, mc.Action, mc.Params, receipt)
	default:
		return fmt.Errorf("module call: unknown module %q", mc.Module)
	}
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
	Token   string `json:"token"`             // contract SQ: address
	To      string `json:"to,omitempty"`      // recipient (mint/transfer)
	Spender string `json:"spender,omitempty"` // approve
	Amount  string `json:"amount"`            // decimal string, smallest unit
}

func dispatchQRC20(stateDB *state.StateDB, ctx ModuleContext, action string, raw json.RawMessage, receipt *types.Receipt) error {
	caller, nonce := ctx.Caller, ctx.Nonce
	switch action {
	case "deploy":
		var p qrc20DeployParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return fmt.Errorf("qrc20 deploy: bad params: %w", err)
		}
		supply, err := parseBig(p.TotalSupply)
		if err != nil {
			return fmt.Errorf("qrc20 deploy: total_supply: %w", err)
		}
		maxSupply := big.NewInt(0)
		if p.MaxSupply != "" {
			if maxSupply, err = parseBig(p.MaxSupply); err != nil {
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
		amt, err := parseBig(p.Amount)
		if err != nil {
			return fmt.Errorf("qrc20 mint: amount: %w", err)
		}
		return NewQRC20Token(stateDB, token).MintWithEvents(caller, to, amt, receipt)

	case "transfer":
		p, token, err := parseTokenAction(raw)
		if err != nil {
			return err
		}
		to, err := qcrypto.AddressFromString(p.To)
		if err != nil {
			return fmt.Errorf("qrc20 transfer: to: %w", err)
		}
		amt, err := parseBig(p.Amount)
		if err != nil {
			return fmt.Errorf("qrc20 transfer: amount: %w", err)
		}
		return NewQRC20Token(stateDB, token).TransferWithEvents(caller, to, amt, receipt)

	case "approve":
		p, token, err := parseTokenAction(raw)
		if err != nil {
			return err
		}
		spender, err := qcrypto.AddressFromString(p.Spender)
		if err != nil {
			return fmt.Errorf("qrc20 approve: spender: %w", err)
		}
		amt, err := parseBig(p.Amount)
		if err != nil {
			return fmt.Errorf("qrc20 approve: amount: %w", err)
		}
		return NewQRC20Token(stateDB, token).ApproveWithEvents(caller, spender, amt, receipt)

	case "burn":
		p, token, err := parseTokenAction(raw)
		if err != nil {
			return err
		}
		amt, err := parseBig(p.Amount)
		if err != nil {
			return fmt.Errorf("qrc20 burn: amount: %w", err)
		}
		return NewQRC20Token(stateDB, token).BurnWithEvents(caller, amt, receipt)

	default:
		return fmt.Errorf("qrc20: unknown action %q", action)
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
		return fmt.Errorf("dex: unknown action %q", action)
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
