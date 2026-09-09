package vm

import (
	"encoding/hex"
	"math/big"
	"strings"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// The multisig module used to deploy owners as Active=false (so nobody could
// confirm), swallow a bad pubkey encoding, and route only deploy/submit, so
// no wallet could ever complete a transaction. These tests drive the whole
// lifecycle through the router: deploy -> submit -> confirm x N -> execute,
// plus every refusal that keeps a threshold meaning something.

type msigOwnerKey struct {
	addr qcrypto.Address
	pub  *qcrypto.PublicKey
	priv *qcrypto.PrivateKey
}

func newOwnerKey(t *testing.T) msigOwnerKey {
	t.Helper()
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	return msigOwnerKey{addr: qcrypto.DeriveAddress(pub), pub: pub, priv: priv}
}

type msigFixture struct {
	sdb    *state.StateDB
	owners []msigOwnerKey
	wallet qcrypto.Address
	sched  forks.Schedule
	nonce  uint64
}

func (f *msigFixture) ctx(caller qcrypto.Address) ModuleContext {
	f.nonce++
	return ModuleContext{Caller: caller, Nonce: f.nonce, Height: 5, Time: 1, Forks: f.sched}
}

func (f *msigFixture) call(t *testing.T, caller qcrypto.Address, action string, params any) error {
	t.Helper()
	return DispatchModuleCall(f.sdb, f.ctx(caller), moduleData(t, "multisig", action, params), &types.Receipt{})
}

func (f *msigFixture) read(t *testing.T, action string, params any) (any, error) {
	t.Helper()
	return DispatchModuleReadCall(f.sdb, ReadContext{}, moduleData(t, "multisig", action, params))
}

func ownerParams(owners []msigOwnerKey) []map[string]string {
	out := make([]map[string]string, 0, len(owners))
	for i, o := range owners {
		out = append(out, map[string]string{
			"address": o.addr.String(), "pubkey": hex.EncodeToString(o.pub.Bytes()), "name": "owner-" + string(rune('a'+i)),
		})
	}
	return out
}

// newMsigFixture deploys a 2-of-3 wallet through the router and funds it.
func newMsigFixture(t *testing.T) *msigFixture {
	t.Helper()
	f := &msigFixture{
		sdb:    newTestState(t),
		owners: []msigOwnerKey{newOwnerKey(t), newOwnerKey(t), newOwnerKey(t)},
		sched:  forks.Schedule{forks.ModuleRevertV1: 1, forks.MultisigV1: 1},
	}
	rc := &types.Receipt{}
	if err := DispatchModuleCall(f.sdb, f.ctx(f.owners[0].addr), moduleData(t, "multisig", "deploy", map[string]any{
		"threshold": 2, "owners": ownerParams(f.owners),
	}), rc); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	if rc.ContractAddress == nil {
		t.Fatal("deploy set no contract address")
	}
	f.wallet = *rc.ContractAddress
	acc, err := f.sdb.GetAccount(f.wallet)
	if err != nil {
		t.Fatal(err)
	}
	acc.AddBalance(big.NewInt(1_000_000))
	f.sdb.SetAccount(acc)
	return f
}

func (f *msigFixture) sign(t *testing.T, o msigOwnerKey, nonce uint64) string {
	t.Helper()
	w := NewMultiSigWallet(f.sdb, f.wallet)
	prop, err := w.GetProposal(nonce)
	if err != nil || prop == nil {
		t.Fatalf("proposal %d: %v", nonce, err)
	}
	to, _ := qcrypto.AddressFromString(prop.To)
	h := ProposalHash(f.wallet, nonce, prop.TxType, to, prop.Value, prop.Data)
	sig, err := qcrypto.Sign(o.priv, h[:], qcrypto.CtxMultisig)
	if err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(sig)
}

func (f *msigFixture) balance(t *testing.T, a qcrypto.Address) *big.Int {
	t.Helper()
	acc, err := f.sdb.GetAccount(a)
	if err != nil {
		t.Fatal(err)
	}
	return new(big.Int).Set(acc.Balance)
}

func TestMultisigDeployActivatesOwnersAndRefusesBadOwners(t *testing.T) {
	f := newMsigFixture(t)
	w := NewMultiSigWallet(f.sdb, f.wallet)
	for _, o := range f.owners {
		info, err := w.GetOwner(o.addr)
		if err != nil || info == nil || !info.Active {
			t.Fatalf("owner %s not active after deploy: %+v %v", o.addr, info, err)
		}
	}
	if n, _ := w.OwnerCount(); n != 3 {
		t.Fatalf("owner count = %d", n)
	}
	if th, _ := w.Threshold(); th != 2 {
		t.Fatalf("threshold = %d", th)
	}

	stranger := newOwnerKey(t)
	bad := []struct {
		name   string
		owners []map[string]string
		thresh int
		want   string
	}{
		{"key of another address", []map[string]string{{"address": f.owners[0].addr.String(), "pubkey": hex.EncodeToString(stranger.pub.Bytes())}}, 1, "does not belong"},
		{"undecodable key", []map[string]string{{"address": f.owners[0].addr.String(), "pubkey": "zz-not-hex"}}, 1, "pubkey"},
		{"duplicate owner", append(ownerParams(f.owners[:1]), ownerParams(f.owners[:1])...), 1, "duplicate"},
		{"threshold above owners", ownerParams(f.owners[:2]), 3, "threshold"},
		{"zero threshold", ownerParams(f.owners[:2]), 0, "threshold"},
	}
	for _, tc := range bad {
		err := f.call(t, f.owners[0].addr, "deploy", map[string]any{"threshold": tc.thresh, "owners": tc.owners})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: got %v, want error containing %q", tc.name, err, tc.want)
		}
		if IsHardModuleError(err) {
			t.Fatalf("%s: must be a SOFT refusal", tc.name)
		}
	}
}

func TestMultisigTransferLifecycleThroughTheRouter(t *testing.T) {
	f := newMsigFixture(t)
	w := NewMultiSigWallet(f.sdb, f.wallet)
	payee := addr(0x77)
	before := f.balance(t, f.wallet)

	if err := f.call(t, f.owners[0].addr, "submit", map[string]any{
		"wallet": f.wallet.String(), "to": payee.String(), "value": "250000", "description": "rent",
	}); err != nil {
		t.Fatalf("submit: %v", err)
	}
	// A non-owner cannot submit.
	if err := f.call(t, addr(0x99), "submit", map[string]any{"wallet": f.wallet.String(), "to": payee.String(), "value": "1"}); err == nil {
		t.Fatal("a non-owner submitted a proposal")
	}

	// One confirmation is not a quorum of two.
	if err := f.call(t, f.owners[0].addr, "confirm", map[string]any{"wallet": f.wallet.String(), "nonce": 1, "signature": f.sign(t, f.owners[0], 1)}); err != nil {
		t.Fatalf("confirm 0: %v", err)
	}
	if err := f.call(t, addr(0x99), "execute", map[string]any{"wallet": f.wallet.String(), "nonce": 1}); err == nil || !strings.Contains(err.Error(), "insufficient confirmations") {
		t.Fatalf("executed below the threshold: %v", err)
	}
	// A confirmation signed by someone else's key, or by a non-owner, is refused.
	if err := f.call(t, f.owners[1].addr, "confirm", map[string]any{"wallet": f.wallet.String(), "nonce": 1, "signature": f.sign(t, f.owners[2], 1)}); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("a confirmation signed by another owner's key was accepted: %v", err)
	}
	stranger := newOwnerKey(t)
	if err := f.call(t, stranger.addr, "confirm", map[string]any{"wallet": f.wallet.String(), "nonce": 1, "signature": f.sign(t, stranger, 1)}); err == nil || !strings.Contains(err.Error(), "not an active owner") {
		t.Fatalf("a stranger confirmed: %v", err)
	}
	// Double-confirm is refused.
	if err := f.call(t, f.owners[0].addr, "confirm", map[string]any{"wallet": f.wallet.String(), "nonce": 1, "signature": f.sign(t, f.owners[0], 1)}); err == nil || !strings.Contains(err.Error(), "already confirmed") {
		t.Fatalf("double confirmation accepted: %v", err)
	}

	// Second owner confirms: quorum. Anyone may execute.
	if err := f.call(t, f.owners[1].addr, "confirm", map[string]any{"wallet": f.wallet.String(), "nonce": 1, "signature": f.sign(t, f.owners[1], 1)}); err != nil {
		t.Fatalf("confirm 1: %v", err)
	}
	if n, _ := w.ConfirmationCount(1); n != 2 {
		t.Fatalf("confirmation count = %d", n)
	}
	if err := f.call(t, addr(0x99), "execute", map[string]any{"wallet": f.wallet.String(), "nonce": 1}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := f.balance(t, payee); got.Cmp(big.NewInt(250_000)) != 0 {
		t.Fatalf("payee balance = %s, want 250000", got)
	}
	if got := f.balance(t, f.wallet); got.Cmp(new(big.Int).Sub(before, big.NewInt(250_000))) != 0 {
		t.Fatalf("wallet balance = %s", got)
	}

	// Replay: a second execute, a late confirm and a revoke are all refused
	// and move nothing.
	for _, action := range []string{"execute", "revoke", "cancel"} {
		err := f.call(t, f.owners[0].addr, action, map[string]any{"wallet": f.wallet.String(), "nonce": 1})
		if err == nil || !strings.Contains(err.Error(), "already executed") {
			t.Fatalf("%s on an executed proposal: %v", action, err)
		}
	}
	if err := f.call(t, f.owners[2].addr, "confirm", map[string]any{"wallet": f.wallet.String(), "nonce": 1, "signature": f.sign(t, f.owners[2], 1)}); err == nil || !strings.Contains(err.Error(), "already executed") {
		t.Fatalf("confirm on an executed proposal: %v", err)
	}
	if got := f.balance(t, payee); got.Cmp(big.NewInt(250_000)) != 0 {
		t.Fatalf("payee balance moved on replay: %s", got)
	}

	// The read side agrees.
	th, err := f.read(t, "proposal_hash", map[string]any{"wallet": f.wallet.String(), "nonce": 1})
	if err != nil {
		t.Fatalf("proposal_hash: %v", err)
	}
	if m, ok := th.(map[string]string); !ok || len(m["hash"]) != 64 {
		t.Fatalf("proposal_hash read = %v", th)
	}
}

// A confirmation is bound to this chain: the same wallet exists at the same
// address on every tier, so a signature must not carry across.
func TestMultisigConfirmationIsBoundToTheChain(t *testing.T) {
	f := newMsigFixture(t)
	if err := f.call(t, f.owners[0].addr, "submit", map[string]any{"wallet": f.wallet.String(), "to": addr(0x77).String(), "value": "1"}); err != nil {
		t.Fatal(err)
	}
	saved := types.ChainID
	types.ChainID = "splendor-quantum-testnet-1"
	foreign := f.sign(t, f.owners[0], 1)
	types.ChainID = saved
	if err := f.call(t, f.owners[0].addr, "confirm", map[string]any{"wallet": f.wallet.String(), "nonce": 1, "signature": foreign}); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("a confirmation signed for another chain was accepted: %v", err)
	}
}

func TestMultisigOwnerChangesAndThresholdGuards(t *testing.T) {
	f := newMsigFixture(t)
	w := NewMultiSigWallet(f.sdb, f.wallet)
	confirmBy := func(nonce uint64, owners ...msigOwnerKey) {
		t.Helper()
		for _, o := range owners {
			if err := f.call(t, o.addr, "confirm", map[string]any{"wallet": f.wallet.String(), "nonce": nonce, "signature": f.sign(t, o, nonce)}); err != nil {
				t.Fatalf("confirm %d by %s: %v", nonce, o.addr, err)
			}
		}
	}
	execute := func(nonce uint64) error {
		return f.call(t, f.owners[0].addr, "execute", map[string]any{"wallet": f.wallet.String(), "nonce": nonce})
	}

	// Contract-call proposals are refused at submit: the module cannot run them.
	if _, err := w.Submit(f.owners[0].addr, MSigCallAny, addr(0x01), nil, []byte{1, 2, 3, 4}, ""); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("a contract-call proposal was accepted: %v", err)
	}

	// Adding an owner whose key is not theirs is refused; a proper one lands.
	stranger := newOwnerKey(t)
	fourth := newOwnerKey(t)
	if err := f.call(t, f.owners[0].addr, "submit", map[string]any{
		"wallet": f.wallet.String(), "tx_type": "add_owner", "owner": fourth.addr.String(), "pubkey": hex.EncodeToString(stranger.pub.Bytes()),
	}); err == nil || !strings.Contains(err.Error(), "does not belong") {
		t.Fatalf("add_owner with a mismatched key was accepted: %v", err)
	}
	if err := f.call(t, f.owners[0].addr, "submit", map[string]any{
		"wallet": f.wallet.String(), "tx_type": "add_owner", "owner": fourth.addr.String(), "pubkey": hex.EncodeToString(fourth.pub.Bytes()), "name": "fourth",
	}); err != nil {
		t.Fatalf("submit add_owner: %v", err)
	}
	confirmBy(1, f.owners[0], f.owners[1])
	if err := execute(1); err != nil {
		t.Fatalf("execute add_owner: %v", err)
	}
	if info, _ := w.GetOwner(fourth.addr); info == nil || !info.Active || info.Name != "fourth" {
		t.Fatalf("fourth owner not added: %+v", info)
	}
	if n, _ := w.OwnerCount(); n != 4 {
		t.Fatalf("owner count = %d, want 4", n)
	}

	// Removing below the threshold is refused: raise the threshold to 4 first,
	// then removal of anyone must fail.
	if err := f.call(t, f.owners[0].addr, "submit", map[string]any{"wallet": f.wallet.String(), "tx_type": "change_threshold", "threshold": 4}); err != nil {
		t.Fatalf("submit change_threshold: %v", err)
	}
	confirmBy(2, f.owners[0], f.owners[1])
	if err := execute(2); err != nil {
		t.Fatalf("execute change_threshold: %v", err)
	}
	if th, _ := w.Threshold(); th != 4 {
		t.Fatalf("threshold = %d, want 4", th)
	}
	if err := f.call(t, f.owners[0].addr, "submit", map[string]any{"wallet": f.wallet.String(), "tx_type": "remove_owner", "owner": fourth.addr.String()}); err == nil || !strings.Contains(err.Error(), "below the threshold") {
		t.Fatalf("removal below the threshold was accepted: %v", err)
	}
	// A threshold above the owner count is refused.
	if err := f.call(t, f.owners[0].addr, "submit", map[string]any{"wallet": f.wallet.String(), "tx_type": "change_threshold", "threshold": 5}); err == nil || !strings.Contains(err.Error(), "invalid threshold") {
		t.Fatalf("threshold above owner count accepted: %v", err)
	}

	// Lower the threshold back to 2 (needs all four now), then remove the
	// fourth owner. Their earlier confirmation on a pending proposal must
	// stop counting.
	if err := f.call(t, f.owners[0].addr, "submit", map[string]any{"wallet": f.wallet.String(), "tx_type": "change_threshold", "threshold": 2}); err != nil {
		t.Fatal(err)
	}
	confirmBy(3, f.owners[0], f.owners[1], f.owners[2], fourth)
	if err := execute(3); err != nil {
		t.Fatalf("execute change_threshold back: %v", err)
	}
	// Pending transfer confirmed by owner0 and the fourth owner (a quorum of 2).
	if err := f.call(t, f.owners[0].addr, "submit", map[string]any{"wallet": f.wallet.String(), "to": addr(0x78).String(), "value": "10"}); err != nil {
		t.Fatal(err)
	}
	confirmBy(4, f.owners[0], fourth)
	// Remove the fourth owner (owner1 and owner2 confirm).
	if err := f.call(t, f.owners[1].addr, "submit", map[string]any{"wallet": f.wallet.String(), "tx_type": "remove_owner", "owner": fourth.addr.String()}); err != nil {
		t.Fatal(err)
	}
	confirmBy(5, f.owners[1], f.owners[2])
	if err := execute(5); err != nil {
		t.Fatalf("execute remove_owner: %v", err)
	}
	if info, _ := w.GetOwner(fourth.addr); info == nil || info.Active {
		t.Fatalf("fourth owner still active: %+v", info)
	}
	// Proposal 4 now has one LIVE confirmation: not executable.
	if err := execute(4); err == nil || !strings.Contains(err.Error(), "insufficient confirmations") {
		t.Fatalf("a removed owner's confirmation still counted: %v", err)
	}
	if f.balance(t, addr(0x78)).Sign() != 0 {
		t.Fatal("proposal 4 paid out")
	}
	// Only the proposer can cancel.
	if err := f.call(t, f.owners[1].addr, "cancel", map[string]any{"wallet": f.wallet.String(), "nonce": 4}); err == nil || !strings.Contains(err.Error(), "only the proposer") {
		t.Fatalf("a non-proposer cancelled: %v", err)
	}
	if err := f.call(t, f.owners[0].addr, "cancel", map[string]any{"wallet": f.wallet.String(), "nonce": 4}); err != nil {
		t.Fatalf("proposer cancel: %v", err)
	}
	if err := execute(4); err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("a cancelled proposal executed: %v", err)
	}
}

// Below multisig_v1 every action is HARD, so a chain that has not scheduled
// the module cannot be made to carry these transactions.
func TestMultisigIsHardBelowTheFork(t *testing.T) {
	f := newMsigFixture(t)
	f.sched = forks.Schedule{forks.ModuleRevertV1: 1, forks.MultisigV1: 1000}
	for _, action := range []string{"deploy", "submit", "confirm", "revoke", "execute", "cancel"} {
		err := f.call(t, f.owners[0].addr, action, map[string]any{"wallet": f.wallet.String(), "nonce": 0})
		if err == nil || !IsHardModuleError(err) {
			t.Fatalf("%s below the fork: got %v, want HARD", action, err)
		}
	}
}
