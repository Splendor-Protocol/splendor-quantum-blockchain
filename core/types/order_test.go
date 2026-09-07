package types

import (
	"encoding/hex"
	"math/big"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
)

// The fixed vectors every other implementation of the exchange digests (the
// WASM signer, the TypeScript gateway's hashOrder) is pinned to. Changing one
// is a wire break. Each was cross-checked against an independent SHA3-256
// (Python hashlib) over the documented preimage when it was pinned.
const (
	orderGoldenChainID    = "splendor-quantum-1"
	orderGoldenCondition  = uint64(42)
	orderGoldenOutcome    = uint8(1)
	orderGoldenSide       = OrderSideBuy
	orderGoldenShares     = "1000000000000000000" // 1e18
	orderGoldenPrice      = "600000"              // 0.60
	orderGoldenExpiration = int64(1_800_000_000)
	orderGoldenNonce      = uint64(3)

	orderGoldenDigest          = "4ff70bf19d522519733bdd58ea58328cb48cf34d88cc2439f67935e9fe0699d1"
	cancelGoldenDigest         = "2b4ca8255e5bed928d8c1b8f2a26716c75f97d31e84e03ab9f2dc705b9bf6d6b"
	incrementNonceGoldenDigest = "bee44b73c6ccbf4eac5012ec7d646aeca5acddf8141c937160c0759310c1e5b3"
)

func orderGoldenSalt() [32]byte {
	var s [32]byte
	for i := range s {
		s[i] = byte(i + 1)
	}
	return s
}

// orderGoldenOrder returns a fresh copy of the golden order so a variant can
// mutate one field without touching the others.
func orderGoldenOrder(t *testing.T) Order {
	t.Helper()
	shares, ok := new(big.Int).SetString(orderGoldenShares, 10)
	if !ok {
		t.Fatal("bad golden share amount")
	}
	price, ok := new(big.Int).SetString(orderGoldenPrice, 10)
	if !ok {
		t.Fatal("bad golden price")
	}
	return Order{
		Salt:        orderGoldenSalt(),
		Maker:       permitTestAddr(0xaa),
		Exchange:    permitTestAddr(0xee),
		Condition:   orderGoldenCondition,
		Outcome:     orderGoldenOutcome,
		Side:        orderGoldenSide,
		ShareAmount: shares,
		Price:       price,
		Expiration:  orderGoldenExpiration,
		Nonce:       orderGoldenNonce,
		Recipient:   permitTestAddr(0xbb),
	}
}

func orderGolden(t *testing.T, chainID string) [32]byte {
	t.Helper()
	d, err := OrderDigest(chainID, orderGoldenOrder(t))
	if err != nil {
		t.Fatalf("OrderDigest(%q): %v", chainID, err)
	}
	return d
}

func TestOrderDigestGolden(t *testing.T) {
	d := orderGolden(t, orderGoldenChainID)
	if got := hex.EncodeToString(d[:]); got != orderGoldenDigest {
		t.Fatalf("order digest golden moved:\n got %s\nwant %s", got, orderGoldenDigest)
	}
}

// The same exchange instance and collateral can exist at the same address on
// every tier, so the chain id has to be part of what is signed.
func TestOrderDigestBindsChainID(t *testing.T) {
	if orderGolden(t, "splendor-quantum-1") == orderGolden(t, "splendor-quantum-devnet-1") {
		t.Fatal("digest must differ across chain ids")
	}
}

func TestOrderDigestEveryFieldMoves(t *testing.T) {
	base := orderGolden(t, orderGoldenChainID)
	variants := map[string]func(o *Order){
		"salt":         func(o *Order) { o.Salt[31] ^= 0x01 },
		"maker":        func(o *Order) { o.Maker = permitTestAddr(0xab) },
		"exchange":     func(o *Order) { o.Exchange = permitTestAddr(0xef) },
		"condition":    func(o *Order) { o.Condition++ },
		"outcome":      func(o *Order) { o.Outcome = 0 },
		"side":         func(o *Order) { o.Side = OrderSideSell },
		"share_amount": func(o *Order) { o.ShareAmount = new(big.Int).Add(o.ShareAmount, big.NewInt(1)) },
		"price":        func(o *Order) { o.Price = new(big.Int).Add(o.Price, big.NewInt(1)) },
		"expiration":   func(o *Order) { o.Expiration++ },
		"nonce":        func(o *Order) { o.Nonce++ },
		"recipient":    func(o *Order) { o.Recipient = permitTestAddr(0xbc) },
		// A zero recipient means "pay the maker" but is still thirty-two
		// signed zero bytes, so it is a different order from an explicit one.
		"recipient zero": func(o *Order) { o.Recipient = qcrypto.Address{} },
	}
	for name, mutate := range variants {
		o := orderGoldenOrder(t)
		mutate(&o)
		d, err := OrderDigest(orderGoldenChainID, o)
		if err != nil {
			t.Fatalf("%s variant: %v", name, err)
		}
		if d == base {
			t.Errorf("changing %s did not move the digest", name)
		}
	}
}

// Fixed-width layout: outcome and side are adjacent single bytes, so swapping
// their values must not collide (a length-prefixed or ambiguous encoding
// could let one field borrow from its neighbour).
func TestOrderDigestAdjacentBytesDoNotCollide(t *testing.T) {
	a := orderGoldenOrder(t)
	a.Outcome, a.Side = 1, 0
	b := orderGoldenOrder(t)
	b.Outcome, b.Side = 0, 1
	da, err := OrderDigest(orderGoldenChainID, a)
	if err != nil {
		t.Fatal(err)
	}
	db, err := OrderDigest(orderGoldenChainID, b)
	if err != nil {
		t.Fatal(err)
	}
	if da == db {
		t.Fatal("swapping outcome and side did not move the digest")
	}
}

// Caller-supplied values reach the digest from the vm, so every malformed
// input must come back as an error — a panic would take the node down.
func TestOrderDigestErrorsNotPanics(t *testing.T) {
	twoPow256 := new(big.Int).Lsh(big.NewInt(1), 256)
	maxU256 := new(big.Int).Sub(twoPow256, big.NewInt(1))

	ok := orderGoldenOrder(t)
	ok.ShareAmount, ok.Price = maxU256, maxU256
	if _, err := OrderDigest(orderGoldenChainID, ok); err != nil {
		t.Fatalf("2^256-1 must be accepted: %v", err)
	}
	ok = orderGoldenOrder(t)
	ok.Expiration = 0
	if _, err := OrderDigest(orderGoldenChainID, ok); err != nil {
		t.Fatalf("expiration 0 (never) must be accepted: %v", err)
	}

	cases := map[string]struct {
		chainID string
		mutate  func(o *Order)
	}{
		"share 2^256":     {orderGoldenChainID, func(o *Order) { o.ShareAmount = twoPow256 }},
		"share nil":       {orderGoldenChainID, func(o *Order) { o.ShareAmount = nil }},
		"share negative":  {orderGoldenChainID, func(o *Order) { o.ShareAmount = big.NewInt(-1) }},
		"price 2^256":     {orderGoldenChainID, func(o *Order) { o.Price = twoPow256 }},
		"price nil":       {orderGoldenChainID, func(o *Order) { o.Price = nil }},
		"price negative":  {orderGoldenChainID, func(o *Order) { o.Price = big.NewInt(-1) }},
		"expiration<0":    {orderGoldenChainID, func(o *Order) { o.Expiration = -1 }},
		"empty chain":     {"", func(o *Order) {}},
		"bad chain":       {"splendor quantum", func(o *Order) {}},
		"zero-value form": {orderGoldenChainID, func(o *Order) { *o = Order{} }},
	}
	for name, c := range cases {
		o := orderGoldenOrder(t)
		c.mutate(&o)
		if _, err := OrderDigest(c.chainID, o); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestCancelDigestGolden(t *testing.T) {
	d := CancelDigest(orderGolden(t, orderGoldenChainID))
	if got := hex.EncodeToString(d[:]); got != cancelGoldenDigest {
		t.Fatalf("cancel digest golden moved:\n got %s\nwant %s", got, cancelGoldenDigest)
	}
}

// The cancel preimage is prefixed: a signature over a cancel must never be
// usable as a signature over the bare order hash (or vice versa), and a
// different order hash must yield a different cancel digest.
func TestCancelDigestIsDomainSeparatedFromOrderHash(t *testing.T) {
	h := orderGolden(t, orderGoldenChainID)
	if CancelDigest(h) == h {
		t.Fatal("cancel digest equals the order hash")
	}
	if CancelDigest(h) == qcrypto.Hash32(h[:]) {
		t.Fatal("cancel digest is an unprefixed hash of the order hash")
	}
	other := h
	other[0] ^= 0x01
	if CancelDigest(h) == CancelDigest(other) {
		t.Fatal("cancel digest does not depend on the order hash")
	}
}

func incrementNonceGolden(t *testing.T, chainID string) [32]byte {
	t.Helper()
	d, err := IncrementNonceDigest(chainID, permitTestAddr(0xee), permitTestAddr(0xaa), orderGoldenNonce)
	if err != nil {
		t.Fatalf("IncrementNonceDigest(%q): %v", chainID, err)
	}
	return d
}

func TestIncrementNonceDigestGolden(t *testing.T) {
	d := incrementNonceGolden(t, orderGoldenChainID)
	if got := hex.EncodeToString(d[:]); got != incrementNonceGoldenDigest {
		t.Fatalf("increment-nonce digest golden moved:\n got %s\nwant %s", got, incrementNonceGoldenDigest)
	}
}

func TestIncrementNonceDigestBindsChainID(t *testing.T) {
	if incrementNonceGolden(t, "splendor-quantum-1") == incrementNonceGolden(t, "splendor-quantum-devnet-1") {
		t.Fatal("digest must differ across chain ids")
	}
}

// expected_nonce is what makes each signature single-use, so it — and every
// other field — must move the digest.
func TestIncrementNonceDigestEveryFieldMoves(t *testing.T) {
	base := incrementNonceGolden(t, orderGoldenChainID)
	ex, mk := permitTestAddr(0xee), permitTestAddr(0xaa)
	variants := map[string]func() ([32]byte, error){
		"exchange": func() ([32]byte, error) {
			return IncrementNonceDigest(orderGoldenChainID, permitTestAddr(0xef), mk, orderGoldenNonce)
		},
		"maker": func() ([32]byte, error) {
			return IncrementNonceDigest(orderGoldenChainID, ex, permitTestAddr(0xab), orderGoldenNonce)
		},
		"expected_nonce": func() ([32]byte, error) {
			return IncrementNonceDigest(orderGoldenChainID, ex, mk, orderGoldenNonce+1)
		},
	}
	for name, f := range variants {
		d, err := f()
		if err != nil {
			t.Fatalf("%s variant: %v", name, err)
		}
		if d == base {
			t.Errorf("changing %s did not move the digest", name)
		}
	}
}

func TestIncrementNonceDigestRejectsInvalidChainID(t *testing.T) {
	ex, mk := permitTestAddr(0xee), permitTestAddr(0xaa)
	for name, chainID := range map[string]string{"empty": "", "bad": "splendor quantum"} {
		if _, err := IncrementNonceDigest(chainID, ex, mk, 0); err == nil {
			t.Errorf("%s chain id: expected an error", name)
		}
	}
}

// Order, cancel and nonce-bump signatures all live under CtxOrder, so their
// preimage prefixes are the only thing keeping them apart. Pin that they are
// distinct over overlapping inputs.
func TestExchangeDigestPrefixesAreDistinct(t *testing.T) {
	order := orderGolden(t, orderGoldenChainID)
	cancel := CancelDigest(order)
	nonce := incrementNonceGolden(t, orderGoldenChainID)
	seen := map[[32]byte]string{order: "order"}
	for name, d := range map[string][32]byte{"cancel": cancel, "increment_nonce": nonce} {
		if prev, dup := seen[d]; dup {
			t.Fatalf("%s digest collides with %s", name, prev)
		}
		seen[d] = name
	}
}
