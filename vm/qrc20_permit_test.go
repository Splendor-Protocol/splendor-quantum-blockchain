package vm

import (
	"math/big"
	"strings"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// permitFixture is one QRC-20 whose deployer (and so whole-supply holder and
// contract owner) is a real ML-DSA-65 key, plus a keyless spender. The holder
// starts with NO registered public key, so the fresh-owner path (owner_pubkey
// supplied and checked against the address) is the default; registerHolderKey
// switches to the registry path.
type permitFixture struct {
	sdb     *state.StateDB
	tok     *QRC20Token
	token   qcrypto.Address
	holder  qcrypto.Address
	pub     *qcrypto.PublicKey
	priv    *qcrypto.PrivateKey
	spender qcrypto.Address
}

const permitTestDeadline = int64(1_800_000_000)

func newPermitFixture(t *testing.T) *permitFixture {
	t.Helper()
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	holder := qcrypto.DeriveAddress(pub)
	sdb := newTestState(t)
	token, err := DeployQRC20(sdb, holder, "Permit Test", "PRM", 18, mustBig("1000000"), 1)
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	return &permitFixture{
		sdb: sdb, tok: NewQRC20Token(sdb, token), token: token,
		holder: holder, pub: pub, priv: priv, spender: addr(0x33),
	}
}

// registerHolderKey puts the holder's key in the account registry, the way a
// first signed transaction would.
func (f *permitFixture) registerHolderKey(t *testing.T) {
	t.Helper()
	acc, err := f.sdb.GetAccount(f.holder)
	if err != nil {
		t.Fatalf("get account: %v", err)
	}
	acc.PublicKey = f.pub.Bytes()
	f.sdb.SetAccount(acc)
}

// sign produces the holder's permit signature over the v2 digest for the
// node's chain id, with an explicit nonce so replay tests can pick a stale one.
func (f *permitFixture) sign(t *testing.T, priv *qcrypto.PrivateKey, contract, owner, spender qcrypto.Address, amount *big.Int, nonce uint64, deadline int64) []byte {
	t.Helper()
	msg, err := PermitMessage(contract, owner, spender, amount, nonce, deadline)
	if err != nil {
		t.Fatalf("permit message: %v", err)
	}
	sig, err := qcrypto.Sign(priv, msg[:], qcrypto.CtxPermit)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return sig
}

func (f *permitFixture) signDefault(t *testing.T, amount *big.Int, deadline int64) []byte {
	t.Helper()
	return f.sign(t, f.priv, f.token, f.holder, f.spender, amount, 0, deadline)
}

func (f *permitFixture) allowance(t *testing.T) *big.Int {
	t.Helper()
	a, err := f.tok.Allowance(f.holder, f.spender)
	if err != nil {
		t.Fatalf("allowance: %v", err)
	}
	return a
}

func (f *permitFixture) permitNonce(t *testing.T) uint64 {
	t.Helper()
	n, err := f.tok.PermitNonce(f.holder)
	if err != nil {
		t.Fatalf("permit nonce: %v", err)
	}
	return n.Uint64()
}

func mustContain(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error containing %q, got nil", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not contain %q", err.Error(), want)
	}
}

func TestPermitFreshOwnerSetsAllowanceAndEmitsApproval(t *testing.T) {
	f := newPermitFixture(t)
	amount := mustBig("12345")
	sig := f.signDefault(t, amount, permitTestDeadline)

	receipt := &types.Receipt{}
	if err := f.tok.Permit(f.holder, f.spender, amount, permitTestDeadline, sig, f.pub.Bytes(), permitTestDeadline-100, receipt); err != nil {
		t.Fatalf("permit: %v", err)
	}
	if got := f.allowance(t); got.Cmp(amount) != 0 {
		t.Fatalf("allowance = %s, want %s", got, amount)
	}
	if got := f.permitNonce(t); got != 1 {
		t.Fatalf("permit nonce = %d, want 1", got)
	}
	if len(receipt.Logs) != 1 || receipt.Logs[0].Topics[0] != TopicApproval {
		t.Fatalf("expected exactly one Approval log, got %+v", receipt.Logs)
	}
	// Permit must never register the presented key: that stays with the
	// transaction path, which verifies a signature over a tx first.
	if _, err := f.sdb.GetPublicKey(f.holder); err == nil {
		t.Fatal("permit registered the owner's public key; it must not")
	}
}

// The bug this PR fixes: the old Permit verified against whatever key the
// caller handed in. A key that does not derive `owner` proves nothing about
// owner's consent, no matter how valid its signature is.
func TestPermitForgedKeyRejected(t *testing.T) {
	f := newPermitFixture(t)
	attackerPub, attackerPriv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	amount := mustBig("999")
	// Valid signature over the right digest — just by the wrong key.
	sig := f.sign(t, attackerPriv, f.token, f.holder, f.spender, amount, 0, permitTestDeadline)

	err = f.tok.Permit(f.holder, f.spender, amount, permitTestDeadline, sig, attackerPub.Bytes(), 1, nil)
	mustContain(t, err, "does not derive")
	if f.allowance(t).Sign() != 0 || f.permitNonce(t) != 0 {
		t.Fatal("a rejected permit must leave allowance and nonce untouched")
	}
}

func TestPermitRegistryPathIgnoresSuppliedKey(t *testing.T) {
	f := newPermitFixture(t)
	f.registerHolderKey(t)
	amount := mustBig("500")

	// Registered owner: no key on the wire is needed.
	sig := f.signDefault(t, amount, permitTestDeadline)
	if err := f.tok.Permit(f.holder, f.spender, amount, permitTestDeadline, sig, nil, 1, nil); err != nil {
		t.Fatalf("permit via registry with nil owner_pubkey: %v", err)
	}
	if got := f.allowance(t); got.Cmp(amount) != 0 {
		t.Fatalf("allowance = %s, want %s", got, amount)
	}

	// A registered key always wins: an attacker key on the wire, even one
	// that signed the digest correctly, is not consulted.
	attackerPub, attackerPriv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	forged := f.sign(t, attackerPriv, f.token, f.holder, f.spender, amount, 1, permitTestDeadline)
	err = f.tok.Permit(f.holder, f.spender, amount, permitTestDeadline, forged, attackerPub.Bytes(), 1, nil)
	mustContain(t, err, "invalid signature")
}

func TestPermitFreshOwnerRequiresKey(t *testing.T) {
	f := newPermitFixture(t)
	amount := mustBig("1")
	sig := f.signDefault(t, amount, permitTestDeadline)
	err := f.tok.Permit(f.holder, f.spender, amount, permitTestDeadline, sig, nil, 1, nil)
	mustContain(t, err, "no registered key")
}

func TestPermitExpired(t *testing.T) {
	f := newPermitFixture(t)
	amount := mustBig("1")
	sig := f.signDefault(t, amount, permitTestDeadline)
	err := f.tok.Permit(f.holder, f.spender, amount, permitTestDeadline, sig, f.pub.Bytes(), permitTestDeadline+3600, nil)
	mustContain(t, err, "expired")
	if f.permitNonce(t) != 0 {
		t.Fatal("an expired permit must not consume the nonce")
	}
}

// deadline == 0 is not "no deadline"; an open-ended permit is refused.
func TestPermitDeadlineZeroRejected(t *testing.T) {
	f := newPermitFixture(t)
	amount := mustBig("1")
	sig := f.signDefault(t, amount, 0)
	err := f.tok.Permit(f.holder, f.spender, amount, 0, sig, f.pub.Bytes(), 0, nil)
	mustContain(t, err, "deadline must be positive")
	err = f.tok.Permit(f.holder, f.spender, amount, -1, sig, f.pub.Bytes(), 0, nil)
	mustContain(t, err, "deadline must be positive")
}

// The vm-level half of the block-time contract: the permit is judged against
// the block time the executor hands in, inclusive at the deadline. (The
// executor-level variant, through a real header, needs the router action and
// lives with it.)
func TestPermitDeadlineUsesBlockTime(t *testing.T) {
	amount := mustBig("77")

	f := newPermitFixture(t)
	sig := f.signDefault(t, amount, permitTestDeadline)
	if err := f.tok.Permit(f.holder, f.spender, amount, permitTestDeadline, sig, f.pub.Bytes(), permitTestDeadline, nil); err != nil {
		t.Fatalf("block time == deadline must be accepted: %v", err)
	}

	g := newPermitFixture(t)
	sig = g.signDefault(t, amount, permitTestDeadline)
	err := g.tok.Permit(g.holder, g.spender, amount, permitTestDeadline, sig, g.pub.Bytes(), permitTestDeadline+1, nil)
	mustContain(t, err, "expired")
}

func TestPermitReplayRejected(t *testing.T) {
	f := newPermitFixture(t)
	amount := mustBig("10")
	sig := f.signDefault(t, amount, permitTestDeadline)
	if err := f.tok.Permit(f.holder, f.spender, amount, permitTestDeadline, sig, f.pub.Bytes(), 1, nil); err != nil {
		t.Fatalf("first use: %v", err)
	}
	// Lower the allowance out of band, then replay the same signature.
	if err := f.tok.Approve(f.holder, f.spender, big.NewInt(0)); err != nil {
		t.Fatalf("approve: %v", err)
	}
	err := f.tok.Permit(f.holder, f.spender, amount, permitTestDeadline, sig, f.pub.Bytes(), 1, nil)
	mustContain(t, err, "invalid signature")
	if f.allowance(t).Sign() != 0 || f.permitNonce(t) != 1 {
		t.Fatal("a replayed permit must change nothing")
	}
	// The next nonce works.
	sig2 := f.sign(t, f.priv, f.token, f.holder, f.spender, amount, 1, permitTestDeadline)
	if err := f.tok.Permit(f.holder, f.spender, amount, permitTestDeadline, sig2, f.pub.Bytes(), 1, nil); err != nil {
		t.Fatalf("nonce 1: %v", err)
	}
}

func TestPermitTamperedFieldsRejected(t *testing.T) {
	f := newPermitFixture(t)
	amount := mustBig("1000")
	sig := f.signDefault(t, amount, permitTestDeadline)

	other := addr(0x44)
	otherToken := NewQRC20Token(f.sdb, addr(0x55))
	cases := map[string]func() error{
		"amount": func() error {
			return f.tok.Permit(f.holder, f.spender, mustBig("1001"), permitTestDeadline, sig, f.pub.Bytes(), 1, nil)
		},
		"spender": func() error {
			return f.tok.Permit(f.holder, other, amount, permitTestDeadline, sig, f.pub.Bytes(), 1, nil)
		},
		"deadline": func() error {
			return f.tok.Permit(f.holder, f.spender, amount, permitTestDeadline+1, sig, f.pub.Bytes(), 1, nil)
		},
		"contract": func() error {
			return otherToken.Permit(f.holder, f.spender, amount, permitTestDeadline, sig, f.pub.Bytes(), 1, nil)
		},
		"context": func() error {
			msg, _ := PermitMessage(f.token, f.holder, f.spender, amount, 0, permitTestDeadline)
			wrongCtx, _ := qcrypto.Sign(f.priv, msg[:], qcrypto.CtxMessage)
			return f.tok.Permit(f.holder, f.spender, amount, permitTestDeadline, wrongCtx, f.pub.Bytes(), 1, nil)
		},
		"chain id": func() error {
			// Signed for another tier: same token address, same everything,
			// different chain id in the digest.
			msg, err := types.PermitDigest("splendor-quantum-devnet-1", f.token, f.holder, f.spender, amount, 0, permitTestDeadline)
			if err != nil {
				return err
			}
			crossChain, _ := qcrypto.Sign(f.priv, msg[:], qcrypto.CtxPermit)
			return f.tok.Permit(f.holder, f.spender, amount, permitTestDeadline, crossChain, f.pub.Bytes(), 1, nil)
		},
	}
	for name, run := range cases {
		err := run()
		if err == nil {
			t.Errorf("%s: tampered permit accepted", name)
			continue
		}
		if !strings.Contains(err.Error(), "invalid signature") {
			t.Errorf("%s: want an invalid-signature error, got %v", name, err)
		}
	}
	if f.allowance(t).Sign() != 0 || f.permitNonce(t) != 0 {
		t.Fatal("tampered permits must change nothing")
	}
}

func TestPermitAmountBounds(t *testing.T) {
	f := newPermitFixture(t)
	twoPow256 := new(big.Int).Lsh(big.NewInt(1), 256)
	sig := make([]byte, qcrypto.SignatureSize)
	for name, amt := range map[string]*big.Int{
		"nil": nil, "negative": big.NewInt(-1), "2^256": twoPow256,
	} {
		if err := f.tok.Permit(f.holder, f.spender, amt, permitTestDeadline, sig, f.pub.Bytes(), 1, nil); err == nil {
			t.Errorf("%s amount accepted", name)
		}
	}
	// 2^256-1 is the largest allowance and is fine.
	maxU256 := new(big.Int).Sub(twoPow256, big.NewInt(1))
	if err := f.tok.Permit(f.holder, f.spender, maxU256, permitTestDeadline, f.signDefault(t, maxU256, permitTestDeadline), f.pub.Bytes(), 1, nil); err != nil {
		t.Fatalf("max uint256 permit: %v", err)
	}
}

// Permit records an allowance, like approve; it is the spend that pause stops.
func TestPermitWorksWhilePausedTransferFromDoesNot(t *testing.T) {
	f := newPermitFixture(t)
	amount := mustBig("250")
	if err := f.tok.Pause(f.holder); err != nil {
		t.Fatalf("pause: %v", err)
	}
	sig := f.signDefault(t, amount, permitTestDeadline)
	if err := f.tok.Permit(f.holder, f.spender, amount, permitTestDeadline, sig, f.pub.Bytes(), 1, nil); err != nil {
		t.Fatalf("permit while paused: %v", err)
	}
	if got := f.allowance(t); got.Cmp(amount) != 0 {
		t.Fatalf("allowance = %s, want %s", got, amount)
	}

	to := addr(0x66)
	err := f.tok.TransferFrom(f.spender, f.holder, to, amount)
	mustContain(t, err, "paused")
	if bal, _ := f.tok.BalanceOf(to); bal.Sign() != 0 {
		t.Fatal("transfer_from moved tokens while paused")
	}

	if err := f.tok.Unpause(f.holder); err != nil {
		t.Fatalf("unpause: %v", err)
	}
	if err := f.tok.TransferFrom(f.spender, f.holder, to, amount); err != nil {
		t.Fatalf("transfer_from after unpause: %v", err)
	}
	if bal, _ := f.tok.BalanceOf(to); bal.Cmp(amount) != 0 {
		t.Fatalf("recipient balance = %s, want %s", bal, amount)
	}
	if f.allowance(t).Sign() != 0 {
		t.Fatal("allowance must be consumed by transfer_from")
	}
}

func TestParseUint256Strict(t *testing.T) {
	maxU256 := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	ok := map[string]*big.Int{
		"0":              big.NewInt(0),
		"1":              big.NewInt(1),
		"123456789":      big.NewInt(123456789),
		maxU256.String(): maxU256,
	}
	for in, want := range ok {
		got, err := parseUint256(in)
		if err != nil {
			t.Errorf("parseUint256(%q): %v", in, err)
			continue
		}
		if got.Cmp(want) != 0 {
			t.Errorf("parseUint256(%q) = %s, want %s", in, got, want)
		}
	}
	twoPow256 := new(big.Int).Lsh(big.NewInt(1), 256)
	bad := []string{
		"", " 1", "1 ", "+1", "-1", "01", "00", "1.0", "1e3", "0x10", "1_000",
		twoPow256.String(), // exactly 2^256
		strings.Repeat("9", 79),
	}
	for _, in := range bad {
		if v, err := parseUint256(in); err == nil {
			t.Errorf("parseUint256(%q) = %s, want an error", in, v)
		}
	}
}
