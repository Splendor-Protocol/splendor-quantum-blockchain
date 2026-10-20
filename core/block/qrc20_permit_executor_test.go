package block

import (
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

// v2Forks is the tier convention: qrc20_v2 and module_revert_v1 from block 1.
func v2Forks() forks.Schedule {
	return forks.Schedule{forks.QRC20V2: 1, forks.ModuleRevertV1: 1}
}

// permitChain is one producer with a router-deployed token whose holder has a
// registered key (its deploy tx registered it), plus a relayer that pays for
// the permits. Block 1 is the deploy.
type permitChain struct {
	ex          *Executor
	sdb         *state.StateDB
	sink        *receiptSink
	holder      qcrypto.Address
	holderPriv  *qcrypto.PrivateKey
	relayer     qcrypto.Address
	relayerPriv *qcrypto.PrivateKey
	spender     qcrypto.Address
	vaddr       qcrypto.Address
	vpub        []byte
	vpriv       *qcrypto.PrivateKey
	token       qcrypto.Address
	blocks      []*types.Block
}

func newPermitChain(t *testing.T) *permitChain {
	t.Helper()
	hpub, hpriv, _ := qcrypto.GenerateKeypair()
	rpub, rpriv, _ := qcrypto.GenerateKeypair()
	vpub, vpriv, _ := qcrypto.GenerateKeypair()
	c := &permitChain{
		holder: qcrypto.DeriveAddress(hpub), holderPriv: hpriv,
		relayer: qcrypto.DeriveAddress(rpub), relayerPriv: rpriv,
		spender: testAddr(0x33),
		vaddr:   qcrypto.DeriveAddress(vpub), vpub: vpub.Bytes(), vpriv: vpriv,
		sink: &receiptSink{},
	}
	c.ex, c.sdb = newTestExecutor(t)
	t.Cleanup(func() { c.ex.Close(); c.sdb.Close() })
	c.ex.WithForks(v2Forks()).WithTxIndex(c.sink)
	c.fund(t, c.sdb)

	deploy := signedModuleCall(t, c.holder, hpriv, envelope(t, "qrc20", "deploy", map[string]any{
		"name": "Header Time", "symbol": "HT", "decimals": 0, "total_supply": "1000",
	}), 0)
	b1 := c.produce(t, deploy)
	if len(b1.Transactions) != 1 || c.sink.receipts[0].ContractAddress == nil {
		t.Fatalf("deploy not applied: %d txs, receipts %+v", len(b1.Transactions), c.sink.receipts)
	}
	c.token = *c.sink.receipts[0].ContractAddress
	return c
}

func (c *permitChain) fund(t *testing.T, sdb *state.StateDB) {
	t.Helper()
	fund(t, sdb, c.holder, 1_000_000)
	fund(t, sdb, c.relayer, 1_000_000)
}

func (c *permitChain) produce(t *testing.T, txs ...*types.Transaction) *types.Block {
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

// permitTx is a relayer-submitted permit for `amount` to the spender, signed
// by the holder for the given permit nonce and deadline. The holder's key is
// in the registry (its deploy tx put it there), so no owner_pubkey travels.
func (c *permitChain) permitTx(t *testing.T, amount *big.Int, permitNonce uint64, deadline int64, txNonce uint64) *types.Transaction {
	t.Helper()
	msg, err := vm.PermitMessage(c.token, c.holder, c.spender, amount, permitNonce, deadline)
	if err != nil {
		t.Fatalf("permit message: %v", err)
	}
	sig, err := qcrypto.Sign(c.holderPriv, msg[:], qcrypto.CtxPermit)
	if err != nil {
		t.Fatalf("sign permit: %v", err)
	}
	return signedModuleCall(t, c.relayer, c.relayerPriv, envelope(t, "qrc20", "permit", map[string]any{
		"token": c.token.String(), "owner": c.holder.String(), "spender": c.spender.String(),
		"amount": amount.String(), "deadline": deadline, "signature": sig,
	}), txNonce)
}

// replay applies every produced block on a fresh follower with the same
// schedule and returns its state.
func (c *permitChain) replay(t *testing.T) *state.StateDB {
	t.Helper()
	exB, sdbB := newTestExecutor(t)
	t.Cleanup(func() { exB.Close(); sdbB.Close() })
	exB.WithForks(v2Forks())
	c.fund(t, sdbB)
	for _, b := range c.blocks {
		if err := exB.ExecuteBlock(b); err != nil {
			t.Fatalf("follower rejected block %d: %v", b.Header.Height, err)
		}
	}
	aRoot, _ := c.sdb.StateRoot()
	bRoot, _ := sdbB.StateRoot()
	if aRoot != bRoot {
		t.Fatalf("state root mismatch: producer=%x follower=%x", aRoot[:8], bRoot[:8])
	}
	return sdbB
}

// The executor-level half of the block-time contract, through a real header:
// a permit whose deadline EQUALS the header timestamp is accepted by the
// producer and by a follower re-executing the block from the header — the
// same second on both, because the producer stamped it once and the module
// never looks at the wall clock. A deadline one second before the previous
// header is expired on both and, under module_revert_v1, lands as a failed
// receipt rather than a dropped tx.
func TestPermitDeadlineUsesHeaderTime(t *testing.T) {
	amount := big.NewInt(250)

	// The producer stamps time.Now() when it builds the block, so the deadline
	// is signed as "now" just before producing. If the wall clock rolls over a
	// second in between, the permit is legitimately expired against the
	// header; start over rather than assert on a header we did not get.
	var c *permitChain
	for attempt := 0; ; attempt++ {
		c = newPermitChain(t)
		deadline := time.Now().Unix()
		b2 := c.produce(t, c.permitTx(t, amount, 0, deadline, 0))
		if b2.Header.Timestamp == deadline {
			break
		}
		if b2.Header.Timestamp < deadline {
			t.Fatalf("header %d is before the signing time %d", b2.Header.Timestamp, deadline)
		}
		if attempt == 10 {
			t.Fatal("could not produce a block in the same second as the permit was signed")
		}
	}
	b2 := c.blocks[1]
	if len(b2.Transactions) != 1 {
		t.Fatalf("block 2 has %d txs, want the permit", len(b2.Transactions))
	}
	rc := c.sink.receipts[1]
	if rc.Status != types.TxStatusSuccess {
		t.Fatalf("permit at deadline == header time failed on the producer: %q", rc.Error)
	}
	if len(rc.Logs) != 1 || rc.Logs[0].Topics[0] != vm.TopicApproval {
		t.Fatalf("want one Approval log, got %d logs", len(rc.Logs))
	}
	tokA := vm.NewQRC20Token(c.sdb, c.token)
	if got, _ := tokA.Allowance(c.holder, c.spender); got.Cmp(amount) != 0 {
		t.Fatalf("producer allowance = %s, want %s", got, amount)
	}
	if n, _ := c.sdb.GetNonce(c.relayer); n != 1 {
		t.Fatalf("relayer nonce = %d, want 1", n)
	}
	if n, _ := c.sdb.GetNonce(c.holder); n != 1 {
		t.Fatalf("holder nonce = %d, want 1 (the holder sends no permit tx)", n)
	}

	// Expired by exactly one second relative to the LAST header: every later
	// header is >= it, so this is deterministic. Included with a failed
	// receipt (module_revert_v1); the relayer's nonce still advances.
	expired := c.permitTx(t, amount, 1, b2.Header.Timestamp-1, 1)
	b3 := c.produce(t, expired)
	if len(b3.Transactions) != 1 {
		t.Fatalf("block 3 has %d txs, want the failed permit included", len(b3.Transactions))
	}
	rc3 := c.sink.receipts[2]
	if rc3.Status != types.TxStatusFailed || !strings.Contains(rc3.Error, "expired") {
		t.Fatalf("expired permit receipt = status %d error %q", rc3.Status, rc3.Error)
	}
	if n, _ := c.sdb.GetNonce(c.relayer); n != 2 {
		t.Fatalf("relayer nonce after failed permit = %d, want 2", n)
	}
	if got, _ := tokA.PermitNonce(c.holder); got.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("permit nonce after expired permit = %s, want 1 (unchanged)", got)
	}

	// A follower reaches the same root from the headers alone.
	sdbB := c.replay(t)
	tokB := vm.NewQRC20Token(sdbB, c.token)
	if got, _ := tokB.Allowance(c.holder, c.spender); got.Cmp(amount) != 0 {
		t.Fatalf("follower allowance = %s, want %s", got, amount)
	}
	if n, _ := sdbB.GetNonce(c.relayer); n != 2 {
		t.Fatalf("follower relayer nonce = %d, want 2", n)
	}

	// A follower WITHOUT qrc20_v2 cannot apply block 2: the permit action is
	// HARD fork-inactive under its rules, so it rejects the block outright.
	exC, sdbC := newTestExecutor(t)
	defer exC.Close()
	defer sdbC.Close()
	exC.WithForks(forks.Schedule{forks.ModuleRevertV1: 1})
	c.fund(t, sdbC)
	if err := exC.ExecuteBlock(c.blocks[0]); err != nil {
		t.Fatalf("block 1 (ungated deploy) must apply without qrc20_v2: %v", err)
	}
	err := exC.ExecuteBlock(c.blocks[1])
	if err == nil || !strings.Contains(err.Error(), "unappliable tx") {
		t.Fatalf("follower without qrc20_v2: want unappliable-tx rejection, got %v", err)
	}
	if strings.Contains(err.Error(), "state root mismatch") {
		t.Fatalf("must be rejected before the root comparison: %v", err)
	}
}
