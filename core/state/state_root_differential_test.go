package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// diskRoot recomputes the state root from what LevelDB durably holds, with the
// reference merkleRoot. It shares nothing with the in-memory index, so it is
// the independent oracle every cached root is checked against.
func diskRoot(t testing.TB, sdb *StateDB) [32]byte {
	t.Helper()
	iter := sdb.db.NewIterator(nil, nil)
	defer iter.Release()
	idx := make(map[string][32]byte)
	for iter.Next() {
		k := string(iter.Key())
		if strings.HasPrefix(k, prefixMeta) {
			continue
		}
		idx[k] = qcrypto.Hash32(iter.Value())
	}
	if err := iter.Error(); err != nil {
		t.Fatal(err)
	}
	return merkleRoot(idx)
}

// rootModel is what a StateDB should hold, as raw leaf values: the committed
// leaves and the pending overlay above them.
type rootModel struct {
	committed map[string][]byte
	pending   map[string][]byte
}

func (m *rootModel) root(withPending bool) [32]byte {
	idx := make(map[string][32]byte, len(m.committed)+len(m.pending))
	for k, v := range m.committed {
		idx[k] = qcrypto.Hash32(v)
	}
	if withPending {
		for k, v := range m.pending {
			idx[k] = qcrypto.Hash32(v)
		}
	}
	return merkleRoot(idx)
}

func (m *rootModel) commit() {
	for k, v := range m.pending {
		m.committed[k] = v
	}
	m.pending = map[string][]byte{}
}

func copyValues(src map[string][]byte) map[string][]byte {
	dst := make(map[string][]byte, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// rootFuzz drives one StateDB through random writes, reverts, discards,
// commits, failed commits, reopens and snapshot imports, and after every step
// checks each root the StateDB reports against the model and against disk.
type rootFuzz struct {
	t      *testing.T
	rng    *rand.Rand
	dir    string
	sdb    *StateDB
	m      *rootModel
	height uint64

	contracts []qcrypto.Address
	accounts  []qcrypto.Address
	slots     map[string]struct {
		contract qcrypto.Address
		slot     [32]byte
	}
}

func newRootFuzz(t *testing.T, seed int64) *rootFuzz {
	f := &rootFuzz{
		t:   t,
		rng: rand.New(rand.NewSource(seed)),
		dir: t.TempDir(),
		m:   &rootModel{committed: map[string][]byte{}, pending: map[string][]byte{}},
		slots: map[string]struct {
			contract qcrypto.Address
			slot     [32]byte
		}{},
	}
	sdb, err := NewStateDB(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	f.sdb = sdb
	for i := 0; i < 6; i++ {
		f.contracts = append(f.contracts, f.randAddr())
	}
	for i := 0; i < 40; i++ {
		f.accounts = append(f.accounts, f.randAddr())
	}
	return f
}

func (f *rootFuzz) randAddr() qcrypto.Address {
	var a qcrypto.Address
	f.rng.Read(a[:])
	return a
}

func (f *rootFuzz) randBytes(max int) []byte {
	b := make([]byte, f.rng.Intn(max+1))
	f.rng.Read(b)
	return b
}

// write stages one random leaf write, an insert or an update, on the StateDB
// and the model alike.
func (f *rootFuzz) write() {
	switch r := f.rng.Intn(10); {
	case r < 5:
		c := f.contracts[f.rng.Intn(len(f.contracts))]
		var slot [32]byte
		slot[31] = byte(f.rng.Intn(48))
		v := f.randBytes(40)
		if err := f.sdb.SetStorageValue(c, slot, v); err != nil {
			f.t.Fatal(err)
		}
		k := storageKey(c, slot)
		f.m.pending[k] = append([]byte(nil), v...)
		f.slots[k] = struct {
			contract qcrypto.Address
			slot     [32]byte
		}{c, slot}
	case r < 8:
		acc := types.NewAccount(f.accounts[f.rng.Intn(len(f.accounts))])
		acc.AddBalance(big.NewInt(f.rng.Int63n(1_000_000)))
		acc.Nonce = uint64(f.rng.Intn(5))
		f.sdb.SetAccount(acc)
		data, err := json.Marshal(acc.Copy())
		if err != nil {
			f.t.Fatal(err)
		}
		f.m.pending[prefixAccount+acc.Address.String()] = data
	case r < 9:
		code := f.randBytes(64)
		h := qcrypto.Hash32(code)
		f.sdb.SetCode(h, code)
		f.m.pending[prefixCode+string(h[:])] = code
	default:
		var nk [32]byte
		nk[0] = byte(f.rng.Intn(16))
		if err := f.sdb.MarkX402NonceUsed(nk, 7); err != nil {
			f.t.Fatal(err)
		}
		f.m.pending[prefixX402Nonce+string(nk[:])] = []byte("7")
	}
}

// rewriteSame stages a write of a committed storage leaf's own value: a change
// set entry that leaves the root where it is.
func (f *rootFuzz) rewriteSame() {
	for k, s := range f.slots {
		v, ok := f.m.committed[k]
		if !ok {
			continue
		}
		if _, pend := f.m.pending[k]; pend {
			continue
		}
		if err := f.sdb.SetStorageValue(s.contract, s.slot, v); err != nil {
			f.t.Fatal(err)
		}
		f.m.pending[k] = v
		return
	}
}

func (f *rootFuzz) checkCommitted(step string) {
	f.t.Helper()
	want := f.m.root(false)
	got, err := f.sdb.StateRoot()
	if err != nil {
		f.t.Fatalf("%s: StateRoot: %v", step, err)
	}
	if got != want {
		f.t.Fatalf("%s: StateRoot %x, model %x (%d leaves)", step, got[:8], want[:8], len(f.m.committed))
	}
	if v := f.sdb.CommittedView(); v.Root != want {
		f.t.Fatalf("%s: CommittedView root %x, model %x", step, v.Root[:8], want[:8])
	}
	addr := f.accounts[f.rng.Intn(len(f.accounts))]
	if _, ok := f.m.committed[prefixAccount+addr.String()]; ok {
		p, err := f.sdb.AccountProofContext(context.Background(), addr)
		if err != nil {
			f.t.Fatalf("%s: AccountProof: %v", step, err)
		}
		if p.Root != want {
			f.t.Fatalf("%s: proof root %x, model %x", step, p.Root[:8], want[:8])
		}
		if err := VerifyAccountProof(p); err != nil {
			f.t.Fatalf("%s: proof does not verify: %v", step, err)
		}
	}
}

func (f *rootFuzz) checkDisk(step string) {
	f.t.Helper()
	if got, want := diskRoot(f.t, f.sdb), f.m.root(false); got != want {
		f.t.Fatalf("%s: disk root %x, model %x", step, got[:8], want[:8])
	}
}

func (f *rootFuzz) commit(step string) {
	f.t.Helper()
	f.height++
	got, err := f.sdb.CommitWithHeight(f.height)
	if err != nil {
		f.t.Fatalf("%s: commit: %v", step, err)
	}
	want := f.m.root(true)
	f.m.commit()
	if got != want {
		f.t.Fatalf("%s: commit returned %x, model %x", step, got[:8], want[:8])
	}
	f.checkDisk(step)
}

func (f *rootFuzz) leaves() []types.StateLeaf {
	out := make([]types.StateLeaf, 0, len(f.m.committed))
	for k, v := range f.m.committed {
		out = append(out, types.StateLeaf{Key: []byte(k), Value: v})
	}
	return out
}

// importInto moves the fuzz onto a fresh StateDB built from the model's
// committed leaves, by ImportSnapshot or by the JSONL path.
func (f *rootFuzz) importInto(jsonl bool) {
	f.t.Helper()
	root := f.m.root(false)
	dir := f.t.TempDir()
	dst, err := NewStateDB(dir)
	if err != nil {
		f.t.Fatal(err)
	}
	f.height++
	if jsonl {
		path := filepath.Join(f.t.TempDir(), "leaves.jsonl")
		var buf []byte
		for _, l := range f.leaves() {
			line, err := json.Marshal(l)
			if err != nil {
				f.t.Fatal(err)
			}
			buf = append(append(buf, line...), '\n')
		}
		if err := os.WriteFile(path, buf, 0o600); err != nil {
			f.t.Fatal(err)
		}
		err = dst.ImportSnapshotJSONL(path, f.height, root)
	} else {
		err = dst.ImportSnapshot(f.leaves(), f.height, root)
	}
	if err != nil {
		f.t.Fatalf("import (jsonl=%v, %d leaves): %v", jsonl, len(f.m.committed), err)
	}
	f.sdb.Close()
	f.sdb, f.dir = dst, dir
	f.m.pending = map[string][]byte{}
	f.checkDisk("after import")
}

func (f *rootFuzz) step(i int) {
	f.t.Helper()
	switch r := f.rng.Intn(100); {
	case r < 40:
		f.write()
	case r < 44:
		f.rewriteSame()
	case r < 52:
		// Writes inside a snapshot that is reverted leave no trace.
		before := copyValues(f.m.pending)
		id := f.sdb.Snapshot()
		for n := f.rng.Intn(3) + 1; n > 0; n-- {
			f.write()
		}
		if err := f.sdb.RevertToSnapshot(id); err != nil {
			f.t.Fatal(err)
		}
		f.m.pending = before
	case r < 64:
		got, err := f.sdb.PendingStateRoot()
		if err != nil {
			f.t.Fatal(err)
		}
		if want := f.m.root(true); got != want {
			f.t.Fatalf("step %d: PendingStateRoot %x, model %x", i, got[:8], want[:8])
		}
	case r < 69:
		f.sdb.DiscardPending()
		f.m.pending = map[string][]byte{}
	case r < 83:
		f.commit("commit")
	case r < 86:
		// A failed state write changes nothing; the retry commits it all.
		f.sdb.FailNextCommit(errors.New("injected"))
		if _, err := f.sdb.Commit(); err == nil {
			f.t.Fatalf("step %d: injected commit failure did not fail", i)
		}
		f.checkCommitted("after failed commit")
		f.checkDisk("after failed commit")
		f.commit("retry after failed commit")
	case r < 90:
		if err := f.sdb.Close(); err != nil {
			f.t.Fatal(err)
		}
		sdb, err := NewStateDB(f.dir)
		if err != nil {
			f.t.Fatal(err)
		}
		f.sdb = sdb
		f.m.pending = map[string][]byte{}
	case r < 95:
		if len(f.m.committed) > 0 {
			f.importInto(false)
		}
	default:
		if len(f.m.committed) > 0 {
			f.importInto(true)
		}
	}
	f.checkCommitted("step")
}

// Every root the StateDB reports — pending, committed, per-view and in proofs —
// must equal the reference Merkle root of the model after any sequence of
// writes, reverts, discards, commits, failed commits, reopens and imports.
func TestStateRootDifferential(t *testing.T) {
	run := func(t *testing.T, seed int64) {
		f := newRootFuzz(t, seed)
		for i := 0; i < 700; i++ {
			f.step(i)
		}
		f.commit("final")
		f.sdb.Close()
		if len(f.m.committed) < 50 {
			t.Fatalf("the run only reached %d leaves", len(f.m.committed))
		}
	}
	// Tiny chunks put every size of a run across the chunk boundaries of the
	// parallel reduction; 0 keeps the shipping configuration.
	for _, c := range []struct {
		seed           int64
		chunk, workers int
	}{{1, 0, 0}, {2, 0, 0}, {3, 2, 3}, {3, 8, 4}, {4, 2, 3}, {4, 8, 4}} {
		t.Run(fmt.Sprintf("seed=%d/chunk=%d/workers=%d", c.seed, c.chunk, c.workers), func(t *testing.T) {
			if c.chunk == 0 {
				run(t, c.seed)
				return
			}
			withRootConfig(t, c.chunk, c.workers, func() { run(t, c.seed) })
		})
	}
}
