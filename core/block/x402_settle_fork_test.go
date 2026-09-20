package block

import (
	"math/big"
	"testing"
	"time"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/x402"
)

func x402SettleForks() forks.Schedule {
	return forks.Schedule{forks.X402SettleV1: 1}
}

// signedX402 builds the payer-signed TxX402 that settles an X402 payment. Data
// carries the RAW 32-byte payment nonce: the executor derives the registry key
// as SHA3-256(from || Data), the same derivation x402.NonceKey performs.
func signedX402(
	t *testing.T,
	from qcrypto.Address,
	fromPub *qcrypto.PublicKey,
	priv *qcrypto.PrivateKey,
	to qcrypto.Address,
	amount int64,
	payNonce [32]byte,
	accountNonce uint64,
) *types.Transaction {
	t.Helper()
	tx := &types.Transaction{
		Version:   1,
		ChainID:   types.ChainID,
		Type:      types.TxX402,
		AlgID:     qcrypto.AlgMLDSA65,
		From:      from,
		To:        to,
		Amount:    big.NewInt(amount),
		Fee:       big.NewInt(testFee),
		Nonce:     accountNonce,
		Timestamp: time.Now().Unix(),
		Data:      payNonce[:],
		PublicKey: fromPub.Bytes(),
	}
	if err := tx.Sign(priv, true); err != nil {
		t.Fatalf("sign x402 tx: %v", err)
	}
	return tx
}

// The X402 settlement record is a state-root leaf. Writing it ungated is a
// consensus change with no activation height: an upgraded node and a node still
// on the old build both agree the payment nonce is consumed and disagree on the
// state root, so the network parts at the first X402 transaction after the first
// node restarts — a rolling upgrade becomes a fork.
//
// This pins the gate on both sides:
//
//   - fork OFF  → no settlement leaf, and the root is the pre-record root, so an
//     old-rules follower accepts the block;
//   - fork ON   → the record exists, binds the payment that actually happened,
//     and the confirm path (Processor.SettlementMatch) resolves it.
//
// It also pins the divergence itself: the SAME transaction under the two rule
// sets produces two different state roots, which is why the height is required.
func TestX402SettlementRecordIsForkGated(t *testing.T) {
	payerPub, payerPriv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	payer := qcrypto.DeriveAddress(payerPub)
	payee := testAddr(0x55)
	vpub, vpriv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("validator keygen: %v", err)
	}
	vaddr := qcrypto.DeriveAddress(vpub)

	var payNonce [32]byte
	payNonce[0] = 0xEE
	const amount = int64(500)
	nonceKey := x402.NonceKey(payer, payNonce)

	produce := func(schedule forks.Schedule) (*types.Block, *state.StateDB, func()) {
		t.Helper()
		ex, sdb := newTestExecutor(t)
		ex.WithForks(schedule)
		fund(t, sdb, payer, 1_000_000)
		tx := signedX402(t, payer, payerPub, payerPriv, payee, amount, payNonce, 0)
		b, err := ex.ProduceBlock([]*types.Transaction{tx}, vaddr, vpub.Bytes(), vpriv, [32]byte{}, 1)
		if err != nil {
			t.Fatalf("produce: %v", err)
		}
		if len(b.Transactions) != 1 {
			t.Fatalf("block carries %d txs, want the X402 payment", len(b.Transactions))
		}
		return b, sdb, func() { ex.Close(); sdb.Close() }
	}

	// ── Fork OFF: the pre-change registry, nothing else ──────────────────────
	blockOff, sdbOff, closeOff := produce(nil)
	defer closeOff()

	if used, err := sdbOff.IsX402NonceUsed(nonceKey); err != nil || !used {
		t.Fatalf("pre-fork: nonce marker used=%v err=%v, want the nonce consumed", used, err)
	}
	rec, err := sdbOff.GetX402Settlement(nonceKey)
	if err != nil {
		t.Fatalf("pre-fork settlement lookup: %v", err)
	}
	if rec != nil {
		t.Fatalf("pre-fork wrote a settlement leaf (%+v): the state root diverges from a node "+
			"still on the old build, which forks the chain at the first X402 payment", rec)
	}

	// ── Fork ON: the record is written and binds the real payment ────────────
	blockOn, sdbOn, closeOn := produce(x402SettleForks())
	defer closeOn()

	recOn, err := sdbOn.GetX402Settlement(nonceKey)
	if err != nil {
		t.Fatalf("post-fork settlement lookup: %v", err)
	}
	if recOn == nil {
		t.Fatal("post-fork wrote no settlement record")
	}
	if !recOn.Matches(types.ChainID, payer, payee, big.NewInt(amount)) {
		t.Fatalf("settlement record does not bind the payment that happened: %+v", recOn)
	}
	if recOn.Height != 1 {
		t.Fatalf("settlement height = %d, want 1", recOn.Height)
	}

	// The confirm path reads the record, so it confirms only under the fork.
	payload := &x402.PaymentPayload{
		Version: 1, ChainID: types.ChainID,
		From: payer, To: payee, Amount: big.NewInt(amount), Nonce: payNonce,
	}
	if ok, err := x402.NewProcessor(sdbOn).SettlementMatch(payload); err != nil || !ok {
		t.Fatalf("post-fork SettlementMatch = %v (err %v), want the payment confirmed", ok, err)
	}
	if ok, err := x402.NewProcessor(sdbOff).SettlementMatch(payload); err != nil || ok {
		t.Fatalf("pre-fork SettlementMatch = %v (err %v), want no record to confirm from", ok, err)
	}

	// ── The gate is load-bearing: the roots differ ───────────────────────────
	if blockOff.Header.StateRoot == blockOn.Header.StateRoot {
		t.Fatal("premise changed: the settlement leaf no longer affects the state root, " +
			"so this gate would be unnecessary — re-derive the rule before deleting it")
	}

	// ── The pre-change root is what an old-rules follower reproduces ─────────
	exFollower, sdbFollower := newTestExecutor(t)
	defer exFollower.Close()
	defer sdbFollower.Close()
	fund(t, sdbFollower, payer, 1_000_000)
	if err := exFollower.ExecuteBlock(blockOff); err != nil {
		t.Fatalf("a node on the old rules rejected a pre-fork X402 block: %v", err)
	}

	// ── And the divergence, stated as the incident it causes ────────────────
	exUpgraded, sdbUpgraded := newTestExecutor(t)
	defer exUpgraded.Close()
	defer sdbUpgraded.Close()
	exUpgraded.WithForks(x402SettleForks())
	fund(t, sdbUpgraded, payer, 1_000_000)
	if err := exUpgraded.ExecuteBlock(blockOff); err == nil {
		t.Fatal("a node with the record rule active accepted a block built without it: " +
			"the two rule sets must not both validate the same X402 block")
	}
}

// A schedule that enforces the SQVL x402 spend linkage before the chain writes
// the settlement records it resolves describes a chain where every x402 spend
// fails. That is a load-time error, not a live outage.
func TestVerifyIdentityRequiresSettlementRecords(t *testing.T) {
	base := forks.Schedule{forks.ModuleRevertV1: 1, forks.VerifyV1: 1}

	missing := forks.Schedule{}
	for k, v := range base {
		missing[k] = v
	}
	missing[forks.VerifyIdentityV1] = 10
	if err := missing.Validate(); err == nil {
		t.Fatal("verify_identity_v1 without x402_settle_v1 was accepted")
	}

	late := forks.Schedule{}
	for k, v := range base {
		late[k] = v
	}
	late[forks.VerifyIdentityV1] = 10
	late[forks.X402SettleV1] = 11
	if err := late.Validate(); err == nil {
		t.Fatal("x402_settle_v1 scheduled after verify_identity_v1 was accepted")
	}

	ok := forks.Schedule{}
	for k, v := range base {
		ok[k] = v
	}
	ok[forks.VerifyIdentityV1] = 10
	ok[forks.X402SettleV1] = 10
	if err := ok.Validate(); err != nil {
		t.Fatalf("same-height schedule refused: %v", err)
	}
}
