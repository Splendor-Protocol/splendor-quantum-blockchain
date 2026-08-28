package vm

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"math/rand"
	"strings"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// ── Fixture ─────────────────────────────────────────────────────────────────

const (
	exTime    = int64(1_000_000)
	exEndTime = int64(2_000_000_000)
	exFunding = int64(1_000_000_000)
)

// exTrader is a maker with a real ML-DSA-65 key and NO registered public key,
// so the fresh-maker path (maker_pubkey supplied, checked against the
// address) is the default.
type exTrader struct {
	addr qcrypto.Address
	pub  *qcrypto.PublicKey
	priv *qcrypto.PrivateKey
}

func newExTrader(t *testing.T) *exTrader {
	t.Helper()
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	return &exTrader{addr: qcrypto.DeriveAddress(pub), pub: pub, priv: priv}
}

// exFixture is one exchange with one binary condition on a collateral token
// deployed through the router, an operator, a resolver, and three funded
// traders. Block time is exTime unless a test moves it.
type exFixture struct {
	sdb      *state.StateDB
	owner    qcrypto.Address
	operator qcrypto.Address
	resolver qcrypto.Address
	token    qcrypto.Address
	tok      *QRC20Token
	x        *Exchange
	pos      *QRC1155Token
	cond     uint64
	alice    *exTrader
	bob      *exTrader
	carol    *exTrader
	time     int64
	salt     byte
}

func newExFixture(t *testing.T) *exFixture {
	t.Helper()
	sdb := newTestState(t)
	f := &exFixture{
		sdb: sdb, owner: addr(0x11), operator: addr(0x0e), resolver: addr(0x77), time: exTime,
		alice: newExTrader(t), bob: newExTrader(t), carol: newExTrader(t),
	}
	dep := &types.Receipt{}
	if err := DispatchModuleCall(sdb, ModuleContext{Caller: f.owner, Nonce: 1, Height: 1, Time: exTime}, moduleData(t, "qrc20", "deploy", qrc20DeployParams{
		Name: "Collateral", Symbol: "USDQ", Decimals: 6, TotalSupply: "1000000000000",
	}), dep); err != nil {
		t.Fatalf("deploy collateral: %v", err)
	}
	f.token = *dep.ContractAddress
	f.tok = NewQRC20Token(sdb, f.token)
	for _, tr := range []*exTrader{f.alice, f.bob, f.carol} {
		if err := f.tok.Transfer(f.owner, tr.addr, big.NewInt(exFunding)); err != nil {
			t.Fatalf("fund: %v", err)
		}
	}
	x, err := DeployExchange(sdb, ModuleContext{Caller: f.owner, Nonce: 2, Height: 1, Time: exTime}, exchangeDeployParams{Name: "Positions"})
	if err != nil {
		t.Fatalf("deploy exchange: %v", err)
	}
	f.x = NewExchange(sdb, x)
	f.pos = NewQRC1155Token(sdb, x)
	if err := f.x.SetOperator(f.owner, f.operator, true, nil); err != nil {
		t.Fatalf("set_operator: %v", err)
	}
	f.cond = f.createCondition(t, f.operator, exEndTime)
	return f
}

func (f *exFixture) ctx(caller qcrypto.Address) ModuleContext {
	return ModuleContext{Caller: caller, Nonce: 0, Height: 1, Time: f.time, Forks: forks.Schedule{forks.QRC1155V1: 1, forks.QRC20V2: 1}}
}

func (f *exFixture) createCondition(t *testing.T, caller qcrypto.Address, endTime int64) uint64 {
	t.Helper()
	id, err := f.x.CreateCondition(f.ctx(caller), exchangeConditionParams{
		Exchange: f.x.addr.String(), Collateral: f.token.String(), Resolver: f.resolver.String(),
		EndTime: endTime, OutcomeCount: 2, QuestionHash: strings.Repeat("ab", 32),
	}, nil)
	if err != nil {
		t.Fatalf("create_condition: %v", err)
	}
	return id
}

func (f *exFixture) nextSalt() [32]byte {
	f.salt++
	var s [32]byte
	s[31] = f.salt
	return s
}

// build returns an order struct for tr at its current nonce; tests mutate
// fields before signing.
func (f *exFixture) build(t *testing.T, tr *exTrader, side, outcome uint8, shares, price int64) types.Order {
	t.Helper()
	nonce, err := f.x.MakerNonce(tr.addr)
	if err != nil {
		t.Fatalf("nonce: %v", err)
	}
	return types.Order{
		Salt: f.nextSalt(), Maker: tr.addr, Exchange: f.x.addr, Condition: f.cond,
		Outcome: outcome, Side: side, ShareAmount: big.NewInt(shares), Price: big.NewInt(price),
		Expiration: 0, Nonce: nonce,
	}
}

func (f *exFixture) signWith(t *testing.T, tr *exTrader, o types.Order, ctxBytes []byte) []byte {
	t.Helper()
	d, err := types.OrderDigest(types.ChainID, o)
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	sig, err := qcrypto.Sign(tr.priv, d[:], ctxBytes)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return sig
}

// wire converts an order to its wire shape with the given signature and key.
func wireOf(o types.Order, sig, pub []byte) exchangeOrderWire {
	w := exchangeOrderWire{
		Salt: hex.EncodeToString(o.Salt[:]), Maker: o.Maker.String(), Exchange: o.Exchange.String(),
		Condition: o.Condition, Outcome: o.Outcome, Side: o.Side,
		ShareAmount: o.ShareAmount.String(), Price: o.Price.String(),
		Expiration: o.Expiration, Nonce: o.Nonce, Signature: sig, MakerPubKey: pub,
	}
	if !o.Recipient.IsZero() {
		w.Recipient = o.Recipient.String()
	}
	return w
}

// signed signs o with tr under CtxOrder and attaches tr's key.
func (f *exFixture) signed(t *testing.T, tr *exTrader, o types.Order) exchangeOrderWire {
	t.Helper()
	return wireOf(o, f.signWith(t, tr, o, qcrypto.CtxOrder), tr.pub.Bytes())
}

// order builds and signs in one step.
func (f *exFixture) order(t *testing.T, tr *exTrader, side, outcome uint8, shares, price int64) exchangeOrderWire {
	t.Helper()
	return f.signed(t, tr, f.build(t, tr, side, outcome, shares, price))
}

func (f *exFixture) match(t *testing.T, caller qcrypto.Address, taker exchangeOrderWire, makers []exchangeOrderWire, fills ...int64) (*types.Receipt, error) {
	t.Helper()
	fs := make([]string, len(fills))
	for i, v := range fills {
		fs[i] = big.NewInt(v).String()
	}
	rc := &types.Receipt{}
	return rc, f.x.Match(f.ctx(caller), exchangeMatchParams{
		Exchange: f.x.addr.String(), TakerOrder: taker, MakerOrders: makers, FillShares: fs,
	}, rc)
}

func (f *exFixture) hash(t *testing.T, w exchangeOrderWire) [32]byte {
	t.Helper()
	h, err := f.x.OrderHash(w)
	if err != nil {
		t.Fatalf("order hash: %v", err)
	}
	return h
}

func (f *exFixture) filled(t *testing.T, w exchangeOrderWire) int64 {
	t.Helper()
	v, err := f.x.OrderFilled(f.hash(t, w))
	if err != nil {
		t.Fatalf("filled: %v", err)
	}
	return v.Int64()
}

func (f *exFixture) collateral(t *testing.T, a qcrypto.Address) int64 {
	t.Helper()
	b, err := f.tok.BalanceOf(a)
	if err != nil {
		t.Fatalf("balance: %v", err)
	}
	return b.Int64()
}

func (f *exFixture) position(t *testing.T, a qcrypto.Address, outcome uint8) int64 {
	t.Helper()
	b, err := f.pos.BalanceOf(a, ExchangePositionID(f.cond, outcome))
	if err != nil {
		t.Fatalf("position: %v", err)
	}
	return b.Int64()
}

func (f *exFixture) locked(t *testing.T) int64 {
	t.Helper()
	c, err := f.x.Condition(f.cond)
	if err != nil {
		t.Fatalf("condition: %v", err)
	}
	return c.Locked.Int64()
}

func (f *exFixture) split(t *testing.T, tr *exTrader, amount int64) *types.Receipt {
	t.Helper()
	rc := &types.Receipt{}
	if err := f.x.Split(f.ctx(tr.addr), f.cond, big.NewInt(amount), rc); err != nil {
		t.Fatalf("split: %v", err)
	}
	return rc
}

func (f *exFixture) resolve(t *testing.T, outcome uint8) {
	t.Helper()
	if err := f.x.Resolve(f.ctx(f.resolver), f.cond, outcome, nil); err != nil {
		t.Fatalf("resolve: %v", err)
	}
}

// exTopics names topic[0] of every log, exchange events included.
func exTopics(rc *types.Receipt) []string {
	names := map[[32]byte]string{
		TopicTransfer: "Transfer", TopicApproval: "Approval", TopicTransferSingle: "TransferSingle",
		TopicPaused: "Paused", TopicUnpaused: "Unpaused", TopicOwnershipTransferred: "OwnershipTransferred",
		TopicConditionCreated: "ConditionCreated", TopicOrderFilled: "OrderFilled",
		TopicMintMatched: "MintMatched", TopicMergeMatched: "MergeMatched",
		TopicOrderCancelled: "OrderCancelled", TopicNonceIncremented: "NonceIncremented",
		TopicPositionSplit: "PositionSplit", TopicPositionsMerged: "PositionsMerged",
		TopicResolved: "Resolved", TopicRedeemed: "Redeemed", TopicRedeemFeeCharged: "RedeemFeeCharged",
		TopicOperatorSet: "OperatorSet", TopicRedeemFeeUpdated: "RedeemFeeUpdated",
		TopicFeeRecipientUpdated: "FeeRecipientUpdated",
	}
	out := make([]string, 0, len(rc.Logs))
	for _, l := range rc.Logs {
		n, ok := names[l.Topics[0]]
		if !ok {
			n = fmt.Sprintf("%x", l.Topics[0][:4])
		}
		out = append(out, n)
	}
	return out
}

func wantExTopics(t *testing.T, rc *types.Receipt, want ...string) {
	t.Helper()
	if got := strings.Join(exTopics(rc), ","); got != strings.Join(want, ",") {
		t.Fatalf("logs = [%s], want [%s]", got, strings.Join(want, ","))
	}
}

func mustSoft(t *testing.T, err error, want string) {
	t.Helper()
	mustContain(t, err, want)
	if IsHardModuleError(err) {
		t.Fatalf("module errors must be SOFT: %v", err)
	}
	if !strings.HasPrefix(err.Error(), "exchange ") {
		t.Fatalf("error lacks the \"exchange <action>: \" prefix: %v", err)
	}
}

// ── Deploy & parameters ─────────────────────────────────────────────────────

func TestExchangeDeploy(t *testing.T) {
	f := newExFixture(t)
	if want := ExchangeAddress(f.owner, 2, 1); f.x.addr != want {
		t.Fatalf("exchange at %s, want %s", f.x.addr, want)
	}
	if acc, _ := f.sdb.GetAccount(f.x.addr); !acc.IsContract {
		t.Fatal("exchange account not marked is_contract")
	}
	if managed, _ := f.pos.IsModuleManaged(); !managed {
		t.Fatal("position collection must be module-managed (owner == exchange)")
	}
	if owner, _ := f.x.Owner(); owner != f.owner {
		t.Fatalf("owner = %s", owner)
	}
	info, err := f.x.Info()
	if err != nil {
		t.Fatal(err)
	}
	if info.Owner != f.owner.String() || info.FeeRecipient != f.owner.String() || info.RedeemFeeBps != 0 ||
		info.Paused || info.ConditionCount != 1 || len(info.Operators) != 1 || info.Operators[0] != f.operator.String() {
		t.Fatalf("info = %+v", info)
	}

	// Same deployer, nonce and height → same address → the collection's
	// re-init guard refuses it, and nothing about the first instance moves.
	if _, err := DeployExchange(f.sdb, ModuleContext{Caller: f.owner, Nonce: 2, Height: 1}, exchangeDeployParams{Name: "Again"}); err == nil {
		t.Fatal("re-deploy at the same address must be refused")
	} else {
		mustSoft(t, err, "already initialised")
	}
	if name, _ := f.pos.Name(); name != "Positions" {
		t.Fatalf("collection name overwritten: %q", name)
	}
	// A different height moves the address.
	if ExchangeAddress(f.owner, 2, 2) == f.x.addr {
		t.Fatal("height must be part of the address")
	}

	bad := ModuleContext{Caller: f.owner, Nonce: 9, Height: 1}
	if _, err := DeployExchange(f.sdb, bad, exchangeDeployParams{RedeemFeeBps: MaxExchangeRedeemFeeBps + 1}); err == nil {
		t.Fatal("fee above the cap accepted")
	}
	if _, err := DeployExchange(f.sdb, bad, exchangeDeployParams{FeeRecipient: qcrypto.Address{}.String()}); err == nil {
		t.Fatal("zero fee recipient accepted")
	}
	if _, err := DeployExchange(f.sdb, bad, exchangeDeployParams{FeeRecipient: ExchangeAddress(f.owner, 9, 1).String()}); err == nil {
		t.Fatal("exchange as its own fee recipient accepted")
	}
	// The failed deploys wrote nothing at their address.
	if owner, _ := NewExchange(f.sdb, ExchangeAddress(f.owner, 9, 1)).Owner(); !owner.IsZero() {
		t.Fatal("a refused deploy left an owner behind")
	}
	if _, err := DeployExchange(f.sdb, ModuleContext{Caller: f.owner, Nonce: 10, Height: 1}, exchangeDeployParams{RedeemFeeBps: 250, FeeRecipient: f.carol.addr.String()}); err != nil {
		t.Fatalf("deploy with fee params: %v", err)
	}
}

func TestExchangeOwnerControls(t *testing.T) {
	f := newExFixture(t)
	stranger := f.carol.addr
	if err := f.x.Pause(stranger, nil); err == nil {
		t.Fatal("non-owner pause accepted")
	} else {
		mustSoft(t, err, "not the owner")
	}
	if err := f.x.SetOperator(f.operator, stranger, true, nil); err == nil {
		t.Fatal("operator granting operators accepted")
	}
	if err := f.x.SetRedeemFeeBps(f.owner, MaxExchangeRedeemFeeBps+1, nil); err == nil {
		t.Fatal("fee above the cap accepted")
	}
	if err := f.x.SetFeeRecipient(f.owner, f.x.addr, nil); err == nil {
		t.Fatal("exchange as fee recipient accepted")
	}
	if err := f.x.SetFeeRecipient(f.owner, qcrypto.Address{}, nil); err == nil {
		t.Fatal("zero fee recipient accepted")
	}
	if err := f.x.SetOperator(f.owner, f.x.addr, true, nil); err == nil {
		t.Fatal("exchange as operator accepted")
	}
	if err := f.x.TransferOwnership(f.owner, qcrypto.Address{}, nil); err == nil {
		t.Fatal("zero new owner accepted")
	}

	rc := &types.Receipt{}
	if err := f.x.SetRedeemFeeBps(f.owner, 250, rc); err != nil {
		t.Fatal(err)
	}
	if err := f.x.SetFeeRecipient(f.owner, f.carol.addr, rc); err != nil {
		t.Fatal(err)
	}
	if err := f.x.SetOperator(f.owner, f.operator, false, rc); err != nil {
		t.Fatal(err)
	}
	if err := f.x.Pause(f.owner, rc); err != nil {
		t.Fatal(err)
	}
	if err := f.x.Unpause(f.owner, rc); err != nil {
		t.Fatal(err)
	}
	if err := f.x.TransferOwnership(f.owner, stranger, rc); err != nil {
		t.Fatal(err)
	}
	wantExTopics(t, rc, "RedeemFeeUpdated", "FeeRecipientUpdated", "OperatorSet", "Paused", "Unpaused", "OwnershipTransferred")
	info, _ := f.x.Info()
	if info.RedeemFeeBps != 250 || info.FeeRecipient != f.carol.addr.String() || len(info.Operators) != 0 || info.Owner != stranger.String() {
		t.Fatalf("info = %+v", info)
	}
	// The old owner is out; the revoked operator cannot match or create.
	if err := f.x.Pause(f.owner, nil); err == nil {
		t.Fatal("old owner still owns")
	}
	if _, err := f.x.CreateCondition(f.ctx(f.operator), exchangeConditionParams{}, nil); err == nil {
		t.Fatal("revoked operator created a condition")
	} else {
		mustSoft(t, err, "neither the owner nor an operator")
	}
	// Re-granting reuses the operator's slot rather than growing the list.
	if err := f.x.SetOperator(stranger, f.operator, true, nil); err != nil {
		t.Fatal(err)
	}
	if n, _ := f.x.getU64(slotEXOperatorCount); n != 1 {
		t.Fatalf("operator list grew to %d on re-grant", n)
	}
	for i := 1; i < MaxExchangeOperators; i++ {
		if err := f.x.SetOperator(stranger, addr(byte(0x80+i)), true, nil); err != nil {
			t.Fatalf("operator %d: %v", i, err)
		}
	}
	if err := f.x.SetOperator(stranger, addr(0xfe), true, nil); err == nil {
		t.Fatal("operator list over the cap accepted")
	} else {
		mustSoft(t, err, "operator list is full")
	}
	ops, _ := f.x.Operators()
	if len(ops) != MaxExchangeOperators || ops[0] != f.operator {
		t.Fatalf("operators = %d, first %s", len(ops), ops[0])
	}
}

func TestExchangeCreateConditionCaps(t *testing.T) {
	f := newExFixture(t)
	good := exchangeConditionParams{
		Exchange: f.x.addr.String(), Collateral: f.token.String(), Resolver: f.resolver.String(),
		EndTime: exEndTime, OutcomeCount: 2, QuestionHash: strings.Repeat("ab", 32),
	}
	for name, tc := range map[string]func(p *exchangeConditionParams){
		"outcome_count 3":        func(p *exchangeConditionParams) { p.OutcomeCount = 3 },
		"outcome_count 1":        func(p *exchangeConditionParams) { p.OutcomeCount = 1 },
		"question_hash short":    func(p *exchangeConditionParams) { p.QuestionHash = strings.Repeat("ab", 31) },
		"question_hash 0x":       func(p *exchangeConditionParams) { p.QuestionHash = "0x" + strings.Repeat("ab", 31) },
		"question_hash upper":    func(p *exchangeConditionParams) { p.QuestionHash = strings.Repeat("AB", 32) },
		"end_time == block time": func(p *exchangeConditionParams) { p.EndTime = exTime },
		"resolver zero":          func(p *exchangeConditionParams) { p.Resolver = qcrypto.Address{}.String() },
		"collateral not deployed": func(p *exchangeConditionParams) {
			p.Collateral = addr(0x99).String()
		},
		"collateral is the exchange": func(p *exchangeConditionParams) { p.Collateral = f.x.addr.String() },
	} {
		p := good
		tc(&p)
		if _, err := f.x.CreateCondition(f.ctx(f.operator), p, nil); err == nil {
			t.Errorf("%s: accepted", name)
		} else if IsHardModuleError(err) || !strings.HasPrefix(err.Error(), "exchange create_condition: ") {
			t.Errorf("%s: %v", name, err)
		}
	}
	if n, _ := f.x.ConditionCount(); n != 1 {
		t.Fatalf("refused conditions changed the count to %d", n)
	}
	if _, err := f.x.CreateCondition(f.ctx(f.carol.addr), good, nil); err == nil {
		t.Fatal("stranger created a condition")
	}
	rc := &types.Receipt{}
	id, err := f.x.CreateCondition(f.ctx(f.owner), good, rc)
	if err != nil || id != 2 {
		t.Fatalf("owner create: id %d, %v", id, err)
	}
	wantExTopics(t, rc, "ConditionCreated")
	c, err := f.x.Condition(2)
	if err != nil {
		t.Fatal(err)
	}
	if c.Collateral != f.token || c.Resolver != f.resolver || c.EndTime != exEndTime || c.OutcomeCount != 2 ||
		hex.EncodeToString(c.QuestionHash[:]) != strings.Repeat("ab", 32) || c.Resolved || c.Locked.Sign() != 0 {
		t.Fatalf("condition = %+v", c)
	}
	if _, err := f.x.Condition(0); err == nil {
		t.Fatal("condition 0 must not exist")
	}
	if _, err := f.x.Condition(3); err == nil {
		t.Fatal("condition 3 must not exist")
	}
}

// ── Happy paths ─────────────────────────────────────────────────────────────

// The primary flow: two BUYs of opposite outcomes mint a set. Maker bob BUY
// NO @0.50 pays floor(0.5·100) = 50; taker alice BUY YES @0.60 pays the
// complement 50 (inside her ceil(60) bound). Then resolution and redemption.
func TestExchangeMintMatchResolveRedeem(t *testing.T) {
	f := newExFixture(t)
	taker := f.order(t, f.alice, types.OrderSideBuy, 0, 100, 600_000)
	maker := f.order(t, f.bob, types.OrderSideBuy, 1, 100, 500_000)
	rc, err := f.match(t, f.operator, taker, []exchangeOrderWire{maker}, 100)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	wantExTopics(t, rc, "Transfer", "Transfer", "TransferSingle", "TransferSingle", "MintMatched", "OrderFilled", "OrderFilled")
	if got := f.collateral(t, f.alice.addr); got != exFunding-50 {
		t.Fatalf("alice collateral = %d", got)
	}
	if got := f.collateral(t, f.bob.addr); got != exFunding-50 {
		t.Fatalf("bob collateral = %d", got)
	}
	if got := f.collateral(t, f.x.addr); got != 100 {
		t.Fatalf("exchange collateral = %d", got)
	}
	if f.locked(t) != 100 || f.position(t, f.alice.addr, 0) != 100 || f.position(t, f.bob.addr, 1) != 100 ||
		f.position(t, f.alice.addr, 1) != 0 || f.position(t, f.bob.addr, 0) != 0 {
		t.Fatal("positions after mint match are wrong")
	}
	if f.filled(t, taker) != 100 || f.filled(t, maker) != 100 {
		t.Fatal("filled not advanced")
	}
	// OrderFilled for the maker: hash, maker, taker=alice; for the taker:
	// taker = the exchange.
	mk, tk := rc.Logs[5], rc.Logs[6]
	if mk.Topics[1] != f.hash(t, maker) || mk.Topics[2] != AddrTopic(f.bob.addr) || mk.Topics[3] != AddrTopic(f.alice.addr) {
		t.Fatal("maker OrderFilled topics wrong")
	}
	if tk.Topics[1] != f.hash(t, taker) || tk.Topics[2] != AddrTopic(f.alice.addr) || tk.Topics[3] != AddrTopic(f.x.addr) {
		t.Fatal("taker OrderFilled topics wrong")
	}
	wantData := append(append(append(append(word(int64(f.cond)), word(1)...), word(0)...), word(100)...), word(50)...)
	if string(mk.Data) != string(wantData) {
		t.Fatalf("maker OrderFilled data = %x", mk.Data)
	}
	mm := rc.Logs[4]
	if mm.Topics[1] != f.hash(t, taker) || mm.Topics[2] != f.hash(t, maker) || string(mm.Data) != string(append(word(int64(f.cond)), word(100)...)) {
		t.Fatal("MintMatched layout wrong")
	}

	// A consumed order cannot be replayed: overfill.
	if _, err := f.match(t, f.operator, taker, []exchangeOrderWire{maker}, 1); err == nil {
		t.Fatal("replay of a filled order accepted")
	} else {
		mustSoft(t, err, "fully filled")
	}

	// Resolution: resolver only, once, in range.
	if err := f.x.Resolve(f.ctx(f.operator), f.cond, 0, nil); err == nil {
		t.Fatal("operator resolved")
	}
	if err := f.x.Resolve(f.ctx(f.resolver), f.cond, 2, nil); err == nil {
		t.Fatal("outcome 2 accepted")
	}
	rc = &types.Receipt{}
	if err := f.x.Resolve(f.ctx(f.resolver), f.cond, 0, rc); err != nil {
		t.Fatal(err)
	}
	wantExTopics(t, rc, "Resolved")
	if err := f.x.Resolve(f.ctx(f.resolver), f.cond, 1, nil); err == nil {
		t.Fatal("second resolution accepted")
	} else {
		mustSoft(t, err, "already resolved")
	}

	// redeem_for by a third party pays the holder, never the caller; the
	// loser errors; the exchange is drained to zero.
	rc = &types.Receipt{}
	if err := f.x.Redeem(f.ctx(f.carol.addr), f.cond, f.alice.addr, rc); err != nil {
		t.Fatalf("redeem_for: %v", err)
	}
	wantExTopics(t, rc, "TransferSingle", "Transfer", "Redeemed")
	if got := f.collateral(t, f.alice.addr); got != exFunding+50 {
		t.Fatalf("alice after redeem = %d", got)
	}
	if got := f.collateral(t, f.carol.addr); got != exFunding {
		t.Fatalf("carol was paid %d", got-exFunding)
	}
	if err := f.x.Redeem(f.ctx(f.bob.addr), f.cond, f.bob.addr, nil); err == nil {
		t.Fatal("loser redeemed")
	} else {
		mustSoft(t, err, "holds no shares of the winning outcome")
	}
	if err := f.x.Redeem(f.ctx(f.alice.addr), f.cond, f.alice.addr, nil); err == nil {
		t.Fatal("second redeem accepted")
	}
	if got := f.collateral(t, f.x.addr); got != 0 || f.locked(t) != 0 {
		t.Fatalf("exchange holds %d, locked %d after full redemption", got, f.locked(t))
	}
}

// Split, a complementary fill (maker SELL → ceil), and a merge match
// (maker SELL → ceil, taker gets the complement).
func TestExchangeSplitComplementaryMerge(t *testing.T) {
	f := newExFixture(t)
	rc := f.split(t, f.alice, 100)
	wantExTopics(t, rc, "Transfer", "TransferSingle", "TransferSingle", "PositionSplit")
	if f.position(t, f.alice.addr, 0) != 100 || f.position(t, f.alice.addr, 1) != 100 || f.locked(t) != 100 || f.collateral(t, f.x.addr) != 100 {
		t.Fatal("split state wrong")
	}

	// bob BUY YES @0.70 (taker) vs alice SELL YES @0.60 (maker), fill 7:
	// cost = ceil(4.2) = 5 ≤ ceil(4.9) = 5.
	taker := f.order(t, f.bob, types.OrderSideBuy, 0, 50, 700_000)
	maker := f.order(t, f.alice, types.OrderSideSell, 0, 50, 600_000)
	rc, err := f.match(t, f.operator, taker, []exchangeOrderWire{maker}, 7)
	if err != nil {
		t.Fatalf("complementary: %v", err)
	}
	wantExTopics(t, rc, "Transfer", "TransferSingle", "OrderFilled", "OrderFilled")
	if f.collateral(t, f.bob.addr) != exFunding-5 || f.collateral(t, f.alice.addr) != exFunding-100+5 {
		t.Fatal("complementary collateral wrong")
	}
	if f.position(t, f.bob.addr, 0) != 7 || f.position(t, f.alice.addr, 0) != 93 || f.locked(t) != 100 {
		t.Fatal("complementary positions wrong")
	}
	if f.filled(t, taker) != 7 || f.filled(t, maker) != 7 {
		t.Fatal("filled wrong")
	}

	// Merge: alice SELL NO @0.40 (maker) + bob SELL YES @0.50 (taker), fill
	// 7: maker gets ceil(2.8) = 3, taker gets 4 ≥ floor(3.5) = 3.
	mTaker := f.order(t, f.bob, types.OrderSideSell, 0, 7, 500_000)
	mMaker := f.order(t, f.alice, types.OrderSideSell, 1, 7, 400_000)
	rc, err = f.match(t, f.operator, mTaker, []exchangeOrderWire{mMaker}, 7)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	wantExTopics(t, rc, "TransferSingle", "TransferSingle", "Transfer", "Transfer", "MergeMatched", "OrderFilled", "OrderFilled")
	if f.collateral(t, f.bob.addr) != exFunding-5+4 || f.collateral(t, f.alice.addr) != exFunding-100+5+3 {
		t.Fatal("merge collateral wrong")
	}
	if f.position(t, f.bob.addr, 0) != 0 || f.position(t, f.alice.addr, 1) != 93 || f.locked(t) != 93 || f.collateral(t, f.x.addr) != 93 {
		t.Fatal("merge positions wrong")
	}

	// Merge by the holder releases exactly what was locked.
	rc = &types.Receipt{}
	if err := f.x.Merge(f.ctx(f.alice.addr), f.cond, big.NewInt(93), rc); err != nil {
		t.Fatalf("merge sets: %v", err)
	}
	wantExTopics(t, rc, "TransferSingle", "TransferSingle", "Transfer", "PositionsMerged")
	// alice: −100 split, +5 sold, +3 merged, +93 released; bob: −5 +4.
	if f.collateral(t, f.x.addr) != 0 || f.locked(t) != 0 || f.collateral(t, f.alice.addr) != exFunding+1 || f.collateral(t, f.bob.addr) != exFunding-1 {
		t.Fatal("final balances wrong")
	}
	if err := f.x.Merge(f.ctx(f.alice.addr), f.cond, big.NewInt(1), nil); err == nil {
		t.Fatal("merge without a set accepted")
	} else {
		mustSoft(t, err, "holds 0 of outcome 0")
	}
}

// A signed recipient receives what the order acquires: shares for a BUY,
// collateral for a SELL. A module address as recipient is refused.
func TestExchangeRecipient(t *testing.T) {
	f := newExFixture(t)
	buy := f.build(t, f.alice, types.OrderSideBuy, 0, 10, 600_000)
	buy.Recipient = f.carol.addr
	taker := f.signed(t, f.alice, buy)
	sell := f.build(t, f.bob, types.OrderSideBuy, 1, 10, 400_000)
	sell.Recipient = f.carol.addr
	maker := f.signed(t, f.bob, sell)
	if _, err := f.match(t, f.operator, taker, []exchangeOrderWire{maker}, 10); err != nil {
		t.Fatalf("mint to recipients: %v", err)
	}
	if f.position(t, f.carol.addr, 0) != 10 || f.position(t, f.carol.addr, 1) != 10 || f.position(t, f.alice.addr, 0) != 0 {
		t.Fatal("shares did not go to the recipient")
	}
	// carol sells YES to bob, proceeds to alice.
	so := f.build(t, f.carol, types.OrderSideSell, 0, 10, 500_000)
	so.Recipient = f.alice.addr
	sTaker := f.signed(t, f.carol, so)
	bMaker := f.order(t, f.bob, types.OrderSideBuy, 0, 10, 500_000)
	if _, err := f.match(t, f.operator, sTaker, []exchangeOrderWire{bMaker}, 10); err != nil {
		t.Fatalf("sell with recipient: %v", err)
	}
	if f.collateral(t, f.alice.addr) != exFunding-6+5 || f.collateral(t, f.carol.addr) != exFunding || f.position(t, f.bob.addr, 0) != 10 {
		t.Fatal("sell proceeds did not go to the recipient")
	}
	for _, bad := range []qcrypto.Address{f.x.addr, ModuleRouterAddress} {
		o := f.build(t, f.alice, types.OrderSideBuy, 1, 1, 500_000)
		o.Recipient = bad
		if _, err := f.x.OrderHash(f.signed(t, f.alice, o)); err == nil {
			t.Fatalf("recipient %s accepted", bad)
		}
	}
}

// ── Adversarial ─────────────────────────────────────────────────────────────

func TestExchangeRejectsForgedKey(t *testing.T) {
	f := newExFixture(t)
	maker := f.order(t, f.bob, types.OrderSideBuy, 1, 10, 500_000)
	// bob signs an order naming alice as maker and hands in his own key.
	forged := f.build(t, f.alice, types.OrderSideBuy, 0, 10, 600_000)
	taker := wireOf(forged, f.signWith(t, f.bob, forged, qcrypto.CtxOrder), f.bob.pub.Bytes())
	_, err := f.match(t, f.operator, taker, []exchangeOrderWire{maker}, 10)
	mustSoft(t, err, "maker_pubkey does not derive")
	// No key at all for an unregistered maker.
	taker.MakerPubKey = nil
	_, err = f.match(t, f.operator, taker, []exchangeOrderWire{maker}, 10)
	mustSoft(t, err, "no registered key and no maker_pubkey")
	// Garbage key bytes.
	taker.MakerPubKey = []byte("nope")
	_, err = f.match(t, f.operator, taker, []exchangeOrderWire{maker}, 10)
	mustSoft(t, err, "maker_pubkey for")

	// Registry path: once alice's key is registered, the supplied key is
	// ignored — bob's forgery fails on the signature, and alice's real
	// order verifies with no key (or a garbage key) supplied.
	acc, _ := f.sdb.GetAccount(f.alice.addr)
	acc.PublicKey = f.alice.pub.Bytes()
	f.sdb.SetAccount(acc)
	taker.MakerPubKey = f.bob.pub.Bytes()
	_, err = f.match(t, f.operator, taker, []exchangeOrderWire{maker}, 10)
	mustSoft(t, err, "invalid maker signature")
	real := f.signed(t, f.alice, forged)
	real.MakerPubKey = []byte("ignored")
	if _, err := f.match(t, f.operator, real, []exchangeOrderWire{maker}, 10); err != nil {
		t.Fatalf("registered maker: %v", err)
	}
	if f.collateral(t, f.alice.addr) != exFunding-5 {
		t.Fatal("registered maker's fill did not settle")
	}
}

func TestExchangeRejectsOrderForAnotherExchange(t *testing.T) {
	f := newExFixture(t)
	other, err := DeployExchange(f.sdb, ModuleContext{Caller: f.owner, Nonce: 3, Height: 1}, exchangeDeployParams{Name: "Other"})
	if err != nil {
		t.Fatal(err)
	}
	maker := f.order(t, f.bob, types.OrderSideBuy, 1, 10, 500_000)
	// Signed for the other instance and presented with its address.
	o := f.build(t, f.alice, types.OrderSideBuy, 0, 10, 600_000)
	o.Exchange = other
	taker := f.signed(t, f.alice, o)
	_, err = f.match(t, f.operator, taker, []exchangeOrderWire{maker}, 10)
	mustSoft(t, err, "order is for exchange")
	// Signed for the other instance but relabelled: the digest is computed
	// from the exchange being called, so the signature does not verify.
	taker.Exchange = f.x.addr.String()
	_, err = f.match(t, f.operator, taker, []exchangeOrderWire{maker}, 10)
	mustSoft(t, err, "invalid maker signature")
	if f.filled(t, maker) != 0 || f.collateral(t, f.alice.addr) != exFunding {
		t.Fatal("a refused match moved something")
	}
}

func TestExchangeRejectsWrongContextAndTampering(t *testing.T) {
	f := newExFixture(t)
	maker := f.order(t, f.bob, types.OrderSideBuy, 1, 10, 500_000)
	o := f.build(t, f.alice, types.OrderSideBuy, 0, 10, 600_000)
	underPermit := wireOf(o, f.signWith(t, f.alice, o, qcrypto.CtxPermit), f.alice.pub.Bytes())
	_, err := f.match(t, f.operator, underPermit, []exchangeOrderWire{maker}, 10)
	mustSoft(t, err, "invalid maker signature")

	good := f.signed(t, f.alice, o)
	// Each tampered order still forms a crossing pair with its maker, so the
	// first thing that can fail is the signature (pricing runs before the
	// verifies by design).
	buyYes := f.order(t, f.bob, types.OrderSideBuy, 0, 10, 500_000)
	buyYesHigh := f.order(t, f.bob, types.OrderSideBuy, 0, 10, 700_000)
	for name, tc := range map[string]struct {
		mutate func(w *exchangeOrderWire)
		maker  exchangeOrderWire
	}{
		"price":      {func(w *exchangeOrderWire) { w.Price = "650000" }, maker},
		"shares":     {func(w *exchangeOrderWire) { w.ShareAmount = "11" }, maker},
		"outcome":    {func(w *exchangeOrderWire) { w.Outcome = 1 }, buyYes},
		"side":       {func(w *exchangeOrderWire) { w.Side = 1 }, buyYesHigh},
		"salt":       {func(w *exchangeOrderWire) { w.Salt = strings.Repeat("00", 32) }, maker},
		"expiration": {func(w *exchangeOrderWire) { w.Expiration = exEndTime }, maker},
		"recipient":  {func(w *exchangeOrderWire) { w.Recipient = f.carol.addr.String() }, maker},
		"maker":      {func(w *exchangeOrderWire) { w.Maker = f.carol.addr.String(); w.MakerPubKey = f.carol.pub.Bytes() }, maker},
	} {
		w := good
		tc.mutate(&w)
		_, err := f.match(t, f.operator, w, []exchangeOrderWire{tc.maker}, 10)
		if err == nil {
			t.Errorf("tampered %s accepted", name)
			continue
		}
		mustSoft(t, err, "invalid maker signature")
	}
	if _, err := f.match(t, f.operator, good, []exchangeOrderWire{maker}, 10); err != nil {
		t.Fatalf("the untampered order must fill: %v", err)
	}
}

// The operator gate is the first check: a stranger carrying garbage
// signatures and keys is refused for being a stranger, before any parse or
// verify runs.
func TestExchangeNonOperatorRejectedBeforeVerify(t *testing.T) {
	f := newExFixture(t)
	taker := f.order(t, f.alice, types.OrderSideBuy, 0, 10, 600_000)
	taker.Signature = []byte("garbage")
	taker.MakerPubKey = []byte("garbage")
	maker := f.order(t, f.bob, types.OrderSideBuy, 1, 10, 500_000)
	maker.Salt = "not hex"
	for _, caller := range []qcrypto.Address{f.alice.addr, f.owner, f.carol.addr, qcrypto.Address{}} {
		_, err := f.match(t, caller, taker, []exchangeOrderWire{maker}, 10)
		mustSoft(t, err, "is not an operator")
		if strings.Contains(err.Error(), "signature") || strings.Contains(err.Error(), "salt") {
			t.Fatalf("a non-operator reached validation: %v", err)
		}
	}
	// A revoked operator is a stranger too.
	if err := f.x.SetOperator(f.owner, f.operator, false, nil); err != nil {
		t.Fatal(err)
	}
	_, err := f.match(t, f.operator, taker, []exchangeOrderWire{maker}, 10)
	mustSoft(t, err, "is not an operator")
}

func TestExchangeOverfillAcrossMatchesAndFilledShortCut(t *testing.T) {
	f := newExFixture(t)
	taker := f.order(t, f.alice, types.OrderSideBuy, 0, 100, 600_000)
	a := f.order(t, f.bob, types.OrderSideBuy, 1, 60, 500_000)
	b := f.order(t, f.bob, types.OrderSideBuy, 1, 60, 500_000)
	if _, err := f.match(t, f.operator, taker, []exchangeOrderWire{a}, 60); err != nil {
		t.Fatal(err)
	}
	_, err := f.match(t, f.operator, taker, []exchangeOrderWire{b}, 60)
	mustSoft(t, err, "fills total 60 but only 40 remains")
	if f.filled(t, taker) != 60 || f.filled(t, b) != 0 {
		t.Fatal("a refused match advanced a fill")
	}
	// Maker overfill: 41 against a 60-share order with 60 filled? No — a
	// fresh 60-share maker with a 41 fill against 40 remaining on the taker
	// fails on the taker; make the maker the short side instead.
	c := f.order(t, f.bob, types.OrderSideBuy, 1, 30, 500_000)
	_, err = f.match(t, f.operator, taker, []exchangeOrderWire{c}, 31)
	mustSoft(t, err, "fill 31 exceeds the 30 remaining")

	// The taker's hash is authorised by its earlier fill: signature and key
	// may be omitted now. Cancel/nonce/expiry checks still run.
	short := taker
	short.Signature, short.MakerPubKey = nil, nil
	if _, err := f.match(t, f.operator, short, []exchangeOrderWire{b}, 40); err != nil {
		t.Fatalf("filled short-cut: %v", err)
	}
	if f.filled(t, taker) != 100 {
		t.Fatal("short-cut fill not recorded")
	}
	// A never-filled order with no signature is refused.
	d := f.order(t, f.bob, types.OrderSideBuy, 1, 5, 500_000)
	d.Signature = nil
	e := f.order(t, f.carol, types.OrderSideBuy, 0, 5, 600_000)
	_, err = f.match(t, f.operator, e, []exchangeOrderWire{d}, 5)
	mustSoft(t, err, "invalid maker signature")
}

func TestExchangeDuplicateHashInOneMatch(t *testing.T) {
	f := newExFixture(t)
	taker := f.order(t, f.alice, types.OrderSideBuy, 0, 100, 600_000)
	a := f.order(t, f.bob, types.OrderSideBuy, 1, 60, 500_000)
	_, err := f.match(t, f.operator, taker, []exchangeOrderWire{a, a}, 10, 10)
	mustSoft(t, err, "maker_orders[1] duplicates maker_orders[0]")
	_, err = f.match(t, f.operator, taker, []exchangeOrderWire{a, taker}, 10, 10)
	mustSoft(t, err, "maker_orders[1] is the taker order")
	if f.filled(t, taker) != 0 || f.filled(t, a) != 0 {
		t.Fatal("a refused match advanced a fill")
	}
	// Two makers with distinct salts are fine, mixed kinds included: the
	// taker BUY YES fills a BUY NO (mint) and a SELL YES (complementary).
	f.split(t, f.carol, 20)
	b := f.order(t, f.carol, types.OrderSideSell, 0, 20, 550_000)
	rc, err := f.match(t, f.operator, taker, []exchangeOrderWire{a, b}, 10, 10)
	if err != nil {
		t.Fatalf("mixed kinds: %v", err)
	}
	wantExTopics(t, rc, "Transfer", "Transfer", "TransferSingle", "TransferSingle", "MintMatched", "OrderFilled", "OrderFilled",
		"Transfer", "TransferSingle", "OrderFilled", "OrderFilled")
	if f.filled(t, taker) != 20 || f.filled(t, a) != 10 || f.filled(t, b) != 10 || f.position(t, f.alice.addr, 0) != 20 {
		t.Fatal("mixed-kind fills wrong")
	}
	// mint: bob pays 5, alice 5; complementary: alice pays ceil(5.5) = 6.
	if f.collateral(t, f.alice.addr) != exFunding-5-6 || f.collateral(t, f.carol.addr) != exFunding-20+6 {
		t.Fatal("mixed-kind collateral wrong")
	}
}

func TestExchangeZeroCostFillRejected(t *testing.T) {
	f := newExFixture(t)
	f.split(t, f.alice, 10)
	// Maker BUY @0.60 fill 1 → floor(0.6) = 0.
	maker := f.order(t, f.bob, types.OrderSideBuy, 0, 10, 600_000)
	taker := f.order(t, f.alice, types.OrderSideSell, 0, 10, 500_000)
	_, err := f.match(t, f.operator, taker, []exchangeOrderWire{maker}, 1)
	mustSoft(t, err, "zero-cost fill")
	// Mint with a maker at 0.5 and fill 1 → floor(0.5) = 0 on the maker leg.
	mMaker := f.order(t, f.bob, types.OrderSideBuy, 1, 10, 500_000)
	mTaker := f.order(t, f.carol, types.OrderSideBuy, 0, 10, 600_000)
	_, err = f.match(t, f.operator, mTaker, []exchangeOrderWire{mMaker}, 1)
	mustSoft(t, err, "zero-cost fill")
	// Fill 2 makes both legs 1.
	if _, err := f.match(t, f.operator, mTaker, []exchangeOrderWire{mMaker}, 2); err != nil {
		t.Fatalf("fill 2: %v", err)
	}
	// Non-crossing pairs and impossible pairs are refused with their own
	// reasons.
	_, err = f.match(t, f.operator, f.order(t, f.carol, types.OrderSideBuy, 0, 10, 400_000), []exchangeOrderWire{mMaker}, 2)
	mustSoft(t, err, "do not sum to")
	_, err = f.match(t, f.operator, f.order(t, f.carol, types.OrderSideBuy, 0, 10, 500_000), []exchangeOrderWire{f.order(t, f.bob, types.OrderSideSell, 0, 10, 600_000)}, 2)
	mustSoft(t, err, "below maker ask")
	_, err = f.match(t, f.operator, f.order(t, f.carol, types.OrderSideBuy, 0, 10, 500_000), []exchangeOrderWire{f.order(t, f.bob, types.OrderSideBuy, 0, 10, 600_000)}, 2)
	mustSoft(t, err, "same side and outcome")
	_, err = f.match(t, f.operator, f.order(t, f.carol, types.OrderSideBuy, 0, 10, 500_000), []exchangeOrderWire{f.order(t, f.bob, types.OrderSideSell, 1, 10, 600_000)}, 2)
	mustSoft(t, err, "different outcomes do not cross")
}

// The rounding rule, over 10k random pairs: the maker settles at its price
// rounded in its favour, the taker never outside its documented bound, mint
// and merge legs sum exactly to the fill, and every crossing pair is either
// filled or refused only as zero-cost.
func TestSettlePairRoundingProperty(t *testing.T) {
	rng := rand.New(rand.NewSource(0x5e771e))
	scale := big.NewInt(ExchangePriceScale)
	realCost := func(price, fill *big.Int) *big.Rat { // price·fill/1e6 exactly
		return new(big.Rat).SetFrac(new(big.Int).Mul(price, fill), scale)
	}
	le := func(a *big.Int, b *big.Rat) bool { return new(big.Rat).SetInt(a).Cmp(b) <= 0 }
	ge := func(a *big.Int, b *big.Rat) bool { return new(big.Rat).SetInt(a).Cmp(b) >= 0 }
	randPrice := func() *big.Int { return big.NewInt(1 + rng.Int63n(ExchangePriceScale-1)) }
	randFill := func() *big.Int {
		switch rng.Intn(3) {
		case 0:
			return big.NewInt(1 + rng.Int63n(20))
		case 1:
			return big.NewInt(1 + rng.Int63n(1_000_000))
		default:
			return new(big.Int).Add(big.NewInt(1e18), big.NewInt(rng.Int63n(1e18)))
		}
	}
	filled, refused := 0, 0
	for i := 0; i < 10_000; i++ {
		fill := randFill()
		kind := rng.Intn(4)
		var taker, maker orderTerms
		switch kind {
		case 0: // maker SELL, taker BUY, same outcome
			maker = orderTerms{Side: types.OrderSideSell, Outcome: 0, Price: randPrice()}
			taker = orderTerms{Side: types.OrderSideBuy, Outcome: 0, Price: randPrice()}
			if rng.Intn(4) == 0 {
				taker.Price = new(big.Int).Set(maker.Price) // equal-price cross
			}
		case 1: // maker BUY, taker SELL
			maker = orderTerms{Side: types.OrderSideBuy, Outcome: 1, Price: randPrice()}
			taker = orderTerms{Side: types.OrderSideSell, Outcome: 1, Price: randPrice()}
			if rng.Intn(4) == 0 {
				taker.Price = new(big.Int).Set(maker.Price)
			}
		case 2: // mint
			maker = orderTerms{Side: types.OrderSideBuy, Outcome: 0, Price: randPrice()}
			taker = orderTerms{Side: types.OrderSideBuy, Outcome: 1, Price: randPrice()}
			if rng.Intn(4) == 0 {
				taker.Price = new(big.Int).Sub(scale, maker.Price) // sum == 1e6 exactly
			}
		default: // merge
			maker = orderTerms{Side: types.OrderSideSell, Outcome: 0, Price: randPrice()}
			taker = orderTerms{Side: types.OrderSideSell, Outcome: 1, Price: randPrice()}
			if rng.Intn(4) == 0 {
				taker.Price = new(big.Int).Sub(scale, maker.Price)
			}
		}
		crossing := false
		sum := new(big.Int).Add(taker.Price, maker.Price)
		switch kind {
		case 0:
			crossing = taker.Price.Cmp(maker.Price) >= 0
		case 1:
			crossing = maker.Price.Cmp(taker.Price) >= 0
		case 2:
			crossing = sum.Cmp(scale) >= 0
		default:
			crossing = sum.Cmp(scale) <= 0
		}
		s, err := settlePair(taker, maker, fill)
		if !crossing {
			if err == nil {
				t.Fatalf("case %d: non-crossing pair %s/%s filled", kind, taker.Price, maker.Price)
			}
			continue
		}
		if err != nil {
			if !strings.Contains(err.Error(), "zero-cost") {
				t.Fatalf("case %d: crossing pair refused: %v (taker %s maker %s fill %s)", kind, err, taker.Price, maker.Price, fill)
			}
			refused++
			continue
		}
		filled++
		mReal, tReal := realCost(maker.Price, fill), realCost(taker.Price, fill)
		if s.makerCost.Sign() <= 0 || s.takerCost.Sign() <= 0 {
			t.Fatal("zero leg filled")
		}
		switch maker.Side {
		case types.OrderSideSell: // maker receives ≥ its ask
			if !ge(s.makerCost, mReal) {
				t.Fatalf("maker SELL received %s < %s", s.makerCost, mReal.FloatString(3))
			}
		default: // maker pays ≤ its bid
			if !le(s.makerCost, mReal) {
				t.Fatalf("maker BUY paid %s > %s", s.makerCost, mReal.FloatString(3))
			}
		}
		switch taker.Side {
		case types.OrderSideBuy: // taker pays ≤ ceil(bid)
			if s.takerCost.Cmp(mulDivCeil(taker.Price, fill)) > 0 {
				t.Fatalf("taker BUY paid %s > ceil(%s)", s.takerCost, tReal.FloatString(3))
			}
		default: // taker receives ≥ floor(ask)
			if s.takerCost.Cmp(mulDivFloor(taker.Price, fill)) < 0 {
				t.Fatalf("taker SELL received %s < floor(%s)", s.takerCost, tReal.FloatString(3))
			}
		}
		if s.kind == matchMint || s.kind == matchMerge {
			if new(big.Int).Add(s.makerCost, s.takerCost).Cmp(fill) != 0 {
				t.Fatalf("mint/merge legs %s + %s != fill %s", s.makerCost, s.takerCost, fill)
			}
		} else if s.makerCost.Cmp(s.takerCost) != 0 {
			t.Fatal("complementary legs differ")
		}
		// The taker's tolerance is at most one unit past its exact bound.
		one := big.NewRat(1, 1)
		if taker.Side == types.OrderSideBuy && !le(s.takerCost, new(big.Rat).Add(tReal, one)) {
			t.Fatal("taker overpaid by more than one unit")
		}
		if taker.Side == types.OrderSideSell && !ge(s.takerCost, new(big.Rat).Sub(tReal, one)) {
			t.Fatal("taker underpaid by more than one unit")
		}
	}
	if filled < 4000 || refused == 0 {
		t.Fatalf("filled %d, refused %d: the sample is not exercising both paths", filled, refused)
	}
	// Equal-price crosses fill at every size (ceil ≥ 1); an exactly-crossing
	// mint fills at every size once its floor leg is non-zero (fill ≥ 4 at
	// 0.333333), and is refused only as zero-cost below that.
	for fill := int64(1); fill < 50; fill++ {
		if _, err := settlePair(orderTerms{Side: 0, Outcome: 0, Price: big.NewInt(333_333)}, orderTerms{Side: 1, Outcome: 0, Price: big.NewInt(333_333)}, big.NewInt(fill)); err != nil {
			t.Fatalf("equal-price cross at fill %d: %v", fill, err)
		}
		_, err := settlePair(orderTerms{Side: 0, Outcome: 1, Price: big.NewInt(666_667)}, orderTerms{Side: 0, Outcome: 0, Price: big.NewInt(333_333)}, big.NewInt(fill))
		if fill >= 4 && err != nil {
			t.Fatalf("exact mint at fill %d: %v", fill, err)
		}
		if fill < 4 && (err == nil || !strings.Contains(err.Error(), "zero-cost")) {
			t.Fatalf("exact mint at fill %d: want zero-cost, got %v", fill, err)
		}
	}
	// The documented example: maker SELL @333333, taker BUY @333333, fill 7
	// → cost ceil(2.33) = 3.
	s, err := settlePair(orderTerms{Side: 0, Outcome: 0, Price: big.NewInt(333_333)}, orderTerms{Side: 1, Outcome: 0, Price: big.NewInt(333_333)}, big.NewInt(7))
	if err != nil || s.makerCost.Int64() != 3 {
		t.Fatalf("documented example: %+v %v", s, err)
	}
}

// Swapping who is labelled taker changes the settled price (the maker's) but
// never puts either side outside its own signed bound.
func TestExchangeRelabellingStaysInsideBounds(t *testing.T) {
	for _, first := range []bool{true, false} {
		f := newExFixture(t)
		a := f.order(t, f.alice, types.OrderSideBuy, 0, 7, 630_000) // bound ceil(4.41) = 5
		b := f.order(t, f.bob, types.OrderSideBuy, 1, 7, 450_000)   // bound ceil(3.15) = 4
		var err error
		if first {
			_, err = f.match(t, f.operator, a, []exchangeOrderWire{b}, 7) // maker bob pays floor(3.15) = 3, alice 4
		} else {
			_, err = f.match(t, f.operator, b, []exchangeOrderWire{a}, 7) // maker alice pays floor(4.41) = 4, bob 3
		}
		if err != nil {
			t.Fatalf("taker=alice %v: %v", first, err)
		}
		alicePaid, bobPaid := exFunding-f.collateral(t, f.alice.addr), exFunding-f.collateral(t, f.bob.addr)
		if alicePaid+bobPaid != 7 || alicePaid > 5 || bobPaid > 4 || alicePaid < 1 || bobPaid < 1 {
			t.Fatalf("taker=alice %v: alice paid %d, bob paid %d", first, alicePaid, bobPaid)
		}
		if f.position(t, f.alice.addr, 0) != 7 || f.position(t, f.bob.addr, 1) != 7 || f.locked(t) != 7 {
			t.Fatal("positions wrong")
		}
	}
}

func TestExchangeCancelNonceExpiry(t *testing.T) {
	f := newExFixture(t)
	maker := f.order(t, f.bob, types.OrderSideBuy, 1, 100, 500_000)
	cancelParams := func(o exchangeOrderWire, sig, pub []byte) exchangeCancelParams {
		return exchangeCancelParams{Exchange: f.x.addr.String(), Order: o, CancelSignature: sig, MakerPubKey: pub}
	}

	// Cancel: a stranger without a signature is refused; with a wrong
	// signature refused; with the maker's CancelDigest signature accepted.
	victim := f.order(t, f.alice, types.OrderSideBuy, 0, 10, 600_000)
	err := f.x.CancelOrder(f.ctx(f.carol.addr), cancelParams(victim, nil, nil), nil)
	mustSoft(t, err, "carries no cancel_signature")
	digest := types.CancelDigest(f.hash(t, victim))
	wrongCtx, _ := qcrypto.Sign(f.alice.priv, digest[:], qcrypto.CtxPermit)
	err = f.x.CancelOrder(f.ctx(f.carol.addr), cancelParams(victim, wrongCtx, nil), nil)
	mustSoft(t, err, "invalid cancel_signature")
	byBob, _ := qcrypto.Sign(f.bob.priv, digest[:], qcrypto.CtxOrder)
	err = f.x.CancelOrder(f.ctx(f.carol.addr), cancelParams(victim, byBob, f.bob.pub.Bytes()), nil)
	mustSoft(t, err, "maker_pubkey does not derive")
	if cancelled, _ := f.x.OrderCancelled(f.hash(t, victim)); cancelled {
		t.Fatal("a refused cancel took effect")
	}
	good, _ := qcrypto.Sign(f.alice.priv, digest[:], qcrypto.CtxOrder)
	rc := &types.Receipt{}
	// The key comes from the order object when the cancel carries none.
	if err := f.x.CancelOrder(f.ctx(f.carol.addr), cancelParams(victim, good, nil), rc); err != nil {
		t.Fatalf("signed cancel by a third party: %v", err)
	}
	wantExTopics(t, rc, "OrderCancelled")
	if rc.Logs[0].Topics[1] != f.hash(t, victim) || rc.Logs[0].Topics[2] != AddrTopic(f.alice.addr) {
		t.Fatal("OrderCancelled layout wrong")
	}
	// Idempotent: no error, no second event.
	rc = &types.Receipt{}
	if err := f.x.CancelOrder(f.ctx(f.alice.addr), cancelParams(victim, nil, nil), rc); err != nil || len(rc.Logs) != 0 {
		t.Fatalf("second cancel: %v, %d logs", err, len(rc.Logs))
	}
	_, err = f.match(t, f.operator, victim, []exchangeOrderWire{maker}, 10)
	mustSoft(t, err, "is cancelled")
	// The maker and the operator cancel without a signature.
	m2 := f.order(t, f.bob, types.OrderSideBuy, 1, 5, 500_000)
	if err := f.x.CancelOrder(f.ctx(f.operator), cancelParams(m2, nil, nil), nil); err != nil {
		t.Fatalf("operator cancel: %v", err)
	}
	m3 := f.order(t, f.bob, types.OrderSideBuy, 1, 5, 500_000)
	if err := f.x.CancelOrder(f.ctx(f.bob.addr), cancelParams(m3, nil, nil), nil); err != nil {
		t.Fatalf("maker cancel: %v", err)
	}

	// Nonce: an order at the old nonce dies with the bump; the signed
	// variant binds expected_nonce so a captured signature is single-use.
	stale := f.order(t, f.alice, types.OrderSideBuy, 0, 10, 600_000)
	nonceParams := func(expected uint64, sig, pub []byte) exchangeNonceParams {
		return exchangeNonceParams{Exchange: f.x.addr.String(), Maker: f.alice.addr.String(), ExpectedNonce: expected, Signature: sig, MakerPubKey: pub}
	}
	err = f.x.IncrementNonce(f.ctx(f.carol.addr), nonceParams(0, nil, nil), nil)
	mustSoft(t, err, "carries no signature")
	err = f.x.IncrementNonce(f.ctx(f.alice.addr), nonceParams(1, nil, nil), nil)
	mustSoft(t, err, "expected_nonce 1 but maker")
	nd, _ := types.IncrementNonceDigest(types.ChainID, f.x.addr, f.alice.addr, 0)
	nsig, _ := qcrypto.Sign(f.alice.priv, nd[:], qcrypto.CtxOrder)
	err = f.x.IncrementNonce(f.ctx(f.carol.addr), nonceParams(0, nsig, f.bob.pub.Bytes()), nil)
	mustSoft(t, err, "maker_pubkey does not derive")
	rc = &types.Receipt{}
	if err := f.x.IncrementNonce(f.ctx(f.carol.addr), nonceParams(0, nsig, f.alice.pub.Bytes()), rc); err != nil {
		t.Fatalf("signed increment_nonce: %v", err)
	}
	wantExTopics(t, rc, "NonceIncremented")
	if n, _ := f.x.MakerNonce(f.alice.addr); n != 1 {
		t.Fatalf("nonce = %d", n)
	}
	// Replaying the captured signature: the bound nonce no longer matches.
	err = f.x.IncrementNonce(f.ctx(f.carol.addr), nonceParams(0, nsig, f.alice.pub.Bytes()), nil)
	mustSoft(t, err, "expected_nonce 0 but maker")
	err = f.x.IncrementNonce(f.ctx(f.carol.addr), nonceParams(1, nsig, f.alice.pub.Bytes()), nil)
	mustSoft(t, err, "invalid signature")
	_, err = f.match(t, f.operator, stale, []exchangeOrderWire{maker}, 10)
	mustSoft(t, err, "carries nonce 0 but maker")
	// The maker bumps its own nonce with no signature.
	if err := f.x.IncrementNonce(f.ctx(f.alice.addr), nonceParams(1, nil, nil), nil); err != nil {
		t.Fatal(err)
	}
	fresh := f.order(t, f.alice, types.OrderSideBuy, 0, 10, 600_000)
	if fresh.Nonce != 2 {
		t.Fatalf("fresh order nonce = %d", fresh.Nonce)
	}
	if _, err := f.match(t, f.operator, fresh, []exchangeOrderWire{maker}, 10); err != nil {
		t.Fatalf("order at the new nonce: %v", err)
	}

	// Expiry: equal to the block time fills, one second earlier is expired;
	// 0 never expires.
	atTime := f.build(t, f.alice, types.OrderSideBuy, 0, 10, 600_000)
	atTime.Expiration = f.time
	if _, err := f.match(t, f.operator, f.signed(t, f.alice, atTime), []exchangeOrderWire{maker}, 10); err != nil {
		t.Fatalf("expiration == block time: %v", err)
	}
	expired := f.build(t, f.alice, types.OrderSideBuy, 0, 10, 600_000)
	expired.Expiration = f.time - 1
	_, err = f.match(t, f.operator, f.signed(t, f.alice, expired), []exchangeOrderWire{maker}, 10)
	mustSoft(t, err, "expired at")
	never := f.build(t, f.alice, types.OrderSideBuy, 0, 10, 600_000)
	never.Expiration = 0
	f.time = exEndTime - 1
	if _, err := f.match(t, f.operator, f.signed(t, f.alice, never), []exchangeOrderWire{maker}, 10); err != nil {
		t.Fatalf("expiration 0 must never expire: %v", err)
	}
	// fresh, atTime and never filled 10 each; victim and stale were refused.
	if f.filled(t, maker) != 30 {
		t.Fatalf("maker filled = %d", f.filled(t, maker))
	}
}

// match and split stop at end_time and after resolution; merge runs until
// resolution; pause blocks only match and split.
func TestExchangeLifecycleGates(t *testing.T) {
	f := newExFixture(t)
	f.split(t, f.alice, 100)
	taker := f.order(t, f.alice, types.OrderSideSell, 0, 10, 500_000)
	maker := f.order(t, f.bob, types.OrderSideBuy, 0, 10, 600_000)
	cancel := exchangeCancelParams{Exchange: f.x.addr.String(), Order: f.order(t, f.carol, types.OrderSideBuy, 0, 1, 500_000)}

	// Paused.
	if err := f.x.Pause(f.owner, nil); err != nil {
		t.Fatal(err)
	}
	_, err := f.match(t, f.operator, taker, []exchangeOrderWire{maker}, 10)
	mustSoft(t, err, "exchange is paused")
	err = f.x.Split(f.ctx(f.alice.addr), f.cond, big.NewInt(1), nil)
	mustSoft(t, err, "exchange is paused")
	if err := f.x.Merge(f.ctx(f.alice.addr), f.cond, big.NewInt(10), nil); err != nil {
		t.Fatalf("merge while paused: %v", err)
	}
	if err := f.x.CancelOrder(f.ctx(f.carol.addr), cancel, nil); err != nil {
		t.Fatalf("cancel while paused: %v", err)
	}
	if err := f.x.IncrementNonce(f.ctx(f.carol.addr), exchangeNonceParams{Exchange: f.x.addr.String(), Maker: f.carol.addr.String()}, nil); err != nil {
		t.Fatalf("increment_nonce while paused: %v", err)
	}
	if err := f.x.Unpause(f.owner, nil); err != nil {
		t.Fatal(err)
	}

	// At end_time.
	f.time = exEndTime
	_, err = f.match(t, f.operator, taker, []exchangeOrderWire{maker}, 10)
	mustSoft(t, err, "has ended")
	err = f.x.Split(f.ctx(f.alice.addr), f.cond, big.NewInt(1), nil)
	mustSoft(t, err, "has ended")
	if err := f.x.Merge(f.ctx(f.alice.addr), f.cond, big.NewInt(10), nil); err != nil {
		t.Fatalf("merge at end_time: %v", err)
	}
	f.time = exEndTime - 1
	if _, err := f.match(t, f.operator, taker, []exchangeOrderWire{maker}, 10); err != nil {
		t.Fatalf("one second before end_time: %v", err)
	}
	if err := f.x.Redeem(f.ctx(f.alice.addr), f.cond, f.alice.addr, nil); err == nil {
		t.Fatal("redeem before resolution")
	} else {
		mustSoft(t, err, "is not resolved")
	}

	// Resolved.
	f.resolve(t, 1)
	_, err = f.match(t, f.operator, taker, []exchangeOrderWire{maker}, 10)
	mustSoft(t, err, "is resolved")
	err = f.x.Split(f.ctx(f.alice.addr), f.cond, big.NewInt(1), nil)
	mustSoft(t, err, "is resolved")
	err = f.x.Merge(f.ctx(f.alice.addr), f.cond, big.NewInt(1), nil)
	mustSoft(t, err, "is resolved")
	// Redeem is never gated, not even by pause.
	if err := f.x.Pause(f.owner, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.x.Redeem(f.ctx(f.bob.addr), f.cond, f.alice.addr, nil); err != nil {
		t.Fatalf("redeem while paused: %v", err)
	}
	// alice: 100 split, merged 20, sold 10 YES → 80 NO win → +80; paid 100,
	// got 20 back, got ceil(6) = 6 from bob.
	if got := f.collateral(t, f.alice.addr); got != exFunding-100+20+6+80 {
		t.Fatalf("alice = %d", got)
	}
	if f.collateral(t, f.x.addr) != 0 || f.locked(t) != 0 {
		t.Fatal("exchange not drained")
	}
	if f.position(t, f.bob.addr, 0) != 10 {
		t.Fatal("losing shares are never burned")
	}
}

// A match that fails while applying — a maker without collateral, a paused
// collateral token, a seller without shares — reverts every write it made,
// including fills recorded for earlier pairs and the logs it appended.
func TestExchangeFailedMatchLeavesNoResidue(t *testing.T) {
	f := newExFixture(t)
	broke := newExTrader(t) // never funded
	taker := f.order(t, f.alice, types.OrderSideBuy, 0, 100, 600_000)
	good := f.order(t, f.bob, types.OrderSideBuy, 1, 50, 500_000)
	bad := f.order(t, broke, types.OrderSideBuy, 1, 50, 500_000)

	snapshot := func() [5]int64 {
		return [5]int64{f.collateral(t, f.alice.addr), f.collateral(t, f.bob.addr), f.collateral(t, f.x.addr), f.locked(t), f.position(t, f.alice.addr, 0)}
	}
	before := snapshot()
	rc, err := f.match(t, f.operator, taker, []exchangeOrderWire{good, bad}, 10, 10)
	mustSoft(t, err, "maker_orders[1]: collateral: QRC20 transfer: insufficient balance")
	if len(rc.Logs) != 0 {
		t.Fatalf("a failed match left %d logs", len(rc.Logs))
	}
	if snapshot() != before {
		t.Fatal("a failed match moved balances")
	}
	if f.filled(t, taker) != 0 || f.filled(t, good) != 0 || f.filled(t, bad) != 0 {
		t.Fatal("a failed match advanced a fill (the first pair had applied)")
	}

	// Paused collateral.
	if err := f.tok.Pause(f.owner); err != nil {
		t.Fatal(err)
	}
	rc, err = f.match(t, f.operator, taker, []exchangeOrderWire{good}, 10)
	mustSoft(t, err, "collateral: QRC20: contract is paused")
	if len(rc.Logs) != 0 || snapshot() != before || f.filled(t, taker) != 0 {
		t.Fatal("paused-collateral match left residue")
	}
	err = f.x.Split(f.ctx(f.alice.addr), f.cond, big.NewInt(1), nil)
	mustSoft(t, err, "collateral: QRC20: contract is paused")
	if err := f.tok.Unpause(f.owner); err != nil {
		t.Fatal(err)
	}

	// A seller without the shares.
	seller := f.order(t, f.carol, types.OrderSideSell, 0, 10, 500_000)
	rc, err = f.match(t, f.operator, taker, []exchangeOrderWire{seller}, 10)
	mustSoft(t, err, "holds 0 of outcome 0, needs 10")
	if len(rc.Logs) != 0 || snapshot() != before {
		t.Fatal("short-seller match left residue")
	}
	// And the same match succeeds once it can apply.
	if _, err := f.match(t, f.operator, taker, []exchangeOrderWire{good}, 10); err != nil {
		t.Fatalf("after unpause: %v", err)
	}
}

// The position collection lives at the exchange with owner == exchange: the
// router cannot mint, transfer or burn it for any caller.
func TestExchangePositionsRouterProtected(t *testing.T) {
	f := newExFixture(t)
	f.split(t, f.alice, 10)
	id := ExchangePositionID(f.cond, 0).String()
	for _, tc := range []struct {
		caller qcrypto.Address
		action string
		p      qrc1155Action
		want   string
	}{
		{f.owner, "mint", qrc1155Action{Collection: f.x.addr.String(), To: f.owner.String(), ID: id, Amount: "1"}, "not owner"},
		{f.operator, "mint", qrc1155Action{Collection: f.x.addr.String(), To: f.operator.String(), ID: id, Amount: "1"}, "not owner"},
		{f.alice.addr, "transfer", qrc1155Action{Collection: f.x.addr.String(), To: f.bob.addr.String(), ID: id, Amount: "1"}, "managed by a native module"},
		{f.alice.addr, "burn", qrc1155Action{Collection: f.x.addr.String(), ID: id, Amount: "1"}, "managed by a native module"},
		{f.owner, "transfer_ownership", qrc1155Action{Collection: f.x.addr.String(), NewOwner: f.owner.String()}, "not owner"},
	} {
		err := DispatchModuleCall(f.sdb, f.ctx(tc.caller), moduleData(t, "qrc1155", tc.action, tc.p), &types.Receipt{})
		mustContain(t, err, tc.want)
	}
	if f.position(t, f.alice.addr, 0) != 10 || f.position(t, f.bob.addr, 0) != 0 {
		t.Fatal("router moved a position")
	}
	if managed, _ := f.pos.IsModuleManaged(); !managed {
		t.Fatal("router changed the collection owner")
	}
	// The read side reports the layout: id = condition*256 + outcome.
	if ExchangePositionID(f.cond, 1).Int64() != int64(f.cond)*256+1 {
		t.Fatal("position id layout")
	}
	if ExchangePositionID(42, 1).String() != "10753" {
		t.Fatal("position id golden")
	}
}

func TestExchangeRedeemFeeGolden(t *testing.T) {
	f := newExFixture(t)
	if err := f.x.SetRedeemFeeBps(f.owner, 250, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.x.SetFeeRecipient(f.owner, f.carol.addr, nil); err != nil {
		t.Fatal(err)
	}
	f.split(t, f.alice, 190_909_090)
	f.resolve(t, 0)
	rc := &types.Receipt{}
	if err := f.x.Redeem(f.ctx(f.bob.addr), f.cond, f.alice.addr, rc); err != nil {
		t.Fatalf("redeem_for: %v", err)
	}
	wantExTopics(t, rc, "TransferSingle", "Transfer", "Transfer", "RedeemFeeCharged", "Redeemed")
	if got := f.collateral(t, f.alice.addr); got != exFunding-190_909_090+186_136_363 {
		t.Fatalf("alice payout = %d, want 186136363", got-(exFunding-190_909_090))
	}
	if got := f.collateral(t, f.carol.addr); got != exFunding+4_772_727 {
		t.Fatalf("fee = %d, want 4772727", got-exFunding)
	}
	if f.collateral(t, f.x.addr) != 0 || f.locked(t) != 0 {
		t.Fatal("paid + fee != burned")
	}
	redeemed := rc.Logs[4]
	if redeemed.Topics[1] != AddrTopic(f.alice.addr) || string(redeemed.Data) != string(append(append(word(int64(f.cond)), word(190_909_090)...), word(186_136_363)...)) {
		t.Fatal("Redeemed layout wrong")
	}
	fee := rc.Logs[3]
	if fee.Topics[1] != AddrTopic(f.carol.addr) || string(fee.Data) != string(word(4_772_727)) {
		t.Fatal("RedeemFeeCharged layout wrong")
	}
	// No fee event when the fee rounds to zero.
	f2 := newExFixture(t)
	if err := f2.x.SetRedeemFeeBps(f2.owner, 250, nil); err != nil {
		t.Fatal(err)
	}
	f2.split(t, f2.alice, 3)
	f2.resolve(t, 1)
	rc = &types.Receipt{}
	if err := f2.x.Redeem(f2.ctx(f2.alice.addr), f2.cond, f2.alice.addr, rc); err != nil {
		t.Fatal(err)
	}
	wantExTopics(t, rc, "TransferSingle", "Transfer", "Redeemed")
	if f2.collateral(t, f2.alice.addr) != exFunding {
		t.Fatal("a zero fee still charged")
	}
}

func TestExchangeMatchCaps(t *testing.T) {
	f := newExFixture(t)
	taker := f.order(t, f.alice, types.OrderSideBuy, 0, 1000, 600_000)
	makers := make([]exchangeOrderWire, MaxExchangeMakers+1)
	fills := make([]int64, MaxExchangeMakers+1)
	for i := range makers {
		makers[i] = f.order(t, f.bob, types.OrderSideBuy, 1, 10, 500_000)
		fills[i] = 10
	}
	_, err := f.match(t, f.operator, taker, makers, fills...)
	mustSoft(t, err, "17 maker orders exceed the cap of 16")
	_, err = f.match(t, f.operator, taker, makers[:16], fills[:15]...)
	mustSoft(t, err, "fill_shares has 15 entries for 16 maker orders")
	_, err = f.match(t, f.operator, taker, nil)
	mustSoft(t, err, "maker_orders is empty")
	rc, err := f.match(t, f.operator, taker, makers[:16], fills[:16]...)
	if err != nil {
		t.Fatalf("16 makers: %v", err)
	}
	if len(rc.Logs) != 16*7 || f.filled(t, taker) != 160 {
		t.Fatalf("16 makers: %d logs, filled %d", len(rc.Logs), f.filled(t, taker))
	}
	// Wire strictness on the fill and order fields.
	bad := f.order(t, f.bob, types.OrderSideBuy, 1, 10, 500_000)
	for name, w := range map[string]exchangeOrderWire{
		"price 0":         func() exchangeOrderWire { w := bad; w.Price = "0"; return w }(),
		"price 1e6":       func() exchangeOrderWire { w := bad; w.Price = "1000000"; return w }(),
		"price +1":        func() exchangeOrderWire { w := bad; w.Price = "+1"; return w }(),
		"shares 0":        func() exchangeOrderWire { w := bad; w.ShareAmount = "0"; return w }(),
		"shares leading0": func() exchangeOrderWire { w := bad; w.ShareAmount = "010"; return w }(),
		"side 2":          func() exchangeOrderWire { w := bad; w.Side = 2; return w }(),
		"outcome 2":       func() exchangeOrderWire { w := bad; w.Outcome = 2; return w }(),
		"salt short":      func() exchangeOrderWire { w := bad; w.Salt = "ab"; return w }(),
		"salt upper":      func() exchangeOrderWire { w := bad; w.Salt = strings.ToUpper(w.Salt); return w }(),
		"maker zero":      func() exchangeOrderWire { w := bad; w.Maker = qcrypto.Address{}.String(); return w }(),
		"condition 0":     func() exchangeOrderWire { w := bad; w.Condition = 0; return w }(),
		"condition 9":     func() exchangeOrderWire { w := bad; w.Condition = 9; return w }(),
		"expiration -1":   func() exchangeOrderWire { w := bad; w.Expiration = -1; return w }(),
	} {
		if _, err := f.match(t, f.operator, taker, []exchangeOrderWire{w}, 1); err == nil {
			t.Errorf("%s: accepted", name)
		} else if IsHardModuleError(err) || !strings.HasPrefix(err.Error(), "exchange match: ") {
			t.Errorf("%s: %v", name, err)
		}
	}
	for _, fill := range []string{"0", "-1", "01", "", "x"} {
		rc := &types.Receipt{}
		err := f.x.Match(f.ctx(f.operator), exchangeMatchParams{Exchange: f.x.addr.String(), TakerOrder: taker, MakerOrders: []exchangeOrderWire{bad}, FillShares: []string{fill}}, rc)
		mustSoft(t, err, "fill_shares[0]")
	}
	if f.filled(t, taker) != 160 {
		t.Fatal("a refused match advanced the taker")
	}
}

// Every action refuses an address nothing is deployed at, and split/merge
// refuse a non-positive amount, so a mistyped exchange is never a fresh
// empty instance.
func TestExchangeUndeployedAddressRefused(t *testing.T) {
	f := newExFixture(t)
	ghost := NewExchange(f.sdb, addr(0x99))
	for name, err := range map[string]error{
		"pause":        ghost.Pause(f.owner, nil),
		"set_operator": ghost.SetOperator(f.owner, f.operator, true, nil),
		"split":        ghost.Split(f.ctx(f.alice.addr), 1, big.NewInt(1), nil),
		"merge":        ghost.Merge(f.ctx(f.alice.addr), 1, big.NewInt(1), nil),
		"resolve":      ghost.Resolve(f.ctx(f.resolver), 1, 0, nil),
		"redeem":       ghost.Redeem(f.ctx(f.alice.addr), 1, f.alice.addr, nil),
		"cancel_order": ghost.CancelOrder(f.ctx(f.alice.addr), exchangeCancelParams{}, nil),
		"increment":    ghost.IncrementNonce(f.ctx(f.alice.addr), exchangeNonceParams{}, nil),
		"match":        ghost.Match(f.ctx(f.operator), exchangeMatchParams{}, nil),
	} {
		if err == nil {
			t.Fatalf("%s on a ghost accepted", name)
		}
		mustSoft(t, err, "no exchange is deployed at")
	}
	if _, err := ghost.CreateCondition(f.ctx(f.owner), exchangeConditionParams{}, nil); err == nil {
		t.Fatal("create_condition on a ghost")
	}
	for _, amt := range []*big.Int{nil, big.NewInt(0), big.NewInt(-1)} {
		if err := f.x.Split(f.ctx(f.alice.addr), f.cond, amt, nil); err == nil {
			t.Fatalf("split of %v accepted", amt)
		}
		if err := f.x.Merge(f.ctx(f.alice.addr), f.cond, amt, nil); err == nil {
			t.Fatalf("merge of %v accepted", amt)
		}
	}
	if err := f.x.Split(f.ctx(f.alice.addr), 7, big.NewInt(1), nil); err == nil {
		t.Fatal("split on a missing condition")
	} else {
		mustSoft(t, err, "condition 7 does not exist")
	}
}
