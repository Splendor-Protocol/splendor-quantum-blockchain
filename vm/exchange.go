// Package vm — exchange: the app-agnostic settlement module (exchange_v1).
//
// This is the chain's own prediction-market settlement surface, built on the
// Core Modules that stay routed on tip: collateral is any QRC-20 (Blockpeak's
// BPC), positions are a module-managed QRC-1155 collection. It is NOT a DEX,
// an AMM or a lending market — none of those are routed, and nothing here
// depends on them.
//
// The module is Polymarket-shaped and holds no market logic: it knows
// conditions (a collateral token, a resolver, an end time, two outcomes),
// complete sets (split collateral into YES+NO, merge them back), and the
// operator-submitted atomic settlement of orders users signed off-chain.
// Matching, pricing and market metadata live in an off-chain ledger; what
// reaches consensus is authorisation (a maker's ML-DSA signature over an
// order) and settlement (moving exactly what that order allows).
//
// Custody. The module holds user collateral only as the backing of minted
// position sets: pre-resolution supply(YES_c) == supply(NO_c) == locked_c
// and balance(exchange, collateral) >= Σ locked_c. Every other balance stays
// with its holder until a settlement the holder signed for moves it. There is
// no allowance and no approval: a valid signed order IS the authorisation,
// and the module moves the settled amounts from the maker directly.
//
// Positions are a QRC-1155 collection living AT the exchange address with
// owner == exchange, so it is module-managed: no key derives to the address,
// router mint/burn/transfer are unsatisfiable or refused, and only this file
// mutates the balances (mintUnchecked/burnUnchecked/transferUnchecked).
// Position id = condition_id*256 + outcome.
//
// Determinism. Nothing here reads the wall clock (block time arrives in
// ModuleContext), iterates a Go map, or formats a pointer; every error string
// is a function of the inputs and the state, prefixed "exchange <action>: ".
// Every loop is bounded by a wire cap (MaxExchangeMakers, MaxExchangeOperators)
// because module calls are unmetered.
//
// Failure. Every write action is atomic at the module level: it snapshots the
// StateDB and the receipt's log count on entry and reverts both on any error,
// so a failed match leaves no residue whether or not the executor's own
// module_revert_v1 snapshot is in force. All errors returned here are SOFT
// (a failed receipt under module_revert_v1); the HARD classes are the
// router's.
package vm

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

const (
	// ExchangePriceScale is the denominator of an order price: a price of
	// 600000 is 0.60 collateral per share. Prices are strictly inside
	// (0, ExchangePriceScale).
	ExchangePriceScale = 1_000_000
	// MaxExchangeMakers caps maker_orders in one match: 17 orders with fresh
	// public keys stay inside the mempool's data limit with room to spare.
	MaxExchangeMakers = 16
	// MaxExchangeRedeemFeeBps caps the winners-only redeem fee at 20%.
	MaxExchangeRedeemFeeBps = 2000
	// MaxExchangeOperators caps how many distinct addresses one instance may
	// ever list as an operator (active or not), so exchange_info's operator
	// walk is bounded.
	MaxExchangeOperators = 64
	// exchangeOutcomeCount is the only outcome count v1 accepts; the id layout
	// leaves room for up to 256.
	exchangeOutcomeCount = 2
	// exchangeOutcomesPerCondition is the stride of the position id layout.
	exchangeOutcomesPerCondition = 256
)

var (
	exchangePriceScale = big.NewInt(ExchangePriceScale)
	exchangeBpsScale   = big.NewInt(10_000)
)

// ── Storage slots ───────────────────────────────────────────────────────────
//
// All under the exchange address, next to the QRC-1155 collection's own
// "qrc1155:" slots (distinct prefixes, so SHA3 keeps them apart).

var (
	slotEXOwner          = qcrypto.Hash32([]byte("exchange:owner"))
	slotEXPaused         = qcrypto.Hash32([]byte("exchange:paused"))
	slotEXRedeemFeeBps   = qcrypto.Hash32([]byte("exchange:redeem_fee_bps"))
	slotEXFeeRecipient   = qcrypto.Hash32([]byte("exchange:fee_recipient"))
	slotEXConditionCount = qcrypto.Hash32([]byte("exchange:condition_count"))
	slotEXOperatorCount  = qcrypto.Hash32([]byte("exchange:operator_count"))
)

func exOperatorSlot(a qcrypto.Address) [32]byte {
	return qcrypto.Hash32(append([]byte("exchange:operator:"), a[:]...))
}

func exOperatorIndexSlot(a qcrypto.Address) [32]byte {
	return qcrypto.Hash32(append([]byte("exchange:operator_index:"), a[:]...))
}

func exOperatorListSlot(i uint64) [32]byte {
	return qcrypto.Hash32(append([]byte("exchange:operator_list:"), u64be(i)...))
}

func exConditionSlot(id uint64, field string) [32]byte {
	key := append([]byte("exchange:condition:"), u64be(id)...)
	key = append(key, ':')
	key = append(key, field...)
	return qcrypto.Hash32(key)
}

func exOrderFilledSlot(h [32]byte) [32]byte {
	return qcrypto.Hash32(append([]byte("exchange:order_filled:"), h[:]...))
}

func exOrderCancelledSlot(h [32]byte) [32]byte {
	return qcrypto.Hash32(append([]byte("exchange:order_cancelled:"), h[:]...))
}

func exNonceSlot(maker qcrypto.Address) [32]byte {
	return qcrypto.Hash32(append([]byte("exchange:nonce:"), maker[:]...))
}

// ExchangePositionID is the QRC-1155 token id of one outcome of a condition:
// condition*256 + outcome.
func ExchangePositionID(condition uint64, outcome uint8) *big.Int {
	id := new(big.Int).SetUint64(condition)
	id.Mul(id, big.NewInt(exchangeOutcomesPerCondition))
	return id.Add(id, big.NewInt(int64(outcome)))
}

// ExchangeAddress is where exchange.deploy by `deployer` at tx nonce `nonce`
// in the block at `height` lands: SHA3(deployer ‖ decimal(nonce) ‖ u64be(height)
// ‖ "exchange"). The height is inside so a re-genesis that keeps its chain id
// cannot accidentally resurrect old orders against a repeated address.
func ExchangeAddress(deployer qcrypto.Address, nonce, height uint64) qcrypto.Address {
	input := append(deployer[:], []byte(fmt.Sprintf("%d", nonce))...)
	input = append(input, u64be(height)...)
	input = append(input, []byte("exchange")...)
	h := qcrypto.Hash32(input)
	var a qcrypto.Address
	copy(a[:], h[:])
	return a
}

// ── Wire shapes ─────────────────────────────────────────────────────────────
//
// 32-byte fields are lowercase hex without 0x, exactly 64 chars. Amounts,
// prices and position ids are decimal strings through parseUint256. Condition
// ids, nonces and outcome/side are JSON integers (they are u64/u8 in the
// signed struct); expiration and end_time are JSON int64 unix seconds.
// signature / maker_pubkey are Go []byte, so standard base64 on the wire.

// exchangeDeployParams is the wire shape of exchange.deploy.
type exchangeDeployParams struct {
	Name         string `json:"name"`
	URIPrefix    string `json:"uri_prefix,omitempty"`
	RedeemFeeBps uint64 `json:"redeem_fee_bps,omitempty"` // default 0, ≤ MaxExchangeRedeemFeeBps
	FeeRecipient string `json:"fee_recipient,omitempty"`  // default deployer
}

// exchangeConditionParams is the wire shape of exchange.create_condition.
type exchangeConditionParams struct {
	Exchange     string `json:"exchange"`
	Collateral   string `json:"collateral"`
	Resolver     string `json:"resolver"`
	EndTime      int64  `json:"end_time"`
	OutcomeCount uint8  `json:"outcome_count"`
	QuestionHash string `json:"question_hash"` // 64 lowercase hex chars
}

// exchangeOrderWire is the signed order as carried inside match and
// cancel_order. signature and maker_pubkey may be omitted on an order the
// chain has already filled part of (its hash is authorised by that fill) and
// maker_pubkey may be omitted for a maker with a registered key.
type exchangeOrderWire struct {
	Salt        string `json:"salt"` // 64 lowercase hex chars
	Maker       string `json:"maker"`
	Exchange    string `json:"exchange"`
	Condition   uint64 `json:"condition"`
	Outcome     uint8  `json:"outcome"`
	Side        uint8  `json:"side"` // 0 buy, 1 sell
	ShareAmount string `json:"share_amount"`
	Price       string `json:"price"` // 1e6-scaled, in (0, 1e6)
	Expiration  int64  `json:"expiration"`
	Nonce       uint64 `json:"nonce"`
	Recipient   string `json:"recipient,omitempty"` // "" = the maker
	Signature   []byte `json:"signature,omitempty"`
	MakerPubKey []byte `json:"maker_pubkey,omitempty"`
}

// exchangeMatchParams is the wire shape of exchange.match: one taker order
// settled against up to MaxExchangeMakers maker orders, fill_shares[i] shares
// against maker i.
type exchangeMatchParams struct {
	Exchange    string              `json:"exchange"`
	TakerOrder  exchangeOrderWire   `json:"taker_order"`
	MakerOrders []exchangeOrderWire `json:"maker_orders"`
	FillShares  []string            `json:"fill_shares"`
}

// exchangeCancelParams is the wire shape of exchange.cancel_order. The order
// is cancelled by its maker, by an operator, or by anyone carrying the
// maker's signature (under CtxOrder) over types.CancelDigest(order hash) in
// cancel_signature. maker_pubkey falls back to order.maker_pubkey.
type exchangeCancelParams struct {
	Exchange        string            `json:"exchange"`
	Order           exchangeOrderWire `json:"order"`
	CancelSignature []byte            `json:"cancel_signature,omitempty"`
	MakerPubKey     []byte            `json:"maker_pubkey,omitempty"`
}

// exchangeNonceParams is the wire shape of exchange.increment_nonce: by the
// maker, or by anyone carrying the maker's signature (under CtxOrder) over
// types.IncrementNonceDigest(chain, exchange, maker, expected_nonce).
type exchangeNonceParams struct {
	Exchange      string `json:"exchange"`
	Maker         string `json:"maker"`
	ExpectedNonce uint64 `json:"expected_nonce"`
	Signature     []byte `json:"signature,omitempty"`
	MakerPubKey   []byte `json:"maker_pubkey,omitempty"`
}

// ── Exchange ────────────────────────────────────────────────────────────────

// Exchange is a handle to one deployed settlement instance.
type Exchange struct {
	stateDB *state.StateDB
	addr    qcrypto.Address
}

// NewExchange returns a handle to the instance at addr.
func NewExchange(stateDB *state.StateDB, addr qcrypto.Address) *Exchange {
	return &Exchange{stateDB: stateDB, addr: addr}
}

// Address is the instance address (also its position collection).
func (x *Exchange) Address() qcrypto.Address { return x.addr }

// positions is the module-managed QRC-1155 collection at the exchange address.
func (x *Exchange) positions() *QRC1155Token { return NewQRC1155Token(x.stateDB, x.addr) }

// DeployExchange creates an instance at ExchangeAddress(ctx.Caller, ctx.Nonce,
// ctx.Height): initialises the self-owned position collection (refusing an
// address that already has one), writes the owner and fee parameters, and
// marks the account as a contract. The deployer is the owner; operators are
// added afterwards with SetOperator.
func DeployExchange(stateDB *state.StateDB, ctx ModuleContext, p exchangeDeployParams) (qcrypto.Address, error) {
	const what = "exchange deploy: "
	if p.RedeemFeeBps > MaxExchangeRedeemFeeBps {
		return qcrypto.Address{}, fmt.Errorf(what+"redeem_fee_bps %d exceeds the cap of %d", p.RedeemFeeBps, MaxExchangeRedeemFeeBps)
	}
	if ctx.Caller.IsZero() {
		return qcrypto.Address{}, errors.New(what + "deployer is the zero address")
	}
	addr := ExchangeAddress(ctx.Caller, ctx.Nonce, ctx.Height)
	feeRecipient := ctx.Caller
	if p.FeeRecipient != "" {
		var err error
		if feeRecipient, err = qcrypto.AddressFromString(p.FeeRecipient); err != nil {
			return qcrypto.Address{}, fmt.Errorf(what+"fee_recipient: %w", err)
		}
	}
	if err := checkFeeRecipient(what, addr, feeRecipient); err != nil {
		return qcrypto.Address{}, err
	}
	x := NewExchange(stateDB, addr)
	if err := x.atomic(nil, func() error {
		// owner == addr: module-managed, so no key can mint or move positions.
		if err := initQRC1155At(stateDB, addr, addr, p.Name, p.URIPrefix); err != nil {
			return fmt.Errorf(what+"%w", err)
		}
		if err := x.setAddr(slotEXOwner, ctx.Caller); err != nil {
			return err
		}
		if err := x.setU64(slotEXRedeemFeeBps, p.RedeemFeeBps); err != nil {
			return err
		}
		if err := x.setAddr(slotEXFeeRecipient, feeRecipient); err != nil {
			return err
		}
		markContract(stateDB, addr)
		return nil
	}); err != nil {
		return qcrypto.Address{}, err
	}
	return addr, nil
}

func checkFeeRecipient(what string, exchange, recipient qcrypto.Address) error {
	if recipient.IsZero() {
		return errors.New(what + "fee_recipient is the zero address")
	}
	if recipient == exchange {
		return errors.New(what + "fee_recipient cannot be the exchange itself")
	}
	return nil
}

// ── Instance state ──────────────────────────────────────────────────────────

// Owner returns the instance owner (zero when nothing is deployed at addr).
func (x *Exchange) Owner() (qcrypto.Address, error) { return x.getAddr(slotEXOwner) }

// IsPaused reports whether match and split are blocked.
func (x *Exchange) IsPaused() (bool, error) { return x.getFlag(slotEXPaused) }

// RedeemFeeBps returns the winners-only redeem fee in basis points.
func (x *Exchange) RedeemFeeBps() (uint64, error) { return x.getU64(slotEXRedeemFeeBps) }

// FeeRecipient returns where redeem fees go.
func (x *Exchange) FeeRecipient() (qcrypto.Address, error) { return x.getAddr(slotEXFeeRecipient) }

// ConditionCount returns how many conditions exist; ids run 1..count.
func (x *Exchange) ConditionCount() (uint64, error) { return x.getU64(slotEXConditionCount) }

// IsOperator reports whether addr may submit matches and create conditions.
func (x *Exchange) IsOperator(addr qcrypto.Address) (bool, error) {
	return x.getFlag(exOperatorSlot(addr))
}

// Operators lists the active operators in the order they were first added.
func (x *Exchange) Operators() ([]qcrypto.Address, error) {
	n, err := x.getU64(slotEXOperatorCount)
	if err != nil {
		return nil, err
	}
	out := []qcrypto.Address{}
	for i := uint64(0); i < n && i < MaxExchangeOperators; i++ {
		a, err := x.getAddr(exOperatorListSlot(i))
		if err != nil {
			return nil, err
		}
		active, err := x.IsOperator(a)
		if err != nil {
			return nil, err
		}
		if active {
			out = append(out, a)
		}
	}
	return out, nil
}

// MakerNonce returns the order nonce a maker must sign with at this instance.
func (x *Exchange) MakerNonce(maker qcrypto.Address) (uint64, error) {
	return x.getU64(exNonceSlot(maker))
}

// OrderFilled returns how many shares of the order with this hash have been
// settled at this instance.
func (x *Exchange) OrderFilled(hash [32]byte) (*big.Int, error) {
	return x.getBig(exOrderFilledSlot(hash))
}

// OrderCancelled reports whether the order with this hash is cancelled.
func (x *Exchange) OrderCancelled(hash [32]byte) (bool, error) {
	return x.getFlag(exOrderCancelledSlot(hash))
}

// ExchangeInfo is the exchange_info read result.
type ExchangeInfo struct {
	Address        string   `json:"address"`
	Owner          string   `json:"owner"`
	Operators      []string `json:"operators"`
	Paused         bool     `json:"paused"`
	RedeemFeeBps   uint64   `json:"redeem_fee_bps"`
	FeeRecipient   string   `json:"fee_recipient"`
	ConditionCount uint64   `json:"condition_count"`
}

// Info returns the instance parameters.
func (x *Exchange) Info() (*ExchangeInfo, error) {
	owner, err := x.Owner()
	if err != nil {
		return nil, err
	}
	ops, err := x.Operators()
	if err != nil {
		return nil, err
	}
	paused, err := x.IsPaused()
	if err != nil {
		return nil, err
	}
	bps, err := x.RedeemFeeBps()
	if err != nil {
		return nil, err
	}
	feeTo, err := x.FeeRecipient()
	if err != nil {
		return nil, err
	}
	count, err := x.ConditionCount()
	if err != nil {
		return nil, err
	}
	opStrs := make([]string, len(ops))
	for i, a := range ops {
		opStrs[i] = a.String()
	}
	return &ExchangeInfo{
		Address: x.addr.String(), Owner: owner.String(), Operators: opStrs, Paused: paused,
		RedeemFeeBps: bps, FeeRecipient: feeTo.String(), ConditionCount: count,
	}, nil
}

// requireDeployed refuses a handle to an address nothing was deployed at.
func (x *Exchange) requireDeployed(what string) error {
	owner, err := x.Owner()
	if err != nil {
		return err
	}
	if owner.IsZero() {
		return fmt.Errorf("%sno exchange is deployed at %s", what, x.addr)
	}
	return nil
}

func (x *Exchange) requireOwner(what string, caller qcrypto.Address) error {
	if err := x.requireDeployed(what); err != nil {
		return err
	}
	owner, err := x.Owner()
	if err != nil {
		return err
	}
	if caller != owner {
		return fmt.Errorf("%scaller %s is not the owner", what, caller)
	}
	return nil
}

func (x *Exchange) requireOperator(what string, caller qcrypto.Address) error {
	if err := x.requireDeployed(what); err != nil {
		return err
	}
	ok, err := x.IsOperator(caller)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%scaller %s is not an operator", what, caller)
	}
	return nil
}

func (x *Exchange) requireOwnerOrOperator(what string, caller qcrypto.Address) error {
	if err := x.requireDeployed(what); err != nil {
		return err
	}
	owner, err := x.Owner()
	if err != nil {
		return err
	}
	if caller == owner {
		return nil
	}
	ok, err := x.IsOperator(caller)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%scaller %s is neither the owner nor an operator", what, caller)
	}
	return nil
}

func (x *Exchange) requireNotPaused(what string) error {
	paused, err := x.IsPaused()
	if err != nil {
		return err
	}
	if paused {
		return errors.New(what + "exchange is paused")
	}
	return nil
}

// ── Owner controls ──────────────────────────────────────────────────────────

// Pause blocks match and split. cancel_order, increment_nonce, merge and
// redeem are never gated by the exchange. Owner-only.
func (x *Exchange) Pause(caller qcrypto.Address, receipt *types.Receipt) error {
	const what = "exchange pause: "
	if err := x.requireOwner(what, caller); err != nil {
		return err
	}
	return x.atomic(receipt, func() error {
		if err := x.setFlag(slotEXPaused, true); err != nil {
			return err
		}
		x.log(receipt, PausedEvent(x.addr, caller))
		return nil
	})
}

// Unpause lifts Pause. Owner-only.
func (x *Exchange) Unpause(caller qcrypto.Address, receipt *types.Receipt) error {
	const what = "exchange unpause: "
	if err := x.requireOwner(what, caller); err != nil {
		return err
	}
	return x.atomic(receipt, func() error {
		if err := x.setFlag(slotEXPaused, false); err != nil {
			return err
		}
		x.log(receipt, UnpausedEvent(x.addr, caller))
		return nil
	})
}

// SetRedeemFeeBps sets the winners-only redeem fee (≤ MaxExchangeRedeemFeeBps).
// Owner-only.
func (x *Exchange) SetRedeemFeeBps(caller qcrypto.Address, bps uint64, receipt *types.Receipt) error {
	const what = "exchange set_redeem_fee_bps: "
	if err := x.requireOwner(what, caller); err != nil {
		return err
	}
	if bps > MaxExchangeRedeemFeeBps {
		return fmt.Errorf(what+"redeem_fee_bps %d exceeds the cap of %d", bps, MaxExchangeRedeemFeeBps)
	}
	return x.atomic(receipt, func() error {
		if err := x.setU64(slotEXRedeemFeeBps, bps); err != nil {
			return err
		}
		x.log(receipt, RedeemFeeUpdatedEvent(x.addr, bps))
		return nil
	})
}

// SetFeeRecipient changes where redeem fees go (non-zero, not the exchange).
// Owner-only.
func (x *Exchange) SetFeeRecipient(caller, recipient qcrypto.Address, receipt *types.Receipt) error {
	const what = "exchange set_fee_recipient: "
	if err := x.requireOwner(what, caller); err != nil {
		return err
	}
	if err := checkFeeRecipient(what, x.addr, recipient); err != nil {
		return err
	}
	return x.atomic(receipt, func() error {
		if err := x.setAddr(slotEXFeeRecipient, recipient); err != nil {
			return err
		}
		x.log(receipt, FeeRecipientUpdatedEvent(x.addr, recipient))
		return nil
	})
}

// SetOperator grants or revokes the right to submit matches and create
// conditions. An address is appended to the bounded operator list the first
// time it is GRANTED; revoking keeps its slot so the list never reorders, and
// re-granting a revoked address reuses that slot. Revoking an address that
// was never listed is a no-op: nothing is written and no event is emitted,
// so a stream of revokes cannot fill the list (the cap counts addresses that
// have been granted at least once). Owner-only.
func (x *Exchange) SetOperator(caller, operator qcrypto.Address, active bool, receipt *types.Receipt) error {
	const what = "exchange set_operator: "
	if err := x.requireOwner(what, caller); err != nil {
		return err
	}
	if operator.IsZero() {
		return errors.New(what + "operator is the zero address")
	}
	if operator == x.addr || operator == ModuleRouterAddress {
		return fmt.Errorf(what+"operator %s cannot be a module address", operator)
	}
	return x.atomic(receipt, func() error {
		idx, err := x.getU64(exOperatorIndexSlot(operator))
		if err != nil {
			return err
		}
		if idx == 0 && !active {
			return nil
		}
		if idx == 0 {
			n, err := x.getU64(slotEXOperatorCount)
			if err != nil {
				return err
			}
			if n >= MaxExchangeOperators {
				return fmt.Errorf(what+"operator list is full (%d)", MaxExchangeOperators)
			}
			if err := x.setAddr(exOperatorListSlot(n), operator); err != nil {
				return err
			}
			if err := x.setU64(exOperatorIndexSlot(operator), n+1); err != nil {
				return err
			}
			if err := x.setU64(slotEXOperatorCount, n+1); err != nil {
				return err
			}
		}
		if err := x.setFlag(exOperatorSlot(operator), active); err != nil {
			return err
		}
		x.log(receipt, OperatorSetEvent(x.addr, operator, active))
		return nil
	})
}

// TransferOwnership hands the instance to newOwner (non-zero, not the
// instance, the router or any deployed contract — none of those can ever
// sign an owner action, so the transfer would only orphan the instance).
// Single-step by design. Owner-only.
func (x *Exchange) TransferOwnership(caller, newOwner qcrypto.Address, receipt *types.Receipt) error {
	const what = "exchange transfer_ownership: "
	if err := x.requireOwner(what, caller); err != nil {
		return err
	}
	if newOwner.IsZero() {
		return errors.New(what + "new_owner is the zero address")
	}
	if newOwner == x.addr || newOwner == ModuleRouterAddress {
		return fmt.Errorf(what+"new_owner %s cannot be a module address", newOwner)
	}
	acc, err := x.stateDB.GetAccount(newOwner)
	if err != nil {
		return fmt.Errorf(what+"new_owner %s: %w", newOwner, err)
	}
	if acc.IsContract {
		return fmt.Errorf(what+"new_owner %s is a contract and can never act as owner", newOwner)
	}
	return x.atomic(receipt, func() error {
		if err := x.setAddr(slotEXOwner, newOwner); err != nil {
			return err
		}
		x.log(receipt, OwnershipTransferredEvent(x.addr, caller, newOwner))
		return nil
	})
}

// ── Conditions ──────────────────────────────────────────────────────────────

// ExchangeCondition is one condition's on-chain record.
type ExchangeCondition struct {
	ID             uint64
	Collateral     qcrypto.Address
	Resolver       qcrypto.Address
	EndTime        int64
	OutcomeCount   uint8
	QuestionHash   [32]byte
	Resolved       bool
	WinningOutcome uint8
	// Locked is the collateral backing this condition's outstanding sets.
	Locked *big.Int
}

// Condition loads condition id; ids run 1..ConditionCount.
func (x *Exchange) Condition(id uint64) (*ExchangeCondition, error) {
	count, err := x.ConditionCount()
	if err != nil {
		return nil, err
	}
	if id == 0 || id > count {
		return nil, fmt.Errorf("condition %d does not exist", id)
	}
	c := &ExchangeCondition{ID: id}
	if c.Collateral, err = x.getAddr(exConditionSlot(id, "collateral")); err != nil {
		return nil, err
	}
	if c.Resolver, err = x.getAddr(exConditionSlot(id, "resolver")); err != nil {
		return nil, err
	}
	endTime, err := x.getU64(exConditionSlot(id, "end_time"))
	if err != nil {
		return nil, err
	}
	c.EndTime = int64(endTime)
	outcomeCount, err := x.getU64(exConditionSlot(id, "outcome_count"))
	if err != nil {
		return nil, err
	}
	c.OutcomeCount = uint8(outcomeCount)
	qh, err := x.stateDB.GetStorageValue(x.addr, exConditionSlot(id, "question_hash"))
	if err != nil {
		return nil, err
	}
	copy(c.QuestionHash[:], qh)
	if c.Resolved, err = x.getFlag(exConditionSlot(id, "resolved")); err != nil {
		return nil, err
	}
	win, err := x.getU64(exConditionSlot(id, "winning_outcome"))
	if err != nil {
		return nil, err
	}
	c.WinningOutcome = uint8(win)
	if c.Locked, err = x.getBig(exConditionSlot(id, "locked")); err != nil {
		return nil, err
	}
	return c, nil
}

func (x *Exchange) setLocked(id uint64, v *big.Int) error {
	return x.setBig(exConditionSlot(id, "locked"), v)
}

// CreateCondition registers a new condition and returns its id. Owner or
// operator only; end_time must be after the block time, the resolver
// non-zero, outcome_count exactly 2 (v1), and the collateral a deployed
// contract other than the exchange.
func (x *Exchange) CreateCondition(ctx ModuleContext, p exchangeConditionParams, receipt *types.Receipt) (uint64, error) {
	const what = "exchange create_condition: "
	if err := x.requireOwnerOrOperator(what, ctx.Caller); err != nil {
		return 0, err
	}
	collateral, err := qcrypto.AddressFromString(p.Collateral)
	if err != nil {
		return 0, fmt.Errorf(what+"collateral: %w", err)
	}
	resolver, err := qcrypto.AddressFromString(p.Resolver)
	if err != nil {
		return 0, fmt.Errorf(what+"resolver: %w", err)
	}
	questionHash, err := parseHex32(p.QuestionHash)
	if err != nil {
		return 0, fmt.Errorf(what+"question_hash: %w", err)
	}
	if p.OutcomeCount != exchangeOutcomeCount {
		return 0, fmt.Errorf(what+"outcome_count must be %d in v1, got %d", exchangeOutcomeCount, p.OutcomeCount)
	}
	if p.EndTime <= ctx.Time {
		return 0, fmt.Errorf(what+"end_time %d is not after the block time %d", p.EndTime, ctx.Time)
	}
	if resolver.IsZero() {
		return 0, errors.New(what + "resolver is the zero address")
	}
	if collateral == x.addr {
		return 0, errors.New(what + "collateral cannot be the exchange itself")
	}
	acc, err := x.stateDB.GetAccount(collateral)
	if err != nil {
		return 0, fmt.Errorf(what+"collateral %s: %w", collateral, err)
	}
	if !acc.IsContract {
		return 0, fmt.Errorf(what+"collateral %s is not a deployed contract", collateral)
	}
	var id uint64
	err = x.atomic(receipt, func() error {
		count, err := x.ConditionCount()
		if err != nil {
			return err
		}
		id = count + 1
		if err := x.setU64(slotEXConditionCount, id); err != nil {
			return err
		}
		if err := x.setAddr(exConditionSlot(id, "collateral"), collateral); err != nil {
			return err
		}
		if err := x.setAddr(exConditionSlot(id, "resolver"), resolver); err != nil {
			return err
		}
		if err := x.setU64(exConditionSlot(id, "end_time"), uint64(p.EndTime)); err != nil {
			return err
		}
		if err := x.setU64(exConditionSlot(id, "outcome_count"), uint64(p.OutcomeCount)); err != nil {
			return err
		}
		if err := x.stateDB.SetStorageValue(x.addr, exConditionSlot(id, "question_hash"), questionHash[:]); err != nil {
			return err
		}
		x.log(receipt, ConditionCreatedEvent(x.addr, id, collateral, resolver, p.EndTime, p.OutcomeCount, questionHash))
		return nil
	})
	return id, err
}

// Resolve records the winning outcome. Resolver-only, once.
func (x *Exchange) Resolve(ctx ModuleContext, condition uint64, outcome uint8, receipt *types.Receipt) error {
	const what = "exchange resolve: "
	if err := x.requireDeployed(what); err != nil {
		return err
	}
	c, err := x.Condition(condition)
	if err != nil {
		return fmt.Errorf(what+"%w", err)
	}
	if ctx.Caller != c.Resolver {
		return fmt.Errorf(what+"caller %s is not the resolver of condition %d", ctx.Caller, condition)
	}
	if c.Resolved {
		return fmt.Errorf(what+"condition %d is already resolved", condition)
	}
	if outcome >= c.OutcomeCount {
		return fmt.Errorf(what+"outcome %d is out of range for condition %d (%d outcomes)", outcome, condition, c.OutcomeCount)
	}
	return x.atomic(receipt, func() error {
		if err := x.setU64(exConditionSlot(condition, "winning_outcome"), uint64(outcome)); err != nil {
			return err
		}
		if err := x.setFlag(exConditionSlot(condition, "resolved"), true); err != nil {
			return err
		}
		x.log(receipt, ResolvedEvent(x.addr, condition, outcome))
		return nil
	})
}

// ── Complete sets ───────────────────────────────────────────────────────────

// Split locks `amount` collateral from the caller and mints `amount` of every
// outcome to it. Blocked while paused, at or after end_time, and once
// resolved.
func (x *Exchange) Split(ctx ModuleContext, condition uint64, amount *big.Int, receipt *types.Receipt) error {
	const what = "exchange split: "
	if err := x.requireDeployed(what); err != nil {
		return err
	}
	if err := x.requireNotPaused(what); err != nil {
		return err
	}
	if err := checkPositiveAmount(what, amount); err != nil {
		return err
	}
	c, err := x.Condition(condition)
	if err != nil {
		return fmt.Errorf(what+"%w", err)
	}
	if c.Resolved {
		return fmt.Errorf(what+"condition %d is resolved", condition)
	}
	if ctx.Time >= c.EndTime {
		return fmt.Errorf(what+"condition %d has ended (end_time %d, block time %d)", condition, c.EndTime, ctx.Time)
	}
	return x.atomic(receipt, func() error {
		if err := NewQRC20Token(x.stateDB, c.Collateral).TransferWithEvents(ctx.Caller, x.addr, amount, receipt); err != nil {
			return fmt.Errorf(what+"collateral: %w", err)
		}
		if err := x.mintSet(c, ctx.Caller, amount, receipt); err != nil {
			return fmt.Errorf(what+"%w", err)
		}
		if err := x.setLocked(condition, new(big.Int).Add(c.Locked, amount)); err != nil {
			return err
		}
		x.log(receipt, PositionSplitEvent(x.addr, ctx.Caller, condition, amount))
		return nil
	})
}

// Merge burns `amount` of every outcome from the caller and releases `amount`
// collateral to it. Blocked only once resolved (never by pause or end_time).
func (x *Exchange) Merge(ctx ModuleContext, condition uint64, amount *big.Int, receipt *types.Receipt) error {
	const what = "exchange merge: "
	if err := x.requireDeployed(what); err != nil {
		return err
	}
	if err := checkPositiveAmount(what, amount); err != nil {
		return err
	}
	c, err := x.Condition(condition)
	if err != nil {
		return fmt.Errorf(what+"%w", err)
	}
	if c.Resolved {
		return fmt.Errorf(what+"condition %d is resolved", condition)
	}
	return x.atomic(receipt, func() error {
		if err := x.burnSet(c, ctx.Caller, amount, receipt); err != nil {
			return fmt.Errorf(what+"%w", err)
		}
		if err := x.release(what, c, amount); err != nil {
			return err
		}
		if err := NewQRC20Token(x.stateDB, c.Collateral).TransferWithEvents(x.addr, ctx.Caller, amount, receipt); err != nil {
			return fmt.Errorf(what+"collateral: %w", err)
		}
		x.log(receipt, PositionsMergedEvent(x.addr, ctx.Caller, condition, amount))
		return nil
	})
}

// mintSet mints amount of every outcome of c to holder (operator = exchange).
func (x *Exchange) mintSet(c *ExchangeCondition, holder qcrypto.Address, amount *big.Int, receipt *types.Receipt) error {
	pos := x.positions()
	for o := uint8(0); o < c.OutcomeCount; o++ {
		if err := pos.mintUnchecked(x.addr, holder, ExchangePositionID(c.ID, o), amount, "", receipt); err != nil {
			return fmt.Errorf("outcome %d: %w", o, err)
		}
	}
	return nil
}

// burnSet burns amount of every outcome of c from holder, checking every
// balance first so a short leg fails before anything moves.
func (x *Exchange) burnSet(c *ExchangeCondition, holder qcrypto.Address, amount *big.Int, receipt *types.Receipt) error {
	pos := x.positions()
	for o := uint8(0); o < c.OutcomeCount; o++ {
		if err := x.requirePosition(pos, holder, c.ID, o, amount); err != nil {
			return err
		}
	}
	for o := uint8(0); o < c.OutcomeCount; o++ {
		if err := pos.burnUnchecked(x.addr, holder, ExchangePositionID(c.ID, o), amount, receipt); err != nil {
			return fmt.Errorf("outcome %d: %w", o, err)
		}
	}
	return nil
}

func (x *Exchange) requirePosition(pos *QRC1155Token, holder qcrypto.Address, condition uint64, outcome uint8, amount *big.Int) error {
	bal, err := pos.BalanceOf(holder, ExchangePositionID(condition, outcome))
	if err != nil {
		return err
	}
	if bal.Cmp(amount) < 0 {
		return fmt.Errorf("%s holds %s of outcome %d, needs %s", holder, bal, outcome, amount)
	}
	return nil
}

// release lowers a condition's locked collateral, refusing to go below zero:
// that would mean the module is paying out more than it ever locked.
func (x *Exchange) release(what string, c *ExchangeCondition, amount *big.Int) error {
	if c.Locked.Cmp(amount) < 0 {
		return fmt.Errorf("%scondition %d has %s locked, cannot release %s", what, c.ID, c.Locked, amount)
	}
	c.Locked = new(big.Int).Sub(c.Locked, amount)
	return x.setLocked(c.ID, c.Locked)
}

// ── Redemption ──────────────────────────────────────────────────────────────

// Redeem burns every winning share `holder` has and pays it
// gross − floor(gross·bps/10000), the fee going to the fee recipient. Anyone
// may trigger it for any holder; the holder is always the one paid. A holder
// with no winning shares errors. Never gated by pause.
func (x *Exchange) Redeem(ctx ModuleContext, condition uint64, holder qcrypto.Address, receipt *types.Receipt) error {
	const what = "exchange redeem: "
	if err := x.requireDeployed(what); err != nil {
		return err
	}
	if holder.IsZero() {
		return errors.New(what + "holder is the zero address")
	}
	c, err := x.Condition(condition)
	if err != nil {
		return fmt.Errorf(what+"%w", err)
	}
	if !c.Resolved {
		return fmt.Errorf(what+"condition %d is not resolved", condition)
	}
	pos := x.positions()
	winID := ExchangePositionID(c.ID, c.WinningOutcome)
	gross, err := pos.BalanceOf(holder, winID)
	if err != nil {
		return err
	}
	if gross.Sign() == 0 {
		return fmt.Errorf(what+"%s holds no shares of the winning outcome %d of condition %d", holder, c.WinningOutcome, condition)
	}
	bps, err := x.RedeemFeeBps()
	if err != nil {
		return err
	}
	feeTo, err := x.FeeRecipient()
	if err != nil {
		return err
	}
	fee := new(big.Int).Mul(gross, new(big.Int).SetUint64(bps))
	fee.Quo(fee, exchangeBpsScale)
	payout := new(big.Int).Sub(gross, fee)
	return x.atomic(receipt, func() error {
		if err := pos.burnUnchecked(x.addr, holder, winID, gross, receipt); err != nil {
			return fmt.Errorf(what+"%w", err)
		}
		if err := x.release(what, c, gross); err != nil {
			return err
		}
		tok := NewQRC20Token(x.stateDB, c.Collateral)
		if err := tok.TransferWithEvents(x.addr, holder, payout, receipt); err != nil {
			return fmt.Errorf(what+"collateral: %w", err)
		}
		if fee.Sign() > 0 {
			if err := tok.TransferWithEvents(x.addr, feeTo, fee, receipt); err != nil {
				return fmt.Errorf(what+"fee: %w", err)
			}
			x.log(receipt, RedeemFeeChargedEvent(x.addr, feeTo, fee))
		}
		x.log(receipt, RedeemedEvent(x.addr, holder, condition, gross, payout))
		return nil
	})
}

// ── Orders ──────────────────────────────────────────────────────────────────

// exchangeOrder is a parsed, statically valid order with its hash and its
// effective payee.
type exchangeOrder struct {
	types.Order
	hash   [32]byte
	sig    []byte
	pubKey []byte
	payTo  qcrypto.Address // Recipient, or Maker when Recipient is zero
}

// parseOrder decodes a wire order for this instance and checks everything
// that needs no state: field ranges, the exchange binding, the recipient
// rule, and the hash. `what` prefixes every error.
func (x *Exchange) parseOrder(what string, w exchangeOrderWire) (*exchangeOrder, error) {
	salt, err := parseHex32(w.Salt)
	if err != nil {
		return nil, fmt.Errorf("%ssalt: %w", what, err)
	}
	maker, err := qcrypto.AddressFromString(w.Maker)
	if err != nil {
		return nil, fmt.Errorf("%smaker: %w", what, err)
	}
	if maker.IsZero() || maker == x.addr || maker == ModuleRouterAddress {
		return nil, fmt.Errorf("%smaker %s cannot sign orders", what, maker)
	}
	exchange, err := qcrypto.AddressFromString(w.Exchange)
	if err != nil {
		return nil, fmt.Errorf("%sexchange: %w", what, err)
	}
	if exchange != x.addr {
		return nil, fmt.Errorf("%sorder is for exchange %s, not %s", what, exchange, x.addr)
	}
	if w.Side != types.OrderSideBuy && w.Side != types.OrderSideSell {
		return nil, fmt.Errorf("%sside must be %d (buy) or %d (sell), got %d", what, types.OrderSideBuy, types.OrderSideSell, w.Side)
	}
	shares, err := parseUint256(w.ShareAmount)
	if err != nil {
		return nil, fmt.Errorf("%sshare_amount: %w", what, err)
	}
	if shares.Sign() == 0 {
		return nil, fmt.Errorf("%sshare_amount must be positive", what)
	}
	price, err := parseUint256(w.Price)
	if err != nil {
		return nil, fmt.Errorf("%sprice: %w", what, err)
	}
	if price.Sign() == 0 || price.Cmp(exchangePriceScale) >= 0 {
		return nil, fmt.Errorf("%sprice must be in (0, %d), got %s", what, ExchangePriceScale, price)
	}
	if w.Expiration < 0 {
		return nil, fmt.Errorf("%sexpiration must not be negative", what)
	}
	var recipient qcrypto.Address
	if w.Recipient != "" {
		if recipient, err = qcrypto.AddressFromString(w.Recipient); err != nil {
			return nil, fmt.Errorf("%srecipient: %w", what, err)
		}
	}
	if recipient == x.addr || recipient == ModuleRouterAddress {
		return nil, fmt.Errorf("%srecipient %s is a module address; funds sent there are stranded", what, recipient)
	}
	o := &exchangeOrder{
		Order: types.Order{
			Salt: salt, Maker: maker, Exchange: x.addr, Condition: w.Condition,
			Outcome: w.Outcome, Side: w.Side, ShareAmount: shares, Price: price,
			Expiration: w.Expiration, Nonce: w.Nonce, Recipient: recipient,
		},
		sig: w.Signature, pubKey: w.MakerPubKey, payTo: recipient,
	}
	if o.payTo.IsZero() {
		o.payTo = maker
	}
	if o.hash, err = types.OrderDigest(types.ChainID, o.Order); err != nil {
		return nil, fmt.Errorf("%s%w", what, err)
	}
	return o, nil
}

// OrderHash is the hash the order is tracked by at this instance: its digest
// computed with this exchange's address, whatever the wire says. Pure.
func (x *Exchange) OrderHash(w exchangeOrderWire) ([32]byte, error) {
	o, err := x.parseOrder("", w)
	if err != nil {
		return [32]byte{}, err
	}
	return o.hash, nil
}

// checkOrderState runs the per-order consensus checks that read state:
// cancelled, expired, nonce, and the fill so far. It returns the shares
// still open and how much has been filled (the signature short-cut key).
func (x *Exchange) checkOrderState(what string, ctx ModuleContext, o *exchangeOrder) (remaining, filled *big.Int, err error) {
	cancelled, err := x.OrderCancelled(o.hash)
	if err != nil {
		return nil, nil, err
	}
	if cancelled {
		return nil, nil, fmt.Errorf("%sorder %x is cancelled", what, o.hash)
	}
	if o.Expiration != 0 && ctx.Time > o.Expiration {
		return nil, nil, fmt.Errorf("%sorder %x expired at %d (block time %d)", what, o.hash, o.Expiration, ctx.Time)
	}
	nonce, err := x.MakerNonce(o.Maker)
	if err != nil {
		return nil, nil, err
	}
	if o.Nonce != nonce {
		return nil, nil, fmt.Errorf("%sorder %x carries nonce %d but maker %s is at nonce %d", what, o.hash, o.Nonce, o.Maker, nonce)
	}
	filled, err = x.OrderFilled(o.hash)
	if err != nil {
		return nil, nil, err
	}
	if filled.Cmp(o.ShareAmount) >= 0 {
		return nil, nil, fmt.Errorf("%sorder %x is fully filled", what, o.hash)
	}
	return new(big.Int).Sub(o.ShareAmount, filled), filled, nil
}

// verifyOrder checks the maker's signature over the order digest under
// CtxOrder, resolving the key registry-first.
func (x *Exchange) verifyOrder(what string, o *exchangeOrder) error {
	pub, err := x.resolveSigner(what, o.Maker, o.pubKey)
	if err != nil {
		return err
	}
	if !qcrypto.Verify(pub, o.hash[:], qcrypto.CtxOrder, o.sig) {
		return fmt.Errorf("%sorder %x: invalid maker signature", what, o.hash)
	}
	return nil
}

// resolveSigner returns the key that speaks for `signer`: the registered one
// when there is one (supplied is then ignored), otherwise the supplied key
// iff it derives the address. Never writes the registry.
func (x *Exchange) resolveSigner(what string, signer qcrypto.Address, supplied []byte) (*qcrypto.PublicKey, error) {
	registered, err := x.stateDB.GetPublicKey(signer)
	switch {
	case err == nil:
		pub, err := qcrypto.PublicKeyFromBytes(registered)
		if err != nil {
			return nil, fmt.Errorf("%sregistered key of %s: %w", what, signer, err)
		}
		return pub, nil
	case errors.Is(err, types.ErrNoPublicKey):
		if len(supplied) == 0 {
			return nil, fmt.Errorf("%s%s has no registered key and no maker_pubkey was supplied", what, signer)
		}
		pub, err := qcrypto.PublicKeyFromBytes(supplied)
		if err != nil {
			return nil, fmt.Errorf("%smaker_pubkey for %s is invalid: %w", what, signer, err)
		}
		if qcrypto.DeriveAddress(pub) != signer {
			return nil, fmt.Errorf("%smaker_pubkey does not derive %s", what, signer)
		}
		return pub, nil
	default:
		return nil, fmt.Errorf("%skey lookup for %s: %w", what, signer, err)
	}
}

// ── Matching ────────────────────────────────────────────────────────────────

// matchKind is how one (taker, maker) pair settles.
type matchKind uint8

const (
	// matchComplementary: a buy against a sell of the same outcome; shares
	// move seller → buyer, collateral buyer → seller.
	matchComplementary matchKind = iota
	// matchMint: two buys of different outcomes whose prices sum to at least
	// 1; both pay, a set is minted, each gets its outcome.
	matchMint
	// matchMerge: two sells of different outcomes whose prices sum to at most
	// 1; both deliver, the set is burned, each is paid.
	matchMerge
)

// orderTerms is the part of an order the settlement arithmetic needs.
type orderTerms struct {
	Side    uint8
	Outcome uint8
	Price   *big.Int
}

// pairSettlement is what one (taker, maker) pair moves. makerCost is the
// collateral the maker pays (BUY) or receives (SELL); takerCost likewise for
// the taker. For mint and merge makerCost + takerCost == fill exactly.
type pairSettlement struct {
	kind      matchKind
	fill      *big.Int
	makerCost *big.Int
	takerCost *big.Int
}

// mulDivFloor is floor(price·fill / 1e6); mulDivCeil rounds up.
func mulDivFloor(price, fill *big.Int) *big.Int {
	v := new(big.Int).Mul(price, fill)
	return v.Quo(v, exchangePriceScale)
}

func mulDivCeil(price, fill *big.Int) *big.Int {
	v := new(big.Int).Mul(price, fill)
	v.Add(v, new(big.Int).Sub(exchangePriceScale, big.NewInt(1)))
	return v.Quo(v, exchangePriceScale)
}

// settlePair is the pricing rule, pure: settle at the MAKER's signed price,
// rounded in favour of the maker's bound (maker SELL → ceil, maker BUY →
// floor); the taker pays or receives the complement. The taker's bound is
// checked with a ≤1-unit rounding tolerance — a taker BUY pays at most
// ceil(p_t·fill/1e6), a taker SELL receives at least floor(p_t·fill/1e6) —
// so an equal-price cross and an exactly-crossing mint/merge fill at any
// size. Every collateral leg must be non-zero: module calls are unmetered,
// so a free dust fill has no gas deterrent here.
func settlePair(taker, maker orderTerms, fill *big.Int) (pairSettlement, error) {
	s := pairSettlement{fill: fill}
	if fill == nil || fill.Sign() <= 0 {
		return s, errors.New("fill must be positive")
	}
	switch {
	case taker.Outcome == maker.Outcome:
		if taker.Side == maker.Side {
			return s, errors.New("same side and outcome do not cross")
		}
		s.kind = matchComplementary
		if maker.Side == types.OrderSideSell {
			// Taker buys at ≥ the maker's ask; pays the ask, rounded up.
			if taker.Price.Cmp(maker.Price) < 0 {
				return s, fmt.Errorf("taker bid %s is below maker ask %s", taker.Price, maker.Price)
			}
			cost := mulDivCeil(maker.Price, fill)
			if cost.Sign() == 0 {
				return s, errors.New("zero-cost fill")
			}
			if cost.Cmp(mulDivCeil(taker.Price, fill)) > 0 {
				return s, fmt.Errorf("cost %s exceeds the taker's bound", cost)
			}
			s.makerCost, s.takerCost = cost, cost
			return s, nil
		}
		// Maker buys at ≥ the taker's ask; pays its bid, rounded down.
		if maker.Price.Cmp(taker.Price) < 0 {
			return s, fmt.Errorf("maker bid %s is below taker ask %s", maker.Price, taker.Price)
		}
		cost := mulDivFloor(maker.Price, fill)
		if cost.Sign() == 0 {
			return s, errors.New("zero-cost fill")
		}
		if cost.Cmp(mulDivFloor(taker.Price, fill)) < 0 {
			return s, fmt.Errorf("proceeds %s are below the taker's bound", cost)
		}
		s.makerCost, s.takerCost = cost, cost
		return s, nil

	case taker.Side != maker.Side:
		return s, errors.New("a buy and a sell of different outcomes do not cross")

	case taker.Side == types.OrderSideBuy:
		s.kind = matchMint
		sum := new(big.Int).Add(taker.Price, maker.Price)
		if sum.Cmp(exchangePriceScale) < 0 {
			return s, fmt.Errorf("bids %s + %s do not sum to %d", taker.Price, maker.Price, ExchangePriceScale)
		}
		s.makerCost = mulDivFloor(maker.Price, fill)
		s.takerCost = new(big.Int).Sub(fill, s.makerCost)
		if s.makerCost.Sign() == 0 || s.takerCost.Sign() == 0 {
			return s, errors.New("zero-cost fill")
		}
		if s.takerCost.Cmp(mulDivCeil(taker.Price, fill)) > 0 {
			return s, fmt.Errorf("cost %s exceeds the taker's bound", s.takerCost)
		}
		return s, nil

	default:
		s.kind = matchMerge
		sum := new(big.Int).Add(taker.Price, maker.Price)
		if sum.Cmp(exchangePriceScale) > 0 {
			return s, fmt.Errorf("asks %s + %s exceed %d", taker.Price, maker.Price, ExchangePriceScale)
		}
		s.makerCost = mulDivCeil(maker.Price, fill)
		s.takerCost = new(big.Int).Sub(fill, s.makerCost)
		if s.makerCost.Sign() == 0 || s.takerCost.Sign() == 0 {
			return s, errors.New("zero-cost fill")
		}
		if s.takerCost.Cmp(mulDivFloor(taker.Price, fill)) < 0 {
			return s, fmt.Errorf("proceeds %s are below the taker's bound", s.takerCost)
		}
		return s, nil
	}
}

func terms(o *exchangeOrder) orderTerms {
	return orderTerms{Side: o.Side, Outcome: o.Outcome, Price: o.Price}
}

// matchPlan is one validated pair, ready to apply.
type matchPlan struct {
	maker  *exchangeOrder
	settle pairSettlement
}

// Match settles one taker order against maker orders, atomically, on behalf
// of an operator. Check order: operator gate → pause → caps → static order
// validation → condition state → per-order state (cancel, expiry, nonce,
// fill) → per-pair pricing → ML-DSA verification (skipped for an order the
// chain has already filled part of) → apply. Nothing is written before the
// last step, and the whole call reverts on any error.
func (x *Exchange) Match(ctx ModuleContext, p exchangeMatchParams, receipt *types.Receipt) error {
	const what = "exchange match: "
	if err := x.requireOperator(what, ctx.Caller); err != nil {
		return err
	}
	if err := x.requireNotPaused(what); err != nil {
		return err
	}
	n := len(p.MakerOrders)
	if n == 0 {
		return errors.New(what + "maker_orders is empty")
	}
	if n > MaxExchangeMakers {
		return fmt.Errorf(what+"%d maker orders exceed the cap of %d", n, MaxExchangeMakers)
	}
	if len(p.FillShares) != n {
		return fmt.Errorf(what+"fill_shares has %d entries for %d maker orders", len(p.FillShares), n)
	}
	fills := make([]*big.Int, n)
	for i, s := range p.FillShares {
		v, err := parseUint256(s)
		if err != nil {
			return fmt.Errorf(what+"fill_shares[%d]: %w", i, err)
		}
		if v.Sign() == 0 {
			return fmt.Errorf(what+"fill_shares[%d] must be positive", i)
		}
		fills[i] = v
	}

	taker, err := x.parseOrder(what+"taker_order: ", p.TakerOrder)
	if err != nil {
		return err
	}
	makers := make([]*exchangeOrder, n)
	for i, w := range p.MakerOrders {
		if makers[i], err = x.parseOrder(fmt.Sprintf("%smaker_orders[%d]: ", what, i), w); err != nil {
			return err
		}
	}

	c, err := x.Condition(taker.Condition)
	if err != nil {
		return fmt.Errorf(what+"taker_order: %w", err)
	}
	if c.Resolved {
		return fmt.Errorf(what+"condition %d is resolved", c.ID)
	}
	if ctx.Time >= c.EndTime {
		return fmt.Errorf(what+"condition %d has ended (end_time %d, block time %d)", c.ID, c.EndTime, ctx.Time)
	}
	if taker.Outcome >= c.OutcomeCount {
		return fmt.Errorf(what+"taker_order: outcome %d is out of range (%d outcomes)", taker.Outcome, c.OutcomeCount)
	}
	for i, m := range makers {
		if m.Condition != c.ID {
			return fmt.Errorf(what+"maker_orders[%d]: condition %d differs from the taker's %d", i, m.Condition, c.ID)
		}
		if m.Outcome >= c.OutcomeCount {
			return fmt.Errorf(what+"maker_orders[%d]: outcome %d is out of range (%d outcomes)", i, m.Outcome, c.OutcomeCount)
		}
		if m.hash == taker.hash {
			return fmt.Errorf(what+"maker_orders[%d] is the taker order", i)
		}
		for j := 0; j < i; j++ {
			if makers[j].hash == m.hash {
				return fmt.Errorf(what+"maker_orders[%d] duplicates maker_orders[%d]", i, j)
			}
		}
	}

	takerRemaining, takerFilled, err := x.checkOrderState(what+"taker_order: ", ctx, taker)
	if err != nil {
		return err
	}
	total := new(big.Int)
	for _, f := range fills {
		total.Add(total, f)
	}
	if total.Cmp(takerRemaining) > 0 {
		return fmt.Errorf(what+"taker_order: fills total %s but only %s remains", total, takerRemaining)
	}
	plans := make([]matchPlan, n)
	makerFilled := make([]*big.Int, n)
	for i, m := range makers {
		prefix := fmt.Sprintf("%smaker_orders[%d]: ", what, i)
		remaining, filled, err := x.checkOrderState(prefix, ctx, m)
		if err != nil {
			return err
		}
		if fills[i].Cmp(remaining) > 0 {
			return fmt.Errorf("%sfill %s exceeds the %s remaining", prefix, fills[i], remaining)
		}
		settle, err := settlePair(terms(taker), terms(m), fills[i])
		if err != nil {
			return fmt.Errorf("%s%w", prefix, err)
		}
		plans[i] = matchPlan{maker: m, settle: settle}
		makerFilled[i] = filled
	}

	// The taker's bound holds on the AGGREGATE too. settlePair allows each
	// pair one unit of rounding, so an operator choosing fill_shares could
	// otherwise split a fill into n one-unit pairs and charge (or short) the
	// taker n units against a signed bound of one: BUY 1000 @0.01 against
	// sixteen SELL-1 makers paid 16 for shares bounded at 1. Over the whole
	// match the taker pays at most ceil(p_t·total/1e6) or receives at least
	// floor(p_t·total/1e6) — the documented tolerance, once per match, not
	// once per pair. No per-pair minimum fill: an equal-price cross and a
	// sum-==-1e6 mint/merge still fill at any size.
	takerTotal := new(big.Int)
	for _, plan := range plans {
		takerTotal.Add(takerTotal, plan.settle.takerCost)
	}
	if taker.Side == types.OrderSideBuy {
		if bound := mulDivCeil(taker.Price, total); takerTotal.Cmp(bound) > 0 {
			return fmt.Errorf(what+"taker bound exceeded (paid %s for %s shares at %s)", takerTotal, total, taker.Price)
		}
	} else if bound := mulDivFloor(taker.Price, total); takerTotal.Cmp(bound) < 0 {
		return fmt.Errorf(what+"taker bound exceeded (received %s for %s shares at %s)", takerTotal, total, taker.Price)
	}

	// Signatures last: an order the chain already filled part of was
	// verified then, and its hash commits to every field.
	if takerFilled.Sign() == 0 {
		if err := x.verifyOrder(what+"taker_order: ", taker); err != nil {
			return err
		}
	}
	for i, m := range makers {
		if makerFilled[i].Sign() == 0 {
			if err := x.verifyOrder(fmt.Sprintf("%smaker_orders[%d]: ", what, i), m); err != nil {
				return err
			}
		}
	}

	return x.atomic(receipt, func() error {
		tok := NewQRC20Token(x.stateDB, c.Collateral)
		pos := x.positions()
		for i, plan := range plans {
			prefix := fmt.Sprintf("%smaker_orders[%d]: ", what, i)
			if err := x.applyPair(prefix, c, tok, pos, taker, plan, receipt); err != nil {
				return err
			}
			if err := x.advanceFill(plan.maker.hash, plan.settle.fill); err != nil {
				return err
			}
		}
		return x.advanceFill(taker.hash, total)
	})
}

func (x *Exchange) advanceFill(hash [32]byte, by *big.Int) error {
	filled, err := x.OrderFilled(hash)
	if err != nil {
		return err
	}
	return x.setBig(exOrderFilledSlot(hash), new(big.Int).Add(filled, by))
}

// applyPair moves what one pair settles and records its events. Collateral
// moves through the token's own transfer (so a paused or short token fails
// the match); positions move through the module-only paths.
func (x *Exchange) applyPair(prefix string, c *ExchangeCondition, tok *QRC20Token, pos *QRC1155Token, taker *exchangeOrder, plan matchPlan, receipt *types.Receipt) error {
	m, s := plan.maker, plan.settle
	switch s.kind {
	case matchComplementary:
		buyer, seller := taker, m
		if m.Side == types.OrderSideBuy {
			buyer, seller = m, taker
		}
		id := ExchangePositionID(c.ID, buyer.Outcome)
		if err := x.requirePosition(pos, seller.Maker, c.ID, buyer.Outcome, s.fill); err != nil {
			return fmt.Errorf("%s%w", prefix, err)
		}
		if err := tok.TransferWithEvents(buyer.Maker, seller.payTo, s.makerCost, receipt); err != nil {
			return fmt.Errorf("%scollateral: %w", prefix, err)
		}
		if err := pos.transferUnchecked(x.addr, seller.Maker, buyer.payTo, id, s.fill, receipt); err != nil {
			return fmt.Errorf("%spositions: %w", prefix, err)
		}

	case matchMint:
		if err := tok.TransferWithEvents(m.Maker, x.addr, s.makerCost, receipt); err != nil {
			return fmt.Errorf("%scollateral: %w", prefix, err)
		}
		if err := tok.TransferWithEvents(taker.Maker, x.addr, s.takerCost, receipt); err != nil {
			return fmt.Errorf("%staker collateral: %w", prefix, err)
		}
		if err := pos.mintUnchecked(x.addr, m.payTo, ExchangePositionID(c.ID, m.Outcome), s.fill, "", receipt); err != nil {
			return fmt.Errorf("%spositions: %w", prefix, err)
		}
		if err := pos.mintUnchecked(x.addr, taker.payTo, ExchangePositionID(c.ID, taker.Outcome), s.fill, "", receipt); err != nil {
			return fmt.Errorf("%staker positions: %w", prefix, err)
		}
		c.Locked = new(big.Int).Add(c.Locked, s.fill)
		if err := x.setLocked(c.ID, c.Locked); err != nil {
			return err
		}
		x.log(receipt, MintMatchedEvent(x.addr, taker.hash, m.hash, c.ID, s.fill))

	case matchMerge:
		if err := x.requirePosition(pos, m.Maker, c.ID, m.Outcome, s.fill); err != nil {
			return fmt.Errorf("%s%w", prefix, err)
		}
		if err := x.requirePosition(pos, taker.Maker, c.ID, taker.Outcome, s.fill); err != nil {
			return fmt.Errorf("%s%w", prefix, err)
		}
		if err := pos.burnUnchecked(x.addr, m.Maker, ExchangePositionID(c.ID, m.Outcome), s.fill, receipt); err != nil {
			return fmt.Errorf("%spositions: %w", prefix, err)
		}
		if err := pos.burnUnchecked(x.addr, taker.Maker, ExchangePositionID(c.ID, taker.Outcome), s.fill, receipt); err != nil {
			return fmt.Errorf("%staker positions: %w", prefix, err)
		}
		if err := x.release(prefix, c, s.fill); err != nil {
			return err
		}
		if err := tok.TransferWithEvents(x.addr, m.payTo, s.makerCost, receipt); err != nil {
			return fmt.Errorf("%scollateral: %w", prefix, err)
		}
		if err := tok.TransferWithEvents(x.addr, taker.payTo, s.takerCost, receipt); err != nil {
			return fmt.Errorf("%staker collateral: %w", prefix, err)
		}
		x.log(receipt, MergeMatchedEvent(x.addr, taker.hash, m.hash, c.ID, s.fill))
	}
	x.log(receipt, OrderFilledEvent(x.addr, m.hash, m.Maker, taker.Maker, c.ID, m.Outcome, m.Side, s.fill, s.makerCost))
	x.log(receipt, OrderFilledEvent(x.addr, taker.hash, taker.Maker, x.addr, c.ID, taker.Outcome, taker.Side, s.fill, s.takerCost))
	return nil
}

// ── Cancel / nonce ──────────────────────────────────────────────────────────

// CancelOrder marks an order cancelled. Allowed for the maker, for an
// operator, or for anyone carrying the maker's CtxOrder signature over
// types.CancelDigest(hash). Idempotent: a cancelled order stays cancelled
// with no second event. Never gated by pause.
func (x *Exchange) CancelOrder(ctx ModuleContext, p exchangeCancelParams, receipt *types.Receipt) error {
	const what = "exchange cancel_order: "
	if err := x.requireDeployed(what); err != nil {
		return err
	}
	o, err := x.parseOrder(what+"order: ", p.Order)
	if err != nil {
		return err
	}
	authorised := ctx.Caller == o.Maker
	if !authorised {
		if authorised, err = x.IsOperator(ctx.Caller); err != nil {
			return err
		}
	}
	if !authorised {
		if len(p.CancelSignature) == 0 {
			return fmt.Errorf(what+"caller %s is neither the maker nor an operator and carries no cancel_signature", ctx.Caller)
		}
		pubKey := p.MakerPubKey
		if len(pubKey) == 0 {
			pubKey = o.pubKey
		}
		pub, err := x.resolveSigner(what, o.Maker, pubKey)
		if err != nil {
			return err
		}
		digest := types.CancelDigest(o.hash)
		if !qcrypto.Verify(pub, digest[:], qcrypto.CtxOrder, p.CancelSignature) {
			return fmt.Errorf(what+"invalid cancel_signature for order %x", o.hash)
		}
	}
	cancelled, err := x.OrderCancelled(o.hash)
	if err != nil {
		return err
	}
	if cancelled {
		return nil
	}
	return x.atomic(receipt, func() error {
		if err := x.setFlag(exOrderCancelledSlot(o.hash), true); err != nil {
			return err
		}
		x.log(receipt, OrderCancelledEvent(x.addr, o.hash, o.Maker))
		return nil
	})
}

// IncrementNonce bumps a maker's order nonce, invalidating every order signed
// with the old one. Allowed for the maker, or for anyone carrying the maker's
// CtxOrder signature over types.IncrementNonceDigest(chain, exchange, maker,
// expected_nonce); expected_nonce must equal the current nonce so each
// signature is single-use. Never gated by pause.
func (x *Exchange) IncrementNonce(ctx ModuleContext, p exchangeNonceParams, receipt *types.Receipt) error {
	const what = "exchange increment_nonce: "
	if err := x.requireDeployed(what); err != nil {
		return err
	}
	maker, err := qcrypto.AddressFromString(p.Maker)
	if err != nil {
		return fmt.Errorf(what+"maker: %w", err)
	}
	if maker.IsZero() {
		return errors.New(what + "maker is the zero address")
	}
	current, err := x.MakerNonce(maker)
	if err != nil {
		return err
	}
	if p.ExpectedNonce != current {
		return fmt.Errorf(what+"expected_nonce %d but maker %s is at nonce %d", p.ExpectedNonce, maker, current)
	}
	if ctx.Caller != maker {
		if len(p.Signature) == 0 {
			return fmt.Errorf(what+"caller %s is not the maker and carries no signature", ctx.Caller)
		}
		pub, err := x.resolveSigner(what, maker, p.MakerPubKey)
		if err != nil {
			return err
		}
		digest, err := types.IncrementNonceDigest(types.ChainID, x.addr, maker, p.ExpectedNonce)
		if err != nil {
			return fmt.Errorf(what+"%w", err)
		}
		if !qcrypto.Verify(pub, digest[:], qcrypto.CtxOrder, p.Signature) {
			return fmt.Errorf(what+"invalid signature for maker %s at nonce %d", maker, p.ExpectedNonce)
		}
	}
	if current == ^uint64(0) {
		return fmt.Errorf(what+"maker %s nonce is exhausted", maker)
	}
	return x.atomic(receipt, func() error {
		if err := x.setU64(exNonceSlot(maker), current+1); err != nil {
			return err
		}
		x.log(receipt, NonceIncrementedEvent(x.addr, maker, current+1))
		return nil
	})
}

// ── Events ──────────────────────────────────────────────────────────────────

// ConditionCreatedEvent: condition indexed; collateral, resolver, end_time,
// outcome_count, question_hash as data words.
func ConditionCreatedEvent(contract qcrypto.Address, condition uint64, collateral, resolver qcrypto.Address, endTime int64, outcomeCount uint8, questionHash [32]byte) *types.Log {
	return NewEvent(contract, "ConditionCreated(uint64,sqaddress,sqaddress,uint64,uint8,bytes32)").
		IndexUint256(new(big.Int).SetUint64(condition)).
		DataAddr(collateral).
		DataAddr(resolver).
		DataUint256(new(big.Int).SetUint64(uint64(endTime))).
		DataUint256(big.NewInt(int64(outcomeCount))).
		DataHash(questionHash).
		Build()
}

// OrderFilledEvent: hash, maker, taker indexed; condition, outcome, side,
// fill and cost as data words. For the taker's own fill `taker` is the
// exchange address, as in the Solidity exchange.
func OrderFilledEvent(contract qcrypto.Address, hash [32]byte, maker, taker qcrypto.Address, condition uint64, outcome, side uint8, fill, cost *big.Int) *types.Log {
	return NewEvent(contract, "OrderFilled(bytes32,sqaddress,sqaddress,uint64,uint8,uint8,uint256,uint256)").
		IndexHash(hash).
		IndexAddr(maker).
		IndexAddr(taker).
		DataUint256(new(big.Int).SetUint64(condition)).
		DataUint256(big.NewInt(int64(outcome))).
		DataUint256(big.NewInt(int64(side))).
		DataUint256(fill).
		DataUint256(cost).
		Build()
}

// MintMatchedEvent: taker and maker hashes indexed; condition and fill as data.
func MintMatchedEvent(contract qcrypto.Address, takerHash, makerHash [32]byte, condition uint64, fill *big.Int) *types.Log {
	return NewEvent(contract, "MintMatched(bytes32,bytes32,uint64,uint256)").
		IndexHash(takerHash).
		IndexHash(makerHash).
		DataUint256(new(big.Int).SetUint64(condition)).
		DataUint256(fill).
		Build()
}

// MergeMatchedEvent: taker and maker hashes indexed; condition and fill as data.
func MergeMatchedEvent(contract qcrypto.Address, takerHash, makerHash [32]byte, condition uint64, fill *big.Int) *types.Log {
	return NewEvent(contract, "MergeMatched(bytes32,bytes32,uint64,uint256)").
		IndexHash(takerHash).
		IndexHash(makerHash).
		DataUint256(new(big.Int).SetUint64(condition)).
		DataUint256(fill).
		Build()
}

// OrderCancelledEvent: hash and maker indexed.
func OrderCancelledEvent(contract qcrypto.Address, hash [32]byte, maker qcrypto.Address) *types.Log {
	return NewEvent(contract, "OrderCancelled(bytes32,sqaddress)").
		IndexHash(hash).
		IndexAddr(maker).
		Build()
}

// NonceIncrementedEvent: maker indexed; the new nonce as data.
func NonceIncrementedEvent(contract, maker qcrypto.Address, nonce uint64) *types.Log {
	return NewEvent(contract, "NonceIncremented(sqaddress,uint64)").
		IndexAddr(maker).
		DataUint256(new(big.Int).SetUint64(nonce)).
		Build()
}

// PositionSplitEvent: holder indexed; condition and amount as data.
func PositionSplitEvent(contract, holder qcrypto.Address, condition uint64, amount *big.Int) *types.Log {
	return NewEvent(contract, "PositionSplit(sqaddress,uint64,uint256)").
		IndexAddr(holder).
		DataUint256(new(big.Int).SetUint64(condition)).
		DataUint256(amount).
		Build()
}

// PositionsMergedEvent: holder indexed; condition and amount as data.
func PositionsMergedEvent(contract, holder qcrypto.Address, condition uint64, amount *big.Int) *types.Log {
	return NewEvent(contract, "PositionsMerged(sqaddress,uint64,uint256)").
		IndexAddr(holder).
		DataUint256(new(big.Int).SetUint64(condition)).
		DataUint256(amount).
		Build()
}

// ResolvedEvent: condition indexed; the winning outcome as data.
func ResolvedEvent(contract qcrypto.Address, condition uint64, outcome uint8) *types.Log {
	return NewEvent(contract, "Resolved(uint64,uint8)").
		IndexUint256(new(big.Int).SetUint64(condition)).
		DataUint256(big.NewInt(int64(outcome))).
		Build()
}

// RedeemedEvent: holder indexed; condition, shares burned and payout as data.
func RedeemedEvent(contract, holder qcrypto.Address, condition uint64, burned, payout *big.Int) *types.Log {
	return NewEvent(contract, "Redeemed(sqaddress,uint64,uint256,uint256)").
		IndexAddr(holder).
		DataUint256(new(big.Int).SetUint64(condition)).
		DataUint256(burned).
		DataUint256(payout).
		Build()
}

// RedeemFeeChargedEvent: fee recipient indexed; the fee as data.
func RedeemFeeChargedEvent(contract, recipient qcrypto.Address, fee *big.Int) *types.Log {
	return NewEvent(contract, "RedeemFeeCharged(sqaddress,uint256)").
		IndexAddr(recipient).
		DataUint256(fee).
		Build()
}

// RedeemFeeUpdatedEvent: the new fee in bps as data.
func RedeemFeeUpdatedEvent(contract qcrypto.Address, bps uint64) *types.Log {
	return NewEvent(contract, "RedeemFeeUpdated(uint256)").
		DataUint256(new(big.Int).SetUint64(bps)).
		Build()
}

// FeeRecipientUpdatedEvent: the new recipient indexed.
func FeeRecipientUpdatedEvent(contract, recipient qcrypto.Address) *types.Log {
	return NewEvent(contract, "FeeRecipientUpdated(sqaddress)").
		IndexAddr(recipient).
		Build()
}

// ── Helpers ─────────────────────────────────────────────────────────────────

// atomic runs fn inside a StateDB snapshot and reverts it — and any logs fn
// appended to receipt — when fn errors. Nested inside the executor's own
// module_revert_v1 snapshot it is redundant; without one (a legacy chain, a
// direct in-package call) it is what keeps a failed action residue-free.
// A snapshot-layer error is HARD: it is a broken invariant in the state
// layer, not the action's own complaint, and must not become a receipt.
func (x *Exchange) atomic(receipt *types.Receipt, fn func() error) error {
	snap := x.stateDB.Snapshot()
	logs := 0
	if receipt != nil {
		logs = len(receipt.Logs)
	}
	if err := fn(); err != nil {
		if rerr := x.stateDB.RevertToSnapshot(snap); rerr != nil {
			return hard(fmt.Errorf("exchange: %w", rerr))
		}
		if receipt != nil {
			receipt.Logs = receipt.Logs[:logs]
		}
		return err
	}
	if err := x.stateDB.CommitSnapshot(snap); err != nil {
		return hard(fmt.Errorf("exchange: %w", err))
	}
	return nil
}

func (x *Exchange) log(receipt *types.Receipt, l *types.Log) {
	if receipt != nil {
		receipt.AddLog(l)
	}
}

func checkPositiveAmount(what string, amount *big.Int) error {
	if amount == nil || amount.Sign() <= 0 {
		return errors.New(what + "amount must be positive")
	}
	if amount.BitLen() > 256 {
		return errors.New(what + "amount does not fit in 256 bits")
	}
	return nil
}

// parseHex32 decodes a 32-byte wire field: exactly 64 lowercase hex chars,
// no 0x prefix.
func parseHex32(s string) ([32]byte, error) {
	var out [32]byte
	if len(s) != 64 {
		return out, fmt.Errorf("want 64 hex chars, got %d", len(s))
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return out, fmt.Errorf("invalid lowercase hex %q", s)
		}
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return out, err
	}
	copy(out[:], b)
	return out, nil
}

func (x *Exchange) setBig(slot [32]byte, v *big.Int) error {
	return x.stateDB.SetStorageValue(x.addr, slot, v.Bytes())
}

// The readers below return a StateDB read ERROR as an error. Folding it into
// the empty-slot zero (as they once did) turned an I/O fault on one node into
// a different, successful result — cancelled=false, filled=0, Locked=0 — and
// therefore a per-node divergence instead of a rejected block. An empty slot
// is still the zero value.
func (x *Exchange) getBig(slot [32]byte) (*big.Int, error) {
	b, err := x.stateDB.GetStorageValue(x.addr, slot)
	if err != nil {
		return big.NewInt(0), err
	}
	if len(b) == 0 {
		return big.NewInt(0), nil
	}
	return new(big.Int).SetBytes(b), nil
}

func (x *Exchange) setU64(slot [32]byte, v uint64) error {
	return x.setBig(slot, new(big.Int).SetUint64(v))
}

func (x *Exchange) getU64(slot [32]byte) (uint64, error) {
	v, err := x.getBig(slot)
	if err != nil {
		return 0, err
	}
	if v.BitLen() > 64 {
		return 0, fmt.Errorf("exchange: slot %x holds %s, not a uint64", slot[:4], v)
	}
	return v.Uint64(), nil
}

func (x *Exchange) setAddr(slot [32]byte, a qcrypto.Address) error {
	return x.stateDB.SetStorageValue(x.addr, slot, a[:])
}

func (x *Exchange) getAddr(slot [32]byte) (qcrypto.Address, error) {
	b, err := x.stateDB.GetStorageValue(x.addr, slot)
	if err != nil {
		return qcrypto.Address{}, err
	}
	if len(b) < qcrypto.AddressSize {
		return qcrypto.Address{}, nil
	}
	var a qcrypto.Address
	copy(a[:], b)
	return a, nil
}

func (x *Exchange) setFlag(slot [32]byte, v bool) error {
	if v {
		return x.stateDB.SetStorageValue(x.addr, slot, []byte{1})
	}
	return x.stateDB.SetStorageValue(x.addr, slot, []byte{0})
}

func (x *Exchange) getFlag(slot [32]byte) (bool, error) {
	b, err := x.stateDB.GetStorageValue(x.addr, slot)
	if err != nil {
		return false, err
	}
	if len(b) == 0 {
		return false, nil
	}
	return b[0] == 1, nil
}
