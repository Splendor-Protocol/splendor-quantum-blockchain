package vm

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// mtForks schedules qrc1155_v1 from block 1, the tier convention.
func mtForks() forks.Schedule { return forks.Schedule{forks.QRC1155V1: 1} }

// mtCtx is a block-1 context with qrc1155_v1 active.
func mtCtx(caller qcrypto.Address, nonce uint64) ModuleContext {
	return ModuleContext{Caller: caller, Nonce: nonce, Height: 1, Time: 1, Forks: mtForks()}
}

// mtFixture is one collection deployed THROUGH the router (so it carries the
// IsContract marker) by `owner`, plus two keyless users.
type mtFixture struct {
	sdb        *state.StateDB
	collection qcrypto.Address
	tok        *QRC1155Token
	owner      qcrypto.Address
	alice      qcrypto.Address
	bob        qcrypto.Address
	nonce      uint64
}

func newMTFixture(t *testing.T) *mtFixture {
	t.Helper()
	sdb := newTestState(t)
	owner := addr(0x11)
	dep := &types.Receipt{}
	if err := DispatchModuleCall(sdb, mtCtx(owner, 1), moduleData(t, "qrc1155", "deploy", qrc1155DeployParams{
		Name: "Positions", URIPrefix: "https://meta/",
	}), dep); err != nil {
		t.Fatalf("deploy via router: %v", err)
	}
	if dep.ContractAddress == nil {
		t.Fatal("deploy set no contract address")
	}
	return &mtFixture{
		sdb: sdb, collection: *dep.ContractAddress, tok: NewQRC1155Token(sdb, *dep.ContractAddress),
		owner: owner, alice: addr(0x22), bob: addr(0x33), nonce: 1,
	}
}

// call submits one qrc1155 write through the router under the active fork.
func (f *mtFixture) call(t *testing.T, caller qcrypto.Address, action string, params any) (*types.Receipt, error) {
	t.Helper()
	f.nonce++
	rc := &types.Receipt{}
	return rc, DispatchModuleCall(f.sdb, mtCtx(caller, f.nonce), moduleData(t, "qrc1155", action, params), rc)
}

// read runs one qrc1155 read through the read dispatcher.
func (f *mtFixture) read(t *testing.T, action string, params any) map[string]interface{} {
	t.Helper()
	res, err := DispatchModuleReadCall(f.sdb, ReadContext{}, moduleData(t, "qrc1155", action, params))
	if err != nil {
		t.Fatalf("%s: %v", action, err)
	}
	return res.(map[string]interface{})
}

func (f *mtFixture) balance(t *testing.T, holder qcrypto.Address, id int64) *big.Int {
	t.Helper()
	b, err := f.tok.BalanceOf(holder, big.NewInt(id))
	if err != nil {
		t.Fatalf("balance: %v", err)
	}
	return b
}

func (f *mtFixture) act(fields qrc1155Action) qrc1155Action {
	fields.Collection = f.collection.String()
	return fields
}

// word is the 32-byte big-endian encoding of v, as the event data uses.
func word(v int64) []byte {
	var w [32]byte
	b := big.NewInt(v).Bytes()
	copy(w[32-len(b):], b)
	return w[:]
}

func wantLog(t *testing.T, l *types.Log, topic [32]byte, indexed []qcrypto.Address, data []byte) {
	t.Helper()
	if l.Topics[0] != topic {
		t.Fatalf("topic[0] = %x, want %x", l.Topics[0][:4], topic[:4])
	}
	if len(l.Topics) != 1+len(indexed) {
		t.Fatalf("topics = %d, want %d", len(l.Topics), 1+len(indexed))
	}
	for i, a := range indexed {
		if l.Topics[1+i] != AddrTopic(a) {
			t.Fatalf("topic[%d] = %x, want %s", 1+i, l.Topics[1+i][:4], a)
		}
	}
	if !bytes.Equal(l.Data, data) {
		t.Fatalf("data = %x, want %x", l.Data, data)
	}
}

// Every qrc1155 write is HARD ErrForkInactive before its height — with no
// schedule, and with the fork one block ahead — and leaves no trace: no
// collection, no balance, no logs. Admission says the same through
// PreflightModuleCall.
func TestQRC1155ActionsGatedOffAreHardAndWriteNothing(t *testing.T) {
	f := newMTFixture(t)
	if _, err := f.call(t, f.owner, "mint", f.act(qrc1155Action{To: f.alice.String(), ID: "1", Amount: "5"})); err != nil {
		t.Fatalf("seed mint: %v", err)
	}
	one := []string{"1"}
	actions := []struct {
		name   string
		params any
	}{
		{"deploy", qrc1155DeployParams{Name: "Gated"}},
		{"mint", f.act(qrc1155Action{To: f.alice.String(), ID: "1", Amount: "1"})},
		{"mint_batch", f.act(qrc1155Action{To: f.alice.String(), IDs: one, Amounts: one})},
		{"transfer", f.act(qrc1155Action{To: f.bob.String(), ID: "1", Amount: "1"})},
		{"batch_transfer", f.act(qrc1155Action{To: f.bob.String(), IDs: one, Amounts: one})},
		{"set_approval_for_all", f.act(qrc1155Action{Operator: f.bob.String(), Approved: true})},
		{"burn", f.act(qrc1155Action{ID: "1", Amount: "1"})},
		{"burn_batch", f.act(qrc1155Action{IDs: one, Amounts: one})},
		{"transfer_ownership", f.act(qrc1155Action{NewOwner: f.bob.String()})},
	}
	for _, sched := range []struct {
		name string
		ctx  ModuleContext
	}{
		{"no schedule", ModuleContext{Caller: f.owner, Nonce: 9, Height: 1, Time: 1}},
		{"fork one block ahead", ModuleContext{Caller: f.owner, Nonce: 9, Height: 1, Time: 1, Forks: forks.Schedule{forks.QRC1155V1: 2}}},
	} {
		for _, a := range actions {
			caller := f.owner
			if a.name == "transfer" || a.name == "batch_transfer" || a.name == "burn" || a.name == "burn_batch" || a.name == "set_approval_for_all" {
				caller = f.alice
			}
			ctx := sched.ctx
			ctx.Caller = caller
			rc := &types.Receipt{}
			err := DispatchModuleCall(f.sdb, ctx, moduleData(t, "qrc1155", a.name, a.params), rc)
			if err == nil {
				t.Fatalf("%s/%s: accepted before the fork", sched.name, a.name)
			}
			if !IsHardModuleError(err) || !errors.Is(err, ErrForkInactive) {
				t.Fatalf("%s/%s: want HARD ErrForkInactive, got %v", sched.name, a.name, err)
			}
			if len(rc.Logs) != 0 || rc.ContractAddress != nil {
				t.Fatalf("%s/%s: gated-off action wrote a receipt", sched.name, a.name)
			}
		}
	}
	if got := f.balance(t, f.alice, 1); got.Cmp(big.NewInt(5)) != 0 {
		t.Fatalf("gated-off actions changed alice's balance: %s", got)
	}
	if got := f.balance(t, f.bob, 1); got.Sign() != 0 {
		t.Fatalf("gated-off actions credited bob: %s", got)
	}
	if owner, _ := f.tok.Owner(); owner != f.owner {
		t.Fatal("gated-off transfer_ownership changed the owner")
	}
	if ok, _ := f.tok.IsApprovedForAll(f.alice, f.bob); ok {
		t.Fatal("gated-off set_approval_for_all approved")
	}
	// An unknown action is still its own HARD class, not a fork complaint.
	err := DispatchModuleCall(f.sdb, mtCtx(f.owner, 9), moduleData(t, "qrc1155", "explode", struct{}{}), &types.Receipt{})
	if !IsHardModuleError(err) || errors.Is(err, ErrForkInactive) || !strings.Contains(err.Error(), "unknown action") {
		t.Fatalf("unknown action: want HARD unknown action, got %v", err)
	}
	// Admission mirrors the gate.
	mint := moduleData(t, "qrc1155", "mint", actions[1].params)
	if _, err := PreflightModuleCall(mint, nil, 0); err != nil {
		t.Fatalf("preflight without a schedule must not gate: %v", err)
	}
	if _, err := PreflightModuleCall(mint, forks.Schedule{forks.QRC1155V1: 2}, 1); !errors.Is(err, ErrForkInactive) {
		t.Fatalf("preflight one block early: want ErrForkInactive, got %v", err)
	}
	if _, err := PreflightModuleCall(mint, mtForks(), 1); err != nil {
		t.Fatalf("preflight at the height: %v", err)
	}
	if _, err := PreflightModuleCall(moduleData(t, "qrc1155", "balance_of", struct{}{}), mtForks(), 1); err == nil || !strings.Contains(err.Error(), "read action") {
		t.Fatalf("a read in a TxCall must be refused at admission: %v", err)
	}
}

// deploy writes the owner slot and the IsContract marker; the same
// (deployer, nonce) can never initialise the collection twice, and a
// module-level deploy at an already-initialised address is refused.
func TestQRC1155DeployWritesOwnerAndRefusesReinit(t *testing.T) {
	f := newMTFixture(t)
	acc, err := f.sdb.GetAccount(f.collection)
	if err != nil || !acc.IsContract {
		t.Fatalf("deployed collection is not marked as a contract (err=%v)", err)
	}
	if owner, _ := f.tok.Owner(); owner != f.owner {
		t.Fatalf("owner = %s, want deployer %s", owner, f.owner)
	}
	info := f.read(t, "token_info", map[string]string{"collection": f.collection.String()})
	if info["owner"] != f.owner.String() || info["name"] != "Positions" || info["uri_prefix"] != "https://meta/" || info["exists"] != true || info["module_managed"] != false {
		t.Fatalf("token_info = %v", info)
	}

	// Same deployer, same nonce → same address → re-init refused, SOFT.
	if _, err := DeployQRC1155(f.sdb, f.owner, "Again", "", 1); err == nil || !strings.Contains(err.Error(), "already initialised") {
		t.Fatalf("re-deploy at the same address: want already initialised, got %v", err)
	}
	if err := initQRC1155At(f.sdb, f.collection, f.bob, "Hijack", ""); err == nil {
		t.Fatal("initQRC1155At must refuse an initialised collection")
	}
	if owner, _ := f.tok.Owner(); owner != f.owner {
		t.Fatalf("a refused re-init changed the owner to %s", owner)
	}
	rc := &types.Receipt{}
	err = DispatchModuleCall(f.sdb, mtCtx(f.owner, 1), moduleData(t, "qrc1155", "deploy", qrc1155DeployParams{Name: "Again"}), rc)
	mustContain(t, err, "already initialised")
	if IsHardModuleError(err) {
		t.Fatalf("re-init is the action's own failure, must be SOFT: %v", err)
	}
	if name, _ := f.tok.Name(); name != "Positions" {
		t.Fatalf("a refused re-init rewrote the name to %q", name)
	}
	if err := initQRC1155At(f.sdb, addr(0x99), qcrypto.Address{}, "Nobody", ""); err == nil {
		t.Fatal("a zero owner must be refused (it would defeat the re-init guard)")
	}

	// A non-deploy write against an address nobody deployed is refused.
	_, err = f.call(t, f.owner, "mint", qrc1155Action{Collection: addr(0xbb).String(), To: f.alice.String(), ID: "1", Amount: "1"})
	mustContain(t, err, "not a deployed contract")
	if IsHardModuleError(err) {
		t.Fatalf("the IsContract guard is SOFT: %v", err)
	}
}

// Minting is owner-only; the events carry the ERC-1155 layout with SHA3
// topics: TransferSingle(operator, from, to indexed; id, value) with from =
// zero on mint, and TransferBatch data = n ‖ ids ‖ values as 32-byte words.
func TestQRC1155OwnerOnlyMintAndEventLayouts(t *testing.T) {
	f := newMTFixture(t)

	_, err := f.call(t, f.alice, "mint", f.act(qrc1155Action{To: f.alice.String(), ID: "7", Amount: "100"}))
	mustContain(t, err, "not owner")
	if IsHardModuleError(err) {
		t.Fatalf("not-owner is SOFT: %v", err)
	}
	if f.balance(t, f.alice, 7).Sign() != 0 {
		t.Fatal("a refused mint credited the recipient")
	}

	rc, err := f.call(t, f.owner, "mint", f.act(qrc1155Action{To: f.alice.String(), ID: "7", Amount: "100", URI: "ipfs://seven"}))
	if err != nil {
		t.Fatalf("owner mint: %v", err)
	}
	wantTopics(t, rc, "TransferSingle")
	wantLog(t, rc.Logs[0], TopicTransferSingle, []qcrypto.Address{f.owner, {}, f.alice}, append(word(7), word(100)...))
	if rc.Logs[0].Address != f.collection {
		t.Fatalf("log address = %s, want the collection", rc.Logs[0].Address)
	}
	if got := f.balance(t, f.alice, 7); got.Cmp(big.NewInt(100)) != 0 {
		t.Fatalf("alice balance = %s, want 100", got)
	}
	if supply, _ := f.tok.TotalSupply(big.NewInt(7)); supply.Cmp(big.NewInt(100)) != 0 {
		t.Fatalf("supply = %s, want 100", supply)
	}
	if uri := f.read(t, "uri", map[string]string{"collection": f.collection.String(), "id": "7"}); uri["uri"] != "ipfs://seven" {
		t.Fatalf("per-token uri = %v", uri["uri"])
	}
	if uri := f.read(t, "uri", map[string]string{"collection": f.collection.String(), "id": "8"}); uri["uri"] != "https://meta/8" {
		t.Fatalf("prefix uri = %v", uri["uri"])
	}

	// mint_batch: one TransferBatch, data = n ‖ ids ‖ values.
	rc, err = f.call(t, f.owner, "mint_batch", f.act(qrc1155Action{
		To: f.bob.String(), IDs: []string{"1", "2", "3"}, Amounts: []string{"10", "20", "30"}, URIs: []string{"a"},
	}))
	if err != nil {
		t.Fatalf("mint_batch: %v", err)
	}
	wantTopics(t, rc, "TransferBatch")
	data := word(3)
	for _, v := range []int64{1, 2, 3, 10, 20, 30} {
		data = append(data, word(v)...)
	}
	wantLog(t, rc.Logs[0], TopicTransferBatch, []qcrypto.Address{f.owner, {}, f.bob}, data)
	bals := f.read(t, "balance_of_batch", map[string]any{
		"collection": f.collection.String(),
		"owners":     []string{f.bob.String(), f.bob.String(), f.bob.String(), f.alice.String()},
		"ids":        []string{"1", "2", "3", "7"},
	})["balances"].([]string)
	if strings.Join(bals, ",") != "10,20,30,100" {
		t.Fatalf("balance_of_batch = %v", bals)
	}

	// Strict decoding: leading zero, sign, positive amount, zero recipient.
	for _, bad := range []struct {
		name string
		p    qrc1155Action
		want string
	}{
		{"leading zero id", f.act(qrc1155Action{To: f.alice.String(), ID: "07", Amount: "1"}), "leading zero"},
		{"signed amount", f.act(qrc1155Action{To: f.alice.String(), ID: "7", Amount: "+1"}), "invalid decimal"},
		{"zero amount", f.act(qrc1155Action{To: f.alice.String(), ID: "7", Amount: "0"}), "must be positive"},
		{"zero recipient", f.act(qrc1155Action{To: qcrypto.Address{}.String(), ID: "7", Amount: "1"}), "zero address"},
		{"uris longer than ids", f.act(qrc1155Action{To: f.alice.String(), IDs: []string{"1"}, Amounts: []string{"1"}, URIs: []string{"a", "b"}}), "more uris"},
	} {
		action := "mint"
		if bad.p.IDs != nil {
			action = "mint_batch"
		}
		_, err := f.call(t, f.owner, action, bad.p)
		if err == nil || !strings.Contains(err.Error(), bad.want) {
			t.Fatalf("%s: want %q, got %v", bad.name, bad.want, err)
		}
	}

	// transfer_ownership: the old owner loses mint; the new one gains it.
	rc, err = f.call(t, f.owner, "transfer_ownership", f.act(qrc1155Action{NewOwner: f.bob.String()}))
	if err != nil {
		t.Fatalf("transfer_ownership: %v", err)
	}
	wantTopics(t, rc, "OwnershipTransferred")
	_, err = f.call(t, f.owner, "mint", f.act(qrc1155Action{To: f.alice.String(), ID: "7", Amount: "1"}))
	mustContain(t, err, "not owner")
	if _, err := f.call(t, f.bob, "mint", f.act(qrc1155Action{To: f.alice.String(), ID: "7", Amount: "1"})); err != nil {
		t.Fatalf("new owner mint: %v", err)
	}
	_, err = f.call(t, f.bob, "transfer_ownership", f.act(qrc1155Action{NewOwner: qcrypto.Address{}.String()}))
	mustContain(t, err, "zero address")
}

// Every batch path — mint_batch, burn_batch, batch_transfer, and the
// balance_of_batch read — refuses 65 entries and accepts 64. The cap is
// checked before any element is parsed, and the token enforces it too.
func TestQRC1155BatchCapRejected(t *testing.T) {
	f := newMTFixture(t)
	list := func(n int, v string) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = v
		}
		return out
	}
	ids := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprint(i + 1)
		}
		return out
	}

	if _, err := f.call(t, f.owner, "mint_batch", f.act(qrc1155Action{To: f.alice.String(), IDs: ids(64), Amounts: list(64, "5")})); err != nil {
		t.Fatalf("mint_batch of 64: %v", err)
	}
	for _, tc := range []struct {
		caller qcrypto.Address
		action string
		p      qrc1155Action
	}{
		{f.owner, "mint_batch", f.act(qrc1155Action{To: f.alice.String(), IDs: ids(65), Amounts: list(65, "1")})},
		{f.alice, "burn_batch", f.act(qrc1155Action{IDs: ids(65), Amounts: list(65, "1")})},
		{f.alice, "batch_transfer", f.act(qrc1155Action{To: f.bob.String(), IDs: ids(65), Amounts: list(65, "1")})},
		// Over the cap AND malformed: the cap complaint comes first.
		{f.owner, "mint_batch", f.act(qrc1155Action{To: f.alice.String(), IDs: list(65, "x"), Amounts: list(65, "1")})},
	} {
		_, err := f.call(t, tc.caller, tc.action, tc.p)
		mustContain(t, err, "batch cap")
		if IsHardModuleError(err) {
			t.Fatalf("%s: the cap is SOFT: %v", tc.action, err)
		}
	}
	if got := f.balance(t, f.alice, 65); got.Sign() != 0 {
		t.Fatalf("a refused batch minted id 65: %s", got)
	}
	if got := f.balance(t, f.alice, 1); got.Cmp(big.NewInt(5)) != 0 {
		t.Fatalf("a refused batch moved id 1: %s", got)
	}
	// Reads.
	_, err := DispatchModuleReadCall(f.sdb, ReadContext{}, moduleData(t, "qrc1155", "balance_of_batch", map[string]any{
		"collection": f.collection.String(), "owners": list(65, f.alice.String()), "ids": ids(65),
	}))
	mustContain(t, err, "batch cap")
	_, err = DispatchModuleReadCall(f.sdb, ReadContext{}, moduleData(t, "qrc1155", "balance_of_batch", map[string]any{
		"collection": f.collection.String(), "owners": list(2, f.alice.String()), "ids": ids(3),
	}))
	mustContain(t, err, "length mismatch")
	// Token level, bypassing the router's list decoder.
	big65 := make([]*big.Int, 65)
	for i := range big65 {
		big65[i] = big.NewInt(int64(i + 1))
	}
	if err := f.tok.MintBatch(f.owner, f.alice, big65, big65, nil); err == nil || !strings.Contains(err.Error(), "batch cap") {
		t.Fatalf("token-level MintBatch of 65: %v", err)
	}
	if err := f.tok.BurnBatch(f.alice, f.alice, big65, big65); err == nil || !strings.Contains(err.Error(), "batch cap") {
		t.Fatalf("token-level BurnBatch of 65: %v", err)
	}
	if err := f.tok.SafeBatchTransferFrom(f.alice, f.alice, f.bob, big65, big65); err == nil || !strings.Contains(err.Error(), "batch cap") {
		t.Fatalf("token-level SafeBatchTransferFrom of 65: %v", err)
	}
	if err := f.tok.MintBatch(f.owner, f.alice, big65[:2], big65[:3], nil); err == nil || !strings.Contains(err.Error(), "length mismatch") {
		t.Fatalf("token-level MintBatch mismatch: %v", err)
	}
}

// An id of 2^256 has no 32-byte slot. The router refuses it in the strict
// decoder; a direct Go caller gets an error, never a panic from padTo32.
func TestQRC1155IDOverflowIsAnErrorNotAPanic(t *testing.T) {
	f := newMTFixture(t)
	twoPow256 := new(big.Int).Lsh(big.NewInt(1), 256)

	_, err := f.call(t, f.owner, "mint", f.act(qrc1155Action{To: f.alice.String(), ID: twoPow256.String(), Amount: "1"}))
	mustContain(t, err, "does not fit in 256 bits")
	_, err = DispatchModuleReadCall(f.sdb, ReadContext{}, moduleData(t, "qrc1155", "balance_of", map[string]string{
		"collection": f.collection.String(), "owner": f.alice.String(), "id": twoPow256.String(),
	}))
	mustContain(t, err, "does not fit in 256 bits")

	for name, fn := range map[string]func() error{
		"Mint":             func() error { return f.tok.Mint(f.owner, f.alice, twoPow256, big.NewInt(1), "") },
		"Burn":             func() error { return f.tok.Burn(f.alice, f.alice, twoPow256, big.NewInt(1)) },
		"SafeTransferFrom": func() error { return f.tok.SafeTransferFrom(f.alice, f.alice, f.bob, twoPow256, big.NewInt(1)) },
		"BalanceOf":        func() error { _, err := f.tok.BalanceOf(f.alice, twoPow256); return err },
		"TotalSupply":      func() error { _, err := f.tok.TotalSupply(twoPow256); return err },
		"URI":              func() error { _, err := f.tok.URI(twoPow256); return err },
		"negative":         func() error { return f.tok.Mint(f.owner, f.alice, big.NewInt(-1), big.NewInt(1), "") },
		"nil":              func() error { return f.tok.Mint(f.owner, f.alice, nil, big.NewInt(1), "") },
	} {
		if err := fn(); err == nil || !strings.Contains(err.Error(), "token id") {
			t.Fatalf("%s: want a token id error, got %v", name, err)
		}
	}
	// 2^256-1 is the largest valid id and round-trips.
	maxID := new(big.Int).Sub(twoPow256, big.NewInt(1))
	if _, err := f.call(t, f.owner, "mint", f.act(qrc1155Action{To: f.alice.String(), ID: maxID.String(), Amount: "3"})); err != nil {
		t.Fatalf("mint at max id: %v", err)
	}
	if got, _ := f.tok.BalanceOf(f.alice, maxID); got.Cmp(big.NewInt(3)) != 0 {
		t.Fatalf("balance at max id = %s", got)
	}
}

// moduleOwned initialises a collection whose owner is its own address —
// the shape a native module's position collection has — and marks it as a
// contract the way a module deploy would.
func moduleOwned(t *testing.T, f *mtFixture, at qcrypto.Address) *QRC1155Token {
	t.Helper()
	if err := initQRC1155At(f.sdb, at, at, "Module Positions", ""); err != nil {
		t.Fatalf("init module-owned collection: %v", err)
	}
	markContract(f.sdb, at)
	return NewQRC1155Token(f.sdb, at)
}

// No key derives to a module's address, so no caller can satisfy the owner
// check: router mint / mint_batch / transfer_ownership on a module-owned
// collection are unsatisfiable, while the module itself mints in-package.
func TestModuleOwnedCollectionCannotBeMintedViaRouter(t *testing.T) {
	f := newMTFixture(t)
	module := addr(0xee)
	tok := moduleOwned(t, f, module)
	if managed, _ := tok.IsModuleManaged(); !managed {
		t.Fatal("a self-owned collection must report module-managed")
	}
	if info := f.read(t, "token_info", map[string]string{"collection": module.String()}); info["module_managed"] != true || info["owner"] != module.String() {
		t.Fatalf("token_info = %v", info)
	}

	for _, caller := range []qcrypto.Address{f.owner, f.alice, ModuleRouterAddress, qcrypto.Address{}} {
		for _, tc := range []struct {
			action string
			p      qrc1155Action
		}{
			{"mint", qrc1155Action{Collection: module.String(), To: f.alice.String(), ID: "1", Amount: "1"}},
			{"mint_batch", qrc1155Action{Collection: module.String(), To: f.alice.String(), IDs: []string{"1"}, Amounts: []string{"1"}}},
			{"transfer_ownership", qrc1155Action{Collection: module.String(), NewOwner: f.alice.String()}},
		} {
			_, err := f.call(t, caller, tc.action, tc.p)
			mustContain(t, err, "not owner")
			if IsHardModuleError(err) {
				t.Fatalf("%s: SOFT expected: %v", tc.action, err)
			}
		}
	}
	if bal, _ := tok.BalanceOf(f.alice, big.NewInt(1)); bal.Sign() != 0 {
		t.Fatalf("router minted on a module-owned collection: %s", bal)
	}
	if owner, _ := tok.Owner(); owner != module {
		t.Fatalf("owner moved to %s", owner)
	}

	// The module mints in-package; the event is from zero, operator = module.
	rc := &types.Receipt{}
	if err := tok.mintUnchecked(module, f.alice, big.NewInt(1), big.NewInt(50), "", rc); err != nil {
		t.Fatalf("mintUnchecked: %v", err)
	}
	wantTopics(t, rc, "TransferSingle")
	wantLog(t, rc.Logs[0], TopicTransferSingle, []qcrypto.Address{module, {}, f.alice}, append(word(1), word(50)...))
	if rc.Logs[0].Address != module {
		t.Fatalf("log address = %s, want the module", rc.Logs[0].Address)
	}
	if bal, _ := tok.BalanceOf(f.alice, big.NewInt(1)); bal.Cmp(big.NewInt(50)) != 0 {
		t.Fatalf("balance after mintUnchecked = %s", bal)
	}
}

// A holder of a module-managed position cannot burn or move it through the
// router — even via an approved operator — so one leg of a complete set can
// never be destroyed behind the module's back. Only burnUnchecked moves it.
func TestModuleOwnedCollectionRejectsRouterBurn(t *testing.T) {
	f := newMTFixture(t)
	module := addr(0xee)
	tok := moduleOwned(t, f, module)
	yes, no := big.NewInt(256), big.NewInt(257)
	for _, id := range []*big.Int{yes, no} {
		if err := tok.mintUnchecked(module, f.alice, id, big.NewInt(40), "", nil); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	// Approvals themselves are allowed (harmless) so an operator exists.
	if _, err := f.call(t, f.alice, "set_approval_for_all", qrc1155Action{Collection: module.String(), Operator: f.bob.String(), Approved: true}); err != nil {
		t.Fatalf("set_approval_for_all on a module-owned collection: %v", err)
	}

	one := []string{"256"}
	for _, tc := range []struct {
		caller qcrypto.Address
		action string
		p      qrc1155Action
	}{
		{f.alice, "burn", qrc1155Action{Collection: module.String(), ID: "256", Amount: "1"}},
		{f.alice, "burn_batch", qrc1155Action{Collection: module.String(), IDs: one, Amounts: []string{"1"}}},
		{f.alice, "transfer", qrc1155Action{Collection: module.String(), To: f.bob.String(), ID: "256", Amount: "1"}},
		{f.alice, "batch_transfer", qrc1155Action{Collection: module.String(), To: f.bob.String(), IDs: one, Amounts: []string{"1"}}},
		{f.bob, "burn", qrc1155Action{Collection: module.String(), From: f.alice.String(), ID: "256", Amount: "1"}},
		{f.bob, "transfer", qrc1155Action{Collection: module.String(), From: f.alice.String(), To: f.bob.String(), ID: "256", Amount: "1"}},
	} {
		rc, err := f.call(t, tc.caller, tc.action, tc.p)
		mustContain(t, err, "managed by a native module")
		if IsHardModuleError(err) {
			t.Fatalf("%s: SOFT expected: %v", tc.action, err)
		}
		if len(rc.Logs) != 0 {
			t.Fatalf("%s: a refused action emitted logs", tc.action)
		}
	}
	for _, id := range []*big.Int{yes, no} {
		if bal, _ := tok.BalanceOf(f.alice, id); bal.Cmp(big.NewInt(40)) != 0 {
			t.Fatalf("balance of %s = %s, want 40", id, bal)
		}
		if supply, _ := tok.TotalSupply(id); supply.Cmp(big.NewInt(40)) != 0 {
			t.Fatalf("supply of %s = %s, want 40", id, supply)
		}
	}

	// The module burns a complete set in-package: TransferSingle to zero.
	rc := &types.Receipt{}
	for _, id := range []*big.Int{yes, no} {
		if err := tok.burnUnchecked(module, f.alice, id, big.NewInt(15), rc); err != nil {
			t.Fatalf("burnUnchecked: %v", err)
		}
	}
	wantTopics(t, rc, "TransferSingle", "TransferSingle")
	wantLog(t, rc.Logs[1], TopicTransferSingle, []qcrypto.Address{module, f.alice, {}}, append(word(257), word(15)...))
	if bal, _ := tok.BalanceOf(f.alice, yes); bal.Cmp(big.NewInt(25)) != 0 {
		t.Fatalf("balance after burnUnchecked = %s", bal)
	}
	if err := tok.burnUnchecked(module, f.alice, yes, big.NewInt(26), nil); err == nil || !strings.Contains(err.Error(), "insufficient") {
		t.Fatalf("burnUnchecked over balance: %v", err)
	}
	// A user collection burns normally through the router, for contrast.
	if _, err := f.call(t, f.owner, "mint", f.act(qrc1155Action{To: f.alice.String(), ID: "1", Amount: "2"})); err != nil {
		t.Fatalf("mint: %v", err)
	}
	if _, err := f.call(t, f.alice, "burn", f.act(qrc1155Action{ID: "1", Amount: "1"})); err != nil {
		t.Fatalf("burn on a user collection: %v", err)
	}
}

// No transfer may name the collection itself as recipient: a balance held by
// the collection address can never move again, and on a module-managed
// collection it would break the module's accounting.
func TestTransferToCollectionRejected(t *testing.T) {
	f := newMTFixture(t)
	if _, err := f.call(t, f.owner, "mint", f.act(qrc1155Action{To: f.alice.String(), ID: "1", Amount: "10"})); err != nil {
		t.Fatalf("mint: %v", err)
	}
	for _, tc := range []struct {
		action string
		p      qrc1155Action
	}{
		{"transfer", f.act(qrc1155Action{To: f.collection.String(), ID: "1", Amount: "1"})},
		{"batch_transfer", f.act(qrc1155Action{To: f.collection.String(), IDs: []string{"1"}, Amounts: []string{"1"}})},
	} {
		_, err := f.call(t, f.alice, tc.action, tc.p)
		mustContain(t, err, "cannot transfer to the collection address")
	}
	if err := f.tok.SafeTransferFrom(f.alice, f.alice, f.collection, big.NewInt(1), big.NewInt(1)); err == nil {
		t.Fatal("token-level transfer to the collection must be refused")
	}
	if err := f.tok.SafeBatchTransferFrom(f.alice, f.alice, f.collection, []*big.Int{big.NewInt(1)}, []*big.Int{big.NewInt(1)}); err == nil {
		t.Fatal("token-level batch transfer to the collection must be refused")
	}
	// Minting TO the collection is the owner's business and stays possible
	// only for a user collection; it is not a transfer.
	if got := f.balance(t, f.alice, 1); got.Cmp(big.NewInt(10)) != 0 {
		t.Fatalf("a refused transfer moved tokens: %s", got)
	}
	if got := f.balance(t, f.collection, 1); got.Sign() != 0 {
		t.Fatalf("the collection holds %s of its own token", got)
	}
}

// transfer / batch_transfer / burn / burn_batch / set_approval_for_all
// through the router, with `from` defaulting to the caller, operator
// approval, the events, and the reads that mirror them.
func TestQRC1155TransferBurnApprovalViaRouter(t *testing.T) {
	f := newMTFixture(t)
	if _, err := f.call(t, f.owner, "mint_batch", f.act(qrc1155Action{To: f.alice.String(), IDs: []string{"1", "2"}, Amounts: []string{"100", "200"}})); err != nil {
		t.Fatalf("mint_batch: %v", err)
	}

	// Self transfer: from defaults to the caller.
	rc, err := f.call(t, f.alice, "transfer", f.act(qrc1155Action{To: f.bob.String(), ID: "1", Amount: "30"}))
	if err != nil {
		t.Fatalf("transfer: %v", err)
	}
	wantTopics(t, rc, "TransferSingle")
	wantLog(t, rc.Logs[0], TopicTransferSingle, []qcrypto.Address{f.alice, f.alice, f.bob}, append(word(1), word(30)...))
	if f.balance(t, f.alice, 1).Cmp(big.NewInt(70)) != 0 || f.balance(t, f.bob, 1).Cmp(big.NewInt(30)) != 0 {
		t.Fatal("transfer did not move 30")
	}
	_, err = f.call(t, f.alice, "transfer", f.act(qrc1155Action{To: f.bob.String(), ID: "1", Amount: "71"}))
	mustContain(t, err, "insufficient balance")

	// Bob is not approved to move alice's tokens.
	_, err = f.call(t, f.bob, "transfer", f.act(qrc1155Action{From: f.alice.String(), To: f.bob.String(), ID: "1", Amount: "1"}))
	mustContain(t, err, "not approved")
	_, err = f.call(t, f.bob, "burn", f.act(qrc1155Action{From: f.alice.String(), ID: "1", Amount: "1"}))
	mustContain(t, err, "not approved")

	rc, err = f.call(t, f.alice, "set_approval_for_all", f.act(qrc1155Action{Operator: f.bob.String(), Approved: true}))
	if err != nil {
		t.Fatalf("set_approval_for_all: %v", err)
	}
	wantTopics(t, rc, "ApprovalForAll")
	if got := f.read(t, "is_approved_for_all", map[string]string{"collection": f.collection.String(), "owner": f.alice.String(), "operator": f.bob.String()}); got["approved"] != true {
		t.Fatalf("is_approved_for_all = %v", got)
	}
	_, err = f.call(t, f.alice, "set_approval_for_all", f.act(qrc1155Action{Operator: f.alice.String(), Approved: true}))
	mustContain(t, err, "approve self")

	// Operator moves and burns on alice's behalf; the operator is topic[1].
	rc, err = f.call(t, f.bob, "batch_transfer", f.act(qrc1155Action{From: f.alice.String(), To: f.bob.String(), IDs: []string{"1", "2"}, Amounts: []string{"20", "50"}}))
	if err != nil {
		t.Fatalf("operator batch_transfer: %v", err)
	}
	wantTopics(t, rc, "TransferBatch")
	data := word(2)
	for _, v := range []int64{1, 2, 20, 50} {
		data = append(data, word(v)...)
	}
	wantLog(t, rc.Logs[0], TopicTransferBatch, []qcrypto.Address{f.bob, f.alice, f.bob}, data)

	rc, err = f.call(t, f.bob, "burn", f.act(qrc1155Action{From: f.alice.String(), ID: "2", Amount: "10"}))
	if err != nil {
		t.Fatalf("operator burn: %v", err)
	}
	wantTopics(t, rc, "TransferSingle")
	wantLog(t, rc.Logs[0], TopicTransferSingle, []qcrypto.Address{f.bob, f.alice, {}}, append(word(2), word(10)...))

	rc, err = f.call(t, f.alice, "burn_batch", f.act(qrc1155Action{IDs: []string{"1", "2"}, Amounts: []string{"5", "5"}}))
	if err != nil {
		t.Fatalf("burn_batch: %v", err)
	}
	wantTopics(t, rc, "TransferBatch")
	data = word(2)
	for _, v := range []int64{1, 2, 5, 5} {
		data = append(data, word(v)...)
	}
	wantLog(t, rc.Logs[0], TopicTransferBatch, []qcrypto.Address{f.alice, f.alice, {}}, data)

	// Final balances and supplies through the reads.
	// alice id1: 100-30-20-5 = 45; id2: 200-50-10-5 = 135. bob id1: 50; id2: 50.
	bals := f.read(t, "balance_of_batch", map[string]any{
		"collection": f.collection.String(),
		"owners":     []string{f.alice.String(), f.alice.String(), f.bob.String(), f.bob.String()},
		"ids":        []string{"1", "2", "1", "2"},
	})["balances"].([]string)
	if strings.Join(bals, ",") != "45,135,50,50" {
		t.Fatalf("balances = %v", bals)
	}
	if got := f.read(t, "balance_of", map[string]string{"collection": f.collection.String(), "owner": f.alice.String(), "id": "1"}); got["balance"] != "45" {
		t.Fatalf("balance_of = %v", got)
	}
	if got := f.read(t, "total_supply", map[string]string{"collection": f.collection.String(), "id": "2"}); got["total_supply"] != "185" {
		t.Fatalf("total_supply = %v", got)
	}

	// Revoke: bob loses the right; a burn over the balance is refused whole.
	if _, err := f.call(t, f.alice, "set_approval_for_all", f.act(qrc1155Action{Operator: f.bob.String(), Approved: false})); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	_, err = f.call(t, f.bob, "burn", f.act(qrc1155Action{From: f.alice.String(), ID: "1", Amount: "1"}))
	mustContain(t, err, "not approved")
	_, err = f.call(t, f.alice, "burn_batch", f.act(qrc1155Action{IDs: []string{"1", "2"}, Amounts: []string{"1", "1000"}}))
	mustContain(t, err, "burnBatch[1]")

	// Writes are refused on /call and reads inside a TxCall; unknown reads too.
	if _, err := DispatchModuleReadCall(f.sdb, ReadContext{}, moduleData(t, "qrc1155", "mint", f.act(qrc1155Action{}))); err == nil || !strings.Contains(err.Error(), "state-changing") {
		t.Fatalf("write on /call: %v", err)
	}
	if _, err := DispatchModuleReadCall(f.sdb, ReadContext{}, moduleData(t, "qrc1155", "nope", f.act(qrc1155Action{}))); err == nil || !strings.Contains(err.Error(), "unknown read action") {
		t.Fatalf("unknown read: %v", err)
	}
	err = DispatchModuleCall(f.sdb, mtCtx(f.alice, 99), moduleData(t, "qrc1155", "balance_of", f.act(qrc1155Action{})), &types.Receipt{})
	if !IsHardModuleError(err) {
		t.Fatalf("a read inside a TxCall must be HARD: %v", err)
	}
}
