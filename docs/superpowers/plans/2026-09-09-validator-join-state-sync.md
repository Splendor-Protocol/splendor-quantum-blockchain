# Validator join + state sync — implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A fresh `sqc-node` joins a running producer without the producer stopping, reaches the tip, and can later register and vote under BFT; chains whose history is unreplayable (mainnet shape) join through a live state snapshot.

**Architecture:** Follows `docs/superpowers/specs/2026-09-09-validator-join-state-sync-design.md` §3 in five parts. 3.1 makes the producer choose BFT for the block it will produce and fail closed on the single path; 3.2 authenticates blocks against the pinned genesis key *or* the active validator set; 3.3 lets validators pull-sync and hold proposals until synced; 3.4 adds a sealed `get_state_snapshot` read plus `--state-sync`; 3.5 is the test list. Each part is one commit.

**Tech Stack:** Go 1.25, goleveldb (`GetSnapshot`), ML-DSA-65 via `core/crypto`, gorilla/mux, zerolog. Tests are plain `testing` with `t.TempDir()` LevelDBs and `httptest` peers.

**Deviations from the spec, decided up front (each is explained in the report):**

1. `Executor.ImportStateSnapshot` does **not** run `verifyProducerAndQC` after the import. The registry after block H is not the set that certified H (a register or unregister inside H changes it), so the check is unsound in both directions. The head is authenticated by the pinned key in the fetcher and the state by the header root.
2. `--state-sync` refuses `--validator`. A validator on an empty chain would mint block 1 (single path at head 0) before the import lands; the spec's own sequence starts the joiner as a follower and restarts it as a validator on a non-empty chain.
3. The `RefreshSet` fallback takes the genesis validator from block 0 when stored, else from the trusted (pinned) key. A joined node has no block 0 (pull-sync starts at height 1).
4. Two small liveness fixes the join needs to survive: the BFT lock is scoped to its height (a failed commit at h no longer rejects every proposal at h+1), and the validator's commit callback treats "already the head" as committed (the pull-sync loop of 3.3 races the BFT commit for the same block).
5. Proposals received over P2P are authenticated with the same rule as pushed blocks (pinned key or active set) before they enter the engine. Today nothing verifies a proposal's signature before the vote.
6. Wire type placement: `types.StateLeaf` / `types.StateSnapshot` live in `core/types` (imported by network, rpc and block alike) instead of `state.KV` + `block.StateSnapshot`.

---

## File map

| File | Change |
|---|---|
| `core/block/executor.go` | `ErrQuorumRequired` guard in `assembleBlock`; `ActiveValidatorKey`; `ExportStateSnapshot`; `ImportStateSnapshot` |
| `core/block/validator_join_test.go` | 3.1 / 3.2 executor tests |
| `core/block/state_snapshot_test.go` | 3.4 executor tests |
| `consensus/validator.go` | `Config.GenesisValidator`, `Config.Ready` |
| `consensus/multivalidator.go` | fallback to genesis validator; skip propose while not ready |
| `consensus/bft.go` | height-scoped lock |
| `consensus/join_test.go` | consensus tests |
| `network/p2p.go` | `VerifyBlockProducer`, `HandleIncomingBlock(body, trustedPK, lookup, apply)` |
| `network/sync.go` | `SyncConfig.LookupProducerKey`, trusted-key accessor, `FetchStateSnapshot`, `Syncer.StateSync` |
| `network/p2p_secure.go` | `KindGetStateSnapshot`, `QuerySealedTimeout` |
| `network/join_test.go`, `network/p2p_security_test.go` | network tests |
| `rpc/p2p_routes.go` | `ServeStateSnapshot` |
| `rpc/server.go` | `Config.TrustedProducerKey`, `Config.LookupProducerKey` for plaintext ingest |
| `rpc/p2p_sealed_sync_test.go` | snapshot read test |
| `core/types/state_snapshot.go` | wire types |
| `core/state/statedb.go` | `ExportSnapshot`, `ImportSnapshot`, `SnapshotRoot` |
| `core/state/export_snapshot_test.go` | state tests |
| `cmd/node/main.go` | `bftForNextBlock`, trusted key, resolver wiring, validator pull-sync + Ready, `--state-sync` |
| `cmd/node/join.go`, `cmd/node/join_test.go` | pure helpers + tests |

---

### Task 1: core/block — fail-closed single path, active-set key resolver, QC tests

**Files:**
- Modify: `core/block/executor.go`
- Test: `core/block/validator_join_test.go`

- [ ] **Step 1: Write the failing tests**

```go
package block

import (
	"errors"
	"math/big"
	"strings"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/forks"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
	"github.com/splendor-protocol/splendor-quantum-blockchain/vm"
)

// registerValidator funds addr with the bond and registers it as Active in
// committed state, exactly as a validators.register tx would leave it.
func registerValidator(t *testing.T, sdb *state.StateDB, pub *qcrypto.PublicKey) qcrypto.Address {
	t.Helper()
	addr := qcrypto.DeriveAddress(pub)
	acc := types.NewAccount(addr)
	acc.AddBalance(new(big.Int).Add(vm.ValidatorBond(), big.NewInt(1_000_000)))
	sdb.SetAccount(acc)
	reg := vm.NewStakingContract(sdb, vm.DeployStaking(nil))
	if err := reg.Register(addr, pub.Bytes(), 0); err != nil {
		t.Fatalf("register %s: %v", addr, err)
	}
	if _, err := sdb.Commit(); err != nil {
		t.Fatalf("commit registry: %v", err)
	}
	return addr
}

func TestSinglePathRefusesWhenQuorumRequired(t *testing.T) {
	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()
	ex.SetForks(forks.Schedule{forks.MultiValidator: 1})
	v1, priv1, _ := qcrypto.GenerateKeypair()
	v2, _, _ := qcrypto.GenerateKeypair()
	addr1 := registerValidator(t, sdb, v1)
	registerValidator(t, sdb, v2)
	rootBefore, _ := sdb.StateRoot()

	_, err := ex.ProduceBlock(nil, addr1, v1.Bytes(), priv1, [32]byte{}, 1)
	if !errors.Is(err, ErrQuorumRequired) {
		t.Fatalf("ProduceBlock with 2 active under multi_validator = %v, want ErrQuorumRequired", err)
	}
	if !strings.Contains(err.Error(), "BFT") {
		t.Fatalf("error should tell the operator to run BFT: %v", err)
	}
	if h, _ := ex.LatestHeight(); h != 0 {
		t.Fatalf("head advanced to %d on a refused block", h)
	}
	if root, _ := sdb.StateRoot(); root != rootBefore {
		t.Fatal("state changed on a refused block")
	}
}

func TestSinglePathStillProducesWithFewerThanTwoActive(t *testing.T) {
	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()
	ex.SetForks(forks.Schedule{forks.MultiValidator: 1})
	v1, priv1, _ := qcrypto.GenerateKeypair()
	addr1 := registerValidator(t, sdb, v1)
	if _, err := ex.ProduceBlock(nil, addr1, v1.Bytes(), priv1, [32]byte{}, 1); err != nil {
		t.Fatalf("one active validator must still produce on the single path: %v", err)
	}
}

// The hole a fresh node hits on a mainnet-shaped chain.
func TestExecuteBlockRejectsQCLessBlockWithTwoActive(t *testing.T) {
	v1, priv1, _ := qcrypto.GenerateKeypair()
	v2, _, _ := qcrypto.GenerateKeypair()

	producer, sdbA := newTestExecutor(t) // no schedule: mints a QC-less block
	defer producer.Close()
	defer sdbA.Close()
	addr1 := registerValidator(t, sdbA, v1)
	registerValidator(t, sdbA, v2)
	b, err := producer.ProduceBlock(nil, addr1, v1.Bytes(), priv1, [32]byte{}, 1)
	if err != nil {
		t.Fatalf("produce: %v", err)
	}

	follower, sdbB := newTestExecutor(t)
	defer follower.Close()
	defer sdbB.Close()
	registerValidator(t, sdbB, v1)
	registerValidator(t, sdbB, v2)
	follower.SetForks(forks.Schedule{forks.MultiValidator: 1})
	err = follower.ExecuteBlock(b)
	if err == nil || !strings.Contains(err.Error(), "quorum certificate") {
		t.Fatalf("QC-less block with 2 active accepted: %v", err)
	}
}

// signVote produces the QC vote a BFT engine attaches on commit.
func signVote(t *testing.T, b *types.Block, addr qcrypto.Address, priv *qcrypto.PrivateKey) types.QCVote {
	t.Helper()
	v := types.QCVote{AlgID: qcrypto.AlgMLDSA65, Height: b.Header.Height, BlockHash: b.Hash, ValidatorAddr: addr, Timestamp: 1}
	h, err := v.Hash()
	if err != nil {
		t.Fatal(err)
	}
	sig, err := qcrypto.Sign(priv, h[:], qcrypto.CtxVote)
	if err != nil {
		t.Fatal(err)
	}
	v.Signature = sig
	return v
}

func TestExecuteBlockAcceptsOneVoteQCFromSoleValidator(t *testing.T) {
	v1, priv1, _ := qcrypto.GenerateKeypair()

	producer, sdbA := newTestExecutor(t)
	defer producer.Close()
	defer sdbA.Close()
	producer.SetForks(forks.Schedule{forks.MultiValidator: 1})
	addr1 := registerValidator(t, sdbA, v1)
	b, err := producer.BuildProposal(nil, addr1, v1.Bytes(), priv1, [32]byte{}, 1)
	if err != nil {
		t.Fatalf("proposal: %v", err)
	}
	b.Votes = []types.QCVote{signVote(t, b, addr1, priv1)}
	root := types.ComputeQCRoot(b.Votes)
	b.Header.QCRoot = root[:]

	follower, sdbB := newTestExecutor(t)
	defer follower.Close()
	defer sdbB.Close()
	follower.SetForks(forks.Schedule{forks.MultiValidator: 1})
	registerValidator(t, sdbB, v1)
	if err := follower.ExecuteBlock(b); err != nil {
		t.Fatalf("one-vote block from the sole validator rejected: %v", err)
	}
}

func TestActiveValidatorKeyResolvesOnlyActiveMembers(t *testing.T) {
	ex, sdb := newTestExecutor(t)
	defer ex.Close()
	defer sdb.Close()
	v1, _, _ := qcrypto.GenerateKeypair()
	addr1 := registerValidator(t, sdb, v1)
	if got := ex.ActiveValidatorKey(addr1); string(got) != string(v1.Bytes()) {
		t.Fatal("active validator key not resolved")
	}
	stranger, _, _ := qcrypto.GenerateKeypair()
	if got := ex.ActiveValidatorKey(qcrypto.DeriveAddress(stranger)); got != nil {
		t.Fatal("unregistered address resolved to a key")
	}
	reg := vm.NewStakingContract(sdb, vm.DeployStaking(nil))
	if err := reg.Unregister(addr1, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := sdb.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := ex.ActiveValidatorKey(addr1); got != nil {
		t.Fatal("an exiting validator resolved to a key")
	}
}
```

- [ ] **Step 2: Run to verify they fail** — `go test ./core/block -run 'SinglePath|QCLess|OneVote|ActiveValidatorKey'` → compile error (`ErrQuorumRequired`, `ActiveValidatorKey` undefined).

- [ ] **Step 3: Implement** in `core/block/executor.go`:

```go
// ErrQuorumRequired is returned by ProduceBlock when multi_validator is active
// at the height being produced and the registry lists two or more active
// validators: from that height on every block needs a quorum certificate,
// which the single-path producer never attaches. Halting here is the
// mainnet-5786 failure made visible instead of a fork.
var ErrQuorumRequired = errors.New("multi_validator: quorum certificate required")

// in assembleBlock, right after the lock:
if persist {
    if err := e.refuseSinglePathIfQuorumRequired(height); err != nil {
        return nil, err
    }
}

func (e *Executor) refuseSinglePathIfQuorumRequired(height uint64) error {
	if !e.schedule().Active(forks.MultiValidator, height) {
		return nil
	}
	active, err := vm.NewStakingContract(e.state, vm.DeployStaking(nil)).ListActive()
	if err != nil {
		return fmt.Errorf("validator set: %w", err)
	}
	if len(active) < 2 {
		return nil
	}
	return fmt.Errorf("%w: %d active validators at height %d, this node must run in BFT mode (restart it)", ErrQuorumRequired, len(active), height)
}

// ActiveValidatorKey returns the ML-DSA-65 public key of addr when the committed
// validator registry lists it as Active, nil otherwise. It is the resolver the
// network layer uses to authenticate a block sealed by a validator other than
// the pinned genesis key; ExecuteBlock remains the authority.
func (e *Executor) ActiveValidatorKey(addr qcrypto.Address) []byte {
	rec, err := vm.NewStakingContract(e.state, vm.DeployStaking(nil)).GetValidator(addr)
	if err != nil || rec == nil || rec.Status != vm.ValidatorActive || len(rec.PublicKey) == 0 {
		return nil
	}
	return append([]byte(nil), rec.PublicKey...)
}
```

- [ ] **Step 4: Run tests** — `go test -count=1 ./core/block` → PASS.
- [ ] **Step 5: Commit** — `git commit -m "block: fail closed on the single path once a quorum is required; resolve active-set keys"`.

---

### Task 2: consensus — genesis-validator fallback, Ready gate, height-scoped lock

**Files:**
- Modify: `consensus/validator.go` (Config), `consensus/multivalidator.go`, `consensus/bft.go`
- Test: `consensus/join_test.go`

- [ ] **Step 1: Write the failing tests**

```go
package consensus

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/block"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/mempool"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/state"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

func newTestChain(t *testing.T) (*block.Executor, *state.StateDB, *mempool.Mempool) {
	t.Helper()
	sdb, err := state.NewStateDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ex, err := block.NewExecutor(sdb, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ex.Close(); sdb.Close() })
	return ex, sdb, mempool.New(100, sdb)
}

func TestRefreshSetFallsBackToGenesisValidator(t *testing.T) {
	ex, sdb, pool := newTestChain(t)
	localPriv, localPK, localAddr := testKey(t)
	_, genPK, genAddr := testKey(t)
	eng, _ := NewBFTEngine(BFTConfig{LocalAddr: localAddr, LocalPubKey: localPK, LocalPrivKey: localPriv})
	mv := NewMultiValidator(Config{
		Address: localAddr, PublicKey: localPK, PrivateKey: localPriv,
		GenesisValidator: ValidatorInfo{Address: genAddr, PublicKey: genPK, IsActive: true},
	}, eng, ex, pool, nil, sdb)
	mv.RefreshSet()
	leader, ok := eng.LeaderAddress(1, 0)
	if !ok || leader != genAddr {
		t.Fatalf("empty registry: leader = %s ok=%v, want the genesis validator %s", leader, ok, genAddr)
	}
	if eng.IsLeader(1, 0) {
		t.Fatal("an unregistered joiner must not believe it is the sole leader")
	}
}

func TestRefreshSetUsesLocalWhenNoGenesisValidatorKnown(t *testing.T) {
	ex, sdb, pool := newTestChain(t)
	localPriv, localPK, localAddr := testKey(t)
	eng, _ := NewBFTEngine(BFTConfig{LocalAddr: localAddr, LocalPubKey: localPK, LocalPrivKey: localPriv})
	mv := NewMultiValidator(Config{Address: localAddr, PublicKey: localPK, PrivateKey: localPriv}, eng, ex, pool, nil, sdb)
	mv.RefreshSet()
	if !eng.IsLeader(1, 0) {
		t.Fatal("with no genesis validator configured the pre-existing local fallback must hold")
	}
}

func TestMultiValidatorDoesNotProposeUntilReady(t *testing.T) {
	ex, sdb, pool := newTestChain(t)
	localPriv, localPK, localAddr := testKey(t)
	var ready atomic.Bool
	eng, _ := NewBFTEngine(BFTConfig{
		LocalAddr: localAddr, LocalPubKey: localPK, LocalPrivKey: localPriv,
		RoundTimeout: time.Hour,
		OnCommit:     func(b *types.Block) error { return ex.ExecuteBlock(b) },
	})
	mv := NewMultiValidator(Config{
		BlockInterval: 20 * time.Millisecond, Address: localAddr, PublicKey: localPK, PrivateKey: localPriv,
		Ready: ready.Load,
	}, eng, ex, pool, nil, sdb)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for b := range eng.CommitCh() {
			mv.NotifyCommit(b)
		}
	}()
	go func() { _ = mv.Run(ctx) }()

	time.Sleep(150 * time.Millisecond)
	if h, _ := ex.LatestHeight(); h != 0 {
		t.Fatalf("proposed %d blocks while not ready", h)
	}
	ready.Store(true)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if h, _ := ex.LatestHeight(); h >= 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no block produced after becoming ready")
}

// A commit that fails at height h must not wedge the engine: the next height's
// proposal is a different block and must still be voted on.
func TestFailedCommitDoesNotLockOutNextHeight(t *testing.T) {
	set, keys := testSet(t, 2)
	eng, _ := NewBFTEngine(BFTConfig{
		LocalAddr: set[0].Address, LocalPubKey: set[0].PublicKey, LocalPrivKey: keys[0],
		RoundTimeout: time.Hour,
		OnCommit:     func(*types.Block) error { return errNotApplied },
	})
	eng.UpdateValidatorSet(set)
	eng.SetHeight(1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = eng.Run(ctx) }()

	mkBlock := func(h uint64) *types.Block {
		leader, _ := eng.LeaderAddress(h, 0)
		b := &types.Block{Header: types.BlockHeader{Height: h, ValidatorAddr: leader}}
		hash, _ := b.ComputeVotedHash()
		b.Hash = hash
		return b
	}
	b1 := mkBlock(1)
	eng.ProposeBlock(b1)
	time.Sleep(50 * time.Millisecond)
	for i := range set {
		v := &Vote{Height: 1, Round: 0, BlockHash: b1.Hash, ValidatorAddr: set[i].Address, Timestamp: 1}
		_ = v.Sign(keys[i])
		eng.SubmitVote(v)
	}
	time.Sleep(50 * time.Millisecond)

	b2 := mkBlock(2)
	eng.ProposeBlock(b2)
	time.Sleep(50 * time.Millisecond)
	eng.stateMu.Lock()
	voting := eng.voteSet != nil && eng.currentHeight == 2
	eng.stateMu.Unlock()
	if !voting {
		t.Fatal("engine refused the next height's proposal after a failed commit")
	}
}

var errNotApplied = errorString("not applied")

type errorString string

func (e errorString) Error() string { return string(e) }
```

- [ ] **Step 2: Run to verify failure** — compile errors on `GenesisValidator`, `Ready`.
- [ ] **Step 3: Implement**

`consensus/validator.go` Config gains:

```go
	// GenesisValidator is the key that sealed block 0. A BFT node whose
	// registry is empty runs with this as the whole validator set, so two nodes
	// on a chain whose genesis validator never registered agree on one leader
	// instead of each electing itself. Zero value keeps the local-node fallback.
	GenesisValidator ValidatorInfo

	// Ready reports whether this node may propose. nil means always. Wired to
	// the syncer, so a validator that is still catching up votes on peers'
	// proposals but never proposes on top of a stale head.
	Ready func() bool
```

`consensus/multivalidator.go`:

```go
	if len(infos) == 0 {
		fallback := m.cfg.GenesisValidator
		if len(fallback.PublicKey) == 0 {
			fallback = ValidatorInfo{Address: m.cfg.Address, PublicKey: m.cfg.PublicKey}
		}
		fallback.IsActive = true
		infos = append(infos, fallback)
	}

func (m *MultiValidator) ready() bool { return m.cfg.Ready == nil || m.cfg.Ready() }
// in the view-change callback and the tick: if !m.ready() { log debug; continue/return }
```

`consensus/bft.go`: add `lockedHeight uint64`; set it beside `lockedHash`; check `e.hasLock && b.Header.Height == e.lockedHeight && b.Hash != e.lockedHash`.

- [ ] **Step 4: Run** — `go test -count=1 ./consensus` → PASS.
- [ ] **Step 5: Commit** — `git commit -m "consensus: genesis-validator fallback, Ready gate before proposing, height-scoped lock"`.

---

### Task 3: network — authenticate blocks against the active set

**Files:**
- Modify: `network/p2p.go`, `network/sync.go`
- Test: `network/join_test.go`; update `network/p2p_security_test.go`

- [ ] **Step 1: Write the failing tests**

```go
package network

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

func sealedBy(t *testing.T, height uint64, pub *qcrypto.PublicKey, priv *qcrypto.PrivateKey) *types.Block {
	t.Helper()
	b := &types.Block{Header: types.BlockHeader{
		Height: height, Timestamp: time.Now().Unix(), ChainID: types.ChainID, AlgID: qcrypto.AlgMLDSA65,
		TxMerkleRoot: qcrypto.Hash32([]byte{}), ValidatorAddr: qcrypto.DeriveAddress(pub),
	}}
	h, _ := b.ComputeHash()
	b.Hash = h
	if err := b.SignBlock(priv); err != nil {
		t.Fatal(err)
	}
	return b
}

func plaintextPeerServing(t *testing.T, b *types.Block) *httptest.Server {
	t.Helper()
	body, _ := json.Marshal(b)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/p2p/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestFetchBlockAcceptsActiveSetProducer(t *testing.T) {
	genesisPub, _, _ := qcrypto.GenerateKeypair()
	otherPub, otherPriv, _ := qcrypto.GenerateKeypair()
	b := sealedBy(t, 9, otherPub, otherPriv)
	srv := plaintextPeerServing(t, b)

	cfg := DefaultSyncConfig(genesisPub.Bytes())
	cfg.AllowPlaintext = true
	cfg.LookupProducerKey = func(addr qcrypto.Address) []byte {
		if addr == qcrypto.DeriveAddress(otherPub) {
			return otherPub.Bytes()
		}
		return nil
	}
	got, err := NewBlockFetcher(cfg).FetchBlock(srv.URL, 9)
	if err != nil {
		t.Fatalf("block from an active-set producer refused: %v", err)
	}
	if got.Header.Height != 9 {
		t.Fatalf("height %d", got.Header.Height)
	}
}

func TestFetchBlockRejectsUnknownProducer(t *testing.T) {
	genesisPub, _, _ := qcrypto.GenerateKeypair()
	otherPub, otherPriv, _ := qcrypto.GenerateKeypair()
	srv := plaintextPeerServing(t, sealedBy(t, 9, otherPub, otherPriv))

	cfg := DefaultSyncConfig(genesisPub.Bytes())
	cfg.AllowPlaintext = true
	cfg.LookupProducerKey = func(qcrypto.Address) []byte { return nil }
	if _, err := NewBlockFetcher(cfg).FetchBlock(srv.URL, 9); err == nil {
		t.Fatal("block from a producer outside the active set accepted")
	}
	// A resolver that hands back a key not bound to the header address is a
	// hostile or buggy registry, and must not authenticate anything.
	cfg.LookupProducerKey = func(qcrypto.Address) []byte { return genesisPub.Bytes() }
	if _, err := NewBlockFetcher(cfg).FetchBlock(srv.URL, 9); err == nil {
		t.Fatal("key not derived from the header address accepted")
	}
	// nil resolver: today's single-key behaviour.
	cfg.LookupProducerKey = nil
	if _, err := NewBlockFetcher(cfg).FetchBlock(srv.URL, 9); err == nil {
		t.Fatal("without a resolver a non-genesis producer must be refused")
	}
}

func TestFetchBlockGenesisOnlyPinnedKey(t *testing.T) {
	genesisPub, _, _ := qcrypto.GenerateKeypair()
	otherPub, otherPriv, _ := qcrypto.GenerateKeypair()
	srv := plaintextPeerServing(t, sealedBy(t, 0, otherPub, otherPriv))
	cfg := DefaultSyncConfig(genesisPub.Bytes())
	cfg.AllowPlaintext = true
	cfg.LookupProducerKey = func(qcrypto.Address) []byte { return otherPub.Bytes() }
	if _, err := NewBlockFetcher(cfg).FetchBlock(srv.URL, 0); err == nil {
		t.Fatal("block 0 must only ever verify against the pinned key")
	}
}

func TestHandleIncomingBlockUsesResolver(t *testing.T) {
	genesisPub, _, _ := qcrypto.GenerateKeypair()
	otherPub, otherPriv, _ := qcrypto.GenerateKeypair()
	body, _ := json.Marshal(sealedBy(t, 4, otherPub, otherPriv))
	applied := false
	apply := func(*types.Block) error { applied = true; return nil }
	lookup := func(addr qcrypto.Address) []byte {
		if addr == qcrypto.DeriveAddress(otherPub) {
			return otherPub.Bytes()
		}
		return nil
	}
	if err := HandleIncomingBlock(body, genesisPub.Bytes(), nil, apply); err == nil || applied {
		t.Fatal("pushed block from a non-genesis producer applied without a resolver")
	}
	if err := HandleIncomingBlock(body, genesisPub.Bytes(), lookup, apply); err != nil || !applied {
		t.Fatalf("pushed block from an active-set producer refused: %v", err)
	}
}
```

Update `TestHandleIncomingBlockMandatorySignature` to pass `nil` as the resolver.

- [ ] **Step 2: Run** — compile errors.
- [ ] **Step 3: Implement** — `network/p2p.go`:

```go
// ProducerKeyLookup resolves the public key of an active validator by address.
type ProducerKeyLookup func(addr qcrypto.Address) []byte

// VerifyBlockProducer authenticates a block's signature against the trusted
// genesis key, or — above height 0 and when a resolver is given — against the
// active-set key of the address the header names. The resolver's key must
// derive to that address. Block 0 verifies against the pinned key only.
func VerifyBlockProducer(b *types.Block, trustedPK []byte, lookup ProducerKeyLookup) error {
	if len(trustedPK) == 0 {
		return fmt.Errorf("no trusted validator pubkey configured")
	}
	if err := b.VerifySignature(trustedPK); err == nil {
		return nil
	} else if b.Header.Height == 0 || lookup == nil {
		return fmt.Errorf("invalid validator signature: %w", err)
	}
	key := lookup(b.Header.ValidatorAddr)
	if len(key) == 0 {
		return fmt.Errorf("block %d: producer %s is neither the genesis validator nor in the active validator set", ...)
	}
	pub, err := qcrypto.PublicKeyFromBytes(key)
	if err != nil { ... }
	if qcrypto.DeriveAddress(pub) != b.Header.ValidatorAddr { ... }
	if err := b.VerifySignature(key); err != nil { ... }
	return nil
}

func HandleIncomingBlock(body []byte, validatorPK []byte, lookup ProducerKeyLookup, applyBlock func(*types.Block) error) error
```

`network/sync.go`: `SyncConfig.LookupProducerKey ProducerKeyLookup`; `BlockFetcher` keeps `trustedPK` behind a `sync.RWMutex` (`TrustedPubKey()`, `setTrustedPubKey()`); `FetchBlock`/`FetchLatestBlock` call `VerifyBlockProducer`; `Syncer.TrustedPubKey()` exposes it.

- [ ] **Step 4: Run** — `go test -count=1 ./network ./rpc` (rpc calls `HandleIncomingBlock` — update the call site in Task 4 first if it fails to compile).
- [ ] **Step 5: Commit** — `git commit -m "network: authenticate blocks against the pinned key or the active validator set"`.

---

### Task 4: rpc — trusted key + resolver on the plaintext ingest path

**Files:**
- Modify: `rpc/server.go`

- [ ] `Config` gains `TrustedProducerKey func() []byte` and `LookupProducerKey network.ProducerKeyLookup`. `handleIncomingBlock` uses them (falls back to `s.validatorPK` when `TrustedProducerKey` is nil, so tests that build a server without the new fields keep working).
- [ ] `go build ./... && go test -count=1 ./rpc` → PASS. Commit with Task 3 or separately.

---

### Task 5: cmd/node — BFT for head+1, trusted key, validator pull-sync, Ready, commit tolerance

**Files:**
- Create: `cmd/node/join.go`, `cmd/node/join_test.go`
- Modify: `cmd/node/main.go`

- [ ] **Step 1: Tests**

```go
package main

func TestBFTChosenForNextHeight(t *testing.T) {
	s := forks.Schedule{forks.MultiValidator: 1}
	if !bftForNextBlock(s, 0) { t.Fatal("head 0 with activation at 1 must run BFT for block 1") }
	if bftForNextBlock(forks.Schedule{forks.MultiValidator: 5}, 3) { t.Fatal("block 4 is before activation") }
	if !bftForNextBlock(forks.Schedule{forks.MultiValidator: 5}, 4) { t.Fatal("block 5 is the first BFT block") }
	if bftForNextBlock(nil, 100) { t.Fatal("nil schedule never activates") }
}

func TestResolveTrustedProducerKey(t *testing.T) {
	// flag wins for validators and full nodes; validators fall back to their own key; full nodes to nil
}

func TestGenesisValidatorInfo(t *testing.T) {
	// block 0 present → its validator; absent → trusted key; neither → ok=false
}

func TestApplyCommittedBlockToleratesAlreadyApplied(t *testing.T) {
	// produce block 1 on ex; applyCommittedBlock(ex, b1) == nil; a different block at 1 → error
}
```

- [ ] **Step 2: Implement** `cmd/node/join.go`:

```go
func bftForNextBlock(s forks.Schedule, head uint64) bool { return s.Active(forks.MultiValidator, head+1) }

func resolveTrustedProducerKey(flagHex string, isValidator bool, localPK []byte) ([]byte, error)

func genesisValidatorInfo(ex *block.Executor, trustedPK []byte) (consensus.ValidatorInfo, bool)

func applyCommittedBlock(ex *block.Executor, b *types.Block) error {
	err := ex.ExecuteBlock(b)
	if err == nil { return nil }
	if head, herr := ex.LatestBlock(); herr == nil && head != nil && head.Hash == b.Hash { return nil }
	return err
}
```

`main.go` wiring: `trustedPK` replaces `validatorPKBytes` in `NewBroadcaster`, `DefaultSyncConfig`, `HandleBlock`, and a new `HandleProposal` verification; `syncCfg.LookupProducerKey = executor.ActiveValidatorKey`; `useBFT := bftForNextBlock(executor.Forks(), head)`; `vCfg.GenesisValidator`, `vCfg.Ready = syncer.IsSynced`; `OnCommit: applyCommittedBlock`; the periodic sync loop runs when `!flagIsValidator || useBFT`.

- [ ] **Step 3:** `go build ./... && go test -count=1 ./cmd/node` → PASS. Commit: `node: pick BFT for the block to be produced, honour the pinned key on validators, pull-sync validators before they propose`.

---

### Task 6: core/types + core/state — snapshot wire type, export, import

**Files:**
- Create: `core/types/state_snapshot.go`, `core/state/export_snapshot_test.go`
- Modify: `core/state/statedb.go`

- [ ] **Step 1: Tests**

```go
func TestExportSnapshotIsConsistentUnderConcurrentCommit(t *testing.T) {
	// goroutine: for h := 1..200 { mutate account; root := CommitWithHeight(h); record roots[h]=root }
	// main: repeatedly ExportSnapshot(64<<20) and assert SnapshotRoot(leaves) == roots[height]
}
func TestExportSnapshotRefusesWithoutHeightMarker(t *testing.T)
func TestExportSnapshotTooLarge(t *testing.T)
func TestImportSnapshotRoundTripAndRefusals(t *testing.T) {
	// export from A; import into fresh B → same root, AppliedHeight; refuse: wrong root (nothing written), non-empty state, pending writes
}
```

- [ ] **Step 2: Implement**

```go
// core/types/state_snapshot.go
type StateLeaf struct { Key []byte `json:"key"`; Value []byte `json:"value"` }
type StateSnapshot struct { Height uint64 `json:"height"`; Block *Block `json:"block"`; Leaves []StateLeaf `json:"leaves"` }

// core/state/statedb.go
var ErrSnapshotTooLarge = errors.New("state: snapshot exceeds the size cap")
var ErrSnapshotUnavailable = errors.New("state: no applied-height marker, cannot serve a snapshot")
var ErrStateNotFresh = errors.New("state: snapshot import needs an empty state")
var ErrSnapshotRootMismatch = errors.New("state: snapshot leaves do not hash to the expected root")

func (s *StateDB) ExportSnapshot(maxBytes int) (uint64, []types.StateLeaf, error) // db.GetSnapshot(); read meta:height; iterate skipping meta:; cap
func SnapshotRoot(leaves []types.StateLeaf) [32]byte                              // merkleRoot over key→Hash32(value)
func (s *StateDB) ImportSnapshot(leaves []types.StateLeaf, height uint64, wantRoot [32]byte) error
```

- [ ] **Step 3:** `go test -count=1 ./core/state` → PASS. Commit: `state: export a consistent live snapshot and import one into a fresh state`.

---

### Task 7: core/block — executor export/import

**Files:**
- Modify: `core/block/executor.go`
- Test: `core/block/state_snapshot_test.go`

- [ ] Tests: `TestImportStateSnapshotRoundTrip` (A: 3 blocks → export → B import → B applies A's block 4), `TestImportStateSnapshotRefusesRootMismatch` (tampered leaf → chain and state untouched), `TestImportStateSnapshotRefusesNonEmptyChain`, `TestImportStateSnapshotRefusesNonEmptyState`, `TestExportStateSnapshotCarriesHeadBlock`.
- [ ] Implement:

```go
const MaxStateSnapshotBytes = 64 << 20

func (e *Executor) ExportStateSnapshot() (*types.StateSnapshot, error) // under mu? No: export reads a DB snapshot; only chain DB read for the block. Take e.mu to keep head/state read ordered.
func (e *Executor) ImportStateSnapshot(snap *types.StateSnapshot) error // mu; chain empty; snap.Block non-nil, Validate, height match, chain id; root check BEFORE storeBlock; storeBlock; state.ImportSnapshot
```

- [ ] `go test -count=1 ./core/block` → PASS. Commit: `block: export and import a state snapshot anchored to its head block`.

---

### Task 8: network + rpc — sealed snapshot read and fetch

**Files:**
- Modify: `network/p2p_secure.go`, `network/sync.go`, `rpc/p2p_routes.go`
- Test: `rpc/p2p_sealed_sync_test.go` (`TestSealedStateSnapshotRead`, `TestFetchStateSnapshotRefusesUnanchoredHead`), `network/join_test.go` (`TestStateSyncTriesPeersInOrder`)

- [ ] Implement: `KindGetStateSnapshot = "get_state_snapshot"` in `IsReadKind`; `P2PConfig.ServeStateSnapshot func() ([]byte, error)`; `serveSealedRead` case; `IdentityCache.QuerySealedTimeout`; `BlockFetcher.FetchStateSnapshot(peer)`; `ErrSnapshotNotAnchored`; `Syncer.StateSync(importer func(*types.StateSnapshot) error) error`.
- [ ] `go test -count=1 ./network ./rpc` → PASS. Commit: `p2p: serve and fetch a state snapshot over the sealed read plane`.

---

### Task 9: cmd/node — `--state-sync`

**Files:**
- Modify: `cmd/node/main.go`, `cmd/node/join.go`
- Test: `cmd/node/join_test.go` (`TestStateSyncFlagValidation`)

- [ ] `validateStateSyncFlags(isValidator bool, trustedPK []byte, genesisPin *[32]byte) error`.
- [ ] Flow on an empty chain with `--state-sync`: skip allocation apply and genesis minting; `prepareStateSyncGenesis` (local `--genesis` verified with the doc pin, else peer `/genesis` pinned) → `persistForkSchedule` + `PutMeta(native_minted)`; goroutine loop `syncer.StateSync(executor.ImportStateSnapshot)` every `RetryDelay` until success; `syncGate atomic.Bool` holds the periodic `SyncOnce` loop until then.
- [ ] `ServeStateSnapshot` in `RegisterP2PRoutes` with a single-flight guard.
- [ ] `go build ./... && go test -count=1 ./cmd/node` → PASS. Commit: `node: --state-sync joins an unreplayable chain from a peer's live snapshot`.

---

### Task 10: Verification

- [ ] `go build ./... && go vet ./... && gofmt -l . && go test -count=1 ./...` → all green, `gofmt -l .` prints nothing.
- [ ] Local two-node run from a temp dir: producer with `genesis-local.json` (validator key filled in), joiner as follower then as validator; register the joiner; observe 2-vote blocks on both nodes. Record the commands in the report.
