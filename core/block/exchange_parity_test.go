package block

import (
	"encoding/hex"
	"math/big"
	"strings"
	"testing"
	"time"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
)

// exchangeForks is the devnet/testnet document: every settlement name at 1.
func exchangeForks() forks.Schedule {
	return forks.Schedule{forks.ModuleRevertV1: 1, forks.QRC20V2: 1, forks.QRC1155V1: 1, forks.ExchangeV1: 1}
}

// exSigner is an account with a real ML-DSA-65 key: it signs transactions
// (as the operator or a user) and orders (as a maker).
type exSigner struct {
	addr qcrypto.Address
	pub  *qcrypto.PublicKey
	priv *qcrypto.PrivateKey
}

func newExSigner(t *testing.T) *exSigner {
	t.Helper()
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	return &exSigner{addr: qcrypto.DeriveAddress(pub), pub: pub, priv: priv}
}

const (
	exChainFunding  = int64(1_000_000)
	exTraderFunding = "100000"
	exChainEndTime  = int64(2_000_000_000)
)

// exchangeChain is one producer running the exchange lifecycle through real
// blocks: the operator (also deployer, owner, resolver and fee recipient),
// two funded makers, and a broke third signer. Blocks 1 and 2 are the
// fixture: collateral + exchange deploy, then funding, operator and
// condition.
type exchangeChain struct {
	ex       *Executor
	sdb      *state.StateDB
	sink     *receiptSink
	op       *exSigner
	alice    *exSigner
	bob      *exSigner
	carol    *exSigner
	vaddr    qcrypto.Address
	vpub     []byte
	vpriv    *qcrypto.PrivateKey
	token    qcrypto.Address
	exchange qcrypto.Address
	blocks   []*types.Block
	salt     byte
}

func newExchangeChain(t *testing.T) *exchangeChain {
	t.Helper()
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	c := &exchangeChain{
		op: newExSigner(t), alice: newExSigner(t), bob: newExSigner(t), carol: newExSigner(t),
		vaddr: qcrypto.DeriveAddress(vpub), vpub: vpub.Bytes(), vpriv: vpriv,
		sink: &receiptSink{},
	}
	c.ex, c.sdb = newTestExecutor(t)
	t.Cleanup(func() { c.ex.Close(); c.sdb.Close() })
	c.ex.WithForks(exchangeForks()).WithTxIndex(c.sink)
	c.fundAll(t, c.sdb)

	// Block 1: the collateral token and the exchange, both by the operator.
	b1 := c.produce(t,
		c.call(t, c.op, 0, "qrc20", "deploy", map[string]any{
			"name": "Collateral", "symbol": "USDQ", "decimals": 0, "total_supply": "1000000",
		}),
		c.call(t, c.op, 1, "exchange", "deploy", map[string]any{"name": "Positions"}),
	)
	if len(b1.Transactions) != 2 || len(c.sink.receipts) != 2 {
		t.Fatalf("block 1: %d txs, %d receipts", len(b1.Transactions), len(c.sink.receipts))
	}
	if c.sink.receipts[0].ContractAddress == nil || c.sink.receipts[1].ContractAddress == nil {
		t.Fatalf("deploys carry no address: %+v", c.sink.receipts)
	}
	c.token = *c.sink.receipts[0].ContractAddress
	c.exchange = *c.sink.receipts[1].ContractAddress
	if want := vm.ExchangeAddress(c.op.addr, 1, 1); c.exchange != want {
		t.Fatalf("exchange at %s, want the height-bound %s", c.exchange, want)
	}

	// Block 2: fund the makers, grant the operator, open a condition.
	c.produce(t,
		c.call(t, c.op, 2, "qrc20", "transfer", map[string]any{"token": c.token.String(), "to": c.alice.addr.String(), "amount": exTraderFunding}),
		c.call(t, c.op, 3, "qrc20", "transfer", map[string]any{"token": c.token.String(), "to": c.bob.addr.String(), "amount": exTraderFunding}),
		c.call(t, c.op, 4, "exchange", "set_operator", c.params(map[string]any{"operator": c.op.addr.String(), "active": true})),
		c.call(t, c.op, 5, "exchange", "create_condition", c.params(map[string]any{
			"collateral": c.token.String(), "resolver": c.op.addr.String(), "end_time": exChainEndTime,
			"outcome_count": 2, "question_hash": strings.Repeat("ab", 32),
		})),
	)
	for i, rc := range c.sink.receipts[2:6] {
		if rc.Status != types.TxStatusSuccess {
			t.Fatalf("block 2 tx %d failed: %q", i, rc.Error)
		}
	}
	return c
}

func (c *exchangeChain) fundAll(t *testing.T, sdb *state.StateDB) {
	t.Helper()
	for _, s := range []*exSigner{c.op, c.alice, c.bob, c.carol} {
		fund(t, sdb, s.addr, exChainFunding)
	}
}

func (c *exchangeChain) params(extra map[string]any) map[string]any {
	p := map[string]any{"exchange": c.exchange.String()}
	for k, v := range extra {
		p[k] = v
	}
	return p
}

func (c *exchangeChain) call(t *testing.T, from *exSigner, nonce uint64, module, action string, params any) *types.Transaction {
	t.Helper()
	return signedModuleCall(t, from.addr, from.priv, envelope(t, module, action, params), nonce)
}

func (c *exchangeChain) produce(t *testing.T, txs ...*types.Transaction) *types.Block {
	t.Helper()
	var prev [32]byte
	if n := len(c.blocks); n > 0 {
		prev = c.blocks[n-1].Hash
	}
	b, err := c.ex.ProduceBlock(txs, c.vaddr, c.vpub, c.vpriv, prev, uint64(len(c.blocks)+1))
	if err != nil {
		t.Fatalf("produce block %d: %v", len(c.blocks)+1, err)
	}
	c.blocks = append(c.blocks, b)
	return b
}

// order is a signed wire order by tr at its current exchange nonce, with a
// fresh public key attached (no maker has a registered key: none of them has
// sent a transaction that would register one, except through the exchange
// calls below, and the module resolves registry-first either way).
func (c *exchangeChain) order(t *testing.T, tr *exSigner, side, outcome uint8, shares, price, expiration int64) map[string]any {
	t.Helper()
	nonce, err := vm.NewExchange(c.sdb, c.exchange).MakerNonce(tr.addr)
	if err != nil {
		t.Fatalf("maker nonce: %v", err)
	}
	c.salt++
	var salt [32]byte
	salt[31] = c.salt
	o := types.Order{
		Salt: salt, Maker: tr.addr, Exchange: c.exchange, Condition: 1, Outcome: outcome, Side: side,
		ShareAmount: big.NewInt(shares), Price: big.NewInt(price), Expiration: expiration, Nonce: nonce,
	}
	d, err := types.OrderDigest(types.ChainID, o)
	if err != nil {
		t.Fatalf("order digest: %v", err)
	}
	sig, err := qcrypto.Sign(tr.priv, d[:], qcrypto.CtxOrder)
	if err != nil {
		t.Fatalf("sign order: %v", err)
	}
	return map[string]any{
		"salt": hex.EncodeToString(salt[:]), "maker": tr.addr.String(), "exchange": c.exchange.String(),
		"condition": 1, "outcome": outcome, "side": side, "share_amount": o.ShareAmount.String(),
		"price": o.Price.String(), "expiration": expiration, "nonce": nonce,
		"signature": sig, "maker_pubkey": tr.pub.Bytes(),
	}
}

func (c *exchangeChain) match(t *testing.T, nonce uint64, taker map[string]any, makers []map[string]any, fills ...string) *types.Transaction {
	t.Helper()
	return c.call(t, c.op, nonce, "exchange", "match", c.params(map[string]any{
		"taker_order": taker, "maker_orders": makers, "fill_shares": fills,
	}))
}

func (c *exchangeChain) collateral(t *testing.T, sdb *state.StateDB, a qcrypto.Address) int64 {
	t.Helper()
	b, err := vm.NewQRC20Token(sdb, c.token).BalanceOf(a)
	if err != nil {
		t.Fatalf("balance: %v", err)
	}
	return b.Int64()
}

func (c *exchangeChain) position(t *testing.T, sdb *state.StateDB, a qcrypto.Address, outcome uint8) int64 {
	t.Helper()
	b, err := vm.NewQRC1155Token(sdb, c.exchange).BalanceOf(a, vm.ExchangePositionID(1, outcome))
	if err != nil {
		t.Fatalf("position: %v", err)
	}
	return b.Int64()
}

// replay applies every produced block on a fresh follower under sched and
// returns it; with the producer's schedule the roots must agree.
func (c *exchangeChain) replay(t *testing.T, sched forks.Schedule) (*Executor, *state.StateDB) {
	t.Helper()
	exB, sdbB := newTestExecutor(t)
	t.Cleanup(func() { exB.Close(); sdbB.Close() })
	exB.WithForks(sched)
	c.fundAll(t, sdbB)
	for _, b := range c.blocks {
		if err := exB.ExecuteBlock(b); err != nil {
			t.Fatalf("follower rejected block %d: %v", b.Header.Height, err)
		}
	}
	return exB, sdbB
}

func topicsOf(rc *types.Receipt) []string {
	names := map[[32]byte]string{
		vm.TopicTransfer: "Transfer", vm.TopicTransferSingle: "TransferSingle",
		vm.TopicMintMatched: "MintMatched", vm.TopicOrderFilled: "OrderFilled",
		vm.TopicPositionSplit: "PositionSplit", vm.TopicPositionsMerged: "PositionsMerged",
		vm.TopicResolved: "Resolved", vm.TopicRedeemed: "Redeemed", vm.TopicRedeemFeeCharged: "RedeemFeeCharged",
		vm.TopicRedeemFeeUpdated: "RedeemFeeUpdated",
	}
	out := make([]string, 0, len(rc.Logs))
	for _, l := range rc.Logs {
		n, ok := names[l.Topics[0]]
		if !ok {
			n = hex.EncodeToString(l.Topics[0][:4])
		}
		out = append(out, n)
	}
	return out
}

// The exchange lifecycle through real blocks: a MINT match whose taker order
// expires exactly at the header timestamp fills on the producer and on a
// follower re-executing from the header; a match whose taker cannot pay is a
// failed receipt under module_revert_v1 that leaves no residue and advances
// the operator's nonce so its next call mines; split, merge, resolve and
// redeem_for follow. Producer and follower reach one root, a crash replay
// through ReconcileState reaches it too, and a follower WITHOUT exchange_v1
// rejects the very first exchange block as unappliable rather than applying
// it to a different root.
func TestExchangeProducerFollowerParity(t *testing.T) {
	// The producer stamps time.Now() when it builds the block, so the taker's
	// expiration is signed as "now" just before producing. If the clock rolls
	// over a second in between the order is legitimately expired against the
	// header; start the chain over rather than assert on a header we did not
	// get.
	var (
		c            *exchangeChain
		taker, maker map[string]any
	)
	for attempt := 0; ; attempt++ {
		c = newExchangeChain(t)
		expiration := time.Now().Unix()
		taker = c.order(t, c.alice, types.OrderSideBuy, 0, 100, 600_000, expiration)
		maker = c.order(t, c.bob, types.OrderSideBuy, 1, 100, 500_000, 0)
		b3 := c.produce(t, c.match(t, 6, taker, []map[string]any{maker}, "100"))
		if b3.Header.Timestamp == expiration {
			break
		}
		if b3.Header.Timestamp < expiration {
			t.Fatalf("header %d is before the signing time %d", b3.Header.Timestamp, expiration)
		}
		if attempt == 10 {
			t.Fatal("could not produce a block in the same second as the order was signed")
		}
	}
	b3 := c.blocks[2]
	if len(b3.Transactions) != 1 {
		t.Fatalf("block 3 has %d txs, want the match", len(b3.Transactions))
	}
	rc3 := c.sink.receipts[6]
	if rc3.Status != types.TxStatusSuccess {
		t.Fatalf("match with expiration == header time failed on the producer: %q", rc3.Error)
	}
	if got := strings.Join(topicsOf(rc3), ","); got != "Transfer,Transfer,TransferSingle,TransferSingle,MintMatched,OrderFilled,OrderFilled" {
		t.Fatalf("match logs = %s", got)
	}
	if c.position(t, c.sdb, c.alice.addr, 0) != 100 || c.position(t, c.sdb, c.bob.addr, 1) != 100 || c.collateral(t, c.sdb, c.exchange) != 100 {
		t.Fatal("mint match did not settle")
	}

	// Block 4: alice splits; the operator submits a match whose taker (carol)
	// holds no collateral — included, failed, reverted — and its next call in
	// the same block mines at the next nonce.
	broke := c.order(t, c.carol, types.OrderSideBuy, 0, 10, 600_000, 0)
	counter := c.order(t, c.bob, types.OrderSideBuy, 1, 10, 500_000, 0)
	bobBefore := c.collateral(t, c.sdb, c.bob.addr)
	b4 := c.produce(t,
		c.call(t, c.alice, 0, "exchange", "split", c.params(map[string]any{"condition": 1, "amount": "50"})),
		c.match(t, 7, broke, []map[string]any{counter}, "10"),
		c.call(t, c.op, 8, "exchange", "set_redeem_fee_bps", c.params(map[string]any{"redeem_fee_bps": 250})),
	)
	if len(b4.Transactions) != 3 {
		t.Fatalf("block 4 has %d txs, want 3 (the failed match is included)", len(b4.Transactions))
	}
	split, failed, fee := c.sink.receipts[7], c.sink.receipts[8], c.sink.receipts[9]
	if split.Status != types.TxStatusSuccess || strings.Join(topicsOf(split), ",") != "Transfer,TransferSingle,TransferSingle,PositionSplit" {
		t.Fatalf("split receipt: status %d logs %v err %q", split.Status, topicsOf(split), split.Error)
	}
	if failed.Status != types.TxStatusFailed || !strings.HasPrefix(failed.Error, "exchange match: ") || !strings.Contains(failed.Error, "insufficient") {
		t.Fatalf("broke match receipt: status %d error %q", failed.Status, failed.Error)
	}
	if len(failed.Logs) != 0 {
		t.Fatalf("failed match kept %d logs", len(failed.Logs))
	}
	if fee.Status != types.TxStatusSuccess || strings.Join(topicsOf(fee), ",") != "RedeemFeeUpdated" {
		t.Fatalf("set_redeem_fee_bps after the failed match: status %d err %q", fee.Status, fee.Error)
	}
	if n, _ := c.sdb.GetNonce(c.op.addr); n != 9 {
		t.Fatalf("operator nonce = %d, want 9 (the failed match consumed its nonce)", n)
	}
	if c.collateral(t, c.sdb, c.bob.addr) != bobBefore || c.position(t, c.sdb, c.carol.addr, 0) != 0 || c.position(t, c.sdb, c.bob.addr, 1) != 100 {
		t.Fatal("failed match left residue")
	}
	if filled, _ := vm.NewExchange(c.sdb, c.exchange).OrderFilled(orderHashOf(t, c, counter)); filled.Sign() != 0 {
		t.Fatalf("failed match advanced the counter order's fill to %s", filled)
	}

	// Block 5: alice merges part of her set; the resolver settles YES.
	c.produce(t,
		c.call(t, c.alice, 1, "exchange", "merge", c.params(map[string]any{"condition": 1, "amount": "20"})),
		c.call(t, c.op, 9, "exchange", "resolve", c.params(map[string]any{"condition": 1, "outcome": 0})),
	)
	// Block 6: anyone triggers alice's redemption; the holder is paid.
	c.produce(t, c.call(t, c.op, 10, "exchange", "redeem_for", c.params(map[string]any{"condition": 1, "holder": c.alice.addr.String()})))
	for i, rc := range c.sink.receipts[10:13] {
		if rc.Status != types.TxStatusSuccess {
			t.Fatalf("block 5/6 tx %d failed: %q", i, rc.Error)
		}
	}
	redeemed := c.sink.receipts[12]
	if got := strings.Join(topicsOf(redeemed), ","); got != "TransferSingle,Transfer,Transfer,RedeemFeeCharged,Redeemed" {
		t.Fatalf("redeem_for logs = %s", got)
	}
	// alice: 100 (match) + 50 (split) − 20 (merge) = 130 YES → fee 3, payout 127.
	// collateral: 100000 − 50 − 50 + 20 + 127.
	const aliceWant = 100_000 - 50 - 50 + 20 + 127
	if got := c.collateral(t, c.sdb, c.alice.addr); got != aliceWant {
		t.Fatalf("alice collateral = %d, want %d", got, aliceWant)
	}
	if got := c.collateral(t, c.sdb, c.op.addr); got != 1_000_000-200_000+3 {
		t.Fatalf("operator (fee recipient) collateral = %d", got)
	}
	if c.position(t, c.sdb, c.alice.addr, 0) != 0 || c.position(t, c.sdb, c.alice.addr, 1) != 30 {
		t.Fatal("alice positions after redeem are wrong")
	}

	// A follower on the same schedule reaches the same root from the headers.
	_, sdbB := c.replay(t, exchangeForks())
	aRoot, _ := c.sdb.StateRoot()
	bRoot, _ := sdbB.StateRoot()
	if aRoot != bRoot {
		t.Fatalf("state root mismatch: producer=%x follower=%x", aRoot[:8], bRoot[:8])
	}
	if got := c.collateral(t, sdbB, c.alice.addr); got != aliceWant {
		t.Fatalf("follower alice collateral = %d", got)
	}
	if n, _ := sdbB.GetNonce(c.op.addr); n != 11 {
		t.Fatalf("follower operator nonce = %d, want 11", n)
	}

	// A follower WITHOUT exchange_v1 cannot apply block 1: the deploy is HARD
	// fork-inactive under its rules, so the block is rejected before any root
	// comparison and nothing is left behind.
	exC, sdbC := newTestExecutor(t)
	defer exC.Close()
	defer sdbC.Close()
	exC.WithForks(forks.Schedule{forks.ModuleRevertV1: 1, forks.QRC20V2: 1, forks.QRC1155V1: 1})
	c.fundAll(t, sdbC)
	err := exC.ExecuteBlock(c.blocks[0])
	if err == nil || !strings.Contains(err.Error(), "unappliable tx") {
		t.Fatalf("follower without exchange_v1: want unappliable-tx rejection, got %v", err)
	}
	if strings.Contains(err.Error(), "state root mismatch") {
		t.Fatalf("must be rejected before the root comparison: %v", err)
	}
	if h, _ := exC.LatestHeight(); h != 0 {
		t.Fatalf("head advanced to %d on a rejected block", h)
	}
	if n, _ := sdbC.GetNonce(c.op.addr); n != 0 {
		t.Fatalf("rejected block left residue: nonce %d", n)
	}

	// Crash replay: a follower with block 6 stored but not applied reaches the
	// producer's root through ReconcileState, which runs the same gated path.
	exD, sdbD := newTestExecutor(t)
	defer exD.Close()
	defer sdbD.Close()
	exD.WithForks(exchangeForks())
	c.fundAll(t, sdbD)
	for _, b := range c.blocks[:5] {
		if err := exD.ExecuteBlock(b); err != nil {
			t.Fatalf("D apply block %d: %v", b.Header.Height, err)
		}
	}
	if err := exD.storeBlock(c.blocks[5]); err != nil {
		t.Fatalf("storeBlock: %v", err)
	}
	if err := exD.ReconcileState(); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if sh, _ := sdbD.AppliedHeight(); sh != 6 {
		t.Fatalf("state height after reconcile = %d, want 6", sh)
	}
	dRoot, _ := sdbD.StateRoot()
	if dRoot != aRoot {
		t.Fatalf("state root mismatch after replay: producer=%x replayed=%x", aRoot[:8], dRoot[:8])
	}
}

// orderHashOf recomputes the instance-bound hash of a wire order built by
// exchangeChain.order.
func orderHashOf(t *testing.T, c *exchangeChain, w map[string]any) [32]byte {
	t.Helper()
	salt, _ := hex.DecodeString(w["salt"].(string))
	shares, _ := new(big.Int).SetString(w["share_amount"].(string), 10)
	price, _ := new(big.Int).SetString(w["price"].(string), 10)
	maker, _ := qcrypto.AddressFromString(w["maker"].(string))
	o := types.Order{
		Maker: maker, Exchange: c.exchange, Condition: 1, Outcome: w["outcome"].(uint8), Side: w["side"].(uint8),
		ShareAmount: shares, Price: price, Expiration: w["expiration"].(int64), Nonce: w["nonce"].(uint64),
	}
	copy(o.Salt[:], salt)
	h, err := types.OrderDigest(types.ChainID, o)
	if err != nil {
		t.Fatalf("order digest: %v", err)
	}
	return h
}
