package vm

import (
	"math/big"
	"math/rand"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// The D6 solvency identity, asserted after every operation of a seeded
// random session:
//
//	pre-resolution   supply(YES_c) == supply(NO_c) == locked_c
//	always           balance(exchange, T) − Σ locked_c == donations
//	post-resolution  locked_c == supply(win)
//	every redeem     paid + fee == burned
//
// and, after every holder redeems, the exchange holds exactly the donations
// (zero when there were none). The equality on supplies holds only because
// the router refuses burn/transfer on the module-owned collection (D5), so
// the session includes holders trying exactly that.

type invSession struct {
	t        *testing.T
	f        *exFixture
	rng      *rand.Rand
	traders  []*exTrader
	open     []exchangeOrderWire // orders that may still have shares left
	donated  int64
	donate   bool
	ops      map[string]int
	resolved bool
}

func (s *invSession) trader() *exTrader { return s.traders[s.rng.Intn(len(s.traders))] }

func (s *invSession) amount(max int64) int64 { return 1 + s.rng.Int63n(max) }

// supplies reads (supply YES, supply NO, locked, exchange balance).
func (s *invSession) supplies() (yes, no, locked, balance int64) {
	f := s.f
	y, _ := f.pos.TotalSupply(ExchangePositionID(f.cond, 0))
	n, _ := f.pos.TotalSupply(ExchangePositionID(f.cond, 1))
	return y.Int64(), n.Int64(), f.locked(s.t), f.collateral(s.t, f.x.addr)
}

func (s *invSession) assertSolvent(op string) {
	s.t.Helper()
	yes, no, locked, balance := s.supplies()
	if !s.resolved && (yes != no || yes != locked) {
		s.t.Fatalf("after %s: supply YES %d, NO %d, locked %d", op, yes, no, locked)
	}
	if s.resolved {
		c, _ := s.f.x.Condition(s.f.cond)
		win, _ := s.f.pos.TotalSupply(ExchangePositionID(s.f.cond, c.WinningOutcome))
		if win.Int64() != locked {
			s.t.Fatalf("after %s: winning supply %d, locked %d", op, win.Int64(), locked)
		}
	}
	if balance < locked {
		s.t.Fatalf("after %s: exchange holds %d, locked %d", op, balance, locked)
	}
	if balance-locked != s.donated {
		s.t.Fatalf("after %s: excess %d, donations %d", op, balance-locked, s.donated)
	}
}

func (s *invSession) soft(op string, err error) {
	s.t.Helper()
	if err != nil {
		if IsHardModuleError(err) {
			s.t.Fatalf("%s: HARD error from the module: %v", op, err)
		}
		s.ops[op+" failed"]++
		return
	}
	s.ops[op]++
}

// randomOrder signs an order for tr with random terms, biased so that
// crossing pairs are common.
func (s *invSession) randomOrder(tr *exTrader, side, outcome uint8, price int64) exchangeOrderWire {
	o := s.f.build(s.t, tr, side, outcome, s.amount(500), price)
	if s.rng.Intn(5) == 0 {
		o.Expiration = s.f.time + s.rng.Int63n(3) - 1 // sometimes already expired
	}
	if s.rng.Intn(6) == 0 {
		o.Recipient = s.trader().addr
	}
	return s.f.signed(s.t, tr, o)
}

func (s *invSession) step() {
	f, t := s.f, s.t
	switch s.rng.Intn(12) {
	case 0, 1: // split
		err := f.x.Split(f.ctx(s.trader().addr), f.cond, big.NewInt(s.amount(2000)), nil)
		s.soft("split", err)
	case 2: // merge
		err := f.x.Merge(f.ctx(s.trader().addr), f.cond, big.NewInt(s.amount(800)), nil)
		s.soft("merge", err)
	case 3, 4, 5, 6: // match: pick a kind, build fresh or reuse open orders
		taker, maker := s.pairFor(s.rng.Intn(4))
		fill := s.amount(400)
		rc, err := f.match(t, f.operator, taker, []exchangeOrderWire{maker}, fill)
		if err == nil {
			s.open = append(s.open, taker, maker)
			if len(rc.Logs) < 4 {
				t.Fatalf("a filled match emitted %d logs", len(rc.Logs))
			}
		}
		s.soft("match", err)
	case 7: // multi-maker match reusing open orders (filled>0 short-cut)
		if len(s.open) < 2 {
			return
		}
		taker := s.open[s.rng.Intn(len(s.open))]
		taker.Signature, taker.MakerPubKey = nil, nil
		n := 1 + s.rng.Intn(3)
		makers := make([]exchangeOrderWire, 0, n)
		fills := make([]int64, 0, n)
		for i := 0; i < n; i++ {
			m := s.open[s.rng.Intn(len(s.open))]
			m.Signature = nil
			makers = append(makers, m)
			fills = append(fills, s.amount(50))
		}
		_, err := f.match(t, f.operator, taker, makers, fills...)
		s.soft("rematch", err)
	case 8: // cancel a random open order, sometimes by signature
		if len(s.open) == 0 {
			return
		}
		o := s.open[s.rng.Intn(len(s.open))]
		p := exchangeCancelParams{Exchange: f.x.addr.String(), Order: o}
		caller := f.operator
		if s.rng.Intn(2) == 0 {
			maker := s.traderFor(o.Maker)
			d := types.CancelDigest(f.hash(t, o))
			p.CancelSignature, _ = qcrypto.Sign(maker.priv, d[:], qcrypto.CtxOrder)
			caller = addr(0x42)
		}
		s.soft("cancel", f.x.CancelOrder(f.ctx(caller), p, nil))
	case 9: // nonce bump (rare)
		if s.rng.Intn(4) != 0 {
			return
		}
		tr := s.trader()
		n, _ := f.x.MakerNonce(tr.addr)
		s.soft("increment_nonce", f.x.IncrementNonce(f.ctx(tr.addr), exchangeNonceParams{Exchange: f.x.addr.String(), Maker: tr.addr.String(), ExpectedNonce: n}, nil))
	case 10: // a holder tries to burn or move one leg through the router
		tr := s.trader()
		id := ExchangePositionID(f.cond, uint8(s.rng.Intn(2))).String()
		for _, action := range []string{"burn", "transfer"} {
			err := DispatchModuleCall(f.sdb, f.ctx(tr.addr), moduleData(t, "qrc1155", action, qrc1155Action{
				Collection: f.x.addr.String(), To: s.trader().addr.String(), ID: id, Amount: "1",
			}), &types.Receipt{})
			if err == nil {
				t.Fatalf("router %s on the position collection succeeded", action)
			}
		}
		s.ops["router refused"]++
	case 11: // donation
		if !s.donate {
			return
		}
		amt := s.amount(100)
		if err := f.tok.Transfer(s.trader().addr, f.x.addr, big.NewInt(amt)); err == nil {
			s.donated += amt
			s.ops["donate"]++
		}
	}
}

func (s *invSession) traderFor(a string) *exTrader {
	for _, tr := range s.traders {
		if tr.addr.String() == a {
			return tr
		}
	}
	s.t.Fatalf("unknown maker %s", a)
	return nil
}

// pairFor builds a (taker, maker) pair of the given kind with prices that
// usually cross (and sometimes do not).
func (s *invSession) pairFor(kind int) (taker, maker exchangeOrderWire) {
	a, b := s.trader(), s.trader()
	p := 100_000 + s.rng.Int63n(800_000)
	spread := s.rng.Int63n(200_000) - 40_000 // negative ⇒ not crossing
	clamp := func(v int64) int64 {
		if v < 1 {
			return 1
		}
		if v >= ExchangePriceScale {
			return ExchangePriceScale - 1
		}
		return v
	}
	switch kind {
	case 0: // taker BUY o, maker SELL o
		o := uint8(s.rng.Intn(2))
		return s.randomOrder(a, types.OrderSideBuy, o, clamp(p+spread)), s.randomOrder(b, types.OrderSideSell, o, clamp(p))
	case 1: // taker SELL o, maker BUY o
		o := uint8(s.rng.Intn(2))
		return s.randomOrder(a, types.OrderSideSell, o, clamp(p-spread)), s.randomOrder(b, types.OrderSideBuy, o, clamp(p))
	case 2: // mint
		return s.randomOrder(a, types.OrderSideBuy, 0, clamp(ExchangePriceScale-p+spread)), s.randomOrder(b, types.OrderSideBuy, 1, clamp(p))
	default: // merge
		return s.randomOrder(a, types.OrderSideSell, 0, clamp(ExchangePriceScale-p-spread)), s.randomOrder(b, types.OrderSideSell, 1, clamp(p))
	}
}

func runInvariantSession(t *testing.T, seed int64, donate bool, feeBps uint64) {
	f := newExFixture(t)
	if err := f.x.SetRedeemFeeBps(f.owner, feeBps, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.x.SetFeeRecipient(f.owner, addr(0xfe), nil); err != nil {
		t.Fatal(err)
	}
	dave := newExTrader(t)
	if err := f.tok.Transfer(f.owner, dave.addr, big.NewInt(exFunding)); err != nil {
		t.Fatal(err)
	}
	s := &invSession{
		t: t, f: f, rng: rand.New(rand.NewSource(seed)), donate: donate,
		traders: []*exTrader{f.alice, f.bob, f.carol, dave}, ops: map[string]int{},
	}
	s.assertSolvent("setup")
	const ops = 500
	for i := 0; i < ops; i++ {
		s.step()
		s.assertSolvent("op")
	}
	if s.ops["match"] < 20 || s.ops["split"] < 10 || s.ops["merge"] < 1 || s.ops["router refused"] < 1 {
		t.Fatalf("session did not exercise enough paths: %v", s.ops)
	}

	// Resolve, then every gate closes except redeem, and the drain is exact.
	f.resolve(t, uint8(s.rng.Intn(2)))
	s.resolved = true
	s.assertSolvent("resolve")
	if err := f.x.Split(f.ctx(f.alice.addr), f.cond, big.NewInt(1), nil); err == nil {
		t.Fatal("split after resolution")
	}
	if err := f.x.Merge(f.ctx(f.alice.addr), f.cond, big.NewInt(1), nil); err == nil {
		t.Fatal("merge after resolution")
	}
	taker, maker := s.pairFor(2)
	if _, err := f.match(t, f.operator, taker, []exchangeOrderWire{maker}, 5); err == nil {
		t.Fatal("match after resolution")
	}
	s.assertSolvent("post-resolution refusals")

	c, _ := f.x.Condition(f.cond)
	feeTo := addr(0xfe)
	feeBefore := f.collateral(t, feeTo)
	totalBurned, totalPaid := int64(0), int64(0)
	for _, tr := range s.traders {
		held := f.position(t, tr.addr, c.WinningOutcome)
		before := f.collateral(t, tr.addr)
		feeBeforeThis := f.collateral(t, feeTo)
		err := f.x.Redeem(f.ctx(addr(0x42)), f.cond, tr.addr, nil)
		if held == 0 {
			if err == nil {
				t.Fatal("a holder with no winning shares redeemed")
			}
			continue
		}
		if err != nil {
			t.Fatalf("redeem_for %s: %v", tr.addr, err)
		}
		paid := f.collateral(t, tr.addr) - before
		fee := f.collateral(t, feeTo) - feeBeforeThis
		if paid+fee != held {
			t.Fatalf("redeem: paid %d + fee %d != burned %d", paid, fee, held)
		}
		if want := held * int64(feeBps) / 10_000; fee != want {
			t.Fatalf("fee %d, want floor(%d·%d/10000) = %d", fee, held, feeBps, want)
		}
		totalBurned += held
		totalPaid += paid
		s.assertSolvent("redeem")
	}
	if totalPaid+(f.collateral(t, feeTo)-feeBefore) != totalBurned {
		t.Fatal("drain: paid + fees != burned")
	}
	if got := f.collateral(t, f.x.addr); got != s.donated {
		t.Fatalf("exchange holds %d after the drain, donations %d", got, s.donated)
	}
	if f.locked(t) != 0 {
		t.Fatalf("locked %d after the drain", f.locked(t))
	}
	if win, _ := f.pos.TotalSupply(ExchangePositionID(f.cond, c.WinningOutcome)); win.Sign() != 0 {
		t.Fatal("winning supply not fully burned")
	}
	t.Logf("seed %d donate %v: %v", seed, donate, s.ops)
}

func TestExchangeSolvencyInvariant(t *testing.T) {
	t.Run("no donations, no fee", func(t *testing.T) { runInvariantSession(t, 1, false, 0) })
	t.Run("donations, 250 bps", func(t *testing.T) { runInvariantSession(t, 2, true, 250) })
	t.Run("no donations, 2000 bps", func(t *testing.T) { runInvariantSession(t, 3, false, MaxExchangeRedeemFeeBps) })
}
